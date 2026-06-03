# Talos + Yaver Mode on a Hetzner Host

## Status

Design analysis for making Yaver a first-class execution mode inside Talos
web/desktop/mobile chat surfaces.

This is not a new AI provider and not a resale path for Codex or Claude Code.
Yaver remains the control plane for the user's own authenticated machines,
toolchains, MCP servers, and coding runners. Talos is a domain app that can
call into that control plane.

## Goal

Talos users should be able to open a Talos chat surface, switch to **Yaver
mode**, and ask for work that runs on a selected Hetzner or desktop machine:

- app changes in Talos ERP
- Convex/backend changes
- web UI changes with live preview
- robotics and harness workflows
- OpenSCAD/CAD generation and rendering
- robot trial orchestration through Talos MCP and Yaver c-agent paths

The remote machine should run:

- `yaver serve`
- the Talos repo and relevant worktrees
- the Talos MCP server
- the Yaver MCP server or local Yaver HTTP control plane
- user-authenticated runners such as Codex, Claude Code, OpenCode, or local
  models
- toolchains such as Node, Convex CLI, OpenSCAD, Python/OpenCV, ROS/Parol
  utilities, and any rendering tools

The Talos UI should show typed progress, previews, artifacts, runner auth
state, and recovery actions without asking the user to SSH into the box.

## Tenant Deployment Model

For Talos, the target shape is **one isolated runtime lane per company**:

```text
Talos company / tenant
  ├─ Convex deployment or isolated Convex namespace
  ├─ dedicated compute resource
  │    ├─ Hetzner server by default
  │    ├─ AWS/GCP/Azure/on-prem alternative
  │    └─ optional extra resource for GPU/local model/robot lab
  ├─ Yaver agent running on the compute resource
  ├─ Talos repo/workspace checked out on that resource
  ├─ Talos MCP server exposed to runners on that resource
  ├─ Yaver MCP/control-plane tools
  └─ company AI policy and runner configuration
```

Dedicated compute should be treated as a **tenant runtime**, not merely a
developer machine. It can still be reachable through the same Yaver device
model, but Talos must know it belongs to a company and must enforce company
policy before allowing a chat task to run there.

The provider should be pluggable:

```ts
type TenantComputeProvider =
  | "hetzner"
  | "aws"
  | "gcp"
  | "azure"
  | "onprem"
  | "byo-yaver-device";
```

Hetzner is the default because it is cheap and predictable, but the Yaver mode
contract must not depend on Hetzner-specific APIs. Provisioning and lifecycle
can be provider-specific; runtime execution should only require a reachable
Yaver agent with the right capabilities.

## Company AI Options

Talos needs a **company-level AI configuration**. Existing Yaver
`userSettings.primaryRunnerByDevice` is useful for individual preference, but
it is not enough for Talos tenants because it cannot express:

- which runner a company allows
- whether the company uses Yaver-managed compute or BYO/on-prem compute
- whether Codex/Claude/OpenCode credentials are company-owned or user-owned
- which MCP servers are mounted for the company
- which data classes are allowed to leave Talos/Convex/compute
- whether robotics actions require human approval
- per-role task permissions

Recommended Talos-side model:

```ts
type CompanyAIOptions = {
  companyId: string;

  runtime: {
    mode: "dedicated-compute" | "bring-your-own-yaver" | "local-only";
    defaultProvider: TenantComputeProvider;
    defaultDeviceId?: string;
    fallbackDeviceIds?: string[];
    region?: string;
  };

  convex: {
    deploymentKind: "dedicated" | "shared-isolated" | "external";
    deploymentName?: string;
    siteUrl?: string;
    envName: "production" | "candidate" | "development" | string;
  };

  runners: {
    defaultRunner: "codex" | "claude" | "opencode" | "ollama" | string;
    allowedRunners: string[];
    defaultModelByRunner?: Record<string, string>;
    allowUserOverride: boolean;
    requireRunnerAuthPerUser: boolean;
    credentialMode:
      | "user-auth-on-runtime"
      | "company-api-key-on-runtime"
      | "local-model-on-runtime"
      | "external-onprem-endpoint";
  };

  opencode?: {
    providers: Array<{
      id: string;
      label: string;
      baseUrl?: string;
      models: string[];
      keyPolicy: "company-secret" | "user-secret" | "none";
    }>;
    defaultAgent?: "build" | "plan" | string;
  };

  mcp: {
    enabledServers: Array<"talos" | "yaver" | string>;
    requiredServers: Array<"talos" | "yaver" | string>;
    toolPolicyByRole?: Record<string, string[]>;
  };

  workKinds: {
    appCode: boolean;
    erpFlow: boolean;
    convex: boolean;
    webUi: boolean;
    harnessCad: boolean;
    openScadCad: boolean;
    robotTrial: boolean;
    inspection: boolean;
  };

  approvals: {
    requireApprovalForProductionWrites: boolean;
    requireApprovalForDeploy: boolean;
    requireApprovalForRobotMotion: boolean;
    requireApprovalForSecretsAccess: true;
  };

  dataPolicy: {
    allowCustomerDataInPrompts: boolean;
    allowScreenshotsInPrompts: boolean;
    allowTelemetryInPrompts: boolean;
    redactPII: boolean;
    retentionDays: number;
  };
};
```

