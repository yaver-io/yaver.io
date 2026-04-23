# Yaver Deployment Audit — 2026-04-23

This audit compares the current codebase against the intended product direction:

- one mobile-first control plane
- multiple execution targets
- zero-terminal happy path where possible
- managed cloud + BYO + local hardware + shared host + runtime-only + builder paths
- payment-gated managed cloud with LemonSqueezy sandbox wired end to end

This document is intentionally implementation-first. It answers:

1. what is already implemented in code
2. what is only partial / preview-grade
3. what plumbing is still missing
4. what the current LemonSqueezy sandbox path actually does

## Executive Summary

The repo already contains substantial pieces of the platform:

- strong auth/bootstrap/pairing foundations
- real guest-sharing and partial multi-user foundations
- install, git, vault, build, and phone-project transport surfaces
- Convex tables and HTTP routes for cloud machines
- LemonSqueezy checkout + webhook + sandbox mode wiring
- a shared-tenant `cloud.yaver.io` deployment path for phone projects

But the repo does **not** yet have one unified deployment system.

The biggest current architectural fact is:

- there is an older **shared tenant cloud** path under `cloud/`
- and a newer **per-user dedicated cloud machine** path under `backend/convex/cloudMachines.ts`

Those two paths are not fully unified.

The biggest current ship blockers for true managed per-user cloud are:

1. dedicated machine provisioning installs tools but does not fully bootstrap a running, enrolled `yaver serve` service
2. billing/subscription logic is still relay-first and only partially generalized to cloud machines
3. cloud auth is still split between preview shared-secret flow and future per-user machine ownership
4. there is no unified target abstraction across cloud machine / BYO / local hardware / builder / runtime-only

## Current-State Scorecard

### A. Identity Layer

Implemented:

- Yaver account sessions and token validation in Convex
- linked auth providers and account merge foundations
- device registration and ownership
- bootstrap pairing for unauthenticated machines
- remote auth recovery for machines that lost auth
- guest invite / accept / revoke flows
- team tables and team membership tables

Primary files:

- `backend/convex/auth.ts`
- `backend/convex/schema.ts`
- `desktop/agent/auth_bootstrap.go`
- `desktop/agent/auth_recover.go`
- `backend/convex/guests.ts`
- `mobile/src/lib/guests.ts`

Assessment:

- **good foundation**
- this is ahead of the rest of the deployment stack

Gaps:

- no single identity model spanning all target types
- cloud-machine ownership, shared-host membership, and builder attachment are still separate concepts
- dedicated managed-cloud auth is still split between future machine ownership and current preview token model

### B. Target Layer

Implemented:

- `devices` table for user-owned agents
- `cloudMachines` table for managed machines
- `managedRelays` table for managed relay infra
- phone project abstractions
- some team-owned machine support via `teamId`

Primary files:

- `backend/convex/schema.ts`
- `backend/convex/cloudMachines.ts`
- `backend/convex/managedRelays.ts`
- `mobile/src/lib/phoneProjects.ts`

Assessment:

- **partial**

Missing:

- no unified `Target` abstraction
- no normalized capability model
- no common type spanning:
  - managed cloud
  - BYO VPS
  - local hardware
  - shared host
  - builder target
  - mobile runtime

Impact:

- onboarding and UI logic remain target-specific
- runtime convergence after bootstrap is not yet represented in data model

### C. Provisioning Layer

Implemented:

- managed relay provisioning exists
- cloud machine creation exists in Convex
- Hetzner API provisioning exists for cloud machines
- Cloudflare DNS creation exists
- custom-domain binding records exist
- preview shared-cloud activation exists
- destroy/deprovision action exists for cloud machines

Primary files:

- `backend/convex/cloudMachines.ts`
- `backend/convex/provisionRelay.ts`
- `backend/convex/http.ts`
- `cloud/deploy.sh`
- `cloud/docker-compose.yml`

Assessment:

- **preview-grade / partial**

Critical gap:

The dedicated cloud machine provisioning flow in `backend/convex/cloudMachines.ts` installs packages and writes TLS-reconciler files, but it does **not** complete the full managed-machine bootstrap expected by the product:

- no verified creation of a real auth token for the agent
- no `config.json` generation for `yaver serve`
- no systemd service for `yaver serve`
- no machine enrollment into Convex as a normal device
- no proof that the box becomes reachable as a user-owned Yaver target

The health check assumes the provisioned machine will answer:

- `http(s)://<hostname>:18080/health`

but the cloud-init currently only guarantees package installation plus TLS reconciler, not a running Yaver daemon.

Conclusion:

- **dedicated per-user cloud provisioning is not fully operational yet**

### D. Runtime Layer

Implemented:

- install manager
- public package registry merge
- git status/log/diff/branch APIs
- vault CRUD APIs
- build manager + artifact serving
- phone project export / receive / promote
- runner auth detection for Claude/Codex/local tools
- guest policy enforcement
- partial multi-user runtime

Primary files:

- `desktop/agent/install_http.go`
- `desktop/agent/git_http.go`
- `desktop/agent/vault_http.go`
- `desktop/agent/build_http.go`
- `desktop/agent/phone_backend_http.go`
- `desktop/agent/runner_auth.go`
- `desktop/agent/guest_scope.go`
- `desktop/agent/multiuser.go`

Assessment:

- **strong for single-agent runtime**
- this is one of the strongest parts of the repo

Gaps:

- runtime capability reporting is not normalized as target capabilities
- no generic builder-target lifecycle
- no managed-cloud-first runtime bootstrap path
- no dedicated-machine-specific first-run orchestration

### E. UX Orchestration Layer

Implemented:

- mobile vault screen
- mobile builds screen
- mobile guests screen
- mobile phone project flows
- web pricing preview page
- CLI cloud preview helpers
- desktop installer provisioning status for managed relay preview

Primary files:

- `mobile/app/vault.tsx`
- `mobile/app/(tabs)/builds.tsx`
- `mobile/app/(tabs)/guests.tsx`
- `mobile/src/lib/phoneProjects.ts`
- `web/app/pricing/page.tsx`
- `desktop/agent/cloud.go`
- `desktop/installer/src/renderer.js`

Assessment:

- **good surface area, not yet unified**

Gaps:

- no single setup wizard spanning all target kinds
- no target capability-driven UX
- no unified provisioning timeline view for managed cloud / BYO / local hardware
- mobile can use many runtime features but target selection/setup is still fragmented

## Deployment Target Audit

### 1. Managed Yaver Cloud

There are currently two separate implementations.

#### 1A. Shared-tenant cloud box

Implemented:

- `cloud/` Docker deployment
- `CLOUD_OWNER_TOKEN` shared-secret auth
- `yaver serve` behind Caddy
- phone project push to `/phone/projects/receive`
- explicit docs for shared-tenant managed cloud

Primary files:

- `cloud/README.md`
- `cloud/docker-compose.yml`
- `cloud/Dockerfile`

Assessment:

- **real but MVP-only**

Limitations:

- single shared secret
- not per-user machine ownership
- not real dedicated-machine provisioning
- docs explicitly say billing hooks and per-user isolation are post-MVP

#### 1B. Dedicated per-user cloud machine

Implemented:

- `cloudMachines` table and provisioning action
- Hetzner server creation
- DNS record creation
- machine rows exposed by `/machines`

Primary files:

- `backend/convex/cloudMachines.ts`
- `backend/convex/http.ts`

Assessment:

- **not complete**

Missing plumbing:

- bootstrapped `yaver serve`
- auth token injection
- device registration
- enrollment back into normal device list
- post-provision setup wizard state
- user-facing machine attach flow from mobile

### 2. BYO VPS

Implemented:

- generic agent runtime
- bootstrap pairing
- auth recovery
- install tooling
- git/vault/build runtime
- docs for self-hosting and Hetzner shared owner workflows

Primary files:

- `desktop/agent/auth_bootstrap.go`
- `desktop/agent/auth_recover.go`
- `docs/hetzner-shared-owner-runbook.md`

Assessment:

- **runtime is real**
- **bootstrap UX is not yet productized as a provider-aware BYO flow**

Missing:

- provider-agnostic target model
- cloud-init / user-data generator as first-class product path
- “adopt existing VPS” wizard
- provider API integrations
- migration flow between BYO and managed cloud

### 3. Local Hardware Cloud

Implemented:

- LAN discovery
- QR pairing
- bootstrap mode
- auth recovery
- auto-start/systemd/launchd helpers
- docs for Pi / headless / auto-boot

Assessment:

- **quite strong**

Missing:

- appliance/image productization as a first-class target type
- unified target-level onboarding shared with managed/BYO

### 4. Shared Host / Guest

Implemented:

- guest access data model
- guest config sync
- guest scope enforcement
- per-project and per-runner restrictions
- containerization/isolation controls
- host-share / borrowed-runner primitives
- repo bus for guest-owned repos

Primary files:

- `desktop/agent/guest_scope.go`
- `desktop/agent/guest_http.go`
- `desktop/agent/host_share_cmd.go`
- `docs/host_guest_borrowed_runner_spec.md`
- `docs/host_share_connectivity_robustness_report.md`

