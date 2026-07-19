# Autorun task — connectivity + mesh hardening, voice UX, then deploy

Run this on the Mac mini. The full diagnosis you are implementing is
`docs/architecture/CONNECTIVITY_MESH_AUDIT.md` — **read it first, in full.**
Every fault below is code-anchored there; do not re-derive the analysis, and do
not trust a line number that has drifted (trust the code and correct the doc in
the same change, per CLAUDE.md).

## Non-negotiable constraints

1. **NEVER disrupt Tailscale on this Mac mini.** It is the only remote access
   path to this box. No change may remove, shadow, or race a route in
   `100.64.0.0/10` here. Do not run `yaver mesh up`, `POST /mesh/up`, or
   anything that calls `ConfigureNetwork` on this machine. Test mesh routing
   logic with unit tests only. If you cannot test a mesh change without
   bringing mesh up on this box, **leave it untested and say so** — losing the
   box is far worse than an untested branch.
2. **`git commit -- <paths>` ALWAYS.** Never `-a`, never `add -A`. This machine
   runs many sessions concurrently.
3. Work in your own dedicated clone on your own branch. Never two autoruns in
   one checkout.
4. Do not revert, force-push over, or stash away anyone else's work. There are
   ~15 other autorun clones and a `vibe-voice` branch with uncommitted changes
   on this box. They are not yours.
5. **Coalesce deploys.** One deploy per converged change, at the very end — not
   one per iteration. TestFlight is capped at ~15–20 uploads/app/day and has no
   rollback.

## Phase 0 — disk clearance (do this first; the box is nearly full)

The Data volume had ~4 GB free at hand-off. The TestFlight preflight requires
**20 GB**, so Phase 5 cannot even start until this is done.

Reclaim only **regenerable** artifacts. In rough priority:

- Go build/test cache: `go clean -cache -testcache -modcache` (modcache last;
  it is the big one and costs a re-download).
- `node_modules` inside **stale autorun clones** under `~/Workspace/*-autorun*`
  — regenerable via `npm install`. Do NOT delete the clones themselves.
- Gradle caches (`~/.gradle/caches`), CocoaPods cache (`~/Library/Caches/CocoaPods`),
  npm cache (`npm cache clean --force`), `~/Library/Caches/*` generally.
- Xcode `DerivedData`, `Archives`, `iOS DeviceSupport`, unused simulator
  runtimes (`xcrun simctl delete unavailable`), `/tmp/Yaver*`, `/tmp/*.xcarchive`.

**Before deleting anything, `ls -la` it and confirm it is a cache or build
artifact.** Never delete a git repo, a working tree, a keystore, `~/.yaver`,
`~/.appstoreconnect`, `~/.claude`, or anything under `keys/`. If a directory's
contents contradict its name, stop and report it rather than deleting.

Also: check for and stop any stray in-flight builds/deploys before starting —
a previous session left **two concurrent `xcodebuild -exportArchive`
processes** racing. `ps aux | grep -E 'xcodebuild|gradle|expo'`.

Target: **≥ 40 GB free** before Phase 5. Report the before/after numbers.

## Phase 1 — mesh safety (highest priority; ship this even if nothing else lands)

Per audit §4c, `SubnetRouteConflict` guards exactly one of three mesh bring-up
paths. Route it into the other two:

- `desktop/agent/mesh_http.go:27` `handleMeshUp` — reachable from the CLI **and
  from the phone** ("enable mesh on all machines").
- `desktop/agent/mesh_agent.go:366` `meshConvergeDesired` — re-enters `Start()`
  every 30s on console-driven desired-state change, so it can undo a correct
  boot-time deferral.

Factor the guard into one helper so a fourth bring-up path cannot miss it, and
return a clear remedy string naming the conflicting interface and route (per the
"carry the why into the error text" rule).

