# Yaver tvOS — native remote-runtime dashboard

> Status: **Apple TV App Store staged** (2026-07-05). Decision:
> `docs/yaver-tvos-fork-adr.md` (Option B — native SwiftUI, **no `react-native-tvos` fork**).
> This target builds and uploads through `scripts/deploy-tvos.sh`.

## Why this is separate from `mobile/`

Stock React Native + Expo cannot target tvOS, and the `react-native-tvos` fork would tax every
future `expo prebuild` / native overlay in `mobile/ios/` for a surface that is 90% touch-first
UI a Siri Remote can't drive. So Apple TV is a **small standalone SwiftUI app** that talks to a
Yaver agent over the **same** surfaces everything else uses:

- **Auth:** exactly two choices: email/password on the TV, or the RFC 8628
  device-code flow against Convex (`POST /auth/device-code`,
  `GET /auth/device-code/poll`). The TV shows a QR + short code; an
  already-signed-in Yaver phone approves it. The phone's original login method
  (Apple, Google, Microsoft, GitHub, GitLab, passkey, or email) is irrelevant.
- **Control:** direct LAN first, relay fallback when account settings provide relay
  metadata for the selected machine. Ops calls use `POST /ops` with
  `{ "verb": ..., "payload": ..., "machine": "local" }`
  and `Authorization: Bearer <session-token>` — identical in spirit to
  `mobile/src/lib/appletvClient.ts`. The same fallback is used for `/projects`, `/tasks`,
  `/feedback`, `/runner/session/turn`, `/droid/frame`, `/capture/frame.jpg`, and
  `/vibing/preview/*`, plus `/health`, so the picker does not say "connected" while the
  actual surface is LAN-only.

No new backend, no new agent code. The agent already serves every verb this app calls.

## Scope (lean-back runtime control — by design)

Shipped slice = the surfaces that are genuinely a 10-foot experience:

1. **Runtime control room** — machine status, dev-server status, Claude/Codex agent sessions,
   STT/TTS readiness, QR-based OAuth handoff, and hot-reload/Hermes-push controls
   (`info`, `status`, `runner`, `runner_auth`, `voice`, `reload`). Its Live Room band follows
   the shared `runtime_turns` queue so commands started from phone, watch, car, Android
   remote, or TV are visible on the television.
2. **Machine picker + wake** — account machines from `GET /devices/list`, selected-machine
   status, live-first auto-connect, shared-machine labels, and managed-box Wake.
3. **Vibing apps + preview** — a searchable four-column grid filters `/projects` to
   mobile, web, frontend, and TV apps, with framework/surface labels instead of
   backend-only repository rows. A selected app starts through `POST /dev/start`;
   the TV verifies `/dev/status.workDir` belongs to that exact checkout before
   opening interactive WebRTC, with authenticated frames as the bounded fallback.
4. **Live session** — `runtime_turn` when available, with `/runner/session/turn` fallback,
   so the TV can drive an existing Codex/Claude session and render the pane/options.
5. **Tasks and feedback** — glanceable task/session status and SDK feedback reports.
6. **Apple TV remote** — D-pad / transport / now-playing card (`appletv_*` verbs).
7. **Capture / now-playing** view of the home capture card (`capture_*`).

Dense code editing is still intentionally **not** on tvOS. The Apple TV is the wall
display/control surface while coding continues from MacBook terminal, Claude Code, Codex,
phone, or web. It can drive short prompts, choose session options, trigger reloads, and show
what the remote runtime is doing.

## QR auth handoff

Apple TV exposes exactly two Yaver account sign-in options:

- Email and password entered directly on the TV.
- QR login shows a short code. The QR targets
  `https://yaver.io/auth/device?code=...`; the signed-in Yaver phone app opens
  the approver via Universal Links/App Links. Mobile Settings puts **Scan TV
  QR** first and opens its in-app camera immediately. Approval uses the phone's
  current bearer, so every supported phone login provider authorizes the same
  TV flow.
