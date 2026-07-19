# `develop_for` — the `machine` parameter is gated remotely but executed locally

> **Status:** bug report / remediation options (2026-07-19). **No code changed by
> this doc.** Found during the XREAL rig audit (`docs/architecture/XREAL_SPLIT_RIG.md` §2a).
> Every claim carries a `file:line` against `main` @ 560e76317. Per CLAUDE.md:
> re-grep before acting.

## Summary

`develop_for` accepts a `machine` deviceId. It uses that deviceId to check
whether **the remote machine** has an authenticated runner — and then resolves
hardware capabilities against the **local** host and boots the simulator on
**localhost**.

The verb does not target the machine you name. It gates on it, then ignores it.

**Severity: correctness, user-visible, silent.** There is no error, no warning,
and no field in the response that reveals which host actually ran the work. The
result looks successful.

## Why this is worth fixing independent of any rig work

`develop_for` is the P2 orchestration verb — the "one spoken intent" entry point
that `N2N_DEVELOP_ANYWHERE.md` §P2 designates as the composed door to everything
else. It is the verb an agent reaches for first. Its `machine` parameter is the
only place in the entire `runtime_*` family that promises cross-machine
targeting, so it is also the only place that can silently break that promise.

Notably, **the sibling verbs are honest.** `runtime_targets`, `runtime_create`,
`runtime_list`, `runtime_control`, `runtime_command`, `runtime_frame`, and
`runtime_stop` accept **no** machine/device parameter at all
(`mcp_tools.go:3363-3440`) — they are local-only and say so by omission. The
defect is isolated to `develop_for`.

## What the tool advertises

`mcp_tools.go:3455` — description, first clause:

> "One-verb dev loop: **resolve machine** → gate on authed runner → pick
> mechanism per (framework, surface, platform) → create+boot a remote-runtime
> session on the resolved target → launch app → return sessionId + first frame."

`mcp_tools.go:3464` — the parameter:

> `"machine": "Optional deviceId; empty = local mini."`

An LLM reading this schema will conclude that passing a deviceId runs the loop on
that device. That is the natural reading, and it is wrong. "resolve machine" is
listed as step one; in the implementation it is the *only* step the machine
touches.

## Root cause — the four-line trace

`RunDevelopFor` (`develop_for.go:110`):

| Step | Line | Uses `req.Machine`? |
|---|---|---|
| Runner-auth gate | `develop_for.go:120` → `runnerAuthGateProbe:77` → `mcpRunnerAuthStatus` | ✅ **remote** |
| Host capability probe | `develop_for.go:124` → `currentHostCaps:189` | ❌ **local** |
| Mechanism resolution | `develop_for.go:125` → `ResolveMechanism` (`dev_mechanism.go:54`) | ❌ consumes local caps |
| Session create / launch / frame | `develop_for.go:142,162,173` → `developForRuntimeCall` | ❌ **localhost** |

The two ends of the asymmetry, verbatim:

**Remote** — `runner_auth_cmd.go:993-999`:
```go
func mcpRunnerAuthStatus(deviceID string) interface{} {
	if strings.TrimSpace(deviceID) != "" {
		rows, err := fetchRunnerAuthStatusRowsRemote(strings.TrimSpace(deviceID))
		...
```

**Local** — `remote_runtime_mcp.go:44`:
```go
req, err := http.NewRequest(method, "http://127.0.0.1:18080"+path, reader)
```

`developForRuntimeCall` is bound to `remoteRuntimeHTTPMCP` at `develop_for.go:67`.
`currentHostCaps` (`develop_for.go:189-198`) shells to local `adb`/`emulator` via
`exec.LookPath` and reads local Apple runtimes via
`testkit.InstalledRuntimeFamilies`.

## Failure modes

Ordered by how badly they mislead.

