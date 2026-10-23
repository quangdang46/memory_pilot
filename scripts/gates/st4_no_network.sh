#!/bin/sh
# ST-4 — No network. Unix sockets for local IPC are allowed; TCP/UDP/DNS/HTTP
# are not (SI boundary, MP-REQ security).
set -e
viol=0
for pat in 'net/http' 'net\.Dial("tcp' 'net\.Dial("udp' 'net\.Listen("tcp' 'ListenPacket'; do
  hits=$(grep -rn --include='*.go' -F "$(echo $pat | sed 's/\\\\//g')" cmd internal platform 2>/dev/null || true)
  case "$pat" in
    'net/http') hits=$(grep -rn --include='*.go' 'net/http' cmd internal platform 2>/dev/null || true);;
    'net\.Dial("tcp') hits=$(grep -rn --include='*.go' 'Dial("tcp' cmd internal platform 2>/dev/null || true);;
    'net\.Dial("udp') hits=$(grep -rn --include='*.go' 'Dial("udp' cmd internal platform 2>/dev/null || true);;
    'net\.Listen("tcp') hits=$(grep -rn --include='*.go' 'Listen("tcp' cmd internal platform 2>/dev/null || true);;
    'ListenPacket') hits=$(grep -rn --include='*.go' 'ListenPacket' cmd internal platform 2>/dev/null || true);;
  esac
  [ -n "$hits" ] && { echo "ST-4 VIOLATION ($pat):"; echo "$hits"; viol=1; }
done
[ "$viol" = 0 ] && echo "ST-4 pass: no network capability" || exit 1
