# Yaver Edge Fleet — Colocation, Donation & the Phone Datacenter (Deep Analysis)

Last updated: 2026-06-17
Companion docs: [Architecture & build handoff](build-handoff-ws-a-g.md) ·
[Rollout & CapEx](rollout-and-capex.md) ·
[Strategy & reality](strategy-and-reality.md) ·
[Operator fleet](../yaver-public-compute-operator-fleet.md)

> Every code claim verified against the repo on 2026-06-17. € figures are
> **approximate — verify before quoting.** This doc analyzes four ideas and how they
> fuse: (1) one modest "anchor" node, (2) a community-grown **donate** fleet, (3)
> per-user **non-interference**, (4) **secondhand phones as compute**, and the
> business model that ties them: **colocation**.

---

## 1. The central insight: physical single-tenancy

The Android audit verdict is unambiguous: a phone **cannot safely host multiple
unrelated users**. proot (`sandbox_proot.go:187`) is a ptrace-based filesystem view
with `PROOT_NO_SECCOMP=1` — *not* a security boundary; all subprocesses run as one
UID. Android exposes **no cgroups to unprivileged apps**, so there is no CPU/RAM/PID/
disk quota and no privilege separation between tenants. Multi-tenant-on-phone is a
non-starter.

**Colocation turns that blocker into the feature.** If each user's assistant runs on
**that user's own phone**, you never multi-tenant. The phone *is* the tenant boundary:

> **1 phone = 1 user = isolation by physics.** No shared filesystem, CPU, RAM,
> network, or kernel between users — because they are on *different physical devices*.
> This is the strongest non-interference guarantee possible, and it requires zero
> software isolation work.

Everything below follows from this. It simultaneously answers:
- *"other users' data should not interfere"* → different hardware, full stop.
- *"don't invest huge CapEx"* → users bring the hardware.
- *"super efficiently increasable"* → capacity grows one phone per user, funded by the user.
- *"can we use secondhand phones"* → yes, as **single-tenant** nodes, not shared pools.

---

## 2. Three supply models (the synergy)

All three share the same node software (the on-device agent) and control plane; they
differ in who owns the phone and who pays.

| Model | Who owns the phone | Who uses it | Yaver charges | Isolation |
| --- | --- | --- | --- | --- |
| **Colocation (paid, primary)** | the user | the user (their own assistant) | **relay + colo fee** (power, bandwidth, rack, margin) | physical (their device) |
| **Donation (free-tier supply)** | donated to Yaver/pool | a *different* free-tier user | nothing (donor gets credits/goodwill) | physical (1 donated phone → 1 free user) |
| **Anchor + cloud burst** | Yaver | anyone (heavy/browser) | metered/markup | software (VM/container) |

**Colocation is the headline product.** Pitch: *"Mail us your old phone. We rack it,
power it, keep it online 24/7, and it becomes your always-on personal AI assistant —
your device, your data, never leaves it. €X/mo for hosting + relay."* It's a
datacenter colo, but for a €30 phone instead of a €3,000 server. Zero compute COGS to
Yaver, perfect isolation, and the user keeps ownership of device and data (matches the
privacy contract: Yaver never holds their data).

**Donation is the free-tier supply engine.** People with a drawer full of dead phones
donate them; each becomes a dedicated, physically-isolated free-tier node for someone
who can't pay. Capacity scales with community generosity at ~€1/mo electricity per
node. This is the anti-VC supply model: **your user base becomes your supply base.**

**Anchor + cloud** covers what phones can't do (browser streaming, redroid, heavy
compute) — see §4.

---

## 3. Deep feasibility — secondhand phones as datacenter

### 3.1 Why phones are unexpectedly good nodes

