#!/bin/bash
# Pulls the Android upload keystore + signing config from the Yaver vault
# and materializes them on disk so `./gradlew bundleRelease` (and the
# deploy-playstore.sh wrapper) can sign locally.
#
# Run once per fresh machine after `yaver auth`. Idempotent.
#
# Vault entries it expects under project=mobile:
#   ANDROID_KEYSTORE_BASE64  (or ANDROID_KEYSTORE) — base64 of the
#                                                    upload keystore
#   ANDROID_KEYSTORE_PASSWORD
#   ANDROID_KEY_ALIAS
#   ANDROID_KEY_PASSWORD
#
# ANDROID_KEYSTORE is the historical GitHub-Actions secret name
# (kept as-is in some vault syncs); ANDROID_KEYSTORE_BASE64 is the
# explicit name some scripts adopted later. Either works.
#
# Materializes:
#   keys/yaver-upload.keystore       (gitignored)
#   mobile/android/keystore.properties (gitignored)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

# A clean release checkout may intentionally reuse owner-only signing files
# from another trusted local checkout. Accept explicit paths before touching
# the vault, and link rather than duplicate private material. All destinations
# remain gitignored and the deploy script still validates the real keystore.
if [ -n "${YAVER_ANDROID_KEYSTORE_PATH:-}" ] ||
   [ -n "${YAVER_ANDROID_KEYSTORE_PROPERTIES_PATH:-}" ] ||
   [ -n "${YAVER_PLAY_STORE_KEY_FILE:-}" ]; then
  : "${YAVER_ANDROID_KEYSTORE_PATH:?Set all three explicit Android signing paths}"
  : "${YAVER_ANDROID_KEYSTORE_PROPERTIES_PATH:?Set all three explicit Android signing paths}"
  : "${YAVER_PLAY_STORE_KEY_FILE:?Set all three explicit Android signing paths}"
  for source_file in "$YAVER_ANDROID_KEYSTORE_PATH" "$YAVER_ANDROID_KEYSTORE_PROPERTIES_PATH" "$YAVER_PLAY_STORE_KEY_FILE"; do
    if [ ! -r "$source_file" ]; then
      echo "ERROR: explicit Android signing source is not readable: $source_file" >&2
      exit 2
    fi
  done
  mkdir -p keys mobile/android
  ln -s "$YAVER_ANDROID_KEYSTORE_PATH" keys/yaver-upload.keystore
  ln -s "$YAVER_ANDROID_KEYSTORE_PROPERTIES_PATH" mobile/android/keystore.properties
  ln -s "$YAVER_PLAY_STORE_KEY_FILE" keys/google-play-service-account.json
  echo "Linked explicit owner-only Android signing sources into this clean checkout."
  exit 0
fi

if ! command -v yaver >/dev/null 2>&1; then
  echo "ERROR: yaver CLI not on PATH. Install with: npm install -g yaver-cli" >&2
  exit 1
fi

# Vault-backed deploy cells are explicitly opt-in in current CLI builds. This
# bootstrap is one of those cells, so every call must carry the flag; otherwise
# it reports every existing entry as missing. Peer sync is advisory and must be
# wall-clock bounded: on 2026-08-18 seven offline peers held Android deploy for
# minutes even though the script promised to fall back to the local vault.
vault_get() {
  YAVER_ENABLE_VAULT=1 yaver vault get "$@"
}

sync_vault_bounded() {
  local timeout_seconds="${YAVER_VAULT_SYNC_TIMEOUT_SECONDS:-45}"
  local sync_log sync_pid elapsed rc
  sync_log="$(mktemp /tmp/yaver-android-vault-sync.XXXXXX)"
  YAVER_ENABLE_VAULT=1 yaver vault sync >"$sync_log" 2>&1 &
  sync_pid=$!
  elapsed=0
  while kill -0 "$sync_pid" 2>/dev/null && [ "$elapsed" -lt "$timeout_seconds" ]; do
    sleep 1
    elapsed=$((elapsed + 1))
  done
  if kill -0 "$sync_pid" 2>/dev/null; then
    kill "$sync_pid" 2>/dev/null || true
    wait "$sync_pid" 2>/dev/null || true
    sed 's/^/  /' "$sync_log"
    echo "  (vault peer sync timed out after ${timeout_seconds}s — using the local vault)"
    find "$sync_log" -delete
    return 124
  fi
  if wait "$sync_pid"; then rc=0; else rc=$?; fi
  sed 's/^/  /' "$sync_log"
  find "$sync_log" -delete
  return "$rc"
}

echo "Syncing vault from peers (bounded)..."
sync_vault_bounded || {
  echo "  (sync failed or no peers online — using whatever's in local vault)"
}

