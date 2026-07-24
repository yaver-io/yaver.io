# Yaver.io — Claude Code Project Guide

## Read This First

**Markdown files drift. Code is the source of truth.** This file,
`docs/architecture/AI_ARCH.md`, `docs/architecture/REMOTE_WORKER.md`, every
other `*.md` — accurate when written, stale by the next handler rename. Before
acting on any `.md` claim:

1. **grep the actual code.** Routes referenced in docs may exist as functions
   without ever being wired to the mux (we shipped `yaver diagnose` like that
   in 1.99.33).
2. **Verify versions.** `yaver --version` (binary on PATH) vs `/info.version`
   (running daemon) vs `git log -- <file>`. If they disagree, the doc matches
   none of them.
3. **Re-read the file on disk, not in the doc.** Other threads bump constants
   in parallel.
4. **When the doc and the code disagree, the doc is the bug.** Fix it in the
   same change.

`docs/architecture/AI_ARCH.md` is the runtime-architecture reference for auth,
bootstrap, relay, mobile discovery, and remote-recovery — read it before
changing those.

## Hard rules

- **THE SNOWBALL PRINCIPLE — fix the PRODUCT, never the machine.** This is
  Yaver's development philosophy and it outranks finishing the immediate task.
  When something is stuck — a box, a phone, a build, a variable, a piece of
  state — the tempting move is to unstick *that instance* and move on. Don't.
  Mutating one machine back to health teaches the product nothing and
  guarantees the next user meets the same wall with no more help than you had.
  Every stuck state is a product requirement you have not written yet.
  The order is always:
  1. **Resolve it first** — get the user unblocked, on the real box, now.
  2. **Then ask: why did the product let this happen, and why didn't it say
     so?** Almost always the answer is that something reported success, or
     reported nothing, while the operation was impossible.
  3. **Land the change that makes it impossible or self-evident next time** —
     in the Go agent, the mobile app, the relay, the CLI, the web, and the
     wiring between them. Not in a runbook, not in a doc, not in your memory.
  4. **Prove the guard works** by breaking it: disable the fix and watch the
     test fail. A guard you have not seen fail is a guess.
  Worked example from 2026-07-24: a Mac mini looked "online" to the phone while
  its relay said 502, Projects said "No projects yet", and every deployed fix
  seemed to do nothing. Re-authing that one box would have "fixed" it in a
  minute and taught the product nothing. The real deliverables were: `serve`
  now logs one guaranteed line before anything that can fail (a silent serve is
  unfalsifiable), bootstrap mode announces that it is NOT serving and names the
  command that fixes it, the discovery walk is bounded from OUTSIDE the walk
  (a deadline you can only check inside a callback is not a bound), and every
  subprocess got a timeout plus `WaitDelay` (a context kill does not free you
  while a grandchild holds the pipe). Those ship to every user; the re-auth
  helped exactly one machine.
  **Applies to the whole stack, equally.** A fix that lands in one of two
  browser-preview implementations is not landed — the same drift shipped a
  broken heartbeat, dropped SSE frames, and a dead shake gesture in `apps.tsx`
  while `DevPreview.tsx` was fine. Cross-surface parity (below) is the same
  rule wearing a different hat.
- **Every incident must leave the product harder than it found it.** When you
  debug a real failure — yours, a user's, or a past session's — fixing the
  immediate symptom is only half the job. Before you call it done, ask: *"what
  would have told me this in ten seconds instead of an hour?"* and then build
  that into Yaver. This is not optional cleanup; it is the deliverable. The
  standing pattern:
  1. **Encode the diagnosis where the agent already looks.** A new `doctor`
     probe (`desktop/agent/doctor_*.go`), an ops verb, a preflight in the
     deploy script. If the check exists but was GREEN during the incident, it
     was a *false green* — fix the check, don't add a second one beside it.
  2. **Probe the real capability, never the proxy.** The whole class of bugs
     here is "the inventory says yes, the operation says no": a certificate is
     present but cannot sign, a tool is on PATH but is a stub, a device is
     `online` but unreachable, a deploy key resolves but the file is gone. If
     you can only learn the truth by attempting the operation, attempt it.
  3. **Carry the *why* into the error text.** The remedy string should name the
     specific fix, not "check your configuration". The cost of a vague error is
     measured in whole sessions — see `errSecInternalComponent` (2026-07-19),
     where the obvious reading ("keychain locked, need the login password") was
     wrong in a way that wasted a session and would have wasted another.
  4. **Ship it to every surface.** Go agent + MCP verb + CLI + web + mobile.
     A diagnosis only the CLI can see doesn't exist for a user on their phone.
     See "Cross-surface parity" below.
  5. **Self-heal when the fix is unambiguous and idempotent**; ask when it
     isn't. Unlocking a keychain the operator explicitly configured is a
     no-brainer. Guessing at passwords or mutating account state is not.
  6. **Write the postmortem into the code, not just the commit message.** The
     file-top comment blocks in `doctor_build_deep.go` and
     `doctor_build_signing.go` are the model: each bullet is a real incident,
     stated as the false green it produced.
- **Never push or commit without explicit user permission.**
- **Committed and pushed work is IMMUTABLE unless the user says otherwise.**
  Once a change is committed — and certainly once pushed — nobody reverts it,
  force-pushes over it, `git checkout --`s it away, or drops it in a rebase
  without the user explicitly asking. This is not a style preference: work has
  already been lost this way. On 2026-07-18 a session's `backend/convex/`
  changes were reverted off disk *after they had been deployed to Convex prod*,
  leaving prod carrying a schema the source tree no longer contained — the next
  deploy from that tree would have dropped live fields. If you believe a
  committed change is wrong, say so and propose a NEW commit that fixes it.
  Never quietly un-land someone's work.
- **Many sessions are always running. Assume concurrency; isolate for it.**
  Multiple Claude/autorun threads share this machine and this repo at all times.
  - **Work on a branch, then merge.** This supersedes the older "always work
    directly on main" habit. A branch per unit of work (`wake-ux`, `forge-seam`)
    keeps concurrent threads from interleaving half-finished edits in one tree.
    Finish → rebase onto latest `main` → merge → push. Conflicts get resolved,
    never bulldozed (see the immutability rule above).
  - **`git commit -- <paths>` ALWAYS.** Never `-a`, never `add -A`. The index is
    shared and goes stale between two consecutive commands, so a bare commit
    sweeps a sibling's staged files. Pathspec commits are the only atomic form.
  - **Autorun gets its own clone, never a shared checkout.** A dirty shared tree
    kills a run at iteration 0. Clone to `~/Workspace/yaver-<topic>-autorun`,
    give it its own branch, and pass task paths as ABSOLUTE paths.
  - After any deploy from a shared checkout, `grep` the source for a symbol you
    added to confirm it is still there. A green deploy is not evidence the code
    survived the next five minutes.
