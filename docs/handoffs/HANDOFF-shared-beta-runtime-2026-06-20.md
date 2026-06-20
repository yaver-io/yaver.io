# Shared Beta Runtime Handoff

Status: handoff for Claude Code / coding agents.  
Date: 2026-06-20.  
Repo commit already pushed: `c72d87aaf Prepare shared beta tenant runtime`.

## Read First

Read `AGENTS.md` and `CLAUDE.md` first. Code is the source of truth; grep before
trusting this document.

Do not deploy anything from this handoff until the owner explicitly says to.
Do not provision or resize Hetzner servers from this handoff. Do not write real
IPs, hostnames, tokens, relay labels, customer identifiers, or provider secrets
into commits or docs.

## Current State

Already done:

- Commit `c72d87aaf` was pushed to `main`.
- Convex was deployed once after that commit.
- No live Hetzner server was provisioned or resized by this handoff.
- A later local-only cost/SKU experiment was not committed, pushed, or
  deployed.

The goal is a shared beta runtime for internal/friend beta users, not a public
paid dedicated-cloud product yet.

## Product Shape

This is for normie beta users, not developers.

The user should see:

- a simple Beta workspace
- a project/app surface
- working included AI
- optionally "connect Claude/Codex" if they bring their own account

The user should not see:

- Hetzner
- hostnames/IPs
- guest grants
- SSH
- Unix users
- Git remotes/PATs
- owner identity
- provider API keys

## Runtime Model

One generic Yaver-owned Hetzner server serves the first internal beta users.

Each beta user gets:

- Unix user: `yv-*`
- Tenant root: `/srv/yaver/tenants/<tenant>/`
- Home: `/srv/yaver/tenants/<tenant>/home`
- Isolated Claude config: `<home>/.claude`
- Isolated Codex config: `<home>/.codex`
- Mode `0700` tenant directories

The hidden infra grant can allow:

```json
["opencode", "claude", "codex"]
```

But it must keep:

```json
{
  "requireIsolation": true,
  "useHostApiKeys": false
}
```

## Runner Policy

Included free lane:

- `opencode`
- GLM/z.ai through Yaver Gateway
- bounded by free wallet grant + gateway daily/hourly/request caps
- owner GLM key never reaches the tenant process

BYO OAuth lanes:

- Claude Code / `claude`
- Codex / `codex`
- user signs into their own Claude/Codex account
- credentials live only in that user's tenant home
- never share or pool the owner's Claude/Codex OAuth

This is the key security line: Yaver can provide runtime and capped GLM
inference, but Claude/Codex accounts are user-owned OAuth sessions.

## Code Anchors

Tenant runtime:

- `desktop/agent/tenant_runtime.go`
- `desktop/agent/tenant_runtime_test.go`
- `desktop/agent/tasks.go`
- `desktop/agent/runner_auth_browser_http.go`

Privileged helper and operator systemd:

- `desktop/agent/helper.go`
- `desktop/agent/helper_test.go`
- `desktop/agent/install_privilege.go`
- `desktop/agent/install_privilege_test.go`

Beta grant and caps:

- `backend/convex/betaAccess.ts`
- `backend/convex/access.ts`
- `backend/convex/schema.ts`
- `backend/convex/cloudLifecycle.ts`
- `backend/convex/http.ts`

Tenant project/git safety:

- `desktop/agent/beta_tenant.go`
- `desktop/agent/beta_scrub.go`
- `desktop/agent/beta_scrub_test.go`
- `desktop/agent/beta_broker.go`
- `desktop/agent/beta_broker_test.go`
- `desktop/agent/beta_managed_git.go`

Provisioning:

- `backend/convex/cloudMachines.ts`
- `backend/convex/cloudMachines.test.mts`

Broader context:

- `beta-invisible-infra-share-design.md`
- `docs/handoffs/HANDOFF-yaver-serverless-normie-cloud.md`

## Existing Server Setup Later

Do not run this until explicitly approved.

When the owner is ready to update the generic beta Hetzner server, the shape is:

```bash
sudo /usr/local/bin/yaver serve --install-systemd-system --operator
sudo systemctl restart yaver-helper yaver
systemctl status yaver-helper yaver --no-pager
```

Before running anything live, verify:

- the binary on the box contains commit `c72d87aaf` or later
- `/srv/yaver/tenants` exists and is root-owned
- `yaver-helper.service` is running
- `yaver.service` is running as the non-root `yaver` user
- the service unit `ReadWritePaths` includes `/srv/yaver/tenants`
- no legacy broad `NOPASSWD:ALL` sudoers file is active for `yaver`
- no Docker/shared-root runtime is being used for Claude/Codex OAuth tenants

## Tests Already Run

Known passing before handoff:

```bash
cd desktop/agent
go test . -run 'Test(Beta|beta|TenantRuntime|Sudoers|Operator|SelfHost|EnsureYaver|Hardened|WriteSudoers|HandleDispatch|ValidShell|SanitizeTenantEnv|RunnerBrowserAuth|RunnerAuth|RunnerScope|GuestHeader|GuestScope|GuestShareLinuxStack|CreateTask.*Guest|Guest.*Policy)'
```

```bash
cd backend
npx convex typecheck
```

```bash
git diff --cached --check
```

The broad `go test .` package run was not completed earlier because it ran
long; use focused tests unless you have time to investigate the full suite.

## 100-User Burn Model

This is planning math, not billing logic.

For one shared generic beta server:

- The fixed server cost is shared by all users.
- The variable cost is mostly inference.
- Claude/Codex costs are not Yaver costs if users bring their own OAuth.

