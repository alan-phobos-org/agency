# GitHub Monitor Design

This document describes the GitHub repository monitor helper for Agency.

**Related docs:**
- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Similar helper pattern
- [REFERENCE.md](REFERENCE.md) - API specifications

---

## Overview

The GitHub monitor is a **Helper** component (Statusable + Observable) that watches configured repositories for events (commits, CI failures, release failures) and triggers agent tasks to review code, fix issues, or repair packaging.

### Design Goals

1. **Reactive automation** - Respond to repository events automatically
2. **Churn prevention** - Quiet period prevents rapid-fire responses
3. **Failure resilience** - Circuit breaker stops after repeated failures
4. **Observable** - Expose status via `/status` endpoint
5. **Gentle polling** - Configurable intervals, respects GitHub rate limits

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                      ag-github-monitor                        │
│                                                              │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐      │
│  │   Commit    │    │     CI      │    │   Release   │      │
│  │   Watcher   │    │   Watcher   │    │   Watcher   │      │
│  └──────┬──────┘    └──────┬──────┘    └──────┬──────┘      │
│         │                  │                  │              │
│         └──────────────────┼──────────────────┘              │
│                            ▼                                 │
│                   ┌─────────────────┐                        │
│                   │  Event Router   │                        │
│                   │  + Quiet Period │                        │
│                   │  + Circuit Brkr │                        │
│                   └────────┬────────┘                        │
│                            │                                 │
│  ┌─────────────────────────┼─────────────────────────────┐  │
│  │                         ▼                              │  │
│  │              ┌─────────────────┐      Task Queue       │  │
│  │              │  Task Submitter │◄─────────────────     │  │
│  │              └────────┬────────┘                       │  │
│  └───────────────────────┼───────────────────────────────┘  │
│                          │                                   │
└──────────────────────────┼───────────────────────────────────┘
                           │ POST /task (or /api/task)
                           ▼
              ┌─────────────────────────┐
              │  ag-view-web (director) │
              │  or ag-agent-claude     │
              └─────────────────────────┘
```

### Component Type

| Attribute | Value |
|-----------|-------|
| Type | Helper |
| Interfaces | Statusable, Observable |
| Binary | `ag-github-monitor` |

---

## Configuration

### Schema

```yaml
# configs/github-monitor.yaml
port: 9020                              # Port for /status endpoint
log_level: info                         # debug, info, warn, error
director_url: http://localhost:8080     # Web director for session tracking
agent_url: http://localhost:9000        # Fallback agent URL
poll_interval: 60s                      # How often to poll GitHub
quiet_period: 5m                        # Delay before acting on events

monitors:
  - name: agency
    owner: alan-phobos-org
    repo: agency
    # Events to watch (all optional, omit to disable)
    watch_commits: true
    watch_ci: true
    watch_releases: true
```

### Configuration Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `port` | int | No | 9020 | Status endpoint port |
| `log_level` | string | No | info | Log verbosity |
| `director_url` | string | No | - | Web director internal API URL |
| `agent_url` | string | No | http://localhost:9000 | Fallback agent URL |
| `poll_interval` | duration | No | 60s | GitHub polling interval |
| `quiet_period` | duration | No | 5m | Delay before acting on events |
| `monitors` | []Monitor | Yes | - | List of repos to monitor |

### Monitor Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | string | Yes | - | Unique monitor identifier |
| `owner` | string | Yes | - | GitHub org/user |
| `repo` | string | Yes | - | Repository name |
| `watch_commits` | bool | No | false | Watch for new commits on main |
| `watch_ci` | bool | No | false | Watch for CI/workflow failures |
| `watch_releases` | bool | No | false | Watch for release failures |

---

## Event Types and Actions

### Commit Events

**Trigger:** New commit(s) detected on `main` branch.

**Quiet Period Behavior:** Timer resets on each new commit. Action triggers 5 minutes after the *last* commit in a series.

**Action:** Code review + fix bugs using **Sonnet** model.

**Prompt Template:**
```
# Code Review: {{ .Repo }}

New commits have landed on the main branch. Review the changes and fix any bugs found.

## Repository
- Owner: {{ .Owner }}
- Repo: {{ .Repo }}
- Branch: main

## Commits to Review
{{ range .Commits }}
- {{ .SHA }} by {{ .Author }}: {{ .Message }}
{{ end }}

