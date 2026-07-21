# Yaver Multi-Cloud Provider Audit

Status: architecture audit plus first implementation slice, 2026-07-21. Code is
source of truth; re-grep the files named here before implementation.

The first slice added the provider facade, Hetzner adapter extraction, guarded
AWS/GCP/Azure adapters, inference wrappers, placement selectors, pool selectors,
and Convex seed catalogs. Do not enable AWS/GCP/Azure/Alibaba for production
placement until live Yaver probes prove the exact workload profile.

## Objective

Yaver should expose one product concept to normal users: a Yaver machine.

The user should not choose AWS vs Azure vs GCP vs Hetzner during onboarding.
The backend should choose a provider by policy, available credits, region,
capability, wake latency, cost, and current fleet health. The UI may show the
provider in an advanced/status view for transparency, but provider choice must
not become the product surface.

The second objective is portability. Yaver can use provider credits to reduce
trial cost, but the codebase must not become an AWS/Azure/GCP app. Provider
logic should live behind a small infrastructure facade; everything above it
continues to talk in Yaver concepts: machine, image, volume, wake, park,
activity, relay, runner, inference endpoint.

Compute and inference are independent planes. A Yaver machine can run on
Hetzner while managed trial inference is served through AWS Bedrock, GCP
Gemini/Vertex, Azure AI, Alibaba Model Studio/DashScope, or another gateway.
Provider credits should be spendable in combinations, not force compute and
models onto the same cloud.

## Current State

The current managed-cloud path is Hetzner-first.

Evidence in code:

- [backend/convex/schema.ts](../../backend/convex/schema.ts)
  already has provider-agnostic fields on `cloudMachines`: `provider`,
  `cloudResourceId`, `serverType`, `volumeId`, `baseImageId`,
  `lastSnapshotId`, wake/park timing, `lastActivityAt`, `providerStatus`.
- [backend/convex/cloudMachines.ts](../../backend/convex/cloudMachines.ts)
  directly calls Hetzner APIs for volumes, server provisioning, Cloudflare DNS,
  first-boot cloud-init, health checks, wake/park, and deletion.
- [desktop/agent/cloud_provisioners.go](../../desktop/agent/cloud_provisioners.go)
  has a provisioner registry, but managed compute is still Hetzner-specific.
- [desktop/agent/cloud_stopstart.go](../../desktop/agent/cloud_stopstart.go)
  implements the right economic behavior for Hetzner: snapshot/delete or
  volume-backed recreate, because a stopped Hetzner server still bills.
- [web/lib/managed-cloud.ts](../../web/lib/managed-cloud.ts)
  already describes the product lifecycle as `Pause` and `Resume`, not
  provider-native stop/start.
- [mobile/src/lib/remoteCodingSelection.ts](../../mobile/src/lib/remoteCodingSelection.ts)
  still has Hetzner-like naming heuristics. That should become a capability
  signal, not a hostname/provider heuristic.

This means the data model is mostly ready for multi-provider work, but the
control plane is not. The next implementation should extract a provider facade
from the Hetzner code rather than adding AWS/Azure/GCP conditionals throughout
`cloudMachines.ts`.

## Product Contract

The user-facing contract is:

- Yaver provides a machine that can run the Go agent.
- The machine can run remote coding runners: Claude Code/Codex/OpenCode/etc.
- The machine can run Hermes/dev-server workflows and WebRTC/relay flows.
- The machine can optionally run Redroid or another Android container runtime
  when that workload profile is requested.
- The machine can run Yaver Serverless for apps that normie users build, either
  on the user's dedicated cloud machine, another Yaver-managed machine, or a
  user self-hosted machine.
- The machine can optionally use managed inference for trial users, but the
  first-stage pricing assumption is that paid users bring their own Claude
  Code/Codex/OpenCode subscription or API key.
- Idle machines are parked automatically so trial credits are not burned on
  unused compute.
- User state survives parking.
- Provider choice is internal. The user sees "Running", "Waking", "Asleep",
  "Needs authorization", "Error", and "Usage limit reached", not provider API
  statuses.

## Provider Capability Profiles

Provider selection must be fail-closed. A provider is eligible only if it
satisfies the required profile for the requested workload. Credits or low price
must never override missing capability.

### Base Linux Runner

Required for every Yaver managed machine:

- Linux VM with root or equivalent cloud-init/custom-script bootstrap.
- Public IPv4 or reliable public endpoint path.
- Outbound internet for Convex, relay, Git providers, package registries, and
  runner OAuth flows.
- Docker or containerd support.
- Systemd or a compatible long-running service model for the Yaver Go agent.
- Firewall/security-group control for SSH, HTTPS, and optional relay ports.
- Provider metadata/control API to create, inspect, and delete instances.
- Durable identity: provider resource id, public IP/hostname, provider status.
- Startup script can write `/root/.yaver/config.json` or equivalent home path
  without hardcoding a personal local username.

### Durable Workspace

Required before provider can run paid or trial users:

- Persistent block volume or equivalent disk that can outlive compute deletion.
- Attach/detach volume on recreate, preferably in the same region/zone.
- A slim base image or image family so wake does not require rebuilding the
  entire toolchain every time.
- Snapshot fallback for legacy rows or provider volume failure.
- Explicit delete semantics for compute and storage. Yaver must know which
  resources keep billing after park.
- Provider-side labels/tags for Yaver ownership, user/machine id, and cleanup.

### Fast Wake / Auto-Park

Required for free trials and credit-backed infra:

- Create VM from image + attach persisted volume in a predictable time budget.
- Query instance state while waking so UI can show honest progress.
- Delete compute without deleting data.
- Recreate network/DNS endpoint or update Yaver routing after IP changes.
- Per-machine `lastActivityAt` drives idle park; provider must not be selected
  if Yaver cannot park it safely.

### Remote Runner AI Wrapping

Required for a remote coding machine:

- Enough CPU/RAM/disk for package installs, monorepo scans, TypeScript, Metro,
  Docker builds, and CLI-based agents.
- Long-running process support for `tmux`, PTY, shell, and runner sessions.
- Browser/device-code OAuth is reachable from the user's normal device.
- Secrets remain on the runtime machine or local vault. Convex stores only
  labels, booleans, ids, timestamps, and capability metadata.
- The machine can report installed/authed runners into `runnersAvailable`.

### Hermes / WebRTC / Relay

Required for Yaver's mobile and remote-development path:

- The Go agent can maintain outbound relay tunnels.
- HTTPS endpoint or relay path can reach agent HTTP.
- UDP can be used when running a self-hosted relay or WebRTC path requires it.
- TCP fallback remains supported, but high-volume/streaming paths must be
  metered and bounded.
- Provider firewalls must be explicit. Do not rely on permissive defaults.

### Redroid / Android Runtime

Required only for workloads that need Redroid or Android container testing:

- Linux VM size with enough CPU/RAM/disk for Android container workloads.
- Docker support with required kernel features for Redroid/binder/ashmem or the
  chosen Android container runtime.
- GPU acceleration is optional, but provider must be marked CPU-only if absent.
- Nested virtualization, privileged containers, or kernel modules must be
  supported if the selected Redroid build requires them.
- Network egress and relay path must allow the Yaver agent to control and stream
  the runtime.

This should be a separate placement profile. Do not send Redroid workloads to a
provider/instance type just because it satisfies the base Linux runner profile.

### Optional Managed Inference

Required only when Yaver pays for inference during trials:

- Provider has a supported first-party model endpoint or Yaver can call an
  external inference gateway from that region.
