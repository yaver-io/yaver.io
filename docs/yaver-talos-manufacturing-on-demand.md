# Yaver × Talos — Manufacturing-on-Demand ("Xometry for finished boxes")

> Deep analysis. Markdown drifts; the code is truth (see CLAUDE.md). This is a
> strategy + architecture document, grounded in capabilities that already exist
> in the repo (`machine/`, `robot/`, `netcapture/`, `machine_sync`→Talos,
> `support`/`guest`, the connector box in `hardware/yaver-connector-box/`).

## 0. One line

A customer **vibes a design and places an order via an MCP tool call**; it lands as
a job in **Talos** (the order + **machine-knowledge** plane); a **Yaver-driven
micro-factory cell** (3D print + outsourced PCB + CNC/laser + cabling + assembly +
test, **human-in-the-loop at gates**) makes it; the camera + `netcapture` verify
it; it ships back. The first product we make this way is the **connector box
itself** — dogfooding the line that the box then expands.

## 1. The wedge

- **Xometry / Protolabs** = single-process (CNC, injection, sheet), high-ish MOQ,
  not integrated. **PCBWay / JLCPCB** = PCB + some assembly + some print, but not
  *your-firmware + cabling + final test + enclosure assembly* as one order. **EMS
  contract manufacturers** = high MOQ, slow, no self-serve. **Nobody self-serves a
  low-volume finished electromechanical "box."**
- Yaver already turns a phone/agent into a generic machine driver
  (`machine`/`robot`/`netcapture` + the connector box). That's a **factory-OS cost
  advantage**: run a micro-factory on cheap machines + phones, AI-supervised,
  humans only at gates → far lower opex than a CNC shop. Same "lower dev opex"
  thesis (CLAUDE.md), applied to atoms.
- **Agent-native**: because ordering is an MCP tool, *an AI agent can order
  hardware the way it orders an API call*. "The manufacturing API for agents" is a
  category no incumbent occupies.

## 2. The two planes — Talos vs Yaver

This mirrors the existing machine-edge split (Yaver = heavy worker, Talos = thin
record/UI; see `ops_machine.go::machineSyncHandler`).

### Talos — the order + **machine-knowledge** plane (record/UI/brain-of-record)
- **Order/job plane:** customers, quotes, orders, **travelers** (ordered op
  routing), BOMs, QA results, billing, status, logistics.
- **Machine-knowledge plane (the moat):** a living catalog of *how to make things*:
  - **Machine catalog** — every machine onboarded (via a connector box), its type,
    work envelope, materials, tolerances, speeds, cost/hour.
  - **Learned schematics** — the `machineManuals` Yaver already syncs
    (register maps, labelled setpoints/live/counters). Extends to process recipes.
  - **Process recipes / programs** — slicer profiles, CAM programs, cabling
    sequences, test scripts, firmware images, keyed by (machine, material, part).
  - **DFM rules** — printability/tolerance/assembly constraints, learned from
    failures.
  - **Yield & telemetry history** — per machine/recipe scrap rate, cycle time,
    drift → feeds quoting and predictive maintenance.
  - **Data effect:** every job run makes the knowledge better → better quotes,
    higher yield, faster onboarding of the *next* machine. This is the defensible
    asset; the machines themselves are commodity.

### Yaver — the execution plane (heavy worker, on each machine)
- Drives each heterogeneous machine through the **connector box** facade
  (`robot` Marlin/G-code, `machine` Modbus, USB-serial, CAN).
- **`netcapture`** deep-analysis watches the wire for faults; the **camera +
  vision verdict** gates each critical motion/step (proven on `ananas` per
  `project_robot_screwdriver_cell`).
- Flashes firmware, runs the self-test fixtures, logs telemetry back to Talos via
  the `machine_sync` route pattern.
- The phone is the brain; the connector box is the facade (no PC on the floor).

## 3. The ordering interface — Yaver MCP (the demand side)

