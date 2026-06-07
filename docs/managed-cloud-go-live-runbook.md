# Managed-Cloud Go-Live + Full Real Test Runbook

> Owner-only. Real Hetzner spend on the platform token. Every step is
> deliberate by design — nothing here is auto-run. Code is shipped on
> main (P0–P6, commit aab3892b; agent npm 1.99.219).

## A. Deploy the feature to Convex prod (clean, no parallel WIP)

Do NOT `convex deploy` from the dev working tree — it bundles the
parallel session's uncommitted `cloudMachines.ts`. Deploy from clean main:

```bash
git fetch github main
git worktree add /tmp/yaver-deploy github/main
cd /tmp/yaver-deploy/backend && npm ci && npx convex deploy --yes
cd - && git worktree remove /tmp/yaver-deploy --force
```

At this point HCLOUD_TOKEN is still UNSET ⇒ everything is fail-closed
dry-run. Run the no-spend validation first:

```bash
TOK=<owner bearer> MID=<machineId> SITE=https://<dep>.convex.site \
CRON_BEARER=<CRON_TRIGGER_SECRET> scripts/e2e-managed-cloud-dryrun.sh
```

All assertions PASS ⇒ P0–P6 wiring proven, $0, machine untouched.

## B. Remove the old box (yaver-cpu-mn7bj94p) — your action

This needs the platform token. Set it (real-spend gate opens):

```bash
cd /tmp/yaver-deploy/backend   # or any clean checkout
npx convex env set HCLOUD_TOKEN <hetzner-cloud-api-token> --prod
```

Then delete the old box via the EXISTING decommission (parallel
session's code — snapshots, then destroys, then frees billing):

- Web ManagedCloudPanel → the box row → **♻ Delete box**, or
- `POST $SITE/billing/yaver-cloud/dev-deprovision {"machineId":"<yaver-cpu-mn7bj94p id>"}`
  with the owner bearer.

Verify it's gone in `yaver devices` + Hetzner console. (A pre-delete
snapshot is kept by the fail-closed invariant — delete that snapshot
too if you don't want the ~€0.50/mo, via `cloud_snapshot_delete`.)

## C. Provision a fresh box + full REAL stop/start test

1. Buy/adopt a new managed box (ManagedCloudPanel → Buy, or
   `dev-activate`/`dev-adopt`). Wait for `status:active`.
2. Top up the wallet: `POST $SITE/billing/yaver-cloud/topup-dev {"amountCents":N}`.
3. **Real STOP** (snapshot+destroy — money-safe, recover-safe):
   `POST $SITE/billing/yaver-cloud/stop {"machineId":"<new id>"}`
   - With HCLOUD_TOKEN set ⇒ NOT dry-run. Expect `status:"paused"`,
     `snapshotId:"<image>"`, server gone in Hetzner, billing stopped.
   - Fail-closed invariant: if the snapshot fails the server is NOT
     deleted (status:error, data safe).
4. **Real START** (recreate from snapshot):
   `POST $SITE/billing/yaver-cloud/start {"machineId":"<new id>"}`
   - `canStart` gate first (402 if balance < reserve). Then a new
     Hetzner server from the pause snapshot; row gets new id/ip;
     `status:"active"`.
5. **Meter**: external Hetzner systemd timer should
   `POST $SITE/crons/run {"name":"cloudMeter"}` hourly. For the test,
   fire it manually and watch `balance` drop. The cron is `dryRun:true`
   until launch by design; flip it live with a single Convex env (no
   code change): `npx convex env set YAVER_CLOUD_METER_LIVE true --prod`.
6. Agent-side BYO stop/start (separate path): on the box,
   `YAVER_CLOUD_STOPSTART_LIVE=1` then `yaver` ops `cloud_stop` /
   `cloud_start` with `confirm:true` — uses the box's OWN vault token.

## D. Money-safety recap (all by construction)

- HCLOUD_TOKEN unset ⇒ every stop/start is dry-run, no spend, nothing
  destroyed. Setting it is the single deliberate go-live switch.
- Snapshot-before-delete is mandatory and fail-closed (failed snapshot
  aborts the delete).
- Prepaid floor (`canStart`, two-part reserve) blocks starting a box
  the wallet can't afford to safely stop again.
- Meter defaults `dryRun:true` until you flip it — wallets don't burn
  pre-launch.
- Roll back: a paused box is recoverable from its snapshot; resume
  recreates it. cloudMachines.ts (parallel session) untouched.

## E. Prepaid credit front door (OpenAI-style top-up) + go-live flags

The wallet is now fundable with REAL money via web credit packs (no
Apple/Google IAP — compute is remote IaaS, sold on the web). Flow:
buy a pack → LemonSqueezy one-time order → `order_created` webhook →
`topUpForOrder` credits the wallet idempotently (keyed on order id).

1. **Create the LS one-time products** (one per pack: $10/$25/$50/$100)
   in the LemonSqueezy store, then wire each variant id:
   ```bash
   npx convex env set LEMONSQUEEZY_CREDIT_PACK_P10_VARIANT_ID  <vid> --prod
   npx convex env set LEMONSQUEEZY_CREDIT_PACK_P25_VARIANT_ID  <vid> --prod
   npx convex env set LEMONSQUEEZY_CREDIT_PACK_P50_VARIANT_ID  <vid> --prod
   npx convex env set LEMONSQUEEZY_CREDIT_PACK_P100_VARIANT_ID <vid> --prod
   ```
   Catalog (amounts) is server-side in `http.ts:CREDIT_PACKS` — the
   webhook never trusts an amount from the payload.
2. **Test checkout**: `POST $SITE/billing/credits/checkout {"packId":"p25"}`
   → returns a LemonSqueezy URL (503 until the variant env is set).
   Pay it (sandbox first) → webhook credits the wallet → balance rises.
3. **Spin up from prepaid** (no subscription): `POST
   $SITE/billing/yaver-cloud/provision {"machineType":"cpu"}` — 402 if
   balance < reserve; otherwise creates + provisions a wallet-funded box.
4. **Open it to all users** (leave private-preview): set the launch flag
   `npx convex env set YAVER_CLOUD_PUBLIC true --prod`. Until then only
   the owner allowlist can touch the prepaid surfaces. Every money route
   stays independently gated (HCLOUD_TOKEN for spend, balance for debit),
   so opening access never lets a stranger spend Yaver's money — only
   their own credit.

### Env-flag summary (all default to safe/off)

| Env (Convex `--prod`) | Default | Effect when set |
|---|---|---|
| `HCLOUD_TOKEN` | unset | real Hetzner create/snapshot/delete (else dry-run) |
| `YAVER_CLOUD_METER_LIVE` | false | wallet meter burns real balance (else dryRun ledger) |
| `YAVER_CLOUD_PUBLIC` | false | prepaid surfaces open to all authed users (else owner-only) |
| `YAVER_CLOUD_MARKUP_CPU` | 2 | per-SKU markup over raw COGS (cpu) |
| `YAVER_CLOUD_MARKUP_GPU` | 3 | per-SKU markup over raw COGS (gpu) |
| `LEMONSQUEEZY_CREDIT_PACK_*_VARIANT_ID` | unset | enables that credit pack's checkout |

Going fully live = set all four (token, meter-live, public, pack
variants). Any subset is a valid intermediate (e.g. owner-only real
test = HCLOUD_TOKEN + METER_LIVE, leave PUBLIC off).
