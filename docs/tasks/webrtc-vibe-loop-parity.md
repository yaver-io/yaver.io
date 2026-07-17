# Task: WebRTC vibe-loop parity for non-Hermes stacks

Goal: make "see the running app while you vibe-code it" work for the
stacks Hermes can never reach (Flutter, Swift, Kotlin) and for web —
with the same feel as the RN Hermes loop. Then get the demo apps out of
this repo so yaver.io is Yaver only.

Read `docs/architecture/AI_ARCH.md` seams before changing transport.
**Docs drift. grep the code; when a doc disagrees with code, the doc is
the bug — fix it in the same change.**

## Ground truth (audited 2026-07-17 — do NOT re-litigate)

- The agent's WebRTC is ~90% done and CORRECT. `pion/webrtc/v4` +
  `pion/turn/v4` are direct deps. `remote_runtime_webrtc.go`,
  `h264_extract.go` (Annex-B), `remote_runtime_video_track.go`
  (`xcrun simctl io <t> recordVideo` for iOS sim, `adb screenrecord` for
  Android, 180s cap handled by segment restart), `stream_webrtc_fanout.go`,
  `turn_credentials.go`. **Do not rewrite any of this.**
- `selectRemoteRuntimeStreamer(targetID, offerSDP)` greps the offer for
  `m=video` (`offerWantsVideo`). No m=video → JPEG-over-DataChannel.
- Commit b9a9318ce fixed the phone + web viewers to offer
  `addTransceiver("video",{recvonly})`, paint `ontrack` into `<video>`,
  fetch ICE from `/stream/webrtc/ice`, and `waitForIce()` before
  signaling (signaling is NON-TRICKLE — there is no `addIceCandidate`
  path on either side, so the offer must carry its candidates).
- **Hermes is RN/Expo-only** and gated at `hotreload.tsx:77`. Flutter is
  classed `DevServerKindWeb` (`devserver_kind.go:37`). Native/Flutter can
  NEVER load into the Yaver container. Their paths are `native-webrtc`
  (viewer) or a standalone APK/IPA via `yaver wire push`.
- **There is NO native Kotlin/Swift feedback SDK.** `sdk/feedback/` has
  only react-native, web, flutter, unity, browser-extension. Do NOT try
  to `import` one. Do NOT invent one in this task.
- **But the viewer owns the feedback trigger**: `remote_runtime.go:709`
  `case "launch-feedback"` pushes `{"type":"feedback-launch-request"}`
  down the events DataChannel; `remote-runtime.tsx:142` +
  `feedbackTrigger.ts:74` drive it. So a STREAMED native app gets the
  feedback loop with no in-app SDK. That is the intended design.
- JPEG-DC is single-viewer by design; RTP fans out. Multi-viewer only
  works on the RTP lane.
- `mobile/package.json` has NO `react-native-webrtc`. The phone viewer is
  `RTCPeerConnection` inside a `react-native-webview` (WKWebView's own
  engine). **Do not add react-native-webrtc** — it forces a dev-client
  prebuild and buys nothing here.

## Hard constraints

- Never commit secrets, infra IPs, hostnames. Repo is public.
- **Never `go test ./...` in `desktop/agent`** — `TestAuthLogout` hits the
  real `~/.yaver` and signs the box out. Always `-run` scoped.
- Only `git commit -- <explicit paths>`. Never `git add -A` / `git add .`
  — this checkout is shared and a sweep has eaten other sessions' work
  before.
- Do not touch ACME/Let's Encrypt TLS code (`ops_cert.go`, `domain.go`,
  `dns_mcp.go`) — unrelated to the "Acme Store" demo.
- Do not touch generic `acme` placeholder fixtures (`com.acme.app`,
  `qa@acme.com`) in Go tests — also unrelated.
- Landing page UI is already good. **Change content, not design.** Do not
  restyle, re-layout, or "improve" `web/app/page.tsx`.
- One deploy per converged change, and only when asked. Do not deploy to
  check something.

## Phases (in order; each must gate green before the next)