Assessment:

- **real and substantial**

Still missing:

- typed brokered host actions as the primary model for isolated guests
- polished runtime transport resolver
- stronger end-to-end validation on live shared infra

### 5. Mobile Runtime Only

Implemented:

- phone sandbox local project system
- phone project schema/app/seed model
- export/import/promote/push logic

Primary files:

- `mobile/src/lib/phoneProjects.ts`
- `desktop/agent/phone_backend.go`
- `desktop/agent/phone_backend_http.go`

Assessment:

- **real**

Missing:

- explicit runtime-only target in the unified target model
- clearer distinction in UI between runtime-only and deployable targets

### 6. Builder Target

Implemented:

- build manager
- artifact streaming/downloading
- platform-aware build resolution
- direct install flows for some artifacts
- publish helpers

Primary files:

- `desktop/agent/build_http.go`
- `desktop/agent/builds.go`
- `desktop/agent/publish.go`
- `mobile/app/(tabs)/builds.tsx`

Assessment:

- **partial**

Missing:

- builder as first-class target kind
- builder attachment model
- queueing model tied to target capabilities
- explicit Linux builder / Android builder / Mac builder abstractions

## Payments / LemonSqueezy Audit

## What is implemented

### Backend checkout creation

Implemented in Convex:

- authenticated checkout endpoint
- environment-variable compatibility for both `LEMONSQUEEZY_*` and `LEMON_SQUEEZY_*`
- checkout custom data
- sandbox/live mode response

Primary file:

- `backend/convex/http.ts`

Current behavior:

- `POST /billing/yaver-cloud/checkout`
- creates hosted LemonSqueezy checkout
- includes:
  - `user_email`
  - `product_type = yaver-cloud`
  - `region`

### Webhook verification

Implemented:

- HMAC verification using webhook secret
- constant-time compare
- explicit 401 on invalid signature

Primary files:

- `backend/convex/http.ts`
- `desktop/agent/lemonsqueezy_test.go`

### Webhook event handling

Implemented:

- `subscription_created`
- `subscription_updated`
- `subscription_resumed`
- `subscription_cancelled`
- `subscription_expired`
- `subscription_payment_failed`

Primary files:

- `backend/convex/http.ts`
- `backend/convex/subscriptions.ts`

### Sandbox integration

Implemented:

- checkout endpoint returns `mode = sandbox|live`
- env flag `LEMONSQUEEZY_SANDBOX` / `LEMON_SQUEEZY_SANDBOX`
- web pricing preview can open checkout
- CLI preview helpers can call checkout or `dev-activate`

Primary files:

- `backend/convex/http.ts`
- `web/app/pricing/page.tsx`
- `desktop/agent/cloud.go`

### Mobile billing safety

Implemented:

- mobile push catches `402 Payment Required`
- mobile intentionally does not auto-open checkout URL
- comment and behavior align with App Store-safe companion-app model

Primary file:

- `mobile/src/lib/phoneProjects.ts`

## What is partial or problematic

### 1. Two LemonSqueezy stacks exist

There are two separate payment-related implementations:

- `backend/convex/http.ts` — real checkout/webhook path for managed infra
- `desktop/agent/lemonsqueezy.go` — agent-side generic LS manager/status/listing layer

Assessment:

- **duplicated domain logic**

Risk:

- drift between backend billing truth and agent-side LS utilities

### 2. Preview gate is effectively open

`isCloudPreviewUser()` currently returns true for any non-empty email.

Primary file:

- `backend/convex/http.ts`

Assessment:

- **preview gating is functionally disabled**

### 3. Subscription model is still relay-first

The `subscriptions` table and comments were designed for managed relays and are now being reused for cloud machines.

Primary files:

- `backend/convex/schema.ts`
- `backend/convex/subscriptions.ts`

Problems:

- `getByUser` returns only the first subscription
- no explicit product/resource scoping
- no support for one user having multiple machine subscriptions cleanly
- no support for machine-specific billing relationships beyond loose `subscriptionId`

### 4. Webhook provisioning is not fully idempotent at resource level

On `subscription_created`, the code provisions a resource immediately.

Problem:

- there is no explicit “resource already exists for this subscription + product” check before creating a cloud machine

Risk:

- duplicate provisioning if webhook delivery/order handling is repeated in unexpected ways

### 5. Cancellation / expiry handling is incomplete for cloud machines

Current code explicitly deprovisions managed relays on expiry.

It does **not** mirror the same complete deprovision flow for cloud machines in the webhook path.

