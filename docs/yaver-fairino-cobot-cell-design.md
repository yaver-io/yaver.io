# Yaver × Fairino — AI-Driven Cobot Assembly Cell (Deep Design)

Status: design. Branch `feat/yaver-robot-cell`. Written 2026-06-06.
Builds on `docs/robot-screwdriver-cell.md`, `docs/yaver-robot-fleet-mesh-design.md`,
`docs/yaver-robotics-edge-vibing.md`, `docs/robot-protocol.md`, and the Talos
robotics bridge (`talos/cloud/convex/robotics.ts`). Read those first; this doc
extends them to a **6-DOF Fairino collaborative arm** running next to the existing
Ender-3 Cartesian screwdriver cell, with an AI supervisor that **plans, verifies
each motion from vision, and troubleshoots stuck/fail cases**.

> **Source-of-truth rule (CLAUDE.md):** every Fairino fact below was pulled from
> web research (FAIR-INNOVATION GitHub, the English manual, the protocol PDFs) and
> is flagged where uncertain. Confirm the ⚠️ items against a live controller before
> building timing-critical code. Every Yaver fact is grounded in code referenced by
> `file:line`; if the code drifts, fix this doc in the same change.

---

## 0. The one big idea

**Fairino is just a new `robot.Backend`.** Everything you already built and proved
live on 2026-06-06 — the move-and-verify `Controller`, the vision gate, the encoder
cross-check, the e-stop latch, the torque-gated screw loop, the mesh-routable
`robot_*` ops verbs, the mobile jog UI, the Talos command-queue bridge — is
**embodiment-agnostic**. The Ender-3 is `BridgeBackend`/`SerialBackend`. The Fairino
arm is a new `FairinoBackend` implementing the same `Backend` interface
(`desktop/agent/robot/backend.go:18-30`), plus a small extension for the things a
Cartesian printer doesn't have: **6-DOF pose, force/torque, and high-rate servo
streaming.**

The AI architecture is the 2026 SOTA consensus and it maps 1:1 onto the framing in
your prompt:

| Your words | SOTA name | Where it runs |
|---|---|---|
| "AI will guide/manage the robots" | **slow planner loop** (0.2–1 Hz) | LLM (Gemini 3 Flash / Claude / Qwen3-VL on GPU) |
| "vision data through advanced GPUs, verify each motion" | **verify gate** (per motion, 1–3 s) | VQA model on Salad/4090 or API |
| "in fail/stuck cases AI troubleshoots, understands, takes actions" | **failure-handling loop** (Behavior Tree + real-time VLM pre/post-condition checks) | LLM + deterministic BT |
| "constant flow code" | **fast servo loop** (100–1000 Hz) | **deterministic** controller — *no LLM in this loop* |

**The load-bearing rule, from every credible system (Figure Helix S1/S2/S0, the
ABB-YuMi 100%-insertion paper arXiv:2503.15202, DoReMi, REFLECT):** the LLM/VLA
**never closes the servo loop**. It proposes; a deterministic, bounds-checked,
safety-rated layer disposes. This is also what your existing code already does — the
encoder cross-check and envelope clamp are deterministic gates that override the
LLM verdict (`robot/control.go` execute pipeline). We keep that invariant and scale it.

---

## 1. What already exists (reuse map — do not rebuild)

Grounded in the current tree on `feat/yaver-robot-cell`:

| Asset | File | Reuse for Fairino |
|---|---|---|
| `Backend` interface | `desktop/agent/robot/backend.go:18-30` | implement `FairinoBackend` against it (+ extension iface §4) |
| Move-and-verify `Controller` | `desktop/agent/robot/control.go` (execute pipeline) | **unchanged** — orchestration is embodiment-agnostic |
| Vision verdict (LLM) | `desktop/agent/robot/verify.go` `VerifyMotion()` | reused; swap model/provider via `VisionConfig` |
| Camera capture + MJPEG | `desktop/agent/robot/camera.go` `GstCamera` | reused; arm gets eye-in-hand + fixed cell cam |
| Torque-gated screw loop | `desktop/agent/robot/screw.go` `DriveScrew()` | reused for the Ender screw axis; Fairino uses tool-DO + F/T (§5.4) |
| Companion torque MCU | `desktop/agent/robot/companion.go` | reused; Fairino can also read torque off the driver itself |
| Ops verbs (9, self-registering, mesh-routable) | `desktop/agent/ops_robot.go` | add Fairino verbs the same way (§9) |
| Standalone HTTP server | `desktop/agent/cmd/robotd/main.go` | gains a Fairino backend select branch |
| Mobile robot UI + mesh client | `mobile/app/(tabs)/robot.tsx`, `mobile/src/lib/robotClient.ts` | reused; add pose/force readouts |
| Wire-observe / deep analysis | `desktop/agent/netcapture/` (+ `ops_netcapture.go`) | **this is your protocol debugger** for the Fairino XML-RPC / 8083 / Modbus links (§7.4) |
| Modbus engine | `desktop/agent/machine/` | for Modbus-TCP screwdrivers/feeders/PLC IO around the cell |
| Talos robotics bridge | `talos/cloud/convex/robotics.ts`, `http.ts` `/robotics/*` | command queue + telemetry + audit plane (§8) |
| Talos machine knowledge | `machineRecipes`, `machineEdgeDevices/Commands/Telemetry` (Convex schema) | order→recipe→job resolution (§8) |

