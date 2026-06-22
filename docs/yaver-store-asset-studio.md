# Yaver Store Asset Studio — production specification

**Status:** production-spec + PARTIALLY BUILT, 2026-06-08 (uncommitted). The
permission-video path is built end-to-end and the redroid recipe is verified on a
real on-prem x86 box. Built (desktop/agent/studio/): runner.go (Local/SSH
runners), surface.go (CaptureSurface+Driver), redroid.go (verified Android
driver), ios.go (simctl/idb iOS surface), flow.go (record + timed caption cues +
permission-proof scene + account steps + orchestrator), permission.go + prose.go
(analyzer + prose), compositor.go (ffmpeg drawtext). Wired: `yaver studio
permission-video [--capture]`, POST /studio/permission-prose, ops verb
`studio_permission_prose`, mobile studioClient.ts + app/studio.tsx + Settings
entry, web CapabilityShelf `studio` card, `studio` credit meter (1.6×) in
managedServices.ts + managedMeter.ts. Remaining: screenshots, store upload,
multi-locale, shared-runner isolation (P6), in-app sign-in flow. This doc names
the exact tables, meter kinds, HTTP routes, package layout, surfaces, isolation
model, and per-phase acceptance criteria. Grounded in code (§4).

> **Read the code, not this doc.** Every file path + "works/stub" claim was true
> on 2026-06-08. grep before building on any single line. When this doc and the
> code disagree, the doc is the bug.

---

## 0. What we're building (one paragraph)

A **metered, multi-surface store-asset generator** for third-party app
developers. An app owner points Yaver at their repo and a runner (their own
machine, an operator-fleet box, or a Yaver-cloud farm box), picks the assets
they want — App Store / Play **screenshots**, **app-preview / promo videos**,
**permission-justification videos + prose**, **feature graphics / icon sets** —
and Yaver builds the app, drives it on a real Android/iOS surface, records,
composites (frames + captions + localization), validates against store specs,
and (optionally) uploads + submits. Every run **consumes prepaid credits**
through the existing managed-meter spine, with the honest "run it on your own Mac
= free" exit always visible. Driven from **CLI, MCP (agent), the Yaver mobile
app, or the web dashboard** — same agent backend, four front doors.

---

## 1. Thesis

Every app release demands a non-code last mile the stores gate on: per-device,
per-locale screenshots; 15–30 s preview videos; permission-justification videos
*with prose* (`FOREGROUND_SERVICE_SPECIAL_USE`, `MANAGE_EXTERNAL_STORAGE`,
background-location, accessibility); feature graphics; icon sets; metadata. This
is deterministic, repetitive, device-farm-shaped toil — *exactly* what Yaver
already does for itself (`yaver shots`). The incumbents are Fastlane (you bring
the Mac, the farm, the glue) and screenshot SaaS (roundtrip, per-seat). Yaver's
wedge — "lower dev opex, kill the SaaS roundtrip" — applies cleanly: do it on the
user's own runner when they have one (free), on Yaver's farm when they don't
(metered, still cheaper than the SaaS + the Mac).

**Cold-open wedge:** the permission-justification video has *no incumbent*.
Fastlane doesn't do it; screenshot SaaS doesn't do it. "Yaver, make my Play
permission video" is the entry drug. Screenshots + preview videos are expansion.

---

## 2. Surfaces (four front doors, one backend)

All four call the same agent HTTP API (`/studio/*`) and the same Convex meter.
The agent is the single execution engine; surfaces are thin.

### 2.1 Mobile app (Yaver) — primary, per the owner's intent

Tab/screen: **Studio**. Flow:

1. **Pick app** — from the owner's connected repos (`code_repos` / `userProjects`)
   or the app currently loaded in the Hermes container.
2. **Pick assets** — toggle cards: Screenshots · Preview video · Permission video
   · Feature graphic · Icons. Each shows a credit estimate.
3. **Pick runner** — "This phone" (Android only, via the on-device sandbox —
   `SandboxService`/redroid-on-device is future), "My Mac/box" (a paired device),
   or "Yaver farm" (metered). Default = cheapest available the owner owns.
4. **Pick targets + locales** — iOS / Android, locale chips.
5. **Run** — live progress via the existing `vibe_preview` SSE/WebRTC stream
   (the capture is literally a vibe-preview session); watch the device drive
   itself. Review outputs inline (the `vibe_preview_clips` viewer already
   renders MP4 + poster).
