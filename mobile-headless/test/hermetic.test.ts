// Hermetic smoke test — spins up an in-process mock agent, drives
// the real MobileClient against it, asserts the happy path works.
// No external services, <1s total runtime.

import { describe, it, expect, beforeAll, afterAll } from "bun:test";
import * as os from "node:os";
import * as path from "node:path";
import * as fs from "node:fs";
import { MobileClient } from "../src/mobile-client";
import { startMockAgent, type MockAgentHandle } from "../src/mock-agent";

let agent: MockAgentHandle;
let mobile: MobileClient;

beforeAll(async () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-hermetic-"));
  process.env.YMH_DATA_DIR = dataDir;
  agent = await startMockAgent({ token: "mock-token" });
  mobile = new MobileClient({
    dataDir,
    authToken: "mock-token",
    convexUrl: agent.baseUrl,
  });
  mobile.useAgentBaseUrl(agent.baseUrl);
});

afterAll(async () => {
  await agent.close();
});

describe("MobileClient against mock agent", () => {
  it("listInstallables returns catalogue", async () => {
    const list = await mobile.listInstallables();
    expect(Array.isArray(list)).toBe(true);
    expect(list.map((x) => x.name)).toContain("ollama");
  });

  it("infraSummary returns machine specs + package managers", async () => {
    const s = await mobile.infraSummary();
    expect(s.machine.name).toBe("mock");
    expect(s.packageManagers).toContain("apt-get");
    expect(s.binaries?.[0]?.name).toBe("git");
  });

  it("installTool streams line + result frames", async () => {
    const frames: any[] = [];
    for await (const f of mobile.installTool("ollama")) {
      frames.push(f);
      if (f.kind === "result") break;
    }
    expect(frames.some((f) => f.kind === "line")).toBe(true);
    expect(frames.find((f) => f.kind === "result")?.status).toBe("ok");
  });

  it("wizard start → answer → generate", async () => {
    const start = await mobile.wizard.start();
    expect(start.session.id).toBe("mock-session");
    const ans = await mobile.wizard.answer("mock-session", "app_template", "saas-dashboard");
    expect(ans.session.done).toBe(true);
    const gen = await mobile.wizard.generate("mock-session");
    expect(gen.ok).toBe(true);
    expect(gen.directory).toBe("/tmp/mock-project");
  });

  it("raw.get /info is unauth'd", async () => {
    const res = await mobile.raw.get("/info");
    expect(res.status).toBe(200);
    expect(res.body.hostname).toBe("mock");
  });
});
