# Yaver — Four-Tier Architecture: Deep Analysis

Date: 2026-07-21
Status: **design of record.** Long form. `yaver-four-tier-master-plan.md` is the
summary; this is the reasoning, the numbers, the failure modes, and the
file-level implementation plan.

All prices measured live from the production Hetzner account on 2026-07-21.
Nothing here is estimated unless it says so.

---

# PART I — FOUNDATIONS

## 1. The two generating rules

Everything below is derived from two rules. If a future change violates either,
it is wrong regardless of how good it looks.

### Rule 1 — Either it parks, or it's shared

> **Nothing is both dedicated and always-on.**

Every margin failure encountered while designing this violated exactly that:

| Configuration | Margin | Violation |
|---|---:|---|
| Relay Pro on a dedicated box | 16% | dedicated + always-on |
| Standby park on a dedicated box | 23% | dedicated + always-on |
| Serverless on a dedicated box | 14% | dedicated + always-on |
| Workspace 24/7 on `cpx32` | −95% | dedicated + never parks |

And every fix was the same move:

| Fix | Margin | Mechanism |
|---|---:|---|
| Relay Pro on a shared pool | 96% | amortise |
| Workspace with 20-min auto-park | 71% | park |
| Serverless on a shared host | 75–88% | amortise |

**A quota cannot rescue an always-on cost.** A quota limits *active* hours; an
always-on cost is incurred at zero hours. When something fails the margin floor,
the answer is always to amortise it or make it park — never to lower the floor,
and never to add a cap.

### Rule 2 — Default to the smallest machine that serves the default workload

> **Capacity is opt-in, not opt-out.**

A user who says nothing about their stack must land on the cheapest box that
still runs the common case *well*. Sizing every workspace for its worst possible
minute is how a 71% tier becomes a 42% tier.

The corollary that makes this safe: **detect the ceiling and offer the upgrade**,
rather than pre-provisioning for it.

---

## 2. Measured input costs

Gross €, `fsn1`, pulled from the live account. Availability is the column people
forget, and it is the one that breaks things.

| Type | c / RAM / disk | €/hour | €/month | EU availability |
|---|---|---:|---:|---|
| cx23 | 2 / 4 / 40 | 0.0104 | 6.49 | **sold out** |
| cx33 | 4 / 8 / 80 | 0.0160 | 8.99 | **sold out** |
| cx43 | 8 / 16 / 160 | 0.0296 | 18.49 | **sold out** |
| cax11 | 2 / 4 / 40 (arm) | 0.0112 | 6.99 | **sold out** |
| cax21 | 4 / 8 / 80 (arm) | 0.0200 | 12.49 | **sold out** |
| **cpx22** | **2 / 4 / 80** | **0.0368** | **22.99** | ✅ available |
| **cpx32** | **4 / 8 / 160** | **0.0673** | **41.99** | ✅ available |
| cpx42 | 8 / 16 / 320 | 0.1314 | 81.99 | ✅ available |
| cpx51 | 16 / 32 / 360 | 0.1338 | 83.49 | sold out |
| ccx13 | 2 / 8 / 80 (ded.) | 0.0809 | 50.49 | ✅ available |

Plus: **Volume €0.044/GB/month** · **reserved primary IPv4 ≈ €1.20/month**.

### 2.1 Three facts in that table that changed the design

**Size is not a proxy for price.** `cpx32` has the same cores and RAM as `cx33`
and costs **4.2×**. Any selector that optimises for "smallest sufficient" instead
of "cheapest sufficient" silently inverts the margin. This was a real bug in the
availability-substitution code.

**Every SKU Yaver was configured to use was sold out in every EU datacenter.**
`cx32`/`cx42`/`cx52`/`gex44` were the defaults — and `cx32`, `cx42`, `cx52`,
`gex44` **do not exist at all** in the catalogue. The shared Intel line is
cx23/cx33/cx43. Every paid provision would have failed at server-create, *after*
creating and paying for the volume.

