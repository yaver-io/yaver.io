# Mobile-triggered headless Yaver re-auth on a remote box

**Status**: design doc + gap analysis. The plumbing exists; the UI gating is wrong.
**Trigger case**: `yaver-test-ephemeral` is in bootstrap (no recent heartbeat),
mobile shows `Disconnected · [Retry]`, the only visible recovery button is
**Reset Auth** (destructive factory wipe).

---

## 1. The user expectation

> When my primary box is in bootstrap state and the agent is reachable on
> its public URL, the mobile app should be able to trigger
> `yaver auth --headless` on that remote box over HTTP, surface the
> resulting device-code URL in the app, let me open it in an in-app browser
> to sign in, and recover the session — **without SSH and without wiping the
> agent**.

That is exactly the contract of `POST /auth/recover` with `mode: "device-code"`
on the agent side, and `recoverAgent(undefined, "device-code")` on the mobile
side. The endpoint and the client wrapper both exist.

The reason it does not fire today: the **only place in the mobile UI that
calls `recoverDeviceAuth` is gated on `device.needsAuth === true`**, and
`needsAuth` is not the same condition as "Convex hasn't seen a heartbeat in
36 minutes."

---

## 2. What already exists

### 2.1 Server: `/auth/recover` mode=device-code

`desktop/agent/auth_recover.go:409` — `func (s *HTTPServer) handleAuthRecover`.

- Mounted at `/auth/recover` in `httpserver.go:429` **without** the `s.auth(...)`
  middleware. Recovery is the path for an agent whose token is gone or rejected,
  so requiring a valid local token to call it would be circular.
- Also mounted by the bootstrap listener at `auth_bootstrap.go:262`. So a
  freshly factory-reset box still answers `/auth/recover`.
- Authentication of the *caller* uses one of two proofs:
  - **host-token** (preferred). The caller presents their own Convex bearer.
    The agent calls Convex's `verifyHostToken` to confirm the bearer's user
    owns the device's hardware fingerprint. If yes → `authedAsHost = true`.
  - **bootstrap secret** (legacy / no-prior-pairing). Pre-shared secret in the
    `secret` body field.
- `mode: "device-code"` requires `authedAsHost = true` (`auth_recover.go:499`).
  Mobile signed in as the device owner satisfies this.
- On accepted call:
  1. POSTs to Convex `/auth/device-code` with the agent's hardware fingerprint
     to get a fresh device-code pair.
  2. Stores a `recoverySession{id, waitToken, …}` in memory with a 15-minute
     TTL.
  3. Spawns `completeDeviceCodeInBackground(convexURL, dc.DeviceCode, recovery, s)`
     — a goroutine that polls Convex on the agent side, writes the new token
     to `~/.yaver/config.json` when authorized, and triggers
     `/auth/reload-from-disk` so the daemon picks up the token without waiting
     for the next 5-minute heartbeat tick.
  4. Returns `{ok:true, mode:"device-code", deviceCodeURL, userCode, expiresAt,
     recoveryId, waitToken, nextAction:"open-browser", state:"awaiting_browser_oauth"}`.
- Rate-limited to 1 call / 5s / IP (`auth_recover.go:433`). X-Forwarded-For
  is honored so each relay client gets its own bucket.

### 2.2 Server: `/auth/recover/session` poll

`auth_recover.go:634` — `GET /auth/recover/session?id=&wait_token=`.

- Returns `{state, nextAction, browserURL, userCode, expiresAt, …}`.
- Lets the caller distinguish *waiting for user to finish OAuth* /
  *authorized — token applied* / *expired* without re-hitting the rate-limited
  POST endpoint.
- Currently **not used** by mobile. Mobile blindly refreshes the device list
  at 1s and 5s after opening the browser sheet.

### 2.3 Mobile: `quicClient.recoverAgent`

`mobile/src/lib/quic.ts:6640` — thin POST wrapper.

```ts
async recoverAgent(
  secret?: string,
  mode: "pair" | "device-code" | "direct" = "pair",
): Promise<RecoveryResult | null>
```

- Iterates `recoveryTargets()` (`quic.ts:1064`):
  active baseUrl → relays → HTTPS tunnels → LAN beacon → Convex host:port.
- POSTs `{mode}` (or `{secret, mode}`) to `${target.baseUrl}/auth/recover`.
- Stops iterating on `429` (rate-limited), `409` (agent already healthy),
  `403` (host-token rejected) — all of those mean the agent has spoken and
  retrying through another transport just hits the same agent.
- Returns the parsed `RecoveryResult` shape:
  ```ts
  {
    ok: true,
    mode: "device-code",
    deviceCodeUrl: "https://yaver.io/auth/device?code=ABCD-EFGH",
    userCode: "ABCD-EFGH",
    expiresAt: "...",
    recoveryId: "...",
    waitToken: "...",
    targetUrl: "https://2859809c-….yaver.io",  // which transport won
  }
  ```

### 2.4 Mobile: `recoverDeviceAuth` orchestrator

`mobile/src/context/DeviceContext.tsx:1861`.

Multi-step recovery dispatcher:

1. Probe `/info` for `lifecycleState`.
2. If `bootstrap` → `quicClient.ownerClaimDevice` (HWID-pin claim) ± direct
   bootstrap recovery.
3. Else `recoverAgent(undefined, "direct")` — single-call host-token push.
4. On direct failure (and not rate-limited) →
   `recoverAgent(undefined, "device-code")`.
5. If `deviceCode.ok && deviceCode.deviceCodeUrl`:
   - `await WebBrowser.openBrowserAsync(deviceCode.deviceCodeUrl, {…})` —
     in-app Safari sheet on iOS.
   - On Android < API 18 / web, falls back to `Linking.openURL`.
   - Schedules `refreshDevices()` at +1s and +5s.

**This is the exact flow the user is describing**, and it is wired
end-to-end. The agent's background goroutine catches the token; mobile's
refresh catches the heartbeat coming back online.

### 2.5 Mobile: auto-trigger on `agentAuthExpired`

`DeviceContext.tsx:2053` — `useEffect` watches the active device and
`agentAuthExpired` flag, and silently calls `recoverDeviceAuth` up to
`AUTO_RECOVERY_MAX_FAILS = 2` times per session. Beyond that, the user is
expected to tap the recovery button manually.

`agentAuthExpired` is set when *probes / heartbeats from this mobile* observe
401s with `WWW-Authenticate: Yaver authExpired=1`. It is **not** set just
because the agent went silent.

### 2.6 Mobile: existing UI surface

`mobile/src/components/DeviceDetailsModal.tsx:885` — RECOVERY section.
Renders three rows:

1. **Ping** — always visible. One-shot `/info` probe.
2. **Recover Yaver auth without wiping the machine** — *gated on
   `device.needsAuth === true`*. Calls `OwnerClaimAuthRow` → `recoverDeviceAuth`
   → device-code OAuth.
3. **Factory-reset Yaver auth** — always visible. Calls
   `quicClient.factoryResetDeviceAuth` → wipes config + device id.

