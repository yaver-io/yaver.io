# Cloud Relay / Compute / Inference Provider Audit

> ⚠️ **SUPERSEDED ON STATE.** The three-plane model below (relay / compute /
> inference) is correct and worth keeping. The **readiness claims are not.** A
> code-first re-audit the same day found the provider facades, both placement
> engines and the inference registry have **zero production callers**, and that
> the AWS/GCP/Azure adapters contain correctness bugs that would create billing
> orphans on first use. The "Provider Readiness Matrix" below overstates every
> non-Hetzner row.
>
> Canonical: **`cloud-workspace-commercialization-audit-2026-07-21.md`** (state,
> risks, sequenced plan) and **`yaver-cloud-workspace-product-model.md`** (what
> is sold, given away, and forbidden).

Date: 2026-07-21

Status: pre-implementation audit. Code is the source of truth; this file records
what is in the tree now and what must change before AWS, GCP, Azure, and Hetzner
are treated as interchangeable Cloud Workspace providers.

## Executive Summary

Yaver already has the right high-level shape: relay, compute, and inference are
separate concepts in most of the code. The backend has a provider facade for
Hetzner, AWS, GCP, and Azure, and a separate inference registry for Bedrock,
external OpenAI-compatible gateways, and BYO inference. The CLI launch path has
working provider-specific code for Hetzner, AWS, and GCP, plus SSH adoption.

The missing piece is not "add one provider switch". The missing piece is making
three independent planes interoperable:

1. Relay plane: public free relay and Relay Pro/private relay are signaling and
   control reachability. They must not own authorization. They should remain
   usable whether compute is Hetzner, AWS, GCP, Azure, parked, direct-IP, or VM
   only.
2. Compute plane: cloud VMs are disposable executors with durable workspace
   state. Hetzner is the only paid-placement provider that is currently
   production-eligible. AWS/GCP/Azure facades exist but are intentionally marked
   non-eligible.
3. Inference plane: managed inference, BYO inference, and provider credits/trials
   are separate from compute. Bedrock is cataloged, but invoke is intentionally
   blocked until gateway quota/redaction policy is connected.

Therefore the next implementation should keep slicing explicit:

- `relay`: free relay, Relay Pro/private relay, self-hosted relay sidecar.
- `compute`: Hetzner/AWS/GCP/Azure VM lifecycle, durable volume/snapshot,
  startup bootstrap, cleanup, cost stop.
- `inference`: Bedrock/Vertex/Azure AI/OpenAI-compatible/BYO model routing,
  token budget, trial credits, redaction policy.

## Current Relay Plane

`Config` already separates relay data from compute:

- `RelayPassword`, `RelayServers`
- `CachedRelayPassword`, `CachedRelayServers`
- `RelaySourceWorker`
- `PublicEndpoints`
- `ConnectionPreferences`

The runtime relay selection also has tests for configured relay plus cached
fallback relays (`relay_runtime_test.go`). That is the right shape for "Relay
Pro first, public free fallback" and for a Cloud Workspace whose compute is
currently down. Relay availability is not tied to a provider machine id.

Managed Cloud bootstrap in `backend/convex/cloudMachines.ts` already models two
relay concepts:

- `relayPassword`: platform relay password injected into the managed box config.
- `boxRelayPassword`: a per-box self-relay password for a bundled relay listener.

Important invariant: relay is multi-tenant and is not a security boundary. Free
relay vs Relay Pro is a capacity/reachability decision only. The box must still
authenticate client device keys and forced-command SSH paths exactly as before.

### Relay Gaps

- There is no typed product-facing "RelaySlice" or "RelayPlacement" object that
  says "public-free", "relay-pro", "workspace-sidecar", or "external-private".
  The data exists in config fields and user settings, but compute placement does
  not consume a provider-neutral relay plan.
- Cloud Workspace currently tends to think of private relay as part of the
  managed machine bootstrap. That works while compute is up, but if compute is
  parked, Relay Pro should be able to remain reachable independently.
- Provider implementations should not open broad public ingress except for the
  minimum needed by the selected relay sidecar mode. The default should be
  outbound-only agent registration to platform relay.

## Current Compute Plane

