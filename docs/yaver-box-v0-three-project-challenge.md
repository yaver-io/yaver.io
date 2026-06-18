# Yaver Box V0 Three-Project Challenge

Status: build target for the first reusable Yaver Box. Written 2026-06-18.

Goal: one physical Raspberry Pi 4 based Yaver Box that can be moved between
three real projects without rewiring its internals:

1. JCWElec listening / machine observe.
2. OCPP / Kalkan energy metering.
3. Simkab robotics / Ender screwdriver / Catpower screwdriver / Fairino early
   bridge.

This is the first challenge. Do not optimize for a generic "all ports" box
before these three pass.

## 0. Talos Project Map Checked

Checked local Talos sources:

- `../talos/MACHINE_RETROFIT_DESIGN.md`
- `../talos/agent/machine_agent/README.md`
- `../talos/agent/machine_agent/SETUP_CST18D.md`
- `../talos/ROBOT_LEARNING_LOOP_DESIGN.md`
- `../talos/docs/cell-vision-motor-tooling-cheap-stack.md`
- `../talos/edge/README.md`
- `../talos/edge/internal/config/config.go`
- `../talos/edge/internal/mode/mode.go`

Relevant Talos project surfaces:

| Talos area | Concrete target | What Yaver Box V0 must support |
| --- | --- | --- |
| Machine retrofit | JCWelec CST18D, later YH-8030H / 2TP / F3 / Schleuniger status-only | read-only RS485/Modbus sniff/read, recipe capture, optional signal/current status |
| Machine agent | `machine_agent` passive-read edge | Pi Linux, isolated USB-RS485, stable serial path, no write path by default |
| Robotics learning loop | Fairino FR3 + Ender-3 screw station | Ethernet to FR3/bridge, USB serial to Ender/Marlin, USB camera, remote recording |
| tedge edge runtime | `cst18d`, `ender`, `screwdriver`, `ocpp` modes | one Pi can run mode-specific systemd instances; hardware must expose stable serial/camera/network paths and keep configs per mode |
| tedge screw cell | screw-and-sense half of terminal-block cell | Ender/Marlin control, CAT Power screwdriver via USB companion/BTS7960 for Yaver V0, MCP3008 current-as-torque experiment, camera snapshot, result logging |
| Cheap cell stack | laptop/Pi host + USB cameras + G-code board | USB-first peripherals, off-the-shelf modules, no raw GPIO field wiring |

This confirms the V0 box should be **USB/RS485/Ethernet/camera first**. GPIO is
not a primary integration path.

There is no sibling `../tedge` checkout on this Mac. The verified source path is
`../talos/edge`, and the binary/runtime name is `tedge`.

## 1. Product Rule

One box first, three boxes later.

V0 should be a reusable field/lab box:

```text
same Pi 4
same Yaver agent
same 24 V DIN power
same RS485 path
same camera path
same terminal/label system
same USB/Modbus/Ethernet-first IO rule
different external cable/recipe per project
```

Hard V0 hardware rule: **no custom PCB**. Use bought modules, DIN terminals,
WAGO/lever connectors, USB adapters, cable labels, 3D printed mounts, and
temporary low-voltage breadboard/perfboard only. A small Arduino/Pico-class USB
companion is allowed for Catpower because it is an off-the-shelf development
board, not a custom carrier PCB. Do not design a Raspberry Pi HAT, custom
terminal carrier, or motor-control PCB until the three-project challenge passes.

Later product split:

- Yaver Box Energy: OCPP/Kalkan/metering.
- Yaver Box Machine: JCWElec/Simkab machine listening.
- Yaver Box Robotics: Ender screwdriver/Catpower screwdriver/Fairino/camera/IO.

V0 should not fork the hardware yet.

## 1.1 Yaver / Talos Interoperability Rule

Yaver and Talos are interoperable projects, not a parent/child dependency.

The same Yaver Box hardware should support three operating modes:

1. **Yaver-alone mode**
   - Yaver agent owns discovery, machine ops, recipes, remote support, and logs.
   - Talos is not installed or not running.
   - Works for OCPP/Kalkan, Modbus/machine observe, Yaver robotics ops, and
     general remote industrial support.

