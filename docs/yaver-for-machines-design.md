# Yaver for Machines — Universal Machine Wrapper (Deep Design)

Status: design. Branch `feat/yaver-robot-cell`. Written 2026-06-06.
Wire-harness-first. Builds on the proven Ender-3 wrapping pattern
(`desktop/agent/robot/backend.go`), the Modbus observe engine
(`desktop/agent/machine/`), wire-observe (`desktop/agent/netcapture/`), the ghost
HMI-vision engine, and the Talos machine-edge bridge
(`talos/cloud/convex/machineEdge.ts`, `/machine-edge/*`).

> **Source-of-truth rule (CLAUDE.md):** Yaver/Talos facts below are grounded in code
> by `file:line`; protocol/vendor facts are from web research and flagged ⚠️ where
> uncertain. When code drifts, fix this doc in the same change.

---

## 0. Thesis — "Yaver for machines" = one driver seam + three verbs

You wrapped the Ender-3 by hiding a printer behind `robot.Backend` and exposing
**view / control / verify** through mesh-routable ops verbs + a mobile UI. The ask now
is to do that for **every machine on the floor** — "Yaver for machines: view them,
watch them, control them" — starting with **wire-harness machinery**.

The one missing abstraction is a **`machine.Driver` interface**: the generalization of
`robot.Backend` to heterogeneous machines (Modbus crimpers, OPC-UA cut-strip lines,
S7 PLCs, MQTT telemetry, and — the moat — screen-only legacy machines wrapped by
camera+VLM). Everything else you already have is a driver, a surface, or a bridge that
plugs into this seam.

Three product verbs, each a surface:
- **View** — discover a machine, learn its capability set + register/tag map, render its
  "Machine Operating Manual."
- **Watch** — live state + telemetry (OEE, counts, alarms, crimp-force curves), AI-narrated,
  camera/HMI vision included.
- **Control** — gated writes: job/recipe download, program-recall, register write — behind
  capability + bounds + approval + audit, with the machine's own PLC interlocks untouched.

**Decision that supersedes the earlier "machines need no Backend" stance:** because the
product goal is *Yaver as the machine wrapper* (not just an observer that hands control to
Talos), we **do** build a driver seam in the Go agent — exactly parallel to `robot.Backend`.
Talos remains the record/UI/recipe plane; Yaver owns the live wrap.

---

## 1. The centerpiece — `machine.Driver` + capability model

A new interface in `desktop/agent/machine/driver.go`, the structural twin of
`robot.Backend` (`backend.go:18-30`) but capability-typed because a crimp press, a CNC,
and a screen-only stripper expose wildly different surfaces.

```go
// machine/driver.go (new)

// Driver wraps ONE machine behind a uniform view/watch/control surface.
// Implementations declare what they can do via Capabilities(); callers (ops verbs,
// AI supervisor, UI) reason over the cap set, never a fixed machine "type".
type Driver interface {
    Name() string
    Kind() string                 // "crimp", "cut_strip", "press", "cnc", "tester", "robot", ...
    Capabilities() CapSet

    Connect(ctx context.Context) error
    Close() error
    Status(ctx context.Context) (MachineStatus, error)   // running|idle|fault|setup|off + heartbeat

    // VIEW: enumerate the machine's addressable surface.
    Browse(ctx context.Context) ([]Tag, error)           // OPC-UA address space / Modbus map / MTConnect probe / learned HMI fields

    // WATCH: read + subscribe (native sub, or polled fallback the driver emulates).
    Read(ctx context.Context, refs []TagRef) ([]Sample, error)
    Subscribe(ctx context.Context, refs []TagRef, opts SubOpts) (<-chan Sample, error)

    // CONTROL (all GATED at the ops layer — see §11): only if the cap is present.
    Write(ctx context.Context, w []TagWrite) error       // CapWrite
    Recall(ctx context.Context, program string) error    // CapProgram (machine-stored program slot)
    SubmitJob(ctx context.Context, job Job) error        // CapJob (download a cutting list / recipe)
}

type Capability string
const (
    CapStatus    Capability = "status"    // derived run/idle/fault — even a CT-clamp-only machine has this
    CapRead                 = "read"
    CapSubscribe            = "subscribe"  // native (OPC-UA) or polled emulation
    CapWrite                = "write"      // direct tag/register write — gated
    CapProgram              = "program"    // recall a machine-stored program by number/name
    CapJob                  = "job"        // download a job/recipe (WPCS/OPC 40570/CSV)
    CapVision               = "vision"     // VLM-of-HMI read path (the moat)
    CapCurve                = "curve"      // per-crimp force curve / measurement stream
    CapEStop                = "estop"      // commandable safe stop
)
type CapSet map[Capability]bool
```

