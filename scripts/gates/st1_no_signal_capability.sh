#!/bin/sh
# ST-1 — No signaling capability except the audited call sites.
#
# Two are allowlisted, and they are different binaries on purpose (ADR-015):
#
#   internal/executor/stopowned_darwin.go  the daemon's ONE signal, OwnedPid-
#                                          gated (ADR-014 L2). mempilotd still
#                                          cannot signal a foreign process.
#   cmd/mpguard/main.go                    the owner-invoked helper. Runs when a
#                                          human types the command; acts on the
#                                          list it just printed.
#
# The daemon residual — "mempilotd cannot signal what it did not spawn" — is
# what this gate must keep true. Widening it to any other file would silently
# delete that property, so the allowlist is by exact path and counted.
set -e
viol=0
ALLOW_EXECUTOR='internal/executor/stopowned_darwin.go'
ALLOW_GUARD='cmd/mpguard/main.go'
for pat in 'syscall\.Kill' 'os\.Process\.Signal' 'os\.Process\.Kill' 'SIGSTOP' 'SIGKILL' 'task_suspend' 'memorystatus_kill'; do
  hits=$(grep -rn --include='*.go' -E "$pat" cmd internal platform 2>/dev/null \
    | grep -v '_test.go' \
    | grep -v "$ALLOW_EXECUTOR" \
    | grep -v "$ALLOW_GUARD" || true)
  if [ -n "$hits" ]; then
    echo "ST-1 VIOLATION ($pat):"; echo "$hits"; viol=1
  fi
done
# Each allowlisted file must contain exactly one signal call site. More than
# one in the same file is as much a failure as one in a new file: it means the
# reviewed code is not the code that ships.
n=$(grep -c 'syscall.Kill' "$ALLOW_EXECUTOR" || true)
[ "$n" = 1 ] || { echo "ST-1 VIOLATION: signal call sites in $ALLOW_EXECUTOR = $n (must be 1)"; viol=1; }
n=$(grep -c 'syscall.Kill' "$ALLOW_GUARD" || true)
[ "$n" = 1 ] || { echo "ST-1 VIOLATION: signal call sites in $ALLOW_GUARD = $n (must be 1)"; viol=1; }

# The daemon must not import the helper, or the split is fiction: a daemon that
# links mpguard could reach the signal through it.
deps=$(go list -deps ./cmd/mempilotd 2>/dev/null | grep -c 'memory_pilot/cmd/mpguard' || true)
[ "$deps" = 0 ] || { echo "ST-1 VIOLATION: mempilotd depends on cmd/mpguard"; viol=1; }

[ "$viol" = 0 ] && echo "ST-1 pass: 2 audited signal call sites, daemon cannot reach the helper" || exit 1