- **ARM SoC**: a 2018–2020 midrange phone ≈ a Raspberry Pi 5 or better, 3–6 GB RAM.
- **Built-in UPS**: the battery rides out power blips for free.
- **Very low power**: ~2–4 W active, <1 W idle → **~€1/mo electricity**.
- **Built-in screen** (status/kiosk), WiFi, often LTE (cellular fallback).
- **Cheap/free**: €20–60 secondhand, or free hand-me-downs / e-waste diversion.
- **The node software already exists.** Verified: `SandboxService.kt:99` launches
  `libyaver.so serve --port 18080` as a foreground service with `START_STICKY` +
  `PowerManager.WakeLock` (survives Doze, restarts on OOM-kill). The agent runs shell/
  runner/assistant work on-device through the proot Alpine rootfs. So a phone is a
  *working* Yaver node today, for a single owner.

### 3.2 What workload fits (and what doesn't)

| Workload | On a phone node? | Why |
| --- | --- | --- |
| Light personal assistant: gateway LLM calls, CRUD connectors, voice relay, notifications, scheduled routines, light chromedp/WebView | **Yes** — the target workload | bursty, low-RAM, low-sustained-CPU |
| Headful browser streaming session (WebRTC) | **No** | 2–3.5 GB RAM + sustained encode; thermal throttle |
| redroid / nested Android | **No** | can't nest; needs a Linux KVM host |
| Heavy build/compile, model inference on-device | **No** | thermal + RAM; push to anchor/Hetzner |

So a phone node is a **dedicated, always-on, physically-isolated light-assistant
host** — which is *exactly* the personal-assistant case the whole thread is about.
Heavy work routes to the anchor node or Hetzner (§4).

### 3.3 The hard constraints (honest)

1. **Battery safety is the #1 physical risk — treat it as a fire-safety project.**
   Old lithium cells held at 100% charge, warm, 24/7, stacked → swelling and, worst
   case, **thermal runaway (fire)**. This is non-negotiable for a colo of dozens of
   old phones. Mitigations, in order:
   - **Charge-limit to 60–80%** (battery-protect ROM/app, or smart USB hubs that cut
     at a threshold). Never trickle at 100% forever.
   - **Battery-health screening on intake**; reject any swollen/degraded cell.
   - **Fire-safe racking**: spacing, non-flammable enclosures/LiPo-safe trays, smoke
     detection, away from combustibles; consider per-shelf thermal cutoff.
   - **Option**: run batteryless on regulated DC where the model allows (some phones
     boot without a battery on USB; many need a "battery simulator"). Removes the fire
     vector but is fiddly per-model.
   - **Liability**: get this reviewed before hosting third-party devices at scale.
2. **Android reliability < a real server.** OEM background-kill, forced OS updates,
   Play-policy on "server" apps. For *colo* you control the devices → disable updates,
   pin a known-good build, use a dedicated/kiosk ROM, root if you must for charge
   control. For *user-retained* phones you don't control, expect lower uptime.
3. **Storage wear.** eMMC/UFS has limited write cycles; assistant scratch should live
   in **RAM/tmpfs**, not flash. Minimize disk writes; logs to control plane (metadata
   only) not local flash.
4. **Connectivity.** Phones sit behind NAT/CGNAT → **relay-only** (already the model).
   No inbound LAN listener (ties to the §13.3 I3 `--relay-only` requirement).
5. **Thermal throttling** under sustained load → keep workloads bursty; ventilate the
   rack.
6. **No on-phone container isolation** (§1). Fine for colo/donate (single-tenant); the
   reason you must NOT pool multiple strangers on one phone.

### 3.4 Verdict

Phones are an **excellent single-tenant edge node for light always-on assistants**,
and a **terrible shared multi-tenant pool**. Colocation/donation use them in exactly
the mode they're good at. The single biggest blocker is **battery fire safety**, not
software — and it's an operations/racking problem with known mitigations.

---

## 4. The combined edge topology (one micro-datacenter for < €200)

