#!/bin/bash
# Stop agency: terminates web view, claude agent, and scheduler
#
# Usage: stop-agency.sh [dev|prod]
#   dev  - Stop development instance (default)
#   prod - Stop production instance
#
# Shutdown method:
#   1. Tries POST /shutdown on web director (cascades to all services)
#   2. Falls back to PID-based termination if API unavailable

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Load port configuration
if [ -f "$SCRIPT_DIR/ports.conf" ]; then
    source "$SCRIPT_DIR/ports.conf"
fi

# Mode from argument (default: dev)
MODE="${1:-dev}"
set_agency_env "$MODE"

PID_FILE="$SCRIPT_DIR/agency-${MODE}.pids"

# Get internal port for shutdown API
INTERNAL_PORT="${AG_WEB_INTERNAL_PORT:-8080}"

echo "Stopping agency ($MODE)..."

# Try API-based shutdown first (more reliable than PID tracking)
if curl -sf -X POST "http://localhost:${INTERNAL_PORT}/shutdown" > /dev/null 2>&1; then
    echo "  Shutdown initiated via API"
    # Wait for processes to terminate
    sleep 2
    # Clean up PID file if it exists
    rm -f "$PID_FILE"
    echo "Agency ($MODE) stopped."
    exit 0
fi

echo "  API shutdown unavailable, falling back to PID-based shutdown..."

# Fallback: PID-based shutdown
if [ ! -f "$PID_FILE" ]; then
    echo "No agency-${MODE}.pids file found. Agency ($MODE) may not be running."
    exit 0
fi

# Read PIDs and terminate
while read -r PID; do
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        echo "  Terminating PID $PID..."
        kill "$PID" 2>/dev/null || true
    fi
done < "$PID_FILE"

# Wait briefly for graceful shutdown
sleep 1

# Force kill if still running
while read -r PID; do
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
        echo "  Force killing PID $PID..."
        kill -9 "$PID" 2>/dev/null || true
    fi
done < "$PID_FILE"

# Clean up
rm -f "$PID_FILE"

echo "Agency ($MODE) stopped."
