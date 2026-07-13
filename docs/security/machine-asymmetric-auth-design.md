# Machine auth — asymmetric, scoped, revocable (design + impl spec)

Status: design (2026-07-13). Grounded in existing prior art:
`desktop/agent/device_sign_key.go` (ed25519), `docs/yaver-relay-asymmetric-auth.md`,
`backend/convex/provisioning.ts` (provision-attest), `POST /relay/resolve-sig`,
`docs/yaver-app-attestation.md`. Extends the arbitrage-resale threat model
(`project_arbitrage_resale_security`) to the **token layer**.

## 1. The problem (found live 2026-07-13)

A managed cloud box is born-authenticated (broker device-code) with a row in
`sessions {tokenHash, userId, deviceId, expiresAt: +365d}`. `sessions` has **no
scope** — so the box holds a **full-owner bearer token for a year**, in
`/root/.yaver/config.json`.

The box is customer/attacker-**rootable**. Extract that token → full account
access: delete the owner's OTHER machines, drain the prepaid wallet, provision
boxes on their dime, enumerate devices. A single bearer secret = the whole
account. The public repo doesn't cause this — the secret is on the box — but the
public repo means **security cannot rely on code secrecy**; it must rely on keys
and scope.

What is ALREADY safe (verified): the device-code *handle* injected into
cloud-init is 15-min TTL + single-use + same-user-only, and only the handle
(never the token) is in user_data. The hole is purely the **standing token**
the box keeps after redemption.

## 2. Thesis — replace the standing bearer with a signed, scoped grant

Three principles, each already partly present in Yaver:

1. **The box proves identity by SIGNING, not by presenting a bearer.** It holds
   an ed25519 **device private key** (already: `device_sign_key.go`,
   generated on-box, `0600`, never transmitted). Its PUBLIC key is published to
   Convex, bound to `deviceId`+`userId`. Every request carries a signature over
   `(method, path, body-hash, deviceId, timestamp, nonce)`; the backend verifies
   against the published public key. Same shape the relay design already uses
   (`X-Yaver-Signature`, `/relay/resolve-sig`).
   - *Rooted box caveat, stated honestly:* a file-based private key IS
     extractable from a rooted box. Signing alone doesn't stop that. Scope (#2)
     + revocation (#3) is what bounds the damage. For hardware-backed
     non-extractability we'd need a vTPM/Nitro-enclave (future; note in §7).

2. **The box's authority is MACHINE-SCOPED.** Its credential (whether a signed
   request or a short AS-JWT) carries `scope: "machine"`. The backend
   **default-denies** account-level ops for machine scope: it may heartbeat,
   report its own phase, pull its OWN git creds, run its OWN tasks — and NOTHING
   that touches other devices, the wallet, provisioning, or account settings. A
   rooted box can only hurt itself; the control plane reaps it.

3. **Everything is per-device REVOCABLE.** Revoke the box's public key (one
   Convex row) → the box is locked out instantly, every other device unaffected.
   No shared secret to rotate platform-wide.

## 3. The three key-holders (who signs what)

| Holder | Key lives | Signs | Verified by |
|---|---|---|---|
| **Yaver AS** (authorization server) | Convex env / GH secret — **never in the repo** | Short-lived scoped grants ("deviceId D is a machine-scope agent for user U until T"), JWKS-published EdDSA | Backend + relay via the AS **public** JWKS |
| **Owner** | Owner's trusted device / passkey (WebAuthn) or a device ed25519 key | The *authorization to create a machine* (owner consent), and high-value ops (decommission, spend) | Backend via owner's published public key |
| **Box (agent)** | On-box ed25519 (`device_sign_key.go`), `0600` | Its own per-request signatures / self-issued short JWT | Backend/relay via Convex-published device pubkey |

Key property for a **public repo**: the only private keys are (a) the AS key in
Convex-env/GH-secrets, (b) owner keys on owner-trusted surfaces, (c) box keys on
the box. None are in code. Verification everywhere uses **public** keys. Leaking
the source reveals the algorithm, not any authority.

