import {
  COMPUTE_PROVIDER_CATALOG,
  INFERENCE_PROVIDER_CATALOG,
  PLACEMENT_POLICY_CATALOG,
  type ComputeProviderCatalogEntry,
  type InferenceProviderCatalogEntry,
} from "./providerCatalog";
import type { ProviderId } from "./cloudProviders/types";

export type RelaySliceKind = "public-free" | "relay-pro" | "workspace-sidecar" | "external-private";
export type ComputeSliceState = "none" | "active" | "parking" | "parked" | "resuming" | "error";
export type InferenceSliceMode = "none" | "byo" | "managed" | "trial";

export type RelaySlice = {
  kind: RelaySliceKind;
  includedWithPlan: boolean;
  persistsWhenComputeParked: boolean;
  authorizationBoundary: "device-keys";
  fallbackKinds: RelaySliceKind[];
};

export type ComputeSlice = {
  provider: ProviderId;
  profile: "standard" | "heavy" | "build" | "gpu";
  state: ComputeSliceState;
  durability: "volume-preferred" | "snapshot-fallback" | "ephemeral";
  spendStopsWhenParked: boolean;
  productionEligible: boolean;
};

export type InferenceSlice = {
  mode: InferenceSliceMode;
  backendId?: string;
  provider?: string;
  hardBudgetRequired: boolean;
  usesProviderCredits: boolean;
  keyPolicy: "managed-secret" | "user-vault" | "none";
};

export type WorkspaceRuntimePlan = {
  plan: "free" | "relay-pro" | "cloud-workspace" | "owner-dev";
  relay: RelaySlice;
  compute: ComputeSlice | null;
  inference: InferenceSlice;
};

export type BuildWorkspaceRuntimePlanArgs = {
  plan: WorkspaceRuntimePlan["plan"];
  computeProvider?: ProviderId;
  computeState?: ComputeSliceState;
  computeProfile?: ComputeSlice["profile"];
  inferenceMode?: InferenceSliceMode;
  inferenceBackendId?: string;
  preferWorkspaceSidecarRelay?: boolean;
};

export function buildWorkspaceRuntimePlan(args: BuildWorkspaceRuntimePlanArgs): WorkspaceRuntimePlan {
  const plan = args.plan;
  const computeProvider = args.computeProvider || PLACEMENT_POLICY_CATALOG.paidDefaultProvider;
  const compute = plan === "cloud-workspace" || args.computeState
    ? buildComputeSlice(computeProvider, args.computeProfile || "standard", args.computeState || "parked")
    : null;
  return {
    plan,
    relay: buildRelaySlice(plan, compute?.state || "none", Boolean(args.preferWorkspaceSidecarRelay)),
    compute,
    inference: buildInferenceSlice(args.inferenceMode || defaultInferenceMode(plan), args.inferenceBackendId),
  };
}

function buildRelaySlice(
  plan: WorkspaceRuntimePlan["plan"],
  computeState: ComputeSliceState,
  preferWorkspaceSidecar: boolean,
): RelaySlice {
  if (preferWorkspaceSidecar && computeState === "active") {
    return {
      kind: "workspace-sidecar",
      includedWithPlan: plan === "cloud-workspace",
      persistsWhenComputeParked: false,
      authorizationBoundary: "device-keys",
      fallbackKinds: plan === "free" ? ["public-free"] : ["relay-pro", "public-free"],
    };
  }
  if (plan === "relay-pro" || plan === "cloud-workspace" || plan === "owner-dev") {
    return {
      kind: "relay-pro",
      includedWithPlan: plan === "relay-pro" || plan === "cloud-workspace" || plan === "owner-dev",
      persistsWhenComputeParked: true,
      authorizationBoundary: "device-keys",
      fallbackKinds: ["public-free"],
    };
  }
  return {
    kind: "public-free",
    includedWithPlan: true,
    persistsWhenComputeParked: true,
    authorizationBoundary: "device-keys",
    fallbackKinds: [],
  };
}

function buildComputeSlice(provider: ProviderId, profile: ComputeSlice["profile"], state: ComputeSliceState): ComputeSlice {
  const entry = computeEntry(provider);
  return {
    provider,
    profile,
    state,
    durability: entry?.sleepStrategy === "delete-and-volume" ? "volume-preferred" : "snapshot-fallback",
    spendStopsWhenParked: entry?.sleepStrategy === "delete-and-volume" || entry?.sleepStrategy === "snapshot-and-delete",
    productionEligible: Boolean(entry?.productionEligible),
  };
}

function buildInferenceSlice(mode: InferenceSliceMode, backendId?: string): InferenceSlice {
  if (mode === "none") {
    return {
      mode,
      hardBudgetRequired: false,
      usesProviderCredits: false,
      keyPolicy: "none",
    };
  }
  const entry = backendId ? inferenceEntry(backendId) : defaultInferenceEntry(mode);
  return {
    mode,
    backendId: entry?.id,
    provider: entry?.provider,
    hardBudgetRequired: mode !== "byo" && PLACEMENT_POLICY_CATALOG.hardBudgetRequiredForManagedInference,
    usesProviderCredits: entry?.costPolicy === "credit-first",
    keyPolicy: entry?.keyPolicy || (mode === "byo" ? "user-vault" : "managed-secret"),
  };
}

function defaultInferenceMode(plan: WorkspaceRuntimePlan["plan"]): InferenceSliceMode {
  if (plan === "free" || plan === "relay-pro") return "byo";
  return "byo";
}

function computeEntry(provider: ProviderId): ComputeProviderCatalogEntry | undefined {
  return COMPUTE_PROVIDER_CATALOG.find((entry) => entry.provider === provider);
}

function inferenceEntry(id: string): InferenceProviderCatalogEntry | undefined {
  return INFERENCE_PROVIDER_CATALOG.find((entry) => entry.id === id);
}

function defaultInferenceEntry(mode: InferenceSliceMode): InferenceProviderCatalogEntry | undefined {
  if (mode === "byo") return inferenceEntry("byo-openai-compatible");
  if (mode === "trial") {
    return INFERENCE_PROVIDER_CATALOG.find((entry) => entry.defaultTrialEligible && entry.costPolicy === "credit-first");
  }
  return INFERENCE_PROVIDER_CATALOG.find((entry) => entry.defaultPaidEligible && entry.costPolicy !== "byo-preferred");
}
