# Local Screen Monitor (`screenlog`) — porting talos "screen recording images" into Yaver

**Status:** BUILT 2026-06-06 (untracked, not committed). Shipped as
`screenlog` — the obvious name `monitor` was already taken by URL **uptime
monitoring** (`monitor_add`/`monitor_list`, `monitor_cmd.go`). Files:
`desktop/agent/screenlog{,_capture,_window,_http,_cmd,_test}.go` + additive
edits to `httpserver.go` (routes + MCP dispatch), `mcp_tools.go` (6 verb
schemas), `main.go` (CLI case), `convex_privacy_test.go` (forbidden fields).
Compiles clean + tests green in an isolated HEAD worktree (the live working
tree has unrelated parallel-session breakage in `ops_machine.go`/`auth.go`).
**Goal:** the talos PC-monitor "screen as a stream of images" capability, reborn
in the Yaver agent: cross-platform (Windows / macOS / Linux + **WSL2**), driven
from the yaver terminal / MCP, **records to local disk only** — never Convex,
never SFTP, never a Storage Box.

---

## 1. What talos actually does (the thing we're copying)

talos has **two** screen-capture engines. The one the user means by "screen
recording images" is the **frame engine**, not the OBS-video engine.

### 1a. Frame engine — `desktop-app/src/electron/services/screen-capture.ts`

A periodic-screenshot loop. The shape:

| Knob | talos default | Source |
|---|---|---|
| Capture API | Electron `desktopCapturer.getSources({types:['screen']})` | screen-capture.ts:241 |
| Scope | **all displays**, in parallel | screen.getAllDisplays() |
| Interval | **3 s** | `intervalSeconds: 3` |
| Format | **JPEG** | `out.toJPEG(65)` |
| Quality | 65/100 | `jpegQuality: 65` |
| Downscale | width capped at **1600px** | `maxWidth` |
| Dedup | **dHash 8×8 → 64-bit**, Hamming threshold 8 — drop frames that look like the previous kept frame | lines 114–145 |
| Heartbeat | keep one frame every **5 min** even if unchanged | `heartbeatSeconds: 300` |
| Idle gate | pause capture when the user is away | lines 232–233 |
| Safety cap | **4000 frames/day** | `dailyFrameCap: 4000` |
| Tagging | each frame tagged with `activeApp` + `activeWindowTitle` | — |
| AI | Claude vision writes a 1–2 sentence summary per frame/chunk | uploader.py |

**Where talos puts the bytes (the part we delete):**

```
<HetznerStorageBox>/<machineId>/<YYYY-MM-DD>/<sessionTs>/<capturedAt>_<displayIdx>.jpg
```

- JPEG bytes → **SFTP** to the org's Hetzner Storage Box (`ssh2-sftp-client`).
- Metadata row → **Convex** `screenFrames` table (`POST /pc-monitor/frame`):
  `machineId, date, sessionTs, capturedAt, displayId, remotePath, sizeBytes,
  width, height, phash, hammingFromPrev, activeApp, activeWindowTitle, summary`.
- Storage Box creds fetched per-org at runtime via `GET /pc-monitor/storage-box`.

### 1b. Cross-platform active-window / idle detection — `tools/pc-monitor/platform_compat.py`

This is the genuinely reusable design knowledge. talos abstracts three OSes:

- **Windows:** `GetLastInputInfo` (idle), `win32gui.GetForegroundWindow` + `psutil` (active window).
- **macOS:** `CGEventSourceSecondsSinceLastEventType` (idle), `NSWorkspace.frontmostApplication` / `osascript` (active window).
- **Linux:** `xprintidle` (idle), `Xlib`/`_NET_ACTIVE_WINDOW` (active window).

### 1c. What we are NOT copying

- OBS WebSocket video-chunking engine (`obs_controller.py`) — that's the *video*
  product, not "images." Yaver already has ffmpeg video via `clip_*`.
- The entire cloud spine: SFTP upload queue, Convex `screenFrames`/`screenRecordings`
  tables, Storage Box credential fetch, per-org tenant isolation, Claude-vision
  summaries. **Local-only means none of this ships.**

