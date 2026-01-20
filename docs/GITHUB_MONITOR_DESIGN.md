# GitHub Monitor Design

This document describes the GitHub repository monitor helper for Agency.

**Related docs:**
- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Similar helper pattern

---

## Overview

The GitHub monitor is a **Helper** component (Statusable + Observable) that watches repositories for events and triggers agent tasks automatically.

| Attribute | Value |
|-----------|-------|
| Type | Helper |
| Interfaces | Statusable, Observable |
| Binary | `ag-github-monitor` |

---

## Configuration

```yaml
# configs/github-monitor.yaml
port: 9020
log_level: info
director_url: http://localhost:8080
agent_url: https://localhost:9000
poll_interval: 60s
quiet_period: 5m

monitors:
  - name: agency
    owner: alan-phobos-org
    repo: agency
    watch_commits: true
    watch_ci: true
    watch_releases: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | int | 9020 | Status endpoint port |
| `director_url` | string | - | Web director for session tracking |
| `agent_url` | string | localhost:9000 | Fallback agent URL |
| `poll_interval` | duration | 60s | GitHub polling interval |
| `quiet_period` | duration | 5m | Delay before acting on events |

---

## Events and Actions

| Event | Trigger | Model | Action |
|-------|---------|-------|--------|
| Commit | New commit on `main` | Sonnet | Code review + fix bugs |
| CI Failure | Any workflow fails | Opus | Debug and push fixes |
| Release Failure | Workflow fails or missing assets | Opus | Fix packaging |

### Prompt Templates

**Commit Review:**
```
# Code Review: {{ .Repo }}

New commits have landed on main. Review and fix any bugs.

## Commits
{{ range .Commits }}
- {{ .SHA }} by {{ .Author }}: {{ .Message }}
{{ end }}

Clone from alan-phobos-org/{{ .Repo }}, review, run tests, fix issues, push.
```

**CI Failure:**
```
# CI Failure: {{ .Repo }}

Workflow {{ .WorkflowName }} failed. Run ID: {{ .RunID }}

Clone, run `gh run view {{ .RunID }} --log-failed`, fix, push.
```

**Release Failure:**
```
# Release Failure: {{ .Repo }}

Release {{ .Tag }} failed or missing assets.

Clone, investigate, fix packaging, push.
```

---

## Behavior

### Quiet Period

Prevents churn from rapid commits or flaky CI.

1. Event detected → start 5-min timer
2. Same event type arrives → reset timer
3. Timer expires → trigger action
4. Different event types have independent timers

```
T+0:00  Commit A → start timer
T+2:00  Commit B → reset timer
T+4:00  CI failure → start separate timer
T+7:00  Commit timer expires → review A+B
T+9:00  CI timer expires → fix CI
```

### Task Queue

- Sequential per-repo (avoid conflicts)
- Parallel across repos
- FIFO processing

### Circuit Breaker

Stops infinite loops when fixes don't work.

1. After fix task, check if issue recurs
2. Same issue reappears → increment failure count
3. 3 consecutive failures → open circuit, log error
4. **Manual reset required** via `POST /reset/{monitor}/{event_type}`

---

## API Endpoints

### GET /status

```json
{
  "type": "helper",
  "interfaces": ["statusable", "observable"],
  "version": "1.0.0",
  "state": "running",
  "monitors": [
    {
      "name": "agency",
      "owner": "alan-phobos-org",
      "repo": "agency",
      "last_poll": "2025-01-15T10:30:00Z",
      "events": {
        "commit": { "circuit_open": false, "consecutive_failures": 0 },
        "ci": { "circuit_open": false, "consecutive_failures": 1 },
        "release": { "circuit_open": true, "consecutive_failures": 3 }
      }
    }
  ],
  "rate_limit": { "remaining": 4850, "limit": 5000 }
}
```

### POST /reset/{monitor}/{event_type}

Reset circuit breaker. Returns 200 on success, 404 if not found.

### POST /trigger/{monitor}/{event_type}

Manual trigger (bypasses quiet period, respects circuit breaker).

### POST /shutdown

Graceful shutdown.

---

## Implementation

### Package Structure

```
internal/github-monitor/
├── config.go      # Configuration
├── monitor.go     # Core logic and HTTP server
├── poller.go      # GitHub API polling
├── events.go      # Event handling and quiet period
├── queue.go       # Task queue
├── circuit.go     # Circuit breaker
└── prompts.go     # Prompt templates

cmd/ag-github-monitor/
└── main.go
```

### GitHub CLI Commands

```bash
gh api repos/{owner}/{repo}/commits/main --jq '.sha'
gh run list --repo {owner}/{repo} --branch main --status failure --json databaseId,name,headSha,url
gh run view {run_id} --repo {owner}/{repo} --log-failed
gh release list --repo {owner}/{repo} --limit 5
gh release view {tag} --repo {owner}/{repo} --json assets
```

### Error Handling

| Scenario | Behavior |
|----------|----------|
| `GITHUB_TOKEN` not set | Fatal on startup |
| GitHub unreachable | Log, retry next poll |
| Rate limit hit | Pause until reset |
| Agent busy (409) | Keep in queue, retry |

---

## Deployment

### Environment

Requires `GITHUB_TOKEN` with `repo` or `public_repo` scope.

### Startup

```bash
ag-github-monitor -config configs/github-monitor.yaml
ag-github-monitor -config configs/github-monitor.yaml --reset-circuits
```

### Ports

- Dev: 9020
- Prod: 9120

---

## Related Documents

- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Similar helper pattern
- [PLAN.md](PLAN.md) - Project roadmap
