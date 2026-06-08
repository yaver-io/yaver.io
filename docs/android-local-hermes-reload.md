# Android full-local Hermes reload — design

**Status:** design-only (2026-06-08). No code yet. Decisions locked below.

**Goal:** On Android, a user starts the Mobile Sandbox (or clones an existing
git repo) on the phone, edits with the AI agent (or via tasks / the Yaver
feedback SDK), and the app **compiles + Hermes-reloads entirely on-device** —
then commits, pushes, and deploys, with ship steps routed **platform-aware**
(web/Convex local; native store builds → farm). iOS stays "edit on phone,
build on a machine" by OS law; Android does not have to.

> Markdown drifts. Every file:line below was accurate when written — re-grep
> before acting on it.

---

## 0. The reframe — the phone is already a box

The hard infrastructure ships in the APK today. This is a **wiring + artifact**
problem, not an architecture problem.

- **`libyaver.so` = the full Go agent, cross-compiled `GOOS=android
  GOARCH=arm64`, static (`CGO_ENABLED=0`)** — `scripts/build-android-sandbox.sh`.
  Runs as a normal Android process, serves the whole agent HTTP surface on
  `127.0.0.1:18080` — `mobile/android/app/src/main/java/io/yaver/mobile/sandbox/SandboxService.kt:99`.
- **proot + Alpine arm64 rootfs**, pre-baked with `node · npm · git · ripgrep ·
  bash · @anthropic-ai/claude-code · @openai/codex · opencode` —
  `RootfsInstaller.kt:15-19`. Fetched from the `kivanccakmak/yaver-models`
  release on first run (not in the APK — keeps it small).
- **The phone registers itself as a synthetic device** `__this_phone__` at
  `http://127.0.0.1:18080` in `connectionManager` — `mobile/src/lib/localBox.ts:17-57`,
  `mobile/src/lib/sandboxControl.ts:100-104`. The app already talks to this
  loopback agent exactly like a remote box.
- **`desktop/agent/sandbox_proot.go`** wraps runner subprocesses into the rootfs
  (`-r rootfs -b /dev -b /proc … -w workdir`), mirrors creds in, sets
  `PROOT_NO_SECCOMP=1`. The agent process is native-Bionic; only subprocesses
  (node, git, hermesc, claude) run inside the glibc Alpine rootfs via ptrace.

**Therefore:** the existing `/dev/build-native` flow in
`mobile/app/(tabs)/hotreload.tsx:569` (`fetch(\`${baseUrl}/dev/build-native\`)`)
needs **no new local-build orchestrator**. Point `baseUrl` at
`127.0.0.1:18080` and the *same agent Go code* runs Metro + hermesc in proot
and returns the same response shape.

---

## 1. LOCKED DECISION — arm64 hermesc (Option A)

Real HBC is required on-device (see §1a for why source-JS does **not** work).
The compiler artifact is the one genuine blocker.

**Decision: ship a pinned aarch64 hermesc as a `yaver-models` release asset**,
dropped at `/usr/local/libexec/yaver/hermesc` so `findSystemHermesc()`
(`desktop/agent/hermesc_resolver.go`) finds it first. ~20 MB. **Pinned to the
container's Hermes bytecode version** (`YaverSDKManifest.hermesBytecodeVersion`)
and rebuilt whenever the container's Hermes bumps.

**Confirmed during P1: the rootfs is Alpine = musl libc, not glibc.** Meta's
prebuilt linux hermesc is glibc-linked → it can't run in the rootfs. The binary
must be **musl-linked**, which the build script gets for free by compiling
*inside* Alpine arm64. The container is RN 0.81.5 / Expo 54, Hermes ref
`hermes-2025-07-07-RNv0.81.0-e0fc67142ec0763c6b6153ca2bf96df815539782` (read
from `mobile/node_modules/react-native/sdks/.hermesversion`) — the build MUST
use that exact facebook/hermes commit or the BC version won't match.

Build tooling: **`scripts/build-hermesc-alpine-arm64.sh`** (committed) —
`docker buildx --platform linux/arm64`, builds the `hermesc` target inside
Alpine, exports just the stripped binary, prints sha256. Runs on any Docker+
buildx host (native on Apple Silicon / the Hetzner arm64 box; QEMU-emulated on
x86). **This is the bottleneck artifact — still needs to be run + the output
baked into the rootfs / published as a `yaver-models` asset.**

