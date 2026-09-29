# Yaver Operator — operating your whole computer without opening it

**Deep audit · 2026-09-29 · audit only, no code changed.**

> **Method.** Every claim below was grepped or read out of the tree on `main` on
> **2026-09-29**. Line numbers are anchors, not contracts. Per `AGENTS.md` /
> `CLAUDE.md`: **markdown drifts, the code is the source of truth** — re-grep
> before acting on any line here, and fix this doc in the same change that makes
> it stale. Where a number is an approximation it is labelled as one.
>
> **Predecessor:** `docs/yaver-remote-pc-operation-audit.md` (2026-07-19). This
> document re-verifies that audit's seven gaps against the current tree. Several
> are **closed** (voice→GUI, app launch, element-tree actuation, the desktop
> control lease, WebRTC desktop transport, the Windows runner seat). This doc is
> the current state, not a copy of the July one.
>
> **Related:** `docs/personal-assistant-automation-infra-audit.md`,
> `docs/yaver-personal-agent-gateway.md`,
> `docs/yaver-anywhere/strategy-and-reality.md`,
> `docs/architecture/FAILURE_PLUMBING_ARCHITECTURE.md`.
>
> **Landed since this audit (same day):** the macOS ghost now actually ships
> (`CGO_ENABLED=1` on darwin), permission truth + honest macOS input errors
> (`ghost.Preflight()`, `/rd/status.permissions`), the `/rd/policy` control
> self-grant is closed, the desktop is exposed as discoverable MCP tools
> (`desktop_screenshot` / `desktop_act` / `desktop_elements` /
> `desktop_operator`), the first bounded operator loop exists
> (`ghost/loop.go` `RunLoop`) with **tree-grounded completion** (`Engine.TreeText`
> + `visionLocator.VerifyGoal`), the **artifact-return channel** exists
> (`artifact_fetch`), and one shared vision config now drives both analysis and
> grounding. See the status table in
> `docs/planning/computer-use-roadmap-2026-09-29.md`. The gap analysis below is
> the *pre-change* snapshot and remains the backlog.

---

## 0. The goal being audited

The user's stated vision, paraphrased:

> **A tool that lets me do everything I do on my computer — development, and not
> just development, but browser use and all of it — without ever opening the
> computer.** Using AI (opencode / deepseek / Claude Code / Codex) with vision
> where needed, browser automation, and everything else.

Restated as a product contract:

> From any Yaver surface (phone, watch, TV, car, glass, web, CLI, or another
> agent via MCP), I can **see** my machine, **start/operate apps** (including a
> browser), **read and change** things, and **have an AI do multi-step work on
> it**, on any OS, with the safety and auditability of an owner-gated control
> plane.

The repo already frames the same thing two ways:

- `docs/yaver-anywhere/strategy-and-reality.md` — "the identity-bound control
  plane that turns any compute you already own or rent … into a streamable,
  agent-drivable, human-gated runtime."
- `docs/yaver-personal-agent-gateway.md` — "an AI-driven CRUD layer over every
  credentialed app/service the user already uses."

This audit measures the distance between that frame and the code.

---

## 1. Verdict in one paragraph

**The spine now exists, mostly.** Since the July audit, Yaver grew the pieces
that were missing: a **WebRTC desktop target** with a single-writer control
lease and consent (`remote_runtime_desktop.go`), **app launch + focus** verbs
(`ghost_launch_app` / `ghost_focus_app`), **element-tree actuation by name with
closed-loop verification** (`ghost_elements` / `ghost_click_element` with
`expect` / `ghost_type_into_element` with read-back), a **spoken desktop-control
entry point that reaches every surface including watch/Wear/tvOS**
(`desktop_voice`), **vision adaptation for text-only models** so
opencode/deepseek can "see" (`mcp_vision.go`), and a **Windows ConPTY runner
seat** (`pty_master_windows.go`, `windows_seat.go`). A full **browser-automation
toolset** (CDP + WebDriver + Selenium + headless web-ghost + human-in-the-loop
co-browse with persistent profiles) ships today.