```text
        ┌──────────────── Anchor node (buy ONE, ≤ €200) ────────────────┐
        │  16–32 GB N100 mini-PC  OR  used 16–32 GB SFF (Dell/HP)        │
        │  roles:  relay + self-hosted TURN  ·  allocator/control        │
        │          ·  heavy/browser sessions  ·  the phone-shelf gateway  │
        └───────────────┬───────────────────────────────┬───────────────┘
                        │ relay (QUIC), jailed            │ LAN/USB to shelf
        ┌───────────────┴───────────┐        ┌───────────┴───────────────┐
        │  Colocated phones (paid)  │        │  Donated phones (free tier)│
        │  1 phone = 1 paying user  │        │  1 phone = 1 free user     │
        │  charge relay + colo      │        │  donor gets credits        │
        │  light assistant, ~€1 pwr │        │  light assistant, ~€1 pwr  │
        └───────────────────────────┘        └────────────────────────────┘
                        │                                 │
                   Hetzner burst (auto-down) for overflow / heavy work
```

- **Anchor node** (the "slightly bigger, not huge" buy, §6) is the brain: relay, TURN,
  allocator, browser/heavy sessions, and the local gateway for the phone shelf.
- **Phone shelf** (colo + donated) is the cheap, physically-isolated, always-on
  assistant capacity that grows one phone at a time at ~€1/mo each.
- **Hetzner burst** (WS-B auto-down) absorbs overflow and the heavy workloads phones
  can't run.

This is a real, growable edge datacenter for **< €200 CapEx**, where capacity scales
via colo fees (user-funded) and donations (community-funded), not your wallet.

---

## 5. Economics (deep)

### 5.1 Per-node cost (approximate)

| Item | Colocated/donated phone | Hetzner CAX11 VM | Anchor node (amortized) |
| --- | --- | --- | --- |
| Hardware CapEx (to Yaver) | **€0** (user/donor brings it) | €0 | €180 once |
| Power | ~€1/mo (3 W) | included | ~€2–3/mo (10 W) |
| Bandwidth | cents (light) + TURN cap | included (20 TB) | home/colo sunk |
| Isolation | **physical (best)** | software (VM) | software |
| Heavy/browser capable | no | yes | yes |
| COGS to Yaver | **~€1–2/mo** | ~€4/mo | ~€3/mo |

### 5.2 Colocation pricing (illustrative)

- COGS per colocated phone ≈ **€1–2/mo** (power + bandwidth + amortized rack).
- Charge **€3–6/mo** (relay + colo + margin) → **margin-positive with zero compute
  COGS**, and the user gets a dedicated always-on assistant on hardware they already
  owned, data never leaving their device.
- **Inference is separate**: BYO model key (free to Yaver) or metered gateway token
  (`gateway_runner_env.go:59` `mintGatewayToken`, key stays in the Worker secret). The
  colo fee is *hosting only* — keep it honest and unbundled.

### 5.3 Free tier via donation

- Donor gives a phone (€0 CapEx to Yaver) → Yaver pays ~€1/mo power to run one free
  user on it + a capped inference budget.
- Free-tier marginal cost ≈ **€1/mo/user + capped inference** — and supply scales with
  donations, not spend. A shelf of 50 donated phones = 50 physically-isolated free
  users for ~€50/mo power + a shared, capped inference pool.

### 5.4 Why this beats both VC clouds and naive self-host

- vs **Browserbase/E2B (VC)**: they buy datacenter capacity; you get capacity donated
  and colo-funded. They can't physically single-tenant at €1/node. Their model *is*
  holding your data; colo *never* holds it.
- vs **naive "rent a VPS per user"**: €4/mo software-isolated vs €1/mo physically-
  isolated for the light case. Phones win on cost *and* isolation for the assistant
  workload.

---

## 6. CapEx answer — "slightly bigger, not huge"

**Buy exactly one anchor node, under €200. Buy no fleet.**

