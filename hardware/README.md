# Yaver Machine-Edge Hardware — three widgets, one mesh

Yaver turns any phone, Pi, or laptop into a machine-troubleshooting + control node
(`netcapture` deep wire-analysis, `machine` Modbus, `robot` G-code + camera-verify,
mesh, vault). The hardware here is just how the bytes reach the machine bus. Three
**edge widgets**, all zero-config, all joined by **Yaver mesh**, all degrading to
**BLE** when a production floor has no Wi-Fi.

| Widget | Brain | Camera | Bus link | Best for |
|---|---|---|---|---|
| **A — Pi Edge** (`yaver-edge-pi/`) | Raspberry Pi (runs the agent) | none | isolated RS485 HAT/dongle → machine | always-on, headless, unattended monitoring |
| **B — Pi + Phone** (`yaver-edge-pi/`) | Raspberry Pi (bus) + phone (camera/vision) | phone | Pi RS485 → machine | vibing/teaching a machine with vision, attended |
| **C — Phone + Box** (`yaver-connector-box/`) | phone (USB-host, runs the app) | phone | **passive** isolated USB-RS485 box + cable | grab-and-go, one technician, no extra compute |

> **Why Pi for A/B and not an MCU?** A Pi runs the **full Yaver agent unchanged**
> (no custom firmware) — `netcapture`, `machine`, `robot`, `mesh`, `vault`, the
> runner. An ESP32 can only ever be a dumb bridge and its firmware was a
> maintenance tax. So: **Pi = brain when you want compute at the edge; phone =
> brain when you don't.** The "connector box" (C) accordingly drops its MCU — for
> the phone-brain path it's a **passive** isolated USB-RS485 breakout (+ optional
> RS232 / USB-A passthrough / USB-C PD charging). No firmware anywhere.

## Connectivity ladder (zero-config, automatic)

Every widget tries, in order — the app/agent picks the best available; the user
configures nothing:

1. **Internet present → Yaver mesh / relay.** Normal remote operation; reach the
   widget from anywhere as the same user.
2. **Local network, no internet → direct LAN / phone hotspot.** The Pi auto-joins
   a known SSID (or the phone's hotspot); the agent API + camera run over local IP.
   Full bandwidth (camera works).
3. **No Wi-Fi at all (locked-down floor) → BLE.** The Pi runs a **BLE GATT bridge**
   (`yaver-edge-pi/ble-bridge/`) that tunnels the agent's HTTP/ops API over
   Bluetooth LE. The phone talks control + Modbus + `netcapture` findings over BLE
   (low bandwidth: control/data yes, live video no — camera degrades to stills or
   waits for tier 2). Pairing is automatic from the provisioning identity.

Auth is identical across all three tiers — the phone's Yaver bearer is forwarded
whether it rides mesh, LAN, or BLE. Nothing re-implements auth per transport.

## Zero-config provisioning

All widgets use the shipped zero-touch flow: scan the QR once → own → attest →
token → autostart (`yaver provision`, `provision_postclaim.go`, `yaver.provision.yaml`).
After that the widget comes up on every boot, joins mesh, and (Pi) starts the BLE
bridge — no SSH, no typing.

## Folders

| Path | What |
|---|---|
| `yaver-edge-pi/` | Widgets **A & B** — Pi image/setup, BLE bridge, enclosure |
| `yaver-connector-box/` | Widget **C** — the box (now passive for phone-brain; ESP firmware kept but deprecated as a brain) |
| `yaver-kits/` | the cheap RS485 cable/adapter ladder (K0–K3) that feeds any widget |

Pick A/B when you want a box that thinks at the machine; pick C when the phone is
enough. They interoperate over the same mesh — a Pi widget and a phone can be on
the same machine, the phone lending its camera to the Pi's bus session.
