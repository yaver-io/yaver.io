# Reconnect / Re-auth / Bootstrap — Deep Audit (Round 2)

Date: 2026-05-01
Author: assistant audit pass on top of `reconnect-reauth-bootstrap-audit.md` and `reconnect-reauth-bootstrap-implementation-report.md`

## Why this document exists

The first audit identified gaps. The implementation report claims those gaps are largely closed. After reading the actual source — agent (`desktop/agent/auth_bootstrap.go`, `auth_owner_claim.go`, `auth_recover.go`, `device_lifecycle.go`, `auth_pair.go`), mobile (`mobile/src/lib/deviceStatus.ts`, `mobile/src/context/DeviceContext.tsx`), web (`web/lib/agent-client.ts`, `web/lib/use-devices.ts`, `web/components/dashboard/DevicesView.tsx`), and the relay (`relay/server.go`) — only some of the impl report's claims hold. Several gaps are still open, and a few new ones surfaced.

The user-facing target is narrow:

> **A remote box is up and on the internet. Its bootstrap HTTP server is alive (direct or via relay). The mobile/web client should be able to reconnect that box without SSH and without copy-pasting passkeys, in one click.**

This document audits whether that target is actually reachable today, and lists the remaining work to make it deterministic.

## TL;DR

- Lifecycle contract (`AgentLifecycleInfo`) is real, wired through both bootstrap and authenticated `/info` + `/health`, and consumed by mobile + web.
- `owner-claim` exists and works for the happy path (relay reachable, prior owner, active pair session). It is the *only* practical relay-only reclaim path for previously-owned boxes.
- The implementation report's claim that owner-claim has "dedicated coverage" is partially true: 4 of the 6 audit-listed scenarios have tests; **2 are still missing** (guest-token rejection, transient pair-session rotation), and there is **no relay-proxied E2E test**.
- The mobile `recoverBootstrapDevice` has a structural design problem: its relay path **cannot succeed by design** (passkey is suppressed over relay) and silently falls through to `ownerClaimDevice`. It's not broken, but it's wasted round-trips and noisy logs.
- `ownerClaim` is **relay-only**. There is no Tailscale, Cloudflare Tunnel, or direct-LAN fallback. If the relay is down for any reason but the box is reachable some other way, the one-click reclaim cannot fire.
- The lifecycle contract's `usable` and `recoverable` fields are reported by the agent but **not read by any client** — dead bits today.
- A subtle race exists between bootstrap-mode `/auth/recover` (which calls `StartPairingSession`) and the bootstrap loop's own `session` pointer. Worth fixing or proving benign.
- Truly-fresh boxes (newly minted `device_id` with no Convex row) cannot be reached remotely at all — `/devices/bootstrap` returns "Device not found", relay can register the tunnel but Convex never lists the device. The audit acknowledged this; it remains unsolved.

## Verification of the implementation report's claims

| Claim | Status | Evidence |
|---|---|---|
| `device_lifecycle.go` exposes canonical contract | ✅ true | `desktop/agent/device_lifecycle.go:18-58` defines `AgentLifecycleInfo` with state/usable/recoverable/recoveryMode/supportsOwnerClaim/ownerClaimReady/requiresFirstPair |
| Bootstrap `/info` + `/health` include lifecycle | ✅ true | `auth_bootstrap.go:441-487` |
| Authenticated `/info` + `/health` include lifecycle | ⚠️ partly verified | Mobile/web both read `info.lifecycle.state || info.lifecycleState`, so the field is present, but I did not directly read `httpserver.go` to confirm both endpoints emit it |
| `auth_owner_claim_test.go` exists with real coverage | ✅ true, but incomplete | 4 tests, see G1 below |
| Mobile reads `info.lifecycle.state` first | ✅ true | `mobile/src/lib/deviceStatus.ts:46-48` |
| Mobile relay bootstrap probe carries auth headers (the "real bug" from the impl report) | ✅ true | `DeviceContext.tsx:1614-1625` constructs `relayTargets` with `Authorization: Bearer ${token}` and `X-Relay-Password` |
| Web reads lifecycle from probe payload | ✅ true | `DevicesView.tsx:223-227, 257-263, 287-291`; `dashboard/page.tsx:166-235` |
| Owner-claim spliced into pair session | ✅ true | `auth_owner_claim.go:107-141` builds a synthetic `/auth/pair/submit` and dispatches it through the same handler |
| Connected panels stop using raw `needsAuth` | ⚠️ mostly true | `dashboard/page.tsx:1326-1327` derives lifecycle and treats `bootstrap` or `yaver-auth-expired` as the same "needs reauth" pill, but several legacy `connectedNeedsAuth = !!liveDevice.needsAuth` reads still exist around line 1473 |

