#!/bin/sh
# Verify the warning board actually speaks at Warn pressure.
#
# Run this in a terminal while `sudo memory_pressure -S -l warn` is holding the
# system at Warn in another. It watches the live ledger for the two records that
# together prove the board worked end to end:
#
#   relief_recommendation   what the daemon decided, with the numbers
#   advisory_notification   that it was handed to the OS, with the outcome
#
# It does not create pressure itself. Simulating pressure needs root, and
# simulating it from a script the owner did not ask to run would be the script
# deciding to scare them.
set -e
DATA=${MEMPILOT_DATA:-$HOME/.local/share/mempilot}
SECS=${1:-420}

echo "watching $DATA for up to ${SECS}s"
echo "the board speaks when: pressure >= Warn AND there is something releasable"
echo

# Line count at start, so only new records are considered.
start=$(cat "$DATA"/ledger.*.jsonl 2>/dev/null | wc -l | tr -d ' ')
deadline=$(( $(date +%s) + SECS ))

while [ "$(date +%s)" -lt "$deadline" ]; do
  lvl=$(sysctl -n kern.memorystatus_vm_pressure_level 2>/dev/null || echo "?")
  new=$(cat "$DATA"/ledger.*.jsonl 2>/dev/null | tail -n +$((start + 1)))

  rec=$(printf '%s' "$new" | grep '"kind":"relief_recommendation"' | head -1 || true)
  ntf=$(printf '%s' "$new" | grep '"kind":"advisory_notification"' | head -1 || true)

  printf '\rpressure=%s  recommendation=%s  notification=%s   ' \
    "$lvl" "${rec:+YES}" "${ntf:+YES}"

  if [ -n "$rec" ] && [ -n "$ntf" ]; then
    echo
    echo
    echo "=== PROVEN: the board spoke at pressure ==="
    echo
    echo "-- what it decided:"
    printf '%s\n' "$rec" | sed 's/\\\\n/\n/g' | cut -c1-400
    echo
    echo "-- delivery:"
    printf '%s\n' "$ntf" | cut -c1-300
    echo
    echo "Check the screen: a notification titled 'Memory Pilot — memory pressure'"
    echo "should have appeared. That banner is the half a log cannot prove."
    exit 0
  fi
  sleep 5
done

echo
echo "=== NOT PROVEN in ${SECS}s ==="
[ -z "$rec" ] && echo "  no recommendation was emitted — pressure may not have reached Warn,"
[ -z "$rec" ] && echo "  or nothing met the bar for being safe to release"
[ -n "$rec" ] && [ -z "$ntf" ] && echo "  a recommendation WAS emitted but never delivered — this is a bug"
exit 1
