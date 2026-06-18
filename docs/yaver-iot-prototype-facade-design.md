# Yaver Box - No-PCB Prototype And Hardware Facade Design

Status: design for first personal bench prototype. Written 2026-06-18.

## Naming

Canonical product/context name: **Yaver Box**.

"IoT", "industrial", "robotics", "EV", "home", "car", and "yacht" are profiles
or use cases of Yaver Box, not separate products. The same box concept should
scale from a normal person checking a car/boat/home sensor to an engineer
debugging Simkab machinery or a Talos robot cell.

## 0. Thesis

Yaver Box is not a PLC, not a safety controller, and not another generic
Modbus-to-MQTT gateway.

It is a reusable hardware facade for fast engineering-phase work:

```text
machines / robots / meters / serial devices / cameras / depth/LiDAR / capture / SDR / LoRa
        -> plug-in no-PCB kit
        -> Yaver agent capability manifest
        -> mobile + web + relay + MCP/ops
        -> Talos, OCPP/Kalkan, robotics, and custom project packages
```

The first prototype is for us first, not a BOM-locked product. It must be built
from off-the-shelf modules, jumper wires, terminal blocks, JST leads, Dupont
leads, USB adapters, DIN-rail parts, and a flashed Yaver image. There is no
strict budget or final BOM at this stage. The first rig should be intentionally
over-capable so we can learn which interfaces matter, then later shrink it into
clean sellable kits.

The product advantage is speed:

- ship one box to a site;
- the user plugs labeled cables into the machine, robot, or fixture;
- the user claims the box from Yaver Mobile;
- the remote engineer or AI agent sees stable capabilities, not random Linux
  devices;
- safe reads work immediately;
- writes and motion require explicit gates.

## 1. Product Boundary

Yaver Box is:

- a remote engineering appliance;
- a normal-person hardware assistant box for cars, yachts, homes, workshops,
  machines, and field gear;
- a physical-world prototyping kit;
- a machine and robotics wrapper;
- a protocol and sensor facade;
- an RF/field-debug bench with SDR, LoRa, BLE, Wi-Fi, and cellular options;
- a perception box with regular cameras, depth cameras, and LiDAR where useful;
- a closed-loop remote development target for Talos, OCPP/Kalkan, robotics, and
  Simkab machinery;
- a mobile-provisioned Linux box;
- a safe way for Yaver agents to observe and operate approved hardware surfaces.

Yaver Box is not:

- a certified safety PLC;
- a deterministic motion controller;
- a replacement for hardwired interlocks;
- a product that freely scans customer networks;
- a product that silently writes PLC registers;
- a custom-board company on day one.

The product should have two faces:

```text
engineering face:
  ports, registers, traces, captures, data pulls, closed-loop runs

normal-person face:
  "what is happening?", "turn this on", "is my boat/car/machine ok?",
  "show me the camera", "where is the box?", "alert me if something changes"
```

The same hardware facade powers both. The UI and permission language change by
audience.

Hard rule: safety stays physical. E-stop, contactors, fuses, guards, limit
switches, safety relays, and PLC interlocks must work without Yaver.

## 2. Similar Product Map

The market already has useful hardware. Yaver should use it where possible.

### Industrial Raspberry Pi / Linux Gateways

Examples: Revolution Pi, Seeed reComputer R series, CompuLab IOT-GATE-RPI,
OnLogic Factor, MyPi Industrial IoT Gateway.

Strengths:

- real industrial power input and enclosure;
- DIN rail mounting;
- RS485/RS232/CAN options;
- eMMC/NVMe instead of weak SD-card deployments;
- better thermal and EMC discipline than hobby boards.

Gaps Yaver fills:

- mobile QR claim;
- Yaver relay and remote reachability;
- AI-agent usable capability surface;
- one facade across machines, robots, cameras, capture, SDR, and project code;
- project-specific packages for Talos/OCPP/robotics.

### PLC / Industrial Automation Platforms

Examples: Bosch Rexroth ctrlX, WAGO Edge Controller, Siemens IOT2050.

Strengths:

- mature industrial ecosystems;
- PLC, fieldbus, motion, HMI, and app stores;
- stronger lifecycle and certification story.

Yaver should not fight them on deterministic control. Yaver sits beside them as
the remote engineering layer, brownfield wrapper, lab facade, and AI-assisted
integration surface.

### IIoT Protocol Gateways

Examples: Moxa, Advantech WISE/ADAM, Node-RED gateways.

Strengths:

- Modbus/OPC-UA/MQTT data movement;
- cloud connectors;
- low-code integration.

Yaver differs because it is not only a data pipe. It connects physical
interfaces to mobile provisioning, Yaver ops, MCP tools, remote debugging,
camera/capture, robot control, and project packages.

## 3. First Prototype Principle

Prototype 0 is intentionally ugly inside the box.

Allowed:

- jumper wires;
- JST-XH / JST-PH leads;
- Dupont leads;
- Wago lever nuts;
- screw terminal breakout boards;
- USB adapters;
- DIN terminal blocks;
- adhesive mounts;
- cable ties;
- printed labels;
- fused 24 V and 5 V rails;
- off-the-shelf isolated modules.

Not allowed in Prototype 0:

- custom PCB;
- solder-only permanent wiring;
- unlabeled wires;
- mains wiring directly on loose breadboard;
- raw Pi GPIO connected directly to industrial voltages;
- safety-critical loads controlled only through Yaver;
- field deployment without strain relief and fusing.

The first prototype is a facade validation rig, not final electrical hardware.

## 4. Prototype SKUs

### 4.1 Base Kit

Purpose: every Yaver Box starts here.

Parts:

- industrial Linux computer: CM5/CM4 industrial gateway or small x86 box;
- eMMC or NVMe storage;
- 24 V DC input if possible;
- 5 V DC buck converter if using Pi-style modules;
- Ethernet;
- Wi-Fi/BLE;
- DIN rail enclosure or bench enclosure;
- QR label with provision claim;
- Yaver image flashed;
- one USB hub with per-port power switches if needed.

Required first-boot behavior:

- start `yaver serve`;
- advertise bootstrap beacon when unclaimed;
- expose `/health`;
- expose `/info`;
- expose `/ops/verbs`;
- produce hardware inventory;
- accept mobile claim;
- survive reboot without SSH.

### 4.2 Industrial Interface Pack

Purpose: machines, PLCs, meters, chargers, and electrical cabinets.

Parts:

- 2x isolated USB-RS485 adapters;
- 1x USB-RS232 adapter;
- 1x USB-CAN or CAN-FD adapter;
- 1x Modbus RTU 3-phase energy meter;
- CT clamps matched to meter inputs;
- 1x Ethernet switch, preferably DIN rail;
- 1x DI/DO module over Modbus RTU/TCP;
- optional analog module for 0-10 V and 4-20 mA;
- terminal blocks for A/B/GND/shield;
- 120 ohm termination plugs or switchable terminators.

### 4.3 Robotics / Fixture Pack

Purpose: robot cells, CNC, jigs, grippers, lab automation.

Parts:

- USB camera or CSI camera;
- USB HDMI capture card;
- USB relay module or Modbus relay module;
- USB serial TTL adapter;
- RS232 adapter;
- stepper/servo controller only for non-safety bench rigs;
- torque/current sensor companion over serial or I2C bridge;
- E-stop input to a DI module;
- fixture lighting;
- optional USB microscope.

### 4.4 Lab / RF / Debug Pack

Purpose: hardware product debugging.

Parts:

- RTL-SDR;
- LoRa USB dongle or SPI LoRa module via a USB/SPI bridge;
- sub-GHz transceiver module where legally usable;
- USB GPS/GNSS module;
- USB logic analyzer;
- USB serial console cables;
- HDMI capture;
- USB camera;
- BLE/Wi-Fi adapter with good Linux support;
- optional cellular modem.

### 4.5 Perception / Robotics Sensor Pack

Purpose: closed-loop robotics, machine state perception, fixture calibration,
and remote vibe development where the AI needs physical feedback.

Parts:

- regular USB camera;
- global-shutter camera if motion blur becomes a problem;
- depth camera, for example RealSense/OAK-class hardware if Linux support is
  acceptable;
- 2D LiDAR for robot/fixture mapping, presence, distance, and safety-adjacent
  observation;
