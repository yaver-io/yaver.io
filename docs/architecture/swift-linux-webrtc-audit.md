# Swift on a Linux Cloud Workspace: Detection, Routing, and the WebRTC Path

Date: 2026-07-22
Status: audit + implementation plan.
Goal: stream a **Swift-only** app over WebRTC from a **Linux** Cloud Workspace,
and let a runner change it (e.g. background colour) with the change visible live.

Companions: `native-reload-and-sdk-webrtc-audit.md`,
`yaver-preview-mode-and-self-development.md`,
`desktop/agent/workspace_preview_strategy.go`.

---

## 1. The question, precisely

"Can Swift stream from Linux?" has no single answer, because **"a Swift app" is
four different things** with four different runtimes. Treating them as one is
how a capability gets declared that the operation cannot deliver — the failure
mode this codebase has hit four times already.

| # | Kind | UI framework | Runs on Linux? |
|---|---|---|---|
| 1 | **Tokamak / SwiftWasm** | Tokamak (SwiftUI-*compatible*) | ✅ compiles to WASM, renders in a browser |
| 2 | **Server-side Swift** | none (Vapor/Hummingbird serve HTML) | ✅ it is a web server |
| 3 | **Apple SwiftUI** | Apple's SwiftUI | ❌ closed framework |
| 4 | **UIKit** | UIKit | ❌ closed framework |

Rows 1 and 2 are fully Linux-native and reuse the **`chrome-webrtc`** strategy
already built for RN and Flutter. Rows 3 and 4 need a Mac host — no engineering
changes that.

**Every row can compile and `swift test` on Linux**, because the Swift toolchain
itself is officially cross-platform. Only *rendering* splits.

---

## 2. Why Tokamak/SwiftWasm is the one Linux path

The toolchain, all Linux-native:

```
Swift source
  → SwiftWasm toolchain (swiftwasm/swift, Linux binaries published)
  → WebAssembly + JS glue (carton / SwiftPM plugin)
  → static bundle served over HTTP
  → headless Chrome renders it            ← already how RN-web and Flutter work
  → WebRTC stream to the phone            ← already built
```

Nothing new below the "static bundle" line. Once a Swift project produces a web
bundle, it is indistinguishable to the rest of Yaver from a Vite app.

### What was ruled out, so nobody re-litigates it

| Option | Why not |
|---|---|
| **Skip.tools** (SwiftUI → Kotlin/Compose) | Transpiler runs as an **Xcode plugin** — needs macOS, so it cannot be the Linux answer despite otherwise fitting perfectly |
| **Darling** (macOS-on-Linux) | Explicitly excludes Xcode and the simulator |
| **Cross-compile Swift → iOS on Linux** | Produces a binary you cannot run; redistributing the iOS SDK is a licence violation |
| **iOS Simulator on Linux** | Apple-proprietary, macOS-only, licence-restricted. Closed, permanently |

---

## 3. Detection — the signals, ranked by reliability

Detection must be **evidence-based and ordered**, because the kinds overlap: a
Tokamak project is also a SwiftPM package, and an Xcode project may contain
server-side Swift targets.

**Order matters — first match wins:**

| Rank | Signal | Concludes |
|---|---|---|
| 1 | `Package.swift` contains `TokamakDOM` / `TokamakShim` / `swiftwasm` | **Tokamak** → Linux + chrome-webrtc |
| 2 | Source imports `TokamakDOM` or `TokamakShim` | **Tokamak** (dependency may be transitive) |
| 3 | `Package.swift` contains `vapor` / `hummingbird` / `swift-nio` **and** no UI import | **server-side** → Linux + chrome-webrtc |
| 4 | `.xcodeproj` / `.xcworkspace` present, **or** source imports `UIKit`/`SwiftUI` | **Apple UI** → Mac host required |
| 5 | `Package.swift` only, no UI imports | **library/logic** → Linux, tests only, no render |

Rank 4 is deliberately *below* 1–3: a Tokamak project can carry an `.xcodeproj`
for editing convenience while still targeting WASM. Checking Xcode first would
misroute it to a Mac it does not need.

**Never guess.** If nothing matches, report "unknown Swift project" and route to
tests-only. Inventing a render target is worse than admitting uncertainty —
the user sees something that is not their app.

---

## 4. Routing matrix (what the resolver must return)

| Detected kind | Primary | Machine class | Renders? | Feedback |
|---|---|---|---|---|
| Tokamak / SwiftWasm | `chrome-webrtc` | standard (2c/4GB) | ✅ | in-app SDK |
| Server-side Swift | `chrome-webrtc` | standard | ✅ | in-app SDK (web) |
| Apple SwiftUI / UIKit | `ios-simulator` | **Mac host** | ✅ only on Mac | viewer-triggered |
| Library / logic only | *none* | standard | ❌ **tests only** | n/a |

The last row must be **honest, not a refusal**: compile + `swift test` on Linux
is real value for an agent loop, and the previous flat "unsupported" turned away
a developer who could have used the product today.

---

