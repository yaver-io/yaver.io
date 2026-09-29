# Remoteless AI + Yaver repair channels (SSH wrapper + JSON-RPC "HTTPS-SSH")

Status: **P0 committed + pushed (ecfc52c62, 2026-08-21)** — interim `remoteless` runner +
aiFix default. **Handoff to next session: `docs/handoffs/remoteless-ai-2026-08-21.md`**
(repo state, concurrent-session WIP, what's left for P1–P4). See AGENTS.md: this doc is
context, the code is the source of truth — grep the symbols before trusting them.

Temporary rollout gate (2026-08-25): every executable remoteless lane is
owner-preview-only. Mobile and tvOS consume the backend-computed `user.isOwner`
bit; the Go agent stores that same `/auth/validate` entitlement and refuses,
hides, and declines to probe the `remoteless` runner otherwise. Cached
`local-only` state is migrated back to `remote-preferred` for non-owners. The
owner identity remains runtime configuration (`CLOUD_PREVIEW_OWNER_EMAILS`),
never a literal in this public repository.

## Goal

Make any owned remote box **usable and fixable from every surface** (electron desktop, mobile,
tvOS, visionOS/AR, watchOS, Wear OS, Android TV, car, web) with **no dependency on a
coding-runner CLI** (claude / codex / opencode) **or a stored key on the box** where we can
avoid it. Three lanes:

- **Lane A — Remoteless AI runner**: hosted-model lane (DeepSeek default — already the shipped
  model default at `desktop/agent/httpserver.go:3323-3329`) for tasks, chat, deep-audit,
  fix-with-AI, connectivity/OAuth diagnosis, and vibing edit loops.
- **Lane B — Client-local SSH**: surfaces hold per-box SSH config in their own secure storage and
  drive a thin wrapper over the agent's existing SSH machinery to fix the box.
- **Lane C — Yaver "HTTPS-SSH"**: a JSON-RPC 2.0 repair channel over the existing
  relay-authenticated HTTP/WS — for surfaces where SSH is impossible (tvOS, AR/VR) and for
  unauthenticated boxes (pair-window-proven ownership).

## Surface-agnostic infrastructure principle (LOAD-BEARING)

The agent HTTP surface is the **single contract**. Every client surface — mobile, tvOS,
visionOS/AR, car, watchOS, Wear OS, Android TV, desktop GUI, web — talks to a box through the
agent's authenticated routes (`/tasks`, `/ops`, `/repair/rpc` over relay `/d/<deviceId>/…`).
Nothing about remoteless AI, client SSH, or the JSON-RPC repair channel may live client-side in a
way that a different surface cannot reuse. Per-surface work is **thin UI + local secure storage
only**; the verbs and the policy live on the agent. A lane that works from the web dashboard must
work from a watch, by construction, because they call the same endpoints.

## Locked decisions

| Fork | Decision |
|---|---|
| Model call location | On-box in-process first; caller-executed (key never on box) after the JSON-RPC tool surface lands |
| Runner implementation | Interim = opencode BYOK → DeepSeek (proven by `e2e/tests/deepseek-opencode-plumbing.spec.ts`); then the Go in-process loop |
| Repair auth gate | Dedicated owner-minted `repair`-scope token; ordinary companion tokens stay locked out of exec/vault/terminal |
| Tool surface v1 | Sandboxed bash (`ValidateCommand`, `sandbox.go`) + fs (read/write/list/grep) + git via bash |
| Repair protocol | JSON-RPC 2.0 — standard envelopes, id correlation, batch, structured error `data:{reasonCode, routeToFix}` |

## Lane A — Remoteless AI runner

**A0. Interim — register `remoteless` as a first-class runner id (P0)**
- `builtinRunners["remoteless"]` in `desktop/agent/tasks.go` → `opencode run
  --dangerously-skip-permissions {prompt}` with `Model: "deepseek/deepseek-flash"`, so the
  existing model splice in `startProcess` (`tasks.go:2943-2971`) injects `--model
  deepseek/deepseek-flash` after `run`.
- The id is a **stable lane contract**: the backend currently resolves to opencode+deepseek, and
  later swaps to an in-process Go loop (A1) without touching callers. `LoadRunnersFromBackend`
  (`tasks.go:376-381`) keeps the local builtin because the id is in `supportedRunnerIDs`.
- Readiness: `DetectRunnerRuntimeStatus` case `remoteless` → `detectRemotelessStatus(workDir)`:
  opencode binary present **and** a DeepSeek credential (env/vault `DEEPSEEK_API_KEY` or
  `opencode.json` `provider.deepseek` with key/baseURL).
- `runnerHasAuthModel("remoteless") = true` so preflight runs; `runnerReauthCommand("remoteless")`
  names the missing-credential CTA.

**A1. In-process Go loop (follow-up build)**
- Generalize `RunGLMLoop` (`desktop/agent/glm_loop.go:115-229`) → `remoteless_loop.go`:
  OpenAI-compatible JSON tool-call loop (DeepSeek default; GLM/ZAI/OpenRouter/Anthropic), one
  sandboxed tool per round-trip.
- `RunnerConfig` (`tasks.go:88-112`) gains `Kind: "binary" | "remoteless"`; `startProcess`
  (`tasks.go:2746`) gets an in-process branch emitting the **same stream-json/raw SSE lanes +
  `command_*` events** so LiveConsoleSection and the vibing render contract work unchanged.
- Tools v1: `bash` (via `ValidateCommand` sandbox `sandbox.go:382-455`), `read_file` /
  `write_file` / `list_files` / `grep` (bounded), `git` via bash. Ask-mode frame
  (`askModePreamble` `task_context.go:303-318`) support → **deep audit works on a runnerless box**.
- Preflight: key present + `/v1/models` probe (pattern: `runnerProviderPreflight`
  `provider_keys.go:219-278`).

**A2. Caller-executed lane (later)** — the phone/web/tvOS runs the LLM loop (reuse
`mobile/src/lib/codingAgent/runner.ts` / `web/lib/coding-runtime.ts`), and each tool call is
forwarded to the box as a JSON-RPC method (Lane C). Zero key on the box.

## Lane B — Fix-with-AI across connectivity, OAuth, deep-audit, vibing

### Constraint from the tvOS QR-auth handoff (2026-08-21)

Read `docs/handoffs/tvos-qr-auth-audit-2026-08-21.md` before building any tvOS
"Fix with AI" surface. Its rules are load-bearing for this lane:

- A TV-scoped companion token is denied `POST /tasks` on deployed agents by
  `auth.session.scope_denied` because the allowlist fix (`6a70b7e3f`,
  `tvTaskMutationAllowed`, `desktop/agent/httpserver.go`) is **unreleased** —
  the fix is a new agent release, then a box update, not a client bug.
- **Verdict routing, not prose:** a `auth.session.scope_denied` verdict must
  render an "Update agent" card and must NEVER render Retry/Fix-with-AI (a fix
  route that cannot run is a wall). `TVRemoteRuntimeController.errorCode` +
  `RemoteRuntimeWebRTCView.runtimeFailurePanel` already do this — the remoteless
  fix lanes must keep the same discipline.
- The remoteless runner's `POST /tasks` under a TV-scoped token is subject to
  the same gate. Lane D's dedicated `repair`-scope token is the intended path
  for TV-initiated repair, and it must exist independently of the released
  allowlist state.
- Companion-scope parity tests must validate **method + path** (a path check
  that accepts GET when only POST is allowed can miss drift).

**B1. Fix-with-AI default → remoteless (P0 starts here)**
- `aiFixRoute` (`capability_gap.go:532-550`) defaults `runner` to `remoteless` when the lane is
  available. Keep the AGENTS.md rule: **deterministic fixer first, LLM only when none exists** —
  remoteless is just the cheaper default coding agent.
- Extend `AIFix` beyond compile-failure (`compileFailureGap` `capability_gap.go:572-596`) to:
  `reload.dev_server_unavailable`, vibe-capture-stuck, `browser_window.chrome_missing`,
  `capability.insufficient_disk` (→ reclaim), `connectivity.relay.pin_stale`, runner-auth re-login.

**B2. New diagnose→fix verb**
- `POST /repair/ai-fix` (and an `/ops` verb for native surfaces): input = structured diagnosis
  (`machine_doctor` findings `ops_machine_doctor.go`, `remote_repair` plan `remote_box_repair_plan.go`,
  reason codes `reason_codes.go`, `/dev/events` replay, `GET /streams/<name>` tail) → a remoteless
  task that either runs a **bounded allowlisted repair** (`remoteRepairCommand`-style table
  `ops_remote_repair.go:106-137`) or returns a **named route-to-fix** (`GapFix` triple).
- **Auto-vs-confirm rule**: allowlisted deterministic repairs auto-run; anything that signs
  auth/OAuth/pushes/deploys → human confirm via `yaver_ask_user`. Preserves the existing refusal
  to auto-guess OAuth signing (`remote_box_repair_plan.go:130-144`).

**B3. OAuth orchestration (remoteless-driven device-code)**
- Remoteless agent executes headless device-code flows for **codex, git, opencode**
  (`codex login --device-auth`, `git_oauth_device.go`) — renders URL+code to the user's surface,
  polls, persists to vault. **claude** stays an honest route: terminal login /
  `runner_auth_credentials_import` / submit-callback / submit-code (hosted-callback flow has no
  device code — impossible ⇒ say so).

**B4. Vibing/render failure path**
- `runtime_render_requested` failure and vibe-preview errors (`PreviewTargetUnreachableError`
  `vibe_preview_takeover.go:98`) gain a remoteless fix route: inspect dev-server/stream logs →
  deterministic restart (dev server, capture loop) or a remoteless **code-fix task on the box's
  project**. Honours the render/reload contract: render only on `completed`/`review`, coalesce
  in-flight, keep-last-good.

## Lane C — Client-local SSH

- **Per-box SSH config in each surface's secure store**: mobile `LOCAL_KEYS`
  (`yaver.secure.ssh_<boxId>` JSON `{host,user,port,key}`, strict store `secure-storage.ts`),
  tvOS/visionOS Keychain (copy `BoxlessDeepSeekKeyStore` pattern), web localStorage (with warning;
  prefer the agent vault `ssh-key` category), electron Chromium/vault. **Never in Convex.**
- **Agent side**: `/ops machine_repair | remote_repair | device_reauth_*` accept an optional
  **in-memory `sshOverride {host,user,port,identityKey}`** — fed into `sshArgsFor` /
  `sshRunMulti` / `ssh_bootstrap` (`ssh_targets.go:171-194`, `remote.go:787-816`,
  `ssh_bootstrap.go`), discarded after the op.
- **New ops verb `ssh`** (thin wrapper of real ssh): `{deviceId, sshOverride?, command|verb}` →
  `ValidateCommand`-sandboxed unless it's a curated repair script; stream via `/exec`-style SSE.
- **Key install once**: `/auth/ssh/authorized-keys` (`auth_ssh_http.go:69`) + `yaver-managed`
  forced-command tagging (`ssh_managed_keys.go:27-57`) so the client's key is a caged key.

## Lane D — Yaver "HTTPS-SSH" = JSON-RPC 2.0 repair channel

- **Protocol**: JSON-RPC 2.0. `POST /repair/rpc` (single request/response) + `WS /ws/repair`
  (long-running ops with JSON-RPC **notifications** for progress). Rides the existing relay
  `/d/<deviceId>/<path>` (`relay/server.go:1871-1975`) + agent bearer. Standard envelope, id
  correlation, batch, error `{code, message, data:{yaverReasonCode, routeToFix}}`.
- **Method allowlist** (seed = `ssh_session_cmd.go:46-74` whitelist + `remoteRepairCommand` +
  vibing/render verbs): `health`, `info`, `list-projects`, `doctor-transport`, `repair-relay`,
  `restart-agent` (self-repair guard `ops_machine_repair.go:68-71`), `restart-dev-server`,
  `restart-vibe-capture`, `reload-bundle`, `kill-exec`, `reclaim-disk` (safe allowlist only),
  `run-task`/`continue-task`, `file-read`, `ai-fix`. Unknown method rejected.
- **Auth gate**: dedicated `repair` scope on the SDK-token mint shape (`auth.go:402-439`):
  `{scopes:["repair"], targetDeviceId, expiresInMs}`, owner-minted. Agent-side scope→route
  enforcement via `scopePathPrefixes` (`httpserver.go:1775-1795`) + `requestAllowedByScopes`.
  `companionSessionAllowed` walls (`httpserver.go:1892-1956`) stay closed.
- **Unauthenticated-box path**: extend the pair window — after `/auth/pair/submit` proves
  **same-Convex-user ownership** (`auth_pair.go:61-89`, 10-min one-shot), the box issues a
  short-lived `repair` token for the allowlist only. Requires the box to remain **relay-registered**;
  a full-bootstrap box with stale relay creds is honestly "unreachable — use LAN/SSH"
  (impossible ⇒ say so, matching `auth_recover_ssh.go:5-8`).
- **Audit ledger**: every RPC call logged `{surface, tokenId, method, params, ts}` →
  `GET /repair/audit`, replayable.

## Surfaces (thin UI only — verbs live on the agent)

- **electron** (embedded agent): `remoteless` runner free; "Fix via SSH" / "Fix with AI" in the
  device view; SSH config in secure storage.
- **mobile**: LOCAL_KEYS DeepSeek already present; per-task key forward (A2-later); SSH store;
  vibing-fix entry; JSON-RPC repair client.
- **tvOS / visionOS**: upgrade `BoxlessDeepSeekClient` from chat-only to a remoteless driver over
  `/repair/rpc` + `/ops`; "Fix this box" flow (doctor → findings → AI-fix or repair-RPC →
  streamed → verify). No SSH — this is the JSON-RPC lane's home.
- **watchOS / Wear OS / Android TV / car**: surface repair + AI-fix through the existing `/ops`
  proxy (already cross-surface); add relay to the watchOS standalone client (`SessionClient.swift`
  is LAN-only today).
- **web**: repair console + "Fix with AI" on the device view; wire `runBrowserPrompt` as the A2 lane later.

## Security invariants (non-negotiable)

Relay stays pass-through + same-owner; **no new trust in the relay/tier**. Keys never in Convex,
never logged; forwarded keys held in memory only. Scope enforcement agent-side (per
`launch_hardening_test.go:53-96`). Pair-proof is the only gate for unauthenticated boxes. Sandbox
for arbitrary input, allowlist for curated repairs. Audit every mutation. No success-without-operation.
Cross-tenant reach is a security bug — a hostile tenant's `/repair/rpc` must fail the relay ladder
before it touches the box.

## Testing & docs (each phase: headless first, closed loop second, prove the guard by breaking it)

- **Unit/headless**: remoteless loop with mocked endpoint; JSON-RPC envelope + error mapping;
  `ValidateCommand` reuse; pair→repair-token issuance; **allowlist parity test** (Go + mobile +
  tvOS copies, like `runtime_render_target_parity_test.go`); hardening test that a wider-scope
  token still can't open `/exec` outside the repair allowlist.
- **Closed loop**: break a dev server on a real box → vibing remoteless fix; break auth →
  pair-proven repair from tvOS; kill the vibe capture loop → JSON-RPC restart; break each guard
  and watch the test fail.
- **Docs**: this file, `CLAUDE.md`/`AGENTS.md`, `FAILURE_PLUMBING_ARCHITECTURE.md`, `REMOTE_WORKER.md`.

## Phasing

- **P0** (in progress) — interim `remoteless` runner (opencode-BYOK wiring, readiness, docs) +
  flip `aiFixRoute` default.
- **P1** — fix-with-AI / connectivity / OAuth / vibing wiring + device-code orchestration.
- **P2** — client SSH lane (`sshOverride`, `ssh` verb, per-surface secure stores).
- **P3** — JSON-RPC repair channel + `repair`-scope token + pair-proven unauthenticated path + audit.
- **P4** — in-process Go loop (A1), caller-executed lane (A2), surface parity, tests, docs.

Each phase ends with the guard broken and re-proven.
