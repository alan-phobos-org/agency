# Agency Project Context

## Repository Structure

- `cmd/` - Main application binaries:
  - `ag-agent-claude` - Task execution agent
  - `ag-cli` - Command-line interface
  - `ag-scheduler` - Cron job scheduler
  - `ag-github-monitor` - GitHub repo event monitor
  - `ag-view-web` - Web dashboard (director)
- `internal/` - Internal packages:
  - `view/web/` - Web UI handlers, sessions, auth, discovery
  - `agent/` - Agent implementation
  - `scheduler/` - Scheduler logic
  - `github-monitor/` - GitHub repo monitor logic
  - `history/` - Task history storage
- `docs/` - Design documents

## Key Patterns

### Session Store (internal/view/web/sessions.go)
- In-memory thread-safe session storage with `sync.RWMutex`
- Sessions track tasks across agent interactions
- Sessions can be archived (hidden from UI but kept in storage)
- `GetAll()` returns only non-archived sessions, sorted by UpdatedAt

### Handlers (internal/view/web/handlers.go)
- HTTP handlers with chi router parameters passed explicitly
- Use `api.WriteJSON` and `api.WriteError` for responses
- Pattern: `HandleX(w, r, ...params)` for handlers with URL params

### Web UI (internal/view/web/templates/dashboard.html)
- Alpine.js for reactive state management
- Danish minimalism dark theme
- Polling-based updates with ETag support
- Confirmation dialogs use native `confirm()` for simplicity
- Viewport uses `maximum-scale=1.0` to prevent iOS auto-zoom on input focus

## Testing Patterns

- Use `t.Parallel()` for test isolation
- Use `httptest.NewRequest` and `httptest.NewRecorder` for handler tests
- Create fresh `NewHandlers()` instance per test
- Verify both success paths and error conditions

## API Endpoints

Session-related endpoints:
- `GET /api/sessions` - List non-archived sessions
- `POST /api/sessions` - Add task to session
- `PUT /api/sessions/{sessionId}/tasks/{taskId}` - Update task state
- `POST /api/sessions/{sessionId}/archive` - Archive session

## Build & Test

```bash
# Run all tests
go test ./...

# Run specific tests
go test ./internal/view/web/... -run "Archive"
```

## Deployment

### Local (Dev) and Remote (Prod)

```bash
# Local development
./build.sh deploy-local    # Start locally (dev mode, ports 8443, 9000-9009)
./build.sh stop-local      # Stop local instance

# Remote production
./build.sh deploy-prod user@host [ssh-port] [ssh-key]  # Deploy to remote
./build.sh stop-prod user@host [ssh-port] [ssh-key]    # Stop remote instance
```

Port configuration is in `deployment/ports.conf`:
- Dev (local): Web=8443, Agent=9000, Discovery=9000-9009, GitHub Monitor=9020
- Prod (remote): Web=9443, Agent=9100, Discovery=9100-9109, GitHub Monitor=9120, Install=~/agency

## Helper Patterns

### Event-Driven Helpers (github-monitor)
- Quiet period pattern: delay action after events to batch rapid changes
- Circuit breaker pattern: stop after N consecutive failures, require manual reset
- Task queue: sequential per-repo to avoid conflicts, parallel across repos
- Use `gh` CLI for GitHub API access (requires GITHUB_TOKEN in .env)
- Model selection: Sonnet for reviews, Opus for fixes
