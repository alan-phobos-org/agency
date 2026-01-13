# Task State Synchronization Design

This document analyzes stuck "working" task bugs and proposes architectural solutions.

---

## Problem

Tasks in the web UI can show "working" state when they've actually completed on the Agent. Root cause: distributed state without synchronization.

## Current Architecture

Three state layers, each can get stale independently:

| Layer | Storage | Lifecycle | Problem |
|-------|---------|-----------|---------|
| **Agent** | `agent.go:tasks` map + history | Task execution → history | Authoritative but task deleted after completion |
| **Web SessionStore** | `sessions.go` in-memory | Web server lifetime | Lost on restart, no persistence |
| **Browser** | JavaScript `sessions` object | Page session | Stale if polling fails |

## Failure Modes

1. **Fast completion** - Task moves to history before first poll
2. **Poll error** - Network failure during polling, state never updated
3. **Tab close** - User closes tab mid-task, SessionStore never updated
4. **Web restart** - SessionStore lost (in-memory only)
5. **Agent restart** - Task lost before saving to history

## Quick Fixes (Implemented)

### 1. Reconcile on Page Load
On dashboard load, check all "working" tasks against Agent. If completed (or in history), update SessionStore. Handles tab close and poll failures.

### 2. Poll Error Fallback
When polling `/api/task/{id}` fails, check `/api/history/{id}` before giving up. Handles fast completion race.

### 3. Unknown State
Tasks not found anywhere are marked "unknown" instead of stuck on "working".

---

## Architectural Solutions (Future)

### Option A: Stateless Web (Recommended)

Remove SessionStore. Derive sessions from Agent history on demand.

**Concept:**
```
GET /api/sessions
  → Query all agents' /history
  → Group by session_id
  → Include current task from /status
  → Return unified view
```

**Pros:** Eliminates stuck task bugs entirely. Simpler code (delete sessions.go).

**Cons:**
- History retention limit (100 entries) truncates old sessions
- In-progress tasks need special handling (not in history yet)
- Performance: O(agents × history) per request

**Implementation concerns:**
- Must include current running task in response
- Need efficient caching (ETag across multiple agents is complex)
- Multi-agent sessions: assumes one session = one agent

### Option B: Agent-Owned Sessions

Move session tracking to Agent. Sessions survive Agent restarts.

**Concept:**
```
Agent stores:
  sessions/{id}/metadata.json
  sessions/{id}/tasks.json
  sessions/{id}/history/

New endpoints:
  GET  /sessions           - List sessions
  GET  /sessions/{id}      - Full session with tasks
  POST /sessions/{id}/task - Create task in session
```

**Pros:**
- Sessions survive restarts
- No history retention limit affecting sessions
- Agent crash recovery possible (mark interrupted task as failed)
- Truly single source of truth

**Cons:**
- More Agent changes
- Session storage grows unbounded (needs own retention policy)
- Session concept currently only exists in Web/Browser

### Option C: Event-Driven Sync

Agent pushes state changes via SSE. Web subscribes and updates reactively.

**Pros:** Real-time updates, lower latency than polling.

**Cons:** Complex (SSE, reconnection, heartbeats). Still needs persistence for Web restarts.

---

## Recommendation

**Short term:** Quick fixes (implemented) handle 80% of cases.

**Long term:** Option B (Agent-Owned Sessions) is architecturally cleanest. The session concept should live where the work happens. This also enables:
- Session-level timeout/cancellation
- Session-level history and artifacts
- Multi-task workflows within a session

Option A is simpler to implement but has edge cases (retention limits, in-progress tasks) that complicate the solution.

---

## Open Questions

1. **Retention:** Should session retention be separate from task history retention?
2. **Multi-agent:** Will sessions ever span multiple agents?
3. **Crash tolerance:** How important is recovering from mid-task Agent crashes?
4. **Remote agents:** Will agents ever be remote (affects performance assumptions)?

---

## Related Documents

- [DESIGN.md](DESIGN.md) - System architecture
- [REFERENCE.md](REFERENCE.md) - API specifications
- [PLAN.md](PLAN.md) - Project roadmap
