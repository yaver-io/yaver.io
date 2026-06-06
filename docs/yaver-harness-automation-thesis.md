# Yaver × Talos — Wire-Harness Automation: the asymmetric thesis

**Status:** strategic + economic + architecture deep analysis, 2026-06-06.
Branch `feat/yaver-robot-cell`. **Design/analysis only.**
**The "how" (mechanical):** [yaver-wire-harness-jig-formboard-design](yaver-wire-harness-jig-formboard-design.md).
**Reuses shipped assets:** `gpu-rental-orchestration` (rented physical-AI brain),
`desktop/agent/robot/` (arm control + vision verify), Yaver mobile app (HMI),
`firmware/yaver-companion` (force/continuity), Talos manufacturing-on-demand
(order→build→test).

---

## 0. One sentence

Wire-harness assembly is the **least-automated, most labor-intensive step in all
of electronics manufacturing**, the incumbents' only moat is **cheap offshore
labor**, and we delete that moat with an **asymmetric, software-defined cell** —
3D-printed per-harness jigs (per-SKU tooling cost → ~$0), auto-generated robot
code, and a rented-GPU "physical AI" that knows the whole parts/flow context —
that drives **per-unit labor toward zero** over **existing machinery for
under $10k of retrofit**.

This is the Ukraine-vs-Russia shape: we don't out-spend a $45M Morocco plant
(Aptiv, 2025, ~3,000 manual jobs). We make those 3,000 jobs unnecessary with a
$2k overlay on machinery we already own, replicated across cells on the mesh.

---

## 1. Why this market is the prize (and why it's still un-automated)

### The numbers
- **$103.3B** wire-harness market in 2025 → **$152.9B by 2035** (≈4% CAGR);
  automotive harness alone ~$48B (2024). [FMI; Polaris]
- Harness assembly is **"among the most labor-intensive manufacturing processes
  in the tier-1 automotive supply chain."** [Persistence MR]
- It is **offshored for one reason: labor.** Morocco, Tunisia, Eastern Europe =
  "the assembly workshop for Western European OEMs." Aptiv opened a Tangier plant
  in 2025 — **$45M for ~3,000 jobs.** [search refs] The capital is spent on
  *people*, not robots.

### Why it resisted automation for 40 years (the structural moat)
Every source says the same three things:
1. **Deformable-object manipulation is an open robotics problem.** Wire is
   floppy, unpredictable, 3D. "An emerging research problem in robotics."
2. **High-mix / low-volume.** Endless variants (gauge, length, color, marker,
   terminal, connector, routing). General-purpose automation can't adapt.
3. **Per-SKU tooling cost.** Physical form/nail-boards "demand a great deal of
   set-up, maintenance, and storage overhead"; hard tooling is only justified at
   automotive ultra-volume. For everything else, **a human + a paper drawing on
   plywood is still the cheapest option.** That's the whole game.

The existing "automation" (CST18D/CC36-class machines) only automates the
**lead prep** — cut/strip/crimp/seal. **The harness *assembly* — insert, route,
bundle, test — is still 100% hands.** That untouched step is where 50%+ of the
labor lives, and it's the beachhead.

> The incumbent's strength *is* the attack surface. Their cost structure assumes
> labor is cheap and tooling is expensive. We invert both.

---

## 2. The asymmetric inversion — every incumbent cost line → ~0

The whole thesis is one table. Win each row and the market's economics flip.

| Cost line | Incumbent reality | Our move | Result |
|---|---|---|---|
| **Per-unit labor** | 50%+ of conversion cost; offshored to $2–4/hr regions; *acute labor shortages even there* | load pre-crimped leads → press go; vision+force gate replaces the operator's eyes/hands | **→ ~0** (asymptote, not exactly 0) |
| **Per-SKU tooling** | $1k–$10k+ hard formboard/fixtures, weeks lead, storage overhead | **3D-printed self-aware jig per harness**, any weird shape, ~$20–50 filament, hours | **→ ~$0 marginal** |
| **Per-SKU engineering** | manual fixture + work-instruction authoring per variant | **harness compiler**: netlist/from-to → jig geometry + robot program + AI context, auto | **→ ~0** |
| **Capital / compute** | $10M+ hard-automation lines (only at OEM volume) | **rented GPU** physical-AI (opex, `gpu-rental-orchestration`) + **<$10k retrofit over existing machinery** | **capex → opex** |
| **Changeover** | re-tool, re-board, re-train per variant — kills low volume | swap printed jig + load new recipe; cell self-registers it | **minutes, near-free** |

