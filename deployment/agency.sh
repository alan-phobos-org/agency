#!/bin/bash
# Start agency: web view + claude agent + scheduler (optional)
# Spawns all as background processes and reports the dashboard URL
#
# Usage: agency.sh [dev|prod]
#   dev  - Use development ports (default)
#   prod - Use production ports

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PID_DIR="$PROJECT_ROOT/deployment"

# Load port configuration
if [ -f "$SCRIPT_DIR/ports.conf" ]; then
    source "$SCRIPT_DIR/ports.conf"
fi

# Set mode from argument (default: dev)
MODE="${1:-dev}"
set_agency_env "$MODE"

# PID file is mode-specific to allow running dev and prod simultaneously
PID_FILE="$PID_DIR/agency-${MODE}.pids"

# Ports (set by set_agency_env, with fallback defaults)
WEB_PORT="${AG_WEB_PORT:-8443}"
WEB_INTERNAL_PORT="${AG_WEB_INTERNAL_PORT:-8080}"
AGENT_PORT="${AG_AGENT_PORT:-9000}"
AGENT_CODEX_PORT="${AG_AGENT_CODEX_PORT:-9001}"
SCHEDULER_PORT="${AG_SCHEDULER_PORT:-9010}"
DISCOVERY_START="${AG_DISCOVERY_START:-9000}"
DISCOVERY_END="${AG_DISCOVERY_END:-9010}"

# Optional scheduler config (set to empty to disable)
SCHEDULER_CONFIG="${AG_SCHEDULER_CONFIG:-$PROJECT_ROOT/configs/scheduler.yaml}"

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
   ! "$PROJECT_ROOT/bin/ag-agent-codex" -version >/dev/null 2>&1 || \
   ! "$PROJECT_ROOT/bin/ag-view-web" -version >/dev/null 2>&1 || \
   ! "$PROJECT_ROOT/bin/ag-scheduler" -version >/dev/null 2>&1; then
    echo "Building binaries..."
    cd "$PROJECT_ROOT" && ./build.sh build
fi

# Check if agency is already running (via API, more reliable than PID file)
if curl -sf "https://localhost:${WEB_INTERNAL_PORT}/status" > /dev/null 2>&1; then
    echo "Agency is already running on port $WEB_INTERNAL_PORT."
    # Check if running interactively
    if [ -t 0 ]; then
        read -p "Shut it down via /shutdown? [Y/n] " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Nn]$ ]]; then
            echo "Shutting down existing agency..."
            if curl -sf -X POST "https://localhost:${WEB_INTERNAL_PORT}/shutdown" > /dev/null 2>&1; then
                echo "  Shutdown initiated, waiting for services to stop..."
                sleep 2
                rm -f "$PID_FILE"
            else
                echo "  Shutdown request failed. Try stop-agency.sh manually."
                exit 1
            fi
        else
            echo "Aborting. Use stop-agency.sh to shut down the existing instance."
            exit 0
        fi
    else
        # Non-interactive: just shut it down
        echo "Shutting down existing agency..."
        if curl -sf -X POST "https://localhost:${WEB_INTERNAL_PORT}/shutdown" > /dev/null 2>&1; then
            sleep 2
            rm -f "$PID_FILE"
        else
            echo "  Shutdown request failed."
            exit 1
        fi
    fi
elif [ -f "$PID_FILE" ]; then
    # PID file exists but API not responding - clean up stale PIDs
    echo "Cleaning up stale PID file..."
    "$SCRIPT_DIR/stop-agency.sh" "$MODE"
fi

# Start web view (with internal API port for scheduler/CLI routing)
echo "Starting web view on port $WEB_PORT (internal: $WEB_INTERNAL_PORT, discovery: $DISCOVERY_START-$DISCOVERY_END)..."
"$PROJECT_ROOT/bin/ag-view-web" -port "$WEB_PORT" -internal-port "$WEB_INTERNAL_PORT" -port-start "$DISCOVERY_START" -port-end "$DISCOVERY_END" -env "$PROJECT_ROOT/.env" -contexts "$PROJECT_ROOT/configs/contexts.yaml" > "$PID_DIR/view-${MODE}.log" 2>&1 &
VIEW_PID=$!

# Start claude agent
echo "Starting claude agent on port $AGENT_PORT..."
"$PROJECT_ROOT/bin/ag-agent-claude" -port "$AGENT_PORT" > "$PID_DIR/agent-${MODE}.log" 2>&1 &
AGENT_PID=$!

# Start codex agent
echo "Starting codex agent on port $AGENT_CODEX_PORT..."
"$PROJECT_ROOT/bin/ag-agent-codex" -port "$AGENT_CODEX_PORT" > "$PID_DIR/agent-codex-${MODE}.log" 2>&1 &
AGENT_CODEX_PID=$!

# Start scheduler (optional)
SCHEDULER_PID=""
if [ -n "$SCHEDULER_CONFIG" ] && [ -f "$SCHEDULER_CONFIG" ]; then
    echo "Starting scheduler on port $SCHEDULER_PORT..."
    "$PROJECT_ROOT/bin/ag-scheduler" -config "$SCHEDULER_CONFIG" -port "$SCHEDULER_PORT" > "$PID_DIR/scheduler-${MODE}.log" 2>&1 &
    SCHEDULER_PID=$!
