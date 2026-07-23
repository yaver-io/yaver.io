# Swift-only users on Hetzner Linux — build, stream, vibe

Date: 2026-07-22
Status: research + routing decision. Extends `swift-linux-webrtc-audit.md` (same
day, earlier session) with the parts that audit did not evaluate: **xtool**, the
**official Swift SDK for Android** (Swift 6.3, March 2026), **Skip's Linux
mode**, **macOS-in-a-VM**, and the **cloud-Mac economics** that decide whether a
Mac tier can exist at all under the scale-to-zero rule.

Companions: `swift-linux-webrtc-audit.md`, `native-reload-and-sdk-webrtc-audit.md`,
`WEBRTC_RN_SIMULATOR_STREAMING.md`, `desktop/agent/workspace_preview_strategy.go`,
`desktop/agent/swift_project_detect.go`, `desktop/agent/preview_host_platform.go`.

---

## 0. The question

A user shows up with a **Swift-only app** and no Apple hardware. Yaver's compute
is Hetzner Linux. What can we honestly give them, how fast is the edit→see loop,
and where is the wall?

The wall is **not** technical curiosity. It is one sentence in Apple's Xcode and
Apple SDKs Agreement:

> "USE OF APPLE SOFTWARE IS GOVERNED BY THIS AGREEMENT AND IS AUTHORIZED ONLY FOR
> EXECUTION ON AN APPLE-BRANDED PRODUCT RUNNING MACOS"

Everything below is a consequence of that line. Every "can we just…" ends there,
so it is stated first rather than rediscovered five sections in.

---

## 1. What already landed (do not rebuild it)

The earlier audit + implementation shipped a real, correct spine. Grounded in
code, not the doc:

| Piece | Where | State |
|---|---|---|
| Swift is 4 runtimes, not 1 | `swift_project_detect.go:32-37` (`SwiftKindTokamak/Server/AppleUI/Library/Unknown`) | ✅ landed |
| Evidence-ordered detection, never guesses | `swift_project_detect.go:79-132`, returns `Unknown` rather than inventing a render target | ✅ landed |
| Routing to a preview plan | `ResolveSwiftPreview` `swift_project_detect.go:140-201` | ✅ landed |
| Ordering fix: wasm case **before** the `contains("swift") → needs macOS` case | `workspace_preview_strategy.go:192-214` | ✅ landed |
| Host platform detected, never assumed | `preview_host_platform.go:33-52` — `HostCanRunIOSSimulator` is macOS-only *by licence*, `HostCanRunRedroid` is Linux-only *by kernel* | ✅ landed |
| SwiftWasm dev server | `devserver_swiftwasm.go` — probes `carton` on PATH (operation, not inventory), `SupportsHotReload() == false` | ✅ landed |
| Workspace image | `Dockerfile.yaver-swiftwasm` — exact patch pin (`6.3.0-jammy`), `swift sdk install --checksum` | ✅ landed |
| `yaver swift doctor` / `swift logic` | `swift_cmd.go:39-49` | ✅ landed |
| Structured no-Mac refusal (`wrong_host_os`), never a silent downgrade | `build_preflight.go:170-176, 229-260` | ✅ landed |
| **BYO paired Mac builder + signaling-only proxy** | `remote_builder.go`, `remote_runtime_dispatch.go:92-197` | ✅ landed — see §6 |
| Native WebRTC streaming of a real simulator | `remote_runtime_capture.go:38-53`, `remote_runtime_video_track.go:239-262` | ✅ landed |
| Native Swift feedback SDK | `sdk/feedback/swift/` | ⚠️ **exists, but the Go layer still says it doesn't** — see §10.2 |

**Two measured facts worth not re-deriving** (from the earlier audit, §5.5/§5.6):

1. Apple's Xcode clang **has no wasm backend** — `swift build --swift-sdk wasm32`
   fails on macOS and works in `swift:6.3.0-jammy`. Linux is the *correct* host
   here, not the consolation prize.
2. A `X.Y-jammy` Docker tag can **never** match a `X.Y-RELEASE` SDK. Swift demands
   an exact patch match. This fails **silently**: image builds green, every wasm
   build inside it fails.

**One correction carried forward:** Tokamak is abandoned (last commit Feb 2023).
The constant is still named `SwiftKindTokamak`; what it should mean going forward
is "Swift that compiles to wasm and renders in a browser" — JavaScriptKit today.

---

