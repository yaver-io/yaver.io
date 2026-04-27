# Yaver Toolchain Share

This document defines the intended architecture for Yaver-managed toolchains across:

- one developer with multiple machines
- a team sharing portable build/deploy capabilities
- guests joining an existing workspace quickly
- project-level overlays on top of machine-level defaults

Code wins over docs. The current implementation already has pieces of this in:

- [`desktop/agent/env_profile.go`](desktop/agent/env_profile.go)
- [`desktop/agent/machine_onboarding.go`](desktop/agent/machine_onboarding.go)
- [`desktop/agent/machine_profile.go`](desktop/agent/machine_profile.go)
- [`desktop/agent/project_manifest.go`](desktop/agent/project_manifest.go)
- [`desktop/agent/workspace.go`](desktop/agent/workspace.go)

This doc explains what exists now, what is missing, and the target architecture.

## 1. Problem

One of the main reasons teams lean on GitHub Actions or GitLab CI for deploys is toolchain uniformity:

- every developer laptop does not need the same exact setup
- builds run in a known environment
- deploy CLIs and auth are centralized
- native/mobile release flows are less dependent on one specific box

Yaver wants to reduce CI cost by shifting more build/test/deploy work onto owned machines. That only works if Yaver can provide a stable toolchain model, not just "hope this box has the right binaries."

The core requirement is:

- Yaver should act like a portable virtual PC environment for developer tooling
- some toolchains should be machine-wide
- some should be project-specific overlays
- not every machine must install every tool
- machines should still be compatible enough that Yaver can move work intelligently

## 2. Current State

Yaver already has the start of a toolchain-share system.

### 2.1 Environment profile and sync

[`desktop/agent/env_profile.go`](desktop/agent/env_profile.go) already provides:

- machine environment snapshots via `EnvironmentProfile`
- remote profile fetch from another device
- compatible tool installation for a subset of common tools
- sync-store import for supported sync kinds
- optional git-credential import

This is exposed over:

- `GET /agent/env-profile`
- `POST /agent/env-profile/apply`
- `GET /agent/toolchain-sync/profile`
- `POST /agent/toolchain-sync/apply`
- `GET /agent/toolchain-sync/git-credentials`

Important current behavior:

- Yaver can clone a compatible host setup from another box
- installs are currently derived from discovered binaries and runner presence
- cross-platform clones are treated conservatively
- removal is only advisory today; OS package uninstall is not automated

This is useful, but it is still descriptive and host-centric.

### 2.2 Vault-backed onboarding

[`desktop/agent/machine_onboarding.go`](desktop/agent/machine_onboarding.go) already handles a portion of "new machine comes online with needed credentials":

- OpenAI key discovery
- GitHub token readiness
- GitLab token readiness
- clone-token and CI-token posture
- vault-backed storage for provider secrets

This gives Yaver a credential bootstrap story, but not a full toolchain policy story.

### 2.3 Machine capability hints

[`desktop/agent/machine_profile.go`](desktop/agent/machine_profile.go) already supports lightweight host hints:

- tags
- signatures
- preferred workload types

That is useful for placement, but it does not yet describe a managed toolchain contract.

### 2.4 Project and workspace intent

[`desktop/agent/project_manifest.go`](desktop/agent/project_manifest.go) and [`desktop/agent/workspace.go`](desktop/agent/workspace.go) already provide:

- project runtime intent
- deploy/runtime exports
- machine-role placement
- workspace-wide defaults
- shared env / secret expectations

These files are the natural place to add project-level and workspace-level toolchain declarations.

## 3. Main Gap

Today Yaver mostly answers:

- "what does this machine currently have?"
- "can I install some matching tools?"

It does not yet fully answer:

- "what toolchain should this machine have?"
- "what project capabilities must be portable across my fleet?"
- "which machines are compatible enough to continue a build or deploy flow?"
- "what should a new guest machine install automatically?"

That is the gap this architecture fills.

## 4. Target Model

Yaver should manage toolchains as first-class state with two layers:

1. machine-level toolchain
2. project-level toolchain overlay

The effective toolchain for a job becomes:

`effective toolchain = machine base + workspace defaults + project overlay + runner overlay`

