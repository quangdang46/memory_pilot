#!/bin/sh
# ST-2 — Sealed action type: the allowlist lives only in model.ActionKind.
set -e
viol=0
# any ActionKind value defined outside internal/model is a violation
hits=$(grep -rn --include='*.go' -E 'ActionKind\("[^"]+"' cmd internal platform | grep -v internal/model | grep -v '_test.go' || true)
[ -n "$hits" ] && { echo "ST-2 VIOLATION: action kinds constructed outside model:"; echo "$hits"; viol=1; }
[ "$viol" = 0 ] && echo "ST-2 pass: action type sealed" || exit 1
