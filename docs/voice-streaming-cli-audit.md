# Audit + plan: bare-`yaver` shell, latest-greatest distribution, and
# STT/TTS-aware streaming across CLI / mobile / web

Status: **research/audit done — plan awaiting approval before any deploy.**
Date: 2026-05-30. Owner thread: yaver.io + CLI.

This is the "deep deep research audit + plan" requested before deploying the
mobile app and the Go agent. Nothing here ships until approved. Findings are
grounded in the code (file:line); the plan is phased and each phase is
independently shippable.

---

## 0. Distribution state (this PC vs npm vs main)

| Where | Version | Notes |
|---|---|---|
| This PC (`yaver --version`) | **1.99.221** | stale — behind npm |
| npm `yaver-cli@latest` | **1.99.238** | released; contains the psql shell + free voice stack |
| `versions.json` / main HEAD | 1.99.238 + **2 unreleased commits** | `bcd7f87f` (deploy-all version bump) + `bd69dc94` (iOS GCKeyCode fix) — NOT on npm yet |

So "latest greatest in npm" is **not** true today: main is ahead of npm by
the two commits we just landed. Making npm latest-greatest = cut
`cli/v1.99.239`. (Deferred to Phase 4 per "plan before deploy".)

Local update to 1.99.238 is running now (explicit ask). Note: **even 1.99.238
will still print help on bare `yaver`** because of the wrapper bug below — the
shell fix needs 1.99.239.

---

## 1. Why bare `yaver` shows help instead of the psql shell

The psql-style shell **exists and is released** (commit `c7e192de`, in
`cli/v1.99.238`):

- `desktop/agent/shell_repl.go` — `maybeRunYaverShell()` (l.33) →
  `runYaverShell()` (l.94). Gated on: `YAVER_NO_SHELL` unset **and** both
  stdin+stdout are TTYs (`term.IsTerminal`).
- `desktop/agent/main.go:339` — bare invocation (`len(os.Args) < 2`) calls
  `maybeRunYaverShell()`; falls back to `printUsage()` only if it returns
  false.

**The Go side is correct.** The bug is in the **npm JS wrapper**, which never
reaches the Go binary on a no-arg call:

```js
// cli/src/index.js:157
if (!command || command === '--help' || command === '-h' || command === 'help') {
  console.log(UNIFIED_HELP);   // ← bare `yaver` prints JS help and exits
  process.exit(0);
}
```

`!command` (bare `yaver`) is lumped in with explicit `--help`, so the wrapper
prints `UNIFIED_HELP` and `process.exit(0)` **before** spawning the agent.
`maybeRunYaverShell()` never runs.

**Fix (Phase 1, tiny):** split the no-arg case from the help case — bare
`yaver` hands off to the Go agent so the TTY shell can launch:

```js
if (command === '--help' || command === '-h' || command === 'help') {
  console.log(UNIFIED_HELP);
  process.exit(0);
}
if (!command) {
  await runAgentCommand([]);   // Go agent: TTY → psql shell, else usage
  return;
}
```

`runAgentCommand` already sets `YAVER_NO_SHELL` when it re-execs for
sub-commands, so there's no recursion risk for the bare hand-off (it passes no
sub-command). Verify the spawn inherits the TTY (stdio: 'inherit').

---

## 2. STT/TTS inventory (what exists, per surface)

| Surface | Local STT | Local TTS | Config source |
|---|---|---|---|
| **Go agent / CLI** | ✅ `whisper.cpp` — `voice_stt_local.go`, provider `"local"`; needs binary **+** model (`~/.yaver/models/ggml-*.bin`) | ✅ host `say`/`espeak`, provider `"local"` | `~/.yaver/config.json` `VoiceConfig` |
| **Mobile (iOS/Android)** | ✅ `whisper.rn`, bundled ggml-tiny (~31MB), provider `"on-device"` — `mobile/src/lib/speech.ts:70` | ✅ `expo-speech` (AVSpeech/Android TTS), provider `"device"` | reads agent `/voice/status` |
| **Web** | ⚠️ label only (`"on-device (Whisper)"`) — no real local transcription | ⚠️ browser API / device | Convex `userSettings` |

