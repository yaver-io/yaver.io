# Yaver Gateway — First-Slice Spec (Auth Broker validation)

> Status: spec, design-only (2026-06-17). Parent: `docs/yaver-personal-agent-gateway.md`.
> Goal of this slice: prove the **Auth Broker** end-to-end with **two read-only connectors** —
> one OAuth (no UI automation), one redroid+TOTP (device-as-2FA) — at **zero ACT risk**.
> Everything here is generic/open; connectors + creds are private (vault).

## 0. Scope

- **In:** connector registry (minimal), Auth Broker with 2 method handlers, vault credential
  schema, the resumable human-gate primitive, capability invoke, MCP exposure of 2 GET tools.
- **Out (deferred):** any ACT/write, the full NL intent router (use a direct dispatcher),
  self-improvement curator (leave hooks only), multi-step plans.
- **Acceptance:** from a host AI (or `ops` verb) you can call two tools — one OAuth-backed, one
  redroid+TOTP-backed — and get a **structured** answer; tokens refresh automatically; the
  redroid first-login pauses for a human gate then never again (golden snapshot).

## 1. Shared spine (minimal versions)

### 1.1 Connector registry
- Manifest = JSON on local disk (`~/.yaver/connectors/<id>.json`), **never** Convex. References
  creds by vault key, never inline.
- Registry loads manifests at startup + on change; exposes `Get(id)`, `List()`,
  `CapabilitiesForMCP()`.

### 1.2 Auth Broker
```
type Session struct { Kind; Token string; Expiry time.Time; StorageStateRef string; DeviceID string }
type AuthMethod interface {
  Ensure(ctx, connector) (Session, error)   // returns a valid session, refreshing/acquiring as needed
  NeedsHuman(connector) bool                 // true if a human gate may fire
}
```
Two handlers in this slice:
- `oauthCodeHandler` — standard OAuth2 **authorization code + PKCE**; auto-refresh.
- `passwordTotpHandler` — redroid/web login + **TOTP auto-fill**; persists via golden snapshot.
Broker picks the handler from `connector.engine`/`connector.auth.method`.

### 1.3 Vault credential schema (project = `gateway`)
```
gateway/<connectorId>/oauth        = { access_token, refresh_token, expiry, scope, token_url, client_id }
gateway/<connectorId>/totp_seed    = base32 seed              (if Yaver holds the seed)
gateway/<connectorId>/storageState = playwright storageState  (web)
gateway/<connectorId>/redroid      = { instanceId, snapshotId } (mobile golden snapshot ref)
```
All encrypted (NaCl secretbox + Argon2id, auth-token-derived key — existing `vault.go`).

### 1.4 Resumable human gate
```
PendingGate { id, connectorId, reason, screenshotRef?, prompt, options?, createdAt, expiresAt, status }
```
- Flow calls `awaitHuman(prompt, screenshot)` → writes a `PendingGate`, **notifies the user's
  real phone/voice** (reuse `notify` / `device_broadcast_command` / push), suspends.
- User responds via `POST /gateway/gate/<id>/resolve {answer}` (or voice) → flow resumes.
- Timeout → flow aborts cleanly, records a finding. **Never** auto-satisfies a human factor.

### 1.5 Capability invoke (the dispatcher — stands in for the router this slice)
```
gatewayInvoke(connectorId, capabilityId, params) ->
  manifest := registry.Get(connectorId)
  session  := broker.Ensure(ctx, manifest)        // may fire a human gate
  raw      := engine.run(manifest, capability, params, session)   // api | chromedp | redroid
  answer   := extract(raw, capability.answerSchema)               // LLM/structured extraction
  return answer (validated against answerSchema)
```

### 1.6 MCP exposure (personalized MCP, minimal)
- Slice 1: a single dispatcher tool `gateway_query {connector, capability, params}` (simplest).
- Slice 1b (stretch): dynamically register one MCP tool **per capability**
  (`gw_<connector>_<capability>`) from the registry — the real "your apps as tools" shape.

## 2. Connector A — OAuth (no UI automation)

**Reference instantiation:** Google (you have a Google account — `…@gmail.com` — so it's
testable). The pattern is generic for any OAuth2 service (Tesla Fleet for EV, Microsoft, etc.).

### Manifest
```json
{
  "id": "google",
  "engine": "api",
  "surface": "https://www.googleapis.com",
  "auth": { "method": "oauth_code",
            "authUrl": "https://accounts.google.com/o/oauth2/v2/auth",
            "tokenUrl": "https://oauth2.googleapis.com/token",
            "scopes": ["https://www.googleapis.com/auth/calendar.readonly"],
            "credRef": "gateway/google/oauth" },
  "capabilities": [
    { "id": "next_event", "verb": "get", "risk": "read",
      "flow": { "type": "api", "method": "GET",
                "path": "/calendar/v3/calendars/primary/events?maxResults=1&singleEvents=true&orderBy=startTime&timeMin={now}" },
      "answerSchema": { "title": "string", "start": "datetime", "location": "string?" } }
  ]
}
```

