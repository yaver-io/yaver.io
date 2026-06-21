# Beta — deep test plan (all cases, your infra)

Date 2026-06-20. Goal: zero-to-hero every beta case end-to-end on the user's own
infra, with the Mac kept load-free.

## Topology (fixed)

| Role | Machine | Load |
|---|---|---|
| Orchestrator / control | **this Mac** | LIGHT only — drives tests, observes. No builds/Hermes/redroid. |
| "Phone" / edge | general-purpose **Hetzner** (Talos box) | plays the device that receives Hermes push / runs app. NOT a beta exec host (`YAVER_BETA_HOST` unset). Stays Talos+personal. |
| Remote dev | **new cpx51 beta box** (`yaver-beta-builder` → snapshot → pool) | ALL heavy work: opencode-as-tenant, redroid, Hermes, Playwright/chromedp. `YAVER_BETA_HOST=1`. Scale-to-zero. |

## Phases & pass criteria

### P0 — Infra (DONE / verifying)
- [x] Control plane: seed/entitlement/no-leak (serhat, batikan, test=`carrotbet,sfmg,nizam`).
- [x] Relay `/beta/wake` cost-gate: attacker → 401, pool stays down ($0).
- [x] Pool controller: scale-to-zero decisions (dry-run tests).
- [x] Unwire gate: box refuses beta unless `YAVER_BETA_HOST=1`.
- [x] Keyless GLM gateway: ygw token → z.ai PONG.
- [x] Close/open preserves data: snapshot→delete→recreate, marker exact. OPEN 49s.
- [x] Cost: idle €0.10/mo, worst ~$99/mo, attacker $0.

### P1 — Golden image (IN PROGRESS)
- [ ] cpx51 provisioned; bake: yaver agent (`YAVER_BETA_HOST=1`) + opencode (+claude/codex) + redroid(amd64+binder) + Playwright/Chromium + chromedp + node/Hermes toolchain + `/srv/yaver/tenants`.
- [ ] Smoke: `modprobe binder_linux` OK; redroid container boots; `npx playwright` chromium runs; opencode `--version`; yaver agent serves.
- [ ] Snapshot → this is the scale-to-zero "zero" state.
- Pass: snapshot id recorded; recreate-from-snapshot boots + all tools present.

### P2 — Data-plane execution
- [ ] `runnerNeedsTenantRuntime` includes opencode (beta is opencode-only) so beta tasks run AS `yv-<id>` confined to `/srv/yaver/tenants/<id>`.
- [ ] `/beta/push` (or task path): a beta user's coding request runs opencode-as-tenant with `betaTenantRunnerEnv` (gateway inference, ZERO host secrets).
- [ ] Repo seeding: `beta_broker`+`beta_scrub` clone scrubbed `carrotbet/sfmg/nizam` into the tenant (sfmg 8.6G → shallow/lazy).
- [ ] Controller real `provisionFn`/`reapFn` → create-from-snapshot + snapshot+delete.
- Pass: smoke test — two tenants get different HOME/cwd/auth; no host secret in tenant env; secret files scrubbed from seeded repos.

### P3 — Apps zero-to-hero (the matrix)
Each = wake → provision → seed → opencode edit → build → preview → feedback → git → (mirror) → deploy → reap.

| App | Surface | Build | Notes / pass |
|---|---|---|---|
| **new sandbox app** | mobile | serverless-lite | create→data API→export canonical bundle→deploy(self/cloud)→share→join (already proven on a normal agent; re-run as beta tenant) |
| **nizam** (1.7G web+mobile) | web + mobile | `npm run dev` (web_preview) + Hermes push | edit via opencode → web preview live + mobile Hermes reload → feedback loop |
| **carrotbet** (1.1G RN, private) | mobile | Hermes | scrub betting model/creds; Hermes build → phone preview |
| **sfmg** | mobile | Hermes | **8.6G is DISK not RAM** (node_modules 5.6G + android build artifacts 2.8G; source only 5.2M/396 files/45 deps). Hermes RAM ≈ 2-4G → **8G box is fine**. Seed SOURCE only (~13M), `npm install` on box; never seed node_modules/build artifacts. |
| **yaver** (self) | — | — | self-host/meta only |
| ~~talos~~ | — | — | **EXCLUDED — private business + secrets; do NOT beta-share** |