The Disconnected banner at the top of every screen ("Disconnected ·
yaver-test-ephemeral [Retry]") routes Retry → `selectDevice(device)`. There is
no recovery affordance there.

---

## 3. Why it didn't fire on `yaver-test-ephemeral`

The screen-recording at `~/Downloads/ScreenRecording_05-03-2026 10-32-04_1.MP4`
shows:

| Surface | Reading |
|---|---|
| Top banner | `Disconnected · yaver-test-ephemeral [Retry]` |
| Device card | `PRIMARY ★`, `READY TO CONNECT`, `PUBLIC ENDPOINT` |
| Card subtitle | `ready to connect · 35m ago` |
| Detail panel | `Status: Offline`, `Last agent signal: 36m ago` |
| Detail panel | `Active path: via https://2859809c-…yaver.io` |
| Detail panel | `Reported host: undefined:18080` |
| Coding agents | Claude Code / Codex / OpenCode — all `not signed in` |
| Recovery section | only **Ping** and **Reset Auth** are visible |
| `yaver primary status` | `bootstrap (no recent heartbeat — re-auth with yaver primary auth)` |

The mismatch:

- The agent on the box has been silent for ~36 minutes — **Convex has flipped
  `IsOnline=false`** but **never received an explicit `authExpired=true`
  signal** (because the agent didn't manage to deliver it). So
  `device.needsAuth` is `false`, even though the agent IS in bootstrap.
- The recovery row is gated on `device.needsAuth === true`. → Not rendered.
- The user is left with `Reset Auth` (factory wipe — preserves projects/vault
  but blows away the device id and pushes the box back to a fresh-pair flow)
  as the only visible non-Ping action.

### Why earlier `yaver primary codex` printed `Yaver: signed in`

The CLI probe (`probeOwnedDeviceReauth` in `mcp_auth_recovery.go:168`) actually
GETs `/info` over the live transport. At 10:02 it succeeded — agent was up
and healthy. By 10:32 the agent had stopped heartbeating; either the
auth file got wiped/expired, or the daemon restarted into bootstrap.

The CLI's *next* call `fetchRunnerAuthStatusRowsRemote` then went through
`resolveRemoteAgentCandidates`, which gated on Convex's stale `IsOnline=false`
flag and bailed before trying any candidate. That earlier fix (relax the
gate in `agent_mesh_remote.go:267`) is unrelated to mobile and only affects
the desktop CLI.

---

## 4. The fix (small, targeted)

### 4.1 Broaden the recovery-button gate

`mobile/src/components/DeviceDetailsModal.tsx:902`:

```tsx
{(device.needsAuth || !device.isOnline || pingedLifecycleState === "bootstrap") ? (
  <>
    <Text>Re-auth Yaver (headless)</Text>
    <Text>
      Open a one-time browser sign-in for this box. The agent runs the OAuth
      poll on its side; when you finish in the browser, the new session is
      written back without SSH and without wiping the machine.
    </Text>
    <OwnerClaimAuthRow device={device} />
  </>
) : null}
```

`pingedLifecycleState` is what the existing `PingRow` already learns from the
`/info` probe — surface it via component state and feed it back to this gate
so users get the button after a single Ping if the box is reachable but in
bootstrap.

### 4.2 Make the "Disconnected" banner reauth-aware

When the banner is shown for >N seconds AND `device.publicEndpoints` is
non-empty AND `device.isOnline === false`, render a secondary action `Re-auth`
next to `Retry`. Tap → `recoverDeviceAuth(device)`. Uses the same dispatcher.

### 4.3 Replace blind double-refresh with `/auth/recover/session` poll

`DeviceContext.tsx:2026-2027` — instead of two `setTimeout(refreshDevices)`
ticks, poll `GET /auth/recover/session?id=${recoveryId}&wait_token=${waitToken}`
every 2s for up to 3 minutes (the agent's recovery TTL is 15m, but the device
code itself expires in ~10m). Surface intermediate states:

- `awaiting_browser_oauth` → spinner with "Open the URL on this phone or any
  device with a browser. Sign in to authorize this box."
- `authorized` → "Re-authorized. Reconnecting…"
- `expired` → "Code expired before sign-in. Tap Re-auth to start again."

### 4.4 (Optional) Top-of-section "Reachability" indicator

After `PingRow` runs, surface its result above the recovery section:

- agent **reachable + healthy** → no recovery row needed
- agent **reachable + bootstrap / auth-expired** → recovery row primary CTA
- agent **unreachable** → recovery row disabled with "Try `yaver primary auth`
  from your laptop or `yaver ssh primary` to bring the box back" note

---

## 5. Sequence diagram (post-fix)

```
phone                                agent (yaver-test-ephemeral)         convex
  |  device list shows Status:Offline, Last signal 36m ago                  |
  |  user taps Ping or opens device modal                                   |
  |  --GET /info-------------------> bootstrap server / live agent          |
  |  <--{lifecycleState:"bootstrap"}---                                     |
  |  recovery row shown                                                     |
  |  user taps "Re-auth Yaver"                                              |
  |  --POST /auth/recover {mode:"device-code"}--->                          |
  |                                  verifyHostToken(bearer)                |
  |                                  ----POST /auth/device-code----------> |
  |                                  <---{deviceCode, userCode, expiresAt}-- |
  |  <--{deviceCodeURL, userCode, recoveryId, waitToken}---                 |
  |  WebBrowser.openBrowserAsync(deviceCodeURL)                             |
  |  --GET /auth/recover/session?id=…--> (loop 2s)                          |
  |                                  completeDeviceCodeInBackground         |
  |                                  ----poll convex device-code---------->  |
  |  user signs in on yaver.io/auth/device in the browser sheet             |
  |                                  <---{token}--------------------------- |
  |                                  write ~/.yaver/config.json             |
  |                                  trigger /auth/reload-from-disk         |
  |                                  resume heartbeats ------> heartbeat -->|
  |  <--{state:"authorized"}---                                             |
  |  banner clears, refreshDevices()                                        |
```

---

## 6. Out-of-scope but related

- **Bootstrap-only listener vs main daemon listener**. When the agent is in
  bootstrap, `auth_bootstrap.go` brings up a small server with just the auth
  endpoints. If the relay is configured to forward to the main daemon and the
  main daemon is dead, `/auth/recover` over relay will hit a 502. The fix is
  to make the relay tunnel agnostic to which listener owns the routes (the
  pieces are already there in `relay/main.go` — just needs the bootstrap
  listener to register the same path table).
- **Convex `device-code` session ownership**. The device code is bound to the
  agent's hardware fingerprint, not to the requesting mobile. So a user could
  in theory open the URL on someone else's phone — that's fine, OAuth still
  validates the human, and Convex pairs the resulting token to whoever signed
  in. This *does* mean the URL is shareable by design.
- **Concurrency**. Two simultaneous `/auth/recover` POSTs from the same IP
  will hit the 5s rate limiter; second one returns 429. Mobile should
  surface this as "Wait a few seconds…" instead of falling through to other
  recovery modes (which it already does — `recoverAgent` short-circuits on
  429).

---

## 7. Test plan

- **Unit**: extend `auth_recover_test.go` with a test that posts
  `mode:"device-code"` with a valid host-token and asserts the response shape.
- **Integration**: on `yaver-test-ephemeral`, factory-reset the agent, observe
  `device.isOnline=false` from mobile, confirm Re-auth button appears, tap it,
  open the URL on the same phone in the in-app sheet, sign in, observe the
  agent's heartbeat resume and the banner clear within ~10s.
- **E2E (Playwright web)**: web dashboard already exercises `direct` and
  `pair` modes. Add a path that forces the agent into bootstrap and runs
  `device-code` to completion against a Convex test deployment.

---

## 8. Files to touch

```
mobile/src/components/DeviceDetailsModal.tsx   # broaden gate, rename row
mobile/src/context/DeviceContext.tsx           # poll /auth/recover/session
mobile/src/lib/quic.ts                         # add recoverSessionStatus(id, waitToken)
mobile/app/(tabs)/devices.tsx                  # disconnect-banner Re-auth action
desktop/agent/auth_recover_test.go             # device-code path coverage
```

No agent-side code changes required — the endpoint is complete.