### Auth flow
1. First run: OAuth **authorization code + PKCE**. Reuse existing OAuth plumbing
   (`auth_oauth_setup/save`, `git_oauth_start` device-grant pattern). Open consent in a browser
   (or device-grant for headless) → exchange code → store `{access,refresh,expiry}` in vault.
2. Every call: `broker.Ensure` checks expiry; refresh with the refresh token if stale; no human.
3. Re-consent only if the refresh token is revoked → human gate.

### What it proves
Token/refresh/vault lifecycle, the cleanest rung of the ladder, zero UI automation, zero ACT.

### Test plan
`gateway_query {connector:"google", capability:"next_event"}` → returns `{title,start,location}`.
Revoke access in Google account settings → next call fires a re-consent human gate.

## 3. Connector B — redroid + password + TOTP (no-API app, device-as-2FA)

**Reference instantiation:** any service you use that is **app/web-only with password + TOTP and
no public API**. (Pick one without a passkey requirement.) The point is to prove the
device-as-2FA model, not the specific app.

### Manifest
```json
{
  "id": "example-app",
  "engine": "redroid",
  "surface": "com.example.app",
  "auth": { "method": "password_totp",
            "loginFlowRef": "example-app/login",
            "totpRef": "gateway/example-app/totp_seed",
            "deviceRef": "gateway/example-app/redroid" },
  "capabilities": [
    { "id": "status", "verb": "get", "risk": "read",
      "flow": { "type": "redroid", "flowRef": "example-app/read_status" },
      "answerSchema": { "value": "string", "updatedAt": "datetime?" } }
  ]
}
```

### Auth flow (the interesting one)
1. **First login (one-time, human gate):**
   - redroid instance launches the app (`droid_launch`), automation types username/password
     (from vault), reaches the TOTP prompt.
   - **TOTP auto-fill:** Yaver generates the 6-digit code from the vault seed (reuse the TOTP
     code-gen behind `totp_*`) and types it — *you own the seed, this is your authenticator*.
   - If the service also pushes an approval or device-trust step only you can do →
     `awaitHuman("approve the login on your phone", screenshot)` → you tap → resume.
   - On success, **snapshot the logged-in redroid** (golden-snapshot / `yaver-base` pattern);
     store `{instanceId, snapshotId}` in vault. The device is now a *remembered, trusted device*.
2. **Subsequent calls:** restore the snapshot (already logged in) → run the read flow → extract.
   No login, no TOTP, no human — unless the session/device-trust expires (→ re-login, gate as above).

### Self-healing hook (leave wired, don't build the curator yet)
`example-app/read_status` selectors are stored with fallbacks; on extraction failure call
`testkit_self_heal_selector` to re-derive, persist the fix, record a reliability event.

### What it proves
The device-as-2FA model, TOTP auto-fill from a seed you own, the golden-snapshot "trusted
device" persistence, and the human-gate primitive — the heart of the Auth Broker.

### Test plan
`gateway_query {connector:"example-app", capability:"status"}` → first call pauses at the human
gate (if the app pushes one), you approve, it returns `{value,updatedAt}`; second call returns
instantly with no human step.

## 4. Data models (summary)
- `Connector` manifest (§2/§3), `Capability`, `Session` (§1.2), `PendingGate` (§1.4), vault
  credential keys (§1.3). All generic/open; instances + creds private.

## 5. Suggested file layout (`desktop/agent/`)
```
gateway_registry.go     // load/store/list manifests
gateway_broker.go       // AuthMethod interface + Ensure dispatch
gateway_oauth.go        // oauthCodeHandler (reuse x/oauth2 + existing oauth plumbing)
gateway_redroid.go      // passwordTotpHandler (droid_* + TOTP code-gen + golden snapshot)
gateway_gate.go         // PendingGate store + notify + /gateway/gate/* routes + resume
gateway_invoke.go       // gatewayInvoke + answerSchema extraction
gateway_mcp.go          // gateway_query tool (+ optional dynamic per-capability tools)
gateway_test.go         // scoped TestGateway* (run with -run TestGateway, NOT full ./...)
```

