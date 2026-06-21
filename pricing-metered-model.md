# Yaver Pricing — Metered Model (base + included hours + overage)

Date: 2026-06-20
Companion to `monetization.md`. That memo argues *what* to sell and *to
whom*. This one specifies *how the meter works* and *what we charge*, for
**both CPU and GPU**, with the no-risk guarantee wired into code.

Everything here is grounded in the actual implementation:
`backend/convex/cloudLifecycle.ts`, `backend/convex/cloudMachines.ts`,
`backend/convex/schema.ts`.

## The model in one sentence

> A monthly subscription includes a default number of **active cloud
> hours** (per machine type). Past that, usage is **metered per hour from
> a prepaid overage wallet**, and the machine **auto-stops** the moment it
> can no longer be paid for — so Yaver never runs CPU or GPU it can't
> bill, and the user never gets a surprise invoice.

This deliberately reconciles the two pulls:
- `monetization.md` said *don't lead with hourly* (normies fear a ticking
  meter). → The hourly part is **overage only**, behind an included grant,
  drawn from a **prepaid** wallet, shown as a **fuel gauge** not a taxi
  meter.
- "We won't risk ourselves." → Overage is **prepaid + auto-stop**, so a
  runaway agent, a forgotten box, or a heavy user can never cost us more
  than the wallet already holds.

## The two normie paths (both bill the same way)

A buyer never opens OpenRouter / GLM / a terminal. They choose one of two
doors on the web UI and pay there; everything after is the Yaver mobile
sandbox driving a remote box.

**Path A — Managed (opens nothing).**
Pay on the web UI → `cloud-agent` → a managed model runs behind the scenes
(GLM / DeepSeek / OpenRouter, dynamically routed, never named to the user)
→ mobile sandbox remote-develops the repo. The user sees "Agent",
"Workspace", "Preview", "Saved" — never "provider", "token", "inference".

**Path B — Bring your own agent.**
The user already has Claude Code / Codex and points it at a Yaver
**managed cloud** box (`cloud-workspace`). Yaver runs the box + private
relay + persistence; the model auth is theirs.

| | Path A `cloud-agent` | Path B `cloud-workspace` |
|---|---|---|
| Web-UI price | $19 / mo | $9 / mo |
| Included model | Yes (managed, invisible) | No (BYO Claude/Codex) |
| Compute meter | base + included hrs + overage | base + included hrs + overage |
| Model meter | managed-model tokens (capped) | none (BYO) |
| Model COGS risk to us | contained by cap + routing | **zero** |
| Private relay | yes | yes (core value) |

Both paths run on the **same compute meter** below. Path A *additionally*
meters managed-model tokens on the separate `managedUsage` meter
(`kind:"inference"`) — that is a token budget with a hard cap, never a
margin we bank on. Path B has no model COGS at all, which is why it is the
safest revenue and the right lead for the developer (HN) audience.

## The machine: one Talos-grade CPU box

We sell **one** CPU SKU, sized so a real monorepo (Talos-class) is
comfortable — not a toy-app box the user outgrows in a week.

| | Old default | New default (shipped) |
|---|---|---|
| Hetzner type | `cpx42` | **`cpx51`** |
| vCPU / RAM / disk | 8 / 16 GB / 320 GB | **16 / 32 GB / 360 GB** |
| Raw COGS basis | €29.99/mo (~4.1¢/hr) | **€54.90/mo (~7.5¢/hr)** |

Why 32 GB: a monorepo tempts concurrent heavy processes — `pnpm install`
across the workspace + monorepo-wide `tsc -b` + a Metro instance — which
together can exceed 16 GB and OOM-kill mid-build, surfacing to the user as
"the agent crashed". 32 GB removes that wall. The Hermes-reload loop
itself is light (~2–4 GB); the monorepo cold-install + typecheck is the
real pressure, and that's what the bigger box buys.

Code: `cloudMachines.ts` `MACHINE_SPECS.cpu` and the COGS in
`cloudLifecycle.ts`. The type string is env-overridable
(`YAVER_CLOUD_CPU_TYPE`) for region/stock swaps with no redeploy.

> ⚠️ Verify before go-live: `cpx51` price/stock with `GET
> /v1/server_types` + `GET /v1/datacenters` ("priced" != "orderable").
> us-region falls back to `ash` — confirm the type is orderable there.
> Prod is fail-closed dry-run until `HCLOUD_TOKEN` is set, so this is safe
> to land now.

## The meter (what the code does)

Per billable tick (hourly cron `meterTick` → `recordUsageAndDeduct`):

1. **Live (active) tick** → consume the subscriber's **included hours for
   that machine type** first (`includedAllowance`, per `userId × period ×
   machineType`). Covered seconds are **free to the user**.
2. Only the **uncovered** seconds hit the **prepaid wallet** at
   `markup × raw` (cpu 2×, gpu 3×, both env-tunable).
3. **Stopped (parked) tick** → for cpx-class boxes the raw stopped rate
   rounds to ~0¢/hr, so a parked workspace is effectively free; the base
   absorbs the snapshot storage. No included-hours drain when stopped.
4. **Auto-stop signal**: a subscriber with included hours **remaining
   never suspends** (next tick is free). Once the grant is spent, the box
   keeps running only while the wallet stays ≥ the snapshot-transition
   reserve (`minimumReserveCents`); below that it is force-stopped
   (snapshot-then-delete, fail-closed) so we can always afford to park it.

New code surface (all in `cloudLifecycle.ts` + one schema table):
- `includedAllowance` table — `userId, period, machineType, plan,
  includedSeconds, usedSeconds` (counters only; privacy-safe).
