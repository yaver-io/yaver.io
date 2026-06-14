# Circuit Simulation as a Cross-Repo Service (Talos + OCPP/Kalkan)

Status: **deep analysis / design** — 2026-06-14. Not yet built. The circuit cell
itself ships today (`desktop/agent/circuit/`); this doc is about making it a
*service two other repos consume*, and the gaps that surface when they do.

> Read `CLAUDE.md` first rule: code is truth. Every claim here is grounded in a
> file path. If a path moved, the claim is the bug — fix it here in the same change.

---

## 0. TL;DR

The circuit cell **already exists and is wired across all four surfaces** the user
asked for:

| Surface | Where | Status |
|---|---|---|
| Go agent | `desktop/agent/circuit/` (MNA solver op/dc/tran/ac + ngspice + ERC + KiCad/EPLAN import + PNG plot) | **Shipped** |
| MCP | `ops` grand-tool verbs `circuit_*` + first-class `circuit_plot` image tool (`mcp_tools.go:2923`) | **Shipped** |
| Web | `web/components/dashboard/CircuitCellView.tsx` @ `/dashboard/circuit` | **Shipped** |
| Mobile | `mobile/app/circuit.tsx` + `mobile/src/lib/circuitClient.ts` | **Shipped** |

So "add support" is the wrong framing. The real work is: **turn an internal cell
into a service that Talos and OCPP actually call, and close the gaps that only
appear once a real consumer pushes a real design through it.**

The two consumers want *different halves* of the cell:

- **OCPP/Kalkan** wants the **analog solver half** (transient/AC of an EV-charger
  safety PCB) + **safety ERC**. Today it uses **KiCad's own ERC** (`gridpilot-erc.rpt`)
  and has **zero** yaver integration.
- **Talos** wants the **connectivity/ERC half** (EPLAN → voltage-domain ERC →
  harness continuity test programs). It barely needs the analog solver at all.

Neither can use the cell today without new glue. Details below.

---

## 1. What ships today (the cell)

Grounded inventory of `desktop/agent/circuit/`:

- `types.go` — `ElementKind` (R,C,L,V,I,D, E=VCVS, G=VCCS, **W=KindConnection** for
  pure connectivity), `Waveform` (dc/sine/pulse), `Netlist{Elements, Directives,
  NodeDomains map[string]float64, Source}`, `Analysis{op|tran|ac|dc}`, `SimResult`,
  `CircuitInfo`.
- `solver.go` — pure-Go **Modified Nodal Analysis**: `op()`, `dcSweep()`, `tran()`
  (trapezoidal companion models for L/C), `ac()` (complex MNA, log sweep), diodes
  via Newton-Raphson. Dense Gaussian elimination, real + complex.
- `backend.go` / `backend_ngspice.go` — `Backend` interface; builtin MNA + optional
  ngspice pass-through (adds Q/M/X — BJT/MOSFET/subckt).
- `netlist.go` — lenient SPICE parser/emitter, engineering-suffix value parsing.
- `import_kicad.go` — **`(export …)` s-expr netlist** (Eeschema "Export Netlist"),
  NOT `.kicad_sch` source. ⚠️ load-bearing for OCPP (see §3).
- `import_eplan.go` — `ParseConnectionList`: wirelist (connector:pin → connector:pin)
  or net-graph CSV; optional `voltage` column → `NodeDomains`.
- `erc.go` — `RunERC`: (1) no-ground, (2) dangling-net, (3) shorted-source,
  (4) **voltage-domain-mismatch** (two nets with different `NodeDomains` joined by a
  non-isolating R/L/W; C/E/G treated as intentional bridges), (5) no-dc-path-to-ground.
  **Data-driven** — new domains need data, not code.
- `plot.go` — dependency-free PNG (stdlib `image/png`).
- `controller.go` — process-wide controller: `Import`, `SetDomain`, `Simulate`,
  `ERC`, `Plot`, `ExportSPICE`, `Engines`.
- `ops_circuit.go` — verbs `circuit_engines`, `circuit_config_get/set`,
  `circuit_import`, `circuit_export`, `circuit_describe`, `circuit_simulate`,
  `circuit_measure`, `circuit_erc`, `circuit_set_domain`, `circuit_plot`.
  Netlist persists **vault-local** (`project:"circuit"`), never Convex.

The single most important architectural fact for this doc: **`KindConnection` +
`NodeDomains` + the data-driven ERC** are a *connectivity verification engine* that
happens to live next to a SPICE solver. Talos wants that engine. OCPP wants the SPICE.

---

## 2. How a sibling repo actually consumes it (the integration surface)

This is the part nobody has built and the user's literal ask ("`../talos` and
`../ocpp` … will use it").

The agent exposes circuit two ways over MCP:

