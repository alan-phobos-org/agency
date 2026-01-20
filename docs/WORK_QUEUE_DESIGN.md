# Work Queue Design

This document describes the design for a task queue system that allows work to be queued for execution by agents.

**Related docs:**
- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Scheduler architecture (cron-style triggering)
- [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) - State synchronization

---

## Problem Statement

Currently, agents can only execute one task at a time and reject subsequent requests with HTTP 409 (Conflict). This creates several issues:

1. **Work loss** - Tasks submitted to busy agents are rejected, not queued
2. **No retry semantics** - Callers must implement their own retry logic
3. **No visibility** - No way to see pending work or manage queue depth
4. **Manual load balancing** - Callers must find idle agents themselves

### Current Flow (Rejected)

```
Scheduler                     Agent (busy)
    |                             |
    |   POST /task                |
    |-------------------------->  |
    |   409 Conflict              |
    |  <--------------------------| (task lost)
    |                             |
```

### Desired Flow (Queued)

```
Scheduler                Queue                   Agent
    |                      |                       |
    |   POST /queue/task   |                       |
    |--------------------> | (task queued)         |
    |   201 {queue_id}     |                       |
    |  <------------------ |                       |
    |                      |   (agent idle)        |
    |                      |   dispatch task       |
    |                      |---------------------->|
    |                      |   task completes      |
    |                      |<----------------------|
```

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Persistence** | JSON file-based | Simple, survives restarts, mirrors history pattern |
| **Ordering** | FIFO (no priority) | Simple, predictable, priority not needed initially |
| **Agent Selection** | First available | Simple, deterministic |
| **Queue Limit** | Reject at 50 tasks | Prevent unbounded growth, clear backpressure |
| **TTL** | None | Tasks wait indefinitely, simple model |

---

## Architecture

### System Overview

The queue lives in the **Web Director** component. All task submitters (Web UI, Scheduler, CLI) route through the queue. The dispatcher finds idle agents and dispatches pending tasks.

```
                 +--------------------------------------------------------+
                 |                  Web Director (8443)                   |
                 |  +------------------+  +-----------------------------+ |
                 |  |   SessionStore   |  |         WorkQueue           | |
                 |  | (active sessions)|  | (pending tasks, max 50)     | |
                 |  +------------------+  +-----------------------------+ |
                 |                               |                        |
                 |  +---------------------------+|+---------------------+ |
                 |  |      Queue API            |||    Dispatcher       | |
                 |  | POST /api/queue/task      ||| (polls every 1s)    | |
                 |  | GET  /api/queue           |||                     | |
                 |  | GET  /api/queue/{id}      ||| FindFirstIdleAgent  | |
                 |  | POST /api/queue/{id}/cancel|| SubmitTask          | |
                 |  +---------------------------+|+---------------------+ |
                 +--------------------------------------------------------+
                        ^         ^         ^              |
                        |         |         |              v
         +--------------+-+ +-----+-----+ +-+--------+ +-------+
         |    Scheduler   | |  Web UI   | |   CLI    | | Agent |
         |                | | (browser) | | ag-cli   | |       |
         +----------------+ +-----------+ +----------+ +-------+
```

### Component Responsibilities

| Component | Responsibility |
|-----------|----------------|
| **WorkQueue** | Stores pending tasks, enforces FIFO, persists to JSON files |
| **Dispatcher** | Background loop that finds idle agents and dispatches tasks |
| **Queue API** | HTTP endpoints for queue operations |
| **Submitters** | Web UI, Scheduler, CLI - all submit via queue API |

---

## New Task States

Extend the existing `TaskState` enum with queue-specific states:

```go
// Existing states (in agent)
TaskStateQueued    TaskState = "queued"     // Created, not yet started
TaskStateWorking   TaskState = "working"    // Currently executing
TaskStateCompleted TaskState = "completed"  // Finished successfully
TaskStateFailed    TaskState = "failed"     // Finished with error
TaskStateCancelled TaskState = "cancelled"  // User cancelled

// New states for queue management (in director)
TaskStatePending     TaskState = "pending"     // In queue, waiting for agent
TaskStateDispatching TaskState = "dispatching" // Being sent to agent
```

