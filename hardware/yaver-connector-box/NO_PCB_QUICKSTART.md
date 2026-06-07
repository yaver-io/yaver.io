# Yaver Connector Box — no-PCB quick test (breadboard)

Validate the firmware + the Yaver app flow with **zero custom PCB** — just an
ESP32-S3 DevKit and a few breakout modules on a breadboard. Three tiers; each
builds on the last. Pin numbers come straight from `firmware/src/config.h`.

> This is the fastest path. The `TRAVELER.md` "Rev-A modules" build is the same
> idea, hardened into the printed enclosure; the Rev-B `pcb.md` board comes only
> after this proves out.

---

## Tier 0 — just the DevKit (~5 min): firmware + SoftAP + control

**Parts:** 1× **ESP32-S3-DevKitC-1** (the `-1U` external-antenna variant if you're
near metal). Nothing else.

1. Flash:
   ```bash
   cd hardware/yaver-connector-box/firmware
   pio run -t upload          # PlatformIO + the espressif32 platform
   pio device monitor         # optional: watch boot
   ```
2. On a laptop/phone, join Wi-Fi **`Yaver-Box-XXXXXXXX`** (password `yaver-connect`).
3. Talk to the control port:
   ```bash
   nc 192.168.4.1 8347        # (or any telnet/raw-TCP tool)
   PING        -> PONG
   INFO        -> INFO fw=... id=... link=wifi bus=rs485 ...
   LED 0 20 0  -> OK          (devkit RGB turns green)
   SENSE       -> S cur=0 force=0 ... (zeros until Tier 2 adds INA219)
   ```
   Wired control also works: plug the DevKit USB into a PC/phone and open the
   USB-CDC serial port at 115200 — same commands.

✅ Proves: firmware boots, SoftAP up, control protocol works, USB-CDC enumerates.

---

## Tier 1 — add RS485 → Modbus gateway to a real/sim slave

**Add:** 1× **RS485 TTL module** with DE/RE (MAX485 breakout for bench, or an
isolated ADM2483/ADM2587 module for realism) + a Modbus-RTU slave to talk to (a
real PLC, **or** a USB-RS485 dongle on a PC running `diagslave -m rtu`).

**Wire (DevKit → RS485 module):**

| RS485 module | ESP32-S3 pin |
|---|---|
| `RO` (receiver out) | GPIO18 (RS485_RX) |
| `DI` (driver in) | GPIO17 (RS485_TX) |
| `DE` **and** `RE` (tie together) | GPIO16 (RS485_DE) |
| `VCC` | 3V3 (or 5V per module) |
| `GND` | GND |
| `A` / `B` | to the slave's A/B (swap if no reply — or use `ABSWAP AUTO`) |

**Test the gateway (wireless, Modbus-TCP → RTU):**
```bash
# from a PC joined to the SoftAP:
mbpoll -m tcp -a 1 -r 1 -c 4 192.168.4.1      # reads holding regs 1..4 via the box
# the box wraps it as RTU, drives DE, talks to your slave, returns the reply
```
Or from the **Yaver mobile app**: join the SoftAP, point `netcapture` / `machine`
at `192.168.4.1:502` and watch the frames.

Polarity gotcha: if you get no reply, send `ABSWAP 1` (or `ABSWAP AUTO`) on the
control port — RS485 A/B is not standardized.

✅ Proves: the Wi-Fi↔RS485 Modbus bridge, DE timing, A/B swap, the "vibe with a
Modbus machine over Wi-Fi" path.

---

## Tier 2 — add power telemetry + the wired raw path + charging

**Add (any subset):**
- **INA219 module** (I²C) for `SENSE` power telemetry:

  | INA219 | ESP32-S3 |
  |---|---|
  | `SDA` | GPIO8 |
  | `SCL` | GPIO9 |
  | `VCC` | 3V3 |
  | `GND` | GND |
  | `Vin+/Vin-` | in series with your DC-in (e.g. a 24 V→5 V buck feeding the rig) |

  Then `SENSE` returns live `vin=` / `ibus=` (the charging/brown-out analytics).

- **USB-RS485 dongle** (FTDI, e.g. DSD TECH SH-U11F) for the **wired raw-data
  path** — plug it into the phone; the Yaver app's `netcapture` serial/`machine`
  talks straight through it (no firmware). The ESP stays the control/companion
  brain. (Build the firmware with `-DBOX_HAS_FT232` so the ESP releases the bus in
  wired mode.)

- **Powered USB-C hub module** (PD-source) to test **charge-while-host**: phone →
  hub → {USB-RS485 dongle + ESP DevKit}, hub powered from the bench supply →
  confirm the phone charges *and* enumerates the serial devices. This is the only
  part that needs a specific module (a plain hub won't charge the phone).

✅ Proves: power telemetry, the wired FTDI data path, and charge-while-host — the
last hardware risk before committing the Rev-B PCB.

---

## What you can skip on the breadboard

- **Isolation** — fine to use a non-isolated MAX485 for bench bring-up; **do NOT**
  connect to a real factory machine without an isolated transceiver (ground-loop /
  shock risk — see README §safety).
- **CAN, RS232, HX711, the PD source IC, the enclosure** — all optional for the
  quick test; add per tier as needed.

## Mapping back to the product

Once Tiers 0–2 pass, the same modules drop into `enclosure.scad` for the Rev-A
unit (`TRAVELER.md` OP30–OP60), and the validated firmware flashes unchanged onto
the Rev-B PCB later. Nothing you test here is throwaway.
