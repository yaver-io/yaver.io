# Yaver — Apple TV Remote Control + Audio/Metadata Streaming (Deep Analysis)

> **Status:** design-only, 2026-06-16. No code written yet.
> **Source of truth:** this document was written *after* grepping the actual
> code, not from the build brief. Where the brief's claims about "existing
> architecture" diverge from the tree, the code wins and the divergence is
> called out explicitly (§2). Re-verify before building — other threads move
> constants in parallel.

This is the implementing-agent's reference for adding remote control of an
Apple TV via a Raspberry Pi running the Yaver agent, with now-playing
metadata + artwork streamed to the mobile app and (optionally) a CarPlay
audio surface. It reconciles the build brief
(`Apple TV Remote Control & Streaming Feature Spec`) against the real
codebase, fixes four load-bearing wrong assumptions in the brief, and lays
out the concrete integration plan + milestones.

---

## 1. One-paragraph thesis

The control plane is cheap and the brief mostly gets it right: the agent
already reaches the Pi from anywhere over the relay/QUIC mesh, the mobile app
already has the exact device-control screen pattern we need (`arm.tsx`,
`remote-desktop.tsx`), and `linux/arm64` is already a shipped build target.
The *real* work and the *real* risk are: (a) standing up a **pyatv engine**
on the Pi — there is partial Python-sidecar prior art (the arm policy server)
but no socket-RPC supervisor yet; (b) deciding **ops-verb vs first-class MCP
tool** correctly (the brief says "first-class tool", but the mobile transport
the brief tells us to reuse is built around **ops verbs**, so the brief
contradicts itself); and (c) **not** building the two things the brief
correctly forbids — HDCP video capture and CarPlay video. Audio + metadata +
control is the whole product.

---

## 2. Brief-vs-code corrections (read before trusting the brief)

The brief's "Part 1 — Existing Architecture (ground truth, do not change)" is
**not** fully ground truth. Four claims are wrong or stale:

### 2.1 ❌ "Follow the existing Cobra command structure"
**Wrong.** There is no Cobra. `desktop/agent/main.go:336` is `func main()`
with a hand-rolled `switch cmd` at line 350 dispatching on `os.Args[1]`
(`case "vault":` line 434, `case "primary":` line 530). New command groups
add a `case "appletv": runAppleTV(os.Args[2:])` and a `runAppleTV(args)`
sub-dispatcher mirroring `runVault` in `vault_cmd.go:39-79`. Do **not** import
`spf13/cobra`.

### 2.2 ⚠️ "Sit alongside the Smart Home tools, follow their conventions"
**Half-true and a trap.** The Smart Home category (Sonos, Hue, Govee, Shelly,
HA, …) was gutted in the 2026-04-28 "lean-stack cut". The tool *schemas* are
still advertised in `mcp_tools.go` (e.g. `hue_lights` line 1533,
`govee_control` line 1549, `sonos_control` line 1561) **but their handlers are
dead stubs** — `mcpSonosControl`, `mcpHueLights`, `mcpGoveeControl`,
`mcpHAService` etc. all live in `mcp_dropped_stubs.go` and return
`{"error":"feature_removed"}`. So:
- The *registration pattern* (schema in `mcp_tools.go` → dispatch case in
  `httpserver.go` → handler fn) is still a valid template to copy.
