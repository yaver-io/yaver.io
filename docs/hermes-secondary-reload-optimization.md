# Hermes Secondary Reload Optimization (vibe-feedback path)

Status: design — no implementation has shipped from this doc.
Scope: Yaver mobile + Yaver agent. Specific to the vibe-iteration loop
through the YaverFeedbackPane / mobile feedback SDK.
Audience: anyone touching `desktop/agent/native_build.go`,
`desktop/agent/devserver.go`, `mobile/ios/Yaver/YaverBundleLoader.*`, or
`mobile/android/.../YaverBundleLoader.kt`.

## 1. Goal

Make the **second + reload** in Yaver mobile feel instant for the vibe
loop. The flow being optimised is:

```
User taps "Send" in YaverFeedbackPane
  → Agent runner edits 1-3 files in src/
    → User taps "Reload"
      → Agent re-bundles + recompiles HBC from scratch
        → Mobile downloads
          → Mobile bridge.invalidate() + recreates RCTBridge
            → Mobile re-runs full bundle
              → React mounts
```

Today's wall time on a 250-module Expo app, agent on
`yaver-test-ephemeral` (cax21 ARM, Helsinki), iPhone on the same LAN:
**12–27 seconds**. The vast majority of that work is redundant — 95%+ of
the bytecode is bit-identical to what the phone loaded a minute ago.

Target after the layers in this doc are applied:
- Layer 0–1 only: 10–22s (incremental)
- Layer 0–2: **3–5s** (this is where it starts feeling fast)
- Layer 0–3: **2–3s**
- Layer 0–4: **~300 ms** (HMR; sidesteps Hermes for JS-only edits)

## 2. Out of scope

- Initial cold install on a phone (one-time cost; not the loop).
- Adding a native dependency (`expo-camera`, `react-native-ble-plx`, …).
  This MUST trigger a full rebuild — see §6 for the invalidation rules.
- Changing `app.json` plugins, `Info.plist`, `AndroidManifest.xml`, or
  any file under `mobile/ios/` / `mobile/android/`.
- WebView fallback for browser-safe RN apps (separate doc).
- `yaver wire push` and TestFlight cycles (different cadence).

## 3. Today's reload timeline

Measured on a representative 250-module Expo app with Metro warm
(second reload of the session, not first):

| Phase | Where | Time |
|---|---|---|
| Metro re-bundle (cached transforms) | agent | 2–4s |
| `hermesc -O` compile | agent | 5–12s |
| Bundle write + HTTP serve | agent | <1s |
| Bundle download (3–5 MB) | LAN | 1–2s |
| `bridge.invalidate()` + RCTBridge recreate | phone | 1–2s |
| Bundle parse + module-map build | phone | 2–3s |
| React mount + initial render | phone | 1–3s |
| **Total** | | **12–27s** |

Two dominant contributors: `hermesc -O` and the phone-side
parse + bridge recreate. Everything else is cheap in absolute terms.

## 4. What stays identical between two reloads

For the typical 1–3 file vibe edit in `src/`:
- All of `node_modules/` (~80–92% of bundle bytes): identical.
- Hermes binary version: identical.
- React / RN / Expo / nav runtime: identical.
- All polyfills, the Metro `__r` resolver, Yaver SDK shim: identical.
- Everything in `src/` not touched by the edit: identical.

What actually changed: 1–3 module function bodies, totalling on the
order of 1–50 KB of source.

The current path repeats ~99% redundant work. Every layer below targets
a slice of that redundancy.

## 5. Optimisation layers

Five layers, each independently shippable. Later layers compound on
earlier ones; you do not need all of them to feel the win.

### Layer 0 — HBC content-hash cache (agent side)

**Idea.** Metro bundle output is deterministic given a fixed source
tree. If two reloads produce a byte-identical Metro JS bundle, the HBC
output of `hermesc` over that bundle is byte-identical too. Cache it.

**How.**
1. After Metro emits the JS bundle, compute `sha256(jsBundle)`.
2. Look up `~/.yaver/hbc-cache/<sha256>.hbc`.
   - Hit → skip `hermesc` entirely; serve cached file straight from disk.
   - Miss → run `hermesc`, persist result keyed by hash.
3. Cache eviction: LRU at ~1 GB, evicted lazily on miss-write (never
   block reload waiting on eviction).

**Cache key includes:**
- `sha256(jsBundle)` (input determines output for a given Hermes).
- `hermesc --version` hash (cache must invalidate on Hermes upgrade).
- A flag bag: `dev|prod`, target arch, source-maps on/off,
  `-O` level. Different flags → different output for same input.

**Saves.**
- "Reload with no source change" — common when user taps Reload mid-vibe
  before the agent has finished writing files: skips full 5–12s `hermesc`.
- Anything that produces repeated identical Metro output (Metro
  occasionally emits identical output for a no-op save): same.

**Doesn't save.**
- Any reload after a real source edit (different Metro output → cache
  miss). This layer is purely for redundant rebuilds.

**Safety.**
- Validate HBC magic + BC version before serving cached file. Corrupted
  cache file → drop entry, fall through to `hermesc`.
- Never block reload on cache write — write asynchronously after serve.
- Cache directory namespaced per project (`~/.yaver/hbc-cache/<projectHash>/`)
  so multi-project devs don't pollute each other's cache.

**Effort.** Half a day. Pure agent-side change in `native_build.go`.
**Impact.** 5–12s saved on reload-without-edit (~30% of mid-vibe
reloads). 0s saved on reload-after-edit.

### Layer 1 — Drop `-O` in dev

**Idea.** `hermesc -O` runs constant folding, dead-code elimination,
peephole optimisations. Cumulative output is ~5–15% smaller and ~5%
faster in JS-heavy code paths. Neither matters for dev iteration.

**How.** Agent's `/dev/build-native` passes `-O0` (or omits `-O`)
whenever the build context is dev. Production / release / wire push
paths still pass `-O`. Single flag in the `hermesc` invocation in
`native_build.go`.

**Saves.** 30–40% of `hermesc` wall time. On the 250-module app:
~2–4s on every reload that hits `hermesc`.