**Hetzner has exactly one datacenter per location** (`nbg1-dc3`, `hel1-dc2`,
`fsn1-dc14`, `ash-dc1`, `hil-dc1`, `sin-dc1`). So "try another datacenter in the
same location" is a no-op. The only intra-location lever is SKU substitution, and
the volume pins the location.

### 2.2 What this means for wake

Park is **delete-not-stop** (Hetzner bills stopped servers; only delete stops the
meter). So a wake must **order a new server** — and nobody guarantees on-demand
capacity. Hetzner has **no reservation product at all**.

A parked workspace is therefore a bet that the market has room when the user
returns. On 2026-07-21 that bet would have lost on the configured SKU and won on
a substitute. Hence §7.

---

# PART II — THE FOUR TIERS

## 3. Tier summary

| Tier | Price | Model | Cost/user | Margin |
|---|---|---|---:|---:|
| **Free** | $0 | BYO machine + public relay + trial inference | ~€0 | — |
| **Relay Pro** | $9 | shared multi-tenant relay pool | €0.35 | **96%** |
| **Cloud Workspace** | $29 | dedicated 2c/4GB VM, 120 h, parks | €7.76 | **71%** |
| **Yaver Serverless** | $9–19 | shared always-on backend host | €2.10 | **75–88%** |

Margin floor **55%** (hard refuse), target **70%**.

---

## 4. Tier 1 — Free

**What it is:** BYO machine + public relay + capped trial inference.

**Cost to us:** effectively zero. The BYO plane uses the user's own provider
token, stored encrypted on their own agent, never sent to Convex
(`http.ts:6520-6533`). The public relay is one shared box already running.

**Why it exists:** it is the funnel, and it is a real product. A developer with
their own machine and their own Claude subscription gets genuine value — remote
access from a phone, hot reload, Hermes push — at no cost to either side.

**Trial inference** is the only free thing that costs money. Non-negotiable
properties, each because the corresponding control was found missing:

| Property | Why |
|---|---|
| Prepaid, fail-closed | A trial user cannot be billed after the fact |
| Hard **input** and output token ceilings | `index.ts:290` estimates worst-case from the *output* clamp and has no input bound at all |
| Time-boxed | An unexpiring trial is a free tier by another name |
| One per verified identity | Otherwise it is a faucet — audit `mergeUserInto` for farming |
| Framed as a **loan** | So conversion does not read as a takeaway |

**Trials get a tightly-constrained VM.**

> ⚠️ **SUPERSEDED 2026-07-21** — see `yaver-activation-trial-analysis.md`.
> This paragraph originally read "trials get no VM". That was correct for an
> OPEN-ENDED trial box and does not hold for the constrained one: 60-minute
> hard TTL, **no inbound ports** (Yaver's transport is already
> outbound-registered, so nothing needs to listen), ephemeral (no volume, no
> reserved IP, no snapshot — hence no satellites to leak), egress-policed, and
> one per verified identity. Measured cost €0.037 per trial; CAC €0.37–1.84
> against €26.7/mo recurring.
>
> The warning below still stands and is why the trial is monitored rather than
> assumed safe.

A free VM with a public IP is the classic abuse vector, and
the consequence is not a lost trial — a datacenter IP hammering third parties
gets **the entire provider account suspended**, paying customers included.
That is why the trial has no inbound ports, blocks SMTP and RFC1918, rate-limits
egress, and ships with a kill switch and an egress-anomaly alert from day one.

---

## 5. Tier 2 — Relay Pro ($9)

### 5.1 Why shared is safe

The relay is **pass-through**: it forwards ciphertext, authorizes nothing, and
**executes no tenant code**. Cross-tenant isolation is enforced in Convex *before*
any bridging:

- Signature path: `relay/server.go:1706` → `authorizeProxyViaSig:751` →
  `resolveSigViaConvex:681` → `backend/convex/devices.ts:2584` —
  `if (String(signer.userId) !== String(target.userId)) return deny`
