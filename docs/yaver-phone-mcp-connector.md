# Yaver as a first-class MCP connector for the phone (Claude app / Codex / Claude Code)

Status: design + first implementation slice landed (2026-07-11, uncommitted).
Grounded in a read of the actual agent code, not the older docs — verify
constants against source before extending (see CLAUDE.md "Read This First").

**Implemented in this pass** (see §9):
- Elevated owner-connector scope — `oauth_mcp.go`
  (`mcpOwnerConnectorAllowedTools`, `mcpElevatedConnectorScope`,
  `stripConnectorElevation`, `connectorScopeElevated`) + the "Full access"
  consent checkbox in `oauth_provider.go`. Elevation is a human decision on
  the consent form, never a client-requestable scope. Tests:
  `oauth_mcp_elevation_test.go`.
- `runner_turn` + `runner_sessions` ops verbs — `ops_runner_turn.go`, sharing
  the tmux hazard core now extracted to `executeRunnerSessionTurn`
  (`runner_session_turn.go`). Surface-aware spoken summary reuses the watch
  summarizer. Tests: `ops_runner_turn_test.go`.
- `yaver mcp setup phone` — `mcp-setup.go` prints the relay `/d/<id>/mcp`
  connector URL + QR.
- `machine_wake` ops verb — `machine_lifecycle.go`. Wakes a scaled-to-zero BYO
  box BY NAME (resolves snapshot/plan/region from the `byoMachines` row, so a
  voice surface needs only the name), idempotent, confirm-gated. Decision logic
  factored to `machineWakeDecision` (pure, unit-tested: `machine_wake_test.go`).
  Note `machine_create/up/down/rm` already existed as ops verbs.