- **Hetzner = metered, NEVER monthly. Never leave a server running.** Hetzner
  bills servers hourly (metered) up to the monthly cap, and bills even
  **stopped** servers — only **deleting** a server stops the meter. NEVER launch
  a Hetzner box and leave it running (or merely stopped) to accrue the monthly
  charge. Every provisioned box MUST be **scale-to-zero**: preserve state
  first, then **DELETE** the server when idle. The preferred Cloud Workspace
  path keeps mutable state on a Hetzner Volume and wakes from a slim base image;
  snapshot+delete remains the legacy/hosted recovery fallback. You pay only for
  active hours + cheap parked storage. No always-on servers for beta/dev/test.
  Before provisioning, confirm the volume-backed or snapshot-backed teardown
  path exists; delete every test/builder box the moment it's done. The user's
  standing directive (2026-06-20): "always pay metered, never ever launch
  something Hetzner will charge monthly automatically."
- **Repo/resource boundary: touch only Yaver project resources.** When working
  in this `yaver.io` repo, agents may modify only files, cloud resources,
  Convex rows, Hetzner servers/snapshots/volumes, LemonSqueezy state, and other
  external resources that are clearly owned by the Yaver project. Do **not**
  delete, revoke, rename, stop, snapshot, migrate, or otherwise mutate personal
  machines, private sibling-project resources, generic `ubuntu-*` boxes, or
  storage volumes unless the user explicitly says that exact resource belongs
  to the Yaver task and should be changed. Before any destructive provider or
  Convex cleanup, list the candidates and verify their Yaver-specific labels,
  names, IDs, subscription links, or `cloudMachines` rows. Ambiguity means stop
  and ask.
- **Do no harm to third parties — it is not our purpose.** Yaver exists to lower
  the *user's own* dev/ops cost, not to attack, scrape-abuse, or burden anyone
  else's infrastructure. A datacenter IP (Hetzner/AWS/…) that hammers a third
  party gets the whole account suspended — and it's wrong regardless. Concretely:
  1. **No scanning/attacking hosts you don't own.** `nmap`, `port_scan`,
     `arp_scan`, brute-force, floods, probes are for the user's OWN LAN/hosts or
     explicitly authorized targets only. Never point them at a third party.
  2. **Collect public data respectfully.** Don't spoof a browser
     `User-Agent`/`Origin`/`Referer` to defeat bot detection; don't bypass
     `robots.txt`, rate limits, WAFs, or geo/IP blocks. Prefer official/keyless
     APIs. **Back off on 403/429/451 and stop** — a block is a "no", not a
     puzzle. The collection layer records blocks as findings; it must never
     rotate IPs or impersonate to route around them.
  3. **Use the right resource, not the loudest one.** Move third-party reads off
     a flagged datacenter IP onto the user's *own* devices/residential vantages
     (`runtime: mobile_user_present`), distributed friend-roster runners, or an
     official API — that's what `collection_plan` runtime selection and the Task
     Packages phone-runner are for. Don't sustain a 24/7 scrape loop from one
     cloud box.
  4. **Peer-egress lends your OWN machines' egress only** — default-off,
     same-user, RFC1918-blocked, never an open relay, never ban/geo evasion.
  5. **If you build a loop that hits an external service:** identify honestly,
     cap concurrency, jitter + back off, make it killable, and stop on a block.
     When in doubt, don't. See `desktop/agent/access_policy.go` (Policy Guard)
     and `egress_proxy.go` (anti-pivot). Mirrored in `../yaver-bet/CLAUDE.md`.
- **Streaming is a neutral tool — like OBS.** Yaver *helps you stream* whatever
  source you point it at (capture card / satellite / set-top box / console /
  camera / screen) to your **own account or an explicitly-invited guest
  account** — never public by default. Yaver is **content-agnostic**: it does
  not inspect, classify, block, or police what's on the wire. If a source is
  dark or HDCP-blanks itself, Yaver streams that as-is (a terse diagnostic hint
  is fine; do **not** litter the code/UI with warnings). **What you capture and
  stream, and the right to do so, is the user's responsibility** — exactly as
  with OBS. The one line in *our* code: Yaver adds **no** DRM/HDCP
  circumvention (no stripper); it passes through exactly what the hardware
  gives. Note: Yaver's aim is utility, **not** privacy — don't sell or gate
  streaming features on a privacy promise (the Convex privacy contract below is
  a separate, data-storage constraint and still holds).
- **Destructive paths**: before `rm -rf`, `git clean -fdx`, `find -delete`,
  `mv` over an existing dir:
  1. `ls -la <path>` first; show what's about to be deleted.
  2. Absolute paths with exact case. macOS is case-insensitive — `~/workspace`
     matches `~/Workspace`. This already wiped a repo once.
  3. Confirm before deleting under `$HOME`, a git repo root, any path computed
     from a variable, or any path transcribed from a user message.
  4. Treat `rm -rf` on anything you didn't create this session like
     `git push --force` to main.
- **Cloudflare deploy size guard**: `web/` must stay under 15 MB (raised from
  10 MB in ddd5868d — demo videos push it over; enforced identically in
  `scripts/deploy-web.sh` and `release-web.yml`). If you add video, compress
  first (`ffmpeg -crf 32 -vf scale=720 -an`) or host external.
- **Deploys and cloud tools cost money — coalesce, never spray.** Vercel billed
  us harshly per build/bandwidth; that is why `web/` runs on Cloudflare Workers
  (`@opennextjs/cloudflare` + `wrangler`). **Cloudflare is cheaper, not cheap.**
  Every deploy is metered somewhere — Workers requests, Convex function calls,
  GitHub Actions minutes, and TestFlight's hard **~15–20 uploads/app/day** cap
  (which has no rollback: a bad build can only be superseded). Treat a deploy
  like a Hetzner hour, not like a save:
  1. **One deploy per converged change — never one per iteration.** When several
     autorun loops or queued tasks (p0…p9) touch the same target, they
     `commit` + `push` only; the **last** one deploys, once, after the whole
     queue converges. N loops must not mean N deploys.
  2. **Never deploy to "check" something.** Use the local dev server, `wrangler
     dev`, or a preview. A deploy is not a build step.
  3. **Be quota-aware before you are quota-blind.** Read the remaining budget
     (TestFlight uploads today, CI minutes) and park the run rather than burn
     it. Out-of-quota is a state that needs a human, not a retry.
  4. Cost-awareness is a product requirement, not just a house rule — it is the
     whole "lower dev opex" wedge. Cloud tool usage and deploys should report
     what they cost (`remote_cost`, `switch_cost` are the existing seams).
- **Never WebView for third-party RN apps.** Use the Hermes-bundle native load
  path (`/dev/build-native` → ExpoReactNativeFactory). WebView is OK for plain
  web content (landing pages, docs).
- **Yaver is not single-user — never hardcode a path, username, or home dir.**
  A remote box can be any OS, any user, any layout. Resolve at runtime
  (`os.UserHomeDir()`, `filepath.Abs`, explicit config). A literal
  `/Users/<name>` or `/home/<name>` outside a deliberately-fixed system path
  (`/home/linuxbrew`, a container tenant root) is a bug. **And never let the
  daemon's CWD stand in for a missing path** — "unspecified" means *unknown*,
  not "use whatever directory I happen to be sitting in". On 2026-07-20
  `workDir` defaulted to `"."`, which was the agent's CWD — the user's HOME —
  so every `POST /tasks` recursively classified the whole home tree and never
  returned. The phone showed "the machine accepted the connection but never
  answered" while the box was idle with three ready runners.
  Guard: `desktop/agent/task_placement_scan_bounds_test.go`.
