# agency

A modular framework for AI-powered software engineering agents. The name reflects both the organizational structure (agents working for directors) and the autonomy granted to AI systems.

---

## Executive Summary

This document outlines v2 of the h2ai agentic framework, informed by lessons from v1's 9,400-line Go codebase. The core insight: **separate the executor (agent) from the orchestrator (director)** to enable flexible composition, better testing, and VCS-agnostic operation.

---

## Build Strategy

**Approach: Prototype → Review → Phases**

Building this with Claude Code in a single shot risks subtle bugs and inconsistent architecture. Strict phase-by-phase is safe but slow. The sweet spot: build a throwaway prototype first, learn from it, then implement properly.

### Step 1: Skeleton Prototype (1-2 hours)

Ask Claude to build a minimal working skeleton:
- Agent with `/status`, `/task`, `/shutdown`
- CLI director that sends one prompt and waits
- No history, no logging, no persistence—just the happy path
- One integration test proving it works end-to-end

This validates the REST API design feels right and process management works.

### Step 2: Hands-on Review (30 min)

- Run it manually, try edge cases (kill agent mid-task, bad requests)
- Note what feels awkward or wrong
- Decide: continue building on skeleton, or restart with lessons learned

### Step 3: Implement Phases Incrementally

Each phase in a focused Claude session:
1. Start with: "Read CLAUDE.md and design doc, implement Phase X"
2. Demand tests first: "Write the integration test before implementing"
3. One phase = one session, resist scope creep
4. Commit working code before starting next phase

### Session Management Tips

- **Context**: Each session starts fresh—always point Claude at CLAUDE.md and the design doc
- **Checkpoints**: Commit after each phase passes tests
- **Focus**: Keep sessions to ~1 hour; long sessions drift

---

## Lessons from h2ai v1

### What Worked Well

1. **Shell-out philosophy** - Calling `gh`, `claude`, and `hcloud` CLIs instead of Go libraries simplified debugging (run commands manually) and eliminated dependency management.

2. **Fresh clone per issue** - Starting each task with `git reset --hard` prevented state pollution between issues. Simpler than complex branch management.

3. **Two-binary design** - Local CLI (`h2ai`) for VM lifecycle, remote daemon (`h2ai-agent`) for execution. Clean security boundary and independent testing.

4. **Human-in-the-loop** - Requiring explicit PR approval (thumbs-up reaction) prevents runaway behavior. Never auto-merge.

5. **Session preservation** - Keeping Claude's session ID across work→review→PR phases maintains context and reduces token usage.

6. **Classified error handling** - `ErrorTypeRetryable`, `ErrorTypeFatal`, `ErrorTypeRateLimit` with exponential backoff made the agent resilient.

7. **Workspace locking** - Mutex-based lock prevented conflicts between issue work and scheduled tasks.

8. **Per-iteration history** - Storing logs, diffs, and metadata per work unit enabled debugging and observability.

### What Was Problematic

1. **Tight GitHub coupling** - The agent loop is deeply tied to GitHub's issue/PR model. Adding GitLab would require significant refactoring.

2. **Embedded cloud-init** - Requires full rebuild to test VM configuration changes.

3. **Multiproject complexity** - Adding multiproject support required touching many files. The design wasn't compositional enough.

4. **Testing latency** - Integration tests require a running VM and take 3-5 minutes. Local testing with mocks is limited.

5. **Config validation** - YAML parsing happens at startup with limited validation. Errors surface late.

---

## v2 Architecture

### Core Concepts

| Component | Responsibility |
|-----------|----------------|
| **Agent** | Executes a single task in a directory context using Claude. Stateless between tasks. |
| **Director** | Orchestrates agents: finds work, assigns tasks, monitors completion, handles results. |
| **Registry** | Service discovery via multicast DNS or port scanning on localhost. |

### Component Types

**Agents** (single implementation):
- `claude-agent` - Executes prompts via Claude CLI in a given directory

**Directors** (multiple implementations):
- `cli-director` - Interactive CLI for ad-hoc tasking
- `github-director` - Watches issues/PRs, creates branches, opens PRs
- `gitlab-director` - Same pattern for GitLab
- `scheduler-director` - Cron-based task scheduling
- `web-director` - Status dashboard aggregating agent APIs
- `claude-director` - AI-driven PM for autonomous coordination (future)

**Hybrid components** - Some components may act as both agent and director (e.g., a coordinator that receives tasks and delegates subtasks). The discovery protocol handles this by having `/status` return a `roles` array.

### Communication Pattern

**REST API over localhost** is the right choice for v2. Rationale:

1. **Simplicity** - HTTP is well-understood, easy to debug with curl
2. **Language agnostic** - Directors could be written in any language
3. **Observable** - Easy to add logging, metrics, tracing
4. **Stateless** - Each request is independent, agents can restart freely
5. **Testable** - Mock servers are trivial to implement

Alternative considered: **gRPC** offers better typing and streaming, but adds complexity (protobuf compilation, harder debugging). For localhost-only communication, REST is sufficient.

### API Design

```
# Universal endpoints (all components)
GET  /status          # {roles: ["agent"|"director"], version, config, state}
POST /shutdown        # Graceful shutdown with drain period

# Agent endpoints
POST /task            # {prompt, workdir, timeout} → {task_id}
GET  /task/:id        # {state, output, exit_code}
POST /task/:id/cancel # Cancel running task
GET  /history         # Past task executions for this agent

# Director endpoints
GET  /history         # Recent task executions (director's view)
GET  /agents          # Connected agents and their states
```

