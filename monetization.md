# Yaver Monetization Analysis

Date: 2026-06-20

This memo is intentionally product and engineering mixed. The goal is to give
Claude Code or another agent enough context to challenge the business model,
the pricing, and the actual Yaver implementation path.

## Summary

Yaver should not sell "GPU time", "inference", "tokens", or "cloud VMs" to
normal users. The public product should be one simple thing:

**Yaver Cloud Agent - $19 starter credit**

- AI coding agent from phone or web
- GitHub projects saved
- Cloud workspace included
- Included managed model
- Auto-stops when idle
- Reopens later with repos and setup preserved

For power users who already pay for Claude Code, Codex, ChatGPT, OpenRouter, or
Anthropic, add a second plan:

**Yaver Cloud Workspace - $9 starter credit**

- Same persistent remote workspace and mobile control
- Bring your own AI account, paid plan, or API key
- Includes a Yaver private relay
- Yaver monetizes the workspace, mobile cockpit, runner wiring, persistence,
  private connectivity, previews, and auto-stop
- No included managed model cost

Do not lead with raw hourly billing or app-store subscription language. Internally everything should still be metered by active machine seconds, model tokens, storage, and worker cost. Externally, normies should buy a simple web-billed infrastructure credit that stays under the Claude Pro / ChatGPT Plus mental ceiling. Auto-stop should be a trust feature, not a pricing footnote.

## Market Position

The current normie baseline is a $20/month AI chat subscription. Claude Pro is
listed by Anthropic at $20/month, with higher Max tiers at $100/month and
$200/month:

- https://support.claude.com/en/articles/11049762-choose-a-claude-plan

The Yaver wedge is not "better model than Claude". That is not controllable or
durable. The wedge is:

> Claude chats about code. Yaver opens your project, changes files, runs it,
> previews it on your phone, and stops the cloud machine when you leave.

For normal users, the value is workflow completion:

1. Connect GitHub.
2. Pick a project.
3. Ask for a change from phone.
4. Agent edits the repo.
5. Preview opens.
6. Yaver checkpoints/saves.
7. Machine auto-stops.
8. User returns later and the workspace is still there.

The model can be DeepSeek, GLM, OpenRouter, or a dynamically selected backend.
The user should not need to know.

## Proposed Plans

### Plan 1: Yaver Cloud Agent

Price:

- $19 starter cloud credit
- Included managed model
- Included saved workspace
- Fair-use active cloud coding time
- Auto-stop enabled by default

Internal cap proposal:

- 40 active workspace hours per starter credit window
- 1 active task at a time
- 1 active workspace by default
- Repo size cap, for example 2 GB checked out excluding dependencies
- Agent step cap per task
- Build/install loop watchdog

User-facing copy:

> Build from your phone. Yaver opens your GitHub project, makes changes, runs
> previews, and stops itself when idle. Your workspace stays saved.

Avoid user-facing wording like "GPU", "tokens", "DeepSeek", "inference",
"serverless", "Hetzner", "CPU", "VM", or "OpenCode" on the main purchase path.

### Plan 2: Yaver Cloud Workspace

Price:

- $9 starter cloud credit
- BYOK/BYO paid AI account
- Same saved workspace and mobile/web control
- Yaver private relay included automatically
- No included managed model

Target users:

- Claude Code fanatics
- Codex/ChatGPT subscribers
- OpenRouter users
- Anthropic/OpenAI API users
- Developers who want Yaver as remote/mobile infrastructure, not a model vendor

Internal cap proposal:

- 40 active workspace hours per starter credit window
- 1 active workspace by default
- User pays model/provider costs directly
- Yaver still meters CPU/storage to protect infra cost

User-facing copy:

> Already use Claude Code or Codex? Bring it to a Yaver cloud workspace and run
> it from your phone. Includes a private Yaver relay so your devices stay
> reachable away from home.

The private relay is important because this plan is not only "cheap cloud CPU".
It is the paid connectivity layer for people who already have their own AI
account or model provider. A Claude Code/Codex fanatic may not want an included
model, but they will pay for:

- a persistent remote workspace
- phone/web access from anywhere
- private relay fallback when LAN/Tailscale/Cloudflare is absent
- saved runner auth/config
- previews and dev-server routing
- auto-stop and recovery

So the $9 plan should be positioned as "your remote coding cockpit", not
"bring your own key discount".

### Plans Not For Launch

Do not launch these on day one:

- Private GPU
- Team seats
- Unlimited plan
- Dedicated machine picker
- Token pack UI
- Model/provider picker for normies

These can exist internally or as invite-only flags. They should not complicate
the first purchase decision.

## Why Simple Infrastructure Credit Beats Raw Hourly For Normies

Hourly is rational for infrastructure buyers. Normies are not infrastructure
buyers. They are used to:

- ChatGPT Plus
- Claude Pro
- Cursor
- Replit
- GitHub Copilot

These are simple predictable purchase mental models. A normal user sees "$0.50/hour" and thinks:

- "What if I forget it on?"
- "How much will this cost me this month?"
- "Is the agent thinking while I am asleep?"
- "Will a bug charge me?"

The product should instead say:

- "$19 starter credit"
- "Auto-stops when idle"
- "Fair-use cloud coding included"
- "Your workspace stays saved"

Internally, the system must still enforce active-hour and token budgets. The pricing simplicity should not mean unlimited compute, and the purchase must remain web-billed managed infrastructure rather than an in-app mobile subscription or in-app purchase.

## Unit Economics

### Current Code Assumptions

The repo already has managed-cloud cost assumptions in
`backend/convex/cloudLifecycle.ts`:

- CPU raw COGS basis: Hetzner `cpx42` at EUR 29.99/month
- Approx raw live cost: 2999 cents / 730 hours = about 4.1 cents/hour
- Default CPU markup: 2x
- Stopped raw storage estimate: about 50 cents per 30 days
- Minimum reserve: stopped storage window plus one live hour

Relevant code:

- `backend/convex/cloudLifecycle.ts`
- `backend/convex/cloudMachines.ts`

The CPU SKU in code is:

- Hetzner `cpx42`
- 8 vCPU
- 16 GB RAM
- 320 GB disk
- amd64

This is a good first workspace SKU. It is enough for React Native/Hermes,
Node, web previews, git operations, and many builds. It is not a serious local
LLM box.

### Gross Margin Sketch: Cloud Agent at $19 Starter Credit

Target average user behavior:

- Machine stopped most of the time
- 5 to 20 active hours/month
- Cheap managed model routing
- Small to medium repos
- Mostly web/RN preview work, not huge native builds

Rough cost envelope:

- CPU active cost: 20 hours * $0.041 = $0.82 raw
- Storage/snapshot: $0.50 to $3.00 depending implementation
- Model usage with DeepSeek/GLM-class provider: highly variable, but should be
  low single-digit dollars for ordinary users if capped
- Payment/support/infra overhead: $2 to $5 target

This can work at $19 if the system prevents runaway usage.

Danger cases:

- User runs agent all day as a professional developer.
- Agent loops on failing installs/builds.
- Repo indexing repeatedly burns context.
- Native build loops dominate CPU.
- Included model is too strong/expensive by default.
- Multi-hour background task continues after user leaves.

These must hit caps, idle detection, step limits, or upgrade prompts.

### Gross Margin Sketch: Cloud Workspace at $9 Starter Credit

This plan has lower revenue but much lower variable model risk. It also
includes private relay value, which makes the plan feel materially useful even
to users who already have Claude Code/Codex set up locally.

Target average user behavior:

- User already has Claude/Codex/OpenAI/OpenRouter spend
- Yaver provides CPU workspace and mobile control
- Model inference cost is externalized

Rough cost envelope:

- CPU/storage similar to Cloud Agent
- No managed model cost
- Private relay traffic and/or relay sidecar cost
- Support overhead may be higher because power users expect deeper runner
  fidelity