### P1 — Remove the demo apps from this repo ("keep yaver as yaver only")
The "Acme Store" was renamed twice and IS `demo/mobile/bento` today
(`acme-store` → `demo/BentoApp` → `demo/mobile/bento`; same LoginForm.tsx
/ProductCard.tsx).
- Delete `demo/mobile/bento/`.
- Delete `demo/README.md` (entirely a dead recording script for a
  superseded video; describes a `demo/acme-store` dir that does not exist).
- Drop the `acme-store` row from `demo/mobile/README.md:18`.
- Drop `.gitignore:16` (`demo/AcmeStore/` — never matched anything).
- `backend/convex/schema.ts:1887`: change the `"AcmeStore dev build"`
  comment example.
- `web/app/page.tsx`: replace the BentoApp phone mock CONTENT with the
  Todo app (`:319` `<h4>BentoApp</h4>`, `:482` `jane@acme.dev`, `:1881`
  `["Name","bentoapp"]`, `:1904`, `:1939`). **Keep the exact same JSX
  structure, classes, and layout.** Also delete the dead
  `DemoSection`/`DEMO_TABS` (`:750-846`) — defined, never called.
- `web/app/docs/developers/page.tsx:2837`, `web/app/docs/feedback-sdk/page.tsx:826`,
  `desktop/agent/sdk_token.go:31,108`: `"BentoApp dev"` → a todo label.
- `desktop/agent/project_actions_test.go:61` `TestDetectProjectActions_AcmeStore`
  points at `demo/BentoApp` which no longer exists, so it SILENTLY SKIPS.
  After demo/ is gone, delete the test (its fixture is gone) or repoint it
  at a synthetic tmpdir fixture. Do not leave a skipping test.
- `desktop/agent/task_context_test.go:26` hardcodes an absolute
  `/Users/kivanccakmak/.../demo/BentoApp` — fix.
- Stale doc paths: `dev_cmd.go:223`, `devserver_http.go:4016`,
  `mobile_projects.go:1289`, `tasks.go:2204`.
- Tests referencing `demo/mobile/*` as SYNTHETIC STRING LITERALS
  (`mobile_projects_test.go:670-676`, `tasks_codex_cd_test.go:24`,
  `monorepo_fallback_picker_test.go`) are fine — they never touch disk.
  Leave them.

GATE P1: `cd web && npx tsc --noEmit` exit 0; `cd desktop/agent && go build ./...`;
`go test -count=1 -run 'TestDetectProjectActions|TestMobileProjects|TestMonorepoFallback' .`;
`grep -rniE "acme[ -_]?(store|shop)|bentoapp|BentoApp" --include=* .` returns
nothing outside ACME/TLS + placeholder fixtures. `demo/mobile/bento` gone.

### P2 — Shared viewer layer (`sdk/viewer/`)
~2400 lines of duplicated viewer logic across 6 files:
`mobile/app/remote-runtime.tsx` (672), `web/.../RemoteRuntimeViewer.tsx` (542),
`web/.../RemoteSessionView.tsx` (430), `mobile/app/remote-desktop.tsx` (360),
`web/.../RemoteDesktopView.tsx` (316), `tvos/.../WebPreviewStreamView.swift` (117).

Build `sdk/viewer/` as the single source of truth. Follow the
`mobile/src/lib/voice/` precedent: surface-agnostic core + adapters.
Abstract base classes with concrete subclasses:

```
abstract ViewerTransport   → negotiate(), attach(surface), dispose(), transportId()
  ├── RtpH264Transport          addTransceiver + ontrack → <video>
  ├── JpegDataChannelTransport  frames DC → <img> blob
  ├── JpegPollTransport         /frame.jpg poll (iOS/tvOS-safe)
  └── MjpegTransport            multipart <img> (web only)

abstract ViewerSurface     → paint(), intrinsicSize(), toDevicePoint()
  ├── VideoSurface              videoWidth/videoHeight
  └── ImageSurface              naturalWidth/naturalHeight

RemoteViewerCore           → ICE fetch, transport select, control dispatch,
                             feedback-launch-request handling
```

