# Robotics dev: Android box as eyes+hands, host Claude Code as the brain

**Status:** agent side BUILT + unit-tested on this Mac (2026-06-08); mobile
producer BUILT, needs on-device verification.

## The shape

A cheap second-hand **Android phone is the edge box**, on the bench opposite an
Ender 3 Pro (and later a Fairino / Dobot / a PLC cell). The phone:

- is **wired to the robot** (USB-serial for Marlin, or TCP/Ethernet for a cobot),
- uses **its own camera** as the workspace eye,
- runs the Yaver agent (`libyaver.so` on `127.0.0.1:18080`).

Your **host machine's Claude Code / Codex is the brain.** It does NOT run on the
phone. It reaches the phone over the Yaver mesh through the **Yaver MCP server**,
*sees* the camera, drives the robot, edits code, re-runs, looks again — a real
vision-in-the-loop robotics dev cycle. The phone is a sensor+actuator node; the
reasoning stays on your dev box.

```
  host Mac                         mesh (relay/LAN/QUIC)        Android box (the cell)
  ┌────────────────────┐                                       ┌───────────────────────┐
  │ Claude Code / Codex │ ── MCP robot_camera {machine:box} ──▶ │ agent  robot_snapshot │
  │  (the brain)        │ ◀── viewable image block ──────────── │   └─ ExternalCamera    │◀─ phone camera
  │                     │ ── MCP ops robot_move/jog ──────────▶ │   └─ SerialBackend ───▶│─▶ Ender (USB)
  └────────────────────┘                                       └───────────────────────┘
```

## Why a first-class MCP tool (not just an ops verb)

The robot cell self-registers as **ops verbs** reached via the `ops` MCP
grand-tool (`ops_robot.go`). But the `ops` MCP handler serializes every result
to **JSON text** (`httpserver.go` `case "ops"` → `mcpToolResult`), so a camera
frame returned by `robot_snapshot` arrives at the host as a base64 string the
model **cannot see**.

MCP can return *viewable* images, but only as a content block
(`{type:"image", data:<b64>, mimeType:"image/jpeg"}`) returned **directly** from
a tool — the pattern `mcpBrowserResult` uses. So we added a **first-class
`robot_camera` MCP tool** that is a thin host-side adapter:

1. it calls `dispatchOps({machine, verb:"robot_snapshot"})` — reusing the entire
   existing mesh path (remote targeting + auth + relay), no new remote endpoint;
2. it pulls the data: URL out of the `OpsResult`, strips the prefix, and
   re-emits it as an **image content block** the host model renders.

`httpserver.go` `case "robot_camera"`, registered in `mcp_tools.go`.

## Camera sources (generic across robotics + PLC/machinery)

`robot.Camera` is a one-method seam (`Grab() []byte`). We added two
implementations next to `GstCamera` so a box with **no `/dev/video0`** still has
an eye (`robot/external_camera.go`):

| Source (`camera` config / `YAVER_ROBOT_CAMERA`) | Use |
|---|---|
| `external` / `push` | **Android phone's OWN camera.** A push buffer the box fills over loopback via `robot_camera_push`. Stale-frame guard (5 s) so a frozen image never feeds a safety verdict. |
| `http://…` / `https://…` | **Network camera.** Pulls JPEG snapshots from an IP cam / phone-as-webcam / a cobot or PLC cell's camera. Great for Fairino over Ethernet. |
| *(empty)* / `/dev/videoN` | Local V4L2 via `gst-launch` (desktop/Pi box, unchanged). |

Selection is in `ensureRobot()` (`ops_robot.go`). A **camera-only** cell (no
motion backend — e.g. a PLC monitoring node) is now valid: `robotEnabled()`
returns true when a camera is configured.

## New verbs / tools

- **`robot_camera`** (host MCP tool) — returns the cell's frame as a **viewable
  image**, `machine`-targetable. *This is the host-LLM eye.*
- **`robot_camera_push`** (ops verb) — the box pushes a JPEG into its external
  buffer (producer side; loopback).
- **`robot_look`** (ops verb) — asks the box's **on-device** vision model about
  the current frame (when you want the edge to reason, not the host). Same
  provider ladder as the verify gate (`GHOST_VISION_* → OPENAI_* → local Ollama`).
- existing **`robot_snapshot`**, **`robot_verify`**, **`robot_move/jog/home`**
  (with `verify`) — unchanged, now fed by whichever camera source is set.

## The producer (phone capturing its own camera)

`mobile/src/lib/robotCameraStream.ts` — `startRobotCameraStream(target,
captureFrame, {fps})` loops a caller-supplied `captureFrame()` (base64 JPEG from
`react-native-vision-camera` `takeSnapshot`/`takePhoto`, already installed, or
`expo-camera` `takePictureAsync({base64:true})`) and pushes to the **co-located**
agent via `robotClient.cameraPush(loopbackTarget(selfId), frame)`. Self-throttled;
frames never leave the device. Set the box camera source to `external` first.

## End-to-end dev workflow (host Claude Code)

```text
# one-time on the Android box: camera source = its own camera
ops robot_config_set {machine:<box>, payload:{camera:"external", profile:"cartesian"}}
# (the Yaver app starts startRobotCameraStream → frames flow into the buffer)

# from your host Claude Code / Codex, with yaver MCP connected:
robot_camera {machine:<box>}                 # ← you SEE the bench
ops robot_jog {machine:<box>, payload:{axis:"X", dist:10, verify:"frames"}}
robot_camera {machine:<box>}                 # ← look again, confirm / fix code
```

`verify:"frames"` returns before/after frames without invoking the box's model —
your host LLM is the judge. `verify:"agent"` uses the box's local vision gate
(useful for unattended safety / obstruction e-stop).

## What's verified vs what remains

**Verified on this Mac:** full agent build green; `robot/` unit tests pass
(`external_camera_test.go`: push/grab/stale/copy semantics, HTTP camera pull +
non-JPEG rejection, `Camera` interface conformance); `robot_camera` MCP tool +
registration compile; mobile `tsc --noEmit` clean for the new files.

**Needs the device / hardware (cannot test on the Mac):**
- On-device: set camera `external`, run `startRobotCameraStream` with a real
  vision-camera capture, confirm `robot_camera {machine:box}` shows live frames
  on host Claude Code.
- **Ender (USB-serial) on Android**: the agent opens a Linux `/dev` tty; a
  non-rooted Android phone has no `/dev/ttyUSB0`. `robotd` already accepts
  `YAVER_ROBOT_SERIAL_FD` (a termux-usb fd) — wire `ensureRobot` to acquire it
  (P0, Termux) or add a native `YaverUsbSerial` (P1). Until then, drive the Ender
  via the HTTP **bridge** backend, or use a **TCP/Ethernet** robot.
- **Fairino / networked cobot**: the camera + host-vision half works today over
  WiFi (`http://` camera source). A generic parametric N-DOF **arm** layer
  (joint-space control, DOF read from the robot or defined in a UI) is the
  follow-on — see the WIP `arm` package design.
- **PLC / machinery**: the `vision_hmi` machine driver already reads a screen via
  camera + VLM; point it at the `external`/`http` camera the same way.
