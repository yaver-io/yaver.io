# Yaver on Apple TV — what exists, what cannot exist, what to build

Handoff brief. Everything marked VERIFIED was checked against the running system
or Apple's tvOS 26.2 SDK on 2026-07-10, not recalled from memory. Anything
uncertain is labelled UNVERIFIED — check it before you rely on it.

Target reader: whoever picks up the tvOS surface next.

---

## 1. Platform constraints — read this before designing anything

Three of these kill obvious ideas. Find out here, not after a week of work.

### 1.1 tvOS apps cannot record audio. At all. (VERIFIED)

```
$SDK/System/Library/Frameworks/Speech.framework   → No such file or directory

AVAudioSession.h:120  recordPermission          … API_UNAVAILABLE(macos, tvos)
AVAudioSession.h:130  requestRecordPermission:  … API_UNAVAILABLE(macos, tvos)
```

No `SFSpeechRecognizer`. No microphone permission. The Siri Remote's mic is
**system-owned**; no app on any Apple TV can open it.

Consequence: a "hold to talk" STT loop is impossible. Do not plan one.

### 1.2 The only dictation path is the system keyboard (VERIFIED by absence above)

Focus a `TextField`; the user presses the mic button on the Siri Remote; tvOS
inserts dictated text. It is an OS affordance granted to any text field, not an
API you call. This is the *only* Siri-powered speech input available, and it is
enough: two D-pad presses, then talk.

### 1.3 TTS works (VERIFIED)

```
$SDK/System/Library/Frameworks/AVFAudio.framework/Headers/AVSpeechSynthesis.h
  @interface AVSpeechSynthesizer
```

`AVSpeechSynthesizer` is present. Yaver can speak replies aloud.

### 1.4 tvOS has no WebKit (VERIFIED)

```
$SDK/System/Library/Frameworks/WebKit.framework → No such file or directory
```

No `WKWebView`. Two consequences:

- The phone's terminal **cannot be ported**. `mobile/app/shell.tsx:32` imports `XtermView`, which renders
  TUIs through xterm.js inside a WebView. That approach is dead on
  tvOS.
- The project's web UI **cannot be rendered on the TV**. Ever. Only streamed as
  pixels from elsewhere.

### 1.5 You do not need a terminal emulator anyway (VERIFIED)

`tmux capture-pane -p` returns plain text with escape sequences already
stripped. Every pane snapshot taken while building `/runner/session/turn` came
back clean. The `pane` field renders in a SwiftUI `Text` with a monospaced font.

This is the single most useful fact in this document.

### 1.6 AVPlayer plays HLS and MP4, not MJPEG (VERIFIED: AVKit, AVFoundation,
VideoToolbox, CoreMedia all present in the SDK; MJPEG is not an AVPlayer format)

The agent's `/rd/stream` and `/capture/stream` are MJPEG. `AVPlayer` will not
play them. Two working routes:

- **JPEG snapshot poll.** Already proven in the app: `AppleTVRemoteView` polls
  `/capture/frame.jpg`. ~5–10 fps, trivial, works today.
- **HLS.** The agent already embeds ffmpeg for capture; emitting HLS is a small
  step and `AVPlayer` plays it natively. `vibe_preview_clip_record` already
  produces MP4 — serve the file and `AVPlayer` plays it with no new plumbing.

### 1.7 Transport: the TV can only reach LAN boxes (VERIFIED)

```
tvos/YaverTV/Info.plist → NSAppTransportSecurity = { NSAllowsLocalNetworking: true }
```

Cleartext to a **public** IP is blocked by ATS. The agent's HTTPS port (18443)
is self-signed, which ATS also rejects. So the TV can drive a Mac on the same
Wi-Fi, and **cannot** drive a remote box (e.g. a Hetzner agent) as built.

This is an architectural decision, not a config tweak. See §5.

### 1.8 Siri custom intents on tvOS (UNVERIFIED)

