# Yaver "Single Kumanda" — Universal AV / Home Control (design)

> **Status:** design-only (2026-06-17). No implementation yet.
> **Source of truth is code, not this file.** Every "LIVE / DROPPED" claim
> below was grepped on 2026-06-17; other threads bump constants in parallel.
> Re-verify against the tree before acting (CLAUDE.md rule).

This is the reference for turning Yaver into a **single universal remote** that
operates the user's whole AV/home setup — and, separately, a diagnostic layer
that helps **fix** real appliances. The two are different products; keep them
apart.

## 0. Two cases — do not blur them

| | **FIX** (the doctor) | **SWISS-ARMY** (this doc) |
|---|---|---|
| Goal | *why is my device broken → part + test* | *operate everything from one brain* |
| Verb | diagnose / repair | control / orchestrate |
| Touches | buses, boards, sensors, RF telemetry | IR / CEC / network control planes |
| Risk | **mains voltage = real danger**; agent triages + hands off, never coaches live-mains work for the unqualified | benign (IR in your own room) |
| Build order | later (safety-gated) | **now** |

The FIX case has its own analysis (sensors, low-voltage buses, board-level
visual triage, sub-GHz `rtl_433`, the mains-safety ceiling). This document is
the **SWISS-ARMY / single-kumanda** product.

## 1. The reframe: a universal remote is a ROUTER, not a blaster

The user's three devices live on three different control planes; a fourth
(AC) on its own. A "single kumanda" that is just an IR blaster cannot drive the
Mi Box or Apple TV well. The real thing is **one logical remote whose every key
routes to the right transport per device**, plus an **activity engine**, plus a
**closed-loop verify**. Each device is modelled as a **gateway connector** so
the existing intent router + voice loop drives it for free.

## 2. Architecture: hub-and-spokes

```
  SPOKES (thin terminals, differ by modality + vantage)        HUB (always-on brain)
  ┌─────────┐ ┌────────┐ ┌──────────────┐ ┌─────────┐      ┌────────────────────────────┐
  │  phone  │ │ watch  │ │  TV (10-ft)  │ │   car   │ ───▶ │ intent router (LIVE)        │──pyatv──▶ Apple TV
  │ cockpit │ │ glance │ │ dashboard +  │ │ voice + │ mesh │ connector registry (LIVE)   │──ADB────▶ Mi Box
  │ + learn │ │ + voice│ │ verify screen│ │ geo     │      │ activity engine (NEW)       │──IR*────▶ Satellite / AC
  └─────────┘ └────────┘ └──────────────┘ └─────────┘      │ capture / vision (LIVE)     │   *external learner or
                                                            │ voice stack STT/TTS (LIVE)  │    Xiaomi-IR phone + DB
                                                            └────────────────────────────┘
```

**The hub must be always-on, in-room, and able to host the agent + IR sidecar +
capture.** Of the user's surfaces, exactly one fits:

| Candidate | Hosts the agent? | Role |
|---|---|---|
| **Mi Box / Android TV box** | yes (Android → `libyaver.so` + proot) | **the natural home hub** (also a controlled device) |
| Raspberry Pi / NUC by the TV | yes (full agent + IR + capture) | dedicated hub |
| **Apple TV** | **no** — closed tvOS, no daemon/python/IR | **controlled, never the hub** |
| Phone / Watch / Car | no (roaming, sleeps) | terminals only |

## 3. Phone IR reality → "DB-first, user-configurable"

- **iPhone: never has IR.** Cockpit only.
- **Xiaomi / Redmi / Poco (+ some Samsung/OnePlus): IR *blaster* = transmit-only.**
  Can *blast* DB codes natively (`ConsumerIrManager`), **cannot learn**.
- **Every phone IR is TX-only** → learning an unknown code needs an external
  **receiver** (Broadlink RM4 / Flipper / ESP+RX). No phone can sniff.

Design = **built-in IR code DB as default** (seed from SmartIR / Flipper-IRDB /
LIRC) → user picks brand/model → optional **learn-your-own** (needs a learner).
All stored in the **vault** (local, never Convex).

| | code source | emitter |
|---|---|---|
| **Blast** (known) | built-in DB | Xiaomi-IR phone native, **or** external blaster via hub |
| **Learn** (unknown) | live capture | external RX only (no phone) |

