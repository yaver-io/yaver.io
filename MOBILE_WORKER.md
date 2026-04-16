# Yaver Mobile Worker

This file defines the intended product shape for treating spare phones as first-class `Yaver` workers.

It is not a pitch deck. It is the implementation spec for:

- phone-as-worker
- phone-as-test-node
- phone-as-sensor/automation endpoint
- phone-first orchestration for solo mobile developers

The main bias of this document is practical value for a solo developer who:

- uses a primary iPhone as the control surface
- vibes from the phone a lot
- uses Claude Code / Codex on a MacBook
- has a Mac mini for always-on automation
- may have a Raspberry Pi for cheap infra glue
- may own one or more second-hand phones
- mostly builds React Native / Expo / mobile-first fullstack apps

The central claim is:

- second-hand phones are valuable to Yaver
- but not as primary large-LLM inference hosts
- they are valuable as `mobile workers`

The most important concrete experience is:

- user vibes from the primary phone
- Yaver uses one or more secondary phones as real-device workers
- the primary phone can watch those workers live
- the primary phone can choose which worker(s) to use for each task
- success/failure/fixed runs come back with video, screenshots, and structured evidence

The primary phone must also remain a valid Hermes/runtime target when the user wants the fastest direct loop on the same device they are holding.

This document also tracks a second, later platform direction:

- a `mini backend` embedded inside the Yaver mobile app
- optimized for creating a new mobile app from a mobile app
- portable later to user-owned infra like Raspberry Pi, Linux, Mac, VPS, Supabase, or Convex

That mini-backend track is explicitly a future layer on top of Yaver's phone-first development workflow. It is not a requirement for the initial mobile-worker rollout.

## Summary

Yaver should support a spare phone registering as a `mobile worker` with explicit capabilities, health state, and placement constraints.

The phone can then be used for:

- real-device testing
- app automation
- screenshot/video capture
- push/deeplink/permission lifecycle validation
- OCR / ASR / image preprocessing
- third-party app handoff testing
- mobile network edge probing
- lightweight local model tasks
- constrained agent-worker execution

It should not be treated as:

- the main LLM runtime
- a replacement for a Mac mini
- a distributed tensor-shard node for large `Llama` inference

Yaver should eventually also support a different mode:

- `mobile-first project backend`

This is not the same thing as using a spare phone as a worker. In this mode, the Yaver app itself hosts a constrained local backend runtime for rapid app creation, preview, and iteration from the phone.

## Why This Matters

For a solo mobile developer, the biggest missing capability is not “more raw tokens per second.” It is “more real mobile surfaces that can actually do the thing.”

A spare phone can do things the MacBook, Mac mini, and Pi cannot:

- run the real mobile OS
- enforce real background execution limits
- receive real push notifications
- exercise real camera, microphone, Bluetooth, GPS, share sheet, deep link, and file picker flows
- perform app-to-app handoffs
- reproduce OEM/device-specific rendering and lifecycle bugs
- test under unstable Wi-Fi / cellular-like conditions

That means the phone’s value is mostly:

- execution surface
- sensing surface
- validation surface

not:

- big reasoning surface

It can also act as a bounded `agent-worker` for app-observe-decide-act loops, as long as Yaver keeps deep reasoning on desktop/cloud infra.

## User Persona

Primary target user:

- solo developer
- fullstack and mobile-heavy
- React Native / Expo / Next.js / Node / Convex / Firebase / Supabase / Stripe style stack
- prefers remote control from phone
- wants Yaver to “keep shipping” while away from laptop

Typical device fleet:

- `Primary iPhone`
  control plane, notifications, monitoring, approvals, chat
- `MacBook`
  active coding, Claude Code / Codex, local simulator, git work
- `Mac mini`
  always-on automation host, signing/builds, orchestration
- `Raspberry Pi`
  relay, webhooks, queue, watchdog, tiny services
- `Secondary phone`
  mobile worker

This document is optimized for that setup.

## Design Principles

1. Spare phones are `specialized workers`, not general-purpose infra.
2. Heavy reasoning stays on laptop, Mac mini, or cloud.
3. Mobile workers should bias toward independent, observable tasks.
4. Mobile workers must have explicit health, thermals, battery, and charging state.
5. Scheduling should favor correctness and user trust over maximizing utilization.
6. All worker behavior must be safe under intermittent connectivity.
7. UI must make it obvious why Yaver used a phone or did not use a phone.
8. Future mobile-first backend features must be portable to user-owned infra and not trap the user in a Yaver-only project format.

## Terminology

### Primary Phone

The user’s actively used phone. This is usually the control plane.