## Instructions
1. Clone the repository from alan-phobos-org/{{ .Repo }}
2. Review the commits listed above
3. Run the test suite to verify correctness
4. If you find bugs or issues, fix them
5. If tests fail, fix them
6. Commit any fixes with clear messages
7. Push your changes

Focus on correctness and test coverage. Do not make stylistic changes unless they fix bugs.
```

### CI Failure Events

**Trigger:** Any GitHub Actions workflow run fails on `main` branch.

**Action:** Debug and push fixes using **Opus** model.

**Prompt Template:**
```
# CI Failure: {{ .Repo }}

A CI workflow has failed. Debug the issue and push fixes.

## Repository
- Owner: {{ .Owner }}
- Repo: {{ .Repo }}
- Branch: main

## Failed Workflow
- Name: {{ .WorkflowName }}
- Run ID: {{ .RunID }}
- Commit: {{ .CommitSHA }}
- URL: {{ .RunURL }}

## Instructions
1. Clone the repository from alan-phobos-org/{{ .Repo }}
2. Fetch the workflow logs using: gh run view {{ .RunID }} --log-failed
3. Analyze the failure and identify the root cause
4. Fix the issue
5. Run tests locally to verify the fix
6. Commit and push the fix

Do not disable tests or skip checks. Fix the underlying issue.
```

### Release Failure Events

**Trigger:** Release workflow fails OR release exists but is missing expected assets.

**Detection:**
1. Check for failed release workflows
2. For successful releases, verify expected assets exist (configurable patterns)

**Action:** Fix packaging using **Opus** model.

**Prompt Template:**
```
# Release Failure: {{ .Repo }}

A release has failed or is missing assets. Fix the packaging issue.

## Repository
- Owner: {{ .Owner }}
- Repo: {{ .Repo }}

## Release Details
- Tag: {{ .Tag }}
- Workflow Run: {{ .RunID }}
- Status: {{ .Status }}
{{ if .MissingAssets }}
- Missing Assets: {{ .MissingAssets }}
{{ end }}

## Instructions
1. Clone the repository from alan-phobos-org/{{ .Repo }}
2. Investigate the release failure:
   - If workflow failed: gh run view {{ .RunID }} --log-failed
   - If assets missing: check the release workflow and build scripts
3. Fix the packaging/release issue
4. Test the fix locally if possible
5. Commit and push the fix
6. The release workflow should re-run automatically, or trigger manually

Focus on the release/packaging system. Do not change application code unless necessary.
```

---

## Behavior

### Polling Loop

```
Every poll_interval:
  For each monitor:
    1. Check for new commits on main
    2. Check for failed workflow runs
    3. Check for failed/incomplete releases
    4. Queue events with timestamps
```

### Quiet Period

The quiet period prevents churn from rapid commits or flaky CI.

**Behavior:**
1. When an event is detected, start a quiet period timer
2. If another event of the **same type** arrives, reset the timer
3. When the timer expires (5 min after last event), trigger the action
4. Different event types have independent timers

**Example:**
```
T+0:00  Commit A detected → start 5min timer
T+2:00  Commit B detected → reset timer to 5min
T+4:00  CI failure detected → start separate 5min timer for CI
T+7:00  Commit timer expires → trigger code review for A+B
T+9:00  CI timer expires → trigger CI fix
```

### Task Queue

Tasks are processed sequentially per repository to avoid conflicts.

**Behavior:**
1. When an action is ready (quiet period expired), add to queue
2. If a task is already running for this repo, wait
3. Process queue in FIFO order
4. Only one task per repo runs at a time

**Cross-repo:** Different repos can have tasks running in parallel.

### Circuit Breaker

Prevents infinite loops when fixes don't work.

**State per (repo, event_type):**
- `consecutive_failures`: count of failed fix attempts
- `circuit_open`: boolean, true if breaker is open

**Behavior:**
1. After a fix task completes, check if the issue recurs
2. If same issue reappears (e.g., CI fails again), increment `consecutive_failures`
3. If `consecutive_failures >= 3`, open the circuit breaker
4. When circuit is open, log error and skip all events of that type for that repo
5. **Manual reset required** - admin must acknowledge and reset

**Reset Mechanism:**
- `POST /reset/{monitor}/{event_type}` - Reset circuit breaker
- Or restart the monitor with `--reset-circuits` flag

### GitHub API Usage

**Authentication:**
- Requires `GITHUB_TOKEN` environment variable
- Token loaded from `.env` file or environment

**Rate Limiting:**
- GitHub allows 5000 requests/hour with authentication
- With 60s polling and typical usage, well under limits
- Monitor tracks remaining rate limit and logs warnings at 10%

**CLI Commands Used:**
```bash
# Check latest commit
gh api repos/{owner}/{repo}/commits/main --jq '.sha'