The compounding asset isn't any single jig. It's that **per-SKU tooling AND
per-SKU engineering both go to ~0** — so the cell serves an **unbounded number
of harness variants at zero marginal setup labor.** That is the exact realm
(high-mix / low-volume) the incumbents declared un-automatable. We don't beat
them at their high-volume game; we **take the long tail they can't touch**, then
walk up the volume curve as cycle time improves.

### The labor-cost asymptote = the kill shot
"Once labor cost converges to ~0, we can take the whole market." Correct, and
here's the mechanism: harness pricing is **cost-plus on labor + materials**. If
our labor → ~0 and tooling → ~0, our price floor is **materials + electricity +
amortized cheap hardware + rented-GPU minutes**. An incumbent paying 3,000
salaries **cannot follow us down** — their fixed labor base is the floor we
priced under. They either shrink to ultra-high-volume OEM contracts or they buy
the capability. Either way the long tail and mid-volume are ours.

---

## 3. The cell as you described it: jig + code + GPU-AI, per harness

Your formula, made concrete:

```
  HARNESS DEFINITION  (from/to list + connector BOM + 3D routing, per SKU)
            │
            ▼
   ┌──────────────────  HARNESS COMPILER (the real moat)  ──────────────────┐
   │  emits, per SKU, automatically:                                         │
   │   (a) printable SELF-AWARE JIG   → parametric SCAD/CAD → 3D printer      │
   │   (b) ROBOT PROGRAM             → poses · insertion order · force specs  │
   │   (c) GPU-AI TASK CONTEXT       → parts flow, jig geometry, recipe,      │
   │                                    prior-failure memory, verify prompts  │
   └────────────────────────────────────┬───────────────────────────────────┘
            │                            │                          │
   3D-print the jig            load robot program          spin rented GPU brain
   (per-SKU, ~$0)              onto the arm                (gpu-rental, opex)
            └───────────────┬────────────┴───────────┬──────────────┘
                            ▼                         ▼
                   EXISTING MACHINERY          YAVER MOBILE APP
                 (CST18D/CC36 leads,           = HMI: start/stop, watch
                  arm, benches)                 streams, approve, audit
                            │
                            ▼
        RUN HARNESS  — for each pre-cut/crimped lead:
        present → insert → seat(force) → verify(GPU vision) → pull-test → tie → self-test
        every step gated; faults → BT recovery → escalate to phone only if stuck
```

### Each block = an asset you already shipped (so the <$10k is real)
- **(a) Self-aware jig** — parametric printed board + self-describing nests on a
  pogo/1-wire grid (board map builds itself); see
  [jig design §C, §10.2-①](yaver-wire-harness-jig-formboard-design.md). Printer +
  filament is the only per-SKU cost. *Weird shapes per harness are a feature, not
  a cost* — the printer doesn't care about geometry, and the compiler emits it.
- **(b) Robot program** — `desktop/agent/robot/` (`Controller`, `Backend`/
  `PoseBackend`, `Envelope`, e-stop, encoder/torque cross-check) already runs
  move-and-verify on Ender (Cartesian) and Fairino (6-DOF). The compiler emits a
  `Program`/recipe; nothing new in the control plane.
- **(c) GPU "physical AI" that knows everything** — **this is what makes it
  adaptive.** Rented GPU via your **shipped `gpu-rental-orchestration`**
  (Salad/DeepInfra, autoscaler, `gpuRentals` live in prod). It hosts the vision
  model (Qwen3-VL-8B / Gemini Flash via `VerifyMotion`) **plus the full task
  context** — the jig's geometry, the parts-flow, the from/to list, and a memory
  of prior failures for *this* SKU — so its verdicts and recovery proposals are
  grounded, not blind. Opex per minute, scales to demand, **zero compute capex.**
- **HMI = Yaver mobile app** — start/stop, live MJPEG, verdicts, approve-gates,
  audit. Already the robot fleet UI ([fleet-mesh](yaver-robot-fleet-mesh-design.md)).
