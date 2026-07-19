# Autorun Fleet Synchronization Audit

Date: 2026-07-18

Audience: Claude Code / future coding agent implementing the next pass.

## Goal

Make Yaver autorun reliable in multi-runner, multi-machine, multi-surface use:

- schedule by real resources: SSD/free disk, CPU/load, RAM, runner OAuth/readiness, deploy credentials, OS/toolchain, and cost lane;
- schedule by dependencies: source path, build target, runner seat, landing to `main`, deploy target, and main-build state;
- let autorunners work on isolated branches/worktrees, but land through `main` only;
- rebase/merge/push safely, abort cleanly on real conflicts, and never lose another runner's work;
- coalesce deploys: many autoruns may finish, but only one deploy should run for a converged state;
- deploy from a pinned `main` SHA, not from a branch or moving worktree;
- surface the same truth over MCP, web, mobile, tablet, tvOS, visionOS, Wear/watch, AR/Mentra, and ops/CLI.

This repo already has much more than the older docs imply. The remaining work is to wire the pieces into one authoritative scheduler/barrier instead of leaving some of them as prompts, local-only maps, or standalone helper verbs.

## Read First

Docs are context only. Verify against code before changing behavior:

- `CLAUDE.md`
- `docs/architecture/AI_ARCH.md`
- `docs/architecture/REMOTE_WORKER.md`
- this file

Important drift found while auditing:

- `desktop/agent/autorun_remote.go` says the repo convention is `github` over HTTPS, but `CLAUDE.md` says the real remote is `github` over SSH. Code should not care about URL scheme, but comments/docs should be corrected when touched.
- `docs/architecture/REMOTE_WORKER.md` is now stale on several items: `mcp_remote_proxy.go`, remote MCP proxy tests, and many `device_id` paths already exist.

## Evidence Matrix

This is the current-state proof map from the 2026-07-18 audit. Treat it as a starting point, not as a substitute for re-grepping before edits.

| Requirement | Current evidence | Status |
|---|---|---|
| Isolated autorun branches/worktrees | `autorunPrepareWorkspace`, `autorunWorkspaceFor`, `autorunBranchName`, and closed-loop tests in `desktop/agent/autorun.go` / `autorun_closedloop_test.go` | Partial: local/daemon autorun path is solid, but restart adoption is incomplete |
| Always land on `main` | `autorunLandOntoMain` and ops `git_land` both serialize, rebase, fast-forward merge, and push `main` | Partial: no fleet `land/main` lease; conflicts abort rather than repair |
| Git-aware push race handling | Push-race retry in `autorunLandOntoMain`; `opsGitLandHandler` retries | Good for per-process plus remote push races |
| Scope/dependency exclusion | `autorun_coordination.go` admits by normalized source areas; `autorun_leases.go` models path/build/seat/land | Partial: dependencies beyond path/build target are not modeled |
| Fleet-wide leases | `autorun_leases_git.go` and `autorun_leases_fleet.go` implement/test git-ref CAS leases | Not wired into actual autorun loop |
| Resource-aware autorun | `autorun_resources.go` checks local disk/RAM/load; `agent_mesh.go` scores machine capabilities | Partial: no fleet scheduler applies it to autorun placement |
| Runner OAuth/deploy capability awareness | `detectMachineCapabilities`, `RunBuildDoctor`, `/fleet/deploy-options`, `/deploy/capabilities` | Partial: readiness is surfaced but not a hard scheduler input everywhere |
| Deploy coalescing | `ship.go` freezes/drains/deploys once; `autorun_gate.go` dead-man lease | Partial: several surfaces still route straight to `/deploy/ship` or prompts |
| Deploy from pinned `main` SHA | `shipPinHead` records `PinnedSHA` | Incomplete: `RunDeployAll` shells from the current worktree, not an enforced pinned checkout |
| UI visibility | `web/components/dashboard/AutorunsView.tsx`, `mobile/app/autoruns.tsx`, deploy capability views, tvOS/visionOS dashboards | Partial: local autoruns/leases visible; no unified fleet/ship status everywhere |
| MCP visibility | Generic `ops` tool can call `autorun_*`, `git_land`, `ship`; direct `deploy_all` exists | Partial: no first-class MCP tools for fleet plan/status/barrier |
| Safe stop/wrapup | `autorun_wrapup.go` discovers tmux loops and preserves dirty work on wrapup branches | Good locally; needs restart/fleet indexing |
| Quota-aware deploy | `autorun_placement.go` accepts `quotaExhausted` input | Mostly missing: no real quota source wired into `ship` |