What is still missing is the *product* around the spine: there is **no single
computer-use agent loop** (observe → decide → act → verify → retry) that a
model can drive across apps and the browser; the desktop primitives are **not
discoverable MCP tools** (only the generic `ops` verb namespace); the browser
and GUI lanes **bypass the governance gateway entirely** (no `web`/`ghost` flow
type, zero connectors shipped); **macOS input fails silently** when
Accessibility is denied; `/rd/policy` still lets a **remote caller self-grant
control**; and desktop **video** control exists on web + mobile only — the
watch/TV/Wear surfaces get speech-only desktop control, not the screen. The
honest status is **"the hands exist and mostly work; the operator loop, the
governed browser path, and cross-surface parity do not."**

---

## 2. What we have (verified inventory)

Anchors are `file:line`. Counts are measured, not quoted from docs.

### 2.1 Development, remotely — the mature core

This is Yaver's original and strongest lane. It is not the point of this audit,
but it is the baseline the "operator" vision extends.

| Capability | Where |
|---|---|
| Coding tasks via **claude / codex / opencode** runners | `desktop/agent/tasks.go` (`supportedRunnerIDs`); opencode supports any BYOK provider/model incl. deepseek |
| Live opencode console streamed to mobile/web | `/tasks/{id}/output?rawSince=` + `onRaw` SSE; `desktop/agent/opencode_stream.go` |
| Dev server start/stop + hot reload (Expo/Flutter/Vite/Next) | `/dev/start`, `/dev/reload`, `/dev/build-native` |
| Hermes bundle push into the phone container | `/dev/build-native`; `ExpoReactNativeFactory` |
| Native WebRTC preview of simulators/emulators/devices | `runtime_create` with `ios-simulator`, `android-emulator`, `native-webrtc`, … |
| Browser preview of a web dev server | `/dev/web-preview/*`, `browser-window` runtime target |
| Git / files / exec / tmux / deploy / builds | `/git/*`, `/files/*`, `/exec`, `/tmux/*`, `/deploy/*`, `/builds` |
| Task-scoped runner controls (`/model`, `/exit`) | `/tasks/{id}/control`; `desktop/agent/task_presentation.go` |
| Autonomy: autorun, routines, schedules, cron, wake | `/autoruns/*`, `routines_mcp.go`, `/schedules`, `cron_*`, `/wake` |

**Fact:** the agent currently registers **884 unique HTTP routes**
(`rg 'HandleFunc\("' desktop/agent/*.go`, unique strings) across **2,155 Go
files** in `desktop/agent/`. This is a very large surface; the operator lane is
a slice of it.

### 2.2 Operate the desktop GUI — the ghost + Remote Desktop + desktop-screen lane

**Cross-OS "ghost" engine** (`desktop/agent/ghost/`): screen capture, mouse +
keyboard injection, and an accessibility tree on macOS (AX), Windows
(UIAutomation via PowerShell), and Linux-X11 (AT-SPI via python3).

- Interfaces: `ghost/ghost.go:112` (`Screen`), `:122` (`Input`), `:134`
  (`Tree`); action vocabulary `ghost.go:43` (move/click/double/drag/scroll/
  type/key).
- **macOS** input via CoreGraphics CGEvent (`ghost/input_darwin.go:13`), capture
  via CoreGraphics (`ghost/screen_darwin.go`), AX tree
  (`ghost/tree_darwin.go:18`). **Requires cgo**; a `CGO_ENABLED=0` macOS build
  gets the unsupported stub.
- **Windows** input via `SendInput`, capture via GDI, tree via UIAutomation
  PowerShell (`ghost/tree_windows.go:25`).
- **Linux-X11** via XTest/xgb; AT-SPI tree (`ghost/tree_linux.go:24`).

**Ops verbs that expose the ghost** (all reachable from any surface via the
generic `ops` MCP tool + remote `machine` proxying):

- Base: `ghost_screenshot`, `ghost_click`, `ghost_type`, `ghost_key`,
  `ghost_scroll`, `ghost_move`, `ghost_windows`, `ghost_locate`
  (`ops_ghost.go`).
- **Element tree (closed the July gap):** `ghost_elements`,
  **`ghost_click_element` with an `expect` verification name that re-reads the
  tree and fails `code=unverified`**, `ghost_type_into_element` (read-back
  verify), `ghost_launch_app`, `ghost_focus_app`
  (`ops_ghost_element.go`).