1. **`ops` grand-tool** (`mcp_tools.go:2852`) — `ops {verb:"circuit_import", payload:{…}, machine:"…"}`.
   Discover the verb list + schemas via `ops_verbs`. This is how `circuit_import`,
   `circuit_erc`, `circuit_simulate`, `circuit_measure`, `circuit_export`,
   `circuit_set_domain` are reached. **They are NOT individual MCP tools.**
2. **`circuit_plot`** (`mcp_tools.go:2923`) — the one first-class circuit tool,
   because it returns a *viewable image block* (the host model needs to SEE the curve).

So an agent running inside `../ocpp` or `../talos` consumes circuit by:

- adding the yaver MCP server to that repo's `.mcp.json` / agent config, then
- calling `ops` with `circuit_*` verbs, and `circuit_plot` for pictures,
- optionally targeting a remote box via `machine` (the design can be simulated on
  whatever device holds the netlist / has ngspice).

**Gap I-1 — no library / CLI path.** Today consumption requires the MCP server be
live and the calling agent be a yaver MCP client. Talos's *deterministic compiler*
(non-agentic Go/TS code) cannot call `ops` — it has no model in the loop. It needs
either (a) a plain HTTP endpoint (`POST /ops {verb:"circuit_erc"}` already exists in
`httpserver.go` — confirm it is reachable from Talos's network position), or (b) the
circuit package vendored/imported as a Go module. **Recommendation:** lean on the
existing `/ops` HTTP endpoint — Talos's Pi/edge already speaks HTTP to the agent;
no new transport. Document the request/response contract as a stable mini-API.

**Gap I-2 — netlist is process-global, single-slot.** `ensureCircuit()` holds *one*
controller with *one* loaded netlist persisted to a single vault entry. Two repos
(or two panels) simulating concurrently stomp each other. For a service, the verbs
need an optional `design` / `id` parameter selecting a named netlist slot (still
vault-local, keyed `circuit/<id>`), defaulting to the current single slot for back-compat.

---

## 3. Consumer A — OCPP / Kalkan (the analog half)

**What it is:** an EV-charging load-balancer for apartment buildings. Custom safety
PCB ("GridPilot"): ESP32-S3 main + STM32G0 safety co-processor, forced-guided
relays AND-gated by both MCUs, IEC 61851 CP pilot (±12 V, 1 kHz PWM), 3× SCT-013
CT clamps → ADS1115, ZCT 20 mA GFCI, Bender RCMA420 DC leakage, NTC thermal cutoff,
RS485 multi-node. Files: `ocpp/hardware/kicad/*.kicad_sch`, `ocpp/hardware/SCHEMATIC_SAFETY.md`,
`ocpp/hardware/netlist/gridpilot_netlist.py` (SKiDL), `ocpp/firmware*/`.

**Today:** uses **KiCad 10 native ERC** (`ocpp/gridpilot-erc.rpt`, mostly off-grid
cosmetics). **No yaver integration, no SPICE, no transient analysis.**

### What it could consume from the cell *as-is*
- **AC small-signal** of the CT anti-alias front-end (SCT-013 + RC into ADS1115):
  Bode plot, −3 dB corner, 50 Hz-harmonic aliasing margin. `circuit_simulate type=ac` + `circuit_plot`.
- **Transient** of the relay-coil drive: MOSFET gate pulse → coil current ramp →
  flyback clamp spike, verify ≤ MOSFET Vds. `circuit_simulate type=tran`.
- **Transient** of the CP pilot: a `pulse` waveform source approximates the 1 kHz PWM;
  check op-amp settling / ringing on the CP line.
- **ERC** on the imported netlist for dangling/island/short sanity beyond KiCad.

### Concrete gaps (ordered)

| # | Gap | Why | Fix sketch |
|---|---|---|---|
| O-1 | **`.kicad_sch` not importable.** `ParseKiCad` wants the `(export …)` netlist; OCPP ships only schematic source. | Can't load the design at all. | (a) Document a one-liner `kicad-cli sch export netlist gridpilot.kicad_sch -o gridpilot.net` then `circuit_import format=kicad`. (b) OR have the SKiDL generator (`gridpilot_netlist.py`) emit the export-netlist form directly. (c) Optional: teach the importer to shell out to `kicad-cli` when handed a `.kicad_sch`. **Lowest-effort: (a), document it.** |
| O-2 | **PWM source ergonomics.** `pulse` exists but no first-class duty/freq PWM with the IEC 61851 duty↔amps mapping. | CP analysis + 8–97 % duty edge cases. | Add a `pwm` waveform type (freq, duty, hi/lo) to `Waveform.At()` in `types.go`; cheap. |
| O-3 | **No nonlinear/behavioral models** beyond the diode: op-amp (LM7332), optocoupler (PC817 CTR+delay), logic-level MOSFET Rds(on) curve, CT as frequency-dependent source. | Quantitative front-end accuracy. | Ship a small **builtin model library** as subcircuit macros (ideal op-amp = high-gain VCVS + output R; opto = LED-current→VCCS with delay; MOSFET = voltage-controlled R). Pure-Go, no ngspice. Mark "first-order" in output. For transistor-accurate, fall back to ngspice (`Q`/`M`/`X`). |
| O-4 | **Safety-domain ERC rules** missing. | The value-add over KiCad ERC. | Extend `erc.go` (data-driven): "every inductive-switching node (relay/solenoid) needs a flyback diode," "every isolation barrier crossing (opto/DC-DC) must not be bridged by R/L/W," "every MCU GPIO input net must terminate in a pull R." These are graph rules over the netlist — no solver needed. |
| O-5 | **IEC 61851 / 62955 compliance mode.** | Standard-aware checks (CP swing ±12 V, duty band, PP resistor ladder 390k/150k/82k/39k). | New verb `circuit_compliance {standard:"iec61851"}` → runs targeted assertions on named nets. Domain-specific; layered on top of sim. |

**Honest scope note for OCPP:** the safety case (dual-MCU interlock timing, GFCI
detection latency, thermal runaway, arc detection >5 MHz) is **firmware + FMEA +
bench**, not SPICE. The cell helps with *analog front-end correctness and ERC*, not
functional-safety proof. Don't oversell it as a substitute for IEC type testing.

---

## 4. Consumer B — Talos (the connectivity half)

**What it is:** elevator-panel + wire-harness manufacturing automation. Wants to
auto-compile a panel/harness from EPLAN → BOM + matrix-box config + robot program +
**test program**. Design docs: `ELEVATOR_PANEL_AUTOCONFIG_DESIGN.md`,
`MATRIX_DISTRIBUTION_BOX_DESIGN.md`, `MACHINE_RETROFIT_DESIGN.md`,
`HARNESS_TO_PANOWAY_STRATEGY.md`. Existing DB: `qeHarnessGraphs` (nodes/edges/cables),
`kesimRows` (cut-list: length/strip/terminal/qty), `manufacturingConfigs`,
`applicatorCrimpConfigs`.

**Today:** EPLAN is named ~40× as the *source format* but **no code ingests it**;
**no ERC, no voltage-domain, no continuity test gen in Talos**. The whole electrical-
verification half is absent — and it maps almost 1:1 onto yaver's existing
`KindConnection` + `NodeDomains` + ERC.

### What maps cleanly today
- **Panel voltage-domain ERC.** Talos rules engine emits a netlist + `NodeDomains`
  (`MAINS_L1:400, CTRL_24V:24, SAFETY_CHAIN:24…`); `circuit_erc` rule 4 flags any
  400 V↔24 V coupling through a non-isolating element. This is the headline check and
  **works with the cell unchanged** — Talos just has to emit the netlist.
- **Harness continuity.** Each crimped wire = a `KindConnection` edge; `circuit_erc`
  dangling-net + no-island rules validate the expected from-to topology. The
  matrix-box "energize port, sense where it lands" tester is exactly this check
  executed in hardware against the compiled expectation.

### Concrete gaps (ordered)

| # | Gap | Why | Fix sketch |
|---|---|---|---|
| T-1 | **EPLAN → netlist emitter (in Talos).** `import_eplan.go` reads a *connection-list CSV*, but Talos has no exporter producing it. | Nothing flows in. | Talos-side: emit `qeHarnessGraphs`/panel nets as the connection-list CSV (or netlist JSON) the cell already parses. **This is Talos's job, not the cell's** — the cell's contract is already the right shape. |
| T-2 | **Continuity tester-netlist generation.** | The op-8 / Cirris replacement. | Add a Talos compiler step that emits "expected continuities" as `KindConnection` elements; call `circuit_erc`; later drive the matrix-box. The *validation* primitive exists; the *generator* is Talos's. |
| T-3 | **Wire-gauge / ampacity ERC.** Cable AWG + terminal rating vs. net current. | Safety: 22 AWG behind a 10 A terminal. | Extend `erc.go` with an optional per-element `ampacity`/`gauge` annotation + a rule: sum net current vs. min conductor rating. Needs a small wire-gauge table (IEC 60228). New, but small and data-driven. |
| T-4 | **Protection coordination ERC.** Fuse → MCB → contactor selectivity (I²t). | EN 81-20 / IEC 60364. | Heavier. New rule comparing trip curves of series protective devices; needs a device-curve table. **Defer** — flag as out-of-scope v1, it's a real engineering DB. |
| T-5 | **Named-design slots** (same as I-2). One panel + one harness in flight at once. | Concurrency. | The `design` param from §2. |

**Talos truth note:** Talos needs the cell to be a *connectivity checker callable
from a deterministic compiler over HTTP* far more than it needs SPICE. The
voltage-domain ERC (rule 4) is the single highest-leverage thing it can adopt, and it
works **today** the moment Talos emits a tagged netlist. Lead with that.

---

## 5. Gap ledger (consolidated, prioritized)

**P0 — unblocks a real consumer with small, contained changes**
- **I-1** Stable `/ops` HTTP contract doc for `circuit_erc`/`circuit_simulate` so
  Talos's compiler + OCPP's scripts can call without an MCP/model loop.
- **O-1** Document `kicad-cli sch export netlist` → `circuit_import format=kicad`
  (and/or SKiDL emit). Zero code; unblocks OCPP import same day.
- **T-1/T-2** (Talos-side, tracked here for the contract): emit connection-list CSV
  + tester netlist. Cell side: confirm `ParseConnectionList` covers the shapes.

**P1 — real value-add, moderate effort, all in `erc.go`/`types.go` (no solver risk)**
- **O-4** Safety-domain ERC rules (flyback-on-inductive-node, isolation-barrier-integrity,
  GPIO-pull-present).
- **T-3** Wire-gauge/ampacity ERC + IEC 60228 table.
- **I-2/T-5** Named netlist slots (`design` param) for concurrent designs.
- **O-2** `pwm` waveform type.

**P2 — bigger, domain-DB-heavy, sequence after a consumer is live**
- **O-3** Builtin first-order behavioral model library (op-amp/opto/MOSFET macros).
- **O-5** `circuit_compliance` IEC 61851/62955 mode.
- **T-4** Protection-coordination (trip-curve DB).

**Non-goals (say so out loud):** PCB power-integrity / EM, thermal co-sim,
functional-safety (SIL/ASIL) proof, arc-fault waveform fingerprinting. These are
bench / specialist-tool / FMEA work; the cell will mislead if it claims them.

---

## 6. Surface-by-surface task map (the user's literal four)

- **Go agent / cell:** O-2, O-3, O-4, O-5, T-3, I-2 land in `circuit/` (`types.go`,
  `erc.go`, `solver.go`, new `models.go`) + new verbs in `ops_circuit.go`
  (`circuit_compliance`, optional `design` param on existing verbs).
- **MCP:** new verbs are auto-reachable via `ops`/`ops_verbs`. Only decide whether
  `circuit_compliance` deserves first-class status like `circuit_plot` (it returns
  text, so probably not — keep it an `ops` verb).
- **Web (`CircuitCellView.tsx`):** add a design-slot selector, a "Compliance"
  results panel, surface ampacity/safety ERC findings in the existing findings table.
- **Mobile (`circuit.tsx` + `circuitClient.ts`):** mirror the new verbs; the
  transport (LAN→relay) and types already generalize.

The cross-repo consumers (`../talos`, `../ocpp`) are **clients**, not surfaces — they
consume via §2. Their work lives in their own repos (EPLAN emitter; kicad-cli export
step) and is tracked here only as the contract they depend on.

---

## 7. The load-bearing architecture: Yaver as a hosted simulator black box

> This section supersedes the MCP-agent framing of §2. Per the actual intent:
> **Talos and OCPP connect their own UI + backend to a Yaver agent running on a
> Hetzner box and run the simulator there. Yaver is the opaque execution engine
> ("the black box"); the consumer just submits a design + analysis and gets
> results/plots back.** §2's `ops` verbs are still the wire format — they're just
> driven by Talos's app, not by a Claude agent.

### 7.1 Topology

**Decision (2026-06-14): per-product boxes.** Each consumer gets its OWN Hetzner
Yaver sim node — `talos-sim` and `ocpp-sim` — not a shared multi-tenant node. This
buys hard isolation for free (one product's designs can never reach another's) and
drops cross-tenant logic from v1: S-1 collapses to "one SDK token gates one box,"
and S-2's tenant-isolation requirement disappears (design slots are still useful
*within* a product — `panel-1` vs `panel-2`, `gridpilot-rev-b` vs `rev-c`).