### Discovery Protocol

**Option A: Port scanning (simple)**
1. Components start on ports 9000-9199 (configurable range)
2. On startup, scan range for `/status` endpoints
3. Cache discovered services, refresh periodically (30s)
4. `/status` returns `roles` array indicating capabilities

**Option B: mDNS/DNS-SD (better)**
1. Components register via mDNS: `_agency._tcp.local`
2. TXT records contain: `roles=agent,director` and `version=1.0.0`
3. Directors discover agents via mDNS query
4. No port range management needed, survives restarts

**Recommendation:** Start with port scanning for simplicity. Add mDNS when managing 5+ components becomes painful. The Go `hashicorp/mdns` library makes this straightforward.

### Fleet Management

**Graceful shutdown of all components:**

```bash
# Shutdown script iterates discovered components
agency shutdown --all              # POST /shutdown to each
agency shutdown --agents           # Only agents
agency shutdown --directors        # Only directors
agency shutdown --port 9001        # Specific component
```

Each component's `/shutdown` endpoint:
1. Stops accepting new tasks
2. Waits for in-flight tasks (30s timeout)
3. Persists state to disk
4. Exits cleanly

**Forced shutdown:** `agency kill --all` sends SIGTERM, then SIGKILL after 5s.

**Config validation:**

```bash
agency validate config.yaml       # Validate a config file
agency validate --all             # Validate all configs in AGENCY_ROOT
```

All components also validate their config at startup and exit with a clear error message if invalid.

---

## State Model

### Agent States

```
idle → working → idle
         ↓
       error → idle (after logging)
```

Agents are intentionally simple. They don't track history across tasks—that's the director's job.

### Director Responsibilities

- **Work discovery** - Poll for new issues, check schedules, accept CLI input
- **Task assignment** - Find idle agent, POST task, track execution
- **Result handling** - Create PRs, update issues, report status
- **History tracking** - Store per-task logs, metadata, artifacts

---

## Testing Strategy

Testing is the foundation of this project. The strategy prioritizes **visual feedback**, **speed**, and **isolation**.

### Test Hierarchy

| Level | Scope | Speed | Dependencies |
|-------|-------|-------|--------------|
| Unit | Single function | <100ms | None |
| Component | Single binary + mocks | <1s | Mock HTTP |
| Integration | Agent + Director | <30s | localhost only |
| System | Full stack | <5min | VM + GitHub |

### Visual Feedback Requirements

**Real-time output is mandatory.** Never buffer test output.

```go
// WRONG - buffers until test completes
t.Log("Starting test...")

// RIGHT - prints immediately to stderr
fmt.Fprintf(os.Stderr, "Starting test...\n")

// For structured output, use cargo-style formatting
fmt.Fprintf(os.Stderr, "%12s %s\n", "Compiling", "agency v0.1.0")
fmt.Fprintf(os.Stderr, "%12s %s\n", "Running", "unit tests")
```

**Progress indicators for long operations:**

```go
func withProgress(name string, fn func() error) error {
    fmt.Fprintf(os.Stderr, "%12s %s... ", "Running", name)
    start := time.Now()
    err := fn()
    if err != nil {
        fmt.Fprintf(os.Stderr, "FAILED (%s)\n", time.Since(start))
    } else {
        fmt.Fprintf(os.Stderr, "ok (%s)\n", time.Since(start))
    }
    return err
}
```

### Speed Optimizations

1. **Parallel by default** - All tests use `t.Parallel()` unless they share state
2. **No sleeps** - Use channels/conditions instead of `time.Sleep`
3. **Mock time** - Use `clock` interface for time-dependent tests
4. **Precompiled binaries** - Integration tests use `go build` once, not per-test
5. **Port reuse** - Tests get unique ports via `allocateTestPort(t)`

```go
// Port allocation using test name hash
func allocateTestPort(t *testing.T) int {
    h := fnv.New32a()
    h.Write([]byte(t.Name()))
    return 10000 + int(h.Sum32()%10000)
}
```

### Configuration Isolation

**Every test gets isolated config.** No shared state between tests.

```go
func TestSomething(t *testing.T) {
    // Create isolated config directory
    configDir := t.TempDir()

    // Reset any cached config
    config.ResetCache()

    // Set environment for this test only
    t.Setenv("AGENCY_ROOT", configDir)
    t.Setenv("AGENCY_PORT", strconv.Itoa(allocateTestPort(t)))
}
```

**Config validation tests:**

```go
func TestConfigValidation(t *testing.T) {
    tests := []struct {
        name    string
        yaml    string
        wantErr string
    }{
        {"missing port", "roles: [agent]", "port is required"},
        {"invalid role", "port: 9000\nroles: [invalid]", "unknown role"},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := config.Parse([]byte(tt.yaml))
            if tt.wantErr != "" {
                require.ErrorContains(t, err, tt.wantErr)
            } else {
                require.NoError(t, err)
            }
        })
    }
}
```

### Mock Components

```go
// MockAgent responds to API calls without running Claude
type MockAgent struct {
    port     int
    delay    time.Duration
    response string
    failRate float64
    calls    []TaskRequest  // Record for assertions
}

func (m *MockAgent) Start(t *testing.T) {
    // Starts HTTP server on m.port
    // Returns after server is accepting connections
}
```

### Test Commands

```bash
./build.sh test          # Unit tests only (<5s)
./build.sh test-all      # Unit + component (<15s)
./build.sh test-int      # Integration tests (<60s)
./build.sh test-system   # Full system tests (requires VM)
```