# List failed workflow runs
gh run list --repo {owner}/{repo} --branch main --status failure --limit 10 --json databaseId,name,headSha,conclusion,url

# Get workflow run logs
gh run view {run_id} --repo {owner}/{repo} --log-failed

# List releases
gh release list --repo {owner}/{repo} --limit 5

# Check release assets
gh release view {tag} --repo {owner}/{repo} --json assets
```

---

## API Endpoints

### GET /status

Returns monitor status and per-repo state.

**Response:**
```json
{
  "type": "helper",
  "interfaces": ["statusable", "observable"],
  "version": "1.0.0",
  "state": "running",
  "uptime_seconds": 3600,
  "config": {
    "director_url": "http://localhost:8080",
    "agent_url": "http://localhost:9000",
    "poll_interval": "60s",
    "quiet_period": "5m"
  },
  "monitors": [
    {
      "name": "agency",
      "owner": "alan-phobos-org",
      "repo": "agency",
      "last_poll": "2025-01-15T10:30:00Z",
      "last_commit_sha": "abc123",
      "events": {
        "commit": {
          "pending": false,
          "quiet_until": null,
          "consecutive_failures": 0,
          "circuit_open": false,
          "last_task_id": "task-xyz"
        },
        "ci": {
          "pending": true,
          "quiet_until": "2025-01-15T10:35:00Z",
          "consecutive_failures": 1,
          "circuit_open": false,
          "last_task_id": "task-abc"
        },
        "release": {
          "pending": false,
          "quiet_until": null,
          "consecutive_failures": 3,
          "circuit_open": true,
          "last_task_id": "task-def"
        }
      }
    }
  ],
  "rate_limit": {
    "remaining": 4850,
    "limit": 5000,
    "reset": "2025-01-15T11:00:00Z"
  }
}
```

### POST /reset/{monitor}/{event_type}

Reset circuit breaker for a specific monitor and event type.

**Parameters:**
- `monitor`: Monitor name (e.g., "agency")
- `event_type`: One of "commit", "ci", "release"

**Response (200):**
```json
{
  "monitor": "agency",
  "event_type": "ci",
  "circuit_open": false,
  "consecutive_failures": 0
}
```

**Response (404):** Monitor or event type not found

### POST /trigger/{monitor}/{event_type}

Manually trigger an action (bypasses quiet period, respects circuit breaker).

**Response (200):**
```json
{
  "monitor": "agency",
  "event_type": "ci",
  "task_id": "task-xyz",
  "status": "submitted"
}
```

**Response (409):** Circuit breaker is open
**Response (404):** Monitor or event type not found

### POST /shutdown

Graceful shutdown.

**Request:**
```json
{
  "force": false
}
```

---

## Implementation

### Package Structure

```
internal/github-monitor/
├── config.go           # Configuration parsing and validation
├── monitor.go          # Core monitor logic and HTTP server
├── poller.go           # GitHub API polling
├── events.go           # Event types and quiet period handling
├── queue.go            # Task queue management
├── circuit.go          # Circuit breaker logic
├── prompts.go          # Prompt templates
└── monitor_test.go     # Tests