This model should live in Talos' company settings and be projected into Yaver
mode at execution time. Yaver should store only the minimum metadata needed for
device reachability and user auth. Secrets stay on the runtime machine, in the
company vault, or in the provider's own secret store.

### Company AI Options UI

Talos should add a settings surface:

```text
Company Settings
  └─ AI & Automation
       ├─ Runtime
       │    ├─ Dedicated Hetzner / AWS / On-prem
       │    ├─ Yaver device binding
       │    ├─ region / size / lifecycle
       │    └─ health + reconnect
       ├─ Runners
       │    ├─ Codex
       │    ├─ Claude Code
       │    ├─ OpenCode
       │    ├─ Ollama/local model
       │    └─ custom/on-prem endpoint
       ├─ MCP
       │    ├─ Talos MCP enabled
       │    ├─ Yaver MCP enabled
       │    └─ tool permissions by role
       ├─ Workflows
       │    ├─ ERP app changes
       │    ├─ Convex/backend changes
       │    ├─ web UI preview
       │    ├─ harness CAD/OpenSCAD
       │    └─ robot trials
       ├─ Data policy
       │    ├─ customer data in prompts
       │    ├─ screenshots
       │    ├─ telemetry
       │    └─ retention/redaction
       └─ Approvals
            ├─ production writes
            ├─ deploys
            └─ robot motion
```

The UI must distinguish three auth planes:

1. **Talos user/company auth**
   Who is allowed to use Yaver mode and which company they act under.

2. **Yaver runtime auth**
   Whether the dedicated machine is connected to Yaver and owned/claimed by
   the right company admin or service identity.

3. **Runner/provider auth**
   Whether Codex/Claude/OpenCode/local model endpoints are available on that
   runtime.

The user-facing health row should look like:

```text
Runtime: hetzner-fsn1-talos-acme        reachable via relay
Yaver:   signed in as acme-admin        healthy
Runner:  Codex                          needs sign-in
MCP:     Talos + Yaver                  ready
Convex:  acme-prod                      connected
Policy:  robot motion requires approval enabled
```

## Company Runtime Identity

There are two viable identity models.

### Model A: company admin owns the Yaver device

The dedicated Hetzner box is a normal Yaver device owned by the company admin
or technical owner. Talos stores `defaultDeviceId` for the company.

Pros:

- works with current Yaver device ownership
- runner auth flows already work
- quickest implementation

Cons:

- company lifecycle is tied to one human owner unless carefully managed
- offboarding the owner needs explicit transfer

### Model B: company service identity owns the Yaver device

Talos/Yaver create a service identity for the company and the runtime signs in
as that identity. Human users act through Talos permissions, not direct device
ownership.

Pros:

- clean company lifecycle
- better audit story
- safer when admins change

Cons:

- requires first-class service-account/session model
- Yaver UI and Convex ownership checks must understand company ownership, not
  only user ownership

Recommended path:

1. Start with Model A for dogfooding and early tenants.
2. Add Model B before selling this as a multi-tenant company feature.

Do not fake service ownership by sharing one human session token across users.
That breaks auditability and makes runner credential responsibility unclear.

## Runtime Provisioning Flow

For each Talos tenant:

1. Create or select Convex deployment/namespace.
2. Provision compute:
   - Hetzner first
   - other provider or on-prem later
3. Install `yaver-cli` and start `yaver serve`.
4. Authenticate Yaver:
   - company admin claim for Model A
   - company service identity for Model B
