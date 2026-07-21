# Yaver — Four-Tier Master Plan

Date: 2026-07-21
Status: **product plan, decided.** Supersedes the pricing sections of
`cloud-workspace-master-plan.md`; that file remains the technical entry point.

Companions: `cloud-unit-economics.md` (measured prices) ·
`cloud-multiprovider-placement-architecture.md` (placement) ·
`cloud-live-wiring-runbook.md` (verification) · `cloud-secrets-and-env.md`

---

## 0. The rule that generates the whole plan

> **Either it parks, or it's shared. Nothing is both dedicated and always-on.**

Every margin failure found while designing this violated exactly that rule:
dedicated relay (16%), dedicated standby (23%), dedicated serverless (14%).
Every fix was the same move — amortise the always-on thing, or make it park.

A second rule follows from it, and drives §3:

> **Default to the smallest machine that serves the default workload.**
> Capacity is opt-in, not opt-out. A user who never says what they're building
> should land on the cheapest box that can still run the common case well.

---

## 1. The four tiers

| Tier | Price | What it is | Cost/user | Margin |
|---|---|---|---|---|
| **Free** | $0 | BYO machine + public relay + capped trial inference | ~€0 | — |
| **Relay Pro** | $9 | shared multi-tenant relay pool, QoS-tiered, always-on | €0.35 | **96%** |
| **Cloud Workspace** | $29 | dedicated 2c/4GB VM, 120 h/mo, **parks** | €7.76 | **71%** |
| **Yaver Serverless** | $9–19 | **shared** always-on host for the user's backend | €2.10 | **75–88%** |

Every tier has a **$0 self-hosted equivalent** (§5). That is the open-source
promise: you can always run it yourself; you pay us for not having to.

### Why each is profitable

- **Relay Pro** — always-on, but amortised 20–50 ways. A relay executes no
  tenant code, so sharing costs nothing in security.
- **Cloud Workspace** — parks. An idle subscriber costs €2.96 (89% margin); one
  who exhausts 120 h costs €7.76 (71%). **Nobody is unprofitable by
  construction**, because the allowance is derived from the margin floor rather
  than guessed.
- **Serverless** — always-on, but shared. Dedicated was ~$95 and unsellable;
  shared is €2.10/user and comfortable at $9.

### The billing model: throttle, don't bill

Flat price + limits that **degrade or stop**, never metered overage. This is the
Claude/Codex model, and it is what this market already accepts. It removes the
usage→invoice pipeline entirely, removes bill shock, and makes the worst case a
number you compute in advance instead of discover on an invoice.

---

## 2. Cloud Workspace — the $29 spec

| | |
|---|---|
| Price | $29/mo (≈ €26.7) |
| Included | **120 h/month of uptime** |
| Default machine | **2c/4GB**, cheapest *available* type (never a hardcoded name) |
| Storage | 20 GB volume, persists across park |
| Network | reserved egress IP — stable address across park/wake |
| Auto-park | **20 minutes** idle, default-on |
| Wake | warm pool, ~60–90 s |
| Exhaustion | park until next period, or degrade to the smallest class |

```
compute   120 h × €0.0368   €4.42
volume    20 GB             €0.88
egress IP                   €1.20
pool share (3/100 users)    €1.26
                            ─────
                            €7.76   →  71%
```

### Machine classes — one price, different burn rates

$29 buys **120 standard-hours**. A bigger box burns the budget faster, so
margin stays constant no matter what the user picks — it cannot be gamed into a
loss.

| Class | Machine | €/h | Burn | Hours from $29 |
|---|---|---|---|---|
| **Default** | 2c/4GB | 0.0368 | **1.0×** | **120 h** |
| Large | 4c/8GB | 0.0673 | 1.8× | 66 h |
| Android / heavy | 8c/16GB | 0.1314 | 3.6× | 33 h |

Burn rates are **derived from `estimateCost()`**, never hardcoded, so they
self-correct when Hetzner pricing or availability shifts.

### The 20-minute park timer is load-bearing