Why not the alternatives:
- Embed in `libyaver.so` (`//go:embed linux/arm64` in
  `desktop/agent/hermesc_embedded.go:13-42`, which today only has
  darwin-arm64/darwin-x64/linux-x64) — bloats the agent `.so`, same
  rebuild-on-bump constraint, no upside over a fetched asset.
- Build-from-source on first run (`buildProjectHermesc()`) — needs
  `cmake/gcc/g++/python3` baked in (~300 MB), 1-2 min first-run stall, thermal.
- Dev-Hermes container (run source JS, no hermesc) — bloats the app binary and
  diverges the store build from a normal release build. Rejected.

### 1c. LOCKED-IN-PROGRESS — on-device build execution & binding model (the P1b fork)

Confirmed by reading the code during P1, and **the real core of on-device
build**: the build execs are not sandbox-wired, and there is no project→rootfs
binding.

- `/dev/build-native` execs Metro (`bundleCommand`, `devserver_http.go:3080/3097`),
  the dep installs (`devserver_http.go:840-935`), and hermesc
  (`devserver_http.go:3296`) **WITHOUT** `sandboxWrapCmd`. Only the PTY
  (`console_terminal.go:103`) and task runners (`tasks.go:1168/1360/2404/3436`)
  are wrapped. So on Android these build steps run natively under Bionic (no
  node, no hermesc) and fail.
- `sandboxWrapCmd`/`buildProotArgv` (`sandbox_proot.go`) treat `cmd.Dir` as a
  path **INSIDE** the rootfs (`-w workDir`). There is **no bind of an Android-fs
  project dir into the rootfs anywhere.** Task runners set `cmd.Dir =
  tm.workDir` and wrap — i.e. the current model assumes the project already
  lives at a rootfs-internal path (e.g. `/root/...`). The on-device coding flow,
  however, stores project files on the **Android fs** (isomorphic-git over
  expo-file-system: `documentDirectory/phone-projects/<slug>`). These two have
  never been reconciled — none of it has run on a physical device.

**The fork:** how do Metro/hermesc (running inside proot) see the phone-edited
project + write build artifacts?

**Recommended model — bind the host workDir + a shared tmp into the rootfs:**
generalize `sandboxWrapCmd` so that when `cmd.Dir` is an existing **host** path
(native fs), it adds `-b <hostDir>:/workspace -w /workspace` instead of treating
`cmd.Dir` as rootfs-internal; and bind a shared build tmp
(`-b <hostTmp>:/tmp/yaver-build`) so hermesc's input JS bundle + output HBC live
on a path visible both to the native agent (which writes/reads them) and to the
proot'd hermesc. Heuristic keeps it backward-compatible: an existing native dir
→ bind at `/workspace`; otherwise → existing `-w <rootfs-internal>` behavior
(PTY/`/root` unaffected). This unifies tasks + build under one rule.

Alternatives considered: (b) clone/copy the project into the rootfs before build
(double storage, sync headaches); (c) store all phone-local projects inside the
rootfs from creation (couples the JS coding flow to the sandbox lifecycle).
Bind-host-dir is the least invasive and matches how desktop already mounts the
project. **CAUTION:** `sandbox_proot.go` is shared with the on-device CLI/task
path — a parallel session may own active on-device sandbox work; coordinate
before changing the wrap semantics.

### 1a. Why source-JS is NOT an escape hatch (correction)

The Yaver container is a **release** Hermes build (`hermesEnabled=true`,
`mobile/android/gradle.properties`). Release Hermes strips the parser/compiler;
it executes **bytecode only**. The validator's legacy path lets plain-JS *bytes*
through (`YaverBundleValidator.kt:238-244` returns null on non-HBC magic), but
the runtime crashes at boot. Source-JS load only works in a Hermes VM built
*with* the compiler (RN dev builds). So on-device we need real HBC → real arm64
hermesc. This retracts the earlier "just skip hermesc" idea.

### 1b. hermesc must stay version-pinned to the container

