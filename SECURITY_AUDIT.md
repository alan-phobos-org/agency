# Security Audit

Date: 2025-02-14

## Findings

1) Critical: agent listens on all interfaces with no auth while running Claude with `--dangerously-skip-permissions`. Remote callers can execute tasks with full local access.
   - Evidence: `internal/agent/agent.go`, `cmd/ag-agent-claude/main.go`
2) Critical: user-supplied `session_id` is used directly as a working directory; absolute paths or `../` allow arbitrary directory access.
   - Evidence: `internal/agent/agent.go`
3) High: web director proxies `agent_url` for task/history endpoints without restricting to discovered agents, enabling SSRF to internal services.
   - Evidence: `internal/view/web/handlers.go`
4) High: auth token in the URL combined with CDN assets leaks the token via `Referer`; access logging also records the full URL with token.
   - Evidence: `internal/view/web/templates/dashboard.html`, `internal/view/web/auth.go`, `cmd/ag-view-web/main.go`
5) Medium: rate limiting trusts `X-Real-IP` directly and falls back to `RemoteAddr` including port, making it easy to spoof or bypass.
   - Evidence: `internal/view/web/auth.go`
6) Medium: `/status` assumes `currentTask.StartedAt` is non-nil; there is a window where this can panic.
   - Evidence: `internal/agent/agent.go`
7) Low: history debug reads use a path derived directly from `task_id`, allowing path traversal to read any `.debug.log` file.
   - Evidence: `internal/history/history.go`, `internal/agent/agent.go`
8) Low: task struct is mutated without consistent locking; `handleGetTask` can race with `executeTask` updates.
   - Evidence: `internal/agent/agent.go`

## Proposed Fixes (Simple)

### Fix for Issue 1 (Unauthenticated agent + dangerous permissions)
- Bind agent to localhost by default and require an auth token for `/task`, `/history`, and `/shutdown`.
- Example:
  - Add `-bind` flag (default `127.0.0.1`) and use it in `Agent.Start` to listen only on localhost.
  - Add optional `AG_AGENT_TOKEN` (or config field) and a middleware that checks `Authorization: Bearer <token>` for mutating endpoints.
  - Keep `/status` unauthenticated for discovery if needed.

### Fix for Issue 2 (Path traversal via session_id)
- Validate `session_id` before using it as a directory name.
- Example:
  - Accept only UUIDs (or a strict regex like `^[a-zA-Z0-9_-]{1,64}$`).
  - Reject values containing path separators (`/` or `\`) or leading `.`.
  - Use `filepath.Clean` and ensure the final path is still under `SessionDir` before creating it.
