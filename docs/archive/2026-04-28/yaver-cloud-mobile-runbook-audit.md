# Yaver Cloud Mobile Runbook And Audit

This document answers one concrete product question:

> How close is Yaver today to a mobile-first dedicated VPS flow where a user pays for a Yaver Cloud box, links GitHub, links Codex / Claude Code, promotes a phone sandbox backend there, and shares that box with friends or colleagues?

It does two things:

- gives a practical setup path you can use today with minimal laptop work
- audits the current implementation so the future managed-cloud feature is grounded in what already exists

## Executive Read

The core direction is already present in the repo:

- phone sandbox creation exists
- push/export to another agent exists
- `yaver-cloud` is already modeled as a target
- cloud-machine provisioning endpoints exist in preview form
- guest sharing, host-share, and multi-user foundations exist

But the full product is not seamless yet.

The biggest gaps are:

1. Cloud pricing and packaging are inconsistent across surfaces.
2. The cloud preview gate is effectively open to any signed-in email.
3. Mobile cloud setup still lacks a clean “link GitHub / link Codex / link Claude” wizard for a managed VPS.
4. Sharing primitives exist, but a dedicated “share my cloud VPS with teammates” product flow is not yet unified.

## What Works Today

### 1. Phone-first project creation

The mobile app already lets you start a project on:

- this phone
- the current connected agent
- another dev machine
- `Yaver Cloud`

Current implementation:

- [mobile/app/phone-projects.tsx](/Users/kivanccakmak/Workspace/yaver.io/mobile/app/phone-projects.tsx:1)
- [mobile/src/lib/phoneProjects.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/phoneProjects.ts:1)

### 2. Push / export continuum

Phone projects can already be exported and received by another agent through:

- `GET /phone/projects/export`
- `POST /phone/projects/receive`
- `POST /phone/projects/promote`

Current implementation:

- [desktop/agent/phone_backend_http.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/phone_backend_http.go:1)
- [MOBILE_BACKEND_EXPORT.md](/Users/kivanccakmak/Workspace/yaver.io/MOBILE_BACKEND_EXPORT.md:1)

### 3. Cloud machine activation exists in preview form

The agent already has preview-oriented cloud billing / activation calls:

- `POST /billing/yaver-cloud/checkout`
- `POST /billing/yaver-cloud/dev-activate`

Current implementation:

- [desktop/agent/cloud.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/cloud.go:300)
- [desktop/agent/cloud_preview_smoke_test.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/cloud_preview_smoke_test.go:1)

### 4. Git-provider usage from phone already exists conceptually

The product already supports “browse repos from phone, clone on the machine”:

- [README.md](/Users/kivanccakmak/Workspace/yaver.io/README.md:1408)

This is the right model for a managed VPS too, but the dedicated-cloud onboarding still needs a smoother credential-attachment path.

### 5. Sharing primitives already exist

There are three relevant sharing layers:

- classic guests
- host-share / borrowed runner
- multi-user sessions

Relevant implementation:

- [desktop/agent/guest_scope.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/guest_scope.go:1)
- [desktop/agent/multiuser.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/multiuser.go:1)
- [docs/host_guest_borrowed_runner_spec.md](/Users/kivanccakmak/Workspace/yaver.io/docs/host_guest_borrowed_runner_spec.md:1)

## What You Can Set Up Today

This is the least-friction path if you want the experience to feel mostly phone-first.

## Phase 1: Get a reachable machine

Pick one of these:

- your persistent Hetzner box
- a self-hosted VPS you own
- preview Yaver Cloud if you are exercising the current preview path

If you are using your own box, use the shared-owner setup:

- [docs/hetzner-shared-owner-runbook.md](/Users/kivanccakmak/Workspace/yaver.io/docs/hetzner-shared-owner-runbook.md:1)

Target machine requirements:

- `yaver serve` running
- authenticated as your account
- reachable from phone via relay / tunnel / direct path
- for shared use: prefer multi-user or guest isolation

## Phase 2: Do the setup mostly from the phone

### A. Sign in on phone

- sign in to the Yaver mobile app with your main account
- use the same account that owns the box

### B. Make the machine appear as a device

For your own VPS:

- authenticate the machine once
- let it connect back to Convex / relay
- confirm it appears in the device list

For preview cloud:

- activate the cloud machine using the preview billing path
- wait for it to show as active

### C. Create or promote a phone project

From the phone:

1. Open **Phone Projects**
2. Create the project locally on the phone first if you want the cleanest mobile-first path
3. Choose one of:
   - `Current agent`
   - `Dev HW`
   - `Yaver Cloud`
4. Let the app create remotely or push/export later

This is already wired through the same phone-project transport surface.

### D. Use Git from the phone

Current best path:

- let the machine hold GitHub / GitLab credentials
- browse and clone repos from the phone through the machine

Today, the product assumption is:

- Git credentials are already present on the machine
- Yaver detects them and uses them locally

That means the current “mostly mobile” GitHub setup is:

1. get GitHub auth onto the machine once
2. then do browsing/cloning from the phone

This is not yet the same as a fully managed “tap Connect GitHub” cloud wizard.

### E. Link Codex or Claude

For Codex today:

- put `OPENAI_API_KEY` into the machine vault
- owner tasks can use it
- guest tasks do not inherit it by default

For Claude today:

- the machine needs Claude auth material locally
- or direct API-key-based config where supported by the runner

Current best path:

- store secrets on the machine, not in repo files
- use owner mode for your own runs
- keep guest or multi-user sessions isolated from host keys unless explicitly allowed

### F. Share the machine

For colleagues or friends:

- use guest access when you want constrained use
- use host-share when they should borrow your runner/tools
- use multi-user when they should get an isolated user session on the same machine

Recommended cloud-product direction:

- owner keeps vault-backed Codex / Claude creds
- collaborators default to guest or multi-user sessions without host keys

## Current-State Audit

## Finding 1: Cloud pricing is inconsistent

Severity: medium

Current surfaces disagree:

- [desktop/agent/switch_targets.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/switch_targets.go:74) says `Yaver Cloud` is `$9/mo`
- [MOBILE_BACKEND_EXPORT.md](/Users/kivanccakmak/Workspace/yaver.io/MOBILE_BACKEND_EXPORT.md:13) says `$19/mo single project, $49/mo unlimited`
- [web/app/docs/cloud-machines/page.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/app/docs/cloud-machines/page.tsx:140) says `$49/mo` CPU and `$449/mo` GPU

Why it matters:

- mobile setup cannot be seamless if plan selection is inconsistent
- the same target appears as different products depending on which surface the user reads

What to do:

- define one canonical Yaver Cloud packaging model
- make `switch_targets.go`, docs, mobile labels, and checkout endpoints agree

## Finding 2: Cloud preview gating is not real gating

Severity: high

Current implementation:

- [mobile/src/lib/cloudPreview.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/cloudPreview.ts:1)

It currently returns true for any non-empty email:

- any signed-in user effectively looks like a cloud-preview user in the mobile UI

Why it matters:

- product availability is not controlled by subscription, allowlist, or preview entitlement
- this will create confusion once real billing and provisioning exist

What to do:

- replace email-presence gating with a real entitlement check from backend
- keep server-side validation as the source of truth

## Finding 3: Mobile cloud target is wired, but the managed-cloud contract is still transitional

Severity: high

Current flow:

- mobile uses `PhonePushTarget.kind = "yaver-cloud"`
- it resolves a base URL like `https://cloud.yaver.io`
- then calls generic phone project endpoints against that base

Relevant code:

- [mobile/src/lib/phoneProjects.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/phoneProjects.ts:713)
- [mobile/src/lib/phoneProjects.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/phoneProjects.ts:1360)

Why it matters:

- this is good as a transport wedge
- but it is not yet a full dedicated-VPS lifecycle with plan selection, machine ownership, provision state, linked credentials, team sharing, and lifecycle management in one coherent product flow

What to do:

- keep the generic `/phone/projects/receive` path as the deployment substrate
- add a dedicated mobile cloud-machine setup layer on top

## Finding 4: GitHub setup is not seamless yet for managed cloud

Severity: high

What exists:

- Git-provider browsing/cloning from phone
- machine-side credential discovery

What is missing:

- a mobile-first “Connect GitHub to this dedicated cloud machine” wizard
- a clear secure storage model for GitHub auth on a managed VPS
- a single user story that ends with “browse repos from phone immediately”

Current implication:

- the GitHub part is smooth only after credentials already exist on the machine
- that is good for self-hosted boxes, but incomplete for a paid dedicated-cloud product

What to do:

- add a machine-vault-backed GitHub credential attach flow from mobile
- make the mobile UI explicitly distinguish:
  - Yaver account identity
  - Git provider identity
  - machine-local Git credentials

## Finding 5: Codex / Claude linking is not yet a unified mobile cloud onboarding flow

Severity: high

What exists:

- runner detection
- machine-local secret sourcing
- vault-backed owner secrets

Relevant code:

- [desktop/agent/runner_auth.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/runner_auth.go:1)

What is missing:

- a productized mobile screen that says:
  - connect Codex
  - connect Claude
  - store key in machine vault
  - verify runner readiness
  - test with one run

Current best practice:

- machine vault for owner secrets
- separate shared-user policies for guests and multi-user sessions

What to do:

- expose machine-vault key setup from mobile
- add one “runner onboarding” wizard per cloud machine

## Finding 6: Sharing architecture exists, but the cloud sharing product is not unified yet

Severity: medium

The repo already has:

- guest access
- host-share
- multi-user

But the dedicated-cloud story still needs one opinionated product choice:

- which sharing mode should a paid cloud machine default to?

Recommended default:

- owner retains vault-backed runner keys
- invited users join as guest or multi-user without host keys
- host-share is optional for explicit borrowed-runner sessions

## Finding 7: Backend export is one of the strongest completed parts

Severity: positive

This area is already aligned with the desired cloud story:

- same portable bundle
- same receive/import endpoint
- same promote/export mental model

Relevant references:

- [desktop/agent/phone_backend_http.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/phone_backend_http.go:381)
- [MOBILE_BACKEND_EXPORT.md](/Users/kivanccakmak/Workspace/yaver.io/MOBILE_BACKEND_EXPORT.md:1)

This should remain the core portability contract:

`phone sandbox -> your dev machine -> Yaver Cloud`

## Recommended Product Shape

If you want the dedicated cloud feature to feel truly seamless from mobile, the setup flow should be:

1. User taps **Get Cloud Machine** in mobile.
2. User picks plan.
3. Billing/entitlement activates.
4. Machine provisions and appears in device list.
5. Mobile shows **Finish setup** checklist:
   - connect GitHub / GitLab
   - connect Codex
   - connect Claude
   - optional Cloudflare / DNS
   - optional share with teammate
6. User creates project on phone.
7. User chooses:
   - keep on phone
   - run on cloud machine
   - promote later
8. User can share machine access without transferring owner secrets.

## Concrete Follow-Ups To Build

Order matters.

### P0

- real cloud entitlement check in mobile and server
- unify Yaver Cloud pricing / packaging / naming
- mobile wizard for machine-vault-backed Codex and Claude setup
- mobile wizard for GitHub / GitLab machine credential setup

### P1

- explicit cloud-machine share flow built on guest or multi-user defaults
- one mobile screen for machine health, runner readiness, and missing setup items
- end-to-end status stream for provisioning and first-run validation

### P2

- cloud-machine templates: solo box, team box, GPU box
- one-tap backend export / promote / rollback status in the app
- richer mobile-first lifecycle management: restart, stop, archive, destroy

## Recommended Setup For You Right Now

If the goal is practical usage now, not waiting for the full managed product:

1. Keep your Hetzner box as the canonical always-on machine.
2. Use the shared-owner model from:
   - [docs/hetzner-shared-owner-runbook.md](/Users/kivanccakmak/Workspace/yaver.io/docs/hetzner-shared-owner-runbook.md:1)
3. Treat it as the prototype for Yaver Cloud.
4. Store your Codex key in the machine vault.
5. Use guest / multi-user for collaborators.
6. Use phone projects + export/push as the mobile-first backend flow.
7. Treat GitHub onboarding as “machine credential setup first, then mobile clone/browse” until the dedicated wizard exists.

That gives you the real workflow now while keeping the future cloud feature honest.
