import type {
  AttachEgressIpRequest,
  BudgetStatus,
  CostEstimate,
  CostEstimateRequest,
  EgressIpReservation,
  ReleaseEgressIpRequest,
  ReserveEgressIpRequest,
  CreateMachineRequest,
  CreateMachineResult,
  CreateVolumeRequest,
  CreateVolumeResult,
  DeleteMachineRequest,
  DeleteVolumeRequest,
  FirewallRequest,
  InferenceCostEstimate,
  InferenceProviderId,
  InferenceEstimateRequest,
  ListTaggedResourcesRequest,
  MachineProfile,
  MachineStatusRequest,
  ManagedInferenceRequest,
  ManagedInferenceResult,
  ModelCapability,
  ProviderCapabilities,
  ProviderId,
  ProviderMachineStatus,
  RegionOption,
  ResolveSkuRequest,
  SkuDecision,
  SnapshotMachineRequest,
  SnapshotResult,
  TaggedResource,
  WakeFromSnapshotRequest,
  WakeFromVolumeRequest,
} from "./types";

export class ProviderOperationError extends Error {
  readonly provider: InferenceProviderId;
  readonly operation: string;
  readonly code: string;

  constructor(args: {
    provider: InferenceProviderId;
    operation: string;
    code: string;
    message: string;
  }) {
    super(args.message);
    this.name = "ProviderOperationError";
    this.provider = args.provider;
    this.operation = args.operation;
    this.code = args.code;
  }
}

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
  abstract listYaverTaggedResources(req?: ListTaggedResourcesRequest): Promise<TaggedResource[]>;

  /**
   * Stable egress identity. Default = unsupported, so a provider that has not
   * implemented it fails LOUDLY at the call instead of silently handing back a
   * churning address. A provider overriding these MUST also declare
   * "stable-egress-ip" in describeCapabilities — and must not declare it
   * otherwise.
   */
  async reserveEgressIp(_req: ReserveEgressIpRequest): Promise<EgressIpReservation> {
    return this.unsupported("reserveEgressIp");
  }

  async attachEgressIp(_req: AttachEgressIpRequest): Promise<void> {
    return this.unsupported("attachEgressIp");
  }

  async releaseEgressIp(_req: ReleaseEgressIpRequest): Promise<void> {
    return this.unsupported("releaseEgressIp");
  }

  protected unsupported(operation: string, message?: string): never {
    throw new ProviderOperationError({
      provider: this.id,
      operation,
      code: "unsupported_operation",
      message: message || `${this.id} provider does not support ${operation}`,
    });
  }
}

export abstract class AbstractInferenceProvider {
  abstract readonly id: InferenceProviderId;
  abstract describeModels(): Promise<ModelCapability[]>;
  abstract estimateInferenceCost(req: InferenceEstimateRequest): Promise<InferenceCostEstimate>;
  abstract invoke(req: ManagedInferenceRequest): Promise<ManagedInferenceResult>;
}
