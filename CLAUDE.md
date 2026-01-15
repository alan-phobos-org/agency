# Agency Project Context

## Repository Structure

- `cmd/` - Main application binaries:
  - `ag-agent-claude` - Task execution agent
  - `ag-cli` - Command-line interface
  - `ag-scheduler` - Cron job scheduler
  - `ag-view-web` - Web dashboard (director)
- `internal/` - Internal packages:
  - `view/web/` - Web UI handlers, sessions, auth, discovery
  - `agent/` - Agent implementation
  - `scheduler/` - Scheduler logic
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

### Dev/Prod Mode

Agency supports running dev and prod instances simultaneously on one machine:

```bash
# Dev mode (default) - ports 8443, 9000-9009
./deployment/agency.sh dev
./build.sh deploy-local

# Prod mode - ports 9443, 9100-9109
./deployment/agency.sh prod
./build.sh deploy-prod
```

Port configuration is in `deployment/ports.conf`:
- Dev: Web=8443, Agent=9000, Discovery=9000-9009, Install=~/agency-dev
- Prod: Web=9443, Agent=9100, Discovery=9100-9109, Install=~/agency

Mode-specific PID files (`agency-dev.pids`, `agency-prod.pids`) allow both to run concurrently.