**Safety.** Bytecode semantics are identical, only optimisation passes
are skipped. BC version, magic, file structure unchanged. Mobile
validators don't see any difference. Slightly larger bundle (worth
~+10% bytes), trivial cost on LAN.

**Effort.** ~1 hour.
**Impact.** -2 to -4s on every reload that compiles.

### Layer 2 — Hermes segments (base + app delta)

This is the main unlock. Everything before this is incremental; this
is the one that changes user-perceived reload time from "OK I'll wait"
to "happens before I let go of the button".

#### 2.1 Primitive: `Hermes::loadSegment`

The Hermes runtime exposes `loadSegment(buffer, segmentID)` — load an
additional HBC blob into a running VM. RN's bridge already uses this
in production for split bundles. We use the same primitive for dev.

#### 2.2 Split point

Two segments:

**Base segment (segment 0)** — frozen across reloads:
- `react`, `react-native`, `react-dom` (RN-Web shim)
- `expo`, `expo-modules-core`, `expo-*` packages
- `react-navigation/*`
- All other `node_modules/*` deps
- Polyfills (`metro-runtime`, `__d`, `__r`)
- Yaver feedback SDK shim, dev-error overlay, etc.

**App segment (segment 1)** — rebuilt every reload:
- `app/` (Expo Router routes)
- `src/`
- Any non-`node_modules` JS imported by the entry tree
- JS-side asset references

Empirically the base is 85–92% of bytes for a typical Expo app. The
app segment is the slice we actually iterate on.

#### 2.3 Build pipeline (agent side)

```
1. Metro produces full JS bundle as today (don't change Metro).
2. Walk Metro's module map; tag each module as base or app by path.
3. Emit two intermediate JS files:
     base.js  — only base modules + the __r runtime resolver
     app.js   — only app modules
4. hermesc base.js → base.hbc — cached, only rebuilt when base
   invalidation triggers fire (see §6).
5. hermesc app.js → app.hbc — rebuilt every reload, ~1–2s on the
   target hardware.
6. Both files served on the agent:
     GET /dev/bundle/base.hbc?v=<base-hash>
     GET /dev/bundle/app.hbc?v=<app-hash>
```

#### 2.4 Module ID stability — the sharp edge

Metro assigns numeric module IDs. Across base and app builds, IDs
**must** be deterministic and **must not** collide, otherwise app's
`__r(47)` calls hit the wrong base module → silent crash or, worse,
silently-wrong behaviour.

Solution:
- Use `createModuleIdFactory` keyed by SHA-1(absolute path), truncated
  to a 31-bit positive integer. Identical input path → identical
  numeric ID across runs and across machines.
- After each build the agent verifies app.hbc's module ID set is
  disjoint from base.hbc's. Collision → build error → fall back to a
  full single-bundle build for the next reload.

CI guard: build the same source tree twice from a clean Metro cache,
assert the resulting module ID maps are byte-identical.

#### 2.5 Mobile pickup

`YaverBundleLoader` learns a new mode: `mode: "segments"`.

```
1. Fetch GET /dev/bundle/base.hbc?v=<hash>.
   - Mobile keeps a per-project cache by hash; reuse if already on disk.
2. Fetch GET /dev/bundle/app.hbc?v=<hash>.
   - Always re-fetch on reload.
3. Validate BOTH have HBC magic 0x1F1903C1 and same BC version (offset 8).
4. Validate the BC version matches what the bundled Hermes runtime
   expects. (Already done for single-bundle path; extend to segments.)
5. Hand both to ExpoReactNativeFactory:
     loadSegment(base.hbc, 0)
     loadSegment(app.hbc, 1)
6. Trigger app entry point.
```

When the user taps Reload after a small edit:
- Step 1: cache hit (base unchanged), nothing fetched.
- Step 2: ~5–50 KB download.
- Step 3–4: microseconds.
- Step 5: only segment-1 swap.
- Step 6: re-mount React.

#### 2.6 Saves

Compile: 5–12s `hermesc` → ~1–2s (only app slice goes through).
Network: 3–5 MB → ~5–50 KB (skip base on cache hit).
Phone-side parse: 2–3s → ~0.3s (only app segment is new).

Total reload from current 12–27s drops to 3–5s.

#### 2.7 Safety

- **BC version match.** Mobile rejects any segment whose BC version
  doesn't match runtime + sibling segment. Reject → fall back to
  single-bundle path.
- **Module ID determinism.** §2.4 plus a CI invariance test.
- **Base invalidation.** §6 lists every trigger that drops base cache.
- **TurboModule registration.** `loadSegment(app)` must NOT
  re-register any module name from base. Hermes treats double-register
  as a runtime error in some RN versions; Yaver's loader guards against
  it before the second `loadSegment` call.
- **Atomic swap.** If app.hbc fetch or validation fails mid-reload,
  the previous good app segment stays loaded. The user sees "reload
  failed, keeping previous version" instead of a wedged app.

**Effort.** 3–5 days. Touches `desktop/agent/native_build.go`,
`desktop/agent/devserver_http.go` (new endpoints), a new
`metro.config.js` overlay for the ID factory, mobile
`YaverBundleLoader.swift` + `YaverBundleLoader.kt`.

**Impact.** -10 to -16s on every reload after the first.

### Layer 3 — VM warm reuse (no bridge teardown)

After Layer 2, the bottleneck shifts to phone-side. Today's mobile
flow on reload:

```
bridge.invalidate()
new RCTBridge(...)
new RCTAppDependencyProvider(...)
loadBundle(...)
```

That's ~3–5s of phone work — destroying TurboModule instances, draining
the JS message queue, recreating everything, re-instantiating the
runtime.

With segments we don't actually need to tear the runtime down. The
runtime is fine; we just want fresher app code in segment 1. The path:

1. Keep RCTBridge + Hermes runtime alive across reloads.
2. On reload, evict app modules from the JS module table, then
   `loadSegment(newAppHbc, 1)` — replaces module-1's contents.