### State Transitions

```
                              +-------------+
     task submitted --------> |   pending   |
                              +------+------+
                                     |
                      agent available|
                                     v
                              +------+------+
                              |dispatching  |
                              +------+------+
                                     |
                     /---------------+---------------\
                     |               |               |
                  success        agent busy       failure
                     |           (409)              |
                     v               v              v
              +------+------+ +------+------+ +----+------+
              |   working   | |  pending    | |  failed   |
              +------+------+ | (back of Q) | +-----------+
                     |        +-------------+
              /------+------\
              |             |
         completed       failed
              |             |
              v             v
       +------+------+ +----+------+
       |  completed  | |  failed   |
       +-------------+ +-----------+
```

---

## Data Model

### QueuedTask

```go
// QueuedTask represents a task waiting in the queue
type QueuedTask struct {
    QueueID      string            `json:"queue_id"`       // Unique queue entry ID
    State        TaskState         `json:"state"`          // pending, dispatching, working, etc.
    CreatedAt    time.Time         `json:"created_at"`     // Queue entry time

    // Original request
    Prompt         string            `json:"prompt"`
    Model          string            `json:"model,omitempty"`
    TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
    SessionID      string            `json:"session_id,omitempty"`
    Project        *api.ProjectContext `json:"project,omitempty"`
    Env            map[string]string `json:"env,omitempty"`
    Thinking       *bool             `json:"thinking,omitempty"`

    // Dispatch tracking
    DispatchedAt *time.Time `json:"dispatched_at,omitempty"` // When sent to agent
    TaskID       string     `json:"task_id,omitempty"`       // Agent's task ID (once dispatched)
    AgentURL     string     `json:"agent_url,omitempty"`     // Target agent (once dispatched)
    Attempts     int        `json:"attempts"`                // Dispatch attempt count
    LastError    string     `json:"last_error,omitempty"`    // Most recent error

    // Source tracking
    Source    string `json:"source"`              // "web", "scheduler", "cli"
    SourceJob string `json:"source_job,omitempty"` // Job name (if scheduler)
}
```

### WorkQueue

```go
// WorkQueue manages pending tasks with file-based persistence
type WorkQueue struct {
    mu       sync.RWMutex
    tasks    []*QueuedTask           // FIFO order
    byID     map[string]*QueuedTask  // Quick lookup by queue_id
    dir      string                  // Persistence directory
    config   QueueConfig
}

// QueueConfig defines queue behavior
type QueueConfig struct {
    MaxSize         int           // Maximum queue depth (default: 50)
    MaxAttempts     int           // Retry limit per task (default: 3)
    DispatchTimeout time.Duration // Time to wait for agent response (default: 30s)
}

const (
    DefaultMaxSize     = 50
    DefaultMaxAttempts = 3
    DefaultDispatchTimeout = 30 * time.Second
)
```

---

## Persistence

### File Structure

Queue state persists to JSON files, mirroring the history storage pattern:

```
~/.agency/queue/
    pending/
        queue-abc12345.json    # Task waiting to be dispatched
        queue-def67890.json
    dispatched/
        queue-xyz11111.json    # Task sent to agent, awaiting completion
```

### File Format

Each task is stored as a single JSON file:

```json
{
  "queue_id": "queue-abc12345",
  "state": "pending",
  "created_at": "2025-01-17T10:00:00Z",
  "prompt": "Perform nightly maintenance...",
  "model": "sonnet",
  "timeout_seconds": 1800,
  "source": "scheduler",
  "source_job": "nightly-maintenance",
  "attempts": 0
}
```

### Persistence Operations