Assessment:

- **cloud billing lifecycle is incomplete**

### 6. `subscription_payment_failed` path is still relay-biased

The payment-failed branch writes:

- `plan: relay-monthly`

even when the product may be cloud-related.

Assessment:

- **incorrect plan handling**

### 7. Preview bypass is useful but hides real E2E gaps

`/billing/yaver-cloud/dev-activate` attaches a preview machine row to a shared server without real checkout or real dedicated provisioning.

Assessment:

- useful for dev
- not a proof that paid dedicated cloud is ready

## LemonSqueezy Sandbox Verdict

**Sandbox is integrated at the backend API layer.**

That means:

- checkout creation is wired
- webhook verification is wired
- subscription upsert is wired
- preview web and CLI flows can trigger sandbox checkout

But:

- sandbox is **not yet a proof of real dedicated cloud activation**
- the dedicated machine runtime bootstrap remains the missing link

So the correct conclusion is:

- **payment sandbox plumbing exists**
- **managed cloud fulfillment is still incomplete**

## Important Mismatches and Contradictions

### 1. Docs are ahead of dedicated-machine reality

`web/app/docs/cloud-machines/page.tsx` and related docs present a more complete managed-cloud story than the dedicated provisioning code currently proves.

### 2. Shared-tenant cloud and dedicated cloud are both called “Yaver Cloud”

This causes product and implementation ambiguity.

Recommendation:

- explicitly name them:
  - `Yaver Cloud Shared Preview`
  - `Yaver Cloud Dedicated`

until the shared preview is retired

### 3. Multi-user comments are ahead of enforcement

`multiuser.go` describes team-restricted access, but `multiUserAuth` currently validates token and creates a session without an obvious real team-membership enforcement step tied to `teamID`.

Assessment:

- **needs audit/fix before treating team machines as secure**

## What Is Already Strong Enough To Build On

These subsystems are solid enough to treat as foundations:

- auth bootstrap and recovery
- device registration
- guest scope enforcement
- install manager
- vault manager
- build/artifact manager
- git provider/runtime surfaces
- phone project transport

The missing work is mostly:

- unification
- lifecycle wiring
- managed-cloud bootstrap
- billing/resource correctness

## Immediate Ship Blockers

These are the concrete blockers to claiming true mobile-first managed cloud.

### Blocker 1: dedicated cloud-init does not start a usable Yaver agent

Need:

- `config.json`
- auth/bootstrap strategy
- systemd service for `yaver serve`
- post-boot readiness
- device enrollment

### Blocker 2: shared secret cloud auth must be retired from the dedicated path

Need:

- per-machine auth
- per-user ownership
- normal session/token flow after activation

### Blocker 3: billing model must support multiple products/resources cleanly

Need:

- subscription/resource scoping
- cloud machine lifecycle tied to payment state
- correct deprovision on expiry/cancel/failure

### Blocker 4: no unified target/capability model

Need:

- one target schema
- one setup state machine
- one capability map

### Blocker 5: no real end-to-end sandbox test for checkout -> webhook -> dedicated machine ready

Current tests cover pieces.

Need one integrated test story:

- create sandbox checkout
- simulate verified webhook
- provision machine row
- bootstrap target
- observe ready machine state

## Recommended Next Implementation Order

### Step 1

Unify cloud naming and architecture:

- freeze shared-tenant preview as legacy preview
- treat dedicated cloud as the real product path

### Step 2

Finish dedicated machine bootstrap:

- generate agent config
- start `yaver serve` under systemd
- enroll machine as a normal device/target
- report readiness back

### Step 3

Fix billing/resource model:

- resource-scoped subscriptions
- idempotent provisioning guard
- deprovision cloud machines on cancel/expiry
- correct payment-failed handling

### Step 4

Introduce unified target model:

- managed cloud
- BYO
- local hardware
- shared host
- mobile runtime
- builder

### Step 5

Build one mobile setup wizard driven by capabilities, not by host-specific code paths.

## Final Assessment

Today, Yaver is:

- **strong as a mobile control plane for existing machines**
- **strong in auth/bootstrap and runtime primitives**
- **real in preview cloud/shared-tenant form**
- **partially wired for dedicated managed cloud**
- **sandbox-integrated for LemonSqueezy at the backend layer**

Today, Yaver is **not yet**:

- a fully complete dedicated managed-cloud product
- a unified multi-target deployment platform
- fully correct in billing/resource lifecycle handling across all managed infra

That is good news, not bad news:

- the hard lower-layer runtime work already exists
- the remaining work is mostly system integration and product unification

