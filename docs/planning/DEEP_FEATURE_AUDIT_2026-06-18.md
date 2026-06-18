# Deep Feature Audit - Yaver Box / Agent Work

Date: 2026-06-18
Scope: dirty-tree developed features around Yaver Box, Wi-Fi/AP mesh, meeting
fabric, remote sessions, automation, robotics helpers, gateway intent, and
mobile/web surfaces.

## Executive Summary

The developed feature set is directionally strong but not integration-ready.
Several pieces are real, code-backed surfaces; several are only scaffolds or
stubs; and the current `desktop/agent` package does not build because unrelated
new files conflict with the existing package structure.

The best-developed slice is the Yaver Console / Yaver Box Wi-Fi backend:
hotspot, AP+STA, and 802.11s/BATMAN mesh managers exist, ops verbs are
registered, and HTTP routes are now mounted. The weakest slices are the
development project manager and the expanded meeting fabric: both contain
duplicate or unmounted code and compile blockers.

## Build State

Current hard blockers:

- `desktop/agent/project_manager.go` is `package agent` inside a directory that
  otherwise uses `package main`, so `go test ./...` fails before most package
  checks can run.
- `desktop/agent/project_manager.go` also duplicates types/functions inside the
  same file and mixes `Project` with `DevelopmentProject`.
- `desktop/agent/automation/surface_web.go` has a syntax error:
  `if err := json.Unmarshal(...); err != {`.
- `meeting_fabric.go` contains later-stage code that references undefined
  identifiers such as `saveMeetingLocked` and `Room`.
- `mobile/app/(tabs)/console.tsx` renders `<WiFiTabScreen />`, but that
  component is not defined in the file.

Verification performed:

- `go test ./gripper ./motor ./pneumatic` passes.
- `go test ./automation` fails on the syntax error above.
- `go test ./...` in `desktop/agent` fails on the package conflict and
  automation syntax error.
- `go test ./...` from repo root is invalid because Go modules are nested.

## Feature Inventory

### 1. Yaver Box Wi-Fi / RF Substrate

Status: partially buildable, most concrete backend slice.

Implemented:

- `WiFiHotspotManager` supports Linux hotspot lifecycle using `hostapd`,
  `dnsmasq`, IP assignment, NAT, AP+STA virtual AP interface setup, status,
  and hardware capability detection.
- `WiFiMeshManager` supports 802.11s mesh setup through `iw` and
  `wpa_supplicant`, plus BATMAN-adv via `batctl`.
- Ops verbs exist for `wifi_*` and `wifi_mesh_*`.
- HTTP routes exist for:
  - `/console/wifi/capabilities`
  - `/console/wifi/status`
  - `/console/wifi/start`
  - `/console/wifi/stop`
  - `/console/wifi-mesh/capabilities`
  - `/console/wifi-mesh/status`
  - `/console/wifi-mesh/start`
  - `/console/wifi-mesh/stop`

Fixed during this audit:

- Mounted the `/console/wifi/*` and `/console/wifi-mesh/*` handlers in
  `HTTPServer.Start`.
- Added missing `HTTPServer` manager fields for hotspot and mesh managers.
- Aligned Wi-Fi ops with the existing `wifiManager()` accessor.
- Added basic hotspot client list/kick/ban/config persistence helpers needed by
  the registered ops.

Remaining risks:

- Client ban state is in-memory only and does not feed back into hostapd deny
  lists, so it is not a real persistent MAC ACL yet.
- Start/stop requires root and Linux; mobile/macOS UI must show this clearly.
- AP+STA reliability depends on `iw phy` interface combinations and needs
  hardware validation on the actual Yaver Box radios.
- The mobile Wi-Fi tab is not implemented even though the tab is listed.

### 2. Yaver Console Mobile Surface

Status: useful existing console sections, broken Wi-Fi section.

Implemented:

- Native React Native console surfaces for metrics, machines, containers,
  catalog, Mailpit, and S3 call existing `/console/*` routes.
- No WebView dependency for these console screens.

Broken/incomplete:

- `WiFiTabScreen` is referenced but missing.
- `WiFiTab` type is declared but unused.
- No mobile controls exist for `/console/wifi/*` or `/console/wifi-mesh/*`.

Recommended next step:

- Add a small Wi-Fi tab with capability/status refresh first. Gate `start/stop`
  behind explicit confirmation because those operations mutate network state.

### 3. Meeting Fabric

Status: split between a small usable core and a large unintegrated expansion.

Usable core:

- Room model, provider/adapter capability map, room create/list, participant
  token minting, simple `/call/:slug` page rendering, and LiveKit token
  generation are implemented.
- Tests cover default Yaver-native room creation, duplicate slug rejection,
  provider capability coverage, scoped participant token minting, and LiveKit
  token generation.

Integration gap:

- `httpserver.go` only mounts the older Calendly-style `/meetings`, `/meet/`,
  and `/bookings` routes. The new `handleMeetingRooms`, `handleCallPage`, and
  related `/call/*` fabric handlers are not mounted.
- The bottom of `meeting_fabric.go` contains a commented "add routes" block
  instead of actual route registration.

Compile blockers in expanded section:

- Calls to `saveMeetingLocked()` should likely be `saveMeetingFabricLocked()`.
- `handleMeetingParticipantKick` uses `Room` instead of the local `room`.
- Several handlers assign `idx` and do not use it.

