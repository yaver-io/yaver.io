# Yaver Relay + Cloud Workspace Business Model Review

Date: 2026-07-18

Purpose: planning memo for a separate Claude Code / engineering review. This
is not an implementation spec yet. It captures the proposed product packaging,
unit economics, wake mechanics, relay-vs-compute boundaries, OAuth risks, and
open questions.

## Executive Summary

Yaver should have only two paid products:

| Product | Price | Buyer Intent |
|---|---:|---|
| Relay Pro | $9/mo | "Connect my own machine from anywhere." |
| Cloud Workspace | $29/mo | "Give me a fast-wake dev machine too." |

The free open-source path remains:

```text
npm install -g yaver-cli
yaver auth
yaver serve
```

Do not sell compute-only. Cloud Workspace must include private relay because
reachability is part of the product. Do not expose many SKUs such as relay-only,
compute-only, relay+compute, managed-agent, etc. Publicly:

```text
Free OSS + limited shared relay -> Relay Pro -> Cloud Workspace
```

Public onboarding should sell Yaver's own connectivity ladder:

```text
same WiFi -> direct LAN
different networks -> free Yaver shared relay with fair limits
daily remote work -> Relay Pro private managed relay
need compute too -> Cloud Workspace
```

Do not teach normies to set up third-party free networking in the landing page,
README, FAQ, or first-run docs. Yaver can remain compatible with existing
private networks and external tunnels for advanced users, but the default public
story should point to Yaver Relay.

Cloud Workspace should be BYO runner at launch: Claude Code, Codex, OpenCode,
Aider, Goose, etc. Do not include managed model usage in the $29 plan. Managed
AI can come later as a separate higher-price plan or add-on, because token COGS
can destroy margin faster than compute.

## Current Implementation Notes

Code audit/update on 2026-07-18:

- New managed Cloud Workspace provisions on the container image path now creates
  a Hetzner data Volume before server creation, attaches it on first boot, and
  mounts it at `/srv/yaver/state` before writing Yaver config, OAuth state,
  repo cache, runner config, or Docker-backed app state.
- The cloud machine row persists `volumeId`, `volumeSizeGb`, and `baseImageId`.
  Park can delete the server without snapshotting when `volumeId` exists; wake
  recreates from the slim base image and re-attaches the data volume.
- Legacy rows without a volume still use the safe snapshot-first fallback. The
  wakeability gate now requires either `lastSnapshotId` or
  `volumeId + baseImageId`, so a base image alone can never be mistaken for user
  data recovery.
- If volume creation succeeds but server creation fails, provisioning best-effort
  deletes the fresh empty volume to avoid leaking parked-storage cost.
- The honest UX remains: the user can queue/vibe immediately through relay while
  the real Cloud Workspace becomes available, but build/deploy work waits for
  the actual owned workspace with repo context.

Important pricing UX correction: do not make Yaver feel like OpenRouter. Yaver
targets normies who want to build and share apps, not developers optimizing
hourly infrastructure spend. The primary product is flat monthly. Internal
credits, quotas, and auto-pause are margin controls, not the thing the user is
buying.

2026-07-19 implementation correction: earlier versions of this memo considered
public Boost packs and prepaid top-ups. That is now superseded for the normal
product. The user-facing web billing surface should expose only Free, Relay Pro,
and Cloud Workspace subscriptions, plus included allowance/status/resource
controls. Any wallet/ledger mechanics remain backend guardrails or future
operator/admin surfaces unless a separate add-on is deliberately launched.

Implementation decision: Cloud Workspace uses one monthly standard-credit pool.
The public plan is flat monthly; the backend meters capacity with hidden weights:

```text
standard profile: 1 standard credit per live hour
heavy profile:    2 standard credits per live hour
build profile:    4 standard credits per live hour
```

The user does not buy 8GB, 16GB, or 32GB snapshots. Yaver chooses the profile
from project/task context and explains it in plain language. Web and mobile may
show remaining standard credits, but should avoid hourly-rate/provider-SKU copy
in normal product surfaces.

Publicly:

```text
$29/mo Cloud Workspace
Build apps from your phone. Yaver handles the machine.
```

Only surface exhausted allowance as a plain pause/limit message:

```text
Workspace use is high this month. Light relay work can continue, but Cloud
Workspace compute pauses until the next period or billing settings are updated.
```

## Product Packaging

### Relay Pro - $9/mo

For people who already have a Mac, Linux box, Pi, NAS, desktop, or VPS.

Includes:

- private managed relay
- stable reachability for the user's own machines
- more devices/projects than the free shared relay path
- guest access / feedback access where appropriate
- relay diagnostics
- no Yaver compute

Honest UX:

```text
Connected to your machine. Running now.
```

If the user's own machine is offline:

```text
Your machine is offline. I can save this request and send it when it reconnects.
```

Relay Pro should not pretend to inspect code when the user's machine is offline.

### Cloud Workspace - $29/mo

For people who want Yaver to run the dev machine too.

Includes:

- everything in Relay Pro
- right-sized cloud workspace, usually 8GB for starter/serverless projects
- 100 GB persistent workspace volume
- included monthly workspace use
- fast wake from parked state
- BYO Claude/Codex/OpenCode auth
- no managed AI tokens included

Suggested public copy:

```text
Start typing immediately. Yaver queues your intent while the workspace wakes,
then runs it on your real cloud dev box.
```

Avoid claiming "instant compute" unless there is a warm VM. The user should feel
the app is immediately responsive, but the UI must be honest about when the live
repo is actually available.

## Margin Targets

Both paid products must clear at least 40% net margin.

Rule:

```text
target net margin >= 40%
max all-in cost = price * 60%
```

| Product | Price | Max All-In Cost For 40% Margin | Target Cost | Target Margin |
|---|---:|---:|---:|---:|
| Relay Pro | $9 | $5.40 | $1.00-$2.25 | 75-89% |
| Cloud Workspace | $29 | $17.40 | $13-$16 | 45-55% |