| Option | Spec | Cost (approx) | Notes |
| --- | --- | --- | --- |
| **N100 mini-PC, 16 GB** (recommended) | 4-core N100, 16 GB, 256–512 GB SSD | **~€160–200** | silent, ~6–10 W, hosts relay+TURN+allocator + ~3–5 browser sessions or many light assistants |
| Used SFF (Dell OptiPlex / HP EliteDesk) | i5, 16–32 GB | **~€100–180** | best €/GB secondhand, ~15–30 W, slightly louder |
| N100 mini-PC, 32 GB | as above, 32 GB | ~€250–300 | more headroom; only if you expect browser concurrency |

The anchor node 10×'s your real capacity over the 4 GB boxes (which can't multi-tenant
browsers) and becomes the gateway/brain for the phone shelf. **Then you grow not by
buying more boxes, but by:** colocating users' phones (they pay), accepting donated
phones (community), and bursting to Hetzner auto-down boxes (OpEx). That's the
"super-efficiently-increasable own-hardware tier."

Keep your **3× 4 GB boxes** for relay/TURN (one of them), dev/build, and dogfooding —
exactly as in the rollout plan. The anchor node is additive, not a replacement.

---

## 7. Non-interference — the full guarantee

Two regimes, because not all nodes are single-tenant:

### 7.1 Phone nodes (colo/donate) — physical isolation (strongest)

Different users are on different physical devices. No shared FS/CPU/RAM/network/kernel.
**Non-interference is automatic.** The only requirements:
- **One tenant per phone, ever** (never pool strangers — §1/§3.3).
- **Relay-only, RFC1918 egress blocked** so a phone can't reach the host LAN or other
  nodes (`egress_proxy.go:149` `isPrivateOrReserved` works; add the `--relay-only`
  inbound bind — §13.3 I3).
- **Data-at-rest**: the user owns the device; for colo, encrypt the phone (FBE/FDE) so
  a physically pulled phone reveals nothing. On return/wipe, factory-reset.

### 7.2 Multi-tenant PC/NUC nodes (donated PCs, anchor) — software isolation gate

These *are* shared, so the §13.3 isolation gate applies, and the audit found real gaps
to close (noisy-neighbor + data):

| Gap (audit) | Anchor | State | Fix |
| --- | --- | --- | --- |
| CPU/mem caps | `container_runner.go:274` `--cpus`/`--memory` | works | keep |
| **PID limit** (fork-bomb) | — | **absent** | add `--pids-limit` |
| **Disk quota** (runaway write fills host) | — | **absent** | per-tenant quota / size-capped volume |
| **Disk/blkio IO caps** | — | **absent** | add `--device-read/write-iops` |
| **Network isolation** | defaults `--network host` (`:306`) | **weak** | per-tenant netns, not host |
| **Encrypted per-tenant volume** | — | **absent** | per-session key, destroyed on teardown |
| **Zero-residue teardown** | `host_share_reaper.go:108` kill+`RemoveAll` | partial | add sync-flush + cache purge |

Add these as **isolation items I7 (resource caps) and I8 (encrypted-volume + secure
teardown)** to the §13.3 gate. Until closed, donated *PC* nodes serve owner-test only;
donated/colocated *phones* are fine immediately (physical isolation).

---

## 8. The "Yaver Donate / Colo" tool — architecture & build

Reuses a lot that exists; the net-new is the donor/colo principal, the one-command UX,
and a (trivial, 1:1) allocator.

### 8.1 What exists (verified)
- On-device node: `SandboxService.kt:99` (`libyaver.so serve`), foreground + wakelock.
- Operator mode: `main.go:2146` `--operator` → `httpserver.go:46` `operatorMode`
  (disables paired-token owner fast-path, enables host-share reaper).
- Teardown: `host_share_reaper.go:86/108` (kill + workspace wipe).
- Egress jail: `egress_proxy.go:149` `isPrivateOrReserved` (RFC1918 block).
- Gateway scoped token: `gateway_runner_env.go:59/183` (LLM key never on the node).
- Onboarding scaffolding: `machine_onboarding.go:607` (git/API creds only today).

