# Session Routing Design

This document describes the design for centralized session management, where all components that task agents route through the Web Director.

**Related docs:**
- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Scheduler architecture
- [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) - State synchronization

---

## Problem Statement

The scheduler (and CLI director) currently bypass the Web Director, posting tasks directly to agents. This creates invisible sessions that:

1. Are not tracked in the Web Director's SessionStore
2. Cannot be viewed or continued via the web UI
3. Exist only in agent history after completion
4. Cannot be handed off between interfaces (e.g., scheduler starts, user continues via web)

### Current Flow (Scheduler)

```
Scheduler                     Agent
    │                           │
    │   POST /task              │
    │ ─────────────────────────>│
    │   {task_id, session_id}   │
    │ <─────────────────────────│
    │                           │
    └── (no web visibility) ────┘
```

### Desired Flow

```
Scheduler              Web Director              Agent
    │                       │                       │
    │   POST /api/task      │                       │
    │ ─────────────────────>│   POST /task          │
    │                       │ ─────────────────────>│
    │                       │   {task_id, session_id}
    │   {task_id, session_id}  <─────────────────────│
    │ <─────────────────────│                       │
    │                       │                       │
    │      (session visible in web dashboard)       │
```

---

## Design: Centralized Session Authority

Make the Web Director the single source of truth for sessions. All components that want to task agents must route through the Web Director's API.

### Architecture

```
                 ┌────────────────────────────────────┐
                 │         Web Director (8443)        │
                 │  ┌──────────────────────────────┐  │
                 │  │       SessionStore           │  │
                 │  │  (authoritative session DB)  │  │
                 │  └──────────────────────────────┘  │
                 │            │                       │
                 │     POST /api/task                 │
                 └────────────────────────────────────┘
                        ▲           │
                        │           ▼
         ┌──────────────┴──┐   ┌────────────┐
         │    Scheduler    │   │   Agent    │
         │     (9100)      │   │   (9000)   │
         └─────────────────┘   └────────────┘
              ▲
              │
         ┌────┴────┐
         │   CLI   │
         └─────────┘
```

---

## API Changes

### Web Director: Enhanced Task Submission

Extend `POST /api/task` to support additional metadata:

```go
type TaskSubmitRequest struct {
    AgentURL       string            `json:"agent_url"`
    Prompt         string            `json:"prompt"`
    Model          string            `json:"model,omitempty"`
    TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
    SessionID      string            `json:"session_id,omitempty"`   // Continue existing session
    Env            map[string]string `json:"env,omitempty"`
    Thinking       *bool             `json:"thinking,omitempty"`

    // New fields for session routing
    Source         string            `json:"source,omitempty"`       // "web", "scheduler", "cli"
    SourceJob      string            `json:"source_job,omitempty"`   // Job name for scheduler
}
```

The `Source` field enables:
- Filtering sessions by origin in the dashboard
- Different retention policies per source
- Audit logging of where tasks originated

### Response (unchanged)

```json
{
  "task_id": "task-abc12345",
  "agent_url": "http://localhost:9000",
  "session_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

---

## Component Changes

### Scheduler

#### Configuration Changes

Add `director_url` to scheduler config:

```yaml
# configs/scheduler.yaml
port: 9100
log_level: info

# New: route through director for session tracking
director_url: https://localhost:8443

# Fallback: direct agent URL (used if director unavailable)
agent_url: http://localhost:9000

jobs:
  - name: nightly-maintenance
    schedule: "0 1 * * *"
    prompt: "..."