"4 h/day of work" is not 4 h of uptime. Scattered sessions (09:00, 11:00, 14:00,
17:00) keep a box alive through every gap shorter than the timer. At 45 minutes
that user runs 6–8 h/day — **180–240 h/month**, turning 71% into 42% or 27%.

At 20 minutes the gaps close, and wake from a volume is 60–90 s, so the friction
is small. **This single constant protects the tier**; it is not a tuning knob.

---

## 3. Default routing — the opinionated path

A user who says nothing about their stack must still land somewhere good and
cheap. The defaults:

| Decision | Default | Why |
|---|---|---|
| **Machine** | 2c/4GB | Smallest box that runs the default workload well. |
| **Stack** | **React Native + TypeScript** | The flagship path — Hermes push to a real phone is Yaver's differentiator. |
| **Preview** | **Chrome (web dev server) + WebRTC** | Lightweight. No emulator, no Redroid, no GPU. |
| **Device target** | the user's **own phone** via Hermes push | Zero server cost — the device is already theirs. |

### Why 4 GB is enough for the default path

The default loop deliberately avoids the two things that need more memory:

| Path | 2c/4GB |
|---|---|
| RN/Expo dev server + TS language server | ✅ |
| **Hermes bundle → user's real phone** | ✅ (fresh project); ⚠️ monorepo |
| Flutter → classed as a web dev server → browser | ✅ |
| Chrome headless + WebRTC stream | ✅ — passthrough, not encoding |
| **Redroid** (Android in a container) | ❌ needs ~6.5 GB |
| Gradle / Kotlin build | ❌ burst, wants 16 GB |

**Redroid and native Android builds are opt-in, not default.** They exist for
"I need an Android device and don't have one" — but the flagship RN flow pushes
a Hermes bundle to the user's *actual phone*, which costs us nothing and is a
better test anyway.

Metro on 4 GB is fine for a fresh project and tight on a large monorepo. The
class ladder handles that: detect the OOM, suggest the Large class, let the user
opt up. **Do not size every workspace for its worst possible minute.**

### Bursts belong on a shared build host

Hermes and Gradle want lots of memory for two minutes and nothing afterwards —
the worst possible fit for a per-user always-on allocation. Route them to a
**shared build host** so the burst is amortised, exactly like the relay and the
warm pool. `/dev/build-native` is already an agent-side operation, so the seam
exists.

---

## 4. Yaver Serverless

**It cannot share Cloud Workspace hours.** A backend must stay up: 730 h/month
against a 120 h budget would exhaust the month in ~5 days, and a workspace that
parks after 20 min would take the user's deployed app down with it. **Parking
and hosting are mutually exclusive** — that is why it is a separate service, not
a pricing choice.

**Shared always-on host**: `cpx32` €41.99 ÷ ~20 tenants = **€2.10/user** → 75%
at $9, 88% at $19.

Sharing is more defensible here than for a dev box: serverless functions do not
hold the user's mirrored Claude/Codex credentials. It is still tenant code on a
shared kernel, so it needs real isolation work — it is a build, not a config
change.

### Ingress is NOT the relay

