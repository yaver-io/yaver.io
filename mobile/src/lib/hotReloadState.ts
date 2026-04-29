import type { IncidentEvent, OperationState } from "./quic";

function normalizePath(value?: string | null): string {
  return typeof value === "string" ? value.trim() : "";
}

export function visibleReloadOperations(
  operations: OperationState[],
  activeProjectPath?: string | null,
): OperationState[] {
  const normalizedActivePath = normalizePath(activeProjectPath);
  return operations.filter((op) => {
    if (op.status === "completed") return false;
    if (!normalizedActivePath) return true;
    const opProjectPath = normalizePath(op.projectPath);
    return !opProjectPath || opProjectPath === normalizedActivePath;
  });
}

export function visibleReloadIncidents(
  incidents: IncidentEvent[],
  currentOperation: OperationState | null,
  activeProjectPath?: string | null,
): IncidentEvent[] {
  if (!currentOperation) return [];
  return incidents.filter((incident) => {
    if (incident.resolved) return false;
    if (currentOperation?.incidentIds?.includes(incident.id)) return true;
    if (currentOperation?.id && incident.operationId === currentOperation.id) return true;
    return false;
  });
}
