# Dev Environment Clone Handoff

This is the handoff/audit dump for Claude Code or another coding agent to
continue the Yaver dev-environment clone feature.

Read these first:

- `AGENTS.md`
- `CLAUDE.md`
- `AI_ARCH.md`
- `REMOTE_WORKER.md`
- `DEV_ENV_CLONE.md`

Important repo rule: Markdown may be stale. Grep code before trusting any claim
below.

## User Intent

Build "clone dev environment" for Yaver:

- After `yaver auth`, user can attach/buy a Yaver-managed cloud machine or
  self-hosted box and turn it into a remote coding environment.
- Primary use case is coding agents and terminal development, not GUI app clone.
- Clone should include repos, toolchains, terminal/editor/shell configs, coding
  agent readiness, Hermes/Gradle/Xcode where platform supports it.
- Clone/sync data must be secure P2P agent-to-agent. Nothing sensitive or
  user-private from clone payloads should be stored in Convex.
- Broadly detect tools/configs, but install only tools the source actually has.

## Current Worktree State

This handoff was originally written during a conflicted worktree, but that
state has since been resolved. Before relying on any note below, check the
current `git status --short` and grep the code paths named here.

Feature files added:

- `DEV_ENV_CLONE.md`
- `DEV_ENV_CLONE_HANDOFF.md`
- `desktop/agent/dev_env_clone.go`
- `desktop/agent/dev_env_clone_http.go`
- `desktop/agent/dev_env_clone_test.go`
- `desktop/agent/dev_config_clone.go`
- `desktop/agent/mcp_dev_env_clone.go`

Feature files modified:

- `desktop/agent/binary_discovery.go`
- `desktop/agent/env_profile.go`
- `desktop/agent/env_profile_test.go`
- `desktop/agent/httpserver.go`
- `desktop/agent/install_cmd.go`
- `desktop/agent/mcp_tools.go`

## What Was Implemented

### Design/spec dump

`DEV_ENV_CLONE.md` now documents:

- Feature goal and non-goals.
- P2P-only security boundary.
- Convex allowed/forbidden data boundary.
- Public HTTP and MCP control plane.
- Request contract.
- Job phases.
- Toolchain/config scope.
- Remaining managed-cloud integration work.

### HTTP API

Added endpoints in `desktop/agent/httpserver.go`:

- `POST /dev-environments/clone/plan`
- `POST /dev-environments/clone/start`
- `GET /dev-environments/clone/status?id=<jobId>`
- `POST /agent/dev-configs/bundle`
- `POST /agent/dev-configs/apply`

Handler files:

- `desktop/agent/dev_env_clone_http.go`
- `desktop/agent/dev_config_clone.go`

### MCP API

Added MCP tools in `desktop/agent/mcp_dev_env_clone.go` and registered them from
`desktop/agent/mcp_tools.go`:

- `dev_environment_clone_plan`
- `dev_environment_clone_start`
- `dev_environment_clone_status`

MCP dispatch routes through `dispatchDevEnvironmentCloneMCP` from the default
MCP tool-call path in `httpserver.go`.

### Clone Orchestrator

Core implementation is in `desktop/agent/dev_env_clone.go`.

It supports:

- target modes: `existing-device`, `ssh`, `managed-cloud`
- source profile from local or peer device
- in-memory clone job registry
- plan/start/status lifecycle
- SSH bootstrap via `RemoteManager.Setup`
- target toolchain/profile apply
- repo clone
- `yaver code` repo selection
- target verify via `/agent/capabilities` and `/runner-auth/status`
- config bundle transfer/apply

Remote calls use existing `proxyToDeviceJSON` with the caller label
`dev_environment_clone`. This is the key privacy property: clone payloads move
through the authenticated owned-device path, not Convex persistence.

### Tool Detection

Expanded `desktop/agent/binary_discovery.go`:

- Adds category, priority, and install-target metadata to `DetectedBinary`.
- Broadens discovery to Yaver, Cloudflare/Wrangler, Convex, Vercel, Netlify,
  Firebase, Railway, Fly, Supabase, Node/npm/npx/pnpm/yarn/bun/deno, Go,
  Python, Rust, Ruby, PHP/Composer, Dart/Flutter, Java/Gradle/Maven, brew,
  apt-get, dnf, pacman, zypper, apk, snap, flatpak, winget/choco/scoop,
  Docker, kubectl, Terraform, AWS/gcloud/az, rg/ripgrep, fd/fdfind, bat/batcat,
  jq, fzf, tmux, build tools, Xcode tools, CocoaPods, fastlane, SQLite/Postgres,
  Redis, Android tools, ffmpeg.

Important behavior:

- Package managers are detected and reported, but have no install target. They
  are evidence for how to install other things, not things to clone blindly.