- Per-user budget/cap enforcement happens before the request leaves Yaver.
- Logs must never include prompts, code, outputs, API keys, or file paths.
- The provider can expose usage/cost metrics quickly enough for quota control.
- First-stage product should treat managed inference as a trial accelerator,
  not the main paid SKU.

## Provider Matrix

| Provider | Compute | Durable state | Fast park/wake | WebRTC/relay | Inference | Good Yaver role | Main risk |
|---|---:|---:|---:|---:|---:|---|---|
| Hetzner | Yes | Volumes + snapshots | Yes, but delete is required to stop server billing | Probe required | No first-party LLM | Baseline paid compute/serverless host, EU cost leader | No free credits; provider-specific volume/image code already leaks upward |
| GCP | Yes | Persistent Disk + snapshots/images | Yes | Probe required | Vertex AI / Gemini | Credit-backed trials, Gemini/Gemma experiments | Program approval uncertain; GPU/region quotas |
| AWS | Yes | EBS + snapshots/AMIs | Yes | Probe required | Bedrock | Credit-backed trials, broad regions, optional inference | Expensive after credits; many APIs tempt lock-in |
| Azure | Yes | Managed disks + snapshots/images | Yes | Probe required | Azure AI / model catalog | Credit-backed Linux trial machines | Signup/card friction; cost can outrun Hetzner |
| Alibaba | Likely | Cloud disks + images/snapshots | Likely | Probe required | Model Studio/DashScope | Later Asia/Turkiye-adjacent credit/region option | Lower initial priority; must pass same gates |

Do not add a provider to production placement until the exact required profile
is implemented and tested. For example, AWS can be eligible for base Linux and
inference before it is eligible for Redroid.

## Provider Facade

Add a narrow provider interface below `cloudMachines.ts`. Suggested shape:

```ts
type ProviderId = "hetzner" | "gcp" | "aws" | "azure" | "alibaba";

type MachineProfile =
  | "linux-runner"
  | "linux-runner-webrtc"
  | "linux-runner-redroid"
  | "linux-runner-gpu"
  | "yaver-serverless-host"
  | "inference-only";

type ProviderCapabilities = {
  provider: ProviderId;
  profiles: MachineProfile[];
  regions: string[];
  supportsPersistentVolume: boolean;
  supportsSnapshotFallback: boolean;
  supportsImageBoot: boolean;
  supportsDeleteStopsComputeBilling: boolean;
  supportsProviderStatus: boolean;
  supportsTags: boolean;
  supportsFirstPartyInference: boolean;
};

type CreateMachineRequest = {
  machineId: string;
  userId: string;
  profile: MachineProfile;
  region: string;
  serverType: string;
  hostname: string;
  bootstrapUserData: string;
  persistedVolume?: {
    sizeGb: number;
    existingVolumeId?: string;
  };
  tags: Record<string, string>;
};

type CreateMachineResult = {
  cloudResourceId: string;
  providerStatus: string;
  serverIp?: string;
  serverType: string;
  volumeId?: string;
  baseImageId?: string;
};
```

Minimum provider methods:

- `describeCapabilities()`
- `listRegions(profile)`
- `createVolume(request)`
- `deleteVolume(volumeId)`
- `createMachine(request)`
- `deleteMachine(resourceId)`
- `snapshotMachine(resourceId)`
- `createMachineFromSnapshot(request)`
- `createMachineFromImageAndVolume(request)`
- `getMachineStatus(resourceId)`
- `estimateCost(request)`
- `listYaverTaggedResources()`

Do not put provider SDK clients in React Native, web, or the desktop agent UI
surface. Provider mutation belongs in the backend/provider worker path, with
BYO-provider flows as a separate user-owned account path.

## Placement Algorithm

Placement runs in stages:

1. Determine workload profile: normal runner, Redroid, GPU, inference-only, or
   hosted backend.
2. Build eligible provider set by hard capabilities. Missing required
   capability means reject before scoring.
3. Filter by policy: country/region, user/team constraints, trial vs paid,
   available credits, quotas, known provider incidents, and capacity.
4. Score candidates:
   - lowest expected cost after credits
   - fastest wake for this profile
   - lowest latency to user/relay
   - existing parked machine reuse
   - provider credit balance and expiry
   - region capacity and historical failure rate
   - data-isolation strength
5. Choose one provider and record the reason in internal telemetry.
6. Return a Yaver machine state to product surfaces, not a provider decision.

Pseudo-code:

```ts
function selectProvider(req: PlacementRequest): PlacementDecision {
  const profile = classifyWorkload(req);
  const eligible = providers
    .filter((p) => hasRequiredCapabilities(p, profile))
    .filter((p) => policyAllows(p, req))
    .filter((p) => quotaAllows(p, profile));

  if (eligible.length === 0) {
    return { ok: false, code: "no_provider_has_required_capabilities" };
  }

  return minBy(eligible, (p) => scoreProvider(p, req, profile));
}
```

Fail-closed examples:

- No persistent volume or snapshot fallback: not eligible for trial users.
- Cannot delete compute while retaining state: not eligible for auto-park.
- No Redroid kernel/runtime support: not eligible for Android runtime.
- Cannot report provider status: not eligible for a wake path exposed to users.
- Cannot tag resources: not eligible for managed production placement.

## Data Isolation

Default isolation model should remain one dedicated VM per user or team
workspace. This is the simplest model to explain and the easiest to move across
providers.

Isolation rules:

- User source code, runner OAuth tokens, package caches, and Yaver Serverless
  app data live on that user's dedicated machine/volume or isolated serverless
  data plane.
- Central Yaver Convex stores metadata only: provider id, resource ids, status,
  capability flags, timestamps, coarse specs, usage counters, app ids, and
  deployment ids.
- No prompts, stdout, paths, source files, screenshots, provider credentials,
  or runner secrets in Convex.
- Provider credentials are platform secrets for managed fleet, or local/vault
  secrets for BYO flows. Never put them in `cloudMachines`.
- Shared multi-tenant machines are a later product and need a stronger runtime
  boundary such as microVMs. Plain Docker on one VM is not a code isolation
  boundary for unrelated users.

Provider abstraction must preserve this. A provider's convenience feature is
not acceptable if it requires moving user code or runner secrets into provider
managed services outside the dedicated-machine boundary.

## Cost Controls

Trial compute should assume credits are temporary and precious.

Required controls:

- Auto-park on by default.
- `lastActivityAt` bumped only by meaningful user activity: task, shell,
  runner session, inference request, dev-server interaction.
- Idle sweep parks after the configured threshold.
- Per-user trial quota: active hours, wake count, concurrent machines,
  inference budget, and storage cap.
- Provider cost estimates stored internally for every placement decision.
- Hard budget alerts per provider account before credits are exhausted.
- No provider production rollout without a cleanup job that lists Yaver-tagged
  resources and flags orphaned VMs, disks, IPs, snapshots, and images.

Important provider difference:

- Hetzner stopped servers still bill like servers, so park must delete compute.
- AWS/Azure/GCP stopped VMs usually still bill disks and sometimes attached
  public IPs or related resources. Park still needs explicit cost accounting;
  "stopped" is not the same as "free".

## Trial vs Paid Routing

Trial users:

- Prefer providers with active credits.
- Prefer small Linux runner profiles.
- Managed inference can be included as a bounded trial accelerator.
- Auto-park aggressively.
- No Redroid unless it is part of a specific trial offer or the capability is
  cheap enough.

Paid users:

- Prefer Hetzner when it meets the workload profile because it is the known
  lower-cost baseline.
- Use AWS/Azure/GCP when credits exist, region is better, required capability is
  missing from Hetzner, or the user explicitly needs provider-specific
  inference.