```

#### Config Schema

```go
type Config struct {
    Port        int    `yaml:"port"`
    LogLevel    string `yaml:"log_level"`
    DirectorURL string `yaml:"director_url"`  // New: primary target
    AgentURL    string `yaml:"agent_url"`     // Fallback if director unavailable
    Jobs        []Job  `yaml:"jobs"`
}
```

#### Behavior Changes

1. **Primary path**: POST to `director_url/api/task`
2. **Fallback path**: If director unavailable, POST to `agent_url/task`
3. **Logging**: Log which path was used

```go
func (s *Scheduler) runJob(js *jobState) {
    log.Printf("job=%s action=triggered", js.Job.Name)

    // Try director first (for session tracking)
    if s.config.DirectorURL != "" {
        taskID, err := s.submitViaDirector(js)
        if err == nil {
            log.Printf("job=%s action=submitted via=director task_id=%s", js.Job.Name, taskID)
            s.updateJobState(js, "submitted", taskID)
            return
        }
        log.Printf("job=%s warning=director_unavailable error=%q", js.Job.Name, err)
    }

    // Fallback to direct agent submission
    taskID, err := s.submitViaAgent(js)
    if err != nil {
        log.Printf("job=%s action=skipped reason=error error=%q", js.Job.Name, err)
        s.updateJobState(js, "skipped_error", "")
        return
    }
    log.Printf("job=%s action=submitted via=agent task_id=%s", js.Job.Name, taskID)
    s.updateJobState(js, "submitted", taskID)
}
```

#### TLS Handling

The scheduler must handle the director's self-signed HTTPS certificate:

```go
func (s *Scheduler) createHTTPClient(targetURL string) *http.Client {
    client := &http.Client{Timeout: 30 * time.Second}

    // Skip TLS verification for localhost HTTPS (self-signed certs)
    if strings.HasPrefix(targetURL, "https://localhost") ||
        strings.HasPrefix(targetURL, "https://127.0.0.1") {
        client.Transport = &http.Transport{
            TLSClientConfig: &tls.Config{
                InsecureSkipVerify: true,
            },
        }
    }

    return client
}
```

### CLI Director

Similar changes for `internal/director/cli`:

```go
type Director struct {
    directorURL string  // New: primary target
    agentURL    string  // Fallback
    client      *http.Client
}

func New(directorURL, agentURL string) *Director {
    return &Director{
        directorURL: directorURL,
        agentURL:    agentURL,
        client:      createTLSClient(),
    }
}
```

### Web Director

Minor enhancement to track task source:

```go
func (h *Handlers) HandleTaskSubmit(w http.ResponseWriter, r *http.Request) {
    var req TaskSubmitRequest
    // ... existing validation ...

    // Default source to "web" if not specified
    source := req.Source
    if source == "" {
        source = "web"
    }

    // Add source to session metadata (for UI filtering)
    h.sessionStore.AddTask(sessionID, req.AgentURL, taskID, state, prompt, source)
}
```

---

## Issues and Mitigations

### Issue 1: Director Becomes Single Point of Failure

**Problem**: If director is down, scheduler cannot submit tasks.

**Mitigation**: Fallback to direct agent submission with logging:

```go
if s.config.DirectorURL != "" {
    err := s.submitViaDirector(js)
    if err == nil {
        return // Success via director
    }
    log.Printf("job=%s warning=director_unavailable, falling back to agent", js.Job.Name)
}
// Fallback to agent_url
```

**Trade-off**: Tasks submitted via fallback are not visible in web UI until agent history sync.

### Issue 2: Startup Order Dependency

**Problem**: Scheduler may start before director is ready.

**Mitigation**: Lazy director availability check (don't fail startup):

```go
func (s *Scheduler) Start() error {
    // Don't check director availability at startup
    // It will be checked on first job trigger
    // ...
}
```

Update `deployment/agency.sh` to start director before scheduler (already the case).

### Issue 3: Session Store Volatility

**Problem**: SessionStore is in-memory; director restart loses session data.

**Mitigation**: This is a known limitation (see TASK_STATE_SYNC_DESIGN.md). Quick fixes:
- Sessions can be reconstructed from agent history
- `/api/dashboard` reconciles on load

**Long-term**: Option B (Decentralized Session Broker) solves this properly.

### Issue 4: Additional Network Hop Latency

**Problem**: Scheduler → Director → Agent adds ~1ms latency.

**Mitigation**: Not significant for cron jobs. Scheduler jobs typically have 30+ minute timeouts.

### Issue 5: Authentication

**Problem**: Director requires authentication; scheduler needs credentials.

**Mitigation Options**:

1. **Internal bypass**: Add `-internal-port` flag to director for unauthenticated internal API
2. **Service token**: Create long-lived token for internal services
3. **Localhost exemption**: Skip auth for localhost connections (least secure)

**Recommended**: Option 1 (internal port) - cleanest separation:

```go
// Web Director: two ports
// 8443: HTTPS with auth (external)
// 8444: HTTP without auth (internal, localhost only)
```

Configuration:
```yaml
# deployment environment
AG_WEB_PORT=8443           # External HTTPS
AG_WEB_INTERNAL_PORT=8444  # Internal HTTP (localhost only)
```

Scheduler config:
```yaml
director_url: http://localhost:8444  # Internal port, no auth
```

### Issue 6: Discovery Integration

**Problem**: Scheduler currently doesn't participate in discovery.

**Mitigation**: Scheduler uses explicit `director_url` config rather than discovery. This is intentional:
- Scheduler runs on same host as director (localhost)
- Discovery is for finding remote/unknown services
- Explicit config is more predictable for scheduled jobs

### Issue 7: Test Infrastructure

**Problem**: Tests need to account for director routing.

**Mitigation**: Update test infrastructure:

```go
// Integration tests: scheduler with mock director
func TestSchedulerDirectorRouting(t *testing.T) {
    // Start mock director
    director := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.URL.Path == "/api/task" {
            // Verify request format
            // Return mock response
        }
    }))
    defer director.Close()

    cfg := &scheduler.Config{
        DirectorURL: director.URL,
        // ...
    }
    // ...
}
```

System tests: Update to verify session visibility:

```go
func TestSchedulerSessionVisibility(t *testing.T) {
    // 1. Start director, agent, scheduler
    // 2. Wait for scheduled job to trigger
    // 3. Verify session appears in GET /api/sessions
    // 4. Verify task visible in dashboard data
}
```

---

## Build/Release Considerations

### Test Targets

Add new integration test for session routing:

```bash
# build.sh additions to test-int
go test -race -tags=integration ./internal/scheduler/... -run TestDirectorRouting
```

### Smoke Test Update

Update `test-smoke` to verify director-scheduler integration:

```bash
test-smoke)
    # ... existing checks ...

    # Verify scheduler can reach director
    curl -sf -k "https://localhost:$AG_WEB_PORT/api/dashboard" > /dev/null
    echo "✓ Director API accessible"
    ;;
