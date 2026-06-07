# Yaver Machine-Enablement Kits — the ladder

Not everyone needs the full Connector Box. Most "I just want to talk to this PLC"
jobs need a **cable and the right software**. These kits are a ladder from a $25
phone cable up to the full box — all **generic RS485 / Modbus** (no vendor lock),
all driven by the same Yaver app/agent. Pick the cheapest one that fits; they all
speak the same `netcapture` / `machine` / `box_*` software on the phone or a Pi.

> **The brain is always the phone or a Pi running Yaver.** The kit is just the
> wire + (optionally) a tiny gateway. Ease-first: plug in → `box_autoconnect` /
> the app auto-resolves A/B + termination → you're reading registers.

## Pick a kit

| You want… | Kit |
|---|---|
| Cheapest, plug a **phone** into a PLC at the panel for a quick look | **K0 — Phone RS485 Tap** |
| An **always-on edge box** at the machine (headless, remote) | **K1 — Pi PLC Harness** |
| **Wireless**: place the phone+camera freely, no cable to the PLC | **K2 — Wireless RS485 Stick** |
| The works: wired+wireless, charging, RS232, USB passthrough, isolation | **K3 — Connector Box** (`../yaver-connector-box/`) |

---

## K0 — Phone RS485 Tap  (~$25, the "just a cable" kit)

The minimum to put a phone on a Modbus-RTU bus.

**BOM**
| Part | ~$ |
|---|---|
| Isolated USB-RS485 dongle (FTDI, DSD TECH **SH-U11F** / Waveshare 23949) | 15–20 |
| USB-C OTG adapter (phone host) | 3 |
| 3-wire flying lead + ferrules (A/B/GND) → screw terminals | 2 |
| Inline A/B mini-swap toggle + 120 Ω termination switch (optional) | 3 |

**Brain:** phone (USB-OTG host) running the Yaver app → `netcapture` serial /
`machine`. The app's `device_filter.xml` auto-claims the FTDI on plug-in.
**Wiring:** dongle `A`→PLC `A/D+`, `B`→PLC `B/D−`, `GND`→PLC `GND`. If no reply,
flip the A/B switch (polarity isn't standardized). Terminate at the two bus ends.
**Use when:** bench/panel, technician with phone, short session. Isolated dongle =
safe on a real machine.

---

## K1 — Pi PLC Harness  (~$70 + Pi, the "leave it there" kit)

An always-on edge node bolted near the machine, reachable remotely.

**BOM**
| Part | ~$ |
|---|---|
| Raspberry Pi (Zero 2 W / 4 / 5) | varies |
| Isolated RS485 HAT (Waveshare **RS485 CAN HAT**) *or* the SH-U11F dongle | 15–25 |
| DIN/panel mount + 12–24 V→5 V buck for Pi power | 10 |
| 3-wire harness to the PLC terminals | 2 |

**Brain:** the Pi runs `yaver serve --netcapture` headless; you reach it over the
Yaver relay/mesh from your phone or laptop (no keyboard at the machine). The Pi's
serial port (`/dev/ttyS0`/`ttyUSB0`/`ttyAMA0`) feeds `netcapture` serial directly.
**Use when:** continuous monitoring, scheduled jobs, a machine you revisit. One Pi
per machine, fleet-managed via Yaver.

---

## K2 — Wireless RS485 Stick  (~$20, the "free the phone" kit)

> **Superseded for most uses by the Pi widget** (`../yaver-edge-pi/`, Widgets A/B):
> a Pi Zero 2 W runs the full agent + has Wi-Fi **and BLE** with zero custom
> firmware ("ESP is trouble"). Keep K2 only when you need the absolute smallest /
> cheapest wireless RS485 gateway and don't want edge compute.

A thumb-sized **ESP32-S3 SoftAP RS485 gateway** — the wireless half of the
Connector Box, alone. Plug it into the PLC's RS485; the phone joins its Wi-Fi and
talks Modbus-TCP. Now the phone + camera can sit anywhere to watch the machine.

**BOM**
| Part | ~$ |
|---|---|
| ESP32-S3 mini module (u.FL) | 4 |
| Isolated RS485 transceiver (ADM2587E) + term/bias | 6 |
| USB-C power in + 3.3 V LDO | 2 |
| 3-pin screw terminal (A/B/GND) + u.FL antenna | 3 |
| Enclosure (`rs485_stick.scad`) | 1 |

**Firmware:** the **same** `../yaver-connector-box/firmware/` built **wireless-only**
(no hub/PD/USB-A). Boots SoftAP `Yaver-Box-XXXX`; phone joins; the app calls
`box_autoconnect` → Modbus over Wi-Fi. Power from any USB-C charger / the PLC's
24 V via a buck.
**Use when:** vibing with the camera placed across the machine, multi-phone
access, no USB cable to the PLC. Enclosure renders from `rs485_stick.scad`.

---

## K3 — Connector Box  (the flagship)

Full design in `../yaver-connector-box/`: wired (USB-C powered hub + **charge while
host**) **and** wireless (SoftAP), isolated RS485 **+** RS232 **+** USB-A
passthrough **+** optional CAN, power telemetry, A/B-swap + termination under
software control. Use when one tool must cover any machine and any connection mode.

---

## Generic harness pinouts (no vendor hardcode)

PLC terminal labels vary — **always check the machine datasheet**, then map:

| Bus | Wires | Common labels (any of) |
|---|---|---|
| **RS485 2-wire** (Modbus-RTU) | `A`, `B`, `GND` | A/B · D+/D− · 485+/485− · TxRx+/TxRx− |
| **RS422 4-wire** | `TX+ TX− RX+ RX− GND` | Y/Z/A/B |
| **RS232** (DB9 DTE) | pin2=RX, pin3=TX, pin5=GND | varies — some swap 2/3 |

**Rules (the box/kits automate these, but know them):**
- **A/B polarity is NOT standardized** → if no reply, swap A/B (K0 toggle, or
  `box_autoconnect` / `ABSWAP AUTO` does it for you).
- **Termination:** 120 Ω at the **two ends of the trunk only** (never every node).
- **Bias:** one fail-safe bias point for the whole bus.
- **Isolation is mandatory** on a real machine (ground-loop / shock / VFD noise) —
  every kit above uses an isolated transceiver except a bare bench MAX485.
- **Baud/parity** must match both ends (`box_cmd BAUD <n>` / app setting).

## Software, identical across kits

Whatever the kit, the phone/Pi runs the same Yaver capabilities:
`netcapture` (deep wire analysis), `machine` (Modbus read/sniff/write),
`box_*` (one-tap connect + self-test for K2/K3), and the camera vision loop.
The kit only changes *how the bytes reach the bus*; the intelligence is constant.
