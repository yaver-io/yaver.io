# Yaver — Wire-Harness Stack Coverage Analysis (video → GPU → model → arm)

**Status:** strategic + code-grounded coverage analysis, 2026-06-08, `main`.
**Companion to:**
[video→policy cell](yaver-video-to-policy-harness-cell.md),
escalation + bimanual,
automation thesis,
[arm-served cell](yaver-arm-served-harness-cell.md),
[robot simulators](yaver-robot-simulators.md),
[jig/formboard](yaver-wire-harness-jig-formboard-design.md),
[GPU rental](gpu-rental-orchestration.md).

> **Code is the source of truth.** This analysis was written after mapping the
> actual code in `desktop/agent/arm/*.go`, `desktop/agent/cell/*.go`,
> `desktop/agent/ops_*.go`, and `desktop/agent/gpu_rental*.go`. Where a claim
> below cites a type or verb, it was verified against the file named. Re-grep
> before building — parallel threads move constants.

---

## 0. The question, restated

> Feed **human video** of wire-harness operations into the system + a **GPU**,
> plus **robotic models / deep-learning models**, and make a **robotic arm
> replicate** the wire-harness operations. Plus **custom magazine (feeder)
> designs**, using **DeepInfra / Salad** for GPU, and grounded in real knowledge
> of the available **robot foundation models**. Endgame: perfect the wire-harness
> company, possibly with **two arms / dexterous (multi-finger) hands**.

This document answers, stage by stage: **what the Yaver stack already covers,
what is a gap, and what to build to close it** — and corrects two framings
("video → robot copies", "5-finger hands") that would burn months if taken
literally.

---

## 1. Coverage scorecard

The pipeline is six stages. Verified state in repo:

| Stage | Goal | State in repo | Verdict |
|---|---|---|---|
| **A. Capture** | operator video → training signal | `arm/demo.go` records dense `{frame,joint,pose}` on the **real arm** (free-drive/teleop), LeRobot-convertible. Raw-human-video→policy **not built** (research accelerator only). | ⚠️ arm-demo built; human-video unsolved |
| **B. Train** | rent GPU, train model | doc-only procedure (LeRobot on rented box). No Go orchestration. Dataset converter (jpg+jsonl→parquet) is a gap. | ◐ manual |
| **C. Serve** | DeepInfra/Salad serves the policy | `gpu_rental.go` provisions Salad/DeepInfra for **LLM/voice only**; robot policy server **not wired** to rental — `policy_bind` takes a hand-typed endpoint. | ◐ partial (§4) |
| **D. Policy client** | box pulls actions from GPU | `arm/policy.go` `PolicyClient` — `POST /act` → action chunk over HTTP, bearer auth. **Built + tested.** | ✅ |
| **E. Safety + execute** | remote model can't crash the arm | `SafetyGate` (per-joint range **refuse**, max-step cap, stale-server watchdog, e-stop latch) + `RunPolicy` chunk executor. **Built + tested** (out-of-range→e-stop, oversized-jump→e-stop, stop-interrupt). | ✅ strongest part |
| **F. Verify** | "is it seated?" gate | vision verify via `robot_camera`/`arm_look` + Claude VLM. Built. | ✅ |

**The spine D–F is real and was run live** (`TestSimPolicyE2E`, `SIM_E2E=1`):
`SimBackend` → `HTTPCamera` → reference policy server → `RunPolicy` drove the sim
arm to goal through 4 safety-gated steps. The weak links are exactly the two the
user is pushing on: **getting data in (A)** and **getting GPU-served models out
(C)**.

---

## 2. The "human video → robot imitates" reality (load-bearing)

The research sweep in [video→policy](yaver-video-to-policy-harness-cell.md)
already landed the honest answer; it governs everything else:

- **"Camera on operator → robot does the task" does not exist for contact-rich
  wire work.** The human-hand→gripper **embodiment gap** is unsolved end-to-end.
  Best published results (DexWild, EgoMimic) **co-train** a little robot data with
  human video — nobody ships contact-rich assembly from raw human video alone.
- **Limp wire is uncontrollable without fixturing.** Every credible automated
  result (Nissan/UTK, the 2025 dual-arm multi-branch paper, CaRoBio) automates
  **one narrow sub-step under heavy fixturing**. Academic name: **DLO (deformable
  linear object) manipulation**; state-estimation-under-occlusion is the
  bottleneck.
