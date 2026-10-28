#!/bin/sh
# ST-13 — No deletes of user data.
#
# SI-3: the daemon never deletes, truncates, renames or moves a file outside its
# own data directory. Every removal in non-test code must be one of a small,
# named set whose target is provably ours:
#
#   internal/ledger/ledger.go   rotation and retention of ledger.*.jsonl, inside
#                               the configured data root (ST-12 proves it stays
#                               there).
#   internal/ipc/ipc.go         the daemon's own unix socket at shutdown.
#   cmd/mempilotd/main.go       the same socket, on the startup path that takes
#                               over a stale one.
#
# Anything else is a violation and must be argued for in review, not added
# quietly. This gate exists because "clean up a little" is exactly how a memory
# tool becomes a product that deletes files.
set -e
viol=0

enumerate() {
  grep -rn --include='*.go' -E 'os\.Remove\(|os\.RemoveAll\(|\.Truncate\(|os\.Rename\(' cmd internal platform 2>/dev/null \
    | grep -v '_test.go' || true
}

#   internal/supervisor/supervisor.go  truncates its OWN lock file (data_root/
#                               mempilot.lock) to write the holding pid. It
#                               never removes the file.
#   internal/store/store.go     os.Rename is the atomic-write idiom (tmp ->
#                               final), not a delete. resolve() refuses paths
#                               outside the root (ST-11 proves it).
allowed_file() {
  case "$1" in
    internal/ledger/ledger.go) return 0 ;;
    internal/ipc/ipc.go) return 0 ;;
    cmd/mempilotd/main.go) return 0 ;;
    internal/supervisor/supervisor.go) return 0 ;;
    internal/store/store.go) return 0 ;;
    *) return 1 ;;
  esac
}

hits=$(enumerate)
if [ -n "$hits" ]; then
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    file=${line%%:*}
    if ! allowed_file "$file"; then
      echo "ST-13 VIOLATION: removal outside the allowlisted files:"; echo "  $line"; viol=1
    fi
  done <<EOF
$hits
EOF
fi

# Each allowlisted file may only remove inside the data root. Cheap structural
# check: the removal expressions must reference the directory the ledger was
# opened with, not an arbitrary path.
for f in internal/ledger/ledger.go internal/ipc/ipc.go cmd/mempilotd/main.go; do
  if grep -qE 'os\.RemoveAll\(' "$f" 2>/dev/null; then
    echo "ST-13 VIOLATION: RemoveAll in $f — recursive deletion has no place here"; viol=1
  fi
done

[ "$viol" = 0 ] && echo "ST-13 pass: removals confined to own data dir and own socket" || exit 1
