# Yaver Dogfood — Screenshot-Driven Self-Improvement Loop

**Status:** design-only, 2026-06-07
**One-liner:** A WhatsApp/Tasks-style thread inside the Yaver mobile app where
**any screenshot you take is auto-caught**, you annotate it Instagram-style and
add a prompt, and it dispatches to a coding agent on your remote dev box that
edits Yaver itself — then Hermes-reloads Yaver so you see the fix in the same
thread. "Use Yaver to improve Yaver," screenshot-first.

> Source-of-truth caveat (per CLAUDE.md): every file path below was grepped on
> 2026-06-07. Re-verify before building — other threads move constants.

---

## 1. What this is (and is NOT)

This is **not** the Hermes hot-reload feature (`apps.tsx` / `/dev/build-native`
/ shake-to-reload). That flow loads *guest* apps and is the *last 5%* of this
loop (the "see the fix" step). It reuses that machinery but is not it.

This **is** a feedback channel pointed inward at Yaver, shaped like a chat:

```
 ┌─ you, anywhere in the Yaver app ─┐
 │  take a screenshot (vol+power)   │   ← the SPECIAL part: auto-caught
 └──────────────┬───────────────────┘
                ▼
   screenshot bubble appears in the Dogfood thread
                ▼
   annotate it (draw / arrow / text / blur)   ← Instagram-style
                ▼
   type a prompt / comments                    ← "make this tab bar taller"
                ▼
   Send → coding agent on remote dev box (create_task + image + repoDir)
                ▼
   agent edits yaver.io mobile code on the box
                ▼
   JS-only change → mobile_hermes_reload → Yaver reloads itself
                ▼
   thread shows "✓ reloaded" — you pull, screenshot again, repeat
```

The mental model is **WhatsApp with a camera roll that fills itself**, where the
other end of the conversation is a coding agent working on Yaver's own repo.

---

## 2. What already exists (reuse map)

Grepped, real, load-bearing:

| Piece | File | State | Role in this loop |
|---|---|---|---|
| Dogfood SDK shim | `mobile/src/lib/yaver-dogfood.ts` | scaffolded, `__DEV__`-gated | config + session + `sendVoiceCommand`→`POST /tasks`. **Extend** (drop `__DEV__` gate). |
| Feedback capture | `mobile/src/lib/feedback.ts` | `captureScreenshot()` is a **stub** | replace stub with real native capture. |
| Contributor dogfood config | `mobile/app/(tabs)/settings.tsx:275` | live | `dogfoodRepoDir` + `dogfoodPrompt` + `openDogfoodTask("code"\|"hermes")`. **Becomes the config backing the new thread.** |
| Screen recorder native module | `mobile/ios/Yaver/YaverScreenRecorder.swift` (`@objc(ScreenRecorder)`) + Android `YaverScreenRecorder*` | live | RPScreenRecorder; reused for optional clips, not for the still. |
| Feedback upload transport | `feedback.ts::uploadFeedback` → `POST /feedback` (multipart, `screenshot_N`) | live | transport for the image to the dev box. |
| Task dispatch | agent `create_task` MCP + `POST /tasks`; mobile Tasks tab opens with `{dir,prompt,runner,openNew}` | live | dispatch the annotated screenshot + prompt. |
| Hermes reload | agent `mobile_hermes_reload` (→ `POST /dev/reload`, returns `changeClass`), `mobile_hermes_doctor`, `mobile_project_build` | live | close the loop. |
| Self-reload bridge swap | `mobile/ios/Yaver/AppDelegate.swift::safeReloadBridge` + `YaverBundleLoaderReload` notification | live | Yaver reloading its *own* JS. |
| Remote dev resolve | `desktop/agent/code_cmd.go::resolveCodeAttachDevice`, `exec_cmd.go::resolveDeviceURL` (relay `/d/<id>` → direct `:18080`) | live | pick + reach the dev box. |
| Annotation deps | `react-native-svg`, `react-native-view-shot`, `expo-image-manipulator`, `@shopify/react-native-skia`, `react-native-gesture-handler` | installed | Instagram-style markup, zero new deps. |

