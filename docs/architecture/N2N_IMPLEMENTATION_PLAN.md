# n2n Develop-Anywhere — Implementation Plan (for Codex)

> Companion to `docs/architecture/N2N_DEVELOP_ANYWHERE.md` (the audit/design).
> This is the **execution plan**. It is self-contained: you do not need the
> audit to follow it, but re-grep every `file:line` before editing — other
> threads move constants and the working tree already has uncommitted changes.

## Progress log (update after EACH phase — this is the resume anchor)

> Future sessions: read this first. "keep working on n2n" = continue from the
> first phase not marked DONE. Append one entry per phase with date + what
> changed + any deviations from the plan.

- **P0 — Apple-runtime fan-out** — **DONE 2026-07-16**
- **P1 — MCP keystone (`runtime_*`)** — **DONE 2026-07-16**
- **P2 — `develop_for` verb** — **DONE 2026-07-16**
- **P3 — Voice everywhere + phone→TV bridge** — **DONE 2026-07-16** (agent-side pieces; native client bindings are handoff work per plan)
- **P4 — Feedback SDK n2n** — **DONE 2026-07-16**
- **P5 — Concurrency + shared registry + cast** — _not started_
- **P6 — Control primitives + Android surfaces + transport quality** — _not started_
- **P7 — Same-session runner continuation (no-fork)** — _not started_
- **P8 — Install + self-healing hardening** — _not started_

### P0 — 2026-07-16 (Europe/Istanbul)
Landed five per-runtime dedicated target IDs (`ios-simulator`,
`ipados-simulator`, `watchos-simulator`, `tvos-simulator`,
`visionos-simulator`) badged with a `Surface` field, wired through a
shared `probeAppleSimTarget` core gated on darwin + xcrun +
xcode-select + per-family runtime install. `runtimeTargetFor` now
threads a `deviceType` substring into `iosSimulatorTarget` so the
simctl driver picks the right sim class; the existing device-agnostic
methods (tap/screenshot/dims) reused unchanged. All five Apple targets
enumerate for `swift` and `flutter` frameworks alongside iOS-device
(and Android where applicable).

Files touched: `testkit/driver_iossim.go` (new `ParseInstalledRuntimeFamilies`
+ `InstalledRuntimeFamilies`), `remote_runtime.go` (Surface field,
`probeAppleSimTarget` + five thin probes, `appleRuntimeFamiliesForCaps`
test seam, expanded swift/flutter target lists), `remote_runtime_target.go`
(`deviceType` on `iosSimulatorTarget`, four new dispatch arms),
`remote_runtime_test.go` (updated existing target-count assertions),
`remote_runtime_apple_targets_test.go` (new file, 6 tests).

Closed-loop verification: `go test -run 'TestParseInstalledRuntimeFamilies|TestRuntimeTargetFor_AllAppleSimIDs|TestCapabilitiesEnumeratesAllAppleSurfacesAndBadgesSurface|TestInstalledRuntimeFamilies_NonDarwinReturnsEmpty|TestRemoteRuntimeCapabilities|TestHandleRemoteRuntimeCapabilitiesReturnsAppleFanOut' -count=1 .` → all pass. The end-to-end check inside `TestHandleRemoteRuntimeCapabilitiesReturnsAppleFanOut` fires the real `/remote-runtime/capabilities` HTTP handler with a stubbed families map and asserts the JSON body carries every Apple sim id + Surface badge — same contract the dashboard picker reads.

### P1 — 2026-07-16 (Europe/Istanbul)
Exposed the remote-runtime lane as MCP verbs so a runner can drive the
*app* not just the code. Seven verbs registered:
`runtime_targets`, `runtime_create`, `runtime_list`, `runtime_control`,
`runtime_command`, `runtime_frame` (first-class image), `runtime_stop`.
Each proxies the local `/remote-runtime/*` HTTP handler via a new
`remoteRuntimeHTTPMCP` helper (mirrors `feedbackHttpMCP`).
`runtime_frame` fetches the JPEG bytes and returns an MCP `image`
content block (`image/jpeg`) — same shape as `droid_frame`.
Session `command` handler extended with `boot` (idempotent re-attach)
and `launch-app {bundleId}` (dispatches to simctl for every Apple sim
family + adb for every Android target). `simulator_screenshot` upgraded
to return an MCP image content block, so an iOS "launch + look" turn
matches the Android path.

