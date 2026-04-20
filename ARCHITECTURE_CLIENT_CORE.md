# `@yaver/client-core` — Shared client library

**Status:** proposed. Written 2026-04-20 after the feedback-SDK drifted
from the Yaver mobile app for the fourth time in a week: duplicate
machine rows, stale Convex URL, 60 s vs 90 s heartbeat thresholds,
missing `localIps[]` parallel probing. Every fix was a re-port of
code that already existed in `mobile/src/…` and already worked.

**Design principle.** The Yaver mobile app is the canonical client.
When the Feedback SDK (or any future client) needs to talk to the Go
agent, it should import the same code the mobile app runs — not a
hand-ported copy. Porting stops; sharing starts.

---

## 1. What's being duplicated today

The same logic lives in at least three places, with drift between
each copy:

| Concept | Mobile source of truth | Feedback-SDK copy | Other copies |
|---|---|---|---|
| Convex site URL constant | `mobile/src/lib/constants.ts:8` | `sdk/feedback/react-native/src/auth.ts::DEFAULT_CONVEX_SITE_URL` | `web/lib/convex-client.ts`, `sfmg/src/components/YaverFeedbackWidget.tsx::YAVER_CONVEX_SITE_URL` |
| Device identity / alias / endpoint keys | `mobile/src/context/DeviceContext.tsx:193-296` | `sdk/feedback/react-native/src/deviceDedup.ts` | none (web + desktop never had it) |
| `collapseAliasDevices` | `DeviceContext.tsx:353-413` | `deviceDedup.ts::collapseRemoteDevices` | — |
| HEARTBEAT_STALE_MS (90 s) | `DeviceContext.tsx:117` + `backend/convex/devices.ts:16` | `deviceDedup.ts::HEARTBEAT_STALE_MS` | — |
| `/devices/list` fetch + normalisation | `DeviceContext.tsx:561-654` | `auth.ts::listReachableDevices` | `web/lib/agent-client.ts` |
| `raceDirectCandidates` (parallel `/health` probe of `[quicHost, ...localIps]`) | `mobile/src/lib/quic.ts:2978-3055` | `Discovery.ts::raceProbe` | — |
| Relay presence lookup (`/presence?ids=…`) | `DeviceContext.tsx:66-91` | not yet ported | — |
| LAN UDP beacon listener (port 19837, fingerprint match) | `mobile/src/lib/beacon.ts` | not yet ported | — |
| NetInfo network-change reconnect | `DeviceContext.tsx:1382-1402` | not yet ported | — |
| App-state resume reconnect | `DeviceContext.tsx:1406-1426` | not yet ported | — |
| Heartbeat poll + relay fallback | `quic.ts:3301-3400` | not yet ported | — |
| Auth header builder (`Bearer` + `X-Relay-Password` + tunnel headers) | `quic.ts:2830-2842` | `P2PClient.authHeaders` | `web/lib/agent-client.ts` |
| P2P HTTP request path (`direct → tunnel → relay` base URL resolver) | `quic.ts:634-645` | `P2PClient.baseUrl` | `web/lib/agent-client.ts` |
| OAuth provider URLs / Apple native endpoint | `mobile/src/lib/auth.ts` | `sdk/feedback/react-native/src/auth.ts` | `web/lib/auth.ts`, `sdk/feedback/web/src/auth.ts` |
| BlackBox SSE command stream | n/a in mobile (mobile doesn't host BB) | `sdk/feedback/react-native/src/BlackBox.ts` | — |
| Dedup of relay servers | `DeviceContext.tsx:222-232` | not ported | `web/lib/agent-client.ts` (different impl) |

Consequence: every time the mobile app learns a new trick (e.g. 0.9.x
gained relay-presence override, 1.18.x gained `hwid` identity key),
the Feedback SDK has to play catch-up and every other client starts
diverging. That's what just produced:

- 403 "invalid token" — wrong Convex URL
- 3× duplicate Mac rows — no dedup
- Yellow dot on a green Mac — client-side staleness re-check
- Hot Reload dead — only probed `quicHost`, not `localIps`

All four were already fixed in mobile's tree.

---

## 2. What `@yaver/client-core` should export

**Zero React / React Native / DOM dependencies.** Pure TypeScript so
the package is consumable from:

- Yaver mobile (React Native, primary reference)
- `yaver-feedback-react-native` (React Native)
- `yaver-feedback-web` (browser)
- `web/` Next.js dashboard (browser)
- `desktop/app/` Electron renderer
- `yaver-cli` npm package (Node)
- Any future JS client

Everything listed below is a pure function or plain class.

### 2.1 Types

```
types/
  device.ts          Device, RemoteDevice, DeviceList
  discovery.ts       DiscoveryResult, DiscoveryOptions
  auth.ts            OAuthProvider, User, Session
  blackbox.ts        BlackBoxEvent, BlackBoxCommand
  feedback.ts        FeedbackBundle, FeedbackMetadata
  agent.ts           AgentStatus, RunnerInfo, DevServerStatus
```

### 2.2 Constants

```
constants.ts
  CONVEX_SITE_URL            = "https://perceptive-minnow-557.eu-west-1.convex.site"
  WEB_BASE_URL               = "https://yaver.io"
  DEFAULT_AGENT_HTTP_PORT    = 18080
  DEFAULT_BEACON_UDP_PORT    = 19837
  HEARTBEAT_STALE_MS         = 90_000
  BEACON_STALE_MS            = 10_000
  PROBE_TIMEOUT_MS           = 2_500
  RELAY_PROBE_TIMEOUT_MS     = 6_000
  OAUTH_REDIRECT             = "yaver://oauth-callback"
```

One import, one source. Mobile / SDK / web all read from here. Next
Convex migration = one commit, four clients fixed.

### 2.3 Dedup & merging

```
device/dedup.ts
  deviceIdentityKey(d)                     → string
  deviceAliasKey(d)                        → string | null
  deviceEndpointKey(d)                     → string | null
  mergeDeviceEntries(a, b)                 → Device
  pickActiveOverStaleNeedsAuth(a, b)       → Device | null
  collapseAliasDevices(devices)            → Device[]

device/health.ts
  isDeviceFresh(d, now?)                   → boolean
  pickAutoConnectTarget(list, primaryId?)  → Device | null
```

Ported verbatim from `mobile/src/context/DeviceContext.tsx:193-413`.
No adaptation needed — these are already pure functions.

### 2.4 Discovery

```
discovery/probe.ts
  probeHealth(url, opts?)                  → Promise<DiscoveryResult | null>
  probeHealthWithHeaders(url, headers)     → Promise<DiscoveryResult | null>

discovery/race.ts
  raceDirectCandidates(urls, opts?)        → Promise<DiscoveryResult | null>
  // Promise.any polyfill built in for old Hermes

discovery/convex.ts
  listDevicesFromConvex(convexUrl, token)  → Promise<RemoteDevice[]>
  discoverFromConvex(opts)                 → Promise<DiscoveryResult | null>

discovery/relay.ts
  listRelays(convexUrl, token)             → Promise<RelayServer[]>
  resolveRelayServers(platform, account?)  → RelayServer[]
  applyRelayPresence(devices, relay)       → Promise<Device[]>
  probeRelay(relayBase, deviceId, pwd?)    → Promise<DiscoveryResult | null>
```

### 2.5 Auth

```
auth/session.ts
  validateToken(convexUrl, token)          → Promise<User | null>
  getUserSettings(convexUrl, token)        → Promise<UserSettings>
  saveUserSettings(convexUrl, token, s)    → Promise<void>

auth/oauth.ts
  buildOAuthUrl(provider, client, extras?) → string
  parseCallbackToken(callbackUrl)          → string | null

// Native-only bits stay in each client; cores export only the
// *shapes* they expect.
```

### 2.6 P2PClient

```
client/agent.ts
  buildBaseUrl(state)                      → string     // direct | tunnel | relay
  buildAuthHeaders(state)                  → Headers
  agentRequest(state, method, path, body?) → Promise<Response>
  reloadDevServer(state)                   → Promise</> (/dev/reload w/ /dev/reload-app fallback)
  uploadFeedback(state, bundle)            → Promise</>
  triggerFix(state, reportId)              → Promise</>
  vibingExecute(state, prompt)             → Promise</>

client/blackbox.ts
  streamBlackBoxCommands(state, handler)   → () => void    // returns unsubscribe
  postBlackBoxEvents(state, events)        → Promise<void>
```

`state` is a plain object: `{ convexUrl, host, port, deviceId, token,
relayUrl, relayPassword, tunnelUrl, tunnelHeaders }`. No React hooks,
no singletons — each client owns its own state.

### 2.7 Storage adapter (platform boundary)

```
storage/types.ts
  interface StorageAdapter {
    getItem(key): Promise<string | null>
    setItem(key, value): Promise<void>
    removeItem(key): Promise<void>
  }
```

Each consumer provides its own adapter:

- RN → AsyncStorage or MMKV wrapper
- Web → localStorage wrapper
- Node → fs-based wrapper
- Electron → electron-store wrapper

Core never imports storage directly; it accepts an adapter via
`configureStorage(adapter)`.

### 2.8 Beacon (platform boundary)

Core **doesn't** include a beacon listener — UDP sockets are
platform-specific (`react-native-udp` vs `dgram` vs browser-not-
applicable). What core ships is:

```
beacon/payload.ts
  parseBeaconPayload(raw)                  → BeaconPayload | null
  computeUserFingerprint(userId)           → string    // 8-hex SHA256 prefix
  beaconMatchesUser(payload, fingerprint)  → boolean
  beaconMatchesKnownDevice(payload, ids)   → boolean
```

Clients that want LAN beacon discovery (mobile, feedback-rn) add the
UDP wrapper themselves, but call into these helpers to parse +
fingerprint. Same matching rules, no drift.

---

## 3. What stays platform-specific

Not every file belongs in core. Leave these in each client:

- **UI components.** React Native / Next.js / Electron each want
  their own `<MachinePicker />` / `<LoginScreen />`. Core exposes
  types and fetch helpers; each UI decorates them.
- **Storage implementation.** Different per platform (AsyncStorage,
  MMKV, localStorage, `electron-store`, fs).
- **Network primitives.**
  - `react-native-udp` beacon socket (mobile, feedback-rn)
  - `NetInfo` (mobile, feedback-rn)
  - `expo-web-browser` + `expo-apple-authentication` (mobile,
    feedback-rn)
  - Web's `window.open` popup + `postMessage` (web SDK)
- **Hermes bundle loader / screenshot / screen recording.** These
  are feedback-SDK specific and don't belong in core.

---

## 4. Proposed directory layout

```
yaver.io/
  shared/client-core/
    package.json             name: "@yaver/client-core", main: "dist/index.js"
    src/
      index.ts               re-exports everything
      constants.ts
      types/
      device/
      discovery/
      auth/
      client/
      beacon/
      storage/
    tests/
  mobile/
    package.json             adds "@yaver/client-core": "workspace:*"
    src/lib/
      quic.ts                thin wrapper over core; keeps RN state
      beacon.ts              UDP socket; delegates parsing to core
      auth.ts                Apple/OAuth UI; delegates URL builders to core
    src/context/DeviceContext.tsx
                             replaces local helpers with core imports
  sdk/feedback/react-native/
    package.json             adds "@yaver/client-core": "workspace:*"
    src/
      Discovery.ts           delegates to core
      auth.ts                delegates to core
      deviceDedup.ts         DELETED — use core
      P2PClient.ts           thin wrapper; delegates to core
  sdk/feedback/web/
    src/P2PClient.ts         uses core (browser build)
  web/
    lib/agent-client.ts      uses core
  desktop/app/
    src/main/                uses core
```

Monorepo setup: a simple npm `workspaces` field in the top-level
`package.json` + `shared/client-core` added to `workspaces`. No
extra tool needed.

---

## 5. Publishing model

Two options; start with (a):

**(a) Private workspace, published as part of each consumer's
bundle.** `@yaver/client-core` isn't published to npm — it's a
repo-internal workspace. Each consumer (mobile, feedback-rn, web)
imports it and bundles the code. Simple, no npm registry coupling.
The downside: third-party repos (like SFMG) that depend on
`yaver-feedback-react-native` don't need core themselves because
it's already bundled in the SDK's `dist/`.

**(b) Publish `@yaver/client-core` to npm.** Needed only if we want
*external* reusers. Adds a version-alignment burden (peer-dep
management between SDK ↔ core). Defer until there's demand.

---

## 6. Migration plan — 4 phases

Each phase is independently mergeable; nothing gets published until
the phase after it is also merged. Estimated half-day each.

### Phase 1 — Extract constants + types + pure dedup

**Scope:** just the stuff that's already pure and has no imports.

- Create `shared/client-core` with `constants.ts`, `types/`, and the
  dedup module copied from `mobile/src/context/DeviceContext.tsx`.
- Mobile: import constants from core; delete the local copies.
- Feedback SDK: delete `deviceDedup.ts`; import from core.
- Make `CONVEX_SITE_URL` the single source of truth. Delete every
  other hardcoded URL string in the repo and in SFMG's widget
  (accept a config-override prop instead).

Test: unit tests in `shared/client-core/tests/dedup.test.ts`; mobile
devices tab renders unchanged; feedback-rn picker still dedups.

Outcome: kills the class of "wrong Convex URL" + "missing dedup"
bugs permanently.

### Phase 2 — Move discovery + probe + relay-presence

**Scope:** pure `fetch` helpers.

- Extract `raceDirectCandidates`, `probeHealth`, `applyRelayPresence`,
  `discoverFromConvex` into `shared/client-core/discovery/`.
- Feedback SDK's `Discovery.ts` becomes a ~30-line wrapper.
- Mobile's `quic.ts::_doAttemptConnect` delegates to the same
  helpers.

Test: both clients can connect to the same agent identically on
same-LAN and over relay. Existing e2e suite proves it.

Outcome: kills "feedback SDK only probes `quicHost`, not
`localIps`" and any future parity bug.

### Phase 3 — Move auth + OAuth URL helpers + P2PClient

**Scope:** network wrappers that don't touch UI.

- `@yaver/client-core/auth/` exports `validateToken`,
  `getUserSettings`, `buildOAuthUrl`, `parseCallbackToken`.
- `@yaver/client-core/client/agent.ts` exports
  `buildBaseUrl(state)`, `buildAuthHeaders(state)`, and the common
  endpoints: `reloadDevServer`, `uploadFeedback`, `triggerFix`,
  `vibingExecute`.
- Mobile's `quic.ts` becomes a stateful class that wraps the core
  helpers.
- Feedback SDK's `P2PClient.ts` becomes the same.

Test: existing test suites pass; the two thin wrappers can be
diffed and the only remaining differences should be state
management (singleton in mobile, instance in feedback SDK).

Outcome: "`/dev/reload-app` in SDK, `/dev/reload` in mobile"
never happens again.

### Phase 4 — Beacon parsing + NetInfo glue

**Scope:** the two platform-boundary pieces that still need native
deps.

- Core exports parsers + fingerprinters (no socket code).
- A tiny `@yaver/client-core-rn` workspace glue package provides
  `startBeaconListener` + `subscribeNetworkChange` (requires
  `react-native-udp` + `@react-native-community/netinfo` as peer
  deps). Both mobile and feedback-rn import that, instead of
  hand-writing UDP code.
- SFMG (and any other third-party RN app) picks up LAN beacon
  discovery by installing the two peer deps alongside the SDK.

Test: same LAN discovery latency on both clients; same fingerprint
match behaviour.

Outcome: "feedback SDK has no beacon, falls back to crude IP sweep"
permanently fixed.

---

## 7. What this removes

After Phase 3:

- `sdk/feedback/react-native/src/deviceDedup.ts` — deleted
- `sdk/feedback/react-native/src/Discovery.ts` — ~80% gone, thin
  wrapper over core
- `sdk/feedback/react-native/src/auth.ts` — shrinks ~50% (OAuth URL
  helpers move to core)
- `mobile/src/context/DeviceContext.tsx` — shrinks ~200 lines
  (dedup + merge + presence helpers move out)
- `web/lib/agent-client.ts` — duplicates deleted

Estimated net removal: **~1500 lines of duplicated code**.

---

## 8. Non-goals

- **Replacing `quic.ts` wholesale.** Mobile has legitimate logic
  outside core: session context, screen transitions, user-settings
  persistence. Core provides primitives; `quic.ts` becomes shorter,
  not deleted.
- **Unifying the UI.** Every client keeps its own `<MachinePicker>`
  and login surface. Core exposes the data; UI frameworks render it.
- **Publishing to npm.** Start with a repo-internal workspace
  (option 5a). Only publish if an external consumer needs it.
- **Supporting Flutter / Dart / Go from the same package.** Core is
  TypeScript-only. Flutter/Dart SDKs have their own analogous
  module; keep them in lockstep via shared tests, not shared code.

---

## 9. Why this is the right design

- **One source of truth.** The mobile app is already the source of
  truth (it works). Core simply carves out the platform-neutral
  parts so every other client can use them too.
- **No more porting cycles.** Today a fix to `collapseAliasDevices`
  in mobile takes a week to reach the SDK. After Phase 1 it takes
  one import statement.
- **New clients get discovery for free.** When the web dashboard or
  a future desktop agent needs to talk to a user's agent, it
  imports core and inherits beacon parsing, dedup, presence
  override, relay fallback — all the things that already work on
  mobile.
- **Breaking changes are visible.** A change in core shows up in
  every consumer's git blame. Today a drift between mobile and
  feedback-SDK is invisible until a user's Mac shows up yellow in
  a screen recording.

---

## 10. Concrete first PR

After we agree on the shape:

1. `git checkout -b client-core-phase-1`
2. `mkdir -p shared/client-core/src/{device,types}`
3. Copy `constants.ts` / dedup functions / HEARTBEAT_STALE_MS from
   mobile + backend into their core homes.
4. Add `shared/client-core` to the top-level `package.json`
   `workspaces`.
5. Mobile: change imports → core.
6. Feedback SDK: change imports → core; delete `deviceDedup.ts`.
7. Run both test suites.
8. Green → open the PR.

If there's pushback on any of the imports (API surface, naming),
resolve in the PR thread before proceeding to Phase 2.

---

Last updated: 2026-04-20.

## 11. Phase 1 status — LANDED

Phase 1 shipped in `yaver-feedback-react-native@0.7.4` +
`mobile` version-bump. Scope:

- Created `shared/client-core/src/` with `constants.ts`,
  `endpoints.ts`, `index.ts` — canonical shared source.
- `scripts/sync-client-core.sh` mirrors the directory into
  `mobile/src/_core/` and `sdk/feedback/react-native/src/_core/`
  byte-identical, with a `// AUTO-SYNCED` banner on every
  mirrored file. Invoking with `--check` fails when mirrors have
  drifted (intended for CI).
- `mobile/src/lib/constants.ts` — now re-exports `CONVEX_SITE_URL`
  from `../_core/constants`. No behavior change.
- `mobile/src/context/DeviceContext.tsx` — imports
  `HEARTBEAT_STALE_MS` from `../_core/constants` instead of a
  local literal.
- `sdk/feedback/react-native/src/auth.ts` —
  `DEFAULT_CONVEX_SITE_URL`, `DEFAULT_WEB_BASE_URL`,
  `DEFAULT_OAUTH_REDIRECT` now source from
  `./_core/constants`.
- `sdk/feedback/react-native/src/deviceDedup.ts` —
  `HEARTBEAT_STALE_MS` re-exported from `./_core/constants`;
  local literal removed.

All 66 SDK tests pass. Mobile typecheck is clean.

What this buys today:

- Next Convex deployment migration (if there is one) is a
  **one-line change** in `shared/client-core/src/constants.ts` +
  one invocation of the sync script. No more hunting for the old
  URL across mobile/ + sdk/ + sfmg/ + web/.
- `HEARTBEAT_STALE_MS` can never drift between backend + mobile +
  SDK picker again. (Before today it was 60 s in SDK, 90 s in
  mobile, 90 s in backend.)
- `OAUTH_REDIRECT` is one string, not three.
- Endpoint paths (`AGENT_ENDPOINTS`, `CONVEX_ENDPOINTS`,
  `RELAY_ENDPOINTS`) are available for callers that want typed
  constants instead of raw strings. Consumers will adopt
  incrementally — no big-bang rewrite required.

What Phase 1 did NOT change:

- No React Native bundling changes; Metro just follows the
  relative `./_core/...` imports like any other source file.
- No npm workspace / bundledDependencies gymnastics. The SDK's
  `dist/` ships the compiled `_core/` as part of its own
  `src/` — external consumers install the SDK as they always
  have.
- The dedup algorithm, `Device`/`RemoteDevice` adapter, Discovery,
  beacon listener, heartbeat orchestrator — all still duplicated.
  Future phases.

Phase 2 next: move pure dedup + `raceProbe` + `applyRelayPresence`
into `shared/client-core/src/devices/`. Estimated half-day.
