# Multi-Provider Placement Architecture — Yaver Cloud Workspace

Date: 2026-07-21
Status: design. Partially implemented (`cloudProviders/selection.ts`).
Goal: **AWS / GCP / Azure / Hetzner all usable and interchangeable for Cloud
Workspace — never disappoint the user because one provider is out of capacity.**

Companions: `yaver-cloud-workspace-product-model.md` (what is sold),
`cloud-live-wiring-runbook.md` (how to verify against real APIs),
`cloud-workspace-commercialization-audit-2026-07-21.md` (current state).

---

## 0. The honest version of "interchangeable"

Interchangeability is two different claims, and only one of them is cheap.

| Claim | Achievable? | Cost |
|---|---|---|
| **A. Any new workspace can be created on any provider** | ✅ yes | placement engine (§2–§4) |
| **B. An existing workspace can move between providers** | ⚠️ only with portable state | state redesign (§6) |

**Why B is not free.** Park is delete-not-stop, and state survives in one of two
provider-native forms: a **Hetzner Volume** (a block device that physically
cannot leave its location, let alone its provider) or a **snapshot** (a
provider-native disk image). The reserved egress IP is pinned harder still — to
a *datacenter*. So a parked Hetzner workspace can only wake on Hetzner, in that
location. That is not a bug in the code; it is what the storage model means.

> **The architectural crux: snapshot/volume-based park makes providers
> non-interchangeable. Content-based park makes them interchangeable.**
> Everything in §6 follows from that one sentence.

So the design is layered: **placement is free, binding is real, migration is
explicit.** Pretending otherwise would produce the worst outcome — a "failover"
that silently abandons a customer's data.

---

## 1. Layer model

```
┌─ Product ──────────────────────────────────────────────────────────┐
│ Workspace label · location label · SSH target · state · cost        │
│ NEVER a provider picker (product-model §6)                          │
└──────────────────────────┬─────────────────────────────────────────┘
┌─ Placement ──────────────▼─────────────────────────────────────────┐
│ PlacementRequest → PlacementPlan (ORDERED candidate ladder)         │
│ eligibility → capacity → credits → cost → locality                  │
│ Runs at: first provision, explicit migration                        │
└──────────────────────────┬─────────────────────────────────────────┘
┌─ Binding ────────────────▼─────────────────────────────────────────┐
│ What this workspace is PINNED to: provider, scope, volume,          │
│ snapshot, egress IP. Runs at: wake, resize.                         │
│ May fail over WITHIN the binding. Escalates to migration only with  │
│ consent.                                                            │
└──────────────────────────┬─────────────────────────────────────────┘
┌─ Adapter ────────────────▼─────────────────────────────────────────┐
│ AbstractCloudProvider — hetzner / aws / gcp / azure                 │
└────────────────────────────────────────────────────────────────────┘
```

The bug this layering prevents: today `cloudLifecycle.pauseMachine` reads
`machine.provider` for telemetry but calls `hetznerDelete` **unconditionally**.
A row bound to AWS would be marked "paused" while the EC2 instance kept running.
Binding must *route*, not merely *annotate*.

---

## 2. The candidate is a triple, not a provider

Capacity exhaustion is almost never provider-wide. It is
**(provider, scope, SKU)**-specific — one datacenter is out of one instance
type. So the unit of placement is:

```ts
type PlacementCandidate = {
  provider: ProviderId;      // hetzner | aws | gcp | azure
  scope: string;             // fsn1-dc14 | eu-north-1a | us-central1-a | westeurope
  sku: string;               // cx33 | m7i.xlarge | n2-standard-4 | Standard_D4s_v5
  profile: MachineProfile;   // what the user actually bought
  score: number;
  reasons: string[];         // why it ranked here — user-invisible, operator-gold
};
```

**This is why the fix must not be "try another provider".** The cheapest,
highest-success-rate failover is *within* Hetzner: same profile, different
datacenter. Reaching for another cloud first would be slower, more expensive,
and would cross the eligibility boundary for no reason.

**Ladder order, cheapest and safest first:**

1. Same provider, same scope, **equivalent SKU** (cx33 → cpx31: same class).
2. Same provider, **different scope** (fsn1 → nbg1 → hel1 → ash).
3. **Different provider**, best equivalent SKU — *only if eligible* (§3).
4. Fail with a specific, honest message (§7).

