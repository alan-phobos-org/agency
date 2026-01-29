# Event-Driven Task Completion Tracking

## Overview

Replace polling-based task completion tracking in the dispatcher with a callback-based event-driven architecture. When agents complete tasks, they proactively notify the director via HTTP callback, eliminating the need for continuous polling.

**Related docs:**
- [WORK_QUEUE_DESIGN.md](../WORK_QUEUE_DESIGN.md) - Queue and dispatcher architecture
- [TASK_STATE_SYNC_DESIGN.md](../TASK_STATE_SYNC_DESIGN.md) - State synchronization patterns

---

## Problem Statement

The current dispatcher (`internal/view/web/dispatcher.go:177-206`) tracks task completion through polling:

```go
func (d *Dispatcher) trackCompletion(task *QueuedTask) {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for range ticker.C {
        status, err := d.getTaskStatus(task.AgentURL, task.TaskID)
        if err != nil {
            continue  // Agent unreachable - keep polling
        }
        if isTerminalState(status) {
            d.sessionStore.UpdateTaskState(...)
            d.queue.Remove(task)
            return
        }
    }
}
```

### Issues with Polling

| Issue | Impact |
|-------|--------|
| **High latency** | 0-5 second delay before completion detection |
| **Network overhead** | Continuous HTTP requests during task execution |
| **Poor scalability** | O(n) goroutines polling for n dispatched tasks |
| **Silent failures** | Agent unreachability causes indefinite polling |
| **Wasted resources** | Polling even during long-running tasks |

### Current Flow

```
Director                            Agent
    |                                 |
    |   POST /task                    |
    |-------------------------------->|
    |   201 {task_id}                 |
    |<--------------------------------|
    |                                 |
    |   GET /task/{id}  (poll)        |
    |-------------------------------->|
    |   {state: "working"}            |
    |<--------------------------------|
    |       ... (every 5 seconds) ... |
    |   GET /task/{id}  (poll)        |
    |-------------------------------->|
    |   {state: "completed"}          |
    |<--------------------------------|
    |                                 |
```

### Desired Flow

```
Director                            Agent
    |                                 |
    |   POST /task                    |
    |   {callback_url: "..."}         |
    |-------------------------------->|
    |   201 {task_id}                 |
    |<--------------------------------|
    |                                 |
    |        ... (task executes) ...  |
    |                                 |
    |   POST /callback/{queue_id}     |
    |   {state: "completed", ...}     |
    |<--------------------------------|
    |                                 |
```

---

## Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| **Notification protocol** | HTTP POST callback | Reuses existing HTTP infrastructure, simple to implement |
| **Callback destination** | Internal API (localhost) | No auth required, fast, reliable within same network |
| **Retry strategy** | 3 attempts with exponential backoff | Balance reliability with resource efficiency |
| **Fallback mechanism** | None | All agents deployed with callback support; simplifies implementation |

---

## Architecture

### System Overview

```
+--------------------------------------------------------+
|                  Web Director                           |
|  +-----------------------------+  +------------------+  |
|  |        WorkQueue            |  |   SessionStore   |  |
|  | (pending/dispatched tasks)  |  | (active sessions)|  |
|  +-----------------------------+  +------------------+  |
|               |                           ^             |
|               v                           |             |
|  +-----------------------------+  +------------------+  |
|  |        Dispatcher           |  |  Callback API    |  |
|  |  - submitToAgent (w/callback)|  |  POST /callback/ |  |
|  |  - registerWaiter           |  |     {queue_id}   |  |
|  |  - handleCallback           |  +------------------+  |
|  +-----------------------------+          ^             |
|               |                           |             |
+---------------|---------------------------|-------------+
                |                           |
                v                           |
         +-----------+            +---------+
         |   Agent   | -- callback -->
         +-----------+
```

### Component Responsibilities

| Component | Responsibility |
|-----------|----------------|
| **Agent** | Execute task, send completion callback to director |
| **Dispatcher** | Register completion waiters, handle callbacks |
| **Callback API** | HTTP endpoint receiving agent completion notifications |
| **WorkQueue** | Store task metadata including callback state |

---

## Detailed Design

### 1. Agent Side Changes