**Net new code is small**: one native screenshot-catch module, one thread
screen, one annotation editor, and rewiring the gate. Everything downstream
(upload, task, reload) already runs in prod.

---

## 3. The special part — auto-catching the screenshot

The OS does **not** hand your app the screenshot bitmap. Two ways to get one:

### Option A — render the key window on the screenshot event (RECOMMENDED)
- **iOS:** register for `UIApplication.userDidTakeScreenshotNotification`. On
  fire, render the key window to a `UIImage`
  (`UIGraphicsImageRenderer` + `drawViewHierarchy(in:afterScreenUpdates:false)`),
  JPEG → cache dir, emit an RN event.
- **Android:** API 34+ has `Activity.registerScreenCaptureCallback` — fires when
  the user screenshots. On fire, render `window.decorView` → `Bitmap` → file →
  event. Below 34, fall back to a `ContentObserver` on `MediaStore.Images` (new
  `IS_SCREENSHOT`/`screenshots/` row) and re-render the decorView.
- **Why this wins:** **no Photos permission** (`expo-media-library` isn't even
  installed — confirms we don't want that surface), instant, captures the exact
  app UI, and lets us run a **redaction pass** before anything leaves the device.
- **Caveat:** window-render misses content drawn off the RN view tree (camera
  preview, DRM video). Irrelevant for dogfooding Yaver's own UI.

### Option B — fetch the real screenshot from Photos
- Observe `PHPhotoLibrary` for new `PHAssetMediaSubtype.photoScreenshot`, fetch
  the latest. Gets the literal OS screenshot (status bar, exact pixels) but needs
  Photos permission and a new dep. **Rejected** unless we later want pixel-exact.

### New native module: `YaverDogfood`
Cross-platform contract (iOS Swift+ObjC bridge + Android Kotlin), mirrors how the
other `Yaver*` overlays are force-tracked into `pbxproj` (see CLAUDE.md
"Force-tracked iOS overlay files"):

```ts
NativeModules.YaverDogfood.start()   // begin listening for screenshots
NativeModules.YaverDogfood.stop()
// emits DeviceEventEmitter "onDogfoodScreenshot":
//   { path: string, takenAt: number, route?: string, appVersion?: string }
```

`route` is the current expo-router path (so the prompt can say *which screen*),
captured from a tiny JS-side route ref the module reads back, or passed at
`start()` and updated on navigation.

**Suppress-when-guest:** like `YaverFeedback`/`ShakeDetector`, this must no-op
when a *third-party* RN app is loaded inside the container (`YaverInfo.isYaver`).
Dogfood is for the host app only.

---

## 4. The thread UI (WhatsApp / Tasks shaped)

New screen `mobile/app/(tabs)/dogfood.tsx`, reached from a **More**-tab card
("🐕 Dogfood Yaver — improve Yaver with screenshots") and/or a dedicated tab when
dogfood mode is on. Layout follows `more.tsx`'s `Pressable` card idiom and the
Tasks/terminal thread patterns already in the app.

```
┌────────────────────────────────────────┐
│ Dogfood Yaver           [● on]  ⚙       │  enable toggle + config
│ Target: dev-box-mini · repo: yaver.io   │  remote dev box + repoDir
├────────────────────────────────────────┤
│                                         │
│  [screenshot]  caught · Settings screen │  ← auto-caught bubble
│   "tab bar too short on tablet"         │     caption
│   ↳ comment: "and the chevron is off"   │     Instagram-style comments
│   [✎ annotate]            [Send →]      │
│                                         │
│  ──────── agent · dev-box-mini ──────── │
│  ✓ task #a3f started (claude-code)      │  agent status as messages
│  ✎ edited app/(tabs)/_layout.tsx        │
│  ✓ Hermes reload · js_only · reloaded   │  loop closed
│                                         │
└────────────────────────────────────────┤
│ [＋ screenshot]   type a prompt…   [↑]  │  manual add + free prompt
└────────────────────────────────────────┘
```

- **Each thread item** = `{ id, imagePath, annotatedPath?, caption, comments[],
  route, status, taskId?, changeClass? }`. `status`:
  `draft → sent → working → reloaded | needs-native | failed`.
- **Comments like Instagram:** an item accretes multiple text comments before you
  send (pile up context), and the agent's progress/results land as comments on
  the same item — so a screenshot + its fix live in one bubble.
- **"then I will pass prompt":** caption + comments compose the task prompt. A
  fixed preamble is prepended (see §6). Nothing is sent until you tap **Send**
  (review-first, matches your "then I will pass prompt" flow — no auto-dispatch).
- **Manual add** (`＋`): pull an existing image via `expo-image-picker` for cases
  where you screenshotted before enabling, or want a mockup.

### The More item = history + live agentic session (Tasks-shaped)
The Dogfood entry under **More** is a full surface, not a launcher:
- **History feed:** every screenshot taken, its notes/caption, and what was
  submitted (with mode badge: PR / Vibe) — your dogfood log over time.
- **Per-item agentic session:** tapping a submitted item opens a **Tasks-tab-style
  live view** — the agent's streaming output, file edits, tool calls, and a
  **follow-up composer** so you can keep talking to the agent ("no, the *other*
  chevron"), approve steps, or stop it. This reuses the existing Tasks
  infrastructure (`/tasks` stream, `continue_task`, stdin) rather than a new
  engine — the dogfood item just carries the `taskId` and renders the same
  thread. In **PR mode** the session view instead shows PR status (branch,
  checks, link to GitHub) and lets you comment.
- **Loop status inline:** "✓ reloaded · js_only" (Vibe) or "PR #123 open" (PR)
  shows on the item, so history doubles as a changelog of what you fixed.

### Voice — STT/TTS throughout
Reuse the existing speech stack (settings `speechProvider`/`sttModel`/`ttsModel`,
on-device whisper.rn or Deepgram Flux, expo-speech / Cartesia — see
`useVoiceHelper`):
- **STT for notes/prompt:** hold-to-talk on the annotate modal and the follow-up
  composer — say "make this tab bar taller" instead of typing. Honors the user's
  configured provider (on-device = private, flux = fast).
- **TTS for agent replies:** the agentic session can read the agent's
  summaries/questions aloud, so you can dogfood hands-busy (phone in one hand,
  shaking the UI with the other). Opt-in toggle, mirrors the Tasks tab's TTS mode.

### Annotation editor (Instagram-style)
Full-screen modal over the image, zero new deps:
- **Pen / freehand:** Skia or `react-native-svg` path from `gesture-handler` pan.
- **Arrow + text labels:** draggable svg primitives.
- **Crop:** `expo-image-manipulator`.
- **Blur / redact:** rectangle → `expo-image-manipulator` blur region (privacy).
- **Flatten:** `react-native-view-shot` captures the composited view → JPEG →
  `annotatedPath`. That flattened image is what ships to the agent.

---

## 4.5. Two modes (PR vs Vibe)

The same captured-and-annotated screenshot + prompt dispatches one of two ways.
The thread header shows a mode switch; the default is auto-picked (see below).

### Mode A — **PR** (no Yaver source on any of your machines)
For users who want to improve Yaver but don't run the Yaver repo locally. The
screenshot + prompt becomes a **GitHub PR against `kivanccakmak/yaver.io`** that
they review/merge in the browser — Instagram-comment-on-a-screenshot → PR.

Dispatch target is a **Yaver-source agent** that already has the repo + `gh`
auth, in priority order:
1. A registered device of theirs that *does* have `yaver.io` checked out
   (detected via the mobile-project scan — if found, prefer Vibe instead).
2. An **ephemeral cloud box** spun from the existing remote-provision / GPU-rental
   infra (`remote_provision`, `cloud_*`), which `git clone`s yaver.io, runs the
   coding agent headless, pushes a branch, opens a PR. Torn down after.
3. Fallback when neither is available: **file a GitHub issue** with the
   screenshot attached + prompt (via `github_issue_create`), so a maintainer /
   CI agent picks it up later. No code change, but the loop isn't dropped.

Reuse: agent already has `github_pr_create`, `github_issue_create`, git tools,
`create_task`. The PR body embeds the annotated screenshot (uploaded to the
repo/PR) + the prompt + route. **No Hermes reload** in this mode — the user sees
the change when the PR is built/merged, not live.

> This is the only place GitHub-cloud execution is first-class, and only because
> the user has *no* machine — consistent with CLAUDE.md's "local-first, CI as
> fallback." Users with a box always get Vibe.

### Mode B — **Vibe** (you have a remote dev box with Yaver source)
The live loop: dispatch to *your* box, agent edits the local `yaver.io` checkout,
then `mobile_hermes_reload` self-reloads Yaver so you see the change in seconds.
Optionally auto-commit to a branch; PR is opt-in, not forced. This is the
fast inner loop (§5–§6).

### Auto-pick
On Send, resolve: is there a reachable registered device whose mobile-project /
repo scan shows a `yaver.io` checkout? → **Vibe** (that box). Else → **PR**. The
user can override the switch per-item (e.g. force a PR even on their own box when
they want review).

---

## 4.6. Dogfood mode = persistent state + silent recording + batch

**Dogfood mode is a sticky toggle** (per-user AsyncStorage flag), not a one-shot.
While ON, three things are true:

### Silent background recording — *Yaver context only*
While enabled, Yaver keeps a **lightweight rolling breadcrumb buffer** of what you
do *inside Yaver* so a screenshot arrives with context, not naked. This is **not**
continuous video (battery + privacy) — it's an in-memory ring buffer (~last 60s /
last N events) of:
- route changes (expo-router path in/out),
- coarse interaction breadcrumbs (tab switch, sheet open, primary button taps),
- recent in-app errors / toasts (reuse the existing log/blackbox feed).

When a screenshot is caught, the current buffer is snapshotted onto that item, so
the dispatched prompt can say *"user was on Settings → tapped Devices → opened
DeviceDetails, then screenshotted here."* Pure JS (expo-router listener + a tiny
recorder), **no native change** beyond `setDogfoodRoute` (already in P0).