Build it for the exact BC version the container's Hermes expects, or the
validator rejects the output with `BC_VERSION_MISMATCH`
(`YaverBundleValidator.kt:120-125`). Add a CI step to rebuild + republish the
asset on every container Hermes bump; gate it the same way the rootfs is
versioned (`RootfsInstaller` `.installed` marker + version stamp).

---

## 2. The JS-side loader gate (native side already done)

Discrepancy that makes this look harder than it is:

- The Android **native** loader is **fully implemented**:
  `mobile/android/app/src/main/java/io/yaver/mobile/YaverBundleLoaderModule.kt`
  (download → validate → save `filesDir/bundles/main.jsbundle` → reload),
  `MainApplication.getJSBundleFile()` wired to the saved path
  (`MainApplication.kt:62-65`), `MainActivity` reload receiver calling
  `recreate()` (Strategy A) + reflection in-place swap (Strategy B,
  `YaverBundleLoaderModule.kt:360-383`).
- But the **JS bridge gates Android off**: `mobile/src/lib/bundleLoader.ts:13-17`
  returns *"Loading apps inside Yaver is iOS-only today…"*, and
  `hotreload.tsx:518-524` shows an "iOS-Only For Now" alert.

**So enabling Android = implement the Android branch in `bundleLoader.ts` to
call the already-present native module + drop the `hotreload.tsx` gate.** The
scary part (native bundle swap on Android) is built.

Caveat: Strategy A (`recreate()`) tears down + rebuilds the activity — heavier
than iOS's in-place bridge swap, and it flashes. Strategy B (reflection into
`ReactHostImpl.loadBundle`) is the nice path but version-fragile. Strategy A is
fine for v1.

---

## 3. The reload-after-edit gap (both platforms)

Today even on iOS the on-device coding agent **edits but never reloads**:
`mobile/src/components/SandboxAiPanel.tsx:189-251` → `onApplied(mutatedPaths)`
only calls `refreshFiles()`. `callMobileHermesReload()`
(`mobile/src/lib/yaverMcpDirect.ts:133-140`) exists but isn't called from the
loop. `mobile/src/lib/codingSession.ts:274` literally says the Hermes-phone
path *"edits phone-local files and reaches for a machine to compile."*

**Close the loop:** agent/task/feedback completion → local `/dev/build-native`
against `127.0.0.1:18080` → `loadAppIfChanged()`
(`mobile/src/lib/bundleLoader.ts:59-82`, which short-circuits on MD5 match).

---

## 4. Testing surfaces — tasks & feedback SDK both route to the local box

Same agent, so both work against loopback with minimal change.

- **Tasks**: `POST /tasks` (`desktop/agent/tasks.go`, `httpserver.go`) runs a
  runner (claude/codex/opencode) in a workdir; on-device the runner executes in
  proot (`sandbox_proot.go` hooks `tasks.go` at spawn). `mobile/app/(tabs)/tasks.tsx`
  already streams output. Gap = nothing in task-complete triggers build+reload
  (§3).
- **Feedback SDK**: shake → `/feedback` → `desktop/agent/feedback_to_vibe.go` →
  task → edit → `mobile_hermes_reload`. Android **shake** already works
  (`mobile/src/lib/feedbackTrigger.ts:135-138`, native `YaverShakeDetector`).
  Gap = **screenshot capture**: `mobile/src/lib/feedback.ts:58-65`
  `captureScreenshot()` is a stub; no Android `YaverDogfood` native module
  (iOS has screenshot-notification + key-window render). Needs an Android
  native capture module (`Activity.registerScreenCaptureCallback` API 34+ /
  `MediaProjection` / `PixelCopy` of the root view).

---

## 5. Commit / push / deploy — platform-aware split (the whole point)

Edit + test are fully local on Android. Ship is a split.