- Claude Code and Codex auth is started on the selected runtime through
  `runner_auth browser_start`. tvOS renders the returned provider URL as a QR;
  the phone opens the system browser and completes OAuth/device-code handling.

The TV never renders a provider grid, OAuth browser, provider token/API-key
form, or native Apple sign-in. Accounts with two-factor email login finish via
the QR option on an already-authenticated phone. Android TV uses the same two
choices and the same Convex contract.

## Secure credential handoff

Settings → Secure credential handoff receives one explicitly approved provider
key or Git token without putting it in Convex. The TV registers only its public
X25519 key under the authenticated account, displays a two-minute public request
QR, then uses tvOS 17 Continuity Camera to scan the phone's authenticated
encrypted response. Both screens show the same six-digit comparison code before
the TV saves to a `ThisDeviceOnly` Keychain item. Expiry, wrong device/account,
tampering, and replay fail closed.

Continuity Camera requires Apple TV 4K (2nd generation or newer), tvOS 17+, and
a compatible iPhone/iPad. It cannot be tested in Simulator. tvOS cannot
advertise BLE services, so BLE receiver UI is intentionally not offered there.
Ordinary non-secret preferences already follow the account through `/settings`;
they do not enter the secret envelope or Keychain.

## Transport note

This app connects direct-first: LAN host/port when available, then the relay HTTP proxy for
machines selected from the account registry. tvOS loads the user's relay URL/password from
`GET /settings`, attaches it to the cached `BoxTarget`, and uses the same endpoint builder for
ops, REST, stream frames, runtime turns, and health probes. tvOS still does not embed a Swift
QUIC client; the fallback is the same HTTPS relay proxy shape used by browser-friendly
endpoints. Manual "type an address" entries remain LAN-only because they have no account
relay row.

## Creating the Xcode target (one-time)

The `.xcodeproj` is generated from `tvos/project.yml` by XcodeGen and is currently
tracked so non-XcodeGen consumers see the same package and source graph. Do not
hand-edit it: edit `project.yml`, regenerate, and include both resulting changes.

```bash
cd tvos && xcodegen generate     # ALWAYS run this before a build or deploy
```

Because the `.xcodeproj` is generated, a local copy can still go stale **silently**:
it keeps compiling whatever file list it was generated with, so a Swift file added by another
commit produces "cannot find X in scope" against code that is plainly on disk. Regenerating is
the fix, and it is cheap — make it reflexive.

Build & run on the tvOS Simulator or a real Apple TV. Sign in with email/password,
or scan the QR with the signed-in Yaver phone app and approve; the TV gets an
installation-bound 1-year companion session.

Submission mirrors the iOS path (App Store Connect, same team/API key), and the bundle id is
`io.yaver.mobile` — the **same** as the iPhone app, on purpose, so Apple treats TV/iOS/visionOS
as one Universal Purchase app record with separate per-platform build streams.

```bash
$(yaver vault env --project mobile)   # or: source ~/.appstoreconnect/yaver.env
./scripts/deploy-tvos.sh --upload
```

The build number is chosen for you: `--upload` asks App Store Connect for the highest existing
**TV_OS** build and uses that + 1 (`scripts/asc-next-build.sh` → `scripts/asc-max-build.py`).
`project.yml` pins `CURRENT_PROJECT_VERSION: "1"`, which is why this lookup matters — without
it every upload archives for minutes and is then rejected as a duplicate, burning a slot of the
~15-20/day TestFlight cap. The upload now stops before archiving if that ASC lookup is unreadable;
retry once the API responds. Set `TVOS_BUILD_NUMBER` only to deliberately override from a verified
ASC maximum, and it must exceed the current ASC max.

GitHub Actions uses `.github/workflows/release-apple-surfaces.yml`. A manual run
defaults to tvOS only, requires the protected `production` environment, validates
the six Apple signing/App Store Connect secrets, chooses the next TV_OS build,
and calls the same canonical `./deploy/deploy.sh tvos` entrypoint. It does not
need a local env file or an Apple ID session on the runner.