Ordering is **programmable**. Two verb families on the existing `ops` grand-tool
(`registerOpsVerb`, AllowGuest gated):

**Customer-side (place + track):**
| Verb | Purpose |
|---|---|
| `mfg_design_upload` | push design intent (STL/STEP, gerbers+BOM, wiring, firmware, test spec) → returns a design id |
| `mfg_quote` | deterministic quote (price + lead time + DFM report) for a design + qty + material |
| `mfg_order_create` | place an order from a quote (human or agent), returns order id + traveler |
| `mfg_order_status` | live status (per-op progress, QA, ship tracking) |
| `mfg_order_cancel` | cancel before a gated op |

**Cell-side (claim + execute) — runs on the manufacturing Yaver:**
| Verb | Purpose |
|---|---|
| `mfg_job_claim` | a cell claims the next dispatchable op for a machine it has |
| `mfg_op_start` / `mfg_op_verify` / `mfg_op_complete` | execute an op, camera/netcapture verify, report result |
| `mfg_op_gate` | request a human-in-loop sign-off at a gate |
| `mfg_telemetry` | stream machine telemetry + yield back to Talos (reuses `machine_sync`) |

**Agent-native flow (the demo that sells it):** in one Claude Code session a user
(or their agent) generates a CAD/PCB, calls `mfg_quote`, then `mfg_order_create` —
and a physical box arrives. The same MCP an agent uses to write code now makes
hardware. (Keep these owner/guest-scoped; orders cost real money + atoms.)

## 4. Order → ship pipeline (deep)

| Stage | Who | What | Gate? |
|---|---|---|---|
| 1. Vibe/design | customer (AI-assisted) | CAD/STL, PCB gerbers+BOM, wiring, firmware, **test spec** | — |
| 2. Quote + DFM | Talos + AI | auto-DFM, machine/material availability, price, lead time | human on edge cases |
| 3. Job/traveler | Talos | routing across machines, BOM, QA plan | — |
| 4. Dispatch | Yaver cell | per-op: load machine via connector box | **stock load** |
| 5. Execute+observe | Yaver | robot/machine drive + `netcapture` + camera verdict per step | auto; pause on fault |
| 6. QA | Yaver + box self-test | dimensional (vision+calipers), electrical (the box tests boxes), functional, firmware smoke | **first-article** |
| 7. Pack+ship | human + Talos | label, logistics, status to customer | **ship approval** |
| 8. Learn | Talos | yield/defects/cycle-time → improve DFM + recipes | — |

## 5. Machine integration map (what plugs into existing code)

| Process | How (MVP → later) | Code |
|---|---|---|
| FDM/SLA print | Marlin/G-code + camera verify (proven) | `robot/` |
| PCB | **outsource bare+SMT (JLC/PCBWay) for v1**; desktop pick-place+reflow later | cell receives + inspects |
| CNC / laser | G-code over the same serial path | `robot/` serial |
| Wire/cabling/crimp | the wire-harness machine (the original CST18D Modbus case) | `machine/` + `netcapture/` |
| Assembly + test | the **connector box is the test fixture** (PD/RS485/isolation) | `netcapture/` + a Yaver self-test script |
| Firmware flash | esptool/JTAG over USB via the box | Yaver op |

Realistic MVP: **outsource the PCB, do final assembly + test + firmware + enclosure
in-cell.** Don't build an SMT line on day one.

## 6. Quoting & DFM (the hard business part)

- **Deterministic cost model** (no LLM at price-time, mirroring `deploy all`
  philosophy): `machine_time + material + labor_touches + outsourced_parts +
  margin`. Machine-time estimated from the design (slicer for print, CAM for CNC,
  op-count for assembly) — that estimation can use AI/`mfg_quote`, but the price
  formula is deterministic and auditable. Pricing transparency is Xometry's moat;
  match it.
- **DFM gates** powered by the Talos knowledge plane: printability, min tolerance,
  assembly feasibility, test coverage. AI pre-checks; human signs off the unusual.

## 7. Human-in-the-loop model