## Use-Case Verdicts

### Use case: two independent autoruns on one box

Example: one runner edits `web/**`, another edits `desktop/agent/**`.

Current result:

- Admission allows disjoint scopes.
- Local leases can hold separate source areas and runner seats.
- During build, the first run releases its runner seat and keeps its source area/build target.

Missing:

- Build target contention is only advisory in the loop. If a build lease is unavailable, `autorunLoop` logs "gating anyway."
- No resource reservation prevents both runs from pushing RAM/disk over a platform-specific floor.

Target result:

- Admit both only if source/build/resource constraints fit.
- If build target or machine pressure is contended, park the later build phase with a visible reason.

### Use case: two autoruns on different remote boxes touch the same target

Example: remote Mac and local Mac both try to build/deploy iOS.

Current result:

- Git-ref fleet lease code exists and is tested.
- Actual autorun loop uses only local `autorunLeases`, so both boxes can proceed independently.
- Landing push retry protects `main` from losing committed work, but it does not prevent wasted build/test/deploy work.

Missing:

- Wire `fleetLeaseCoordinator` into edit/build/land.
- Publish/fetch lease namespace before claiming scarce targets like `build/ios`, `build/tvos`, `build/android`, and `land/main`.

Target result:

- One box owns `build/ios`; the other waits, validates another safe target, or reports why it skipped.

### Use case: an autorun finishes while another run still owns the same deploy platform

Example: run A finishes a small mobile UI fix; run B is still editing `mobile/ios/**`.

Current result:

- `ship` can coalesce deploys if a human/surface invokes it after freezing.
- There is no task-level "deploy target owner" dependency that lets run A decide to skip deploy and test run B.

Missing:

- A dependency graph with `deploys`, `validates`, `produces`, and `consumes`.
- A bus/status event shape for "skipped deploy because `<run>` owns `<target>`."

Target result:

- Run A lands to `main`, sees `deploy/testflight-ios` is still owned by run B, skips deploy, and optionally runs a safe validation against B's committed branch or a clean checkout.

### Use case: ship from phone/watch/AR while autoruns run remotely

Current result:

- Ops `ship` is designed for this and can freeze remote machines.
- Watch has a deploy intent, but it is a thin phone-confirmed path.
- AR/Mentra handles notifications, not control.
- Native iOS deploy pane calls `/deploy/ship` directly.

Missing:

- Route all human-facing deploy intents to ops `ship`.
- Show `ship_status` and drain/freeze/lease state on phone/tablet/web, with read-only summaries on tvOS/visionOS and notifications on Wear/AR.

Target result:

- Every surface says the same thing: which machines are frozen, which runs parked, which pinned SHA is shipping, which targets will deploy, which work lands next ship.

### Use case: deploy must use the exact `main` SHA

Current result:

- `ship` pins a SHA before repair/detect/deploy.
- `RunDeployAll` ignores that SHA and runs commands in the current repo root.

Missing:

- A deploy worktree at the pinned SHA.
- Per-step verification that `HEAD` equals the pinned SHA.
- Report/log fields for `pinnedSha`, deploy worktree path, and source branch.

Target result:

- Deploy cannot accidentally include a later autorun commit or omit the pinned state.

## What Already Exists

### Isolated Autorun Work

Implemented mainly in:

- `desktop/agent/autorun.go`
- `desktop/agent/autorun_cmd.go`
- `desktop/agent/autorun_ops.go`
- `desktop/agent/autorun_coordination.go`
- `desktop/agent/autorun_wrapup.go`

Current behavior:

- `yaver autorun` prepares a dedicated worktree under `~/.yaver/worktrees/...`.
- Branch names are deterministic, e.g. `autorun/<task>/<runner>`.
- Task paths must live inside the source workdir.
- The doer runner is told not to commit/push; Yaver owns gate, commit, push, and final landing.
- Scope admission exists before a daemon-started run begins. Overlapping `--scope` values are rejected as `slot_busy` or `area_owned`.
- Scope validation still runs after every kick and rolls back out-of-scope edits into a diagnostic stash.
- Gate-passing iterations are signed commits with explicit path adds.
- A final commit is marked with `final autorun commit`.
- `autorun wrapup` and `autorun stop` discover tmux sessions outside the in-process manager, preserve dirty work on `autorun/wrapup/...`, and push that branch without touching `main`.

### Landing To Main

Implemented in:

- `desktop/agent/autorun.go`
- `desktop/agent/ops_git_land.go`

Current behavior:

- Successful autoruns release their worktree by landing onto `main`.
- Landing is serialized per process with `autorunLandMu`.
- Landing does `fetch`, `pull --rebase`, `merge --ff-only <autorun branch>`, and non-forced `push <remote> main`.
- Push races are retried up to 4 times.
- Rebase failures abort cleanly.
- `ops git_land` exposes the same intent outside autorun, with conflict reporting and `git_land_state`.
- `autorun_status` enriches sessions with landing state so a finished run is distinguishable from a landed/pushed run.

### Local Resource Awareness

Implemented in:

- `desktop/agent/autorun_resources.go`
- `desktop/agent/autorun_cmd.go`
- `desktop/agent/console_machines.go`
- `desktop/agent/capabilities_snapshot.go`
- `desktop/agent/infra_http.go`

Current behavior:

- Every iteration probes disk, total RAM, CPU count, and 1-minute load before spending a runner turn.
- Free disk below 3 GB triggers `go clean -cache`; if still below floor, the run stops as `insufficient machine resources`.
- Total RAM below 2 GB refuses to kick a build/test loop.
- High load backs off one interval instead of failing.
- Machine capability snapshots include hardware, runner readiness, Docker, iOS/TestFlight, Android/Play, local LLM, ghost, machine sniff, `lowPower`, and `maxTaskSlots`.
- `agent_mesh.go` scores node placement by machine capability, runner readiness, OS/toolchain hints, hardware, profile tags, sharing policy, and cost lane.

### Typed Leases

Implemented in:

- `desktop/agent/autorun_leases.go`
- `desktop/agent/autorun_leases_git.go`
- `desktop/agent/autorun_leases_fleet.go`

Current model:

- `path/<area>`: one writer per source area.
- `build/<target>`: one build per toolchain target.
- `seat/<runner>`: one active conversation per runner seat.
- `land/<base>`: one landing flow per base branch.
- Local leases have TTL and all-or-nothing acquisition.
- Git-ref leases use `refs/yaver/lease/...`, `git update-ref` CAS, TTL records, fetch/publish, and privacy-safe metadata.
- Fleet coordinator can combine local and git tiers.

Important limitation:

- The actual autorun session path uses the local `autorunLeases` singleton directly, not `fleetLeaseCoordinator`.
- Build-lease contention during the gate is currently best-effort: it logs and gates anyway.
- `land/<base>` is modeled but not used by `autorunLandOntoMain` or `opsGitLandHandler`.

### Ship Barrier

Implemented in:

- `desktop/agent/ship.go`
- `desktop/agent/ship_fanout.go`
- `desktop/agent/ship_ops.go`
- `desktop/agent/ship_cmd.go`
- `desktop/agent/autorun_gate.go`
- `desktop/agent/ship_targets.go`

Current behavior:

- `yaver ship` / ops `ship` is the closest thing to the requested global protocol.
- It sends `toparla` prompts, freezes local/remote autoruns, drains to iteration boundaries, pins `HEAD`, optionally repairs red `main`, detects changed deploy targets, deploys once, marks a watermark, thaws, and sends notification.
- Remote freezes use `autorun_pause_all` with a dead-man lease.
- Any unreachable freeze target aborts the ship.
- Drain timeout is non-fatal because deploy pins a SHA; late autorun work lands next ship.

Important limitation:

- The pinned SHA is recorded, but `RunDeployAll` still runs shell commands from the current working tree. It does not enforce checkout at the pinned SHA for every target.
- `RunDeployAll` is sequential but still has a simplistic preflight (`go build ./...` only from `desktop/agent`) and default target names that do not fully match the `ship` schema wording.
- Deploy placement exists separately in `autorun_placement.go`, but `ship` does not use it to choose deploy host/route/quota.

### Deploy Capability Surfaces

Implemented in:

- `desktop/agent/fleet_deploy_options.go`
- `desktop/agent/deploy_capabilities.go`
- `desktop/agent/deploy_run.go`
- `desktop/agent/deploy_all.go`
- `mobile/ios/Yaver/YaverDeployPane.swift`
- `mobile/app/deploy-tokens.tsx`
- `web/components/dashboard/VibeCodingView.tsx`
- `web/components/dashboard/DeployCapabilitiesView.tsx`

Current behavior:

- `/fleet/deploy-options` fans out build doctors to reachable machines and reports per-target deploy capability.
- iOS deploy pane can pick a target and machine, then POST `/deploy/ship`.
- Web/mobile expose deploy readiness and capability hints.
- `/deploy/ship` supports remote machine targeting and SSE.

Important limitation:

- The iOS deploy pane uses `/deploy/ship`, not the newer `ship` barrier. It can still trigger a deploy while autoruns elsewhere keep working unless the caller separately uses the barrier.
- Web deploy quick actions generate prompts to agents rather than issuing the barrier as a structured operation.

### MCP / Ops

Relevant code:

- `desktop/agent/mcp_tools.go`
- `desktop/agent/mcp_remote_proxy.go`
- `desktop/agent/ops_http.go`
- `desktop/agent/ops_git_land.go`
- `desktop/agent/ship_ops.go`
- `desktop/agent/autorun_ops.go`

Current behavior:

- MCP has an `ops` grand-tool, so all ops verbs including `autorun_*`, `git_land`, and `ship` are reachable.
- Many direct MCP tools support `device_id` or remote routing.
- Secret-sensitive ops are blocked across machines by `ops_http.go`.

Important limitation:

- MCP does not expose a single first-class `autorun_fleet_status` / `ship_barrier` tool with stable schema and guidance. It relies on generic `ops`.
- Tool descriptions exist, but MCP clients are not forced to use the barrier before deploy-capable tools.

### UI Surfaces

Current state by surface:

- Web: rich dashboard, machine capability hints, deploy prompt generation, agent graph, and `AutorunsView` with local sessions plus local lease holds.
- Mobile/phone: task UI has runner/model/device support, multi-target wizard state, deploy buttons, auth recovery, wake, tablet layout, and `mobile/app/autoruns.tsx` with local sessions plus local lease holds.
- Tablet: same React Native tasks screen has tablet split-pane handling.
- Native iOS shake overlay: deploy machine picker exists, but it calls `/deploy/ship`, not barrier `ship`.
- tvOS: native SwiftUI runtime control room shows machine status and runner sessions via ops.
- visionOS: native dashboard/session surfaces show machine status, runner sessions, and reload.
- Wear OS/watch: thin voice intent/notification/wake path. Deploy intent exists in protocol but is intentionally confirm-gated through phone.
- AR/Mentra: notification/focus surface for runner auth and Hermes reload; not a control plane.

Missing common truth:

- No surface shows the fleet-wide lease table, ship sessions, cross-machine drains, pinned SHA, queued deploy target, and "lands next ship" work in one consistent vocabulary.

## Critical Gaps

### 1. Fleet leases are implemented but not the active autorun coordinator

`fleetLeaseCoordinator` is tested but not wired into `autorunSessions.start`, `autorunLoop`, `autorunLandOntoMain`, or `opsGitLandHandler`.

Required change:

- Create an autorun lease facade used by all autorun/ship/git-land paths.
- Use local-only mode when no git remote exists, but expose degraded status.
- Acquire source and seat at edit admission through the facade.
- Acquire build target through the facade before gate. For heavyweight build/deploy targets, do not "gate anyway" on contention; wait/park with a reason.
- Acquire `land/main` through the facade before landing in both autorun and `git_land`.
- Renew all held fleet leases during long runner turns, long builds, and ship.