- Unknown binaries are not automatically install targets.

### Toolchain Apply

Expanded `desktop/agent/env_profile.go`:

- `EnvironmentProfile` now includes:
  - `ToolchainTargets []string`
  - `Configs []DetectedDevConfig`
- `toolchainTargetsFromBinaries` maps detected source binaries into prioritized
  install targets.
- Apply order prioritizes Yaver/Cloudflare/Convex/Git/Node/language/infra/dev
  tools instead of alphabetical-only install order.
- `profileInstallTarget` now maps more binaries to targets but still only
  installs targets that were present in the source profile and are supported by
  `lookupIntegration` or `PackageRegistry`.

Added install targets in `desktop/agent/install_cmd.go`:

- `wrangler`
- `go`
- `rg`
- `fd`
- `bat`
- `jq`
- `fzf`

Existing targets already covered Git, gh/glab, Node, Convex, Vercel,
cloudflared, Docker, tmux, ffmpeg, etc.

### Developer Config Clone

Added `desktop/agent/dev_config_clone.go`.

Detected config candidates:

- `.vimrc`
- `.gvimrc`
- `.vim`
- `.config/nvim`
- `.tmux.conf`
- `.config/tmux`
- `.zshrc`
- `.bashrc`
- `.bash_profile`
- `.profile`
- `.inputrc`
- `.oh-my-zsh/custom`
- `.config/starship.toml`
- `.config/i3`
- `.i3`
- `.config/sway`
- `.config/alacritty`
- `.config/kitty`
- `.config/wezterm`
- `.gitconfig`
- `.gitignore_global`
- `.codex`
- `.claude`
- `.config/opencode`

Safety behavior:

- No raw home-directory clone.
- File size limit: 256 KiB per file.
- Bundle size limit: 4 MiB.
- File count limit: 256.
- Skips likely secret-bearing names/paths: secret, token, apikey, credentials,
  oauth, session, cookie, keychain, private SSH keys, `.env`, etc.
- Skips contents with common API-key/token patterns.
- Target apply validates relative paths and rejects absolute/parent traversal.
- Existing target files are backed up with `.yaver-backup-YYYYMMDDHHMMSS`.

## Verification Already Run

From `desktop/agent`:

```sh
go test . -run 'TestEnvironmentProfile|TestDevEnvironmentClone|TestCloneURLForDetectedRepo|TestDevConfigBundle'
go build .
```

Both passed after resolving working-tree conflict markers in
`desktop/agent/httpserver.go`.

Also ran:

```sh
git diff --check -- DEV_ENV_CLONE.md desktop/agent/binary_discovery.go desktop/agent/env_profile.go desktop/agent/install_cmd.go desktop/agent/dev_config_clone.go desktop/agent/dev_env_clone.go desktop/agent/dev_env_clone_http.go desktop/agent/dev_env_clone_test.go desktop/agent/mcp_dev_env_clone.go desktop/agent/httpserver.go desktop/agent/mcp_tools.go
```

It passed.

Earlier, a full `go test .` was stopped because the package has slow/integration
tests. Prefer focused tests unless you intentionally want that long run.

## Known Gaps / Remaining Implementation

### 1. Recheck Git State Before Continuing

Before continuing this feature:

- Inspect `git status --short`.
- Check for conflict markers in touched files.
- Run the focused backend tests before changing the clone handlers.

Useful checks:

```sh
rg -n '<<<<<<<|=======|>>>>>>>' desktop/agent/httpserver.go
go test . -run 'TestEnvironmentProfile|TestDevEnvironmentClone|TestCloneURLForDetectedRepo|TestDevConfigBundle'
go build .
```

### 2. Managed Cloud Flow Integration

Current clone contract supports `target.mode = managed-cloud`, but if there is
no `targetDeviceId`, the job stops with a warning/manual continuation.

Needed:

- Find the managed-cloud purchase/provisioning flow.
- After cloud machine reports a Yaver device id, call
  `dev_environment_clone_start`.
- Keep clone request payloads agent-local. Convex/cloud rows may store only
  billing/provisioning/entitlement and non-sensitive phase names.

Do not store in Convex:

- repo URLs
- branches
- paths
- config manifests/content
- logs/output
- env vars
- runner config
- tokens/credentials

### 3. Real Device Resolution / Ownership UX

`existing-device` currently requires a target device id but the plan does not
deep-validate target ownership/reachability before start. The underlying proxy
will fail if invalid.

Better UX:

- Resolve device aliases/names through existing owned-device APIs.
- Show online/offline and platform in plan.
- Fail plan early if target is clearly not owned/reachable.

### 4. Toolchain Desired-State Model

Current profile is observed-source -> install-target list. It is pragmatic but
not a full desired-state toolchain model.

