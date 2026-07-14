# Yaver — Remote Runtime From the Car by Voice (+ cheap in-car category surfaces)

> **Status:** design-only + build-safe scaffold, 2026-06-17. No native rebuild,
> nothing committed, no entitlements requested.
> **Source of truth:** this document was written *after* grepping the actual
> code, not from the build brief. Where a brief claim diverged from the tree,
> the code wins and it's called out. Re-verify before building — other threads
> move constants in parallel.

This is the implementing-agent's reference for two related, low-effort
features:

1. **The "cheap" in-car category surfaces** — Android Auto **messaging** +
   **weather** (and an IoT skeleton noted as a heavier follow-up). These are
   the Android Auto categories that need *no* special entitlement.
2. **Remote runtime from the car by voice** — speak a command → STT → dispatch
   to the selected Yaver machine/runtime → result summarized to one sentence →
   TTS read back over car audio. Coding, Talos operation, builds, and machine
   checks are all task types behind the same voice loop.

The two features share one delivery vehicle: **the remote runtime appears as a
conversation you message by voice and that reads replies aloud.** Android Auto
treats that as a *messaging app*, which is the cheapest, entitlement-free way
onto the car head unit.

---

## 1. One-paragraph thesis

Almost the entire pipeline already exists and ships today. Yaver mobile has a
complete STT layer (`mobile/src/lib/speech.ts`: on-device whisper.rn,
`expo-speech` device TTS, plus OpenAI/Deepgram/AssemblyAI cloud), a complete
agent-voice WebSocket loop (`mobile/src/lib/agentVoice.ts` ↔ agent
`/voice/stream`), and the agent already turns a final transcript into a remote
coding task and waits for its result
(`desktop/agent/voice_dispatch.go::DispatchVoiceTranscript`, source
`"voice-input"`, with a voice-budgeted TTS readback constraint baked into the
prompt wrapper). The remote-box dispatch path is just
`quicClient.sendTask(..., codeMode=true)` against whichever device the
connection pool is pointed at (`connectionManager.clientFor(deviceId)`). So
"car voice runtime" is **not** a new pipeline — it is a *new front end and a
new safety posture* over the existing remote-task path. The net-new work is: (a) a
hands-free / push-to-talk **driving loop** that records → STT → dispatch →
summarize-to-one-sentence → TTS without ever rendering a diff; (b) the
**Android Auto MessagingStyle** notification surface so the loop can run from
the car head unit with `expo-notifications` (already a dependency); and (c) the
manifest plumbing those notifications need, injected via an **Expo config
plugin** so `expo prebuild --clean` doesn't wipe it.

---

## 2. The compliance reframe: agent-as-contact / messaging category

Car platforms refuse to let a generic app paint arbitrary UI on the head unit —
that is the entitlement wall. But both Android Auto and CarPlay carve out a
**messaging / communication** category that any app may use to (a) post an
incoming-message notification, (b) be read aloud by the Assistant, and (c)
accept a spoken reply via voice. The reframe:

> The **remote runtime is a contact.** You "message" it by voice; it
> "replies" with a one-sentence status that the car reads aloud. The whole
> runtime session is modeled as a *conversation thread* (`MessagingStyle`),
> not as an app screen.

This is honest — it *is* a conversational, asynchronous, voice-first
interaction. On Android it is exactly the shape Android Auto's notification
messaging templates are designed for. On Apple, the submitted request is
Voice-Based Conversational, because Yaver is voice-first remote runtime control
rather than human messaging or VoIP. It is also the lowest-effort Android path:
a MessagingStyle notification needs no CarAppService, no Kotlin `Screen`
hierarchy, no template review.

---

## 3. The three delivery tiers