| Step | Local on Android? | Mechanism | Notes |
|---|---|---|---|
| **commit + push** | ✅ | **isomorphic-git** on the JS side already (`mobile/src/lib/cloneToPhone.ts`, `codingAgent/codingAgentRun.ts` `makeGitTools`, `gitHubNetFromStore` token). Agent also exposes `/git/commit-push` (`desktop/agent/git_commit_push.go:60`) for in-proot. | Token already stored for clone; reuse for push. |
| **Convex deploy** | ✅ | `npx convex deploy --yes` (`desktop/agent/deploy_all_cmd.go:138`) — node in rootfs, runs in proot. Needs convex CLI in rootfs + deploy key from vault. | Cloud works; self-hosted targets on-box. |
| **Cloudflare web** | ⚠️ marginal | `wrangler deploy` + `@opennextjs/cloudflare` build — node-based, RAM-heavy in proot. | Gate to high-RAM devices. |
| **iOS TestFlight** | ❌ never | Xcode required. | Farm only — `desktop/agent/publish_worker.go` `darwin→[ios,android]`. |
| **Android Play (AAB)** | ❌ practically no | Gradle+JDK+Android SDK in proot is multi-GB + thermal. | Farm only (`linux→[android]`). |

The data model already encodes this: `mobile/app/(tabs)/publish.tsx:84` filters
to farm devices with non-empty `publishCapabilities`; the phone (`edge-mobile`)
advertises none — correct. Full-local just adds the branch: **phone does
commit/push + web/convex deploy; native store builds route to farm.** Chips are
already driven per-project by `mobile/src/lib/projectKind.ts`.

---

## 6. Compatibility wall — bcVersion / SDK manifest pinning

For arbitrary cloned repos (not Sandbox-scaffolded), the on-device HBC must
match the container's BC version + native-module set or the validator rejects
it (`YaverBundleValidator.kt:120-164`: `BC_VERSION_MISMATCH`,
`SDK_MANIFEST_MISMATCH`, `NATIVE_MODULE_INCOMPATIBLE`, `RUNTIME_FAMILY_MISMATCH`).

- **Mobile Sandbox scaffolds**: pinned to the container SDK → always match.
- **Cloned third-party repos** (`mobile/app/repo-coding.tsx` flow): only repos
  whose native surface ⊆ the container's can Hermes-reload — **same wall as
  remote build today**, not new. A repo needing a native module the container
  doesn't ship can't Hermes-reload *anywhere*; it needs `wire push` / a native
  rebuild. Set this expectation in the UI.

---

## 7. Risk list (resource/runtime, not architecture)

1. **Metro RAM** 1-2 GB + ~20-30% proot ptrace overhead; agent caps node heap
   at `--max-old-space-size=5120` (`desktop/agent/devserver_http.go:3108`) —
   lower on-device. **Gate to 8 GB+ devices.**
2. **Metro fs-watch under proot**: thousands of `stat`/`inotify` calls; inotify
   flaky under ptrace. Use polling-watch / `--no-watch` on-device.
3. **Thermal / Doze**: `SandboxService` holds `PARTIAL_WAKE_LOCK`
   (`SandboxService.kt:163-169`) — good, but long screen-off builds still risk
   throttle/suspend.
4. **Storage**: node_modules 300 MB-1 GB+, Metro cache 500 MB+, rootfs ~200 MB,
   hermesc ~20 MB. Needs cleanup/quota.
5. **HBC cache is the win**: `desktop/agent/hbc_cache.go` is content-addressable
   on the JS bundle hash and **skips hermesc on a hit**
   (`hermes_dev_compile.go:62-119`) — the difference between 12-27 s and ~3 s
   reloads. Keep it.

---

## 8. Platform-awareness matrix

| Capability | iOS | Android (target) |
|---|---|---|
| On-device agent (proot/CLI) | ❌ OS-forbidden | ✅ shipping (`libyaver.so`) |
| Edit (agent / tasks / feedback) | ✅ | ✅ |
| Local Metro bundle | ❌ | ✅ (node in rootfs) |
| Local HBC compile | ❌ | ⚠️ needs arm64 hermesc (§1) |
| Hermes reload (native loader) | ✅ | ✅ native built, JS gate off (§2) |
| commit / push | ✅ isomorphic-git | ✅ |
| Convex / web deploy | needs machine | ✅ in-proot (node) |
| iOS / Android store build | farm only | farm only |

---

## 9. Phasing