```
  talos-sim box:  Talos web UI ─ relay ──┐
                  Talos Convex ─ public ─┼─→ Yaver agent (yaver serve) ─ circuit/ cell ─ vault-local netlists
                                          ┘     <talos-sim>.yaver.io / 18080 / 4433

  ocpp-sim box:   OCPP web UI ─ relay ───┐
                  OCPP firmware CI ─ pub ─┼─→ Yaver agent (yaver serve) ─ circuit/ cell ─ vault-local netlists
                                          ┘     <ocpp-sim>.yaver.io
```

- The **Hetzner box runs `yaver serve`** as a normal yaver device (the disposable
  test box pattern, or a dedicated long-lived "sim node"). The circuit cell is
  already part of the agent — nothing to deploy beyond the agent itself.
- **Talos web UI** reaches it the way every browser does: **relay-only** (CLAUDE.md
  connection table — browser CORS blocks LAN). It calls `POST /ops` (relayed) with
  `circuit_*` verbs, or the `<deviceId>.yaver.io` Cloudflare public URL.
- **Talos Convex backend** (serverless `httpAction`) can't hold a relay session;
  it calls the **public URL** (`https://<deviceId>.yaver.io/ops`) with an SDK token.
- `machine`-routing means the *same* verbs also work if Talos points at a yaver
  agent that then forwards to the Hetzner sim node — but direct-to-sim-node is simpler.

