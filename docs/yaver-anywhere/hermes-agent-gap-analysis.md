# Hermes Agent Gap Analysis for Yaver Anywhere

Last updated: 2026-06-17

Source reviewed: official Hermes Agent docs at
<https://hermes-agent.nousresearch.com/docs/> and linked official pages for
configuration, tools, memory, skills, cron, profiles, MCP, security, and
architecture.

This is not a clone plan, and Yaver should not become Hermes. Hermes is an
open-source personal-agent toolkit. Yaver targets normal users who should not
have to understand containers, terminal profiles, or model routing. Yaver's
product angle is dedicated resources plus managed/runtime fabric:
browser/desktop, Android phones, redroid, local machines, capture surfaces, and
relay-bound remote sessions. The point of this review is to borrow only the
product primitives that reduce confusion while keeping Yaver's mechanism.

## What Hermes Has That Matters

Hermes documents these notable capabilities:

- A closed learning loop: curated memory, session search, autonomous skill
  creation, skill improvement, and optional external memory providers.
- Six terminal backends: local, Docker, SSH, Modal, Daytona, and
  Singularity/Apptainer.
- A broad messaging gateway: CLI plus Telegram, Discord, Slack, WhatsApp,
  Signal, Matrix, Teams, SMS, and others.
- Scheduled automations with one-shot/recurring jobs, skill-backed jobs, job
  edit/pause/resume/run/remove, no-agent script mode, and delivery to chat or
  files.
- Profiles: independent agent homes with separate config, env, memory, skills,
  cron jobs, sessions, and gateway state.
- Toolsets: explicit enable/disable control by platform, with presets and MCP
  dynamic toolsets.
- MCP support for stdio and HTTP servers, including OAuth, paste-back auth for
  remote hosts, mTLS, and tool selection.
- Security UX: dangerous-command approvals, messaging-platform approval flow,
  gateway allowlists, one-time pairing codes, and rate limits.
- Research/dev affordances: batch processing, trajectory export, RL training
  hooks, Python library/API server entry points, and a documented architecture.

## Yaver Already Has Comparable Pieces

Yaver is ahead or different in these areas:

- Real runtime fabric: managed cloud, local machines, phones, redroid, capture,
  remote runtime, relay, QUIC/WebRTC, guest sessions, and mobile control.
- MCP/ops breadth: hundreds of local tools and peer tool calls through ACL/relay.
- Android/redroid QA: redroid surface, testkit, QA jobs, warm base snapshots,
  oracle bank, and catch/fix loop.
- Privacy boundary: Convex stores identity/session bookkeeping, while sensitive
  work stays on user devices.
- Phone-node direction: home-hosting, relay-only serving, reset/colo path, and
  future Android Enterprise enrollment.

## Gaps Worth Closing

### 0. Dedicated Resource UX

Hermes exposes backends. Yaver should expose owned capacity: "my cloud box",
"my Android clone", "my home phone", "my laptop". Normal users should see
whether each resource is ready, sleeping, missing setup, or needs human action.

Built now:

- `redroid_resource_status` ops verb: read-only status for whether the current
  machine can host a private dedicated Android clone. It reports Docker,
  redroid image, host support, and next actions without starting containers.
- `android_clone_provision` ops verb: dry-run by default, owner-only, creates a
  dedicated Hetzner ARM/CAX Android clone only when the user confirms and the
  live-spend gate is enabled. It bootstraps Docker, binder, and redroid.

Build next:

- Dashboard/mobile resource cards using the same states.
- Managed-cloud purchase/provision entry point that ends in "Ready", not in a
  provider console.
- One resource = one user by default for phones/redroid clones; do not pitch
  this as shared multi-tenant Android.

### 1. Productized Profiles

Hermes profiles give each agent its own config, env, memory, skills, sessions,
cron jobs, and gateway state. Yaver has devices, projects, peers, and sessions,
but not a crisp user-facing "profile" primitive for separate assistants.

Build:

- `assistantProfile` data model: `id`, `name`, `purpose`, `defaultDeviceId`,
  `defaultProjectSlug`, `allowedToolsets`, `memoryPolicy`, `schedulePolicy`.
- Profile picker in dashboard/mobile.
- Per-profile gateway/channel bindings.
- Per-profile vault namespace, without putting secrets into Convex.

Why it matters: a coding worker, personal assistant, QA runner, and phone-hosted
assistant should not share all state or permissions.

### 2. Memory With Explicit Approval

Hermes separates small curated memory from searchable session history and offers
approval gates for memory and skill writes. Yaver currently relies heavily on
project docs, local state, and user-provided context.

Build:

- Local-only `MEMORY.md` / `USER.md` equivalent per profile or project.
- FTS session search over local conversation/job history.
- Dashboard/mobile "pending memory writes" review.
- Privacy tests: no memory text to Convex unless explicitly synced as metadata.

