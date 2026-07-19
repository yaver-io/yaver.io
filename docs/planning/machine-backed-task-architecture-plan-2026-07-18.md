# Machine-Backed Task Architecture Plan

Date: 2026-07-18

Status: plan plus backend/web/mobile implementation slice. Code is the source
of truth; this plan was written after reading the current repo surfaces listed
below, but every task must re-grep the code before editing.

Implemented on 2026-07-18:

- `backend/convex/schema.ts` now has privacy-safe `projectProfiles` and
  `taskPlacements` tables.
- `backend/convex/taskPlacement.ts` now exposes `upsertProjectProfile`,
  `preview`, `record`, `listRecent`, `getForActivation`,
  `attachCloudMachine`, and `markStatus`.
- `backend/convex/http.ts` now exposes bearer-authed placement routes:
  `/tasks/placement/preview`, `/tasks/placement/record`,
  `/tasks/placement/recent`, `/tasks/placement/status`, and
  `/tasks/placement/activate`.
- `mobile/src/lib/taskPlacement.ts` now wraps those HTTP routes for mobile.
- `web/lib/task-placement.ts` wraps the same HTTP routes for web.
- Web dashboard chat and `VibeCodingView` preview, record, display, and
  activate task placements after the agent accepts a task.
- Mobile task creation previews placement before dispatch. Cloud Workspace
  decisions are queued locally while compute wakes; owned-machine/relay tasks
  still run through the selected agent path and sync placement status after
  acceptance.
- `backend/convex/_generated/api.d.ts` was regenerated with Convex codegen.
- Desktop agent `/tasks` now records backend placement metadata after local
  task acceptance via `desktop/agent/task_placement_client.go`. The recorder
  sends only coarse labels and counters, stores the returned placement decision
  on the local task, and returns it in task create/list/detail responses. The
  prompt, stdout, repo path, files, branch, remote, and secrets remain local.
- Desktop agent `/tasks` now previews placement before local runner launch.
  If the backend chooses a Cloud Workspace that is not ready on this daemon,
  the handler records a prompt-free `pending-cloud:*` placement, attempts
  placement activation, returns
  `409 { action: "cloud_workspace_required", pendingTaskId, placement,
  activation? }`, and creates no local task. The caller must keep the prompt
  client-side, wait for activation, then dispatch directly to the assigned
  workspace. Structured activation blockers such as `runner_auth_required`
  pass through this response, so the UI can ask for sign-in instead of retrying
  or launching on the wrong machine.
- Desktop HTTP clients now decode that `cloud_workspace_required` response into
  a typed `CloudWorkspaceRequiredError` instead of flattening it into a generic
  HTTP failure. Local agent requests, attach sessions, terminal task creation,
  and remote-agent JSON forwarding preserve the pending id, target device, lane,
  reason, and activation result for the next handoff layer.
- HTTP terminal task creation and the `yaver code` task helper now perform a
  first-pass caller-held handoff: on `CloudWorkspaceRequiredError`, they keep the
  prompt in memory, wait up to roughly one minute for the target workspace to
  become reachable through the existing direct/public/relay candidate stack,
  then POST the original task body directly to that workspace with
  `allowLocalFallback=true`. Terminal mode streams SSE from the selected target
  transport; `yaver code` receives the remote task id. If the target is still
  unreachable after the bounded wait, the prompt remains local and the caller
  receives the pending id/target in the typed error.
- After the target workspace accepts the real task, desktop handoff now calls
  `/tasks/placement/rebind` with only `{ placementId, taskId, status }` so the
  prompt-free pending placement and any linked wake runs point at the actual
  runner task id. Rebind failure is best-effort: the real task is not failed or
  retried just because bookkeeping failed, avoiding duplicate task creation.
- Desktop task completion now also marks the prompt-free placement lifecycle
  from the agent's `OnTaskDone` hook: completed tasks become `completed`, and
  failed or stopped tasks become `failed`. This closes Cloud Workspace
  placement records even if the web dashboard is closed, without uploading
  prompts, stdout, repo paths, branches, or secrets.
- `/tasks/placement/status` now supports a read-only GET shape that joins the
  prompt-free placement row with the latest linked wake/provision run. If a
  machine-level wake/provision run has been re-associated with a newer
  placement for the same Cloud Workspace, status falls back to the latest
  wake/provision run on that machine. Web and mobile clients have typed helpers
  for this so pending Cloud Workspace tasks can show honest phase/progress
  without guessing from a stale placement row or storing prompt/source context
  centrally.
- Web dashboard pending Cloud Workspace dispatch loops now refresh each
  prompt-held row through that placement-status read model before attempting
  dispatch. Placeholders show wake phase/progress and map Yaver-auth,
  runner-auth, resize, and wake-failed phases into actionable blocked states,
  while the prompt remains only in browser local storage until the target
  workspace connects.
- Mobile pending Cloud Workspace dispatch helpers now have the same
  placement-status merge primitive as web, including wake phase/progress,
  user-action blocker mapping, and prompt-free placeholder rendering.
- Mobile QUIC task creation now preserves the desktop agent's structured
  `cloud_workspace_required` response as a typed transport error instead of
  flattening it into a generic alert string. The mobile UI can now read the
  pending task id, placement metadata, activation blocker, target device, and
  wake reason needed for the same client-held Cloud Workspace queue used on
  web.
- The mobile task screen now consumes that typed defer path: when an agent
  returns `cloud_workspace_required`, the phone stores the prompt-bearing task
  body only in its local pending queue, writes prompt-free dispatch-intent
  metadata to Convex, renders a Cloud Workspace placeholder in the task list,
  refreshes wake/provision status, and dispatches the held task to the assigned
  workspace once that target is already connected in the mobile connection
  pool.
- Web and mobile final handoff POSTs now set `allowLocalFallback=true` only
  when sending the client-held task body to the already selected workspace.
  This prevents the target agent from re-running placement and returning a
  second `cloud_workspace_required` response for the same prompt, while initial
  task creation still fails closed before local execution on the wrong machine.
- Mobile now has direct pending-dispatch tests for the client-held Cloud
  Workspace queue: prompt-free dispatch-intent merges, user-action blocker
  preservation, wake progress rendering, and placeholder prompt-leak
  prevention.
- Mobile Cloud Workspace defer decoding now lives in a dependency-free helper
  that `quic.ts` re-exports. It has a direct test for the exact
  `409 cloud_workspace_required` agent response and rejects unrelated or
  malformed conflicts, so the mobile handoff entry point is pinned without
  importing React Native in Node tests.
- Web and mobile task-create request bodies now have direct tests for the
  `allowLocalFallback` handoff guard. Normal initial task creation serializes
  the guard as absent/false, while final Cloud Workspace handoff serializes it
  as `true`.
- Desktop handoff now also writes prompt-free `taskDispatchIntents` metadata
  during the bounded wait: queued when handoff starts, dispatching for each
  target POST attempt, dispatched after workspace acceptance, and blocked when
  the target is still unreachable after the wait. These records contain only
  ids, source surface, lane, target device, runner/project labels, reason,
  attempts, and short errors; prompt/body/workdir/files/stdout/secrets remain
  with the caller until the final target `/tasks` POST.
- Classic `yaver attach` now retargets active-task polling after a Cloud
  Workspace handoff. The creator returns `{ taskId, baseURL, headers }`; local
  mobile-task discovery continues polling the local daemon, while the active
  handoff task is polled from the chosen workspace transport with relay headers
  preserved.
- The code-terminal TUI now stores the same active-task remote reference and
  fetches the cloud task during each local poll tick, so cloud handoff output
  appears in the TUI and completed remote tasks do not become invalid local
  `/continue` sessions.
- Legacy QUIC terminal task creation now uses the same placement contract before
  launching a local runner. When placement selects a different Cloud Workspace,
  QUIC returns a structured `cloud_workspace_required` error, records a
  prompt-free `pending-cloud:*` placement, attempts activation, and creates no
  local task. The QUIC terminal client keeps the prompt in memory, waits for the
  target workspace through the existing direct/public/relay candidate stack,
  posts to that workspace over HTTP with `allowLocalFallback=true`, and streams
  output from the selected target.
- Desktop cloud handoff now has a local restart-safe spool at
  `~/.yaver/pending-cloud-dispatch.json` with owner-only permissions. The
  prompt-bearing task body remains on disk only on the caller's machine; Convex
  still receives only prompt-free dispatch-intent metadata. A successful target
  acceptance deletes the local row. A bounded wait timeout leaves the row
  `blocked`, and terminal startup retries queued/blocked rows while skipping
  `dispatching` rows to avoid duplicate task creation after a crash during the
  final POST.
- Desktop terminals now expose a `cloud` / `cloud-pending` command that reads
  that local spool and prints status, lane, target device, attempts, age, and
  last error. It does not render title, prompt, userPrompt, workdir, files,
  stdout, or secrets.
- Desktop Cloud Workspace handoff now emits prompt-free live progress events
  while the caller is inside the bounded wait: queued while the workspace is not
  reachable, dispatching when a target transport is found, queued again after a
  failed target POST, dispatched after target acceptance, and blocked after the
  timeout. Terminal/attach surfaces print those transitions so the user sees
  that Yaver is waiting or retrying instead of silently hanging.
- Desktop Cloud Workspace handoff now fails fast into a local `blocked` prompt
  spool when activation already returned an actionable blocker such as
  `runner_auth_required`, `yaver_auth_required`, `wake_failed`, or
  `resize_required`. Those blockers are prompt-free in Convex and avoid wasting
  the user's one-minute attention window on retries that cannot succeed without
  sign-in, billing, resize, or provider recovery.
- Web pending Cloud Workspace dispatch now stores the structured activation
  blocker action locally and does not auto-dispatch rows blocked on runner auth,
  Yaver auth, billing, resize, or wake failure when the target later appears.
  Plain reachability blocks remain retryable, so "workspace came online after
  the wait" still recovers without resubmitting the prompt centrally. The local
  store also preserves those user-action blockers when a stale queued
  dispatch-intent response arrives after activation.
- Backend dispatch intents now carry the same prompt-free `blockedAction`
  metadata and preserve an existing user-action block when a stale create call
  or stale queued/dispatching status update tries to reset the row. This keeps
  multiple browsers, mobile, and desktop handoff from disagreeing about whether
  a prompt can auto-dispatch.
- Billing web UI now exposes the two paid products directly in the billing
  surface: Relay Pro and Cloud Workspace. Checkout is web-only through
  `/billing/checkout`; free remains limited shared relay and has no checkout.
  Active subscribers can open the LemonSqueezy portal or call Yaver's
  `/billing/cancel` unsubscribe path from the same screen.
- Relay Pro users can upgrade to Cloud Workspace from the web Billing screen,
  but `/billing/yaver-cloud/change-plan` now syncs the LemonSqueezy subscription
  variant before flipping the local Convex plan or granting Cloud Workspace
  entitlements. If billing sync is missing or fails, the upgrade fails closed.
  After a Relay Pro upgrade succeeds, the route ensures the user's dedicated
  Cloud Workspace row so the subscription has compute capacity attached.
- The checkout route now rejects unknown `productId` values instead of silently
  normalizing arbitrary input to Relay Pro. Legacy cloud aliases still normalize
  to Cloud Workspace, and legacy relay labels still normalize to Relay Pro.
  Signed webhook handlers treat malformed product metadata as Relay Pro instead
  of accidentally activating Cloud Workspace.
- Mobile now has a focused placement-helper parity test for the same
  Cloud Workspace defer/confirm rules already covered on web.
- Cloud Workspace allowance is now metered as one shared monthly
  standard-credit pool instead of separate visible 8GB/16GB/32GB buckets.
  Standard workspaces cost 1 credit-hour per live hour, heavy workspaces cost
  2, and build workspaces cost 4. Backend balance/status APIs expose
  `includedStandardCredits`, `usedStandardCredits`, and
  `remainingStandardCredits` while keeping legacy seconds/hour fields for old
  clients.
- Web and mobile cloud controls now avoid provider/hourly copy in the paid
  product surfaces. The product remains flat monthly to the user; included
  allowance plus required auto-pause is the normal margin guardrail after the
  allowance is exhausted.
- Web Billing now removes the old prepaid credit cockpit from the normie-facing
  surface. It shows Free, Relay Pro, Cloud Workspace, included workspace
  allowance, LemonSqueezy portal/cancel paths, and managed relay/workspace
  resource controls. Backend wallet/ledger fields remain internal cost-control
  plumbing rather than an OpenRouter-style product model.
- The legacy public credit-pack routes now fail closed with `410`, and the
  legacy `/billing/yaver-cloud/provision` wallet-funded spin-up route no longer
  creates machines. New Yaver-managed compute must come from Cloud Workspace
  subscription/reconcile flows so a public checkout or copied API call cannot
  bypass the flat-plan cost guardrails.
- Account-level Cloud Workspace mutation routes now explicitly reject
  machine-scoped tokens, and the shared mutation guard refuses non-Yaver-managed
  rows or unsupported provider resources. This keeps a rooted workspace token,
  BYO/self-hosted row, or forged public API call from parking, waking, adopting,
  or decommissioning infrastructure outside Yaver-owned managed rows.
- Cloud placement and resize selection now share the same eligibility rule:
  only Yaver-managed, usable Hetzner rows (`active`, `paused`, `suspended`,
  `resuming`, `provisioning`, or `grace`) can be selected. Dead/deleting rows
  (`stopped`, `stopping`, `removed`, `error`), self-hosted rows, and unsupported
  providers are ignored even if they are newer or better-sized, so tasks do not
  bind to destroyed capacity or user-owned infrastructure.
- Explicit Cloud Workspace decommission now differs from Pause and subscription
  cancellation: the `/billing/yaver-cloud/dev-deprovision` route cancels linked
  billing, marks the row stopping, and schedules a full provider purge
  (server, persistent volume, and legacy snapshots). Pause remains
  data-preserving and wakeable; cancellation can stop server spend without
  pretending the user's workspace data has been exported.
- Task placement now treats `active` and `past_due` subscriptions consistently
  across products. A Cloud Workspace in payment-retry state keeps routing to the
  Cloud Workspace layer just like billing/status considers it subscribed, while
  cancelled/expired rows fall back to Free/manual decisions.
- Placement activation now uses the same Cloud Workspace subscription-status
  rule as preview/record: `active` and `past_due` can activate/wake; cancelled
  rows cannot. Web and mobile also treat `wake_failed` as a first-class
  activation blocker so a failed provider wake is shown honestly instead of
  leaving a vague pending placeholder.
- Desktop and QUIC handoff clients now preserve structured non-2xx activation
  blockers such as `runner_auth_required`, `yaver_auth_required`,
  `wake_failed`, and `resize_required` instead of flattening them into generic
  backend status strings.
- Pending Cloud Workspace placeholders now map dispatch-intent terminal states
  to task states: failed dispatch renders as a failed task, while cancelled or
  expired dispatch renders stopped. Blocked dispatch remains queued/actionable
  because it usually waits for sign-in, billing, or workspace assignment.
- Web and mobile local pending-dispatch queues now persist the server
  dispatch-intent `expiresAt` timestamp. If the server intent ages out, the
  client-held placeholder becomes `expired`/stopped locally instead of looking
  like an indefinitely queued Cloud Workspace task.
- Desktop's owner-local Cloud Workspace prompt spool now follows the same
  dispatch-intent expiry. Stale prompt-held rows normalize to `expired`, are
  not retried against a later workspace session, and report the terminal status
  back to Convex without uploading the prompt body.
- Managed Cloud Workspace auto-park is default-on across the running agent and
  backend `/machine/park-self` guard. `YAVER_CLOUD_IDLE_DISABLE=true` is the
  operator emergency brake; otherwise an idle managed box arms a grace window
  and then asks the backend to snapshot/delete so Hetzner compute billing stops.
  Customer-facing `/billing/yaver-cloud/auto-park` traffic can tune/repair
  auto-close but cannot disable it; this protects the flat-price margin from
  forgotten always-on machines.
- Cloud Workspace wake/start gating accepts remaining flat-plan included
  standard credits as payment for the next billable window, so a $29 subscriber
  is not forced into an add-credit flow while their included allowance remains.
  Once included credits cannot cover the window, Cloud Workspace compute is
  paused or blocked until the next period or an explicit billing setting changes.