```go
// Save writes task to appropriate directory based on state
func (q *WorkQueue) save(task *QueuedTask) error {
    dir := "pending"
    if task.State == TaskStateDispatching || task.State == TaskStateWorking {
        dir = "dispatched"
    }
    path := filepath.Join(q.dir, dir, task.QueueID+".json")
    data, _ := json.MarshalIndent(task, "", "  ")
    return os.WriteFile(path, data, 0644)
}

// Remove deletes task file (on completion/cancellation)
func (q *WorkQueue) remove(task *QueuedTask) error {
    // Try both directories
    for _, dir := range []string{"pending", "dispatched"} {
        path := filepath.Join(q.dir, dir, task.QueueID+".json")
        os.Remove(path)
    }
    return nil
}
```

### Recovery on Startup

```go
func (q *WorkQueue) loadFromDisk() error {
    // 1. Load dispatched tasks first
    dispatchedDir := filepath.Join(q.dir, "dispatched")
    files, _ := os.ReadDir(dispatchedDir)
    for _, f := range files {
        task := loadTask(filepath.Join(dispatchedDir, f.Name()))

        // Check if agent still has this task
        if !q.agentHasTask(task.AgentURL, task.TaskID) {
            // Task lost - move back to pending
            task.State = TaskStatePending
            task.TaskID = ""
            task.AgentURL = ""
            task.DispatchedAt = nil
            q.moveToDir(task, "pending")
        }
        q.tasks = append(q.tasks, task)
        q.byID[task.QueueID] = task
    }

    // 2. Load pending tasks
    pendingDir := filepath.Join(q.dir, "pending")
    files, _ = os.ReadDir(pendingDir)
    for _, f := range files {
        task := loadTask(filepath.Join(pendingDir, f.Name()))
        q.tasks = append(q.tasks, task)
        q.byID[task.QueueID] = task
    }

    // 3. Sort by created_at for FIFO
    sort.Slice(q.tasks, func(i, j int) bool {
        return q.tasks[i].CreatedAt.Before(q.tasks[j].CreatedAt)
    })

    return nil
}
```

---

## Queue API

The queue API surface (submit, status, detail, cancel) is specified in [REFERENCE.md](REFERENCE.md). This document focuses on queue behavior and data model rather than duplicating request/response examples.

Key behaviors:
- Submit returns `queue_id`, `position`, and initial `state` (`pending`).
- Queue full returns `503 Service Unavailable`.
- Positions apply only to pending tasks; dispatched tasks include `agent_url` and `task_id`.

---

## Dispatcher

### Dispatcher Loop

The dispatcher runs in the background (1s cadence):
- Find the first idle, healthy agent.
- Pop the next pending task (FIFO).
- Mark task as `dispatching`, submit to agent, then persist agent URL/task ID on success.
- On errors, route to dispatch error handling (requeue or fail).

### Dispatch Error Handling

- 409 from agent: requeue at back (agent raced to busy).
- Retryable errors: increment attempts and return to pending.
- Max attempts reached: mark failed and remove from queue.

### Task Completion Tracking

When a task is dispatched, the queue needs to know when it completes. Two approaches:

**Option A: Poll-based (simpler)**

```go
func (d *Dispatcher) trackCompletion(task *QueuedTask) {
    // Poll agent every 5s for task status
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        status, err := d.getTaskStatus(task.AgentURL, task.TaskID)
        if err != nil {
            continue // Agent unreachable, keep polling
        }

        if status.IsTerminal() {
            d.queue.Remove(task)
            return
        }
    }
}
```

**Option B: Webhook callback (more complex)**

Agent calls back to director when task completes. Requires new agent endpoint.

**Decision:** Use Option A (polling) for simplicity. The dashboard already polls agents, so this adds minimal overhead.

---

## Component Integration

### Web View Integration

The web UI submits tasks through the queue API instead of directly to agents.

#### Current Flow (Direct)