5. Register device capabilities:
   - `talos-runtime`
   - `convex`
   - `browser`
   - `node`
   - `openscad`
   - `robotics` when applicable
6. Clone/sync Talos workspace.
7. Configure Talos MCP server.
8. Configure runner policy:
   - Codex device auth
   - Claude Code OAuth
   - OpenCode provider config
   - Ollama/local endpoint
   - external on-prem model endpoint
9. Run preflight and store health in Talos company settings.

The tenant should not be marked "AI ready" until all required policy checks pass.

## Resolver API

Talos web UI, mobile app, desktop app, and headless/MCP callers should not
duplicate Yaver runtime selection rules. They should call Yaver before starting
a Yaver-mode chat/job:

```http
POST /company-ai/resolve
Authorization: Bearer <yaver-token>
Content-Type: application/json
```

```json
{
  "teamId": "team_xxx",
  "workKind": "openscad-cad",
  "requestedRunner": "opencode",
  "requestedModel": "optional",
  "requestedDeviceId": "optional",
  "source": "talos-web"
}
```

Supported `workKind` values:

- `app-code`
- `erp-flow`
- `convex`
- `web-ui`
- `harness-cad`
- `openscad-cad`
- `robot-trial`
- `inspection`

The response includes selected runtime provider/device, runner/model policy,
MCP required servers, approval requirements, prompt hints, artifact kinds,
dispatch paths, and setup flags such as `configureRuntimeDevice` and
`reauthRunner`. It returns no secrets.

Talos should use this response as the shared contract for web, mobile, desktop,
and headless flows. If `nextActions.reauthRunner` is true, or if
`/agent/runners` reports the selected runner is not authenticated, Talos should
launch the existing Yaver runner OAuth/browser/device reauth flow for the
selected runtime. Talos should not collect raw provider keys in chat.

## Existing Yaver Surfaces To Reuse

### Remote agent connection

Yaver already has direct/relay/tunnel connection handling. The web dashboard
client can connect to a device, route through relay URLs, and issue HTTP calls
to the machine-side agent.

Useful current surfaces:

- `web/lib/agent-client.ts`
- `web-headless/src/web-client.ts`
- `mobile/src/lib/quic.ts`
- `docs/headless-clients.md`

### Task dispatch

The web client already creates tasks with runner, model, project, and workDir:

```ts
await agentClient.createTask({
  title,
  description,
  userPrompt,
  runner,
  model,
  projectName,
  workDir,
  videoEnabled,
  askMode,
});
```

This is the minimal wire shape Talos can use for general app work.

### Runner auth and reauth

Yaver already supports browser-auth flows for Codex and Claude Code on the
target machine, including peer routing for a different remote device:

- `/runner-auth/browser/start`
- `/runner-auth/browser/status`
- `/runner-auth/browser/submit-code`
- `/runner-auth/status`
- `/peer/<deviceId>/runner-auth/browser/*`

This is critical for Hetzner. The Talos UI should not tell the user to SSH and
run `codex login` or `claude auth login`; it should start the Yaver runner auth
flow and show the URL/code.

### Yaver agent reauth

Yaver already supports agent recovery through `/auth/recover`. Web/mobile can
hand a fresh session to a remote box over relay when the agent auth is stale.
Talos should surface this as a blocking recovery CTA when the selected Hetzner
host is reachable but not Yaver-authed.

### MCP and graph execution

Yaver already exposes graph and mesh tools:

- `agent_machine_inventory`
- `agent_graph_start`
- `agent_graph_show`
- `code_mesh_start`

Graph nodes already accept:

- `allowed_devices`
- `allowed_runners`
- `resource_modes`
- `preferred_video_mode`
- placement hints like `prior_device`, `sticky_device`, `prior_runner`

This is the right foundation for multi-step Talos work: plan, implement,
render/test, verify, summarize.

### Remote MCP proxy

`desktop/agent/mcp_remote_proxy.go` can forward MCP tool calls to another
Yaver-owned agent by `device_id`, while blocking secrets/vault tools from being
proxied. Talos should respect that boundary: domain tools can be proxied;
secrets stay local to the target machine or vault owner flow.

## Product Model

### Yaver mode

Yaver mode is a chat execution mode in Talos:

```ts
type TalosChatMode = "normal" | "yaver";
```

