# Yaver Anywhere — Rollout, Hardware & CapEx Plan

Last updated: 2026-06-17
Companion docs:
[Architecture & build handoff](build-handoff-ws-a-g.md) ·
[Strategy & reality](strategy-and-reality.md)

> Bootstrapped, no VC. The plan is cashflow-positive per transaction and spends as
> little CapEx as possible. All € figures are **approximate — verify before
> committing money.**

---

## 0. Inputs & the honest constraint

**Hardware you can allocate now:** 2 laptops @ 4 GB RAM + 1 NUC @ 4 GB RAM =
**~12 GB total, 3 always-on-capable nodes.**

**4 GB is the binding constraint.** Be honest about what fits:

| Workload | RAM needed | Fits on a 4 GB box? |
| --- | --- | --- |
| Yaver Go agent | ~50–150 MB | yes (trivially) |
| Self-hosted relay (Go) | ~50–150 MB | yes |
| Self-hosted TURN | RAM-light, **bandwidth-bound** | yes |
| One headful Chrome session + ffmpeg encode | ~2–3.5 GB | **one at a time, tight** |
| redroid (Android container) | ~2 GB+ each | **effectively no** (1 barely, unstable) |
| Light text/CRUD/gateway assistant (no browser) | ~200–500 MB | ~2–3 per box |
| Multi-tenant browser sessions | 2–3.5 GB each | **no** (not on 4 GB) |

**Conclusion:** your 3 boxes are excellent for **dev, build, an always-on relay+TURN,
dogfooding, and a tiny isolation pilot** — but they **cannot host the "low-hundreds of
free users" tier**. That tier needs 16–32 GB mini-PCs (N100 class). So the question
isn't "use my HW *or* cloud" — it's "use my HW for the roles it's good at, rent cloud
for serving, and only buy bigger HW once demand is proven." That's the recommended
hybrid below.

---

## 1. Two tracks, and the recommended hybrid

### Track A — Cloud-only (no own HW in the serving path)

- All serving on Hetzner; auto-down (WS-B) keeps a bursty assistant at **~€0.4–1/mo**.
- **CapEx €0.** OpEx scales with users; you can grow with a credit card and stop any
  time.
- Self-host TURN on a tiny Hetzner box (~€4/mo) or use the relay box.
- Best when you have **no spare always-on HW** or don't want to babysit hardware.

### Track B — Own-HW in the serving path (your 3 boxes)

- Marginal cost ≈ electricity (~€1–3/box/mo). CapEx €0 (you already own them).
- But 4 GB caps you at **~3 concurrent browser sessions OR ~6–9 light assistants total**
  across the fleet — a **pilot/dogfood scale, not a public free tier.**
- Network-jail + isolation gate (§13.3 of the architecture doc) is **mandatory** before
  any stranger touches your home hardware.

### Recommended — Hybrid (do this)

> **Own HW for the cheap, safe, always-on roles (relay + TURN + dev + dogfood +
> isolation pilot). Rented Hetzner for actually serving users, kept cheap by
> auto-down. Buy bigger HW only after the free tier proves demand.**

The single highest-value use of your existing hardware is **self-hosting relay + TURN**
— it's RAM-light, it directly unblocks WS-A ("Anywhere" off-LAN), and it removes the
TURN bandwidth bill. Do that on day one; it's free leverage.

---

## 2. Box role assignment (your specific 3× 4 GB nodes)

| Box | Role | Why it fits 4 GB | Unblocks |
| --- | --- | --- | --- |
| **Laptop 1** | **Always-on relay + self-hosted TURN** (`relay/` + pion TURN; set `YAVER_TURN_URL`, `TURN_AUTH_SECRET`) | relay/TURN are RAM-light, bandwidth-bound | **WS-A** for free — TURN without a cloud bill |
| **Laptop 2** | **Dev + build machine** + one dogfood remote-session runtime (1 browser at a time) | one headful Chrome fits in 4 GB | **WS-E** dogfood; build/test everything |
| **NUC** | **Always-on personal-assistant host** (your own assistant, `--operator` dogfood, 1–2 light tenants) | light CRUD/gateway assistants are ~300–500 MB | **WS-G** isolation pilot (I1–I4) on real HW, owner-only first |

This costs **€0 extra**, runs ~€3–9/mo total electricity, and advances WS-A, WS-E, and
WS-G simultaneously with hardware you already have.

---

## 3. Phased rollout (mapped to workstreams + spend)

Timeline is effort-ordered, not calendar-promised (solo dev). Spend column is the
*new* money at each phase.

### Phase 1 — Foundation · spend €0
- Laptop 1 → relay + TURN online; set the env vars; verify `/stream/webrtc/ice`
  returns the TURN entry.
- **WS-A**: wire `iceServersForPeer()` into `remote_runtime_webrtc.go:257`; verify a
  session from your phone on cellular reaches Laptop 2 over **your own TURN**.
- **WS-C**: capture + publish one Hetzner arm64 snapshot; sync the ID into
  `cloud-images.json`.
- **Exit:** off-network interactive session works; instant-provision image exists.

### Phase 2 — Beta product · spend ~€4 (one Hetzner test box, hours only)
- **WS-E**: commit + harden the Remote Session MVP; dogfood on Laptop 2.
- **WS-B**: auto-down schema + idle sweep; test snapshot→delete→recreate on one
  Hetzner box (auto-down means the test box costs cents).
- **Exit:** beta managed-browser session that auto-downs and is cost-controlled.