### Mobile Worker

A spare phone registered as an edge execution node. It is available to receive jobs from Yaver.

### Agent Worker

A spare phone that is explicitly allowed to run constrained agent-like loops.

This means:

- short bounded prompts
- local screen/state classification
- app-observe-decide-act loops
- small recovery or verification logic

This does not mean:

- primary coding agent
- repo-wide reasoning
- large-context planning

### Worker Profile

Structured capability metadata reported by the phone:

- platform
- OS version
- device model
- battery level
- charging state
- thermal state
- storage pressure
- local model capability
- automation capability
- available sensors
- available third-party apps

### Mobile Job

A task that can be executed on a mobile worker:

- install build
- open app
- navigate flow
- capture screenshot
- record video
- wait for push
- check deep link
- run OCR
- transcribe voice
- verify third-party handoff

### Agent Job

A narrower mobile job where the worker is allowed to make bounded local decisions before returning control or escalating to main infra.

Examples:

- decide the next tap in a known flow
- determine whether onboarding succeeded
- verify whether a fix cleared a visible error
- retry a blocked step a limited number of times

### Placement

The decision of whether a task should run on:

- `edge`
- `infra`
- `hybrid`

## Parallel Product Track: Mini Backend Inside Yaver

This is a future platform track for phone-first creation, not part of the initial spare-phone worker rollout.

The goal is:

- user creates a mobile app from the Yaver mobile app
- Yaver provides a constrained local backend runtime inside the app
- the project remains portable later to real infra

This is the right answer to "how can I start building from my phone?" It is not "run full Convex locally on iPhone."

### What It Is

A `mini backend` embedded in the Yaver mobile app should provide:

- local data storage
- schema definitions
- query and mutation primitives
- local file/blob storage abstraction
- local auth mock or profile abstraction
- local event/sync layer
- seed data and fixtures
- export or promotion path to real backend providers later

The runtime should be optimized for:

- React
- React Native
- Expo
- Hermes-first preview loops
- vibe-driven CRUD and app-flow generation

### What It Is Not

It is not:

- a full Convex replacement on the phone
- a full Supabase clone on the phone
- a general-purpose server runtime
- a Node process manager
- a place to host arbitrary production workloads

The purpose is fast prototyping, iteration, and phone-first creation.

### Why This Matters

This gives Yaver a path for:

- creating a new mobile app from the mobile app
- letting a user vibe from the phone without first provisioning desktop infra
- supporting weak-computer setups where the phone is the primary interface
- keeping the project portable when the user later adopts Pi, Linux, Mac, or hosted backend infra

### Phone-First Fullstack Creation Mode

This future mode should explicitly support:

- user enters `Codex`, `Claude`, or other model API keys into the Yaver mobile app
- Yaver creates and edits the project directly on the primary phone
- project source stays on the phone first
- Yaver previews and runs the project inside a Yaver-owned host/runtime contract
- user can later export or promote the project to desktop or cloud infra

The first version of this mode should assume:

- React / React Native / Expo-first workflows
- constrained local backend runtime
- Hermes-targeted hosted preview inside Yaver
- portable project manifest and source layout

It should not assume:

- full native iOS build pipeline on the phone
- arbitrary native module compilation on the phone
- full desktop-equivalent server orchestration on the phone

The product promise is:

- phone-first creation
- not phone-only forever

Projects created this way must be promotable to:

- Yaver backend
- Raspberry Pi / Linux / Mac hosts
- VPS
- Supabase
- Convex
- standard React Native / Expo projects where possible

### Git and Monorepo Expectations

Phone-first projects should be git-native from the start.

Default assumptions:

- new projects are monorepos
- app code, mini-backend code, config, and generated artifacts live in one repo shape
- the project can be initialized entirely inside Yaver on the phone
- the same project can later be exported to a local machine or cloud runner without format conversion drama

The first version should support:

- initialize git repo on-phone
- branch creation and switching
- commit history and diff view from mobile
- remote setup for GitHub and GitLab
- push / pull / sync flows
- export repo bundle to Mac, Linux, Pi, or VPS

The repo layout should be compatible with later promotion to:

- GitHub-hosted repo
- GitLab-hosted repo
- self-hosted git remote

The mini backend and mobile-worker features must not assume a single-repo-per-device toy model. They should assume a portable monorepo with multiple app/backend packages as the steady-state default.

### Portability Contract

The mini backend must be designed around a portability contract.

That means each project should be representable as:

- schema
- collections or tables
- queries
- mutations or actions
- auth rules
- storage rules
- seed data
- environment bindings

