# Yaver — the Harness Compiler (the moat)

**Status:** design, 2026-06-06, branch `feat/yaver-robot-cell`. Design-only.
**Why it's the moat:** yaver-harness-automation-thesis §2,§7.
**Inputs/outputs touch:** [`harness-recipe.schema.json`](../hardware/yaver-harness-jig/harness-recipe.schema.json),
[`formboard.scad`/`nest_grid.scad`](../hardware/yaver-harness-jig/),
`desktop/agent/robot/` (`Program`, `Controller`, `VerifyMotion`),
`gpu-rental-orchestration`, Talos.

---

## 0. The one idea

> **A wire harness is a netlist. Compile it.**

The disposable thing is the jig. The defensible thing is the **compiler** that
turns a `harnessRecipe` (from/to list + connector BOM) into the three things a
cell needs — **automatically, deterministically, per SKU**:

```
                          harnessRecipe (per SKU)
                                  │
                   ┌──────────────┴──────────────┐
                   │       HARNESS COMPILER       │   pure function, no LLM
                   │   compile(recipe) -> Artifacts│   at deploy time
                   └──────────────┬──────────────┘
          ┌──────────────┬────────┴────────┬───────────────┐
          ▼              ▼                  ▼               ▼
   (a) JIG PACKAGE   (b) ROBOT PROGRAM  (c) AI CONTEXT  (d) TEST PLAN
   SCAD params +     robot.Program +    grounding pack  MATRIX pinMap +
   STLs + layout     force specs +      for the GPU     pull/mating specs
   + place sheet     insertion order    physical-AI
```

This collapses the two costs that kept harnesses un-automatable —
**per-SKU tooling and per-SKU engineering both go to ~0**. A new SKU becomes
*print + load*, not *engineer a fixture + write a program + author instructions*.

---

## 1. Why a compiler, not an LLM-per-harness

The deploy path must be **deterministic and auditable** — same recipe in, same
jig + program out, every time, no model drift. So `compile()` is a **pure
function** (the same posture as `deploy_script_gen.go` and the Fleet SDK's
`apply(verified)`: LLM for *understanding*, deterministic code for *acting*).

The LLM/GPU shows up in exactly two places, never in the deploy transform:
1. **Authoring (upstream):** turning a customer PDF / CAD / spreadsheet into a
   `harnessRecipe` — a one-time understanding task (like `yaver autoinit`).
2. **Runtime verify/recover (downstream):** the GPU physical-AI gating each
   insertion (`VerifyMotion`) and proposing recovery on faults (BT loop 3).

Between those, `compile()` is boring, fast, and testable. That separation is
the reliability story you already ship everywhere else.

---

## 2. Input — EPLAN → `harnessRecipe`

**The real front-end is EPLAN.** Control-panel ("pano") harnesses are designed in
EPLAN (the dominant electrical CAD), which already holds the **from-to connection
list**, connector & terminal BOM, cable list, and wire numbers. So the first
compiler pass is an **EPLAN importer**: ingest an EPLAN export — the
connection/wiring list + cable list (CSV/XML), or EPLAN Harness proD / Smart
Wiring data — and normalize it into the `harnessRecipe` below. This is what makes
the "pano-way" service (EPLAN upload → finished panel) possible; hand-authoring a
recipe (as in `example-harn-0042.json`) is only the test path.

`harnessRecipe` is defined in
[`harness-recipe.schema.json`](../hardware/yaver-harness-jig/harness-recipe.schema.json).
The load-bearing fields:
- `connectors[]` — `ref`, `family` (→ nest preset), `class`
  (`housing` = crimp-contact-into-cavity | `terminal_block` = ferrule/wire-into-cage),
  `gridCell`, `rotDeg`, `pullSpecN` / `termTorqueNmm`.
- `fromTo[]` — the circuit: `lead`, `from` `"<ref>:<cavity>"`, `to`, `gauge`,
  `color`, `marker`.
- `board`, `guides[]`, `ties[]`, `tests[]`.

A **connector-family library** backs `family` → physical facts the recipe
shouldn't repeat: cavity array (rows×cols×pitch), keying, contact pull spec,
insertion-axis, nest preset. (Closed, curated — part of the moat, lives in
Talos; OSS ships a few open families.)

---

## 3. The compile passes

```
compile(recipe, familyLib, cellProfile) -> Artifacts
```

`cellProfile` = the *this-cell* facts (arm backend + reach/payload/F-T?, camera
ids, companion pins, printer bed, available tools) so the same recipe targets
different cells.

### Pass 1 — Resolve & validate (fail fast, before any motion)
- Every `from`/`to` references a declared `connector:cavity` that exists in the
  family's cavity array. Unknown cavity → hard error.
- No cavity double-booked unless the family allows splices.
- Every connector's `gridCell` (+ rotation + nest footprint) fits on the board
  and **doesn't overlap** another nest/guide; all within arm reach
  (`cellProfile`). Overlap/out-of-reach → error with the offending refs.
- `class` matches the family (housing vs terminal_block).
- This is the deterministic gate; an invalid harness never reaches the floor.

### Pass 2 — Place (recipe → board coordinates)
- Map each `gridCell` → board frame (`gridCell * pitch + origin`), apply
  `rotDeg`, attach the nest footprint.
- Resolve each cavity → a **6-DOF target pose** in board frame (position from
  the cavity array + nest tilt; orientation from the insertion axis). Board frame
  ties to robot frame at runtime via the 3-2-1 datum + AprilTag refine
  (`harness_register_board`).
- Place `guides[]` (combs/forks) and `ties[]` at their cells.
- Emit a **place sheet** (human/QC): which nest/guide bolts to which cell.

