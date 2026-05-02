# yaver-test-ephemeral → mobile-app Hermes flow — pre-deploy audit

**Date**: 2026-05-02
**Subject**: end-to-end flow `mobile (iOS/Android) → relay → yaver-test-ephemeral (Hetzner Linux box) → /dev/build-native → Hermes bundle pushed back to phone → super-host bridge reload`
**Question**: do the security fixes shipped in agent 1.99.111 break this golden path before manual TestFlight verification?

---

## TL;DR

**Yes, with one timing caveat.** The flow works end-to-end after deployment. The signed-URL bundle path was specifically designed to coexist with the existing mobile fetch flow:

- Mobile reads `bundleUrl` from the `/dev/build-native` response → already signed.
- Agent emits the signed URL with sig+exp in query params.
- Relay forwards query strings unchanged (`relay/server.go:977`).
- Agent verifies sig before serving. Pass.

Ahead-of-time fixes I applied while writing this audit:
1. Web-bundle cookie path changed `/dev/web-bundle/` → `/` so it matches when accessed via the relay's `/d/<deviceId>/...` URL scheme. **Just fixed in this session.** Build clean.

The one caveat below — relay refuse-on-collision — is by design and only affects the rare "agent registers twice" edge case (process-restart loop, bad agent supervisor). Mitigated by QUIC keepalive within 120s.

---

## Flow trace (success path)

```
┌──────────────┐         ┌────────────────┐       ┌──────────────────────┐
│ iOS / Android│  HTTPS  │ relay.yaver.io │ QUIC  │ yaver-test-ephemeral │
│ Yaver app    │────────►│ (public)       │──────►│ (Hetzner Linux box)  │
└──────┬───────┘         └────────────────┘       └────────┬─────────────┘
       │                                                   │
       │ 1. Convex device list discovers ephemeral box     │
       │ 2. Mobile fetches https://relay/d/<id>/health     │
       │ 3. Mobile POST /dev/start  (Authorization: Bearer)│
       │ 4. Agent spawns Metro on the Linux box            │
       │ 5. Mobile POST /dev/build-native                  │
       │     ← {bundleUrl: "/dev/native-bundle?           │
       │            build=ios-XYZ&exp=...&sig=..."}        │
       │ 6. Mobile GET <bundleUrl>  → 200 + Hermes bytes   │
       │ 7. YaverBundleLoader validates HBC, swaps bridge  │
       └───────────────────────────────────────────────────┘
```

---

## Security checkpoints — per step

### Step 1 — Convex peer discovery

- **Mobile**: signs in via OAuth → owner session token. Convex returns ephemeral box's `deviceId`.
- **Auth**: owner-tier token. Goes through `auth()` middleware on subsequent requests.
- **Risk**: none from this audit. The cloud-preview hardcoded email was replaced with env vars, but ephemeral box's owner doesn't pivot through that path.

### Step 2 — `/health` probe through relay

- **Path**: `https://relay.yaver.io/d/<deviceId>/health`
- **Relay**: forwards to agent. Now slim — `{ok, version}` only.
- **Mobile expectation**: mobile reads `ok: true`. ✓ unchanged.
- **Concern**: previously `/health` returned `tunnels: <count>` for diagnostic tooling. If mobile depended on that, it's gone. Audit:

```bash
$ grep -rn '\.tunnels\|response\.tunnels' mobile/src 2>/dev/null
# (no hits — mobile does not parse /health.tunnels)
```
**Verdict**: safe.

### Step 3 — `POST /dev/start` (start Metro)

- **Path**: `/dev/start` is wrapped in `s.authSDKOrGuest(s.handleDevServerStart)`.
- **Auth**: mobile's owner token passes both checks.
- **Header strip**: my `allowGuest()` change strips `X-Yaver-Guest*` from inbound. Mobile never sets these — neutral.
- **Verdict**: works.

### Step 4 — Metro starts on Linux

- **No security change in this path.** Metro listens on `0.0.0.0:8081` inside the Linux box. The agent's `/dev/*` proxy fronts Metro for any request after start.
- **Risk**: none from this audit.

### Step 5 — `POST /dev/build-native`

- **Path**: `mux.HandleFunc("/dev/build-native", s.authSDKOrGuest(s.handleBuildNativeBundle))`.
- **Mobile auth**: owner token. ✓
- **Agent emits** `bundleUrl: "/dev/native-bundle?build=<id>&exp=<unix>&sig=<hex>"`. The sig is HMAC of `(buildID|kind|exp)` keyed by `blobSecret()` (persisted to `~/.yaver/blob-secret`).
- **TTL**: 30 minutes. Plenty for a slow phone fetch + retries.