Yaver should later be able to promote that project to:

- local Raspberry Pi or Linux box
- Mac mini or MacBook
- VPS
- Supabase
- Convex
- custom backend scaffolds where needed

The internal Yaver runtime must therefore stay intentionally constrained and declarative.

### iPhone and Android Expectations

For this mini-backend direction:

- `iPhone` is viable if the runtime stays inside the app sandbox and uses app-owned storage/runtime primitives
- `Android` is also viable and gives somewhat more flexibility

For both platforms, the design should avoid assuming:

- shell access
- background subprocess orchestration
- unrestricted local networking
- arbitrary package/toolchain execution

### Suggested Scope

The first useful version should support:

- local collections
- CRUD queries and mutations
- optimistic UI and local subscriptions
- fixture data
- local uploads/media references
- simple auth personas
- export to structured project manifest

The first useful version should not try to support:

- arbitrary server code
- inbound webhooks
- heavy background jobs
- complex production auth providers
- provider-specific advanced features

### Relationship To Mobile Workers

These are complementary product tracks:

- `mobile worker`
  uses spare phones as real-device execution, capture, and validation surfaces
- `mini backend`
  lets Yaver host a constrained app backend inside the mobile app for early-stage creation

The likely end state is:

- primary phone runs Yaver control plane
- Yaver can create and edit a mobile app project directly on the phone
- Yaver can preview on the primary phone or on spare-phone workers
- project can later be promoted to Mac, Pi, Linux, VPS, Supabase, or Convex

### Sequencing

This should not block the current implementation plan.

Execution order remains:

1. real-device worker selection and target persistence
2. remote preview session model
3. screenshot/video evidence and live session control
4. bounded mobile `agent-worker` loops
5. only after that, the phone-native `mini backend` track

## What A Mobile Worker Is Good For

### 1. Real Device App Testing

Highest-value use case.

Examples:

- install latest iOS build
- open app and verify onboarding
- validate permission prompts
- validate app launch after update
- verify background/foreground state recovery
- verify notification tap opens the right route
- verify deeplink or universal link behavior
- verify share extension or file-open flow

Why it matters:

- simulators miss real-device lifecycle behavior
- OS dialogs and permission flows often differ from simulator behavior
- push notifications and deep links are far more credible on a real device

### 2. Third-Party App Handoff Testing

Very important for mobile products.

Examples:

- open Stripe payment flow
- hand off to Maps
- open WhatsApp share flow
- test mail composer or browser redirect
- verify OAuth handoff and return
- verify app store / TestFlight / Play Store redirection

Why it matters:

- app-to-app behavior is hard to test well on desktop
- it is one of the most fragile production surfaces in mobile products

### 3. Push Notification Validation

Examples:

- send test push
- confirm push arrives within threshold
- tap push
- verify open target route
- verify badge count and app state transition

Why it matters:

- push is a real-device problem
- simulator coverage is weak or misleading

### 4. Permission / Sensor / Hardware Flows

Examples:

- camera permission and capture
- microphone permission and recording
- photo library picker
- location prompt
- background location continuation
- Bluetooth permission
- QR scanner flow

Why it matters:

- these are exactly the flows that regress in shipping apps

### 5. Visual Regression and Evidence Capture

Examples:

- screenshot critical screens
- record short video of onboarding or checkout
- compare rendering against baseline
- capture state when agent finds a failure

Why it matters:

- gives the user trust
- gives the AI evidence
- creates shareable artifacts

### 6. OCR / Speech / Lightweight Local Inference

Useful but secondary to testing.

Examples:

- OCR text from current screen
- classify UI state from screenshot
- transcribe a short voice memo
- generate embeddings for small local artifacts
- label screenshots before sending metadata upstream

Why it matters:

- cheap edge preprocessing
- privacy-preserving local extraction
- lower bandwidth to main infra

### 7. Constrained Agent Worker Execution

This is the second important use case.

A spare phone can act as an `agent-worker` when the loop is local, bounded, and tied to real-device execution.

Good examples:

- inspect current screen and choose the next navigation step
- resolve a permission prompt and continue
- keep trying a small flow until success or blocker
- verify that a visible bug is actually gone after a fix
- summarize the device-side failure before escalating to desktop/cloud

Why it matters:

- lowers round-trip latency for simple app-observe-act loops
- makes the spare phone feel like a semi-autonomous real-device emulator
- lets Yaver keep progressing while the user steers from the primary phone

### 8. Network Edge Probing

Examples:

- verify relay reachability from a mobile network
- test captive-portal-like network weirdness
- test degraded connectivity behavior
- verify the app under offline / flaky conditions