3. Trigger React's `unmount` + `mount` on the root, which runs the
   new module code.

#### 3.1 How to evict the old segment

Three options, in increasing cleanliness:

(a) **Bump segment ID each reload** (`1`, then `2`, `3`, …). New
    segment registers fresh module IDs; old segment becomes
    unreachable but not freed. Memory grows. OK for short dev sessions,
    bad for long ones.

(b) **`Hermes::clearStoredSegment(segmentID)`** if the version of
    Hermes shipped in Yaver's mobile container exposes it. Frees the
    old segment cleanly. Need to verify per platform.

(c) **JS-side eviction via the Metro `__r` resolver.** Yaver controls
    the runtime resolver in the base segment — wrap it with a
    per-segment registry. On reload, drop the app segment's entries
    from `__r`'s cache, then `loadSegment` to repopulate. Most portable
    across Hermes versions; ~50 lines of JS.

(c) is the recommended path. (b) as a follow-up if/when the runtime
exposes the API.

#### 3.2 What state survives the swap

By design, only the app segment's modules are reset. Everything else
persists:

- React's root state (good — vibe iteration wants to keep the user on
  the same screen and watch the change).
- Navigation state (good).
- Local component state in components whose modules didn't change
  (good — Fast Refresh-like UX without Fast Refresh).
- AsyncStorage / on-disk state (orthogonal; reload doesn't touch).
- `globalThis` polyfills, network state, console (owned by base; survives).

For the rare case where the developer wants a from-scratch reload
(testing first-launch flows), expose a separate "Hard Reload" gesture
that goes through the single-bundle path with full bridge teardown.

#### 3.3 Saves

Skips bridge teardown + recreate (~2–4s) and skips the re-init of
React's runtime + polyfills.

Reload total drops from ~3–5s (Layer 2) to ~2–3s.

**Effort.** 2–3 days, mostly mobile.
**Impact.** -2 to -4s after Layer 2.

### Layer 4 — Selective module hot-swap (Metro Fast Refresh / HMR)

The deepest layer. For JS-only edits, this sidesteps Hermes entirely.

#### 4.1 How RN's Fast Refresh works

1. Metro watches source files.
2. On change, Metro emits a JS delta — only the changed modules' source.
3. Delta sent over a WebSocket from Metro to the running app.
4. App's HMR client `eval`s the delta into the running JS VM.
5. React's runtime detects which components changed (via a Babel
   transform that adds metadata) and hot-swaps them in place,
   preserving local state in unaffected components.

Wall time: ~100–300ms regardless of bundle size. Bytecode is never
touched.

#### 4.2 Mapping to Yaver's vibe-feedback flow

1. Agent runner edits files in `src/`.
2. Agent already has fs-watch (`devserver.go`); extend to forward
   Metro's HMR WebSocket events.
3. Phone connects to `wss://<agent>/dev/hmr` (forwarded over QUIC
   tunnel for cellular paths).
4. Phone's HMR client (RN core, just needs the WS URL set) applies
   the delta.
5. React Fast Refresh re-renders only changed components.

User taps Reload? Mostly unnecessary — change applies live as the
agent saves. The Reload button becomes "force a re-render" rather than
a rebuild trigger.

#### 4.3 Why we don't have it today

Fast Refresh requires Hermes in **dev mode**. Yaver's mobile container
ships RN + Hermes in **release** (smaller binary, marginally faster
runtime). Dev-mode Hermes:
- Larger binary (~2 MB delta).
- Marginally slower JS execution (irrelevant — Yaver itself doesn't
  bundle production-grade JS in a measured hot path).
- Enables `__DEV__`-gated functionality including HMR client.

Two options:
- Always ship dev-mode Hermes in Yaver mobile (simpler, slight bloat).
- Ship dual variants and pick at runtime based on whether a
  Yaver-managed dev session is active (more code, no bloat for users
  not using dev features).

Recommendation: ship dev-mode always. The audience for Yaver mobile
is by definition developers; the binary delta is acceptable.

#### 4.4 Edge cases that auto-degrade to full reload

Some edits force Metro's HMR to give up:
- Changing a class export's signature.
- Renaming a hook in a way that breaks the Fast Refresh metadata.
- Adding/removing top-level exports.
- Changing module-level `import` statements in non-trivial ways.

In these cases the HMR client emits a "needs reload" event and the
mobile loader falls back to Layer 3 (segment swap). User-visible delay
goes from ~150ms to ~2–3s — still much better than today.

#### 4.5 Saves

For all reloads where HMR applies (~80–90% of vibe edits, rough
estimate from how Metro typically classifies changes): wall time goes
from Layer 3's 2–3s down to ~150–300ms.

For the remaining 10–20%: same as Layer 3.

**Effort.** 1–2 weeks. Touches: mobile build pipeline (dev-Hermes
variant), agent (HMR WS proxy), `YaverBundleLoader` (HMR-mode
selection + RN HMR client init).
**Impact.** -2 to -3s after Layer 3 for HMR-eligible edits.

## 6. Cumulative impact projection

For the average 250-module Expo RN app, second reload after a 1–3 file
edit, agent on `yaver-test-ephemeral`, phone on LAN:

| Stack | Compile | Network | Phone init | Total |
|---|---|---|---|---|
| Today | 7–16s | 1–2s | 4–8s | **12–27s** |
| + L0 cache | (unchanged after edit) | same | same | 12–27s* |
| + L1 `-O0` | 5–12s | same | same | 10–22s |
| + L2 segments | 1–2s | <0.2s | 1–2s | **3–5s** |
| + L3 VM warm | 1–2s | <0.2s | 0.3–0.6s | **2–3s** |
| + L4 HMR | 0s | ~100ms (WS) | ~150ms | **~300 ms** |

`*` L0 only saves the no-edit-but-reload case. It's still worth
shipping — eliminates the worst-feeling case (user taps Reload, agent
is mid-write, nothing happened, but they wait 20s).

Translation:
- L0 + L1 alone: incremental, mostly free, ~20% better.
- L0–2: where reload starts feeling fast (~5s ceiling).
- L0–3: feels native.
- L0–4: indistinguishable from local dev — every save in agent shows
  up live.

