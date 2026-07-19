# XREAL + Beam Pro rig — thin client on glass, Mac mini as build plane

> **Status:** deep audit / design-record (2026-07-19). **No feature code changed
> by this doc.** Grounded in a 4-way read-only audit of the tree at `main`
> (560e76317); every claim carries a `file:line`. Per CLAUDE.md: **the code is
> the source of truth — re-grep before acting on any line number here.**
>
> This documents a capability we want to *have*, not the default. It does not
> supersede or redesign `N2N_DEVELOP_ANYWHERE.md`; it is the concrete rig that
> falls out of it.

## The rig

XREAL AR glasses (tethered display) + XREAL Beam Pro 8G/256G (Android host) +
foldable BT keyboard. Mac mini at home is the build plane.

```
   XREAL glasses            Beam Pro 8G                    Mac mini
   ─────────────            ───────────                    ────────
   display only      ←USB-C─ Yaver mobile app              yaver serve
   (mirror today)            ├─ command surface            ├─ Metro + hermesc
                             ├─ wraps/steers runners       ├─ WebRTC / sim stream
                             ├─ HOSTS app-under-test  ←────┤ serves HBC bundle
                             └─ feedback capture      ─────→ receives feedback
```

The Beam Pro does **not** build. It is the surface you look through and the
device the app-under-test actually runs on. That split is the whole point, and
it maps onto the codebase better than expected.

---

## Verdict up top

**The rig you described mostly works today — but only in one of its two possible
shapes, and the codebase silently misleads you about the other.**

| Shape | What it means | Verdict |
|---|---|---|
| **A. Thin client** — Beam Pro focuses the mini as its active device; mini is agent *and* builder; runners run on the mini | Everything routes to one agent | ✅ **Works today, ~zero new code** |
| **B. Split brain** — runners run *on* the Beam Pro; mini used only as a build/stream service | Two agents, one session | ⚠️ **Needs building.** Template exists, is narrow, and one entry point is actively wrong |

