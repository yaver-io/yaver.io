# Yaver Box Lite: Raspberry Pi 4 USB Prototype Analysis

Status: quick-prototype decision memo, written 2026-06-18.

Scope: build a fast Yaver Box Lite using Raspberry Pi 4, USB industrial
peripherals, 24 V power, DIN/bench enclosure, and terminal blocks. The goal is a
working Modbus/RS485/OCPP/machine/robotics edge box without custom PCB and
without pretending it is certified industrial hardware.

V0 constraint: do not design or order a custom PCB, custom Raspberry Pi HAT,
custom terminal carrier, or custom motor-control board. Use off-the-shelf USB
adapters, DIN terminals, WAGO/lever connectors, 3D printed mounts, and temporary
low-voltage breadboard/perfboard only. A cheap Arduino/Pico-class USB companion
for Catpower is allowed because it is an off-the-shelf module, not a custom PCB.

Primary acceptance target: satisfy the first real project consumers:

- `../ocpp` / Kalkan:
  - Modbus RS485 energy meter reads;
  - charger/OCPP sidecar network reachability;
  - optional DI/DO for contactor/alarm/status;
  - reliable remote support over Yaver mesh.
- `../talos`:
  - `../talos/edge` / `tedge` runtime modes: `cst18d`, `ocpp`, `ender`,
    `screwdriver`;
  - screwdriver project;
  - CAT Power screwdriver through BTS7960/USB companion;
  - Ender/Marlin Cartesian robot experiments;
  - Simkab machine/robotics cells;
  - Fairino-style robotic arm/cobot supervision;
  - camera/HMI observation and bounded robot/machine jobs.

Yaver and Talos must both remain independently useful:

- Yaver-alone: Yaver agent runs Modbus/OCPP/machine/robot ops and remote support
  without Talos installed.
- Talos-alone: `tedge` runs `cst18d`, `ocpp`, `ender`, or `screwdriver` without
  Yaver installed.
- Interoperable: Yaver inventories/supports the box while `tedge` owns a
  specific active mode.

The hardware contract is shared, not exclusive: stable `/dev/serial/by-id/...`
paths, camera labels/URLs, Ethernet/Wi-Fi, terminal labels, and per-mode recipes.
Do not let either software stack monopolize the hardware design.

## 1. Short Answer

Yes: make the quick prototype with Raspberry Pi 4 plus USB peripherals.

Best first architecture:

```text
24 V DC DIN supply
  -> fuse / terminal blocks
  -> 24 V field IO modules
  -> isolated 24 V -> 5 V USB-C buck for Raspberry Pi 4

Raspberry Pi 4
  -> USB SSD for OS/logs
  -> powered USB hub if needed
  -> isolated USB-RS485 adapter (one now, second later)
  -> optional isolated USB-RS232 adapter
  -> optional isolated USB-CAN/CAN-FD adapter
  -> USB camera
  -> USB serial to Ender/Marlin or robot bridge
  -> USB screwdriver companion for Catpower/BTS7960

RS485 / RS232 / CAN / DI / DO / analog
  -> terminal blocks
  -> labeled field cables
```

Recommendation:

- Use USB industrial adapters first.
- Use plug-and-play HATs only when they clearly reduce wiring.
- Do not use a custom PCB in V0.
- Avoid raw GPIO for field IO in Prototype Lite.
- Make OCPP/Kalkan and Talos robotics pass before adding random extra sensors.
- Do not put mains AC wiring inside the first box unless it is a proper DIN
  enclosure build with fuse, strain relief, earth/PE handling, and safe spacing.
  For the first desk prototype, use an external certified AC/DC brick or DIN
  24 V PSU mounted safely.

## 2. Why Pi 4 Is Good Enough For Prototype Lite

Raspberry Pi 4 is not the ideal final platform, but it is good for a fast Yaver
Box Lite:

- already supported by Raspberry Pi OS;
- many people know how to debug it;
- USB ports are enough for serial/CAN/camera adapters;
- Ethernet and Wi-Fi/BLE are built in;
- Docker/Yaver agent/Go services are fine;
- USB SSD boot/logging is practical;
- old Pi 4 boards may already be on the shelf.

Do not over-index on Pi 4 performance. Prototype Lite is mostly IO-bound:

- Modbus RTU is slow;
- OCPP/Kalkan control loops are not CPU-heavy;
- DI/DO and relay IO are slow;
- machine observe/sniff/read workflows are not deep-learning workloads;
- camera snapshots are fine if we avoid heavy local vision.

Pi 4 becomes weak when we need:

- multiple camera streams;
- local VLM/AI;
- heavy browser automation;
- high-rate video capture;
- large offline inference;
- lots of containers.

For those, use CM5/RK3588S/x86 Max later.

## 2.1 First Project Fit

The first Pi 4 Yaver Box Lite should be judged by these concrete workflows, not
by abstract "industrial gateway" completeness.

### Kalkan / OCPP

Needs:

- RS485 Modbus RTU to read an energy meter or SmartEVSE-style board;
- Ethernet/Wi-Fi network path to existing chargers / OCPP controller;
- stable logs and remote access;
- optional DI/DO for alarm/contactor/status experiments.

Pi 4 USB build satisfies this with:

- RS485 adapter A = PZEM-6L24 energy meter / SmartEVSE / Modbus test bus;
- Ethernet = charger/site network;
- optional RS485 adapter B = second meter or IO module;
- optional DI/DO Modbus module = contact/status lab.
- Talos `tedge@ocpp` can use the same stable serial path.

Do not require CAN, RS232, robot IO, or analog for the first Kalkan demo.

### Talos Screwdriver / Ender Pro

Needs:

- USB serial to Ender/Marlin controller;
- USB camera looking at the work area;
- optional second serial/companion sensor for torque/current;
- optional tool output through Marlin E-stepper, servo, or isolated DO module;
- physical e-stop/power cut outside Yaver.

Pi 4 USB build satisfies this with:

- USB serial = Ender/Marlin motion backend;
- USB camera = view/verification;
- RS485/DI/DO module = tool/status experiments;
- USB companion + BTS7960B = Catpower screwdriver drive path;
- Yaver `robot_*` and `gcode_*` ops = remote control surface.
- Talos `tedge@ender` can use the same Ender serial path.
- Talos `tedge@screwdriver` currently expects Pi GPIO/pigpiod for the motor path;
  the boxed hardware should stay USB-companion-first, with Pi GPIO only as an
  internal lab fallback until `tedge` gets a USB companion backend.

This is the fastest Talos screwdriver prototype.

### Fairino / Robotic Arm

Needs:

- Ethernet to robot controller;
- camera;
- optional DI/DO/tool IO;
- API/bridge process running near the robot;
- strict safety boundary: robot controller remains the safety authority.

Pi 4 USB build satisfies early supervision:

- Ethernet = controller/API network;
- USB camera = workcell view;
- DI/DO = non-safety tool/status experiments;
- Yaver bridge = remote ops and telemetry.

It should not claim final motion-control quality until the Fairino backend/bridge
is implemented and tested.

### Simkab Machine / Robotics Cell

Needs:

- RS485/Modbus observe/read for machinery;
- camera/HMI observation;
- optional Ender/Fairino/screwdriver assist;
- Talos work-order context;
- bounded job handoff with read-back.

Pi 4 USB build satisfies the first cell with:

- RS485 A = machine bus;
- RS485 B = meter/IO/second machine bus;
- camera = HMI/workcell;
- USB serial/Ethernet = robot assist if present.

This is why the practical Lite build should have two RS485 ports, not one.

For the very first challenge, the second RS485 remains a convenience. Leave
physical terminal/panel space for it, but do not block V0 on buying it.

## 3. USB First vs HAT vs Raw GPIO

### Option A: USB Peripherals First

This is the recommended Prototype Lite path.

Use:

- isolated USB-RS485 adapters;
- isolated USB-RS232 adapter;
- USB-CAN/CAN-FD adapter;
- USB camera;
- USB SSD;
- powered USB hub if power budget gets messy.

Advantages:

- least board bring-up work;
- Linux sees stable `/dev/serial/by-id/...` paths;
- adapters can be swapped live during debugging;
- avoids GPIO pin conflicts between HATs;
- avoids kernel overlays for SPI/I2C UART/CAN chips;
- easy to replace a bad adapter in the field;
- easy to move same adapter stack to CM5, x86, or RK3588S later.

Disadvantages:

- physically uglier;
- USB hub/cable reliability matters;
- more enclosure wiring;
- per-port power and strain relief must be done properly;
- not as compact as a custom HAT/carrier.

