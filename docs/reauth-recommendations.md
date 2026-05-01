# Yaver Reauth — Recommendations for Codex Implementation

Date: 2026-05-01
Author: live-debugging session against the Hetzner test box, building on
top of `docs/reauth-recovery-report.md` and the actually-shipped
behavior verified end-to-end today.

## TL;DR

The architectural report (`reauth-recovery-report.md`) is sound but
its session-bound recovery model is overkill for the *primary* owner-
authed path. Today's deep-debugging session confirmed end-to-end that
**`/auth/recover` mode=direct is a single round-trip** and is what
mobile / web / agent-CLI should converge on. Sessions remain the right
shape for `pair`, `device-code`, and bootstrap-secret modes, plus for
headless MCP callers (Claude Code / Codex) that need a poll loop.

What follows is the recommendation set, ordered by leverage. Each item
is independently shippable. The primary-path UX win (mode=direct
everywhere) is already in flight — items below build on that.

## Current code delta

This file must be read against the code, not by itself.

As of the current tree:

- The first-pass MCP recovery tools already exist in
  `desktop/agent/mcp_auth_recovery.go` and are registered in
  `desktop/agent/mcp_tools.go`:
  - `recovery_transport_status`
  - `recovery_target_status`
  - `recovery_target_start`
  - `device_reauth_status`
  - `device_reauth_start`
  - `device_reauth_wait`
  - `runner_auth_browser_start/status/submit_code/cancel`
- Alias-aware selection is already wired for both the shared remote
  resolver and owned-device reauth helpers, including `@alias` style
  selectors in the CLI path.
- What is still missing is the **session-bound recovery waiter**
  described below. `device_reauth_wait` currently polls the remote
  machine's state via `/info`-style probing instead of polling a
  recovery capability. That is good enough for the happy path, but it
  is not the final secure/stable shape for `pair`, `device-code`, or
  bootstrap-secret recovery.
- `recovery_target_wait` is still not implemented.

So the remaining focus is not "add recovery MCP from zero"; it is
"replace probe-based waiting with capability-scoped waiting."

## What's already done

These shipped and don't need re-implementation:

1. Mobile `recoverAgent` learned `mode=direct` and prefers it
   (`mobile/src/lib/quic.ts`, `mobile/src/context/DeviceContext.tsx`).
2. Web dashboard already used direct mode
   (`web/lib/agent-client.ts:2494`).
3. Agent `applyRecoveredAuthToken` validates the incoming token
   against Convex BEFORE persisting (`desktop/agent/auth_recover.go`).
   Stops the "Reclaim → 401 → Reclaim → 401 forever" corruption loop
   where a stale mobile push overwrote the agent's working token.
4. Mobile `Recover Yaver Auth` button in DeviceDetailsModal switched
   from `quicClient.ownerClaimDevice` (bootstrap-only, 404 on active
   agents) to the smart `recoverDeviceAuth` dispatcher that probes
   first, then routes to direct / pair / owner-claim / device-code
   based on what the agent actually offers
   (`mobile/src/components/DeviceDetailsModal.tsx`).
5. Mobile auto-guides the user to the per-device Recovery section via
   one-time Alert when the silent auto-recover loop fails
   (`mobile/src/context/DeviceContext.tsx`).
6. The cross-tab "Reclaim banner" was removed
   (`mobile/app/(tabs)/_layout.tsx`, no `DeviceAttentionBanner.tsx`).
7. Headless MCP/browser auth tooling was added for remote recovery and
   wrapped runners (`desktop/agent/mcp_auth_recovery.go`,
   `desktop/agent/mcp_tools.go`, `desktop/agent/httpserver.go`).
8. Alias compatibility was extended across remote-device resolution,
   including `@alias` support for the SSH/device selector path
   (`desktop/agent/main.go`, `desktop/agent/agent_mesh_remote.go`).

End state: from a host-authed yaver instance, recovering a remote
agent's Yaver session is a single HTTP call:

```
POST https://<relay>/d/<deviceId>/auth/recover?__rp=<password>
Authorization: Bearer <host token>
Content-Type: application/json
{"mode":"direct"}
→ HTTP 200 {"ok":true,"mode":"direct"}
agent: lifecycleState yaver-auth-expired → ready-to-connect
```

