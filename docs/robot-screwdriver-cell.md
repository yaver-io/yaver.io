# Yaver Robot Cell — Cartesian screwdriver (Ender-3) with camera-in-the-loop

> Drive a re-purposed Creality Ender-3 (nozzle removed → screwdriver end-effector)
> as a Cartesian pick-screw robot **natively from the Yaver agent**, verify every
> motion with a **webcam → vision-LLM** loop, and **wire the whole thing into Talos**.
> The same control path must run from an **old Android phone over USB-OTG** (phone
> is the host + the eye) instead of the laptop.
>
> Status: ARCHITECTURE + Phase-1 build. Source of truth is the code; this doc is
> the map. Re-grep before trusting any claim here.

---

## 0. Why this is mostly assembly, not greenfield

Two halves already exist and are reused, not rebuilt:

| Existing asset | Where | Reused for |
|---|---|---|
| Raw Linux serial open (`/dev/ttyUSB*`, 8N1, baud) | `desktop/agent/machine/serial_linux.go` | Marlin transport (made bidirectional) |
| Image→LLM primitive (PNG + instruction → structured JSON, provider ladder: payload→`GHOST_VISION_*`→`OPENAI_*`→local Ollama) | `desktop/agent/ghost_vision.go` | Camera-frame → verdict |
| Ops-verb + MCP-tool registration + Talos `/machine-edge` sync | `desktop/agent/ops_machine.go` | New `robot_*` verbs + sync |
| Ender-3 machine contract (envelope, FAN-port tool, feeds, wait-`ok`) | `talos/robotics/wirecell/firmware/klemens/{config,stream,SPEC}.py` | Driver parameters, parity with Talos |
| Convex `machineEdge*` tables + `/machine-edge/*` HTTP + `MachineEdgePanel` + `robotics-panel.tsx` jog UI + `talos_machine_*` MCP | `talos/cloud/convex`, `talos/web`, `talos/cli` | Product surface for the robot |

Net-new: a Marlin **motion model** (position tracking, soft-limits, homing state,
e-stop), **V4L2 webcam capture** (no ffmpeg dependency), the **verify loop** that
gates motion on vision, and the **phone USB-OTG + Camera2** native bridge.

---

## 1. Physical cell (as built on `magara`/ananas)

```
 ThinkPad E430 (Ubuntu 20.04, yaver agent)            Ender-3 Pro
   │  USB  CH340 1a86:7523  → /dev/ttyUSB0  ───────────►  Marlin board (115200 8N1)
   │                                                       X/Y/Z steppers + endstops
   │  webcam Ricoh UVC → /dev/video0  ◄─ looks at ─────    carriage: screwdriver (nozzle removed)
   │                                                       FAN port → MOSFET → 24V driver (0.4 N·m clutch)
   └─ yaver mesh (QUIC/relay) ──► Talos Convex + web + MCP
```

Machine contract (from `klemens/config.py`, treat as defaults, override per-device):

- Envelope `X=220 Y=220 Z=250` mm. Homing `G28` to stock min-endstops. **Open-loop**
  (no missed-step feedback) → re-home between runs, light payload, modest `M201/M203`.
- Tool = part-cooling FAN MOSFET: **`M106 S255` ON / `M107` OFF** (`tool=fan`), or
  `M42 P<pin> S255/S0` (`tool=pin`). Clutch — not the board — sets the 0.4 N·m torque.
- Feeds (mm/min): travel 3000, approach 600, **drive ≤60**, retract 900. Dwell 500 ms,
  settle 150 ms. Z: safe 25, approach 1.0, engage ~5 (per-jig calibrate).
- Serial: board **resets on port open** → wait ~2 s, flush, then talk. Flow control =
  **send one line, wait for `ok`**. Abort on `Error`/`!!`/`Halt`. On any abort/disconnect
  send `M107` (tool off) + `M84` (motors off).

---