- **Advisory work must never sit in the critical path of the operation it
  annotates.** Placement labels, project classification, repo metrics and
  telemetry must carry a wall-clock deadline and degrade to empty rather than
  block. A depth limit is not a bound — breadth defeats it; only a deadline
  bounds wall-clock. Same lesson as the `/tasks` payload incident: bounding the
  proxy (rows, depth) never bounds the thing that actually hurts (bytes, time).
- **Never commit credentials, infra IPs, or hostnames.** The repo is public on
  GitHub. Apple keys, Hetzner IPs, npm tokens, Play service-account JSON,
  relay passwords, Tailscale IPs — all gitignored / GH secrets only. If a
  secret was ever committed: rotate it AND `git filter-repo --replace-text`
  before pushing.
- **The relay is MULTI-TENANT — a hostile relay user must NEVER reach another
  user's box or phone.** The Yaver free relay (and Relay Pro) are shared by many
  unrelated users, and the code is open source, so an attacker reads everything.
  **Security therefore rests on KEYS, not on secret request shapes — never add a
  "security" that a source reader defeats.** Non-negotiable invariants (see
  `docs/architecture/ROBUST_TRANSPORT_SSH_QUIC.md` §4d +
  `SECURE_FRICTIONLESS_TRANSPORT_SETUP.md`): (1) the relay is **pass-through +
  access-graph-scoped** — it forwards ciphertext, authorizes nothing, holds no
  device keys, and bridges only within the **same owner/access-graph** (the same
  userID check as the `already registered` eviction in `relay/server.go`); (2) a
  box authenticates the **client's device key, public-key ONLY**, against its own
  `# yaver-managed` set — a stranger's key isn't there, so the handshake fails,
  and a *fully compromised relay still can't get in* (it has no key; the phone key
  lives in the Secure Enclave); (3) SSH / reverse-SSH channels are **forced-command
  cages** (no shell/pty/forward/subsystem — `ssh_control_server.go`,
  `ssh_session_cmd.go`); (4) **Free vs Pro is NOT a security boundary** — identical
  auth, Pro only buys capacity. Any new transport/relay/mesh code must preserve all
  four; a change that lets tenant A's traffic reach tenant B's agent, or that
  trusts the relay/tier to authorize, is a security bug even if it "works".
- **No PRIVATE data in the codebase — ever. PUBLIC key material may live in code;
  private material may not.** Yaver is public open source: a hacker reads every
  tracked file. So the split is cryptographic, not vibes-based:
  - **Private → secret store, never tracked.** Private keys, certificates *with*
    their private key, session/auth tokens, passwords, provider API credentials,
    relay passwords, signing keys → **GitHub Actions secrets**, **Convex env**,
    or the **local encrypted `yaver vault` / `~/.yaver` 0600 store** (or the
    device **Secure Enclave / Keychain**). If leaking it lets someone
    impersonate, decrypt, or sign, it is private.
  - **Public → codebase is fine.** *Public* keys, *public* certificates, relay/
    mesh **public** identities, host-key **fingerprints to pin** — these are
    verifiers, safe to ship in the repo (that is the whole point of public-key
    crypto). Shipping a public key is not a leak.
  - The test before committing any key/cert/credential: *"if an attacker reads
    this exact bytes from the public repo, can they get in or forge?"* Yes →
    secret store. No (it only lets them *verify*) → code is acceptable.
  This generalizes the "never commit credentials" rule to the open-source reality
  and to the new SSH/mesh key material.
- **Public docs use machine aliases**, not real infra labels. Prefer
  `primary`, `selected-machine`, `your-box`, `example-host` in examples.
- **Hetzner test box (`yaver-test-ephemeral`)** is disposable. Its IP, SSH
  key material, `hcloud` token, real device IDs never go in tracked files.
  Refer to it in code/docs only as `yaver-test-ephemeral`.
- **Local deploy first, CI second.** Every deploy that can run on this Mac
  should run on this Mac. Yaver's wedge is "lower dev opex" — defaulting to
  GitHub Actions burns CI minutes you don't need to spend, slows down
  iteration, and re-introduces the SaaS roundtrip you're trying to remove.
  Use CI only when the deploy genuinely cannot work locally (a Linux-only
  toolchain that isn't on macOS, a runner that needs a secret you don't
  have on this machine, etc.) — and when you do, say so explicitly.

  | Target | Local command (preferred) | CI fallback |
  |---|---|---|
  | npm (`yaver-cli`) | `cd cli && npm publish` | `release-cli.yml` |
  | TestFlight (iOS) | `./scripts/deploy-testflight.sh` | local-only by design |
  | Google Play internal | `JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh && PLAY_STORE_KEY_FILE=keys/google-play-service-account.json python3 scripts/upload-playstore.py` | `release-mobile.yml` (android job) |
  | Convex backend | `cd backend && npx convex deploy --yes` | not wired to CI |
  | Cloudflare web | `./scripts/deploy-web.sh` | `release-web.yml` |

  When the user asks to ship, run the local command — don't push a tag and
  let CI do it unless the user explicitly says "use CI". If a local deploy
  fails for a reason that CI would also hit, fix the root cause; don't
  switch to CI as a workaround.

## Distribution — npm only

As of 1.99.124, **`npm install -g yaver-cli`** is the **only** supported install
path on every platform: macOS (Apple Silicon + Intel), Linux (x64 + arm64,
including Raspberry Pi / ARM cloud), and Windows via WSL2.

The npm package detects the platform and downloads the matching, signed +
notarized agent binary into `~/.yaver/bin/<version>/<platform>/yaver`. macOS
binaries are Developer ID + hardened runtime + notarized — Gatekeeper passes
on first run.

The previous distribution paths — apt repo, Homebrew tap, Scoop bucket, AUR,
Winget, Chocolatey, raw tarballs, install.sh, Docker image — are **all
removed**. Their repos (`kivanccakmak/{homebrew,scoop,aur,apt}-yaver`) are
archived read-only with a deprecation README.

`release-cli.yml` only does: validate → publish-npm → publish-mcp-registry →
build (with darwin sign+notarize) → release. Don't add deb/rpm/dmg/brew/scoop
steps back without an explicit ADR.

```bash
# install (any supported platform)
npm install -g yaver-cli

# update
npm install -g yaver-cli@latest

# headless (Pi / VPS / SSH-only) — short code + URL, sign in from any browser
yaver auth --headless
```

## Repository

- **Source of truth**: `github.com/yaver-io/yaver.io` (open source). Owned by
  the `yaver-io` org since 2026-07-17 — it was `kivanccakmak/yaver.io` before,
  and GitHub still redirects the old URL, so a stale remote keeps working and
  will quietly hide its own staleness. Only one remote here — `github`, over
  **SSH** (`git@github.com:yaver-io/yaver.io.git`). `branch.main.remote=github`,
  so plain `git push` works. No GitLab mirror.