The relay can be delivered two ways:

1. **Managed relay row / dedicated relay service**
   - Existing `backend/convex/managedRelays.ts` models per-user managed relays.
   - Good for users who do not have a Yaver Cloud workspace running.

2. **Cloud workspace doubles as user relay**
   - `backend/convex/cloudMachines.ts` has `boxRelayPassword` support.
   - `desktop/agent/Dockerfile.yaver-cloud` includes `yaver-relay` and starts it
     when `RELAY_PASSWORD` is set.
   - This is attractive for the $9 Workspace plan: the user's paid workspace can
     also be their private relay endpoint.

For launch, avoid promising "dedicated relay VM" unless the implementation
really provisions one. Safer copy:

> Includes Yaver private relay for reliable remote access.

Internally, that can map to a relay sidecar on the cloud workspace or a managed
relay row depending on availability and cost.

This plan monetizes users who would otherwise say: "I already pay Claude Max,
why would I pay for your model?"

The answer:

> Do not pay us for the model. Pay us for the remote workspace that makes your
> existing agent usable from phone, private-relayed, persistent, recoverable,
> and auto-stopped.

## Inference Strategy

### Public Product

Do not sell inference.

The product is "Cloud Agent". The user buys an outcome, not a backend resource.

### Internal Architecture

Split the system into two resources:

1. **Workspace Machine**
   - Per user or per workspace
   - Persistent identity
   - Stores repos, `.yaver`, runner config, cached deps, task state
   - Cheap CPU
   - Can stop/restart safely

2. **Inference Pool**
   - Shared
   - Stateless or mostly stateless
   - Dynamically routed
   - Can be managed API, rented GPU, own GPU, or BYOK
   - Should be invisible to normal users

Most users should not get a dedicated GPU. A dedicated GPU per user destroys
margin because coding workloads are bursty and idle-heavy.

Routing order for included plan:

1. Cheap fast model for classification, small edits, summaries, and simple
   tool decisions.
2. Stronger coding model for actual patch generation.
3. Reasoning/thinking model only when stuck or requested by task type.
4. GPU pool only when sustained load justifies it economically.

The workspace can run OpenCode, but the inference should be allowed to route to
managed providers behind the scenes.

### Current Model Cost Context

DeepSeek's official API pricing is far below Claude API pricing on the current
pricing page. The page currently lists non-thinking and thinking modes for
DeepSeek's newer model family and states legacy `deepseek-chat` /
`deepseek-reasoner` names will be deprecated on July 24, 2026:

- https://api-docs.deepseek.com/quick_start/pricing

Anthropic's API model pricing is much higher, especially for Opus/Sonnet
output tokens:

- https://platform.claude.com/docs/en/about-claude/models/overview

Conclusion:

- Included plan should default to cheap managed coding models.
- Claude/Codex should be BYOK/BYO paid AI account unless there is explicit
  metered pass-through with markup.
- Never promise "unlimited Claude-like coding" for $19.

## GPU Strategy

GPU should be an internal scaling option or an invite-only advanced SKU, not
the launch product.

Current public GPU rental signals:

- RunPod pricing page: https://www.runpod.io/pricing
- Lambda pricing page: https://lambda.ai/pricing
- Vast pricing page: https://vast.ai/pricing

Broad reality:

- RTX 4090/L40S/A100/H100 hourly costs are much higher than the CPU workspace.
- Coding users are bursty, so a per-user GPU will sit idle.
- Shared GPU inference can work only with batching, queueing, and fast scale
  down.
- GPU stock, cold starts, provider reliability, and abuse risk increase ops
  burden.

Launch policy:

- No GPU plan on public pricing page.
- No "GPU" word in normie onboarding.
- Add "private GPU" later for teams, privacy, or heavy users.
- Require prepaid balance, hard spend cap, and aggressive idle reaping.

## Auto-Stop As Trust Feature

Auto-stop should be first-class:

> We shut the workspace down when you are not using it. Your repos and setup
> stay saved.