---

## 3. Eligibility is earned, and it gates failover

**A provider may only receive paid placement if it can prove the capability
floor** (`PAID_PLACEMENT_CAPABILITIES` in `cloudProviders/selection.ts`):

- `delete-stops-compute-spend` — park is delete-not-stop. If delete does not
  stop the meter, scale-to-zero is a lie and the customer is billed for a box
  they think is asleep.
- `durable-volume` — state must survive the park.
- `tagged-cleanup` — a leak we cannot detect is worse than one we can.

> ⚠️ **Do not relax this to satisfy "never disappoint the user".** Failing over
> onto a provider that cannot park correctly does not rescue the user — it hands
> them a workspace that cannot sleep, cannot wake, and bills forever. A clear
> "no capacity right now, try region X" is a far better user experience than a
> box that silently costs them money. Use `YAVER_FORCE_COMPUTE_PROVIDER` for
> adapter testing; it is loud and auditable.

Today only Hetzner clears the floor. §8 is the path for the other three.

**Corollary — the capability list is a load-bearing security/cost boundary, not
documentation.** Hetzner declared `tagged-cleanup` for months while
`listYaverTaggedResources` returned `[]`. Never declare a capability that is not
really implemented; the placement gate believes it.

---

## 3.5 MEASURED REALITY — capacity is already the binding constraint

Probed live against the production Hetzner account, 2026-07-21:

```
datacenter   avail/supported   cx33   cx43  cpx51  cax21  cax31
nbg1-dc3     12/24             sold   sold   sold   sold   sold
hel1-dc2     12/24             sold   sold   sold   sold   sold
fsn1-dc14     8/24             sold   sold   sold   sold   sold
ash-dc1      11/11                -      -    YES      -      -
hil-dc1      11/11                -      -    YES      -      -
```

**Every SKU Yaver is configured to use is sold out in every EU datacenter.**
`supported` means the datacenter can host the type; `available` means you can
actually order one. Two-thirds of `fsn1-dc14`'s catalogue is unorderable.

Three conclusions, none of them optional:

1. **There is no wake guarantee — from anyone.** Park is delete-not-stop, which
   hands the capacity back. No hyperscaler guarantees on-demand capacity either;
   the only guarantees are *paid reservations* (AWS Capacity Reservations, Azure
   Capacity Reservations, GCP reservations), and **Hetzner has no equivalent at
   all**. A parked workspace is a bet that the market has room when the user
   returns.
2. **The hardcoded SKU ladder is the actual defect.** 12 types *are* available in
   `nbg1` — `cpx32` (4c/8GB/160GB) is a clean substitute for `cx33` (4c/8GB/80GB).
   A wake fails today not because capacity is absent but because we ask for one
   specific name. **Availability-driven SKU selection is mandatory, not an
   optimisation.**
3. **Hetzner has exactly ONE datacenter per location** (`nbg1-dc3`, `hel1-dc2`,
   `fsn1-dc14`, `ash-dc1`, `hil-dc1`, `sin-dc1`). So "try another datacenter in
   the same location" — §6.1 step 1 — **is a no-op on Hetzner.** The only
   intra-provider levers are SKU substitution and changing location, and
   changing location is blocked by the volume.

### 3.5.1 The trap this creates

A volume is location-bound, and reading it needs a server in that location. If a
location sells out *completely*, a parked workspace can be neither **woken** nor
**evacuated** — the data is hostage to capacity. Today that is survivable only
because some types remain orderable.

**Mitigations, in order of cost:**

- **Substitute the SKU dynamically** (§4.1) — free, and sufficient today.
- **Evacuate with any available type.** Rescue does not need the workspace's
  profile; the smallest orderable box can stream the content out. Keep this path
  independent of the normal SKU ladder so a sold-out profile cannot block rescue.
- **Portable state (§6.2)** — the only structural fix. Turns "trapped in a
  location" into "restore anywhere".
- **Paid capacity reservation** on a hyperscaler for workspaces that need a wake
  SLA. Costs real money and only makes sense for a premium tier.

> This is the strongest argument for multi-provider, and it is not about user
> choice: **it is about being able to wake at all.** Interchangeability is an
> availability requirement.

---

## 4. Capacity: proactive signal, reactive truth

