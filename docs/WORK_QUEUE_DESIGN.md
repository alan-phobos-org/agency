# Work Queue Design

This document describes the design for a task queue system that allows work to be queued for execution by agents.

**Related docs:**
- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Scheduler architecture (cron-style triggering)
- [SESSION_ROUTING_DESIGN.md](SESSION_ROUTING_DESIGN.md) - Centralized session routing
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
         |     (9100)     | | (browser) | | ag-cli   | | (9000)|
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

### Submit Task to Queue

**POST /api/queue/task**

Submit a task to the queue. Returns immediately with queue position.

**Request:**
```json
{
  "prompt": "Task prompt here",
  "model": "sonnet",
  "timeout_seconds": 1800,
  "session_id": "optional-session-id",
  "source": "scheduler",
  "source_job": "nightly-maintenance"
}
```

**Response (201 Created):**
```json
{
  "queue_id": "queue-abc12345",
  "position": 3,
  "state": "pending"
}
```

**Error Responses:**
- `400 Bad Request` - Invalid request (missing prompt)
- `503 Service Unavailable` - Queue full (50 tasks)

```go
func (h *Handlers) HandleQueueSubmit(w http.ResponseWriter, r *http.Request) {
    var req QueueSubmitRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        api.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
        return
    }

    if req.Prompt == "" {
        api.WriteError(w, http.StatusBadRequest, "validation_error", "prompt is required")
        return
    }

    task, position, err := h.queue.Add(req)
    if err == ErrQueueFull {
        api.WriteError(w, http.StatusServiceUnavailable, "queue_full",
            fmt.Sprintf("Queue is at capacity (%d tasks)", DefaultMaxSize))
        return
    }

    api.WriteJSON(w, http.StatusCreated, map[string]interface{}{
        "queue_id": task.QueueID,
        "position": position,
        "state":    task.State,
    })
}
```

### Get Queue Status

**GET /api/queue**

Returns queue state and pending tasks.

**Response:**
```json
{
  "depth": 5,
  "max_size": 50,
  "oldest_age_seconds": 300,
  "tasks": [
    {
      "queue_id": "queue-abc12345",
      "state": "pending",
      "position": 1,
      "created_at": "2025-01-17T10:00:00Z",
      "prompt_preview": "Perform nightly maintenance...",
      "source": "scheduler"
    },
    {
      "queue_id": "queue-def67890",
      "state": "dispatching",
      "position": 2,
      "created_at": "2025-01-17T10:01:00Z",
      "prompt_preview": "Review pull request...",
      "source": "web"
    }
  ]
}
```

### Get Queued Task Status

**GET /api/queue/{queue_id}**

Returns detailed status of a queued task.

**Response (pending):**
```json
{
  "queue_id": "queue-abc12345",
  "state": "pending",
  "position": 2,
  "created_at": "2025-01-17T10:00:00Z",
  "attempts": 0,
  "source": "scheduler"
}
```

**Response (dispatched/working):**
```json
{
  "queue_id": "queue-abc12345",
  "state": "working",
  "task_id": "task-xyz789",
  "agent_url": "http://localhost:9000",
  "dispatched_at": "2025-01-17T10:05:00Z",
  "source": "scheduler"
}
```

**Response (404):** Task not found (completed or never existed)

### Cancel Queued Task

**POST /api/queue/{queue_id}/cancel**

Removes a task from the queue or cancels it on the agent if already dispatched.

**Response:**
```json
{
  "queue_id": "queue-abc12345",
  "state": "cancelled",
  "was_dispatched": false
}
```

If the task was already dispatched:
```json
{
  "queue_id": "queue-abc12345",
  "state": "cancelled",
  "was_dispatched": true,
  "agent_url": "http://localhost:9000",
  "task_id": "task-xyz789"
}
```

---

## Dispatcher

### Dispatcher Loop

The dispatcher runs as a background goroutine, polling every second:

```go
func (d *Dispatcher) Start(stop <-chan struct{}) {
    ticker := time.NewTicker(time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-stop:
            return
        case <-ticker.C:
            d.dispatchNext()
        }
    }
}

func (d *Dispatcher) dispatchNext() {
    // Find first idle agent
    agent := d.findFirstIdleAgent()
    if agent == nil {
        return // No idle agents
    }

    // Get next pending task (FIFO)
    task := d.queue.NextPending()
    if task == nil {
        return // Queue empty
    }

    // Mark as dispatching and persist
    d.queue.SetState(task, TaskStateDispatching)

    // Submit to agent
    taskID, sessionID, err := d.submitToAgent(agent, task)

    if err != nil {
        d.handleDispatchError(task, err)
        return
    }

    // Success - update task with agent info
    d.queue.SetDispatched(task, agent.URL, taskID, sessionID)
}

func (d *Dispatcher) findFirstIdleAgent() *AgentInfo {
    agents := d.discovery.GetAgents()
    for _, agent := range agents {
        if agent.State == "idle" && agent.Healthy {
            return agent
        }
    }
    return nil
}
```

### Dispatch Error Handling

```go
func (d *Dispatcher) handleDispatchError(task *QueuedTask, err error) {
    task.Attempts++
    task.LastError = err.Error()

    // Check if it's a retryable error
    if isAgentBusy(err) {
        // Agent became busy between check and submit - re-queue at back
        d.queue.RequeueAtBack(task)
        return
    }

    if task.Attempts >= d.config.MaxAttempts {
        // Max attempts reached - fail the task
        d.queue.SetState(task, TaskStateFailed)
        d.queue.Remove(task)
        log.Printf("queue=dispatch_failed queue_id=%s error=%q attempts=%d",
            task.QueueID, err, task.Attempts)
        return
    }

    // Retryable error - back to pending
    d.queue.SetState(task, TaskStatePending)
    log.Printf("queue=dispatch_retry queue_id=%s error=%q attempt=%d/%d",
        task.QueueID, err, task.Attempts, d.config.MaxAttempts)
}

func isAgentBusy(err error) bool {
    // Check for 409 Conflict response
    var httpErr *HTTPError
    if errors.As(err, &httpErr) {
        return httpErr.StatusCode == http.StatusConflict
    }
    return false
}
```

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

```go
// HandleTaskSubmit now routes through queue
func (h *Handlers) HandleTaskSubmit(w http.ResponseWriter, r *http.Request) {
    var req TaskSubmitRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        api.WriteError(w, http.StatusBadRequest, "validation_error", err.Error())
        return
    }

    // Convert to queue request
    queueReq := QueueSubmitRequest{
        Prompt:         req.Prompt,
        Model:          req.Model,
        TimeoutSeconds: req.TimeoutSeconds,
        SessionID:      req.SessionID,
        Project:        req.Project,
        Env:            req.Env,
        Thinking:       req.Thinking,
        Source:         "web",
    }

    task, position, err := h.queue.Add(queueReq)
    if err == ErrQueueFull {
        api.WriteError(w, http.StatusServiceUnavailable, "queue_full",
            "Queue is at capacity. Please try again later.")
        return
    }

    // Return queue info (different from direct submission)
    api.WriteJSON(w, http.StatusAccepted, map[string]interface{}{
        "queue_id": task.QueueID,
        "position": position,
        "state":    "pending",
        "message":  "Task queued for execution",
    })
}
```

#### Dashboard Changes

The dashboard needs to:
1. Show queue depth in the header/status area
2. Display pending tasks in a "Queue" panel
3. Allow cancellation of queued tasks
4. Update task status as it moves through states

```javascript
// Alpine.js additions to dashboard
{
    queue: [],
    queueDepth: 0,

    async fetchQueue() {
        const resp = await fetch('/api/queue');
        const data = await resp.json();
        this.queue = data.tasks;
        this.queueDepth = data.depth;
    },

    async cancelQueued(queueId) {
        await fetch(`/api/queue/${queueId}/cancel`, { method: 'POST' });
        this.fetchQueue();
    }
}
```

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