- Password path → `userSettings.ts:757-765` — `device_mismatch`
- Registration collision refused for a different owner —
  `relay/server.go:1063,1088`

And **free vs Pro is explicitly NOT a security boundary** — Pro buys capacity,
nothing else. So sharing costs zero security and multiplies margin by six.

### 5.2 Economics

| Model | Cost/user | Margin |
|---|---:|---:|
| Dedicated box today (`cax11` €6.99, always-on) | €6.99 | **16%** |
| Shared, 20 tenants/box | €0.35 | **96%** |
| Shared, 50 tenants/box | €0.14 | **98%** |

20 tenants already clears everything, so there is no need to oversubscribe —
which matters, because chasing the last 2 points converts a margin win into an
outage.

`provisionRelay.ts` currently provisions a **dedicated box per subscriber**. That
is the single biggest fixable margin leak in the product.

### 5.3 QoS on shared hardware

The scarce resource is **bandwidth, not CPU** — a `cpx12` has ample CPU for
pass-through. What you actually meter is Hetzner's ~20 TB/month traffic
allowance.

Two traffic classes, because the workloads want opposite things:

| Class | Examples | Treatment |
|---|---|---|
| **Latency-sensitive** | WebRTC signalling, control, SSH | prioritise |
| **Bulk** | Hermes bundle push (≤50 MB, already capped) | rate-limit |

Per-tenant ceiling + guaranteed floor so free traffic cannot starve Pro. Cap
concurrent streams.

**Two blockers before QoS means anything:**

1. The SSH splice lane **bypasses `RecordBytes` entirely** (`relay/server.go:1948`
   → `:2119`). You cannot tier what you do not measure — and today that lane is
   also uncapped free egress on shared infrastructure.
2. Tier is not plumbed to the relay: `validateRelayAccessE` resolves a userId but
   not a plan.

### 5.4 Private Relay

Dedicated relay box, sold separately at **≥$19** — priced to actually cover an
always-on machine. For customers who want isolation for compliance reasons, not
as the default shape of Relay Pro.

---

## 6. Tier 3 — Cloud Workspace ($29)

### 6.1 Specification

| | |
|---|---|
| Price | $29/mo (≈ €26.7) |
| Included | **120 h/month of uptime** |
| Default machine | **2c/4GB**, cheapest *available* type |
| Storage | 20 GB volume, persists across park |
| Network | reserved egress IP (stable across park/wake) |
| Auto-park | **20 minutes** idle, default-on, opt-out |
| Wake | warm pool, ~60–90 s |
| Exhaustion | park until next period, or degrade to smallest class |

```
compute   120 h × €0.0368   €4.42
volume    20 GB × €0.044    €0.88
egress IP                   €1.20
pool share (3 spares/100)   €1.26
                            ─────
                            €7.76   →  71%
```

### 6.2 The margin curve — why parking *is* the business model

Same box, different uptime:

| Active hours | Cost (2c/4GB) | Margin |
|---:|---:|---:|
| 0 (idle subscriber) | €3.34 | **87%** |
| 40 | €4.81 | **82%** |
| 120 (the cap) | €7.76 | **71%** |
| 240 (timer too loose) | €12.17 | 54% |
| 730 (never parks) | €30.20 | **−13%** |

On the Large class (4c/8GB) the same 730 h is €52.09 → **−95%**.

**Scale-to-zero is not an optimisation; it is the entire business model.**

### 6.3 The 20-minute park timer is load-bearing

"4 h/day of work" is **not** 4 h of uptime. A user working at 09:00, 11:00,
14:00 and 17:00 keeps the box alive through every gap shorter than the timer:

| Timer | Realistic uptime | Monthly | Margin |
|---|---|---:|---:|
| 45 min | 6–8 h/day | 180–240 h | 42–54% |
| **20 min** | ~4–5 h/day | **120–150 h** | **65–71%** |