### 8.2 What's net-new
- **Donor/colo principal** (operator-fleet gap A): a node binds a *scoped service
  identity*, not a person's user token. A leaked node token must not be an account.
- **`yaver donate` / mobile "Donate or Colocate this device"**: one toggle →
  registers the node, sets caps (max RAM/CPU%, schedule "only when charging+idle",
  bandwidth cap, monthly hours), opts in as colo (owner-only) or donation (pool).
- **Heartbeat/health from nodes**: liveness, battery %, temp, charge state → control
  plane (metadata only, privacy-safe). Auto-drain a node that overheats / unplugs.
- **Allocator**: for colo/donate phones it's **1:1** (a phone serves its owner, or one
  assigned free user) — trivially simple. For PC pools, capacity/geo matching (extend
  `project_manifest.go:929` `resolveProjectRuntimeRole`, today owned-only).
- **Billing**: relay + colo meter (reuse the meter framework; new "colo" kind).
- **Donor incentive**: donate-compute → earn Yaver/managed-cloud credits + a "powered
  by N community nodes" page. Turns users into supply.

### 8.3 Proposed workstreams (append to the architecture doc)
- **WS-H — Donor/Colo onboarding**: principal (gap A), `yaver donate` UX, caps/
  schedule, node heartbeat, relay-only bind (I3), colo meter. DoD: a phone joins via
  one toggle, serves exactly its owner over relay, bills colo, and a kill-switch drains
  + factory-resets it.
- **WS-I — Phone-node hardening**: charge-limit integration, tmpfs scratch (storage-
  wear), thermal/battery auto-drain, kiosk build, FBE/FDE for colo. DoD: a phone runs
  a light assistant 24/7 for a week within thermal/battery-safe limits, scratch in
  RAM, survives reboot.
- **WS-J — Multi-tenant resource isolation** (PC pool only): close I7/I8
  (`--pids-limit`, disk quota, blkio, netns, encrypted volume, secure teardown). DoD:
  two tenants on one donated PC cannot starve or read each other; proven by test.

### 8.4 Legal / ethical (CLAUDE.md alignment)
- **Colo = hosting the user's own device & data** — clean; Yaver is a host, not a data
  controller (privacy contract intact).
- **Donation = ownership transfer or explicit loan** — get clear consent terms; donor
  must understand the device is wiped and repurposed.
- **Network jail mandatory** (relay-only + RFC1918 block) so no node can pivot or be
  used to harm third parties (Policy Guard / anti-pivot rules in `CLAUDE.md`).
- **Not proxyware/botnet**: nodes lend *compute for transparent community/owner
  benefit*, never covert IP rental; egress is jailed + policy-guarded; identify
  honestly, back off on blocks.
- **e-waste / ESG story** (real marketing asset): "we keep old phones out of landfill
  and turn them into always-on assistants." Genuinely good, and differentiating.

---

## 9. Risk register (ranked)

| Risk | Severity | Mitigation |
| --- | --- | --- |
| **Lithium battery fire** (24/7 old cells) | **critical** | charge-limit 60–80%, intake health screen, fire-safe racking, smoke detection, possibly batteryless DC; legal review |
| Android reliability / OEM kills | high | controlled devices, pinned ROM, foreground service (exists), START_STICKY (exists) |
| Multi-tenant escape on phones | high | **never multi-tenant a phone** — colo/donate is 1:1 by design |
| Storage wear from churn | medium | tmpfs scratch, minimal flash writes |
| PC-pool noisy-neighbor/data bleed | medium | WS-J (I7/I8) before opening PC pools |
| Logistics cost (shipping/racking) | medium | regional intake; start local; donor drop-off |
| Inference budget overrun (free tier) | medium | hard gateway cap + BYO-key pressure valve |
| Colo legal/liability | medium | clear terms; user owns device+data; insurance for the rack |

---

## 10. TL;DR — the model

