import { describe, expect, it } from "bun:test";
import {
  listHetznerServerActions,
  powerOnHetznerServer,
  renameHetznerServer,
  shutdownHetznerServer,
} from "@yaver/mobile-lib/hetznerDirectCore";

const response = (body: unknown) => new Response(JSON.stringify(body), {
  status: 200,
  headers: { "content-type": "application/json" },
});

describe("TestFlight Hetzner power lifecycle uses the real mobile core", () => {
  for (const scenario of [
    { action: "poweron", from: "off", transition: "starting", target: "running" },
    { action: "shutdown", from: "running", transition: "stopping", target: "off" },
  ] as const) {
    it(`${scenario.from} -> ${scenario.transition} -> ${scenario.target} preserves server and IP`, async () => {
      const calls: Array<{ method: string; path: string; authorization: string }> = [];
      let serverReads = 0;
      let actionReads = 0;
      const fakeFetch = async (url: string, init?: RequestInit) => {
        const path = url.replace("https://api.hetzner.cloud/v1", "");
        const headers = init?.headers as Record<string, string>;
        calls.push({ method: String(init?.method || "GET"), path, authorization: headers.Authorization });
        if (path === "/servers/90210") {
          serverReads += 1;
          const status = serverReads === 1 ? scenario.from : serverReads === 2 ? scenario.transition : scenario.target;
          return response({ server: {
            id: 90210,
            name: "private-vps",
            status,
            public_net: { ipv4: { ip: "203.0.113.10" } },
          } });
        }
        if (path === `/servers/90210/actions/${scenario.action}`) {
          return response({ action: { id: 77, status: "running" } });
        }
        if (path === "/actions/77") {
          actionReads += 1;
          return response({ action: { id: 77, status: actionReads === 1 ? "running" : "success" } });
        }
        throw new Error(`unexpected request ${path}`);
      };
      const waits: number[] = [];
      const run = scenario.action === "poweron" ? powerOnHetznerServer : shutdownHetznerServer;
      const final = await run("HEADLESS_CANARY_TOKEN", 90210, fakeFetch, async (ms) => { waits.push(ms); });

      expect(final).toMatchObject({ id: 90210, ip: "203.0.113.10", status: scenario.target });
      expect(calls.map((call) => `${call.method} ${call.path}`)).toEqual([
        "GET /servers/90210",
        `POST /servers/90210/actions/${scenario.action}`,
        "GET /actions/77",
        "GET /actions/77",
        "GET /servers/90210",
        "GET /servers/90210",
      ]);
      expect(calls.every((call) => call.method !== "DELETE")).toBe(true);
      expect(calls.every((call) => call.authorization === "Bearer HEADLESS_CANARY_TOKEN")).toBe(true);
      expect(calls.every((call) => !call.path.includes("HEADLESS_CANARY_TOKEN"))).toBe(true);
      expect(waits).toEqual([2_000, 2_000, 2_000]);
    });
  }

  it("renames the existing server without replacing it or exposing a destructive method", async () => {
    const calls: string[] = [];
    const renamed = await renameHetznerServer("HEADLESS_CANARY_TOKEN", 90210, "mn71pn9c.cloud.yaver.io", async (url, init) => {
      const path = url.replace("https://api.hetzner.cloud/v1", "");
      calls.push(`${init?.method || "GET"} ${path}`);
      const headers = init?.headers as Record<string, string>;
      expect(headers.Authorization).toBe("Bearer HEADLESS_CANARY_TOKEN");
      expect(path).not.toContain("HEADLESS_CANARY_TOKEN");
      return response({ server: {
        id: 90210,
        name: init?.method === "PUT" ? "mn71pn9c.cloud.yaver.io" : "previous-name",
        status: "running",
        public_net: { ipv4: { ip: "203.0.113.10" } },
      } });
    });
    expect(renamed).toMatchObject({ id: 90210, name: "mn71pn9c.cloud.yaver.io", ip: "203.0.113.10" });
    expect(calls).toEqual(["GET /servers/90210", "PUT /servers/90210"]);
    expect(calls.some((call) => call.startsWith("DELETE "))).toBe(false);
  });

  it("reads native provider action history without sending it through Yaver", async () => {
    const calls: Array<{ path: string; method: string; authorization: string }> = [];
    const actions = await listHetznerServerActions("HEADLESS_CANARY_TOKEN", 90210, async (url, init) => {
      calls.push({
        path: url.replace("https://api.hetzner.cloud/v1", ""),
        method: String(init?.method || "GET"),
        authorization: (init?.headers as Record<string, string>).Authorization,
      });
      return response({ actions: [{ id: 88, command: "start_server", status: "success", progress: 100 }] });
    });
    expect(actions).toEqual([{
      id: 88, command: "start_server", status: "success", progress: 100,
      started: null, finished: null, errorCode: null,
    }]);
    expect(calls).toEqual([{
      path: "/servers/90210/actions?sort=started:desc&per_page=20",
      method: "GET",
      authorization: "Bearer HEADLESS_CANARY_TOKEN",
    }]);
  });
});
