#!/usr/bin/env bash
# Gate for docs/tasks/webrtc-vibe-loop-parity.md
#
# The autorun loop's ONLY oracle. /goal is a directive the runner can satisfy
# by editing a file; this cannot be satisfied by writing prose.
#
# NEVER add `go test ./...` here. TestAuthLogout in desktop/agent hits the real
# ~/.yaver and signs the box out mid-run. Every Go test below is -run scoped on
# purpose.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
fail=0
step() { printf '\n=== %s ===\n' "$1"; }
check() { if [ "$1" -ne 0 ]; then echo "GATE FAIL: $2"; fail=1; else echo "ok: $2"; fi; }

step "go build (agent)"
(cd desktop/agent && go build ./...) ; check $? "desktop/agent builds"

step "go vet (touched files' package)"
(cd desktop/agent && go vet . >/dev/null 2>&1) ; check $? "desktop/agent vet"

# Scoped. The remote-runtime + streamer tests are the ones this task can break:
# remote_runtime_webrtc_test.go asserts an m=video offer selects rtpH264Streamer
# and that a video-less offer falls back to JPEG-DC.
step "go test (scoped: remote runtime, streamer, project detect, mobile projects)"
(cd desktop/agent && go test -count=1 \
  -run 'TestRemoteRuntime|TestSelectRemoteRuntimeStreamer|TestOfferWantsVideo|TestApplyWebRTCOffer|TestDetectProjectActions|TestMobileProjects|TestMonorepoFallback|TestTaskContext|TestConvexPrivacy' \
  . >/dev/null 2>&1) ; check $? "scoped agent tests"

# The privacy contract is enforced by a test; a new sync path must not leak
# paths/tokens into Convex. Cheap to run, expensive to regress.
step "convex privacy contract"
(cd desktop/agent && go test -count=1 -run 'TestConvexPrivacy|TestFieldsWeForbid' . >/dev/null 2>&1)
check $? "convex privacy test"

step "mobile typecheck"
(cd mobile && npx tsc --noEmit) ; check $? "mobile tsc"

step "web typecheck"
(cd web && npx tsc --noEmit) ; check $? "web tsc"

# The viewer JS lives inside a template string, so tsc does NOT see it. A syntax
# error there ships silently and only fails on a real device. Catch it here.
step "injected viewer JS syntax (invisible to tsc)"
tmp="$(mktemp -d)"
awk '/^    <script>$/{f=1;next} /^    <\/script>$/{f=0} f' mobile/app/remote-runtime.tsx \
  | sed 's/^      const cfg = \${payload};/      const cfg = {baseUrl:"",headers:{},sessionId:"s",deviceDims:null,transportMode:"direct-webrtc"};/' \
  > "$tmp/viewer.js"
if [ ! -s "$tmp/viewer.js" ]; then
  echo "GATE FAIL: could not extract viewer JS — did the <script> markers move?"
  fail=1
else
  node --check "$tmp/viewer.js" ; check $? "viewer JS parses"
fi

# The whole point of this task: the offer must ask for video, or the agent
# silently ships 1.1fps JPEG. Guard the literal call rather than trusting prose.
step "viewer still offers video (the regression this task exists to prevent)"
grep -q 'addTransceiver("video"' mobile/app/remote-runtime.tsx
check $? "mobile viewer offers m=video"
grep -q 'addTransceiver("video"' web/components/dashboard/RemoteRuntimeViewer.tsx
check $? "web viewer offers m=video"
grep -q 'waitForIce' mobile/app/remote-runtime.tsx
check $? "mobile viewer waits for ICE (signaling is non-trickle)"
grep -q 'waitForIce' web/components/dashboard/RemoteRuntimeViewer.tsx
check $? "web viewer waits for ICE"

rm -rf "$tmp"

step "result"
if [ "$fail" -ne 0 ]; then echo "GATE: FAIL"; exit 1; fi
echo "GATE: PASS"
