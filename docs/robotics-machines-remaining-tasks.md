# Robotics & "Yaver for Machines" — Remaining Tasks / Session Dump

Generated 2026-06-06, branch `feat/yaver-robot-cell`. Handoff for the robotics +
machine-wrapping work designed this session. **Everything below is DESIGN-ONLY — nothing
in this dump is built yet.** Build status of the *existing* substrate is noted where it
gates a task.

## Session output (3 design docs produced this session)
- `docs/yaver-fairino-cobot-cell-design.md` — AI-driven Fairino 6-DOF cobot + Ender cell.
- `docs/yaver-for-machines-design.md` — universal `machine.Driver` wrapper, wire-harness-first.
- `docs/yaver-talos-open-core-strategy.md` — open-core split (added in-session; open CODE ≠
  public DATA; moat = domain configs + config-authoring tools + production data in Talos).

## Already-built substrate these tasks reuse (do NOT rebuild)
- `desktop/agent/robot/` — `Backend` iface, Controller move-and-verify, vision gate, encoder
  cross-check, e-stop latch, screw torque loop, companion MCU, GstCamera, 9 `robot_*` ops
  verbs, `cmd/robotd/`. (BUILT, live-proven on ananas.)
- `desktop/agent/machine/` — Modbus TCP/RTU, passive sniff + `classify()`, `ops_machine.go`
  verbs incl. `machine_understand` LLM labeler + `machine_sync`. (BUILT; observe + raw R/W only.)
- `desktop/agent/netcapture/` — wire-debug decoders + `netcapture_analyze`. (BUILT.)
- ghost HMI-vision engine (`ghost/`, `ghost_vision.go`, `ops_ghost*.go`). (BUILT.)
- Talos bridge — `machineEdgeDevices/Commands/Telemetry/machineManuals/machineRecipes/
  machineJobContexts` + `/machine-edge/*`; robotics bridge `/robotics/*`. (BUILT.)
- Mobile robot UI + `robotClient.ts`; capability heartbeat (`console_machines.go`).

---

## TRACK A — Fairino cobot cell (`docs/yaver-fairino-cobot-cell-design.md`)

**A0 — sim, zero hardware**
- [ ] `PoseBackend`/`ServoBackend` interface extension in `robot/backend.go` (+ `Pose`,
      `Joints`, `Wrench`, `Fault`, `Axis6`, `ServoMode` in `robot/types.go`; additive
      optional fields on `MoveResponse`).
- [ ] `FairinoBackend` Route A — Python `fairino_ui` sidecar (twin of ender_ui:8330) +
      Go HTTP backend; env `YAVER_FAIRINO_BRIDGE` / `YAVER_FAIRINO_IP`.
- [ ] Drive against FAIRINO WebApp/SimMachine virtual controller (`192.168.58.2`); RoboDK on
      Mac for IK prototyping.
- [ ] Verbs `robot_pose` / `robot_move_l` / `robot_move_j`; drive sim from existing mobile UI.

**A1 — first real motion, verify-gated (bench, no contact)**
- [ ] ⚠️ Confirm XML-RPC command port (expected 20003) + 8083 stream rate via protocol PDFs /
      `ss` / netcapture.
- [ ] Hand-eye calibration (eye-in-hand). Pose cross-check replaces encoder cross-check.
- [ ] Free-space MoveL through existing verify gate; e-stop + `clear_faults` recovery proven.

**A2 — screwdriving**
- [ ] `robot_screw` — tool-DO driver fire, torque-spec termination, `GetToolDI` complete,
      VQA "head flush?" cross-check (reuse `screw.go` shape).

**A3 — insertion (the hard one)**
- [ ] Wrist F/T + admittance/spiral search; vision-to-hole + force-to-seat fusion.
- [ ] `robot_insert` / `robot_force_move`. Budget heavy iteration on F/T tuning.

**A4 — AI supervisor & autonomous jobs**
- [ ] `robot_run_job` (Behavior Tree over pole/step sequence) — also closes the doc-only gap
      from `robot-screwdriver-cell.md §3.4`.
- [ ] Per-step pre/postcondition VLM checks (arXiv:2503.15202 pattern); `robot_recover`
      structured recovery wired to netcapture diagnosis + Fairino fault codes.

**A5 — Talos + fleet + phone-edge + GPU pool**
- [ ] Register cell in Talos robotics bridge; `robotCameraFrames` audit table; order→recipe→job.
- [ ] Multi-cell fleet picker; phone-as-edge camera; GPU rental pool behind `robot-vision`.

**A6 — research lane (parallel, non-blocking)**
- [ ] GR00T N1.5-3B / π0 on GPU box for coarse pick-place ONLY; never the precision/contact loop.

**Vision-pose service (shared A3+):** `robot-vision` on GPU box — `/vision/verify`
(Qwen3-VL-8B FP8 or Gemini Flash), `/vision/pose` (FoundationPose), `/vision/grasp`
(Contact-GraspNet; avoid AnyGrasp commercial license). Stateless/Salad-safe.