### 2. Scheduling is still local/advisory for resources

Autorun measures local disk/RAM/load, but no fleet-wide scheduler allocates work to the best machine based on:

- free disk and expected artifact size;
- CPU saturation and concurrent build count;
- RAM pressure;
- runner OAuth readiness and per-runner concurrency caps;
- deploy credential presence;
- target OS/toolchain;
- cost/provider;
- current leases.

Required change:

- Add `autorun_plan` or extend `autorun_start` to return a placement decision before starting.
- Feed `MachineInfo.Capabilities`, live host metrics, `autorun_leases`, and `autorunPlacementPlan` into one scorer.
- Distinguish hard constraints from preferences:
  - hard: OS/toolchain, credentials, runner ready, source area lease, deploy quota, online/reachable;
  - soft: local, low cost, fast disk, high memory, current load, profile tags.
- Return rejected machines with concrete reasons.

### 3. Deploy coalescing is split between `ship` and `/deploy/ship`

`ship` is the correct barrier. `/deploy/ship` remains a lower-level execution endpoint and several UI surfaces still call or prompt toward it directly.

Required change:

- Treat `ship` as the only user-facing "deploy after autoruns" operation.
- Keep `/deploy/ship` as the primitive executor used by `RunDeployAll` and specialized deploy calls.
- Update mobile native deploy pane, web deploy actions, watch deploy intent, voice deploy intent, and MCP descriptions to call ops `ship` by default.
- Only allow direct `/deploy/ship` when the caller explicitly selects "deploy now without fleet barrier" or is deploying an isolated preview/sandbox target.

### 4. Deploy from pinned SHA is not enforced enough

`ship` pins a SHA, but `RunDeployAll` shells in the current repo path.

Required change:

- Add a `PinnedSHA` or `Commit` field to `DeployAllRequest`.
- Before each deploy step, verify `git rev-parse HEAD == pinnedSha` in the deploy workdir or run from a temporary clean worktree checked out at the pinned SHA.
- Prefer a temporary deploy worktree:
  - `git worktree add ~/.yaver/deploy-worktrees/<ship-id> <sha>`
  - run all deploy steps there;
  - remove it after deploy.
- Never deploy from an autorun branch.
- Write the pinned SHA and target list into deploy logs/reports.

Minimum implementation detail:

- `ship.go` should call `RunDeployAll(ctx, DeployAllRequest{Only: plan.Targets, PinnedSHA: sha})`.
- `RunDeployAll` should resolve `repoRoot`, create a deploy worktree, run `deployPreflight` there, call `DefaultDeploySteps(deployRoot)`, and remove only that exact deploy worktree after inspecting it.
- The deploy report must include `PinnedSHA` and `DeployRoot`.

### 5. Target naming is inconsistent

`shipSchema` mentions `web-cloudflare`, `cli-npm`, `testflight-ios`, `playstore-android`. Other layers use target IDs like `cloudflare`, `testflight`, `playstore`, `convex`.

Required change:

- Define one canonical deploy target enum.
- Keep aliases for backward compatibility, but normalize at the boundary.
- Make `ship`, `/fleet/deploy-options`, `/deploy/capabilities`, `/deploy/ship`, `RunDeployAll`, mobile Swift, web TS, and docs share the same names.

Observed target vocabularies:

- `ship_targets.go`: `convex`, `web-cloudflare`, `cli-npm`, `testflight-ios`, `playstore-android`.
- `/fleet/deploy-options`: accepts target IDs derived from `buildTargets`, including `testflight`, `playstore`, `cloudflare`, `convex`, etc.
- Native iOS pane: hardcodes `testflight` and `playstore`.
- `RunDeployAll`: step names are `convex`, `web-cloudflare`, `cli-npm`, `testflight-ios`, `playstore-android`.

Recommended canonical shape:

- Internal deploy step IDs: keep `convex`, `web-cloudflare`, `cli-npm`, `testflight-ios`, `playstore-android`.
- Public aliases: accept `cloudflare -> web-cloudflare`, `web -> web-cloudflare`, `testflight -> testflight-ios`, `ios -> testflight-ios`, `playstore -> playstore-android`, `android -> playstore-android`.
- Normalize once in an `normalizeDeployTargetID` helper used by every boundary.

