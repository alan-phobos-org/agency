# Integration Patterns

Common patterns for integrating Agency components.

**Related:** [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md), [GITHUB_MONITOR_DESIGN.md](GITHUB_MONITOR_DESIGN.md)

---

## Scheduler Integration

### Session Tracking for Scheduled Jobs

For scheduled jobs to appear in the web UI with proper session tracking:

1. Start the web director with an internal HTTP port:
   ```bash
   ag-view-web -internal-port=8080
   ```

2. Configure the scheduler to use this internal port in `configs/scheduler.yaml`:
   ```yaml
   director_url: http://localhost:8080
   ```

**Why this matters:** The scheduler routes tasks through the director to create tracked sessions. Without `director_url`, tasks fall back to direct agent submission and sessions won't appear in the web UI.

### Manual Job Triggering

Jobs can be triggered manually via `POST /trigger/{job}` on the scheduler. The web UI exposes this through the Fleet panel when helpers with jobs are discovered.

**Web UI endpoint:** `POST /api/scheduler/trigger?scheduler_url=<url>&job=<name>`

This is useful for testing scheduled jobs without waiting for the cron schedule.

---

## Helper Patterns

### Event-Driven Helpers (github-monitor)

- **Quiet period pattern**: delay action after events to batch rapid changes
- **Circuit breaker pattern**: stop after N consecutive failures, require manual reset
- **Task queue**: sequential per-repo to avoid conflicts, parallel across repos
- Use `gh` CLI for GitHub API access (requires GITHUB_TOKEN in .env)
- Model selection: Sonnet for reviews, Opus for fixes

### Handler Conventions (internal/view/web)

- HTTP handlers with chi router parameters passed explicitly
- Use `api.WriteJSON` and `api.WriteError` for responses
- Pattern: `HandleX(w, r, ...params)` for handlers with URL params
- Session store: in-memory thread-safe with `sync.RWMutex`
- Sessions can be archived (hidden from UI but kept in storage)

---

## Work Queue Integration

See [WORK_QUEUE_DESIGN.md](WORK_QUEUE_DESIGN.md) for the full design.

### Key Decisions

- **Persistence**: JSON file-based (`~/.agency/queue/pending/`, `~/.agency/queue/dispatched/`)
- **Ordering**: FIFO (no priority)
- **Agent selection**: First available idle agent
- **Queue limit**: Reject at 50 tasks (503 Service Unavailable)
- **TTL**: None (tasks wait indefinitely)

### New Task States

```go
TaskStatePending     TaskState = "pending"     // In queue, waiting for agent
TaskStateDispatching TaskState = "dispatching" // Being sent to agent
```

### Scheduler Changes with Work Queue

When work queue is implemented:
- Scheduler requires `director_url` in config
- `agent_url` remains as an explicit fallback when the director is unavailable
- Job status changes from "submitted" to "queued"
- Uses `POST /api/queue/task` instead of direct agent submission
