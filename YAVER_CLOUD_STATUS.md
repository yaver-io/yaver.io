# Yaver Managed Cloud — Build Status

_Session dump · 2026-05-19 · prod Convex `perceptive-minnow-557` · LS store `yaver` #313855 (test mode)_

## Target

Make **"buy a managed cloud box"** work end to end:

LemonSqueezy payment → Convex webhook → provision a **dedicated Hetzner
cpx42 VM** → VM installs Docker → pulls & runs the **yaver Docker image**
(agent + every dev tool) → box registers as the buyer's device and is
usable from yaver.io.

Constraints set during the session:
- **Single-user now** (just the owner); multi-tenant is a future investment.
- **Fully secure, no code leak.**
- Provisioning uses a prebuilt **GHCR Docker image**, not per-boot installs.
- Image must carry: yaver Go agent, `gh`, `glab`, claude-code, codex,
  opencode, RN/Hermes bundling toolchain, node/expo, go, rust.
- Must support **remote OAuth** (yaver + claude + codex + opencode) +
  GLM api-key — subscription-based, never API-key billing.
- Don't overspend.

## Architecture (decided)

- **CPU SKU = Hetzner `cpx42`** (8 vCPU / 16 GB / 320 GB, amd64, €29.99/mo,
  EU `fsn1`). Price → **$34.99/mo** in LemonSqueezy (owner action, pending).
- **Per-tenant dedicated VM** is the isolation boundary (already enforced).
- VM cloud-init is **thin**: install Docker → `docker run
  ghcr.io/kivanccakmak/yaver-cloud:latest` with `/srv/yaver/state:/root`
  volume (persists remote-OAuth + GLM key across restarts/upgrades).
- nginx+certbot stay on the host, proxy `:443 → container :18080`.

## Done ✅

### Code (committed up to `09a10cca`; later changes UNCOMMITTED)
- Removed shared-preview-box override on the paid webhook path → every
  paid sub gets a **dedicated** box (no SSH/shell cross-tenant code leak).
- Agent fetch fixed: release ships `yaver-linux-<arch>.tar.gz` (not raw) —
  cloud-init now extracts it (also fixed in `provisionRelay.ts`).
- `cloudMachines.provision` clears stale `errorMessage` on success
  (no false "error" on a working box).
- `MACHINE_SPECS.cpu`: `cx42`(deprecated)→`cpx41`(US-only)→`ccx33`
  (€73.99 — too dear)→ **`cpx42`** (final).
- New `buildManagedCloudInitContainer()` + `provision()` branches on
  `YAVER_CLOUD_IMAGE` (thin Docker cloud-init vs legacy in-VM installs).
- `Dockerfile.yaver-cloud` (desktop/agent/) — the GHCR image definition.
- Web: real **♻ Delete box** button in `ManagedCloudPanel` (was a
  dead-end "recycle on device card" that didn't exist for managed boxes);
  GPU option set to **coming soon**.
- Regression test: cloud-init must tar-extract the agent, never raw-curl.
- Convex typecheck clean; `cloudMachines` tests 3/3.

### Deployed to prod Convex
- All of the above **except** the web changes (need a Cloudflare deploy).
- LS webhook recreated at the **correct host**
  `https://perceptive-minnow-557.eu-west-1.convex.site/webhooks/lemonsqueezy`
  (old `…convex.site` 404'd — this would have silently provisioned nothing).

### Prod env vars set
`LEMONSQUEEZY_API_KEY`, `LEMONSQUEEZY_STORE_ID=313855`,
`LEMONSQUEEZY_SANDBOX=true`, `LEMONSQUEEZY_YAVER_CLOUD_VARIANT_ID=1674514`,
`LEMONSQUEEZY_WEBHOOK_SECRET`, `HCLOUD_TOKEN`, `CF_API_TOKEN`,
`CF_ZONE_ID`, `CLOUD_PREVIEW_OWNER_EMAIL` (all 4 of owner's emails),
`YAVER_CLOUD_IMAGE=ghcr.io/kivanccakmak/yaver-cloud:latest`.

### Proven working earlier
A real LS test purchase created an `active` subscription + provisioned a
real Hetzner box (the `ccx33` one — deleted after the price scare). The
LS→webhook→provision chain is verified, not theoretical.

## Remaining / blockers ⏳

1. **Image build+push** — `ghcr.io/kivanccakmak/yaver-cloud:latest`
   building now (cross-arch amd64). Until pushed, a purchase provisions
   the VM but `docker pull` fails.
2. **GHCR package visibility** — new packages are private; VM pulls with
   no creds. Owner sets package **public** (no secrets in image) OR a
   read-only pull token gets baked into cloud-init. **Decision pending.**
3. **One test provision** — authorized: build → deploy → provision ONE
   cpx42, validate the container is healthy, then **auto-delete** (~1¢).
4. **Web deploy** — `./scripts/deploy-web.sh` so the Delete button +
   GPU-coming-soon ship (backend `dev-deprovision` route already live).
5. **LS price → $34.99** — owner action in LS dashboard (no API).
6. **Commit** — everything after `09a10cca` is uncommitted on branch
   `fix/yaver-cloud-per-tenant-isolation` (not pushed; awaiting OK).
7. **us-region gap** — `cpx42` not stocked in `ash`/`hil`; eu→fsn1 works,
   a us SKU needs a different type/location before selling US.

## Security caveat (multi-tenant) ⚠️

A plain Docker container on a shared VM is **not** a strong boundary for
untrusted multi-tenant code (shared kernel; escape = cross-tenant leak).
- **Single-user (now): safe** — one buyer, one dedicated VM, sole tenant.
- **Multi-tenant (future): needs** per-tenant VM (current model) **or**
  a hardened runtime — Kata / Firecracker microVM / gVisor — per tenant.
  Do **not** ship plain-Docker multi-tenant as "secure". Tracked: task #5.

## Cost notes 💸

- No real money: LS is **test mode**.
- `ccx33` mistake box: deleted ~6 min after create → cents, no $73 charge
  (Hetzner bills hourly; delete stops the meter; power-off does NOT).
- cpx42 ≈ €0.04/hr; the test box auto-deletes in minutes (~1¢).
- GHCR image: free.

## Task list

| # | Task | Status |
|---|---|---|
| 1 | CPU SKU → cpx42 | ✅ done + deployed |
| 2 | Delete box wired into web UI | ✅ code done (web deploy pending) |
| 3 | Build yaver Docker image | ⏳ building/pushing |
| 4 | Container provisioning + remote-OAuth | ✅ code done + deployed |
| 5 | Phase-2 secure multi-tenant runtime | 📋 planned |