```
Browser                    Web Director                Agent
   |   POST /api/task          |                         |
   |-------------------------> |   POST /task            |
   |                           |-----------------------> |
   |   {task_id, session_id}   |   {task_id, session_id} |
   | <------------------------ | <---------------------- |
```

#### New Flow (Queued)

```
Browser                    Web Director                Agent
   |   POST /api/task          |                         |
   |-------------------------> |                         |
   |                           |   (add to queue)        |
   |   {queue_id, position}    |                         |
   | <------------------------ |                         |
   |                           |                         |
   |    (later, via dispatcher)|                         |
   |                           |   POST /task            |
   |                           |-----------------------> |
   |                           |   {task_id}             |
   |                           | <---------------------- |
```

#### Handler Changes

- `/api/task` should enqueue and return `queue_id`, `position`, and `state` instead of a direct `task_id`.
- `queue_full` returns 503 with a user-facing error.
- Request fields map 1:1 from task submission to queue submission, with `source: web`.

#### Dashboard Changes

Dashboard updates:
1. Show queue depth in the header/status area.
2. Display pending tasks in a Queue panel.
3. Allow cancellation of queued tasks.
4. Update task status as it moves through queue states.

### Scheduler Integration

The scheduler routes all job submissions through the queue.

#### Current Flow

```go
func (s *Scheduler) runJob(js *jobState) {
    // Try director first
    if s.config.DirectorURL != "" {
        taskID, err := s.submitViaDirector(js)
        if err == nil {
            s.updateJobState(js, "submitted", taskID)
            return
        }
    }
    // Fallback to agent
    taskID, err := s.submitViaAgent(js)
    // ...
}
```

#### New Flow (Queue)

- Scheduler submits to `POST /api/queue/task` on `director_url`.
- If `director_url` is missing or queue is full, mark the job as skipped with a specific status.
- On success, store `queue_id` on the job state and report `queued`.

#### Config Changes

- `director_url` is required for queue submission (use the web internal port).
- Keep `agent_url` only as an explicit fallback when the director is unavailable.
- See `configs/scheduler.yaml` for canonical values.

#### Job Status Changes

Update `jobState` to track queue status:

```go
type jobState struct {
    Job        *Job
    Cron       *CronExpr
    mu         sync.RWMutex
    NextRun    time.Time
    LastRun    time.Time
    LastStatus string // "queued", "skipped_queue_full", etc.
    LastQueueID string // Queue ID instead of task ID
    isRunning  bool
}
```

#### Status Endpoint Changes

- `/status` includes `last_queue_id` and queue-related `last_status` values (`queued`, `skipped_queue_full`, etc.).

### CLI Integration

Add queue commands to `ag-cli`:

```bash
# Submit task to queue
ag-cli queue "Task prompt here" --model sonnet --timeout 30m

# Check queue status
ag-cli queue-status

# Get specific queued task
ag-cli queue-status queue-abc12345

# Cancel queued task
ag-cli queue-cancel queue-abc12345
```

#### Implementation

CLI commands are thin wrappers over the queue API; keep argument parsing and output formatting minimal.

---

## Error Handling

### Dispatch Failures

| Scenario | Action |
|----------|--------|
| Agent returns 409 (busy) | Re-queue at back of queue |
| Agent unreachable | Re-queue, increment attempts |
| Agent returns 4xx error | Mark task failed (client error) |
| Agent returns 5xx error | Re-queue, increment attempts |
| Max attempts (3) exceeded | Mark task failed, remove from queue |

### Queue Full

| Submitter | Behavior |
|-----------|----------|
| Web UI | Show error: "Queue is full. Please try again later." |
| Scheduler | Log warning, update job status to "skipped_queue_full" |
| CLI | Exit with error: "Error: queue is at capacity (50 tasks)" |

### Recovery Scenarios

| Scenario | Behavior |
|----------|----------|
| Director restart | Load queue from disk, re-check dispatched tasks |
| Agent restart mid-task | Dispatched task fails, queue removes on next poll |
| Network partition | Dispatch timeout, task re-queued |

