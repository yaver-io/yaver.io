---
doer: codex
---

# Mail: the Gmail/O365 connector is built and cannot complete setup

## Why this exists

Do not build a mail feature. **It already exists** and is substantially real:
`email.go` (Gmail REST + Microsoft Graph, SQLite cache), `mail_fetch.go` (a
second, newer Gmail/Graph client with a normalized `MailMessage`, a
sender-history classifier, and `BuildDraftPrompt`), `ops_mail.go` (`mail_search`,
`mail_unread`, `mail_send` — dry-run by default, `confirm:'send'` to execute),
`mail_learning.go` (on-box allow/deny lists that never leave the machine), and a
23KB mobile inbox at `mobile/app/(tabs)/mail.tsx` reachable from
`more.tsx:3931`. ~3000 lines, zero TODO markers.

The problem is that **a user cannot finish OAuth setup**, and one provider path
reports success while storing nothing. Everything below is a verified defect with
a file:line, not a design proposal.

A prior audit (2026-07-17) established the facts. Trust them over any `*.md`.

## Ground rules

- Do the priorities **in order**. Each is one increment the gate can verify.
- **Gate: `cd desktop/agent && go build ./...`** plus the scoped mail tests:
  `cd desktop/agent && go test -count=1 -run '^(TestMailOpsRegistered|TestMailSendDefaultsToDryRun|TestMailSendExecuteRequiresConfirm)$' .`
- **NEVER run bare `go test ./...` in `desktop/agent`.** `TestAuthLogout` hits the
  real `~/.yaver` and will sign this machine out mid-run. Always pass `-run` with
  an anchored pattern. This has cost real time before.
- Scope is `desktop/agent/**` only. No new dependencies. Do not touch `web/**`,
  `mobile/**`, `tvos/**`, `watch/**`, `wear/**` — surfaces are a follow-up loop
  (see "Deferred" below).
- **Do not add a Yaver-owned OAuth client ID/secret to the repo.** The repo is
  public. The current design is BYO-credentials (the user creates their own
  Google Cloud project / Azure app registration) and that is deliberate — see
  "The constraint" below. Do not "simplify" setup by shipping a shared client.
- Credentials live in `config.json` (0600) today, not the vault. Leave that alone;
  vault v2 is broken on the author's MacBook and a vault dependency would make
  mail unopenable there. Do not "improve" this in the same change.
- Email content must never reach Convex. `convex_privacy_test.go:31` enumerates
  the fence. Mail is local-only today — keep it that way. Do not add a sync path.

## P0 — the redirect URI Google refuses to accept

`handleMailConfig` (`mail_fetch_http.go:267`) returns
`publicOauthBase(r) + "/mail/onboard/callback"` and the mobile wizard tells the
user to paste that into Google Cloud Console. `publicOauthBase`
(`oauth_provider.go:302-311`) builds it from the request `Host` header and
defaults the scheme to `http`. Driven from the phone over LAN that yields
`http://192.168.1.50:18080/mail/onboard/callback`.

Google rejects this twice over. Verified against
`developers.google.com/identity/protocols/oauth2/web-server`:

> "Redirect URIs must use the HTTPS scheme, not plain HTTP. Localhost URIs
> (including localhost IP address URIs) are exempt from this rule."
> "Hosts cannot be raw IP addresses. Localhost IP addresses are exempted."

Plain HTTP **and** a raw IP. The Console rejects the string at paste time, so
setup cannot be completed from a phone at all. Note the doc comment at
`mail_fetch_http.go:260` claims it returns
`https://public.yaver.io/mail/onboard/callback` — that is aspirational and
false; fix the comment too.

The callback must terminate somewhere Google will accept:
- **Preferred: the relay.** `https://<relay>/d/<deviceId>/mail/onboard/callback`
  is already https and already routes to this agent. Derive it from the relay the
  agent is registered with rather than the inbound `Host`.
- **Fallback: loopback.** `http://127.0.0.1:<port>/mail/onboard/callback` is
  explicitly exempt. Only correct when the browser is on the same box.
- Pick based on where the flow was initiated, and return the *same* string from
  both `GET /mail/config` and `handleMailOnboardStart` — if they disagree the
  exchange fails with `redirect_uri_mismatch`. They share it via
  `publicOauthBase` today; keep one source of truth.

Do not paper over this by telling the user to run the wizard on localhost. The
phone is the primary surface.

## P1 — the O365 callback throws its token away and reports success

`handleMailOnboardCallback` (`mail_fetch_http.go:399`). The `gmail` branch is
correct: it decodes the response, requires a refresh token, stores it, marks
done. The `o365` branch (`:452-470`) POSTs the `authorization_code` exchange and
then **never reads the response body**. No decode. No token. It sets
`cfg.Email.Provider = "office365"` and `sess.Status = "done"` unconditionally —
including on an HTTP 400. The user completes the entire Microsoft consent screen
and is told it worked.

It cannot be fixed in place, because there is nowhere to put the token:
`EmailConfig` (`config.go:352-373`) has `GoogleRefreshToken` and **no Azure
refresh-token field at all**. Grep confirms `AzureRefreshToken` /
`MicrosoftRefreshToken` / `GraphRefreshToken` exist nowhere in the tree.

Consequence: O365 only ever works through `getGraphToken` (`mail_fetch.go:330`),
which uses `grant_type=client_credentials` + scope `.default` + a tenant ID —
**app-only** auth. That needs an admin to grant tenant-wide `Mail.Read`, i.e.
read every mailbox in the org to power one inbox.

