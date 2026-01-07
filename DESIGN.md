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
- `ag-agent-claude` - Executes prompts via Claude CLI in a given directory

**Directors** (multiple implementations):
- `ag-director-github` - Watches issues/PRs, creates branches, opens PRs
- `ag-director-gitlab` - Same pattern for GitLab
- `ag-director-scheduler` - Cron-based task scheduling
- `ag-director-claude` - AI-driven PM for autonomous coordination (future)

**Views:**
- `ag-view-web` - Status dashboard with task submission form (HTTPS, auth)

**Tools:**
- `ag-cli` - Interactive CLI for ad-hoc tasking, status, discovery

**Hybrid components** - Some components may act as both agent and director (e.g., a coordinator that receives tasks and delegates subtasks). The discovery protocol handles this by having `/status` return a `roles` array.

### Architectural Note: Agent vs Director Taxonomy

The agent/director split provides clear responsibilities but creates tension when a component needs both capabilities. `ag-director-claude` is the prime example - it's really a **hierarchical agent** (an agent that can delegate to other agents).

**Alternative mental model - "Agents with capabilities":**

| Component | Capabilities | Autonomy |
|-----------|--------------|----------|
| ag-agent-claude | execute | low (single task) |
| ag-director-cli | - | - (client only) |
| ag-director-web | discover | - (dashboard only) |
| ag-director-claude | execute, delegate, discover | high (breaks down goals) |
| ag-director-github | delegate, discover | medium (rule-based) |

The current naming convention is retained because:
1. Simple agents remain simple - no delegation logic pollution
2. "Director" signals "this component coordinates others"
3. Refactoring taxonomy can happen later once patterns emerge

**Key insight:** `ag-director-claude` is an *agent that happens to direct other agents*, not a director that happens to use Claude. This distinction matters for implementation - it receives the same sandboxing, task API, and lifecycle management as regular agents.

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
# Universal endpoints (server-based components only)
GET  /status          # {type, interfaces, version, config, state}
POST /shutdown        # Graceful shutdown with drain period

# Agent endpoints
POST /task            # {prompt, timeout, session_id, project} → {task_id, session_id}
GET  /task/:id        # {state, output, exit_code, session_id}
POST /task/:id/cancel # Cancel running task
GET  /history         # Past task executions for this agent

# Director endpoints (server-based directors: web, github, scheduler)
GET  /history         # Recent task executions (director's view)
GET  /agents          # Connected agents and their states
```

**Note:** Agents use a shared session directory (`<session_dir>/<session_id>/`) instead of per-task workdirs. New sessions get a fresh directory; resumed sessions reuse the existing one.

Note: The CLI director (`ag-director-cli`) is a one-shot command-line tool, not a server. It does not expose any HTTP endpoints.

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
        go build -ldflags "$LDFLAGS" -o bin/ag-agent-claude ./cmd/ag-agent-claude
        go build -ldflags "$LDFLAGS" -o bin/ag-director-cli ./cmd/ag-director-cli
        go build -ldflags "$LDFLAGS" -o bin/ag-director-web ./cmd/ag-director-web
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
│   ├── agency/              # CLI tool (fleet management, stub)
│   │   └── main.go
│   ├── ag-agent-claude/     # Agent binary
│   │   └── main.go
│   ├── ag-director-cli/     # CLI director binary
│   │   └── main.go
│   └── ag-director-web/     # Web director binary
│       └── main.go
├── internal/
│   ├── agent/            # Agent logic + HTTP handlers
│   ├── config/           # YAML parsing, validation
│   ├── director/         # Director implementations
│   │   ├── cli/          # CLI director (Phase 1)
│   │   └── web/          # Web director (Phase 1.1)
│   └── testutil/         # Shared test helpers
└── testdata/             # Test fixtures and mock scripts
    ├── configs/          # Test config files
    ├── mock-claude       # Fast mock for tests
    └── mock-claude-slow  # Slow mock for timeout tests
```

**Future packages (Phase 2+):**
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
- `history/`: Retains last 100 completed task records, oldest auto-deleted
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

### Director Configuration (Web example)

```yaml
# /home/claude/agency/directors/web-main/config.yaml
type: web
name: web-main
port: 8443
bind: 0.0.0.0                    # Listen on all interfaces
discovery:
  method: port_scan
  port_range: [9000, 9199]
  refresh_interval: 1s           # How often to poll /status endpoints
tls:
  cert: ~/.agency/web-director/cert.pem
  key: ~/.agency/web-director/key.pem
  auto_generate: true            # Generate self-signed cert if missing
```

