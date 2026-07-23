# Yaver Desktop App Vibing Architecture Audit

Status: product and software architecture audit, 2026-07-21. Code is source of
truth; re-grep the referenced routes before implementation.

## Objective

Build one Yaver desktop app for macOS, Windows, and Linux using Electron.

The desktop app should feel like the richer desktop companion to the mobile app:
project discovery, P2P device pairing, Cloud Workspace control, WebRTC vibing,
Feedback SDK triage, Yaver Git, and Yaver Serverless deploys. It should not
feel like a cloud-provider console, simulator-management app, or low-level agent
debugger.

For normal users:

- open Yaver;
- sign in or pair;
- see projects and connected machines;
- click a project;
- start vibing;
- see live preview through Hermes/WebRTC/browser as appropriate;
- collect feedback from SDK users;
- deploy to Yaver Serverless;
- use Yaver Git without learning Git internals.

For advanced users:

- inspect tmux/autorun sessions;
- pick local vs Cloud Workspace execution;
- manage runner auth;
- inspect logs/doctor probes;
- open dev/runtime tools;
- export/self-host Yaver Serverless apps.

## Product Positioning

Yaver should have three first-class client surfaces:

| Surface | Primary job | What it should hide |
|---|---|---|
| Mobile app | Vibe from anywhere, voice-first tasks, project status, feedback, approvals | Provider routing, simulator internals, raw shell |
| Desktop app | Rich local control, project discovery, live preview, Git/Serverless workflows, Feedback SDK inbox | Cloud provider knobs, simulator setup complexity |
| Web dashboard | Account/admin/billing/team/device overview, browser fallback | Local native privilege and simulator control |

The desktop app is not a replacement for Cloud Workspace. It is the local trust
bridge and rich workstation surface. It should make local development pleasant
when available, and make Cloud Workspace feel native when local tools are absent.

Normal-user default:

- Cloud Workspace runs managed simulators, Redroid, serverless hosting, and
  remote runners.
- Desktop app shows the experience: project, task, preview, feedback, deploy.
- The user does not install Xcode/Android Studio/tmux/provider CLIs just to get
  started.

Advanced-user escape hatch:

- local machine can run the Go agent, simulators, tmux, runners, local
  serverless, and local Git;
- desktop app exposes details only behind developer tools.

## Current State In Repo

There are two Electron-ish desktop surfaces:

- `desktop/app`: richer Yaver desktop shell. It has auth, device selection,
  agent proxy IPC, tasks, runners, projects, dev server, preview, builds,
  guests, Git, health, quality, sandbox, and settings.
- `desktop/installer`: older installer/control-panel style. It installs/starts
  the Go agent, checks service state, handles onboarding/survey, tasks, and
  basic dashboard.

The real product capabilities are already in the Go agent and existing
web/mobile clients:

- P2P/auth/bootstrap/discovery: `desktop/agent/auth_bootstrap.go`,
  `desktop/agent/beacon.go`, `mobile/src/lib/beacon.ts`,
  `desktop/agent/agent_mesh_remote.go`.
- Devices and peer proxying: `/devices`, `/peer/<id>/...`, mesh/relay code.
- Project discovery: `/projects`, `/projects/refresh`, `/projects/mobile`,
  `/projects/web`, `/projects/all`, `/project/kind`, `/projects/actions`.
- Remote runtime/WebRTC: `/remote-runtime/capabilities`,
  `/remote-runtime/sessions`, `/remote-runtime/sessions/<id>/webrtc/offer`,
  `/stream/webrtc/offer`, `/stream/webrtc/ice`.
- Vibing/preview: `/vibing`, `/vibing/execute`, `/vibing/commit`,
  `/vibing/deploy`, `/vibing/preview/*`.
- Feedback SDK: `/feedback`, `/feedback/stream`, `/feedback-board`,
  `/feedback-work/config`, guest scopes and SDK tokens.
- Runner setup: `/agent/runners`, `/runner-auth/status`,
  `/runner-auth/browser/*`, `/machine/onboarding/status`,
  `/machine/onboarding/apply`.
