# Yaver on the Wrist — Apple Watch & Wear OS as the Thinnest Voice Terminal

*2026-06-17. Phase P0/P0' spine BUILT (uncommitted) — see §11 Implementation
status. The verifiable Go + TS layers compile and pass tests; the native
watchOS/Wear apps are scaffolded on the `tvos/` precedent (device-built).
Grounds every claim in code; flags what still needs a device.*

## 0. One-paragraph thesis

The watch is the **car surface pushed one notch more constrained**. It renders
no React Native, holds no agent, runs no chromedp. It is a **voice-first I/O
membrane**: a wrist-raise + a spoken sentence go out; a one-glance summary +
haptic come back. Everything real — the coding agent, redroid, chromedp /
playwright, the personal-agent-gateway CRUD over your credentialed apps — runs
on the **remote runner** (self-hosted box or managed cloud) exactly as it does
for the car. The watch adds **zero new backend**; it adds a new *native client
shell* and a *transport choice* (phone-paired bridge vs. standalone). The
honest framing: ~85% of what a watch needs already exists and is tested; the
net-new is two small native apps and one decision about how the watch reaches
the runner.

This matches the user's stated intent verbatim:
- "connect to remote device, remote device will have chromedp playwright redroid" → the runner is unchanged; watch is just another caller of `/ops` + task dispatch.
- "stt/tts to do things, add/update/get super generic" → the **personal agent gateway** (`gateway_registry.go`) is already a generic CRUD-verb router; the watch is its perfect voice front-end.
- "coding would also be possible" → `carVoiceCoding.ts::dispatchAndSummarize()` already dispatches a coding task to a remote box and speaks a one-sentence result; reuse unmodified.
- "collab with yaver mobile app" → phone-paired is the *default*, easiest topology (WCSession / Wear Data Layer); the phone is the watch's relay and brain-of-record.

## 1. Why this is not a new system — it's a new shell on a proven spine

The in-car voice loop is the reference implementation and it is **built +
tested**, not designed. The watch reuses it almost verbatim. Reuse map:

| Capability | Where it lives today | Watch reuse |
|---|---|---|
| STT (on-device whisper.rn + OpenAI/Deepgram/AssemblyAI) | `mobile/src/lib/speech.ts::transcribe()` | **As-is** (runs on phone when paired; native dictation when standalone) |
| TTS (expo-speech device + OpenAI/OpenRouter cloud) | `mobile/src/lib/speech.ts::speakText()` | **As-is** (phone speaks, or watch speaker / paired AirPods) |
| Risk gate ("confirm before deploy/push/delete/prod") | `mobile/src/lib/carVoiceConfirm.ts::needsConfirm/assessRisk/interpretConfirmReply` | **As-is** — pure functions, zero platform coupling |
| One-sentence readback (never speak code, ≤200 chars) | `mobile/src/lib/carVoiceCoding.ts::summarizeForReadback()` | **As-is** — even more critical on a 1.5″ screen |
| Async dispatch → poll-to-terminal → summarize loop | `mobile/src/lib/carVoiceCoding.ts::dispatchAndSummarize()` (DI-based, headless-capable) | **As-is** — inject watch-flavored `speak`/`getTask` deps |
| Hands-free entry trigger bus | `mobile/src/lib/carVoiceEntry.ts::carVoiceEntryBus` | Pattern reused; native trigger is a watch complication / Siri intent |
| Generic CRUD over credentialed apps (get/add/update/delete) | `desktop/agent/gateway_registry.go` + `gateway_mcp.go::mcpGatewayQuery` | **As-is** — this *is* the "super generic add/update/get" |
| Remote coding task dispatch | `desktop/agent/voice_dispatch.go::DispatchVoiceTranscript` | **As-is** |
| Transport (LAN beacon UDP 19837 / direct 18080 / QUIC relay 4433) | `desktop/agent/beacon.go`, `quic.go`, `relay/protocol.go` | **As-is** when standalone; bypassed when phone-paired |
| Surface viewport hint to tune the model | `desktop/agent/tasks.go::TaskViewport{Surface,PaneCols,PaneRows,TTSBudget,Voice}` | Add one enum value: `"wearable-watch"` |
| Command push to a device | `desktop/agent/blackbox.go::SendCommandToDevice` + `mcp_device_broadcast.go` | **As-is** for "task done, wake the wrist" |

**Net-new is only:** two thin native apps (watchOS SwiftUI, Wear OS Compose),
one transport adapter, one `TaskViewport.Surface` value, and — if you want
standalone push — APNs/FCM wake (which the car never needed because it had the
phone). That is the entire delta.

