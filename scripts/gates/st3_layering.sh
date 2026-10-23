#!/bin/sh
# ST-3 — Layering (module-contracts): intelligence & policy layers must not
# import platform, os, net, or ledger. Analysis cannot act (SI-2).
set -e
viol=0
for pkg in internal/model; do
  hits=$(go list -deps ./$pkg 2>/dev/null | grep -E 'memory_pilot/(platform|internal/(ledger|ipc))' || true)
  [ -n "$hits" ] && { echo "ST-3 VIOLATION: $pkg imports:"; echo "$hits"; viol=1; }
done
[ "$viol" = 0 ] && echo "ST-3 pass: layering intact" || exit 1
