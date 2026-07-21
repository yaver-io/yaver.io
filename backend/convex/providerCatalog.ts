import type {
  InferenceBackendDescriptor,
  MachineProfile,
  ModelCapability,
  ProviderCapabilities,
  ProviderId,
  RequiredCapability,
} from "./cloudProviders/types";

type UserVisibleCloudLabel = "Hetzner" | "GCP" | "AWS" | "Azure" | "Alibaba";
type UserVisibleInferenceLabel = "BYO" | "DeepSeek" | "Gemini" | "Azure AI" | "DashScope" | "External";

export type ComputeProviderCatalogEntry = ProviderCapabilities & {
  label: string;
  userVisibleLabel: UserVisibleCloudLabel;
  defaultTrialEligible: boolean;
  defaultPaidEligible: boolean;
  creditAware: boolean;
  sleepStrategy: "delete-and-volume" | "stop-instance" | "snapshot-and-delete" | "unsupported";
  userFacingDisclosure: "provider-label-only";
};

export type InferenceProviderCatalogEntry = InferenceBackendDescriptor & {
  userVisibleLabel: UserVisibleInferenceLabel;
  defaultTrialEligible: boolean;
  defaultPaidEligible: boolean;
  costPolicy: "byo-preferred" | "credit-first" | "metered-managed" | "disabled";
};

export type PlacementPolicyCatalog = {
  productionEligibleRequired: boolean;
  hideProviderDetailsByDefault: boolean;
  userVisibleComputeFields: string[];
  userVisibleInferenceFields: string[];
  paidDefaultProvider: ProviderId;
  trialProviderOrder: ProviderId[];
  paidProviderOrder: ProviderId[];
  sleepIdleAfterMs: number;
  deleteIdleAfterMs: number;
  hardBudgetRequiredForManagedInference: boolean;
};

const LINUX_RUNNER_CAPABILITIES: RequiredCapability[] = [
  "cloud-init",
  "docker",
  "systemd",
  "durable-volume",
  "delete-stops-compute-spend",
  "provider-status",
  "tagged-cleanup",
  "budget-telemetry",
  "outbound-relay",
  "runner-claude",
  "runner-codex",
  "runner-opencode",
];

const WEBRTC_EXTRA_CAPABILITIES: RequiredCapability[] = [
  "udp-ingress",
  "stable-endpoint",
  "webrtc-probe",
];

const REDROID_EXTRA_CAPABILITIES: RequiredCapability[] = [
  "redroid-probe",
];

const SERVERLESS_EXTRA_CAPABILITIES: RequiredCapability[] = [
  "serverless-runtime",
  "custom-domain-tls",
];

