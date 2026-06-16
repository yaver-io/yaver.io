# Yaver Wear OS — thin native Compose app

> Status: **scaffold** (2026-06-17). Design: `docs/yaver-smartwatch-voice-terminal.md`
> (§3 transport, §4 interaction, §6 platform). This is the Android-watch
> counterpart of "Yaver on the wrist" — a voice-first I/O membrane. The
> directory is **source-only**: it is **not** wired into any build pipeline or CI
> yet (mirrors `tvos/` and the unregistered `mobile/plugins/withAndroidTV.js`).

## Why this is a separate, standalone project (not a `:wear` module)

Wear OS is plain **Jetpack Compose** — it does **not** run React Native. If the
watch lived as a `:wear` Gradle module inside `mobile/android/`, every
`expo prebuild --clean` would clobber it and an Expo config plugin would have to
keep re-injecting it (the regeneration hazard called out in CLAUDE.md §cold-start
and `docs/yaver-smartwatch-voice-terminal.md` §6). So — exactly like `tvos/` for
Apple TV — the watch is a **fully separate, reproducible Gradle project** kept
out of the mobile prebuild entirely.

The watch **owns nothing and shows nothing complex**: voice in, one short
sentence + haptic out. It never renders code or diffs, and it **never blocks the
wrist** on a remote task (dispatch → "On it" + vibrate → background → wake on the
summary). The real loop — coding agent, redroid, chromedp/playwright, the
gateway CRUD — runs on the phone (default) or a remote box (standalone). The
watch adds no new backend.

## Transport (phone-paired first)

| Mode | Path | When |
|---|---|---|
| **A — phone-paired (DEFAULT)** | Wear Data Layer `MessageClient` → paired Android Yaver app → runner | Normal case. Watch holds no token; phone is brain-of-record. |
| B — standalone LAN (secondary) | `POST http://<box>:18080/watch/turn`, `Authorization: Bearer <session-token>` | Phone absent; explicit "use without phone" opt-in. |

Standalone auth is the **RFC 8628 device-code flow** against Convex
(`POST /auth/device-code`, poll `GET /auth/device-code/poll`) — identical in
shape to `mobile/src/lib/tvSignIn.ts` and the tvOS `Backend.swift`. The watch
shows a QR + short code; an already-signed-in browser/phone approves it.

### Wear Data Layer paths + wire protocol v1 (JSON, UTF-8 bytes)

Single source of truth: `WatchProtocol.kt` (`PATH_TURN`, `PATH_REPLY`).

| Direction | Data Layer path | Messages |
|---|---|---|
| Watch → Phone | `/yaver/watch/turn` | `transcript` · `confirm` · `intent` |
| Phone → Watch | `/yaver/watch/reply` | `ack` · `confirm-needed` · `working` · `summary` · `error` · `handoff` |

```jsonc
// Watch → Phone
{"v":1,"kind":"transcript","text":"<spoken command>"}
{"v":1,"kind":"confirm","token":"<token>","reply":"confirm"}   // or "cancel"
{"v":1,"kind":"intent","intent":"run-tests"}                   // or "deploy" | "status"

// Phone → Watch
{"v":1,"kind":"ack","spoken":"On it."}
{"v":1,"kind":"confirm-needed","token":"<token>","prompt":"That looks like a deploy command — confirm?"}
{"v":1,"kind":"working","taskId":"<id>","spoken":"Working…"}
{"v":1,"kind":"summary","taskId":"<id>","status":"completed","spoken":"Done. Tests pass."}
{"v":1,"kind":"error","spoken":"I couldn't reach your box."}
{"v":1,"kind":"handoff","target":"phone","spoken":"Sent it to your phone."}
```

**Every write/deploy verb is confirm-gated.** The PHONE decides what's risky and
sends `confirm-needed`; the watch only renders the prompt and echoes back
`confirm`/`cancel` with the opaque token. (Wrist taps misfire easily — Cancel is
the safe default.)

## Creating / Building (one-time, LOCAL)

This is source-only and **not CI-wired**. To build locally you need the Android
SDK with the Wear OS platform + a Gradle wrapper.

```bash
cd wear

# 1. Point Gradle at your SDK (gitignored, machine-local).
echo "sdk.dir=$HOME/Library/Android/sdk" > local.properties

# 2. Generate the Gradle wrapper if it isn't present (this scaffold ships
#    source only — no wrapper jar checked in).
gradle wrapper --gradle-version 8.7    # or use a system gradle

# 3. Build the debug APK.
./gradlew :app:assembleDebug

# 4. Install onto a connected Wear OS emulator / watch (adb over Wi-Fi or USB).
adb install app/build/outputs/apk/debug/app-debug.apk
```

Pairing for mode A: install the Yaver phone app, install this watch app, ensure
both are signed into the same account. The phone app must advertise the
`yaver_phone` Data Layer capability (`PhoneBridge.resolvePhoneNode()` looks for
it) and listen on `/yaver/watch/turn`. Versions in the `build.gradle.kts` files
(AGP / Kotlin / Compose compiler / Wear Compose) are plausible-and-recent but
**may need aligning** with the toolchain on your build machine.

## File map

| File | Role |
|---|---|
| `settings.gradle.kts` / `build.gradle.kts` / `gradle.properties` | Standalone Gradle build (`YaverWear`, `:app`). |
| `app/build.gradle.kts` | `io.yaver.wear`, minSdk 30, Compose + Wear Compose + play-services-wearable + speech + OkHttp + zxing. |
| `app/src/main/AndroidManifest.xml` | Watch app: `type.watch` feature, RECORD_AUDIO/INTERNET/VIBRATE, MAIN activity, reply listener service. |
| `YaverWearApp.kt` | `Application` (deliberately empty — watch owns nothing). |
| `WatchProtocol.kt` | **Single source of truth** for v1 wire protocol + Data Layer paths (`org.json`, dependency-free). |
| `PhoneBridge.kt` | `MessageClient` wrapper: send transcript/confirm/intent; node discovery via Capability/Node clients. |
| `ReplyListenerService.kt` | `WearableListenerService` for `/yaver/watch/reply` → `WatchState` + haptic (wakes even when backgrounded). |
| `WatchState.kt` | Process-wide in-memory UI state (`StateFlow`); maps replies → phase/line + haptic policy. |
| `AgentClient.kt` | Standalone LAN `POST /watch/turn` (Bearer), OkHttp. |
| `Backend.kt` | Standalone device-code auth (create + poll). |
| `Dictation.kt` | `RecognizerIntent` speech-to-text → transcript. |
| `Haptics.kt` | `Vibrator` cues (click / success / failure). |
| `ui/MainActivity.kt` | `ComponentActivity` hosting Compose; tap → dictate → send → one line + haptic, async. |
| `ui/WearApp.kt` | Wear Compose root: big result line, record button, working spinner, quick intents. |
| `ui/ConfirmScreen.kt` | `confirm-needed` prompt → Confirm/Cancel. |
| `ui/SignInScreen.kt` | Standalone-only QR + short code. |
