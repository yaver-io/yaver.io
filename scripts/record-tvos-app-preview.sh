#!/bin/bash
set -euo pipefail

# Records the already-installed tvOS Simulator app. This script deliberately
# does not build, sign, upload, or read credentials.

SIMULATOR="${TVOS_SIM_UDID:-booted}"
DURATION="${TVOS_PREVIEW_SECONDS:-25}"
OUTPUT="${1:-$HOME/Desktop/Yaver-tvOS-App-Preview.mp4}"
BUNDLE_ID="${TVOS_BUNDLE_ID:-io.yaver.mobile}"

usage() {
  cat <<'EOF'
Usage: scripts/record-tvos-app-preview.sh [output.mp4]

Record a 15-30 second App Store preview from an already-installed, booted
Apple TV Simulator. The app opens on Vibing; use the Simulator remote during
the recording to search for SFMG and show the live preview.

Environment:
  TVOS_PREVIEW_SECONDS  Recording length, 15-30 seconds (default: 25)
  TVOS_SIM_UDID         Simulator UDID (default: booted)
  TVOS_BUNDLE_ID        Installed app bundle id (default: io.yaver.mobile)
EOF
}

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
  usage
  exit 0
fi

case "$DURATION" in
  ''|*[!0-9]*)
    echo "ERROR: TVOS_PREVIEW_SECONDS must be an integer from 15 through 30." >&2
    exit 2
    ;;
esac
if [ "$DURATION" -lt 15 ] || [ "$DURATION" -gt 30 ]; then
  echo "ERROR: App Store previews must be 15-30 seconds; requested ${DURATION}s." >&2
  exit 2
fi

for tool in xcrun ffmpeg ffprobe; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "ERROR: $tool is required to record and validate the preview." >&2
    exit 1
  }
done

# AudioToolbox's AAC encoder preserves the declared 256 kbps rate for silence;
# FFmpeg's native VBR AAC encoder collapses a silent track to a few kbps, which
# falls outside App Store Connect's preview specification.
if ! ffmpeg -hide_banner -encoders 2>/dev/null | grep -q 'aac_at'; then
  echo "ERROR: this ffmpeg must provide the macOS AudioToolbox AAC encoder (aac_at)." >&2
  exit 1
fi

if ! xcrun simctl list devices booted | grep -q "Apple TV"; then
  echo "ERROR: boot an Apple TV Simulator before recording." >&2
  exit 1
fi
if ! xcrun simctl get_app_container "$SIMULATOR" "$BUNDLE_ID" app >/dev/null 2>&1; then
  echo "ERROR: $BUNDLE_ID is not installed on the selected Apple TV Simulator." >&2
  echo "Install the build you want to submit, sign in, and rerun this recorder." >&2
  exit 1
fi

mkdir -p "$(dirname "$OUTPUT")"
RAW_VIDEO="$(mktemp -t yaver-tvos-preview.XXXXXX).mov"
cleanup() {
  rm -f "$RAW_VIDEO"
}
trap cleanup EXIT INT TERM

xcrun simctl terminate "$SIMULATOR" "$BUNDLE_ID" >/dev/null 2>&1 || true
xcrun simctl launch "$SIMULATOR" "$BUNDLE_ID" -yaver.tv.startAt vibing >/dev/null

echo "Recording begins in 5 seconds. Show: search SFMG → open → WebRTC preview."
sleep 5
xcrun simctl io "$SIMULATOR" recordVideo --codec=h264 --force "$RAW_VIDEO" >/dev/null 2>&1 &
RECORDER_PID=$!
sleep "$DURATION"
kill -INT "$RECORDER_PID" 2>/dev/null || true
wait "$RECORDER_PID" || true

if [ ! -s "$RAW_VIDEO" ]; then
  echo "ERROR: Simulator produced no recording." >&2
  exit 1
fi

# Apple's current Apple TV preview contract: landscape 1920x1080, H.264 High
# Profile level 4.0 or lower, at most 30 fps, 10-12 Mbps target video bitrate,
# and stereo AAC when an audio track is present. A silent standards-compliant
# audio track avoids depending on Simulator audio capture.
ffmpeg -hide_banner -loglevel error -y \
  -i "$RAW_VIDEO" \
  -f lavfi -i "anullsrc=channel_layout=stereo:sample_rate=48000" \
  -map 0:v:0 -map 1:a:0 -t "$DURATION" \
  -vf "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=30,format=yuv420p" \
  -c:v libx264 -profile:v high -level:v 4.0 \
  -b:v 11M -maxrate 12M -bufsize 24M \
  -c:a aac_at -b:a 256k -ar 48000 -ac 2 \
  -movflags +faststart -shortest "$OUTPUT"

PROBE="$(ffprobe -v error -select_streams v:0 \
  -show_entries stream=codec_name,width,height,r_frame_rate \
  -of csv=p=0 "$OUTPUT")"
ACTUAL_DURATION="$(ffprobe -v error -show_entries format=duration \
  -of default=noprint_wrappers=1:nokey=1 "$OUTPUT")"
AUDIO_PROBE="$(ffprobe -v error -select_streams a:0 \
  -show_entries stream=codec_name,sample_rate,channels,bit_rate \
  -of csv=p=0 "$OUTPUT")"

if [ "$PROBE" != "h264,1920,1080,30/1" ]; then
  echo "ERROR: preview validation failed: expected h264,1920,1080,30/1; got $PROBE" >&2
  exit 1
fi
if ! awk -v seconds="$ACTUAL_DURATION" 'BEGIN { exit !(seconds >= 15 && seconds <= 30.1) }'; then
  echo "ERROR: preview duration is outside 15-30 seconds: ${ACTUAL_DURATION}s" >&2
  exit 1
fi
if ! printf '%s\n' "$AUDIO_PROBE" | awk -F, '
  $1 == "aac" && $2 == "48000" && $3 == "2" && $4 >= 240000 && $4 <= 270000 { ok = 1 }
  END { exit !ok }
'; then
  echo "ERROR: expected stereo 48kHz AAC near 256kbps; got $AUDIO_PROBE" >&2
  exit 1
fi

echo "App Store preview ready: $OUTPUT"
echo "Video: $PROBE · audio: $AUDIO_PROBE · duration ${ACTUAL_DURATION}s"