- `grantIncludedHours({userId, plan, machineType?, period?, hours?})` —
  called by the subscription activation/renewal webhook (P6, `http.ts`)
  or owner tooling. Idempotent per period; a new period = fresh row =
  monthly reset (no reset cron).
- `getAllowance({userId, machineType?})` — drives the "X of 40 hrs left"
  fuel gauge + entitlements.
- `recordUsageAndDeduct` — now consumes included hours before the wallet;
  pay-as-you-go users (no allowance row) are byte-identical to the legacy
  path.

Default included grant (env-overridable): `cloud-agent` and
`cloud-workspace` = **40 CPU-hours**, **0 GPU-hours**. GPU is therefore
pure prepaid overage by default — see below.

## What we charge — number tables

Raw CPU COGS = **7.5¢/active-hr** (cpx51). Overage markup is the policy
lever (`YAVER_CLOUD_MARKUP_CPU`). Recommended: keep base meter at 2× but
price overage at **3×** — overage users are by definition heavy users, and
3× (~22.5¢/hr) is still far under Replit/Cursor.

### CPU — `cloud-agent` $19/mo, 40 included hrs

| User | Active hrs | Our CPU cost | Overage charged (3×) | Revenue | Infra margin* |
|---|---|---|---|---|---|
| Light | 10 | $0.75 | $0 | $19 | ~96% |
| Typical | 25 | $1.88 | $0 | $19 | ~90% |
| Heavy | 40 (all incl.) | $3.00 | $0 | $19 | ~84% |
| Pro | 100 (40+60) | $7.50 | $13.50 | $32.50 | ~77% |
| Whale | 250 (40+210) | $18.75 | $47.25 | $66.25 | ~72% |

\* infra only — before managed-model tokens (Path A), payment fees, and
support. The point: **the CPU box cannot lose money** while auto-stop
fires and Path-A tokens are capped. Every overage hour is sold at ~3× cost
and prepaid, so the whale row is *more* profitable, not a risk.

### GPU — opt-in, prepaid overage, 0 included by default

GPU is sold the **same way** (subscription can grant a few GPU-hours; past
that it's metered) but with two honesty rules baked in:

1. **Source it cloud-hourly** (RunPod/Vast/Lambda), where the per-hour
   cost is real and the box can spin down. A Hetzner *dedicated* GPU
   (GEX44) is a fixed monthly bill that auto-stop **cannot** reduce to $0
   — only sell that as a separate flat "private GPU" SKU, never as
   metered-with-autostop.
2. **0 included hours by default, hard prepaid cap, faster auto-stop**
   (idle GPU bleeds 10–20× a CPU; stop at 5–10 min idle, not 30).

| GPU (cloud-hourly) | Raw/hr | Markup | User/hr |
|---|---|---|---|
| RTX 4090 | ~$0.44 | 3× | ~$1.32 |
| A100 40GB | ~$1.20 | 3× | ~$3.60 |

The no-risk guarantee is identical to CPU and already enforced in code:
`canStart` blocks a GPU resume the wallet can't afford; the suspend floor
uses the **gpu** rate + 3× markup, so a GPU box auto-stops while the wallet
can still pay for the snapshot. We never run a GPU we can't bill.

## Why this is "no risk" — the invariants

1. **Prepaid, never postpaid.** Overage draws a wallet the user topped up.
   When it can't cover the next tick + reserve → auto-stop. Max loss to
   the user = what they prepaid; max exposure to us = one tick + the
   snapshot we always take anyway.
2. **Included ≠ unlimited.** The grant is a per-period ceiling
   (`includedSeconds`); past it, real money or stop.
3. **Compute only runs while payable.** `canStart` gate on start/resume +
   suspend floor on every tick. True for CPU and GPU.
4. **Fail-closed everywhere.** No `HCLOUD_TOKEN` → dry-run (no real spend).
   Snapshot fails → delete aborts (no data loss). These predate this
   change and still hold.
5. **Stopped is ~free.** Parked workspaces cost snapshot storage (~0¢/hr
   rounded), absorbed by the base — so "keep my workspace saved" is a
   promise we can afford to keep indefinitely.

## Wiring left (not in this change)

- **Webhook → grant.** On subscription `active`/renewal, call
  `grantIncludedHours({userId, plan})` (and a GPU grant if the plan
  includes GPU hours). One line in `http.ts`; left to the billing owner.
- **Meter cron.** `crons.interval("cloud meter", {hours:1},
  internal.cloudLifecycle.meterTick, {intervalSeconds:3600})` — the
  one-liner already noted in `cloudLifecycle.ts`.
- **Fuel-gauge UI.** Surface `getAllowance` + wallet balance as "X of 40
  hrs left · $Y overage credit" — a gauge, not a per-second meter
  (`monetization.md` §"Why Monthly Beats Hourly").
- **Idle auto-stop trigger.** The meter enforces the *budget* floor; the
  *idle* stop (30 min CPU / 5–10 min GPU, never during build/task/git) is
  the agent-side watchdog → `pauseMachine`.
- **Entitlements query.** Combine plan + `getAllowance` into the
  backend-computed entitlement shape in `monetization.md` §Entitlements.

## Summary

- One Talos-grade CPU box (cpx51, 32 GB) — shipped.
- Subscription grants included active-hours per machine type — shipped
  (`includedAllowance` + `grantIncludedHours` + metering).
- Overage = prepaid wallet × markup, auto-stop when unpayable — shipped
  (CPU and GPU share the mechanism; GPU defaults to pure overage).
- Two normie doors (managed model / BYO agent) bill on the same compute
  meter; managed adds a capped token meter, BYO adds nothing.
- Net: calm monthly price on the outside, strict prepaid+auto-stop meter
  on the inside, zero chance of running compute we can't bill.