**"Only Yaver context"** is enforced three ways:
- **Host-only:** pause the recorder + native capture when a *guest* bundle is
  loaded (`YaverInfo.isYaver` / current module ≠ host), exactly like
  `ShakeDetector`/`YaverFeedback` suppress themselves inside guests.
- **Foreground-only:** pause on `AppState` background/inactive — nothing recorded
  while Yaver isn't on screen.
- **Local-only:** buffer lives in memory; only the snapshot attached to a sent
  item is persisted, and only ever P2P to your box / the PR — never Convex.

Optionally, a short **rolling screen clip** (reuse `YaverScreenRecorder`) can back
the breadcrumbs for "show me the bug happening," but that's opt-in (heavier) and
defaults off.

### Screenshot → annotation UI opens immediately
A caught screenshot doesn't just drop a bubble to find later — it **pops the
annotate/describe modal** (the Instagram editor + "which part should change?"
caption) right away, pre-filled with route + breadcrumbs. You mark it up, say
what's wrong, and either **Send now** or **Add to batch**.

### Batch
Two send strategies on the modal:
- **Send now** — dispatch this single item (PR or Vibe).
- **Add to batch** — keep using Yaver, shoot more, annotate each; the thread
  shows the pending batch. **Send batch** dispatches them as *one* request: in
  Vibe mode, a single `create_task` whose prompt enumerates each screenshot +
  its caption (all images written to `.yaver/dogfood/`); in PR mode, one PR that
  addresses the batch. Batching is how you report "these 5 things across the app"
  in one agent run instead of five round-trips.