1. **Colocation is the headline product**: users mail in old phones; Yaver racks/
   powers/connects them; each becomes the user's own always-on assistant. Charge
   **relay + colo (~€3–6/mo)**, zero compute COGS, **perfect isolation by physics**,
   data never leaves the device.
2. **Donation is the free-tier supply engine**: donated phones each power one
   physically-isolated free user at ~€1/mo power. Supply scales with the community, not
   your wallet. e-waste-positive story.
3. **Phones are great single-tenant nodes, terrible shared pools** — colo/donate use
   them exactly right. Biggest blocker is **battery fire safety**, an ops problem with
   known fixes, not software.
4. **Non-interference is solved by physical single-tenancy** on phones; for shared PC
   nodes, close I7/I8 (PID/disk/IO caps, netns, encrypted volume) first.
5. **CapEx: buy ONE anchor node (≤ €200)** as relay/TURN/allocator/heavy-host +
   phone-shelf gateway. Grow via colo (user-funded) + donation (community) + Hetzner
   burst — not by buying boxes.
6. **The node software already runs on Android** (`SandboxService` → `libyaver.so
   serve`). The build work is the donor/colo principal, one-command UX, heartbeat,
   relay-only bind, and the colo meter (WS-H/I/J).
7. **Trust is the real blocker, not battery or software — see §11.** The fix:
   colocated/donated phones are **wiped clean appliances**, not the user's data-laden
   daily phone, with owner secrets sealed in the phone's secure chip. And the default
   offer is **home-hosting (zero physical trust)** — the phone never leaves the user.

---

## 11. Trust & data sovereignty — the real adoption blocker (and how to clear it)

> "But the old phone has the user's personal data, and they won't trust us simply :)"
> — correct, and this kills naive colocation. The whole §2–§8 model only works if the
> trust story is airtight. Here it is.

### 11.1 Name the two distinct trust problems

1. **Colocation**: the user's phone has *their* data (photos, messages, accounts).
   Mailing it to Yaver = "a third party physically holds my digital life." Hard no on
   faith.
2. **Donation**: the donated phone has the *donor's* data. Putting it in a pool where a
   stranger uses it = "my data could leak to whoever gets my phone." Also a hard no.

Both die if the device keeps personal data and if trust requires *believing Yaver*.
The resolution: **(a) no personal data ever survives intake, and (b) trust the crypto
+ open source, not the company.**

### 11.2 Principle 1 — a colocated/donated phone is a WIPED CLEAN APPLIANCE

The phone is **not** the user's daily driver shipped as-is. On intake it is
**securely wiped and reflashed to a minimal open-source "Yaver Node OS"** before it
ever runs as a node:

- **Cryptographic erase, not "factory reset and hope."** Modern Android (10+) uses
  file-based encryption; a factory reset destroys the encryption keys → prior data is
  cryptographically unrecoverable. On intake, go further: `fastboot -w` (wipe
  userdata) + flash the clean Node OS image. Donor/user data is **gone**, provably.
- The phone now holds **zero** of the original owner's photos/apps/accounts. It is a
  blank appliance whose only job is to run one assistant.
- **For donation** this means the donor's data is destroyed before anyone uses the
  phone — the free-tier user gets a clean node, the donor's privacy is intact.
- **For colocation** this means the user does **not** hand over a phone full of their
  life. They hand over (or Yaver wipes) a blank appliance that *then* runs their
  assistant. The assistant's working data (connector tokens, etc.) is provisioned
  fresh — it is not their old camera roll.

So the visceral objection ("you'll have my photos") is simply false by construction:
**the phone has no personal data on it while it serves.**

### 11.3 Principle 2 — Yaver physically holds only ciphertext it cannot read

Even a clean appliance running the user's assistant holds *assistant* secrets
(connector tokens to their bank/email). The user must trust that Yaver-the-host — with
physical access, or if compromised/seized/insider — cannot read them. Don't ask for
faith; make it cryptographically true:

- **Full-disk / file-based encryption** on the Node OS. A phone pulled from the rack is
  an encrypted brick.