### 7.2 What already works
- **Remote execution.** Every `circuit_*` verb already accepts `machine` and runs on
  the targeted box (`ops_circuit.go` dispatch). A sim on Hetzner is `circuit_simulate
  {machine:"<hetzner-id>"}` today.
- **`circuit_plot`** returns a base64 PNG data-URL — trivially renderable in Talos's UI.
- **Privacy contract holds unchanged.** Netlists persist **vault-local on the sim
  node**, never Convex (`ops_circuit.go` storage). Talos/OCPP designs stay on the box;
  only opaque results cross the wire. This is already the "forbidden in Convex" posture.

### 7.3 What's missing for a *service* (the real build)

| # | Gap | Why it matters for a hosted service | Fix |
|---|---|---|---|
| **S-1** | **Cross-account auth.** The consuming product's UI/backend is not the same yaver user/mesh as the sim node owner. | A browser/Convex from *another product* must authenticate to the Yaver box. | With **per-product boxes** this is simply: **one SDK token per box** (`sdk_token_create`, `auth.go::ValidateSdkToken*`), scoped to the circuit verbs, stored in that product's server env. No cross-tenant scoping needed; no OAuth round-trip per call. (Guest `simulate` scope still an option if a box is ever shared.) |
| **S-2** | **Persistent multi-design state.** `ensureCircuit()` is one global netlist in one vault slot — `panel-1`, `panel-2`, `rev-b`, `rev-c` would stomp each other *within a product*. | Even a single-product box holds many designs. | Add an optional **`design` id** to every circuit verb; key the vault store `circuit/<design>`; default slot preserves back-compat. **Per-product boxes mean no cross-tenant prefix is required** — `design` is just a name within the box. |
| **S-3** | **Always-on + health.** A "running simulator support" implies the box is up and the consumer can see it. | Talos UI needs a reachable, healthy endpoint. | `yaver serve` + systemd unit already give always-on (CLAUDE.md). Add a `circuit_health` verb (engine list + load) for the consumer's status widget. The Hetzner box can be the existing `yaver-test-ephemeral` pattern or a dedicated long-lived node. |
| **S-4** | **Long-running / streaming sims.** Big transients or sweeps shouldn't block an HTTP request. | UX for heavier designs. | Optional: a job model (`circuit_simulate_async` → job id → poll/stream via the existing SSE pattern used by netcapture/devserver). Defer unless a real design needs it. |
| **S-5** | **Black-box audit of runs.** "Yaver will be the black box for such service." | Each consumer wants an auditable trail of what was simulated and the verdict — and Yaver already *has* a black-box/audit primitive. | Record each sim/ERC run (who, design id, analysis, ok/fail, timestamp — **no netlist contents**) to the activity audit summary (allowed in Convex per privacy contract) and/or the local blackbox. Gives Talos/OCPP a tamper-evident "what did the sim service do" log without leaking design IP. |