**Net new code is small:** one backend, one interface extension, a vision-pose
service, an AI-supervisor service, and a handful of ops verbs + Talos fields.

---

## 2. SOTA grounding → the chosen stack (and why)

Full survey lives in the research notes; the decisions:

1. **Do NOT use a VLA (π0 / GR00T / OpenVLA) to close the loop on precise
   screwdriving or insertion.** No open VLA is production-ready for sub-mm,
   torque-spec'd, contact-rich assembly as of 2026. They're great at coarse
   pick-place, weak at "the final 2 mm." Classical force control still wins the
   contact phase. We may *experiment* with GR00T N1.5-3B (runs on a 4090/L40,
   ~64 ms/chunk on L40) as a research lane, but it is **not** in the production path.

2. **Planner / supervisor = a frontier multimodal LLM with strict structured
   output.** Default **Gemini 3 / 3.5 Flash** (`response_schema`, <0.3% schema
   failure, lowest token overhead, doubles as the spatial-reasoning "Robotics-ER"
   planner) **or** OpenAI GPT-5 strict mode (≈100% schema adherence) when you want
   maximal rigidity. Claude Opus/Sonnet for the careful "explain the discrepancy"
   recovery reasoning. The model emits **bounded JSON motion intents**, never raw
   setpoints (§6).

3. **Verify gate = VQA, 1–3 s budget.** Two interchangeable providers behind the
   existing `VisionConfig` ladder:
   - **API:** Gemini 3 Flash — simplest, sub-2 s, no serving ops.
   - **On-prem / private images:** **Qwen3-VL-8B FP8** on a rented Salad/4090 or
     L40 (Apache-2.0, explicit 2D/3D grounding "for embodied AI"). This is the GPU
     path your prompt asks for. 32B on an A100 if you need finer judgement.
   The verdict is always **cross-checked against a deterministic signal** (encoder
   delta for the Ender, joint/TCP readback + driver torque for Fairino).

4. **Failure handling = Behavior Tree + real-time VLM pre/post-condition checks.**
   Copy the architecture from **arXiv:2503.15202** ("Real-Time Failure Handling
   using VLMs, Reactive Planner and Behavior Trees" — 100% on peg insertion on an
   ABB YuMi). The differentiator vs REFLECT/DoReMi is *real-time* verification
   (per-step pre/postcondition) instead of post-hoc. We already verify per-step;
   we add the BT recovery substrate (§7.3).

