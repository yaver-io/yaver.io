#!/usr/bin/env bash
set -euo pipefail

[ "${1:-}" = "--upload" ] || { echo "Refusing upload without explicit --upload" >&2; exit 2; }
: "${STEAM_APP_ID:?set STEAM_APP_ID}"
: "${STEAM_DEPOT_ID_WINDOWS:?set STEAM_DEPOT_ID_WINDOWS}"
: "${STEAM_DEPOT_ID_MACOS:?set STEAM_DEPOT_ID_MACOS}"
: "${STEAM_DEPOT_ID_LINUX:?set STEAM_DEPOT_ID_LINUX}"
: "${STEAM_CONTENTBUILDER:?set STEAM_CONTENTBUILDER to Steamworks ContentBuilder root}"
: "${STEAM_USERNAME:?set STEAM_USERNAME}"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
for platform in windows macos linux; do
  [ -d "$ROOT/steam/staging/$platform" ] || { echo "missing staged $platform depot" >&2; exit 1; }
done

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT
sed -e "s/__APP_ID__/$STEAM_APP_ID/g" \
    -e "s/__DEPOT_WINDOWS__/$STEAM_DEPOT_ID_WINDOWS/g" \
    -e "s/__DEPOT_MACOS__/$STEAM_DEPOT_ID_MACOS/g" \
    -e "s/__DEPOT_LINUX__/$STEAM_DEPOT_ID_LINUX/g" \
    -e "s|__CONTENT_ROOT__|$ROOT/steam/staging|g" \
    "$ROOT/steam/app_build.vdf.template" > "$out/app_build.vdf"
for platform in windows macos linux; do
  sed -e "s/__DEPOT_WINDOWS__/$STEAM_DEPOT_ID_WINDOWS/g" \
      -e "s/__DEPOT_MACOS__/$STEAM_DEPOT_ID_MACOS/g" \
      -e "s/__DEPOT_LINUX__/$STEAM_DEPOT_ID_LINUX/g" \
      -e "s|__CONTENT_ROOT__|$ROOT/steam/staging|g" \
      "$ROOT/steam/depot_${platform}.vdf.template" > "$out/depot_${platform}.vdf"
done

steamcmd=""
for candidate in builder/steamcmd builder/steamcmd.sh builder/steamcmd.exe builder_osx/steamcmd; do
  if [ -x "$STEAM_CONTENTBUILDER/$candidate" ]; then steamcmd="$STEAM_CONTENTBUILDER/$candidate"; break; fi
done
[ -n "$steamcmd" ] || { echo "steamcmd was not found inside STEAM_CONTENTBUILDER" >&2; exit 1; }
"$steamcmd" +login "$STEAM_USERNAME" +run_app_build "$out/app_build.vdf" +quit