1. **P0 — Unblock the loader** — *code-complete 2026-06-08, pending on-device
   verification.* The Android native loader (`YaverBundleLoaderModule`) was
   already built **and registered** (`MainApplication.kt:30`), so
   `NativeModules.YaverBundleLoader` is truthy on Android — the TS load path
   already worked. The only blockers were UI gates. Done:
   - Added `isBundleLoaderAvailable()` to `mobile/src/lib/bundleLoader.ts`
     (capability check = `!!YaverBundleLoader`) + de-iOS'd the unavailable
     message (now web/old-build, not Android).
   - Flipped 5 `Platform.OS === "android"` gates → `!isBundleLoaderAvailable()`:
     `hotreload.tsx` (handleOpen), `apps.tsx` (open-in-Yaver + error hint),
     `_layout.tsx` (reload_bundle command), `tasks.tsx` (triggerHermesReload +
     ⚡ composer button visibility).
   - `tsc --noEmit` clean (0 errors project-wide).
   - **STILL TODO**: verify Strategy A reload on a physical Android device with
     a hand-built (remote-agent) HBC — needs `yaver wire push` from repo root +
     a device. This is the remote-build → Android-load path; proves the loader
     before P1 touches on-device compile. Note: `apps.tsx:1717`
     `handleDirectBuild` stays iOS-only (Xcode USB install) — correct.
2. **P1 — local on-device compile.** Split into:
   - **P1a — sandbox-aware hermesc resolution** — *code-complete 2026-06-08.*
     `findSystemHermesc()` now locates the prewarmed musl/arm64 hermesc inside
     the rootfs when the sandbox is active (env-gated, not GOOS), returning the
     rootfs-internal path; existence+type check only (no native --version probe
     under Bionic). 4 unit tests, `go build`/`go test` green. Inert until P1b
     wraps the exec — but no regression (android already failed).
   - **P1b — exec-wrap + binding model** (§1c) — *code-complete 2026-06-08,
     pending on-device verification.* Chosen model: **bind the host project dir
     into the rootfs at its OWN absolute path** (`-b workDir:workDir`, `-w
     workDir`) — because all build artifacts already live under
     `workDir/.yaver-build/` (`devserver_http.go:401/3273/3287`), every absolute
     path the build constructs resolves UNCHANGED inside proot, zero path
     translation. Done:
     - `sandbox_proot.go`: `buildProotArgv` gained variadic `extraBinds`;
       `sandboxWrapBuildCmd` (binds an existing-host-dir `cmd.Dir` at its own
       path, leaves rootfs-internal dirs as plain `-w`); `sandboxBuildEnv`
       (layers caller's NODE_OPTIONS/NODE_ENV/EXPO_*/Convex over the rootfs
       PATH/HOME, drops host-specific keys); `ensureSandboxCredDirs` factored out.
     - `devserver_http.go`: wrapped the 3 build execs — Metro/Expo
       (`bundleCommand`), hermesc, and project dep install — with
       `sandboxWrapBuildCmd` (no-op off-sandbox).
     - 5 new unit tests, `go build`/`go test`/`go vet` green; desktop build path
       unaffected (wrap is a no-op when the YAVER_ANDROID_* env is absent).
     - **NOT wrapped** (deferred): the corepack/global-tool setup
       (`devserver_http.go:840-852`) and the hermesc `--version` doctor probe
       (`hermescSummaryAt`) — would mis-report on android-sandbox; polish later.
     - Edited shared `sandbox_proot.go` with user's OK to manage parallel-session
       collisions.
   - **P1c — build the binary + rootfs** — *built + locally verified 2026-06-08
     (commits 39c862a1, 2e6214ab).* See §11 for the full artifact report. hermesc
     produces BC-version-96 HBC; the 38 MB rootfs runs node+hermesc when extracted.
     **Pending (needs explicit go + device):** publish the tarballs as
     `yaver-models` assets and wire enablement (see §11).
3. **P2 — Close the loop**: trigger local build + `loadAppIfChanged()` from
   coding-agent / task / feedback completion (§3).
4. **P3 — Android screenshot capture** native module (feedback visual) (§4).
5. **P4 — Platform-aware ship**: phone commit/push + convex/web deploy local;
   native store builds → farm via existing `publishCapabilities` (§5).
6. **P5 — Hardening**: 8 GB gate, heap cap, Metro polling-watch, storage quota,
   thermal backoff (§7).

---

## 10. Open / to-verify before P0

