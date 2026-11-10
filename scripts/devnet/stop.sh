#!/usr/bin/env bash
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DATA_DIR="$SCRIPT_DIR/node-data"

echo "Stopping devnet..."

if [[ -f "$DATA_DIR/chakram.pid" ]]; then
  kill "$(cat "$DATA_DIR/chakram.pid")" 2>/dev/null || true
  rm -f "$DATA_DIR/chakram.pid"
  echo "chakram stopped"
fi

if [[ -f "$DATA_DIR/geth.pid" ]]; then
  kill "$(cat "$DATA_DIR/geth.pid")" 2>/dev/null || true
  rm -f "$DATA_DIR/geth.pid"
  echo "geth stopped"
fi

# Fallback: kill by process pattern
pkill -f "geth.*11642" 2>/dev/null || true
pkill -f "chakram.*chakram-devnet-1" 2>/dev/null || true

echo "Done."