---

## Configuration

### Web Director

```yaml
# Environment variables
AG_QUEUE_DIR=~/.agency/queue     # Queue persistence directory
AG_QUEUE_MAX_SIZE=50             # Maximum queue depth (default: 50)
AG_QUEUE_MAX_ATTEMPTS=3          # Retry limit (default: 3)
AG_QUEUE_DISPATCH_TIMEOUT=30s    # Agent response timeout (default: 30s)
```

### Scheduler

Scheduler submits via `director_url` (internal web port) to `POST /api/queue/task`. Keep `agent_url` only as an explicit fallback when the director is unavailable. See `configs/scheduler.yaml` and [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md).

---

## Observability

### Logging

```
2025-01-17T10:00:00Z INFO queue=task_added queue_id=queue-abc12345 depth=5 source=scheduler
2025-01-17T10:00:01Z INFO queue=dispatch queue_id=queue-abc12345 agent=https://localhost:9000
2025-01-17T10:00:01Z INFO queue=dispatch_success queue_id=queue-abc12345 task_id=task-xyz789
2025-01-17T10:00:00Z WARN queue=dispatch_failed queue_id=queue-def67890 error="agent busy" attempt=1/3
2025-01-17T10:00:00Z WARN queue=dispatch_failed queue_id=queue-def67890 error="timeout" attempt=3/3 final=true
2025-01-17T10:00:00Z WARN queue=submit_rejected reason=queue_full depth=50
```

### Status Endpoint

Extend `/status` to include queue info:

```json
{
  "type": "view",
  "interfaces": ["statusable", "observable", "taskable"],
  "version": "1.0.0",
  "queue": {
    "depth": 5,
    "max_size": 50,
    "oldest_age_seconds": 300,
    "dispatched_count": 2
  }
}
```

---

## Testing Strategy

### Unit Tests

```go
// internal/view/web/queue_test.go

func TestQueueAdd(t *testing.T) {
    q := NewWorkQueue(t.TempDir(), QueueConfig{MaxSize: 50})

    task, pos, err := q.Add(QueueSubmitRequest{Prompt: "test"})
    require.NoError(t, err)
    require.Equal(t, 1, pos)
    require.Equal(t, TaskStatePending, task.State)
}

func TestQueueFIFO(t *testing.T) {
    q := NewWorkQueue(t.TempDir(), QueueConfig{MaxSize: 50})

    q.Add(QueueSubmitRequest{Prompt: "first"})
    q.Add(QueueSubmitRequest{Prompt: "second"})
    q.Add(QueueSubmitRequest{Prompt: "third"})

    task := q.NextPending()
    require.Equal(t, "first", task.Prompt)
}

func TestQueueMaxSize(t *testing.T) {
    q := NewWorkQueue(t.TempDir(), QueueConfig{MaxSize: 2})

    q.Add(QueueSubmitRequest{Prompt: "1"})
    q.Add(QueueSubmitRequest{Prompt: "2"})
    _, _, err := q.Add(QueueSubmitRequest{Prompt: "3"})

    require.ErrorIs(t, err, ErrQueueFull)
}

func TestQueuePersistence(t *testing.T) {
    dir := t.TempDir()

    // Add task
    q1 := NewWorkQueue(dir, QueueConfig{MaxSize: 50})
    q1.Add(QueueSubmitRequest{Prompt: "persistent"})

    // Reload from disk
    q2 := NewWorkQueue(dir, QueueConfig{MaxSize: 50})
    require.Equal(t, 1, q2.Depth())

    task := q2.NextPending()
    require.Equal(t, "persistent", task.Prompt)
}
```

### Integration Tests