Wake from a volume is 60–90 s, so the friction is small. **This constant
protects the tier.** It is not a tuning knob, and it must not be quietly relaxed
for UX reasons without re-running the numbers.

### 6.4 Machine classes — one price, different burn rates

$29 buys **120 standard-hours**. A bigger box burns the budget faster, so margin
stays roughly constant no matter what the user picks — it cannot be gamed into a
loss.

| Class | Machine | €/h | Burn | Hours | Margin at cap |
|---|---|---:|---:|---:|---:|
| **Default** | 2c/4GB `cpx22` | 0.0368 | **1.0×** | **120** | 71% |
| Large | 4c/8GB `cpx32` | 0.0673 | 1.8× | 66 | 72% |
| Heavy | 8c/16GB `cpx42` | 0.1314 | 3.6× | 33 | 72% |

Burn rates are **derived from `estimateCost()`**, never hardcoded, so they
self-correct when pricing or availability shifts.

Why this beats price tiers: one price, no upsell conversation, no plan-migration
flow, and a light user and a heavy user land at the same margin.

### 6.5 Park / wake lifecycle

**Park** (idle sweep, `dryRun:false`): delete the server, **keep the volume**.
Correct for Hetzner. The reserved egress IP survives via `auto_delete:false`.

**Wake:** check SKU availability → substitute if sold out (margin-gated) → create
from the slim `baseImageId` → attach volume → re-attach reserved IP → health
check → only then mark `active`.

Three storage roles:

| Artifact | Role | Constraint |
|---|---|---|
| **Volume** | workspace/Docker/model data, survives park | **location-bound** — pins where you can wake |
| `baseImageId` | slim OS+toolchain boot image | makes wake 60–90 s instead of ~10 min |
| `lastSnapshotId` | legacy full-disk fallback | only for rows with no volume |

`"active"` means **usable**, not created. Only the health check may set it, and
it gates the meter — so you never bill for a box the user cannot reach.

### 6.6 Stable egress identity

Park deletes the server, so without intervention **every wake mints a new public
IP**. A user parking/waking twice a day presents one subscription credential from
~60 different datacenter IPs a month.

**Status: hypothesis, not verified.** No evidence has been gathered that
Anthropic or OpenAI act on this. What is known: post-auth CLI traffic from
datacenter IPs is routine (Claude Code runs in GitHub Actions on Azure-hosted
runners). The concern is only that the *pattern* resembles credential sharing.

**Primary IP, never Floating IP.** A Floating IP changes what reaches the box
*inbound*; the box still **sources** outbound connections from its primary
address. A Floating IP would have tested green — reserved ✅, attached ✅ — while
the vendor kept seeing a new address every wake.

Independent benefits that justify it regardless: stable DNS (no A-record
re-point per wake), stable SSH `known_hosts`, workable firewall allowlists.
€1.20/mo ≈ 4% of revenue.

**Action:** mirror credentials to `yaver-test-ephemeral`, run real sessions,
park/wake repeatedly, record whether anything trips. Hours, not weeks.

### 6.7 What the box can and cannot do

| Workload | 2c/4GB | 4c/8GB | 8c/16GB |
|---|---|---|---|
| Coding agent, git, tests | ✅ | ✅ | ✅ |
| TS language server | ✅ | ✅ | ✅ |
| RN/Expo dev server | ✅ | ✅ | ✅ |
| **Hermes bundle build** | ✅ fresh project · ⚠️ monorepo | ✅ | ✅ |
| Flutter (web dev server) | ✅ | ✅ | ✅ |
| Chrome headless + WebRTC passthrough | ✅ | ✅ | ✅ |
| WebRTC real-time **encoding** | ⚠️ | ✅ | ✅ |
| **Redroid** (one instance) | ❌ | ✅ ~6.5 GB of 8 | ✅ comfortable |
| Gradle / Kotlin build | ❌ | ⚠️ | ✅ |
| **Android emulator / AVD** | ❌ | ❌ | ❌ — needs nested virt, unavailable on Hetzner shared vCPU |