## 7. Safety / invariants (cross-cutting)

These apply across all layers and ARE the contract:

1. **Bytecode version match.** Every segment validates HBC magic
   (`0x1F1903C1`) + BC version (offset 8) against the runtime's
   expected version + sibling segment's version. Mismatch → reject,
   fall back to single-bundle path. Already partly wired in
   `YaverBundleValidator.swift`; extend for segments.

2. **Module ID determinism.** Path-hashed factory; CI test asserts
   ID stability across cleared-cache rebuilds. Collision check at
   build time → fall back if any.

3. **Base invalidation triggers.** Any of these → drop base cache and
   force full rebuild on next reload:
   - `package.json` content hash changed.
   - `package-lock.json` / `pnpm-lock.yaml` / `yarn.lock` changed.
   - `app.json` plugins / native config changed.
   - `Info.plist`, `AndroidManifest.xml`, `mobile/ios/` overlay files,
     `mobile/android/` overlay files changed.
   - Hermes binary version changed (`hermesc -version` hash).
   - RN package version changed.
   - Expo SDK version changed.

4. **Fall-back path always works.** Any failure (validation, network,
   Metro build error, BC mismatch, segment registration error) MUST
   degrade gracefully to the next-simpler layer, ultimately to today's
   single-bundle path. The user should see at most "took a sec longer"
   — never a wedged app.

5. **Source map merging.** Debugging across base + app segments needs
   unified source maps. Hermes doesn't merge them natively. Agent
   emits both maps; mobile error-overlay loads both and resolves
   stack frames per segment. Acceptable to defer to v2 — vibe
   iteration usually doesn't hit production-style stack traces, but
   when something goes wrong the user wants `src/Foo.tsx:123` not a
   raw bytecode offset.

6. **No partial state.** A reload that fetches base but fails app
   (network drop, validation fail) MUST keep the previous good app
   segment. Atomic swap: fetch + validate first, register second.
   Never leave the runtime with a missing app segment.

7. **Memory growth.** Repeated reloads under Layer 3 must release old
   segment memory. CI test: reload 100× consecutively, assert phone
   memory usage stays bounded.

8. **Vibe + reload race.** User taps Reload while agent is mid-write.
   Today's full-rebuild path tolerates this because everything is
   recompiled. Segments tighten the race: a half-written file might
   land in app.hbc with a syntax error. Mitigation: agent gates
   `/dev/build-native` and segment build on a clean fs-write fence
   (`devserver.go` already has the primitive — debounce + "no writes
   in last 200ms" check).

9. **TurboModule re-registration.** Native modules register once per
   process. Segment swap must NOT re-register; Hermes treats the
   second call as either a no-op or a fatal error depending on the RN
   version it's tied to. Loader guards the registration set.

10. **HMR auth.** The HMR WebSocket carries source diffs. Authenticate
    over the same QUIC tunnel auth path used for the rest of `/dev/*`,
    not a bare `wss://`. Reuse `X-Relay-Password` / signed-token
    machinery.

## 8. Implementation phasing

| Phase | What | Effort | Risk | Cumulative reload |
|---|---|---|---|---|
| 0 | HBC content-hash cache | 0.5 day | low | 12–27s* |
| 1 | Drop `-O` in dev | 1 hour | trivial | 10–22s |
| 2a | Build pipeline: split Metro output | 1–2 days | medium | (unchanged yet) |
| 2b | Agent serves base + app HBCs | 1 day | low | (unchanged yet) |
| 2c | Mobile loads segments | 2–3 days | medium | 3–5s |
| 3 | VM warm reuse + module map evict | 2–3 days | medium-high | 2–3s |
| 4a | Mobile dev-Hermes variant | 3–5 days | high (build infra) | (unchanged yet) |
| 4b | HMR WebSocket proxy + client wiring | 3–5 days | medium | ~300 ms |

Recommended ship order:

1. **Phase 0 + 1 together** in a single PR. Low risk, ~1 day, immediate
   incremental win. Establishes the cache infrastructure that Phase 2
   reuses.
2. **Phase 2** in three sub-PRs (build, serve, load) over 1–2 weeks.
   Phase 2c is the highest-risk piece (mobile changes); ship behind a
   feature flag for the first week.
3. **Phase 3** as a follow-up after Phase 2 has been bake-tested for a
   few days. Touches similar mobile code paths, but only after Phase 2
   is stable.
4. **Phase 4** evaluated separately. The Phase 0–3 stack already gets
   us to 2–3s — "feels fast enough" for most users. Phase 4 is the
   "feels native" jump and is worth doing once Phase 0–3 has settled
   and we've measured how often HMR-eligible edits occur in real vibe
   sessions.

## 9. Open questions

1. **Does the Hermes shipped in current Yaver mobile expose
   `clearStoredSegment`?** Verify in the iOS + Android binaries we
   currently link. If not, fall back to JS-side eviction (Layer 3,
   option c).

2. **Real-world base-vs-app size ratio.** Estimates above assume
   85–92% base. Worth measuring on 3–5 real apps (sfmg, talos,
   carrotbet, a simple counter, a feature-rich dashboard) before
   locking in the split point.

3. **Module ID factory: path-based vs hash-based?** Path-based is
   simplest but breaks when files move. Hash-based survives renames.
   Wire format is integer IDs — both factories produce ints; the
   difference is the input.

4. **HMR transport: bare Metro WS vs QUIC-tunnelled?** Direct WS is
   simpler but requires the phone to reach Metro's port directly
   (LAN only). QUIC-tunnelled is uniform with our existing transport
   and works on cellular. Default to tunnelled.

5. **Mobile bundle cache eviction.** Phone holds N base bundles
   across N projects. Eviction strategy: LRU per project with a
   per-project cap (e.g. 50 MB)? Plus a global cap.

6. **What's the right "Reload" UX after Layer 4?** When HMR is live
   and applying changes silently, the Reload button needs new
   semantics. Probably: tap = re-render, long-press = force segment
   swap, double-long-press = hard reload (bridge teardown).

## 10. Risks

- **Module ID drift.** If the factory changes between base and app
  builds (different hash function, different path normalisation,
  different Metro version), shipped bundle is internally broken and
  crashes at runtime in non-obvious ways. Hard to catch at build time.
  Mitigation: CI test that asserts ID stability across rebuilds; build
  step verifies app/base ID disjointness.

- **Segment-related runtime crashes.** Hermes' segment loader has had
  iOS-specific bugs in past versions. Test matrix: iOS 16, 17, 18 +
  Android 12, 13, 14 + at least one tablet form factor.

- **Source maps degraded.** Acceptable short-term, but vibe iteration
  occasionally hits a case where the user wants to read a stack trace
  ("which of my files threw this?"). Layer 0–2 must at minimum surface
  the broken file path; full prod-quality stack traces can wait for v2.

- **Cache corruption.** A bad HBC blob landing in cache → every
  subsequent reload loads broken code. Mitigations: validate magic +
  BC version + a CRC of the HBC payload before serving; on validation
  fail, drop the entry and `hermesc` from scratch.

- **Vibe + reload race.** Detailed in §7.8. Mitigation: write fence in
  agent; mobile retries reload after 200ms if first attempt validates
  to a syntax-error segment.

- **Dev-Hermes mobile binary bloat.** Layer 4 ships a larger Hermes.
  Acceptable for the mobile audience but add to the build-size guard
  in CI so we don't accidentally double-ship dev + release variants.

## 11. Why not deeper (per-module HBC chunks)?

Tempting to go further: emit one HBC per module, lazy-load on demand,
recompile only the literal one module that changed. The math says
~10–50ms reload — better than even Layer 4.

It's the wrong investment because:

- Hermes doesn't expose a "load just this module's bytecode" primitive.
  We'd be writing a custom runtime resolver in JS that calls into
  native to lazy-decode HBC chunks, all of which has to track future
  Hermes API evolution.
- Layer 4 (HMR) achieves the same wall-time impact at ~1/10 the
  complexity, using a code path that RN core already maintains.
- Hermes' bytecode format isn't designed for per-module loading — the
  string and identifier tables are file-global. Splitting into per-
  module HBC chunks duplicates these tables, eating most of the size
  savings.

If RN's HMR ever stops working for us (architectural change in RN, a
security policy in the Yaver container that blocks the WS, Hermes
bytecode format change), per-module chunks become viable. Until then,
Layers 0–4 is the right shape.