## 2. Layered architecture

```
        ┌──────────────────────────── Product plane (Talos) ───────────────────────────┐
        │  Convex: machineEdgeDevices · machineEdgeCommands · machineTelemetry ·         │
        │          robotCameraFrames(new) · machineRecipes                              │
        │  Web:   RobotCellPanel (jog + live camera + motion log + verify verdict)      │
        │  MCP:   talos_robot_* (run job, jog, verify, status)                          │
        └───────────────▲───────────────────────────────────────────▲──────────────────┘
                        │ /machine-edge/* heartbeat+commands+frames   │ peer-tool calls
        ┌───────────────┴──────────────── Yaver mesh (QUIC/relay/LAN) ┴──────────────────┐
        │   acl_call_peer_tool  ·  /ops  ·  /peer/<deviceId>/ops  ·  device heartbeat     │
        └───────────────▲───────────────────────────────────────────▲──────────────────┘
        ┌───────────────┴──── Edge node (laptop OR phone) ───────────┴──────────────────┐
        │  CONTROL plane                         VISION plane                            │
        │   robot.Driver (Marlin/G-code)          camera.Grab (V4L2 / Camera2)           │
        │    position model + soft-limits          → vision.Verify (LLM, ghost provider) │
        │    homing state · e-stop · tool          → verdict {moved,confidence,reason}   │
        │   robot.Job (pole sequence)             gates the next motion step             │
        └─────────────▲──────────────────────────────────────────────────────────────────┘
                      │ USB serial (/dev/ttyUSB0  |  Android USB-OTG fd)
                 [ Ender-3 Marlin ]   +   [ webcam | phone camera ]
```

Three planes, one edge contract:

- **Control plane** — `robot.Driver`: owns the serial link, the position model, the
  safety envelope, homing/e-stop, and the tool. Pure motion; knows nothing about vision.
- **Vision plane** — `camera.Grab` (one JPEG) → `vision.Verify(frame, expectation)` →
  `{moved: bool, confidence: 0..1, reason, observed}`. Stateless; reused for any check.
- **Verify loop** — the orchestrator: snapshot-before → command motion → snapshot-after →
  ask the LLM "did the carriage move from A toward B?" → record verdict → **gate** the
  next step (halt + e-stop on repeated "did not move" / "obstruction").

The edge contract (the verbs below) is identical whether the edge node is the laptop
agent or the phone bridge — the product plane never knows which is underneath.

---

## 3. Yaver `robot` package (Go) — control plane

New package `desktop/agent/robot/` (sibling of `machine/`). Gated by a `--robot`
serve flag + `config.robot_enabled`, owner-only verbs (mirrors `--machine`).

### 3.1 Transport — `robot/serial_marlin.go` (+ `serial_linux.go`, `serial_other.go`)
Bidirectional Marlin link. Reuses the `machine` termios setup but read+write:
```go
type Conn interface { io.ReadWriteCloser }
func OpenMarlin(dev string, baud int) (Conn, error)   // O_RDWR, 8N1, raw, 2s post-open settle
```
Auto-baud/auto-port (`Discover()`): enumerate `/dev/serial/by-id/*`, `/dev/ttyUSB*`,
`/dev/ttyACM*`; CH340 VID `0x1A86` first; probe `M115` at [115200,250000,57600,…] until a
firmware banner returns (mirrors `talos/robotics/discover_printer.py`).

### 3.2 Protocol — `robot/marlin.go`
```go
type Marlin struct { conn Conn; mu sync.Mutex; log func(string) }
func (m *Marlin) SendOK(ctx, line string) error          // write + wait "ok", surface Error/!!/Halt
func (m *Marlin) Query(ctx, line string) (string, error) // e.g. M114 → parse "X:.. Y:.. Z:.."
func (m *Marlin) Firmware(ctx) (FirmwareInfo, error)      // M115
```
Send-line→wait-`ok`, identical semantics to `klemens/stream.py`. Busy/temp lines are
logged, not errors. Every command is mirrored to a ring buffer (the "motion log").

