#!/bin/bash
# Deploy agency to a remote host
# Usage: deploy-agency.sh <hostname> [mode] [ssh-port] [ssh-key]
#
# Builds binaries for Linux, copies them to the remote host along with .env,
# configs, and starts the agency services (web view, agent, and scheduler).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Load port configuration
if [ -f "$SCRIPT_DIR/ports.conf" ]; then
    source "$SCRIPT_DIR/ports.conf"
fi

# Parse arguments
if [ $# -lt 1 ]; then
    echo "Usage: $0 <hostname> [mode] [ssh-port] [ssh-key]"
    echo ""
    echo "Arguments:"
    echo "  hostname   Remote host (user@host or just host)"
    echo "  mode       Deployment mode: dev (default) or prod"
    echo "  ssh-port   SSH port (default: 22)"
    echo "  ssh-key    Path to SSH private key (optional)"
    echo ""
    echo "Mode determines port ranges:"
    echo "  dev:  Web=$DEV_WEB_PORT, Agent=$DEV_AGENT_PORT, Discovery=$DEV_DISCOVERY_START-$DEV_DISCOVERY_END"
    echo "  prod: Web=$PROD_WEB_PORT, Agent=$PROD_AGENT_PORT, Discovery=$PROD_DISCOVERY_START-$PROD_DISCOVERY_END"
    echo ""
    echo "Environment variables (override defaults):"
    echo "  AG_WEB_PORT           Web view port on remote"
    echo "  AG_WEB_INTERNAL_PORT  Internal API port for scheduler/CLI"
    echo "  AG_AGENT_PORT         Agent port on remote"
    echo "  AG_SCHEDULER_PORT     Scheduler port on remote"
    echo "  AG_DISCOVERY_START    Discovery port range start"
    echo "  AG_DISCOVERY_END      Discovery port range end"
    echo "  AG_SCHEDULER_CONFIG   Path to scheduler config (default: configs/scheduler.yaml)"
    echo "  REMOTE_DIR            Installation directory (overrides mode default)"
    echo "  SSH_KEY               Path to SSH private key (alternative to argument)"
    exit 1
fi

REMOTE_HOST="$1"
MODE="${2:-dev}"
SSH_PORT="${3:-22}"
SSH_KEY="${4:-${SSH_KEY:-}}"

# Set environment based on mode
set_agency_env "$MODE"

# Build SSH options
SSH_OPTS="-C -p $SSH_PORT"
SCP_OPTS="-C -P $SSH_PORT"
if [ -n "$SSH_KEY" ]; then
    SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
    SCP_OPTS="$SCP_OPTS -i $SSH_KEY"
fi

# Ports are set by set_agency_env
WEB_PORT="$AG_WEB_PORT"
WEB_INTERNAL_PORT="$AG_WEB_INTERNAL_PORT"
AGENT_PORT="$AG_AGENT_PORT"
SCHEDULER_PORT="$AG_SCHEDULER_PORT"
DISCOVERY_START="$AG_DISCOVERY_START"
DISCOVERY_END="$AG_DISCOVERY_END"
SCHEDULER_CONFIG="${AG_SCHEDULER_CONFIG:-$PROJECT_ROOT/configs/scheduler.yaml}"

echo "=== Agency Remote Deployment ($MODE) ==="
echo "Host: $REMOTE_HOST"
echo "SSH Port: $SSH_PORT"
echo "Remote Dir: $REMOTE_DIR"
echo "Ports: Web=$WEB_PORT, Agent=$AGENT_PORT, Discovery=$DISCOVERY_START-$DISCOVERY_END"
echo ""

# Build Linux binaries
echo "Building Linux binaries..."
cd "$PROJECT_ROOT"
GOOS=linux GOARCH=amd64 ./build.sh build

# Verify binaries exist
for bin in ag-view-web ag-agent-claude ag-agent-codex ag-scheduler; do
    if [ ! -f "$PROJECT_ROOT/bin/$bin" ]; then
        echo "ERROR: Binary $bin not found"
        exit 1
    fi
done

# Check for .env file
if [ ! -f "$PROJECT_ROOT/.env" ]; then
    echo "WARNING: No .env file found. You may need to set AG_WEB_PASSWORD on remote."
fi

# Create remote directory structure
echo "Creating remote directory..."
ssh $SSH_OPTS "$REMOTE_HOST" "mkdir -p $REMOTE_DIR/bin $REMOTE_DIR/deployment $REMOTE_DIR/configs $REMOTE_DIR/prompts"

# Stop running services before copying (binaries can't be overwritten while running)
echo "Stopping existing services..."
ssh $SSH_OPTS "$REMOTE_HOST" "[ -f $REMOTE_DIR/stop.sh ] && $REMOTE_DIR/stop.sh || true"

# Copy binaries
echo "Copying binaries..."
scp $SCP_OPTS \
    "$PROJECT_ROOT/bin/ag-view-web" \
    "$PROJECT_ROOT/bin/ag-agent-claude" \
    "$PROJECT_ROOT/bin/ag-agent-codex" \
    "$PROJECT_ROOT/bin/ag-scheduler" \
    "$REMOTE_HOST:$REMOTE_DIR/bin/"

# Copy .env if it exists
if [ -f "$PROJECT_ROOT/.env" ]; then
    echo "Copying .env..."
    scp $SCP_OPTS "$PROJECT_ROOT/.env" "$REMOTE_HOST:$REMOTE_DIR/"
fi

# Copy configs if they exist
if [ -n "$SCHEDULER_CONFIG" ] && [ -f "$SCHEDULER_CONFIG" ]; then
    echo "Copying scheduler config (adjusting ports for $MODE mode)..."
    # Transform director_url and agent_url ports to match deployment mode
    sed -e "s|director_url: http://localhost:[0-9]*|director_url: http://localhost:$WEB_INTERNAL_PORT|" \
        -e "s|agent_url: http://localhost:[0-9]*|agent_url: http://localhost:$AGENT_PORT|" \
        "$SCHEDULER_CONFIG" | ssh $SSH_OPTS "$REMOTE_HOST" "cat > $REMOTE_DIR/configs/scheduler.yaml"
fi

# Copy prompts
if [ -d "$PROJECT_ROOT/prompts" ]; then
    echo "Copying agency prompts..."
    scp $SCP_OPTS "$PROJECT_ROOT/prompts/"*-prod.md "$REMOTE_HOST:$REMOTE_DIR/prompts/" 2>/dev/null || \
        echo "  (No production prompts found, skipping)"
fi

# Copy deployment scripts
echo "Copying deployment scripts..."
scp $SCP_OPTS \
    "$SCRIPT_DIR/agency.sh" \
    "$SCRIPT_DIR/stop-agency.sh" \
    "$SCRIPT_DIR/ports.conf" \
    "$REMOTE_HOST:$REMOTE_DIR/deployment/"

# Create a remote-specific start script with baked-in port configuration
# Note: Using unquoted REMOTE_SCRIPT to expand $WEB_PORT etc, but escaping
# variables that should remain as shell variables on the remote side
echo "Creating remote start script..."
ssh $SSH_OPTS "$REMOTE_HOST" "cat > $REMOTE_DIR/start.sh" << REMOTE_SCRIPT
#!/bin/bash
# Start agency on remote host
# Port configuration baked in at deploy time: mode=$MODE
set -euo pipefail

AGENCY_DIR="\$(cd "\$(dirname "\$0")" && pwd)"
PID_FILE="\$AGENCY_DIR/agency.pids"

# Ports configured at deployment time
WEB_PORT=$WEB_PORT
WEB_INTERNAL_PORT=$WEB_INTERNAL_PORT
AGENT_PORT=$AGENT_PORT
AGENT_CODEX_PORT=$AGENT_CODEX_PORT
SCHEDULER_PORT=$SCHEDULER_PORT
DISCOVERY_START=$DISCOVERY_START
DISCOVERY_END=$DISCOVERY_END
SCHEDULER_CONFIG="\$AGENCY_DIR/configs/scheduler.yaml"
AGENCY_PROMPTS_DIR="\$AGENCY_DIR/prompts"

# Load env vars from .env if not set
if [ -f "\$AGENCY_DIR/.env" ]; then
    if [ -z "\${AG_WEB_PASSWORD:-}" ]; then
        AG_WEB_PASSWORD=\$(grep '^AG_WEB_PASSWORD=' "\$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export AG_WEB_PASSWORD
    fi
    if [ -z "\${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
        CLAUDE_CODE_OAUTH_TOKEN=\$(grep '^CLAUDE_CODE_OAUTH_TOKEN=' "\$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export CLAUDE_CODE_OAUTH_TOKEN
    fi
    if [ -z "\${GITHUB_TOKEN:-}" ]; then
        GITHUB_TOKEN=\$(grep '^GITHUB_TOKEN=' "\$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export GITHUB_TOKEN
    fi
    if [ -z "\${GIT_SSH_KEY_FILE:-}" ]; then
        GIT_SSH_KEY_FILE=\$(grep '^GIT_SSH_KEY_FILE=' "\$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export GIT_SSH_KEY_FILE
    fi
fi

# Set prompts directory if it exists
if [ -d "\$AGENCY_PROMPTS_DIR" ]; then
    export AGENCY_PROMPTS_DIR
fi

# Check if already running
if [ -f "\$PID_FILE" ]; then
    echo "Agency appears to be running. Stop it first."
    exit 1
fi

# Start web view (with internal API port for scheduler/CLI routing)
echo "Starting web view on port \$WEB_PORT (internal: \$WEB_INTERNAL_PORT, discovery: \$DISCOVERY_START-\$DISCOVERY_END)..."
"\$AGENCY_DIR/bin/ag-view-web" -port "\$WEB_PORT" -internal-port "\$WEB_INTERNAL_PORT" -port-start "\$DISCOVERY_START" -port-end "\$DISCOVERY_END" -env "\$AGENCY_DIR/.env" > "\$AGENCY_DIR/web.log" 2>&1 &
WEB_PID=\$!

# Start claude agent
echo "Starting claude agent on port \$AGENT_PORT..."
"\$AGENCY_DIR/bin/ag-agent-claude" -port "\$AGENT_PORT" > "\$AGENCY_DIR/agent.log" 2>&1 &
AGENT_PID=\$!

# Start codex agent
echo "Starting codex agent on port \$AGENT_CODEX_PORT..."
"\$AGENCY_DIR/bin/ag-agent-codex" -port "\$AGENT_CODEX_PORT" > "\$AGENCY_DIR/agent-codex.log" 2>&1 &
AGENT_CODEX_PID=\$!

# Start scheduler (optional)
SCHEDULER_PID=""
if [ -f "\$SCHEDULER_CONFIG" ]; then
    echo "Starting scheduler on port \$SCHEDULER_PORT..."
    "\$AGENCY_DIR/bin/ag-scheduler" -config "\$SCHEDULER_CONFIG" -port "\$SCHEDULER_PORT" > "\$AGENCY_DIR/scheduler.log" 2>&1 &
    SCHEDULER_PID=\$!
fi

# Save PIDs
echo "\$WEB_PID" > "\$PID_FILE"
echo "\$AGENT_PID" >> "\$PID_FILE"
echo "\$AGENT_CODEX_PID" >> "\$PID_FILE"
if [ -n "\$SCHEDULER_PID" ]; then
    echo "\$SCHEDULER_PID" >> "\$PID_FILE"
fi

# Wait for services
sleep 2

# Check if processes are running
if ! kill -0 "\$WEB_PID" 2>/dev/null; then
    echo "ERROR: Web view failed to start. Check web.log"
    rm -f "\$PID_FILE"
    exit 1
fi

if ! kill -0 "\$AGENT_PID" 2>/dev/null; then
    echo "ERROR: Claude agent failed to start. Check agent.log"
    kill "\$WEB_PID" 2>/dev/null || true
    kill "\$AGENT_CODEX_PID" 2>/dev/null || true
    [ -n "\$SCHEDULER_PID" ] && kill "\$SCHEDULER_PID" 2>/dev/null || true
    rm -f "\$PID_FILE"
    exit 1
fi

if ! kill -0 "\$AGENT_CODEX_PID" 2>/dev/null; then
    echo "ERROR: Codex agent failed to start. Check agent-codex.log"
    kill "\$WEB_PID" 2>/dev/null || true
    kill "\$AGENT_PID" 2>/dev/null || true
    [ -n "\$SCHEDULER_PID" ] && kill "\$SCHEDULER_PID" 2>/dev/null || true
    rm -f "\$PID_FILE"
    exit 1
fi

if [ -n "\$SCHEDULER_PID" ] && ! kill -0 "\$SCHEDULER_PID" 2>/dev/null; then
    echo "ERROR: Scheduler failed to start. Check scheduler.log"
    kill "\$WEB_PID" 2>/dev/null || true
    kill "\$AGENT_PID" 2>/dev/null || true
    kill "\$AGENT_CODEX_PID" 2>/dev/null || true
    rm -f "\$PID_FILE"
    exit 1
fi

echo ""
echo "Agency started!"
echo "  Web View PID: \$WEB_PID (HTTPS: \$WEB_PORT, Internal: \$WEB_INTERNAL_PORT)"
echo "  Claude Agent PID: \$AGENT_PID (port: \$AGENT_PORT)"
echo "  Codex Agent PID: \$AGENT_CODEX_PID (port: \$AGENT_CODEX_PORT)"
if [ -n "\$SCHEDULER_PID" ]; then
    echo "  Scheduler PID: \$SCHEDULER_PID"
fi
echo "  Discovery range: \$DISCOVERY_START-\$DISCOVERY_END"
echo ""
echo "Dashboard: https://\$(hostname):\$WEB_PORT"
echo "Internal API: http://localhost:\$WEB_INTERNAL_PORT (scheduler/CLI routing)"
REMOTE_SCRIPT

ssh $SSH_OPTS "$REMOTE_HOST" "chmod +x $REMOTE_DIR/start.sh"

# Create remote stop script
echo "Creating remote stop script..."
ssh $SSH_OPTS "$REMOTE_HOST" "cat > $REMOTE_DIR/stop.sh" << 'REMOTE_SCRIPT'
#!/bin/bash
# Stop agency on remote host
set -euo pipefail

AGENCY_DIR="$(cd "$(dirname "$0")" && pwd)"
PID_FILE="$AGENCY_DIR/agency.pids"

if [ ! -f "$PID_FILE" ]; then
    echo "No agency.pids file found."
    exit 0
fi

echo "Stopping agency..."
while read -r PID; do
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        echo "  Terminating PID $PID..."
        kill "$PID" 2>/dev/null || true
    fi
done < "$PID_FILE"

sleep 1

while read -r PID; do
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        echo "  Force killing PID $PID..."
        kill -9 "$PID" 2>/dev/null || true
    fi
done < "$PID_FILE"

rm -f "$PID_FILE"
echo "Agency stopped."
REMOTE_SCRIPT

ssh $SSH_OPTS "$REMOTE_HOST" "chmod +x $REMOTE_DIR/stop.sh"

# Open firewall ports if ufw is available
echo ""
echo "Configuring firewall..."
ssh $SSH_OPTS "$REMOTE_HOST" "if command -v ufw >/dev/null 2>&1; then
    sudo ufw allow $WEB_PORT/tcp comment 'Agency web director' && echo '  Opened port $WEB_PORT (web director)'
else
    echo '  ufw not found, skipping firewall configuration'
fi"

# Start the agency on remote
echo ""
echo "Starting agency on remote host..."
ssh $SSH_OPTS "$REMOTE_HOST" "cd $REMOTE_DIR && ./start.sh"

echo ""
echo "=== Deployment Complete ($MODE) ==="
echo ""
echo "Remote commands:"
SSH_CMD="ssh $SSH_OPTS $REMOTE_HOST"
echo "  Start:  $SSH_CMD '$REMOTE_DIR/start.sh'"
echo "  Stop:   $SSH_CMD '$REMOTE_DIR/stop.sh'"
echo "  Logs:   $SSH_CMD 'tail -f $REMOTE_DIR/*.log'"