**Hard constraint: the core must be PURE DOM with ZERO imports.** web
imports it as a module; mobile stringifies it into the WebView HTML.
That constraint is what makes one source of truth possible — WKWebView
and the browser are both DOM. Wire it via a `paths` alias in BOTH
`web/tsconfig.json` and `mobile/tsconfig.json` (each currently aliases
`@/*` to its own dir; there is NO cross-package sharing today and no root
package.json — yaver.io is not an npm workspace).

Migrate the 4 WebRTC/JPEG viewers onto it. tvOS is Swift and has no
WebKit — it CANNOT share this; leave `WebPreviewStreamView.swift` alone
in P2.

GATE P2: both tsc exit 0; the Chromium offer harness (see P5) still shows
`m=video` + `m=application` + >0 candidates for the mobile AND web viewers;
no behavior change in remote-desktop.

### P3 — Fix the capability lie
`desktop/agent/remote_runtime.go:190-193` returns
`FeedbackSDKCompatible: mode == ExecutionModeNativeWebRTC` — so
`GET /remote-runtime/capabilities?framework=swift` claims
`feedbackSdkCompatible: true` when no native Swift/Kotlin SDK exists.
The note even hedges ("is *intended to* coexist").

Replace with something true and useful. Suggested shape — decide from the
code, not from this doc:
- `feedbackViaViewer: true` for `native-webrtc` (the viewer's
  launch-feedback path genuinely works — this is the real capability)
- `feedbackViaInAppSDK: false` for swift/kotlin, `true` for
  rn-hermes/web/flutter (`yaver-feedback-react-native`, `yaver-feedback-web`,
  `yaver_feedback` on pub.dev all exist and are published)
Update `sdk/feedback/README.md` — it says "five runtimes" and silently
omits native, and understates versions ("RN 0.6+" is really 0.9.x, "web
0.2+" is really 0.5.0).

GATE P3: `go test -count=1 -run 'TestRemoteRuntime|TestCapabilities' .` green;
no route claims an SDK that doesn't exist.

### P4 — tvOS / other native surfaces (assess, don't force)
- tvOS (`tvos/YaverTV/Views/WebPreviewStreamView.swift`) is hash-gated
  JPEG poll @700ms and is **LAN-only** (`YaverStore.swift:101` — tvOS has
  no relay). tvOS has NO WebKit, so the WebView route does not transfer.
  Real H.264 there needs a native Swift WebRTC pod. **Scope it, write the
  plan, do NOT half-land it.**
- **CarPlay video is forbidden by Apple** (`CPVoiceControlTemplate`
  category, `car-voice-coding.tsx:388`). Do NOT attempt video on car.
  Voice-only is correct and stays.
- watchOS/Wear have no viewer and are not video surfaces. Out of scope.
- Glass/AR-VR (`glass-*.tsx`) is RN-shared → inherits P2 for free. Verify
  it actually does; don't assume.

GATE P4: a written assessment in this file's Findings section. No
half-finished native ports.

### P5 — CLOSED-LOOP validation: prove the loop, don't assert it

Two boxes, two vantages. The point is a REAL receiver on the far side of
the network, not a mock.

**mac-mini** (alias `mac-mini`, 229aeb03, macOS 26.4.1) = origin: runs
`yaver serve` (the agent), builds, hosts the iOS Simulator / Android
emulator being captured, and runs this autorun.

**magara** (alias `linux-2`, 08182df8, 10.0.0.45, user `kivi`) = the phone
vantage. Verified 2026-07-17: **x86_64**, Ubuntu 20.04, Docker 24.0.5,
`redroid/redroid:13.0.0-latest` image present, container
`yaver-studio-redroid` already Up. It CAN run the real Yaver mobile APK as
a real Android receiver.

**Known gaps on magara — fix these first, they are the setup work:**
- `adb` is NOT installed → cannot drive redroid. Install it.
- `yaver` is NOT on `kivi`'s PATH (yet `yaver devices` lists magara online
  at `10.0.0.45:18080`, so an agent runs under some other user/unit).
  Resolve which, don't guess.
- **magara must be signed in as the same Yaver user** or none of the
  session/relay/QUIC paths authorize. It's SSH-only → use
  `yaver auth --headless` (short code + URL, approve from any browser).
  Verify with `yaver ping mac-mini` from magara: it checks reachability
  AND auth-as-same-user. A QUIC 401 means the box is logged out, not a
  network fault. Do NOT provision an API key — subscription/OAuth login
  only.