### 3.3 Motion model + safety — `robot/driver.go`, `robot/safety.go`
```go
type Pos struct{ X, Y, Z float64; Homed bool }
type Envelope struct{ Xmin,Xmax,Ymin,Ymax,Zmin,Zmax float64 } // default 0..220/0..220/0..250
type Driver struct { m *Marlin; pos Pos; env Envelope; tool Tool; estop atomic.Bool; ... }

func (d *Driver) Home(ctx, axes string) error                 // G28; sets Homed, pos=0
func (d *Driver) MoveTo(ctx, x,y,z *float64, feed int) error  // bounds-check → G0/G1 → M400 → update pos
func (d *Driver) Jog(ctx, dx,dy,dz float64, feed int) error   // relative; requires Homed
func (d *Driver) ToolOn(ctx) / ToolOff(ctx) error             // M106 S255 / M107  (or M42)
func (d *Driver) Dwell(ctx, ms int) error                     // G4 P<ms>
func (d *Driver) EStop(ctx) error                             // set estop; M107; M112 (or M410+M84); latched
func (d *Driver) Position(ctx) (Pos, error)                   // M114 readback, reconcile model
```
Safety invariants enforced **before** any byte is written:
1. **Soft-limits**: every absolute target and every relative jog target is clamped to
   `Envelope`; out-of-range → refuse (no clamp-and-go surprises), like `ops_machine` write range-guard.
2. **Homing gate**: relative `Jog` and job execution require `Homed==true`; absolute
   moves below `z_safe` while not homed are refused.
3. **E-stop latch**: once tripped, all motion verbs return `ErrEStopped` until an explicit
   `robot_reset` (which re-requires homing). E-stop also fires on serial disconnect and on
   the verify-loop "obstruction/no-move" escalation.
4. **Tool-safety**: tool is forced OFF on Open, on EStop, on disconnect, and on every
   `MoveTo` that changes X/Y (only drive while plunging Z, per SPEC §6).
5. **Watchdog**: a command with no `ok` within timeout → abort + e-stop (never hang).

### 3.4 Job model — `robot/job.go`
Declarative pole sequence (the `klemens/job.py` program as data, not python):
```go
type Job struct { Pole1 [2]float64; Pitch float64; Poles int; PoleAxis string;
                  Zsafe,Zapproach,Zengage float64; Ftravel,Fapproach,Fdrive,Fretract,DwellMs,SettleMs int }
func (j Job) Steps() []Step    // travel→approach→toolOn→plunge→dwell→toolOff→retract, per pole
```
Each `Step` is executed by the verify loop, not blindly streamed — so vision gates progress.

### 3.5 Verbs / MCP (registered like `ops_machine.go`)
| Verb / MCP tool | Payload | Effect |
|---|---|---|
| `robot_status` | `{}` | enabled, port, homed, pos, tool, estop, lastVerdict |
| `robot_connect` | `{device?,baud?}` | discover/open, M115, leave idle, tool OFF |
| `robot_home` | `{axes?}` | G28 |
| `robot_jog` | `{dx,dy,dz,feed?}` | relative move (homed-gated, clamped) |
| `robot_move` | `{x?,y?,z?,feed?}` | absolute move (clamped) |
| `robot_tool` | `{on:bool}` | M106 S255 / M107 |
| `robot_verify` | `{expectation,beforeFrame?}` | grab frame → vision verdict (no motion) |
| `robot_step` | `{step,verify:true}` | one job step, snapshot-before/after, verdict, gate |
| `robot_run_job` | `{job,verifyEachStep:true,dryRun?}` | full pole sequence with per-step verify |
| `robot_estop` | `{}` | latched stop, tool off |
| `robot_reset` | `{}` | clear e-stop (re-requires home) |
| `robot_sync` | `{talosUrl,orgId,orgSecret,deviceId}` | push heartbeat+telemetry+frame to Talos |