Redroid is chosen precisely *because* it sidesteps the nested-virt wall that
kills a real AVD — it is a container sharing the host kernel.

---

## 7. Capacity, failover, and the wake guarantee

### 7.1 There is no wake guarantee, from anyone

Hetzner has no capacity reservation. The hyperscalers sell reservations, but they
cost close to an always-on box. Park hands capacity back and hopes.

Measured 2026-07-21:

```
datacenter   avail/supported   cx33  cx43  cpx51  cax21  cax31
nbg1-dc3     12/24             sold  sold   sold   sold   sold
hel1-dc2     12/24             sold  sold   sold   sold   sold
fsn1-dc14     8/24             sold  sold   sold   sold   sold
ash-dc1      11/11                -     -    YES      -      -
```

**Zero ARM types available in `nbg1`.** And `change-type` cannot cross
architectures — so an ARM box could never upgrade. **Standby and pool boxes must
be x86.**

### 7.2 Availability-driven substitution

12 types *were* orderable in `nbg1`. A wake fails today not because capacity is
absent but because the code asks for one specific name.

Selection must: query `GET /datacenters → server_types.available`, filter by the
profile's floors (**disk is a hard floor** — a snapshot will not restore onto a
smaller disk), rank by **price**, and enforce a ceiling.

> ⚠️ **The ceiling must be margin-gated, not a price ratio.** A 1.6× multiplier
> blocks the only available substitute (4.2×) — the exact rescue it was built
> for. The right test is *"does this still clear the floor at expected hours?"*
> `cpx32` at 40 h is 79% and fine; at 200 h it is 38% and must be refused.

### 7.3 Error classification — never retry the unretryable

| Class | Signal | Action |
|---|---|---|
| `capacity` | `resource_unavailable`, `InsufficientInstanceCapacity`, `ZONE_RESOURCE_POOL_EXHAUSTED` | **advance the ladder** |
| `quota` | limit exceeded | advance + **alert operator** — our problem |
| `bad_request` | unknown SKU/image, invalid arg | **STOP** — identical everywhere |
| `auth` | 401/403 | **STOP + alert** — retrying spreads it |
| `transient` | 5xx, timeout | retry **same** rung, bounded |

`bad_request` is the `cx32` class. A ladder without classification would have
marched a nonexistent SKU through every datacenter and reported "no capacity
anywhere".

**Every rung must reclaim what it created before advancing**, or the ladder
multiplies the orphan bug by the number of rungs.

### 7.4 Warm pool

Keep a few spare, booted servers per (location, class). On wake, hand one over
and attach the user's volume.

| | Margin @ 120 h | Wake risk |
|---|---:|---|
| Deep park + substitution | 76% | real — depends on the market |
| **Warm pool** | **71%** | **none — capacity is held** |

Five points to eliminate "your workspace won't come back". Cost scales with
**spares, not users**: 3 spares across 100 users is €1.26/user; across 500 it is
€0.25.

`backend/convex/cloudPoolPlacement.ts` already contains `selectPoolEntry()` and
`leaseWouldExceedBudget()` — **with zero callers**.

**Open tension:** a pool server has its own IP, so handing one over re-introduces
churn. Swapping the user's reserved IP onto it needs a power-off (~60 s). Still
far better than a create that fails. Verify the exact interruption before
promising a number.

---

## 8. Tier 4 — Yaver Serverless ($9–19)

### 8.1 Why it cannot share Workspace hours

| | Hours/month |
|---|---:|
| Workspace budget | 120 |
| Serverless needs | **730** |

Drawing from one pool exhausts the month in ~5 days. Worse: a workspace parks
after 20 min, so hosting a backend *on the workspace box* means the user's app
dies whenever they stop typing. **Parking and hosting are mutually exclusive** —
structural, not a pricing choice.