Why it matters:

- mobile apps fail in network transitions, not just in ideal Wi-Fi

## What A Mobile Worker Is Bad For

### 1. Large LLM Hosting

Not the right use.

Reasons:

- low usable RAM
- weak sustained performance
- thermal throttling
- poor economics per operator hour
- high orchestration complexity

### 2. Long Context Reasoning

Even if technically possible on a few devices, it is not an effective product bet.

### 3. Distributed Multi-Phone LLM Sharding

This is mostly a trap for interactive product work.

Reasons:

- synchronization overhead
- poor latency
- heterogeneous hardware
- operational fragility
- little practical gain relative to just using a real host machine

### 4. Primary Coding Agent Runtime

The spare phone should not host the main repo-writing, repo-reading, long-lived coding agent.

That belongs on:

- MacBook
- Mac mini
- cloud

## Product Model

Yaver should model spare phones as first-class workers with explicit roles.

### Role Types

- `control-phone`
- `mobile-worker`
- `agent-worker`
- `desktop-agent`
- `build-host`
- `relay-node`
- `server-node`

This role system should not be cosmetic. It should affect:

- scheduling
- UI
- exposed tools
- health checks
- permissions

### Mobile Worker Subtypes

- `test-runner`
- `automation-runner`
- `capture-node`
- `push-node`
- `ocr-node`
- `speech-node`
- `probe-node`
- `agent-node`

A single phone can expose multiple subtypes.

## Worker Profile

Every mobile worker should report a structured profile during registration and heartbeat.

### Required Fields

- `deviceId`
- `name`
- `platform`
- `osVersion`
- `deviceModel`
- `workerRole`
- `workerSubtypes`
- `batteryPct`
- `isCharging`
- `thermalState`
- `networkType`
- `isLowPowerMode`
- `screenLocked`
- `lastHeartbeat`

### Capability Fields

- `supportsBuildInstall`
- `supportsPushValidation`
- `supportsDeepLinks`
- `supportsScreenshotCapture`
- `supportsScreenRecording`
- `supportsOCR`
- `supportsSpeechToText`
- `supportsVisualDiff`
- `supportsThirdPartyAutomation`
- `supportsCameraFlows`
- `supportsMicFlows`
- `supportsLocalInference`
- `maxModelClass`
- `supportsAgentWorker`
- `agentWorkerModes`

### agentWorkerModes

Suggested values:

- `ui-observe-act`
- `verification-loop`
- `local-classifier`
- `fallback-autonomy`

### Optional Inventory Fields

- installed Yaver-managed test app ids
- installed third-party app ids
- notification permission state
- camera permission state
- microphone permission state
- photo permission state
- location permission state
- local free storage
- memory estimate
- connectivity class

### Health Interpretation

`thermalState`:

- `nominal`
- `warm`
- `hot`

`availability` should be derived from:

- online
- unlocked/usable
- charging
- thermal state
- battery threshold
- storage pressure
- current job

Suggested status buckets:

- `ready`
- `degraded`
- `busy`
- `cooldown`
- `offline`

## Scheduling Rules

This is the core of the feature.

### Rule 1: Phones are opt-in workers

No phone should accidentally become a worker just because it is logged in.

User must explicitly enable:

- `Use this device as a mobile worker`

### Rule 2: Control phone should not be used as a worker by default

The primary iPhone is the user’s interface. It should only be used as a worker if the user explicitly allows it.

Default:

- `control-phone`: no worker jobs
- `mobile-worker`: allowed worker jobs

### Rule 3: Testing and automation beat inference

If the scheduler has a choice between:

- using the phone for real-device validation
- using the phone for small edge compute

it should prefer:

- real-device validation

### Rule 4: Thermals and charging matter

Do not schedule non-urgent jobs on a phone that is:

- not charging and battery < 35%
- thermal state `hot`
- low power mode on

### Rule 5: Long-running jobs should prefer docked devices

If a phone is:

- on power
- on Wi-Fi
- physically stationary

it is a better worker candidate.

### Rule 6: Heavy reasoning stays elsewhere

Tasks like:

- code planning
- long context agent sessions
- repo-wide reasoning
- large model inference

should go to:

- MacBook
- Mac mini
- cloud

### Rule 6b: Phone agent-workers only get bounded loops

Phone agent-workers may run:

- short observe-decide-act loops
- local verification
- compact extraction/classification
- fallback automation when disconnected from the main brain

They may not run:

- open-ended coding tasks
- large-context planning
- repo-wide reasoning
- long unattended code generation

### Rule 7: Batch preprocessing can use worker pools