Product risk:

- Provider adapters for Zoom, Google Meet, Teams, PSTN, lobby, invites, kick,
  and ban are mostly metadata stubs. They should be labeled as stubs in UI/API
  responses until real provider calls and media paths exist.

### 4. Remote Browser Session / WebRTC Surface

Status: plausible backend and web UI, needs integration tests.

Implemented:

- Ops verbs:
  - `remote_session_start`
  - `remote_session_status`
  - `remote_session_stop`
- Start validates HTTP(S) URL, starts screen streaming through `ghostStream`,
  opens a persistent Chrome profile, and navigates it.
- Web dashboard component `RemoteSessionView.tsx` can select a device, start a
  session, open WebRTC, and send `/rd/input` events.

Risks:

- `remoteSession` is a package-global singleton, so one process supports one
  managed remote session at a time.
- Start currently requires `c.Server.browserMgr != nil`; if browser manager is
  normally lazy-created elsewhere, this op may report unavailable instead of
  initializing it.
- Audio routing is still thin; the web component asks for `audio_devices`, but
  end-to-end meeting audio needs explicit validation.

Recommended next step:

- Add a focused test around URL normalization and start/status/stop behavior
  using a fake browser manager or split out the state machine.

### 5. Gateway Intent Model Classifier

Status: reasonable backend design, one mobile UI bug.

Implemented:

- `gateway_intent_model.go` adds a model-backed classifier with:
  - keyword fallback,
  - strict JSON parsing,
  - catalog validation,
  - cheap-first tiered routing,
  - no connector/capability invention.

Mobile issue:

- `mobile/app/gateway-intent.tsx` checks `result.actId` and moves to confirm
  state, but never calls `setActId(result.actId)`. Confirming an action will
  no-op because `actId` remains `null`.

Recommended next step:

- Set `actId` after `gatewayIntent`, and add a small test or manual flow for a
  write intent returning an action preview.

### 6. Automation Surface

Status: promising abstraction, currently not buildable.

Implemented:

- `automation/surface.go` defines a comprehensive `AutomationSurface`
  abstraction for web/mobile targets, state capture, diagnostics, metrics, and
  pooling.
- `automation/surface_web.go` starts a chromedp-backed web implementation.

Blocker:

- Syntax error in `parseSnapshotJSON` prevents the automation package from
  building.

Quality concerns:

- Chrome launches with `no-sandbox`; acceptable for local testing only, but a
  production automation surface needs a clearer trust boundary.
- Several methods are still simplified placeholders and do not yet connect to
  the broader Yaver ops registry.

### 7. Robotics Helper Packages

Status: compiles, but simulated only.

Implemented:

- `gripper`, `motor`, and `pneumatic` packages define clean interfaces and
  in-memory/simulated controllers for:
  - two-finger, suction, and magnetic grippers,
  - PWM, stepper, brushed DC, and BLDC-style motors,
  - solenoid valves and cylinders.

Verification:

- `go test ./gripper ./motor ./pneumatic` passes.

Limit:

- No actual GPIO, serial, Modbus, PWM, CAN, or Yaver Box hardware backend is
  wired. These are domain models and simulation placeholders, not operational
  machine-control code.

Recommended next step:

- Add a backend interface per package, then one concrete safe backend that
  talks to existing robot/G-code/Modbus ops with dry-run and limit gates.

### 8. Development Project Manager

Status: not salvageable as-is without cleanup.

Problems:

- Wrong package name for the directory.
- Duplicate type and function definitions in the same file.
- References `s.projectManager`, but `HTTPServer` has no such field in the
  current main package.
- Hardcodes local workspace paths and repository names.
- Deployment/hot-reload methods mostly simulate work rather than invoking real
  Yaver flows.

Recommendation:

- Do not wire this into the agent yet. Replace it with a smaller adapter over
  existing project/build/session-transfer APIs, or move it into its own package
  with tests and no hardcoded personal paths.

## Priority Fix Order

1. Restore buildability:
   - remove or convert `project_manager.go` to `package main`;
   - delete duplicate definitions;
   - fix `automation/surface_web.go`;
   - fix `meeting_fabric.go` undefined identifiers.
2. Finish the Yaver Box Wi-Fi vertical:
   - implement mobile `WiFiTabScreen`;
   - make MAC bans effective through hostapd config/control;
   - add tests for HTTP route registration and ops payloads.
3. Decide meeting fabric boundary:
   - either mount only the small tested `/meeting-rooms` + `/call` core, or
     keep expanded lobby/provider code out until it compiles and is tested.
4. Fix gateway intent mobile confirmation:
   - set `actId` from `result.actId`;
   - verify dry-run to confirm flow.
5. Turn robotics helpers from simulations into gated hardware backends:
   - use dry-run defaults;
   - require explicit operator approval for motion/output writes.

## Bottom Line

The codebase has enough real pieces to pursue Yaver Box as a single hardware
facade: agent ops, console, Wi-Fi substrate, machine/robot anchors, and remote
session streaming all point in the same direction. The immediate problem is not
product direction; it is integration hygiene. Get the tree build-clean, ship one
end-to-end Wi-Fi/control vertical, and quarantine or trim the large speculative
files until they are wired and tested.