### What Claude Needs to Run Tests

For Claude to effectively run and debug tests:

1. **Single entry point** - `./build.sh test` runs everything
2. **Clear failure output** - Errors include file:line, expected vs actual
3. **No external services** - Unit/component tests work offline
4. **Deterministic** - Same input always produces same output
5. **Fast iteration** - Change code, run test, see result in <5s

---

## Build System

### build.sh

Single entry point for all build operations:

```bash
#!/bin/bash
set -euo pipefail

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS="-X main.version=$VERSION"

case "${1:-help}" in
    build)
        echo "Building agency $VERSION..."
        go build -ldflags "$LDFLAGS" -o bin/agency ./cmd/agency
        go build -ldflags "$LDFLAGS" -o bin/claude-agent ./cmd/claude-agent
        go build -ldflags "$LDFLAGS" -o bin/cli-director ./cmd/cli-director
        ;;
    test)
        echo "Running unit tests..."
        go test -race -short ./...
        ;;
    test-all)
        echo "Running all tests..."
        go test -race ./...
        ;;
    test-int)
        echo "Running integration tests..."
        go test -race -tags=integration ./...
        ;;
    lint)
        echo "Running linters..."
        gofmt -l -w .
        staticcheck ./...
        ;;
    check)
        # Full pre-commit check
        $0 lint && $0 test
        ;;
    clean)
        rm -rf bin/ coverage.out
        ;;
    *)
        echo "Usage: $0 {build|test|test-all|test-int|lint|check|clean}"
        ;;
esac
```

### Version from Git Tags