5. **6-DOF perception for insertion = FoundationPose** (NVlabs, novel-object 6D
   pose + tracking, no per-object fine-tune) + **Contact-GraspNet** (avoid
   AnyGrasp's commercial license unless you license it) + **hand-eye calibration**
   (`easy_handeye2` for ROS2, or our own). Runs on the GPU box (§5.3).

6. **Middleware: use, don't build.** ROS2 + MoveIt2 for free-space planning and
   collision checking around the Fairino; **BehaviorTree.ROS2** for execution +
   recovery. We do **not** rebuild motion planning. Yaver is the orchestration,
   mesh, vision-verify, AI-supervisor, and fleet plane on top — consistent with
   `[[project_yaver_is_orchestrator]]`.

7. **Compliance:** "most compliant" = the model **cannot emit a malformed or
   out-of-range command**. Strict JSON schema with hard numeric bounds (pos/vel/
   force/torque min-max, enum of allowed primitives) + a **deterministic re-validation**
   before any byte reaches the controller. The LLM is advisory; safety is hardwired
   (§6, §10).

---

## 3. Physical cell topology

```
                       ┌──────────────────────────────────────────────┐
                       │  EDGE BOX (Linux, NUC/Jetson Orin)            │
                       │  yaver serve --robot --machine --netcapture   │
                       │   ├─ robot.Controller (Ender backend)         │
                       │   ├─ robot.Controller (Fairino backend)       │
                       │   ├─ machine.Engine (Modbus IO/feeders)       │
                       │   ├─ netcapture (XML-RPC/8083/Modbus debug)   │
                       │   └─ mesh: relay + LAN + Convex heartbeat     │
                       └───┬───────────────┬──────────────┬───────────┘
            XML-RPC :20003⚠️│         USB serial          │ LAN
              + TCP :8083    │         (Marlin)            │
                ┌────────────▼──────┐   ┌─────────────────▼───┐   ┌──────────────┐
                │ FAIRINO FR5/FR3   │   │ ENDER-3 + screw axis│   │ GPU (Salad/   │
                │ 6-DOF cobot       │   │ Cartesian screwdr.  │   │ 4090 / L40)   │
                │  + tool-DO driver │   │  + companion torque │   │ Qwen3-VL FP8  │
                │  + wrist F/T (opt)│   │    MCU              │   │ FoundationPose│
                │  + eye-in-hand cam│   └─────────────────────┘   └──────────────┘
                └───────────────────┘
                       ▲ fixed cell camera(s) → GPU vision
                       │
              ┌────────┴─────────┐
              │ Mac / phone /web  │  control + observe over Yaver mesh
              │ (yaver, mobile,   │  (the "ultimate goal" surface §11)
              │  Talos dashboard) │
              └───────────────────┘
```

**Hardware picks (from research):**
- **Arm: Fairino FR5** (5 kg/7 kg payload, 922 mm reach, ±0.02 mm) for fixture reach
  + payload headroom for an electric screwdriver + vision bracket + feeder; **FR3**
  (3 kg, 622 mm) if the workpiece fits a smaller bubble and budget is tight.
  Repeatability is *not* the limiter — your hand-eye calibration is.
- **Screwdriver:** torque-controlled electric driver fired via the Fairino
  **tool-flange `SetToolDO()`**, "fastening complete" read via `GetToolDI()`; its own
  current/torque telemetry is the ground-truth seat signal.
- **Wrist F/T sensor (recommended for insertion):** enables admittance/spiral search;
  joint-torque collision detection alone is coarse.
- **Cameras:** eye-in-hand (on the FR flange) for approach/pose + ≥1 fixed cell cam
  for the verify gate's "global" view. 2D webcam is enough for the verdict gate; add
  a RealSense/Luxonis depth cam if you want FoundationPose 6-DOF.
- **GPU:** rented Salad/4090 (interruptible, fine for stateless VQA) or an L40/A100
  for FoundationPose + larger VQA. Keep the **servo loop off rented GPU** — Salad is
  not real-time-safe.

---

## 4. The `Backend` extension for 6-DOF + force + servo

The current `Backend` (`backend.go:18-30`) is Cartesian-XYZ + tool + e-stop. Fairino
needs pose, joints, force, fault model, and servo streaming. Add a **superset
interface**; the `Controller` uses the base for the proven verify loop and
type-asserts for the extras.

```go
// robot/backend.go (extension — additive, non-breaking)

// PoseBackend is implemented by arms (6-DOF). XYZ backends (Ender) don't.
type PoseBackend interface {
    Backend // Name/Status/Home/Jog/Move/Tool/WaitMoves/Position/EStop

    // Cartesian pose in the robot base frame (x,y,z mm + rx,ry,rz deg or quat).
    Pose(ctx context.Context) (Pose, error)
    MoveL(ctx context.Context, target Pose, vel, acc float64) error // linear, blocking-gated
    MoveJ(ctx context.Context, target Joints, vel, acc float64) error
    Joints(ctx context.Context) (Joints, error)

    // Tool IO on the flange (drives the screwdriver / gripper).
    ToolDO(ctx context.Context, pin int, on bool) error
    ToolDI(ctx context.Context, pin int) (bool, error)

    // Force/torque (wrist sensor or joint estimate). Nil sensor → ErrNoFT.
    Wrench(ctx context.Context) (Wrench, error)         // Fx..Fz, Tx..Tz
    ForceMove(ctx context.Context, dir Axis6, limit Wrench, maxDist float64) (Pose, error)

    // Fault model (Fairino XML-RPC int error codes + 8083 fault fields).
    Faults(ctx context.Context) ([]Fault, error)
    ClearFaults(ctx context.Context) error  // ResetAllError() + RobotEnable(1)
    DragTeach(ctx context.Context, on bool) error // hand-guiding capture
}

// ServoBackend: high-rate streaming for closed-loop vision/force correction.
// MUST hold the controller's cadence (Fairino ServoJ/ServoCart = 1–16 ms / 60–1000 Hz).
type ServoBackend interface {
    PoseBackend
    ServoBegin(ctx context.Context, mode ServoMode) (*ServoSession, error)
    // ServoSession.Push(target) called on a fixed clock; missing the window faults.
    // The deterministic fast loop owns this; the LLM never calls it directly.
}
```

`Pose`, `Joints`, `Wrench`, `Fault`, `Axis6`, `ServoMode` go in `robot/types.go`
next to the existing `Position`/`Verdict`/`Action` shapes (`types.go:10-46`). The
existing `MoveResponse` gains optional `Pose *Pose`, `Joints *Joints`,
`Wrench *Wrench`, `Faults []Fault` — additive, so the mobile/web/Talos paths keep
working and just render more when present.

**Why a superset, not a rewrite:** the proven `execute()` pipeline (before-frame →
motion → WaitMoves → fresh readback → after-frame → LLM verdict → deterministic
cross-check → vision gate → e-stop on obstruction) is the crown jewel. For Fairino,
"fresh readback" = `Pose()`/`Joints()` from the **8083 stream** instead of M114, and
"cross-check" compares commanded vs observed pose delta instead of XYZ encoder delta.
Same shape, new probe.

---

## 5. `FairinoBackend` — protocol details

A new file `desktop/agent/robot/fairino.go`. Two implementation routes; pick by env.

### 5.1 Route A — XML-RPC via a thin Python sidecar (fastest to ship)

Fairino's first-party SDK is Python (`from fairino import Robot; Robot.RPC('192.168.58.2')`),
C++, C#, Java — all the **same XML-RPC** call graph, int error codes. The Go agent
has no XML-RPC client and Fairino's wire protocol PDF is the only ground truth, so
the **lowest-risk first cut mirrors the proven `BridgeBackend` pattern**
(`backend.go:32-35`): a small Python `fairino_ui` sidecar (twin of the Ender
`ender_ui` on :8330) that wraps the SDK and exposes the same flat HTTP verbs
(`/api/movej`, `/api/movel`, `/api/pose`, `/api/tooldo`, `/api/wrench`,
`/api/clearfaults`, `/api/status`). `FairinoBackend` is then HTTP→sidecar, exactly
like `BridgeBackend` is HTTP→ender_ui. **Reuses a battle-tested pattern; isolates the
vendor SDK in Python where it's first-class.**

```
YAVER_FAIRINO_BRIDGE=http://127.0.0.1:8331   # python fairino_ui sidecar
YAVER_FAIRINO_IP=192.168.58.2                # controller (factory default)
```

### 5.2 Route B — native Go XML-RPC + 8083 (later, removes Python)

Once the protocol is confirmed against hardware (XML-RPC command port — ⚠️ expected
**20003** but VERIFY via the "Robot Controller Communication Command Protocol" PDF or
`ss -tlnp` on the controller; the SDK hides the port), implement XML-RPC in Go
(`net/rpc`-style over HTTP, or `kolo/xmlrpc`) and a TCP reader for the **8083 status
feedback stream** (joint pos, TCP pose, torques/currents, IO, mode, fault codes — push
channel; ⚠️ exact rate is in the "8083 Port Status Feedback Protocol" PDF, design as
configurable). This removes the Python dependency and gives the agent the real-time
state stream directly. Not needed for phases 1–4.

### 5.3 Vision-pose service (the GPU path)

A separate service `robot-vision` on the GPU box, reachable over the mesh:
- **VQA verify** (`/vision/verify`): image(s) + expectation → `{moved, seated,
  aligned, obstruction, confidence, reason}` JSON. Backed by Qwen3-VL-8B FP8 (vLLM)
  or proxied to Gemini Flash. This is what `robot.VerifyMotion` calls — just point
  `VisionConfig.BaseURL` at it (OpenAI-compatible).
- **6-DOF pose** (`/vision/pose`): RGB(D) + object id → `Pose` in camera frame →
  transformed through hand-eye calibration to base frame. FoundationPose (CAD or
  few-shot reference). Optional for phases ≤3; required for autonomous pick/insert.
- **Grasp** (`/vision/grasp`): Contact-GraspNet → 6-DOF grasp candidates.

Keep stateless and restartable (Salad-safe). The servo loop never depends on it.

### 5.4 Screwdriving & insertion on the arm

- **Approach (free space):** MoveJ/MoveL to a pre-insert pose (planned by MoveIt2 or
  taught), gated by the verify loop (camera confirms target visible/clear).
- **Insertion (contact):** `ForceMove` / admittance — spiral or wiggle search under a
  **force limit**, terminate on seat. F/T + vision fusion is the standard; vision gets
  you to the hole, force closes it (research: 96.7% on 0.1–1.0 mm clearances).
- **Screw seating:** fire driver via `ToolDO`, monitor driver torque/current +
  `ToolDI` "complete"; **torque-spec termination** is the deterministic gate, VQA
  ("is the head flush, not cam-stripped?") is the independent cross-check — exactly the
  dual-signal pattern in `screw.go` `DriveScrew()`.

---

## 6. Compliance: how the AI emits motion safely

The supervisor LLM emits a **bounded motion intent**, validated twice (model schema +
deterministic guard), then lowered to backend calls. Never raw setpoints.

```jsonc
// strict response_schema / tool schema for the planner
{
  "primitive": "move_l | move_j | screw | insert | tool | home | verify | abort",
  "pose":   { "x":  {min:-700,max:700}, "y": {...}, "z": {...},
              "rx": {min:-180,max:180}, "ry": {...}, "rz": {...} },  // bounds = workspace
  "vel":    { "min": 1, "max": 250 },     // mm/s, hard-capped
  "force_limit": { "min": 1, "max": 40 }, // N, for insert
  "torque_target_nmm": { "min": 50, "max": 2000 },
  "expectation": "free text for the verify gate",
  "reason": "why the planner chose this"
}
```

Pipeline (mirrors and extends the existing deterministic gates):
1. **Model strict mode** (Gemini `response_schema` / OpenAI strict / Claude tool):
   guarantees well-formed JSON in-bounds *as the model sees the bounds*.
2. **Deterministic re-validation in Go** before any controller byte:
   `checkEnvelope` (extended to 6-DOF workspace + joint limits), velocity/force/torque
   caps, homed/enabled precondition, e-stop-latch check. Out-of-bounds → refuse (never
   clamp-and-move — same rule as `control.go`).
3. **Lower to backend** (`MoveL`/`ForceMove`/`ToolDO`).
4. **Verify** (the existing `execute()` loop) — vision verdict + pose cross-check.
5. **Gate**: obstruction or cross-check disagreement → e-stop latch.

**Safety is never in the model.** Hardwired E-stop, the cobot's ISO/TS-15066-style
force/speed limits (configured in the Fairino WebApp Safety section), and the agent's
deterministic guards are the real safety layer. The LLM is advisory. ⚠️ Confirm
ISO 10218 / TS 15066 conformance with Fairino's Declaration of Conformity if this is a
deployment gate — features are present, formal cert was unconfirmed in research.

---

## 7. The AI supervisor & troubleshooting loop ("constant flow + fix when stuck")

Three nested loops on the edge box, talking to the GPU/API model:

### 7.1 Fast loop (100–1000 Hz, deterministic, no LLM)
Servo / force control during contact (`ServoBackend`, Fairino ServoJ/ServoCart cadence
1–16 ms). Owns the actual motion. Hardware safety underneath it.

### 7.2 Mid loop (per motion, 1–3 s, verify gate)
The existing `Controller.execute()` after every primitive: before/after frames →
VQA verdict → deterministic readback cross-check → e-stop on obstruction/disagreement.
Already built; Fairino just changes the readback probe (§4).

### 7.3 Slow loop (0.2–1 Hz, planner + failure handling)
A new `robot-supervisor` service (Go) running a **Behavior Tree** (BehaviorTree.ROS2
or a lightweight Go BT) where leaf nodes are `robot_*` ops verbs. The LLM is consulted
at three points (the arXiv:2503.15202 pattern):
- **Pre-condition check** (before a step): VLM confirms the world matches the plan's
  assumption ("tray present, slot empty, part in gripper"). Fail → replan, don't move.
- **Post-condition check** (after a step): the verify gate (§7.2) *is* this.
- **Failure recovery** (on stuck/fail): feed the LLM the **rich context Yaver already
  collects** — before/after frames, pose/joint trace, wrench trace, Fairino fault
  codes (`Faults()`), and the **netcapture diagnosis** of the XML-RPC/8083/Modbus
  links (§7.4). The LLM returns a structured recovery action (retry with offset, back
  off + re-approach, clear-fault + re-enable, request human, abort). Bounded + validated
  like any motion (§6).

Recovery primitives the LLM can pick from (all deterministic underneath):
`clear_faults` (`ResetAllError` + `RobotEnable(1)`), `back_off` (force-controlled
retreat), `re_home`, `re_approach_with_offset`, `regrasp`, `drag_teach` (ask human to
hand-guide a waypoint), `escalate` (notify via mesh/phone), `abort` (e-stop latch).

### 7.4 Why netcapture is your secret weapon for troubleshooting
When the arm goes silent or faults weirdly, the failure is often *on the wire*, not in
the plan: XML-RPC timeouts, 8083 stream stalls, Modbus exceptions from a feeder, a
USB-serial DTR reset on the Ender (your known gotcha). `netcapture`
(`desktop/agent/netcapture/`) already decodes Modbus (TCP+RTU), serial/Marlin, HTTP,
and does deterministic findings + LLM narrative (`netcapture_analyze`). Point it at the
Fairino links (add a thin XML-RPC + 8083 decoder alongside the existing ones) and the
supervisor's recovery prompt gets a *root-cause wire diagnosis* for free — e.g. "8083
stream stopped 4 s ago, last fault code 0x… , controller likely in protective stop;
recovery = clear_faults then re_approach."

---

## 8. Talos integration (order → recipe → job → cell → audit)

Talos already has the exact bridge pattern (`talos/cloud/convex/robotics.ts`,
`http.ts` `/robotics/*`): org-secret heartbeat, command queue
(`roboticsCommands` with `jog|move|home|screw|run_board|insert_pole|drive_pole|
estop|...`), device snapshot (`roboticsDevices`), and session-auth mobile enqueue.
The Fairino cell plugs in **without inventing a new control plane**:

- **Register** the cell as a `roboticsDevices` row (`capabilities: ["arm6dof","screw",
  "insert","vision","force"]`); snapshot carries `{armPose, joints, wrench, toolState,
  faults, lastVerdict}`. Extend `roboticsCommands.type` with `move_l|move_j|insert|
  force_move|clear_faults` (or keep generic and nest in `args`).
- **Yaver mesh = live control plane** (low-latency jog/verify over QUIC LAN/relay,
  `proxyToDevice` → `robot_*` verbs). **Convex = record/audit plane** (telemetry,
  frames, command history, OEE/downtime). This split is already how the machine-edge
  side works and matches `[[feedback_p2p_only]]` (data flows P2P; Convex is bookkeeping).
- **Order→instruction** (Talos machine-knowledge plane): a kesim/BOM row →
  `machineRecipes` resolve (`talos_machine_recipe_resolve`) → returns
  `{poses, torqueSpec, insertForce, screwCount, partGeometry}` → supervisor builds the
  BT → executes on the cell → posts verdicts + telemetry back. This is the
  manufacturing-on-demand flywheel from `[[project_mfg_on_demand]]`: the cell becomes a
  Talos execution node whose **machine knowledge** (calibrated poses, torque specs,
  recovery playbooks) accrues as a moat.
- **New Convex tables** (design): `robotCameraFrames` (`{orgId, deviceId, ts, stepId,
  storageId, verdict, kind}`) for the audit trail of every verify-gate frame; reuse
  `machineRecipes`/`machineManuals` for calibrated pose + recovery-playbook storage.
- **Privacy:** per CLAUDE.md, frames/poses/wrench traces are **work-derived** → P2P /
  on-device / org-secret HTTP only, **never** Convex beyond storageId pointers +
  summaries. Add the new fields to `convex_privacy_test.go`'s forbidden-payload set.

---

## 9. New ops verbs & API surface (self-registering, mesh-routable)

Same pattern as `ops_robot.go` (init() → `registerOpsVerb`, `AllowGuest:false`,
`proxyToDevice` does the mesh). Additive — no edits to `ops.go`/`httpserver.go`.

| Verb | Payload | Notes |
|---|---|---|
| `robot_pose` | `{}` | TCP pose + joints + wrench snapshot (no motion) |
| `robot_move_l` | `{pose, vel, acc, verify?, expectation?}` | linear; bounds-validated; verify-gated |
| `robot_move_j` | `{joints\|pose, vel, acc, verify?}` | joint move |
| `robot_insert` | `{approach, target, forceLimit, maxDist, verify?}` | admittance/spiral search + seat |
| `robot_screw` | `{pose, torqueTargetNmm, dwellMs, verify?}` | tool-DO fire + torque-gate + VQA cross-check |
| `robot_force_move` | `{dir, limit, maxDist}` | guarded compliant move (probe/back-off) |
| `robot_clear_faults` | `{}` | `ResetAllError` + `RobotEnable(1)` |
| `robot_drag_teach` | `{on}` | hand-guiding capture |
| `robot_run_job` | `{job, verifyEachStep, dryRun?}` | the BT/pole sequence (the doc-only gap from robot-screwdriver-cell.md §3.4 — finally built here) |
| `robot_recover` | `{context}` | invoke the supervisor recovery loop on demand |

Mobile (`robotClient.ts`) and Talos MCP (`talos_robot_*`) get matching wrappers;
the existing jog/verify/estop UI extends with pose/force/fault readouts.

---

## 10. Safety model (defense in depth)

Layered, outermost is hardware, innermost is advisory:
1. **Hardwired E-stop** (control box + pendant) — cuts power, model can't override.
2. **Cobot safety config** (Fairino WebApp): TCP force/speed/power limits, safety
   planes/zones, collision level 1–10, protective stop. ISO/TS-15066-style PFL.
3. **Agent deterministic guards** (Go): 6-DOF envelope + joint limits, vel/force/torque
   caps, homed/enabled precondition, **e-stop latch** (once tripped, all motion fails
   until explicit reset — existing `control.go` invariant), watchdog on WaitMoves /
   8083-stall, tool-off on e-stop/disconnect.
4. **Deterministic cross-check** overrides the LLM verdict (pose/encoder delta;
   driver torque vs spec).
5. **LLM verify gate** (advisory): obstruction/misalignment → request e-stop.
6. **Human-in-the-loop**: high-risk recipe params require approval
   (`roboticsCommands.requiresApproval`, already in the Talos schema); supervisor can
   `escalate` to phone.

Rule, restated: **the model proposes, a deterministic safety-rated layer disposes.**

---

## 11. The ultimate goal: phone-as-edge, vision through GPUs, fleet on the floor

Your end state — "cartesian/cobot arms on the production floor, phones running Yaver,
vision through advanced GPUs, all ops AI-driven" — is the natural extension:

- **Phone as the cell's edge host + camera** (`docs/yaver-robotics-edge-vibing.md`):
  an old phone running the Yaver mobile container is the cell camera *and* the
  USB-OTG serial host (Termux `OpenMarlinFD` path for the Ender; the Fairino arm is
  LAN/XML-RPC so the phone just needs network). Phone grabs frames → mesh → GPU VQA →
  verdict → supervisor → motion. No PC per cell.
- **GPU pool** (Salad/4090/L40) shared across cells for VQA + FoundationPose; ties into
  `[[project_gpu_rental_orchestration]]` (the GPU autoscaler already shipped — point a
  rental pool at the `robot-vision` service).
- **Fleet** (`docs/yaver-robot-fleet-mesh-design.md`): every cell is a `deviceId` with
  `capabilities ∋ robot/arm6dof`; the mobile/web picker filters by capability;
  `callOps(verb, payload, machine=<cellId>)` routes anywhere. One operator (or one
  Claude Code session) supervises N cells from a phone.
- **Talos** is the order/knowledge/OEE plane; **Yaver** is the execution/mesh/AI plane.
  Together: agent-native manufacturing-on-demand (`[[project_mfg_on_demand]]`).

---

## 12. Roadmap — easy → hard, each phase shippable & dogfoodable

**Phase 0 — Sim, zero hardware (do this before the arm arrives).**
- RoboDK on the Mac (first-class Fairino FR3/FR5) for IK/path prototyping; *or*
  ROS2 + MoveIt2 + `fairino_description` URDF in RViz2.
- Stand up the Python `fairino_ui` sidecar against the **FAIRINO WebApp/SimMachine
  virtual controller** at `192.168.58.2` (the SDK talks to *an IP* — virtual is a
  drop-in). Prove `MoveJ/MoveL/Pose/ToolDO/ClearFaults` over HTTP. ⚠️ note the open
  SimMachine↔ros2 bug (frcobot_ros2 #12) — sidecar path avoids it.
- Build `FairinoBackend` (Route A) + the `PoseBackend` interface + `robot_pose`/
  `robot_move_l`/`robot_move_j` verbs. Drive the sim from the existing mobile UI.

**Phase 1 — First real motion, verify-gated (arm on bench, no contact).**
- Real FR5 on LAN. Confirm ⚠️ XML-RPC port (20003?) and 8083 rate via the protocol
  PDFs / `ss`/netcapture. Hand-eye calibration (eye-in-hand).
- Free-space MoveL with the **existing verify gate** (camera + VQA). Pose cross-check
  replaces encoder cross-check. E-stop + clear-fault recovery proven. This is the
  Ender "home G28 + vision verdict" milestone, re-proven on the arm.

**Phase 2 — Screwdriving (deterministic torque + VQA cross-check).**
- Tool-DO driver fire, torque-spec termination, `GetToolDI` complete signal, VQA
  "head flush?" cross-check. Reuse `screw.go` logic shape. `robot_screw` verb.

**Phase 3 — Insertion (force control).**
- Wrist F/T + admittance/spiral search, vision-to-hole + force-to-seat fusion.
  `robot_insert`/`robot_force_move`. This is the first genuinely hard step — budget
  iteration time; F/T tuning dominates.

**Phase 4 — AI supervisor & autonomous job runs.**
- `robot_run_job` (BT over the pole/step sequence), per-step pre/postcondition VLM
  checks, structured recovery loop (`robot_recover`) wired to netcapture diagnosis +
  fault codes. "Constant flow, AI fixes stuck cases" goes live.

**Phase 5 — Talos + fleet + phone-edge + GPU pool.**
- Register cell in Talos robotics bridge; order→recipe→job; `robotCameraFrames` audit;
  multi-cell fleet picker; phone-as-edge camera; GPU rental pool behind `robot-vision`.

**Phase 6 (research lane, parallel, non-blocking).**
- GR00T N1.5-3B / π0 on the GPU box for coarse autonomous pick-place; never on the
  precision/contact path. Compare against the classical stack; promote only if it
  beats it on a real task.

---

## 13. First-contact verification checklist (the ⚠️ items)

Pull from `frtech.fr/DOWNLOAD2` and confirm on the live controller before timing work:
1. **XML-RPC command port** — expected 20003; confirm via "Robot Controller
   Communication Command Protocol" PDF or `ss -tlnp` on the controller.
2. **8083 push cycle rate** — port confirmed by name; rate is in the "8083 Port Status
   Feedback Protocol" PDF. Design the reader configurable.
3. **ServoJ/ServoCart cadence** — confirmed 1–16 ms (60–1000 Hz) hard window; the fast
   loop must never miss it or the controller faults.
4. **SDK↔firmware pairing** — match `fairino-python-sdk` release to controller fw
   (v3.9.6 current); mismatch throws XML-RPC faults.
5. **ISO 10218 / TS 15066 conformance** — request the Declaration of Conformity if
   safety certification is a deployment gate.
6. **Tool-flange IO map** — which DO pin fires the driver, which DI is "complete",
   24V/RS-485 availability.

---

## 14. Risks & open questions

- **Insertion precision is the real hard part**, not the arm or the AI plumbing.
  Force/torque tuning + hand-eye calibration accuracy dominate. De-risk with the F/T
  sensor and a fixtured first part.
- **Rented GPU is not real-time-safe** — keep it strictly to stateless VQA/pose; servo
  + safety stay on local/edge hardware.
- **Vendor doc drift (CN vs EN)** — the protocol PDFs are ground truth; the HTML manual
  lags. Verify the ⚠️ list.
- **Fairino force-control fidelity** without a wrist F/T sensor is coarse (joint-torque
  estimate) — budget for the sensor if insertion clearances are tight.
- **Two embodiments, one cell** — coordinate Ender + Fairino motion (interlocks so they
  don't collide in shared space). Model as BT guards + shared workspace zones.
- **Open VLAs are not production-ready** for this — resist the temptation to put π0/
  GR00T in the contact loop; keep them in the Phase-6 research lane.

---

## 15. TL;DR

Fairino is a new `robot.Backend` (a `PoseBackend`/`ServoBackend` superset). Everything
else — the move-and-verify Controller, vision gate, deterministic cross-check, e-stop
latch, mesh ops verbs, mobile UI, Talos bridge, netcapture wire-debug — is already
built and embodiment-agnostic. The AI runs as three loops: **deterministic fast servo
(no LLM)**, **per-motion VQA verify gate (Qwen3-VL on GPU or Gemini Flash)**, and a
**slow Behavior-Tree planner/recovery supervisor** that troubleshoots stuck cases using
frames + pose/wrench/fault traces + netcapture diagnosis. Motion intents are
strict-JSON, numerically bounded, re-validated deterministically; the model is advisory,
safety is hardwired. Ship sim-first (RoboDK / WebApp virtual controller), then
bench-motion, screw, insert, supervisor, then Talos+fleet+phone-edge+GPU pool. Don't put
open VLAs in the precision loop.
