# Yaver as a Plug-in Tool for Existing Coding Agents

This doc specs Yaver's plug-in mode, where Yaver acts as an MCP tool server
for an existing Claude Code / Codex / Cursor / Windsurf / Zed install. In
this mode the coding agent drives and Yaver provides capabilities (builds,
deploys, Hermes push, Yaver Git, Yaver Serverless, remote workers).

Current product policy (2026-07-19): new app development always depends on a
real remote box — either a self-hosted Yaver mesh machine or Yaver Managed
Cloud. The phone/web/car/watch surfaces are control, voice, feedback, and
preview surfaces; they are not the development sandbox. The old phone/browser
sandbox code remains in-tree for a future phone-local LLM path, but it is not
shown in UI or MCP discovery.

Greenfield defaults unless the user says otherwise:

- Yaver product stack: Yaver Mesh, Relay Pro, Cloud Workspace, Yaver Git,
  Yaver Serverless, Hermes/WebRTC preview, Feedback SDK
- repo home: Yaver Git
- layout: Yaver monorepo
- backend/data: Yaver Serverless
- execution: selected remote box (`device_id`) or Cloud Workspace placement
- infra/transport: Yaver Mesh by default, compatible with direct LAN, Yaver
  Relay, and Tailscale-style private addresses when present
- mobile preview: Hermes reload through Yaver
- web preview: Yaver web preview / remote runtime
- control surfaces: phone, web, watch, car, TV, and AR/VR all create/continue
  the same remote-box tasks with STT/TTS hints when present
- feedback loop: newly-created apps should wire the Yaver Feedback SDK when
  appropriate so shake, voice notes, screenshots, crashes, and black-box context
  feed follow-up tasks back into the same remote repo

## What this doc adds (scope)

1. A plug-and-play install story: `npm install -g yaver-cli` + `yaver auth`,
   and every coding agent on the machine sees Yaver's MCP tools on next launch.
2. A `device_id` parameter on build / deploy / dev-loop MCP tools so a
   laptop's Claude Code can offload work to another Yaver box.
3. A handful of convenience tools (`list_machines`, `create_task` with
   `device_id`, `git_clone` with `device_id`, `public_url`, …).
4. New layers surfaced through the existing MCP dispatcher (tokens, vault,
   projects, tunnels) — all thin wrappers over things that already exist
   on the HTTP side.

## Install UX contract

Two commands, zero config editing:

```
npm install -g yaver-cli
yaver auth
```

After the second command:

- Daemon is running (auto-started by `startServeIfStopped()`).
- Yaver is registered as an MCP server in every coding agent the machine has
  installed.
- The user restarts their coding agent and the tools are there.

If the vibe-coder has to read anything past those two commands, the install
has failed. Plug and play or it is not the product.

## What already exists (don't re-implement)

Verified by reading the code:

| Piece | Where | Status |
|---|---|---|
| `yaver mcp` stdio subcommand | `main.go::runMCP` → `runMCPStdio` | **Exists** |
| `yaver mcp` HTTP subcommand | same | **Exists** |
| Idempotent registration into Claude Code / Codex / opencode | `mcp-setup.go::autoSetupMCP`, `setupClaudeCode`, `setupCodex`, `setupOpenCode` | **Exists** |
| Auto-register at end of `yaver auth` | `main.go` calls `autoSetupMCP()` at lines 602 / 639 / 735 / 1527 | **Exists** |
| Auto-start daemon after auth | `main.go::startServeIfStopped` | **Exists** |
| MCP dispatcher | `mcp_tools.go` | **Exists** |
| Legacy container sandbox runtime | `container_runner.go`, `Dockerfile.sandbox` | **Exists but hidden from UI/MCP** |
| Hermes bundle compile + push | `/dev/build-native`, `yaver-cli`'s bundled `hermesc` | **Exists** |
| Dev server manager (Expo / Flutter / Vite / Next) | `devserver.go` | **Exists** |
| Machine enumeration + capabilities | `console_machines.go::listAllMachines`, `MachineCapabilities` | **Exists** |
| Relay re-targeting by device id | `{relay}/d/{deviceId}/…` used by web + mobile today | **Exists** |
| Cross-device auth (one token owns all user's agents) | `auth.go`, `httpserver.go::auth()` | **Exists** |
| Guest token rejection prefixes | `httpserver.go::guestAllowedPrefixes` | **Exists** |
| Cross-machine MCP/ops proxy | `mcp_remote_proxy.go::proxyToDevice`, `ops.go` | **Exists** |
| Mac mini Apple-surface worker bootstrap | `scripts/setup-mac-mini-dev.sh`, `docs/mac-mini-remote-worker.md` | **Exists** |

## What's missing (the actual work)

> **Measured 2026-07-18 — read this before scoping Layers 2 and 3.**
>
> Layer 1 is **done**: all six dev-loop verbs route to a remote worker. The audit
> that produced this note found Layer 1 was already 5 of 6 wired, not 0 of 6 —
> `proxyToDevice` exists and 57 tools already accept `device_id`. Only
> `mobile_hermes_doctor` was missing, and it needed a new
> `/mobile/hermes/doctor` route rather than just a flag: advertising `device_id`
> with nothing to proxy to would have shipped a capability that 404s.
>
> Layers 2 and 3 measure at **3/16** and **1/11** typed verbs wired — but that
> understates what works. **`exec_command` already accepts `device_id` and
> proxies**, and every Layer 2/3 verb is a shell command underneath:
> `gradle build`, `cmake --build`, `npm run …`, `git status`. So remote build,
> deploy and git are **possible today**; what is missing is the typed verb, not
> the capability.
>
> That makes Layers 2–3 an **ergonomics and discoverability** job — structured
> output, schema-level discovery, no hand-written shell — rather than an
> enabling one. Worth doing, and worth not being scoped as a blocker.
>
> Each remaining verb costs three things, and the third is the one that bites:
> a `device_id` in the schema, a `proxyToDevice` branch in the dispatcher, and
> **an HTTP route on the receiving side**. Most of these verbs have no route
> today, which is why the count is not a one-line-per-tool job.


### A. `device_id` on existing tools

Touch only `mcp_tools.go` (the tool schemas) and each tool's dispatcher
branch. Every affected tool becomes:

```go
if req.DeviceID != "" {
    return proxyToDevice(ctx, req.DeviceID, "/path/…", body)
}
// existing local handler, unchanged
```

Tool layers (ship in this order):

**Layer 1 — Dev loop / Hermes (hot path, ship first):**
`dev_start`, `dev_stop`, `dev_status`, `dev_reload`, `dev_reload_app`,
`dev_build_native`.

**Layer 2 — Build / deploy:**
`gradle_build`, `gradle_test`, `xcodebuild_archive`, `xcodebuild_export`,
`xcodebuild_test`, `testflight_upload`, `playstore_upload`,
`cloudflare_deploy`, `wrangler_deploy`, `npm_publish`, `pypi_publish`,
`cmake_configure`, `cmake_build`, `cmake_test`, `cmake_install`.

**Layer 3 — Git / source control:**
`git_clone` (key win — clone onto a remote worker, no laptop hop),
`git_status`, `git_diff`, `git_log`, `git_commit`, `git_push`, `git_pull`,
`git_fetch`, `git_branch_*`, `git_stash_*`, `github_pr_*`, `gitlab_mr_*`.

**Layer 5 — Projects / scaffolding:**
`project_list`, `project_scaffold`, `project_switch_backend`, Yaver Git
repo creation, Yaver Serverless setup. `phone_project_*` stays legacy/hidden
unless the phone-local LLM path is deliberately re-enabled.

**Layer 6 — Tunnels / local exposure:**
`tunnel_expose`, `tunnel_list`, `tunnel_close`, `public_url`.

**Escape hatch:**
`exec`, `fs_read`, `fs_write`, `fs_list`, `tmux_send`, `tmux_capture`.

### B. Layer 4 — local-only tools (reject `device_id`)

Secrets must not cross machines via the MCP layer. These tools stay on the
local daemon only; `proxyToDevice()` returns an error if any of them is
called with a non-empty `device_id`:

- `sdk_token_create`, `sdk_token_list`, `sdk_token_rotate`, `sdk_token_revoke`
- `vault_set`, `vault_get`, `vault_list`, `vault_delete`
- `env_import`, `env_inject`
- `deploy_cred_set` (Apple / Play / npm / Cloudflare credentials)

### C. New convenience tools

- `list_machines` — thin wrapper over `listAllMachines` + `MachineCapabilities`.
  Implemented as a compatibility alias for `agent_machine_inventory`.
- `create_task(device_id?, work_dir?, placement_kind?, prompt, …)` — direct
  remote-box task creation. This is the MCP entry for a dreamer saying "I want
  a mobile app for dentists": select/wake a box, create the task there, and let
  the runner scaffold the Yaver monorepo with Yaver Git + Yaver Serverless.
- `sandbox_run` / `run_project_in_sandbox` — dormant legacy ideas. Do not expose
  or use them for app development unless the product explicitly re-enables
  phone-local development after local LLMs become good enough.

## The hot path — Hermes push-to-device

This is what the vibe-coder will *feel*. Every few keystrokes, not every few
days. Budget: under 2 seconds end to end.

**Remote-box flow (primary shape):**

1. User speaks/types a product idea from Tasks (phone, car, watch, TV, AR/VR,
   web, CLI, MCP).
2. Surface chooses a self-hosted Yaver box or Yaver Managed Cloud.
3. Agent/MCP calls `create_task(device_id="box", prompt="…")`.
4. The box runner creates or clones the Yaver Git repo, scaffolds the Yaver
   monorepo, wires Yaver Serverless, and commits progress.
5. For React Native / Expo, the box compiles Hermes and pushes reloads to the
   paired phone. For web, it starts a Yaver web preview / remote runtime.

Same tool, same signature, only `device_id` differs.

## Use cases, by frequency

1. **Greenfield app task on a remote box** (`create_task(device_id=…)`)
2. **Hermes push-to-device** (every few keystrokes for mobile apps)
3. **Remote dev server** (`dev_start(device_id=…)` for Metro / Vite / Next / Flutter)
4. **Remote build** (native changes only — `xcodebuild_archive`, `gradle_build`)
5. **Remote deploy** (TestFlight / Play / Cloudflare / npm — secrets on worker)
6. **Remote git clone** (`git_clone(device_id=…)` — pull straight onto the worker)
7. **Escape hatch** (`exec`, `fs_*`, `tmux_*`)

## Security / privacy

- **Auth**: user's existing bearer token; no new credentials.
- **Guests**: guest tokens rejected at the MCP stdio boundary (owner-only)
  and at `proxyToDevice()`. Guests already cannot drive builds from the
  mobile app across hosts; this inherits the same rule.
- **Audit**: proxied calls set `X-Yaver-Proxied-By: <laptop-device-id>`.
- **Secrets stay on the worker**: Layer 4 tools refuse `device_id`. Apple /
  Play / npm / Cloudflare credentials never leave the machine they were set
  on.
- **Additivity**: uninstall Yaver (`npm uninstall -g yaver-cli`) and Claude
  Code works as before. `yaver mcp unregister` guarantees no ghost entries.

## Implementation checklist

**Phase 0 — close the two-command install gap** (small):

- [ ] `mcp-setup.go::setupCodex(yaverPath)` writing `~/.codex/config.toml`
- [ ] Add `setupCodex` to `autoSetupMCP()`
- [ ] Add `.yaver-backup` file creation before every edit (paranoia)
- [ ] `yaver mcp unregister` — inverse of `autoSetupMCP()` touching the
      same six config files
- [ ] `cli/package.json` — `preuninstall` script runs `yaver mcp unregister`

**Phase 1 — hot path (Hermes + remote box + list_machines + proxy)**:

- [ ] `desktop/agent/mcp_remote_proxy.go` — `proxyToDevice()` (~50 LOC)
- [x] `desktop/agent/mcp_tools.go` — `list_machines` tool
- [x] `desktop/agent/mcp_tools.go` — `create_task.device_id` for direct remote task creation
- [x] `desktop/agent/mcp_tools.go` — hide sandbox MCP tools while remote-box-first is active
- [ ] `desktop/agent/mcp_tools.go` — `device_id` on Layer 1 tools only
- [ ] Guest-token rejection unit test
- [ ] Layer 4 refusal unit test (passing `device_id` returns error)
- [ ] `desktop/agent/mcp_remote_proxy_test.go` — two agents on localhost,
      proxy + auth + token isolation

**Phase 2 — build / deploy / git (Layers 2 + 3)**:

- [ ] `device_id` on Layer 2 tools (gradle / xcodebuild / cmake / deploy)
- [ ] `git_clone` with `device_id`
- [ ] `device_id` on remaining Layer 3 tools

**Phase 3 — tokens / projects / tunnels (Layers 4 + 5 + 6)**:

- [ ] Layer 4 tools (`sdk_token_*`, `vault_*`, `env_*`, `deploy_cred_*`)
      — local-only, with explicit refusal for non-empty `device_id`
- [ ] Layer 5 tools (`project_*`, `phone_project_*` re-export)
- [ ] Layer 6 tools (`tunnel_*`, `public_url`)

**Phase 4 — docs**:

- [ ] `README.md` — lead with "Install in two commands"
- [ ] `CLAUDE.md` — new "Plug-in mode (MCP with device_id)" section linking
      this doc
- [ ] `AI_ARCH.md` — diagram: Coding agent → `yaver mcp` stdio → daemon
      → local handler OR `{relay}/d/<id>/…`

## Non-goals (explicitly out of scope)

- Replacing the Claude Code mobile app. The vibe-coder uses both Yaver
  mobile and Claude Code mobile side by side.
- A scheduler / job queue for single tool calls. `agent_mesh.go` already
  schedules `agent_graph` runs; tool calls don't need it.
- Worker-initiated commands. The coding agent drives; Yaver responds.
- Cross-device file sync. Use `git push` / `git pull` / rsync.
- A new dashboard. The Console tab already lists machines.
- Guest cross-host routing. Scoped to one host by design.

## Success criterion

The two-command bar:

```
$ npm install -g yaver-cli
$ yaver auth
Signed in successfully.
  MCP configured for 3 editor(s). Restart them to activate.
```

Restart Claude Code. Without any config editing, say:

> "I have an idea for a mobile app for dentists. Build the first version and
>  show it on my phone."

Claude Code calls (no user intervention):

1. `list_machines` → sees a self-hosted Yaver box or Yaver Managed Cloud
2. `create_task(device_id="box", placement_kind="vibe", prompt="…")`
3. The box creates the Yaver Git repo, Yaver monorepo, and Yaver Serverless
   backend/data defaults
4. The box starts the mobile/web dev loop and pushes Hermes/web-preview updates
   to the user's selected surface
5. The app includes Yaver Feedback SDK wiring when appropriate, so shake/voice
   feedback, screenshots, crashes, and black-box events create follow-up tasks
6. The task commits progress on the remote box and reports the preview/reload
   URL or phone status

Same afternoon the vibe-coder edits their own RN app — Claude Code calls
`dev_reload(device_id="box", mode="bundle")` dozens of times, each round-trip
under 2s. The laptop/phone remain control surfaces; the box does the work.

Next week they add a Mac mini, run `yaver auth` on it, and Claude Code on
the laptop starts offloading builds with `device_id="mac-mini"` — no new
commands to learn. Same tools, one new param.

That is the bar.
