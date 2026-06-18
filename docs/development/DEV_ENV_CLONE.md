# Yaver Dev Environment Clone

This document is the implementation contract for cloning a coding-focused
development environment through Yaver. Code is still the source of truth; this
file exists to keep the feature shape coherent while the Go agent, MCP tools,
mobile app, web dashboard, and managed-cloud flow converge.

## Goal

After `yaver auth`, a user should be able to buy or attach a machine and turn it
into a usable remote coding environment:

- Yaver-managed cloud box
- self-hosted Linux box, Raspberry Pi, or Mac mini reachable over SSH
- existing owned Yaver device

The first use case is coding-agent work, not GUI app streaming. The target box
should be ready for:

- GitHub / GitLab repo clone and pull
- `yaver code --attach`
- Claude Code / Codex / OpenCode where available
- tmux / terminal workflows
- common terminal tools (`rg`/ripgrep, `fd`, `bat`, `jq`, `fzf`, `make`,
  compilers) when they exist on the source
- shell/editor/window-manager config such as `.vimrc`, Neovim, tmux, zsh/bash,
  Oh My Zsh custom snippets, i3/sway, and terminal emulator config
- Hermes bundle builds for React Native / Expo
- Android Gradle builds when the host can support Android tooling
- Xcode / TestFlight only on macOS hosts with Xcode and signing state

## Non-Goals

- Cloning arbitrary GUI apps.
- Blindly copying a whole home directory.
- Copying secrets into Convex.
- Pretending Linux cloud can clone macOS-only Xcode state.
- Replacing the existing `/agent/toolchain-sync/*`, `/repos/*`,
  `/runner-auth/*`, and `/machine/onboarding/*` APIs.

## Current Building Blocks

The feature is an orchestrator over existing code:

- `/agent/toolchain-sync/profile` and `/agent/toolchain-sync/apply`
  snapshot and apply observed toolchains.
- `/agent/dev-configs/bundle` and `/agent/dev-configs/apply` snapshot and apply
  allowlisted developer config files over the agent-to-agent path.
- `/machine/onboarding/status|apply|remove` handles OpenAI, GitHub, and GitLab
  credential readiness.
- `/repos/clone` clones repos with existing credential injection.
- `/runner-auth/credentials/import` and `/runner-auth/browser/*` handle runner
  auth on local or remote machines.
- `/runner/opencode/config` reads and patches OpenCode config.
- `/dev/build-native`, `/dev/start`, `/dev/reload`, `/builds/*`, and
  `/deploy/capabilities` verify build/deploy readiness.
- `RemoteManager.Setup` can bootstrap an SSH host with Docker, Node, Git, and
  Yaver.
- Managed cloud provisioning already creates Yaver-owned cloud machines and
  eventually yields a normal device id.

## Feature Shape

The public control plane is:

- `POST /dev-environments/clone/plan`
- `POST /dev-environments/clone/start`
- `GET /dev-environments/clone/status?id=<jobId>`
- MCP `dev_environment_clone_plan`
- MCP `dev_environment_clone_start`
- MCP `dev_environment_clone_status`

The first implementation supports existing devices and SSH targets. Managed
cloud is represented as a target mode in the contract, but the actual purchase
and provisioning wait should continue to use the existing managed-cloud billing
flow until the cloud machine has a device id.

## Request Contract

```json
{
  "sourceDeviceId": "optional; defaults to local machine",
  "targetDeviceId": "existing owned Yaver device",
  "target": {
    "mode": "existing-device | ssh | managed-cloud",
    "deviceId": "same as targetDeviceId",
    "sshHost": "203.0.113.10",
    "sshUser": "root",
    "managedCloudMachineId": "future"
  },
  "repos": [
    {
      "url": "https://github.com/org/repo.git",
      "branch": "main",
      "dir": "~/Workspace",
      "autoInit": true,
      "autoInitRunner": "codex"
    }
  ],
  "includeDiscoveredRepos": true,
  "installMissing": true,
  "includeGitCredentials": true,
  "skipConfigs": false,
  "configKeys": ["vimrc", "nvim-config", "tmux", "zshrc", "i3"],
  "syncKinds": ["runner-config"],
  "configureCode": true,
  "runnerIds": ["codex", "claude-code", "opencode"],
  "verify": true,
  "dryRun": false
}
```

## Job Phases

1. Resolve source and target.
2. For SSH target, run `RemoteManager.Setup`.
3. Fetch or build source environment profile.
4. Plan/apply target toolchain using `/agent/toolchain-sync/apply`.
5. Push GitHub/GitLab clone credentials only when explicitly requested.
6. Clone requested repos.
7. Configure `yaver code` target repo when requested.
8. Apply allowlisted developer configs unless `skipConfigs` is true.
9. Verify runner, repo, toolchain, and capabilities.
10. Return a durable job record with steps, warnings, errors, and next actions.