Needed later:

- `ToolchainProfile`
- `ToolchainBundle`
- version pins when safe
- platform capability requirements
- target actual-vs-desired diff
- project manifest integration

### 5. Install Coverage

Broad discovery exists, but not every detected binary is installable.

Current deliberately installable additions:

- `wrangler`
- `go`
- `rg`
- `fd`
- `bat`
- `jq`
- `fzf`

Still worth adding install plans or registry entries for:

- `deno`
- `flutter`
- `dart`
- `kubectl`
- `terraform`
- `aws`
- `gcloud`
- `az`
- `netlify`
- `firebase`
- `railway`
- `flyctl`
- `fastlane`
- `pod`
- `make` / `cmake` / `ninja` / compiler meta-targets

Keep the rule: broad detect, source-driven install only.

### 6. Config Clone Hardening

Current config clone is allowlisted and filtered, but still needs product-level
review.

Open questions:

- Should `.gitconfig` be split to strip credential helpers/includeIf paths?
- Should `.codex`, `.claude`, `.config/opencode` be partially parsed rather
  than copied after content grep?
- Should Vim/Neovim plugin dirs be excluded more selectively? Current skips
  common heavy dirs like `plugged` and `bundle`.
- Should shell rc files be rewritten to avoid machine-specific absolute paths?
- Should config apply be opt-in by default in mobile/web, even though API
  default currently applies configs unless `skipConfigs` is true?

### 7. Remote Source Repo Inference

Local source can infer repos from discovered projects via local git remotes.
Remote source profile currently cannot infer repo URL because profile only has
project path/branch.

Needed:

- Add safe repo remote summary to `EnvironmentProjectSummary`, or
- Add peer endpoint that returns sanitized repo clone URLs only for explicit
  clone flow.

Privacy warning: repo URLs must not go to Convex.

### 8. UI / Product Surface

No mobile/web UI was implemented in this slice.

Needed:

- Managed cloud checkout/onboarding CTA.
- Plan preview:
  - target
  - source tools
  - installable targets
  - configs to clone
  - repos
  - warnings/manual blockers
- Start/status screen.
- Clear text that configs/credentials move P2P and are not stored in Convex.

### 9. Auth/Credential Boundaries

Implemented:

- Git credentials only transfer if `includeGitCredentials` is true.
- Config clone filters likely tokens.

Still separate and should remain separate:

- Runner credential import.
- API key vault import.
- Cloud provider tokens.
- Xcode signing/Keychain.

Do not merge these into generic config clone.

### 10. Better Tests

Existing tests added:

- plan requires target for existing-device
- SSH without target device id becomes manual continuation
- HTTP plan endpoint
- clone URL helper
- config bundle allowlist/secret filtering

Needed:

- HTTP `/agent/dev-configs/bundle` and `/apply` tests.
- Dry-run clone job test.
- Remote proxy call tests with fake peer.
- Toolchain target ordering test.
- Package-manager detection should not create install targets test.
- Config apply backup/path traversal test.

## Suggested Next Command Sequence

From repo root:

```sh
rg -n '<<<<<<<|=======|>>>>>>>' desktop/agent/httpserver.go mobile/app/glass-terminal.tsx
git status --short
cd desktop/agent
go test . -run 'TestEnvironmentProfile|TestDevEnvironmentClone|TestCloneURLForDetectedRepo|TestDevConfigBundle'
go build .
```

If any file shows `UU`, resolve the index carefully without touching unrelated
files.

## Architecture Notes

Important functions/files:

- `buildEnvironmentProfile` in `env_profile.go`
- `DiscoverInstalledBinaries` in `binary_discovery.go`
- `buildDevEnvironmentClonePlan` in `dev_env_clone.go`
- `runDevEnvironmentCloneJob` in `dev_env_clone.go`
- `applyDevEnvProfileToTarget` in `dev_env_clone.go`
- `applyDevEnvConfigsToTarget` in `dev_env_clone.go`
- `buildDevConfigBundle` in `dev_config_clone.go`
- `applyDevConfigBundle` in `dev_config_clone.go`
- `devEnvironmentCloneMCPTools` in `mcp_dev_env_clone.go`

Important security invariant:

The clone orchestrator may use Convex-backed identity/discovery/entitlement
systems indirectly through existing Yaver auth/device plumbing, but clone
payloads themselves must stay agent-local or agent-to-agent:

- tool profiles
- repo lists
- config bundle contents
- git credentials
- job details/logs

## One-Line Status

A working first backend/MCP vertical slice exists and builds, with broad
tool/config discovery and P2P-only payload flow. The largest remaining work is
managed-cloud UI/provisioning integration, stronger config/toolchain hardening,
more tests, and cleaning the dirty/unmerged worktree state before commit.
