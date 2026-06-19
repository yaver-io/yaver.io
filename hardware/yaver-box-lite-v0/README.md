# Yaver Box Lite V0 Mechanical Pack

Status: first printable mechanical concept for one reusable Yaver Box.

Purpose: one Raspberry Pi 4 box that can be moved between:

- OCPP/Kalkan PZEM-6L24 metering;
- JCWelec CST18D read-only machine listening;
- Simkab robotics: Ender/Marlin screwdriver, Fairino bridge, camera, tedge-style
  screw-and-sense experiments, including CAT Power screwdriver through BTS7960.

The design is intentionally a printable internal mounting system, not a sealed
certified enclosure. Put it inside a real enclosure or on an open DIN test plate
for V0.

V0 is intentionally PCB-free. Do not design a custom Pi HAT, terminal carrier,
motor board, or power board for this revision. Use off-the-shelf USB adapters,
DIN terminals, WAGO/lever connectors, labeled pigtails, and printed retainers.

Yaver and Talos are peer software stacks on this hardware. The box must work as:

- Yaver-alone: Yaver agent owns machine/OCPP/robot ops.
- Talos-alone: Talos `tedge` owns `cst18d`, `ocpp`, `ender`, or `screwdriver`.
- Interop: Yaver inventories/supports the box while `tedge` owns a specific
  active mode.

## Parts

| File | Part |
| --- | --- |
| `base_plate.scad` | Main internal mounting plate: Pi 4 standoffs, PSU zone placeholder, USB adapter clips, terminal panel mounts, cable tie slots. |
| `terminal_panel.scad` | Front terminal/label strip with POWER / RS485 / ROBOT / BENCH MOTOR zones. |
| `usb_adapter_clip.scad` | Generic clip for USB-RS485, USB hub, or slim USB adapter. |
| `lab_tray.scad` | Removable low-voltage tray for breadboard/MCP3008 experiments. |

OpenSCAD usage:

```text
openscad -D PART=\"plate\" -o base_plate.stl base_plate.scad
openscad -D PART=\"panel\" -o terminal_panel.stl terminal_panel.scad
openscad -D PART=\"clip\" -o usb_adapter_clip.stl usb_adapter_clip.scad
openscad -D PART=\"tray\" -o lab_tray.stl lab_tray.scad
```

## Mechanical Intent

Zones on the base plate:

```text
LEFT     24 V PSU / power distribution
CENTER   Raspberry Pi 4 + USB strain relief
RIGHT    USB-RS485 / USB hub / adapter clips
FRONT    terminal panel + labels
REAR     cable exit / strain relief
```

V0 prints should be PLA/PETG for bench. Use PETG/ABS/ASA and a real enclosure
for anything warm or cabinet-adjacent.

## Safety

- Do not expose Raspberry Pi GPIO on field terminals.
- Prefer a USB screwdriver companion for CAT Power/BTS7960; Pi GPIO is fallback
  only.
- Do not add custom PCB dependencies to V0.
- Do not mount mains wiring on printed plastic without a proper enclosure and
  covered terminals.
- Keep EDR-120-24 AC input covered and separated from low-voltage logic.
- Breadboard/MCP3008 tray is `LOW VOLTAGE ONLY`.
- CAT Power/BTS7960 output needs fuse, bulk capacitor, and physical kill switch
  before driving anything.

## Verify Before Printing Final

The EDR-120-24 and USB-RS485 adapter footprints are placeholders. Measure the
actual parts with calipers and adjust parameters at the top of each SCAD file.
