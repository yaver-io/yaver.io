# Handoff → continue on the Mac mini — 2026-07-16 ~03:15

This MacBook is being closed. Everything below is committed + pushed to `main`.
On the Mac mini: `git pull` (it's the user's own dev box, `ssh pokayoke` /
Tailscale `mobiles-mac-mini` `100.89.155.25`), then continue from "What's next".

## TL;DR — what shipped this session
- **Netflix-grade auth persistence** (sign in once, silent extend-only
  `/auth/refresh` on launch, never re-prompt) across web/tvOS/visionOS/watchOS/
  Wear (+ mobile/desktop already had it).
- **Connectivity self-heal** (mobile + car/glass/AR via shared DeviceContext):
  on a relay-auth failure, silently `POST /settings/repair-relay` → re-pull creds
  → retry, once per streak. tvOS analog = host re-resolve. This is the
  "every box: no reachable transport" fix.
- **Stream C narrated auto-connect** on load (sticky→primary→secondary→best-online)
  — the phone connects to the Mac-mini **Primary** on open instead of showing the
  picker. Web + tvOS too.
- **Wake/sleep honest progress**: no false 100% before the box is usable
  (runnersAuthorized gate), elapsed timer + **estimated time remaining**
  ("Booting… · 1:24 · ~2:30 left"), signed-out→"Sign this machine in" CTA,
  in-stage creep. 8 wake tests (3 unit + 5 closed-loop) pass.
- **iOS/redroid streaming** (remote_runtime + WebRTC + iossim/redroid drivers).

## Deploy state (all done)
- **`main`**: all pushed. Latest commits: `63892751c` (visionos MachineRegistry),
  `d8c6a3ff5` (transport+streaming), `58ddddac8` (wake/sleep+tests),
  `2c4897d12` (relay self-heal), `f99b6b9e9` (tvOS heal + CLAUDE.md parity rule),
  `9e9c06200`/`5460d3108`/`8d05d7324` (web/tvOS/auth).
- **Convex**: deployed to prod (`perceptive-minnow-557`).
- **Cloudflare web**: live (`web/v1.1.157` auth, `web/v1.1.158` Stream C).
- **TestFlight**: **iOS build 441** (incl. embedded watchOS companion — my
  `watch/YaverWatch/*` auth changes ride in the iOS app; altool has NO `watchos`
  platform, so watch CANNOT ship standalone), **tvOS (YaverTV)**, **visionOS
  (YaverVision)** all uploaded, processing on Apple's side. Install once
  processed.

## The screen recording (old build) → 441 fixes each
`ScreenRecording_07-16 03-09` showed the CURRENT build: (1) re-prompted Sign in
with Apple, (2) "No machine selected / Looking for machines" → picker, (3)
connected to Kvancs-MacBook-Air, not the Mac-mini Primary. All boxes were green/
online (connectivity already good). Build 441 fixes all three: auth persistence
(no re-signin), auto-connect-on-open, and prefer-Primary (→ Mac-mini).

## What's next (do on the Mac mini)
1. **Install TestFlight 441** on the iPhone; verify: open app → **no re-signin**,
   **auto-connects to Mac-mini (Primary)**, not the picker. Then tvOS/vision/watch.
2. **Managed cloud (Hetzner) wake/sleep** from the phone: wake a parked box →
   watch the honest ladder + timer + ETA; park it. A live wake costs metered
   Hetzner time.
3. **Develop Talos from the phone** (the test use case): load Talos (`../talos`,
   Expo/RN) into the Yaver container via **Hermes push** (`/dev/build-native` →
   Hermes bundle → `ExpoReactNativeFactory`), or Talos + feedback SDK. Drive it
   from the phone against the Mac mini or a managed box.

## Operational landmines (READ before rebuilding mobile)
- **After deleting/reinstalling `mobile/node_modules` you MUST**: (a)
  `cd mobile/ios && pod install` (re-runs prepare_commands — downloads
  react-native-audio-api FFmpeg xcframeworks + regenerates ExpoSQLite session
  build flags), and (b) `rm -rf /tmp/YaverBuild` + `~/Library/Developer/Xcode/
  DerivedData/ModuleCache.noindex` (deploy-testflight.sh only cleans the
  .xcarchive, so a stale broken module gets reused). Skipping this cost 4 build
  attempts. See `reference_testflight_node_modules_reinstall` memory. Prefer
  `npm ci`, not `npm install`.
- Raw archive log is `/tmp/arch_full.log` (script only tails 3 lines).
- **watchOS ships embedded in iOS**; don't chase standalone `deploy-watchos.sh`
  (altool has no `watchos` platform).
- **visionOS** shares tvOS sources via `visionos/project.yml` (xcodegen) — if
  YaverStore/etc. reference a new tvOS file, ADD it to project.yml + `xcodegen
  generate`, else xrOS won't compile. Bump `VISIONOS_BUILD_NUMBER` per upload
  (redundant-binary otherwise).

## HOW TO RESUME on the Mac mini (this session dies when the MacBook closes)

This Claude session runs on the MacBook and CANNOT follow you to the Mac mini,
and it has no SSH/token to reach the mini remotely. Continuation is one of:

**A) Drive the Mac mini FROM THE PHONE** (the product's whole point — do this
after TestFlight 441 finishes processing):
- Open the Yaver app → it auto-connects to the Mac-mini (Primary) → open a
  Claude/Codex runner and give it this file's "What's next" as the task. That IS
  "developing at the Mac mini" — the runner executes on the mini, you drive from
  the phone.

**B) New Claude Code session ON the Mac mini** (`ssh pokayoke`, it's your box):
```bash
cd ~/Workspace/yaver.io   # or wherever the repo lives on the mini
git pull
# start an auto-runner that continues this work:
claude   # then paste: "read docs/handoff/2026-07-16-mac-mini-continuation.md and continue"
# or a yaver runner wrapper if configured: yaver claude --machine=mac-mini
```

**C) Autonomous overnight** (if you want it running while you sleep): start a
Claude session on the mini with the `/loop` skill pointed at the "What's next"
list, or a scheduled cloud agent (`/schedule`). Neither can be launched from THIS
MacBook onto the mini — start it on the mini (B) or from the phone (A).

Note: the mini's agent is healthy + reachable on Tailscale
(`100.89.155.25:18080` → `lifecycleState: ready-to-connect`, v1.99.302); it just
needs a driver (phone or a session on it).

## Open follow-ups (not blocking)
- tvOS/visionOS auto-connect role narration is machine-name-only; upgrade to
  Primary/Secondary via a GET /settings fetch.
- Deepest wake fix (optional): don't flip `status:"active"` in the backend until
  the box is actually usable — fixes both the bar AND the device-list `online`
  flicker at the source (touches many consumers, left as recommendation).
- More-menu reorg (#11 in `docs/handoff/2026-07-16-remote-box-ui-bugs.md`).