Fix:
- Add `AzureRefreshToken` to `EmailConfig` (json `azure_refresh_token,omitempty`).
- Decode the exchange response; require a refresh token; on absence or on a
  non-200, set `sess.Status = "failed"` with the provider's error. A discarded
  error must never render as done — this repo's rule is visible failure over
  silent success.
- Add a delegated refresh path alongside `getGraphToken` and **prefer it** when
  `AzureRefreshToken` is set; fall back to client-credentials only when it isn't.
  Use the `/consumers` or `/common` authority when no tenant ID is configured —
  a personal outlook.com account has no tenant.

**Scope of the prize, so you do not oversell it in the commit:** this buys
**personal outlook.com**, which is currently impossible and is the least
obstructed Microsoft path. It does **not** rescue work accounts. Microsoft's
default tenant policy ("Let Microsoft manage your consent settings") names
`Mail.Read`, `Mail.ReadWrite`, `Mail.ReadBasic` on an exclusion list where end
users cannot self-consent; those still escalate to tenant-wide admin consent.
`Mail.Send` is not on that list.

## P2 — `mail_send` cannot use the OAuth you just granted

`handleMailOnboardStart` requests `gmail.send` (and Graph `Mail.Send`). But
`mailSendOpsHandler` (`ops_mail.go`) calls `SendTransactionalEmail`
(`email_send.go:67`), which hard-requires `cfg.Email.SMTPHost` and errors
`"SMTP host missing from config"` — it is `net/smtp` only (`dialAndSendSMTP`,
`:97`). So a user completes Gmail OAuth, grants send, and still cannot send
without separately configuring an SMTP relay the wizard never asks for.

The capability already exists in the *other* stack: `email.go` sends via Graph
`/sendMail` (`:195`) and Gmail `messages/send` (`:494`). The newer `mail_*` stack
regressed it.

Fix: give `mail_send` a provider-API path — when an OAuth provider is configured,
send through Gmail/Graph; fall back to SMTP only when it isn't. Reuse `email.go`'s
send rather than writing a third client. Keep the dry-run default and the
`confirm:'send'` gate exactly as they are (`ops_mail_test.go` covers both — those
tests must still pass).

## The constraint — read before "improving" setup

Verified 2026-07-17. Google and Microsoft independently converged on the same
shape: **sending is cheap, reading is gated.**

- **Google:** `gmail.readonly`, `gmail.compose` and `gmail.metadata` are all
  *restricted* scopes (`support.google.com/cloud/answer/13464325`). Restricted
  scopes require CASA third-party security assessment, renewed **every 12
  months** (`support.google.com/cloud/answer/13463816`), ~$500–1k Tier 2 /
  ~$4.5k Tier 3. `gmail.send` is not restricted. There is no scope-trimming
  escape that keeps an inbox reader.
- **Microsoft:** read is blocked by default tenant policy (above). `Mail.Send` is
  not. Microsoft's real answer for mail clients is a hardcoded allowlist of six
  app IDs (Apple Mail, Spark, eM Client, Thunderbird, +2) with no public
  application process — not a template anyone can copy.

This is why BYO-credentials exists. It is not laziness; it is the only path that
avoids CASA on Google and admin consent on Microsoft. **Do not replace it.**

## Open question — DO NOT GUESS

Whether Yaver's mail is a **send-only tier** (free, no compliance cost on either
platform, ships everywhere) or an **inbox reader** (CASA money on Google, BYO
friction forever on Microsoft) is an unresolved business decision belonging to
the repo owner. The built feature contradicts itself today: the classifier and
`mail_unread` are read features, while the launch posture is free+OSS.

P0/P1/P2 above are correct under **either** answer — that is why they are in this
file and the tier question is not. If a change you are about to make only makes
sense under one answer, stop and leave it.

## Deferred — cross-surface parity (do not do here)

Per CLAUDE.md the fix must eventually reach every surface. Audited state:

| Surface | Path to agent | Verdict |
|---|---|---|
| mobile | `quicClient` | **done** — inbox + wizard + classifier |
| car | `carSurfaceIntent.ts:13-14` | ~90% — `mail_unread`/`mail_send` intents already ship |
| glass | shared RN `DeviceContext` | inherits RN for free |
| web | `agent-client.ts` `callOps` | **missing** — no `MailView.tsx`, no tab. Cheapest full client. |
| desktop | preload → IPC → HTTP | missing; 79KB vanilla renderer is the cost |
| tvOS | `AgentClient.swift:65`, LAN HTTP only | missing; **no relay → LAN-only, structurally** |
| watchOS | `SessionClient`/Convex standalone | missing; follow `Views/GuestAccessView.swift` |
| Wear OS | `PhoneBridge`/`SessionClient` | missing; `WearApp.kt` says "no lists, ever" |

Two unresolved conflicts for whoever picks this up: tvOS mail is LAN-only and
that is structural, not a bug; and watchOS and Wear **contradict each other in
their own source** — `GuestAccessView.swift` renders lists but bans typed-email
compose ("hostile on a wrist"), while `WearApp.kt` bans lists outright. Both
cannot be "parity". Pick which file's rule wins before writing either.

## Verified-safe — do not "fix"

- Guests cannot read the owner's inbox. `/mail` appears nowhere in
  `guest_scope.go`, and `isGuestAllowedPathForScopeVibe` is default-deny.
- `mail_dev_*` (`mail_dev.go`) is a mailpit wrapper for local SMTP testing.
  Unrelated to real inboxes. Leave it.
- `integrations_*`'s "email" channel is an outbound alert sink, not an inbox.
