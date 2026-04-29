import { describe, expect, it } from "bun:test";
import {
  shouldShowCurrentReloadIncident,
  visibleReloadIncidents,
  visibleReloadOperations,
} from "../../mobile/src/lib/hotReloadState";
import type { IncidentEvent, OperationState } from "../../mobile/src/lib/quic";

function op(partial: Partial<OperationState>): OperationState {
  return {
    id: partial.id || "op",
    kind: partial.kind || "build_native",
    status: partial.status || "running",
    startedAt: partial.startedAt || 1,
    updatedAt: partial.updatedAt || 1,
    ...partial,
  };
}

function incident(partial: Partial<IncidentEvent>): IncidentEvent {
  return {
    id: partial.id || "incident",
    timestamp: partial.timestamp || 1,
    severity: partial.severity || "error",
    category: partial.category || "build",
    code: partial.code || "build.failed",
    source: partial.source || "agent",
    title: partial.title || "Build failed",
    userMessage: partial.userMessage || "Build failed",
    logsAvailable: partial.logsAvailable ?? false,
    recoverable: partial.recoverable ?? true,
    ...partial,
  };
}

describe("hot reload state filtering", () => {
  it("hides completed operations and keeps active-project operations", () => {
    const operations = [
      op({ id: "completed", status: "completed", projectPath: "/a" }),
      op({ id: "other", status: "running", projectPath: "/b" }),
      op({ id: "active", status: "running", projectPath: "/a" }),
      op({ id: "global", status: "running" }),
    ];
    expect(visibleReloadOperations(operations, "/a").map((item) => item.id)).toEqual(["active", "global"]);
  });

  it("shows no blocker before the current run has an active operation", () => {
    const incidents = [
      incident({ id: "stale", projectPath: "/other" }),
      incident({ id: "resolved", projectPath: "/active", resolved: true }),
      incident({ id: "active", projectPath: "/active" }),
    ];
    expect(visibleReloadIncidents(incidents, null, "/active").map((item) => item.id)).toEqual([]);
  });

  it("keeps incidents linked to the current operation even if project path is missing", () => {
    const currentOperation = op({ id: "op-1", incidentIds: ["incident-1"] });
    const incidents = [
      incident({ id: "incident-1" }),
      incident({ id: "incident-2", operationId: "op-1" }),
      incident({ id: "incident-3", projectPath: "/other" }),
    ];
    expect(
      visibleReloadIncidents(incidents, currentOperation, "/active").map((item) => item.id),
    ).toEqual(["incident-1", "incident-2"]);
  });

  it("hides stale same-project incidents while a newer operation is still running", () => {
    const currentOperation = op({ id: "op-running", projectPath: "/active" });
    const incidents = [
      incident({ id: "stale-path-match", projectPath: "/active" }),
      incident({ id: "linked", operationId: "op-running" }),
    ];
    expect(
      visibleReloadIncidents(incidents, currentOperation, "/active").map((item) => item.id),
    ).toEqual(["linked"]);
  });

  it("keeps the blocker card hidden while the linked operation is still running", () => {
    const currentOperation = op({ id: "op-running", status: "running" });
    const currentIncident = incident({ id: "linked", operationId: "op-running" });
    expect(shouldShowCurrentReloadIncident(currentIncident, currentOperation)).toBe(false);
  });

  it("shows the blocker card after the linked operation settles", () => {
    const currentOperation = op({ id: "op-failed", status: "failed" });
    const currentIncident = incident({ id: "linked", operationId: "op-failed" });
    expect(shouldShowCurrentReloadIncident(currentIncident, currentOperation)).toBe(true);
  });
});