Default behavior:

- Warn at 25 minutes idle.
- Auto-stop at 30 minutes idle.
- One-tap keep running.
- Never stop during active agent task, package install, build, deploy, git
  operation, dev server compile, file write, or terminal command.
- Hard stop when prepaid active budget is exhausted.
- Stop before balance goes below recovery/storage reserve.

Important distinction:

- "Stop" must not mean power off only if the provider keeps billing powered-off
  machines.
- Current `desktop/agent/cloud_stopstart.go` correctly models stop as
  snapshot-then-delete for Hetzner so billing halts, with fail-closed safety:
  if snapshot fails, delete is aborted.

Persistence invariant:

- A workspace must never be deleted without a recoverable snapshot and a git
  checkpoint path.

## Persistence Model

Use three layers:

1. **Git remote**
   - Canonical user recovery path.
   - Encourage GitHub connect before meaningful work.
   - Auto-create checkpoint branch or commits for long-running tasks.

2. **Persistent workspace volume/state**
   - Current cloud init writes persistent state to `/srv/yaver/state`.
   - Workspaces live under `/srv/yaver/state/Workspace`.
   - Runner auth/config and `.yaver` state survive container restarts.

3. **Provider snapshot/golden image**
   - `desktop/agent/byo_golden.go` already supports bake-once boot-many
     snapshots for faster starts.
   - Stop/start should snapshot user state before deleting billable compute.

The purchase promise is:

> Stop anytime. Restart later. Your repos and agent setup are still there.

That promise is more important than raw model quality for trust.

## Current Repo Fit

The repo already has many of the needed pieces.

### Backend

Existing:

- `backend/convex/cloudMachines.ts`
  - Managed machine creation.
  - CPU/GPU SKU definitions.
  - Cloud-init generation.
  - OpenCode config for managed coding plan.

- `backend/convex/cloudLifecycle.ts`
  - Wallet.
  - Metering.
  - Estimated hourly cost.
  - Minimum reserve.
  - Auto-stop/suspend hooks.

- `backend/convex/http.ts`
  - `/billing/yaver-cloud/checkout`
  - `/billing/yaver-cloud/balance`
  - `/billing/yaver-cloud/provision`
  - `/billing/yaver-cloud/start`
  - `/billing/yaver-cloud/stop`
  - `/billing/credits/checkout`
  - `/billing/subscription`

Current checkout is LemonSqueezy-oriented. User said Stripe official setup will
come later. So the next implementation should be provider-neutral plan catalog
first, Stripe env wiring second.

### Desktop Agent

Existing:

- `desktop/agent/cloud.go`
  - CLI cloud buy/create/status/ssh paths.

- `desktop/agent/ops_cloud.go`
  - Ops/MCP managed-cloud verbs.

- `desktop/agent/cloud_stopstart.go`
  - Snapshot-delete stop and snapshot-start primitives.

- `desktop/agent/runner_auth_setup.go`
  - Runner setup/auth flows.

- `desktop/agent/opencode_config.go`
  - OpenCode provider/model config surface.

### Mobile/Web

Existing:

- `mobile/src/lib/managedCloudFlow.ts`
  - Post-purchase mobile setup flow.
  - Waits for box, then mirrors runner auth or sets up OpenCode/GLM.

- `web/components/dashboard/ManagedCloudPanel.tsx`
  - Managed cloud UI.
  - Balance/top-up/provision controls.

- `mobile/src/components/ManagedCloudCard.tsx`
  - Mobile cloud access/status.

Needed:

- Replace infra-style "balance, spin up CPU box, prepaid" language for normies.
- Add simple plan selection: Cloud Agent vs Cloud Workspace.
- Keep prepaid wallet in admin/dev/advanced surfaces or behind settings.
- Make auto-stop and saved workspace the hero promise.

## Billing And Stripe Plan

Since official Stripe settings will be configured later, do not hardcode live
Stripe product/price IDs now. Add env placeholders and a provider-neutral plan
catalog.

