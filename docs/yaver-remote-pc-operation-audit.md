# Operating a remote PC from phone / AR-VR with voice + AI — deep audit

> **Audit only. No code was changed.** Every claim below was grepped or read out of
> the tree at `main` on **2026-07-19**. Where a doc or a code comment disagrees with
> the code, the code won and the drift is called out. Per `CLAUDE.md`: markdown
> drifts, code is the source of truth — re-verify before building on any line here.

## The goal being audited

A user wearing AR/VR glasses, or holding a phone, connects to their own remote
**Windows / Linux / macOS** machine and — by talking to it — browses the desktop,
opens applications, and gets work done: reading CRM/ERP data, driving AutoCAD or
Fusion, writing a LaTeX paper, building a presentation. Via APIs where APIs exist,
via the GUI where they don't. Through Yaver's MCP, using runners. On every OS,
from every UI surface.

## Verdict in one paragraph

**Every individual piece exists somewhere in the tree, and almost none of them are
connected to each other.** There is a real cross-platform screen+input engine
(`ghost/`), a real accessibility-tree reader on all three OSes, a real MJPEG remote
desktop with working mouse/keyboard from web and mobile, a real remote-verb proxy
that can fire `ghost_click` on another machine, two real voice stacks, a real WebXR
scene, and a genuinely well-built consent/audit/credential frame for business data.
What does **not** exist is the spine that would make them one product: **voice is
wired to nothing in the GUI-control path, the vision loop is single-shot with no
verify, nothing can launch an application, the accessibility tree is read-only, and
the business-data gateway ships with zero connectors.** The honest status is
*"seven strong components, zero end-to-end path"* — not *"80% done"*.

Two things sharpen that verdict into a warning. **Windows — the goal's headline
target — is the least-supported configuration in the repo**: the GUI code compiles
and has never been run by CI, and the interactive runner layer does not function at
all (no PTY, no tmux, detached autodev and service units are stubs). And **the
desktop GUI has no concurrency lease**, so two clients driving one machine simply
fight — while the lease machinery that would fix it exists, tested, on a different
subsystem.

The single most important structural finding: **the semantic layer is present but
unused.** `ghost_windows` returns a real AX / UIAutomation / AT-SPI tree with element
names, roles and bounds on macOS, Windows and Linux — and no verb acts on it. Every
click in the system is a blind screen coordinate produced by a vision model. That is
the difference between "drives AutoCAD reliably" and "clicks approximately where it
thinks a button is."

---

## 1. What actually works today

These are real, wired, and exercised.

| Capability | Where | Notes |
|---|---|---|
| Screen capture, 3 OSes | `ghost/screen_darwin.go:21`, `screen_linux.go:28`, `screen_windows.go:72` | CoreGraphics / X11 xgb / GDI BitBlt. No cgo except macOS. |
| Mouse + keyboard injection, 3 OSes | `ghost/input_darwin.go:13`, `input_linux.go:75`, `input_windows.go:121` | Unicode typing is layout-independent on all three. |
| Accessibility tree, 3 OSes | `ghost/tree_darwin.go:18` (AX), `tree_windows.go:25` (UIAutomation via PowerShell), `tree_linux.go:24` (AT-SPI via python3) | Real semantic elements: role, name, bounds, `AutomationId`. Depth-capped at 5–6. |
| Remote Desktop over HTTP | `remotedesktop_http.go:76–219`, all 5 routes wired at `httpserver.go:737-741` | MJPEG stream + single-frame JPEG + input POST. |
| Web dashboard control | `web/components/dashboard/RemoteDesktopView.tsx:90,143-215` | True MJPEG, full mouse/kbd/scroll/modifiers, fullscreen. **The best surface today.** |
| Mobile control | `mobile/app/remote-desktop.tsx:78,89-144` | 600 ms JPEG poll (~1.6 fps), tap/drag/long-press/2-finger scroll/soft keyboard. |
| Remote verb execution | `ops.go:268-318` → `mcp_remote_proxy.go:115` → `agent_mesh_remote.go:238` | `ops(machine=B, verb="ghost_click")` genuinely fires on B. Direct-LAN then relay candidates. |
| Headless browser ghosting | `ops_ghost_web.go:61-122` | 7 chromedp verbs incl. `ghost_web_text`. Truly headless, ARM-capable. |
| WebXR scene | `web/app/spatial/vr/VRScene.tsx:15-25` | `@react-three/fiber` + `@react-three/xr`, real immersive-vr session. |
| Hands-free voice loop | `mobile/src/lib/voice/conversationCore.ts`, `car-voice-coding.tsx:326`, `glass-terminal.tsx:378` | Streaming whisper.rn STT → dispatch → TTS. Real on **car and glass only**. |
| Gateway safety machinery | `gateway_act.go:198-292`, `gateway_audit.go:141`, `gateway_creds.go:20` | Policy guard → persisted velocity cap → dry-run → confirm → vault creds → local JSONL audit. Production-grade. |
| Windows agent build | verified this session: `GOOS=windows GOARCH=amd64 go build ./...` | Compiles clean. Linux too. |

---

## 2. The seven gaps that block the goal

### Gap 1 — Voice is not connected to GUI control. At all.