### P4 — Surfaces
- [ ] Mobile app: beta surface (badge), no-key inference radio, develop nizam from phone (browser-automation of the RN app via the Hetzner-as-phone or redroid).
- [ ] Web UI: BetaWorkspaceView → VibeCodingView, develop nizam from browser.
- Pass: a beta user signs in (`betatester@yaver.io`/`BetaTester2026!`), sees only Beta surface, codes nizam, no infra/owner identity leaked.

### P5 — yaver-git (incl. guest-shared)
- [ ] managed-git per new project: init → checkpoint → backup → restore.
- [ ] yaver-git for nizam/sfmg/carrotbet: mirror to existing GitHub repos (redact tokens).
- [ ] guest-shared yaver-git: one shared repo + per-guest branches via `beta_broker` (reads shared, writes branch-isolated, no mirror creds to guests).
- Pass: a guest's commit lands on `beta/<id>/<ts>` of the shared repo; owner can mirror; guest never sees creds.

### P6 — Security / cost gates (re-assert on the live box)
- [ ] attacker `/beta/wake` (no token) → 401, no provision.
- [ ] tenant has no `HCLOUD_TOKEN`/GLM key/owner vault/git creds in env.
- [ ] seeded repos: `.env*`/keys/`.ssh`/service-account scrubbed.
- [ ] device list for the beta user → owner box NOT shown.
- [ ] idle 20m → reap → snapshot+delete → idle cost €0.10/mo.

## Execution order
P1 (golden image) → P2 (tenant exec) → P3 nizam first (smallest hybrid) → P3 carrotbet → P3 sfmg (heavy) → P3 new-sandbox → P4 surfaces → P5 git → P6 re-assert.

## Cost guardrails during testing
- One box at a time; reap/delete after each phase (don't leave cpx51 running).
- `HCLOUD_TOKEN` stays out of prod control plane until P2 controller verified (so prod never auto-provisions mid-build).
- Every provisioned box deleted at phase end; snapshots pruned except the golden one.

## Advanced yaver-git (requested 2026-06-21) — spec

Owner's accumulated asks for yaver-git. Privacy first: managed-git bare repos are
LOCAL + credential-free; mirrors go to PRIVATE GitHub/GitLab repos only, NEVER
public (talos especially). Tenant pushes never carry creds (beta_broker model).

1. **Mirror-on-push** (NEW — not built): a `post-receive` hook on the managed-git
   bare repo → if a mirror is connected (`ManagedGitMirrorToProvider` /
   `/managed-git/mirrors/connect` already exist, MANUAL today) → auto-`git push`
   to the GitHub/GitLab mirror. Start with the OWNER's account (owner holds the
   creds; the hook runs owner-side, NOT in a tenant's credential-free push).
2. **AI-runner merge-conflict resolution** (NEW): when a push/sync conflicts,
   spawn an opencode/runner task to resolve the conflict, then continue. Keeps
   normies out of rebase/merge hell.
3. **Bidirectional mirror sync** (NEW): periodic/triggered sync managed-git ↔
   mirror (pull remote changes in too), with the AI-runner resolver on conflict.
4. **Isolation invariants** (enforced): per-tenant 0700 partition
   (/srv/yaver/tenants/<userId>) → beta users never see each other's repos;
   talos only in the owner/test account's grant; mirror tokens redacted from
   errors; tenant never gets mirror creds (broker pushes owner-side).

Build order: (1) mirror-on-push for owner → (2) AI conflict resolver → (3) sync.