`AppIntents.framework` and `Intents.framework` are both present in the tvOS SDK.
That does **not** establish that a custom App Intent is invocable by voice on
tvOS — there is no Shortcuts app on tvOS, and Siri on Apple TV historically
serves media domains only. **Verify before promising "Hey Siri, ask Yaver…".**
The dictation path in §1.2 needs none of this and should be the default plan.

---

## 2. What exists today

### 2.1 The tvOS app is real (~1,340 lines of SwiftUI, no stubs)

`tvos/YaverTV.xcodeproj`, single target, bundle id `io.yaver.mobile`
(shared with iOS — tvOS rides the same App Store record).

| File | What it does |
|---|---|
| `YaverTVApp.swift:22-27` | Auth gate: `SignInView` until a token exists, then `DashboardView` |
| `SignInView.swift` | RFC 8628 device-code sign-in: QR + short code, polls Convex every 5s |
| `DashboardView.swift:19-38` | Tile launcher: Catalog, Runtime, Apple TV, Capture, change box, sign out |
| `DashboardView.swift:87-112` | `AddBoxView` — type a machine name + LAN IP |
| `RuntimeDashboardView.swift` | Status cards polled every 4s; Hot Reload / Hermes Push; **runner OAuth QR** (`:104-165`) |
| `AppleTVRemoteView.swift` | D-pad, transport, now-playing, capture-card frame view (polls every 3s) |
| `AgentClient.swift:52-63` | `POST http://<host>:18080/ops` with `{verb, payload, machine:"local"}` + bearer |
| `Backend.swift:15` | Convex origin, hardcoded, used **only** for device-code auth |
| `YaverStore.swift:11` | `@AppStorage("yaver.tv.token")` — the 1-year session token |

Verbs it already calls: `info`, `status`, `voice`, `runner` (op `agents_list`),
`mobile_platform_matrix`, `runner_auth` (browser_start / browser_status),
`reload`, `appletv_*`, `capture_status`.

### 2.2 The agent side of the voice loop shipped 2026-07-10

`POST /runner/session/turn` (`desktop/agent/runner_session_turn.go`):

```jsonc
// request
{ "session": "yaver-codex",   // or "runner": "codex", or neither if only one is live
  "text":    "keep developing this",   // a prompt   (mutually exclusive with choice)
  "choice":  "2",                      // a menu answer
  "waitMs":  6000 }

// response
{ "ok": true, "session": "yaver-codex", "runner": "codex",
  "sent": "prompt",              // or "choice"
  "awaitingChoice": false,
  "options": ["1. Yes, I trust this folder", "2. No, exit"],
  "pane": "…plain text, ANSI already stripped…" }
```

Guards, both learned by breaking a real box — **do not remove them**:

- A **prompt** into a pane showing a menu returns `409` + `options[]`. Its
  submitting Enter would otherwise pick whatever option is highlighted. A prompt
  sent while codex showed `1. Update now` ran `npm install` and killed the
  session.
- A **choice** when no menu is showing returns `409`. tmux types the digit into
  the composer as literal text, where it silently prefixes the next prompt
  (`"2reply with exactly …"`).
- A menu digit is sent **without Enter**. The digit confirms by itself; a
  trailing Enter answers the *next* modal blind — and claude's next modal
  renumbers `1` to `No, exit`. It quit.
- Menu state is read only after the pane stops redrawing (`settleAndInspectPane`),
  because a modal painting 200 ms late reads as "no menu".

Menus **chain** and **renumber**. Always loop on `awaitingChoice`; never assume
option 1 means yes.

### 2.3 Sessions now survive an agent restart (shipped 1.99.280)

The tmux server holding every runner lived in `yaver.service`'s cgroup, so
systemd's default `KillMode=control-group` destroyed all sessions on any restart
— upgrade, crash, reboot. Fixed with `KillMode=process` in all three unit
templates. Verified: codex pid identical either side of a restart, and survives a
binary swap.

