# Vibe from Tasks — talk to build, load the app, keep vibing

Status: **core shipped (voice loop + load-app-by-voice + checklist UI)**, rest is
roadmap · 2026-07-19

> Docs drift; code is truth (see `CLAUDE.md`). Where this disagrees with
> `mobile/app/vibe.tsx`, `mobile/src/lib/voice/*`, the code wins — fix the doc in
> the same change.

## Design principle — Yaver is a remote AI runner; keep the mobile UI tiny

Yaver's job, on **every** surface, is to be a **remote AI runner + build
system** you drive by voice while you *watch the result*. The mobile app is not
a control panel with hundreds of screens — it is a thin lens on that loop:

> **talk → runner builds on your box → Hermes/WebRTC-load the result →
> feel it via the feedback SDK → talk again.**

Concretely, the mobile surfaces should collapse toward:

1. **Tasks (mostly STT/TTS)** — the home of the app. You describe work; it runs
   on the box; output comes back **summarized and clear** (checklist cards, one
   line per turn), never a raw log/diff dump.
2. **Load & feel** — Hermes / WebRTC load + the feedback SDK reload, by voice.
   The visual experience of the running app is the point; make it perfect.
3. **Optional extras, only when asked** — recap videos of what happened while you
   were away; MCP tool support; deploy/CI status. These are opt-in, not
   permanent chrome.

Everything else in `mobile/app/` is a candidate for removal or for hiding behind
"More". When adding a mobile screen, ask: *does this serve the runner loop?* If
not, it probably shouldn't be a top-level surface. This principle holds for
phone, tablet, watch, TV, car, and glass/AR-VR alike — one simple loop, many
lenses.

## The problem

Texting a coding agent from a phone is a terrible "vibing" experience: there's
no screen room, and typing kills flow. The wedge is **voice**: you just talk.
Describe a change, it runs on your box; say *"load me the app with Hermes"* and
the running thing appears in the Yaver container (with the feedback overlay); you
poke at it, tap **Back to Yaver**, and keep talking. No submit button, no
keyboard.

This is the phone/tablet sibling of the CarPlay hands-free loop — same
`VoiceConversationCore`, one new interceptor and one new surface screen.

## What ships today

| Piece | Where |
|---|---|
| Voice-first surface ("Vibe") | `mobile/app/vibe.tsx` — surface `"phone"` |
| Entry points | Tasks-tab mic FAB (`app/(tabs)/tasks.tsx`), `More → Vibe` (`app/(tabs)/more.tsx`) |
| Hands-free loop | shared `VoiceConversationCore` via `useHandsFreeVoice` (see `VOICE_CONVERSATION.md`) |
| Coding dispatch | `quicClient.runnerSessionTurn(activeDevice, …)` → LIVE claude/codex session on the box |
| "load me the app …" | `mobile/src/lib/voice/loadAppIntent.ts` + `loadAppInterceptor` → `openAppBus.publish` + open Hot Reload tab |
| Machine switch by voice | "switch to my mac mini" → `selectDevice` (shared `machineSwitchInterceptor`) |
| Risk gate | deploy/push/delete/force → spoken confirm (shared `carRiskPolicy`) |
| Summarized checklist UI | `vibe.tsx` turn cards — status glyph + instruction + one-line result, Codex/Claude-Code style (never a raw transcript dump) |

### Load-app-by-voice — the intent

`classifyLoadApp()` (pure TS, tsx-tested in `loadAppIntent.test.ts`) recognises,
conservatively, when a spoken thought is "bring the app up in here" vs. a coding
instruction that must go to the runner:

- **Triggers:** `load` / `reload` / `render` / `launch` (strong), and
  `show` / `open` / `run` next to an explicit *"app"* (soft). `download` /
  `upload` deliberately do **not** trigger (no word boundary before `load`).
- **Guard:** only fires with an explicit *"app"*, a mode word, `"… it/this/that"`
  after a strong verb, `"… here"`, or `"load me …"`. So *"load the config file"*,
  *"render the login screen"*, *"run the tests"* all still go to the runner.
- **Mode:** `hermes` / `webrtc` / `native` / `auto`. `auto` lets the existing
  Hot Reload flow (`apps.tsx::handleTapProject`) pick per framework — Hermes for
  RN/Expo, `native-webrtc` for native, dev-server for web.
- **App name:** everything left after scaffolding/mode words. Unspecified →
  `""` → open the picker.