## Recording the App Store preview

After installing and signing in to the build you want to submit on a booted
Apple TV Simulator, record the real Vibing → SFMG → live-preview flow without
rebuilding it:

```bash
scripts/record-tvos-app-preview.sh "$HOME/Desktop/Yaver-tvOS-App-Preview.mp4"
```

The recorder opens Vibing, waits five seconds, and gives you 25 seconds to use
the Simulator remote. It exports and validates a 1920×1080, 30 fps, H.264 High
Profile level 4.0 file at an 11 Mbps target bitrate with stereo AAC. Override
the duration with `TVOS_PREVIEW_SECONDS=15..30`; the script refuses values
outside Apple's accepted App Preview duration and never builds or uploads.

## File map

| File | Role |
|---|---|
| `YaverTVApp.swift` | `@main` App; injects `YaverStore`. |
| `Backend.swift` | Convex origin + email/password and device-code auth (create + event/poll + claim). |
| `AgentClient.swift` | `POST /ops` to a box over LAN HTTP, Bearer auth. |
| `Models.swift` | `Codable` for now-playing, capture status, devices. |
| `YaverStore.swift` | `@MainActor ObservableObject` — session token, selected box, persistence. |
| `Views/SignInView.swift` | The two account choices: email/password or phone-approved QR. |
| `Views/DashboardView.swift` | Lean-back tile launcher. |
| `Views/RuntimeDashboardView.swift` | Runtime control room: status, Claude/Codex sessions, voice, QR OAuth, reload, Apple surface readiness. |
| `Views/AppleTVRemoteView.swift` | D-pad / transport / now-playing. |
| `FailureSignals.swift` | Named failure seams — capability gaps + install route, stream-drop recovery, relay deny verdicts, runner-auth terminal states. Pure Foundation, no SwiftUI. |

## Verifying `FailureSignals`

`project.yml` declares the app plus unit/UI test targets; regenerate the tracked
project with `xcodegen generate` before building so added sources and tests are
not silently omitted. Pure failure classification can also be checked without
a simulator or Apple TV:

```bash
swiftc -O -parse-as-library \
  tvos/YaverTV/FailureSignals.swift \
  tvos/Checks/FailureSignalsChecks.swift \
  -o /tmp/yaver-tv-checks && /tmp/yaver-tv-checks
```

`tvos/Checks/` sits outside `tvos/YaverTV/` on purpose: the XcodeGen spec globs
that whole directory into the shipping app, and a second `main` there is a link
error. Everything the app renders about a failure goes through `FailureSignals`,
so a wording or classification change is caught here rather than on a TV.

## Fast iteration loops (learned 2026-08-13 — don't wait for TestFlight)

TestFlight's processing + manual install is an hour per iteration. Two faster
loops live in `scripts/deploy-tvos.sh`:

```bash
scripts/deploy-tvos.sh --simulator        # ~1-2 min: build → install → launch
                                          # on the booted Apple TV simulator
                                          # (xcrun simctl). Same app, same agent
                                          # connection — everything except Siri
                                          # Remote dictation + hardware decode.

scripts/deploy-tvos.sh --device [UDID]    # minutes: automatic DEV signing +
                                          # devicectl install + launch on a
                                          # network-paired Apple TV. No
                                          # TestFlight processing wait.
```

`--simulator` needs only a booted Apple TV simulator (`xcrun simctl list
devices available`). `--device` needs the Apple TV paired with Xcode once —
on the TV itself, confirm the pairing prompt / enable Xcode connectivity
(Settings → AirPlay and Handoff), then `xcrun devicectl list devices` shows it.
Prereq for both: `brew install xcodegen` (the `.xcodeproj` is regenerated from
`project.yml` on every run — a stale project silently drops new Swift files).

This is the loop every third-party app developer should reach for: iterate the
SwiftUI in minutes on the simulator, verify hardware-only bits on the real TV,
and use TestFlight only for the final distribution build.
