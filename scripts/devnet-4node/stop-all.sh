#!/usr/bin/env bash
# Stop all 4 Chakram devnet nodes.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_ROOT="$SCRIPT_DIR/node-data"

echo "Stopping all nodes..."

for i in 0 1 2 3; do
  NODE_DIR="$DATA_ROOT/node$i"
  for proc in chakram geth; do
    PID_FILE="$NODE_DIR/${proc}.pid"
    if [[ -f "$PID_FILE" ]]; then
      kill "$(cat "$PID_FILE")" 2>/dev/null || true
      rm -f "$PID_FILE"
    fi
  done
done

# Fallback: kill by process name
pkill -f "geth.*11642" 2>/dev/null || true
pkill -f "chakram.*devnet-4" 2>/dev/null || true

echo "Done."