- Headless web-ERP: `ghost_web_open/goto/click/type/screenshot/text/close`
  (`ops_ghost_web.go`).
- Remote-view providers: `ghost_remote_providers/connect/disconnect/status`
  (RustDesk / AnyDesk / VNC — `ops_ghost_remote.go`, `remoteview.go`), for a
  customer PC where only the remote-view tool is installed ("blackbox").
- **Vision grounding:** `ghost_locate` builds a `visionLocator`
  (`ops_ghost.go:284`) that speaks any OpenAI-compatible `/chat/completions`
  endpoint, with local Ollama as the on-prem default
  (`ghost_vision.go:35-74`).

**Remote Desktop (`/rd/*`)** — owner viewing/controlling their own box over
MJPEG: routes `/rd/status`, `/rd/policy`, `/rd/stream`, `/rd/frame.jpg`,
`/rd/input` (`httpserver.go:799-803`; `remotedesktop_http.go`). Web surface is
the richest (`web/components/dashboard/RemoteDesktopView.tsx`); mobile polls
JPEG inside a WebView (`mobile/app/remote-desktop.tsx`).

**`desktop-screen` — the modern path (closed two July gaps):**
`remote_runtime_desktop.go` makes the host desktop a first-class runtime target
that inherits the runtime layer's **RTP H.264 over WebRTC**, **adaptive encode
profile**, and **single-writer control lease** (`remote_runtime_lease.go`),
gated by the same Remote Desktop consent policy. It grabs the framebuffer with
ffmpeg (`avfoundation` / `x11grab` / `gdigrab`) so frames never round-trip
through JPEG (`remote_runtime_desktop.go:281-333`). `launchDesktopApp`
(`:382`) is the per-OS launch primitive (`open -a` / `start` / `gtk-launch`).

**Transport/proxy:** `ops(machine=B, verb="ghost_click", payload=…)` genuinely
fires remotely via `dispatchOps` (`ops.go`) + `mcp_remote_proxy.go` +
`agent_mesh_remote.go`, over same-LAN / mesh / Tailscale / Cloudflare tunnel /
relay, picked by measured liveness.

### 2.3 Drive a browser

| Lane | Tools / routes | Notes |
|---|---|---|
| **Generic CDP + WebDriver** | 20 `browser_*` MCP tools: `browser_open/close/navigate/click/type/select/scroll/wait/wait_navigation/screenshot/extract_text/extract_attribute/get_dom/snapshot/evaluate/sessions/targets` (`mcp_tools.go:4833-5069`) | Chrome via CDP, Firefox/real Safari via W3C WebDriver. **Persistent profiles** (`profile` name/path) so cookies + Cloudflare clearance survive runs (`browser_open` schema). |
| **Human-in-the-loop co-browse** | `browser_interactive_start/status/stop`; `/browser/interactive/{start,frame/,input/,status/,stop/}` (`httpserver.go:1095-1099`) | Headful/headless Chrome streamed as JPEG; raw mouse/keyboard/scroll relayed so a human solves a captcha/login remotely; automation resumes on the same persistent session (`browser_interactive.go:1-11`). |
| **Selenium sidecar** | 12 `selenium_*` tools (`mcp_selenium.go`) — status/start/fix/search/navigate/click/type/snapshot/text/screenshot | Alternative driver lane. |
| **Headless web-ghost** | 7 `ghost_web_*` verbs (`ops_ghost_web.go`) | Runs on any OS incl. a Pi; the ERP-migration lane. |
| **Browser as a streamed target** | `runtime_create` target `browser-window` | A browser window streamed over WebRTC with input, like a device (`remote_runtime_target.go:124`; `dev_mechanism.go:78`). |
| **DOM mode + screen context** | `/dom-inspect`, `/screen-context` (`dom_inspect.go`, `screen_context.go`) | The user clicks an element in the preview → its outerHTML/CSS/rect + cropped screenshot reach the runner; screen-context carries "what the user is looking at" into the prompt. |
| **Native browser profile/keychain** | `browser_interactive.go:108-170` | Correctly pairs a Chrome profile with the Chrome binary that encrypted it, and refuses Chromium's protected default profile. |
| **Browser extension** | `sdk/feedback/browser-extension/` | MV3, captures screenshot + DOM + computed styles as a design reference; `window.__yaver` for Playwright/Selenium. |