---

## 5. Dispatch + reaching the dev box (Vibe mode)

**Target resolution** reuses `resolveCodeAttachDevice` / primary-device logic:
the thread header lets you pick *which* registered machine runs the agent
(default = primary). The phone talks to it via the same relay `/d/<deviceId>` →
direct `:18080` path the CLI uses.

**Transport (two hops, both already in prod):**
1. `POST /feedback` (multipart) uploads `annotatedPath` → agent stores it under
   the repo's gitignored `.yaver/dogfood/<id>.jpg`. Returns an id/path.
2. `create_task` / `POST /tasks` with `{ dir: dogfoodRepoDir, runner, prompt,
   title }`. The prompt **references the on-disk image path** so the vision-capable
   runner (claude-code) reads it directly from the workdir.

**Why write the image into the repo workdir:** runners can't receive image
attachments over the task API, but they *can* `Read` a file in their cwd. Putting
it at `.yaver/dogfood/<id>.jpg` (gitignored) lets the agent see exactly what you
saw.

---

## 6. The task preamble (what makes the agent edit Yaver correctly)

Prepended to every dogfood task, derived from the existing `dogfoodPrompt`
default in `settings.tsx`:

```
You are improving the Yaver mobile app itself (this repo). A screenshot of the
running Yaver UI is at .yaver/dogfood/<id>.jpg — open it. The user was on the
"<route>" screen. Make the change they describe.

Rules:
- Prefer JS/TS-only changes so Hermes hot-reload can apply them instantly.
- Mobile app must stay loadable in the Yaver container (no WebView for RN).
- If a native change is unavoidable, say so — it needs a wire/TestFlight build.
- Keep the diff small and match surrounding code style.

User request:
<caption>
<comments joined>
```

