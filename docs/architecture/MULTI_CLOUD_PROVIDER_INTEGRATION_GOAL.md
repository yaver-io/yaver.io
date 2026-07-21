# Multi-Cloud Provider Integration Goal

Status: handoff goal for Claude Code on the Mac mini, 2026-07-21.

Use this with Claude Code `/goal` from the repo root:

```text
/goal Finish Yaver multi-cloud compute and inference integration without vendor lock-in. Read AGENTS.md and CLAUDE.md first. Treat docs as context and code as source of truth. Continue from docs/architecture/MULTI_CLOUD_PROVIDER_AUDIT.md and the provider facade in backend/convex/cloudProviders. Keep users on a simple Cloud Workspace surface: show at most machine provider label such as AWS/GCP/Hetzner and inference label such as BYO/DeepSeek. Do not expose provider knobs. Do not store or print cloud credentials. Do not mutate non-Yaver provider resources. Implement only behind capability gates and tests.
```

## Product Target

Yaver sells one thing to normal users: Cloud Workspace plus Yaver Serverless.
The user can start vibing from the app, run Claude Code/Codex/OpenCode, preview
apps, and deploy/export the app through Yaver Serverless. Provider choice is
internal. The UI may show a compact status label:

- Machine: `Hetzner`, `AWS`, `GCP`, `Azure`, or `Alibaba`.
- Inference: `BYO`, `DeepSeek`, `Gemini`, `Azure AI`, `DashScope`, or `External`.
- State: `Waking`, `Running`, `Asleep`, `Usage limit reached`, or `Needs setup`.

Do not build a provider picker for normies. Placement policy decides provider by
capability, credits, cost, wake latency, budget, and fleet health.

## Hard Rules

- Public repo: never commit cloud tokens, billing ids, account ids, private IPs,
  customer IPs, relay secrets, model API keys, OAuth tokens, or screenshots of
  consoles.
- Only touch resources that are unmistakably Yaver-owned by name, tag, label,
  project, subscription, billing setup, or Convex `cloudMachines` row.
- AWS/GCP/Azure/Alibaba must remain fail-closed until live probes pass.
- Credits do not bypass missing capabilities.
- Paid default compute remains Hetzner unless cost telemetry proves otherwise.
- Managed inference must have per-user hard budgets before any request leaves
  Yaver.
- BYO inference must stay first-class and should be preferred for paid users
  when available.
- Idle machines must not keep billing for compute. Park by delete+volume,
  snapshot+delete, or provider-specific equivalent with explicit tests.
- Yaver Serverless apps must not depend on Convex. Yaver itself can use Convex;
  apps built by users run on Yaver Serverless and can be exported/self-hosted.

## Current First Slice

Already implemented:

- `backend/convex/cloudProviders/types.ts`
- `backend/convex/cloudProviders/abstract.ts`
- `backend/convex/cloudProviders/hetzner.ts`
- `backend/convex/cloudProviders/aws.ts`
- `backend/convex/cloudProviders/gcp.ts`
- `backend/convex/cloudProviders/azure.ts`
- `backend/convex/cloudProviders/bedrockInference.ts`
- `backend/convex/cloudProviders/openaiCompatibleInference.ts`
- `backend/convex/cloudProviderPlacement.ts`
- `backend/convex/cloudPoolPlacement.ts`
- `backend/convex/inferencePlacement.ts`
- `backend/convex/inferenceBackends.ts`
- `backend/convex/providerCatalog.ts`
- Provider catalog seeding through `backend/convex/seed.ts`.
- OpenCode model seed rows for BYO and Bedrock DeepSeek in
  `backend/convex/aiModels.ts`.
- Existing Hetzner provisioning is routed through the provider adapter in
  `backend/convex/cloudMachines.ts`.

## Integration Work

1. Re-read `AGENTS.md`, `CLAUDE.md`, this file, and
   `docs/architecture/MULTI_CLOUD_PROVIDER_AUDIT.md`.