A **dumb commodity crimp press** is `{status, vision}` (CT clamp + camera-on-HMI). A
**YH-8030H cut-strip** is `{status, read, write, program, vision}` (Modbus + HMI). A
**Komax Alpha / Schleuniger CrimpCenter** is `{status, read, subscribe, job, curve}`
(OPC 40570 over OPC-UA). A **robot cell** is the existing `robot.Backend` surfaced *also*
as a `Driver` (`{status, read, estop}`) for the view/watch wall. Same seam, different caps.

**Relationship to `robot.Backend`:** robot stays its own real-time motion interface
(M400 gates, servo cadence — see `docs/yaver-fairino-cobot-cell-design.md`); a thin
`robotDriverAdapter` exposes a robot as a read-only `machine.Driver` so the fleet wall
shows arms and crimpers in one grid. We do **not** force motion through the generic seam.

---

## 2. Reuse map (what already exists — do not rebuild)

| Need | Existing asset | Reuse as |
|---|---|---|
| Modbus TCP/RTU client + frame decode | `machine/modbus.go`, `machine/machine.go` | `modbusDriver` |
| Passive bus sniff + register-role inference | `machine/sniff.go` (`classify()`), `ops_machine.go` `machine_sniff_*` | discovery for `modbusDriver` |
| AI register labeling (LLM → name/unit/scale) | `ops_machine.go` `machine_understand` | builds the Machine Operating Manual |
| Wire-level protocol debugger | `netcapture/` (Modbus TCP+RTU, serial, HTTP; `netcapture_analyze` LLM diagnosis) | troubleshoot any driver's link |
| HMI screen read + click/type + vision locate | ghost engine (`ghost/`, `ghost_vision.go`, `ops_ghost*.go`) | `visionDriver` (the moat) |
| Capability advertisement in heartbeat | `console_machines.go:268` `SupportsMachineSniff` | extend with driver caps |
| Talos device/command/telemetry bridge | `talos` `machineEdgeDevices/Commands/Telemetry`, `/machine-edge/*` (BUILT) | record/UI plane |
| Learned register map storage | Talos `machineManuals` (BUILT schema) | Machine Operating Manual store |
| Recipe resolution (cable→params→program) | Talos `machineRecipes`, `/mobile/machine-edge/recipe-resolve` | order→job |
| Supervised-sniff job tagging | Talos `machineJobContexts` | ground-truth labels for discovery |
| Store-and-forward / offline-durable | Fleet SDK queue, agent durable loops | UNS edge buffering |
| Mesh routing (LAN/relay), owner-auth ops | `ops.go` `proxyToDevice`, `AllowGuest:false` | every `machine_*` verb |

**What's genuinely new:** the `Driver` interface + non-Modbus drivers (OPC-UA, MQTT/
Sparkplug, S7, file/WPCS, digital-IO/CT, vision), a UNS/Sparkplug publish path, and the
OEE/status-wall "watch" UI. Everything else is composition.

---

## 3. SOTA grounding → decisions

From the connectivity + wire-harness research (citations in the research notes):

1. **Protocol hierarchy (build in this order of leverage):**
   - **Modbus TCP/RTU** — the brownfield workhorse, already built. First-class.
     Go: keep our own (or `grid-x/modbus`). Port 502 / RS-485.
   - **OPC-UA** — the industrial lingua franca; the *only* protocol that gives generic
     browse + read/write + native subscribe. **Go `github.com/gopcua/opcua` is
     production-grade for client work** (Northvolt, United Manufacturing Hub use it).
     ⚠️ client-only, binary-over-TCP only (no PubSub/server) — fine for us. Port 4840.
   - **MQTT + Sparkplug B** — the IIoT telemetry standard and our **data model** (§7).
     `paho.mqtt.golang` for transport; **no blessed Go Sparkplug lib — generate bindings
     from `sparkplug_b.proto` and own the birth/death/seq state machine** (~few hundred LoC).
   - **S7comm** (Siemens PLC piggyback, port 102, `gos7`/snap7 cgo) and **MTConnect**
     (CNC, read-only HTTP/XML — stdlib client) where present.
   - **Fieldbus (Profinet/EtherCAT/EtherNet-IP)**: **do NOT reimplement in software** —
     read it *through the PLC* (Modbus/S7/OPC-UA) or a **$300 HMS Anybus gateway**. Saying
     this explicitly is a credibility signal, not a gap.