### Relay Pro Economics

Assumptions:

- payment fee roughly 5% + $0.50
- shared relay pools, not dedicated VM per user

Approximate monthly unit economics:

```text
Revenue:          $9.00
Payment fee:      -$0.95
Relay infra:      -$0.25 to -$1.00
Backend/misc:     -$0.25 to -$0.50
--------------------------------
Profit:           $6.55 to $7.55
Net margin:       73% to 84%
```

Main margin risk: giving each user a dedicated relay VM. Do not do that unless
priced separately.

### Cloud Workspace Economics

Assumptions:

- $29/mo subscription
- payment fee roughly 5% + $0.50
- included monthly workspace use, enforced internally with credits/quotas
- persistent 80-100 GB workspace volume
- private relay included
- BYO model/runner
- no managed AI tokens
- hard auto-pause when included allowance is exhausted

Approximate monthly unit economics:

```text
Revenue:             $29.00
Payment fee:         -$1.95
Compute credits:     -$3.00 to -$5.50
100GB volume:        -$4.50 to -$6.00
Relay/backend:       -$1.00
Support buffer:      -$2.00 to -$3.00
-------------------------------------
Profit:              $13.55 to $15.55
Net margin:          47% to 54%
```

Hard rules to protect the 40%+ margin:

```text
Price:                  $29/mo minimum
Included compute:        120 standard credits/mo, weighted internally
Persistent volume:       100GB included max
Overage:                 no postpaid surprise; pause or explicit future add-on
Extra storage:            +$5 per 100GB
Idle warm window:         60 min default
Park after:               3h idle / overnight
Managed AI:               not included
```

At 120 standard credits/month, the same allowance maps to:

| Internal Profile | Visible Meaning | Included Live Time |
|---|---|---:|
| Standard | normal app / serverless project | 120h |
| Heavy | bigger app / moderate monorepo | 60h |
| Build | native build / large monorepo / Docker-heavy work | 30h |

This protects margin without making normies decide RAM. After the allowance,
Cloud Workspace compute pauses until the next billing period or until an
explicit future add-on exists; no postpaid surprise bills.

## Why $9 Is Not The Main Cloud Price

$9/mo only works as a teaser, not as the main persistent cloud workspace.

With persistent volume:

```text
Revenue:             $9.00
Payment fee:         -$0.95
Compute, 20h:        -$1.80
Volume:              -$3.50 to -$4.50
Relay/backend/misc:  -$0.75
--------------------------------
Profit:              $1.00 to $2.00
Margin:              11% to 22%
```

Therefore:

- $9 can be a limited starter/promo only.
- $29 should be the real Cloud Workspace product.
- Relay Pro remains the high-margin entry product.

## Provider Choice

Do not switch away from Hetzner for v1 unless benchmarking proves the wake SLO is
unachievable.

Current provider take:

| Provider | Good For | Storage Cost Signal | Take |
|---|---|---:|---|
| Hetzner | cheap compute + cheap volumes | about EUR 0.044/GB/mo volumes | best margin |
| DigitalOcean | simpler UX, fast droplets | about $0.10/GiB/mo volumes | easier, worse margin |
| AWS EC2/EBS | reliability and capacity | around $0.08-$0.10/GB/mo EBS | too expensive for $29 default |
| Fly.io | fast machine starts | about $0.15/GB/mo volumes | strong wake story, bad 100GB economics |

For 100GB persistent workspace storage:

```text
Hetzner volume:       ~EUR 4.40/mo
DigitalOcean volume: ~$10/mo
AWS EBS:             ~$8-$10/mo
Fly volume:          ~$15/mo
```

At $29/mo, Fly's volume alone is too much. Hetzner is the only obvious default
that preserves 40%+ margin.

Sources checked on 2026-07-18:

- Hetzner volumes: https://www.hetzner.com/cloud/block-storage/
- Hetzner cloud pricing / server billing: https://www.hetzner.com/cloud/regular-performance
- Hetzner volume docs: https://docs.hetzner.com/cloud/volumes/overview/
- DigitalOcean volume pricing: https://docs.digitalocean.com/products/volumes/details/pricing/
- AWS EBS pricing: https://aws.amazon.com/ebs/pricing/
- Fly.io pricing: https://fly.io/docs/about/pricing/

## Fast Wake Requirement

Target product promise:

```text
Most wakes: under 60 seconds
Normal P50: 30-45 seconds
Normal P95 target: under 60-90 seconds
Fallback/cold repair: 2-5 minutes
```

Do not use full-disk snapshot/restore as the normal park/wake path. It is too
slow and makes the user lose the vibe.

### Correct Lifecycle

Use:

```text
Golden image:
  OS + Docker + Yaver cloud image + Node/Go/Android tooling preinstalled

Persistent volume:
  /workspace
  ~/.yaver
  ~/.claude
  ~/.codex
  ~/.config/opencode
  ~/.config/gh
  ~/.config/glab
  package-manager caches
  repo checkout
  node_modules
  build caches

Disposable server:
  compute only
```

Wake:

```text
create server from golden image
attach persistent volume
mount volume
start yaver
verify runner auth
route session through relay
```

Park:

```text
stop agent/dev services safely
sync filesystem
unmount persistent volume
detach volume
delete server
keep volume
```

Snapshots should be background backups only, not the normal parking primitive.

### Expected Wake Time

With optimized Hetzner golden image + volume:

```text
Create VM from image:       20-40 sec
Attach volume:              3-8 sec
Mount volume:               1-3 sec
Start yaver/container:      3-10 sec
Heartbeat/reconnect:        2-5 sec
--------------------------------
Expected normal wake:       30-60 sec
```

### Warm Spare Option

After demand exists, keep a small warm spare pool per region.

Wake path with spare:

```text
assign already-running spare
attach user's persistent volume
mount volume
start user agent
replace spare asynchronously
```

Potential target:

```text
10-30 sec
```

Cost:

```text
1 CPX51 spare: ~$65/mo
100 cloud users: ~$0.65/user/mo
50 cloud users:  ~$1.30/user/mo
25 cloud users:  ~$2.60/user/mo
```

Recommendation:

- launch without CPX51 spares
- add spares after paid usage proves demand
- do not overbuild before revenue

## Idle Policy

To preserve vibe:

```text
0-60 min idle: stay hot
60-180 min idle: optional warm/park depending user setting
3+ hours idle: park fast
overnight: park fast
weekly/daily: background backup snapshot
```

Never park during:

- active runner/task
- Metro/dev server running
- build/test/deploy running
- terminal/tmux recent output
- phone app foreground
- recent file changes
- explicit "keep warm" setting

## React Native / Full-Stack Capability Boundary

Cloud Workspace can support RN full-stack monorepos, with an important platform
boundary.

| Workload | Cloud Workspace Linux Box |
|---|---|
| Expo / React Native JS editing | yes |
| Hermes bundle push into Yaver mobile app | yes |
| Metro / Next / Vite dev servers | yes |
| Android Gradle builds | yes |
| Android emulator / WebRTC stream | likely yes, needs tuning |
| Convex / Next / full-stack monorepo | yes |
| Docker services | yes, if Docker socket/host runtime is wired correctly |
| iOS Hermes bundle push to Yaver app | yes |
| iOS native build with custom native modules | no, needs macOS/Xcode |
| TestFlight build/upload | no, needs macOS/Xcode |

Honest product copy:

```text
Cloud Workspace runs your full-stack app and pushes React Native changes to
your phone. For iOS native builds, connect a Mac worker.
```

Do not claim:

```text
Cloud Workspace replaces your Mac.
```

That would create refunds and trust problems.

## Relay During Compute Wake

Use the relay during wake, but only as an always-on control/session surface.

Do not run the real Claude/Codex coding process on the relay.

Correct model:

```text
Relay session = interactive intake session
Compute session = real execution session
```

The user experiences one continuous session, but internally it is a handoff
between two producers.

### What Relay Can Honestly Do

Safe relay-side work:

- receive prompt
- accept voice input
- collect screenshots/log attachments
- ask clarifying questions
- show previous task history
- show last known project cards
- queue task
- wake compute
- stream wake status
- maintain one stable session id
- maybe run a cheap, clearly-labeled preliminary classifier/planner

Example copy:

```text
Workspace is waking. I do not have live repo access yet, but you can describe
the change and I will hand it to the workspace when it is ready.
```

### What Relay Must Not Do

Relay must not:

- claim it read live files
- claim it reproduced a bug
- create diffs
- run builds/tests/deploys
- use Claude/Codex OAuth
- access secrets
- mount the workspace volume
- run shell commands against repo state

This is both a trust issue and a security issue.

## Relay Context Cache

Relay may have zero live project context. That is acceptable if the UI is honest.

Reliable relay knowledge:

```text
user identity
subscription
machine id
last known machine status
last selected repo path
last known project name/framework
previous Yaver task summaries
phone screenshot / voice / user prompt
wake progress
```

Unreliable or unavailable:

```text
current files
current git status
current dependencies
secrets
runner auth
node_modules
logs since last sync
actual build state
```

For Cloud Workspace, compute can periodically sync a small non-secret
workspace card to relay/Convex:

```json
{
  "repoName": "my-app",
  "projectPath": "/workspace/my-app",
  "frameworks": ["expo", "next", "convex"],
  "apps": ["apps/mobile", "apps/web"],
  "scripts": ["dev", "test", "typecheck"],
  "lastBranch": "main",
  "lastCommit": "abc123",
  "lastSyncedAt": "2026-07-18T..."
}
```

Optional:

- shallow file tree, limited depth
- package.json scripts/dependencies
- last task summaries
- last known dev server target

Do not cache by default:

- source file contents
- `.env`
- secrets
- full diffs
- private logs
- runner tokens

If cached metadata is shown, label it as stale:

```text
Last seen: apps/mobile, branch main, 2 hours ago
```

## Session Handoff Mechanics

Use one stable user-facing session id.

Event stream:

```text
user_message
relay_ack
wake_started
wake_progress
wake_ready
handoff_started
runner_auth_verified
runner_started
runner_output
tool_call
diff
done
```

Relay pending session shape:

```json
{
  "sessionId": "sess_123",
  "userId": "...",
  "machineId": "...",
  "repoId": "...",
  "messages": [
    { "role": "user", "text": "fix login crash" },
    { "role": "assistant", "text": "Queued. I will inspect files when the workspace is ready." }
  ],
  "attachments": [],
  "selectedRunner": "codex",
  "createdAt": 123,
  "state": "waiting_for_compute"
}
```

When compute wakes:

```text
1. Compute agent registers ready.
2. Relay sends pending_session to compute.
3. Compute reconstructs prompt:
   - user intent
   - relay-side clarifications
   - selected repo/path, if known
   - phone screenshots/logs
   - stale metadata, explicitly marked stale
4. Compute verifies runner auth.
5. Compute starts Claude/Codex/OpenCode fresh.
6. Runner output emits into the same session id stream.
```

Handoff prompt should include:

```text
The user described this while the workspace was waking:

<messages>
<attachments>
<selected project path if known>
<last known metadata, marked stale>

Now:
1. Check current git status.
2. Inspect the relevant files.
3. Verify dependencies/scripts.
4. Then implement.

Do not trust cached relay metadata as current repo state.
```

This is not live process migration. It is a clean task handoff.

## OAuth Design

Two OAuth flows will kill the vibe. Avoid relay-side runner OAuth entirely.

Rules:

```text
Yaver OAuth:
  account, device registry, billing, subscription

Runner OAuth:
  only on the actual dev machine / Cloud Workspace persistent volume

Relay OAuth:
  never for Claude/Codex
```

For Cloud Workspace, runner auth should live on the persistent volume:

```text
~/.claude
~/.codex
~/.config/opencode
~/.config/gh
~/.config/glab
~/.yaver
```

Every newly created compute VM mounts the same volume and uses the same Linux
user/uid. After first setup, runner auth persists across park/wake.

Wake auth flow:

```text
1. Compute boots.
2. Attach/mount volume.
3. Start yaver as stable uid.
4. Verify runner auth:
   - claude auth status
   - codex login status
   - opencode config check
5. If valid, start queued task.
6. If invalid, pause handoff and show reconnect CTA.
```

If invalid:

```text
Workspace is ready. Connect Codex to run this task.
[Connect Codex]
```

After auth completes, the queued task starts automatically. The user's prompt
is preserved.

Important implementation detail already present in the codebase: runner auth
status must distinguish "credentials file exists" from "auth verified by the
runner". File presence alone is not enough.

## OAuth Risks

Possible failures:

| Failure | Cause | Handling |
|---|---|---|
| token expired | provider revoked or aged token | show reconnect CTA before task starts |
| IP changed | new cloud VM IP triggers provider checks | browser/device auth flow |
| stale credential file | file exists but provider rejects it | verify with runner, not file presence |
| uid/gid mismatch | volume mounted under wrong owner | fixed uid for yaver user |
| Codex sandbox write failure | bwrap cannot write to owned path | ensure project ownership matches runner uid |
| session path mismatch | runner stores sessions under machine-specific home | persistent home path on volume |

Do not copy runner OAuth tokens to a shared relay host. It increases security
risk and can break provider trust assumptions.

### Auth When Switching Machine Size

The user should not re-auth when Yaver moves the workspace from 8GB to 16GB or
32GB. Machine size is disposable; the encrypted user volume is the identity
carrier.

Correct model:

```text
8GB VM:
  mounts user volume
  uses /home/yaver from volume
  verifies Claude/Codex/Git auth

need larger machine:
  stop runner cleanly
  unmount/detach volume
  delete 8GB VM
  create 16GB/32GB VM from golden image
  attach same volume
  start same uid/home
  verify auth again
  resume queued task
```

Do not copy token folders between machines during resize. They are already on
the volume. The only copied state should be non-secret telemetry/status back to
Yaver control plane.

Runner-specific placement:

| Credential | Store On Persistent Volume | Store On Relay | Notes |
|---|---:|---:|---|
| Yaver account session | yes | yes, scoped | relay needs Yaver session only |
| Claude Code auth | yes | no | cloud workspace only |
| Codex auth | yes | no | cloud workspace only |
| OpenCode config/BYOK | yes | only if explicitly relay runner | prefer cloud workspace |
| GitHub/GitLab OAuth | yes | limited app token allowed | relay can use narrow Git app token |
| SSH deploy keys | yes | no | avoid shared relay secrets |
| app `.env` / vault secrets | yes/vault mount | no | compute only |

The key distinction:

```text
Yaver auth can exist on relay.
Git metadata access can exist on relay if scoped.
Runner OAuth and project secrets must stay on the user's workspace volume.
```

### Git Auth For Relay Runner

Relay needs enough Git capability to keep the vibe alive while compute wakes,
but it should not need the user's full development auth.

Safe relay Git options:

- Yaver Git native repos: relay can operate directly because Yaver controls the
  repo service and permission model.
- GitHub/GitLab App installation token: relay receives short-lived,
  repo-scoped token for branch/commit/PR/issue operations.
- No raw user SSH key on relay.
- No deploy token on relay.
- No package registry token on relay unless explicitly needed and scoped.

For imported GitHub/GitLab repos, relay should prefer:

```text
create branch
edit source files
commit
open draft PR / issue
wait for compute validation
```

Compute then performs:

```text
install dependencies
read private package registry credentials
run tests/builds
push Hermes/APK/deploy artifacts
deploy with secrets
```

This avoids two Claude/Codex logins while still letting relay do useful work.

## Relay Runner + Cloud Build Machine Variant

There is a stronger variant than "relay only queues while compute wakes":

```text
Relay runner:
  always available
  has a secretless Git mirror / worktree
  can do source-only vibe edits on branches

Cloud Workspace:
  wakes silently
  pulls the branch
  performs builds, Hermes reload, tests, deploys, and device work
```

This makes the product feel active immediately without pretending the relay has
the full machine.

The boundary should be Git-based, not process-migration-based:

```text
user prompt
  -> relay runner creates task branch
  -> relay runner commits source edits
  -> cloud workspace wakes
  -> cloud workspace pulls branch
  -> build/test/Hermes/deploy happens on compute
```

Relay runner may do:

- source edits
- docs/copy changes
- small static checks
- package.json/script inspection
- issue triage
- branch creation
- commits to draft branch
- draft PR / Yaver Git task branch

Relay runner must not do:

- access `.env` or vault
- use deploy credentials
- run native/Hermes builds
- run Docker Compose services with secrets
- deploy production
- use the user's Claude/Codex OAuth unless relay is upgraded into a true
  per-user runner host

OAuth recommendation for v1:

```text
Relay runner uses Yaver-managed lightweight model or user BYOK OpenCode key.
Cloud Workspace uses user's Claude/Codex/OpenCode auth on the persistent volume.
```

Do not require a second Claude/Codex login on the relay.

User-facing copy:

```text
Starting source edit now.
Build machine is waking for device preview.
```

Then:

```text
Build machine ready.
Testing and pushing to phone.
```

This preserves honesty and vibe.

## Autorun And Margin Controls

Autorun is a major product value, but it can destroy margins if uncapped.
Treat autorun as a quota-governed workload, not as an unlimited background
employee. However, the user experience should not be "Yaver killed my run in
the middle of a vibe." Follow the Claude Code / Codex style: quota, scheduling,
concurrency limits, fair-use tiers, and clear capacity states rather than abrupt
stops.

Autorun should be allowed, but every run must consume one or more budgets:

```text
compute credits
runner/model credits
artifact storage quota
parallelism slots
daily task cap
```

### Default Autorun Policy For Cloud Workspace

Suggested included policy for the $29 plan:

```text
1 active autorun at a time
monthly included standard compute quota
daily fair-use envelope
heavy build credits consume faster
max N queued autoruns
no production deploy without owner approval
no heavy build loop without explicit budget confirmation
```

Use credits rather than exposing machine details:

```text
Light relay/source autorun:
  cheap credit rate

Cloud build/test autorun:
  standard compute credit rate

Heavy Android/native/large-monorepo autorun:
  higher credit rate
```

Example:

```text
120 standard compute credits included.
Heavy build minutes use credits faster.
```

### Relay Autorun

Relay-side autorun can be margin-friendly if it is constrained to source-only
work on a Git branch.

Allowed:

- plan
- edit source branch
- commit checkpoint
- open/update issue
- open draft PR
- wait for compute build result

Not allowed by default:

- infinite retry loops
- large test/build loops
- deploys
- secret access
- multiple concurrent branches without cap

This lets the user "keep vibing" while the build machine wakes, but prevents a
single $29 user from burning many CPX51 hours.

### Compute Autorun

Compute autorun must be metered, observable, and resumable. Avoid killing a run
mid-task for normal quota reasons. Instead, use a staged policy:

```text
normal quota available:
  run immediately

quota low:
  warn, continue current step, avoid starting expensive new phases silently

quota exhausted:
  finish the current atomic step where practical
  checkpoint branch/artifacts
  queue remaining work
  continue on relay/source-only mode if possible
  wait for quota reset or explicit billing-setting change before heavy compute resumes

safety abuse / runaway:
  only then stop or park, with checkpoint first where possible
```

The app should show:

```text
This autorun will use about 18 standard minutes.
You have 9h 20m standard compute left this month.
```

For normies, avoid scary billing language, but keep a visible fuel gauge.

### Margin Rules

Autorun must not change the unit economics:

```text
Cloud Workspace $29:
  included credits must fit inside $17.40 max all-in cost
  no automatic overage or hidden usage bill
  no postpaid surprises
  do not start new heavy compute phases when quota cannot pay for them
```

If autorun is popular, introduce add-ons:

```text
Extra compute credits: $10 pack
More parallel autoruns: future Plus/team feature
Always-warm workspace: separate add-on, not included
```

Default owner approval gates:

- push to protected branch
- production deploy
- npm publish
- TestFlight / Play upload
- spending beyond included credits
- creating paid external resources

The safe path:

```text
autorun -> branch -> build artifact -> preview -> owner approve -> merge/deploy
```

Not:

```text
autorun -> infinite loop -> deploy/spend silently
```

Important nuance:

```text
Do not hard-stop normal autorun mid-vibe.
Do meter it like Claude Code / Codex-style quota.
When quota is low, checkpoint and degrade to cheaper modes instead of silently
burning margin.
```

## UI / UX States

Use honest states:

```text
Intake mode:
  Relay is collecting your request.

Live workspace mode:
  Workspace is awake and inspecting code.

Build/deploy mode:
  Real machine is running commands.
```

Suggested copy:

```text
Workspace waking
You can describe the change now. I will inspect the repo as soon as the
workspace is ready.
```

Then:

```text
Workspace ready
Checking git status and project files...
```

Do not use:

```text
I found the bug.
I am editing the file.
I ran the tests.
```

until compute is awake and has live repo access.

## Major Risks

1. Wake p95 exceeds 60 seconds on Hetzner.
   - Mitigation: benchmark golden image + volume attach; add spare pool only
     after paid usage exists.

2. Persistent volume cost or storage growth erodes margin.
   - Mitigation: include 100GB max; charge +$5/100GB; alert before growth.

3. Runner OAuth breaks on newly created compute IPs.
   - Mitigation: keep auth on stable volume, verify before task start, make
     reconnect one-click and resume queued task.

4. Relay overclaims context and loses user trust.
   - Mitigation: hard product rule: relay collects intent, compute touches code.

5. Docker-in-Docker / services issue.
   - Current cloud image runs Yaver in a container. Full-stack scaffold services
     may require Docker Compose. Need to verify whether the cloud container has
     host Docker socket access, host-level services, or a non-containerized
     agent architecture.

6. iOS native-build expectation mismatch.
   - Mitigation: clearly say Linux Cloud Workspace supports RN/Hermes and
     Android/web/backend; iOS native builds require a Mac worker.

7. Support cost exceeds the $3/user buffer.
   - Mitigation: reduce plan complexity, strong diagnostics, cap included
     hours/storage, no managed model in $29.

8. Abuse / unattended compute burn.
   - Mitigation: active-hours allowance, hard auto-park, pause-on-exhaustion,
     never postpaid.

## Normie Build-And-Share Use Case

The likely mainstream use case is not "senior engineer runs a huge monorepo".
It is:

```text
1. User opens Yaver.
2. User starts coding with an agent.
3. Work runs either on their own machine through Relay Pro or on Yaver Cloud
   Workspace.
4. The app is built in a sandbox / workspace.
5. User deploys it.
6. User sends a link or installable preview to friends.
7. Friends try it and send feedback through Yaver / Feedback SDK.
```

This should be the core product story:

```text
Build an app from your phone and share it with friends.
```

For this use case, the production runtime should be serverless/external by
default:

- web app: Cloudflare Workers/Pages or equivalent
- backend: Convex or Supabase
- object/artifact storage: R2 / Hetzner Object Storage
- mobile preview: Yaver app loads Hermes bundle / preview artifact
- mobile distribution: TestFlight / Play internal later, not the default first
  share path

The Cloud Workspace VM should be the dev/build/deploy machine, not the
long-running production host. After deploy, friends should hit the deployed app
or preview artifact, not the parked dev VM.

Recommended "show friends" paths:

| App Type | Fast Share Path | Notes |
|---|---|---|
| Web / Next / Vite | public preview URL | easiest and should be the default |
| Expo/RN JS app | Yaver preview/share bundle | friends may need Yaver app unless web export exists |
| Full-stack app | Cloudflare + Convex/Supabase | serverless enough for MVP sharing |
| Native iOS app | TestFlight later | not instant; requires Mac/signing |
| Native Android app | APK artifact or Play internal later | easier than iOS, but still distribution friction |

Therefore, "Yaver serverless" should mean:

```text
Yaver creates and deploys to serverless providers, then stores the build
artifacts and links. It does not keep the dev VM online as the production app.
```

Convex/Supabase is enough for many normie apps if templates are constrained.
Do not let the first Cloud Workspace product become "host arbitrary databases
and background workers forever on the dev VM".

### Yaver-Native Project Path

The expected default for normies should not be importing a large private
monorepo.
It should be:

```text
Yaver mobile app sandbox
-> Yaver Git repo
-> Yaver serverless template/runtime
-> Yaver deploy/preview artifact
-> friends test and send feedback
```

This path is much easier to keep fast and profitable:

- Yaver controls the repo shape, dependency graph, and build templates.
- The relay can safely understand more context from Yaver Git metadata and
  cached project summaries without storing a whole arbitrary repo.
- The 8GB standard workspace is enough for most template/serverless projects.
- Deploy targets are serverless/object-storage backed, so the dev VM can sleep.
- Artifact storage carries APKs, Hermes bundles, preview bundles, logs, and
  screenshots while compute is parked.
- Feedback SDK items can become Yaver tasks, GitHub/GitLab issues, or Yaver Git
  issues without giving guest users repo/secret access.

This should be the "happy path" in product onboarding. Imported GitHub/GitLab
repos remain supported, but they should be classified and metered according to
size and build pressure.

## Is 8GB RAM Enough?

Short answer: 8GB is enough for a constrained normie/serverless app builder.
It is not enough to honestly promise arbitrary RN/full-stack monorepo work.

### 8GB Is Probably Enough For

- small Next/Vite apps
- small Expo/RN JS apps using Hermes bundle push
- template-generated apps with controlled dependencies
- Convex/Supabase-backed apps where the backend is remote/serverless
- TypeScript checks for small projects
- package installs when caches are warm
- deploying static/serverless output
- storing artifacts externally

### 8GB Is Risky For

- large monorepos
- `pnpm install` across many workspaces
- monorepo-wide `tsc -b`
- Metro + TypeScript + Next dev server at the same time
- Android Gradle builds
- Android emulator / Redroid / WebRTC streaming
- local Docker Compose stacks with Postgres/Redis/MinIO/Convex
- native module rebuilds
- concurrent runner + build + dev server

The original 32GB reasoning still stands for "serious monorepo":

```text
workspace install + monorepo-wide typecheck + Metro can exceed 16GB and OOM.
```

But for the normie app builder, the product can avoid that class of workload by
using serverless providers and constrained templates.

### Product Strategy For 8GB

Keep only two public products, but allow internal right-sizing.

Public product:

```text
Cloud Workspace - $29/mo
```

Internal compute policy:

```text
default light workspace:
  8GB RAM for small/serverless/template apps

burst/heavy workspace:
  32GB RAM when Android/native/large-monorepo/build pressure is detected
```

This protects margin while preserving the simple two-product story.

Possible internal classes:

| Internal Class | User Sees | Use |
|---|---|---|
| light | Cloud Workspace | serverless/web/Expo starter work |
| heavy | Cloud Workspace | RN monorepo/native/heavy build bursts |

Do not expose these as separate SKUs at launch. Instead, show simple capacity
copy:

```text
Yaver picks the right workspace size for the task. Heavy builds may use more
included compute credit.
```

If charging active hours differently by machine class, show it as credits, not
as a confusing machine menu.

Example:

```text
120 standard compute credits included.
Heavy build minutes use credits faster.
```

Suggested credit mapping:

```text
8GB standard workspace:   1 credit/hour  -> about 120 h/mo included
16GB heavy workspace:     2 credits/hour -> about 60 h/mo included
32GB build workspace:     3 credits/hour -> about 40 h/mo included
```

This is materially better than promising 40 hours of CPX51-class compute to
everyone. Most Yaver-created projects should stay in the 8GB/serverless path;
imported large repos and native builds should burn credits faster.

### Large Private Monorepo Ceiling

If the working example is "a normie may develop a large private monorepo at
most," treat that as the ceiling, not the median. A representative repo in that
class is about:

```text
15GB on disk
~235k files
many package.json/package-lock/go.mod/docker-compose surfaces
web, mobile, desktop, cloud, appliance, scanner, CLI, and deploy areas
```

That is not a normal starter app. It is a large product monorepo.

For large private monorepos:

- 8GB is acceptable only for source-only work on one selected leaf app/package
  with sparse checkout or repo slicing.
- 16GB should be the minimum for a usable imported-monorepo workspace.
- 32GB should be used for reliable native/mobile builds, Docker-heavy work,
  monorepo-wide installs, and broad typecheck/test runs.
- Yaver should avoid running "the whole repo" by default. Ask/select the target
  app: web, mobile, API, CLI, desktop, etc.
- Relay can keep the vibe alive with source edits, Git operations, task queues,
  and review while compute wakes or while a heavy build is queued.
- Heavy monorepo work should consume credits faster; it should not be a
  separate public SKU.

UX copy:

```text
Large project detected. Yaver will focus on the selected app first. Native
builds and full-repo checks may use compute credits faster.
```

This lets the product support large imported repos without making the default
cost base large-monorepo-shaped for every $29 user.

## Artifact Storage / Deploy Storage

There is a related question: should Cloud Workspace include storage beyond the
persistent development volume?

Answer: yes, but as a bundled product feature, not as a third paid product.

Cloud Workspace should include a small artifact store for:

- APKs / AABs
- Hermes bundles (`.hbc`)
- JS bundle zips
- web preview bundles
- release artifacts
- build logs
- screenshots / short repro clips
- deploy manifests
- rollback pointers

This storage is different from the workspace volume:

```text
Persistent volume:
  hot dev state, repo, node_modules, runner auth, caches

Artifact object storage:
  immutable build outputs, deploy artifacts, logs, rollback packages
```

Do not store build artifacts primarily on the persistent volume. That bloats
fast-wake state and makes parking/wake slower over time.

Recommended product inclusion:

```text
Cloud Workspace $29/mo:
  100GB persistent workspace volume
  25GB artifact storage included
  30-day artifact retention by default
  latest successful build retained until explicitly deleted
  extra artifact storage +$5/100GB/mo or equivalent
```

Publicly, do not sell "storage" as a third product at launch. Say:

```text
Cloud Workspace includes build artifact storage for APKs, Hermes bundles, logs,
and deploy rollback packages.
```

Implementation contract:

- Yaver-held artifact storage is Cloud Workspace only, with an env-configured
  owner-preview bypass for development.
- Free and Relay Pro users may save external HTTPS artifact links, but cannot
  mint Yaver storage upload URLs or attach Yaver/Convex `storageId` artifacts.
- This keeps "show my app to friends" in the $29 product without creating a
  hidden storage COGS leak in the free or $9 relay products.

### Friend Preview / Feedback Loop

Artifact storage is also a product feature for sharing with friends.

Flow:

```text
1. User builds app in Yaver.
2. Yaver stores shareable artifact:
   - web preview bundle
   - Hermes bundle for Yaver mobile
   - Android APK
   - build logs / source metadata pointer
3. User sends friend a Yaver share link.
4. Friend opens link:
   - web app opens in browser, or
   - Yaver mobile opens/downloads the Hermes preview bundle, or
   - Android APK install path is shown where allowed.
5. Friend reports issues through Yaver Feedback SDK / in-app feedback.
6. Feedback routes back to the owner's workspace as a task package.
7. Owner fixes, rebuilds, and updates the same preview link.
```

This is more valuable than raw storage. The storage is the backing mechanism for
"show my app to friends and get actionable feedback."

Important boundaries:

- Friend preview artifacts should be immutable/versioned.
- A share link should point to a release channel such as `latest`, `staging`,
  or a pinned artifact id.
- Feedback SDK tokens should be scoped to the shared app/project, not the
  owner's whole machine.
- Friends should not need access to the owner's relay, shell, repo, or secrets.
- The owner's Cloud Workspace does not need to stay awake for friends to try the
  latest artifact.

This fits the scale-to-zero model: shareable apps and feedback stay available
from object storage/serverless infrastructure while dev compute sleeps.

## Friend / Guest Feedback And Collective Development

Yaver Feedback SDK can become the collaboration layer for normie-built apps.
Friends should not need a developer account or repo access to help improve an
app.

Default guest flow:

```text
1. Friend opens shared app preview.
2. Friend shakes / taps feedback.
3. Friend writes a prompt, records voice, or attaches screenshot.
4. Feedback SDK captures safe context:
   - route/screen
   - screenshot
   - console/runtime error
   - device info
   - optional replay/blackbox summary
5. Yaver creates a queued item for the owner.
6. Owner reviews it in Yaver and chooses:
   - vibe on it now
   - turn it into GitHub/GitLab/Yaver Git issue
   - assign it to an agent run
   - reject/archive
```

This supports "collective development with normie friends" without giving
friends shell, repo, runner OAuth, or deploy permissions.

### Guest Modes

Use explicit permission levels:

| Mode | Who | Can Do | Cannot Do |
|---|---|---|---|
| Feedback-only | default friend/tester | submit prompt, screenshots, logs, repro notes | run agent, edit code, deploy |
| Suggestion queue | trusted tester | create prioritized tasks / issues | write directly to repo |
| Co-vibe | invited collaborator | start agent runs in scoped project/branch | deploy/push main unless granted |
| Maintainer | owner/team | approve, merge, deploy | n/a |

Default should be Feedback-only.

The SDK may use "vibe-like" input, but the output should be a queue item unless
the owner explicitly grants more authority.

Suggested copy for friends:

```text
Shake to suggest a fix.
The app owner will review it in Yaver.
```

Suggested copy for owner:

```text
3 friends reported this screen. Start a fix?
```

### Queue Destinations

A feedback item can become:

- Yaver task
- GitHub issue
- GitLab issue
- Yaver Git issue / internal lightweight issue
- agent run on a new branch
- release note / changelog item

Do not automatically commit/push to main from friend feedback. Safe automation:

```text
friend feedback -> queued task -> agent branch -> diff/preview -> owner approve -> push/merge/deploy
```

For trusted collaborators, optional policy:

```text
friend feedback -> agent branch -> draft PR
```

Still avoid direct deploy unless owner grants a deploy role.

### Feedback SDK Token Scope

Feedback SDK tokens should be scoped narrowly:

```text
project/app id
artifact/release channel
guest id
allowed event types
expiration
rate limit
no shell access
no vault access
no runner OAuth
no broad machine access
```

This matches the product promise: friends can help shape the app, but they do
not get access to the owner's machine.

### Collective Development UX

The owner should see a board like:

```text
Inbox
  - Sarah: "Login button is hidden on iPhone SE" + screenshot
  - Mert: "Add dark mode" + voice note
  - Alex: crash on /checkout, 3 reports grouped

Actions
  [Start vibe fix] [Create issue] [Ask follow-up] [Archive]
```

This gives Yaver a social loop:

```text
build -> share -> friends try -> feedback queue -> owner vibes fixes -> redeploy
```

That loop is easier to monetize than raw compute because it creates recurring
collaboration value.

Storage provider options:

| Provider | Use | Notes |
|---|---|---|
| Hetzner Object Storage | default for EU/Hetzner-local artifacts | low base price, S3-compatible |
| Cloudflare R2 | public downloads / egress-heavy artifacts | no egress fees, good for public delivery |
| Persistent volume | hot workspace only | do not fill with immutable artifacts |