- time-of-flight distance sensor modules for cheap single-point distance;
- IMU module for vibration/tilt when useful;
- fixture lighting;
- calibration board / fiducial tags / AprilTags;
- rigid camera/LiDAR mounts.

Depth and LiDAR are optional. Add them when they improve calibration,
collision-adjacent observation, part localization, or machine-state sensing. Do
not make them required for the first kit.

### 4.6 Personal Max Prototype Pack

Purpose: our first internal "more than Revolution Pi" bench box. This is not the
sellable BOM. It is the learning rig for Talos robotics, OCPP/Kalkan, Simkab
machines, and random hardware projects.

Include as much as is useful:

- base industrial Linux box;
- powered USB hub;
- multiple RS485 adapters;
- RS232;
- CAN/CAN-FD;
- Modbus DI/DO;
- Modbus analog input/output;
- 3-phase meter and CT clamps;
- USB camera;
- depth camera if available;
- 2D LiDAR if available;
- HDMI capture;
- RTL-SDR;
- LoRa dongle/module;
- GPS/GNSS module;
- BLE/Wi-Fi adapter;
- cellular modem if available;
- USB serial console;
- relay module;
- logic analyzer;
- spare JST/Dupont/screw-terminal breakouts.

The personal max prototype is allowed to be physically larger and messier than
the future kit. Its job is to validate the facade and closed-loop remote
development, not prove industrial packaging.

This first box should bias toward breadth over cost. A slightly bigger and more
expensive internal Yaver Box is the right first move because the expensive thing
in early robotics, OCPP, machine, and appliance projects is engineering time,
not the extra USB adapter or DIN module. The product can later split into
smaller SKUs after the real interface mix is known.

Design consequence:

- build the first box as a premium max kit, not a cheap minimal gateway;
- leave physical room for adapters, terminal blocks, powered USB, fuses, and
  cable strain relief;
- make every interface discoverable by software, even if it is just a USB
  dongle in prototype 0;
- prefer replaceable plug-in modules over hidden custom wiring;
- treat the max box as the reference hardware for MCP tools, docs, demos, and
  early customer work.

## 5. Physical Layout For No-PCB Prototype

Use a two-zone enclosure.

```text
+-------------------------------------------------------------+
| LOW VOLTAGE / COMPUTER                                      |
|                                                             |
|  [Linux box] [USB hub] [Wi-Fi/BLE] [LoRa] [SDR] [capture]   |
|  [camera] [depth/LiDAR adapter if USB]                      |
|                                                             |
|  USB cables are short, strain-relieved, and labeled.         |
|                                                             |
|-------------------------------------------------------------|
| FIELD I/O / TERMINALS                                       |
|                                                             |
|  [24V IN fuse] [5V buck] [RS485 TB] [RS232 TB] [CAN TB]     |
|  [DI/DO TB]   [meter TB] [CT inputs] [camera/capture ports] |
|                                                             |
|  All field wires enter here. No loose breadboard.            |
+-------------------------------------------------------------+
```

Rules:

- every cable gets a printed label at both ends;
- every USB adapter gets a stable physical port label;
- every serial adapter must be referenced by `/dev/serial/by-id`, never by
  `/dev/ttyUSB0` in saved config;
- each bus gets A/B/GND/shield labels;
- RS485 termination must be explicit;
- CT clamps must have direction arrows and phase labels;
- field wiring must have strain relief;
- 24 V and mains-adjacent wiring must be separated from USB/jumper wiring;
- prototype screenshots and photos must be part of the build record.

## 6. Connector Strategy

Prototype connectors are not the final customer connector, but they must be
repeatable enough to debug.

Use:

- screw terminals for RS485, RS232, CAN, 24 V, DI/DO, analog;
- JST-XH for internal low-voltage module harnesses;
- JST-PH for small sensors;
- USB-A/C for commodity adapters;
- RJ45 for Ethernet and optional serial breakouts;
- Wago lever connectors for bench-only temporary power distribution.

Avoid:

- raw Dupont wires on field side;
- loose breadboards in the enclosure;
- unlabeled JST pigtails;
- one-off soldered splices hidden under heat shrink without a label;
- connecting industrial signals directly to Pi GPIO.

Color convention:

```text
red      = +5 V or +24 V, label required
black    = DC 0 V / GND
green    = earth / shield, where applicable
blue     = RS485 A / CAN L, label required
white    = RS485 B / CAN H, label required
yellow   = signal / DI / pulse
orange   = relay / DO controlled output
```

Do not rely on color alone. Labels win.

## 7. Software Image

The image should be called Yaver Box Image, not Raspberry Image, even if the
first build targets Raspberry Pi hardware.

Base profiles:

- `yaver-box-rpi-cm4`;
- `yaver-box-rpi-cm5`;
- `yaver-box-x86`;
- later: `yaver-box-jetson`.

Installed baseline:

- `yaver-cli`;
- systemd service for `yaver serve`;
- ffmpeg;
- gstreamer;
- v4l2 utilities;
- rtl-sdr tools;
- LoRa tools and packet-forwarder dependencies when a LoRa module is present;
- GPS/GNSS tools such as gpsd or a small local parser;
- can-utils;
- serial tools;
- Modbus test tools;
- camera/depth utilities where hardware support is stable;
- LiDAR driver or bridge process only for the selected prototype sensor;
- udev rules;
- hardware inventory service;
- optional Docker or rootless container runtime;
- optional Node-RED for advanced users, not the primary Yaver UX.

Systemd units:

```text
yaver.service              main agent
yaver-inventory.service    one-shot hardware manifest
yaver-inventory.timer      periodic refresh
yaver-firstboot.service    first-boot identity and provision setup
yaver-watchdog.service     optional recovery watchdog
```

The image must boot into an unclaimed but reachable state. SSH should not be the
normal setup path.

## 8. Yaver Capability Manifest

`/info` should grow an industrial section. The code is source of truth; this is
the target shape.

```json
{
  "deviceClass": "industrial-edge",
  "profile": "yaver-iot-prototype-v0",
  "hardware": {
    "base": "rpi-cm5-industrial",
    "image": "yaver-box-2026.06",
    "serial": "box-local-id"
  },
  "interfaces": {
    "serial": [
      {
        "id": "rs485-main",
        "path": "/dev/serial/by-id/usb-FTDI_RS485_01",
        "kind": "rs485",
        "isolated": true,
        "label": "RS485 A"
      }
    ],
    "can": [
      {"id": "can0", "path": "can0", "kind": "socketcan"}
    ],
    "video": [
      {"id": "cam0", "path": "/dev/video0", "kind": "camera"},
      {"id": "capture0", "path": "/dev/video2", "kind": "hdmi_capture"},
      {"id": "depth0", "path": "usb-depth-camera", "kind": "depth_camera"}
    ],
    "lidar": [
      {"id": "lidar0", "path": "/dev/serial/by-id/usb-LiDAR", "kind": "2d_lidar"}
    ],
    "sdr": [
      {"id": "rtl0", "kind": "rtl_sdr"}
    ],
    "lora": [
      {"id": "lora0", "kind": "sx126x_or_sx127x", "path": "usb-or-spi-bridge"}
    ],
    "gps": [
      {"id": "gps0", "kind": "gnss", "path": "/dev/serial/by-id/usb-GNSS"}
    ],
    "network": [
      {"id": "eth0", "kind": "ethernet"},
      {"id": "wlan0", "kind": "wifi"}
    ]
  },
  "capabilities": [
    "serial.list",
    "modbus.rtu.read",
    "modbus.rtu.scan",
    "modbus.tcp.read",
    "machine.sniff",
    "machine.read",
    "camera.snapshot",
    "camera.motion",
    "capture.snapshot",
    "capture.stream",
    "can.read",
    "sdr.capture",
    "lora.scan",
    "lora.receive",
    "lora.send",
    "depth.frame",
    "lidar.scan",
    "yaver.mesh",
    "box.mesh.lan",
    "box.mesh.relay",
    "box.mesh.offgrid_text",
    "gps.location",
    "gps.track",
    "energy.3phase"
  ]
}
```

The mobile app should render this as a box capability screen:

- ports;
- drivers;
- current permission state;
- last self-test;
- suggested first actions.

## 9. Ops Surface

Use existing `/ops`. Do not create a separate IoT server.

Existing relevant verbs:

- `machine_ports`;
- `machine_scan_registers`;
- `machine_read`;
- `machine_write`;
- `machine_sniff_start`;
- `machine_sniff_status`;
- `machine_sniff_stop`;
- `machine_connect`;
- `machine_list`;
- `machine_state`;
- `machine_read_tags`;
- `machine_write_tags`;
- `gcode_open`;
- `gcode_send`;
- `gcode_stream`;
- `gcode_estop`;
- `robot_*`;
- `camera_motion`;
- `stream_*`.

New verbs to add first:

```text
iot_inventory
iot_selftest
iot_label_port
iot_notes_get
iot_notes_set
modbus_discover
energy_meter_discover
camera_probe
capture_probe
can_probe
sdr_probe
lora_probe
lora_receive
lora_send
gps_probe
gps_location
gps_track_start
gps_track_stop
depth_probe
lidar_probe
perception_snapshot
closed_loop_run
dio_status
dio_set
```

The first versions can wrap existing implementation where possible. For example
`modbus_discover` can call the same engine paths behind `machine_ports`,
`machine_scan_registers`, and Kalkan meter-scan logic.

## 10. Safety Gates

Every capability is one of five risk levels.

```text
observe       cannot affect hardware: inventory, camera snapshot, video frame
passive_bus   reads existing traffic without transmitting
active_read   transmits read requests: Modbus read, CAN request if applicable
bounded_write writes a value with min/max, read-back, and owner confirmation
motion        robot/CNC/actuator movement; requires fixture limits and approval
```

Default permissions:

```text
observe       allowed for owner
passive_bus   allowed after selecting interface
active_read   explicit per-bus start
bounded_write confirm=true + safe range + read-back
motion        confirm=true + soft limits + visible status + stop path
```

Never allow:

- blind broad LAN scanning;
- writing without selected target;
- writing machine safety registers;
- motion with no soft-limit envelope;
- unattended first-time writes;
- remote guest writes by default.

RF-specific rules:

- SDR receive is observe-only, but captures may still contain sensitive local
  signals, so store captures locally by default.
- LoRa transmit must be region-aware. The box must know its configured radio
  region before enabling send.
- LoRaWAN gateway mode and raw LoRa lab mode are separate profiles.
- Do not ship default workflows that jam, replay, impersonate, or bypass access
  controls.

Perception-specific rules:

- Cameras, depth cameras, and LiDAR are observation and verification inputs by
  default.
- Perception may approve "state looks correct" only after a deterministic action
  gate has already approved the command.
- Do not let a VLM or camera-only classifier directly command motion without
  the normal motion gate.
- Store calibration data locally unless the user explicitly syncs it to a
  project package.

## 10.1 Closed-Loop Remote Vibe Development

The target workflow is remote vibing against real hardware:

```text
developer / AI agent remote
-> edits Talos/OCPP/robotics code
-> deploys or reloads on Yaver Box
-> box acts on selected machine/robot/charger/test fixture
-> sensors observe result
-> agent sees telemetry, frames, LiDAR/depth, serial logs, registers
-> agent fixes code and repeats
```

This is the product loop. The box is the physical feedback surface for code.

Closed-loop examples:

- Talos robotics: update robot routine, run bounded motion, inspect camera/depth
  result, adjust calibration.
- OCPP/Kalkan: change load-balancing algorithm, replay charger/meter simulator
  or live bench meter, inspect per-phase current and charger state.
- Simkab machinery: connect RS485/camera to a machine, read state/counters,
  improve Talos machine model and UI remotely.
- Hardware product: power-cycle device, capture serial boot log, capture HDMI,
  read current draw, fix firmware/test code.

`closed_loop_run` should be a higher-level orchestration verb later, not a new
transport. It should compose existing ops:

```text
prepare -> act -> observe -> verify -> artifact
```

Every run should produce a local artifact:

- code version or package version;
- command sent;
- approval record if any;
- sensor frames or hashes;
- Modbus/register samples;
- serial logs;
- pass/fail verdict;
- operator notes.

## 10.2 Yaver Box Mesh Network

Multiple Yaver Box boxes should form a local and remote mesh. This is a core
feature, not an add-on.

Use three layers, with clear responsibilities:

```text
Layer A - LAN/device discovery
  UDP beacon, mDNS-style discovery, direct HTTP, local Wi-Fi/Ethernet.

Layer B - Yaver IP/ops mesh
  Existing Yaver deviceId routing, relay /d/<deviceId>/..., optional WireGuard
  overlay where enabled, ACLs, peer tags, remote /ops.

Layer C - low-bandwidth off-grid message mesh
  LoRa/Meshtastic-style text/SOS/telemetry. No video, no internet, no robot
  motion control over this layer.
```

Do not build a separate industrial network stack. A Yaver Box is already a
Yaver device. The mesh should use normal Yaver identity, device registry,
`deviceId`, relay, ACL, and `/ops` routing wherever IP exists.

### 10.2.1 Mesh Goals

The box mesh must support:

- many boxes in one lab or factory;
- one mobile app selecting any box by name/deviceId;
- one box acting as a gateway for boxes behind it;
- relay fallback when LAN fails;
- remote vibing over 4G/home internet;
- local operation if internet is down;
- optional off-grid LoRa text/telemetry;
- fleet-wide hardware inventory;
- routing robot/machine/camera/capture actions to the correct box;
- box-to-box event sharing for closed-loop projects.

Example:

```text
Yaver Mobile / remote engineer
  -> gateway box in Simkab office
    -> robot box beside robot arm
    -> meter box in electrical cabinet
    -> camera box watching machine HMI
    -> charger box in garage
```

Every target remains addressed by `deviceId`, not by a hardcoded IP.

### 10.2.2 Mesh Roles

Each box can advertise roles:

```text
edge_node       attached to one machine/robot/charger/sensor cluster
gateway_node    has good internet/LAN and can route to other boxes
sensor_node     mostly observes: camera, meter, LiDAR, SDR, LoRa
robot_node      has motion/control capabilities
energy_node     has meters/CT clamps/charger links
rf_node         has SDR/LoRa/cellular/radio modules
lab_node        general-purpose bench prototyping box
```

Roles are not SKUs. They are detected/configured capabilities.

### 10.2.3 Network Modes

Daily online mode:

```text
boxes on Ethernet/Wi-Fi
-> Yaver LAN discovery
-> direct /ops when reachable
-> relay fallback by deviceId
-> mobile/web/agent can target any box
```

Factory messy LAN mode:

```text
one gateway box has internet
other boxes are on isolated Ethernet/Wi-Fi segments
-> gateway proxies selected peers
-> no broad LAN scanning by default
-> explicit routes only
```

Offline lab mode:

```text
no internet
-> local Wi-Fi/Ethernet still works
-> local mobile/web controls boxes
-> logs/artifacts stay local
-> sync later when internet returns
```

Off-grid text mode:

```text
LoRa/Meshtastic-style mesh
-> text, SOS, GPS, tiny telemetry
-> no video
-> no Modbus register scans
-> no robot control
-> no "internet over LoRa" claims
```

### 10.2.4 Box Mesh Manifest

Add a mesh section to `/info`:

```json
{
  "mesh": {
    "enabled": true,
    "roles": ["gateway_node", "robot_node"],
    "links": [
      {"kind": "ethernet", "iface": "eth0", "state": "up"},
      {"kind": "wifi", "iface": "wlan0", "state": "up"},
      {"kind": "relay", "state": "ready"},
      {"kind": "lora", "state": "present", "profile": "lora_lab"}
    ],
    "peers": [
      {"deviceId": "box-robot-1", "alias": "robot cell", "route": "lan"},
      {"deviceId": "box-meter-1", "alias": "meter cabinet", "route": "relay"}
    ]
  }
}
```

### 10.2.5 Mesh Ops To Add

```text
box_mesh_status
box_mesh_peers
box_mesh_ping
box_mesh_route_test
box_mesh_tag_set
box_mesh_role_set
box_mesh_acl_get
box_mesh_acl_set
box_mesh_event_publish
box_mesh_event_tail
box_mesh_offgrid_send
box_mesh_offgrid_inbox
```

Most of these should be thin wrappers around existing Yaver mesh/device/relay
state. Avoid duplicating backend state.

### 10.2.6 Box-to-Box Events

Closed-loop robotics and machines need local events, not only request/response.

Examples:

```text
robot box       -> "motion complete", "estop pressed", "camera changed"
meter box       -> "phase current high", "voltage sag"
machine box     -> "cycle count changed", "fault bit set"
rf box          -> "LoRa packet received", "SDR threshold event"
gateway box     -> "internet lost", "relay restored"
```

Use a local event bus with bounded retention:

- publish locally;
- relay to selected peers if allowed;
- store recent tail on the emitting box;
- never sync raw sensitive captures by default;
- let Talos/OCPP packages subscribe to structured events.

### 10.2.7 Mesh ACLs

Mesh visibility is not control permission.

Suggested defaults:

```text
same owner can see inventory/status
same owner can call observe ops
active reads require per-box enable
writes require target-box approval policy
motion requires target-box approval policy and local stop path
guest access is observe-only unless explicitly granted
off-grid LoRa roster is separate from Yaver cloud identity
```

The mobile UI should show:

- who can see this box;
- who can call observe ops;
- who can perform active reads;
- who can request writes/motion;
- last remote action per peer.

### 10.2.8 Mesh For Remote Vibing

The mesh makes remote closed-loop development practical:

```text
agent edits code on laptop/cloud
-> deploys to gateway box or target box
-> target box runs hardware action
-> sensor boxes publish observations
-> gateway aggregates artifact
-> agent sees result and fixes code
```

Example: Talos robot cell with separate boxes:

```text
box A: robot controller + E-stop input
box B: camera/depth/LiDAR view
box C: meter/current sensing
gateway: relay + artifact aggregation
```

The AI should be able to ask:

```text
"run the bounded test on robot-box, watch camera-box, and compare current on meter-box"
```

That becomes a graph of `/ops` calls addressed by deviceId.

### 10.2.9 What LoRa Mesh Is For

LoRa is useful, but only for the right payloads:

- off-grid text;
- GPS/SOS;
- tiny telemetry;
- "box is alive";
- "machine fault occurred";
- "internet is down";
- small command intent that still requires local policy before execution.

LoRa is not for:

- video;
- depth/LiDAR frames;
- SDR samples;
- firmware updates;
- broad Modbus scans;
- robot live control;
- generic internet.

For robotics and machinery, LoRa can be a last-resort status/alert channel, not
the control plane.

## 10.3 MCP Orchestration For Box Utilization

Yaver MCP should make boxes usable from coding agents without requiring the
agent to know low-level ports first.

The MCP layer should expose the fleet as typed resources:

```text
boxes
interfaces
machines
meters
robots
captures
rf_modules
perception_sources
data_pull_plans
closed_loop_runs
```

The coding agent should be able to ask for intent-level actions:

```text
"Find the box connected to the Sampo test rig and pull machine status."
"Run a read-only Modbus discovery on the Fortaco meter cabinet."
"Compare camera motion with current draw for the Simkab cut-strip machine."
"Start a closed-loop Talos robotics run using robot-box and camera-box."
"Collect 10 minutes of charger/meter telemetry from the OCPP bench."
```

MCP tools should compile these intents into explicit, auditable plans:

```text
select boxes -> verify permissions -> choose interfaces -> run safe probes
-> pull bounded data -> summarize -> store local artifact -> optionally sync
```

### 10.3.1 MCP Tools To Add

High-level tools:

```text
box_list
box_inventory
box_mesh_status
box_select_for_task
box_data_pull_plan
box_data_pull_run
box_closed_loop_run
box_artifacts_list
box_artifact_get
box_capability_explain
```

Industrial tools:

```text
industrial_site_list
industrial_site_map
industrial_machine_discover
industrial_machine_watch
industrial_meter_discover
industrial_meter_watch
industrial_diagnose
industrial_report
```

Robotics tools:

```text
robotics_cell_list
robotics_cell_snapshot
robotics_closed_loop_run
robotics_calibration_run
robotics_fixture_test
```

OCPP/Kalkan tools:

```text
ev_site_list
ev_meter_pull
ev_charger_discover
ev_load_test_run
ev_dlm_replay
```

These should be wrappers over `/ops` and existing machine/robot/stream verbs,
not a parallel execution engine.

### 10.3.2 Data Pull Plans

Industrial data pulling must be deliberate. A plan is a structured object, not
an ad-hoc agent loop.

```json
{
  "planId": "pull-sampo-line-1-20260618",
  "site": "sampo",
  "target": "machine-or-meter-id",
  "boxes": ["box-rs485-1", "box-camera-1"],
  "risk": "active_read",
  "durationSec": 600,
  "sources": [
    {
      "kind": "modbus_rtu",
      "box": "box-rs485-1",
      "port": "rs485-main",
      "unit": 1,
      "reads": [
        {"start": 0, "count": 20, "fc": 3, "intervalMs": 1000}
      ]
    },
    {
      "kind": "camera_motion",
      "box": "box-camera-1",
      "source": "cam0",
      "intervalMs": 1000
    }
  ],
  "storage": {
    "localOnly": true,
    "syncSummary": true,
    "syncRaw": false
  },
  "approval": {
    "required": true,
    "approvedBy": null
  }
}
```

Rules:

- default to local-only raw data;
- sync summaries and structured metrics first;
- raw frames/register dumps require explicit opt-in;
- each source has bounded duration, interval, and target;
- no broad scanning inside customer networks by default;
- writes and motion are separate plan types with stronger approval.

### 10.3.3 MCP Result Shape

MCP results should be machine-readable enough for agents:

```json
{
  "ok": true,
  "artifactId": "artifact-...",
  "summary": "Machine was idle for 62% of the window; current spikes align with camera motion.",
  "signals": [
    {"name": "phase_l1_current", "unit": "A", "min": 2.1, "max": 19.4},
    {"name": "camera_motion_score", "unit": "score", "min": 0.01, "max": 0.42}
  ],
  "findings": [
    {"severity": "warn", "text": "Motion observed while PLC running bit stayed 0."}
  ],
  "nextActions": [
    {"tool": "industrial_diagnose", "reason": "Correlate PLC bits with current draw"}
  ]
}
```

Agents should not scrape prose. They should receive structured metrics,
findings, artifact IDs, and next-action candidates.

## 10.4 Backend Model

The backend should store identity, capability summaries, site topology, and
audit metadata. It should not store customer raw data by default.

Suggested entities:

```text
iotBoxes
  deviceId, ownerId/teamId, alias, deviceClass, imageVersion, online,
  roles[], tags[], lastSeen, siteId?, locationLabel?

iotBoxCapabilities
  deviceId, capability, present, label, riskLevel, lastSelfTest, metadataSummary

iotSites
  siteId, name, customerAlias, ownerId/teamId, notes, policy

iotAssets
  assetId, siteId, kind(machine|robot|meter|charger|fixture|camera|rf),
  alias, boundBoxes[], tags[], statusSummary

iotDataPullPlans
  planId, siteId, createdBy, risk, sourcesSummary, approvalState,
  localArtifactRef, syncRawAllowed, createdAt, lastRunAt

iotRunSummaries
  runId, planId, startedAt, endedAt, status, summary, findings,
  localArtifactRef, syncedMetricCount

iotAuditEvents
  who, box, action, risk, approval, result, timestamp
```

Convex/privacy constraint:

- do not store raw frames by default;
- do not store register dumps by default;
- do not store customer LAN IPs unless explicitly needed and allowed;
- do not store secrets;
- store site aliases, device IDs, summaries, and artifact references.

Raw artifacts live on the box:

```text
~/.yaver/iot/artifacts/<runId>/
  plan.json
  summary.json
  samples.jsonl
  frames/
  logs/
  notes.md
```

The mobile/web UI can request raw artifacts from the box over direct/relay
transport when the owner opens the run.

## 10.5 UI Surfaces

### Mobile

Mobile should be the field/operator surface:

- claim box;
- name box;
- assign to site;
- see online/offline;
- run self-test;
- see ports and sensors;
- approve bounded read/write/motion;
- start/stop data pull;
- view live camera/capture;
- see active safety/permission gates;
- receive alerts.

Mobile screens:

```text
Boxes
Box Detail
Site Map
Machine Detail
Robot Cell
Meter Detail
Data Pull Plan
Run Artifact
Approvals
Mesh
```

### Web

Web should be the engineering cockpit:

- fleet table;
- site topology graph;
- machine register map;
- meter/camera correlation charts;
- Modbus sniff/session viewer;
- LiDAR/depth/camera calibration panels;
- robot closed-loop run viewer;
- artifact browser;
- MCP plan preview;
- policy/audit screen.

For industrial customers, web is where the value is visible:

```text
site -> boxes -> assets -> live signals -> findings -> reports
```

### MCP / Agent UI

When an MCP client is using boxes, the UI should show a live "agent using box"
banner:

```text
Codex is reading meter-box-1
Risk: active_read
Plan: pull-fortaco-meter-10min
Raw sync: off
[Stop] [View Plan] [View Live Data]
```

No invisible agent activity against physical systems.

## 10.6 Industrial Customer Patterns

The same generic tool can fit several customer/project shapes.

### Sampo / Fortaco Style Supplier Work

Likely value:

- remote factory diagnosis;
- meter and machine utilization measurement;
- camera-assisted process verification;
- cycle-time measurement;
- harness/fixture QA evidence;
- comparing expected Talos schedule vs observed machine state.

Example plan:

```text
watch crimp press for 30 min
-> camera motion
-> current draw
-> PLC running bit if available
-> operator notes
-> report downtime windows and inconsistent signals
```

### Simkab Machinery

Likely value:

- wrap existing cut/strip/crimp/test machines;
- read Modbus or serial service ports;
- camera-HMI fallback;
- feed Talos machine capacity;
- remote diagnose jams, idle time, wrong program, low utilization;
- close the loop from Talos recipe/order to machine state.

### OCPP / Electricity / EV Chargers

Likely value:

- meter discovery;
- per-phase current and voltage logging;
- charger discovery;
- load management replay;
- remote building diagnosis;
- local-first operation with Yaver remote support.

### Robotics / Fixtures

Likely value:

- remote code/deploy/test loop;
- camera/depth/LiDAR feedback;
- fixture power/current measurement;
- bounded robot motion;
- artifacted QA runs;
- AI-assisted calibration.

## 10.7 Normie Programming: Vibe Automations

Yaver Box should be programmable by normal users through Yaver, without asking
them to learn Linux, cron, Modbus, MQTT, or Node-RED.

The interaction model:

```text
user: "Every morning at 8, tell me if the yacht battery is low."
Yaver: drafts automation -> shows what it reads, when it runs, what it may do
user: approves
Yaver Box: runs it locally on schedule, stores history, sends alerts
```

This is the merge point between the current Yaver product and Yaver Box:

- Yaver already has agents/runners/MCP;
- Yaver already has a scheduler that can fire ops verbs on a target `machine`;
- Yaver already has Task Packages for portable scheduled work;
- Yaver Box adds physical-world capabilities as MCP/ops tools;
- the user programs the box by vibing, not by wiring dashboards manually.

### 10.7.1 Automation Shapes

There should be three user-facing automation levels.

Simple rule:

```text
When temperature > 40 C, notify me.
When GPS leaves marina geofence, alert me.
When garage charger current exceeds 25 A, reduce charger limit if allowed.
```

Routine / cron:

```text
Every day at 08:00, send me boat/car/machine status.
Every 10 minutes, log phase current and charger state.
Every Friday, create a machine utilization report.
```

Agentic task:

```text
Watch this machine for a shift and diagnose why it idles.
Keep improving the robot calibration until the camera says the part is aligned.
Monitor my yacht bilge/camera/GPS/battery and explain abnormal changes.
```

Simple rules should compile to deterministic schedules or event subscriptions.
Agentic tasks should compile to Task Packages or guarded closed-loop runs.

### 10.7.2 Draft, Review, Install

Natural language must never become invisible hardware control. The install flow:

```text
1. user describes goal
2. Yaver drafts automation
3. Yaver shows:
   - boxes used
   - sensors read
   - actions allowed
   - schedule
   - data stored
   - alert destinations
   - risk level
4. user approves
5. automation is installed on the box
6. user can pause/edit/delete from mobile/web/MCP
```

Example compiled automation:

```json
{
  "name": "Yacht morning status",
  "schedule": {"cron": "0 8 * * *"},
  "targetBox": "yacht-box",
  "risk": "observe",
  "reads": [
    {"capability": "gps.location"},
    {"capability": "energy.dc_battery"},
    {"capability": "camera.snapshot", "source": "bilge-cam"}
  ],
  "actions": [
    {"kind": "notify", "channel": "mobile_push"}
  ],
  "stores": {"summary": true, "rawFrames": false}
}
```

For machinery:

```json
{
  "name": "Simkab cut machine idle report",
  "schedule": {"every": "5m"},
  "targetBox": "simkab-line-box",
  "risk": "active_read",
  "reads": [
    {"capability": "machine.read_tags", "machine": "cut-strip-01"},
    {"capability": "camera.motion", "source": "hmi-cam"},
    {"capability": "energy.3phase"}
  ],
  "actions": [
    {"kind": "local_artifact"},
    {"kind": "notify_on_finding", "severity": "warn"}
  ],
  "stores": {"summary": true, "raw": "local_only"}
}
```

### 10.7.3 Existing Runtime Fit

Use the existing scheduler for direct routines:

```text
ScheduledTask.Verb      -> ops verb
ScheduledTask.Machine   -> target deviceId / box
ScheduledTask.OpsPayload -> action payload
Cron / RepeatInterval   -> timing
History                 -> run result
```

Use Task Packages for bigger automations:

```text
kind: monitor | operate | agent
engines: mcp | runner | fetch | playwright | redroid
schedule: every/cron
target: local box, remote box, phone, docker
guard: read-only vs acting
```

Use MCP as the authoring and operations surface:

```text
box_automation_compose
box_automation_install
box_automation_list
box_automation_pause
box_automation_resume
box_automation_delete
box_automation_run_now
box_automation_history
box_automation_explain
```

These are mostly wrappers over scheduler + package ops + `/ops`, but they make
the normal-user mental model clean.

### 10.7.4 Normal-Person Use Cases

Yacht:

- GPS/geofence status;
- bilge camera or water sensor;
- battery/solar/shore-power monitoring;
- temperature/humidity;
- engine/service serial if available;
- LoRa/cellular fallback alerts;
- "send me a morning boat report."

Car / camper / van:

- GPS location;
- battery voltage;
- dash/cabin camera;
- OBD/CAN where legally and safely available;
- tire/temperature sensors if bridged;
- "alert me if battery drops or car moves."

Home / workshop:

- power meter;
- camera/capture;
- door/relay/IR/home devices;
- SDR/LoRa sensors;
- "turn on ventilation if air quality is bad";
- "record a clip if motion happens."

Machine / shop floor:

- utilization report;
- idle/fault diagnosis;
- energy usage;
- camera/HMI observation;
- "tell me why this machine stopped."

The key is the same UX:

```text
ask -> draft -> approve -> box runs -> Yaver reports
```

## 10.8 Universal Interface Target

The long-term Yaver Box target is "plug into almost anything" for monitoring,
diagnosis, and approved control. Prototype 0 will not physically include every
connector cleanly, but the facade should be designed as if it can.

Interface families:

```text
USB host            cameras, capture cards, SDR, LoRa, serial adapters, GPS
USB-C PD            power + phone/laptop integration
RS485               Modbus RTU, meters, chargers, industrial sensors
RS232               service ports, machinery, old equipment
TTL UART            dev boards, fixtures, embedded products
CAN / CAN-FD        vehicles, BMS, industrial controllers, elevators where allowed
Ethernet            Modbus TCP, OCPP, IP cameras, PLC gateways, local APIs
Wi-Fi               local APIs, SoftAP setup, internet uplink
BLE                 sensors, beacons, phones, low-power setup
LoRa                off-grid text/tiny telemetry
SDR RX              receive-only RF inspection
HDMI capture        device displays, HMIs, set-top boxes, embedded UIs
CSI/USB camera      vision, OCR, machine state, appliance panels
Depth / LiDAR       robotics, presence, mapping, calibration
IR TX/RX            AC/TV/legacy remotes
GPIO via modules    low-voltage lab signals, never raw industrial direct
Digital inputs      24 V industrial status, dry contacts, e-stop status observe
Digital outputs     24 V relay/DO modules for non-safety control
Analog inputs       0-10 V, 4-20 mA sensors
Analog outputs      0-10 V control only where equipment supports it
Pulse/counter       flow meters, energy pulses, cycle counters
1-Wire/I2C/SPI      internal sensors through safe adapter modules
GPS/GNSS            location, geofence, moving assets, time
Audio               microphone/speaker, abnormal sound, voice notes
```

