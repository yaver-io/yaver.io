# Yaver as a multi-robot management layer (Ender + Fairino + myCobot + PAROL6)

**Status:** Go agent + mobile UI + web UI BUILT and (where possible without
hardware) unit-tested on this Mac, 2026-06-08. Backends that talk to real
controllers are implemented from each vendor's documented protocol and marked
NEEDS-HARDWARE-VERIFY; the generic + bridge paths are fully exercised by tests.

## The idea

One layer — Yaver — drives every arm on the bench, so you never open a vendor
app. The same control surface, the same camera, the same teach-and-repeat, the
same host-AI loop, for:

| Robot | Kind | Driver | Transport |
|---|---|---|---|
| Creality Ender 3 Pro | 3-axis Cartesian | `robot` package (existing) | Marlin serial / `ender_ui` bridge |
| Fairino FR-series | 6-DOF cobot | `arm` / `fairino` | XML-RPC (`:20003/RPC2`, what `Robot.RPC` uses) |
| Elephant myCobot | 6-DOF (or N) | `arm` / `mycobot` | pymycobot binary frames over USB serial **or** TCP |
| Source Robotics PAROL6 | 6-DOF | `arm` / `parol6` | JSON bridge → `scripts/parol6_bridge.py` → official headless_commander |
| anything with a line protocol | N-DOF | `arm` / `generic_tcp`·`generic_serial` | one command template per op, **zero code** |

## Nothing about DOF is hardcoded

`arm.ArmInfo` *is* the robot: a list of `JointSpec{name,type,min,max,home,unit}`
(URDF conventions: revolute/prismatic/continuous, lower/upper/velocity limits).
DOF = `len(joints)`. It is **read from the robot** (`Describe()` — Fairino/myCobot
report their joint vector) or **defined in the UI**, and `Source` records which.
Every UI renders N joint controls from that — one screen for all robots.

Industry-standard motion: **MoveJ** (joint space), **MoveL** (linear Cartesian,
pose = x,y,z + roll,pitch,yaw in a named frame), **jog**, speed/accel as a % of
the robot's max. Targets outside a joint's limits are **refused, never silently
clamped**.

## Go agent layer (`desktop/agent/arm/` + `ops_arm.go`)

- `Backend` interface — DOF-agnostic: Describe / Status / Enable / JointState /
  Pose / MoveJoints / MoveLinear / WaitIdle / Stop / EStop / **FreeDrive** / Raw.
- `Controller` — move-and-verify (before-frame → move → wait → after-frame →
  vision verdict → obstruction e-stop), soft-limit checks from `ArmInfo`, reuses
  `robot.Camera` + `robot.VisionConfig`.
- Backends: `backend_fairino.go` (XML-RPC, `xmlrpc.go`), `backend_mycobot.go`
  (binary `0xFE 0xFE LEN cmd …data 0xFA`, angles `deg*100` int16-BE),
  `backend_generic.go` (command templates), `backend_bridge.go` (JSON/HTTP).
- `program.go` — teach-and-repeat: free-drive (hand-guide) → `Capture` waypoints
  → save → `RunProgram` (repeat), each waypoint verified.
- Verbs (`ops_arm.go`): `arm_drivers / arm_config_get|set / arm_describe /
  arm_status / arm_state / arm_enable / arm_jog / arm_movej / arm_movel /
  arm_home / arm_stop / arm_estop / arm_reset / arm_freedrive / arm_teach_capture
  / arm_program_save|list|get|delete|run / arm_snapshot / arm_look / arm_raw`.

## Camera — phone OR laptop looking at the robot → UI **and** host AI

The arm cell **shares the box's one eye** (`armCamera()` reuses the robot cell's
camera): `external` (an Android phone pushing its own camera over loopback),
`http(s)://` (a laptop/IP webcam snapshot URL), or `/dev/video0`. The same frame
feeds: the move-and-verify gate, the UI (`arm_snapshot` polled into an `<img>`),
and the **host Claude Code / Codex** via the `robot_camera` MCP tool (which now
falls back to `arm_snapshot`). So the host model sees the arm and does
vision-in-the-loop: `robot_camera {machine:box}` → `ops arm_movej …` → look again.

## UIs (one view per surface, all data-driven)

- Mobile: `mobile/app/arm.tsx` + `mobile/src/lib/armClient.ts` — driver setup,
  N joint jog rows, MoveJ/home/enable/e-stop, free-drive + capture + save +
  repeat, live camera.
- Web: `web/components/dashboard/ArmCellView.tsx` at `/dashboard/arm` (relay-only).

## Honest caveats

- No robot hardware here, so the Fairino/myCobot/PAROL6 wire details (exact MoveJ
  arg order, coord scaling, free-drive calls) are from docs and **must be
  confirmed on the metal**; they're isolated in each backend and easy to correct.
- PAROL6 is the one robot that still needs its **own** process running
  (`headless_commander`, wrapped by `scripts/parol6_bridge.py`) because its
  steps-based streaming protocol isn't safely reimplementable — Yaver drives the
  other three directly, no vendor app.
- `generic_tcp` is the universal escape hatch: any robot with a documented line
  protocol is wired by parameters, tested end-to-end (`arm_test.go`).
