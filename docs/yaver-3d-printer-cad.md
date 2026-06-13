# Yaver 3D-Printer Cell + Remote CAD — Bambu P1S

Status: **built 2026-06-12** (Go agent + ops verbs + mobile + web). Live-verified
to the point of credential-free discovery; full control/camera need the printer's
LAN access code (entered once by the owner). Design + topology of record.

This is the third device cell after the Cartesian robot (`ops_robot.go`) and the
multi-DOF arm (`ops_armcell.go`): one parametric driver layer → `registerOpsVerb`
→ mesh `/ops` → mobile client + web view, reusing the shared `robot.Camera` eye.

---

## 1. Topology (verified)

`yaver devices` + an SSDP/port sweep run from `primary` over `yaver ssh`:

| Box | Role | LAN | Sees printer? |
|---|---|---|---|
| `example-host` (`00000000…`) | the box you said is "connected to" the printer | `192.0.2.30` | yes (same /24) |
| `primary` (`11111111…`) | **primary**, online, runners `claude/codex/opencode` | `192.0.2.45` | **yes — confirmed** |
| Bambu **P1S** | the printer | **`192.0.2.11`** | — |

**Printer identity (from its own SSDP NOTIFY, no credentials):**

```
USN (serial):  01P00X000000000
DevModel:      C12   → P1S
DevName:       3DP-01P-978
Firmware:      01.09.01.00
Signal:        -41 dBm   (strong)
Connect/Bind:  cloud / occupied
```

Open ports from `primary` → `192.0.2.11`: **8883** (MQTT/TLS control+telemetry),
**990** (implicit FTPS upload), **6000** (chamber camera). That triple is the P1S
fingerprint and is exactly what the driver speaks.

> The bed currently holds **pre-printed parts** — so nothing in this work ever
> issues a print/move. `printer_print` is confirm-gated *and* refuses to start
> over a busy machine; live verification was limited to read-only discovery.

---

## 2. What was built

### Go agent — `desktop/agent/printer/` (new package)

| File | Role |
|---|---|
| `types.go` | `Backend` interface + normalized `Status`/`Info`/`Discovered`/`Config`. Cross-driver vocabulary so OctoPrint/Klipper/PrusaLink slot in later without touching verbs/UI. |
| `discovery.go` | SSDP listener (UDP 2021/1990) → `[]Discovered`. Pure `ParseSSDP` is unit-tested against the real P1S NOTIFY. Model-code map (`C12→P1S`, …). |
| `mqtt.go` | A **dependency-free MQTT 3.1.1 client** (CONNECT/SUBSCRIBE/PUBLISH/PING/DISCONNECT over TLS). No paho — same no-deps posture as the rest of the repo. |
| `backend_bambu.go` | The Bambu driver: `pushall`+report parsing → `Status`; `pause/resume/stop`; `gcode_line` (set-temp/raw); `ledctrl` (chamber light); FTPS upload via `curl --ftp-ssl`; `project_file` start-print. Reports are partial deltas, so a `merge()` keeps the last non-empty field. |
| `camera_bambu.go` | The port-6000 chamber stream: 80-byte auth packet (`bblp` + access code) → length-prefixed JPEG frames. Implements `robot.Camera` (`Grab`/`Available`/`StreamMJPEG`) so it drops straight into the shared-eye path. |
| `printer_test.go` | 13 deterministic tests (SSDP parse, MQTT wire roundtrip, report→status, partial-merge, auth-packet shape, frame parse, config). **All green.** |

### Go agent — verbs

`ops_printer.go`: `printer_discover` · `printer_drivers` · `printer_config_get`
(access code **redacted**) · `printer_config_set` (vault project `printer`/`config`,
secret never leaves the box) · `printer_info` · `printer_status` · `printer_snapshot`
(data: URL, like `robot_snapshot`) · `printer_light` · `printer_pause` ·
`printer_resume` · `printer_stop` · `printer_set_temp` · `printer_gcode`
(confirm-gated) · `printer_upload` · `printer_print` (**confirm:true + busy-interlock**).

`ops_cad.go` — remote OpenSCAD: `cad_tools` · `cad_render` (scad → STL + PNG
preview, PNG inline as data: URL) · `cad_preview` (fast PNG) · `cad_slice`
(STL/3MF → gcode via OrcaSlicer/PrusaSlicer CLI) · `cad_get` (artifact → base64
over the mesh, 24 MB cap, sandboxed to `~/.yaver/cad/`). Pairs with the existing
`robot_jig_generate` (which already emits parametric OpenSCAD).