Do not build first: external memory-provider marketplace. Start with bounded
local files plus search.

### 3. Scheduled Automations

Hermes has a complete cron lifecycle and can deliver results through messaging.
Yaver has long-running jobs and managed runtimes, but not a polished scheduler
that a user can ask for in natural language.

Build:

- `scheduled_task_create/list/pause/resume/run/delete` ops verbs.
- Delivery targets: dashboard notification, email/mobile push later, local file.
- Runtime target selection: managed cloud, home phone, local machine, redroid.
- Guardrail: scheduled tasks cannot create more scheduled tasks.
- Billing guardrail: managed-cloud schedules must declare max runtime and
  auto-down policy.

This pairs directly with Yaver's managed cloud moat: "run this every morning on
my cheapest available runtime, wake it if needed, then shut it down."

### 4. Toolset Filtering UX

Hermes exposes toolsets and platform presets. Yaver's MCP surface is powerful,
but the average user needs safer presets.

Build:

- Toolset presets: `safe`, `coding`, `device-control`, `managed-cloud`,
  `redroid-qa`, `home-host`, `dangerous`.
- Per-profile/per-session tool allowlists.
- UI that shows why a tool is hidden and what enabling it permits.
- Tests that guest tokens cannot enable owner-only toolsets.

### 5. Messaging Gateway

Hermes wins distribution by meeting users in many chat platforms. Yaver has
mobile/web/dashboard and MCP, but not the same outbound/inbound chat presence.

Build:

- Start with Telegram and Discord only.
- Pairing code flow for owner approval.
- Per-platform allowlist and deny-by-default behavior.
- Deliver scheduled task summaries and remote-session prompts.

Do not build every platform now. Two reliable channels beat twenty shallow ones.

### 6. MCP OAuth And Remote Auth

Hermes documents HTTP MCP, OAuth, paste-back auth for remote hosts, and mTLS.
Yaver has MCP/ACL strength, but hosted MCP onboarding should be more productized.

Build:

- First-class HTTP MCP config with OAuth metadata.
- Paste-back OAuth for headless Yaver boxes.
- Token cache in local vault, never Convex.
- Tool selection after OAuth discovery.

### 7. Redroid As A First-Class QA Product

Yaver already has more Android/runtime depth than Hermes. The missing product
piece is turning redroid into a clear button/API.

Build:

- `redroid_boot` ops wrapper over `studio.RedroidSurface`.
- `android_install_app` wrapper over install/push with artifact tracking.
- `qa_explore_app` crawler that maps screens and emits flow YAML.
- `qa_run` dashboard entry point with artifacts, screenshots, logs, and report.

This is where Yaver should lead, not follow.

### 8. Security UX

Hermes has visible dangerous-command approvals and gateway authorization docs.
Yaver has serious policy code, but the UX should make permissions legible.

Build:

- A unified approval inbox for shell, file edits, device control, cloud spend,
  and messaging replies.
- "Approve once / session / always / deny" semantics where applicable.
- Pairing codes for chat gateways and invited remote-session guests.
- Audit viewer that is useful to normal users, not just developers.

## Recommended Sequence

1. Finish Yaver Anywhere proof: TURN, home-host, real phone, redroid smoke.
2. Productize dedicated resources: resource cards, readiness states, managed
   provision, and redroid/private Android clone status.
3. Productize redroid QA: wrappers, dashboard report, explorer.
4. Add profiles only as normal-user "assistants", not as developer config
   directories.
5. Add scheduled automations on top of profiles and runtime selection.
6. Add memory approval and session search.
7. Add Telegram/Discord gateway with pairing.
8. Add hosted MCP OAuth/paste-back.

## Non-Goals

- Do not replace Yaver's managed cloud with generic Modal/Daytona backends.
- Do not copy every messaging platform before the core gateway is reliable.
- Do not sync rich memory, transcripts, terminal output, or device data to
  Convex.
- Do not treat profiles as a sandbox. Profiles separate state; runtime policy
  and OS/container boundaries provide isolation.

## Immediate Tickets

### Ticket H1 - Real-Device Evidence Loop

Ship and run `docs/yaver-anywhere/real-device-testing.md` plus
`scripts/smoke-yaver-anywhere-android.sh` on a physical phone. This closes the
gap between unit tests and hardware proof.

### Ticket H2 - Redroid Product Wrapper

Expose redroid boot/install/run/report as first-class ops verbs or dashboard
actions, not only as lower-level studio/testkit internals.

### Ticket H3 - Assistant Profiles ADR

Write an ADR and schema proposal for profiles before implementing memory or
cron, because both need a state boundary.

### Ticket H4 - Scheduled Task MVP

Implement local-only scheduled tasks with pause/resume/run/delete and dashboard
delivery. Add managed-cloud runtime selection after auto-down is live.

### Ticket H5 - Memory Approval MVP

Implement bounded local profile memory with pending writes and explicit approval
before any automatic write.
