import { describe, it, expect } from "bun:test";
import { MobileClient } from "../src/mobile-client";

const AGENT_URL = process.env.YMH_SMOKE_AGENT_URL || "";
const AGENT_TOKEN = process.env.YMH_SMOKE_AGENT_TOKEN || "";

const maybe = AGENT_URL ? describe : describe.skip;

maybe("phone-only vibing smoke against live yaver agent", () => {
  const mobile = new MobileClient({
    agentBaseUrl: AGENT_URL,
    authToken: AGENT_TOKEN,
  });

  it("creates a todo phone project, fetches vibing state, and exports it", async () => {
    const name = `Smoke Todo ${Date.now()}`;
    const created = await mobile.phoneProjects.create({
      name,
      template: "todos",
      prompt: "Build a mobile-only todo app with local backend state.",
    });
    expect(created.slug).toBeTruthy();
    expect(created.template).toBe("todos");

    const fetched = await mobile.phoneProjects.get(created.slug);
    expect(fetched?.schema).toBeDefined();
    expect(fetched?.stats).toBeDefined();

    const browse = await mobile.raw.get(
      `/phone/projects/browse?slug=${encodeURIComponent(created.slug)}&table=todos&limit=20`,
    );
    expect(browse.status).toBe(200);
    expect(Array.isArray(browse.body?.rows)).toBe(true);
    expect(browse.body.rows.length).toBeGreaterThan(0);

    const state = await mobile.vibing.state(created.dir);
    expect(state.path).toBeTruthy();
    expect(state.quickActions.length).toBeGreaterThan(0);

    const exec = await mobile.vibing.execute({
      prompt: state.quickActions[0].prompt,
      projectPath: created.dir,
      projectName: created.name,
    });
    expect(exec.ok).toBe(true);
    expect(typeof exec.taskId === "string" || exec.runtimeDeploy !== undefined).toBe(true);

    const exported = await mobile.phoneProjects.export(created.slug, { includeData: true });
    expect(exported.size).toBeGreaterThan(0);
    expect(exported.contentType).toContain("application/");
  }, 120000);
});