This is the headline. A grep of every `desktop/agent/voice_*.go` and `ops_voice.go`
for `ghost|vision|remotedesktop|runtime_control` returns **zero functional hits**
(only the substring "provision"). Reverse direction: `ghost_stream.go` and
`ghost_vision.go` never mention voice. `mobile/app/remote-desktop.tsx` has zero
speech imports.

Voice dispatches to exactly three places, none of them the GUI:
1. **TaskManager** — `voice_dispatch.go:74` creates a task and polls 250 ms for up to 15 min.
2. **Nullary ops verbs** — `voice_control.go:425-429` builds `opsCLIRequest{Verb: act.Verb}` and **never populates `PayloadJSON`**. So `ghost_click` is name-matchable but fires with no coordinates. Only zero-argument verbs are genuinely reachable. (`run` is the exception — real arbitrary shell, gated by a spoken "confirm" at `voice_control.go:385`.)
3. **Hermes app launch** — `voice_launch.go:45`, a fixed regex intent.

The aspiration is written down but not built — `voice_mcp.go:28` comments that
`SessionID` exists so "voice intents can drive `runtime_control` on the same
target." Nothing implements it. Worse, **`voice_listen_start` and `voice_speak` are
stubs**: they broadcast BlackBox commands (`voice_mcp.go:54,81`) that **no client in
the repo listens for** — grep across mobile/watch/tvos/web returns only design docs.
They return `ok: true` and do nothing.

### Gap 2 — Two voice stacks that share nothing

- **Stack A (Go)**: ffmpeg mic → Deepgram/OpenAI/AssemblyAI/local-whisper → TaskManager → Cartesia/ElevenLabs/Deepgram/OpenAI/local TTS. CLI + `/voice/stream` WS.
- **Stack B (React Native)**: whisper.rn on-device → `POST /runner/session/turn` → expo-speech.

They share no code, no protocol, no config, no credentials. Stack B never reads
`VoiceConfig` and can't reach Stack A's providers. Any "voice drives the desktop"
work has to pick one, and the phone/glass surfaces only have Stack B.

Two advertised features are stubs:
- **Semantic completeness judge**: `adapters/localJudge.ts:75` has `const path = modelPath; if (!path) return null;` and `createVoiceCore.ts:66` never passes `modelPath`. The llama.rn judge documented in `docs/architecture/VOICE_CONVERSATION.md` **is a heuristic in every shipped build.**
- **Barge-in**: `conversationCore.ts:19-20` says outright that true open-mic barge-in "needs a native echo-cancelling capture adapter … the adapter just isn't built yet." Only tap-to-interrupt works. Stack A is worse — `voice_control.go:294-300` *drops mic frames while TTS speaks*, making interruption impossible by design.

`types.ts:31-38` declares 7 voice surfaces and the architecture doc claims
"car · phone · watch · tv · web · glass · vr". **Two are wired** (car, glass).
Watch and tvOS have TTS output only, no STT. Web, VR and tablet have nothing.

### Gap 3 — Nothing can launch or focus an application

There is **no app-launch and no window-focus verb for a desktop, on any OS.**

- `/rd/input` accepts exactly 7 event types, all pointer/keyboard (`remotedesktop_http.go:256-278`).
- The ghost `Action` vocabulary has no launch kind (`ghost/ghost.go:43-52`); `Input` is mouse+keyboard only (`:122-130`); `Tree` is read-only (`:134-138`).
- `glass_pc_open` opens a **headless-Chrome browser window**, not an app (`ops_glass_pc.go:126`). `glass_pc_focus` is advisory only and reorders nothing (`:306-317`, admitted at `:66`).
- The `open_app`-shaped verbs all target something else: `open_app` → paired phones (`mobile_session_http.go:75`), `android_app_launch` → redroid, `appletv_launch_app` → tvOS.

Practical consequence: **to open AutoCAD you must synthesize `cmd+space` and type
"AutoCAD"**, or fall back to `exec_command`. There is no affordance in any client UI.

### Gap 4 — The vision loop is single-shot, and semantics are unused

`Engine.Act` (`ghost/vision.go:86-102`) is capture → Locate → Execute. **One
iteration, no re-screenshot, no verification.** Its only caller is
`ghostLocateHandler` (`ops_ghost.go:316`). The absence is deliberate and documented
at `ghost/vision.go:83-85`: "The multi-step plan→verify→retry loop is owned by the
caller (Talos)" — i.e. by a component outside this repo.

The locator returns **one action, not a scene graph**: `ghost_vision.go:86-95`
prompts for a single `{kind,x,y,button,text,keys,dx,dy,reason}`. No element boxes,
no enumerated targets.

Meanwhile the real semantic data sits unused. `ghost_windows` returns AX /
UIAutomation / AT-SPI trees with names, roles and bounds — and `Find()` exists on
the `Tree` interface, but **no verb invokes an element by name.** For a menu-heavy
app like AutoCAD or Fusion, coordinate-guessing from a vision model is the
difference between a demo and a tool.

Note also `ops_vision.go` is **not** screen vision — it is 24×24 luma-grid camera
motion detection in pure Go (`:76-134`), for IP cameras. Misleading filename.

### Gap 5 — No business-data connectors ship. Zero.

The gateway is a genuinely well-engineered **empty frame**. ~12k lines of registry,
OAuth/PKCE, vault creds, dry-run, velocity caps, human gates, self-heal, local
JSONL audit — and **not one connector manifest exists in the repo.** No seed, no
template, no catalog, no "connect HubSpot" button. `~/.yaver/connectors/*.json` must
be hand-authored at runtime.

