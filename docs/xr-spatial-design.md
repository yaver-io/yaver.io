# Yaver + Talos XR / AR / VR — spatial design (XREAL-first, mobile-driven)

**Design only — no implementation.** The hero use case, the hardware reality, the
architecture, the per-platform matrix, and the spatial UI/UX for seeing "your
whole company from the beach" through XREAL glasses driven by the phone.

---

## 0. The hero use case — "my company from the beach"

You're on a lounger. iPhone/Android in your pocket, **XREAL One** glasses on. You
say "**show me the floor**" and a calm, dark, semi-transparent ring of panels
fades in around you: the **CST18D stripper running at 42,350 pcs / 3.2 A**, a
**cash-flow billboard** to your left, **today's production orders** ahead, the
**quote that's waiting on a supplier price** to your right. You glance at the
stripper card, say "**why did line 2 alarm**," and the AI agent (your plan, your
machines) answers in your ear while the alarm register highlights. Nobody around
you sees anything — it's a private 130" workspace floating over the sea.

That's the target: **the phone is the brain + the network (Yaver mesh to your
machines), the glasses are the display, voice + gaze are the controls, and the
company's live data are floating glass panels.**

---

## 1. Hardware reality (this shapes everything)

XR glasses split into two classes, and the design must serve both:

| Class | Examples | DoF | How it connects | Spatial capability |
|---|---|---|---|---|
| **Tethered display glasses** | **XREAL Air 2 / One / One Pro**, Viture, Rokid | 0–3 DoF (head-lock / anchored) | USB-C **DisplayPort-Alt-mode** → glasses are an external screen | The phone renders; glasses show it. 3DoF head-tracking on One/One Pro via onboard IMU ("anchored" virtual screens). |
| **Standalone 6DoF headsets** | Quest 3, Vision Pro, Android XR | 6 DoF | Their own browser / OS | Full room-scale WebXR (this is what `web/app/spatial` already targets). |

**Hard constraints (from `project_spatial_constraints` + the code):**
- **HTML can't render inside immersive-VR** — only WebGL. The existing `/spatial`
  scene already obeys this (xterm → canvas → texture; no `<div>`s in 3D).
- **iOS is the hard case.** XREAL on iPhone 15+/USB-C works as a *display*
  (mirror / virtual screen); **6DoF spatial on iOS is not first-class** (no public
  XREAL spatial SDK for iOS the way Android has it; ARKit world-tracking is the
  phone's, not the glasses'). So **iOS = a beautiful anchored 2D/2.5D virtual
  workspace**; full 6DoF room-scale is **Android-first**.
- **Android** gets the most: XREAL Nebula / the XREAL SDK + the phone's ARCore
  pose can drive a 6DoF spatial layout.
- **No new heavy native deps casually** — Talos mobile *already* ships
  `expo-gl` + `expo-three` + `three` (the `Cell3D` robotic-cell renderer) +
  `expo-sensors` (IMU) + `expo-camera`. Yaver mobile ships none of that yet. So
  Talos is the closer-to-ready spatial host; Yaver mobile would add the GL stack.

**Conclusion:** there is no single "VR build." There's **one spatial scene
graph** rendered three ways: (a) standalone-headset WebXR (Quest/Vision Pro —
already built), (b) Android tethered-glasses 6DoF (phone renders GL, glasses
display, IMU/ARCore pose), (c) iOS tethered-glasses anchored-2.5D (phone renders
GL to the glasses as a curved virtual screen, 3DoF head-lock).

---

## 2. Architecture — phone is the brain, glasses are the display

```
        ┌───────────────────────── the company ─────────────────────────┐
        │ Pi @ machine (Modbus)   cloud boxes   Convex (Talos/Yaver)      │
        └───────▲───────────────────────▲────────────────▲───────────────┘
                │ Yaver mesh / relay     │ HTTPS          │ HTTPS
        ┌───────┴────────────────────────┴────────────────┴──────────────┐
        │  PHONE (the brain)                                              │
        │   • Yaver mesh node → reaches machines/boxes from anywhere      │
        │   • data poller (reuse useAgentBridge pattern) → panel state    │
        │   • voice loop (whisper.rn / Deepgram → agent → Cartesia TTS)   │
        │   • scene graph: three.js / @react-three (web) OR expo-gl (RN)  │
        └───────────────────────────────┬─────────────────────────────────┘
                         USB-C DP-alt / WebXR
        ┌───────────────────────────────┴─────────────────────────────────┐
        │  GLASSES (the display + head pose)                              │
        │   floating glass panels · spatial audio · gaze reticle          │
        └───────────────────────────────────────────────────────────────-┘
```

