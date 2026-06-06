# Yaver Robot Cell — teach-and-repeat, motor/GPIO control, multi-camera QC

> Deep analysis of four converging requirements on top of the mesh-native robot
> cell (`docs/yaver-robot-fleet-mesh-design.md`):
> 1. **Teach-and-repeat** — jog the Cartesian robot on the phone, *record* the
>    moves/G-code, save as a program, *replay* it (the screwdriving sequence).
> 2. **Screwdriver motor manipulation** — not just on/off, but *rotation*
>    (turns / speed / direction) and generic **GPIO** from the phone.
> 3. **Convex support** — persist programs/config/audit across agent+cloud+mobile
>    within Yaver's privacy contract.
> 4. **Multi-camera QC** — extra camera-phones (e.g. top-down) as additional
>    inputs; the UI shows several feeds and the **AI verifies across multiple
>    cameras** (e.g. "heatshrink visible from the top").
>
> Everything rides the existing mesh: each capability is an agent ops verb
> addressed by deviceId, so it works through any gateway from one app over many
> robots. Status: design + edge engine started (`robot/program.go`, `Backend.Raw`).

---

## 1. Teach-and-repeat (the "memory" / teach-pendant)

### Model
A **Program** is an ordered list of **Steps**. Teaching = the phone records each
action it already issues while you jog; the edge stores + replays it.

```
Step.Type ∈ home | move(x,y,z,feed) | jog(axis,dist,feed) | tool(on)
          | dwell(ms) | rotate(turns,rpm,ccw) | gpio(pin,value) | screw(pole macro)
```

### Where recording happens — **client-side** (the phone is the pendant)
The phone *issues* every command, so it *knows* every step. As you tap
`Z+10 / X+5 / screwdriver on / …`, the app:
- executes the verb (so you see it move + camera-verify), **and**
- appends the step to a local `recordedSteps[]`.
Plus two pendant affordances:
- **"Record point"** → captures the current absolute `M114` position as a
  `move` waypoint (teach-by-positioning: jog the bit onto a screw head, record it).
- **"Screw here"** → appends a `screw` macro at the current XY (plunge→drive→retract).

"Save" → `robot_program_save{name, steps}`; the **edge** persists it
(`~/.yaver/robot-programs/<name>.json`, `robot.ProgramStore`). Replay →
`robot_program_run{name, verifyEachStep}` → `Controller.RunProgram` executes each
step **camera/encoder-verified**, halting on the first failure or e-stop.

### Verbs (agent)
`robot_program_save · robot_program_list · robot_program_get · robot_program_delete
 · robot_program_run · robot_program_stop`. (Engine: `robot/program.go` — built.)

### Why client-records / edge-replays
The edge stays stateless about "teaching" (no fragile session); it just stores
and replays programs. The phone owns the UX. A program is portable data — the
same JSON runs on any robot device (one taught sequence, many cells).

---

## 2. Screwdriver motor manipulation — rotation + GPIO

On/off (`M106`/`M107` FAN, or `M42`) only *energises* a DC driver. Real
manipulation = **rotation** (turns, speed, direction) and **GPIO** for driver
pins. The clean, no-extra-cabling path is to drive the screwdriver as a **stepper
on the freed extruder (E) axis** (`docs/yaver-phone-harness-analysis.md` §3.3b).

### Rotation
| Hardware | How | Verb → G-code |
|---|---|---|
| **E-stepper screwdriver** (repurposed extruder) | exact turns + speed + direction | `robot_screw_rotate{turns,rpm,ccw}` → `M302 P1` (cold) ; `M83` ; `G1 E<±turns·EperTurn> F<rpm-feed>` |
| **Servo** screwdriver/flipper | angle | `robot_screw_rotate` → `M280 P<i> S<angle>` |
| **DC + companion driver** | PWM speed + dir pin | companion verbs (PWM + GPIO dir) |
| TMC2209 on E | **torque/StallGuard** while rotating | read during rotate → seat-at-torque |

Calibration: `EperTurn` (E units per full rotation) is a per-rig constant
(`robot_config`), so "rotate 3 turns" is exact. CCW = negative E.

### GPIO (generic, for motor drivers / relays / sensors)
- `robot_gpio{pin,value}` → `M42 P<pin> S<value>` — set any board pin
  (driver enable, direction, a relay/MOSFET, an LED).
- `robot_gcode{line}` — raw passthrough (power users): any `M`/`G` command.
- (companion path) `gpio_set` / `sense_read` for pins the printer board lacks
  + INA219/HX711 torque (`docs/yaver-companion-mcu.md`).
- Read-back: `M119` (endstops), companion `SENSE` (current/force) — so the app
  can show pin/sensor state, not just write.

`Backend.Raw(line)` (added to the interface; serial + bridge impls) carries all
of these; `Controller.Rotate`/`Controller.GPIO` wrap them with the e-stop/homing
safety the rest of the controller enforces.

### Safety
Rotation honours the latched e-stop; tool/motor forced off on e-stop, disconnect,
and any abort; GPIO writes are owner-only and refused while e-stopped; a
companion clutch (or a current/StallGuard limit) caps torque mechanically.