After the agent reports done, the thread calls `mobile_hermes_reload`:
- `changeClass: "js_only"` → Yaver self-reloads via the AppDelegate bridge swap;
  thread shows "✓ reloaded".
- `changeClass: "native_rebuild_required"` → thread shows "needs native build →
  `yaver wire push`" (can't Hermes-reload native deltas).

---

## 7. Gating, privacy, persistence

- **Gate on an explicit "Dogfood mode" toggle** (AsyncStorage flag, per-user
  key like the existing `@yaver/u/<id>/dogfood_yaver`), **not `__DEV__`**. The
  current `yaver-dogfood.ts` hard-returns on `!__DEV__` — that's wrong for this
  use case; you want it on a real TestFlight build on your own phone. `start()`
  the native listener only while the toggle is on.
- **Privacy contract (CLAUDE.md):** screenshots of your UI can contain data →
  **never to Convex**. Everything flows P2P (phone → your dev box) and persists
  **locally** (AsyncStorage thread + cache-dir images). The blur tool is the
  redaction escape hatch. No new forbidden fields enter `convexSyncer`.
- **Persistence:** thread state in AsyncStorage; images in
  `FileSystem.cacheDirectory + "dogfood/"`. Optionally mirror the thread to the
  dev box over P2P so it survives reinstall — still never Convex.

---

## 8. Build plan (phased, smallest-first)

- **P0 — capture spike (DONE 2026-06-07):** `YaverDogfood` iOS module
  (`mobile/ios/Yaver/YaverDogfood.swift` + `.m`, registered in pbxproj) renders
  the key window on `userDidTakeScreenshotNotification`, writes JPEG to
  `Caches/dogfood/`, emits `onDogfoodScreenshot`. JS subscriber
  `mobile/src/lib/dogfoodCapture.ts` (`start/stop/onDogfoodScreenshot/setRoute`).
  Needs `yaver wire push` (native build) to test on-device.