- **Free voice stack auto-provision** (commit `05d95f55`): `cli/src/postinstall.js:522`
  runs `voice deps --install --quiet` → ffmpeg + whisper.cpp + `ggml-base.en.bin`
  (~78MB) to `~/.yaver/models/`. Opt-out `YAVER_SKIP_POSTINSTALL_VOICE=1`.
- **Web Preferences** (commit `72f539f1`): `web/.../PreferencesView.tsx` —
  `speechProvider` default `"on-device"`, plus `ttsEnabled`/`ttsProvider`,
  POSTed to Convex `/settings`.

**This PC right now:** whisper-cli ✅, ffmpeg ✅, `say` ✅, **model ❌ (no
`~/.yaver/models/`)**. STT won't work until the model lands — the running
update's postinstall should fetch it; otherwise `yaver voice deps --install`.

---

## 3. The core gap: the agent is NOT aware of the user's STT/TTS state

> "go agent should be aware that end user is using stt/tts both or none for
> streaming messages through mobile/web."

Today there is **no propagation**:

- Web stores `speechProvider`/`ttsProvider` in **Convex**; the agent reads its
  own `~/.yaver/config.json` only (`config.go` `VoiceConfig`,
  `EffectiveSTTProvider`/`EffectiveTTSProvider`). The agent reads Convex
  `userSettings` for `primaryDeviceId`/`runnerId` only (`primary_cmd.go`) —
  **no voice fields**.
- Mobile reads the agent's `/voice/status` at session start but never tells the
  agent what *it* supports.
- Result: when the agent streams a message, it has **no idea** whether the
  consuming client will read it aloud (TTS) or expects voice input (STT), or
  whether the user is on a CLI with neither.

### How streaming + client identity work today (fragmented)

- **Task output**: SSE `GET /tasks/{id}/output` (`httpserver.go:3588`), events
  `{"type":"output","text":...}`, plus structured `command_*` events
  (`command_events.go`). **No surface awareness.**
- **Mobile bus**: `/blackbox/stream` + `/blackbox/command-stream`
  (`blackbox_http.go`) identify the client via `X-Device-ID` / `X-Platform` /
  `X-App-Name` — but only inside the BlackBox session, not the task.
- **Voice**: `WS /voice/stream` + `GET /voice/status` (`voice_http.go`). The WS
  client-hello already carries `surface` (`mobile-phone`/`web-desktop`/
  `glasses-*`/`cli`/…) and `ttsBudget`. `TaskViewport` (`tasks.go:715`) already
  stores `Surface`, `Voice`, `TTSBudget` — but only when voice initiates a task.

So the **building blocks exist** (`TaskViewport.Surface`, `/voice/status`,
WS client-hello), but there is **no unified client-capability handshake** and
no CLI-vs-mobile/web discrimination at the message level.

---

## 4. Plan

### Phase 1 — bare-`yaver` psql shell fix (CLI only, low risk)
- Fix `cli/src/index.js:157` per §1 (split no-arg from `--help`).
- Add a CLI test asserting bare invocation hands off to the agent (not JS
  help). Manual: `yaver` in a TTY → shell; `yaver | cat` (non-TTY) → usage;
  `yaver --help` → JS help unchanged.
- Ships in the 1.99.239 release (Phase 4).