export const COMPUTE_PROVIDER_CATALOG: ComputeProviderCatalogEntry[] = [
  {
    provider: "hetzner",
    label: "Hetzner Cloud",
    userVisibleLabel: "Hetzner",
    profiles: ["linux-runner"],
    capabilities: LINUX_RUNNER_CAPABILITIES,
    regions: ["fsn1", "nbg1", "hel1", "ash", "hil"],
    productionEligible: true,
    defaultTrialEligible: false,
    defaultPaidEligible: true,
    creditAware: false,
    sleepStrategy: "delete-and-volume",
    userFacingDisclosure: "provider-label-only",
    notes: [
      "Baseline paid compute provider.",
      "Current production path supports Linux runner machines. WebRTC, Redroid, and Yaver Serverless require probes before enabling.",
    ],
  },
  {
    provider: "gcp",
    label: "Google Cloud",
    userVisibleLabel: "GCP",
    profiles: [] satisfies MachineProfile[],
    capabilities: [
      ...LINUX_RUNNER_CAPABILITIES,
      ...WEBRTC_EXTRA_CAPABILITIES,
      ...SERVERLESS_EXTRA_CAPABILITIES,
      "image-boot",
      "snapshot-fallback",
      "first-party-inference",
    ],
    regions: ["europe-west1", "europe-west4", "us-central1"],
    productionEligible: false,
    defaultTrialEligible: true,
    defaultPaidEligible: false,
    creditAware: true,
    sleepStrategy: "snapshot-and-delete",
    userFacingDisclosure: "provider-label-only",
    notes: [
      "Candidate for credit-funded trial compute and Gemini inference.",
      "Keep disabled until live probes verify Yaver agent bootstrap, relay/WebRTC, and teardown semantics.",
    ],
  },
  {
    provider: "aws",
    label: "Amazon Web Services",
    userVisibleLabel: "AWS",
    profiles: [] satisfies MachineProfile[],
    capabilities: [
      ...LINUX_RUNNER_CAPABILITIES,
      ...WEBRTC_EXTRA_CAPABILITIES,
      ...REDROID_EXTRA_CAPABILITIES,
      ...SERVERLESS_EXTRA_CAPABILITIES,
      "image-boot",
      "snapshot-fallback",
      "first-party-inference",
    ],
    regions: ["eu-central-1", "eu-west-1", "us-east-1"],
    productionEligible: false,
    defaultTrialEligible: true,
    defaultPaidEligible: false,
    creditAware: true,
    sleepStrategy: "snapshot-and-delete",
    userFacingDisclosure: "provider-label-only",
    notes: [
      "Candidate for credit-funded trial compute plus Bedrock DeepSeek.",
      "Enable only after EC2 quota, security group, Redroid, and WebRTC probes pass.",
    ],
  },
  {
    provider: "azure",
    label: "Microsoft Azure",
    userVisibleLabel: "Azure",
    profiles: [] satisfies MachineProfile[],
    capabilities: [
      ...LINUX_RUNNER_CAPABILITIES,
      ...WEBRTC_EXTRA_CAPABILITIES,
      ...SERVERLESS_EXTRA_CAPABILITIES,
      "image-boot",
      "snapshot-fallback",
      "first-party-inference",
    ],
    regions: ["westeurope", "northeurope", "eastus"],
    productionEligible: false,
    defaultTrialEligible: true,
    defaultPaidEligible: false,
    creditAware: true,
    sleepStrategy: "snapshot-and-delete",
    userFacingDisclosure: "provider-label-only",
    notes: [
      "Candidate for Azure credit-funded compute and Azure AI.",
      "Keep disabled until VM, disk, firewall, budget, and Yaver Serverless probes pass.",
    ],
  },
  {
    provider: "alibaba",
    label: "Alibaba Cloud",
    userVisibleLabel: "Alibaba",
    profiles: [] satisfies MachineProfile[],
    capabilities: [
      ...LINUX_RUNNER_CAPABILITIES,
      ...WEBRTC_EXTRA_CAPABILITIES,
      ...SERVERLESS_EXTRA_CAPABILITIES,
      "image-boot",
      "snapshot-fallback",
      "first-party-inference",
    ],
    regions: ["eu-central-1", "me-central-1", "us-west-1"],
    productionEligible: false,
    defaultTrialEligible: false,
    defaultPaidEligible: false,
    creditAware: true,
    sleepStrategy: "snapshot-and-delete",
    userFacingDisclosure: "provider-label-only",
    notes: [
      "Later candidate for ECS compute and DashScope/Qwen inference.",
      "No adapter should be enabled until account, quota, billing, teardown, and relay probes exist.",
    ],
  },
];

const BEDROCK_DEEPSEEK_MODELS: ModelCapability[] = [
  { id: "deepseek.r1-v1:0", label: "DeepSeek R1", contextTokens: 128_000 },
  { id: "deepseek.v3-1-v1:0", label: "DeepSeek V3.1", contextTokens: 128_000 },
];