There are two compute surfaces that do not yet fully converge.

### Backend Paid Placement Surface

`backend/convex/cloudProviders/types.ts` defines a useful provider-neutral
contract:

- capabilities (`cloud-init`, `durable-volume`, `snapshot-fallback`,
  `delete-stops-compute-spend`, `outbound-relay`, etc.)
- SKU resolution
- cost estimate
- budget status
- create/delete volume
- create/delete machine
- wake from image+volume or snapshot
- snapshot machine
- provider status
- firewall
- tagged cleanup

Provider implementations exist:

- `hetzner.ts`: concrete API calls for volume, machine, snapshot, delete, status.
  Production eligible for `linux-runner`.
- `aws.ts`: concrete signed EC2 Query API calls for run/terminate/describe,
  EBS volume create/delete, and security group ingress. Not production-eligible.
- `gcp.ts`: concrete Compute REST calls for disk/instance/firewall lifecycle.
  Not production-eligible.
- `azure.ts`: concrete ARM calls for disk/VM/status/delete/security rule.
  Not production-eligible.

The registry (`cloudProviders/registry.ts`) creates all four providers, but the
actual Cloud Workspace creation path in `cloudMachines.ts` still imports and
uses `createHetznerProvider` directly. That is the largest backend coupling.

### CLI Manual Launch Surface

`yaver launch` currently has provider-specific implementation functions for:

- `hetzner`
- `aws`
- `gcp`
- `ssh`

All launch providers share the same authentication bootstrap:

- request a device code from Convex
- authorize it with the launching device's token
- inject pending auth through cloud-init
- wait for the new box to consume the code
- mirror runner credentials when reachable

This is good. It means cloud-provider identity does not change the Yaver device
auth model.

These are implementation/operator surfaces, not product surfaces. The end-user
Cloud Workspace product should expose a workspace label/location and SSH access
only. Provider choice belongs to Yaver's placement algorithm, based on capacity,
credits, cost guardrails, reliability, and live probes. Explicit provider
override is appropriate only for owner/operator development and should be gated.

CLI gaps:

- Azure is missing from the concrete launch adapter.
- AWS/GCP currently depend on prepublished Yaver images in `cloud-images.json`.
  The manifest currently has nulls, so a normal user cannot test those paths
  unless they first publish images. Hetzner already has an Ubuntu fallback with
  cloud-init install. AWS/GCP need the same testable fallback.
- `cloud-images.json` has no Azure section.

## Provider Readiness Matrix

| Provider | Backend facade | Paid placement | CLI launch | Durable state | Snapshot/park | Relay posture | Main blockers |
|---|---:|---:|---:|---:|---:|---:|---|
| Hetzner | Yes | Yes | Yes | Volume path exists in managed bootstrap | Snapshot+delete exists; volume model is preferred | Outbound platform relay; optional box relay | Still directly wired in `cloudMachines.ts`; facade cleanup/listing incomplete |
| AWS | Yes | No | Yes | EBS create/delete facade exists | Terminate exists; snapshot wake not wired | Outbound platform relay only | VPC/SG bootstrap, AMI fallback, tagged cleanup, cost/budget telemetry, live probes |
| GCP | Yes | No | Yes | Persistent disk create/delete facade exists | Delete exists; snapshot wake not wired | Outbound platform relay only | Firewall/bootstrap, image fallback, cleanup listing, budget telemetry, live probes |
| Azure | Yes | No | No | Managed disk create/delete facade exists | Delete exists; snapshot wake not wired | Outbound platform relay only | CLI launch, VNet/NIC bootstrap, public IP discovery, image section, cleanup, budget telemetry |

## Current Inference Plane

Inference is already separate from compute:

- `cloudProviders/types.ts` defines inference backend descriptors separately from
  compute providers.
- `inferenceBackends.ts` builds a registry containing Bedrock, optional external
  OpenAI-compatible gateway, and BYO.
- `inferencePlacement.ts` selects a candidate by managed/BYO allowance, model,
  context, production eligibility, latency, cost, quality, and credit expiry.
- `gateway/src/pricing.ts` and `gateway/src/index.ts` meter managed inference as
  `kind: "inference"` with provider cost and charged cents.

