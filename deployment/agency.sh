#!/bin/bash
# Start agency: web view + claude agent
# Spawns both as background processes and reports the dashboard URL

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PID_DIR="$PROJECT_ROOT/deployment"
PID_FILE="$PID_DIR/agency.pids"

# Default ports
WEB_PORT="${AG_WEB_PORT:-8443}"
AGENT_PORT="${AG_AGENT_PORT:-9000}"

# Load env vars from .env if not set
if [ -f "$PROJECT_ROOT/.env" ]; then
    if [ -z "${GITHUB_TOKEN:-}" ]; then
        GITHUB_TOKEN=$(grep '^GITHUB_TOKEN=' "$PROJECT_ROOT/.env" | cut -d= -f2)
        export GITHUB_TOKEN
    fi
    if [ -z "${GIT_SSH_KEY_FILE:-}" ]; then
        GIT_SSH_KEY_FILE=$(grep '^GIT_SSH_KEY_FILE=' "$PROJECT_ROOT/.env" | cut -d= -f2)
        export GIT_SSH_KEY_FILE
    fi
fi

# Build binaries if needed (verify they exist AND can run)
if ! "$PROJECT_ROOT/bin/ag-agent-claude" -version >/dev/null 2>&1 || \
   ! "$PROJECT_ROOT/bin/ag-view-web" -version >/dev/null 2>&1; then
    echo "Building binaries..."
    cd "$PROJECT_ROOT" && ./build.sh build
fi

# Stop any existing instance
if [ -f "$PID_FILE" ]; then
    echo "Stopping existing instance..."
    "$SCRIPT_DIR/stop-agency.sh"
fi

# Start web view
echo "Starting web view on port $WEB_PORT..."
"$PROJECT_ROOT/bin/ag-view-web" -port "$WEB_PORT" -env "$PROJECT_ROOT/.env" -contexts "$PROJECT_ROOT/configs/contexts.yaml" > "$PID_DIR/view.log" 2>&1 &
VIEW_PID=$!

# Start claude agent
echo "Starting claude agent on port $AGENT_PORT..."
"$PROJECT_ROOT/bin/ag-agent-claude" -port "$AGENT_PORT" > "$PID_DIR/agent.log" 2>&1 &
AGENT_PID=$!

# Save PIDs
echo "$VIEW_PID" > "$PID_FILE"
echo "$AGENT_PID" >> "$PID_FILE"

# Wait for services to become ready via status API
wait_for_status() {
    local name="$1"
    local url="$2"
    local pid="$3"
    local max_attempts=30

    for i in $(seq 1 $max_attempts); do
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "ERROR: $name (PID $pid) died. Check logs."
            return 1
        fi
        if curl -sf -k "$url" > /dev/null 2>&1; then
            return 0
        fi
        sleep 0.1
    done
    echo "ERROR: $name not responding after ${max_attempts} attempts"
    return 1
}

echo -n "Waiting for agent..."
if ! wait_for_status "Claude agent" "http://localhost:$AGENT_PORT/status" "$AGENT_PID"; then
    echo " failed. Check $PID_DIR/agent.log"
    kill "$VIEW_PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
fi
echo " ready"

echo -n "Waiting for view..."
if ! wait_for_status "Web view" "https://localhost:$WEB_PORT/status" "$VIEW_PID"; then
    echo " failed. Check $PID_DIR/view.log"
    kill "$AGENT_PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
fi
echo " ready"

echo ""
echo "Agency started successfully!"
echo "  Web View PID: $VIEW_PID"
echo "  Claude Agent PID: $AGENT_PID"
echo ""

echo "Dashboard: https://localhost:$WEB_PORT/"
echo ""
echo "Logs:"
echo "  View:  $PID_DIR/view.log"
echo "  Agent: $PID_DIR/agent.log"
echo ""
echo "Stop with: $SCRIPT_DIR/stop-agency.sh"