### 4.1 Proactive — filter before attempting

Hetzner publishes real availability per datacenter (verified live 2026-07-21):

```
GET /datacenters/{id} → server_types.{supported[], available[], available_for_migration[]}
# fsn1-dc14: 24 supported, only 8 currently available
```

`supported ≠ available` is exactly the inventory-vs-operation trap. **Rank on
`available`.** Equivalents: AWS `DescribeInstanceTypeOfferings`, GCP zone
availability + `MACHINE_TYPE` quota, Azure `ResourceSku` with
`restrictions[]`.

Cache for ~5 minutes. It is a hint, never a guarantee.

### 4.2 Reactive — classify the failure, do not blanket-retry

The existing wake loop retries on a regex over the error body. Generalize it
into an explicit classification, because **retrying the wrong class is how you
create orphans**:

| Class | Signal | Action |
|---|---|---|
| `capacity` | `resource_unavailable`, `no available`, capacity, placement, `InsufficientInstanceCapacity`, `ZONE_RESOURCE_POOL_EXHAUSTED`, `SkuNotAvailable` | **advance the ladder** |
| `quota` | quota / limit exceeded | advance provider, **alert the operator** — this is our problem, not the user's |
| `bad_request` | unknown SKU, bad image, invalid arg | **STOP.** Never retry; it will fail identically everywhere. *(This is the class the `cx32` bug lived in — a ladder that retried it would have burned every candidate.)* |
| `auth` | 401/403 | **STOP**, alert. Retrying with broken credentials just spreads the failure. |
| `transient` | 5xx, timeout | retry **same** candidate, bounded, with jitter |

### 4.3 Circuit breaker

Per `(provider, scope)`: N capacity failures inside a window ⇒ demote to the
back of the ladder for a cool-off. Prevents every user in a minute stampeding
the same exhausted datacenter.

### 4.4 The safety invariant for the attempt loop

**Every attempt that creates a resource must reclaim it before advancing.**
The ladder multiplies R1: N candidates means N chances to leave a half-created
server behind. The loop must carry the same `createdCloudResourceId` discipline
now in `cloudMachines.provision`, per iteration.

---

## 5. Scoring, once candidates are eligible + available

Ordered lexicographically — earlier keys dominate:

1. **Binding** — for a wake, the bound candidate wins outright (§6).
2. **Capability fit** — must satisfy the profile's required capabilities.
3. **Provider credits** — burn free credits before real money. This is the
   `credit-first` policy in `providerCatalog.ts`, and the reason multi-provider
   is worth building at all. *(Requires real credit sync; today
   `creditUsdRemaining` is populated by nothing.)*
4. **Cost** — `estimateCost` is real for Hetzner now (live `/server_types`
   pricing); the others must implement it before they can be ranked honestly.
5. **Locality** — user's region preference (`eu` / `us`), then latency.
6. **Stability** — recent success rate for that `(provider, scope)`.

**Never rank on a number you cannot source.** A guessed cost is worse than an
absent one: it silently misroutes spend. Rank unknown-cost candidates last, and
say so in `reasons[]`.

---

## 6. Binding, and what true interchangeability requires

### 6.1 What pins a workspace today

| Pin | Scope | Escapable? |
|---|---|---|
| Volume | provider + **location** | ❌ block device cannot move |
| Snapshot | provider | ❌ provider-native image format |
| Egress IP | provider + **datacenter** | ⚠️ release + re-reserve (address changes) |
| Hostname/DNS | none | ✅ re-point A record |
| Device identity | none | ✅ Convex row, provider-agnostic |

So a parked Hetzner workspace wakes on Hetzner or not at all. **A wake must
never "fail over" to another provider** — that would silently abandon the
user's data. Correct behaviour on capacity failure at wake:

1. Try an **available SKU** in the volume's location (§4.1). *(Note: "try
   another datacenter in the same location" does NOT apply to Hetzner — it has
   exactly one datacenter per location. SKU substitution is the only
   intra-location lever.)*
2. Try an **equivalent SKU** whose disk ≥ the snapshot's disk
   (`hetznerServerTypeForDisk` already encodes this constraint — a snapshot only
   restores onto a type with a disk at least as large).
3. If the reserved egress IP's datacenter is the blocker: **release it, wake
   elsewhere, re-reserve** — the address changes, so tell the user (this is the
   `egressIpUsed:false` path already implemented).