**Still open**: the mobile-app UI to show/revoke elevated connectors;
`runner_start` + `ssh_run` ops verbs (see §9 for why they're deferred);
connector-token revocation beyond the 1-hour access-token TTL; the live
connector handshake test (needs a running box + a phone).

## 1. The goal — lower-friction, not new muscle

You already have a mature Yaver mobile app with voice/car/watch loops, a
tmux-persisted remote-runner PTY, and ~740 MCP tools. This is **not** about
building more surface. It's about a **lower-friction front door**:

> Open the Claude app (or Codex, or Claude Code) you already use, add Yaver
> as a connector once, then just *talk*. Claude picks Yaver's tools to SSH,
> run ops, and spawn/attach a coding runner (`claude`/`codex`/`opencode`/`glm`)
> on your Hetzner or self-hosted box — and reads results back as one spoken
> sentence when you're driving.

Topology (decided): **phone is the thin head, the remote box is the muscle.**
Claude holds the conversation; a Yaver MCP tool spawns/attaches the *real*
runner on the box. The phone never does the coding over the wire.

Output shape (decided): **surface-aware.** Car/watch → one TTS-ready sentence
with full detail on request; screen → full output. One tool, two renderings.

## 2. What already exists (so we don't rebuild it)

| Capability | Where | State |
|---|---|---|
| Remote OAuth MCP endpoint `/mcp` (streamable HTTP) | `httpserver.go:1445` (`authMCP(handleMCP)`), `httpserver.go:5393` | **Built.** RFC 9728 challenge + `.well-known/oauth-protected-resource/mcp` (`httpserver.go:606`), RFC 8707 audience binding, per-user OAuth 2.1 (MCP auth spec 2025-11-25) |
| "Add Yaver as a connector in Claude/ChatGPT/Codex" is the *stated purpose* | `oauth_mcp.go:3-5` | **Built** — but scope-locked, see §3 |
| Relay reachability `/d/{deviceId}/mcp` → agent `/mcp` | `relay/server.go:715,1018`; `?token=`→Bearer promotion `:1069` | **Built.** Password via `X-Relay-Password`/`?__rp=` |
| `ops(machine, verb, payload)` grand-tool + `ops_plan` + `ops_verbs` | `ops.go:36,50,181`; tools `mcp_tools.go:3026-3142` | **Built.** ~290 verbs, uniform result, remote mesh routing |
| Remote mesh routing (`machine != "local"`) over LAN→Tailscale→direct→CF-tunnel→relay | `mcp_remote_proxy.go:100-133`; `agent_mesh_remote.go:47,232` | **Built.** Forwards caller's real user bearer; same-user enforced |
| Runner PTY wrap `yaver <runner> --machine`, WS `/ws/runner`, tmux persist | `runner_pty_cmd.go:572`, `runner_pty.go:39,102-145` | **Built** (min agent `1.99.274`). Owner-only |
| **Synchronous** voice-shaped session driver `POST /runner/session/turn` (types prompt, waits, returns pane-tail + `awaitingChoice`+`options`) | `runner_session_turn.go:40-75`; `httpserver.go:1195` | **Built.** Handles menu/digit-confirm/chained-menu hazards |
| Deterministic no-code one-sentence summarizer + code refusal + 200-char clamp | `carVoiceCoding.ts:130-186`; server budget `voice_dispatch.go:147-160`; `watch_risk.go` | **Built** |
| Surface hint enum `car,watch,tv,mobile,mcp,cli` on some tools; `say` TTS tool; `gateway_intent` as the "single entry point a voice/watch surface uses" | `mcp_tools.go:2578,1578,1677` | **Partial** — not plumbed into ops result shaping |
| Runners + subscription auth (P2P credential mirror, never Convex) | `tasks.go:267`; `runner_auth*.go`; `provider_keys.go:155-165` (GLM z.ai) | **Built** |
| BYO Hetzner scale-to-zero (`machine down` snapshots **then** deletes; aborts delete on snapshot failure) | `machine_lifecycle.go:12-13,182-190`; `machine_cmd.go:25-36` | **Built** — matches metered-never-monthly rule |

**Bottom line:** the connector door, the transport, the remote muscle, the
runner, the summarizer, and the scale-to-zero box all already ship. Four small
things stand between that and your car.

## 3. The four gaps (this is the whole project)

### Gap A — the connector scope is 22 toys. *This is the crux.*

`mcpConnectorAllowedTools()` (`oauth_mcp.go:35-42`) allows only pure-computation
tools: `calculate, translate, world_clock, currency_exchange, convert_units,
crypto_price, stock_price, weather, news, qr_code, uuid, hash, base64, color,
password_gen, lorem_ipsum, epoch, regex_test, jwt_decode, figlet, tldr, geocode`.

The comment is explicit and correct as a *default*: a connector token lives in
a third-party cloud (Anthropic's servers when you use the Claude app), so
default-deny to tools with "NO access to the host's files, shell, devices,
cloud, or state" is the right posture for a stranger-safe default.

But your use case is the **owner** wanting **full power over their own box from
their own phone.** So we don't *widen the default* — we add a second,
**explicitly-elevated owner-connector scope**:

- Keep `mcpConnectorAllowedTools()` as the default for any freshly-added
  connector.
- Add an **owner opt-in elevation**: when the signed-in user (the owner)
  authorizes a connector, they can promote it to an `owner-connector` class
  whose allowlist is the small **voice-shaped set** in §4 — *not* all 740 tools.
- Elevation is per-connector, revocable, and shown in the app ("Claude (phone)
  — elevated · revoke"). Store the class on the OAuth grant, stamp a different
  `X-Yaver-AllowedTools` for elevated tokens.
- Even elevated, keep the Layer-4 secret block (`vault_*`, `sdk_token_*`,
  `env_*`, `deploy_cred_*` never cross machines — `mcp_remote_proxy.go:42-74`)
  and route every **write/ACT** verb through the existing confirm gate (§6).

This is the one genuinely security-sensitive decision. It should be a reviewed,
explicit change to `oauth_mcp.go` + the OAuth grant model, not a config toggle.

### Gap B — 740 tools is unusable for a general agent

Even with elevation, do **not** hand the Claude app 740 tools. A general model
picks badly from that many, and it blows the context budget. The elevated
connector should see **≤ ~10 high-level tools** (§4). The `ops` grand-tool
already exists for exactly this reason — lean on it.

### Gap C — no single "run a runner on my box and speak the result" verb

`/runner/session/turn` is the right primitive (synchronous, pane-tail,
`awaitingChoice`) but it's an HTTP route, not an `ops` verb, so a connector
can't reach it. Wrap it as one verb (§4, `runner_turn`). Same for
"start/attach a session" (`runner_start`) and "list sessions"
(`runner_sessions`).

### Gap D — surface-awareness + auto-wake not plumbed

- No ops verb accepts `surface: voice|car|watch` to auto-summarize its result.
  The summarizer exists (`carVoiceCoding.ts`, `watch_risk.go`) but lives in the
  mobile/voice layer, not in `ops` result shaping. Plumb a `surface` field into
  `OpsResult` rendering.
- Auto-wake: the deep-dive found `machine up` (snapshot-restore) exists
  (`machine_lifecycle.go`) but is **not** wired into the `--machine` runner
  path — the docs claim `yaver codex --machine=X` auto-wakes a stopped box, but
  the PTY preflight (`runner_pty_cmd.go:79`) only SSH-restarts the *agent*, it
  doesn't restore a scaled-to-zero box. For the car flow ("wake my box and run
  codex") this must be wired: `runner_turn`/`runner_start` should call
  `hetznerStartServer` when the target `byoMachines` row is `stopped`, then
  wait for `/health`.

## 4. The connector tool surface (small, voice-shaped)

What the *elevated* Claude-app connector should see — nothing more:

1. **`ops`** — the grand-tool. `ops(machine, verb, payload, surface?)`. Covers
   the ~290 read/ops verbs (status, logs, git, deploy, project context…) with
   one tool. Add the optional `surface` field.
2. **`ops_verbs`** — lets Claude discover verbs on demand instead of us
   pre-listing 290 in the schema.
3. **`ops_plan`** — dry-run: "where would this run, is it allowed" — good for
   Claude to reason before an ACT.
4. **`runner_turn`** — *new.* Wraps `/runner/session/turn`. Payload:
   `{machine, runner, session?, prompt, surface?}`. Returns
   `{spoken, awaitingChoice, options?, detail?}`. This is "say something to
   codex on my box, get one sentence back."
5. **`runner_start`** — *new.* Wake box if needed (Gap D), ensure runner authed
   (mirror creds via P2P if this device holds them), spawn/attach the
   tmux-persisted session, return the session name.
6. **`runner_sessions`** — *new.* List live runner sessions (wraps
   `/runner/sessions`) so "what's running on my box" is one call.
7. **`machine`** — lifecycle: `up`/`down`/`status`/`list` (wraps
   `machine_lifecycle`), confirm-gated. "Park my box" = `machine down`.
8. **`ssh_run`** — a thin, confirm-gated one-shot exec for plain VPSes without a
   Yaver agent (wraps `remote_exec`), so the two remote paths (mesh `ops` vs
   SSH `remote_exec`) are both reachable. Long-term, unify these.
9. **`project_chat`** — *optional new.* A retrieval verb over the user's
   projects (sfmg/talos/yaver) — see §7. Or just let Claude use `ops(verb:
   project_context)` + `runner_turn` for deep analysis; a dedicated verb only
   if we want curated cross-repo context.
10. **`say`** — already exists; keep it so Claude can force a spoken line.

Everything else (the other ~730 tools) stays owner/paired-token only, reachable
from Claude Code CLI on a laptop (stdio, full trust) but **not** from the cloud
connector.

## 5. Reachability — how the phone actually connects

1. Agent runs on the box with `--mode http` (or the daemon already serves
   `/mcp`). 
2. Box registers a relay tunnel; connector URL is the stable
   `https://relay.yaver.io/d/<deviceId>/mcp` (works from cellular, no LAN).
3. In the Claude app: add custom connector → paste that URL. The 401 →
   RFC 9728 discovery → the user signs into Yaver's own OAuth AS
   (`/oauth/authorize`) → connector gets an RS256 access token.
4. First connect lands in the **default toy scope**; the user elevates it once
   from the Yaver app (Gap A). 
5. `yaver mcp setup` should grow a `phone`/`connector` target that prints the
   relay URL + a QR so adding it is one scan (mirrors `mcp-setup.go:107` which
   today only wires claude-code/codex/opencode stdio).

Relay password: the `/d/` proxy needs relay auth (`server.go:1043`). For a
per-user relay that's already handled; confirm the connector flow carries it
(likely via the box's registration, not the phone).

## 6. Confirm model for eyes-free ACTs

Reuse what the watch/car already do — don't invent:

- **Reads fire freely** (status, logs, `runner_sessions`, `ops_plan`).
- **Writes/ACTs confirm** — `machine down`, `deploy`, force-push, `ssh_run`.
  The runner review gate ("codex wants to force-push — say confirm") is the
  killer feature per the smartwatch doc; `runner_turn` surfaces
  `awaitingChoice`+`options` and the spoken layer reads them.
- Never speak code/diffs/secrets — the summarizer already refuses these
  (`carVoiceCoding.ts:165-180`).
- OAuth never happens on a car surface — head unit says "approve on your phone."

## 7. Deep project chat (sfmg / talos / yaver)

Two options:

- **Cheap:** no new verb. Claude (phone) uses `ops(verb: project_context)`,
  `git_log_advanced`, `search_content`, `read_file` (elevated) against
  `machine=<box>` where the repos live, plus `runner_turn` to ask a *local*
  runner on the box to do the heavy reading. The runner has the repo checked
  out; Claude just orchestrates and narrates. Best for "deep deep analysis"
  because the analysis runs where the code is, not over the wire.
- **Curated:** a `project_chat` verb that assembles cross-repo context (the
  connect-your-project surfaces from memory `project_connect_your_project_pivot`).
  Only if the cheap path proves too chatty.

Recommendation: ship the cheap path first — it's zero new retrieval infra and
matches "phone head → remote muscle."

## 8. Security posture (summary)

- Default connector scope stays toys (`oauth_mcp.go`) — stranger-safe.
- New `owner-connector` elevation is explicit, per-connector, revocable, and
  still Layer-4-blocked + confirm-gated on ACTs.
- Mesh stays single-user (caller's bearer forwarded, target validates
  ownership — `mcp_remote_proxy.go`).
- Secrets never leave a box; credential mirroring is P2P, never Convex
  (matches the privacy contract + `runner_auth` design).
- Every third-party-facing loop obeys the CLAUDE.md do-no-harm rules; a runner
  on a Hetzner box that hits external services must identify/backoff/stop.

## 9. Implementation plan (phased)

**P0 — prove the connector end-to-end (no new power yet)** — CLI DONE, live test pending
- [x] Add `yaver mcp setup phone` printing the `/d/<id>/mcp` relay URL + QR.
- [ ] Verify the Claude app can add it, OAuth through Yaver's AS, and call a toy
  tool (`weather`) over the relay. This de-risks the whole transport. (Needs a
  running box with a relay tunnel — do this before relying on the flow.)

**P1 — elevated owner-connector scope (Gap A + B)** — SERVER DONE, app UI pending
- [x] Elevation marker + second allowlist (the §4 set); stamp elevated
  `X-Yaver-AllowedTools`; keep Layer-4 block. Elevation minted only on the
  human consent checkbox, stripped from client-supplied scope.
- [ ] App UI to show/revoke an elevated connector.

**P2 — the runner verbs (Gap C)** — turn/sessions/wake DONE; start/ssh pending
- [x] `runner_turn`, `runner_sessions` as `ops` verbs wrapping the existing
  `/runner/session/turn` + `/runner/sessions`, with a surface-aware spoken summary.
- [x] `machine_wake` (wake-by-name from snapshot) — plus the pre-existing
  `machine_create/up/down/rm` verbs, all reachable via the elevated `ops` scope.
- [ ] `runner_start` — DEFERRED, not dropped. A headless spawn must reuse the
  auth-repair path (`preflightRemoteRunnerAuth`: subscription login, P2P
  credential mirror, stale-login-screen retirement); a naive detached
  `tmux new-session` would leave sessions parked on a login prompt. Build it
  ON TOP of that path, and validate on a live authed box — not before. Until
  then, start sessions with `yaver <runner> --machine=<box>` (auth-aware) and
  drive them with `runner_turn`.
- [ ] `ssh_run` — wrap `remote_exec` for plain (agent-less) VPSes; confirm-gated.
  Needs a live box to validate SSH round-trip.

**P3 — surface-awareness + auto-wake (Gap D)**
- `surface` field on `ops`/`runner_turn`; route car/watch results through the
  existing summarizer before returning `spoken`.
- Wire `hetznerStartServer` wake into `runner_start`/`runner_turn` when the
  target box is `stopped`; wait for `/health`.

**P4 — polish**
- `project_chat` only if the cheap path (§7) is insufficient.
- Codex/Claude-Code-CLI parity check (they use stdio + full scope already;
  confirm the §4 verbs also read well there).

## 9b. Hosting tiers + auto scale-to-zero (cost)

The agent must clearly distinguish how a box is hosted, because it decides
whether Yaver may power-manage it. Three tiers (`hosting_tier.go`):

| Tier | What it is | Auto-lifecycle? |
|---|---|---|
| `managed` | Yaver infra, customer pays Yaver metered. cloudMachines row, `origin != self-hosted`. | Yes — scale-to-zero cuts the customer's Yaver bill. |
| `byo` | Customer's own cloud account, Yaver provisioned it + holds the snapshot/recreate path (byoMachines row). | Yes — cuts THEIR provider bill; CLAUDE.md requires it for any Hetzner box. Flagged separately in UI. |
| `self-hosted` | Customer's own pre-existing box, just runs the agent. No provisioning record. | **NEVER.** Hands off — no snapshot, no delete, no auto-anything. |

**Safety invariant:** uncertain provenance → classify as `self-hosted`
(`resolveLocalHostingTier` fails safe). An auto-delete of the wrong box is
unrecoverable; a missed saving is not. `machine_wake` already can't touch a
self-hosted box (no byoMachines row → `not_found`).

**Park policy — idle + grace-confirm** (`scaleToZeroDecision`, pure + tested):
managed/byo only → after `IdleTimeout` with no active runner session / connector
activity, `notify` ("parking sfmg in 2 min unless you say keep-alive") → after
`GraceWindow` still idle → `execute` (snapshot + delete). A keep-alive or any
activity cancels it. Wake on the next `runner_turn` / `machine_wake`.

**Built (this pass):**
- Tier taxonomy, classifier, `tierAllowsAutoLifecycle`, `scaleToZeroDecision` —
  unit-tested (`hosting_tier_test.go`).
- **Three-way `hosting` in the backend**: `listMyDevices` now joins
  `byoMachines` too and returns `yaver-hosted | byo | self-hosted`
  (`devices.ts`); `ListedDevice.hosting` widened. Self-hosted is the default
  (no row) — so most boxes are already tagged, and safely.
- **Managed self-detection**: `cloudMachines.hostingForDevice` query +
  `GET /machine/hosting` route (`http.ts`); agent `resolveLocalHostingTier`
  calls it and fails safe to self-hosted on any error.
- **Seeding**: `machine_seed` ops verb (`byo_sync.go`) reconciles real provider
  inventory into `byoMachines` AND links THIS box to its row by public-IP match
  (the device↔box link the byo tier needs). Managed keeps precedence.
- **Park loop, decide-only**: `machine_park_check` + `machine_keepalive` ops
  verbs (`park_check.go`). The control plane polls `machine_park_check`; on
  `execute` IT calls `machine_down` — the box never deletes itself. Activity is
  tracked from real runner sessions (`touchParkActivity` in the runner path),
  not from poll traffic. Config: `YAVER_PARK_IDLE_MIN`/`YAVER_PARK_GRACE_MIN`.
  Tests: `park_check_test.go`.

**Pending (needs prod deploy + live box):** deploy the backend to prod Convex
(currently only codegen'd to dev); run `machine_seed` on each byo box to link
its deviceId; the control-plane poller that calls `machine_park_check` on a cron
and executes `machine_down` on `execute`. Do not enable auto-park until that
poll→execute path is validated end-to-end on a disposable managed box.

## 10. Open questions

1. **Elevation UX** — one-tap "trust this connector fully" vs per-verb-family
   grants? Recommend one-tap owner elevation + per-ACT confirm, since the
   confirm gate already backstops it.
2. **Default target box** — should `machine` omitted mean `primary`
   (`resolvePrimaryDeviceID`) so "run codex on sfmg" needs no box name in the
   car? Recommend yes: default `machine=primary`, `auto` for project-aware
   placement.
3. **Self-hosted vs Yaver-hosted** — auto-wake only applies to BYO/Yaver-hosted
   Hetzner (`byoMachines`); a self-hosted always-on box needs no wake. The verb
   should no-op wake when the row isn't `stopped`.
4. **Relay password on the connector path** — confirm the phone-added connector
   inherits relay auth from the box registration and the user never types it.
5. **Codex/ChatGPT connector** — same `/mcp` works, but ChatGPT's connector
   scope model differs; validate the elevation story there separately.
</content>
</invoke>