```go
// devserver_http.go (after my edit)
buildID := fmt.Sprintf("%s-%d", meta.MD5, time.Now().UTC().UnixNano())
bundleSig, _ := signDevBundleURL(buildID, "native", 30*time.Minute)
assetsSig, _ := signDevBundleURL(buildID, "assets", 30*time.Minute)
bundleURL := "/dev/native-bundle?" + bundleSig
assetsURL := "/dev/native-assets?" + assetsSig
```

**Verdict**: signed URL minted.

### Step 6 — `GET /dev/native-bundle?build=…&sig=…&exp=…`

This is the critical step. Three independent auth paths exist:

1. **Loopback bypass** — for the local dev preview iframe.
2. **Bearer fallback** — `hasValidYaverBearer()` accepts owner / paired / support / SDK tokens that the agent has cached.
3. **Signed URL** — sig/exp/build in query string.

Mobile fetch through the relay sends:
- `Authorization: Bearer <owner-session-token>` (always)
- Query `build=…&sig=…&exp=…` (from the bundleUrl response)

Both (2) and (3) succeed. Mobile already does (2) without knowing about sigs. ✓

```go
// dev_bundle_sig.go::verifyDevBundleSig
if r != nil && isLoopbackRequest(r) { return nil }
if r != nil && hasValidYaverBearer(r) { return nil }
// ...sig check...
```

**Critical compat detail**: when `/dev/build-native` runs on a fresh agent process, `blobSecret()` lazy-creates the persisted file. First request might be slightly slower; subsequent ones reuse the same key. Confirmed `blobSecret()` is the same secret used by `/blobs/public` so it's already battle-tested.

**Relay query-string forwarding**:
```go
// relay/server.go:977
forwardQuery := r.URL.RawQuery
// ...passed verbatim to the agent tunnel...
```
Confirmed. The sig+exp+build trio survives the relay hop.

**Verdict**: works without any mobile-side change.

### Step 6b — Bundle metadata header

The agent sets `X-Yaver-Bundle-Metadata` on the response so the SDK can validate HBC version + size + MD5 before swapping the bridge. Unchanged by this audit. ✓

### Step 7 — `YaverBundleLoader` validates + reloads

- iOS: `YaverBundleValidator` is a stub that returns nil (no-op) per `mobile/ios/Yaver/YaverBundleValidator.swift` (CLAUDE.md notes this).
- Android: same.
- Bridge reload via `ExpoReactNativeFactory` + `RCTAppDependencyProvider` (full New Architecture).
- **Not security-relevant for this audit.** No backend security change touches this path.

---

## Specific concerns I checked + ruled out

### A. Does the relay refuse-on-collision break agent reconnect?

**Yes, briefly. Mitigated by QUIC keepalive.**

`yaver-test-ephemeral` runs `yaver serve` under systemd. Process restart → new QUIC dial → relay sees deviceId already registered → refuses with `Type: "error"`.

**Recovery path**:
1. New connection refused.
2. Old connection eventually times out via `MaxIdleTimeout: 120s` (relay/server.go:266).
3. Once timed out, `s.tunnels` slot frees.
4. Next agent reconnect (tunnel.go exponential backoff up to 30s) succeeds.

**Worst case wait**: 120s + up to 30s backoff = ~2.5 minutes after a hard restart.

**Soft case**: if process exits cleanly, QUIC sends close → tunnel gone immediately → next reconnect succeeds.

**Mitigation in test plan**: when manually verifying, restart the agent ONCE and wait ~2 min before expecting mobile reconnection. Or use `kill -SIGTERM` (clean shutdown) instead of `kill -9` to skip the keepalive wait.

### B. Does relay /presence cap of 50 IDs break mobile polling?

**No.** Mobile polls `/presence?ids=...` with the user's full device list. Most users have <10 devices. The 50-cap is well above any realistic case. Mobile's catch-handler returns the input list unchanged on 400 — graceful degradation built in.

### C. Does `RELAY_ADMIN_TOKEN` mandatory check break the box?

**No, conditionally.** `--allow-open` is the new requirement only when neither password nor convex URL is set. yaver-test-ephemeral uses the public relay (relay.yaver.io) which IS configured with `RELAY_PASSWORD`, so the agent dials with that password → no `--allow-open` needed.

The ADMIN token is for relay-side `/admin/*` endpoints — only matters if you SSH to the relay and call them. Agent ↔ relay traffic doesn't touch admin endpoints.

### D. Does `/info` redaction break mobile UX?