4. Otherwise: fail honestly. State is safe; the user waits or migrates.

### 6.2 Making providers genuinely interchangeable

The blocker is the *storage format*, not the adapters. Replace provider-native
park with a **portable workspace artifact**:

```
Park:  quiesce → snapshot the workspace CONTENT (restic/borg, deduped,
       encrypted client-side) → push to object storage → delete the box
Wake:  place freely (any eligible provider) → boot the base image →
       restore the content artifact → resume
```

Consequences, good and bad:

- ✅ **Any provider can wake any workspace.** Binding collapses to "where is the
  artifact", and object storage is reachable from everywhere.
- ✅ Cross-provider migration becomes a normal wake, not a special operation.
- ✅ Credit-first placement becomes *actually* usable — you can move workloads to
  whoever is giving away credits this quarter.
- ❌ **Wake gets slower.** The volume path is the ~60–90s wake; a content restore
  is minutes. That was the whole point of the volume model.
- ❌ Object storage is a new always-on cost and a new failure domain.
- ❌ Client-side encryption is mandatory — the artifact holds the user's code,
  and the privacy contract forbids that going anywhere we can read it.

**Recommendation: hybrid.** Keep the volume fast-path as the *cache* for the
common case (wake on the same provider), and treat the portable artifact as the
*source of truth* that makes migration and cross-provider placement possible.
Volume present ⇒ fast wake. Volume absent or provider changed ⇒ restore from
artifact. This is the only design that gets both the 90-second wake and real
interchangeability.

---

## 7. Failure semantics — "never disappoint" done honestly

Ranked by what is actually best for the user:

1. **Place them somewhere eligible.** Ladder §2.
2. **Offer a different location explicitly** — *"eu is full right now; start in
   us?"* A choice is not a disappointment.
3. **Offer a different size** — never silently. Downgrading a paid profile
   without consent is a billing problem wearing a UX costume.
4. **Queue with a real ETA**, if capacity is expected back.
5. **Fail with a specific cause and a remedy.** Never a generic error.

**Never do:** place on a non-eligible provider; silently change the SKU class;
report success while a resource is stranded; or retry a `bad_request` across
every candidate.

Every outcome records `reasons[]` — the operator needs to know *why* a user
landed where they did, and "no capacity" must be distinguishable from "we have
no eligible provider", which is our failure, not the market's.

---

## 8. Implementation phases

**Phase A — ladder within Hetzner** *(highest value, zero new eligibility risk)*
- Candidate triple + attempt loop with per-attempt reclamation (§4.4).
- Error classification (§4.2) replacing the regex.
- Proactive datacenter availability (§4.1), 5-min cache.
- Equivalent-SKU table honouring the disk constraint.

**Phase B — observability**
- Persist `PlacementPlan` + chosen candidate + `reasons[]`.
- Alert on `quota` and on "no eligible provider".
- Circuit breaker (§4.3).

**Phase C — make a second provider eligible**
- Pick one (AWS is furthest along). Work the go/no-go list in the runbook §7.
- Credential refresh is the true blocker for GCP/Azure — 1-hour env-var tokens.
- Only then flip `productionEligible`.

**Phase D — credit-first placement**
- Real `readBudgetStatus` per provider feeding `creditUsdRemaining`.
- Legal check first: most credit programmes prohibit *resale*; free-tier use is
  usually fine, billed compute backed by free credits may not be.

**Phase E — portable state (§6.2)**
- Content artifact + client-side encryption + object storage.
- Hybrid volume fast-path.
- This is what turns "multi-provider" from placement-time into real
  interchangeability.

---

## 9. Invariants any implementation must preserve

1. The **user never chooses a provider**; provider is a read-only label.
2. A client can never influence placement — server-side decision only.
3. **Non-eligible providers never take paid placement**, capacity pressure or not.
4. **Every attempt reclaims what it created** before advancing the ladder.
5. **A wake never crosses providers** while state is provider-native.
6. **Never silently downgrade** the profile the user paid for.
7. **Never rank on an unsourced number.**
8. **Never declare a capability that is not implemented** — the gate believes it.
9. Delete stops spend — including satellites (volume, egress IP, snapshot).
10. Cost is legible to the user; that is the product, not just telemetry.