### 2.4 Seeing — vision for text-only models (opencode / deepseek)

This is the seam the user is asking about, and it is real and well-designed.

- **`mcp_vision.go` is explicitly built for a text-only client** — its header
  names `opencode` driving `deepseek-v4-flash` as the motivating case
  (`desktop/agent/mcp_vision.go:5-30`). On the MCP `initialize` handshake it
  resolves the client's vision capability:
  1. `YAVER_MCP_VISION_MODE` env override,
  2. known-client table (claude/codex ⇒ vision),
  3. **opencode model sniff** from `~/.config/opencode/opencode.json`
     (`deepseek*` / `qwen3-coder*` ⇒ text-only) — `opencodeModelVisionMode()`
     at `:112`,
  4. safe default **text-only**.
  Text-only clients get image content blocks rewritten into structured text —
  **dims → macOS Vision-framework OCR ($0) → optional vision-LLM verdict**.
  Vision-capable clients are untouched.
- **Tools any runner can call:** `vision_analyze_image` (with `tier`:
  free/fast/quality and provider/model override), `ui_inspect`,
  `testkit_visual_check`, `vision_pdf_extract`, `vision_diff`, plus a raw
  `screenshot` tool and a pure-Go pixel diff (`mcp_tools.go:1660-1732`).
- **Vision providers:** Mistral / OpenAI / Anthropic, plus local Ollama
  (`vision_cmd.go`, `mcp_vision.go:380-410`). `yaver vision` CLI exists.
- **Ghost grounding** reuses the same AI infra: `ghost_vision.go` prioritizes
  explicit payload → `GHOST_VISION_*` → `OPENAI_*` (what Yaver's runner settings
  inject) → local Ollama (`llama3.2-vision` default on-box).
- **Screenlog** records a local-only screenshot + input-event trace
  (`{"screenshot","action"}` pairs) under `~/.yaver/screenlog/` — literally a
  computer-use training/inspection substrate (`screenlog.go:1-25`).

### 2.5 Speaking to the machine — voice

- **`desktop_voice`** (`ops_desktop_voice.go`) is the **all-surfaces entry
  point**: one spoken sentence (`"open Safari"`, `"click Save"`, `"type hello
  into the search box"`, `"what's on screen"`) → a desktop ops verb → a short
  spoken-style reply. It **works with no video** (reads the accessibility tree)
  so it is usable from a watch, CarPlay, TV, or a thin link, and costs no
  egress.
- **`voice_desktop.go`** maps phrases to typed `PayloadJSON` for
  `ghost_click_element` / `ghost_type_into_element` / `ghost_launch_app` /
  `ghost_focus_app` / `ghost_key` — **the July "voice never sets PayloadJSON"
  gap is closed** (`voice_desktop.go:78-180`).
- **Native clients exist:** `watch/YaverWatch/DesktopVoiceClient.swift`,
  `wear/app/.../DesktopVoiceClient.kt`, `tvos/YaverTV/AgentClient.swift`.

### 2.6 Surfaces — which screen reaches which capability

| Surface | Dev loop | Desktop video | Desktop speech | Browser |
|---|---|---|---|---|
| **Web dashboard** | ✅ | ✅ `RemoteDesktopView` + `RemoteSessionView` | ✅ (ops) | ✅ |
| **Mobile (iOS/Android)** | ✅ | ✅ `remote-desktop.tsx` (JPEG poll) | ✅ | ✅ `browser-interactive.tsx` |
| **tvOS** | ✅ (tasks) | ❌ | ✅ `DesktopVoiceClient` | ❌ |
| **watchOS** | ✅ (tasks) | ❌ | ✅ `DesktopVoiceClient` | ❌ |
| **Wear OS** | ✅ (tasks) | ❌ | ✅ `DesktopVoiceClient` | ❌ |
| **CarPlay / glass** | ✅ (voice coding) | ❌ | ⚠️ via `desktop_voice` verb if wired | ❌ |
| **CLI / MCP** | ✅ | ✅ (`runtime_*`, `ops`) | ✅ | ✅ |