This lets Yaver:

- keep a reliable baseline across many machines
- avoid installing everything everywhere
- choose the best compatible machine for deploy/build/test
- onboard new guests faster

## 5. Core Concepts

### 5.1 Toolchain profile

A toolchain profile should be declarative, not just observed.

Suggested shape:

```go
type ToolchainProfile struct {
    ID            string
    Scope         string // machine | workspace | project | guest-preset
    Name          string
    Description   string
    BaseProfiles  []string
    Bundles       []string
    Capabilities  []string
    VersionPins   map[string]string
    Env           map[string]string
    CredentialRefs []string
    Policy        ToolchainPolicy
}
```

This is the desired state Yaver plans against.

### 5.2 Toolchain bundle

A bundle is the portable install unit.

Suggested shape:

```go
type ToolchainBundle struct {
    ID            string
    Version       string
    Platforms     []string
    Tools         []ToolSpec
    Capabilities  []string
    Prerequisites []string
    CacheKey      string
    SizeBytes     int64
}
```

Examples:

- `core-web`
- `proof-video`
- `cloudflare-deploy`
- `convex-deploy`
- `react-native-js`
- `flutter-core`
- `android-build`
- `apple-release`

### 5.3 Capability

Yaver should plan against capabilities, not raw binary names.

Examples:

- `cloudflare-deploy`
- `convex-deploy`
- `github-ci`
- `gitlab-ci`
- `react-web-build`
- `react-native-hermes-build`
- `flutter-android-build`
- `testflight-upload`
- `play-internal-upload`
- `proof-video`
- `video-summary`

This is the correct abstraction for multi-machine placement.

## 6. What "Embed All Tools Into Yaver" Should Mean

Pragmatically, Yaver should embed or manage as much of the common CLI stack as possible, but not pretend every native SDK can be fully internalized.

Good candidates for Yaver-managed bundles:

- `git`
- `gh`
- `glab`
- `node`
- `pnpm`
- `bun`
- `wrangler`
- `convex`
- `ffmpeg`
- browser runtimes and drivers
- OpenCode / local coding helpers
- common web/test/build CLIs

Partially portable, host-prerequisite-heavy cases:

- Xcode and Apple release tooling
- Android SDK / emulator stack
- Flutter SDK
- Hermes/native React Native toolchain pieces

So "embedded" should mean:

- Yaver owns the install flow
- Yaver tracks versions and capabilities
- Yaver caches portable pieces
- Yaver knows what remains a host prerequisite

Not:

- one monolithic binary containing every platform SDK

## 7. Scopes

### 7.1 Machine-level toolchain

This is the base setup for a box.

Typical machine base:

- `core-web`
- `proof-video`
- `git`
- `gh`
- `node`
- `pnpm`
- `wrangler`
- `convex`

This should be enough for many web/deploy flows immediately after Yaver setup.

### 7.2 Workspace-level toolchain

A workspace can declare defaults that every app in the repo expects.

Examples:

- required deploy CLIs
- shared browser/test bundles
- standard Node line
- common credential refs

### 7.3 Project-level overlay

A project can add or tighten requirements for one app/repo.

Examples:

- React Native project requires Hermes support
- Flutter app requires Flutter SDK and Android capability
- iOS release path requires Apple signing + Xcode-ready host

This is optional, but critical for multi-app repos and guest onboarding.

## 8. Placement and Execution

Toolchain should plug directly into Yaver placement for:

- `ops`
- `code_deploy`
- `code_dev`
- graph nodes
- deploy/reload/build helpers

Planner inputs should include:

- required capabilities
- version constraints
- host OS/arch
- credential locality
- repo locality
- cached bundle presence
- prior-machine affinity
- host-share or guest ACL restrictions

This lets Yaver choose among:

1. run on current machine
2. run on another owned machine
3. install missing bundle then run
4. fall back to GitHub/GitLab CI

The intended bias is:

- prefer owned machines
- install portable bundles when reasonable
- use CI only when it is the best option

## 9. Guests and Team Onboarding

One of the strongest uses of toolchain share is onboarding.

When a new guest joins a workspace, Yaver should be able to resolve:

- which toolchain profiles are required
- which bundles are allowed for this guest
- which credentials are indirectly usable through host-share or local vault
- which project overlays are relevant

This should support flows like:

- "join this repo with web deploy capability"
- "join this mobile repo with simulator and proof-video capability"
- "join as deploy-only guest for Cloudflare and Convex"

The result should be close to:

- authenticate
- accept invite
- apply team/workspace toolchain preset
- get productive quickly

## 10. Manifest Extensions

The existing manifest surfaces are the right place to declare toolchain intent.

### 10.1 Workspace manifest extension

Suggested addition to [`yaver.workspace.yaml`](yaver.workspace.yaml):

```yaml
workspace:
  toolchain:
    base_profiles:
      - core-web
      - proof-video
    policy:
      auto_install: true
      allow_ci_fallback: true
      prefer_owned_machines: true
```

### 10.2 Project manifest extension

Suggested addition to `.yaver/project.yaml`:

```yaml
runtime:
  toolchain:
    profiles:
      - react-native-js
      - android-build
    capabilities:
      - play-internal-upload
      - react-native-hermes-build
    version_pins:
      node: "20.x"
      flutter: "3.29.x"
```

This lets Yaver plan against project requirements before deployment fails.

## 11. MCP and `yaver code`

Toolchain share should be visible through both MCP and `yaver code`.

Suggested MCP tools:

- `toolchain_profile_get`
- `toolchain_profile_apply`
- `toolchain_profile_diff`
- `toolchain_bundle_list`
- `toolchain_bundle_install`
- `toolchain_capabilities_explain`
- `toolchain_sync_from_device`
- `toolchain_sync_from_workspace`
- `toolchain_guest_preset_apply`

Suggested `yaver code` surfaces:

- `yaver code toolchain status`
- `yaver code toolchain apply`
- `yaver code toolchain install <bundle>`
- `yaver code toolchain explain deploy`
- `yaver code toolchain sync --from <device>`

The goal is for wrapped coding agents and human developers to use the same control plane.

## 12. Recommended Storage Model

Toolchain share should behave like vault in spirit, but not in payload.

What should sync broadly:

- desired profiles
- installed bundle metadata
- capability attestations
- version pins
- compatibility notes

What should not sync blindly:

- huge SDK payloads
- machine-local caches that are not portable
- secrets themselves

Instead:

- metadata syncs fleet-wide
- artifacts/bundles download on demand
- credentials remain in vault or machine-local secure stores

## 13. Current Code Changes Needed

The existing `env_profile.go` path is the natural base, but it needs to evolve from:

- detected binaries
- ad hoc install targets
- simple profile clone/apply

into:

- declarative toolchain profiles
- versioned bundle install plan
- capability-based placement signal
- workspace/project policy integration

High-level implementation areas:

1. Add `ToolchainProfile`, `ToolchainBundle`, and capability models.
2. Extend environment profile endpoints to report desired vs actual state.
3. Add workspace/project manifest parsing for toolchain declarations.
4. Feed toolchain capabilities into `ops` auto-placement and graph placement.
5. Expose toolchain actions in MCP and `yaver code`.
6. Tie guest onboarding and host-share presets into toolchain policy.

## 14. Key Design Rules

These constraints matter:

1. Capability-first, not binary-first.
2. Two-layer model: machine base plus project overlay.
3. CI is fallback, not default.
4. Secrets stay separate from tool payloads.
5. Portable bundles where possible; host prerequisites where necessary.
6. Placement should prefer compatibility and locality over arbitrary balancing.
7. Guest onboarding should consume the same toolchain contract as owner machines.

## 15. Summary

The right end state is:

- Yaver-managed portable toolchain bundles
- synced desired toolchain metadata across user/team machines
- workspace and project overlays
- capability-aware placement for build/test/deploy/graph execution
- quick guest onboarding into a usable fullstack environment
- reduced dependency on GitHub/GitLab CI for routine deploy and test work

Yaver already has the beginnings of this in environment profiles, onboarding, machine profiles, manifests, and placement-aware deploy flows. The missing step is to promote toolchain from "observed host setup" to "first-class portable control-plane state."
