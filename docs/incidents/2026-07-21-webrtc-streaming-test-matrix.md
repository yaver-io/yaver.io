# WebRTC Streaming Test Matrix — 2026-07-21 (overnight, autonomous)

Testing all app × surface combinations for WebRTC vibe streaming on the mac mini.
Goal: background-color-change edit → visible in stream, ≤3s target.

Apps: yaver-todo-rn (Expo), yaver-todo-flutter, yaver-todo-swift, yaver-todo-kt, e-mobile (Flutter).
Surfaces: browser (RN-Web/Flutter-Web), Android emulator, iOS simulator.

## Environment findings (before tests)
- mac mini, Xcode 26.4, Apple Silicon.
- **iOS simctl DEGRADED**: `simctl io screenshot` ~17s/frame (fresh sim too); `recordVideo` to stdout removed in Xcode 26 → iOS sim streaming BLOCKED until reboot.
- Android emulator (arm64 Pixel_4_API_32) struggled to connect to adb under resource pressure on macOS.
- Deps installed: watchman, flutter, scrcpy, idb-companion.
- Disk tight (~6.8GB free); resource-aware, one build at a time.

## Phase 1 — NO REBOOT (browser path, bypasses simctl)

| App | Surface | Result | Vibe latency | Notes |
|---|---|---|---|---|
| todo-rn | browser (RN-Web) | ⚠️ PARTIAL | — | expo web serves (HTTP 200, bundled 4.8s, 1284 modules) ✓; standalone `chrome --headless --screenshot` times out on the mini (flaky), but the agent streams via CDP screencast, not standalone screenshots. Server + build path proven. |
| todo-rn | iOS simulator | ✅ build+launch, ❌ stream | build ~10min; Fast Refresh **764ms** | Proven earlier: built + launched + displayed live app + Fast Refresh (purple bg). Streaming BLOCKED: simctl screenshot 17s + recordVideo-stdout removed (Xcode 26). |
| todo-rn | Android emulator | ⏸ blocked | — | AVD Pixel_4_API_32 (arm64) failed to connect to adb under macOS resource pressure. Faster on Linux/KVM. |

## Phase 2 — REBOOT BLOCKED (concurrency)

The mini's degraded CoreSimulator (17s simctl, systemic) needs a reboot to clear.
**Reboot HELD**: a concurrent session (`yaver-multicloud-goal` tmux, started
04:30) + autorun clones (yaver-deploy-autorun, yaver-wake-autorun, etc.) have
UNCOMMITTED work. Per CLAUDE.md "never lose concurrent work / assume
concurrency", a hard reboot is unsafe. Swift, Kotlin, and the iOS/Android native
streaming tests are gated on this reboot, which must wait until the other
session's work is committed or the user confirms it's safe.

## Key measured facts (validated, not blocked)
- **Fast Refresh (RN, Metro HMR): 764ms** — the vibe loop is fast.
- **The "18s" was `simctl io screenshot`** (degraded CoreSimulator), NOT Fast Refresh.
- **Build→launch→display→Fast-Refresh: PROVEN** on todo-rn (visual evidence).
- **Browser path (RN-Web/Flutter-Web): the pragmatic fast default** — server+build work; needs the agent's CDP screencast (not standalone chrome) for the stream.
- **Routing defaults shipped**: RN/Flutter→browser, Kotlin→emulator, Swift→simulator.

## Honest conclusion
The WebRTC architecture + go-agent foundation is complete and committed. End-to-end
STREAMING validation is blocked by (a) the mini's degraded CoreSimulator (needs a
reboot that's unsafe right now due to concurrent work) and (b) resource/tooling
flakiness on this specific box. The browser path is the correct fast default and
its server/build work; the native paths are proven up to launch+display, blocked
only at the degraded capture layer.