2. **Talos-alone mode**
   - `tedge` owns its selected mode: `cst18d`, `ocpp`, `ender`, or
     `screwdriver`.
   - Yaver agent is not required for the Talos production flow.
   - The box still uses the same physical wiring: stable serial paths, camera,
     Ethernet, labels, terminal blocks, and safe power.

3. **Interoperable mode**
   - Yaver supervises/inventories/remotely supports the box.
   - Talos/`tedge` owns the active machine or robot mode.
   - A recipe records port ownership so both processes do not fight over the
     same serial adapter.

Shared contract:

- hardware names are stable: `/dev/serial/by-id/...`, camera labels/URLs,
  interface labels, terminal labels;
- no code path assumes Talos is present just because it is a Yaver Box;
- no code path assumes Yaver is present just because it is a Talos edge cell;
- integration happens through explicit recipes, ports, systemd units, local HTTP,
  telemetry files, or command APIs;
- credentials stay in each project/runtime's local config and are never copied
  into this repo.

## 2. Current Owned Core

Already available:

- Raspberry Pi 4.
- Mean Well EDR-120-24 DIN 24 V PSU.
- Raspberry Pi 4 5 V adapters.
- industrial USB-RS485 converter.
- PZEM-6L24 3-phase power monitor with split CT.
- MCP3008 ADCs, resistors, 100 nF capacitors, breadboards.
- BTS7960B / IBT-2 motor driver.
- 2200 uF 50 V capacitors.

This is enough to start the challenge after adding storage, terminal hardware,
camera, fusing, and mounting.

Existing SD card is acceptable for V0 bring-up. Do not use it as the final log
or capture store. Keep logs small, ship logs remotely, and move to USB SSD when
the box starts running 24/7 or capturing video.

## 3. Physical Architecture

The box should be internally stable and externally reconfigurable.

Inside:

- Raspberry Pi 4 mounted on printed standoffs.
- EDR-120-24 mounted on DIN rail or fixed DIN clip, separated from low-voltage
  logic.
- industrial USB-RS485 converter clipped down.
- optional USB hub clipped down.
- terminal strip and strain relief.
- camera exits through USB or panel cable.
- breadboard/MCP3008 only on a removable lab tray, not permanent field wiring.
- Raspberry Pi GPIO is not exposed to field terminals by default.
- no custom PCB, custom HAT, or hand-soldered terminal carrier in V0.

External zones:

```text
LEFT:  POWER
  +24V, 0V, PE/shield, fuse

CENTER: RS485 / MODBUS
  A/D+, B/D-, GND, shield, TERM label

RIGHT: ROBOTICS / IO
  USB camera, Ender USB, optional DI/DO, optional motor bench output
```

The front panel should be labels-first:

- `POWER 24V`
- `RS485 A/B/GND`
- `PZEM / METER`
- `MACHINE / JCWELEC`
- `CAMERA`
- `ENDER / ROBOT`
- `CATPOWER SCREWDRIVER`
- `BENCH MOTOR ONLY`

## 4. Project Acceptance Tests

### Challenge A: JCWElec Listening

Purpose: listen/observe a JCWelec CST18D-style machine bus without controlling
it. Talos already frames CST18D as the first open/readable machine target.

Minimum wiring:

- Pi 4 -> industrial USB-RS485 -> terminal block -> machine RS485 tap.
- Optional camera aimed at HMI/status panel.

Pass criteria:

- Linux sees the adapter under `/dev/serial/by-id/...`.
- Yaver `machine_ports` lists it, or Talos `machine_agent --doctor` sees it.
- Yaver can open a passive/manual or active read-only Modbus session.
- Talos `machine_agent` can run in read-only/sniffer mode on the same class of
  Pi box.
- Talos `tedge@cst18d` can use the same stable USB-RS485 path and config shape.
- Yaver records a machine recipe:
  - project = `jcwelec-listening`;
  - Talos machine key = `cst18d` for the first target;
  - RS485 adapter ID;
  - baud/parity/unit guesses;
  - camera label if used.
- No writes are possible in the default recipe.

Useful ops:

- `machine_ports`
- `machine_sniff_start`
- `machine_sniff_status`
- `machine_sniff_stop`
- `machine_scan_registers`
- `machine_understand`
- `machine_connect`
- `machine_read_tags`

### Challenge B: OCPP / Kalkan

Purpose: read real 3-phase energy data for Kalkan/OCPP load-balancing work.

Minimum wiring:

- Pi 4 -> industrial USB-RS485 -> PZEM-6L24.
- EDR-120-24 powers the 24 V side where appropriate.
- Charger/OCPP path over Ethernet or Wi-Fi.

Pass criteria:

- PZEM responds over Modbus RTU.
- Yaver reads voltage/current/power/energy registers.
- Kalkan/OCPP side can consume or mirror the meter values.
- Talos `tedge@ocpp` can run against the same meter path when needed.
- Box keeps remote logs visible through Yaver.
- The recipe records:
  - project = `kalkan-ocpp-meter`;
  - meter model = `PZEM-6L24`;
  - RS485 adapter ID;
  - unit ID and register map;
  - CT orientation notes.

Safety:

- Treat mains/3-phase as qualified electrical work.
- CT and voltage wiring must be enclosed, fused, labeled, and strain-relieved.
- V0 can test with safe bench conditions first.

Useful ops:

- `box_profiles`
- `box_profile_plan` with `ocpp` / `kalkan`
- `box_bom`
- `machine_scan_registers`
- `machine_connect`
- `machine_read_tags`

### Challenge C: Simkab Robotics

Purpose: use the same box for robot/camera/machine assistance. Talos concrete
robotics targets are the terminal-block cell, Fairino FR3, Ender-3 screw
station, and tedge screw-and-sense flow.

Minimum wiring:

- Pi 4 -> USB camera.
- Pi 4 -> Ender/Marlin USB serial for screw station / tedge-compatible path.
- Pi 4 -> USB screwdriver companion -> BTS7960 -> CAT Power screwdriver motor.
- Pi 4 -> Ethernet to Fairino FR3 controller or bridge.
- Optional RS485 path for a machine/IO device.
- MCP3008 current sensing for screwdriver torque/clutch-slip experiments.
- BTS7960B is part of the Catpower screwdriver path, not just a generic bench
  motor driver.

Pass criteria:

- Camera snapshot works.
- Ender/Marlin path returns G-code status.
- Talos `tedge@ender` can use the same Ender serial path.
- Talos `tedge@screwdriver` can either run in its current Pi-GPIO form or be
  adapted to the USB companion command path without changing box wiring.
- USB screwdriver companion answers `PING`, `STATUS`, `ENABLE`, `DRIVE`, and
  `BRAKE`.
- CAT Power motor can run at 24 V through BTS7960 with soft-start, bulk cap, and
  physical kill/fuse.
- Fairino bridge/controller health is reachable when used.
- Yaver can run a dry robot jog/program with physical power cut available.
- A tedge-compatible screw trace can be represented at least as `{ok, reason,
  peak_a, ms}` once the motor/current path is wired.
- Recipe records:
  - project = `simkab-robotics`;
  - robot backend = `ender-marlin` or `fairino-bridge`;
  - screwdriver driver = `usb-companion-bts7960-catpower`;
  - camera ID;
  - tool/motor output marked Catpower screwdriver only, not generic machine
    control.

Useful ops:

- `box_profile_plan` with `ender`, `fairino`, or `simkab`
- `robot_status`
- `robot_snapshot`
- `robot_config_set`
- `robot_jog`
- `robot_run`
- `gcode_open`
- `gcode_status`
- `gcode_estop`

## 4.1 tedge Compatibility Contract

Yaver Box V0 must be able to host Talos edge runtime (`tedge`) beside the Yaver
agent. Treat `tedge` as a first-class workload, not a separate hardware product.

Verified `tedge` modes from `../talos/edge`:

| tedge mode | Yaver Box role | Required hardware path |
| --- | --- | --- |
| `cst18d` | JCWElec read-only machine listening | stable `/dev/serial/by-id/...` USB-RS485 |
| `ocpp` | PZEM/charger load-balance experiments | stable USB-RS485 meter path, optional charger path |
| `ender` | Ender/Marlin Cartesian robot | stable USB serial to Marlin, optional camera URL |
| `screwdriver` | Ender + screw motor + sensing | Ender serial/camera plus Catpower motor path |

Runtime rules:

- install/run `tedge` as a normal Linux service on the Pi;
- use systemd template instances such as `tedge@cst18d`, `tedge@ender`,
  `tedge@screwdriver`, and `tedge@ocpp`;
- keep per-mode configs under `/etc/talos-agent/edge-<mode>.json` or a matching
  runtime-managed path;