- **What works in 2025/26:** demos on the **real arm** → train
  ACT/Diffusion-Policy/SmolVLA (or fine-tune π0) in LeRobot → fine-tune on a
  rented GPU → serve with **action-chunking** → execute behind a **local safety
  gate** → Claude does sub-task planning + VLM "seated?" verification.

**Therefore the realistic role of operator video in v1 is authoring the sub-task
sequence + coordinates / positionMap** (anchored to a shared fiducial grid) and
**co-training data — not the policy itself.** The arm-demo path (`arm_demo_*`) is
the spine; video is an accelerator bolted on later (HaMeR/WiLoR hand-pose +
CoTracker + fiducial retarget — research-grade, not a v1 dependency).

> **Do not let "feed video, robot copies" set the v1 expectation.** Feed video to
> author *recipes and coordinates*; feed *arm demos* to train *policies*.

---

## 3. Robotic / deep-learning model menu, mapped to *this* task

Wire-harness work is "fixed-station, contact-rich, high-mix-SKU." Model choice is
task-shaped:

| Model | Params / license | Hz | Fit in this cell |
|---|---|---|---|
| **ACT** (Action Chunking Transformer) | ~40M, open (LeRobot) | ~50 | **Default.** ~8h/A100. Beats big VLAs on a *fixed* task (your stations are fixed). One policy per sub-step: seat-terminal, insert-seal, route-segment. |
| **Diffusion Policy** | open (LeRobot) | — | Alt to ACT; smoother on multimodal moves (the dressing stroke, where several paths are valid). |
| **SmolVLA** | 450M, open (LeRobot) | async | Best practical VLA: async inference, one consumer GPU. Use for **language-conditioned** ("seat red lead in cavity 4") across SKUs without per-SKU retrain. |
| **π0 / openpi** | big VLA, **Apache-2.0** | — | Only open **and** commercially-licensed big VLA. Multi-SKU, language-conditioned. Fine-tune target when ACT-per-task stops scaling across the catalog. |
| OpenVLA-OFT | 7B, MIT | ~50 | Heavier alternative if π0 underperforms. |
| **GR00T N1.5** | **non-commercial** | — | ⛔ eval only — license **blocks product use**. Don't build on it. |
| RT-2 | closed | — | Reference only. |

**Strategy matching the stack:** ACT-per-sub-step now (cheap, fast, fixed
stations) → graduate to **π0 fine-tune** when high-mix SKU count makes per-task
ACT training a treadmill. **LeRobot is the spine** — the demo recorder already
writes a LeRobot-convertible dataset on purpose, so no single-model lock-in.

**Safety-gate nuance:** every model outputs an **action chunk** (N steps, ~5).
`RunPolicy` executes the chunk as sequential blocking moves behind the gate —
correct/safe on a cobot. True 50 Hz open-loop streaming (Physical-Intelligence
pattern: local action buffer + a streaming `Backend.SetTargetStream`) is a
**noted gap**; not needed for station work, wanted for the dressing stroke (§6).

---

## 4. GPU layer — DeepInfra / Salad coverage + the one real gap

Verified in `gpu_rental.go`, `gpu_rental_ops.go`, `gpu_autoscaler*.go`,
`accounts.go`:

- **Active providers: Salad + DeepInfra only.** `accounts.go` enumerates
  `ProviderSalad/DeepInfra/RunPod/Vast`, but the rental layer implements only
  `HostSalad` and `HostDeepInfra` (RunPod/Vast are enum-present, **not
  implemented**).
- **Salad** = container marketplace. Provisions a **container group** running an
  OpenAI-compatible image (default `vllm/vllm-openai:latest`); returns group ID +
  endpoint URL once DNS assigns. **Right primitive for serving a robot policy** —
  swap the image for `yaver_policy_server.py`.
- **DeepInfra** = serverless per-token inference; "provisioning" just validates a
  token + binds a catalog model to their OpenAI base URL. **Per-token billing is
  wrong for a custom robot policy** (per-step, custom checkpoint, not a catalog
  LLM). DeepInfra stays the LLM/voice lane.
