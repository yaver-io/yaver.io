# Reload speed + feedback-overlay polish — work in progress

Last updated: 2026-05-04 (mobile 1.18.78, agent built locally as `/tmp/yaver-arm64`).

## State right now

### Pushed and live
- **Mobile 1.18.78** wireless-pushed to phone. Includes the URL-construction fix
  (transcript-overlay reload chip now resolves `/dev/reload-app` directly via
  `yaverResolveAgentURL` instead of the broken `deletingLastPathComponent` hack
  that produced `/d/dev/reload-app` and made the relay 502 with "device not
  connected to relay").

### Staged in repo, NOT deployed yet
- **Agent build** at `/tmp/yaver-arm64` (sha: see latest `sha256sum` —
  rebuild with `cd desktop/agent && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go
  build -o /tmp/yaver-arm64 .`). Diffs not on yaver-test-ephemeral yet:
  - `desktop/agent/vibing.go::pickReadyVibingRunner` — restricted to
    `supportedRunnerIDs` allowlist; never picks aider/goose/etc.
  - `desktop/agent/feedback_to_vibe.go::vibingifyFeedbackTaskBody` — now
    consults Convex `userSettings.primaryRunnerByDevice` for the self device
    when inbound runner is empty; pulls model from there too; drops
    incompatible model+runner combos.
  - `desktop/agent/ops_resolve.go` — new `resolvePrimaryRunnerForSelf` +
    30s cache, new `userSettingsRow` parse.
  - `desktop/agent/tasks.go` — claude default model is now
    `claude-opus-4-7` (was sonnet); new `runnerModelCompatible` helper.
  - `desktop/agent/devserver.go::EmitReloadDone` — new explicit terminal
    event; `EmitLog` now mirrors `logLine` into `message` so older SSE
    consumers see it.
  - `desktop/agent/devserver.go::SubscribeFresh` — variant of `Subscribe`
    that skips the 200-event history replay.
  - `desktop/agent/devserver_http.go` — `/dev/events?fresh=1` honours
    SubscribeFresh; `/dev/reload-app mode=bundle` emits `EmitReloadDone`
    after broadcast so the overlay can clear its spinner.
- **Mobile diffs in 1.18.78** (already pushed, but double-check the build
  on the phone matches HEAD before more iteration):
  - `mobile/ios/Yaver/YaverFeedbackPane.swift::kickOverlayReload` —
    direct URL resolution, retry-on-502/503, `?fresh=1` on /dev/events.
  - `mobile/ios/Yaver/YaverFeedbackPane.swift::summarizeReloadEvent` —
    reads `logLine`; clears `overlayReloadInFlight` on `reload_done` or
    `phase=done` on topic `reload-app`.
  - `mobile/src/context/DeviceContext.tsx` — `DEFAULT_MODEL_BY_RUNNER`
    constant; `setPrimaryRunnerForDevice` auto-picks default model on
    runner change (so picking codex from DeviceDetailsModal sets gpt-5.4
    instead of leaving the previous claude-side "sonnet").
  - `mobile/src/components/DeviceDetailsModal.tsx` — model picker IDs
    canonicalised to full Anthropic IDs (`claude-sonnet-4-6`,
    `claude-haiku-4-5-20251001`, `claude-opus-4-7`).
  - `backend/convex/aiModels.ts` — `PREDEFINED_MODELS` updated; opus is
    `isDefault` for claude.

### Not deployed
- Agent binary not yet on yaver-test-ephemeral. To deploy:
  ```bash
  scp /tmp/yaver-arm64 yaver-test-ephemeral:/tmp/yaver-new
  ssh yaver-test-ephemeral "T=/root/.yaver/bin/1.99.149/linux-arm64/yaver; \
    rm -f \$T && cp /tmp/yaver-new \$T && chmod +x \$T && sha256sum \$T"
  ssh yaver-test-ephemeral 'cd /root && (nohup /root/.local/bin/yaver \
    serve --debug >/tmp/yaver-agent.log 2>&1 </dev/null &) && sleep 4 && \
    pgrep -af "yaver serve" | head'
  ```
- Convex `aiModels.ts` change requires `cd backend && npx convex deploy
  --yes` (kivanc runs manually; not part of CI).

## What still needs doing — the Hermes reload-speed work

The user asked for a deep dive on safe Hermes optimizations. Top wins,
unimplemented:

### 1. `-O0` for dev-mode hermesc (biggest single win)
**Current**: `desktop/agent/devserver_http.go:2886` always passes `-O`
(heavy optimize) for non-debug builds. Hermes optimizer is responsible
for ~70% of the reload-time cost (5–12s of the ~10–15s total).

**Plan**:
- Add `FastReload bool` to the `/dev/build-native` request struct.
- `/dev/reload-app mode=bundle` sets `FastReload=true` when re-issuing
  the build request (devserver_http.go#L1831).
- In hermesc invocation, when `req.FastReload` is true, replace `-O`
  with `-O0`. Skip `-output-source-map` (already off in non-debug; no
  change needed).
- Thread `FastReload` into `prepareDevHBCCacheLookup` so the cache key
  reflects the opt level. Result: separate cache lane for fast reloads
  vs production builds — no key collisions.

**Expected savings**: hermesc 5–12s → 1–3s. Reload total 10–15s → 3–6s.

**Trade-off**: HBC runs ~10–15% slower at startup. Irrelevant for
vibing iteration (next patch incoming in 30s anyway).

**Touch**: `desktop/agent/devserver_http.go` (build-native handler +
hermesc args), `desktop/agent/hermes_dev_compile.go` (cache key opts),
`desktop/agent/hbc_cache.go` (HBCCacheKey already encodes OptLevel).
~40 LOC. Add a unit test that exercises both lanes.

### 2. Pre-warm hermesc binary at agent startup
First hermesc invocation per agent lifetime pays ~300ms for OS to
page-in the binary. Run `hermesc -version` once at agent startup so
subsequent calls hit the page cache.
**Touch**: `desktop/agent/devserver.go::Start` (or wherever the agent
boots its dev-server manager). ~5 LOC. Almost free.

### 3. Lazy compilation (`-lazy`)
Hermes supports `-lazy` which defers compilation of individual functions
until they're first called. Near-instant initial bundle load; first call
into each function pays its compile cost.
**Trade-off**: tiny per-call latency on cold paths. For a dev preview
session, almost certainly net-positive on perceived reload speed.
**Risk**: needs a smoke test against the Yaver mobile bundle loader to
confirm Hermes runtime doesn't choke on lazy bytecode. Hermes 0.12+
should be fine; verify with the version yaver ships.

### 4. Confirm Metro persistent cache isn't getting cleared
Each codex patch invalidates ONE file's Metro cache entry, not all —
Metro's incremental graph rebuild is fast IF the cache directory
survives across builds. Verify by inspecting Metro's cache dir between
two reloads on the test box. If it's getting wiped, add a
`--reset-cache=false` or set `metro.config.js::cacheStores` explicitly.

### What I'd NOT do
- Persistent hermesc daemon (no upstream support, fork risk).
- In-process Metro via Node bindings (huge surface, breaks Metro
  upgrades).
- Drop integrity checks (HBC magic / BC version / MD5) — they're cheap
  and necessary for correctness.

## Other follow-ups parked here

### Reload-flow fixes shipped today (all in 1.18.78 + staged agent)
1. **URL bug**: transcript overlay's reload chip used to corrupt the
   relay path; fixed with direct `yaverResolveAgentURL` resolution.
2. **Spinner stuck at 90s**: agent now emits `EmitReloadDone` after the
   `reload_bundle` broadcast; iOS detects it and clears the latch. Old
   build relied on SSE close, which never came because /dev/events is
   long-lived.
3. **Duplicate "Hot reload triggered"**: caused by SSE history replay.
   `?fresh=1` skips it.
4. **`logLine` invisibility**: iOS summarizer now reads it; agent's
   `EmitLog` also mirrors into `message`.

### Convex / mobile alignment for runner+model
- Mobile `setPrimaryRunnerForDevice` auto-picks default model on runner
  change (was the source of "I picked codex but it ran with sonnet"
  bug — codex 400'd with "model not supported with ChatGPT account").
- Agent now reads Convex `userSettings.primaryRunnerByDevice` directly
  in the feedback flow, so the runner is correct even when the mobile
  UserDefault hint is empty.

### Things to test once redeployed
- `yaver-test-ephemeral` on the new agent: feedback flow runs codex
  with gpt-5.4 (not aider, not opus on a codex), reload-app posts
  succeed, transcript shows phase events for THIS reload only and the
  spinner clears on bundle broadcast (no 90s timeout).
- Wireless-push 1.18.78 → vibe a change → tap Reload App → confirm
  spinner clears in ~10–15s (or 3–6s after the `-O0` change lands).

### Hermes-side risks before shipping `-O0`
- The Yaver mobile container's bundle validator
  (`mobile/ios/Yaver/YaverBundleValidator.swift`) checks BC magic +
  version. -O0 doesn't change either. Should be fine.
- Bundle size grows ~30% with -O0 (no DCE / inlining). Relay transfer
  is one of the slower stages on cellular; with `-O0` we might add ~1s
  to the download. Net win still positive on average WiFi+LAN; tighten
  by gzipping the bundle on relay if not already (probably is).
- Some edge-case codepaths in user code (eval, dynamic require) might
  behave subtly differently across opt levels. Yaver's bundles are
  Metro-built RN apps — no eval, no dynamic require. Safe.

## Where to pick this up

1. Implement `-O0` fast-reload lane (item 1 above).
2. Pre-warm hermesc (item 2).
3. Maybe lazy compilation (item 3) after measuring the gain from 1+2.
4. Deploy to yaver-test-ephemeral, push iOS bump.
5. Test with the same "make app background dark gray" prompt — measure
   reload time, confirm <6s end-to-end.

The fixes already staged today should be deployed first (URL bug,
duplicate replay, completion signal, runner allowlist) — they're the
correctness baseline; the `-O0` work is the speed improvement on top
of a working flow.