- never store API keys or customer connection details in this repo;
- use `/dev/serial/by-id/...` paths in configs and recipes, not unstable
  `/dev/ttyUSB0` assumptions;
- avoid two processes opening the same serial adapter at the same time: Yaver
  recipe mode should mark the port owner as `yaver`, `tedge`, or `manual`;
- expose camera as a local URL or device path that both Yaver and `tedge` can
  reference by recipe;
- Yaver can supervise, inventory, and remote-support the box while `tedge` owns a
  specific machine/robot mode.

Important V0 deviation from current `tedge` screwdriver implementation:

- current `tedge` screwdriver code expects pigpiod/GPIO for BTS7960 and optional
  MCP3008/HX711 sensing;
- Yaver Box hardware should still be wired for the USB companion first, because
  the user wants the Raspberry Pi side clean;
- if current `tedge@screwdriver` is used before software adaptation, the Pi-GPIO
  wiring remains an internal lab fallback only and must not be exported to field
  terminals;
- the correct software follow-up is a `tedge` screwdriver backend option like
  `driver:"usb_companion"` using the same serial protocol documented in
  `firmware/screwcell/usb_companion/`.

## 5. Wiring Philosophy

V0 has one primary RS485 channel. That is acceptable.

Use Raspberry Pi GPIO as little as possible.

Preferred IO order:

1. USB peripherals:
   - USB-RS485;
   - USB camera;
   - USB serial to Ender/Marlin;
   - USB screwdriver companion;
   - USB hub if needed.
2. Ethernet/network APIs:
   - OCPP/charger side;
   - Fairino bridge/controller;
   - local Yaver agent/mesh.
3. Modbus RTU/TCP IO modules:
   - DI/DO;
   - relay;
   - analog;
   - meter.
4. Raspberry Pi GPIO only for internal, low-voltage, non-field use:
   - status LED;
   - local button;
   - lab-only MCP3008 SPI experiment if no USB companion is available;
   - internal enable line through a protected driver.

Do not route raw GPIO to customer/machine terminals. If GPIO is used at all, it
must stay inside the enclosure, remain 3.3 V logic, and go through protection or
an opto/driver board before touching anything noisy. Never use Pi GPIO as the
safety path, motor power path, machine output, or 24 V input.

Catpower screwdriver exception:

- The screwdriver needs PWM/direction/current sensing. Prefer a **USB companion**
  board so the Pi side stays clean.
- Best low-cost companion choices:
  - Arduino Nano/Uno compatible board over USB serial;
  - Raspberry Pi Pico over USB serial;
  - later, a dedicated USB DC motor controller if we want a packaged module.
- Generic USB GPIO bridges like FT232H/MCP2221 are useful for simple GPIO/I2C/SPI,
  but they are not the best primary screwdriver controller because the screwdriver
  needs local soft-start, braking/dead-time, and a watchdog.
- Industrial USB/Modbus DI/DO modules are good for slow field IO and status
  inputs, not high-rate screwdriver PWM.
- The companion owns:
  - RPWM/LPWM/R_EN/L_EN;
  - soft-start ramp;
  - brake/dead-time;
  - watchdog timeout;
  - optional ADC/current sensing.
- The Pi owns only:
  - USB serial command/telemetry;
  - logging;
  - Yaver/Talos recipe/run orchestration.
- Existing Pi-GPIO `firmware/screwcell/screwdriver_control.py` remains a fallback,
  not the preferred boxed V0 path.

No-custom-PCB detail:

- no custom Raspberry Pi HAT;
- no custom terminal-block PCB;
- no custom BTS7960 carrier;
- no PCB-mounted relay/IO design for V0;
- no ordering JLCPCB/PCBWay boards until the three-project challenge passes;
- use DIN blocks, ferrules, WAGO 221, USB modules, and 3D printed retainers
  instead.

Talos alignment:

- CST18D machine-agent path wants isolated USB-RS485, not raw GPIO.
- OCPP/PZEM path wants RS485/Modbus, not raw GPIO.
- Ender path wants USB serial/G-code, not raw GPIO.
- Fairino path wants Ethernet/API, not raw GPIO.
- CAT Power screwdriver should use USB companion first; MCP3008 on Pi SPI is only
  fallback and stays internal/low-voltage.