The session is the shared state. Two clients attach to one tmux session; a third
surface drives it over plain HTTP. Verified `created=1783658581` identical across
detach/reattach.

### 2.4 Runner auth is honest now (shipped 1.99.278/279)

`/runner-auth/status?live=1` returns `authVerified`, which distinguishes "the
runner told us it is signed in" from "a credentials file exists". Presence of
`~/.claude/.credentials.json` proves nothing — it also stores MCP plugin OAuth.

Also: Claude's TUI runs its first-run wizard (theme → **Select login method** →
browser OAuth URL) whenever `~/.claude.json` lacks `hasCompletedOnboarding`,
**even with a valid credential**. The agent now sets that flag wherever it
establishes a credential. Any surface that opens a Claude TUI depends on this.

### 2.5 `yaver tv push` (shipped 1.99.280)

`desktop/agent/tv_push.go`. Builds any tvOS Xcode project and installs it on a
paired Apple TV.

```
yaver tv detect                 list paired Apple TVs
yaver tv push                   build the tvOS project here, install, launch
yaver tv push --project <path> --scheme <name> --device <udid> --no-launch
```

Project-agnostic (walks **up** from CWD for third-party apps) and dogfooding
(descends into `./tvos`, `./tv`, `./appletv` from a monorepo root, like
`wire push` descends into `./mobile`).

The install primitive needed **no changes**: `devicectl device install app` and
`process launch` treat an Apple TV exactly like an iPhone. What excluded tvOS was
discovery — `wire_cmd.go:244` drops everything that isn't an iPhone/iPad, its own
comment admitting *"Apple TV, Vision Pro etc come through too"*.

---

## 3. What does not exist

- **No Session screen on the TV.** The backend is live; nothing calls it.
- **No project selection.** `list_projects` / `project_context` exist agent-side;
  the tvOS app never calls them.
- **No results streaming.** `AppleTVRemoteView` polls capture frames; nothing
  shows browser automation, redroid, or preview clips.
- **No TTS.** `AVSpeechSynthesizer` is available and unused.
- **No CI builds the tvOS app.** Build `1.0.0 (4)` was uploaded by hand.
- **No `RunsAsCurrentUser`** in `tvos/YaverTV/Info.plist`. See §6.

---

## 4. What to build — the loop, and it is small

```
TextField + Siri dictation
   → POST /runner/session/turn {runner:"codex", text:"…"}
   ← {pane, awaitingChoice, options[]}
   → if awaitingChoice: render options[] as big focusable buttons
       → POST {choice:"2"}  (loop; menus chain and renumber)
     else: AVSpeechSynthesizer speaks a summary of `pane`
```

No terminal emulator. No WebView. No microphone. Every piece is available.

`awaitingChoice` + `options[]` is not a safety rail on this surface — it *is* the
interaction model. A D-pad is good at exactly one thing: picking from a short
list.

### Five screens, all mapped to endpoints that exist

1. **Machines** — `/ops {verb:"info"}`, `yaver_devices`. Blocked on §1.7 for
   non-LAN boxes.
2. **Projects** — `list_projects`, `project_context`.
3. **Runners** — `/runner-auth/status?live=1` (gate the pill on `authVerified`,
   not `authConfigured`), plus **Sign in** → `/runner-auth/browser/start` →
   render `openUrl` as a QR. Half of this already lives in
   `RuntimeDashboardView:104-165`.
4. **Session** — pane as monospaced `Text`; `TextField` for dictation; option
   buttons on `awaitingChoice`; TTS on reply.
5. **Results** — full-screen `AVPlayer` (HLS/MP4) or a JPEG poll.

### Suggested order

1. **Session.** It is the product, and needs zero new agent code.
2. **Runners + remote OAuth.** Half exists; `authVerified` makes it truthful.
3. **Results.** Start with the JPEG poll the app already does for capture.
4. **Transport (§1.7).** The gap between "drives my Mac" and "drives my Hetzner box".

