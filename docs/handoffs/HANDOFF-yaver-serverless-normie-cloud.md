# Yaver Serverless Normie Cloud Handoff

Status: active implementation handoff for Claude Code / coding agents.
Date: 2026-06-20.
Read first: `CLAUDE.md`, `AGENTS.md`, then grep the files named below. Code wins over this document.

## Product Thesis

Yaver's mobile sandbox should feel like a normal person made an app on their
phone, paid for hosting when they needed it, and could let friends try it before
TestFlight or store review.

The default lifecycle is:

```text
phone sandbox
  -> Yaver Serverless Lite bundle
  -> self-hosted yaver serve OR Yaver managed cloud
  -> friend preview in Yaver app
  -> optional TestFlight / Play / web deploy later
```

Do not make Convex the import/export target for sandbox projects. Convex can
remain Yaver's control plane for identity, subscriptions, device discovery, and
entitlements. User app runtime data moves through Yaver Serverless Lite:

```text
yaver.serverless.yaml + schema.yaml + app.yaml + auth.yaml + data/app.sqlite
```

The normie mental model is "my app", not "a sandbox, a database vendor, a CI
pipeline, a Git provider, and a release train".

## Current Code Anchors

Serverless bundle and import/export:

- `desktop/agent/phone_backend.go`
  - `PhoneProject.ManagedGit`
  - `PhoneCreateSpec.ManagedGit`
  - `phoneServerlessManifest`
  - `collectPhoneExportFiles`
  - `ExportPhoneProjectWithOptions`
  - `ImportPhoneProject`
- `desktop/agent/phone_backend_http.go`
  - `registerPhoneRoutes`
  - `handlePhoneExport`
  - `handlePhoneReceive`
- `desktop/agent/phone_data_http.go`
  - `/data/<slug>/<table>` runtime API
- `docs/yaver-serverless-lite.md`
  - lower-level runtime/bundle design

Managed Git:

- `desktop/agent/managed_git.go`
  - `EnsureManagedGitForProject`
  - `ManagedGitCheckpoint`
  - `ManagedGitBackup`
  - `ManagedGitBackupToTarget`
  - `ManagedGitRestoreBundle`
  - `ManagedGitMirrorToProvider`
  - `/managed-git/*` HTTP routes
- `desktop/agent/managed_git_test.go`
  - lifecycle proof for managed repo, checkpoint, backup, restore, external backup
- `desktop/agent/httpserver.go`
  - `s.registerManagedGitRoutes(mux)`

Mobile sandbox orchestration:

- `mobile/src/lib/phoneProjects.ts`
  - `PhoneProject.managedGit`
  - `PhoneCreateSpec.managedGit`
  - `PhoneShare`
  - `createPhoneProject`
  - `createPhoneProjectAt`
  - `pushPhoneProject`
  - `deployPhoneProjectRuntime`
  - `managedGitMirrorAt`
- `mobile/app/phone-projects.tsx`
  - create flow, start target, managed Git defaulting, managed cloud push/mirror
- `mobile/app/phone-project/[slug].tsx`
  - project detail, deploy, managed Git backup/restore/mirror UI
- `mobile/src/lib/auth.ts`
  - device-local Git, GLM, self-hosted, Yaver cloud tokens

Friend preview:

- `desktop/agent/phone_share.go`
  - `PhoneShare`
  - `CreatePhoneShare`
  - `ResolvePhoneShare`
- `desktop/agent/phone_backend_http.go`
  - `/phone/projects/share`
  - `/phone/projects/join`
- `mobile/src/lib/phoneProjects.ts`
  - `sharePhoneProject`
  - `joinPhoneShare`

## Bundle Contract

The canonical export is `.yaver.tgz`; `.zip` remains an OS/coding-agent twin.

Expected files:

```text
<slug>/
  yaver.serverless.yaml
  .yaver/config.yaml
  .yaver/project.yaml
  schema.yaml
  auth.yaml
  seed.json
  app.yaml
  data/app.sqlite
  local.db
  schema.sql
  schema.postgres.sql
  README.md
  AGENTS.md
```

Rules:

- `data/app.sqlite` is canonical runtime state.
- `local.db` is compatibility only.
- `yaver.serverless.yaml` must not contain secrets, hostnames, IPs, absolute paths, or deploy tokens.
- Import prefers `data/app.sqlite`, falls back to `local.db`.
- Export/import must preserve runtime rows.
- Managed cloud and self-hosted receive the same bundle through `/phone/projects/receive`.

## User Journey

1. User opens Yaver mobile and describes an app.
2. GLM/OpenAI only drafts schema/app plan; it does not become hidden state.
3. Phone creates a local sandbox project with SQLite-backed tables and a simple app plan.
4. Managed Git is enabled by default for serious projects:
   - private local bare repo under `~/.yaver/managed-git/repos`
   - automatic checkpoint after create
   - checkpoint before deploy
   - bundle backup before destructive overwrite