## Security Rules

- Secrets never go to Convex.
- Dev-environment clone payloads do not go to Convex: repo lists, clone plans,
  target paths, dotfile manifests, runner config, and job logs stay on the
  user's Yaver agents unless the user explicitly exports them.
- Device-to-device clone/sync must use Yaver's authenticated P2P path
  (direct/relay transport as implemented by the agent). Convex may be used only
  for identity, entitlement, device discovery, and reachability metadata.
- Git credentials transfer is direct device-to-device and owner-auth gated.
- Runner credential import is explicit and remains separate from environment
  profile sync.
- Dotfiles/configs are allowlisted, size-limited, path-checked, and filtered for
  likely secret-bearing names/content. Existing target files are backed up before
  overwrite.
- Layer-4 MCP tools stay local-only when `device_id` is set.
- Logs and job records must not contain raw tokens, API keys, credential URLs,
  or absolute paths from other users.

## Platform Policy

- macOS -> macOS can plan Xcode/TestFlight readiness, but still cannot clone
  Keychain signing state blindly.
- macOS -> Linux can install common web/Hermes/Android-capable tools, but Xcode
  becomes a manual blocker.
- Linux -> Linux can clone most CLI-centric state.
- Linux ARM targets are supported for web, Git, terminal, and many coding-agent
  flows; Android emulator and Xcode are blocked.

## Implementation Phases

### Phase 1: Vertical Slice

- Add job structs and in-memory registry.
- Add plan/start/status HTTP endpoints.
- Add MCP tools.
- Existing-device target calls `/agent/toolchain-sync/apply`, `/repos/clone`,
  `/runner-auth/status`, `/agent/capabilities`.
- SSH target runs `RemoteManager.Setup`, then asks for the new device id if the
  target agent is not yet registered.

### Phase 2: Managed Cloud Join

- Let the managed-cloud purchase/onboarding flow call `dev_environment_clone_start`
  after the machine reports an agent device id.
- Store only non-sensitive phase/progress state on the cloud machine row. The
  actual clone request, repo URLs, paths, and logs stay agent-local and move
  only over the authenticated device-to-device channel.

### Phase 3: Declarative Toolchains

- Promote the current observed `EnvironmentProfile` into a desired/actual diff.
- Add `ToolchainProfile`, `ToolchainBundle`, `CapabilityRequirement`, and
  version pins.
- Extend workspace/project manifests with toolchain intent.

### Phase 4: Dotfiles and Runner Config

- Implemented as an allowlisted config bundle over `/agent/dev-configs/*`.
- Current coverage: `.vimrc`, `.gvimrc`, `.vim` without plugin/vendor trees,
  `.config/nvim`, `.tmux.conf`, `.config/tmux`, `.zshrc`, `.bashrc`,
  `.bash_profile`, `.profile`, `.inputrc`, `.oh-my-zsh/custom`,
  `.config/starship.toml`, i3, legacy `.i3`, sway, Alacritty, Kitty, WezTerm,
  `.gitconfig`, global gitignore, Codex, Claude, and OpenCode config dirs.
- Files/directories with names like token, secret, credential, OAuth, session,
  cookie, private key, or `.env` are skipped. Contents with common API-key/token
  patterns are skipped.

### Phase 5: Verification Matrix

- Capability tests for Hermes, Gradle, Android SDK, Xcode, TestFlight, Play
  upload, Cloudflare, Convex, tmux, Codex, Claude, and OpenCode.
- The clone result must say exactly what is ready, blocked, or manual.

## MVP Acceptance

A user with a local signed-in Yaver agent and an already-online cloud/remote
Yaver device can call one tool and get:

- target toolchain synced as far as the platform supports,
- requested private repos cloned,
- Git credentials available on the target when explicitly requested,
- `yaver code` attached to the remote repo,
- runner/toolchain readiness report,
- a clear list of manual blockers.

## Convex Boundary

Allowed in Convex for this feature:

- machine/device ids and online state,
- managed-cloud billing/provisioning state,
- entitlement checks,
- non-sensitive phase names such as `provisioning` or `ready`.

Forbidden in Convex for this feature:

- repo URLs for a clone request,
- absolute paths,
- branch names tied to a user's private work,
- tool output/logs,
- environment variables,
- dotfile contents,
- runner config contents,
- API keys, PATs, OAuth tokens, SSH keys, or any credential material.