**File: `internal/agent/agent.go`**

#### Extended TaskRequest

Add optional callback URL field:

```go
type TaskRequest struct {
    Prompt         string            `json:"prompt"`
    Tier           string            `json:"tier,omitempty"`
    TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
    SessionID      string            `json:"session_id,omitempty"`
    Env            map[string]string `json:"env,omitempty"`
    CallbackURL    string            `json:"callback_url,omitempty"` // NEW: optional
}
```

**Note:** `callback_url` is optional to support direct task submission for testing. When omitted, the agent completes the task normally but doesn't notify anyone - the caller must poll `/task/{id}` or `/history/{id}` to check status.

#### TaskCallback Payload

Define the callback payload structure:

```go
// TaskCallback is the completion notification sent to the director
type TaskCallback struct {
    TaskID      string     `json:"task_id"`
    State       TaskState  `json:"state"`
    ExitCode    *int       `json:"exit_code,omitempty"`
    Error       *TaskError `json:"error,omitempty"`
    CompletedAt time.Time  `json:"completed_at"`
}
```

#### Callback Notification Function

```go
// notifyCompletion sends a completion callback to the director.
// Retries up to 3 times with exponential backoff.
// Failures are logged but do not affect task completion.
func (a *Agent) notifyCompletion(task *Task, callbackURL string) {
    callback := TaskCallback{
        TaskID:      task.ID,
        State:       task.State,
        ExitCode:    task.ExitCode,
        CompletedAt: *task.CompletedAt,
    }
    if task.Error != nil {
        callback.Error = task.Error
    }

    body, err := json.Marshal(callback)
    if err != nil {
        a.log.WithTask(task.ID).Warn("failed to marshal callback", map[string]any{
            "error": err.Error(),
        })
        return
    }

    client := &http.Client{
        Timeout: 5 * time.Second,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
        },
    }

    backoff := 100 * time.Millisecond
    const maxAttempts = 3

    for attempt := 1; attempt <= maxAttempts; attempt++ {
        resp, err := client.Post(callbackURL, "application/json", bytes.NewReader(body))
        if err == nil {
            resp.Body.Close()
            if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
                a.log.WithTask(task.ID).Debug("completion callback delivered", map[string]any{
                    "callback_url": callbackURL,
                    "attempt":      attempt,
                })
                return
            }
            a.log.WithTask(task.ID).Debug("callback returned non-success", map[string]any{
                "status_code": resp.StatusCode,
                "attempt":     attempt,
            })
        } else {
            a.log.WithTask(task.ID).Debug("callback delivery failed", map[string]any{
                "error":   err.Error(),
                "attempt": attempt,
            })
        }

        if attempt < maxAttempts {
            time.Sleep(backoff)
            backoff *= 2
        }
    }

    a.log.WithTask(task.ID).Warn("completion callback failed after retries", map[string]any{
        "callback_url": callbackURL,
        "attempts":     maxAttempts,
    })
}
```

#### Integration into executeTask

Store callback URL during task creation and call notification after completion:

```go
// In handleCreateTask, store callback URL on task
task.callbackURL = req.CallbackURL  // Add callbackURL field to Task struct

// At end of executeTask, after saveTaskHistory and before cleanupTask
if task.callbackURL != "" {
    go a.notifyCompletion(task, task.callbackURL)
}
a.cleanupTask(task)
```

### 2. Director Side Changes

**File: `internal/view/web/dispatcher.go`**

#### Extended Dispatcher Structure

```go
type Dispatcher struct {
    queue         *WorkQueue
    discovery     *Discovery
    sessionStore  *SessionStore
    client        *http.Client
    pollInterval  time.Duration
    internalPort  int                         // NEW: for building callback URLs
    pendingTasks  map[string]*completionWaiter // NEW: tasks awaiting callback
    mu            sync.RWMutex                // NEW: protect pendingTasks
}

// completionWaiter tracks a dispatched task awaiting completion
type completionWaiter struct {
    task      *QueuedTask
    startedAt time.Time
}
```

#### Updated Constructor