- Confirm `YaverBundleLoaderModule.kt` is actually registered in the running
  build (package + `getJSBundleFile` wiring) on a physical device — none of
  this has executed on real hardware yet. (Source is registered:
  `MainApplication.kt:30`.)
- Decide hermesc cross-build host (CI vs local) — DONE, built locally (§11).

---

## 11. Built artifacts + remaining wiring (2026-06-08 overnight)

Both build tools are committed and their outputs were built + verified on the
dev Mac (Docker, native arm64 — **no Hetzner spend**). Artifacts live under
`out/` (gitignored), not published.

### hermesc — `scripts/build-hermesc-alpine-arm64.sh` (commit 39c862a1)
- musl/arm64, **HBC bytecode version 96** (matches the container; RN 0.81).
- Self-contained: bundles its ICU trio + libstdc++ + libgcc_s next to the binary
  with a baked rpath of `/usr/local/libexec/yaver`, so it's decoupled from the
  rootfs's own ICU/toolchain versions.
- Verified in a bare Alpine 3.20 (≠ its 3.17 build base, no `apk add`): runs and
  compiles JS→HBC, output header magic `0x1F1903C1` + version `96`.
- Hard-won build facts: Alpine **3.17** (musl 1.2.3 keeps the LFS64 aliases
  LLVH needs; 3.18+ dropped `lseek64`) + `-D_LARGEFILE64_SOURCE`; **dynamic, not
  -static** (Alpine's `libicudata.a` is a 1.3 KB stub); `HERMES_UNICODE_LITE`
  does **not** bypass the ICU requirement in this ref.

### rootfs — `scripts/build-android-rootfs-alpine-arm64.sh` (commit 2e6214ab)
- The `yaver-rootfs-alpine-arm64` asset (node 20 + npm + git + ripgrep + bash +
  hermesc baked at `/usr/local/libexec/yaver/`), **38 MB** gz, lean base.
- **Built sha256 `131aa5685838300afb789c82fc7f4f2eff324f8e8b352199b612167fd0ef2b57`,
  version `2026-06-08-1`.**
- Verified: extracted exactly as `RootfsInstaller.kt` would, chroot'd in, ran
  `node v20.15.1` + hermesc JS→HBC (magic+BC96). Full on-device build path proven
  (chroot ≈ proot).
- **Coding CLIs (claude/codex/opencode) NOT in this rootfs** — they install but
  expose no bin on musl/arm64 (separate cli-on-device problem). The hermes-reload
  path needs only node+hermesc, so this base is sufficient for it; cli-on-device
  needs a follow-up (likely fetch the platform binaries / fix bin linking).

### THE blocker that's bigger than hermesc: enablement is UI-orphaned
`installRootfs` / `startSandbox` (`mobile/src/lib/sandboxControl.ts`) have **no
callers anywhere in the app** — nothing triggers rootfs download/extract or
starts the sandbox. So even with a published rootfs + working hermesc, a user
can't turn the on-device sandbox on. This mirrors the iOS repo-coding
UI-orphan pattern. **The real next build** is an enablement surface (settings/
onboarding) that:
1. calls `installRootfs(url, sha256, version)` with a progress UI, using the
   pinned sha/version above and the published asset URL;
2. calls `startSandbox(token)` → registers `__this_phone__` (`localBox.ts`);
3. surfaces status. Then the reload/tasks/feedback flows that already route to
   the local box (`codingExecution.ts`) light up.

### Exact remaining steps (need explicit go + a physical Android device)
1. **Publish assets**: create `kivanccakmak/yaver-models` (doesn't exist yet),
   `gh release upload` both tarballs. (gh is authed with `repo`/`write:packages`.)
   Outward-facing — left for an explicit go.
2. **Wire enablement UI** (above), pinning sha `131aa568…` / version
   `2026-06-08-1` / the asset URL.
3. **Device-verify** the whole chain: enable sandbox → `/dev/build-native`
   against `127.0.0.1:18080` builds HBC in-proot → `loadAppIfChanged` mounts it.
4. **P1b polish**: wrap the corepack/global-tool setup + the `hermescSummaryAt`
   `--version` doctor probe (would mis-report on android-sandbox).
5. Then **P2** (reload-after-edit), **P3** (Android screenshot capture).
