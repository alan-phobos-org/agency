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
| **Fallback mechanism** | Timer-based polling | Ensures completion detection when callbacks fail |
| **Fallback interval** | 30 seconds initial, 15 seconds subsequent | Much longer than current 5s since callbacks are primary |

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
|  |  - fallbackPoll (timer)     |          ^             |
|  +-----------------------------+          |             |
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
| **Dispatcher** | Register completion waiters, handle callbacks, fallback polling |
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
    CallbackURL    string            `json:"callback_url,omitempty"` // NEW
}
```

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
    fallback  *time.Timer
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

    waiter := &completionWaiter{
        task:      task,
        startedAt: time.Now(),
    }

    // Fallback poll after 30 seconds if no callback received
    waiter.fallback = time.AfterFunc(30*time.Second, func() {
        d.fallbackPoll(task.QueueID)
    })

    d.pendingTasks[task.QueueID] = waiter
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

    // Stop fallback timer
    waiter.fallback.Stop()
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

#### Fallback Polling

```go
func (d *Dispatcher) fallbackPoll(queueID string) {
    d.mu.RLock()
    waiter, ok := d.pendingTasks[queueID]
    d.mu.RUnlock()

    if !ok {
        return // Already handled via callback
    }

    task := waiter.task
    status, err := d.getTaskStatus(task.AgentURL, task.TaskID)

    if err != nil {
        // Agent unreachable, reschedule poll
        fmt.Fprintf(os.Stderr, "queue: fallback poll failed for %s: %v\n", queueID, err)
        d.mu.Lock()
        if w, ok := d.pendingTasks[queueID]; ok {
            w.fallback = time.AfterFunc(15*time.Second, func() {
                d.fallbackPoll(queueID)
            })
        }
        d.mu.Unlock()
        return
    }

    if isTerminalState(status) {
        // Trigger completion handling
        d.HandleTaskCallback(queueID, TaskCallback{
            TaskID:      task.TaskID,
            State:       TaskState(status),
            CompletedAt: time.Now(),
        })
    } else {
        // Still running, reschedule with longer interval
        d.mu.Lock()
        if w, ok := d.pendingTasks[queueID]; ok {
            w.fallback = time.AfterFunc(15*time.Second, func() {
                d.fallbackPoll(queueID)
            })
        }
        d.mu.Unlock()
    }
}
```

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

## Migration Strategy

### Phase 1: Add Callback Support (Backward Compatible)

1. Add `callback_url` field to agent's `TaskRequest` (ignored if not present)
2. Add `notifyCompletion` function to agent (only called if callback_url set)
3. Deploy agents first - they continue to work with old directors

### Phase 2: Enable Callbacks

1. Add callback endpoint to director's internal router
2. Modify dispatcher to send callback URLs and register waiters
3. Keep existing polling as fallback (30s interval instead of 5s)
4. Deploy directors - callbacks active, polling as backup

### Phase 3: Monitor and Tune

1. Monitor callback success rate
2. Adjust fallback intervals based on callback reliability
3. Consider removing fallback polling entirely once stable

---

## Error Handling

### Callback Delivery Failures

| Scenario | Agent Behavior | Director Behavior |
|----------|---------------|-------------------|
| Network error | Retry 3x with backoff | Fallback poll triggers |
| 4xx response | Log and stop retrying | Task may be cancelled |
| 5xx response | Retry with backoff | Fallback poll handles |
| Timeout | Retry with backoff | Fallback poll handles |

### Edge Cases

| Scenario | Handling |
|----------|----------|
| Callback arrives before waiter registered | Return OK (task handled elsewhere) |
| Task cancelled during execution | Waiter removed, late callback returns OK |
| Director restart loses waiters | Queue persistence + fallback on next poll cycle |
| Duplicate callbacks | Idempotent via pendingTasks lookup |
| Agent crash after task completion | Fallback poll detects via history endpoint |

---

## Observability

### Logging

```
# Callback delivered successfully
DEBUG queue: completed queue-123 via callback (status=completed, latency=1.2s)

# Fallback poll triggered
INFO  queue: fallback poll for queue-123 (no callback after 30s)

# Callback delivery failure
WARN  agent: completion callback failed after retries callback_url=http://... attempts=3
```

### Metrics (Future)

| Metric | Type | Description |
|--------|------|-------------|
| `dispatcher_callbacks_total` | Counter | Total callbacks received |
| `dispatcher_callback_latency_seconds` | Histogram | Time from dispatch to callback |
| `dispatcher_fallback_polls_total` | Counter | Fallback polls triggered |
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
   - Fallback timer set to 30 seconds
   - Timer properly cancelled on callback

3. **Dispatcher `HandleTaskCallback`**: Test:
   - Successful completion (waiter exists)
   - Unknown task (waiter doesn't exist)
   - Duplicate callback (idempotent)
   - Session store update triggered

4. **Dispatcher `fallbackPoll`**: Test:
   - Rescheduling on agent unreachable
   - Terminal state detection
   - Timer cleanup on completion

### New Integration Tests Required

1. **Callback happy path**: Submit task via queue, mock agent sends callback, verify:
   - Queue task removed
   - Session store updated
   - Fallback timer cancelled

2. **Callback failure with fallback**: Submit task, block callback endpoint, verify:
   - Fallback poll triggers after 30s
   - Completion still detected

3. **Graceful shutdown**: Verify pendingTasks cleaned up on dispatcher shutdown

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
| **Constructor signature change** | Breaking change to `NewDispatcher()` | Add `internalPort` as last parameter with default |
| **Callback endpoint security** | Unauthorized callbacks could corrupt state | Validate queueID exists, localhost-only endpoint |
| **Orphaned waiters on shutdown** | Memory leak, stale timers | Add cleanup in dispatcher shutdown |
| **Timer goroutine leaks** | Resource exhaustion | Always call `timer.Stop()` before replacing |
| **Race between callback and fallback** | Double completion handling | Use `pendingTasks` map as single source of truth |

### Operational Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| **Agent/Director version mismatch** | Old agents don't send callbacks | Fallback polling handles this |
| **Network partition** | Callbacks fail, delayed detection | Exponential backoff + fallback polling |
| **Director restart** | Pending waiters lost | Queue persistence ensures tasks tracked; fallback restarts on next dispatch check |
| **High callback volume** | Director overwhelmed | Callbacks are small JSON, one per task completion |

---

## Performance Impact

| Metric | Before (Polling) | After (Callback) |
|--------|-----------------|------------------|
| Completion latency | 0-5 seconds | ~100-200ms |
| HTTP requests/task | n (poll cycles) | 2-4 (with retries) |
| Goroutines/task | 1 (polling loop) | 0 (timer-based) |
| Network bandwidth | O(n * poll_freq) | O(n * 1) |

For a typical 60-second task:
- **Before**: ~12 poll requests
- **After**: 1 callback request (+ possible retries)

---

## File Changes Summary

| File | Changes |
|------|---------|
| `internal/agent/agent.go` | Add `CallbackURL` to TaskRequest, add `callbackURL` field to Task, add `notifyCompletion` function, call notification at end of `executeTask` |
| `internal/view/web/dispatcher.go` | Add `completionWaiter` struct, add `pendingTasks` map, add `registerCompletionWaiter`, `HandleTaskCallback`, `fallbackPoll` functions, modify `submitToAgent` to include callback URL |
| `internal/view/web/director.go` | Add callback endpoint to `InternalRouter`, pass `InternalPort` to dispatcher constructor |