## 2. The architecture (one diagram, three transports)

```
   ┌─────────────┐   wrist raise + "add a 9am standup to my calendar"
   │   WATCH     │   or "fix the failing test on magara and deploy"
   │ (I/O only)  │   or "is the Trugo charger on Bağdat free?"
   └─────┬───────┘
         │  Transport choice (§3):
         │   A. phone-paired   → WCSession / Wear Data Layer → phone → runner
         │   B. standalone LAN → beacon 19837 → http 18080 /ops
         │   C. standalone net → QUIC relay 4433 → /d/<deviceId>/ops
         ▼
   ┌──────────────────────────────────────────────────────────┐
   │  REMOTE RUNNER  (self-hosted box OR managed cloud)         │
   │   • Coding agent      (code_dev / claude -p / redroid)     │
   │   • Browser engines   (chromedp, playwright)               │
   │   • Android via redroid (gateway_redroid_invoke.go)        │
   │   • Gateway CRUD      (gateway_registry.go: get/add/       │
   │                        update/delete over your apps)       │
   └─────┬────────────────────────────────────────────────────┘
         │  async: task runs seconds→minutes
         │  result → summarizeForReadback() → ≤1 sentence, never code
         ▼
   haptic tap + spoken summary + complication update on the WATCH
   ("Done. Tests pass, deployed to magara.")
```

Two invariants carry over from the car and are **non-negotiable** on the watch:
1. **Async by design.** Never block the wrist on a remote task. Dispatch → "On it" haptic → background → wake on completion. (`dispatchAndSummarize` already does this; the early-ack fire-and-forget is at the `stage === "dispatched"` branch.)
2. **Never render code/diffs.** `isReadCodeRequest()` refuses to read code aloud; `summarizeForReadback()` strips code-shaped lines. A watch cannot show a diff and shouldn't try.

## 3. Transport — the one real decision (and it's already made for you)

The user said "collab with yaver mobile app," which selects the default:

**A. Phone-paired (DEFAULT, ship first).** The watch never talks to the runner
directly. It sends the transcript (or raw audio) over `WCSession`
(watchOS↔iPhone) / Wear Data Layer (Wear OS↔Android phone) to the **already-
working Yaver mobile app**, which runs the *exact* `carVoiceCoding` loop and
pushes the summary back to the wrist. Why this is the right first move:
- Zero new auth: the phone already holds the session token, the connection cache, the relay path. The watch holds nothing sensitive.
- Zero new transport: reuses `quic.ts::sendTask` / `clientFor(deviceId)` unchanged.
- STT/TTS run on the phone (better mic models, no watch battery hit).
- The watch app is ~300 lines of "record/forward/glance" SwiftUI/Compose.
- Matches Apple's own model — most watch apps are phone companions.

**B. Standalone LAN.** Watch on Wi-Fi, phone absent. Watch listens for the
beacon (UDP 19837, same token-fingerprint filter as `mobile/src/lib/beacon.ts`)
and POSTs `/ops` to `http://<runner>:18080` with a bearer token, mirroring
`mobile/src/lib/appletvClient.ts` (LAN-first, relay-fallback). Needs the watch
to hold its own token (Keychain / app-group) — a real but bounded escalation.

**C. Standalone over the internet.** Watch on LTE/Wi-Fi, off-LAN. QUIC relay at
`/d/<deviceId>/ops`, exactly the appletvClient relay fallback. This is the only
mode that wants a push-wake (APNs/FCM) so a long task can re-raise the wrist —
the **one piece of genuinely new backend** (`watch_push.go` token store +
deliver on `blackbox` task-complete).

**Recommendation:** Ship A only for v1. Add B for "phone-left-on-desk" once A
proves out. C last, behind the same managed-cloud story the car uses.

## 4. The interaction model — generic CRUD, voice-first

The wrist has three input affordances and you want all three to funnel into the
**same generic intent router** the gateway already exposes:

1. **Voice (primary).** "add / update / get / delete X" → STT → the gateway's
   NL→`{connector, capability, params}` routing (`gateway_mcp.go`
   `CapabilitiesForMCP()` already advertises the verb surface to the model).
   Examples that work with *today's* read path and the *specced* write path:
   - get: "what's my next meeting" → `gateway_query("google","next_event")`
   - get: "is the Bağdat Trugo free / how much" → EV connector (one of the gateway connectors)
   - get: "EUR right now" → broker/fx connector
   - add (write/ACT, specced): "add standup 9am tomorrow" → `gateway_act("google","add_event",…)` behind confirm
   - update/delete: same shape, all behind `carVoiceConfirm.needsConfirm()`