**Two render hosts, one design language:**
- **Web `/spatial` (exists)** — the gold path for standalone headsets AND a
  WebView the mobile app can host for tethered glasses. Already does
  surface-detection for `xreal-air` / `android-trio`. *Reuse, don't rebuild.*
- **Native RN spatial (new)** — for the tightest tethered-glasses experience
  (lower latency, real IMU pose, no browser). Talos already has the GL stack;
  Yaver mobile would adopt it. Renders the **same panel catalog** as the web
  scene.

**Why the phone, not the glasses:** the data lives behind the **Yaver mesh** —
the phone is already a mesh node that can reach the factory Pi + cloud boxes from
the beach. The glasses never touch the network; they're a private display. This
is the same "phone is the substrate, thin display on top" pattern as the rest of
Yaver/Talos.

---

## 3. Per-platform matrix

| | **Yaver** | **Talos** |
|---|---|---|
| **Quest 3 / Vision Pro** (standalone) | `/spatial` WebXR (built) — ops/devices/tasks/mesh panels | `/spatial`-equivalent WebXR — company dashboards |
| **Android + XREAL** (tethered, 6DoF) | RN `expo-gl` spatial OR WebXR-in-WebView; ARCore pose | RN spatial on the **existing expo-three** stack; killer machine-edge AR |
| **iOS + XREAL** (tethered, anchored 2.5D) | curved virtual-screen workspace; 3DoF head-lock | same; ObjectCapture/LiDAR already present for part overlays |
| **Phone-only (no glasses)** | the normal 2D tabs (today) | the normal 2D tabs (today) |

**Graceful degradation is the rule:** no glasses → today's flat UI; tethered →
anchored panels; standalone → full room-scale. Same data, same voice, same panel
components — only the *presentation host* changes. (Mirrors how `/spatial`
already collapses 3 panes → 2 → 1 by viewport.)

---

## 4. Spatial UX — the layout

Reuse the proven geometry from `web/app/spatial/vr/VRScene.tsx` (a 1.5 m arc of
panels at eye height) and generalize it to a **company workspace**:

```
                     (gaze reticle ·)
        ┌─────────┐      ┌─────────┐      ┌─────────┐
        │ CASHFLOW│      │  FLOOR  │      │  QUOTES │     ← primary arc
        │  ₺1.2M  │      │ CST18D▶ │      │ 3 await │       1.5m, eye height
        └─────────┘      │ 42,350  │      └─────────┘
            −35°         │ 3.2A ●  │          +35°
                         └─────────┘
                         (straight ahead)
              ┌───────────────────────────────┐
              │  ▌ status strip — voice state, │            ← tmux-style strip
              │    alerts, "say a command"     │              1m below center
              └───────────────────────────────┘
                         · ground ring ·                     ← anti-vertigo grid
```

- **Primary arc (3 panels, ±35°)** — the user's pinned domains. Center = the one
  they're "in." Reuses `PaneArc` (1.05 m × 0.65 m quads).
