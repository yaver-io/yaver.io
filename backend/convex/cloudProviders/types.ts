export type ProviderId = "hetzner" | "gcp" | "aws" | "azure" | "alibaba";

export type MachineProfile =
  | "linux-runner"
  | "linux-runner-webrtc"
  | "linux-runner-redroid"
  | "linux-runner-gpu"
  | "yaver-serverless-host"
  | "inference-only";

export type RequiredCapability =
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
  // A reservable public address that SURVIVES the server delete performed by
  // park, so one workspace keeps one outbound identity across wakes. Declare
  // this ONLY if reserve/attach/release are all really implemented — the
  // capability list is a placement gate, and a false declaration here is the
  // same class of bug as claiming "tagged-cleanup" while returning [].
  | "stable-egress-ip"
  | "outbound-relay"
  | "udp-ingress"
  | "stable-endpoint"
  | "webrtc-probe"
  | "redroid-probe"
  | "runner-claude"
  | "runner-codex"
  | "runner-opencode"
  | "serverless-runtime"
  | "custom-domain-tls"
  | "first-party-inference";

export type ProviderCapabilities = {
  provider: ProviderId;
  profiles: MachineProfile[];
  capabilities: RequiredCapability[];
  regions: string[];
  productionEligible: boolean;
  notes?: string[];
};

export type RegionOption = {
  id: string;
  label?: string;
};

export type ResolveSkuRequest = {
  profile: MachineProfile;
  region: string;
  vcpu?: number;
  ramGb?: number;
  diskGb?: number;
};

export type SkuDecision = {
  sku: string;
  arch: "amd64" | "arm64";
  vcpu?: number;
  ramGb?: number;
  diskGb?: number;
  notes?: string;
};

export type CostEstimateRequest = {
  profile: MachineProfile;
  region: string;
  sku: string;
  diskGb?: number;
  hours?: number;
};

export type CostEstimate = {
  currency: "USD" | "EUR";
  estimatedHourlyCompute?: number;
  estimatedMonthlyCompute?: number;
  estimatedMonthlyStorage?: number;
  confidence: "known" | "estimated" | "unknown";
  notes?: string;
};

export type BudgetStatus = {
  provider: ProviderId;
  ok: boolean;
  creditUsdRemaining?: number;
  creditExpiresAt?: number;
  monthToDateSpendUsd?: number;
  hardStopAtUsd?: number;
  lastSyncedAt: number;
  reason?: string;
};

export type CreateVolumeRequest = {
  name: string;
  sizeGb: number;
  region: string;
  tags: Record<string, string>;
};

export type CreateVolumeResult = {
  volumeId: string;
};

export type DeleteVolumeRequest = {
  volumeId: string;
};

export type CreateMachineRequest = {
  name: string;
  region: string;
  sku: string;
  image: string | number;
  userData: string;
  tags: Record<string, string>;
  volumeIds?: string[];
  sshKeyNames?: string[];
  providerOptions?: Record<string, unknown>;
};

export type CreateMachineResult = {
  cloudResourceId: string;
  serverIp?: string;
  providerStatus?: string;
  serverType: string;
};

export type WakeFromVolumeRequest = CreateMachineRequest;

export type WakeFromSnapshotRequest = CreateMachineRequest & {
  snapshotId: string;
};

export type SnapshotMachineRequest = {
  cloudResourceId: string;
  label: string;
};

export type SnapshotResult = {
  snapshotId: string;
};

export type DeleteMachineRequest = {
  cloudResourceId: string;
};

export type MachineStatusRequest = {
  cloudResourceId: string;
};

export type ProviderMachineStatus = {
  status: string;
  rawStatus?: string;
};

export type FirewallRequest = {
  cloudResourceId?: string;
  ports: Array<{ port: number; protocol: "tcp" | "udp"; source?: string }>;
  tags: Record<string, string>;
};

export type TaggedResource = {
  id: string;
  type: "machine" | "volume" | "snapshot" | "image" | "ip" | "firewall" | "unknown";
  provider: ProviderId;
  tags: Record<string, string>;
  status?: string;
};

export type ListTaggedResourcesRequest = {
  tags?: Record<string, string>;
};

/**
 * ─── Stable egress identity ─────────────────────────────────────────────────
 *
 * Park is delete-not-stop, so without a reserved address every wake gives the
 * workspace a brand-new datacenter IP — and the user's mirrored runner
 * credentials therefore reach the vendor from a different address every time.
 *
 * The primitive differs per provider (Hetzner Primary IP with
 * auto_delete:false, AWS Elastic IP, GCP static external IP, Azure Standard
 * static Public IP) but the contract is identical: reserve once, attach on
 * every create, release ONLY on decommission.
 *
 * ⚠️ It must be the address outbound traffic is SOURCED from. An inbound-only
 * primitive (a Hetzner Floating IP, an Azure Load Balancer frontend) does not
 * satisfy this contract even though it looks attached.
 *
 * ⚠️ A reserved address is a DETACHABLE PAID RESOURCE that outlives its server.
 * It must be reclaimed on decommission and listed by listYaverTaggedResources,
 * or it becomes the next silent leak.
 */
export type ReserveEgressIpRequest = {
  name: string;
  region: string;
  tags: Record<string, string>;
  /** Pin into the exact placement scope a machine will be created in. */
  scope?: string;
};

export type EgressIpReservation = {
  egressIpId: string;
  address: string;
  /**
   * Placement scope the address is bound to (Hetzner datacenter, AWS/GCP
   * region, Azure region). A create MUST land inside this scope or the address
   * cannot be attached.
   */
  scope: string;
  /** Ongoing cost while held but NOT attached — the parked-box cost. */
  idleCostUsdPerMonth?: number;
};

export type AttachEgressIpRequest = {
  egressIpId: string;
  cloudResourceId: string;
  providerOptions?: Record<string, unknown>;
};

export type ReleaseEgressIpRequest = {
  egressIpId: string;
};

export type ModelCapability = {
  id: string;
  label?: string;
  contextTokens?: number;
};

export type InferenceEstimateRequest = {
  model: string;
  inputTokens?: number;
  outputTokens?: number;
};

export type InferenceCostEstimate = {
  currency: "USD";
  estimatedUsd?: number;
  confidence: "known" | "estimated" | "unknown";
};

export type InferenceProviderId = ProviderId | "byo" | "external";

export type InferenceBackendKind =
  | "bedrock"
  | "vertex"
  | "azure-ai"
  | "dashscope"
  | "openai-compatible"
  | "byo"
  | "external";

export type ManagedInferenceRequest = {
  model: string;
  input: unknown;
  budgetUsd: number;
  metadata?: Record<string, string>;
};

export type ManagedInferenceResult = {
  output: unknown;
  usage?: {
    inputTokens?: number;
    outputTokens?: number;
    estimatedUsd?: number;
  };
};

export type InferenceBackendDescriptor = {
  id: string;
  provider: InferenceProviderId;
  kind: InferenceBackendKind;
  label: string;
  baseUrl?: string;
  productionEligible: boolean;
  keyPolicy: "managed-secret" | "user-vault" | "none";
  models: ModelCapability[];
};