### 8.2 Shared host economics

`cpx32` €41.99 ÷ ~20 tenants = **€2.10/user** → 75% at $9, 88% at $19.

Sharing is more defensible here than for a dev box: serverless functions do not
hold the user's mirrored Claude credentials. It is still tenant code on a shared
kernel and needs real isolation — a build, not a config change.

### 8.3 Ingress is not the relay

| Plane | Path | Who reaches it |
|---|---|---|
| Control (agent ↔ your devices) | Yaver relay | **same owner only** |
| Serverless ingress | `<id>.cloud.yaver.io` → box IP, **direct** | public internet |

Routing customer backends through the relay would break the same-owner
invariant, put production traffic on shared infrastructure, and make the relay a
hard dependency for other people's apps. **Relay Pro supports operating
serverless, not serving it.**

> ⚠️ **Blocker:** the DNS record is `proxied: false`, so the box must terminate
> TLS — today that is a self-signed LAN cert. Needs Let's Encrypt on the box, or
> `proxied: true` with Cloudflare terminating. `custom-domain-tls` exists in the
> capability enum, unimplemented.

---

# PART III — THE DEFAULT PATH

## 9. Opinionated routing

A user who says nothing must still land somewhere good and cheap.

| Decision | Default | Rationale |
|---|---|---|
| **Machine class** | 2c/4GB | smallest box that runs the default workload well |
| **Stack** | **React Native + TypeScript** | the flagship path; Hermes push is the differentiator |
| **Preview** | **Chrome (web dev server) + WebRTC** | lightweight — no emulator, no Redroid, no GPU |
| **Device target** | the user's **own phone** via Hermes push | zero server cost, and a better test |
| **Park** | 20 min | protects the tier |
| **Provider** | Hetzner | 3–5× cheaper than hyperscalers |
| **Region** | user's `eu`/`us` preference | volumes are location-bound |

### 9.1 Why 4 GB is enough for *this* path

The default loop deliberately avoids the two memory hogs:

- **No Redroid** — the RN flow pushes a Hermes bundle to the user's *real phone*.
  Redroid exists for "I have no Android device", which is the exception.
- **No native Android build** — Gradle is burst work and belongs on a shared
  build host.

What remains — Metro/Expo dev server, TypeScript LSP, Chrome headless, WebRTC
passthrough — fits 4 GB for a fresh project.

**Metro on a monorepo is the known ceiling.** The answer is *detect and offer the
upgrade*, not pre-provision for it (Rule 2).

### 9.2 Chrome + WebRTC as the lightweight preview

Passthrough streaming, not encoding. The heavy variant is real-time video
encoding, which is why `linux-runner-webrtc` is a separate profile in the enum.
The default path streams a browser surface, which 2 cores handles.

### 9.3 Bursts belong on a shared build host

Hermes and Gradle want lots of memory for two minutes and nothing afterwards —
the worst possible fit for a per-user allocation. Route them to a shared build
host so the burst is amortised, exactly like the relay and the warm pool.
`/dev/build-native` is already an agent-side operation, so the seam exists.

---

## 10. Self-hosted — every tier has a $0 path

| Paid | Self-hosted equivalent | Status |
|---|---|---|
| Relay Pro | run `relay/` — QUIC, password-protected | **already supported** |
| Cloud Workspace | BYO machine, your own provider token | **already supported** (`byoMachines`) |
| Yaver Serverless | run your backend on your own box | **already supported** |

Deliberate positioning, not a concession. **The product is not having to operate
it**: capacity, wake, parking, egress stability, reclamation, QoS.

It also keeps us honest — if the managed tier is only worth paying for because
self-hosting is artificially crippled, it is not worth paying for.

---

# PART IV — PROTECTIONS AND EXECUTION

## 11. Loss protections

### 11.1 Live