Rule: "all interfaces" does not mean all on one PCB. It means the Yaver Box
facade can normalize all of them when the right module is plugged in.

## 10.9 Home / Appliance Troubleshooting

Yaver Box should be a normal-person troubleshooting box for home devices:
washing machines, fridges, freezers, kombi/boilers, heat pumps, EV chargers,
electrical panels, pumps, garage doors, gates, AC units, elevators where
authorized, and "whatever is making a weird sound."

The goal is not to replace a repair technician. The goal is to make the first
diagnosis cheap and remote:

```text
observe -> detect pattern -> explain likely causes -> suggest safe checks
-> optionally call a technician with a useful report
```

### 10.9.1 Detection Methods

Yaver Box can combine weak signals into useful diagnosis:

- electrical signature from CT clamps, smart plugs, Modbus meters, or 3-phase
  meters;
- temperature probes, humidity, water leak, certified gas/smoke sensors;
- vibration sensors on motors, pumps, compressors, washing machines;
- microphone for abnormal sound capture;
- camera/OCR for error displays, LEDs, gauges, water drips, HMI screens;
- thermal camera later if a safe commodity module is available;
- protocol adapters: Modbus, OCPP, OpenTherm, MQTT, Home Assistant, Matter,
  vendor LAN APIs, IR, BLE, CAN/OBD where appropriate;
- AI correlation across timelines.

AI should produce hints and evidence, not overclaim certainty.

### 10.9.2 Washing Machine

Possible sensors:

- smart plug / current meter;
- vibration sensor;
- leak sensor;
- camera pointed at panel/error LED;
- microphone if the user wants noise diagnosis.

Useful diagnoses:

- cycle stuck;
- unbalanced spin;
- pump not draining;
- heater not drawing power;
- door/lock fault pattern;
- water leak event;
- abnormal vibration trend.

Safe actions:

- notify;
- record a clip;
- power-cycle only through a properly rated smart plug and only when safe;
- suggest checking filter/door/water valve.

Avoid:

- bypassing door locks;
- running unattended risky cycles;
- controlling mains wiring directly.

### 10.9.3 Fridge / Freezer

Possible sensors:

- temperature probe inside;
- door contact sensor;
- smart plug / current monitor;
- camera if needed;
- humidity sensor.

Useful diagnoses:

- compressor short cycling;
- door left open;
- temperature rising;
- defrost pattern abnormal;
- power outage;
- excessive energy use;
- food-safety alert.

Safe actions:

- notify;
- trend report;
- suggest cleaning condenser / checking door seal;
- emergency alert if freezer temperature rises.

### 10.9.4 Kombi / Boiler / Heat Pump

Kombi is a high-safety appliance because it combines gas, water, electricity,
pressure, exhaust/flue, and combustion. Yaver Box should be observe-first.

Possible safe surfaces:

- room temperature;
- pipe flow/return temperature clamps;
- electricity consumption;
- camera/OCR of boiler display error code;
- pressure gauge camera read;
- certified leak/gas detector integration;
- OpenTherm gateway where the appliance and installation support it;
- manufacturer API or Home Assistant integration if already configured.

Useful diagnoses:

- error-code report;
- heating demand vs boiler response;
- flow/return temperature delta;
- short cycling;
- pressure dropping over days;
- no ignition / lockout pattern from display + temperature + power;
- hot water usage pattern;
- efficiency hints for heat pump/boiler monitoring.

Control policy:

- default read-only;
- temperature setpoint changes only through supported thermostat/OpenTherm/API
  and user approval;
- never bypass flame, pressure, flue, gas, or overheat protections;
- never drive gas valves, pumps, or safety circuits directly;
- technician-required banner for combustion, gas smell, flue, repeated lockout,
  pressure relief, or electrical work.

Useful prior art: OpenEnergyMonitor documents heat-pump monitoring with
temperature, electricity, pulse, and Modbus metering. OpenTherm is a boiler /
thermostat communication protocol, but it needs special adapter hardware.

### 10.9.5 Home Electrical Network

Possible sensors:

- whole-home CT clamps;
- per-circuit CT clamps;
- smart plugs;
- 3-phase energy meter;
- voltage/frequency readings;
- panel temperature sensor where safely installed.

Useful diagnoses:

- high bill explanation;
- appliance detection by power signature;
- phase imbalance;
- EV charging impact;
- standby/phantom load;
- compressor/heater duty cycle;
- "this device is consuming power but not producing expected effect."

Safety:

- panel work is electrician territory;
- Yaver Box should integrate with certified meters/sensors;
- no loose CT/mains wiring in customer deployments;
- raw mains switching requires certified hardware and explicit user approval.

### 10.9.6 EV Chargers

Yaver Box can bridge normal and industrial EV use cases.

Possible surfaces:

- OCPP charger connection;
- charger vendor LAN API if available;
- Modbus meter;
- CT clamps;
- contactor feedback;
- thermal sensor;
- camera of charger display;
- RFID/session events where available.

Useful diagnoses:

- charger online/offline;
- car connected but not charging;
- suspended by EV vs suspended by EVSE;
- building overload risk;
- phase imbalance;
- charging session cost;
- charger draws current but OCPP state disagrees;
- "why did charging stop?"

Control policy:

- read-only by default;
- current limit changes are bounded writes;
- start/stop charging requires approval unless a user-installed automation
  explicitly allows it;
- electrical safety always stays with charger hardware and installation.

### 10.9.7 Elevators

Elevators are a special case. Yaver Box can be valuable for monitoring and
diagnosis, but control is heavily safety- and regulation-bound.

Allowed/realistic surfaces when authorized:

- camera/OCR of controller/display/status panel;
- dry-contact status outputs explicitly provided for monitoring;
- Modbus/BACnet/CAN/serial gateway if the elevator vendor exposes one;
- vibration sensor on motor/door machinery for trend monitoring;
- current meter for motor/drive energy profile;
- temperature sensor in machine room;
- log capture from service port with technician approval.

Useful diagnoses:

- door cycle abnormal;
- motor current/vibration trend;
- frequent fault code;
- cabin stuck/open-close loop observed from status signals;
- machine-room overheating;
- event timeline for technician.

Hard boundary:

- do not command elevator motion;
- do not bypass locks, doors, interlocks, emergency circuits, or inspections;
- do not attach to safety circuits;
- any integration must be owner/vendor/technician approved;
- Yaver reports and observes; certified elevator systems control.

### 10.9.8 24 V Motors, Relays, And Actuators

Yaver Box may need 24 V industrial parts to control motors or actuators in
fixtures, robotics, valves, fans, lights, and lab rigs. This should be module
based, not raw GPIO.

Use:

- certified 24 V DIN power supply;
- fused 24 V distribution;
- opto-isolated digital input module;
- relay or solid-state output module rated for the load;
- motor driver appropriate for DC/stepper/servo;
- contactor only where a qualified person designs the circuit;
- flyback protection for inductive loads;
- physical E-stop / kill switch for motion rigs;
- limit switches wired to the controller, not only to software.

Control tiers:

```text
status input       read 24 V input / dry contact
low-risk output    light, buzzer, fan, indicator
bounded actuator   valve, small DC motor, fixture clamp with limit switches
motion             robot/CNC/axis movement with soft limits + physical stop
high power         electrician/technician-designed only
```

Never:

- drive motors directly from Pi GPIO;
- switch mains with hobby relays in customer installs;
- control safety-critical motion only in software;
- use Yaver Box as the only E-stop path;
- run unattended first-time motion.

### 10.9.9 Normal-Person Troubleshooting UX

The UX should hide protocols until needed:

```text
What do you want to troubleshoot?

[Washing machine] [Fridge/freezer] [Kombi/heating] [EV charger]
[Electricity bill] [Pump/motor] [Elevator monitoring] [Something else]
```

Then:

```text
Yaver: "What can I see?"
  - power meter present
  - camera present
  - temperature probe present
  - GPS present
  - no vibration sensor

Yaver: "I can run a 30-minute observe-only diagnosis. It will read power,
temperature, and camera frames. It will not control the device."

[Start diagnosis]
```

Report shape:

```text
Summary
  The fridge temperature rose from 4 C to 9 C while the compressor drew no
  current. Door sensor was closed. Likely compressor/start relay/power issue.

Evidence
  temperature chart
  power chart
  camera frame / display OCR

Safe next checks
  check outlet, breaker, door seal, ventilation
  call technician if temperature keeps rising
```

### 10.9.10 Appliance MCP Tools

MCP should expose simple appliance-level tools:

```text
appliance_discover
appliance_diagnose
appliance_watch
appliance_report
appliance_create_automation
home_energy_diagnose
kombi_diagnose
fridge_watch
washer_diagnose
ev_charger_diagnose
elevator_monitor
motor_fixture_control
```

These compile to the same lower-level Yaver Box ops:

```text
meter reads + camera + temperature + vibration + protocol adapter + AI summary
```

The agent should not need to know whether the fridge was detected by a smart
plug, CT clamp, or vendor API. It should ask the facade for "fridge status" and
the box picks the best available signals.

## 10.10 Monetization With Yaver Box

Yaver Box can monetize the merged Yaver product better than pure software
because the box creates a durable place for Yaver to run.

### 10.8.1 Revenue Layers

Hardware:

- personal Yaver Box kit;
- industrial interface pack;
- robotics/perception pack;
- EV/metering pack;
- RF/off-grid pack;
- prebuilt premium box later.

Software subscription:

- remote access / relay;
- mobile/web dashboards;
- box automations;
- alerting;
- history retention;
- multi-box mesh;
- MCP agent orchestration;
- reports.

Usage-based:

- AI diagnosis runs;
- closed-loop robotics runs;
- video/capture relay bandwidth;
- artifact storage;
- managed remote support sessions;
- premium model calls.

Vertical packages:

- Talos machine monitoring package;
- OCPP/Kalkan building/charger package;
- robotics fixture package;
- yacht/car/home monitoring package;
- industrial diagnosis/report package.

Services:

- setup help;
- custom automation building;
- industrial integration;
- remote diagnosis session;
- support subscription.

### 10.8.2 Product Tiers

Personal:

```text
one box
basic automations
mobile access
limited history
pay for AI-heavy diagnosis
```

Pro / engineering:

```text
multiple boxes
MCP orchestration
closed-loop runs
artifact history
advanced sensors
remote dev workflows
```

Industrial:

```text
sites/assets
audit logs
approval policies
team access
reports
data retention controls
support links
SLA-style remote diagnostics
```

Vertical add-ons:

```text
Talos manufacturing
OCPP/Kalkan EV + metering
Robotics lab/cell
Yacht/car/home monitoring
RF/off-grid
```

### 10.8.3 Why Box Helps Yaver Monetize

Without hardware, Yaver is "a remote dev/agent tool." With Yaver Box, Yaver
becomes the thing that connects AI to the physical world:

```text
software agent value     -> code, MCP, runners, tasks
box value                -> sensors, hardware, remote reachability
combined value           -> "tell Yaver what you want the physical system to do"
```

This creates daily usage:

- status reports;
- alerts;
- remote checks;
- scheduled automations;
- sensor histories;
- diagnosis;
- closed-loop improvement;
- support calls.

Daily usage justifies subscription better than one-off hardware.

### 10.8.4 Marketplace Later

Once automations are package-shaped, a marketplace becomes natural:

```text
"Yacht morning report"
"Workshop compressor monitor"
"EV charger load report"
"Simkab machine idle diagnosis"
"Robot camera calibration loop"
"LoRa weather node collector"
```

Each package declares:

- required box capabilities;
- schedule;
- risk tier;
- data stored;
- actions it can take;
- price/free status.

Do not start with marketplace. Start with internal packages and customer-specific
packages. Marketplace only after permissions, signing, and package review are
boring.

## 11. Machine Use Cases

### 11.1 Brownfield PLC / Modbus Machine

Flow:

```text
connect RS485 A/B/GND
-> mobile selects RS485 adapter
-> modbus_discover probes selected baud/unit range
-> machine_scan_registers reads safe ranges
-> machine_understand labels likely registers
-> machine_connect creates a wrapped machine
-> machine_state and machine_read_tags feed Talos
```

Value:

- remote support without sending engineer;
- discover unknown register maps;
- turn a legacy machine into a watchable Yaver/Talos machine.

### 11.2 Passive Bus Sniff

Flow:

```text
tap RS485 read-only adapter
-> machine_sniff_start
-> observe existing master/slave frames
-> infer registers, units, counters, status fields
-> stop and produce candidate schematic
```

Use when active polling might disturb the system.

### 11.3 Camera-Only Machine

Flow:

```text
mount USB camera
-> camera_probe
-> define ROI
-> camera_motion
-> derive idle/running/fault candidate
-> optional VLM reads HMI text
```

This is the fastest retrofit when no PLC access is possible.

### 11.4 Electrical / Energy

Flow:

```text
install Modbus 3-phase meter + CT clamps
-> energy_meter_discover
-> read per-phase current/power
-> Talos/OCPP/Kalkan consumes energy.3phase
```

Important: CT installation around mains conductors is electrician territory in
real deployments. Prototype only under controlled bench conditions.

## 12. Robotics Use Cases

### 12.1 CNC / GRBL / Marlin Fixture

Flow:

```text
USB serial to controller
-> gcode_open
-> gcode_status
-> camera_probe
-> gcode_stream dryRun
-> operator confirm
-> stream bounded program
```

Hard requirements:

- soft limits configured;
- visible stop path;
- physical E-stop independent of Yaver.

### 12.2 Robot Arm Cell

Flow:

```text
robot controller over serial/TCP
camera on fixture
DI for E-stop status
DO for light/gripper if safe
-> robot_status
-> robot_snapshot
-> robot_jog / arm_* with bounds
-> teach capture
-> replay with operator approval
```

Yaver should treat robotics as view/watch/control with more severe gates than
normal machine registers.

### 12.3 Test Jig / Production Fixture

Flow:

```text
DUT serial console + camera + relay + meter
-> power cycle DUT
-> capture boot logs
-> capture HDMI or screen
-> read current draw
-> run test package
-> produce pass/fail artifact
```

This is likely one of the strongest commercial uses: a field-deployable remote
test bench for hardware products.

## 13. OCPP / Kalkan Use Cases

Yaver Box can host the physical edge for Kalkan/OCPP:

- Modbus RS485 energy meters;
- 3-phase current monitoring;
- EV charger discovery;
- SmartEVSE board scan;
- OCPP server or bridge;
- local dashboard;
- Yaver mobile remote support.

Existing OCPP/Kalkan code already has meter scan, charger scan, and EVSE RS485
scan concepts. The Yaver version should expose them as ops capabilities so the
same box can be used outside the Kalkan-specific UI.

## 13.1 Talos Robotics Use Cases

The box should be the Talos robotics physical edge.

Use it for:

- robot arm setup;
- fixture and jig development;
- camera/depth calibration;
- LiDAR-assisted workspace mapping;
- test cell observation;
- gripper/relay/lighting control;
- serial or TCP robot controller integration;
- remote teach/replay experiments;
- machine-vision verification after each motion.

Talos should not need to know whether the sensor is a USB camera, RealSense-like
depth camera, 2D LiDAR, or a phone camera. It should consume a Yaver perception
surface:

```text
perception_snapshot
  -> rgb frame
  -> optional depth frame
  -> optional lidar scan
  -> optional fiducials
  -> timestamp
  -> calibration profile
```

Robotics closed loop:

```text
Talos plan / AI edit
-> Yaver deploys new cell code
-> robot/gcode command with approval
-> camera/depth/LiDAR observes result
-> Talos updates calibration or program
```

## 13.2 Simkab Machinery Use Cases

The Simkab use case is brownfield machinery plus production engineering.

Likely surfaces:

- RS485 Modbus on cut/strip/crimp/test machinery;
- RS232 service ports;
- Ethernet Modbus TCP or vendor TCP APIs;
- HMI camera capture when there is no API;
- machine-current sensing with CT/meter;
- fixture camera for part/cable/terminal verification;
- digital input from machine ready/fault contacts;
- digital output only for non-safety request/ack lines.

Useful workflows:

- learn machine register maps;
- count cycles;
- detect running/idle/fault;
- compare machine capacity vs Talos schedule;
- remote debug why a machine is down;
- validate harness/fixture operations with camera and register data;
- generate a "machine operating manual" from observed tags.

