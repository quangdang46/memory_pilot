#!/bin/sh
# run_loaded.sh — 8h loaded-budget sample (performance-budget.md §4).
# Runs the standard synthetic workload (per stress-and-soak.md) while sampling
# daemon status every 60s. Workload: churning subprocess spawns + cache-growth
# allocators + a normal desktop-ish file read loop. Budget: RSS<=200MB,
# CPU<=3% avg (MP-REQ-071).
set -e
OUT="${1:-bench/results/loaded-snap-$(date +%Y%m%d-%H%M).jsonl}"
DUR="${2:-28800}"           # 8h default
CACHE_MB="${CACHE_MB:-256}" # cache-growth workload size
cd "$(dirname "$0")/../.."

echo "loaded run: $DUR s, cache workload ${CACHE_MB}MB -> $OUT"

# Workload A: cache-growth allocator (grows then plateaus, cache-like)
python3 - "$CACHE_MB" <<'PYWA' &
import sys, time
target_mb = int(sys.argv[1]); cache = []
grow = True
end = time.time() + float(sys.argv[2]) if len(sys.argv) > 2 else time.time() + 28800
while time.time() < end:
    if grow:
        cache.append(bytearray(4 * 1024 * 1024))  # 4MB
        if len(cache) * 4 >= target_mb: grow = False
    else:
        # plateau with occasional churn (sawtooth, benign)
        if cache and int(time.time()) % 60 < 2: cache.pop(0); cache.append(bytearray(4*1024*1024))
    time.sleep(1)
PYWA
WA=$!

# Workload B: subprocess spawn/exit churn (build-tool-like)
( END=$(( $(date +%s) + DUR )); N=0
  while [ "$(date +%s)" -lt "$END" ]; do
    /bin/sh -c 'i=0; while [ $i -lt 200 ]; do i=$((i+1)); done' &
    N=$((N+1)); [ $((N % 20)) -eq 0 ] && wait
    sleep 2
  done ) &
WB=$!

# Sampler
END=$(( $(date +%s) + DUR ))
while [ "$(date +%s)" -lt "$END" ]; do
  MEMPILOT_SOCK="${MEMPILOT_SOCK:-$HOME/.local/share/mempilot/mempilot.sock}" \
    ./bin/mempilot status >> "$OUT" 2>>"$OUT.err" || true
  sleep 60
done

kill $WA $WB 2>/dev/null || true
python3 bench/budget/analyze.py "$OUT" --mode loaded