Verdict:

Use USB first. Yaver Box value is the software/capability layer, not an elegant
PCB on day one.

### Option B: Terminal Block HAT / Industrial HAT

This is useful for the second quick build, especially if the HAT has:

- 24 V input;
- watchdog;
- opto-isolated DI;
- relay or transistor DO;
- RS485/Modbus;
- screw terminals;
- DIN mount.

Good classes of HAT:

- Sequent Microsystems industrial/building automation HATs;
- Waveshare isolated RS232/RS485/CAN/CAN-FD expansion kit;
- Monarco-style industrial IO HATs;
- Widgetlords/industrial IO modules if available.

Advantages:

- cleaner terminal-block UX;
- fewer USB dongles;
- may include watchdog/RTC/UPS features;
- stackable IO can be compact;
- easier to photograph/demo as an "industrial box".

Disadvantages:

- HATs use GPIO/SPI/I2C/UART pins and can conflict;
- overlays/drivers can be annoying;
- some HATs are not fully isolated;
- power path can backfeed the Pi if misunderstood;
- field replacement is harder than swapping a USB adapter;
- stacking multiple HATs can become mechanically fragile.

Verdict:

Use one well-chosen off-the-shelf HAT only if it solves
terminal-blocks/power/watchdog cleanly. Do not design a custom HAT for V0. Avoid
stacking a random tower of HATs for Prototype Lite.

### Option C: Raw GPIO To Terminal Blocks

Avoid this for field IO.

Raw Pi GPIO is 3.3 V logic. It is not industrial IO. It is not protected enough
for 24 V cabinets, motors, chargers, noisy RS485 buses, or unknown customer
wiring.

Raw GPIO is acceptable only for:

- internal status LEDs;
- an internal button;
- reading a known safe 3.3 V signal;
- controlling an internal relay module through an opto/driver board.

Raw GPIO is not acceptable for:

- direct 24 V inputs;
- direct relay coils;
- direct machine signals;
- safety functions;
- long wires leaving the enclosure.

Verdict:

Raw GPIO last. Use isolated USB/HAT/Modbus IO modules.

## 4. Power Architecture

### Preferred Prototype Power

Use a 24 V DC system inside the box:

```text
AC mains
  -> certified external 24 V adapter OR enclosed DIN 24 V PSU
  -> fuse
  -> 24 V distribution terminal blocks
  -> 24 V field IO modules
  -> isolated/non-isolated buck to 5 V USB-C for Pi 4
```

For desk prototype:

- easiest and safest: external 24 V DC brick;
- then 24 V terminal blocks inside;
- use a quality 24 V to 5 V buck with USB-C or a known-good Pi 4 power supply.

For DIN/cabinet prototype:

- Mean Well HDR-60-24 or similar 24 V DIN rail PSU;
- AC input behind covered terminals;
- fuse/breaker;
- PE/earth handled correctly;
- strain relief;
- no exposed mains.

### Pi Power

Best:

- power Pi 4 through USB-C with a 5.1 V / 3 A capable supply or 24 V -> 5 V
  USB-C buck.

Avoid:

- powering Pi through 5 V GPIO pins unless the design is controlled;
- cheap buck converters without brownout testing;
- sharing motor/relay noisy 5 V with Pi directly;
- backfeeding from powered USB hubs.

Add:

- Pi undervoltage monitoring;
- watchdog HAT or systemd watchdog;
- power loss/recovery test;
- short brownout test.

## 5. Terminal Block Strategy

Do not expose Pi pins directly as "industrial terminals". The terminal block
front panel should expose named interfaces:

```text
POWER
  +24V IN
  0V
  PE / SHIELD

RS485 A
  D+
  D-
  GND
  SHIELD
  TERM switch

RS485 B
  D+
  D-
  GND
  SHIELD
  TERM switch

RS232
  TX
  RX
  GND

CAN
  CAN_H
  CAN_L
  GND
  SHIELD
  TERM switch

DI
  DI1..DI4
  COM

DO
  DO1..DO4
  COM
```

Labeling matters more than the exact adapter brand.

Use ferrules for stranded wire. Use Wago/phoenix-style terminal blocks. Use
color:

- red: +24 V;
- black/blue: 0 V;
- green/yellow: PE;
- blue/white: RS485 pair;
- green/white: CAN pair.

## 6. Quick BOM