CRM/ERP specifically: grep for `salesforce|hubspot|odoo|netsuite|zoho|netsis|mikro|
logo tiger|dynamics` across all Go/TS returns **six hits, all non-integration** — a
comment at `ops_ghost_web.go:7-8` naming them as targets, and test fixtures using
"Logo Tiger ERP" as an example string. The ERP thesis lives in
`docs/yaver-talos-ghost-erp-migration.md` as design, not code.

Structural limits even once connectors exist:
- Only two engines are permitted — `api` and `redroid` (`gateway_registry.go:364-369`). The `device` engine is declared (`:135`) and matched against (`gateway_phone_inventory.go:194`) but **rejected by validation — unreachable code.**
- **There is no `web` or `ghost` flow type.** So `ghost_web_*` / `browser_*` / `selenium_*` bypass answer-schema projection, audit, consent and policy guard entirely. The GUI fallback is not a gateway path.
- `projectAnswer` is dotted-path only (`gateway_invoke.go:15-19`). No pagination, no incremental sync, no schema discovery. "List 400 opportunities since Tuesday" cannot be expressed.
- The intent router scores only against **registered** capabilities with a hard threshold of 2.0 (`gateway_intent.go:90-125`) — with an empty registry every business utterance falls through to the coding agent.

### Gap 6 — No CAD, no LaTeX, no presentations, no Office

- **`ops_cad.go` is not CAD.** It is OpenSCAD + a slicer for 3D printing: `cad_render` shells `openscad -o x.png/x.stl`, `cad_slice` shells Orca/PrusaSlicer (`:88,:185`). Known formats are png/stl/3mf/gcode (`:321`). **AutoCAD, Fusion, .dwg, .dxf, .step, .iges: all zero hits repo-wide.** The filename oversells; the header comment is honest.
- **LaTeX: zero.** The only hit for `pdflatex|documentclass|beamer|pandoc` is `pdfgen.go:6` — a comment *disclaiming* it ("without standing up LaTeX"). `pdfgen.go` is chromedp HTML→PDF print.
- **Presentations/Office: zero.** No pptx, keynote, docx, xlsx, marp, reveal, libreoffice. The only "Excel" strings are screenlog test fixtures (`screenlog_emulator.go:31`).
- **`ops_studio.go` is not a document studio** — it is Play/App Store permission-justification asset generation.
- **No scripting bridge is exposed.** `osascript` is used internally for *reading* the active window (`screenlog_window.go:35`) and one notification; PowerShell for capture and the UIA tree; `xdotool` for reading a window name. **None are exposed as a send-command verb.** No AutoLISP, no Fusion Python API, no COM automation, no `wmctrl`/`ydotool`/hammerspoon. So Excel's and Fusion's actual automation APIs are unreachable except through raw shell.

The real escape hatch is `exec_command` + `files`: you *could* today `write_file` a
`.tex` and `exec_command "pdflatex paper.tex"` on a remote box. But `read_file` caps
at 100 KB and returns text, and the only base64 artifact path (`cad_get`) is
hard-scoped to `~/.yaver/cad/` (`ops_cad.go:241`) — **a compiled PDF or a .dwg has no
clean way home.**

### Gap 7 — AR/VR is two disconnected halves, neither showing a PC

- **`mobile/app/glass-*.tsx` — only two exist**, `glass-terminal.tsx` and `glass-workspace.tsx`. Both are **plain 2D React Native**; no three, no WebGL. Neither references `/rd/` or `remoteDesktop`. They are a high-contrast terminal and an i3-style tiling pane view.
- **The real XR lives on the web at `/spatial`** — `VRScene.tsx` with R3F + `@react-three/xr`, `EnterVRButton.tsx:24` feature-detecting `immersive-vr`. But `RemoteWindow3D` shows a **remote browser window**, not the PC desktop, and **no client anywhere calls `glass_pc_open`** — the scene only polls `/remote-runtime/sessions` and filters for pre-existing browser sessions (`useAgentBridge.ts:158-166`). Windows must be created out-of-band via MCP/CLI.
- **Hand-tracking, gaze, pinch, spatial anchoring: not implemented.** Only prose mentions them (`VoiceOrb3D.tsx:8-9`); the pointer behaviour is inherited free from R3F. `XROrigin` is a fixed `[0,0,0]` (`VRScene.tsx:66`). No `useXRHandState`, no `XRHand`, no ARKit, no RealityKit, no OpenXR anywhere.
- **The native visionOS app is flat** — plain SwiftUI `WindowGroup` (`YaverVisionApp.swift:8,15`), no `ImmersiveSpace`, no RealityKit, no remote desktop.
- **tvOS / watchOS / Wear OS have no remote desktop at all** — grep for `/rd/|RemoteDesktop` returns zero.

