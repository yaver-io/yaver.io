# Yaver Companion MCU — torque/force feedback + extra GPIO

> The cell is open-loop: the screwdriver clutch sets ~0.4 N·m mechanically and
> *nothing measures it*. The companion is a £3 microcontroller on a second
> serial link that adds the missing channel — **force/torque sensing** — so a
> screw is driven to a real torque target, plus spare GPIO the Ender board can't
> provide. It turns "the axis moved" (camera + encoder) into "the screw **seated
> at torque**". Pairs with `docs/yaver-phone-harness-analysis.md` §3.
>
> Code: firmware `firmware/yaver-companion/yaver_companion.ino`; host client
> `desktop/agent/robot/companion.go`; closed loop `robot/screw.go`
> (`Controller.DriveScrew`); tests `robot/screw_test.go` (pass, no hardware).

---

## 1. Why (the feedback gap)

| Channel | Have? | Answers |
|---|---|---|
| Encoder (`M114`) | ✅ | did the axis reach the commanded position |
| Camera (vision verdict) | ✅ | did the world visibly change / obstruction |
| **Force / torque** | ❌→**companion** | did the screw actually **seat**, and at what torque |

Without the third channel you can confirm the bit went *down* but not that it
*tightened a screw to spec* (vs missed the head, cross-threaded, or stripped).

---

## 2. Hardware (wire what you have — both sensors are compile-guarded)

| Part | Bus | Gives | Torque path |
|---|---|---|---|
| **INA219** | I²C | screwdriver supply **current** | `τ ≈ (I) · kT` (motor torque constant) |
| **HX711 + load cell** | 2-wire | **force** on the jig/bit | `τ = F · arm` (lever arm, direct & preferred) |
| MCU: Arduino Nano / RP2040 / **ESP32** | USB-serial / BLE | runs the loop, GPIO/PWM/ADC | — |
| MOSFET/relay | GPIO | switch the 24 V screwdriver (optional — or use the printer FAN port) | — |

ESP32 adds BLE so the companion can be **cable-free to the phone** (talks over
BLE-serial), which also gives galvanic isolation.

```
 screwdriver 24V ─┬─ INA219 (shunt) ─ motor
                  └─ MOSFET ◄─ MCU GPIO (or printer FAN port M106/M107)
 jig/bit ── load cell ── HX711 ── MCU
 MCU ── USB-serial (or BLE) ── phone/laptop (2nd port, NOT the printer's)
```

---

## 3. Wire protocol (ASCII, newline, 115200 — mirrors Marlin's simplicity)

```
PING             -> PONG
ZERO             -> OK                       # tare the load cell
SENSE            -> S cur=<mA> force=<g> tq=<Nmm>
GPIO <pin> <0|1> -> OK
STREAM <hz>      -> repeated "S ..." lines    # 0 = stop
```

Host side: `robot.LineCompanion` over any `io.ReadWriteCloser` (a serial fd, or
a BLE characteristic adapter). `robotd` opens it when `YAVER_ROBOT_COMPANION=
/dev/ttyUSB1` is set; absent → torque verbs return `{code:"no_companion"}` and
everything else is unchanged.

---

## 4. The closed loop — `Controller.DriveScrew`

```
travel to pole (X,Y, Zsafe); M400
camera: before frame
tool ON (companion GPIO pin, or Marlin FAN) ; companion.ZERO (tare)
loop, slow stepped plunge (Step mm, Feed≈60):
    Z -= Step ; move ; M400
    r = companion.SENSE
    if r.tq >= TargetTorqueNmm:  seated ✓  break        # the screw is tight
    if Z <= Zmax:               floor reached, stop      # hard depth limit
dwell at seat (optional) ; tool OFF ; retract to Zsafe
camera: after frame -> vision verdict
return { seated, measuredTorqueNmm, finalZ, steps, position, verify, frames }
```

Safety: hard **Zmax floor** (never plunge past, even if never seated), soft-limit
+ homing-gate + latched e-stop inherited from `Controller`, `M400`-gated every
step, tool forced OFF on any exit. `not_seated` is a first-class outcome (torque
never reached in the window) — the caller re-tries or flags the screw, never
assumes success.

Verified in `screw_test.go` with a fake companion whose torque rises as the
carriage plunges: seats at z≈3 when τ hits 400 N·mm, stops + retracts; an
unreachable target plunges to `Zmax` and returns `not_seated`; missing companion
returns `no_companion`.

---

## 5. Calibration (one-time, per rig)

- **Load cell**: `ZERO` (tare), hang a known mass → set `HX_COUNTS_PER_G`; measure
  the **arm** (cell→screw axis, mm). Torque is then direct & trustworthy.
- **Current path**: drive against a torque wrench at a few setpoints, fit
  `kT` (N·mm/A). Coarser than the load cell — use as fallback/redundancy.
- Cross-check the two channels; disagreement = a fault (bit slip, binding).

---

## 6. Protocol / API surface

- HTTP (robotd): `POST /robot/screw` with `ScrewParams` + `{verify,expectation}`.
- Agent (later): `robot_screw` ops verb (same body) + `gpio_set`/`sense_read`
  companion verbs, MCP-exposed so chat can say "drive pole 2 to 0.4 N·m and show
  me the torque + camera".
- Talos: `machineTelemetry` gains `screwTorqueNm`/`seated`; the RobotCellPanel
  shows a per-pole torque bar beside the camera frame.

---

## 7. Phone relevance

On the second-hand-phone host (`docs/yaver-phone-harness-analysis.md`), the
companion is the clean answer to "GPIO from a phone": the phone has none, but it
drives the companion over USB-serial/BLE through the same harness, and the
companion does all real-world I/O **and** the torque sensing — the highest-value
reason to add phone-driven GPIO at all.
