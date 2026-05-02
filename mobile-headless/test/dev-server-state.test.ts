import { describe, expect, it } from "bun:test";
import { isActiveDevServerStatus } from "../../mobile/src/lib/devServerState";

describe("dev server active state", () => {
  it("treats running sessions as active", () => {
    expect(isActiveDevServerStatus({
      framework: "expo",
      running: true,
      port: 8081,
      bundleUrl: "/dev/",
      hotReload: true,
    })).toBe(true);
  });

  it("keeps build-only sessions active while native compile is in progress", () => {
    expect(isActiveDevServerStatus({
      framework: "expo",
      running: false,
      building: true,
      port: 8081,
      bundleUrl: "/dev/",
      hotReload: true,
      workDir: "/tmp/app",
    })).toBe(true);
  });

  it("drops idle sessions", () => {
    expect(isActiveDevServerStatus({
      framework: "expo",
      running: false,
      building: false,
      port: 8081,
      bundleUrl: "/dev/",
      hotReload: true,
    })).toBe(false);
  });
});