- **"Servo in the infra"** — cheap distributed actuation (NEMA17 ≈ $5–12 ea,
  driven over the **machine engine** you already run, Modbus/serial steppers):
  motorized lead presenters, indexing/tilting board (jig design §10.2-③),
  active nests (§10.2-④). **Many cheap DOF beats one expensive arm** — itself an
  asymmetric move. A turntable lets a small FR3 cover what would need an FR5;
  steppers let the board feed itself.
- **Order → build → test loop** — Talos manufacturing-on-demand
  ([mfg-on-demand](yaver-talos-manufacturing-on-demand.md)): a harness is
  ordered by part number → compiler → cell builds + self-tests + records. The
  cell is just another machine class on the
  [yaver-for-machines](yaver-for-machines-design.md) stack.

---

## 4. Unit economics (illustrative — confirm with a real SKU)

Take a mid-complexity harness: ~20 leads, 2–3 connectors, a few ties.

| | Incumbent (offshore manual) | Yaver cell |
|---|---|---|
| Assembly labor / unit | ~15–40 min @ $2–4/hr loaded → **$0.5–2.7** + supervision/QC/rework overhead | machine-tended; human touch ≈ load bin + unload → **→ ~$0.05–0.2** |
| Per-SKU tooling | hard formboard + fixtures **$1k–$10k**, weeks | printed jig **$20–50**, hours, auto-generated |
| Per-SKU engineering | fixture + work-instruction authoring | **compiler, ~$0** |
| QC / rework | visual, escapes to field (weak crimp passes) | **100% inline** vision + **pull-test every contact** → fewer escapes |
| Compute | — | rented GPU minutes (opex), shared across cells |
| Cell retrofit capex | — | **<$10k over existing machinery** (often <$2k overlay: printer, cams, MCU, gripper, light) |

The breakeven that matters: **incumbent tooling + labor has a high fixed +
high variable cost; ours has a near-zero fixed + near-zero variable cost.** So
we're *cheaper at unit #1* on a new SKU (no $5k tooling wait) **and** cheaper at
unit #100,000 (no labor). There's no volume band where their structure wins on
the long tail — only the OEM ultra-volume contracts (already locked, hard-tooled)
stay theirs, and those aren't the target.

---

## 5. Where it's hard — honest risk map (so we sequence right)

| Step | Difficulty | Why | Mitigation |
|---|---|---|---|
| Lead prep | **solved** | CST18D/CC36 already do it | out of scope — feed from it |
| **Continuity / pull / mating test** | **easy** | deterministic; companion `MATRIX` + F/T | **do first** — instant "we catch defects they ship" win |
| **Contact → terminal block (ferrule/screw)** | **medium** | reuse screwdriver + torque termination | class B first (no tiny lances) |
| **Contact → housing cavity (lance click)** | **medium-hard** | tiny parts, seat detection, pull-test | F/T spiral search + funnel nests + vision; the core IP |
| **Routing / lay-up of floppy leads** | **hard** | deformable manipulation, the open problem | start with **jig combs/forks (passive)** + pick-to-light human; automate later |
| **Taping / spiral-wrap / convolute** | **hardest** | continuous deformable wrapping | keep human/semi-auto (tie head) longest; don't gate the thesis on it |

The sequencing insight: **labor → 0 does not require solving the hardest step
first.** Most assembly labor is **insertion + test + handling**, not wrapping.
Automate insertion + test + presentation (medium difficulty, all reuse existing
Yaver loops) and you've already removed the bulk of the labor; leave wrapping
semi-manual until the deformable-manipulation models mature. **Partial
automation already collapses the cost structure** — the asymptote doesn't need
the last 5%.

---

## 6. Roadmap to "labor → ~0"

- **P0 — Self-test bed + digital formboard (no robot).** Companion `MATRIX`
  continuity + pull-data capture + top-down vision color/route check +
  pick-to-light. **Immediate edge:** 100% inline test the incumbents don't do,
  on a printed board. Proves the compiler emits a usable jig + recipe.
- **P1 — Harness compiler v1.** from/to + connector BOM → parametric jig (SCAD)
  + robot `Program` + GPU-AI context. The per-SKU-engineering-→0 asset.
- **P2 — Class-B insertion (terminal blocks).** Reuse screwdriver/torque. First
  real labor removed.
- **P3 — Class-A insertion (housings).** F/T + vision + pull-test. The core.
- **P4 — Servo infra.** Lead presenter + indexing/tilting board + active nests
  over the machine engine. Cycle-time + cheap-arm leverage.
