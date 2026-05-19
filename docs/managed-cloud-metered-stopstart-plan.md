# Managed-Cloud Metered Prepaid + Stop/Start — Audit & Plan

> Status: PLAN (no code yet). 2026-05-19. Code is source of truth —
> file:line are a 2026-05-19 snapshot; re-grep before building.
> Scope decisions (user): isolated modules on main + provisioner seam
> (NO edits to the parallel session's cloudMachines.ts/subscriptions.ts/
> Dockerfile.yaver-cloud); owner-gated + DRY-RUN, no real Hetzner spend.

## Cost model (the why)
16GB box (Hermes need) = Hetzner CAX31 ~€13/mo cap, ~€0.022/hr. 2×
markup. Phone-vibing dev who stops when idle: ~€1.5 (light) / ~€3.5
(typical) / ~€6 (active) / ~€9.5 (heavy) per month vs ~€26 always-on.
Stopping ≈ 85% saving for the typical user; Yaver keeps 100% margin.
+ user's own BYOK GLM key (separate). Estimates — verify live rates.

## Audit (file:line)
- Convex `cloudMachines` schema.ts:843-942: `status` is v.string()
  (provisioning|active|grace|stopping|stopped|error); snapshot fields
  exist; NO paused/resuming/suspended; NO credit/usage tables. Gate
  `subscriptions.canProvisionManaged` subscriptions.ts:87-107 (active
  sub OR ownerAllowlist.isOwner, env, fail-closed). Owner routes
  http.ts:3275-3461. Privacy ALLOWS usage counters (runnerUsage/
  dailyTaskCounts/guestUsage precedent).
- Agent: snapshot create fail-closed cloud_deploy.go:941-958; delete;
  list. **GAP: no recreate-from-snapshot — hetznerCreateServer:877
  hardcodes ubuntu-22.04, no imageID.** No stop/suspend/resume verb.
  Reuse dry-run: host_recycle.go:45-65 + cloud_provisioner_robot.go:
  53-127 (+ accountField vault token). No first-boot readiness hook.
- Mobile ManagedCloudCard.tsx (infra.tsx:604): box + decommission,
  gated by server `cloudPreviewOwner` (cosmetic hide). NO start/stop
  (web or mobile), NO balance UI. subscription.ts lacks
  prepaidBalance/stoppedAt.

## Seam (no parallel-file edits)
New `cloudLifecycle.ts` + appended tables `prepaidCredits`,
`creditUsage`. `status` is v.string() ⇒ paused/resuming/suspended are
new string values via the existing internal patch boundary (NO
cloudMachines.ts logic edit; read-only internal queries only). Prepaid
gate lives in NEW wrapper routes, NOT inside cloudMachines.provision.
Agent stop/start additive in cloud_deploy.go/ops_cloud.go (NOT the
parallel-refactor mcp_tools.go/httpserver.go core).

## Phases
- P0 schema tables + cloudLifecycle.ts (balance/deduct/meter, dry-run-
  simulable) + privacy test.
- P1 agent: imageID param on hetznerCreateServer; hetznerStopServer
  (reuse fail-closed snapshot) + hetznerStartServer (from-snapshot);
  cloud_stop/cloud_start ops verbs, recycle-style confirm/dry-run,
  owner-env-gated; httptest unit tests (no mocks).
- P2 cloudLifecycle pause/resume state machine + usage-meter cron +
  auto-stop-before-zero + two-part min reserve (transition + ≥1mo
  stopped).
- P3 owner-gated routes /billing/yaver-cloud/{stop,start,balance,
  topup-dev}. ⚠ http.ts actively modified by parallel session — the
  collision point; minimal delimited additive block, rebase-careful.
- P4 ✅ agent first-boot readiness self-check + vault-openable probe:
  `/yaver-agent/audit.readiness` returns ready / needs-reauth with
  reasons [vault|runner|git].
- P5 ✅ mobile ManagedCloudCard start/stop + balance; extended
  subscription.ts; behind unchanged cloudPreviewOwner gate. Backend
  `/billing/yaver-cloud/{start,stop}` exists as owner-gated dry-run
  state transitions until the P1 provider verbs land.
- P6 ✅ owner-dev prepaid top-up + balance HTTP routes wired to
  cloudLifecycle ledger. Real LemonSqueezy prepaid checkout remains
  post-YC.

## Collision/risk register
- DO NOT EDIT: cloudMachines.ts, subscriptions.ts,
  Dockerfile.yaver-cloud (parallel session).
- http.ts = real collision (modified on their branch) — P3 sharp edge;
  isolated main worktree + careful rebase (same as command-cards).
- schema.ts shared — append-only.
- Real-spend safety: every Hetzner stop/start behind owner-env +
  confirm; default dry-run; never auto-provision/auto-snapshot a real
  box; prepaid deduction simulated in dry-run.
