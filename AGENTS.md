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

## Release Process

To create a release:

```bash
# Step 1: Run all automated checks and tests
./build.sh prepare-release

# Step 2: Update CHANGELOG.md with release notes (requires human/LLM)
# Add a new section: ## [X.Y.Z] - YYYY-MM-DD

# Step 3: Review docs (CLAUDE.md, README.md, docs/) for completed work
# Remove or mark done any TODO items, planned features now implemented, etc.

# Step 4: Create the release commit and tag
./build.sh release X.Y.Z

# Step 5: Push to remote
git push origin main vX.Y.Z
```

The `prepare-release` target runs:
1. Format, lint, and unit tests (`check`)
2. Full test suite including integration tests (`test-all`)
3. System tests with real binaries (`test-sys`)
4. Local deployment test (start services, verify health, stop)
5. Shows git log of changes since last tag

The `release` target:
1. Validates semver format
2. Checks CHANGELOG.md has entry for version
3. Creates release commit (if CHANGELOG.md modified)
4. Creates annotated tag

## Build Commands

```bash
./build.sh build           # Build all binaries to bin/
./build.sh test            # Unit tests only (<5s)
./build.sh test-all        # Unit + integration tests
./build.sh test-int        # Integration tests
./build.sh test-sys        # System tests (builds + runs real binaries)
./build.sh test-release    # Unit + integration + system tests
./build.sh lint            # Format and lint
./build.sh check           # Full pre-commit check
./build.sh clean           # Remove build artifacts
./build.sh deploy-local    # Build and run local deployment
./build.sh prepare-release # Run all release checks and show changes
./build.sh release X.Y.Z   # Create release commit and tag
```

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
│   ├── view/
│   │   └── web/    # Web view (dashboard + discovery)
│   └── testutil/   # Test helpers (port allocation, health checks)
└── testdata/       # Test fixtures and mock Claude scripts
```

## Testing

- Tests use `t.Parallel()` unless sharing state
- Use `testutil.AllocateTestPort(t)` for unique ports
- Mock Claude CLI via `CLAUDE_BIN` env var pointing to `testdata/mock-claude`
- Print progress to stderr, not t.Log()
- System tests build real binaries and run end-to-end
- Use production-style IDs in tests (e.g., `task-abc123` not `test123`) to catch error message formatting issues where prefixes get duplicated

### Test Coverage

| Package | Tests | Notes |
|---------|-------|-------|
| internal/agent | Unit + Integration + System | Well covered |
| internal/config | Unit | Validation tests |
| internal/history | Unit | Storage, pruning, outline extraction |
| internal/view/web | Unit + Integration + System | Discovery, auth, handlers |
| cmd/* | None | Thin entry points |

## Environment Variables

- `AGENCY_ROOT`: Override config directory (default: ~/.agency)
- `CLAUDE_BIN`: Path to Claude CLI (default: claude from PATH)
- `AG_WEB_PASSWORD`: Password for web view login (required)

## Phase 1 - Complete

MVP: Agent + CLI director with REST API.

### Agent Endpoints
- `GET /status` - Agent state, version, config, current task preview
- `POST /task` - Submit task (prompt, timeout, env, model, session_id, project, thinking)
- `GET /task/:id` - Task status and output (includes session_id)
- `POST /task/:id/cancel` - Cancel running task
- `POST /shutdown` - Graceful shutdown (supports force flag)
- `GET /history` - Paginated task history (page, limit params)
- `GET /history/:id` - Full task details with execution outline
- `GET /history/:id/debug` - Raw Claude output (retained for 20 most recent tasks)

### Extended Thinking
The API accepts a `thinking` parameter for future use, but extended thinking is
automatically enabled by the Claude CLI for compatible models (no CLI flag exists
to control it). The UI toggle and contexts `thinking` field are preserved for
compatibility but don't currently affect CLI behavior.

### Max Turns and Auto-Resume
The Claude CLI limits each task to a maximum number of conversation turns (default: 50).
When a task hits this limit:
1. Agent automatically resumes the session (up to 2 additional attempts)
2. If still incomplete after 3 total attempts, task fails with `max_turns` error type
3. Error message suggests breaking the task into smaller steps

Configure via `max_turns` in agent config:
```yaml
claude:
  max_turns: 100  # Increase turn limit (default: 50)