### 7.4 "Black box" — both readings, and they compose
1. **Opaque engine:** Talos submits netlist+analysis, gets results; never sees MNA
   internals. Already true by construction.
2. **Black-box recorder:** Yaver records the *fact* of each run (S-5) as the audit
   black box for the service. The two compose: Yaver is the engine **and** the flight
   recorder for the simulation service it hosts for Talos/OCPP.

### 7.5 Same shape for OCPP (its own box)
Identical, on the dedicated `ocpp-sim` box: OCPP's web UI (and firmware CI) point at
it, `circuit_import` their KiCad-exported netlist (gap O-1), `circuit_simulate`/
`circuit_plot` the analog front-end, `circuit_erc` the safety rules. Its own SDK token
(S-1), its own `design` slots (S-2, e.g. `gridpilot-rev-c`), its own audit (S-5).
Because the boxes are separate, OCPP and Talos designs are physically isolated — no
shared-tenant logic to get wrong.

### 7.7 Status — service primitives landed (2026-06-14)

Built agent-side in `desktop/agent/ops_circuit.go` (build + unit tests green;
`ops_circuit_design_test.go`):

- **S-2 design slots** — every `circuit_*` verb now takes an optional `design` id.
  `""`/`"default"` = the legacy single slot (back-compat); named ids persist to
  vault `circuit/circuit-design-<id>` (+ `~/.yaver/circuit-design-<id>.json`
  fallback). Ids are sanitized (`[a-z0-9._-]`, ≤64, traversal-stripped) so a slot
  name can't escape into a path/vault-key. Each slot gets its own controller —
  concurrent designs never stomp. New verbs `circuit_designs` (list) and
  `circuit_design_delete {design}` (named-only; default refuses).