```go
// internal/view/web/queue_integration_test.go

func TestQueueSubmitAndDispatch(t *testing.T) {
    // 1. Start mock agent (initially busy)
    agent := httptest.NewServer(mockBusyAgent())
    defer agent.Close()

    // 2. Start director with queue
    director := startTestDirector(t, agent.URL)
    defer director.Close()

    // 3. Submit task
    resp := postJSON(t, director.URL+"/api/queue/task", map[string]string{
        "prompt": "test task",
    })
    require.Equal(t, http.StatusCreated, resp.StatusCode)

    var result struct{ QueueID string }
    json.NewDecoder(resp.Body).Decode(&result)

    // 4. Verify task is pending
    status := getJSON(t, director.URL+"/api/queue/"+result.QueueID)
    require.Equal(t, "pending", status["state"])

    // 5. Make agent idle
    agent.Config.Handler = mockIdleAgent()

    // 6. Wait for dispatch
    testutil.Eventually(t, 5*time.Second, func() bool {
        status := getJSON(t, director.URL+"/api/queue/"+result.QueueID)
        return status["state"] == "working"
    })
}

func TestSchedulerQueueIntegration(t *testing.T) {
    // 1. Start director, agent, scheduler
    agent := startTestAgent(t)
    director := startTestDirector(t, agent.URL)
    scheduler := startTestScheduler(t, director.InternalURL)

    // 2. Make agent busy
    submitTask(t, agent.URL, "blocking task")

    // 3. Trigger scheduler job
    triggerJob(t, scheduler.URL, "test-job")

    // 4. Verify task queued (not rejected)
    status := getSchedulerStatus(t, scheduler.URL)
    require.Equal(t, "queued", status.Jobs[0].LastStatus)

    // 5. Wait for agent to complete
    testutil.Eventually(t, 30*time.Second, func() bool {
        return getAgentState(t, agent.URL) == "idle"
    })

    // 6. Verify queued task dispatched
    queueStatus := getQueueStatus(t, director.URL)
    require.Equal(t, 0, queueStatus.Depth) // Queue empty
}
```

### System Tests

```go
// tests/system/queue_test.go

func TestFullQueueFlow(t *testing.T) {
    // Uses real binaries, not mocks

    // 1. Build and start all components
    buildAll(t)
    agent := startAgent(t)
    director := startDirector(t)
    scheduler := startScheduler(t)

    // 2. Submit multiple tasks while agent is working
    // 3. Verify queue grows
    // 4. Verify tasks dispatched in order as agent becomes idle
    // 5. Verify all tasks complete
}
```

---

## Implementation Plan

### Phase 1: Core Queue

1. Add `QueuedTask` and `WorkQueue` types to `internal/view/web/`
2. Implement file-based persistence
3. Implement queue API endpoints (`/api/queue/*`)
4. Implement dispatcher loop
5. Tests: unit + integration

### Phase 2: Component Integration

1. Update web UI to use queue API
2. Add queue panel to dashboard
3. Update scheduler to use queue API
4. Update CLI with queue commands
5. Tests: integration + system

### Phase 3: Polish

1. Recovery logic on startup
2. Logging and observability
3. Documentation updates
4. End-to-end system tests

---

## Migration Path

### Backward Compatibility

The direct `/api/task` endpoint is replaced by queue-based submission:

| Before | After |
|--------|-------|
| `POST /api/task` returns `task_id` immediately | `POST /api/task` returns `queue_id`, task dispatched async |

For components that need immediate task IDs (e.g., session tracking), they poll `/api/queue/{id}` until the task is dispatched.

### Scheduler Migration

1. Add `director_url` to scheduler config (already exists in `configs/scheduler.yaml`)
2. Remove `agent_url` from scheduler config
3. Scheduler automatically uses queue API

### No Fallback

Queue submission prefers the director. If the director is unavailable and `agent_url` is configured, fallback to direct agent submission is allowed but bypasses the queue and session tracking.

Rationale: Simpler model, guaranteed queue visibility, no split-brain scenarios.

---

## Related Documents

- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Cron-style scheduling
- [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) - State synchronization
- [REFERENCE.md](REFERENCE.md) - API specifications