### Phase 3 — Private alpha · spend ~€5–15/mo OpEx
- **WS-F**: doorman (`POST /wake` Convex httpAction); wake-from-snapshot ~60 s.
- Invite **3–10 friendly users.** Serve them on Hetzner (auto-down → ~€1/user) or 1–2
  on the NUC.
- **WS-G I1–I4**: build + *prove* isolation on the NUC in `--operator` mode —
  ephemeral per-tenant container, `--relay-only` bind, RFC1918 egress block,
  zero-residue teardown. **Owner-test accounts only** until I1–I4 pass.
- **Exit:** real users on a cost-controlled, off-network product; isolation proven.

### Phase 4 — Open the free tier (gated) + CapEx decision · spend: decision point
- Once **I1–I4 pass**, open the free tier to low-tens of users.
- Run free serving on **Hetzner first** (OpEx, no CapEx). Your 3 boxes handle relay/
  TURN/dev/dogfood + a couple of free pilots.
- **CapEx trigger (§4):** only consider buying mini-PCs when free-tier demand is steady
  and OpEx would exceed the buy-back window.
- **WS-D**: flip metering live (cloud meter first, then managed); start converting free
  → BYO-key or paid.
- **Exit:** free on-ramp live and gated; paid path proven on a non-owner account.

### Phase 5 — Commercialize · self-funding
- Package **AI Agent Runtime** with human-takeover, **control-plane pricing on
  BYO-compute** (zero COGS), self-hosted license + SLA. Free tier becomes the funnel.

---

## 4. CapEx plan — what to actually spend, and when

**Default stance: don't buy hardware. Rent Hetzner, use the 3 boxes you have.** Reasons:
no CapEx risk, scales with a card, auto-down keeps it cheap, and you avoid babysitting
hardware while you should be shipping.

### CapEx tiers (spend only when the trigger fires)

| Tier | Trigger | Buy | Cost (approx) |
| --- | --- | --- | --- |
| **0 — now** | building / alpha | nothing; use the 3 owned boxes | **€0** |
| **1 — proven product** | WS-A/E work + 5–20 wanting in | nothing yet — rent 1–2 Hetzner boxes | **€0 CapEx**, ~€8–20/mo OpEx |
| **2 — free tier has demand** | steady free demand **and** Hetzner OpEx > ~€30/mo for **3+ consecutive months** | **1–2× N100 mini-PC, 16–32 GB** | **~€180–250 each** |
| **3 — scale free** | the free tier is the funnel and converts | **2–3× N100/32 GB** total fleet | **~€500–750 total** |

### Buy-vs-rent breakeven (the math behind Tier 2)

- A 32 GB N100 mini-PC ≈ a Hetzner CPX41/CX42-class box (~€16–30/mo rented).
- Buy one N100/32 GB for ~€220 → breakeven vs renting one ≈ **8–14 months**.
- So: **rent until you're confident you'll run the fleet > ~1 year**, then buying wins
  on cash — but factor your time, reliability, and home upload bandwidth (TURN/stream)
  into "wins."

### What 4 GB boxes are NOT worth buying for
Don't buy more 4 GB hardware. It can't host browser sessions multi-tenant. If you buy,
buy **16–32 GB N100-class** — that's the free-tier fleet unit (dozens of bursty users
each).

---

## 5. OpEx model by stage

| Stage | Serving | Monthly OpEx (approx) |
| --- | --- | --- |
| Alpha (3–10 users, auto-down) | Hetzner bursty + own relay/TURN | **~€5–15** + ~€3–9 electricity |
| Free tier (low-tens, gated) | Hetzner + capped shared inference | **~€15–40** + electricity |
| Free tier (low-hundreds) | N100 fleet **or** Hetzner | HW: ~€10–30 elec · Cloud: ~€40–120 |
| Paid / BYO | customer's compute or metered Hetzner | **margin-positive** (markup or control-plane fee) |

**The dominant variable cost at every free stage is LLM inference, not compute.** Cap
it hard (gateway scoped token) and push users to BYO-key. Orchestration on own HW or
auto-down Hetzner is the cheap part.

---

## 6. Decision triggers & kill criteria

- **Spend CapEx (Tier 2)** only when: isolation gate passed + steady free demand +
  Hetzner OpEx > €30/mo for 3 months. Otherwise keep renting.
- **Open the free tier** only when WS-G I1–I4 pass on a real node (owner-test first).
- **Pull the free tier** if: inference budget overruns the cap, abuse appears, or
  conversion to BYO/paid is near zero after a fair window. It's an acquisition tactic
  with a built-in off-ramp, not a permanent commitment.
- **Stop and reassess** if WS-A (TURN) can't deliver reliable off-network sessions —
  that's the load-bearing assumption for the entire "Anywhere" thesis.

---

## 7. TL;DR

1. **Use your 3 boxes now, buy nothing.** Laptop 1 = relay+TURN (free WS-A leverage),
   Laptop 2 = dev + dogfood, NUC = assistant host + isolation pilot.
2. **Serve real users on rented Hetzner**, kept cheap by auto-down (~€1/bursty user/mo).
3. **4 GB can't run the public free tier** — it's pilot/dogfood scale. The free tier
   needs 16–32 GB N100 nodes.
4. **Only buy HW (N100/32 GB, ~€220 each) after** isolation passes *and* demand is
   proven *and* Hetzner OpEx is steady > €30/mo for 3 months. Until then, rent.
5. **Inference is the only real cost** — cap it, push BYO-key. Compute is nearly free
   either way.
6. **Isolation is the gate** for anything on your own hardware. No strangers until
   I1–I4 pass.
