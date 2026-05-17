# Managed-Cloud Host Lifecycle — Audit + Plan

> **Status: Phase A + money-safety landed in code 2026-05-17 (not
> prod-deployed). Code is the source of truth — re-grep; this drifts.**
>
> **Two distinct products — do not conflate:**
> - **BYO / self-hosted** (agent): the *user's own* vault-backed
>   Hetzner token (`accountField(ProviderHetzner)`). User pays Hetzner
>   directly → **no LemonSqueezy gate**. `provisionHetzner` /
>   `mcpCloudDestroy` in `desktop/agent/cloud_provisioners.go`
>   (commit `b71201c1`).
> - **Managed cloud** (Convex): *Yaver's platform* Hetzner token, in
>   Convex env only (`process.env.HCLOUD_TOKEN` — never repo/tables/
>   payload; secret audit 2026-05-17 found zero token literals in the
>   tree). Any user, but **NEVER provisioned without an active
>   LemonSqueezy subscription** — fail-closed gate
>   (`subscriptions.isActive`) in `provisionRelay.provision` +
>   `cloudMachines.provision`; signed webhook is primary proof,
>   gate is defense-in-depth. Cancel **and** expiry now tear the box
>   down (grace-snapshot first). Commit `96c29a5c`; **not deployed —
>   prod is live billing, needs explicit owner go-ahead.**
>
> Original goal stands: decommission + re-provision from web **and
> mobile** UI (web → relay → agent → hcloud for BYO; LemonSqueezy
> webhook → Convex → platform hcloud for managed). Token never leaves
> its trust boundary (agent vault for BYO, Convex env for managed).

## 1. The seam is sound, the parts mostly exist

Web is relay-only and Convex stores no infra (privacy contract). The
only viable path is **web → relay → agent HTTP → agent reads
`yaver vault` → Hetzner API**. The agent is the API client; the token
never touches Convex or the browser. This is architecturally clean and
matches `feedback_p2p_only` + `feedback_no_github_ci_executor`
(user-machine executor first, CI fallback only).

What already exists:

- `desktop/agent/cloud_deploy.go:843 hetznerCreateServer()` +
  `:922 hetznerDeleteServer()` — real hcloud API calls, **working**,
  but **not wired** into the provisioner registry (deliberately
  dropped 2026-04-28; re-enable hint at
  `tier_c_audit_pitr_ha.go:254-255`). Shared with the dormant
  managed-cloud/LemonSqueezy flow — don't break it.
- `cloud_deploy.go:869 cloudBootstrapScript()` — cloud-init user-data.
- `desktop/agent/cloud_provisioners.go:32 provisionerRegistry()` —
  Supabase + Vercel only; `mcpCloudProvision()` dispatches to it.
- `mcp_tools.go:1803 cloud_provision` MCP tool + `httpserver.go:9535`
  router; `ops_cloud.go:37-89` ops verbs `provision/scale/destroy`
  (thin wrappers; `destroy` requires `confirm=true`).
- Bootstrap: `scripts/deploy-yaver-agent-hetzner.sh:158-217` (npm
  install + systemd `yaver serve`).
- Headless auth: `primary_cmd.go:109 yaver primary auth`.
- Device register: `backend/convex/schema.ts:201 devices` +
  `:343 pendingDeviceClaims`; web `lib/use-devices.ts:626
  usePendingClaims()` already lists/claims bootstrap-pending boxes.
- Set primary: `primary_cmd.go:832 yaver primary set`.
- Token privacy: `vault.go:26` (never leaves agent),
  `convex_privacy_test.go` forbids `token/secret/...` in any Convex
  payload.

## 2. Gaps (EXISTS / PARTIAL / MISSING)

| Step | Status |
|---|---|
| Create new box | PARTIAL — `hetznerCreateServer` exists, unregistered |
| Delete old box | PARTIAL — `hetznerDeleteServer` exists; no `cloud_destroy` tool |
| Bootstrap agent on new box | EXISTS (`deploy-yaver-agent-hetzner.sh`) |
| Headless auth | EXISTS (`yaver primary auth`) |
| Device register | EXISTS (`/devices/bootstrap-pending` + claim) |
| Set primary | EXISTS (`yaver primary set`) |
| Lifecycle orchestration (old→new→cutover→reap) | MISSING |
| Web + mobile UI trigger | MISSING (no managed-cloud surface in either) |
| Token handling | OK by design (vault, agent-side only) |

**Biggest gap:** there is no orchestration that sequences
snapshot→create→bootstrap→claim→primary→verify→delete-old, and no web
surface to start it. The primitives are all present.

## 3. Hard constraints baked into the plan