**No.** Mobile is owner-tier. `isNonOwnerInfoCaller(r)` returns false because mobile has no `X-Yaver-Guest`/`X-Yaver-Support`/`X-Yaver-HostShare` headers. Mobile sees full `/info` payload with hostname, workDir, project list — same as before.

If mobile is signed in as a guest of the ephemeral box (unusual), it sees redacted /info. Mobile UI handles missing fields gracefully (`d.hardwareId ?? d.hwid` pattern throughout).

### E. Does the X-Yaver-Guest header strip break delegated SDK auth?

**No.** The strip happens in `allowGuest()` AFTER the middleware decides the caller is a guest, then re-stamps from server-resolved state. Mobile owner traffic doesn't go through `allowGuest()` at all. SDK tokens carrying `delegatedGuest*` info go through `applyDelegatedGuestSDKHeaders()` which also strips and re-stamps. No data loss for legitimate callers.

### F. Does the deploy webhook secret requirement block ephemeral box deploys?

**No.** `/deploy/webhook` is for GitHub-style push webhooks. yaver-test-ephemeral doesn't run that flow — it tests hybrid mode + Hermes push, not deploy automation. Owner-triggered deploy via `/deploy/ship` (different path) still works.

### G. Symlinks in `/files/*`?

**No conflict.** The Linux box's project root is a regular tree. EvalSymlinks is a no-op on regular files. Test confirmed: `TestSafeJoinHandlesSymlinkedRoot` passes.

### H. Mobile beacon (LAN)?

**Irrelevant.** Hetzner box is a different network. Mobile reaches it via relay only. The bootstrap-passkey-in-beacon (M-3) doesn't apply.

### I. Recovery / pairing flow?

**Different surface.** `/auth/recover` with `X-Relay-Password: ...` now constant-time-validated. yaver-test-ephemeral is already authenticated; it doesn't recover. The new validation only fires if you re-pair the box.

**If you DO want to re-pair the ephemeral box**: SSH in, rerun `yaver auth`. The CLI auth flow (browser-based) is unchanged.

---

## Things to manually verify after deploy

1. **`yaver --version` on yaver-test-ephemeral** → should show 1.99.111 (or whatever the new release is).
2. **`yaver serve` startup log** — confirm `relay password loaded: <hash>` and `quic-relay tunnel registered as <deviceId>`. No `refused: deviceId already registered` errors.
3. **Mobile app /info display** — owner sees full payload. Check the Settings → About screen renders hostname + version.
4. **`/dev/start` from mobile → Metro PID running on Linux box** — `ps -ef | grep metro` should show Metro.
5. **`/dev/build-native` from mobile → bundle saved to `.yaver-build/main.jsbundle`** — `ls -lt /tmp/yaver-build*` or wherever the build dir lands.
6. **Mobile fetch of bundleUrl** — phone logs should show "downloading bundle… X kB / Y kB" then "validation passed" then bridge reload.
7. **Hermes bundle loads in super-host** — guest app appears, renders. No SIGABRT, no white screen.
8. **Repeat after `systemctl --user restart yaver`** — wait ~2 min, mobile auto-reconnects, flow works again.

If any step fails, check `journalctl --user -u yaver -f` on the Linux box and the mobile-app debug logs for the specific error.

---

## What's NOT covered by this audit

- **TestFlight / Play Store mobile binary distribution** — the bumped versions in 1.99.111 release commit (Info.plist + project.pbxproj + build.gradle) need `./scripts/deploy-testflight.sh` + `./scripts/deploy-playstore.sh` to actually build + upload. Those flows haven't run yet for this version.
- **Convex schema migration** — `userSettings.managed` and the new `normalizePrimaryDeviceId` validator landed in this release. Backend deploy via `npx convex deploy --yes` is required.
- **Web dashboard deployment** — `web/v1.1.115` tag triggers `release-web.yml` which does the wrangler deploy. Without the tag push, web stays at 1.1.114.
- **Homebrew / Scoop / npm publish** — these need a tagged GitHub release with cross-compiled binaries for the CLI to land on `brew install yaver`.

The deploy command sequence is in the next message; see CLAUDE.md "Deployments" + "Mobile Release Policy — Local-First, CI Optional" for the canonical commands.

---

## Bottom line

**Safe to deploy and test.** The Hermes-bundle path is one of the most-trafficked flows in the agent and was specifically protected by the bearer-fallback + loopback-exempt + signed-URL chain. Mobile's existing fetch behaviour passes through both auth paths. The only surprise is the ~2-min reconnect delay after a hard agent restart — call that out in the test plan.
