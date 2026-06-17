# Yaver Anywhere — Decisions, Rationale & Open Questions

Last updated: 2026-06-17 · Part of the [handoff package](README.md)

Captures every decision made in the 2026-06-17 working session, the reasoning, and what
remains open. Read alongside [ARCHITECTURE.md](ARCHITECTURE.md).

---

## How we got here (the design arc)

1. Started from an aspirational 9-phase "Anywhere runtime" plan. Ground-truthed it
   against code (4 parallel audits) → the fabric is ~80% built; the plan oversold scope
   and undersold existing capability. Rewrote it as reality-anchored.
2. Reframed the product: not remote-desktop / cloud-browser / device-farm (each has a
   focused OSS competitor) but **the control plane over heterogeneous compute** — moat =
   breadth + human-approval + data-never-held.
3. Constraint: **bootstrapped, no VC** → sell control plane, not compute.
4. Hardware question → 2 laptops + 1 NUC @ 4 GB each → too small to multi-tenant
   browsers; good for relay/TURN/dev/dogfood.
5. "How far can free go?" → own-hardware fleet economics → donate-to-grow idea.
6. "Phones as datacenter?" → audit says phones **can't** safely multi-tenant.
7. **Colocation insight** resolved it: 1 phone = 1 user = isolation by physics. This
   also answered CapEx, supply scaling, and non-interference at once.
8. **Trust objection** ("won't trust us with my data") → wiped clean appliance + TEE +
   home-hosting default → turned the objection into a differentiator.
9. **Non-engineer onboarding** → guided factory reset + QR Device-Owner enrollment;
   "delete data, keep apps" is impossible (Android sandbox) → home-hosting *is* the
   "keep everything" path.
10. Packaged everything as this nested handoff.

---

## Decision log

### D1 — Position as a runtime control plane, not a point tool
**Why:** can't out-feature RustDesk/Browserbase/E2B on their single axis; the union
(heterogeneous targets + agent-native + human approval + data-never-held) is the
defensible, un-clonable thing. **Status:** adopted.

### D2 — Monetize by selling the control plane, not compute (no VC)
BYO/own/donated/rented compute; charge a control-plane fee + metered markup + hosted
TURN + license/SLA. Zero inventory capital. **Status:** adopted. **Off path:** OEM/Togg
(long cycle) — keep in deck, not critical path.

### D3 — License: keep the existing FSL-1.1-Apache-2.0 core + Apache-2.0 SDK split
Already implemented; fixed the stale README AGPL badge → FSL. **Status:** done (badge
edited).

### D4 — The five product gaps are the whole game
TURN-interactive, auto-down, publish-image, metering-live, commit-MVP. Four are
finishing, not greenfield. **Status:** adopted as WS-A…E.

### D5 — "Down" means snapshot+delete, never power-off
Hetzner bills stopped servers; only deletion stops billing. **Status:** adopted (WS-B).

