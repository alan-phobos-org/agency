# agency

A modular framework for AI-powered software engineering agents.

## Vision

Deploying agents should feel like reliable infrastructure, not babysitting experiments. Prioritize operational stability (graceful failures, clear errors, predictable behavior) over feature breadth. The web UI should be immediately usable without docs. Security defaults are paranoid - assume hostile networks. UI responsiveness must never feel sluggish.

## Documentation

| Document | Purpose | Read When |
|----------|---------|-----------|
| [AGENTS.md](AGENTS.md) | Quick reference, commands, workflows | Always |
| [DEVELOPMENT.md](DEVELOPMENT.md) | Development workflow, iteration guide | Developing features |
| [README.md](README.md) | Project overview, quick start | Getting started |
| [docs/REFERENCE.md](docs/REFERENCE.md) | API specs, endpoints, config | Implementing API changes |
| [docs/DESIGN.md](docs/DESIGN.md) | Architecture, patterns | Major refactoring |
| [docs/TESTING.md](docs/TESTING.md) | Test conventions, commands | Writing/running tests |
| [docs/RELEASE.md](docs/RELEASE.md) | Release process | Preparing releases |
| [docs/WEB_UI_DESIGN.md](docs/WEB_UI_DESIGN.md) | Dashboard design, Alpine.js | Modifying web UI |
| [docs/INTEGRATION_PATTERNS.md](docs/INTEGRATION_PATTERNS.md) | Scheduler, helpers, queue | Component integration |
| [docs/SCHEDULER_DESIGN.md](docs/SCHEDULER_DESIGN.md) | Scheduler architecture | Modifying scheduler |
| [docs/WORK_QUEUE_DESIGN.md](docs/WORK_QUEUE_DESIGN.md) | Task queue architecture | Implementing work queue |
| [docs/PLAN.md](docs/PLAN.md) | Vision, phases, backlog | Planning work |
| [docs/DEBUGGING_DEPLOYED.md](docs/DEBUGGING_DEPLOYED.md) | Remote system diagnostics | Debugging deployed systems |
| [SMOKE_TEST_ROBUSTNESS.md](SMOKE_TEST_ROBUSTNESS.md) | Smoke test cleanup, build.sh refactor | Fixing test infrastructure |
| [SMOKE_TEST_GAP_ANALYSIS.md](SMOKE_TEST_GAP_ANALYSIS.md) | Analysis of why smoke tests miss UI bugs | Understanding test coverage gaps |
| [SMOKE_TEST_IMPROVEMENTS.md](SMOKE_TEST_IMPROVEMENTS.md) | Improvements to catch UI bugs | After fixing UI bugs |
| [CHANGELOG.md](CHANGELOG.md) | Release history | Preparing releases |

## Quick Reference

Use `./build.sh` for all build/test/lint/release/deploy actions. Do not use Makefiles or ad-hoc scripts unless a doc explicitly calls for it.

### Build Commands

| Command | Purpose |
|---------|---------|
| `./build.sh check` | **Pre-commit** (format + lint + test) |
| `./build.sh quick-test` | **Fast iteration** (build + deploy-quick + smoke tests) |
| `./build.sh build` | Build all binaries to bin/ |
| `./build.sh test` | Unit tests only (<5s) |
| `./build.sh test-smoke` | E2E smoke tests (see [SMOKE_TEST_ROBUSTNESS.md](SMOKE_TEST_ROBUSTNESS.md) if stuck) |
| `./build.sh deploy-local` | Full deployment with all tests |
| `./build.sh deploy-local-quick` | Fast deployment (skips integration/system tests) |
| `./build.sh stop-local` | Stop local deployment |

See [DEVELOPMENT.md](DEVELOPMENT.md) for detailed workflow examples.

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `AG_WEB_PASSWORD` | Web view login (required) |
| `AGENCY_ROOT` | Config directory (default: ~/.agency) |
| `CLAUDE_BIN` | Claude CLI path (default: from PATH) |
| `CODEX_BIN` | OpenAI Codex CLI path (default: codex) |
| `GITHUB_TOKEN` | GitHub API access for github-monitor |

