#!/bin/sh
# run_idle.sh — 1h idle budget sample (performance-budget.md §4).
# Appends status snapshots to a results file every 60s for the analyzer.
set -e
OUT="${1:-bench/results/idle-snap-$(date +%Y%m%d-%H%M).jsonl}"
DUR="${2:-3600}"
echo "sampling $DUR s -> $OUT"
END=$(( $(date +%s) + DUR ))
while [ "$(date +%s)" -lt "$END" ]; do
  MEMPILOT_SOCK="${MEMPILOT_SOCK:-$HOME/.local/share/mempilot/mempilot.sock}" \
    ./bin/mempilot status >> "$OUT" 2>>"$OUT.err" || true
  sleep 60
done
python3 bench/budget/analyze.py "$OUT" --mode idle
