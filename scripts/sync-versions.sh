#!/bin/bash
# sync-versions.sh — Propagate versions.json to all downstream files.
# Run this after editing versions.json to keep everything in sync.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSIONS_FILE="$REPO_ROOT/versions.json"

if [ ! -f "$VERSIONS_FILE" ]; then
  echo "ERROR: versions.json not found at $VERSIONS_FILE"
  exit 1
fi

# Read versions using node (available in all our environments)
read_version() {
  node -e "console.log(JSON.parse(require('fs').readFileSync('$VERSIONS_FILE','utf8'))['$1'])"
}

CLI_VERSION=$(read_version cli)
MOBILE_VERSION=$(read_version mobile)
RELAY_VERSION=$(read_version relay)
WEB_VERSION=$(read_version web)
INSTALLER_VERSION=$(read_version installer)
BACKEND_VERSION=$(read_version backend)
PI_IMAGE_VERSION=$(read_version piImage)

changed=0
update_file() {
  local file="$1" desc="$2"
  if [ -f "$file" ] && ! diff -q "$file" "$file.tmp" >/dev/null 2>&1; then
    mv "$file.tmp" "$file"
    echo "  updated: $desc ($file)"
    changed=1
  else
    rm -f "$file.tmp"
  fi
}

echo "Syncing versions from versions.json..."
echo "  cli=$CLI_VERSION mobile=$MOBILE_VERSION relay=$RELAY_VERSION"
echo "  web=$WEB_VERSION installer=$INSTALLER_VERSION backend=$BACKEND_VERSION piImage=$PI_IMAGE_VERSION"
echo ""

# --- Desktop CLI (Go const) ---
CLI_MAIN="$REPO_ROOT/desktop/agent/main.go"
if [ -f "$CLI_MAIN" ]; then
  sed "s/^const version = \".*\"/const version = \"$CLI_VERSION\"/" "$CLI_MAIN" > "$CLI_MAIN.tmp"
  update_file "$CLI_MAIN" "CLI version"
fi

# --- Relay (Go const) ---
RELAY_MAIN="$REPO_ROOT/relay/main.go"
if [ -f "$RELAY_MAIN" ]; then
  sed "s/^const version = \".*\"/const version = \"$RELAY_VERSION\"/" "$RELAY_MAIN" > "$RELAY_MAIN.tmp"
  update_file "$RELAY_MAIN" "Relay version"
fi

# --- Mobile: app.json ---
APP_JSON="$REPO_ROOT/mobile/app.json"
if [ -f "$APP_JSON" ]; then
  sed "s/\"version\": \"[0-9]*\.[0-9]*\.[0-9]*\"/\"version\": \"$MOBILE_VERSION\"/" "$APP_JSON" > "$APP_JSON.tmp"
  update_file "$APP_JSON" "mobile app.json"
fi

# --- Mobile: Info.plist (CFBundleShortVersionString) ---
INFO_PLIST="$REPO_ROOT/mobile/ios/Yaver/Info.plist"
if [ -f "$INFO_PLIST" ]; then
  # Use PlistBuddy on macOS, sed on Linux
  if command -v /usr/libexec/PlistBuddy >/dev/null 2>&1; then
    cp "$INFO_PLIST" "$INFO_PLIST.tmp"
    /usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $MOBILE_VERSION" "$INFO_PLIST.tmp"
  else
    sed "s|<string>[0-9]*\.[0-9]*\.[0-9]*</string><!-- CFBundleShortVersionString -->|<string>$MOBILE_VERSION</string><!-- CFBundleShortVersionString -->|" "$INFO_PLIST" > "$INFO_PLIST.tmp"
    # Fallback: replace the line after CFBundleShortVersionString key
    if diff -q "$INFO_PLIST" "$INFO_PLIST.tmp" >/dev/null 2>&1; then
      awk -v ver="$MOBILE_VERSION" '
        /CFBundleShortVersionString/{found=1; print; next}
        found && /<string>/{sub(/<string>[^<]*<\/string>/, "<string>" ver "</string>"); found=0}
        {print}
      ' "$INFO_PLIST" > "$INFO_PLIST.tmp"
    fi
  fi
  update_file "$INFO_PLIST" "iOS Info.plist version"
fi

# --- Mobile: project.pbxproj (MARKETING_VERSION, appears twice) ---
PBXPROJ="$REPO_ROOT/mobile/ios/Yaver.xcodeproj/project.pbxproj"
if [ -f "$PBXPROJ" ]; then
  sed "s/MARKETING_VERSION = [0-9]*\.[0-9]*\.[0-9]*/MARKETING_VERSION = $MOBILE_VERSION/g" "$PBXPROJ" > "$PBXPROJ.tmp"
  update_file "$PBXPROJ" "iOS MARKETING_VERSION"
fi

# --- Mobile: Android build.gradle (versionName) ---
BUILD_GRADLE="$REPO_ROOT/mobile/android/app/build.gradle"
if [ -f "$BUILD_GRADLE" ]; then
  sed "s/versionName \"[0-9]*\.[0-9]*\.[0-9]*\"/versionName \"$MOBILE_VERSION\"/" "$BUILD_GRADLE" > "$BUILD_GRADLE.tmp"
  update_file "$BUILD_GRADLE" "Android versionName"
fi

# --- package.json files (use sed to preserve formatting) ---
update_pkg_version() {
  local file="$1" ver="$2" desc="$3"
  if [ -f "$file" ]; then
    sed "s/\"version\": \"[0-9]*\.[0-9]*\.[0-9]*\"/\"version\": \"$ver\"/" "$file" > "$file.tmp"
    update_file "$file" "$desc"
  fi
}

update_pkg_version "$REPO_ROOT/web/package.json" "$WEB_VERSION" "web package.json"
update_pkg_version "$REPO_ROOT/backend/package.json" "$BACKEND_VERSION" "backend package.json"
update_pkg_version "$REPO_ROOT/desktop/installer/package.json" "$INSTALLER_VERSION" "installer package.json"
update_pkg_version "$REPO_ROOT/cli/package.json" "$CLI_VERSION" "cli package.json"

echo ""
if [ "$changed" -eq 0 ]; then
  echo "All files already in sync."
else
  echo "Done. Review changes with: git diff"
fi
