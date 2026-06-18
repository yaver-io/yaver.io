# Yaver Pi Edge — Widgets A & B

A Raspberry Pi that **is** the Yaver agent at the machine. No custom firmware —
the Pi runs `yaver serve --netcapture` and brings the full stack (`netcapture`
deep wire-analysis, `machine` Modbus read/sniff/write, `robot` G-code +
camera-verify, `mesh`, `vault`, the runner) to the bus. Two configs off one build:

- **Widget A — Pi only (no camera):** unattended monitoring/control. Headless,
  DIN-mounted, reached over Yaver mesh (or BLE on a no-Wi-Fi floor).
- **Widget B — Pi + phone-as-camera:** the Pi owns the bus; a phone on the same
  mesh lends its **camera** to the vision loop (camera-verify gating each move,
  reading an HMI screen, defect QA). Attended "vibe with the machine."

> Why Pi, not ESP: the Pi runs the agent **unchanged** — zero firmware to write or
> maintain (ESP was a dead end as a brain). See `../README.md`.

## Bill of materials (per widget)

| Part | ~$ | Notes |
|---|---|---|
| **Raspberry Pi 3B/3B+** (Wi-Fi + **BLE** built in) | used/cheap | Recommended cheap IoT edge target for machine IO. Run Raspberry Pi OS Lite **64-bit** so the npm-distributed Yaver Go agent uses the linux-arm64 binary. |
| Raspberry Pi Zero 2 W | 15 | Works for very small monitor-only installs, but Pi 3B/3B+ is easier to wire/debug. |
| Raspberry Pi 4/5 | varies | Best as a dev/headroom board. Use remote machines for GPUs, models, and coding agents; the edge Pi should stay small. |
| Isolated RS485 for Pi — **Waveshare RS485 CAN HAT** (isolated) *or* an isolated USB-RS485 dongle (DSD TECH SH-U11F) | 15–25 | isolation mandatory on a factory floor |
| microSD (16 GB+ A1) | 5 | |
| 9–24 V → 5 V/3 A buck (industrial) *or* USB-C PSU | 6 | 24 V is common at the panel |
| DIN/panel enclosure (`enclosure.scad`) | 1 | printed |
| 3-wire harness to the PLC (A/B/GND) | 2 | see `../yaver-kits/KITS.md` pinouts |

**Widget B** adds nothing on the Pi — the camera is the **phone** (any phone with
the Yaver app), mounted on the `../yaver-connector-box/phone_mount.scad` rig
pointed at the machine.

## Setup (zero-config)

```bash
# flash Raspberry Pi OS Lite (64-bit), boot, then one line:
curl -fsSL https://yaver.io/pi | sudo bash         # or: sudo ./setup-pi.sh
```

`setup-pi.sh` (in this folder) is idempotent and:
1. installs Node + `YAVER_IOT_EDGE=1 npm install -g yaver-cli` (Go agent only; skips local coding agents, mobile build tools, Playwright, voice models, and sandbox bootstrap),
2. enables the UART / RS485 serial port (disables the login console on it),
3. runs the zero-touch claim (`yaver provision claim` from the QR, or
   `yaver auth --headless`) and enables autostart (`yaver serve --netcapture`),
4. installs the **BLE bridge** systemd service (`ble-bridge/`) so the phone can
   reach the Pi even with no Wi-Fi,
5. joins **Yaver mesh** so you reach it from anywhere as the same user.

After this the Pi comes up on every boot, on mesh, with BLE up — no SSH, no typing.

## Pi 3B / Yaver agent compatibility

Use **Raspberry Pi OS Lite 64-bit** on Pi 3B/3B+. The npm package currently
downloads `linux-arm64` Yaver agent assets; 32-bit `armv7/armhf` Pi OS is not
the supported Yaver IoT lane. This is intentional for V0: the Pi is an edge IO
appliance for RS485/GPIO/USB and mesh, while LLMs, GPUs, browser automation,
and coding agents run on remote machines reached through Yaver.

The lightweight install path is:

```bash
YAVER_IOT_EDGE=1 npm install -g yaver-cli
yaver auth --headless
yaver serve --netcapture
```

Use a Pi 4 first while developing if you want more headroom, then move the
proven machine wiring/config back to Pi 3B/3B+.

## How each connection tier behaves (camera implications)

| Floor condition | Transport | Widget A | Widget B (camera) |
|---|---|---|---|
| Internet | mesh/relay | full remote | full remote + live camera |
| LAN / phone hotspot, no internet | local IP | full local | full local + live camera |
| **No Wi-Fi at all** | **BLE** | full control + `netcapture` findings + Modbus | control works; **camera degrades** (stills on demand, no live video — BLE bandwidth). Use a phone hotspot if live video is needed. |

BLE carries the agent's control/ops + Modbus + analysis JSON fine; it cannot carry
live video. That's the one honest limitation of the no-Wi-Fi tier — `../README.md`
connectivity ladder spells it out.

## Driving a machine from the Pi

Same software as everywhere else:
- `netcapture` taps the bus (RS485/Modbus, S7, etc.) for deep analysis.
- `machine` reads/sniffs/writes Modbus registers; the LLM labels them.
- `robot` drives Marlin/G-code (a 3D-printer-as-Cartesian-robot or arm) with the
  **phone's camera** (Widget B) gating each move via the vision verdict.
- `box_autoconnect` (one-tap A/B + termination) works here too — the Pi is just
  another agent the verb runs on.

## Files

| File | What |
|---|---|
| `setup-pi.sh` | idempotent Pi installer (agent + serial + BLE + provisioning + autostart) |
| `enclosure.scad` | parametric Pi Zero 2 W + RS485 enclosure (DIN/panel) |
| `ble-bridge/peripheral.py` | BLE GATT ↔ agent-HTTP bridge (no-Wi-Fi transport) |
| `ble-bridge/GATT_PROTOCOL.md` | UUIDs + chunk framing + mobile-client spec |
| `ble-bridge/yaver-ble.service` | systemd unit for the bridge |
