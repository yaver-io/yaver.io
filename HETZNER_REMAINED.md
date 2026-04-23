# Hetzner Remained

This file is the single-place dump for what remains around Hetzner, shared infra, guest access, Docker isolation, and the future brokered host-action model.

It is derived from:
- `remained.md`
- `docs/remained.md`
- `REMAINED_HETZNER_CLOUD_SERVICE.md`
- `README.md`
- `CLAUDE.md`

It is intentionally practical: what is already true, what is still missing, and what should happen next.

## 1. Current Position

Yaver already supports the idea of using an always-on box you own, and Hetzner is the main example of that model.

There are really two Hetzner stories in the codebase:

1. Your own always-on dev machine
- A Hetzner VPS running `yaver serve`
- Used as your personal remote dev box
- Fits the "my own infra, not SaaS CI" story

2. Future Yaver Cloud managed tier
- Convex + LemonSqueezy + Hetzner + Cloudflare flow
- Provisions a dedicated machine for the paying user
- Still needs provider-side setup and real end-to-end validation

## 2. What Has Already Landed

### Guest / shared-infra policy

These pieces now exist:
- guest scope model (`full`, `feedback-only`)
- project allowlist support
- runner allowlist support
- device-scoped sharing support
- `requireIsolation` guest flag
- resource presets:
  - `machine-only`
  - `machine-with-host-keys`
  - `desktop-control`
  - `desktop-control-with-host-keys`

### Guest execution hardening

These are now enforced in the Go agent:
- guest `/tasks` must carry `projectName` when restricted
- guest project path is resolved server-side
- guest cannot smuggle `workDir`
- guest runner allowlist is enforced
- omitted `runner` still respects the allowlist by checking the effective default runner
- `/agent/runners` is filtered for guests, so they only see shared runners
- isolated guests fail closed when Docker is required but unavailable
- isolated guests can reload an already-running allowed project
- isolated guests cannot directly start/stop/retarget/native-build host dev infrastructure

## 3. What Is Still Missing

## 3.1 Real brokered host actions

This is the biggest missing technical piece.

Right now:
- isolated guests are blocked from host-dev mutations
- but they do not yet get a safe brokered replacement path

What is still needed:
- typed host actions such as:
  - `start_dev(projectName)`
  - `stop_dev(projectName)`
  - `build_native(projectName, platform)`
  - `reload_bundle(projectName)`
- all of those should run through a host-side broker
- all of them should reuse existing guest policy:
  - allowed devices
  - allowed projects
  - allowed runners
  - require isolation
  - host key policy

Without this, the isolated-guest story is safer, but not complete.

## 3.2 Real Hetzner two-account end-to-end validation

This has not been completed yet.

The intended real test is:
- create two real email/password Yaver accounts
- sign the Hetzner machine into the host account
- invite the guest account
- share only one project
- share only one device
- share only selected runners
- require isolation
- verify task, reload, revoke, and deny flows against the real machine

This is still pending.

## 3.3 Remote desktop / browser / tunnel as real capabilities

The config and policy fields exist:
- `allowDesktopControl`
- `allowBrowserControl`
- `allowTunnelForward`

But the full product/runtime path is still not finished.

Still needed:
- actual runtime exposure for these capabilities
- explicit host approval per session
- separate revocation
- no accidental widening from one capability to another

## 3.4 Managed Yaver Cloud provider setup

The codebase says the managed tier is mostly implemented, but real provider setup is still required.

Still needed:
- LemonSqueezy test/prod credentials
- webhook setup
- Hetzner Cloud API token
- Cloudflare token + zone wiring
- deployment of `cloud.yaver.io`
- first real paid provisioning test

## 4. Hetzner / Yaver Cloud Human Checklist

From the current `REMAINED_HETZNER_CLOUD_SERVICE.md` flow, the remaining human tasks are:

### LemonSqueezy
- create or confirm test-mode store
- create Yaver Cloud product + variant
- rotate API key
- set store ID
- set variant ID
- create webhook
- set webhook secret
- optionally set redirect/receipt vars

### Hetzner
- create dedicated Hetzner project
- create RW API token
- set `HCLOUD_TOKEN`

### Cloudflare
- get `CF_ZONE_ID`
- create scoped token
- set `CF_API_TOKEN`

### Cloud tenant
- provision a fresh VPS
- point `cloud.yaver.io`
- run `cloud/deploy.sh`
- retrieve `CLOUD_OWNER_TOKEN`

### Dashboard
- deploy web dashboard
- verify Domains tab

### First real provisioning flow
- complete checkout
- verify webhook delivery
- verify machine provisioning
- verify DNS assignment
- verify health check
- verify machine appears in dashboard

## 5. Product Gaps The User Would Notice

If a user actually tries to use Hetzner/shared infra today, the missing pieces they would notice most are:

1. Isolated guests cannot yet do brokered host actions
- they can be safely blocked
- but not yet safely delegated

2. Shared infra is not fully validated end-to-end on a live Hetzner box
- especially with two real accounts

3. Desktop/browser/tunnel sharing is not yet product-complete

4. Managed Yaver Cloud still depends on real provider wiring

## 6. Strategic Position

The docs are consistent about this:
- Hetzner is the preferred example of "always-on box I own"
- Yaver is not trying to become a generic hosted CI replacement first
- the core wedge is still "your own machine, your own infra"

That means the most important remaining Hetzner work is not billing first.

It is:
- safe shared infra
- brokered host actions
- real live two-account validation

## 7. Recommended Execution Order

If this is resumed later, the recommended order is:

1. Build the brokered host-action path
- narrow typed verbs
- reuse guest policy checks
- no arbitrary host shell for isolated guests

2. Run real Hetzner two-account E2E
- host account
- guest account
- project/device/runner slicing
- isolation required
- revoke and deny cases

3. Finish runtime support for desktop/browser/tunnel grants

4. Finish provider-side setup for managed Yaver Cloud

## 8. Concrete Ship Blockers

These are the true blockers for saying "Hetzner shared infra is properly done":

- no real broker for isolated guest host actions
- no completed live two-account Hetzner validation
- no full runtime implementation for desktop/browser/tunnel guest grants

These are blockers for saying "managed Yaver Cloud is live":

- LemonSqueezy credentials/webhook not fully wired in real env
- Hetzner token not fully wired in real env
- Cloudflare token/zone not fully wired in real env
- cloud tenant not deployed and verified end-to-end

## 9. Short Bottom Line

Current state:
- Hetzner as an always-on host is already part of the product story
- guest project/device/runner slicing is much stronger now
- isolated guests are safer now

Still remaining:
- real brokered host actions
- real live Hetzner two-account test
- completion of desktop/browser/tunnel sharing
- provider wiring for managed Yaver Cloud
