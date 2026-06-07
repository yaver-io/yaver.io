# Yaver Connector Box — Production Traveler (Rev A, dogfood unit)

This **finalizes** the connector box as the **first product made on the
Yaver×Talos manufacturing line** (see `docs/yaver-talos-manufacturing-on-demand.md`).
It is simultaneously the Rev-A build guide, the QA plan, and the literal
**traveler** a Yaver cell executes — every op maps to an `mfg_op_*` call and reports
to Talos. Making this part exercises the whole pipeline (print → PCB → flash →
test → assemble → QA → ship) end-to-end before any customer order.

## Frozen Rev-A definition

- **Variant:** `full` (both modes) — populate everything in `bom.csv` `full`.
- **Build method:** **modules, not a PCB spin** (de-risk first):
  - Isolated FTDI USB-RS485 module (DSD TECH SH-U11F) for the raw RS485 path.
  - Powered USB-C hub module (PD-source capable) for charge-while-host.
  - ESP32-S3 DevKit-C (u.FL variant) as the facade brain.
  - ADM2587E + ADM3251E breakout (or the FTDI module's isolation) for the bus.
  - 24→5 V buck module; INA219 module; screw-terminal breakouts.
  - Hand-wired on a protoboard inside the printed enclosure.
- **Enclosure:** `enclosure.scad` (`base`,`lid`) + `phone_mount.scad`
  (`clamp`,`ball`,`adapter`), PETG/ASA. STLs already render clean.
- **Firmware:** `firmware/` ESP32-S3 facade build (USB-CDC + SoftAP gateway +
  companion protocol).
- Rev-B (the `pcb.md` integrated board) follows once Rev-A proves the firmware +
  the charge-while-host on the real target phones.

## Traveler (op routing)

Each op: **machine · who (auto=Yaver / human) · output · QA · ~time · → Talos**.
Gates are human sign-offs (`mfg_op_gate`).

| Op | Step | Machine / station | Who | QA / verify | ~time |
|---|---|---|---|---|---|
| **OP10** | Print enclosure base+lid+mount | FDM printer (via connector box / `robot`) | auto | camera: first-layer + completion verdict | 3–4 h |
| **GATE** | De-rack + visual | bench | human | warp/stringing/peel check | 5 m |
| **OP20** | Procure PCB+SMT *(Rev B)* / gather modules *(Rev A)* | outsourced (JLC) / stock | human | incoming inspection vs `bom.csv` | — |
| **OP30** | Mount modules + THT (DC jack, USB-A, screw terminals, USB-C) | solder bench | human | continuity, no shorts | 30 m |
| **OP40** | Flash ESP32-S3 firmware | box USB (esptool) | auto | enumerate CDC + `Yaver-Box-XXXX` SoftAP appears | 3 m |
| **OP50** | **Self-test** (box tests itself, see below) | self-test fixture | auto | all checks PASS → logged to Talos | 5 m |
| **GATE** | Self-test review | bench | human | review the PASS report / triage fails | 2 m |
| **OP60** | Assemble into enclosure (standoffs, lid, light pipes, u.FL antenna), apply QR label (SoftAP creds + serial) | bench | human | lid seats, antenna torqued, label scans | 10 m |
| **OP70** | Functional QA (the box drives a real machine) | reference printer/PLC | auto+human | Yaver app connects (wired+wireless), drives a test move, `netcapture` shows clean frames; camera photo | 10 m |
| **GATE** | Final QA sign-off | bench | human | approve/reject | 2 m |
| **OP80** | Pack + ship + status | pack station | human + Talos | label, tracking → `mfg_order_status` | 10 m |

## OP50 — Self-test spec (the box tests boxes)

A finished, known-good connector box is the **fixture** for testing the next one.
The unit-under-test (UUT) is driven by a Yaver self-test script over its own
USB/CDC + companion `CONTROL` channel (`firmware/README.md`). All results post to
Talos via `mfg_telemetry`.

| # | Check | Method | Pass criteria |
|---|---|---|---|
| 1 | Rails | `SENSE` → `vin/vbus`; measure 5 V/3.3 V | 5.0±0.2 V, 3.3±0.15 V, no short |
| 2 | Input telemetry | INA219 over I²C | reads plausible `vin`/`ibus` |
| 3 | RS485 loopback | UUT `A/B` → fixture USB-RS485; UUT sends a Modbus read; `netcapture` (serial) on the fixture decodes it | CRC-valid frame, correct func/addr |
| 4 | RS485 A/B auto | `ABSWAP AUTO` | reports the polarity that yields a CRC-valid reply |
| 5 | Termination/bias | toggle `TERM`/`BIAS`, re-run #3 | both states behave; no bus lockup |
| 6 | RS232 loopback | UUT TX→RX via fixture | byte-exact echo |
| 7 | Isolation | megger primary ↔ `GND_ISO` | ≥ 1 kV, no breakdown |
| 8 | PD charge-while-host | plug a reference phone | phone **charges AND enumerates** the hub (the critical wired-mode claim) |
| 9 | USB-A passthrough | plug a USB-serial dongle | enumerates through the box hub |
| 10 | Wi-Fi SoftAP | scan for `Yaver-Box-XXXX`; connect; Modbus-TCP `:502` → re-run #3 over Wi-Fi | bridge works wirelessly |
| 11 | CAN *(if populated)* | loopback to fixture | frame round-trips |
| 12 | Status LEDs | `LED` cycle | all colors visible |

A serial number + the full PASS/FAIL vector is written to Talos (machine-knowledge
plane) so per-build yield + common-failure stats accrue → feeds DFM + the quote
model for *customer* boxes later.

## Bringup order (first article)

1. Print + de-rack OP10; verify fit of `base`/`lid`/standoffs with a bare module stack.
2. Wire OP30; **power-only smoke** (no phone): rails + INA219 before anything else.
3. OP40 flash; confirm CDC + SoftAP.
4. OP50 self-test on the bench using a *second* box (or a USB-RS485 dongle) as the fixture.
5. OP70 drive a real FDM printer / modbus-emu to prove the full app path.
6. Photograph + label; this unit becomes the **golden fixture** for the next 9.

## Definition of done (dogfood batch)

- **10 boxes** built, each with a Talos-logged PASS self-test vector.
- The `mfg_*` op runner (`ops_mfg.go`) drove OP10/OP40/OP50/OP70 for ≥ 1 unit
  (proves the agent-native execution path, not just hand-building).
- One box used to onboard a *new* machine type to the cell (proves the flywheel).
- Yield + cycle-time per op captured in Talos → first real DFM/quote data point.
