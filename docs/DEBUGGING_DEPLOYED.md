# Debugging Deployed Systems

Quick reference for debugging agency running on remote hosts.

## SSH Connection

```bash
# Uses DEPLOY_HOST, DEPLOY_PORT, DEPLOY_KEY from .env
ssh -i ~/.ssh/<key> -p <port> <user>@<host>
```

## Directory Layout

| Path | Purpose |
|------|---------|
| `~/agency-prod/` | **Production deployment** (binaries, configs, logs, start/stop scripts) |
| `~/agency/` | Git clone (for maintenance jobs — NOT where prod runs) |
| `~/.agency/` | Runtime data (history, sessions, queue, auth) |
| `~/.agency/history/agent/` | Task history JSON files |
| `~/.agency/auth-sessions.json` | Valid sessions for API testing |

**Important:** Prod services run from `~/agency-prod/`, completely isolated from the git clone at `~/agency/`. Any `git pull` or `git clean` on `~/agency/` will NOT affect running services.

## Diagnostic Commands

### 1. Check Running Processes

```bash
ps aux | grep -E 'ag-'
```

Expected: `ag-view-web`, `ag-agent-claude`, `ag-agent-codex`, `ag-scheduler` all running.
Verify they're running from `~/agency-prod/bin/` (not `~/agency/bin/`).

### 2. Verify Binary Versions

```bash
~/agency-prod/bin/ag-view-web -version
~/agency-prod/bin/ag-agent-claude -version
~/agency-prod/bin/ag-scheduler -version
```

### 3. Check Scheduler Health (Most Useful)

```bash
curl -sk https://localhost:9110/status | python3 -m json.tool
```

This shows:
- **`config.port`**: The port in the loaded config (should match the listening port)
- **`config.director_url`**: Where the scheduler sends jobs (should be `http://localhost:9080` in prod)
- **`config.agent_url`**: Fallback agent URL (should be `https://localhost:9100` in prod)
- **`config.config_path`**: Which config file is loaded
- **`jobs[].last_status`**: `queued`, `submitted`, `skipped_error`, `skipped_busy`, `skipped_queue_full`
- **`jobs[].last_error`**: The actual error message when `last_status` is an error (e.g., `connection refused`)

**Quick port mismatch check:**
```bash
# All these should show prod ports (9080, 9100, 9110)
curl -sk https://localhost:9110/status | python3 -c "
import sys,json; d=json.load(sys.stdin)
c=d['config']
print(f\"port: {c['port']}\")
print(f\"director: {c.get('director_url','(none)')}\")
print(f\"agent: {c['agent_url']}\")
errs=[j for j in d['jobs'] if j.get('last_error')]
for j in errs: print(f\"ERROR {j['name']}: {j['last_error']}\")
if not errs: print('No job errors')
"
```

### 4. Test Agent Status

```bash
curl -sk https://localhost:9100/status   # Claude agent
curl -sk https://localhost:9101/status   # Codex agent
```

Expected: JSON with `"type":"agent"`, `"state":"idle"` or `"state":"working"`.

### 5. Check Logs

```bash
tail -50 ~/agency-prod/web.log        # Web view
tail -50 ~/agency-prod/agent.log      # Claude agent
tail -50 ~/agency-prod/scheduler.log  # Scheduler
```

If a log file was deleted but the process is still running (e.g., after a git clean):
```bash
# Find the scheduler PID, then read from its file descriptor
PID=$(pgrep -f ag-scheduler)
cat /proc/$PID/fd/1 | tail -50
```

### 6. Network Listeners

```bash
ss -tlnp | grep -E '9100|9443|9110'
```

Expected: All ports listening.

### 7. Check Deployed Config

```bash
head -20 ~/agency-prod/configs/scheduler.yaml
```

Verify `port`, `director_url`, and `agent_url` all use **prod** ports (9110, 9080, 9100).

## Common Issues

### Scheduler Jobs Failing with "connection refused"

**Symptom:** `curl -sk https://localhost:9110/status` shows `last_error` containing "connection refused" on wrong port.

**Cause:** Scheduler config has dev ports instead of prod ports. This can happen if:
- Config was copied without port transforms
- Config was overwritten by a git operation on the same directory

**Fix:**
```bash
# From local machine:
./build.sh deploy-prod-scheduler-config
# Scheduler auto-reloads within 60 seconds
```

### Agent Not Showing in UI

1. **Check agent is running**: `curl -sk https://localhost:9100/status`
2. **Check discovery range**: Web view scans ports 9100-9110 in prod
3. **Verify API returns agent**: Query `/api/agents` with valid session

### Task Stuck in "Working" State

See [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) for details.

```bash
# Check agent's actual state
curl -sk https://localhost:9100/status | python3 -m json.tool
# If state is "idle" but UI shows "working", hard refresh browser
```

### TLS Handshake Errors in Logs

Noise from external scanners probing the server. Ignore unless from expected IPs.

## Restart Services

```bash
cd ~/agency-prod
./stop.sh
./start.sh
```