---

## TRACK B — Yaver for Machines (`docs/yaver-for-machines-design.md`)

**B0 — the seam (no new hardware)**
- [ ] `machine.Driver` interface + `CapSet` in `machine/driver.go` (the generalization of
      `robot.Backend`).
- [ ] Wrap existing Modbus engine as `modbusDriver`.
- [ ] `robotDriverAdapter` — surface a `robot.Backend` read-only on the machine wall.
- [ ] Verbs `machine_list` / `machine_browse` / `machine_status`; prove a unified grid
      (robot + a Modbus-TCP test slave).

**B1 — WATCH the commodity tier (highest ROI, zero writes — sellable alone)**
- [ ] `ioDriver` (CT clamp + signal-tower/cycle/part-count taps → run/idle/count).
- [ ] `visionDriver` (camera/old-phone-on-HMI → VLM → state/count/program/alarm). THE MOAT.
- [ ] OEE "watch" wall (web + mobile): R/Y/G tiles, OEE, downtime + reason codes, counts,
      AI-narrated state. Target a real YH-8030H / CST18D.
- [ ] `machine_watch` (subscribe → SSE/UNS), `machine_vision_read`.

**B2 — read config + supervised-sniff discovery**
- [ ] Wire supervised-sniff loop (operator job-tag → LLM label → verified Machine Operating
      Manual in `machineManuals`). Live param view (length/strip/qty/speed/alarms).

**B3 — gated control**
- [ ] `machine_recall` (program slot) then `machine_write` (bounds-checked register) with
      read-back + vision verify + approval gate. Order→recipe→job for YH-8030H/CST18D.

**B4 — tier-1 OPC-UA / OPC 40570**
- [ ] `opcuaDriver` (`gopcua` — pin version; client-only). OPC 40570 typed nodes + native subs.
- [ ] `fileDriver` — CSV/XML cutting-list ingest + DataMatrix barcode scan-to-setup.
- [ ] `machine_submit_job`; `machine_quality` (crimp-force pass/fail + Cpk + curve pointer);
      IPC/WHMA-A-620 Rev E traceability fields.
- [ ] New Talos tables `machineQuality` + `machineCurves` (storageId pointers, not bytes).

**B5 — UNS + fleet + QA vision**
- [ ] `mqttDriver` + Sparkplug B edge node (own the birth/death/seq state machine; gen Go
      bindings from `sparkplug_b.proto` — no blessed lib). ISA-95 UNS topics.
- [ ] `s7Driver` (snap7/cgo), `mtconnectDriver` (stdlib HTTP) where present.
- [ ] Fleet wall across cells/lines; vision crimp inspection (IPC-620); PdM from CT/vibration
      (MCSA/FFT).

**B6 — phone-edge + GPU vision pool** (shared with A5).

---

## TRACK C — open-core / packaging (`docs/yaver-talos-open-core-strategy.md`)
- [ ] Decide the open/closed line: open robotics+machine ENGINE (drivers, jog, teach-and-repeat,
      camera, SDK, verbs); keep DATA (taught programs, recipes, tuning, machine manuals) +
      config-authoring tools private (local-first or Talos). Confirm before any public push.

---

## Cross-cutting / blocking notes
- **Privacy (CLAUDE.md):** all production/work-derived data is P2P / on-device / org-secret
  HTTP; Convex gets summaries + storageId pointers only. Add every new sync field to
  `convex_privacy_test.go`'s forbidden set.
- **Safety invariant (both tracks):** reads default-on; writes GATED (cap grant + safeRanges
  bounds-check + read-back + verify + approval + sim-first + audit); machine PLC interlocks +
  hardwired e-stop NEVER bypassed; LLM is advisory only.
- **Talos MCP gap:** `talos_machine_*` / `talos_robot_*` tools designed, not built — clone the
  `talos_ghost_*`/robotics tool families.
- **Parallel sessions:** other threads touch `desktop/agent` untracked files — shelve, don't
  edit theirs; ship test/fixture alongside features; prefer new files over big edits.
- **RS-485 gotcha:** never open a serial port another process holds (DTR reset → EIO).

## ⚠️ First-contact verification (before timing-critical / write work)
- Fairino: XML-RPC port (20003?), 8083 push rate, ServoJ cadence (1–16ms), SDK↔fw pairing,
  ISO 10218/TS 15066 DoC, tool-flange IO map.
- Machines: OPC 40570 real per-machine support; WPCS/MIKO via Komax SDK only (NDA wire format);
  crimp-curve export availability; commodity model numbers (CST18D/YH-8030H/CC36) Modbus map via
  sniff; `gopcua` version pin; which registers are interlock-critical before enabling `CapWrite`.

## Suggested next action
Start **B0 + B1**: define `machine/driver.go` + `CapSet`, wrap Modbus as `modbusDriver` +
`robotDriverAdapter` (unified machine list), then the camera-on-HMI watch wall against one real
cut-strip machine — no writes, no risk, immediately demoable.
