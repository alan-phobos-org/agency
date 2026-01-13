# Task State Synchronization Design

This document analyzes the root causes of tasks getting stuck in "running" state in the web UI and proposes an architectural solution.

---

## Problem Statement

Tasks submitted through the web UI can become stuck showing "working/running" state even after the underlying task has completed on the agent. This creates confusion and forces users to manually refresh or restart sessions.

---

## Current Architecture Analysis

### State Management Layers

The system maintains task state at **three separate layers**, each with its own source of truth:

| Layer | Location | State Stored | Lifecycle |
|-------|----------|--------------|-----------|
| **Agent** | `agent.go:tasks` map | Authoritative task state | Task completion + pruning |
| **Web SessionStore** | `sessions.go` in-memory map | Cached copy of task state | Web server lifetime |
| **Browser** | `dashboard.html` JavaScript | UI display state | Page session |

### Current State Flow

```
1. Browser submits task via POST /api/task
2. Web proxies to Agent POST /task
3. Agent creates task, returns {task_id, session_id}
4. Web returns response to Browser
5. Browser calls saveSessionTask() to store in SessionStore
6. Browser sets activeTask and starts polling

--- Task Executes on Agent ---

7. Browser polls GET /api/task/{id}?agent_url=...
8. Web proxies to Agent GET /task/{id}
9. Agent returns current state
10. If terminal state: Browser calls updateSessionTaskState()
11. Browser clears activeTask

--- Potential Failure Points ---
```

---

## Root Causes of Stuck Tasks

### 1. Race Condition: Task Completes Between Submission and First Poll

**Scenario:**
1. Browser submits task
2. Agent accepts and executes task
3. Task completes very quickly (sub-second)
4. Agent moves task from `tasks` map to `history`
5. Browser's first poll hits Agent `/task/{id}` which returns 404
6. Web falls back to `/history/{id}` and finds the completed task
7. **BUT:** If `session_id` query param is missing, SessionStore is NOT updated

**Code Evidence:** `handlers.go:209-276`
```go
// HandleTaskStatus - auto-update only happens if session_id is provided
if sessionID != "" {
    // Update session store with terminal state from history
    h.sessionStore.UpdateTaskState(sessionID, taskID, historyData.State)
}
```

**Impact:** Moderate - The mitigation exists but requires caller cooperation.

### 2. Network/Browser Failures During Polling

