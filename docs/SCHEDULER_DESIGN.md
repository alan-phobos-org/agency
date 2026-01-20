# Scheduler Design

This document describes the minimal scheduler component for Agency.

**Related docs:**
- [DESIGN.md](DESIGN.md) - Core architecture
- [REFERENCE.md](REFERENCE.md) - API specifications

---

## Overview

The scheduler is a **Helper** component (Statusable + Observable) that triggers tasks at configured times. It runs as a standalone daemon (`ag-scheduler`) that reads a YAML configuration file specifying scheduled jobs.

### Design Goals

1. **Minimal complexity** - Cron-style scheduling with simple config
2. **Fire and forget** - Submit tasks, don't track completion
3. **Observable** - Expose status via `/status` endpoint
4. **Resilient** - Handle agent unavailability gracefully

---

## Architecture

```
┌──────────────────┐
│   ag-scheduler   │
│                  │
│  ┌────────────┐  │      ┌─────────────┐
│  │  Cron      │──┼──────│  ag-agent-  │
│  │  Runner    │  │ POST │  claude     │
│  └────────────┘  │ /task└─────────────┘
│                  │
│  ┌────────────┐  │
│  │  /status   │  │  GET /status
│  │  Handler   │◄─┼──────────────────
│  └────────────┘  │
└──────────────────┘
```

### Component Type

| Attribute | Value |
|-----------|-------|
| Type | Helper |
| Interfaces | Statusable, Observable |
| Binary | `ag-scheduler` |

---

## Configuration

### Schema

```yaml
# ag-scheduler configuration
port: 9010                    # Port for /status endpoint (dev default)
log_level: info               # debug, info, warn, error
agent_url: https://localhost:9000  # Default agent to submit tasks to

jobs:
  - name: nightly-maintenance
    schedule: "0 1 * * *"     # Cron expression (minute hour day month weekday)
    prompt: |
      Perform nightly maintenance tasks...
    model: opus               # Optional: override default model
    timeout: 2h               # Optional: task timeout
    agent_url: https://localhost:9000  # Optional: override default agent

  - name: weekly-cleanup
    schedule: "0 2 * * 0"     # Sundays at 2am
    prompt: "Clean up old logs..."
```

### Configuration Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `port` | int | No | 9100 | Status endpoint port (deployments use 9010/9110) |
| `log_level` | string | No | info | Log verbosity |
| `director_url` | string | No | - | Web director internal API URL for session tracking |
| `agent_url` | string | No | https://localhost:9000 | Default agent URL (fallback if director unavailable) |
| `jobs` | []Job | Yes | - | List of scheduled jobs |

### Web UI Integration

For scheduled jobs to appear in the web UI with proper session tracking:

1. Start the web director with an internal HTTP port:
   ```bash
   ag-view-web -internal-port=8080
   ```

2. Configure the scheduler to use this internal port:
   ```yaml
   director_url: http://localhost:8080
   ```

