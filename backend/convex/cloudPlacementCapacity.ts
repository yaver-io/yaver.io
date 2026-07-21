export type CloudWorkspaceProfile = "standard" | "heavy" | "build";

export type CloudWorkspaceProfilePolicy = {
  profile: CloudWorkspaceProfile;
  resourceClass: "standard" | "heavy" | "build";
  ramGb: number;
  vcpu: number;
  standardCreditWeight: number;
  defaultIncludedStandardCredits: number;
  minimumNetMargin: number;
};

// 120 standard-credit hours per period — $29 (≈€26.7) buys 120 h on the
// DEFAULT class. See docs/architecture/yaver-four-tier-deep-analysis.md §6.
const CLOUD_WORKSPACE_INCLUDED_STANDARD_CREDITS = 120;

const PROFILE_POLICY: Record<CloudWorkspaceProfile, Omit<CloudWorkspaceProfilePolicy, "defaultIncludedStandardCredits" | "minimumNetMargin">> = {
  // ─── Class ladder ────────────────────────────────────────────────────────
  // Specs match the four-tier plan: the DEFAULT is 2c/4GB, because the default
  // path (RN + TypeScript, Chrome/WebRTC preview, Hermes pushed to the user's
  // OWN phone) deliberately avoids Redroid and Gradle — the only two workloads
  // that genuinely need more memory. Capacity is opt-in.
  //
  // standardCreditWeight is VALIDATED against measured Hetzner prices
  // (2026-07-21, gross €/h at fsn1):
  //     standard cpx22 €0.0368  →  measured 1.0x, charged 1x
  //     heavy    cpx32 €0.0673  →  measured 1.8x, charged 2x
  //     build    cpx42 €0.1314  →  measured 3.6x, charged 4x
  // Charged weights are deliberately ROUNDED UP. A weight below the true cost
  // ratio lets a user convert cheap credits into expensive compute and invert
  // the margin; rounding up errs toward us and keeps the numbers memorable.
  // Re-validate whenever the default SKUs change — `hcloud server-type list`.
  standard: {
    profile: "standard",
    resourceClass: "standard",
    ramGb: 4,
    vcpu: 2,
    standardCreditWeight: 1,
  },
  heavy: {
    profile: "heavy",
    resourceClass: "heavy",
    ramGb: 8,
    vcpu: 4,
    standardCreditWeight: 2,
  },
  build: {
    profile: "build",
    resourceClass: "build",
    ramGb: 16,
    vcpu: 8,
    standardCreditWeight: 4,
  },
};

export function cloudMachineTypeForPlacement(resourceClass: unknown): "standard" | "heavy" | "build" {
  const value = String(resourceClass || "").trim();
  if (value === "build") return "build";
  if (value === "heavy") return "heavy";
  return "standard";
}

export function cloudWorkspaceProfileForPlacement(resourceClass: unknown): CloudWorkspaceProfile {
  return cloudMachineTypeForPlacement(resourceClass);
}

export function cloudWorkspaceProfilePolicy(profileOrResourceClass: unknown): CloudWorkspaceProfilePolicy {
  const profile = cloudWorkspaceProfileForPlacement(profileOrResourceClass);
  return {
    ...PROFILE_POLICY[profile],
    defaultIncludedStandardCredits: CLOUD_WORKSPACE_INCLUDED_STANDARD_CREDITS,
    minimumNetMargin: 0.4,
  };
}

export function includedHoursForCloudWorkspaceProfile(profileOrResourceClass: unknown, standardCredits = CLOUD_WORKSPACE_INCLUDED_STANDARD_CREDITS): number {
  const policy = cloudWorkspaceProfilePolicy(profileOrResourceClass);
  const credits = Number.isFinite(standardCredits) && standardCredits >= 0 ? standardCredits : CLOUD_WORKSPACE_INCLUDED_STANDARD_CREDITS;
  return credits / policy.standardCreditWeight;
}

export function cloudWorkspaceProfileLabel(profileOrResourceClass: unknown): string {
  const policy = cloudWorkspaceProfilePolicy(profileOrResourceClass);
  if (policy.profile === "build") return "Build workspace";
  if (policy.profile === "heavy") return "Heavy workspace";
  return "Standard workspace";
}

export function cloudMachineMeetsPlacement(machine: any, resourceClass: unknown): boolean {
  const ramGb = Number(machine?.specs?.ramGb ?? 0);
  const type = String(machine?.machineType || "").trim();
  const resource = String(resourceClass || "").trim();
  // Thresholds track the ladder above (build 16 GB, heavy 8 GB). They were
  // 24/16 when standard was 8 GB; leaving them there after the ladder moved
  // would have made every heavy/build box fail its own placement check.
  if (resource === "build") return type === "build" || type === "cpu" || type === "gpu" || ramGb >= 16;
  if (resource === "heavy") return type === "heavy" || type === "build" || type === "cpu" || type === "gpu" || ramGb >= 8;
  return true;
}

export function cloudMachineEligibleForPlacement(machine: any): boolean {
  if ((machine?.origin ?? "managed") !== "managed") return false;
  const provider = String(machine?.provider || "hetzner").trim().toLowerCase();
  if (provider && provider !== "hetzner") return false;
  const status = String(machine?.status || "").trim().toLowerCase();
  return ["active", "paused", "suspended", "resuming", "provisioning", "grace"].includes(status);
}

export function selectCloudMachineForPlacement(
  machines: any[],
  resourceClass: unknown,
  placementMachineId?: unknown,
) {
  const sortedMachines = machines.filter(cloudMachineEligibleForPlacement).sort(
    (a: any, b: any) => (b.updatedAt ?? b.createdAt ?? 0) - (a.updatedAt ?? a.createdAt ?? 0),
  );
  const placementMachine = placementMachineId
    ? sortedMachines.find((machine: any) => String(machine._id) === String(placementMachineId))
    : null;
  if (placementMachine && cloudMachineMeetsPlacement(placementMachine, resourceClass)) {
    return placementMachine;
  }
  return sortedMachines.find((candidate: any) => cloudMachineMeetsPlacement(candidate, resourceClass)) ?? null;
}

function hasPersistentRecoverySource(machine: any): boolean {
  return Boolean(machine?.volumeId && machine?.baseImageId);
}

export function selectResizeSourceForPlacement(
  machines: any[],
  resourceClass: unknown,
  placementMachineId?: unknown,
) {
  const desiredType = cloudMachineTypeForPlacement(resourceClass);
  if (desiredType === "standard") return null;

  const sortedMachines = machines.filter(cloudMachineEligibleForPlacement).sort(
    (a: any, b: any) => (b.updatedAt ?? b.createdAt ?? 0) - (a.updatedAt ?? a.createdAt ?? 0),
  );
  const isResizeCandidate = (machine: any) =>
    hasPersistentRecoverySource(machine) && !cloudMachineMeetsPlacement(machine, resourceClass);
  const placementMachine = placementMachineId
    ? sortedMachines.find((machine: any) => String(machine._id) === String(placementMachineId))
    : null;
  if (placementMachine && isResizeCandidate(placementMachine)) {
    return placementMachine;
  }
  return sortedMachines.find(isResizeCandidate) ?? null;
}
