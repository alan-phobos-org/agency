# Technical Design

This document covers the architecture and technical design of Agency.

For project roadmap and phases, see [PLAN.md](PLAN.md).

---

## Architecture

### Core Concepts

| Component | Responsibility |
|-----------|----------------|
| **Agent** | Executes a single task in a directory context using Claude. Stateless between tasks. |
| **Director** | Orchestrates agents: finds work, assigns tasks, monitors completion, handles results. |
| **View** | Dashboard for status and task submission. |
| **Registry** | Service discovery via port scanning (mDNS planned). |

### Component Types

| Type | Interfaces | Can Task Others | Examples |
|------|------------|-----------------|----------|
| **Agent** | Statusable + Taskable | No | ag-agent-claude |
| **Director** | Statusable + Observable + Taskable | Yes | ag-cli (CLI director) |
| **Helper** | Statusable + Observable | Yes (not taskable itself) | ag-scheduler |
| **View** | Statusable + Observable | Yes (tasks + observes) | ag-view-web |

### Communication Pattern

**REST API over localhost** is the right choice. Rationale:

1. **Simplicity** - HTTP is well-understood, easy to debug with curl
2. **Language agnostic** - Directors could be written in any language
3. **Observable** - Easy to add logging, metrics, tracing
4. **Stateless** - Each request is independent, agents can restart freely
5. **Testable** - Mock servers are trivial to implement

### API Design

```
# Universal endpoints (server-based components only)
GET  /status          # {type, interfaces, version, config, state}
POST /shutdown        # Graceful shutdown with drain period

# Agent endpoints
POST /task            # {prompt, timeout, session_id, project} → {task_id, session_id}
GET  /task/:id        # {state, output, exit_code, session_id}
POST /task/:id/cancel # Cancel running task
GET  /history         # Past task executions for this agent

# View endpoints (web)
GET  /api/agents      # Discovered agents
GET  /api/directors   # Discovered directors
POST /api/task        # Submit task (proxies to selected agent)
```

### Discovery Protocol

**Port scanning (current implementation):**
1. Components start on configurable port ranges (default: 9000-9010 for dev, 9100-9110 for prod)
2. On startup, scan range for `/status` endpoints
3. Cache discovered services, refresh periodically (1s for working, 5s for idle)
4. `/status` returns `type` and `interfaces` for component identification

**mDNS/DNS-SD (future):**
1. Components register via mDNS: `_agency._tcp.local`
2. TXT records contain: `type=agent` and `version=1.0.0`
3. No port range management needed

---

## State Model

### Agent States

```
idle → working → idle
  \-> cancelling -> idle
```

Agents are intentionally simple. They don't track history across tasks—that's the director's job.

### Director Responsibilities

- **Work discovery** - Poll for new issues, check schedules, accept CLI input
- **Task assignment** - Find idle agent, POST task, track execution
- **Result handling** - Create PRs, update issues, report status
- **History tracking** - Store per-task logs, metadata, artifacts

---

## Testing Strategy

### Test Hierarchy

| Level | Scope | Speed | Dependencies |
|-------|-------|-------|--------------|
| Unit | Single function | <100ms | None |
| Component | Single binary + mocks | <1s | Mock HTTP |
| Integration | Agent + Director | <30s | localhost only |
| System | Full stack | <5min | Real binaries |

### Key Principles

1. **Parallel by default** - All tests use `t.Parallel()` unless they share state
2. **No sleeps** - Use channels/conditions instead of `time.Sleep`
3. **Precompiled binaries** - Integration tests use `go build` once, not per-test
4. **Port isolation** - Tests get unique ports via `testutil.AllocateTestPort(t)`
5. **Real-time output** - Print to stderr, not t.Log() (which buffers)

### Mock Claude for Fast Tests

```go
// In test setup
t.Setenv("CLAUDE_BIN", "./testdata/mock-claude")

// testdata/mock-claude is a simple script that returns JSON
```

---

## Build System

### build.sh

Single entry point for all build operations:

```bash
./build.sh build           # Build all binaries to bin/
./build.sh test            # Unit tests only (<5s)
./build.sh test-all        # Unit + integration tests
./build.sh test-int        # Integration tests
./build.sh test-sys        # System tests (builds + runs real binaries)
./build.sh lint            # Format and lint
./build.sh check           # Full pre-commit check
./build.sh deploy-local    # Build and run local deployment
./build.sh prepare-release # Run all release checks
./build.sh release X.Y.Z   # Create release commit and tag
```

### Version from Git Tags

Following setuptools_scm philosophy:
- `v1.2.3` - Clean tag
- `v1.2.3-5-g1a2b3c4` - 5 commits after tag
- `v1.2.3-5-g1a2b3c4-dirty` - With uncommitted changes

---

## Repository Structure