Phone farming is worth using only for jobs that are:

- independent
- parallel
- latency-insensitive

Examples:

- OCR batches
- screenshot labeling
- short transcription jobs
- media preprocessing

### Rule 8: User-visible explanations are mandatory

For every mobile-worker placement decision, the UI should show why.

Examples:

- `Used iPhone SE worker for push validation because it is idle, charging, and has push entitlement`
- `Skipped mobile workers because task requires large-context reasoning`

## Core Use Cases

### Use Case 1: Build -> Install -> Smoke Test -> Report

Flow:

1. User chats from primary phone: `ship onboarding changes`
2. Claude/Codex works on MacBook or Mac mini
3. Build host produces iOS/Android artifact
4. Yaver installs it on spare phone
5. Mobile worker runs smoke flow
6. Worker captures screenshot/video
7. Worker streams live state back to primary phone
8. Result lands on primary phone as pass/fail/fixed evidence

Success output:

- pass/fail
- screenshots
- recording
- notes
- suggested next action

Primary phone controls during run:

- start
- stop
- retry
- switch worker
- open live screen
- open last failure clip
- compare failed vs fixed run

### Use Case 2: Push Notification Validation

Flow:

1. User triggers `test push`
2. Backend/service sends push payload
3. Mobile worker waits for push
4. Worker confirms receipt, captures timestamp
5. Worker taps notification
6. Worker verifies landing route
7. Worker sends evidence back

### Use Case 3: OAuth / Deep Link / Third-Party Handoff

Flow:

1. Agent prepares test state
2. Mobile worker opens app
3. Worker triggers OAuth or external handoff
4. Worker verifies roundtrip back into app
5. Worker records whether route and state are correct

### Use Case 4: Nightly Real Device Patrol

Flow:

1. Mac mini schedules nightly patrol
2. Mobile worker installs latest dev build
3. Worker executes a small suite:
   onboarding, login, push, deeplink, purchase entry, background restore
4. Evidence and failures are posted into morning summary

### Use Case 5: OCR / Visual Extraction Edge Queue

Flow:

1. Desktop or server generates many screenshots
2. Queue distributes independent OCR jobs
3. Spare phones process small batches
4. Results are aggregated centrally

This is one of the few legitimate “phone farm” uses.

### Use Case 6: Secondary Phone As Agent Worker While User Vibes On Primary Phone

Flow:

1. User is steering from the primary phone
2. Main reasoning runs on MacBook, Mac mini, or cloud
3. Yaver assigns a spare phone as `agent-worker`
4. The spare phone observes the app, makes bounded local decisions, and executes a flow
5. The primary phone watches live and can intervene at any point
6. Hard reasoning or ambiguous failures are escalated back to main infra

Examples:

- “keep trying onboarding until you land on Home or hit a blocker”
- “handle permission prompts and continue”
- “confirm the visible bug is actually fixed on real hardware”
- “behave like a semi-autonomous emulator, but on a real device”

## Architecture

### Control Plane

The control plane remains:

- mobile app
- web dashboard
- desktop agent
- Convex backend

### Execution Plane

Execution should expand to include:

- desktop agent nodes
- build hosts
- mobile workers
- optional Pi relay/queue nodes

### Separation of Concerns

- mobile worker executes concrete device-bound actions
- desktop agent performs reasoning and repository actions
- backend stores worker metadata and job state
- relay/tunnel provides reachability

### Recommended Device Responsibilities

#### Primary iPhone

- control plane
- job approval
- task monitoring
- artifact review
- emergency stop
- live screen viewing
- worker pool selection
- switching active real-device target during a session

#### MacBook

- active coding
- interactive AI sessions
- local debugging

#### Mac mini

- always-on orchestration
- CI-like loops
- build/signing
- simulator/test hosting

#### Raspberry Pi

- relay
- queue
- cron
- webhook intake
- health watchdog

#### Spare Phone

- mobile worker
- real-device testing
- capture
- push/deeplink validation
- third-party automation
- constrained agent-worker execution

## Data Model

The backend should extend the `devices` model with explicit mobile worker fields.

### Minimal Additions

- `deviceRole`
- `workerEnabled`
- `workerSubtypes`
- `agentWorkerEnabled`
- `agentWorkerModes`
- `availability`
- `batteryPct`
- `isCharging`
- `thermalState`
- `networkType`
- `isLowPowerMode`
- `supportsBuildInstall`
- `supportsPushValidation`
- `supportsDeepLinks`
- `supportsScreenshotCapture`
- `supportsScreenRecording`
- `supportsOCR`
- `supportsSpeechToText`
- `supportsThirdPartyAutomation`
- `supportsLocalInference`
- `maxModelClass`