### 2.7 Governance, consent, audit

- Remote Desktop policy (`~/.yaver/remotedesktop/policy.json`, local-only):
  view consent + control opt-in + notify + local JSONL audit
  (`remotedesktop.go`).
- Runtime **control lease** (`remote_runtime_lease.go`) now also arbitrates the
  desktop target.
- Gateway: policy guard, dry-run, velocity caps, human tap gate, vault
  credentials, local audit (`gateway_*.go`) — but see §3.
- `access_policy.go` (Policy Guard) + `egress_proxy.go` (anti-pivot) constrain
  what a loop may touch.

### 2.8 Autonomy (unattended)

`/autoruns/*`, `routines_mcp.go`, `/schedules` + `cron_*`, `/wake`,
`machine/onboarding`, `custodian/*` (sweep/playbook/events). The agent is
designed to run under launchd/systemd and wake on demand.

---

## 3. What is missing or broken (verified)

Ordered by how much it blocks the vision. Each names the file:line that proves
the gap.

### G1 — There is no computer-use agent loop (the spine is unbuilt)
`ghost.Engine.Act` is still **single-shot** — capture → locate → execute, one
iteration, no re-screenshot, no verify, no retry (`ghost/vision.go:82-102`).
The per-action verify exists for `ghost_click_element`/`ghost_type_into_element`
(`expect` / read-back), but there is **no multi-step plan→act→observe→retry**
that a model can run across apps, windows, or a browser session. The loop is
still "owned by the caller" — i.e. by an out-of-repo component. This is the
single biggest product gap.

### G2 — The desktop primitives are invisible to MCP clients
`ghost_*` verbs are **not named MCP tools**; a client sees 275 named tools
(`mcp_tools.go`) and must know to call the generic `ops` tool with
`verb="ghost_click_element"` after discovering it via `ops_verbs`. No
`screenshot`-as-image-first desktop tool exists in the `runtime_frame` /
`robot_camera` pattern for the full screen. Discoverability is why an LLM
"doesn't know it can drive the desktop."

### G3 — Browser + GUI bypass the governance gateway
`validateConnectorManifest` permits exactly **`api` and `redroid`** engines
(`gateway_registry.go:361-369`); there is **no `web` / `playwright` / `ghost`
flow type**. So `browser_*`, `ghost_web_*`, `selenium_*` and the GUI verbs
**answer-schema, audit, consent, and policy-guard are all absent** for them.
`docs/yaver-personal-agent-gateway.md` names `playwright` and `webview` engines;
the code does not implement them.

### G4 — Zero connectors ship
`ls desktop/agent/connectors` and a repo-wide search find **no connector
manifest**. The gateway is a well-engineered empty frame; every CRM/ERP/bank
story is design, not code. (`docs/yaver-personal-agent-gateway.md`.)

### G5 — macOS input fails silently when Accessibility is denied
Every method on `macInput` returns `nil` unconditionally
(`ghost/input_darwin.go:100-134`) — the C helpers are `void` and `CGEventPost`
reports nothing. A TCC-denied Mac answers `/rd/input` `{"ok":true}` while
injecting nothing. Screen capture *does* surface a permission error
(`screen_darwin.go:86`); input does not. **This is a false green** and will read
as "Yaver lies."

### G6 — `/rd/policy` lets a remote caller self-grant control
`handleRemoteDesktopPolicy` protects **view** first-consent
(`rdViewPolicyUpdateEnforce`), but `ControlEnabled` is written with **no
locality gate** (`remotedesktop_http.go:126-136`). A remote authenticated caller
can set `controlEnabled:true` and immediately drive the box. The documented
"control is opt-in from the box itself" two-tier model collapses to one identity
check.

### G7 — Multi-monitor is unsupported
`ghost` enumerates and captures **only display 0** on every OS
(`screen_darwin.go:79-80`, and the same in `screen_linux.go` /
`screen_windows.go`); the desktop target refuses `display != 0` with a clear
message (`remote_runtime_desktop.go:113-115`). One screen only.

