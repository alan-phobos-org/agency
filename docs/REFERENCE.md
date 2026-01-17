# API Reference

Detailed endpoint specifications and technical reference for Agency components.

**Read this file when:** implementing API changes, debugging HTTP issues, or extending endpoints.

---

## Agent Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Agent state, version, config, current task preview |
| `/task` | POST | Submit task (prompt, timeout, env, model, session_id, project, thinking) |
| `/task/:id` | GET | Task status and output (includes session_id) |
| `/task/:id/cancel` | POST | Cancel running task |
| `/shutdown` | POST | Graceful shutdown (supports force flag) |
| `/history` | GET | Paginated task history (page, limit params) |
| `/history/:id` | GET | Full task details with execution outline |
| `/history/:id/debug` | GET | Raw Claude output (retained for 20 most recent tasks) |

### Agent States

```
idle → working → idle
         ↓
       error → idle (after logging)
```

- `idle` - Ready to accept tasks
- `working` - Executing a task
- `cancelling` - Task cancellation in progress

### Task Request Fields

```json
{
  "prompt": "string (required)",
  "timeout_seconds": "int (optional)",
  "env": "map[string]string (optional)",
  "model": "string (optional, default: sonnet)",
  "session_id": "string (optional, generates if omitted)",
  "project": {"name": "string", "prompt": "string"} (optional),
  "thinking": "bool (optional)"
}
```

---

## Web View Endpoints

### Public (No Auth)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/status` | GET | Universal status endpoint |
| `/login` | GET | Login form |
| `/login` | POST | Authenticate with password |
| `/pair` | GET | Device pairing form |
| `/pair` | POST | Exchange pairing code for session |

### Authenticated

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Dashboard HTML page |
| `/logout` | POST | End session |
| `/api/agents` | GET | List discovered agents |
| `/api/directors` | GET | List discovered directors |
| `/api/contexts` | GET | List available task contexts |
| `/api/task` | POST | Submit task to selected agent |
| `/api/task/:id` | GET | Get task status (requires agent_url param) |
| `/api/sessions` | GET | List all sessions |
| `/api/sessions` | POST | Add task to session |
| `/api/sessions/:id/tasks/:taskId` | PUT | Update task state |
| `/api/pair/code` | POST | Generate pairing code (10min TTL) |
| `/api/devices` | GET | List active sessions/devices |
| `/api/devices/:id` | DELETE | Revoke device session |
| `/api/queue/task` | POST | Submit task to queue |
| `/api/queue` | GET | Queue status and pending tasks |
| `/api/queue/:id` | GET | Specific queued task status |
| `/api/queue/:id/cancel` | POST | Cancel queued task |

### Queue Endpoints

The work queue allows tasks to be queued when agents are busy. The dispatcher automatically dispatches pending tasks to idle agents.

**Submit to Queue**
```json
POST /api/queue/task
{
  "prompt": "string (required)",
  "model": "string (optional)",
  "timeout_seconds": "int (optional)",
  "session_id": "string (optional)",
  "source": "string (optional, e.g., web, scheduler, cli)",
  "source_job": "string (optional, job name if scheduler)"
}

Response (201):
{
  "queue_id": "queue-123",
  "position": 1,
  "state": "pending"
}
```

**Queue Status**
```json
GET /api/queue

Response:
{
  "depth": 5,
  "max_size": 50,
  "oldest_age_seconds": 120,
  "dispatched_count": 2,
  "tasks": [
    {
      "queue_id": "queue-123",
      "state": "pending",
      "position": 1,
      "prompt_preview": "First 100 chars...",
      "source": "scheduler"
    }
  ]
}
```

**Queue States**
- `pending` - In queue, waiting for agent
- `dispatching` - Being sent to agent
- `working` - Running on agent
- `completed` - Finished
- `failed` - Failed
- `cancelled` - Cancelled

---

## Configuration Reference

### Agent Config (YAML)

```yaml
port: 9000
log_level: info
session_dir: /tmp/agency/sessions
history_dir: ~/.agency/history
preprompt_file: /path/to/custom.md  # optional, falls back to embedded

claude:
  model: sonnet      # default model (overridable per-task)
  timeout: 30m       # default timeout (overridable per-task)
  max_turns: 50      # conversation turn limit
```

