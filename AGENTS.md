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
| [docs/PLAN.md](docs/PLAN.md) | Vision, phases, backlog | Planning work |
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

## Project Structure

```
agency/
├── cmd/
│   ├── ag-agent-claude/  # Agent binary
│   ├── ag-cli/           # CLI tool (task, status, discover)
│   └── ag-view-web/      # Web view binary
├── configs/              # Configuration files (contexts.yaml)
├── deployment/           # Local deployment scripts
├── internal/
│   ├── agent/      # Agent logic + REST API handlers
│   ├── api/        # Shared types and constants
│   ├── config/     # YAML parsing, validation
│   ├── history/    # Task history storage and outline extraction
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

### Current Phase: 1.2 (Complete)

- **Agent**: Single-task executor with REST API, session support, auto-resume
- **CLI**: `ag-cli task|status|discover` commands
- **Web View**: HTTPS dashboard with auth, discovery, task submission, contexts

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