- **P5 — Rented-GPU physical-AI at scale + Talos on-demand.** Cells as mesh
  `deviceId`s, autoscaled GPU brain, order→build→test→record. Replicate the
  cell, not the headcount.
- **P6 — Walk up the volume curve.** As cycle time drops, contest mid-volume,
  then squeeze the long tail of the $103B market from underneath.

---

## 7. The moat, restated

Anyone can buy an arm and a printer. The defensibility is the **stack already
shipped underneath this**: provisioning, mesh fleet, gpu-rental autoscaling,
move-and-verify with vision+force gating, fault recovery, order→recipe→job,
and now the **harness compiler** that turns a netlist into a printed self-aware
jig + robot code + grounded AI context. Competitors would have to rebuild that
whole vertical to match the per-SKU-→0 economics. The jig is disposable; the
**compiler + the fleet-of-machines agent are the asset.**

---

## 8. Open questions to confirm (drives the first real build)

1. **First real harness SKU** (Talos's own / a customer's) — its connector
   families, lead count, volume. Sets the first nest + compiler target.
2. **Existing machinery inventory** — exact arm(s) available (model/reach/
   payload/F-T?), benches, the CST18D/CC36 output format (rack/bandolier/bin?).
   The <$10k retrofit is sized against what's already there.
3. **GPU model + provider** for the physical-AI brain (Qwen3-VL self-host on
   Salad vs Gemini Flash API) — privacy vs latency vs $/min.
4. **Test-first or insert-first** as the wedge demo for Talos — I'd argue
   **self-test bed first** (cheapest, instant credibility: "we catch defects you
   ship"), then insertion.
5. **Volume target** of the first program — sets how far up the servo/tie
   automation to push vs leave semi-manual.

---

---

## 9. Field findings (line visit, 2026-06-06) — what's real, and the pivots

Photos of the actual line (Turkey; JCW auto crimp machine; gws/GES Kablo) changed
several assumptions. Ground truth:

| Finding | Implication |
|---|---|
| **Customer = Arkel** (ARCODE elevator control systems — CANbus, "plug & play" pre-wired harnesses, floor/LOP cables). High-mix but **standardized, repeating** connectorized cables. | Ideal compiler target: a **small connector-family library covers many SKUs**; near-identical floor/LOP cables = **high volume per SKU** = best automation ROI. |
| **#1 labor sink = routing / bundling / loom + tie** (not insertion). The deformable-handling step. | **Formboard-first.** Attack the worst step by speeding the *human* (pick-to-light, projector, recipe-checked routing) — **no robot, no deformable-manipulation solve required.** Semi-auto **tie head (T1)** next. Robot insertion drops to a *later* phase (it wasn't the sink). |
| **They already own a dedicated harness tester** (Cirris/DIT-class). | **Do NOT build a test bed.** The compiler **emits a test program for their existing tester** (net list → tester adapter map). The `MATRIX` firmware stays only as an optional ad-hoc inline gate, not the pitch. |
| **No formboard today — they mark positions with red tape on cardboard** (IMG_5061/5062). | The self-aware printed formboard is a **drop-in upgrade of something already improvised** — zero workflow disruption, instant pick-to-light + recipe. |
| **All four connector families present**: rectangular multi-pin (Harting-style), screw/spring terminal blocks, ring/fork lugs (bolt-on), push/lever (blue WAGO/Wieland-style). | Nest library needs **all four** generators, not automotive Mega-Fit. Industrial connectors are **bigger / more forgiving** → a cheaper, less-precise arm suffices when robot insertion does come. |
| **Loose crimp pins live in bins** (IMG_5059); leads sorted/rubber-banded by circuit by hand (kitting). | Bin-picking loose pins is hard — present contacts on the JCW carrier/comb instead. **Kitting is its own labor line** worth a presenter/rack. |

### Revised P0 (matches the real sink)
1. **Self-aware formboard + pick-to-light + projector** — replaces cardboard+tape, speeds the #1 labor step (routing), no robot.
2. **Compiler emits a test program for the existing tester** — integrate, don't rebuild.
3. **Semi-auto tie head (T1)** — cut the tie labor.
4. *Later:* robot insertion for the highest-repeat Harting looms; 4-family nest library.

### Arkel ARCODE as the first real program
ARCODE's plug-and-play, standardized connector set is the sweet spot: a handful of
families + repeating floor/LOP cables means the **compiler's family library + a few
recipes cover a large share of volume.** To compile the first real one I need
**part 1004930's** from-to list (or drawing / BOM / the tester's net file) — share
the PDF/CSV or point me at where it lives (Talos? ERP export?). I'll turn it into a
`harnessRecipe`, then emit the board STL + nest STLs + place sheet + tester program.