- If a paid user was created on credit-backed AWS/Azure/GCP and credits run
  down, migration should be possible because Yaver state is on a portable
  workspace volume/snapshot abstraction and the product does not expose
  provider-specific contracts.

## Migration Strategy

Migration should be treated as a product requirement, not an emergency script.

Minimum viable migration:

- Park source machine.
- Export workspace state to a provider-neutral archive or rsync stream.
- Create destination volume/machine from the same Yaver base image.
- Restore `/srv/yaver/state`, runner config, and workspace.
- Preserve Yaver `deviceId` or register a replacement with an explicit mapping.
- Update `cloudMachines.provider`, `cloudResourceId`, `serverIp`, `hostname`,
  `volumeId`, `baseImageId`, and wake/park facts atomically.
- Keep old provider resources until destination health check passes.
- Delete old compute/storage only after explicit provider-id verification.

Do not rely on provider-native snapshots as the only migration path; snapshots
are usually not portable across providers.

## Coding Architecture Addendum

The multi-cloud work should be coded as a provider capability system, not as a
provider switch statement scattered through the product.

Target backend layout:

```text
backend/convex/
  cloudMachines.ts              lifecycle coordinator; owns Yaver machine state
  cloudPlacement.ts             workload classification + provider selection
  cloudProviderHealth.ts        credits, budgets, quota, incident flags
  cloudProviderCleanup.ts       orphan scanner for tagged resources
  cloudInference.ts             optional managed inference gateway policy
  cloudProviders/
    types.ts                    provider facade contracts
    registry.ts                 provider list and capability registry
    hetzner.ts                  current implementation moved behind facade
    gcp.ts                      Compute Engine/Persistent Disk/images
    aws.ts                      EC2/EBS/AMI + optional Bedrock metadata
    azure.ts                    Linux VM/Managed Disk/images
    alibaba.ts                  later ECS/cloud disk/images + Model Studio
    fake.ts                     deterministic provider for tests
```

The lifecycle coordinator asks for Yaver operations:

- create machine
- create durable state
- park machine
- wake machine
- read provider status
- delete machine
- list Yaver-tagged resources
- estimate cost

Only the provider adapter knows whether that maps to Hetzner Volumes, AWS EBS,
Azure Managed Disks, GCP Persistent Disk, or Alibaba cloud disks. Web, mobile,
and desktop must never import provider SDKs or contain provider mutation logic.

## Required Workload Profiles

Provider support is per profile. A provider can be integrated for one profile
and rejected for another.

```ts
type ProviderId = "hetzner" | "gcp" | "aws" | "azure" | "alibaba";

type MachineProfile =
  | "linux-runner"
  | "linux-runner-webrtc"
  | "linux-runner-redroid"
  | "linux-runner-gpu"
  | "yaver-serverless-host"
  | "inference-only";

type RequiredCapability =
  | "cloud-init"
  | "docker"
  | "systemd"
  | "durable-volume"
  | "snapshot-fallback"
  | "image-boot"
  | "delete-stops-compute-spend"
  | "provider-status"
  | "tagged-cleanup"
  | "budget-telemetry"
  | "outbound-relay"
  | "udp-ingress"
  | "stable-endpoint"
  | "webrtc-probe"
  | "redroid-probe"
  | "runner-claude"
  | "runner-codex"
  | "runner-opencode"
  | "serverless-runtime"
  | "hosted-convex"
  | "custom-domain-tls"
  | "first-party-inference";
```

Fail-closed rule: if a provider does not support every required capability for
the selected profile, it is not returned by placement and should not be plumbed
into that path at all.

Examples:

- A provider with Bedrock/Vertex/Azure AI but no wake/sleep lifecycle is
  eligible only for `inference-only`, not for a Yaver machine.
- A provider that runs Ubuntu but cannot pass WebRTC probes is eligible for
  `linux-runner`, not `linux-runner-webrtc`.
- A provider that cannot run Redroid kernel/container requirements is never
  eligible for `linux-runner-redroid`.
- A provider that cannot preserve hosted app data, run the serverless runtime,
  and serve stable HTTPS/TLS is not eligible for `yaver-serverless-host`.
- A provider with no tag-based cleanup is not eligible for managed production
  placement even if it has free credits.

## Provider Facade Contract

Minimum provider methods:

```ts
interface CloudProvider {
  id: ProviderId;
  describeCapabilities(): ProviderCapabilities;
  listRegions(profile: MachineProfile): Promise<RegionOption[]>;
  resolveSku(req: ResolveSkuRequest): Promise<SkuDecision>;
  estimateCost(req: CostEstimateRequest): Promise<CostEstimate>;
  readBudgetStatus(): Promise<BudgetStatus>;

  createVolume(req: CreateVolumeRequest): Promise<CreateVolumeResult>;
  deleteVolume(req: DeleteVolumeRequest): Promise<void>;

  createMachine(req: CreateMachineRequest): Promise<CreateMachineResult>;
  createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult>;
  createMachineFromSnapshot(req: WakeFromSnapshotRequest): Promise<CreateMachineResult>;
  snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult>;
  deleteMachine(req: DeleteMachineRequest): Promise<void>;
  getMachineStatus(req: MachineStatusRequest): Promise<ProviderMachineStatus>;

  openFirewall(req: FirewallRequest): Promise<void>;
  listYaverTaggedResources(): Promise<TaggedResource[]>;
}
```

Every concrete provider must use Yaver tags/labels on resources:

- `yaver:project=yaver`
- `yaver:machineId=<cloudMachines._id>`
- `yaver:userHash=<non-reversible user hash>`
- `yaver:env=<prod|staging|dev>`
- `yaver:managed=true`

No email, prompt, path, repo content, runner token, API key, or customer data
goes into provider tags.

## Runner Architecture

Yaver compute must support the Yaver Go agent plus runner execution. This is
separate from provider inference.

Required runner facts:

- Go agent starts on boot and after wake.
- `tmux`, PTY, shell execution, and streaming work.
- Claude Code, Codex, and OpenCode can be installed/discovered.
- At least one runner is usable before the machine is considered ready.
- Runner auth lives on the user's persisted volume or local machine vault.
- Convex stores only `runnersAvailable` metadata: installed/authed booleans,
  runner ids, labels, and optional default model labels.
- Remote OAuth can be initiated from web/mobile and completed by the user.

Do not confuse "provider has inference" with "provider can run runners".
Bedrock/Gemini/Azure AI/Qwen help trial inference; they do not replace the
remote machine's Claude/Codex/OpenCode runtime.

## Normie Trial To Serverless Flow

The default product journey should be:

1. User downloads Yaver.
2. User selects free trial.
3. Yaver chooses an eligible provider internally and wakes a remote runner.
4. User starts vibing immediately from mobile/web.
5. Runner uses either Yaver-managed trial inference or the user's BYO key/token.
6. User builds a real app shaped like the Yaver monorepo pattern:
   React Native/mobile UI, web UI, backend functions, database/data model,
   auth, storage, and deployment config.
7. User taps deploy.
8. Yaver packages and deploys the app to Yaver Serverless.
9. The serverless app is reachable by stable HTTPS, with optional custom domain.
10. Idle coding compute sleeps. Hosted app runtime sleeps only if the selected
    plan allows cold starts.

The user should never have to learn AWS/Azure/GCP/Hetzner/Alibaba vocabulary to
complete this flow.

## Yaver Serverless Architecture

Yaver Serverless is the runtime for apps normie users build. It is a separate
workload profile from the coding runner, even when both initially run on the
same dedicated cloud machine.

Important distinction:

- Yaver itself uses Convex today as the control plane for accounts, device
  registry, placement metadata, subscriptions, and lifecycle coordination.
