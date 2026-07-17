---
doer: codex
---

# Mail: wire the connector into every UI surface + Settings

## Why this exists

The Gmail/O365 connector is built and works over `/ops` (`mail_search`,
`mail_unread`, `mail_send`) and `/mail/*` HTTP routes. **Mobile already has a
full inbox** at `mobile/app/(tabs)/mail.tsx` — list, classifier chips, AI reply
drafts, compose, and a 3-step OAuth setup wizard — reachable from a "Mail" row in
`more.tsx:3931`.

Every other surface has **nothing**. There is no `MailView.tsx` on web, no mail
tab, and no Mail entry in Settings anywhere. Per CLAUDE.md's cross-surface parity
rule, RN surfaces (car, glass) inherit from shared code, but web / desktop /
tvOS / watchOS / Wear OS **do not inherit and must be ported explicitly**.

This file is the port. The agent-side defects are a **separate** task file
(`tasks/mail-gmail-o365.md`) — do not fix them here, and do not wait for them:
the UI wiring is correct regardless of whether OAuth setup currently completes.

## Ground rules

- Do the priorities **in order**. Each is one increment the gate can verify.
- **Gate:** `cd web && npx tsc --noEmit` and `cd mobile && npx tsc --noEmit`.
  Both must pass. If a priority you touched has no typecheck coverage (vanilla
  JS), say so in the commit rather than claiming it verified.
- **Do not run `go test ./...` in `desktop/agent`** — `TestAuthLogout` hits the
  real `~/.yaver` and signs the machine out. You should not need Go at all here.
- Scope is `web/**`, `mobile/**`, `desktop/app/**`. **Do not touch
  `desktop/agent/**`** — that is the other task file's scope and a shared
  checkout will collide.
- **Do not touch `tvos/**`, `watch/**`, `wear/**`.** They need Xcode/Gradle gates
  and have unresolved design conflicts — see "Deferred" below.
- **Reuse, do not reinvent.** Every verb already exists. You are adding views
  that call `callOps`, nothing more. If you find yourself writing a Gmail client,
  stop — you are in the wrong layer.
- No new dependencies. No `lucide` — this repo uses inline SVG.
- Email content is local-only and must never reach Convex
  (`convex_privacy_test.go:31` is the fence). A view renders what `/ops` returns;
  it must not persist or forward it.

## P0 — web: `MailView.tsx` + dashboard tab

The cheapest full client in the repo. `web/lib/agent-client.ts` already exposes
`callOps(verb, payload)` (~:1949) — the transport is done, there is zero
plumbing work.

- Add `web/components/dashboard/MailView.tsx`. Follow
  `CircuitCellView.tsx` as the template — it is the cleanest example and its
  header comment states the rule you are following: *"Web is relay-only by
  design. All circuit_* verbs are the same the mobile cell calls."* Same applies
  to `mail_*`.
- Signature must match its siblings:
  `export default function MailView({ devices, token }: { devices: Device[]; token: string | null })`.
- List via `callOps("mail_search", {...})` / `callOps("mail_unread", {...})`.
  Compose via `callOps("mail_send", {...})`.
- **`mail_send` is dry-run by default and requires `confirm:'send'` to execute**
  (`ops_mail.go`). The web UI must make that a real confirm step, not a silent
  pass-through of `confirm:'send'`. Do not defeat the gate — it is the only thing
  standing between an AI draft and a sent email.
- Render the classifier bucket (`personal`/`transactional`/`marketing`/`bulk`)
  the same way mobile does — mobile's `CHIP_COLORS` (`mail.tsx:30`) is the
  reference for parity.
- Wire into `web/app/dashboard/page.tsx`: add `"mail"` to the tab-id union
  (~:837), to the `tabs` array (~:1975), to `CONNECTION_REQUIRED_TABS` (~:734),
  and add the `activeTab === "mail" ? <MailView … />` branch in the ladder
  (from ~:2706).

## P1 — Settings entries (the explicit ask)

