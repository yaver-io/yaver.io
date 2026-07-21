# Cloud Workspace — Master Plan

Date: 2026-07-21
Status: **decisions settled, work queued.** This is the single entry point.

Detail lives in:
`yaver-cloud-workspace-product-model.md` (what is sold) ·
`cloud-unit-economics.md` (margins, measured) ·
`cloud-multiprovider-placement-architecture.md` (placement/capacity) ·
`cloud-live-wiring-runbook.md` (how to verify against real APIs) ·
`cloud-secrets-and-env.md` (credentials) ·
`cloud-workspace-commercialization-audit-2026-07-21.md` (state + risks)

---

## 1. Settled decisions

1. **Sell compute. Never sell inference.** Cloud Workspace ($29) and Relay Pro
   ($9) are the business. Inference is a capped, time-boxed trial loan — it
   exists so someone without a Claude/ChatGPT subscription can taste the
   product. Runners are always the user's own subscription (vendor terms).
2. **Never dedicate an always-on box to one user.** This single rule kills
   Relay-Pro-on-dedicated-hardware (16% gross) *and* standby-as-a-dedicated-box
   (23%). If a cost is always-on, **amortise it** — never quota it, because a
   quota limits active hours and an always-on cost is incurred at zero hours.
3. **Dedicated VM + deep park stays the workspace product.** 87% margin, real
   VM isolation, already built and audited. Do not replace it with shared
   containers.
4. **Margin floor is 70%** (`unitEconomics.MIN_GROSS_MARGIN`), target 80%.
   Failing the floor is a signal to amortise, never to lower the floor.
5. **Trials get no VM.** A free VM carries an abuse blast radius that can
   suspend the entire provider account, paying customers included.

---

## 2. The architecture in one page

```
Free            BYO machine + public relay + trial inference   (costs ~€0)
Relay Pro $9    SHARED relay pool, QoS-tiered                  (96% margin)
Workspace $29   dedicated VM + deep park + warm pool           (74-87% margin)
```

**Why dedicated VMs and not shared containers:** Yaver's product is running
coding agents with dangerous permissions — arbitrary code execution is the
feature — and the box holds the user's **mirrored Claude/Codex credentials**. Two
tenants on one kernel means a container escape leaks another customer's source
*and* their subscription. That is categorically different from sharing a relay
(pass-through, executes nothing) or a standby host (idle volumes). Shared
execution requires microVMs, which requires nested virtualisation, which
requires dedicated/bare-metal hosts, which collides with the "never monthly
Hetzner" rule. Not worth it for a margin the warm pool already delivers.

**Warm pool** — keep a few spare, booted servers per (location, SKU class). On
wake, hand one over and attach the user's volume. Capacity is guaranteed because
the server already exists; wake is instant; isolation stays VM-level. Cost
scales with **spares, not users**: 3 spare `cpx32` across 100 users is
€1.26/user → **74% margin**; at 500 users it is €0.25.

`backend/convex/cloudPoolPlacement.ts` already contains `selectPoolEntry()` and
`leaseWouldExceedBudget()` — **with zero callers**. The design was sketched and
never wired. Cheapest high-value item on the board.

---

## 3. ⚠️ Open tension: warm pool vs stable egress IP

**A pool server has its own public IP.** So handing a user a different pool
server each wake re-introduces the exact IP churn the reserved egress IP was
built to prevent — arguably worse, because the address is now effectively random
rather than merely new.

The two features are in direct tension, and the resolution costs something:

| Option | Wake speed | Egress stability |
|---|---|---|
| Pool server keeps its own IP | **instant** | ❌ random per wake |
| Swap the user's reserved Primary IP onto the pool server | **~60s** (Hetzner requires power-off to assign) | ✅ stable |
| No pool, dedicated create + reserved IP | minutes, may FAIL | ✅ stable |

**Recommended: swap the IP.** ~60 s is still far better than a full create
(2–10 min) and infinitely better than a create that fails because the region is
sold out. Sell it as "resuming", not "instant".

**This must be verified before it is promised** — whether Hetzner permits
assigning a Primary IP to a running server, and what the real interruption is.
Runbook tier C, cents to test.

### 3.1 The IP question for Claude/Codex specifically

**Status: hypothesis, NOT verified.** No evidence has been gathered that
Anthropic or OpenAI act on datacenter-IP churn for a single subscription.

