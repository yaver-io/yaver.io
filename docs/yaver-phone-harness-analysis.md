# Yaver Wire Harness — a second-hand phone as the Cartesian-robot controller

> Deep analysis. Goal: throw away the tethered laptop and drive the Ender-3
> screwdriver cell from a **£30 second-hand Android phone running Yaver** — the
> phone is controller + camera-eye + UPS + dashboard — connected through the
> right **ara kablo** (adapter cables) and, where needed, a tiny GPIO companion.
> Pairs with `docs/robot-screwdriver-cell.md` (the cell) and
> `docs/robot-protocol.md` (the move-and-verify contract).

---

## 0. Thesis

A used phone is *almost the perfect* machine controller and we're currently
wasting a whole laptop on it:

| Phone already has | We were using the laptop for |
|---|---|
| ARM CPU + RAM | running Yaver / robotd |
| **Camera** | a separate USB webcam (the verify eye) |
| **Battery** | nothing — and it's a free **UPS**: survives a power blip mid-screw |
| Touch screen | a local jog UI / dashboard |
| WiFi + BT | the mesh uplink |
| USB-C/micro-USB (OTG host) | the USB-serial link to Marlin |

So the phone collapses **5 boxes into 1** (controller, webcam, UPS, display,
network) for ~£30, and reuses e-waste — squarely Yaver's "lower opex" wedge.

The four real gaps, each analysed below: **(1)** Android isn't Linux and the Go
agent doesn't target it; **(2)** the USB/OTG/charging "ara kablo" chain; **(3)**
phones have **no GPIO**; **(4)** Android power/permission hostility to a headless
24/7 machine controller.

---

## 1. Putting Yaver *on* the phone — three stacks

The Go agent targets darwin/linux/windows only (no `GOOS=android`). Three ways
to bridge that, in increasing polish:

| | **A. Native RN app** (ship) | **B. Termux + arm64 binary** (prototype) | **C. Rooted + chroot** (niche) |
|---|---|---|---|
| Yaver runtime | the Yaver **mobile app** + a `YaverUsbSerial` native module | `robotd`/agent **arm64 Linux binary** under Termux (proot) | full Linux agent in a chroot |
| USB-serial path | `usb-serial-for-android` (felHR85/mik3y) — CH340/FTDI/CP210x in **user space**, no root | `termux-usb` hands the app a **USB file descriptor**; a userspace CH340 driver speaks to it | kernel `ch341` module → real `/dev/ttyUSB0` |
| Camera | Camera2/CameraX in the app | `termux-camera-photo` | v4l2 over UVC (rare on phones) |
| GPIO (see §3) | native module → USB-I2C/companion MCU | same libs over the fd | full Linux GPIO-over-USB |
| Distribution | App/Play Store, one tap | adb/F-Droid, technical | root, very technical |
| Effort | **weeks** (native module) | **days** (reuse `robotd`, add fd-open) | medium, fragile |
| Headless 24/7 | foreground-service, solid | OK if Termux:Boot + wakelock | best |

**Recommendation:** **B now, A as the product.** `robotd` already exists and is
stdlib-only; the *only* change for Termux is an `OpenMarlinFD(fd)` backend
(open the `termux-usb` fd instead of `/dev/ttyUSB0`) — a half-day. That proves
phone-as-host this week with zero native code. Then build the `YaverUsbSerial`
native module so the **jog screen we already shipped** drives the printer
locally with no Termux.

> Key architectural win: the **protocol is identical** whichever stack runs. The
> phone is just another `machineEdgeDevices` row with `host:"android"`; Talos and
> the mobile UI never learn which stack is underneath.

---

## 2. The "ara kablo" chain — physical USB layer (deep)

The signal path, phone → machine:

```
[phone USB-C/µUSB] →(OTG)→ [USB-A female] → [USB-A→USB-B cable] → [CH340 on Marlin board]
        host                 adapter              stock cable           1a86:7523
```