cmd/ag-github-monitor/
└── main.go             # Entry point
```

### Dependencies

- Standard library `time` for polling and timers
- Standard library `net/http` for API server and agent communication
- Standard library `os/exec` for `gh` CLI invocation
- Existing `internal/api` for shared types
- `gopkg.in/yaml.v3` for configuration (already used by scheduler)

### State Management

**In-memory state (lost on restart):**
- Quiet period timers
- Task queue
- Last known commit SHAs
- Circuit breaker state

**Rationale for no persistence:**
- Simple implementation
- On restart, monitor will re-check GitHub state
- Worst case: one redundant task submission (quiet period handles most churn)
- Circuit breaker resets on restart (acceptable for manual-reset design)

### Error Handling

| Scenario | Behavior |
|----------|----------|
| `GITHUB_TOKEN` not set | Fatal error on startup |
| GitHub API unreachable | Log error, retry next poll |
| GitHub rate limit hit | Log warning, pause polling until reset |
| Agent unreachable | Log error, keep event in queue, retry |
| Agent busy (409) | Log info, keep event in queue, retry |
| Task submission fails | Increment failure count, check circuit |

---

## Logging

All events logged to stderr with structured format:

```
2025-01-15T10:30:00Z INFO  monitor=agency event=commit sha=abc123 action=detected
2025-01-15T10:30:00Z INFO  monitor=agency event=commit action=quiet_period_started until=2025-01-15T10:35:00Z
2025-01-15T10:32:00Z INFO  monitor=agency event=commit sha=def456 action=detected quiet_period_reset
2025-01-15T10:37:00Z INFO  monitor=agency event=commit action=triggered commits=2
2025-01-15T10:37:00Z INFO  monitor=agency event=commit action=submitted task_id=task-xyz model=sonnet
2025-01-15T10:40:00Z WARN  monitor=agency event=ci action=failure_detected consecutive=2
2025-01-15T10:45:00Z ERROR monitor=agency event=release action=circuit_open consecutive_failures=3 message="manual reset required"
```

---

## Deployment

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `GITHUB_TOKEN` | Yes | GitHub personal access token |

Token permissions required:
- `repo` - Full control of private repositories (for CI logs, releases)
- Or `public_repo` - Access to public repositories only

### Startup

```bash
# Start with config file
ag-github-monitor -config configs/github-monitor.yaml

# Override port
ag-github-monitor -config configs/github-monitor.yaml -port 9025

# Reset all circuit breakers on start
ag-github-monitor -config configs/github-monitor.yaml --reset-circuits
```

### Integration with agency.sh

Add to `deployment/agency.sh`:

```bash
# Optional GitHub monitor config
GITHUB_MONITOR_CONFIG="${AG_GITHUB_MONITOR_CONFIG:-}"
GITHUB_MONITOR_PORT="${AG_GITHUB_MONITOR_PORT:-9020}"

# Start GitHub monitor (optional)
GITHUB_MONITOR_PID=""
if [ -n "$GITHUB_MONITOR_CONFIG" ] && [ -f "$GITHUB_MONITOR_CONFIG" ]; then
    echo "Starting GitHub monitor on port $GITHUB_MONITOR_PORT..."
    "$PROJECT_ROOT/bin/ag-github-monitor" -config "$GITHUB_MONITOR_CONFIG" -port "$GITHUB_MONITOR_PORT" > "$PID_DIR/github-monitor-${MODE}.log" 2>&1 &
    GITHUB_MONITOR_PID=$!
fi
```

### Port Configuration

Add to `deployment/ports.conf`:

```bash
# GitHub Monitor ports
AG_GITHUB_MONITOR_PORT_DEV=9020
AG_GITHUB_MONITOR_PORT_PROD=9120
```

---

## Sample Configuration

### Full Example

```yaml
# configs/github-monitor.yaml
port: 9020
log_level: info
director_url: http://localhost:8080
agent_url: http://localhost:9000
poll_interval: 60s
quiet_period: 5m

monitors:
  # Full monitoring for main project
  - name: agency
    owner: alan-phobos-org
    repo: agency
    watch_commits: true
    watch_ci: true
    watch_releases: true

  # CI only for supporting projects
  - name: opengrok-navigator
    owner: alan-phobos-org
    repo: opengrok-navigator
    watch_ci: true

  - name: lldb-objc
    owner: alan-phobos-org
    repo: lldb-objc
    watch_ci: true
```

---

## Security Considerations

1. **Token scope:** Use minimal required permissions
2. **Token storage:** Load from environment, never log or expose
3. **Rate limiting:** Monitor and respect GitHub limits
4. **Prompt injection:** Commit messages are included in prompts - agent must handle untrusted input
5. **Network:** All GitHub communication over HTTPS

---

## Future Considerations

Not in scope for v1, but may be added later:

- **Webhook support** - Receive push events instead of polling
- **PR monitoring** - Watch pull requests, not just main branch
- **Custom prompts** - Per-monitor prompt overrides in config
- **Slack/Discord notifications** - Alert on circuit breaker trips
- **Metrics export** - Prometheus metrics for monitoring
- **Multi-branch** - Watch branches other than main
- **Asset verification patterns** - Configurable expected release assets

---

## Related Documents

- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Similar helper pattern
- [REFERENCE.md](REFERENCE.md) - API specifications
- [PLAN.md](PLAN.md) - Project roadmap
