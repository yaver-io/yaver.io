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
