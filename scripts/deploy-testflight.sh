#!/bin/bash
set -eo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Keep the Apple Watch companion target present in the committed iOS project.
# The phone bridge is injected by Expo prebuild; this target is what makes the
# real paired watch app install alongside the iPhone app.
node "$ROOT/scripts/add-watch-ios-target.js"

# Same deal for the Live Activity widget extension: it is what puts Yaver on the
# CarPlay Dashboard (and the Lock Screen / Dynamic Island / Watch Smart Stack).
# Idempotent — a no-op when the target is already in the committed pbxproj.
node "$ROOT/scripts/add-liveactivity-ios-target.js"

cd "$ROOT/mobile/ios"

# Load secrets from the Yaver vault (project="mobile" + globals). Vault
# values win when present; values not in the vault fall through from the
# parent env. Locally: `yaver vault add APP_STORE_KEY_PATH --project mobile`.
# In CI: just don't put the secret in the vault — GitHub Actions env vars
# pass through unchanged.
if command -v yaver >/dev/null 2>&1; then
  eval "$(yaver vault env --project mobile 2>/dev/null || true)"
fi

# Vault-locked fallback (mirrors deploy-web.sh's ~/.androidplay/yaver.env).
# After kivanc's auth token rotates more than once, `yaver vault env`
# returns "wrong passphrase or corrupted vault" until YAVER_VAULT_PASSPHRASE
# is supplied — and `yaver deploy all` runs this script non-interactively,
# so there's no chance to set it. Without this, the script dies at the
# APP_STORE_KEY_PATH:? guard below with a misleading "secret not set"
# error when the real cause is a locked vault. `~/.appstoreconnect/yaver.env`
# is gitignored and pre-seeded with all four App Store Connect exports
# (see CLAUDE.md "iOS — TestFlight"). Vault values still win when readable;
# this only fills the gap when the vault can't be opened.
if [ -f "$HOME/.appstoreconnect/yaver.env" ]; then
  # shellcheck source=/dev/null
  set -a; source "$HOME/.appstoreconnect/yaver.env"; set +a
fi

# App Store Connect API key — set these env vars or in the Yaver vault.
AUTH_KEY="${APP_STORE_KEY_PATH:?APP_STORE_KEY_PATH unset. Likely cause: the Yaver vault is locked (auth token rotated >1x). Recover with ANY of: (1) pre-seed ~/.appstoreconnect/yaver.env (gitignored, 4 exports — see CLAUDE.md \"iOS — TestFlight\"); (2) YAVER_VAULT_PASSPHRASE=<old-token> before deploy; (3) re-add: yaver vault add APP_STORE_KEY_PATH --project mobile --value ...}"
AUTH_KEY_ID="${APP_STORE_KEY_ID:?Set APP_STORE_KEY_ID (env or yaver vault)}"
AUTH_KEY_ISSUER="${APP_STORE_KEY_ISSUER:?Set APP_STORE_KEY_ISSUER (env or yaver vault)}"

# Bump build number.
#
# BUG FIX (2026-07-19): this used to bump from the LOCAL Info.plist only. The
# local number drifts from reality — three autorun clones on one box sat at 445,
# 446, 447 while App Store Connect already had all of 441–447 from a prior day —
# so every upload collided (ITMS "build number already used") and burned a slot
# of the ~18/day TestFlight cap. Bump from max(local, ASC-highest) + 1 so a new
# build can never collide. The ASC query is best-effort: if the crypto libs or
# network aren't there, fall back to local+1 and warn (the export-error handler
# below now surfaces a collision clearly either way).
PLIST="Yaver/Info.plist"
CURRENT_BUILD=$(/usr/libexec/PlistBuddy -c "Print CFBundleVersion" "$PLIST")
ASC_MAX=$(APP_STORE_KEY_PATH="$AUTH_KEY" APP_STORE_KEY_ID="$AUTH_KEY_ID" APP_STORE_KEY_ISSUER="$AUTH_KEY_ISSUER" \
  python3 "$ROOT/scripts/asc-max-build.py" 2>/dev/null || echo "")
if [ -n "$ASC_MAX" ] && [ "$ASC_MAX" -ge "$CURRENT_BUILD" ] 2>/dev/null; then
  echo "ASC highest build is $ASC_MAX (local $CURRENT_BUILD) — bumping from max"
  NEW_BUILD=$((ASC_MAX + 1))
else
  [ -z "$ASC_MAX" ] && echo "WARN: could not read ASC max build — bumping from local $CURRENT_BUILD"
  NEW_BUILD=$((CURRENT_BUILD + 1))
fi
# PlistBuddy rewrites the whole plist and DROPS XML COMMENTS. Info.plist
# carries a long comment explaining why NSAllowsArbitraryLoads is set (the
# 100.64/10 CGNAT range that NSAllowsLocalNetworking does not exempt) — that
# reasoning was being silently deleted on every single deploy. Patch the one
# value as text instead so the documentation survives.
python3 - "$PLIST" "$NEW_BUILD" <<'PYEOF'
import re, sys
path, new_build = sys.argv[1], sys.argv[2]
with open(path) as f:
    s = f.read()