In `normal` mode, Talos chat answers from Talos' usual app-side AI context.

In `yaver` mode, chat creates or continues a remote Yaver session:

```ts
type YaverModeSession = {
  id: string;
  app: "talos";
  project: string;
  deviceId: string;
  deviceAlias?: string;
  runner: "codex" | "claude" | "opencode" | "ollama" | string;
  model?: string;
  workDir: string;
  mcpServers: Array<"yaver" | "talos" | string>;
  status:
    | "checking-device"
    | "needs-yaver-auth"
    | "needs-runner-auth"
    | "ready"
    | "running"
    | "blocked"
    | "completed"
    | "failed";
};
```

The selected Hetzner machine is just a Yaver device with a known role:

```ts
type YaverDeviceRole =
  | "primary-dev"
  | "talos-hetzner"
  | "robotics-brain"
  | "render-host"
  | "build-host";
```

Do not special-case Hetzner throughout the app. Model it as a machine role and
capability set.

## Configuration Resolution

Talos should resolve Yaver mode configuration with explicit precedence.

Recommended order:

```text
1. hard safety policy
2. company AI policy
3. workspace/project policy
4. user preference
5. chat-message override
```

Safety policy always wins. A user cannot override:

- disallowed runner
- disallowed model endpoint
- robot-motion approval
- production-write approval
- secrets locality
- tenant data policy

Company policy wins over user preference. For example, if Acme allows only
`opencode` with an on-prem model endpoint, the chat composer must not offer
Claude Code even if the individual user's Yaver account has Claude Code signed
in on the same machine.

Workspace/project policy narrows company policy. For example:

```yaml
# talos/.talos/ai.yaml
yaverMode:
  workKinds:
    harnessCad: true
    robotTrial: false
  requiredMcp:
    - talos
    - yaver
  defaultWorkDir: /workspace/talos
  requiredTools:
    - node
    - convex
    - openscad
```

User preference can pick among allowed choices:

- preferred runner among allowed runners
- preferred model among allowed models
- preferred runtime among allowed devices
- OpenCode mode/agent such as `build`, `plan`, `review`

Chat override is per-message and must be validated:

```text
"Use Codex on the Hetzner robotics box"
```

This is accepted only if:

- Codex is company-allowed
- the selected box is company-bound
- the user role can run that work kind
- Codex is authenticated or can be authenticated by this user/admin

## Policy-Resolved Session Shape

Before executing a task, Talos should produce a resolved session object. This
is the single payload the UI, audit log, and Yaver call agree on:

```ts
type ResolvedYaverModeSession = {
  companyId: string;
  userId: string;
  role: "owner" | "admin" | "engineer" | "operator" | "viewer" | string;

  runtime: {
    deviceId: string;
    deviceAlias?: string;
    provider: TenantComputeProvider;
    origin: "managed" | "self-hosted" | "onprem";
    region?: string;
    connectionMode?: "relay" | "direct" | "tunnel";
  };

  repo: {
    workDir: string;
    branchPolicy: "candidate" | "direct-dev" | "read-only";
    candidateLane?: string;
  };

  convex: CompanyAIOptions["convex"];

  runner: {
    id: string;
    model?: string;
    mode?: string;
    provider?: string;
    credentialMode: CompanyAIOptions["runners"]["credentialMode"];
  };

  mcp: {
    servers: string[];
    allowedTools: string[];
  };

  policy: {
    workKind: TalosYaverPromptPackage["workKind"];
    approvalsRequired: string[];
    dataPolicy: CompanyAIOptions["dataPolicy"];
    expectedArtifacts: string[];
  };
};
```

All UI actions should render against this resolved object. If it cannot be
resolved, the chat composer should show exactly what is missing.

Example blocking states:

```text
Company AI is not configured.
No Yaver runtime is bound to this company.
The selected runtime is offline.
Yaver auth expired on the runtime.
Codex is allowed but not authenticated on this runtime.
Your role can review robot trials but cannot start robot motion.
OpenSCAD is required for harness CAD but missing on this runtime.
```

## Company AI UI Wiring

### Settings page

The company AI settings page is the source of truth for defaults and policy.

Required controls:

- Runtime provider selector: Hetzner, AWS, on-prem, existing Yaver device
- Bound runtime list with health
- Default runtime per work kind
- Runner allowlist
- Default runner/model per work kind
- OpenCode provider catalog and key policy
- MCP server enablement and role permissions
- Data policy toggles
- Approval toggles
- Toolchain checks