The fixes in the implementation report are real. The remaining issues are below.

## Gaps and bugs

### G1. owner-claim test coverage is 4/6, not 6/6

The first audit listed six required scenarios. Current `auth_owner_claim_test.go` covers four:

| Scenario | Test? |
|---|---|
| no `device_id` (truly fresh) | ✅ `TestOwnerClaimRejectsFreshBootstrapWithoutDeviceID` |
| device not in caller's owned list | ✅ `TestOwnerClaimRejectsUserWhoDoesNotOwnDevice` |
| **guest / shared-scope token** | ❌ missing — handler enforces `match.IsGuest || (AccessScope != "owner")` (`auth_owner_claim.go:100`) but no test |
| no active pair session | ✅ `TestOwnerClaimRequiresActivePairSession` |
| **transient pair-session rotation** | ❌ missing — agent rotates the bootstrap pair session every 10 minutes; a claim that lands during the gap should retry, but there's no test |
| success path through a relay-style proxy | ❌ missing — `TestOwnerClaimSubmitsBearerIntoActivePairSession` is direct only; nothing exercises `__rp=` query forwarding or `X-Forwarded-For` headers |

The implementation report's wording ("dedicated coverage") oversells this. The two missing tests are exactly the ones an attacker or a flaky network would trip first.

**Severity: high** — these are the failure modes most likely to surface under real-world load.

### G2. `recoverBootstrapDevice` (mobile) has an unreachable relay arm

In `DeviceContext.tsx:1599-1673`, the function builds a list of `directTargets + relayTargets` and tries them in order. Over relay it calls `/info`, then expects `info.bootstrapPasskey || info.passkey` to be present (line 1644).

But by deliberate design (`auth_bootstrap.go:85-100, 477-481`):
- `bootstrapPasskeyVisible` returns `false` whenever the request has `X-Forwarded-For`, `X-Relay-Password`, or comes from a non-LAN IP.
- The relay arm always sets `X-Relay-Password`.
- → `info.bootstrapPasskey` is **always empty** over relay.

The function then falls into:
- **encrypted pair branch** (line 1647): `submitEncryptedPair(target.url, token, device.publicKey, pairCode)` — but `pairCode` is empty and `handlePairEncrypted` rejects with 400 "missing code" (`bootstrap_security_test.go:65-68` confirms).
- **passkey pair branch** (line 1649): `submitPair({code: pairCode, ...})` with empty code — also rejected.
- **else branch** (line 1656): `lastError = "did not expose a passkey"`, continue to next target.

So the relay arm of `recoverBootstrapDevice` **always fails by design**. The actual relay-only reclaim works only because `recoverDeviceAuth` (line 1675) catches the failure and falls through to `ownerClaimDevice` (line 1694).

This is wasted work (one or more 3.5s-timeout HTTP round-trips per relay) and confusing logs. It also makes the mobile flow harder to reason about: the function appears to handle relay but it doesn't.

**Severity: medium** — net behavior is correct; latency, log noise, and review-time confusion are the costs.

**Fix**: in `recoverBootstrapDevice`, skip relay targets entirely (they will always fall through), or make relay targets jump straight to the owner-claim path. Simpler: have `recoverDeviceAuth` only call `recoverBootstrapDevice` for direct-reachable devices, and call `ownerClaimDevice` first when the only reachable transport is relay.

