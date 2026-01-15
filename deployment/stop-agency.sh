#!/bin/bash
# Stop agency: terminates web view, claude agent, and scheduler
#
# Usage: stop-agency.sh [dev|prod]
#   dev  - Stop development instance (default)
#   prod - Stop production instance

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Mode from argument (default: dev)
MODE="${1:-dev}"
PID_FILE="$SCRIPT_DIR/agency-${MODE}.pids"

if [ ! -f "$PID_FILE" ]; then
    echo "No agency-${MODE}.pids file found. Agency ($MODE) may not be running."
    exit 0
fi

echo "Stopping agency ($MODE)..."

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