s2 = re.sub(r'(<key>CFBundleVersion</key>\s*\n\s*<string>)[^<]*(</string>)',
            lambda m: m.group(1) + new_build + m.group(2), s, count=1)
if s2 == s:
    sys.exit("deploy-testflight: could not patch CFBundleVersion in " + path)
with open(path, "w") as f:
    f.write(s2)
PYEOF
echo "Build $CURRENT_BUILD → $NEW_BUILD"

# Clean stale archive so a failed build can't silently reuse it
ls -la /tmp/Yaver.xcarchive 2>/dev/null || true
rm -rf /tmp/Yaver.xcarchive

# Archive
echo "Archiving..."
xcodebuild -workspace Yaver.xcworkspace -scheme Yaver -configuration Release \
  -archivePath /tmp/Yaver.xcarchive archive \
  DEVELOPMENT_TEAM="${APPLE_TEAM_ID:?Set APPLE_TEAM_ID}" CODE_SIGN_STYLE=Automatic \
  CODE_SIGN_ALLOW_ENTITLEMENTS_MODIFICATION=YES \
  ENABLE_USER_SCRIPT_SANDBOXING=NO -allowProvisioningUpdates \
  -authenticationKeyPath "$AUTH_KEY" \
  -authenticationKeyID "$AUTH_KEY_ID" \
  -authenticationKeyIssuerID "$AUTH_KEY_ISSUER" \
  -derivedDataPath /tmp/YaverBuild 2>&1 | tee /tmp/arch_full.log | tail -3

# Verify archive was created
if [ ! -d /tmp/Yaver.xcarchive ]; then
  echo "ERROR: Archive failed — no .xcarchive produced"
  exit 1
fi

# ExportOptions (no single-quote on EOF so APPLE_TEAM_ID expands)
# uploadSymbols=false: rnwhisper framework has missing dSYMs that
# Xcode 15+ treats as a fatal export error. Crash reports still work
# from Apple's symbolication — we just skip uploading our local dSYMs.
cat > /tmp/ExportOptions.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>method</key><string>app-store-connect</string>
    <key>teamID</key><string>${APPLE_TEAM_ID}</string>
    <key>signingStyle</key><string>automatic</string>
    <key>destination</key><string>upload</string>
    <key>uploadSymbols</key><false/>
</dict>
</plist>
EOF

# Export & upload (destination=upload sends directly to App Store Connect).
#
# BUG FIX (2026-07-19): this used to be `EXPORT_OUTPUT=$(xcodebuild …)`. Under
# `set -eo pipefail` (line 2), a FAILED command substitution aborts the script
# AT that assignment — so the diagnostic below and the "Redundant Binary Upload"
# tolerance were unreachable dead code, and every real failure surfaced as a
# bare `exit 70/65` with no message. That masked a full day of debugging (the
# actual cause was a build-number collision + a locked signing keychain).
# Stream to a log with `set +e` so the exit code is captured AND the error is
# visible.
echo "Exporting & uploading..."
EXPORT_LOG=/tmp/yaver_export.log
set +e
xcodebuild -exportArchive -archivePath /tmp/Yaver.xcarchive \
  -exportOptionsPlist /tmp/ExportOptions.plist \
  -exportPath /tmp/YaverExport -allowProvisioningUpdates \
  -authenticationKeyPath "$AUTH_KEY" \
  -authenticationKeyID "$AUTH_KEY_ID" \
  -authenticationKeyIssuerID "$AUTH_KEY_ISSUER" 2>&1 | tee "$EXPORT_LOG"
EXPORT_EXIT=${PIPESTATUS[0]}
set -e

# Success = exit 0, OR "Redundant Binary" (build already uploaded — treat as ok).
if [ "$EXPORT_EXIT" -ne 0 ] && ! grep -q "Redundant Binary Upload" "$EXPORT_LOG"; then
  echo "ERROR: Export/upload failed (exit $EXPORT_EXIT). Diagnosis:"
  # Surface the two most common real causes explicitly (learned 2026-07-19):
  if grep -q "errSecInternalComponent" "$EXPORT_LOG"; then
    echo "  → codesign errSecInternalComponent: a signing keychain is LOCKED to this"
    echo "    (headless) session. Unlock BOTH yaver-ci.keychain-db and login.keychain-db"
    echo "    + set-key-partition-list. See CLAUDE.md → 'Headless codesign'."
  fi
  if grep -qiE "bundle version.*already|redundant|The build.*has already been used|ITMS-4238" "$EXPORT_LOG"; then
    echo "  → build number $NEW_BUILD collides with an existing App Store Connect build."
    echo "    Bump CFBundleVersion above the ASC max and retry."
  fi
  grep -iE "error|errSec|EXPORT FAILED|ITMS-" "$EXPORT_LOG" | tail -8
  exit 1
fi

echo "✓ TestFlight build $NEW_BUILD uploaded"

mobile-cache-cleanup.sh mark-deployed yaver || true
