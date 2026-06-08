# Yaver — Wire-Harness Assembly Jigs, Fixtures & Formboard (formalama) Design

**Status:** design-only, 2026-06-06. Branch `feat/yaver-robot-cell`.
**Builds on:** [robot-screwdriver-cell](robot-screwdriver-cell.md),
[yaver-fairino-cobot-cell-design](yaver-fairino-cobot-cell-design.md),
[yaver-robot-teach-motor-multicam](yaver-robot-teach-motor-multicam.md),
[robot-protocol](robot-protocol.md), [netcapture](../desktop/agent/netcapture/).
**Reuses, does not redesign:** `desktop/agent/robot/` (`Backend`,
`Controller`, `VerifyMotion`, `GstCamera`, `LineCompanion`, `Envelope`,
e-stop latch, encoder/torque cross-check), `firmware/yaver-companion`
(HX711 + INA219 + GPIO over serial), `hardware/yaver-connector-box`
(KF2EDGK terminals, ADM2587E isolation, force ADC), Talos
`machineRecipes`/`roboticsCommands` bridge.

---

## 0. TL;DR

We already automate **make a single motion + verify it with vision + force**.
A wire harness is just **a recipe of those motions** against **fixtures that
hold parts at poses the robot knows**. This doc designs the fixtures — the
**formboard (formalama)**, the **connector nests**, the **insertion/seat/pull
stations**, the **tie & test stations** — and the thin recipe layer that drives
them with the existing robot loop.

The wedge: harness shops still hand-build on dumb plywood formboards with a
paper drawing. We make the **same formboard self-aware** — it knows the
from/to list, lights the next step for a human, *or* lets a Fairino do the
insertion, *or* both — and it tests itself at the end. Cheap board first
(human + pick-to-light, ships day one), robot-tended later (Route B), same
recipe drives both.

**Division of labor between your two robots:**

| Robot | What it's good at | Harness role |
|---|---|---|
| **Fairino FR3/FR5** (6-DOF, wrist F/T, eye-in-hand cam) | dexterous, oriented, force-controlled | **contact insertion** into cavities, seat + pull-test, connector mating for test |
| **Ender-3 Cartesian** (3 linear axes, cheap) | linear sweeps, carrying a tool, top-down camera | **lead presenter/indexer**, **tie-gun carriage**, **top-down route-scan camera**, label/marker, **pick-to-light projector gantry** |

Reserve the cobot for the only genuinely 6-DOF task (insertion). Everything
linear is the $200 printer's job. This is the same "use the cheap axis where a
cheap axis suffices" split as the screwdriver cell.

---

## 1. The actual process we're fixturing

Upstream (already solved by your machines — **out of scope here**):

```
wire spool ──[JCW CST18D / CC36]──► finished LEADS in a bin
                                     (cut · stripped · crimped both ends ·
                                      heat-shrink marker / ink label)
```

Downstream — **this is the gap, this is the formboard's job**:

```
LEADS ─►(1) LAY-UP / DRESS  ─►(2) INSERT/LOAD ─►(3) BUNDLE ─►(4) TEST ─► harness
        route on the board     contact→cavity     cable-tie     continuity +
        following the drawing   OR ferrule→block   tape/wrap     pull + mating
```

Where the labor and the defects live:

| Step | Manual pain | Defect modes | Who automates it |
|---|---|---|---|
| (1) Lay-up | reading the drawing, holding floppy leads in place | wrong route, crossed branches | **formboard combs/forks** (passive) + top-down vision check |
| (2) Insert | aligning tiny contact to tiny cavity, feeling the click | **wrong cavity, unseated contact, bent pin, back-out** | **Fairino + nest + F/T + vision** (the hard part) |
| (3) Bundle | tensioning ties evenly, right spots | loose/over-tight ties, missed spot | tie station (T0 human → T2 Ender carriage) |
| (4) Test | plugging into a tester, reading meters | missed open/short, weak crimp passes | **self-test bed** + companion continuity + pull |

The "insertion" defects (wrong cavity / unseated / bent / back-out) are the
expensive ones — they pass visual and fail in the field. Vision + force gating
each insertion is precisely where Yaver's loop earns its keep.