### Already Purchased Inventory

Only technical inventory is recorded here. Personal delivery/contact details
from order confirmations are intentionally not copied into the repo.

| Part | Qty | Unit Price | Line Total |
| --- | ---: | ---: | ---: |
| 10K 1/8W resistor | 50 | 0.21 TRY | 10.50 TRY |
| MCP3008 I/P DIP-16 ADC | 2 | 227.31 TRY | 454.62 TRY |
| 100nF 100V ceramic capacitor | 10 | 1.02 TRY | 10.20 TRY |
| 830-point breadboard | 2 | 56.83 TRY | 113.66 TRY |
| Industrial USB-to-RS485 converter | 1 | 936.73 TRY | 936.73 TRY |
| Raspberry Pi 4 5V 2.8A power adapter | 2 | 255.72 TRY | 511.44 TRY |
| Raspberry Pi 4 | 1 | already owned | price not recorded |
| Mean Well EDR-120-24 DIN 24V PSU | 1 | already owned | price not recorded |
| BTS7960B / IBT-2 20A motor driver | 1 | 385.62 TRY incl. VAT | 545.62 TRY incl. shipping |
| PZEM-6L24 3-phase power monitor + split CT | 1 | 2,820.00 TRY incl. VAT | 2,820.00 TRY |
| 2200uF 50V electrolytic capacitor | 10 | 14.10 TRY incl. VAT | 141.00 TRY |
| **Recorded priced total** |  |  | **5,543.77 TRY** |

What this already covers:

- one real RS485/Modbus path through the industrial USB-RS485 converter;
- one real Kalkan/OCPP energy-meter target through PZEM-6L24;
- Raspberry Pi 4 compute;
- 24 V DIN power through EDR-120-24;
- safe low-voltage bench analog experiments through MCP3008;
- Pi 4 bench power through the purchased adapters;
- motor/actuator bench experiments through BTS7960B;
- RC/filter/prototype parts and breadboard work.

Important boundaries:

- MCP3008 is for 0-3.3 V bench analog only unless protected/scaled/isolated.
  Do not connect 24 V, 0-10 V, 4-20 mA, motor lines, charger lines, or unknown
  machine wiring directly to it.
- PZEM-6L24 is valuable for Kalkan/OCPP energy monitoring, but any mains/3-phase
  wiring must be treated as qualified electrical work with enclosure, fusing,
  spacing, strain relief, and CT orientation discipline.
- BTS7960B is reserved for the CAT Power screwdriver path. It is not a safety
  motion controller and should not drive arbitrary machinery without independent
  power cut, fusing, current limits, and mechanical guarding.
- Breadboards are fine for MCP3008/logic experiments. Do not use breadboards for
  24 V power distribution, motor current, RS485 field wiring, or mains/3-phase.

### Minimum Useful Pi 4 Lite

| Part | Approx Cost |
| --- | ---: |
| Raspberry Pi 4, 4GB/8GB or existing board | already owned |
| Pi case/heatsink/DIN mount | $15-$40 |
| USB SSD or high-quality storage | $25-$60 |
| 24 V DC brick or DIN PSU | already owned |
| 24 V -> 5 V USB-C buck / Pi supply | $0-$40 |
| 1x isolated USB-RS485 | already purchased |
| Terminal blocks, ferrules, wire, labels | $30-$90 |
| Small enclosure / DIN rail plate | $30-$120 |
| Remaining total after owned/purchased parts | $100-$350 |

This is enough for:

- Modbus RTU meter;
- one machine RS485 bus;
- Kalkan/OCPP small demo;
- Yaver claim + remote ops;
- basic data logging.

### Practical Yaver Box Lite

| Part | Approx Cost |
| --- | ---: |
| Raspberry Pi 4, 4GB/8GB | already owned |
| USB SSD | $25-$60 |
| 24 V supply + buck + fusing | $25-$80 |
| second isolated USB-RS485 | $15-$60 |
| 1x isolated USB-RS232 | $15-$40 |
| 1x USB-CAN/CAN-FD | $35-$120 |
| 4-8 DI/DO Modbus module | $30-$90 |
| USB camera | $20-$80 |
| Ender/robot USB serial cable / bridge wiring | $5-$30 |
| powered USB hub if needed | $20-$60 |
| terminal blocks, enclosure, labels, harness | $80-$250 |
| Remaining total after owned/purchased parts | $245-$850 |

