# Cloud Provider Implementation Handoff

> ⚠️ **SUPERSEDED ON STATE — read the audit first.**
> This file is accurate about *intent* and **materially optimistic about what
> works**. A code-first re-audit the same day found:
> - The provider registry, both placement engines, the inference registry, the
>   runtime-slice model and the troubleshooting classifier have **zero
>   production callers** — the whole multi-provider layer is a test-only island.
> - AWS/GCP/Azure are not merely "not production-eligible", they are
>   **incorrect**: GCP reads an `instances.insert` Operation as an Instance,
>   AWS/Azure never return `serverIp` against a hard throw, and GCP/Azure auth is
>   a ~1h OAuth token in an env var with no refresh.
> - Hetzner declares a `tagged-cleanup` capability it does not implement.
>
> Canonical documents:
> - **`cloud-workspace-commercialization-audit-2026-07-21.md`** — state, risks, plan.
> - **`yaver-cloud-workspace-product-model.md`** — what is sold, given, forbidden.
>
> The "Recommended Next Goal" section below is **not** the current plan. Yaver
> sells compute; inference is a capped trial loan and is never sold; trials do
> not include a VM.

Date: 2026-07-21

Purpose: handoff for a follow-up Claude Code goal to continue Cloud Workspace
provider implementation. This summarizes what was completed in the current
Codex pass, what is still incomplete, and the constraints that must shape the
next implementation.

Read this together with:

- `AGENTS.md`
- `CLAUDE.md`
- `docs/architecture/cloud-relay-compute-inference-audit-2026-07-21.md`
- `backend/convex/cloudProviders/*`
- `backend/convex/cloudMachines.ts`
- `desktop/agent/launch_*.go`

## Product Decision Locked In

End users must not choose AWS/GCP/Azure/Hetzner for Cloud Workspace.

Cloud Workspace is a Yaver-managed product:

- Yaver chooses the compute provider automatically from capacity, cost,
  reliability, region health, provider credits, and hard budget policy.
- The user sees a workspace label/location and gets SSH access to the selected
  workspace.
- Provider names may be visible as informational labels, but not as a control.
- Explicit provider selection is allowed only for owner/operator/development
  testing.
- Backend placement must never trust a client-supplied provider unless the
  caller is owner/operator gated by the existing owner allowlist or an equivalent
  server-side entitlement.

Relay, compute, and inference are separate planes:

- Relay: public free relay, Relay Pro/private relay, workspace-sidecar relay,
  external private relay.
- Compute: Hetzner/AWS/GCP/Azure VM lifecycle and durable workspace state.
- Inference: BYO, managed, trial/credit-backed providers such as Bedrock,
  Vertex, Azure AI, OpenAI-compatible gateways.

Cloud Workspace includes Relay Pro, but Relay Pro should not be assumed to live
on the same VM as compute. Compute can be parked while Relay Pro remains
reachable.

## Completed In Current Pass

### Audit Document

Added:

- `docs/architecture/cloud-relay-compute-inference-audit-2026-07-21.md`

This file documents:

- existing provider facades
- current CLI launch capability
- current inference registry
- missing paid-placement requirements
- relay/compute/inference slicing model
- safety gates
- product rule that users do not choose providers

### Runtime Slicing Model

Added:

- `backend/convex/runtimeSlices.ts`
- `backend/convex/runtimeSlices.test.mts`

What it does:

- Defines `RelaySlice`, `ComputeSlice`, `InferenceSlice`, and
  `WorkspaceRuntimePlan`.
- Models Relay Pro as persistent while compute is parked.
- Models workspace-sidecar relay as active-only with fallback to Relay Pro and
  public free relay.
- Keeps compute provider independent from inference provider.
- Marks trial inference as credit-backed and hard-budget-required.

Important behavior covered by tests:

- Cloud Workspace includes Relay Pro even when compute is parked.
- Sidecar relay only applies while compute is active.
- Azure compute can pair with BYO inference; compute provider does not imply
  inference provider.
- Trial inference uses provider credits and hard budgets.

### Provider Troubleshooting Model

Added:

- `backend/convex/providerTroubleshooting.ts`
- `backend/convex/providerTroubleshooting.test.mts`

What it does:

- Classifies runtime failures by plane:
  - provider
  - agent
  - relay
  - SSH
  - ready
- Distinguishes:
  - provider create/update failed
  - provider VM exists but is not running
  - VM running but Yaver agent has not heartbeated
  - agent online but relay signaling/data path is broken
  - Yaver control works but direct SSH is blocked

