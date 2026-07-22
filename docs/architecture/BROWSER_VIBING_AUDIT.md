# Browser-Based Vibing — Audit and Handoff

Status: 2026-07-22. **Code is source of truth.** Every claim here was grepped
or executed on the day of writing; the traps section exists because several
"obvious" readings were wrong.

Scope: previewing a user's app *in a browser inside the Yaver container*, the
options that surround it (Hermes / WebRTC / browser), and the STT vibing loop
that should sit on top.

---

## 1. The one thing to understand first

**There are TWO routing models in this repo and only one of them runs.**

| Layer | Files | Production callers |
|---|---|---|
| Preview *plan* | `workspace_preview_strategy.go`, `preview_host_platform.go`, `preview_transport_route.go` | **ZERO** |
| Remote-runtime *targets* | `remote_runtime.go` → `remoteRuntimeCapabilitiesForProject` | the mobile app, via `GET /remote-runtime/capabilities` |

The plan layer is well-tested and wired to nothing. `grep -rn "ResolveWorkspacePreview\|ResolvePreviewRoute" --include="*.go" .`
outside those files and their tests returns nothing.

This cost a full cycle on 2026-07-22: a green capability matrix "proved"
SwiftWasm-on-Linux worked while the product still refused it, because the
matrix tested the island. **Before any further preview work, wire the plan
layer or delete it.** Two parallel models is exactly how `browser-window`
stayed invisible to Swift for so long.

---

## 2. What the browser lane is, and why it matters

`probeBrowserWindowTarget()` (`remote_runtime_browser.go:361`) is a headless
Chrome tab on the agent host, streamed to the phone. Its own probe declares
`RuntimeHostClass: "any"`, `HostOS: "any"` — it is gated **only** on a usable
browser binary.

That makes it the one lane that works everywhere: Linux, macOS, native Windows,
WSL1, WSL2. It carries every browser-renderable stack — Flutter, RN-web,
SwiftWasm, Next.

Three independent reasons it should be preferred over pixels wherever it applies:

1. **Cost.** `direct-url`/browser is ~0 vCPU against ~1–2 for chrome-webrtc or
   an emulator. On a 2c/4GB box one WebRTC preview eats half the machine.
2. **Feedback.** On the browser path the *in-app SDK* applies — `yaver_feedback`
   (pub.dev) for Flutter, `yaver-feedback-web`/RN for JS stacks — so feedback
   carries real app state. On a pixel path the app is a *video*, so feedback
   degrades to viewer-triggered messages down the events DataChannel.
   **Same app, strictly worse loop, for more money.**
3. **Fidelity.** A WebView renders at native DPI with real touch; WebRTC of a
   web page is a lossy re-encode with synthetic input.

---

## 3. Current wiring, per stack

| Stack | Target order today | Where |
|---|---|---|
| Flutter | **browser-window** first, then emulators/simulators | `remote_runtime.go` flutter arm |
| Swift (Tokamak/SwiftWasm) | **browser-window** first, then iOS sim/device | swift arm, chosen via `DetectSwiftProject` |
| Swift (UIKit/SwiftUI) | iOS sim/device only — browser correctly withheld | swift arm |
| Kotlin | emulator/Redroid/device | kotlin arm |
| **React Native** | **no arm at all** under `ExecutionModeNativeWebRTC` | — |

### RN is the open item, and it is NOT a one-liner

The framework switch lives under `case ExecutionModeNativeWebRTC`. RN never
reaches it — RN is routed through Hermes/dev-server mode. So "browser + Hermes
+ WebRTC" for RN means **giving RN a native-webrtc capability path it does not
have**, not appending `probeBrowserWindowTarget()` to a list.

An earlier version of this document claimed it was a one-line change. That was
wrong and is recorded here so nobody re-plans around it.

The three RN options, once wired, are genuinely different products:

- **Hermes** — the real bundle in the Yaver container on THIS phone. True
  runtime, device APIs, in-app SDK.
- **Browser** — RN-web in a headless tab on the box. Cheapest; good enough for
  layout/logic vibing; in-app web SDK.
- **WebRTC** — a simulator/emulator on the box. Needed when the phone cannot
  build it or the target platform is not this phone.

Only the user knows which they want, which is why it must be a choice and not a
heuristic.

---

## 4. Host matrix (measured 2026-07-22)

| Host | Browser lane | Android | iOS |
|---|---|---|---|
| macOS | ✅ | emulator (WHPX/HVF) | ✅ simulators |
| Linux | ✅ | Redroid *if binder*, else emulator w/ KVM | ❌ never |
| Windows native | ✅ | emulator (WHPX) | ❌ |
| WSL2 | ✅ | emulator belongs on the Windows side | ❌ |
| WSL1 | ✅ | ❌ no kernel | ❌ |