---

## 2b. Modular profiles (device-type settings)

The robot cell is **modular** — a device exposes only the modules its hardware
has, chosen by a **profile** the user picks in Yaver:

| Profile | Modules enabled | Verbs available |
|---|---|---|
| **Ender-3 Cartesian** | motion (XYZ Marlin) + camera | home/jog/move/verify/snapshot |
| **Ender-3 Cartesian + screwdriver** | motion + tool/rotate + gpio + camera | all robot_* |
| **Screwdriver only** | tool/rotate + gpio (+ camera) — **no XYZ** | tool/screw_rotate/gpio/snapshot |
| Custom | any subset | the subset's verbs |

Profile → a `RobotConfig{kind, modules[], serial, toolMode, ePerTurn, screwParams,
cameraDev}` stored on the **edge** (`robot_config` get/set verb, a local JSON like
the program store). The agent advertises its **modules** in the heartbeat
capability, and the mobile UI shows only the relevant controls (a screwdriver-only
device shows the rotation/torque panel, no jog pad). Motion verbs on a
screwdriver-only device return `no_motion`. So "control only the screwdriver with
Yaver" is just the **screwdriver-only profile** — same verbs, no XYZ required.
Screwdriver-only hardware drives the motor via the companion MCU or a USB motor
driver (rotate → companion PWM/step, or Marlin-E when a board is present).

## 3. Config + persistence — open-core (Yaver OSS, Talos proprietary)

Yaver is **open source**; the proprietary value (a wire-harness company's
**screwdriver squeezing/torque recipes**, terminal-keyed configs) lives in
**Talos**, the proprietary tier. So persistence is **local-first with Talos as an
optional config-provider + backup** — *not* Yaver's platform Convex:

```
  Yaver (OSS)  ── local-first ──>  edge files: profiles, programs, run logs
       │  ConfigProvider interface (open):
       │    • LocalProvider (default, OSS) — the edge JSON
       │    • TalosProvider (proprietary)  — fetch + backup
       ▼
  Talos (proprietary tier, the company product):
     • CONFIG SOURCE: screwdriver squeezing/torque config keyed by terminal/
       cable/product → Yaver loads it (robot_config can pull from Talos)
     • BACKUP SERVICE: a data model that backs up local programs/configs/QC
       frames + run history (the production record)
```

### The boundary — Yaver *runs*, Talos *configures* (the moat)

The open/proprietary line is **capabilities vs. config-tools**, not features vs.
features. Yaver gives the user every raw capability; the *valuable tuning* that a
wire-harness company sells is the **config tooling**, which is **not in Yaver**:

| | **Yaver (open)** — "do whatever you want" | **Talos (proprietary)** — "not open source" |
|---|---|---|
| Move/jog/home/rotate/GPIO/camera/verify | ✅ all generic control | — |
| **Raw teach** (record your jogs → a program) + run it | ✅ | — |
| Profiles (cartesian / +screwdriver / screwdriver-only) | ✅ pick modules | — |
| **Squeezing-config TOOL** (author torque/depth/dwell/approach **recipe** for a specific terminal/cable, the tuned numbers) | ❌ **not here** | ✅ the proprietary tool + algorithm |
| **Recipe library** (terminal → seated-torque profile, QC criteria) | ❌ | ✅ the company IP |
| **Tuning / optimization** (auto-derive the squeeze params from a part) | ❌ | ✅ |
| **Backup / production data model** (programs, runs, QC history) | local file only | ✅ the backup service |

So Yaver **consumes** an opaque config/recipe at run time but cannot **author**
one — `robot_config` can *apply* a recipe and `RunProgram` can *execute* it, but
the **squeezing recipe is produced by Talos's config tool**, not Yaver. A
Yaver-only user can hand-teach a crude program by jogging; the *correct, tuned
squeeze for terminal AI-0.75-8 at 0.4 N·m* comes from Talos. That's the moat:
open engine, proprietary recipes + the tools that make them.

Interface (kept narrow + open in Yaver): a `ConfigProvider{ GetRecipe(key) →
opaque Program/params ; Backup(data) }` with a `LocalProvider` (OSS) and a
`TalosProvider` (proprietary plugin). Yaver ships the interface + local provider;
Talos ships the provider that fetches recipes + backs up. **Yaver's platform
Convex stays out entirely** — only the open mesh (discovery + capability flag).

Yaver's privacy contract forbids work-derived data (G-code, file contents,
camera frames, paths) in the **platform** Convex (`perceptive-minnow-557`,
enforced by `convex_privacy_test.go`). So:

| Data | Lives in | Why |
|---|---|---|
| **Programs** (steps = G-code, work-derived) | **edge file** (`ProgramStore`) + optional **user's Talos org Convex** (`robotPrograms` table) | work content → never Yaver platform Convex |
| Screwdriver/motor **config** (EperTurn, tool mode, pins, torque target) | edge `robot_config` + Talos org | rig setup, user-owned |
| **Run audit** (program X ran on device Y, seated N/M screws, ts) | activity-summary in Yaver Convex (action+target+outcome+ts only) **+** richer log in Talos | summary allowed; detail = user's org |
| Robot **capability** + program **names** (index, no steps) | Yaver `devices`/heartbeat capability flag | discovery only, no work content |
| **Camera frames** | edge → mobile over mesh; QC keepers → **Talos** `robotCameraFrames` + storage | frames are the user's bench → their org, not Yaver platform |