6. **Ship or download** — one tap to upload+submit, or pull the artifacts.

Reuses: `app/local-box.tsx` patterns, the clips viewer, the SSE client, the
cockpit balance widget. New: `app/studio.tsx` + `src/lib/studioClient.ts`.

### 2.2 Web dashboard — co-primary

Route `@/dashboard/studio`, `StudioView.tsx`. Same five steps, bigger canvas:
side-by-side asset preview grid, per-locale tabs, drag-to-reorder screenshot
scenes, a live device mirror (WebRTC H.264 track — already built,
`remote_runtime_video_track.go`). Integrates the **capability shelf**
(`CapabilityShelf.tsx`) — Studio appears as a shelf card with its credit burn.

### 2.3 CLI — pro / CI path

```
yaver studio screenshots     [--targets ios,android] [--locales en-US,tr,de] [--submit]
yaver studio preview-video   [--targets ios,android] [--locales …] [--scene-flow path]
yaver studio permission-video --permission <PERM> [--target android|ios]
yaver studio icons | feature-graphic
yaver studio plan            # dry-run: show what it'd capture + credit estimate
yaver studio status <jobId>
```

Mirrors `shots_cmd.go` flag style. Honors `.yaver/studio.yaml` job spec.

### 2.4 MCP — agent path

`studio_plan`, `studio_run`, `studio_status`, `studio_permission_video`,
`studio_list_assets`. So host Claude Code (or the mobile assistant) can drive it.
Returned artifacts: for images, image content blocks (like `robot_camera`); for
video, a path + poster + a short MP4 the surface can fetch.

---

## 3. The credit-consuming model (production spec)

Drops into the existing meter with **zero new tables**.

### 3.1 Service key + meter kind

- Add `"studio"` to `MANAGED_SERVICE_KEYS` in `backend/convex/managedServices.ts`.
- Add `studio: "studio"` to `SERVICE_TO_METER_KIND` (its own ledger kind, so the
  cockpit shows Studio spend as its own line).
- Add `studio: 1.6` to `MARKUP_BY_KIND` in `managedMeter.ts` (env-overridable
  `YAVER_MANAGED_MARKUP_STUDIO`). Rationale: COGS is farm-minutes (Hetzner arm64
  ~€0.011/hr) + a thin render cost; the *value* is "no Mac, no Fastlane, no
  device" — but keep markup modest because a screenshot SaaS anchor (~$15–40/mo)
  already makes even 1.6× COGS read as nearly free.

### 3.2 Billable units (honest, COGS-anchored)

One `recordManagedUsage` row **per job**, `kind:"studio"`, with line items folded
into `quantity`/`providerCostCents`:

| Cost component | Unit | When charged |
|---|---|---|
| Farm capture | `farm-minute` | wall-clock the redroid/sim farm box is up for this job |
| Build | `build-minute` | if Yaver built the artifact (skip if owner supplied APK/IPA) |
| Render/composite | `render-minute` | ffmpeg compositor CPU time |
| Store upload | `upload` | flat, tiny — ASC/Play API calls |

`providerCostCents` = sum of real COGS; `chargedCents = ceil(COGS × 1.6)`.
**Runs on the owner's own runner cost the user `providerCostCents: 0` → charged
0 (free), still logged** (same pattern the CI meter already uses for self-hosted
runs — see `managedMeter.ts` `ci` comment). That *is* the BYO free exit.

### 3.3 Gating (defense-in-depth, already enforced)

`applyManagedUsage` already requires BOTH the global `YAVER_MANAGED_METER_LIVE`
flag AND per-user `userSettings.managedServices.studio === true` before a real
charge; otherwise it's simulated (`dryRun`). Launch posture stays dry-run. No new
gating code — wiring `kind:"studio"` inherits it.

### 3.4 Cockpit

`CapabilityShelf.tsx` CAPABILITIES array gets one entry:
`{ key:"studio", icon, title:"Store Studio", blurb:"Screenshots, preview &
permission videos — no Mac, no Fastlane", priceHint:"~credits/run", replaces:
"Fastlane + a screenshot SaaS + a spare device", meterKind:"studio" }`.
`burnBreakdownForUser` / `cockpitSummaryForUser` pick up the new kind
automatically (they aggregate by `kind` from `managedUsage`). No backend change
beyond the key.

### 3.5 Pre-run estimate