Answer to "can we fully depend on WSL?" — **yes for the browser lane**, which
is most of what a remote machine is asked to do. See `host_wsl.go`.

Two gates were categorically wrong before this audit and are now probed:

- Windows was excluded from `HostCanRunAndroidEmulator`, making every Windows
  box useless for Kotlin despite WHPX.
- `HostCanRunRedroid` trusted the OS label. **WSL2 *is* Linux and still cannot
  run Redroid**: Microsoft's stock kernel omits binder/ashmem, so the container
  fails at *start*, not at probe.

---

## 5. Traps — each one cost real time today

1. **`_wasm_test.go` never runs.** Go reads `_wasm` before `_test.go` as an
   implicit **GOARCH constraint**, so the file only builds under `GOARCH=wasm`.
   It sits in `IgnoredGoFiles` reporting `no tests to run` while looking
   perfectly healthy. Check `go list -f '{{.IgnoredGoFiles}}'` when a test
   "passes" suspiciously fast. Same applies to `_linux`, `_darwin`, `_windows`,
   `_arm64`, `_js`.
2. **Name-based binary detection is a false green.** Ubuntu's
   `chromium-browser` is a snap stub (`2:1snap1-0ubuntu2`, jammy *and* noble)
   that resolves on PATH and refuses to launch. Always verify by running
   `--version` — `DiscoverChromeBinary()`/`chromeBinaryUsable()`.
3. **`apt-get install google-chrome-stable` fails on stock Debian/Ubuntu.** The
   package is in Google's own repo, which must be added with a keyring first.
4. **macOS ships Chrome as an app bundle** with nothing on `$PATH`. A
   PATH-only probe reports "no browser" on a Mac with Chrome installed.
5. **tmux `-F` escapes control bytes** — a `0x1f` field separator arrives as
   the literal `\037`. Use tab.
6. **`go test ./...` in `desktop/agent` signs the machine out** — it hits the
   real `~/.yaver`. Always scope with `-run`.

---

## 6. The STT vibing loop — design, not yet built

The target experience: the user browses their app in the Yaver container, says
*"this isn't good, change this"*, and a screenshot + transcript reach the
runner, which edits and the view hot-reloads.

Why the browser lane is the right substrate for it:

- **Screenshot is free and faithful.** The app is a real DOM in a real tab, so
  capture is a page screenshot, not a video keyframe.
- **Hot reload already exists** for every browser-renderable stack — Flutter web,
  Vite/Next HMR, RN-web fast refresh. The runner edits, the dev server reloads,
  the tab updates. No new transport.
- **The in-app SDK is already there** to carry context (route, state, logs)
  alongside the screenshot.

Pieces that exist and can be composed:
- STT: `mobile/src/lib/voice/` — `VoiceConversationCore`, streaming STT,
  semantic endpointing (`endpointer.ts`, `completenessJudge.ts`).
- Runner turn: `POST /runner/session/turn` — types a prompt into a live agent
  pane and reads the result back. Already menu-guarded.
- Feedback capture: `sdk/feedback/*` + `blackbox*.go`.

Pieces that do **not** exist:
- A capture→prompt assembler that packages (screenshot, transcript, route) into
  one runner turn.
- Any UI affordance for "speak a change while previewing".
- A reload-confirmation signal back to the viewer, so the user sees *that* the
  change landed rather than guessing.

**Recommended order:** wire/delete the plan layer → RN browser arm → capture+
prompt assembler → STT trigger in the viewer. Each is independently testable;
the loop is not testable until the first two exist.

---

## 7. Closed-loop test status

Real, executing, in `desktop/agent`:
- `remote_runtime_swiftwasm_target_test.go` — SwiftWasm gets `browser-window`;
  UIKit does not; Flutter leads with it; macOS reaches the simulator target.
- `host_wsl_test.go` — Windows allowed the emulator; iOS never off Apple;
  Redroid gated on binder not the label; WSL detected via `/proc/version`
  (not `WSL_DISTRO_NAME`, which is absent under systemd).
- `preview_capability_matrix_test.go` — **tests the unwired plan layer.**
  Accurate about that layer, and proves nothing about product behaviour until
  §1 is resolved.

**Not built:** a cross-machine harness exercising this Mac and a Hetzner box
against the todo fixtures in all stacks. That is the missing coverage, and it
is the only thing that would have caught the plan-layer island automatically.
It needs a real dev server per stack, a real browser target, and an assertion
that frames actually arrive — the same "probe the operation" standard applied
to the test suite itself.