5. User taps deploy:
   - free path: push to their paired Mac / PC / self-hosted `yaver serve`
   - paid path: push to Yaver managed cloud after entitlement check
6. Target receives the same `.yaver.tgz`, materializes SQLite, serves `/data/<slug>/<table>`, and returns project metadata.
7. Mobile stores a binding from source slug to target base URL.
8. User taps share:
   - host mints short code
   - friend installs Yaver
   - friend enters code or opens invite
   - friend's Yaver app fetches the bundle from the host and runs the app against host `dataUrl`
9. Later, owner can export back, mirror to GitHub/GitLab, or ship through TestFlight/Play.

## Payments and Entitlement Boundary

Mobile must not initiate prohibited in-app purchases for managed cloud. The
paid managed-cloud flow should be:

- Web/CLI handles checkout and subscription management.
- Mobile shows neutral entitlement state.
- Mobile can retry deploy after the account already has an active managed cloud machine.
- Agent/cloud receive endpoint may return `402 Payment Required`.
- `PhonePushPaymentRequired` exists in `mobile/src/lib/phoneProjects.ts`; mobile UI must not open checkout URLs in store builds.

The "normie pays money and it works" product is real, but store-compliant:

```text
Buy/manage plan on web -> mobile sees entitlement -> deploy button works.
```

## Managed Git Role

Yaver Git is not a developer-facing burden. It is the app's safety rail:

- every generated app has a private history
- every deploy can checkpoint
- every managed-cloud overwrite can backup first
- every "AI broke it" moment can restore
- every external export can mirror to GitHub/GitLab when the user asks

Important product language:

- User sees "History", "Backup", "Restore", "Mirror".
- Advanced users can see Git details.
- Normies should not need to know branches, remotes, or PATs.

Implementation requirements:

- Create flow should pass `managedGit: { enabled: true, visibility: "private" }` when the user chooses serious/local/cloud start modes.
- Import/receive should preserve `.yaver/managed-git.yaml` only if it is safe for the receiving placement. Do not preserve source absolute paths as authoritative on the target.
- Managed cloud should checkpoint and backup before overwrite.
- Friend preview shares must not grant write access to managed Git.
- External mirror must redact provider tokens from errors.

## Friend Preview Before TestFlight

Friend preview is distribution inside Yaver, not public app-store distribution.

Contract:

```json
{
  "code": "ABCD23",
  "slug": "todo-app",
  "runtime": "yaver-serverless-lite",
  "dataUrl": "/data/todo-app",
  "bundleUrl": "/phone/projects/export?slug=todo-app&format=zip&includeData=1",
  "expiresAt": "..."
}
```

Behavior:

- Share records live on the host agent under `~/.yaver/phone-projects/_shares`.
- Codes expire.
- Friend gets read/preview behavior by default.
- Friend should run the app in the Yaver container/Hermes path.
- Friend does not receive owner deploy tokens, managed Git write access, or cloud credentials.
- For a hosted app, `dataUrl` is resolved against the host origin.
- Legacy `hostedConvexUrl` may exist for old clients but must not be the new contract.

Needed next:

- Add a client-side join path that imports/runs the received bundle with `runtime: yaver-serverless-lite`.
- Add explicit read-only token/scoping for friend preview data APIs.
- Add "stop sharing" and active share list.
- Add QR/deep-link for code entry.

## Web UI Role

The phone remains the primary normie control surface, but web UI should cover:

- plan/subscription management
- managed cloud machine state
- project list
- logs/errors
- backups/history
- domain setup
- share links/codes
- TestFlight/Play handoff status

Web UI must not become the only way to operate the product. The target state is:

```text
phone can create, edit, deploy, share, restore, and inspect enough logs
web can administer billing, domains, deeper logs, and release workflows
```

## Managed Cloud Target

MVP target is an operator-owned Hetzner server running the same agent receive
path as self-hosted:

```text
TLS/proxy -> yaver serve -> ~/.yaver/phone-projects/<slug>/data/app.sqlite
```

No real IPs, hostnames, or tokens in docs or commits.

Public managed cloud must add before it is sold broadly:

- per-user deploy auth
- per-project receive token
- quota checks before import
- bundle size caps
- backup before overwrite
- tenant isolation stronger than a shared owner home
- audit rows in Yaver control plane without app data
- billing/entitlement gate
- no Convex storage of user app contents

## Internal Beta Shared Runtime — 100 Normie Users

Status on 2026-06-20: root implementation is now in the repo. Treat this as
the first internal/friend beta path, not the later paid dedicated-cloud SKU.

Product shape:

- One generic Yaver-owned Hetzner server serves the first internal beta users.
- Users are normies, not developers: hide infra, hosts, guest grants, SSH,
  git remotes, and API keys. The UI says "Beta" and shows the project/workspace.
