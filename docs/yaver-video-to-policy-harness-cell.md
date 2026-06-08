# Yaver: Operator-Video → GPU-Served Policy → Real Wire-Harness Arm Cell

Status: **research done + policy spine built** (2026-06-08, uncommitted, tests
green). The Go control/serving/recording glue is implemented; the model training
(LeRobot on a rented GPU) and the physical cell are documented procedures, not
code. Builds on [the simulator layer](yaver-robot-simulators.md),
[multi-robot arm layer](yaver-multi-robot-arm-layer.md),
[GPU-rental orchestration](gpu-rental-orchestration.md), and
[the wire-harness jig/formboard design](yaver-wire-harness-jig-formboard-design.md).

> Code is the source of truth. Verify against `desktop/agent/arm/*.go` and
> `ops_*.go`; fix this doc in the same change if they drift.

## The goal (user's words)

Take a **video of an operator** assembling a wire harness, and have **Claude Code
+ a rented GPU (DeepInfra/Salad) + a DOF robot arm + a camera stream + a learned
robot model** imitate it — on a **real** cell, not just the simulator — on real
jigs/formboards (3D-printed **or bought**).

## The honest reality (from deep research)

Three research sweeps (robot-foundation-models/VLAs, imitation-from-video,
GPU-rental serving; and the wire-harness physical/perception side) converge on one
correction to the dream:

- **"Camera on an operator → robot does the task" is still research** for
  contact-rich wire work. The human-hand→gripper **embodiment gap** is unsolved
  end-to-end; the best published results (DexWild, EgoMimic) **co-train** a little
  robot data with human video — nobody ships contact-rich assembly from raw human
  video alone.
- **Limp wire is uncontrollable without fixturing.** Every credible result
  (Nissan/UTK, the 2025 dual-arm multi-branch paper, CaRoBio) automates ONE narrow
  sub-step under heavy fixturing. "DLO (deformable linear object) manipulation" is
  the academic name; state estimation under occlusion is the bottleneck.
- **What actually works in 2025/26:** collect **demonstrations on the real arm**
  (free-drive/kinesthetic or teleop) → train **ACT / Diffusion-Policy / SmolVLA**
  (or fine-tune **π0/openpi**) in **LeRobot** → **fine-tune on a rented GPU
  (per-hour)** → **serve via action-chunking** (per-second serverless or local
  Jetson) → **execute behind a LOCAL safety gate** → **Claude does sub-task
  planning + VLM "is it seated?" verification** (NOT low-level servo).
- **Remote GPU control is viable** *only* with action-chunking + local buffer +
  local safety — proven by Physical Intelligence running real-time π-model
  inference on Modal over a QUIC portal (~10–15 ms net overhead, region-pinned).

So the operator video's realistic role in v1 is **authoring the sub-task sequence
and coordinates** (anchored by a shared fiducial grid) and **co-training data**,
not the sole policy. The arm-demo path is the spine.

### Model menu (what to serve)

| Model | License | Role |
|---|---|---|
| **ACT** (Action Chunking Transformer) | open (LeRobot) | **Default.** ~40M, trains ~8h/A100, 50 Hz, beats big VLAs on a *fixed* task |
| **Diffusion Policy** | open (LeRobot) | alt to ACT, smoother multimodal |
| **SmolVLA** (450M) | open (LeRobot) | best "fancy but practical": async inference, runs on one consumer GPU |
| **π0 / openpi** | **Apache-2.0** | only open *and* commercially-licensed big VLA; language-conditioned multi-SKU |
| OpenVLA-OFT (7B) | MIT | ~50 Hz, heavier |
| **GR00T N1.5** | **non-commercial** | eval only — license blocks product use |
| RT-2 | closed | reference only |

**LeRobot** (HuggingFace) is the unifying framework: dataset format + training for
ACT/DP/SmolVLA/π0 + deployment. Our demo recorder writes a LeRobot-convertible
dataset on purpose.

## Architecture (where each piece runs)

```
┌───────────────── LOCAL (the box at the cell) ─────────────────┐
│ camera(s) ─ wrist + overhead RGB        arm joint state        │
│        │                                      │                │
│        ▼   arm.Controller.RunPolicy (arm/policy.go)            │
│   ┌─────────────────────────────────────────────────────┐     │
│   │ observe → infer(remote) → SAFETY GATE → execute chunk │     │
│   │ SafetyGate: per-joint range refuse · max-step cap ·   │     │
│   │ stale-server watchdog · e-stop latch  (MANDATORY)     │     │
│   └─────────────────────────────────────────────────────┘     │
└──────────────┬──────────────────────────────▲────────────────┘
       obs {frames+state+prompt}          action chunk {joint targets}
               ▼                                │  (HTTP /act today; QUIC stream = next)
┌──────── RENTED GPU policy server (Salad/DeepInfra/Modal/RunPod) ┐
│ served ACT/SmolVLA/π0 checkpoint · /act · /healthz             │
└───────────────────────────────────────────────────────────────┘
               ▲ (seconds cadence, NOT servo loop)
┌──────── Claude (host) via Yaver MCP ───────────────────────────┐
│ task decomposition · robot_camera VLM "seated? Y/N" gate/retry  │
└────────────────────────────────────────────────────────────────┘
```