- **Autoscaler** scales on **sustained concurrency** (`BurstAtConcurrency=20`,
  `ReapBelowConcurrency=10`, `SustainTicks=3`) read from a `/metrics` endpoint —
  tuned for voice/LLM, not a single-cell robot policy (concurrency≈1).

**The gap, precisely:** the policy server is a **separate deployment concern** —
`gpu_rental*.go` never imports `arm/policy.go`. Today the flow is manual:
`cloud_provision host=salad` (policy image) → `gpu_status` (read endpoint) →
paste into `policy_bind`. The doc itself flags the fix: *"add RunPod/Modal
providers + a `kind:"policy"` endpoint registry alongside the LLM inference
binding."*

**Recommended split:**
- **Train** on per-hour boxes (Lambda/RunPod/Vast) — latency irrelevant for a
  batch job; cheapest per-hour wins.
- **Serve** on **Salad per-hour** *or* a **local Jetson** at the cell. For tight
  contact loops, the **Jetson wins** (ACT ~83 Hz on AGX Orin; factory networks
  are flaky and contact loops hate jitter). Rented GPU serving is fine for
  non-contact / language-conditioned planning cadence.

---

## 5. Custom magazine / feeder designs — partial coverage

In harness work a **magazine** = the component-presentation device (terminal
bandolier, seal carrier, connector nest, ferrule reel) that puts a part in a
**known pose** so the arm grabs it without a vision search.

**What exists:**
- `cell/job.go` **`FeedSpec`** (length + outer/inner strip + qty, from a Talos
  kesimRows row) and **`Lane`** (prep lane = ordered prep stations + a `Nest`
  pick-pose for the finished lead). This is *feed-of-the-wire*, not
  *magazine-of-the-components*.
- `cell/station.go` station kinds (`StationSealInsert`, `StationFerruleCrimp`,
  `StationConnInsert`, …) model the **machine** with taught
  `Approach/Present[]/Withdraw` poses + a trigger/done handshake. The
  presentation fixture is *implicit* in the taught poses.
- `ops_jig.go` **`robot_jig_generate`** emits parametric **OpenSCAD** for a
  klemens/pocket **grid** fixture — the seed of a magazine generator, grid-pockets
  only.

**The gap (real build opportunity):** no first-class **`Magazine`/`Feeder`**
abstraction, no parametric generator for *component* magazines (terminal-strip
indexers, seal trays, ferrule-bandolier guides, connector-nest arrays with
lead-in funnels). Clean extension:

- `cell/magazine.go`: `{kind, pitch, rows, cols, leadInFunnelMm, indexAxis,
  originPose}` → emits printable STL **and** the world-frame **pick-pose table**
  (pocket(i,j) → XYZ), so the arm knows where each part is.
- Wire new `kind:` values into `robot_jig_generate`: `terminal_strip`,
  `seal_tray`, `connector_nest`, `ferrule_guide`.
- **Design principle already in the codebase:** *lead-in funnels absorb robot
  positioning error.* Generate magazines **with funnels** so a cheap arm
  (±1 mm repeatability) still seats parts — this is how you avoid buying an
  expensive arm. Buildable on the existing OpenSCAD/grid spine; the natural next
  rung after `robot_jig_generate`.

---

## 6. Two arms vs. five-finger hands (the endgame question)

Separate two things the "2 five-finger arms" framing conflates: **bimanual (two
arms)** and **dexterous (multi-finger) hands**. The stack treats them very
differently.

### 6a. Bimanual (two arms) — well-reasoned, seam missing

escalation + bimanual already nails
the doctrine, and it's correct: **formalama (routing/dressing/tying the floppy
bundle) is the #1 labor sink and the one robotics-hard step**; the human does it
bimanually (one hand anchors a node, the other strokes the wire). The endgame is
**two tabletop arms** — and the winning move is to **collapse required dexterity**
by offloading two of three hard sub-problems to cheap infra:

| Sub-problem | Human uses | Offload to |
|---|---|---|
| **Holding** the routed topology | fingers as clamps | passive combs + **active servo posts** (board holds it) |
| **Knowing** the deformable state | eyes + proprioception | **GPU vision** — wire-centerline tracking + verify vs taught path |
| **The dynamic move** (anchor + stroke) | two hands | **two arms** — the only residual that truly needs the 2nd arm |