- Apps built by normie users should use Yaver Serverless as their default
  backend/database/runtime.
- Generated apps do not use Convex by default. They compile/deploy against the
  Yaver Serverless API, data model, auth/session layer, jobs, storage, and
  realtime primitives.
- The user should not have to create a Convex project, open a Convex dashboard,
  understand Convex functions, or manage Convex deployment state.
- Existing self-hosted Convex experiments in this repo are useful implementation
  evidence for "box-local backend with persistent data", but they are not the
  user-facing product contract for Yaver Serverless.

Initial product shape:

- A trial or paid Cloud Workspace can build the app.
- A `yaver-serverless-host` machine can host the app after deployment.
- For early stages, the same dedicated user machine may run both coding runner
  and hosted app if capability and cost policy allow it.
- For paid or production hosting, placement may separate "builder" and
  "serverless host" so idle coding sleep does not take the app offline.
- Custom domain/TLS uses Yaver's existing domain and TLS reconciler path.
- User app data stays in Yaver Serverless storage on the user's dedicated
  volume or a future isolated serverless data plane.
- Convex rows for Yaver may reference app ids, deployment ids, placement ids,
  domains, quotas, and lifecycle status, but not the generated app's database
  rows as the default storage engine.

Required for `yaver-serverless-host`:

- Container runtime for generated apps.
- Reverse proxy with HTTPS and custom-domain support.
- Durable app state across park/wake/migration.
- Yaver Serverless backend/database runtime for the default path.
- App process supervisor and health probes.
- Per-app CPU/RAM/disk/request limits.
- Build artifact packaging from the remote runner to the serverless host.
- Deploy rollback or previous-version retention.
- Log handling that avoids central storage of source code, secrets, prompts,
  stdout dumps, and customer data.
- Migration path to another provider.
- Wake path that restores the app and backend before routing traffic.

Serverless routing rule:

- Coding sessions can wake a runner.
- App traffic can wake a serverless host only when product policy allows cold
  starts for that plan.
- Paid always-available hosting likely needs a different cost model than trial
  coding compute because sleeping an app affects the user's end users.

Do not place Yaver Serverless on a provider until `linux-runner` plus durable
state, TLS, app health, rollback, and cleanup all pass. A provider can be
eligible for coding trials before it is eligible for hosted apps.

### Distributed Serverless Data Plane

Yaver Serverless should be multi-provider from the beginning at the architecture
level, even if the first implementation runs on Hetzner.

Control plane:

- Yaver account, app, deployment, domain, version, quota, and billing metadata.
- Lives in Yaver's existing backend/control plane.
- Stores only metadata and lifecycle state.
- Decides placement, routing, wake/sleep, migration, and quota.
- Never stores user source, env secrets, database rows, prompts, or raw logs.

Data plane:

- Provider-hosted serverless hosts on Hetzner, GCP, AWS, Azure, and later
  Alibaba.
- Runs user app containers/functions, hosted backend/database runtime, reverse
  proxy, Yaver agent sidecar/control process, health probes, and metrics.
- Can be single-tenant first: one user's apps on their dedicated machine/volume.
- Can later evolve to stronger multi-tenant pools only with a real isolation
  boundary.

Routing plane:

- Stable app URL points to Yaver routing metadata, not directly to a provider
  identity.
- Provider host can change without changing the user's product concept.
- DNS/TLS reconciler issues certs for auto domains and custom domains.
- Cold-start-capable plans can route to a wake endpoint while compute starts.
- Always-on plans route only to active healthy hosts or fail over to a standby.

Suggested tables/modules:

```text
backend/convex/
  serverlessApps.ts             app records, owner/team, desired plan
  serverlessDeployments.ts      immutable deployment versions/artifacts
  serverlessPlacements.ts       provider/region/host chosen for a version
  serverlessDomains.ts          auto/custom domain state and TLS lifecycle
  serverlessRuntime.ts          start/stop/health/promote/rollback coordinator
  serverlessUsage.ts            requests, cpu-ms, memory, egress, storage
```

Suggested records:

```ts
type ServerlessApp = {
  userId: Id<"users">;
  teamId?: string;
  name: string;
  plan: "trial" | "sleepy" | "always-on" | "pro";
  desiredRuntime: "container" | "function" | "static-web-plus-api";
  dataPolicy: "dedicated" | "future-isolated-pool";
  createdAt: number;
  updatedAt: number;
};

type ServerlessDeployment = {
  appId: Id<"serverlessApps">;
  version: string;
  artifactRef: string;       // opaque object id, not a local path
  runtimeManifestRef: string;
  status: "building" | "ready" | "failed" | "rolled-back";
  createdAt: number;
};

type ServerlessPlacement = {
  appId: Id<"serverlessApps">;
  deploymentId: Id<"serverlessDeployments">;
  provider: ProviderId;
  region: string;
  hostMachineId?: Id<"cloudMachines">;
  providerResourceId?: string;
  status: "provisioning" | "active" | "sleeping" | "waking" | "draining" | "error";
  coldStartAllowed: boolean;
  lastRequestAt?: number;
  lastWakeDurationMs?: number;
};
```

### Serverless Runtime Shapes

Start with containers because they are portable across providers.

Shape A: dedicated machine, single user:

- A user's Cloud Workspace runs builder + serverless host.
- Cheapest and simplest first version.
- Good for free trial demos and early paid users.
- Risk: sleeping the coding machine can sleep the hosted app unless separated
  by policy.

Shape B: dedicated host, separate from builder:

- Builder sleeps aggressively.
- Hosted app stays active or follows its own cold-start policy.
- Better paid hosting story.
- Still simple isolation: one user's apps per host/volume.

Shape C: regional Yaver Serverless pool:

- Multiple apps in one provider/region pool.
- Requires stronger isolation than plain Docker if unrelated users share hosts.
- Use only after microVM/Kata/Firecracker/gVisor-style boundary and per-tenant
  network/storage controls exist.

Shape D: provider-native functions:

- AWS Lambda, Azure Container Apps/Functions, Cloud Run, Alibaba Function
  Compute can be considered later.
- Do not start here; every provider's native serverless semantics differ and
  would leak provider vocabulary into Yaver.
- If used, wrap behind the same `yaver-serverless-host` contract and keep
  deployment manifests provider-neutral.

### Serverless Placement

Serverless placement is not identical to runner placement.

Runner placement optimizes for:

- fast interactive wake
- low trial cost
- runner availability
- WebRTC/relay capability
- temporary coding sessions

Serverless placement optimizes for:

- stable HTTPS routing
- app uptime policy
- storage durability
- migration safety
- predictable egress/request cost
- region close to app users
- database/backend locality
- cold-start policy

Placement inputs:

- app plan: trial/sleepy/always-on/pro
- app runtime: static, API, background jobs, WebSocket/WebRTC, database
- required backend: Yaver Serverless, external BYO backend, none
- expected traffic and egress
- custom domain requirement
- data residency preference
- provider credits and expiry
- current provider incident/capacity

Fail-closed examples:

- No stable HTTPS/custom-domain path: not eligible.
- No durable database/app state: not eligible.
- No rollback or previous-version retention: not eligible for production.
- No request/egress metering: not eligible for public app hosting.
- No WebSocket/WebRTC support: not eligible for real-time app profiles.
- No cleanup scanner: not eligible.

### Serverless Build And Deploy Pipeline

The remote runner builds the app; serverless hosts run the app.

Pipeline:

1. Runner creates or edits the app in the user's workspace.
2. Yaver detects app manifest: mobile UI, web UI, backend functions, database
   schema, env requirements, storage requirements, jobs, and realtime needs.