2. **Wire-harness is now standardized — design against it.** **OPC 40570 "OPC UA
   Companion Specification for Wire Harness Manufacturing," v1.0.0, published 2025** by
   Komax+Schleuniger+DiIT via VDMA. It defines typed nodes for **cut/strip/crimp/seal/slit**
   — `CutInputDataType`/`CutOutputDataType`, `CrimpInput/OutputDataType` (incl. measurement
   **curves**), `StoreAndStartJob`, `ISA95JobOrderDataType`, events `ProductFinishedEventType`
   / `RunCompleteEventType`. Built on OPC 40001 (Machinery) + DI + ISA-95 + **VEC (VDA 4968/
   PSI21)** for article description. **Adopt OPC 40570's job/result types as our canonical
   internal schema** — we align with where the whole industry is heading and map everything
   else (Modbus, vision, CSV) into it. ⚠️ Real machine support is *early* (2025 spec) — build
   for graceful degradation; most machines won't speak it yet.

3. **The Komax/Schleuniger stack is `MIKO + WPCS + OPC-UA/MQTT`.** **WPCS** (Wire
   Processing Communication Standard) is the job/data app-layer (cutting list down,
   production+quality data up); **MIKO** the real-time open interface; **Komax Connect**
   aggregates cross-vendor via OPC-UA+MQTT. ⚠️ **WPCS wire format/port is NDA** — integrate
   *through* Komax software or OPC 40570, don't reverse-engineer it. The friction-free
   inbound path many shops use is **CSV cutting list → TopConvert → WPCS → machine**, so
   **CSV/XML job ingest + DataMatrix barcode scan-to-setup** is a universal low-tier path.

4. **The moat is vision-of-the-HMI.** Kepware/HighByte/Litmus/Ignition cannot wrap a
   machine that only has a *screen*. Yaver can — the proven webcam→VLM-verify loop
   (ghost engine + the robot vision gate) reads counters/state/alarm text off an HMI with
   a VLM (Qwen3-VL / Gemini Flash understand UI context, not just OCR). **Reading is
   production-credible today; VLM-*driving* an HMI (writing setpoints by simulated touch) is
   frontier — keep vision on the read/verify side** until a hardware interlock backs it.

5. **Unified Namespace (UNS) as the data model.** MQTT broker as single source of truth,
   ISA-95 topic hierarchy (`enterprise/site/area/line/cell/...`), Sparkplug B for
   self-describing birth/death + metric state. It's the converging industry standard, it's
   MQTT (we already speak it), and it gives one addressing scheme spanning crimp-press → CNC
   → robot cell. **Yaver *is* the Sparkplug edge node** that bridges each wrapped machine
   into the UNS.

6. **Crimp quality is its own data plane.** Crimp Force Monitors (Komax/Schleuniger
   **ACO 07/08**, **CPA+**) produce per-crimp **force-vs-stroke signature curves** compared
   to a taught envelope; outputs flow to vendor software (CFAlab/WinCrimp/QCenter). ⚠️ raw
   curve export is proprietary — consume via QCenter / OPC 40570 `CrimpOutputDataType` where
   available, else capture pass/fail + crimp-height/pull-force Cpk. **IPC/WHMA-A-620 Rev E**
   is the acceptability standard and mandates **full traceability** for Class 2/3 (material
   certs, process params, inspection results) — our telemetry/audit schema should carry it.

---

## 4. The three surfaces

### 4.1 View — discover + render the Machine Operating Manual
- `machine_browse` → `Driver.Browse()` returns the addressable surface (OPC-UA address
  space, Modbus register map, MTConnect probe, or learned HMI fields).
- For machines with no API, the **discovery loop** (§6) learns the map; result is stored as
  the Talos `machineManuals` row (BUILT schema) — a versioned, fleet-reusable "manual."
- UI: a machine card showing kind, caps, connection, tag map, confidence, last-seen.

### 4.2 Watch — live state, telemetry, OEE, AI narration
- `machine_subscribe` / `machine_read` stream `Sample`s; the agent publishes them to the
  **UNS (Sparkplug edge node)** and mirrors summaries to Talos `machineTelemetry`.