This is the recommended quick prototype.

For first OCPP + Talos satisfaction, CAN/CAN-FD and the second RS485 can both be
deferred. Do not defer the camera.

### HAT-Based Variant

| Part | Approx Cost |
| --- | ---: |
| Raspberry Pi 4 | $75-$130 |
| Waveshare isolated RS232/RS485/CAN/CAN-FD expansion kit | about $60-$100 |
| Sequent relay/industrial IO/watchdog HAT | $50-$100 |
| 24 V power + enclosure + terminals | $100-$300 |
| USB SSD/camera/extras | $50-$150 |
| Total | $335-$780 |

This looks cleaner but needs pin/driver validation.

This is not the first V0 path. The first path is PCB-free USB/DIN wiring.

### Remaining Shopping List

Minimum next buys:

- USB SSD or high-endurance storage;
- terminal blocks, ferrules, DIN rail or mounting plate, labels, wire;
- enclosure or open DIN test plate;
- fuses/fuse holder;
- USB camera;
- Ender/Marlin USB serial cable if not already available.
- Arduino Nano/Uno/Pico-class USB companion board for CAT Power screwdriver,
  unless using Pi GPIO fallback.

Strongly recommended next buys:

- Modbus RTU DI/DO module;
- powered USB hub with known-good power behavior;
- 24 V -> 5 V USB-C buck for final boxed power path, unless the Pi remains on
  its separate adapter during bench testing.
- second isolated USB-RS485 adapter only after the single-RS485 PZEM / JCWElec
  / machine path is proven.

Can wait:

- RS232 adapter;
- CAN/CAN-FD adapter;
- analog 0-10 V / 4-20 mA module;
- LTE modem;
- industrial HAT;
- custom PCB/carrier.

Do not buy or make the custom PCB/carrier for V0.

The second RS485 adapter is not critical for V0. Leave mounting and terminal
space for it, but prove the single RS485 path first.

## 7. Recommended Build Order

### Day 1: Bench Bring-Up

1. Pi 4 + Raspberry Pi OS Lite.
2. Enable SSH.
3. Install/run Yaver agent.
4. Use USB SSD if available.
5. Plug the purchased industrial USB-RS485 adapter.
6. Verify `/dev/serial/by-id/...`.
7. Connect a Modbus simulator, PZEM-6L24 on a safe bench setup, or another known
   Modbus device.
8. Run Yaver `machine_scan_registers` or direct test.
9. Optionally wire MCP3008 on breadboard for safe 0-3.3 V bench analog only.

### Day 2: Power + Enclosure

1. Add 24 V DC supply.
2. Keep purchased Pi adapter for bench, or add tested 24 V -> 5 V Pi power.
3. Add fused terminal blocks.
4. Mount Pi and adapters to DIN/plate.
5. Label RS485 A/B/GND/shield.
6. Add strain relief.
7. Reboot/power-cut test 20 times.

### Day 3: Multi-Interface

1. Add second RS485.
2. Add RS232.
3. Add CAN/CAN-FD.
4. Add DI/DO Modbus module.
5. Add USB camera.
6. Add the CAT Power screwdriver path through USB companion + BTS7960B with fused
   motor power, bulk capacitor, and physical kill switch.
7. Install or stage Talos `tedge` and verify one mode config can reference the
   same stable `/dev/serial/by-id/...` or camera path.
8. Generate `box_inventory`.
9. Run 24-hour soak.

## 8. What To Avoid

Avoid:

- raw GPIO directly to customer wiring;
- powering Pi from random buck without undervoltage tests;
- mains AC loose inside the box;
- multiple stacked HATs before pin conflict review;
- SD-card-only field logging;
- non-isolated adapters for unknown industrial buses;
- relay control of safety-critical loads;
- pretending this is a PLC.

## 9. Software To Develop For This Prototype

The Pi 4 Lite prototype should drive the next Yaver Box ops:

1. `box_inventory`
   - platform: raspberry-pi-4;
   - serial adapters by `/dev/serial/by-id`;
   - CAN interfaces;
   - cameras;
   - storage;
   - undervoltage/throttling status.

2. `box_interface_test`
   - RS485 loopback / known Modbus device;
   - PZEM-6L24 meter read;
   - RS232 loopback;
   - CAN loopback;
   - DI/DO test;
   - camera snapshot.