require_entry() {
  local name="$1"
  vault_get "$name" --project mobile >/dev/null 2>&1 || {
    echo "ERROR: vault entry mobile/$name missing." >&2
    echo "  Add it on a machine that has it, then 'yaver vault sync' from this one." >&2
    exit 2
  }
}

# Accept either ANDROID_KEYSTORE_BASE64 (explicit) or ANDROID_KEYSTORE
# (matches the GitHub-Actions secret name). Pick whichever exists.
KEYSTORE_ENTRY="ANDROID_KEYSTORE_BASE64"
if ! vault_get ANDROID_KEYSTORE_BASE64 --project mobile >/dev/null 2>&1; then
  if vault_get ANDROID_KEYSTORE --project mobile >/dev/null 2>&1; then
    KEYSTORE_ENTRY="ANDROID_KEYSTORE"
  else
    echo "ERROR: neither mobile/ANDROID_KEYSTORE_BASE64 nor mobile/ANDROID_KEYSTORE is in the vault." >&2
    echo "  Add it on a machine that has it, then 'yaver vault sync' from this one." >&2
    exit 2
  fi
fi
require_entry ANDROID_KEYSTORE_PASSWORD
require_entry ANDROID_KEY_ALIAS
require_entry ANDROID_KEY_PASSWORD

mkdir -p keys
KEYSTORE_PATH="keys/yaver-upload.keystore"
PROPERTIES_PATH="mobile/android/keystore.properties"

echo "Materializing $KEYSTORE_PATH (from $KEYSTORE_ENTRY) ..."
vault_get "$KEYSTORE_ENTRY" --project mobile | base64 -d > "$KEYSTORE_PATH"
chmod 600 "$KEYSTORE_PATH"

# Sanity check: keytool can read it
if command -v keytool >/dev/null 2>&1; then
  STORE_PW="$(vault_get ANDROID_KEYSTORE_PASSWORD --project mobile)"
  if ! keytool -list -keystore "$KEYSTORE_PATH" -storepass "$STORE_PW" >/dev/null 2>&1; then
    echo "ERROR: keystore decoded but keytool can't open it (bad password or corrupt base64)." >&2
    exit 3
  fi
fi

echo "Materializing $PROPERTIES_PATH ..."
mkdir -p "$(dirname "$PROPERTIES_PATH")"
{
  echo "storeFile=../../../keys/yaver-upload.keystore"
  echo "storePassword=$(vault_get ANDROID_KEYSTORE_PASSWORD --project mobile)"
  echo "keyAlias=$(vault_get ANDROID_KEY_ALIAS --project mobile)"
  echo "keyPassword=$(vault_get ANDROID_KEY_PASSWORD --project mobile)"
} > "$PROPERTIES_PATH"
chmod 600 "$PROPERTIES_PATH"

# Materialize the Play Store service-account JSON file the upload
# script needs. Vault carries the JSON content under
# PLAY_STORE_SERVICE_ACCOUNT_JSON; the upload script's PLAY_STORE_KEY_FILE
# env var must be a path to a JSON file on disk.
PLAY_KEY_PATH="keys/google-play-service-account.json"
if vault_get PLAY_STORE_SERVICE_ACCOUNT_JSON --project mobile >/dev/null 2>&1; then
  echo "Materializing $PLAY_KEY_PATH ..."
  vault_get PLAY_STORE_SERVICE_ACCOUNT_JSON --project mobile > "$PLAY_KEY_PATH"
  chmod 600 "$PLAY_KEY_PATH"
  if ! python3 -c "import json,sys; json.load(open(sys.argv[1]))" "$PLAY_KEY_PATH" >/dev/null 2>&1; then
    echo "ERROR: $PLAY_KEY_PATH is not valid JSON — vault entry corrupted?" >&2
    exit 4
  fi
else
  echo "(skipping $PLAY_KEY_PATH — PLAY_STORE_SERVICE_ACCOUNT_JSON not in vault)"
fi

# Confirm gitignored before we exit (paranoia — these must never
# end up in a commit)
for f in "$KEYSTORE_PATH" "$PROPERTIES_PATH" "$PLAY_KEY_PATH"; do
  [ -e "$f" ] || continue
  if ! git check-ignore -q "$f" 2>/dev/null; then
    echo "WARNING: $f is NOT gitignored. Add to .gitignore before committing." >&2
  fi
done

echo
echo "Done. You can now run:"
echo "  cd mobile/android && ./gradlew bundleRelease"
echo "or:"
echo "  ./scripts/deploy-playstore.sh"
echo
echo "After build, upload to Play Store internal track:"
echo "  PLAY_STORE_KEY_FILE=$PLAY_KEY_PATH python3 scripts/upload-playstore.py"
