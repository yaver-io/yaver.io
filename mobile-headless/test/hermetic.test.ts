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
let targetAgent: MockAgentHandle;
let mobile: MobileClient;

beforeAll(async () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-hermetic-"));
  process.env.YMH_DATA_DIR = dataDir;
  agent = await startMockAgent({ token: "mock-token" });
  targetAgent = await startMockAgent({ token: "target-token" });
  mobile = new MobileClient({
    dataDir,
    authToken: "mock-token",
    convexUrl: agent.baseUrl,
  });
  mobile.useAgentBaseUrl(agent.baseUrl);
});

afterAll(async () => {
  await agent.close();
  await targetAgent.close();
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

  it("creates and reads a local phone project", async () => {
    const created = await mobile.phoneProjects.create({
      name: "Todo Backend",
      template: "todos",
      prompt: "Make a mobile-only todo app with a backend export path.",
    });
    expect(created.slug).toBe("todo-backend");
    expect(created.template).toBe("todos");

    const fetched = await mobile.phoneProjects.get(created.slug);
    expect(fetched?.slug).toBe(created.slug);

    const listed = await mobile.phoneProjects.list();
    expect(listed.some((project) => project.slug === created.slug)).toBe(true);
  });

  it("creates a phone project on another agent", async () => {
    const created = await mobile.phoneProjects.createAt(
      { baseUrl: targetAgent.baseUrl, authToken: "target-token" },
      { name: "Hetzner Todo Box", template: "todos" },
    );
    expect(created.slug).toBe("hetzner-todo-box");
    expect(created.dir).toContain("/tmp/mock-phone-projects/");
  });

  it("exports and pushes a phone project bundle to another agent", async () => {
    const created = await mobile.phoneProjects.create({
      name: "Deploy Me",
      template: "todos",
    });
    const exported = await mobile.phoneProjects.export(created.slug, { includeData: true, containerize: true });
    expect(exported.size).toBeGreaterThan(0);
    expect(exported.contentType).toBe("application/gzip");

    const pushed = await mobile.phoneProjects.push(created.slug, {
      baseUrl: targetAgent.baseUrl,
      authToken: "target-token",
    }, {
      includeData: true,
      containerize: true,
    });
    expect(pushed.slug).toBeTruthy();
    expect(pushed.project.template).toBe("imported");

    const remoteList = await fetch(`${targetAgent.baseUrl}/phone/projects/list`, {
      headers: { Authorization: "Bearer target-token" },
    }).then((res) => res.json() as Promise<{ projects: Array<{ slug: string }> }>);
    expect(remoteList.projects.length).toBeGreaterThan(0);
  });

  it("bootstraps a todo backend deploy in one step", async () => {
    const result = await mobile.phoneProjects.bootstrapTodoDeploy({
      name: "One Tap Todo",
      prompt: "Deploy this todo backend to my Hetzner cloud box.",
      target: {
        baseUrl: targetAgent.baseUrl,
        authToken: "target-token",
      },
      includeData: true,
      containerize: true,
    });
    expect(result.localProject.slug).toBe("one-tap-todo");
    expect(result.remote.project.template).toBe("imported");
    expect(result.remote.browseUrl).toContain("/phone-projects/");
  });
});