### Mobile — `mobile/`

- `src/lib/printerClient.ts` — mesh client (LAN-first, relay fallback), mirrors `armClient`.
- `app/printer.tsx` — the **3D Printer section**: host-box picker, discover→link
  (access-code field), live status (temps/progress/stage), chamber camera (snapshot
  poll, iOS-safe), controls, and the CAD panel.
- `src/components/StlViewer.tsx` — a **WebView + three.js** viewer that renders the
  rendered STL in 3D on the phone (rotate/zoom), fed the STL bytes via `cad_get`.
- Entry added to the **More** menu next to "Robot Cell" → `/printer`.

### Web — `web/`

- `components/dashboard/PrinterCellView.tsx` — relay-only dashboard cell (status,
  camera, controls, CAD render+preview, slice→print).
- `app/dashboard/printer/page.tsx` — standalone route `/dashboard/printer`.

All four surfaces typecheck/​build clean (`go build ./...`, `go test ./printer/`,
`tsc --noEmit` on the new mobile + web files).

---

## 3. The closed loop ("remote dev makes a 3D drawing, I see it on my phone")

```
phone/web ──cad_render(scad)──▶ dev box: openscad → STL + PNG
        ◀── PNG preview (data: URL) + STL via cad_get ──┘
phone renders STL in three.js (StlViewer)  ← you SEE the model, rotate it
        ──cad_slice(STL)──▶ OrcaSlicer/PrusaSlicer → gcode.3mf
        ──printer_upload──▶ FTPS → printer storage
        ──printer_print(confirm)──▶ MQTT project_file   (gated; not used yet)
```

The AI coding agent on a remote box writes/edits the OpenSCAD; the same box
renders + slices; the phone is the viewport + control surface.

---

## 4. Seeded for kivanc's account

- **Non-secret printer config written to `primary:~/.yaver/printer-config.json`**
  (driver/addr/serial/model/ports — **no access code**). The agent reads this as a
  fallback, so once `primary` runs the new binary the printer is pre-linked except
  for the secret. The Vostro can be seeded with the identical one-liner once it's
  reachable (it currently rejects SSH from this Mac — no key/bootstrap).
- Nothing sensitive went to Convex (privacy contract): no IP, no access code. The
  access code lives only in the box vault after activation.

### Activation (the two owner-only steps)

1. **Deploy the new agent** to the printer host (`primary` and/or the Vostro):
   `cd desktop/agent && go build` → run it as the agent. (Not auto-deployed — the
   repo rule is no deploy without explicit permission.)
2. **Enter the LAN access code** once, from the app's 3D-Printer screen (or
   `printer_config_set {accessCode}`). It's on the printer: *Settings → WLAN →
   Access Code* (LAN mode must be on). That unlocks status, camera, and control.

For OpenSCAD CAD, the host box needs `openscad` (`apt install openscad`) and,
for slicing, OrcaSlicer/PrusaSlicer. `cad_tools` reports what's present; `primary`
currently has neither, so install before using `cad_render`/`cad_slice`.

---

## 5. Safety + privacy

- `printer_print` requires `confirm:true` **and** a non-busy machine; `printer_gcode`
  requires `confirm:true`. `printer_stop` is always safe.
- Access code is redacted from `printer_config_get`, stored vault-encrypted, never
  synced to Convex, never logged.
- Camera/control are LAN-scoped; over the relay they ride the same authenticated
  mesh as every other ops verb.

## 6. Known gaps / next

- **Live status/camera unverified** pending the access code (protocol matches
  Bambu's published LAN spec + community implementations; encode/decode unit-tested).
- `printer_print` `url`/plate path is best-effort for P1S local-storage prints —
  validate against one real (intended) print before relying on it.
- No long-lived telemetry subscription yet (status is a per-call `pushall`); fine
  for 4 s polling, but a persistent stream would make the live UI snappier.
- Web 3D viewer shows the PNG; the interactive three.js STL viewer is mobile-only
  so far (web can add the same `cad_get`→three path).
- X1-series camera is RTSP (not the 6000 JPEG stream) — add an RTSP path when an
  X1 appears.