### Chat composer

The Talos chat composer should show a compact Yaver mode bar:

```text
Yaver mode · Acme Robotics · hetzner-fsn1 · Codex · Talos+Yaver MCP
```

Expandable details:

- runtime health
- runner auth
- selected work kind
- expected artifacts
- approvals required

Actions:

- Change runtime
- Change runner/model
- Sign in runner
- Reconnect runtime
- Open progress
- Open artifacts

The composer should never expose a free-form "provider URL + key" input unless
the company policy allows user-provided providers. For most companies, provider
configuration belongs in Company Settings, not in every chat.

### Admin/operator split

Admins can:

- provision/bind runtimes
- configure allowed runners
- connect company-level provider keys
- enable Talos/Yaver MCP tools
- change data policies
- approve production promotions

Engineers can:

- run app/code/Convex tasks within policy
- select among allowed runners
- trigger preview/candidate builds
- review artifacts

Operators can:

- ask operational questions
- request harness/robot trial plans
- review previews/artifacts
- start robot trials only when explicitly allowed

Viewers can:

- see summaries and artifacts
- cannot start runner tasks

## Runner Credential Modes

Yaver mode needs to make credential ownership explicit.

### `user-auth-on-runtime`

Codex/Claude Code OAuth runs on the tenant runtime, but each human user signs
in with their own account where the provider supports it.

Use when:

- the company lets engineers use personal subscriptions/accounts
- audit should show which human ran the task
- the runner CLI stores per-user auth on the runtime

Risk:

- multi-user runtime needs strong Unix-user/session isolation or the runner
  credentials can blur together.

### `company-api-key-on-runtime`

The company stores an API key or provider key on the runtime. OpenCode or a
custom runner uses it.

Use when:

- the company pays centrally
- users should not connect personal accounts
- the provider supports service/API-key usage

Rule:

- never store the key in Convex plaintext
- never send it through Talos chat messages
- UI can show only configured/not configured

### `local-model-on-runtime`

The runtime uses Ollama/vLLM/LM Studio or another local/on-prem endpoint.

Use when:

- customer data must stay on the tenant resource
- the company has GPU/on-prem model infrastructure
- internet egress is restricted

Talos should test:

- endpoint reachable from runtime
- model list
- context length/throughput
- fallback model

### `external-onprem-endpoint`

The runner calls an on-prem model endpoint not hosted on the same runtime.

Use when:

- the company already has an internal AI gateway
- Yaver runtime is just the tool executor

Talos/Yaver should store only endpoint metadata and secret references, not
plaintext credentials.

## Tenant Runtime vs Personal Yaver

Personal Yaver and Talos company Yaver must stay separate in the UI.

Personal Yaver:

- the user's own devices
- user's own runner preferences
- personal projects
- userSettings-level primary runner/device

Company Yaver mode:

- company-bound runtime devices
- company policy
- company audit log
- company Convex/data boundaries
- role-based access

It is acceptable for one physical machine to appear in both worlds only if the
agent can enforce workspace, credential, and access separation. For company
tenants, prefer a dedicated runtime.

## Session Lifecycle

### 1. Resolve target

Talos asks Yaver for devices and picks the preferred Talos host:

- explicit user choice
- saved project default
- primary device
- capability-based selection, e.g. `resource_modes: ["browser", "openscad"]`

Required UI data:

- device name/alias
- online/reachable state
- transport path: relay/direct/tunnel
- Yaver auth state
- runner auth rows
- toolchain capability rows

### 2. Preflight

Before sending the prompt to Codex/Claude, Yaver mode checks:

- Yaver agent reachable
- Yaver agent authed
- requested runner installed
- requested runner authenticated
- Talos repo exists at `workDir`
- Talos MCP server is available
- required toolchains exist for the selected work kind

Example domain requirements:

- general app work: Node, package manager, Git
- Convex work: Convex CLI and env
- OpenSCAD work: `openscad`, optional mesh tools
- robotics work: Talos MCP, c-agent/robot bridge, Python/OpenCV/ROS/Parol tools

### 3. Recover if needed

If Yaver auth is stale:

- call Yaver reauth flow
- show progress as "Re-authorizing remote Yaver agent"
- resume preflight after success