- The "watch" surface (web + mobile) is the **status wall**: red/yellow/green tiles, live
  **OEE = Availability × Performance × Quality**, downtime with operator-attributed reason
  codes, part counts, crimp-force pass/fail + Cpk, applicator stroke/tool-life, alarm codes.
- **AI-narrated state** is the differentiator: the same vision-verdict + telemetry
  cross-check pattern from robot — "machine idle 6 min, last alarm 0x21 (wire jam), CT
  current flat" — and `netcapture_analyze` for link-level root cause.

### 4.3 Control — gated writes only
- `machine_write` (register/tag), `machine_recall` (program slot), `machine_submit_job`
  (CSV/OPC 40570 job). Every write passes the gate in §11. Reads/subscribes default-on;
  writes are an explicitly granted capability.

---

## 5. Drivers — one per protocol (behind `machine.Driver`)

| Driver | File (new unless noted) | Caps | Status | Notes |
|---|---|---|---|---|
| `modbusDriver` | wraps `machine/modbus.go` + `sniff.go` | status, read, write, program(via reg) | **substrate BUILT** | wire into `Driver`; RS-485 gotcha: never open a port another proc holds (DTR reset → EIO) |
| `opcuaDriver` | `machine/driver_opcua.go` | status, read, subscribe, write, job, curve | new | `gopcua/opcua`; OPC 40570 typed nodes; native subscriptions |
| `mqttDriver` | `machine/driver_mqtt.go` | status, read, subscribe | new | ingest existing machine MQTT/Sparkplug; also our UNS *publish* path |
| `s7Driver` | `machine/driver_s7.go` | status, read, write | new | Siemens PLC piggyback (`gos7`/snap7, cgo) — read-low-risk |
| `mtconnectDriver` | `machine/driver_mtconnect.go` | status, read, subscribe | new | CNC read-only; stdlib HTTP, no special lib |
| `fileDriver` (WPCS/CSV) | `machine/driver_file.go` | job | new | CSV/XML cutting-list ingest + watch a job dir; DataMatrix scan-to-setup |
| `ioDriver` (digital-IO + CT) | `machine/driver_io.go` | status, read | new | signal tower / cycle relay / part-count taps + clamp-on current → run/idle/count |
| `visionDriver` (HMI) | `machine/driver_vision.go` | status, vision, read | **substrate BUILT (ghost)** | camera/HMI capture → VLM → counts/state/alarm text. **The moat.** |
| `robotDriverAdapter` | `machine/driver_robot.go` | status, read, estop | new (thin) | surfaces a `robot.Backend` on the machine wall |

Fieldbus (Profinet/EtherCAT/EtherNet-IP) is intentionally **not** a software driver —
read through the PLC (`s7Driver`/`modbusDriver`/`opcuaDriver`) or an Anybus gateway.

---

## 6. Discovery & the Machine Operating Manual

Reuse the existing discovery modes (already in the Talos schema's `discoveryMode`:
`sniff | supervised_sniff | guided | vision | off`) and the AI labeler:

1. **Sniff** (Modbus) — passive RS-485 tap, `classify()` infers register roles
   (setpoint/live/counter/alarm), `machine_understand` LLM labels name/unit/scale.
   (`machine/sniff.go`, `ops_machine.go` — BUILT.)
2. **Supervised sniff** — operator tags the running job in the mobile app
   (`machineJobContexts`, BUILT), giving ground-truth values ("length=1250") that anchor
   the LLM's scale/name inference ("register 40012 = cut_length ×0.25"). Highest accuracy,
   low effort — the recommended path.
3. **Guided** — a wizard asks the operator to change one setting at a time; the agent
   diffs the register/HMI delta to derive the mapping.
4. **Vision** — for no-bus machines: camera snapshot → VLM classifies machine + reads
   fields off the HMI. No electrical access at all.
5. **Browse** (OPC-UA) — for tier-1 machines, just walk the OPC 40570 address space; the
   "manual" is mostly free.

Output is a versioned **Machine Operating Manual** (Talos `machineManuals`: `registers[]`,
`hmiFields[]`, `programSlots[]`, `safeRanges`, `confidence`, `status: draft→learning→
verified`). **Learn a machine *type* once; every identical unit clones + re-verifies** —
fleet-wide compounding knowledge, the manufacturing moat from `[[project_mfg_on_demand]]`.

