# Yaver Social Graph + Invite-to-Code — Deep Analysis

**Status:** design-only, 2026-06-08.
**Scope:** friendships / social connections in the UI, device sharing,
project sharing ("ask him to join my project"), and the full git wiring
(GitHub / GitLab / git) so a non-technical invitee ("normie") can clone →
code → test → commit → push → deploy from Yaver — hosted EITHER on the
inviting developer's own machine OR on a Yaver-managed cloud box.

> Per CLAUDE.md: code is the source of truth. Every "exists" claim below is
> grounded in a file:line read on 2026-06-08. Every "gap" is a thing I
> looked for and did **not** find. Re-grep before building.

---

## Build status (2026-06-08)

**P0 (social graph) + P1 (shared projects) + the normie git endpoints are
BUILT and typecheck/compile clean. Uncommitted. Convex not yet prod-deployed
(codegen ran against the dev deployment only).**

- Convex: `connections` + `projectShares` + `projectMemberships` tables
  (`schema.ts`), `connections.ts`, `projectShares.ts`, and all
  `/connections/*` + `/project-shares/*` HTTP routes (`http.ts`). `tsc -p
  convex` clean.
- Agent (Go): `git_pr.go` — `POST /git/pull-request` (GitHub PR / GitLab MR,
  token stays on box) + `POST /git/identity` (per-repo commit author),
  registered in `httpserver.go`. `git_pr_test.go` covers the remote parser.
  `go build ./...` + tests green.
- Web: `lib/connections.ts`, `lib/projectShares.ts`, `CollabView.tsx`
  (People + Shared Projects), wired as the **People** tab in
  `app/dashboard/page.tsx`. Full `tsc` clean.
- Mobile: `src/lib/connections.ts`, `src/lib/projectShares.ts`,
  `app/connections.tsx`, linked from the **Collaborate** section in
  `(tabs)/settings.tsx`. `tsc` clean.