- **Secrets sealed in the phone's TEE / secure element** (Android Keystore StrongBox /
  Titan-M-class). The vault key is **hardware-bound, non-exportable**, and its release
  is **gated on the authenticated owner** (owner auths through the control plane; the
  key unwraps inside the TEE, never in host-readable memory at rest). Yaver-the-host
  cannot extract it.
- **Verified boot + dm-verity + remote attestation**: the running image is provably the
  unmodified, open-source Yaver Node OS — not a tampered build that exfiltrates. The
  user (or their client) verifies the attestation before trusting the node.
- **Open source + CI privacy contract** (`convex_privacy_test.go`): anyone can audit
  that the Node OS and control plane never ship the user's data off-device. Trust the
  audit, not the marketing.

This is **confidential edge computing**: Yaver provides power, cooling, uptime, and
connectivity for a sealed appliance it is mathematically unable to read. That is a
*stronger* privacy posture than any VC cloud (whose business model is reading your
session) — and it's the differentiator, not just a mitigation.

### 11.4 Principle 3 — offer the zero-trust default: home-hosting

For users who won't accept *any* physical trust, don't make them. The same node
software runs **at the user's home** (the old phone in a drawer, plugged in), and Yaver
provides **only relay + control plane**:

- The device **never leaves the user**. There is nothing to trust Yaver with physically.
- Yaver charges for **relay + control plane** (the connectivity/uptime/management
  value), not for holding hardware.
- Trade-off: home internet/power reliability is the user's problem; colo exists
  precisely to solve that for users who *do* want managed always-on.

So the product is a **trust ladder**, user's choice:

| Tier | Where the phone lives | What Yaver holds | Trust required |
| --- | --- | --- | --- |
| **Home-hosting** (default) | user's home | nothing physical; relay + metadata only | **none** |
| **Clean-appliance colo** | Yaver rack | an encrypted, TEE-sealed, attested appliance it cannot read | crypto + open source (not faith) |
| **Donation** | Yaver/pool | a wiped blank node (donor data destroyed) | verified wipe (not faith) |

### 11.5 Honest residual risk

Physical access is powerful. TEE-sealing + verified boot + FDE raise the bar enormously
but are not infinite — a sophisticated attacker with the device, the ciphertext, and
the ability to intercept the owner-auth unwrap is a real (if hard) threat model. The
honest stance: **colo trust = FDE + TEE-bound owner-gated secrets + verified-boot
attestation + open source + CI privacy tests.** Users wanting *zero* physical trust
choose home-hosting. Never market colo as "perfectly secure"; market it as "we host an
appliance we cannot read, and you can verify that." (`CLAUDE.md`: utility, not a
privacy *promise* we can't keep — but here the privacy is cryptographic, not a pledge.)

### 11.6 Build implications (fold into WS-H/I)

- **Intake pipeline**: verified secure-wipe + Node OS reflash + attestation enrollment
  before a phone is ever bound to an account or pool. No node serves un-wiped.
- **TEE key management**: assistant vault key generated in StrongBox/TEE, owner-gated
  release via control-plane auth; never persisted host-readable. (Extends the existing
  vault model, which today derives keys from the auth token — move the colo key into
  hardware.)
- **Attestation endpoint**: client verifies Node OS image + hardware-backed keys before
  trusting a colocated node.
- **Donation consent + wipe certificate**: donor gets a signed "your data was
  destroyed" attestation; explicit ownership-transfer terms.
- **Home-hosting path**: the default onboarding; colo/donation are opt-in upgrades.

### 11.7 TL;DR of the trust story

The phone that serves has **no personal data on it** (wiped clean appliance). What it
*does* hold (assistant secrets) is **sealed in hardware Yaver cannot read** (TEE + FDE +
attestation + open source). And the **default is home-hosting**, where the device never
leaves the user and no trust is required at all. Colo/donation are opt-in tiers for
people who want managed always-on and accept *cryptographic* trust instead of faith.
This isn't a patch on the objection — it's what makes Yaver's edge fleet a stronger
privacy product than any cloud.