3. Runner builds deployment artifact.
4. Artifact is stored in a provider-neutral object store or Yaver artifact
   store under an opaque id.
5. Serverless placement chooses provider/region/host.
6. Host pulls artifact, validates manifest, starts new version.
7. Health probe passes.
8. Router promotes traffic.
9. Prior version remains available for rollback.

Artifact rules:

- Artifact ids are opaque.
- Source paths and local usernames are not stored in central metadata.
- Env secrets are injected from the user's vault/runtime secret store.
- Build logs are local or redacted; central logs contain high-level phases only.

### Serverless State And Database

For normies, "backend/database" must exist without making them configure cloud
services.

Supported modes:

- Default backend: Yaver Serverless runs the app's data model, functions,
  storage, jobs, auth/session integration, and realtime primitives.
- Self-hosted Yaver Serverless: user runs the same runtime on their own
  machine, VPS, homelab, or provider account. Yaver can assist install/update
  through the Go agent, but the machine and provider bill are the user's.
- BYO/export backend: advanced users can export or wire an external backend,
  but it is not the normie onboarding path.
- Static/app-only: web/mobile bundle with no hosted database.

Yaver Serverless database requirements:

- Data volume survives compute deletion.
- Backup/export exists before destructive operations.
- Migration can move data to another provider.
- App and DB placement stay close enough for latency.
- Central Yaver control-plane Convex stores only app ids, deployment ids,
  placement ids, URLs/status, quota, and lifecycle metadata. It does not store
  generated app rows as the default database engine.
- Database dump/export is a product feature, not an internal emergency path.

### Lean Yaver UI

Yaver Serverless should have a lean Yaver web UI, not a Convex-style developer
console.

The default UI should show:

- app status
- current deployment/version
- domain and HTTPS status
- env variable names/status, not secret values
- recent deploy phases
- coarse logs and health
- usage/quota
- export database
- download deployment artifact/manifest
- rollback

The default UI should not become a full database admin studio. The primary
interaction model stays vibing with the runner and app; the UI exists for
status, deploy control, export, and recovery.

### Cloud Workspace Package

Yaver Serverless is part of the Cloud Workspace package.

Product-level package:

- remote builder machine
- Yaver Go agent
- Claude/Codex/OpenCode runner support
- optional managed trial inference or BYO inference
- Hermes/WebRTC/relay support where the profile requires it
- Yaver Serverless deploy target
- database/export/runtime for generated apps
- auto-sleep for unused builder compute
- app-host sleep/always-on policy based on plan

Implementation-level placement may split the package:

- builder and serverless host on the same machine for simple trials
- builder sleeps, serverless host stays active for paid hosting
- builder on credit-backed GCP/AWS/Azure, serverless host on Hetzner
- inference on Bedrock/Vertex/Azure AI/Alibaba while compute runs elsewhere

The user still buys/uses "Cloud Workspace"; provider and machine splitting are
internal details.

### Self-Hosted Yaver Serverless

Self-hosted Yaver Serverless is required for trust, portability, and the
open-source product promise. A user should be able to outgrow Yaver-managed
hosting without rewriting the app.

Self-host targets:

- user's laptop/dev machine
- user's home server
- user's existing Hetzner/AWS/GCP/Azure/Alibaba VM
- bare-metal or on-prem Linux
- future Kubernetes/container host

Self-host contract:

- Same app manifest as Yaver-managed Serverless.
- Same runtime API surface.
- Same database dump/import format.
- Same artifact format.
- Same env/secret declaration format.
- Same health/readiness endpoints.
- Same Yaver agent control hooks when the user opts into management.
- No provider credential required by Yaver when the user self-hosts manually.

Self-host modes:

- Assisted: Yaver agent installs and manages Yaver Serverless on a user-owned
  machine. Credentials stay local/vault-backed.
- Manual: user runs a generated install command or Docker Compose bundle.
- Export-only: user downloads artifact, database dump, and manifest and runs it
  outside Yaver.

Self-host export bundle should include:

- app artifact image or source bundle
- serverless manifest
- database schema
- database dump
- storage metadata and object export pointers
- env var names with no secret values
- version and migration metadata
- runtime install notes

Do not make Yaver Serverless depend on a private Yaver-only control plane for
basic runtime operation. Yaver's control plane can improve onboarding, routing,
updates, monitoring, and billing, but the app/runtime must be exportable.

### Serverless Sleep Policy

Coding compute and hosted app runtime have different sleep rules.

Trial coding runner:

- aggressive auto-sleep
- wake on user interaction
- quota-limited inference and compute

Trial serverless app:

- can sleep if positioned as preview/demo hosting
- wakes on owner preview or explicit test traffic
- public traffic may show cold-start behavior only if product copy allows it

Paid serverless app:

- sleepy plan: cold starts acceptable, cheaper
- always-on plan: no sleep during active subscription, or warm standby
- pro plan: regional failover or separate builder/host

Never let a coding-runner idle policy accidentally take down a paid app that is
supposed to be always available.

### Distributed Provider Roles

Different providers can serve different planes at the same time:

- Hetzner: default paid compute/serverless host when capability fits and cost is
  the main variable.
- GCP: trial compute and Gemini/Vertex-backed inference when credits are active.
- AWS: trial compute, Bedrock inference, and later high-availability regions.
- Azure: trial compute and Azure AI where credits exist.
- Alibaba: later Asia-region compute and Qwen/Model Studio benefits.

These roles are independent. Example: a user can build on a GCP-credit runner,
use Bedrock trial inference, and deploy paid hosting to Hetzner if the
capability/cost score says so.

Placement should therefore produce three independent decisions when needed:

- builder compute provider
- serverless hosting provider
- inference provider

They may be the same provider, but they should not be forced to be the same.
The only reason to co-locate them is a measured requirement: latency,
data-locality, bandwidth cost, compliance, or a workload-specific provider
capability.

### Serverless Multi-Provider Migration

Migration must be a normal lifecycle:

1. Freeze deployment writes.
2. Export app artifact and hosted backend state.
3. Provision destination serverless host.
4. Restore state.
5. Start app version.
6. Run health/readiness probes.
7. Shift routing.
8. Keep source placement for rollback window.
9. Delete old provider resources after ownership/tag verification.

Do not build serverless on provider-native features that cannot be represented
in a provider-neutral manifest unless there is an explicit escape hatch and a
clear "not portable" label.

## WebRTC / Relay Architecture

Yaver is actively developing WebRTC and relay-heavy remote workflows, so compute
providers must be validated against that path.

Required for `linux-runner-webrtc`:

- Outbound relay tunnel remains connected over long-lived sessions.
- HTTPS control plane reaches the Go agent through direct endpoint or relay.
- UDP ingress can be opened for self-hosted relay, STUN/TURN, or media paths
  when the selected Yaver mode requires it.
- Provider firewall/security group rules are explicit and tagged.
- NAT behavior does not break long-lived outbound tunnels.
- CPU headroom is enough for Go agent, runner process, relay tunnel, and WebRTC
  encode/decode or forwarding.
- A Yaver WebRTC readiness probe runs after first boot and after wake.

Do not mark a provider or SKU as WebRTC-capable based on marketing docs. The
runtime image must prove it with a probe.

## Wake / Sleep Cost Architecture

Stopping cost is a first-class requirement, not an optimization.

Principle: a user's remote coding machine should cost Yaver zero compute spend
when nobody is using it. Storage may remain, but CPU/RAM instance spend must be
stopped through provider-specific park/delete/deallocate behavior.

Lifecycle states:

- `provisioning`: initial create.
- `active`: Go agent, relay, and runner readiness probes pass.
- `parking`: preserving state and removing compute spend.
- `asleep`: compute spend stopped; durable state remains.
- `waking`: recreating compute and waiting for readiness.
- `error`: curated failure label, no secrets.

Rules:

- Trial users cannot disable auto-park.
- Paid users may tune idle minutes, but default remains auto-park on.
- Health checks do not count as activity.
- Meaningful activity is task execution, shell/PTY, runner turn, dev-server
  interaction, WebRTC/relay session, or machine-bound inference.
- Parking must fail closed: never delete compute unless volume/snapshot
  recovery exists.
- Wake must not mark active until `/health`, relay readiness, and runner
  readiness pass.
- Provider cleanup must scan and report orphan VMs, disks, public IPs,
  snapshots/images, firewalls, and security groups.
- The coding runner and the deployed serverless app have separate idle
  policies. Do not keep the builder machine running just because an app exists.
- If the app is deployed to the same machine as the builder, the plan must make
  that tradeoff explicit: preview/sleepy apps can cold-start; always-on apps
  must move to a host whose running cost is covered by the hosting plan.

Provider-specific cost mapping:

- Hetzner: stopped servers still bill; park deletes compute and keeps volume or
  snapshot recovery.
- AWS: account for EC2, EBS, snapshots, AMIs, Elastic IP, NAT/data transfer,
  and Bedrock separately.
- Azure: account for VM, managed disk, snapshot/image, public IP, bandwidth,
  and Azure AI separately.
- GCP: account for VM, Persistent Disk, snapshot/image, reserved IP, bandwidth,
  and Vertex/Gemini separately.
- Alibaba: later account for ECS, cloud disk, custom image/snapshot, EIP,
  bandwidth, and Model Studio/DashScope separately.

If a provider cannot support wake/sleep with durable state and cleanup, do not
integrate it into compute placement.

## Managed Inference Architecture

Inference is optional and separate from compute placement.

```text
mobile/web/agent
  -> Yaver inference gateway
    -> user/team quota
    -> trial budget
    -> redaction/no-log policy
    -> provider adapter
      -> Bedrock | Vertex/Gemini | Azure AI | Alibaba Model Studio/DashScope | external
```

The compute provider and inference provider are selected separately:

```text
Placement request
  -> compute placement
       Hetzner | GCP | AWS | Azure | Alibaba
  -> inference placement
       BYO key | Yaver gateway | Bedrock | Gemini/Vertex | Azure AI | DashScope
  -> combined session plan
       machineProvider + inferenceProvider + budget policy + wake policy
```

Valid examples:

- Hetzner compute + Bedrock inference.
- Hetzner compute + Gemini/Vertex inference.
- Hetzner compute + Azure AI inference.
- GCP credit compute + Bedrock inference.
- AWS credit compute + Gemini inference.
- Azure credit compute + user BYO Claude/Codex/OpenCode.
- User self-hosted compute + Yaver-managed trial inference.

Rules:

- Managed inference must go through the Yaver gateway.
- The gateway enforces user/team budget before the provider call.
- Prompts, code, output, file paths, screenshots, and secrets are not stored.
- Provider selection is internal unless the user explicitly configures BYOK.
- Inference can keep a machine awake only when attached to an active
  machine-backed session; standalone inference must not burn compute hours.
- Free-tier/provider-credit inference is useful for onboarding, but paid
  pricing should not assume Yaver resells inference until margins are proven.
- Inference provider choice should optimize remaining credits, model quality,
  latency, cost, regional availability, and quota.
- Compute provider choice should optimize wake/sleep, durable state,
  WebRTC/relay capability, serverless capability, and cost.
- Do not couple the two unless a workload explicitly requires same-provider
  locality.

### Credit Mixer

Yaver should maintain an internal credit ledger per provider account:

```ts
type ProviderCreditState = {
  provider: ProviderId;
  creditUsdRemaining?: number;
  creditExpiresAt?: number;
  monthlyBudgetUsd?: number;
  monthToDateSpendUsd?: number;
  hardStopAtUsd?: number;
  supportsCompute: boolean;
  supportsInference: boolean;
  lastSyncedAt: number;
};

type SessionCostPlan = {
  computeProvider: ProviderId;
  inferenceProvider?: ProviderId | "byo" | "external";
  estimatedWakeCostUsd: number;
  estimatedHourlyComputeUsd: number;
  estimatedInferenceBudgetUsd: number;
  autoParkMinutes: number;
  reason: string;
};
```

Credit mixer policy:

- Spend expiring credits first, but only after capability gates pass.
- Prefer Hetzner for paid compute when no credit advantage exists.
- Prefer provider credits for trial compute if wake/sleep and cleanup are
  implemented.
- Prefer provider model credits for trial inference even when compute runs on
  Hetzner.
- Never run a session if both compute and inference budgets are unknown.
- Stop or degrade before crossing hard budget limits.
- Record why a combination was selected, but expose only simple product state
  to the user.

Alibaba note: official Alibaba pages currently advertise free-trial/startup
credit paths and an AI Catalyst path, including AI/model benefits. Treat this
as a real later opportunity, but not a reason to skip the same compute,
WebRTC, wake/sleep, runner, cleanup, and credential gates.

## Open-Source Credential Safety

Yaver is open source, so cloud integration must assume the repository is public
and hostile readers can inspect every code path.

Hard rules:

- Never commit AWS, GCP, Azure, Hetzner, Alibaba, Cloudflare, Convex, runner, or
  inference credentials.
- Never put provider credentials in `cloudMachines`, provider tags, logs,
  analytics, screenshots, crash reports, docs, or tests.
- Managed provider credentials live only in provider secret stores or deployment
  secrets controlled by Yaver/Simkab operators.
- BYO provider credentials live only in the user's local vault/account store,
  never in central Convex.
- Test fixtures use fake providers or local httptest-style servers, not real
  credential-shaped strings.
- Error messages must say which capability failed without printing request
  bodies, headers, tokens, resource secrets, or cloud-init contents containing
  secrets.
- Resource ids are allowed only when needed for lifecycle bookkeeping and must
  be scoped by user/team ownership before any read or mutation.
- Public docs use aliases and capability names, not real account ids, IPs,
  hostnames, access keys, billing ids, or token prefixes.

This applies especially to provider SDK initialization. The code may define the
environment variable names it expects, but never default them to real values and
never echo them in diagnostics.

## Deep Software Architecture

The implementation should be a small set of composable services, not a monolith
that directly calls cloud APIs from product routes.

Core services:

```text
CloudProviderRegistry
  owns provider adapters and capability metadata

CapabilityEvaluator
  checks whether a provider/SKU satisfies a workload profile

CreditLedger
  tracks remaining credits, expiry, monthly spend, hard stops, and provider
  budget status

PlacementPlanner
  builds candidate compute/serverless/inference plans and scores them

LeaseManager
  reserves capacity/credits while provisioning or waking

MachinePoolManager
  manages warm, parked, and active machine pools per provider/profile/region

LifecycleOrchestrator
  executes create, wake, park, delete, migrate, and cleanup workflows

InferenceRouter
  chooses BYO vs managed inference provider per session/request

ServerlessRuntimeOrchestrator
  deploys, promotes, rolls back, sleeps, wakes, and migrates generated apps
```

### Class / Interface Shape

Use TypeScript interfaces in `backend/convex/cloudProviders/types.ts`. Convex
does not require classical OOP inheritance; the important part is a stable
contract. If a later worker process uses classes, the same contracts apply.

