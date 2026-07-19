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

## C. Provision a fresh Cloud Workspace + full REAL stop/start test

1. Subscribe to Cloud Workspace from the web billing UI. Do not use mobile
   checkout. The subscription webhook/reconcile path creates the managed row.
   Owner-only `dev-activate` remains a developer bypass and now requires the
   local `YAVER_CLOUD_DEV_BYPASS=1` CLI flag path before it is reachable from
   the CLI.
2. Wait for `status:active`, then verify auto-park is enabled for the row.
3. **Real STOP** (volume-backed delete, or legacy snapshot+destroy —
   money-safe, recover-safe):
   `POST $SITE/billing/yaver-cloud/stop {"machineId":"<new id>"}`
   - With HCLOUD_TOKEN set ⇒ NOT dry-run. Expect `status:"paused"`,
     server gone in Hetzner, billing stopped.
   - Volume-backed rows keep data on the attached volume and skip slow
     snapshotting. Legacy rows snapshot before delete.
   - Fail-closed invariant: if the snapshot fails the server is NOT
     deleted (status:error, data safe).
4. **Real START** (recreate from volume/base image or legacy snapshot):
   `POST $SITE/billing/yaver-cloud/start {"machineId":"<new id>"}`
   - `canStart` gate first. Included Cloud Workspace standard credits can
     satisfy the gate; once exhausted, prepaid/internal reserve logic must
     still prevent unaffordable wake.
   - The row gets a new provider server id/ip; health checks decide when it is
     actually usable.
5. **Meter and idle park**: Convex-native idle sweep is live by default and
   independent of wallet meter simulation. `YAVER_CLOUD_IDLE_DISABLE=true` is
   the emergency brake. Do not disable it for customer workspaces.
6. Agent-side BYO stop/start (separate path): on a user-owned BYO box,
   `YAVER_CLOUD_STOPSTART_LIVE=1` then `yaver` ops `cloud_stop` /
   `cloud_start` with `confirm:true` — uses the box's OWN vault token.

## D. Money-safety recap (all by construction)

- HCLOUD_TOKEN unset ⇒ every stop/start is dry-run, no spend, nothing
  destroyed. Setting it is the single deliberate go-live switch.
- Snapshot-before-delete is mandatory and fail-closed (failed snapshot
  aborts the delete).
- Included allowance + internal reserve gates block starting a box that cannot
  be safely parked again once the monthly Cloud Workspace allowance is exhausted.
- Legacy credit-pack checkout and prepaid workspace provisioning are retired:
  `/billing/credits/checkout` and `/billing/yaver-cloud/provision` return HTTP
  410. Do not create LemonSqueezy one-time credit-pack products for this model.
- Roll back: a paused box is recoverable from its volume/base image or legacy
  snapshot; resume recreates it.

## E. Flat-product go-live flags

The public catalog has only Free, Relay Pro, and Cloud Workspace. Purchases and
unsubscribe/update-payment flows are web-only.

1. **Relay Pro**: set the LemonSqueezy variant env for Relay Pro and test
   `/billing/checkout {"productId":"relay-pro"}` from web.
2. **Cloud Workspace**: set the Cloud Workspace variant env and test
   `/billing/checkout {"productId":"cloud-workspace"}` from web. The active
   subscription webhook or `/billing/yaver-cloud/reconcile` provisions the
   managed workspace row.
3. **Open Cloud Workspace controls**: set
   `npx convex env set YAVER_CLOUD_PUBLIC true --prod` only when the web
   billing flow, unsubscribe flow, auto-park, and reconcile tests pass.
4. **Keep mobile purchase-free**: mobile may show entitlement/status and route
   users to web; it must not call checkout, portal, cancel, or plan-change APIs.

### Env-flag summary (all default to safe/off)

| Env (Convex `--prod`) | Default | Effect when set |
|---|---|---|
| `HCLOUD_TOKEN` | unset | real Hetzner create/snapshot/delete (else dry-run) |
| `YAVER_CLOUD_IDLE_DISABLE` | false | emergency brake that disables auto-park when true |
| `YAVER_CLOUD_PUBLIC` | false | Cloud Workspace controls open to all authed users (else owner-only) |
| `YAVER_CLOUD_WORKSPACE_VARIANT_ID` | unset | enables Cloud Workspace checkout |
| `YAVER_RELAY_PRO_VARIANT_ID` | unset | enables Relay Pro checkout |
| `YAVER_CLOUD_MARKUP_STANDARD` | 2 | internal margin model for standard workspace overage |
| `YAVER_CLOUD_DEV_BYPASS` | unset | local CLI-only developer bypass gate; never set for users |

Going fully live = set provider token, product variant ids, and public access
after the web billing/unsubscribe/reconcile tests pass. Do not re-enable credit
packs or direct prepaid provisioning.