### Job Tables

Create dedicated tables for mobile jobs.

Suggested tables:

- `mobileWorkerJobs`
- `mobileWorkerRuns`
- `mobileWorkerArtifacts`
- `mobileWorkerCapabilitiesHistory`

### mobileWorkerJobs

Fields:

- `jobId`
- `teamId` or `userId`
- `targetDeviceId`
- `status`
- `jobType`
- `priority`
- `payload`
- `placementReason`
- `createdAt`
- `startedAt`
- `finishedAt`
- `requestedBy`
- `originSurface`

### jobType examples

- `install_build`
- `launch_app`
- `smoke_test`
- `push_validate`
- `deeplink_validate`
- `permission_flow`
- `capture_screenshot`
- `record_video`
- `ocr_extract`
- `speech_transcribe`
- `third_party_handoff`
- `network_probe`
- `agent_verify_flow`
- `agent_ui_loop`
- `agent_fix_verify`

### mobileWorkerArtifacts

Artifacts should support:

- screenshot
- video
- live screen stream session metadata
- failure clip
- success clip
- before/after comparison pair
- OCR text
- log bundle
- structured observation

Suggested artifact tags:

- `run-start`
- `step-pass`
- `step-fail`
- `fixed-pass`
- `final-success`
- `final-failure`
- `live-stream`

## API Surface

### Worker Registration

Add or extend:

- `POST /devices/register`
- `POST /devices/heartbeat`

Payload should include mobile-worker capability fields.

### Worker Availability

Add:

- `POST /mobile-workers/availability`
- `GET /mobile-workers/list`

### Job APIs

Add:

- `POST /mobile-workers/jobs`
- `POST /mobile-workers/jobs/:id/cancel`
- `GET /mobile-workers/jobs/:id`
- `GET /mobile-workers/jobs`
- `POST /mobile-workers/jobs/:id/artifacts`
- `POST /mobile-workers/jobs/:id/takeover`
- `POST /mobile-workers/jobs/:id/release-control`

### Placement API

Add or expand:

- `POST /devices/placement/recommend`

It should be able to answer:

- can a mobile worker do this?
- which worker should do it?
- should this be edge, infra, or hybrid?
- is farming worthwhile for this task type?

## Job Protocol

Jobs sent to a mobile worker should be resumable and idempotent when possible.

### Required Job Fields

- `jobId`
- `jobType`
- `payload`
- `timeoutSec`
- `attempt`
- `artifactPolicy`
- `expectedOutputs`

### Execution States

- `queued`
- `assigned`
- `accepted`
- `running`
- `uploading_artifacts`
- `succeeded`
- `failed`
- `timed_out`
- `cancelled`
- `cooldown`

### Observations

Workers should emit structured observations during execution.

Examples:

- `app_installed`
- `app_launched`
- `permission_prompt_visible`
- `push_received`
- `deeplink_opened`
- `screen_text_extracted`
- `handoff_returned`
- `assertion_failed`
- `video_stream_started`
- `video_stream_stopped`
- `failure_clip_saved`
- `fix_verification_started`
- `fix_verification_passed`

This should stream into mobile/web UI and agent logs.

### Live Screen / Video Streaming

This is a first-class requirement for the product.

The worker should be able to expose a low-latency view to the primary phone during:

- smoke tests
- repro runs
- fix verification
- manual operator takeover

The system should support two modes:

- `live stream`
  lower-latency screen/video feed for active observation
- `artifact capture`
  durable clips and screenshots for timeline/history

The product should always prefer artifact capture over recording everything forever. Live streams are ephemeral by default unless pinned as evidence.

Suggested stream controls from primary phone:

- start viewing
- pause viewing
- switch worker
- request screenshot now
- start manual recording
- end recording and save clip
- take control

### Remote Control Plane

The primary phone should be able to remotely operate the worker during a run.

Levels of control:

- `observe`
  watch live stream, view logs, read assertions
- `assist`
  request screenshot, request re-run of a step, mark point of interest
- `takeover`
  temporarily drive the worker directly

`takeover` is important because the user may want to interact with a real device the way they would with an emulator, except on real hardware.

Suggested actions:

- tap
- swipe
- type text
- press home/back
- open notifications
- relaunch app
- switch app
- approve install/retry

This should be modeled as an explicit control lease so automated execution and manual control do not fight each other.

## MCP Surface

Yaver should expose mobile-worker operations through MCP so desktop agents can use them naturally.

### Core Tools