---

## 5. Open decisions

**Transport to a non-LAN box.** Pick one:
(a) give the relay a real certificate and route the TV through HTTPS;
(b) add a narrow ATS exception for the agent's host;
(c) proxy through the Mac's agent, which already reaches remote boxes.
(a) is the only one that scales to other users.

**Claude's paste-back code.** Claude device-auth hands the user a long code to
paste into the CLI (`/runner-auth/browser/submit-code`). There is no paste on a
Siri Remote. The TV shows the QR; **the phone submits the code**. This is the
canonical TV+phone case and should be designed in, not bolted on.

**Results transport.** JPEG poll (works today, 5–10 fps) vs HLS from the agent's
existing ffmpeg (smooth, more work). Recommend poll first.

---

## 6. Known bugs / risks in the tvOS app

- **Shared token across tvOS users.** `RunsAsCurrentUser` and
  `TVUserManagementSupported` are both absent from `tvos/YaverTV/Info.plist`, so
  `@AppStorage("yaver.tv.token")` lands in one container shared by every user on
  that Apple TV. On a family TV, another user opening Yaver is signed in **as
  you** — their machines, sessions, Hot Reload. Fix with `RunsAsCurrentUser`, or
  move the token to the Keychain with per-user scoping.
- **App Review cannot test this app.** It needs a Yaver agent on the reviewer's
  own LAN, and §1.7 forbids pointing it at a remote demo box. Review notes were
  empty at submission; expect guideline 2.1. Ship notes + a demo video.
- **No CI.** Nothing builds or uploads the tvOS target.
- **`Info.plist` README drift.** `tvos/README.md:64` claims no `.xcodeproj` is
  checked in. One is.

---

## 7. Things that sound possible and are not

- **Hermes reload rendering the mobile app on the TV.** No RN tvOS host, no
  WebKit. The TV can *trigger* a reload (`/dev/reload`, `/dev/build-native` —
  buttons exist today) and *watch* a pixel stream. No bundle will ever run on
  the Apple TV.
- **Rendering the project's web UI on the TV.** No WebKit. Stream pixels.
- **Hold-to-talk voice.** No mic access. Use the keyboard's dictation.
- **Porting `shell.tsx`'s terminal.** It is xterm.js in a WebView.

---

## 8. Pairing an Apple TV (the one thing that cannot be scripted)

Required before `yaver tv push`, because Apple refuses a tvOS provisioning
profile until the device is registered to the team:

```
error: Your team has no devices from which to generate a provisioning profile.
```

1. Apple TV: **Settings → Remotes and Devices → Remote App and Devices**
2. Mac: **Xcode → Window → Devices and Simulators** (Shift-Cmd-2)
3. Click the TV under *Discovered* → **Pair** → type the 6-digit code.

`devicectl manage pair` cannot do it: it fails to find the TV by UUID, hostname,
or IP, and the TV never advertises `_remotepairing` (that advertiser is the
iPhone). Xcode's Devices window exposes no buttons to Accessibility, so it cannot
be automated either.

**TestFlight needs none of this.** tvOS build 4 is `IN_BETA_TESTING` in the
internal group `yavers`.

Also: `codesign` needs a GUI login session. In a Background session it fails with
`errSecInternalComponent`, which explains nothing. The agent daemon runs in that
session, so **an MCP-driven push can never sign on macOS**. `yaver tv push`
checks `launchctl managername` and says so.

---

## 9. Export compliance (fixed 2026-07-10, keep it fixed)

`ITSAppUsesNonExemptEncryption=false` is now declared in both
`mobile/ios/Yaver/Info.plist` and `tvos/YaverTV/Info.plist`. Without it every
upload parks at `MISSING_EXPORT_COMPLIANCE` — uploaded, `VALID`, unexpired, and
**invisible to every TestFlight tester**. iOS 392 and tvOS 262/263 were all
stranded this way while nobody noticed, because the upload reports success.