- **P0.5 — mode state + silent recorder:** sticky dogfood toggle (per-user
  AsyncStorage); `start/stop` the native capture on toggle; JS breadcrumb ring
  buffer (expo-router + AppState + isYaver gating); `setDogfoodRoute` on nav.
  Pure JS + the P0 module — no new native.
- **P1 — thread MVP + capture modal + mode switch:** `dogfood.tsx` under More;
  screenshot **pops the annotate/describe modal** (caption + route + breadcrumbs);
  **mode switch (PR / Vibe)** with auto-pick; **Send now / Add to batch / Send
  batch**. Vibe → `create_task` (§6 preamble); PR → `github_pr_create` (issue
  fallback). History feed of shots + notes + submissions.
- **P2 — agentic session view + Vibe loop close:** per-item Tasks-style live view
  (stream, follow-up composer, approve/stop) carrying `taskId`; `mobile_hermes_reload`
  after completion; surface `changeClass`; self-reload on `js_only`. PR-mode item
  shows PR status instead.
- **P3 — annotation + voice:** Instagram editor (pen/arrow/text/crop/blur, flatten
  via view-shot); **STT** on notes + follow-up, **TTS** on agent replies (reuse
  `useVoiceHelper`/speech settings).
- **P4 — PR depth + Android:** ephemeral cloud-box provisioner for no-machine PR
  mode; Android `registerScreenCaptureCallback` (34+) + `ContentObserver`
  fallback; dev-box picker; optional rolling screen clip; manual `＋` add.

P0 done. P0.5–P2 is the working Vibe loop with live agent interaction; P1 also
lands PR mode's happy path.

### Built 2026-06-07 (uncommitted) — P0 through P3

- **P0** native capture: `mobile/ios/Yaver/YaverDogfood.swift`+`.m` (pbxproj-registered),
  `mobile/src/lib/dogfoodCapture.ts`.
- **P0.5** sticky mode + breadcrumbs: `dogfoodMode.ts`, `dogfoodBreadcrumbs.ts`,
  wired in root `app/_layout.tsx` (`loadDogfoodMode` + `recordDogfoodRoute`).
- **P1** thread + capture modal + mode switch + batch:
  `app/(tabs)/dogfood.tsx` (toggle, config card, history feed, batch bar, manual
  add), `src/components/DogfoodCaptureHost.tsx` (global capture→modal, mounted in
  root), `src/lib/dogfoodConfig.ts` + `dogfoodThread.ts` (persist + dispatch via
  `client.sendTask` with the screenshot as an `ImageAttachment`), More-tab card +
  `dogfood` route registered.
- **P2** agentic session: inline `DogfoodSession` streams `quicClient.streamTaskOutput`
  + "Open in Tasks"; status → done flips item; PR vs Vibe result copy.
- **P3** annotation + voice: `src/components/DogfoodAnnotateModal.tsx` — pen draw
  (PanResponder + react-native-svg), color, undo, flatten via react-native-view-shot;
  caption with hold-to-talk STT (`startRealtimeTranscribe`); TTS read-back (`speakText`).

**Remaining:** P4 (ephemeral cloud box for no-machine PR mode; Android
`registerScreenCaptureCallback`). Needs `yaver wireless push` (native build) to
exercise the auto-catch on device.

---

## 9. Open decisions (need your call)

1. **Capture mechanism** — window-render (no perms, app-UI only) vs Photos fetch
   (pixel-exact, needs permission + dep). Recommend window-render.
2. **Annotation depth** — full draw/arrow/text/blur editor, or just caption +
   blur-redact to start? Affects P3 size.
3. **Where it lives** — dedicated bottom tab when dogfood is on, or a sub-screen
   under **More** only? Recommend More sub-screen (no tab-bar churn).
4. **Auto-send vs review-first** — your "then I will pass prompt" implies
   review-first (screenshot stages, you add prompt, then Send). Confirm.
