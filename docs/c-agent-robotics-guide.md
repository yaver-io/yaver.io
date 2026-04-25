# Video + LLM + Yaver — Iteration & Learning Guide

A practical guide for building an iterative-learning workflow that
takes human video demonstrations, an LLM, and Yaver's c-agent
runtime, and produces deployable robot policies. Worked example:
**wire-harness terminal-block insertion on a Parol 6** open-source
desktop arm.

This is the application-side companion to
[`c-agent-architecture.md`](./c-agent-architecture.md) and
[`c-agent-vendor-modules.md`](./c-agent-vendor-modules.md). Read
those first if you haven't — the iterative module-loading model is
what makes this workflow possible.

## Status — read first

> Research-grade workflow. The c-agent runtime + module ABI are
> stable enough to integrate against today; everything else
> (vision pipelines, ML training, sim-to-real, force-feedback
> tuning) is well-trodden but not turnkey. Expect to assemble
> several open-source pieces, write integration glue, and spend
> calendar weeks on the first end-to-end loop.
>
> Bench setup only. Not for unsupervised production until physical
> safety (light curtains, e-stop, fenced workcell, force-limited
> arm) and engineering review of policies are in place.

## The complete loop in one diagram

```
                        ┌──────────────────────┐
                        │  Operator (mobile)    │
                        │  - records demo video │
                        │  - approves trials    │
                        │  - tags outcomes      │
                        └─────────┬────────────┘
                                  │
                                  ▼
        ┌──────────────────────── Cloud Brain ──────────────────────────┐
        │                                                                │
        │  1. Demo ingest  ──►  vision encoder (ViT/CLIP/DINO)           │
        │                       extract trajectory + grasp + insertion   │
        │                                                                │
        │  2. LLM coordinator  ──► reads demo embeddings + library       │
        │                          + prior trial outcomes                │
        │                          decides: which policy to ship next?   │
        │                                                                │
        │  3. Policy generation  ──► train ACT / diffusion / behavior    │
        │                            cloning, OR retrieve from library,  │
        │                            OR LLM-author a script policy       │
        │                                                                │
        │  4. Compile + sign  ──► policy → c-agent module                │
        │                         (rust→wasm, or rust→native, or wasm    │
        │                         calling vendored ONNX runtime)         │
        │                                                                │
        │  5. Ship  ──► INVOKE { module_hash, "trial" }                  │
        │                                                                │
        │  9. Observe  ──► trial telemetry streams back                  │
        │                  vision frames + joint angles + F/T sensor    │
        │                  outcome: succeeded / partial / failed         │
        │                                                                │
        │  10. Refine  ──► retrain / tweak / replace, loop back to 4    │
        └────────────────────────────┬───────────────────────────────────┘
                                     │
                                     ▼
            ┌──────────── c-agent on Robot Linux Host ──────────────┐
            │                                                        │
            │   6. Verify signed module against pinned root          │
            │                                                        │
            │   7. Bind capabilities:                                │
            │        arm.move_joint, arm.set_pose,                   │
            │        gripper.actuate, gripper.read_force,            │
            │        vision.read_frame, vision.detect_aruco,         │
            │        terminal_block.locate_hole, force.threshold     │
            │                                                        │
            │   8. Safety gate (pre-validates):                      │
            │        - workspace bounds                              │
            │        - joint velocity / acceleration limits          │
            │        - max torque envelope                           │
            │        - force.threshold for insertion                 │
            │                                                        │
            │   Run the policy in wasm sandbox                       │
            │   Stream telemetry back via STREAM_CHUNK frames        │
            └──────────────────────────┬─────────────────────────────┘
                                       │
                                       ▼
            ┌──────── Real-time controller (separate, not c-agent) ───────┐
            │  ros2_control / custom CAN driver / Klipper-style stepper   │
            │  1 kHz+ servo loop, hardware safety interlocks              │
            │  Hardware: Parol 6 + camera + F/T sensor + gripper          │
            └──────────────────────────────────────────────────────────────┘
```

## What each piece does, concretely

### 1. Operator — phone records demo