### G8 — Wayland is unsupported (and fails inconsistently)
No `wayland` reference in `ghost/` (`rg -in wayland desktop/agent/ghost/` →
none). The WebRTC desktop target **fails loudly** on Wayland
(`remote_runtime_desktop.go:459-461`), but the MJPEG `/rd/` and `ghost`
X11 paths will silently capture an XWayland root with no native-Wayland client
content. Modern Ubuntu/Fedora default to Wayland.

### G9 — Desktop video control reaches only web + mobile
`rg` for remote-desktop/runtime on tvOS/watch/Wear/visionOS returns **zero**.
Those surfaces have **speech-only** desktop control (`desktop_voice`). The
watch/TV "look at and drive my screen" experience does not exist. Browser
control is web + mobile only.

### G10 — No general artifact / file-transfer channel
There is no `files_download` / artifact MCP tool (only `storage_list`);
`/files/read` caps at ~100 KB text; the only base64 artifact path (`cad_get`) is
hard-scoped to `~/.yaver/cad/`. A compiled PDF, a `.dwg`, a screenshot from a
browser session, or a downloaded file has **no clean route back to the phone**.
(`docs/yaver-remote-pc-operation-audit.md` §Gap 6, still open.)

### G11 — Clipboard is MCP-only, not surfaced
`clipboard_read` / `clipboard_write` exist (`mcp_devtools.go:200`), but there is
no phone/web affordance, and no bridge to the **browser** clipboard or to a
remote desktop session's clipboard. Copy-on-agent → paste-on-phone is not a
feature.

### G12 — No autonomous, governed "browse and report" task
There is no first-class task/tool of the shape *"go to this site as me, do X,
verify, report back"* that runs under the gateway's consent/audit while using
the persistent profile. The primitives exist (`browser_*` + profile +
`vision_analyze_image`); the governed task does not.

### G13 — Windows runner seat is now implemented but historically unvalidated
ConPTY seats now exist (`pty_master_windows.go`, `windows_seat.go`,
`runner_pty.go`, `pty_master_windows_contract_test.go`), which is real progress
past the July audit. But there is **no Windows job** for the Go agent in the
main CI workflows, and `ghost` GUI code is compile-checked only — the
best-accessibility-tree platform is still the least-executed.

### G14 — Vision locator has no built-in default provider
`ghost_locate` needs a vision endpoint (payload / `GHOST_VISION_*` / `OPENAI_*`
/ local Ollama). On a box with only an opencode/deepseek BYOK config and no
`OPENAI_*` env, the locator has **no model**, so coordinate grounding is
unavailable even though the runner itself is configured. The text-only MCP
vision adaptation and the ghost locator are **two separate config paths**.

### G15 — Two voice stacks, no barge-in
The Go voice stack (`voice_*`) and the RN stack (`mobile/src/lib/voice`)
share nothing; barge-in is still documented as unbuilt in the RN adapter, and
the Go stack historically drops mic frames while speaking. Voice→desktop works;
conversational desktop control is not yet fluid.

### G16 — Legacy MJPEG `/rd/` remains the default in the surfaces
The good path is `desktop-screen` over WebRTC, but mobile's `remote-desktop.tsx`
still polls `/rd/frame.jpg` (~1.6 fps) and the web `RemoteDesktopView` still
uses `/rd/stream`. Users get the worse transport unless the WebRTC path is
surfaced.

---

## 4. Capability × reality matrix