## 5. The demo target: Swift todo app, colour change over WebRTC

End-to-end chain to prove:

```
1. workspace provisions (2c/4GB Linux)
2. detect: Tokamak project
3. carton dev / swift build --triple wasm32 → static bundle on :8080
4. headless Chrome loads it
5. WebRTC streams Chrome's surface to the phone
6. runner: "change the background to blue"
7. agent edits Color(...) in the Swift source
8. rebuild → bundle → browser reload → next WebRTC frame
9. phone sees blue
```

### Where this will be slower than RN

**Step 8 is the risk.** RN gets HMR — a module swap in a live runtime, ~200–600 ms.
SwiftWasm has **no HMR**: a change means a full WASM recompile and a page
reload. Realistic estimate **5–20 s** on 2c/4GB, dominated by the Swift
compiler.

That is still far better than the 20–90 s native rebuild-reinstall, and the
developer never leaves their phone. But it must be **measured, not assumed** —
if it lands at 60 s the loop is not usable and the honest answer is a bigger
class or a Mac.

**Consequence:** a full page reload loses UI state. For a todo app that is
invisible; for a deep navigation stack it means re-navigating on every edit.
Worth knowing before promising a general Swift loop.

---

## 5.5 MEASURED: why this must be Linux, not macOS

Attempted locally on Swift 6.2.3 / Xcode, 2026-07-22:

```
swift sdk install …swift-wasm-6.2-RELEASE…      ✅ installs (108 MB, checksum-verified)
swift build --swift-sdk …wasm32-unknown-wasip1  ❌ error: unable to create target:
                                                   'No available targets are compatible
                                                    with triple "wasm32-unknown-wasip1"'
```

**Apple's Xcode clang is built without the WebAssembly backend.** The SwiftWasm
SDK supplies the Swift stdlib for wasm, but Tokamak's tree contains C targets
(`_CJavaScriptKit`, `_CJavaScriptEventLoop`) that need a clang able to emit
wasm — and Apple's cannot.

This **validates** the Linux decision rather than undermining it: the
open-source toolchain in `swift:*-jammy` ships the wasm backend out of the box.
macOS would need a separate swift.org toolchain installed alongside Xcode.

Two consequences applied to `Dockerfile.yaver-swiftwasm`:

1. **Base bumped 5.10 → 6.2.** Tokamak's dependency tree (JavaScriptKit,
   OpenCombine) resolves to versions requiring Swift 6.x; 5.10 would have failed
   at dependency resolution.
2. **Use the native `swift sdk install`** (108 MB, checksummed) instead of
   building a toolchain fork from source — on a 2-core box that is tens of
   minutes of image build avoided. Note `swift sdk install` REQUIRES
   `--checksum` for remote URLs; it is published alongside the artifact.

The cheap local attempt cost nothing and moved two Dockerfile bugs from
"discovered during a 40-minute image build on a billing box" to "fixed before
the first build".

---

## 6. Prerequisites not yet in place

| Item | State |
|---|---|
| SwiftWasm toolchain in the workspace image | ❌ not installed |
| `carton` (SwiftWasm dev server) | ❌ not installed |
| A Tokamak todo app fixture | ❌ does not exist |
| Detection + routing in the agent | ❌ this change |
| chrome-webrtc streaming | ✅ strategy exists (unverified live) |

The image work is the same shape as the trial image: **bake the toolchain**, do
not install at first run. A SwiftWasm toolchain download is hundreds of MB — in
the critical path it would be minutes of spinner.

---

## 7. Risks, stated before building

1. **Tokamak adoption is small.** This serves Swift devs who *chose* Tokamak, not
   the iOS mainstream. Real, but narrow — position it as "Swift on the web",
   not "iOS development on Linux".
2. **No HMR** (§5). The loop is rebuild-and-reload; measure it.
3. **SwiftWasm binary size and compile time** are both worse than native Swift.
   2c/4GB may not be enough — the class ladder exists for this, but it changes
   the margin story if Swift needs `heavy` by default.
4. **Fidelity is not iOS.** A Tokamak render is a DOM render. It will not match
   UIKit pixel-for-pixel, and must never be described as an iOS preview.
5. **Toolchain drift.** SwiftWasm tracks Swift releases with a lag; a pinned
   version in the image is required, and it will need deliberate upgrades.

---

## 8. Implementation order

1. **Detection + routing in the Go agent** (this change) — pure, testable, no
   toolchain needed. Ships value immediately by replacing the flat Swift refusal.
2. **Bake SwiftWasm + carton into the workspace image.**
3. **Build the Tokamak todo fixture** (`yaver-todo-swift-wasm`, outside this
   repo, like the other fixtures).
4. **Wire the dev-server kind** so a Tokamak project starts `carton dev` and is
   classed web.
5. **Measure the edit→visible latency.** That number decides whether this is a
   product or a demo.
6. Only then: the colour-change runner demo end to end.

Step 1 is worth doing regardless of whether 2–6 ever happen, because the current
resolver turns away Swift developers whose logic and tests would run fine today.