Bedrock exists as a descriptor, but `BedrockInferenceProvider.invoke` currently
throws `not_wired`. That is intentional and correct until quota, redaction, and
hard budget policy are connected.

### Inference Gaps

- No Vertex AI provider implementation yet.
- No Azure AI provider implementation yet.
- Bedrock has model catalog but no invoke path.
- Trial/provider credits are represented in selection inputs, but there is no
  provider credit sync that fills `creditUsdRemaining` per provider.
- Inference placement is not yet tied to Cloud Workspace entitlement in a single
  typed "slice" response that says what relay, compute, and inference the user
  gets.

## Required Slicing Model

The product should represent a Cloud Workspace as a composition, not a provider:

```ts
type WorkspaceRuntimePlan = {
  relay: RelaySlice;
  compute: ComputeSlice;
  inference: InferenceSlice;
};
```

Relay slice:

- `public-free`: shared relay, limited capacity.
- `relay-pro`: managed private/pro relay, persists while compute is parked.
- `workspace-sidecar`: relay listener on the active workspace VM.
- `external-private`: user-provided relay.

Compute slice:

- `provider`: `hetzner | aws | gcp | azure`
- `profile`: standard/heavy/build/gpu/serverless-host
- `state`: active/parking/parked/resuming/error
- `durability`: volume preferred, snapshot fallback
- `costGuard`: included allowance + prepaid overage + fail-closed stop

Inference slice:

- `mode`: none/BYO/managed/trial
- `backend`: bedrock/vertex/azure-ai/openai-compatible/external/byo
- `budget`: token cap + provider-credit cap + redaction/quota policy

This keeps the important product truth intact: Cloud Workspace includes Relay
Pro, but Relay Pro is not necessarily running on the same machine as compute.
When compute is down, the relay slice can remain active; when compute is up,
the workspace may also run a sidecar relay for same-owner devices.

## Implementation Order

1. Write the slicing types and catalog first. It should be pure data with tests:
   relay slice, compute provider slice, inference slice, and a combined plan.
2. Make CLI launch testable for all four providers:
   - add Azure launch
   - add AWS/GCP Ubuntu cloud-init fallback
   - add Azure manifest defaults
3. Move backend `cloudMachines.ts` from direct Hetzner usage toward provider
   registry selection, but keep non-Hetzner `productionEligible:false` until live
   probes pass.
4. Add provider cleanup/listing and cost/budget telemetry before enabling paid
   placement on AWS/GCP/Azure.
5. Expand inference providers behind the existing gateway policy:
   - Bedrock invoke only after hard budget/redaction is wired
   - Vertex and Azure AI descriptors first, invoke second
   - provider credit sync feeds `inferencePlacement`

## Non-Negotiable Safety Gates

- Relay never authorizes access. It only forwards same-owner/access-scoped
  ciphertext. Device keys and forced-command cages remain the security boundary.
- Compute delete must stop provider spend. For providers where stopped disks,
  IPs, or snapshots still bill, the cost must be explicit and bounded.
- Hetzner remains delete-not-stop for metered safety unless the volume-backed
  parked path is active and verified.
- Non-Hetzner providers stay non-production-eligible until they can prove:
  tagged cleanup, budget telemetry, reachable Yaver agent, relay data path, and
  durable state restore.
- Inference trials must be prepaid/capped provider credits, never an uncapped
  Yaver liability.
- End users must not supply `provider=aws|gcp|azure|hetzner` for Cloud
  Workspace. Owner/operator overrides use runtime allowlists/env gates and are
  for adapter testing only.

## Concrete Missing Code

- `desktop/agent/launch_azure.go`
- `yaver launch azure` parser/usage wiring
- `cloud-images.json.providers.azure`
- AWS/GCP fallback image selection for normal testability without public Yaver
  images
- provider-neutral runtime plan/slice module and tests
- backend provider registry consumption in `cloudMachines.ts`
- provider resource listing/cleanup for AWS/GCP/Azure
- provider budget telemetry for AWS/GCP/Azure
- Vertex/Azure AI inference descriptors
- Bedrock invoke guarded by gateway quota/redaction, not direct user traffic
