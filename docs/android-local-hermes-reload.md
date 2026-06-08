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

**Decision: ship a pinned `aarch64-linux-gnu` hermesc as a `yaver-models`
release asset**, dropped at `/usr/local/libexec/yaver/hermesc` so
`findSystemHermesc()` (`desktop/agent/hermesc_resolver.go:53-66`) finds it
first. ~20 MB. **Pinned to the container's Hermes bytecode version**
(`YaverSDKManifest.hermesBytecodeVersion`) and rebuilt whenever the container's
Hermes bumps.

Why not the alternatives:
- Embed in `libyaver.so` (`//go:embed linux/arm64` in
  `desktop/agent/hermesc_embedded.go:13-42`, which today only has
  darwin-arm64/darwin-x64/linux-x64) — bloats the agent `.so`, same
  rebuild-on-bump constraint, no upside over a fetched asset.
- Build-from-source on first run (`buildProjectHermesc()`) — needs
  `cmake/gcc/g++/python3` baked in (~300 MB), 1-2 min first-run stall, thermal.
- Dev-Hermes container (run source JS, no hermesc) — bloats the app binary and
  diverges the store build from a normal release build. Rejected.

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
2. **P1 — arm64 hermesc (Option A)**: cross-build, ship as `yaver-models`
   asset, wire `findSystemHermesc()` rootfs path. Verify `/dev/build-native`
   against `127.0.0.1:18080` end-to-end.
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
  this has executed on real hardware yet.
- Confirm the baked Alpine rootfs asset exists on the `yaver-models` release
  (the rootfs itself was listed as not-yet-published in
  `docs/coding-agent-on-device.md`).
- Decide hermesc cross-build host (CI vs local) and pin to current container BC
  version.
