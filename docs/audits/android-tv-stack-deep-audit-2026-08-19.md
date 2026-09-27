# Android TV stack deep audit — 2026-08-19

## Verified source of truth

The Android TV implementation is a standalone `androidtv/` Compose build with
Kotlin namespace `io.yaver.tv`. Release builds use application ID
`io.yaver.mobile` so the distinct TV AAB can ship from the existing Play listing
on `tv:internal`; the required Leanback feature keeps it TV-only.

## Findings

### Fixed in this implementation slice

1. The shared `TvTextButton`, `TvTile`, and `TvChip` accepted `onClick` but
   only exposed `focusable`; Select was a no-op. They now use Compose
   `clickable` and retain the TV focus treatment.
2. The standalone project did not compile due to stale OkHttp APIs, missing
   coroutine/Compose imports, incorrect settings map parsing, invalid Row and
   Column parameters, and a missing navigation argument. These are repaired.
3. The Chat dashboard route no longer lands on a static placeholder: it loads
   `/tasks`, provides loading/error/retry states, and opens `/tasks/{id}`.
   Absolute home paths are still redacted before display.

### Remaining implementation gates

- `TaskComposerScreen`, live `SessionScreen`, `VibingScreen`, preview, and
  Android-device stream remain placeholders.
- A physical/emulator D-pad pass is still required; source compilation cannot
  prove focus order or 10-foot readability.
- The standalone release/deploy script now builds and verifies the TV AAB.
  Play Console must enable the Android TV form factor and `tv:internal` track
  once before the Publisher API can upload it.
- Unit tests pass with the JVM `org.json` test dependency; offline Gradle mode
  still cannot resolve uncached artifacts, so CI or a connected build is the
  reliable test path.

## Acceptance order

1. `assembleDebug` and unit tests pass.
2. Install on Android TV emulator; verify every visible control by D-pad only.
3. Verify LAN request, relay fallback, wake, task list/detail, and sign-out.
4. Build signed TV artifact through the canonical deployment wrapper.
5. Deploy only after explicit owner confirmation and a clean mobile/Xcode
   process check.
