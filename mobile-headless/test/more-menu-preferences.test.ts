import { describe, expect, it } from "bun:test";
import {
  OPTIONAL_MORE_TOOLS,
  isOptionalMoreToolEnabled,
  normalizeOptionalMoreTools,
} from "../../mobile/src/lib/moreOptionalTools";

describe("More menu optional tool preferences", () => {
  it("keeps rare tools out of the default More surface", () => {
    for (const tool of OPTIONAL_MORE_TOOLS) {
      expect(isOptionalMoreToolEnabled(undefined, tool.id)).toBe(false);
      expect(isOptionalMoreToolEnabled([], tool.id)).toBe(false);
    }
  });

  it("shows only tools explicitly enabled by Convex userSettings", () => {
    const settingsPayload = ["robot-cell", "screw-cell"];

    expect(isOptionalMoreToolEnabled(settingsPayload, "robot-cell")).toBe(true);
    expect(isOptionalMoreToolEnabled(settingsPayload, "screw-cell")).toBe(true);
    expect(isOptionalMoreToolEnabled(settingsPayload, "printer")).toBe(false);
  });

  it("normalizes stale Convex values before the mobile UI reads them", () => {
    expect(normalizeOptionalMoreTools(["robot-cell", "unknown-tool", "robot-cell", null])).toEqual([
      "robot-cell",
    ]);
  });
});