- **Never self-destruct.** The orchestrating agent must run on a
  device whose `deviceId != target box deviceId`. Decommissioning the
  box you're running on mid-flight is the obvious footgun — hard guard,
  refuse otherwise. (For the current `yaver-test-ephemeral`, run the
  recycle from kivanc's Mac or another owned device.)
- **Snapshot before delete, always** (CLAUDE.md Hetzner rules:
  snapshot ≈ €0.10/mo vs €6.49/mo running; reproducible from
  `ci/remote/bootstrap.sh`). Delete only after the new box is verified
  healthy.
- **No secret in cloud-init.** user-data carries a one-time
  device-code / pending-claim handshake only; the user claims the new
  box from the web UI (reuse `pendingDeviceClaims`). A long-lived auth
  token must never be embedded in user-data (privacy + it's plaintext
  on the Hetzner console).
- **No token in the web trigger or Convex.** Web sends
  `{verb, plan, region}`; agent reads `HCLOUD_TOKEN` from
  `yaver vault` (project-scoped). `convex_privacy_test` must stay
  green.
- **Cost safety.** Tag every yaver-created server with a label;
  orphan-reaper lists+deletes failed-create servers so a crashed
  provision never leaves a paid box running.
- **Rollback.** If the new box fails health, keep the old one, delete
  nothing, surface the failure (mirrors
  `feedback_visible_failure_over_silent_retry`).

## 4. Phased plan

### Phase A — re-enable Hetzner provisioner + `cloud_destroy` (task #9)
Re-register Hetzner in `provisionerRegistry()`; add the missing
`cloud_destroy` MCP tool → `hetznerDeleteServer` (mandatory `confirm`,
snapshot-first). `ProvisionResult` returns IP/ID/name. Token strictly
from vault. Fake-hcloud httptest tests; privacy test green; managed-
cloud/LemonSqueezy path untouched.

### Phase B — host-recycle orchestration (task #10, needs A)
`yaver host recycle <deviceId|alias>` + an ops verb:
snapshot old → `hetznerCreateServer` (cloud-init: npm install +
`yaver serve`) → poll healthy → new box self-registers
`/devices/bootstrap-pending` → claim + `yaver primary set` → verify
reachable as same user → snapshot+delete old. Self-destruct guard,
idempotent, rollback, orphan label/reaper. Dry-run mode.

### Phase C — web **and mobile** managed-cloud surface (task #11, needs B)
Same agent contract (`/ops` verb `provision`/`destroy` or
`/managed-cloud/recycle`) wired into **both** clients — the agent
endpoint is the single source of truth; each UI is a thin trigger +
progress view. Zero infra IP/token in any Convex payload; both
clients reach the agent over their normal transport (web = relay
only; mobile = LAN beacon → Convex-known IP → relay, per CLAUDE.md
connection strategy).

- **Web:** `web/app/dashboard/DevicesView.tsx` — "Provision new box"
  + per-card "Recycle / Decommission" (cloud devices only). Reuse
  `web/lib/use-devices.ts:626 usePendingClaims()` for the new box
  appearing.
- **Mobile:** `mobile/app/(tabs)/devices.tsx` +
  `mobile/src/components/DeviceDetailsModal.tsx` — add the same
  "Recycle / Decommission" action to the per-device modal (next to
  PingRow) and a "Provision new box" entry; reuse the mobile
  pending-claim path already wired in `devices.tsx`/`settings.tsx`
  (`/devices/bootstrap-pending`). `mobile/src/context/DeviceContext.tsx`
  is the data seam.

Both surfaces: confirm dialog + cost note on destroy; progress
streamed (create→bootstrap→claim→primary→old-snapshotted-deleted);
self-destruct guard is enforced agent-side (Phase B) so neither UI
can footgun it. Build once on the agent, wire twice.

## 5. Immediate bridge (today, no new code)

To remove the current box and stand up a new one **now**, the existing
CI scripts run locally (CLAUDE.md "Local deploy first"):
`ci/hcloud/snapshot-server.sh` → `hcloud server delete
yaver-test-ephemeral` → `ci/hcloud/create-server.sh` (uses
`HETZNER_TEST_SNAPSHOT_ID`) → `scripts/deploy-yaver-agent-hetzner.sh`
→ `yaver primary set`. Run from kivanc's Mac (not from the box being
deleted). The plan above is the productized, web-UI version of exactly
this sequence.

## 6. Precise anchors (re-grep before use)

- hcloud API: `cloud_deploy.go:843/922/869`
- registry: `cloud_provisioners.go:32`; re-enable note
  `tier_c_audit_pitr_ha.go:254`
- MCP/ops: `mcp_tools.go:1803`, `httpserver.go:9535`,
  `ops_cloud.go:37-89`
- bootstrap/auth/primary: `scripts/deploy-yaver-agent-hetzner.sh`,
  `primary_cmd.go:109/832`
- device register: `backend/convex/schema.ts:201/343`,
  `web/lib/use-devices.ts:626`
- mobile UI seam: `mobile/app/(tabs)/devices.tsx`,
  `mobile/src/components/DeviceDetailsModal.tsx`,
  `mobile/src/context/DeviceContext.tsx`
- privacy: `vault.go:26`, `convex_privacy_test.go`
- CI scripts: `ci/hcloud/{create,delete,snapshot}-server.sh`,
  `ci/remote/bootstrap.sh`