```go
func NewDispatcher(queue *WorkQueue, discovery *Discovery, sessionStore *SessionStore, internalPort int) *Dispatcher {
    return &Dispatcher{
        queue:        queue,
        discovery:    discovery,
        sessionStore: sessionStore,
        client:       createHTTPClient(queue.Config().DispatchTimeout),
        pollInterval: time.Second,
        internalPort: internalPort,
        pendingTasks: make(map[string]*completionWaiter),
    }
}
```

#### Callback URL Generation

```go
func (d *Dispatcher) buildCallbackURL(queueID string) string {
    return fmt.Sprintf("http://127.0.0.1:%d/api/callback/%s", d.internalPort, queueID)
}
```

#### Modified submitToAgent

```go
func (d *Dispatcher) submitToAgent(agent *ComponentStatus, task *QueuedTask) (taskID, sessionID string, err error) {
    // Build agent request with callback URL
    agentReq := buildAgentRequest(task.Prompt, task.Tier, task.TimeoutSeconds, task.SessionID, task.Env)
    agentReq["callback_url"] = d.buildCallbackURL(task.QueueID)

    body, _ := json.Marshal(agentReq)
    resp, err := d.client.Post(agent.URL+"/task", "application/json", bytes.NewReader(body))
    // ... rest unchanged
}
```

#### Replace trackCompletion with registerWaiter

```go
// In dispatchNext, replace: go d.trackCompletion(task)
// With:
d.registerCompletionWaiter(task)

func (d *Dispatcher) registerCompletionWaiter(task *QueuedTask) {
    d.mu.Lock()
    defer d.mu.Unlock()

    d.pendingTasks[task.QueueID] = &completionWaiter{
        task:      task,
        startedAt: time.Now(),
    }
}
```

#### Callback Handler

```go
// HandleTaskCallback processes completion callbacks from agents
func (d *Dispatcher) HandleTaskCallback(queueID string, callback TaskCallback) error {
    d.mu.Lock()
    waiter, ok := d.pendingTasks[queueID]
    if !ok {
        d.mu.Unlock()
        return fmt.Errorf("unknown task: %s", queueID)
    }

    delete(d.pendingTasks, queueID)
    d.mu.Unlock()

    task := waiter.task

    // Update session store
    if task.SessionID != "" {
        d.sessionStore.UpdateTaskState(task.SessionID, task.TaskID, string(callback.State))
    }

    // Remove from queue
    d.queue.Remove(task)

    fmt.Fprintf(os.Stderr, "queue: completed %s via callback (status=%s, latency=%v)\n",
        task.QueueID, callback.State, time.Since(waiter.startedAt))

    return nil
}
```

#### Remove trackCompletion and getTaskStatus

The following functions are no longer needed and should be removed:
- `trackCompletion()`
- `getTaskStatus()`

### 3. Director Router Changes

**File: `internal/view/web/director.go`**

#### Add Callback Endpoint

```go
func (d *Director) InternalRouter() chi.Router {
    r := chi.NewRouter()
    r.Use(middleware.Recoverer)

    r.Route("/api", func(r chi.Router) {
        // ... existing routes ...

        // Callback endpoint for agent completion notifications
        r.Post("/callback/{queueId}", func(w http.ResponseWriter, req *http.Request) {
            queueID := chi.URLParam(req, "queueId")
            d.handleTaskCallback(w, req, queueID)
        })
    })

    // ... rest unchanged
}

func (d *Director) handleTaskCallback(w http.ResponseWriter, r *http.Request, queueID string) {
    var callback TaskCallback
    if err := json.NewDecoder(r.Body).Decode(&callback); err != nil {
        api.WriteError(w, http.StatusBadRequest, api.ErrorValidation, "Invalid callback payload")
        return
    }

    if err := d.dispatcher.HandleTaskCallback(queueID, callback); err != nil {
        // Log but return OK - task may have been cancelled or already completed
        fmt.Fprintf(os.Stderr, "queue: callback for unknown task %s\n", queueID)
    }

    api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
```

#### Pass InternalPort to Dispatcher

```go
// In New()
dispatcher := NewDispatcher(queue, discovery, handlers.sessionStore, cfg.InternalPort)
```

---

## Deployment

All agents and directors must be restarted together with the new code.