This is pure classification only. It does not yet run live probes.

### CLI Launch Product Boundary

Modified:

- `desktop/agent/launch_cmd.go`
- `desktop/agent/launch_cmd_test.go`
- `desktop/agent/launch_auto.go`

What changed:

- Public usage is now `yaver launch cloud` and `yaver launch ssh`.
- `yaver launch cloud` uses automatic provider selection through
  `launchCloudAuto`.
- Explicit provider commands still exist as development/operator adapters:
  - `hetzner`
  - `aws`
  - `gcp`
  - `azure`
- Explicit provider launch is blocked unless one of these env vars is truthy:
  - `YAVER_OPERATOR_PROVIDER_OVERRIDE`
  - `YAVER_LAUNCH_PROVIDER_OVERRIDE`

This intentionally preserves concrete adapter testability while preventing
normal users from selecting a provider.

### Concrete Azure CLI Launch Adapter

Added:

- `desktop/agent/launch_azure.go`

What it does:

- Uses Azure CLI (`az`) rather than storing Azure credentials itself.
- Requires `YAVER_AZURE_RESOURCE_GROUP` or `AZURE_RESOURCE_GROUP`.
- Reads location from:
  - `--region`
  - `YAVER_AZURE_LOCATION`
  - `AZURE_LOCATION`
  - `cloud-images.json` default
  - fallback `westeurope`
- Uses Ubuntu cloud-init install path.
- Injects the same pending device-code bootstrap as other providers.
- Waits for the box to consume the device code.
- Mirrors runner credentials if reachable.
- Prints workspace name/location/IP/SSH target.

This is an operator/dev implementation, not end-user provider control.

### AWS/GCP Testable Fallbacks

Modified:

- `desktop/agent/launch_aws.go`
- `desktop/agent/launch_gcp.go`

AWS:

- If `cloud-images.json` has no Yaver AMI for the selected region/arch, resolve
  official Ubuntu 24.04 AMI through AWS SSM:
  `/aws/service/canonical/ubuntu/server/24.04/stable/current/<arch>/hvm/ebs-gp3/ami-id`
- Use cloud-init install fallback so first boot installs `yaver-cli`.

GCP:

- If `cloud-images.json` has no Yaver image, use Ubuntu image family fallback:
  - `ubuntu-2404-lts-arm64`
  - `ubuntu-2404-lts-amd64`
- Use cloud-init install fallback so first boot installs `yaver-cli`.

This means concrete AWS/GCP launch testing no longer depends on published
Yaver golden images.

### Azure Provider Metadata

Modified:

- `cloud-images.json`
- `desktop/agent/accounts.go`
- `desktop/agent/project_manifest.go`

Added:

- Azure manifest defaults:
  - default location `westeurope`
  - default VM sizes for amd64/arm64
  - image slots
- Azure account provider constant and account metadata.
- Azure recognized by project runtime provider status checks.

## Verification Completed

Passed:

```bash
cd backend
npx tsc --noEmit -p convex/tsconfig.json
```

Passed:

```bash
cd desktop/agent
go test . -run 'TestLaunchProvider'
```

Attempted but not valid in this repo:

```bash
cd backend
npm test -- --help
```

Result: backend has no `test` script.

Attempted but not valid directly:

```bash
node --test backend/convex/*.test.mts
```

Result: Node cannot load `.mts` directly without the repo's TypeScript loader.
Use `tsc --noEmit` for type verification unless/until a test runner is added.

## Not Finished

### Backend Paid Placement Still Hetzner-Coupled

`backend/convex/cloudMachines.ts` still directly imports and uses:

```ts
import { createHetznerProvider } from "./cloudProviders/hetzner";
```

and later creates a Hetzner provider directly.

Next goal should move this toward provider registry based selection, while
keeping non-Hetzner providers `productionEligible:false` until all safety probes
pass.

Do not simply switch paid placement to AWS/GCP/Azure. That would be unsafe.

### Non-Hetzner Providers Are Facade-Concrete But Not Production-Eligible

Existing files:

- `backend/convex/cloudProviders/aws.ts`
- `backend/convex/cloudProviders/gcp.ts`
- `backend/convex/cloudProviders/azure.ts`
- `backend/convex/cloudProviders/hetzner.ts`
- `backend/convex/cloudProviders/registry.ts`

Current state:

- Hetzner is production-eligible for baseline Linux runner.
- AWS/GCP/Azure have concrete API facades but are intentionally not
  production-eligible.

Missing before enabling AWS/GCP/Azure paid placement:

- provider resource listing
- provider tagged cleanup
- provider budget telemetry
- live Yaver agent bootstrap probe
- relay data path probe
- SSH reachability probe
- durable state restore verification
- firewall/network bootstrap not requiring precreated resources
- cost model and hard stop policy
- provider-specific delete semantics that actually stop spend

### Azure Backend Facade Needs More Work

`backend/convex/cloudProviders/azure.ts` currently requires an existing network
interface for VM creation:

- `providerOptions.networkInterfaceId`
- or `AZURE_NETWORK_INTERFACE_ID`

That is not enough for Yaver-managed production placement.

Needs:

- resource group policy
- VNet/subnet/NIC creation or selection
- public IP handling
- NSG creation/rule management
- disk attach/detach lifecycle
- tagged cleanup listing
- budget telemetry
- restore-from-disk/snapshot path

### AWS Backend Facade Needs More Work

`backend/convex/cloudProviders/aws.ts` currently requires:

- subnet id
- security group id

Needs:

- VPC/subnet/security group bootstrap or controlled selection
- public IP/endpoint handling
- EBS durable volume attach/mount semantics
- AMI/image fallback in backend placement path
- snapshot/wake orchestration
- resource listing/tagged cleanup
- budget telemetry via Cost Explorer/Budgets or a safer Yaver-side ledger
- live agent/relay/SSH probes

### GCP Backend Facade Needs More Work

`backend/convex/cloudProviders/gcp.ts` has instance/disk/firewall calls but
needs:

- network/firewall bootstrap policy
- public endpoint handling
- persistent disk attach/mount semantics
- image fallback in backend placement path
- snapshot/wake orchestration
- resource listing/tagged cleanup
- budget telemetry
- live agent/relay/SSH probes

### Inference Plane Is Cataloged, Not Fully Invokable

Existing:

- `backend/convex/inferenceBackends.ts`
- `backend/convex/inferencePlacement.ts`
- `backend/convex/cloudProviders/bedrockInference.ts`
- `backend/convex/cloudProviders/openaiCompatibleInference.ts`
- `backend/convex/providerCatalog.ts`
- `gateway/src/index.ts`
- `gateway/src/pricing.ts`

Current state:

- Bedrock descriptor exists.
- BYO/external OpenAI-compatible descriptors exist.
- Inference selection can score provider credits.
- Gateway meters managed inference as `kind: "inference"`.

Still missing:

- Bedrock invoke implementation behind gateway quota/redaction policy.
- Vertex AI inference provider descriptor/invoke.
- Azure AI inference provider descriptor/invoke.
- Provider credit sync feeding `creditUsdRemaining`.
- Trial inference hard caps.
- Unified entitlement response that returns relay/compute/inference slices.

Do not expose uncapped managed inference. Trials must be prepaid/capped provider
credits or hard Yaver-side limits.

### Troubleshooting Is Classification Only

`providerTroubleshooting.ts` does not run probes.

Next implementation should wire real probe inputs:

- provider status from provider facade
- cloud-init/agent service result from direct SSH or provider serial logs
- Convex heartbeat freshness
- relay registration/presence samples
- relay password validation
- SPKI pin validation where configured
- direct SSH reachability

The important user-facing distinction:

- "VM create failed" is provider plane.
- "VM exists but Yaver agent did not appear" is bootstrap/agent plane.
- "Agent online but relay broken" is relay/signaling plane.
- "Yaver works but SSH is blocked" is SSH/firewall/key plane.

### Cloud Workspace UI Should Not Show Provider Picker

Mobile/web should show:

- workspace label
- state
- location
- SSH target
- relay state
- inference mode label
- maybe provider label as read-only diagnostic detail

Mobile/web should not show:

- AWS/GCP/Azure/Hetzner picker
- provider token entry for Yaver-managed Cloud Workspace
- any ability for normal users to override placement provider

Provider account connection can remain for BYO/dev/operator features, but Cloud
Workspace product provisioning should use Yaver-owned provider credentials and
server-side placement.

## Recommended Next Goal For Claude Code

Suggested objective:

> Implement provider-neutral Cloud Workspace placement and troubleshooting wiring
> while keeping AWS/GCP/Azure disabled for paid placement until safety probes
> pass.

Suggested steps:

1. Read `cloudMachines.ts` around the current `createHetznerProvider` usage.
2. Add a provider-selection function that returns a provider decision from
   server-side policy only.
3. Keep paid default on Hetzner.
4. Add an owner/operator-only path that can force provider for backend adapter
   testing, using `ownerAllowlist.ts` or a server-side env gate.
5. Add live probe plumbing that feeds `providerTroubleshooting.ts`.
6. Add tests proving normal users cannot supply provider selection.
7. Add tests proving owner/operator override can exercise AWS/GCP/Azure without
   making them production-eligible.
8. Add provider-specific resource listing/cleanup for AWS/GCP/Azure.
9. Add provider budget telemetry stubs that fail closed until real budget source
   is configured.
10. Only then consider marking a non-Hetzner provider production-eligible.

## Concrete Files To Start With

Backend placement:

- `backend/convex/cloudMachines.ts`
- `backend/convex/cloudProviders/registry.ts`
- `backend/convex/cloudProviderPlacement.ts`
- `backend/convex/providerCatalog.ts`
- `backend/convex/runtimeSlices.ts`
- `backend/convex/providerTroubleshooting.ts`
- `backend/convex/ownerAllowlist.ts`
- `backend/convex/userSettings.ts`

Provider facades:

- `backend/convex/cloudProviders/hetzner.ts`
- `backend/convex/cloudProviders/aws.ts`
- `backend/convex/cloudProviders/gcp.ts`
- `backend/convex/cloudProviders/azure.ts`

CLI/operator launch:

- `desktop/agent/launch_cmd.go`
- `desktop/agent/launch_auto.go`
- `desktop/agent/launch_hetzner.go`
- `desktop/agent/launch_aws.go`
- `desktop/agent/launch_gcp.go`
- `desktop/agent/launch_azure.go`

Relay/signaling:

- `desktop/agent/relay_runtime_test.go`
- `desktop/agent/relay_presence_probe.go`
- `desktop/agent/relay_pinning.go`
- `desktop/agent/infra_http.go`
- `desktop/agent/host_share_prepare.go`

Inference:

- `backend/convex/inferenceBackends.ts`
- `backend/convex/inferencePlacement.ts`
- `backend/convex/cloudProviders/bedrockInference.ts`
- `backend/convex/cloudProviders/openaiCompatibleInference.ts`
- `gateway/src/index.ts`
- `gateway/src/pricing.ts`

## Safety Invariants

- Never push or commit without explicit user permission.
- Never hardcode owner email/user id in public source. Use env-backed allowlists.
- Relay is not an authorization boundary.
- Free relay vs Relay Pro is not a security boundary.
- Provider choice is not an end-user control.
- Cloud Workspace deletion/parking must stop compute spend.
- Hetzner servers must not be left running or merely stopped when the intended
  parked state is delete/volume or snapshot/delete.
- Non-Hetzner providers must remain production-disabled until cleanup and budget
  telemetry are real.
- Inference trials must be capped and fail closed.
- Do not touch the SSH/reverse-SSH implementation if another session owns it;
  consume its outputs through probe/status interfaces instead.

## Current Verification Commands

Use these after modifying the current files:

```bash
cd backend
npx tsc --noEmit -p convex/tsconfig.json
```

```bash
cd desktop/agent
go test . -run 'TestLaunchProvider'
```

When touching provider placement tests, also run the existing `.mts` tests using
the repo's TypeScript-compatible test path if one is added. Direct
`node --test *.mts` does not currently work in this repo.

## Notes On Dirty Worktree

At the time of this handoff the repo already had many unrelated dirty files
outside this provider work. Do not revert them. Treat them as user/other-session
changes.

Files intentionally touched by this provider pass:

- `docs/architecture/cloud-relay-compute-inference-audit-2026-07-21.md`
- `docs/architecture/cloud-provider-implementation-handoff-2026-07-21.md`
- `backend/convex/runtimeSlices.ts`
- `backend/convex/runtimeSlices.test.mts`
- `backend/convex/providerTroubleshooting.ts`
- `backend/convex/providerTroubleshooting.test.mts`
- `desktop/agent/launch_cmd.go`
- `desktop/agent/launch_cmd_test.go`
- `desktop/agent/launch_auto.go`
- `desktop/agent/launch_azure.go`
- `desktop/agent/launch_aws.go`
- `desktop/agent/launch_gcp.go`
- `desktop/agent/accounts.go`
- `desktop/agent/project_manifest.go`
- `cloud-images.json`