fi

# Save PIDs
echo "$VIEW_PID" > "$PID_FILE"
echo "$AGENT_PID" >> "$PID_FILE"
echo "$AGENT_CODEX_PID" >> "$PID_FILE"
if [ -n "$SCHEDULER_PID" ]; then
    echo "$SCHEDULER_PID" >> "$PID_FILE"
fi

# Show last N lines of a log file with header
show_log_tail() {
    local log_file="$1"
    local lines="${2:-20}"
    if [ -f "$log_file" ] && [ -s "$log_file" ]; then
        echo ""
        echo "=== Last $lines lines of $log_file ==="
        tail -n "$lines" "$log_file"
        echo "=== End of log ==="
    elif [ -f "$log_file" ]; then
        echo ""
        echo "(Log file $log_file is empty)"
    fi
}

# Wait for services to become ready via status API
wait_for_status() {
    local name="$1"
    local url="$2"
    local pid="$3"
    local log_file="$4"
    local max_attempts=30

    for i in $(seq 1 $max_attempts); do
        if ! kill -0 "$pid" 2>/dev/null; then
            echo ""
            echo "ERROR: $name (PID $pid) exited unexpectedly"
            show_log_tail "$log_file" 30
            return 1
        fi
        if curl -sf -k "$url" > /dev/null 2>&1; then
            return 0
        fi
        sleep 0.1
    done
    echo ""
    echo "ERROR: $name not responding after ${max_attempts} attempts (3 seconds)"
    echo "Process is running (PID $pid) but not accepting connections at $url"
    show_log_tail "$log_file" 30
    return 1
}

AGENT_LOG="$PID_DIR/agent-${MODE}.log"
AGENT_CODEX_LOG="$PID_DIR/agent-codex-${MODE}.log"
VIEW_LOG="$PID_DIR/view-${MODE}.log"
SCHEDULER_LOG="$PID_DIR/scheduler-${MODE}.log"

echo -n "Waiting for claude agent..."
if ! wait_for_status "Claude agent" "https://localhost:$AGENT_PORT/status" "$AGENT_PID" "$AGENT_LOG"; then
    kill "$VIEW_PID" 2>/dev/null || true
    kill "$AGENT_CODEX_PID" 2>/dev/null || true
    [ -n "$SCHEDULER_PID" ] && kill "$SCHEDULER_PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
fi
echo " ready"

echo -n "Waiting for codex agent..."
if ! wait_for_status "Codex agent" "https://localhost:$AGENT_CODEX_PORT/status" "$AGENT_CODEX_PID" "$AGENT_CODEX_LOG"; then
    kill "$VIEW_PID" 2>/dev/null || true
    kill "$AGENT_PID" 2>/dev/null || true
    [ -n "$SCHEDULER_PID" ] && kill "$SCHEDULER_PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
fi
echo " ready"

echo -n "Waiting for view..."
if ! wait_for_status "Web view" "https://localhost:$WEB_PORT/status" "$VIEW_PID" "$VIEW_LOG"; then
    kill "$AGENT_PID" 2>/dev/null || true
    kill "$AGENT_CODEX_PID" 2>/dev/null || true
    [ -n "$SCHEDULER_PID" ] && kill "$SCHEDULER_PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
fi
echo " ready"

if [ -n "$SCHEDULER_PID" ]; then
    echo -n "Waiting for scheduler..."
    if ! wait_for_status "Scheduler" "https://localhost:$SCHEDULER_PORT/status" "$SCHEDULER_PID" "$SCHEDULER_LOG"; then
        kill "$VIEW_PID" 2>/dev/null || true
        kill "$AGENT_PID" 2>/dev/null || true
        kill "$AGENT_CODEX_PID" 2>/dev/null || true
        rm -f "$PID_FILE"
        exit 1
    fi
    echo " ready"
fi

echo ""
echo "Agency started successfully! (mode: $MODE)"
echo "  Web View PID: $VIEW_PID (HTTPS: $WEB_PORT, Internal: $WEB_INTERNAL_PORT)"
echo "  Claude Agent PID: $AGENT_PID (port: $AGENT_PORT)"
echo "  Codex Agent PID: $AGENT_CODEX_PID (port: $AGENT_CODEX_PORT)"
if [ -n "$SCHEDULER_PID" ]; then
    echo "  Scheduler PID: $SCHEDULER_PID (port: $SCHEDULER_PORT)"
fi
echo "  Discovery range: $DISCOVERY_START-$DISCOVERY_END"
echo ""

echo "Dashboard: https://localhost:$WEB_PORT/"
echo "Internal API: https://localhost:$WEB_INTERNAL_PORT/ (scheduler/CLI routing)"
echo ""
echo "Logs:"
echo "  View:          $PID_DIR/view-${MODE}.log"
echo "  Claude Agent:  $PID_DIR/agent-${MODE}.log"
echo "  Codex Agent:   $PID_DIR/agent-codex-${MODE}.log"
if [ -n "$SCHEDULER_PID" ]; then
    echo "  Scheduler:     $PID_DIR/scheduler-${MODE}.log"
fi
echo ""
echo "Stop with: $SCRIPT_DIR/stop-agency.sh $MODE"