## What's built now (this change)

All glue is in the `arm` package + `ops_*` verbs, reusing the existing
arm.Controller, camera path, sim, GPU-rental, and vault.

| Piece | File | What |
|---|---|---|
| Policy client | `arm/policy.go` | `PolicyClient` — obs{frames+state+prompt} → action chunk over HTTP (`/act`,`/healthz`); bearer auth |
| **Safety gate** | `arm/policy.go` | `SafetyGate` — per-joint range **refuse** (not clamp) + max-step cap + unknown-joint reject |
| Chunk executor | `arm/policy.go` | `Controller.RunPolicy` — observe→infer→gate→execute loop; e-stop on any violation; stale-server watchdog (http timeout); `stop()` interrupt; budgets (maxChunks/maxSeconds) |
| Demo recorder | `arm/demo.go` | `DemoRecorder` — dense {frame,state,pose} capture → `<name>/episode_NNN/{meta.json,frames/*.jpg,states.jsonl}` (LeRobot-convertible) |
| Policy verbs | `ops_policy.go` | `policy_bind` (vault), `policy_status` (+health), `policy_step`, `policy_run`, `policy_stop` |
| Demo verbs | `ops_policy.go` | `arm_demo_start`, `arm_demo_stop`, `arm_demo_list` |
| Jig verb | `ops_jig.go` | `robot_jig_generate` — printable OpenSCAD fixture from grid params |

Tests (`arm/policy_test.go`, `arm/demo_test.go`): fake policy server + in-memory
backend cover the happy path, **safety trips** (out-of-range → e-stop, oversized
jump → e-stop), stale-server watchdog, stop-interrupt, and demo capture/list.

**It runs against the simulator today.** Because the sim (driver `sim`) is an
`arm.Backend` with a rendered-frame camera, `policy_run` works end-to-end on the
sim as a **dry-run twin** before any hardware moves — the recommended way to prove
a policy safely.

### The policy-server contract (what the rented GPU serves)

```
POST /act  { images:{"main":"data:image/jpeg;base64,…"}, state:{joints:{J1:..}, pose?}, prompt? }
       ->  { actions:[ {joints:{J1:..,J2:..}} , … ], done?:bool }
GET  /healthz -> 200
```

A thin `yaver_policy_server.py` (next step) wraps any LeRobot/ACT/π0 checkpoint to
speak this — load the checkpoint, accept the obs dict, return the predicted action
chunk in joint space. Serve it on the GPU you rent via `cloud_provision
host=salad` → `gpu_status` → use the endpoint in `policy_bind`.

## End-to-end flow (operator → running cell)

1. **Build the cell.** Buy or print a grid baseboard (openGrid 28 mm / Gridfinity
   42 mm / industrial tooling plate). Fixtures: connector nests with **lead-in
   funnels** (absorb robot positioning error), routing combs, a wire-presentation
   cradle — `robot_jig_generate` emits printable OpenSCAD, or buy equivalents.
   Put **AprilTags at the grid corners**: the grid pitch becomes the metric
   **world frame** shared by robot, camera, and demo video.
2. **Calibrate.** Hand-eye (`cv2.calibrateHandEye`, ChArUco, ≥15 varied poses);
   define the `board` frame from the corner tags. Eye-to-hand overhead RealSense
   D435 (or monocular + tags for a flat board).
3. **Demonstrate.** `arm_freedrive on` → hand-guide the arm through the sub-step;
   `arm_demo_start {name,prompt,fps}` records dense frames+state; `arm_demo_stop`.
   Collect 30–50 episodes for a first sub-step, 200–400 for full contact-rich
   insertion. (Operator video → co-training data is a later accelerator.)
4. **Train (rented GPU).** Convert episodes to a LeRobotDataset, fine-tune ACT
   (~8h/A100) or SmolVLA. Rent per-hour (Lambda/RunPod/Salad/Vast) — latency
   doesn't matter for a batch job.
5. **Serve (rented GPU or Jetson).** Run `yaver_policy_server.py` with the
   checkpoint; expose the endpoint. For tight contact loops or a flaky factory
   network, serve on a **local Jetson** instead (ACT ~83 Hz on AGX Orin).
6. **Dry-run in sim.** Point the same policy at the sim arm; `policy_run` — confirm
   the gate never trips and motion looks right.