### Phase 2 — unify client surface + capability handshake (Go agent)
Make every client declare *who it is* and *what it supports*, once, with a
backward-compatible default of "CLI, no voice".
- **Client hello header convention** (all HTTP/SSE entrypoints):
  `X-Yaver-Surface: cli|mobile-phone|mobile-tablet|web-desktop|web-spatial-vr|glasses-*`
  and `X-Yaver-Voice: stt,tts | stt | tts | none`. Default when absent: `cli` /
  `none` (preserves today's behavior).
- Thread the parsed `{surface, sttEnabled, ttsEnabled}` into the existing
  `TaskViewport` for **all** task-creating paths (not just voice) — `tasks.go`.
- Extend `/voice/status` into a general `GET /client/capabilities` the agent
  can also *report* (what providers are ready locally) so clients and agent
  agree.

### Phase 3 — STT/TTS-aware streaming (Go agent + clients)
With the viewport carrying voice state, make streaming adapt — additively, so
old clients ignore new fields:
- New optional task-stream events (`command_events.go`):
  - `{"type":"tts-hint","text":"…","budget":N}` — "this chunk is meant to be
    spoken" (only emitted when `viewport.ttsEnabled`).
  - `{"type":"stt-prompt","text":"…"}` — "expecting voice input here" (only
    when `viewport.sttEnabled`).
- **Discrimination rule** (the explicit ask): CLI (`surface=cli`, voice=none)
  → plain text stream, no voice events, no spoken summaries. mobile/web with
  `tts` → agent may emit `tts-hint` and keep prose short/voice-friendly within
  `ttsBudget`. `stt` → agent may end turns with an explicit `stt-prompt`.
  none-but-mobile → normal text, no voice events.
- **Web↔agent voice-pref sync**: agent reads `speechProvider`/`ttsProvider`
  from Convex `userSettings` (extend `primary_cmd.go`'s existing read) so a
  web Preferences change reaches the agent; precedence:
  per-session client-hello > Convex userSettings > local `config.json`.

### Phase 4 — distribution (the actual deploy, after approval)
- Bump cli → **1.99.239**, ship via `cli/v1.99.239` tag (release-cli.yml
  builds the signed per-platform binaries + publishes npm) — per
  `project_cli_deploy_must_use_tag` (raw `npm publish` would ship a wrapper
  whose postinstall fetches a non-existent binary).
- This release carries: Phase 1 shell fix, the deploy-all version-bump feature
  (already on main), the GCKeyCode iOS fix, and whatever of Phases 2–3 is ready.
- Then this PC's `npm install -g yaver-cli@latest` is genuinely latest-greatest
  **and** bare `yaver` launches the shell.
- Mobile app: the iOS fix already shipped to TestFlight (build 356). A mobile
  release for Phase 2–3 client-hello/voice-event support is a separate
  `mobile/v*` tag once those land.

### Sequencing / what to deploy when
1. Phase 1 + the two already-landed commits → **1.99.239** (small, unblocks
   the shell + ships the iOS fix to npm-built agents). Lowest risk; do first.
2. Phase 2 (handshake) → 1.99.240 + a mobile build that sends the headers.
3. Phase 3 (aware streaming + web sync) → 1.99.241 + mobile + web + a Convex
   deploy (new userSettings voice fields read path).

---

## 5. Guardrails (carried from existing rules)
- **No headless runners** (`-p`) anywhere — voice/STT must not change that
  (`feedback_no_headless_p_mode`). The agent stays interactive TUI.
- **Privacy**: STT transcripts, audio, TTS text are task I/O — **forbidden in
  Convex** (`convex_privacy_test.go`). Only the *preference* (`speechProvider`
  enum, `ttsEnabled` bool) lives in Convex userSettings; never the content.
- **Subscription-only** runner auth unchanged; voice providers are a separate
  concern (local Whisper is free/offline; cloud STT keys live in vault).
- **No secrets in tracked files**; cloud STT/TTS keys via `yaver vault`.
- CLI releases **must** go through the `cli/v*` tag, never raw `npm publish`.

---

## 6. Open questions for the user
- Voice-event protocol: additive event types on the **existing** task SSE
  stream (Phase 3 above), or a dedicated voice channel only? (Plan assumes
  additive + backward-compatible.)
- For CLI usage, should the agent ever speak via local `say` (opt-in
  `--voice`), or is CLI always silent text? (Plan assumes silent unless
  `--voice`.)
- Precedence if web says "tts on" but the connected mobile client's hello says
  `voice=none`: plan favors the **per-session client-hello** (the device
  actually rendering) over the stored web preference — confirm.