Current pricing signals checked 2026-07-18:

- Hetzner Object Storage advertises a monthly base price including 1TB storage
  and 1TB egress: https://www.hetzner.com/storage/object-storage/
- Cloudflare R2 advertises $0.015/GB-month storage and no egress fees:
  https://developers.cloudflare.com/r2/pricing/

Recommendation:

```text
Use Hetzner Object Storage for private artifact storage if the compute is on
Hetzner. Use Cloudflare R2 for public/share/download-heavy artifacts.
```

The first launch can choose one provider. If the product already deploys web
through Cloudflare Workers, R2 may be operationally easier for public downloads;
Hetzner may be cheaper for large private artifact pools.

## Deploy / Serverless Scope

Yaver already has deploy surfaces. Cloud Workspace should not become an
always-on app hosting platform by default. That would change the business from
"dev workspace" to "production PaaS" and introduce uptime/SLA/support risk.

Recommended boundary:

```text
Cloud Workspace:
  develop, build, test, preview, package, deploy

External/serverless providers:
  run the production app
```

Good production targets:

- Convex for backend
- Supabase for Postgres/auth/storage when selected by the app
- Cloudflare Workers/Pages/R2 for web and artifacts
- App Store / TestFlight / Google Play for mobile distribution

Do not promise that the Cloud Workspace VM is the user's production server.
Allow advanced users to self-host from it if they insist, but do not make it
the default paid promise.

Suggested user-facing language:

```text
Yaver Cloud Workspace is your dev and deploy machine. Your production app still
ships to Convex, Supabase, Cloudflare, the App Store, or Google Play.
```

This keeps compute intermittent, margin protected, and support bounded.

## Should Compute Be Always Up?

Default answer: no.

Always-up CPX51-class compute destroys margin unless the plan is much more
expensive. It also shifts user expectation from "workspace wakes fast" to
"production server/SLA".

Instead:

```text
Default:
  fast wake + included compute credits + auto-park

Optional:
  keep warm while active
  prepaid overage when included credits are exhausted
  future always-on add-on if enough users demand it
```

If an always-on option exists later, price it separately and honestly:

```text
Always-on Workspace add-on:
  +$49-$79/mo depending machine
```

Do not include always-on compute in the $29 plan.

## Machine Selection / Hetzner Capacity

Cloud Workspace should select the best currently available Hetzner machine, not
blindly hard-code one type forever.

Selection goals:

1. maintain at least 32GB RAM for real monorepos
2. prefer low cost
3. prefer same region as user's relay/data
4. avoid deprecated or unavailable SKUs
5. preserve x86 compatibility unless the toolchain is known ARM-safe
6. keep disk/volume compatibility for wake/attach

Default target:

```text
CPX51-class: 16 vCPU / 32GB RAM
```

Fallback order should be policy-driven, not scattered constants. Example:

```text
eu:
  cpx51 -> cx53 -> ccx33

us:
  cpx51/cpx41 if orderable -> cx53 -> ccx33
```

Use live provider checks before provisioning:

```text
GET /v1/server_types
GET /v1/datacenters
```

The codebase already has a resilient create concept in
`desktop/agent/cloud_capacity.go`; Claude Code should verify whether managed
cloud provisioning uses it consistently and whether the backend has equivalent
provider-side fallback.

Open question: the repo currently has conflicting CPX51 price assumptions in
different files. Any production billing launch must reconcile these constants
against the live Hetzner API and then put pricing in one source of truth.

## Open Questions For Claude Code Review

Please evaluate:

1. Does the current managed cloud implementation support persistent Hetzner
   volumes, or is it still snapshot/delete only?
2. Can the existing cloud-init path mount a persistent volume to `/srv/yaver/state`
   and preserve runner auth safely?
3. Does the current containerized cloud agent have access to Docker Compose for
   user full-stack services, or does the architecture need host-level agent
   execution?
4. What is the real measured wake time for:
   - create from golden image
   - attach volume
   - mount volume
   - start yaver
   - first successful `/health`
5. What are the exact current CPX51 prices by region? The repo has conflicting
   older assumptions (`€54.90/mo` in metering doc vs `€65.99/mo` UI constants).
6. Is any user-facing surface still advertising the retired Cloud Agent
   concept? If yes, change it to the two paid products: Relay Pro and Cloud
   Workspace.
7. How should the relay pending-session event log be stored without persisting
   sensitive code/task content beyond the privacy contract?
8. Can runner auth verification be performed quickly enough during wake without
   adding significant delay?
9. What exact data is safe to include in the relay-side workspace card?
10. What tests should gate the "relay intake -> compute handoff -> runner start"
    path?
11. Should Cloud Workspace artifact storage use Hetzner Object Storage, R2, or a
    hybrid based on private-vs-public artifact access?
12. Where does `yaver deploy` currently store APK/Hermes/build artifacts, and
    what should move to object storage?
13. Does any deploy path assume the dev VM remains alive after deployment? If
    yes, that path conflicts with scale-to-zero workspace economics.
14. Is there one policy-driven Hetzner machine selector, or are server-type
    fallbacks duplicated across backend, CLI, and mobile UI?
15. What is the lowest fallback SKU that still supports a serious RN/full-stack
    monorepo without OOM during install/typecheck/build?


## Decision Snapshot

Recommended next product/business decisions:

```text
Paid products:
  Relay Pro        $9/mo
  Cloud Workspace  $29/mo

Main cloud architecture:
  Hetzner CPX51-class compute
  persistent Hetzner volume
  golden image
  delete compute when parked
  background snapshots only

Wake UX:
  relay online immediately
  user can describe/queue task while compute wakes
  compute handoff starts once live repo and runner auth are available

OAuth:
  no runner OAuth on relay
  runner auth persists on compute workspace volume

Margin:
  both paid products must stay above 40% net margin
```
