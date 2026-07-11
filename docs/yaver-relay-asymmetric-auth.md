# Yaver relay — asymmetric-key auth (design)

Status: design (2026-07-11). No code yet. Grounded in the relay security audit
(same date) and a read of the current relay + agent + AS code. Verify constants
against source before implementing (CLAUDE.md "Read This First").

## 1. Problem — the shared password is the root of several findings

Today a client authenticates to the relay with a **symmetric secret**: an
account-wide relay password, sent as `X-Relay-Password` or `?__rp=`, validated
locally (self-hosted shared password) or per-user via Convex
(`relay/server.go::validateRelayAccess`, `bus.go`). Symmetric means: every
client embeds the secret, and the relay must **know** it to check it. That is
the source of a cluster of the audit findings:

| Audit finding | Root cause |
|---|---|
| #3 `?__rp=<password>` leaks via logs + `Referer` | the credential is a bearer string that must ride the URL for header-less clients (iframes/EventSource) |
| #7 prefix-match routing (authorized id ≠ routed id) | the password authorizes an *account*, not a *specific request/device* |
| #10 relay not authenticated to client (`InsecureSkipVerify`) | on-path attacker can capture the shared password at register time |
| operational: one leak → rotate for **everyone** | there is one shared secret, not per-device credentials |

(Findings #1/#4/#6 — IP-spoof rate limit, brute-force throttle, stream
exhaustion — are DoS/resource issues, already fixed in `a501627ec`; they are
orthogonal to auth and NOT addressed here.)

## 2. Thesis — the relay should hold only PUBLIC keys

**Asymmetric flips the trust:** the relay *verifies* signatures/tokens against
**public** keys; it stores nothing an attacker can steal to impersonate anyone.
Breaching the relay yields zero credentials. This is the same principle as the
money-token design (`project_arbitrage_resale_security`, `4a27422b5` — "secret
never on an attacker-reachable wire") applied to *client* auth. It directly
answers the standing requirement: the relay must never hold a reusable secret,
and a hacker who owns the (open-source, self-hostable) relay must gain nothing.

## 3. Primitives Yaver already has (reuse, don't reinvent)

- **Device keypairs** — the `(deviceId, hardwareId, publicKey)` identity triple,
  `dk.PublicKeyBase64()`, registered in Convex device rows
  (`desktop/agent/auth_bootstrap.go:713-819`; `devices.publicKey` in schema).
  Currently curve25519 for pairing encryption; add an **ed25519 signing** key
  per device (or derive one).
- **OAuth AS with RS256 + JWKS** — `desktop/agent/oauth_provider.go` mints RS256
  JWTs; the AS advertises `jwks_uri: <issuer>/oauth/jwks`. This is exactly what
  the `/mcp` connector already verifies (`oauth_mcp.go`). The relay can verify
  AS-minted JWTs against the JWKS — no shared secret.
- **Relay ↔ Convex** — the relay already calls Convex per-user
  (`validateAndResolveViaConvex`); swapping "validate password" for "fetch this
  device's public key" / "verify this signature's deviceId is owned by userX" is
  the same call shape. It already **refuses open mode** (`main.go` `--allow-open`).

## 4. The design — two credential models, chosen per surface

Both converge at the relay to one operation: **verify a signature/token against a
public key.** No password at rest, on either side.

| Surface | Model | Rationale |
|---|---|---|
| **Yaver CLI, mobile, Talos CLI** | **Device-key signed requests** — ed25519 private key in Keychain / Android Keystore / `0600` file; each request (or a short self-issued JWT) is signed; relay verifies against the Convex-published public key for that `deviceId` | Can hold a private key that **never leaves the device**. Nothing static to embed or extract. Per-device revocation. No AS round-trip (relay verifies via Convex pubkey). |
| **Web dashboard** | **Short-lived AS-issued JWT** (the `/mcp` connector model) — OR a **WebCrypto non-extractable** device key in IndexedDB | Browsers can't safely hold a long-lived secret. Short-lived RS256 tokens (minutes, `aud=relay`) verified via JWKS keep blast radius tiny. WebCrypto can generate a **non-extractable** keypair (private key exists but JS can't read it) — lets even web do the device-key model. |