1. **Silent wrong-host success.** Caller is on a Mac, names another Mac. Gate
   passes. Everything resolves and boots — on the caller's machine. Response
   carries `sessionId`, `mechanism`, `targetId`, `firstFrameJpegBase64`, and a
   `note` reading `mechanism=… target=… surface=…` (`develop_for.go:181`).
   **Nothing in the response names a host.** The operator believes they drove the
   remote box.

2. **Confusing failure on a heterogeneous pair — the XREAL rig case.** Caller is
   an Android Beam Pro, names the Mac mini, asks for `surface: phone,
   platform: ios`. The gate passes (mini has authed runners). Then
   `currentHostCaps` inspects *the phone*, finds no Apple runtimes, and
   `ResolveMechanism` errors or picks an Android target. The error text describes
   the phone's toolchain while the operator is looking at a Mac mini. This is the
   shape that cost time during the rig audit.

3. **Inverted gate.** Local machine is fully capable but has no authed runner;
   named remote does. The gate passes on the remote's credentials and the work
   runs locally — i.e. the gate authorized a machine that did not perform the
   work. The gate's own error text hardcodes the assumption
   (`develop_for.go:105`): `"no authed runner on %s — run \`yaver runner auth\` on %s"`.

## Why no test caught it

The test suite **structurally cannot** catch this. `develop_for_test.go:27-29`
substitutes all three seams at once:

```go
developForRunnerAuthGate = gate
developForRuntimeCall    = rt
developForFrameCall      = frame
```

- The gate stub discards its argument — `func(_ string) error` (`:108`). No test
  passes a `Machine` value and asserts on it.
- The runtime-call stub replaces the very function that hardwires
  `127.0.0.1:18080`, so no test ever observes the target URL.

The seams exist for a good reason — `develop_for.go:64-72` explains they let a
pure-Go test exercise the verb without a live agent or booted sim. But by
replacing transport *and* gate together, the tests validate orchestration order
while making host routing structurally unobservable. **Any fix should add a test
that asserts the target host, not just the call sequence.**

## Remediation options

### Option 1 — Reject a non-local `machine` loudly

Smallest correct change. In `RunDevelopFor`, after the gate: if `req.Machine` is
non-empty and does not resolve to this device, return an error naming the
limitation. Update the tool description and the `machine` param to say local-only.

- **Pro:** a few lines; removes the false promise immediately; no new transport;
  cannot regress.
- **Con:** removes a capability the docs advertise; punts the real feature.
- **Fits when:** the priority is that nothing lies, and Shape A (single-agent) is
  the supported topology.

### Option 2 — Honor `machine` end-to-end

Route the whole verb at the named device. Two sub-parts:

**(a) Transport.** Replace the `developForRuntimeCall` binding with a
device-aware variant. The primitives already exist and are mature:
- `remoteAgentJSONForDevice(ctx, deviceID, method, path, body, &resp)` —
  `agent_mesh_remote.go:1002`, with candidate scoring (`:44-60,126`), liveness
  probing (`:711`), health persistence (`:90-125`), ~60 existing call sites.
- Or the generic peer proxy `/peer/<deviceId>/<path>` — `peer_proxy_http.go`,
  registered `httpserver.go:1142`, with relay traversal and auth re-signing.

The idiom is already established repo-wide (`code_control.go:604,869,1004,1726`):
```go
if deviceID == "" { /* local */ } else {
    err = remoteAgentJSONForDevice(ctx, deviceID, "POST", "/tasks", body, &resp)
}
```

**(b) Capabilities — cheaper than it looks.** `currentHostCaps` must describe the
*target*. Two existing authed routes already serve this remotely:

- `GET /remote-runtime/capabilities?framework=&workDir=` —
  `httpserver.go:1019`. **This is the better source:** it returns each target's
  id, surface badge, and enabled/disabled state *computed on that host*. It is
  already what `runtime_targets` calls (`httpserver.go:13554`). Resolving the
  mechanism from the target's own enabled-target list is strictly more accurate
  than reconstructing `HostCaps` remotely — and it sidesteps the fact that
  `MachineCapabilities` is coarser than `HostCaps` (`SupportsIOS` bool vs.
  `AppleRuntimeFamilies` list).