### G3. `owner-claim` and lifecycle probe are relay-only — no Tailscale / tunnel / LAN fallback

Both clients hard-code relay-only iteration:

```ts
// web/lib/agent-client.ts:2719
if (this.relayServers.length === 0) {
  return { ok: false, error: "no relay servers configured" };
}
for (const relay of this.relayServers) { ... }
```

```ts
// mobile DeviceContext via quicClient.ownerClaimDevice — same shape
```

And `probeMobileDeviceStatus` (`deviceStatus.ts:84-148`) iterates relays, then direct LAN, but never:
- Tailscale CGNAT (`100.64.0.0/10`) addresses
- Cloudflare Tunnel hostnames (`tunnelUrl` / `publicEndpoints` on `Device`)

In the user's stated scenario ("box is up and connected to internet"), tunnel-only or Tailscale-only reachability is realistic. Today, those boxes show as `bootstrap` but cannot be reclaimed because the only reclaim path is relay.

**Severity: medium-high** for the stated user goal (which is exactly "remote box, on the internet, not LAN").

**Fix**: extend `ownerClaimDevice` and `probeMobileDeviceStatus` to also try `device.tunnelUrl` and Tailscale-format `lanIps`. The agent's `auth_bootstrap.go:307-323` already starts a relay tunnel in bootstrap mode; if the user has Cloudflare or Tailscale wired, those should work too. The owner-claim handler doesn't care about transport — it only cares that the request lands.

### G4. Lifecycle contract has dead bits

`AgentLifecycleInfo` exports `Usable` and `Recoverable` (`device_lifecycle.go:21-22`). They are emitted on every probe but no client reads them:

- `mobile/src/lib/deviceStatus.ts` reads only `state`, `bootstrap`, `authExpired`.
- `web/lib/use-devices.ts` declares them in the type, but `DevicesView.tsx` only branches on `state`, `requiresFirstPair`, `supportsOwnerClaim`, `ownerClaimReady`.

This is harmless today, but it means future clients have no canonical signal for "the box reports it's usable" without re-deriving from `state`. Either remove the unused fields or wire them into the CTA decision (e.g. a CTA could be "Reconnect" only when `recoverable && !usable`).

**Severity: low** — cleanup, but worth a one-line decision.

### G5. Type safety in `device_lifecycle.go`

`AgentLifecycleState` is a typed `string`. The four recovery-mode constants (`AgentLifecycleFreshBootstrap`, `AgentLifecycleBootstrapRecover`, `AgentLifecycleReauthRecover`, `AgentLifecycleNoRecovery`) are plain `string` and the `RecoveryMode` field uses untyped `string`. Easy to typo at the call sites (`bootstrapLifecycleInfo` / `lifecycleInfo`).

**Fix**: introduce `type AgentLifecycleRecoveryMode string` and use it for both the constants and the field.

**Severity: low**.

### G6. Bootstrap `/auth/recover` may have a session-rotation race

`runBootstrapServe` (`auth_bootstrap.go:188-395`) starts its own pair session via `StartPairingSession(bootstrapPairingTTL)` and blocks on `<-session.done`.

`handleAuthRecover` mode=`pair` (`auth_recover.go:372-397`) calls `StartPairingSession(10*time.Minute)` if no current session is reusable. If this fires while bootstrap is mid-loop, it can end the bootstrap loop's session and start a new one. The bootstrap loop is now blocked on a `done` channel that will never close (the new session's `done` is in package-level `activePairing`, not in the local `session` variable).

The 10-minute `time.After` in the bootstrap loop will eventually rescue it by rotating to a fresh session, so the agent doesn't deadlock — but the recovery's pair-submit may have already landed and persisted to disk via `applyRecoveredAuthToken`, while the bootstrap loop never sees `session.done` close and so never re-execs into `yaver serve`. Result: token saved, agent stuck in bootstrap until the next 10-minute rotation… or until the next recovery starts another session and the user retries.

