# Yaver Remaining Work — Dependency-Ordered Dump (2026-08-10)

Status: code is the source of truth. Every item cites what was verified live this
session (configured owner account, prod Convex
`perceptive-minnow-557`, real Hetzner boxes) or a committed audit. Priorities are
expressed as **dependency order** — a later phase should not start before the
phases it depends on are green.

Commits that landed 2026-08-09/10 (all pushed to `github` main, backend deployed):
`9da96dae9` (provision+wake core fixes) · `397f55606` (owner-path relay wiring +
decommission hygiene) · `7c4fe5d20` (device-row cleanup) · `147fe64b2` (failure
render + field dedupe) — plus the parallel session's `376fa3079`/`473654482`/
`5272bf133`/`1803b305c`.

---

## Phase 0 — Owner-only, NO billing integration: ✅ VERIFIED (goal closed)

The owner account path works end-to-end **without any subscription/payment**.
Everything below was exercised on real boxes this session.

| Capability | Evidence |
|---|---|
| Owner gate (no sub required) | `cloudPreviewOwner: True`, `cloudAccess: True`; `canProvisionManaged` = active sub **OR** owner allowlist (`subscriptions.ts:122`) |
| Provision without billing | Box `mn71pn9c` provisioned via `internal.cloudMachines.create` (no sub, owner allowlist) → active |
| Park (sleep) | `POST /billing/yaver-cloud/stop` → `paused`, server deleted (404 at Hetzner), volume kept |
| Wake (resume) | `POST /billing/yaver-cloud/start` → auto-retry on the volume-release race → **active in ~4.5 min**, `wakeOutcome: ready` |
| Projects after wake | `/projects` shows the git-backed workspace (`/root/Workspace/yaver.io → github.com/kivanccakmak/yaver.io`) — **identical before and after sleep** |
| Git integration after wake | `git credential fill` resolves github+gitlab; **GitHub private repos accessible** (API probe) |
| API keys after wake | opencode `deepseek/deepseek-flash` → **PONG** (auth.json + provider config survive on the volume) |
| Removal | `POST /billing/yaver-cloud/dev-deprovision` (the web Delete route) → server 404, **volumes NONE, servers NONE, DNS gone, device rows NONE** |
| Web UI controls | `ManagedCloudPanel.tsx`: ⏸ Pause (active) / ▶ Resume (paused) / Delete — all wired to the correct owner-gated routes with confirm dialogs |
| Wallet | $24.50 prepaid balance unused — no billing mechanics in the owner path |

**Known owner-path rough edges (non-blocking, worked around manually this session):**

1. **Fresh-box git hydration gap** — `autoHydrateGitCredentialsOnManagedBox` runs once at
   boot and no-ops if no owner device is reachable in the ~6 min window. Hit twice this
   session (fresh boxes booted without creds; hydrated manually over SSH). The box still
   works; private git needs the manual hydration or a re-auth.
2. **Stale agent image** — the `yaver-cloud` container image ships agent `1.99.285`
   (current is `1.99.409+`). Functional, but wakes do a full docker install/pull because
   no golden snapshot is baked (`YAVER_CLOUD_IMAGE_ID_*` unset).

---

## Phase 1 — Owner-path hardening (dependencies for everything later)

1. **Reliable fresh-box hydration** (fixes rough edge 1)
   - Server-side/vault hydration decoupled from the primary device, OR an on-demand
     retry when a task needs git, OR hydrate at provision time from any online owner
     device. Also set `git config --global credential.helper store` during hydration
     (found missing this session).
   - Depends on: nothing. **Unblocks:** private-git-first-time UX.
2. **`autoParkMinutes` single source** — reconcile env (30) / sweep (30) / row+surface
   (45) / four-tier doc (20) to ONE knob (`YAVER_CLOUD_IDLE_MINUTES`), documented.
3. **Egress IP reservation verify/fix** — `reserveEgressIpIfEligible` never persisted a
   Primary IP this session (stability was coincidental Hetzner recycling). Diagnostics
   are logged; verify on a paid-path wake or fix the eligibility condition.
4. **SPKI pin wiring** — boxes log "no SPKI pin configured — relay identity UNVERIFIED";
   `/config` already carries `spkiPin` for the free relay — consume it client-side.
5. **Agent image rebuild + golden snapshot** (fixes rough edge 2) —
   `scripts/build-cloud-image.sh` → rebuild `ghcr.io/kivanccakmak/yaver-cloud` with the
   current agent; bake a golden snapshot and set `YAVER_CLOUD_IMAGE_ID_AMD64` (wake
   drops from ~4.5 min to ~1 min).
6. **Owner relay-config reconciliation** — the old box's relay clobbered
   `relay_password` on devices; the free-relay session expired; a stopped
   `jh7e0nd0.relay.yaver.io` row lingers. One account-level `yaver relay set-password`
   + cleanup of the Mac daemon config.