```go
func (s *Scheduler) runJob(js *jobState) {
    // Always use queue via director
    if s.config.DirectorURL == "" {
        log.Printf("job=%s action=skipped reason=no_director_url", js.Job.Name)
        s.updateJobState(js, "skipped_no_director", "")
        return
    }

    queueID, err := s.submitViaQueue(js)
    if err != nil {
        if isQueueFull(err) {
            log.Printf("job=%s action=skipped reason=queue_full", js.Job.Name)
            s.updateJobState(js, "skipped_queue_full", "")
        } else {
            log.Printf("job=%s action=skipped reason=error error=%q", js.Job.Name, err)
            s.updateJobState(js, "skipped_error", "")
        }
        return
    }

    log.Printf("job=%s action=queued queue_id=%s", js.Job.Name, queueID)
    s.updateJobState(js, "queued", queueID)
}

func (s *Scheduler) submitViaQueue(js *jobState) (string, error) {
    req := QueueSubmitRequest{
        Prompt:         js.Job.Prompt,
        Model:          js.Job.Model,
        TimeoutSeconds: int(js.Job.Timeout.Seconds()),
        Source:         "scheduler",
        SourceJob:      js.Job.Name,
    }

    body, _ := json.Marshal(req)
    resp, err := s.client.Post(
        s.config.DirectorURL+"/api/queue/task",
        "application/json",
        bytes.NewReader(body),
    )
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusServiceUnavailable {
        return "", ErrQueueFull
    }
    if resp.StatusCode != http.StatusCreated {
        return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }

    var result struct {
        QueueID string `json:"queue_id"`
    }
    json.NewDecoder(resp.Body).Decode(&result)
    return result.QueueID, nil
}
```

#### Config Changes

```yaml
# configs/scheduler.yaml
port: 9100
log_level: info
director_url: http://localhost:8080  # Required for queue submission

# agent_url no longer needed - queue handles dispatch
# agent_url: http://localhost:9000  # Removed

jobs:
  - name: nightly-maintenance
    schedule: "0 1 * * *"
    prompt: |
      Perform nightly maintenance...
```

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

```json
{
  "type": "helper",
  "interfaces": ["statusable", "observable"],
  "version": "1.0.0",
  "state": "running",
  "jobs": [
    {
      "name": "nightly-maintenance",
      "schedule": "0 1 * * *",
      "next_run": "2025-01-18T01:00:00Z",
      "last_run": "2025-01-17T01:00:00Z",
      "last_status": "queued",
      "last_queue_id": "queue-abc12345"
    }
  ]
}
```

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

```go
// cmd/ag-cli/queue.go

func cmdQueue(args []string) error {
    if len(args) < 1 {
        return fmt.Errorf("usage: ag-cli queue <prompt> [--model MODEL] [--timeout DURATION]")
    }

    prompt := args[0]
    model := flagModel
    timeout := flagTimeout

    req := QueueSubmitRequest{
        Prompt:         prompt,
        Model:          model,
        TimeoutSeconds: int(timeout.Seconds()),
        Source:         "cli",
    }

    resp, err := submitToQueue(directorURL, req)
    if err != nil {
        return err
    }

    fmt.Printf("Queued: %s (position %d)\n", resp.QueueID, resp.Position)
    return nil
}

func cmdQueueStatus(args []string) error {
    if len(args) > 0 {
        // Specific task
        task, err := getQueuedTask(directorURL, args[0])
        if err != nil {
            return err
        }
        printQueuedTask(task)
        return nil
    }

    // Full queue
    queue, err := getQueue(directorURL)
    if err != nil {
        return err
    }

    fmt.Printf("Queue: %d/%d tasks\n", queue.Depth, queue.MaxSize)
    for i, task := range queue.Tasks {
        fmt.Printf("  %d. %s [%s] %s\n",
            i+1, task.QueueID, task.State, task.PromptPreview)
    }
    return nil
}
```

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

```yaml
# configs/scheduler.yaml
port: 9100
director_url: http://localhost:8080  # Required
# agent_url removed - queue handles dispatch
```

---

## Observability

### Logging

```
2025-01-17T10:00:00Z INFO queue=task_added queue_id=queue-abc12345 depth=5 source=scheduler
2025-01-17T10:00:01Z INFO queue=dispatch queue_id=queue-abc12345 agent=http://localhost:9000
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

1. Add `director_url` to scheduler config (already exists per SESSION_ROUTING_DESIGN.md)
2. Remove `agent_url` from scheduler config
3. Scheduler automatically uses queue API

### No Fallback

Unlike SESSION_ROUTING_DESIGN.md which proposed fallback to direct agent submission, the queue model requires all submissions to go through the director. If the director is down, tasks cannot be submitted.

Rationale: Simpler model, guaranteed queue visibility, no split-brain scenarios.

---

## Related Documents

- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Cron-style scheduling
- [SESSION_ROUTING_DESIGN.md](SESSION_ROUTING_DESIGN.md) - Session management
- [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) - State synchronization
- [REFERENCE.md](REFERENCE.md) - API specifications