### Pass 3 — Emit the jig package (a)
- Per connector: choose the nest generator + parameters from the family, write a
  `-D`-parameterized OpenSCAD invocation against
  [`nest_grid.scad`](../hardware/yaver-harness-jig/nest_grid.scad)
  (or `block_clamp.scad` for terminal blocks) and render STL.
- Emit the board STL from [`formboard.scad`](../hardware/yaver-harness-jig/formboard.scad)
  sized to `board.tile`, with the AprilTag + part-number QR pockets.
- Output: STLs + the OpenSCAD commands (reproducible) + the place sheet.
- **Per-SKU tooling cost = filament + print time.** Weird geometry is free.

### Pass 4 — Emit the robot program (b)
- Sequence `fromTo[]` into an insertion order that minimizes tool travel and
  respects reachability/occlusion (insert far/low cavities before near ones can
  block them).
- For each lead emit the insertion macro (the §4 loop of the mechanical design)
  as `robot.Step`s / a `robot.Program` (reuse `program.go`): present → approach
  standoff → ForceMove-with-search → seat-signature gate → vision verify
  (expectation = "contact flush in cavity N, wire straight") → pull-test to
  `pullSpecN`. Terminal-block class → land + drive-screw-to-`termTorqueNmm`
  (reuse `screw.go`).
- Bake in hard numeric bounds (force caps, envelope) so the deterministic Go
  gate validates every step independent of any model.
- Output is a plain `robot.Program` the existing `Controller.RunProgram`
  executes — **no new control plane.**

### Pass 5 — Emit the AI context (c)
The grounding pack the rented GPU physical-AI gets so its verdicts/recovery are
*informed*, not blind:
- the from/to list + cavity map (what *should* be where),
- the jig geometry + nest fiducials + camera extrinsics (where to look),
- per-step expectations + accept/reject thresholds (what "good" looks like),
- a **failure memory** keyed by SKU (priors: "cavity 4 on family X tends to
  high insertion force → search wider first").
Delivered as the structured context to `VerifyMotion` / the BT supervisor;
hosted on `gpu-rental-orchestration` (opex, autoscaled, shared across cells).

### Pass 6 — Emit the test plan (d)
- `continuity`: assign each `connector:cavity` a companion `MATRIX` index →
  the `pinMap`; expected adjacency derived from `fromTo`. (Drives the firmware
  `MATRIXPINS`/`MATRIX` command; diff per the
  [jig README](../hardware/yaver-harness-jig/README.md).)
- `pull`: per-contact retention targets (already applied during insertion).
- `mating`: max insertion force for the Fairino F/T plug-in check.

---

## 4. Artifacts (compiler output)

```jsonc
Artifacts = {
  jig:   { boardStl, nestStls[], openscadCmds[], placeSheet },
  program: robot.Program,            // ready for Controller.RunProgram
  aiContext: { fromTo, cavityMap, expectations[], thresholds, failureMemoryRef },
  testPlan: { continuity:{pinMap, expectedPairs[]}, pull[], mating[] },
  report: { warnings[], reachMap, cycleTimeEstimate }
}
```

---

## 5. Where it plugs in (all existing)

| Need | Existing asset |
|---|---|
| run the program | `robot.Controller` / `RunProgram` / `Backend`+`PoseBackend` |
| vision gate | `robot/verify.go` `VerifyMotion` on rented GPU (`gpu-rental-orchestration`) |
| force seat/pull | wrist F/T or `firmware/yaver-companion` (HX711/INA219) |
| continuity test | companion `MATRIX` (new), `harness_test` verb |
| store/dispatch recipe + program | Talos (`machineRecipes`/`roboticsCommands`), program-index in Yaver |
| order → build → test | Talos manufacturing-on-demand |
| HMI | Yaver mobile app (fleet UI) |

New surface is thin: the `compile()` package + a few `harness_*` ops verbs
(`harness_compile`, `harness_load_recipe`, `harness_register_board`,
`harness_run`, `harness_test`) registered the same self-registering,
mesh-routable, `AllowGuest=false` way as `robot_*`.

**Privacy:** `harnessRecipe` + from/to + part numbers are **work-derived → user's
Talos org only**, never Yaver Convex. Only a capability + program *index* lives
in Yaver for discovery (same split as teach-and-repeat programs). Any new sync
path adds its fields to `fieldsWeForbidInAnyConvexPayload`.

---

## 6. Build order

1. **Types + Pass 1–2** (resolve/validate/place) + the family lib for the first
   real SKU. Pure Go, fully unit-testable with no hardware (golden recipes).
2. **Pass 6 + Pass 3** → drives the **P0 self-test bed + printed jig** that
   already exist in `hardware/yaver-harness-jig/`. First end-to-end: recipe →
   board STL + nest STLs + `MATRIX` pinMap → print, wire, test.
3. **Pass 4** (robot program) once a real arm + cell profile is in hand.
4. **Pass 5** (AI context) wired to the rented-GPU verify loop.
5. `harness_*` ops verbs + Talos recipe storage + mobile HMI.

Test posture (per repo convention): golden `harnessRecipe` → assert
`Artifacts` (placement coords, program steps, pinMap, raised warnings); render
the emitted SCAD in CI and assert **manifold**. No mocks, no hardware needed for
the compiler's own tests.

---

## 7. Restating the moat

Anyone can buy an arm and a printer. They cannot cheaply replicate **a compiler
that emits jig + robot program + grounded AI context + test plan from a netlist**
sitting on top of an already-shipped stack (provisioning, mesh fleet, gpu-rental
autoscaling, move-and-verify, fault recovery, order→recipe→job). That vertical is
the per-SKU-→0 economics. The jig is disposable; **the compiler is the asset.**

---

*Code is the source of truth. `desktop/agent/robot/`, `firmware/yaver-companion`,
and `gpu-rental*` move under parallel sessions — re-grep before building.*