```
agency/
├── build.sh              # Single build entry point
├── AGENTS.md             # Instructions for AI agents
├── README.md             # Project overview
├── CHANGELOG.md          # Release history
├── docs/                 # Documentation
│   ├── PLAN.md           # Roadmap and phases
│   ├── DESIGN.md         # This document
│   └── *.md              # Feature docs
├── cmd/
│   ├── ag-agent-claude/  # Agent binary
│   ├── ag-cli/           # CLI tool
│   ├── ag-github-monitor/  # GitHub repo event monitor
│   ├── ag-scheduler/     # Scheduler binary
│   └── ag-view-web/      # Web view binary
├── configs/              # Configuration files
├── deployment/           # Local and remote deployment scripts
├── internal/
│   ├── agent/            # Agent logic + REST API handlers
│   ├── api/              # Shared types and constants
│   ├── config/           # YAML parsing, validation
│   ├── history/          # Task history storage
│   ├── view/web/         # Web view implementation
│   └── testutil/         # Shared test helpers
└── testdata/             # Test fixtures and mock scripts
```

---

## Configuration

### Agent Configuration

```yaml
port: 9000
log_level: info
claude:
  model: sonnet      # default model (overridable per-task)
  timeout: 30m       # default timeout (overridable per-task)
  max_turns: 50      # conversation turn limit
```

### Web View Configuration

Environment variables:
- `AG_WEB_PASSWORD` - Required password for authentication
- `AG_WEB_PORT` - Port (default: 8443)

Command-line flags:
- `-port` - HTTPS port
- `-port-start`, `-port-end` - Discovery scan range
- `-contexts` - Path to contexts YAML file
- `-access-log` - Path to access log file

### Credential Storage

```
~/.agency/                    # Default location (override with AGENCY_ROOT)
├── auth-sessions.json        # Web view sessions
├── history/<agent-name>/     # Task history
└── web-director/
    ├── cert.pem              # TLS certificate
    └── key.pem               # TLS private key
```

---

## Design Patterns

### Fallback on Resource Lifecycle Transitions

When a resource moves between endpoints during its lifecycle (e.g., active → archived), clients polling the original endpoint will get 404. Proxy layers implement fallback logic:

1. Try primary endpoint (e.g., `/task/:id` for active tasks)
2. On 404, check secondary endpoint (e.g., `/history/:id` for completed tasks)
3. Return data from whichever succeeds

### Session Directories

Agents use a shared session directory instead of per-task workdirs:
- Directory: `<session_dir>/<session_id>/`
- New sessions: directory is created fresh
- Resumed sessions: directory is reused with existing state

### Embedded Instructions

Components have embedded CLAUDE.md files prepended to all prompts:
- `internal/agent/claude.md` - Agent instructions
- `internal/director/claude.md` - Director instructions

---

## Non-Functional Requirements

### Observability

**Log correlation:** Simple timestamp-based correlation. No distributed tracing until proven needed.

### Error UX (Web View)

Visual indicators for agent health:
- **Green (healthy):** Agent responding normally
- **Yellow (warning):** 1-2 consecutive failed polls
- **Red (error):** 3+ consecutive failed polls

### Scalability

**Adaptive polling:**
- Idle agents: Poll every 5 seconds
- Working agents: Poll every 1 second

### Startup Behavior

1. Agent starts, binds port immediately
2. `/status` returns `{"state": "starting"}` during initialization
3. Agent validates config and Claude CLI
4. `/status` transitions to `{"state": "idle"}` when ready
5. Task requests during `starting` return `503 Service Unavailable`

---

## Appendices

### Appendix A: Claude CLI Interface

Command format:
```bash
claude --print \
  --dangerously-skip-permissions \
  --model sonnet \
  --output-format json \
  --max-turns 50 \
  --prompt "Your task prompt here"
```

Session continuation:
```bash
claude --print \
  --dangerously-skip-permissions \
  --session-id "session-abc123" \
  --resume \
  --prompt "Continue with..."
```

### Appendix B: Error Codes

| Error Code | HTTP Status | Retryable |
|------------|-------------|-----------|
| `validation_error` | 400 | No |
| `not_found` | 404 | No |
| `agent_busy` | 409 | Yes (poll) |
| `already_completed` | 409 | No |
| `rate_limited` | 429 | Yes (backoff) |
| `internal_error` | 500 | Yes |
| `claude_error` | 502 | Yes |
| `timeout` | 504 | Yes |

### Appendix C: Sandbox Isolation (Phase 2)

Platform-specific filesystem isolation:
- **Linux:** bubblewrap with namespace isolation
- **macOS:** sandbox-exec with Seatbelt profiles

Failure policy: Sandbox setup failures cause immediate task failure. No fallback to unsandboxed execution.

---

## Related Documents

- [PLAN.md](PLAN.md) - Project roadmap and phases
- [authentication.md](authentication.md) - Auth system design
- [security-audit.md](security-audit.md) - Security findings
