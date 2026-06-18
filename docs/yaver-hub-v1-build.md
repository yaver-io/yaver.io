# Yaver Daily Home Hub — v1 Build Sheet (2 units, for fun)

> Concrete buildable config of `yaver-hub-kit-radio-offgrid.md`. Goal: assemble
> **2 units** for personal use + a Teknofest-style "Yaver offline case" demo.
> No soldering, no PCB — commodity pluggable modules in a 3D-printed tray.
> Phone is the host/brain; modules connect by USB (powered hub) / BLE / WiFi.

## What it is

A **daily home hub** (assistant + home control + media + radio-RX + FM-TX) that,
when cellular/internet collapse, flips to an **offline solar LoRa mesh node** for
neighborhood text/SOS. Daily use keeps it charged + ready; the disaster mesh
rides along.

## BOM — per unit (rough USD; ×2 for the pair)

| Part | Class / example | ~$ | Connects via |
|---|---|---|---|
| Bundled phone | used Android, 6–8 GB, BLE5, WiFi (USB-OTG capable) | 80–100 | — (host) |
| Powered USB hub | powered 7-port USB3 (the spine) | 25 | USB |
| LoRa node | **LilyGo T-Beam** (LoRa **868** + GPS + 18650) | 40 | **BLE** |
| 18650 cell + 868 SMA antenna | for the T-Beam | 16 | — |
| FM transmitter | commodity micro **FM-TX** (car-modulator class, 3.5mm/USB) | 20 | audio-out |
| IR control | **Broadlink-class WiFi IR** (blast+learn) | 20 | **WiFi** |
| SDR receive | **RTL-SDR v3** (AM/FM/SW/weather) | 35 | USB |
| Far-field mic | USB mic array (UAC) | 35 | USB |
| Speaker | USB speaker (UAC) | 15 | USB |
| Temp/humidity | BLE thermo-hygrometer (Govee/Xiaomi) | 15 | **BLE** |
| HDMI capture | USB capture (UVC) — place-shift | 20 | USB |
| Battery+BMS | 100 Wh / 27,000 mAh **USB-C PD power bank** | 50 | USB-C |
| Solar (offline demo) | 10 W USB panel | 30 | USB |
| 3D tray + cables | filament (~$8 own printer) + adapters | 18 | — |
| **Per unit** | | **~$430–470** | |
| **Pair (×2)** | | **~$860–940** | |

(Zigbee omitted from v1 — see "Android reality" below; use WiFi/BLE smart devices.)

## Assembly (no soldering)

1. 3D-print a **vented** tray with bays for the hub, power bank, T-Beam, and a
   phone dock. (Vent = lithium safety; never seal the pack near heat.)
2. Seat the **powered USB hub** as the spine; plug USB modules (SDR, mic,
   speaker, capture) into it; hub → phone via **USB-C OTG**.
3. FM-TX takes the phone's **audio-out** (3.5mm/USB-C DAC).
4. T-Beam + BLE sensors + Broadlink stay **wireless** (BLE/WiFi) — nothing wired.
5. Power bank feeds the hub + tops the phone (pass-through); solar → power bank.

## Software / flashing

1. **T-Beam → Meshtastic**, region **EU_868**, set a **channel PSK** (your
   family/emergency channel — pre-shared, the no-OAuth identity). Pair to the
   phone over **BLE**.
2. **Phone → Yaver app**: sign in for daily use; configure **offline mode**
   (skip-OAuth, on-device model) for the blackout demo. Enable **HW capability
   manifest** detection (lists IR/SDR/FM-TX/LoRa/sensors present).
3. Phone runs the Yaver Android agent driving the USB modules + BLE/WiFi devices;
   the LoRa node is reached over BLE; messaging is **async store-and-forward**.

## Android reality (the one fiddly part — be honest)

**Plug-and-works on Android** (class-compliant / wireless): BLE LoRa (T-Beam),
BLE sensors, WiFi IR (Broadlink), USB mic/speaker (UAC), USB capture (UVC),
FM-TX (audio-out). Build around these first.

**Needs fiddling / a helper host:**
- **RTL-SDR** works on Android only via specific driver apps; wiring it into the
  Yaver agent is real work → for the demo, run SDR decode on a laptop/Pi Zero, or
  defer.
- **Zigbee USB** wants a Linux stack (zigbee2mqtt) → use **WiFi/BLE** smart
  devices instead in v1, or add a small Pi helper later.

Lean v1 = wireless + class-compliant USB; treat SDR/Zigbee as stretch.

## FM-TX + LoRa — legal note (BTK-verified)

- **LoRa 868 = license-free in Turkey** ✅. Turkey follows the **EU863-870 SRD**
  plan and harmonized with **RED 2014/53/EU** (since Nov 2020); CE-marked
  compliant devices are fine. Use Meshtastic **EU_868**; channel-PSK encryption
  is fine on ISM. (PMR446 / CB are also license-exempt if the walkie path is ever
  added.)
- **FM-TX = the grey one** ⚠️. Turkey's license-exempt (KET) list covers SRD/
  PMR/CB but **does not cleanly include FM-broadcast-band transmitters** (no
  US-Part-15-style allowance). Car FM modulators are widely *used* in practice,
  but strictly it's not a clean exemption → **FM-TX = personal/demo only; do NOT
  ship it commercially** without checking the BTK KET technical spec.