This needs a closer read of `StartPairingSession` (does it `EndPairingSession` first? Does it close the old `done`?). The code path *might* be benign because `applyRecoveredAuthToken` flips `s.authExpired` and saves config — but the bootstrap loop reads neither.

**Severity: medium** — needs investigation; if confirmed, the fix is for `runBootstrapServe` to listen on `activePairingSnapshot()` deltas, not its own captured pointer.

**Repro plan**:
1. Start `yaver serve` on a box with no token (bootstrap loop active, session A).
2. From a phone, hit `/auth/recover` with mode=pair (creates session B via `StartPairingSession`).
3. Submit a token to session B's code via `/auth/pair/submit`.
4. Observe whether the bootstrap loop wakes and re-execs, or stays blocked.

### G7. `recoverDeviceAuth` double-probes over relay

`recoverDeviceAuth` (`DeviceContext.tsx:1675-1774`) does:
1. Line 1680: `probeMobileDeviceStatus(device, token, 3500)` — hits relay/info first.
2. If lifecycleState=`bootstrap`, line 1686: `recoverBootstrapDevice(device)` — which probes relay/info **again** as part of its target loop.

Two 3.5 s probes back-to-back over relay = 7 s minimum before any reclaim attempt fires. With a degraded relay, this can stretch to 14+ s before the user sees a result.

**Fix**: pass the already-fetched `lifecycleProbe.info` into `recoverBootstrapDevice` (it can use the cached `bootstrapPasskey` for direct, and skip relay altogether per G2).

**Severity: low-medium** — UX latency.

### G8. Truly-fresh boxes cannot be reclaimed remotely (Case D from audit)

`auth_bootstrap.go:296-302` mints a fresh UUID when `cfg.DeviceID == ""` and tries to register with Convex via `notifyConvexBootstrap`. Convex's `/devices/bootstrap` requires an existing record matching the `(deviceId, hardwareId, publicKey)` triple — the audit's Case D explicitly notes that "a box without an active pair session" or "no `device_id`" must use the URL/QR pair flow.

But in the remote-from-cafe scenario, the user has **no LAN reach** to scan a QR code. The relay tunnel is up (the agent registered it), but Convex shows the device as "not found" so the dashboard never lists it.

There is no protocol path to discover a fresh-bootstrap box from afar.

**Severity: medium** — this is rare in practice (most boxes are previously owned) but it's the single-case the user explicitly named ("box is up and connected to internet"). For a clean install this is broken.

**Possible fix**: have the relay surface a "pending pair" presence channel (`/relay/pending`) listing boxes that registered tunnels with `bootstrap-pending` token but have no Convex row. Web/mobile dashboards can show these as "uninvited devices, click to claim". The claim endpoint would create the Convex row from the agent-supplied (deviceId, hardwareId, publicKey, relayPassword) triple after the user proves account ownership, then trigger the normal owner-claim handshake. This is a real protocol extension, not a UI patch.

### G9. No end-to-end smoke test for relay-only bootstrap reclaim

Both audit and impl report flag this. Confirmed not present. The four owner-claim handler tests are in-process; nothing exercises the full path:

```
phone → relay /d/<id>/auth/pair/owner-claim
  ?__rp=<password>
  Authorization: Bearer <user-token>
→ relay validates password, forwards over QUIC tunnel
→ agent verifies ownership against Convex
→ agent splices bearer into active pair session
→ pair-submit logic persists token + re-execs
→ next probe sees lifecycle=ready-to-connect
```

This is the highest-value test for the user's stated goal.

**Severity: high**.

### G10. Stale device-row identity targeting (audit's finding 6) is unaddressed

The implementation report says "remaining gap". `web/lib/use-devices.ts:181-200` does hardware-id-first dedup, but devices without `hardwareId` fall back to `(platform, name)` — racy across renames, and ignores `publicKey` rotation.