Today mail is reachable only from mobile's More menu. There is no Settings entry
on any surface. Add one everywhere Settings exists.

- **Mobile** (`mobile/app/(tabs)/settings.tsx`): add a "Mail" section following
  the existing `<View style={styles.section}>` + `<Text style={styles.sectionLabel}>`
  shape. Place it near "Coding agent"/"Voice", not in the Account block.
  It should show connection state (`GET /mail/config` returns
  `{gmailConfigured, o365Configured}`) and deep-link to the existing wizard via
  `router.navigate("/(tabs)/mail")` — **do not rebuild the wizard in Settings.**
  Note the repo's own convention: credential-heavy setup lives on its own screen
  (`vault.tsx`, `apikeys.tsx`, `accounts.tsx`), and Settings only links to it.
- **Web**: add the equivalent to `SettingsView.tsx` — connection state + a link
  to the Mail tab. `web/app/integrations/page.tsx:212` already advertises
  *"Email — Email notifications via Office 365 or Gmail — Configure in
  settings"*. That string is currently **a lie**: there is no such setting. P1
  is what makes it true.
- Consider `mobile/src/lib/moreOptionalTools.ts` (`OPTIONAL_MORE_TOOLS`) if Mail
  should be toggleable in the More menu rather than always shown.

## P2 — desktop Electron

Transport is trivial; the renderer is the cost.

- `desktop/app/src/main/preload.js`: expose mail through the existing generic
  seam — `ipcRenderer.invoke('agent-request','POST','/ops',{verb,payload})`.
  Follow the per-domain sugar already there (`listTasks`, `inviteGuest`).
- The renderer is a **single ~79KB vanilla `index.html`** — no React, no bundler.
  Budget for hand-written list/read/compose, and keep it modest: a list plus a
  read pane is honest; a full compose experience is not worth the cost here.
- `tsc` does not cover this. Do not claim it typechecked.

## The tier caveat — flag, do not resolve

Whether Yaver mail is a **send-only tier** or an **inbox reader** is an open
business decision owned by the repo owner (see `tasks/mail-gmail-o365.md`,
"Open question"). Reading Gmail requires restricted scopes (CASA, ~$500–4.5k/yr,
renewed annually); `gmail.send` does not. Microsoft mirrors this.

**This file assumes the reader tier**, because mobile already ships a reader and
"parity" means matching it. That assumption is inherited, not decided. If the
owner picks send-only, P0's list view is dead code and only compose survives —
so keep list and compose in separate components, and do not entangle them. That
separation is the whole hedge; respect it.

## Deferred — native surfaces (do NOT do here)

| Surface | Why deferred |
|---|---|
| tvOS | `AgentClient.swift:65` is LAN-HTTP direct — **no relay, structurally**. Mail works on the couch and nowhere else. Decide if that ships before building it. |
| watchOS | Follow `Views/GuestAccessView.swift`; standalone-opt-in only. |
| Wear OS | `WearApp.kt` says *"No tabs, no lists, no diffs. Ever."* |

**watchOS and Wear contradict each other in their own source.**
`GuestAccessView.swift` renders lists but bans typed-email compose ("hostile on a
wrist"); `WearApp.kt` bans lists outright. Both cannot be "parity". Whoever picks
this up must pick which file's rule wins **before** writing either — that is a
design decision, not an implementation detail.

## Already done — do not redo

- **mobile** — full inbox, wizard, classifier, drafts (`mail.tsx`).
- **car** — `mobile/src/lib/carSurfaceIntent.ts:13-14` already ships `mail_unread`
  and `mail_send` intents with spoken summaries, address-missing bailout, and
  confirm-gating. Tests in `carSurfaceIntent.test.mts`.
- **glass** — shared RN `DeviceContext`; inherits for free. Verify, don't rebuild.
- **voice** — `createVoiceCore`'s `callOps` option already routes "read me my
  unread email" through `surfaceIntentInterceptor`. A reply-to-last-read intent
  is the only real gap.