## Recommendations Codex should implement

### R1. Recovery-session registry on the agent (HIGH)

Adopt the `reauth-recovery-report.md` Phase 1+2+3 design — but ONLY
for `pair`, `device-code`, and bootstrap-secret modes. `direct` does
not need it because the call is synchronous.

Concrete shape:

```go
// desktop/agent/auth_recover_session.go (new file)
type RecoverySession struct {
    ID         string    // 32-byte hex; opaque to clients
    WaitToken  string    // separate 32-byte hex; needed for status reads
    Mode       string    // "pair" | "device-code" | "bootstrap-pair"
    State      string    // started | awaiting_pair_submit |
                         // awaiting_browser_oauth | applying_token |
                         // recovered | failed | expired
    NextAction string    // "submit-pair-code" | "open-browser" | "" 
    PairCode   string    // when Mode==pair, surfaced once on create
    BrowserURL string    // when Mode==device-code, surfaced once on create
    UserCode   string    // when Mode==device-code, surfaced once on create
    AuthMethod string    // "host_token" | "bootstrap_secret"
    CreatedAt  time.Time
    ExpiresAt  time.Time
    LastError  string    // sanitized, never includes token / hostname
}
```

Server endpoints to add:

- `POST /auth/recover` — when Mode != "direct", create a session and
  return `{recovery_id, wait_token, mode, expires_at, next_action,
  pair_code?, browser_url?, user_code?}`. When Mode == "direct", keep
  current sync behavior (return `{ok, mode:"direct"}`).
- `GET /auth/recover/session?id=<recovery_id>&wait_token=<wait_token>`
  — return only the session payload listed above. **Never** return
  hostname, workdir, runner inventory, project list, lifecycleState
  of the broader machine. The endpoint is a *capability status*, not
  a machine oracle.

Internal hooks: pair-submit code path already calls
`applyRecoveredAuthToken` — extend to flip the matching session's
state to `applying_token` then `recovered`. Same for device-code
poll-completion.

### R2. Rework MCP `device_reauth_*` wait semantics (HIGH for headless callers)

The tool family exists already. The missing part is the waiter model.

Owner-context tools that already exist:

- `device_reauth_status(device)` — lifecycle + last-known reachability
  for an owned device. Resolves device by id / name / alias / prefix.
  Calls the standard Convex `/devices/list` + a single `/info` probe
  through the relay. No new state surface.
- `device_reauth_start(device, mode?)` — defaults `mode="direct"` when
  `mode` is omitted. Other values: `pair`, `device-code`.
- `device_reauth_wait(...)` — currently **device-based**, not
  session-based. It polls for the remote machine to look healthy again.

What should change after R1:

- `device_reauth_start(device, mode?)` should keep the current direct
  fast-path for `mode="direct"`.
- For async modes (`pair`, `device-code`, bootstrap-secret recovery),
  it should return the session payload from R1.
- `device_reauth_wait(recovery_id, wait_token, timeout?)` should poll
  `/auth/recover/session`, not broad machine state.
- Add `recovery_target_wait(recovery_id, wait_token, timeout?)` for
  the explicit-target no-local-bearer path.

The verb ergonomic: a Claude Code / Codex caller types
`device_reauth_start({device:"hetzner"})`, gets back `{ok:true,
mode:"direct"}` immediately, done. If the user wants pair instead,
pass `mode:"pair"` and use `device_reauth_wait`.

Files to touch:
- `desktop/agent/auth_recover.go`
- `desktop/agent/httpserver.go`
- `desktop/agent/mcp_auth_recovery.go`
- `desktop/agent/mcp_tools.go`

### R3. Tighten public defaults (MEDIUM)

Per the report's Phase 6:

- New installs default `Config.RequirePrivateRecovery = true`. When
  set, the `classifyRecoveryIngress` helper rejects plain-public-HTTP
  callers entirely (they'd need Tailscale / private relay / Cloudflare
  tunnel).
- Existing installs keep their current setting on upgrade — no surprise
  lockouts.
- `yaver auth allow-public-recovery` flips the bit when the user wants
  the legacy behavior, with a banner on the next `yaver status` print.

