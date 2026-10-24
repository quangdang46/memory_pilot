#!/bin/sh
# run_pressure.sh — 30min induced-pressure run (performance-budget.md §4).
# Controlled allocator ramps +6..8GB (hard-capped, releases at end) while the
# daemon samples; verifies SI-6 degrade-before-host behavior and that no signal
# is sent to any non-owned pid (ST-22 discipline). Analyzer: idle budgets still
# apply (daemon must stay small even when the HOST is under pressure).
set -e
OUT="${1:-bench/results/pressure-snap-$(date +%Y%m%d-%H%M).jsonl}"
DUR="${2:-1800}"        # 30min default
MAX_GB="${MAX_GB:-8}"   # hard cap — never exceed
cd "$(dirname "$0")/../.."

echo "pressure run: $DUR s, ramp cap ${MAX_GB}GB -> $OUT"

# Controlled allocator: 512MB touched chunks, stop at cap, release at end.
python3 - "$MAX_GB" "$DUR" <<'PYPA' &
import sys, time
cap_gb = int(sys.argv[1]); dur = int(sys.argv[2])
keep = []
end = time.time() + dur
try:
    while time.time() < end:
        if len(keep) < cap_gb * 2:
            b = bytearray(512 * 1024 * 1024)
            for j in range(0, len(b), 4096): b[j] = 1  # touch every page
            keep.append(b)
        time.sleep(5)  # ~1GB/10s ramp
finally:
    keep = None  # release
PYPA
PA=$!

END=$(( $(date +%s) + DUR ))
while [ "$(date +%s)" -lt "$END" ]; do
  MEMPILOT_SOCK="${MEMPILOT_SOCK:-$HOME/.local/share/mempilot/mempilot.sock}" \
    ./bin/mempilot status >> "$OUT" 2>>"$OUT.err" || true
  # record kernel pressure level alongside (honest evidence of induced state)
  sysctl -n kern.memorystatus_vm_pressure_level >> "$OUT.pressure" || true
  sleep 30
done

kill $PA 2>/dev/null || true; wait $PA 2>/dev/null || true
python3 bench/budget/analyze.py "$OUT" --mode idle
echo "pressure level trace: $OUT.pressure"