The interceptor's side-effect mirrors the tab layout's proven `open_app` path:
navigate to `/(tabs)/apps` and `openAppBus.publish(name)`, which replays the same
`handleTapProject` a manual tap would. The feedback SDK / feedback overlay comes
for free — the Yaver container owns shake/feedback when a guest app is loaded
inside it (see `CLAUDE.md` "Suppress-when-inside-Yaver").

## The north star (roadmap — NOT yet built)

The product intent, captured so it isn't lost. Each of these is its own epic;
build behind the shared voice loop, keep the checklist UI honest about
progress/failure.

### 1. Remote autorun / goal from voice, with live status

A big ask — *"add auth, wire the settings screen, and write tests"* — should be
dispatchable as a long-running **autorun/goal** on the box (the user already
drives this with the `autorun` / `goal` keywords), not just a single session
turn. The vibe surface should:

- Kick off the remote loop (`runner_autorun` / the goal Stop-hook path) and show
  it as a **live checklist row** that updates as sub-tasks land — the same
  summarized card model, streamed.
- Keep the user updated by voice ("still working — 3 of 5 done") without reading
  code/diffs aloud (reuse the car readback guard).
- Seams: `desktop/agent/runner_autorun*`, autorun run rows, the Epic-7 tmux
  observability already surfaced on autorun cards.

### 2. Protected build — Hermes-load a *last-good* build while autorun churns

The concurrency hazard: an autorun is rewriting the working tree while the user
wants to Hermes-load and vibe on a **working** app. Loading the dirty in-progress
tree would hand them a broken build.

Design: **load from a protected checkpoint, not the live tree.**
- Reuse `managed_git_checkpoint` / `managed_git_backup` (`desktop/agent/managed_git*`)
  and the CLAUDE.md rule that autorun gets its **own clone/branch/worktree**.
- `/dev/build-native` (Hermes) and the `native-webrtc` path build from the
  last-green commit / a dedicated `vibe-preview` worktree, so the tested bundle
  is isolated from the autorun's uncommitted edits.
- Voice: *"load the last working build"* vs. *"load what you have now"* — an
  explicit `LoadMode`-adjacent selector (protected vs. live).

### 3. Concurrent feedback-SDK vibing while autorun runs

The user tests the protected build via the feedback SDK (Hermes **or** WebRTC)
and files feedback, **while** the remote autorun keeps landing features. Feedback
items flow back as new tasks/goals for the box. Seams: `sdk/feedback/*`,
`desktop/agent/feedback*.go`, `blackbox*.go`, the viewer-triggered
`launch-feedback` path for native surfaces.

### 4. Voice-driven deploys across every target, with progress/failure UI

Intent to ship — *"deploy the backend"*, *"push a TestFlight build"* — routed to
the right target with a **clear progress/failure/ capability UI** in the vibe
checklist (and mirrored in MCP chat): Convex, Cloudflare (`web/`), TestFlight,
Google Play internal, tvOS, CarPlay, watchOS, AR/VR. All are hard-gated by the
risk policy (deploy = confirm). Respect CLAUDE.md deploy rules: local-first,
coalesce (one deploy per converged change), quota-aware (TestFlight ~15–20/day),
never deploy to "check". Seams: the `scripts/deploy-*.sh` table in `CLAUDE.md`,
`deploy_script_gen.go`, `publish_*` / `platform_*` ops verbs.

### 5. Pragmatic CI status (GitHub / GitLab)

Show CI/pipeline status inline and let voice ask for it (*"how's CI?"*) — reuse
`git_ci_status` (already an interceptor verb) and `gh_run` / `gitlab_pipelines`.
Failures render with the failing job + a one-line cause, not a log dump.

## Cross-surface parity

The voice loop is shared, so a fix here reaches car/watch/TV/glass/VR for free
(RN surfaces) — but native surfaces (tvOS/watchOS/web) need explicit ports (see
`CLAUDE.md` "Cross-surface parity"). The `loadAppInterceptor` is phone-gated via
`createVoiceCore({onLoadApp})`; other surfaces opt in by wiring their own
`onLoadApp`.

## Verification

- `npx tsx mobile/src/lib/voice/loadAppIntent.test.ts` — 26 assertions (positive
  load/render/launch, negative coding instructions, mode + name extraction).
- On-device: `yaver wireless push` from repo root, open **Tasks → mic FAB**,
  talk; say *"load me the app with Hermes"*.
