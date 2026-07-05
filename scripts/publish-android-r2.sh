#!/bin/bash
#
# publish-android-r2.sh — push the Android APK + install site to the public
# download hub at https://download.yaver.io (Cloudflare R2 bucket "yaver-apk").
#
# This codifies what used to be a manual `wrangler r2 object put` flow. It is
# the yaver analog of talos's publish_apk.sh, but the host is Cloudflare R2
# (HTTPS via the custom-domain binding) instead of a Hetzner box — no server to
# keep running, TLS is automatic.
#
# What lands in the bucket (all HTTPS, all at download.yaver.io):
#   yaver-<code>.apk           immutable per-build artifact
#   latest.apk                 copy of the newest build (QR + Download button target)
#   version.json               {app,versionName,versionCode,file,size,package}
#   index.html                 the QR install page (scripts/download-site/index.html)
#   .well-known/assetlinks.json  Android App Links + passkeys for io.yaver.mobile
#
# Usage:
#   scripts/publish-android-r2.sh [path/to/app.apk]
#
#   With no arg it builds a universal, signed APK from the release AAB
#   (mobile/android/app/build/outputs/bundle/release/app-release.aab) via
#   bundletool — run scripts/deploy-playstore.sh first to produce the AAB, or
#   pass an already-built APK path.
#
# Env:
#   BUCKET                 R2 bucket name (default: yaver-apk)
#   ANDROID_RELEASE_SHA256 prod Play app-signing SHA-256, merged into assetlinks
#   (Android signing creds are read from mobile/android/keystore.properties and
#    the yaver vault / ~/.androidplay/yaver.env, same as deploy-playstore.sh.)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUCKET="${BUCKET:-yaver-apk}"
PACKAGE="io.yaver.mobile"
APP="yaver"
ANDROID_DIR="$REPO_ROOT/mobile/android"
GRADLE_FILE="$ANDROID_DIR/app/build.gradle"
SITE_DIR="$REPO_ROOT/scripts/download-site"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --- credentials (mirror deploy-playstore.sh: vault wins, env-file fills gap) --
if command -v yaver >/dev/null 2>&1; then
  eval "$(yaver vault env --project mobile 2>/dev/null || true)"
fi
if [ -f "$HOME/.androidplay/yaver.env" ]; then
  set -a; . "$HOME/.androidplay/yaver.env"; set +a
fi

wrangler() { npx --yes wrangler@latest "$@"; }