`studio plan` (CLI/MCP) and the mobile/web "Run" button call a new
`GET /studio/estimate?spec=…` on the agent → returns predicted farm/build/render
minutes × markup, shown before the user commits. The owner always sees the price
and the free-on-own-runner alternative before spending.

---

## 4. What already exists (grounded inventory)

| Capability | File(s) | Status |
|---|---|---|
| `yaver shots` iOS pipeline (sim→Maestro→sips→ASC upload→metadata→submit) | `shots_cmd.go`, `shots_capture.go`, `shots_asc.go`, `shots_analyzer.go`, `shots_http.go` | ✅ working |
| Embedded ASC Python (upload/set-info/submit) | `shots_scripts/*.py` | ✅ working |
| Expo-router flow auto-gen + committed override | `shots_analyzer.go` | ✅ working (Expo only) |
| Sim/emulator clip record → H.264 MP4 + poster | `vibe_preview_clip.go` | ✅ working |
| Maestro-exercise while recording (auto-gen YAML) | `vibe_preview_exercise.go` | ✅ working |
| Phone-recorded clip upload (ReplayKit/MediaProjection→HTTP) | `vibe_preview_clip_upload.go`, `YaverScreenRecorder.swift` | ✅ working |
| Live preview stream (SSE PNG + WebRTC H.264 track + JPEG-DC fallback) | `vibe_preview.go`, `remote_runtime_video_track.go`, `h264_extract.go`, `remote_runtime_streamer.go` | ✅ working |
| Appium DOM introspection + bug-hunter walk | `vibe_preview_appium.go` | ✅ working |
| iOS sim control (list/boot/screenshot) | `mcp_appdev.go` (`mcpSimulator*`) | ✅ working |
| Native build to artifact (xcode/gradle/flutter/expo/eas) | `builds.go`, `native_build.go` | ✅ working |
| Hetzner farm bootstrap (Android SDK+AVD arm64+ffmpeg+xvfb+H.264; qemu-TCG) | `ci/remote/bootstrap.sh`, `ci/hcloud/*` | ✅ working (TCG = slow) |
| Per-use meter spine (opt-in, wallet, markup, ledger, dry-run, cockpit) | `managedMeter.ts`, `managedServices.ts`, `CapabilityShelf.tsx` | ✅ working |
| Operator-fleet / remote runner provisioning + network jail design | `cloud_provisioners.go`, `docs/yaver-public-compute-operator-fleet.md` | ⚠️ ~80%, teardown gaps |
| Play screenshot upload (standalone) | `scripts/upload-playstore-screenshots.py` | ⚠️ exists, not wired |
| `adb_screenshot`/`adb_devices`/`adb_command` MCP | `mcp_dropped_stubs.go` | ❌ stub (dropped 2026-04-28) |

**Net:** the engines (capture, record, stream, build, meter, ASC upload) exist
and are production-grade. The *product* (Android parity, compositor, video
upload, permission videos, runner isolation, the four surfaces, metering) does
not. This is integration + the genuinely new bits, not a rebuild.

---

## 5. Architecture

```
 surfaces ─────────────────────────────────────────────────────────────
   mobile app.tsx / web StudioView / CLI / MCP   →  agent HTTP  /studio/*
                                                          │
 agent: studio package (desktop/agent/studio/) ───────────┼──────────────
   Job  →  Planner  →  CaptureDriver  →  FlowEngine  →  Recorder
                          │                                   │
                          ▼                                   ▼
                  ┌───────────────┐                  ┌────────────────┐
                  │ capture surface│  raw png/mp4     │  Compositor    │
                  │ sim / redroid  │ ───────────────▶ │ ffmpeg recipes │
                  │ / device       │                  │ frames+caption │
                  └───────────────┘                   │ +i18n +conform │
                          ▲                            └────────┬───────┘
                          │ build artifact                      │ store-ready
                  ┌───────────────┐                    ┌────────▼───────┐
                  │ Builder        │                    │ Publisher      │
                  │ native_build   │                    │ ASC / Play     │
                  └───────────────┘                    └────────┬───────┘
                                                                 │
 meter ──────────────────────────────────────────────────────── ▼
   recordManagedUsage(kind:"studio", farm/build/render/upload minutes)
```

Stage interfaces (Go):