**Environment file (`~/.agency/.env`):**
```bash
# Token for web director authentication
AG_WEB_TOKEN=a1b2c3d4e5f6...  # Generate with: openssl rand -hex 32
```

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
| Browser testing | [`go-rod/rod`](https://github.com/go-rod/rod) | Headless Chrome automation for UI tests |

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
| [Playwright](https://playwright.dev/) | Requires Node.js, MCP complexity. Rod is pure Go and simpler to integrate. |
| [chromedp](https://github.com/chromedp/chromedp) | More verbose API than Rod, less automatic waiting, worse zombie cleanup. |

**go.mod will contain:**

```go
require (
    github.com/go-chi/chi/v5 v5.0.0
    github.com/stretchr/testify v1.9.0
    github.com/gavv/httpexpect/v2 v2.16.0
    github.com/robfig/cron/v3 v3.0.0
    github.com/go-rod/rod v0.116.0  // Browser testing (Phase 5+)
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
- CLI director is a one-shot client (not a server), so it does not expose `/status`. Only server-based directors (web, github, scheduler) implement the universal `/status` endpoint.

### Phase 1.1: Web Director

**Goal:** Status dashboard and task submission UI with security.

**Binary:** `ag-director-web`

**UI Template:** [Tabler](https://preview.tabler.io/) - A premium-looking open-source template with clean, modern design. Built on Bootstrap 5 with 200+ responsive UI components. Minimal aesthetic suited for developer-facing tools.

*Alternatives considered:*
- [CoreUI](https://coreui.io/demos/bootstrap/5.0/free/) - Clean Bootstrap 5 template, actively maintained, MIT license
- [AdminLTE](https://adminlte.io/themes/v3/) - Most popular free admin template (45k GitHub stars), comprehensive components

**Role:** Dashboard only - pure UI that proxies task submissions directly to agents. No work queue or orchestration logic. This is intentionally simpler than a full director.

**Deliverables:**
- HTTPS web server binding on all interfaces (0.0.0.0)
- Self-signed TLS certificate generation on first run
- Token-based authentication (from `.env` file)
- Dashboard showing all agents/directors (via port scan + `/status` polling)
- Real-time status updates (1-second polling via JavaScript fetch)
- Task submission form to dispatch work to idle agents
- Responsive, minimal CSS embedded via `go:embed` (single binary)

**Authentication:**
- Token via URL query parameter: `?token=<secret>`
- Token via Authorization header: `Authorization: Bearer <secret>`
- Token loaded from `.env` file: `AG_WEB_TOKEN=<secret>`
- All requests without valid token get 401
- Tokens should be generated securely (e.g., `openssl rand -hex 32`)

**Why token-based auth:**
- Easier to test programmatically (curl, httptest, browser automation)
- No session management complexity
- Works well with bookmarks for quick access
- Sufficient security for localhost/LAN use with HTTPS

**TLS Configuration:**
- Self-signed cert generated to `~/.agency/web-director/cert.pem` and `key.pem`
- Cert valid for localhost, 127.0.0.1, and local hostname
- 1-year validity, RSA 2048-bit
- Regenerate with `ag-director-web --regen-cert`

**UI Components:**
1. **Agent/Director Grid** - Cards showing each component's status, current task, uptime
2. **Task Form** - Select agent, enter prompt, workdir, timeout, submit
3. ~~Task History~~ - Deferred to Phase 2 (requires `/history` endpoint)

**Testing Strategy (Three-Tier):**

| Level | Tool | Tests |
|-------|------|-------|
| Unit | `testing` + `testify` | Discovery logic, state management, helpers |
| Integration | `httptest` + mock agent/director | Auth, API endpoints, discovery with mocks |
| System | Real binaries | End-to-end with actual agent + CLI director |

**Note:** Browser testing with Rod deferred - unit and integration tests provide sufficient coverage for Phase 1.1. Browser tests add complexity (Chrome dependency, flaky waits) without proportional benefit for a simple dashboard.

**Success Criteria:**

1. **Discovery & Status Display**
   - Web director starts and scans configured port range for running agents/directors
   - Discovers agents and directors by calling `/status` on each port
   - Displays discovered components in a dashboard grid
   - Shows for each component: role, state, version, uptime, current task (if any)
   - Auto-refreshes status every 1 second via JavaScript fetch
   - Correctly identifies components by their `roles` field (agent vs director)

2. **Task Submission**
   - Task form allows selecting an idle agent from discovered agents
   - Form fields: prompt (required), workdir (required), timeout (optional), model (optional)
   - Submit POSTs to selected agent's `/task` endpoint
   - Form disabled/hidden for busy agents
   - Shows success message with task ID on successful submission
   - Shows error message if agent rejects task (busy, validation error)

3. **Task Monitoring**
   - After submitting a task, dashboard shows task state (queued → working → completed/failed)
   - Polls agent's `/task/:id` endpoint to track progress
   - Updates agent card to show "working" state with task preview
   - Displays task result (output or error) when complete
   - Task completion transitions agent back to "idle" in UI

4. **Unit Tests** (`internal/director/web/`)
   - Discovery logic: port scanning, status parsing, component classification
   - State management: tracking discovered components, handling disappearing components
   - Template rendering: verify HTML generation doesn't panic
   - Auth middleware: token validation, header vs query param

5. **Integration Tests** (with mock agent/director)
   - Start web director with mock agent responding to `/status` and `/task`
   - Verify discovery finds the mock agent
   - Verify `/api/agents` returns discovered agents
   - Verify task submission proxies to mock agent correctly
   - Verify task status polling works
   - Test auth: valid token succeeds, invalid token returns 401
   - Test discovery with multiple mock components (2 agents, 1 director)

6. **System Tests** (update existing `internal/agent/system_test.go`)
   - Add web director to existing system test infrastructure
   - Start: agent, CLI director, web director (all real binaries)
   - Web director discovers both agent and CLI director
   - Verify web director's `/api/agents` shows correct state
   - Run CLI director task, verify web director shows agent as "working"
   - After CLI task completes, verify web director shows agent as "idle"
   - Submit a new task via web director's API
   - Poll until task completes
   - Verify task output matches expected result

**API Endpoints (Web Director):**

```
GET  /status              # Universal status endpoint (roles: ["director"])
GET  /                    # Dashboard HTML page
GET  /api/status          # Web director's own status (alias for /status)
GET  /api/agents          # List discovered agents with their status
GET  /api/directors       # List discovered directors with their status
POST /api/task            # Submit task (proxies to selected agent)
     Body: {"agent_url": "http://localhost:9000", "prompt": "...", "workdir": "...", ...}
     Response: {"task_id": "...", "agent_url": "http://localhost:9000"}
GET  /api/task/:id        # Get task status (requires agent_url query param)
     Example: /api/task/task-abc123?agent_url=http://localhost:9000
```

**Task-to-Agent Mapping:**

The web director is stateless and does not maintain a mapping of task IDs to agents. Instead:
- `POST /api/task` returns both `task_id` and `agent_url` in the response
- `GET /api/task/:id` requires `agent_url` as a query parameter
- The client (dashboard JS) is responsible for storing this association

*Trade-off*: An alternative is to maintain an in-memory map of `task_id → agent_url` in the web director. This simplifies the client API (just use task_id) but adds server-side state that would be lost on restart. Since the web director is a dashboard (not a task queue), statelessness is preferred—the dashboard already tracks submitted tasks in the browser session.

**Discovery Behavior:**

- Components are discovered by scanning the configured port range and calling `/status`
- Status is polled every `refresh_interval` (default: 1s)
- A component is removed from the discovered list after 3 consecutive failed polls
- When a component reappears, it is re-added immediately on the next successful poll

**Test File Structure:**

```
internal/director/web/
├── director.go           # Main web director implementation
├── director_test.go      # Unit tests
├── discovery.go          # Port scanning + status polling
├── discovery_test.go     # Unit tests for discovery
├── handlers.go           # HTTP handlers
├── handlers_test.go      # Handler unit tests
├── integration_test.go   # Integration tests with mock components
└── templates/            # HTML templates (embedded)
    └── dashboard.html

internal/agent/
└── system_test.go        # Updated to include web director tests
```

### Phase 1.2: Interface-Based Architecture

**Goal:** Refactor to a clean interface-based architecture with explicit component types and capabilities.

#### Core Interfaces

| Interface | Purpose | Endpoints |
|-----------|---------|-----------|
| **Statusable** | Report type, version, basic config | `GET /status` |
| **Taskable** | Accept prompts, execute agentic work | `POST /task`, `GET /task/:id`, `POST /task/:id/cancel` |
| **Observable** | Report tasks held by the component | `GET /tasks` |
| **Configurable** | Get/set configuration (Phase 2+) | `GET /config`, `PUT /config` |

#### Component Types

| Type | Interfaces | Can Task Others | Examples |
|------|------------|-----------------|----------|
| **Agent** | Statusable + Taskable | No | ag-agent-claude |
| **Director** | Statusable + Observable + Taskable | Yes | ag-director-claude |
| **Helper** | Statusable + Observable | Yes (not taskable itself) | ag-tool-scheduler |
| **View** | Statusable + Observable | Yes (tasks + observes) | ag-view-web |

#### Component Renaming

| Old Name | New Name | Rationale |
|----------|----------|-----------|
| ag-director-web | **ag-view-web** | It's a view, not a director—observes + tasks but isn't taskable |
| ag-director-cli + agency | **ag-cli** | Pure CLI client, not a component |

#### Sessions

Sessions enable multi-turn conversations that persist across CLI invocations using Claude Code's `--session-id` and `--resume` flags.

**Concept:**
- Sessions are owned by directors/views
- Tasks are individual prompts within a session
- Agent generates session ID if not provided, returns it in response
- Caller passes session_id to continue conversation

**API Changes:**
- `POST /task` request: optional `session_id` field to resume
- `POST /task` response: always includes `session_id`
- Agent behavior: empty session_id → new UUID; set session_id → use `--resume`

#### Project Context

Tasks can include a project context that gets prepended to the prompt:

```json
{
  "prompt": "Fix the bug in auth",
  "project": {
    "name": "myapp",
    "prompt": "Work in https://github.com/org/myapp, reset to main..."
  }
}
```

#### Status Response Changes

```json
{
  "type": "agent",
  "interfaces": ["statusable", "taskable"],
  "version": "...",
  "state": "idle"
}
```

Note: The deprecated `roles` field has been removed. Use `type` and `interfaces` for component identification.

#### Session Directories

Agents use a shared session directory instead of per-task workdirs:
- Directory: `<session_dir>/<session_id>/` (configurable, default `/tmp/agency/sessions`)
- New sessions: directory is created fresh (cleaned if exists)
- Resumed sessions: directory is reused with existing state

This eliminates the need for callers to manage workdirs.

#### Embedded Instructions

Components have embedded CLAUDE.md files that are prepended to all prompts:
- `internal/agent/claude.md` - Agent instructions (e.g., no AI references in git commits)
- `internal/director/claude.md` - Director instructions

These ensure consistent behavior across all Claude invocations.

**Deliverables:**
- `internal/api/types.go` with shared types and constants
- Renamed ag-director-web → ag-view-web
- Merged ag-director-cli + agency → ag-cli
- Session support in agent (--session-id, --resume)
- Session directories instead of per-task workdirs
- Embedded CLAUDE.md files for agent/director instructions
- Project context support (prepended to prompt)
- Updated status responses with type/interfaces (roles removed)
- Updated discovery to filter by interface

### Phase 2: Production Readiness

**Goal:** Observability, security isolation, and multi-instance support.

#### 2.0 Cleanup

- Rename packages to remove github.com/anthropics 
- Tidy up deployment scripts so there's local and remote and one command to rebuild and deploy to each

#### 2.1 Observability

**Deliverables:**
- Structured logging (JSON) with levels
- Per-task history storage (last 100 tasks retained)
- `/history` API endpoint with pagination
- `/history/:id` endpoint for full task details
- Health checks and graceful shutdown
- Fleet management CLI (`agency shutdown --all`)

**Success criteria:**
- `/status` returns rolling log buffer with lifecycle events (task started, completed, errors) - NOT full Claude output
- `/history` returns paginated task list (default 20, max 100 per page)
- `/history/:id` returns full task details including complete logs
- History persists across agent restarts (loaded from disk)

**API Design:**
```
GET /history?page=1&limit=20    # Paginated task list (summary only)
GET /history/:id                # Full task details with logs
```

**UI Integration:**
- Dashboard shows task overview list
- Each task row has expand button (`+`)
- Expanding loads full logs on demand via `/history/:id`
- Keeps initial page load fast even with large history

#### 2.2 Agent Sandbox Isolation

**Deliverables:**
- `internal/sandbox/` package with platform-specific implementations
- bubblewrap (Linux) and sandbox-exec (macOS) support
- Configurable read-only/read-write paths

**Failure Policy:** Sandbox setup failures cause immediate task failure. No fallback to unsandboxed execution - fail closed for security.

**Success criteria:**
- Tasks cannot read/write outside workdir and explicitly allowed paths
- Existing tests pass without modification (sandbox disabled or mocked)
- Integration test verifies sandbox blocks `/etc/passwd` access
- Sandbox setup failure returns error type `sandbox_error` with clear message

**Design:** See Appendix E for full implementation details.

#### 2.3 Multi-Instance Architecture

**Goal:** Support dev/prod instances on same host without conflicts.

**Key points:**
- Sandbox is stateless and per-invocation (no coordination needed)
- No global config or daemon - each `Wrap()` call is independent
- Temp files use unique names via `os.CreateTemp`
- Agent ports already isolated via config

**Limitation:** Multiple agents share `~/.claude` config directory. For true dev/prod isolation of Claude state, use separate OS users or containers.

#### 2.4 Task Flow Improvements

**Goal:** Enable Claude sessions that can be revisited with additional context via API.

**Session Model:**
Tasks run until the caller explicitly ends them. The agent does not auto-detect "questions" or "paused" states - session lifecycle is entirely caller-controlled.

**Deliverables:**
- Session continuation support in task API
- `POST /task/:id/continue` endpoint for providing follow-up context
- `POST /task/:id/end` endpoint for explicitly ending a session
- Web director UI for continuing or ending sessions

**API Design:**
```
POST /task/:id/continue    # Send follow-up prompt to existing session
     Body: {"prompt": "Yes, proceed with option A"}
     Response: {"task_id": "...", "state": "working"}

POST /task/:id/end         # Explicitly end a session
     Response: {"task_id": "...", "state": "completed", "output": "..."}
```

**Acceptance criteria:**
- Task stays in "idle" state after Claude returns (awaiting continue or end)
- Caller sends follow-up via `/task/:id/continue`
- Claude resumes with shared session context
- Caller explicitly ends session via `/task/:id/end`
- Multiple continue calls work within same session

### Phase 3: ag-director-claude MVP

**Goal:** AI-driven "manager agent" that delegates implementation work to other agents while focusing on exploratory testing and deployment.

**Architecture:**
`ag-director-claude` is a hybrid component - both an agent (accepts tasks, runs in sandbox) and a director (discovers and delegates to other agents). It's an "agent that directs other agents" rather than a pure orchestrator.

**Core Responsibilities:**
- Receive high-level goals from users/other directors
- Break down goals into implementation tasks
- Delegate coding tasks to `ag-agent-claude` instances
- Clone and inspect codebases to evaluate progress
- Run applications to perform exploratory acceptance testing
- Focus on user-facing behavior validation, NOT unit/system test execution
- Handle deployment concerns

**Explicitly NOT responsible for:**
- Writing code directly (delegates to implementation agents)
- Running automated test suites (that's the implementation agent's job)
- The split maintains implementer/tester separation

**Discovery & Delegation:**
- Uses same port-scan discovery as web director
- Simple affinity model for agent selection (prefer previously-used agent for same workdir)
- Cannot spawn new agents, only uses existing ones

**Sandbox Requirement:**
Since `ag-director-claude` runs Claude to make decisions, it requires sandboxing just like `ag-agent-claude`. Uses same `internal/sandbox/` package with appropriate permissions for:
- Cloning repositories (read-write to workdir)
- Running applications for testing (network access)
- Inspecting file contents (read-only to delegated workdirs)

**Deliverables:**
- `ag-director-claude` binary
- Task submission/monitoring (same API as agent)
- Agent discovery and delegation logic
- Sandbox configuration for manager operations

**Success criteria:**
- Receives goal like "Fix the login bug in repo X"
- Clones repo, inspects code to understand the issue
- Delegates implementation task to an idle agent
- Monitors agent progress, inspects results
- Runs application to verify fix works from user perspective
- Reports outcome to caller

**Design notes:**
- Prototype with minimal scope first
- Start with single-agent delegation before multi-agent coordination
- Reuses existing agent infrastructure where possible

### Phase 4: GitHub Director

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

### Phase 5: Scheduler Director

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

### Phase 6+: Extensions
- GitLab director
- mDNS discovery
- Multi-VM coordination

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

## Non-Functional Requirements

### Observability

**Log correlation:** Keep it simple. Rely on timestamps and manual correlation across components. Avoid distributed tracing complexity until proven needed. Each component logs independently; operators correlate via timestamps when debugging cross-component issues.

**Structured logging** (Phase 2) will use JSON format with consistent fields (`timestamp`, `level`, `component`, `message`) but no trace IDs or span contexts.

### Error UX (Web Director)

**Visual indicators** for agent health state:
- **Green (healthy):** Agent responding normally
- **Yellow (warning):** 1-2 consecutive failed polls; show last-known status
- **Red (error):** 3+ consecutive failed polls; show "unreachable" with last-known state
- **Recovery:** Returns to green on first successful poll

This gives operators visibility into degraded state rather than silently removing unreachable agents.

### Scalability

**Adaptive polling** to reduce load:
- Idle agents: Poll every 5 seconds
- Working agents: Poll every 1 second

This reduces polling traffic by ~5x for idle agents while maintaining responsiveness for active tasks. For <20 agents this is sufficient; revisit if fleet grows significantly.

### Debug Access

**Separate debug endpoint** for raw Claude CLI output:
```
GET /task/:id/debug    # Returns raw stderr/stdout from Claude process
```

Normal `/task/:id` returns parsed, structured output. Debug endpoint returns unparsed process output for troubleshooting Claude CLI issues, encoding problems, or unexpected behavior.

Debug output is retained alongside task history and cleared with the same retention policy.

### Startup Behavior

**Health endpoint stages** for startup validation:

1. Agent starts, binds port immediately
2. `/status` returns `{"state": "starting", ...}` during initialization
3. Agent validates: config correctness, Claude CLI exists and runs `--version`
4. `/status` transitions to `{"state": "idle", ...}` when ready
5. Task requests during `starting` state return `503 Service Unavailable`

This provides:
- Fast port binding (process supervision sees it come up quickly)
- Clear signal to load balancers/discovery that agent isn't ready
- Fail-fast on config errors before accepting work
- Web director can show "starting" state in UI

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
  "model": "opus",             // optional: override agent's default model
  "timeout_seconds": 1800,     // optional: override agent's default timeout
  "session_id": null,          // optional: resume existing session
  "project": {                 // optional: project context prepended to prompt
    "name": "myapp",
    "prompt": "Work in https://github.com/org/myapp..."
  },
  "env": {
    "GITHUB_TOKEN": "ghp_xxx"
  }
}

// Response 201 Created
{
  "task_id": "task-abc123",
  "session_id": "session-def456",  // always returned
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
  "message": "prompt is required"
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

---

## Appendix E: Agent Sandbox Isolation

### Overview

Filesystem isolation for `ag-agent-claude` so each task runs in a sandbox with controlled filesystem access. Uses platform-specific mechanisms: `bubblewrap` on Linux, `sandbox-exec` on macOS.

### Design Goals

1. **Minimal core code impact** - Sandbox logic isolated to a single package
2. **Testable without sandboxing** - Tests can run with sandbox disabled
3. **Graceful degradation** - Agent works (with warning) if sandbox tools unavailable
4. **Simple abstraction** - One interface, two implementations

### Package Structure

```
internal/
├── agent/
│   └── agent.go          # Uses sandbox.Wrap() when building command
└── sandbox/
    ├── sandbox.go        # Interface + factory + config
    ├── sandbox_linux.go  # bubblewrap implementation
    ├── sandbox_darwin.go # sandbox-exec implementation
    ├── sandbox_noop.go   # Passthrough (disabled/unsupported)
    └── sandbox_test.go   # Unit tests
```

### Interface

```go
package sandbox

// Config controls what the sandboxed process can access
type Config struct {
    Workdir        string   // Read-write (required)
    ReadOnlyPaths  []string // Mounted read-only (e.g., /usr, /lib)
    ReadWritePaths []string // Additional writable paths beyond Workdir
    AllowNetwork   bool     // Permit network access (default: false)
}

// Sandbox wraps command execution with platform-specific isolation
type Sandbox interface {
    // Wrap transforms a command to run inside the sandbox.
    // Returns the wrapped command and a cleanup function.
    Wrap(cmd *exec.Cmd, cfg Config) (*exec.Cmd, func(), error)

    // Available reports if this sandbox can be used
    Available() bool

    // Name returns the sandbox implementation name
    Name() string
}

// New returns the appropriate sandbox for the current platform.
// If sandbox tools are unavailable, returns a noop sandbox.
func New() Sandbox
```

### Linux Implementation (bubblewrap)

```go
// sandbox_linux.go

func (s *bwrapSandbox) Wrap(cmd *exec.Cmd, cfg Config) (*exec.Cmd, func(), error) {
    args := []string{
        // Minimal root filesystem
        "--ro-bind", "/usr", "/usr",
        "--ro-bind", "/lib", "/lib",
        "--ro-bind", "/lib64", "/lib64",
        "--ro-bind", "/bin", "/bin",
        "--ro-bind", "/etc/resolv.conf", "/etc/resolv.conf",
        "--ro-bind", "/etc/ssl", "/etc/ssl",
        "--symlink", "/usr/bin", "/sbin",

        // Process isolation
        "--unshare-pid",
        "--unshare-uts",

        // Private temp
        "--tmpfs", "/tmp",

        // Workdir (read-write)
        "--bind", cfg.Workdir, cfg.Workdir,
        "--chdir", cfg.Workdir,
    }

    for _, p := range cfg.ReadOnlyPaths {
        args = append(args, "--ro-bind", p, p)
    }
    for _, p := range cfg.ReadWritePaths {
        args = append(args, "--bind", p, p)
    }
    if !cfg.AllowNetwork {
        args = append(args, "--unshare-net")
    }

    args = append(args, "--", cmd.Path)
    args = append(args, cmd.Args[1:]...)

    wrapped := exec.CommandContext(cmd.Context(), "bwrap", args...)
    wrapped.Dir = cmd.Dir
    wrapped.Env = cmd.Env
    wrapped.Stdin, wrapped.Stdout, wrapped.Stderr = cmd.Stdin, cmd.Stdout, cmd.Stderr

    return wrapped, func() {}, nil
}
```

### macOS Implementation (sandbox-exec)

```go
// sandbox_darwin.go

func (s *seatbeltSandbox) Wrap(cmd *exec.Cmd, cfg Config) (*exec.Cmd, func(), error) {
    profile := s.generateProfile(cfg)

    // Write profile to temp file
    profileFile, err := os.CreateTemp("", "sandbox-*.sb")
    if err != nil {
        return nil, nil, err
    }
    profileFile.WriteString(profile)
    profileFile.Close()

    cleanup := func() { os.Remove(profileFile.Name()) }

    args := []string{"-f", profileFile.Name(), cmd.Path}
    args = append(args, cmd.Args[1:]...)

    wrapped := exec.CommandContext(cmd.Context(), "sandbox-exec", args...)
    wrapped.Dir = cmd.Dir
    wrapped.Env = cmd.Env
    wrapped.Stdin, wrapped.Stdout, wrapped.Stderr = cmd.Stdin, cmd.Stdout, cmd.Stderr

    return wrapped, cleanup, nil
}

func (s *seatbeltSandbox) generateProfile(cfg Config) string {
    var b strings.Builder
    b.WriteString("(version 1)\n(deny default)\n")

    // Process execution
    b.WriteString("(allow process-exec process-fork signal)\n")

    // System read access
    b.WriteString("(allow file-read* (subpath \"/usr\") (subpath \"/Library\") ")
    b.WriteString("(subpath \"/System\") (subpath \"/bin\") (subpath \"/sbin\") ")
    b.WriteString("(literal \"/dev/null\") (literal \"/dev/random\") (literal \"/dev/urandom\"))\n")

    // Workdir: full access
    fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", cfg.Workdir)

    // Additional paths
    for _, p := range cfg.ReadOnlyPaths {
        fmt.Fprintf(&b, "(allow file-read* (subpath %q))\n", p)
    }
    for _, p := range cfg.ReadWritePaths {
        fmt.Fprintf(&b, "(allow file-read* file-write* (subpath %q))\n", p)
    }

    // Temp directories
    b.WriteString("(allow file-read* file-write* (subpath \"/private/tmp\") ")
    b.WriteString("(regex #\"^/var/folders/\"))\n")

    if cfg.AllowNetwork {
        b.WriteString("(allow network*)\n")
    }

    return b.String()
}
```

### Noop Implementation (fallback)

```go
// sandbox_noop.go

type noopSandbox struct{ reason string }

func (s *noopSandbox) Wrap(cmd *exec.Cmd, _ Config) (*exec.Cmd, func(), error) {
    return cmd, func() {}, nil
}

func (s *noopSandbox) Available() bool { return true }
func (s *noopSandbox) Name() string    { return "noop (" + s.reason + ")" }
```

### Configuration Extension

```yaml
# In agent config
sandbox:
  enabled: true           # default: true
  allow_network: true     # default: true (Claude needs API access)
  read_only_paths: []     # additional read-only paths
  read_write_paths: []    # additional writable paths
```

```go
// internal/config/config.go

type SandboxConfig struct {
    Enabled        bool     `yaml:"enabled"`
    AllowNetwork   bool     `yaml:"allow_network"`
    ReadOnlyPaths  []string `yaml:"read_only_paths"`
    ReadWritePaths []string `yaml:"read_write_paths"`
}
```

### Agent Integration

Changes to [agent.go](internal/agent/agent.go) are minimal (~15 lines):

```go
// In Agent struct
type Agent struct {
    // ... existing fields ...
    sandbox sandbox.Sandbox
}

// In New()
func New(cfg *config.Config, version string) *Agent {
    var sb sandbox.Sandbox
    if cfg.Sandbox.Enabled {
        sb = sandbox.New()
        if !sb.Available() {
            fmt.Fprintf(os.Stderr, "Warning: sandbox unavailable, running without isolation\n")
        }
    } else {
        sb = sandbox.Noop("disabled")
    }
    return &Agent{/* ... */ sandbox: sb}
}

// In executeTask() - wrap the command (around line 432)
cmd := exec.CommandContext(ctx, claudeBin, args...)
cmd.Dir = task.Workdir
// ... env setup ...

sbCfg := sandbox.Config{
    Workdir:        task.Workdir,
    ReadOnlyPaths:  a.config.Sandbox.ReadOnlyPaths,
    ReadWritePaths: a.config.Sandbox.ReadWritePaths,
    AllowNetwork:   a.config.Sandbox.AllowNetwork,
}
wrapped, cleanup, err := a.sandbox.Wrap(cmd, sbCfg)
if err != nil {
    // Fail closed - no fallback to unsandboxed execution
    task.State = StateFailed
    task.Error = &TaskError{
        Type:    "sandbox_error",
        Message: fmt.Sprintf("sandbox setup failed: %v", err),
    }
    return
}
defer cleanup()
task.cmd = wrapped
```

### Testing Strategy

**1. Unit Tests (no actual sandboxing)**

```go
func TestBwrapArgsGeneration(t *testing.T) {
    s := &bwrapSandbox{}
    cmd := exec.Command("echo", "test")
    wrapped, _, _ := s.Wrap(cmd, Config{Workdir: "/work"})

    // Verify args without executing
    require.Contains(t, wrapped.Args, "--bind")
    require.Contains(t, wrapped.Args, "/work")
}

func TestSeatbeltProfileGeneration(t *testing.T) {
    s := &seatbeltSandbox{}
    profile := s.generateProfile(Config{Workdir: "/test/work"})

    require.Contains(t, profile, "(version 1)")
    require.Contains(t, profile, `(subpath "/test/work")`)
}
```

**2. Agent Unit Tests (mock sandbox)**

```go
func TestAgentWithMockSandbox(t *testing.T) {
    mockSb := &mockSandbox{
        wrapFn: func(cmd *exec.Cmd, cfg Config) (*exec.Cmd, func(), error) {
            require.Equal(t, expectedWorkdir, cfg.Workdir)
            return cmd, func() {}, nil
        },
    }
    agent := New(cfg, "test")
    agent.sandbox = mockSb  // Inject mock
    // ... test as normal ...
}
```

**3. Integration Tests (real sandbox, mock Claude)**

```go
func TestSandboxBlocksFileAccess(t *testing.T) {
    if !sandbox.New().Available() {
        t.Skip("sandbox not available")
    }
    // Use testdata/mock-claude-escape that tries to read /etc/passwd
    // Verify it fails
}
```

**4. Test Fixtures**

```bash
# testdata/mock-claude-escape
#!/bin/bash
# Attempts to access files outside workdir
if cat /etc/passwd > /dev/null 2>&1; then
    echo "ESCAPE_SUCCEEDED"
    exit 1
else
    echo "ESCAPE_BLOCKED"
    exit 0
fi
```

### Known Challenges and Mitigations

| Challenge | Mitigation |
|-----------|------------|
| **Claude CLI dependencies** | Start with generous read-only paths; config allows adding more without code changes |
| **Claude config directory** | Add `~/.claude`, `~/.config/claude` to ReadWritePaths by default |
| **Git operations** | Workdir includes `.git`; add `~/.gitconfig` to read-only paths |
| **Network for Claude API** | `AllowNetwork: true` by default |
| **sandbox-exec deprecation** | Still works, used by major tools; monitor for alternatives |
| **Linux user namespaces** | Detect availability; fall back to noop with warning |

### Default Paths

**Linux (bubblewrap):**
- Read-only: `/usr`, `/lib`, `/lib64`, `/bin`, `/etc/ssl`, `/etc/resolv.conf`
- Read-write: workdir, `~/.claude`, `~/.config/claude`

**macOS (sandbox-exec):**
- Read-only: `/usr`, `/Library`, `/System`, `/bin`, `/sbin`, `/private/var`
- Read-write: workdir, `/private/tmp`, `/var/folders/*`, `~/.claude`

### Rollout Phases

1. **Package implementation** - Create `internal/sandbox/` with noop + interface
2. **Linux support** - Implement bubblewrap, unit tests
3. **macOS support** - Implement sandbox-exec, unit tests
4. **Agent integration** - Add SandboxConfig, integrate into executeTask()
5. **Hardening** - Escape-attempt tests, real Claude testing
