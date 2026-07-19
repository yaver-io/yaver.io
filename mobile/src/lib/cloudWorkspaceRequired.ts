export interface CloudWorkspaceRequiredPlacement {
  id?: string;
  lane?: string;
  resourceClass?: string;
  targetDeviceId?: string | null;
  cloudMachineId?: string | null;
  subscriptionPlan?: string | null;
  entitlement?: string | null;
  status?: string;
  reason?: string;
  wakeRequired?: boolean;
  wakeTargetMs?: number | null;
  estimatedCreditCost?: number | null;
  creditEstimate?: { display?: string | null } | null;
}

export interface CloudWorkspaceRequiredActivation {
  ok?: boolean;
  action?: string;
  productId?: string;
  machineId?: string;
  machineType?: string;
  currentMachineType?: string;
  wakeRunId?: string | null;
  targetDeviceId?: string;
  machineStatus?: string;
  phase?: string;
  profileMatched?: boolean;
  reason?: string;
  error?: string;
}

export class CloudWorkspaceRequiredError extends Error {
  pendingTaskId: string;
  placement?: CloudWorkspaceRequiredPlacement;
  activation?: CloudWorkspaceRequiredActivation;
  reason?: string;

  constructor(args: {
    pendingTaskId: string;
    placement?: CloudWorkspaceRequiredPlacement;
    activation?: CloudWorkspaceRequiredActivation;
    reason?: string;
  }) {
    super(args.reason || args.activation?.reason || args.placement?.reason || "Cloud Workspace is required for this task.");
    this.name = "CloudWorkspaceRequiredError";
    this.pendingTaskId = args.pendingTaskId;
    this.placement = args.placement;
    this.activation = args.activation;
    this.reason = args.reason;
  }
}

export async function decodeCloudWorkspaceRequiredError(res: Response): Promise<CloudWorkspaceRequiredError | null> {
  if (res.status !== 409) return null;
  try {
    const data = await res.clone().json();
    if (data?.action !== "cloud_workspace_required") return null;
    const pendingTaskId = typeof data.pendingTaskId === "string" ? data.pendingTaskId.trim() : "";
    if (!pendingTaskId) return null;
    return new CloudWorkspaceRequiredError({
      pendingTaskId,
      placement: data.placement && typeof data.placement === "object" ? data.placement : undefined,
      activation: data.activation && typeof data.activation === "object" ? data.activation : undefined,
      reason: typeof data.reason === "string" ? data.reason : undefined,
    });
  } catch {
    return null;
  }
}
