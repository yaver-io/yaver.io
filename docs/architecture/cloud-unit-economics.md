# Cloud Unit Economics — What to Default To

Date: 2026-07-21
Basis: **real Hetzner prices pulled from the production account**, not estimates.
Companion: `yaver-cloud-workspace-product-model.md` (what is sold at all).

---

## 1. The input costs (gross €, fsn1, 2026-07-21)

| Type | c / RAM / disk | €/hour | €/month | EU availability |
|---|---|---:|---:|---|
| cx23 | 2 / 4 / 40 | 0.0104 | 6.49 | **sold out** |
| cx33 | 4 / 8 / 80 | 0.0160 | 8.99 | **sold out** |
| cx43 | 8 / 16 / 160 | 0.0296 | 18.49 | **sold out** |
| cax11 | 2 / 4 / 40 (arm) | 0.0112 | 6.99 | **sold out** |
| cax21 | 4 / 8 / 80 (arm) | 0.0200 | 12.49 | **sold out** |
| cpx22 | 2 / 4 / 80 | 0.0368 | 22.99 | ✅ available |
| **cpx32** | **4 / 8 / 160** | **0.0673** | **41.99** | ✅ available |
| cpx42 | 8 / 16 / 320 | 0.1314 | 81.99 | ✅ available |
| cpx51 | 16 / 32 / 360 | 0.1338 | 83.49 | sold out |
| ccx13 | 2 / 8 / 80 (ded.) | 0.0809 | 50.49 | ✅ available |

Plus: **Volume €0.044/GB/month**, **reserved primary IPv4 ≈ €0.50–1.20/month**.

> ⚠️ **Size is not a proxy for price.** `cpx32` has the same cores and RAM as
> `cx33` and costs **4.7×**. Any logic that picks "smallest sufficient" instead
> of "cheapest sufficient" will quietly invert your margin. This was a real bug
> in the availability-substitution code and is now gated by
> `YAVER_SKU_SUBSTITUTE_MAX_MULTIPLIER` (default 1.6×).

---

## 2. Cloud Workspace at $29/mo (≈ €26.7)

Fixed floor per workspace, paid whether or not it runs: 40 GB volume (€1.76) +
egress IP (€1.20) = **€2.96/month**.

| Active hours/mo | on cx33 (€0.016/h) | Total cost | Gross margin |
|---:|---:|---:|---:|
| 40 | €0.64 | €3.60 | **86%** |
| 100 | €1.60 | €4.56 | **83%** |
| 200 | €3.20 | €6.16 | **77%** |
| 730 (24/7) | €11.68 | €14.64 | **45%** |

On the *available* substitute `cpx32` (€0.0673/h):

| Active hours/mo | Compute | Total | Margin |
|---:|---:|---:|---:|
| 40 | €2.69 | €5.65 | **79%** |
| 100 | €6.73 | €9.69 | **64%** |
| 200 | €13.46 | €16.42 | **38%** |
| 730 | €49.13 | €52.09 | **−95% (LOSS)** |

And on a build-class `cpx51`, 24/7 costs €97.67 against €26.7 of revenue.

**Conclusion: scale-to-zero is not an optimisation, it is the entire business
model.** A workspace left running 24/7 on an expensive SKU loses money on every
plan. Auto-park is the profit engine, and it must stay default-on and
fail-closed.

---

## 3. Relay Pro at $9/mo (≈ €8.3) — the biggest fixable problem

`provisionRelay.ts` provisions a **dedicated Hetzner box per subscriber**.

| Model | Cost/user | Margin |
|---|---:|---:|
| **Dedicated box today** (cax11 @ €6.99, always-on) | €6.99 | **16%** |
| **Shared pool** (one cax11 serving ~50) | €0.14 | **98%** |

16% gross does not survive a single support ticket, a single failed wake, or the
grace snapshot the teardown leaves behind. And a relay must be always-on to be
useful, so **Relay Pro is the one product with no scale-to-zero escape hatch** —
its cost is structural.

**Recommendation: Relay Pro should be a capacity/QoS tier on a shared
multi-tenant pool, not dedicated hardware.**

This is safe precisely because of the transport invariants already enforced:
the relay is **pass-through and authorizes nothing**, cross-tenant bridging is
blocked in Convex (`devices.ts:2584`, `userSettings.ts:757-765`), and **free vs
Pro is explicitly NOT a security boundary** — Pro buys capacity, nothing else.
So sharing a box costs no security and multiplies margin by six.

Sell the dedicated box as a separate, higher-priced **Private Relay** SKU for
customers who want isolation for compliance reasons — priced to actually cover
an always-on machine (≥ $19), not $9.

---

## 4. Recommended defaults

| Decision | Default | Why |
|---|---|---|
| **Provider** | **Hetzner** | 3–5× cheaper than the hyperscalers for identical specs. AWS/GCP/Azure are for **availability failover and burning credits**, never for cost. Defaulting to a hyperscaler would erase the margin. |
| **Standard SKU** | cheapest *available* 4c/8GB | Availability-driven, cost-ranked, ceiling-capped. Never a hardcoded name — every configured SKU was sold out in the EU on 2026-07-21. |
| **Auto-park** | **ON, 30–45 min idle** | The profit engine. Opt-out, never opt-in. |
| **Included hours** | **~60–100 h/month**, then metered | Protects against the 24/7 loss cases above while covering normal use (a dev box is used a few hours a day). |
| **Heavy/build SKUs** | included hours **scaled down** by cost ratio | `cpx51` costs 8× `cx33`; giving it the same hours guarantees a loss. |
| **Egress IP** | **ON for paid workspaces** | €1.20/mo = ~4% of revenue. Buys stable outbound identity, stable DNS (no A-record re-point per wake), stable SSH `known_hosts`, and firewall allowlists. Cheap insurance even though the vendor-blocking risk is unverified. |
| **Trials** | **no compute, inference only** | Free VMs carry the abuse blast radius that can suspend the whole provider account. |
| **Free tier** | BYO only | Costs us ~nothing and is the funnel. |

