# Yaver Cloud Workspace — Product Model

Date: 2026-07-21
Status: **decided.** This file is the canonical statement of what Yaver sells,
what it gives away, and what it must never sell. Engineering decisions that
contradict this file are bugs.

Companion documents:

- `cloud-workspace-commercialization-audit-2026-07-21.md` — what the code
  actually does today, with citations, and the sequenced plan to close the gap.
- `cloud-relay-compute-inference-audit-2026-07-21.md`,
  `cloud-provider-implementation-handoff-2026-07-21.md` — earlier passes.
  Accurate about intent, **optimistic about state**; read them second.

---

## 1. The model in one table

| Plane | Free | Paid | Sold standalone? |
|---|---|---|---|
| **Relay** | ✅ public free relay | Relay Pro (capacity) | yes |
| **Compute** | ❌ — BYO only (user's own machine) | ✅ **Cloud Workspace** | **yes — this is the business** |
| **Inference** | ✅ **trial credits only, capped, time-boxed** | ❌ | **NEVER** |
| **Runner** (Claude Code / Codex / opencode) | user's own subscription | user's own subscription | **NEVER — forbidden by vendor terms** |

**One sentence:** Yaver sells *compute*. Inference is a loaner used to let a new
user taste the product, never a SKU. The runner is always the user's own
subscription, by law.

---

## 2. Why inference is never sold

Three independent reasons. Any one of them is sufficient.

1. **No moat.** The gateway routes through OpenRouter, itself a thin-margin
   router. Marking that up is a price war against the cheapest possible
   competitor, on a product with zero switching cost.
2. **Unbounded, unobservable unit cost.** A VM has a known hourly rate. A prompt
   does not. Per-request COGS cannot be bounded in advance, and today it is
   priced off placeholder rates (`gateway/src/pricing.ts:9-11`), so the true
   margin is unknown.
3. **The runner — the thing users actually want — cannot use it anyway.**
   `claude-code` speaks the Anthropic wire protocol; the gateway is OpenAI-only
   (`desktop/agent/provider_keys.go:143-146`). Managed inference is structurally
   unavailable to the flagship runner, so "sell inference" would mean selling a
   second-class path.

**What inference IS for:** letting someone who has no Claude/ChatGPT
subscription experience Yaver at all. That is a customer-acquisition cost, and
it should be accounted for as one.

---

## 3. Why the runner is always BYO — this is compliance, not pricing

Claude Code, Codex and opencode run under the **user's own subscription login**,
CLI-only. Multi-tenant resale of a subscription CLI violates Anthropic's and
OpenAI's terms. This is not a lever you may pull for revenue.

Concretely forbidden, in order of severity:

1. **One Yaver-held subscription serving multiple users' runners.** This is
   straightforward resale. It gets an account terminated, not rate-limited.
2. Storing or proxying a user's subscription credentials anywhere other than
   their own device and their own box.
3. Headless `-p` mode, or any flow that substitutes an API key for a
   subscription login where the subscription is the product being consumed.

Permitted, and already how the code works: the user's credentials are mirrored
from **their own machine** to **their own box**
(`mobile/app/cloud-onboarding.tsx`). One user, one subscription, their own
hardware.

> ⚠️ **Documentation defect, open.** This rule is cited by
> `docs/autotest-spec.md:204-205,449` and `docs/autorun-remote-handoff.md:149`
> as `feedback_no_api_keys_subscription_only.md` and
> `feedback_no_headless_p_mode.md` — **neither file exists in the repo**, and the
> rule is absent from `CLAUDE.md`. For a constraint this load-bearing, that is
> unacceptable. Write the canonical text.

---

## 4. The free tier

### 4.1 Free forever (costs Yaver ~nothing)

- **Public relay.** Already shipped.
- **BYO compute** — the user's own machine, their own provider account, their
  own token, stored encrypted on their own agent and never sent to Convex
  (`backend/convex/http.ts:6520-6533`). This is a genuinely free product and the
  main funnel.
- **Their own runner subscription.**

### 4.2 Trial inference — the only thing Yaver gives away that costs money

**Purpose:** let a user with no model subscription see Yaver work, once.

**Non-negotiable properties.** Each of these exists because the audit found the
corresponding control missing or fail-open:

| Property | Why |
|---|---|
| **Prepaid and fail-closed** | A trial user cannot be billed after the fact. If the cap fails open, the loss is unrecoverable. |
| **Hard token + spend ceiling per request AND per account** | `gateway/src/index.ts:290` currently estimates worst-case cost from the *output* clamp and has no input ceiling at all. |
| **Time-boxed** | An unexpiring trial is a free tier by another name. |
| **One per verified identity** | Otherwise it is a faucet. Audit `mergeUserInto` for farming loopholes. |
| **Explicitly framed as a loan** | See §4.4. |

**Trials do NOT include compute.** See §4.3 — this is the load-bearing decision.

### 4.3 Decision: the trial lends inference, not a machine

A trial that hands out a VM would give true zero-install onboarding. It was
considered and **rejected for v1** because lending compute is what carries every
expensive failure mode:

- **Abuse.** A free VM with a public IP is the classic vector — mining,
  scraping, spam. `CLAUDE.md` already names the consequence: a datacenter IP
  hammering third parties gets the **entire provider account** suspended,
  including for paying customers. The blast radius of trial abuse is the whole
  business.
- **Orphan cost.** Trials are a high-volume create/destroy engine, and the
  decommission path leaked resources until 2026-07-21 with no sweep able to
  detect it.
- **Egress churn.** §5 — does not apply to trials at all, because a trial user
  has no mirrored subscription credentials to protect.

**Accepted cost of this decision:** the trial does **not** deliver "use the whole
product with no installation". A trial user must still install the agent on
their own machine. The trial removes the *"I need a model subscription"*
barrier — not the *"I must install and authenticate something"* barrier.

**Know which barrier your buyer actually has:**

- *Solo devs* (the stated audience) mostly already pay for Claude or ChatGPT.
  Free inference is not their blocker; install friction and lacking an always-on
  machine are. Trial inference gives them something they do not need.
- *Normie / phone-first users* will not buy a model subscription to evaluate an
  unknown tool. For them, trial inference is the entire unlock.

A zero-install demo box remains a **separate, later, tightly-capped experiment**,
to be built only if conversion data shows install friction is the actual
killer — and only after the Phase 0 reaper and Phase 2 metering gates in the
audit document are green.

### 4.4 Framing: the trial must not read as a downgrade

The trial runs an opencode/GLM-class agent on gateway inference. The paid
product expects the user's own Claude subscription. If the trial is presented as
"Yaver includes AI", conversion feels like a takeaway.

Present it as a loan, in product copy:

> *"We're lending you a model so you can drive today. Bring your own Claude or
> ChatGPT subscription to keep going — Yaver works with the one you already pay
> for."*

The thing the user should fall in love with is Yaver's **orchestration** — coding
from a phone, a box that parks itself, the agent reachable anywhere — not the
model behind it.

---

## 5. Stable egress identity (paid workspaces only)

**The problem.** Park is delete-not-stop, which is correct for Hetzner metering.
But every wake mints a **brand-new public IP**. A user parking and waking twice a
day presents their single Claude/ChatGPT subscription from ~60 different
datacenter IPs a month, across regions. To an abuse heuristic that is close to
the signature of credential sharing or resale — not because anything is wrong,
but because the heuristic cannot tell the difference.

> **Status: hypothesis, not verified.** No evidence has been gathered that
> Anthropic or OpenAI actually act on this pattern. It is being mitigated
> because the fix is cheap and has independent benefits (stable SSH
> `known_hosts`, firewall allowlists, DNS that stops needing re-pointing on every
> wake) — **not** because the threat is confirmed. **Action required:** mirror
> credentials to `yaver-test-ephemeral`, run real sessions, park/wake repeatedly,
> and record whether anything trips. Convert this from a guess into a fact.

**The fix:** one workspace = one reserved egress address, held across park/wake,
released only on decommission.

| Provider | Primitive | Survives server delete | Parked cost |
|---|---|---|---|
| Hetzner | **Primary IP**, `auto_delete:false` | yes | ~€0.50–1.20/mo |
| AWS | Elastic IP | yes (disassociates on terminate) | ~$3.6/mo |
| GCP | Static external IP | yes | ~$3–7/mo |
| Azure | Standard static Public IP | yes | ~$3.6/mo |

**Two decisions worth preserving, both non-obvious:**

1. **Primary IP, never Floating IP.** A Hetzner Floating IP changes what reaches
   the box *inbound*; the box still **sources** outbound connections from its
   primary address. Vendor heuristics see the source. A Floating IP would have
   tested green — reserved ✅, attached ✅ — while the vendor kept seeing a new
   address every wake. Inventory vs. operation.
2. **Availability beats stability.** A reserved Primary IP pins the datacenter,
   which collides with the wake path's location-fallback loop. The pinned
   location is tried first and alone; on capacity failure the wake proceeds
   **without** the IP and reports `egressIpUsed:false` so the caller re-reserves
   and surfaces the change. A workspace that refuses to wake is worse than one
   whose address changed — but it must never change *silently*.

**Eligibility:** paid workspaces only. Never trials (no compute) and never BYO
(not our resource). Auto-release after a long park — a reserved IP costs more
than the parked volume it accompanies, and `egressIpReservedAt` is the clock.

**A reserved IP is a detachable paid resource that outlives its server** — the
same shape as the volume that leaked. It is reclaimed through the single path,
`cloudLifecycle.reclaimAuxResources`, and must never get a second one.

---

## 6. What the user sees

**Never, for the managed product:** a provider picker, a provider token field, or
any ability to influence placement. Yaver chooses the provider from capacity,
cost, credits and health. Provider names may appear as read-only diagnostic
labels.

This rule is **already honored** in the shipped UI — `ManagedCloudPanel.tsx`
exposes plan + `eu`/`us` region only. Provider pickers exist solely on the
explicitly separate **BYO** surfaces, which is correct: there the user is
spending their own money in their own account.

**Surface parity target** (today's gaps in the audit doc §2.6): see workspace,
buy, park/wake, decommission, and read cost — on web, mobile, and CLI at
minimum. The CLI currently has **zero** cloud-workspace commands.

---

## 7. Money rules

1. **Metered usage must become a charge, or it must not be metered.** Today
   `managedUsage`/`creditUsage` accumulate and nothing ever bills them.
2. **Every spend gate fails CLOSED.** Absent policy row ⇒ deny. Absent rate-limit
   binding ⇒ deny. Unknown cost ⇒ deny.
3. **Delete must stop spend — including the satellites.** A server delete does
   not stop a volume, a reserved IP, or a snapshot. All terminal reclamation goes
   through one path.
4. **Never report a resource as stopped while it still bills.** Name the resource
   and its provider id in the error, and make the retry real.
5. **A leak that cannot be detected is worse than one that can.** Provider →
   Convex reconciliation (`listYaverTaggedResources`) is a launch requirement,
   not a nicety. All four providers currently return `[]`.
6. **Cost-awareness is a product feature.** Yaver's wedge is lowering dev opex;
   the bill must be legible to the user, not just to us.

---

## 8. Open decisions

| # | Question | Owner |
|---|---|---|
| 1 | Which audience does the trial target — devs (install friction) or normies (no subscription)? §4.3 turns on this. | product |
| 2 | Trial size: token cap, wall-clock window, one-per-identity enforcement. | product |
| 3 | Long-park IP auto-release threshold (proposed: 30 days). | product |
| 4 | Is the `hosted` tier still a product? It carries grace-period and self-hosted-Convex machinery nothing in the public funnel reaches. | product |
| 5 | SSH keys: `# yaver-managed` caged (System A) or plain bootstrap (System B)? They are mutually exclusive today and A has zero callers. | engineering |
| 6 | Do provider credit terms permit credit-backed *billed* compute, or only free-tier use? | legal |