- Git and forge: `/git/commit-push`, `desktop/agent/managed_git.go`,
  `desktop/agent/git_provider.go`, `desktop/agent/forge*.go`.
- Yaver Serverless/local app runtime seams: project runtime routes,
  companion/project manifest routes, phone backend and serverless-lite traces.
- Tmux/autorun tasks: `/tmux/sessions`, `/tmux/adopt`, `/tmux/input`,
  `/tmux/detach`, task metadata in `/tasks`.

The desktop app should not reimplement these capabilities in Node. Electron
should be a secure, native shell over the Go agent API plus local OS affordances.

## One Desktop App, Not Two

Converge `desktop/app` and `desktop/installer` into one product:

- `desktop/app` becomes the canonical app.
- Installer responsibilities move into the canonical app's onboarding flow:
  download agent, install service, start/stop service, repair service.
- `desktop/installer` becomes either deleted later or reduced to packaging-only
  code after parity is reached.

Reasons:

- Two Electron shells will drift on auth, agent version, routes, UI, and
  security policy.
- The installer shell has product logic that the real app also needs.
- A normal user should download one app, not an app plus a hidden agent manager.

## Architecture

Recommended shape:

```text
Electron Main Process
  - window lifecycle
  - tray/menu
  - auto-update
  - agent install/service supervision
  - OS permissions
  - secure IPC allowlist
  - native file/folder pickers
  - deep links and auth callback

Electron Renderer
  - React/Next/Vite UI shell
  - project/task/preview/feedback/deploy surfaces
  - no Node integration
  - no raw filesystem/process access

Preload Bridge
  - typed, narrow IPC API
  - no generic agentRequest for untrusted renderer code long-term
  - route allowlist and payload validation

Go Agent
  - auth, pairing, mesh/relay, P2P
  - runners, tmux, tasks, logs
  - project discovery
  - WebRTC/remote runtime
  - Feedback SDK host endpoints
  - Git/Yaver Git
  - Yaver Serverless local/cloud control
  - doctor/preflight/self-heal

Convex/Yaver Backend
  - account, devices, billing, teams
  - prompt-free dispatch metadata
  - cloud workspace/provider placement
  - SDK grants and feedback work metadata
```

Electron is the view/controller. The Go agent is the local runtime authority.
Convex is the account/control-plane authority. Cloud Workspace is a managed
remote runtime selected by policy.

## Main Surfaces

### 1. Home

Purpose: answer "what can I do right now?"

Show:

- signed-in user and current device;
- local agent state;
- Cloud Workspace state;
- selected/default project;
- active tasks/autoruns;
- recent feedback;
- deploy status;
- runner readiness.

Do not show:

- provider SKU lists;
- raw cloud resource ids;
- simulator installation internals;
- exhaustive logs.

User-facing states should be compact:

- `Ready`
- `Needs sign-in`
- `Waking Cloud Workspace`
- `Runner needs auth`
- `Preview running`
- `Feedback waiting`
- `Deploying`
- `Blocked`

### 2. Projects

Desktop should have the best project browser in Yaver.

Inputs:

- local project discovery from Go agent;
- Cloud Workspace project list;
- mobile phone-local projects;
- Yaver Git repositories;
- GitHub/GitLab linked repos;
- Feedback SDK project registrations;
- recent task/project affinities.

Project card fields:

- project name;
- source: `Local`, `Cloud Workspace`, `Phone`, `Yaver Git`, `GitHub`, `GitLab`;
- stack: React Native, web, Node, Go, Python, etc.;
- preview capability: Hermes, WebRTC, browser, static, none;
- feedback capability;
- serverless deploy state;
- dirty Git state as a simple label.

Normal actions:

- Open;
- Vibe;
- Preview;
- Feedback;
- Deploy;
- Sync;
- Share tester link.

Advanced actions:

- reveal path;
- open terminal;
- open in editor;
- inspect project manifest;
- configure runtime providers;
- export/self-host.

Important privacy rule:

- local paths can be displayed in the local desktop app;
- local paths should not be sent to Convex except where existing privacy
  contracts explicitly allow coarse labels.

### 3. Vibe Workspace

This is the core desktop experience.

Layout:

- left: conversation/task stream;
- center/right: live preview;
- lower/side panel: feedback, logs, diffs, deploy;
- top strip: project, machine, runner, inference, preview mode.

Top strip should show only:

- Project: `Todo App`
- Machine: `This Mac`, `Cloud Workspace`, or compact provider label if useful
- Runner: `Claude Code`, `Codex`, `OpenCode`, `BYO`
- Preview: `Hermes`, `WebRTC`, `Browser`, `Serverless`
- Deploy: `Local`, `Preview`, `Production`

Avoid a provider picker. Placement selects runtime.

### 4. Live Preview

Desktop should be the best surface for WebRTC vibing.

Preview modes:

- `Hermes`: React Native bundle pushed to native Yaver mobile runtime.
- `WebRTC`: live stream from iOS simulator, Android emulator, Redroid, browser,
  desktop screen, or Cloud Workspace runtime.
- `Browser`: embedded preview for web apps.
- `Serverless Preview`: app deployed/running on Yaver Serverless preview URL.
- `Static Artifact`: screenshots/videos generated by the agent.

Preview controls:

- reload;
- shake/feedback;
- rotate/device size;
- input tap/click;
- screenshot;
- record short clip;
- open in browser;
- copy share/tester link.

The renderer should not use generic third-party React Native WebViews for RN
apps. RN preview stays Hermes/native or WebRTC. Browser preview is only for web.

WebRTC expectations:

- use Go agent remote-runtime routes for session creation and signalling;
- support TURN/STUN via relay config;
- render stats: connected, bitrate, fps, latency, dropped frames;
- gracefully fall back to snapshot/JPEG preview with a clear "slow preview"
  label when H.264/WebRTC is unavailable;
- expose doctor probe when preview is slow or blank.

### 5. Feedback SDK Inbox

Desktop should make Yaver Feedback SDK useful to product owners.

Inbox views:

- new feedback;
- crashes/errors;
- screenshots/clips;
- user reproduction sessions;
- AI-suggested fixes;
- linked task/branch/deploy.

Actions:

- reproduce in preview;
- start fix task;
- assign to Cloud Workspace;
- ask runner to inspect logs;
- create Yaver Git branch;
- deploy preview;
- reply/mark resolved.

Security:

- SDK tokens are scoped;
- feedback-only users cannot access tasks/projects/builds;
- repo/project scoping must be visible to owner;
- prompts/output generated by fix tasks stay P2P/task-local unless explicitly
  saved as project artifacts.

### 6. Yaver Git

Desktop should present Git as "project history and sync," not as a raw Git UI.

Normal user actions:

- Save checkpoint;
- Compare changes;
- Sync project;
- Publish preview branch;
- Restore checkpoint;
- Invite collaborator/tester;
- Connect GitHub/GitLab.

Advanced actions:

- branch list/switch;
- commit message editor;
- conflict resolver;
- PR/MR creation;
- remote auth management;
- clone/import/export.

Yaver Git should work across:

- local project folder;
- Cloud Workspace volume;
- phone-local project via isomorphic-git;
- Yaver-managed Git backend;
- GitHub/GitLab/self-hosted remote.

Do not store Git provider tokens in Electron renderer state. Store through the
Go agent vault or OS credential store with explicit scopes.

### 7. Yaver Serverless

Desktop should make deploy feel native.

Surfaces:

- Deploy Preview;
- Production Deploy;
- Logs;
- Runtime env;
- Database browser/export/import;
- Domain/TLS;
- Scale-to-zero state;
- Self-host/export bundle.

Normal user flow:

1. Vibe app.
2. Preview app.
3. Click Deploy.
4. Choose `Preview` or `Production`.
5. Yaver handles package/build/runtime/database.
6. User sees URL and share controls.

Advanced flow:

- choose target: Cloud Workspace, Yaver-managed serverless, self-hosted machine;
- inspect generated runtime manifest;
- export app/database;
- configure custom domain;
- configure jobs/workers.

Yaver apps built by users should use Yaver Serverless, not Convex. Yaver itself
can continue using Convex for the control plane.

### 8. Devices And P2P Discovery

Desktop should have parity with mobile discovery, but in a desktop shape.

Device sources:

- local Go agent;
- LAN bootstrap beacon;
- Convex device list;
- relay/tunnel reachability;
- mesh peers;
- Cloud Workspace machines.

Device card:

- label;
- type: local desktop, phone, Cloud Workspace, shared machine;
- state: online/offline/waking/needs auth;
- connection: LAN, relay, mesh, Cloud Workspace;
- runner readiness;
- project capability summary.

Actions:

- pair/adopt;
- make primary;
- wake;
- diagnose;
- runner auth;
- open tasks;
- open project list.

Do not expose raw IPs/hostnames in public logs or docs. Local app can show local
network details behind advanced diagnostics.

### 9. Autorun/Tmux

Desktop should treat tmux/autorun as task infrastructure.

Normal UI:

- active autorun appears as task;
- show runner and status;
- show compact tmux label if relevant;
- allow follow-up and stop/detach.

Advanced UI:

- list tmux sessions;
- adopt/detach;
- session id/window/pane id;
- bounded pane preview;
- send input/menu choice.

The desktop app should share the same contract documented in
`docs/architecture/TMUX_AUTORUN_MOBILE_TASKS.md`.

## Desktop App For Normies

The desktop app should not ask normal users to manage simulators.

Default:

- "Preview app" chooses the best available runtime.
- If local tools are missing, use Cloud Workspace.
- If Cloud Workspace is sleeping, wake it.
- If runner auth is missing, show one clear auth action.
- If preview is unavailable, show one repair action.

Only advanced/dev tools should expose:

- iOS simulator selection;
- Android emulator/Redroid internals;
- WebRTC codec details;
- tmux panes;
- ports;
- raw logs;
- provider/region/SKU.

## Electron Security Requirements

Current `desktop/app` is useful but too permissive for the final app:

- `webviewTag: true`;
- CSP allows `connect-src *`, `img-src *`, `frame-src *`;
- preload exposes a generic `agentRequest(method, path, body)`.

Target:

- `nodeIntegration: false`;
- `contextIsolation: true`;
- `sandbox: true` where compatible;
- no generic renderer-to-agent proxy for untrusted routes long-term;
- typed IPC methods grouped by domain;
- route allowlist in main process;
- payload validation per method;
- no secrets returned to renderer unless strictly necessary;
- no provider credentials in renderer;
- no third-party content with privileged preload;
- WebRTC/browser preview isolated from app-control context;
- explicit external URL handling through `shell.openExternal` allowlist;
- `webSecurity` stays enabled;
- no broad `connect-src *` in production.

Suggested bridge modules:

- `auth`;
- `devices`;
- `projects`;
- `tasks`;
- `preview`;
- `feedback`;
- `git`;
- `serverless`;
- `workspace`;
- `diagnostics`;
- `settings`.

Avoid:

- `window.yaver.agentRequest("POST", arbitraryPath, arbitraryBody)`;
- renderer access to `fs`, `child_process`, or raw tokens;
- storing bearer tokens in plain JSON if OS keychain is available.

## Agent Supervision

Electron should own user-friendly supervision, not runtime logic.

Responsibilities:

- install/update Go agent;
- start/stop/restart service;
- show version mismatch;
- collect doctor results;
- expose logs;
- repair service permissions;
- manage tray/menu state;
- deep-link auth callback.

Platform details:

- macOS: LaunchAgent, Developer ID signing/notarization, keychain where needed,
  permissions prompts for screen/audio if preview capture uses local desktop.
- Windows: Windows Service or user-startup process, WSL awareness, firewall
  prompt handling, code signing.
- Linux: systemd user service when available, AppImage/deb packaging, Wayland
  screen-capture constraints, distro package manager variance.

The app should never silently run destructive cloud/provider operations.