2. **Complication tap (glanceable + one-shot).** A watch face complication is
   the wrist's "quick action." Bind 2–3 to fixed intents: "run tests on
   primary," "is my charger free," "deploy." Tapping dispatches without
   speaking — the cheapest possible interaction.

3. **Canned-reply / dictation reply.** When the runner asks a question
   (task status `review`, or a gateway 2FA human-gate), surface it as a
   notification with WatchKit canned replies + dictation — the same shape as
   the Android Auto `RemoteInput` reply flow already modeled in
   `carMessagingNotification.ts`.

**"Super generic add/update/get"** is not a watch feature to build — it is the
gateway's existing verb model (`Capability.Verb` ∈ get/add/update/delete). The
watch just speaks into it. The only watch-specific rule: **every write verb is
confirm-gated** (wrist taps are easy to fire by accident; the car already
treats deploy/push/delete/prod as confirm-required).

## 5. Coding on the wrist — yes, and here's the honest envelope

"Coding would also be possible" — true, and already wired, with one caveat about
*what* coding means on a watch:

- **What works today, unchanged:** dictate an intent ("add a test for the auth
  refresh path and run it on magara") → `dispatchAndSummarize()` →
  `voice_dispatch.go` creates a task, source=`voice-input`, `TaskViewport.Voice`
  → the remote coding agent (claude -p / code_dev) does the work → terminal
  status → `summarizeForReadback()` → "Done. New test passes." This is the car
  path; the watch inherits it for free in phone-paired mode.
- **What the watch is good at:** *dispatch + verdict*. Kick off, abort, approve
  a `review`-gated step, hear pass/fail. Glance-and-go.
- **What the watch must never attempt:** showing a diff, scrolling output,
  editing text. `summarizeForReadback` and `isReadCodeRequest` already enforce
  the no-code-on-screen rule. For "show me the diff," the correct response is
  a hand-off: "Sent the diff to your phone" (§7).
- **Review gate is the killer feature here.** A coding task that hits a
  human-decision point (`status: review`, which `isTerminalTaskStatus` treats
  as terminal) becomes a wrist notification: "magara wants to force-push — say
  confirm or open phone." That's `carVoiceConfirm.interpretConfirmReply()` on a
  tap target. This is genuinely better on a watch than anywhere else.

## 6. Platform reality — two thin native apps, NOT React Native

This is settled precedent in the repo, not an open question. The **tvOS ADR
(`docs/yaver-tvos-fork-adr.md`)** already rejected forking React Native for
non-touch surfaces, and the **tvOS scaffold proves the chosen pattern**: a
separate, reproducible native project (`tvos/project.yml` → `xcodegen generate`,
bundle `io.yaver.tv`, thin SwiftUI talking to the agent's `/ops`). Watches
follow identically — watchOS and Wear OS **do not run React Native**, and 90% of
the RN app (touch-first screens) is useless on a wrist regardless.

**watchOS** — mirror `tvos/`:
- New `watch/` dir with `watch/project.yml` (XcodeGen), bundle `io.yaver.watch`, `DEVELOPMENT_TEAM 5SJZ4KA39A`, deploymentTarget watchOS 10+.
- SwiftUI app: wrist-raise record → `WCSession.sendMessage(transcript)` to the iPhone Yaver app → receive summary → render one line + haptic.
- Standalone (mode B/C) adds a tiny HTTP/relay client mirroring `appletvClient.ts` (LAN `/ops` first, QUIC relay fallback) + Keychain token.
- Reproducible-project-out-of-git, like tvOS (`tvos/.gitignore` pattern).

**Wear OS** — mirror Android TV's plumbing:
- Either a sibling Gradle module (`mobile/android/settings.gradle` → `include ':wear'`) **or** a standalone Compose project (cleaner; avoids `expo prebuild --clean` regeneration fights). Given the prebuild-regeneration hazard (CLAUDE.md §cold-start), a **standalone project like `tvos/` is the safer choice** than a `:wear` module that an Expo plugin must keep re-injecting.
- Jetpack Compose for Wear OS app: voice (built-in dictation or on-device) → Wear Data Layer `MessageClient` to the Android phone app → summary back.
- Standalone mode = same LAN-`/ops` / relay client as watchOS.

**Why standalone projects over Expo config plugins here:** `withMeshTunnel.js`
and `withAndroidAutoMessaging.js` show the plugin pattern works, but it forces
every watch source to survive `expo prebuild --clean` re-injection. The watch
apps share almost nothing with the RN bridge (unlike the Android Auto module,
which *must* register in `MainApplication`). The tvOS precedent — fully separate
XcodeGen project — is the lower-friction path. Keep watch builds out of the
mobile prebuild entirely.

## 7. Collaboration with the Yaver mobile app — the handoff is the point

Phone-paired is not just a transport; it's a **division of labor**:

- **Watch = quick membrane.** Dispatch, verdict, confirm, glance.
- **Phone = the screen of record.** Anything the watch can't show goes here.
- **Handoff verbs:** "open this on my phone" / "send the diff to my phone" →
  the watch tells the phone (WCSession) to foreground the relevant RN screen
  (Hot Reload tab, task detail, the `code` TUI attach). The phone already has
  the `openAppBus` / command-stream plumbing to navigate itself
  (`device_broadcast_command` → `blackbox` `open_app`).
- **Continuity:** start a task by voice on the wrist while walking; sit down,
  the phone (or web dashboard) already shows it streaming — because it's the
  *same task on the same runner*, the watch only ever held a reference
  (taskId), never state. This is the thin-terminal payoff: no sync problem,
  because the watch owns nothing.
- **Failure ergonomics:** if the watch loses the runner mid-task, the task
  keeps running (it's on the remote box); the wrist just stops getting updates
  and the phone picks them up. Same as the car's "Still working — I'll let you
  know on your phone."

## 8. Honest constraints & risks

- **No long background polling on watchOS.** Apple kills background execution
  aggressively. The async-completion wake must be a **push** (APNs in
  standalone mode C) or a **phone-relayed local notification** (phone-paired
  mode A — the phone polls, the phone pushes the complication update). Mode A
  sidesteps the whole problem; that's another reason to ship it first.
- **STT quality / battery on the watch mic** is worse than the phone. Prefer
  phone-side STT (mode A) or paired AirPods. On-device whisper.rn on a watch is
  likely too heavy — don't promise it.
- **Accidental dispatch.** Wrist taps and "Hey Siri" misfires are real. Every
  *write/ACT/coding-deploy* verb is confirm-gated (reuse `needsConfirm`); read
  verbs (get) fire freely.
- **Apple review surface.** A watch app that "controls a remote computer" is
  fine; one that reads arbitrary remote screen content aloud invites scrutiny.
  The no-code-readback / summary-only design keeps it clean — same posture as
  the CarPlay entitlement doc.
- **Two stores, two reviews, two signings.** watchOS rides the iOS app's
  TestFlight/App Store record (companion); Wear OS is a separate Play listing.
  Budget the submission overhead, not just the build.
- **Standalone token custody.** Mode B/C means a session token on the watch
  Keychain. Bounded, but it's the one place the watch stops being "holds
  nothing sensitive." Gate it behind an explicit "use without phone" opt-in.
- **The gateway WRITE path is specced, not built.** `gateway_registry.go` is
  READ-ONLY today (`Verb: "get"`). "add/update/delete by voice" depends on the
  gateway ACT layer (`docs/yaver-personal-agent-gateway.md §16`) landing first.
  The watch is a *consumer* of that work, not a driver of it — don't let watch
  scope pull the ACT/consent model forward half-baked.

## 9. Build phases (smallest shippable first)

- **P0 — Phone-paired watchOS, read + coding-dispatch only.** `watch/`
  XcodeGen project; SwiftUI raise-to-record; WCSession → phone runs
  `carVoiceCoding` loop unchanged; one-line summary + haptic back. Add
  `TaskViewport.Surface = "wearable-watch"` (PaneCols ~16, PaneRows ~4,
  TTSBudget ~150, Voice true). **No new backend.** This is the 80/20.
- **P0' — Wear OS equivalent.** Standalone Compose project, Wear Data Layer to
  Android phone app. Same loop.
- **P1 — Complications + canned-reply confirm.** Bind 2–3 fixed intents to
  complications; surface `review`-gated tasks as notifications with
  confirm/cancel (reuse `interpretConfirmReply`).
- **P2 — Generic gateway CRUD by voice.** Once the gateway ACT layer ships,
  the watch's "add/update/delete" verbs light up for free — they route through
  the same `gateway_query`/`gateway_act` the phone uses.
- **P3 — Standalone (mode B LAN, then C relay + push).** Watch token in
  Keychain, `appletvClient`-style LAN `/ops` + relay; `watch_push.go` for
  completion wake. Behind an explicit "works without your phone" toggle.

## 10. Implementation status (2026-06-17 — BUILT, uncommitted)

The phone-paired spine (P0/P0') plus the standalone agent endpoint (early P3)
are built. Verified layers compile and pass tests; native apps are scaffolds
that build on a device, exactly like `tvos/`.

**Verified (Go + TS):**
- `desktop/agent/viewport_prompt.go` — `wearable-watch`/`wearable-wear` surface
  shape ("ONE short sentence, no code"); `tasks.go` doc + `viewport_prompt_test.go`.
- `desktop/agent/watch_risk.go` — Go mirror of the phone risk gate +
  one-sentence summarizer + read-code guard + complication-intent expansion.
  `watch_risk_test.go` (7 tests) green in isolation.
- `desktop/agent/watch_http.go` — standalone `POST /watch/turn` (non-blocking
  dispatch, stateless base64 confirm token) + `GET /watch/result` poll; routes
  registered `s.auth` in `httpserver.go` next to `/mobile/*`.
- `mobile/src/lib/watchBridge.ts` — the phone-side bridge: wire-protocol v1
  source of truth + `handleWatchTurn` reusing `carVoiceConfirm` (risk gate) and
  `carVoiceCoding::dispatchAndSummarize` (the car loop, unchanged).
  `watchBridge.test.mts` (11 tests) green.
- `mobile/src/lib/watchEntry.ts` — the native-transport adapter bus
  (`configure`/`deliver`/`sender`, `parseTurn` validation). `watchEntry.test.mts`
  (5 tests) green. `tsc --noEmit` clean.

> Note: the agent package does not fully `go build` right now because an
> unrelated parallel session left an untracked `gateway_intent.go` that
> redeclares `containsAny` against the committed `repos_http.go`. The watch
> code has **zero** errors of its own (confirmed by isolating it); that
> conflict is someone else's WIP to resolve, not part of this work.

**Scaffolded (native, build on device — like `tvos/`):**
- `watch/` — watchOS SwiftUI app (XcodeGen `project.yml`, bundle `io.yaver.watch`):
  WCSession phone-paired primary + standalone LAN `AgentClient`/device-code auth.
  `xcodegen generate` validated; `swiftc -parse` passes on SDK-independent files.
- `wear/` — standalone Wear OS Compose project (`io.yaver.wear`): Wear Data Layer
  primary + standalone LAN client.
- `mobile/native-watch/ios/` (WCSession bridge), `mobile/native-wear/android/`
  (Wear Data Layer module + listener service + ReactPackage), and
  `mobile/plugins/withWatchBridge.js` (copies sources on prebuild) — the plugin
  loads cleanly and is **deliberately unregistered** in `app.json` (same posture
  as `withMeshTunnel.js`) until the native targets are built on a Mac/device.

**Wire protocol v1** (one source of truth, mirrored 4×): `watchBridge.ts` ↔
`watch_http.go` ↔ `WatchProtocol.swift` ↔ `WatchProtocol.kt`. Watch→server:
`transcript` / `confirm{token,reply}` / `intent`. Server→watch: `ack` /
`confirm-needed{token,prompt}` / `working` / `summary` / `error` / `handoff`.

**Remaining to ship (need a Mac + paired devices):** add the watchOS companion
target to the Xcode project and register `withWatchBridge.js`; build `wear/` and
install on a paired watch; on-device pair test (Simulator/emulator
WCSession/Data-Layer pairing is unreliable). Then P1 (complications + canned
confirm), P2 (gateway CRUD once the ACT layer lands), P3 (relay + push-wake).

## 11. Verdict

Adding the watch is **mostly a client-shell exercise, not a systems exercise.**
The hard parts — voice I/O, risk gating, one-sentence summarization, async
dispatch-to-terminal, relay transport, the generic CRUD gateway, the coding-task
path — are built and tested for the car and are **device-agnostic by
construction** (dependency-injected, pure-function). The watch reuses them
verbatim and contributes a thin native membrane plus one transport decision
(phone-paired first). The genuinely new work is two small native apps
(`watch/` XcodeGen + a Wear Compose project, both following the tvOS precedent)
and, only for the standalone case, a push-wake. Coding, browsing (chromedp/
playwright), Android-app driving (redroid), and credentialed-app CRUD all happen
on the unchanged remote runner — the wrist just speaks to it and glances at the
answer.

The single most important design commitment, inherited intact from the car:
**the watch owns nothing and shows nothing complex.** It is a voice in and a
sentence out. Everything else is somewhere with a real screen and a real CPU.
```
