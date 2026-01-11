#!/bin/bash
# Deploy agency to a remote host
# Usage: deploy-agency.sh <hostname> [port]
#
# Builds binaries for Linux, copies them to the remote host along with .env,
# and starts the agency services.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Parse arguments
if [ $# -lt 1 ]; then
    echo "Usage: $0 <hostname> [ssh-port] [ssh-key]"
    echo ""
    echo "Arguments:"
    echo "  hostname   Remote host (user@host or just host)"
    echo "  ssh-port   SSH port (default: 22)"
    echo "  ssh-key    Path to SSH private key (optional)"
    echo ""
    echo "Environment variables:"
    echo "  AG_WEB_PORT    Web director port on remote (default: 8443)"
    echo "  AG_AGENT_PORT  Agent port on remote (default: 9000)"
    echo "  REMOTE_DIR     Installation directory (default: ~/agency)"
    echo "  SSH_KEY        Path to SSH private key (alternative to argument)"
    exit 1
fi

REMOTE_HOST="$1"
SSH_PORT="${2:-22}"
SSH_KEY="${3:-${SSH_KEY:-}}"

# Build SSH options
SSH_OPTS="-C -p $SSH_PORT"
SCP_OPTS="-C -P $SSH_PORT"
if [ -n "$SSH_KEY" ]; then
    SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
    SCP_OPTS="$SCP_OPTS -i $SSH_KEY"
fi
REMOTE_DIR="${REMOTE_DIR:-~/agency}"
WEB_PORT="${AG_WEB_PORT:-8443}"
AGENT_PORT="${AG_AGENT_PORT:-9000}"

echo "=== Agency Remote Deployment ==="
echo "Host: $REMOTE_HOST"
echo "SSH Port: $SSH_PORT"
echo "Remote Dir: $REMOTE_DIR"
echo ""

# Build Linux binaries
echo "Building Linux binaries..."
cd "$PROJECT_ROOT"
GOOS=linux GOARCH=amd64 ./build.sh build

# Verify binaries exist
for bin in ag-view-web ag-agent-claude; do
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
ssh $SSH_OPTS "$REMOTE_HOST" "mkdir -p $REMOTE_DIR/bin $REMOTE_DIR/deployment $REMOTE_DIR/configs"

# Stop running services before copying (binaries can't be overwritten while running)
echo "Stopping existing services..."
ssh $SSH_OPTS "$REMOTE_HOST" "[ -f $REMOTE_DIR/stop.sh ] && $REMOTE_DIR/stop.sh || true"

# Copy binaries
echo "Copying binaries..."
scp $SCP_OPTS \
    "$PROJECT_ROOT/bin/ag-view-web" \
    "$PROJECT_ROOT/bin/ag-agent-claude" \
    "$REMOTE_HOST:$REMOTE_DIR/bin/"

# Copy .env if it exists
if [ -f "$PROJECT_ROOT/.env" ]; then
    echo "Copying .env..."
    scp $SCP_OPTS "$PROJECT_ROOT/.env" "$REMOTE_HOST:$REMOTE_DIR/"
fi

# Copy contexts config if it exists
if [ -f "$PROJECT_ROOT/configs/contexts.yaml" ]; then
    echo "Copying contexts config..."
    scp $SCP_OPTS "$PROJECT_ROOT/configs/contexts.yaml" "$REMOTE_HOST:$REMOTE_DIR/configs/"
fi

# Copy deployment scripts
echo "Copying deployment scripts..."
scp $SCP_OPTS \
    "$SCRIPT_DIR/agency.sh" \
    "$SCRIPT_DIR/stop-agency.sh" \
    "$REMOTE_HOST:$REMOTE_DIR/deployment/"

# Create a remote-specific start script that uses absolute paths
echo "Creating remote start script..."
ssh $SSH_OPTS "$REMOTE_HOST" "cat > $REMOTE_DIR/start.sh" << 'REMOTE_SCRIPT'
#!/bin/bash
# Start agency on remote host
set -euo pipefail

AGENCY_DIR="$(cd "$(dirname "$0")" && pwd)"
PID_FILE="$AGENCY_DIR/agency.pids"

WEB_PORT="${AG_WEB_PORT:-8443}"
AGENT_PORT="${AG_AGENT_PORT:-9000}"

# Load env vars from .env if not set
if [ -f "$AGENCY_DIR/.env" ]; then
    if [ -z "${AG_WEB_PASSWORD:-}" ]; then
        AG_WEB_PASSWORD=$(grep '^AG_WEB_PASSWORD=' "$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export AG_WEB_PASSWORD
    fi
    if [ -z "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
        CLAUDE_CODE_OAUTH_TOKEN=$(grep '^CLAUDE_CODE_OAUTH_TOKEN=' "$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export CLAUDE_CODE_OAUTH_TOKEN
    fi
    if [ -z "${GITHUB_TOKEN:-}" ]; then
        GITHUB_TOKEN=$(grep '^GITHUB_TOKEN=' "$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export GITHUB_TOKEN
    fi
    if [ -z "${GIT_SSH_KEY_FILE:-}" ]; then
        GIT_SSH_KEY_FILE=$(grep '^GIT_SSH_KEY_FILE=' "$AGENCY_DIR/.env" | cut -d= -f2 || true)
        export GIT_SSH_KEY_FILE
    fi
fi

# Check if already running
if [ -f "$PID_FILE" ]; then
    echo "Agency appears to be running. Stop it first."
    exit 1
fi

# Start web view
CONTEXTS_ARG=""
if [ -f "$AGENCY_DIR/configs/contexts.yaml" ]; then
    CONTEXTS_ARG="-contexts $AGENCY_DIR/configs/contexts.yaml"
fi
echo "Starting web view on port $WEB_PORT..."
"$AGENCY_DIR/bin/ag-view-web" -port "$WEB_PORT" -env "$AGENCY_DIR/.env" $CONTEXTS_ARG > "$AGENCY_DIR/web.log" 2>&1 &
WEB_PID=$!

# Start claude agent
echo "Starting claude agent on port $AGENT_PORT..."
"$AGENCY_DIR/bin/ag-agent-claude" -port "$AGENT_PORT" > "$AGENCY_DIR/agent.log" 2>&1 &
AGENT_PID=$!

# Save PIDs
echo "$WEB_PID" > "$PID_FILE"
echo "$AGENT_PID" >> "$PID_FILE"

# Wait for services
sleep 2

# Check if processes are running
if ! kill -0 "$WEB_PID" 2>/dev/null; then
    echo "ERROR: Web view failed to start. Check web.log"
    rm -f "$PID_FILE"
    exit 1
fi

if ! kill -0 "$AGENT_PID" 2>/dev/null; then
    echo "ERROR: Agent failed to start. Check agent.log"
    kill "$WEB_PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
fi

echo ""
echo "Agency started!"
echo "  Web View PID: $WEB_PID"
echo "  Agent PID: $AGENT_PID"
echo ""
echo "Dashboard: https://$(hostname):$WEB_PORT"
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
ssh $SSH_OPTS "$REMOTE_HOST" "cd $REMOTE_DIR && AG_WEB_PORT=$WEB_PORT AG_AGENT_PORT=$AGENT_PORT ./start.sh"

echo ""
echo "=== Deployment Complete ==="
echo ""
echo "Remote commands:"
SSH_CMD="ssh $SSH_OPTS $REMOTE_HOST"
echo "  Start:  $SSH_CMD '$REMOTE_DIR/start.sh'"
echo "  Stop:   $SSH_CMD '$REMOTE_DIR/stop.sh'"
echo "  Logs:   $SSH_CMD 'tail -f $REMOTE_DIR/*.log'"