```ts
export abstract class AbstractCloudProvider {
  abstract readonly id: ProviderId;

  abstract describeCapabilities(): ProviderCapabilities;
  abstract listRegions(profile: MachineProfile): Promise<RegionOption[]>;
  abstract resolveSku(req: ResolveSkuRequest): Promise<SkuDecision>;
  abstract estimateCost(req: CostEstimateRequest): Promise<CostEstimate>;
  abstract readBudgetStatus(): Promise<BudgetStatus>;

  abstract createVolume(req: CreateVolumeRequest): Promise<CreateVolumeResult>;
  abstract deleteVolume(req: DeleteVolumeRequest): Promise<void>;

  abstract createMachine(req: CreateMachineRequest): Promise<CreateMachineResult>;
  abstract createMachineFromImageAndVolume(req: WakeFromVolumeRequest): Promise<CreateMachineResult>;
  abstract createMachineFromSnapshot(req: WakeFromSnapshotRequest): Promise<CreateMachineResult>;
  abstract snapshotMachine(req: SnapshotMachineRequest): Promise<SnapshotResult>;
  abstract deleteMachine(req: DeleteMachineRequest): Promise<void>;
  abstract getMachineStatus(req: MachineStatusRequest): Promise<ProviderMachineStatus>;

  abstract openFirewall(req: FirewallRequest): Promise<void>;
  abstract listYaverTaggedResources(req: ListTaggedResourcesRequest): Promise<TaggedResource[]>;
}

export abstract class AbstractInferenceProvider {
  abstract readonly id: ProviderId | "external";
  abstract describeModels(): Promise<ModelCapability[]>;
  abstract estimateInferenceCost(req: InferenceEstimateRequest): Promise<InferenceCostEstimate>;
  abstract invoke(req: ManagedInferenceRequest): Promise<ManagedInferenceResult>;
}
```

Concrete classes:

```text
HetznerProvider extends AbstractCloudProvider
GcpProvider extends AbstractCloudProvider
AwsProvider extends AbstractCloudProvider
AzureProvider extends AbstractCloudProvider
AlibabaProvider extends AbstractCloudProvider

BedrockInferenceProvider extends AbstractInferenceProvider
VertexInferenceProvider extends AbstractInferenceProvider
AzureAiInferenceProvider extends AbstractInferenceProvider
DashScopeInferenceProvider extends AbstractInferenceProvider
ExternalGatewayInferenceProvider extends AbstractInferenceProvider
```

Rules:

- Provider constructors receive only secret handles, not raw secrets.
- Raw provider credentials are loaded inside the provider adapter at the last
  possible moment from the approved secret source.
- Provider methods return curated errors with `code`, `provider`, `operation`,
  and `safeMessage`; never raw SDK request/response dumps.
- Provider classes never write Convex rows directly. They return facts to the
  orchestrator; the orchestrator mutates Yaver state.

### Placement Request Model

Placement should plan compute, serverless hosting, and inference independently.

```ts
type PlacementRequest = {
  userId: Id<"users">;
  teamId?: string;
  intent: "trial" | "paid" | "owner-dev" | "self-host-assisted";
  workload: {
    builder?: BuilderWorkload;
    serverless?: ServerlessWorkload;
    inference?: InferenceWorkload;
  };
  regionHint?: string;
  latencyHint?: "turkiye" | "eu" | "us" | "asia" | "global";
  budgetPolicy: BudgetPolicy;
  allowColdStart: boolean;
};

type PlacementPlan = {
  builder?: ComputePlacement;
  serverless?: ServerlessPlacementPlan;
  inference?: InferencePlacement;
  combinedEstimatedCost: CostEstimate;
  requiredLeases: PlacementLeaseRequest[];
  reasons: string[];
};
```

### Candidate Pipeline

Placement is a pipeline with hard filters before scoring:

```ts
function planPlacement(req: PlacementRequest): PlacementPlan {
  const profiles = classifyProfiles(req);

  const computeCandidates = profiles.builder
    ? providerRegistry.computeProviders()
        .flatMap((p) => candidateSkus(p, profiles.builder))
        .filter((c) => capabilityEvaluator.accepts(c, profiles.builder))
        .filter((c) => policyAllows(c, req))
        .filter((c) => budgetAllows(c, req))
    : [];

  const serverlessCandidates = profiles.serverless
    ? providerRegistry.computeProviders()
        .flatMap((p) => candidateSkus(p, profiles.serverless))
        .filter((c) => capabilityEvaluator.accepts(c, profiles.serverless))
        .filter((c) => serverlessPolicyAllows(c, req))
        .filter((c) => budgetAllows(c, req))
    : [];

  const inferenceCandidates = profiles.inference
    ? providerRegistry.inferenceProviders()
        .flatMap((p) => candidateModels(p, profiles.inference))
        .filter((c) => inferencePolicyAllows(c, req))
        .filter((c) => budgetAllows(c, req))
    : [];

  return scoreAndCompose(req, computeCandidates, serverlessCandidates, inferenceCandidates);
}
```

Hard filters:

- capability profile
- provider enabled flag
- provider account health
- region/quota
- budget hard stop
- cleanup support
- credential availability
- live incident flag

Scoring inputs:

- remaining credits and expiry
- expected paid cost after credits
- wake time history for this provider/profile/region
- failure rate
- latency to user and relay
- existing parked machine reuse
- serverless uptime policy
- inference model quality/cost/latency
- migration portability

### Credit-Aware Selection

Credit-aware does not mean "pick the cloud with the biggest credit number".
Credits apply after capability, isolation, and lifecycle gates.

```ts
function scoreCredit(c: Candidate, credit: ProviderCreditState, now: number): number {
  if (!credit.creditUsdRemaining || credit.creditUsdRemaining <= 0) return 0;
  if (credit.hardStopAtUsd && credit.monthToDateSpendUsd! >= credit.hardStopAtUsd) {
    return -Infinity;
  }
  const daysToExpiry = credit.creditExpiresAt
    ? Math.max(1, (credit.creditExpiresAt - now) / 86_400_000)
    : 90;
  const expiryPressure = Math.min(3, 90 / daysToExpiry);
  const usableCoverage = Math.min(1, credit.creditUsdRemaining / c.estimatedMonthlyUsd);
  return usableCoverage * expiryPressure;
}
```

Selection policy:

- Trial builder compute: prefer expiring credits if the provider can park to
  zero compute spend and wake reliably.
- Paid builder compute: prefer Hetzner unless another provider is cheaper after
  credits or has a required capability Hetzner lacks.
- Serverless preview: may use credit-backed providers if cold starts are
  acceptable and state is portable.
- Serverless always-on: prefer predictable low cost and reliability over
  short-lived credits.
- Managed inference: prefer model credits first, because inference can be
  remote from compute.
- BYO inference: no Yaver model spend; choose compute independently.

Never select:

- unknown budget state for a managed trial
- provider over hard spend limit
- provider missing cleanup scanner
- provider missing wake/sleep for remote builder machines
- provider missing serverless data export for Yaver Serverless

### Pooling

Pooling is how Yaver makes wake feel fast without paying for idle machines.

Pool types:

```ts
type PoolKind =
  | "warm-builder"
  | "parked-builder"
  | "serverless-preview"
  | "serverless-always-on"
  | "inference-budget";

type PoolKey = {
  provider: ProviderId;
  region: string;
  profile: MachineProfile;
  sku: string;
};

type PoolEntry = {
  key: PoolKey;
  machineId?: Id<"cloudMachines">;
  state: "warm" | "leased" | "parking" | "parked" | "waking" | "active" | "draining" | "error";
  leaseId?: string;
  reservedForUserId?: Id<"users">;
  expiresAt?: number;
  lastProbeAt?: number;
  lastWakeDurationMs?: number;
};
```

Pool strategy:

- `parked-builder`: default pool. Stores durable volume/snapshot and no compute
  spend. Used for returning users.
- `warm-builder`: tiny, capped pool for trials only if credits cover it and
  conversion benefit is proven. Warm entries must have short TTLs.
- `serverless-preview`: can sleep and wake on owner preview.
- `serverless-always-on`: only for paid plans where running cost is covered.
- `inference-budget`: not a machine pool; it is per-provider/model budget
  reservation for gateway calls.

Pooling rules:

- Do not keep a per-user builder running when nobody is using it.
- Warm pools must be global/capped, never one warm VM per trial user.
- Leases expire automatically.
- A leased machine is not assigned to another user until scrub/readiness passes.
- Dedicated-user machines keep user data; shared warm pools must contain no user
  data until leased and must be destroyed/scrubbed after use.
- Serverless always-on pool cost must map to paid hosting revenue.

### Lease Manager

Every create/wake/inference budget reservation needs a lease so concurrent
sessions do not overspend the same credits.

```ts
type PlacementLease = {
  leaseId: string;
  userId: Id<"users">;
  provider: ProviderId;
  kind: "compute-wake" | "compute-hour" | "serverless-host" | "inference-budget";
  estimatedUsd: number;
  expiresAt: number;
  status: "reserved" | "consumed" | "released" | "expired";
};
```

Lease flow:

1. Planner proposes a plan.
2. LeaseManager reserves compute/inference budget.
3. LifecycleOrchestrator executes provider operation.
4. On success, lease becomes consumed or rolls into active metering.
5. On failure, lease is released and candidate is marked unhealthy if needed.
6. Expired leases are swept.

### Lifecycle Orchestrator

Provider adapters are primitives; orchestrators own workflows.

Builder wake:

1. Acquire lease.
2. Select parked machine or create new volume/machine.
3. Provider create/wake.
4. Wait for provider running status.
5. Wait for Yaver Go agent `/health`.
6. Run relay/WebRTC probe if profile requires it.
7. Run runner readiness probe.
8. Mark active.

Builder sleep:

1. Check last meaningful activity.
2. Drain runner sessions.
3. Persist state.
4. Verify recovery source.
5. Delete/deallocate compute according to provider adapter.
6. Verify compute spend stopped.
7. Mark asleep.

Serverless deploy:

1. Build artifact on runner.
2. Store artifact and manifest.
3. Select serverless host placement.
4. Start new version.
5. Health probe.
6. Promote route.
7. Keep previous version for rollback.

### Test Matrix

Before adding real providers, write provider contract tests using `FakeProvider`.

Required tests:

- provider missing required capability is rejected
- credits do not bypass missing capability
- hard budget stop rejects placement
- expiring credits are preferred among equally capable candidates
- Hetzner wins paid compute when no credit advantage exists
- inference can be selected from a different provider than compute
- serverless host can be selected from a different provider than builder
- lease prevents double-spend under concurrent placement requests
- warm pool TTL expires and parks/destroys compute
- no provider credentials appear in errors, tags, logs, or Convex rows
- WebRTC profile rejects provider without `webrtc-probe`
- Redroid profile rejects provider without `redroid-probe`
- Yaver Serverless rejects provider without export/durable-state support

## Implementation Plan

### Phase 0: Audit and Naming Cleanup

- Replace UI/provider heuristics such as `isHetznerLikeDevice` with capability
  facts from backend/device inventory.
- Add a provider-capability table or module, initially with Hetzner only.
- Define workload profiles: `linux-runner`, `linux-runner-webrtc`,
  `linux-runner-redroid`, `linux-runner-gpu`, `yaver-serverless-host`,
  `inference-only`.
- Add `CreditLedger`, `PlacementPlanner`, `LeaseManager`, and fake provider
  contract tests before real provider integrations.
- Add tests that a provider missing a required capability is not selected.

### Phase 1: Extract Hetzner Provider

- Move direct Hetzner calls in `cloudMachines.ts` behind a provider facade.
- Keep existing behavior byte-for-byte where possible.
- Preserve current volume-backed wake path and snapshot fallback.
- Add a provider fake for tests instead of mocking global fetch ad hoc.
- Verify park/wake, orphan cleanup, and provider status still work.

### Phase 2: Add GCP Compute

- Implement Compute Engine + Persistent Disk + image/snapshot support only.
- Do not integrate Vertex/Gemini in the same PR.
- Use GCP credits for trial capacity only after budget caps are live.
- Required tests: create, tag, status, park/delete compute, recreate from image
  and volume, cleanup orphan resources.
- Eligible profiles: `linux-runner` only until WebRTC/Redroid/serverless
  requirements are proven.

### Phase 3: Add AWS Compute

- Implement EC2 + EBS + AMI/image support only.
- Do not integrate Bedrock in the same PR.
- Required tests: create, tag, status, park/delete compute, recreate from image
  and volume, cleanup orphan resources.
- Eligible profiles: `linux-runner` only until WebRTC/Redroid/serverless
  requirements are proven.

### Phase 4: Add Azure Compute

- Implement Linux VM + managed disk + image/snapshot path.
- Keep startup scripts compatible with the same Yaver cloud-init contract.
- Eligible profiles: `linux-runner` only until WebRTC/Redroid/serverless
  requirements are proven.

### Phase 5: Optional Inference Routing

- Add inference as a separate provider capability, not as part of VM creation.
- Support provider first-party inference only through a Yaver gateway that
  enforces per-user budget before calls.
- Implement independent inference placement so Hetzner compute can use Bedrock,
  Gemini/Vertex, Azure AI, Alibaba DashScope, or external inference.
- Keep paid product positioning focused on compute/remote runner unless the
  inference margin is proven.

### Phase 6: Yaver Serverless Host Profile

- Add `yaver-serverless-host` placement gates: durable state, HTTPS/TLS,
  export/dump, rollback, app health, request/egress metering, cleanup.
- Implement builder/serverless split decisions.
- Implement self-host export bundle contract.
- Ensure generated apps use Yaver Serverless by default, not Convex.

### Phase 7: WebRTC And Redroid Profiles

- Build a WebRTC readiness probe inside the Yaver image.
- Build a Redroid readiness probe inside the Yaver image.
- Record readiness capabilities after first boot and after wake.
- Only mark a provider/instance type eligible after the probe succeeds in CI or
  controlled staging.
- Add placement tests proving normal Linux eligibility does not imply WebRTC or
  Redroid eligibility.

### Phase 8: Alibaba Provider

- Add after Hetzner/GCP/AWS/Azure are stable.
- Implement ECS/cloud disk/image/snapshot/security group/status/tags/cleanup.
- Add DashScope/Model Studio only through the inference gateway.
- Keep out of placement until it passes the same profile gates.

## Open Questions

- Which account owns AWS/Azure/GCP platform resources: Yaver/Simkab platform
  accounts only, or also BYO provider accounts later?
- How many free-trial machine hours should a user get before requiring payment?
- Should trial managed inference be always-on, or only available inside guided
  onboarding tasks?
- What is the acceptable wake time for a "normie" trial user: 60 seconds,
  2 minutes, or 5 minutes?
- Should paid users be migrated to Hetzner automatically when provider credits
  expire, or should migration happen only for new machines first?

## Decision

Use Hetzner as the current baseline and add GCP, AWS, and Azure behind a
provider facade. Design for Alibaba as a later fifth provider. Do not let users
choose providers in the default UX. Do not select a provider unless it satisfies
the workload's required capability profile. Compute, serverless hosting, and
inference are independent placement decisions. Credits are a scoring input
after capability, isolation, lifecycle, cleanup, and budget checks, never a
reason to bypass them.
