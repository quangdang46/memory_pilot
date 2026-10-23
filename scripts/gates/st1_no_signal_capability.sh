#!/bin/sh
# ST-1 — No signaling capability except the ONE audited call site
# (internal/executor/stopowned_darwin.go, OwnedPid-gated, ADR-014 L2).
set -e
viol=0
for pat in 'syscall\.Kill' 'os\.Process\.Signal' 'os\.Process\.Kill' 'SIGSTOP' 'SIGKILL' 'task_suspend' 'memorystatus_kill'; do
  hits=$(grep -rn --include='*.go' -E "$pat" cmd internal platform 2>/dev/null | grep -v '_test.go' | grep -v 'internal/executor/stopowned_darwin.go' || true)
  if [ -n "$hits" ]; then
    echo "ST-1 VIOLATION ($pat):"; echo "$hits"; viol=1
  fi
done
# the whitelist must contain exactly one signal call site
n=$(grep -c 'syscall.Kill' internal/executor/stopowned_darwin.go || true)
[ "$n" = 1 ] || { echo "ST-1 VIOLATION: signal call sites in stopowned_darwin.go = $n (must be 1)"; viol=1; }
[ "$viol" = 0 ] && echo "ST-1 pass: single audited signal call site (stopOwnedChild)" || exit 1
