# Yaver Cloud — golden image, lifecycle state, fast mobile-driven config (DEEP ANALYSIS)

**Status:** analysis / design — 2026-06-07. Requested "deep deep analysis first."
Nothing here is built yet. Code is source of truth; claims anchored to file:line.
Covers three queued asks that form one cluster:

1. **Yaver Cloud image in Hetzner** — plug-and-play at instance creation, from mobile, per-hour.
2. **Convex keeps lifecycle state** — deleted / sleeping(stopped) / alive(active) + lastUp/deletedAt, ids only (no hackable info).
3. **Mobile-P2P git config + projects** → configure a new Hetzner box in seconds from mobile-local.

---

## 0. TL;DR / recommendations

- **The golden image has a hard split, because Hetzner snapshots are
  account-private.** Our prebuilt image only helps the **managed** plane (our
  account). For **BYO** (the user's own account/token) the user literally cannot
  see our snapshot — so BYO gets a **"bake once per account"** image + a fast
  ubuntu cloud-init fallback. Don't promise "our image boots in your account" —
  it can't.
- **Convex lifecycle state**: add a counter/id/timestamp-only `byoMachines` table
  (`serverId`, `state`, `lastUpAt`, `stoppedAt`, `deletedAt`, …). Same
  privacy class as `userProjects`/`devices`. **No token, ever.**
- **Fast config**: store a P2P "box profile" (git identity + repo list, NOT
  secrets); on provision, clone repos via cloud-init + push git creds
  device→device (`git_push_creds`, never via Convex). Sub-minute once the image
  is prebaked.

---

## 1. The Yaver Cloud golden image (Hetzner) — deep analysis

### 1.1 What exists today

- **Build pipeline:** `scripts/build-cloud-image.sh` (Hetzner / GCP / AWS). For
  Hetzner it: provisions a fresh Ubuntu 24.04 VM → pushes the `cloud-image/`
  overlay + `yaver` binary → runs `ci/remote/bootstrap.sh` (same as the test
  box) → **shuts down → captures a snapshot** → deletes the build VM → writes
  `dist/cloud-image/hetzner-<version>-<arch>.json` with `{imageId, …}`
  (`build-cloud-image.sh:1-26, 173-214`).
- **Boot-from-image (agent):** `hetznerCreateServerFromImage(token,name,plan,region,imageID)`
  (`cloud_stopstart.go:85`) and my `hetznerCreateServerCustom(...imageID,repoURL)`
  (`cloud_byo_provision.go`) both accept a numeric image id → fast boot. The
  `cloud_provision` ops verb already takes an `imageId` opt.
- **Boot-from-image (managed/Convex):** `hetznerCreateFromImage` in
  `cloudLifecycle.ts:281` (used by resume). Initial managed provision currently
  uses **cloud-init** (`cloudMachines.ts buildManagedCloudInit*`), i.e. slow
  first boot.

### 1.2 The load-bearing constraint: Hetzner snapshots are account-private

A Hetzner snapshot/image id is only visible to the **account that created it**.
There is no clean public-custom-image or cross-account share API. Consequences:

| Plane | Token / account | Can use OUR golden snapshot? | Fast boot path |
|---|---|---|---|
| **Managed** (Yaver Cloud, paid) | platform `HCLOUD_TOKEN`, our account | **Yes** — same account | boot from our golden snapshot id |
| **BYO** (their own Hetzner) | their vault token, their account | **No** — different account | bake-once-per-account, else ubuntu+cloud-init |

This means "we have a yaver cloud image in Hetzner, plug-and-play at instance
creation" is **true for managed**, and for **BYO** must become one of:

1. **Bake-once-per-account (recommended for BYO).** First BYO provision boots
   ubuntu + cloud-init (installs Yaver) — slow once (~3–5 min today). Then we
   **snapshot that box into the user's own account** and cache the resulting
   image id (agent vault + `byoMachines.imageId`). Every later spin-up boots from
   that per-account image → seconds. "Bake once, boot many."
2. **Optimized cloud-init fallback (always available).** No per-account image:
   download the prebuilt `yaver` binary (release asset) + minimal apt; target
   ~60–90s instead of 3–5 min. This is the universal path and the bake source.
3. ~~Share our snapshot to their account~~ — not viable on Hetzner. Reject.

For GCP/AWS the story is friendlier later (public images / AMI sharing exist),
but Hetzner-first means (1)+(2).

### 1.3 Image contents, versioning, security

- **Contents:** Docker, git, node/go/rust/python, expo/eas, the `yaver` agent,
  firewall — from `ci/remote/bootstrap.sh` + `cloud-image/` overlay. Same as the
  test box, so it's already exercised.
- **NO secrets baked in.** The golden image must contain zero tokens / keys /
  device identity — those are injected per-box at boot (cloud-init user_data /
  `/machine/onboarding/apply`). A baked secret would leak to every box and (for
  BYO, snapshots living in the user's account) across the trust boundary. Add a
  build-time assertion + a test that greps the captured image overlay for
  secret-shaped strings before publishing.
- **Versioning:** `dist/cloud-image/hetzner-<version>-<arch>.json` already
  carries `imageId` + `version`. Managed reads the latest via env
  `YAVER_CLOUD_IMAGE_ID` (per deployment) or a published `GET /cloud-images`
  Convex route. Arch: `cax*`=arm64, `cpx*/cx*`=amd64 — pick by plan.
- **Rebuild cadence:** rebuild on agent release; old image ids stay valid
  (resume/restore from older snapshots must still work).

### 1.4 What to build (image)

- **Managed fast boot (high value, our account):** thread `YAVER_CLOUD_IMAGE_ID`
  into `cloudMachines.provision` so initial provision boots the golden snapshot
  instead of cloud-init. (Touches the parallel-owned `cloudMachines.ts` —
  coordinate.) Per-hour billing already exists via the wallet/meter.
- **BYO bake-once:** after a BYO box reaches "ready", snapshot it into the user's
  account, cache `imageId` (vault + `byoMachines`), and have `cloud_provision`
  prefer that cached id. New verb `cloud_bake` (snapshot a ready box as the
  reusable image) or auto-bake on first ready.
- **`GET /cloud-images`** (optional): publish current golden image ids per
  provider/arch so the managed app picks them without an env redeploy.

---

## 2. Convex lifecycle state — `byoMachines` (ids + state + timestamps only)

### 2.1 Why it's allowed

The privacy contract permits identity/discovery/bookkeeping (`devices`,
`userProjects` = slug+deviceId+flags, **no paths/secrets**). A machine row with
provider id + state + timestamps is the same class — **no token, no key, no
absolute path**. Pin the field names in `convex_privacy_test.go` like the wallet
+ companion tables already are.

### 2.2 Schema

```ts
byoMachines: defineTable({
  userId: v.id("users"),
  provider: v.string(),            // "hetzner" | "digitalocean"
  serverId: v.string(),            // provider numeric id (NOT a secret)
  deviceId: v.optional(v.string()),// once the box self-registers
  name: v.string(),
  region: v.optional(v.string()),
  plan: v.optional(v.string()),
  serverIp: v.optional(v.string()),// the user's OWN public box ip (managed cloudMachines already stores this); omit if you want to be maximally conservative
  imageId: v.optional(v.string()), // baked per-account golden image (fast re-spin)
  snapshotImageId: v.optional(v.string()), // current stop snapshot to resume from
  state: v.union(
    v.literal("active"),           // alive / running
    v.literal("stopped"),          // sleeping (snapshot kept, server deleted)
    v.literal("deleted"),          // gone
  ),
  createdAt: v.number(),
  lastUpAt: v.optional(v.number()),   // last time it went active
  stoppedAt: v.optional(v.number()),  // last sleep
  deletedAt: v.optional(v.number()),  // tombstone
  updatedAt: v.number(),
}).index("by_user", ["userId", "updatedAt"])
  .index("by_user_server", ["userId", "serverId"]),
```

`state` answers "deleted / sleeping / alive"; the three timestamps answer "latest
up / deleted at". **Forbidden here:** token, password, ssh key, any path.

### 2.3 Who writes it — single source of truth

Two options; recommend **both, idempotent**:

- **Agent-side (robust, works headless):** after a BYO op the agent calls
  `convexSyncer.callMutation("byoMachines:upsert", {serverId, state, ...})`.
  `cloud_provision` (my file) can do this directly; `cloud_stop`/`cloud_start`
  live in `cloud_stopstart.go` (other author) — either add one sync line each
  (small, coordinated) or let the reconcile path below cover them.
- **Reconcile (self-healing):** a `cloud_list` → diff → `byoMachines` upsert that
  marks servers no longer present as `deleted`, refreshes `active`. Run on app
  open + after ops. This keeps Convex truthful even if a direct write was missed.

Mobile reads via `GET /byo/machines` for the lifecycle UI ("alive/sleeping/
deleted", last-up timestamps) across all the user's devices.

### 2.4 Security

`byoMachines` is per-user-scoped (every read/write checks `userId ===
session.userDocId`). It stores only the user's OWN server ids/state. Even fully
public, a row reveals a server id + state — not a credential, and you still need
the (vault-only, never-synced) token to act on it. One user can't see or touch
another's because rows are session-scoped and the **token never leaves the
owner's agent**.

---

## 3. Mobile-P2P git config + projects → configure a new box fast

### 3.1 What exists

- **Projects:** `userProjects` (Convex, slug+deviceId+stack+branch, **no paths**,
  `schema.ts`); mobile `projectStore.ts` / `phoneProjects.ts` local stores.
- **Git creds:** `git_push_creds` ops verb pushes GitHub/GitLab tokens
  **device→device** via `/machine/onboarding/apply` — **never through Convex**
  (`ops_git.go:27`). `git_oauth_*` device-flow persists to
  `~/.yaver/git-credentials.json` on the agent.
- **Repo clone on boot:** managed cloud-init clones `repoUrl`
  (`cloudMachines.ts`); my `cloud_provision` clones `repoUrl` on the BYO box.

### 3.2 Design — a P2P "box profile"

A small, non-secret profile the mobile app holds (and mirrors to the agent vault)
and replays onto any new box:

```
boxProfile = {
  git: { userName, userEmail },          // non-secret identity
  repos: [ { url, branch?, dir? } ],     // what to clone
  setup?: [ "npm i", "..." ],            // optional first-run commands
}
```

- **Stored P2P:** on the phone (local store) + agent vault — NOT in Convex
  (Convex keeps only the non-secret `userProjects` summary). Git **tokens** stay
  in the vault / `git-credentials.json` and move device→device only.
- **Fast configure on provision:**
  1. `cloud_provision { repoUrl, imageId }` → box boots (from the per-account
     baked image = seconds) and shallow-clones the first repo.
  2. Mobile calls `git_push_creds { deviceId }` → installs the user's git
     tokens on the new box (private repos now clone/pull).
  3. Mobile replays the profile: `git config user.name/email`, clone remaining
     repos, run `setup` — over the agent P2P channel (`/ops` exec), not Convex.
- **"Use mobile-local to configure it quickly":** the phone is the orchestrator;
  the profile lives on the phone, so even a brand-new box is configured from the
  phone in one tap, P2P, no round-trip to our servers.

### 3.3 Security

- Git tokens: device→device only (`git_push_creds`), never Convex — unchanged.
- Profile (name/email/repo URLs): non-secret; phone-local + vault. Repo URLs in
  Convex `userProjects` already (no creds).
- The new box authenticates as the user (per-box 1-year token minted at
  provision), so pushing the profile is owner-only.

---

## 4. Phased plan

- **P1 — Convex lifecycle state** (`byoMachines` + upsert mutation + `GET
  /byo/machines` + privacy-test pin + agent/mobile writes + reconcile). Smallest,
  high value, unlocks the "alive/sleeping/deleted + timestamps" UI.
- **P2 — BYO bake-once image** (`cloud_bake` / auto-bake on ready → cache imageId
  in vault + `byoMachines`; `cloud_provision` prefers it). Fast re-spin on the
  user's account.
- **P3 — Managed golden-image fast boot** (`YAVER_CLOUD_IMAGE_ID` into
  `cloudMachines.provision`; coordinate on the parallel-owned file).
- **P4 — Box profile + one-tap configure** (profile store + replay on provision +
  `git_push_creds` integration). 
- **P5 — Optimized cloud-init fallback** (~60–90s) + `GET /cloud-images` +
  build-time no-secrets assertion.

## 5. Open decisions (need you)

1. **Store `serverIp` in Convex `byoMachines`?** Managed `cloudMachines` already
   stores it; it's the user's own public box (not a credential). Include for a
   usable UI, or omit to be maximally conservative? (Recommend: include.)
2. **BYO image strategy:** bake-once-per-account (P2) vs ubuntu fast cloud-init
   only (P5)? (Recommend: both — fallback always, bake for speed.)
3. **Who writes `byoMachines`:** agent-side sync (needs 2 lines in the other
   author's `cloud_stopstart.go`) + reconcile, or mobile-records-only? (Recommend:
   agent provision + reconcile; coordinate the 2 lines.)
4. **Scope now:** build P1 (state) first, or P1+P4 (state + fast config), or all?