```

### Session Directories
Agent uses a shared session directory (`/tmp/agency/sessions/<session_id>/`) instead of per-task workdirs:
- New sessions: directory is created fresh (cleaned if exists)
- Resumed sessions: directory is reused with existing state
- Configurable via `session_dir` in agent config

### Agent States
- `idle` - Ready to accept tasks
- `working` - Executing a task
- `cancelling` - Task cancellation in progress

### ag-cli
- `ag-cli task <prompt>` - Submit task to agent and poll until completion
- `ag-cli status [url]` - Get status of component
- `ag-cli discover` - Discover running components

## Phase 1.1 - Complete

Web View: Status dashboard and task submission UI.

### Web View Features
- HTTPS with auto-generated self-signed certificates
- Password-based authentication with secure session cookies
- Device pairing for multi-device access (no password sharing)
- IP rate limiting (10 failed attempts = 1 hour block)
- Access logging (optional, via `-access-log` flag)
- Port scanning discovery of agents and directors
- Real-time status updates (1-second polling)
- Task submission form with model/timeout/thinking selection
- Task contexts (predefined prompt prefixes and settings)
- Task monitoring with output display
- Global sessions (server-side storage, shared across all UI views)
- Extended thinking toggle (default: on)

### Authentication
Single-user auth with device pairing (password required):
- **Password login**: Set `AG_WEB_PASSWORD` env var (required), login at `/login`
- **Device pairing**: Generate pairing code from dashboard, enter at `/pair`
- **Session types**: Auth sessions (12h, auto-refresh) and device sessions (long-lived)
- **Session storage**: Persisted to `~/.agency/auth-sessions.json`
- **Cookies**: HttpOnly, Secure, SameSite=Strict

### Web View Endpoints
- `GET /status` - Universal status endpoint (no auth)
- `GET /login` - Login form (no auth)
- `POST /login` - Authenticate with password (no auth)
- `GET /pair` - Device pairing form (no auth)
- `POST /pair` - Exchange pairing code for session (no auth)
- `POST /logout` - End session
- `GET /` - Dashboard HTML page
- `GET /api/agents` - List discovered agents
- `GET /api/directors` - List discovered directors
- `GET /api/contexts` - List available task contexts
- `POST /api/task` - Submit task to selected agent (supports session_id, thinking)
- `GET /api/task/:id` - Get task status (requires agent_url param)
- `GET /api/sessions` - List all sessions (global across views)
- `POST /api/sessions` - Add task to session
- `PUT /api/sessions/:id/tasks/:taskId` - Update task state
- `POST /api/pair/code` - Generate pairing code (10min TTL, single-use)
- `GET /api/devices` - List active sessions/devices
- `DELETE /api/devices/:id` - Revoke device session

### Running the Web View
```bash
# Start with defaults (port 8443, scan 9000-9199)
AG_WEB_PASSWORD=your-password ./bin/ag-view-web

# With custom options
AG_WEB_PASSWORD=your-password ./bin/ag-view-web -port 8080 -port-start 9000 -port-end 9050

# With access logging enabled
AG_WEB_PASSWORD=your-password ./bin/ag-view-web -access-log /var/log/agency/access.log

# With task contexts
AG_WEB_PASSWORD=your-password ./bin/ag-view-web -contexts configs/contexts.yaml

# Password can be loaded from .env file (AG_WEB_PASSWORD=... in .env)
./bin/ag-view-web
```

Access dashboard at `https://localhost:8443` (redirects to login page)

### Task Contexts
Contexts define predefined settings for task submission (prompt prefix, model, timeout, thinking).
Configure via YAML file:
```yaml
contexts:
  - id: my-context
    name: My Context
    description: Description shown in UI
    model: opus
    thinking: true
    timeout_seconds: 1800
    prompt_prefix: |
      Instructions prepended to all prompts...
```

The UI shows a "Manual" option for custom settings, plus any configured contexts.
When a non-manual context is selected, its settings are applied and manual controls are hidden.

## Phase 1.2 - Complete

Interface-based architecture refactoring.

### Core Interfaces
- **Statusable** - Report type, version, basic config (`GET /status`)
- **Taskable** - Accept prompts, execute work (`POST /task`, `GET /task/:id`)
- **Observable** - Report held tasks (`GET /tasks`)
- **Configurable** - Get/set config (Phase 2+)

### Component Types
- **Agent** (Statusable + Taskable) - ag-agent-claude
- **Director** (Statusable + Observable + Taskable) - ag-director-claude
- **Helper** (Statusable + Observable) - ag-tool-scheduler
- **View** (Statusable + Observable) - ag-view-web

### Sessions
Multi-turn conversations via Claude Code `--session-id` and `--resume`:
- Agent generates session ID if not provided
- Pass `session_id` in task request to continue session
- Response always includes `session_id`

### Embedded Instructions
Components have embedded CLAUDE.md files that are prepended to all prompts:
- `internal/agent/claude.md` - Agent instructions (git commit rules, etc.)
- `internal/director/claude.md` - Director instructions
These ensure consistent behavior across all Claude invocations.

Custom preprompt can be loaded from file via `preprompt_file` in agent config (falls back to embedded default).

### Project Context
Tasks can include project context prepended to prompt:
```json
{"prompt": "...", "project": {"name": "myapp", "prompt": "Work in repo..."}}
```

## Deployment Scripts

Quick-start scripts for running the full agency stack locally:

```bash
# Start web view + agent
./deployment/agency.sh

# Stop all services
./deployment/stop-agency.sh

# Deploy to remote host
./deployment/deploy-agency.sh user@host [ssh-port] [ssh-key]
```

Environment variables:
- `AG_WEB_PORT`: Web view port (default: 8443)
- `AG_AGENT_PORT`: Agent port (default: 9000)
- `AG_WEB_PASSWORD`: Auth password (loaded from .env if not set)

Logs written to `deployment/*.log`, PIDs tracked in `deployment/agency.pids`.

## Design Patterns

### Fallback on Resource Lifecycle Transitions

When a resource moves between endpoints during its lifecycle (e.g., active → archived), clients polling the original endpoint will get 404. Proxy layers should implement fallback logic:

1. Try primary endpoint (e.g., `/task/:id` for active tasks)
2. On 404, check secondary endpoint (e.g., `/history/:id` for completed tasks)
3. Return data from whichever succeeds

This prevents stale state in clients that poll between lifecycle transitions. The proxy absorbs the complexity so clients don't need to know about resource migration.

Example: `HandleTaskStatus` checks `/history/:id` when agent returns 404 for `/task/:id`.

## Known Limitations

- Single-task agent (returns 409 if busy)
- No structured logging (stderr only)
- Task session data stored in memory (not persisted across web view restarts)

## Task History

Agent stores task history at `~/.agency/history/<agent-name>/`:
- Outline entries: 100 tasks retained with execution step previews (200 char limit)
- Debug logs: 20 most recent tasks retain full Claude output
- Persisted to disk, survives agent restarts
- Configurable via `history_dir` in agent config