All `AllowGuest=false`. `dryRun` prints the G-code (parity with `stream.py --dry-run`),
sends nothing. `robot_run_job` refuses unless `Homed` and a successful `robot_verify`
baseline exists.

---

## 4. Vision plane — camera-in-the-loop

### 4.1 Capture — `desktop/agent/camera/` (no ffmpeg)
Native **V4L2 MJPEG** grab so a fresh box needs zero apt installs:
```go
// camera/v4l2_linux.go  (build tag linux)
func GrabJPEG(dev string, w,h int) ([]byte, error)  // VIDIOC_S_FMT MJPEG → REQBUFS mmap → QBUF/STREAMON → DQBUF → copy
func ListCameras() []string                          // /dev/video* that advertise CAPTURE+MJPEG
```
Most UVC webcams (incl. the Ricoh on ananas) emit MJPEG directly → the dequeued buffer
*is* a JPEG; no transcode. Fallback path: if only YUYV is offered, convert YUYV→JPEG in
Go (`image/jpeg`). macOS/Windows backends are stubs initially (laptop here is Linux);
the phone uses Camera2 (§6), never this file.

### 4.2 Verify — `desktop/agent/vision_verify.go`
Reuse `ghost_vision`'s provider resolution; new structured call (not a click action):
```go
type Verdict struct { Moved bool; Confidence float64; Reason string; Observed string; Obstruction bool }
func VerifyMotion(ctx, before, after []byte, expectation string) (Verdict, error)
```
Sends **two** images (before/after) + a strict JSON-only system prompt:
> "You verify a Cartesian robot. Given BEFORE and AFTER frames and the expected motion,
> answer JSON `{moved,confidence,reason,observed,obstruction}`. moved=true only if the
> carriage visibly moved consistent with the expectation. obstruction=true if anything is
> in the path or the tool looks crashed."

Single-frame mode (`before==nil`) answers "is the cell in the expected state?" for the
baseline check. Default model = whatever the runner already uses (`OPENAI_*`) or local
Ollama `llama3.2-vision` — same ladder as ghost, **no new keys**.

### 4.3 The loop (in `robot_step` / `robot_run_job`)
```
baseline := grab(); v0 := VerifyMotion(nil, baseline, "cell idle, tool above jig")
for each step:
    before := grab()
    driver.execute(step)              // bounded, clamped, homed-gated
    driver.m.SendOK(ctx, "M400")      // ⚠ WAIT for moves to COMPLETE before snapshotting
    after  := grab()
    v := VerifyMotion(before, after, step.Expectation)   // "carriage moved +5.08mm along X to pole 2"
    record(step, v, before, after)    // motion log + frames → telemetry/Talos
    if v.Obstruction || (!v.Moved && retries exhausted): driver.EStop(); abort
```
**Empirically verified gotcha (2026-06-06 live run):** Marlin returns `ok` when a move is
*queued into the planner*, NOT when motion finishes. Snapshotting the "after" frame on the
command `ok` races the motion and the verifier sees a mid-travel/unmoved carriage (false
"did not move"). Every `MoveTo`/`Jog`/`Home` in `driver.go` therefore issues a blocking
**`M400`** before returning, so `pos` readback (`M114`) and the verify snapshot both reflect
the *settled* position. This is mandatory, not optional — it's the difference between a
trustworthy verdict and a flaky one.
Vision is a **gate**, not just a logger: repeated "did not move" or any "obstruction"
latches e-stop. This is the open-loop machine's substitute for missed-step detection.

---

## 5. Talos wiring ("fully")

Attach to the seams that already exist; add the minimum new surface.

### 5.1 Convex (`talos/cloud/convex`)
- **Reuse** `machineEdgeDevices` — register `{deviceId, machineKey:"creality_ender",
  protocol:"marlin_usb", capabilities:["move","tool","vision"]}`.
