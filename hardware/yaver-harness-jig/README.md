# Yaver Harness Jig — P0 self-aware formboard + self-test bed

The first **buildable** slice of the wire-harness automation thesis
([docs/yaver-harness-automation-thesis.md](../../docs/yaver-harness-automation-thesis.md)):
a 3D-printed, datum'd, **self-aware formboard** plus a **100% inline self-test
bed** — value on day one with **no robot**, and the on-ramp for the robot-tended
cell ([mechanical design](../../docs/yaver-wire-harness-jig-formboard-design.md),
[compiler](../../docs/yaver-harness-compiler-design.md)).

Why this first: it's the cheapest, lowest-risk step and it's an immediate edge —
incumbents ship harnesses tested by eye; we test **every contact, every net,
inline.** It also proves the compiler emits a usable jig + recipe.

## What's here

| File | What | Build |
|---|---|---|
| `formboard.scad` | parametric base tile: M5 grid @25 mm + 3-2-1 dowel datum + AprilTag pocket + board-ID QR pocket | `openscad -o board.stl -D PART='"board"' formboard.scad` → print PETG/ASA |
| `nest_grid.scad` | parametric connector nest: flat gridded baseplate + keyed housing cradle + chamfered lead-in + self-fiducial | set `cav_rows/cols/pitch`, `foot_cells`, then `openscad -o nest.stl nest_grid.scad` |
| `harness-recipe.schema.json` | the per-SKU `harnessRecipe` contract (compiler input / cell input) | JSON Schema 2020-12 |
| `example-harn-0042.json` | a worked recipe (2×3 Mega-Fit ↔ KF2EDGK-3P, 3 leads) | validates against the schema |

Firmware lives at [`../../firmware/yaver-companion/yaver_companion.ino`](../../firmware/yaver-companion/yaver_companion.ino)
— extended with the `MATRIX` continuity-scan command (the self-test bed).

## Self-test bed (the day-one win)

Wire each harness test point — through the **mating connectors** — to one
companion MCU pin. Then, per `harnessRecipe.tests[].pinMap`:

```
host → MATRIXPINS 2 3 4 5 6 7      # declare the test-point pins (index order = pinMap keys)
MCU  ← OK 6
host → MATRIX                       # scan
MCU  ← MX 0 1                       #   pin[0] (A:1) connects to pin[1] (C:1)   ✓ expected
MCU  ← MX 1 0
MCU  ← MX 2 3                       #   A:2 ↔ C:2  ✓
MCU  ← MX 3 2
MCU  ← MX 4                         #   A:3 has NO partner  → OPEN (lead L03 bad/unseated) ✗
MCU  ← MX 5
MCU  ← MX DONE
```

The host builds the adjacency from the `MX` lines, maps indices→nets via
`pinMap`, and diffs against `fromTo`:
- **missing expected pair** → open (bad crimp / unseated contact)
- **unexpected pair** → short / mis-wire
- **wrong partner** → swapped cavity

100% coverage, no instruments, ~$5 of MCU. Mechanism is portable (uses only
`INPUT_PULLUP` — works on AVR/RP2040/ESP32): drive one pin LOW, the connected
pins read LOW.

> **Pull-test** (contact retention) happens during insertion in the robot phase,
> not here — catch a weak crimp before bundling. **Mating-force** is the Fairino
> F/T phase. P0 is continuity + the digital formboard.

## Digital formboard (also no robot)

- Print the board; press M5 brass inserts on the grid; press 3 Ø6 dowels for the
  datum; drop a printed AprilTag in the pocket and a board-ID QR label.
- Print nests per the recipe's connectors; bolt them at their `gridCell`s.
- A top-down webcam + the recipe drive **pick-to-light** + color/route checks
  (reuses `robot/verify.go` vision) — the operator follows the dot, the agent
  confirms each lead vs `fromTo` before tie.

## Next

`block_clamp.scad` (terminal-block nest, reuses the screwdriver + torque
termination), `comb.scad`/`fork.scad` (route guides), and the compiler
(`docs/yaver-harness-compiler-design.md`) that emits all of the above from a
`harnessRecipe` automatically — so a new SKU is *print + load recipe*, zero
fixture engineering.

*SCAD render-checked with OpenSCAD (both parts manifold, NoError). Firmware is
not hardware-flashed yet — bench-verify the `MATRIX` timing/settle on your MCU.*