## 13.3 OCPP / Kalkan Use Cases

The OCPP/Kalkan use case is EV charger and electricity-network prototyping.

Likely surfaces:

- Modbus 3-phase meters;
- CT clamps;
- RS485 SmartEVSE or compatible boards;
- Ethernet OCPP chargers;
- charger simulator;
- relay/contact feedback for lab fixtures;
- thermal sensors;
- cellular/Wi-Fi fallback for garages.

Useful workflows:

- dynamic load management algorithm development;
- charger discovery;
- apartment/current mapping;
- meter calibration;
- remote debugging at a building;
- local-first operation with optional Yaver remote view.

## 13.4 RF / LoRa / SDR Use Cases

RF is for field debugging and prototype sensors, not for bypassing networks.

Useful workflows:

- receive SDR samples for local signal inspection;
- inspect spectrum occupancy around a prototype;
- receive LoRa packets from our own test nodes;
- run a private LoRa gateway profile;
- debug sensor-node firmware remotely;
- compare RF events with machine/camera/electrical telemetry.

Keep profiles separate:

```text
sdr_observe        receive-only spectrum/sample capture
lora_lab           raw LoRa send/receive against our own nodes
lorawan_gateway    packet-forwarder mode with configured region/network
```

## 14. Mobile UX

First screen after claim:

```text
Yaver Box
Online

Interfaces
RS485 A       ready   /dev/serial/by-id/...
RS485 B       ready
Camera        ready
HDMI Capture  ready
CAN           missing driver
SDR           unplugged

Suggested
[Discover Modbus]
[Probe Camera]
[Start Capture Preview]
[Run Self Test]
```

Machine setup wizard:

```text
1. Pick interface
2. Pick mode: passive sniff / active read / manual
3. Pick protocol: Modbus RTU/TCP, G-code, serial console, camera-only
4. Run safe probe
5. Save as machine
6. Optional Talos/OCPP/robotics package binding
```

Write/motion confirmation:

```text
Write register 120 = 35
Machine: YH-8030H-01
Range: 0..100
Read-back required: yes
Risk: bounded_write

[Approve once] [Cancel]
```

## 15. Web UX

Web is for denser engineering views:

- live port table;
- frame/capture preview;
- Modbus register map;
- timeline of samples;
- sniff frame log;
- machine manual editor;
- robot/camera split view;
- package install/config pages.

Mobile is claim and quick operation. Web is bench/debug depth.

## 16. Build Record

Every prototype box should have a local and repo-side build record:

```text
box id
base computer
image version
module list
USB adapter serials
port labels
photos
self-test output
known issues
intended demo
```

Do not rely on memory. The point of no-PCB prototyping is fast iteration, but it
will become chaos unless every wire and adapter is named.

## 17. First Bench Prototype Checklist

Hardware:

- [ ] Linux base boots from eMMC/NVMe/known-good SD.
- [ ] Yaver image installed.
- [ ] Ethernet works.
- [ ] Wi-Fi works if included.
- [ ] QR claim label attached.
- [ ] RS485 adapter appears under `/dev/serial/by-id`.
- [ ] RS232 adapter appears under `/dev/serial/by-id`.
- [ ] Camera appears under `/dev/video*`.
- [ ] HDMI capture appears under `/dev/video*`.
- [ ] SDR appears in probe tool.
- [ ] LoRa module appears in probe tool if installed.
- [ ] Depth camera appears in probe tool if installed.
- [ ] LiDAR appears in probe tool if installed.
- [ ] All field connectors labeled.
- [ ] 24 V and 5 V rails fused.

Software:

- [ ] `yaver serve` starts on boot.
- [ ] `/health` responds.
- [ ] mobile claim works.
- [ ] `/info` shows `industrial-edge`.
- [ ] `/ops/verbs` includes machine and camera verbs.
- [ ] `machine_ports` returns stable serial paths.
- [ ] `camera_motion` works.
- [ ] capture snapshot works.
- [ ] SDR probe works if installed.
- [ ] LoRa receive works against our own test node if installed.
- [ ] depth/LiDAR probe works if installed.
- [ ] Modbus read works against simulator or known meter.
- [ ] relay path works outside LAN.

Demo:

- [ ] scan QR;
- [ ] see box in mobile;
- [ ] see hardware inventory;
- [ ] read a Modbus simulator/meter;
- [ ] show camera motion;
- [ ] show capture preview;
- [ ] show SDR/LoRa/depth/LiDAR probes where installed;
- [ ] wrap one machine or robot fixture;
- [ ] push telemetry to Talos or project UI.

## 18. First Demos To Build

### Demo A: Machine Watch

Bench setup:

- RS485 Modbus simulator or real meter;
- USB camera aimed at a moving object;
- Yaver Box.

Outcome:

- mobile discovers RS485 and camera;
- Modbus reads values;
- camera classifies motion;
- `machine_list` shows a machine state;
- Talos receives state.

### Demo B: Robot Fixture

Bench setup:

- GRBL/Marlin controller or small robot;
- USB camera;
- relay-controlled light;
- Yaver Box.

Outcome:

- open controller;
- query status;
- dry-run G-code;
- approve bounded move;
- camera verifies motion;
- stop path works.

### Demo C: OCPP/Kalkan Edge

Bench setup:

- Modbus energy meter or simulator;
- charger simulator or OCPP test charger;
- Yaver Box.

Outcome:

- meter scan works;
- charger scan works;
- per-phase data appears;
- remote Yaver view works.

### Demo D: RF / LoRa / SDR Bench

Bench setup:

- RTL-SDR;
- LoRa dongle/module;
- one known local LoRa test node or simulator;
- Yaver Box.

Outcome:

- inventory shows SDR and LoRa;
- SDR probe captures spectrum/sample metadata;
- LoRa receive shows packets from our test node;
- LoRa send is disabled until region/profile is configured;
- after explicit local lab profile, send a test packet to our node.

### Demo E: Talos Closed-Loop Robotics

Bench setup:

- small robot, CNC, or motion fixture;
- USB camera;
- optional depth camera or 2D LiDAR;
- relay/lighting;
- Yaver Box.

Outcome:

- Talos or Yaver agent edits a routine;
- box deploys/reloads it;
- bounded motion runs after approval;
- camera/depth/LiDAR snapshot verifies result;
- agent adjusts calibration or code and repeats.

### Demo F: Simkab Machine Wrapper

Bench setup:

- real or simulated Simkab machine interface;
- RS485/RS232/Modbus or camera-only fallback;
- optional meter/current sensing;
- Yaver Box.

Outcome:

- box reads machine state;
- camera/meter gives independent verification;
- Talos receives running/idle/fault/counter samples;
- remote AI improves machine model or UI from the observed loop.

## 19. When To Design A PCB

Do not design a PCB until at least these are true:

- same connector set repeated in 10+ prototypes;
- field wiring mistakes are consistent and a carrier would prevent them;
- USB adapter reliability is the bottleneck;
- enclosure space/cost is painful;
- customers need a cleaner install;
- isolation/protection requirements are well understood;
- software capability manifest has stabilized.

First PCB should likely be a carrier / harness board, not the whole computer:

- terminal blocks;
- fusing;
- TVS/protection;
- RS485 termination switches;
- isolated transceivers;
- labeled JST harnesses;
- power distribution;
- maybe a small MCU for watchdog/DI/DO.

Keep compute modular.

## 20. Near-Term Implementation Plan

1. Create Yaver Box Image profile for industrial-edge.
2. Add `industrial-edge` device class to agent/mobile/backend where needed.
3. Add hardware inventory service and `/info` industrial manifest.
4. Add `iot_inventory` and `iot_selftest` ops.
5. Wrap existing `machine_ports`, Modbus read/scan, and camera/capture paths into
   first-class mobile flows.
6. Build one physical no-PCB kit and a build record.
7. Run Demo A and Demo B.
8. Only then decide which module pack becomes the first sellable kit.

## 21. Design Mantra

The box should feel like a physical USB-C hub for the real world:

```text
plug in messy hardware
Yaver names it
Yaver makes it reachable
Yaver makes it safe enough to use remotely
Yaver lets projects consume it
```

That is the facade. The first prototype should prove that feeling before any
custom electronics work starts.
