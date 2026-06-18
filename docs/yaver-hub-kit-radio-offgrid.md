# Yaver Hub Kit + Radio / Off-Grid — Design & Honest Analysis

> Companion to `yaver-personal-assistant-mvp.md` (§8 hardware, §9 TV kit).
> This doc covers the **physical kit** and the **radio / off-grid** dimension.
> Source-of-truth rule (CLAUDE.md): where this says *proposed*, it doesn't exist
> yet. Radio law is real and regional — every TX claim here is checked against
> license-free bands; do not ship TX on licensed/broadcast bands.

## 0. Hardware philosophy (decided)

- **The phone is the brain/hub** (local model = cheap inference tier, real
  Android, SIM/2FA, BLE/WiFi). The kit is **peripherals**, never compute.
- **v1 = a 3D-printed tray/shell holding commodity *pluggable* modules.**
  No soldering, no GPIO HATs, no terminal blocks, no custom PCB, no RF front-end.
  Everything connects by **USB / USB-C / BLE / WiFi**. Pure curated bundling.
- **Commodity, pre-certified modules only.** Off-the-shelf USB dongles and
  ready-made radio boards are already FCC/CE/RED certified → **we inherit their
  certification and design nothing that needs a TX cert.** This is the single
  biggest cost/landmine we sidestep by not building custom RF.
- **Premium is OK — this is not a race to the cheapest BOM.** Pick good modules;
  position as a quality kit. Margin comes from the bundle + recurring services,
  not from shaving cents.
- **Phasing:** (1) 3D-printed tray + commodity modules (zero tooling, validate
  demand) → (2) if it sells, a custom **PCB / integrated enclosure** later →
  (3) injection-molded run only if (2) sells out. Never tool ahead of demand.
  (This is the Home Assistant Green/Yellow path.)
- **Agnostic by design** — to content *and* to upstream hardware (CLAUDE.md
  streaming rule). Ship **no splitter/stripper**; non-inducing dual-use tool.

## 1. Hardware capability manifest (context for the agent)

The kit is only useful if **the assistant knows what's plugged in**. Proposed: a
**HW capability manifest** the agent gets as context, so intent routing can gate
on presence ("send SOS" requires a LoRa node; "watch home TV" requires capture).

- Detect attached modules (`lsusb` / USB VID:PID match, BLE/WiFi service scan,
  Meshtastic API ping) → normalized capability set.
- Shape (proposed): `{capability: "ir_tx" | "ir_learn" | "lora_mesh" |
  "sdr_rx" | "fm_rx" | "hdmi_capture" | "zigbee" | "ble" | "walkie_audio",
  module, transport: usb|ble|wifi, present, region, tx_allowed}`.
- Stored **local-only** (per the privacy contract; like `gateway_phone_inventory`).
- Fed into the agent's context so it answers honestly ("I can't send a mesh
  message — no LoRa radio detected") instead of hallucinating capability.
- Reuses the existing pattern: `get_system_info` / `agent_machine_inventory` /
  `gateway_phone_inventory.go`. This is a sibling manifest, not new infra.

## 1.5 Phone ↔ kit transport (WiFi primary, BLE for low-power)

BLE is too thin for the rich data plane (media place-shift, HDMI capture,
mic-array audio, fast UI). Use **WiFi for richness, BLE for endurance** —
power-aware and reusing Yaver's existing IP stack (HTTP 18080 / QUIC 4433 /
beacon 19837), so there's **no new transport code**.

- **Prefer kit-hosted WiFi AP (SoftAP) over Wi-Fi Direct.** Wi-Fi Direct strands
  iOS (Apple uses AWDL, not Wi-Fi P2P). A SoftAP the phone simply *joins* works
  on **iOS + Android** and speaks the normal Yaver HTTP/QUIC-over-IP.
- **Layered, power-aware:**
  | Situation | Link | Note |
  |---|---|---|
  | Daily, home router | both on **home WiFi LAN** | Yaver's normal LAN/beacon path — nothing new. |
  | Off-grid / no router | kit **SoftAP**, phone joins | cross-platform; same IP stack. |
  | Low-power / relay mode | **BLE only** (+ LoRa) | WiFi down to save battery (§4.2). |
- **BLE's narrow role:** text-mesh + control + wake + LoRa-node bridge (low data,
  low power — Meshtastic uses BLE phone↔node for text). WiFi/SoftAP flips **on**
  only for the rich data plane.