## 6. Milestones
| ID | Milestone | Proves |
|---|---|---|
| M-G1 | Registry + manifest load + vault cred schema | storage |
| M-G2 | `oauthCodeHandler` + Connector A end-to-end | OAuth/refresh lifecycle |
| M-G3 | Human-gate primitive (PendingGate + notify + resume) | human-in-loop |
| M-G4 | `passwordTotpHandler` + redroid login + TOTP auto-fill + golden snapshot | device-as-2FA |
| M-G5 | `gatewayInvoke` + answerSchema extraction + Connector B end-to-end | the read loop |
| M-G6 | MCP exposure (`gateway_query`, then dynamic per-capability) | personalized MCP |

## 7. Acceptance criteria
- Two GET tools callable; both return data validated against their `answerSchema`.
- OAuth token auto-refreshes without a human; revocation triggers a clean re-consent gate.
- redroid first-login fires at most one human gate, then golden-snapshot makes subsequent calls
  human-free.
- No write/ACT path exists in this slice. Nothing in Convex. Creds only in vault.
- Honest guardrails enforced: no captcha/bot-detection evasion; back off + record on a block;
  passkey-protected services rejected with "use official API/manual"; bank-type services
  steered to Open Banking, not redroid scraping.

## 8. Explicitly out of scope for the slice
NL intent router intelligence, ACT consent/dry-run, two-key, spend caps, the self-improvement
curator (hooks only), proactive monitoring, multi-step/conditional plans. All specced in the
parent doc (§14–18) for the following slices.

---

# Next slices (queued — fire after M-G1+M-G2 lands, on the established interfaces)

> These share the gateway Go package with the foundation, so they are **sequenced, not
> parallelized** (concurrent edits to `gateway_broker.go`/types = conflicts). Verified primitives:
> `/rd/stream`+`/rd/input` (`remotedesktop_http.go`), `droid_frame`/`droid_input`
> (`droid_interactive.go`), `device_broadcast_command` (`mcp_device_broadcast.go`), TOTP secret
> machinery (`two_factor_cmd.go`).

## M-G3 — Human-gate primitive (incl. interactive remote-view captcha + authenticator routing)
New file `gateway_gate.go`:
```
GateKind = "simple_confirm" | "enter_code" | "interactive" | "approve_push"
PendingGate { id, connectorId, kind GateKind, prompt, screenshotRef?, viewRef?, options?, createdAt, expiresAt, status }
awaitHuman(ctx, GateRequest) (Resolution, error)   // writes PendingGate, delivers, BLOCKS until resolve/timeout
```
- **Delivery to the user's own phone:** reuse `device_broadcast_command` to push a notification +
  deep-link. **No assumption of physical access to the remote device.**
- **Interactive kind (captcha / push-approval):** `viewRef` points at a live remote-view session —
  web → `/rd/stream`+`/rd/input`; redroid → `droid_frame`+`droid_input`. The user solves the
  challenge themselves in the live window; flow resumes on completion. (The machine never solves
  the captcha — the account owner does. See parent §19.1.)
- **Routes:** `GET /gateway/gate` (list), `POST /gateway/gate/<id>/resolve {answer}`.
- **Timeout:** abort flow cleanly, record a finding. Never auto-satisfy a human factor.
- **Test:** in-memory gate store; assert deliver→block→resolve and timeout paths (no real device).

## M-G4 — redroid `password_totp` AuthMethod handler (device-as-2FA)
New file `gateway_redroid.go` — implements the `AuthMethod` interface from the foundation:
```
Ensure(ctx, connector):
  if goldenSnapshot(connector) valid → restore, return Session
  else login:
    droid_launch(app) → type user/pass (from vault)
    at 2FA prompt, route by connector.auth.mechanism:
      "totp_seed"        → generate code from vault seed (reuse two_factor TOTP gen) → type   // fully remote, no human
      "authenticator_app"→ read rotating code off the on-device app (droid_frame + ui_texts)  // remote
      "push_to_app"      → awaitHuman(kind="approve_push", interactive remote-view)            // remote tap
      "sms"              → read OTP from redroid's own inbox OR awaitHuman                      // sourced
      "passkey"          → fail: non-relayable → official API / manual
    on success → snapshot logged-in instance (golden) → store {instanceId, snapshotId} in vault
```
- **Device interaction behind an interface** (`deviceDriver` with `Launch/Type/Frame/Tap`) so the
  handler is unit-testable without a real redroid (DI test double).
- **TOTP correctness test:** RFC 6238 test vectors against the reused gen — deterministic, no device.
- **Push-only test:** asserts it routes to a human-gate (M-G3), never auto-approves.
- Policy Guard: on a block/anti-automation signal → back off + record, never evade.

## Launch plan
On M-G1+M-G2 green: fire **M-G3 and M-G4 together** (different files, both depend only on the
foundation interfaces — mutually non-conflicting). Then M-G5b (AI answer-extraction) + M-G6
(dynamic per-capability MCP tools). ACT consent model (parent §16) is the slice after.