- **Upper shelf (2.35 m, ±25°)** — overflow / drill-downs (a machine's register
  table, a quote's supplier list) — reuses the `RemoteWindowStack` placement.
- **Status strip** — voice state + the single most-urgent alert (an alarm, an
  overdue invoice). Reuses `StatusStrip`.
- **Ground ring** — subtle grid so the user stays oriented (anti-vertigo).
- **Beach mode** (see §7) — dims everything, widens spacing, drops to a calm
  3-card "company at a glance."

**Panel anatomy (one design, all domains):**
```
┌──────────────────────────────┐
│ ● CST18D Stripper #1   running│  ← title + live state dot (green/amber/red)
│ ──────────────────────────────│
│   42,350 pcs      3.2 A        │  ← 2-3 hero metrics, big mono numerals
│   AWG_22_TEFLON   no alarm     │
│ ──────────────────────────────│
│   ▁▂▅▇▆▃  cycle/min (sparkline)│  ← one trend, canvas-drawn
│   [ recall program ]  ← gaze   │  ← at most one primary action (gated)
└──────────────────────────────┘
```
Glass material (frosted, `YaverGlass`-equivalent in 3D via translucent
`MeshBasicMaterial`), high-contrast mono numerals (legible on see-through
optics), **one** trend + **one** action per card. Everything else is a
voice-or-gaze drill-down. Optics are low-nits and see-through, so: **dark UI,
few elements, fat type, no fine gridlines.**

---

## 5. Interaction — voice-first, gaze-second, phone-as-trackpad

Tethered glasses have **no controllers and (mostly) no hand tracking**, so the
input model is:

1. **Voice (primary, hands-free)** — the existing stack already does this. Wake
   or push-to-talk → STT → agent → TTS, with the floating **VoiceOrb3D** showing
   state (idle/listening/thinking/speaking) and **positional audio** so replies
   come *from* the orb. Commands: "show me the floor", "why did line 2 alarm",
   "what's overdue", "pin cashflow", "go back".
2. **Gaze + dwell (selection)** — a head-ray reticle; dwell ~800 ms on a card to
   focus it, on an action to trigger it (with a confirm for risky writes). No
   hands needed.
3. **Phone as trackpad/clicker** — the phone screen becomes a minimal control
   surface: swipe to rotate the arc, tap to select the gazed card, a big
   **Approve/Deny** button for gated machine writes. (The phone is in your hand
   anyway; it's the reliable, eyes-free confirm.)
4. **Standalone headsets** add real hand/controller rays on top (Quest/Vision
   Pro) — already supported by `@react-three/xr`.

**Safety carries over from the machine engine:** a Modbus write (e.g. recall a
program, change crimp height) shows a **confirm card** and requires an explicit
"yes" (voice) **and** the phone Approve button for high-risk params — same
two-key gate as `machine_write allowHighRisk` + the verified read-back shown
live on the card.

---

## 6. The panel catalog (what floats)

**Yaver (dev/ops owner):**
| Panel | Live data | Source |
|---|---|---|
| Devices/mesh | nodes, transport, online, overlay IPs | `/devices`, mesh peers |
| Tasks | running agent tasks, output, tokens | `/tasks` (already in VRScene) |
| Monitor | errors, uptime, machine metrics, releases | monitor screen data |
| Health | custom HTTP checks up/down/latency | healthmon |
| Terminal | live agent shell | `TerminalPane3D` (built) |

**Talos (business owner) — the richer set:**
| Panel | Hero metric | Source |
|---|---|---|
| **Machine-Edge / IoT** ⭐ | state · pcs · amps · alarm · program | `machineTelemetry` / `/machine-edge` |
| **Quotations / RFQ** | material cost, supplier quotes, awaiting prices | `rfqMultiSupplier`, `latestPrices` |
| **Production orders** | % complete, due date, station | `productionOrders/Progress` |
| **Inventory** | stock, bin, shortfalls (MRP) | `materials`, `mrp` |
| **Quality** | pass/fail %, NCR, crimp pull-force | `dizgiInspections`, `crimpPullForceTests` |
| **Finance / cash flow** | receivables, overdue, balance | `invoices`, `cashflow`, `accounts` |
| **Sales orders** | status, ship ETA | `salesOrders`, `deliveryNotes` |

**The two killer demos:**
1. **Shop-floor AR (Android, on-site):** stand in front of a machine → its
   **live telemetry card pins over the actual machine** (anchored by ARCore +
   the `machineEdgeDevices.deviceId`), alarm registers glow red, "recall program
   AWG_20" by voice → confirm on phone → verified read-back animates on the card.
   Talos's `Cell3D` (expo-three) already renders a live robotic cell — extend it
   from a screen widget to a world-anchored AR object.
2. **Beach CFO (iOS/Android, off-site):** the calm 3-card overview — floor
   status, cash position, the one decision waiting on you — over the sea.

---

## 7. "Beach mode" — the relaxed ambient profile

A distinct UX profile (not a different app):
- **Calm**: 3 cards max, wide spacing, low brightness, slow fades, no sparklines
  unless asked — "company at a glance," not a NOC wall.
- **Glanceable + ambient**: cards auto-surface only what *changed* or *needs you*
  (a new alarm, an overdue invoice, a quote that got its last price). Otherwise a
  single serene "all nominal" line.
- **Voice-only by default**: hands stay on the drink. "anything I need to know?"
  → the agent narrates the day in one breath. "show me the floor" → expands.
- **Privacy**: see-through optics = nobody reads your P&L; the phone screen can
  blank during glasses use.
- It's the same scene graph + panel catalog with a `profile: "beach"` preset
  (spacing, density, brightness, poll cadence) — mirrors `/spatial`'s existing
  `?surface=` preset switching.

---

## 8. Use-case catalog (all)

- **Beach / remote owner** — ambient company overview, voice Q&A, approve the one
  thing that's blocked. (hero)
- **On the shop floor** — telemetry pinned over each machine; walk the line and
  see every machine's state/alarm without touching an HMI; AI explains anomalies.
- **Remote support (ties to Support Links)** — a friend/tech shares their machine
  via a Yaver support link; you "stand in their factory" in AR from your office.
- **Dev/ops (Yaver)** — the existing `/spatial` terminals + tasks + mesh, now
  also on tethered glasses, not just Quest.
- **Quoting** — float a quote's BOM; gaze a part → supplier options + lead times
  fan out; say "pick the cheapest under 2 weeks."
- **Standup / review** — pin production + quality + cashflow in a row; talk
  through the day spatially instead of tab-switching on a phone.
- **LiDAR part overlay (iOS Pro)** — Talos `ObjectCapture` already scans parts;
  overlay the scanned geometry + its spec/quote in AR next to the real part.

---

## 9. Phasing & reuse (when you say "build it")

1. **P0 — extend web `/spatial` to the company catalog.** Add the Talos data
   panels (machine-edge, quotes, finance) to the existing WebXR scene using the
   built `TerminalPane3D`/canvas-texture + `useAgentBridge` patterns. Instantly
   works on Quest/Vision Pro **and** as a WebView for tethered glasses. Lowest
   effort, highest coverage. *No new native code.*
2. **P1 — tethered-glasses host in mobile.** Host `/spatial` in a full-screen
   WebView routed to the USB-C display; feed it phone IMU for 3DoF. Add a minimal
   phone "trackpad/approve" control surface. Yaver + Talos both.
3. **P2 — native RN spatial (Talos first).** Promote `Cell3D` (already
   `expo-gl`+`expo-three`) into a multi-panel spatial scene + ARCore/ARKit pose
   for true anchoring; the machine-over-machine demo. Yaver mobile adds the GL
   stack here.
4. **P3 — beach mode + ambient agent** — the calm profile + "narrate my company"
   voice summary.

**Reuse map:** scene geometry + panels + voice orb + spatial audio + agent
polling = `web/app/spatial/*` (built). Voice STT/TTS = `mobile/src/lib/speech.ts`
+ `voiceSession.ts` + `desktop/agent/voice_*`. 3D-in-RN = Talos `Cell3D` +
`expo-gl/expo-three`. Data = the Convex/agent endpoints already feeding the 2D
tabs. Safety = the machine-engine confirm/read-back gate.

## 10. Open decisions (for when we build)
- iOS spatial depth: settle for anchored-2.5D virtual screen, or invest in an
  ARKit-pose native module to fake 6DoF for tethered glasses?
- WebView-`/spatial` vs native-RN as the tethered host (latency vs reuse).
- Which app leads: Talos (richer data, GL stack ready) almost certainly first.
- Anchoring source for shop-floor AR: ARCore plane/image anchors vs a QR/AprilTag
  on each machine mapped to its `deviceId`.
```
