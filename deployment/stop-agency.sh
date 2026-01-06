#!/bin/bash
# Stop agency: terminates web director and claude agent

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PID_FILE="$SCRIPT_DIR/agency.pids"

if [ ! -f "$PID_FILE" ]; then
    echo "No agency.pids file found. Agency may not be running."
    exit 0
fi

echo "Stopping agency..."

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

echo "Agency stopped."