> **Two insertion classes — fixture differs, loop is identical.** Your phrase
> "crimped cables → connector terminal block" spans both; we design both:
>
> - **A. Crimp-contact → housing cavity** (Molex/JST/Dupont/Mega-Fit/Mini-Fit
>   style): a male/female metal contact crimped on the wire is *pushed* into a
>   plastic housing until a **retention lance clicks**. Insertion is axial,
>   click is a force signature, retention is a pull-force spec.
> - **B. Ferrule/stripped wire → screw or spring terminal block** (KF2EDGK /
>   Phoenix / pluggable-terminal-block SBDK 5.08, the family already in
>   `hardware/yaver-connector-box`): wire end is *landed* in the cage, then the
>   **block clamps it** (screw torque or spring-lever push). Insertion is
>   axial-into-cage, retention is screw torque (you already do torque
>   termination in `robot/screw.go`) or lever-snap.

---

## 2. Fixture taxonomy

Five sub-fixtures. Each is a printed/parametric part on a common datum'd base.

```
                    FORMBOARD BASE  (datum'd, gridded, tag'd)
   ┌──────────────────────────────────────────────────────────┐
   │  [Nest A]══comb══fork══════comb═════[Nest B]              │   A,B,C = connector
   │     ║                                   ║                  │   nests (precision)
   │     ║          ┌── tie station ──┐      ║                  │
   │   clamp        │ (T0/T1/T2)      │    clamp                │   combs/forks/clamps
   │     ║          └─────────────────┘      ║                  │   = route guides
   │  [Nest C]═══════════════════════════════╝                 │   (passive)
   │                                                            │
   │   ◄ AprilTag (board pose)        M5 grid @25mm pitch ►     │   TEST BED docks
   └──────────────────────────────────────────────────────────┘   at board edge
        ▲ 3-2-1 kinematic datum (3 dowels) — fixes board→robot frame
```

### A. Formboard base (the formalama)
- **Carrier** for everything. 1:1 scale of the harness drawing.
- **Optical-breadboard pattern:** M5 threaded inserts (heat-set, brass) on a
  **25 mm grid**. Nests/combs bolt anywhere but only at quantized positions →
  every fixture's pose is *known by construction*, not measured.
- **3-2-1 kinematic datum:** 3 precision steel dowel pins (Ø6 h6) define the
  board origin in robot frame. Bolt the board to the robot table once →
  board→robot transform is fixed and repeatable to the dowel tolerance.
- **AprilTag/ArUco pocket** at a known grid cell: the Fairino eye-in-hand reads
  it each setup to *refine* board pose (catches re-clamp drift) — same camera +
  pipeline as `robot/camera.go`, new "register board" verb.
- **Board-ID QR** encodes the **harness part number** → recipe (§5).
- **Cable-management undertray** so the finished bundle lifts off cleanly.
- **Sizes:** start with a robot-reachable tile **~400×600 mm** (inside Fairino
  FR3 ~600 mm reach, and tileable for longer harnesses with re-registration).
- Parametric `formboard.scad` (matches existing `enclosure.scad`/
  `rs485_stick.scad` style): params = `tile_x, tile_y, grid_pitch=25,
  insert=M5, dowel_dia=6, tag_cell=[col,row]`.

### B. Route guides (passive — make the drawing physical)
The cheap magic of a formboard: leads *stay where you put them*.
- **Combs** — slotted walls the trunk routes through (one slot per branch).
- **Forks / fingers** — Y-shaped posts at branch breakouts hold leads up off
  the board so the bundle keeps 3D shape.
- **Finger clamps** — sprung TPU jaws that hold a lead end during lay-up.
- All grid-mounted, all printed (`comb.scad`, `fork.scad`, `clamp.scad`),
  positions come from the recipe so the **same board re-lays-out for a
  different harness** by moving guides to new grid cells (the agent can show
  *which* cells via pick-to-light, §6).

### C. Connector nests (precision — the linchpin)
This is the part that makes robot insertion possible. Per connector family, a
printed cradle that:
1. **Captures the housing** on its own anti-rotation/keying features (ribs,
   flats, latch ears) — zero ambiguity in orientation.
2. **Presents the cavity face** up and **tilted 15–30°** toward the robot for a
   straight, gravity-friendly insertion axis.
3. **Lead-in funnel per cavity:** a printed chamfer ring over each cavity mouth
   that forgives **±0.5 mm** lateral robot error; the F/T spiral search (§4)
   handles the last bit.
4. **Back-stop / reaction wall:** insertion force reacts into the fixture, so
   the robot isn't pushing the connector out of its own nest. Critical — a
   compliant nest = false "seated" readings.
