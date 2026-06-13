# Yaver Circuit Simulator

Yaver's generic, domain-agnostic **electrical-circuit cell** — the electrical
sibling of the robot-arm, Cartesian-robot, and 3D-printer cells. Import a
circuit, simulate it, rule-check it, and view the waveform — from the host
Claude Code (MCP), the web dashboard, or the phone, against any device on your
mesh.

Like every Yaver cell it follows one shape:

```
circuit.Backend  →  circuit.Controller  →  ops_circuit.go verbs
                                         →  circuit_plot first-class MCP tool
                                         →  web CircuitCellView / mobile circuit.tsx
```

## Why it exists

Electrical work spans SPICE analog design, KiCad schematics, and EPLAN /
wire-harness control panels. None of those tools run on a headless box, and none
expose an agent surface. Yaver adds a thin, **dependency-free** layer so an AI
agent (or you, from the phone) can drive circuit design on a self-hosted device:
edit a netlist → simulate → look at the curve → fix.

## Engines

Two interchangeable engines implement `circuit.Backend`:

| Engine | When | Capability |
|---|---|---|
| **builtin** | always (zero installs) | Pure-Go modified-nodal-analysis solver. `op` / `dc` / `tran` / `ac`. Linear R/L/C, independent V/I sources (DC / sine / pulse), linear controlled sources (E/G), nonlinear diodes (Newton-Raphson). |
| **ngspice** | when `ngspice` is on PATH | Shells out to `ngspice -b` for full `.model` device libraries (BJT/MOSFET/sub-circuits). |

Engine select (`circuit_config_set {engine}`): `auto` (ngspice if installed,
else builtin) · `builtin` · `ngspice`. The built-in solver is verified against
analytic results (resistive divider, RC time-constant, AC −3 dB cutoff, diode
drop, inductor DC short) in `circuit/solver_test.go`.

## Import formats

`circuit_import {format?: "auto", text}` — format is sniffed when omitted:

- **SPICE** (`.cir`/`.sp` subset): R/C/L/V/I/D/E/G cards, `.model`/`.tran`/`.ac`/`.dc`
  directives, `*` and `;` comments, `+` continuations. Engineering suffixes
  (`k`, `meg`, `m`, `u`/`µ`, `n`, `p`, `f`, `g`, `t`).
- **KiCad** netlist export (the `(export …)` s-expression from Eeschema). Two-pin
  parts (R/C/L/D/V/I, inferred from the reference prefix) become simulatable
  elements; multi-pin/IC parts become Connection edges for ERC.
- **EPLAN / harness connection list** (CSV / `;` / TSV, header- or position-based):
  - *wirelist* (`fromConnector, fromPin, …, toConnector, toPin`) → nodes are
    terminals, edges are wires.
  - *net-graph* (`net, component, pin`) → nodes are nets, edges are the components
    that bridge them; a `voltage` column tags a net's nominal volts for the ERC
    isolation check.

  Connection-list designs aren't SPICE-simulable; `circuit_simulate` refuses and
  points you at `circuit_erc`.

## ERC — generic electrical-rule-check

`circuit_erc` runs a domain-agnostic rule set (severity error/warn/info):

- **no-ground** — no `0` reference node.
- **dangling-net** — a net with a single connection.
- **shorted-source** — a voltage source with both terminals on one node.
- **no-dc-path-to-ground** — connectivity island not reaching ground (union-find).
- **voltage-domain-mismatch** — two nets with different `domainV` tags joined by a
  non-isolating element (R/L/wire). A capacitor or controlled source is treated
  as an intentional (non-DC) bridge, so coupling caps don't false-positive. Arm
  it with `circuit_set_domain {net, volts}` (or a `voltage` column on import).

Domain rules are **data-driven**: power / safety / harness callers add per-net
voltage tags; no code changes. This is the seam for future domain rule packs
(EN 81 panels, IEC 61851 EV pilot, IEC 61439 switchgear).

## Ops verbs

All `AllowGuest:false`, routable to any mesh device via `machine`:

| Verb | Purpose |
|---|---|
| `circuit_engines` | available engines + capabilities |
| `circuit_config_get` / `circuit_config_set` | engine / ngspicePath / defaultAnalysis |
| `circuit_import` | parse SPICE/KiCad/EPLAN text → load |
| `circuit_export` | emit loaded netlist as SPICE or JSON |
| `circuit_describe` | parametric snapshot (nets, elements, sources) |
| `circuit_simulate` | run `{type: op\|dc\|tran\|ac, …}` → `SimResult` |
| `circuit_measure` | convenience DC op-point (node V + branch I) |
| `circuit_erc` | run the ERC engine |
| `circuit_set_domain` | tag a net's nominal voltage |
| `circuit_plot` | render a waveform PNG (data URL) |

## First-class MCP tool: `circuit_plot`

`ops` flattens results to text, which hides an image. `circuit_plot` (declared
in `mcp_tools.go`, handled in `httpserver.go`) is a thin adapter over the
`circuit_plot` verb that re-wraps the rendered PNG as a **viewable MCP image
block** — so the host model SEES the waveform and can iterate. The PNG is drawn
purely with the std `image`/`image/png` packages (axes, grid, colored traces,
legend); no plotting dependency.

## UI

- **Web** — `web/components/dashboard/CircuitCellView.tsx` at `/dashboard/circuit`
  (its own route, like the arm/printer cells; relay-only). Engine picker, netlist
  editor + import, op/tran/ac/dc controls, waveform image, op-point table, ERC list.
- **Mobile** — `mobile/app/circuit.tsx` (linked from **More → Circuit Simulator**),
  client in `mobile/src/lib/circuitClient.ts`. LAN-first, relay fallback, same verbs.

## Privacy

Netlists are user work-derived. They live in the local vault
(`circuit`/`circuit-config`) with a `~/.yaver/circuit-config.json` fallback, and
flow device-to-device over the ops mesh. **They never touch Convex** — same
contract as arm programs.

## Out of scope (first cut)

PCB/gerber DRC, KiCad/EPLAN PDF OCR ingestion, transistor/IC models in the
built-in solver (use ngspice), and certification-envelope rule packs. The
`Backend` interface and the ERC rule list are built to extend into these.

## Tests

```bash
cd desktop/agent && go test ./circuit/      # solver vs analytic, netlist round-trip, ERC, imports, plot
```

This package never touches the vault/auth keychain, so it runs without macOS
keychain prompts.