- **Tags trigger releases**: `cli/v*` → release-cli.yml, `mobile/v*` →
  release-mobile.yml, `web/v*` → release-web.yml. Tag protection is a repo
  ruleset (`release tag protection`); it survived the org transfer, as did all
  61 Actions secrets — verified by diffing before/after.
- **Cloudflare web deploy**: `./scripts/deploy-web.sh` (size-guarded at 15 MB,
  uses `@opennextjs/cloudflare` + `wrangler deploy`).
- **Convex backend deploy**: `cd backend && npx convex deploy --yes`. Not
  triggered by CI — deploy explicitly when schema or HTTP routes change.

## Secrets

Three places. Never anywhere else. Never in a tracked file or git history.

1. **GitHub Actions secrets** (CI). `gh secret list -R yaver-io/yaver.io`
   for the canonical list. Includes:
   `ANDROID_KEYSTORE`, `ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`,
   `ANDROID_KEY_PASSWORD`, `APPLE_CERTIFICATE_P12`,
   `APPLE_CERTIFICATE_PASSWORD`, `APP_STORE_CONNECT_API_KEY`,
   `APP_STORE_CONNECT_KEY_ID`, `APP_STORE_CONNECT_ISSUER_ID`,
   `APPLE_TEAM_ID`, `PLAY_STORE_SERVICE_ACCOUNT_JSON`, `NPM_TOKEN`,
   `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID`, `CONVEX_DEPLOY_KEY`,
   `RELAY_PASSWORD`, `HCLOUD_TOKEN`, `HETZNER_TEST_SERVER_*`, etc.
2. **Local gitignored files** (dev machine): `.env.test`,
   `mobile/android/keystore.properties`, `keys/*`. All in `.gitignore`. Never
   force-add.
3. **Runtime env vars** (ad-hoc scripts). Scripts exit 2 if required vars are
   missing — never fall back to a hardcoded default.

If you find yourself about to put a secret in a tracked file, stop. Add as
GitHub secret + read from env. If it ever reached your clipboard, rotate.

`yaver vault` is for project-scoped local secrets that the daemon needs at
runtime. `yaver vault add APP_STORE_KEY_ISSUER --project mobile --value <uuid>`
etc. — vault is encrypted with your auth-token-derived key, so it locks if
your token rotates.

## Privacy contract — what lives where

Yaver's promise: Convex stores identity, peer discovery, and session
bookkeeping only. Anything sensitive or work-derived stays on the user's
devices and flows P2P.

**Allowed in Convex**: `users`, `sessions` (token hashes only), `sdkTokens`
(hashes only), `devices`, `relayServers`, `platformConfig`,
`guestInvitations`, `guestAccess`, `teams`, `teamMembers`, `userProjects`
(slug + deviceId + flags + branch — **no absolute paths**), activity audit
summaries (action + target + outcome + timestamp).