```go
type CaptureSurface interface {     // where the app runs while we capture
    Provision(ctx, JobSpec) (Surface, error)   // sim | redroid | device | farm
    Install(ctx, artifact string) error
    Screenshot(ctx) ([]byte, error)
    RecordStart(ctx, RecordOpts) (RecordHandle, error)
    Teardown(ctx) error                          // MUST run (defer) — bills stop here
}
type FlowEngine interface {          // drives the app through scenes
    Run(ctx, Scene) error            // Maestro | Appium-DOM | committed YAML
}
type Compositor interface {          // ffmpeg-only, no GUI deps
    Frame(ctx, in, device string) (out string, err error)
    Caption(ctx, in string, cues []Cue, locale string) (out string, err error)
    Conform(ctx, in string, spec StoreSpec) (out string, err error)  // + validate
}
type Publisher interface {           // ASC + Play (+ unlisted video host)
    UploadScreenshots / UploadPreview / UploadPromoLink / SetMetadata / Submit
}
```

`CaptureSurface` is the seam that makes the **runner model** (§6) pluggable and
the whole thing testable: a `fakeSurface` lets the planner/flow/compositor/meter
paths be unit-tested with no device (per the repo's no-mocks-but-real-local
testing convention — fake is a real in-proc implementation, not a mock).

---

## 6. The runner model — "give the runner the owner's code" (the hard part)

Three runner classes, one isolation contract. This is where production-grade
security lives, and it reuses the operator-fleet design
(`docs/yaver-public-compute-operator-fleet.md`).

| Runner | Code gets there by | Isolation | Cost to user |
|---|---|---|---|
| **Owner's own machine** (paired device) | already has the repo, or `git` clone over the existing P2P tunnel | the owner's box, owner's trust | **free** (COGS 0) |
| **Operator fleet** (Yaver-run shared box) | shallow `git clone` of the owner's repo into an ephemeral per-job workdir | **per-job workdir + proot/container + network jail + teardown-on-done** | metered (farm-minutes) |
| **Yaver-cloud farm** (owner pays for a dedicated box) | same clone path | dedicated box, still per-job workdir + teardown | metered (compute + farm) |

### Hard isolation requirements (non-negotiable for shared runners)

1. **Per-job ephemeral workdir**, removed on teardown. No cross-tenant residue.
   (Closes operator-fleet gap C: allocations must be removable — wipe + proc-kill
   on done.)
2. **No secrets transit Convex or logs.** The owner's repo may carry a
   `google-services.json` / signing key. Studio needs an *installable* artifact,
   not signing — prefer the owner supplies a pre-built debug/unsigned APK/IPA, or
   builds happen on the *owner's* runner. If Studio builds on a shared box, it
   builds **debug/unsigned only** (screenshots/videos don't need release
   signing) and never touches the owner's keystore.
3. **Network jail** on shared runners: relay-only egress, RFC1918 blocked
   (reuse the operator-fleet jail). The app under capture must not phone home
   into the runner's LAN.
4. **Scoped token, never env-leak.** The runner uses a per-job scoped token, not
   the owner's auth token, and never `os.Environ` passthrough (operator-fleet gap
   D).
5. **Teardown is `defer`'d and metered-to-zero on it.** Billing stops at
   teardown; a hung job can't bill forever (cap + watchdog).

### Capture surfaces under a runner