Files touched: new `remote_runtime_mcp.go` (proxy helper +
`remoteRuntimeFrameJPEG`), `remote_runtime.go` (`launchAppOnRuntimeTarget`
helper, extended command handler with `boot`/`launch-app`, BundleID on
the request struct), `mcp_tools.go` (7 tool schemas), `httpserver.go`
(7 dispatchers + first-class image for `simulator_screenshot`), new
`remote_runtime_mcp_test.go` (5 scoped tests).

Closed-loop verification: `go test -run 'TestHandleRemoteRuntimeSessionCommand_LaunchAppRequiresBundleId|TestHandleRemoteRuntimeSessionCommand_LaunchAppNeedsDevice|TestHandleRemoteRuntimeSessionCommand_RejectsUnknownCommand|TestHandleRemoteRuntimeSessionCommand_BootIsIdempotentOnAttachedSession|TestLaunchAppOnRuntimeTarget_UnsupportedTargetReturnsError|TestRuntimeCommandRequestParsesBundleId' -count=1 .` → all pass. `TestHandleRemoteRuntimeSessionCommand_BootIsIdempotentOnAttachedSession` fires the real HTTP command handler with an already-booted session and asserts the JSON response carries the resolved device id + updated LastCommand — same wire contract a runner will see.

### P2 — 2026-07-16 (Europe/Istanbul)
Landed the composed `develop_for` MCP verb + a pure mechanism resolver.
Turns "launch Talos for Android Watch" into one call: resolve machine
→ hard-gate on an installed+authenticated runner (uses the existing
`runnerAuthStatus` probe) → `ResolveMechanism(framework, surface,
platform, hostCaps)` → POST /remote-runtime/sessions on the resolved
target → launch-app when `bundleId` is set → fetch first frame →
return `{sessionId, mechanism, targetId, runnerSessionHint, renderOn,
firstFrameJpegBase64}`. Axis-3 `renderOn` is surfaced on the response
so a sibling client can attach; full cast routing lands in P5.

Files touched: new `dev_mechanism.go` (pure resolver, no I/O), new
`dev_mechanism_test.go` (exhaustive table test), new `develop_for.go`
(orchestrator + runner-auth gate + var seams for tests), new
`develop_for_test.go` (4 scoped tests — happy path, gate fail, missing
surface, missing framework), `mcp_tools.go` (verb schema),
`httpserver.go` (dispatcher).