## 2. What the earlier audit ruled out — and what changed in 2026

Its §2 rejection table is right in spirit and **stale in three rows**. Restated
with 2026 evidence:

| Option | Earlier verdict | 2026 reality |
|---|---|---|
| Skip.tools | "Xcode plugin — needs macOS" | **Partly wrong.** Skip has Linux support: framework projects, and `skip android` driving the Swift SDK for Android. *Full app projects* still need macOS 15 + Xcode 16.4+. |
| Cross-compile Swift → iOS on Linux | "produces a binary you cannot run" | **Wrong — `xtool` builds AND signs AND installs to a real device from Linux.** The blocker moved from *technical* to *licence*. See §3. |
| SwiftUI-like on Linux | "no maintained option" | **Partly wrong.** `swift-cross-ui` (GTK4 backend) ships releases; OpenSwiftUI is active. Neither is source-compatible with SwiftUI. See §5. |
| Darling | "excludes Xcode" | Still true. Preview `0.1.20260222`; most GUI apps still don't run. |
| iOS Simulator on Linux | "closed, permanently" | Still true. Permanently. |

The new entry the audit never considered at all:

| Option | Verdict |
|---|---|
| **Official Swift SDK for Android** (Swift 6.3, March 2026) | ✅ **Real, first-class, Linux-hosted.** Ships alongside the Static Linux and WebAssembly SDKs. This is the biggest change in the Swift landscape since the audit was written. |

---

## 3. xtool — the "iOS app built on Linux" path, and why we cannot ship it

`xtool` (xtool-org) is a cross-platform Xcode replacement. On Linux it will
**build, sign, and install a SwiftPM-based iOS app onto a real device**. It is
genuine engineering, not a toy.

**Why Yaver cannot bake it into a workspace image:**

- xtool needs the **iOS SDK and Swift toolchain out of `Xcode.xip`**. xtool itself
  ships none of it — setup asks the *user* to supply their own copy. That design
  is deliberate and correct on their side.
- Apple's agreement forbids installing Xcode/the SDK on non-Apple hardware, and
  separately forbids "upload to or host on any website or server, sell,
  redistribute, or sublicense the Apple Software."
- A Hetzner box is not Apple-branded hardware. **A Yaver image containing the
  Darwin SDK would be redistribution on a server — two violations at once.**

**Also technically limited today:** no support for most entitlements or app
extensions, and dependencies that are not pure Swift source generally don't work.

**The honest position:** xtool is a *user* choice on a machine they control, not a
Yaver capability. If a user brings their own legally-obtained SDK to their own
BYO workspace, that is between them and Apple. We do not ship it, do not bake it,
do not document it as a Yaver feature, and do not automate the `Xcode.xip` fetch.

> Guard to add: if `xtool` or a Darwin SDK artifact is ever detected in a
> **managed** (Yaver-billed) workspace image build, fail the build loudly. This
> is the same class as the Convex privacy test — a rule that only holds if
> something enforces it.

---

## 4. macOS in a VM on Linux — the answer is no, twice

**Technically:** OSX-KVM / Docker-OSX boot macOS under QEMU/KVM. Xcode needs
≥16 GB RAM to the guest and tens of GB of disk. USB passthrough for a physical
iPhone is described by everyone who has tried it as extensive configuration.

**Three independent blockers, any one of which is fatal for us:**

1. **Licence.** Same clause as §3. Apple permits macOS virtualization only on
   Apple-branded hardware. Running it on Hetzner is out of compliance, and Yaver
   is a commercial product — this is not the "personal tinkering" case.
2. **Hetzner has no nested virtualization** on cloud VMs. This is the same
   constraint that kills Android-emulator farms there (and why the
   android-emulator strategy exists in the shape it does). A macOS guest would
   need bare metal, which is a monthly-billed dedicated box — a direct violation
   of the **metered, never monthly, always-deletable** rule.
3. **The guest is x86 and the platform moved.** macOS 26 Tahoe is the **last**
   Intel-supporting release; macOS 27 is Apple-silicon only. Xcode 26 already
   ships a separate Apple-silicon build. Even a working Hackintosh guest is on a
   dead-end branch with a known expiry.

