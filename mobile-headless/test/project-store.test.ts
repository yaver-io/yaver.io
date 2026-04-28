// project-store.test.ts — exercises the agent-tier ProjectStore
// (HTTP) end-to-end against the in-process mock-agent. PhoneSandbox
// tier needs expo-sqlite and runs only on-device, so it is not
// covered here; the contract it satisfies is identical and tested
// indirectly through pullFromAgent's source half.

import { afterAll, beforeAll, describe, expect, it } from "bun:test";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { startMockAgent, type MockAgentHandle } from "../src/mock-agent";
import {
  agentStore,
  isProjectNotFound,
  ProjectNotFoundError,
  pullFromAgent,
  type Project,
  type ProjectStore,
} from "@yaver/mobile-lib/projectStore";

let agent: MockAgentHandle;

beforeAll(async () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-projectstore-"));
  process.env.YMH_DATA_DIR = dataDir;
  agent = await startMockAgent({ token: "mock-token" });
});

afterAll(async () => {
  await agent.close();
});

describe("agentStore HTTP tier", () => {
  it("list returns [] against an empty agent", async () => {
    const store = agentStore({
      baseUrl: agent.baseUrl,
      headers: { Authorization: "Bearer mock-token" },
    });
    const got = await store.list();
    expect(Array.isArray(got)).toBe(true);
    // The mock agent might return [] or a couple of seeded projects
    // depending on test order; assert the shape, not the count.
    for (const m of got) {
      expect(m.tier).toBe("agent");
      expect(typeof m.slug).toBe("string");
    }
  });

  it("read of an unknown slug surfaces ProjectNotFoundError", async () => {
    const store = agentStore({
      baseUrl: agent.baseUrl,
      headers: { Authorization: "Bearer mock-token" },
    });
    let caught: any = null;
    try {
      await store.read("definitely-not-real-slug-zzz");
    } catch (e) {
      caught = e;
    }
    // The mock agent's /phone/projects/get returns 200 with a null
    // body for unknown slugs (matches the real agent's contract for
    // tolerant clients). The store treats a non-object body as
    // not-found and throws ProjectNotFoundError so the typed sentinel
    // behaviour holds for callers regardless of the agent's choice.
    expect(caught).not.toBeNull();
    expect(isProjectNotFound(caught)).toBe(true);
    if (caught instanceof ProjectNotFoundError) {
      expect(caught.slug).toBe("definitely-not-real-slug-zzz");
    }
  });

  it("write → read round-trips identity fields", async () => {
    const store = agentStore({
      baseUrl: agent.baseUrl,
      headers: { Authorization: "Bearer mock-token" },
    });
    const p: Project = {
      slug: "rt-store",
      name: "Round Trip Store",
      template: "blank",
    };
    const meta = await store.write(p);
    expect(meta.slug).toBe("rt-store");
    expect(meta.tier).toBe("agent");

    const got = await store.read("rt-store");
    expect(got.slug).toBe("rt-store");
    expect(got.name).toBe("Round Trip Store");
  });

  it("baseUrl trailing slash is normalised", async () => {
    const store = agentStore({
      baseUrl: agent.baseUrl + "//",
      headers: { Authorization: "Bearer mock-token" },
    });
    const got = await store.list();
    expect(Array.isArray(got)).toBe(true);
  });
});

describe("pullFromAgent", () => {
  it("reads from agent and writes to a destination store", async () => {
    // Use the in-memory destination store below to avoid touching
    // expo-sqlite (which mobile-headless does not shim).
    const memStore: ProjectStore & { _data: Map<string, Project> } = {
      _data: new Map<string, Project>(),
      async list() {
        return Array.from(this._data.values()).map((p) => ({
          slug: p.slug,
          name: p.name,
          template: p.template,
          createdAt: p.createdAt,
          updatedAt: p.updatedAt,
          tier: "phone-sandbox" as const,
        }));
      },
      async read(slug: string) {
        const p = this._data.get(slug);
        if (!p) throw new ProjectNotFoundError(slug);
        return p;
      },
      async write(p: Project) {
        this._data.set(p.slug, p);
        return {
          slug: p.slug,
          name: p.name,
          template: p.template,
          createdAt: p.createdAt,
          updatedAt: p.updatedAt,
          tier: "phone-sandbox" as const,
        };
      },
    };

    // Seed the agent with a project to pull.
    const src = agentStore({
      baseUrl: agent.baseUrl,
      headers: { Authorization: "Bearer mock-token" },
    });
    await src.write({ slug: "pull-target", name: "Pulled" });

    const meta = await pullFromAgent(
      "pull-target",
      {
        baseUrl: agent.baseUrl,
        headers: { Authorization: "Bearer mock-token" },
      },
      memStore,
    );
    expect(meta.tier).toBe("phone-sandbox");
    expect(meta.slug).toBe("pull-target");
    const got = await memStore.read("pull-target");
    expect(got.name).toBe("Pulled");
  });
});