- **Reuse** `machineEdgeCommands` — add command types `robot_home`, `robot_jog`,
  `robot_move`, `robot_run_job`, `robot_estop` (queue + approval + result, exactly the
  existing pattern in `machineEdge.ts`).
- **Reuse** `machineTelemetry` — extend with `pos{x,y,z}`, `homed`, `tool`, `estop`,
  `lastVerdict{moved,confidence}`.
- **New** `robotCameraFrames` — `{orgId, deviceId, ts, stepId, storageId, verdict, kind:
  before|after|baseline}`. Frames go to Convex **file storage**, the row holds the id +
  the verdict. (Frames are work-derived images of the user's own bench — allowed; they are
  the user's data in the user's org, not Yaver-platform Convex. See §8.)
- **New** HTTP `POST /machine-edge/camera-frame` in `http.ts` — multipart frame → storage →
  `robotCameraFrames` row; mirrors `/machine-edge/telemetry`.

### 5.2 Web (`talos/web/src/components`)
- **Extend** `robotics-panel.tsx` (already has X/Y/Z jog scaffolding) into `RobotCellPanel`:
  jog pad + Home + **E-STOP** (big red, always enabled) + live camera card (latest frame +
  verdict %), a **motion log** table (cmd → ok/err → verdict), and a "pole N of M" job
  progress bar. Risk-tiered exactly like `MachineEdgePanel` (non-admin read-only).
- Route reuse: the `/api/machine-edge` proxy already serves devices/telemetry/commands;
  add `?action=frames&deviceId=` for the camera strip.

### 5.3 MCP (`talos/cli/cmd/mcp.go`)
New `talos_robot_*` tools that enqueue `machineEdgeCommands` and read telemetry/frames —
sibling to the existing `talos_machine_*` family, so chat/Claude-Code can say
"home the Ender, then drive pole 1 at 0.4 N·m and show me the verify frame."

### 5.4 Sync path
Edge `robot_sync` (Yaver verb) ⇄ Talos `/machine-edge/{heartbeat,telemetry,camera-frame}`
+ command drain — the same bearer-auth bridge `machine_sync` already uses. The edge node
polls its command queue and executes through the §3 driver with §4 verification.

---

## 6. Phone path — old Xiaomi as host + eye ("fully")

The Go agent does **not** run on Android and there is **no** USB-OTG serial today. Two
ways to make the phone the edge node; we ship **B** as the Yaver-native answer and keep
**A** as the power-user escape hatch.

### Option A — Termux (fast, power-user)
`termux-usb` hands a USB file descriptor to a process; run a small arm64 build of the
robot edge that accepts an fd instead of opening `/dev/ttyUSB0`, and capture via
`termux-camera-photo`. Pro: reuse 90% of the Go code (`OpenMarlinFD(fd)`), days not weeks.
Con: Termux install, not App-Store-shippable, no nice UI.

### Option B — Yaver mobile native bridge (the real product)
Make the **Yaver RN app** a first-class robot edge node:
- **`YaverUsbSerial` native module (Android)** — `android.hardware.usb` host mode +
  `usb-serial-for-android` (felHR85) for CH340/FTDI/CP210x. JS contract:
  `open(vid,pid,baud) → handle`, `write(line)`, `onLine(cb)`, `close()`. Exposes the same
  send-line→wait-`ok` Marlin loop in TS (`mobile/src/lib/marlin.ts`).
- **Camera frames** — `expo-camera` `takePictureAsync()` (the app already bundles
  `expo-camera` for QR; reuse it for the verify frame). No new dep.
- **Edge runtime in RN** — `mobile/src/lib/robotBridge.ts` implements the §3 verbs and the
  §4 verify loop, registering as mesh **peer tools** so any Commander (`acl_call_peer_tool`)
  or Talos command drains to the phone. Vision LLM is called via the existing agent
  `/voice`/vision relay or directly with the runner key — the phone holds no model.
