# iOS Simulator dev path

Fast iteration without a physical iPhone. Especially useful for: UI tweaks,
Liquid Glass material testing (Xcode 26+), voice loop testing (Mac mic
forwarded), Hermes bundle loading, and `/spatial` preview in Simulator
Safari with `?surface=` overrides.

## Prerequisites

- macOS Sonoma 14+ (Sequoia 15+ for Xcode 16)
- Xcode 16 minimum
- Xcode 26 if you want true iOS-26 Liquid Glass in the Simulator (App
  Store download, ~10GB)
- Simulator runtimes installed via **Xcode → Settings → Platforms**
- CocoaPods (`sudo gem install cocoapods` if missing)

## Boot the app in Simulator

```bash
cd <repo>/mobile
npx expo run:ios                           # first time: full native build (~10-20 min)
npx expo run:ios --device "iPhone 16 Pro"  # pick a specific Simulator
npx expo run:ios --no-build                # subsequent: relaunch existing build (fast)
```

Once built, the app installs to the booted Simulator automatically.
Subsequent boots can skip the build with `--no-build` and just `xcrun
simctl launch booted io.yaver.mobile`.

## What works in Simulator

| Feature | Works? | Notes |
|---|---|---|
| App boots, all tabs visible | ✓ | iOS Simulator runs full app |
| Voice STT (`/voice/stream`) | ✓ | Mac mic forwarded — Deepgram receives Mac audio |
| TTS playback (Cartesia) | ✓ | Goes through Mac speakers |
| Liquid Glass mic orb / tab bar | ✓ on Xcode 26+ | YaverGlass falls back to BlurView on older Xcode |
| `/spatial` WebView preview pane | ✓ | Opens yaver.io/spatial in WebView |
| Hermes bundle load (in-app) | ✓ | Loads from your local agent the same as a phone would |
| Connection to local `yaver serve` | ✓ | Simulator shares the Mac's network stack |
| TestFlight install path | ✓ | But why? Just use the running build |
| Tablet layout testing | ✓ | Pick `iPad Pro (M4) 13-inch` Simulator |

## What does NOT work in Simulator

| Feature | Reason |
|---|---|
| Audio codec edge cases | Simulator audio path is software-only, differs from CoreAudio on device |
| Bluetooth pairing (no BLE stack) | Affects `yaver wireless push` testing — must use real iPhone |
| Push notifications via APNs | Apple requires real device push token |
| Background tasks (true freeze/resume) | Simulator's lifecycle approximates but isn't identical |
| Performance benchmarks | You're running on Mac silicon; numbers don't reflect iPhone hardware |
| `yaver wireless push` from CLI | Auto-detect targets real-device UDIDs only |
| `xcrun simctl bluetooth` | Not a real BT controller |
| App Store Review screenshots | Sim screenshots are accepted, but final review tests on real devices |

## Recipes

### Voice loop on Simulator

```bash
# 1. Start the agent locally with voice configured
yaver voice setup                            # prints config block to copy
# Edit ~/.yaver/config.json with Deepgram + Cartesia API keys + launch_projects map
yaver serve                                  # start agent on :18080

# 2. Boot the app in Simulator
cd <repo>/mobile
npx expo run:ios --device "iPhone 16 Pro"

# 3. In the running Simulator:
#    - Home tab → tap mic orb
#    - Permission dialog → "Allow"
#    - Speak into Mac mic (Sim forwards): "list my running tasks"
#    - Watch transcript appear in real-time, TTS reads back via Mac speakers

# Voice config tip: if Mac mic isn't picked up, check
# System Settings → Privacy → Microphone → Simulator.app is allowed.
```

### Liquid Glass on Simulator (Xcode 26+ only)

```bash
# Confirm Xcode 26 is the active toolchain
sudo xcode-select -s /Applications/Xcode-26.app    # if you have multiple

# Bump RN to 0.80+ first (task #6) — see CLAUDE.md "Cold-start mobile rebuild" runbook
cd <repo>/mobile
npm install                                  # picks up expo-glass-effect ~55.0.11
npx expo prebuild --platform ios --clean --no-install
git checkout -- mobile/ios/                  # restore force-tracked overlays per CLAUDE.md
cd mobile/ios && pod install && cd ../..

npx expo run:ios --device "iPhone 16 Pro"

# In the running Simulator (iOS 26+):
#   - Mic orb shows TRUE Liquid Glass refraction (not BlurView fallback)
#   - Tab bar surfaces refract
#   - Session chips on Home are glass capsules
#   - Toggle System Settings → Accessibility → Reduce Transparency to verify fallback to solid
```

### `/spatial` route in Simulator Safari

```bash
# 1. Run web dev server
cd <repo>/web
npm run dev                                  # next dev --turbopack on :3000

# 2. Get the Simulator's loopback host
#    Simulator-on-Mac sees localhost the same as the Mac

# 3. Open Safari in the booted Simulator
xcrun simctl openurl booted "http://localhost:3000/spatial?agent=http://localhost:18080&token=$(yaver sdk token --scope voice 2>/dev/null | tail -1)"

# 4. Try the surface overrides
xcrun simctl openurl booted "http://localhost:3000/spatial?surface=quest&agent=http://localhost:18080&token=..."
xcrun simctl openurl booted "http://localhost:3000/spatial?surface=ray-ban-display&agent=..."
```

### Useful `simctl` snippets

```bash
xcrun simctl list devices               # list installed Simulators
xcrun simctl boot "iPhone 16 Pro"       # boot one
xcrun simctl shutdown all               # shut down all booted Sims
xcrun simctl erase all                  # factory-reset (between test runs)
xcrun simctl openurl booted https://...
xcrun simctl io booted screenshot home.png
xcrun simctl io booted recordVideo demo.mov
xcrun simctl push booted io.yaver.mobile payload.json    # simulate APNs payload (Xcode 11.4+)
xcrun simctl appinfo booted io.yaver.mobile
xcrun simctl uninstall booted io.yaver.mobile
```

## Why this matters for Yaver dev

1. **Liquid Glass iteration** — the YaverGlass + YaverSheet primitives gracefully
   degrade to BlurView on Xcode 16, but you can't actually see the real Apple
   refraction without Xcode 26 + iOS 26 Simulator. Booting the Sim is the only
   way short of borrowing/buying an iPhone 16+ running iOS 26.

2. **Voice loop validation** — Deepgram Flux + Cartesia Sonic-3 don't care
   whether the audio came from CoreAudio or Simulator-forwarded Mac audio.
   You can validate the WS round-trip end-to-end without an iPhone.

3. **Cross-form testing** — the same `npx expo run:ios` flag with different
   `--device` values gives you iPhone SE, iPhone 16, iPhone 16 Plus, iPad mini,
   iPad Pro 13" — entire tablet layout pass in 5 minutes vs. owning each.

4. **Failure-mode reproduction** — `xcrun simctl push` to send synthetic
   notifications; `simctl erase all` for clean-slate testing.

## Related

- CLAUDE.md → "Mobile dev iteration (fast, no TestFlight)" section
- `yaver wireless push` runbook for real-device alternative
- `web/app/spatial/lib/surfaceDetect.ts` for the `?surface=` query overrides
- Memory: `project_spatial_deploy_test_2026_05_27`
- Memory: `project_liquid_glass_shipped_2026_05_27`