## 10. Line integration — merge the semi-auto islands into one flow (AGV/conveyor)

Your insight: don't replace the working semi-auto stations — **connect them.** Turn
islands (JCW crimp → kitting → formboard → tie → existing tester → pack) into a
lights-out flow with cheap carriers, orchestrated by the agent. The asymmetric move
again: **cheap carriers + software beat a monolithic transfer line.**

```
 JCW crimp ─► KIT ─► FORMBOARD ─► TIE ─► TESTER(existing) ─► PACK
     │         │         │         │          │              │
   pallet/board carries the recipe TAG (board-ID QR, §A) station-to-station
     └──── AGV / conveyor / pallet carriers move WIP ───────┘
                          ▲
        one agent BT-supervises the whole line; each station + each
        AGV = a mesh deviceId; recipe/order = Talos order→job
```

How it maps to what's already shipped — **no new control plane:**
- **The pallet/board is the data carrier.** Its board-ID QR/tag (`formboard.scad`
  pocket) = the recipe key. Every station reads the tag → loads its slice of the
  recipe → does its part → stamps progress. WIP routes itself.
- **AGV / conveyor motion = the machine engine.** Conveyor motors, diverters, AGV
  drive = Modbus/serial axes you already drive (`yaver-for-machines`, the machine
  engine). An AGV is just another `deviceId` on the mesh running the agent (phone/
  Pi brain, the connector-box edge).
- **Orchestration = the BT supervisor across stations**, the same loop-3 pattern,
  scaled from one cell to a line: sequence stations, hold WIP on fault, escalate to
  a phone only when stuck. Each station's verbs already exist or are thin additions.
- **Order → line → record = Talos manufacturing-on-demand.** Order a harness →
  recipe → the line builds + tests + records, replicated by adding stations/AGVs,
  not headcount.

Staging: prove **one cell** (formboard + tie + tester integration) first; then add a
**conveyor/pallet loop** between two stations; then AGVs for cross-line moves. Each
step is cheap carriers + a `deviceId` + recipe-on-tag — incremental, never a
forklift upgrade.

---

## 11. The grounded plan (Simkab × Arkel, real machinery, the endgame)

Mined from the Talos ERP (`/Users/kivanccakmak/Workspace/talos`). This replaces
the guesses with what the shop actually runs.

### 11.1 The shop & the machines (correction)
- **Shop:** Simkab (Turkey), ERP = Talos. Customers: **Arkel** (elevator/ARCODE,
  primary, quoted at ~$10/hr labor), **Harting** (both customer & supplier),
  **Cornelius** (Grohe/Blanco appliance), **Schneider**, **Langlotz**,
  **Arniepol**, **Bozankaya** (automotive).
- **The earlier "cc36" = Schleuniger CrimpCenter 36 S/SP** (fully-auto crimp,
  Komax/closed controller) — not a connector. The fully-auto **cut/strip/crimp**
  front of the line is: **Schleuniger CC36 + JCWELEC CST18D** (dual-end
  cut/strip/crimp/seal, 1–2k pcs/h) + **Yuanhan YH-8030H** (cut+strip, 100
  programs) + hand presses (Artos/2TP/Hatko) + electrical testers.
- **Real component families** (Talos `materials`/`recipes`): Harting Han B/D/E
  crimp pins+housings (5101/5201/93001005xx/91500051xx), WAGO 261-3xx blocks +
  221-4xx levers, JST XAP, Molex 39012160, Wieland, ferrules (DIN 46228), ring
  lugs, faston, H07V2-K / LiYY / LiYCY / HF cable. **These are the nest-library
  targets** — confirms §9 (industrial, forgiving, four families).

### 11.2 The exact scope: "0 labor after cut/crimp"
User's spec, mapped onto **Arkel's own 9-operation cost sheet** (Talos CLAUDE.md):