- **S-3 `circuit_health`** — engine availability + active-design summary + design
  count, for a consumer status widget.
- **S-5 run audit** — `circuit_import/simulate/measure/erc/plot/design_delete`
  record `(user, action, "circuit/<design>", analysisType, outcome, error)` to the
  local audit black box via `AuditLog`. Privacy-checked: only
  action/target/outcome/error sync to Convex (the analysis-type detail stays in the
  local `audit.db` payload column); **never** netlist contents, element values, or
  paths.
- **S-1 auth** — no dispatcher change needed for per-product boxes: an SDK token
  that authenticates as the box owner yields `Caller:"owner"`, which already passes
  the owner-only gate on these verbs. One token per box = the whole S-1 story under
  the per-product-box decision. (Fine-grained per-verb SDK scopes remain deferred —
  only relevant if a box is ever shared.)
- **S-4 async/streaming** — deferred (no consumer needs it yet).

Not done (consumer-side, their repos): the OCPP `kicad-cli` export step (O-1) and
Talos's EPLAN→netlist emitter (T-1/T-2). Web/mobile design-slot picker is optional
surface polish (§6) — the verbs accept `design` today regardless.

### 7.8 UI surfaces — design picker + Talos consumer apps (2026-06-14)

All four surfaces the user asked for now reach the cell, plus both Talos apps:

- **Yaver mobile** (`mobile/app/circuit.tsx` + `circuitClient.ts`) — design-slot
  picker (chips + new-slot input), `design` threaded through every verb; new
  `designs()`/`designDelete()`/`health()` client methods. tsc clean.
- **Yaver web** (`web/components/dashboard/CircuitCellView.tsx`) — design picker +
  delete; `call()` auto-injects the active `design`. tsc clean.
- **Talos web** — `talos/web/src/app/api/yaver/ops/route.ts` (a **circuit-scoped**
  ops proxy: only `circuit_*` verbs, server-held agent token, forwards to the sim
  node's `/ops`), `talos/web/src/components/circuit-panel.tsx` (full panel,
  waveforms via `circuit_plot` PNG), `talos/web/src/app/dashboard/circuit/page.tsx`
  (first-class route). tsc clean.
- **Talos mobile** — `talos/mobile/src/screens/more/CircuitScreen.tsx` (same panel,
  calls the shared `/api/yaver/ops` via `apiFetch` — no agent token on device),
  registered in `navigation/index.tsx` + a "⚡ Electrical Simulator" More-menu entry.
  tsc clean.

Both Talos surfaces share ONE server-side seam (`/api/yaver/ops`) — the mobile app
reuses the same proxy route as web, so auth lives in exactly one place. This is the
"vibe on electrical" loop for non-Yaver products: a Talos user opens the simulator,
edits a netlist into a design slot, runs it on the Hetzner sim node, and sees the
curve — the agent-in-the-loop variant is already covered by the `circuit_plot` MCP
image tool (edit → simulate → SEE → fix).

### 7.9 OCPP/Kalkan surfaces + verified E2E (2026-06-14)

- **OCPP web** — `ocpp/web/app/api/yaver/ops/route.ts` (cookie-gated `kalkan_session`,
  circuit-scoped, env `YAVER_AGENT_URL`/`YAVER_AGENT_TOKEN` → `ocpp-sim` box),
  `ocpp/web/app/components/CircuitPanel.tsx`, `ocpp/web/app/dashboard/circuit/page.tsx`.
  tsc clean.
- **OCPP mobile** — `ocpp/mobile/app/(tabs)/circuit.tsx` as a first-class "Devre" tab.
  NOTE: kalkan mobile talks directly to the charger (`192.168.4.1`), not a web
  backend — so there's no session-proxy to reuse. The screen connects to the Yaver
  sim node directly via a **configurable URL + SDK token** stored in AsyncStorage
  (the per-box S-1 model). tsc clean.