---

## Phase 2 — Billing integration (LemonSqueezy) — depends on Phase 1 green

**Owner manual actions first (cannot be automated):**
- [ ] Create the **$19 "Cloud Agent" LS variant** → paste the id → set
  `LEMONSQUEEZY_YAVER_CLOUD_HOSTED_VARIANT_ID` on prod (until then Agent checkout is a
  clean 503 — safe).
- [ ] **GitLab re-auth** — the mirrored GitLab PAT is dead (HTTP 401 on the box AND the
  Mac). Run `yaver git oauth gitlab` once; then re-verify on-box.

**Code (dependency order):**
1. **LS webhook gaps** — `subscription_paused` (box keeps running → **cost leak**),
   `subscription_unpaused`, `subscription_plan_changed`, `subscription_payment_success`,
   `order_refunded` are silent no-ops (lemon-squeezy audit G1). Fix `subscription_paused`
   first (park compute + revoke entitlements).
2. **Subscription↔LS reconciliation** — poll `/v1/subscriptions` so a missed webhook
   can't wedge billing state (G2).
3. **Metering → invoice** — `managedUsage`/`creditUsage` accumulate; nothing bills
   (`YAVER_CLOUD_METER_LIVE` unset). Prepaid wallet ($24.50) + overage path exist but are
   dormant. Build the usage→charge pipe (launch gate).
4. **Gateway daily-cap backstop** — `userOptedIntoKind` has no writer; `gatewayPolicy`
   absent → enabled (fails OPEN). Only bites if hosted inference sells.
5. **Launch flips** (only after 1-4 + Phase 0): `HIDE_PAID_UI=false`, `YAVER_CLOUD_PUBLIC
   =true`, checkout to live, confirm variants.

---

## Phase 3 — Relay Pro — depends on Phase 2 (billing) + Phase 1.4/1.6

1. **Shared-pool live test** — `relayPool.ts` (committed `376fa3079`) exists; verify at
   20-50 tenants/box (96% margin) vs the dedicated path (16%). Needs a paid sub to test
   properly.
2. **RecordBytes on the SSH splice lane** — the splice bypasses metering; required for
   QoS + byte caps (audit §2.3).
3. **Relay QoS tiers** (latency vs bulk) + per-tenant caps — after 2.
4. **SPKI enforcement** — after Phase 1.4 (wire the pin, then enforce).

---

## Phase 4 — Resilience / scale — depends on Phases 1-3

1. **GOLD checkpoint + cold restore (Hetzner Storage)** — the one real resilience gap:
   the volume is fsn1-bound. restic/borg, **client-side encrypted** (privacy contract),
   deduped, pushed on park + before decommission; restore = new volume in any location.
   Cost ≈ €0.40/mo/workspace at 80GB (object storage €0.005/GB vs volume €0.044/GB).
   Matches `cloud-multiprovider-placement-architecture.md` §6.2 hybrid + state-audit §12
   step 5. **Exclude or encrypt `.yaver` secrets** in the artifact.
2. **Cron-host SPOF** — meter/idle/reconcile run from an external box POSTing
   `/crons/run`; if it dies, metering stops silently. Move to Convex `cronJobs` or a
   second timer host + alarm.
3. **Warm pool** (`cloudPoolPlacement.ts` — zero callers) — instant wake at scale; only
   matters publicly.
4. **Multi-provider live wiring** — only after credit terms are read; the GOLD artifact
   (4.1) is the enabler.

---

## Phase 5 — Platform release — depends on Phases 1-4 green

1. **Web/Cloudflare deploy** — ships `147fe64b2`'s web half (real failure-cause render in
   the failed-state card). `./deploy/deploy.sh web`.
2. **cli release** — `cli/v*` npm publish (ships every agent-side fix; held per open
   todos).
3. **OpenRouter gateway deploy + hosted-box join** — parallel session's gateway Worker is
   built but NOT deployed; then `YAVER_GATEWAY_URL` + per-user key injection on hosted
   boxes.
4. **Pin the flaky test** — `cloudMachines.test.mts` timestamp off-by-one (passed in all
   runs, but was flaky once).

---

## Dependency graph (one line each)

```
Phase 0 (owner, no billing) ✅ DONE
Phase 1 (owner hardening)  → Phase 2 (billing) → Phase 3 (Relay Pro)
Phase 4 (resilience/scale) ← after 1-3
Phase 5 (release)          ← after 1-4
Owner manual (GitLab re-auth, $19 LS variant) blocks Phase 2.1+ and the public funnel.
```

**Next recommended action:** the Phase 1 small-correctness batch (items 1-4) — it
hardens the owner path the goal just proved, and unblocks clean billing work.