- **Fail-closed ordering**: credentials → entitlement → quota → *then* spend
  (`cloudMachines.provision`).
- **Auto-park runs live** (`dryRun:false`) even while the wallet meter simulates
  — deliberate asymmetry: a simulated ledger must never keep a real server alive.
- **Wallet-reserve gate** on start.
- **Margin floor in code** (`unitEconomics.ts`) — a losing config is refused, not
  invoiced.
- **Per-attempt reclamation** in the placement ladder.
- **One reclamation path** for every satellite — volume, egress IP, snapshot
  (`reclaimAuxResources`). Never reports `stopped` while something bills.
- **Orphan sweep** — provider→Convex reconciliation, report-only by design
  (the token can see boxes that are not Yaver workspaces).
- **`past_due` → disable gateway + park compute.** Previously a failing card
  bought indefinite free compute.

### 11.2 Open, ranked by what they would cost

1. **Cron host is a single point of failure.** Metering *and* auto-park run from
   one external box POSTing `/crons/run`, with **no alarm**. If it dies you keep
   collecting $29 while costs run unbounded — silently disabling the single best
   loss protection in the system.
2. **Substitution ceiling is wrong** (§7.2) — blocks the rescue it was built for.
3. **Gateway caps** — hourly cap and per-user credit limit are unbound bindings
   that fail **open**; no input-token ceiling; placeholder COGS rates. Bites only
   when inference goes live, which is why it stays unannounced.
4. **Metering never bills** — moot under throttle-don't-bill, but the caps must
   actually work.

---

## 12. Implementation plan

### P0 — correctness
1. Commit the working branch (67 files, typecheck-clean).
2. **Margin-gated substitution** — replace `YAVER_SKU_SUBSTITUTE_MAX_MULTIPLIER`
   with `assessViability()` against expected hours.
   → `cloudLifecycle.hetznerPickAvailableServerType`
3. **Alarm on missing cron ticks.**

### P1 — the default path (§9)
4. Default class = 2c/4GB, availability-driven → `hetznerServerType`, `resolveSku`
5. Default stack = RN + TypeScript when unspecified → `stack_detect.go`
6. Default preview = Chrome/web + WebRTC; Redroid opt-in → `devserver_kind.go`
7. Park timer 45 → **20 min** → `YAVER_CLOUD_IDLE_MINUTES`, `http.ts:9584`
8. Machine-class burn multipliers from `estimateCost()` → `includedAllowance`

### P2 — the tiers
9. Shared relay pool → `provisionRelay.ts`, `managedRelays.ts`
10. Warm pool wired → `cloudPoolPlacement.ts` (zero callers today)
11. Shared serverless host + TLS

### P3 — later
12. Multi-provider live wiring · relay QoS classes · shared build host

---

## 13. Verify before selling

| Check | Why | Cost |
|---|---|---|
| Hetzner runbook tiers A→C | live probes found 3 bugs in one afternoon | €0–0.01 |
| **Metro on 4 GB** — fresh project *and* monorepo | validates or kills the 4 GB default | cents |
| **Redroid on a real box** | tightest fit in the plan; decides 8 vs 16 GB | cents |
| **Real uptime vs 120 h** | the input the plan is most sensitive to | free, post-launch |
| Claude credential mirroring across park/wake | converts §6.6 from guess to fact | cents |

---

## 14. The standing lesson

Three separate bugs found in one session were the same shape — **inventory is
not operation**:

1. `cx32`/`cx42`/`cx52`/`gex44` were the default SKUs and **do not exist**.
2. Hetzner declared the `tagged-cleanup` capability while
   `listYaverTaggedResources` returned `[]`.
3. The new orphan sweep filtered `yaver=managed` while provisioning writes
   `managed=true` — it would have reported zero orphans forever, and looked
   healthy doing it.

**Two of those were in code written specifically to prevent that failure mode.**

Every one was caught by a single free read-only command. Probe the real
capability, never the proxy.