---

## 12. Consumer onboarding — preparing a phone without an engineer

Users are not engineers. `fastboot -w` + reflash (§11.2) is a Yaver-side or
power-user step; it must **not** be the consumer path. Here's the reality and the
non-technical flow.

### 12.1 What an in-app button can and cannot do (Android security model)

- **Cannot** "delete personal data but keep apps." A normal app is sandboxed and
  **physically cannot wipe another app's data** (WhatsApp, Photos, Gmail, …). Any
  "selective personal-data cleanup keeping apps" is an *incomplete guided checklist*,
  never a guarantee — **do not offer it as a safety feature** or market it as
  "cleaned for handover." It will miss data.
- **Cannot** trigger a factory reset programmatically *until* the app is a Device
  Owner (`DevicePolicyManager.wipeData` requires device/profile owner).
- **Can** deep-link the user to the built-in reset screen with a guided checklist.
- **Can**, once enrolled as Device Owner, wipe / encrypt / kiosk-lock / remote-wipe.

### 12.2 The key reframe: a wipe is only needed when the phone LEAVES the user

This splits the consumer's "keep my apps / don't delete my stuff" wish cleanly:

| Intent | Path | Wipe? | Engineer? |
| --- | --- | --- | --- |
| "Keep everything, just run an assistant" | **Home-hosting** (default) | **none** | no — install app, tap "host here" |
| "Send my phone to be hosted" (colo) | **Guided factory reset** + QR enroll | yes (crypto-erase) | no — Settings flow + scan QR |
| "Give away my old phone" (donation) | Guided reset + Yaver verified wipe on intake | yes ×2 | no — Yaver does intake wipe |

So **"keep apps" is the home-hosting product** — the phone never leaves the user, the
Yaver node app runs in its own sandbox alongside their apps and never touches their
data, and **no wipe happens at all.** This is the non-engineer default for most people.

### 12.3 The non-engineer "make it a clean appliance" flow (colo/donation)

Entirely on-phone, no PC, no root:

1. **In-app prep checklist** (Yaver app): "Back up anything you want · sign out of
   your Google/Apple/bank accounts · we'll erase this device." Yaver deep-links each
   step where possible.
2. **Guided factory reset.** Yaver deep-links Settings → Reset. On Android 10+ this
   **destroys the file-based-encryption keys → all personal data is cryptographically
   unrecoverable** (the real secure erase, built into Android). Apps go too — correct,
   because the device becomes a dedicated assistant appliance.
3. **QR managed-enrollment.** During the post-reset setup wizard the user **scans a
   Yaver QR** → the phone enrolls via **Android Enterprise / Device Owner** (the
   standard EMM/MDM path). Yaver then auto-installs only the node app, enforces
   encryption, locks the device to **single-app kiosk** (only the node runs), and can
   **remote-wipe** it later. One QR scan — no terminal, no PC, no root.
4. **Attestation enrollment** (§11.3): the clean Node OS/app registers hardware-backed
   keys; the owner's client can verify the appliance before trusting it.

For **donation**, Yaver additionally does a **verified wipe/reflash on intake** as
belt-and-suspenders and issues the donor a signed "your data was destroyed"
certificate — that's Yaver's job, not the donor's.

### 12.4 Build implications (fold into WS-H/I)

- **WS-H** add: an in-app **"Prepare this phone" wizard** (checklist + reset deep-link),
  a **QR managed-enrollment** flow (Android Management API / Device Owner provisioning),
  and a **home-hosting one-tap** path that does *no* wipe.
- **WS-I** add: Device-Owner **kiosk lock** to the node app, enforced FDE, remote-wipe,
  and (donation) the intake verified-wipe + certificate.
- **Never ship** a "delete personal data, keep apps" button as a handover-safety
  feature — it cannot be made complete (§12.1).
