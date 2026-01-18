# Project Plan

This document outlines what we're building, current status, and the roadmap.

## Vision

Agency is a modular framework for AI-powered software engineering agents. The core insight: **separate the executor (agent) from the orchestrator (director)** to enable flexible composition, better testing, and VCS-agnostic operation.

## Next Steps

### v2.0

* ag-agent-codex: `{"type":"thread.started","thread_id":"019bce81-4adf-7372-a168-b1547122749e"} {"type":"turn.started"}` results parsing needs sorting out!
* ag-web-view: agents need to show more detail in fleet pane (type etc)
* ag-web-view: agent kind selection doesn't seem to work - is that because types are wrong?
* ag-web-view: model override is unnecessary (show model-specific name though)
* ag-web-view: put 'archive' button on collapsed session (far end?)

## Delivery Phases

### Phase 2: Production Readiness - IN PROGRESS

Observability, security isolation, and multi-instance support.

#### 2.1 Observability
**Complete:**
- Per-task history storage (last 100 tasks)
- `/history` API with pagination
- Health checks via `/status` and graceful shutdown via `/shutdown`

**Remaining:**
- Structured logging (JSON) with levels
- Web UI observability integrated into the existing dashboard with progressive disclosure
- Populate tokens/cost, step/trace data, and (optionally) stream output when requested

#### 2.2 Security
- Agent auth (bind localhost, add token)
- Session ID path traversal validation
- SSRF protection in web proxy
- Sandbox isolation (bubblewrap/sandbox-exec)

### Phase 2.3: GitHub Monitor - PLANNED

Event-driven helper that watches repositories and triggers agent tasks.

**Deliverables:**
- `ag-github-monitor` binary for repo monitoring
- Watch for commits on main, CI failures, release failures
- Trigger code reviews (Sonnet) or fixes (Opus) automatically
- Quiet period (5 min) to batch rapid events
- Circuit breaker (3 failures → stop, manual reset)
- Configurable polling interval (default 60s)

**Events and Actions:**
| Event | Action | Model |
|-------|--------|-------|
| New commit | Code review + fix bugs | Sonnet |
| CI failure | Debug and push fixes | Opus |
| Release failure | Fix packaging | Opus |

See [GITHUB_MONITOR_DESIGN.md](GITHUB_MONITOR_DESIGN.md) for details.

### Phase 3: ag-director-claude MVP - PLANNED

AI-driven "manager agent" that delegates to other agents.

**Responsibilities:**
- Receive high-level goals
- Break down into implementation tasks
- Delegate coding to `ag-agent-claude` instances
- Clone and inspect codebases
- Run apps for exploratory testing

### Phase 4: Advanced Scheduler Features - PLANNED

- Task queue with workspace locking
- History for scheduled executions
- Completion tracking and retry logic

### Phase 5: GitHub/GitLab Director - PLANNED

Feature parity with h2ai v1 GitHub integration.

- Issue polling and claiming
- Branch creation and management
- PR creation with review cycle

## Backlog

### Remote Deployment & Multi-Instance

- Support dev/prod instances on same host
- Per-invocation sandbox

## Related Documents

- [DESIGN.md](DESIGN.md) - Architecture and technical design
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Scheduler architecture
- [GITHUB_MONITOR_DESIGN.md](GITHUB_MONITOR_DESIGN.md) - GitHub monitor architecture
- [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) - Task state synchronization