| Tier | Surface | Entitlement | Ships | What it is |
|---|---|---|---|---|
| **0** | Phone app + Bluetooth car audio + push-to-talk / Siri Shortcut | **None** | **today** | The voice loop runs in the phone app; audio plays over the car's A2DP/Bluetooth speakers. PTT button, lock-screen, or a Siri Shortcut / Android shortcut triggers it. No car SDK at all. |
| **1** | Android Auto **MessagingStyle** notification | **None** | next | The runtime conversation is an `expo-notifications` MessagingStyle notification with a `RemoteInput` reply. Android Auto reads new "messages" (status updates) aloud and lets you reply by voice from the head unit. Needs only the `automotive_app_desc.xml` + `com.google.android.gms.car.application` manifest meta-data. |
| **2** | CarPlay voice-based conversational | **Entitlement (strict Apple review)** | submitted | A real CarPlay voice-control scene. Requires Apple to grant the voice-based conversational entitlement, a regenerated provisioning profile, and a minimal CarPlay template that never displays code/logs/diffs. |

**Tier 0 is the product that ships now.** Tiers 1 and 2 are progressive
enhancements that put the *same loop* on the actual head unit.

### Cheap in-car category surfaces (separate, additive)

Beyond coding, Android Auto's entitlement-free categories also include
**weather** and **messaging**. Yaver already has a `weather` MCP verb
(agent-side) and `expo-notifications`. So the "cheap in-car category" surfaces
are:

- **Messaging** — the runtime conversation (this doc's Tier 1).
- **Weather** — a glanceable card / spoken weather brief sourced from the
  existing `weather` verb. (Skeleton only; not built here.)
- **IoT / EV** — a `CarAppService` (`androidx.car.app`) Kotlin surface for
  lights / EV charging. This is the **heavy follow-up** — it needs a real
  native Kotlin service, not a notification — and is explicitly **out of scope
  for this pass**. Noted here so it isn't forgotten.

---

## 4. Safety / UX rule (load-bearing)

This is the one non-negotiable design constraint:

> **Async voice command in, high-level STATUS read-back out. Never read
> diffs, code, file contents, or long output aloud while driving.**

Concretely:

- The readback is **one sentence** (`summarizeForReadback`, hard cap ~200
  chars). It says *what happened*, not *what changed* — "Done. Tests pass on
  magara." / "Failed — build error in auth.go." / "Working on it, I'll tell
  you when it's done."
- The agent already enforces a voice budget: `voice_dispatch.go` marks the
  task `Voice: true` so the prompt wrapper produces a terse, TTS-shaped
  result, and `voicePickResultText` truncates to ~600 chars server-side. The
  car loop truncates again to one sentence client-side — **belt and braces**.
- **No code is ever spoken.** If the user explicitly asks "read me the diff"
  the loop declines with "I'll have it ready on your phone when you're
  parked." (Driving-mode guard.)
- The interaction is **asynchronous**: you fire a command and keep driving;
  the result arrives as a spoken status (Tier 0) or an incoming MessagingStyle
  "message" (Tier 1) when the remote task finishes. You are never asked to
  read a screen.

This is consistent with the project's content-agnostic, utility-first stance
and with platform driver-distraction rules.

## 4.1 OAuth / sign-in handoff while driving

Car surfaces do **not** run OAuth. That rule applies to CarPlay, Android Auto,
and the phone-over-Bluetooth Tier 0 flow.

- **Yaver runtime sign-in:** if a car-initiated command needs a machine to be
  signed in, Yaver creates the same device-code URL used by TV and CLI:
  `https://yaver.io/auth/device?code=ABCD-1234`. The car reads the short code
  aloud and sends a push/deep link to the phone. The already-signed-in Yaver
  phone app approves it.
- **Claude Code / Codex auth:** the car starts no provider browser. It asks the
  selected runtime for a `runner_auth browser_start` session, then hands
  `https://yaver.io/runner-auth/browser?runner=claude|codex` or the provider
  `openUrl` to the phone. The phone opens the system browser sheet and handles
  provider redirects, clipboard code capture, biometrics, and token writeback.
- **iOS and Android parity:** iOS uses Universal Links/custom scheme routing;
  Android uses App Links/custom scheme routing. Both land in the same mobile
  screens (`approve-device`, `runner-auth/browser`, `runner-auth/approve`).

While the vehicle is moving, the head unit should only say "Approve this on
your phone" or "I need Codex sign-in on your phone." It should never display a
provider login page, long URL, password field, API key field, or OAuth token.

---

## 5. The STT → dispatch → summarize → TTS pipeline, mapped onto existing primitives

```
 [PTT / Siri Shortcut / Android Auto reply]
        │  (record short utterance, expo-av)
        ▼
 ┌──────────────────────────┐
 │ STT                      │  mobile/src/lib/speech.ts::transcribe()
 │  on-device whisper.rn    │  — already supports on-device + OpenAI/Deepgram/
 │  or cloud provider       │    AssemblyAI; config in Settings > Voice
 └──────────────────────────┘
        │  final transcript (string)
        ▼
	 ┌──────────────────────────┐
	 │ DISPATCH to remote box   │  quicClient.sendTask(title, transcript,
	 │  (Yaver runtime)         │    model, runner, …, codeMode=true)
 │                          │  → POST {box}/tasks  source="mobile-code"
 │                          │  via connectionManager.clientFor(deviceId)
 └──────────────────────────┘
        │  taskId
        ▼
 ┌──────────────────────────┐
 │ POLL until terminal      │  quicClient.getTask(taskId) until status ∈
 │                          │    {completed, failed, stopped, review}
 │                          │  (server already waits + budgets via
 │                          │   voice_dispatch.go for the WS path; the car
 │                          │   loop polls REST so it works on any box)
 └──────────────────────────┘
        │  task.resultText / status
        ▼
 ┌──────────────────────────┐
 │ SUMMARIZE → one sentence │  summarizeForReadback() — local, deterministic,
 │                          │  no extra LLM call. status + first clause.
 └──────────────────────────┘
        │  spoken string (≤ ~200 chars)
        ▼
 ┌──────────────────────────┐
 │ TTS over car audio       │  speech.ts::speakText() → expo-speech (device,
 │                          │  free) OR OpenAI/OpenRouter. Plays over the
 │                          │  active audio route (Bluetooth car speakers).
 └──────────────────────────┘
```

The **agent-side WS variant** (`agentVoice.ts` ↔ `/voice/stream` ↔
`voice_dispatch.go`) already does STT-in / task / TTS-out end to end and is the
better path when the box's Deepgram/Cartesia keys are configured. The **car
loop deliberately also supports a REST fallback** (client-side STT via
`speech.ts`, dispatch via `sendTask`, poll via `getTask`, TTS via `speakText`)
so it works against *any* box — including one with no voice keys — and so the
driving summary/guard logic lives client-side where the driving-mode rules
apply.

---

## 6. What already exists vs what is net-new

### Already exists (verified, file:line)

- **STT, all providers** — `mobile/src/lib/speech.ts::transcribe()` (on-device
  whisper.rn `startRealtimeTranscribe` / `transcribeWithWhisper`, plus OpenAI,
  Deepgram, AssemblyAI). Device + cloud TTS in the same file
  (`speakText()`, `expo-speech` at speech.ts:521).
- **Agent voice WS loop** — `mobile/src/lib/agentVoice.ts::AgentVoiceSession`
  (start/streamAudioFile/finalize, transcript/task/tts-frame callbacks);
  mic UI `mobile/src/components/AgentVoiceButton.tsx`.
- **Agent-side transcript → remote coding task → TTS readback** —
  `desktop/agent/voice_dispatch.go::DispatchVoiceTranscript` (source
  `"voice-input"`, `Voice:true` viewport, terminal-status poll, 600-char
  readback trim); WS handler in `desktop/agent/voice_http.go`; routes wired in
  `desktop/agent/httpserver.go` (`/voice/status`, `/voice/stream`,
  `/voice/config`).
- **Remote-box task dispatch + poll** — `mobile/src/lib/quic.ts`
  `QuicClient.sendTask(... codeMode)` (quic.ts:1584, POST `/tasks`,
  `source="mobile-code"`) and `getTask()` (quic.ts#L1893). Cross-device routing
  via `mobile/src/lib/connectionManager.ts::clientFor(deviceId)`
  (connectionManager.ts#L102) + `peerEndpoint`.
- **Notifications dependency** — `expo-notifications` already a dependency and
  already registered in `mobile/app.json` `plugins`.
- **Expo config-plugin pattern** — `mobile/plugins/withMeshTunnel.js` is the
  exact "manifest-injecting, deliberately-unregistered-until-activated"
  template to copy.
- **Weather source** — `weather` MCP verb (agent-side).
- **`say` MCP verb / host TTS** — agent `httpserver.go:8029` (`case "say"`),
  `voice_listen.go:366` (`say` binary), `mcp_tools.go:1511`.

### Net-new (this pass builds the bracketed items)

- **[built] Tier 0 car voice loop** — `mobile/src/lib/carVoiceCoding.ts`: the
  driving-safe orchestrator (PTT → STT → dispatch → poll → summarize → TTS),
  with `summarizeForReadback()` and the driving-mode guard. Self-contained,
  importable, with a `.test.mts`.
- **[built] Tier 1 Android Auto plugin** —
  `mobile/plugins/withAndroidAutoMessaging.js`: injects
  `automotive_app_desc.xml` + the `com.google.android.gms.car.application`
  `<meta-data>` so the app is recognized as an Android Auto messaging app.
  **Deliberately not registered in app.json** (activation = register + native
  rebuild + on-device test), mirroring `withMeshTunnel.js`.
- **[built] Tier 1 MessagingStyle helper** —
  `mobile/src/lib/carMessagingNotification.ts`: builds the MessagingStyle +
  CarExtender + RemoteInput reply notification representing the remote-runtime
  conversation, on top of `expo-notifications`. Native CarExtender fields that
  `expo-notifications` can't express are documented as the activation gap.
- **[not built — heavier follow-up] IoT / EV `CarAppService`** — a real
  Kotlin `androidx.car.app` surface. Out of scope; see §3.
- **[account granted — profile-gated] Tier 2 CarPlay** voice-based conversational
  scene. Apple granted the account-level capability on 2026-07-14; keep the
  entitlement key out of the build until the `io.yaver.mobile` App ID is
  configured and the regenerated provisioning profile carries it.
- **[not built — wiring] UI entry point** — a hidden/dev screen or Settings
  toggle + Siri Shortcut / Android shortcut to start the Tier-0 loop. The lib
  is importable now; wiring is a follow-up.

---

## 7. Honest gaps / risks

1. **`expo-notifications` cannot fully express CarExtender / MessagingStyle.**
   The managed-Expo notification API exposes `title`/`body`/`data`, not the
   native `NotificationCompat.MessagingStyle` + `CarExtender.UnreadConversation`
   builder. `carMessagingNotification.ts` therefore ships a *best-effort*
   notification today and documents the exact native fields a small native
   module (or `@config-plugins/...`-style mod) must add to be Auto-readable.
   Tier 1 is "scaffolded + documented", not "head-unit-verified".
2. **No on-device verification.** Per the no-native-rebuild constraint, none of
   this was built into an APK or driven on a head unit. Android Auto messaging
   needs DHU (Desktop Head Unit) or a real car to verify.
3. **Driving-mode detection is best-effort.** True "am I driving" needs the
   car connection signal (Android Auto connected / CarPlay scene active). Tier
   0 from the phone can't know for sure, so it relies on the user invoking the
   PTT/Shortcut deliberately and on the one-sentence-readback guard regardless.
4. **CarPlay (Tier 2) is profile-gated** — Apple granted the account-level
   voice-based conversational capability, but the App ID/profile must carry it
   before the native scene can ship.
5. **Latency.** A remote coding task can take minutes. The loop must speak an
   immediate "On it" acknowledgement and deliver the result asynchronously
   (Tier 0: a later spoken status; Tier 1: an incoming MessagingStyle message)
   — never block the driver waiting.

---

## 8. Activation checklist (when someone takes Tier 1 live)

1. Register `withAndroidAutoMessaging` in `mobile/app.json` `plugins`.
2. Add a native MessagingStyle/CarExtender notification builder (small native
   module or mod) — the `expo-notifications` path is not Auto-readable on its
   own (see §7.1).
3. `expo prebuild --platform android --clean` → restore force-tracked overlays
   → gradle bundleRelease (~28 min cold).
4. Verify with Android Auto **DHU** (Desktop Head Unit) before any real car.
5. CarPlay Tier 2 waits on Apple's entitlement decision, then simulator +
   vehicle-style voice-template testing before it is enabled in the iOS binary.

Until step 1, every existing TestFlight/Play build stays green — the plugin is
inert and the lib no-ops when no car surface is present.