## 4. Use case A — a new machine is "born authenticated," securely

Today: broker mints a full-owner session, injects a 15-min handle. Keep the
zero-friction UX, change what the box ends up holding:

1. Web/MCP (already-authenticated owner) calls provision. Backend, gated on the
   owner session, mints an **owner-consent grant**: AS signs
   `{deviceId, userId, scope:"machine", exp:+15m, purpose:"provision"}`.
   (Optionally co-signed by the owner key for high-value/paid provisions.)
2. Only the 15-min grant (a signed JWT, not a token) goes into cloud-init.
3. On first boot the box: generates its ed25519 device key, publishes the pubkey
   to Convex **presenting the grant** (proves it's the authorized deviceId), and
   from then on **signs its own requests**. The grant is single-use + expires;
   the box never holds a standing owner bearer.
4. The box's ongoing credential = its device key (machine-scoped). A leaked
   cloud-init grant is worthless after 15 min / first use, exactly as today —
   but now there's ALSO no full-owner token to steal afterward.

## 5. Use case B — resource ops from the box

The box needs to: heartbeat, report phase, pull its own git creds, run tasks,
serve the owner's inbound connections. Each is a **machine-scope** op:

- Box → Convex/relay: request signed with device key; backend verifies sig +
  checks `scope==machine` + checks the op is in the machine-self allowlist.
- Git-cred autohydrate stays device→device (pull from an owner device), but the
  *pulling* box authenticates by signature, and the source device authorizes the
  pull because the requester's pubkey is owned by the same user (Convex resolves
  ownership, as `/relay/resolve-sig` already does).
- Owner-only / destructive ops (decommission THIS box, or ANY box; wallet spend;
  provision) require the **owner** signature or a full-scope session — a machine
  token is default-denied.

## 6. Implementation plan (bounded, testable, backward-compatible)

Do it in safe increments; each is shippable and reversible. **This is auth for
every user — land behind a flag, test, then enforce.**

**Phase 0 — scope field (no behavior change).** Add `scope?: "full" | "machine"`
to `sessions` (undefined ⇒ "full", so existing tokens are unaffected). Broker
path (`createAuthorizedDeviceCodeForUser`) sets `scope:"machine"` for boxes.
Surface `scope` from `validateSessionInternal` / `authenticateRequest`. **Enforce
nothing yet** — just observe.

**Phase 1 — default-deny the crown jewels for machine scope.** On the highest-
value endpoints only — wallet/billing spend, provision, stop/start of a machine
the caller doesn't *equal*, device removal of OTHER devices, account settings —
reject `scope=="machine"` with 403. Small, high-value, low-risk (a box never
legitimately calls these). Ship + watch logs.

**Phase 2 — machine-self allowlist (default-deny everything else).** Flip
machine scope to allow ONLY an explicit allowlist (heartbeat, /machine/phase,
own git-cred pull, own tasks, own dev-server). Requires enumerating the box's
real call set first (instrument in Phase 0/1). This is the big correctness step
— do it with the call-set data, not by guessing.

