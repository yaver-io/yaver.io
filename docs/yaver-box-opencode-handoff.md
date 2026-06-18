# Yaver Box - OpenCode / GML Handoff

Status: handoff for continuing Yaver Box design and first prototype planning.
Written 2026-06-18.

## Goal

Build **Yaver Box**: one slightly large, premium, plug-and-play Linux hardware
box for fast physical-world prototyping, remote troubleshooting, robotics,
industrial machine monitoring, EV/OCPP work, home/appliance diagnosis, RF/debug,
and normal-person automations.

The first prototype is **not** a clean PCB product. It is a no-PCB max kit made
from off-the-shelf modules, USB adapters, terminal blocks, DIN parts, JST/Dupont
leads, screw terminals, fuses, and a flashed Yaver image. Optimize for interface
breadth and engineering speed first; reduce BOM and packaging later.

Canonical name: **Yaver Box**. Do not split this into separate IoT/robotics/EV
products yet. Those are profiles/use cases of the same box.

## Main Design Doc

Primary doc to continue:

- `docs/yaver-iot-prototype-facade-design.md`

That doc already contains the main product thesis, prototype packs, capability
manifest, ops surface, safety gates, mesh model, MCP orchestration, UI surfaces,
home/appliance cases, monetization, and first demos.

Important: this repo has a dirty worktree with many unrelated user changes. Do
not revert anything. The Yaver Box doc is currently untracked in this workspace.

## Product Thesis

Yaver Box is a **hardware facade**:

```text
machines / robots / meters / serial devices / cameras / depth/LiDAR / capture / SDR / LoRa
        -> no-PCB plug-in kit
        -> Yaver agent capability manifest
        -> Yaver mobile + web + relay + MCP/ops
        -> Talos, OCPP/Kalkan, robotics, home, yacht/car, and custom packages
```

It should let a user plug into a device, claim the box from Yaver Mobile, and
then let a remote engineer or AI agent observe, diagnose, automate, and, where
approved, safely control bounded hardware surfaces.

Yaver Box is not a safety PLC, not a deterministic motion controller, and not a
replacement for hardwired interlocks. Safety stays physical: E-stop, contactors,
fuses, guards, limit switches, safety relays, and PLC interlocks must work
without Yaver.

## First Prototype Strategy

Build the first internal box as **Yaver Box Max** in spirit, even if the public
name remains just Yaver Box.

Principles:

- one bigger/premium box is acceptable;
- no custom PCB for prototype 0;
- no solder-only construction;
- no loose breadboard as field wiring;
- use replaceable modules and labeled connectors;
- make every interface discoverable by software;
- normalize all devices behind a stable Yaver capability manifest;
- later split into smaller kits only after real usage proves which interfaces
  matter.

Suggested first physical parts:

- industrial Linux base: Raspberry Pi CM4/CM5 industrial carrier, Seeed
  reComputer R-class, RevPi-like hardware, or similar;
- 24 V input, DC/DC rails, fuses, power switch, current monitoring;
- powered USB hub inside the enclosure;
- Ethernet, Wi-Fi, BLE, cellular option;
- RS485, RS232, TTL UART, CAN/CAN-FD;
- Modbus DI/DO, analog input/output, relay modules;
- 3-phase meter and CT clamps;
- regular USB camera, optional global-shutter camera;
- depth camera and/or 2D LiDAR when useful;
- HDMI capture;
- RTL-SDR receive-only;
- LoRa module/dongle;
- GPS/GNSS module;
- logic analyzer / USB serial console;
- JST/Dupont/screw-terminal breakout area.

## Existing Yaver Anchors

Before implementing anything, verify code because repo docs may be stale.

Useful local files:

- `desktop/agent/ops_machine.go`
  - Existing machine verbs include status, sniffing, register scan, read, write,
    ports, understand, sync, and G-code ops.
- `desktop/agent/ops_machine_driver.go`
  - Uniform machine driver layer exists. Currently supports Modbus TCP and
    vision-like drivers; planned surface fits OPC-UA, MQTT, S7, etc.
- `desktop/agent/machine/driver.go`
  - Driver interface and capability flags: status/read/subscribe/write/program/
    job/vision/curve/estop.
- `desktop/agent/ops_robot.go`
  - Robot ops, serial/bridge, camera, external camera, vault-backed config.
- `desktop/agent/scheduler.go`
  - Scheduled tasks already support ops payloads, target devices/machines, cron,
    and repeat intervals. This is the core for normal-person automations.
- `docs/yaver-task-packages.md`
  - Portable scheduled work / agent-created package design.
- `mobile/app/provision-add.tsx`
  - Existing QR claim/provisioning path.
- `mobile/app/local-box.tsx`
  - Phone-as-box/local-box ideas.
- `backend/convex/mesh.ts`
  - Mesh model stores public keys/endpoints/IP while private keys remain local.
- `docs/mesh-mobile-tunnel.md`
- `docs/yaver-robot-fleet-mesh-design.md`
- `docs/yaver-hub-kit-radio-offgrid.md`
- `docs/robot-screwdriver-cell.md`
- `docs/yaver-robot-fixturing-fuju.md`

