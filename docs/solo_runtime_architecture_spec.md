# Yaver Solo Runtime Architecture Spec

Status: draft  
Date: 2026-04-24

## Goal

Define the smallest coherent Yaver product for the target persona:

- solo developer
- comfortable with monorepos
- mostly React Native
- wants low opex
- wants to self-host as much as possible
- wants managed cloud only as a peace-of-mind always-on option

This spec is not "integrate every vendor." It is the opposite: pick the common stack and make it obvious, cheap, and operable from the phone, CLI, MCP, and vibing flows.

## Product Thesis

Yaver should be the orchestration layer for a solo developer's runtime.

- Cloudflare is the default edge for public web and DNS.
- Convex is the default managed app backend when the user wants serverless.
- Yaver agents on the user's own machines are the default execution plane for cron, monitors, backups, builds, vibing, and admin work.
- Yaver managed cloud is an optional always-on execution plane with the same semantics as a user-owned machine.

The product should strongly prefer running recurring and operational work on Yaver machines instead of third-party metered platforms.

## Non-Goals

- Replace analytics products.
- Replace end-user auth.
- Replace database hosting broadly.
- Build a general distributed cluster scheduler.
- Support every deploy target equally.

## Existing Surfaces We Reuse

These are already in the repo and should remain the substrate:

- Workspace manifest: [yaver.workspace.yaml](/Users/kivanccakmak/Workspace/yaver.io/yaver.workspace.yaml:1), [workspace.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/workspace.go:1), [workspace_engine.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/workspace_engine.go:1)
- Project manifest: [project_manifest.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/project_manifest.go:1)
- Local scheduler: [scheduler.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/scheduler.go:1), [/schedules](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/httpserver.go:3362), [cron_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/cron_cmd.go:1)
- Uptime monitor: [monitor_cmd.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/monitor_cmd.go:1), [monitor_http.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/monitor_http.go:1)
- DNS and Cloudflare primitives: [ops_dns.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/ops_dns.go:1), [cloudflare_dns.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/cloudflare_dns.go:1)
- Domain metadata and verification: [userDomains.ts](/Users/kivanccakmak/Workspace/yaver.io/backend/convex/userDomains.ts:1)
- Cloud machines: [cloudMachines.ts](/Users/kivanccakmak/Workspace/yaver.io/backend/convex/cloudMachines.ts:1), [cloud.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/cloud.go:1)
- Machine inventory: [console_machines.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/console_machines.go:1)
- Vibing execution and eligibility: [vibing.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/vibing.go:700)
- Mobile transport surface: [quic.ts](/Users/kivanccakmak/Workspace/yaver.io/mobile/src/lib/quic.ts:1)

## Core Decision

Do not introduce a third manifest.

Use:

- `yaver.workspace.yaml` for monorepo-wide structure
- `.yaver/project.yaml` for one project/runtime's desired state

The new work extends those two files.

## Desired State Model

### 1. Workspace Manifest

`yaver.workspace.yaml` remains the monorepo index. It gains optional placement defaults.

New fields:

```yaml
version: 1
name: carrotbet
workspace:
  root: .
  primary_device: auto
  relay: managed
  vault: local
  placement:
    default_execution_role: primary
    managed_cloud_fallback: true
    budget_mode: prefer-owned

apps:
  - name: web
    path: ./apps/web
    stack: vite
    provider:
      deploy: cloudflare-pages
    runtime:
      public_surface: true
      machine_role: primary

  - name: backend
    path: ./backend
    stack: convex
    provider:
      deploy: convex
    runtime:
      public_surface: false
      machine_role: cron
```

Purpose:

- declare apps
- declare stack and deploy provider hints
- declare coarse machine-role intent per app

### 2. Project Runtime Manifest

`.yaver/project.yaml` becomes the project-level source of truth for runtime, jobs, domains, and placement.

Replace the current narrow shape with:

```yaml
name: carrotbet
stack: react-native-monorepo
backend: convex
auth: better-auth

runtime:
  frontend:
    kind: cloudflare-pages
    app: web
    branch: main
    domain: carrotbet.com
  backend:
    kind: convex
    app: backend
    deployment: production
  mobile:
    app: mobile
    ota:
      provider: yaver
      channel: production

placement:
  roles:
    primary:
      mode: owned-or-cloud
    build-mac:
      mode: owned
      capabilities: [ios-build]
    cron:
      mode: always-on
    gpu:
      mode: optional
      capabilities: [local-llm]
  assignments:
    web: primary
    backend-admin: cron
    ios-release: build-mac
    vibe-heavy: gpu
  policy:
    prefer_owned: true
    allow_managed_cloud: true
    monthly_budget_usd: 25

domains:
  - domain: carrotbet.com
    target: frontend
    dns_provider: cloudflare
  - domain: api.carrotbet.com
    target: backend-admin
    dns_provider: cloudflare

jobs:
  - id: nightly-build
    kind: workflow
    machine_role: build-mac
    schedule:
      cron: "0 2 * * *"
      timezone: Europe/Istanbul
    steps:
      - kind: shell
        run: npm run build:web
      - kind: shell
        run: npm run test

  - id: convex-reconcile
    kind: convex-action
    machine_role: cron
    schedule:
      cron: "*/15 * * * *"
      timezone: UTC
    convex:
      function: jobs:reconcile
      args:
        lane: production

  - id: homepage-monitor
    kind: monitor
    machine_role: cron
    monitor:
      url: https://carrotbet.com
      interval: 60s
      alert_after_failures: 3

deploy:
  web:
    provider: cloudflare-pages
    app: web
  mobile:
    ios: testflight
    android: playstore
  backend:
    provider: convex

env:
  CONVEX_DEPLOYMENT: production
```

Purpose:

- express the whole project runtime in one place
- keep deploy, domain, jobs, and machine placement together
- give vibing and MCP a structured target to edit

## Machine Role Model

Machine roles are logical, not tied to one physical host forever.

Supported built-in roles:

- `primary`: default machine for repo work and admin actions
- `cron`: always-on machine for scheduled jobs, monitors, backups
- `build-mac`: machine capable of iOS release builds
- `gpu`: machine for local LLM or heavier vibe work
- `edge-admin`: machine used for Cloudflare/DNS/deploy control if needed

Resolution rules:

1. explicit assignment in `.yaver/project.yaml`
2. workspace default role
3. current selected machine
4. managed cloud fallback if policy allows

Selection constraints:

- `mode: owned` means only user-owned machines
- `mode: always-on` prefers online, non-laptop, non-needs-auth machines
- `capabilities` must match `MachineCapabilities`
- if nothing matches and managed cloud fallback is allowed, pick a compatible cloud machine

## Job Model

Jobs should be Yaver-native. Convex cron becomes an optional target, not the scheduler itself.

Supported kinds:

- `shell`
- `workflow`
- `convex-action`
- `deploy`
- `monitor`
- `backup`

Execution semantics:

- all jobs are reconciled into the existing local scheduler in [`scheduler.go`](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/scheduler.go:1)
- each job carries a `machine_role`
- the selected machine executes the job locally
- `convex-action` means "run this Convex function from the machine", not "install this in Convex cron"

This is the key opex decision.

### Convex Usage Rule

Use Convex for:

- app backend functions
- reactive data
- auth and device registry for Yaver

Do not default to Convex for:

- recurring operational jobs
- monitors
- backup schedules
- deploy orchestration

## Reconciliation Model

`yaver apply` and the matching MCP/mobile actions should:

1. load `yaver.workspace.yaml`
2. load `.yaver/project.yaml`
3. resolve machine-role assignments
4. compute plan
5. apply:
   - deploy config
   - domain records and verification metadata
   - scheduled jobs
   - uptime monitors
   - backups and alerts

The existing [`ApplyManifest`](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/project_manifest.go:66) path is the starting point, but it needs typed runtime reconciliation instead of the current narrow add-only behavior.

## New HTTP and MCP Surface

Minimal first set:

### 1. `GET /project/runtime`

Returns merged runtime view:

- workspace manifest
- project manifest
- resolved machine assignments
- drift summary

MCP alias:

- `project_runtime_get`

### 2. `POST /project/runtime/plan`

Input:

- optional manifest patch

Output:

- structured plan
- target machines
- cost and placement notes
- changes to schedules, domains, monitors, deploy config

MCP alias:

- `project_runtime_plan`

### 3. `POST /project/runtime/apply`

Applies the plan and returns stepwise results.

MCP alias:

- `project_runtime_apply`

### 4. `POST /project/runtime/patch`

Patch-friendly endpoint for vibing and UI.

Examples:

- add a job
- change frontend deploy provider
- assign `cron` role to managed cloud

MCP alias:

- `project_runtime_patch`

### 5. `POST /project/runtime/resolve-machine`

Input:

- role
- optional project
- optional required capabilities

Output:

- chosen machine
- why it was chosen
- fallback reasoning

MCP alias:

- `project_runtime_resolve_machine`

## New Ops Verbs

