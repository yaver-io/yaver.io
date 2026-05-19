# #9 — Web one-click runner OAuth for a managed box (turnkey plan)

_Grounded in the actual code. Deliberately NOT rushed at the tail of a
maximal-context session: this is auth + a Docker-agent rebuild._

## What already exists (reuse, don't reinvent)

- `desktop/agent/runner_auth_cmd.go` — status (`collectRunnerAuthStatusRows`,
  `mcpRunnerAuthStatus`), set (`mcpRunnerAuthSet`).
- `desktop/agent/runner_auth_browser_http.go` — browser/device-code
  login flow HTTP handlers + `handleRunnerAuthCredentialsImport`
  (copy a signed-in machine's subscription token to a remote box —
  the PREFERRED path per feedback_yaver_single_user_wrapper /
  feedback_no_api_keys_subscription_only).
- `desktop/agent/runner_auth_http.go` — `handleRunnerAuthStatus`.
- MCP tools: `runner_auth_browser_start|status|submit_code|cancel`,
  `runner_auth_credentials_import`, `runner_auth_status` (all accept
  `device_id` to target a remote owned box).
- `ops_git.go` `git_connect` — the EXACT pattern the web already
  drives via `agentClient.callOps(verb, {…deviceId})` and renders
  (`ManagedMachineActions`: user_code + verification_uri + poll).
- Convex: `cloudMachines.runnersAuthorized` + `setPhase` flip it;
  owner route `POST /billing/yaver-cloud/runners-authorized`
  (shipped) flips the UI Unauthorized→ready.

## The gap

Runner-auth is exposed as **MCP + HTTP handlers**, NOT as an **ops
verb**, so the web's `callOps` can't drive it the way it drives
`git_connect`. That ops wrapper is the only missing transport.

## Turnkey steps

1. **`desktop/agent/ops_runner_auth.go`** (new) — `registerOpsVerb`
   `runner_auth` with `{op: status|browser_start|browser_status|
   submit_code|credentials_import, runner, session_id, code,
   credentials_json}`, each delegating to the EXISTING functions the
   MCP tools call (pure transport wrapper — no new auth logic).
   Mirror `ops_git.go` structure exactly (Streaming:false,
   AllowGuest:false, deviceId routing handled by the ops layer).
2. **Image**: `Dockerfile.yaver-cloud` already fetches the latest
   agent tarball, so a new `cli/v*` release ships the verb to boxes
   automatically — no Dockerfile change, just release the agent.
3. **Web** `ManagedCloudPanel`: replace the disabled "Authorize
   runners" button with the `git_connect`-style flow against the
   box `deviceId`: callOps `runner_auth {op:"browser_start",
   runner:"claude"}` → show `verification_uri`/`user_code` → poll
   `{op:"browser_status"}` → on `done` POST
   `/billing/yaver-cloud/runners-authorized {machineId}` → UI flips
   ready. Repeat per runner (claude/codex/opencode) or a "Authorize
   all" that loops. Offer "Import from this computer" →
   `{op:"credentials_import"}` when the user has a local signed-in
   agent (the friction-free subscription-token copy).
4. **Status truth**: a small reconcile/heartbeat can call
   `runner_auth {op:"status", deviceId}` and POST
   runners-authorized so the flag self-corrects (token expiry, manual
   CLI auth) without the user re-clicking.
5. **Tests**: ops verb unit test (delegation + bad-payload), mirror
   `ops_git` test; web spec extends `managed-cloud-delete.spec.ts`.
6. **#10 mobile** then mirrors step 3's contract — same callOps verb,
   same Convex flip — so it's a thin RN screen once #9 lands.

## Why parked here

`runner_auth` is a mature auth subsystem; the only correct change is a
thin ops wrapper + web flow + an agent release. Doing it precisely in
a fresh context (not at max-context tail) is the difference between
working subscription-OAuth and a subtly broken one that double-bills
or silently fails. Everything money-critical (buy/provision/delete/
paid-but-no-box recovery/visibility/progress) is already shipped.