- Included AI is GLM/z.ai quality through Yaver Gateway + `opencode`.
- Claude Code and Codex are valid only as BYO OAuth: each user signs into
  their own account, and the CLI runs under their tenant Unix user. Never pool
  or share the owner's Claude/Codex OAuth.
- Isolation is per user: `/srv/yaver/tenants/<tenant>/home`, distinct `yv-*`
  Unix user, `0700`, isolated `HOME`, `CLAUDE_CONFIG_DIR`, and `CODEX_HOME`.
- The hidden infra grant can allow `["opencode","claude","codex"]`, but it
  must keep `requireIsolation:true` and `useHostApiKeys:false`.

Relevant code anchors:

- `desktop/agent/tenant_runtime.go`: tenant root/home/env and `sudo -u yv-*`
  launch wrapper for Claude/Codex.
- `desktop/agent/tasks.go`: isolated guest Claude/Codex tasks bypass shared
  auth readiness checks and run through tenant runtime.
- `desktop/agent/runner_auth_browser_http.go`: runner OAuth sessions and
  credential import are scoped by tenant.
- `desktop/agent/helper.go`: root helper creates only canonical tenant roots
  (`/home/yv-*` or `/srv/yaver/tenants/<id>`).
- `desktop/agent/install_privilege.go`: operator systemd/helper units include
  `/srv/yaver/tenants`, scoped sudo for tenant drop-in only, no broad
  `NOPASSWD:ALL`.
- `backend/convex/betaAccess.ts`: owner-gated hidden beta grant, free
  inference wallet grant, gateway caps, visible Beta allowance.
- `backend/convex/cloudMachines.ts`: new VM cloud-init installs the operator
  systemd service rather than the old `yaver-agent.service`.

Operational setup for an existing generic Hetzner beta server:

```bash
# On the box, after installing the new yaver binary:
sudo /usr/local/bin/yaver serve --install-systemd-system --operator
sudo systemctl restart yaver-helper yaver
systemctl status yaver-helper yaver --no-pager
```

Do not write real IPs, hostnames, tokens, or customer identifiers into this
handoff or commits.

### 100-user burn model

Use this as an operator budget, not pricing copy. Inputs are intentionally
simple and conservative.

Current repo COGS basis:

- Shared beta server: `cpx62` modeled at about `$152.99/mo` in
  `backend/convex/cloudLifecycle.ts` after checking `hcloud describe cpx62`
  on 2026-06-20. This is the current EU 16 vCPU / 32 GB regular-performance
  SKU; old `cpx51` is deprecated/unavailable for new EU boxes.
- Compute hourly equivalent: `$152.99 / 730 = $0.2096/hour`.
- For a single always-on shared beta server, server burn is about
  `$153/mo total`, not per user. At 100 users that is `$1.53/user/mo`.
- Add snapshots/backups/logs/egress buffer: budget `$25-$50/mo` for this
  first shared box unless usage proves higher. Planning server baseline:
  `$180-$205/mo`.

Included inference budget:

- `betaAccess.ts` default free wallet grant is `$5/user`.
- For 100 users, worst-case inference exposure is `$500` if every user fully
  consumes their grant.
- Daily/hourly/request caps make that exposure gradual, not an instant account
  drain.
- Practical expected use for friends/internal normies is likely far lower:
  model scenarios as `$0.50`, `$1`, `$2`, and `$5` consumed per user.

100-user monthly burn scenarios:

| Scenario | Server | Inference | Total burn | Burn/user |
|---|---:|---:|---:|---:|
| Light beta | ~$180 incl. buffer | $50 | ~$230 | ~$2.30 |
| Normal beta | ~$180 incl. buffer | $100 | ~$280 | ~$2.80 |
| Heavy beta | ~$180 incl. buffer | $200 | ~$380 | ~$3.80 |
| Max capped grant | ~$180 incl. buffer | $500 | ~$680 | ~$6.80 |

Currency is mixed in the source systems; for planning, treating `€1 ~= $1`
is close enough. Re-run with exact FX and current Hetzner/z.ai prices before
paid launch.

### ROI shape

The shared beta runtime is attractive because fixed compute amortizes well.
The bad business is per-user always-on dedicated servers; the good business is:

1. Shared beta server for early normies and friends.
2. Free GLM/opencode lane with hard inference grants.
3. BYO OAuth for Claude/Codex, so Yaver does not carry those token costs.
4. Later paid dedicated Hetzner per user, but only with auto up/down and
   included-hour limits.

If 100 beta users produce:

- 10 paid users at `$19/mo`: revenue `$190/mo`; server baseline covered, but
  normal inference burn is not fully covered.