- `mobile_worker_list`
- `mobile_worker_status`
- `mobile_worker_reserve`
- `mobile_worker_release`
- `mobile_worker_install_build`
- `mobile_worker_launch_app`
- `mobile_worker_smoke_test`
- `mobile_worker_capture_screenshot`
- `mobile_worker_record_video`
- `mobile_worker_send_push`
- `mobile_worker_wait_for_push`
- `mobile_worker_test_deeplink`
- `mobile_worker_run_ocr`
- `mobile_worker_transcribe_audio`
- `mobile_worker_run_handoff_test`
- `mobile_worker_collect_artifacts`
- `mobile_worker_agent_verify_flow`
- `mobile_worker_agent_take_step`
- `mobile_worker_agent_takeover`

### Tool Design Rules

1. Tools must expose real constraints.
2. Tools must return evidence, not just booleans.
3. Tools must include device identity and timing in responses.
4. Tool failures must be distinguishable from app failures.
5. Agent-worker tools must be bounded and explicit about step limits.

### Example MCP Flow

An agent working on a React Native feature might do:

1. `mobile_worker_list`
2. choose spare iPhone worker
3. `mobile_worker_install_build`
4. `mobile_worker_smoke_test`
5. `mobile_worker_capture_screenshot`
6. read result
7. decide whether to patch code or ask user

## Mobile App UX

The mobile app needs three major surfaces.

### 1. Worker Setup

On a spare phone:

- `Use this device as a mobile worker`
- `Allow this device to act as an agent-worker`
- choose worker subtypes
- choose charging/battery policy
- choose when jobs are allowed:
  only while charging / anytime / scheduled windows
- choose allowed projects or teams

### 2. Worker Dashboard

On the worker device:

- current status
- current job
- recent jobs
- battery / charging / thermals
- quick pause
- quick drain protection
- lock-screen-safe minimal status
- whether live stream is active
- whether primary phone currently has control

### 3. Control View

On primary phone:

- list all mobile workers
- view readiness
- start tests on a worker
- select one or many workers for a run
- view live screen/video from a worker
- switch between workers during a run
- take remote control of a worker
- queue installation
- view screenshots/videos
- view failed clips and fixed clips side by side
- see placement reasons
- see whether each worker supports agent-worker mode

The control view should work like a real-device lab dashboard in miniature.

For each worker, show:

- device name
- OS/device model
- online/charging/battery/thermal state
- busy/ready/cooldown state
- current app under test
- current job step
- live preview thumbnail when active

For multi-worker runs, the primary phone should be able to:

- select exact workers manually
- save worker groups
- choose `auto-select best available`
- keep `this phone` as the default target when no worker is selected
- pin one worker as preferred verification target
- fan out the same run to multiple workers
- choose whether each worker is used for `test`, `capture`, or `agent-worker` duty

### UX Rules

1. Worker mode must be explicit.
2. Current job must always be visible.
3. Battery/thermal safety must be understandable.
4. Evidence must be one tap away.
5. Failures must be actionable, not vague.

## Web Dashboard UX

Web should have:

- worker inventory
- capability chips
- health indicators
- queue view
- artifacts browser
- “run on spare phone” actions
- nightly patrol configuration
- live session wall with one tile per active worker
- side-by-side failed vs fixed playback

Useful dashboard sections:

- `Workers`
- `Real Devices`
- `Mobile Jobs`
- `Artifacts`
- `Schedules`

## Desktop Agent Responsibilities

The desktop agent remains the orchestrator for code and build flows.

It should:

- decide when to use a mobile worker
- publish mobile-worker jobs
- consume artifacts
- surface results to AI runners
- merge evidence into autotest / feedback / reports
- coordinate live stream/session leases
- support operator takeover from primary phone

It should not:

- assume the phone can reason deeply
- tunnel all logic into the phone

When using an `agent-worker`, the desktop agent should:

- send bounded goals
- cap local decision count
- require structured observations
- pull hard reasoning back to desktop/cloud

## Security

Mobile workers operate on sensitive surfaces. Security rules need to be explicit.

### Requirements

- worker registration must be authenticated
- worker mode must be user-approved
- jobs must be scoped to authorized user/team/project
- artifacts must be encrypted in transit
- sensitive artifacts should support retention policy
- third-party automation must be opt-in

### Suggested Safeguards

- per-worker approval token
- project allowlist
- artifact auto-delete windows
- “never use this worker for third-party apps” switch
- “do not record screen” switch
- “manual approval required for install” switch

### Lock-Screen / Privacy

Yaver should respect:

- screen lock state
- privacy mode
- hidden notification content