---

## 2. What Yaver already has (so we don't rebuild it)

| Capability | File | Reuse verdict |
|---|---|---|
| **`screenshot` → cross-platform single PNG** | `tools.go:107 captureScreen()` | **THE core primitive.** Already does macOS `screencapture -x`, Linux `gnome-screenshot`/`scrot`/`import`, Windows PowerShell `CopyFromScreen`. The monitor loop is "call this on a timer + dedup + retain." |
| `clip_start/stop/list` — ffmpeg screen **video** | `recorder.go` | Reference for session-state + local `~/.yaver/...` layout. **Windows unsupported** there; our monitor must not inherit that gap. macOS/Linux only. |
| `cast_*` — asciinema terminal capture | `asciinema.go` | Not screen pixels. Ignore. |
| `record_start/stop/drivers` — **declared MCP tools, NO handler** | `mcp_tools.go:3689` | "morning reel" stubs. We can either implement these or add new `monitor_*` verbs (see §6). |
| ghost vision — macOS `CGDisplayCreateImage` (cgo) | `ghost/screen_darwin.go` | Higher-fidelity macOS capture, but cgo + macOS-only. Keep `captureScreen()` shelling for portability; ghost is an optional fast-path later. |
| Privacy enforcement | `convex_privacy_test.go` | The guardrail that *proves* local-only. New frame fields must be added to the forbidden list. |
| **WSL host detection** | `discovery.go:105 isWSLHost()` (`WSL_DISTRO_NAME` / `WSL_INTEROP` / `/proc/version` contains "microsoft") | Already exists — reuse verbatim. |
| **WSL→Windows interop pattern** | `process_wsl.go:56` shells `cmd.exe /C ...` from inside WSL | The exact bridge we need to capture the *Windows* screen from a WSL shell. |

**Bottom line:** ~80% of the primitive already exists. The new work is the
*loop + retention + dedup + active-window tagging + local index + MCP verbs*,
not the pixel grab.

---

## 3. The WSL problem (the interesting one)

The user explicitly wants this "in WSL." WSL is the one case where naïve reuse
breaks, because **there are two different screens** and `runtime.GOOS == "linux"`
can't tell them apart:

1. **The Windows host desktop** (what the user actually looks at). Not an X11
   display. `scrot`/`import` see nothing useful.
2. **The WSLg Wayland/X surface** (Linux GUI apps, usually empty for a terminal
   user).

Today `captureScreen()` on WSL takes the Linux branch → `gnome-screenshot`/
`scrot`/`import` → captures the (empty) WSLg root, or errors with "no screenshot
tool found." That's wrong for the user's intent.

**Fix:** branch on `isWSLHost()` *before* the `GOOS=="linux"` switch and run the
**Windows** PowerShell capture through interop:

```go
// pseudo — inside captureScreen() / a WSL-aware variant
if isWSLHost() {
    // write to a Windows-visible temp path, capture via host PowerShell,
    // then read it back through /mnt/c
    cmd = exec.Command("powershell.exe", "-NoProfile", "-Command", psCaptureScript)
    // psCaptureScript saves to C:\Users\<u>\AppData\Local\Temp\yaver-shot.png
    // → read /mnt/c/Users/<u>/AppData/Local/Temp/yaver-shot.png
}
```

