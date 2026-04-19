#!/usr/bin/env bash
# Encode a raw screen recording to a web-ready hero MP4.
#
# Usage:
#   ./encode.sh input.mov [output.mp4]
#
# Produces an H.264 CRF 28, 30 fps, no-audio, 1080p-max clip,
# trimmed to the first 20 seconds. Target final size ≤2 MB.
#
# Why these settings:
#   - CRF 28 is the quality/size sweet spot for screen content
#     (terminals + UI + small motion). Visually near-identical
#     to CRF 23 at a fraction of the size.
#   - `-an` drops audio — hero videos autoplay muted on the web.
#   - `scale=-2:'min(1080,ih)'` caps height at 1080p, keeps
#     aspect ratio, ensures even width (libx264 requires even).
#   - `-movflags +faststart` moves moov atom to the front so the
#     video starts playing before fully downloaded.
#   - `-t 20` hard-caps duration. If your recording is shorter,
#     ffmpeg just uses what's there.

set -euo pipefail

INPUT="${1:-}"
OUTPUT="${2:-$(dirname "$0")/output/hero.mp4}"

if [[ -z "$INPUT" || ! -f "$INPUT" ]]; then
  echo "usage: $0 <input.mov> [output.mp4]" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUTPUT")"

ffmpeg -hide_banner -loglevel warning -y \
  -i "$INPUT" \
  -t 20 \
  -vf "scale=-2:'min(1080,ih)',fps=30" \
  -c:v libx264 -preset slow -crf 28 \
  -pix_fmt yuv420p \
  -movflags +faststart \
  -an \
  "$OUTPUT"

SIZE=$(du -h "$OUTPUT" | cut -f1)
DUR=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$OUTPUT")
echo ""
echo "→ $OUTPUT"
echo "  duration: ${DUR}s"
echo "  size:     $SIZE"

SIZE_BYTES=$(stat -f%z "$OUTPUT" 2>/dev/null || stat -c%s "$OUTPUT")
SIZE_KB=$((SIZE_BYTES / 1024))
CAP_KB=$((2 * 1024))
if (( SIZE_KB > CAP_KB )); then
  echo ""
  echo "⚠ output is ${SIZE_KB} KB — above the 2 MB target."
  echo "  Options: re-shoot shorter, raise CRF (try 30 or 32)," \
       "or drop resolution to 720 in the scale filter."
else
  echo ""
  echo "✓ under the 2 MB target — ready for web/public/demo-push.mp4"
fi