- Device row reports `runners unreachable`.
- redroid is **x86_64**. **Verify the Yaver Android APK actually ships an
  x86_64 ABI** — RN/Expo builds are frequently arm64-only, in which case
  the APK will not install in redroid. Check `abiFilters`/`splits` in
  `mobile/android/app/build.gradle` BEFORE assuming this vantage works. If
  it's arm-only, either add the ABI split or fall back to the
  headless-Chromium vantage (below) and say so.

**Harness (check in under `e2e/` or `scripts/`, runnable cold):**

1. **Offer-shape regression** (already proven at b9a9318ce — keep it):
   extract the injected viewer JS from the template string (NOTE: `tsc`
   does NOT typecheck it), serve via playwright `route.fulfill`, stub
   `/stream/webrtc/ice` + `/webrtc/offer`, assert the captured offer has
   `m=video`, `m=application`, and >0 `a=candidate:` lines. Run for BOTH
   the mobile viewer and `RemoteRuntimeViewer.tsx`.

2. **Live H.264, mini → magara.** Boot the app on the mini, start a
   `native-webrtc` session, receive on magara. Assert with
   `RTCPeerConnection.getStats()`: an `inbound-rtp` video report exists,
   `framesDecoded > 0` and RISING, and `codecId` resolves to H264. Assert
   the agent reported transport `webrtc-rtp-h264-v1` and NOT
   `webrtc-datachannel-jpeg-v1` — the JPEG fallback is the exact failure
   this whole task exists to kill, so a silent fallback MUST fail the gate,
   loudly.
   - Vantage A (preferred): the real Yaver APK in redroid on magara, driven
     by `adb`. Frames read back via `adb exec-out screencap -p`.
   - Vantage B: **Yaver mobile IS an RN project**, so `cd mobile && npm run
     web` renders the actual app (react-native-web) in a browser on magara,
     and browser automation drives the real screens — including
     `remote-runtime.tsx`. This exercises the same viewer code as the phone
     (the viewer is DOM inside a WebView on device; in RN-web it's plain
     DOM), so it validates the P2 shared core directly. Cheaper and far
     more debuggable than redroid — do this one first, then A.
   - Vantage C (fallback): headless Chromium on magara pointed straight at
     the session URL. Still a real second box across the network.
   Use the existing browser automation seams (`browser_*` / `selenium_*` MCP
   verbs, `e2e/` playwright) — do not invent a new driver.

3. **The money use case — the landing-video loop.** With the stream live:
   change the app's background color in source → agent rebuilds →
   relaunches → assert the RECEIVED frames' dominant color actually
   changed. Sample the decoded frame on the receiver, not the sender.
   This is the whole product claim in one test: *see the app change while
   you vibe-code it*, on a stack Hermes cannot serve. A passing version of
   this is worth more than every other test here.

4. **Per-stack matrix.** For each of rn (BOTH Hermes and webrtc lanes),
   flutter, kotlin, swift (mac-only — no Linux path, Apple SDK), web:
   record what actually passes, with evidence. Include the feedback loop:
   for streamed native stacks that means the viewer's `launch-feedback`
   → `feedback-launch-request` path (there is NO in-app native SDK — do
   not test for one); for rn/web/flutter it means the real in-app SDK.
   **An honest matrix with failures beats a green one that lies.** If a
   cell can't be tested on this hardware, mark it untested — never green.

GATE P5: harness runs green from a cold start on the mini, with magara as
receiver — or the matrix honestly records which cells fail and why, with
the reason.

## Out of scope (do not start these)
- Writing native Kotlin/Swift feedback SDKs (weeks; separate effort).
- Extracting `yaver-todo-*` to GitHub repos — blocked on the human
  creating the `yaver` org (org creation is web-UI only; no API path).
- `react-native-webrtc`.
- Enabling relay TURN in production (`--turn-port`) — config + deploy
  decision, needs a human.
- Any deploy.

## Findings
(Append verified findings here as you go. State what you PROVED and how,
and what you could not verify. Do not write "should work".)