| Plane | Path | Who reaches it |
|---|---|---|
| Control (agent ↔ your devices) | Yaver relay | **same owner only**, Convex-enforced |
| Serverless ingress (your app's users) | `<id>.cloud.yaver.io` → box IP, **direct** | the public internet |

Routing customer backends through the relay would break the same-owner
invariant, put production traffic on shared infrastructure, and make the relay a
hard dependency for other people's apps. Relay Pro **supports operating**
serverless, not serving it.

> ⚠️ **Open blocker:** the DNS record is created `proxied: false`, so the box
> must terminate TLS — and today that is a self-signed LAN cert. Public
> serverless needs Let's Encrypt on the box, or `proxied: true` with Cloudflare
> terminating. `custom-domain-tls` exists in the capability enum; it is not
> implemented.

---

## 5. Self-hosted — every tier has a $0 path

| Paid | Self-hosted equivalent | Status |
|---|---|---|
| Relay Pro | run `relay/` yourself — QUIC, password-protected | **already supported** |
| Cloud Workspace | BYO machine, your own provider account/token | **already supported** (`byoMachines`, agent-side vault) |
| Yaver Serverless | run your backend on your own box | **already supported** |

This is deliberate positioning, not a concession. The product is *not having to
operate it*: capacity, wake, parking, egress stability, reclamation, QoS. A user
who enjoys running infrastructure should self-host, and they are still a Yaver
user — the free tier is a real product, not a demo.

It also keeps us honest: if the managed tier is only worth paying for because
self-hosting is artificially crippled, it is not worth paying for.

---

## 6. Inference — never sold

Capped, time-boxed **trial credits only**, so someone with no Claude/ChatGPT
subscription can experience the product once. Three independent reasons it is
never a SKU:

1. No moat — the gateway routes through OpenRouter, itself a thin-margin router.
2. Per-request COGS is unbounded and priced off placeholder rates today.
3. **`claude-code` structurally cannot use the gateway** — it speaks the
   Anthropic wire protocol; the gateway is OpenAI-only. Managed inference can
   never serve the flagship runner.

**Runners are always the user's own subscription**, mirrored from their own
machine to their own box. One Yaver-held subscription serving many users is
resale — termination-grade, never built.

---

## 7. Loss protections

**Live:** fail-closed ordering (credentials → entitlement → quota → spend) ·
auto-park running live even while the wallet meter simulates · wallet-reserve
gate · margin floor enforced in `unitEconomics.ts` · per-attempt reclamation in
the placement ladder · one reclamation path for every satellite (volume, egress
IP, snapshot) · orphan sweep · `past_due` → disable gateway + park.

**Open, and ranked by what they would actually cost:**

1. **Cron host is a single point of failure.** Metering *and* auto-park run from
   one external box POSTing `/crons/run`, with **no alarm**. If it dies you keep
   collecting $29 while costs run unbounded, silently. This disables the single
   best loss protection in the system.
2. **Substitution ceiling is wrong.** The 1.6× multiplier would block the exact
   rescue it was built for (the only available substitute is 4.2×). Must be
   **margin-gated** — "does this still clear the floor at expected hours?" —
   not a price ratio.
3. Metering never bills (moot under throttle-don't-bill, but the caps must work).
4. Gateway hourly cap + per-user credit limit are unbound bindings that fail
   **open**; no input-token ceiling; placeholder COGS rates.

---

## 8. Implementation order

**P0 — correctness**
1. Commit the working branch (67 files, typecheck-clean).
2. Margin-gated substitution (replaces the 1.6× ceiling).
3. Alarm on missing cron ticks.

**P1 — the default path (§3)**
4. Default machine class = 2c/4GB, availability-driven.
5. Default stack = RN + TypeScript when unspecified.
6. Default preview = Chrome/web dev server + WebRTC; Redroid opt-in only.
7. Park timer → 20 minutes.
8. Machine-class burn multipliers derived from `estimateCost()`.

**P2 — the tiers**
9. Shared relay pool (16% → 96%).
10. Warm pool wired (`cloudPoolPlacement.ts`, currently zero callers).
11. Shared serverless host + TLS.

**P3 — later**
12. Multi-provider live wiring · relay QoS classes · shared build host.

---

## 9. Verify before selling

- **Run Redroid on a real box.** It is the tightest fit in the plan and decides
  whether the opt-in Android class needs 8 or 16 GB.
- **Check Metro on 4 GB** with a fresh RN project *and* a monorepo — this
  validates or kills the 4 GB default.
- **Measure real uptime** against the 120 h assumption. It is the single input
  the plan is most sensitive to.
- **Hetzner runbook tiers A→C.** Live probes found three bugs in a single
  afternoon that typechecking never would.

The standing lesson: **inventory is not operation.** Three separate bugs this
session were that shape — a default SKU that did not exist, a declared
capability that returned `[]`, and an orphan sweep whose label filter matched
nothing. Two of them were in code written specifically to prevent that failure
mode.
