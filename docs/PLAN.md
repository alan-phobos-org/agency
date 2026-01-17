# Project Plan

This document outlines what we're building, current status, and the roadmap.

## Vision

Agency is a modular framework for AI-powered software engineering agents. The core insight: **separate the executor (agent) from the orchestrator (director)** to enable flexible composition, better testing, and VCS-agnostic operation.

## Current Status

**Released: v1.1.0** (2026-01-13)

Completed phases:
- Phase 1: Foundation (MVP) - Agent + CLI director with REST API
- Phase 1.1: Web View - Status dashboard with auth and task submission
- Phase 1.2: Interface-Based Architecture - Clean component taxonomy
- Phase 1.3: Scheduler - Cron-style task scheduling

## Delivery Phases

### Phase 1: Foundation (MVP) - COMPLETE

Working agent + CLI director with solid testing infrastructure.

**Deliverables:**
- Agent binary with REST API (`/status`, `/task`, `/task/:id`, `/task/:id/cancel`, `/shutdown`)
- CLI director for interactive prompt submission
- Unit and component test suites
- `build.sh` with all targets

### Phase 1.1: Web View - COMPLETE

Status dashboard and task submission UI with security.

**Deliverables:**
- HTTPS web server with self-signed TLS
- Password auth + device pairing
- Port scanning discovery
- Real-time status updates (1s polling)
- Task submission with contexts

### Phase 1.2: Interface-Based Architecture - COMPLETE

Clean interface-based architecture with explicit component types.

**Core Interfaces:**
- **Statusable** - Report type, version, config (`GET /status`)
- **Taskable** - Accept prompts, execute work (`POST /task`, `GET /task/:id`)
- **Observable** - Report held tasks (`GET /tasks`)

**Component Types:**
- **Agent** (Statusable + Taskable) - ag-agent-claude
- **Director** (Statusable + Observable + Taskable) - ag-cli (CLI director)
- **Helper** (Statusable + Observable) - ag-scheduler, ag-github-monitor
- **View** (Statusable + Observable) - ag-view-web

### Phase 1.3: Scheduler - COMPLETE

Cron-based task scheduling.

**Deliverables:**
- `ag-scheduler` binary for cron-style task triggering
- Standard 5-field cron expressions
- Configurable agent URL, model, and timeout per job
- Status endpoint showing job states and next run times
- Fire-and-forget task submission

See [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) for details.

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

### Session Routing (Centralized)

Route all task submissions through the web director for unified session tracking. Scheduler and CLI should use `director_url` when available, with explicit fallback behavior when the director is unavailable.

### Task State Synchronization

Tasks can appear stuck in "working" state due to distributed state. Options in [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md).

### Remote Deployment & Multi-Instance

- Support dev/prod instances on same host
- Per-invocation sandbox

## Related Documents

- [DESIGN.md](DESIGN.md) - Architecture and technical design
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Scheduler architecture
- [GITHUB_MONITOR_DESIGN.md](GITHUB_MONITOR_DESIGN.md) - GitHub monitor architecture
- [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) - Task state synchronization