### Web View Config

Environment variables:
- `AG_WEB_PASSWORD` - Required password for authentication
- `AG_WEB_PORT` - Port (default: 8443)
- `AG_AGENT_PORT` - Agent port for deployment scripts (default: 9000)
- `AGENCY_ROOT` - Override config directory (default: ~/.agency)
- `CLAUDE_BIN` - Path to Claude CLI (default: claude from PATH)

### Claude Code CLI Authentication

The agent inherits environment variables and passes them to the Claude CLI. Supported auth methods:

| Variable | Purpose |
|----------|---------|
| `ANTHROPIC_API_KEY` | Direct API key authentication (pay-per-use billing) |
| `CLAUDE_CODE_OAUTH_TOKEN` | OAuth token from Max subscription or similar |

For CI/GitHub Actions, use `CLAUDE_CODE_OAUTH_TOKEN` as a repository secret.
For production deployments, the `.env` file should contain `CLAUDE_CODE_OAUTH_TOKEN`.

The agent uses `--model` flag with shorthand names: `haiku`, `sonnet`, `opus`.

Command-line flags:
- `-port` - HTTPS port
- `-port-start`, `-port-end` - Discovery scan range (default: 9000-9010; deployments often set 9000-9010/9100-9110)
- `-contexts` - Path to contexts YAML file
- `-access-log` - Path to access log file

### Task Contexts (YAML)

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

---

## Interface Definitions

### Core Interfaces

| Interface | Methods | Purpose |
|-----------|---------|---------|
| Statusable | `GET /status` | Report type, version, basic config |
| Taskable | `POST /task`, `GET /task/:id` | Accept prompts, execute work |
| Observable | `GET /tasks` | Report held tasks |
| Configurable | `GET/SET /config` | Get/set config (Phase 2+) |

### Component Types

| Type | Interfaces | Examples |
|------|------------|----------|
| Agent | Statusable + Taskable | ag-agent-claude |
| Director | Statusable + Observable + Taskable | ag-cli (CLI director) |
| Helper | Statusable + Observable | ag-scheduler |
| View | Statusable + Observable | ag-view-web |

---

## Session Management

### Session Directories

Agent uses shared session directories (`/tmp/agency/sessions/<session_id>/`):
- New sessions: directory is created fresh (cleaned if exists)
- Resumed sessions: directory is reused with existing state

### Multi-turn Conversations

Pass `session_id` in task request to continue a session. Response always includes `session_id`.
Session IDs must be 1-128 chars of `A-Za-z0-9._-` and cannot include `..` or path separators.

### Max Turns and Auto-Resume

The Claude CLI limits each task to max turns (default: 50). When hit:
1. Agent automatically resumes (up to 2 additional attempts)
2. If still incomplete after 3 total attempts, task fails with `max_turns` error
3. Error suggests breaking the task into smaller steps

---

## Authentication

### Password Login
Set `AG_WEB_PASSWORD` env var, login at `/login`.

### Device Pairing
Generate pairing code from dashboard, enter at `/pair`.

### Session Types
- Auth sessions: 12h, auto-refresh
- Device sessions: long-lived

### Security
- Cookies: HttpOnly, Secure, SameSite=Strict
- Rate limiting: 10 failed attempts = 1 hour block
- Session storage: `~/.agency/auth-sessions.json`

---

## Task History

Stored at `~/.agency/history/<agent-name>/`:
- Outline entries: 100 tasks retained with execution step previews (200 char limit)
- Debug logs: 20 most recent tasks retain full Claude output
- Persisted to disk, survives agent restarts

---

## Design Patterns

See [DESIGN.md](DESIGN.md) for fallback behavior, embedded instructions, and session directory conventions.

---

## Extended Thinking

The `thinking` parameter is accepted for future use. Extended thinking is automatically enabled by the Claude CLI for compatible models (no CLI flag exists to control it). The UI toggle and contexts `thinking` field are preserved for compatibility.