### API Endpoints (Session)

- `GET /api/sessions` - List non-archived sessions
- `POST /api/sessions` - Add task to session
- `PUT /api/sessions/{sessionId}/tasks/{taskId}` - Update task state
- `POST /api/sessions/{sessionId}/archive` - Archive session

### API Endpoints (Queue)

- `POST /api/queue/task` - Submit task to queue
- `GET /api/queue` - Get queue status and pending tasks
- `GET /api/queue/{id}` - Get queued task status
- `POST /api/queue/{id}/cancel` - Cancel queued task

### Port Configuration

From `deployment/ports.conf`:
- **Dev**: Web=8443, Agent=9000, AgentCodex=9001, Scheduler=9010, Discovery=9000-9010, GitHub Monitor=9020
- **Prod**: Web=9443, Agent=9100, AgentCodex=9101, Scheduler=9110, Discovery=9100-9110, GitHub Monitor=9120

## Workflows

| Trigger | Action |
|---------|--------|
| **Making code changes** | `./build.sh quick-test` (fast iteration cycle) |
| Before any commit | `./build.sh check` |
| Testing deployment | See [DEVELOPMENT.md](DEVELOPMENT.md) for iteration workflows |
| "what's next", "status" | `./build.sh status` → read `docs/PLAN.md` → summarize (10-15 lines) |
| "prepare release" | `./build.sh prepare-release` → update CHANGELOG.md → `./build.sh release X.Y.Z` → push |
| Full deployment | `./build.sh deploy-local` (all tests, uses AG_WEB_PASSWORD from .env) |

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
│   ├── ag-agent-claude/    # Agent binary (wraps Claude CLI)
│   ├── ag-agent-codex/     # Agent binary (wraps OpenAI Codex CLI)
│   ├── ag-cli/             # CLI tool (task, status, discover)
│   ├── ag-github-monitor/  # GitHub repo event monitor
│   ├── ag-scheduler/       # Scheduler binary (cron-style task triggering)
│   └── ag-view-web/        # Web view binary (HTTPS dashboard)
├── configs/                # Configuration files (scheduler.yaml)
├── deployment/             # Local and remote deployment scripts
├── internal/
│   ├── agent/          # Agent logic + REST API handlers
│   ├── api/            # Shared types and constants
│   ├── config/         # YAML parsing, validation
│   ├── github-monitor/ # GitHub repo monitor logic
│   ├── history/        # Task history storage and outline extraction
│   ├── logging/        # Structured JSON logging with queryable storage
│   ├── scheduler/      # Scheduler logic, cron parsing, job runner
│   ├── view/web/       # Web view (dashboard + discovery)
│   └── testutil/       # Test helpers
├── tests/smoke/            # E2E smoke tests with Playwright
└── testdata/               # Test fixtures and mock Claude scripts
```

---

## Testing

See [docs/TESTING.md](docs/TESTING.md) for conventions, race condition prevention, and test commands.

Quick reference: `./build.sh test` (unit), `./build.sh test-all` (unit + integration), `./build.sh check` (pre-commit).

Smoke tests (Playwright): Cover Claude and Codex agents via scheduled jobs and manual UI selection. Tests verify output block visibility, log stability, and agent kind routing. See [SMOKE_TEST_IMPROVEMENTS.md](SMOKE_TEST_IMPROVEMENTS.md) for recent enhancements.

---

## Release Process

See [docs/RELEASE.md](docs/RELEASE.md) for the full process.

Quick reference: `./build.sh prepare-release` then `./build.sh release X.Y.Z`.

---

## Component Overview [READ IF: implementing new features or debugging architecture]

### Current Phase: 1.3 (Complete)

- **Agent**: Single-task executor with REST API, session support, auto-resume
- **CLI**: `ag-cli task|status|discover` commands
- **Web View**: HTTPS dashboard with auth, discovery, task submission
- **Scheduler**: Cron-style task triggering (`ag-scheduler -config configs/scheduler.yaml`)
  - Standard 5-field cron expressions
  - Configurable agent URL, model, and timeout per job
  - Status endpoint at `/status` showing job states and next run times
  - System test config at `tests/system/fixtures/scheduler-system.yaml` (no smoke-nightly-maintenance job)

### Key Behaviors

- Agent returns 409 if busy (single-task only)
- Web view discovers agents via port scanning (9000-9009 dev, 9100-9109 prod)
- Sessions persist in shared directories for multi-turn conversations
- Task history stored at `~/.agency/history/<agent>/`
- Two agent kinds: `claude` (Anthropic), `codex` (OpenAI)
- Model tiers: `fast`/`standard`/`heavy` map to provider-specific models (Claude: haiku/sonnet/opus; Codex: gpt-5.1-codex-mini/gpt-5.2-codex/gpt-5.1-codex-max)

For detailed endpoint specs, see [docs/REFERENCE.md](docs/REFERENCE.md).

---

## Recent Fixes

### Codex Agent Output Display (2026-01-19)

**Issue**: Codex agent tasks completed successfully but output blocks were hidden in the web UI. The frontend's `x-show="getTaskOutput(session.id, task)"` condition evaluated to false because task output wasn't being captured.

**Root Cause**:
1. The codex CLI outputs JSON in format: `{"type":"item.completed","item":{"type":"agent_message","text":"..."}}`
2. The `extractOutputText()` function in [codex_runner.go](internal/agent/codex_runner.go) didn't recognize this structure
3. Even though token metrics showed output was generated, the parsed output was empty
4. The history Entry had `Output=""`, which with `omitempty` JSON tag meant the frontend received no output field

**Fix**: Two changes to [codex_runner.go](internal/agent/codex_runner.go):
1. Added parsing for `item.text` field in codex CLI JSON output format (line 139-145)
2. Added assignment of `parsedOutput.Output` to `task.Output` in [agent.go](internal/agent/agent.go:941-943)

**Files Modified**:
- `internal/agent/codex_runner.go`: Added extraction of `item.text` from codex CLI output
- `internal/agent/agent.go`: Added assignment `task.Output = parsedOutput.Output` when `parsedOutput.HasOutput` is true

**Testing**: Smoke test "5. Trigger Codex Scheduled Job" now passes (verifies output block visibility and content).

---

## Known Limitations

- Single-task agent (returns 409 if busy) - see [docs/WORK_QUEUE_DESIGN.md](docs/WORK_QUEUE_DESIGN.md) for planned queue
- Task session data stored in memory (not persisted across web view restarts)
- Tasks can appear stuck in "working" state - see [docs/TASK_STATE_SYNC_DESIGN.md](docs/TASK_STATE_SYNC_DESIGN.md)
- Smoke tests leave orphaned processes on failure - see [SMOKE_TEST_ROBUSTNESS.md](SMOKE_TEST_ROBUSTNESS.md) for cleanup plan

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

### HTTP Configuration

**CORS**: Agents serve CORS headers (`Access-Control-Allow-Origin: *`) for cross-origin requests from web view. See `internal/agent/agent.go:corsMiddleware`.

**TLS**: Agents use self-signed certs. Internal web API (port 8080/18080) uses plain HTTP. Director URL in scheduler config must match protocol.

---

## Web UI Architecture

See [docs/WEB_UI_DESIGN.md](docs/WEB_UI_DESIGN.md) for the full design (visual system, components, state management, accessibility).

Quick reference:
- Alpine.js SPA in `internal/view/web/templates/dashboard.html`
- Polling: 5s idle, 1s active; pauses when tab hidden
- Keyboard: `N` new task, `R` refresh, `F` fleet panel, `J/K` navigate

---

## Integration Patterns

See [docs/INTEGRATION_PATTERNS.md](docs/INTEGRATION_PATTERNS.md) for scheduler integration, helper patterns, and work queue details.

Quick reference:
- Scheduler needs `director_url` in config to create tracked sessions
- Manual job trigger: `POST /api/scheduler/trigger?scheduler_url=<url>&job=<name>`
- Work queue design: [docs/WORK_QUEUE_DESIGN.md](docs/WORK_QUEUE_DESIGN.md)