| Capability | State | Anchor |
|---|---|---|
| Screen capture (3 OSes) | REAL (macOS needs cgo+Screen Recording) | `ghost/screen_*` |
| Mouse/keyboard injection | REAL (macOS silent failure) | `ghost/input_*`, G5 |
| Accessibility tree (3 OSes) | REAL | `ghost/tree_*` |
| Act by element name + verify | **NEW, REAL** | `ops_ghost_element.go` |
| App launch / focus | **NEW, REAL** | `ops_ghost_element.go`, `remote_runtime_desktop.go:382` |
| Desktop stream over WebRTC + lease + consent | **NEW, REAL** | `remote_runtime_desktop.go` |
| Remote Desktop (MJPEG) + input | REAL (legacy) | `remotedesktop*.go` |
| Browser automation (CDP/WebDriver) | REAL | `mcp_tools.go:4833+` |
| Human-in-loop co-browse (persistent profile) | REAL | `browser_interactive.go` |
| Selenium lane | REAL | `mcp_selenium.go` |
| Headless web-ghost (ERP) | REAL | `ops_ghost_web.go` |
| Browser extension capture | REAL | `sdk/feedback/browser-extension` |
| Vision for text-only models (deepseek/opencode) | REAL | `mcp_vision.go` |
| Ghost vision grounding | REAL but **unconfigured by default** | `ghost_vision.go`, G14 |
| Spoken desktop control (all surfaces) | REAL | `ops_desktop_voice.go` |
| Computer-use **agent loop** | **MISSING** | G1 |
| Desktop verbs as **named MCP tools** | **MISSING** | G2 |
| Governed browser/GUI path | **MISSING** | G3 |
| Connectors | **ZERO** | G4 |
| Multi-monitor | **MISSING** | G7 |
| Wayland | **MISSING** | G8 |
| Desktop video on TV/watch/Wear | **MISSING** | G9 |
| Artifact/file return | **MISSING** | G10 |
| Clipboard on surfaces | **MISSING** | G11 |
| Autonomous browse task | **MISSING** | G12 |
| Windows CI validation | **MISSING** | G13 |
| macOS permission preflight | **MISSING** | G5 |

---

## 5. Security findings still open

1. **macOS input silently succeeds under denied TCC** (G5) — false green.
2. **`/rd/policy` remote control self-grant** (G6) — the two-key promise is one
   key. (The July audit's `gateway_act_confirm` finding should be re-checked
   independently; it is outside this audit's scope but is the more serious one.)
3. **Browser-session token scope**: the July audit flagged `/rd/input` and
   `/rd/policy` reachable with a 2-minute path-scoped browser-session token
   minted for `<img>`/EventSource GET viewing. Re-verify the allowlist is still
   prefix-scoped rather than per-route+method.
4. **GUI control has no lease on the legacy `/rd/` path** — the lease exists on
   the `desktop-screen` runtime target but `handleRemoteDesktopInput` still
   replays straight into the engine (`remotedesktop_http.go`). Two clients on
   `/rd/` still fight.
5. **Long-lived streams never re-check consent**; revoking `ViewEnabled` does
   not cut an active viewer (verify current `/rd/stream` loop).

Per the repo's rule, every one of these is owed a **detection → named signal →
visible cause → route-to-fix** before it is "shipped."

---

## 6. Doc + test drift to fix in the same change

| Claim | Where | Reality (2026-09-29) |
|---|---|---|
| "glm is a runner" | `ops_runner_turn.go` copy | Three runners: claude / codex / opencode |
| "81 ops verb families collapsing 744 specialist tools" | `docs/yaver-anywhere/strategy-and-reality.md` | Measured: ~379 `registerOpsVerb(` call sites, ~290 distinct verb names in `ops_*.go` — re-measure and state the date |
| "playwright / webview engines" | `docs/yaver-personal-agent-gateway.md` | Code allows `api` \| `redroid` only (`gateway_registry.go:361`) |
| July audit's "no element actuation / no app launch / no desktop lease" | `docs/yaver-remote-pc-operation-audit.md` | All three closed — that doc needs its status header updated |
| "macOS/Linux stubbed until Phase 2" | `ghost/ghost.go:9-11` | All three OSes implemented |

---

## 7. Open questions for the user

1. **Which OS is the operator target?** macOS (best input fidelity, silent-TCC
   bug), Linux-X11 (Wayland cliff), or Windows (best tree, least validated).
2. **Is the browser lane "automate a site as me" (governed, persistent profile,
   autonomous) or "co-browse with me" (human-in-loop)?** The primitives for both
   exist; the product shape differs.
3. **Which surface is the primary operator surface?** Web/mobile have video;
   watch/TV/Wear have speech. Investing in TV/watch desktop video is a different
   bet from polishing the phone.
4. **Do we unify the two config paths for "the model that can see"** (MCP
   `vision_analyze_image` vs `ghost_locate`), so one provider setting makes both
   work?
5. **Is the operator loop an MCP tool, an ops verb, or a task?** (This decides
   G1/G2's shape.)