If runner auth is stale:

- call `startRunnerBrowserAuth(runner, deviceId)`
- show URL/code from status polling
- after completion, re-check `/runner-auth/status`

This should be surfaced as part of the same chat flow, not as a separate
settings-only feature.

### 4. Build prompt package

Talos should not send the raw user message alone. Yaver mode should build a
prompt package:

```ts
type TalosYaverPromptPackage = {
  userPrompt: string;
  app: "talos";
  workKind:
    | "app-code"
    | "erp-flow"
    | "convex"
    | "web-ui"
    | "harness-cad"
    | "robot-trial"
    | "inspection";
  repo: {
    workDir: string;
    branch?: string;
    relevantPaths?: string[];
  };
  mcp: {
    requiredServers: string[];
    preferredTools: string[];
  };
  constraints: string[];
  expectedArtifacts: string[];
  verification: string[];
};
```

### 5. Execute

For simple work, use `/tasks`.

For broad work, use `agent_graph_start` or `code_mesh_start` with explicit
nodes. Examples:

```json
{
  "prompt": "Add a supplier invoice approval flow",
  "work_dir": "/workspace/talos",
  "allowed_devices": ["hetzner-primary"],
  "allowed_runners": ["codex"],
  "nodes": [
    {
      "id": "plan",
      "title": "Plan ERP flow",
      "kind": "chat",
      "resource_modes": ["browser"],
      "design_points": 2
    },
    {
      "id": "implement",
      "title": "Implement Talos changes",
      "kind": "autodev",
      "depends_on": ["plan"],
      "resource_modes": ["build"]
    },
    {
      "id": "verify",
      "title": "Run checks and capture preview",
      "kind": "autotest",
      "depends_on": ["implement"],
      "resource_modes": ["browser", "proof-video"],
      "preferred_video_mode": "browser"
    }
  ]
}
```

### 6. Stream progress

Talos should render typed progress events rather than terminal blobs only.

Minimum event model:

```ts
type YaverModeEvent =
  | { type: "phase"; phase: string; label: string; at: number }
  | { type: "runner"; runner: string; status: string; detail?: string }
  | { type: "tool"; server: "talos" | "yaver"; tool: string; status: string }
  | { type: "artifact"; artifact: YaverArtifact }
  | { type: "preview"; url: string; kind: "web" | "image" | "stl" | "video" }
  | { type: "question"; questionId: string; prompt: string; kind: string }
  | { type: "done"; status: "completed" | "failed" | "blocked" };
```

Keep terminal output available as a secondary detail pane.

### 7. Return artifacts

Everything generated by a remote Hetzner session should come back as
structured artifacts:

```ts
type YaverArtifact = {
  id: string;
  kind:
    | "diff"
    | "log"
    | "web-preview"
    | "image"
    | "video"
    | "openscad"
    | "stl"
    | "robot-telemetry"
    | "convex-report";
  title: string;
  url?: string;
  path?: string;
  mime?: string;
  metadata?: Record<string, unknown>;
};
```

For web UI work, this maps to dev preview URLs and proof clips.
For harness work, this maps to `.scad`, PNG renders, STL exports, and checks.
For robot work, this maps to telemetry, camera frames, and outcome summaries.

## Talos MCP Responsibilities

Talos MCP should expose domain tools, not generic machine operations.

Suggested tool families:

### ERP/application tools

- `talos_schema_summary`
- `talos_route_map`
- `talos_convex_functions`
- `talos_erp_flow_describe`
- `talos_seed_preview_data`
- `talos_test_scenario_run`

### Harness/CAD tools

- `talos_harness_brief_create`
- `talos_harness_constraints_get`
- `talos_openscad_render`
- `talos_mesh_check`
- `talos_harness_artifact_register`

### Robotics tools

- `talos_robot_status`
- `talos_robot_trial_plan`
- `talos_robot_trial_start`
- `talos_robot_trial_stop`
- `talos_robot_telemetry_get`
- `talos_robot_camera_frame`
- `talos_robot_outcome_label`

Yaver MCP should continue owning infrastructure:

- connect/recover device
- runner auth
- task/graph execution
- preview/recording
- artifact serving
- vault and secrets on the local machine only

## Harness-First Robotics Lane

For Talos robotics, focus first on harness cases:

1. terminal-block insertion fixture
2. wire routing clips
3. connector presentation jig
4. strain relief and label holder
5. inspection fixture for camera visibility

