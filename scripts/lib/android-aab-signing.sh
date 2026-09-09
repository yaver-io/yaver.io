#!/usr/bin/env bash

# Verify both that an Android App Bundle is signed and that its signer is the
# public Yaver upload certificate. Google Play rejects unsigned bundles, but a
# merely valid signature from the wrong key is also an unrecoverable release
# error because version codes are immutable once accepted.
yaver_verify_aab_signer() {
  local aab="$1"
  local expected="${YAVER_ANDROID_UPLOAD_SHA256:-FF:E9:9C:94:63:B0:2A:42:0D:11:42:35:CD:A7:3F:76:7A:5A:0D:18:0F:73:CE:CF:BC:2E:E2:DF:C4:AC:D5:4E}"
  local jarsigner_bin="${JAVA_HOME:+$JAVA_HOME/bin/jarsigner}"
  local keytool_bin="${JAVA_HOME:+$JAVA_HOME/bin/keytool}"

  if [ ! -f "$aab" ]; then
    echo "ERROR: Android App Bundle does not exist: $aab" >&2
    return 1
  fi
  if [ -z "$jarsigner_bin" ] || [ ! -x "$jarsigner_bin" ]; then
    jarsigner_bin="$(command -v jarsigner 2>/dev/null || true)"
  fi
  if [ -z "$keytool_bin" ] || [ ! -x "$keytool_bin" ]; then
    keytool_bin="$(command -v keytool 2>/dev/null || true)"
  fi
  if [ -z "$jarsigner_bin" ] || [ -z "$keytool_bin" ]; then
    echo "ERROR: AAB verification requires jarsigner and keytool from a JDK." >&2
    return 1
  fi

  if ! "$jarsigner_bin" -verify "$aab" >/dev/null; then
    echo "ERROR: Android App Bundle signature verification failed: $aab" >&2
    return 1
  fi

  local actual
  actual="$(LC_ALL=C "$keytool_bin" -printcert -jarfile "$aab" 2>/dev/null \
    | sed -n 's/^[[:space:]]*SHA256:[[:space:]]*//p' | head -1)"
  if [ -z "$actual" ]; then
    echo "ERROR: could not read the AAB signing certificate fingerprint: $aab" >&2
    return 1
  fi
  if [ "$(printf '%s' "$actual" | tr '[:lower:]' '[:upper:]')" != \
       "$(printf '%s' "$expected" | tr '[:lower:]' '[:upper:]')" ]; then
    echo "ERROR: AAB signer does not match the configured Yaver upload certificate." >&2
    echo "  expected SHA256: $expected" >&2
    echo "  actual SHA256:   $actual" >&2
    return 1
  fi

  echo "AAB signature verified: SHA256 $actual"
}