---

## Director Restart Recovery

When the director restarts, the in-memory `pendingTasks` map is lost but tasks remain on disk in the queue (marked as "dispatched"). On startup, the dispatcher must recover these orphaned tasks.

### Recovery Logic

```go
// In Dispatcher.Start(), before starting the dispatch loop
func (d *Dispatcher) recoverOrphanedTasks() {
    orphaned := d.queue.GetDispatched()
    for _, task := range orphaned {
        d.registerCompletionWaiter(task)
        fmt.Fprintf(os.Stderr, "queue: recovered orphaned task %s (agent=%s)\n",
            task.QueueID, task.AgentURL)
    }
}
```

### Queue Method Addition

```go
// GetDispatched returns all tasks in "dispatched" or "working" state
func (q *WorkQueue) GetDispatched() []*QueuedTask {
    q.mu.RLock()
    defer q.mu.RUnlock()

    var result []*QueuedTask
    for _, task := range q.tasks {
        if task.State == TaskStateDispatching || task.State == TaskStateWorking {
            result = append(result, task)
        }
    }
    return result
}
```

This ensures that if a callback arrives after director restart, the waiter exists to handle it. If the agent already completed and the callback was lost, the task remains in the queue for manual inspection.

---

## Task Cancellation

When a task is cancelled via `POST /api/queue/{queueId}/cancel`, the dispatcher must:

1. Remove the waiter from `pendingTasks`
2. Notify the agent to cancel the running task
3. Remove from queue

### Cancellation Flow

```go
func (d *Dispatcher) CancelTask(queueID string) error {
    d.mu.Lock()
    waiter, ok := d.pendingTasks[queueID]
    if ok {
        delete(d.pendingTasks, queueID)
    }
    d.mu.Unlock()

    if !ok {
        return fmt.Errorf("task not found: %s", queueID)
    }

    task := waiter.task

    // Notify agent to cancel (best effort)
    if task.AgentURL != "" && task.TaskID != "" {
        go d.notifyAgentCancel(task.AgentURL, task.TaskID)
    }

    // Update session store
    if task.SessionID != "" {
        d.sessionStore.UpdateTaskState(task.SessionID, task.TaskID, TaskStateCancelled)
    }

    // Remove from queue
    d.queue.Remove(task)

    fmt.Fprintf(os.Stderr, "queue: cancelled %s\n", task.QueueID)
    return nil
}

func (d *Dispatcher) notifyAgentCancel(agentURL, taskID string) {
    resp, err := d.client.Post(agentURL+"/task/"+taskID+"/cancel", "application/json", nil)
    if err != nil {
        fmt.Fprintf(os.Stderr, "queue: failed to notify agent of cancellation: %v\n", err)
        return
    }
    resp.Body.Close()
}
```

### Late Callback Handling

If the agent sends a callback after cancellation, the waiter no longer exists. The callback handler returns OK but logs that the task was unknown (already cancelled).

---

## Error Handling

### Callback Delivery Failures

| Scenario | Agent Behavior |
|----------|---------------|
| Network error | Retry 3x with backoff, then log warning |
| 4xx response | Log and stop retrying |
| 5xx response | Retry with backoff |
| Timeout | Retry with backoff |

### Edge Cases

| Scenario | Handling |
|----------|----------|
| Callback arrives before waiter registered | Return OK (task handled elsewhere) |
| Task cancelled during execution | Waiter removed, agent notified, late callback ignored |
| Duplicate callbacks | Idempotent via pendingTasks lookup |
| Direct task submission (no callback_url) | Agent completes normally, caller polls for status |
| Director restart with running tasks | Orphaned tasks recovered on startup, waiters re-registered |

---

## Observability

### Logging

```
# Callback delivered successfully
DEBUG queue: completed queue-123 via callback (status=completed, latency=1.2s)

# Callback delivery failure
WARN  agent: completion callback failed after retries callback_url=http://... attempts=3
```

### Metrics (Future)

| Metric | Type | Description |
|--------|------|-------------|
| `dispatcher_callbacks_total` | Counter | Total callbacks received |
| `dispatcher_callback_latency_seconds` | Histogram | Time from dispatch to callback |
| `agent_callback_attempts_total` | Counter | Callback delivery attempts |