The first-class work kind is `harness-cad`, backed by OpenSCAD:

```ts
await yaverMode.start({
  workKind: "harness-cad",
  prompt: "Create a clip for 8x 22AWG wires with 12mm bend radius",
  outputs: ["openscad", "png", "stl"],
});
```

Prompt package constraints should include:

- units are millimeters
- named parameters at top
- wire gauge and count
- bend radius minimum
- terminal block or connector dimensions
- robot gripper and wrist-camera clearance
- print material and nozzle assumptions
- no undeclared OpenSCAD libraries

Progress phases:

- preparing harness brief
- writing parametric OpenSCAD
- rendering PNG
- exporting STL
- checking dimensions
- registering artifact
- ready for review

## Web/Desktop UI Shape

Talos web and desktop should have a Yaver mode panel with:

- chat composer
- target machine picker
- runner picker
- auth/preflight ribbon
- progress timeline
- terminal/log drawer
- preview pane
- artifact drawer
- "Open in Yaver" deep link or shared session id

Domain-specific panes can appear based on work kind:

- web UI: browser preview iframe and proof clip
- Convex/backend: schema/function report
- harness CAD: OpenSCAD editor, PNG render, STL viewer
- robot trial: camera frame, telemetry chart, outcome controls

## Security and Product Boundaries

1. Runner credentials are user-owned.
   Yaver may start login flows, but it must not pool, resell, or proxy
   third-party AI subscriptions as a service.

2. Secrets do not cross machines.
   Reuse the existing Layer-4 rule in `mcp_remote_proxy.go`: vault/env/token
   tools are local-only.

3. Guests need scoped access.
   A Talos operator can be allowed to start approved robot trials or review
   artifacts without gaining `/exec`, terminal, vault, or unrestricted tasks.

4. Robotics actions require hard gates.
   AI-authored code can propose a policy or fixture, but physical execution
   must go through non-AI safety gates: workspace bounds, velocity limits,
   force thresholds, e-stop state, and explicit operator approval.

5. Talos production changes need lanes.
   For ERP work, keep feedback/self-improvement on candidate branches and
   preview deployments before promotion.

## Implementation Plan

### Phase 1: Yaver mode using existing task API

- Add a Talos-side client wrapper around Yaver web/headless client semantics.
- Let Talos select a Yaver device and create `/tasks` with `runner`,
  `model`, `projectName`, and `workDir`.
- Surface runner/auth/preflight rows.
- Stream task status and result artifacts.

No new Yaver backend route required.

### Phase 2: Yaver mode sessions

- Add a small session model in Talos that stores selected device, runner,
  workDir, MCP servers, and last task/graph ids.
- Add typed progress rendering in Talos chat.
- Add auth recovery CTAs inline.

### Phase 3: Talos MCP

- Implement Talos domain MCP tools.
- Ensure the runner launched by Yaver can see both Yaver and Talos MCP
  capabilities.
- Add prompt package generation for Talos app, Convex, and harness contexts.

### Phase 4: Graph-backed Yaver mode

- Use `agent_graph_start` / `code_mesh_start` for broad tasks.
- Provide explicit nodes for plan/implement/verify/render.
- Use `resource_modes` for browser, proof-video, openscad, robot, and
  custom Talos resources.

### Phase 5: Harness CAD first-class

- Add `harness-cad` as a Talos work kind.
- Add OpenSCAD render commands on the Hetzner host.
- Register `.scad`, PNG, STL, and validation outputs as artifacts.
- Render PNG/STL in Talos web/desktop.

### Phase 6: Robot trial loop

- Add robotics MCP tools for status/trial/telemetry/outcome.
- Keep physical actions behind operator confirmation and safety gates.
- Link trial telemetry back to the Yaver task or graph that produced it.

## Acceptance Criteria

Yaver mode is real when:

1. From Talos web UI, user selects Hetzner host and Codex/Claude.
2. If runner auth is missing, the UI starts the remote login flow and recovers.
3. User asks for a Talos app change and sees progress, diff/result, and web
   preview without SSH.
4. User asks for a harness OpenSCAD part and receives `.scad`, PNG, and STL
   artifacts rendered in the UI.
5. User can run the same flow from MCP/headless automation.
6. Secrets remain local and runner credentials are never stored in Talos app
   data.