- **iOS:** Apple-licensed simulators only run on macOS → iOS capture requires a
  **Mac runner** (owner's Mac or a Mac in the fleet). No Linux path. State this
  plainly in the UI (iOS screenshots/videos need a Mac).
- **Android:** **redroid (Android-in-Docker) on arm64** is the farm path — real
  AOSP on the host kernel via `binderfs`, **no KVM** (Hetzner Cloud has none).
  arm64 host runs arm64 APKs/native libs without translation. Record via
  `scrcpy --no-display --record` or `adb screenrecord`. The existing qemu-TCG AVD
  in `bootstrap.sh` is the slow fallback. **Risk:** host kernel needs
  `CONFIG_ANDROID_BINDERFS` + `--privileged`/ptrace for proot guests — verify on
  the image (Ubuntu 24.04 usually yes via `linux-modules-extra` + `modprobe
  binder_linux`).
- **Real device:** highest fidelity, via existing `wire`/`wireless`.

### 6.1 Proven redroid recipe (VERIFIED 2026-06-08 on `magara`)

End-to-end validated on a real on-prem box (Ubuntu 20.04, kernel 5.4.0, x86_64,
3.7 GB RAM, Docker 24.0.5, user in `docker` group but NO passwordless sudo). The
same recipe is what the Go redroid driver (§5) must encode, and it works
identically on a Yaver-managed-cloud box and an on-prem box — the only difference
is who owns the host:

1. **Load `binder_linux` without host sudo** via a privileged helper container
   (Docker daemon is root, so a `--privileged` container with `/lib/modules`
   mounted can `modprobe` into the host kernel):
   `docker run --rm --privileged -v /lib/modules:/lib/modules debian:bullseye-slim bash -c 'apt-get install -y -qq kmod; modprobe binder_linux devices=binder,hwbinder,vndbinder'`.
   Requires `linux-modules-extra-$(uname -r)` present (it was) and
   `CONFIG_ANDROID_BINDERFS=m`. This is the key on-prem unlock — most dev boxes
   give docker-group access but not sudo.
2. **Boot redroid** (privileged, it mounts binderfs itself):
   `docker run -itd --privileged --name … -v ~/redroid-data:/data -p 5555:5555 redroid/redroid:13.0.0-latest androidboot.redroid_width=1080 …`.
   **Booted in ~12 s.** Do NOT use `--rm` during bring-up — a crashed container
   self-deletes and you lose the logs.
3. **x86 hosts need an x86_64 build of the app's native exec.** Yaver's
   `libyaver.so` ships arm64-only; on an x86 box `SandboxService` would
   `stopSelf()`. Cross-compile it for android/amd64 — note `CGO_ENABLED=0
   GOOS=android GOARCH=amd64` FAILS ("requires external linking"); use the NDK:
   `CGO_ENABLED=1 GOOS=android GOARCH=amd64 CC=<ndk>/x86_64-linux-android24-clang
   go build -ldflags=-checklinkname=0 …` → a proper bionic PIE. Then build the
   universal APK from the AAB (`bundletool --mode=universal`), inject
   `lib/x86_64/libyaver.so`, `zipalign -p 4`, re-sign (`apksigner`, debug key is
   fine for redroid). The Studio builder must do this per-target-ABI automatically
   when the runner arch ≠ the app's native-lib arch.
4. **Install + drive over adb** (`adb connect 127.0.0.1:5555`), record with
   `adb screenrecord` (or scrcpy), pull, teardown.

This is `studio/redroid_capture.sh` (updated with the privileged-container binder
load). The Go driver wraps the same steps and runs them over the runner transport
(local exec on the runner, or P2P/relay from the agent).

### 6.2 Account provisioning for capture (the sign-in gate)

The feature being demoed often lives behind sign-in (Yaver's sandbox is in
Settings after auth). On a fresh redroid the app is signed out. Studio handles
this with a **throwaway capture account**: create/sign-in a disposable account in
the app on the capture surface before driving the flow (the runner has the repo
and can use the project's own auth/test path), then tear it down with the
surface. For third-party apps, the owner supplies test credentials or a
seed/deeplink that lands on the screen under demo. Never use the owner's real
account on a shared runner.

### 6.3 iOS parity

iOS app-preview and (where Apple asks for them) permission/review-notes videos
follow the SAME pipeline with a different CaptureSurface: a **Mac runner** (the
owner's Mac, a Mac in the managed fleet, or a paired Mac-mini — `Mobiles-Mac-mini`
is already in the fleet) running the iOS Simulator. The capture+record half
already exists: `shots_capture.go` (sim boot/install/Maestro/screenshot) +
`vibe_preview_clip.go` source `sim-ios` (`xcrun simctl io booted recordVideo`).
The Studio iOS driver reuses these; only the permission-analyzer needs an iOS
arm (parse `Info.plist` `UIBackgroundModes` / `NS*UsageDescription`) and the
prose template needs an Apple-review-notes variant. No Linux iOS path exists
(simulators are macOS-only) — Studio hard-routes iOS jobs to a Mac runner.

> **arm64 capacity reality:** Hetzner cax is frequently sold out across regions.
> The farm provisioner must **retry across {cax21,cax31,cax11}×{fsn1,nbg1,hel1}**
> and fall back to (a) the owner's own runner, (b) an x86 box with redroid x86
> for APKs that ship x86 splits (Yaver's own arm64-only libs won't load there),
> or (c) queue the job until capacity returns. Never hard-fail on "no arm64."

---

## 7. Asset matrix + store-spec validators

| Asset | iOS (ASC) | Android (Play) | Pipeline |
|---|---|---|---|
| Screenshots | 6.9"/6.7" 1290×2796, 6.1", 13" iPad; per-locale | phone + 7"/10" tablet, per-locale | capture→composite→upload |
| Preview/promo video | 15–30 s, device-exact res, H.264/HEVC, ≤500 MB | promo = **YouTube URL** (not a file) | record→composite→upload/link |
| Permission video | n/a (Apple = prose + review notes) | **unlisted link** in the declaration form | §8 |
| Feature graphic | n/a | 1024×500 | composite (static) |
| Icon set | from 1024² | 512² + adaptive | composite (static) |
| Metadata / what's-new | per-locale text | per-locale text | prose generator |

**Validators are data, not constants** (specs churn). A `StoreSpec` table of
`{platform, displayType, w, h, maxDurSec, codecs[]}`; `Conform` fails loudly with
the exact violated rule (reviewers reject on a 31 s video or a 1289-px-wide shot).
Platform hard-gate: a permission video for an Android perm must be captured on
Android — an iOS clip is auto-rejected by Play; Studio refuses to submit
cross-platform.

---

## 8a. Narrative use-case permission video (2026-06-22)

The original P0 recorded a *mechanical* proof (start service → notification →
home → stop). Reviewers need the **use case**, so there is now a narrative
variant that records a real, justifying story and is the default for the
first-class verb:

- `studio.UseCaseProofSteps` / `studio.UseCaseConfig` (flow.go) +
  `GenerateUseCaseJustification` (prose.go): open → start the feature → **give a
  real task** (`TaskSteps`, JSON-drivable via `taskActions`) → `WaitText`
  proof-of-work → expand the foreground notification → **background the app**
  (the captioned WHY: Android would kill the process and lose the in-flight
  work) → wait for completion in the background → reveal the **“task finished”
  notification** → stop. Captions + prose name the actual work, not generic
  steps. No new Driver verbs were needed.
- App side: `SandboxService.postTaskFinished` posts a dismissible Android
  completion notification (channel `yaver_tasks`, self-scoping to the hosting
  device), `updateStatus` reflects the running task in the ongoing FGS
  notification. Bridged via `YaverSandbox.notifyTaskFinished/setTaskStatus`,
  wired in the mobile Tasks screen on terminal task transitions.

**MCP / ops verbs (Yaver and any third-party app — supply your own
apk/package/manifest):**

- `studio_permission_video` — first-class one-call recorder. Defaults to the
  narrative video; `mechanical:true` for the bare proof. Fields: `permission`,
  `apk`, `package`, `manifest`, `activity`, `hostWorkDir`, `sshHost`, plus
  `useCase{whatRuns, startButtonText, stopButtonText, progressText,
  completionText, taskActions[]}`.
- `studio_job_start` — same, lower-level (pass `useCase` explicitly).
- `studio_permission_prose` — add `useCase:true` for the narrative prose.
- `studio_job_status` — poll; artifacts now fetchable over HTTP:
  `GET /studio/jobs/<id>/captioned` (or `/raw`, `/justification`) — Range-enabled
  so the web/mobile UI can stream/seek/download the recorded MP4.

**Publish requirement wiring:** `buildPermVideoCheck` (publish_status.go) detects
a declared `FOREGROUND_SERVICE_SPECIAL_USE` and adds a `permission-video`
blocker to the readiness checklist (the "Ready to ship?" banner) until a video
exists — committed under `yaver-store-assets/` or produced by a completed studio
job — with the exact generate command.

## 8. Permission-video subsystem (the wedge, P0)

New surface; everything else is integration. Steps:

1. **Static analysis** — `permission_analyzer.go`. Android: parse
   `AndroidManifest.xml` for the `<service android:foregroundServiceType=…>` whose
   type maps to the declared permission, read its
   `PROPERTY_SPECIAL_USE_FGS_SUBTYPE`, and locate the UI entry point that triggers
   it (grep the native-module start call → the screen that calls it). iOS: parse
   `Info.plist` `UIBackgroundModes` / `NS*UsageDescription`. Output: a
   `PermissionFacts{permission, serviceClass, subtype, triggerScreen, whatRuns,
   whyUninterruptible}`.
2. **Flow synthesis** — generate a scene that navigates to the trigger, taps it,
   waits for the foreground notification, backgrounds the app (`pressKey Home`),
   shows persistence, returns, stops. Heuristic first; AI+Appium fallback when the
   layout is unknown (`vibe_preview_appium.go` `PageSource()` → LLM picks taps
   toward the goal).
3. **Record** — `vibe_preview_clip` on the chosen surface while the scene drives.
4. **Composite** — overlay reviewer captions ("1. User enables feature →
   2. Foreground notification appears → 3. Still running backgrounded → 4. User
   stops it"), title card, optional device frame. `permission-proof` recipe.
5. **Prose** — generate the two Play Console fields from `PermissionFacts`:
   the task one-liner (Other) and the "why it must start immediately and cannot
   be paused/restarted" description. Template seeded from a known-good example
   (Yaver's own FGS justification).
6. **Host the link** — Play wants a URL: upload unlisted to YouTube (own OAuth)
   or self-host on R2 / `<deviceId>.yaver.io`. **Explicit, confirmed external
   action** (privacy: this publishes).
7. **Output** — `studio/permission/<perm>.mp4` + `.justification.md` + link.

**Dogfood = feature:** the first real run produces Yaver's own pending
`FOREGROUND_SERVICE_SPECIAL_USE` video (`SandboxService`, subtype
`on_device_coding_agent`, reached via Settings → "This phone as a box" →
`app/local-box.tsx`).

---

## 9. Flow authoring (graceful degradation)

1. **Committed** `.yaver/studio/<asset>.flow.yaml` — deterministic pro path
   (precedence like `findShotsFlow`).
2. **Heuristic synthesis** — extend `shots_analyzer.go` past Expo: Flutter routes,
   native Activity/VC graphs, tab bars; permission→trigger mapping (§8.1).
3. **AI + Appium goal-seek** — read live view tree, LLM chooses next tap toward a
   named goal. (The bug-hunter walk, repurposed.)

Always **seed the generated flow to disk** so the owner can commit + tweak it —
the flow is a reviewable artifact, breakage is debuggable.

---

## 10. Compositor (new, ffmpeg-only — keeps the single-binary promise)

ffmpeg is already a declared dep (`record_drivers`). Recipes are named data
templates (`clean-frame`, `captioned-walkthrough`, `permission-proof`):

- **Device frames** — transparent PNG bezels per device, `overlay` the
  screen scaled into the frame's screen-rect. Bezels are static assets fetched on
  demand from a `yaver-models`-style assets host (don't bloat the binary or the
  15 MB web cap).
- **Captions / title cards** — `drawtext` (bundled font), timed to scene cues.
- **Localization** — caption + metadata text from `.yaver/studio/strings.<locale>.yaml`;
  one composite pass per locale over the same capture when only overlay text is
  localized (cheap), one capture per locale when the app UI itself localizes
  (expensive, flag-gated, priced accordingly).
- **Conform + validate** — final `scale`/`pad`/`fps`/`-t` to the exact `StoreSpec`,
  hard-fail on out-of-spec.
- **Music/voiceover** — v2 (TTS via existing `say`/voice stack).

---

## 11. Data model + HTTP API

- **No new Convex billing tables** — reuse `managedUsage` (kind `studio`) +
  `prepaidCredits`. Privacy test (`convex_privacy_test.go`) already pins
  `managedUsage` fields; the new kind needs no new fields, so no privacy change —
  but add a `TestStudioUsageFields` assertion that a studio payload carries no
  path/secret.
- **Job state lives on the agent**, not Convex (jobs reference repos + artifacts =
  work-derived → never Convex). `studio/jobs.go` in-memory + on-disk
  `~/.yaver/studio/<jobId>/` (artifacts, flow, logs), like `builds.go`.
- **Agent HTTP**: `POST /studio/plan`, `POST /studio/run`, `GET /studio/status`,
  `GET /studio/estimate`, `GET /studio/jobs/<id>/<asset>` (serve artifact),
  reuse `/vibing/preview/*` SSE for live progress.

---

## 12. Privacy & security

- App binaries, repos, screenshots, recordings = **work-derived → never Convex**,
  P2P only. Convex sees the meter ledger (counters/labels/timestamps) — already
  privacy-pinned.
- Shared-runner isolation per §6 (ephemeral workdir, network jail, scoped token,
  unsigned builds, teardown wipe). These are the operator-fleet gaps; Studio must
  not ship the shared-runner path until they're closed (owner-runner + dedicated
  box are safe day one).
- External uploads (ASC, Play, YouTube/R2) **publish** — always an explicit,
  confirmed action showing the destination; never silent. Content may be cached/
  indexed even if later deleted.

---

## 13. Failure modes & edge cases

- **No arm64 capacity** → retry matrix → fall back to owner runner / x86 / queue;
  never hard-fail (§6).
- **Flow breaks on UI drift** → AI+Appium retry, then surface the seeded YAML for
  manual fix; partial assets still returned.
- **Build fails on shared runner** (missing secret) → tell the owner to supply a
  prebuilt artifact or run on their own Mac; don't ask for their keystore.
- **Hung capture** → watchdog + cap; teardown bills to zero on exit.
- **Store rejects generated video** → it must be *real* feature footage (redroid
  arm64 so the feature actually runs), not a stub screen; validators catch spec
  violations pre-submit.
- **iOS on Linux** → impossible; UI states iOS needs a Mac runner.
- **YouTube OAuth absent** → fall back to R2 / `<deviceId>.yaver.io` self-host
  link.

---

## 14. Phased build plan (each phase shippable, with acceptance criteria)

- **P0 — Permission-video MVP (wedge + dogfood).** Android, owner-runner or
  redroid. `permission_analyzer.go` + flow synth + record + `permission-proof`
  composite + prose generator + unlisted-link helper. *Accept when:* running it on
  Yaver's own repo produces a reviewer-ready `FOREGROUND_SERVICE_SPECIAL_USE`
  MP4 + justification.md, with a credit row logged (dry-run).
- **P1 — Android screenshot parity.** Restore an `adb screencap` capture path,
  wire `upload-playstore-screenshots.py`, mirror `yaver shots` for Play. *Accept
  when:* `yaver studio screenshots --target android` produces + uploads a phone
  screenshot set for a sample app.
- **P2 — Compositor.** Device frames + captions + `StoreSpec` validators across
  screenshots and videos. *Accept when:* a screenshot comes out framed + captioned
  + passing the iOS 6.9" validator.
- **P3 — Preview/promo video end-to-end.** ASC app-preview upload + Play promo
  link, multi-segment concat. *Accept when:* a 30 s preview uploads to ASC and a
  promo link lands in Play.
- **P4 — Credit metering + estimate + cockpit card.** `studio` service key + meter
  kind + markup + `/studio/estimate` + shelf card. *Accept when:* a farm run
  debits the wallet (live flag on a test user) and the cockpit shows a Studio
  burn line; an owner-runner run charges 0.
- **P5 — Surfaces.** `app/studio.tsx` (mobile) + `StudioView.tsx` (web) + MCP
  verbs, live stream + inline review + one-tap ship. *Accept when:* a full job
  runs from the mobile app and from web, watching the device live, ending in
  downloadable assets.
- **P6 — Shared-runner hardening.** Close operator-fleet gaps C/D (teardown wipe,
  scoped token, network jail) so the metered shared-farm path is safe. *Accept
  when:* a job on a shared box leaves zero residue and cannot reach the runner's
  LAN.
- **P7 — Multi-locale batch + non-Expo flow synthesis (Flutter/native).**

Ordering front-loads the no-incumbent capability (permission video) and the
immediate dogfood, then fills incumbent-occupied ground (screenshots) where Yaver
wins on integration + farm + price, defers shared-runner billing until isolation
is proven.

---

## 15. Risks

- **Reviewer judgment** on generated videos — mitigate with real footage (arm64
  redroid) + spec validators.
- **redroid kernel dependency** (`binderfs`/ptrace) — verify per image before
  promising the farm path.
- **arm64 capacity** — design retries + fallbacks in from the start (§6).
- **Shared-runner isolation** — the operator-fleet teardown gaps are real; gate
  the metered shared path behind P6.
- **Store spec churn** — specs are data + validators, one source of truth.
- **Flow brittleness** — committed-flow override + seed-and-commit generated
  flows.
- **YouTube OAuth** — R2 self-host fallback.

---

## 16. One-line summary

Yaver already records device video, drives apps with Maestro/Appium, generates +
uploads iOS screenshots, runs an arm64 farm, streams live, and meters per-use.
**Studio** wraps those into a production, credit-metered store-asset product for
third-party devs across mobile + web + CLI + MCP — led by a
permission-justification-video generator that has no incumbent and that Yaver
needs for its own Play submission today.