---

## 5. Where the margin actually leaks

Ranked by how much money each has cost or could cost:

1. **A box that never parks.** One 24/7 `cpx32` wipes out the margin on ~4
   well-behaved subscribers. Auto-park failing silently is the single most
   expensive bug class in this system — which is why `cloudIdleSweep` runs
   `dryRun:false` even while the wallet meter is still simulating.
2. **Dedicated Relay Pro boxes.** −82 points of margin versus shared, on every
   subscriber, forever.
3. **Cost-blind SKU substitution.** Up to 4.7× on a wake. Now capped.
4. **Satellites that outlive their server** — volumes, reserved IPs, snapshots,
   AMIs and their EBS snapshots. Each is small; each is permanent; none was
   detectable before the orphan sweep existed.
5. **Metering that never bills.** `managedUsage`/`creditUsage` accumulate and
   nothing converts them to a charge, so overage is currently absorbed at 100%.
6. **Hyperscaler default.** 3–5× input cost for the same box.

---

## 6. The pricing consequence of no wake guarantee

Hetzner has **no capacity reservation product**, and on 2026-07-21 every
configured SKU was sold out across all three EU datacenters. Park hands capacity
back; nothing guarantees it returns.

So a wake-time SLA cannot be promised on the cheap tier — it would be a promise
backed by someone else's spare inventory. Two honest options:

- **Best-effort wake** (what the price supports): substitute SKUs, offer another
  region, be explicit that a busy region may mean a wait. This is what the
  current implementation does.
- **Guaranteed wake** as a premium tier, backed by a paid capacity reservation
  on a hyperscaler — priced to cover a reservation, which is close to paying for
  an always-on box.

Do not sell the second at the price of the first.

---

## 7. Park modes — what each one can honestly promise

Hetzner has **no capacity reservation** and bills a *stopped* server exactly like
a running one — only DELETE stops the meter. So there is no cheap-but-guaranteed
state to buy. Three modes, and the margin decides which can be default.

| Mode | What happens | Cost/mo @ $29 | Margin | Promise |
|---|---|---:|---:|---|
| **deep** *(default)* | server deleted, volume kept | €3.60 (40h) | **87%** | best-effort wake; **may fail when a region is sold out** |
| **standby** | smallest server stays alive with the volume attached; resize up on demand | €10.78 (40h) | **60%** | **always reachable**; burst speed best-effort |
| **reserved** | never parks | €14.64 (cx33) / €52.09 (cpx32) | 45% / **−95%** | full guarantee |

> ⚠️ **Correction to an earlier estimate.** Standby was first sketched at "~€6.49/mo,
> ~69% margin". That was wrong: the standby box bills **hourly for the ~690 idle
> hours** (€0.0104 × 690 = €7.18), *on top of* active compute, volume and IP.
> Standby roughly triples the parked cost floor and **fails the 60% margin
> floor**. It must be a **paid add-on (~+$9/mo)** or a higher-tier bundle —
> never the default.

### 7.1 What "standby" means in the product

Not "a permanently slow box". The sequence is:

1. **Reconnect is instant** — the box never went away. Agent online, SSH live,
   relay connected. No wake, no provisioning wait.
2. **Light work just runs.** Editing, git, tests and small builds are fine on a
   2c/4GB box, so most sessions never resize at all.
3. **Heavy work triggers a resize up** (`change-type`). Hetzner requires the
   server POWERED OFF to change type, so this costs a **~60–90s interruption**,
   then full speed.
4. **Idle resizes back down.**

Two properties that make this the honest answer to "can you guarantee a wake?":

- **`change-type` is itself capacity-constrained.** If the large SKU is sold
  out the upgrade fails — **but the small box is still there and still yours.**
  Standby converts *"your workspace cannot wake"* into *"your workspace is
  slower today."* That is a promise we can actually keep.
- Always resize with **`upgrade_disk: false`** so the change stays reversible; a
  Hetzner disk upgrade is one-way. Volume-backed workspaces keep a small OS
  disk, so this is free to do.

The standby box must be chosen by **availability and price**, never a fixed
name — `cx23`, the obvious choice, was itself sold out on 2026-07-21.

---

## 8. The margin floor is enforced in code

Owner directive: *"we won't have any business with 16% gross at all."*
`backend/convex/unitEconomics.ts` encodes it — `MIN_GROSS_MARGIN` (default 60%),
`assessViability()`, `dedicatedAlwaysOnViable()`, and
`maxIncludedHoursForTarget()` for scaling allowances by SKU cost so an expensive
type cannot be given a cheap type's hours.

Verified against the real numbers:

```
FAIL   16%  €  6.99  Relay Pro $9 dedicated cax11     ← the rule's origin
PASS   98%  €  0.14  Relay Pro $9 shared (50/box)
PASS   96%  €  0.35  Relay Pro $9 shared (20/box)
PASS   87%  €  3.60  Workspace $29 deep-park 40h cx33
PASS   83%  €  4.56  Workspace $29 deep-park 100h cx33
FAIL   60%  € 10.78  Workspace $29 standby 40h
FAIL   45%  € 14.64  Workspace $29 reserved cx33
FAIL  -95%  € 52.09  Workspace $29 reserved cpx32
```

Run this as a **preflight** before shipping a plan, a default SKU, or a park
mode. A losing configuration should be refused by the system, not discovered on
an invoice.
