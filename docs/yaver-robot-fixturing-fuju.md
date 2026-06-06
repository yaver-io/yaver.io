# Presenting terminal blocks to the cell — fixturing + the Fuju build

> The question: how do you get ~10 "klemens" (terminal blocks) in front of the
> screwdriver so the robot can drive each screw home? Three answers, ordered by
> how much **custom engineering** they cost. Start at the top; graduate only when
> volume forces it.

## 1. Jig inside the Cartesian work area — least custom (start here)

A simple fixture holds the klemens at a **known grid** inside the robot's XY
envelope. The Cartesian screwdriver travels to each `(x,y)` and drives the screw
home to torque. **No extra motion hardware** — the robot you already have does the
whole job.

- Fixture = a 3D-printed plate (or aluminium with locating pins) that seats the
  klemens at a fixed pitch. The plate's first-klemens corner + pitch define the
  grid; nothing else is bespoke.
- Software: `robot_array_build {mode:"grid", cols, rows, pitchX, pitchY,
  originX/Y or captureOrigin, targetTorqueNmm}` expands the grid into a runnable
  fastening Program (move → drive-home, per klemens). `captureOrigin:true` anchors
  the grid at the current position, so you **teach the corner by jogging there**
  and Yaver fills in the rest. Serpentine ordering minimises travel.
- Limit: how many klemens fit the envelope (Ender ≈ 220×220; a Fuju-rail build can
  be much larger — see §4). When the strip outgrows the area, go to §2.

This is the recommendation: it's the array/teach software already shipped, and it
needs only a printed jig.

## 2. Single linear rail indexer (one Fuju axis) — scale-up for long strips

A linear rail (Fuju driver) indexes a **strip** of klemens past a **fixed**
screwdriver: move the rail to klemens *i*, plunge in place, repeat. The workpiece
moves, the tool stays.

- Software: `robot_array_build {mode:"linear", axis:"X", count, pitch, origin or
  captureOrigin, targetTorqueNmm}`. The generated screw steps carry **no X/Y**, so
  `DriveScrew` runs `AtCurrent` — it lifts Z and plunges where the rail left the
  klemens, rather than re-travelling.
- Cost: integrating the rail + driver + a fixed screwdriver mount. Worth it for a
  production strip that won't fit a jig, not for a one-off.

## 3. Robotic arm picks into a jig — most flexible, most hardware

An arm picks klemens from a feeder and places each into the jig (then the
screwdriver, §1, drives it). This is a **different scenario** — real
material-handling, a new `robot.Backend` for the arm — and only pays off when the
parts can't be pre-loaded into a jig by hand. Defer unless the line demands it.

## 4. Building a rigid 3-axis Cartesian from your 3 Fuju drivers

The Ender-3 was the prototype. With **3 Fuju drivers + linear rails** you build a
stiffer, larger, production-grade 3-axis Cartesian — and **Yaver drives it
unchanged**, because the integration point stays the same:

```
  Yaver  ──G-code/serial──>  controller board (Marlin/GRBL/Klipper)
                                   │  step + dir (3 axes)
                                   ▼
                          Fuju drivers ×3  ──>  steppers/servos on the rails
```

The Fuju drivers are **step/dir** — they take pulses, exactly what a Marlin board
emits to its stepper outputs. So: wire each Fuju driver's STEP/DIR/EN to a board
axis output (external-driver pins), keep the screwdriver on the freed E/FAN path,
and Yaver's existing G-code control runs the new machine with **no code change**.

Only two things differ from the Ender, both **config, not engineering**:

- **Steps/mm per axis** — depends on the rail lead × Fuju microstepping. Set it in
  the cell config (`stepsPerMm: {x,y,z}`); Yaver pushes `M92` to the board on
  connect, so the rails move in real millimetres. (Or set it once in firmware.)
- **Work envelope** — set `envelope` to the rails' travel so soft-limits match the
  bigger volume.

Everything else — jog, home, camera verify, encoder cross-check, calibration (Z
touch-off), torque seating, teach-and-repeat, the klemens array generator — works
identically on the Fuju machine and the Ender. The profile is still
`cartesian+screwdriver`; only the numbers change.

## Decision

Build the **3-axis Fuju Cartesian** as the real machine (rigid, sized to your
strip), load klemens into a **printed/located jig** in its work area (§1), and let
`robot_array_build` turn the jig grid into the fastening program. Keep the linear
rail (§2) and the arm (§3) on the shelf for when a strip outgrows the jig or the
line needs auto-feeding. That's the most capability for the least custom build.