Also fix `backend/convex/mesh.ts:35`, which still claims the mesh range is
*"Deliberately OUTSIDE Tailscale's 100.64.0.0/10"*. That is arithmetically false
(`100.96/12` ⊂ `100.64/10`) and it is the allocator that hands out addresses.
`desktop/agent/mesh/device.go:33-55` already carries the retraction; propagate it.

Add unit tests asserting each bring-up path consults the guard. Today
**zero** tests assert this (audit §7).

## Phase 2 — connectivity faults (audit §1, §2, §3, §5)

In audit §6 order:

1. **Stop the read-path union of `localIps`** — `backend/convex/devices.ts:364`
   in `mergeListedDevices`. Recency-stamp each IP with the reporting row's
   `lastSeen`, drop stale ones, cap the set, and never union from an offline
   row. The agent and the heartbeat mutation are **innocent** — do not "fix"
   them. Note `sameDeviceList` compares `lanIps` element-wise
   (`deviceListEquality.ts:119`), so stabilising this also kills the probe storm.
2. **Negative-cache unroutable legs.** `directProbeFailure.ts:42` — an instant
   `Network request failed` currently falls through every branch and is
   indistinguishable from a transient failure, so impossible legs are re-raced
   forever. Classify it `unroutable`, cache per (network identity, candidate),
   and gate tailnet/mesh candidates on **observed membership** rather than the
   `allowTailnet` preference (`quic.ts:6137`). The desktop side already has
   this shape in `localMeshUp()`.
3. **Split relay auth errors.** `userSettings.ts:712` returns the same failure
   for a dead session token as for a bad password; `relay/server.go:955`
   reports both as one string; `main.go:11195` then misroutes recovery into a
   password refetch that is a provable no-op. Give them distinct codes and
   route the token case to re-auth. This is the most likely reason the mini
   goes dark for hours.
4. **Bound `probeMobileDeviceStatus`** (`deviceStatus.ts:213-278`) with the same
   phase deadline `raceDirectCandidates` already has (`quic.ts:6204`), and stop
   double-pushing port 18080 (`quic.ts:6150`).
5. **Stop asserting connected without checking.** `DeviceContext.tsx:1453` uses
   the unvalidated in-memory `isConnected` flag; add the repair rung to
   `quic.ts scheduleReconnect` (it has none); make `lastError` during a ladder
   carry the real cause instead of masking it with `Reconnecting (n/max)...`,
   which defeats the self-heal string match at `DeviceContext.tsx:2437`.

Also fix the security consequence in audit §2b: the session bearer is attached
to every private-IP leg including stale ones and `172.17.0.1`, so it is sprayed
in cleartext at whatever host now owns that address.

**Requirement: this must be robust both on and off Tailscale.** The phone is
frequently on 5G with no tailnet, and the mini is Tailscale-only. Those two
facts together are the whole problem — when relay is wedged there is genuinely
no path, so the ladder must fail *fast and honestly* rather than retrying
impossible legs at full rate.

## Phase 3 — mesh stability parity (audit §4e)

Bar: "as stable as regular Tailscale, with or without Tailscale underneath."

1. **Periodic endpoint re-registration** — the single largest defect.
   `meshRegisterJoin` is called from three places and the one in
   `mesh_agent.go:371` sits inside an `if !changed { return }` guard, so a node
   that roams Wi-Fi→cellular advertises a dead endpoint forever.
2. **Data-plane liveness in `reconcileLoop`** (`mesh/manager.go:290`) — it never
   re-invokes `ConfigureNetwork` and never verifies the interface still holds
   its address or that the route still exists.
3. **Link-change / sleep-wake hooks** — there are none. The boot retry ladder is
   one-shot.
4. **MTU probing** — hardcoded 1420 (`mesh/device.go:24`). Required before
   mesh-over-Tailscale can work at all: nested in another tunnel this silently
   drops large packets and nothing notices.