The concern: park is delete-not-stop, so every wake mints a new public IP. A
user parking/waking twice a day presents one subscription credential from ~60
different datacenter IPs a month. That pattern resembles credential sharing or
resale — not because anything is wrong, but because an abuse heuristic cannot
tell the difference.

What is *known*, and bounds the worry:
- Post-auth CLI traffic from datacenter IPs is routine — Claude Code runs in
  GitHub Actions on Azure-hosted runners.
- The fragile leg is the **interactive browser login**, which Yaver already
  avoids: credentials are mirrored from the user's own machine (residential IP)
  via `cloud-onboarding.tsx`. **Keep it that way — never have the box log in.**

Mitigation is cheap and has independent value regardless of the vendor question:
stable DNS (no A-record re-point per wake), stable SSH `known_hosts`, and
firewall allowlists. €1.20/mo ≈ 4% of revenue.

**Action: verify empirically.** Mirror credentials to `yaver-test-ephemeral`,
run real sessions, park/wake repeatedly, record whether anything trips. A few
hours converts a guess into a fact.

**Termination-grade, and separate from all of this:** one Yaver-held
subscription serving multiple users' runners. That is resale, not IP reputation.
Never build it.

---

## 4. Security posture for Cloud Workspace

Non-negotiables, most already enforced:

1. **VM-level isolation per tenant.** No shared kernel while tenant code runs.
2. **User credentials never leave the user's own machine and their own box.**
   Mirrored from their device; never proxied, stored centrally, or reused.
3. **Provider credentials in Convex env only** — never a table, never a tracked
   file, never a log line, never an error string. Audited clean 2026-07-21;
   errors name the missing *variable*, never its value. See
   `cloud-secrets-and-env.md`.
4. **Relay is not an authorization boundary.** Device keys are. Free vs Pro is
   capacity, not security — which is precisely why sharing a relay is safe.
5. **Cross-tenant isolation is enforced in Convex**, before any bridging:
   `devices.ts:2584`, `userSettings.ts:757-765`.
6. **Fail closed on money and on auth.** Absent policy ⇒ deny. Absent
   credentials ⇒ abort. Unknown cost ⇒ refuse.
7. **Delete stops spend — including satellites** (volume, reserved IP, snapshot,
   AMI + its EBS snapshots). One reclamation path,
   `cloudLifecycle.reclaimAuxResources`.

Known open items: `managedRelays.password` is plaintext in a Convex *table*
(bounded — the relay authorizes nothing — but should be hashed before GA); the
SSH splice lane bypasses `RecordBytes`, so per-tenant accounting is incomplete;
`authorizedManagedKeysChecker` accepts any key in `authorized_keys`, so
`YAVER_SSH_CONTROL` must not be enabled anywhere until fixed.

---

## 5. Work queue

**P0 — money correctness (before any customer)**
1. ✅ `past_due` → disable gateway + park compute. *(Implemented 2026-07-21 — it
   was a live leak: a failing card previously bought indefinite free compute.)*
2. Commit this branch (~1,700 lines, typecheck-clean, uncommitted).
3. Run Hetzner runbook tiers A→C. Live probes found three bugs this session that
   typechecking never would.

**P1 — make it sellable**
4. Shared relay pool (16% → 96%).
5. Wire `cloudPoolPlacement` (warm pool) + decide the IP-swap tradeoff (§3).
6. Included hours scaled by SKU cost (`maxIncludedHoursForTarget`).
7. Usage → invoice: today `managedUsage`/`creditUsage` accumulate and nothing
   ever bills them.

**P2 — later**
8. Multi-provider live-wiring when AWS/GCP/Azure accounts exist.
9. Relay QoS classes (needs `RecordBytes` on the splice lane first).
10. Trial inference with hard caps; shared containers **only** for trials.

---

## 6. The standing lesson

Three separate bugs this session were the same shape — **inventory is not
operation**:

- `cx32`/`cx42`/`cx52`/`gex44` were the default SKUs and **do not exist**.
- Hetzner declared `tagged-cleanup` while `listYaverTaggedResources` returned `[]`.
- The new orphan sweep filtered `yaver=managed` while provisioning writes
  `managed=true` — it would have reported zero orphans forever, and looked
  healthy doing it.

Two of those were in code written specifically to prevent that failure mode.
**Probe the real capability, never the proxy.** Every one was caught by a single
free read-only command.