- 15 paid users at `$19/mo`: revenue `$285/mo`; normal beta roughly covered.
- 20 paid users at `$19/mo`: revenue `$380/mo`; heavy beta roughly covered.
- 10 paid users at `$29/mo`: revenue `$290/mo`; normal beta roughly covered.
- 14 paid users at `$29/mo`: revenue `$406/mo`; heavy beta roughly covered.

For the later dedicated-user SKU, never include an always-on `cpx51` at a low
consumer price. Use an included-hours model:

- Normie dedicated workspace: smaller SKU first if the workload allows it.
- Auto down after idle; snapshot/delete or pause so idle cost is storage only.
- Include a small number of active hours, then require prepaid overage.
- Keep GLM free grant separate from compute included hours.

Rule of thumb:

- Shared beta CAC target: keep cash burn around `$3/user/mo` normal case.
- Hard cap: about `$7/user/mo` if everyone spends the full inference grant.
- Paid conversion needs roughly `15% at $19/mo` or `10% at $29/mo` to make
  the first 100-user cohort viable before labor/support costs.

## Claude Code Tickets

### Ticket 1: finish serverless share/join on mobile

Goal: friend can install Yaver, enter a code, receive `bundleUrl`, and run the app against `dataUrl`.

Acceptance:

- `PhoneShare.runtime === "yaver-serverless-lite"`
- `PhoneShare.dataUrl` is used by the runtime adapter
- `hostedConvexUrl` is ignored unless running an older legacy bundle
- share/join tests updated
- no owner tokens in share payload

### Ticket 2: managed Git default in create/import/deploy

Goal: normie-created apps get history without Git setup.

Acceptance:

- create wizard defaults managed Git on for cloud/dev-machine modes
- local-only drafts can stay off until first deploy or explicit "History on"
- deploy calls checkpoint before export
- receive overwrite creates backup first when managed Git is enabled
- restore UI works from phone project detail

### Ticket 3: import/export manifest hardening

Goal: target can trust a Yaver Serverless Lite bundle without inheriting unsafe local state.

Acceptance:

- import ignores source absolute paths in managed-git metadata
- import rewrites target-local `WorkDir` and bare repo paths
- import rejects path traversal in bundle entries
- export excludes secrets by default
- tests cover canonical `data/app.sqlite`, legacy `local.db`, and `.zip`

### Ticket 4: managed cloud entitlement receive

Goal: paid users deploy from phone to Yaver managed cloud; unpaid users get a neutral retry state.

Acceptance:

- cloud `/phone/projects/receive` checks entitlement
- returns `402` with machine-readable reason when absent
- mobile does not open checkout in store build
- web dashboard can complete purchase
- after purchase, same mobile deploy succeeds

### Ticket 5: friend preview read scope

Goal: friends can try the app without becoming owners.

Acceptance:

- share code resolves to scoped preview token or server-side share session
- `/data/<slug>` honors preview scope
- default preview is read-only unless owner explicitly allows write
- owner can revoke share
- friend cannot access `/managed-git/*`, `/phone/projects/receive`, or owner vault

### Ticket 6: web UI parity for normie operations

Goal: user can operate cloud app from browser as well as phone.

Acceptance:

- dashboard shows serverless projects
- project page shows data API status, logs, backups, share codes, domains
- deploy/import/export controls use the same bundle contract
- no docs/UI copy says Convex is the sandbox runtime

## Tests To Keep Green

Run from `desktop/agent`:

```bash
go test -count=1 . -run 'TestPhoneShare_CreateResolveExpire|TestFullSandboxLoop_E2E|TestExportImportRoundtrip|TestImportAcceptsCanonicalDataSQLiteWithoutLegacyLocalDB|TestPhoneProjectExportReceiveBetweenAgents|TestManagedGitCreateCheckpointAndBackup|TestCreateTodoPhoneProjectWithManagedGitLifecycle'
```

Mobile-side checks:

```bash
cd mobile
npx tsc --noEmit
```

Browser/mobile e2e:

- open `/phone-projects`
- create app from prompt
- enable/verify managed Git
- deploy to self-hosted
- deploy to managed cloud when entitlement/token exists
- share with code
- join from another Yaver app/session
- verify friend preview loads rows from `/data/<slug>`

## Non-Goals

- Do not wrap Convex as the sandbox runtime.
- Do not build a Convex UI clone.
- Do not require GitHub before the user can deploy.
- Do not require TestFlight before friends can try an app.
- Do not put app SQLite data into Yaver's Convex control plane.
- Do not expose owner tokens through share payloads.

## Working Tree Warning

This repo often has multiple active threads. Before editing, run:

```bash
git status --short
rg -n 'yaver-serverless-lite|data/app.sqlite|managedGit|PhoneShare|hostedConvexUrl' desktop mobile web docs
```

Treat uncommitted unrelated files as user-owned. Do not revert them.