**Darling** avoids the licence problem (it reimplements Darwin rather than running
Apple's OS) and is still shipping previews — but it explicitly does not cover
Xcode or the simulator, and most GUI apps do not run. Not a path.

**Verdict: closed.** Record it here so no future session re-opens it.

---

## 5. The four things that *do* work on a Hetzner Linux box

Ordered by how much of a real Swift app they cover.

### 5.1 Compile + `swift test` — works for **every** Swift project

Including UIKit and SwiftUI ones, for the non-UI targets. The Swift toolchain is
genuinely cross-platform, and this is already wired (`SwiftRunsTestsOnLinux`
returns true for any detected kind, `swift_project_detect.go:203`).

For an **agent loop** this is most of the value: a runner that can edit, compile,
and run tests has a closed feedback loop even with no pixels. Do not undersell it
as a fallback — for `SwiftKindLibrary` it *is* the product.

Known trap to surface early: packages that `import Darwin` or lean on Apple-only
Foundation behaviour fail on Linux with errors that read like the user's fault. A
**compat lint** that flags missing `#if canImport(Darwin)` guards before the first
build is cheap and is a genuine differentiator (§8).

### 5.2 Server-side Swift — it *is* a web server

Vapor / Hummingbird / swift-nio. Detected (`SwiftKindServer`), routed to
`chrome-webrtc`. Nothing special needed: once it serves HTTP it is
indistinguishable from a Vite app to the rest of Yaver.

### 5.3 Swift → WebAssembly → browser → WebRTC

The landed path. **JavaScriptKit**, not Tokamak. Cost: DOM-style Swift rather
than SwiftUI-style. Honest framing is "Swift on the web", never "iOS on Linux".

Measured in-repo: the Swift fixture compiles to a **9.6 MB wasm artifact in
5.42 s** in `swift:6.3.0-jammy` (`workspace_preview_strategy.go:196-199`).

**No HMR** (`devserver_swiftwasm.go:100`). Every edit is a full wasm recompile +
page reload, so UI state is lost each time. Fine for a todo app; painful for a
deep navigation stack.

### 5.4 Swift → Android — the 2026 unlock

**Swift 6.3 ships the first official Swift SDK for Android**, owned and versioned
by the Swift project, hosted on Linux/macOS/Windows, built on the Android NDK.
Combine with **SkipUI** (reimplements SwiftUI on Jetpack Compose) and a real
SwiftUI codebase renders as a native Android app — compiled **on Linux**.

Two modes matter differently to us:

- **Skip Fuse (native)** — compiles Swift natively for Android via the official
  SDK. The recommended mode, and the one that aligns with a Linux host.
- **Skip Lite (transpiled)** — Swift → Kotlin source.

**The catch, stated plainly:** Skip's own docs say Linux/Windows support is
*preliminary* — it covers creating/building/testing/exporting **framework**
projects and running `skip android`, but **full app projects require macOS 15 +
Xcode 16.4+**. So this is not yet a turnkey "SwiftUI app on Linux". It is the
most promising direction, and it is moving fast.

**And the rendering problem is ours to solve, not Skip's:** an Android build needs
somewhere to run. **Hetzner has no nested virtualization**, so the AVD emulator is
out. The two viable targets:

- **Redroid** — Android in a container, sharing the host kernel. Already modelled:
  `HostCanRunRedroid` is Linux-only *because* it needs a real kernel, and returns
  false on macOS rather than promising a container that won't start
  (`preview_host_platform.go:54-60`). Capture is `h264-scrcpy`
  (`remote_runtime_capture.go:38-53`).
- **A real Android device** on the user's side, over the existing wire/WebRTC path.

**The margin catch, already encoded:** Redroid is the *only* strategy that forces
the `build` machine class (8c/16GB) instead of `standard` (2c/4GB) — it needs
~6.5 GB, and `devserver_kind.go:53-71` spells out that defaulting to it would
halve the margin on the $29 tier. So "SwiftUI renders on Linux via Skip+Redroid"
is technically the most exciting row in this document and the **most expensive**
one. Price it before promising it.

*(Naming trap: `cloud_emu_*` is **not** the Android emulator — it is
LocalStack-style cloud-service emulation, `cloud_emulator.go:24-40`. The Android
story is Redroid: `studio_base_cmd.go:44-65`, `android_resource.go:32-33`.)*

### 5.5 swift-cross-ui / OpenSwiftUI — watch, don't bet

`swift-cross-ui` has a GTK4 backend that works on Linux, plus experimental AppKit
and Qt backends. Actively released. **But**: "similar but not identical to
SwiftUI" — it is a *different framework*, so an existing SwiftUI app does not
port. Three years in, one release, docs explicitly deferred until it matures.

OpenSwiftUI is an open reimplementation of SwiftUI, self-described as for
educational and research purposes.

**Correction to the earlier audit:** its "no maintained SwiftUI-like option" is
too strong — maintained options exist. Its *conclusion* still stands, for a
different reason: they are not **source-compatible**, so they do not let a Swift
developer bring their app. Betting the product on either would be betting on
someone else's pre-1.0.

---

## 6. DECIDED: there is no managed Mac cloud workspace

**Decision (2026-07-22): Yaver does not resell or operate cloud Macs.** A Mac is
one of exactly two things:

1. **BYO Mac** — the user's own machine, paired into their mesh. This is the
   supported path and it is already built (§6.1).
2. **A macOS VM the user runs themselves** — their hardware, their licence
   exposure, their call. Yaver does not provision it, image it, or document it
   as a feature (§4 and §6.3).

The rest of this section is the evidence for that decision — keep it so nobody
re-opens "should we just rent Macs?" in three months.

### 6.0 Why not (the numbers)

For rows 3–4 (real SwiftUI/UIKit rendering, App Store archive, simulator) there
is no substitute for a Mac. The question is whether Yaver can offer one under its
own cost rules.

| Option | Price (2026) | Minimum commitment |
|---|---|---|
| Scaleway Mac mini M4 | ~€0.22/h (~€161/mo) | **24 h minimum lease** — licence-mandated |
| AWS EC2 `mac2.metal` | from $0.65/h | **24 h minimum host allocation** — licence-mandated |
| MacStadium | ~$109–119/mo (M2.S / M4.S) | monthly |
| Corellium (virtual iOS on Arm) | $10k+/yr | enterprise |
| Appetize (streamed simulators) | $40–400/mo | subscription |

**The structural finding:** Apple's SLA forces a **24-hour minimum allocation** on
every hourly cloud Mac. That is not a vendor pricing choice — AWS and Scaleway
both cite the licence. So:

> **A Mac cannot be scale-to-zero.** The smallest unit of Mac is a day. Hetzner's
> "delete it the moment it's idle" model has no Apple equivalent.

Why that kills the tier rather than merely complicating it:

1. **It breaks the one rule the cost model rests on.** Every Hetzner box is
   metered-and-deletable; a Mac's smallest unit is a day. A user who wants ten
   minutes of simulator costs us 24 hours.
2. **A pool would fix the economics and add an operational business.** One 24h
   allocation serving many users' short jobs is the only profitable shape — but
   that is a queue, capacity planning, lease management, and multi-tenant
   isolation on a machine we don't control the kernel of. That is a *hosting
   company*, not a dev tool.
3. **Corellium and Appetize don't help.** Corellium is kernel-level virtual iOS
   at $10k+/yr for AppSec; Appetize streams simulators running on *their* Macs.
   Neither removes the Mac from the chain — they relocate it, and rebill it.
4. **The user's own Mac is free and already legal.** Yaver's whole thesis is
   "your machine is your cloud". Here Apple's licence makes that not merely
   cheaper but the *only* clean answer.

### 6.1 The BYO-Mac path is already built (and better than expected)

Point 3 is not aspirational — `remote_builder.go` exists. A Mac opts in with
`yaver serve --builder-platforms=ios`, gets paired into `~/.yaver/builders.json`
(`yaver builder add|list|use|forget|ping`), and
`remote_runtime_dispatch.go:92-127` `pickBuilderForFramework` routes
`framework=="swift"` off any non-macOS host to it automatically.

**The architectural property that makes this cheap** is worth stating loudly
(`remote_runtime_dispatch.go:11-18`): the Linux box proxies **signaling only** —
SDP, control, lifecycle. After ICE, RTP media flows **direct viewer ↔ Mac**. The
Linux box never decodes a frame, never holds a Pion `TrackLocal`, and never
appears as an ICE candidate.

That means a Mac tier's Linux-side cost is ~zero, and the Mac is doing only what
*only* a Mac can do. It also means the builder registry is correctly kept out of
Convex (`remoteBuilderHostname` / `remoteBuilderTunnelToken` are on the
forbidden-keys list, enforced by `convex_privacy_test.go`).

**What is absent is now absent on purpose:** there is no Mac provisioner
(`cloud_provisioners.go:33-51` has no MacStadium / EC2-mac / Scaleway entry), no
queue, no capacity leases, no scheduling — and per §6 there should not be.
`pickBuilderForFramework:87-91` documents itself as the extension point for a
pool; **leave the hook, don't build the pool.**

So the shape is: **transport and dispatch are done; capacity is the user's.**

### 6.2 WebRTC for a Mac *workspace* (not just a Mac *builder*)

A Mac today is modelled as a **builder** — something a Linux workspace dispatches
to. The thing still missing is treating a Mac as a **first-class workspace** the
user opens directly, with the same WebRTC surface as a Linux one.

**The media plane already works and needs no new engineering.** The pieces:

| Piece | Where | Notes |
|---|---|---|
| H.264 from a booted simulator | `remote_runtime_video_track.go:239-262` | `xcrun simctl io <udid> recordVideo --codec=h264 -`, darwin-gated |
| fMP4 → Annex-B unpack → Pion samples | `h264_extract.go:7-11`, `remote_runtime_video_track.go:190-206` | iOS emits fragmented MP4; Android emits raw Annex-B |
| Capture-method choice | `remote_runtime_capture.go:38-53` | `h264-recordvideo` for iOS sims — chosen because screenshots measured ~18 s/frame |
| JPEG-over-DataChannel fallback | `remote_runtime_webrtc.go:111,577` | 60 KB cap, quality backoff |
| `events` DC + `feedback-launch-request` | `remote_runtime.go:1344-1350, 1430-1438` | `remote-runtime-feedback-v1` |
| Viewer input back to the device | `remote_runtime.go:615-745` | tap/swipe/pinch/navigate/text/back/home/key, single-writer lease |
| Signaling-only proxy | `remote_runtime_dispatch.go:11-18, 132-197` | RTP flows **direct viewer ↔ Mac** after ICE |

**So "WebRTC for a Mac cloud workspace" is not a streaming problem — it is a
workspace-lifecycle problem.** What a Mac lacks relative to a Linux workspace:

1. **Workspace identity.** A Mac is a `BuilderEntry` in `~/.yaver/builders.json`,
   not a workspace with a class, a lifecycle, or a preview strategy. It should be
   openable as a workspace, not only reachable as a build target.
2. **`ResolvePreviewForHost` on a Mac host is already correct** — it returns
   `Supported` for `PreviewIOSSimulator` when `HostCanRunIOSSimulator` is true
   (`preview_host_platform.go:46-52`, `:81-100`). The routing is there; nothing
   *offers* it as a place to work.
3. **Parity gaps to check, not assume:** does a Mac workspace get the same dev
   server, same task placement, same doctor probes, same wake/park? Grep each
   seam rather than trusting that "the agent is the same binary".
4. **Redroid is Linux-only** (`preview_host_platform.go:54-60`) — a Mac workspace
   cannot be the Android render target. A user who wants both surfaces needs both
   hosts, which the mesh already supports.

Concretely, the work is: let a paired Mac be **selected as the workspace**, not
just dispatched to; make the preview strategy resolve on *that* host; and surface
it everywhere a Linux workspace appears. The streaming is already built and
already correct.

### 6.3 The macOS-VM-on-Linux option, stated honestly

If "Mac = a VM on Linux" is to be one of the two options, it has to be the
**user's** VM on the **user's** hardware. Yaver's position:

- We do **not** provision it, ship an image for it, bake a Darwin SDK, or automate
  the `Xcode.xip` fetch. That would be redistribution on a server (§3) and
  running Apple software off Apple hardware (§4) — as a commercial product, with
  our name on it.
- We do **not** need to detect or block what a user runs on their own box. If a
  macOS VM appears in their mesh as a darwin host, the existing builder/workspace
  path treats it like any other Mac. That is the user's compliance decision.
- Practically, it will not run on our Hetzner fleet anyway: **no nested
  virtualization** on Hetzner cloud VMs, so it needs bare metal — which is
  monthly-billed and violates the metered-never-monthly rule as surely as a
  rented Mac would.
- And the platform is closing: macOS 26 is the last Intel release, macOS 27 is
  Apple-silicon only, Xcode 26 already ships a separate Apple-silicon build. An
  x86 Hackintosh guest is a dead-end branch with a known expiry date.

**Net: option 2 is real but it is not a Yaver feature — it is a thing some users
will do. Plan the product around option 1 (BYO Mac).**

---

## 7. Routing matrix (what a Swift-only user actually gets)

Supersedes §4 of the earlier audit. `xtool`/Skip rows are new.

| Detected kind | Host | Render target | Edit→visible | Feedback |
|---|---|---|---|---|
| wasm (JavaScriptKit) | Linux | headless Chrome → WebRTC | rebuild+reload, **measure it** | web SDK |
| Server-side Swift | Linux | headless Chrome → WebRTC | server restart | web SDK |
| Library / logic | Linux | **none — tests only, honestly** | `swift test` | n/a |
| Any project, non-UI targets | Linux | none | `swift test` | n/a |
| SwiftUI via Skip Fuse | Linux build → Redroid or real device | Compose UI | Android build | viewer-triggered |
| Apple SwiftUI / UIKit | **Mac only** | simulator / device | Xcode loop | viewer-triggered |
| Unknown | Linux | **none — say "unknown"** | — | — |

Two rules that must not be softened:

- **"Tests only" is a feature, not a refusal.** The flat "Swift: unsupported"
  turned away developers whose logic and tests would have run fine that day.
- **Never call a DOM render or a Compose render an iOS preview.** It isn't one,
  and the moment a user believes it is, every pixel difference becomes a bug
  report against us.

---

## 8. Making it fast — the "vibe" loop

The user's bar is RN: sub-second HMR. Swift will not meet it. What it *can* meet
is "fast enough that you stay in flow", and the gap is mostly avoidable waste.

**The wins, in order of payoff:**

1. **Warm the build cache.** A cold `swift build` refetches and recompiles every
   dependency. Persist `~/.swiftpm` and `.build` across sessions, and mount a
   **shared SwiftPM package cache across the fleet**. This is the single biggest
   lever and it is pure infrastructure — no Swift expertise needed.
2. **Bake, never install-at-first-run.** The toolchain + SDK are hundreds of MB.
   In the critical path that is minutes of spinner. Already the stated policy for
   `Dockerfile.yaver-swiftwasm`; it must hold for the Android SDK too.
3. **Measure edit→visible and publish the number.** The earlier audit estimated
   5–20 s on 2c/4GB and said explicitly it must be measured. **That measurement
   is still owed.** If it lands at 60 s, the honest answer is a bigger class or a
   Mac — not a shipped loop that feels broken.

   Precedent for why measuring beats assuming: on the Mac path, `simctl io
   screenshot` was measured at **~18 s/frame** on Xcode 26.4 — which is the entire
   reason `remote_runtime_capture.go` prefers `h264-recordvideo` over the obvious
   screenshot loop. Nobody would have guessed 18 seconds.
4. **Wire SourceKit-LSP.** It ships with the toolchain. Without it the workspace
   is a bare terminal; with it, completion and diagnostics catch errors before a
   30-second compile does. Cheapest latency win available.
5. **Preserve state across reloads where possible.** A full page reload loses the
   navigation stack. For a deep app that means re-navigating on every edit — the
   thing that makes a loop feel slow even when the number looks fine.
6. **On the Mac tier, use `Inject` / `HotReloading`.** Real SwiftUI hot reload,
   works on physical devices. If we ever run a Mac path, this is what makes it
   competitive with RN instead of a 30-second rebuild.

**A cheap trick worth taking seriously:** the earlier session found two Dockerfile
bugs by attempting the build locally for ~€0, before a 40-minute image build on a
billing box. Probe the operation locally first. That is the same principle as the
doctor probes — inventory lies, operations don't.

---

## 9. The seam that makes this a product, not a compromise

The interesting design is not "how much Swift fits on Linux". It is the
**handoff**, and Yaver is unusually well-positioned for it because the mesh
already exists.

```
Linux workspace (cheap, metered, scale-to-zero)
  ├─ edit, compile, swift test          ← 80% of iterations
  ├─ wasm / server-side preview → WebRTC
  └─ Skip → Android → Redroid or device
        │
        │  "I need the real thing"
        ▼
Mac in the user's mesh  (or a pooled 24h Mac)
  └─ simulator, archive, TestFlight     ← 20% of iterations
```

**The good news: the bottom half of that diagram is built.** `pickBuilderForFramework`
already turns "this host can't do Swift UI" into "dispatch it to the paired Mac",
and the refusal path is structured (`wrong_host_os`) rather than a compiler error
(`build_preflight.go:229-260`). The licence boundary is *already* a routing
decision, not a failure — which is precisely what the P2P model was built for.

**What is missing is the moment before it.** Today the handoff works if a builder
is already paired. If none is, the user gets a correct refusal and no next step.
The product gap is therefore not the transport — it is:

- **Discovery**: "you have a Mac in your mesh; pair it as a builder?" — the agent
  can already see the user's devices.
- **The zero-Mac case**: a user with no Apple hardware at all. Right now that is
  a dead end, and it is exactly the user this document is about. Their options are
  §5 (a real, useful Linux loop) or a Mac they don't own — and §6 says the second
  cannot be scale-to-zero. Say so plainly rather than implying a tier that
  doesn't exist.
- **Surfacing it**: `RemoteRuntimeCapabilities.RemoteBuilders` exists
  (`remote_runtime.go:57-73`) — the data is there for every surface to render a
  "run this on my Mac" affordance.

---

## 10. What is owed (honest backlog)

| # | Item | Why it matters |
|---|---|---|
| 1 | **Measure edit→visible on the wasm path** | Decides whether §5.3 is a product or a demo. Explicitly owed since the earlier audit. |
| 2 | **Fix the stale "no Swift feedback SDK" claim** — see below | A shipped SDK that the router denies exists. |
| 3 | Rename `SwiftKindTokamak` → a wasm-neutral name | Tokamak is dead; the constant now misdescribes what it routes. |
| 4 | Guard: fail any managed image build containing a Darwin SDK | §3 is a licence rule that only holds if something enforces it. |
| 5 | Evaluate Skip Fuse + Swift SDK for Android on a Linux box | The one path that renders *actual SwiftUI source* without a Mac. Preliminary today, moving fast. Cost it against the `build` class (§5.4). |
| 6 | Linux-compat lint for `import Darwin` / Apple-only Foundation | Converts "compiles on my Mac, fails on yours" into a pre-build warning. |
| 7 | SourceKit-LSP wired into the workspace surface | Cheapest latency win. |
| 8 | Shared SwiftPM cache across the fleet | Biggest build-time lever. |
| 9 | ~~Decide the Mac tier shape~~ — **DECIDED §6: BYO only, no managed Mac** | Keep the `pickBuilderForFramework` hook; do not build a pool. |
| 10 | **Make a paired Mac a first-class *workspace*, not just a builder** | §6.2. The WebRTC media plane is done; workspace lifecycle/selection/parity is not. |
| 11 | "Pair a Mac from your mesh" discovery + surfacing | §9: the handoff works, but only for users who already paired. |
| 12 | CI for `sdk/feedback/swift` | It exists, it is guarded for Linux with `#if canImport(UIKit)`, and **nothing verifies it builds**. |

### 10.2 The stale claim, stated precisely

`sdk/feedback/swift/` **exists** — `Package.swift` (tools 5.7, zero deps),
`Sources/YaverFeedback/{YaverFeedback.swift,FeedbackTypes.swift}`, with every
UIKit use behind `#if canImport(UIKit)`. So does `sdk/feedback/kotlin/`. Both
landed in `ab5d4ae65` — *"feat(sdk): Yaver Feedback SDK for Kotlin and Swift +
native reload audit"*.

The Go layer still asserts the opposite, in three places:

- `workspace_preview_strategy.go:80-88` — *"there is NO native Kotlin or Swift
  feedback SDK…"*
- `workspace_preview_strategy.go:319-343` `FeedbackSDKPackage` — returns `""` for
  swift, with a comment about not inventing a package that does not exist.
- Consequence: every Swift plan gets `FeedbackViewerTriggered`
  (`workspace_preview_strategy.go:230`, `swift_project_detect.go:166`,
  `preview_transport_route.go:247`) even though the in-app SDK is right there.

This is the **inventory-vs-operation** failure inverted: the inventory says *no*
while the operation says *yes*.

**Three more places carrying the same stale claim** — fix them in one change:

- `CLAUDE.md`, "Validation apps live OUTSIDE this repo" table — still lists
  kt/swift as "❌ **none exists** — viewer-triggered only".
- `CLAUDE.md`, "Three facts worth not rediscovering" — *"There is no native
  Kotlin/Swift feedback SDK. `sdk/feedback/` ships react-native, web, flutter,
  unity, browser-extension."*
- `sdk/feedback/README.md` — check it lists the two new packages.

Related and still true: `remote_runtime.go` returning
`FeedbackSDKCompatible: mode == ExecutionModeNativeWebRTC` was previously a lie
for swift/kotlin. With `ab5d4ae65` landed it has become *accidentally correct* —
worth re-reading the claim on purpose rather than leaving it right by luck.

---

## 11. One-paragraph answer, for when someone asks in chat

> Swift compiles and tests on Linux for **every** project, and that alone closes
> an agent's feedback loop. Swift→wasm and server-side Swift render on Linux today
> and stream over the existing WebRTC path. **SwiftUI and UIKit cannot render
> without a Mac** — not for engineering reasons but because Apple's licence
> restricts the SDK and simulator to Apple hardware, which also kills macOS-VMs,
> baking `xtool`'s Darwin SDK into our images, and (via a 24h minimum lease)
> any scale-to-zero Mac. The 2026 opening is the **official Swift SDK for
> Android** plus Skip, which renders real SwiftUI source as Compose from a Linux
> host. Until that matures, the right product answer is: do 80% of the loop on a
> cheap Linux box, and hand the last 20% to a Mac in the user's own mesh.
> **We do not sell cloud Macs.** The 24-hour minimum allocation makes a Mac the
> one machine that cannot be scale-to-zero, so a Mac is either the user's own or
> a VM they choose to run themselves. The WebRTC path to it is already built —
> RTP flows direct viewer↔Mac, and only signaling passes through our Linux box.

---

## Sources

- [Xcode and Apple SDKs Agreement (Apple)](https://www.apple.com/legal/sla/docs/xcode.pdf)
- [xtool — cross-platform Xcode replacement](https://github.com/xtool-org/xtool) · [Swift Forums thread](https://forums.swift.org/t/xtool-cross-platform-xcode-replacement-build-ios-apps-on-linux-and-more/79803) · [OSnews](https://www.osnews.com/story/142331/xtool-cross-platform-xcode-replacement-for-linux-windows-and-macos/)
- [Announcing the Swift SDK for Android (Swift.org)](https://www.swift.org/blog/nightly-swift-sdk-for-android/) · [Exploring the Swift SDK for Android](https://www.swift.org/blog/exploring-the-swift-sdk-for-android/) · [Official Android support in Swift 6.3 (Skip)](https://skip.dev/blog/swift-63-android-support/)
- [Skip FAQs — platform requirements](https://skip.dev/docs/faq/) · [Skip Getting Started](https://skip.dev/docs/gettingstarted/) · [Native and Transpiled Modes](https://skip.tools/docs/modes/) · [skip-ui](https://github.com/skiptools/skip-ui)
- [swift-cross-ui](https://swiftpackageindex.com/stackotter/swift-cross-ui) · [OpenSwiftUI](https://github.com/OpenSwiftUIProject/OpenSwiftUI) · [Tokamak (read-only, unmaintained)](https://github.com/TokamakUI/Tokamak)
- [Docker-OSX](https://github.com/sickcodes/Docker-OSX) · [Is Hackintosh/OSX-KVM/Docker-OSX legal?](https://sick.codes/is-hackintosh-osx-kvm-or-docker-osx-legal/) · [Running Xcode in Linux (Baeldung)](https://www.baeldung.com/linux/xcode)
- [Darling](https://www.darlinghq.org/) · [Darling on GitHub](https://github.com/darlinghq/darling)
- [Amazon EC2 Mac FAQs — 24h minimum](https://aws.amazon.com/ec2/instance-types/mac/faqs/) · [EC2 Mac instances](https://aws.amazon.com/ec2/instance-types/mac/) · [Scaleway Apple silicon pricing](https://www.scaleway.com/en/pricing/apple-silicon/) · [Scaleway Mac mini M4](https://www.scaleway.com/en/mac-mini-m4/) · [MacStadium pricing](https://macstadium.com/pricing)
- [macOS 26 is the last Intel release (The Register)](https://www.theregister.com/2025/06/10/apple_macos_26_last_intel_support/) · [Separate Xcode 26 build for Apple silicon (9to5Mac)](https://9to5mac.com/2025/08/05/apple-now-offers-a-separate-xcode-26-beta-build-for-apple-silicon-macs/)
- [Inject — hot reloading for Swift](https://github.com/krzysztofzablocki/Inject) · [HotReloading](https://github.com/johnno1962/HotReloading)
- [Hetzner nested virtualization limits (SSD Nodes)](https://www.ssdnodes.com/blog/hetzner-alternatives-2026/) · [Android emulator needs KVM](https://github.com/dmgolembiowski/android-emulator-container-scripts/blob/master/cloud-init/README.MD)