Add a high-level `runtime` ops verb rather than forcing everything through older fragmented endpoints.

Payload:

```json
{
  "op": "get|plan|apply|patch|resolve-machine",
  "root": "/repo",
  "patch": {},
  "role": "cron"
}
```

This should live beside the existing verbs in [`ops_workspace.go`](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/ops_workspace.go:1) and [`ops_cloud.go`](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/ops_cloud.go:1).

## Mobile Product Surface

Replace the current fragmented Ops tabs with project-centric runtime control.

Primary screens:

### Project Runtime

Per project:

- frontend target
- backend target
- machine-role assignments
- domains
- job count
- monitor count

### Jobs

Per project:

- list jobs
- create/edit/pause/resume/run-now
- choose machine role
- pick `shell`, `convex-action`, `monitor`, `deploy`

This should sit on top of the existing `/schedules` and `/monitors` substrate, not bypass it.

### Machines

Per project:

- show available owned machines
- show managed cloud machine if subscribed
- show which role each machine is eligible for
- allow reassigning `primary`, `cron`, `build-mac`, `gpu`

### Domains

Reuse and simplify the current domain flow:

- choose project target
- choose Cloudflare/manual
- create DNS records
- verify
- surface TLS status

### Vibing Actions

Expose structured intents:

- "add a nightly build"
- "move jobs to my Pi"
- "deploy web to Cloudflare"
- "attach api.carrotbet.com"
- "use managed cloud as cron runner"

## Web Product Surface

The web dashboard should not be a generic admin pile. It should mirror the same runtime model.

Primary views:

- Project Runtime
- Domains
- Convex
- Machines
- Jobs

The existing views in [ConvexView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/ConvexView.tsx:1) and [DomainsView.tsx](/Users/kivanccakmak/Workspace/yaver.io/web/components/dashboard/DomainsView.tsx:1) should plug into the new runtime page instead of standing alone.

## Vibing Integration

Vibing should stop treating ops as loose text whenever structured targets exist.

New behavior:

1. parse intent
2. patch `.yaver/project.yaml`
3. run `project_runtime_plan`
4. show summary
5. run `project_runtime_apply` if approved

Examples:

- "run `jobs:reconcile` every 15 minutes on my always-on box"
- "serve the marketing site from Cloudflare and keep background jobs on my Mac mini"
- "if my Mac mini is offline, fall back to managed cloud for cron only"

## Migration Strategy

### Phase 1

Schema and read paths only.

- extend `ProjectManifest`
- add runtime resolver
- add `GET /project/runtime`
- no destructive writes

### Phase 2

Jobs and monitor reconciliation.

- map `jobs` to `/schedules`
- map `monitor` jobs to `/monitors`
- support `convex-action` as machine-run jobs

### Phase 3

Domains and Cloudflare flow.

- map `domains` to `userDomains`
- integrate Cloudflare DNS auto-create when credentials exist
- add TLS and verification status to runtime summary

### Phase 4

Machine-role assignment and managed cloud fallback.

- resolve roles against `console_machines`
- allow managed cloud as execution target
- show budget and placement reasoning

### Phase 5

Vibing and mobile/web product polish.

- structured patch flows
- project-centric runtime UI
- deprecate older fragmented ops tabs where redundant

## First Implementation Sequence

Implement in this order:

1. extend `ProjectManifest` in [project_manifest.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/project_manifest.go:1) with `runtime`, `placement`, `jobs`
2. add runtime resolver against [console_machines.go](/Users/kivanccakmak/Workspace/yaver.io/desktop/agent/console_machines.go:1)
3. add `/project/runtime` read endpoint
4. add `/project/runtime/plan`
5. reconcile `jobs` to existing scheduler
6. reconcile monitor jobs to existing monitor store
7. add mobile `Project Runtime` screen
8. add vibing patch/apply flow

## Sharp Edges To Avoid

- Do not create a separate cron engine for Convex.
- Do not make managed cloud mandatory for schedules.
- Do not expose raw provider credential complexity first.
- Do not build cluster scheduling.
- Do not force every project into self-hosting; Convex and Cloudflare remain first-class narrow defaults.

## Success Criteria

A solo developer should be able to do all of this from Yaver:

- declare that web deploys to Cloudflare
- declare that backend uses Convex
- run recurring admin jobs from their own machine instead of Convex cron
- choose which machine handles builds, cron, and vibe-heavy work
- fall back to managed cloud only when needed
- operate all of it from mobile, CLI, MCP, and vibing

If this works, Yaver stops being "remote coding plus a bag of ops tools" and becomes the runtime control plane for the common solo stack.