Conservative current planning basis:

- 1 shared 16 vCPU / 32 GB Hetzner-class server: budget about `$150-$205/mo`
  including a buffer.
- 100 users means server burn around `$1.50-$2.05/user/mo`.
- Included GLM/z.ai free grant default in code is `$5/user`.
- Worst-case inference exposure for 100 users is `$500` if all grants are fully
  consumed.

Scenarios for 100 users:

| Scenario | Server | Inference | Total burn | Burn/user |
|---|---:|---:|---:|---:|
| Light beta | ~$180 | $50 | ~$230 | ~$2.30 |
| Normal beta | ~$180 | $100 | ~$280 | ~$2.80 |
| Heavy beta | ~$180 | $200 | ~$380 | ~$3.80 |
| Max capped grant | ~$180 | $500 | ~$680 | ~$6.80 |

If using an older cheaper existing server, actual burn may be lower. Do not
resize upward just to match this table.

## ROI

The shared beta server works because compute is amortized. The dangerous model
is always-on dedicated servers for every low-price normie user.

Break-even style estimates for 100 beta users:

- 10 paid users at `$19/mo` = `$190/mo`: covers server baseline, not normal
  inference.
- 15 paid users at `$19/mo` = `$285/mo`: roughly covers normal beta.
- 20 paid users at `$19/mo` = `$380/mo`: roughly covers heavy beta.
- 10 paid users at `$29/mo` = `$290/mo`: roughly covers normal beta.
- 14 paid users at `$29/mo` = `$406/mo`: roughly covers heavy beta.

Rule of thumb:

- Normal shared beta CAC target: about `$3/user/mo`.
- Hard cap if all grants are used: about `$7/user/mo`.
- A viable first 100-user cohort needs roughly `15% at $19/mo` or `10% at
  $29/mo`, before labor/support costs.

## Later Dedicated Runtime

The later product can give users their own dedicated Hetzner server, but only
with cost controls:

- auto up/down
- idle snapshot/delete or pause
- small included active-hour allowance
- prepaid overage after included hours
- GLM free grant separate from compute hours
- no always-on dedicated `16 vCPU / 32 GB` box included at a cheap consumer
  subscription price

For normies, default to the smallest SKU that keeps the app experience smooth.
Only upgrade when there is measured need.

## Next Work

Recommended next implementation tasks:

1. Add a visible owner-only beta admin flow to seed/revoke beta users without
   hand-running Convex actions.
2. Verify the deployed generic Hetzner beta server with a disposable test
   tenant before inviting friends.
3. Add a smoke test that starts two tenant runtimes and verifies different
   `HOME`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, cwd confinement, and auth session
   IDs.
4. Add runtime observability for per-tenant process count, disk usage, and
   active runner.
5. Add a hard per-tenant disk quota before inviting more than a tiny friends
   cohort.
6. Add owner-visible burn dashboard: server fixed cost, inference grant used,
   active beta users, and projected month-end burn.

## Relay Signalling (built 2026-06-20, NOT deployed)

`relay/beta_signal.go` + tests. The free relay is the always-on entry; an
owner-side pool controller (not yet built) provisions/reaps the box.

- `POST /beta/wake` — a beta client signals demand. **Cost gate:** the relay does
  NOT trust the caller; it forwards the bearer to Convex `/gateway/authorize`
  (already deployed). Only a scoped beta token resolves to a userId → flips the
  shared pool `down → waking`. No valid beta token → 401, pool stays `down`, no
  provision, **no spend**. Per-user 8s cooldown + single shared phase debounce a
  burst to ONE box. (`TestBetaWake_AttackerCannotWake` pins this.)
- `GET /beta/state` — clients poll until `phase=="up"`; controller polls to
  decide provision/reap. `POST /beta/state` is admin-token-gated (controller only).
- The relay holds only `{phase,lastWakeAt,lastActivityAt,boxAddr,wakeCount}` —
  control signalling, never task data (same class as presence/bandwidth).

## Day-One Sizing (NOT the 100-user model)

Do NOT start at the 16 vCPU / 32 GB 100-user box. Day one = a handful of friends:

- Start with the **smallest** SKU that runs opencode + 1-2 tenants: `cax11`
  (2 vCPU / 4 GB arm64, ~€3.29/mo) or `cx22` (2 vCPU / 4 GB, ~€3.79/mo).
- The 100-user burn table above is **far-future planning only**.

### Start small → grow bigger WITHOUT data loss

Two ways, both preserve `/srv/yaver/tenants`:

1. **In-place resize** — `hcloud server change-type <name> <bigger> --keep-disk`
   (CPU/RAM up, disk unchanged → **reversible**, ~1-2 min power-cycle, same IP,
   data intact). A disk upgrade is one-way; avoid until needed.
2. **Snapshot → recreate-bigger** — `snapshot-server.sh` then
   `create-server.sh` with a larger type. The snapshot is a full disk image, so
   tenant data carries over. This is the same primitive the scale-to-zero reaper
   uses, so growth is free of new mechanism.

Rule: only grow on **measured** need (per-tenant disk/CPU observability, task #4).

## Stop Conditions

Stop and ask the owner before:

- deploying Convex again
- deploying web/mobile
- provisioning, resizing, deleting, snapshotting, or restarting live Hetzner
  infrastructure
- changing default Hetzner SKU or price assumptions in live billing code
- increasing beta grant amounts
- enabling Claude/Codex without tenant runtime isolation
- exposing hidden grants in guest UI

