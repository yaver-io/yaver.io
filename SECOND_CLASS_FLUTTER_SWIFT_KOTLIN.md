# SECOND_CLASS_FLUTTER_SWIFT_KOTLIN

## Goal

Add a second-class mobile flow for Flutter, Swift, and Kotlin without weakening the first-class Hermes path for React Native / Expo.

## Non-Negotiables

- Hermes stays first-class.
- `Open in Yaver` remains Hermes-only.
- Hermes keeps working over LAN, relay, and 4G.
- Flutter / Swift / Kotlin are LAN-only in this pass.
- Do not claim these frameworks load inside the Yaver container.
- Vibing / Agent Mode is the user-facing entrypoint, not manual CLI flags.
- Claude Code / Codex / Aider decide intent from Yaver context; Yaver decides transport and execution.

## Control Plane

- Vibing is the orchestration layer.
- The runner sees structured Yaver context:
  - selected machine
  - selected phone
  - project framework
  - connection mode
  - LAN eligibility
- The runner decides the goal:
  - open in Yaver
  - compile Hermes
  - flush to app
  - flush build to phone
- The Go agent decides the execution path:
  - `expo` / `react-native` -> Hermes
  - `flutter` -> Flutter wrapper
  - `swift` -> Xcode build/install
  - `kotlin` -> Gradle build/install
- The mobile app is a control surface and status surface, not the source of framework truth.

## Device Awareness

- The user may operate Yaver from a different device than the target phone.
- The selected machine and selected phone are first-class routing inputs.
- The Go agent must be target-device aware, not just platform aware.
- `ios` and `android` are not sufficient as concrete routing values for second-class flows.
- Second-class execution is allowed only when the selected machine and selected phone are on the same LAN.
- If LAN eligibility fails, Yaver should explain that Hermes is the only first-class remote path.

## Product Shape

- React Native / Expo:
  - Keep the existing `Open in Yaver` and Hermes compile flow unchanged.
- Flutter:
  - Show a LAN-only `Flush to App` action instead of pretending it opens inside Yaver.
  - Reuse the existing dev-server / reload path so the phone app acts as the control surface, not the runtime container.
- Swift / Kotlin:
  - Show a LAN-only `Flush Build to Phone` action.
  - Reuse the existing build pipeline.
  - On iOS, prefer direct Xcode device install when available.
  - On Android, build the APK, download it to the phone, and trigger install from the mobile app.

## UI Changes

- In `mobile/app/(tabs)/apps.tsx`:
  - Add framework detection helpers for second-class mobile frameworks.
  - Categorize `flutter`, `swift`, and `kotlin` as mobile projects.
  - Add custom action-sheet actions for LAN-only flush flows.
  - Remove misleading disabled "Hot Reload" entries for second-class frameworks when a better LAN-only flush action is available.
  - Change the running-app card copy so Flutter does not say `Open in Yaver` / Hermes.
  - Keep all Hermes labels and code paths intact for Expo / React Native.
- In Vibing / agent prompts:
  - pass selected machine, selected phone, framework, and LAN eligibility into Claude Code / Codex / other runners
  - let the runner choose intent, not low-level flags like `--platform ios`

## Execution Plan

1. Add a plan-safe framework split in `Apps`:
   - Hermes-first frameworks: `expo`, `react-native`
   - Second-class frameworks: `flutter`, `swift`, `kotlin`

2. Add LAN-only custom actions:
   - Flutter: `Flush to App (LAN)`
   - Swift/Kotlin: `Flush Build to Phone (LAN)`

3. Reuse existing transport/build code:
   - Flutter: `startDevServer({ framework: "flutter" })` + `reloadDevServer()`
   - Swift iOS: `startBuild("xcode-device-install", ..., true)`
   - Kotlin Android: `startBuild("gradle-apk", ...)`, then download artifact and install locally

4. Make the Go agent framework-aware and target-aware:
   - do not treat `ios` / `android` as concrete Flutter device IDs
   - resolve the actual selected phone for second-class paths
   - preserve Hermes routing unchanged

5. Make Vibing the orchestration layer:
   - the runner receives Yaver context and decides the goal
   - Yaver maps that goal to the correct framework-specific execution path

6. Update running-state UI:
   - Hermes projects keep `Open in Yaver`
   - Flutter running projects get `Flush to App`
   - Reload button remains reload

7. Verify no Hermes regression:
   - No edits to Hermes bundling or loading logic
   - No edits to the React Native compatibility flow except UI branching

## Risks

- Flutter LAN reload depends on the host-side Flutter toolchain seeing the real phone.
- Native Android install depends on the existing APK installer module.
- iOS direct install remains macOS + Xcode only.
- Cross-device use requires correct machine/phone routing metadata in Vibing and the Go agent.

## Follow-Up Work

- If needed later, add an explicit build-status stream for second-class flushes in the Apps UI.
- If needed later, add a shared manifest endpoint / OTA polish for IPA-based installs outside the direct-install path.
- Add a dedicated agent-side `mobile_flush` capability that accepts:
  - framework
  - workDir
  - targetDeviceId
  - targetDeviceName
  - targetDeviceClass
  - requestedByRunner
