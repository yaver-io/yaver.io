import type { Task, TaskSliceContract } from "./quic";

export type FleetTaskMetadata = {
  role: "master" | "worker";
  stage: string;
  label: string;
  detail: string;
  runId?: string;
  nodeId?: string;
};

const STAGE_LABELS: Record<string, string> = {
  architecture: "Architecture",
  implementation: "Implementation",
  validation: "Validation",
};

export function fleetMetadataFromSlice(contract?: TaskSliceContract): FleetTaskMetadata | null {
  const role = contract?.orchestrationRole;
  if (role !== "master" && role !== "worker") return null;
  const stage = String(contract?.orchestrationStage || "").trim();
  const stageLabel = STAGE_LABELS[stage] || (stage ? stage.charAt(0).toUpperCase() + stage.slice(1) : "Orchestration");
  const roleLabel = role === "master" ? "Master" : "Worker";
  return {
    role,
    stage,
    label: `${roleLabel} · ${stageLabel}`,
    detail: role === "master"
      ? stage === "validation" ? "Independent review and evidence check" : "Architecture, roadmap, and test strategy"
      : "Bounded implementation and summarized report",
    runId: contract?.runId,
    nodeId: contract?.nodeId,
  };
}

export function fleetMetadataForTask(task?: Pick<Task, "sliceContract"> | null): FleetTaskMetadata | null {
  return fleetMetadataFromSlice(task?.sliceContract);
}