---

## Testing Strategy

### Existing Test Compatibility

**This change can be implemented with NO modifications to existing smoke tests.**

Analysis of existing tests shows that no tests directly exercise the `trackCompletion()` goroutine:

| Test Suite | What It Tests | Impact |
|------------|---------------|--------|
| **System tests** (`system_test.go`) | Dashboard endpoints, session ordering, discovery, job triggering | None - no dispatcher completion tracking tested |
| **Integration tests** (`integration_test.go`) | Web view handlers, task status proxying, history fallback | None - tests web view layer, not dispatcher |
| **Queue handler tests** (`queue_handlers_test.go`) | Task submission, dispatch triggering | None - calls `dispatchNext()` once, doesn't wait for completion |

The key insight is that `trackCompletion()` runs in a background goroutine that existing tests don't wait for. Tests either:
- Mock agent responses directly and verify immediate state changes
- Call `dispatcher.dispatchNext()` manually without waiting for completion
- Test session store updates via explicit API calls

### New Unit Tests Required

1. **Agent `notifyCompletion`**: Mock HTTP server, verify:
   - Successful callback delivery
   - Retry behavior with exponential backoff (100ms, 200ms, 400ms)
   - Graceful failure after 3 attempts

2. **Dispatcher `registerCompletionWaiter`**: Verify:
   - Waiter stored in `pendingTasks` map

3. **Dispatcher `HandleTaskCallback`**: Test:
   - Successful completion (waiter exists)
   - Unknown task (waiter doesn't exist)
   - Duplicate callback (idempotent)
   - Session store update triggered

### New Integration Tests Required

1. **Callback happy path**: Submit task via queue, mock agent sends callback, verify:
   - Queue task removed
   - Session store updated

2. **Graceful shutdown**: Verify pendingTasks cleaned up on dispatcher shutdown

### Regression Testing

The existing tests serve as regression tests ensuring:
- Task submission flow unchanged
- Agent communication protocol unchanged
- Session store behavior unchanged
- Queue state transitions unchanged

---

## Risks and Mitigations

### Implementation Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| **Constructor signature change** | Breaking change to `NewDispatcher()` | Add `internalPort` as last parameter |
| **Callback endpoint security** | Unauthorized callbacks could corrupt state | Validate queueID exists, localhost-only endpoint |
| **Orphaned waiters on shutdown** | Memory leak | Add cleanup in dispatcher shutdown |
| **Callback delivery failure** | Task stuck in queue | Agent retries 3x; logged for manual intervention if needed |

### Operational Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| **Director restart** | Pending waiters lost | Recover orphaned tasks on startup; re-register waiters |
| **High callback volume** | Director overwhelmed | Callbacks are small JSON, one per task completion |

---

## Performance Impact

| Metric | Before (Polling) | After (Callback) |
|--------|-----------------|------------------|
| Completion latency | 0-5 seconds | ~100-200ms |
| HTTP requests/task | n (poll cycles) | 1 (+ retries) |
| Goroutines/task | 1 (polling loop) | 0 |
| Network bandwidth | O(n * poll_freq) | O(n) |

For a typical 60-second task:
- **Before**: ~12 poll requests
- **After**: 1 callback request

---

## File Changes Summary

| File | Changes |
|------|---------|
| `internal/agent/agent.go` | Add `CallbackURL` to TaskRequest, add `callbackURL` field to Task, add `notifyCompletion` function, call notification at end of `executeTask` |
| `internal/view/web/dispatcher.go` | Add `completionWaiter` struct, add `pendingTasks` map, add `registerCompletionWaiter`, `HandleTaskCallback`, `CancelTask`, `recoverOrphanedTasks` functions, remove `trackCompletion` and `getTaskStatus`, modify `submitToAgent` to include callback URL |
| `internal/view/web/queue.go` | Add `GetDispatched()` method |
| `internal/view/web/director.go` | Add callback endpoint to `InternalRouter`, pass `InternalPort` to dispatcher constructor |
| `internal/view/web/queue_handlers.go` | Update cancel handler to call `dispatcher.CancelTask()` |
