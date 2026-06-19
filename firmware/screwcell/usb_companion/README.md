# Screwdriver USB Companion

Status: V0 preferred path for keeping Raspberry Pi GPIO clean.

Purpose: drive the CAT Power screwdriver motor through BTS7960/IBT-2 without
using Raspberry Pi GPIO directly. The Pi talks USB serial. The companion owns
PWM timing, direction, soft-start, brake, watchdog, and optional current sensing.

This sits beside the existing `screwdriver_control.py` Pi-GPIO implementation,
which remains a fallback.

## Why

For Yaver Box V0, the Pi should stay a Linux edge computer:

- USB-RS485 for OCPP/JCWElec.
- USB camera / Ender USB / Ethernet for robotics.
- No exposed Pi GPIO terminals.
- No motor timing tied to Linux scheduling.

The screwdriver is the exception where GPIO/PWM is actually useful. So we put
that GPIO/PWM on a cheap USB companion board.

## Recommended Hardware

Preferred:

- Arduino Nano / Uno compatible board, or Raspberry Pi Pico running a USB-serial
  sketch.
- BTS7960 / IBT-2 motor driver.
- Mean Well EDR-120-24 at 24 V.
- 1000-2200 uF / 50 V bulk capacitor across BTS7960 B+ / B-.
- Optional 100 nF brush-noise capacitor across the motor.
- Optional current sense to ADC input.
- Physical motor kill switch or fuse in the 24 V motor feed.

Not preferred for this specific screwdriver:

- FT232H/MCP2221 directly from Linux for PWM. They are useful USB GPIO/I2C/SPI
  bridges, but screwdriver control needs deterministic ramp/brake/watchdog logic.
- Raspberry Pi GPIO, except as fallback during development.

Use industrial USB/Modbus DI/DO modules for slow field IO. Use the USB companion
for screwdriver PWM.

## Serial Protocol

Line-oriented ASCII over USB serial, `115200 8N1`.

Commands:

```text
PING
STATUS
ENABLE
DISABLE
BRAKE
DRIVE <dir> <duty_pct> <max_ms>
CALIBRATE
LIMIT <amps>
```

Replies:

```text
OK key=value ...
ERR code message
```

Examples:

```text
PING
OK pong=1

ENABLE
OK enabled=1

DRIVE FWD 45 1200
OK done=1 peak_a=0.00 ms=1200

BRAKE
OK brake=1
```

Safety defaults:

- companion starts disabled;
- watchdog stops motor when command time expires;
- direction changes brake first;
- duty is clamped;
- first ramp step is limited;
- `DISABLE` drops both enable pins.

## Wiring

Example Arduino pin map:

```text
Companion D5  -> BTS7960 RPWM
Companion D6  -> BTS7960 LPWM
Companion D7  -> BTS7960 R_EN
Companion D8  -> BTS7960 L_EN
Companion 5V  -> BTS7960 VCC
Companion GND -> BTS7960 GND
EDR -V        -> BTS7960 B-
EDR +V        -> fuse / kill switch -> BTS7960 B+
Motor         -> BTS7960 M+ / M-
Bulk cap      -> BTS7960 B+ / B-
```

Ground rule:

- Companion ground and BTS7960 logic ground must be common.
- Keep Pi isolated at the USB level as much as practical; do not run motor current
  through Pi ground wiring.

## Pi Integration

The Pi sees the companion as `/dev/ttyACM*` or `/dev/serial/by-id/...`.

Yaver Box recipe should record:

```json
{
  "project": "catpower-screwdriver",
  "driver": "usb_companion",
  "port": "/dev/serial/by-id/...",
  "motor": "CAT Power 36V brushed screwdriver at 24V",
  "hbridge": "BTS7960",
  "supply": "EDR-120-24"
}
```

## Source Anchors

- Existing Pi fallback driver: `firmware/screwcell/screwdriver_control.py`
- Existing runner: `firmware/screwcell/cell_runner.py`
- Talos build guide: `../talos/cloud/convex/buildGuides.ts`