## Adjacent Repo Anchors

### Talos

Look in `../talos`.

Relevant areas found:

- `MACHINE_CAPACITY_FORECAST_DESIGN.md`
  - Names Simkab/JCWelec-style machines: CST18D, CC36, YH-8030H, JCW-2TP,
    JCW-F3.
  - Contains capacity/cycle-time/machine-monitoring thinking.
- `ROBOT_LEARNING_LOOP_DESIGN.md`
  - Screw-and-sense cell, Ender/tedge-style fixture, MCP3008 current/torque,
    HX711 force, per-screw trace.
- `robotics/wirecell/firmware/cell_runner.py`
  - Concrete screwdriver cell runner.
  - Uses Ender motion, BTS7960 screwdriver driver, CAT Power 36 V motor run at
    24 V, Mean Well EDR-120-24, MCP3008 current sensing, HX711 seat force, and
    pushes results to Talos.
- `web/src/app/api/screw-cell`
  - Screw-cell API surface.
- `web/src/app/api/machine-counter`
  - Video/cycle counting surface.
- `web/src/app/api/machine-edge`
  - Machine edge path.
- `web/src/app/api/manufacturing/machine-capacity`
- `web/src/app/api/manufacturing/machine-load`
- `web/src/app/dashboard/machine-effectiveness`
- `web/src/app/dashboard/machines/[machineId]`

### OCPP / Kalkan

Look in `../ocpp`.

Relevant concepts:

- Modbus meter scan;
- OCPP charger scan;
- EVSE RS485 scan;
- SmartEVSE Modbus driver;
- load balancing / building power management.

Yaver Box should become the generic physical edge for these, not only a
Kalkan-specific gateway.

## Screwdriver / Fastening Cell Requirement

Add this as a first-class Yaver Box use case.

Yaver Box should replace the one-off sidecar wiring for Talos screwdriving and
become the reusable controller/observer box for Ender/Fuju/Cartesian fastening
cells.

Required surfaces:

- USB serial to Marlin/GRBL/Ender controller for G-code;
- optional step/dir integration for Fuju-style external drivers;
- 24 V motor driver output for screwdriver motor, for example BTS7960-class
  driver, SSR, or industrial DC motor driver module;
- current sensing for torque/clutch inference, for example MCP3008 + sensor,
  INA-class sensor, or Hall current sensor;
- HX711/load-cell input for seat/force detection;
- USB/CSI camera and optional depth camera;
- fixture lighting output;
- limit switch and E-stop status inputs;
- relay/DO module for non-safety control lines;
- 24 V PSU, fusing, terminal blocks, strain relief, and physical E-stop outside
  Yaver.

Software shape:

```text
screw_cell_inventory
-> screw_cell_calibrate
-> screw_cell_run dryRun
-> operator approval
-> bounded gcode/motor/current/force/camera loop
-> per-screw trace artifact
-> Talos result sync
```

Use existing Yaver `robot_*`, `gcode_*`, camera, scheduler, and task-package
concepts. Add new ops only where the generic surface is missing.

## JCWelec / Simkab Machine Monitoring Requirement

Add this as a first-class Yaver Box use case.

Target machines from Talos context include:

- JCWelec CST18D;
- JCW-2TP;
- JCW-F3;
- Schleuniger CC36;
- Yuanhan YH-8030H;
- related Simkab cut/strip/crimp/test machinery.

The box should support multiple monitoring paths because brownfield machinery is
inconsistent:

- RS485/Modbus RTU;
- RS232 service ports;
- Ethernet Modbus TCP or vendor TCP;
- HMI camera/OCR when no API exists;
- HDMI capture where the UI is exposed;
- 3-phase meter/CT clamps for running/idle/load inference;
- DI from ready/fault/cycle contacts;
- vibration/IMU or microphone for cycle/fault inference if useful;
- camera-based cycle counting and part verification.

Useful workflows:

- discover ports and likely protocols;
- learn machine register maps safely;
- count cycles;
- infer running/idle/fault;
- measure machine seconds and utilization;
- correlate machine capacity with Talos schedules;
- report down/idle/fault to Yaver Mobile/Web;
- produce machine operating summaries;
- feed Talos machine capacity forecasting.

Safety:

- passive observe by default;
- active reads only after operator approval;
- writes are rare, bounded, audited, and require explicit per-machine
  permission;
- never bypass guards, E-stops, or machine safety logic.

## MCP / Backend Shape

Yaver MCP should expose boxes and hardware in a way agents can use without
guessing Linux device names.

Core resources:

```text
yaver://boxes
yaver://boxes/{boxId}/interfaces
yaver://boxes/{boxId}/machines
yaver://boxes/{boxId}/meters
yaver://boxes/{boxId}/robots
yaver://boxes/{boxId}/captures
yaver://boxes/{boxId}/rf
yaver://boxes/{boxId}/perception
yaver://boxes/{boxId}/data-pull-plans
yaver://boxes/{boxId}/closed-loop-runs
```

Core tools:

```text
box_list
box_inventory
box_selftest
box_data_pull_plan
box_data_pull_run
industrial_modbus_discover
industrial_meter_discover
industrial_machine_monitor_start
robotics_cell_inventory
robotics_screw_cell_run
ev_charger_discover
ev_meter_read
perception_snapshot
lora_send
sdr_probe
gps_location
dio_status
dio_set
```

Backend tables likely needed:

- `iotBoxes`;
- `iotBoxCapabilities`;
- `iotSites`;
- `iotAssets`;
- `iotDataPullPlans`;
- `iotRunSummaries`;
- `iotAuditEvents`.

Before adding tables, inspect existing Convex schema and Yaver device/session
tables.

## Mobile / Web UX

Mobile should cover:

- QR claim;
- onboarding wizard;
- box health;
- interface inventory;
- safe cable checklist;
- scan buttons;
- live camera/sensors;
- approve/deny write or motion gates;
- automations/routines;
- alerts.

Web should cover:

- dense engineering dashboard;
- port/capability table;
- machine/robot/meter asset map;
- logs and data pulls;
- camera/capture/perception panes;
- register/tag browser;
- charts for current, voltage, cycles, state, and faults;
- task package editor;
- audit trail.

Normal-person UX should avoid raw protocol language unless the user asks for
engineering mode. It should say things like "washing machine is drawing power
but drum motion is not detected" or "charger is limiting current because house
load is high."

## Mesh Requirement

Multiple Yaver Boxes should form a mesh:

- LAN discovery for nearby boxes;
- Yaver relay/mesh for remote reachability;
- optional LoRa for off-grid text/tiny telemetry;
- one box can act as site gateway for others;
- local private keys stay local;
- ACLs decide which box can read/control which asset.

LoRa is not for video, internet, or rich control. Use it for SOS, location,
tiny sensor state, and "box alive" messages.

## Home / Normal-Person Scope

Yaver Box should also work for normal-person troubleshooting:

- washing machine;
- fridge/freezer;
- kombi/boiler/heat pump;
- home electrical panel;
- EV charger;
- garage/gate/pump;
- car/boat/yacht monitoring.

For kombi/boiler/elevators and similar regulated/safety-sensitive systems,
Yaver Box should mostly observe. It may use documented APIs/protocols such as
OpenTherm only through approved adapters and safe setpoint operations. It must
not touch gas valves, burner safety circuits, elevator safety chains, or mains
internals as a casual user product.

## Next Work Items

1. Expand `docs/yaver-iot-prototype-facade-design.md` with dedicated sections
   for:
   - Yaver Box Max / premium first box rationale;
   - Talos screwdriver/fastening cell;
   - JCWelec/Simkab machine monitoring.
2. Convert the design into a first prototype BOM/checklist:
   - exact modules;
   - connector labels;
   - enclosure layout;
   - cable colors;
   - first-safe wiring diagrams.
3. Add a capability-manifest schema draft:
   - JSON example for interfaces;
   - JSON example for machine asset;
   - JSON example for screw cell;
   - JSON example for EV/meter site.
4. Add implementation issues or stubs for Yaver agent ops:
   - inventory/selftest;
   - serial/modbus/can/camera/capture/sdr/lora/gps probes;
   - perception snapshot;
   - data pull plan;
   - screw cell run;
   - machine monitor start/stop.
5. Map existing Yaver ops to Yaver Box tools so we do not duplicate:
   - `machine_*`;
   - `robot_*`;
   - `gcode_*`;
   - scheduler/task packages;
   - mobile provisioning;
   - mesh/relay.
6. Inspect Talos machine/screw APIs and write a small integration map:
   - what Yaver Box sends to Talos;
   - what Talos sends to Yaver Box;
   - artifact formats for traces, photos, current/force curves, cycle counts.
7. Keep all writes/controls permissioned:
   - observe;
   - passive bus;
   - active read;
   - bounded write;
   - motion.

## Non-Negotiables

- No PCB for prototype 0.
- No raw mains or industrial 24 V directly into Pi GPIO.
- No safety bypass.
- No hidden writes to PLC/machine/charger registers.
- No RF transmit by default except approved LoRa/BLE/Wi-Fi operations.
- No network scanning outside an explicitly selected local/site scope.
- Every control action must be auditable.
- The normal-person product must hide protocol complexity, but the engineering
  mode must expose enough detail for real work.

## Current External Product References

These are useful comparisons, not products to clone:

- Revolution Pi Connect 5: industrial Raspberry Pi style gateway.
- Seeed reComputer R1000: Raspberry Pi CM4 industrial gateway.
- CompuLab IOT-GATE-RPI4: industrial Raspberry Pi IoT gateway.
- WAGO Edge Devices: industrial edge controller family.
- Siemens SIMATIC IOT2050: industrial IoT gateway.
- Moxa IIoT gateways: industrial protocol gateway class.
- Advantech ADAM-6700 / EdgeLink: edge I/O gateway class.

Yaver's angle is not "another gateway." The angle is the unified Yaver
hardware facade plus mobile provisioning, relay/mesh, MCP, AI-assisted
diagnosis, task packages, robotics/perception loops, and normal-person
automation.