- But there is **no live Smart Home category to belong to**. Apple TV would be
  *reviving* a tool family that was deliberately removed. That's a product
  decision, not a freebie. Flag it: do we want one live consumer-device tool
  in a tree that just cut all of them? (Recommendation: yes, but keep it
  self-contained in `appletv_*.go` so it isn't entangled with the dead stubs,
  and don't re-light the zombie schemas.)

### 2.3 ⚠️ "Preferred: a Python sidecar … (no prior art assumed)"
**Better than the brief thinks.** The brief proposes a supervised Python
sidecar as if it were novel. There *is* prior art: the arm subsystem ships
Python helpers the Go agent drives — `desktop/agent/arm/yaver_policy_server.py`
(a long-lived inference server), `arm/sim_harness.py` (embedded PyBullet),
`arm/yaver_lerobot_export.py`, and `scripts/parol6_bridge.py`. None is a
clean stdio-JSON-RPC supervisor, but the "Go agent owns a Python child that
speaks a local protocol" shape already exists and is the right model. See §5.

### 2.4 ✅ "Ensure GOARCH=arm64 is in the build matrix"
**Already done.** `.github/workflows/release-cli.yml:129-130` builds
`linux/arm64`; `cli/src/agent-runtime.js` (`resolveLocalAsset` ~line 482,
`fetchRemoteAsset` ~line 533) detects `linux-arm64`, downloads
`yaver-linux-arm64.tar.gz`, caches at
`~/.yaver/bin/<ver>/linux-arm64/yaver`. `npm install -g yaver-cli` on a Pi
already works. This acceptance criterion is pre-satisfied — just verify the
release after adding the feature.

---

## 3. Architecture decision: ops verbs **and** one first-class tool

The brief says "new MCP tools (register alongside Smart Home)" and lists
`appletv_remote_key`, `appletv_now_playing`, etc. as first-class tools. But
the brief *also* says the mobile app must "dispatch through the existing
P2P/relay client (same path as task creation)". Those two statements pull in
different directions, because:

- **First-class MCP tools** (`httpserver.go:handleMCPToolCallWithAddr` switch,
  ~line 5197) are **local-only**. No `machine` routing. Great for an MCP
  client (Claude Desktop) talking to the agent it's already attached to.
- **Ops verbs** (`ops.go`, `dispatchOps`, `registerOpsVerb`) carry a
  `{machine, verb, payload}` envelope with **full mesh routing + relay
  fallback + per-verb guest scopes + streaming streamIds**. The mobile app's
  device-control screens (`armClient.ts` → `quicClient.callOpsOnDevice`,
  `quic.ts:7718`) are built **entirely on ops verbs**. LAN-direct first, relay
  fallback, no call-site changes — exactly the brief's acceptance criterion
  "works on-LAN and remote without code changes at call sites."

**Decision — do both, each for its right consumer:**

| Surface | Mechanism | Why |
|---|---|---|
| Mobile remote screen | **ops verbs** `appletv_*` | Reuses `callOpsOnDevice` mesh+relay routing; the Pi is remote, the phone roams. This is the *only* way the brief's "same transport, no call-site changes" criterion is actually met. |
| MCP clients (Claude Desktop etc.) | **thin first-class tools** that wrap the ops verbs | So the feature is usable from any MCP client per the brief. Mirror the **`robot_camera`** pattern (`httpserver.go:11850-11904`): a first-class tool that internally calls `dispatchOps(... Verb:"robot_snapshot")` and re-wraps the result. |
| `appletv_now_playing` artwork | **first-class image tool** | Must return an MCP `image` content block (base64 + mimeType), which the ops text envelope can't carry cleanly. Again: copy `robot_camera` verbatim — it already solves "ops verb returns a data: URL → first-class tool re-emits it as an image block." |

So the canonical implementation is: **ops verbs are the source of truth**;
first-class tools are thin `dispatchOps` wrappers. This is the established
`robot_camera` → `robot_snapshot` relationship, applied to Apple TV.

### Proposed ops verbs (in `ops_appletv.go`, `init()` → `registerOpsVerb`)
```
appletv_scan        {}                         -> list discovered ATVs
appletv_list        {}                         -> paired devices (from vault)
appletv_pair_begin  {id}                        -> start PIN pairing (streaming)
appletv_pair_finish {id, pin}                   -> store creds in vault
appletv_remote_key  {device?, key}              -> up/down/left/right/select/menu/home
appletv_launch_app  {device?, bundle_id}
appletv_power       {device?, state}            -> on|off
appletv_transport   {device?, action}           -> play|pause|stop|next|previous
appletv_seek        {device?, seconds}
appletv_now_playing {device?}                    -> metadata (+ artwork ref)
appletv_now_playing_stream {device?}             -> Streaming:true, streamId for deltas
```
`AllowGuest: false` on everything except possibly `appletv_now_playing`
(read-only) — pairing/control are owner-only by default, consistent with the
ops default.

### First-class tools (thin wrappers, register in `mcp_tools.go` + dispatch in `httpserver.go`)
```
appletv_remote_key, appletv_launch_app, appletv_power, appletv_transport,
appletv_seek         -> wrap dispatchOps, return mcpToolJSON(out.Initial)
appletv_now_playing  -> wrap dispatchOps, return MCP image block (robot_camera pattern)
```

---

## 4. CLI surface (`appletv_cmd.go`)

Mirror `vault_cmd.go:39-79`. Add `case "appletv": runAppleTV(os.Args[2:])` at
`main.go:~434`.

```
yaver appletv scan
yaver appletv pair <id>          # interactive PIN; writes creds to vault
yaver appletv list
yaver appletv key <name>         # up/down/left/right/select/menu/home/...
yaver appletv app <bundle-id>
yaver appletv now-playing        # JSON
yaver appletv power <on|off>
yaver appletv transport <play|pause|stop|next|previous>
yaver appletv seek <seconds>
```

Each subcommand should call the **same code path as the ops verb handler**
(not a parallel impl) — i.e. the verb handlers live in `appletv.go` /
`appletv_engine.go`, and both the ops registration and the CLI subcommands
call into them. One engine, three front doors (CLI, ops, first-class tool).

---

## 5. The pyatv engine on the Pi (the actual hard part)

Apple TV control is **IP-based** (MRP / Companion / AirPlay over the LAN) — no
HDMI, no IR. pyatv is the mature open-source engine. Decision: **supervised
Python sidecar over a local 127.0.0.1 socket**, not `atvremote` shell-outs.

### Why sidecar over CLI shell-out
- pyatv's push-updates (now-playing deltas) require a **persistent
  connection**; `atvremote` per-call re-pairs/re-connects each time (slow,
  ~1-2s, and loses the push subscription). The metadata-streaming milestone
  (§7) needs a long-lived connection — a sidecar is the only clean fit.
- The sandbox (`access_policy.go` Policy Guard) blocks arbitrary shell; a
  single whitelisted long-lived `python3 -m yaver_atv_bridge` invocation is
  far easier to audit than N `atvremote` shell-outs per session.

### Supervision pattern (copy the proven shapes)
There is **no existing stdio-JSON-RPC supervisor**, but two patterns compose
into one:
- **Daemon-start + readiness-poll**, from `models.go:Serve()` (281-322):
  `exec.Command(...).Start()`, then poll a readiness endpoint with a deadline.
- **Long-lived Python inference server driven by the agent**, from
  `arm/yaver_policy_server.py` + the Go side that launches/queries it. Same
  shape: Go owns a Python child, talks to it over a local transport.

Concretely, `appletv_engine.go`:
1. `ensureBridge()` — if not running, `exec.Command("python3", bridgePath,
   "--sock", "/run/user/<uid>/yaver-atv.sock")`. Bind to a unix socket (or
   `127.0.0.1:<ephemeral>` if unix sockets are awkward on the target),
   **never** a LAN-reachable port.
2. Poll a `{"method":"ping"}` until ready (deadline ~10s, `models.go` cadence).
3. JSON-RPC framing: one JSON object per line over the socket
   (`{"id", "method", "params"}` → `{"id", "result"|"error"}`). Keep it
   line-delimited — matches the SSE/blackbox line-delimited-JSON idiom already
   in the tree.
4. Restart-on-crash with backoff; surface "bridge down" as a clear ops error
   `code:"appletv_bridge_unavailable"` so the app shows a setup prompt
   (acceptance criterion: degrade gracefully).

### Where the bridge script ships
Put `yaver_atv_bridge.py` under `desktop/agent/appletv/` alongside the Go
(mirrors `desktop/agent/arm/*.py`). pyatv itself is **not** vendored — it's a
pip dependency the user installs on the Pi (`pip3 install pyatv`).
`yaver doctor` must report its presence (§6).

### Credentials → vault, never plaintext
pyatv pairing yields per-protocol credential blobs (MRP/AirPlay/Companion
tokens). Persist them via the vault Go API, **not** a config file:
- Write: `vs.Set(VaultEntry{Project:"appletv", Name:"<deviceId>",
  Category:"custom", Value:<json-creds>})` (`vault.go:468`).
- Read: `vs.Get("appletv", "<deviceId>")` (`vault.go:448`).
- **Vault v2 nuance** (correcting CLAUDE.md's blanket "vault locks on token
  rotation"): v2 (`YV2\x00`, master key from `~/.yaver/master.key` via
  `vault_keychain.go:EnsureMasterKey`) is **decoupled** from the auth token —
  pairing creds survive token rotation. Only legacy v1 vaults lock. On a fresh
  Pi this will be v2, so pairing persists across restarts and re-auths. Good.

The bridge process receives creds **only** from the Go agent at launch/connect
time (passed over the local socket, or via an env var the agent sets on the
child) — the Python side never reads the vault directly.

---

## 6. `yaver doctor` capability check

Add an Apple TV section to `runDoctor()` (`main.go:~6118`) following the
`probeTool` shape in `doctor_build.go:275`:
- `python3` present?
- `pyatv` importable? (`python3 -c "import pyatv"` with a 2s timeout)
- bridge script present at expected path?
- vault unlocked + any `appletv/*` entries paired?

Report `✓/!/✗` like the rest of `runDoctor`. This satisfies the brief's
"`yaver doctor` reports pyatv/sidecar availability" criterion.

---

## 7. Streaming: every primitive already exists

The brief's M3/M4 streaming asks map cleanly onto existing transports — **no
new public ports, nothing through Convex** (privacy contract holds because all
of these ride the already-authed relay/mesh tunnel).

| Need | Reuse | Reference |
|---|---|---|
| Now-playing **metadata deltas** | SSE fan-out, history-replay, drop-slow-subscriber | `logstream.go:223-264`; mirror `/incidents/stream`, `/operations/stream`. New route `GET /appletv/nowplaying/stream`. |
| **Artwork** image | Snapshot-poll + optional MJPEG, content-addressed cache | `ghost_stream.go` + `/rd/frame.jpg` (`remotedesktop_http.go:197`). iOS = poll `/appletv/artwork/latest.jpg`; web can use MJPEG. Cache `Cache-Control: max-age, immutable` like `vibe_preview_sse.go`. |
| Artwork **blob transfer** (one-shot) | multipart media path | `feedback_http.go:73-128` upload + `serveFeedbackFile` 159-198 serve. |
| Large/high-res payloads w/o iOS stalls | binary chunked QUIC wire | `relay_stream_wire.go` (magic `0xFE`, 64KiB chunks) — the iOS NSURLError -999 fix. |
| **Audio companion** stream (M4) | bidirectional WS, PCM frames | `/voice/stream` (`voice_http.go`) already streams 16k PCM frames both ways; Opus encode would be net-new but the WS transport is done. |

**Mobile consumption** must follow the documented iOS rule (from
`remote-desktop.tsx` + memory): **MJPEG only on web; iOS snapshot-polls**.
For metadata, SSE works on both. The now-playing card polls
`appletv_now_playing` (or subscribes to the SSE) every ~900ms, the same cadence
`arm.tsx` polls status.

### Audio capture reality (don't oversell M4)
- pyatv can act as an **AirPlay receiver** in some configs, but routing the
  Apple TV's *audio* into the Pi reliably usually means a **physical path**
  (HDMI-audio-extractor → USB/optical into the Pi), then Opus-encode.
- Content protection *can* still apply to the audio path depending on source.
  **Default to metadata-only**; treat audio as best-effort and gate it behind
  M4. Document this honestly in user-facing copy (acceptance criterion).

---

## 8. Mobile app (`mobile/`) — pure pattern reuse

Net-new is one client facade + one screen; everything else is reuse.

- **Client facade** `mobile/src/lib/appletvClient.ts` — copy `armClient.ts`
  structure exactly: LAN-direct attempt loop → `quicClient.callOpsOnDevice`
  fallback (`quic.ts:7718`). Methods map 1:1 to the ops verbs in §3.
- **Screen** `mobile/app/appletv-remote.tsx` — copy `arm.tsx` /
  `remote-desktop.tsx`: `useDevice()` for device picker + `connectionStatus`,
  `AppScreenHeader`, a `run()`/`sendCommand()` busy-wrapper, ~900ms poll loop.
  Layout = D-pad (up/down/left/right/select) + menu/home + transport row +
  now-playing card (artwork via `/appletv/artwork/latest.jpg`, title/app,
  scrubber).
- **Navigation** — expo-router filesystem route; add an entry point from
  `(tabs)/more.tsx` (`router.push("/appletv-remote")`). No new nav infra.
- **Connection-state UI, token refresh, multi-device** — all inherited from
  `DeviceContext` + `quic.ts`. No new network client (brief criterion).

### CarPlay (M4, optional) — confirmed greenfield
There is **zero** CarPlay code today (`mobile/ios/Yaver/Info.plist` has no
CarPlay scene/entitlement; no CarPlay target in the pbxproj). M4 would add:
- A CarPlay **audio app** entitlement + `CPTemplateApplicationSceneDelegate`.
- `CPNowPlayingTemplate` + a `CPListTemplate` of controls.
- **No `CPMapTemplate`/video templates.** Audio + metadata only.
This is a real new iOS target with its own provisioning — non-trivial, rightly
gated to last.

---

## 9. Hard non-goals (enforce in code comments + UX)

Straight from the brief, reaffirmed because they're also Yaver's "do no harm /
respect third parties" line and a legal line:

1. **No HDMI capture of the Apple TV.** HDCP-protected; software capture is
   blocked and circumventing it violates streaming ToS. No capture path may be
   left half-built in the tree (acceptance criterion). If a task seems to
   require it, **stop and surface the constraint** — do not engineer around it.
2. **No CarPlay video.** Audio app category only.
3. The *legitimate* adjacent feature — streaming the user's **own
   non-protected** sources (home media, IP camera, entitled IPTV) Pi→phone/car
   via FFmpeg→HLS/WebRTC — is **a separate track**, build only on explicit
   request, kept modular so it never entangles the Apple TV control path.

These belong as a doc-comment header in `appletv.go` and `ops_appletv.go` so
the next maintainer can't accidentally grow a capture path.

---

## 10. Milestones (ship incrementally)

| M | Scope | New files | Reuses |
|---|---|---|---|
| **M1** Control plane | pyatv bridge + supervisor; ops verbs; CLI `appletv`; vault pairing; doctor check | `appletv/yaver_atv_bridge.py`, `appletv_engine.go`, `ops_appletv.go`, `appletv_cmd.go`, doctor section | `models.go` supervise, `arm/*.py` shape, `vault.go`, `ops.go` |
| **M2** Mobile remote UI | client facade + screen + nav entry | `mobile/src/lib/appletvClient.ts`, `mobile/app/appletv-remote.tsx` | `armClient.ts`, `arm.tsx`, `remote-desktop.tsx`, `DeviceContext`, `quic.ts` |
| **M3** Now-playing streaming | metadata SSE + artwork snapshot/MJPEG + first-class `appletv_now_playing` image tool | `/appletv/nowplaying/stream`, `/appletv/artwork/*` routes | `logstream.go`, `ghost_stream.go`, `robot_camera` pattern |
| **M4** Audio + CarPlay audio app | optional; Opus encode + WS audio + iOS CarPlay target | CarPlay scene/templates, audio encoder | `/voice/stream` WS transport |
| **(sep.)** Own-source video | only on explicit ask | FFmpeg→HLS/WebRTC module | — |

**M1 is headless and terminal-testable** (brief's intent). Gate any
real-Apple-TV integration test behind an env flag in `.env.test`, mirroring
how remote-server tests are gated.

---

## 11. Open questions for the user

1. **Revive a consumer-device tool family right after the lean-stack cut?**
   The Smart Home category was deliberately gutted (§2.2). Apple TV is the
   first live consumer-device control to re-enter. Confirm that's intended
   (recommendation: yes, but self-contained, don't re-light the dead Sonos/Hue
   schemas).
2. **Audio scope.** Is M4 audio actually wanted, given it likely needs a
   physical HDMI-audio-extractor on the Pi and may hit content protection? Or
   is control + metadata the real product and audio a "later, maybe"?
3. **pyatv install UX.** Auto-`pip install pyatv` on first `yaver appletv` use
   (with consent), or doctor-only "please install"? (Recommendation:
   doctor-only + a one-line `yaver appletv setup` that pip-installs into a
   venv, never touching system Python.)

---

---

# PART B — Multi-surface "Yaver Anywhere": Pi capture-card source → car / glasses / phone

> Added 2026-06-16 after the user reshaped scope: *"open Yaver in apple car
> android car etc … from home streaming with my raspi config with capture card
> etc, also control … same for yaver glass case."*
>
> This is no longer "an Apple TV remote." It's a **home A/V source + control
> plane on the Pi, consumed by many display/control surfaces.** The engine is
> built once; surfaces are thin clients on the same transport. This part is the
> honest architecture, including the legal line we will NOT cross.

## B.1 The one hard constraint, stated up front (capture card + HDCP)

The user wants a **capture card on the Pi** feeding video home→car/glasses.
A USB capture card on the Pi appears as a **V4L2 device** (`/dev/videoN`) +
an ALSA audio device; `ffmpeg` reads it. That is a clean, legitimate path
**for non-protected sources**.

It is **not** a way to stream a premium Apple TV app to the car, and we will
not make it one:

- The Apple TV asserts **HDCP** on its HDMI output. An HDCP-compliant capture
  card receives a **black/blocked frame** on protected playback (Netflix,
  Disney+, Apple TV+, etc.). The Apple TV **home screen, Settings, and many
  non-DRM apps capture fine**; premium video does not.
- Defeating HDCP (an HDCP "stripper" between Apple TV and the card) is a DMCA
  §1201 circumvention and a streaming-service ToS violation. **We do not
  detect, document, or assume a stripper.** If the captured frame is black,
  the feature reports "source is HDCP-protected — capture unavailable" and
  stops. That message is a feature, not a bug (acceptance criterion §2.8).

**So the capture-card product is:** stream **your own non-protected HDMI
sources** — a games console, a camera/dashcam, a laptop you own, a non-DRM
media player, the Apple TV *menu/non-DRM apps* — from the Pi to any surface.
The **Apple TV control plane (Part A)** is the robust, always-legal way to
*drive* the Apple TV; the capture path *shows* whatever non-protected pixels
the card legitimately receives. Two orthogonal capabilities, composed.

This collapses the brief's §2.7 "adjacent feature" into the **core** — the
user explicitly asked for it — but keeps the §2.1 / §9 non-goals intact.

## B.2 Architecture: source vs. surface (build the source once)

```
            HOME (Raspberry Pi, yaver agent, linux/arm64)
            ┌───────────────────────────────────────────┐
            │  SOURCE PLANE                               │
            │   • appletv engine (pyatv) — CONTROL+meta   │
            │   • capture engine (v4l2+ffmpeg) — VIDEO    │  ← capture card
            │   • now-playing/metadata (SSE)              │
            └───────────────┬─────────────────────────────┘
                            │  existing relay/QUIC mesh (auth'd, P2P,
                            │  nothing through Convex — privacy contract)
        ┌───────────────────┼───────────────────┬──────────────────┐
        ▼                   ▼                   ▼                  ▼
   PHONE (RN)        CARPLAY (iOS)       ANDROID AUTO        YAVER GLASS
   full app:         audio app:          media app:          HUD app:
   video + dpad +    now-playing +       now-playing +       now-playing +
   now-playing       controls (NO        controls (NO        small video +
                     video, driving)     video, driving)     voice control
                                                              (platform-dep.)
        ▲
   ANDROID HEAD UNIT (full Android in dash) — runs the SAME RN APK:
   full app incl. VIDEO of non-protected sources (parked / passenger)
```

**Key insight that keeps this tractable:** the *source plane* and the
*streaming/control contract* are **one body of work** (Part A verbs +
capture verbs + MJPEG/SSE routes). Every surface is a thin client of that
contract. We build the source + the phone client end-to-end; the other
surfaces are native shells that call the *same* endpoints.

## B.3 Per-surface reality (what each one can actually do)

| Surface | Runtime | Video? | Control? | Native work needed | Status |
|---|---|---|---|---|---|
| **Phone (RN)** | existing app | ✅ (poll/MJPEG) | ✅ | none — reuse `quic.ts`/`DeviceContext` | **buildable now (M2/M3)** |
| **Android head unit** (aftermarket, full Android in dash) | **same RN APK** | ✅ non-protected (park/passenger) | ✅ | none — it's the same Android build; just install the APK | **free** (verify on device) |
| **Apple CarPlay** | iOS, projected | ❌ Apple forbids arbitrary video while driving | ✅ audio + now-playing + lists | new CarPlay **audio-app** scene + entitlement + `CPNowPlayingTemplate`/`CPListTemplate` | **scaffold; needs provisioning** |
| **Android Auto** | Android, projected | ❌ same restriction (media template only) | ✅ media controls + now-playing | new **MediaBrowserService** + `media-automotive` declaration | **scaffold; needs provisioning** |
| **Yaver Glass** (smart glasses) | platform-specific | ⚠️ tiny HUD video possible IF the glasses run Android/WebView | ✅ voice + now-playing HUD | depends entirely on the glasses platform (see B.5) | **design only; hardware-gated** |

The two **projected car** modes (CarPlay/Android Auto) are deliberately
**audio + control**, never video — that is Apple's and Google's rule for
safety, not ours to override. **Video in the car is only the parked
full-Android head-unit case**, and only for non-protected sources.

## B.4 The streaming/control contract (one set of endpoints, all surfaces)

All over the existing authed mesh (§7 primitives). New on the agent:

**Capture (video, the capture card):** mirror `remotedesktop_http.go`/`ghost_stream.go`.
```
ops capture_devices         -> [{path:/dev/video0, name, formats}], ffmpeg present?
ops capture_start {device,fps,width,height}  -> Streaming: capture frame loop
ops capture_stop
ops capture_status
GET /capture/frame.jpg       -> latest JPEG (iOS/glasses snapshot-poll)
GET /capture/stream          -> multipart/x-mixed-replace MJPEG (web/head-unit)
                              -> reports HDCP-black as a clear 409, not a black stream
```
**Control + metadata (Apple TV, Part A):** the `appletv_*` ops verbs +
`GET /appletv/nowplaying/stream` SSE + `appletv_now_playing` first-class image
tool. Surfaces that can't show video (CarPlay/Android Auto) consume **only**
the now-playing SSE + control verbs — they never touch `/capture/*`.

**Why this shape:** a CarPlay audio template and an Android head-unit video
view are wildly different native code, but they call the *identical* agent
endpoints. Build the endpoints once; each surface picks the subset it's
allowed to render.

## B.5 Yaver Glass — honest take

"Yaver Glass" is not one thing; what's possible is entirely
platform-determined:
- **Android-based glasses (e.g. INMO, TCL RayNeo, Xreal w/ Android host)** —
  run the **same RN APK** or a slim WebView pointed at `/capture/stream` +
  the now-playing SSE. Tiny HUD video of a non-protected source + voice
  control is feasible. Closest to "free" (like the head unit).
- **Tethered display glasses (Xreal/Rokid as a USB-C monitor)** — they're just
  an external display for the phone; the phone app already drives them. No new
  code.
- **Closed platforms (Apple Vision-class, Meta)** — need a first-party app on
  their SDK; out of scope until there's a target device. Design the client
  around the same endpoints so porting is a UI layer only.

**Decision:** there is no generic "glasses" target. We make the **phone RN
client renderable on a small HUD layout** (a `?surface=glass` compact mode)
so Android glasses / WebView get it for free, and we document the rest as
hardware-gated. We do **not** scaffold a fake glasses SDK target.

## B.6 Revised milestones (supersedes §10 for the multi-surface scope)

| M | Scope | Buildable here? |
|---|---|---|
| **M1** Apple TV control plane (pyatv bridge, ops verbs, CLI, vault, doctor) + first-class tools | ✅ fully (headless) |
| **M1b** Capture-card engine (v4l2/ffmpeg → MJPEG buffer) + `/capture/*` routes + verbs, HDCP-black detection | ✅ fully (degrades w/o device/ffmpeg) |
| **M2** Phone RN surface: device picker, D-pad/transport, now-playing card, **capture video view**, compact `glass` layout | ✅ code; device-verify later |
| **M3** Now-playing SSE streaming + artwork | ✅ fully |
| **M4** CarPlay audio app (iOS scene/entitlement) | ⚠️ scaffold; needs Apple provisioning + device |
| **M5** Android Auto media service | ⚠️ scaffold; needs Play automotive review |
| **M6** Android head unit | ✅ same APK — verify on a real unit |
| **M7** Yaver Glass HUD | ⚠️ compact RN layout free; native glasses hardware-gated |

This session implements **M1, M1b, M3 (agent side) + M2 (phone)** in full,
and lays honest scaffolds/docs for **M4–M7**.

---

# PART C — Yaver everywhere: TV apps, peer streaming to own/guest accounts, an "OBS-wrap"

> Added 2026-06-16 after the user reshaped scope again: *"distribute yaver in
> android tv and apple tv … streaming so user can make mobile app camera stream
> to guest accounts or his account, likewise his pc or his tv stream … we may
> have obs wrap or similar features, simpler usage, full mobile app based."*
>
> This generalizes Parts A/B from "Apple TV + capture card" into **"every Yaver
> device is a streaming SOURCE and/or SINK, shareable to your own or a guest's
> account, with a simple mobile-directed compositor."** The capture/now-playing
> plane built in M1b/M3 is the foundation; this part is the generalization and
> the honest build sequencing.

## C.1 One model: sources, sinks, shares

```
  SOURCES (produce frames/metadata)        SINKS (render)
  • phone camera (expo-camera)             • phone app
  • PC screen (screenlog / ghost) ────►    • Android TV / Apple TV app
  • capture card (capture.go) ──── mesh ─► • car head unit / CarPlay-audio
  • Apple TV now-playing ─────────────►    • glass HUD
  • robot/host camera (robot_snapshot)     • web dashboard
                  │                                  ▲
                  └──── SHARE ──────────────────────┘
        own account: any device you own, instantly
        guest account: a "stream"-scoped guest token (read-only view, NOTHING else)
```

Everything rides the existing authed mesh; **nothing through Convex** (privacy
contract). This is already true for the verbs shipped this session.

## C.2 What shipped this session for C (the shared primitive)

`ops_stream.go` + a new **`stream` capability scope** (`ops.go`
`capabilityScopeVerbPrefix["stream"]="stream_"`):
- `stream_list` — enumerate shareable sources on a box (capture card, Apple TV
  now-playing, camera), read-only, **guest-viewable**.
- `stream_snapshot {source, device?}` — pull one frame as a base64 data URL
  (`capture` / `appletv` artwork / `camera` via `robot_snapshot`).

A guest token scoped `stream` is **isolated to `stream_*` only** — it can view a
shared source and reach nothing else on the machine (no control, no exec, no
vault), exactly like the `circuit` capability scope isolates Talos/OCPP to the
simulator. **This is the "stream to a guest account" core**, testable now.

The remaining streaming work is layered on this:

## C.3 Peer streaming — sources (the real native work)

| Source | Mechanism | Status |
|---|---|---|
| **Capture card** | ffmpeg→MJPEG (`capture.go`) | ✅ shipped (M1b) |
| **Apple TV now-playing** | pyatv (`appletv.go`) | ✅ shipped (M1/M3) |
| **PC screen** | already exists — `screenlog`/`ghost_stream` (`/ghost/stream` MJPEG) | ✅ reuse; just expose via `stream_*` + guest auth |
| **Phone camera** | `expo-camera` frames → encode → push | ⚠️ NET-NEW native: a frame producer that POSTs JPEG frames to a box's capture-style buffer (low-fps, reuses the feedback multipart media path) OR WebRTC for real-time. Mobile-side capture + uplink is the bulk of the effort. |
| **TV screen** | tvOS ReplayKit broadcast / Android TV MediaProjection | ⚠️ NET-NEW native, per-platform; TVs can only broadcast their OWN app's content, not other apps (OS sandbox) |

**Phone-camera-to-account** is the headline new ask. Tractable path: the phone
runs a producer that grabs `expo-camera` frames at a few fps, JPEG-encodes, and
pushes them to a chosen box's frame buffer (a `POST /capture/push` mirroring the
existing feedback upload + the robot "external camera push" buffer the agent
already supports for `robot_camera`). Viewers (own or guest) then consume via
`stream_snapshot` / MJPEG. Real-time (sub-second, audio) needs **WebRTC**, which
is a larger lift — recommend low-fps JPEG push first (reuses everything), WebRTC
as a later milestone.

## C.4 Guest sharing — reuse the guest system, add a share object

Yaver already has guest infrastructure (`backend/convex/guests.ts`,
`desktop/agent/guest_*.go`; scopes `full`/`feedback-only`/`deploy`). A **stream
share** = mint a guest token with the new `stream` capability scope, bound to a
source + a source-device. The guest's app calls `stream_list`/`stream_snapshot`
(or, later, a guest-authed MJPEG endpoint) and sees ONLY that view. No new trust
model — it's the capability-scope pattern that already isolates the circuit
simulator, applied to live views. **Net-new:** a small share-create UI + binding
a share to a specific `{source, device}` (so a `stream` token can't enumerate
other boxes).

## C.5 Yaver as an Android TV / Apple TV app (distribution)

The TV is both a **sink** (big now-playing, a viewer for shared streams, a
remote target) and — limited by the OS — a **source** (its own broadcast only).

| Target | Reality | Status |
|---|---|---|
| **Android TV** | The existing RN **Android APK** runs on Android TV. Needs: a `LEANBACK_LAUNCHER` intent + `android.software.leanback` feature (non-touch), D-pad **focus management** on the screens, and a TV-grid home layout. Buildable with the current Gradle/Expo setup. | ⚠️ config + focus work; no new toolchain |
| **Apple TV (tvOS)** | Expo/React Native does **not** target tvOS without the **`react-native-tvos`** fork + a tvOS Xcode target. This is a **separate build target** (own entitlements, focus engine, App Store tvOS submission). Significant. | ⚠️ large native effort; needs the RN-tvOS fork decision |
| **`?surface=glass` / `?surface=tv` layouts** | The RN screens already branch on a compact layout (shipped for glass). A `tv` layout = larger type + focusable rows; reuses the same components. | ✅ pattern in place; add `tv` skin |

**Decision:** Android TV is the cheap win (same APK + leanback + focus). tvOS is
a real project gated on adopting `react-native-tvos` — call it out as an ADR, do
not silently fork the mobile build. Both are SINKS first; TV-as-source (ReplayKit
/ MediaProjection broadcast) is a later, separate milestone.

## C.6 The "OBS-wrap" — simpler, mobile-directed, box-encoded

OBS = scene composition + mixing + encode + broadcast. A faithful on-phone clone
is heavy (GPU compositing + encode drains battery, fights the OS). The Yaver-
shaped, **simpler** version:

- **Phone is the director, a box is the mixer.** The mobile app picks sources
  (camera + a PC screen + an overlay) and a layout; a box (Pi/PC) runs the heavy
  `ffmpeg -filter_complex` compose+encode (it already has ffmpeg for capture).
  This matches Yaver's whole thesis — heavy work on the box, phone drives.
- **Scenes as data.** A scene = `{sources:[{stream, rect}], overlays:[{text|image, rect}], output:{fps, size}}`. Store in the vault; the box renders it with one ffmpeg graph. New verbs `scene_set` / `scene_start` / `scene_stop`, output to the same MJPEG/`stream_*` plane, shareable to own/guest exactly like any source.
- **Simple usage first.** v1 = one source + a now-playing/text overlay + push to
  viewers. Full multi-source mixing + transitions is a later milestone. Don't
  build an on-phone GPU compositor.
- **Broadcast out (RTMP to Twitch/YouTube)** is a trivial ffmpeg output target
  once compose-on-box exists — but it's third-party egress: identify honestly,
  user-initiated only, never a hidden loop (CLAUDE.md "do no harm").

## C.7 Milestones for Part C

| M | Scope | Buildable here? |
|---|---|---|
| **M8** `stream` guest scope + `stream_list`/`stream_snapshot` viewer verbs | ✅ **shipped this session** |
| **M9** Guest-authed MJPEG (`/capture/stream` etc. under a stream-scoped token) + share-create UI | ⚠️ agent auth plumbing + mobile UI — buildable next |
| **M10** Phone-camera source: `expo-camera` → JPEG push → box buffer (reuse feedback/external-camera path) | ⚠️ native mobile producer |
| **M11** PC-screen source exposed via `stream_*` (wraps existing `ghost`/`screenlog`) | ✅ mostly wiring |
| **M12** Android TV app (leanback + focus + `tv` layout) | ⚠️ config + focus, no new toolchain |
| **M13** Apple TV (tvOS) app via `react-native-tvos` | ⚠️ ADR + separate target, large |
| **M14** Box-side "OBS-wrap" compositor (`scene_*` verbs, ffmpeg filter_complex) | ⚠️ box ffmpeg graph + mobile director UI |
| **M15** WebRTC real-time + RTMP broadcast-out | ⚠️ largest; later |

**Recommended next step:** M9 + M11 (make the streams I built guest-shareable
and add the PC-screen source — both are mostly wiring over shipped code), then
M12 (Android TV, cheap), then decide the tvOS ADR (M13) and the compositor (M14).

---

# PART D — Distribution playbook + TV sign-in + watch-a-remote-box

> Added 2026-06-17. Answers "how do I distribute in Apple car / Google car /
> Apple TV / Google TV", the TV QR sign-in ask, and "stream from magara (open
> YouTube/Netflix/Gain/Exxen via chat) to my phone."

## D.1 How each surface is actually distributed

The headline: **CarPlay and Android Auto are NOT separate apps** — they're
capabilities of the *existing* iOS/Android app. **Apple TV and Google TV** are
separate *form factors* of the same app record. You never ship a 5th store
listing.

| Target | How you distribute it | What it takes | Video? |
|---|---|---|---|
| **Apple CarPlay** | The **same iOS app** on the App Store, with the **CarPlay entitlement**. | Request the CarPlay entitlement from Apple (developer.apple.com → an **audio** app category fits Yaver). Add `com.apple.developer.carplay-audio` to the entitlements, a `CPTemplateApplicationSceneDelegate`, and `CPNowPlayingTemplate` + `CPListTemplate`. Ship in the normal iOS binary. Apple must approve the entitlement. | ❌ audio + now-playing + lists only |
| **Android Auto** | The **same Android app** on Play, declaring an Auto capability. | Add a `MediaBrowserService` (media category) — or the Jetpack **Car App Library** for the IoT/POI categories — plus `automotive_app_desc.xml` + the `com.google.android.gms.car.application` meta-data. Passes an **Android Auto quality review** at Play submission (no separate approval for media apps). | ❌ media template only |
| **Apple TV (tvOS)** | A **separate tvOS binary**, same App Store Connect **app record**, new platform. | RN doesn't target tvOS in stock Expo — adopt the **`react-native-tvos`** fork + a tvOS Xcode target (own entitlements, focus engine). TestFlight supports tvOS. Submit as the tvOS platform of your app. **This is the one real new build target** (ADR-worthy). | ✅ full app incl. non-protected video |
| **Google TV / Android TV** | The **same Play app / AAB**, made TV-eligible. | Add `<uses-feature android:name="android.software.leanback" android:required="false">`, a `LEANBACK_LAUNCHER` `<intent-filter>` on a TV activity, a 320×180 TV **banner**, and D-pad **focus** handling. Passes the **Android TV quality** checklist. Google TV surfaces the same APK — no new toolchain. | ✅ full app incl. non-protected video |

**Sequencing:** Google TV/Android TV (cheapest — same APK + leanback + focus) →
CarPlay-audio + Android-Auto-media (entitlement/service on the existing apps) →
tvOS (the only one needing the `react-native-tvos` fork). Yaver's deploy scripts
(`deploy-testflight.sh`, `deploy-playstore.sh`) already cover the iOS/Android
binaries; tvOS would add a tvOS archive step, Android TV is the same AAB with the
manifest additions.

## D.2 TV sign-in — shipped (QR / device-code)

Typing credentials on a TV remote is miserable, so the TV uses the **device-code
flow** (RFC 8628) — the *same* one `yaver auth` uses headless, which Yaver
already has end-to-end (`backend/convex/deviceCode.ts`, phone approver
`app/approve-device.tsx` with a QR scanner).

Shipped this round:
- `mobile/src/lib/tvSignIn.ts` — `createTVDeviceCode()` → `POST /auth/device-code`,
  `pollTVDeviceCode()` → `GET /auth/device-code/poll`.
- `mobile/app/tv-signin.tsx` — shows a **QR** (`react-native-qrcode-svg`,
  already a dep) encoding `https://yaver.io/auth/device?code=…` + a big short
  code, polls every 5s, calls `login(token)` on approval, auto-refreshes on
  expiry.
- `mobile/app/index.tsx` — `Platform.isTV` routes unauthenticated TV users to
  `/tv-signin` instead of `/login`.

Flow: TV shows QR → user scans with the signed-in phone (already routes to
`approve-device.tsx`) or visits the URL → one-tap approve → TV signs itself in.
**No new backend** — reuses the existing device-code contract.

## D.3 Watch a remote box (magara) — open YouTube/video by chat

Shipped: the `screen_watch {url}` ops verb opens a URL in **that box's desktop
browser** (`openBrowser` → xdg-open/open/start) and returns the screen-stream
URL. So a chat command to the agent on magara — "open this video and stream it
to me" — opens it on magara and you watch via the existing **Remote Desktop**
(`/rd/stream`, `app/remote-desktop.tsx`) or `/ghost/stream`. `stream_list` now
includes a `screen` source; `stream_snapshot {source:"screen"}` returns a frame.

What composes here (mostly already-shipped):
- **Open / navigate by chat** → the agent's `open_url` + `browser_navigate` /
  `browser_click` tools, now plus `screen_watch`.
- **View the box's screen on the phone** → Remote Desktop (exists) / the screen
  source.
- **Share that view to a friend** → the `stream` guest scope (Part C).

### The DRM line (Netflix / Gain / Exxen / Disney+ …) — held, not crossed
Premium streaming services enforce **Widevine/FairPlay DRM + HDCP**. Their video
**blanks under screen capture** (the browser/OS refuses to render protected
frames into a capture buffer) — exactly like the Apple TV HDCP case (§B.1).
`screen_watch` returns a `drmNote` saying so. We **drive** these apps (open,
navigate, play/pause via the browser tools) and stream **non-protected** content
(YouTube, your own media, web UIs); we do **not** strip DRM/HDCP to re-stream
protected video. A block is a "no", not a puzzle (CLAUDE.md "do no harm").

The always-legal pattern for premium services: use Yaver to **control** them on
the box, and watch them **on the device that's licensed to play them** — not by
re-capturing protected pixels.

---

## 12. File-reference appendix (verified 2026-06-16)

- CLI dispatch (no Cobra): `desktop/agent/main.go:336,350,434,530`
- Vault group template: `desktop/agent/vault_cmd.go:39-79`
- Vault Go API: `desktop/agent/vault.go:448 (Get), 468 (Set), 508 (Delete)`;
  v2/master key `vault_keychain.go:EnsureMasterKey`
- MCP tool schemas: `desktop/agent/mcp_tools.go` (zombie smart-home @1533-1561)
- MCP dispatch switch: `desktop/agent/httpserver.go:5197`
- Dead smart-home handlers: `desktop/agent/mcp_dropped_stubs.go`
- Image-tool-over-ops pattern: `desktop/agent/httpserver.go:11850-11904` (robot_camera)
- Ops registry/dispatch: `desktop/agent/ops.go` (`registerOpsVerb`, `dispatchOps`)
- Python sidecar prior art: `desktop/agent/arm/yaver_policy_server.py`,
  `arm/sim_harness.py`, `scripts/parol6_bridge.py`
- Daemon supervise + readiness poll: `desktop/agent/models.go:281-322`
- SSE fan-out: `desktop/agent/logstream.go:223-264`
- MJPEG + frame poll: `desktop/agent/ghost_stream.go`, `remotedesktop_http.go:150-217`
- Binary chunk wire: `desktop/agent/relay_stream_wire.go`
- Multipart media: `desktop/agent/feedback_http.go:73-198`
- Voice WS PCM: `desktop/agent/voice_http.go`
- Build matrix arm64: `.github/workflows/release-cli.yml:129-130`;
  `cli/src/agent-runtime.js` (~482, ~533)
- Mobile control screens: `mobile/app/arm.tsx`, `mobile/app/remote-desktop.tsx`
- Mobile ops client: `mobile/src/lib/armClient.ts`, `mobile/src/lib/quic.ts:7718,1202`
- Device context: `mobile/src/context/DeviceContext.tsx`
- CarPlay: **absent** — `mobile/ios/Yaver/Info.plist` (no CarPlay keys/target)