5. **Optional compliance + TPA access:** a sliver of TPU under the housing lets
   the lance "click" register cleanly on the wrist F/T; cut-outs expose the
   terminal-position-assurance / CPA secondary lock for vision + a later push.
6. **Self-fiducial:** a printed dot/tag on the nest so vision indexes cavity
   numbers reliably regardless of board-level drift.
- Parametric `nest_grid.scad`: `rows, cols, cavity_pitch, cavity_dia,
  key_profile, tilt_deg, funnel_chamfer, foot_grid`. One generator → every
  rectangular-array housing. Odd housings get a hand-tweaked subclass.
- **For class B (terminal blocks):** the nest is simply a **DIN-rail / grid
  clamp** holding the block (KF2EDGK / Phoenix / SBDK 5.08) with the cage mouths
  presented; reuse the **screwdriver tool + torque termination** you already
  ship for the screw-clamp variant.

### D. Tie / bundle station
- **T0 (human, ships now):** printed tie-channel saddles at recipe-defined
  trunk stations; operator zip-ties; vision counts/locates ties for QC.
- **T1 (semi-auto):** a benchtop **auto-tension-and-cut head** (Hellermann
  Autotool-class) on a fixed post; robot/human presents the bundle, head
  tensions to spec and flush-cuts. Best effort/cost ratio.
- **T2 (robot, defer):** tie head on the **Ender carriage**, fed from a tie
  magazine, cinching at each station as it sweeps the trunk. High complexity;
  only if volume justifies.

### E. Self-test bed
- Counterpart connectors (the mating halves) wired into a **continuity matrix**.
- Driven by the **companion MCU** as a GPIO scan matrix (drive one pin, read
  which pins go high) → from/to verification = open/short/mis-wire detection.
  Reuse `LineCompanion` serial protocol; add a `MATRIX` command.
- **Pull-test** is done *during insertion* (§4), not here — better to catch a
  weak crimp before bundling.
- **Mating-force** check optional: Fairino plugs harness into the bed with
  capped Wrench; F/T trace flags high-insertion (bent pin) or low (missing
  detent).
- Bed docks at the board edge on the same grid; results → Talos as a test
  record (action + pass/fail + measured forces, **no wire content/paths** per
  the privacy contract).

---

## 3. End-effectors (Fairino tool flange)

Quick-change tool plate so the Fairino swaps tools mid-program (you already
treat the flange as swappable: screwdriver via `SetToolDO`):

| Tool | Fires via | Purpose |
|---|---|---|
| **Contact gripper** | tool-DO (pneumatic/servo) or companion GPIO | grip the **rigid crimped contact** (not the floppy wire), present tip forward; V-jaws locate on the wire-barrel shoulder |
| **Screwdriver** (existing) | `SetToolDO` + torque/`GetToolDI` | terminal-block screw termination, also any harness fasteners |
| **Mating adapter** | passive | plug harness connectors into the test bed under F/T |
| **Tie head** (T2) | companion GPIO | optional robot-side cinch |

Notes:
- **Grip the contact, not the wire.** Wires are non-rigid; the only reliable
  pick is on the metal contact's grip shoulder. The gripper jaws are a printed
  V with a hard insert; a contact "magazine/presenter" (the Ender's job, §lead
  presenter) feeds contacts tip-out so the gripper picks repeatably.
- **Lead presenter (Ender role):** instead of the cobot fishing in a bin (hard,
  6-DOF, slow), the **Ender moves a gripped lead** from the bin/feed to a fixed
  **handoff nest** at a known pose; Fairino picks from the handoff nest. Splits
  the bin-picking problem off the cobot entirely. Or: leads pre-loaded into a
  **lead comb/bandolier** (CST18D output can drop into a sequencing rack) and
  the Ender indexes the rack.

---

## 4. The insertion loop — maps 1:1 onto the existing stack

Contact-into-cavity insertion is **exactly** the move-and-verify pattern you
already run (`Controller.execute()` → snapshot → motion → M400 → readback →
vision verdict → cross-check → gate). It just runs on the Fairino `PoseBackend`
with force, and the "did it move" question becomes "did it **seat**".