When `director_url` is configured, the scheduler submits to the director queue (`/api/queue/task`), which creates tracked sessions and tags jobs with `source` metadata. If the director is unavailable, the scheduler falls back to direct agent submission (sessions won't appear in web UI).

### Job Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | string | Yes | - | Unique job identifier |
| `schedule` | string | Yes | - | Cron expression |
| `prompt` | string | Yes | - | Task prompt to submit |
| `model` | string | No | sonnet | Claude model |
| `timeout` | duration | No | 30m | Task timeout |
| `agent_url` | string | No | (global) | Override agent URL |

### Cron Expression Format

Standard 5-field cron format:
```
┌───────────── minute (0-59)
│ ┌───────────── hour (0-23)
│ │ ┌───────────── day of month (1-31)
│ │ │ ┌───────────── month (1-12)
│ │ │ │ ┌───────────── day of week (0-6, 0=Sunday)
│ │ │ │ │
* * * * *
```

Examples:
- `0 1 * * *` - Every day at 1:00 AM
- `*/15 * * * *` - Every 15 minutes
- `0 2 * * 0` - Sundays at 2:00 AM
- `30 4 1 * *` - 4:30 AM on the 1st of each month

---

## API Endpoints

### GET /status

Returns scheduler status and job information.

**Response:**
```json
{
  "type": "helper",
  "interfaces": ["statusable", "observable"],
  "version": "1.0.0",
  "state": "running",
  "uptime_seconds": 3600,
  "config": {
    "agent_url": "https://localhost:9000",
    "director_url": "http://localhost:8080"
  },
  "jobs": [
    {
      "name": "nightly-maintenance",
      "schedule": "0 1 * * *",
      "next_run": "2025-01-14T01:00:00Z",
      "last_run": "2025-01-13T01:00:00Z",
      "last_status": "submitted",
      "last_task_id": "task-abc123"
    }
  ]
}
```

### POST /trigger/{job}

Manually triggers a job by name. Useful for testing scheduled jobs without waiting for the cron schedule.

**Response (200):**
```json
{
  "name": "nightly-maintenance",
  "last_status": "submitted",
  "last_task_id": "task-abc123"
}
```

**Response (404):** Job not found
**Response (409):** Job already running

### POST /shutdown

Graceful shutdown with optional drain period.

**Request:**
```json
{
  "force": false
}
```

---

## Behavior

### Task Submission

1. When a job triggers, scheduler submits to `POST /api/queue/task` on `director_url` if configured; otherwise `POST /task` to the agent
2. Scheduler logs the submission result but does not track completion
3. If agent is busy (409), scheduler logs warning and skips this run
4. If agent is unreachable, scheduler logs error and skips this run

### Resilience

- **Agent unavailable**: Log error, skip run, retry at next scheduled time
- **Agent busy**: Log warning, skip run (do not queue)
- **Config reload**: Not supported in v1 (restart required)

### Logging

All job executions are logged to stderr:
```
2025-01-13T01:00:00Z INFO  job=nightly-maintenance action=triggered
2025-01-13T01:00:00Z INFO  job=nightly-maintenance action=submitted task_id=task-abc123
2025-01-13T01:00:00Z WARN  job=weekly-cleanup action=skipped reason=agent_busy
```

---

## Implementation

### Package Structure

```
internal/scheduler/
├── config.go      # Configuration parsing and validation
├── scheduler.go   # Core scheduler logic
├── cron.go        # Cron expression parsing
└── scheduler_test.go

cmd/ag-scheduler/
└── main.go        # Entry point
```

### Dependencies

- Standard library `time` for scheduling (no external cron library)
- `net/http` for agent communication and status endpoint
- Existing `internal/api` for shared types

---

## Sample Configuration

### Nightly Maintenance

```yaml
# configs/scheduler.yaml
port: 9010
agent_url: https://localhost:9000

jobs:
  - name: nightly-maintenance
    schedule: "0 1 * * *"
    model: opus
    timeout: 2h
    prompt: |
      # Nightly Maintenance Run

      Perform the following maintenance tasks across the specified repositories.
      Work through each repository sequentially.

      ## Target Repositories
      - agency
      - opengrok-navigator
      - lldb-objc
      - cue

      ## Tasks for Each Repository

      ### 1. Code Cleanup
      - Remove dead code and unused imports
      - Fix obvious code style issues
      - Remove commented-out code blocks

      ### 2. Testing
      - Run the test suite and fix any failing tests
      - Add tests for uncovered edge cases if time permits
      - Ensure CI would pass

      ### 3. Bug Fixing
      - Check for and fix any TODO/FIXME items that are straightforward
      - Address any obvious bugs discovered during testing

      ### 4. Documentation Updates
      - Update README if code changes affect usage
      - Ensure doc comments match current behavior
      - Update CHANGELOG with any notable fixes

      ## Workflow

      For each repository:
      1. Clone from alan-phobos-org/<repo>
      2. Run tests to establish baseline
      3. Perform maintenance tasks
      4. Run tests again to verify no regressions
      5. Commit changes with descriptive messages
      6. Move to next repository

      If any repository takes too long or has complex issues,
      make a note and move on to the next one.
```

---

## Future Considerations

Not in scope for v1, but may be added later:

- **Job dependencies** - Run job B after job A completes
- **Config reload** - Reload config without restart via SIGHUP
- **Job history** - Track past executions in memory/disk
- **Multiple agents** - Round-robin or load-balanced submission

---

## Related Documents

- [DESIGN.md](DESIGN.md) - Core architecture
- [REFERENCE.md](REFERENCE.md) - API specifications
- [PLAN.md](PLAN.md) - Project roadmap