## UI Information Architecture

Recommended navigation:

- Home
- Projects
- Vibe
- Feedback
- Deploys
- Devices
- Git
- Tasks
- Settings

Advanced drawer:

- Tmux
- Logs
- Doctor
- Runtimes
- Network
- Vault
- Cloud Workspace internals

The first viewport after onboarding should be the usable workspace, not a
marketing landing page.

## Data Model Needed In Desktop UI

Use shared typed DTOs, ideally generated or copied from a single TS package.

Core DTOs:

- `DeviceSummary`
- `ProjectSummary`
- `TaskSummary`
- `TmuxSession`
- `RemoteRuntimeCapabilities`
- `RemoteRuntimeSession`
- `FeedbackItem`
- `YaverGitStatus`
- `ServerlessApp`
- `ServerlessDeployment`
- `CloudWorkspaceSummary`
- `RunnerReadiness`
- `DoctorFinding`

Do not create Electron-only DTOs when mobile/web already define compatible
shapes. The desktop app should consume the same agent API contracts as mobile
and web.

## Yaver Serverless Desktop Contract

Minimum app-level contract:

```ts
type ServerlessTarget = "preview" | "production" | "self-hosted";

type ServerlessAppSummary = {
  id: string;
  projectSlug: string;
  name: string;
  runtime: "node" | "static" | "worker" | "container";
  target: ServerlessTarget;
  status: "not-deployed" | "building" | "running" | "asleep" | "failed";
  url?: string;
  lastDeployAt?: number;
  database?: {
    kind: "sqlite" | "postgres" | "kv" | "none";
    exportable: boolean;
  };
};
```

The desktop app should be able to:

- ask the agent/backend for deploy readiness;
- trigger preview deploy;
- trigger production deploy;
- stream build/deploy logs;
- open deployed URL;
- export app bundle and database;
- wake/sleep preview runtime;
- show quota/cost state.

## WebRTC Desktop Contract

Minimum UI contract:

```ts
type PreviewMode = "hermes" | "webrtc" | "browser" | "serverless" | "snapshot";

type PreviewSession = {
  id: string;
  projectSlug: string;
  mode: PreviewMode;
  targetLabel: string;
  status: "starting" | "connected" | "slow" | "failed" | "closed";
  latencyMs?: number;
  fps?: number;
  bitrateKbps?: number;
  fallbackReason?: string;
};
```

Desktop should:

- negotiate WebRTC through the Go agent;
- isolate the preview frame from privileged app IPC;
- show slow/failed preview diagnostics;
- support direct control events where the target allows it;
- record bounded clips for feedback.

## Feedback SDK Desktop Contract

Minimum UI contract:

```ts
type FeedbackInboxItem = {
  id: string;
  projectSlug: string;
  status: "new" | "triaged" | "fixing" | "fixed" | "closed";
  source: "sdk" | "guest" | "owner" | "crash";
  title: string;
  screenshotUrl?: string;
  clipUrl?: string;
  taskId?: string;
  branch?: string;
  deployUrl?: string;
  createdAt: number;
};
```

Desktop should make "fix this feedback" a one-click task:

1. create scoped fix task;
2. open project preview;
3. create Yaver Git branch/checkpoint;
4. run agent;
5. show diff;
6. deploy preview;
7. mark feedback resolved.

## Yaver Git Desktop Contract

Minimum UI contract:

```ts
type YaverGitStatus = {
  projectSlug: string;
  branch?: string;
  ahead?: number;
  behind?: number;
  dirty: boolean;
  changedFiles: number;
  conflicts: string[];
  remotes: Array<{ name: string; kind: "yaver" | "github" | "gitlab" | "other" }>;
};
```

Normal UI should say:

- `Saved`
- `Unsaved changes`
- `Needs sync`
- `Conflict needs review`
- `Published`

Raw Git details belong in advanced mode.

## Packaging And Distribution

Target:

- macOS: signed/notarized DMG and zip;
- Windows: signed NSIS installer;
- Linux: AppImage and deb first;
- auto-update later, after code-signing and rollback policy are stable.

