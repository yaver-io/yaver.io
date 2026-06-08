# Yaver Robot Simulators + Robot-Model Support

Status: **built** (PyBullet backend + URDF import + curated catalog + mobile/web
wiring), 2026-06-08. MuJoCo is a clean seam, not yet implemented. Device
verification (a box with `pip install pybullet`) is the only remaining step.

> Code is the source of truth. This doc describes the design; if it disagrees
> with `desktop/agent/arm/*.go`, the code wins — fix the doc in the same change.

## Why

Yaver is the single management layer for any robot arm (Fairino, Elephant
myCobot, PAROL6, generic line-protocol). The gap: you needed **hardware** to do
anything — to learn the UI, teach a program, or demo the product. A simulator
closes that gap: jog, teach-and-repeat, and *watch the arm move* with no robot,
on a headless Linux box (incl. ARM cloud). It's also the natural home for
"support any robot model" — because every model is a URDF, and the sim is what
consumes URDFs.

## The one design decision: a sim is just an `arm.Backend`

The whole feature rides on one choice: the simulator implements the **existing**
`arm.Backend` interface (`desktop/agent/arm/backend.go`) as a new driver,
`"sim"`. Consequences, all of which fell out for free:

- Every `arm_*` ops verb already works against it: `arm_jog`, `arm_movej`,
  `arm_movel`, `arm_home`, `arm_status`, `arm_freedrive` → `arm_teach_capture`
  → `arm_program_save`/`arm_program_run`. No new control path.
- The **camera path renders the simulation**: the harness serves a JPEG at
  `/frame.jpg`; `ensureArm` wires that as a `robot.HTTPCamera`, so
  `arm_snapshot` / `arm_look` / move-and-verify and the host `robot_camera` MCP
  tool show the arm moving — through the identical code a real webcam uses.
- The mobile/web pickers are **data-driven** from `arm_drivers` / `arm_models`;
  adding `sim` to those catalogs makes it appear in the existing UI. The only
  bespoke UI is a model picker, a SIM badge, and a "Reset sim" button.

```
phone / web  ──/ops arm_*──►  agent  ──http JSON──►  PyBullet harness (python)
     ▲                          │                          │ renders
     └────── arm_snapshot ──────┘◄──── HTTPCamera ─────────┘ /frame.jpg
            (relay/LAN, iOS-safe snapshot-poll — same as Remote Desktop)
```

## Engine choice — PyBullet now, MuJoCo seam

Researched the field (MuJoCo, Gazebo, Isaac Sim, PyBullet, Webots, Genesis,
CoppeliaSim, Drake, SAPIEN, Brax/MJX). For "drive from a Go layer + stream
frames to a phone on a headless Linux ARM box":

- **PyBullet (chosen first)** — zlib licence, pip-installs on x86 + ARM,
  `DIRECT` mode + `ER_TINY_RENDERER` renders frames **with no GPU and no
  display**, and its joint/IK API maps 1:1 onto Jog/MoveJoint. Fastest path to a
  working demo. `Sim.Engine = "pybullet"`.
- **MuJoCo (seam)** — Apache-2.0, native arm64, OSMesa software offscreen render
  (no GPU), `mujoco_menagerie` = 70+ curated arm models. Higher fidelity. Drops
  in behind the *same* HTTP contract: implement `--engine mujoco` in the harness
  (or a `libmujoco` cgo backend) and set `Sim.Engine = "mujoco"`. Nothing else
  changes.
- **Isaac Sim / SAPIEN** — rejected for the default target: require an NVIDIA
  RTX GPU (Isaac needs RT cores, no CPU fallback). Only relevant on rented GPU
  boxes.

## Model format — URDF is canonical

URDF is the interchange lingua franca (every sim imports it; ROS-Industrial,
`robot_descriptions.py`, `mujoco_menagerie` are URDF-first). So "support any
robot model" = "parse a URDF":

- `arm/urdf.go` — pure-Go `ParseURDF([]byte) (ArmInfo, error)`. Actuated joints
  (revolute/continuous/prismatic) in document order become the DOF; fixed /
  floating / mimic joints are skipped; SI→industrial unit conversion (rad→deg,
  m→mm). This is the same data-not-code contract the `arm` package already uses
  (`JointSpec` mirrors `<joint><limit>`).
- The harness reads the loaded robot's joints back via PyBullet and returns them
  through `/describe`, so DOF is always **read from the model**, never hardcoded.

