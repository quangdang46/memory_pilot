#!/bin/sh
# start_run.sh <run-dir> <minutes>
# Starts every probe named in <run-dir>/labels.json, keeps THIS script alive as
# their parent (so ppid lineage survives and subprocess grouping is testable),
# and waits for them. Ctrl-C or `pkill -f start_run` stops everything.
set -e
RUN="$1"; MIN="${2:-40}"
mkdir -p "$RUN"
[ -f "$RUN/labels.json" ] || { echo "labels.json required: declare truth before the run"; exit 2; }
date -u +"%Y-%m-%dT%H:%M:%SZ" > "$RUN/started.txt"

python3 - "$RUN" "$MIN" <<'PY'
import json, os, subprocess, sys, time
run, minutes = sys.argv[1], sys.argv[2]
labels = json.load(open(os.path.join(run, "labels.json")))
here = os.path.dirname(os.path.abspath(__file__))
procs = []
for name, spec in labels.items():
    args = [os.path.join(here, name), "-mode", spec["mode"], "-min", minutes]
    args += ["-mb", str(spec.get("mb", 8))]
    if "grow_ticks" in spec:
        args += ["-grow-ticks", str(spec["grow_ticks"])]
    if "children" in spec:
        args += ["-children", str(spec["children"])]
    log = open(os.path.join(run, name + ".log"), "w")
    procs.append((name, subprocess.Popen(args, stdout=log, stderr=log)))
    print(f"started {name} mode={spec['mode']} pid={procs[-1][1].pid}")

print("probes started; parent pid is this process (lineage preserved)")

# Stay alive as the parent until the probes exit.
while any(p.poll() is None for _, p in procs):
    time.sleep(2)
print("all probes exited")
PY
