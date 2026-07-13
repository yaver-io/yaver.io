# Yaver Security Audit — 2026-07-13 (managed-box auth)

> **Scope**: the zero-friction managed-cloud provisioning + OAuth-propagation
> path exercised end-to-end this session (provision → broker → box token → relay
> → git creds). Focus: "if the box / this code leaks, what happens?" on a PUBLIC
> repo. Fixes tracked in `machine-asymmetric-auth-design.md`.

## Critical

### C-1. Managed box holds a FULL-OWNER 1-year bearer token
A brokered box gets `sessions {userId, expiresAt:+365d}` with **no scope** — a
full owner login, sitting in the box's `/root/.yaver/config.json`. The box is
customer/attacker-**rootable**. Extract the token → full account access for up to
a year: delete the owner's OTHER machines, drain the prepaid wallet, provision
boxes on their dime, enumerate devices.
**Severity**: CRITICAL (rooted-box → full account compromise; the arbitrage
threat at the token layer).
**Status**: Phase 0 shipped 2026-07-13 — `sessions.scope` field added; broker now
stamps box tokens `scope:"machine"` (recorded, **not yet enforced**). Enforcement
(default-deny account-level ops for machine scope; then device-key signatures +
revocation) is Phases 1–4 in the design doc — land behind a flag, tested.

## High

### H-1. Relay per-user password is a shared secret extractable from a box
A box authenticates to `public.yaver.io` with the owner's `relayPassword`
(`X-Relay-Password`). A rooted box leaks it → an attacker can impersonate the
owner's devices on the relay until the password is rotated (one shared secret per
user, not per device).
**Severity**: HIGH.
**Fix**: migrate the relay to **device-signature-only** auth (prior art already
present: `relay/sigauth.go`, `/relay/resolve-sig`, `yaver-relay-asymmetric-auth.md`)
so there's no shared relay secret to steal; until then, per-device relay creds +
short Convex-validation cache TTL so rotation/revocation takes effect fast.

## Verified OK (defenses confirmed this session)

- **V-1. Broker device-code handle is tight.** The value injected into
  cloud-init/user_data is only a **handle** (never the token), with a **15-min
  TTL** (`DEVICE_CODE_TTL_MS`) + **single-use** (`pollDeviceCode` clears
  `pendingToken` on first read) + **same-user-only** (broker gated on the
  caller's session; box bound to the SAME userId). A leaked cloud-init handle is
  near-worthless.
- **V-2. No secrets in the code path** → the public repo doesn't weaken any of
  this. AS/relay/owner private keys live in Convex-env / GH-secrets / on-device,
  never in source.
- **V-3. Relay hardening present** — constant-time password compare
  (`subtle.ConstantTimeCompare`), Convex-authoritative validation, device-sig
  path, abuse guard (trusted-proxy IP gating, brute-force throttle, per-user
  stream cap) — the Bucket-B DoS work.
- **V-4. Provision is wallet-gated** (`cloudLifecycle.canStart` prepaid floor) and
  binds the box to the caller's userId — a new/other user can only provision into
  their OWN account, and a $0 account can't spin a fleet.

## Must-hold invariants (don't regress)

1. Never let a **machine-scope** token call account-level/destructive endpoints
   (spend, provision, act on OTHER devices) — Phase 1.
2. Relay stays **never an open proxy**; peer-egress default-off, same-user,
   RFC1918-blocked (CLAUDE.md hard rule) — a rooted tenant box can't pivot to a
   third party or another tenant.
3. Relay routes a signed request **only** to the deviceId in the signature, owned
   by the authenticated user — no cross-tenant routing.
4. Paid provision + decommission should require the **owner key** (Phase 4), so a
   stolen session alone can't drain the wallet.

See `machine-asymmetric-auth-design.md` for the full asymmetric model + phased,
flag-gated, tested rollout.
