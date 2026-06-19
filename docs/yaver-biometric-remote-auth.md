# Yaver Biometric Remote Auth — Deep Analysis (Face ID / Touch ID)

> Status: **design analysis, 2026-06-20.** Grounded in current code
> (`backend/convex/passkeys*.ts`, `deviceCode.ts`, `http.ts`;
> `desktop/agent/devicecode.go`, `sdk_token.go`, `auth.go`, `vault_keychain.go`;
> `mobile/src/lib/passkey.ts`, `web/app/auth/page.tsx`). Code is the source of
> truth per `CLAUDE.md`. Verdict is deliberately skeptical about effort/value.

## The friction we're trying to kill (without weakening security)

Authing a **remote** machine today (`ubuntu-4gb-hel1-1`, `magara`, a fresh
Hetzner box) means one of:

1. **SSH in + run `yaver auth --headless`** → it prints a `ABCD-1234` code + a
   `https://yaver.io/auth/device?code=…` URL → you **open the URL in a browser
   where you're already signed in** and approve (`devicecode.go:191-233`,
   `deviceCode.ts:159-208`). Works, but: you SSH'd, you context-switched to a
   browser, you pasted/clicked.
2. For CI/automation we went further and put an **account password** in
   `.env.test` + GitHub secrets. That's the *least* secure thing in the whole
   system: a long-lived bearer secret, copyable, that grants full account
   access. (It's also now in a chat transcript.)

Both are the wrong primitive for "frictionless but secure." The good news from
the code audit: **~80% of the secure version already exists.** The missing 20%
is a phone-push approval channel and swapping passwords for scoped tokens.

## What already exists (reuse, don't rebuild)

| Primitive | State | Where |
|---|---|---|
| **Passkeys / WebAuthn** — register + passwordless login, discoverable creds | ✅ complete | backend `passkeys.ts`, `passkeysDb.ts` (5-min challenge TTL); HTTP `/auth/passkey/*` (`http.ts:595-862`); web `@simplewebauthn/browser` (`web/app/auth/page.tsx:6,317`); **mobile `react-native-passkey`, RP_ID `yaver.io`** (`mobile/src/lib/passkey.ts:24-215`) |
| Account already has a passkey enrolled | ✅ | observed: `lookupExistingProvidersByEmail` → `hasPasskey:true` |
| **Device-code flow** (`yaver auth --headless`) | ✅ complete | `deviceCode.ts:28-208` (code 15-min TTL, session 1-yr, one-time token), HTTP `/auth/device-code{,/poll,/info,/authorize}` (`http.ts:3548-3645`), agent `devicecode.go:164-240` |
| **Scoped SDK tokens** (scopes, CIDR allowlist, TTL, guest delegation) | ✅ create/validate | `sdk_token.go:29-76`, `auth.go:221-275` |
| **macOS Keychain / Touch ID for the vault key** | ✅ partial | `vault_keychain.go:8-290` — master key mirrored to Keychain; Touch ID gate is a *macOS system setting*, not enforced by Yaver |
| `expo-local-authentication` (Face ID / Touch ID API) | ✅ available, **unused for auth** | present in `mobile/node_modules` |

**Key insight:** passkeys mean Face ID / Touch ID *already* gate human sign-in —
the native passkey sheet triggers the biometric as part of the WebAuthn
ceremony. The piece that's missing is using that same trust to **approve a
*different* (headless/remote) machine from your phone**.

## What's missing (the 20%)

1. **No push channel.** No APNs/FCM token storage, no "pending approval" table,
   no `POST /auth/device-code/push-request`. The remote box can only show a URL;
   it can't ring your phone.
2. **No approve-from-app surface.** Mobile has approve/deny UIs for *gateway
   gates / support / guests* (`mobile/app/gateway-gates.tsx:157`) but nothing
   that calls `/auth/device-code/authorize`. Approval still requires a browser.
3. **No biometric gate on the approval action.** `expo-local-authentication`
   is sitting unused; an approval should require a fresh Face ID, not just an
   open app session.
4. **No SDK-token revocation API** (`auth.go` SDK section). Tokens live to TTL.
   Required before we lean on them for automation.

## Target UX

**A. Coding on the Mac → Touch ID.** Already real via passkey login: the web
dashboard and any localhost WebAuthn ceremony prompt Touch ID (Secure Enclave),
no password. For the *CLI*, add a tiny localhost WebAuthn helper (open
`127.0.0.1` page → `navigator.credentials.get()` → Touch ID → assertion → token)
so `yaver auth` on your own Mac is a Touch ID tap, never a keychain-password
nag. This is the systematic version of "don't ask keychain if it's me": the
biometric is the gate, and only for a *human* sign-in, never for the agent's
own automated calls.

**B. Remote box / iPhone Face ID → approve "all things".** The flagship flow,
and it reuses the entire device-code backend:

```
remote box: yaver auth            (no SSH needed if initiated from dashboard)
   └─ creates device-code (exists)
   └─ NEW: POST /auth/device-code/push-request  → APNs push to your iPhone
iPhone:  "Approve sign-in for ubuntu-4gb-hel1-1 (Hetzner, fra)?  [Approve]"
   └─ NEW: Face ID via expo-local-authentication  (fails closed)
   └─ calls EXISTING POST /auth/device-code/authorize (your signed-in session)
remote box: poll returns token (exists) → writes ~/.yaver/config.json
```

Same for runner OAuth and any future "approve X": one generic
"approval request → push → Face ID → authorize" rail, parameterized by what's
being approved.

## Security analysis (why this is *stronger* than today)

- **Phishing-resistant.** Passkeys are origin-bound (RP ID `yaver.io`,
  `passkeys.ts:24-50`); a fake page can't replay them. The account password
  can be phished/leaked; the passkey can't.
- **Possession + biometric.** Approval needs your enrolled iPhone *and* a live
  Face ID — two factors, no shared secret on the wire.
- **Least privilege + revocable.** Remote boxes should receive a **scoped SDK
  token** (`feedback,builds,…` only, optional CIDR pin), not a full 1-yr
  session — once revocation exists, a lost box is one click to cut off.
- **Threats to design against (don't hand-wave):**
  - *Approval phishing / "MFA fatigue":* show **what** is being approved (box
    name, geo/IP, time) and **rate-limit + auto-expire** requests; never a bare
    "Approve?" Bind the push to a specific device-code, single-use.
  - *Push spoofing:* the push only *notifies*; authority comes from the
    app calling `/authorize` with your real session — backend already gates on
    `authenticateRequest` (`http.ts:606`).
  - *Stolen unlocked phone:* `disableDeviceFallback:true` on the biometric so a
    passcode can't substitute; require re-auth per approval.
  - *Token sprawl:* the **missing revocation API** is a prerequisite, plus a
    per-token last-used audit.

## Immediate action (don't wait for the 4–6 week build)

The single biggest security win is cheap and available **now**: stop using the
**account password** for CI/headless and switch to a **scoped, revocable SDK
token** (`yaver sdk-token create --scopes … --expires 30d`), stored in the
`integration` GitHub Environment instead of `YAVER_TEST_PASSWORD`. The password
on the real account can then be removed (or kept only as a human break-glass).
Gap to close first: add the SDK-token **revoke** endpoint.

## Roadmap

- **P0 (now, hours):** swap CI password → scoped SDK token; add SDK-token
  revoke + last-used audit (`auth.go`/`sdk_token.go`).
- **P1 (small):** mobile `approve-device-auth` screen calling the existing
  `/auth/device-code/authorize`, gated by `expo-local-authentication`. Removes
  the browser hop even before push lands (open app → approve with Face ID).
- **P2 (medium):** APNs/FCM token registry + `push-request` endpoint + mobile
  notification listener → true "ring my phone" approval.
- **P3:** localhost WebAuthn helper for `yaver auth` on Mac (Touch ID for CLI);
  generalize the approval rail to runner OAuth and other sensitive actions.

**Verdict:** the vision is sound and mostly *assembly*, not invention — the
passkey ceremony and device-code rails already exist and need **zero** changes.
Real new work is the push channel + a mobile approval screen + a biometric gate,
plus token revocation. P0+P1 deliver most of the "frictionless but secure" value
in a fraction of the effort; P2 is the polish that makes it feel magic.