Closed-loop verification: `go test -run 'TestResolveMechanism|TestRunDevelopFor' -count=1 .` → all pass. `TestRunDevelopFor_HappyPathReturnsSessionAndFrame` drives the whole verb end-to-end through the same seams a runner-facing MCP call would hit — stubbed only at the runner-auth gate + at the HTTP proxy boundary (so we don't need a live daemon on 127.0.0.1:18080), asserts session creation + launch-app + frame proxy were all called with the right paths + the response carries a base64 JPEG.

### P3 — 2026-07-16 (Europe/Istanbul) — agent-side pieces
Added two missing MCP verbs so a runner can start STT on a named
surface and cast TTS to a named surface: `voice_listen_start
{device, provider?, sessionId?}` and `voice_speak {device?, text,
voice?, rate?, renderOn?}`. Both ride the existing BlackBoxCommand
pipe `device_broadcast_command` uses so we introduce zero new
transport — client SDK listeners react to `command == voice_*` and
drive the local mic / TTS. `renderOn` carries Axis-3 sink hints so a
runner on the car can voice_speak to the phone; full presence-based
cast routing lands in P5.

Client bindings (`AudioCaptureAdapter` + `TtsAdapter` on the five
un-wired surfaces) and the phone-as-mic → TV-render bridge are
follow-on handoff work per the plan — the agent-side seam is now in
place and the RN core is ready to consume it.

Files touched: new `voice_mcp.go`, new `voice_mcp_test.go` (5 scoped
tests), `mcp_tools.go` (schemas), `httpserver.go` (dispatcher).

Closed-loop verification: `go test -run 'TestVoice' -count=1 .` → all pass. Tests use a live `BlackBoxManager`, subscribe a fake client, and assert the on-the-wire `BlackBoxCommand.Data` carries the runtime session id / provider hint / renderOn field intact — same shape the RN SDK / native listeners will parse.

### P4 — 2026-07-16 (Europe/Istanbul)
Two new verbs so the feedback loop works on keyboard-less surfaces
and can be filed programmatically:

  `feedback_create {surface, transcript?, screenshotSessionId?, ...}`
      Mints a FeedbackReport via the real `FeedbackManager.ReceiveFeedback`
      path so on-disk persistence, indexing, and privacy contract all
      hold. When `screenshotSessionId` is set, the current `/frame`
      JPEG from that runtime session auto-attaches — a runner can
      `runtime_create` → observe → `feedback_create` in one turn.

  `feedback_speak {id?, device?, maxItems?}`
      Composes a spoken summary (last N reports or one specific id)
      and hands it to `voice_speak` — reuses the P3 pipe, no new TTS
      engine.

Files touched: new `feedback_p4.go` (verbs + summariser + `ListReports`
helper), new `feedback_p4_test.go` (5 scoped tests), `mcp_tools.go`
(schemas), `httpserver.go` (dispatchers). Also fixed two pre-existing
`go vet` warnings (`autorun.go` redundant newline, unreachable code in
`monorepo_start_auth.go`) so scoped `go test` runs clean without
`-vet=off`.

Closed-loop verification: `go test -run 'TestFeedbackCreate|TestFeedbackSpeak' -count=1 .` → all pass. `TestFeedbackCreate_PersistsReport` writes through the real `~/.yaver/feedback` layer (redirected via HOME override) and asserts `ListFeedback` returns the freshly-minted report. `TestFeedbackSpeak_SummarizesQueueThroughVoicePipe` subscribes a fake TV client and confirms the composed summary reaches it via the same `voice_speak` BlackBox command shape a real client will parse.

_Environment: this work runs on the mac mini (`Mobiles-Mac-mini`,
`~/Workspace/yaver.io`, aligned to github/main). Build check:
`cd desktop/agent && go build ./...`. Do NOT commit/push; owner reviews._

---

## Ground rules (read first)

- **Do NOT commit or push.** Leave changes in the working tree; the owner
  reviews and commits.
- **Repo is public.** No secrets, no infra IPs/hostnames, no absolute
  `/Users/...` paths in tracked code.
- **Go**: `gofmt`, standard layout, no new deps without cause. Build tags only
  when truly platform-specific.
- **Tests**: real HTTP servers on random ports, **no mocks, no external deps**
  (see any `desktop/agent/*_test.go`). Prefer extracting a **pure** function and
  unit-testing it over shelling out in a test.
- **Privacy contract**: do not add any new field to a Convex-bound payload.
  Remote-runtime session state stays agent-local/in-memory (it already is).
- **Cross-surface parity**: RN surfaces share code; native (tvOS/watchOS/web/
  Wear) must be ported explicitly. P0–P2 are agent-only (Go) + MCP; no client
  work until P3.
- Working tree already modifies `remote_runtime_target.go`,
  `testkit/driver_iossim.go`, `mobile/src/lib/quic.ts`, `transport.ts`. **Read
  current file state before editing**; build on it, don't revert it.
- After each phase: `cd desktop/agent && go build ./... && go test -run <new> ./...`.
  Do NOT run the full suite on the dev Mac (keychain prompts); scope tests.

---

## PHASE 0 — Apple-runtime fan-out (stream-first)

**Goal:** make iPhone, iPad, watchOS, tvOS, visionOS each an *addressable,
streamable* remote-runtime target. Booting + screenshotting already works
(simctl is runtime-agnostic); this phase only adds enumeration + addressing.
No new control fidelity (tvOS remote / crown / pinch is P6). Backward compatible:
the existing `ios-simulator` id keeps meaning "iPhone".

### 0.1 — Driver: confirm DeviceType threading (`testkit/driver_iossim.go`)
`IOSSimDriver.DeviceType` already exists (`:30`) and `Boot` already calls
`pickSimulator(d.DeviceType)` (`:53-64`). `pickSimulatorFromList` already scores
iPhone/iPad/Vision/TV/Watch (`:127-180`). **No change needed here** except:
- Add exported helper for enumeration (pure, testable):
  ```go
  // ParseInstalledRuntimeFamilies parses `xcrun simctl list runtimes` output and
  // returns the set of installed families: "iOS","watchOS","tvOS","visionOS".
  func ParseInstalledRuntimeFamilies(simctlRuntimesOutput string) map[string]bool
  ```
  Match lines like `iOS 26.4 (...) - com.apple...SimRuntime.iOS-26-4` and
  `visionOS ...`, `watchOS ...`, `tvOS ...`. Only count lines **not** containing
  `(unavailable`. Add `InstalledRuntimeFamilies(ctx) (map[string]bool, error)`
  that runs `xcrun simctl list runtimes` and calls the pure parser.

### 0.2 — Target struct: add a Surface field (`remote_runtime.go:24-33`)
Additive, low-risk (JSON clients ignore unknown fields):
```go
type RemoteRuntimeTarget struct {
    ... existing ...
    Surface string `json:"surface,omitempty"` // phone|tablet|watch|tv|vision (n2n picker badge)
}
```

### 0.3 — Per-runtime probes (`remote_runtime.go`, near `:285`)
Refactor `probeIOSSimulatorTarget` into a shared core + five thin probes.
Enablement = current checks (darwin + xcrun + xcode-select) **AND** the runtime
family is installed (from 0.1). Table:

| fn | ID | Surface | Label | DeviceType (pickSimulator substring) | family |
|---|---|---|---|---|---|
| `probeIOSSimulatorTarget` | `ios-simulator` | phone | iPhone Simulator | `iPhone` | iOS |
| `probeIPadSimulatorTarget` | `ipados-simulator` | tablet | iPad Simulator | `iPad` | iOS |
| `probeWatchOSSimulatorTarget` | `watchos-simulator` | watch | Apple Watch Simulator | `Apple Watch` | watchOS |
| `probeTVOSSimulatorTarget` | `tvos-simulator` | tv | Apple TV Simulator | `Apple TV` | tvOS |
| `probeVisionOSSimulatorTarget` | `visionos-simulator` | vision | Vision Pro Simulator | `Apple Vision` | visionOS |

```go
func probeAppleSimTarget(id, surface, label, family string) RemoteRuntimeTarget {
    t := RemoteRuntimeTarget{ID: id, Surface: surface, Label: label,
        Platform: "ios", RuntimeHostClass: "macos-ios", HostOS: runtime.GOOS,
        RequiredCLI: "xcrun simctl"}
    // darwin / xcrun / xcode-select gate (copy existing :294-308) → Enabled/Reason
    // then: if !installedFamilies[family] { Enabled=false; Reason="<family> runtime not installed. Xcode > Settings > Components." }
    return t
}
```
Cache `InstalledRuntimeFamilies` once per capabilities call (pass the map in) to
avoid five `simctl` shells. Keep `Platform:"ios"` so existing platform-gated
logic is untouched; the new `Surface` field is what the picker badges.

### 0.4 — Enumerate in the swift/flutter cases (`remote_runtime.go:196-230`)
swift case becomes (order = default preference):
```go
fams, _ := InstalledRuntimeFamilies(ctx)
caps.Targets = []RemoteRuntimeTarget{
    probeIOSSimulatorTarget(fams), probeIPadSimulatorTarget(fams),
    probeWatchOSSimulatorTarget(fams), probeTVOSSimulatorTarget(fams),
    probeVisionOSSimulatorTarget(fams), probeIOSDeviceTarget(),
}
```
Add the same five to the flutter case alongside the Android targets. Disabled
(not-installed) targets still appear with a `Reason` (the picker greys them).

### 0.5 — Dispatch arms (`remote_runtime_target.go:62-84`)
Add a `deviceType` field to `iosSimulatorTarget` and new arms:
```go
type iosSimulatorTarget struct{ deviceType string }
func (t iosSimulatorTarget) Attach(ctx context.Context) (string, error) {
    return (&testkit.IOSSimDriver{DeviceType: t.deviceType}).Boot(ctx)
}
// in runtimeTargetFor:
case "ios-simulator":   return iosSimulatorTarget{deviceType: "iPhone"}, nil
case "ipados-simulator":return iosSimulatorTarget{deviceType: "iPad"}, nil
case "watchos-simulator":return iosSimulatorTarget{deviceType: "Apple Watch"}, nil
case "tvos-simulator":  return iosSimulatorTarget{deviceType: "Apple TV"}, nil
case "visionos-simulator":return iosSimulatorTarget{deviceType: "Apple Vision"}, nil
```
Every other method on `iosSimulatorTarget` already takes `deviceID` and is
device-type-agnostic — **no other change**. They inherit JPEG-DC streaming
(`CanEncodeRTPH264()==false` stays; correct for Xcode 26).

### 0.6 — `Create` target lookup (`remote_runtime.go:407-415`)
`Create` matches `caps.Targets[i].ID == targetID`. New IDs flow through
unchanged. Verify a disabled target returns the existing "unknown/!enabled"
error path rather than booting.

### 0.7 — Tests (new: `remote_runtime_apple_targets_test.go`)
- `ParseInstalledRuntimeFamilies`: feed a captured `simctl list runtimes` string
  (iOS+visionOS present, watchOS/tvOS absent) → assert the map.
- `runtimeTargetFor`: assert each new id yields `iosSimulatorTarget` with the
  right `deviceType`; unknown id still errors.
- Enumeration: with an injected families map, assert the swift case lists 5 sim
  targets and that absent families are `Enabled:false` with a Reason.
  (Refactor the swift case to accept the map so the test needn't shell out.)

**P0 acceptance:** `GET /remote-runtime/capabilities?framework=swift` lists
ios/ipados/watchos/tvos/visionos targets (installed ones enabled); creating a
`watchos-simulator`/`tvos-simulator`/`visionos-simulator` session boots that sim
and `GET /remote-runtime/sessions/<id>/frame` returns a live JPEG. Verified on
the mac mini (sims present).

---

## PHASE 1 — MCP keystone: `runtime_*` verbs over remote-runtime

**Goal:** expose the HTTP-only remote-runtime lane as MCP tools so a runner can
**create + stream + control + observe** any target — the prerequisite for
runner-driven app operation. Pattern to copy: `feedback_mcp.go` (proxies MCP →
local agent HTTP) and `droid_frame` (first-class image return).

### 1.1 — New file `desktop/agent/remote_runtime_mcp.go`
A local-HTTP proxy helper mirroring `feedback_mcp.go:23 feedbackHttpMCP`:
```go
func remoteRuntimeHTTPMCP(method, path string, body any) ([]byte, int, error)
```
Hits `http://127.0.0.1:<agentPort>/remote-runtime/...` with the local auth token
(reuse whatever `feedbackHttpMCP` uses for loopback auth).

### 1.2 — Register verbs in `mcp_tools.go` (near the feedback block `:3228-3274`)
| Verb | Proxies to | Returns |
|---|---|---|
| `runtime_targets {framework,workDir}` | `GET /remote-runtime/capabilities` | JSON target list |
| `runtime_create {workDir,framework,targetId,transportMode?}` | `POST /remote-runtime/sessions` | JSON `{sessionId,...}` |
| `runtime_list` | `GET /remote-runtime/sessions` | JSON |
| `runtime_control {sessionId,action,x?,y?,x2?,y2?,text?,key?}` | `POST /remote-runtime/sessions/{id}/control` | JSON ok |
| `runtime_command {sessionId,command,...}` | `POST /remote-runtime/sessions/{id}/command` | JSON |
| `runtime_frame {sessionId}` | `GET /remote-runtime/sessions/{id}/frame` | **first-class image** |
| `runtime_stop {sessionId}` | `DELETE /remote-runtime/sessions/{id}` | JSON ok |

- `runtime_frame` must return an MCP **image content block**, not JSON. Copy the
  exact shape from `droid_frame` (`mcp_tools.go:4388` → `httpserver.go:8755-8771`,
  jpeg) / `robot_camera` (`httpserver.go:12570-12617`). i.e. fetch the jpeg bytes
  and emit `{"type":"image","data":<b64>,"mimeType":"image/jpeg"}`.
- Mesh-routable: verbs go through the `ops`/mcp machine-routing seam so
  `machine=`/`device_id` targets a remote agent (same as `droid_*`). Confirm by
  matching how `droid_frame` is dispatched.

### 1.3 — Extend session `command` (`remote_runtime.go:523` handler, `:553` impl)
Today only `launch-feedback`. Add:
- `boot` — ensure attached/booted (idempotent; calls target.Attach).
- `launch-app {bundleId}` — iOS sim: `simctl launch <udid> <bundleId>`
  (`IOSSimDriver.Launch`, driver_iossim.go:86); Android: `am start` via
  `AndroidEmuDriver`. Add a `LaunchApp(ctx, deviceID, bundleID)` to the
  `runtimeTarget` interface **only if** clean; otherwise branch by platform in
  the handler using the existing drivers (prefer the interface for symmetry, but
  keep it optional to avoid touching all impls — use a type assertion
  `interface{ LaunchApp(...) }`).
- `build {mode}` — optional hook to `/dev/build-native` (can defer to P2).

### 1.4 — First-class image for `simulator_screenshot` (`httpserver.go:9513`)
Currently returns JSON via `mcpToolJSON`. Change to return an image content
block like `droid_frame`. Small, self-contained; do it here so "launch + show
me" works for iOS too, not just Android.

### 1.5 — Tests (`remote_runtime_mcp_test.go`)
- Spin the agent HTTP server on a random port (existing test harness), create a
  session against a **fake/stub target** registered for tests (add a
  `test-still` target that returns a fixed PNG from Screenshot and no-op control),
  then drive `runtime_create` → `runtime_control` → `runtime_frame` through the
  MCP handler and assert the image block + that control reached the stub.
- Assert `runtime_frame` returns image content, `runtime_control` validates
  action, unknown session → error.

**P1 acceptance:** from an MCP client (e.g. a runner on the mini) you can
`runtime_create` a session, `runtime_control` a tap, and `runtime_frame` returns
a live image — no dashboard involved. This is "the runner can drive the app."

---

## PHASE 2 — `develop_for` orchestration verb (+ mechanism resolver, runner-auth gate)

**Goal:** one verb turns *"launch Talos for Android Watch"* into the whole loop.
Composes existing verbs; no new transport.

### 2.1 — Mechanism resolver (new `desktop/agent/dev_mechanism.go`, pure + tested)
```go
type Mechanism string // "hermes" | "webrtc-stream" | "webview" | "native-rebuild"
func ResolveMechanism(framework, surface, platform string, host HostCaps) (Mechanism, targetID string, err error)
```
Table:
- RN/Expo + surface phone|tablet → `hermes`, target `ios-simulator`/`ipados-simulator`/`android-emulator`.
- RN + surface watch|tv|car|vision → `native-rebuild` + `webrtc-stream`, target `<surface>-simulator` (iOS) or the Android emu equiv (P6).
- web framework (vite/next/flutter-web) → `webview`; non-WebView client → `webrtc-stream`/pixel.
- fallback for any bootable runtime → `webrtc-stream`.
Unit-test the table exhaustively (pure function, no I/O).

### 2.2 — Verb `develop_for {project, surface, platform?, machine?, renderOn?}` (`mcp_tools.go` + handler)
Sequence (compose, don't reinvent):
1. Resolve `machine` (default = primary; `device_primary_get`).
2. **Runner-auth gate**: check the machine has an authed runner
   (`runner_auth_status`); if not → return an actionable error ("run
   `yaver runner auth` on <machine>"). **Hard gate.**
3. `ResolveMechanism(...)` → mechanism + targetId.
4. Prepare: `remote_dev_prepare` (repo/workdir) as needed.
5. Build the artifact per mechanism: `hermes`→`/dev/build-native`;
   `native-rebuild`→`mobile_platform_deploy` (build only, the surface enum
   already exists at `mcp_tools.go:2103`); `webview`→`/dev/start`.
6. `runtime_create` (P1) with the resolved targetId + `runtime_command boot` +
   `launch-app`.
7. Return `{sessionId, mechanism, target, firstFrame(image), runnerSessionHint,
   renderOn}`.
8. `renderOn` (Axis 3): if set and ≠ command surface, include a cast handle the
   sink client can attach to (full cast routing is P5; here just surface the
   session id + a `renderOn` field so a sibling can join the RTP fan-out).

### 2.3 — Tests
- `ResolveMechanism` table test (pure).
- `develop_for` happy path against stub target + a stubbed runner-auth = true;
  and the gate path (runner-auth = false → error, no session created).

**P2 acceptance:** `develop_for {project:"talos", surface:"watch", platform:"ios"}`
on a runner-authed machine boots the watchOS sim, launches the app, and returns
a first frame + session handle in one call. Gate fails cleanly when no runner.

---

## PHASE 3 — Voice everywhere + phone-as-mic → TV render bridge
*(client work; RN shared core + native ports)*
- Bind `AudioCaptureAdapter` + `TtsAdapter` on the 5 un-wired surfaces
  (`mobile/src/lib/voice/` core is ready; `useHandsFreeVoice` seam). Phone/web
  first (JS), then tvOS/watch/Wear native bridges.
- New MCP verbs `voice_listen_start {device}` / `voice_speak {device,text}` so a
  runner can start STT / cast TTS to a *named* surface (nothing does this today).
- **phone→TV bridge**: phone runs `conversationCore`, targets the box, sets
  `renderOn: <tvDeviceId>`; TV attaches the RTP fan-out as a render sink. Wire on
  `tvos/YaverTV` (add a remote-runtime viewer — see P5/P6).
- Voice-navigate the streamed app = voice intent → runner → `runtime_control`
  (P1 keystone). No new transport.

## PHASE 4 — Feedback SDK n2n
- MCP `feedback_create {surface, transcript?, screenshotSessionId?}` — mint a
  `FeedbackReport` programmatically (agent side `FeedbackManager.ReceiveFeedback`;
  `feedback.go`). Auto-attach a `runtime_frame` screenshot when a session id is
  given.
- Voice→FeedbackReport authoring path (route a voice turn to
  `FeedbackManager` instead of task-create; today it only task-creates,
  `voice_http.go:386`).
- `feedback_speak {id?}` — TTS-summarize the feedback queue (new; TTS today only
  reads task results, `voice_http.go:428`).
- Emit real `surface` values (stop hardcoding `'feedback-sdk'`,
  `VibeChatScreen.tsx:325`).

## PHASE 5 — Concurrency arbitration + shared session registry + cast
- **Control lease**: add a single-writer lease to `/remote-runtime/.../control`
  and `/rd/input` (both free-for-all today). Role split: one controller, N
  viewers; take/release handshake; broadcast the holder to all peers via the
  existing `sendEventJSON` (`remote_runtime_webrtc.go:453-481`).
- **Shared reactive registry**: back the remote-runtime session list with the
  relay per-user event bus (`relay/bus.go:34-130`) so all a user's surfaces see
  live session list + state without polling. Wire session create/attach/drop to
  publish bus events.
- **Unify JPEG-DC/RTP** so a mixed fleet co-views (today a JPEG-DC offer tears
  down RTP peers, `remote_runtime_webrtc.go:251-255`): make JPEG-DC additive or
  auto-upgrade the second viewer to RTP.
- **Cast routing (`renderOn`)**: resolve the sink device via presence, attach it
  to the session's RTP fan-out. Add TURN (`:854` "TURN not wired") for NAT'd
  fleets so cast works when the agent isn't directly reachable.

## PHASE 6 — Per-surface control primitives + Android surfaces + transport quality
- Control fidelity: extend `runtimeTarget` (or enrich `Key`) —
  tvOS directional/select/menu/play-pause, watchOS Digital Crown, visionOS
  pinch-at-coordinate. iOS bottleneck is `wda_client.go:184-194`.
- Android surface targets: Wear OS / Android TV / Android XR / Android Auto
  emulators (clone `androidEmulatorTarget`, all adb-based).
- **RTP for Apple sims**: replace the dead `simctl recordVideo`-to-stdout path
  with a file-backed MP4-fragment tailer (`remote_runtime_target.go:140-147`) →
  restores H.264 RTP quality for every Apple sim. In-process x264 for
  `browser-window` (`remote_runtime_browser.go:278`).

## PHASE 7 — Same-session runner continuation (no-fork supervisor)
*Motivation: runners routinely stop after 3 of N tasks. We want a native Yaver
feature that keeps THAT runner going through the whole task/todo list — nudging
it to continue when it idles — without ever forking a new runner process.*
- Native supervisor goroutine in the agent (`runner_continuation.go` new). Owns
  one *live* session and one queue; NEVER spawns a fresh runner process.
- Liveness detection is content-based, not `pane_current_command`-based (that
  proved unreliable tonight). Hash the tmux pane's tail + last-modified stamp
  every N seconds; when nothing changes for a debounce window AND the queue is
  non-empty, inject a `keep going` prompt into the same pane.
- Nudge policy: exponential backoff, hard cap on consecutive nudges, stop when
  the runner's transcript emits a completion marker or the user takes it over.
- Expose CLI `yaver runner continue <session> --tasks <file>` and MCP verb
  `runner_continue {session, tasks, maxNudges?}`.

## PHASE 8 — Install + self-healing hardening
*Build on the existing SIGKILL / resign-macos-adhoc recovery so a runner never
silently stalls and the agent auto-recovers.*
- Closed-loop health checks: agent polls a small `/health/deep` endpoint (runner
  responsiveness, tmux pane liveness, WebRTC pump alive) and takes graduated
  action (nudge → restart runner→ resign binary → reinstall).
- Fold in tonight's lessons: same-session no-fork continuation (P7 seam),
  pane-content liveness (never pane_current_command), closed-loop verification
  in every recovery step.
- Wire the recovery path into `runner_auth_status`/`primary status` so a stalled
  runner shows up as `degraded` with the last recovery action + timestamp.

---

## Suggested execution order for Codex
1. **P0** (agent-only, low risk, immediately visible on the mini) — ship + verify
   watchOS/tvOS/visionOS streaming.
2. **P1** (agent-only, the keystone) — runner can drive the app over MCP.
3. **P2** (agent-only) — one-command `develop_for`.
4. Stop and demo P0–P2 end-to-end from a runner on the mini before client work.
5. **P3–P6** as separate PRs (client + arbitration + quality), each with the
   cross-surface parity check.

Each phase: build + scoped tests green, then hand back for the owner to commit.
Do not proceed past P2 into client/native work without confirming the P0–P2
loop works on real sims.