- The Cloud Workspace meter now treats allowance/wallet exhaustion as an
  immediate park trigger for a live managed machine. If provider park cannot
  complete, the row is marked `suspended` with a loud error instead of silently
  leaving a Hetzner server running behind a suspended UI state.
- Public web/mobile copy no longer teaches Tailscale, Cloudflare Tunnel, or
  self-hosted relay setup as the normal path. Those routes can remain as
  compatibility internals, but user-facing setup should point to Yaver Relay,
  Relay Pro, or Cloud Workspace. The targeted public-doc scan covers
  `README.md`, `web/app/docs`, `web/app/manuals`, FAQ, landing, and
  integrations pages for the old setup phrases.
- Subscription recovery and activation now only reuse managed Cloud Workspace
  rows that are active, in-flight, parked, suspended, or in hosted grace. Dead
  or deleting rows such as `stopped`, `stopping`, `removed`, and `error` are not
  treated as usable capacity, so a paid activation cannot attach to a destroyed
  workspace record.
- Cloud placement labels in web/mobile product surfaces avoid RAM/provider
  vocabulary. Internally Yaver still chooses standard/heavy/build profiles, but
  the user sees product-level labels such as Cloud workspace, Heavy workspace,
  and Heavy build.

This is still not the final router. The current implementation tags tasks,
starts the cloud activation path for cloud lanes, performs first-pass
client-held handoff to the selected workspace, and keeps relay/owned-machine
tasks unchanged. Workspace resize and runner OAuth migration remain explicit
blocked/next phases rather than hidden best-effort behavior.

## Goal

Make Yaver choose and operate the right machine layer for every task:

```text
phone/mobile sandbox
-> relay/source runner
-> user's own Yaver machine
-> Yaver Cloud 8GB standard workspace
-> Yaver Cloud 16GB heavy workspace
-> Yaver Cloud 32GB build workspace
-> external deploy/serverless/artifact storage
```

The user should not choose RAM, provider server types, or wake mechanics. The
user should choose intent:

```text
Build an app.
Use my machine.
Use Yaver Cloud.
Share it with friends.
```

Yaver decides placement, wakes or resizes compute, verifies auth, meters credits,
and keeps the UI honest while the real machine becomes ready.

Lifecycle wording must stay precise:

- Pause/auto-park: preserve the workspace, delete only active server spend, and
  keep the row wakeable from volume/base image or legacy snapshot.
- Cancel plan: stop subscription billing and linked managed-resource spend; do
  not imply the user exported app data.
- Decommission/Delete box: irreversible resource purge. This is the only path
  that should delete the persistent workspace volume and legacy snapshots.

## Product Contract

Public products stay deliberately simple:

- Free: limited shared public relay, with stricter quotas and abuse controls.
- Relay Pro: private relay for the user's own machines, no Yaver compute.
- Cloud Workspace: Relay Pro plus a managed cloud dev box, persistent workspace
  volume, included standard-credit allowance, and required auto-pause on
  allowance exhaustion.

Do not expose compute-only, relay+compute+storage bundles, provider server
types, or OpenRouter-style usage pricing as the default normie experience.
Internally, placement may choose standard/heavy/build capacity, but the UI
should explain that as "normal app", "larger app", or "heavy build" work.

Subscriptions, checkout, portal, cancellation, and plan changes must stay on
the web UI. Mobile may show current subscription state and control already
active cloud workspaces, but it must not sell or upgrade packages inside the
app.

## Current Code Audit

### Backend / Convex

Relevant files:

- `backend/convex/cloudMachines.ts`
- `backend/convex/cloudLifecycle.ts`
- `backend/convex/plans.ts`
- `backend/convex/runnerUsage.ts`
- `backend/convex/schema.ts`
- `backend/convex/taskPackages.ts`

What exists:

- `cloudMachines` already tracks managed cloud machines.
- Schema already has `volumeId`, `volumeSizeGb`, `baseImageId`,
  lifecycle fields, provision phases, and machine status.
- `cloudLifecycle.ts` already has wallet, ledger, included allowance, metering,
  minimum reserve, start/stop, and usage deduction primitives.
- `plans.ts` grants included hours and managed gateway budgets for the current
  Relay Pro / Cloud Workspace product story while preserving legacy labels for
  old rows.
- Placement code recognizes new plan labels (`relay-pro`, `cloud-workspace`)
  and legacy labels (`managed-relay`, `cloud-agent`, `yaver-cloud*`) so current
  rows keep working during the billing migration.
- `runnerUsage.ts` records task durations per runner/device.
- `managedUsage` exists as a generic ledger for inference/backend/web/publish
  usage, separate from older `creditUsage`.

Gaps:

- `taskPlacements` now exists as a durable control-plane object that says where
  a task should run and why. Mobile and web task creation now record it, but
  desktop agent `/tasks` does not yet call it directly.
- Web and mobile task creation now refuse to dispatch a Cloud Workspace
  placement to the wrong currently-connected machine. If compute is still
  waking, or if the active connection is not the placement target, the surface
  records and activates the placement and keeps a local queued placeholder
  instead of sending the prompt to another machine.
- Web now has a browser-local pending dispatch queue for Cloud Workspace tasks.
  The prompt/body stay in `localStorage`, not Convex; the worker refreshes
  placement metadata and prompt-free dispatch intent status, connects to the
  assigned cloud device when it becomes reachable, then creates the real agent
  task on that device and removes the local pending row.
- `taskDispatchIntents` now stores durable, prompt-free dispatch metadata for
  cloud-bound local tasks: `localTaskId`, optional `placementId`, lane,
  target device id, cloud machine id, runner id, project slug, status,
  attempts, and TTL. Web creates/updates this record while keeping prompts,
  repo paths, command bodies, images, stdout, artifacts, and secrets only in
  the browser-held pending row until the real workspace is connected.
- `wakeRuns` now syncs wake/provision progress into linked non-terminal
  `taskDispatchIntents` rows. A queued placeholder can show phases like
  `creating`, `registering`, `awaiting-yaver-auth`, or `authorizing-runners`
  through the existing prompt-free reason/status fields while the prompt body
  remains only in the caller-held local queue. The sync skips rows already
  `dispatching`, `dispatched`, `cancelled`, or `expired`, so lifecycle progress
  cannot clobber a real task handoff.
- Mobile now mirrors that model with an AsyncStorage-backed pending dispatch
  queue and typed dispatch-intent client. The active task composer previews
  placement before remote-runner dispatch, queues Cloud Workspace tasks locally
  while compute wakes, merges prompt-free dispatch intent status into
  placeholders, keeps those placeholders visible across polling, selects the
  assigned cloud device when it is reachable, dispatches through that device's
  pooled client, and rebinds the placement to the real task id.
- `POST /tasks/placement/rebind` now lets Web/mobile replace a temporary
  `pending-cloud:*` task id with the real agent task id after local dispatch.
  This keeps placement history, wake runs, and task status tied to the actual
  runner task without ever uploading the prompt while the workspace wakes.
  The rebind mutation also patches wake runs indexed by the placement, so
  Cloud activity history does not stay attached to the temporary local id.
- `/tasks/placement/activate` now turns a recorded Cloud Workspace decision
  into `already_active`, `already_in_flight`, `wake_scheduled`,
  `provision_scheduled`, `reconcile_scheduled`, or `billing_required`.
- Placement responses now include a structured `creditEstimate` contract in
  addition to the legacy scalar `estimatedCreditCost`. The estimate is
  metadata-only and keeps the existing cloud wallet unit (`usd_cents`) for
  backward compatibility while also exposing standard-credit metadata:
  `standardCredits`, `includedStandardCreditsBucket`, and `creditWeight`.
  Cloud lanes are shown as included Cloud Workspace allowance first; Relay/
  owned-machine lanes show no Yaver compute charge.
- `wakeRuns` now exists as a durable metadata-only ledger for
  provision/wake/park attempts. Activation starts placement-linked runs, and
  lifecycle phase/timing mutations mirror progress/outcome into the ledger.
- Wake/park lifecycle writes provider metadata to `wakeRuns`: provider label,
  opaque provider resource id, opaque provider action id when Hetzner returns
  one, provider status, and `dryRun`. These fields are control-plane ids only;
  no IPs, hostnames, tokens, prompts, logs, or repo paths belong there.
- Dry-run wake/park is fail-closed. If `HCLOUD_TOKEN` is missing, or a lifecycle
  caller explicitly requests dry-run, Yaver must not mark a machine `active` or
  `paused`; it records a failed simulated wake/park instead. `active` remains a
  machine-bound proof state: the backend health check must hear from the agent,
  confirm it is usable and not auth-expired, then promote the row.
- Wake/placement activation now has a first-class auth-blocked state.
  `awaiting-yaver-auth` and `authorizing-runners` write `wakeRuns.status =
  blocked`, and `/tasks/placement/activate` returns `yaver_auth_required` or
  `runner_auth_required` instead of `already_active`/generic in-flight when a
  cloud workspace is awake but cannot honestly accept tasks yet.
- Claude/Codex subscription OAuth is now blocked for SDK/guest/scoped runner
  auth calls at the agent HTTP handler layer. Owner-authenticated web/mobile can
  still run browser auth or credential import on an owned or Cloud Workspace
  machine, but Relay/SDK/guest-scoped calls cannot start Claude/Codex auth,
  import Claude/Codex credentials, or run Claude/Codex setup that could create
  owner-level runner state.
- `projectProfiles` now has an authenticated HTTP upsert route
  (`POST /tasks/project-profile`), and Web/mobile task creation writes coarse
  project hints before preview/record. The stored data remains metadata-only:
  basename slug, stack label, counters/booleans, resource class, confidence,
  and source device id.
- `backend/convex/taskPlacementClassifier.ts` is now the pure backend
  classifier for task kind + coarse project metadata. It keeps small
  source/vibe work on Relay, routes native/Docker/medium-large projects to
  16GB, and reserves 32GB for build/deploy, huge repos, or large/native
  monorepos.
- Compute sizing is now estimate-aware across 8GB/16GB/32GB profiles, but the
  durable billing ledger remains `cloudLifecycle`/wallet, not task placement.
- `cloudMachines.ts` now has internal `standard`, `heavy`, and `build`
  machine profiles in addition to legacy `cpu` and `gpu`. New placement-driven
  Cloud Workspace activation provisions `standard/heavy/build`; legacy `cpu`
  remains CPX51-shaped so existing snapshot-backed rows do not wake onto a
  smaller disk by accident.
- New managed Cloud Workspace provisioning on the container image path creates
  a Hetzner data Volume before server creation, attaches it on first boot,
  mounts it at `/srv/yaver/state` before writing OAuth/repo/cache state, and
  records `volumeId + baseImageId` for volume-backed wake. Legacy rows without
  a volume keep the snapshot-first fallback.
- Cloud lifecycle metering now tracks `standard/heavy/build` buckets separately
  and plan activation grants smaller included-hour buckets for heavy/build work.
- Legacy compatibility code still accepts old managed-cloud plan labels, but
  public web/MCP copy now advertises only Relay Pro and Cloud Workspace.
- Provider progress is now first-class enough for recent wake/provision history
  through `/cloud/wake-runs/recent`; provider action ids are recorded when the
  provider returns one.
- Health promotion needs stronger machine-bound proof before readiness/billing
  promotion.

### Desktop Agent

Relevant files:

- `desktop/agent/tasks.go`
- `desktop/agent/httpserver.go`
- `desktop/agent/console_machines.go`
- `desktop/agent/agent_mesh.go`
- `desktop/agent/agent_mesh_remote.go`
- `desktop/agent/mcp_remote_proxy.go`
- `desktop/agent/autorun_placement.go`
- `desktop/agent/ops_cloud.go`
- `desktop/agent/cloud_stopstart.go`
- `desktop/agent/cloud_capacity.go`
- `desktop/agent/runner_auth.go`
- `desktop/agent/runner_auth_mirror.go`
- `desktop/agent/mcp_tools.go`
- `desktop/agent/Dockerfile.yaver-cloud`

What exists:

- `TaskManager.CreateTaskWithOptions` runs Claude/Codex/OpenCode locally with
  workdir, runner, model, mode, guest isolation, video, and source metadata.
- `/tasks` is the core task creation endpoint.
- `listAllMachines` and `MachineCapabilities` already expose hardware, runner
  readiness, Docker, Android/iOS, Play/TestFlight, and machine profile tags.
- `agent_mesh.go` already scores machines by readiness, runner, hardware,
  platform, affinity, and cost lane.
- `autorun_placement.go` answers build/deploy eligibility per target.
- `mcp_remote_proxy.go` already forwards MCP calls to another owned device and
  refuses local-only secret tools.
- `runner_auth.go` distinguishes `AuthConfigured` from `AuthVerified`.
- `runner_auth_mirror.go` can copy runner credentials between owned machines.
- `ops_cloud.go` exposes managed `cloud_wake` and `cloud_park`.
- `cloud_stopstart.go` has snapshot+delete / recreate-from-snapshot primitives,
  but the desired v1 path should prefer persistent volumes and golden images.

Gaps:

- Desktop task creation now records placement, blocks wrong-machine Cloud
  Workspace execution before runner launch, creates a prompt-free pending
  placement id, and attempts activation. HTTP terminal and `yaver code` have
  first-pass automatic handoff, and classic/TUI attach can retarget active task
  polling after handoff. Legacy QUIC terminal task creation now follows the
  same caller-held handoff model. Desktop terminal handoff now also has a local
  restart-safe spool/retry path and an on-demand prompt-free `cloud` status
  command. The bounded-wait loop emits prompt-free live progress to the
  terminal, and Web/mobile pending-task panels now merge prompt-free dispatch
  intent/wake progress into their local placeholders while keeping prompts only
  in local storage.
- Mobile/web cannot submit "run this on whichever layer is best" as a first
  class task. They mostly submit to a selected connected agent.
- Relay/source-runner vs cloud-build-machine boundaries are encoded in backend
  placement rows and enforced at desktop `/tasks` for cloud lanes. Relay/source
  execution is still first-pass; it does not yet perform Git-provider branch
  edits while a build workspace wakes.
- Auth mirroring exists, but the cloud plan must restrict runner OAuth to the
  user's persistent workspace volume, never shared relay.
- Machine size changes are only partially modeled. New placements can pick a
  different internal profile when provisioning a fresh workspace. Activation
  now refuses to wake an underpowered existing workspace for heavy/build
  placements. If the existing workspace has a persistent volume-backed recovery
  source, activation records `resize-required` on that machine, attaches the
  placement to it, and blocks the prompt-free dispatch intent until the provider
  worker recreates the stateless box on the larger profile. The first provider
  worker is now wired: it deletes the current stateless server if present,
  detaches the persisted volume if needed, recreates from the same base image on
  the target profile, repoints DNS, and lets the existing resume health check
  prove the agent before marking the workspace active.
- Web and mobile now ask for confirmation before heavy/build Cloud Workspace
  placements. The copy uses product-level terms such as Heavy Workspace/Heavy
  Build and included allowance, not provider SKUs or hourly rates.

### Mobile

Relevant files:

- `mobile/app/(tabs)/tasks.tsx`
- `mobile/app/phone-projects.tsx`
- `mobile/app/cloud-onboarding.tsx`
- `mobile/src/lib/quic.ts`
- `mobile/src/lib/boxInit.ts`
- `mobile/src/lib/hcloud.ts`
- `mobile/src/lib/cloudPreview.ts`

What exists:

- Task composer sends to the selected connected agent through `sendTask`.
- There is a local Yaver-Agent fallback for control-plane prompts when no host
  is connected.
- `phone-projects.tsx` already has choices for this phone, connected machine,
  other box, and Yaver Cloud.
- `boxInit.ts` already normalizes "can this box code?" across runner auth,
  agent online state, and Git provider readiness.
- `hcloud.ts` is a phone-direct BYO Hetzner client with plan-to-server-type
  mapping and snapshot/delete helpers.

Gaps:

- Mobile task creation now calls placement preview/record/activate, but it still
  submits the actual runnable task to the selected connected agent. The missing
  piece is task handoff/continuation when relay-source work should transfer to
  the woken Cloud Workspace.
- No user-facing distinction between cheap relay/source progress and real
  workspace/build progress.
- No shared "Large Project detected" and "heavy credits" UX in the task flow.
- Mobile still has several independent wake/connection ladders per the managed
  cloud wake audit.