A **Xiaomi-with-IR + bundled DB = a working kumanda with zero extra hardware**
(de-risks the satellite milestone).

**AC is the exception to "hardcode codes":** an AC remote sends the *entire
state* per press, not one button. AC DB entries must be **protocol-encoder**
shaped (SmartIR climate JSON / IRremoteESP8266), not raw per-button captures.
TV/satellite = discrete codes; AC = encoder.

## 4. The control-plane routing table (logical key → live verb)

| Logical key | Apple TV `appletv_*` (LIVE) | Mi Box `droid_input` keyevent (LIVE) | Satellite `ir_blast` (NEW) |
|---|---|---|---|
| up/down/left/right/ok | `appletv_remote_key` | 19/20/21/22/23 | learned/DB IR |
| back / home / menu | `appletv_remote_key` | 4 / 3 / 82 | IR |
| play / pause | `appletv_transport` | 85 | IR |
| power | `appletv_power` | 26 | IR |
| launch app | `appletv_launch_app` | `droid_launch <pkg>` | n/a |
| ch +/− , digits | n/a | (app) | **IR** (the satellite's real value) |
| vol +/− , input | TV/AVR via CEC/IR | TV/AVR | TV/AVR |

## 5. Device connectors

Each device = a connector manifest (the registry already exists) with **read**
and **act** capabilities mapped to verbs above:

- `apple_tv` — acts: keys/transport/power/launch; reads: `now_playing`.
- `mibox` — acts: keyevents/launch/power; reads: current app, `droid_frame`.
- `satellite_ir` — acts: per-key `ir_blast`; reads: (none — IR is one-way) →
  rely on **capture verify** instead.
- `ac` (later) — acts: `set_state{mode,temp,fan,swing}`. **Backend ladder, best
  available first:** (1) **WiFi-local** — Tuya-local (tinytuya), Gree local
  UDP/AES, Midea local — most common on cheap modern WiFi ACs, no cloud, fast;
  (2) **WiFi-cloud** — Daikin Onecta, LG ThinQ, Samsung SmartThings, MELCloud;
  (3) **IR** stateful encoder for dumb ACs; (4) **wired bus** (CN105/S21/P1P2)
  for heat pumps via `netcapture serial`. If the AC has WiFi, **skip IR
  entirely** — a network connector is simpler and bidirectional (real state +
  verify for free). Reads: live state on backends 1/2/4; none on IR.

The **tiered intent router** (keyword→model, NL→read/act/code, dry-run+confirm)
then drives all of them: *"pause"* → `apple_tv/transport`; *"channel 42"* →
`satellite/digits`, with the built-in confirm gate for risky acts.

## 6. The IR subsystem (hardware-agnostic)

Mirror the **proven pyatv sidecar pattern** (`appletv.go`): embed a python
sidecar, supervise it, store codes in the vault. Backend pluggable —
**Broadlink** (`python-broadlink`) default; Flipper / ESPHome swappable.

Verbs: `ir_scan`, `ir_learn` (phone prompts "press the button now" → sidecar
captures → vault), `ir_blast`, `ir_list`. Seed the DB from SmartIR /
Flipper-IRDB so a device is identified from 1–2 presses instead of learning 45
buttons.

## 7. The activity engine + closed-loop verify (the one genuinely new subsystem)

A declarative, **state-aware** multi-device sequence — Harmony "activities" but
self-correcting. Stored locally (`collection_store` / vault), exposed as ops
verbs `activity_create | list | run | delete`, runnable by voice or a phone/TV
tile.

**Activity shape (spec, illustrative):**
```
{
  "name": "Watch Satellite",
  "vars": { "channel": null },
  "steps": [
    { "device": "tv",        "act": "power_on",     "verify": "capture.on",        "on_error": "retry:2" },
    { "device": "tv",        "act": "input_hdmi2",  "verify": "capture.input==hdmi2","on_error": "fallback:cec" },
    { "device": "satellite", "act": "power",        "verify": "capture.not_black",  "on_error": "continue" },
    { "device": "satellite", "act": "tune",         "params": { "ch": "{channel}" }, "verify": "capture.ocr_channel=={channel}" }
  ]
}
```

**Executor:** walks steps sequentially, substitutes vars, runs the act, then
runs `verify`. `on_error ∈ abort | continue | retry:N | fallback:<transport>`.
Streams progress over the existing `/streams/<id>` SSE so the phone/TV shows
"step 2/4: switching input…". Returns `{status, step_results[], errors[]}`.

**Closed-loop verify is the differentiator over Harmony** (which was fire-and-
pray): after each step, read the **live `/capture` frame** (already wired) —
did the TV land on HDMI2? is the box on (not black)? what channel does OCR see?
If not, **re-send or fall back IR→CEC**. State-aware, retrying, honest.

- For network devices (Apple TV / Mi Box) verify is even cheaper: they report
  real state over the wire — no guessing.
- For IR-only devices (satellite/AC, which can't talk back), capture/vision is
  the *only* feedback channel — so verify is mandatory, not optional.

Examples: *Watch Apple TV*, *Watch Satellite*, *Cool the room* (AC encoder),
*Movie night* (TV + AVR + Apple TV + lights), *Good night* (all-off).

## 8. Voice + chat-AI integration (reuse, no new speech code)

The voice stack is **LIVE** (24 `voice_*.go`: STT whisper-local + Deepgram /
OpenAI / AssemblyAI; TTS Cartesia / Deepgram / ElevenLabs / OpenAI;
`/voice/stream` WS; `DispatchVoiceTranscript`; `voice_agent`). Loop:

```
mic → /voice/stream (STT) → gateway_intent (route) → connector act OR activity
    → confirm-gate (LIVE) → TTS readback (summarizeForReadback / summarizeForWatch)
```

*"switch to satellite, channel 42"* → router picks the activity + `digits` act →
executes → OCRs the channel off the capture feed → speaks *"Satellite, channel 42."*

## 9. Multi-surface model (one brain, four renderings)

Surfaces are **thin terminals** differing by **modality** and **vantage**; the
same `voice → intent → act → confirm → readback` loop just renders per surface.

| Surface | Trigger | Confirm | Readback | Emits IR? | Best at |
|---|---|---|---|---|---|
| **TV** (Android TV box) | remote mic / on-screen tile | on-screen card | on screen + capture | via hub/CEC | hub + 10-ft dashboard + self-verify screen |
| **Watch** | wrist voice / button | haptic + voice | spoken + haptic | no (dispatch only) | eyes-free, on-wrist-anywhere, one-tap activity |
| **Car** | voice (IoT door) | spoken | spoken | no (dispatch only) | pre-arrival conditioning, departure check, geofence |
| **Phone** | touch / voice | dialog | screen | Xiaomi-native or hub | full cockpit + the only **learner** surface |

**Vantage-aware activities:** car arriving → "coming home" (AC/heater on);
watch in-bed → "good night" (all off); TV → "watch X"; phone → manual + author.

**Honest constraints:** Apple TV is controlled, not a hub; watch/car never emit
(dispatch to the hub, which emits → need LAN/relay reachability); car is
cellular-intermittent → async readback is mandatory (and already the model);
tvOS needs the react-native-tvos fork ADR → **Android TV is the pragmatic TV
surface**. Thin-by-mandate (never-block / never-render-code / confirm-gate
writes) is inherited for free — "turn off all AC" gets the same gate as "deploy
to prod".

## 10. Verified audit (2026-06-17 — re-check before building)

**LIVE — build ON these (as OPS VERBS via `registerOpsVerb`/`dispatchOps`):**
`appletv_*` (pyatv sidecar embedded, vault creds) · `droid_frame/input/launch`
(Mi Box, ADB `input keyevent`, network ADB via `<ip>:5555`) · `capture_*` +
`/capture/frame.jpg` (HDCP-black detect) · `netcapture` serial (wired AC bus
CN105/S21) · gateway connector registry + tiered intent router
(`gateway_intent.go` + `gateway_intent_mcp.go` + `gateway_intent_model.go`) ·
voice stack (above) · vault · `collection_store` (local-first) · routines ·
blackbox phone↔agent command bus.

**DROPPED 2026-04-28 (`mcp_dropped_stubs.go`, return `feature_removed`; a
concurrent thread keeps re-adding them — do NOT lean on):** `ha_service` /
`ha_states` / `ha_toggle` · `mqtt_publish` · `say` (TTS verb) ·
`adb_command` / `adb_devices` / `adb_screenshot` flat tools · govee / elgato.

**ABSENT — net-new:** IR learn/blast (`ir.go` + embedded `yaver_ir_bridge.py`,
mirror pyatv pattern) · HDMI-CEC · Android-TV-Remote-v2 (`androidtvremote2`
sidecar) · the **activity/macro engine + closed-loop verify** · AV device
connectors · IR-code learning capture + schema · generalized mobile remote
screen.

## 10b. Security cameras (ONVIF / Hikvision) — hub + viewer + controller + AI-watch

Cameras fit the hub model exceptionally well and **reuse the existing capture
pipeline** (`capture.go` already shells `ffmpeg`; ffmpeg ingests `rtsp://`
natively). Hikvision is **substantially open via standard protocols** — no
proprietary SDK, no Hik-Connect cloud needed:

| Need | Protocol (open) | Notes |
|---|---|---|
| **View live** | **RTSP** `rtsp://user:pass@ip:554/Streaming/Channels/101` | bridge RTSP→MJPEG/snapshot with ffmpeg → reuse `/capture/stream` + `robot_camera` image tool |
| **Snapshot** | ONVIF / ISAPI `/ISAPI/Streaming/channels/101/picture` | one JPEG → first-class image to the AI (robot_camera pattern) |
| **Discover** | ONVIF WS-Discovery | auto-find cameras on the LAN |
| **PTZ / presets** | **ONVIF** (Profile S) or ISAPI | the "controller" — pan/tilt/zoom/preset acts |
| **Motion / events** | ONVIF events or ISAPI alert stream (`pyhik`) | drive notify / voice / watch |
| **NVR (multi-cam)** | same RTSP/ONVIF/ISAPI, per channel | one connector, N channels |

**Yaver as the camera hub:** the hub box (Mi Box / Pi) is on the LAN with the
cameras; it pulls one RTSP stream, exposes it to phone/TV/web via the **existing
streaming surface**, runs PTZ via an `onvif`/`camera` connector, and — the part
Yaver is uniquely good at — **the AI watches the frame**: "someone at the door",
"garage left open", "package arrived", motion → routed to the multi-surface
notify (watch buzz, TV PiP overlay, car "someone's at the door"). Closed-loop
with the same capture/vision tools the AV remote uses.

**Do-no-harm + privacy (strict here):** **own cameras only** (never scan/attack
others' devices); credentials in the **vault** (never committed/Convex); feeds
stay **local** (LAN/relay) and **never** go to Convex (privacy contract already
forbids stream content); cameras are weak CPUs — **one RTSP pull, no swarm**.
Net security *improvement*: Yaver-as-local-hub lets the user **firewall the
camera off the vendor cloud** (Hik-Connect) entirely and still use it — and
keeps Hikvision's known CVEs/geopolitical baggage off the internet.

**Reuse vs net-new:** REUSE — `capture.go` ffmpeg path, `capture_*` verbs,
`/capture/stream`, `robot_camera` image tool, vault, multi-surface notify.
NEW — an ONVIF/ISAPI client (Go, or a python sidecar like the pyatv pattern;
libs to mirror: `python-onvif-zeep`, `pyhik`; HA's `onvif`/`hikvision`
integrations as reference) + a `camera` connector + RTSP→snapshot bridge.

## 10c. Spare-phone-as-camera, open/close devices, and vision

**Spare phone as a security camera.** An old phone running the Yaver mobile app
is already "a box" (the agent runs on Android). Its camera becomes a **camera
node** that streams to the hub — reuse the existing `robot.ExternalCamera` /
`HTTPCamera` producers + `robot_camera` image tool (a phone pushes frames; the
hub consumes them exactly like an RTSP cam). Zero new hardware; turns drawer
phones into a multi-cam mesh. Same privacy rules as §10b (local, vault, never
Convex).

**Open / close devices** (garage door, gate, blinds/curtains, smart lock, a
relay/switch). These are just connectors with `open` / `close` / `toggle` /
`stop` acts. Transport per kind: a Shelly/Tuya relay (WiFi-local), a 433 MHz
opener (RF blaster — same external-learner story as IR), Zigbee (cover), or a
servo/robot for dumb shutters. Risk tier matters: a lock/gate is a **confirm-
gated** act (the gateway dry-run+confirm path), and `home_key` already carries
arbitrary logical keys — `open`/`close` slot in as a new device kind in the
router with no new surface.

**Vision — local first, LLM optional.** On camera feeds the hub runs a tiered
pipeline: (1) cheap **local motion/object detection** (frame-diff, then an
on-box detector — YOLO/MobileNet class) to gate everything; (2) **optional LLM**
on the gated frame for semantics ("a delivery person left a box", "the garage is
open", "an unfamiliar face") — the `robot_camera` first-class image tool already
hands a frame to the model. Local-first keeps it cheap, private, and offline-
capable; the LLM call is opt-in per camera/event. Detections route to the
multi-surface notify (watch buzz / TV PiP / car readback) and can **trigger
activities** ("motion at the gate after 11pm → turn on the floodlight").

## 11. Privacy / do-no-harm + licensing

- Control of your **own** gear in your **own** room — benign, content-agnostic,
  OBS-like. No mains, no third party.
- Codes / activities / profiles stay **vault-local, never Convex** (privacy
  contract).
- **Licensing (repo is public):** SmartIR / Flipper-IRDB / community LIRC sets
  have mixed/unclear licenses. Bundle only clearly-licensed code sets, or
  fetch-on-first-use / import — do not ship someone's GPL/CC data into the tree.
  One-line ADR before embedding any DB.

## 12. Milestones

| M | Scope | New HW? | Built on |
|---|---|---|---|
| **M0** | Unify Apple TV + Mi Box under the connector model + one remote screen + voice; lights up phone/watch/car/TV as terminals | ❌ | all LIVE verbs |
| **M1** | Activity engine + closed-loop verify (capture) → "Watch Apple TV / Mi Box" | ❌ | + activity engine |
| **M2** | IR sidecar (Broadlink default; or Xiaomi-IR phone + DB) → satellite learn→blast = **true single kumanda across all 3** | ✅ learner (or Xiaomi-IR) | + ir engine |
| **M3** | AC connector — **WiFi-local first** (Tuya/Gree/Midea), cloud + IR-encoder + wired-bus fallbacks; HDMI-CEC for clean power/input | ❌ for WiFi ACs / ✅ for IR ACs | + cec, AC connector |
| **M4** | Camera connector — RTSP/ONVIF view + PTZ + **spare-phone camera node** + AI-watch (local detect → optional LLM) → multi-surface notify | ❌ (own IP cams / spare phone) | + camera connector, reuse capture/ffmpeg/robot_camera |
| **M5** | Open/close + switch devices (garage/gate/blinds/lock/relay) via WiFi-local / 433-RF / Zigbee; confirm-gated for locks/gates | ❌ for WiFi relays / ✅ for RF | + new device kinds in the router |

**M0–M1 ship with zero new hardware. M3 (WiFi ACs) and M4 (IP cameras) also
need none** — they're network connectors. Only IR ACs / the satellite box need
a learner.

## 13. OSS reuse (don't reinvent)

`postlund/pyatv` (✓ embedded) · `tronikos/androidtvremote2` (Mi Box remote v2,
no ADB/dev-mode) · `mjg59/python-broadlink` (RM4 IR+RF learn/blast) ·
`smartHomeHub/SmartIR` (120+ climate + TV/media code DB) ·
`flipperdevices/IRDB` (huge IR DB) · `crankyoldgit/IRremoteESP8266` (stateful
AC encoders) · `SwiCago/HeatPump` + `geoffdavis/esphome-mitsubishiheatpump`
(wired CN105 AC) · `rtl_433` (sub-GHz sensors — FIX case).

## 14. Net-new file surface (when building; all as OPS VERBS)

`ops_remote.go` (logical-key router) · `ops_activity.go` + an executor ·
`ir.go` / `ops_ir.go` (+ embedded `yaver_ir_bridge.py`, Broadlink default) ·
device connector manifests (`apple_tv`, `mibox`, `satellite_ir`, `ac`) ·
generalized mobile remote screen (from `appletv-remote.tsx`) · optional
`androidtvremote2` sidecar · optional `ops_cec.go`.

The brain (router), eyes (capture), voice, and the Apple-TV / Mi-Box transports
already exist. The net-new work is the IR sidecar, the activity engine, the
connectors, and the unified UI.