```
per contact (from the recipe's from/to list):
 1. PICK         Fairino grips contact at handoff nest (Ender pre-presented it)
 2. APPROACH     MoveL free-space to a standoff pose above target cavity
                   (pose from nest grid-cell + cavity index, refined by tag)
 3. ALIGN+SEARCH  ForceMove along insertion axis with Wrench cap.
                   if lateral F exceeds thresh before axial progress →
                   spiral/lissajous admittance search (loop 1, deterministic,
                   no LLM) — the funnel + search absorb ±0.5 mm
 4. SEAT DETECT   watch Fz signature: rise → peak (lance deflect) → drop (snap).
                   deterministic gate on the peak-then-drop, OR companion HX711
                   tooltip force. NO LLM in this gate.
 5. VISION VERIFY eye-in-hand: "is the contact shoulder flush with the housing
                   face? wire straight? correct cavity (N)? any adjacent skip?"
                   → Qwen3-VL-8B / Gemini Flash via VerifyMotion → Verdict
                   gates step (loop 2). reuse robot/verify.go shape verbatim.
 6. PULL-TEST     retract with capped Wrench (pull to spec, e.g. 20–40 N).
                   contact backs out below spec → bad crimp / unseated → REJECT.
                   this is DriveScrew's torque-termination pattern as a linear
                   pull; force from wrist F/T or companion.
 7. CROSS-CHECK   deterministic: commanded cavity == vision-read cavity AND
                   seat-force-signature ok AND pull-force ≥ spec → PASS.
                   any disagree → e-stop latch / BT recovery (loop 3).
```

Failure → **loop 3 (BT + LLM supervisor)**, identical to the cell design:
bent-pin / wrong-cavity / stuck → LLM proposes recovery (retry with offset,
skip+flag, summon human via pick-to-light), bounded by the same hard numeric
schema + deterministic gate. **Vision never moves the robot; it only gates.**

Terminal-block (class B) variant: steps 3–4 become "land wire in cage" +
"drive screw to torque" (existing `robot/screw.go`) or "press spring lever";
pull-test still applies. Same skeleton.

### Force thresholds (starting points, tune per connector)
| Quantity | Start value | Source |
|---|---|---|
| Axial insertion cap | 30 N | wrist F/T or HX711 |
| Lateral search trigger | 5 N | wrist F/T |
| Seat "click" signature | peak ≥ 8 N then ≥30% drop | F/T Fz trace |
| Pull-test spec | 20–40 N (connector datasheet) | wrist F/T / companion |
| Screw torque (class B) | per block (e.g. 0.4 N·m) | `screw.go` / companion KT |

---

## 5. Recipe & software layer (thin — reuse everything)

A harness is a **recipe** in the same shape as `machineRecipes`/Talos config.
Work-derived (from/to lists, customer part numbers) → lives in the **user's
Talos org**, per the privacy split already established
([yaver-robot-teach-motor-multicam](yaver-robot-teach-motor-multicam.md));
only a capability + program **index** lives in Yaver for discovery.

```jsonc
// harnessRecipe (Talos) — analogous to machineRecipes
{
  "partNumber": "HARN-0042",
  "board":   { "tile": "400x600", "tagCell": [1,1], "datum": "3-2-1-A" },
  "connectors": [
    { "ref": "A", "family": "molex_megafit_6", "nest": "nest_grid_2x3",
      "gridCell": [3,7], "rotDeg": 90, "pullSpecN": 30 },
    { "ref": "C", "family": "kf2edgk_3p", "nest": "block_clamp",
      "gridCell": [3,1], "rotDeg": 0, "termTorqueNmm": 400 }
  ],
  "fromTo": [          // the circuit list — the source of truth
    { "lead": "L01", "from": "A:1", "to": "C:1", "color": "red"  },
    { "lead": "L02", "from": "A:2", "to": "C:2", "color": "blk"  }
  ],
  "ties":  [ { "station": "T1", "gridCell": [3,4], "tensionN": 80 } ],
  "guides":[ { "type": "comb", "gridCell": [3,5], "slots": 6 } ],
  "tests": [ { "type": "continuity" }, { "type": "mating", "maxN": 40 } ]
}
```

