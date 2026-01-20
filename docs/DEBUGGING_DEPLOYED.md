# Debugging Deployed Systems

Quick reference for debugging agency running on remote hosts.

## SSH Connection

```bash
ssh -i ~/.ssh/<key> -p <port> <user>@<host>
```

## Diagnostic Commands

### 1. Check Running Processes

```bash
ps aux | grep -E 'ag-|agency'
```

Expected: `ag-view-web`, `ag-agent-claude`, `ag-scheduler` all running.

### 2. Verify Binary Versions

```bash
~/agency/bin/ag-view-web -version
~/agency/bin/ag-agent-claude -version
~/agency/bin/ag-scheduler -version
```

### 3. Test Agent Status Directly

```bash
curl -s -k https://localhost:9000/status
```

Expected: JSON with `"type":"agent"`, `"state":"idle"` or `"state":"working"`.

### 4. Test Discovery (with auth)

Get a valid session from `~/.agency/auth-sessions.json`, then:

```bash
curl -s -k -H 'Cookie: agency_session=<session_id>' https://localhost:8443/api/dashboard
```

### 5. Check Logs

```bash
tail -50 ~/agency/web.log      # Web view logs
tail -50 ~/agency/agent.log    # Agent logs
tail -50 ~/agency/scheduler.log # Scheduler logs
```

### 6. Network Listeners

```bash
netstat -tlnp 2>/dev/null | grep -E '9000|8443|9100'
```

Expected: All three ports listening (may show as `:::` for IPv6).

## Common Issues

### Agent Not Showing in UI

1. **Check agent is running**: `curl -k https://localhost:9000/status`
2. **Check discovery range**: Web view scans ports 9000-9010 by default (dev) or 9100-9110 (prod)
3. **Verify API returns agent**: Query `/api/agents` with valid session

### Task Stuck in "Working" State

The web UI can show tasks as "working" when they've actually completed. This is a known state sync issue.

**Diagnosis:**
```bash
# Check agent's actual state
curl -s -k https://localhost:9000/status | python3 -m json.tool

# If state is "idle" but UI shows "working", it's a UI sync issue
```

**Resolution:**
- Hard refresh browser (Ctrl+Shift+R / Cmd+Shift+R)
- Check `/api/dashboard` directly for true state
- See [TASK_STATE_SYNC_DESIGN.md](TASK_STATE_SYNC_DESIGN.md) for architecture details

### TLS Handshake Errors in Logs

Noise from external scanners probing the server. Ignore unless from expected IPs.

### Services Not Starting

Check the start script and PID file:
```bash
cat ~/agency/agency.pids
cat ~/agency/start.sh
```

## File Locations

| Path | Purpose |
|------|---------|
| `~/agency/` | Deployment directory |
| `~/agency/bin/` | Binaries |
| `~/agency/configs/` | contexts.yaml, scheduler.yaml |
| `~/agency/.env` | Environment variables |
| `~/.agency/` | Runtime data |
| `~/.agency/auth-sessions.json` | Valid sessions for API testing |
| `~/.agency/history/agent/` | Task history JSON files |

## Restart Services

```bash
cd ~/agency
./stop.sh
./start.sh
```