If a device is locked and cannot execute safely, scheduler should back off or request manual action.

## Reliability

Phones are flaky compared with servers. Design for that.

### Expected Failure Modes

- app backgrounded
- OS suspended worker
- battery saver triggered
- Wi-Fi changed
- user unplugged phone
- phone overheated
- artifact upload interrupted

### Reliability Strategies

- resumable jobs
- small bounded task units
- heartbeats
- lease-based assignment
- cooldown states
- artifact chunking
- graceful requeue
- stream degradation fallback from live video to periodic screenshots
- manual takeover recovery after disconnect

## Farming: When It Actually Makes Sense

This is the honest section.

Phone farming is only worthwhile when all of these are true:

- jobs are independent
- each job is small
- latency is not critical
- the phones are mostly idle
- the user already owns the phones

### Good Farming Workloads

- OCR queue
- screenshot labeling
- small transcription queue
- media compression
- batch visual checks
- test matrix across device/OS combinations
- bounded verification loops across multiple real devices

### Weak Farming Workloads

- general-purpose AI coding
- interactive chat inference
- long-running planning agents
- anything requiring strong shared memory or low-latency coordination

### Best Farming Value for Yaver

Not “cheap LLM cluster.”

Best value is:

- real-device test fleet
- device matrix
- mobile app probe fleet
- lightweight preprocessing fleet

## Phased Rollout

### Phase 1: Inventory and Placement

Goal:

- phone can register as `mobile-worker`
- capability metadata visible in UI
- placement recommendation works

Deliverables:

- device role fields
- worker profile
- worker inventory UI
- placement reasoning

### Phase 2: Basic Job Queue

Goal:

- run concrete jobs on spare phone

Deliverables:

- install build
- launch app
- screenshot
- screen recording
- smoke test
- primary-phone worker selection
- live preview thumbnails

### Phase 3: Push / Deeplink / Permissions

Goal:

- cover real high-value mobile regressions

Deliverables:

- push validation
- deeplink validation
- permission-flow jobs
- failed/fixed clip capture
- comparison timeline on primary phone

### Phase 4: Third-Party Handoff

Goal:

- test external-app flows

Deliverables:

- maps/share/oauth/browser/app-store style handoffs

### Phase 5: Edge Preprocessing + Farming

Goal:

- support limited useful worker-pool workloads

Deliverables:

- OCR queue
- speech queue
- screenshot labeling queue

### Phase 6: Autotest / MCP Integration

Goal:

- make mobile workers native citizens in Yaver agent workflows

Deliverables:

- MCP tools
- autotest integration
- nightly patrols
- morning summaries with mobile evidence
- live control plane from primary phone
- multi-worker orchestration and saved worker groups
- bounded agent-worker loops

## Success Metrics

This feature should be measured by practical leverage, not infra vanity.

### Product Metrics

- number of users enabling mobile worker mode
- number of successful real-device jobs per week
- percentage of mobile regressions caught on worker before ship
- percentage of push/deeplink issues caught on worker
- number of nightly patrol runs

### Infra Metrics

- job success rate
- median job latency
- artifact upload success rate
- worker utilization while charging
- requeue rate
- thermal cooldown rate

### Not A Success Metric

- tokens/sec from second-hand phones

That is not the point of this product surface.

## Non-Goals

The following are explicitly not the goal of `MOBILE_WORKER`:

- replace desktop agent with phone
- run full repo AI coding on spare phone
- host large `Llama` models as the main runtime
- do multi-phone transformer sharding
- pretend old phones are server GPUs

## Recommended First Implementation

If implementation starts tomorrow, the best first vertical slice is:

1. register a spare phone as `mobile-worker`
2. expose worker inventory in mobile + web
3. add `install_build` job
4. add `smoke_test` job
5. add screenshot/video artifacts
6. wire results into primary-phone UI
7. let primary phone choose which worker to use

That yields immediate value for a solo React Native developer.

The second slice should be:

1. push validation
2. deeplink validation
3. permission-flow checks
4. live screen viewing from primary phone
5. failed vs fixed clip comparison

That is where real-device leverage becomes undeniable.

The third slice should be:

1. `agent-worker` capability flag
2. bounded `agent_verify_flow` jobs
3. primary-phone takeover/release controls
4. MCP tools for constrained real-device agent loops

## Final Position

Yaver should absolutely support spare phones as infra.

But the correct framing is:

- `mobile execution infra`
- `mobile validation infra`
- `mobile sensing infra`

not:

- `big LLM infra`

If we build to this framing, second-hand phones become one of the strongest parts of the Yaver story for solo mobile developers.