A reclaim that targets the wrong logical identity will silently succeed against the wrong device or fail with a misleading "device not connected to relay" if the surviving row's `id` no longer matches a live tunnel.

**Severity: medium** — this is the "fixed but not fixed" failure mode the audit warned about.

### G11. The owner-claim handler hits Convex inside the request

`auth_owner_claim.go:80-86` calls `listDevicesForOwnerClaimFn(cfg.ConvexSiteURL, bearer)` synchronously. Convex round-trip is typically 100–300 ms, plus the 12 s client-side timeout (`agent-client.ts:2707`) means a slow Convex turn can block the relay's QUIC stream. Fine in practice, but worth monitoring — under Convex degradation, owner-claim degrades too.

**Mitigation**: nothing to change today; document the dependency. If it becomes a problem, add a 5 s timeout around the Convex call and return a structured "convex slow, retry" error.

**Severity: low**.

### G12. Concurrent recovery on multiple devices is not throttled

`recoveringAuthRef` in `DeviceContext.tsx:1781-1799` is keyed per device. If a user has 5 boxes that all flip to `auth-expired` simultaneously (e.g. a Convex deploy invalidated tokens), all 5 recovery flows run in parallel, each hitting Convex `/devices/owner-by-hardware` and the relay. Not catastrophic, but a stampede on a degraded backend.

**Fix**: a global semaphore (max 2 concurrent reauths) is enough.

**Severity: low**.

## Architectural observation

The largest remaining structural issue is **transport asymmetry**:

- `probeMobileDeviceStatus`: relay → direct
- `recoverBootstrapDevice`: direct → relay (with broken relay arm per G2)
- `ownerClaimDevice`: relay only
- Web's `agent-client.ts`: relay first for almost everything, with Cloudflare tunnel and Tailscale only honored on connect, not on recovery

Different functions choose different transport orderings. That makes failure modes hard to predict and hard to test. A single `chooseTransports(device, purpose)` helper that returns an ordered list would let recovery, probe, and connect share the same fall-through.

## Implementation plan

Ordered by value × cost.

### Phase 1 — Close the test asymmetry (high value, low cost)

Goal: catch the failure modes the user is most likely to hit, before they hit them.

1. Add `auth_owner_claim_test.go` cases:
   - Guest token rejection (build `DeviceInfo{IsGuest: true}` or `AccessScope: "shared-scoped"`).
   - Pair-session rotation: start session A, end it, start session B, fire owner-claim during the gap (should 409 cleanly).
2. Add `auth_recover_test.go` case: bootstrap-mode `/auth/recover` mode=`pair` interacting with the bootstrap loop (asserts the loop re-execs after the recovered token lands). This nails down G6.
3. Add a relay-proxied integration test (`relay/expose_proxy_test.go` style) that:
   - Spins a fake relay, fake bootstrap agent.
   - Phone fires `POST /d/<id>/auth/pair/owner-claim?__rp=<pw>` with a Convex stub.
   - Asserts 200, asserts a pair-submit happened, asserts agent flips lifecycle to `ready-to-connect`.

This is the smallest investment that turns the user goal into a CI guarantee.

### Phase 2 — Fix the structural mistakes (high value, medium cost)

Goal: stop wasting round-trips and stop pretending relay can do passkey pair.

4. In `DeviceContext.tsx`, separate the recovery dispatcher:
   - If `lifecycleProbe.path === "direct"` and `bootstrap`: call `recoverBootstrapDevice` (passkey works).
   - If `lifecycleProbe.path === "relay"` and `bootstrap`: skip `recoverBootstrapDevice`, go straight to `ownerClaimDevice`.
   - If `auth-expired`: existing `auth/recover` path.
5. Pass the already-fetched probe `info` into `recoverBootstrapDevice` so direct doesn't re-fetch.
6. Apply the same shape on web (`agent-client.ts` `reauthDevice` already does most of this; align mobile to match).

### Phase 3 — Transport breadth (medium value, medium cost)

Goal: honor the user's actual statement ("up and on the internet" includes Tailscale and tunnels).

