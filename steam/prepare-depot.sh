#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TARGET="${1:-}"
STAGE="$ROOT/steam/staging/$TARGET"

case "$TARGET" in
  macos)
    [ "$(uname -s)" = "Darwin" ] || { echo "macOS depots must be built on macOS" >&2; exit 2; }
    if [ -n "${STEAM_APP_BUNDLE:-}" ]; then
      SOURCE="$STEAM_APP_BUNDLE"
    else
      (cd "$ROOT/electron" && npm run pack)
      SOURCE="$(find "$ROOT/electron/dist" -maxdepth 3 -type d -name 'Yaver.app' -print -quit)"
    fi
    [ -n "$SOURCE" ] || { echo "Yaver.app was not produced" >&2; exit 1; }
    codesign --verify --deep --strict "$SOURCE"
    if ! xcrun stapler validate "$SOURCE" >/dev/null 2>&1 || ! spctl -a -t exec "$SOURCE" >/dev/null 2>&1; then
      echo "Yaver.app is signed but not notarized/stapled; refusing a false-ready Steam depot" >&2
      echo "Build through the notarized macOS release lane, then stage that exact app bundle" >&2
      exit 1
    fi
    ;;
  linux)
    [ "$(uname -s)" = "Linux" ] || { echo "Linux depots must be built on Linux" >&2; exit 2; }
    (cd "$ROOT/electron" && npm run pack)
    SOURCE="$(find "$ROOT/electron/dist" -maxdepth 2 -type d -name '*linux*unpacked*' -print -quit)"
    [ -n "$SOURCE" ] || { echo "unpacked Linux application was not produced" >&2; exit 1; }
    ;;
  windows)
    case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) ;; *) echo "Windows depots must be built and signed on Windows" >&2; exit 2 ;; esac
    (cd "$ROOT/electron" && npm run pack -- --platform=win32 --arch=x64 && npx electron-builder --dir --win --x64)
    SOURCE="$(find "$ROOT/electron/dist" -maxdepth 2 -type d -name 'win-unpacked' -print -quit)"
    [ -n "$SOURCE" ] || { echo "unpacked Windows application was not produced" >&2; exit 1; }
    ;;
  *) echo "usage: $0 <windows|macos|linux>" >&2; exit 2 ;;
esac

rm_stage="$STAGE"
case "$rm_stage" in "$ROOT/steam/staging/"*) ;; *) echo "unsafe stage path" >&2; exit 1 ;; esac
if [ -e "$STAGE" ]; then
  ls -la "$STAGE"
  rm -rf "$STAGE"
fi
mkdir -p "$STAGE"
if [ "$TARGET" = "macos" ]; then
  ditto "$SOURCE" "$STAGE/Yaver.app"
else
  cp -R "$SOURCE"/. "$STAGE"/
fi
printf '%s\n' "distribution=steam" "product=Yaver" "platform=$TARGET" > "$STAGE/yaver-distribution.txt"
echo "Prepared $TARGET depot at $STAGE"