- iOS purchase policy keeps subscription checkout/cancel on web, not in-app.

### Existing Planning Docs To Preserve

- `docs/handoff/managed-cloud-wake-broken.md`: P0 wake-run model and honest
  wake UX findings.
- `docs/yaver-cloud-credits-design.md`: legacy prepaid credit pack and ledger
  design. Treat the public top-up parts as superseded by the flat-plan rule;
  keep the ledger concepts only for internal cost accounting.
- `docs/architecture/REMOTE_WORKER.md`: MCP `device_id`, proxy, and
  remote-worker layers.
- `docs/planning/relay-cloud-workspace-business-model-2026-07-18.md`: product,
  margin, relay/compute split, large-repo ceiling, and OAuth policy.

## Target Architecture

### Public Product Model

Only two paid products:

```text
Relay Pro          $9/mo
Cloud Workspace   $29/mo
```

Cloud Workspace includes Relay Pro plus a persistent cloud workspace, artifact
storage, fast wake, and generous monthly workspace use. Exhausted allowance
pauses compute instead of creating a hidden postpaid bill.

No public "8GB / 16GB / 32GB" picker.

### Pricing UX: Flat Monthly Only

Yaver targets normies, not infrastructure developers. Therefore the primary
pricing UX should be flat monthly, not OpenRouter-style metered developer
pricing.

OpenRouter-style balance is useful internally for margin accounting, but it
should not be exposed as the normal product.

Recommended public model:

```text
Relay Pro
  $9/mo
  use your own machines from anywhere

Cloud Workspace
  $29/mo
  cloud dev workspace, relay, storage, deploy previews, and generous monthly use
```

Normie-facing copy:

```text
Most users never think about machine size or hourly compute.
Yaver includes enough monthly workspace use for building and sharing apps.
Heavy native builds or very large projects may use the monthly allowance faster.
```

Avoid this as primary UX:

```text
Add $10 balance.
8GB costs $0.06/hr.
16GB costs $0.12/hr.
32GB costs $0.20/hr.
```

That is developer/IaaS language and will reduce conversion.

Use this instead:

```text
Included this month:
  app building
  deploy previews
  artifact sharing
  standard workspace use

May pause compute sooner:
  Android native build loops
  very large imported repos
  long-running all-day workspaces
```

Internally, still meter everything. The flat plan needs a hidden cost-control
system to preserve margin:

```text
Public:     flat monthly plan
Internal:   credits, compute profiles, quotas, auto-park, pause-on-exhaustion
```

Suggested wording:

```text
Heavy Build       better than "32GB"
Large Project     better than "monorepo surcharge"
Workspace use     better than "compute hours"
```

The wallet/ledger should exist mostly as internal accounting:

- no postpaid billing
- no surprise charges
- no normal add-credit cockpit
- not required to understand Yaver on day one

Flat monthly is better for acquisition. Metering is better for margin. The
architecture needs both, but the UI should lead with flat monthly.

### Two-Tier Flat-Price Entitlement Architecture

The sellable product has two paid tiers. Free OSS remains a distribution path,
not a SaaS tier.

| Tier | Price | What User Thinks They Bought | What Backend Grants |
|---|---:|---|---|
| Relay Pro | $9/mo | "My own machines work from anywhere." | private relay entitlement, higher device/project limits, guest feedback routing |
| Cloud Workspace | $29/mo | "Yaver gives me the machine too." | Relay Pro + cloud workspace entitlement, persistent volume, artifacts, fair-use compute pool, required auto-pause |

Free shared relay still exists, but it is a limited acquisition path, not a
third paid tier:

```text
Free OSS + shared relay:
  install Yaver, connect personal devices, light usage, fair limits

Relay Pro:
  private managed relay, higher limits, daily-use reliability

Cloud Workspace:
  Relay Pro plus Yaver-operated compute/storage/build/deploy workspace
```

Public docs should not teach third-party free networking as the recommended
path. Yaver can remain compatible with existing private networks and external
tunnels, but normie-facing onboarding should lead with:

```text
same WiFi -> direct LAN
different networks -> Yaver free relay
daily remote work -> Relay Pro
want Yaver to provide the machine too -> Cloud Workspace
```

Backend should model this as subscription entitlements first:

```text
subscriptionPlan:
  relay_pro
  cloud_workspace

entitlements:
  relay.private = true
  cloud.workspace = true/false
  cloud.workspaceStorageGb = 100
  cloud.artifactStorageGb = bundled allowance
  cloud.fairUsePool = internal monthly pool
  cloud.maxActiveWorkspaces = 1
  cloud.autoPark = required by default
  cloud.autoPauseOnAllowanceExhaustion = true
```

The internal fair-use pool should not be marketed as a wallet. It exists so the
scheduler can protect margin:

```text
normal user:
  never sees credits

approaching limit:
  sees "Workspace use is high this month"

expensive action:
  sees "This Heavy Build uses workspace allowance faster"

limit exhausted:
  relay/source work continues
  cloud compute pauses until the next period or billing settings change
```

Subscription is the primary source of truth for access. Internal ledgers prove
margin and enforce fairness:

```text
subscriptions:
  who has Relay Pro or Cloud Workspace

internal usage ledger:
  what compute/storage/artifact work actually cost

internal wallet/ledger:
  tracks included allowance, operator adjustments, and cost-control decisions
```

Do not build the normal Cloud Workspace UI around:

```text
balanceCents
hourlyRate
providerCost
machine type
```

Those can exist in diagnostics, admin views, invoices, or power-user billing
details, but they should not drive the first-run normie experience.

### Internal Execution Lanes

| Lane | Purpose | User Copy | Compute |
|---|---|---|---|
| `phone_sandbox` | local mobile/Yaver-native sandbox work | Phone sandbox | phone |
| `relay_source` | planning, Git metadata, source-only branch edits | Light work | shared relay/source runner |
| `owned_machine` | user's paired Mac/Linux/Pi/VPS | Your machine | existing device |
| `cloud_standard` | Yaver-native serverless/web/Expo starter | Standard workspace | 8GB |
| `cloud_heavy` | imported repo, larger installs, multi-server dev | Heavy workspace | 16GB |
| `cloud_build` | Android/native/Docker/large-monorepo broad checks | Build workspace | 32GB |
| `external_deploy` | serverless/object/app-store deploy output | Deploy target | Cloudflare/Convex/Supabase/R2/etc |

### Task Flow

```text
1. User submits prompt from mobile/web/MCP.
2. Task router creates a durable prompt-free dispatch intent.
3. Router classifies project + task.
4. Router estimates required lane and credit impact.
5. If relay/source can start now, it starts immediately with honest context.
6. If cloud compute is required, create or reuse a wakeRun.
7. Compute mounts the user's persistent volume and verifies runner auth.
8. Handoff occurs through Git/task branch/artifact queue, not process migration.
9. Build/deploy/artifact output is stored outside the VM.
10. Compute parks after idle; task remains resumable.
```

### Repo Classification

Classify every project before heavy work:

```text
Yaver-native small:
  Yaver Git repo, controlled template, serverless backend, low file count

Imported medium:
  normal GitHub/GitLab app, one package/app, no native/Docker pressure

Large Project:
  monorepo, many workspaces, android/ios/native folders, docker-compose,
  lockfile pressure, previous OOM/timeout

Large-monorepo ceiling:
  about 15GB / 235k files / many packages and deploy surfaces
```

Default behavior:

- Yaver-native projects start on `cloud_standard`.
- Imported projects start focused on one selected app/package.
- Large Project triggers included-allowance warning and may use `cloud_heavy`.
- Native Android/Hermes/APK/Redroid/Docker-heavy tasks use `cloud_build`.
- Never run the whole monorepo unless the user explicitly asks.

### Internal Credit Model

Use hidden/internal credits for cost control. Do not make the credit wallet the
main buying experience.

Suggested $29 Cloud Workspace mapping:

```text
included fair-use pool:
  120 standard compute credits/month internally

relay_source:      near-free / tiny rate
cloud_standard:   1 credit/hour
cloud_heavy:      2 credits/hour
cloud_build:      4 credits/hour
```

Default UI should show:

```text
Workspace use: Healthy
Heavy builds: within monthly allowance
```

When the user starts expensive work, UI can show:

```text
This is a Heavy Build. It uses workspace allowance faster than normal app work.
```

Do not show:

```text
cpx31 vs cpx51 vs ccx...
8GB costs $0.06/hr...
```

### Wake And Resize Model

Correct storage model:

```text
one encrypted persistent user volume
many stateless golden-image machine profiles
periodic backup snapshots only
```

Do not create separate user snapshots per size.

Resize flow:

```text
8GB VM running
-> task needs heavy/build
-> checkpoint/stop runner
-> unmount/detach volume
-> delete VM
-> create 16GB/32GB VM from golden image
-> attach same volume
-> same uid/home path
-> verify runner auth
-> resume task
```

Wake SLO:

```text
P50: 30-45s
P95 target: under 60-90s
2+ minutes: degraded/cold repair path
```

### OAuth / Secret Boundary

Rules:

```text
Yaver auth:
  relay + workspace

Git scoped auth:
  relay allowed only with repo/branch-scoped app tokens or Yaver Git grants

Claude/Codex/OpenCode runner auth:
  persistent workspace volume only

project secrets / deploy credentials:
  workspace/vault only
```

Relay must not store Claude/Codex OAuth. If relay performs source-only work, it
uses one of:

- Yaver-managed lightweight model for non-secret source planning.
- Yaver Git metadata and cached project summaries.
- GitHub/GitLab App installation token with narrow repo/branch scope.
- User BYOK only if relay becomes a true per-user isolated runner host later.

### Serverless And Artifact Path

Happy path for normies:

```text
Yaver mobile app sandbox
-> Yaver Git repo
-> Yaver serverless template/runtime
-> Yaver deploy/preview artifact
-> friends test
-> Feedback SDK creates task/issue/branch
```

Cloud Workspace is the dev/build/deploy machine, not the production host.

Store outside VM:

- Hermes bundles
- APK/AAB
- web preview bundles
- build logs
- screenshots
- release artifacts
- rollback packages
- Feedback SDK attachments

## Coding Plan

### Phase 0 - Freeze Contracts Before Code

Deliverables:

- `TaskPlacementRequest`
- `TaskPlacementDecision`
- `ProjectClassification`
- `ComputeProfile`
- `WakeRun`
- `CreditEstimate`

Planned files:

- New doc/spec only first.
- Later backend schema additions in `backend/convex/schema.ts`.
- Later pure desktop classifier in `desktop/agent/task_placement.go`.
- Later pure mobile display model in `mobile/src/lib/taskPlacement.ts`.

Acceptance:

- Contract contains no prompts, file contents, stdout, absolute paths, secrets,
  or token plaintext in Convex-visible fields.
- Privacy tests explicitly allow only ids, counters, labels, timestamps, and
  coarse project metadata.

### Phase 1 - Durable Wake Run

Implement after this plan is approved.

Backend tasks:

- Add `machineWakeRuns` table.
- Add `latestWakeRunId` to `cloudMachines`.
- Add append-only wake events.
- Capture provider action ids and provider progress.
- Represent blocked states explicitly:
  - `blocked_yaver_auth`
  - `blocked_runner_auth`
  - `blocked_capacity`
  - `blocked_balance`
  - `blocked_provider`
- Stop setting `active` during dry-run/fail-closed paths.
- Require owned-agent proof before promoting a wake to `ready`.

Desktop tasks:

- Add health proof endpoint or signed health challenge.
- Ensure cloud boot sends machine-token phase beacons.
- Keep `/health` public for recovery but do not let unauthenticated public
  health promote readiness.

Mobile/web tasks:

- Replace client-local wake ladders with wake-run state.
- Show primary CTA when blocked on sign-in.
- Show hold deadline while compute is kept alive for auth recovery.

Tests:

- Backend wake-run state machine tests.
- Provider action failure test.
- Dry-run does not mark active.
- Wrong public health endpoint cannot promote ready.
- Mobile/web phase parity tests.

### Phase 2 - Project Classifier

Desktop pure classifier:

- Count files with caps/timeouts.
- Measure repo disk size.
- Detect workspaces:
  - pnpm/npm/yarn/bun workspaces
  - Go modules
  - Docker Compose
  - Android/iOS/native folders
  - Expo/RN/Flutter
  - Next/Vite
- Detect Yaver-native project metadata.
- Detect selected app/package.
- Persist a non-secret summary to local `.yaver/project-profile.json`.
- Send only safe summary fields to Convex if needed.

Classification output:

```text
small_yaver_native
small_imported
medium_imported
large_project
large_monorepo
native_build_required
```

Tests:

- Fixture for tiny Yaver-native serverless project => standard.
- Fixture for Expo app => standard unless native build requested.
- Fixture with android + docker-compose => build.
- Synthetic large-monorepo summary => large/build.
- Classifier times out safely and returns unknown, not heavy by default.

### Phase 3 - Task Placement Router

Backend/desktop split:

- Backend owns subscription, wallet, wake runs, managed cloud rows.
- Desktop owns local capabilities and actual runner execution.
- A new router composes both through explicit decisions.

Decision inputs:

- task source: mobile, mobile-code, feedback, MCP, autorun, deploy, voice
- requested project/app
- selected user preference: this phone, my machine, Yaver Cloud, auto
- runner preference and auth readiness
- project classification
- machine inventory
- wake state
- wallet/credits
- expected heavy phases

Decision output:

```json
{
  "lane": "relay_source|owned_machine|cloud_standard|cloud_heavy|cloud_build",
  "machineId": "...",
  "computeProfile": "standard|heavy|build",
  "needsWake": true,
  "needsResize": false,
  "needsAuth": [],
  "creditEstimate": {},
  "honestMessage": "...",
  "handoffMode": "direct|git_branch|artifact_queue"
}
```

Desktop tasks:

- Add pure `DecideTaskPlacement`.
- Integrate with `agent_mesh.go` scoring instead of duplicating scoring.
- Integrate build/deploy eligibility from `autorun_placement.go`.
- Add refusal/ask-before-heavy behavior when credits are low.

Mobile tasks:

- Add "Auto / Yaver decides" task target.
- On no connected host, submit to control-plane cloud intent instead of only
  local Yaver-Agent fallback.
- Show "Workspace waking; I can plan now, live repo when ready" when the relay
  lacks fresh repo context.

Tests:

- No connected machine + Cloud Workspace entitlement => creates cloud intent.
- Connected ready machine => uses owned machine.
- Large project + Android build => build profile.
- Low credits => relay/source allowed, heavy build queued/blocked.
- Guest feedback => feedback-only lane, no secrets/build by default.

### Phase 4 - Cloud Profiles And Persistent Volume Resize

Backend tasks:

- Implemented: replace single CPX51 default for new placement-driven work with
  internal profiles:
  - `standard` 8GB
  - `heavy` 16GB
  - `build` 32GB
- Still open: store explicit `currentProfile`, `desiredProfile`, `profileRate`,
  `profileCreditRate` fields instead of deriving most of this from
  `machineType`, `specs`, and the billing meter.
- Store provider `serverType` per created machine.
- Keep `volumeId` as the user state anchor.
- Prefer golden image + volume attach over full snapshot restore.
- Use snapshot restore only as legacy fallback or backup recovery.

Desktop/agent tasks:

- Ensure same uid/gid for `/home/yaver` and `/workspace`.
- Verify runner auth after attach.
- Check Codex writable root after attach.
- Repair ownership safely when volume was mounted by wrong uid.
- Prewarm common caches in golden image only; store user caches on volume.

Tests:

- Resize preserves same volume id.
- Resize does not require runner re-auth when credentials are valid.
- Wrong uid produces actionable repair state, not silent runner failure.
- Snapshot fallback is not used on normal wake when volume exists.

### Phase 5 - Relay Source Runner

Scope for v1:

- Yaver Git native repos.
- Imported GitHub/GitLab with App installation token.
- Source-only branch edits.
- No user Claude/Codex OAuth on shared relay.
- No `.env`, SSH keys, package registry tokens, deploy credentials, or vault.

Allowed relay tasks:

- understand task intent
- ask clarifying questions
- read cached project summary
- create branch
- edit low-risk source files
- commit checkpoint
- open/update Yaver Git task
- open draft PR/issue
- wait for compute result

Forbidden relay tasks:

- run private dependency installs
- run native builds
- run Docker Compose with secrets
- deploy
- read/write vault
- use user Claude/Codex OAuth