export const INFERENCE_PROVIDER_CATALOG: InferenceProviderCatalogEntry[] = [
  {
    id: "bedrock",
    provider: "aws",
    kind: "bedrock",
    label: "Amazon Bedrock",
    userVisibleLabel: "DeepSeek",
    productionEligible: false,
    keyPolicy: "managed-secret",
    models: BEDROCK_DEEPSEEK_MODELS,
    defaultTrialEligible: true,
    defaultPaidEligible: false,
    costPolicy: "credit-first",
  },
  {
    id: "vertex-gemini",
    provider: "gcp",
    kind: "vertex",
    label: "Vertex AI Gemini",
    userVisibleLabel: "Gemini",
    productionEligible: false,
    keyPolicy: "managed-secret",
    models: [
      { id: "gemini-managed-default", label: "Gemini" },
      { id: "gemma-managed-default", label: "Gemma" },
    ],
    defaultTrialEligible: true,
    defaultPaidEligible: false,
    costPolicy: "credit-first",
  },
  {
    id: "azure-ai",
    provider: "azure",
    kind: "azure-ai",
    label: "Azure AI",
    userVisibleLabel: "Azure AI",
    productionEligible: false,
    keyPolicy: "managed-secret",
    models: [{ id: "azure-ai-managed-default", label: "Azure AI" }],
    defaultTrialEligible: true,
    defaultPaidEligible: false,
    costPolicy: "credit-first",
  },
  {
    id: "dashscope",
    provider: "alibaba",
    kind: "dashscope",
    label: "Alibaba DashScope",
    userVisibleLabel: "DashScope",
    productionEligible: false,
    keyPolicy: "managed-secret",
    models: [{ id: "qwen-managed-default", label: "Qwen" }],
    defaultTrialEligible: false,
    defaultPaidEligible: false,
    costPolicy: "credit-first",
  },
  {
    id: "external-openai-compatible",
    provider: "external",
    kind: "openai-compatible",
    label: "External OpenAI-compatible gateway",
    userVisibleLabel: "External",
    productionEligible: false,
    keyPolicy: "managed-secret",
    models: [{ id: "custom", label: "Custom" }],
    defaultTrialEligible: false,
    defaultPaidEligible: false,
    costPolicy: "metered-managed",
  },
  {
    id: "byo-openai-compatible",
    provider: "byo",
    kind: "byo",
    label: "Bring your own inference",
    userVisibleLabel: "BYO",
    productionEligible: true,
    keyPolicy: "user-vault",
    models: [{ id: "custom", label: "Custom" }],
    defaultTrialEligible: true,
    defaultPaidEligible: true,
    costPolicy: "byo-preferred",
  },
];

export const INFERENCE_MODEL_CATALOG = [
  {
    runnerId: "opencode",
    modelId: "bedrock/deepseek.r1-v1:0",
    userVisibleLabel: "DeepSeek",
    backendId: "bedrock",
    provider: "aws",
    managed: true,
  },
  {
    runnerId: "opencode",
    modelId: "bedrock/deepseek.v3-1-v1:0",
    userVisibleLabel: "DeepSeek",
    backendId: "bedrock",
    provider: "aws",
    managed: true,
  },
  {
    runnerId: "opencode",
    modelId: "byo/openai-compatible",
    userVisibleLabel: "BYO",
    backendId: "byo-openai-compatible",
    provider: "byo",
    managed: false,
  },
] as const;

export const PLACEMENT_POLICY_CATALOG: PlacementPolicyCatalog = {
  productionEligibleRequired: true,
  hideProviderDetailsByDefault: true,
  userVisibleComputeFields: ["providerLabel", "regionLabel", "machineState"],
  userVisibleInferenceFields: ["sourceLabel", "modelLabel", "byoRequired"],
  paidDefaultProvider: "hetzner",
  trialProviderOrder: ["gcp", "aws", "azure", "hetzner", "alibaba"],
  paidProviderOrder: ["hetzner", "gcp", "aws", "azure", "alibaba"],
  sleepIdleAfterMs: 15 * 60 * 1000,
  deleteIdleAfterMs: 2 * 60 * 60 * 1000,
  hardBudgetRequiredForManagedInference: true,
};

export function providerCatalogDefaults(): Record<string, string> {
  return {
    cloud_provider_catalog: JSON.stringify(COMPUTE_PROVIDER_CATALOG),
    inference_provider_catalog: JSON.stringify(INFERENCE_PROVIDER_CATALOG),
    inference_model_catalog: JSON.stringify(INFERENCE_MODEL_CATALOG),
    placement_policy_catalog: JSON.stringify(PLACEMENT_POLICY_CATALOG),
  };
}