Use recipes and labels to switch roles:

```text
Recipe A: JCWElec
  RS485 = machine listen/read
  Camera = HMI

Recipe B: OCPP
  RS485 = PZEM-6L24
  Ethernet/Wi-Fi = charger/OCPP network

Recipe C: Simkab robotics
  RS485 = optional machine/IO
  USB = Ender/Marlin
  Camera = workcell
```

Second RS485 is a later convenience, not a V0 blocker. Leave panel space and
mounting holes for it.

## 6. Mechanical Design Requirements

3D printed parts should make the box reusable without custom PCB:

1. Base plate:
   - mounts Pi 4;
   - reserves EDR-120-24 / DIN rail zone;
   - clips USB-RS485 adapter;
   - has cable tie slots;
   - fits inside a generic enclosure or on an open DIN test plate.

2. Terminal panel:
   - printed label strip;
   - screw holes for common terminal blocks;
   - zones for power, RS485, camera/robot, bench motor.

3. USB adapter clips:
   - generic clip for RS485 dongle;
   - generic clip for USB hub;
   - cable tie backup.

4. Strain relief comb:
   - keeps RS485, camera USB, Ender USB, and power leads from pulling on boards.

5. Lab tray:
   - removable breadboard/MCP3008 tray;
   - clearly marked `LOW VOLTAGE ONLY`.

Initial printable files live in:

- `hardware/yaver-box-lite-v0/base_plate.scad`
- `hardware/yaver-box-lite-v0/terminal_panel.scad`
- `hardware/yaver-box-lite-v0/usb_adapter_clip.scad`
- `hardware/yaver-box-lite-v0/lab_tray.scad`

These are parametric placeholders. Measure the actual EDR-120-24, USB-RS485
adapter, terminal blocks, and enclosure before final printing.

## 7. Minimum Remaining Build Items

Needed now:

- USB camera.
- SD card can be used now; USB SSD later.
- terminal blocks.
- ferrules.
- wire labels / heat-shrink labels.
- fuse holder / inline fuse.
- enclosure or open DIN plate.
- cable glands or strain reliefs.
- Ender/Marlin USB cable if not already available.

Cable bundle simplification buys:

- ferrule crimp kit:
  - insulated bootlace ferrules, 0.25 / 0.5 / 0.75 / 1.0 / 1.5 mm2;
  - ratcheting ferrule crimper;
  - makes terminal-block wiring reliable and repeatable.
- DIN rail terminal block starter kit:
  - feed-through grey terminals;
  - PE/earth terminals;
  - end stops/end covers;
  - jumper bars/bridges;
  - marker strips.
- WAGO 221 lever connector assortment:
  - 2-port, 3-port, 5-port;
  - useful for quick service loops and temporary split/join inside the box;
  - still label everything.
- cable labels:
  - heat-shrink labels if possible;
  - otherwise wrap-around wire markers plus heat-shrink protection.
- cable glands / strain relief:
  - PG7 / PG9 / PG11 or M12 / M16 gland assortment;
  - rubber grommets for USB/camera exits.
- braided sleeve / spiral wrap:
  - 6 mm / 10 mm / 15 mm mixed sizes;
  - keeps USB, RS485, and screwdriver wires bundled but serviceable.
- keyed connector kits:
  - JST-XH for low-voltage internal signals;
  - Molex Micro-Fit 3.0 or similar locking connector for Catpower/BTS7960 logic
    and low-voltage power pigtails;
  - do not use Dupont jumpers for permanent moving/field wiring.
- ready pigtails:
  - `PZEM_RS485`;
  - `JCWELEC_RS485`;
  - `ENDER_USB`;
  - `CATPOWER_DRV`;
  - `CAMERA_USB`;
  - each with the same label at both ends.

Useful soon:

- Modbus RTU DI/DO module.
- powered USB hub.
- 24 V -> 5 V USB-C buck for final single-supply box.
- second RS485 adapter after V0 passes.

## 8. Go / No-Go

The single box passes V0 when it can complete:

```text
JCWElec:  see RS485 adapter -> sniff/read Modbus safely -> save recipe
OCPP:     read PZEM-6L24 over RS485 -> expose meter values -> save recipe
Simkab:   camera snapshot + Ender/Fairino health -> dry robot run -> save recipe
```

Only after those pass should we split the design into the later three boxes.
