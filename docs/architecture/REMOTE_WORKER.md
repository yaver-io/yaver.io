# Yaver as a Plug-in Tool for Existing Coding Agents

This doc specs Yaver's plug-in mode, where Yaver acts as an MCP tool server
for an existing Claude Code / Codex / Cursor / Windsurf / Zed install. In
this mode the coding agent drives and Yaver provides capabilities (builds,
deploys, Hermes push, containerization, remote workers).

## What this doc adds (scope)

1. A plug-and-play install story: `npm install -g yaver-cli` + `yaver auth`,
   and every coding agent on the machine sees Yaver's MCP tools on next launch.
2. A `device_id` parameter on build / deploy / dev-loop MCP tools so a
   laptop's Claude Code can offload work to another Yaver box.
3. A handful of new convenience tools (`list_machines`,
   `run_project_in_sandbox`, `git_clone` with `device_id`, `public_url`, …).
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
| Container sandbox runtime | `container_runner.go`, `Dockerfile.sandbox` | **Exists** |
| Hermes bundle compile + push | `/dev/build-native`, `yaver-cli`'s bundled `hermesc` | **Exists** |
| Dev server manager (Expo / Flutter / Vite / Next) | `devserver.go` | **Exists** |
| Machine enumeration + capabilities | `console_machines.go::listAllMachines`, `MachineCapabilities` | **Exists** |
| Relay re-targeting by device id | `{relay}/d/{deviceId}/…` used by web + mobile today | **Exists** |
| Cross-device auth (one token owns all user's agents) | `auth.go`, `httpserver.go::auth()` | **Exists** |
| Guest token rejection prefixes | `httpserver.go::guestAllowedPrefixes` | **Exists** |
| Cross-machine MCP/ops proxy | `mcp_remote_proxy.go::proxyToDevice`, `ops.go` | **Exists** |
| Mac mini Apple-surface worker bootstrap | `scripts/setup-mac-mini-dev.sh`, `docs/mac-mini-remote-worker.md` | **Exists** |

## What's missing (the actual work)

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
`project_list`, `project_scaffold`, `project_switch_backend`, `phone_project_*`.

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
- `run_project_in_sandbox(project_dir, device_id?, api_keys?)` — builds the
  yaver-sandbox image if needed, mounts the project, injects only the listed
  API keys as env vars, starts the dev server inside, pushes Hermes bundle
  to the paired phone. Wraps existing `ContainerRunner` + `DevServer`.

## The hot path — Hermes push-to-device

This is what the vibe-coder will *feel*. Every few keystrokes, not every few
days. Budget: under 2 seconds end to end.

**Local flow (Shape A — plug-in mode, one machine):**

1. Claude Code edits `App.tsx` on the laptop
2. Claude Code calls `dev_reload(mode="bundle", work_dir="<project-dir>")`
3. Local Yaver daemon runs `hermesc`, POSTs the bundle to the paired
   phone on port 8347
4. Phone reloads under 2s

**Remote flow (Shape B — plug-in + remote worker, two machines):**

1. Claude Code edits `App.tsx` on the laptop; source is mirrored to the
   Mac mini via git / rsync
2. Claude Code calls `dev_reload(device_id="mac-mini", mode="bundle", work_dir="…")`
3. Laptop's Yaver daemon proxies to `{relay}/d/mac-mini/dev/build-native`
4. Mini compiles Hermes, pushes to the phone on its LAN
5. Phone reloads under 2s; laptop's fans never spin up

Same tool, same signature, only `device_id` differs.

## Use cases, by frequency

1. **Hermes push-to-device** (every few keystrokes — the dominant case)
2. **Third-party project containerization** (`run_project_in_sandbox`)
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

**Phase 1 — hot path (Hermes + sandbox + list_machines + proxy)**:

- [ ] `desktop/agent/mcp_remote_proxy.go` — `proxyToDevice()` (~50 LOC)
- [ ] `desktop/agent/mcp_tools.go` — `list_machines` tool
- [ ] `desktop/agent/mcp_tools.go` — `run_project_in_sandbox` tool
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

> "clone github.com/some/rn-app, open it in a sandboxed container, push the
>  Hermes bundle to my phone"

Claude Code calls (no user intervention):

1. `list_machines` → sees the local box; Docker + phone pairing present
2. `git_clone(repo="…", target_dir="/tmp/rn-app")`
3. `run_project_in_sandbox(project_dir="/tmp/rn-app")`
4. Container spins up, dev server starts inside, Hermes bundle lands on
   phone, phone reloads in under 3s
5. Host toolchain untouched: no `npm install` on the host, no leaked env

Same afternoon the vibe-coder edits their own RN app — Claude Code calls
`dev_reload(mode="bundle")` dozens of times, each round-trip under 2s. Fans
stay quiet.

Next week they add a Mac mini, run `yaver auth` on it, and Claude Code on
the laptop starts offloading builds with `device_id="mac-mini"` — no new
commands to learn. Same tools, one new param.

That is the bar.