`powershell.exe` is on `PATH` inside WSL2 by default (same mechanism
`process_wsl.go` already uses with `cmd.exe`). Multi-monitor: the existing
PowerShell uses `PrimaryScreen`; for "all displays" use
`[System.Windows.Forms.SystemInformation]::VirtualScreen` to grab the union.
Path translation Windows→WSL is the only fiddly bit (`wslpath -u` or string-map
`C:\` → `/mnt/c/`).

**Caveat to surface in the doc/UI:** capturing the host screen from WSL requires
the Windows session to be unlocked and a desktop to exist (no headless/SSH-only
Windows). Worth a one-line `monitor_drivers`-style probe.

---

## 4. Local-only storage layout (proposed)

Mirror the `~/.yaver/clips/` and `~/.yaver/asciinema/` conventions:

```
~/.yaver/monitor/
  index.json                       # session + frame metadata (local only)
  <sessionId>/
    0001_<capturedAtMs>_d0.jpg     # frame, display 0
    0001_<capturedAtMs>_d1.jpg     # frame, display 1 (multi-monitor)
    ...
```

`index.json` (never leaves the box):

```json
{
  "sessions": [{
    "id": "mon_2026-06-06_0913",
    "startedAt": 1717664000000, "endedAt": 1717667600000,
    "host": "this-machine", "displays": 2,
    "intervalSec": 3, "format": "jpg", "quality": 65, "maxWidth": 1600,
    "frames": [{
      "idx": 1, "capturedAt": 1717664003000, "display": 0,
      "file": "mon_.../0001_..._d0.jpg", "bytes": 84213,
      "w": 1600, "h": 900, "phash": "...", "hammingFromPrev": 14,
      "activeApp": "Code", "activeWindowTitle": "local-screen-monitor.md"
    }]
  }]
}
```

**Retention** (local disk is finite — talos leaned on a Storage Box, we can't):

- `--max-frames-per-day` (default 4000, matching talos cap).
- `--max-disk-mb` ring buffer: when the session dir exceeds budget, delete
  oldest frames first.
- `--retention-days`: prune session dirs older than N days on start.
- Default OFF until explicitly started; never auto-starts at boot.

---

## 5. Active-window / idle tagging — Go port of `platform_compat.py`

A small `monitor_window.go` with build-tagged or runtime-switched helpers:

| OS | Active window | Idle seconds |
|---|---|---|
| macOS | `osascript -e 'tell application "System Events" to get name of first process whose frontmost is true'` (no extra perms beyond the Screen-Recording one screenshots already need) | `ioreg -c IOHIDSystem` HIDIdleTime, or CGEventSource via cgo (optional) |
| Linux/X11 | `xdotool getactivewindow getwindowname` / `_NET_ACTIVE_WINDOW` | `xprintidle` |
| Windows | PowerShell `GetForegroundWindow` + `GetWindowText` | `GetLastInputInfo` |
| WSL | run the **Windows** variant via `powershell.exe` interop | same |

All optional/best-effort — a frame with no active-window tag is still a valid
frame. Degrade silently (the screenshot is the product; tags are metadata).

---

## 6. MCP / CLI surface (two options)

**Option A — implement the dormant `record_*` tools.** They already exist in
`mcp_tools.go:3689` (`record_drivers/record_start/record_stop`) but were specced
for "morning reel" *video*, with a `/recordings/{run_id}/{task_id}/video.mp4`
serving contract. Repurposing them risks muddling video-vs-frames semantics.

**Option B (chosen) — new `screenlog_*` verbs**, parallel to `clip_*`/`cast_*`.
(`monitor_*` was the original recommendation but collides with uptime
monitoring, so the namespace is `screenlog`.) As built:

| Verb | Behavior |
|---|---|
| `screenlog_drivers` | Real capture probe: which path is live (`macos:screencapture` / `linux:scrot|gnome|import` / `windows:powershell` / `wsl-interop:powershell.exe`), display count, WSL flag. Fails loud if capture is impossible. |
| `screenlog_start` | Begin a local frame session. Args: `interval_sec`, `format` (png/jpg), `max_width`, `displays` (all/primary), `dedup`, `max_disk_mb`, `retention_days`, `tag_window`, `wsl_target` (auto/host/wslg). Returns `{session, viewUrl}`. |
| `screenlog_stop` | Finalize → `{id, frames, viewUrl}`. |
| `screenlog_status` | Live counters: `keptFrames`, `dropped`, `bytes`, `elapsedSec`, `lastError`. |
| `screenlog_list` | Local sessions newest-first (frame counts, no frame arrays). |
| `screenlog_frames` | Frame metadata (`idx`, `capturedAt`, `display`, `file`, `activeApp`, `activeWindow`) — agent-readable for "what was on screen at <time>" and bug-report attach. Images at `/screenlog/<id>/<file>`. |

CLI: `yaver screenlog start --interval 2 --format png --displays all`, `yaver
screenlog status|stop|list|drivers`, `yaver screenlog open [<id>]`. Wired through
the same dispatch as `clip_*` (`httpserver.go handleMCPToolCallWithAddr`) and the
local-daemon client (`localAgentRequest`). HTTP routes under `/screenlog/*`, all
`s.auth`-gated (private — no public share links, unlike clips). The
`/screenlog/<id>` HTML viewer is a lazy-loaded frame grid.

**Higher-fidelity defaults (per product decision):** 2 s interval, full-res
**PNG**, dedup on (Hamming ≤ 6), 5-min heartbeat, 4096 MB disk-budget ring
buffer, 7-day retention.

Implementation core is a single goroutine:

```
ticker(interval):
  frame = captureScreen()            // WSL-aware (§3), per-display
  if dedup && hamming(phash(frame), lastKept.phash) < threshold
     && now - lastKept < heartbeat:  drop
  else:                              write jpg, append index, lastKept = frame
  if overBudget():                   evict oldest
```

dHash/Hamming is ~30 lines of pure Go (grayscale 9×8 → gradient bits); no new
dependency. JPEG re-encode + downscale via stdlib `image/jpeg` + `golang.org/x/image/draw`.

---

## 7. Privacy contract (the whole point)

Local-only must be *enforced*, not promised. `convex_privacy_test.go` already
fails the build if forbidden keys reach a Convex payload. Action items:

- Add to `fieldsWeForbidInAnyConvexPayload`: `frameJpeg`, `frameBytes`,
  `framePath`, `monitorFrame`, `activeWindowTitle`, `phash`, `monitorDir`,
  `screenFrame`, `screenshotB64`.
- Add a test asserting the monitor's index/frames never enter `callMutation`.
- The monitor goroutine **never imports `convexSyncer`**. No SFTP, no HTTP-out.
  The only network surface is the *local* `/monitor/<id>` viewer (same-origin as
  `/clips/:id`), served from disk.
- Reuse the path-leak scanner: frame paths under `~/.yaver/monitor/` contain the
  home dir, which is exactly why they must never sync.

This is also the product wedge vs talos: talos PC-monitor is an
**employer-surveillance / org** tool (frames → org Storage Box + Convex, visible
to admins). The Yaver port is a **personal black-box** — your machine, your disk,
nothing leaves. Same mechanism, inverted trust model.

---

## 8. Build plan (phased, each independently testable)

- **P0 — WSL-aware `captureScreen()`.** Branch on `isWSLHost()` → `powershell.exe`
  interop + `/mnt/c` readback + VirtualScreen multi-monitor. Unit-testable via a
  fake capture binary on a random temp path (repo's no-mock test style).
- **P1 — frame loop + local index + retention.** `monitor.go`: goroutine,
  `~/.yaver/monitor/`, `index.json`, disk-budget ring buffer. dHash dedup.
- **P2 — `monitor_*` MCP verbs + CLI + dispatch.** Wire like `clip_*`.
- **P3 — active-window/idle tagging** (`monitor_window.go`), best-effort per OS.
- **P4 — `/monitor/<id>` local viewer** (HTML grid of frames, scrub timeline),
  cloned from `/clips/:id`.
- **P5 — privacy test additions** (do alongside P1, not after).

Optional later: local-only Claude-vision summaries **only if** the user has a
local runner (respects `feedback_no_api_keys` — no API-key path); ghost
`CGDisplayCreateImage` macOS fast-path; capture a single window instead of full
screen.

---

## 8b. Tier 2 — Analyze (built): "what did this machine spend time on"

The black box is only half the value; the other half is *reading* it. Built
on top of capture:

- **`activity_report.go`** — a **source-agnostic** report engine. Feed it a
  `[]ActivitySample{Start,End,Category,Label,Idle}` and it returns
  time-by-category (most first), active vs idle, top labels, an hourly
  histogram, and a runner-ready `NarrativePrompt()`. This is the **generic
  analysis spine**: screen frames are `source:"screen"`, but a PLC tag stream
  (`source:"machine"`), a process sampler (`source:"process"`), or packet
  flows (`source:"net"`) reduce to the same `ActivitySample` and get the same
  report. One code path answers "what did this *thing* spend its time on" for
  a PC, a tool, or a PLC.
- **`screenlog_analyze.go`** — reduces a session's frames to samples: each
  kept frame is attributed to its `activeApp` for the span until the next
  frame, **capped** (`max_attribute_sec`), and gaps larger than
  `idle_gap_sec` become idle. Deterministic — the breakdown is exact, no LLM.
- **`screenlog_analyze`** MCP verb → `{report, topActivity, narrativePrompt}`.
  `topActivity` literally answers "what he spent most time on."
- **Vision keyframes** — `screenlog_frames {sample:N}` now returns N
  evenly-spaced frames as **inline MCP images** (mixed text+image content,
  like the `screenshot` tool), so a vision-capable runner can *see*
  representative moments, not just read app names.

"Utilizes runners" without breaking [[feedback_no_headless_p_mode]]: the agent
**never spawns a headless runner**. The MCP *client* — the claude-code/codex
the user is already driving (possibly on a different machine) — calls
`screenlog_analyze`/`screenlog_frames`, gets exact numbers + keyframe images +
the narrative prompt, and writes the prose. The runner is the consumer, not a
thing the agent forks.

## 8c. Tier 3 — Remote enable + permissions (built): same-account & mesh

The capture loop lives in the long-running agent, so "trigger it on the fly on
a remote machine" is just reaching that agent. Three trust paths, all riding
existing Yaver infra, with a new screenlog-specific consent gate on top:

| Caller | Transport (exists) | Permission gate (built) |
|---|---|---|
| **Same yaver account** (your own devices, e.g. dad's WSL box under your account) | owner token over relay/direct → `/screenlog/*` (all `s.auth`-gated); or `yaver code --attach <device>` then call the verbs | `ScreenlogPolicy.AllowRemoteControl` (default on) + audit + (optional) notify |
| **Delegated / guest** (someone you invited) | guest token | `guest_scope.go`: `/screenlog/` added to the **full** tier only — feedback-only & read-only **support** guests can't reach it |
| **Yaver mesh peer** (a *different* account you peered with, e.g. dad has his own account) | `acl.go CallPeerTool` (tools/call to the peer) | `ScreenlogPolicy.RequireMeshGrant` — the peer id must be in `AllowedPeers`; **a mesh peering does NOT implicitly grant screen access** |

**`screenlog_policy.go`** — the recorded machine's owner control surface,
stored at `~/.yaver/screenlog/policy.json` (local only — it lists peer ids):

- `Enabled` — master **kill-switch** (`yaver screenlog disable` → refuse all
  starts, even local).
- `AllowRemoteControl` — may a **non-loopback** caller start/stop. The HTTP
  handler classifies via `r.RemoteAddr` (loopback = local), the MCP dispatch
  via the threaded `clientAddr`.
- `RequireMeshGrant` + `AllowedPeers` — per-peer mesh grant.
- **Audit trail** (`audit.jsonl`): every start/stop/deny/policy event with
  caller remoteness + peer id → `screenlog_audit`. So the owner always sees
  *who* started recording and *when*.

`startScreenlogGuarded` is the single choke point both surfaces call, so the
consent gate can't be bypassed by picking HTTP vs MCP. Verbs:
`screenlog_policy_get/set`, `screenlog_audit`; CLI `yaver screenlog
enable|disable|allow-remote|deny-remote|allow-peer|revoke-peer|policy|audit`.

## 8d. North star — observe → understand → replicate

The user's framing: a third-party app (or talos) uses Yaver to **observe** what
a human does, **understand** it (activity report + keyframes), then **replicate**
the human via the **ghost API** (`ghost/` computer-use: screen + synthetic
input — the same surface talos's `pc_agent.py` used to drive an ERP). screenlog
is the *observe + understand* tiers; the captured frames + per-frame
`activeApp`/`activeWindow` + timestamps already form a **behavioral trace** a
runner can read and reproduce through ghost. The seam is intentional: an
automation runner consumes `screenlog_frames`/`screenlog_analyze`, then emits
ghost actions. (Replication itself is out of scope here — it's a ghost-side
concern — but the trace shape is designed to feed it.)

## 8e. Answer — can WSL capture the *full* Windows screen?

**Yes, the entire Windows desktop — with two real limits.** The WSL path runs
`powershell.exe` (interop) → `CopyFromScreen` over
`SystemInformation.VirtualScreen` (the union of **all monitors**, full
resolution) → PNG to the Windows temp dir → read back via `/mnt/c`. So "fully"
= every monitor, full res, the actual Windows desktop (not the WSLg surface).
Limits, both inherent to GDI `CopyFromScreen`, not to Yaver:

1. **The Windows session must be interactive + unlocked.** On the lock screen
   or a session with no desktop (headless / SSH-only Windows), the grab is
   black. For "dad actively using his PC," fine.
2. **DRM-protected surfaces show black** (Netflix, some protected video). Same
   limitation every GDI screenshotter has.

Future fidelity upgrade: a tiny bundled Windows helper using the **Desktop
Duplication API** (DXGI) would capture hardware-accelerated/DRM content and
cut per-frame cost — but GDI is the zero-install baseline and works today.

## 8f. Tier 4 — surfaces, input trace, smart/optimized capture (built)

**Web + mobile viewers.** The dashboard gets a **Screen Monitor** tab
(`web/components/dashboard/ScreenMonitorView.tsx`): session list, the
activity-report bars ("what it spent time on"), a blob-loaded frame grid
(through the auth'd relay), live status, start/stop, and the consent-policy
toggles. Mobile gets a parallel screen (`mobile/app/(tabs)/screenlog.tsx`,
reached from More) with the same surface; frames render via RN `<Image>` with
auth headers. Both target the **currently connected device** — so "monitor my
dad's WSL box from my phone" is: connect to that device → open Screen Monitor.

**Notify-on-remote-start.** When a NON-local caller starts recording and
`policy.NotifyOnStart` is set, the owner gets a push (reusing
`globalNotifyManager.NotifyAgentEvent`, the same fan-out the uptime monitor
uses) **and** a local desktop toast (`defaultDesktopNotify`). Screen capture is
never silent.

**Smart, optimized capture (not "dummy").**
- **Dedup** (already core): identical/near-identical screens are dropped via
  dHash+Hamming — redundant frames are never written.
- **Active intervals**: each kept frame stores `ActiveToMs`, so a frame's
  `[CapturedAt, ActiveToMs]` covers every de-duped duplicate until the next
  *distinct* screen — i.e. **"this screen was on from 12:01 to 12:53."** The
  analyzer uses these exact intervals.
- **Ephemeral frames** (`ephemeral` / `EphemeralFrames`): "temporary
  screenshots" — capture, derive the label (which app/window) + hash +
  interval, then **discard the image**, keeping only the activity trace.
  Storage-light + privacy-friendly; still answers "what was it doing."

**Input-event companion stream (keys + mouse) — `screenlog_input.go`.** The
session becomes a `{screenshot, action}` trace. Events are appended to
`events.jsonl` in a **standard, AI-training-friendly schema** (the de-facto
computer-use action format), so a recording pairs 1:1 with frames for
imitation-learning / RPA replay / the ghost replicate-the-human loop:

```
{"t":1717,"type":"click","x":840,"y":210,"button":"left","screenW":2560,"screenH":1440}
{"t":1718,"type":"key","key":"Enter"}
{"t":1719,"type":"scroll","dx":0,"dy":-3}
```

Pixel coords are absolute; `screenW/H` travel with each event so a consumer can
normalize to 0..1 (the GUI-agent-dataset convention). **Capture is decoupled
from storage (producer model)**: a producer POSTs to
`POST /screenlog/<id>/events`; the model + redaction + stats live in the agent,
so producers stay thin and **don't require the built-in agent loop**. Native
global hooks (macOS CGEventTap · Windows `SetWindowsHookEx` host exe, reachable
from WSL · Linux evdev/XRecord) are the next phase — the schema + storage land
now. Privacy: input capture is OFF unless **both** `CaptureInput` (config) and
`AllowInputCapture` (policy — a separate, stronger gate) are set; typed
characters are **redacted by default** (`AllowRawText=false`) to a placeholder
that keeps action structure without storing secrets; audited. Read via
`screenlog_events` (+ deterministic stats: clicks, keys, actions/min).

**Runner-optional smart labeling.** "Which app / which service" is answered two
ways: **deterministically** (the `activeApp`/`activeWindow` tags + report — no
LLM), and **optionally with a runner** (`screenlog_frames {sample:N}` hands
keyframes as inline images to a vision runner, which can name the *service*,
e.g. Gmail vs Slack, and write a narrative). The runner is always the *consumer*
(the claude-code/codex the user already drives) — the agent never forks a
headless one.

**Works without the agent.** `yaver screenlog start --local` runs the capture
loop in the CLI process itself (no `yaver serve` daemon), writing the same
`~/.yaver/screenlog/` files every other surface reads. The file format is the
contract, so a thin standalone recorder or SDK can produce/consume without the
daemon.

## 8g. Tier 5 — native input capture, pull, in-device mobile (built)

**Native input capture (the real keylogger/mouse hooks).** A shared,
testable manager (`screenlog_input_capture.go`) runs a per-platform
*producer* and ingests its JSON-line event stream into the session's
`events.jsonl` while frames record. Producers (`screenlog_input_producers.go`):
- **WSL → Windows host (top use case)**: the WSL agent can't hook Windows
  itself, so it spawns `powershell.exe` running a low-level
  `WH_KEYBOARD_LL`/`WH_MOUSE_LL` hook (C# via `Add-Type`) that streams each
  click / scroll / keystroke as a JSON line back over the interop pipe — the
  same bridge screen capture uses, no extra binary to ship.
- **Linux (X11)**: an `xinput test-xi2 --root` producer (best-effort, no
  coords; real evdev capture documented as follow-up).
- **macOS / native Windows**: producer-pluggable via `nativeStartInputCapture`
  (CGEventTap / in-process `SetWindowsHookEx`) — documented next step.

Mouse *moves* are intentionally dropped (they flood); clicks, scroll, and
keys are captured. Gated by `CaptureInput` + `AllowInputCapture` + redaction,
audited. The manager is unit-tested with a fake producer; both darwin and
**windows** cross-compile clean.

**Pull.** `GET /screenlog/<id>/export` streams the whole session (index +
frames + `events.jsonl`) as a **tar.gz**. `yaver screenlog pull <id> [--out
f]` downloads it; `screenlog_export` (MCP) returns the URL. Pull a remote
box's recording by attaching to it (or via the relay) and hitting the same
URL — the export is the contract.

**Mobile, inside Devices.** `ScreenlogSection` (a new component mirroring
`CodingAgentsSection`) is embedded in `DeviceDetailsModal` — so from the
device list on your phone you expand a device and get a **Screen Monitor**
panel: start/stop, the smart activity report (top-app bars), and a frame
grid, **peer-routed** via `/peer/<id>` so it drives a *non-active* device
(dad's box) without a full connect. The standalone Screen Monitor tab
(`app/(tabs)/screenlog.tsx`) remains for the active device.

## 8h. Tier 6 — headless emulator + QoS bounding (built)

**Emulator (test on Linux/Mac, no display, no hardware).** `screenlog_emulator.go`
drives the *real* pipeline (de-dup → active intervals → persist → input
events → analyze → export) from a **synthetic activity timeline** — "user in
App X for N minutes" segments with click/keystroke rates. It generates one
app-distinct synthetic frame per tick (identical within a segment so de-dup
drops them, distinct across segments so a new frame is kept and the previous
interval closes) plus synthetic input. `yaver screenlog emulate
[--scale-seconds N]` runs it **in-process, no daemon, no display** — verified
on macOS producing a real session: `Code 30s (41%), Excel 18s, Chrome 12s,
Slack 8s, idle 5s`, with `events.jsonl` keystrokes redacted to `•`. This is the
`$0` no-hardware harness (same spirit as the emulated-PLC e2e); two tests
(`TestScreenlogEmulationEndToEnd`, `TestScreenlogFrameCapBoundsMemory`) assert
the full flow + the memory bound.

**QoS / resource bounding (won't balloon RAM, disk, or CPU).** The capture
loop was refactored into a single bounded `ingestFrame`:
- **In-memory index capped** at `MaxFrames` (default 5000) — oldest evicted +
  file deleted. Ephemeral mode is capped too (it previously appended forever).
- **Disk** bounded by the `MaxDiskMB` ring buffer (default 4 GB).
- **index.json writes throttled** to every 10 kept frames (+ on stop), so we
  never re-marshal a growing slice every tick (was O(n²) IO).
- **CPU**: duplicate screens are dropped before any encode; the interval is
  the throttle.
- Fixed a latent **slot-shift bug**: front-eviction now adjusts the per-display
  "last kept frame" indices so interval-close writes to the right frame.

**Analysis fix surfaced by the emulator.** A span *within* the idle threshold
is now fully active ("on Excel 12:01→12:53"); only a span exceeding it (capture
paused — asleep/away) is capped to a short head + idle remainder. Default idle
gap raised to 600 s (just past the heartbeat) so a heartbeat-kept unchanged
screen counts active, not idle.

## 8i. Tier 7 — cheap whole-screen defaults + dependency self-install (built)

**Defaults flipped to whole-screen + cheap** (quality isn't important for
activity monitoring; this is NOT power-saving — cadence + full coverage are
unchanged, only per-frame cost drops):
- `Format: jpg`, `Quality: 60`, `MaxWidth: 1920` (was full-res PNG). Downscale
  uses the fast `ApproxBiLinear`. Still captures the **whole** screen (all
  displays), just downscaled.
- Measured on a real Retina Mac (30 s live capture): CPU peaks **~38% → ~11%**,
  frame size **~1 MB → ~290 KB**, session disk **12 MB → 1.4 MB**. RAM stays
  bounded. The web/mobile start calls drop their `format` override so the new
  default applies.

**`yaver install` provisions the capture dependency** (`screenlog_deps.go`):
- macOS / Windows / WSL need **nothing** (screencapture / PowerShell /
  interop are built in).
- Linux (real display) needs a screenshot CLI → we install **scrot** via the
  detected package manager (`apt-get`/`dnf`/`pacman`/`zypper`/`apk`, sudo when
  not root). `yaver serve` calls `ensureScreenlogDepsBestEffort()` async +
  non-fatal on startup (installs where passwordless sudo/root allows, else logs
  the one-liner); `yaver screenlog install-deps` is the explicit interactive
  path. `screenlog drivers` reports `deps` status so the UI can prompt.

## 9. Open questions for the user

1. **Capture cadence vs storage** — keep talos's 3 s / JPEG-65 / 1600px / dHash
   defaults, or different (e.g. lower fps, PNG, full-res)?
2. **What's it FOR in Yaver?** Personal activity black-box you scrub later? Feed
   frames to the agent ("what was I doing at 14:30")? Auto-attach recent frames
   to a feedback/bug report? This decides whether we need the `/monitor` viewer,
   agent-readable frame access, or both.
3. **`monitor_*` new verbs (recommended) vs reusing dormant `record_*`?**
4. **WSL scope** — capture the *Windows host* screen (most likely intent), or the
   Linux WSLg surface, or auto-detect/offer both?
5. **Retention defaults** — disk budget (MB) and retention days before pruning.