### D6 — Doorman = Convex httpAction
Relay is QUIC pass-through (doesn't talk to Convex); the gateway Worker is
inference-only; Convex already holds lifecycle mutations and is always-on/free.
**Status:** adopted (WS-F).

### D7 — Cut car / TV / watch-meeting / signage from the critical path
They ride the same session object; build when a buyer is in hand. **Status:** adopted.

### D8 — Buy ONE anchor node (≤ €200), not a fleet
16–32 GB N100 / used SFF. 4 GB boxes can't multi-tenant browsers. Grow via colo +
donate + Hetzner burst, not by buying boxes. **Status:** recommended. **Trigger to
spend:** isolation gate passed + steady demand + Hetzner OpEx > €30/mo for 3 months.

### D9 — Box roles for the existing 3×4 GB machines
Laptop-1 = relay + self-hosted TURN (free WS-A leverage); Laptop-2 = dev/build +
single dogfood session; NUC = always-on assistant + `--operator` isolation pilot.
**Status:** recommended.

### D10 — Phones are single-tenant edge nodes, never shared pools
proot is not a security boundary; no unprivileged cgroups. **Status:** adopted →
colocation/donation are 1:1 by design.

### D11 — Colocation is the headline edge product
User's own (wiped) phone, Yaver-hosted, runs their assistant; charge relay + colo
(~€3–6/mo, ~€1 power COGS); perfect isolation; data never leaves the device.
**Status:** adopted.

### D12 — Donation is the free-tier supply engine
Donated phones (data destroyed on intake) each power one physically-isolated free user
at ~€1/mo; supply scales with the community; donor earns credits; e-waste-positive.
**Status:** adopted.

### D13 — Inference is the only real variable cost → cap it / BYO-key
Orchestration on own HW or auto-down Hetzner is ~free; LLM tokens are the budget.
Gateway mints scoped tokens, key stays in the Worker secret. Free tier = capped shared
inference + BYO-key pressure valve. **Status:** adopted.

### D14 — Isolation is a hard gate, not a follow-up
Free tier serves owner-test accounts only until I1–I4 pass (ephemeral per-tenant
container, no paired-token=owner, relay-only bind, zero-residue teardown). **Status:**
adopted (WS-G §13.3).

### D15 — Trust by crypto, not faith; home-hosting is the zero-trust default
Wiped clean appliance (no personal data) + TEE-sealed owner-gated secrets + FDE +
attestation + open source. Home-hosting (phone stays with user) needs no trust.
**Status:** adopted.

### D16 — Do NOT ship "delete personal data, keep apps"
Android sandbox makes it impossible to do completely → can't be trustworthy for
handover. "Keep everything" = home-hosting (no wipe). Safe-for-handover = guided
factory reset + QR enroll. **Status:** adopted (edge-fleet §12).

### D17 — Consumer onboarding is non-engineer, on-phone, no PC
Guided factory reset (built-in crypto-erase) + QR Device-Owner enrollment (Android
Enterprise). 2C is a spike-first. **Android-only** (iOS can't be a headless appliance).
**Status:** adopted (WS-H/I, Ticket 2).

### D18 — Multi-tenant PC nodes need resource isolation before opening
Add `--pids-limit`, disk quota, blkio, per-tenant netns (today only cpus/memory), +
encrypted per-tenant volume + secure teardown. **Status:** adopted (WS-J / I7–I8).

---

## Open questions (need a call)

| # | Question | Default lean |
| --- | --- | --- |
| Q1 | First paid package: AI Agent Runtime or Cloud Browser? | **AI Agent Runtime** (most differentiated, least capital) |
| Q2 | Linux cloud-browser display: Xvfb, Wayland, or native headful? | Xvfb first (simplest) |
| Q3 | First audio stack: PulseAudio or PipeWire? | PipeWire (modern) — verify on the image |
| Q4 | Are private meeting URLs ever allowed in Convex metadata? | No — local-only |
| Q5 | Android enrollment: Android Management API vs DIY Device Owner? | **Spike first** (Ticket 2C) |
| Q6 | Colo battery safety: charge-limit vs batteryless DC? | Charge-limit 60–80% + fire-safe rack; legal review |
| Q7 | Buy anchor node now or rent Hetzner until proven? | **Rent until proven** (D8 trigger) |
| Q8 | Operator/donor principal design (scoped service identity) | Required before any stranger traffic; design in WS-H |

---

## Risks carried forward (top 3)

1. **Lithium battery fire** on a colo of old phones (critical) → charge-limit, intake
   health screen, fire-safe racking, smoke detection, legal review.
2. **TURN reliability off-LAN** — the load-bearing assumption for "Anywhere." If WS-A
   doesn't deliver reliable off-network sessions, stop and reassess.
3. **Isolation regression** — never open free/colo/donation to strangers before the
   gate (I1–I4) passes; never multi-tenant a phone.

---

## What was actually changed in the repo this session

- Rewrote `strategy-and-reality.md` (was the aspirational plan).
- Created `build-handoff-ws-a-g.md`, `rollout-and-capex.md`, `edge-fleet-colo-donate.md`,
  `coding-tickets.md`.
- Created this package: `README.md`, `ARCHITECTURE.md`, `DECISIONS.md`; moved the five
  docs under `docs/yaver-anywhere/` and fixed cross-links.
- Edited `README.md` (root): license badge AGPL → FSL.
- **No code committed or pushed.** Pre-existing uncommitted code referenced
  (`ops_remote_session.go`, `RemoteSessionView.tsx`, `gateway_intent_model.go`, etc.)
  was audited, not modified.