New ops verbs, registered the same self-registering, mesh-routable,
`AllowGuest=false` way as the existing `robot_*` (extend, don't fork):

| Verb | Motion? | Does |
|---|---|---|
| `harness_load_recipe{partNumber}` | no | pull recipe from Talos, validate against connected tools/cams |
| `harness_register_board` | yes (cam only) | eye-in-hand reads AprilTag → board→robot transform |
| `harness_present_lead{lead}` | yes (Ender) | Ender indexes lead to handoff nest |
| `harness_insert{lead}` | yes (Fairino) | the §4 loop for one from/to entry |
| `harness_run{partNumber}` | yes | full sequence: present→insert→verify→tie→test, step-gated like `RunProgram` |
| `harness_tie{station}` | yes | T0 prompt / T1 present / T2 cinch |
| `harness_test{type}` | no motion (or mate) | companion continuity matrix / pull / mating-force |
| `harness_status` / `robot_estop` / `robot_reset` | — | reuse existing |

Reused verbatim from `desktop/agent/robot/`:
- `Controller` orchestration + `Envelope` soft-limits + **e-stop latch** +
  encoder/torque **cross-check**.
- `VerifyMotion(ctx, vc, before, after, expectation) → Verdict` — the vision
  gate; multi-cam variant (eye-in-hand seat + Ender top-down route) per
  [teach-motor-multicam](yaver-robot-teach-motor-multicam.md).
- `LineCompanion` (HX711 force, INA219 current, GPIO) — pull-test force, gripper
  fire, **+ new `MATRIX` command** for continuity scan.
- `GstCamera` / phone Camera2 — frames; mesh snapshot URL for the fleet UI.
- Talos bridge (`roboticsCommands`/`machineEdgeCommands`) — durable+audited path
  identical to robot-protocol; direct mesh for low-latency jog/teach.

**Privacy:** test records and audit summaries to Convex carry action +
pass/fail + measured forces only — **never** the from/to list, customer part
content, or filesystem paths (enforced by `convex_privacy_test.go`; add
harness payload fields to `fieldsWeForbidInAnyConvexPayload` if any new sync
path touches them).

---

## 6. Pick-to-light — the human-first feature that ships before any robot

Before a single robot move, the *self-aware formboard* already beats plywood:

- A **top-down LED/laser or pico-projector on the Ender gantry** (or a fixed
  arm) the agent drives to **point at the next nest/route/tie station** from the
  recipe — light-directed assembly. Operator just follows the dot.
- Top-down camera + LLM **checks lead color vs the from/to list** before the
  operator ties ("L02 should be black into C:2 — confirm") — the same
  `VerifyMotion` vision call, expectation from recipe.
- Continuity self-test at the end gives an instant pass/fail with the
  mis-wired pair called out.

This is a real product on a $200 printer + a webcam + a light, with **zero
cobot** — and it's the on-ramp that makes the Fairino insertion (Route B)
a drop-in upgrade on the *same board and same recipe*. (Same "Route A ships
first" staging as the Fairino cell doc.)

---

## 7. Bill of materials (fixtures — reuses repo blocks)

| Item | Part / source | Note |
|---|---|---|
| Formboard tile | 18 mm ACM / aluminum tooling plate or printed PETG ribs | grid + dowels |
| Grid inserts | M5 brass heat-set, 25 mm pitch | optical-breadboard pattern |
| Datum | 3× Ø6 h6 dowel + 2× M6 clamp | 3-2-1 kinematic |
| AprilTag | printed 36h11, ~40 mm | eye-in-hand registers board |
| Nests / combs / forks | printed PETG/ASA (`*.scad`) | matches existing enclosure style |
| Contact gripper | servo/pneumatic micro-gripper + printed V-jaws | tool-flange DO |
| Force tooltip (opt.) | **HX711 + load cell** | already in `firmware/yaver-companion` |
| Continuity matrix | **companion MCU GPIO** + mating connectors | new `MATRIX` cmd |
| Terminals (class B) | **KF2EDGK / SBDK 5.08** | already in `yaver-connector-box` BOM |
| Tie head (T1) | Hellermann Autotool-class, fixed post | off-the-shelf |
| Camera(s) | UVC webcam (top-down) + Fairino eye-in-hand | `GstCamera` |
| Pico-projector / laser (opt.) | any HDMI/USB pico or line laser on Ender | pick-to-light |

---

## 8. Roadmap (staged, cheap-first)

- **P0 — Self-aware formboard (human, no robot).** `formboard.scad` +
  `nest_grid.scad` + recipe schema + `harness_load_recipe`/`harness_test` +
  top-down cam color-check + continuity matrix on the companion. Ships value on
  a plywood-replacement board immediately.
- **P1 — Pick-to-light.** Ender-gantry light/projector + `harness_present_lead`
  + route/tie guidance. Still human insertion.
- **P2 — Fairino insertion (class B first: terminal blocks).** Reuse
  screwdriver + torque termination; `harness_register_board` + `harness_insert`
  for ferrule/screw-cage landing. Lowest-risk robot step (no tiny lances).
- **P3 — Fairino contact-into-housing (class A).** Contact gripper + F/T spiral
  search + seat signature + pull-test + vision cavity-index. The hard one.
- **P4 — Ender lead presenter + handoff nest.** Take bin-picking off the cobot.
- **P5 — Robot tie (T2) + mating self-test + Talos manufacturing-on-demand
  hook** ([yaver-talos-manufacturing-on-demand](yaver-talos-manufacturing-on-demand.md)):
  order a harness by part number → recipe → cell builds + self-tests + records.

---

## 9. Open questions / assumptions to confirm

1. **Connector family/families** for the first real harness — drives the first
   `nest_grid.scad` parameters and pull-force specs. (Assumed: a rectangular
   crimp housing for class A + KF2EDGK/SBDK 5.08 for class B, since the latter
   is already in the repo.)
2. **Lead source format** — does the CST18D/CC36 drop leads into a sequencing
   rack/bandolier (great — Ender indexes it) or a loose bin (needs the presenter
   + possibly vision bin-pick)?
3. **Are leads single- or double-ended crimped** coming off the machine, and is
   one end a contact (housing) vs a ferrule (terminal block)? Sets whether a
   pick handles a contact or a wire.
4. **Volume** — sets how far up the tie automation (T0→T2) and presenter
   automation are worth pushing.
5. **Fairino model** (FR3 vs FR5) and whether the **wrist F/T** is purchased —
   F/T makes seat-detect + pull-test deterministic; without it we lean harder on
   companion HX711 + vision (workable, slightly less crisp).

---

---

## 10. Full lights-out automation + innovative robot-native harness boards

§1–9 stage a human board up to a robot-tended one. This section is the **end
state**: a closed-loop **harness cell** that runs unattended, and the board
**innovations** that make a dumb plywood formboard obsolete. The theme: the
board stops being a passive drawing and becomes an **active, self-describing,
self-reconfiguring peripheral** the agent drives like any other machine.

### 10.1 The cell (lights-out flow)

```
 CST18D/CC36 ─► LEAD BUFFER ─► [Ender presenter] ─► [Fairino insert] ─► [tie] ─► [test] ─► UNLOAD
 (finished       sequencing      pick from rack       §4 loop on the      auto-     in-board    pass→bin
  leads)         rack/bandolier  → handoff nest       active board        tension   continuity  fail→rework
                      │                                     │                          │           │
                      └──────────── agent (BT supervisor, loops 1/2/3) orchestrates all ──────────┘
                                    recipe = single source of truth · every step vision+force gated
```

One agent owns the cell. Each station is an ops verb; the **behavior-tree
supervisor** (loop 3) sequences them, retries, and escalates to a human *only*
on unrecoverable faults — exactly the `RunProgram` step-gating you already have,
scaled to a line. Throughput scales by adding cells (each a `deviceId` on the
mesh — [yaver-robot-fleet-mesh-design](yaver-robot-fleet-mesh-design.md)), not
by adding operators.

### 10.2 Innovative boards (ranked by leverage × buildability)

**① Self-describing nests on an active grid — "plug-and-play fixtures."**
The grid isn't just threaded holes; each cell carries **power + a 1-wire/I²C
bus via pogo pads**. Every nest/comb has a tiny **EEPROM (e.g. DS28E07)** storing
`{type, cavity-array, key-orientation, pull-spec}`. Drop a nest anywhere →
it powers up, the agent reads its identity *and the grid cell it's sitting on*
→ **the board map builds itself**. No manual layout entry, no "is nest A really
at (3,7)?" drift. Changeover = rearrange nests; the board re-describes itself.
This is the most Yaver-native idea — fixtures that **onboard themselves** the
way `netcapture`/provisioning onboard machines. Buildable now (1-wire EEPROM +
pogo grid is cheap; reuse the connector-box's `+5V` rail + I²C patterns).

**② Robot-reconfigured universal board — zero-changeover.**
A dense grid of **bistable / magnetic-foot posts** (combs, forks, finger
clamps) that **the Fairino itself places** from the recipe. New harness =
robot tears down and rebuilds its own route guides in minutes; the board is a
*blank universal substrate*. Combine with ① so each placed post self-reports.
Kills the per-harness tooling cost that makes low-volume harnesses uneconomic —
which is precisely where on-demand manufacturing
([yaver-talos-manufacturing-on-demand](yaver-talos-manufacturing-on-demand.md))
wants to play. Higher build effort (post mechanism + magnetic grid); huge payoff.

**③ Indexing / tilting board (rotary or 2-axis tilt stage).**
Mount the board on a turntable or tilt cradle so it **presents each connector
face normal to the insertion axis** and brings far nests into the cobot's sweet
spot. Turns a 6-DOF reach problem into "rotate the work, insert straight down" —
lets an **FR3 (smaller/cheaper) cover a board that would otherwise need an FR5**,
and even lets the **Ender's 3 axes** do more inserts (board orients, printer
pushes straight). The board becomes a coordinated **7th/8th axis** the agent
drives (an extra serial/Modbus axis — reuse the machine engine).

**④ Active nests (the board participates in insertion).**
Nests with **motorized funnel gates, retractable retention catches, and
self-clamping fingers** (micro-servo/solenoid, fired over the same companion
GPIO/I²C). The nest *closes around* the contact after the robot seats it, and
**actuates the TPA/CPA secondary lock itself** — so the robot only has to get
the contact *near* seated; the board finishes and confirms. Dramatically relaxes
robot precision → cheaper arm, faster cycle, fewer back-outs.

**⑤ In-board self-test (test bed built into the substrate).**
Continuity rails run **under the grid**; mating counterparts live in the nests.
The harness is **tested in-situ on the same board it's built on** — no transfer
to a separate tester, no re-fixturing. The companion `MATRIX` scan + Fairino
mating-force trace produce the pass/fail before the harness ever leaves the
board. Pairs with ① (nests already on the bus).

**⑥ Compliant (RCC) nests for cheap-arm insertion.**
Build **remote-center-compliance** into the nest (or a passive RCC tooltip):
lateral/angular float centered at the contact tip so misalignment self-corrects
mechanically during push. Lets a **low-repeatability or low-cost arm** (or the
Ender) do class-A inserts that would otherwise demand the Fairino's ±0.02 mm.
Pure mechanical, no electronics — cheapest path to "any arm works."

**⑦ Pallet-flow boards (mini-line).**
Boards are **pallets** that flow station-to-station (lay-up → insert → tie →
test → unload), each carrying its recipe on its tag (①). A single robot serves
multiple pallets; stations specialize. This is the scale-up topology once one
cell is proven — and each pallet is just another tagged board the agent already
knows how to register.

### 10.3 Why this is a Yaver feature, not a one-off rig

Every innovation above reduces to **"a fixture that describes/reconfigures/tests
itself and is driven by the agent through existing verbs"** — the same posture
as `yaver-for-machines` (view/watch/control any machine) applied to *tooling*.
The board joins the mesh, self-onboards (①), is recipe-driven (§5), vision+force
gated (§4), fault-supervised (loop 3), and on-demand-orderable (P5). No new
control plane — the harness cell is **one more machine class** on the stack you
already shipped. That's the moat: not the jig, but that the jig is *native* to a
fleet-of-machines agent that already does provisioning, recovery, vision verify,
and order→recipe→job.

### 10.4 Suggested first innovative build

Smallest step that proves the thesis: **① self-describing nests on a pogo grid +
⑤ in-board continuity**, on a **③ single-axis turntable**. That's a board that
(a) builds its own map when you drop nests on it, (b) rotates to present faces to
an FR3, and (c) tests the finished harness without moving it — all on the
existing companion + machine-engine + vision stack. ② and ④ (universal
self-reconfiguring + active nests) are the follow-on once that's real.

---

---

## 11. Real machinery inventory + the TWO robot roles

Grounded in the the-partner-ERP shop (Talos ERP + machine specs). The robotics job is
**not just assembly** — it's also **tending the existing semi-auto crimpers** so
they run lights-out. Two roles, one robot stack.

### 11.1 Machine inventory (what to feed)

| Machine | Function | Auto level | Robot opportunity |
|---|---|---|---|
| **Schleuniger CrimpCenter 36 S/SP** | fully-auto crimp (Komax, closed) | **full** | feed cut wire in / take leads out (front/back tending) |
| **JCWELEC CST18D** | fully-auto **dual-end** cut/strip/crimp/seal, 1–2k pcs/h, AWG22–14 | **full** | outfeed → kitting rack |
| **Yuanhan YH-8030H** | auto cut+strip, 100 programs | **full** | outfeed → kitting |
| **JCW-2TP** | **semi-auto multi-conductor** cable strip+crimp (servo + ballscrew, strip ≤18 mm, free core count) | **semi — operator-fed** | **TEND**: present each core to the die |
| **JCW-F3** | **semi-auto** strip + **ferrule / Deutsch solid-contact / pre-insulated tubular** crimp, 0.35–6 mm², loose bootlace ≤12 mm, ~2.5 s/cycle, quad/hex | **semi — operator-fed** | **TEND**: present stripped end to depth, trigger |
| Hand presses (Artos / 2TP / Hatko) | manual single crimp | **manual** | **TEND** or arm-replace |
| Electrical tester | continuity / hipot / pull | manual load | **TEND**: robot mates harness to tester |

So the autos (CC36/CST18D/YH) already do op 1+3 for their cases; the **semi-autos
(2TP, F3, hand presses) still burn an operator each** — and that operator is what
a tending robot removes.

### 11.2 Robot role A — machine tending (semi-auto → lights-out)

Convert each operator-fed crimper to unattended with a **printed tending fixture +
a magazine + an arm** — *no new crimping machine bought.*

- **Magazine / infeed:** the robot handles **wire**; the **machine keeps feeding
  the terminal** (its own reel / vibratory bowl — e.g. F16-class bowl, F3 loose
  ferrule track). Don't reinvent terminal feed. Wire blanks come from the
  auto-cutter as a **bandolier / sequencing rack** (the kitting output), and the
  arm picks from the rack — not a bin.
- **Die-zone presentation fixture (printed, per machine):** locates the crimp
  anvil/applicator zone, a **wire stop at the correct insertion depth** (F3 wants
  the stripped end to a set depth; 2TP wants each core presented square), a
  guide funnel. The arm brings the wire end to this fixed pose; the fixture +
  F/T set depth.
- **Trigger:** the machine's foot-pedal / cycle input fired via **companion
  GPIO** (or the machine's own IO) once the wire is seated.
- **Outfeed:** finished lead → a **chute → kit/sequencing rack** for assembly
  (role B). Vision verifies the crimp (op 9) before it drops.
- **Reuse:** `robot.Controller` + F/T/HX711 (depth + crimp confirm), companion
  GPIO (pedal), `VerifyMotion` (crimp QC), e-stop latch, mesh.
- **Safety (non-negotiable):** crimp dies are **pinch/amputation hazards** — the
  tending robot runs behind a light curtain / guard with the existing latched
  e-stop; this is exactly where "human out, machine in" pays for itself.
- **Layout:** one arm in the center can tend **several crimpers in an arc** +
  drop to the kit rack — a tending island.

This removes the operators on ops **2 (splice/join crimp)** and **3 (the
off-machine ferrule/terminal crimp)** and produces a clean, sequenced kit.

### 11.3 Robot role B — harness assembly (the rest of this doc)

On the **6×6 reusable grid base + custom printed jig** (compiler-generated):
insert (op 2 contacts), terminal-block screw (op 4, reuse `screw.go`), heat-shrink
place+heat (op 5), **form/route/tie (op 7 — the #1 sink)**, mate-to-tester (op 8),
GPU-vision (op 9). Cartesian does the linear work (present / tie / heat-gun /
pick-to-light); Fairino does the dexterous work (insert / screw / mate). See
thesis §11.2 for the op→robot map and §1–10
above for the fixtures.

### 11.4 The whole cell

```
auto cut/crimp (CC36·CST18D·YH) ─┐
                                 ├─► KIT RACK ─► role B: 6×6 assembly cell ─► tester ─► pack
semi-auto crimp (2TP·F3·press) ──┘   (sequenced     (insert·screw·shrink·form·tie·verify)
   ▲ role A: arm + magazine + tending fixture        ▲ Cartesian + Fairino + GPU cam, custom jig
   └─ converts semi-auto → lights-out                └─ stations merged, portable reusable base
```

Both roles = the same Yaver robot stack, the same compiler-emitted fixtures, the
same mesh. Role A feeds Role B. Together = **0 labor after cut/crimp.**

---

*Code is the source of truth. Before building any of this, re-grep
`desktop/agent/robot/` and `firmware/yaver-companion` — they move under
parallel sessions.*