Both XR design docs are explicit that they are design-only: `docs/xr-spatial-design.md`
("Design only — no implementation") and `docs/xr-normie-use-cases.md` ("Status: ideas
only. No implementation."). That is accurate and worth respecting.

---

## 3. Per-OS reality matrix

| Capability | macOS (cgo) | macOS (CGO=0) | Linux X11 | Linux Wayland | Linux headless | Windows |
|---|---|---|---|---|---|---|
| Screen capture | REAL | **STUB** | REAL | **ABSENT** | ABSENT | REAL |
| Multi-monitor | ABSENT | ABSENT | ABSENT | ABSENT | — | ABSENT |
| Mouse / scroll | REAL | STUB | REAL | ABSENT | ABSENT | REAL |
| Double-click / drag fidelity | REAL | STUB | PARTIAL | ABSENT | ABSENT | PARTIAL |
| Unicode typing | REAL | STUB | REAL | ABSENT | ABSENT | REAL |
| Key chords | REAL | STUB | **PARTIAL** | ABSENT | ABSENT | REAL |
| Input error reporting | **ABSENT — always nil** | REAL | REAL | — | — | REAL |
| Permission preflight | ABSENT | — | XTEST probe only | ABSENT | ABSENT | ABSENT |
| Accessibility tree | REAL (AX) | STUB | REAL (AT-SPI, needs `python3-pyatspi`) | REAL (AT-SPI) | REAL if bus | REAL (UIA) |
| App launch / window focus | ABSENT | ABSENT | ABSENT | ABSENT | ABSENT | ABSENT |
| Agent compiles | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ (verified) |
| Covered by CI | partial | — | partial | — | — | **NONE** |

**The Windows GUI path has never been tested anywhere.** Verified this session:
`GOOS=windows GOARCH=amd64 go build ./...` in `desktop/agent` succeeds, and
`GOOS=windows go list -deps .` confirms `github.com/yaver-io/agent/ghost` is in the
Windows build graph — so the code compiles and ships. But the **only** `windows-latest`
runner in all 80 workflows is `release-installer.yml:106`; there is no Windows job for
the Go agent in `ci.yml` or `test-suite.yml`. Combined with `TestTreeStubUnsupported`
(`ghost/ghost_test.go:134`) being unable to pass on a Windows build, the Windows
screen-capture, `SendInput` injection and UIAutomation-tree code is **compile-checked
only, never executed by CI**. The 11 `*_windows.go` files in `desktop/agent` are all
infrastructure (vault lock, process, systemd finalize, runner detach) — none of them
GUI. For a goal whose primary target is "connect to my remote Windows machine", this
is the largest unquantified risk in the audit: the code looks complete and has no
evidence behind it.

Three per-OS landmines worth naming:

1. **Wayland is wholly unhandled in `ghost/`** — zero hits for "wayland" in the package. `xgb.NewConn()` will succeed via XWayland and `GetImage` on the XWayland root returns *no native-Wayland client content*. This is silent, not an error. Modern Ubuntu/Fedora default to Wayland. Note the sibling `screenlog_capture.go:9` **does** handle Wayland — the inconsistency is internal.
2. **macOS silently degrades under `CGO_ENABLED=0`** — the build tag at `input_other.go:1` is `!windows && !linux && (!darwin || !cgo)`, so a cgo-less macOS build gets `ErrUnsupported` stubs. Given the package header's stated CGO-off goal (`ghost/ghost.go:12`), this is a packaging footgun.
3. **`Supported()` lies.** It returns the compile-time constant `platformSupported` (`ghost/ghost.go:164`), unconditionally `true` on Linux and Windows. `/rd/status` reports `"supported": true` on a headless Linux box or a TCC-denied Mac.

---

## 3b. Transport and runners — how a phone actually reaches the box

### The path that works

Phone `fetch` → relay `/d/<deviceId>/ops` → agent tunnel client re-issues against
`http://127.0.0.1:18080` stamping `X-Yaver-Via-Relay: 1` (`main.go:11562,11582`) →
`httpserver.go:533 /ops` → `ops.go:187 dispatchOps` → verb handler. If `machine !=
"local"`, `dispatchOps` re-proxies to a second box (`ops.go:287`). **This is fully
wired** and is how a remote `ghost_click` genuinely fires.

**The MCP server always lives on the target box** — never on the phone, never on a
runner. Three possible instances, all agent-side: HTTP `/mcp` (`httpserver.go:1519`),
`yaver mcp --mode=http`, and per-runner stdio children. The phone is always a client.

Two naming traps worth knowing:
- **`mobile/src/lib/quic.ts` is not QUIC.** Its own header (`:1-6`) says it is "a placeholder implementation that uses HTTP as a fallback transport until a native QUIC module is available for React Native." Every mobile call is `fetch()`.
- **`desktop/agent/quic.go` is legacy and irrelevant here.** Its protocol supports exactly four messages — `task_create/stop/list/continue` (`quic.go:318-329`). **No ops, no MCP.** Its only client is the Go CLI, with `InsecureSkipVerify: true` (`client.go:35`), against a cert whose only SAN is `127.0.0.1` (`quic.go:497`).

### Per-client transport reality

| Client | Direct LAN | Relay | Blocker |
|---|---|---|---|
| iOS app | ✅ | ✅ | ATS globally disabled in-app (`Info.plist:69-74`), so cleartext works |
| **Android release** | ❌ | ✅ | **`usesCleartextTraffic` appears only in the `debug` and `debugOptimized` manifests** (`src/debug/AndroidManifest.xml:6`), not `src/main`. Plain-`http://` LAN legs are OS-blocked in production builds |
| Web browser | ❌ | ✅ | Mixed content. `web/lib/agent-client.ts:3626-3640` doesn't even try: "Browsers block fetch from `https://` origins to `http://` targets" |
| tvOS | ✅ only | ❌ | `SessionClient.swift:63` and `MachineRegistry.swift:167-178` hardwire `http://<host>:<port>`. **LAN-only**, despite `BoxLifecycle.swift:61` narrating "Connecting over the free relay…" |
| watch / Wear | — | — | No independent transport; they ride the phone |

### Runners: three, not four — and none of them run on Windows

`tasks.go:145` defines exactly `claude`, `codex`, `opencode` (`supportedRunnerIDs`,
`tasks.go:247`). GLM/Ollama/OpenRouter arrive via opencode's BYOK config, not as
runners. **`glm` is still advertised as a fourth runner** in verb descriptions
(`ops_runner_turn.go:5,36,46`, `runner_keeper_mcp.go:4`) — stale copy.

All three spawn with a permissions-bypass flag (`tasks.go:166,191,206`) — deliberate
and documented, but worth naming out loud.

**A runner has full, unrestricted access to its box's entire MCP/ops surface, set up
automatically.** `autoSetupMCP` runs silently during `yaver serve` and registers
`{"command": yaverPath, "args": ["mcp"]}` into all three CLIs (`mcp-setup.go:510-527`).
The runner operates at `caller="owner"` with no connector allowlist. And because
`dispatchOps` honours `machine`, **a runner on box A can drive box B** — the only
brake is `refuseRemoteLayer4` blocking vault/SDK-token tools from crossing machines
(`mcp_remote_proxy.go:40-65`). Stated plainly: *the coding agent you launch on your
box is by construction a fully-privileged owner of your whole fleet, minus secrets.*

### 🔴 Windows has no runner sessions at all

This is decisive for a goal whose premise is "utilizes runners". Beyond the untested
GUI code in §3:

| Feature | Windows | Evidence |
|---|---|---|
| Runner PTY / `/ws/runner` | ❌ **broken at runtime** | `runner_pty.go:153` calls pty start; `creack/pty@v1.1.18/start_windows.go:17` returns `ErrUnsupported` unconditionally. tmux is also absent (`tmux.go:104`) |
| `runner_turn` / `runner_sessions` verbs | ❌ | Built entirely on tmux PTY sessions (`ops_runner_turn.go:3-10,149`) |
| Detached autodev | ❌ stub | `runner_detach_windows.go:24-28` prints "detach not supported on Windows yet" and returns `(0,"")` |
| Durable OS service units | ❌ stub | `managed_units_windows.go:20` — `durableUnitsSupported() → false`; `removeManagedUnit` is a **silent no-op** |
| Process-group kill | ⚠️ degraded | `exec_platform_windows.go:14-21` kills only the single PID, orphaning grandchildren |

So on Windows the agent serves HTTP, authenticates, and exposes ops verbs — but the
interactive runner layer, the thing that would actually *do the work*, does not
function. Combined with zero Windows CI, **"connect to my remote Windows machine and
have an agent operate it" is the single least-supported configuration in the repo.**

### 🔴 The GUI has no concurrency lease

`runtime_take_control` / `runtime_release_control` / `runtime_lease_status` are real,
tested single-writer leases (`remote_runtime_lease.go:37-117`, 60 s idle timeout) —
but they arbitrate **emulator/simulator/browser-preview sessions**, not the desktop.
The target enum (`remote_runtime_target.go:64-90`) has no desktop-screen entry, and
the design comment (`remote_runtime_lease.go:5-8`) is about "phone + TV fighting over
one session."

Remote Desktop and the GUI ghost have **zero arbitration**.
`handleRemoteDesktopInput` (`remotedesktop_http.go:219-283`) checks policy and replays
events straight into `eng.Input` — no holder, no clientId, no lease. Two phones drive
one desktop simultaneously, last-writer-wins. **The two safety mechanisms landed on
opposite subsystems**: GUI control has consent+audit but no lease; the runtime layer
has a lease but no consent policy.

Also: the lease verbs are **MCP-only, never registered as ops verbs**, so they are
unreachable from the phone's `/ops` surface and from an elevated MCP connector
(`oauth_mcp.go:69-76`). And `runtime_stop`/`runtime_frame` hardcode
`http://127.0.0.1:18080` (`remote_runtime_mcp.go:44`) — local-box-only despite reading
as remote.

### MJPEG over relay will not survive contact

Mechanically it works; operationally it is a demo, not a product:

- **Hard 15-minute cut.** The agent's tunnel client sets `Timeout: 15 * time.Minute` (`main.go:11376,11422`). MJPEG isn't in the SSE detector list — which is triplicated across three files with "KEEP IN SYNC" comments (`relay/server.go:1856-1865`, `relay/tunnel.go:290`, `main.go:11628-11637`) and lists only `/output`, `/dev/events`, `/subscribe`, `/blackbox/*`, `/feedback/stream`, `/streams/`. So a stream falls to the regular-request branch and survives only because `writeStreamingResponse` chunks — **by accident, not design**, and still dies at 15 min.
- **WebSocket-fallback tunnels reject streaming outright** — `502 "streaming endpoints require QUIC relay"` (`relay/server.go:1758-1771`).
- **Holds a device concurrency slot for its entire life** (`relay/server.go:1707-1712`).
- **Bandwidth is recorded as literal zero** — `RecordBytes(deviceID, bytesIn, 0, relayPaid)` with the comment "treat streaming outbound as best-effort" (`relay/server.go:1889`). MJPEG's whole cost is invisible to quota. Given `CLAUDE.md`'s cost-awareness rule, that is a product bug, not just an accounting one.
- **Nothing adapts.** `ghost_stream.go:51` hardcodes quality 55; the capture loop (`:62-82`) has no backpressure, no client-count awareness, no feedback. The only adaptive encoder in the tree is `remote_runtime_webrtc.go:562-574` (quality 55→35 to fit a data-channel frame, plus a 720px downscale) — and it serves the runtime layer, not the desktop.

The one genuinely good piece here: transport selection is measured, not guessed —
`transportKindRank` (`agent_mesh_remote.go:47-70`) orders same-lan < mesh < tailscale
< direct < cloudflare-tunnel < hostname < relay, `remoteAgentCandidateScore` folds in
probe RTT (`:126-147`), and `orderRemoteAgentCandidatesByLiveness` races parallel
`/health` probes on a 1.5 s budget (`:711-778`). It also correctly distinguishes the
Yaver mesh overlay `100.96/12` from Tailscale's `100.64/10` CGNAT range (`:506-541`).

---

## 4. Security findings — verified directly, not delegated

Three of these are serious enough to fix regardless of whether this goal is pursued.

### 4a. macOS input silently succeeds when Accessibility is denied 🔴

Every method on `macInput` returns `nil` unconditionally (`ghost/input_darwin.go:100-134`)
because the C helpers are `void` and `CGEventPost` has no return value. A TCC-denied
Mac answers `POST /rd/input` with `{"ok":true,"applied":N}` (`remotedesktop_http.go:285-295`)
while injecting nothing. Linux and Windows both surface real errors. The operator gets
no signal their clicks are going nowhere — this will read as "Yaver is broken and
lying" in exactly the first-run scenario where it matters.

### 4b. `POST /rd/policy` has no locality gate 🔴

Confirmed by reading `remotedesktop_http.go:104-148`: the handler checks only
`s.auth`. It records `Remote: !isLoopbackAddr(...)` in the audit entry at `:138` but
**never enforces it**. A remote caller can set `ControlEnabled: true` and immediately
drive the box. The documented "view is default-on, control is opt-in from the box
itself" two-tier model (`remotedesktop.go:17-18`) collapses to a single identity
check with a self-grant path. There is no physical-presence confirmation anywhere.

### 4c. `/rd/input` is reachable with a browser-session token 🟠

Confirmed at `browser_session.go:112-113`. The `/rd/` entry is prefix-scoped and its
own comment explains it was added because "live media views … render with an
`<img>`/EventSource that can't carry headers." That rationale covers **GET viewing
only** — but the prefix also covers `POST /rd/input` (injection) and `POST /rd/policy`
(self-grant). Minting is owner-authed, and the token is 2-minute path-scoped, so this
is privilege *breadth*, not a bypass — but the allowlist should be per-route+method.

### 4d. `gateway_act_confirm` defeats its own two-key model 🔴

Confirmed: `gateway_act_mcp.go:154` passes `PreApproved: true` (comment: "the explicit
confirm call is the second key"), and `gateway_act.go:243` skips the entire
confirm/tap block when it is set. **Both calls are made by the same agent** — so an
MCP client calls `gateway_act` then `gateway_act_confirm{answer:"approve"}` and a
*financial* act executes with **no phone tap**, despite `RequiresTapKey: true` in the
preview and the user-facing promise "you'll still tap to confirm"
(`gateway_act_mcp.go:107`). The second key is the same key. This is outside the
present goal but is the most serious thing the audit found.

### 4e. Lesser, but real

- **Screen capture never stops.** There is no `/rd/stop` route and `ghostStream.stop()` is `--ghost`-gated (`ghost_stream.go:244-248`). One `/rd/frame.jpg` thumbnail request pins a full-rate capture loop for the remainder of the process lifetime, with zero viewers.
- **Long-lived streams never re-check consent** (`remotedesktop_http.go:172-194`). Revoking `ViewEnabled` does not cut active viewers.
- **Audit gaps.** The `view` action is defined (`remotedesktop.go:167`) but never emitted — screen viewing is entirely unaudited. The `control` record is written *inside* the 5-minute notify throttle (`:319-326`), so a sustained remote-control session logs at most one entry per 5 minutes.
- **Policy load fails open** — any read/parse error silently returns defaults, with view **ON** (`remotedesktop.go:100-106`).
- **Shared global stream state.** `ghostStream` is one package-level instance (`ghost_stream.go:32`); whichever of `/ghost/*` or `/rd/*` starts first pins fps for both.

---

## 5. Doc + test drift found along the way

Fix these in whatever change touches them (`CLAUDE.md`: "when the doc and the code
disagree, the doc is the bug").

| Claim | Where | Reality |
|---|---|---|
| "macOS/Linux are stubbed with ErrUnsupported until Phase 2" | `ghost/ghost.go:9-11` | All three OSes implemented since 2026-06-04 |
| "`ghost_windows` … Phase 2; returns unsupported on Phase 1" | `ops_ghost.go:129` | Real on all three OSes — **understates a shipped capability, which will mislead a model reading tool descriptions** |
| "stubs in tree_linux.go" | `ghost/tree.go:3-5` | `tree_linux.go` is a full AT-SPI2 implementation |
| `TestTreeStubUnsupported` asserts `ErrUnsupported` | `ghost/ghost_test.go:134-138` | **Cannot pass on Linux or Windows builds.** Passes on macOS+cgo only by accident when no app is focused |
| voice surfaces "car · phone · watch · tv · web · glass · vr" | `docs/architecture/VOICE_CONVERSATION.md:47` | Two wired (car, glass) |
| semantic on-device judge | same doc | Heuristic in all shipped builds (`localJudge.ts:75`) |
| `ops_cad.go` filename | — | OpenSCAD 3D-printing pipeline, **overstates** |
| `screenlog_live` "works remotely over relay/mesh" | tool description | Tool declares no `device_id` and never proxies |

---

## 6. If this is to be built — the shape of the work

Not a plan to execute, just the honest dependency order. The two starred items are
the ones that turn seven components into one product.

**Tier 0 — correctness/safety first, small and independent**
1. Make macOS input report real errors; add `AXIsProcessTrusted` / `CGPreflightScreenCaptureAccess` preflight and surface it in `/rd/status` (4a).
2. Gate `POST /rd/policy` on locality, or require an on-box confirmation to raise `ControlEnabled` (4b).
3. Scope the browser-session allowlist per route+method (4c).
4. Fix the `gateway_act_confirm` second key (4d) — independent of this goal, do it anyway.
5. Add a `/rd/stop` + refcounted teardown; re-check policy inside the stream loop.
6. **Give the desktop GUI a control lease.** `remote_runtime_lease.go` is already a tested single-writer lease with idle-expiry and force-steal — reuse it in `handleRemoteDesktopInput` rather than writing a second one. Without this, two clients on one machine is undefined behaviour.
7. Fix the Android release manifest (cleartext opt-in exists only in debug), or accept that production Android is relay-only on LAN.

**Tier 1 — the two missing primitives** ★
6. **`desktop_app_launch` / `desktop_window_focus` verbs.** Per-OS: `open -a` + AX raise / `ShellExecute` + `SetForegroundWindow` / `gtk-launch`+`wmctrl` or AT-SPI. Without this nothing can start AutoCAD.
7. **`ghost_element_*` — act on the accessibility tree by name.** The tree already exists on all three OSes; `Tree.Find()` already exists. Exposing `ghost_click_element(name)` converts blind coordinate-guessing into reliable menu navigation, and is the single highest-leverage change in this document.

**Tier 2 — the loop**
8. Multi-step act→screenshot→verify→retry inside the agent (today it is explicitly delegated to out-of-repo Talos, `ghost/vision.go:83-85`). Prefer tree-assertions over pixel-diffs for verification.
9. A `machine`-parameterized first-class MCP **image** tool for the desktop screen, following the `robot_camera` / `circuit_plot` pattern (`mcp_tools.go:3220`). Today `ops(machine=B, verb="ghost_screenshot")` returns base64 **inside a JSON text block** (`httpserver.go:12902`) — exactly the failure that pattern was invented to avoid.

**Tier 3 — voice**
10. Pick one voice stack. Give voice a payload path (`voice_control.go:426` never sets `PayloadJSON`) so it can carry arguments to a verb.
11. Either implement the `voice_listen_start`/`voice_speak` client listeners or delete the stubs — a verb that returns `ok:true` and does nothing is worse than an absent one.
12. Build the echo-cancelling capture adapter that barge-in is already wired for.

**Tier 4 — data and apps**
13. Add a `web` flow type to `CapabilityFlow` and route it to the browser manager, reusing `projectAnswer` — this is what makes `ghost_web_*` a *governed* data path rather than a bare verb. The extractor contract already exists; only the wiring is missing.
14. Ship at least one real connector end-to-end before claiming any CRM/ERP story.
15. Expose scripting bridges as verbs (`osascript_run`, PowerShell/COM, Fusion Python) — for apps that have an automation API, this beats GUI-driving by an order of magnitude in both reliability and latency.
16. An artifact-return channel for arbitrary binaries (generalize `cad_get` beyond `~/.yaver/cad/`), or LaTeX/CAD output can't get back to the phone.

**Tier 5 — XR**
17. Point the existing `/spatial` WebXR scene at `/rd/stream` instead of only remote browser windows. This is comparatively small — the scene, the stream, and the input path all exist; they have never been connected.

**Deliberately not recommended:** MJPEG will not survive as the transport for a
usable desktop (full-frame JPEG, quality hardcoded at 55, fps clamped 1–10 with no
client control on `/rd/*`, two unsynchronised tickers adding up to 2× frame-interval
latency, no change detection, no adaptive bitrate, no multi-monitor). It is fine for
"glance at the box." For "work in AutoCAD from a headset" it needs a real codec —
and the repo already has WebRTC infrastructure (`native-webrtc`, `glass_pc_open`,
`remote_runtime.go`) that would be the place to start rather than a new stack.

---

## 6b. Sizing, critical path, and the smallest honest first slice

§6 lists *what* is missing. This section is *how much*, and *in what order* — so the
decision to build (or not) can be made on numbers rather than vibes. Estimates are
engineering-days for someone fluent in this codebase, and assume no unknown-unknowns
in the untested Windows GUI path (which is itself the largest estimation risk here).

### The critical path is shorter than the gap list suggests

Of the seven blockers, **only three are on the critical path to a first working
demo**. The rest are breadth, not depth:

| # | Work | Size | Why it's on the path |
|---|---|---|---|
| 1 | `desktop_app_launch` + `desktop_window_focus` verbs | **3–5 d** | Without it nothing can start an app. Per-OS: `open -a`+AX raise / `ShellExecute`+`SetForegroundWindow` / `gtk-launch`+wmctrl. Mechanically simple, three implementations. |
| 2 | `ghost_element_*` — act on the AX/UIA/AT-SPI tree by name | **5–8 d** | The trees and `Tree.Find()` already exist on all three OSes. This is wiring + a coordinate-resolution step, not new platform code. **Highest leverage in the document.** |
| 3 | Act→verify→retry loop with tree assertions | **5–8 d** | Today's single-shot `Engine.Act` cannot recover from a misclick. Assert on tree state, not pixels. |
| 4 | Voice payload path + one dispatch target | **3–5 d** | `voice_control.go:426` never sets `PayloadJSON`. Fixing that plus routing to the new element verbs is the whole "say things to your PC" story. |
| 5 | Desktop control lease (reuse `remote_runtime_lease.go`) | **1–2 d** | Cheap, and undefined behaviour without it. |
| 6 | Tier-0 security fixes (§6, items 1–5, 7) | **3–5 d** | Independent, small, should land regardless. |

**Critical path total: ~20–33 engineering-days** for "talk to your Mac or Linux box
from a phone and have it reliably open and drive an app." That is the real number,
and it is smaller than the seven-blocker framing implies — because items 1–3 all
build on platform code that already exists and is already tested on two of three OSes.

### What is NOT on that path (and should be deferred)

- **CRM/ERP connectors** — the `api` engine already works; one real connector is ~2–3 d *once someone picks a vendor*. Deferring costs nothing.
- **CAD / LaTeX / Office** — `exec_command` + `files` already drive any headless CLI today. The missing piece is an artifact-return channel (~2 d, generalize `cad_get`), not integration work. GUI-driving AutoCAD is a *consequence* of items 1–3, not separate work.
- **AR/VR** — pointing the existing `/spatial` WebXR scene at `/rd/stream` is ~2–3 d. The scene, the stream and the input path all exist and have simply never been connected. Do it last; it's a demo multiplier, not a capability.
- **Second voice stack unification** — pick one, ignore the other. Merging is weeks and buys nothing until item 4 proves the loop.

### The two items that are large and unavoidable *if Windows is first*

| Work | Size | Note |
|---|---|---|
| Windows runner layer (PTY without creack/pty, or ConPTY; session model without tmux) | **10–20 d, high variance** | This is a port, not a fix. ConPTY is the likely route. Detached autodev and service units are additional. |
| Windows GUI validation (first-ever execution of the GDI/SendInput/UIA code + CI) | **5–10 d, unbounded downside** | The code has never run in CI. `TestTreeStubUnsupported` cannot pass there. Budget for discovering real bugs, not confirming correctness. |

**So: macOS-or-Linux-first is ~20–33 d. Windows-first is ~35–63 d** with materially
worse variance. That ratio is the single most important number in this document.

### Recommended smallest honest slice

If the goal is to learn whether this product is real, the cheapest decisive
experiment is:

> **One OS (macOS or Linux). One app. Voice → element-tree click → verify → speak the
> result.** No AR/VR, no CRM connector, no Windows, no second voice stack.

That is items 1–5 above (~17–28 d) and it answers the only question that matters:
**does tree-driven GUI automation hit a reliability bar a human will tolerate?**
`docs/yaver-personal-assistant-audit.md` already flags UI-driving at 10–30 s/task as
existential risk versus Alexa's ~1 s, and rates reliability "<95% = trust death."
Items 2 and 3 exist precisely to beat that number. If they don't, no amount of AR/VR
or connector work saves it — and everything deferred above was correctly deferred.

**Do not start with breadth.** The failure mode this codebase is already exhibiting
is seven strong components and no spine; adding an eighth component makes it worse,
not better.

---

## 7. Open questions for the user

1. **Which OS first?** The gaps are not symmetric, and Windows is the trap: it has the best accessibility tree (UIA `AutomationId`) and honest input errors, but zero CI, unvalidated GUI code, and **no working runner layer at all**. macOS has the best input fidelity and the silent-permission bug. Linux is X11-only with a Wayland cliff. If Windows is genuinely the target, the runner-on-Windows work (§3b) is a prerequisite, not a follow-up — and it is a bigger job than anything in Tier 1.
2. **API-first or GUI-first?** For CRM/ERP the `api` engine + a real connector is dramatically more reliable than GUI-driving. For AutoCAD/Fusion the GUI (or their scripting APIs) is the only option. These are different projects with different risk profiles.
3. **Is the AR/VR surface the phone-driven XREAL model** (per `docs/xr-spatial-design.md`, phone = brain, glasses = display) **or a standalone headset?** The former reuses the existing RN stack; the latter needs the flat visionOS app rebuilt around `ImmersiveSpace`.
4. **Latency tolerance?** `docs/yaver-personal-assistant-audit.md` already flags UI-driving at 10–30 s/task as "existential" risk versus Alexa's ~1 s. That analysis applies directly here and is worth re-reading before committing.