### 2.1 Connector / OTG matrix
| Phone port | Cable to printer (USB-B) | Notes |
|---|---|---|
| USB-C (most 2018+) | **USB-C → USB-B** single cable (cleanest) **or** USB-C-OTG → USB-A → USB-A→B | one cable = fewer dropout junctions |
| micro-USB (older 2nd-hand) | micro-USB-**OTG** → USB-A(f) → USB-A→B cable | needs an OTG adapter that shorts the ID pin |
Phone must support **USB Host (OTG)** — virtually all Android ≥5; verify with a
USB-OTG-check app or `lsusb` under Termux. iPhones: USB host is locked down →
Android only for this role.

### 2.2 Power — the two hard cases
1. **Host mode drains, doesn't charge.** Hosting + screen + WiFi + vision can
   out-draw the battery. The CH340 itself sips ~30 mA; the phone is the load.
   Fixes (the "ara kablo" options), best-last:
   - **(a) Just run on battery** — it's a UPS; a full charge lasts hours. Recharge between jobs.
   - **(b) OTG-Y / "accessory charging" cable** — OTG splitter with a charge-in port → charges *while* hosting. ~£5. Caveat: some phones need the charger present at enumeration; a few never allow OTG+charge — **test per model**.
   - **(c) Powered USB hub on the OTG port** — hub powers the bus (and the printer's logic); some back-feed charge to the phone.
   - **(d) USB-C dock with PD passthrough** *(recommended)* — PD charges the phone, USB-A hosts the printer **plus** extra cameras and the GPIO companion (§3). The "ara kablo" becomes a small dock = headroom for the whole harness.
2. **Never back-power the motors from the phone.** Motors run off the printer's
   own 24 V PSU; USB carries **only logic/serial**. Keep the phone out of the
   motor power domain entirely.

### 2.3 Signal-integrity gotchas (learned the hard way today)
- **Cheap/long USB or OTG cables cause CH340 dropouts** — we hit a live `EIO`
  this session. Use **short, shielded** cables; tape down strain relief (worn
  second-hand USB-C jacks go intermittent under cable weight).
- **Common-ground over USB** ties phone GND ↔ printer logic GND. Usually fine;
  in electrically noisy cells add a **USB isolator (ADuM3160-class) "ara kablo"**
  between phone and printer to break ground loops and protect the phone.
- The board **resets on USB open** (DTR) — the phone-side driver must wait ~2 s
  after enumerate before talking (same rule as the laptop).

### 2.4 Recommended ara-kablo BOM (per phone)
- USB-C phone: 1× **USB-C PD dock** (PD-in + ≥2 USB-A) + 1× short USB-A→USB-B.
- micro-USB phone: 1× micro-USB **OTG-Y** (charge-in) + 1× short USB-A→USB-B.
- Optional: 1× **USB isolator** (noisy environments), 1× right-angle USB-B.

---

## 3. GPIO from a phone — the core question

**A phone has no GPIO pins.** The analysis that makes this a non-problem:

### 3.1 The Ender-3's MCU *is* your GPIO controller — over the same serial link
You already command a 30-pin MCU (PWM/ADC/digital) via G-code on the wire you're
already using. **No phone GPIO, no extra cable** for the common cases:

| Need | Marlin G-code | Used for |
|---|---|---|
| Digital/PWM out on a spare pin | `M42 P<pin> S<0-255>` | relays, MOSFETs, LEDs |
| FAN MOSFET (switched 24 V) | `M106 S255` / `M107` | **the screwdriver** (what we use) |
| Servo | `M280 P<i> S<angle>` | a servo gripper / flipper |
| Read endstops | `M119` | limit / homing / presence |
| Coordinated motion | `G0/G1/G28` | the XYZ itself |

So screwdriver, an extra relay, a servo gripper, a vacuum solenoid — all ride
the **printer board's pins**, driven from the phone purely as G-code text.

### 3.2 When the board's pins aren't enough → USB-attached GPIO
For more I/O, analog/force sensing, fast loops, or galvanic isolation, the phone
gets GPIO from a **USB peripheral on the OTG dock**. Decision matrix:

| Bridge | What it gives | Cable case | Best for |
|---|---|---|---|
| **Companion MCU over USB-serial** (Arduino Nano / RP2040 / ESP32) | unlimited GPIO/PWM/ADC/encoders, runs tight local loops | 2nd USB-serial on the dock (or shares the hub) | **the robust path** — Yaver ships the firmware; a tiny "harness controller board" |
| **USB-GPIO chip** FT232H / **MCP2221** / CH341 | direct GPIO + I²C/SPI/ADC, no firmware | one more USB device on the dock | quick digital I/O + I²C sensors |
| **I²C/SPI hubs** behind either: PCA9685 (16×PWM/servo), MCP23017 (16×GPIO), ADS1115 (ADC), **INA219** (current), **HX711** (load cell) | scale to many actuators/sensors | ride the MCU/USB-I²C | servo arrays, **torque/force sensing** |
| **ESP32 over BLE/WiFi** | GPIO with **no cable to the phone** | *no ara kablo* — talks over mesh/BLE | isolation, reach, avoiding dock sprawl |

### 3.3 This is where GPIO-from-phone *earns its keep*: closed-loop torque
The cell is open-loop (the clutch sets 0.4 N·m, nothing measures it). Add a
**companion MCU + INA219 current-sense** (or an **HX711 load cell** under the
jig) and the phone reads real force/torque over the harness — turning the
camera+encoder "did it move" loop into a **"did the screw actually seat at
torque"** loop. That's the missing feedback channel, delivered by phone-driven
GPIO.

### 3.3b One USB, zero extra cabling — repurpose the freed nozzle outputs (best)

The nozzle is gone, which **frees the two beefiest outputs on the Marlin board** —
and they're already on the one USB-serial link. This is the minimal-cabling answer:

| Freed output | Now unused because… | Re-use as the screwdriver | G-code | Notes |
|---|---|---|---|---|
| **Extruder (E) stepper driver** | no filament to push | **a stepper/geared screwdriver** — the E motor *is* the driver | `M302 P1` (allow cold "extrude") then `G1 E<turns> F<rpm>` | full **rotation + speed + direction**, all over the one USB; mount the bit on the E-motor shaft / a geared coupling |
| **Hotend heater MOSFET (E0)** | no heater | **switch a DC screwdriver** (it's a ~10–15 A MOSFET, far beefier than the 1 A fan port) | `M42 P<heater_pin> S255/S0` (thermal protection already disabled per klemens SPEC) | on/off only, but **no external relay needed** — it can carry the motor current directly |
| Servo header (`PB0`) | — | a servo screwdriver / part flipper | `M280 P0 S<angle>` | — |
| **TMC2209 StallGuard** (if the board has TMC drivers) | — | **torque/load sensing on the E-stepper screwdriver** | `M122` / StallGuard regs | gives the seat-torque feedback **without a companion/load-cell**, over the same USB |

**Bottom line for "less cabling":** drive the screwdriver as the **repurposed E-axis
stepper** (`G1 E` after `M302 P1`) — you get controlled rotation, direction, and (with
TMC StallGuard) even torque, **entirely over the single USB to the Ender board**. No FAN
relay, no second serial, no companion in the common case. The FAN-port path (§ tool=fan)
stays as the simple on/off DC option; the companion (`docs/yaver-companion-mcu.md`) becomes
**optional** — only needed when the board lacks TMC StallGuard and you still want a load
cell. A future `robot` backend verb maps "tool on/plunge/torque" onto `G1 E` + StallGuard
so the protocol is unchanged.

### 3.4 The decision tree
```
Need an output the printer board has a spare pin for?  → M42 / M106 / M280   (no extra HW)
Need more digital/PWM than spare pins?                 → PCA9685 / MCP23017 via USB-I²C or companion MCU
Need analog / force / current (torque verify)?         → INA219 / HX711 on a companion MCU
Need isolation or no cable?                            → ESP32 over BLE/WiFi
```

---

## 4. The camera — the phone *is* the eye

The rear camera replaces the USB webcam: Camera2/CameraX in the app (or
`termux-camera-photo` in stack B). The **live MJPEG stream** capability we built
(`robot.GstCamera.StreamMJPEG`, `GET /robot/stream`) is the laptop version of
this; on the phone the same `multipart/x-mixed-replace` stream is produced from
Camera2 frames and viewed from Talos/web/other phones as a plain `<img>`. The
verify-LLM runs on the **mesh** (the phone holds no model) or a small on-device
model for offline. Mounting the phone to face the work removes a device **and**
its cable.

---

## 5. The "Yaver Wire Harness" — two true readings

1. **The kit (the literal harness):** phone mount + the **ara-kablo bundle**
   (PD dock / OTG-Y + short shielded USB-B + optional **companion MCU board** +
   sensor pigtails + USB isolator) + the printed screwdriver mount. A bag that
   turns *any* Ender-3 + *any* second-hand phone into a camera-verified robot cell.
2. **The application (the work):** assembling **electrical wire harnesses** —
   the robot drives the M2.5 rising-cage screws on terminal blocks (Megaradar
   SBDK 5.08, the klemens job). The cell *is* a wire-harness station; the kit
   *is* a wire harness. Both meanings ship together.

Yaver provides across both: the **app** (jog/verify/stream/run-job — jog screen
already built), the **protocol**, the **companion firmware**, the **calibration
flow** (`robot_jog` + store-pole, the native twin of `klemens/calibrate.py`),
the **cable BOM**, and the **mount STL**.

---

## 6. Phased plan

| Phase | Deliverable | Proof |
|---|---|---|
| **P0 (days)** | `OpenMarlinFD(fd)` backend → `robotd` arm64 under **Termux + termux-usb**; phone camera stream; verify over mesh | a phone (no laptop) homes + jogs the printer, streams its own camera |
| **P1 (weeks)** | `YaverUsbSerial` RN native module (usb-serial-for-android) + Camera2; the **shipped jog screen** drives the printer locally | tap "Z ▲" on the phone → printer moves → phone-camera verify, no Termux |
| **P2** | **companion MCU** firmware + Yaver GPIO protocol (M42 passthrough + `gpio_*`/`sense_*` verbs); INA219/HX711 torque | closed-loop "screw seated at torque", not just "moved" |
| **P3** | kit BOM + mount + docs; Talos product surface (RobotCellPanel + stream view) | a packaged second-hand-phone robot cell |

---

## 7. Risks & Android-specific hostility (must-handle)

- **USB permission prompt on every attach** — Android pops a per-device consent.
  Mitigate with an `intent-filter` + "always open for this device" + a
  foreground service; truly headless still needs the one-time grant (or MANAGED
  provisioning on enterprise).
- **Doze / battery optimization kills background apps** — a 24/7 controller MUST
  run a **foreground service** with a persistent notification (dashcam pattern)
  and be whitelisted from battery optimization, or Android suspends motion mid-job.
- **OTG+charge variance & worn connectors** on second-hand units → §2.2; pick the
  PD-dock path and tape the cable.
- **CH340 cable dropouts** (seen live) → short shielded cable; the driver must
  auto-reconnect + re-home, never silent-retry into a crash.
- **Termux USB persistence** across reboot (Termux:Boot + re-grant) for stack B.
- **No `GOOS=android`** — stack A's serial+camera+GPIO are native-module work;
  budget it. Stack B sidesteps it for the prototype.
- **Ground loops / ESD** through the phone over USB → USB isolator in noisy cells.
- **Safety** is unchanged and edge-enforced: soft-limits, homing-gate, latched
  e-stop, tool-off, `M400` gating — the phone is just the new host for the same
  guards in `robot/control.go`.

---

## 8. Bottom line

Nothing here needs the phone to have GPIO, and nothing needs a laptop. The
printer's own MCU is the GPIO controller (G-code over the one USB-serial wire);
a PD dock solves OTG+charge; a £3 companion MCU adds real sensing (torque) when
you want closed-loop screws; the phone's camera + battery delete the webcam and
add a UPS. The same Yaver protocol and the same jog screen we already shipped
ride on top — so "second-hand phone instead of the laptop" is a **backend swap
(`SerialBackend`/`OpenMarlinFD`) + a native USB-serial module**, not a rewrite.
