import { describe, expect, it } from "bun:test";
import { MobileClient } from "../src/mobile-client";
import { startMockAgent } from "../src/mock-agent";
import {
  normalizeProjectChipName,
  preferredDefaultModelForRunner,
  preferredDefaultRunnerForDevice,
  resolveModelForRemoteSend,
  resolveRunnerForRemoteSend,
} from "../../mobile/src/lib/remoteCodingSelection";

describe("remote box coding flow", () => {
  it("auto-selects a reachable coding-ready box over a non-coding primary", () => {
    const mobile = new MobileClient();
    const target = mobile.pickAutoConnectTarget(
      [
        { id: "mac", name: "MacBook", host: "mac.local", online: true, os: "darwin" },
        { id: "hetzner", name: "remote-linux", host: "remote", online: true, os: "linux" },
      ],
      "mac",
      {
        probes: {
          mac: { reachable: true, codingReady: false, codingRunners: [] },
          hetzner: {
            reachable: true,
            codingReady: true,
            codingRunners: [{ id: "codex", ready: true }],
          },
        },
      },
    );

    expect(target?.id).toBe("hetzner");
  });

  it("ignores stale online flags when explicit probes say a box is unreachable", () => {
    const mobile = new MobileClient();
    const target = mobile.pickAutoConnectTarget(
      [
        { id: "stale", name: "stale-primary", host: "stale", online: true },
        { id: "live", name: "live-secondary", host: "live", online: true },
      ],
      "stale",
      {
        secondaryDeviceId: "live",
        probes: {
          stale: { reachable: false, codingReady: true },
          live: { reachable: true, codingReady: false },
        },
      },
    );

    expect(target?.id).toBe("live");
  });

  it("resolves OpenCode/GLM runner/model from the focused remote box at send time", () => {
    const runner = resolveRunnerForRemoteSend({
      activeDeviceId: "hetzner",
      primaryRunnerByDevice: { hetzner: "opencode" },
      selectedRunner: "claude",
      fallbackRunner: "claude",
      userPickedRunner: false,
    });
    const model = resolveModelForRemoteSend({
      runnerId: runner,
      activeDevice: { id: "hetzner", name: "remote-linux", os: "linux" } as any,
      primaryModelByDevice: { hetzner: "zai-coding-plan/glm-4.7" },
      selectedModel: "sonnet",
      fallbackModel: "sonnet",
      availableRunners: [
        { id: "opencode", models: [{ id: "zai-coding-plan/glm-4.7", isDefault: true }] },
        { id: "codex", models: [{ id: "gpt-5.3-codex", isDefault: true }] },
        { id: "claude", models: [{ id: "claude-opus-4-7", isDefault: true }] },
      ],
      userPickedModel: false,
    });

    expect(runner).toBe("opencode");
    expect(model).toBe("zai-coding-plan/glm-4.7");
  });

  it("prefers OpenCode with GLM defaults on Hetzner-style Linux boxes", () => {
    const device = { name: "Hetzner box", hostName: "yaver-cpu-1234", os: "linux" };
    expect(preferredDefaultRunnerForDevice(device, "dev@example.com", ["claude", "codex", "opencode"])).toBe("opencode");
    expect(preferredDefaultModelForRunner("opencode", device, "dev@example.com")).toBe("zai-coding-plan/glm-4.7");
  });

  it("does not show transport addresses or root as task context chips", () => {
    expect(normalizeProjectChipName("root")).toBe("");
    expect(normalizeProjectChipName("/root")).toBe("");
    expect(normalizeProjectChipName("workspace")).toBe("workspace");
  });

  it("dogfoods OpenCode config through the Go-agent endpoint", async () => {
    const agent = await startMockAgent({ token: "mock-token" });
    try {
      const mobile = new MobileClient({ agentBaseUrl: agent.baseUrl, authToken: "mock-token" });
      const saved = await mobile.saveOpenCodeConfig({
        defaultAgent: "build",
        model: "zai-coding-plan/glm-4.7",
        providers: [{ id: "zai-coding-plan", apiKey: "test-key" }],
      });

      expect(saved.ok).toBe(true);

      const cfg = await mobile.getOpenCodeConfig();
      expect(cfg?.model).toBe("zai-coding-plan/glm-4.7");
      expect(cfg?.providers?.some((p) => p.id === "zai-coding-plan" && p.hasApiKey)).toBe(true);
    } finally {
      await agent.close();
    }
  });
});