- **WiFi is a local *bubble*, not a backbone:** ~10–30 m through walls (rubble
  kills it), draws ~1–2 W (can't run 24/7), ~8–10 clients, and iOS nags on a
  no-internet AP ("stay connected?"). So **WiFi = on-demand only** (pull a batch /
  local voice call, then drop). **LoRa is the only km-scale link.**
- **SoftAP uplink pass-through is DAILY-only** (4G modem / home-router bridge via
  `peer-egress`): one WiFi join gives the phone kit services *and* internet **when
  internet exists.** In a blackout there is **no uplink to pass through** — do not
  count on it for disaster mode (§4.0).

## 1.6 Messaging & voice (digital only, WhatsApp-style, bandwidth-optimized)

Digital text + voice (no analog walkie for this path). **Async / store-and-forward
is the default model — comms do NOT need to be real-time.** Reuses existing
Yaver pieces.

- **Everything is an async message** — text, voice notes, location, SOS — queued,
  hopped opportunistically, retried until delivered. No live end-to-end path
  needed at send time (the natural fit for a nodes-come-and-go mesh; it's how
  WhatsApp mostly behaves anyway). Yaver app is RN **iOS+Android** already.
- **Voice = async voice notes, not calls:** **bandwidth-optimal = on-device STT →
  send text → TTS on the far end** (reuse `voice_*.go`); **Codec2 (700–3200 bps)**
  audio note only if the original voice is wanted (eats airtime).
- **Real-time Opus calls = daily / local-WiFi-island *bonus* only** (reuse the
  shipped **WebRTC Opus** stack, commit `85876105e`) — never required, never
  expected over the mesh / in blackout.
- **Honest UX:** `sent → queued → delivered`; "queued — will deliver when a path
  is found" is a *normal* state (minutes/hours OK), not an error. LLM uses the
  slack to batch, dedupe over time, and keep SOS at the front.
- **Optimization stack:** text-first; Opus on WiFi, Codec2 last-resort on LoRa;
  **binary/protobuf framing** (never JSON over RF); LLM compression +
  store-and-forward.
- **iOS+Android — confirmed,** with two iOS caveats to honor: **no Wi-Fi Direct on
  iOS** (→ SoftAP, §1.5) and the **iOS Local Network permission** prompt is
  required to reach the kit/SoftAP/LAN (else discovery is silently blocked).

## 2. The module catalog (all commodity, all pluggable)

| Capability | Commodity module (example class) | Transport | TX? | Legal note |
|---|---|---|---|---|
| **HDMI capture** | USB HDMI capture stick | USB | — | Dual-use; HDCP-black passes through (no stripper). |
| **IR control (blast)** | Broadlink-class WiFi IR, or USB IR | WiFi/USB | IR (not RF-regulated like radio) | TV/AC/satellite. Stateful AC = protocol encoder (kumanda). |
| **IR learn** | USB IR with RX, or a small IR-RX module | USB | IR | Phone IR is blast-only/can't-learn — learn needs RX. |
| **Zigbee / Thread** | USB Zigbee/Thread coordinator (Sonoff-class) | USB | license-free 2.4G | Lights/plugs/locks/sensors. |
| **Z-Wave** | USB Z-Wave stick | USB | license-free sub-GHz | Region-specific frequency (EU 868 / US 908). |
| **433/315 MHz** | USB 433 transceiver | USB | ISM license-free | Cheap sensors/switches. |
| **BLE** | phone built-in (no module) | BLE | license-free | Beacons, sensors, pucks. |
| **LoRa mesh** | **Meshtastic board** (Heltec/LilyGo/RAK) | BLE/USB | ISM license-free | Off-grid text/SOS — see §4. Integrate, don't reinvent. |
| **SDR receive** | **RTL-SDR USB dongle** | USB | **RX only** | AM/FM/shortwave/weather/ADS-B/ham monitor. Broad + legal. |
| **FM receive** | RTL-SDR (covers FM), or a USB/Si47xx FM tuner | USB | **RX only** | Stream FM audio for fun + emergency broadcasts (§3, §5). |
| **Walkie voice** | Commodity **PMR446/FRS/MURS/CB** handheld + USB audio+PTT interface | USB audio | license-free voice | Yaver as a voice endpoint on the channel (§3). |
| **AM/FM/SW receive** | RTL-SDR, or **Si4732/35 USB AM/FM/SW** tuner | USB | **RX only** | AM needs direct-sampling/converter on RTL-SDR; Si47xx does AM natively. |
| **Micro FM transmit** (optional) | **Part-15-certified FM transmitter** (car-modulator class) | USB/3.5mm | **TX (micro)** | Legal **US-only** under FCC §15.239 (~200 ft); **region-gated, off by default** elsewhere (EU/BTK stricter). Play assistant/SDR audio on a nearby FM radio. |
| **Far-field mic array** | USB conferencing/mic-array | USB | — | Room-scale "talk to Yaver" wake-word/voice. Biggest in-home UX lift. |
| **Speaker / audio out** | USB speaker / USB audio DAC | USB | — | Assistant TTS voice; FM/SDR/walkie playback. |
| **Camera** | USB webcam | USB | — | Presence/security/vision (`robot_camera` path). |
| **GPS/GNSS** | USB GPS | USB | RX only | Off-grid location for SOS (Meshtastic often has onboard GPS). |
| **Cellular uplink** | USB 4G/LTE modem + SIM | USB | licensed (carrier) | Backup uplink + the *one connected node* `peer-egress` shares (only honest "internet mesh"). |
| **Temp / humidity** | BLE thermo-hygrometer (Govee/Xiaomi/Aqara) | BLE | — | Comfort, frost/damp/mold alerts, greenhouse/pet. Reuses `govee_*`/`sensors` verbs — near-zero integration. |
| **Air quality / gas / smoke** | BLE/USB AQ sensor | BLE/USB | — | Home health + post-quake gas-leak safety. |
| **Local storage** | USB SSD/flash | USB | — | DVR recording, offline model weights, mesh/SDR logs. |
| **Wired net** | USB Ethernet | USB | — | Connectivity fallback when WiFi is flaky. |

### Backbone & power (the only "infrastructure" in v1)
- **Powered USB hub** = the spine; everything hangs off it. Spec it generously —
  a weak hub browns out when capture + SDR + modem draw at once.
- **Phone-adapter set** (USB-C / micro / Lightning) so any second-hand phone docks.
- **Battery + BMS (optional) + solar input (optional)** — this is what makes the
  **off-grid/earthquake mode real**: the kit keeps running when the grid is down
  (§4). Battery is the difference between "home gadget" and "disaster gear."

### Deliberately excluded from v1
- **TX-capable SDR (HackRF)** — transmit = type-certification + legal landmine.
  Agnostic stance: a licensed hobbyist may use their own; no Yaver feature needs it.
- Anything requiring **soldering, HATs, terminal blocks, or a custom RF front-end**.

### Novelty / phase-2 candidates
eInk status display (mesh messages without the phone), NFC/RFID reader,
buzzer/siren (quake/alert output), thermal camera (hobby/search).

Everything above is **buy-and-plug**. The 3D-printed tray just holds a **powered
USB hub** + the modules + the phone dock (+ optional battery). That's the whole
v1 BOM — zero soldering, zero custom PCB.

### MCU choice (only if/when we build the optional puck — phase 2)
"Doesn't have to be ESP32." Honest fit:
- **ESP32-S3** — WiFi+BLE+USB-OTG, IR libs, RadioLib (LoRa); good *home/IR puck* MCU.
- **nRF52** — best BLE + low power; what battery **Meshtastic mesh nodes** use.
- **RTL-SDR / SDR work → the phone or a Pi Zero 2 W**, NOT an MCU (ESP32 can't
  decode SDR).
- **Conclusion:** no single chip does it all. v1 avoids the question entirely by
  using commodity modules; pick the MCU per-puck only at phase 2.

## 3. Radio — the honest taxonomy (deep analysis)

The enthusiastic version of "talk to Yaver over AM radio / walkie / mesh" trips
several laws. Sort every idea into one of five buckets:

### Bucket 1 — RX-only, broad, legal (lean in hard)
**SDR (RTL-SDR) + FM tuner = receive almost anything; transmit nothing.**
Legal everywhere, rich, hobby-friendly:
- AM/FM broadcast, shortwave, **NOAA/weather**, **emergency broadcasts**, ADS-B
  (planes), ham monitoring, pager/utility.
- **Yaver value:** decode → the local model **narrates/summarizes** ("emergency
  broadcast on 162.4 says evacuate zone 3"), **streams the audio** to the app,
  logs alerts. Works fully offline. This is the safest, highest-value radio
  feature and it's all receive.

### Bucket 2 — Voice TX on license-free bands (fun/novelty, fine)
**PMR446 (EU) / FRS (US) / MURS / CB** — license-free *analog voice* bands.
- **Yaver value:** a **walkie bridge** — Yaver answers on a family channel
  ("talk to Yaver over the walkie, no phone needed"), or relays
  walkie↔app↔walkie. Region-gated (PMR446 ≠ FRS; pick by locale).
- **Honest limit:** analog **voice only — no data, no internet.** It's a fun
  hobby endpoint, not a transport. Don't oversell it.

### Bucket 3 — Text/SOS mesh on ISM (the real off-grid answer)
**LoRa / Meshtastic** (433/868/915 ISM, license-free).
- Proven off-grid **text + GPS location + SOS + telemetry** mesh, open-source,
  phone-paired over BLE, used exactly for hiking/disaster. **Integrate
  Meshtastic; don't reinvent it.**
- **Honest limit:** ~kbps. It is **NOT internet** — text/coordinates/SOS only.
  Anyone promising "internet over LoRa" is lying. (See §4.)

### Bucket 4 — Ham / amateur (agnostic, never a dependency)
Huge capability, but **requires a license, forbids encryption, forbids
commercial use.** A commercial product can't depend on it. Position: Yaver is
**agnostic** — a licensed hobbyist may point their own gear at it (same dual-use
stance as the capture card), but no Yaver feature *requires* ham, and we never
ship encrypted traffic over it.

### Bucket 1b — Micro-power AM/FM TX (legal in the US under Part 15; region-gated)
A real carve-out, but it's a **power limit, not specific channels** — micro-power
on *any clear* broadcast frequency, no license:
- **FM** — FCC §15.239: 250 µV/m @ 3 m, **~200 ft range** (the legal "car FM
  transmitter" regime).
- **AM** — FCC §15.219: **≤100 mW** input + ≤3 m antenna, ~200 ft.
- **Yaver value:** play the assistant's voice / SDR / walkie / streamed audio
  onto a **nearby FM radio** (car, kitchen) within Part-15 limits.
- **Constraints:** room/yard range only; **US-only by default** — EU/ETSI and
  **Turkey (BTK) are much stricter** (mostly disallow / far lower), so
  **region-gate it, off by default outside the US, verify per country.** Use a
  **commodity FCC-certified Part-15 FM module** (inherit cert; build no RF).
  Never marketed as "your own station."

### Bucket 5 — Illegal / avoid (write it down so nobody builds it)
- **TX on AM/FM broadcast bands *above* the Part-15 micro-power limits** (Bucket
  1b) → illegal (licensed broadcasters only). So **"broadcast to the
  neighborhood over FM" is out**; only the ~200 ft Part-15 regime is legal, and
  only where the region allows it. For two-way voice use Bucket 2; for off-grid
  text use Bucket 3.
- TX on any licensed band without a license; over-power on ISM; custom RF that
  isn't type-certified. **We design no custom TX → this bucket can't happen by
  accident.**

## 4. Off-grid / disaster mode (the differentiator)

Genuinely valuable and mission-aligned (Turkey 2023 quakes; "inventor→society").
Be honest about physics and liability.

### "Internet mesh" — what it really is
- **LoRa mesh = text/SOS/telemetry**, not internet (too slow).
- **WiFi mesh** = higher bandwidth but short range; a local-area neighborhood
  mesh at best.
- **True internet sharing** only works if **one node has a real uplink**
  (cellular/WiFi/Starlink), then **peer-egress shares it** — and Yaver already
  has that primitive (`egress_proxy.go` / `egress_bridge.go`, opt-in,
  same-user, RFC1918-blocked, *not* an open relay). So: "if anyone in the mesh
  has connectivity, others can reach it" is real **only** over a real link, not
  over LoRa. Market that precisely; never "internet over walkie/LoRa."

### Offline assistant (architectural requirement)
Disaster = no towers, no internet, no auth servers. So the assistant must run
**fully offline**:
- **Phone local model** (the cheap tier from MVP §2) answers with no cloud.
- **Skip-OAuth / offline mode** (§5) — no Convex, no relay, no auth roundtrip.
- **LoRa SOS/location** out via Meshtastic; **SDR** in for emergency broadcasts.
- Walkthrough: towers down → phone flips to offline mode → local model assists,
  Meshtastic carries SOS+GPS peer-to-peer, RTL-SDR receives the emergency
  bulletin and the model reads it aloud. All local, all license-free.

### Liability (non-negotiable honesty)
This is **best-effort hobby/community gear, NOT certified life-safety
equipment.** Never market "Yaver will save you in an earthquake." Market
"best-effort off-grid text/SOS and emergency-broadcast receive when
infrastructure is down." Overpromising life-safety is both dishonest and a
liability bomb.

### 4.1 Emergency mesh — range, endurance, routing, pre-provisioning (deep)

**Range (LoRa/Meshtastic, field-verified — environment dominates):**
| Environment | Per-hop (realistic) |
|---|---|
| Dense urban, stock antenna | **0.5–1 km** |
| Urban, antenna at window/balcony | 2–3 km |
| Suburban, decent placement | 3–8 km |
| Rural / line-of-sight | 10–20 km (single hop can exceed 20) |
| **Earthquake/rubble, ground-level** | often a **few hundred metres** |
- **Mesh extends range only with intervening nodes** (≤7 hops). A lone kit =
  single-hop. **Density is a public good** — more neighbors with kits = more
  range for all.
- **Elevation is the multiplier:** one rooftop/balcony **relay node on solar**
  turns "few hundred metres" into neighborhood coverage. → ship a **relay-node
  mode**.
- **Text/GPS/SOS only — never voice or internet** over LoRa (~kbps). Voice =
  walkie (Bucket 2); richer data = WiFi/BLE short-range (last ~100 m).

**Endurance:** LoRa sips power — a **10,000 mAh pack relays for days**, a
**5–10 W solar panel sustains it indefinitely**. The **phone is the hog**, so the
**kit must relay autonomously on its own battery with the phone off/dead**;
wake the phone only to read/compose. Airtime is scarce by **law + physics**
(EU 868 = **1% duty-cycle**, ~36 s TX/hour; channel is a few kbps) — which is
why load-reduction matters.

**Routing & "pollution" — LLM at the *content* layer, not the packet layer:**
- ❌ **No LLM in packet routing** — rebroadcast is a millisecond decision on the
  LoRa MCU; routing stays a **deterministic protocol** (managed flood /
  hop-limit / SNR-aware). "LLM routes packets" is a category error.
- ✅ **The local LLM shrinks/shapes the offered load** (the actual cause of mesh
  collapse), all on phone/host: **(1) compress** human messages into tiny
  structured payloads; **(2) triage/prioritize** SOS over chatter during
  congestion; **(3) dedupe/aggregate at relay nodes** (one message for ten
  identical reports — biggest reducer); **(4) template** structured SOS
  (location+status+needs); **(5) answer locally** from a baked-in first-aid KB so
  nothing is transmitted at all. Net design: *deterministic routing + LLM
  load-shaping + content-aware smart-relay.*

**Texting (async by design):** pre-shared **family/group channel** (AES via PSK),
**public emergency channel**, **direct messages**; **store-and-forward** with
**hold-and-forward at backbone nodes** (relay stores for offline recipients,
delivers when they reappear) and **data-mule ferrying** (a person walking
between neighborhoods physically carries queued messages, bridging disconnected
islands — only works because it's async); **priority queue** (SOS preempts).
Phone = app + local model; kit = radio + relay + battery.

**No-OAuth offline identity (make-or-break):** you **cannot authenticate after
the quake**. So provision in advance — exchange the **channel PSK in person via
QR** to form a "family emergency mesh" that works with **zero servers**;
on-device keypair for identity. Product feature: **"Set up your emergency mesh
once — works when everything's down."** First-run must never require internet for
this path. (Encryption on ISM/LoRa is fine — unlike ham.)

**Security boundary:** offline mode = **local + RF only** (assist + comms + local
home control). **No remote gateway acts** (no money / act-as-user) — no auth, no
audit offline. Hard mode gate (mirrors §5 / MVP §4).

**Mesh trust is self-contained — NO cloud concepts.** In blackout, drop every
internet-era primitive: **no Yaver guests, no Convex, no OAuth, no relay.** The
mesh is its own trust domain: **membership = pre-shared channel PSK** (QR-shared
in person, pre-disaster); **identity = per-device local keypair** (peer
exchanged). Two separate, non-bridging trust models: **cloud** (guests/OAuth,
daily) vs **mesh** (PSK + local keys, offline). The mesh must never depend on a
cloud concept — that's what makes it survive a total blackout.

### 4.1b Liveness / "I'm alive" check-in roster (deep)

A neighborhood **check-in roster**: pre-register (identity + home), daily quiet
heartbeat, and in emergency a person taps **"I'm alive"** → propagates over the
mesh → roster shows who's checked in, who's silent, and **where they live** → a
prioritized list of addresses for neighbors/rescuers to physically check.

**The critical rule — silence ≠ dead.** No-signal has benign causes (dead
battery — most likely; buried-but-fine phone; out of range; no device; didn't
tap). Presuming casualty misdirects rescue and causes panic. **Three states:**
- ✅ **Alive** — explicit tap, *as of HH:MM*.
- ❓ **Unknown — check** — no signal = a **to-do, not a verdict**.
- 🆘 **Needs help** — explicit SOS.
Value = a **checklist of doors to knock on**, never a casualty list.

**Device-liveness ≠ person-liveness:** auto-heartbeat = "device powered" (weak —
can beat from a trapped person's pocket); explicit tap = "person asserts OK"
(stronger but **stale fast** → stamp + **decay** "alive as of HH:MM").

**Data model:** distributed eventually-consistent roster — **CRDT, last-writer-
wins per person**, `{id, status, alive-as-of, home-loc}`; async store-and-forward,
converges via mesh (+ data-mule). **Hierarchical to avoid heartbeat pollution:**
phones sync presence **locally to the backbone** (WiFi/BLE), backbone **holds the
neighborhood roster**, backbones exchange only **deltas** over LoRa. The
**muhtarlık backbone naturally owns the roster** (fits the real mahalle registry).

**Privacy/safety:** a who's-home map is a burglary/vulnerability map → **opt-in,
group-scoped (PSK), local-only (never Convex)**, home reg encrypted in-group.
**Daily presence stays minimal/private** (no continuous "I'm home" broadcast);
**full roster reveal is an emergency-armed mode**, not always-on.

**UX:** register-once flow (you + home + who-to-notify, no internet); emergency =
big **"I'm OK"** + **SOS** buttons, assistant prompts + auto-composes; roster view
= color states + addresses + last-seen, sorted by "unknown — go check."

**Honest limit:** best-effort; **misses** no-device/dead-battery/out-of-range
people and shows **false unknowns**. **Aid to human search, not an authority** on
who's alive. Never induce panic from "offline."

### 4.1c Earthquake detection / early warning (deep, honest)

**Tight MCP packs are required** for the local 7–14B model — tool-selection
accuracy collapses in a firehose; a scoped pack (e.g. `afad_recent`,
`kargo_track`) routes reliably, is testable, and is read/approval-tagged. (Same
"appliance not firehose" principle, now a hard requirement for local models.)

Three **distinct** layers — do not conflate:

**1. Reporting (poll AFAD/Kandilli) — post-hoc, NOT early warning.** By publish
time the shaking already happened at the epicenter; this is a notification feed
(sec–min latency) for situational awareness + **auto-arm trigger**. Sources:
**AFAD JSON** `deprem.afad.gov.tr/EventData/GetEventsByFilter`, Kandilli/KOERI
list + community GeoJSON APIs (~1 min). **Official API, respectful cadence
(~30–60 s), back off on blocks** (public-good servers).

**2. Local shake detection (the docked phone's accelerometer).** A stationary,
powered, always-on box-phone is an **ideal seismic station** (better than a
pocket phone). Detects shaking the instant it arrives → local alarm
("DROP–COVER–HOLD"), lights, auto-arm. Own-site advance warning ≈ 0; the value is
**warning others farther away** (layer 3).

**3. Community sensor mesh = real EEW, and it's *proven* (MyShake, UC Berkeley,
Science Advances).** On-device NN spots quake-like motion → triggers to a
coordinator → **network-detection confirms across multiple devices** → alert
reaches farther areas **before the S-wave**. A dense field of Yaver boxes *is*
this network. Physics: P-wave (~6–8 km/s) + internet alert (~light) race the
destructive S-wave (~3–4 km/s) → **seconds–tens of seconds** lead, growing with
distance.

**Critical honest constraints:**
- **A single box must NEVER alarm alone** — false positives (dropped box, truck,
  footsteps) destroy trust. Requires on-device filtering **+ network
  corroboration** (multiple boxes simultaneously) → why **density + a coordinator**
  matter; no single-node EEW.
- **EEW needs the FAST path (internet), not LoRa** — the alert must outrun the
  wave; LoRa airtime/hops are too slow for near-field. **EEW runs while internet
  is up; LoRa mesh is the *after*-quake layer.** Two phases, two transports.
- **Complement AFAD, never replace.** Best-effort, experimental, complementary.
- **Don't overpromise** — EEW gives seconds, sometimes zero, sometimes false.

**Lifecycle (auto):** daily (phone shake-detect + AFAD poll, coordinator online)
→ shaking → local detect + **corroboration** → EEW push to farther boxes + local
alarm + **auto-arm** → big quake drops internet → **blackout mode** (LoRa mesh +
roster + SOS). One sensor (phone accelerometer) + one feed (AFAD) auto-arms the
whole stack.

**Verdict:** reporting + auto-arm + local-shake alarm = real, do it. Community
EEW = novel, MyShake-proven, ideal for always-on docked phones, but
**density-dependent, corroboration-critical, internet-timed, best-effort** — a
stretch goal, never a v1 guarantee or an AFAD replacement.

### 4.2 Power system — battery + BMS + solar (deep)

**v1 pragmatic: don't build a battery.** A **USB-C PD power bank IS a
battery+BMS+regulation in a certified box.** v1 = commodity PD bank + USB solar +
pass-through. Custom **LFP + BMS + MPPT** is a **PCB-phase** upgrade, never v1.

**Chemistry — LiFePO4 (LFP), not standard Li-ion, for emergency use:** LFP wins
on the axes that matter for gear that sits dormant for months — **no thermal
runaway** (post-quake fire safety), **2,000–5,000 cycles**, **low
self-discharge**, **wide temp** — at the cost of size/density. → **budget v1 =
quality Li-ion PD bank; premium/emergency SKU = small LFP power station.**
Safety: **never charge lithium below ~0 °C**; the 3D enclosure **must vent** (no
sealed pack near heat).

**Power budget (avg draw; disaster mode gates off the hogs):**
| Module | Avg | Disaster mode |
|---|---|---|
| LoRa relay/listen | ~0.3–0.5 W | ✅ always |
| GPS | ~0.15 W | ✅ periodic |
| Phone top-up | ~10–15 Wh/charge | ✅ intermittent |
| SDR | ~1–2 W | ⛔ on-demand |
| WiFi AP | ~1–2 W | ⛔ bursts |
| Mic array | ~0.5–1 W | ⛔ off |
| Capture | ~1–2.5 W | ⛔ never |

**Endurance by pack & mode** (kit draw; phone has its own battery):
| Pack | Relay (~0.7 W) | + 1 phone charge/day | WiFi-bubble heavy (~2.5 W) |
|---|---|---|---|
| 10,000 mAh (37 Wh) | ~2 days | ~1 day | ~15 h |
| 27,000 mAh (100 Wh) | ~6 days | ~3.5 days | ~40 h |
| 256 Wh LFP station | ~2 weeks | ~9 days | ~4 days |
| 500 Wh LFP station | ~4 weeks | ~2.5 weeks | ~8 days |

**With sun:** a 10 W panel (~40 Wh/day) > ~17 Wh/day relay draw → **indefinite +
surplus** for a daily phone charge. Battery size only governs the dark stretch.

**Three honest caveats:**
1. **The phone is the real drain** — local-LLM inference + screen burn the
   *phone* fast. In blackout, use the phone in **bursts**; let the kit relay carry
   the load.
2. **Cold wrecks it (Turkey-specific):** the **Feb 2023 quakes were freezing.**
   Near 0 °C, batteries lose **~20–40%** capacity *and lithium can't charge below
   0 °C.* **Derate the table ~30% for winter** and size the LFP pack accordingly.
3. **WiFi-on is "free" only because brief** (~2.5 Wh/hour) — ruinous if left on.

The LLM coordinates duty-cycling (wake to relay, sleep otherwise).

**LoRa hardware (commodity):** **LilyGo T-Beam** ≈ LoRa+GPS+18650 in one board
(BLE→phone, flash Meshtastic); Heltec/RAK/Seeed alternates. **Region-locked:
868 MHz TR/EU**, 915 US, 433 elsewhere (feeds manifest `region`/`tx_allowed`).
**Antenna + placement = cheapest range win**; offer the rooftop **relay-node**.

**Dormancy/reliability (most emergency gear fails here):** LFP low self-discharge
+ float charge; **monthly assistant "kit health" self-test** (battery/LoRa/GPS/
last-mesh-contact); charge paths **solar (MPPT) → car USB → optional hand-crank**;
pass-through powers while charging.

**Composed node:** phone (offline LLM + UI) ⟷ BLE ⟷ kit (`LFP+BMS+solar/MPPT` →
always-on `LoRa+GPS` + on-demand `SDR/WiFi-AP`) in a **vented 3D tray**. Relays a
week+ (indefinite with sun) even with the phone dead.

**Phone is always bundled (not BYO).** A second-hand phone ships *with* the kit
and is the **brain + texting UI + GPS + local LLM** — GSM-dead is fine (no SIM
needed; GPS + WiFi/BLE still work). So the kit core is just **power + radios**;
**GPS lives on the phone**, and the **T-Beam onboard GPS is optional redundancy**
that lets the kit fire an **SOS beacon with location while the phone is
asleep/dead.**

## 5. Skip-OAuth / offline mode (architecture + security boundary)

A real feature with a sharp security edge:
- **Requirement:** full local function — local model, local home/IR control,
  LoRa mesh, SDR — with **no auth, no internet, no Convex/relay**.
- **The boundary:** offline mode is **local-device + local-RF only.** It must
  **never** expose remote control or act-as-user gateway actions without auth.
  Concretely: financial/high-risk gateway acts (assistant MVP §4) stay
  **disabled** offline (no confirm relay, no audit sync) — offline mode is for
  *assist + comms + local home control*, not for moving money. Define this as a
  hard mode gate, not a config toggle.
- Reuses: the local-first tiered inference, `ops_home` local control, the
  consent/gate primitives (which already work local-only).

## 6. All home use cases (what the kit unlocks)

- **AV / place-shift** — TV/satellite/AC via IR + capture; watch home TV on the
  phone (MVP §9 boundary applies). FM/SDR audio streaming for fun.
- **Home control** — lights/plugs/locks/sensors via Zigbee/Thread/Z-Wave/433/BLE
  (`ops_home.go`/`home_store.go`, in flight).
- **Activity/routines** — kumanda activity engine ("good night", "movie").
- **Presence/security** — cameras + sensors; SDR can monitor 433 door/PIR sensors.
- **Energy** — `shelly_power`, EV connectors.
- **In-home voice assistant** — local model; optional walkie endpoint (Bucket 2).
- **Emergency/off-grid** — §4.

## 6.5 Daily-use first; emergency is the *clincher* (vitamin + insurance)

**Sell a daily-useful product; the emergency capability is the reason to *prefer*
it, not its identity.** Positioning = "vitamin + insurance": the daily value
justifies the buy; *"and unlike any other hub, if the towers go down your building
stays connected and you can check everyone's OK"* is the peace-of-mind clincher
that closes it. **Never market "earthquake gear"** (rots in a drawer, hard sell) —
market the most resilient daily tool on the market. The daily use *also* keeps it
charged/updated/paired, so the mesh is always warm and actually ready. Mesh/SOS
rides along for ~$40 of low-power LoRa.

### Shared-resource model (apartman yönetimi / NGO) — the best wedge
**One communal box per building/org**, multi-user, **aidat/org-funded** — shared
infrastructure like the elevator/intercom. **Solves the density problem for free:
every building with one = a neighborhood mesh backbone.** Per-flat economics: a
~$400 box ÷ 20 flats ≈ **$20/flat one-time** + tiny running → trivial on an aidat
line.

**Apartman yönetimi daily (real TR building pains):** announcements (water-cut/
maintenance/meeting/aidat) to residents; **aidat tracking** + receipts
(`invoice_*`/`data_*`); complaints/requests → tracked, assigned to kapıcı; **cargo
notifications**; digital intercom + visitor mgmt; shared cameras (resident-scoped);
shared-resource booking + building utilities (`shelly_*`, water-tank, elevator
status); flat directory (= roster) + voting (`forms`) + kapıcı tasks (`routine_*`).
**Emergency clincher:** the same daily box becomes the building's mesh node +
**liveness roster** ("is everyone OK?") + SOS + neighborhood backbone — already
there, charged, paid for.

**Multi-user/privacy:** residents see **shared** items (announcements, own dues,
roster), **not each other's private data** — Yaver multi-user/teams + guest
scoping in daily (online) mode; emergency falls back to the **PSK mesh** (no
accounts). Same two-mode trust split.

### BOM (rough, commodity ~2026 USD)
| Config | Contents | ~BOM |
|---|---|---|
| **Outdoor solar relay node** (no phone) | T-Beam + LFP + 10 W solar + weatherproof case + antenna | **$90–150** |
| **Daily mesh node** (w/ bundled phone) | + used phone + powered hub + cables + 3D tray | **$220–320** |
| **Daily home hub** | + IR + Zigbee + mic array + speaker + temp/AQ + SDR | **$400–480** |
| **Loaded** | + HDMI capture + FM-TX + 4G + webcam + walkie | **$600–750** |

Caveats: 3D case ≈ $5–10 filament (own printer); **used-phone price is the swing
factor** ($40–120); **Turkey import/VAT +~20–40%**; SDR can replace the FM/AM
tuner (−$15); the power-bank BMS is included (no extra v1 cost).

### Deployment tiers (two-tier mesh, maps to TR neighborhood structure)
- **Home kits = leaf nodes** (daily hubs in apartments).
- **Institutional outdoor nodes = backbone** — **muhtarlık / apartman yönetimi /
  belediye** mount a weatherproof solar relay **high** → the §4.1 elevation
  multiplier turns urban mesh into neighborhood coverage, making every nearby
  home kit useful. Outdoor nodes earn daily keep via **environmental sensing +
  a neighborhood channel**. Pitch as best-effort "neighborhood resilience," not
  a certified emergency-system bid.

## 6.6 Daily use by buyer segment (the device must earn its keep every day)

**The unifying daily loop = a "majordomo": proactive briefing + ask-anything
(read) + act-with-approval (write).** The **daily briefing** is the engagement
engine — a morning digest tailored per role drives people to open the app every
day, which is *also* what keeps the box charged/paired/ready for the emergency
case. Example briefings: **home** "2 fatura yarın son gün, kargon dağıtımda,
yağmurlu, anneannenin doğum günü"; **yönetici** "3 yeni şikayet, 5 daire aidat
geç, su deposu %40, asansör bakımı yarın"; **NGO** "3 saha ziyareti, stokta 40
battaniye, bağışçı raporu cuma"; **muhtar** "2 ikametgah talebi, toplantı 14:00,
1 şikayet (sokak lambası)". Concrete per-segment scenarios below; all run the
same engine (curated pack → selection layer → read-mostly + approval-gated,
role-scoped, vault creds, local audit, wrapping incumbent tools).

**Compute by segment:** home = second-hand phone **4/8 GB** (leaf, cloud-offload);
**muhtarlık/NGO = 16 GB Pi 5 / mini-PC** (always-on backbone, real local 7–14B +
small VLM, hosts shared services, holds the roster + mesh backbone).

**Unifying daily product:** a **private, local-first, self-hosted "mini-cloud +
AI + hub + comms"** — runs daily digital life without Big-Tech cloud; the offline
mesh rides the same box for free. Institutional wedge = **data sovereignty /
KVKK** (resident/beneficiary data stays on their box).

**Muhtarlık (daily):** local **resident registry + request tracking**
(`forms`/`data_*`, KVKK-local); **AI staff assistant** (draft letters/notices,
**translate**, summarize); **mahalle bulletin** (`site_*`/`newsletter_*` + mesh
announcements) with the **roster as resident directory**; **document gen**
(`pdf_render`); **civic environment dashboard** (kit AQ/noise/temp); office IR +
`meeting_*`/`routine_*`; **free local resident↔muhtarlık mesh comms**.

**NGO (daily):** **beneficiary/case mgmt** (local DB, privacy for vulnerable
people); **offline-first field data collection** (`forms` sync over mesh/LAN);
**AI** report/grant drafting + **translation** (refugee/multilingual, voice
STT/TTS); **reporting/finance** (`invoice_*`/`pdf_render`); donor `site_*`/
`newsletter_*`; logistics tables; field coordination over mesh.

**Home (daily):** personal **assistant** (gateway MVP); **home automation**
(IR/Zigbee/BLE, routines); **media/place-shift** (capture); **environment+safety**
sensors; **private home server** (`phone_project_*`/`phone_backend` — calendar/
notes/photos, your data); **family comms** (mesh intercom + roster check-in);
radio-for-fun (SDR/FM); energy (`shelly_*`/EV).

**Two cautions:** (1) **curate per-segment "appliances" (Muhtarlık / NGO / Home
packs)** — don't dump the 500-tool firehose (Cockpit-vs-Workbench by vertical);
(2) **it needs setup/admin** — the institutional 16 GB box must "just work" with a
simple admin UI + Yaver remote-support; not magic-turnkey.

## 6.7 Wrap incumbents (Apsiyon etc.) — read-mostly, approval-gated, MCP packs

**Wrap, don't rebuild.** Each segment already uses an incumbent (apartments =
**Apsiyon**; NGOs = their tools; home = personal apps). Yaver is the **layer on
top**, not a competitor: **AI/NL** ("aidatımı ödedim mi?" → reads Apsiyon),
**resilience** (mesh/roster Apsiyon lacks), **local hub** (IoT/intercom), and
**offline** (cached data readable when the cloud is down). Apsiyon stays the
system of record. **This is the assistant-MVP gateway applied to institutions —
the two halves are one system.**

**Engine ladder (reuses `gateway_registry` / MVP):** Apsiyon **official API
first**; **no API → redroid/web on the 16 GB box** (`droid_*` /
`gateway_redroid_invoke`). Creds in **vault**, acts **as the building account
with the yönetici's explicit consent**, **locally audited**.

**Read-mostly + approval-gated (`gateway_act.go`):** reads = auto/safe (90% of
use); writes = **confirm-gated + audited**, **never auto-write financial**.
**Role-based on the shared box:** residents **read + request**; the yönetici
**approves + writes** (resident "report a leak" → approval item in the yönetici's
inbox). Maps to multi-user/teams + the Approvals inbox.

**MCP packs (appliance, not firehose):** curated connector+verb sets, read-mostly
+ approval-gated — **Apartman** (Apsiyon + IoT + roster + mesh), **NGO** (tools +
`forms`/`invoice_*`/`translate`), **Home** (personal gateway + automation +
media). Net-new vs existing = the **Apsiyon connector**, the **packs**, and
**role-based approval routing**; the gateway/redroid/vault/gate/multi-user stack
already exists.

**MCP selection layer (many MCPs → tight set, the way local models stay
reliable).** Tool-RAG / dynamic selection — the same pattern that lets a harness
expose 1000+ tools, and an extension of Yaver's `gateway_intent` tiered
classifier. Two tiers, **both local**:
- **Catalog → index** (precomputed embeddings + keywords of every tool
  description, ~MBs) → **selection layer** (cheap, local, no big LLM):
  (1) role/config default pack, (2) keyword fast-path, (3) embedding top-K via a
  small 100–400 MB embed model, (4) escalate to cloud LLM only if ambiguous.
- Exposes **~5–15 tools** → the local 7–14B model routes reliably → ACT
  (read-auto / approval-gated) → audit.
- **Selection = the permission + mode boundary too:** surfaces only tools that are
  **relevant ∩ allowed-for-role ∩ allowed-in-mode ∩ consented** — residents see
  read/request only, yönetici sees approve/write, **emergency mode shrinks to
  comms/roster/SOS** (financial/write disabled, §5). Selection and authorization
  are one auditable layer.
- **Caveats:** return **top-K (not top-1)** + a "none matched → ask/broaden" path
  (no silent single-point failure); quality depends on **tool descriptions** —
  the synergy with tight packs (good descriptions help selector *and* model);
  keep **keyword+embedding hybrid** (more robust than either alone).
- Reuses `gateway_intent*` (tiered classifier), `gateway_registry::
  CapabilitiesForMCP()` (catalog), and the `ops` grand-tool router pattern.

**Honest caveats:** (1) prefer the **official API** — UI-automation is fragile
(needs MVP §3 vision-heal) and possibly **against ToS**; read-mostly,
human-cadence, back off on blocks (no scraping swarm). (2) **redroid + local LLM
on a Pi 16 GB is at the edge** → a **mini-PC (x86, 16–32 GB)** suits the
redroid-heavy institutional box better. (3) **Consent + vault + local audit +
approval-gated writes** are load-bearing — you're touching building financial/
resident data via a SaaS you don't own.

## 7. Business — honest verdict

- **Radio/off-grid is a hobbyist + mission wedge, not a mass-revenue line.** Its
  payoff is **community, credibility, and goodwill** — the Meshtastic/SDR/ham
  crowds are passionate and open-source-aligned, perfect for adoption and the
  "inventor→society" story. Don't model it as a profit center.
- **The kit's money** is the same as the TV kit: bundle (premium, modest margin)
  + **recurring metered relay/stream/credits** + the assistant upsell into the
  same installed household. Hardware is the wedge; services are the business.
- **Don't overscope.** Prioritize by value-per-effort:
  - **Tier 1 (build):** offline assistant + **LoRa SOS/mesh** (reuses local
    model; real value; mission-aligned) and **SDR/FM receive + narrate**.
  - **Tier 2:** home-control radio breadth (Zigbee/Thread), place-shift AV.
  - **Tier 3 (novelty):** walkie voice bridge, FM streaming for fun.
  - **Avoid:** anything in Bucket 5; any custom TX hardware pre-PCB.

## 8. Build order + reuse

1. **HW capability manifest** (§1) — detect modules → agent context. Cheap,
   unblocks honest routing. Reuses `gateway_phone_inventory.go` pattern.
2. **Offline/skip-OAuth mode** (§5) with the hard security gate.
3. **LoRa/Meshtastic integration** (§3 Bucket 3) — BLE pairing, send/receive
   text+SOS+GPS, surface in the app.
4. **SDR/FM receive + narrate** (§3 Bucket 1) — `rtl_sdr` decode → local model
   summarizes → stream audio. (SDR decode runs on phone or a Pi Zero, not an MCU.)
5. **3D-printed tray + USB hub + commodity modules** — the physical v1. No
   soldering, no PCB.
6. *(Tier 3 / later)* walkie bridge; *(phase 2)* optional ESP32-S3/nRF52 puck;
   *(phase 3)* custom PCB if validated.

## 9. Legal / regulatory honest lines (summary)

- **TX only on license-free bands** (ISM/LoRa, PMR446/FRS/MURS/CB, WiFi/BLE).
  **RX can be broad** (SDR/FM/AM/weather). **No TX on AM/FM broadcast or any
  licensed band.**
- **Region-gate the bands** — PMR446 (EU) ≠ FRS (US); Z-Wave/LoRa frequencies
  differ by region. The manifest carries `region` + `tx_allowed`.
- **No custom TX hardware before a PCB phase** → inherit module certifications;
  a custom transmitter needs FCC/CE/RED type approval ($$$), which v1 avoids.
- **Ham:** agnostic, never a dependency, no encrypted traffic.
- **Emergency:** best-effort, **not** certified life-safety; never marketed as
  guaranteed.
- **Content + upstream-hardware agnostic; no splitter/stripper; non-inducing**
  (CLAUDE.md streaming rule; MVP §9).
