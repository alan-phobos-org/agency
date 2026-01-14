# agency

A modular framework for AI-powered software engineering agents.

## Vision

Deploying agents should feel like reliable infrastructure, not babysitting experiments. Prioritize operational stability (graceful failures, clear errors, predictable behavior) over feature breadth. The web UI should be immediately usable without docs. Security defaults are paranoid - assume hostile networks. UI responsiveness must never feel sluggish.

## Documentation

| Document | Purpose | Read When |
|----------|---------|-----------|
| [AGENTS.md](AGENTS.md) | Development workflow, quick reference | Always |
| [README.md](README.md) | Project overview, quick start | Getting started |
| [docs/REFERENCE.md](docs/REFERENCE.md) | API specs, endpoints, config | Implementing API changes |
| [docs/DESIGN.md](docs/DESIGN.md) | Architecture, patterns | Major refactoring |
| [docs/SCHEDULER_DESIGN.md](docs/SCHEDULER_DESIGN.md) | Scheduler architecture | Modifying scheduler |
| [docs/SESSION_ROUTING_DESIGN.md](docs/SESSION_ROUTING_DESIGN.md) | Centralized session routing | Implementing session routing |
| [docs/PLAN.md](docs/PLAN.md) | Vision, phases, backlog | Planning work |
| [docs/DEBUGGING_DEPLOYED.md](docs/DEBUGGING_DEPLOYED.md) | Remote system diagnostics | Debugging deployed systems |
| [CHANGELOG.md](CHANGELOG.md) | Release history | Preparing releases |

## Quick Reference

### Build Commands

| Command | Purpose |
|---------|---------|
| `./build.sh check` | **Pre-commit** (format + lint + test) |
| `./build.sh build` | Build all binaries to bin/ |
| `./build.sh test` | Unit tests only (<5s) |
| `./build.sh test-all` | Unit + integration tests |
| `./build.sh lint` | Format and lint |
| `./build.sh deploy-local` | Build and run local deployment |

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `AG_WEB_PASSWORD` | Web view login (required) |
| `AGENCY_ROOT` | Config directory (default: ~/.agency) |
| `CLAUDE_BIN` | Claude CLI path (default: from PATH) |

## Workflows

| Trigger | Action |
|---------|--------|
| Before any commit | `./build.sh check` |
| "what's next", "status" | `./build.sh status` → read `docs/PLAN.md` → summarize (10-15 lines) |
| "prepare release" | `./build.sh prepare-release` → update CHANGELOG.md → `./build.sh release X.Y.Z` → push |
| "deploy locally" | `./deployment/agency.sh` (uses AG_WEB_PASSWORD from .env) |
| "stop services" | `./deployment/stop-agency.sh` |

---

## CRITICAL: Git Commit Messages

**NEVER include any AI/agent identifiers in commit messages.** This applies to ALL commits, especially releases.

Forbidden in commit messages:
- "Claude", "Anthropic", "AI", "LLM", "Codex", "GPT", "OpenAI", "Gemini", "Copilot"
- "generated", "automated", "assisted by", "with help from"
- Co-Authored-By headers mentioning AI
- "Generated with [tool name]" footers
- Any emoji

Write commit messages as a human developer would:
- Focus on WHAT changed and WHY
- Use conventional commit format (feat:, fix:, refactor:, etc.)
- Keep messages concise and professional

**This rule is absolute and applies to every commit including releases and version bumps.**

## Project Structure

```
agency/
├── cmd/
│   ├── ag-agent-claude/  # Agent binary (wraps Claude CLI)
│   ├── ag-cli/           # CLI tool (task, status, discover)
│   ├── ag-scheduler/     # Scheduler binary (cron-style task triggering)
│   └── ag-view-web/      # Web view binary (HTTPS dashboard)
├── configs/              # Configuration files (contexts.yaml, scheduler.yaml)
├── deployment/           # Local and remote deployment scripts
├── internal/
│   ├── agent/      # Agent logic + REST API handlers
│   ├── api/        # Shared types and constants
│   ├── config/     # YAML parsing, validation
│   ├── history/    # Task history storage and outline extraction
│   ├── scheduler/  # Scheduler logic, cron parsing, job runner
│   ├── view/web/   # Web view (dashboard + discovery)
│   └── testutil/   # Test helpers
└── testdata/       # Test fixtures and mock Claude scripts
```

---

## Testing [READ IF: implementing features, fixing bugs, debugging]

- Tests use `t.Parallel()` unless sharing state
- Use `testutil.AllocateTestPort(t)` for unique ports
- Mock Claude CLI via `CLAUDE_BIN` env var pointing to `testdata/mock-claude`
- Print progress to stderr, not t.Log()
- Use production-style IDs in tests (e.g., `task-abc123` not `test123`)

### Race Condition Prevention

Tests run with `-race` flag in CI. When using `httptest.NewServer` with shared variables:

```go
// WRONG - race condition between handler goroutine and test goroutine
var count int
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    count++  // Write from handler goroutine
}))
assert.Equal(t, 1, count)  // Read from test goroutine - RACE!

// CORRECT - use sync/atomic for cross-goroutine access
var count int32
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    atomic.AddInt32(&count, 1)  // Atomic write
}))
assert.Equal(t, int32(1), atomic.LoadInt32(&count))  // Atomic read
```

