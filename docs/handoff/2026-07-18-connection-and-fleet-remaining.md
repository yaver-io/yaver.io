# Handoff — connection, runner auth, fleet coordination (2026-07-18)

Status: **not finished.** Written so the next session starts from measurements
rather than re-deriving them. Every claim below has a command that produced it.

## 0. Read this first

Seven hypotheses were refuted in this session. Each was plausible, and each cost
an hour. They are listed at the end under "Dead ends" — read that section BEFORE
theorising, because the same theories will look attractive again.

The pattern: **every fix that held came from a measurement; every wrong turn
came from reasoning ahead of evidence.** Measure first.

---

## 1. Shipped and verified

| Change | Where | Verified by |
|---|---|---|
| `GET /tasks` bounded (50 default, 500 cap, newest-first) | `1.99.312` | mutation tests; was ~8 MB / 4000 tasks |
| Agent diagnostic log — tagged, levelled, 20 MiB ceiling | `1.99.312` | rotation + credential-leak mutation tests |
| Sign in button on the runner banner | TestFlight 444 | user screenshot: modal opened against the mini |
| RunnerAuthModal: ✕ closes; paste box full-width/multiline; Submit own line | `4af8d61d0` | typecheck; **needs build 445** |
| MCP registry namespace `io.github.yaver-io/yaver` | `99456437c` | takes effect next release |
| Deploy quota producer (placement's missing half) | `a2dd066c2` | mutation tests |
| iOS ATS allows arbitrary loads | `443` | verified inside the shipped archive |
| Two-machine lease contention harness | `scripts/lease-contention-check.sh` | ran mini↔MacBook: 1 winner, exclusion holds, clean handoff |

---

## 2. Open, with evidence

### 2.1 Probe storm starves the working connection — FIXED (215f2edc8), NOT device-verified

The phone races direct candidates for **6 devices** continuously (~15 candidates
per cycle, 2.5s timeout each) plus **four concurrent reconnect ladders**. Backoff
never escapes: `1s → 2.1s → 4.1s → 8.4s` then **resets to attempt 1**.

The reset cadence matches `refreshDevices` exactly — every 30s (observed
19:07:23, 19:07:53, 19:08:23, 19:09:22). Both in-code resets are legitimate
(`quic.ts:1707` explicit disconnect, `quic.ts:6437` successful connect), so the
re-entry comes from the **device-refresh path**. Located:

- `DeviceContext.tsx:1348` — `refreshDevices` calls `setDevices(withLocalBox)`
  every 30s with a **freshly built array**. New identity every tick, even when
  nothing about the fleet changed.
- `DeviceContext.tsx:2177` — an effect keyed on **`devices`** (the array, not a
  stable key) therefore re-runs on every tick, re-entering
  `connectionManager.setFocused()` / `clientFor()` / `setConnectionStatus()`.
- `DeviceContext.tsx:2188` — `deviceIdsKey` bumps `autoConnectNonce` whenever
  the ID SET changes, which fires a **full auto-connect sweep**. The logs show
  the count genuinely moving (`Found 8 device(s)` → `Found 6 device(s)`), so
  this fires for real, not just on identity churn.

Note the asymmetry that makes this subtle: `deviceIdsKey` was written precisely
to avoid identity churn — it is a sorted, joined string. The effect one block
above it was not given the same treatment and still depends on `devices`.

Cheapest first move: key the `2177` effect on `deviceIdsKey` (or a targeted
subset) rather than `devices`, and confirm against the Connection Logs that the
30s re-entry stops. That is a small, checkable change — unlike touching the
backoff itself.

Consequence, from one log: relay connected at 19:07:11, and by 19:09:07 even
relay failed — *while a Mac on the same relay got HTTP 200s throughout*. At
19:08:52 the app cold-launched (`JS error handlers installed` + `auth restored`),
killing an in-progress sign-in.

Fix shape: do not reset reconnect state on a routine refresh; stop racing
structurally-unreachable candidates for non-focused devices. `quic.ts:6598`'s
forever-retry for "previously-reachable" hosts is the amplifier — it was judged
cosmetic earlier in the session and is not.

**Shipped in `215f2edc8`**, fixed at the source: `refreshDevices` now keeps the
previous array when nothing material changed (`mobile/src/lib/deviceListEquality.ts`),
so EVERY effect keyed on `devices` is protected — not just the one at 2177.

`lastSeen` is deliberately excluded from the comparison: it advances on every
heartbeat, so including it would make two lists never compare equal and silently
restore this bug while looking like a tightening in review. Mutation-tested —
adding it fails a test named for the storm.

STILL TO DO: verify on device. Install the build, open Connection Logs, and
confirm the 30s `[direct] racing …` re-entry stops when the fleet is unchanged.
Unit tests prove the comparator; only the phone proves the storm is gone.

### 2.2 The mini cannot take agent updates

`POST /devices/request-update` → `{"ok":true,"requestedVersion":"1.99.312"}`.
The device **claimed** it (`desiredAgentVersion` cleared) and never applied it —
still `1.99.311` twelve minutes and several heartbeats later.

Per `backend/convex/devices.ts:894`, a failed update deliberately does not
re-fire, so the failure is invisible on every surface and the 6-12h auto-update
cycle is the only backstop. Look at `claimAgentUpdateRequest` and whatever
consumes it agent-side.

Circular: the diagnostic log that would explain this ships **in** `1.99.312`.

### 2.3 Claude Code is signed out on the mini

Not a false positive — ground truth from `/runner-auth/status`:

```
claude    ready=False  authConfigured=False  "signed out on this machine"
codex     ready=True   authConfigured=True   ✅
opencode  ready=True   authConfigured=True
glm       ready=True   authConfigured=True
```

Route work to **codex** until this is signed in. Note the field trap:
`authVerified=true` means *the check ran*, not *auth is valid*.

### 2.4 Remote sign-in never yields a URL

The modal sits on "Waiting for the verification URL from the remote CLI…"
indefinitely. Polling now surfaces failures after 4 consecutive misses
(`RunnerAuthModal.tsx` ~line 152), but the underlying question is unanswered:
does the remote `claude` login command emit a URL at all on that box? Check
agent-side `/runner-auth/browser/start`.

### 2.5 Smaller, confirmed

- **`172.17.0.1` (Docker bridge) is still probed** — `isLikelyDockerBridgeIP`
  should have stripped it. Either that device runs an old agent or the filter
  has a gap.
- **`publish-mcp-registry` fails while the run reports success.** Green runs
  hiding failed steps. §1's namespace fix should clear the 403; the reporting
  weakness remains.
- **The mini holds ~4000 tasks and had 11 GB free.** `1.99.312` bounds the
  *response*, not the *store*. Pruning is the user's call — it is their data.

### 2.6 Needs a product decision, not code

**Phone-side Yaver Mesh does not exist as a build target.**
`mobile/ios/YaverMeshTunnel/PacketTunnelProvider.swift` is present but appears
**0 times** in `project.pbxproj` — never compiled. That is why no log has ever
contained a `lan-mesh` candidate.

iOS permits **one** active VPN tunnel, so shipping it makes Yaver Mesh and
Tailscale mutually exclusive on the phone. Either ship the NetworkExtension and
accept that, or build an app-scoped userspace path (larger). The user's standing
rule — never occupy the phone's VPN slot — points at the second.

Also unresolved: `MeshSubnetCIDR` (100.96.0.0/12) sits **inside** Tailscale's
100.64.0.0/10. Corrected in `b8bc13db5` with `SubnetRouteConflict` so default-on
defers to an existing VPN, but relocating the range is fleet-breaking and was
not done.

### 2.7 Untouched queue

- `docs/handoff/managed-cloud-wake-broken.md` **§5–§9**.
- `docs/handoff/mobile-local-assistant-wiring.md` — **does not exist**, and
  never has (`git log --all --diff-filter=A` finds nothing). Ask what it meant.

---

## 3. Dead ends — do not re-derive

| Theory | Why it's wrong |
|---|---|
| iOS ATS blocks tailnet | `NSAllowsArbitraryLoads: true` verified **inside the 443 archive** |
| Tailnet routing / ACL broken | Safari on the phone loaded `http://<mini-tailnet-ip>:18080/health` fine |
| The mini's agent is wedged | `/health` 0.8s, load 2.41, agent at 1.3% CPU |
| Web dashboard lies about runner auth | Could not find the code; `authVerified` appears **nowhere** in web or mobile. Unsubstantiated — do not repeat it |
| Reconnect only tries direct, never relay | It calls `attemptConnect()`, the full ladder |
| `quic.ts:6598` forever-retry is cosmetic | It is the probe-storm amplifier (§2.1) |
| No path to update the mini without SSH | `/devices/request-update` exists — it just doesn't work (§2.2) |

**RN hides the error code.** `fetch` collapses every `NSURLError` into
`"Network request failed"`, so ATS-blocked and no-route are indistinguishable
from the phone. Any theory resting on that string is unfalsifiable there —
measure from the box or from another machine instead.

## 4. Useful commands

```bash
# Reach the mini without Tailscale (relay works; needs BOTH headers)
curl -H "Authorization: Bearer $TOKEN" -H "X-Relay-Password: $RP" \
  https://public.yaver.io/d/<deviceId>/health

# Ground truth on runner auth
  .../d/<deviceId>/runner-auth/status

# Two-machine lease exclusion
scripts/lease-contention-check.sh <ssh-target>
```

Relay is Tailscale-independent and was measured at **5/5, ~450ms** from a
machine with Tailscale **down**. When something looks like a network failure,
compare `/health` (tiny) against the endpoint in question — that single
comparison is what finally cracked the `/tasks` bug.