The operator (engineer, technician, or even end-customer) records
video on their phone:

- Holds wire end above the terminal block.
- Approaches insertion hole.
- Makes the insertion.
- Releases.

Records 10–50 demonstrations per task. Phone-side, the existing
Yaver mobile app can grow a "Demo Capture" surface that:

- Records video at 30 fps.
- Captures phone IMU (operator's hand pose).
- Tags each demo with task name + outcome (success / failure).
- Uploads to brain over the existing relay/auth path.

For v1, even simpler: operator points the *robot's* wrist camera
at their hand, runs the task with the robot in passive lead-through
mode (Parol supports this via stepper drivers + zero current).
That gives you joint trajectories AND vision in one capture, no
phone needed.

### 2. Cloud brain — vision encoder + LLM coordinator

**Vision encoder**: pretrained model that turns raw RGB frames into
embeddings. Open-source options:

- **DINOv2** (Meta) — strong general-purpose features, small.
- **CLIP** (OpenAI) — vision + text alignment, good for "find the
  thing labeled X" tasks.
- **SAM 2** (Meta) — segmentation masks, good for "where is the
  wire end."
- **OWL-ViT** — open-vocabulary detection, "find a green M3 wire."

For wire-harness insertion specifically, a custom-trained
fine-tune is overkill at v1. Use DINOv2 features + classical CV
(ArUco markers on the terminal block, template matching for hole
locations) for a working v1 in ~2 weeks.

**LLM coordinator**: the brain's reasoning layer. Uses retrieval
over:

- The demo library for this task.
- Prior trial outcomes (what worked, what failed).
- Library policies that succeeded on similar tasks.

Every iteration the LLM decides: "given the failures so far, do I
(a) retrain with more demos, (b) tweak a hyperparameter on the
existing policy, (c) author a new script-based policy entirely?"

For wire-harness insertion the LLM's first decision is usually:
"this looks like grasp-approach-insert with three sub-policies;
load the existing terminal-block-grasp policy from library, train
a new approach policy from these demos, keep the existing
insertion policy because it succeeded on the last 5 similar
terminal-block types."

### 3. Policy generation

Three regimes, picked by the coordinator:

**Regime A — script policy (fastest, weakest)**

The LLM authors a deterministic script: "approach the hole at this
offset, descend at 5 mm/s while monitoring force, retract on
threshold." Compiled directly to a c-agent module. Good for
solidly-known geometry (when the terminal block is identical to a
prior one) and as the **iteration-zero baseline** before any
demo-trained policy exists.

**Regime B — behavior cloning (balanced)**

Train a small MLP / Transformer on the demo trajectories. Open-
source baselines:

- **LeRobot** (HuggingFace) — `pi0`, ACT, diffusion policy.
  Production-ready training pipelines, exports to ONNX.
- **Octo** (Stanford) — generalist policy for arm manipulation.
- **ACT** (Stanford) — Action Chunking Transformer, the ALOHA
  paper's policy. Battle-tested for fine-manipulation tasks.

ACT or LeRobot's diffusion policy is the sweet spot for
wire-harness work: handles 0.5–2 mm precision with force feedback,
needs ~50 demos per task variant.

**Regime C — VLA (vision-language-action) policy (research-grade)**

Open-source: **OpenVLA**, **π0** (Physical Intelligence), **Octo**.
These take video + language ("insert the green wire into hole 3")
and emit action sequences. State-of-the-art but expensive (multi-
GPU training, 7B+ parameters), and on-device inference needs a
Jetson Orin or larger.

For v1, skip Regime C. The script + behavior-cloning combo gets
you 80% of the value in 20% of the effort.

### 4. Compile + sign as a c-agent module

The policy gets compiled into a c-agent module artifact:

- **For script policies (Regime A)**: brain authors Rust source,
  compiles to `wasm32-wasi`, signs, ships. ~10 KB module.
- **For behavior-cloning (Regime B)**: train ONNX; package the
  ONNX file + a small Rust runner that loads it via wasi-nn. The
  device-side wasm calls into ONNX runtime. ~5–50 MB module.
- **For VLA (Regime C)**: too big for wasm; ship as a native .so
  to a Jetson-class on-arm compute board, OR keep inference in
  the brain and stream commands over the c-agent's TCP transport
  (slower per-step but no on-device GPU).

Either way, the artifact is a signed module the brain hands to the
device's c-agent via the standard INVOKE/MODULE flow.

### 5–7. Ship + verify + bind capabilities

Same flow as any c-agent module:

- Brain sends `INVOKE { tool_hash, args }`.
- Device looks up module in cache; on miss, asks for it via NEED.
- Brain ships the signed artifact via MODULE.
- Device verifies signature against pinned root.
- Device binds the capability list declared in the descriptor.
- Device instantiates the wasm runtime and runs.

For robotics, the capability allowlist becomes the safety boundary
(see §8 below).

### 8. Safety gate — the critical piece

The c-agent capability binding + the safety gate is what turns "AI
runs arbitrary code on a 6-axis arm" from terrifying to acceptable.

Suggested capability families to expose to a wire-harness policy:

```
arm.read_pose          - read current TCP pose (read-only)
arm.read_joints        - read joint angles (read-only)
arm.move_pose          - move to a target pose; gated to
                          workspace bounds + max velocity
arm.move_joints        - direct joint move; gated to joint
                          velocity + acceleration limits
arm.read_torque        - read current joint torques (read-only)
gripper.read           - read gripper state + force (read-only)
gripper.set_position   - set gripper position; gated to max force
gripper.set_force      - direct force command; gated to max
                          force envelope
vision.read_frame      - read camera frame (read-only)
vision.detect_aruco    - run ArUco detection on a frame
vision.template_match  - run template matching
trial.log              - emit a structured event into the
                          telemetry stream (read-only side-effect)
```

The safety gate runs INSIDE the c-agent host runtime, between the
capability call and the underlying ROS / driver. It enforces:

- Workspace bounds: every commanded pose is clipped (or rejected)
  to a configured AABB.
- Joint velocity: a moving-window check on `arm.move_joints` —
  reject if velocity exceeds 30°/s on any joint.
- Force envelope: `gripper.set_force` clamped to 0–20 N for thin
  wires.
- Insertion-specific: `arm.move_pose` with z-descent + force
  > threshold = abort and retract.

A buggy policy that asks for `arm.move_pose([5m, 5m, 5m])`
encounters a host-side trap before any command reaches the driver.
The arm stays put.

Crucially, **the safety gate is the only thing on the trust path
that ISN'T AI-authored.** It lives in Layer 0 of the c-agent
firmware-time code, hand-audited, never shipped from the brain.

### 9. Trial execution + telemetry

The policy module runs synchronously inside wasm. Its `invoke()`
dispatches to the host capabilities; each capability call streams
back via STREAM_CHUNK frames:

```
{ "ts_ms": 12345, "kind": "joint_state", "joints": [0.1, 0.2, ...] }
{ "ts_ms": 12347, "kind": "wrist_force", "fxyz": [0.1, 0.2, 5.3] }
{ "ts_ms": 12350, "kind": "vision_frame", "frame_id": 42 }
{ "ts_ms": 13000, "kind": "outcome", "value": "inserted" }
```

The brain receives the stream live and the operator's mobile app
can render real-time:

- Live wrist force trace.
- Joint trajectory.
- Camera frame (sampled at 5 Hz).
- Outcome label.

### 10. Observe + refine

After each trial, the brain has:

- The signed policy module hash that was tried.
- The full telemetry stream.
- An outcome label (success / partial / failure / near-miss).

Together with the demo embeddings + library, the brain decides the
next iteration:

- All-success: graduate the policy to the library.
- Mixed: retrain with the failed cases as additional demos
  (online learning).
- All-failure: pull a different policy variant; or ask the
  operator to record more demos.

This is the loop that makes c-agent's iterative model match
real-world robotics workflows. Every trial is an immutable signed
artifact pair (input policy + output telemetry) — that's the
audit trail + the dataset for the next round of training.

## Concrete steps to start — wire-harness insertion on Parol 6

Realistic 4-week first end-to-end run.

### Week 1 — Hardware + bring-up

Bill of materials:

- **Parol 6** arm (~$2 000) — Source Robotics' open desktop arm.
- **Wrist camera** — ESP32-CAM or RPi Camera V3 + small mount on
  the gripper (~$30).
- **Force sensor** — ATI Mini40 is overkill ($1 500); for v1 use
  the existing Parol stepper-current readings as proxy.
- **Reference terminal block** — pick a common one, e.g. Phoenix
  Contact PT 2.5 (~$5).
- **20–50 wires** with crimped terminals (~$30).
- **Compute**: a Linux desktop or NUC running c-agent + the Parol
  driver. Hetzner box for the brain.
- **Lighting + workcell** — fenced area, e-stop within reach.

Setup tasks:

- [ ] Install Parol's Python control library on the Linux host.
- [ ] Build the c-agent for the host (already cross-compiles
      cleanly on Linux, see embedded/c-agent/build.sh).
- [ ] Wire up a c-agent capability layer around Parol's Python
      API: `arm.read_pose`, `arm.move_pose`, `gripper.actuate`.
- [ ] Cross-check: from a Hetzner brain process, INVOKE a
      no-op probe on the c-agent and see it return.

### Week 2 — Demo capture + script-policy v1 (Regime A)

Record demos:

- [ ] Hand-guide the Parol through 10 wire-insertion attempts.
      Use Parol's lead-through mode (zero stepper current).
      Record joint trajectory + wrist camera at 30 fps.
- [ ] Process: extract approach pose, insertion start pose,
      retract pose. Save as a JSON trajectory.

Author a script policy:

- [ ] LLM (or hand-written) Rust code that:
      1. Read terminal-block pose via ArUco marker on its frame.
      2. Approach: move to (block_pose + offset).
      3. Descend at 5 mm/s while monitoring stepper current.
      4. On current spike, abort and retract.
      5. Otherwise: continue 8 mm of descent → release gripper.
- [ ] Compile to wasm32-wasi.
- [ ] Sign with a dev key (real key infrastructure later).
- [ ] Ship to c-agent on the host.
- [ ] Trial 10 times. Log outcome.

### Week 3 — Behavior cloning v2 (Regime B)

- [ ] Take the 10 demo trajectories from week 2 + 30 more new
      ones.
- [ ] Train an ACT or LeRobot diffusion policy on a single GPU.
      ~2–6 hours wall-clock for first-cut quality.
- [ ] Export as ONNX.
- [ ] Package as a wasm module that calls into wasi-nn / ONNX
      runtime.
- [ ] Sign + ship.
- [ ] Trial 50 times. Compare success rate to script policy.

### Week 4 — Iteration loop

- [ ] Brain-side: integrate an LLM coordinator that, given
      trial outcomes, decides "more demos / retrain / tweak
      hyperparameters / try different policy class."
- [ ] Mobile app: add a "Train this Robot" surface — operator
      records a demo, hits "deploy + trial," watches outcome,
      thumbs-up / thumbs-down.
- [ ] Run the full loop on a fresh terminal-block variant.
      Goal: <2 hours from "first demo" to "90%+ success rate."

## What to use today vs. what to defer

### Use today (already production-ready open source)

- **Parol 6** arm + Python control library.
- **DINOv2 / CLIP / SAM 2** for vision encoding.
- **ArUco / OpenCV** for fiducial-based positioning.
- **ROS 2** (optional, only if Parol's native API isn't enough).
- **LeRobot** for behavior cloning.
- **ONNX Runtime** for on-device inference.
- **Yaver c-agent** (this repo, today).

### Defer

- **VLA models** (OpenVLA, π0) — research-grade, need GPU on
  device or in brain at inference time. Wait until v1 demos
  prove the loop works.
- **Sim-to-real** (Isaac Sim, MuJoCo) — adds calendar weeks,
  not needed for the first 100 trials on real hardware.
- **Production safety certification** — wire-harness assembly
  isn't safety-critical at the per-station level, but if you
  scale to a line, you'll need ISO 10218 + IEC 61496 review.
- **Multi-task generalist policies** — start narrow ("insert
  M3 wire into PT 2.5 block"), generalize later.

## Honest limitations

- **Parol 6's repeatability is 0.1–0.3 mm** spec'd; in practice
  closer to 0.5 mm under payload. For PT 2.5 terminal blocks (3
  mm hole pitch) that's fine. For 1.27 mm-pitch JST connectors,
  it's marginal — you'd need force-feedback compliance to make
  it work. For 0.5 mm board-edge connectors, get a different
  arm.
- **Parol's max payload is ~1 kg**. Wire-harness work fits;
  larger industrial connectors with 50+ wires won't.
- **The vision pipeline at 30 fps + force at 1 kHz + policy at
  10 Hz is ~5 GFLOPs/s.** Fits on a Jetson Orin Nano (~$500),
  not on a Raspberry Pi 5 alone.
- **Real-time control still lives outside c-agent.** The c-agent
  emits trajectory waypoints at 10–50 Hz; the underlying Parol
  controller (or ros2_control) does the 1 kHz servo loop. If you
  need direct torque control at 1 kHz, c-agent isn't the layer
  for that — your policy module emits target torques and the RT
  layer applies them.
- **No formal safety guarantees from the WASM sandbox.** The
  sandbox limits memory + CPU + capability access. It does not
  guarantee that a malicious or buggy policy can't combine
  permitted capabilities into something harmful. Physical safety
  (light curtains, force-limited arm, fenced cell) remains
  necessary.

## What this enables

Once the loop works on Parol + wire-harness, the same pipeline
applies to:

- **PCB component placement** (different arm, same loop).
- **Electrical-panel cable management** (same task family).
- **Fabric handling / textile work** (different tooling, same
  policy class).
- **Lab automation** (pipetting, sample handling).
- **Light assembly tasks** in small-batch manufacturing.

The defensible position isn't "we built a better policy" — it's
"we built the deployment substrate for whoever's building
policies, with first-class safety + replay + audit + iteration."
Every robotics lab has the policy-training pipeline; very few
have a deployment substrate that handles the boring but critical
production-engineering parts (signing, capability gating, audit,
hot-swap, rollback). Owning that substrate is a real wedge.

## Files that need to be added to this repo

For the wire-harness Parol demo, the missing pieces are:

```
embedded/c-agent/
├── transports/include/yvr/ros.h        ROS 2 capability bridge (optional)
├── probes/src/parol_arm.c              Parol API → capability layer
├── probes/src/vision_aruco.c           OpenCV ArUco detection
├── probes/src/safety_gate.c            workspace + velocity + force gate
├── examples/parol-wire-harness/        full vendor host example
└── docs/c-agent-robotics-guide.md      this document

cloud/iot-brain/
├── ingest/                              video + telemetry ingest
├── policy_compiler/                     ACT / diffusion → wasm module
├── coordinator/                         LLM-driven trial orchestrator
└── library/                             curated policies per task class
```

These are the next implementation slices once the high-level
direction is locked in.

## References / open-source projects to study

- **LeRobot** — HuggingFace. https://github.com/huggingface/lerobot
- **ALOHA / ACT** — Stanford. https://github.com/tonyzhaozh/aloha
- **OpenVLA** — open-vocabulary VLA. https://openvla.github.io
- **Octo** — Stanford generalist policy.
- **Parol 6** — Source Robotics. Open-source desktop arm.
- **ros2_control** — real-time control framework.
- **ONNX Runtime + wasi-nn** — on-device inference.

## TL;DR

The c-agent + brain iterative loop is the right substrate for
video-fed robotic learning. The pieces you'll integrate (vision
encoders, ACT/diffusion policy training, ONNX inference, ros2_control)
are all open and battle-tested. The defensible Yaver contribution
is the *deployment substrate* — signed modules, capability-gated
safety, hot-swap, audit, replay — not the policies themselves. Wire-
harness terminal-block insertion on Parol is a tractable v1: $2k
of hardware, 4 weeks of integration work, fits on a desk, has real
industrial demand.