Your latest framing ("Beam Pro only for wrapping agents and showing UI… does not
have to build hermes") lands on **Shape A**, which is the good news: the thing
you want is close to the default, not a new architecture.

The trap: `develop_for` accepts a `machine` parameter that makes Shape B *look*
supported. It is not — see the split-brain bug below.

---

## 1. Shape A — why it already works

The Hermes loop is **agent-pull, phone-initiated**, and the phone talks to
whatever agent is focused. Nothing in that path assumes co-location with the
phone.

1. Phone `POST ${baseUrl}/dev/build-native` — `mobile/src/components/DevPreview.tsx:316`,
   `mobile/app/(tabs)/hotreload.tsx:571`, natively `mobile/ios/Yaver/AppDelegate.swift:717`.
2. Agent builds locally: Metro at `desktop/agent/devserver_http.go:3153`
   (`cmd.Dir = workDir`), hermesc at `:3353`.
3. Phone `GET ${baseUrl}${bundleUrl}` — `DevPreview.tsx:370` → `loadAppIfChanged` `:371`.
4. Agent serves from local disk — `http.ServeFile` at `devserver_http.go:3869`,
   HMAC-gated by `verifyDevBundleSig` `:3838`.
5. Phone loads it into the Yaver container via the super-host bridge.

`baseUrl` is just the focused device — `mobile/src/_core/endpoints.ts:17`,
`mobile/src/lib/deviceAgentFetch.ts:45`, default port `18080`
(`mobile/src/_core/constants.ts:45`). Over relay it becomes
`https://public.yaver.io/d/<deviceId>/...` (`mobile/ios/Yaver/YaverBundleLoader.swift:211-212`).

**So: focus the mini in the device picker, and the Beam Pro gets mini-built
bundles today, on LAN or over relay.** No dispatch layer involved.

> Watch out: `YaverBundleLoader.swift:203-245` has non-trivial logic to preserve
> the `/d/<deviceId>` prefix when deriving the base URL. Stripping it caused the
> recurring `subdomain 'public' not registered` failure. Relevant because a
> roaming Beam Pro is *always* on the relay path.

### 1a. Third-party apps + feedback SDK on the Beam Pro — works, with a UX caveat

**CLAUDE.md is slightly wrong here and it matters for your plan.** It says the
SDK's `init()` and `ShakeDetector.start()` "silently no-op" inside the Yaver
container. The actual behavior is better — *dormant but wakeable*:

> "we run in HOST MODE: dormant by default (no shake detector, no auto BlackBox,
> no QuickActionIcon — Yaver's host shell owns those), but we DO register a
> DeviceEventEmitter listener so Yaver's overlay can flip the SDK live at
> runtime."
> — `sdk/feedback/react-native/src/YaverFeedback.ts:42-49`

Detection is `!!NativeModules.YaverInfo` (`:52`). Yaver's overlay dispatches
`yaverFeedback:startReport` into the guest bridge, which wakes the SDK and opens
the modal over the running guest UI. So a third-party RN app Hermes-loaded into
the Beam Pro **does** get the full feedback loop.

Feedback then posts to `${agentUrl}/feedback`
(`sdk/feedback/react-native/src/YaverFeedback.ts:370` via `P2PClient`;
web `sdk/feedback/web/src/YaverFeedback.ts:407`; flutter
`sdk/feedback/flutter/lib/src/p2p_client.dart:123`) → lands on the mini at
`desktop/agent/feedback_http.go:76` → `FeedbackManager.ReceiveFeedback`
(`feedback.go:188`). The fix loop from there is already hands-free
(`feedback_to_vibe.go:250`), with MCP verbs at `feedback_mcp.go`.

**The caveat is ergonomic, not architectural: the trigger is shake.** With the
Beam Pro clipped to a belt or sat on a desk driving glasses, shaking it is not a
gesture you will make. A non-shake trigger already exists —
`launch-feedback` over the remote-runtime events channel
(`desktop/agent/remote_runtime.go:729-761`, consumed at
`mobile/app/remote-runtime.tsx:259`, `mobile/src/lib/feedbackTrigger.ts:74`) —
but note what it actually is: a fire-and-forget **notification** with no ack and
no correlation id (`remote_runtime.go:746` sets `Status = "feedback-pending"`
and returns `"accepted"`). It is not an invocation. For a glasses rig the
keyboard/voice trigger is the missing ergonomic piece, not new transport.

---

## 2. Shape B — the split brain, and the bug that pretends it works

### 2a. `develop_for` accepts a `machine` it does not honor — **P1 correctness bug**

> Full write-up with remediation options: **`docs/develop-for-machine-split-brain.md`**.

`develop_for` is the P2 orchestration verb (`desktop/agent/develop_for.go`). It
takes `Machine string` (`:30`). Trace what that field does:

- `develop_for.go:120` — `developForRunnerAuthGate(req.Machine)` → gates on
  whether **the named remote machine** has an authed runner
  (`runnerAuthGateProbe` `:77`).
- `develop_for.go:124` — `currentHostCaps(ctx)` shells to **local** `adb` /
  `emulator` (`:189-198`) and reads **local** installed Apple runtimes.
- `develop_for.go:125` — `ResolveMechanism(framework, surface, platform, host)`
  where `host` is those local caps (`dev_mechanism.go:54`).
- `develop_for.go:142` — `developForRuntimeCall` = `remoteRuntimeHTTPMCP`, which
  is hardwired to `http://127.0.0.1:18080` (`remote_runtime_mcp.go:44`).

**So `develop_for {machine: "mac-mini"}` checks the mini's runner auth, then
resolves capabilities against the *caller's* hardware and boots the simulator on
*localhost*.** On a Beam Pro that means: gate passes (mini has runners), then
`ResolveMechanism` inspects an Android phone's toolchain and either errors or
picks the wrong target — while the operator believes they targeted the mini.

This is the single most misleading thing in the current tree for this rig. It is
a genuine bug independent of any XREAL work.

### 2b. The dispatch template that *does* work — and its four limits

`desktop/agent/remote_runtime_dispatch.go` is a real, working "front agent ≠
working agent" proxy, and it is the correct template:

- `pickBuilderForFramework` `:92-125` — policy hook. The comment at `:89-91`
  explicitly names itself the single expansion point.
- `dispatchCreateToBuilder` `:132` — creates the session on the builder, mints a
  local id `rr_proxy_<nanos>` (`:173`), stores `proxiedSession{BuilderURL,
  BuilderToken, RemoteID}` (`:39-44`).
- `forwardSessionRequest` `:210` — forwards `/webrtc/offer`, `/control`,
  `/command`, `/frame`, DELETE verbatim.
- Entry: `remote_runtime.go:494`.

Its design choices are good and worth preserving:

- **Signaling only.** `:12-16` — RTP media flows viewer↔Mac direct after ICE.
  The front agent never decodes a frame, never holds a Pion track, never appears
  as an ICE candidate. For a Beam Pro this is exactly right: it must not proxy
  video.
- **Explicit auth boundary.** `:226-232` — inbound `Authorization` is
  deliberately *not* forwarded; the builder is reached with its pairing token.
  This is the precedent to follow rather than re-litigate.

Four limits stand between it and this rig:

1. **iOS/Swift only.** `pickBuilderForFramework:97-101` triggers on
   `framework == "swift"`, or flutter + `ios-simulator`. Expo/RN never dispatch.
2. **Never consulted by the Hermes path.** `handleBuildNativeBundle`
   (`devserver_http.go:2561`) does not call it. The builder registry's only
   consumers are `remote_runtime.go:494` and `remote_runtime_dispatch.go:132`.
3. **URL+token, not deviceId.** `BuilderEntry{Alias,URL,Token,Platforms}`
   (`remote_builder.go:34`) in `~/.yaver/builders.json`. Paired via
   `yaver builder add <alias> <url>` (`remote_builder_cmd.go:49`), advertised via
   `yaver serve --builder-platforms=ios` (`main.go:2302`). **A roaming Beam Pro
   has no stable URL for a home mini.** Meanwhile `peer_proxy_http.go` and
   `agent_mesh_remote.go` already solve exactly this with deviceId + relay
   traversal. The two seams have not converged.
4. **Static single default.** `reg.Default` (`:113`) — no mesh awareness, no
   scoring, no per-request choice from the client.

### 2c. The build itself cannot be delegated at all

Every consumer of `workDir` in the build path is local-filesystem or
local-subprocess: `cmd.Dir = workDir` (`devserver_http.go:3153`, `:3354`),
`filepath.Join(workDir, ...)` for build dir / bundle / assets (`:2782-2786`,
`:3844`, `:3932`), `os.Stat` + `os.ReadFile` for framework detection (`:3873-3907`).

**The single line that enforces "server must be builder" is the `os.Stat` +
`http.ServeFile` pair at `devserver_http.go:3853`/`:3869`.** Any distributed-build
transport would need to preserve `verifyDevBundleSig` (`:3838`) and the
`X-Yaver-Bundle-Metadata` header (`:3862-3868`).

Two notes for whoever eventually touches this:

- **`devserver_pull.go` is misleadingly named.** It is pre-build *git* update
  (`:167` `rev-parse --is-inside-work-tree`, `:171` upstream check), not bundle
  pull. It will actively mislead anyone auditing a distributed-build seam. Its
  cloud-placement hook (`:139`) only *suppresses* the pull; it never relocates
  the build.
- **`hbc_cache.go` is the right substrate and is 90% there.** Content-addressed
  Hermes bytecode cache whose key already encodes every input that affects output
  — source hash, hermesc version, opt level, target arch, sourcemap flag
  (`hbc_cache.go:22-25`, `:74-99`). It has **no remote lane** (no http/fetch in
  the file). A remote builder filling this cache is a much smaller change than a
  general remote-build RPC, and the safety properties (SP1–SP5, `:20-30`) are
  already specified.

For scale: a full rebuild is **12–27 s**, and the HMR layers get the common case
to **~300 ms** (`docs/hermes-secondary-reload-optimization.md:28`, `:35`). The
mini is worth using because it is fast, not because the Beam Pro is incapable —
Metro/hermesc *can* run on Android under proot (`sandbox_proot.go:229`
`sandboxWrapBuildCmd`, wired at `devserver_http.go:3172`). That keeps Shape A a
graceful-degradation story rather than a hard dependency.

---

## 3. The client side is further along than the server side

This is the pleasant surprise, and it means the mobile work for either shape is
small.

`mobile/src/lib/connectionManager.ts` is **already a multi-device pool** —
`Map<deviceId, QuicClient>` (`:45`), uncapped (`:24-27`), every signed-in device
holding a live QUIC connection in parallel. `focusedId` (`:46`) is documented as
"just a UI affordance" (`:17`). The header states the motivating requirement
outright (`:12-13`): *"push a long-running task to box B while you kept watching
box A."* `clientFor(deviceId)` (`:102`) reaches any pooled device.

The routing primitive is `peerEndpoint` (`mobile/src/lib/quic.ts:1477-1484`):
collapses to direct when the target is attached, else `/peer/<id>`
(server side `desktop/agent/peer_proxy_http.go`, registered `httpserver.go:1142`).

Per-feature targeting today:

| Feature | Targeting | Evidence |
|---|---|---|
| Dev server, runner-auth, opencode, storage, process monitor | **per-device** | `quic.ts:1744,1774,2656,2666` |
| Feedback task | **per-device** | `quic.ts:1928` `peerEndpoint(deviceId,"/tasks")` |
| Main task send | **focused only** | `quic.ts:1890` — hardcoded `${this.baseUrl}/tasks` |
| Remote runtime / WebRTC | **focused only** | `quic.ts:2989,2997,3009` |

Note the main task send is hardcoded to focused while the per-device version
already exists ~40 lines below it. That is a small change, not a project.

---

## 4. "Automatic understanding of this setup" — the honest state

You asked for the rig to be *recognized*, not configured. Three separate things
block that, in increasing order of difficulty.

### 4a. There is no device role, and capabilities die at the agent boundary

**No ROLE field exists.** `grep -n "role" backend/convex/schema.ts` on device
fields returns zero. `yaver primary` is *not* a role — it is one scalar per user
(`schema.ts:860` `primaryDeviceId` on `userSettings`, sibling
`secondaryDeviceId:866`), documented as an auto-connect preference
(`schema.ts:855-857`), read via hardcoded slot helpers (`primary_cmd.go:682,722-727`).

The agent **already computes** exactly what a scheduler would need.
`MachineCapabilities` (`console_machines.go:60-75`) carries `SupportsIOS`,
`SupportsAndroid`, `SupportsTestFlight`, `SupportsPlayStore`, `SupportsDocker`,
`SupportsLocalLLM`, `LowPower`, `MaxTaskSlots`. `detectMachineCapabilities()`
(`:207-264`) does **real probing** — `toolLooksInstalled("xcrun")` for TestFlight
(`:232`), `java`/`gradle`/`adb` for Android (`:234-235`), per-runner `LookPath`
(`:212-228`).

**None of it reaches Convex.** The heartbeat mutation validator
(`backend/convex/devices.ts:971-1010`) accepts only `runners`,
`installedRunnerIds`, `hardwareProfile`, `deviceClass`, `edgeProfile`, etc.; the
agent sends `installedRunnerIds` (`auth.go:1744`) and `publishCapabilities`
(`auth.go:1802`) and nothing more.

Worse, what *is* advertised is fabricated. `publish_worker.go:551-559` is a
hardcoded `switch runtime.GOOS`:

```go
case "darwin": return []string{"ios","android","tvos","watchos","visionos",...}
case "linux":  return []string{"android","android-tv","wear-os","android-xr"}
```

**Every macOS box claims iOS capability whether or not Xcode is installed.** No
probe involved — even though `detectMachineCapabilities` next door does probe.

### 4b. Cloud placement can put an iOS build on a Linux box

`backend/convex/taskPlacement.ts` has a real lane picker (`decidePlacement`
`:392-514`, lanes at `:28-36`). But machine selection is a linear first-match
`.find()` with no scoring (`candidateOwnedDevice` `:354-375`), and the build test
is only:

```ts
return Array.isArray(d.publishCapabilities) && d.publishCapabilities.length > 0;
```

`taskPlacement.ts:373` — **it never checks that the capability matches the target
platform.** An iOS build happily selects a Linux box advertising `["android",...]`.

Meanwhile `desktop/agent/project_manifest.go:1016-1045`
(`projectRuntimeMachineScore`) already implements a sensible weighted scorer over
the rich capability struct — `+40` primary, `+20` not-shared, `+10` not-LowPower,
`+MaxTaskSlots`, `+20` role `build-mac` when `SupportsIOS` — with human-readable
justification (`projectRuntimeReason` `:1055-1070`). It runs **agent-side, on data
the backend never sees.**

**Highest-leverage single change in this whole audit:** widen the heartbeat
validator to carry `MachineCapabilities`, then let `candidateOwnedDevice` use the
scorer that already exists in Go. That fixes the iOS-on-Linux hazard and is the
prerequisite for any automatic role realization.

For completeness: there are **five** independent placement paths —
`taskPlacement.ts`, `project_manifest.go`, `autorun_placement.go`,
`devices.ts:1891` `recommendTaskPlacement`, and `scheduler.go` (which is a cron
scheduler, not a placement engine — it passes `Machine` straight through,
`scheduler.go:31`). Unifying them is a larger question than this rig needs.

Beam-Pro-specific footnote: `agent_mesh.go:355` grants `+35` for
`RAM >= 24GB`. An 8 GB Beam Pro simply doesn't earn that bonus — it is a scoring
weight, not a gate, so it correctly de-prioritizes the phone for heavy work
without excluding it.

### 4c. XREAL display is a mirror, not a surface

`grep -rn -i 'xreal|nreal'` finds only web and docs references — `web/app/spatial/page.tsx:284`
(names XReal Air explicitly), `web/app/spatial/lib/keyboardShortcuts.ts:9`,
`surfaceDetect.ts:88` (`android-trio` = phone + glasses + keyboard).

**There is no `Presentation` / `DisplayManager` / DisplayPort code anywhere in
`mobile/`.** Verified: the only hits are `Modal presentationStyle`, unrelated. So
the glasses mirror the Beam Pro's portrait screen; the real 3-pane `android-trio`
layout lives in the **web** `/spatial` route, not the native app.

This is the biggest *experiential* gap for the rig, and it is independent of
every dispatch question above.

Related, and worth knowing before designing anything spatial: HTML-in-VR is
impossible in `immersive-vr` sessions — `dom-overlay` is granted only for
`immersive-ar` in shipping browsers. `/spatial`'s div+xterm works as a **2D
window** in Quest Browser / Vision Pro Safari; a true immersive upgrade needs the
WebGL path (`@react-three/uikit`). For a *tethered* XREAL rig this mostly doesn't
bite — the glasses are a display, not a headset runtime — but it caps how far the
"AR" framing can go.

---

## 5. What the glass surfaces actually are

Four unrelated tracks share the word "glass". Worth stating plainly, because the
naming actively misleads:

| Track | What | Status |
|---|---|---|
| `mobile/app/glass-terminal.tsx` (1564 ln), `glass-workspace.tsx` (713 ln) | Full RN screens tuned for display glasses — **this is your rig's surface** | EXISTS |
| `?surface=glass` query param | Cosmetic skin on exactly 2 screens (`appletv-remote.tsx:39`, `car-voice-coding.tsx:111`) | Skin only |
| `web/app/spatial/` | The real WebXR/headset surface, `Surface` union incl. `android-trio` (`surfaceDetect.ts:20`) | EXISTS, most built-out |
| `desktop/agent/glass_hud.go` | Text-HUD push for Even G1/G2-class glasses, ~60 chars × 6 lines (`:58-60`) | EXISTS |

`glass-terminal` and `glass-workspace` both share `DeviceContext`
(`glass-terminal.tsx:126`, `glass-workspace.tsx:80,311`) but **neither imports
AuthContext** — auth arrives implicitly via `quicClient`. The tiling engine is
real, not a skin: `mobile/src/components/workspace/YaverWorkspace.tsx:24`
(`WorkspaceLayout` 1x1…2x3), `layoutGrid()` `:119`, Cmd-1..6 / Cmd-J/K BT-keyboard
nav via `useWorkspaceKeyboard.ts`.

Known gaps in that surface: `glass-workspace`'s **shell pane is read-only**
(`glass-workspace.tsx:23-25`) — typing into tmux means bouncing to
`glass-terminal`; no landscape grid (`:21` defers it, and the deferral comment
literally names Beam Pro users); no pane resize (`YaverWorkspace.tsx:11`).

Good news vs. the older audit: **mobile now does call `/tmux/*`** —
`mobile/src/lib/quic.ts:4093` (`sessions`), `:4107` (`adopt`), `:4122` (`detach`),
`:4136` (`input`).

---

## 6. Corrections this audit forces

Several tracked docs and memories are stale in ways that would misdirect this
work. Listing them so they get fixed rather than re-discovered:

| Claim | Where | Reality |
|---|---|---|
| n2n is "design / no feature code" | `N2N_DEVELOP_ANYWHERE.md:3-6` | **Stale.** P1–P4 largely landed: `runtime_targets/create/control/frame` (`mcp_tools.go:3363-3427`, `httpserver.go:13542-13673`), `develop_for` (`develop_for.go`, `httpserver.go:13699`), `voice_listen_start`/`voice_speak` (`:13709`,`:13713`), `feedback_p4.go` |
| Feedback SDK "silently no-ops" inside container | `CLAUDE.md` | **Imprecise.** Dormant-but-wakeable via DeviceEventEmitter — `YaverFeedback.ts:42-49` |
| mobile never calls `/tmux/*` | `docs/phone-dev-environment-audit.md:43,109` | **Stale.** `quic.ts:4093-4136` |
| `BEAM_PRO_DEV.md` "lives in repo" | project memory | **Does not exist.** `scripts/setup-remote-dev.sh` does |
| Beam Pro = 6 GB | project memory | User's unit is **8 GB / 256 GB** |
| Android BundleLoader missing | `project_android_bundleloader_missing` | **Stale** per `N2N_DEVELOP_ANYWHERE.md:225-226` |
| `devserver_pull.go` pulls bundles | filename implies it | It is pre-build **git** update (`:167-171`) |

---

## 7. If this is ever built — the order that falls out

Not a plan to execute; the dependency order the audit implies. Nothing here is
required for Shape A.

- **Fix `develop_for`'s `machine` split-brain first** (§2a). It is a correctness
  bug today, it misleads anyone exploring this rig, and it is independent of
  everything else. Either honor `machine` end-to-end (resolve caps *on* the
  target, route `developForRuntimeCall` through `peerEndpoint`/`remoteAgentJSONForDevice`)
  or reject a non-local `machine` loudly.
- **Carry `MachineCapabilities` on the heartbeat** (§4a). Unblocks honest role
  realization and lets `candidateOwnedDevice` use the scorer that already exists
  (`project_manifest.go:1016`). Also fixes the fabricated `publish_worker.go:551-559`
  GOOS switch and the iOS-on-Linux hazard (`taskPlacement.ts:373`).
- **Converge the builder registry onto deviceId + relay** (§2b limit 3). A
  roaming Beam Pro cannot use a URL-pinned builder. `peer_proxy_http.go` +
  `agent_mesh_remote.go` already do deviceId, relay traversal, candidate scoring
  (`agent_mesh_remote.go:44-60,126`) and health persistence (`:90-125`).
- **Only then** consider remote Hermes builds — and prefer *filling `hbc_cache`
  from a remote builder* over a general build RPC (§2c), preserving
  `verifyDevBundleSig` and `X-Yaver-Bundle-Metadata`.
- **Non-shake feedback trigger** for keyboard-less/glasses use (§1a) — and if
  `launch-feedback` is leaned on, give it an ack + correlation id, because today
  it is fire-and-forget (`remote_runtime.go:729-761`).
- **Android `Presentation` external-display surface** (§4c) — the largest
  experiential win, wholly independent of the dispatch stack.

## 8. Open questions worth deciding before code

1. **Where do runners actually run?** Shape A (mini) is free today. Shape B
   (Beam Pro, via proot/Termux genuine CLI) is the only sub-path that forces the
   split-brain work. Note the compliance constraint: subscription tokens are
   **CLI-only** — a plan token may only be used by the genuine CLI, never the
   in-app Hermes loop (`docs/phone-dev-environment-audit.md:8-23`), so
   "runners on the Beam Pro" means Android proot `claude`/`codex`, not an in-app
   reimplementation.
2. **Auth model for cross-machine sessions.** The runner-agent-session HTTP
   surface is owner-only and unsandboxed *by explicit design* —
   `runner_agent_session_http.go:13-16`: "a guest tier here would mean arbitrary
   code execution on someone else's box." Delegating sessions widens exactly that
   blast radius. `forwardSessionRequest:226-232` already set the precedent
   (drop caller auth, use pairing token); follow it rather than re-open it.
3. **Does `AgentSession` ever need a device field?** It has none today
   (`runner_agent_session.go:68-83`); binding is by local process (`:261`,`:388`).
   Cloud deferral *parks* rather than delegates (`:425-467`). Shape A never needs
   this. Only pursue it if Shape B becomes real.

---

## The rig, one line

`{Beam Pro: surface + app-under-test + feedback capture} × {Mac mini: agent +
builder + stream} × {XREAL: display}` — **Shape A works today by focusing the
mini**; Shape B is a real but bounded extension whose template already exists in
`remote_runtime_dispatch.go` and whose only load-bearing prerequisite is making
machine capabilities visible beyond the agent that computed them.