- **USB-OTG cable** = USB-C/micro-USB OTG adapter → the Ender's USB-B. Phone powers the
  logic; the printer PSU powers motors (never back-power motors from the phone).
- **Degradation** (per `feedback_visible_failure_over_silent_retry`): if OTG enumerates no
  CH340 → "Plug the printer into the phone (OTG)"; if camera denied → motion still runs but
  verify shows "no eye"; never silently proceed past a failed verify.

Shared contract: laptop agent and phone bridge expose **identical verbs**, so §5 (Talos)
and §3/§4 (semantics) are written once. The phone is just another `machineEdgeDevices` row
with `host:"android"`.

---

## 7. Safety model (physical machine — load-bearing)

- **Refuse, don't clamp** out-of-envelope targets; clamp only the displayed jog preview.
- **Home before motion**; absolute Z below `z_safe` blocked until homed; re-home between jobs.
- **E-stop is latched + always reachable** (verb, MCP, web red button, phone shake→estop
  reuse of the existing ShakeDetector). Clears only via explicit reset → re-home.
- **Tool defaults OFF**; only energized during a Z-plunge step; OFF on any X/Y move,
  abort, disconnect, e-stop. Hardware clutch caps torque at 0.4 N·m regardless of firmware.
- **Watchdog** on every `ok`; serial disconnect ⇒ e-stop + `M107`/`M84`.
- **Vision gate** substitutes for the open-loop machine's missing feedback: no confirmed
  motion ⇒ stop. Obstruction verdict ⇒ stop.
- **Dry-run first**: `robot_run_job{dryRun:true}` prints G-code, sends nothing (parity with
  `stream.py --dry-run`); first real run is single-step, operator-confirmed.

---

## 8. Privacy / data contract

- Yaver-platform Convex (`perceptive-minnow-557`) stays clean per the privacy test — **no**
  camera frames, paths, or G-code go there. Robot device identity is a normal `devices`/
  heartbeat row (deviceId + flags only).
- Camera frames + motion logs + recipes live in the **user's own Talos org Convex**
  (their data, their bench) — same trust boundary as `machineTelemetry` today.
- The vision LLM call uses the runner's existing provider creds (no new keys); frames can
  be pinned to **local Ollama `llama3.2-vision`** for fully on-prem verification.

---

## 9. Phased plan + test strategy

| Phase | Scope | Test (no mocks, real ports) |
|---|---|---|
| **P1** | `robot` pkg: serial+Marlin+driver+safety+job+verbs+MCP; `camera` V4L2; `vision_verify`; `--robot` flag | unit: bounds/estop/job-steps/marlin-parse (fake serial pty); live read-only on ananas (`M115`/`M114`); then **operator-gated** single bounded jog + camera verify |
| **P2** | Talos: command types, `robotCameraFrames`, `/machine-edge/camera-frame`, `RobotCellPanel`, `talos_robot_*` MCP, sync drain | Convex unit + web e2e (jog enqueues command, frame renders); end-to-end laptop→Talos→verify |
| **P3** | Phone: `YaverUsbSerial` Android module, `marlin.ts`, `robotBridge.ts`, expo-camera verify, mesh peer-tool registration | on-device: OTG→CH340 enumerated, `M115` round-trips, bounded jog, camera verify; same verbs as P1 |

Tests follow the repo rule: real serial via a PTY pair / loopback, real HTTP on random
ports, no mocks (`desktop/agent/*_test.go` pattern).

---

## 10. Open calibration items (operator, ~5 min, per jig)
`pole1_x/pole1_y/z_engage` are bench-specific — captured with a jog-and-store flow
(`robot_jog` + `robot_store_pole`) that writes the device's `machineRecipes` row, the
Yaver-native twin of `klemens/calibrate.py`.
