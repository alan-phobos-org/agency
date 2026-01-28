# Kind-Based Agent Tasking

## Overview

Changed the web UI from specific-agent tasking to kind-based tasking with queue routing. Users now select an agent "kind" (claude/codex) instead of a specific agent by port number. Tasks queue automatically and are assigned to the first available agent of the requested kind using FIFO ordering.

## Problem

The previous UI required users to select a specific agent by port (e.g., "agent-9001"). This caused "agent busy" errors when the selected agent was working, even if other agents of the same kind were idle.

## Solution

### Frontend Changes

The web UI now:
- Shows only an "Agent Kind" dropdown (claude/codex)
- Removes the specific agent selection dropdown
- Submits tasks with only `agent_kind` (no `agent_url`)
- All tasks route through the queue system

### Backend Behavior

The queue dispatcher:
- Uses FIFO ordering for pending tasks
- Matches tasks to agents by kind
- Implements **strict session affinity**:
  - Follow-up tasks to an existing session MUST use the session's original agent
  - If the session's agent is busy, the task waits in queue
  - New sessions are assigned to the first available agent of the requested kind

### Queue Flow

```
User submits task with agent_kind="claude"
  ↓
Task added to queue (FIFO)
  ↓
Dispatcher polls queue (every 1 second)
  ↓
Is this a follow-up to existing session?
  YES → Wait for session's original agent to be idle
  NO  → Find first idle agent of requested kind
  ↓
Dispatch task to selected agent
  ↓
Track completion
  ↓
Remove from queue
```

## Benefits

- **No agent busy errors**: Tasks queue instead of failing
- **Better resource utilization**: Any idle agent of the correct kind can handle new tasks
- **Session continuity**: Follow-up tasks always use the same agent
- **FIFO fairness**: Tasks are dispatched in the order they were submitted
- **Backward compatible**: CLI and scheduler can still use `agent_url` for direct submission

## Implementation Details

### Files Modified

- `internal/view/web/templates/dashboard.html`: UI changes (removed agent dropdown, updated submission logic)
- `internal/view/web/dispatcher.go`: Added strict session affinity logic

### Session Affinity Logic

When a task has a `session_id`:
1. Look up the session in the session store
2. Get the session's `agent_url`
3. Check if that agent is idle
4. If idle → dispatch to that agent
5. If busy → task waits in queue (dispatcher returns without dispatching)

When a task has no `session_id`:
1. Find the first idle agent matching `agent_kind`
2. Dispatch to that agent
3. Session is created with that agent's URL

### Backward Compatibility

The backend handler `HandleTaskSubmitViaQueue` preserves two code paths:
- If `agent_url` is provided AND agent is idle → direct submission (for CLI/scheduler)
- If `agent_url` is omitted → queue submission (web UI behavior)

This ensures existing tools (CLI, scheduler) continue to work while the web UI benefits from queue routing.

## Testing

### Manual Verification

1. Submit task when all agents busy → task queues (no error)
2. Submit task with specific kind → routes to correct agent type
3. Continue session with follow-up task → uses same agent
4. Submit multiple tasks rapidly → FIFO ordering maintained

### Automated Tests

- Unit: Kind-based routing
- Unit: Strict session affinity
- Integration: Queue flow under load
- Integration: Mixed agent kinds (claude + codex)
- Regression: Backward compatibility with `agent_url`

## Configuration

No configuration changes required. The queue system uses existing settings:
- `MaxSize`: 50 tasks (default)
- `MaxAttempts`: 3 retries (default)
- `DispatchTimeout`: 30 seconds (default)
- Dispatcher poll interval: 1 second

## Metrics

Queue metrics available via `/api/queue`:
- `depth`: Number of pending tasks
- `oldest_age`: Seconds since oldest pending task
- `dispatched_count`: Number of tasks currently being worked on

## Migration Notes

No migration required. The change is transparent to users:
- Existing sessions continue to work
- In-flight tasks are unaffected
- Queue state persists across restarts

The UI change is immediate - users will see the simplified interface on next page load.
