#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.appstoreconnect/yaver.env"; set +a
fi

for candidate in /opt/homebrew/bin/python3 /usr/local/bin/python3 python3; do
  if command -v "$candidate" >/dev/null 2>&1 \
    && "$candidate" -c 'import jwt, requests, cryptography' >/dev/null 2>&1; then
    exec "$candidate" "$ROOT/scripts/provision-apple-signing.py" "$@"
  fi
done

echo "ERROR: Apple signing provisioning needs Python with PyJWT, requests, and cryptography." >&2
echo "       Install them for one local Python interpreter, then retry." >&2
exit 1
