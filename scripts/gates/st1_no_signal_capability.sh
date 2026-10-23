#!/bin/sh
# ST-1 — No signaling capability in the binary (SI-1).
# Forbidden: sending signals / terminating / suspending other processes.
# Allowed: receiving own signals (signal.Notify for SIGTERM/SIGINT shutdown),
# stopping OWN children via the single audited call site (Phase 4).
set -e
viol=0
for pat in 'syscall\.Kill' 'os\.Process\.Signal' 'os\.Process\.Kill' 'SIGSTOP' 'SIGKILL' 'SIGSUSP' 'task_suspend' 'memorystatus_kill'; do
  hits=$(grep -rn --include='*.go' -E "$pat" cmd internal platform 2>/dev/null | grep -v '_test.go' || true)
  if [ -n "$hits" ]; then
    echo "ST-1 VIOLATION ($pat):"; echo "$hits"; viol=1
  fi
done
[ "$viol" = 0 ] && echo "ST-1 pass: no signaling capability" || exit 1