Open question:

- current project rule says CLI distribution is npm-only. Desktop app can be a
  separate end-user product, but shipping channels must be explicitly decided
  before release. Do not quietly add Homebrew/apt/Scoop/etc. for the CLI.

Desktop app should install/manage the Go agent from a signed release artifact,
not build from source on the user's machine.

## Implementation Plan

### Phase 0: Audit And Freeze Contracts

- Decide canonical Electron folder: likely `desktop/app`.
- Inventory routes used by mobile/web that desktop must share.
- Add typed `desktop/app/src/shared/agentTypes.ts` or import/generate shared
  types.
- Write route allowlist for preload/main IPC.
- Define product nav and first screen.

### Phase 1: Secure Shell

- Replace broad renderer bridge with typed IPC methods.
- Tighten CSP.
- Remove `webviewTag` from privileged app surface; use isolated BrowserView or
  safer preview container for web content.
- Store desktop auth using OS keychain where possible.
- Keep Go agent token handling in main process.

### Phase 2: Agent Install And Pairing

- Merge installer capabilities into canonical app.
- Add install/start/repair flow.
- Show local agent state.
- Support bootstrap beacon/adopt pairing.
- Support Convex device list and relay/mesh status.

### Phase 3: Projects And Vibe

- Project browser from `/projects/*`.
- Vibe workspace shell.
- Task stream with runner/model labels.
- Preview panel with browser/Hermes/WebRTC modes.
- Cloud Workspace wake/placement state.

### Phase 4: Feedback And Git

- Feedback SDK inbox.
- Feedback-to-fix workflow.
- Yaver Git status/checkpoint/sync.
- Branch/deploy preview linkage.

### Phase 5: Yaver Serverless

- Deploy readiness.
- Preview deploy.
- Production deploy.
- Logs.
- Env/database/domain.
- Export/self-host.

### Phase 6: Advanced Tools

- Tmux/autorun sessions.
- Runtime diagnostics.
- WebRTC stats.
- Doctor findings.
- Vault and runner auth.
- Provider internals behind admin/dev flag only.

## Tests And Probes

Required tests:

- Electron main IPC route allowlist unit tests.
- Preload API shape tests.
- Renderer smoke test for Home/Projects/Vibe/Feedback/Deploy.
- Go agent route smoke tests for every desktop critical path.
- WebRTC preview smoke with blank-frame detection.
- Feedback SDK scoped-token tests.
- Yaver Git token redaction tests.
- Serverless deploy dry-run tests.
- Cross-platform service install tests with mocks.

Required doctor probes:

- desktop app can reach local agent;
- local agent version matches app expectation;
- relay/mesh/direct status;
- WebRTC preview capability;
- runner auth state;
- project discovery scan bounded by deadline;
- serverless deploy readiness;
- Git credential readiness;
- Feedback SDK token scope readiness.

## Risks

- Electron becomes a second backend if Node starts implementing product logic.
  Keep logic in Go agent.
- Broad IPC or webview privileges can turn preview content into local code
  execution. Lock down bridge and isolate previews.
- Simulator/Redroid controls can overwhelm normies. Hide by default.
- Provider details can leak into product UX. Show compact machine labels only.
- Two desktop shells can diverge. Converge early.
- WebRTC performance can regress silently. Add stats and blank-frame probes.
- Local paths/secrets can leak through Convex if desktop reuses mobile metadata
  carelessly. Keep privacy boundary explicit.

## Decision Summary

- Use Electron for macOS/Linux/Windows desktop app.
- Keep Go agent as runtime authority.
- Converge to one canonical desktop app.
- Desktop app should mirror mobile's core surfaces but optimize for rich
  workspace use: project browser, live preview, feedback inbox, deploys, Git.
- Normal users should not manage simulators, provider infra, tmux, ports, or
  runners unless something needs authorization.
- Cloud Workspace and Yaver Serverless should feel native inside desktop.
- Advanced tools remain available, but behind a clear developer/diagnostics
  layer.