Handoff:

```text
relay branch commit
-> compute wakes
-> compute pulls branch
-> compute verifies repo/auth/secrets
-> build/test/deploy
-> compute pushes result/artifacts
```

Current implementation:

- `relaySourceIntents` is now the prompt-free control-plane queue for relay
  source work. It validates active project-share ownership/membership, denies
  viewer-created source work, requires `relay_source` placement when a placement
  is linked, generates only `yaver/*` branch refs, and stores no prompts, diffs,
  file paths, stdout, tokens, vault references, or runner OAuth.
- Project owners can claim and update relay-source intents for their own shares
  via `/tasks/relay-source-intents/claim`, so an owner relay can prepare work
  created by a collaborator without needing the collaborator's auth token.
- Web/mobile have thin create/update/list clients for those intents.
- Desktop managed-git now has a first-pass relay-source prepare primitive:
  `/managed-git/relay-source/prepare` claims the intent, creates/pushes the
  scoped `yaver/*` branch from a temporary clone of the Yaver-managed bare repo
  without touching the user's working tree, then marks the intent
  `handoff_ready`. It still does not run Claude/Codex, touch vault, install
  dependencies, deploy, or edit files.
- Desktop managed-git also has a bounded source-only patch primitive:
  `/managed-git/relay-source/apply` accepts explicit small text file updates,
  rejects secret/native/build-output paths such as `.env`, `.git`, `.yaver`,
  `node_modules`, `ios`, `android`, and lockfiles, commits in a temporary clone,
  pushes only the scoped `yaver/*` branch, and marks the intent
  `committed`/`handoff_ready`.
- Desktop managed-git now exposes `/managed-git/relay-source/plan`, a local
  authenticated planner contract that checks whether a prompt + optional
  explicit file patches are relay-safe. It returns `apply_patch`,
  `prepare_only`, or `compute_required`, normalizes the scoped branch, and
  explains the decision without writing prompt/diff/file-path data to Convex.
  Web and mobile have thin client wrappers for this endpoint.
- Desktop managed-git also exposes `/managed-git/relay-source/work-once`, the
  first worker-loop primitive. With client-held context, it claims or accepts a
  relay-source intent, calls the planner, applies explicit safe patches when
  allowed, or prepares only a scoped branch. With only prompt-free queued
  metadata, it is honest and does branch prep only for compute handoff.