## 12. References

- Hermes runtime: https://github.com/facebook/hermes
- Metro bundler module IDs: `metro-config` `serializer.createModuleIdFactory`
- RN Fast Refresh: `react-refresh-babel`, `react-native/Libraries/Core/setUpReactRefresh.js`
- Yaver mobile bundle loader: `mobile/ios/Yaver/YaverBundleLoader.swift`,
  `mobile/android/.../YaverBundleLoader.kt`
- Yaver dev server: `desktop/agent/devserver.go`,
  `desktop/agent/devserver_http.go`, `desktop/agent/native_build.go`
- HBC validation: `mobile/ios/Yaver/YaverBundleValidator.swift`
- Privacy contract for any new agent → mobile traffic:
  `desktop/agent/convex_privacy_test.go` (none of the segment / HMR
  payloads should land in Convex; transport stays P2P / relay).

---

## 13. Deep safety analysis

§7 covers cross-cutting invariants briefly. This section enumerates
every realistic crash mode per layer, the mitigation, and the
recovery path. Two non-negotiable properties shape every choice:

- **SP1 — No-crash invariant.** No path through this stack produces a
  phone-side crash that the user can't recover from with a hard
  reload. Worst case: "reload took longer than expected."
- **SP2 — Initial-build untouched.** Every existing build pipeline
  (TestFlight, Play Store, `yaver wire push`, release CI) produces
  byte-equivalent output before and after this stack lands. The dev
  iteration path is the only mutated path.
- **SP3 — Cache-or-fail.** A correctness bug in caching is the
  highest-priority class of failure: it produces silently-wrong code,
  worse than a visible crash. Every cache hit goes through full
  validation; any validation failure drops the entry and recompiles.

### 13.1 Layer 0 (HBC content-hash cache) — failure modes

| Failure | Symptom | Mitigation |
|---|---|---|
| Cache file truncated mid-write (process killed, disk full, OS swap-out) | Phone loads invalid HBC → JSI throw at parse time | Atomic write: `<name>.tmp` + `fsync` + `rename`. Never expose a partial file under its final name. |
| Hermes upgraded between writes | Phone loads HBC compiled with old Hermes against newer runtime | Cache key includes `sha256(hermesc -version)`. Bumped Hermes invalidates all entries. |
| Cache key forgets a flag | Wrong bytecode for current build context | Cache key encodes EVERY hermesc input: source bundle hash, Hermes version, target arch, dev/prod, source-maps on/off, optimization level. CI test: rotate any single flag, assert cache miss. |
| Disk corruption (rare but real) | Phone loads garbled bytecode | Pre-serve validation: HBC magic at offset 4 (`0x1F1903C1`), BC version at offset 8, file size matches header's reported total, optional CRC of payload. Validation fail → drop entry, recompile. |
| Concurrent agents write same key | One overwrites another mid-read; a read sees a half-written replacement | `flock(LOCK_EX)` on the cache file path during write; `flock(LOCK_SH)` for read. Combined with rename-after-fsync, readers see only complete files. |
| Cache directory grows unbounded | Disk fills, all writes fail | LRU eviction at 1 GB ceiling; eviction runs async after serve so reload latency is unaffected. |
| Cache schema bump (we change the on-disk format) | Old entries served against new code | `~/.yaver/hbc-cache/SCHEMA` file records schema version. Mismatch on startup → wipe cache, regenerate. |
| Adversarial cache poisoning (someone writes hostile HBC) | Bytecode runs as expected from the user's perspective but does something else | Threat model: this requires local filesystem write under `$HOME/.yaver`, which is already root-of-trust. Out of scope for this layer; addressed by overall machine security. |

