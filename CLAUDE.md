# agency

A modular framework for AI-powered software engineering agents.

## Development Workflow

Before committing, always run:
```bash
./build.sh check
```

This runs:
1. `gofmt -w .` (auto-format)
2. `staticcheck ./...` (lint)
3. `go test -race -short ./...` (unit tests)

All three must pass before committing.

## Build Commands

```bash
./build.sh build      # Build all binaries to bin/
./build.sh test       # Unit tests only (<5s)
./build.sh test-all   # Unit + component tests
./build.sh test-int   # Integration tests
./build.sh test-sys   # System tests (builds + runs real binaries)
./build.sh lint       # Format and lint
./build.sh check      # Full pre-commit check
./build.sh clean      # Remove build artifacts
```

## Project Structure

```
agency/
├── cmd/
│   ├── agency/           # CLI tool (fleet management, stub)
│   ├── ag-agent-claude/  # Agent binary
│   └── ag-director-cli/  # Director binary
├── internal/
│   ├── agent/      # Agent logic + REST API handlers
│   ├── config/     # YAML parsing, validation
│   ├── director/
│   │   └── cli/    # CLI director implementation
│   └── testutil/   # Test helpers (port allocation, health checks)
└── testdata/       # Test fixtures and mock Claude scripts
```

## Testing

- Tests use `t.Parallel()` unless sharing state
- Use `testutil.AllocateTestPort(t)` for unique ports
- Mock Claude CLI via `CLAUDE_BIN` env var pointing to `testdata/mock-claude`
- Print progress to stderr, not t.Log()
- System tests build real binaries and run end-to-end

### Test Coverage

| Package | Tests | Notes |
|---------|-------|-------|
| internal/agent | Unit + Integration + System | Well covered |
| internal/config | Unit | Validation tests |
| internal/director/cli | Unit | HTTP client + polling tests |
| cmd/* | None | Thin entry points |

## Environment Variables

- `AGENCY_ROOT`: Override config directory (default: ~/.agency)
- `CLAUDE_BIN`: Path to Claude CLI (default: claude from PATH)

## Phase 1 (Current) - Complete

MVP: Agent + CLI director with REST API.

### Agent Endpoints
- `GET /status` - Agent state, version, config, current task preview
- `POST /task` - Submit task (prompt, workdir, timeout, env, model)
- `GET /task/:id` - Task status and output
- `POST /task/:id/cancel` - Cancel running task
- `POST /shutdown` - Graceful shutdown (supports force flag)

### Agent States
- `idle` - Ready to accept tasks
- `working` - Executing a task
- `cancelling` - Task cancellation in progress

### CLI Director
- Connects to agent at specified URL
- Submits task and polls until completion
- Displays result to stdout

## Known Limitations (Phase 1)

- Single-task agent (returns 409 if busy)
- No task history persistence
- No structured logging (stderr only)
- No discovery (agents specified by URL)
