# Project Plan

This document outlines what we're building, current status, and the roadmap.

## Vision

Agency is a modular framework for AI-powered software engineering agents. The core insight: **separate the executor (agent) from the orchestrator (director)** to enable flexible composition, better testing, and VCS-agnostic operation.

The name reflects both the organizational structure (agents working for directors) and the autonomy granted to AI systems.

## Current Status

**Released: v1.0.1** (2026-01-11)

Completed phases:
- Phase 1: Foundation (MVP) - Agent + CLI director with REST API
- Phase 1.1: Web View - Status dashboard with auth and task submission
- Phase 1.2: Interface-Based Architecture - Clean component taxonomy

## Delivery Phases

### Phase 1: Foundation (MVP) - COMPLETE

Working agent + CLI director with solid testing infrastructure.

**Deliverables:**
- Agent binary with REST API (`/status`, `/task`, `/task/:id`, `/task/:id/cancel`, `/shutdown`)
- CLI director for interactive prompt submission
- Unit and component test suites
- `build.sh` with all targets

**Implementation notes:**
- Agent: `internal/agent/agent.go` (~515 LOC)
- CLI: `internal/director/cli/director.go` (~163 LOC)
- Single-task model: agent rejects concurrent tasks with 409

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
- **Configurable** - Get/set config (Phase 2+)

**Component Types:**
- **Agent** (Statusable + Taskable) - ag-agent-claude
- **Director** (Statusable + Observable + Taskable) - ag-director-claude
- **Helper** (Statusable + Observable) - ag-tool-scheduler
- **View** (Statusable + Observable) - ag-view-web

### Phase 2: Production Readiness - PLANNED

Observability, security isolation, and multi-instance support.

#### 2.0 Cleanup - COMPLETE
- ~~Rename packages to remove github.com/anthropics~~ (Done: now `phobos.org.uk/agency`)

#### 2.1 Observability
- Structured logging (JSON) with levels
- Per-task history storage (last 100 tasks)
- `/history` API with pagination
- Health checks and graceful shutdown
- Fleet management CLI (`agency shutdown --all`)

#### 2.2 Security
- Agent auth (bind localhost, add token)
- Session ID path traversal validation
- SSRF protection in web proxy
- Referrer leakage prevention
- Rate limiting improvements
- Sandbox isolation: `internal/sandbox/` package
- bubblewrap (Linux) and sandbox-exec (macOS)
- Configurable read-only/read-write paths
- Fail-closed policy (no fallback to unsandboxed)

### Phase 3: ag-director-claude MVP - PLANNED

AI-driven "manager agent" that delegates to other agents.

**Responsibilities:**
- Receive high-level goals
- Break down into implementation tasks
- Delegate coding to `ag-agent-claude` instances
- Clone and inspect codebases
- Run apps for exploratory testing
- Focus on user-facing behavior validation

**NOT responsible for:**
- Writing code directly (delegates)
- Running automated test suites (implementer's job)

### Phase 4: Scheduler Director - PLANNED

Cron-based task scheduling.

- Cron expression parsing
- Task queue with workspace locking
- REST API for schedule management
- History for scheduled executions

### Phase 5: GitHub Director - PLANNED

Feature parity with h2ai v1 GitHub integration.

- Issue polling and claiming
- Branch creation and management
- PR creation with review cycle
- Status issue updates
- Failure tracking with backoff

### Phase 6+: Extensions - FUTURE

- GitLab director
- mDNS discovery
- Multi-VM coordination

## Backlog

### Task State Synchronization

Tasks can appear stuck in "working" state in the web UI due to distributed state without synchronization. Quick fixes implemented (reconciliation on load, poll error fallback). Full architectural solution needed.

**Options (see [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md)):**
- **Option A: Stateless Web** - Derive sessions from Agent history
- **Option B: Agent-Owned Sessions** - Move session tracking to Agent (recommended)

**Blockers:**
- Decision on multi-agent sessions
- Decision on session retention policy separate from history

### Remote Deployment & Multi-Instance

- Add `deploy-remote` build target with staging/prod mechanisms
- Support dev/prod instances on same host
- Per-invocation sandbox (no coordination needed)

## Related Documents

- [DESIGN.md](DESIGN.md) - Architecture and technical design
- [authentication.md](authentication.md) - Auth system design
- [security-audit.md](security-audit.md) - Security findings