7. Extract `chooseTransports(device, purpose)` in mobile + web. Purposes: `probe`, `reclaim`, `connect`. For each, return an ordered list of `{url, headers, label}`.
8. Add Tailscale and Cloudflare tunnel fallbacks to `probeMobileDeviceStatus` and `ownerClaimDevice`. The agent handler is transport-agnostic; the only blocker is the client-side iteration list.
9. Add a `web/components/dashboard/DevicesView.tsx` test for the CTA decision matrix (state × transport × supports-owner-claim).

### Phase 4 — Truly-fresh box discoverability (medium value, high cost)

Goal: close audit Case D so a clean install is reachable from a cafe.

10. Add a relay endpoint `GET /relay/pending` that lists deviceIds whose tunnel registered with `bootstrap-pending` and that have not produced a successful `/devices/bootstrap` response (relay can mirror this lazily off its register stream).
11. Add a Convex mutation `claimPendingDevice(deviceId, hardwareId, publicKey, relayPassword)` that creates the device row tied to the calling user, then returns OK.
12. Web/mobile dashboards consume this: a "pending devices on your network" section, one tap to claim.

This is a real protocol slice and the only one in this plan that requires Convex schema changes.

### Phase 5 — Identity hardening (medium value, high cost)

Goal: kill the "fixed but not fixed" class.

13. Make `deviceIdentityKey` always require `hardwareId || publicKey`. If neither is present on a stale row, mark it as a ghost and surface in the UI as "pending data, cannot reconnect" rather than letting reconnect target a guess.
14. Add a Convex migration that backfills `hardwareId` from heartbeat metadata for rows that lost it.
15. Add a unit test for `deviceIdentityKey` that proves a renamed device + missing hardwareId scenario does NOT collapse two distinct boxes into one row.

### Phase 6 — Cleanup (low value, low cost)

16. Type `AgentLifecycleRecoveryMode` (G5).
17. Decide on `usable`/`recoverable` (G4): either remove or wire into a CTA gate.
18. Add a global semaphore for concurrent reauth (G12).
19. Audit `dashboard/page.tsx` for remaining raw `liveDevice.needsAuth` reads and replace with `deriveDeviceLifecycleState`.

## Recommended order if there's only one week

If only one week is available, do **Phase 1 + Phase 2 in full**, plus item 7 (transport-chooser extraction) from Phase 3. That:

- locks the existing happy path with real CI tests,
- removes the wasted round-trips,
- gives a clean seam to add Tailscale/tunnel fallbacks later without re-touching the dispatcher.

Phase 4 is the only item that delivers genuinely new capability (truly-fresh remote reclaim). It deserves a separate planning round because it crosses Convex, relay, and clients.

## Risks if nothing changes

- The user's stated goal works *most* of the time today: previously-owned box, relay tunnel up, owner-claim succeeds. That covers ~80% of the real-world scenarios.
- The 20% it does not cover (relay degraded, tunnel-only reachability, fresh installs, identity collisions) will continue to produce "box is up but reconnect failed" reports, which is exactly the loop the first audit was trying to break.
- Owner-claim has no relay-proxied test; a regression in the relay's `__rp=` query handling, or in the agent's pair-submit splice, will only be caught by users in production.

## Conclusion

The implementation report is directionally honest: the protocol-first move was real, owner-claim is real, lifecycle is real. But it overstates coverage and leaves the asymmetry between probe/reclaim/connect transport orderings in place.

The shortest path to "the user's box is up, one tap reconnects it" is:

1. Prove the happy path in CI (Phase 1, ~1 day).
2. Stop the relay arm of `recoverBootstrapDevice` from doing impossible work (Phase 2, ~1 day).
3. Honor Tailscale and Cloudflare tunnels for owner-claim (Phase 3 partial, ~2 days).

Everything beyond that is genuinely new product surface (Phase 4) or hygiene (Phase 5–6). Don't ship hygiene as a substitute for the test gap in Phase 1.