2. Grep the current code before trusting any document:
   `rg -n "cloudMachines|ProviderId|cloudProvider|inferencePlacement|platformConfig|Yaver Serverless" backend desktop web mobile docs`.
3. Add a provider probe layer for each workload profile:
   `linux-runner`, `linux-runner-webrtc`, `linux-runner-redroid`,
   `yaver-serverless-host`, and `inference-only`.
4. A provider becomes production-eligible only after its probes verify:
   bootstrap, agent heartbeat, outbound relay, firewall rules, durable volume or
   snapshot restore, park/delete cost stop, tagged cleanup, budget telemetry, and
   workload-specific WebRTC/Redroid/serverless behavior.
5. Wire provider catalog values from Convex `platformConfig` into placement
   selection. Catalog defaults are seeds, not immutable code policy.
6. Wire compute placement into the managed machine creation path so trials can
   use credit-backed providers only when capability gates pass.
7. Add pool storage for warm/parked/leased Cloud Workspace machines. Leases must
   be budget-aware and expire if the user abandons onboarding.
8. Add inference routing through an internal gateway abstraction:
   managed Bedrock/Vertex/Azure/DashScope/external gateway and BYO keys.
9. Implement hard inference caps before invoking managed models. Store usage
   numbers only; do not log prompts, file paths, code, outputs, or secrets.
10. Add Yaver Serverless runtime contract:
    app package, runtime config, database export/import, domain/TLS mapping,
    logs, scale-to-zero, self-host bundle, and provider-backed deploy target.
11. Ensure generated apps can be moved between Yaver-managed serverless,
    dedicated Cloud Workspace machine, and self-hosted user machine.
12. Update web/mobile surfaces to show only compact labels:
    provider label, region label if useful, machine state, inference label, and
    quota state. No provider SKU picker for normies.
13. Add cleanup/doctor/admin probes that list only Yaver-tagged resources and
    refuse ambiguous resources.
14. Add tests for each provider adapter with mocked HTTP, placement behavior,
    seed defaults, no-secret catalogs, and fail-closed gating.

## Provider Enablement Order

1. Hetzner: keep production baseline; finish WebRTC and Yaver Serverless probes.
2. GCP: enable for credit-funded trials after VM/disk/firewall/budget probes and
   Gemini/Gemma inference budget enforcement.
3. AWS: enable after EC2/EBS/security-group/budget probes and Bedrock DeepSeek
   gateway budget enforcement.
4. Azure: enable after VM/disk/NSG/budget probes and Azure AI budget enforcement.
5. Alibaba: add adapter later only after account credits, ECS disk/image,
   firewall, budget, DashScope, and relay probes are understood.

## Verification Commands

Run these before marking the goal complete:

```bash
cd backend
./node_modules/.bin/tsc -p convex/tsconfig.json
./node_modules/.bin/esbuild convex/cloudProviderPlacement.test.mts --bundle --platform=node --format=esm --outfile=/tmp/yaver-cloudProviderPlacement.test.mjs && node --test /tmp/yaver-cloudProviderPlacement.test.mjs
./node_modules/.bin/esbuild convex/cloudPoolPlacement.test.mts --bundle --platform=node --format=esm --outfile=/tmp/yaver-cloudPoolPlacement.test.mjs && node --test /tmp/yaver-cloudPoolPlacement.test.mjs
./node_modules/.bin/esbuild convex/inferencePlacement.test.mts --bundle --platform=node --format=esm --outfile=/tmp/yaver-inferencePlacement.test.mjs && node --test /tmp/yaver-inferencePlacement.test.mjs
./node_modules/.bin/esbuild convex/providerCatalog.test.mts --bundle --platform=node --format=esm --outfile=/tmp/yaver-providerCatalog.test.mjs && node --test /tmp/yaver-providerCatalog.test.mjs
```

Add broader tests as integration points are wired. If a provider operation is
not live-tested, keep that provider `productionEligible: false`.
