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
│   ├── ag-director-cli/  # CLI director binary
│   └── ag-director-web/  # Web director binary
├── deployment/           # Local deployment scripts
├── internal/
│   ├── agent/      # Agent logic + REST API handlers
│   ├── config/     # YAML parsing, validation
│   ├── director/
│   │   ├── cli/    # CLI director implementation
│   │   └── web/    # Web director (dashboard + discovery)
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
| internal/director/web | Unit + Integration + System | Discovery, auth, handlers |
| cmd/* | None | Thin entry points |

## Environment Variables

- `AGENCY_ROOT`: Override config directory (default: ~/.agency)
- `CLAUDE_BIN`: Path to Claude CLI (default: claude from PATH)
- `AG_WEB_TOKEN`: Authentication token for web director

## Phase 1 - Complete

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

## Phase 1.1 - Complete

Web Director: Status dashboard and task submission UI.

### Web Director Features
- HTTPS with auto-generated self-signed certificates
- Token-based authentication (header or query param)
- Port scanning discovery of agents and directors
- Real-time status updates (1-second polling)
- Task submission form with model/timeout selection
- Task monitoring with output display

### Web Director Endpoints
- `GET /status` - Universal status endpoint (no auth)
- `GET /` - Dashboard HTML page
- `GET /api/agents` - List discovered agents
- `GET /api/directors` - List discovered directors
- `POST /api/task` - Submit task to selected agent
- `GET /api/task/:id` - Get task status (requires agent_url param)

### Running the Web Director
```bash
# Start with defaults (port 8443, scan 9000-9199)
./bin/ag-director-web

# With custom options
./bin/ag-director-web -port 8080 -port-start 9000 -port-end 9050

# Token from environment or .env file
AG_WEB_TOKEN=your-secret-token ./bin/ag-director-web
```

Access dashboard at `https://localhost:8443/?token=your-token`

## Deployment Scripts

Quick-start scripts for running the full agency stack locally:

```bash
# Start web director + agent
./deployment/agency.sh

# Stop all services
./deployment/stop-agency.sh
```

Environment variables:
- `AG_WEB_PORT`: Web director port (default: 8443)
- `AG_AGENT_PORT`: Agent port (default: 9000)
- `AG_WEB_TOKEN`: Auth token (loaded from .env if not set)

Logs written to `deployment/*.log`, PIDs tracked in `deployment/agency.pids`.

## Known Limitations

- Single-task agent (returns 409 if busy)
- No task history persistence
- No structured logging (stderr only)
- Web director is stateless (task-to-agent mapping in browser)

## Additional Notes

This repository was successfully cloned and modified using Claude Code.