**Scenario:**
1. Browser submits task, SessionStore has task as "working"
2. Browser starts polling
3. Network error or browser crash during polling
4. Task completes on Agent
5. User refreshes page or returns later
6. Browser loads sessions from SessionStore - still shows "working"
7. Agent has no active task (it's in history)

**Code Evidence:** `dashboard.html:764-798`
```javascript
async function pollTask() {
    try {
        const status = await api(url);
        if (['completed', 'failed', 'cancelled'].includes(status.state)) {
            await updateSessionTaskState(...);  // <-- Only runs on success
        }
    } catch (err) {
        console.error('Task poll failed:', err);
        // No cleanup! Task stays "working" in SessionStore
    }
}
```

**Impact:** High - Common scenario, no automatic recovery.

### 3. Tab/Window Close During Task Execution

**Scenario:**
1. Browser submits task
2. User closes tab before task completes
3. Task completes on Agent
4. User reopens dashboard
5. Sessions show task as "working" (stale state in SessionStore)

**Code Evidence:** No `beforeunload` handler, no heartbeat mechanism.

**Impact:** High - Users frequently close tabs.

### 4. Web Server Restart During Task

**Scenario:**
1. Browser submits task
2. Web server restarts (deploy, crash)
3. SessionStore is in-memory only - all session data lost
4. Task completes on Agent
5. User sees empty sessions list (data loss)

**Code Evidence:** `sessions.go` - pure in-memory storage with no persistence.

**Impact:** Medium - Loses ALL session data, not just stuck tasks.

### 5. Agent Task Deletion During Execution

**Scenario:**
1. Task starts on Agent
2. Agent crashes or restarts
3. Agent's in-memory `tasks` map is lost
4. SessionStore still shows task as "working"
5. Polls return 404 from both `/task` and `/history` (not saved yet)

**Code Evidence:** `agent.go` - tasks map is not persisted until completion.

**Impact:** Medium - Agent restarts are rare but possible.

### 6. Inconsistent Session ID Handling

**Scenario:**
1. First task creates session with UUID
2. Agent may return different `session_id` in response (from Claude CLI)
3. Web stores task under original session ID
4. Subsequent lookups may mismatch

**Code Evidence:** `agent.go:564-568`
```go
// Only update session_id if Claude returns a non-empty value
if claudeResp.SessionID != "" {
    task.SessionID = claudeResp.SessionID
}
```

**Impact:** Low - Session IDs are generated upfront now, but edge cases exist.

---

## Fundamental Architecture Problem

The core issue is **distributed state without synchronization primitives**:

```
+----------+     +----------+     +----------+
|  Browser |     |   Web    |     |  Agent   |
|  (state) |<--->|  (state) |<--->|  (state) |
+----------+     +----------+     +----------+
     ^               ^                  ^
     |               |                  |
   Volatile      Volatile          Authoritative
   (JS memory)  (Go memory)        (but ephemeral)
```

**Problems:**
1. No single source of truth accessible to all layers
2. State synchronization is optimistic (poll-based) and fragile
3. No recovery mechanism when synchronization fails
4. No persistence for durability across restarts

---

## Proposed Architectural Solution

### Principle: Agent as Single Source of Truth

The Agent already maintains authoritative task state. The solution is to **eliminate redundant state in other layers** and make them pure caches that can always be rebuilt from the Agent.

### Option A: Stateless Web View (Recommended)

**Concept:** Remove SessionStore entirely. Web becomes a pure proxy.

**Changes:**

1. **Remove SessionStore** - No in-memory session tracking in Web
2. **Sessions derived from Agent history** - Query all agents for history, group by session_id
3. **Browser polls Agent directly** (via Web proxy)
4. **No state to get stale** - Every view is fresh from Agent

**Implementation:**

```go
// New endpoint: GET /api/sessions
// Queries all discovered agents' /history, groups by session_id
func (h *Handlers) HandleSessions(w http.ResponseWriter, r *http.Request) {
    agents := h.discovery.Agents()
    sessions := make(map[string]*Session)

    for _, agent := range agents {
        history := fetchAgentHistory(agent.URL)
        for _, entry := range history {
            sid := entry.SessionID
            if sessions[sid] == nil {
                sessions[sid] = &Session{ID: sid, AgentURL: agent.URL}
            }
            sessions[sid].Tasks = append(sessions[sid].Tasks, TaskFromHistory(entry))
        }
    }

    return sortedSessions(sessions)
}
```

**Pros:**
- Eliminates ALL stuck task bugs
- Simpler code (remove SessionStore)
- Survives Web restarts
- Agent is already designed as source of truth

**Cons:**
- More HTTP traffic (acceptable for localhost)
- Need to handle in-progress tasks (query `/task` for current, `/history` for past)
- History retention limits affect session visibility

### Option B: Event-Driven State Sync

**Concept:** Agent pushes state changes to Web via webhooks or SSE.

**Changes:**

1. **Agent emits events** on task state changes
2. **Web subscribes** to agent events
3. **SessionStore updates** reactively, not poll-based
4. **Persistence** added to SessionStore (SQLite or file)

**Implementation:**

```go
// Agent: Add SSE endpoint
GET /events
// Emits: {"event": "task_state", "task_id": "...", "state": "completed"}

// Web: Subscribe on discovery
func (d *Discovery) subscribeToEvents(agentURL string) {
    sse := NewSSEClient(agentURL + "/events")
    sse.OnMessage(func(event Event) {
        h.sessionStore.UpdateTaskState(event.SessionID, event.TaskID, event.State)
    })
}
```

**Pros:**
- Real-time updates
- Lower latency than polling
- Maintains session history across Web restarts (with persistence)

**Cons:**
- More complex (SSE, reconnection logic)
- Still requires heartbeats/timeouts for lost connections
- Agent changes required
- Persistence adds complexity

### Option C: Hybrid Approach

**Concept:** Keep SessionStore but add reconciliation.

**Changes:**

1. **Background reconciliation job** - Periodically validates SessionStore against Agent
2. **On page load** - Full reconciliation of visible sessions
3. **Stale detection** - Mark tasks as "unknown" if Agent has no record

**Implementation:**

```javascript
// Browser: On page load or session view
async function reconcileSession(sessionId) {
    const session = sessions[sessionId];
    for (const task of session.tasks) {
        if (task.state === 'working') {
            const agentState = await fetchTaskState(task);
            if (agentState.state !== 'working') {
                await updateSessionTaskState(sessionId, task.taskId, agentState.state);
            }
        }
    }
}
```

**Pros:**
- Minimal changes to existing architecture
- Backward compatible
- Eventually consistent

**Cons:**
- Complexity creep
- Still has race conditions during reconciliation gaps
- Doesn't fix root cause

---

## Recommendation

**Option A (Stateless Web View)** is the recommended solution because:

1. **Eliminates the class of bugs entirely** - No local state means nothing to get stale
2. **Simpler architecture** - Remove code instead of adding it
3. **Already proven** - Agent history is reliable and queryable
4. **Minimal Agent changes** - Only need to ensure history is complete

### Implementation Steps

1. **Add "include current task" to Agent history** - Return in-progress task in history queries
2. **New Web endpoint** - `GET /api/sessions` derives sessions from agent histories
3. **Update Browser** - Fetch sessions from new endpoint instead of local storage
4. **Remove SessionStore** - Delete `sessions.go` and related handlers
5. **Add caching** - ETag-based caching for history queries

### Migration Path

1. Implement new endpoints alongside existing ones
2. Browser can use either (feature flag)
3. Monitor for issues
4. Remove old code paths

---

## Immediate Quick Fixes

While planning the architectural solution, these quick fixes reduce the problem:

### Fix 1: Always Include session_id in Status Polls

```javascript
// dashboard.html pollTask()
let url = `/api/task/${activeTask.taskId}?agent_url=${encodeURIComponent(activeTask.agentUrl)}`;
if (activeTask.sessionId) {
    url += `&session_id=${encodeURIComponent(activeTask.sessionId)}`;
}
```

**Status:** Already implemented in current code.

### Fix 2: Reconcile on Page Load

```javascript
// dashboard.html - Add to refresh()
async function reconcileWorkingSessions() {
    for (const sessionId of sessionOrder) {
        const session = sessions[sessionId];
        for (const task of session.tasks) {
            if (task.state === 'working') {
                const status = await api(`/api/task/${task.taskId}?agent_url=${session.agentUrl}&session_id=${sessionId}`);
                if (status.state !== 'working') {
                    await updateSessionTaskState(sessionId, task.taskId, status.state);
                    task.state = status.state;
                }
            }
        }
    }
}

// Call on page load
reconcileWorkingSessions();
```

### Fix 3: Handle Poll Errors Gracefully

```javascript
// dashboard.html pollTask() error handling
catch (err) {
    console.error('Task poll failed:', err);
    // Check if task exists on agent
    try {
        const history = await api(`/api/history/${activeTask.taskId}?agent_url=${activeTask.agentUrl}`);
        if (history.state) {
            await updateSessionTaskState(activeTask.sessionId, activeTask.taskId, history.state);
            activeTask = null;
        }
    } catch {
        // Task not found anywhere - mark as unknown
    }
}
```

### Fix 4: Session Persistence (Web Server)

Add file-based or SQLite persistence to SessionStore for durability across restarts.

---

## Testing the Solution

### Test Cases for Stuck Task Bug

1. **Fast completion** - Task completes in <100ms
2. **Tab close** - Close tab during task, reopen
3. **Network partition** - Simulate network failure during poll
4. **Web restart** - Restart Web during task execution
5. **Agent restart** - Restart Agent during task (expect data loss)
6. **Concurrent tasks** - Multiple sessions polling simultaneously

### Success Criteria

- No task shows "working" when Agent reports terminal state
- Page refresh always shows correct state
- Web restart preserves session data (if persistence implemented)

---

## Conclusion

The "stuck running task" bug is a symptom of **distributed state synchronization without proper primitives**. The architectural solution is to designate the Agent as the single source of truth and make the Web a stateless proxy that derives session state on demand.

This eliminates the entire class of bugs rather than patching individual failure modes.

---

## Related Documents

- [DESIGN.md](DESIGN.md) - System architecture
- [REFERENCE.md](REFERENCE.md) - API specifications
- [PLAN.md](PLAN.md) - Project roadmap
