# Yaver — Arm-Served Wire-Harness Cell (generic engine)

**Status:** 2026-06-08. The **public, domain-agnostic** engine for an automated
wire-harness cell. This doc describes only the generic machinery shipped in the
open-source repo — no customer, product, board, BOM, or pricing data lives here
(open CODE ≠ private DATA; the domain knowledge is the moat and stays private).

## Idea

Make the robot arm the **transfer system** a turnkey harness machine builds in
steel, and make every other operation a cheap **semiautomatic station** the arm
presents the cut lead end to (ferrule crimp, terminal press, seal, heatshrink
apply/heat, mark, terminal-block insert, connector insert, pull-test). One feed,
arm carries the lead through a ring of commodity stations, then routes it to the
board for termination. The engine is **data-driven**: a generic wire-list drives
the run; the wire-list's *contents* are supplied by the operator, never baked in.

## What's in the repo (all generic)

- **`desktop/agent/arm/`** — parametric arm layer (DOF/joints = data). Backends:
  Fairino (XML-RPC), myCobot, PAROL6, generic line-protocol, bridge, sim. Move-and-
  verify controller, teach-and-repeat (`arm_freedrive`/`arm_teach_capture`/
  `arm_program_*`), and **force/contact** (`arm/force.go`: `Wrench`, `ForceMove`,
  verbs `arm_wrench`/`arm_force_move`) for insert / seat-to-backstop / pull-test.
- **`desktop/agent/cell/`** — the cell engine:
  - `Station` + `Handshake` — a station = a `machine.Driver` (modbus trigger/done)
    or manual/timeout/vision handshake + taught present-pose(s).
  - `Orchestrator` — the deterministic **rendezvous state machine**
    (`APPROACH→PRESENT→SETTLE→TRIGGER→ACTUATING→VERIFY→WITHDRAW`) that owns
    pinch-safety: no trigger until the present pose is vision-verified; no withdraw
    until done (or e-stop). The LLM is never in this loop.
  - `CellProgram` — taught per-SKU station sequence + a `positionMap` (color →
    connector cavity) captured by demonstration when a source drawing omits routing.
  - `Job`/`WireRow`/`Lane`/`RoutePath` + `RunJob` — the **4-stage data-driven
    runner**: PREP (per-lane stations) → ROUTE (lay into the board) → TERMINATE
    (push-in seated automatically; screw + twin-ferrule flagged for an operator) →
    BUNDLE+TEST. `WireRow` is the wire-list **schema shape** only.
- **ops verbs** (`ops_cell.go`): `cell_station_add/list/get/delete/teach/test`,
  `cell_program_*`, `cell_job_save/list/get/delete/run`, `cell_status`.

## The automation boundary (generic)

`WireRow.AutoTerminable()` = push-in pole **and** single ferrule **and** no twin
partner → the arm seats it (`ForceInsert`). Screw poles and twin ferrules are
**flagged for an operator**, not faulted. The boundary is designed to move later
(add a screwdriver end-effector / a twin-ferrule pairing jig) without changing the
engine.

## Privacy / open-core boundary

- Taught arm poses, waypoints, and camera frames are **work-derived** → stay
  on-device (`~/.yaver/cell-programs`, `~/.yaver/cell-jobs`); never synced beyond
  bookkeeping summaries.
- The **wire-list contents**, board definitions, BOM, cost model, and any specific
  harness design are **DATA** that live in the operator's private knowledge plane,
  not in this repo. The engine consumes them at runtime; it does not contain them.

## Hardware-verify / remaining

`arm.Backend` force methods + Fairino positional args are `NEEDS-HARDWARE-VERIFY`.
Per-station IO (trigger relay across a pedal, done-sense), lead-in funnels, regrip
fixtures, and the cell-teach UI are integration work. Start with short, stiff leads
+ one-end processing (ferrule + push-in insert) to remove labor with the least
robot-programming risk; grow station coverage outward.