### Request shape (device-key model)
```
Authorization: Yaver-Sig v1
X-Yaver-Device: <deviceId>
X-Yaver-Timestamp: <unix-ms>
X-Yaver-Nonce: <random-128-bit>          # optional, for the nonce cache
X-Yaver-Signature: base64(ed25519_sign(privkey,
    "<method>\n<path>\n<deviceId>\n<timestamp>\n<nonce>\n<sha256(body)>"))
```
The relay: resolves the device's public key (Convex, cached), checks the
timestamp is within a ±window, verifies the signature over the canonical string,
and — critically — the `deviceId` in the signed payload is the one it routes to
(closes #7: authorized id == routed id). No password in the URL (closes #3).

## 5. Relay verification chain (backward-compatible)

```
1. Authorization: Yaver-Sig  → verify ed25519 over canonical string vs Convex pubkey
2. Bearer <JWT> (2 dots)     → verify RS256 vs AS JWKS, aud=relay, exp, deviceId claim
3. X-Relay-Password / __rp   → existing password path (self-hosted / legacy)
```
Official Yaver relay prefers 1/2; a hobbyist self-hoster can still run
password-only. Deprecate `?__rp=` on the official relay once clients migrate.

## 6. Replay protection (mandatory)

A signed request is a bearer artifact until it expires — a captured one replays.
- **Timestamp window**: reject if `|now - ts| > 60s`.
- **Nonce cache**: LRU of seen `(deviceId, nonce)` within the window; reject
  repeats. Bounded memory (window × rate).
- For JWTs: short `exp` (≤5 min) + `jti` in the same nonce cache.

## 7. Certificates / mTLS — opt-in, not default

- **Full mTLS (client certs)**: QUIC is TLS, so client certs fit naturally — the
  AS becomes a private CA issuing short-TTL per-device certs; the relay's TLS
  layer validates against the CA. Strongest, and it would also close #10 for
  free (mutual auth). But it adds real ops weight (issuance, rotation,
  revocation/short-TTL) — painful for self-hosters of the free relay.
- **App-layer signatures/JWTs (§4)**: ~90% of the benefit, reuses existing
  RS256 + device keys, trivial for self-hosters.
- **Recommendation**: app-layer first. Offer mTLS as opt-in defense-in-depth for
  high-assurance deployments; never force self-hosters into a CA.
- **#10 without mTLS**: pin the relay's SPKI/public key in the agent config and
  verify it instead of `tunnel.go`'s `InsecureSkipVerify: true` — cheap, closes
  the relay-impersonation gap independently of client auth.

## 8. Per-surface embedding notes

- **Talos CLI** (separate product): authenticates as a Yaver device (its ed25519
  public key registered to the Talos user in Convex) — shares the same verify
  path. Open decision: does Talos share Yaver's AS/Convex identity, or is there a
  cross-trust? (see §11). Either way Talos embeds **no shared secret**.
- **Mobile**: key in iOS Keychain / Android Keystore (hardware-backed where
  available). Reuses the existing device-identity bootstrap.
- **Web**: prefer short-lived AS JWTs; WebCrypto non-extractable keys as an
  upgrade. Never a long-lived secret in `localStorage`.

## 9. What this closes (audit cross-ref)

- **#3** — password out of the URL → no `Referer`/log leak. (Header `Referrer-Policy`
  already shipped as interim in `a501627ec`.)
- **#7** — signature binds the request to a specific `deviceId` → no
  authorized-vs-routed mismatch.
- **#10** — SPKI pinning (§7) closes relay impersonation; mTLS closes it fully.
- **#2 (auto-expose no auth)** — the auto-provisioned `<deviceId>.<domain>` path
  can now require a per-device signature to prove ownership, instead of being an
  unauthenticated door. (This is the one Bucket-A finding that also needs a
  product decision — see §11.)
- **Operational** — per-device revocation replaces global password rotation.

## 10. Migration plan

1. **Agent**: generate/register an ed25519 signing key per device (extend the
   bootstrap that already publishes `publicKey`); add a signing HTTP transport.
2. **Relay**: add verify-chain steps 1 & 2 (§5); keep password fallback. Add the
   nonce cache. Pin relay SPKI (#10).
3. **Backend**: a Convex query "public key for deviceId (owned by userX)" — the
   inverse of the current password validate. JWKS already exists.
4. **Web**: short-lived AS JWT issuance for the dashboard (reuse `/mcp` AS).
5. **Cutover**: official relay prefers sig/JWT, warns on `?__rp=`, then drops it.
   Self-hosters keep password mode.

## 11. Open decisions (need your call)

1. **Primary credential**: device-key signatures (fully decentralized — relay
   verifies via Convex-published pubkey, no AS round-trip) vs everyone gets AS
   JWTs (simpler verify, adds an AS dependency). Lean: **device-sig for
   CLI/mobile/Talos + AS-JWT for web**.
2. **Talos identity**: shares Yaver's AS/Convex identity, or its own product with
   a cross-trust? Changes where Talos's public key lives.
3. **mTLS**: in scope, or app-layer signatures only (recommended)?
4. **#2 auto-expose**: require per-device signature on the subdomain path, or
   stop auto-exposing the agent control port and only expose user-chosen ports?
</content>