**Initial-build impact.** Zero. Cache is consulted only in the dev path
via `/dev/build-native`. The release pipelines build through
`xcodebuild` / Gradle / `expo export:embed` and never touch this cache.

**Worst-case crash sequence.** Corrupted cache file passes magic +
BC version checks but has internally inconsistent offsets → Hermes
deserializer throws → red screen. User taps Reload → next-tier
validation (CRC) catches the corruption → cache entry dropped →
hermesc recompiles → app loads normally. User sees one slow reload,
not a wedged app.

### 13.2 Layer 1 (`-O0` in dev) — failure modes

The risk surface is small. Hermes' `-O` passes are documented as
semantics-preserving optimizations.

| Failure | Symptom | Mitigation |
|---|---|---|
| User code depends on `-O` behavior (e.g. relies on dead-code elim removing a dev-only branch) | App behaves differently in `-O0` vs `-O` | Real-world incidence is essentially zero — Hermes optimization doesn't change observable behavior. Document as a known caveat. |
| Larger bundle blows past mobile cache limit | Cache eviction churns on phone | Bundle grows ~10–15% with `-O0`. Per-project cap (50 MB) absorbs this comfortably. |
| Slower runtime tickles a timing-sensitive bug | Race appears only in dev | Same answer: code that breaks because hermesc skipped peephole is broken regardless. Mitigation: keep `-O` available behind a flag for diagnosing such cases. |

**Initial-build impact.** Zero. Production / release paths still pass
`-O`. Only the dev `/dev/build-native` path drops it.

**Worst-case crash sequence.** Bytecode interpreter has no special
path for `-O`d vs unoptimized files. Crash risk is theoretical.

### 13.3 Layer 2 (Hermes segments) — failure modes

This is the layer with the largest surface. Each subsection below
documents one failure class with the full mitigation chain.

#### 13.3.1 Module ID collision (base ∩ app)

**Risk.** App segment's module IDs overlap with base's. App calls
`__r(47)` expecting its own module; gets base's instead. Behaviour
ranges from silent corruption (ID happens to point at a structurally
similar module) to immediate JSI throw (ID points at a wildly
different shape).

**Mitigation chain:**
1. ID factory is `sha1(abs_path) & 0x7fffffff` — deterministic, stable
   across machines, independent of file ordering or count.
2. Build-time disjointness check: agent computes
   `set(app_module_ids) ∩ set(base_module_ids)` after each segment
   build. Non-empty → build error → fall back to single-bundle path
   for this reload.
3. CI test: build same source tree twice from cleared Metro caches.
   Assert resulting ID maps are byte-identical.
4. Mobile-side trust: on `loadSegment`, runtime treats segment IDs as
   already validated. We cannot detect collision at load time without
   an O(n²) scan; trust the build-time guard.

**Worst-case sequence.** Build-time guard mis-fires (bug in our
disjointness check) → phone runs corrupted code → React error
boundary or JSI throw → red screen → user taps Hard Reload →
single-bundle path runs → recovery. User loses ~30s, no data loss.

#### 13.3.2 Module ID drift between reloads

**Risk.** Reload N produces app IDs `{1..100}`; reload N+1 produces
`{1, 2, 4, 5, ..., 101}` because Metro saw a different file order.
App's `__r(47)` now points at a different file. State held by user
code (closures, native-module callbacks) becomes invalid.

**Mitigation:**
- Path-based factory: same path always gets same ID. New file gets new
  ID; existing file keeps its ID across rebuilds.
- Agent records every assigned `(path, ID)` tuple in
  `~/.yaver/<projectHash>/module-ids.json`. Each subsequent build
  verifies existing paths get the same ID. Drift detected → log
  warning, fall back to single-bundle path for this reload.
- CI test: 10 sequential simulated edits, assert ID stability.

#### 13.3.3 BC version mismatch

**Risk.** base.hbc was compiled with Hermes 0.13; app.hbc with 0.14.
Runtime expects 0.13. One segment loads, the other rejects.

**Mitigation:**
- Each segment's BC version is at offset 8. Mobile validates BOTH
  segments before either `loadSegment` call.
- If versions don't match each other or the runtime: reject both,
  drop cache, fall back to single-bundle path.
- Agent records `hermesc -version` hash alongside base.hbc; if a
  later compile uses a different hash, base is invalidated.

#### 13.3.4 Partial / interrupted loads

**Risk.** Phone fetches base.hbc OK, then app.hbc fetch fails
mid-stream. Phone has dangling base with no app.

**Mitigation: atomic swap protocol.**
1. Fetch base + app to disk first (under `pending/`).
2. Validate both (magic, BC version, size, optional CRC).
3. Hand both to runtime in a single critical section.
4. If any step fails, discard new files; retain previous good
   `(base, app)` pair.

Mobile keeps a `current` symlink → live `(base, app)`. New fetches
go to `pending/`; promotion to `current` happens only after all
validations pass.

#### 13.3.5 TurboModule double-registration

**Risk.** `loadSegment(base)` registers `MyTurboModule`.
`loadSegment(app)` somehow contains another registration → Hermes
throws on second register.

**Mitigation:**
- TurboModule registration is by design a base-segment concern. The
  split rule explicitly excludes any module path containing
  `TurboModuleRegistry.get*` or a `NativeComponent` registration —
  forced into base.
- Agent walks Metro's module map at split time; flags any module that
  matches the registration patterns and force-includes it in base.
- Defensive: mobile loader catches `loadSegment` throws, drops the
  new app segment, retains the current one.

#### 13.3.6 Vibe + reload race (write-half-file)

**Risk.** Agent runner is mid-write to `src/Foo.tsx` when user taps
Reload. Half-written file lands in Metro's bundle → app.hbc is
syntactically broken.

**Mitigation:**
- Write fence in `devserver.go` (already partly there for full
  builds): debounce reload triggers; require "no fs write in last
  200 ms" before kicking off bundle.