**Not yet built:** P2 managed-cloud auto-provision on accept (the host
chooser stores `hostKind`/`payer` but accept doesn't yet spin a box),
P3 deploy role-gate + auto branch-checkout wiring into the coding surfaces,
P4 QR/handle discovery + notifications. To go live: `cd backend && npx convex
deploy --yes`, then cut a CLI release so the agent ships `/git/pull-request`.

---

## 0. TL;DR

The hard parts are already built and dormant. What's missing is a thin
**social/address-book layer** and a **project-collaboration wrapper** that
composes primitives we already ship:

- **Access control spine** — `infraAccessGrants` + `guestAccess` +
  `allowedProjects` + scope tiers (`full` / `feedback-only` / `sdk-project` /
  `support`). Fine-grained device/machine scoping, resource limits, TTLs,
  reverse-grants. **(built)**
- **Git spine** — provider connect (manual + auto-detect + RFC-8628 device
  flow), clone-with-metadata, `commit-push` with auto-rebase + conflict
  handoff, status/log/diff/branches/stash, P2P cred transfer to owned boxes,
  repo create. Tokens live on-device, never in Convex. **(built)**
- **Hosting spine** — managed cloud provisioning (Hetzner via cloud-init),
  prepaid wallet + metering, pause/resume/snapshot, self-registers as a
  `devices` row, runner OAuth. BYO-phone provisioning too. **(built, dryRun
  metering)**
- **Coding spine** — agentic coding loop on phone (GLM in Hermes) +
  `repo-coding.tsx` + on-box runners (Claude/Codex). **(built)**

**The three real gaps:**

1. **No social graph.** Every collaboration is a one-shot invitation edge
   (`guestInvitations`, `supportInvites`, `hostShareInvites`). There is no
   reusable "friends/connections" list. You re-type an email or paste a code
   every single time. → new `connections` table + address-book UI.
2. **No first-class shared project.** Projects are `userProjects`
   (slug+deviceId+flags+branch, no paths) and guests get an `allowedProjects`
   string allowlist. Nothing ties *a repo + a host + a roster of people +
   their roles* into one object you can "ask someone to join." → new
   `projectShares` wrapper that **composes** an `infraAccessGrant` +
   `allowedProjects` + (optionally) a managed-box provision.
3. **No PR / branch-isolation flow for normies, and git wiring isn't
   surfaced as a normie journey.** We can clone/commit/push and *create* a
   repo, but there is no GitHub/GitLab **pull-request** creation, no
   per-collaborator branch convention, and no normie-safe deploy gate. The
   agent endpoints exist; the guided UX does not.

Everything else is wiring, not invention.

---

## 1. What exists today (grounded inventory)

### 1.1 Access-control spine — `backend/convex/`

| Object | File:line | What it gives us |
|---|---|---|
| `guestInvitations` | `schema.ts:1282` | email/userId invite, 6-char code, scope, `proposedDeviceIds`, `allowedProjects`, 2-day TTL |
| `guestAccess` | `schema.ts:1316` | active grant: scope, `allowedProjects`, `dailyTokenLimit`, `allowedRunners`, `usageMode`, schedule |
| `infraAccessGrants` | `schema.ts:1352` | the real ACL: per-device/per-machine, resource caps (cpu/ram), isolation, desktop/browser/tunnel toggles, `expiresAt`, `origin` |
| `infraAccessGrantDevices` / `…Machines` | `schema.ts:1390` / `1402` | scope a grant to specific devices / cloud machines |
| `supportInvites` | `schema.ts:1419` | shareable link → **reverse** grant (friend = host, supporter = guest) |
| `hostShareInvites` / `hostShareSessions` | `schema.ts:1445` / `1479` | time-boxed sessions with tooling/resource presets + idle timeout |
| `teams` / `teamMembers` | `schema.ts:953` / `966` | shared billing + device pool, admin/member roles |
| `sdkTokens` | `schema.ts:1526` | long-lived **delegated** guest tokens (feedback SDK), scoped to project+device |

Mutations/queries already in place: `guests.ts` (`invite`, `accept`,
`acceptByCode`, `revoke`, `updateGuestConfig`, `listGuests`, `listHosts`,
`getGuestConfig`, `lookupPublicUser`), `support_link.ts`
(`createSupportInvite`, `redeemSupportInvite`, `listSupportConnections`,
`getSupportInviteInfo`), `hostShare.ts` (`createInvite`, `joinByCode`,
`endSession`, `getAccessForHostDevice`/`…GuestDevice`), `teams.ts`, and the
`access.ts` helper library (`getActiveInfraGrant`,
`guestCanReachSpecificHostDevice`, `revokeInfraGrantsBetweenUsers`, …).

**Agent-side enforcement** is real: `guest_config.go` (CheckAccess /
CheckRunner / CheckProject / CheckSharedStorage, 10s refresh from Convex),
`guest_http.go` (path ACL + scope + `/info` redaction), `host_share_*.go`
(session TTL + idle timeout).

`lookupPublicUser` (`guests.ts:593`) already resolves `userId → {userId,
fullName, email}` — the seed of an address book exists; nothing accumulates
it into a list.

### 1.2 Git spine — `desktop/agent/`

| Capability | File | Route / entry |
|---|---|---|
| Provider state (tokens on-disk, never Convex) | `git_provider.go` | `~/.yaver/git-providers.json`, `git-credentials.json` |
| Auto-detect tokens (gh/glab/env/credhelper) | `git_provider.go` | `GET /git/provider/detect` |
| Manual setup + verify + SSH key gen/upload | `git_provider.go` | `POST /git/provider/setup` |
| Device flow (RFC 8628) | `git_oauth_device.go` | scopes: GH `repo, read:org, read:user`; GL `api, read_user, read_repository, write_repository` |
| List repos | `git_provider.go` | `GET /git/provider/repos?host=` |
| Clone + metadata (framework/CI detect) | `git_provider.go` | `POST /repos/clone` |
| **Commit + push** (auto-rebase, conflict handoff) | `git_commit_push.go` | `POST /git/commit-push` → `{pushed, rebased, requiresAgent, conflicts[]}` |
| status / log / diff / branches / stash | `git_http.go` | `GET /git/{status,log,diff,branches}`, `POST /git/stash` |
| Find existing clone (dedupe) | `git_find.go` | `GET /git/find-repo?url=` |
| Create repo on GH/GL | `git_provider.go` | `POST /git-providers/repo/create` |
| Push creds to **owned** remote box (P2P) | `git_push_creds_cmd.go`, `ops_git.go` | `git_connect`, `git_push` verbs → `/machine/onboarding/apply` |
| Dev-env clone (repos+toolchain) | `dev_env_clone.go`, `mcp_dev_env_clone.go` | `POST /dev-environment/clone/{plan,start}` |

Mobile mirror: `mobile/app/repo-coding.tsx` (isomorphic-git clone + GLM
agent + push), `mobile/app/git-accounts.tsx` (keychain-only provider creds),
`mobile/src/lib/cloneToPhone.ts`, `SandboxGitPanel.tsx`.

Web: `web/components/dashboard/GitView.tsx` (device-flow UI, repo browse,
clone, commit&push, target-device picker).

**Privacy:** GH/GL tokens live in `~/.yaver/*.json` (agent), device keychain
(mobile), or P2P-transferred to **owned** boxes via
`/machine/onboarding/apply`. Convex `authIdentities` stores identity only
(provider + providerId), **never tokens** — verified against the privacy
contract in `convex_privacy_test.go`.

### 1.3 Hosting spine — managed cloud

`cloudMachines` (`schema.ts:1119`) + `cloudMachines.ts`
(`create`→`provision` internalAction, Hetzner cloud-init, phase beacon,
`setProvisioned`, `ensureForSubscription`, BYO `mintByoBootstrap`),
`cloudLifecycle.ts` (prepaid wallet, `recordUsageAndDeduct`, markup cpu 2× /
gpu 3×, dryRun), `managedMeter.ts` (generic meter, suspend-on-zero).
Agent: `ops_cloud.go` (`cloud_provision/destroy/checkout/scale/list/byo`),
`cloud.go` CLI. UI: `web/.../ManagedCloudPanel.tsx`,
`mobile/.../ManagedCloudCard.tsx`. HTTP: `/billing/yaver-cloud/*` in
`http.ts:3991+`.

**A provisioned box self-registers as a normal `devices` row** (deviceId
`cloud-<prefix>`, tags `["cloud","managed"]`) and runs the agent. So once it
exists, it is reachable and shareable through the **exact same**
`infraAccessGrant` path as a self-hosted box. This is the load-bearing fact
that makes "my machine OR managed cloud" a single design, not two.

### 1.4 Coding spine

Phone: `codingAgent/{sandboxTools,runner,sandboxBinding}.ts` (GLM-default
agentic loop, built 2026-06-08), `repo-coding.tsx`. Box: runner OAuth
(`RunnerAuthCTA` in `ManagedCloudPanel.tsx`) signs Claude/Codex in on the
managed box. iOS = Hermes + GLM only (no CLIs); reload / Convex-deploy still
need a machine.

---

## 2. The gaps, precisely

1. **Social graph** — no `connections`/friends table. `lookupPublicUser`
   resolves one id; nothing remembers people. Result: every share re-enters
   an email or code.
2. **Shared project object** — no entity binding `{repo, host, roster,
   roles}`. `userProjects` is per-device bookkeeping; `allowedProjects` is a
   bare string allowlist on a grant.
3. **PR + branch isolation** — `commit-push` pushes to the current branch;
   there is no GH/GL **pull-request** creation (only repo *create*), no
   per-collaborator branch convention, no "normie pushes to a feature
   branch, owner reviews" flow.
4. **Normie deploy gate** — `deploy_script_gen.go` / `yaver deploy` exist but
   there is no role check that says "normies can't deploy to prod."
5. **Normie git identity** — commits would carry the box owner's
   `user.name/email` unless we set per-session author identity.
6. **Guided journey** — all the endpoints exist but there is no single
   "accept invite → you're coding" flow for a non-technical user.

---

## 3. Proposed design

Two new Convex objects, both **thin wrappers** over existing primitives, plus
UI on all three surfaces. No new enforcement engine — `infraAccessGrants` +
`guest_config.go` already enforce everything.

### 3.1 `connections` — the social graph (address book)

```
connections:
  userId        Id<users>          // owner of this row's perspective
  peerUserId    Id<users>
  status        "pending"|"accepted"|"blocked"
  direction     "outgoing"|"incoming"  // who initiated (UI affordance)
  nickname      optional string    // "Serhat (designer)"
  source        "email"|"username"|"support-link"|"project-invite"|"qr"
  createdAt, acceptedAt, blockedAt
index: by_user, by_peer, by_user_peer
```

- **Mutual** modeled as two rows (one per perspective), written together on
  accept — same pattern as `guestAccess`/`infraAccessGrants` directionality.
- **Discovery:** by email (reuse the email path in `guests.invite`), by a
  shareable `@handle` (new optional `users.handle`), by QR (reuse the
  support-link code rail), or auto-suggested from existing
  guest/support/team edges (we can backfill connections from
  `guestAccess`/`supportInvites`/`teamMembers` on first load — zero typing).
- **Why a graph at all:** it turns every existing invite flow from
  "type/paste a code" into "pick a friend." It is the UI substrate the user
  asked for ("social connection friendships etc"). It carries **no**
  sensitive data → privacy-contract clean by construction.

New mutations (`backend/convex/connections.ts`): `request(peerEmail|handle)`,
`accept(connectionId)`, `block`, `remove`, `setNickname`; queries:
`list(status?)`, `search(query)` (wraps `lookupPublicUser`),
`suggested()` (derived from existing edges).

### 3.2 `projectShares` — "ask him to join my project"

```
projectShares:
  ownerUserId   Id<users>
  slug          string             // human label, NOT a path
  repoUrl       string             // git remote (host/owner/repo form)
  defaultBranch string
  hostKind      "owner-device"|"managed-cloud"
  hostDeviceId  optional string    // when owner-device or already-provisioned cloud
  hostMachineId optional Id<cloudMachines>
  payer         "owner"|"invitee"  // who funds managed compute
  createdAt, archivedAt
index: by_owner, by_slug

projectMemberships:
  shareId       Id<projectShares>
  userId        Id<users>
  role          "owner"|"dev"|"normie"|"viewer"
  branch        optional string    // per-collaborator feature branch ("yaver/serhat")
  grantId       optional Id<infraAccessGrants>  // the materialized access edge
  status        "invited"|"active"|"revoked"
  invitedAt, acceptedAt, revokedAt
index: by_share, by_user, by_share_user
```

**Roles → existing scope mapping (no new enforcement):**

| Role | infraAccessGrant scope | allowedProjects | runners | deploy | push target |
|---|---|---|---|---|---|
| owner | full | — | all | yes | any branch / main |
| dev | full | `[slug]` | all | yes (gated) | feature branch, PR to main |
| normie | full (coding) | `[slug]` | GLM/BYO or host runner | **no** (PR only) | `yaver/<name>` branch only |
| viewer | feedback-only | `[slug]` | — | no | none |

The role is just a **preset** that fills in the `updateGuestConfig` /
`hostShareInvites` policy fields we already have (`allowedRunners`,
`allowedProjects`, isolation, resource caps). "normie" = the existing
hardened, auto-containerized profile + a branch pin + deploy off.

### 3.3 The invite-to-code flow (the thing the user asked for)

```
Developer (web/mobile)
  1. Picks repo  → GitView/repo browse (POST /repos/clone target chosen below)
  2. Picks host  → AskUserQuestion-style chooser:
        (a) "My machine"        → existing online owned device
        (b) "Yaver Cloud (I pay)"   → provision managed box, payer=owner
        (c) "Yaver Cloud (they pay)"→ invitee provisions, payer=invitee
  3. Picks person → from connections list (or invite-by-email creates a
        pending connection + the project invite in one step)
  4. Picks role  → normie (default) / dev / viewer
  → createProjectShare(): writes projectShares + projectMemberships(invited)
    and, for host (a)/(b), pre-provisions/ensures the box + clones the repo
    onto it + ensures push creds are present (owner's, via existing
    git_push_creds / onboarding/apply — scoped, see §4).

Invitee (mobile/web)
  5. Notification "Dilan invited you to <repo>"  (reuse listHosts surface)
  6. Accept → materializes:
        - connection (accepted)
        - infraAccessGrant(host=owner, guest=invitee) scoped to the box,
          allowedProjects=[slug], role-preset policy   ← guests.accept path
        - projectMemberships.status=active, branch=yaver/<name>
  7. Lands directly in the coding surface, repo already there, agent ready:
        - mobile: repo-coding.tsx (GLM) attached to the box, or on-device
          clone if hostKind=managed but invitee prefers phone-local
        - web: GitView + terminal attached to the box
```

For host **(c)** "they pay," the invitee must have their own active Cloud
Workspace subscription. Accept records the repo/share relationship; the
workspace comes from the invitee's subscription/reconcile/placement flow, not a
direct wallet-funded provision route. The owner only shares the repo URL and,
optionally, a deploy key.

### 3.4 Git wiring for the normie loop: clone → test → commit → push → deploy

All five steps map to existing endpoints; the new work is sequencing + a PR
step + guards.

| Step | Mechanism | New work |
|---|---|---|
| **clone** | `POST /repos/clone` (box) or `cloneToPhone.ts` (phone) | pre-run at invite time; pin to `allowedProjects` |
| **branch** | `git checkout -b yaver/<name>` | auto-create on first edit; store on membership |
| **code** | GLM (phone) / Claude runner (box) | scope tools to project (already in `guest_config.go`) |
| **test** | run project test script via task/exec | normie-safe: run in isolation (feedback-only already auto-containerizes) |
| **commit** | `POST /git/commit-push` | set per-session `user.name/email` = invitee (new: author identity) |
| **push** | same endpoint, auto-rebase | **pin to feature branch** for normies; block direct main |
| **PR** | **GAP** — new `POST /git/pull-request` | create GH/GL PR from `yaver/<name>` → defaultBranch; owner notified |
| **deploy** | `yaver deploy` / `deploy_script_gen.go` | **role gate**: normie deploy → request-approval, owner one-taps |

**New agent endpoints (small):**

- `POST /git/pull-request {repoUrl, head, base, title, body}` — uses the
  on-box provider token (already present) to call GH `POST /repos/{o}/{r}/pulls`
  or GL `POST /projects/:id/merge_requests`. Lives next to repo-create in
  `git_provider.go`. Tokens never leave the box.
- `POST /git/identity {name, email}` (or a flag on commit-push) — sets the
  commit author so the normie's name lands on the history, not the box
  owner's. Per-session, derived from the invitee's `users` row.

**Web OAuth note:** web login requests GitHub `read:user user:email` /
GitLab `openid profile email` — **identity only, by design** (`web/lib/oauth.ts`).
Repo push capability comes from the **agent-side device flow** (`repo` /
`write_repository`), not the web identity. Keep them separate: a normie who
only ever codes on a *shared box* never needs to connect their own GitHub —
they push through the box's scoped creds onto their own branch. A normie who
wants to push under their **own** GitHub connects via the device-flow UI in
GitView (already built).

---

## 4. Privacy, security, and the credential question

The sharp edge: a normie pushing to the repo needs *some* git credential on
the host box. Options, least-trust first:

1. **Owner's token, branch-scoped, PR-only (default for "normie").** The box
   already has the owner's push creds. The normie can only push to
   `yaver/<name>` (agent enforces branch pin) and can only open a PR — never
   merge, never touch main, never deploy. The owner reviews + merges. This is
   the safest default and needs **no** token sharing with the normie.
2. **Normie connects own GitHub (device flow).** For a normie who *has* a
   GitHub account and should commit under their own identity with their own
   token. Token lands in their keychain (mobile) or the box's
   `git-credentials.json` keyed to their session — never Convex. Use when the
   repo grants them collaborator access directly.
3. **Deploy key / fine-grained PAT per project.** Owner mints a
   repo-scoped fine-grained token, stored in `yaver vault` on the box, used
   only for that `projectShare`. Revocable independently.

**Convex stays clean:** `connections`, `projectShares`,
`projectMemberships` carry only ids, slugs, repoUrl (a public-ish remote),
branch names, roles, timestamps. No paths, no tokens, no repo content. Add
all three tables' payloads to `fieldsWeForbidInAnyConvexPayload` coverage in
`convex_privacy_test.go` and assert `repoUrl` is normalized to
`host/owner/repo` (strip any embedded creds — `git_find.go` already has the
normalizer to reuse).

**Revocation cascade:** removing a `projectMembership` →
`revokeInfraGrantsBetweenUsers` (exists) + drop branch creds on the box.
Blocking a `connection` → revoke all shares + grants with that peer.

---

## 5. UI surfaces

### Web (`web/components/dashboard/`)
- `ConnectionsView.tsx` (new) — friends list, requests, search, suggested
  (from existing edges). The address book.
- `ProjectsView.tsx` (new or fold into existing) — shared projects, roster,
  per-member role + branch + status, "Invite" button.
- Reuse `GitView.tsx` for the actual git operations; reuse
  `ManagedCloudPanel.tsx` for the "host on cloud" branch of the chooser.
- Invite modal = repo picker + host chooser (own/cloud/cloud-they-pay) +
  connection picker + role.

### Mobile (`mobile/app/`)
- `connections.tsx` (new) — same address book, contact-style.
- `project-invite.tsx` (new) — accept screen ("Dilan invited you to <repo>")
  → role/scope summary → Accept → deep-link into `repo-coding.tsx`.
- Extend `repo-coding.tsx` to accept a `projectShare` context (pre-cloned,
  branch pinned, PR button instead of raw push for normies).
- Reuse `ManagedCloudCard.tsx` for the "they pay" provisioning path.

### CLI (`desktop/agent/`)
- `yaver connect <email|@handle>` / `yaver connections`
- `yaver project share <repo> --to <friend> --role normie --host my|cloud`
- `yaver project join <code>`
- These wrap the same mutations; keeps power users first-class.

---

## 6. Build phases

- **P0 — Social graph.** `connections` table + `connections.ts` +
  `ConnectionsView` (web) + `connections.tsx` (mobile) + backfill from
  existing edges. No git, no hosting. Ships the "friendships" the user asked
  for and de-risks everything downstream (every later invite picks a friend).
- **P1 — projectShares wrapper.** Tables + mutations that **compose**
  `guests.invite`/`accept` + `allowedProjects` + role presets. Invite modal +
  accept screen. Host = "my machine" only (reuse online owned device). No new
  enforcement.
- **P2 — Managed-cloud host option.** Wire host-chooser branches (b)/(c) to
  `cloudMachines.create` + wallet. "I pay" vs "they pay." Pre-clone on
  provision.
- **P3 — Normie git loop hardening.** Branch pin + per-session author
  identity + `POST /git/pull-request` + deploy role gate +
  request-approval-to-deploy.
- **P4 — Polish.** QR/handle discovery, notifications, suggested-friends,
  privacy-test coverage for the three new tables.

Each phase is independently shippable and each is mostly *wiring existing
endpoints*, consistent with "70% already exists, build the front door."

---

## 7. Open questions

1. **Mutual vs directed connections** — do we require accept (Facebook-style)
   or allow follow (Twitter-style)? Recommend mutual for the trust model
   (sharing a box is high-trust).
2. **`@handle` namespace** — add `users.handle` for human-friendly invites,
   or stay email-only at P0? (email is zero-new-infra; handle is nicer for
   normies who don't want to reveal email).
3. **PR merge** — do we ever let `dev` (not owner) merge? Probably yes for
   `dev`, never for `normie`.
4. **Deploy-by-normie** — request-approval (owner one-taps) vs hard-no.
   Recommend request-approval; it's the dogfood-friendly path.
5. **Phone-local vs box for the normie** — if hostKind=managed but the
   invitee is on iOS, do they code on the box (attach) or clone to phone
   (GLM)? Default to attach-to-box (full toolchain); offer phone-local as a
   fallback when offline.

---

## 8. One-paragraph pitch

A developer hits **Share project**, picks a repo, picks where it runs (their
machine or a one-tap Yaver Cloud box, paid by either side), picks a friend
from their connections, and picks a role. The friend gets "Dilan invited you
to *acme-app*," taps **Accept**, and is dropped straight into a coding
surface with the repo cloned, an AI agent ready, and a personal branch. They
describe what they want, the agent edits, they test in isolation, commit
under their own name, and open a pull request — all without touching a
terminal, a token, or `git` itself. The owner reviews the PR and deploys.
Everything sensitive (tokens, code, paths) stays on the devices and flows
P2P; Convex only ever learns *who is connected to whom* and *who can reach
which box for which project*.