Files: `desktop/agent/config.go`, `desktop/agent/auth_recover.go`
(`classifyRecoveryIngress` already exists — extend it to honor the
flag), `desktop/agent/auth_cmd.go` (the new subcommand).

### R4. Polish UI feedback for the auto-recover loop (LOW, MOBILE)

Today the auto-recover loop in `mobile/src/context/DeviceContext.tsx`
is silent during the work window — it only surfaces an Alert if it
*fails*. Ship a non-blocking inline indicator on the active device's
card so the user sees the work happen:

- Add `lifecycleState === "yaver-auth-expired"` → small "Refreshing
  session…" pill (existing badge family in `devices.tsx`).
- When the recover succeeds, the pill flips to "Connected" via the
  existing badge transition. No extra plumbing needed.

The win is psychological: the user sees the system working instead
of guessing whether the silent retry is happening.

### R5. Wrapped runner auth (codex/claude) → same session contract (LOW)

Today `/runner-auth/browser/start` already returns a session object
and MCP wrappers for it already exist. Align its payload shape with R1
so mobile / web / MCP can drive Yaver-level reauth and runner-level
reauth through the same waiter primitive.

Concrete: rename `id` → `recovery_id`, add `wait_token`, add
`next_action`, add `expires_at` if not present. Keep the `runner`
field for differentiation. Then mobile / MCP wait-tools become
generic over both auth families.

File: `desktop/agent/runner_auth.go`.

### R6. Telemetry for recovery outcomes (LOW)

The existing `reportRecoveryEventFn` writes auth events to Convex but
the dashboard doesn't surface a "last recovery attempt" timeline.
Add a small panel on the web Devices view that shows
`{timestamp, mode, outcome, authMethod}` for the last 5 attempts per
device. Helps you (and us) debug "why did this device flip auth-
expired again" without SSH.

File: `web/components/dashboard/DevicesView.tsx` + a new Convex query
in `backend/convex/devices.ts`.

## What NOT to do (from the report)

The report's "session-bound recovery for direct mode" is a regression
in this context. `direct` is sync; adding session polling means a
caller that today sees `{ok:true}` in 1 round-trip would have to:

1. POST `/auth/recover {mode:"direct"}` → get session id
2. GET `/auth/recover/session?id=…` → see `applying_token`
3. GET `/auth/recover/session?id=…` again → see `recovered`

That's 3 round-trips for a 1-round-trip flow. The status endpoint is
valuable for `pair` + `device-code` (which ARE async) and for headless
callers that want a uniform poll loop. Not worth slowing down the hot
path for symmetry alone.

## Suggested implementation order

| # | Item | Why first |
|---|---|---|
| 1 | R1 (recovery session registry) | Foundation for R2 + R5 |
| 2 | R2 (rework MCP waiters onto recovery sessions) | Finishes Claude Code / Codex headless reauth safely |
| 3 | R5 (runner auth session shape unification) | Small, leverages R1 |
| 4 | R4 (mobile inline feedback pill) | Cosmetic but improves perceived reliability |
| 5 | R3 (tighten public defaults) | Independent — new-install behavior change |
| 6 | R6 (recovery telemetry on web) | Nice-to-have observability |

Each item is < 1 day of focused work. Total scope: ~3-5 days.

## Verification target

Codex should reproduce this end-to-end test against any test box at
the end of R1 + R2:

```bash
# 1. From a yaver-CLI machine that's host-authed
yaver mcp call device_reauth_start '{"device":"hetzner","mode":"direct"}'
# expect: {"ok":true,"mode":"direct"} in <500ms

# 2. From the same machine, same target, force pair-mode
yaver mcp call device_reauth_start '{"device":"hetzner","mode":"pair"}'
# expect: {"recovery_id":"...","wait_token":"...","pair_code":"123456",
#          "next_action":"submit-pair-code","mode":"pair"}

yaver mcp call device_reauth_wait '{"recovery_id":"...","wait_token":"...","timeout":60}'
# expect: blocks until the user POSTs to /auth/pair/submit, then
#         returns {"state":"recovered","mode":"pair"}
```

If both paths return cleanly with no `/info` or `/health` poll
involvement, R1+R2 are done.