- Mobile-side defense: app.hbc validation includes a parse sanity
  check (try to read the bytecode header before `loadSegment`).
  Parse error → reject, retain previous app segment, schedule retry
  in 1s.
- Worst-case: phone keeps showing previous app code while agent
  recovers; user sees "reload pending" rather than a broken app.

#### 13.3.7 Base invalidation race

**Risk.** User adds a new dep to `package.json`. Agent's base-cache
invalidator triggers. Mid-rebuild, user taps Reload. App segment
references old base's module IDs but new base hasn't been served.

**Mitigation:**
- Reload is a single transaction. Agent serves a `(base.hash,
  app.hash)` PAIR. Mobile fetches them as a pair from the same
  endpoint. Mid-flight transitions are not exposed.
- If base is rebuilding when reload is requested, agent either waits
  for base to finish OR returns "rebuild in progress, retry in Ns"
  so mobile shows a progress spinner.
- A failed reload during base rebuild is recoverable: previous good
  `(base, app)` pair stays loaded.

#### 13.3.8 Hermes version skew (host vs phone)

**Risk.** Agent uses `hermesc` 0.14 to produce base + app, but the
Hermes runtime in the phone's Yaver app is 0.13.

**Mitigation:**
- BC version at offset 8 IS the wire-format check. Mobile rejects
  any HBC whose BC version doesn't match the runtime's expected
  version, regardless of which segment it is.
- Recovery: fall back to whatever bundle is on disk; user sees a
  "version mismatch" notice asking to update Yaver mobile.
- This case shouldn't happen in practice — the agent's `hermesc` is
  pinned alongside the cli, mobile is updated via TestFlight/wire —
  but the validation closes the loop if it does.

**Initial-build impact for Layer 2.** Zero in production. Segment
build is exclusively in `/dev/build-native` (dev path).
TestFlight, Play Store, and `yaver wire push` all use Metro +
hermesc as a single bundle — no agent segment-build involvement.
The mobile `YaverBundleLoader` change is gated behind a
`mode: "segments"` discriminator; production builds boot via the
pre-bundled HBC in the app's resources and never invoke segment
mode.

**Worst-case crash analysis for Layer 2.** All identified failure
modes have either build-time or load-time validation that triggers
fall-back. Longest path to a wedged app: build-time guard misses
ID collision AND mobile validation passes → phone runs broken code
→ React error boundary OR JSI throws → red screen → Hard Reload →
single-bundle path → recovery. No data loss at any step.

### 13.4 Layer 3 (VM warm reuse) — failure modes

| Failure | Symptom | Mitigation |
|---|---|---|
| Memory leak: old segments never freed | Phone OOMs after N reloads | Forced bridge teardown ("hard reload" path) every 50 segment swaps. Memory regression test in CI: 100 swaps, RSS bounded under +500 MB delta. |
| Stale closures held by old code reference freed objects | JSI throw mid-execution | JS-side `__r` eviction runs BEFORE `loadSegment` of the new segment. Closures hold strong refs but their entries are no longer reachable from `__r`; their next call attempts old code → caught by error boundary. Acceptable: those closures are about to be replaced by the React unmount/mount cycle. |
| In-flight network requests / timers | Continue running after segment swap, reference old code | Cancel all timers on swap (`clearAllTimers` in RN runtime). Network: `AbortController.abort()` on tracked controllers. Untracked promises: documented as best-effort — swap may produce a caught error from a promise that started in old code; user sees no impact because React has already remounted. |
| Native module reference held by old segment | Segfault if old segment freed before native call returns | TurboModules live in base segment which never frees. Native bindings survive across swaps. |
| Globals (window-equivalents) reference old segment exports | TypeError when called post-swap | Globals live in base; they reference base's `__r` resolver which is untouched. App-segment exports that get attached to globals must be re-attached after swap (done via React's mount lifecycle). |

**Initial-build impact.** Zero. First load uses normal `RCTBridge.init`.
VM warm reuse only activates on second + reloads of a session.

**Worst-case crash analysis.** Memory leak is the only real risk.
Mitigation (force hard reload every 50 swaps) plus regression test
bounds the damage. If the test fails, OOM crash → app restarts →
user resumes from cold reload (worst case ~30s, identical to today).

### 13.5 Layer 4 (HMR / Fast Refresh) — failure modes

| Failure | Symptom | Mitigation |
|---|---|---|
| HMR delta is malformed | `eval` throws | Try/catch in HMR client; fall back to Layer 3 (segment swap). |
| React Fast Refresh metadata missing on a component | Runtime emits "needs reload" event | Loader catches the event; falls back to Layer 3. |
| HMR WS disconnects | Subsequent edits don't propagate | Auto-reconnect with exponential backoff. UI shows "HMR disconnected — manual reload needed" if reconnect fails for >30s. |
| Auth token expires mid-session | WS reject; client retries forever | Reuse the dev session's `X-Relay-Password` / signed token. Token refresh is handled by the existing transport layer (already in `quic.ts` / `agent_mesh_remote.go`). |
| Component state lost after Fast Refresh applies | UX irritation, not a crash | Known Fast Refresh limitation; documented. |
| Dev-Hermes binary breaks an unrelated production code path | Production behavior changes | Dogfood for 2 weeks before broad release. RN itself ships dev-mode Hermes in Expo Go; tested by RN core. |

**Initial-build impact.** The dev-Hermes mobile binary is slightly
larger (~2 MB delta). CI's bundle-size guard must accept the new
ceiling. Otherwise, HMR client code is dormant unless an active dev
session is established.

**Worst-case crash analysis.** HMR is purely additive — when it
fails, fall back to Layer 3 → Layer 2 → single-bundle. Each tier is
independently validated. The only "always-on" change is the
dev-Hermes binary, which is well-tested by RN core.

### 13.6 The fall-back ladder (every degradation path)

```
HMR delta fails              → segment swap (Layer 3)
Segment swap fails           → segment full reload (Layer 2 cold)
Segment validation fails     → single-bundle reload (today's path)
Single-bundle download fails → cached previous bundle (today's behavior)
Single bundle invalid        → red screen, user can hard-reload
Hard reload fails            → kill app + relaunch (always works)
```