**Stack coverage:** `arm` is single-controller today. Bimanual = two `Config`s +
two `Controller`s + a **coordination layer that does not exist yet** — a
`cell/bimanual.go` state machine ("arm A anchors → arm B strokes → active post
clamps → arm B releases → vision verifies"). `cell/orchestrator.go` is
single-arm-serving-stations. This is the **R6 rung — explicitly last, funded by
R0–R5, gated on volume.**

### 6b. Five-finger dexterous hands — reframe, don't build

Honest analysis on anthropomorphic 5-finger hands (Shadow/Allegro/Wonik/humanoid
hands):

- **DOF + obs gap:** a 5-finger hand is **16–24 DOF**. The `arm` package is
  DOF-agnostic (DOF = data from URDF), so `Backend` *could* drive a hand as "more
  joints." But there is **no end-effector / gripper abstraction, no tactile
  model, no contact-state channel** in the obs (`/act` is images + joints).
  A dexterous hand without tactile feedback is nearly useless for wire work, and
  the obs contract has nowhere to put tactile data.
- **Data explosion:** ACT needs 30–400 episodes per sub-step on a 6-DOF arm. A
  20-DOF hand doing in-hand manipulation needs **orders of magnitude more** data,
  and the human-video embodiment gap is **worse** (your fingers ≠ the robot's
  fingers, joint-for-joint).
- **Cost:** research-grade 5-finger hands are **$10k–$100k each** — two is the
  whole-plant budget the doctrine
  explicitly refuses to spend in one bet.

**Verdict (consistent with the doctrine's own "don't build human hands"):**
five-finger hands are the trap. They *maximize* the dexterity you must learn,
when the winning move is to *collapse* it. For wire harness specifically:

- **Insertion / seating / crimping** → simple **2-finger parallel gripper +
  force** (`ForceBackend`, interface exists) + lead-in funnels. No fingers.
- **Dressing / routing** → **two simple arms + active servo posts**, not two
  hands. The stroke is an anchored sweep, not finger gymnastics.
- **Tying** → a dedicated **tie-head station** the arm serves, not finger-tied
  knots.

If a sub-step genuinely needs multiple fingers (rare in harness work), the
cheapest viable rung is a **3-finger underactuated adaptive gripper**
(Robotiq-class, ~$5–10k, ≈1 controlled DOF + compliance) — **not** a 24-DOF
anthropomorphic hand — and the stack would still need a gripper abstraction +
tactile obs channel first.

> **Reframe "2 five-finger arms" → "2 simple arms + active-infra board + funneled
> magazines + force-gripper."** That is exactly R5→R6 and is buildable on this
> stack. Five-finger hands are a different (research) company.

---

## 7. Deep dive: human **2-finger** video → robot actions (the pinch≡gripper insight)

This is the single most important reframing in the whole analysis. The "embodiment
gap" that makes "human video → robot copies" research-hard (§2) is a **morphology
mismatch**: a 5-finger human hand doing in-hand reorientation has no equivalent on
a gripper, so the mapping is lossy and unsolved.

**But if the operator works with a 2-finger pinch (thumb + index), that gap nearly
disappears.** A pinch grasp maps almost **bijectively** onto a 2-finger parallel
gripper. You are no longer transferring across morphologies — you are transferring
between two devices that do the *same thing*. This is the **easy case**, not the
hard one, and it is exactly the gripper the §6/§5 plan already lands on.

### 7.1 The bijection (why it works)

From a tracked human pinch, every value a parallel gripper needs is directly
recoverable, frame by frame:

| Gripper needs | Recovered from the human pinch |
|---|---|
| **TCP position** | midpoint of (thumb-tip, index-tip), in the board frame |
| **Grip axis / roll** | unit vector (index-tip − thumb-tip) → the finger-opening axis |
| **Approach orientation** | hand normal + wrist direction (palm/back orientation) |
| **Grip width command** | ‖index-tip − thumb-tip‖, clamped to the gripper's stroke |
| **Grasp / release events** | width collapses below a threshold while loaded → close; widens → open |

The output is a clean trajectory of **(TCP pose, gripper width, grasp/release)** —
which is *exactly* what a parallel gripper executes. Crucially, this lives in
**task space (Cartesian)**, so you sidestep joint-space embodiment entirely: the
Fairino consumes TCP poses via `MoveL` (it reports `HasCartesian=true`) and does
its own IK. No human-joint → robot-joint retarget is needed.

> **Constrain the demo to constrain the problem.** Tell the operator: pinch only,
> no in-hand reorientation, pick-place-insert moves. That single rule turns the
> research-hard transfer into an engineering pipeline.

### 7.2 The perception → retarget pipeline (the "AI transform")

This is the new build. It runs as a batch job on the **same rented GPU**
(Salad/DeepInfra/Lambda) you already use for training — latency is irrelevant for
offline video processing:

```
operator video (overhead RGB over the grid board)
   │
   ├─ HaMeR / WiLoR  ─────► 3D hand keypoints per frame (thumb-tip, index-tip, wrist)
   ├─ AprilTag + solvePnP ► camera↔board extrinsics  (every frame, drift-free)
   ├─ CoTracker ─────────► wire centerline / connector point tracks (the object)
   │
   ▼  retarget (§7.1)
trajectory of (TCP pose, grip width, grasp/release)  IN THE BOARD FRAME
   │
   ├─(a)─► CellProgram waypoints + positionMap   (deterministic recipe — no model)
   └─(b)─► jpg+jsonl episode (existing demo format) → co-training data for ACT/π0
```

### 7.3 The grid board is the keystone (not a nice-to-have)

Without a shared metric frame, human hand keypoints live in pixel/camera space and
are useless to the robot. The grid board + AprilTags do four load-bearing jobs —
this is why "we will have grid boards as well" is the enabling condition, not a
detail:

1. **Scale.** Monocular hand pose is scale-ambiguous; the known AprilTag size +
   grid pitch fix absolute millimetres.
2. **One world frame everywhere.** The grid pitch *is* the metric frame shared by
   **camera, human demo, and robot** — already the design in
   [video→policy](yaver-video-to-policy-harness-cell.md) §1. Human-demo
   coordinates == robot coordinates == jig coordinates == magazine pick-poses.
   **Train once, execute directly, no re-registration.**
3. **The z=0 work plane.** For a flat formboard, assuming the board plane resolves
   the monocular depth ambiguity for everything *on* the board — which is most
   pick/place/route work.
4. **The same coordinate table drives everything.** Jig generation (§ `robot_jig_generate`),
   magazine pick-poses (§5), and the retargeted human trajectory all reference the
   one grid origin. The board is the universal join.

### 7.4 Two outputs, two timelines

- **(a) Deterministic recipe / coordinate authoring — the near-term win.** Watch
  the operator do a SKU *once*; emit the taught `CellProgram` waypoints +
  `positionMap` (cavity→color) **without training any model**. This is "teach by
  watching" and it's the fastest path to value: it replaces the manual
  hand-guide-and-capture (`arm_demo_start` / `cell_station_teach`) for the
  *coordinate* parts of the job. Execution is the already-built MoveL + safety gate
  + vision verify.
- **(b) Co-training data — the policy accelerator.** The same retargeted
  (pose, image) pairs, emitted in the existing `episode_NNN/{meta.json,
  frames/*.jpg, states.jsonl}` format, **augment** real-arm demos for ACT/π0,
  cutting how many real-arm demos you need. Per the research (§2) this is
  **co-training, never standalone** — you still collect *some* real-arm demos for
  the contact micro-motion.

### 7.5 Honest caveats even in the 2-finger best case

The pinch bijection solves *grasp geometry*. It does **not** solve:

- **Off-plane depth.** Monocular is rough above the board plane. Mitigate: a second
  camera (stereo) or a wrist camera; or accept that 3D insertion *angles* come from
  a few real-arm demos. Planar pick/place is fine from one overhead camera.
- **Contact force.** "How hard to push to seat" is **not in the video.** This is
  exactly what `ForceBackend` (gap #4) + a little real-arm tuning supplies. Video
  gives you the *where*; force control gives you the *how-hard*.
- **Occlusion at the grasp instant.** The hand covers the part right when it grabs.
  Mitigate: infer the grasp pose from pre-grasp frames + CoTracker interpolation.
- **Deformable wire state.** Still the hard formalama problem (§6a) — unchanged by
  the 2-finger insight; that's the R6 endgame.

### 7.6 What Yaver must add to support this

1. **A `video2demo` perception service** (Python, runs on the rented GPU):
   HaMeR/WiLoR + AprilTag solvePnP + CoTracker → emits **either** the existing
   jpg+jsonl episode format **or** a `CellProgram`. The big new build; everything
   downstream already exists.
2. **A gripper / end-effector abstraction** in `arm` (grip-width command, grasp/
   release) — currently missing; needed to consume the width channel.
3. **A Cartesian safety gate.** Today `SafetyGate` is joint-space (per-joint range
   + max-step). Video retargets to *Cartesian* `MoveL` targets, so it needs a
   pose-space sibling (workspace box + max Cartesian step + orientation clamp).
4. **Verbs:** `cell_program_from_video` (output a) and `arm_demo_from_video`
   (output b) — thin wrappers over the perception service.

> **Bottom line:** "human uses 2 fingers + grid boards" is not a harder ask than
> the general video→robot dream — it is the **specific, tractable** version of it.
> The pinch≡gripper bijection + the grid-board world frame turn it into a
> perception pipeline whose outputs plug straight into the already-built execution
> spine. Start with output (a) — teach-by-watching for coordinates — because it
> needs no training and banks value immediately.

---

## 8. Prioritized gap list (dependency / ROI order)

All buildable on the existing spine:

1. **LeRobot dataset converter** (jpg+jsonl → parquet+video) — small script;
   unblocks all training. Cheapest, highest leverage.
2. **`kind:"policy"` in the GPU-rental binding** — let `policy_bind` consume a
   `gpu_*`-provisioned Salad endpoint directly + a `yaver_policy_server.py`
   serving image. Closes the §4 gap.
3. **Magazine / feeder generator** (`cell/magazine.go` + new `robot_jig_generate`
   kinds, with lead-in funnels + pick-pose table). The "custom magazine designs"
   ask (§5).
4. **`ForceBackend` implementation** on Fairino (interface exists, no backend
   implements it; sim could via PyBullet contacts) — unlocks contact-rich
   insertion + pull-test.
5. **Gripper / end-effector abstraction** in `arm` (grip-width command, grasp/
   release) — missing; needed for any real pick-place and to consume the §7 width
   channel.
6. **`video2demo` perception service** (HaMeR/WiLoR + AprilTag solvePnP +
   CoTracker → existing episode format *or* `CellProgram`) + the
   `cell_program_from_video` / `arm_demo_from_video` verbs. The "AI transforms
   2-finger operator video into our infra" ask (§7). **Start with output (a),
   teach-by-watching, which needs no model training.**
7. **Cartesian safety gate** — pose-space sibling of `SafetyGate` (workspace box +
   max Cartesian step + orientation clamp) for video-retargeted `MoveL` execution.
8. **Multi-camera obs** (wrist + overhead) — the `images` map already supports
   names; small. Also improves §7 off-plane depth.
9. **Streaming execution** (`Backend.SetTargetStream` + local action buffer) —
   only for the dressing stroke, not station work.
10. **`cell/bimanual.go` coordinator** (arm A anchor / arm B stroke / active-post
    IO) — the R6 endgame; last, volume-gated.

---

## 9. One-paragraph answer

The spine that makes a remote-GPU-served learned policy drive a real arm safely
(**policy client + safety gate + chunk executor + vision verify**) is **built and
tested**. The gaps are at the two ends: **data in** (operator-video→policy is
research; the real path is arm-demos → LeRobot → ACT/π0) and **models out**
(Salad can serve a policy, but `policy_bind` isn't wired to the rental layer yet).
"Custom magazines" is a natural extension of the existing OpenSCAD jig generator.
And the endgame is **two simple arms + active-infra board + funneled magazines +
a force gripper** — *not* five-finger hands, which the project's own escalation
doctrine correctly warns against. Climb the rungs cheapest-ROI-first; every rung
stands alone and funds the next.

---

*Costs are 2026 planning figures against Talos/partner labor + op data, not
quotes. Re-grep `desktop/agent/arm/`, `desktop/agent/cell/`, `gpu_rental*.go`
before building; fix this doc in the same change if code drifts.*