Proposed plan IDs:

- `cloud-agent`
- `cloud-workspace`

Proposed env placeholders:

- `STRIPE_SECRET_KEY`
- `STRIPE_WEBHOOK_SECRET`
- `STRIPE_CLOUD_AGENT_PRICE_ID`
- `STRIPE_CLOUD_WORKSPACE_PRICE_ID`
- `STRIPE_BILLING_PORTAL_RETURN_URL`
- `STRIPE_CHECKOUT_SUCCESS_URL`
- `STRIPE_CHECKOUT_CANCEL_URL`

Temporary compatibility:

- Existing LemonSqueezy checkout can stay as dev/legacy provider.
- New checkout endpoint should accept `{ planId }`.
- If Stripe env is absent, return a clean "billing not configured" message
  or fall back to existing LemonSqueezy only in dev/owner preview.

Webhook requirements:

- Idempotency by Stripe event ID.
- Paid infrastructure entitlement/checkout status synced to backend billing tables.
- Plan or credit product stored as `cloud-agent` or `cloud-workspace`.
- Customer ID stored.
- Cancel/update/delete events reconciled.
- Daily reconcile job remains necessary because webhook-only billing is fragile.

Do not allow a forged webhook to provision compute. The existing LemonSqueezy
signature code already moved fail-closed; Stripe must be the same.

## Entitlements

Entitlements should not be inferred from UI state. Backend should compute them.

Suggested entitlement shape:

```json
{
  "plan": "cloud-agent",
  "status": "active",
  "includedModel": true,
  "byok": false,
  "activeHoursMonthly": 40,
  "maxWorkspaces": 1,
  "maxConcurrentTasks": 1,
  "autoStopMinutes": 30,
  "repoSizeGb": 2
}
```

For `cloud-workspace`:

```json
{
  "plan": "cloud-workspace",
  "status": "active",
  "includedModel": false,
  "byok": true,
  "privateRelay": true,
  "activeHoursMonthly": 40,
  "maxWorkspaces": 1,
  "maxConcurrentTasks": 1,
  "autoStopMinutes": 30,
  "repoSizeGb": 2
}
```

The agent/mobile/web should ask backend "what can this user do?" rather than
duplicating pricing rules across clients.

## Abuse And Cost Controls

Required before public launch:

- Active-hour meter per workspace.
- Token meter per included-model plan.
- Agent step cap.
- Max task wall-clock duration.
- Max concurrent task count.
- Idle auto-stop.
- Build/install watchdog.
- Egress policy and rate limits.
- Repo size and file count caps.
- Snapshot/gift branch before destructive actions.
- Suspicious account throttling.
- Payment failure lockout.
- Low-balance or over-cap stop before provider cost continues.

This matters because Yaver runs arbitrary user code on cloud machines. Even
well-intentioned users can accidentally run infinite loops, huge package
installs, crypto miners from compromised dependencies, or aggressive network
jobs. The repo's existing safety posture in `CLAUDE.md` should apply here too.

## Product UX

### Main Purchase Page

One primary CTA:

**Start Yaver Cloud Agent - $19 starter credit**

Secondary link:

**Already have Claude Code or Codex? Use Cloud Workspace - $9 starter credit**

Do not show a three-card pricing grid at launch unless there is a real third
plan. The decision should feel obvious.

### Onboarding Flow

Cloud Agent:

1. Sign in.
2. Connect GitHub.
3. Pick repo.
4. Yaver creates/starts workspace.
5. User asks first task.
6. Preview/status shown.

Cloud Workspace:

1. Sign in.
2. Connect GitHub.
3. Pick "I use Claude/Codex/OpenRouter".
4. Yaver starts workspace.
5. Yaver configures private relay for the workspace.
6. Runner auth flow opens.
7. User starts task.

### In-App Copy

Use:

- "Workspace"
- "Agent"
- "Project"
- "Preview"
- "Saved"
- "Stopped"
- "Reopen"

Avoid:

- "GPU"
- "Inference"
- "Token"
- "Provider"
- "Hetzner"
- "VM"
- "cpx42"
- "OpenCode" for normies

Power-user screens can expose runner/model/provider details, but not the normie
first run.

## Implementation Roadmap

### Phase 0: Product Copy And Plan Catalog

- Add shared plan constants: `cloud-agent`, `cloud-workspace`.
- Update web marketing copy to remove "no paid tiers" language once ready.
- Add simple cloud pricing section.
- Keep existing free/open-source positioning for self-hosted Yaver.

### Phase 1: Checkout Shape

- Change `/billing/yaver-cloud/checkout` to accept `planId`.
- Store plan ID in checkout metadata/custom data.
- Keep LemonSqueezy fallback for dev if Stripe env is absent.
- Add Stripe placeholders only, no live secrets.

### Phase 2: Entitlements

- Add backend entitlement query.
- Map cloud plan to included model vs BYOK.
- Gate managed model access on `cloud-agent`.
- Gate workspace creation on either active plan.
- Grant private relay entitlement on both plans, with `cloud-workspace` using it
  as a core value proposition.

### Phase 3: UI

- Web purchase page with one main plan and BYOK secondary option.
- Mobile cloud onboarding shows plan status, not infrastructure controls.
- Move prepaid wallet/spin-up controls to advanced/dev page.
- Show auto-stop promise everywhere cloud is mentioned.

### Phase 4: Auto-Stop And Safety

- Implement idle detection at workspace level.
- Add warning event at 25 minutes.
- Stop at 30 minutes if no active protected operation.
- Ensure snapshot/git checkpoint before stop.
- Add tests for "do not stop during build/task/git/package install".

### Phase 5: Included Model Metering

- Meter included-model usage per user per starter credit window.
- Route cheap model first.
- Add over-cap behavior: slow down, ask to wait, or suggest upgrade.
- Keep provider/model invisible on normie plan.

### Phase 6: Stripe Official Setup

- Create Stripe products/prices or credit-pack prices manually.
- Set env price IDs.
- Implement webhook verification.
- Add billing reconcile.
- Test cancel/update/payment_failed flows.

## Key Risks

### Pricing Risk

$19 can fail if included model usage is too generous. The fix is not raising
the public price immediately; the fix is caps, routing, and step limits.

### Quality Risk

Cheap models may underperform Claude Code. The product must compensate with
workflow, previews, retries, and narrow tasks. For users who demand Claude, sell
Cloud Workspace BYOK.

### Trust Risk

Any surprise billing or lost repo destroys trust. Auto-stop and persistence are
more important than squeezing extra active-hour revenue.

### Compliance Risk

Reselling Claude/ChatGPT paid-plan capacity is dangerous. Do not pool or
resell user-facing paid accounts. For Claude/Codex fanatics, use BYOK/BYO
paid AI account and charge for Yaver workspace infrastructure.

### Infrastructure Risk

Stop/start must be reliable. A powered-off server may still bill; snapshot and
delete is the correct cost stop for Hetzner, but it raises recovery complexity.

### Support Risk

Normies will ask product questions, not infra questions. The UI must avoid
provider/model terminology and present failures as recoverable project states.

## Final Recommendation

Launch with two plans, but present one as primary:

1. **Yaver Cloud Agent - $19 starter credit**
   - Default.
   - Included managed model.
   - Saved cloud workspace.
   - Auto-stop.
   - Normie-friendly.

2. **Yaver Cloud Workspace - $9 starter credit**
   - Secondary.
   - BYOK/BYO paid AI account.
   - Private Yaver relay included.
   - For Claude Code, Codex, ChatGPT, and OpenRouter users.
   - Monetizes power users without taking model COGS risk.

Internally keep the current metered architecture. Externally, sell calm,
predictable infrastructure credit with auto-stop. The core promise is:

> A coding agent that actually works on your GitHub project from your phone,
> costs less than a typical chat subscription, and stops itself so you do not get
> surprised.