The user is never trapped. The slowest possible recovery — kill app,
relaunch — is the same recovery path that exists today. No regression
in failure mode.

### 13.7 Production (initial-build) preservation guarantees

Each production pipeline gets explicit verification:

**TestFlight (`scripts/deploy-testflight.sh`):**
- Reads `mobile/sdk-manifest.json`.
- `npx expo export:embed --platform ios` produces a single bundle.
- `xcodebuild` archives.
- Bundle is embedded in the `.ipa`.
- **No Yaver agent involvement. No segment build. No HBC cache.**
- Verification: byte-compare the produced `.ipa` content with a
  baseline before this stack lands and after. Diff should be limited
  to version strings + signing.

**Play Store (`scripts/deploy-playstore.sh`):**
- Same as TestFlight but `bundleRelease` produces `.aab`.
- `npx expo export:embed --platform android` is the bundling step;
  no agent.
- Verification: byte-compare AAB content vs baseline.

**`yaver wire push` (cable install):**
- `desktop/agent/wire_cmd.go` → `device_install.go` → `native_build.go`.
- Production code path uses the existing single-bundle build with `-O`.
- Segment mode is opt-in via a flag that `wire_cmd` does NOT pass.
- Verification: integration test in
  `desktop/agent/swift_cmd_integration_test.go` that runs wire push
  end-to-end and asserts the produced binary's bundle is single-segment.

**release-mobile.yml CI:**
- Currently disabled (`if: false`) per CLAUDE.md; workflow remains.
- When re-enabled: same as TestFlight path. No segment opt-in.

**`mobile/sdk-manifest.json` contract:**
- Must remain in sync across the four locations listed in CLAUDE.md.
- This stack does not modify `sdk-manifest.json` shape.

### 13.8 Pre-flight checklist before merging Layer 2+

A Layer-2-touching PR must pass all of:

- [ ] CI: build same source twice from cleared Metro cache → module IDs byte-identical.
- [ ] CI: app/base ID disjointness check on a representative project.
- [ ] CI: byte-compare TestFlight artifact pre/post (excluding version strings).
- [ ] CI: byte-compare Play Store AAB pre/post.
- [ ] CI: `yaver wire push` to an Android emulator produces a working bundle.
- [ ] Memory: 100 segment swaps, phone RSS bounded under +500 MB delta.
- [ ] Manual: iPhone (LAN) — vibe edit + reload, validate fall-back when app.hbc fetch is interrupted.
- [ ] Manual: iPhone (cellular) — same.
- [ ] Manual: Android — same.
- [ ] Manual: hermesc upgraded mid-session — base cache correctly invalidates.
- [ ] Manual: package.json edit — base cache correctly invalidates.
- [ ] Manual: malformed app.hbc served by agent — mobile retains previous good app, shows recoverable error.
- [ ] Manual: agent crashes mid-segment-build — mobile timeout + fall-back.

### 13.9 Roll-out / kill switch

Layer 2+ ships behind a runtime flag in agent config
(`devSegmentMode: "off" | "on" | "auto"`):

- `off` (default for first ~2 weeks): single-bundle path, today's behavior.
- `on`: force segments. For dogfooding.
- `auto`: enable segments if all pre-flight checks pass for the project; otherwise fall back. Becomes default after dogfooding settles.

Mobile-side: `YaverBundleLoader` reads a feature flag from device
settings, with fallback to single-bundle on any error.

Remote disable: a regression found post-ship can be killed via
Convex's `platformConfig` (already wired for similar dev kill
switches). Phone code paths remain available; only the dispatch
decision changes.

### 13.10 Honest residual risks

Things that could go wrong even with all the above:

1. **Hermes runtime bug we trip.** The segment loader has been stable
   in RN's bridge for years, but our usage pattern (rapid swaps, dev
   mode) is less exercised than RN's production path (single load at
   startup). Mitigation: dogfood for 1–2 weeks before broad release;
   report any reproducible crashes upstream.

2. **Edge cases in user code.** Some libraries snapshot module IDs
   for logging. Mitigation: kill switch + monitoring for elevated
   error rates from segmented projects.

3. **Apple App Store review.** If we ever ship Yaver mobile with
   dev-Hermes (Layer 4), App Store review might flag the larger
   binary or the open WebSocket capability. Mitigation: review-cycle
   test before broad release; have an approved binary on standby
   without Layer 4 if review pushes back.

4. **Long-tail RN versions.** RN's segment API has had subtle changes
   between versions. We pin a specific RN version in
   `mobile/sdk-manifest.json`; deviations require explicit retest.

5. **Concurrent dev sessions on one agent.** Two developers (e.g. mob
   programming via shared SSH). Each segment-cache write must be
   locked. Tested under §13.1 but worth re-validating once Layer 2
   lands in real use.

6. **Yaver agent crash mid-segment-build.** Mobile is mid-fetch when
   the agent restarts. Mobile's atomic-swap protocol (§13.3.4)
   handles this — `pending/` files are discarded on next reload.

None of these are blocking. They become exit criteria for moving
the rollout flag from `auto` to "default on" without an opt-out.

### 13.11 Summary

The stack is safe to ship layer-by-layer because:

- **Each layer has an independent fall-back** to the layer below it,
  ultimately to today's path. No new failure mode strands the user.
- **Every cache hit and segment load goes through full validation.**
  Magic, BC version, file size, optional CRC. Validation failure is
  always recoverable.
- **Production build pipelines don't touch any of this code** —
  TestFlight, Play Store, wire push all bypass the agent's segment
  builder and hit the existing single-bundle hermesc path.
- **Build-time invariants are CI-enforced.** Module ID determinism,
  ID disjointness, artifact byte-equivalence in production paths.
- **A kill switch exists at every level** (agent flag, mobile flag,
  Convex remote disable).

The bound on damage: any single failure costs the user one extra
slow reload (~20–30s, today's worst case). No data loss, no app
state corruption that survives across launches, no regression in
production builds.
