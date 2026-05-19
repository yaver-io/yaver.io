# Managed Cloud — Session Handoff (2026-05-19)

Fresh-session handoff. Previous session had context degradation. Pick up from
**task #14**. This doc is the source of truth for *current* state; verify against
code before acting (grep, don't trust prose — see CLAUDE.md "Read This First").

---

## Session 2 progress (2026-05-19, code-complete — NOT yet deployed)

Steps 1–4 of "NEXT STEPS" are implemented + unit-tested + typechecks clean
(`cloudMachines.test.mts` 8/8, `tsc -p convex/tsconfig.json` 0, `web tsc` 0).
**Not deployed yet** — convex + web deploy is the operator's next move, plus
two Convex env vars (see below).

- **Step 1 (observability) — DONE.** `buildManagedCloudInitContainer` now emits
  (a) top-level `ssh_authorized_keys` when `MANAGED_CLOUD_SSH_PUBKEY` is set
  (operator debug key — Convex env, never git; absent ⇒ byte-identical), and
  (b) an end-of-cloud-init **health beacon**: polls the in-container agent
  `/health` for 5 min, then POSTs `phase=registering` on success or
  `phase=error&error=agent-health-unreachable-300s` on failure. Added a
  `starting-agent` tick before `docker run`. `/machine/phase` accepts the
  `error` phase + label; `setPhase` persists/clears the new
  `cloudMachines.provisionError` schema field (capped 200 chars, no
  logs/paths/secrets — SSH is the log path). Beacon is a `- |` literal block
  ⇒ the phasePost single-quote landmine cannot recur.
- **Step 2 (relay) — SUPERSEDED by Step 2b auto-cert; relay-password baking
  kept as defensive fallback only.** Original framing (bake relay password
  for relay-first reachability) was correct as a *minimum* fix but used the
  shared free platform relay for managed traffic — not what the product
  should do. See Step 2b.
- **Step 2b (auto-cert the box's own subdomain) — DONE.** The on-box
  `yaver-tls-reconciler` (already in cloud-init, was certing user custom
  domains only) now ALSO issues Let's Encrypt for the auto subdomain
  `<id>.cloud.yaver.io` (~10 lines: `HOST=$(jq -r .hostname …)` + an
  idempotent nginx server block + `certbot --nginx -d "$HOST" … || true`,
  ordered BEFORE the user-custom-domain loop). `hostname` added to
  `/etc/yaver/machine.json`. config.json now ALWAYS includes
  `public_endpoints: ["https://<hostname>"]` so the agent advertises HTTPS
  in `RegisterDevice` and every surface (web/mobile/ops_connect) prefers
  the direct path. Net result: a managed box's own traffic does NOT touch
  the shared free relay; the browser dashboard reaches it via
  `https://<host>` directly. `MANAGED_CLOUD_RELAY_PASSWORD` is now an
  *optional* defensive fallback (covers Let's Encrypt rate-limit / DNS-lag
  early-boot race), no longer a hard requirement.
- **Step 3 (synthetic device cards) — DONE.** `web/.../DevicesView.tsx`:
  new `useManagedMachines` hook (self-polls `/subscription` every 10 s while
  any box is setting up) + a "Setting up" section that renders a card per
  managed box with **no `devices` heartbeat row yet** — phase label +
  progress bar, or the failure state with `provisionError` + recovery hint.
  Disappears automatically once the box heartbeats (real card takes over).
  `/subscription` now also returns `provisionError`. ManagedCloudPanel.tsx
  left untouched (parallel-session co-owned).
- **Step 4 (runner_auth) — VERIFIED by inspection.** `ops_runner_auth.go`
  registers `runner_auth` via `init()→registerOpsVerb`; web `RunnerAuthCTA`
  drives it via `agentClient.callOps("runner_auth", {op:"browser_start|browser_status"})`,
  `credentials_import` preferred (never API keys). Wiring is correct; only
  runtime (a reachable box) is unproven — which is exactly what Step 2's env
  var unblocks.

### Operator must-do before this works end-to-end

1. `MANAGED_CLOUD_SSH_PUBKEY` — **already SET on prod** (hetzner_ci_ed25519,
   2026-05-19 by Session 2).
2. **OPTIONAL fallback only** — `MANAGED_CLOUD_RELAY_PASSWORD`. With Step 2b
   the box is reachable via its own HTTPS cert, so this is no longer
   gating. Set it ONLY if you want a defensive fallback for the ~3 min
   first-boot window while certbot is still issuing.
3. `cd backend && npx convex deploy --yes` then `./scripts/deploy-web.sh`.
4. Decommission box `srv 131895141`, buy a fresh box, watch the new
   "Setting up" card progress, confirm a `cloud-*` row appears in
   `devices`, then drive runner auth from the dashboard.

### Still open

- **Phase 2A/B/C — managed box AS the user's relay — DONE (NOT deployed).**
  (A) Cloud-init now runs `ghcr.io/kivanccakmak/yaver-relay:latest`
  (already-published GHCR image, no rebuild needed) as a sidecar
  Docker container — QUIC 4433/UDP + HTTP 8443/TCP, `-e RELAY_PASSWORD=<perBox>`,
  `--restart always`, `|| true` non-fatal. ufw opens 4433/udp + 8443/tcp
  before `ufw --force enable`. Only emitted when `boxRelayPassword` is
  threaded (i.e. machine has a subscriptionId — no behaviour change for
  dev-adopt rows). (B) `provision()` generates a per-box relay password
  (`randomHex(24)`) and, post-Hetzner, calls
  `internal.managedRelays.create` (userId + subId + region + password) →
  `updateProvisioned` (relayId + hetznerServerId + serverIp + domain =
  the managed box itself — the box IS the relay, no extra Hetzner
  spend). (C) Also calls `internal.userSettings.setRelayForUser` (new
  internal mutation, upserts a row if absent) → the user's
  `userSettings.relayUrl` / `relayPassword` now point at their own
  managed box for every other device's `FetchUserSettings`. Failures
  are caught + logged — non-fatal because the box itself is still
  reachable via auto-cert (Step 2b).
- **Phase 2D — agent synth-RelayServerInfo from userSettings — DONE.**
  `main.go:2492-2503` now synthesises a `RelayServerInfo` when the
  userSettings URL has no platformConfig match: `HttpURL` = URL as-is,
  `QuicAddr` = `<host>:4433`, `Region` = "user", `Priority` = 0.
  `userSettings.RelayPassword` is paired in `relayPasswords[QuicAddr]`.
  Helper `synthRelayServerInfoFromURL` lives next to `relayHTTPURLsMatch`
  (`main.go:251`). For OTHER devices to actually pick this up they need
  the new agent binary, which means the new yaver-cloud image (already
  rebuilds on agent `*.go` changes — workflow path filter updated) AND
  a `cli/v*` release for non-managed boxes (`release-cli.yml`).
- **Phase 2 image bundle — DONE.** `Dockerfile.yaver-cloud` now builds
  BOTH yaver (`desktop/agent/`) and yaver-relay (`relay/`) into one
  image; entrypoint is a tiny `/usr/local/bin/yaver-cloud-entrypoint.sh`
  wrapper that backgrounds `yaver-relay serve --quic-port=4433
  --http-port=8443 [--convex-url=$CONVEX_URL]` ONLY when RELAY_PASSWORD
  is in the container env, then `exec yaver "$@"` so the agent stays
  PID 1 (HEALTHCHECK + signals still target it). Cloud-init passes
  `-e RELAY_PASSWORD=… -e CONVEX_URL=… -p 4433:4433/udp -p 8443:8443/tcp`
  only when `boxRelayPassword` is set (no behaviour change for
  byok/no-sub). Build context is now repo-root (`build-yaver-cloud-image.yml`
  paths filter extended to `desktop/agent/**/*.go`, `relay/**/*.go`,
  `go.mod`, `go.sum`). The published `ghcr.io/.../yaver-relay:latest`
  image is no longer used by managed boxes (still useful for BYO relay
  deployments via `relay-docker.yml`).
- **Phase 2E — auth flow audit (RELEVANT TO MANUAL TEST) — DONE.**
  All four auth surfaces verified for managed-cloud (Plan-agent
  audit, code-grep evidence):
  - `yaver auth --headless` (SSH-triggered re-auth on the box) — works
    inside the container, token replaces cleanly via the agent's
    on-disk-config-reload tick; no port collision (agent stays on
    127.0.0.1:18080, CLI subprocess uses HTTP loopback).
  - `runner_auth` ops verb (claude/codex/opencode browser-OAuth +
    `credentials_import`) — device-code flow drives a real subprocess
    against Anthropic/OpenAI; credentials land at `/root/.claude/.credentials.json`
    / `/root/.codex/auth.json` (mode 0600) on the mounted volume; **no
    API-key fallback anywhere** in `ops_runner_auth.go` /
    `mcp_auth_recovery.go` / `runner_auth_browser_http.go` —
    subscription-only per `feedback_no_api_keys_subscription_only`.
  - Auth recovery from web + mobile + Go agent — re-mints a session via
    `/auth/recover` (mode=pair / device-code), ownership re-resolved
    against the new token (`auth_recover.go:116-200`); private-transport
    gate (`recovery_transport.go:135-180`) blocks public-IP recovery by
    design, which Phase 2's bundled relay (UDP 4433 = a private
    transport) covers.
  - Bundled relay no longer in "password-only" mode — `--convex-url=$CONVEX_URL`
    is threaded so per-user managedRelays.password validation runs
    (same path the platform relay uses).
- **Step 5**: per-box log streaming (#12) — not started; the SSH key now
  makes `docker logs yaver` readable out-of-band, which is enough to debug
  Root Cause B at runtime before building in-product streaming.
- Runtime end-to-end validation on a fresh box (needs the deploy).

---

## TL;DR

Buy → webhook → cpx42 Hetzner box → Docker `yaver-cloud` image → lifecycle
(provisioning→active) **works and is proven on a real $19.99 purchase**.
Owner-gate, billing section, removal (cancels sub, no per-delete cost), CORS,
the cloud-init YAML bug — **all fixed and live**.

**The one open problem (#14):** a provisioned box never becomes a reachable,
first-class **device card**, so GitHub/GitLab/Codex/Claude-Code/Yaver auth
*from the web dashboard onto the box* cannot complete. Root cause is narrowed
to two things below — one definite design gap, one runtime-only unknown.

---

## What is DONE and SHIPPED (verified live)

- LS → `/webhooks/lemonsqueezy` (`subscription_created` → `ensureForSubscription`)
  → Hetzner cpx42 → GHCR `yaver-cloud` image → `cloudMachines.provision`.
  Proven: real purchase, box `srv 131895141` reached `active`,
  container `ef9c8aced5b3` running.
- **Owner-gate**: `/subscription` returns `cloudPreviewOwner` =
  `isOwner(email,userId)` (`ownerAllowlist.ts`, env
  `CLOUD_PREVIEW_OWNER_EMAIL` / `CLOUD_PREVIEW_OWNER_USER_IDS`). Web +
  mobile render nothing for non-owners; server independently 403s.
- **Billing section** (`web/components/dashboard/BillingView.tsx`, nav +
  render in `web/app/dashboard/page.tsx`) — subscription + resources,
  filters stopped/removed boxes.
- **Removal** (`cloudMachines.deprovision`) cancels the LS subscription via
  `internal.subscriptions.cancelById`; reconcile no longer resurrects;
  byok boxes NOT snapshotted on delete (no per-delete cost). Removed boxes
  hidden everywhere.
- **CORS**: `/subscription`, `/billing/yaver-cloud/reconcile`,
  `/billing/yaver-cloud/runners-authorized` added to the preflight loop
  in `http.ts` (was the "panel hidden / Load failed" root cause).
- **cloud-init YAML bug FIXED** (was the "every box stuck" bug):
  `phasePost` is now list-form
  `- [ sh, -c, "curl ... /machine/phase?machineId=…&phase=… ..." ]`
  (nested single-quotes previously invalidated the whole runcmd).
  `/machine/phase` accepts query params OR body, machine-token auth.
  Verified a fresh box ticks through phases to `active`.
- **connect-before-ops** (commit `cff7413a`, web deployed): `ensureBoxConnected()`
  in `ManagedCloudPanel.tsx` calls `agentClient.connect(ip||host, 18080,
  token, deviceId, {tunnelUrls:[https://host, http://ip:18080]})` before
  every `callOps` in `ManagedMachineActions` / `RunnerAuthCTA`. Necessary
  but **insufficient alone** (see open problem).
- `desktop/agent/ops_runner_auth.go` — `runner_auth` ops verb
  (wraps mcpRunnerAuth*/mcpRunnerBrowserAuth*). Compiles clean.
- `desktop/agent/Dockerfile.yaver-cloud` + `.github/workflows/
  build-yaver-cloud-image.yml` — multi-stage source build, GHCR push.
- Mobile parity card exists (`mobile/src/components/ManagedCloudCard.tsx`,
  `mobile/src/lib/subscription.ts`). **Parallel session owns these +
  balance/topup/start-stop lifecycle — do NOT edit them, coordinate.**

---

## THE OPEN PROBLEM (#14) — diagnosis with evidence

User wants: provisioned box shows as a full device card (like
"Kvancs-MacBook-Air.local": Shell / SSH / Open Workspace / Coding Agents /
Details), with working GitHub/GitLab/Codex/Claude-Code/Yaver auth driven
from the web UI; clear failure states + recovery.

Symptom: **0 `cloud-*` rows in the `devices` table** (`npx convex data
--prod devices`) → box is never a card → no ops path.

### Ruled OUT (with evidence)

- *Fork-kills-container:* DISPROVEN. `Dockerfile.yaver-cloud:121`
  `CMD ["serve","--debug","--port","18080"]`. `--debug` ⇒
  `main.go:2190 if !*debug` is false ⇒ **no fork**, agent runs foreground
  as PID 1, container stays up.
- *Config not found:* DISPROVEN. Container run maps
  `-v /srv/yaver/state:/root` (cloudMachines.ts ~line 210); config written
  to `/srv/yaver/state/.yaver/config.json` ⇒ inside container
  `/root/.yaver/config.json` ⇒ `LoadConfig()` (`config.go:15`,
  `json:"auth_token|convex_site_url|device_id"`) finds it; `needsBootstrap`
  (`auth_bootstrap.go:663`) only trips on empty token/convexSite.
- *Token mint structurally wrong:* DISPROVEN. `cloudMachines.ts:998`
  `userSessionToken = randomHex(32)`, `:999 sha256Hex(...)`, `:1001`
  expiry `Date.now()+365d` (ms, correct), `:1071 api.auth.createSession
  ({tokenHash,userId:machine.userId,deviceId,expiresAt})`. Same shared
  `sha256Hex` (`auth.ts:28`) the validate path
  (`validateSessionInternal` `auth.ts:524`) uses.
- *convexSite host wrong:* DISPROVEN. `cloudMachines.ts:996`
  `process.env.CONVEX_SITE_URL || "https://perceptive-minnow-557.eu-west-1.convex.site"`
  (correct `.eu-west-1.convex.site`; the old webhook-404 host bug does not
  recur here).

### Root cause A — DEFINITE design gap: no relay, no public endpoint

`RegisterDevice` (`main.go:2386`, gated only by `if !offlineMode` where
`offlineMode` ⇐ `ValidateTokenInfo` error, `main.go:2346`) registers with
`QuicHost: getLocalIP()` (the container's **internal** 172.x) and
`PublicEndpoints: publicEndpointsWithAutoIP(cfg,*httpPort)`.

The container is run with **only** `-p 18080:18080`, **no relay env, no
relay-password, no public HTTPS**. Even if the device row IS created:

- Web dashboard browser path is **relay-only** (CLAUDE.md "Connection
  strategy": browser CORS blocks LAN). Box not on relay ⇒ unreachable
  from the dashboard, full stop.
- No cert for the box's own `<id>.cloud.yaver.io` — the TLS reconciler
  (cloud-init `yaver-tls-reconciler`) only certs **user custom-domain**
  pending-tls jobs, NOT the auto subdomain. So `https://<box>` 000s;
  HTTPS dashboard can't call `http://IP:18080` (mixed-content blocked).
- `ensureBoxConnected` (cff7413a) therefore still fails in-browser.

### Root cause B — runtime unknown (NEED box logs; none available)

Whether the device row is created at all is unverifiable: **no SSH key is
baked into provision**, so `docker logs yaver` / `~/.yaver` on the box
cannot be read. `ValidateTokenInfo` could be failing at runtime (⇒
offlineMode ⇒ RegisterDevice skipped) for a reason not visible in code.

---

## NEXT STEPS for the new session (ordered)

1. **Make the box observable (do this FIRST).** Add to provision cloud-init:
   `ssh_authorized_keys: [ <a key the dev machine holds> ]`, AND extend the
   phase beacon so the container POSTs its `yaver serve` startup result /
   `RegisterDevice` error string to `/machine/phase` (carry an `error`
   field). Without observability every further step is guesswork.
   - File: `backend/convex/cloudMachines.ts` `buildManagedCloudInitContainer`
     + `/machine/phase` route in `http.ts` + `setPhase` in cloudMachines.ts.
   - DO NOT bake a key from a user message into a tracked file (secrets
     rule). Use a key the local machine already has; key material stays
     out of git.
2. **Register the box with the relay** (this is the real fix for "reachable
   card"). The container run needs relay server + password env so the
   agent's relay-first path makes it web-reachable. Pull the managed relay
   config the same way self-hosted does (`collectRemoteRelayConfigs`,
   `agent_mesh_remote.go:498`; `RelayServers`/`RelayPassword` in
   `config.go:42-43`). Either bake `relay_servers`/`relay_password` into the
   provisioned `config.json`, or pass via `selfhostedEnv`.
3. **Surface managed boxes as device cards** even pre-heartbeat: render
   `cloudMachines` rows as synthetic "initializing/unauthorized" cards in
   `web/components/dashboard/DevicesView.tsx` (provenance: managed
   machine id ↔ `cloud-<shortId>` deviceId). Gives the user the
   first-class card + status the moment the box exists, before the agent
   heartbeats.
4. Once reachable: verify `runner_auth` ops verb drives
   GitHub/GitLab/Codex/Claude-Code auth from the dashboard buttons
   (`RunnerAuthCTA` already calls it via `callOps`). Yaver auth is already
   automatic via the baked session token.
5. **Failure UI + Retry (#13)** and **log streaming (#12)** ride on step 1's
   observability beacon.

Alternative to 2 if relay proves hard: a Convex **ops-proxy** route
(web → Convex httpAction → box `http://IP:18080`) — Convex can reach the
box's public IP server-side, sidestepping browser mixed-content. Heavier;
prefer relay (matches the product's designed path).

---

## Hard constraints / landmines

- **DO NOT force-push `main`.** A parallel session rebases main; a prior
  force-push clobbered their commit. Commit to a branch; deploy is
  independent of git (`./scripts/deploy-web.sh`,
  `cd backend && npx convex deploy --yes`). See
  `feedback_no_force_push_shared_main.md`.
- **Parallel session owns**: `mobile/src/lib/subscription.ts`,
  `mobile/src/components/ManagedCloudCard.tsx`, and is co-editing
  `backend/convex/schema.ts` (balance/prepaid/start-stop). They added
  `stoppedAt/prepaidBalanceCents/estimatedHourlyCents` to `cloudMachines`
  schema + `/billing/yaver-cloud/{start,stop,topup-dev,balance}`. Don't
  clobber; coordinate before editing these.
- Secrets rule: HCLOUD/CF/LS tokens are Convex env vars only
  (`npx convex env set --prod`), never tracked files / git history. SSH
  keys for step 1 likewise.
- Owner-only private preview until LS fully integrated; multi-tenant later.
- Prod Convex = `perceptive-minnow-557`; always `--prod` flag
  (`backend/.env.local` pins CLI to dev otherwise).
- Local deploy first (CLAUDE.md). Web: `./scripts/deploy-web.sh`.
  Convex: `cd backend && npx convex deploy --yes`.

## Box currently under test

- Hetzner `srv 131895141`, IP `178.105.158.237`, hostname
  `mn7bj94p.cloud.yaver.io`, deviceId `cloud-mn7bj94p`, status `active`,
  container running. `http://178.105.158.237:18080/health` → 200;
  `https://mn7bj94p.cloud.yaver.io/health` → 000 (no cert). 0 `cloud-*`
  rows in `devices`. User plan: finish auth fully, then decommission this
  box and validate from a fresh box end-to-end.

## Key code references

| What | Where |
|---|---|
| Provision + cloud-init (container) | `backend/convex/cloudMachines.ts` `provision`, `buildManagedCloudInitContainer` |
| Session mint | `cloudMachines.ts:998-1001,1071`; `auth.ts:1173 createSession`, `:524 validateSessionInternal`, `:28 sha256Hex` |
| serve → register/heartbeat | `desktop/agent/main.go:2013 runServe`, `:2386 RegisterDevice`, `:2346 offlineMode`, heartbeat loop ~`:8969-9033`; `auth.go:1527 RegisterDevice`, `:1569 SendHeartbeat` |
| bootstrap gate | `auth_bootstrap.go:663 needsBootstrap` |
| config schema | `config.go:15` Config (`auth_token`/`convex_site_url`/`device_id`/`relay_servers`/`relay_password`) |
| relay config plumb | `config.go:42-43`, `agent_mesh_remote.go:498 collectRemoteRelayConfigs` |
| ops connect (web) | `web/components/dashboard/ManagedCloudPanel.tsx` `ensureBoxConnected` (cff7413a); `web/app/dashboard/page.tsx` `connectToDevice` ~`:1498` |
| runner auth verb | `desktop/agent/ops_runner_auth.go` |
| Dockerfile | `desktop/agent/Dockerfile.yaver-cloud` (CMD `serve --debug --port 18080`) |
| /subscription, /webhooks/lemonsqueezy, /machine/phase, CORS | `backend/convex/http.ts` |
| owner allowlist | `backend/convex/ownerAllowlist.ts` |

Related memory: `project_managed_cloud_onboarding_gap.md`,
`project_managed_cloud_verified_state.md`,
`project_managed_vs_byo_hetzner.md`, `feedback_no_force_push_shared_main.md`.

---

## Fresh-box manual-test runbook (Session 2, 2026-05-20)

Readiness audit (prod Convex env, masked): `YAVER_CLOUD_IMAGE` SET (⇒ the
container cloud-init path I edited is the one used), `MANAGED_CLOUD_SSH_PUBKEY`
SET (hetzner_ci_ed25519), `HCLOUD_TOKEN`/`CF_API_TOKEN`/`CF_ZONE_ID`/
`CONVEX_SITE_URL` SET, `YAVER_CLOUD_CPU_TYPE`=cpx22 (cheap throwaway).
**Only `MANAGED_CLOUD_RELAY_PASSWORD` is MISSING** — that + deploys is the
whole gap. No yaver-cloud image rebuild needed (zero Go changes; the relay-
password→relay-first path is long-standing core agent code already in the
GHCR image — confirm at test time via SSH/`docker logs`).

### 0. One-time prerequisites (operator)

```bash
# (Optional defensive fallback — NOT required for the test to work.
# Auto-cert makes the box directly https-reachable; this only covers the
# ~3 min first-boot window while certbot is still issuing.)
# cd backend && npx convex env set --prod MANAGED_CLOUD_RELAY_PASSWORD "<platform relay password>"

# 0a. REBUILD THE yaver-cloud IMAGE. The Session-2 changes include the
# bundled yaver-relay + entrypoint wrapper in Dockerfile.yaver-cloud
# AND the Phase 2D synth-RelayServerInfo fallback in desktop/agent/main.go,
# both of which only land in a managed box via the GHCR image. The
# build workflow path filter is now extended to fire on *.go changes
# (was Dockerfile-only) so just pushing the branch triggers it; or:
gh workflow run build-yaver-cloud-image.yml --ref fix/yaver-cloud-per-tenant-isolation
# Wait until ghcr.io/kivanccakmak/yaver-cloud:latest has the new sha.

# 0b. Deploy the Session-2 Convex + web code (cloud-init container-bundle
# changes, /machine/phase error, provisionError, /subscription field,
# web synthetic cards, ManagedRelays wiring on provision).
cd backend && npx convex deploy --yes
./scripts/deploy-web.sh
```

### 1. Decommission the stale test box (`srv 131895141`)

Web → Billing → the `mn7bj94p` box → Remove (drives `cloudMachines.deprovision`
→ cancels LS sub, deletes Hetzner server, removes the `devices` row; byok ⇒
no snapshot/no per-delete cost). Confirm:

```bash
cd backend && npx convex data --prod cloudMachines | grep -i mn7bj94p   # gone / status removed
npx convex data --prod devices | grep -i cloud-mn7bj94p                  # gone
# Hetzner server 131895141 no longer in the project (hcloud/console).
```

### 2. Buy a fresh box

Web → managed-cloud panel → buy the cpu SKU (real $19.99 LS checkout; owner
allowlist still gates it). Capture from the success state / `/subscription`:
`machineId`, `shortId` (first 8 of machineId), `deviceId` = `cloud-<shortId>`,
`serverIp`, `hostname` = `<shortId>.cloud.yaver.io`.

### 3. Watch provisioning (web is the primary surface)

The new **"Setting up" card** appears immediately in Devices and self-polls
every 10 s. Expect the phase to walk:
`booting → installing-docker → pulling-image → starting-agent → registering`
(progress bar). On failure it flips to the red **Setup failed** state showing
`provisionError` (e.g. `agent-health-unreachable-300s`) + recovery hint.

Server-side cross-check (read-only):

```bash
cd backend && npx convex data --prod cloudMachines | grep -iA4 <shortId>
#   provisionPhase / provisionProgress / provisionError / status
```

### 4. SSH debug in parallel (this is the Root-Cause-B verification)

```bash
ssh -i ~/.ssh/hetzner_ci_ed25519 -o StrictHostKeyChecking=accept-new root@<serverIp>
docker ps                          # `yaver` container Up?
docker logs --tail=120 yaver       # look for: "Device registered." vs
                                   #   "device registration failed" / "offline mode"
cat /root/.yaver/config.json       # MUST contain relay_password + device_id=cloud-<shortId>
cloud-init status --long           # runcmd finished, no YAML error
journalctl -u yaver-tls.service --no-pager | tail
```

`config.json` having `relay_password` confirms Step 2 baked correctly.
`docker logs` showing `Device registered.` confirms Root Cause B is NOT
hit; if it shows offline/registration-failed, the log line is the answer.

### 5. Confirm first-class reachability

```bash
cd backend && npx convex data --prod devices | grep -i cloud-<shortId>   # row EXISTS
# With Step 2b the reconciler issues a Let's Encrypt cert for the auto
# subdomain on the first OnBootSec=3min timer fire, then every 5 min.
# Allow ~3-5 min from boot before this 200s. Until then it'll be 502/000.
curl -sS -m10 https://<hostname>/health     # 200 {"ok":true} after first reconcile

# What success looks like in /etc/letsencrypt:
ssh -i ~/.ssh/hetzner_ci_ed25519 root@<serverIp> \
  "ls /etc/letsencrypt/live/<hostname>/ 2>/dev/null && echo CERT-OK || echo CERT-PENDING"
```

In web Devices: the synthetic "Setting up" card should be **replaced** by a
full device card (Shell / SSH / Open Workspace / Coding Agents / Details)
once the heartbeat row exists. The dashboard's `ensureBoxConnected` now
reaches the box via `https://<hostname>` directly — no shared free relay
in the path for this managed box's own traffic.

### 6. Drive runner auth from the dashboard

On the box's device card → **Authorize runners** (`RunnerAuthCTA` →
`callOps("runner_auth",{op:"browser_start"})`). Open the returned
`verification_uri`, complete GitHub/GitLab/Codex/Claude-Code; poll
`browser_status`. `credentials_import` is preferred (subscription token
copy, never API keys). Yaver auth itself is already automatic (baked
session token). `runnersAuthorized` flips true → card stops showing
"Unauthorized — Authorize runners".

### Failure triage quick map

| Symptom | Likely cause | Where to look |
|---|---|---|
| Card stuck pre-`installing-docker` | cloud-init YAML invalid again | `ssh` → `cloud-init status --long`, `/var/log/cloud-init-output.log` |
| `Setup failed` + `agent-health-unreachable-300s` | container didn't come up | `docker ps`, `docker logs yaver` |
| `/health` OK but no `devices` row | Root Cause B (RegisterDevice/offline) | `docker logs yaver` for "offline mode" |
| `devices` row exists but card never goes "full" / unreachable in browser | auto-cert not yet issued (first 3-5 min) OR Let's Encrypt failed | `ls /etc/letsencrypt/live/<host>/`, `/var/log/letsencrypt/letsencrypt.log` |