# --- resolve version from build.gradle -----------------------------------------
VERSION_CODE="$(grep 'versionCode ' "$GRADLE_FILE" | head -1 | sed 's/[^0-9]//g')"
VERSION_NAME="$(grep 'versionName ' "$GRADLE_FILE" | head -1 | sed -E 's/.*versionName "([^"]+)".*/\1/')"
if [ -z "$VERSION_CODE" ] || [ -z "$VERSION_NAME" ]; then
  echo "ERROR: could not read versionCode/versionName from $GRADLE_FILE" >&2
  exit 1
fi

# --- obtain the APK -------------------------------------------------------------
APK_PATH="${1:-}"
if [ -z "$APK_PATH" ]; then
  AAB_PATH="$ANDROID_DIR/app/build/outputs/bundle/release/app-release.aab"
  if [ ! -f "$AAB_PATH" ]; then
    echo "ERROR: no APK arg and no release AAB at:" >&2
    echo "  $AAB_PATH" >&2
    echo "Run scripts/deploy-playstore.sh first, or pass an APK path." >&2
    exit 1
  fi
  # keystore.properties: storeFile is relative to mobile/android/app
  KS_PROPS="$ANDROID_DIR/keystore.properties"
  if [ ! -f "$KS_PROPS" ]; then
    echo "ERROR: $KS_PROPS not found (needed to sign the universal APK)." >&2
    echo "See CLAUDE.md > Android — Play Store for how to restore it." >&2
    exit 1
  fi
  KS_STORE_FILE="$(grep '^storeFile=' "$KS_PROPS" | cut -d= -f2-)"
  KS_STORE_PASS="$(grep '^storePassword=' "$KS_PROPS" | cut -d= -f2-)"
  KS_ALIAS="$(grep '^keyAlias=' "$KS_PROPS" | cut -d= -f2-)"
  KS_KEY_PASS="$(grep '^keyPassword=' "$KS_PROPS" | cut -d= -f2-)"
  KS_ABS="$(cd "$ANDROID_DIR/app" && cd "$(dirname "$KS_STORE_FILE")" && pwd)/$(basename "$KS_STORE_FILE")"

  command -v bundletool >/dev/null 2>&1 || {
    echo "ERROR: bundletool not found. Install with: brew install bundletool" >&2
    exit 1
  }
  echo "Building universal signed APK from AAB via bundletool..."
  bundletool build-apks \
    --bundle="$AAB_PATH" \
    --output="$WORK/app.apks" \
    --mode=universal \
    --ks="$KS_ABS" \
    --ks-pass="pass:$KS_STORE_PASS" \
    --ks-key-alias="$KS_ALIAS" \
    --key-pass="pass:$KS_KEY_PASS"
  unzip -o "$WORK/app.apks" universal.apk -d "$WORK" >/dev/null
  APK_PATH="$WORK/universal.apk"
fi

if [ ! -f "$APK_PATH" ]; then
  echo "ERROR: APK not found at $APK_PATH" >&2
  exit 1
fi

APK_SIZE="$(wc -c < "$APK_PATH" | tr -d ' ')"
APK_KEY="yaver-${VERSION_CODE}.apk"

echo ""
echo "Publishing to R2 bucket '$BUCKET' (https://download.yaver.io):"
echo "  app:         $APP"
echo "  versionName: $VERSION_NAME"
echo "  versionCode: $VERSION_CODE"
echo "  apk:         $APK_KEY ($((APK_SIZE / 1024 / 1024)) MB)"
echo ""

# --- version.json ---------------------------------------------------------------
cat > "$WORK/version.json" <<JSON
{"app":"$APP","versionName":"$VERSION_NAME","versionCode":$VERSION_CODE,"file":"$APK_KEY","size":$APK_SIZE,"package":"$PACKAGE"}
JSON

# --- assetlinks.json (start from tracked file, merge prod SHA like deploy-web) ---
ASSETLINKS_SRC="$REPO_ROOT/web/public/.well-known/assetlinks.json"
cp "$ASSETLINKS_SRC" "$WORK/assetlinks.json"
if [ -n "${ANDROID_RELEASE_SHA256:-}" ] && command -v jq >/dev/null 2>&1; then
  jq --arg sha "$ANDROID_RELEASE_SHA256" '
    if (.[0].target.sha256_cert_fingerprints | index($sha)) then .
    else .[0].target.sha256_cert_fingerprints += [$sha] end
  ' "$WORK/assetlinks.json" > "$WORK/assetlinks.merged" && mv "$WORK/assetlinks.merged" "$WORK/assetlinks.json"
  echo "assetlinks.json: production SHA-256 merged."
fi

# --- upload (order: artifact -> latest -> metadata -> page) --------------------
APK_CT="application/vnd.android.package-archive"
put() { # key file content-type [cache-control]
  wrangler r2 object put "$BUCKET/$1" --file="$2" --content-type="$3" \
    ${4:+--cache-control="$4"} --remote
}

echo "→ $APK_KEY"
put "$APK_KEY"                    "$APK_PATH"              "$APK_CT"   "public, max-age=31536000, immutable"
echo "→ latest.apk"
put "latest.apk"                 "$APK_PATH"              "$APK_CT"   "public, max-age=300"
echo "→ version.json"
put "version.json"               "$WORK/version.json"     "application/json" "public, max-age=60"
echo "→ .well-known/assetlinks.json"
put ".well-known/assetlinks.json" "$WORK/assetlinks.json" "application/json" "public, max-age=3600"
echo "→ index.html"
put "index.html"                 "$SITE_DIR/index.html"   "text/html; charset=utf-8" "public, max-age=300"

echo ""
echo "Done. Live at:"
echo "  https://download.yaver.io/            (QR install page)"
echo "  https://download.yaver.io/latest.apk  (v$VERSION_NAME, build $VERSION_CODE)"
echo "  https://download.yaver.io/version.json"