### 6. Dependency graph is path-based, not task-graph aware

Today dependencies are inferred from scopes/build targets. There is no explicit graph for "runner A should wait for runner B's generated API/schema change" or "autorunner finished coding; sibling still coding same platform; run sibling's tests instead."

Required change:

- Add a task graph/admission model:
  - `produces`: paths, packages, deploy targets, generated artifacts;
  - `consumes`: paths, packages, generated artifacts;
  - `validates`: test/build commands it can run for another task;
  - `deploys`: target IDs.
- If a deploy target is already owned by a live coding run, a finished sibling should:
  - skip deploy;
  - optionally run validation against the sibling's branch/worktree if safe;
  - publish a bus event/comment: "I skipped deploy because `<run>` owns `<target>`; ran `<tests>` instead."
- Cross-worktree validation must use a clean checkout or the target run's committed branch, not uncommitted files.

### 7. Conflict resolution is intentionally conservative, not automatic

Current landing does rebase/push retry and aborts real conflicts. It does not auto-resolve semantic conflicts.

Required change:

- Keep this conservative behavior.
- Add an autorun conflict-repair mode only after a clean abort:
  - preserve conflicted branch/worktree;
  - write a task file naming conflicted paths and both branches;
  - start a bounded repair autorun with scope limited to conflicted files;
  - require gate green before retrying `git_land`.
- Do not silently choose one side or create merge commits behind the caller's back.

### 8. Secret and runner OAuth readiness is only partially modeled

Machine capabilities know whether runners are ready and deploy doctors know secrets, but fleet scheduling does not treat "runner OAuth/deploy capability" as a first-class lease/resource.

Required change:

- Add capability dimensions:
  - `runnerSeats`: runner ID, ready/auth source, max sessions, current sessions;
  - `deployCredentials`: target ID, present, source, expires/unknown, local-only;
  - `quotas`: TestFlight uploads remaining/unknown, CI budget/unknown, provider limits.
- Keep secret values local. Only export boolean/status/reason.

### 9. Persistence/recovery is incomplete

Autorun sessions and freezes are in-memory. This is acceptable for local loops but weak for "perfect" fleet coordination.

Required change:

- Persist minimal autorun session metadata under `~/.yaver/autorun/sessions/`.
- On daemon restart, rediscover tmux/worktrees and reconstruct status enough to stop/wrapup/land.
- Persist ship sessions enough for `ship_status` to survive coordinator restarts.
- Keep freeze TTL fail-open behavior.

### 10. Surfaces are not all wired to the same control plane

Required UI/API additions:

- Add ops verbs:
  - `autorun_fleet_status`: machines, runs, leases, drain state, resource pressure, landing state.
  - `autorun_plan`: dry-run scheduler answer for a task/gate/scopes/targets.
  - `ship_plan`: dry-run barrier plan including freeze targets, deploy host, pinned SHA candidate, target detection, quota, and blockers.
- Add MCP first-class tools that wrap those ops verbs with clear schemas.
- Web dashboard: extend the existing Autoruns view into an Autorun Fleet panel showing cross-machine leases, running/parked/draining runs, landing state, and active ship.
- Mobile phone/tablet: extend the existing Autoruns screen and Tasks/Infra summaries with fleet status; deploy buttons should launch `ship`.
- Native iOS deploy pane: replace `/deploy/ship` call with ops `ship`, show ship ID, and provide status polling.
- tvOS/visionOS: show active ship and runner drain status read-only; allow confirm-gated ship start if UX supports it.
- Wear/watch: keep as intent-only; route `deploy` intent to phone confirmation, then ops `ship`.
- AR/Mentra: notification-only is fine; add events for ship started/failed/ok and runner auth/blockers.

### 11. `ship` does not automatically know every remote autorun host

`ship` freezes `local` plus the explicitly supplied `FreezeMachines`. That is correct for safety today, because freezing an unknown/unreachable machine should not be guessed. It is not "perfect autorun" yet.

Required change:

- Add a discovery mode that reads `autorun_runs` across cached/known machines, finds live autoruns, and proposes freeze targets.
- Require explicit confirmation or a `freezeAllKnownAutorunHosts` flag before freezing all known hosts.
- If a known live autorun host is unreachable, abort the ship unless the user explicitly excludes it and accepts that its work lands next ship.

### 12. `ship` repair is too repo-specific

Current `shipRepair` uses:

- task: `tasks/ship-repair.md` or `docs/tasks/ship-repair.md`;
- gate: `go build ./...`;
- scope: `desktop/agent/`.

That is acceptable for this repo's current emergency path but not a generic multi-platform autorun barrier.

Required change:

- Make repair gate and scopes derive from failed preflight/target plan.
- For web/mobile/backend repairs, choose target-specific gates.
- Keep the explicit task-file requirement for any repair that edits code.

## Desired End-State Protocol

### Starting Autorun

1. Resolve workdir, task, requested runner/master, scopes, gate, deploy target hints.
2. Build an `autorun_plan`:
   - owned source areas;
   - build targets;
   - deploy targets;
   - candidate machines;
   - hard blockers;
   - chosen machine/runner/master;
   - leases needed per phase.
3. Acquire edit leases fleet-wide: source + seat.
4. Create or reuse isolated worktree from latest remote `main`.
5. Run iterations.
6. On gate/build phase, release seat, acquire source + build target, renew leases.
7. Commit only gate-passing changes with explicit pathspecs.
8. Push autorun branch if requested.
9. On completion, acquire `land/main`, rebase/merge/push to `main`, retry push races, abort cleanly on conflicts.
10. Release all leases and publish final status.

### Shipping

1. User/surface calls ops `ship`, not `/deploy/ship`.
2. Ship determines freeze targets from explicit input plus known machines with live autoruns.
3. Send `toparla`.
4. Freeze every target with a TTL lease.
5. Wait for drain up to timeout.
6. Fetch/rebase/sync `main`; pin SHA.
7. If `main` is red, run bounded repair from explicit task file.
8. Detect deploy targets since last successful ship, or use explicit targets.
9. Check deploy placement/credentials/quota for every target.
10. Run deploy once from a clean worktree at pinned SHA.
11. Mark ship watermark only after deploy success.
12. Thaw on every exit path.
13. Publish events to all surfaces.

## Implementation Order

1. Wire fleet leases into autorun and git landing.
2. Normalize deploy target IDs and aliases.
3. Make `RunDeployAll` accept and enforce a pinned SHA via a deploy worktree.
4. Route all user-facing deploy surfaces through ops `ship`.
5. Add `autorun_fleet_status`, `autorun_plan`, and `ship_plan`.
6. Extend machine capability snapshots with live pressure, runner seat counts, deploy credentials, and quotas.
7. Add task-graph metadata for dependency-aware scheduling and "run sibling tests instead of duplicate deploy."
8. Persist autorun/ship session metadata across daemon restart.
9. Add UI panels/status cards across web, mobile/tablet, tvOS, visionOS, watch, and AR notification surfaces.

## Tests To Add

- Two machines acquire the same `build/ios` lease: one wins, the other waits or reports blocked; no "gate anyway" for heavy targets.
- Two machines land different branches to `main`: both land with retry, no force push, no merge commit.
- Rebase conflict during land aborts cleanly and leaves status with conflicted paths.
- `ship` with one unreachable freeze target aborts and thaws already-frozen machines.
- `ship` deploys from a pinned clean worktree even if `main` moves after pinning.
- Mobile/iOS/web deploy actions call ops `ship` by default.
- A finished autorun skips deploy when another live run owns the same deploy target and records the reason.
- Secret-sensitive ops/MCP calls remain local-only across remote routing.
- Daemon restart can rediscover autorun tmux sessions/worktrees and still stop/wrapup them.

## Non-Negotiables

- Never force-push `main`.
- Never silently discard a sibling run's committed or uncommitted work.
- Never deploy from an autorun branch.
- Never deploy a red `main`.
- Never run N deploys because N autoruns finished.
- Never move secret values across machines; move only readiness booleans and reasons.
- Never leave a remote freeze without a TTL.
- Never hide degraded coordination. If fleet leases, capability probes, or quota checks are unavailable, surfaces must say so.
