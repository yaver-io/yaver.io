#!/usr/bin/env bash
# Copies shared/client-core/src/*.ts into the `_core/` mirror inside
# every consumer (mobile, yaver-feedback-react-native). Run this after
# editing anything under shared/client-core/.
#
# CI invokes `--check` to fail the build if the mirrors have drifted,
# which would indicate someone edited a mirror directly instead of the
# source of truth.
#
# See ARCHITECTURE_CLIENT_CORE.md.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$REPO_ROOT/shared/client-core/src"

# Each mirror is a path relative to the repo root.
MIRRORS=(
  "mobile/src/_core"
  "sdk/feedback/react-native/src/_core"
)

mode="write"
if [[ "${1:-}" == "--check" ]]; then
  mode="check"
fi

exit_code=0

for mirror in "${MIRRORS[@]}"; do
  dest="$REPO_ROOT/$mirror"
  if [[ "$mode" == "write" ]]; then
    mkdir -p "$dest"
    rm -f "$dest"/*.ts 2>/dev/null || true
    cp "$SRC"/*.ts "$dest"/
    # Add a banner to the first line of each mirror file so anyone
    # editing them sees the warning before typing.
    for f in "$dest"/*.ts; do
      tmp="$(mktemp)"
      {
        echo "// AUTO-SYNCED from shared/client-core/src/$(basename "$f")."
        echo "// DO NOT EDIT IN PLACE. Edit the source and re-run"
        echo "// scripts/sync-client-core.sh. CI checks drift via \`--check\`."
        echo ""
        cat "$f"
      } > "$tmp"
      mv "$tmp" "$f"
    done
    echo "synced → $mirror ($(ls "$dest" | wc -l | tr -d ' ') files)"
  else
    # Compare each file under $SRC against its mirror, stripping the
    # auto-sync banner (4 lines) from the mirror before diffing.
    for src_file in "$SRC"/*.ts; do
      base="$(basename "$src_file")"
      mirror_file="$dest/$base"
      if [[ ! -f "$mirror_file" ]]; then
        echo "missing mirror: $mirror/$base" >&2
        exit_code=1
        continue
      fi
      # Strip the 4-line banner.
      stripped="$(tail -n +5 "$mirror_file")"
      expected="$(cat "$src_file")"
      if [[ "$stripped" != "$expected" ]]; then
        echo "drift detected: $mirror/$base" >&2
        exit_code=1
      fi
    done
  fi
done

if [[ "$mode" == "check" ]]; then
  if [[ $exit_code -eq 0 ]]; then
    echo "all client-core mirrors are in sync."
  else
    echo "" >&2
    echo "Fix: run scripts/sync-client-core.sh (no args) and commit the result." >&2
  fi
  exit $exit_code
fi