**Verified end-to-end (not just typecheck):** `ops_circuit_e2e_test.go` drives the
real registered handlers through `dispatchOps`/`spec.Handler`: design-slot
persistence + isolation (`panel-a` survives a `panel-b` import), `circuit_designs`
lists default+named, `circuit_health`, `circuit_simulate` runs the MNA engine and
returns samples, `circuit_design_delete` drops a slot and refuses the default. PASS.
(The live local daemon on :18080 was a **stale binary** predating the circuit cell —
`unknown_verb` even for `circuit_import` — so the dispatch-path test is the
authoritative proof; a real curl E2E needs the box running a current binary.)

### 7.6 Net new surface work vs. §6
The cell-level ERC/solver gaps (§5) are unchanged. The **service** adds, on the agent
side: `design` param plumb-through (S-2), `circuit_health` (S-3), SDK-token/guest
scope for circuit verbs (S-1), audit hook (S-5), optional async (S-4). On the consumer
side (their repos): a thin client to the public URL + an SDK token + UI. **None of
this touches the solver** — it's all auth + state + routing, which yaver already does
for every other cell.

---

## 7.10 Auth & isolation — the circuit capability scope (BUILT 2026-06-14)

Yaver is a resource-wrapper layer: it exposes a single resource (the circuit
simulator) as an **isolated service** that other products consume — without
lending them the rest of the owner's machine. The auth layer that makes "nobody
but my services can touch my resources" real:

**The `circuit` capability scope.** A new guest-scope (`GuestScopeCircuit`) that is
a strict single-family allowlist. A token at this scope can reach **only** `/ops`
(+ `/info`/`/health`) and within `/ops` **only** `circuit_*` verbs — no exec, no
vault, no tasks, no file read, no other verb family, not even verbs marked
`AllowGuest`. Built across:
- `ops.go` — `capabilityScopeVerbPrefix{"circuit":"circuit_"}`, `isCapabilityScope`,
  `firstCapabilityScope`, `guestVerbAllowed`; `OpsContext.Scope` plumbed from the
  `X-Yaver-GuestScope` header; both guest gates in `dispatchOps` now call
  `guestVerbAllowed` (a capability scope ignores `AllowGuest` — strict allowlist).
- `guest_scope.go` — `GuestScopeCircuit` + `guestCircuitAllowedPrefixes` (`/ops`,
  `/ops/plan`, `/info`, `/health`) wired into `guestScopeOrDefault` +
  `isGuestAllowedPathForScope`.
- `httpserver.go` — `scopePathPrefixes["circuit"]` (SDK-token path gate) +
  `applyDelegatedGuestSDKHeaders` now **auto-demotes a capability token to a scoped
  guest**. THIS is the load-bearing fix: without it an SDK token with
  `scopes:["circuit"]` would still hit `/ops` as the *owner* (full access). Now any
  token carrying a capability scope is stamped `X-Yaver-Guest:true` +
  `X-Yaver-GuestScope:circuit` + a synthetic `svc:circuit` identity, so the verb
  gate applies.
- Tests: `ops_circuit_scope_test.go` (authz matrix + dispatch-path isolation),
  `ops_circuit_e2e_test.go` (real MNA run through the handlers). All green.

**Five layers gate a product's request — defence in depth:**
1. **Account binding** (already in agent): `sdkInfo.UserID != s.ownerUserID → 403`.
   The token must be minted from the **same Yaver account** the sim node is signed
   into. A stranger can't mint a token for your account (needs your session secret).
2. **CIDR binding**: `--allowed-ips <product-egress>/32` — a leaked token is useless
   off the product's server.
3. **Path gate**: `scopes:["circuit"]` → only `/ops`/`/info`/`/health`.
4. **Capability demotion**: token → scoped guest (not owner).
5. **Verb gate**: scoped guest + `circuit` scope → only `circuit_*` verbs.

**Minting an account-based service token (no backend change — scopes are free-form):**
```bash
# on a machine signed into YOUR Yaver account:
yaver sdk-token create --label talos-circuit \
  --scopes circuit --allowed-ips <talos-server-egress-ip>/32 --expires 365d
# → prints the token. Put it in Talos's server env as YAVER_AGENT_TOKEN,
#   and the sim node URL as YAVER_AGENT_URL=https://<ocpp-sim-or-talos-sim>.yaver.io
```
Repeat for OCPP (`--label ocpp-circuit --allowed-ips <ocpp-egress>/32`). Each product
gets its OWN token + IP binding; revoke one without touching the other.

## 8. Secure deployment plan (Hetzner, co-located with Talos systemd)

Target: the existing Talos systemd box hosts the sim service too, but **isolated**.

1. **Fresh binary.** The live local daemon is a stale build — the sim node must run a
   binary built from THIS source (it carries the capability-scope enforcement).
   Build `linux/<arch>` and ship it to the box.