So "Convex support" = (a) advertise the **robot capability** + program index for
fleet discovery in Yaver Convex; (b) sync **programs/config/QC-frames** to the
**user's Talos org** Convex (the existing `machineEdge*`/`robotCameraFrames`
seam, `docs/robot-screwdriver-cell.md` §5) for cross-device access and history.
The control path stays mesh-direct (agent verbs); Convex is persistence +
discovery + audit, never the live control channel.

---

## 4. Multi-camera QC + AI multi-view

### Camera source = any Yaver device with a camera
`robot_snapshot` / `robot_stream` are **per-device** verbs. So a top-down
**camera-phone**, a side webcam on the robot edge, and an overhead laptop are all
just **deviceIds that answer `robot_snapshot`**. No special infra — the same mesh
ops path. (Phone-as-camera-source needs the RN app to expose its Camera2 frames
as a `robot_snapshot`-style verb — the one net-new piece; until then any
agent-on-a-box-with-a-webcam is a source.)

### The mobile UI — multiple feeds
The Robot Cell screen gains a **camera sources** list (pick N devices). It shows
a primary feed (the robot's eye) + a strip of secondary feeds (top-down QC,
side). Each polls its source's `robot_snapshot`. So you watch the cell from
several angles while you teach/run.

### AI verification across cameras
The verify loop generalises from one frame to a **frame set**: `VerifyMotion`
(and a new `VerifyQuality`) accept `before[]`/`after[]` from multiple sources and
send them all to the vision LLM with a multi-view prompt:
> "From the SIDE and TOP views: did the carriage move +5.08mm to pole 2 (side),
> and is the heat-shrink seated and visible at the top (top)? JSON
> {moved,seated,shrinkVisible,confidence,obstruction}."

So a step can be gated on **geometry (side cam) + quality (top cam)** at once —
e.g. "screw driven AND heatshrink intact from above." Per-source weights and a
QC pass/fail (`robot_qc{checks:[{source,question}]}`) make it a first-class
quality gate: a pole only counts done when motion + torque + top-view QC agree.

### Where QC frames go
Keeper frames (a QC pass/fail + its top-view image) sync to the user's Talos
`robotCameraFrames` for the production record; transient frames stay on the mesh.

---

## 5. Unified architecture (how it all composes)

```
  ONE mobile app
   ├─ Teach pendant: jog → record steps → save program (edge) → play (verified)
   ├─ Motor/GPIO: rotate(turns,rpm,dir) · gpio(pin,val) · gcode  ── all robot_* verbs
   ├─ Cameras: N sources (robot edge + QC phones), multi-feed view
   └─ AI verify: multi-camera frame set → LLM (geometry + quality)
        │  callOps(robot_*, machine=<robotDeviceId>) over the mesh (any gateway)
        ▼
  Robot edge agent (laptop / phone+kit)
   robot.Controller ── SerialBackend/Raw ── Marlin (XYZ + E-stepper screwdriver + M42 GPIO)
                    └─ Companion (GPIO/PWM, INA219/HX711 torque)  [optional]
   ProgramStore (edge JSON)            GstCamera/Camera2 (robot_snapshot/stream)
        │ capability + program index + run-audit summary
        ▼
  Convex:  Yaver platform (discovery + audit summary only)  +  user's Talos org
           (programs · config · QC frames — the work-derived data)
```

Every box is an agent verb over the mesh; persistence is privacy-tiered; cameras
are just more deviceIds. The same model scales to many robots and many cameras
from one app.

---

## 6. Phased plan

| Phase | Scope | Test |
|---|---|---|
| **T1 (edge, in progress)** | `program.go` (Program/Step/Store/RunProgram) ✅; `Backend.Raw` ✅; add `Controller.Rotate`/`GPIO` + verbs (`robot_screw_rotate`, `robot_gpio`, `robot_gcode`, `robot_program_*`) | `/ops` headless: save a 3-step program, run it; rotate; M42 toggle |
| **T2 (mobile teach)** | record toggle + step list + "record point"/"screw here" + save/list/play UI | teach a 2-pole program on the phone, replay |
| **T3 (motor UI)** | rotation control (turns/rpm/dir dial) + GPIO panel + `EperTurn` calibration | rotate the screwdriver from the phone |
| **T4 (multi-cam)** | camera-source picker + multi-feed view; `VerifyQuality` multi-frame; phone-as-camera-source (RN Camera2 → `robot_snapshot` verb) | top-down QC phone feed shown + AI "heatshrink visible" check |
| **T5 (Convex)** | robot capability advertise + program/QC sync to Talos org; run-audit summary | program list + QC history in cloud |

T1 is the load-bearing engine (mostly built); the rest layers the UX + cloud on
the same verb surface.