5. **Decide and act on the mobile mesh contradiction**: `withMeshTunnel` is
   missing from `mobile/app.json`'s plugins array, so the phone has no mesh data
   plane in any shipped build — yet `quic.ts:6135` still races `lan-mesh`
   candidates. Either register the plugin or stop building those candidates.
   Today it is neither, which is the worst of both.

Note honestly in the commit that subnet routes and exit node are **advertised
but non-functional** (`AllowedIPs` is cryptokey routing; no host route is ever
installed). Do not claim they work.

## Phase 4 — voice UX follow-ups

Already landed on `main` (commit `bc81f993e`): the Tasks "+" FAB is gone (mic is
the single FAB) and Vibe has a "Prefer to type?" pill that hands off to the
Tasks composer seeded with the last heard transcript. Build on that:

1. **In mic mode, run STT and show the text live as it is recognised.** The
   partial transcript must be visible while speaking — today `vibe.tsx` shows a
   live line, so verify it actually updates continuously from partial STT
   results and is not only populated on endpoint. Make the recognised text
   unmistakably visible and readable.
2. **From text mode, always allow going back to mic.** The Tasks composer must
   carry a mic affordance back to Vibe, so the switch is symmetric. Today the
   hand-off is one-way (Vibe → composer); make it round-trip. Preserve any typed
   text when switching back, the same way `lastHeardRef` preserves speech going
   the other way.
3. Keep both affordances quiet and non-competing — voice is the primary path,
   typing is the escape hatch, and neither should shout.

Cross-surface parity applies: the RN surfaces (mobile/tablet/car/glass) share
`DeviceContext`/`AuthContext` and get connectivity fixes for free — **verify**
that rather than assuming. tvOS, watchOS, Wear OS and web have their own code
and need explicit ports; note the per-surface status in the commit.

## Phase 5 — verify, then deploy (once, at the end)

Gate before any deploy:

- `cd desktop/agent && go test -count=1 ./...` — note: `go build` does not
  compile test files, so BUILD:OK proves nothing here.
  **Warning:** broad `go test` in `desktop/agent` has previously signed the box
  out by hitting the real `~/.yaver`. Scope test runs; do not run unbounded
  `-run` patterns against auth paths.
- `cd relay && go test ./...`
- `cd mobile && npx tsc --noEmit` — necessary but NOT sufficient; it is a known
  false green for RN screens.
- `cd web && npm run build` — `tsc --noEmit` alone is a known false green.

Then deploy, in this order, **once**:

1. **Go agent** — build + publish per the repo's normal path.
2. **TestFlight** — `$(yaver vault env --project mobile)` then
   `./scripts/deploy-testflight.sh`; if the vault is locked, `source
   ~/.appstoreconnect/yaver.env` instead. Run `mobile-cache-cleanup.sh preflight`
   first (hard-fails under 20 GB). Headless codesign on this box needs BOTH
   `yaver-ci.keychain-db` and `login.keychain-db` unlocked with
   `set-key-partition-list` — the deploy script sources
   `~/.yaver/local-secrets.env` to self-unlock.
   - There is an **uncommitted `mobile/ios/Yaver/Info.plist` CFBundleVersion
     bump (447→450)** on this box from a killed deploy. Reconcile it before
     archiving; do not let it silently ride along.
   - Respect the daily upload cap. If "Upload limit reached", **stop and report**
     — do not retry.
3. Report what actually shipped. If a deploy failed, say so with the output. Do
   not report a deploy as done because the command exited 0 — verify the build
   appears in TestFlight.

## Definition of done

- Audit §6 items 1–6 implemented, or explicitly reported as not-done with the
  reason.
- Mesh guard on all bring-up paths, with tests.
- Voice UX: live STT text visible in mic mode; symmetric mic↔text switching.
- Disk ≥ 40 GB free.
- Gates green, agent + TestFlight deployed once, outcome reported honestly.
- Tailscale on this Mac mini still up and reachable. **Verify this last, and
  verify it explicitly** — `tailscale status` plus an actual inbound check.