---

## 7. Data model — ISA-95/UNS + OPC 40570 canonical types

- **Topic/addressing:** ISA-95 UNS — `org/site/area/line/cell/<machine>/<metric>`. Each
  wrapped machine is a **Sparkplug B edge node** (NBIRTH/NDEATH via MQTT LWT, DBIRTH per
  device, aliased metrics, seq numbers, rebirth-on-request).
- **Job/result canonical schema = OPC 40570 shape:** internal `Job` ≈ `ISA95JobOrderDataType`
  + `Cut/Strip/Crimp/SealInputDataType`; internal `Result` ≈ `*OutputDataType` (+ curves).
  Modbus/vision/CSV machines map *into* these types, so the AI and UI see one model
  regardless of how the data was obtained.
- **Talos mirror (record plane):** `machineTelemetry` (state, pcs, cycleDelta, currentA,
  alarmCode, program, raw), `machineEdgeDevices.snapshot`, plus a new **`machineQuality`**
  (crimp-height/pull-force Cpk, force-curve pass/fail, IPC-620 traceability fields) and
  **`machineCurves`** (storageId pointer to the force curve, not the bytes — privacy).
- **Privacy (CLAUDE.md):** production data is work-derived → P2P / on-device / org-secret
  HTTP; Convex gets summaries + storageId pointers only. Add new fields to
  `convex_privacy_test.go`'s forbidden-payload set (no absolute paths, no raw dumps).

---

## 8. Wire-harness domain (first focus)