3. `box_modbus_scan`
   - safe scan of RTU/TCP unit IDs and registers;
   - rate limits;
   - read-only default.

4. `box_power_status`
   - Pi undervoltage;
   - uptime;
   - boot count;
   - storage health;
   - optional UPS/watchdog status.

5. `box_recipe`
   - saved wiring/profile plan per customer/site:
     - RS485 A = PZEM-6L24 meter;
     - RS485 B = machine;
     - camera = HMI;
     - DI1 = alarm dry contact.

6. `box_motor_test`
   - CAT Power screwdriver USB companion PWM/direction test;
   - requires external fused motor supply;
   - refuses arbitrary machine-control profile;

7. `box_tedge_status`
   - detect installed `tedge` version;
   - list active `tedge@*` systemd instances;
   - validate serial/camera paths referenced by `/etc/talos-agent/edge-*.json`;
   - warn when Yaver and `tedge` are configured to own the same serial port.
   - logs current/voltage if sensed through a protected input.

8. `box_software_mode`
   - reports `yaver-alone`, `talos-alone`, or `interop`;
   - records which runtime owns each serial/camera/motor path;
   - refuses to start a Yaver scan on a serial port already owned by an active
     `tedge@*` instance unless explicitly forced.

## 10. Final Recommendation

Build Prototype Lite as:

```text
Raspberry Pi 4
+ USB SSD
+ 24 V DIN/brick supply
+ 24 V -> 5 V USB-C Pi power
+ 2x isolated USB-RS485
+ 1x USB-RS232
+ 1x USB-CAN/CAN-FD (optional for first OCPP/Talos pass)
+ 1x Modbus DI/DO module
+ terminal blocks and labels
+ USB camera
+ USB serial to Ender/Marlin / robot bridge cable
+ enclosure / DIN plate
```

Do not use raw GPIO for field IO. Use USB peripherals for the first build. Add a
terminal-block HAT only if it has a clear power/watchdog/IO benefit and does not
create pin conflicts.

The business reason: this build can prove Yaver Box software, Modbus/OCPP/Talos
integration, mobile claim, remote debugging, and customer workflows in days. A
custom carrier/PCB should wait until the software tells us which ports are
actually important.

The product reason: this must be a neutral edge box. It should make Yaver better,
make Talos better, and still keep each one usable by itself.

## 10.1 First Acceptance Checklist

The first box is useful when all of these work:

- Kalkan/OCPP:
  - reads a Modbus RTU meter over RS485;
  - keeps stable network access to charger/OCPP side;
  - streams logs remotely through Yaver.
- Talos screwdriver:
  - opens Ender/Marlin USB serial;
  - opens CAT Power USB screwdriver companion;
  - returns `gcode_status`;
  - captures USB camera snapshot;
  - runs a dry robot jog/program with physical power cut available.
- Simkab machine:
  - lists two RS485 adapters by stable `/dev/serial/by-id`;
  - can sniff or scan a Modbus test device;
  - saves a wiring/profile recipe.
- Fairino early bridge:
  - reaches robot controller network;
  - captures cell camera;
  - exposes bridge/API health without taking over safety.

## 11. Source Anchors Checked

- Raspberry Pi 4 product page:
  https://www.raspberrypi.com/products/raspberry-pi-4-model-b/
- Raspberry Pi 2026 price increase / Pi 4 3GB announcement:
  https://www.raspberrypi.com/news/a-new-3gb-raspberry-pi-4-for-83-75-and-more-memory-driven-price-increases/
- Sequent Microsystems Raspberry Pi industrial automation HATs:
  https://sequentmicrosystems.com/collections/raspberry-pi-i-o-cards-for-industrial-automation
- Sequent Microsystems RS485 HAT notes:
  https://sequentmicrosystems.com/blogs/blog/rs485-port-for-raspberry-pi
- Waveshare isolated RS232/RS485/CAN/CAN-FD Raspberry Pi expansion coverage:
  https://www.cnx-software.com/2026/05/19/60-kit-transforms-the-raspberry-pi-4-5-into-a-din-rail-industrial-controller-with-isolated-rs232-rs485-and-can-bus/
- Waveshare isolated expansion board listing:
  https://www.amazon.com/Waveshare-Expansion-Compatible-Raspberry-Extending/dp/B0DFTMJYFB