**Rule**: Any variable accessed from both an HTTP handler and the test function MUST use `sync/atomic` or a mutex.

### Test Commands

| Command | Purpose | When to Use |
|---------|---------|-------------|
| `./build.sh test` | Unit tests (<5s) | Quick validation |
| `./build.sh test-all` | Unit + integration | Before PR |
| `./build.sh test-int` | Integration only | Testing API changes |
| `./build.sh test-sys` | System tests | End-to-end validation |
| `./build.sh test-release` | Full suite | Before release |

### Coverage by Package

| Package | Tests |
|---------|-------|
| internal/agent | Unit + Integration + System |
| internal/config | Unit (validation) |
| internal/history | Unit (storage, pruning) |
| internal/scheduler | Unit (cron, config, job submission) |
| internal/view/web | Unit + Integration + System |
| cmd/* | None (thin entry points) |

---

## Release Process [READ IF: user explicitly requests release]

```bash
# 1. Run all checks
./build.sh prepare-release

# 2. Update CHANGELOG.md (add: ## [X.Y.Z] - YYYY-MM-DD)

# 3. Review docs for completed TODOs

# 4. Create release
./build.sh release X.Y.Z

# 5. Push
git push origin main vX.Y.Z
```

The `prepare-release` target runs: check, test-all, test-sys, local deployment test, shows git log.

The `release` target: validates semver, checks CHANGELOG.md entry, creates commit and tag.

---

## Component Overview [READ IF: implementing new features or debugging architecture]

### Current Phase: 1.3 (Complete)

- **Agent**: Single-task executor with REST API, session support, auto-resume
- **CLI**: `ag-cli task|status|discover` commands
- **Web View**: HTTPS dashboard with auth, discovery, task submission, contexts
- **Scheduler**: Cron-style task triggering (`ag-scheduler -config configs/scheduler.yaml`)
  - Standard 5-field cron expressions
  - Configurable agent URL, model, and timeout per job
  - Status endpoint at `/status` showing job states and next run times

### Key Behaviors

- Agent returns 409 if busy (single-task only)
- Web view discovers agents via port scanning (9000-9199)
- Sessions persist in shared directories for multi-turn conversations
- Task history stored at `~/.agency/history/<agent>/`

For detailed endpoint specs, see [docs/REFERENCE.md](docs/REFERENCE.md).

---

## Known Limitations

- Single-task agent (returns 409 if busy)
- No structured logging (stderr only)
- Task session data stored in memory (not persisted across web view restarts)
- Tasks can appear stuck in "working" state - see [docs/TASK_STATE_SYNC_DESIGN.md](docs/TASK_STATE_SYNC_DESIGN.md)

---

## Architecture Patterns [READ IF: debugging state sync issues]

### State Management

The system has three state layers that must stay synchronized:

1. **Agent** (`internal/agent/agent.go`) - Authoritative source, tasks map + history
2. **Web SessionStore** (`internal/view/web/sessions.go`) - In-memory cache, volatile
3. **Browser** (`dashboard.html` Alpine.js) - UI state, polls via `/api/task`

**Key insight:** Agent is the source of truth. Web and Browser are caches that can get stale.

### Common Pitfalls

1. **Fast task completion** - Task moves to history before first poll
2. **Missing session_id in polls** - Prevents auto-update of SessionStore
3. **Tab close during task** - No cleanup, SessionStore stays stale
4. **Web restart** - SessionStore lost (in-memory only)

### Design Principle

When in doubt, query the Agent. The Web's `/api/task/{id}` handler falls back to `/history/{id}` when task not found - this pattern should be used consistently.

---

## Web UI Architecture [READ IF: modifying the dashboard]

### Alpine.js Dashboard

The web UI (`internal/view/web/templates/dashboard.html`) uses Alpine.js for reactive state management. Key design decisions:

**State Management:**
- Single Alpine.js component (`dashboard()`) manages all UI state
- Uses ETags for efficient dashboard polling (returns 304 if unchanged)
- Visibility API pauses polling when tab is hidden
- Debounced refresh (500ms) prevents thundering herd on rapid updates

**Polling Strategy:**
- Idle: 5s poll interval
- Active tasks: 1s poll interval (auto-adjusts)
- Per-task polling for streaming output updates
- Falls back to history endpoint when task not found

**Race Condition Handling:**
- `activeTaskPolling` object tracks polling state per task
- `activeTasks` stores real-time output separately from session history
- Session history loaded on-demand when expanding a session
- Task state changes trigger refresh + history reload

**UI Structure:**
- Fleet panel: Collapsible, shows agent/director/helper status
- Session cards: Accordion pattern, one expanded at a time
- Session tabs: I/O, Details, Metrics
- Task modal: Agent selection, context presets, advanced options

**Keyboard Shortcuts:**
- `N` - New task
- `R` - Refresh
- `F` - Toggle fleet panel
- `J/K` - Navigate sessions
- `Escape` - Close expanded session

**CSS Variables:**
- Dark mode only (Danish minimalism aesthetic)
- Mobile-first responsive (optimized for narrow screens)
- Safe area insets for notched devices
- Reduced motion support via `prefers-reduced-motion`

**TODOs (placeholder for future observability):**
- Token usage aggregation per session (needs history API enhancement)
- Cost estimation (requires pricing data)
- Step/trace visualization (requires expanded history API)