Explicit **gates**; everything between gates is autonomous (AI + machine + camera).
Gates: stock load, first-article inspection, self-test review, final QA, ship.
One human supervises **N cells**. Yaver already has the remote-human primitives:
`support`/`guest` sessions for a remote expert, and the **XREAL/AR-glasses** remote-
support idea for "see what the operator sees." Labor scales sub-linearly with
volume — that's the margin story.

## 8. Dogfooding — the flywheel

The connector box is **both the first product and the tool that expands the line**:
- Each box you make lets you **onboard one more machine** (the facade that puts a
  machine under Yaver control). → self-replicating manufacturing capacity.
- Making the box exercises **every process** (print + PCB + THT + flash + test +
  assemble + QA + ship) on a real multi-process product **before** taking customer
  orders. It is the perfect end-to-end test of the whole pipeline.
- The box **tests boxes**: a finished box is the self-test fixture for the next
  (PD source, RS485 loopback, isolation). See `hardware/yaver-connector-box/TRAVELER.md`.

## 9. Competition / positioning

| Player | Gap we exploit |
|---|---|
| Xometry / Protolabs | single-process, not integrated, not low-volume finished boxes |
| JLCPCB / PCBWay | PCB-centric; no cabling + your-firmware + final test + enclosure as one order |
| EMS / CMs | high MOQ, slow, no self-serve, no agent API |
| **Yaver/Talos** | **self-serve, low-volume (1–100), integrated, AI-quoted, agent-orderable, run on a cheap AI-supervised micro-factory** |

## 10. MVP / staged plan

- **Phase 0 — dogfood:** one cell = 1 FDM printer + 1 bench + the connector-box
  self-test fixture + a phone. Outsource PCB. **Make 10 connector boxes
  end-to-end.** Prove traveler + QA + the Talos/Yaver job flow + `mfg_*` MCP.
- **Phase 1 — friends & family:** accept enclosure + simple PCB-carrier orders;
  manual quoting; Talos order portal + `mfg_order_create`.
- **Phase 2:** add CNC/laser + cabling cell; semi-auto DFM/quote; onboard more
  machines via more boxes.
- **Phase 3:** multi-cell, 1-human-N-cells, auto-DFM, logistics integration,
  open the agent-native ordering API.

## 11. Risks (honest)

- **Atoms are hard:** scheduling, scrap, machine maintenance, shipping damage, QA
  liability, inventory, space, capital. Margins thinner than SaaS — this is a
  **services business with a software moat**, not a software business. Stay
  capital-light early (one printer + outsourced PCB).
- **Product-liability / compliance** for anything you ship as a finished good
  (CE/FCC if it's an end product; the connector box notes this in its README).
- **Don't over-automate v1.** Human-in-loop is cheaper than perfect autonomy early;
  let the knowledge plane earn the automation.
- **Quote accuracy** is existential — a wrong quote eats the margin. Start
  conservative + human-reviewed; let yield history tighten it.

## 12. Concrete repo work this implies

- **Talos (separate repo):** `orders`/`jobs`/`travelers`/`machineCatalog`/
  `processRecipes`/`dfmRules`/`yield` schema + customer portal + quoting. Extends
  the existing `machineManuals`/`machineEdgeDevices`/`machineTelemetry` tables.
- **Yaver (`desktop/agent`):** an `ops_mfg.go` with the `mfg_*` verbs above; a
  **traveler/op runner** that executes an op via the right driver (`robot`/
  `machine`/`netcapture`) with gates + camera QA + telemetry sync (reuse
  `machineSyncHandler`'s POST pattern). A **`cell` concept** (a Yaver agent that
  advertises the machines it has and claims ops).
- **Connector box:** the self-test routine (firmware self-test + a Yaver QA
  script) — see `TRAVELER.md`.

Build order: dogfood the box (TRAVELER.md) → write `ops_mfg.go` job runner against
that single real traveler → Talos order schema/portal → open `mfg_*` to customers.