**The concrete first targets (named in Talos code today):**
- **Yuanhan YH-8030H** (cut+strip, 7" HMI, ~100 stored programs, reserved I/O) — best
  auto-discovery + supervised-sniff + program-recall candidate. `{status,read,write,program,vision}`.
- **JCWelec CST18D** (dual-end crimp+seal, 10" HMI, internal PLC, RS-485 exposed) —
  Modbus read → register write. `{status,read,write,program,vision}`.
- **Schleuniger CrimpCenter 36** (Komax, closed, licensed SMG) — observe-only via signal
  tower + CT + vision + (where licensed) OPC 40570/WPCS export. `{status,vision,curve?}`.
  ⚠️ commodity model numbers were unverifiable on vendor sites; treat as HMI-only Chinese
  benchtop class — exactly the vision-moat segment.

**Two tiers of shop reality:**
- **Tier-1 (Komax/Schleuniger/DiIT):** speak OPC 40570 / WPCS-via-software / CSV+barcode;
  rich job download + crimp-force curves. Few machines, high value.
- **Tier-2 (commodity/legacy, the majority):** Modbus-if-lucky, else digital-IO/CT + the
  **vision moat**. Many machines, this is where Yaver wins vs Kepware/HighByte.

**QA loop (the harness-specific payoff):** integrate the **crimp-force monitor** (pass/fail
+ curve where exportable) and add **vision crimp inspection** (terminal seating, strip
length, presence) using the robot vision gate — gated against **IPC/WHMA-A-620 Rev E**
acceptability, with full Class-2/3 traceability written to `machineQuality`.

---

## 9. Talos integration (record / UI / recipe plane)

The bridge is **already built** — reuse, don't reinvent:
- Register each machine as a `machineEdgeDevices` row (`protocol`, `discoveryMode`, `caps`,
  `snapshot`); the agent heartbeats via `/machine-edge/heartbeat`, drains
  `machineEdgeCommands`, posts `/machine-edge/telemetry`, upserts the manual via
  `/machine-edge/manual`. (All BUILT in `machineEdge.ts` / `http.ts`.)
- **Order → job:** a kesim/BOM row → `machineRecipes` resolve
  (`/mobile/machine-edge/recipe-resolve`) → `{lengthMm, stripL/R, qty, crimpHeightMm,
  programSlot, applicatorId}` → enqueue `set_program`/`set_params`/`submit_job` → agent
  executes through the right `Driver` → read-back verify → ack + telemetry. This is the
  manufacturing-on-demand execution path (`[[project_mfg_on_demand]]`).
- **Live control stays P2P over the Yaver mesh** (low-latency `machine_*` ops via
  `proxyToDevice`); **Convex is the audit/OEE record** — same split as `[[feedback_p2p_only]]`.
- **Gap to close:** `talos_machine_*` MCP tools (status/recipe/resolve/manual/learn/load)
  are designed, not built — clone the `talos_ghost_*`/robotics tool family.

---

## 10. The vision moat (ghost operator for screen-only machines)

The substrate exists: the ghost engine reads screens + locates UI fields + (on PCs)
clicks/types; the robot vision gate proves the webcam→VLM verdict loop. `visionDriver`
composes them:
- **Camera-on-HMI** (cheapest, universal): a fixed cam or an old phone running the Yaver
  container points at the machine's HMI; periodic frame → VLM → `{state, count, program,
  alarmText}` → normalized `Sample`s into the UNS. Works on *any* machine with a screen and
  zero electrical access.
- **Industrial-PC HMI** (where the HMI is a Windows/CE PC): ghost screen-capture + locate,
  read directly (and, later, drive — frontier, interlock-gated).
- **Verify-gate for control:** after a program-recall or register write, the vision read of
  the HMI is the independent cross-check that the setpoint actually took — exactly the
  deterministic-cross-check pattern from `robot/control.go`.

This is the line Kepware/HighByte/Litmus can't cross. Lead with it.

---

## 11. Safety model (read-default, write-gated)

Defense in depth, outermost is hardware:
1. **Machine PLC interlocks + hardwired E-stop** — never bypassed, never network-writable.
   Reading the PLC is low-risk; writing a live tag bypasses its interlock logic, so:
2. **Writes are a granted capability**, not a default. `CapWrite`/`CapJob`/`CapProgram`
   must be explicitly enabled per machine (`--machine` flag + per-device grant).
3. **Bounds-check every write** against the manual's `safeRanges` — refuse out-of-range
   (never clamp-and-write), `allowHighRisk` opt-in for edge cases (existing `machine_write`
   discipline in `ops_machine.go`).
4. **Read-back verify** every write (Modbus read-back; vision-of-HMI cross-check).
5. **Approval gate** for high-risk params (`machineEdgeCommands.requiresApproval`, BUILT) —
   human OK before the agent executes.
6. **Simulation-first** where a digital twin exists (OPC 40570 / recipe dry-run) before
   touching the live machine.
7. **Audit** every command + result (Convex activity summary; full detail P2P).
8. **Owner-only** ops (`AllowGuest:false`), opt-in engine (`--machine`).

Rule: **the AI proposes; a deterministic, bounds-checked, approval-gated layer disposes;
the machine's own safety system is untouched.**

---

## 12. New ops verbs (self-registering, mesh-routable)

Same pattern as `ops_machine.go`/`ops_robot.go` (init → `registerOpsVerb`,
`AllowGuest:false`, `proxyToDevice`). The existing `machine_sniff_*`/`machine_read`/
`machine_understand`/`machine_write`/`machine_sync` stay; add the driver-level surface:

| Verb | Caps used | Purpose |
|---|---|---|
| `machine_connect` | — | open a `Driver` from a manual/profile (protocol, conn) |
| `machine_list` | — | enumerate wrapped machines + caps + status (the "view" list) |
| `machine_browse` | read | address space / register map / HMI fields |
| `machine_watch` | subscribe | start a subscription → UNS + telemetry stream (SSE to UI) |
| `machine_status` | status | live snapshot (extend existing) |
| `machine_recall` | program | recall a stored program (gated) |
| `machine_submit_job` | job | download a CSV/OPC 40570 cutting list (gated) |
| `machine_quality` | curve | crimp-force pass/fail + Cpk + curve pointer |
| `machine_vision_read` | vision | one-shot VLM read of the HMI |
| `machine_discover` | — | run a discovery mode → draft manual |

Mobile (`robotClient.ts` twin → `machineClient.ts`) and web get matching wrappers; the
"watch" wall is a new web/mobile surface (clone the robot UI + add OEE tiles).

---

## 13. Roadmap — easy → hard, wire-harness-first

**Phase 0 — the seam (no new hardware).**
Define `machine.Driver` + `CapSet`; wrap the existing Modbus engine as `modbusDriver`;
add `robotDriverAdapter` so the Ender already shows on a unified machine wall. Ship
`machine_list`/`machine_browse`/`machine_status` over the mesh. Prove one grid showing the
robot + a Modbus-TCP test slave.

**Phase 1 — Watch the commodity tier (highest ROI, lowest risk).**
`ioDriver` (CT clamp + signal-tower/cycle taps) + `visionDriver` (camera-on-HMI) on a
**YH-8030H / CST18D**. Live OEE status wall (web + mobile), downtime + reason codes, part
counts, AI-narrated state. Zero writes — pure observe. This alone is a sellable product and
the vision moat in action.

**Phase 2 — Read config + supervised-sniff discovery.**
`modbusDriver` read + the supervised-sniff loop (operator tags job → LLM labels registers →
verified Machine Operating Manual). View the live param set (length/strip/qty/speed/alarms).

**Phase 3 — Control (gated).**
Program-recall, then bounds-checked register write, read-back + vision verify, approval
gate. Order→recipe→job for the YH-8030H/CST18D. First real "control."

**Phase 4 — Tier-1 OPC-UA / OPC 40570.**
`opcuaDriver` (gopcua) + `fileDriver` (CSV→job, barcode scan) against a Komax/Schleuniger
line; native subscriptions; crimp-force curves via `machine_quality`; IPC-620 traceability.

**Phase 5 — UNS + fleet + QA vision.**
`mqttDriver` publishes every machine as a Sparkplug edge node into an ISA-95 UNS; fleet
wall across cells/lines; vision crimp inspection gated on IPC/WHMA-A-620; predictive
maintenance from CT/vibration signatures (MCSA/FFT — the same current signal that gives
utilization gives PdM).

**Phase 6 — phone-as-edge + GPU vision pool.**
Old phone running Yaver = the cell's camera + edge host; VLM reads run on the rented GPU
pool (`[[project_gpu_rental_orchestration]]`). One operator supervises N machines from a
phone — the production-floor end state.

---

## 14. First-contact verification checklist (⚠️ items)

1. **OPC 40570 actual machine support** — spec is 2025; confirm which of your machines
   really expose it vs need WPCS-via-software or Modbus. Build for graceful degradation.
2. **WPCS/MIKO** — wire format is NDA; get the SDK from Komax if native job-push is needed;
   otherwise go CSV→TopConvert→WPCS or OPC 40570.
3. **Crimp-force curve export** — proprietary; confirm QCenter/CFAlab export or OPC 40570
   `CrimpOutputDataType` availability per machine; else capture pass/fail + Cpk only.
4. **Commodity model numbers (CST18D/YH-8030H/CC36)** — unverified on vendor sites; confirm
   actual Modbus map / RS-485 pinout on the real units via sniff + `netcapture`.
5. **gopcua** — pin a version (README warns API can change); budget an upgrade pass.
6. **Sparkplug B Go** — no blessed lib; own the edge-node state machine.
7. **PLC write safety** — confirm which registers are safe to write vs interlock-critical
   before enabling `CapWrite` on any machine.

---

## 15. Risks

- **Heterogeneity is the work** — the seam is small; the long tail of drivers + per-machine
  manuals is the effort. Sequence by ROI (watch-first, commodity-first).
- **Vendor lock on tier-1** (Komax NDA WPCS/curves) — integrate through their software, don't
  fight it; the open path is OPC 40570 as it matures.
- **VLM-driving-HMI is frontier** — keep vision read-only for control until a hardware
  interlock backs a write path.
- **Write safety** — the only truly dangerous surface; gate hard, never bypass PLC interlocks.
- **Don't out-build the plumbing incumbents** — Kepware/HighByte/Ignition own generic data
  plumbing. Yaver's wedge is agent-native + AI-in-the-loop + phone form factor + the vision
  moat. Lead there, not on "another OPC server."

---

## 16. TL;DR

Add one abstraction — **`machine.Driver` + a capability set** — the generalization of
`robot.Backend`, and the whole floor becomes wrappable behind **view / watch / control**.
Modbus + sniff + AI-label + ghost HMI-vision + the Talos bridge already exist; the new work
is non-Modbus drivers (OPC-UA via gopcua, MQTT/Sparkplug, S7, file/WPCS, digital-IO/CT,
vision) + an OEE "watch" wall + a UNS publish path. Model data on **ISA-95/UNS + OPC 40570**
(the 2025 wire-harness standard); make **vision-of-the-HMI the moat** (wrap screen-only
machines nobody else can); keep **reads default-on, writes gated** (bounds + approval +
read-back, PLC interlocks untouched). Ship wire-harness-first: **watch the commodity tier
(YH-8030H/CST18D) → discover → gated control → tier-1 OPC 40570 → UNS/fleet/QA-vision →
phone-edge + GPU pool.**
