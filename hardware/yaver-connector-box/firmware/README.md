# Yaver Connector Box — ESP32-S3 firmware (the facade brain)

**This is microcontroller firmware, not an OS. The box runs no Linux, no Yaver.**
All intelligence (AI runner, vision, `netcapture`, `machine`/`robot`) stays on the
**phone**. This firmware only: (1) bridges the phone link to the machine bus, and
(2) reports the box's own health/identity + lets the app toggle bus options. That
"facade, not a PC" rule is the whole point — keep this firmware dumb, small, and
reboot-instant.

Build with ESP-IDF or Arduino-ESP32 for `ESP32-S3-WROOM-1U-N8`.

## Two link paths, one bridge

```
WIRED:    phone (USB host) ──USB-C──▶ hub ──▶ ESP32-S3 native USB (CDC-ACM) ──┐
WIRELESS: phone (Wi-Fi) ──join SoftAP "Yaver-Box-XXXX"──▶ TCP/WS/Modbus-TCP ──┤
                                                                              ▼
                                                            UART1 ⇄ isolated RS485 (A/B)
                                                            UART2 ⇄ isolated RS232
                                                            (opt) TWAI ⇄ CAN
```

The firmware presents the **same bridge** over whichever link is active. Either
path carries two multiplexed channels:

- **DATA channel** — transparent bytes ⇄ the selected machine bus (RS485/RS232/CAN).
  This is what `netcapture` (serial decoder) and `machine`/`robot` on the phone
  talk through. The firmware does **no protocol parsing** of machine traffic — it's
  a pipe (parsing/AI is the phone's job).
- **CONTROL channel** — the companion ASCII protocol below (box health + bus config).

### Wired (USB-CDC)
- ESP32-S3 native USB as **CDC-ACM**. Two interfaces (or one CDC + control framing):
  CDC0 = DATA (raw bus), CDC1 = CONTROL. Single-CDC fallback: CONTROL lines are
  prefixed `#` and DATA is everything else (escape `#` as `##`).
- VID/PID: use Espressif default or a registered pair; list it in
  `../android/device_filter.xml` so the Yaver app auto-claims it.

### Wireless (SoftAP, no infrastructure Wi-Fi)
- Boots a **SoftAP** `Yaver-Box-<chipid>` (WPA2, password on the QR label).
  No factory Wi-Fi needed — the phone joins the box directly. (STA-join-existing-AP
  is an optional setting, off by default to stay "appliance, not PC".)
- Services on `192.168.4.1`:
  - **`:502` Modbus-TCP ⇄ Modbus-RTU gateway** — so even generic Modbus tools and
    the phone's `netcapture`/`machine` work unchanged over Wi-Fi.
  - **`:8347/ws` WebSocket** — DATA channel (binary frames) + CONTROL (text frames),
    matching the phone HTTP port convention.
  - **`:9000` raw TCP** — transparent socket ⇄ selected UART (telnet-style bridge).
- mDNS `_yaver-box._tcp` so the app discovers it after joining the AP.

## CONTROL protocol (ASCII, newline-terminated, 115200 wired / over WS wireless)

Superset of `desktop/agent/robot/companion.go` so the existing companion driver
works against the box unchanged:

| Cmd | Reply | Meaning |
|---|---|---|
| `PING` | `PONG` | liveness |
| `INFO` | `INFO fw=<ver> id=<chipid> link=<usb\|wifi> bus=<rs485\|rs232\|can>` | identity |
| `SENSE` | `S cur=<mA> force=<g> tq=<Nmm> vin=<mV> vbus=<mV> ibus=<mA>` | force/torque (HX711/INA219) **+ power telemetry** |
| `ZERO` | `OK` | tare load cell |
| `STREAM <hz>` | repeated `S ...` | periodic sense (0=stop) |
| `GPIO <pin> <0\|1>` | `OK` | advisory output (tool enable etc — never a safety chain) |
| `BUS <rs485\|rs232\|can>` | `OK` | select active machine bus for DATA |
| `BAUD <n>` | `OK` | set bus baud (default 9600/115200) |
| `ABSWAP <0\|1>` | `OK` | RS485 A/B polarity swap (drives `AB_SEL`/U11) |
| `TERM <0\|1>` | `OK` | RS485 120 Ω termination |
| `BIAS <0\|1>` | `OK` | RS485 fail-safe bias |
| `LED <r> <g> <b>` | `OK` | status LED |

### Power deep-analysis (the charging-while-host telemetry)
`SENSE` returns `vin`, `vbus`, `ibus` from INA219 (input) + TPS65987D I²C (PD
VBUS/IBUS). The phone surfaces "charging 9 V @ 1.6 A, input 23.9 V, box 0.7 A" and
flags brown-outs/over-temp — the same way `netcapture` flags bus faults. The box
never decides anything from this; it just reports.

## A/B auto-detect helper (RS485 polarity gotcha)
On request `ABSWAP AUTO`, the firmware toggles `AB_SEL`, briefly drives a Modbus
read, and reports which polarity yielded a CRC-valid reply — so the phone can
"just works" past the A/B-not-standardized problem. (It only *reports*; the phone's
`netcapture` confirms frames.)

## Watchdog & safety
- Task watchdog; on link loss, **release the bus** (tri-state DE) — never leave the
  RS485 driver asserted. GPIO outs fail to the safe (off) state.
- No firmware OTA from the bus side. OTA only over the SoftAP CONTROL channel with
  the label password, so the machine can't reflash the box.

## Why not put Yaver on the box?
Because then it's a PC: an OS to patch, a disk to corrupt, a thing to secure, a
boot time, a BOM 5× higher. The phone already has the compute, the camera, the
modem, the screen, and the Yaver runner. The box's job is to be the cheap,
certifiable, instant **wire facade** — and nothing more.
