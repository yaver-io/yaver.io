#!/usr/bin/env bash
# Factory/DIY helper run while this station is joined to the AtomS3U setup AP.
# Wi-Fi credentials are read from an owner-only JSON file and the returned
# 256-bit pairing credential is written directly to another owner-only file.
set -euo pipefail

usage() {
  echo "usage: $0 --wifi-credentials <owner-only-json> --token-output <new-file> [--url http://192.168.4.1:8348]" >&2
  exit 2
}

WIFI_CREDENTIALS=""
TOKEN_OUTPUT=""
SETUP_URL="http://192.168.4.1:8348"
while [ $# -gt 0 ]; do
  case "$1" in
    --wifi-credentials) [ $# -ge 2 ] || usage; WIFI_CREDENTIALS="$2"; shift 2 ;;
    --token-output) [ $# -ge 2 ] || usage; TOKEN_OUTPUT="$2"; shift 2 ;;
    --url) [ $# -ge 2 ] || usage; SETUP_URL="$2"; shift 2 ;;
    *) usage ;;
  esac
done
[ -n "$WIFI_CREDENTIALS" ] && [ -n "$TOKEN_OUTPUT" ] || usage
[ -f "$WIFI_CREDENTIALS" ] || { echo "Wi-Fi credential file not found" >&2; exit 1; }
[ ! -e "$TOKEN_OUTPUT" ] || { echo "token output already exists; refusing to overwrite it" >&2; exit 1; }
case "$SETUP_URL" in
  http://192.168.4.1:8348|http://192.168.4.1:8348/) ;;
  *) echo "setup URL must be the AtomS3U local setup address" >&2; exit 2 ;;
esac

command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
command -v python3 >/dev/null || { echo "python3 is required" >&2; exit 1; }
python3 - "$WIFI_CREDENTIALS" <<'PY'
import os
import stat
import sys

mode = stat.S_IMODE(os.stat(sys.argv[1]).st_mode)
if mode & 0o077:
    raise SystemExit("Wi-Fi credential file must be owner-only (chmod 600)")
PY

OUTPUT_DIR="$(dirname "$TOKEN_OUTPUT")"
mkdir -p "$OUTPUT_DIR"
TMP_RESPONSE="$(mktemp "$OUTPUT_DIR/.m5-commission.XXXXXX")"
cleanup() {
  rm -f -- "$TMP_RESPONSE"
}
trap cleanup EXIT
chmod 0600 "$TMP_RESPONSE"

echo "Press the AtomS3U button now; commissioning requires its 60-second physical window."
curl --fail-with-body --silent --show-error \
  --connect-timeout 4 --max-time 12 \
  -H 'Content-Type: application/json' \
  --data-binary "@$WIFI_CREDENTIALS" \
  "${SETUP_URL%/}/v1/commission" >"$TMP_RESPONSE"

python3 - "$TMP_RESPONSE" "$TOKEN_OUTPUT" <<'PY'
import json
import os
import sys

source, destination = sys.argv[1:]
with open(source, "r", encoding="utf-8") as handle:
    reply = json.load(handle)
token = reply.get("token", "")
if not isinstance(token, str) or len(token) != 64:
    raise SystemExit("AtomS3U did not return a 32-byte pairing credential")
try:
    if len(bytes.fromhex(token)) != 32:
        raise ValueError
except ValueError:
    raise SystemExit("AtomS3U returned an invalid pairing credential")
fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, "w", encoding="ascii") as handle:
    handle.write(token + "\n")
PY

chmod 0600 "$TOKEN_OUTPUT"
echo "Commissioning accepted. The pairing credential was saved to $TOKEN_OUTPUT and was not printed."
echo "Wait for AtomS3U to join its assigned Wi-Fi, press its button, then run:"
echo "  yaver kvm pair --auto --token-file $TOKEN_OUTPUT"
