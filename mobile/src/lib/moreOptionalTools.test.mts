import { describe, expect, test } from "bun:test";
import { isOptionalMoreToolEnabled, normalizeOptionalMoreTools } from "./moreOptionalTools";

describe("normalizeOptionalMoreTools", () => {
  test("keeps known tool ids in user order", () => {
    expect(normalizeOptionalMoreTools(["screw-cell", "robot-cell"])).toEqual([
      "screw-cell",
      "robot-cell",
    ]);
  });

  test("drops unknown, duplicate, and non-string values", () => {
    expect(normalizeOptionalMoreTools(["robot-cell", "unknown", "robot-cell", 42, null])).toEqual([
      "robot-cell",
    ]);
  });

  test("defaults non-arrays to an empty list", () => {
    expect(normalizeOptionalMoreTools(undefined)).toEqual([]);
    expect(normalizeOptionalMoreTools({})).toEqual([]);
  });

  test("treats optional More tools as hidden until explicitly enabled", () => {
    expect(isOptionalMoreToolEnabled(undefined, "robot-cell")).toBe(false);
    expect(isOptionalMoreToolEnabled([], "screw-cell")).toBe(false);
    expect(isOptionalMoreToolEnabled(["screw-cell"], "screw-cell")).toBe(true);
  });
});
