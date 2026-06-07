# Yaver Robot Protocol (v0) — move-and-verify

> The contract the **mobile app / Talos / any Commander** speaks to drive the robot
> cell. The caller issues a motion; **Yaver executes it, looks through the camera,
> and returns a verdict** (did it actually move as expected?) plus the before/after
> frames and the encoder cross-check. The caller never touches the printer — it
> speaks this protocol and Yaver validates.
>
> Implemented by the `robot/` Go package (importable). Surfaced two ways, same shapes:
> 1. **Agent verbs** `robot_*` over the mesh — `POST /ops {verb,payload}` (+ MCP).
> 2. **Standalone `robotd`** HTTP server (`/robot/*`) for boxes not yet running the
>    full agent — identical request/response bodies.

---

## 0. The one rule that makes verdicts trustworthy

Marlin returns `ok` when a move is **queued**, not finished. So every motion in this
protocol is **`M400`-gated**: execute → `M400` (wait for moves to complete) → read a
**fresh `M114`** (never the bridge's cached status) → only THEN snapshot the "after"
frame. Without this the camera races the motion and the verdict flaps. (Learned live,
2026-06-06.)

---

## 1. Verbs / endpoints

| Agent verb | `robotd` route | Payload | Motion? |
|---|---|---|---|
| `robot_status` | `GET /robot/status` | — | no |
| `robot_home` | `POST /robot/home` | `{axes?, verify?, expectation?}` | G28 |
| `robot_jog` | `POST /robot/jog` | `{axis, dist, feed?, verify?, expectation?}` | relative |
| `robot_move` | `POST /robot/move` | `{x?, y?, z?, feed?, verify?, expectation?}` | absolute |
| `robot_tool` | `POST /robot/tool` | `{on}` | actuator |
| `robot_verify` | `POST /robot/verify` | `{expectation}` | no (camera only) |
| `robot_estop` | `POST /robot/estop` | — | latched stop |
| `robot_reset` | `POST /robot/reset` | — | clear e-stop |

`verify` (default `true`) selects how the camera judgment is produced:

- `"agent"` / `true` — Yaver calls its configured vision model (provider ladder below)
  and fills `verify{}` itself. Fully closed-loop on the edge.
- `"frames"` — Yaver returns the before/after frames and an **empty** `verify`, leaving
  the judgment to the caller's LLM (the mobile/Talos runner, or Claude Code). Use this
  when the edge box has no model (e.g. an old laptop / phone with no local Ollama).
- `false` — no camera; motion + encoder readback only.

Provider ladder (reused from `ghost_vision.go`): explicit payload `{baseUrl,apiKey,model}`
→ `GHOST_VISION_*` → `OPENAI_*` (the runner's configured provider) → local Ollama
`llama3.2-vision`. No new keys; on-prem-capable.

---

## 2. Request example (mobile taps “Z ▲ +10”)

```json
POST /ops   { "verb": "robot_jog",
  "payload": { "axis": "Z", "dist": 10, "feed": 600,
               "verify": "agent", "expectation": "carriage moved UP ~10mm" } }
```

## 3. Response — the move-and-verify result (what the app renders)

```json
{
  "ok": true,
  "action": { "kind": "jog", "axis": "Z", "dist": 10, "feed": 600 },
  "position": { "x": 110.0, "y": 110.0, "z": 35.0, "homed": true },   // fresh M114, post-M400
  "verify": {
    "mode": "agent",
    "moved": true, "confidence": 0.95, "obstruction": false,
    "expectation": "carriage moved UP ~10mm",
    "reason": "gantry crossbar is higher; carriage rose ~1cm",
    "observed": "before: gantry low; after: gantry raised"
  },
  "frames": { "before": "data:image/jpeg;base64,…", "after": "data:image/jpeg;base64,…" },
  "encoderCrossCheck": {
    "expectedDelta": { "z": 10 }, "observedDelta": { "z": 10 }, "agree": true
  },
  "tookMs": 4200
}
```

Decision the caller makes from this: `verify.moved && verify.confidence ≥ 0.8 &&
encoderCrossCheck.agree` → ✅ approve, proceed. `verify.obstruction` → 🛑 e-stop.
otherwise → ↻ retry (re-home / re-issue), then e-stop if still unconfirmed. That branch
is the closed loop.

`verify.mode:"frames"` returns the same body with `verify.moved/confidence` omitted and
both frames populated — the caller’s model fills the verdict.

Errors: `{ "ok": false, "code": "estopped|out_of_range|not_homed|backend|bad_payload|no_camera|no_vision", "error": "…" }`.

---

## 4. Two transports, one contract

```
 MOBILE / TALOS                         EDGE NODE (printer's box)
 ┌──────────────┐  direct mesh call     ┌───────────────────────────────┐
 │ tap Z ▲ +10  │ ───POST /ops────────► │ robot_jog verb                │
 │              │ ◄──move-verify JSON──  │  grab(before)                 │
 └──────────────┘                        │  backend.Jog → M400           │
        │                                │  grab(after)                  │
        │ durable/audited path           │  M114 (fresh) + verify(LLM)   │
        ▼                                │  → MoveResponse               │
 ┌──────────────┐  enqueue command       └───────────────────────────────┘
 │ Talos Convex │ ──machineEdgeCommands─►  edge drains queue, runs the SAME
 │              │ ◄─telemetry+frame─────   verb, posts verdict + frame back
 └──────────────┘                         (/machine-edge/telemetry,/camera-frame)
```

- **Direct mesh** (LAN/relay): low-latency, for interactive jogging from the app.
- **Talos command queue**: durable + audited + multi-user; the edge drains
  `machineEdgeCommands{type:"robot_jog",args}` and reports the verdict to
  `machineTelemetry` + the frame to `/machine-edge/camera-frame`. Same verb underneath.

The mobile app picks direct when the device is reachable on the mesh, falls back to the
Talos queue when it isn't — identical request/response either way.

---

## 5. Backend abstraction

`robot.Backend` has two implementations behind the identical verb logic:
- `BridgeBackend` — HTTP to the existing Python `ender_ui` server (`127.0.0.1:8330`):
  `/api/{jog,move,home,screw,gcode,estop,status}`. Ships today; reuses the proven cell.
- `SerialBackend` (later) — native Go Marlin over `/dev/ttyUSB0` (the `robot` driver in
  `docs/robot-screwdriver-cell.md` §3). Swap-in; the mobile protocol does not change.

`robot.Camera` likewise: `GstCamera` (shells `gst-launch-1.0` → JPEG) now; native V4L2 /
Android Camera2 later. The protocol is stable across all of these.

---

## 6. Safety (enforced edge-side, never trust the caller)

Soft-limit clamp/refuse on every target; relative jog requires `homed`; e-stop is latched
and always reachable; tool forced off on any X/Y move, disconnect, e-stop; `M400` watchdog
→ e-stop on no-`ok`. The caller’s “go up 10” is bounded by the edge envelope regardless of
what the app sends.