## Phone charge management (don't hold a used phone at 100%)

A docked always-on **second-hand** phone held at 100% cooks its worn battery.
- **v1, no-solder:** pick a used phone with a **native charge-limit / battery
  protection** feature (most 2021+ Androids) → cap at **80%**, let it cycle.
- **Phase-2:** kit-controlled USB power gate — **T-Beam/ESP32 reads phone % over
  BLE, toggles a MOSFET** (charge-to-80 / drain-to-50). Soldering → defer.
- **Vent the tray** (heat is the other battery killer + safety).

## Mesh trust (no cloud)

Offline/mesh mode uses **no Yaver guests, no Convex, no OAuth, no relay** —
membership = **pre-shared channel PSK** (QR in person, pre-disaster), identity =
per-device keypair. See `yaver-hub-kit-radio-offgrid.md` §5.

## The "Yaver offline case" — competition pitch (Teknofest-style)

**"An off-grid AI hub: a daily-use home assistant that, when an earthquake takes
down cellular and internet, becomes a solar LoRa mesh node so neighbors can still
text and send SOS — fully local, no servers, open-source."**

Why it scores:
- **Edge AI** (on-device LLM, works with zero connectivity).
- **Disaster comms** (LoRa async mesh, hold-and-forward, data-mule ferrying) —
  Turkey-relevant post-2023.
- **Open-source + DIY** (publish BOM/3D/image; supporter tier; each unit grows
  the mesh — public good).
- **Honest scope:** best-effort, *not* certified life-safety. Demo = two units
  texting over LoRa with phones in airplane mode + a backbone node "on a roof."

Demo script: both phones **pre-register** (person + home) → **airplane mode**
(simulate blackout) → both tap **"I'm alive"**, then **one goes silent** → the
**liveness roster** shows **1 alive / 1 unknown-at-address** (silence = "go
check," *not* dead) → send text + STT→text "voice note" + **SOS with GPS** over
LoRa → assistant answers **offline** → narrate an SDR/FM emergency broadcast. The
**backbone unit holds the roster** (the muhtarlık node). See roster design in
`yaver-hub-kit-radio-offgrid.md` §4.1b.

## Shopping list (concrete picks, ×2)

- **Phone:** used Android 2021+ with a **native 80% charge-limit** (Samsung
  A-series / Pixel / Sony) — the charge-limit feature is a hard requirement.
- **LoRa:** **LilyGo T-Beam (868 MHz)** + **18650** + **868 SMA antenna** (a
  better antenna is the cheapest range win).
- **Power:** ~**100 Wh USB-C PD power bank** (or a small **LFP power station** for
  the premium/cold-tolerant build) + **10 W USB solar panel**.
- **Hub:** **powered 7-port USB3** (generous power rating — weak hubs brown out).
- **Radios/IO:** **RTL-SDR v3**; **Broadlink WiFi IR**; **USB mic array (UAC)**;
  **USB speaker**; **USB HDMI capture (UVC)**; **BLE Govee/Xiaomi thermo-hygro**;
  commodity **USB/3.5mm FM-TX** (demo only).
- **Enclosure:** 3D-printed **vented** tray (PETG > PLA for heat) + USB-C OTG +
  short cables.

Buy 2 of each except print 2 trays. Flash both T-Beams to the **same channel
PSK** so the pair meshes out of the box.

## Roster sync — CRDT + Meshtastic (codeable spec)

**Meshtastic setup:** region **EU_868**; one **primary channel** with a 256-bit
**PSK** (the family/neighborhood group); share via Meshtastic's **channel QR/URL**
in person (pre-disaster). Modem preset **LongFast** default (or LongSlow for max
range / least airtime). Roster rides a **dedicated app PortNum** (private port) so
it never collides with text.

**Roster CRDT (per-person LWW-Register):**
```
Entry { personId: u32, status: enum{alive,sos}, aliveAsOf: u32(unix),
        homeGeohash: 6–8 chars, deviceId: u32 }   // ~15 bytes
Merge: keep max(aliveAsOf); tie-break by deviceId.   // last-writer-wins
Unknown = NOT transmitted — computed locally when aliveAsOf older than
          threshold (e.g. >2 h) → state "unknown — go check" (silence ≠ dead).
SOS     = separate high-priority message (immediate flood), not a digest field.
```

**Anti-entropy (low airtime, hierarchical):** phones LWW-merge to the **backbone
node** over WiFi/BLE; the backbone holds the full roster and emits **deltas** over
LoRa at a **low rate** (respect EU 868 1% duty-cycle); backbones merge deltas. A
~200 B LoRa packet carries ~10 entries. Priority queue: **SOS > roster delta >
chat**. LLM aggregates/dedupes before injecting (§4.1 load-shaping).

**Privacy:** roster is **local-only, group-scoped (PSK), emergency-armed**
(full reveal only in emergency mode; daily presence stays minimal). Never Convex.

## Honest expectations

- This is a **hobby build**: expect to fiddle with Android USB
  drivers/permissions per module.
- The **phone** (LLM + screen) is the real battery drain — ration in the demo.
- **Cold derates** batteries ~30% (Turkey winter) — size/charge accordingly.
- Daily value carries it; the offline mesh is the bonus that wins the competition.