**Phase 3 — device-key signatures (the asymmetric layer).** Box publishes its
ed25519 pubkey via the provision grant; requests carry `X-Yaver-Signature`
(reuse the relay design's canonical string + ±window + nonce replay cache).
Backend verifies sig → resolves deviceId→pubkey→userId. Now the box's authority
is a *revocable key*, not a stored bearer. Bearer stays valid in parallel
(backward-compatible) until sig coverage is proven, then the box stops persisting
a full bearer at all.

**Phase 4 — revocation + short-TTL + rotation.** Per-device pubkey revocation
(decommission revokes instantly). Box grant/JWT TTL 30–90d with silent
device-key-signed refresh (no standing 1y bearer). Owner co-sign required for
paid provision + decommission.

## 7. Honest limits

- File-based box keys are extractable from a rooted box. Scope + revocation bound
  the blast radius to *that box*, which is the goal; true non-extractability
  needs a vTPM / cloud enclave (Nitro, SEV-SNP) — a later hardware step, tracked
  separately.
- Don't over-rotate or you DoS your own boxes; refresh must be signed + automatic.
- Every phase is an auth change → land behind a flag, test against a throwaway
  box AND a normal login, and watch logs before enforcing.

## 9. New-user & owner machine creation (edge cases)

- **First-time / new user, first machine.** They have no primary device and no
  git creds anywhere, so autohydrate has nothing to pull — that's fine, git is
  simply unlinked until they connect a provider (the box comes up Yaver-authed +
  empty-git, not broken). Provision is still gated on the **prepaid balance
  floor** (`cloudLifecycle.canStart`), so a $0 account can't spin infinite boxes.
  The broker still binds the box to *their* userId — a brand-new user can only
  ever provision into their own account.
- **Owner creating additional machines.** Same broker path; each box gets its
  OWN device key + machine-scope grant. No box can act on another box (Phase
  1–2). One rooted box ≠ fleet compromise.
- **Abuse gate.** Provision must remain wallet-gated + rate-limited per user
  (prevent a compromised session minting a fleet). Owner co-sign (Phase 4) makes
  paid provision require the owner key, not just a session — so a stolen
  *session* alone can't drain the wallet by provisioning.
- **Free-tier at scale.** With 100k free users each able to provision, the
  balance floor + per-user provision rate limit + machine-scope (a free box
  can't provision more boxes) are the guardrails. Never let a machine-scope
  token call provision (Phase 1).

## 10. Public relay (`public.yaver.io`) security — current state + gaps

The shared relay is multi-tenant; its job is to pass QUIC through, never store
task data. Current mechanisms (verified in code):

- **Per-user password** (`X-Relay-Password`, from `userSettings.relayPassword`),
  compared **constant-time** (`subtle.ConstantTimeCompare`, `relay/server.go`)
  and validated against Convex per-user (`validatePasswordViaConvex`,
  `/relay/validate`).
- **Device-signature path** (`relay/sigauth.go`) — the asymmetric model already
  landed fail-open: relay verifies a device signature, routes only to the signed
  `deviceId`, constant-time. This is the future-proof auth (no shared secret).
- **Abuse guard** (`relay/abuse_guard.go`) — trusted-proxy IP gating
  (RFC1918/Cloudflare only), brute-force throttle, per-user stream cap,
  Referrer-Policy (the Bucket-B DoS hardening).

**Gaps / must-hold invariants:**
1. The per-user **password is a shared secret** — a rooted box leaks the owner's
   relay password → an attacker can impersonate the owner's devices ON the relay
   until rotated. **Migrate the relay to device-signature-only** (§2/§3;
   `yaver-relay-asymmetric-auth.md`) so there's no shared relay secret to steal.
   Until then: per-device relay creds, not one per-user password.
2. **Anti-pivot / peer-egress** must stay default-off, same-user, RFC1918-blocked
   — the relay is never an open proxy (CLAUDE.md hard rule). A rooted tenant box
   must not be able to reach a third party or another tenant through the relay.
3. **Routing isolation:** the relay must route a signed request ONLY to the
   deviceId in the signature, owned by the authenticated user — never
   cross-tenant. (`resolve-sig` + Convex ownership check; keep it mandatory, not
   fail-open, once device-sig coverage is proven.)
4. **Password validation must be Convex-authoritative + cached with a short TTL**
   so a revoked/rotated password takes effect fast (revocation latency = attack
   window after a box is rooted).

## 8. Why this is safe in a public repo

No phase embeds a secret in code. AS private key → Convex env / GH secret. Owner
keys → owner devices / passkeys. Box keys → the box. Verification is all public-
key. Leaking the repo reveals the *design* (which is fine — it's the standard
asymmetric model) and zero authority.
