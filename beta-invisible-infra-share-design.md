# Beta — Invisible Owner-Infra Share (design)

Date: 2026-06-20
Soft-launch (Phase 1) design. Companion to `pricing-metered-model.md` and
`monetization.md`.

## Goal

Let a small set of **beta users** experiment with mobile-sandbox remote
coding **on the owner's infrastructure** (owner's GLM key + owner's
Hetzner box), where:

1. **Only `kivanc.cakmak@icloud.com` can grant it.** No one else's key or
   box is ever shared by this mechanism.
2. **Beta users see only "Beta", never the infra.** They DO see a plain
   "Beta user" status (a visible badge — that's all). They do NOT see a
   "guest" relationship, a shared-device row, the owner's identity, or any
   "powered by Kıvanç's infra" marking. Under the hood it is the existing
   guesting / infra-grant machinery with a `hidden` flag suppressing the
   *infra* from their UI; the only thing surfaced is the Beta badge.
3. **Beta users can never touch the owner's box itself.** No SSH, no host
   shell, no host env, no access to the owner's files, other tenants, or
   the GLM key. They get a **partitioned git workspace** and nothing else.

## What is shared (three things, all invisible)

| # | Resource | Mechanism | Invisible via |
|---|---|---|---|
| A | **GLM API key** (inference) | Cloudflare gateway Worker (key = Worker secret) + per-user wallet grant + `gatewayPolicy` | "the wallet IS the key" — they just see working AI |
| B | **Hetzner box** (remote runner / execution) | `infraAccessGrants` row (host=owner, guest=beta) with `hidden:true` | UI-listing queries skip hidden grants |
| C | **Partitioned git workspace** on the box | per-tenant OS user + dir under a dedicated partition, granted via the same infra grant | never surfaced as a "device" |

Key never sits on the box (A): the box runs untrusted beta code, so its
runner reaches inference through the gateway with a scoped `ygw_` token —
**not** the host's keys (`useHostApiKeys:false`). This is the whole reason
the key is a Cloudflare secret and not a box env var.

### Runners: included GLM lane + BYO OAuth lanes

Beta tenants get two different runner classes. Keep them separate:

- **Included lane:** `opencode` through the Yaver Gateway. It is
  OpenAI-compatible: point `OPENAI_BASE_URL` at the gateway + a `ygw_`
  token and it runs on the owner's GLM/z.ai upstream, fully capped +
  metered. This is the free normie beta lane.
- **BYO OAuth lanes:** `claude` / `claude-code` and `codex` are valid only
  when the beta user signs into their own Claude/Codex account. They must
  run under that user's tenant Unix account with isolated
  `HOME`, `CLAUDE_CONFIG_DIR`, and `CODEX_HOME`. Never pool or reuse the
  owner's Claude/Codex OAuth credentials.

The grant can therefore allow `["opencode","claude","codex"]`, but the
runtime must enforce `useHostApiKeys:false` and `requireIsolation:true`.
The free CAC budget applies to the included GLM/opencode lane; Claude/Codex
costs remain outside Yaver because the user brings their own OAuth.

## Owner gating — `kivanc.cakmak@icloud.com` only

Reuse the existing single source of truth `ownerAllowlist.ts::isOwner`
(env-config, never hardcoded — the repo is public):

```
convex env set CLOUD_PREVIEW_OWNER_EMAIL    kivanc.cakmak@icloud.com
convex env set CLOUD_PREVIEW_OWNER_USER_IDS <kivanc's users._id>   # OAuth/Apple often has no email
```

Every beta-seed / revoke route is gated by `isOwner(callerEmail,
callerUserId)`. Unset env ⇒ `false` for everyone ⇒ fully fail-closed: the
share mechanism does nothing until the owner opts themselves in. The
**host** of every grant is forced to the resolved owner userId — a
non-owner can never seed a grant from someone else's infra.

## Invisibility — `hidden` flag, with the visible/access split

The one correctness subtlety: a `hidden` grant must still **work**
(access-control says yes) while being **unseen** (UI lists say no).

- `infraAccessGrants` gains `hidden: v.optional(v.boolean())` +
  `beta: v.optional(v.boolean())` (marker for cleanup/audit).
- **Access-control path** (`listActiveInfraGrantsForGuest`,
  `guestCanReachHostDevice`, machine/device resolution): **includes
  hidden grants** → the beta user can actually reach the box.
- **UI-listing path** (anything that renders the guest a list of shared
  devices / "shared with me" / guest relationships): uses a new
  `listVisibleInfraGrantsForGuest` (= active ∧ ¬hidden) → the beta user
  sees nothing.
- The **host (Kıvanç)** still sees and can manage/revoke all grants —
  hidden only hides from the *guest*, never from the owner.

## Hetzner box isolation — partition + no-SSH + no-host-touch

The box is a **shared, keyless executor**. Beta tenants get a git
workspace and nothing else. Enforced in layers (reuse
`infraAccessGrants.requireIsolation` + `multiuser.go`):

1. **Dedicated data partition.** Tenant workspaces live under a separate
   mount (e.g. `/srv/yaver/tenants`, its own volume/partition), never on
   `/` or the owner's home. Per-tenant dir `…/tenants/<betaUserId>/` owned
   by a per-tenant unprivileged OS user (`yv-<short>`), mode `0700`. Quota
   per tenant so one can't fill the disk.
2. **No SSH, ever.** Tenants never get an OS login, no `authorized_keys`,
   no PTY on the host. They reach the box only through the Yaver agent's
   authenticated HTTP/relay API, which runs the runner **as the tenant's
   unprivileged user** inside the tenant dir.
3. **No host environment.** The runner is started with a **scrubbed env**
   (the `gateway_runner_env.go` precedent already strips host secrets and
   injects only `OPENAI_BASE_URL` + the tenant's `ygw_` token). No
   `HCLOUD_TOKEN`, no owner vault, no GLM key reaches the tenant process.
4. **No cross-tenant / no host files.** `0700` per-tenant dirs + distinct
   UIDs; agent refuses paths outside the tenant root. `requireIsolation:
   true` on the grant enforces the sandbox path.
5. **Network jail (anti-pivot).** Tenant egress is RFC1918-blocked and
   not an open proxy (existing `access_policy.go` / `egress_proxy.go`
   posture) so a tenant can't reach the owner's LAN, other Hetzner
   internal IPs, or use the box to attack third parties.
6. **Disposable.** No tenant state is irreplaceable; their canonical
   recovery is their own git remote. The box can be rebuilt from
   cloud-init at any time.

The owner's own infra (SSH keys, `HCLOUD_TOKEN`, vault, `/root`, other
projects) is on the host OS and the data partition is separate — tenants,
confined to their UID + tenant dir + scrubbed env + no SSH, have no path
to any of it.

## Caps / bounded risk (your key, your money)

- **Per-user wallet grant** = hard inference ceiling (e.g. 500¢). Spent ⇒
  gateway returns 402, AI stops. They never know it was a budget.
- **Σ grants = global ceiling** (10 users × $5 = $50 max exposure). Plus a
  **hard spend limit on the z.ai account** as a backstop.
- **Per-request + hourly caps** via `gatewayPolicy` (operator-set,
  user-immutable) catch a runaway agent loop fast.
- **Per-tenant disk quota** on the partition; CPU/RAM caps via the grant
  (`cpuLimitPercent`, `ramLimitMb`) so one tenant can't starve the box.
- **Revocation** = flip `gatewayPolicy.enabled=false` + grant
  `status:"revoked"` + stop topping up. No key rotation needed; a leaked
  `ygw_` token dies at the gateway.

## Implementation map

Convex (this change):
- `schema.ts`: `infraAccessGrants` += `hidden`, `beta`.
- `access.ts`: add `listVisibleInfraGrantsForGuest` (UI path); leave
  `listActiveInfraGrantsForGuest` (access path) including hidden. Apply
  the visible variant in guest-facing list queries (`guests.ts`,
  `devices.ts`).
- `betaAccess.ts` (new, owner-gated): `seedBetaUser` /
  `revokeBetaUser` — one call each:
  - `seedBetaUser` → assert owner → resolve host=owner → `topUp` wallet
    grant → `gatewayPolicy` set (caps) → mint `ygw_` token →
    `grantIncludedHours(plan:"beta")` (the **visible** Beta badge + included
    box hours, reusing the entitlement layer) →
    `infraAccessGrants` insert `{hidden:true, beta:true,
    requireIsolation:true, useHostApiKeys:false,
    allowedRunners:["opencode"], shareAllMachines:true}` (+ machine/device
    link rows). The only user-visible result is plan="beta"; everything
    else is hidden.
  - `revokeBetaUser` → grant `revoked` + `gatewayPolicy.enabled=false`
    (wallet grant left to expire / drain).
- `http.ts`: `POST /beta/seed`, `POST /beta/revoke`, gated by `isOwner`.

Box (cloud-init / `multiuser.go`, follow-up):
- Mount the tenant partition; per-tenant user + `0700` dir + quota;
  runner launched as tenant user with scrubbed env (gateway token only);
  no `authorized_keys` for tenants; egress jail on.

Mobile:
- Remote backend points at `YAVER_GATEWAY_URL`; the seeded session token
  authorizes inference. Tenant box reached via the agent API (no SSH).

## Data plane — partition, project sharing, invisible push (DEEP)

The beta user does exactly two things: **(1) code in the mobile sandbox,
(2) commit+push** — and neither reveals git, the owner's identity, the
owner's credentials, or other tenants. Everything below runs on the
owner's Hetzner box.

### Per-tenant partition (cross-tenant blind)

- Each beta user gets `/srv/yaver/tenants/<betaUserId>/` on a **dedicated
  data partition**, owned by a per-tenant unprivileged OS user
  (`yv-<short>`), mode `0700`, with a disk quota.
- A tenant can `ls` only their own dir. They cannot enumerate other
  tenants, the owner's home, or the owner's other projects. "They won't
  see each other's projects" = `0700` + distinct UIDs + the agent
  refusing any path outside the caller's tenant root.

### Project sharing — sandbox OR a real owner repo (`../carrotbet`, `../sfmg`)

Two modes the owner picks per beta user:

1. **Scratch sandbox** — the mobile SQLite sandbox; no real repo.
2. **A real owner project** — the owner seeds e.g. `carrotbet` or `sfmg`
   into the tenant's partition so they develop on actual code.

For mode 2 the broker (below) clones the project into the tenant dir. The
beta user then has the **full source** of that project — unavoidable if
they're to develop on it; it is the owner's explicit choice per project.

### Invisible owner-credentialed push — the two-repo broker

The requirement "they commit+push **from my account** without knowing" is
the dangerous part, because the beta user's `opencode` runs **arbitrary
code** (builds/tests). If the owner's git token is anywhere the tenant
process can read — env, a credentialed remote URL, a git credential
helper, `.git/config` — the tenant **exfiltrates the owner's git token**.
So raw creds must NEVER touch the tenant.

The clean isolation is **two repos**:

```
 tenant partition (yv-<id>, 0700)        broker dir (agent user, 0700, OUTSIDE any tenant)
 ───────────────────────────────         ─────────────────────────────────────────────────
 <project>/            ← opencode edits   <project>.mirror/   ← credentialed remote lives HERE only
   .git remote = LOCAL only, NO creds       remote "origin" = github + OWNER token
```

Flow on "commit & push" (triggered by the mobile sandbox → agent API):
1. The **broker** (Yaver agent, running as a privileged service user the
   tenant cannot impersonate) reads the tenant working tree.
2. Broker pulls the tenant's commits into its OWN mirror via a local
   `git fetch`/`bundle` (no creds involved).
3. Broker commits (author = owner, per "from my account"; or a "Yaver
   Beta" bot author — see attribution note) and **pushes from the mirror**
   to a **per-tenant branch** `beta/<betaUserId>/<ts>` — **never `main`**.
4. The owner's token exists ONLY in the mirror dir + the broker's env,
   both unreadable by the tenant UID. Tenant code never runs in the push
   process.

The tenant's own clone has **no credentialed remote and no credential
helper** → an in-sandbox `git push` simply fails. There is no git UI, no
"connect GitHub", no remote shown — the beta user never knows a push
happened, only that "it saved".

### Honest risk analysis (this is where it can bite you)

| Risk | Why it's real | Control (required) |
|---|---|---|
| **Tenant steals owner git token** | opencode runs arbitrary code; a credentialed remote/env in the tenant = game over | Two-repo broker; creds ONLY in broker mirror + broker env (different UID, `0700`); tenant clone has no creds |
| **Sharing `carrotbet`/`sfmg` leaks the project's OWN secrets** | real repos contain `.env`, `keys/`, vault refs — seeding the repo hands them to the beta user | **Scrub before seed**: never copy `.env*`, `keys/`, `*.pem`, `.yaver` vault, CI secrets; seed from a cleaned export, not a raw clone |
| **Beta user pushes junk/malicious to your repo** | owner creds can write to the repo | **Branch-only** (`beta/<id>/*`), NEVER main; owner reviews diff before merge; consider push-to-fork |
| **Attribution** | commits authored as owner = your name on a stranger's code | choose: author = "Yaver Beta" bot (recommended) vs owner; pusher/creds always owner |
| **Arbitrary code execution / escape** | opencode builds run untrusted code on your box | isolation (separate box ideal), egress jail (RFC1918 block, no open proxy), resource caps, disposable box |
| **Source exposure** | beta user reads full `carrotbet`/`sfmg` source | inherent to "develop on it" — owner opts in per project; don't share what you won't show |

### Net

The control-plane (Convex grants/caps/hidden — **done**) is the easy half.
The push broker + secret-scrubbed project seeding + the per-tenant
partition is the hard half, and its non-negotiables are: **two-repo
credential isolation, branch-only push, scrub project secrets before
seeding, and a network-jailed disposable box.** Skip any one and you
either leak your git token, leak the shared project's secrets, or let a
stranger write to `main`.

## DEEP ANALYSIS — Hermes, vibe loop, and the phone+PC surface

This is the full picture of what "they just code and it works" actually
requires, surface by surface.

### Surfaces: phone AND PC (same backend)

A beta user may drive the SAME tenant workspace from either:
- **Mobile (Yaver app)** — the primary surface; sandbox + remote coding.
- **Web (PC)** — the dashboard, relay-only (browser can't reach LAN).

Both authenticate with the user's session token, both resolve to the same
`infraAccessGrants` (hidden) → same tenant partition on the box → same
opencode runner → same gateway for inference. Nothing about the surface
changes the isolation or the caps; the PC is just a second client of the
identical backend. **Web wiring needed:** the dashboard must (a) detect
`plan:"beta"` from the entitlement, (b) render the beta workspace view
(project + preview + "ask"/vibe box) instead of the infra/wallet controls,
(c) route coding to the same agent endpoint the mobile sandbox uses, and
(d) suppress the hidden grant in every device/guest list (the
`listVisibleInfraGrantsForGuest` path). The beta user on PC sees a project
and a chat box — no device, no guest, no git, no provider.

### Hermes reload for sfmg / carrotbet (RN apps)

Hermes is the differentiator and it already exists for the owner path —
the beta path REUSES it, it is not new engine work:

1. `POST /dev/build-native {framework:"expo", workDir:<tenant project>}`
   → Metro bundles + `hermesc` compiles the tenant's edited RN app to a
   Hermes bytecode bundle (HBC magic `0x1F1903C1`, BC v96).
2. The bundle is pushed into the **beta user's own Yaver mobile container**
   via `ExpoReactNativeFactory` — the same suppress-when-inside-Yaver path
   used today.
3. `POST /dev/reload` hot-swaps it. The beta user sees their change running
   on their real phone in seconds.

What's tenant-specific (the wiring, not the engine):
- `/dev/*` must run **as the tenant's unprivileged user, scoped to the
  tenant project dir** (the partition + scrubbed-env model), never the
  host. The build runs the tenant's `node_modules` → arbitrary code →
  must be jailed exactly like opencode.
- The bundle is delivered to **that beta user's** device only (the hidden
  grant resolves which device), never cross-tenant.
- sfmg and carrotbet are both Expo/RN, so the existing framework detection
  applies unchanged; the seeder just needs to `npm install` in the tenant
  copy (after scrub) before the first build.

### The vibe loop (Yaver feedback SDK)

"Vibe coding" = the closed loop where the running app feeds the agent:

```
 opencode edits → /dev/build-native → Hermes reload on phone
        ↑                                      │
        │  feedback SDK: user shakes / taps,   ▼
        └──── annotates the live screen, types "make this bigger"
              → feedback event (screenshot + note + component) → opencode
```

- The **feedback SDK** (`sdk/feedback/react-native`) is already what
  captures a shake → screenshot → annotation → structured feedback event,
  and inside the Yaver container it routes to the container (not a
  standalone server). For the beta loop, that feedback event becomes the
  **next opencode prompt** (with the screenshot + the targeted component),
  so the user "talks to the screen" and the agent iterates → rebuild →
  reload. That is the vibe loop end to end.
- **SDK install is in the OTHER repos.** sfmg and carrotbet must each
  carry the RN feedback SDK (`YaverFeedback.init()`), which is a change in
  `../sfmg` and `../carrotbet`, not this repo. The container suppresses the
  SDK's own UI (SDK 0.5.5+ `YaverInfo` no-op) so it doesn't double up.
- **carrotbet caveat repeats here:** seeding it runs `npm install` and
  builds *its* code on your box for a stranger — same scrub + jail rules,
  and it's your private betting source running under a beta user.

### What's BUILT vs REMAINING (honest)

| Piece | State |
|---|---|
| Control-plane: hidden grant, opencode-only, caps, Beta badge | ✅ built, typecheck green |
| Owner tools: seed / seedByEmail / reset / revoke / **purge** | ✅ built, typecheck green |
| **Secret scrubber** (`beta_scrub.go` + tests) — the leak gate | ✅ built, `go test` green |
| Hermes build/reload **engine** | ✅ exists (owner path) — needs tenant-scoping |
| Feedback SDK capture **engine** | ✅ exists — needs install in sfmg/carrotbet |
| Per-tenant **partition + jailed `/dev/*` + opencode** as tenant user | ⛔ box-side, not built (needs the box) |
| **Two-repo push broker** (invisible owner-credentialed push) | ⛔ not built (security-critical, needs box) |
| **Web/PC beta wiring** (plan:beta view, route to agent, hide grant) | ⛔ web work, not built |
| SDK in `../sfmg` / `../carrotbet` | ⛔ other repos, not built |

The engines (Hermes, feedback, gateway, scrubber) exist or are now built.
The remaining work is **integration + isolation**: scope `/dev/*` + opencode
to a jailed tenant partition, the push broker, the web view, and the
two cross-repo SDK installs. None of it is new *invention*; all of it
needs the actual box / repos to build and verify, which is why it can't be
honestly "finished" from here in one pass.

## DEEP ANALYSIS — Managed Git + Serverless Lite for beta users

The owner now wants a fresh beta user to ALSO: push via **Yaver Managed
Git** and **deploy Serverless Lite to the owner's box** — all zero-config.
Reading `managed_git.go` changes the security design in our favour, and
surfaces one real gap.

### Finding 1: Managed Git dissolves the credential-exposure problem

The whole two-repo push broker existed to keep the owner's GitHub token
away from a tenant's arbitrary code. **Managed Git makes that mostly
unnecessary**, because a managed project's `origin` is a **local bare repo
on the box**, not GitHub:

- `EnsureManagedGitForProject` → `git remote add origin <barePath>` where
  `barePath = …/managed-git/repos/<repoID>.git` (a filesystem path).
- `ManagedGitCheckpoint` → `git add/commit/push origin HEAD:main` — a push
  to a **local path, with NO credentials**.
- `ManagedGitMirrorToProvider` → the ONLY credentialed step (push the bare
  repo → GitHub/GitLab), and it is **owner-triggered, owner-side**.

So the safe beta push path is:

```
 tenant (opencode, no creds) ──commit+push──▶ local bare repo (origin, no creds)
                                                   │  owner-triggered, owner-side
                                                   ▼
                                         ManagedGitMirrorToProvider ──▶ GitHub (owner creds)
```

The tenant never touches a credential. **My hand-rolled `BetaPushBroker` is
therefore partly superseded**: the *push primitive* should be
`ManagedGitCheckpoint` (push to local bare), and the *credentialed mirror*
should be `ManagedGitMirrorToProvider` (owner-side). The broker's residual
value is only the **isolation wrapper** (run as the tenant user, scrubbed
env) and the invariant that the mirror step never runs in tenant context —
which Managed Git already satisfies structurally. Net: **wire beta push
through Managed Git, don't run a parallel broker.**

### Finding 2: the real gap — Managed Git is single-tenant today

`managedGitReposRoot()` = `ConfigDir()/managed-git/repos/` — **one shared
location under the owner's `~/.yaver`, keyed only by repo slug.** That is
correct for the box owner's own projects but **wrong for multi-tenant
beta**:

1. **Path is owner-only.** A tenant runs as `yv-<id>` with `HOME=<tenant
   dir>` and no access to the owner's `~/.yaver` → a tenant `git push
   origin` to a bare repo under the owner's home **fails** (no FS perms).
2. **Slug collision / cross-tenant read.** Two beta users whose projects
   slug to the same `repoID` would share one bare repo. No tenant scoping.

**Required integration:** a **per-tenant managed-git root inside the tenant
partition** — `/srv/yaver/tenants/<betaUserId>/managed-git/repos/<repoID>.git`
— owned by `yv-<id>`, `0700`. Then:
- the tenant can push to *their own* bare repo (it's in their partition),
- tenants can't see each other's repos (distinct partitions + UIDs),
- the owner-side mirror reads from the tenant's bare repo and pushes to
  GitHub with owner creds (the one credentialed hop, outside tenant code).

This needs Managed Git to accept a **root override** (today it hardcodes
`ConfigDir()`). That's a small change to `managedGitReposRoot` (take a base
dir) — but it lives in the **parallel-WIP file**, so it must be coordinated
with whoever owns `managed_git.go`, not edited blindly.

### Finding 3: Serverless Lite for beta = deploy on the owner's box, jailed

"Deploy serverless to my machine" means a tenant ships a function that runs
on the owner's box. Same isolation rules as opencode, plus deploy-specific
ones:

- Runs **as the tenant user**, in the tenant partition, with
  `betaTenantRunnerEnv` (gateway only, zero host secrets).
- **Per-tenant port allocation** (each tenant's function on its own port
  range; never collide, never bind the owner's services).
- **Resource caps** (`cpuLimitPercent`/`ramLimitMb` from the grant) +
  **network jail** (RFC1918-blocked egress, not an open proxy) — a deployed
  function is long-lived untrusted code, the highest-risk surface here.
- **Lifecycle tied to the grant**: `revokeBetaUser` / `purgeBetaUser` must
  stop + remove the tenant's deployed functions (else they outlive access).

### The full fresh-beta-user vertical (zero-config)

```
 sign in (Beta badge)
   → workspace = scrubbed clone of sfmg/carrotbet in /srv/yaver/tenants/<id>/
   → opencode (as yv-<id>, gateway inference)        ← code
   → /dev/build-native + /dev/reload (as yv-<id>)    ← Hermes on their phone
   → ManagedGitCheckpoint → tenant-local bare repo   ← commit/push, no creds
        (owner optionally mirrors to GitHub)
   → Serverless Lite deploy (as yv-<id>, jailed)     ← runs on owner's box
```

Every arrow runs as the unprivileged tenant user, in the tenant partition,
with the gateway-only env. None of it sees the owner's keys, git creds,
other tenants, or host. That is the whole promise: **they use everything,
configure nothing, and can touch nothing of the owner's.**

### Honest build status (why no new integration code this turn)

The agent package **does not currently compile** — the parallel worktree
building Managed Git has it mid-edit (`gitCmd` redeclared between
`managed_git.go` and `mcp_productivity.go`; earlier, undefined
`ManagedGitProjectMeta` in `phone_backend.go`). Writing the beta↔managed-git
wiring now would mean integrating against a **non-compiling, actively
changing API** — I'd be building on sand and could not verify it. So this
turn is analysis + spec; the wiring (below) lands once the parallel Managed
Git work settles and `go build ./...` is green again.

**Wiring to implement once the build is green:**
1. `managedGitReposRoot` gains a base-dir param → per-tenant root in the
   partition (coordinate with the parallel owner of the file).
2. `POST /beta/push` → run `ManagedGitCheckpoint(tenantProjectDir)` as the
   tenant user (no creds), return branch/commit.
3. Owner-side `ManagedGitMirrorToProvider` exposed as an OWNER action
   (mirror a tenant's repo → GitHub), never in tenant context.
4. Launch opencode + `/dev/*` + serverless-deploy **as `yv-<id>`** with
   `betaTenantRunnerEnv` + the partition as cwd.
5. Grant teardown (`revokeBetaUser`/`purgeBetaUser`) stops tenant functions
   + removes the tenant partition.

## INTEGRATION — beta isolation IS the serverless-normie-cloud tenant layer

`docs/handoffs/HANDOFF-yaver-serverless-normie-cloud.md` defines the
broader product (phone sandbox → Yaver Serverless Lite bundle → deploy to
self-hosted OR managed cloud → friend preview). Its **"Managed Cloud
Target → Public managed cloud must add"** list is exactly the gap this beta
work fills. Mapping:

| Handoff requirement (public managed cloud) | Beta work |
|---|---|
| **tenant isolation stronger than a shared owner home** | ✅ `beta_tenant.go` — per-tenant partition `/srv/yaver/tenants/<id>`, `yv-<id>` user, `betaConfinePath`, `betaTenantRunnerEnv` (allowlist, zero host secrets) |
| per-user deploy auth | ✅ beta grant + gateway scoped token + `gatewayPolicy` |
| billing/entitlement gate | ✅ `getBetaStatus` / `/subscription` `beta`, gateway caps |
| backup before overwrite | ✅ reuse `ManagedGitCheckpoint`/`ManagedGitBackup` (handoff §Managed Git) |
| audit rows without app data | ✅ `managedUsage` meter (counters only, privacy-pinned) |
| no Convex storage of app contents | ✅ app SQLite stays on box; Convex = control plane only |
| per-project receive token / quota / size caps | ⛔ to wire in the parallel receive path |

### The one seam to wire (NOT to clobber)

The handoff's receive path lands app state at
`~/.yaver/phone-projects/<slug>/data/app.sqlite` — **a shared owner home,
single-tenant.** That is the very "shared owner home" the handoff says
public managed cloud must move beyond. The beta substrate already provides
the destination: a **per-tenant base dir**.

So the integration is a single parameter, mirrored across two roots:
- **phone-projects root** → `/srv/yaver/tenants/<betaUserId>/phone-projects/<slug>/`
  (instead of `~/.yaver/phone-projects/<slug>`).
- **managed-git root** → `betaTenantRepoRoot(tenantDir)` (already built in
  `beta_managed_git.go`), instead of `managedGitReposRoot()`.

Both live in **parallel-owned, actively-churning files**
(`phone_backend.go`, `managed_git.go`) — per the handoff's own "Working
Tree Warning" and `CLAUDE.md`, those are **not edited here**. The beta side
provides the tenant base dir + the isolation primitives; the receive path
takes a per-tenant base dir param when the parallel thread is ready. This
keeps `TestPhoneProjectExportReceiveBetweenAgents` /
`TestManagedGitCreateCheckpointAndBackup` green (verified: they pass
alongside the beta suite).

### Friend preview ↔ beta hidden grant

The handoff's friend preview ("friend cannot access `/managed-git/*`,
`/phone/projects/receive`, or owner vault"; "no owner tokens in share
payload") is the read-only cousin of the beta share. The beta hidden grant
+ credential isolation + gateway-scoped token satisfy the same invariants
for the *write/develop* tier; friend preview is the *read-only* tier on the
same isolation substrate.

### PROPOSED PATCH — per-tenant receive (for the phone_backend.go owner)

Not applied here (parallel-owned, churning files). The beta side already
provides the destination helper `betaTenantPhoneRoot(userID)` (built +
tested in `beta_managed_git.go`, returns `/srv/yaver/tenants/<id>/phone-projects`
or `""` for a non-beta user). Three drop-in edits complete the seam:

**1. `phone_backend.go` — add a base override to import options:**
```go
type PhoneImportOptions struct {
    SlugOverride string
    OnConflict   string
    SkipSeed     bool
    // BaseRoot, when non-empty, materialises the project under
    // BaseRoot/<slug> instead of the shared PhoneProjectsRoot()
    // (~/.yaver/phone-projects). Used for per-tenant isolation on public
    // managed cloud. MUST be an absolute path the agent controls.
    BaseRoot string
}
```

**2. `phone_backend.go` — honour it where the destination is resolved.**
Today `ImportPhoneProject` (and its conflict/materialise helpers
`PhoneProjectDir` / `phoneProjectExists` / `DeletePhoneProject` /
`CreatePhoneProject`) root everything at `PhoneProjectsRoot()`. Thread an
optional base through that single resolver:
```go
func phoneDestRoot(baseRoot string) (string, error) {
    if strings.TrimSpace(baseRoot) != "" {
        if err := os.MkdirAll(baseRoot, 0o700); err != nil {
            return "", err
        }
        return baseRoot, nil   // per-tenant partition
    }
    return PhoneProjectsRoot() // default shared root (unchanged behaviour)
}
```
Then in `ImportPhoneProject`, resolve the slug dir / conflict check /
materialise against `phoneDestRoot(opts.BaseRoot)` rather than the global
root. (Ticket 3's "import rewrites target-local WorkDir and bare repo
paths" already touches these sites — fold BaseRoot in there. Keep path-
traversal rejection from that ticket.) Empty BaseRoot ⇒ byte-identical to
today, so `TestPhoneProjectExportReceiveBetweenAgents` /
`TestImportAcceptsCanonicalDataSQLiteWithoutLegacyLocalDB` stay green.

**3. `phone_backend_http.go` — set BaseRoot for a beta caller in `handlePhoneReceive`:**
```go
// after the existing per-user deploy auth resolves the caller's userId:
if betaRoot := betaTenantPhoneRoot(callerUserID); betaRoot != "" {
    importOpts.BaseRoot = betaRoot // isolate this tenant's app to their partition
}
```
A non-beta caller gets `""` → the default shared root → no behaviour
change. A beta tenant's app + its managed-git repo
(`betaTenantRepoRoot`) both land in `/srv/yaver/tenants/<id>/…`,
satisfying "tenant isolation stronger than a shared owner home" with no
edits to the beta-owned files.

This keeps the two threads disjoint: beta owns the helpers + tests
(`betaTenantPhoneRoot`, `betaTenantRepoRoot`), the phone_backend owner owns
the three drop-in edits above. Neither edits the other's lines.

### Net

Beta = the **multi-tenant isolation + entitlement layer** the serverless
normie cloud needs to go from "operator's own box" (handoff MVP) to "public
managed cloud" (handoff target). The substrate is built + test-green; the
final wiring is one per-tenant-base-dir parameter threaded through the
parallel receive path, to be done when that thread settles — not clobbered
mid-flight.

## Seed / revoke flow

```
# owner only (kivanc.cakmak@icloud.com):
POST /beta/seed   { email | userId, grantCents?, dailyCapCents? }
POST /beta/revoke { email | userId }
```

Seeding is idempotent per (host, guest). The beta user, next sign-in,
silently has: working AI (gateway), a reachable hidden box (infra grant),
a partitioned git workspace — and sees none of it.