```

### Config Migration

Existing `configs/scheduler.yaml` needs update:

```yaml
# Before (agent-direct)
agent_url: http://localhost:9000

# After (director-routed)
director_url: https://localhost:8443  # Primary
agent_url: http://localhost:9000      # Fallback
```

Document in CHANGELOG.md as breaking config change.

---

## Implementation Plan

### Phase 1: Config and Fallback Structure

1. Add `DirectorURL` to scheduler config
2. Implement fallback logic in `runJob()`
3. Add TLS client for HTTPS director
4. Update config validation
5. Tests: unit tests for config parsing, fallback logic

### Phase 2: Internal API Port

1. Add `-internal-port` flag to ag-view-web
2. Create internal router (no auth middleware)
3. Mount `/api/task` on internal router
4. Update deployment scripts
5. Tests: integration test for internal port

### Phase 3: Session Metadata

1. Add `Source` field to TaskSubmitRequest
2. Update SessionStore to track source
3. Add source filtering to dashboard API
4. Update web UI to display source
5. Tests: verify source tracking end-to-end

### Phase 4: CLI Director Update

1. Add `--director-url` flag to ag-cli
2. Implement director routing with fallback
3. Update CLI help text
4. Tests: CLI integration tests

### Phase 5: System Integration

1. Update deployment scripts for internal port
2. Update configs/scheduler.yaml template
3. Add system test for full flow
4. Update documentation

---

## Rollback Plan

If issues arise, rollback is straightforward:

1. Remove `director_url` from scheduler config
2. Scheduler falls back to direct agent submission
3. No data loss (agent history preserved)
4. Sessions created during director routing remain in history

---

## Future Considerations

This design enables future enhancements:

1. **Session persistence**: Director could persist SessionStore to disk
2. **Multi-agent workflows**: Director could coordinate sessions across agents
3. **Web UI enhancements**: Filter/group sessions by source
4. **Audit logging**: Track all task submissions centrally

For more comprehensive session management (persistence, multi-director support), see the Session Broker design in PLAN.md backlog.

---

## Related Documents

- [DESIGN.md](DESIGN.md) - Core architecture
- [SCHEDULER_DESIGN.md](SCHEDULER_DESIGN.md) - Scheduler architecture
- [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) - State synchronization
- [PLAN.md](PLAN.md) - Project roadmap (Option B in backlog)