7. **Run on hardware.** `policy_bind {endpoint}` → `policy_run {prompt}`. Every
   chunk passes the safety gate; Claude verifies each seat via the camera and
   gates/retries the next sub-task. `policy_stop` / E-STOP always available.

## Fixtures: buy or print (the "grid things")

- **Buy:** openGrid/Gridfinity printed-or-purchased plates, industrial workholding
  grid/tooling plates, vendor connector nests/combs. Fastest.
- **Print:** `robot_jig_generate` (parametric OpenSCAD today: klemens/pocket grid)
  → extend toward connector nests, combs, routing channels, strain reliefs.
- **The wedge:** no vendor ships a **netlist → printable-grid-formboard
  compiler**. CAD tools (Siemens Capital, Zuken E3.formboard) flatten 3D→2D
  nail-up; parametric grid generators print fixtures; the glue (harness flat
  pattern → snap connectors/branches/clip-runs to grid cells → emit STLs + the
  world-frame coordinate table) is the buildable novelty, aligned with the
  existing harness-compiler design.
- **Altınay** is a general automotive integrator, **not** a harness-jig vendor (no
  public harness/board product) — a potential integration partner, not a source.

## Verified end-to-end (2026-06-08, on a dev Mac, no GPU/pybullet)

The whole spine was **run live** via `TestSimPolicyE2E` (gated `SIM_E2E=1`):
real `SimBackend` spawned the harness on the **kinematic engine** (no pybullet —
see below), `HTTPCamera` pulled the rendered frame, the **reference policy
server** (`arm/yaver_policy_server.py`, standing in for a rented-GPU model)
returned action chunks, and `RunPolicy` drove the sim arm through the safety gate
to the commanded goal:

```
sim arm: Generic 6-DOF (kinematic), 6 DOF
RunPolicy: {OK:true Chunks:1 Steps:4 Done:true Stopped:done TookMs:4}
final joints: J1=30.00 J2=-20.00 (goal 30/-20) — reached via 4 safety-gated steps
```

**Kinematic engine.** The harness now auto-falls-back to a no-physics, no-GPU
`KinematicSim` (joints integrate instantly, frames rendered with PIL) when
pybullet isn't installed — so the dry-run runs on any box (dev Mac, CI). pybullet
remains the high-fidelity engine for contact-rich work; install it (or run on the
Linux box) for real physics. Run it: `SIM_E2E=1 go test ./arm/ -run TestSimPolicyE2E -v`.

**Real PyBullet, on a fresh Linux box (2026-06-08).** Spun up a throwaway Hetzner
box, `pip install pybullet` (built from source — no cp312 wheel), ran the same
harness with `--engine pybullet` + the reference policy server, and drove the
observe→act→execute loop against actual physics: `engine=pybullet`, joints
`J1..J6` with catalog limits, loop converged to `J1=29.66 J2=-20.06` for goal
`30/-20`, `done=true` — note the non-exact settle (real dynamics) vs the kinematic
engine's exact value. Box deleted immediately after. This surfaced + fixed a real
bug: `createMultiBody` gave the builtin arm auto-named, limitless joints; pinned
to `J1..J6` + catalog limits so builtin:arm6 is the same arm on every engine.

## Gaps / next (honest)

- **`yaver_policy_server.py`** — shipped as a **reference** proportional-control
  policy (stdlib, runs anywhere). Replace `predict()` with a LeRobot/ACT/π0
  checkpoint for real inference (the swap is ~5 lines, documented in the file).
- **True streaming execution.** `RunPolicy` executes a chunk as sequential
  blocking moves (correct/safe on cobot+sim). 50 Hz open-loop streaming needs a
  streaming `Backend` method (`SetTargetStream`) + a local action buffer — the
  Physical-Intelligence pattern. Design seam noted.
- **Multi-camera obs.** Today one `main` frame; wrist+overhead is a small
  extension (the obs `images` map already supports names).
- **Force in the loop.** Contact-rich insertion wants `arm.ForceBackend`
  (Wrench/ForceMove exists as an interface; no backend implements it; the sim
  could via PyBullet contacts).
- **LeRobot dataset converter** (our jpg+jsonl → parquet+video) — a small script.
- **Demo-from-video** (HaMeR/WiLoR hand pose + CoTracker + fiducial retarget) —
  research-grade co-training accelerator, not a v1 dependency.
- **Mobile/web UI** for policy run + demo record — not wired this pass (verbs are
  MCP/CLI-drivable now).
- **GPU rental**: add RunPod/Modal providers + a `kind:"policy"` endpoint registry
  alongside the LLM inference binding.

## Privacy

Demos and frames are local-first (`~/.yaver/arm-demos`); they leave the box only
as a training upload the user initiates. The policy endpoint + key live in the
vault (encrypted). Nothing new crosses to Convex.