### Where models come from (`arm/models_sim.go`)

`SimSource` is the load token; there's always a zero-network fallback:

| token | how it loads | network |
|---|---|---|
| `builtin:arm6` | procedural 6-DOF arm via `createMultiBody` | none |
| `pybullet:<path>` | URDF bundled in the pybullet wheel (kuka_iiwa, franka_panda) | none |
| `desc:<name>` | `robot_descriptions.py` (ur5e, ur10e, iiwa14, gen3) | once, cached |
| `urdf:<path-or-url>` | any URDF on disk or http(s) | if URL |

Curated catalog: Generic 6-DOF (default), KUKA iiwa, Franka Panda, UR5e, UR10e,
KUKA iiwa14, Kinova Gen3. `arm_models` returns these under a `Simulator` vendor
group alongside the hardware models, so the existing model picker shows them.

## Components built

| Layer | File | What |
|---|---|---|
| URDF import | `arm/urdf.go` (+test) | URDF → `ArmInfo` |
| Catalog | `arm/models_sim.go` (+test) | `SimModels()`, `SimSource` field on `RobotModel` |
| Config | `arm/config.go` | `SimConfig{engine,model,port,python,gui}`, defaults, `Enabled()` |
| Backend | `arm/backend_sim.go` (+test) | `SimBackend`: spawn/manage harness, bridge contract, `FrameURL`, `Reset`, `LoadModel` |
| Harness | `arm/sim_harness.py` (embedded) | PyBullet, the JSON contract + `/frame.jpg` |
| Ops | `ops_armcell.go`, `ops_sim.go` | `sim` driver case + camera wiring; `sim_models`/`sim_status`/`sim_load`/`sim_reset` |
| Mobile | `mobile/src/lib/armClient.ts`, `mobile/app/arm.tsx` | sim methods + model picker + SIM badge + Reset sim |
| Web | `web/components/dashboard/ArmCellView.tsx` | same |

The harness is **embedded in the agent** (`//go:embed sim_harness.py`) and
extracted to `~/.yaver/sim/` on first use, so it travels with the binary — no
repo checkout on the box. A binary upgrade ships a new harness automatically
(content-hash refresh).

## Lifecycle

1. User picks a Simulator model (or sets driver `sim`) → `arm_config_set`.
2. First arm verb calls `ensureArm` → `NewSimBackend` → `Connect`:
   - if a harness already answers on the port, reuse it (agent restart);
   - else `exec` `python3 ~/.yaver/sim/sim_harness.py --port --model`, wait for
     `/healthz` (40s budget — first `desc:` load may download), surface stderr
     on failure (e.g. "pip install pybullet").
3. Control/teach/camera all flow through the existing arm verbs.
4. `sim_load` hot-swaps the robot in the running sim (no restart) and
   `RefreshDescribe`s the new DOF; `sim_reset` re-homes.

## Cross-network frames

The harness frame URL is `127.0.0.1` on the box. Phone/web reach it **through**
`arm_snapshot` over `/ops` (LAN-first, relay fallback) — never directly. This is
the same iOS-safe, relay-safe snapshot-poll the Remote Desktop view uses; we
deliberately did *not* add a new transport. (A future optimisation: MJPEG over
the existing `/rd/stream`-style pipe for smooth web playback.)

## Privacy

Nothing new crosses to Convex. Sim runs entirely on the box; frames and joint
state flow P2P like any arm. The `robotAction` audit summary is unchanged.

## Gaps / next

- **Device-verify**: run on a box with `pip install pybullet numpy pillow`;
  confirm `/frame.jpg` renders and `desc:` models fetch. (Go side is unit-tested
  against a fake harness; the Python side is syntax-checked only here.)
- **MuJoCo engine**: implement `--engine mujoco` behind the same contract for
  fidelity + `mujoco_menagerie`.
- **Client-side render** (web only): `three.js` + `urdf-loader` to render the
  URDF in-browser from the joint-state stream (no server render). RN stays on
  MJPEG/snapshot (Hermes has no DOM).
- **MJPEG live stream** for smoother web playback vs 1 Hz snapshot-poll.
- **ForceBackend in sim**: PyBullet exposes contact forces — could implement the
  optional `arm.ForceBackend` (Wrench/ForceMove) so insert/seat/pull-test demos
  work in sim too.