2. **Dedicated unprivileged service identity.** Run the sim agent as its own OS user
   (`yaver-sim`), NOT root, NOT the Talos service user — a circuit verb can't read
   Talos's files because (a) the circuit scope exposes no file verbs and (b) the
   process user has no access to Talos's data dir. Separate `~/.yaver` (its own
   account login / device).
3. **Network posture.** Inbound: only `/ops` reachable (relay-only, or firewall so
   the box accepts the agent port only from the relay / the product servers' IPs).
   Outbound: the sim does no egress; an egress firewall (drop RFC1918 + internet) is
   belt-and-suspenders so a hypothetical bug can't reach the LAN/other services.
4. **Account wiring.** Sign the sim node into YOUR Yaver account (so minted tokens'
   `ownerUserID` matches). It registers as a device; alias it `talos-sim`/`ocpp-sim`.
5. **systemd unit** for `yaver serve` under `yaver-sim`, auto-restart, `ProtectSystem`,
   `PrivateTmp`, `NoNewPrivileges`, a locked-down `User=yaver-sim`.
6. **Mint + wire tokens** (above), set product env, done.

Open decisions for you (in the reply): one shared sim box vs. per-product boxes
(§7.1 chose per-product — but you said co-locate on the Talos box), and whether I
SSH in to do steps 1-5 now or you want to review each.

## 8a. DEPLOYED + verified in production (2026-06-14)

The isolated circuit-sim service is **live** on the Talos box and end-to-end verified
through a scoped token over the relay:

- **Instance:** `yaver-sim.service` (systemd), user `yaver-sim`, ports 18090/4434,
  `--debug --no-tls --relay-only` (loopback-bound; reachable only via the relay → no
  firewall changes). Device `<sim-deviceId>`, signed into the owner account. The existing
  `yaver.service` (:18080) + all `talos-*` services are untouched and active.
- **Routing reality:** `<deviceId>.yaver.io` is NOT routed (Vercel 404). The real
  public path is the **relay**: `https://public.yaver.io/d/<deviceId>`, which is
  **password-gated** — so a product sends BOTH `Authorization: Bearer <circuit-token>`
  AND `X-Relay-Password: <relay-pw>`. (Talos `yaver-proxy.ts` already sends both via
  `YAVER_RELAY_PASSWORD`; OCPP web route + mobile screen updated to send it too.)
- **One bug fixed to make it work:** `/ops` + `/ops/plan` were registered with the
  owner-only `auth()` middleware, so SDK tokens never validated (→ "invalid token").
  Switched to `authSDKOrGuest` (httpserver.go:458) — owner/agent tokens pass through
  unchanged; capability-scoped SDK tokens validate + CIDR-check + demote to scoped
  guest. Host build + tests green.
- **Verified remote (this Mac → public.yaver.io → box):** `circuit_health` → ok,
  `engine:auto`; `circuit_simulate` → ok, **512 samples, engine:ngspice**; and the
  isolation holds — `arm_describe` → `unauthorized "not permitted for this scoped
  session"`, `exec_command`/`vault_list` → refused.
- **Credentials:** `/root/.yaver/circuit-service-tokens.env` on the box (0600) holds
  `YAVER_AGENT_URL`, `TALOS_CIRCUIT_TOKEN`, `OCPP_CIRCUIT_TOKEN`, `YAVER_RELAY_PASSWORD`.
  Tokens are account-bound + circuit-scoped (no `--allowed-ips` yet — re-mint with
  `--allowed-ips <product-egress>/32` to add the CIDR factor).

**Remaining (needs YOUR infra access — secrets must live in product env, not code):**
- Talos web + OCPP web: set `YAVER_AGENT_URL` + `YAVER_AGENT_TOKEN` (their token) +
  `YAVER_RELAY_PASSWORD` in their Cloudflare/server env. Then `/dashboard/circuit`
  works live.
- OCPP mobile: enter URL + token + relay password in the "Devre" tab. (Security note:
  this puts the relay password on-device; blast radius is circuit-only, but routing
  OCPP mobile through the OCPP web proxy later is cleaner.)

## 9. Recommended first move

Don't boil the ocean. Pick the one consumer with the shortest path to a *real*
result and prove the service shape end-to-end:

- **OCPP path:** `kicad-cli` export → `circuit_import` → `circuit_plot` an AC Bode of
  the CT front-end → one safety ERC rule (flyback). Proves the analog half + the
  remote-consumption surface. ~1 day, mostly docs + one ERC rule.
- **Talos path:** emit one panel's nets + `NodeDomains` → `circuit_erc` over `/ops`
  HTTP → catch a planted 400 V↔24 V short. Proves the connectivity half + the
  deterministic-compiler HTTP surface. ~1 day, mostly Talos-side emitter + the I-1 doc.

Either one validates "two repos consume the cell" with near-zero solver risk. Do one,
write the contract down, then fan out the P1 ERC rules.
