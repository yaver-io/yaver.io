import { describe, expect, it } from "bun:test";
import { formatGuestCrashReport, shouldShowGuestCrashReport } from "../../mobile/src/lib/guestCrash";

describe("guest crash report UI", () => {
  it("stays hidden when there is no persisted guest crash", () => {
    expect(shouldShowGuestCrashReport(null)).toBe(false);
    expect(shouldShowGuestCrashReport({})).toBe(false);
  });

  it("renders concise persisted guest crash context", () => {
    expect(formatGuestCrashReport({
      phase: "bridge_started_guest",
      message: "The guest app terminated unexpectedly while Yaver was in phase 'bridge_started_guest'.",
      moduleName: "main",
      appVersion: "1.18.22",
      appBuild: "261",
    })).toEqual([
      "phase: bridge_started_guest",
      "The guest app terminated unexpectedly while Yaver was in phase 'bridge_started_guest'.",
      "module: main",
      "yaver: 1.18.22 (261)",
    ]);
  });
});