- The planner now has a bounded safe source-edit generator for owner-held
  prompts: fenced blocks of the form ```` ```yaver-file path/to/file.md ````.
  Generated patches still go through the same path, extension, size, native-dir,
  lockfile, and secret-content guards before `work-once` can commit them. It
  does not infer arbitrary code edits from natural language and does not use
  Claude/Codex/OAuth on a shared relay.
- Relay-source apply now has a first-pass provider mirror push: after committing
  a scoped `yaver/*` branch to the managed bare repo, the owner relay can push
  that same branch to connected GitHub/GitLab mirrors with the owner's local
  provider token. It never pushes relay-source work to `main`, redacts tokens
  from failure logs, and remains best-effort so local managed-git state is not
  blocked by provider availability. True GitHub/GitLab App installation-token
  scoped edits remain a later backend/app integration.
- Relay-source intents now carry a non-secret provider branch contract:
  provider kind, host, repo label, `yaver/*` branch, branch URL, app
  installation id label, auth mode, and auth status. Convex derives
  GitHub/GitLab branch targets from the project-share repo URL and marks app
  auth `required` by default. If the desktop relay mirrors with the owner's
  local provider token, it records `owner_local_token` /
  `owner_token_fallback` so product and worker code do not confuse that with a
  real GitHub/GitLab App installation token.
- Backend now has a first-pass GitHub App installation-token broker for
  relay-source intents: owner-only, GitHub.com only, env-configured with
  `YAVER_GITHUB_APP_ID` / `YAVER_GITHUB_APP_PRIVATE_KEY`, resolves the app
  installation for the target repo, requests a one-hour installation token
  narrowed to that repository and `contents:write`, records only non-secret
  app/auth metadata on the intent, and returns the short-lived token only to
  the authenticated owner relay. It does not store the token in Convex.
- Desktop relay-source `work-once` now uses that GitHub App broker after a
  successful local source-only commit: it asks for the one-hour installation
  token, pushes only `refs/heads/yaver/*:refs/heads/yaver/*` from the managed
  bare repo, redacts the token from git failure output, and records
  `app_installation` / `available` provider metadata when the push succeeds.
  If the broker is unconfigured, unavailable, or the provider push fails, the
  local managed-git branch and owner-local token fallback remain the handoff.
- GitLab scoped provider auth is intentionally explicit rather than fake:
  `/tasks/relay-source-intents/gitlab-token` is owner-only, validates the
  GitLab relay-source target, records non-secret `providerAuthStatus:
  unsupported`, and returns 501. GitLab's write-capable options are user OAuth,
  PAT, project/group access token, deploy token, or CI job token; Yaver should
  not mint/store those in Convex as a hidden backend secret. The supported
  GitLab path today is owner-local provider token fallback on the relay machine.
- The web Feedback page now includes a compact owner-facing relay branch
  handoff panel. It lists recent owner relay-source intents with project,
  `yaver/*` branch, provider repo/branch link when available, and auth label
  (`GitHub App`, `Owner token fallback`, `Provider app token required`, or
  `Provider app token unsupported`) without exposing tokens, paths, prompts, or
  diffs.
- The agent now has an opt-in background relay-source worker scheduler. It is
  off by default, can be enabled with local config or
  `YAVER_RELAY_SOURCE_WORKER=1`, polls queued owner-scoped relay-source intents,
  and prepares scoped branches from prompt-free metadata. It does not infer
  edits because it has no local prompt/file context.
- The current relay layer deliberately does not run Claude/Codex, touch vault,
  install dependencies, deploy, or infer arbitrary edits by itself on shared
  relay.

Tests:

- Relay cannot access vault endpoints.
- Relay token scope cannot push protected branch.
- Relay can create a draft branch in Yaver Git.
- Compute can resume from relay-created branch.
- If relay context is stale, UI copy says so.

### Phase 6 - Credits, Top-Ups, And Margin Controls

Backend tasks:

- Move from "included hours per machineType" to "standard credits" naming.
- Keep integer cents ledger as billing source of truth.
- Add `creditUnitUsage` or adapt `managedUsage` for compute profile credits.
- Use cost/0.60 pricing rule for 40% minimum margin.
- Make profile credit rates env-configurable.
- Add dry-run/live flags per meter.
- Add daily fair-use and per-task estimate gates.

Product defaults:

```text
Cloud Workspace $29:
  120 standard credits/month
  100GB persistent workspace
  artifact storage bundle
  prepaid overage
  no postpaid
  managed AI not included in base
```

Tests:

- 8GB 120h case stays above target margin.
- 12h/day wall-clock with only 4h/day compute stays profitable.
- 12h/day active build-profile work exhausts allowance and parks/blocks compute.
- Exhausted allowance cannot start heavy phase until reset or billing changes.
- Current atomic step checkpoints instead of abrupt kill on quota exhaustion.

### Phase 7 - Yaver-Native Serverless And Artifacts

Backend/storage tasks:

- Implemented first-pass artifact metadata model:
  - artifact id
  - project/share id
  - kind
  - size
  - storage provider
  - object key or external URL
  - public-link token and share URL expiry
  - owner
  - task/local task linkage
- HTTP routes now exist for signed-in project members:
  - `POST /project-artifacts/upload-url`
  - `POST /project-artifacts`
  - `GET /project-artifacts`
  - `GET /project-artifacts/usage`
  - `POST /project-artifacts/cleanup`
  - `POST /project-artifacts/hide`
  - `GET /project-artifacts/public?token=...`
- Web/mobile have thin client helpers for artifact create/list/usage/cleanup/
  hide/public lookup.
- Web dashboard now has a Project Artifacts surface where signed-in project
  users can list usage, save external HTTPS artifact links, upload Convex-backed
  files, copy Yaver public artifact links, hide stale outputs, and trigger
  expired-artifact cleanup.
- Public artifact links now resolve through `/artifacts/:token`, so a normie's
  friends can open Yaver-hosted artifact metadata and then launch/download the
  attached APK, Hermes bundle, preview, or external output without signing in.
- Artifact records can now point at Convex storage via `storageId`; the upload
  URL route mints project-scoped upload URLs for owners/devs/normies.
- Yaver-held artifact storage is gated to active Cloud Workspace subscriptions
  or env-configured owner-preview accounts. Free and Relay Pro users can still
  save external HTTPS artifact links because those do not store bytes on Yaver,
  but they cannot mint Convex upload URLs or attach Convex `storageId` artifacts.
- The web Project Artifacts dashboard mirrors that contract: it fetches
  `/subscription`, leaves external-link saving available, disables file upload
  controls unless the active plan is Cloud Workspace, and shows Cloud Workspace
  copy instead of letting free/Relay Pro users hit a backend 403.
- The same dashboard now displays quota pressure from both stored bytes and
  pending upload reservations, so a started-but-not-yet-attached upload is
  visible as metered/reserved instead of disappearing from the UI while still
  counting against backend quota.
- Mobile has no Yaver artifact upload UI. Its low-level project artifact helper
  still allows external HTTPS artifact records, but refuses `convex` /
  `yaver-storage`, `storageId`, or `uploadIntentId` usage unless the caller
  explicitly marks the operation as confirmed Cloud Workspace storage. That
  keeps accidental mobile storage affordances out of the free/Relay Pro path.
- Convex-backed artifact uploads now require the client to declare a positive
  `sizeBytes` before an upload URL is minted. Minting creates a pending
  `projectArtifactUploadIntents` reservation that counts against owner/project
  quota until artifact metadata consumes it. Metadata creation must present the
  reservation id, and the reservation is validated against uploader, owner,
  share, slug, and Convex's server-side `storage.getMetadata(storageId).size`
  before the `storageId` is attached. The stored artifact records Convex's
  actual byte size, not the client-declared size. This bounds abandoned upload
  URLs and oversized upload attempts to the user's storage allowance instead of
  allowing an unmetered cost leak.
- Feedback work items now validate attached artifact ids against the same
  active project share before queueing. Cross-project, hidden, expired, and
  wrong-owner artifacts are rejected; private artifacts can only be attached by
  the project owner or the artifact uploader.
- Public artifact reads and access-touch now share the same visibility rule:
  active artifact, public link enabled, unexpired public URL, and unexpired
  artifact row. Expired or hidden links do not refresh `lastAccessedAt`.
- Owner-level storage quota is enforced for Yaver-held artifact bytes, external
  HTTPS links do not burn bundled storage, and owner-only cleanup can expire old
  rows and delete Convex storage objects.
- Still open: richer signed download URL policy, automated scheduled retention
  jobs, and provider-level deletion for already-uploaded blobs that never
  receive a metadata row. The current guardrail is quota bounding, not magical
  discovery of unknown storage ids.

Yaver-native project tasks:

- Ensure project creation defaults to Yaver Git + serverless template.
- Treat Convex/Supabase/Cloudflare as deploy targets, not VM services.
- Keep mobile preview paths first-class:
  - Hermes bundle share
  - web preview URL
  - APK artifact

Feedback SDK tasks:

- Guest feedback-only mode by default.
- Feedback creates a bounded owner-reviewed queue item via
  `POST /feedback-work-items` using a feedback-scoped SDK token.
- SDK-created feedback is queue-capped before insert: defaults are 200 pending
  queued/claimed items per owner and 80 per project, with env overrides
  `YAVER_FEEDBACK_OWNER_PENDING_LIMIT` and
  `YAVER_FEEDBACK_PROJECT_PENDING_LIMIT`. Expired and terminal rows do not count.
  Setting a limit to `0` explicitly disables that cap for operator-controlled
  deployments.
- Owner can turn feedback into:
  - Yaver task
  - Yaver Git issue
  - GitHub/GitLab issue
  - draft branch/PR
- Owner APIs now exist for queue review/worker handoff:
  - `GET /feedback-work-items`
  - `POST /feedback-work-items/claim`
  - `POST /feedback-work-items/status`
  - `POST /feedback-work-items/route`
  - `POST /feedback-work-items/queue-relay-source`
- Feedback-originated relay-source work now derives the same safe provider
  target metadata as normal relay-source work: GitHub/GitLab host, repo,
  `yaver/*` branch, branch URL, and `providerAuthStatus="required"`. URLs with
  embedded credentials are ignored, and feedback body text still stays out of
  the relay-source ledger.
- Feedback-to-relay handoff now reuses an existing relay-source intent only
  when the linked row still matches the same owner, project share, and
  `feedback:<itemId>` local task id, and is still in a reusable state
  (`queued`, `claimed`, `committed`, or `handoff_ready`). Failed/cancelled/
  expired linked intents flow back through the normal requeue path, while
  foreign local-task collisions are rejected.
- Web dashboard now has an owner-facing Feedback Work tab that lists feedback
  queue items, filters by status, rejects stale reports, queues branch-scoped
  relay-source work, and retargets items back to `queued` for owner-machine task
  or private issue-draft workers. The browser does not write provider issues and
  does not receive local draft paths.
- Connected web dashboards can read/write the owner machine's local
  `/feedback-work/config` endpoint to enable the feedback worker and explicitly
  toggle provider issue posting. This is local agent config, not Convex state;
  provider tokens remain in the owner machine's git provider store. Desktop
  tests now pin the route registration behind owner `s.auth`, and
  `/feedback-work/` is banned from guest/support allowlists.
- Owner-controlled feedback-to-relay-source conversion now creates or reuses a
  prompt-free `relaySourceIntent` linked from the feedback item. The relay sees
  only ids, repo/share labels, branch, kind, and bounded reason text; the
  submitted feedback body stays in the owner-reviewed queue.
- Desktop agent now has an opt-in `feedback_work_worker` /
  `YAVER_FEEDBACK_WORK_WORKER=1` bridge that claims one queued feedback item
  and creates a local owner-machine Yaver task when `target="task"`, writes a
  private local issue draft when `target="issue"`, or queues prompt-free
  relay-source work for branch-style feedback. For local tasks and issue drafts,
  the feedback body is read only on the owner's machine; backend status updates
  receive only ids/status/reason metadata. It does not post to GitHub/GitLab or
  copy the feedback body into relay intent metadata.
- Provider issue posting is now present but fail-closed: the same worker can
  create a GitHub/GitLab issue from the private local draft only when
  `feedback_work_worker.create_provider_issues=true` or
  `YAVER_FEEDBACK_WORK_CREATE_PROVIDER_ISSUES=1` is set on the owner machine.
  It uses the owner's local git provider token store and project `origin`
  remote; Convex receives only the resulting HTTPS issue URL and bounded reason.
- The relay-source worker now recognizes `feedback:<itemId>` relay intents and
  marks the owner-reviewed feedback item `branch_created` after it prepares the
  scoped `yaver/*` branch. The status update includes only the item id, branch,
  worker id, and bounded reason.
- Still open: better runtime lifecycle for turning a stopped feedback worker on
  without restarting `yaver serve`, and clearer queue-row surfacing of
  missing-provider-token errors.

Tests:

- Friend can view artifact while dev VM is parked.
- Guest feedback cannot read repo/secrets.
- Feedback item can be queued, claimed, linked to relay-source branch work, and
  marked as task/issue-draft/branch-created without leaking the submitted body
  into relay intent metadata or backend status updates.
- Artifact storage quota/cleanup counters are enforced.

### Phase 8 - UI Integration

Mobile:

- Task composer adds "Yaver Cloud auto" path.
- New task progress states:
  - `Planning on relay`
  - `Workspace waking`
  - `Waiting for runner sign-in`
  - `Running on workspace`
  - `Heavy build allowance`
  - `Parked after idle`
- Large Project warning with allowance impact.
- Included allowance fuel gauge.
- Web-only subscription management copy on iOS.

Web:

- Managed Cloud panel shows included allowance and wake runs.
- Project/task detail shows placement decision and machine lane.
- Artifact share page.

MCP:

- Add `task_place` / `task_run_auto` or extend existing task tool after
  contract stabilizes.
- Preserve `device_id` for explicit remote calls.
- Add `cloud_workspace_status` shape that includes wake run, credits, and
  runner auth.

Tests:

- Mobile send does not fail when no machine is connected and Cloud Workspace is
  available.
- Mobile shows honest relay-only message before compute is ready.
- iOS does not deep-link to external credit checkout unless policy allows it.
- Web/mobile wake states match backend slugs.

## Data Model Proposal

### `machineWakeRuns`

Fields:

- `userId`
- `machineId`
- `requestedBySurface`
- `state`
- `phase`
- `progress`
- `startedAt`
- `phaseStartedAt`
- `deadlineAt`
- `completedAt`
- `blockingAction`
- `blockingActionExpiresAt`
- `provider`
- `providerServerId`
- `providerActionIds`
- `providerStatus`
- `providerProgress`
- `providerErrorCode`
- `providerErrorMessage`
- `agentReachable`
- `agentUsable`
- `agentProofStatus`
- `safeUserMessage`
- `operatorMessage`
- `createdAt`
- `updatedAt`

Privacy: no IPs if avoidable, no secrets, no prompts, no repo paths, no stdout.

### `taskPlacements`

Fields:

- `userId`
- `taskIntentId`
- `sourceSurface`
- `projectId`
- `projectClass`
- `lane`
- `machineId`
- `deviceId`
- `computeProfile`
- `handoffMode`
- `runner`
- `runnerAuthState`
- `needsWake`
- `wakeRunId`
- `needsResize`
- `creditEstimate`
- `decisionReason`
- `userMessage`
- `createdAt`
- `updatedAt`

Privacy: no full prompt, no file contents, no absolute paths.

### `projectProfiles`

Fields:

- `userId`
- `projectId`
- `source`
- `repoProvider`
- `repoSizeClass`
- `fileCountBucket`
- `diskSizeBucket`
- `workspaceCountBucket`
- `hasNativeIos`
- `hasNativeAndroid`
- `hasDockerCompose`
- `hasServerlessTemplate`
- `selectedApp`
- `lastClassifiedAt`
- `lastHeavyReason`

Privacy: use buckets and booleans. Do not store absolute paths or dependency
lists centrally.

## Implementation Order

Recommended order:

1. Implemented: WakeRun model first. Without this, cloud task routing inherited
   ambiguous wake state.
2. Project classifier second. Without this, Yaver cannot decide 8GB vs 16GB vs
   32GB defensibly.
3. Task placement router third. It can then depend on wake + classification.
4. Cloud profile/volume resize fourth. This is the expensive provider work.
5. Relay source runner fifth. It improves perceived latency but must respect
   auth boundaries.
6. Credit UI/metering polish sixth. The ledger exists, but naming and profile
   rates need product alignment.
7. Artifact/share/Feedback SDK loop seventh.

## P0 Task List

- [x] Add durable `wakeRuns`.
- [x] Make placement activation and direct web/mobile wake/park routes return
      `wakeRunId`.
- [x] Persist provider action ids and progress.
- [x] Prevent dry-run wake from setting machine `active`.
- [x] Require machine-bound proof before wake `ready`.
- [x] Add project profile ingestion route and Web/mobile coarse profile writes.
- [x] Add project classifier pure library.
- [x] Add large-monorepo/large-project detection.
- [x] Add placement decision contract.
- [x] Add credit estimate contract.
- [x] Add first-pass client-held queue guard while cloud wakes or target
      connection differs.
- [x] Add browser-local pending dispatch worker for Web Cloud Workspace tasks.
- [x] Wire mobile task composer to the mobile-local pending dispatch worker.
- [x] Rebind pending placement task ids to the real agent task after local
      dispatch.
- [x] Add durable p2p/local task route that can queue while cloud wakes without
      storing prompt content centrally.
- [x] Record desktop `/tasks` placement metadata without central prompt storage.
- [x] Block desktop `/tasks` wrong-machine Cloud Workspace execution before
      local runner launch.
- [x] Have desktop `/tasks` create prompt-free pending placements and attempt
      activation when local execution is deferred.
- [x] Preserve structured activation blockers from desktop `/tasks` Cloud
      Workspace deferral before any local runner starts.
- [x] Preserve structured Cloud Workspace defer errors through desktop HTTP
      clients instead of generic HTTP failure strings.
- [x] Add first-pass caller-held desktop handoff for HTTP terminal and
      `yaver code` task creation.
- [x] Rebind desktop pending Cloud Workspace placements to the real workspace
      task id after handoff.
- [x] Record prompt-free desktop dispatch intent status during Cloud Workspace
      handoff.
- [x] Retarget classic `yaver attach` active-task polling after Cloud
      Workspace handoff.
- [x] Retarget code-terminal TUI active-task polling after Cloud Workspace
      handoff.
- [x] Add first-pass legacy QUIC terminal handoff for Cloud Workspace
      placements.
- [x] Add local desktop spool/retry for pending Cloud Workspace handoffs after
      process restart.
- [x] Add prompt-free terminal status for local pending Cloud Workspace
      handoffs.
- [x] Expire stale desktop prompt-spool handoffs using the backend
      dispatch-intent deadline before retrying.
- [x] Persist desktop local-spool `blockedAction` and skip retry for
      auth/billing/resize/wake-failure blockers until another surface clears
      or requeues the task.
- [x] Refresh desktop local-spool blockers from prompt-free backend dispatch
      intent metadata before skipping, so a cleared auth/resize blocker can
      resume dispatch without storing prompts centrally.
- [x] Send desktop `clearBlockedAction` when a backend-cleared local-spool row
      resumes dispatch, keeping server-side blocker preservation aligned with
      web/mobile while prompts stay local.
- [x] Emit prompt-free live progress from desktop Cloud Workspace handoff while
      the bounded wait is in progress.
- [x] Fail fast from desktop Cloud Workspace handoff when activation already
      returned an actionable blocker instead of waiting for reachability.
- [x] Prevent web pending Cloud Workspace dispatch from auto-running
      auth/billing/resize/wake-failed blocked prompts after the target connects.
- [x] Persist prompt-free dispatch `blockedAction` server-side and preserve
      user-action blockers against stale queued/dispatching create/update
      races.
- [x] Apply the same preservation rule when `wakeRuns` syncs progress into
      dispatch intents, and derive structured auth/resize/wake-failure
      `blockedAction` values from wake/provision phases.
- [x] Add prompt-free placement status read model with latest wake/provision
      progress for web/mobile Cloud Workspace pending UX, including a
      same-machine fallback when a shared wake run is re-associated to a newer
      placement.
- [x] Filter placement-status wake selection to wake/provision runs so linked
      park metadata cannot mask actionable wake/provision state for a pending
      Cloud Workspace task.
- [x] Wire web pending Cloud Workspace placeholders to that status read model
      before dispatch attempts, including wake progress and user-action
      blocker mapping.
- [x] Add mobile pending Cloud Workspace status-merge helper parity for wake
      progress and user-action blocker placeholders.
- [x] Clear web/mobile user-action blockers only when placement status proves
      the workspace is runnable again (`running` placement or succeeded
      wake/provision run), so stale queued metadata cannot clear auth/billing
      blockers while successful re-auth can resume dispatch.
- [x] Add explicit backend dispatch-intent unblock (`clearBlockedAction`) used
      by web/mobile only after that readiness proof, so server-side blocker
      preservation does not trap cleared auth/resize tasks forever.
- [x] Preserve structured `cloud_workspace_required` task-create responses in
      the mobile QUIC client for future mobile client-held Cloud Workspace
      dispatch.
- [x] Wire the mobile task screen to save prompt-held Cloud Workspace deferrals,
      show pending placeholders, sync wake status, and dispatch when the
      assigned workspace is connected.
- [x] Set `allowLocalFallback=true` on web/mobile final Cloud Workspace handoff
      POSTs so the assigned target accepts the task instead of re-deferring it.
- [x] Add direct mobile pending Cloud Workspace queue tests for prompt privacy,
      wake progress, and user-action blocker preservation.
- [x] Add direct mobile transport tests for structured
      `cloud_workspace_required` decoding.
- [x] Add web/mobile request-body tests for `allowLocalFallback` final handoff
      serialization.
- [x] Add runner-auth blocked state to wake/task UI.
- [x] Preserve structured desktop/QUIC activation blockers on Cloud Workspace
      defer responses.
- [x] Update Cloud Workspace copy from old $9/40h/CPX51 wording to credit-based
      internal profiles.
- [x] Make managed Cloud Workspace auto-park default-on, with explicit operator
      disable only, so forgotten Hetzner boxes do not keep billing.
- [x] Add backend coverage that customer-facing auto-park requests can enable
      or tune auto-close, but cannot disable Cloud Workspace cost protection.
- [x] Let flat-plan included Cloud Workspace allowance satisfy wake/start
      gating until the next billable window can no longer be covered.
- [x] Make meter exhaustion immediately attempt Cloud Workspace park instead
      of only marking the row suspended while provider compute may still run.
- [x] Remove visible Tailscale/Cloudflare/self-host relay setup guidance from
      normal web/mobile product surfaces while keeping compatibility internals.
- [x] Add mobile regression coverage that store/mobile source does not call
      Yaver infrastructure checkout, credit checkout, portal, cancel, plan
      upgrade, or RevenueCat purchase APIs.
- [x] Keep buyer-side MCP billing tools to flat-plan subscription UX only:
      status, checkout, and manage for Free, Relay Pro, and Cloud Workspace;
      no discoverable top-up or credit-pack tool and no normie-facing prepaid
      wallet copy.
- [x] Prevent subscription activation/recovery from reusing dead or deleting
      Cloud Workspace rows as if they were usable capacity.
- [x] Ensure no Claude/Codex OAuth can be stored through relay/SDK/guest-scoped
      runner-auth paths.

## P1 Task List

- [x] Implement persistent-volume normal wake path.
- [x] Prevent heavy/build placements from reusing underpowered Cloud Workspace
      profiles.
- [x] Make placement preview/record capacity-aware too: heavy/build decisions
      no longer preselect an active smaller workspace just because it is newest;
      activation can then resize/provision honestly.
- [x] Keep placement preview/record inside Yaver-managed Cloud Workspace rows:
      explicit `origin="self-hosted"` rows are ignored, while legacy rows with
      no origin still count as managed for back-compat.
- [x] Record resize-required control-plane state for an underpowered persisted
      workspace before provisioning a second profile.
- [x] Implement first-pass resize by detach/delete/recreate/attach.
- [x] Add first-pass provider profile mapping for standard/heavy/build.
- [x] Add first-pass profile-specific metering and included-hour buckets.
- [x] Align Cloud Workspace included allowance to 120 standard credits:
      120h standard, 60h heavy, 30h build by default, with env overrides.
- [x] Add standard-credit metadata to placement estimates so web/mobile can
      avoid provider/hourly pricing copy while preserving legacy cents fields.
- [x] Update web billing/cloud panels to describe included workspace allowance
      and flat subscriptions instead of Boost balance, raw hourly running rates,
      or prepaid top-up controls to normies.
- [x] Add large build ask/confirm before starting expensive phase.
- [x] Add relay source intent queue/API for Yaver Git.
- [x] Add owner-scoped relay claim/update path for project-share work.
- [x] Add first-pass relay worker branch-prep primitive for Yaver Git.
- [x] Add bounded relay source-only patch commit primitive for Yaver Git.
- [x] Add first-pass local relay-source planner contract that validates
      prompt-held work and explicit safe source patches.
- [x] Add first-pass relay worker-loop primitive that claims/accepts a
      relay-source intent and calls plan/prepare/apply once without storing
      prompts centrally.
- [x] Add background relay worker scheduler that polls queued relay-source
      intents and prepares scoped branches from prompt-free metadata.
- [x] Add first-pass artifact metadata object model and share URL routes.
- [x] Add first-pass artifact upload URL route and Convex storage-backed
      artifact records.
- [x] Gate Yaver-held artifact storage to Cloud Workspace or owner-preview;
      free/Relay Pro artifact records are external-link only.
- [x] Gate the web artifact upload controls to active Cloud Workspace while
      keeping external HTTPS artifact links available on all plans.
- [x] Show pending artifact upload reservations in web storage usage cards so
      UI quota math matches backend quota enforcement.
- [x] Add a mobile helper guard so Yaver-held artifact storage requires an
      explicit Cloud Workspace storage confirmation; external links remain
      available from mobile helpers.
- [x] Add artifact retention cleanup and storage quota/meter increments.
- [x] Require declared positive file size before Yaver-held artifact upload URL
      minting and delete Convex blobs best-effort when metadata is refused.
- [x] Add pending artifact upload reservations so abandoned upload URLs stay
      counted against owner storage quota until metadata consumes them.
- [x] Add web dashboard artifact manager and public artifact page for
      friend-visible build/app outputs.
- [x] Validate Feedback SDK artifact attachments against the project/share
      boundary before owner-visible work is queued.
- [x] Share public artifact visibility checks between public reads and
      access-touch so expired/hidden links do not refresh access metadata.
- [x] Add first-pass Feedback SDK queue-to-task/issue path: SDK can queue,
      owner can list/claim/update status.
- [x] Add per-owner/per-project pending feedback queue caps so leaked SDK tokens
      cannot create unbounded owner-reviewed work.
- [x] Add owner-controlled feedback-to-relay-source conversion that links a
      feedback item to prompt-free branch work.
- [x] Derive safe GitHub/GitLab provider target metadata for feedback-created
      relay-source intents without storing feedback body text or credentials.
- [x] Validate feedback-linked relay-source intent reuse by owner/share/local
      task and refuse foreign local-task collisions.
- [x] Add opt-in desktop feedback worker bridge that claims queued feedback and
      queues prompt-free relay-source branch work.
- [x] Add opt-in desktop feedback worker path that turns `target="task"`
      feedback items into local owner-machine Yaver tasks.
- [x] Mark feedback queue items `branch_created` after relay prepares the linked
      scoped branch.
- [x] Add owner UI controls and GitHub/GitLab issue conversion for feedback
      queue items.
- [x] Add web Billing subscribe/manage/cancel controls for the two paid products
      only: Relay Pro and Cloud Workspace.
- [x] Make web Billing resource state product-aware: Relay Pro surfaces the
      managed relay row and does not warn about a missing Cloud Workspace box,
      while Cloud Workspace still warns when its compute row is missing.
- [x] Make billing reconcile product-aware: Relay Pro repairs a missing
      managed relay, Cloud Workspace repairs a missing managed box, and both
      stay behind active-subscription/provisioning gates.
- [x] Add backend regression coverage for Relay Pro reconcile reuse: active or
      provisioning relay rows are reused, while stopped/error/missing rows
      trigger the repair path.
- [x] Add safe source-edit generator for small changes, without user
      Claude/Codex OAuth on shared relay.
- [x] Add first-pass GitHub/GitLab mirror push for relay-source `yaver/*`
      branches using the owner's local provider token.
- [x] Add non-secret provider branch target/auth-status contract for
      GitHub/GitLab App scoped relay work.
- [x] Add first-pass GitHub App installation-token broker for owner-scoped
      relay-source intents.
- [x] Wire GitHub App installation token into scoped `yaver/*` provider branch
      push with token redaction tests.
- [x] Add explicit GitLab scoped-token unsupported boundary so relay/UI can
      fall back honestly without storing GitLab secrets in Convex.
- [ ] Add a future GitLab user-OAuth or project-token flow if Yaver decides to
      hold refreshable GitLab write credentials.
- [x] Surface relay-source provider branch/auth status in the owner web UI.
- [x] Add first-pass mobile/web parity tests for wake and placement helper
      states.

## P2 Task List

- [ ] Warm-spare pool for sub-30s wake on paid/high-usage users.
- [ ] Team/shared Cloud Workspace policies.
- [ ] Always-warm paid add-on.
- [ ] Managed AI add-on separate from base Cloud Workspace.
- [ ] Dedicated per-user relay runner host if Claude/Codex-on-relay is ever
      required.
- [ ] Cross-region capacity marketplace.

## Testing Plan

Backend:

```text
npx convex dev/typecheck path appropriate to repo scripts
cloudMachines.test.mts
new wakeRun tests
new placement schema/privacy tests
```

Desktop:

```text
go test ./desktop/agent -run 'TaskPlacement|ProjectClass|RemoteProxy|RunnerAuth|CloudStopStart|AutorunPlacement'
go test ./desktop/agent -run 'ConvexPrivacy'
```

Mobile:

```text
npm test -- taskPlacement
npm test -- wakeMachine
npm test -- boxInit
```

Manual dry-run:

```text
create Yaver-native project
send task with no connected machine
verify cloud intent queues and wakeRun appears
verify relay/source message is honest
verify compute attach verifies runner auth
verify build artifact persists after park
```

Provider live gate:

- Do not flip live Hetzner flags until dry-run proves:
  - wake run state transitions
  - no active state on dry-run
  - reserve gate
  - auto-park
  - volume attach
  - runner auth verification
  - provider action progress

## Risks

1. Wake exceeds 60s.
   - Mitigation: persistent volume + golden image, no full snapshot restore in
     normal path, warm spare later.

2. Claude/Codex OAuth breaks on resize or new IP.
   - Mitigation: tokens stay on persistent volume, same uid/home, verify before
     task, show reconnect CTA.

3. Relay overclaims context.
   - Mitigation: explicit relay-source state and honest copy when live repo is
     not inspected yet.

4. 8GB default OOMs imported monorepos.
   - Mitigation: classifier, app/package focus, heavy/build escalation, credit
     warning.

5. $29 margin breaks under all-day compute.
   - Mitigation: credits, profile multipliers, pause-on-exhaustion, auto-park,
     source-only relay mode.

6. Provider stale health promotes wrong machine.
   - Mitigation: signed/machine-token health proof before readiness.

7. Mobile App Store purchase policy.
   - Mitigation: web subscription management, mobile shows status/control only
     on iOS unless external purchase entitlement is approved.

8. Existing docs/code disagree.
   - Mitigation: every implementation phase begins by grepping current code and
     updating stale docs in the same PR.

## Non-Goals For First Implementation

- No public machine-size picker.
- No compute-only SKU.
- No always-on workspace in $29.
- No managed AI tokens included in base Cloud Workspace.
- No user Claude/Codex OAuth on shared relay.
- No production hosting on the dev VM.
- No full arbitrary monorepo promise on 8GB.
- No postpaid surprise billing.

## First Code Change Recommendation

When implementation starts, do not begin with provider resizing. Begin with the
control-plane contract:

```text
machineWakeRuns + task placement pure decision + project classifier fixtures
```

This gives every surface one truth before money-spending provider code changes.
After that, wire mobile task submission to create a cloud intent when no live
machine is selected, and only then implement persistent-volume profile resize.

## Implementation Evidence: Prompt-Free Cloud Handoff

Local code now pins the relay-to-compute handoff boundary:

- Convex `taskDispatchIntents` is metadata only: ids, target machine/device,
  lane, runner label, status, short reason/error, counters, expiry.
- Backend `/tasks/dispatch-intents` and `/tasks/dispatch-intents/status` use
  explicit allowlists before calling mutations.
- Web and mobile dispatch-intent HTTP helpers now build explicit request bodies
  instead of stringifying arbitrary runtime objects.
- Prompt-bearing fields stay in local pending queues until the assigned
  workspace is reachable: prompt text, command body, `workDir`, images, files,
  stdout, speech context, and secrets are not part of the durable intent body.
- Placeholder tasks shown during wake render honest status/progress/blockers
  without echoing the original prompt.

Regression checks run locally:

```text
web/lib: npx tsx pending-cloud-dispatch.test.ts
web: npx tsc --noEmit --pretty false
mobile/src/lib: npx tsx pendingCloudDispatch.test.mts
mobile/src/lib: npx tsc --noEmit --pretty false
backend/convex: npx tsx taskDispatchIntents.test.mts
backend/convex: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Feedback Work Worker Placement Guard

Background feedback work items that target owner tasks now respect machine
placement before creating local runner work:

- The feedback work worker previews placement before creating a
  `feedback-work` task from a claimed queue item.
- If placement selects a different Cloud Workspace, the worker records and
  activates pending placement, marks the feedback item `blocked` with the
  pending id and activation reason, and creates no local task.
- This is deliberately a blocked queue state rather than silent remote task
  creation. Feedback work needs explicit task-completion/status reconciliation
  before remote task execution can be made honest.
- Relay-source and provider-issue targets are unchanged; only owner-machine
  task creation is guarded.
- Placement metadata remains prompt-free and does not include feedback body,
  generated task prompt, title, or local workdir path.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestFeedbackWorkWorkerBlocksTaskTargetWhenCloudPlacementSelected|TestFeedbackWorkWorkerTickCreatesOwnerTaskForTaskTarget|TestFeedbackWorkWorkerQueuesRelaySourceForOwnedProject'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Code Control Placement Audit

The terminal/code-control task surface was audited for direct machine bypasses:

- New code-control tasks call the local or selected remote agent `POST /tasks`
  endpoint through `codeCreateTask`, so placement remains owned by the task API
  instead of by the CLI.
- If the task API returns `CloudWorkspaceRequiredError`, code-control uses the
  shared `createTaskOnCloudWorkspace` handoff path. That path only waits for an
  already selected placement to become reachable and records a pending dispatch;
  it does not contain provider-specific Hetzner creation or snapshot calls.
- Forks prefer `POST /tasks/{id}/fork`, which is the same placement-aware fork
  route used by other surfaces. The legacy fallback only runs for old agents
  that return 404, and that fallback creates the child through `codeCreateTask`.
- Continues remain pinned to the selected existing task/device with
  `POST /tasks/{id}/continue`; they do not create new Cloud Workspace capacity.
- Terminal client wrappers in `client.go`, `code_cmd.go`, `attach.go`, and
  `code_terminal.go` were re-audited. They create tasks only by calling
  placement-aware HTTP `/tasks`, QUIC `task_create`, or code-control helpers;
  they do not call `TaskManager.CreateTask*` directly.

No code change was required for this audit. No live Hetzner, Convex production,
LemonSqueezy, server, snapshot, or billing state was touched.

## Implementation Evidence: Agent Graph Placement Audit

Agent graph task creation was re-audited separately from generic task ingress:

- Graph nodes already carry an explicit node placement model. Non-local
  placements run through `executeRemoteChatNode` / remote graph execution
  instead of local `TaskManager.CreateTaskWithOptions`.
- Local graph chat nodes still create local tasks, but only after
  `prepareGraphNodeSlice` resolves the node's local slice/workdir contract.
  This is a graph-level placement decision, not a direct Cloud Workspace bypass
  from a public ingress surface.
- Moving local graph nodes into the generic Cloud Workspace task placement
  layer would need graph-run state replication: node status, dependency
  scheduling, slice contracts, task-id attachment, remote completion callbacks,
  and run summary reconciliation. Until that protocol exists, graph placement
  remains owned by the graph engine.

No code change was required for this audit. No live Hetzner, Convex production,
LemonSqueezy, server, snapshot, or billing state was touched.

## Implementation Evidence: Telegram Chatbot Placement Guard

Telegram chatbot task ingress now respects the machine placement layer before
creating local runner work:

- Shared ingress deferral logic now lives in the desktop placement client:
  preview placement, record a pending task id, activate placement, and return a
  structured deferral without giving ingress surfaces provider controls.
- `NewChatBot` accepts placement config from the same backend URL, auth token,
  local device id, and workdir already used by the agent task API.
- Telegram `/task` and plain-text task creation preview placement with
  prompt-free metadata (`sourceSurface=telegram`, coarse project/repo signals
  only).
- If placement selects a different Cloud Workspace, the bot records and
  activates a pending placement, returns a visible Cloud Workspace handoff
  message to chat, and creates no local task on the relay.
- The bot does not silently remote-dispatch yet, because its completion watcher
  polls the local task registry. This keeps the UX honest and avoids burning
  relay runner time while the workspace wakes or needs auth.
- If backend placement config is absent or preview fails, the legacy local
  behavior remains unchanged for self-host/local chatbot setups.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestChatBotDefersTelegramTaskWhenCloudPlacementSelected|TestFeedbackWorkWorkerBlocksTaskTargetWhenCloudPlacementSelected|TestSchedulerPausesClassicTaskWhenCloudPlacementSelected'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Vibe Clip Fix Placement Guard

Vibe-preview clip auto-fix now checks machine placement before creating local
runner work:

- The endpoint still files the feedback report locally because it depends on a
  local recorded MP4 artifact and FeedbackManager's local persistence.
- Before auto-fix task creation, the handler uses the shared HTTPServer ingress
  deferral helper with prompt-free metadata (`sourceSurface=vibe-clip-fix`,
  `kind=vibe`, coarse project/repo signals only).
- If placement selects a different Cloud Workspace, the response returns the
  feedback id plus a `pending-cloud:*` handoff task id and no local runner task
  is created on the relay.
- The clip comment, generated feedback metadata, MP4 bytes, and generated fix
  prompt remain local-only and are not included in placement payloads.

Verification for this slice is compile/hygiene only because the handler depends
on existing local vibe-preview clip and feedback persistence setup:

```text
desktop/agent: go test . -run '^$'
git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Testkit Grow Placement Guard

Testkit self-growth authoring now respects the machine placement layer before
creating local runner work:

- The `project_test_grow` ops verb checks placement before enqueueing the
  optional runner-authored spec-writing task.
- The post-task `maybeGrowTestsAfterTask` hook also attempts placement when
  local agent config provides backend auth; self-host/local setups without that
  config keep the previous local behavior.
- Placement metadata is prompt-free (`sourceSurface=testkit-grow`,
  `kind=test`, requested runner, coarse project/repo signals only).
- If placement selects a different Cloud Workspace, the returned grow plan uses
  a `pending-cloud:*` task id and no local task is created on the relay. The
  author prompt is stripped from the ops response after cloud deferral so the
  prompt remains local/client-held.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestOpsProjectTestGrowDefersAuthorTaskWhenCloudPlacementSelected|TestBlackBoxFatalCrashDefersWhenCloudPlacementSelected'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Blackbox Crash Placement Guard

Blackbox fatal-crash auto-fix now respects the machine placement layer before
creating local runner work:

- `HTTPServer` exposes a shared ingress deferral helper that builds placement
  config from the server backend URL, auth token, local device id, and task
  workdir. The helper previews, records, and activates placement without giving
  the Blackbox surface provider controls.
- Fatal crash SSE ingestion uses prompt-free metadata
  (`sourceSurface=blackbox-crash`, coarse project/repo signals only) before
  synthesizing the crash-fix prompt.
- If placement selects a different Cloud Workspace, the SSE stream emits a
  `cloud_workspace_required` event with a `pending-cloud:*` task id and no
  local task is created on the relay.
- Crash message, stack frames, app logs, and generated fix prompt remain
  local-only and do not appear in placement payloads.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestBlackBoxFatalCrashDefersWhenCloudPlacementSelected|TestFinalizeBlocksInitialTaskWhenCloudPlacementSelected|TestAgentSessionDefersTaskWhenCloudPlacementSelected'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Finalize Placement Guard

Finalize-mode background task creation now respects the machine placement layer
before starting local runner work:

- `FinalizeManager` accepts placement config from `HTTPServer`, using the same
  backend URL, auth token, local device id, and task workdir as other desktop
  task ingress surfaces.
- Initial finalize task creation previews, records, and activates placement
  through the shared ingress deferral helper with prompt-free metadata
  (`sourceSurface=finalize`, requested runner, coarse project/repo signals
  only).
- If placement selects a different Cloud Workspace, the finalize run is marked
  `blocked` with a `pending-cloud:*` task id and a clear Cloud Workspace
  blocker message. No local `TaskManager` task is created and no relay runner
  is started.
- The finalize objective and generated finalize prompt remain local-only and do
  not appear in placement metadata. Remote finalize execution/result
  reconciliation remains a future protocol, so the current behavior chooses a
  visible blocked state over silent remote dispatch.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestFinalizeBlocksInitialTaskWhenCloudPlacementSelected|TestAgentSessionDefersTaskWhenCloudPlacementSelected|TestDispatchVoiceTranscriptDefersWhenCloudPlacementSelected|TestChatBotDefersTelegramTaskWhenCloudPlacementSelected'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Agent Session Placement Guard

Agent-session task orchestration now respects the machine placement layer before
creating local runner work:

- `AgentSessionManager` accepts placement config from `HTTPServer`, so HTTP and
  MCP runner ops use the same backend URL, auth token, local device id, and
  workdir as the regular task API.
- Initial session creation and follow-up messages call the shared ingress
  deferral helper with prompt-free metadata (`sourceSurface=agent-session`,
  coarse project/repo signals only).
- If placement selects a different Cloud Workspace, the session is persisted as
  `awaiting_input` with a `pending-cloud:*` current task id and a local message
  explaining that the handoff was queued. No local `TaskManager` task is
  created and no relay runner is started.
- The actual session prompt and follow-up text remain in the local
  `agent-sessions.json` state only; placement metadata does not include
  session title, prompt body, history, or workdir text.
- Remote execution/result reconciliation for agent sessions remains a future
  protocol, so this guard chooses honest deferral over silent remote dispatch.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestAgentSessionDefersTaskWhenCloudPlacementSelected|TestDispatchVoiceTranscriptDefersWhenCloudPlacementSelected|TestChatBotDefersTelegramTaskWhenCloudPlacementSelected'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Voice Dispatch Placement Guard

Voice transcript task dispatch now respects the machine placement layer before
creating local runner work:

- `/voice/stream` passes the agent backend URL, auth token, local device id,
  and task workdir into `DispatchVoiceTranscript`.
- Voice dispatch uses the shared ingress deferral helper with prompt-free
  metadata (`sourceSurface=voice-input`, coarse project/repo signals only).
- If placement selects a different Cloud Workspace, voice returns a
  `cloud_workspace_required` task result with a pending task id and spoken text
  explaining that the workspace is selected. No local task is created and no
  relay runner is started.
- Voice does not silently remote-dispatch yet, because its TTS bridge waits on
  the local task registry. The current behavior is deliberately honest while
  the remote result/streaming callback contract is still pending.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestDispatchVoiceTranscriptDefersWhenCloudPlacementSelected|TestChatBotDefersTelegramTaskWhenCloudPlacementSelected'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Scheduler Placement Guard

Classic scheduled tasks now have a machine-placement guard so unattended cron
jobs do not quietly spend local runner time when Cloud Workspace is the selected
lane:

- The daemon wires scheduler placement config from the same Convex URL, auth
  token, and local device id used by the HTTP task layer.
- Before a non-routine scheduled task calls `CreateTaskWithOptions`, the
  scheduler previews placement using coarse schedule/source/runner/workdir
  metadata.
- If placement selects a different Cloud Workspace, the scheduler records and
  activates a pending placement, pauses the schedule, records a
  `cloud_workspace_required` history row, and creates no local task.
- This is deliberately a visible pause rather than a silent retry loop: a
  recurring schedule that needs Cloud Workspace auth/wake/billing should not
  keep firing and burning local resources or repeatedly waking infrastructure.
- Verb-mode routines are unchanged because they already target machines through
  the ops dispatcher instead of `TaskManager.CreateTask`.
- Placement metadata remains prompt-free and does not include the schedule
  title, description, or local workdir path.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestSchedulerPausesClassicTaskWhenCloudPlacementSelected|TestSchedulesHTTPFlow|TestScheduleRunNowRequiresPOST'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Vibing Execute Placement Guard

Owner `/vibing/execute` now participates in the machine placement layer before
creating local work:

- Non-guest vibing requests preview placement with coarse project/source/runner
  metadata before `CreateTaskWithOptions`.
- If placement selects a different Cloud Workspace, the local agent records and
  activates pending placement, returns `action="cloud_workspace_required"`, and
  does not create a local vibing task.
- The response is intentionally a blocker rather than a silent remote handoff
  because `/vibing/task/{id}` currently polls the local task registry. A remote
  task id would need a first-class remote vibing-task proxy before it can be
  honest UX.
- Owner vibing requests now pass project paths through per-task `WorkDir`
  instead of mutating `TaskManager.workDir` globally, matching the recovery
  handler fix.
- Guest vibing stays local because its project access, runner policy, and
  sandbox/isolation constraints are local-host state.
- Placement metadata remains prompt-free and does not include the local workdir
  path.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestVibingExecuteDefersCloudPlacement|TestRecoverDefersCloudPlacement|TestHotReloadPullSkipsWhenCloudPlacementSelected'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Pre-Build Pull Placement Guard

The hot-reload pre-build git update helper now avoids spending local runner
time when placement says the work belongs on Cloud Workspace:

- Before spawning the `devserver-prepull` coding-agent task, the helper previews
  placement using coarse source/runner/workdir metrics.
- If placement selects a different Cloud Workspace, the helper returns a
  skipped result and does not create the local pre-build pull task.
- This is deliberately a skip, not a remote handoff: the actual build/reload
  flow owns the Cloud Workspace task lifecycle, while pre-pull is only a local
  convenience helper for ambiguous git states.
- Placement metadata remains prompt-free and does not include the local workdir
  path.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestHotReloadPullSkipsWhenCloudPlacementSelected|TestChooseHotReloadPullRunner|TestInterpretHotReloadPullResult'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: MCP Fork Placement Guard

MCP `fork_task` now matches the HTTP task-fork placement contract:

- Non-guest MCP forks preview placement before creating a local child task.
- If placement selects a different Cloud Workspace, MCP records and activates
  pending placement, then attempts short Cloud Workspace handoff for the forked
  child task.
- If handoff is blocked by wake/auth/billing, MCP returns structured
  `action="cloud_workspace_required"` and does not create a local child task.
- Guest-scoped parent tasks remain local for the same reason as HTTP forks:
  their sandbox, project, runner, CPU, and RAM constraints are local-host state.
- Convex placement/dispatch metadata stays prompt-free; the bounded fork
  handoff and new input stay only in the local pending dispatch body.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestMCPForkTaskDefersCloudPlacement|TestHandleTaskForkDefersCloudPlacement|TestMCPAskDefersCloudPlacement'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Task Fork Placement Guard

Owner-authenticated task forking now respects machine placement before creating
a local child task:

- `POST /tasks/{id}/fork` previews placement using coarse parent/source/runner
  metadata before `CreateTaskWithOptions`.
- If placement selects a different Cloud Workspace, the local agent records and
  activates pending placement, then attempts short Cloud Workspace handoff for
  the forked child task.
- If handoff is blocked by wake/auth/billing, the endpoint returns structured
  `action="cloud_workspace_required"` and does not create a local child task;
  the parent remains the only local task.
- Guest-scoped parent tasks stay local because their sandbox, runner, CPU/RAM,
  and project constraints are local-host state.
- Convex placement/dispatch metadata remains prompt-free; the bounded handoff
  prompt and new user input are only held in the local pending dispatch body for
  the assigned workspace.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestHandleTaskForkDefersCloudPlacement|TestHandleTaskForkCreatesChild|TestRecoverDefersCloudPlacement'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Recovery Placement Guard

Owner-authenticated `/recover` now routes through the machine placement layer
before creating local repair work:

- Recovery tasks preview placement using coarse source/project/workdir metrics
  before `CreateTaskWithOptions`.
- If placement selects a different Cloud Workspace, the local agent records and
  activates pending placement, then attempts short Cloud Workspace handoff. If
  wake/auth/billing blocks dispatch, `/recover` returns structured
  `action="cloud_workspace_required"` instead of starting local compute.
- The handler no longer mutates `TaskManager.workDir` globally when the request
  supplies `workDir`; it passes that directory through per-task options so one
  recovery request cannot silently retarget later unrelated tasks.
- Convex placement/dispatch metadata remains prompt-free; the raw recovery
  error text and generated repair prompt are only held in the local pending
  dispatch body for the assigned workspace handoff.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestRecoverDefersCloudPlacement|TestMCPAskDefersCloudPlacement|TestCreateTaskDefersCloudPlacement'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: MCP Ask Placement Guard

MCP `yaver_ask` single-task mode now participates in the machine placement
layer before answering locally:

- Single ask tasks preview placement before `CreateTaskWithOptions`.
- If placement selects a different Cloud Workspace, MCP records/activates a
  pending placement and attempts the same short Cloud Workspace handoff used by
  MCP `create_task`.
- If handoff is blocked by wake/auth/billing, MCP returns structured
  `action="cloud_workspace_required"` with `pendingTaskId`, placement, and
  activation details instead of answering locally.
- Convex placement/dispatch metadata remains prompt-free; the actual question
  is only held in the local pending dispatch body for the assigned workspace.
- Deep/broad ask graph mode remains local for now because it uses the separate
  multi-agent graph runtime and needs its own placement contract.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestMCPAskDefersCloudPlacement|TestMCPCreateTaskDefersCloudPlacement|TestWatchTurnDefersCloudPlacement'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Local-State Task Boundary Audit

Some task creators intentionally remain local until the machine layer has a
state replication protocol, even though they can spend runner time:

- `/todolist/implement-all`, `/todolist/{id}/implement`, auto-consume, and
  SDK classify immediate actions operate on local todo state, local screenshot
  files, blackbox captures, and a local completion watcher that updates item
  status from the local task registry. Routing these directly to a Cloud
  Workspace would either strand completion state on the relay or require
  copying prompt artifacts/images into a remote workspace without a lifecycle
  contract.
- Feedback-fix, test-app generation, and Blackbox/clip artifact repair flows
  that still depend on local feedback report directories, local videos, local
  screenshots, or local completion watchers are treated as local-artifact
  boundaries unless they now have a specific placement guard documented above.
- The correct Cloud Workspace version is not "post this local prompt to remote
  and hope"; it needs a first-class todo work-item model with artifact upload,
  workspace-side task creation, remote completion callbacks, and idempotent
  local status reconciliation.
- QUIC `task_create` was re-audited and already has a pre-task placement guard
  (`cloudRequiredMessage`) with regression coverage for prompt-free placement
  metadata and structured activation blockers.

Near-term implementation rule: new owner-auth task entrypoints that do not
depend on local-only artifacts must call the placement layer before
`CreateTask*`. Local-artifact entrypoints must either stay pinned to the
current host or first implement an explicit artifact/status handoff protocol.

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this audit.

## Implementation Evidence: Watch Placement Guard

Standalone smartwatch task creation now respects machine placement before
starting local runner work:

- `/watch/turn` transcript/confirmed-intent dispatch previews placement before
  `CreateTaskWithOptions`.
- If placement selects a different Cloud Workspace, the local agent records and
  activates a pending placement, returns a `handoff` watch reply targeting
  `cloud-workspace`, and does not create a local task.
- The watch path intentionally does not silently create a remote task yet,
  because `/watch/result` currently polls the local task registry. Returning a
  handoff preserves UX honesty instead of giving the wrist a task id it cannot
  poll.
- Convex receives only coarse placement metadata; the watch transcript and
  expanded watch prompt are not sent in placement/activation payloads.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestWatchTurnDefersCloudPlacement|TestWhatsAppTaskDefersCloudPlacement|TestChainDefersCloudPlacement'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: WhatsApp Ingress Placement Guard

The shared-secret WhatsApp command ingress now participates in the machine
placement layer before creating local tasks:

- `action="task"` previews placement with coarse source/project/workdir metrics
  and no WhatsApp command text in the Convex metadata payload.
- If placement selects a different Cloud Workspace, the local agent records and
  activates a pending placement, stores the prompt-bearing command only in the
  local pending dispatch queue, and returns `action="cloud_workspace_required"`
  instead of starting local compute.
- If the target workspace is reachable during the short handoff window, the
  ingress can return the remote Cloud Workspace task id/status. If wake/auth is
  blocked, the response carries the pending id and activation details.
- Status/reload WhatsApp actions stay unchanged; only task creation is guarded.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestWhatsApp|TestChainDefersCloudPlacement|TestDeployDefersCloudPlacement'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Chain Placement Guard

The owner-authenticated `/chain` endpoint now has a placement preflight before
it creates any local chain tasks:

- Chain creation previews privacy-safe placement metadata derived from the
  caller surface, runner, local workdir metrics, and the first chain item.
- If placement selects a different Cloud Workspace and the caller did not set
  `allowLocalFallback`, the local agent records/activates a pending placement
  and returns `action="cloud_workspace_required"` without creating any local
  chained tasks.
- The chain body stays client-side during the blocker response; Convex receives
  only coarse placement metadata and activation status, not the task list,
  prompt text, descriptions, or local workdir.
- This is intentionally conservative until a first-class remote `/chain`
  handoff protocol exists. It protects margins and avoids wrong-machine chain
  execution without pretending the local relay can run a full cloud chain.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestChainDefersCloudPlacement|TestDeployDefersCloudPlacement|TestCreateTaskDefersCloudPlacement'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Deploy Placement Guard

Deploy-shaped HTTP actions now respect the same Cloud Workspace placement
contract as `/tasks`, MCP, webhook, and CLI task creation:

- `/deploy` previews placement before creating a local deploy task. If the
  backend selects a different Cloud Workspace, the local agent records a pending
  placement, activates it, stores prompt-held dispatch locally, and returns
  `action="cloud_workspace_required"` instead of running the deploy locally.
- `/vibing/deploy` does the same for owner-authenticated deploys resolved from
  the active vibing project. Guest vibing deploys stay local because their
  project and sandbox permissions are scoped to the current host.
- Both deploy paths pass prompt-free metadata to Convex placement/dispatch
  routes; the deploy command and working directory are only kept in the local
  pending dispatch body for the assigned workspace handoff.
- Cloud Workspace handoff responses now avoid assuming recorded placement is
  non-nil before reading `targetDeviceId`, so an edge-case backend response
  cannot panic the local agent.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestDeployDefersCloudPlacement|TestWebhookTriggerDefersCloudPlacement|TestMCPCreateTaskDefersCloudPlacement|TestCreateTaskDefersCloudPlacement|TestCloudTaskDispatchIntentPayloadsArePromptFree|TestPromptFreeConvexMetadataPayload'
desktop/agent: go test . -run '^$'
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Direct Provision Bypass Closed

The Cloud Workspace backend now has fewer accidental live-spend paths:

- Generic `POST /machines` remains authenticated and full-scope checked, but now
  returns HTTP 410 instead of calling `internal.cloudMachines.create`. New
  managed machines must come from Cloud Workspace subscription, reconcile, or
  placement activation paths.
- Owner preview activation no longer calls `internal.cloudMachines.create`.
  It uses `cloudMachines.createPreviewSharedMachine`, which writes only a
  metadata row for the already-running shared preview server and does not
  schedule `internal.cloudMachines.provision`.
- The preview row is then attached to the configured shared preview hostname/IP,
  preserving the owner test path without risking a surprise Hetzner allocation.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsx cloudMachines.test.mts
backend/convex: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: CLI Billing Copy and Dev Bypass Gate

The desktop CLI now aligns with the normie-facing flat Cloud Workspace product:

- `yaver cloud` usage copy says Cloud Workspace purchase is web-only and no
  longer advertises `--skip-payment`, private-preview checkout, or a
  no-LemonSqueezy bypass path.
- `yaver cloud create --skip-payment`, legacy `YAVER_CLOUD_SKIP_PAYMENT=true`,
  and `yaver cloud smoke --skip-payment` now require the explicit local dev flag
  `YAVER_CLOUD_DEV_BYPASS=1` before they call `/billing/yaver-cloud/dev-activate`.
- The low-level activation helper remains testable for owner/debug smoke tests,
  but the normal CLI path is checkout/subscription first.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestCloudDevBypassRequiresExplicitEnv|TestCloudUsageDoesNotAdvertiseDevBypass|TestActivateCloudMachine_DevBypass|TestCloudPreviewTodoApp_PushBundleRoundTrip'
backend/convex: npx tsx http.test.mts
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Prompt-Free Placement Request Builders

Placement preview/record requests are now explicitly whitelisted on both client
and backend sides:

- Web `taskPlacementRequestBody` serializes only `taskId`, `kind`,
  `sourceSurface`, `projectSlug`, runner/target ids, force flags, and coarse
  project metrics.
- Mobile uses the same pure helper shape from `taskPlacementCore`, defaulting
  `sourceSurface` to `mobile`.
- Runtime extras such as `title`, `description`, `userPrompt`, `workDir`, and
  `diff` are stripped before web/mobile placement preview or record requests
  are serialized.
- The Convex HTTP placement routes `/tasks/placement/preview`, `/record`,
  `/status`, `/rebind`, and `/activate` now use the prompt-free body guard too,
  so prompt-bearing placement metadata requests fail with HTTP 400 instead of
  being silently ignored.
- Feedback/artifact endpoints are intentionally not included in this guard
  because they are user-visible content stores, not task-placement metadata.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsc --noEmit --pretty false
web: npx tsx lib/task-placement-request.test.ts
web: npx tsc --noEmit --pretty false
mobile: npx tsx src/lib/taskPlacement.test.mts
mobile: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Backend Prompt-Free Metadata Route Guard

The Convex HTTP layer now rejects prompt-bearing metadata requests before they
reach the task-dispatch or relay-source mutations:

- `promptFreeMetadataBodyDeniedReason` recursively scans request bodies for
  sensitive task-content keys such as `title`, `description`, `prompt`,
  `userPrompt`, `bodyJson`, `workDir`, `files`, `images`, `stdout`, `secret`,
  `token`, `diff`, `patch`, `customCommand`, and git remote/branch fields.
- `/tasks/dispatch-intents` and `/tasks/dispatch-intents/status` return HTTP
  400 if a client tries to include those fields.
- `/tasks/relay-source-intents`, `/status`, `/claim`, `/github-app-token`, and
  `/gitlab-token` use the same guard.
- This turns the prompt-free architecture from "handlers ignore extra fields"
  into a fail-closed API contract. The actual task body still belongs in the
  client-held local spool or direct P2P request to the selected machine.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsx taskDispatchIntents.test.mts
backend/convex: npx tsx relaySourceIntents.test.mts
backend/convex: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Desktop Prompt-Free Dispatch Guard

The desktop agent now has a shared defensive guard for Convex coordination
metadata used by Cloud Workspace and relay-source task handoff:

- `ensurePromptFreeConvexMetadataPayload` rejects sensitive task-content keys
  such as `title`, `description`, `prompt`, `userPrompt`, `bodyJson`, `workDir`,
  `gitRemote`, `gitBranch`, `diff`, `patch`, `sourceCode`, and `customCommand`.
- The guard runs before `postTaskDispatchIntent` can send Cloud Workspace
  dispatch-intent create/status metadata.
- The same guard runs before relay-source status, provider-branch, GitHub App
  token, GitLab token, and claim metadata posts.
- This is defense in depth: callers still build prompt-free payloads, but a
  future accidental field addition fails locally before an HTTP request can
  send prompt/source/workdir content to Convex.
- The local pending-dispatch spool still stores the actual task body only on
  the user's machine so it can hand the prompt to the selected workspace after
  wake/auth is ready.

Regression checks run locally:

```text
desktop/agent: go test . -run 'Test(CloudTaskDispatchIntentPayloadsArePromptFree|PromptFreeConvexMetadataPayloadRejectsSensitiveKeys|PostTaskDispatchIntentRefusesSensitivePayloadBeforeHTTP|ClaimRelaySourceIntentPayloadIsPromptFree|PreviewTaskPlacementDoesNotSendPromptText|CreateTaskDefersCloudPlacement|RetryPendingCloudTaskDispatches|RenderPendingCloudTaskDispatchStatusIsPromptFree)'
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Web-Only Flat Billing Product Surface

The web dashboard billing surface now keeps the customer-facing product story to
Free, Relay Pro, and Cloud Workspace:

- `BillingView` exposes checkout, LemonSqueezy portal, upgrade, and cancel
  paths on web only.
- Cloud Workspace resource rows no longer display raw provider resource ids in
  the subscription billing list.
- The old "box" and "Yaver Cloud" purchase copy in the web Cloud Workspace
  panel was changed to "workspace" and "Cloud Workspace" so the UI matches the
  two-product model.
- The compact launcher now shows standard-credit allowance text instead of a
  generic credit fraction.
- `web/lib/billing-ui-copy.test.ts` locks down the product copy and prevents raw
  provider-resource display from returning in those billing panels.
- The existing mobile boundary test still proves the mobile app does not include
  infrastructure checkout, portal, cancellation, plan-change, or RevenueCat
  purchase APIs.

Regression checks run locally:

```text
web: npx tsx lib/billing-ui-copy.test.ts
web: npx tsc --noEmit --pretty false
mobile: npx tsx src/lib/mobileBillingBoundary.test.mts
mobile: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Subscription Webhook Provisioning Fail-Closed

The LemonSqueezy subscription webhook now treats provider status as an explicit
state instead of defaulting unknown values to `active`:

- `normalizeLemonSqueezySubscriptionStatus` preserves `active`, `past_due`,
  `cancelled`, and other provider statuses as sanitized strings, with missing
  values stored as `unknown`.
- `subscription_created` now schedules Relay Pro or Cloud Workspace provisioning
  only when the normalized status is exactly `active`.
- Non-active or unknown create/update/resume events may update subscription
  metadata, but they do not mint new Hetzner relay/workspace resources.
- The lower-level Cloud Workspace provision action still has its own active
  subscription / owner / start-gate checks, so the webhook gate is an additional
  margin-safety guard rather than the only protection.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsx cloudLifecycle.test.mts
backend/convex: npx tsx cloudMachines.test.mts
backend/convex: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Placement Activation Machine Eligibility

The Cloud Workspace activation route now uses the same placement eligibility
helper as preview/record selection before it can attach, wake, resize, or
provision against an existing machine:

- `/tasks/placement/activate` still requires full user scope and resolves the
  placement for the authenticated user before doing cloud work.
- Existing candidate machines are fetched through `cloudMachines.listForUser`,
  checked against the authenticated `userId`, and filtered with
  `cloudMachineEligibleForPlacement`.
- The shared helper keeps activation limited to Yaver-managed Hetzner rows in
  usable lifecycle states; self-hosted, unsupported-provider, stopped, stopping,
  deleting, and error rows do not reach the wake/resize selector.
- The route still runs `denyNonYaverManagedMachine` immediately before mutating
  the selected machine, preserving defense in depth for public-source safety.

Regression checks run locally:

```text
backend/convex: npx tsx taskPlacement.test.mts
backend/convex: npx tsx http.test.mts
backend/convex: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: MCP Flat Billing Surface

The desktop MCP buyer-side billing tools now match the two paid product model:

- The registered Yaver billing tools are only `yaver_billing_status`,
  `yaver_billing_checkout`, and `yaver_billing_manage`.
- No `yaver_billing_topup` or credit-checkout MCP tool is discoverable from the
  desktop agent tool list.
- Tool descriptions expose Free, Relay Pro, and Cloud Workspace only. Regression
  coverage rejects retired public billing language such as top-up, credit pack,
  prepaid wallet, Cloud Agent, and `$19` in the buyer-facing MCP descriptions.
- `yaver_billing_status` no longer includes wallet USD in its normal MCP output;
  internal wallet/meter fields can still exist in backend diagnostics and
  cost-control code without becoming the customer-facing product model.

Regression checks run locally:

```text
desktop/agent: go test . -run 'Test.*Billing|Test.*MCP.*Billing|Test.*Topup'
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Managed Resource Scope Boundary

The local backend now treats Yaver-managed provider resources as account-level
operations that machine-scoped tokens cannot mutate:

- Billing checkout, billing status, portal, cancel, plan change, legacy
  provision/top-up, Cloud Workspace start/stop/auto-park/dev-activate/dev-adopt,
  dev-deprovision, reconcile, runner authorization, and POST `/machines` all
  require full user scope.
- Machine-scoped tokens receive the shared denial message before account-level
  Cloud Workspace actions can reach subscription, ownership, or provider
  mutation code.
- Start, stop, deprovision, reconcile, and runner authorization keep the
  existing ownership checks and additionally deny non-Yaver-managed machines or
  unsupported providers before any Hetzner-side action can be attempted.
- Self-hosted/open-source runners remain compatible as relay/agent endpoints,
  but public clients cannot use those rows to mutate Yaver-owned Hetzner
  resources.
- The focused static route test is intentionally conservative: if a future
  refactor removes full-scope enforcement from one of these sensitive HTTP
  routes, the backend test fails before deploy.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsc --noEmit --pretty false
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Fast-Wake Product Contract

The local fast-wake/product contract now matches the flat Cloud Workspace model:

- `managedMachineLimit("cloud-workspace")` is pinned to 1, matching the product
  promise of one saved workspace per user rather than a fleet of always-on boxes.
- New Cloud Workspace copy in web/mobile describes the normal path as saved
  state / recovery source / volume-backed wake, not full snapshot restore.
- Legacy snapshot language remains only where it describes legacy rows or
  destructive decommission safety.
- Web and mobile progress labels now show `preparing-volume` as saved workspace
  preparation and `restoring-snapshot` as starting the workspace, so users are
  not told normal wakes are slow snapshot restores.
- Internal lifecycle comments and resume failure messages use “recovery source”
  for the volume/base-image path while preserving legacy snapshot safety.

Regression checks run locally:

```text
backend/convex: npx tsx cloudMachines.test.mts
backend/convex: npx tsx cloudLifecycle.test.mts
backend/convex: npx tsc --noEmit --pretty false
web: npx tsc --noEmit --pretty false
mobile: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Web Cloud Handoff Fallback

The local web client now handles the server-side Cloud Workspace defer response
even when the optimistic placement preview was stale or unavailable:

- `AgentClient.sendTask` and `AgentClient.createTask` decode HTTP 409
  `cloud_workspace_required` into a typed web `CloudWorkspaceRequiredError`
  before falling back to generic error handling.
- Dashboard chat and `VibeCodingView` catch that typed response and create the
  same local pending Cloud Workspace placeholder used by the preview-first path.
- Prompt-bearing fields stay in browser storage; Convex dispatch intents still
  receive only placement ids, target device ids, runner/project labels, status,
  and short reasons.
- Activation blockers such as runner auth, Yaver auth, billing, resize, and
  wake failure render as user-action blocks instead of repeated auto-dispatch.
- Final dispatch still sets `allowLocalFallback:true`, so the assigned workspace
  accepts the locally held task body instead of deferring it again.

Regression checks run locally:

```text
web/lib: npx tsx pending-cloud-dispatch.test.ts
web/lib: npx tsx agent-client.test.ts
web: npx tsc --noEmit --pretty false
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Shared Web Cloud Handoff Helper

The local web fallback path now has one shared conversion from server-side
Cloud Workspace defer responses into prompt-local pending rows:

- `web/lib/pending-cloud-dispatch.ts` owns blocker classification for
  `runner_auth_required`, `yaver_auth_required`, billing, resize, and wake
  failures.
- The same helper saves prompt-bearing params only to browser storage, then
  posts prompt-free dispatch intent metadata to Convex when a web auth token is
  available.
- Dashboard chat and `VibeCodingView` use the helper for stale-preview
  `cloud_workspace_required` responses, reducing the chance that one web surface
  silently clears a user-action block or leaks prompt fields in a future edit.

Regression checks run locally:

```text
web/lib: npx tsx pending-cloud-dispatch.test.ts
web/lib: npx tsx agent-client.test.ts
web: npx tsc --noEmit --pretty false
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Shared Mobile Cloud Handoff Helper

The local mobile fallback path now mirrors the web helper:

- `mobile/src/lib/pendingCloudDispatch.ts` owns the conversion from
  `CloudWorkspaceRequiredError` into an AsyncStorage pending row.
- The helper marks runner auth, Yaver auth, billing, resize, and wake failures
  as user-action blocks, so stale queued updates cannot clear them.
- Prompt-bearing params, images, speech context, and work directories stay in
  local mobile storage until the assigned workspace is reachable.
- Dispatch-intent metadata creation is best-effort and prompt-free; the helper
  lazily loads the mobile task-placement client so Node tests do not import the
  React Native auth stack.
- The task composer uses the helper instead of duplicating blocker and intent
  logic inline.

Regression checks run locally:

```text
mobile/src/lib: npx tsx pendingCloudDispatch.test.mts
mobile/src/lib: npx tsx taskPlacement.test.mts
mobile/src/lib: npx tsx taskRequestBody.test.mts
mobile/src/lib: npx tsx quicCloudWorkspaceRequired.test.mts
mobile: npx tsc --noEmit --pretty false
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Dispatch Intent Ownership Scope

The backend dispatch-intent metadata queue is now scoped by authenticated user
and local task id:

- `taskDispatchIntents` has a `by_user_local_task` index on
  `["userId", "localTaskId"]`.
- Create idempotency uses the authenticated user's row for a local pending task
  id instead of the first globally matching `localTaskId`.
- Status updates that use `localTaskId` also resolve through the authenticated
  user's namespace before applying the existing ownership check.
- This prevents one user's `pending-cloud:*` id from shadowing another user's
  local pending row while keeping the prompt-free metadata contract unchanged.

Regression checks run locally:

```text
backend/convex: npx tsx taskDispatchIntents.test.mts
backend/convex: npx tsx http.test.mts
backend/convex: npx tsc --noEmit --pretty false
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Dispatch Intent Foreign Reference Guard

The backend dispatch-intent create path now validates foreign-key-like metadata
before storing it:

- `placementId` must still belong to the authenticated user.
- `cloudMachineId` now also has to resolve to a Cloud Workspace row owned by the
  authenticated user.
- Foreign or missing references return generic not-found errors, so one account
  cannot create prompt-free handoff metadata pointing at another account's
  workspace.
- The guard affects only Convex metadata rows; it does not call provider APIs or
  mutate Hetzner resources.

Regression checks run locally:

```text
backend/convex: npx tsx taskDispatchIntents.test.mts
backend/convex: npx tsx http.test.mts
backend/convex: npx tsc --noEmit --pretty false
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Flat Billing Boundary

The local backend/web/mobile billing boundary now matches the two paid product
model:

- Web checkout accepts only `relay-pro` or `cloud-workspace`; Free has no
  checkout, and the legacy Cloud Workspace alias emits Cloud Workspace metadata.
- Cloud Workspace checkout metadata now uses the current `standard` workspace
  profile label instead of the legacy `cpu` label.
- Subscription plan normalization maps old Relay aliases to Relay Pro and old
  Cloud aliases to Cloud Workspace, while unrelated labels resolve to Free.
- Public credit-pack checkout routes remain HTTP 410.
- LemonSqueezy `order_created` one-time credit-pack webhooks are ignored even if
  stale variant ids remain configured; monthly subscription allowance grants
  still use the internal idempotent wallet grant path.
- Mobile billing boundary tests continue to reject checkout, portal, cancel,
  and plan-change routes from the app source tree.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsx plans.test.mts
backend/convex: npx tsc --noEmit --pretty false
web: npx tsc --noEmit --pretty false
mobile/src/lib: npx tsx mobileBillingBoundary.test.mts
mobile/src/lib: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Webhook Task Placement Guard

The public shared-secret webhook task trigger now respects Cloud Workspace
placement instead of creating local tasks unconditionally:

- `/webhooks/trigger` previews placement with prompt-free metadata before
  calling `TaskManager`.
- If placement selects a non-local Cloud Workspace, the webhook records a
  pending placement, activates/wakes the workspace path, and does not create a
  local task on the wrong daemon.
- If the workspace accepts the task during the short handoff window, the
  webhook returns the remote task id/status. If auth/wake/billing blocks
  dispatch, it returns structured `action="cloud_workspace_required"` with a
  `pendingTaskId`.
- Convex placement/dispatch metadata remains prompt-free; the actual webhook
  prompt is kept only in the local pending Cloud Workspace dispatch file until
  a target workspace accepts `/tasks`.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestWebhookTrigger|TestMCPCreateTask|TestCreateTaskDefersCloudPlacement|TestCloudTaskDispatchIntentPayloadsArePromptFree|TestPromptFreeConvexMetadataPayload|TestPostTaskDispatchIntent'
desktop/agent: go test . -run '^$'
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: CLI Cloud Workspace Handoff

The local terminal task creators now understand the same Cloud Workspace
handoff contract as web/mobile/MCP:

- A shared desktop helper posts `/tasks`, decodes structured
  `cloud_workspace_required` conflicts, and invokes the existing pending
  Cloud Workspace dispatch/handoff path instead of returning a generic
  status-409 failure.
- `yaver code` one-shot, `yaver ask` single-agent mode, and `yaver voice agent`
  now create tasks through that helper.
- When Cloud Workspace accepts the task, those CLI flows stream output from the
  selected remote workspace daemon using the resolved candidate URL/headers
  instead of assuming the task id belongs to the local daemon.
- Existing terminal remote attach already used `httpCreateTask`, which had the
  Cloud Workspace handoff path; this slice aligns the local one-shot helpers
  with that behavior.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestMCPCreateTask|TestCreateTaskDefersCloudPlacement|TestCloudTaskDispatchIntentPayloadsArePromptFree|TestPromptFreeConvexMetadataPayload|TestPostTaskDispatchIntent|TestBillingToolsRegistered|TestBillingToolDescriptionsStayFlatPlanOnly|TestCode|TestAsk'
desktop/agent: go test . -run '^$'
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: MCP Task Placement Guard

The desktop MCP `create_task` path now uses the same placement contract as
HTTP/web/mobile task creation:

- Before creating a local MCP task, the daemon previews placement with
  prompt-free coarse metadata.
- If placement selects a different Cloud Workspace or a waking workspace, MCP
  records a pending placement, activates/wakes the workspace path, and does not
  create a local task on the wrong machine.
- The actual prompt stays in the local pending Cloud Workspace dispatch file
  until a target workspace accepts `/tasks`; Convex dispatch-intent metadata
  remains prompt-free.
- If the workspace accepts the task during the short handoff window, MCP returns
  the remote task id/status. If auth/wake/billing blocks dispatch, MCP returns
  structured `action="cloud_workspace_required"` with `pendingTaskId` so the
  client can wait or clear the blocker.
- The MCP tool description now states that `create_task` may route/defer to
  Cloud Workspace instead of always running on the connected local daemon.

Regression checks run locally:

```text
desktop/agent: go test . -run 'TestMCPCreateTask|TestCreateTaskDefersCloudPlacement|TestCloudTaskDispatchIntentPayloadsArePromptFree|TestPromptFreeConvexMetadataPayload|TestPostTaskDispatchIntent|TestBillingToolsRegistered|TestBillingToolDescriptionsStayFlatPlanOnly'
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Retired Wallet Capability Surface

The local backend/web surface now keeps legacy wallet/capability controls out
of the normal product model:

- Legacy credit-pack catalog and checkout routes still return `410`, and now
  require full user scope before returning the retired-product response.
- Legacy `/billing/yaver-cloud/balance` and `/billing/yaver-cloud/usage` are
  gated behind full user scope. They remain only as compatibility/account reads
  for allowance panels and diagnostics, not machine-scoped agent APIs.
- Buyer-side `/billing/status` no longer reads or returns raw wallet cents; it
  returns product id, subscription state, included Cloud Workspace allowance,
  and managed-inference state.
- The old `/managed/services`, `/managed/cockpit`, and `/managed/burn`
  à-la-carte capability cockpit routes now return `410` without reading wallet
  data or mutating `userSettings.managedServices`.
- The web `CapabilityShelf` component is now a no-op so the dashboard does not
  call retired wallet-backed managed-service endpoints. BillingView remains the
  customer-facing surface for Free, Relay Pro, and Cloud Workspace.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsc --noEmit --pretty false
web: npx tsc --noEmit --pretty false
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.

## Implementation Evidence: Cost-Safe Idle Parking

The local backend now keeps idle compute parking separate from wallet-meter
simulation:

- `/crons/run {name:"cloudIdleSweep"}` schedules `cloudLifecycle.idleSweep`
  with `dryRun:false` when auto-off is enabled. `YAVER_CLOUD_METER_LIVE=false`
  may keep the ledger simulated during preview, but it no longer turns idle
  parking into a no-op for real servers.
- `YAVER_CLOUD_IDLE_DISABLE=true` remains the explicit operator emergency brake.
- `pauseMachine` still fails closed if `HCLOUD_TOKEN` is absent, so local tests
  and source-only environments do not touch provider state.
- `cloudMachines.setAutoPark` now rejects `enabled:false` at the mutation layer,
  matching the public HTTP validator. Legacy OFF rows can be turned ON, but a
  customer-facing path cannot persist a new OFF setting.
- The cloud-init container test now freezes `Date.now()` for byte-identical
  default-tier comparisons, removing a timestamp flake unrelated to lifecycle
  behavior.

Regression checks run locally:

```text
backend/convex: npx tsx http.test.mts
backend/convex: npx tsx cloudLifecycle.test.mts
backend/convex: npx tsx cloudMachines.test.mts
backend/convex: npx tsc --noEmit --pretty false
repo root: git diff --check
```

No live Hetzner, Convex production, LemonSqueezy, server, snapshot, or billing
state was touched for this slice.