Following the [setuptools_scm](https://github.com/pypa/setuptools-scm) philosophy for Go:

```go
// Set at build time via -ldflags
var version = "dev"

func Version() string {
    return version
}
```

Git tag format: `v1.2.3` (standard semver)

Build produces versions like:
- `v1.2.3` - Clean tag
- `v1.2.3-5-g1a2b3c4` - 5 commits after tag
- `v1.2.3-5-g1a2b3c4-dirty` - With uncommitted changes

---

## Tooling

### Required Tools

```bash
# Install development tools
go install honnef.co/go/tools/cmd/staticcheck@latest
```

### Pre-commit Workflow

The CLAUDE.md will specify:

```markdown
## Development Workflow

Before committing, always run:
./build.sh check

This runs:
1. gofmt -w . (auto-format)
2. staticcheck ./... (lint)
3. go test -race -short ./... (unit tests)

All three must pass before committing.
```

### Editor Integration

For VSCode (in CLAUDE.md):

```markdown
## Editor Setup

Recommended VSCode settings for Go:
- Format on save: enabled
- Lint on save: staticcheck
- Test on save: disabled (too slow)
```

---

## Repository Structure

### Monorepo Layout

```
agency/
├── build.sh              # Single build entry point
├── CLAUDE.md             # Instructions for Claude
├── DESIGN.md             # This document
├── go.mod                # Single module
├── go.sum
├── cmd/
│   ├── agency/           # CLI tool (fleet management, stub)
│   │   └── main.go
│   ├── claude-agent/     # Agent binary
│   │   └── main.go
│   └── cli-director/     # CLI director binary
│       └── main.go
├── internal/
│   ├── agent/            # Agent logic + HTTP handlers
│   ├── config/           # YAML parsing, validation
│   ├── director/         # Director implementations
│   │   └── cli/          # CLI director (Phase 1)
│   └── testutil/         # Shared test helpers
└── testdata/             # Test fixtures and mock scripts
    ├── configs/          # Test config files
    ├── mock-claude       # Fast mock for tests
    └── mock-claude-slow  # Slow mock for timeout tests
```

**Future packages (Phase 2+):**
- `internal/api/` - Shared HTTP middleware (if extracted from agent)
- `internal/discovery/` - Port scanning, mDNS
- `internal/director/github/` - GitHub director
- `internal/director/scheduler/` - Cron-based director

### Versioning Strategy

For Go monorepos, the standard approach is a single `go.mod` at the root with unified versioning. This works because:

1. **All components share a release cycle** - Agent and directors are versioned together
2. **Internal packages** - Everything in `internal/` is private
3. **Single binary per cmd** - No library consumers to worry about

If you later need independent versioning (e.g., open-sourcing just the agent), you can split into [multi-module repository](https://github.com/golang/go/wiki/Modules#faqs--multi-module-repositories) with prefixed tags like `cmd/agent/v1.2.3`.

For now, keep it simple: one module, one version, one tag format (`v1.2.3`).

---

## Directory Layout (Runtime)

### Per-Instance Isolation

Every agent and director gets a dedicated root directory:

```
/home/claude/agency/
├── agents/
│   ├── agent-01/
│   │   ├── config.yaml     # Instance configuration
│   │   ├── agent.log       # Structured log output (rolling, last 1000 entries)
│   │   ├── workspaces/     # Per-task temporary directories (cleaned after task)
│   │   └── history/        # Per-task records (last 10 runs retained)
│   └── agent-02/
│       └── ...
├── directors/
│   ├── github-myrepo/
│   │   ├── config.yaml
│   │   ├── director.log
│   │   ├── state.json      # Persistent state (tracked issues, etc.)
│   │   └── history/        # Per-task records (last 10 runs retained)
│   └── scheduler-main/
│       └── ...
└── agency.log              # Fleet-level log
```

**Log rotation:**
- `agent.log` / `director.log`: Rolling buffer, last 1000 entries
- `history/`: Retains last 10 completed task records, oldest auto-deleted
- `workspaces/`: Temporary per-task directories, cleaned up after task completion

---

## Configuration

### Agent Configuration

```yaml
# /home/claude/agency/agents/agent-01/config.yaml
port: 9000
log_level: info
claude:
  model: sonnet      # default model: opus | sonnet | haiku (overridable per-task)
  timeout: 30m       # default timeout (overridable per-task)
  # All tools permitted by default - no allowed_tools restriction
```

### Director Configuration (GitHub example)

```yaml
# /home/claude/agency/directors/github-myrepo/config.yaml
type: github
name: myagent                # Director name - only issues prefixed with [myagent] are processed
port: 9100
repo: owner/repo
poll_interval: 30s
discovery:
  method: port_scan    # or: mdns
  port_range: [9000, 9199]
prompts:
  work: |
    You are working on issue #{{.Number}}: {{.Title}}
    {{.Body}}
  review: |
    Review changes. Say "LGTM" if acceptable.
settings:
  max_reviews: 3
  branch_prefix: claude/issue-
  require_approval: true
```

**Issue filtering:** The GitHub director only processes issues whose title starts with `[name]` where `name` matches the director's configured name. For example, with `name: myagent`, only issues titled `[myagent] Fix login bug` would be picked up. Issues without the prefix or with different prefixes are ignored.

### Director Configuration (Scheduler example)

```yaml
# /home/claude/agency/directors/scheduler-main/config.yaml
type: scheduler
name: scheduler-main
port: 9101
discovery:
  method: port_scan
  port_range: [9000, 9099]
schedule:
  - name: daily-cleanup
    cron: "0 2 * * *"           # 2am daily
    prompt: "Review and clean up stale branches older than 7 days"
    workdir: /home/claude/projects/myapp
  - name: weekly-deps
    cron: "0 9 * * 1"           # 9am Monday
    prompt: "Check for dependency updates and create a PR if any are available"
    workdir: /home/claude/projects/myapp
```

**Cron format:** Standard 5-field cron (`minute hour day month weekday`). Uses [robfig/cron](https://github.com/robfig/cron) syntax.

### Credential Management

Credentials are stored in a global config store, separate from per-instance configuration:

```
~/.agency/                    # Default location
├── credentials/
│   ├── claude-token          # Claude Code OAuth token
│   └── github-token          # GitHub personal access token
└── git-config                # Git user identity (name, email)
```

**Override location** with `AGENCY_ROOT` environment variable:

```bash
# Development/testing isolation
AGENCY_ROOT=~/.agency-dev agency start

# CI environment
AGENCY_ROOT=/tmp/agency-ci agency start
```

**Required credentials:**

| Credential | Source | Purpose |
|------------|--------|---------|
| `claude-token` | `claude setup-token` (1-year OAuth) | Claude Code API access |
| `github-token` | GitHub PAT with `repo` scope | Git operations, GitHub API |

**Git identity** is derived from the GitHub token automatically via API lookup, stored in `git-config`:

```yaml
# ~/.agency/git-config
name: "Claude Agent"
email: "claude@users.noreply.github.com"
```

**Credential access:** Agents read credentials directly from `AGENCY_ROOT` (or `~/.agency`) at startup. Credentials are NOT passed via the REST API—this keeps the API simple and avoids credential transmission over HTTP.

**Security considerations:**

- Credentials never written to agent logs
- Stored with `0600` permissions
- Each AGENCY_ROOT is independent (no cross-contamination between dev/prod)

---

## Development Considerations

### Language Choice

**Go remains the right choice:**

1. **Fast startup** - Sub-10ms cold start vs 200-500ms for Node/Python
2. **Single binary** - No runtime dependencies, easy deployment
3. **Strong typing** - Catches errors at compile time
4. **Concurrency** - Goroutines for parallel agent management
5. **Cross-compilation** - Build for Linux from macOS trivially

### Dependencies

**Core libraries:**

| Purpose | Library | Rationale |
|---------|---------|-----------|
| HTTP routing | [`chi`](https://github.com/go-chi/chi) | Lightweight, stdlib-compatible, path params |
| YAML config | [`yaml.v3`](https://gopkg.in/yaml.v3) | De facto standard, proven in h2ai v1 |
| Assertions | [`testify`](https://github.com/stretchr/testify) | Concise `require.Equal`, good diffs |
| HTTP testing | [`httpexpect`](https://github.com/gavv/httpexpect) | Chainable API assertions, clean integration tests |
| Cron | [`robfig/cron`](https://github.com/robfig/cron) | Battle-tested, used in h2ai v1 |

**Why these choices:**

- **chi over net/http** - Path parameters (`/task/:id`) without regex hacks. Composes with stdlib middleware. ~1k LOC, minimal footprint.
- **chi over gin** - Gin uses reflection and custom `gin.Context`. Chi uses stdlib `http.Handler` throughout—easier to test and compose.
- **testify over stdlib** - `require.Equal(t, want, got)` vs 3-line `if` blocks. Worth the dependency for readability.
- **httpexpect for API tests** - Purpose-built for REST API testing. Chainable assertions make integration tests readable.

**Not included:**

| Library | Why not |
|---------|---------|
| [Ginkgo/Gomega](https://github.com/onsi/ginkgo) | BDD style (`Describe`/`It`) adds ceremony. stdlib + testify is sufficient. |
| [Testcontainers](https://golang.testcontainers.org/) | Overkill—we don't need Docker containers. Our integration tests use real binaries on localhost. |
| [GoConvey](https://github.com/smartystreets/goconvey) | Web UI is nice but unnecessary. Adds complexity. |
| [GoMock](https://github.com/golang/mock) | Manual mocks are simpler for our use case. Consider if mock complexity grows. |

**go.mod will contain:**

```go
require (
    github.com/go-chi/chi/v5 v5.0.0
    github.com/stretchr/testify v1.9.0
    github.com/gavv/httpexpect/v2 v2.16.0
    github.com/robfig/cron/v3 v3.0.0
    gopkg.in/yaml.v3 v3.0.1
)
```

### Integration/System Testing Strategy

**httpexpect for API integration tests:**

```go
func TestAgentAPI(t *testing.T) {
    // Start agent on test port
    agent := startTestAgent(t)
    defer agent.Shutdown()

    e := httpexpect.Default(t, agent.URL)

    // Test status endpoint
    e.GET("/status").
        Expect().
        Status(http.StatusOK).
        JSON().Object().
        HasValue("state", "idle").
        HasValue("roles", []string{"agent"})

    // Submit task
    task := e.POST("/task").
        WithJSON(map[string]interface{}{
            "prompt":  "echo hello",
            "workdir": t.TempDir(),
            "timeout_seconds": 30,
        }).
        Expect().
        Status(http.StatusCreated).
        JSON().Object()

    taskID := task.Value("task_id").String().Raw()

    // Poll until complete
    eventually(t, 10*time.Second, func() bool {
        resp := e.GET("/task/{id}", taskID).
            Expect().
            Status(http.StatusOK).
            JSON().Object()
        return resp.Value("state").String().Raw() == "completed"
    })
}
```

**When to use each test level:**

| Test Type | Tools | When |
|-----------|-------|------|
| Unit | `testify` + stdlib | Pure functions, no I/O |
| Component | `httpexpect` + `httptest.Server` | Single binary, mock dependencies |
| Integration | `httpexpect` + real binaries | Agent + Director on localhost |
| System | `httpexpect` + VM | Full stack with GitHub/GitLab |

**Mock Claude for fast tests:**

For component/integration tests, mock the Claude CLI by injecting a test binary:

```go
// In test setup
t.Setenv("CLAUDE_BIN", "./testdata/mock-claude")

// testdata/mock-claude is a simple script:
// #!/bin/bash
// echo '{"session_id":"test","result":"done","exit_code":0}'
```

This lets integration tests run in <1s without real Claude calls.

### Dev vs Production Isolation

To run both versions simultaneously, use `AGENCY_ROOT` to override the default `~/.agency` location:

```bash
# Development environment
AGENCY_ROOT=~/.agency-dev ./bin/agent start

# CI/testing environment
AGENCY_ROOT=/tmp/agency-test ./bin/agent start

# Production uses default (~/.agency)
./bin/agent start
```

### Repository Checkout Strategy

**Let the agent check out code via agentic work.** Rationale:

1. **Simpler director** - No git logic in directors
2. **Consistent** - Agent uses same tools for all file operations
3. **Auditable** - Checkout appears in agent logs
4. **Flexible** - Agent can handle edge cases (submodules, LFS)

Token cost is minimal (~100-200 tokens for a git clone). Optimize later only if profiling shows it's significant.

---

## Delivery Phases

### Phase 1: Foundation (MVP) - COMPLETE

**Goal:** Working agent + CLI director with solid testing infrastructure.

**Deliverables:**
- Agent binary with REST API (`/status`, `/task`, `/task/:id`, `/task/:id/cancel`, `/shutdown`)
- CLI director that submits prompts interactively
- Unit and component test suites with visual feedback
- `build.sh` with all targets
- `CLAUDE.md` with development workflow

**Success criteria:**
- Start an agent process
- Run CLI director with a prompt passed via command line
- Director blocks until agent completes the task
- Output displayed on screen when done

**Implementation notes:**
- Agent implemented in `internal/agent/agent.go` (~515 LOC)
- CLI director in `internal/director/cli/director.go` (~163 LOC)
- Test coverage: agent (unit + integration + system), config (unit), director (needs tests)
- Session continuation fields exist but not yet wired up
- Single-task model: agent rejects concurrent tasks with 409

### Phase 2: Observability

**Goal:** Production-ready logging, history, and status reporting.

**Deliverables:**
- Structured logging (JSON) with levels
- Per-task history storage
- `/history` API endpoint
- Health checks and graceful shutdown
- Fleet management CLI (`agency shutdown --all`)

**Success criteria:**
- `/status` returns rolling log buffer with lifecycle events (task started, completed, errors) - NOT full Claude output
- `/history` returns per-run details (completed tasks, outcomes, timestamps, full output)

### Phase 3: GitHub Director

**Goal:** Feature parity with h2ai v1 GitHub integration.

**Deliverables:**
- Issue polling and claiming
- Branch creation and management
- PR creation with review cycle
- Status issue updates
- Failure tracking with backoff

**Success criteria:**
- Director starts and creates a status issue in the repo
- Monitors repo for new issues with configured label
- On new issue: assigns to agent user, adds 👀 reaction, tasks agent
- On completion: adds comment with results, creates PR if code changed
- Waits for approval (👍) or follow-up comment (more work requested)
- On shutdown: removes status issue cleanly

### Phase 4: Scheduler Director

**Goal:** Cron-based task scheduling.

**Deliverables:**
- Cron expression parsing (robfig/cron)
- Task queue with workspace locking
- Status reporting via API
- History for scheduled executions

**Success criteria:**
- Director starts with config file containing crontab-style schedules
- Agents execute at scheduled times
- REST API allows viewing current schedule (`GET /schedule`)
- REST API allows modifying schedule (`PUT /schedule`, `POST /schedule/task`)
- Schedule changes take effect without restart

### Phase 5+: Extensions

- GitLab director
- Web dashboard aggregating status
- mDNS discovery
- Multi-VM coordination
- Claude-driven director (autonomous PM)

---

## Answers to Design Questions

### 1. Is REST API the best bet?

**Yes, for this use case.** REST over localhost provides simplicity, debuggability, and language flexibility. gRPC would add complexity without proportional benefits for localhost-only communication.

### 2. Can the architecture be improved?

The agent/director separation is sound. Consider adding:
- **Task queues** - Directors queue tasks, agents pull when idle (vs push model)
- **Event sourcing** - Store all state changes as events for debugging
- **Plugin system** - Directors as plugins rather than separate binaries

However, start simple. These can be added when complexity justifies them.

### 3. What other v1 lessons apply?

- **Fresh state per task** - Don't carry state between tasks in agents
- **Human-in-the-loop** - Keep approval requirements in directors
- **Graceful shutdown** - 30s drain period for in-flight tasks
- **Error classification** - Retry transient failures, fail fast on permanent errors
- **Verbose test output** - Print to stderr, not t.Log()

### 4. How to design the testing scheme for Claude?

See the expanded **Testing Strategy** section above. Key points:
- Single command: `./build.sh test`
- Real-time stderr output (no buffering)
- Deterministic port allocation
- Config isolation per test
- Fast feedback (<5s for unit tests)

### 5. What language for components?

**Go** for all components. Benefits: fast startup, single binary, strong concurrency.

### 6. How to run dev and production simultaneously?

**Environment-based isolation:** Use `AGENCY_ROOT` to override the default `~/.agency` location:
- `AGENCY_ROOT=~/.agency-dev` for development
- `AGENCY_ROOT=/tmp/agency-test` for CI/testing
- Default `~/.agency` for production

---

## Migration Path

v2 is a clean break, not an incremental upgrade. Recommended approach:

1. Build agency in new repository
2. Port one director at a time (start with CLI, then GitHub)
3. Run h2ai and agency in parallel on different repos
4. Deprecate h2ai once agency reaches feature parity
5. Keep VM provisioning from h2ai (rename to `hcloudx`)

---

## Conclusion

agency embraces the agent/director separation to achieve modularity, testability, and VCS independence. The REST API over localhost provides simple, observable communication. Go remains the implementation language for its startup speed and deployment simplicity.

The phased delivery ensures each component is solid before building on it. Start with a working agent + CLI director, add observability, then port the GitHub integration.

---

## Appendix A: API Schemas

### Universal Endpoints

**GET /status**
```json
// Request: none

// Response 200 OK
{
  "roles": ["agent"],
  "version": "v1.2.3",
  "state": "idle",
  "uptime_seconds": 3600,
  "current_task": null,
  "config": {
    "port": 9000,
    "model": "sonnet"
  }
}

// Response when busy
{
  "roles": ["agent"],
  "version": "v1.2.3",
  "state": "working",
  "uptime_seconds": 3600,
  "current_task": {
    "id": "task-abc123",
    "started_at": "{{timestamp}}",
    "prompt_preview": "Fix the authentication bug in..."
  },
  "config": {
    "port": 9000,
    "model": "sonnet"
  }
}
```

**POST /shutdown**
```json
// Request (optional)
{
  "timeout_seconds": 30,
  "force": false
}

// Response 202 Accepted
{
  "message": "Shutdown initiated",
  "drain_timeout": 30
}

// Response 409 Conflict (task in progress, force=false)
{
  "error": "task_in_progress",
  "message": "Task task-abc123 is running. Use force=true to terminate.",
  "task_id": "task-abc123"
}
```

### Agent Endpoints

**POST /task**
```json
// Request
{
  "prompt": "Fix the authentication bug in login.go. The session token is not being validated correctly.",
  "workdir": "/home/claude/projects/myapp",
  "model": "opus",             // optional: override agent's default model
  "timeout_seconds": 1800,     // optional: override agent's default timeout
  "session_id": null,
  "env": {
    "GITHUB_TOKEN": "ghp_xxx"
  }
}

// Response 201 Created
{
  "task_id": "task-abc123",
  "status": "queued"
}

// Response 409 Conflict (agent busy)
{
  "error": "agent_busy",
  "message": "Agent is currently processing task-xyz789",
  "current_task": "task-xyz789"
}

// Response 400 Bad Request
{
  "error": "validation_error",
  "message": "workdir does not exist: /home/claude/projects/myapp"
}
```

**GET /task/:id**
```json
// Response 200 OK (completed)
{
  "task_id": "task-abc123",
  "state": "completed",
  "exit_code": 0,
  "started_at": "{{timestamp}}",
  "completed_at": "{{timestamp}}",
  "duration_seconds": 342,
  "output": "I've fixed the authentication bug...",
  "session_id": "session-def456",
  "token_usage": {
    "input": 15000,
    "output": 8500
  }
}

// Response 200 OK (in progress)
{
  "task_id": "task-abc123",
  "state": "working",
  "exit_code": null,
  "started_at": "{{timestamp}}",
  "completed_at": null,
  "duration_seconds": 120,
  "output": null,
  "session_id": "session-def456",
  "token_usage": null
}

// Response 200 OK (failed)
{
  "task_id": "task-abc123",
  "state": "failed",
  "exit_code": 1,
  "started_at": "{{timestamp}}",
  "completed_at": "{{timestamp}}",
  "duration_seconds": 75,
  "output": null,
  "error": {
    "type": "timeout",
    "message": "Task exceeded timeout of 1800 seconds"
  },
  "session_id": "session-def456",
  "token_usage": {
    "input": 5000,
    "output": 2000
  }
}

// Response 404 Not Found
{
  "error": "not_found",
  "message": "Task task-abc123 not found"
}
```

**POST /task/:id/cancel**
```json
// Request: none

// Response 200 OK
{
  "task_id": "task-abc123",
  "state": "cancelled",
  "message": "Task cancellation initiated"
}

// Response 409 Conflict (already completed)
{
  "error": "already_completed",
  "message": "Task task-abc123 has already completed",
  "final_state": "completed"
}
```

### Error Response Format

All errors follow this structure:
```json
{
  "error": "error_code",
  "message": "Human-readable description",
  "details": {}
}
```

Error codes and HTTP status mapping:
| Error Code | HTTP Status | Retryable |
|------------|-------------|-----------|
| `validation_error` | 400 | No |
| `not_found` | 404 | No |
| `agent_busy` | 409 | Yes (poll) |
| `task_in_progress` | 409 | Yes (wait) |
| `already_completed` | 409 | No |
| `rate_limited` | 429 | Yes (backoff) |
| `internal_error` | 500 | Yes |
| `claude_error` | 502 | Yes |
| `timeout` | 504 | Yes |

---

## Appendix B: Claude CLI Interface

### Invoking Claude

The agent shells out to the `claude` CLI. The binary is resolved as:
1. `CLAUDE_BIN` environment variable if set (for testing with mock)
2. `claude` from PATH (default)

Command format:

```bash
claude --print \
  --dangerously-skip-permissions \
  --model sonnet \
  --output-format json \
  --max-turns 50 \
  --prompt "Your task prompt here"
```

**Flags explained:**
- `--print` - Output result to stdout (non-interactive)
- `--dangerously-skip-permissions` - Skip tool approval prompts (all tools permitted)
- `--model` - Model selection: `opus`, `sonnet`, `haiku` (from task request or config default)
- `--output-format json` - Structured output for parsing
- `--max-turns` - Limit conversation turns

### Session Continuation

To continue an existing session:

```bash
claude --print \
  --dangerously-skip-permissions \
  --session-id "session-abc123" \
  --resume \
  --prompt "Continue with the review step..."
```

### Output Parsing

JSON output structure:
```json
{
  "session_id": "session-abc123",
  "result": "I've completed the task...",
  "exit_code": 0,
  "usage": {
    "input_tokens": 15000,
    "output_tokens": 8500
  }
}
```

### Error Detection

Parse stderr and exit codes:

| Exit Code | Meaning | Action |
|-----------|---------|--------|
| 0 | Success | Return output |
| 1 | Claude error | Parse message, may retry |
| 2 | Invalid args | Fatal, fix config |
| 124 | Timeout (if using `timeout` wrapper) | Retry with longer timeout |

**Stderr patterns to detect:**

```go
var claudeErrors = map[string]ErrorType{
    "rate limit":       ErrorTypeRateLimit,
    "context window":   ErrorTypeFatal,
    "invalid api key":  ErrorTypeFatal,
    "network error":    ErrorTypeRetryable,
    "timeout":          ErrorTypeRetryable,
}
```

---

## Appendix C: Timeout Handling

### Timeout Architecture

```
Director                    Agent                      Claude CLI
    |                         |                            |
    |--POST /task------------>|                            |
    |  timeout: 1800s         |                            |
    |                         |--exec claude-------------->|
    |                         |  (with context timeout)    |
    |                         |                            |
    |                         |<-----(running)-------------|
    |                         |                            |
    |  [1800s elapsed]        |                            |
    |                         |--SIGTERM------------------>|
    |                         |  (graceful: 10s)           |
    |                         |                            |
    |                         |<-----(cleanup)-------------|
    |                         |                            |
    |                         |--SIGKILL------------------>|
    |                         |  (if still running)        |
    |<--timeout error---------|                            |
```

### Implementation

```go
func (a *Agent) executeTask(ctx context.Context, task *Task) (*Result, error) {
    // Create timeout context
    ctx, cancel := context.WithTimeout(ctx, task.Timeout)
    defer cancel()

    // Start Claude process
    cmd := exec.CommandContext(ctx, "claude", args...)

    // Capture output
    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    err := cmd.Run()

    if ctx.Err() == context.DeadlineExceeded {
        return nil, &AgentError{
            Type:    ErrorTypeTimeout,
            Message: fmt.Sprintf("Task exceeded timeout of %s", task.Timeout),
        }
    }

    // ... handle other errors
}
```

### Graceful Cancellation

When `/task/:id/cancel` is called:

1. Set task state to `cancelling`
2. Send SIGTERM to Claude process
3. Wait up to 10s for graceful exit
4. If still running, send SIGKILL
5. Set task state to `cancelled`
6. Return partial output if available

```go
func (a *Agent) cancelTask(taskID string) error {
    task := a.currentTask
    if task == nil || task.ID != taskID {
        return ErrTaskNotFound
    }

    task.State = StateCancelling

    // Signal the process
    if task.cmd != nil && task.cmd.Process != nil {
        task.cmd.Process.Signal(syscall.SIGTERM)

        // Wait with timeout
        done := make(chan error, 1)
        go func() { done <- task.cmd.Wait() }()

        select {
        case <-done:
            // Exited gracefully
        case <-time.After(10 * time.Second):
            task.cmd.Process.Kill()
        }
    }

    task.State = StateCancelled
    return nil
}
```

---

## Appendix D: Test Fixtures

### Config Fixtures

```yaml
# testdata/configs/minimal-agent.yaml
port: 9000
claude:
  model: sonnet
  timeout: 5m
```

```yaml
# testdata/configs/full-agent.yaml
port: 9001
log_level: debug
claude:
  model: opus
  timeout: 30m
  # All tools permitted by default
```

```yaml
# testdata/configs/github-director.yaml
type: github
port: 9100
repo: testowner/testrepo
poll_interval: 5s
discovery:
  method: port_scan
  port_range: [9000, 9010]
prompts:
  work: |
    Fix issue #{{.Number}}: {{.Title}}
  review: |
    Review the changes. Say LGTM if acceptable.
settings:
  max_reviews: 2
  branch_prefix: test/issue-
```

### Mock Responses

```go
// testdata/mocks/claude_responses.go
package testdata

var ClaudeSuccessResponse = `{
  "session_id": "test-session-001",
  "result": "I've successfully completed the task. The bug was in line 42.",
  "exit_code": 0,
  "usage": {"input_tokens": 1000, "output_tokens": 500}
}`

var ClaudeErrorResponse = `{
  "session_id": "test-session-002",
  "result": "",
  "exit_code": 1,
  "error": "Rate limit exceeded. Please retry after 60 seconds."
}`

var ClaudeTimeoutResponse = "" // Empty, process killed
```

### Expected Outputs

```go
// testdata/expected/task_completed.json
{
  "task_id": "{{.TaskID}}",
  "state": "completed",
  "exit_code": 0,
  "output": "I've successfully completed the task. The bug was in line 42.",
  "token_usage": {
    "input": 1000,
    "output": 500
  }
}
```

### Test Helper Functions

```go
// internal/testutil/fixtures.go
package testutil

import (
    "os"
    "path/filepath"
    "testing"
)

// LoadFixture reads a fixture file from testdata/
func LoadFixture(t *testing.T, name string) []byte {
    t.Helper()
    path := filepath.Join("testdata", name)
    data, err := os.ReadFile(path)
    if err != nil {
        t.Fatalf("Failed to load fixture %s: %v", name, err)
    }
    return data
}

// LoadConfig loads and parses a config fixture
func LoadConfig(t *testing.T, name string) *config.Config {
    t.Helper()
    data := LoadFixture(t, filepath.Join("configs", name))
    cfg, err := config.Parse(data)
    if err != nil {
        t.Fatalf("Failed to parse config %s: %v", name, err)
    }
    return cfg
}

// TempConfigDir creates a temporary config directory with fixtures
func TempConfigDir(t *testing.T, files map[string]string) string {
    t.Helper()
    dir := t.TempDir()
    for name, content := range files {
        path := filepath.Join(dir, name)
        os.MkdirAll(filepath.Dir(path), 0755)
        os.WriteFile(path, []byte(content), 0644)
    }
    return dir
}

// MockClaudeServer starts a mock HTTP server that simulates Claude CLI behavior
func MockClaudeServer(t *testing.T, responses map[string]string) *httptest.Server {
    t.Helper()
    return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Match request to response based on prompt content
        body, _ := io.ReadAll(r.Body)
        for pattern, response := range responses {
            if strings.Contains(string(body), pattern) {
                w.Write([]byte(response))
                return
            }
        }
        w.WriteHeader(500)
        w.Write([]byte(`{"error": "no matching mock response"}`))
    }))
}
```

### Integration Test Setup

```go
// internal/testutil/integration.go
package testutil

import (
    "context"
    "testing"
    "time"
)

// IntegrationTest sets up a full agent + director for integration testing
type IntegrationTest struct {
    Agent    *agent.Agent
    Director *director.Director
    AgentURL string
    t        *testing.T
}

func NewIntegrationTest(t *testing.T) *IntegrationTest {
    t.Helper()

    agentPort := AllocateTestPort(t)
    directorPort := AllocateTestPort(t)

    // Start agent with mock Claude
    agentCfg := &config.Config{
        Port: agentPort,
        Claude: config.ClaudeConfig{
            Model:   "sonnet",
            Timeout: 30 * time.Second,
        },
    }
    agent := agent.New(agentCfg)
    go agent.Start()

    // Wait for agent to be ready
    WaitForHealthy(t, fmt.Sprintf("http://localhost:%d/status", agentPort))

    // Start director
    directorCfg := &config.Config{
        Port: directorPort,
        Discovery: config.DiscoveryConfig{
            Method:    "port_scan",
            PortRange: []int{agentPort, agentPort},
        },
    }
    director := director.NewCLI(directorCfg)
    go director.Start()

    WaitForHealthy(t, fmt.Sprintf("http://localhost:%d/status", directorPort))

    return &IntegrationTest{
        Agent:    agent,
        Director: director,
        AgentURL: fmt.Sprintf("http://localhost:%d", agentPort),
        t:        t,
    }
}

func (it *IntegrationTest) Cleanup() {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    it.Agent.Shutdown(ctx)
    it.Director.Shutdown(ctx)
}
```