| # | Operation (Arkel sheet) | Today | Target |
|---|---|---|---|
| 1 | Kesim (cut/strip) | **AUTO** — CC36 / CST18D / YH | keep |
| 3 | Yüksük çakma (ferrule crimp) | **AUTO-ish** — RKES robot ≤6 mm² | keep |
| 2 | Birleştirme çakma (splice/join crimp) | manual press | **robotics** |
| 4 | Klemens vidalama (terminal-block screw) | manual | **robotics — reuse screwdriver+torque (`screw.go`)** |
| 5 | Makaron (heat-shrink fit + heat) | manual | **robotics — place + heat-gun end-effector** |
| 6 | Lehimleme (solder) | manual | robotics (rare) |
| 7 | **Formalama (form / route / tie)** | manual — **#1 labor sink** | **formboard + pick-to-light + tie head** |
| 8 | Elektriksel test | manual + existing tester | **compiler emits tester net file** |
| 9 | Göz ile kontrol (visual) | manual | **GPU-vision verify** |

→ The robotics targets are **ops 2, 4, 5, 7, 8, 9**. Cut/strip/crimp (1, 3) stay
on the existing autos — that's the only "semi-auto allowed" the user named.

### 11.3 The hardware standard: 6×6 reusable grid + custom per-product jig
- A **portable, reusable standard base = a 6×6 grid** of mount cells
  (`formboard.scad` parametrized to 6×6). Onto it bolts the **compiler-generated
  custom jig for that product** (nests + combs/forks/tie stations). The base
  travels and is reused; **the jig is the cheap per-SKU part** (print, ~$0).
- **Robotics edge works the grid:** GPU camera (verify, op 9), **Cartesian
  (Ender-class)** for linear work (present leads, tie, heat-gun sweep,
  pick-to-light), **Fairino** for dexterous work (contact insert op 2, terminal
  screw op 4, mate-for-test).
- **Stations merge:** the same gridded board is *form + insert + screw + test* in
  one reconfigurable cell — not a line of separate machines. Portable + reusable +
  self-describing (§10.2-①). This is the physical unit that the AGV/conveyor line
  (§10) carries between any heavier steps.

### 11.4 The compiler front-end is EPLAN
Control-panel ("pano") harnesses are designed in **EPLAN** — the dominant
electrical CAD. EPLAN already holds the **from-to (connection list)**, connector
& terminal BOM, cable list, and wire numbers. So the harness compiler
([compiler design](yaver-harness-compiler-design.md)) ingests **EPLAN exports**
(connection/wiring/cable lists as CSV/XML; or EPLAN Harness proD / Smart Wiring
data) → `harnessRecipe` → jig STLs + robot program + **cut/crimp programs pushed
to CC36 / CST18D / YH via the Talos machine bridge** + tester net file. **EPLAN is
the netlist; we compile it.** (No more hand-authoring recipes — that was the toy
path.)

### 11.5 The endgame: "pano-way" — Xometry/PCBWay for panels & harnesses
The next step the user named: a service where **customers upload their EPLAN
project → auto-quote → we manufacture with 0 human after cut/crimp → ship.**
"Pano" (control panel) first, via the wire harness. This is **Talos
manufacturing-on-demand** ([mfg-on-demand](yaver-talos-manufacturing-on-demand.md))
with **EPLAN as the upload format** and the **6×6 robotics cell as the execution
plane**. The flywheel: every job grows the connector-family library + failure
memory, widening what compiles fully automatically — so coverage of "any EPLAN in
→ finished pano out" keeps expanding. That is the moat at the business layer:
not a cheaper harness, but **the only on-demand, EPLAN-native, ~0-labor panel
factory.**

### 11.6 Immediate next builds (grounded)
1. **EPLAN → harnessRecipe importer** (parse a real Arkel connection/cable list;
   1004930 once the user provides its EPLAN/export).
2. **Nest generators for the real families**: Harting Han (rect multi-pin), WAGO
   261/221 block clamp, ferrule terminal block, ring-lug present — on the **6×6
   base**.
3. **Compiler Pass 6 → existing tester net file** (not the MATRIX bed).
4. Then the cell: Cartesian + Fairino + GPU camera on the 6×6 grid, ops 7→4→2.

---

*Code is the source of truth. `gpu-rental-orchestration`, `desktop/agent/robot/`,
and `firmware/yaver-companion` move under parallel sessions — re-grep before
building. Machinery/part facts are from the Talos ERP (Simkab); market figures
are 2025–2026 analyst estimates, cited inline.*
