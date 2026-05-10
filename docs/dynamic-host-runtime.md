# Dynamic Yaver Mobile Host Runtime — Design

> **Status**: design draft. No code yet. Decision needed before implementation.
> **Trigger**: 2026-05-10 user complaint — "yaver go agent should be dynamic; it should compile/configure according to host (iphone device) config at all".
> **Symptom**: loading `carrotbet/mobile` (React 19.2.5) into Yaver mobile (React 19.1.0) is blocked by the `RUNTIME_FAMILY_MISMATCH` compatibility check, even when the diff is small enough that the bundle would actually run.

## Problem

The Yaver mobile container ships with a single fixed Hermes runtime baked in
at TestFlight/App Store build time:

| Component | Pinned in host bundle |
|---|---|
| Hermes BC | 96 |
| React Native | 0.81.5 |
| React | 19.1.0 |
| Expo | 54.0.33 |

A guest app (sfmg, carrotbet, todo-rn) can only be loaded if its declared
`react`, `react-native`, and `expo` versions land in the same "host family".
The compat checker (`devserver_http.go::3460`) refuses to load anything else
to avoid runtime crashes from JSX-runtime / scheduler / native-module ABI
drift.

This is correct for **safety** but the wedge ("phone-first dev loop, no Mac
required") asks for the **opposite** ergonomics: any project the user has
should load, full stop. The user shouldn't have to pin their personal
projects to whatever React minor Yaver shipped that month.

## Three architectures, in increasing scope

### A. Force-bypass + smarter compat rules — *days, ships incremental*

Keep the static host. Loosen the gate.

**Mechanics:**
1. Agent: accept `force=true` on `POST /dev/build-native`. When set,
   skip the `RUNTIME_FAMILY_MISMATCH` block entirely; bundle gets
   shipped to the device. (~30 LOC.)
2. Mobile: when the agent returns a `RUNTIME_FAMILY_MISMATCH`, the
   compat dialog gains a **"Reload Anyway"** button. Tap → re-fire
   the request with `force=true`. (~50 LOC.)
3. Compat checker: same-minor React (19.1 ↔ 19.2) becomes a
   `severity=warning` finding rather than a blocking error. Same for
   patch-level RN (0.81.5 ↔ 0.81.6) and patch-level Expo
   (54.0.33 ↔ 54.0.34). Major bumps still block. (~120 LOC.)

**Pros**:
- Ships in one turn. Zero infra change. No App Store review impact.
- Matches semver semantics — same minor = "should work, sometimes
  doesn't"; same major = "depends what you touched".
- The escape hatch is permanent: even when the smart rules say no,
  the user can override.

**Cons**:
- Doesn't actually make the host "dynamic" — it just makes the gate
  less paranoid. A project on React 20 still won't load.
- Force-bypassed bundles can crash at runtime in subtle ways
  (use-on-19.2-only-API). The user needs to know "Reload Anyway"
  is an opt-in *risk*.

**When to pick this**: 80% of the user's actual projects are within
one minor of whatever Yaver ships. This unblocks everything except
the genuinely-far-apart cases. Cheapest path to the user's
unblock-immediately ask.

### B. Multi-family host bundle — *weeks, ships in a TestFlight*

Yaver mobile bundles N runtime families inside the .ipa / .aab. At
load time, the agent picks the family that matches the guest's
declared versions. Switching is instant — no rebuild, no reinstall.

**Mechanics:**
1. Yaver iOS / Android build process compiles N separate Hermes
   contexts (e.g. Family A: RN 0.81.5/React 19.1.0; Family B:
   RN 0.81.6/React 19.2.5; Family C: RN 0.82/React 19.3). Each
   ships its own JSI bridge state.
2. `mobile-host-runtime-families.json` (new contract) declares which
   families are bundled. Sent to the agent at heartbeat time so the
   agent can pick the right one for each guest project.
3. Agent's `runtime_align.go` resolves the guest's versions against
   the available families and picks the closest. The compat dialog
   surfaces "this app uses Family B; switching" instead of "blocked".
4. iOS: `RCTBridge` + `RCTHost` get reset between guest loads so
   the guest's family becomes the active one. Memory cost: ~30-50 MB
   per family resident. App size cost: ~20-30 MB per extra family.

**Pros**:
- True multi-version support without per-project rebuilds.
- Fast switching — instant, in-app.
- App Store reviewers see one app, no per-project bundles or
  enterprise-distribution gymnastics.

**Cons**:
- Yaver mobile bundle size grows linearly with families. Three
  families = +60-90 MB on App Store (already at the user's
  attention given the 100 MB cellular-download warning).
- New families require a Yaver TestFlight roundtrip (1 day) — so
  React 20 still requires Yaver to ship a build before users can
  load React 20 apps.
- Native module ABI: each family carries its own copy of every
  native module (camera, file-system, push). Shared modules
  break this model fast.

**When to pick this**: the user's project portfolio clusters around
2-3 React versions and stays there for months. Family churn is
quarterly, not weekly.

### C. Per-project dynamic host build — *multi-week, deepest reach*

When the agent sees a project with no matching host family, it
builds a project-specific Yaver host on the local Mac and pushes
it to the iPhone via wireless / TestFlight / Ad-hoc. Each major
project gets its own bundle ID variant.

**Mechanics:**
1. Agent reads guest `package.json` + lock file. Resolves exact
   RN/React/Expo + native module versions.
2. Agent runs `expo prebuild --clean` or equivalent in a
   project-scoped Yaver workspace (~/.yaver/dynamic-hosts/<project>).
   That workspace has its own copy of `mobile/`, dependencies
   match the guest, and the build product is a Yaver-shaped
   wrapper around the guest's runtime.
3. Built `.app` / `.apk` is wireless-pushed to the iPhone. Bundle
   ID is `io.yaver.dynamic.<project>`. The user gets one Yaver
   icon per "dynamic host" they've built.
4. Subsequent reloads of the same project skip the build (cache
   hit on package-lock checksum).
5. Cleanup: `yaver host clean` evicts unused dynamic hosts.

**Pros**:
- True dynamic — any React/RN version the user wants, instantly
  available after a one-time per-project build.
- Native modules: the dynamic host can actually link the guest's
  exact native modules. No more "Yaver doesn't include X" dialog.
- App Store: not a thing. Dynamic hosts ride the user's developer
  cert; they install on their devices via wireless push, not the
  store.

**Cons**:
- First build is the *full* expo prebuild + xcodebuild round-trip
  — 25 minutes on a Mac mini for cold cache.
- Bundle ID proliferation: every project = a new icon on the
  Springboard. Devices fill up quickly.
- Linux / Windows machines can't build iOS — the dynamic-host
  pipeline is Mac-only for iOS. Android works anywhere.
- Apple Developer cert needs a free seat for every dynamic host
  bundle ID (the user's cert is per-team, so it scales OK; but
  CI signing infrastructure has to be re-architected for
  per-project bundle IDs).
- App Store distribution of Yaver itself (the orchestrator) is
  still single-bundle-ID. Dynamic hosts can't ship through the
  store; they're a power-user side-load.

**When to pick this**: the user routinely loads projects with
incompatible native modules (in-app purchases, payments, certain
camera SDKs). Sometimes the only correct answer is "build the host
to match this exact project."

## Recommendation

**Ship A immediately. Earn B over the next month. Treat C as the
exit-from-store-distribution endpoint when (and only when) Yaver's
power users hit native-module walls that A and B can't solve.**

Concretely:
1. **This week**: A (Force/Override + semver-aware loosening).
   Solves the user's `carrotbet` problem today.
2. **Next 2 weeks**: B preview (single extra family bundled, picked
   at runtime). Validates the multi-family path doesn't blow the
   bundle size budget.
3. **Beyond**: C only when a real project blocks B. Don't pre-build
   an icon-spawning per-project pipeline before there's a project
   that needs it.

## Open questions

- **A's "Reload Anyway" telemetry**: do we record per-project that
  the user opted into a force load, so the next compat dialog can
  say "you've used this project on this host before, the bundle
  worked"? (Yes — same shape as the existing `OperationState`.)
- **B's family-selection UX**: surface the chosen family in the
  Reload card or hide it? (Surface it, in `extra` slot of the
  shared `RemoteBoxBanner` — already supports per-tab content.)
- **C's bundle ID naming**: `io.yaver.dynamic.<sha256(lockfile)>`
  vs `io.yaver.dynamic.<project-slug>` vs user-typed name? (Slug
  for human recognition; sha256 suffix when the same slug already
  has a different lockfile hash. So `io.yaver.dynamic.carrotbet`
  for the first one, `io.yaver.dynamic.carrotbet.a3f9` for a
  divergent fork.)
- **Cross-cutting**: how do dynamic hosts interact with the SDK
  consumer-version handshake (`mobile/sdk-manifest.json`)? Each
  dynamic host carries its own copy; the agent needs to ship per-
  host SDK manifests in `/dev/build-native` payloads.

## Files this would touch

| Layer | Files (A) | Additional for B | Additional for C |
|---|---|---|---|
| Agent | `devserver_http.go`, `runtime_align.go`, new `compat_semver.go` | Convex `mobileHostRuntimeFamilies` table + reader | new `dynamic_host_builder.go`, `dynamic_host_pusher.go`, `host_cache.go` |
| Mobile | `nativeBuild.ts` (force flag), `apps.tsx` Compatibility dialog (Reload Anyway button) | `RuntimeFamilySwitcher.tsx`, native bridge reset wiring | `DynamicHostList.tsx`, side-loaded host detection |
| iOS | — | `Yaver.xcodeproj` family slicing | per-project `Yaver-<project>.xcodeproj` template |
| Android | — | Gradle product flavors per family | dynamic-feature module per project |
| Backend | — | `mobileHostRuntimeFamilies` Convex schema | per-user dynamic-host bundle-ID registry |
| CI | — | matrix expanded to include each family | per-project signing automation |

## Decision

(blank — fill in after the user picks a layer.)