**Forbidden in Convex** (enforced by `desktop/agent/convex_privacy_test.go`):
vault values, raw tokens / API key plaintext, task input prompts or stdout,
file contents, exec session output, absolute filesystem paths (they leak the
user's home-dir username), customer LAN IPs.

All Convex-bound calls go through `convexSyncer.callMutation`. The privacy
test enumerates forbidden keys (`path`, `workDir`, `token`, `stdout`,
`output`, `vaultValue`, `secret`, …) AND scans for path leaks (`/Users/`,
`/home/`, `/root/`, `C:\Users\`). New sync paths must add their fields to
`fieldsWeForbidInAnyConvexPayload` and a test for the payload.

## Monorepo rule

**Yaver is ONE monorepo — this one.** Agent, CLI, mobile, watch, TV, car,
AR/VR, web, relay, backend, SDKs all ship together here. Don't propose
splitting a surface out: they have to agree on protocol and version, and
separate repos would only buy skew.

The `yaver-io` org holds this repo plus **validation / use-case apps** —
anything whose job is to exercise Yaver from the OUTSIDE. Those get their own
repos on purpose: a fixture that lives in here inherits this repo's tooling and
stops being an honest test of what a user's project actually hits.

**Rule: product code here; anything that tests the product from outside gets
its own repo.**

## Validation apps live OUTSIDE this repo

The todo fixtures are their own public repos, not `demo/` subdirectories:

| Repo | Stack | Reach | Feedback SDK |
|---|---|---|---|
| [yaver-todo-rn](https://github.com/yaver-io/yaver-todo-rn) | Expo / RN | Hermes (HBC swap) | ✅ `yaver-feedback-react-native` |
| [yaver-todo-kt](https://github.com/yaver-io/yaver-todo-kt) | Android / Kotlin | `native-webrtc` | ❌ **none exists** — viewer-triggered only |
| [yaver-todo-swift](https://github.com/yaver-io/yaver-todo-swift) | iOS / SwiftUI | `native-webrtc` | ❌ **none exists** — viewer-triggered only |
| [yaver-todo-flutter](https://github.com/yaver-io/yaver-todo-flutter) | Flutter | `native-webrtc` | ✅ `yaver_feedback` (pub.dev) |
| [yaver-todo-web](https://github.com/yaver-io/yaver-todo-web) | Next.js | dev server / HMR | ✅ `yaver-feedback-web` |

Same todo UX five ways, on purpose: the app is the control, so any difference
you observe is the transport. Local-only — no backend, no accounts, no network.

Three facts worth not rediscovering:
- **Hermes is RN/Expo-only**, gated at `hotreload.tsx:77`. Flutter is classed
  `DevServerKindWeb` (`devserver_kind.go:37`). Native and Flutter apps can never
  load into the Yaver container — they take `native-webrtc` or a standalone
  install via `yaver wire push`.
- **There is no native Kotlin/Swift feedback SDK.** `sdk/feedback/` ships
  react-native, web, flutter, unity, browser-extension. Don't import one.
- **The viewer owns the feedback trigger** for streamed apps:
  `remote_runtime.go:709` `case "launch-feedback"` pushes
  `feedback-launch-request` down the events DataChannel. That's how kt/swift get
  the loop without an in-app SDK.

## Three-part architecture

1. **Mobile app** (App Store / Play Store) — native container for testing
   third-party RN apps via Hermes push. AI agent control. HTTP server on
   port 8347 for `yaver-cli` push-to-device.
2. **`yaver-cli` (npm)** — third-party RN devs push their own projects to
   the phone via `yaver push`. No agent needed; talks directly to the phone's
   8347 server. Same npm package.
3. **Desktop agent (Go)** — same `yaver-cli` package. P2P, relay, MCP, hot
   reload (Expo / Flutter / Vite / Next.js), session transfer, builds, vault.
   Used to drive AI agents from your phone.

## Connection strategy

Direct-first, relay-fallback, per surface:

| Surface | Strategy |
|---|---|
| Mobile app | LAN beacon (UDP 19837) → Convex-known IP → relay. WiFi → direct first; cellular → relay only. |
| Desktop Electron | Local IPC (`localhost:18080`) → direct LAN → relay. |
| Web dashboard | Relay only (browser CORS blocks LAN). |
| Go CLI | Local daemon by default. `yaver connect` / `yaver code --attach <device>` resolve a remote and tunnel. |

Each surface stores its own session token independently. The same OAuth user
can be signed into all surfaces simultaneously. Sessions are 1-year, refreshed
on every heartbeat. Sign-out on one surface doesn't affect others.

## Daily commands

```bash
# auth + serve
yaver auth                           # OAuth (Apple / Google / GitHub / GitLab / Microsoft / email)
yaver auth --headless                # short-code flow for SSH-only boxes
yaver serve                          # start agent (auto-installs systemd unit on Linux)

# everyday
yaver status                         # local agent + auth + relay state
yaver primary status                 # remote primary device — agent version, lifecycle, runners
yaver primary auth                   # SSH into primary + run yaver auth --headless there
yaver primary set <deviceId|alias>   # pick a device as your primary
yaver primary ping                   # one-shot reachability + auth-as-same-user check
yaver ping <alias|deviceId|name>     # same, any device

# devices
yaver devices                        # list registered devices
yaver alias set <name> <deviceId>    # short name for ssh / connect / ping
yaver ssh <alias|primary>            # OpenSSH wrap, resolves LAN-on-subnet → Tailscale (gated on local 100.x interface up) → device row → ssh config

# code
yaver code                           # local TUI on this machine
yaver code --attach <device>         # remote machine via QUIC tunnel
yaver insert <app>                   # tell the paired mobile to load <app> via Hermes push

# Mac mini remote worker (Yaver via Yaver + Codex)
scripts/setup-mac-mini-dev.sh        # on the mini: Xcode platforms, simulators, Codex gpt-5.5, Yaver MCP
                                      # details: docs/mac-mini-remote-worker.md

# cable
yaver wire detect                    # USB-attached iPhone/iPad/Android
yaver wire push [path]               # framework-aware build + install (Expo/RN/Flutter/Native)

# vault
yaver vault add <name> --project <p> --value <v>
yaver vault env --project <p>        # source for deploy scripts
```

## Networking — short reference

| Layer | Port | Purpose |
|---|---|---|
| HTTP | 18080 | agent API (auth + tasks + dev server proxy) |
| QUIC | 4433 | relay tunnel out + direct phone connections |
| UDP beacon | 19837 | LAN auto-discovery (auth-aware via token-hash fingerprint) |
| HTTPS (LAN) | 18443 | self-signed TLS for SDK clients on LAN |
| Phone HTTP | 8347 | mobile-app inbound for `yaver push` from CLI |

Relay is application-layer QUIC, password-protected, self-hostable, no
TUN/TAP. Pass-through — never stores task data.

## Mobile app

### Hermes-push (default for RN/Expo)

`yaver-cli push` and the agent's `/dev/build-native` both produce a Hermes
bytecode bundle and load it into the Yaver mobile container via
`ExpoReactNativeFactory + RCTAppDependencyProvider` (TurboModules, Fabric,
JSI). Validation: HBC magic `0x1F1903C1` at offset 4, BC version (96) at
offset 8.

**Suppress-when-inside-Yaver** (RN SDK 0.5.5+): when a third-party RN app
loads inside the Yaver container, `YaverFeedback.init()` and
`ShakeDetector.start()` silently no-op via the `YaverInfo` native module.
Yaver's container owns shake/feedback ("Reload" + "Back to Yaver" overlay).
Standalone TestFlight/Play builds are unaffected.

### `sdk-manifest.json` contract

Source of truth: `mobile/sdk-manifest.json`. Must be in sync with
`mobile/android/app/src/main/assets/sdk-manifest.json`, the iOS bundle copy,
and `cli/sdk-manifest.json`. Update all four when bumping `mobile/package.json`.

### iOS — TestFlight (LOCAL ONLY)

CI is intentionally disabled (`if: false` in `release-mobile.yml`) because GH
runner keychains don't carry your registered iPhone UDIDs. Always run from
this Mac.

```bash
# vault path (preferred when auth is fresh)
$(yaver vault env --project mobile)
./scripts/deploy-testflight.sh

# fallback when vault is locked: source the gitignored env file
source ~/.appstoreconnect/yaver.env
./scripts/deploy-testflight.sh

# explicit env path (no vault, no env file — type the values yourself)
export APP_STORE_KEY_PATH="$HOME/.appstoreconnect/private_keys/AuthKey_77Z6B543D5.p8"
export APP_STORE_KEY_ID="77Z6B543D5"
export APP_STORE_KEY_ISSUER="<uuid>"
export APPLE_TEAM_ID="5SJZ4KA39A"
./scripts/deploy-testflight.sh
```

`~/.appstoreconnect/yaver.env` is gitignored and pre-seeded with all four
exports — sourcing it is the friction-free path when vault is locked
(common after auth token rotation). Keep it in sync if you rotate the
App Store Connect issuer/key.

GitHub Actions secrets backing the same flow (already populated): `APPLE_TEAM_ID`,
`APP_STORE_CONNECT_API_KEY`, `APP_STORE_CONNECT_KEY_ID`, `APP_STORE_CONNECT_ISSUER_ID`,
`APPLE_CERTIFICATE_P12`, `APPLE_CERTIFICATE_PASSWORD`. Verify with `gh secret list -R yaver-io/yaver.io`.

Vault entries (write once when vault is unlocked, then `yaver vault env --project mobile`
emits the same exports as the env file):
```bash
yaver vault add APP_STORE_KEY_PATH   --project mobile --value "$HOME/.appstoreconnect/private_keys/AuthKey_77Z6B543D5.p8"
yaver vault add APP_STORE_KEY_ID     --project mobile --value 77Z6B543D5
yaver vault add APP_STORE_KEY_ISSUER --project mobile --value <uuid>
yaver vault add APPLE_TEAM_ID        --project mobile --value 5SJZ4KA39A
```

The script auto-bumps CFBundleVersion, archives at `/tmp/Yaver.xcarchive`,
exports, and uploads. On flake/timeout, re-export from the existing archive
without re-archiving:

```bash
xcodebuild -exportArchive -archivePath /tmp/Yaver.xcarchive \
  -exportOptionsPlist /tmp/ExportOptions.plist -exportPath /tmp/YaverExport \
  -allowProvisioningUpdates -authenticationKeyPath "$APP_STORE_KEY_PATH" \
  -authenticationKeyID "$APP_STORE_KEY_ID" \
  -authenticationKeyIssuerID "$APP_STORE_KEY_ISSUER"
```

**TestFlight rate limit**: ~15-20 uploads/app/day. Don't re-run after
"Upload limit reached" — wait 24h.

**`uploadSymbols=false`** in ExportOptions.plist is mandatory. Xcode 15+
treats missing dSYMs as a fatal export error; `rnwhisper` ships without
dSYMs. Apple symbolicates server-side from bitcode anyway.

#### Headless codesign — unlocking the keychain from an SSH/agent session

The single hardest failure for an **autonomous (SSH / yaver-agent) deploy** is
`CodeSign … errSecInternalComponent`. It is NOT "cert missing" — `security
find-identity` happily lists the identities. It means the signing **private key**
is in a **locked** keychain that a non-GUI session can't open. Two facts learned
the hard way (2026-07-19), verified with per-cert `codesign -s <SHA1>` probes:

1. **The signing identity spans two keychains.** The **Apple Distribution**
   (SIMKAB, team `5SJZ4KA39A`) cert+key live in a dedicated
   `~/Library/Keychains/yaver-ci.keychain-db`; the **Apple Development** cert
   *private keys* live in `login.keychain-db`. During an App Store archive,
   Xcode signs the app-extension/watch intermediates with the **development**
   identity and re-signs with distribution at export — so **both** keychains
   must be unlocked, or the archive dies at `CodeSign …/*.appex`.
2. **Unlock is not enough — you must set the partition list.** For codesign to
   use a key *without a GUI prompt* you must run, per keychain:
   ```bash
   security unlock-keychain      -p "<pw>" "$KC"
   security set-keychain-settings          "$KC"   # no flags = never auto-lock
   security set-key-partition-list -S apple-tool:,apple: -s -k "<pw>" "$KC"
   ```
   Do this for **both** `yaver-ci.keychain-db` AND `login.keychain-db` before
   `deploy-testflight.sh`. `launchctl asuser` / gui-domain LaunchAgents do NOT
   help (they still hit the locked key); only the password does.

The passwords come from the secure local store below — never hard-code them, and
`deploy-testflight.sh` sources them so a headless run self-unlocks. `set-keychain-settings`
with no flags disables auto-lock so the ~20-min archive can't relock mid-build.

### Local privileged credential store (`~/.yaver/local-secrets.env`)

Some autonomous ops need **local-machine unlock secrets** a human would normally
type at a GUI: the **signing-keychain password**, the **login-keychain / macOS
login password**, and **sudo**. These live in `~/.yaver/local-secrets.env`:

- **`chmod 600`, owner-only. NEVER committed, NEVER synced to a cloud/GH secret.**
  A macOS login/sudo password in GitHub secrets would *widen* the attack surface —
  the exact opposite of the goal. It stays on the box, protected by filesystem
  perms + FileVault at-rest encryption, and never leaves it.
- A CI-scoped signing-keychain password MAY also go in GH Actions secrets
  (`YAVER_CI_KEYCHAIN_PASSWORD`) because CI needs it and it only unlocks the
  disposable signing keychain — but the **login/sudo** password must not.
- Canonical home is the encrypted **`yaver vault`**; this env file is the
  fallback used when the vault is unavailable (mirrors `~/.appstoreconnect/yaver.env`).
  If `yaver vault` reports "corrupted vault", repair with `yaver vault reset --yes`
  then re-add, or restore `~/.yaver/master.key`.
- Keys: `YAVER_CI_KEYCHAIN_PATH/PASSWORD`, `YAVER_LOGIN_KEYCHAIN_PATH`,
  `YAVER_LOGIN_PASSWORD`, `YAVER_SUDO_PASSWORD`. The yaver agent reads these to
  unlock keychains / run sudo headlessly; a hostile process can only read them
  with local FS access, which already implies the box is compromised.

### Android — Play Store

CI handles it via `release-mobile.yml` on `mobile/v*` tags using
`PLAY_STORE_SERVICE_ACCOUNT_JSON`, `ANDROID_KEYSTORE*` secrets. Android
versionCodes auto-increment.

Local equivalent:
```bash
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh
PLAY_STORE_KEY_FILE=keys/google-play-service-account.json \
  python3 scripts/upload-playstore.py
```

`mobile/android/keystore.properties` is gitignored. Restore after
`expo prebuild --clean`:
```
storeFile=../../../keys/yaver-upload.keystore
storePassword=<password manager>
keyAlias=yaver-upload
keyPassword=<password manager>
```

**Play app-signing SHA-256 fallback (mirrors the TestFlight env file)**:
the SHA lives in Play Console → Setup → App integrity. The canonical
source is the vault (`yaver vault add ANDROID_RELEASE_SHA256 --project
mobile --value <fingerprint>`), but after an auth-token rotation the
vault locks. Pre-seed `~/.androidplay/yaver.env` once and
`deploy-web.sh` will source it on every run:
```bash
mkdir -p ~/.androidplay && cat > ~/.androidplay/yaver.env <<'EOF'
export ANDROID_RELEASE_SHA256="AA:BB:CC:..."
EOF
```
Without this, `assetlinks.json` ships with only the upload-keystore
SHA — passkey enrollment silently fails on Play-distributed builds
because Credential Manager can't bind `yaver.io` to the Play-resigned
APK.

### Force-tracked iOS overlay files

`mobile/ios/` is gitignored, but a few overlays are force-added because
`expo prebuild --clean` regenerates the dir:

- `mobile/ios/Yaver/AppDelegate.swift` — super-host bootstrap, ShakeDetectingWindow, YaverHTTPServer.shared.start(), safe bridge reload
- `mobile/ios/Yaver/Yaver-Bridging-Header.h` — Swift ↔ ObjC, GCDWebServer
- `mobile/ios/Yaver/YaverBundleLoader.swift` + `.m` — `NativeModules.YaverBundleLoader`
- `mobile/ios/Yaver/YaverScreenRecorder.swift` + `.m` — feedback visual capture
- `mobile/ios/Yaver/YaverHTTPServer.swift` — port-8347 bundle-receive server (currently a stub)
- `mobile/ios/Yaver/YaverInfo.swift` + `.m` — `isYaver` detection from guest bundles
- `mobile/ios/Yaver/YaverBundleValidator.swift` — HBC validation + `SDKManifest` singleton (currently stub)
- `mobile/ios/Yaver/sdk-manifest.json` — copy of mobile/sdk-manifest.json

The HTTPServer / Validator / Info stubs exist because `pbxproj` references
them. When filling in real implementations, match the signatures in
`YaverBundleLoader.swift` and `AppDelegate.swift`.

### Cold-start mobile rebuild (after `expo prebuild --clean`)

```bash
cd mobile
npm install --legacy-peer-deps
npx expo prebuild --platform ios     --clean --no-install
npx expo prebuild --platform android --clean --no-install
git checkout -- mobile/ios/ mobile/android/   # restore force-tracked overlays
cp mobile/sdk-manifest.json mobile/ios/Yaver/sdk-manifest.json
cd mobile/ios && pod install && cd ../..
# create mobile/android/keystore.properties (see above)
./scripts/deploy-testflight.sh
JAVA_HOME=$(/usr/libexec/java_home -v 17) ./scripts/deploy-playstore.sh
```

First-time pod install is ~28 min, archive is ~15-20 min, gradle bundleRelease
is ~28 min.

### Disk-space preflight

Before any mobile deploy:
```bash
mobile-cache-cleanup.sh preflight    # fails hard if < 20 GB free
```
After successful deploy:
```bash
mobile-cache-cleanup.sh mark-deployed yaver
```

The script lives at `~/.local/bin/mobile-cache-cleanup.sh` (shared across
local mobile projects — don't fork).

### Version bumping (5 places, all must match)

When bumping `mobile/v<x>`:
1. `mobile/app.json` → `expo.version`
2. `mobile/package.json` → `version`
3. `mobile/ios/Yaver/Info.plist` → `CFBundleShortVersionString`
4. `mobile/ios/Yaver.xcodeproj/project.pbxproj` → `MARKETING_VERSION` (×2: Debug + Release)
5. `mobile/android/app/build.gradle` → `versionName`
6. `versions.json` → `mobile`

Build numbers (`CFBundleVersion` / `versionCode`) auto-increment in deploy
scripts.

### Mobile dev iteration (fast, no TestFlight)

USB-connected iPhone, no daily limit:
```bash
xcrun xctrace list devices 2>&1 | grep -v Simulator    # find UDID
yaver wire push                                          # detect framework + install Release build
# OR: cd mobile && npx expo run:ios --device <UDID>     # Debug build (needs Metro on :8081)
```

Multiple RN projects fight for port 8081. Either kill the others
(`pgrep "expo start" | xargs kill`) or build Release.

### Pushing Yaver itself (`yaver wire push` / `yaver wireless push`)

Both commands auto-detect the mobile project from CWD by walking up to
the first `app.json` / `package.json` with `expo` or `react-native`,
or `pubspec.yaml`, or `ios/*.xcodeproj`. **CWD matters.**

**To iterate on Yaver mobile (this repo):**
```bash
# from repo ROOT — wire/wireless walks into ./mobile automatically
cd <repo>
yaver wireless push                                      # WiFi-paired iPhone
# or:
yaver wire push                                          # USB-attached
```
Running from `desktop/agent`, `web/`, `relay/`, or any non-mobile
subdir fails with `no mobile project detected at <path>`. Always
`cd` to repo root first.

**To iterate on a third-party app:**
```bash
cd <example-app>
yaver wire push       # builds + installs that app, NOT Yaver
```
Third-party RN apps load INSIDE the Yaver container via Hermes-push;
they don't need their own native install once Yaver is on-device.
But `yaver wire push` from the third-party repo will native-build +
install the third-party app (useful for first-time setup or when
testing a non-Hermes change).

**Rule of thumb**: the binary getting installed = whatever mobile
project lives in CWD. If you want to ship Yaver, `cd yaver.io` first.

Output ends with `App installed:` + `bundleID: io.yaver.mobile` (when
pushing Yaver) or `bundleID: <third-party>` (when pushing a guest
app). Check the bundleID line if unsure what just got installed.

## Mobile dev-server proxy / Hermes flow on remote agent

Three commands matter:
- `POST /dev/start {framework, workDir}` — Metro/Vite/Flutter/Next
- `POST /dev/build-native` — compile Hermes bundle once
- `POST /dev/reload` (or `/dev/reload-app {mode}`) — hot reload (Expo/RN
  refresh through native Hermes path; web frameworks refresh via WebView)

`yaver-test-ephemeral` (Linux ARM) → mobile flow:
```bash
# from any machine signed in as the same user:
yaver insert sfmg                  # broadcast "open_app" to paired phones
# mobile receives via /blackbox/command-stream → navigates to Hot Reload tab
# → POST /dev/build-native on the agent → loads Hermes bundle
```

## Hetzner test box rules

`yaver-test-ephemeral` (cax21 arm64) is for remote integration testing.
**Disposable** — kill anytime. Reproducible from `ci/remote/bootstrap.sh`.

- No secrets ever live there.
- No user-visible copy mentions it.
- Cost-cheap pause: snapshot via `ci/hcloud/snapshot-server.sh`, then
  `hcloud server delete yaver-test-ephemeral`. Snapshot ~€0.10/mo vs
  €6.49/mo running.
- Recreate from snapshot: `ci/hcloud/create-server.sh` (uses
  `HETZNER_TEST_SNAPSHOT_ID` GH secret).
- Wired secrets (set with `gh secret set`): `HCLOUD_TOKEN`,
  `HCLOUD_SSH_PRIVATE_KEY`, `HETZNER_TEST_SERVER_ID`,
  `HETZNER_TEST_SERVER_IP`, `HETZNER_TEST_SNAPSHOT_ID`.

In Yaver device lists / Convex rows / `yaver ssh`, the same box may appear
as `ubuntu-4gb-hel1-1`. Set per-user alias `test` and use `yaver ssh test`.

## Conventions

- Go: standard layout, `gofmt`, build tags only when truly platform-specific.
- TypeScript / React: functional components, hooks. No class components.
- Convex: mutations for writes, queries for reads, httpAction for HTTP routes.
- Mobile: native builds (xcodebuild + Gradle), never Expo CLI for production.
- Tests: real HTTP servers on random ports, no mocks, no external deps. See
  `desktop/agent/*_test.go` for the pattern.

## Cross-surface parity — every fix ships to EVERY UI surface

**A fix is not done until it exists on all surfaces.** This holds for
connectivity, auth/session, wake/park, and any user-facing behavior. The
surfaces are: **mobile** (phone + tablet), **web**, **tvOS**, **watchOS +
Wear OS**, **CarPlay (car)**, and **glass / AR-VR**.

Two families with DIFFERENT propagation:

1. **React-Native surfaces share code** — mobile, tablet, car
   (`app/car-voice-coding.tsx`), and glass/AR-VR (`app/glass-*.tsx`) all consume
   the same `DeviceContext` + `AuthContext`. A fix there (e.g. the relay
   self-heal in `DeviceContext.tsx`, auth extend-only refresh in `lib/auth.ts`)
   reaches all of them for free. Verify it isn't gated to one screen.
2. **Native surfaces have their OWN code and must be ported explicitly** —
   tvOS (`tvos/YaverTV/YaverStore.swift`, `AgentClient.swift`), watchOS
   (`watch/YaverWatch/WatchStore.swift`, `SessionClient.swift`), Wear OS
   (`wear/.../*.kt`), and web (`web/lib/use-auth.ts`, `web/app/dashboard/`).
   They do NOT inherit RN fixes — port the same behavior and note it in the
   commit. Examples already mirrored this way: Netflix-grade session persistence
   (extend-only `/auth/refresh` on launch), Stream-C narrated auto-connect, and
   relay self-heal (`POST /settings/repair-relay` → re-pull creds → retry, once
   per failure streak, per-user only).

When you fix connectivity/auth/lifecycle on one surface, grep the others for the
same seam and either confirm shared-code coverage or land the native port in the
same change. A per-surface parity table belongs in the handoff/commit.

## Tests

```bash
# unit
cd desktop/agent && go test -count=1 ./...
cd relay && go test ./...

# e2e + integration suites
./scripts/test-suite.sh                     # full
./scripts/test-suite.sh --unit --lan --relay  # subset
./scripts/run-ci-local.sh                   # mirrors GH CI matrix

# trigger CI from terminal
./scripts/run-gh-ci.sh                      # all workflows
./scripts/run-gh-ci.sh ci e2e               # subset
```

Browser e2e (`e2e/`): Playwright + Chromium. CI runs on PRs touching `web/`
or `e2e/`.

## Feature pointers

For everything below, the canonical reference is the source. Brief pointers
only; the directories have their own READMEs / inline comments where it
matters.

| Area | Where |
|---|---|
| Auth + bootstrap + recovery | `desktop/agent/auth*.go`, `auth_recover.go`, `auth_bootstrap.go`; `backend/convex/auth.ts`, `deviceCode.ts` |
| Heartbeat + device registry | `desktop/agent/auth.go::SendHeartbeat`, `backend/convex/devices.ts::heartbeat`, `backend/convex/http.ts /devices/heartbeat` |
| Hot reload / dev server | `desktop/agent/devserver.go`, `devserver_http.go`, `dev_cmd.go`; mobile `app/(tabs)/apps.tsx` |
| Hermes push for guest apps | `cli/src/{analyzer,bundler,discovery,transport}.js`; mobile `ios/Yaver/YaverBundleLoader.*` + `YaverBundleValidator.swift` |
| Wire (cable) | `desktop/agent/wire_cmd.go`, `device_install.go`, `native_build.go` |
| Remote / insert (paired phone) | `desktop/agent/remote_mobile_cmd.go`, `mobile_session_http.go`; mobile `src/lib/openAppBus.ts`, `app/(tabs)/_layout.tsx` |
| Ping (reachability + auth-as-same-user) | `desktop/agent/ping_cmd.go`; mobile `DeviceDetailsModal.tsx::PingRow`; web `DevicesView.tsx::InlinePingButton` |
| Vault | `desktop/agent/vault.go`, `vault_cmd.go`, `vault_http.go`. NaCl secretbox + Argon2id, encrypted with auth-token-derived key |
| Deploy script generator + doctor | `desktop/agent/deploy_script_gen.go`, `doctor_build.go`, `deploy_script_http.go` |
| Store tester/build management (TestFlight + Play) | `desktop/agent/appstoreconnect.go` (ASC API: beta testers/groups/builds), `playpublish_api.go` (Play tracks/testers/rollout), `ops_store.go` (`store_*` MCP verbs, multi-tenant per-project vault, runs on managed cloud). Web `StoresView.tsx` Testers tab; mobile `app/store-testers.tsx` + `src/lib/storeTestersClient.ts`. Reuses Store Studio auth (`resolveAppleASCCreds`/`mintASCJWT`, `resolveGoogleSA`/`getGoogleAccessToken`). Apple=per-email testers; Google=track Google-Groups + rollout (per-email = Console-only). Doc `docs/yaver-store-tester-management.md`, blog `/blog/mobile-beta-testing-apple-google`. |
| Guest access | `backend/convex/guests.ts`; `desktop/agent/guest_*.go`. Scopes: `full` / `feedback-only` / `deploy` |
| Container sandbox (deferred) | `desktop/agent/container_runner.go`, `Dockerfile.sandbox`. End-to-end testing TODO — see `docs/guides/DOCKER_REMAINED.md` |
| Multi-user | `desktop/agent/multiuser.go`, `multiuser_http.go`; `backend/convex/teams.ts` |
| Account linking + merge | `backend/convex/auth.ts::mergeUserInto`; `desktop/agent/account_cmd.go`, `mcp_auth_link_tools.go` |
| Phone-first mini backend | `desktop/agent/phone_backend.go`, `phone_backend_http.go`; mobile `app/phone-projects*` |
| Switch engine (target migrations) | `desktop/agent/switch_*.go` — 19 targets, snapshots, 7-day rollback |
| Session transfer | `desktop/agent/session_*.go`, `transfer.go` |
| Hands-free voice (all surfaces) | `mobile/src/lib/voice/` — one surface-agnostic `VoiceConversationCore` (streaming STT → semantic endpointing → runner dispatch → TTS → barge-in) + adapters; `useHandsFreeVoice` React seam; car wired in `app/car-voice-coding.tsx`. Semantic "when to submit" = timing trigger (`endpointer.ts`) → on-device judge (`completenessJudge.ts`, free llama.rn). Runner-only (no Flux). Doc `docs/architecture/VOICE_CONVERSATION.md`. tsx tests. |
| Feedback SDK + black box | `sdk/feedback/{react-native,web,flutter}/`; `desktop/agent/blackbox*.go`, `feedback*.go` |
| Support sessions (TeamViewer-style) | `desktop/agent/support*.go` |
| SDK token security | `desktop/agent/auth.go::ValidateSdkToken*`, `sdk_token.go`, `tls.go` |
| Networking (relay + beacon + QUIC) | `desktop/agent/quic.go`, `beacon.go`; `relay/` |
| Workspace manifest | `desktop/agent/workspace*.go`; `yaver.workspace.yaml` |
| Managed toggle | `desktop/agent/managed*.go`; `backend/convex/userSettings.ts` |
| `ops` MCP grand-tool | `desktop/agent/ops*.go` — single MCP tool, 20 verbs |
| Remote Desktop (screen view + mouse/kbd control) | `desktop/agent/remotedesktop*.go` (`/rd/status`,`/rd/policy`,`/rd/stream` MJPEG,`/rd/frame.jpg`,`/rd/input`); reuses `ghost/` engine + `ghost_stream.go`. Runtime consent policy (control opt-in), NOT the `--ghost` flag. Web `RemoteDesktopView.tsx`/`RemoteDesktopModal.tsx`; mobile `app/remote-desktop.tsx` (iOS-safe snapshot-poll, MJPEG only on web). Fullscreen on shell + remote view both surfaces. |
| Apple TV control + capture-card streaming | `desktop/agent/appletv.go` (pyatv sidecar supervisor + vault creds), `ops_appletv.go` (`appletv_*` + `capture_*` verbs), `appletv_cmd.go` (`yaver appletv …`), `capture.go` (ffmpeg→MJPEG, HDCP-black detection, `/capture/stream`+`/capture/frame.jpg`), `appletv/yaver_atv_bridge.py` (embedded). First-class image tool `appletv_now_playing` (robot_camera pattern). Mobile `app/appletv-remote.tsx` + `src/lib/appletvClient.ts` (D-pad/transport/now-playing/capture video, `?surface=glass` HUD). Control+metadata always-legal; capture = OWN non-protected sources only (NO HDCP capture, NO CarPlay video). Doc `docs/yaver-appletv-remote-control.md`, README `desktop/agent/appletv/README.md`. |
| Circuit simulator cell | `desktop/agent/circuit/` (dep-free pure-Go MNA solver op/dc/tran/ac + ngspice pass-through; SPICE/KiCad/EPLAN import; generic ERC; PNG plot) + `ops_circuit.go` (`circuit_*` verbs) + `circuit_plot` first-class MCP image tool (`mcp_tools.go`/`httpserver.go`). Web `CircuitCellView.tsx` @ `/dashboard/circuit`; mobile `circuit.tsx` + `circuitClient.ts`. Netlists = vault-local, never Convex. Doc `docs/yaver-circuit-simulator.md`. |

## Local development

```bash
cd backend && npx convex dev          # convex dev
cd web && npm run dev                  # next dev
cd mobile && npm run web               # browser RN preview (dev only)
cd desktop/agent && go run . serve     # agent
cd relay && go run . serve --password <secret>
```

For dev-server iteration on a specific RN project: see "Mobile dev iteration"
above. Don't tell users to run `expo start` manually — the agent handles it.