- `GET /infra/summary` — `httpserver.go:442`, returns
  `Machine.Capabilities = detectMachineCapabilities(workDir)`
  (`infra_http.go:76`) with real probing (`console_machines.go:207-264`:
  `toolLooksInstalled("xcrun")`, `java`/`gradle`/`adb`, per-runner `LookPath`).
  Useful as a coarse fallback.

- **Pro:** delivers what the schema already promises; unblocks the split-brain
  rig topology; reuses transport with no new protocol.
- **Con:** widens the auth blast radius (below); more surface to test.
- **Fits when:** cross-machine `develop_for` is genuinely wanted.

### Recommendation

**Option 1 now, Option 2 when the split topology is actually wanted.** The lie is
the urgent part and is independent of whether anyone wants the feature; Option 1
removes it in a few lines and cannot regress. Option 2 should not be rushed
because of the auth question below, which deserves a decision rather than an
implementation detail.

Either way: **add a test that asserts the target host.** Without it the next
change re-introduces this silently, exactly as the current seams allowed.

## The auth question Option 2 must answer first

Do not settle this inside the implementation.

`develop_for` boots runtimes and launches apps. Routing it to another machine
means one box causing code execution on another. The adjacent surface is
owner-only and unsandboxed **by explicit design** —
`runner_agent_session_http.go:13-16`:

> "a guest tier here would mean arbitrary code execution on someone else's box."

There is already a precedent in-tree, and it is deliberate.
`forwardSessionRequest` (`remote_runtime_dispatch.go:226-232`) drops the caller's
`Authorization` and reaches the builder with a pairing token stored at
`~/.yaver/builders.json` (mode 0600):

> "We deliberately do NOT pass the user's Authorization from the inbound request
> — the builder has its own auth (the token we stored at pairing time). This
> keeps cross-host auth boundaries explicit instead of relying on bearer-token
> reuse."

**Follow that precedent rather than re-litigating it.** Note the tension to
resolve: `remoteAgentJSONForDevice` and the peer proxy use same-user session
auth, while builder dispatch uses explicit pairing tokens. Option 2 has to pick
one model deliberately — the two seams have not converged, and that
non-convergence is itself tracked in `docs/architecture/XREAL_SPLIT_RIG.md` §2b.

## Adjacent nit

`httpserver.go:13701` discards the unmarshal error:

```go
var req DevelopForRequest
json.Unmarshal(call.Arguments, &req)
```

Malformed arguments silently produce a zero-value request, which then fails
downstream on `framework required` / `surface required` (`develop_for.go:115,118`)
— a misleading message for what is actually a decode failure. This pattern
repeats across neighbouring MCP cases (`:13710`, `:13714`), so fix it as a
consistent sweep or leave it; it is not part of this bug.

## Reference — files touched by any fix

| File | Role |
|---|---|
| `desktop/agent/develop_for.go` | `RunDevelopFor:110`, seams `:62-72`, `currentHostCaps:189` |
| `desktop/agent/remote_runtime_mcp.go` | `:44` hardwired `127.0.0.1:18080` |
| `desktop/agent/dev_mechanism.go` | `ResolveMechanism:54` consumes `HostCaps` |
| `desktop/agent/mcp_tools.go` | `:3455` description, `:3464` `machine` param |
| `desktop/agent/httpserver.go` | `:13699` dispatch; `:1019` capabilities route; `:442` infra route |
| `desktop/agent/develop_for_test.go` | `:27-29` seam substitution, `:108` gate stub |
| `desktop/agent/agent_mesh_remote.go` | `remoteAgentJSONForDevice:1002` — transport for Option 2 |
| `desktop/agent/peer_proxy_http.go` | generic `/peer/<deviceId>/` proxy |
| `desktop/agent/remote_runtime_dispatch.go` | `:226-232` cross-host auth precedent |
