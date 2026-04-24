// Hermetic tests — no external services. Everything runs against a
// local mock HTTP server that plays back canned Convex + agent
// responses. Goal: lock in the contract between WebClient and the
// web dashboard's agent-client.ts so future drift fails CI, not
// production.

import { describe, expect, test, beforeAll, afterAll } from "bun:test";
import { WebClient } from "../src/web-client";
import { createServer, type IncomingMessage, type ServerResponse, type Server } from "node:http";

type Handler = (req: IncomingMessage, res: ServerResponse, body: string) => void | Promise<void>;

function startMock(handler: Handler): Promise<{ url: string; server: Server }> {
  return new Promise((resolve) => {
    const server = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        const body = Buffer.concat(chunks).toString("utf8");
        Promise.resolve(handler(req, res, body)).catch((e) => {
          res.statusCode = 500;
          res.end(String(e));
        });
      });
    });
    server.listen(0, "127.0.0.1", () => {
      const addr = server.address();
      if (!addr || typeof addr === "string") throw new Error("bad addr");
      resolve({ url: `http://127.0.0.1:${addr.port}`, server });
    });
  });
}

function stopMock(server: Server): Promise<void> {
  return new Promise((resolve) => server.close(() => resolve()));
}

describe("WebClient — Convex surface", () => {
  let convex: { url: string; server: Server };
  let seenAuthHeader: string | null = null;

  beforeAll(async () => {
    convex = await startMock((req, res, body) => {
      const hdr = req.headers["authorization"];
      seenAuthHeader = typeof hdr === "string" ? hdr : null;
      res.setHeader("Content-Type", "application/json");

      if (req.url === "/auth/signup") {
        const p = JSON.parse(body);
        res.end(JSON.stringify({ ok: true, token: `token-for-${p.email}` }));
        return;
      }
      if (req.url === "/auth/login") {
        const p = JSON.parse(body);
        res.end(JSON.stringify({ ok: true, token: `signin-token-${p.email}` }));
        return;
      }
      if (req.url === "/auth/me") {
        res.end(JSON.stringify({ id: "u1", email: "dev@example.com" }));
        return;
      }
      if (req.url === "/config") {
        res.end(
          JSON.stringify({
            relayServers: [
              { id: "r1", httpUrl: "http://127.0.0.1:9", priority: 1, password: "stub" },
            ],
          }),
        );
        return;
      }
      if (req.url === "/settings" && req.method === "GET") {
        res.end(JSON.stringify({ settings: { relayPassword: "user-pw" } }));
        return;
      }
      if (req.url === "/settings/repair-relay" && req.method === "POST") {
        res.end(JSON.stringify({ ok: true, repaired: true, reason: "synced to platform default" }));
        return;
      }
      if (req.url === "/devices/list") {
        res.end(
          JSON.stringify({
            devices: [
              { id: "d1", name: "mac-mini", online: true },
              { id: "d2", name: "yaver-test-ephemeral", online: true, lanIps: ["10.0.0.5"] },
            ],
          }),
        );
        return;
      }
      res.statusCode = 404;
      res.end(JSON.stringify({ error: `no handler for ${req.url}` }));
    });
  });

  afterAll(async () => {
    await stopMock(convex.server);
  });

  test("signUp populates token", async () => {
    const c = new WebClient({ convexUrl: convex.url });
    const t = await c.signUp({ email: "a@b", password: "x" });
    expect(t).toBe("token-for-a@b");
    expect(c.isAuthed).toBe(true);
  });

  test("signIn populates token", async () => {
    const c = new WebClient({ convexUrl: convex.url });
    const t = await c.signIn({ email: "a@b", password: "x" });
    expect(t).toBe("signin-token-a@b");
  });

  test("whoami returns null when not authed", async () => {
    const c = new WebClient({ convexUrl: convex.url });
    expect(await c.whoami()).toBeNull();
  });

  test("whoami passes Authorization when authed", async () => {
    const c = new WebClient({ convexUrl: convex.url, token: "abc" });
    seenAuthHeader = null;
    await c.whoami();
    // Explicit cast — TS narrows seenAuthHeader to `null` after the
    // reassignment above, since it can't prove the mock handler ran.
    expect(seenAuthHeader as unknown as string).toBe("Bearer abc");
  });

  test("listDevices returns the agent's list", async () => {
    const c = new WebClient({ convexUrl: convex.url, token: "abc" });
    const devs = await c.listDevices();
    expect(devs).toHaveLength(2);
    expect(devs[0].id).toBe("d1");
    expect(devs[1].lanIps).toEqual(["10.0.0.5"]);
  });

  test("refreshRelayConfig picks up user password override", async () => {
    const c = new WebClient({ convexUrl: convex.url, token: "abc" });
    const cfg = await c.refreshRelayConfig();
    expect(cfg.relayServers).toHaveLength(1);
    // User-level password overrides the platform default.
    expect(cfg.relayServers[0].password).toBe("user-pw");
    expect(cfg.userRelayPassword).toBe("user-pw");
  });

  test("repairRelay reports synced + refreshes config", async () => {
    const c = new WebClient({ convexUrl: convex.url, token: "abc" });
    const r = await c.repairRelay();
    expect(r.repaired).toBe(true);
    expect(r.reason).toBe("synced to platform default");
  });
});

describe("WebClient — agent surface (dev server, tasks)", () => {
  let agent: { url: string; server: Server };
  let devStatus = { running: false, framework: "" as string, workDir: "" };
  const tasks: Record<string, { id: string; title: string; status: string }> = {};

  beforeAll(async () => {
    agent = await startMock((req, res, body) => {
      res.setHeader("Content-Type", "application/json");

      if (req.url === "/health") {
        res.end(JSON.stringify({ ok: true }));
        return;
      }
      if (req.url === "/info") {
        res.end(JSON.stringify({ ok: true, version: "1.99.30" }));
        return;
      }
      if (req.url === "/dev/status") {
        res.end(JSON.stringify(devStatus));
        return;
      }
      if (req.url === "/dev/start" && req.method === "POST") {
        const p = JSON.parse(body);
        devStatus = {
          running: true,
          framework: p.framework || "vite",
          workDir: p.workDir || "/tmp",
        };
        res.end(JSON.stringify({ ok: true }));
        return;
      }
      if (req.url === "/dev/stop" && req.method === "POST") {
        devStatus = { running: false, framework: "", workDir: "" };
        res.end(JSON.stringify({ ok: true }));
        return;
      }
      if (req.url === "/dev/reload" && req.method === "POST") {
        res.end(JSON.stringify({ ok: true, framework: devStatus.framework }));
        return;
      }
      if (req.url === "/tasks" && req.method === "POST") {
        const p = JSON.parse(body);
        const id = `t-${Object.keys(tasks).length + 1}`;
        tasks[id] = { id, title: p.title, status: "running" };
        res.end(JSON.stringify({ taskId: id }));
        return;
      }
      if (req.url?.startsWith("/tasks/") && req.method === "GET") {
        const id = req.url.slice("/tasks/".length);
        const t = tasks[id];
        if (!t) {
          res.statusCode = 404;
          res.end(JSON.stringify({ error: "not found" }));
          return;
        }
        res.end(JSON.stringify(t));
        return;
      }
      res.statusCode = 404;
      res.end(JSON.stringify({ error: `no handler for ${req.url}` }));
    });
  });

  afterAll(async () => {
    await stopMock(agent.server);
  });

  test("direct connect + dev-server lifecycle", async () => {
    const c = new WebClient({ token: "abc", agentBaseUrl: agent.url });
    // Direct-only client; connect succeeds via the direct probe.
    const r = await c.connect("dev-device");
    expect(r.ok).toBe(true);
    expect(r.via).toBe("direct");

    // Initial state: no dev server.
    expect((await c.getDevServerStatus())!.running).toBe(false);

    await c.startDevServer({ framework: "vite", workDir: "/workspace/app" });
    const status = await c.getDevServerStatus();
    expect(status!.running).toBe(true);
    expect(status!.framework).toBe("vite");

    const reload = await c.reloadDevServer();
    expect(reload.ok).toBe(true);

    await c.stopDevServer();
    expect((await c.getDevServerStatus())!.running).toBe(false);
  });

  test("createTask + getTask roundtrip", async () => {
    const c = new WebClient({ token: "abc", agentBaseUrl: agent.url });
    await c.connect("dev-device");
    const t = await c.createTask({ title: "add login", description: "add a login page" });
    expect(t.id).toBeTruthy();
    expect(t.title).toBe("add login");
    const fetched = await c.getTask(t.id);
    expect(fetched.id).toBe(t.id);
  });

  test("devPreviewUrl returns null when relay-password not yet loaded", () => {
    const c = new WebClient({ token: "abc" });
    // Manually put us in "have relay URL, no password yet" state.
    (c as any).deviceId = "d1";
    (c as any).activeRelayUrl = "https://relay.example.com";
    (c as any).activeRelayPassword = null;
    expect(c.devPreviewUrl).toBeNull();

    (c as any).activeRelayPassword = "pw";
    expect(c.devPreviewUrl).toBe("https://relay.example.com/d/d1/dev/?__rp=pw");
  });
});

describe("WebClient — reconnectAndFix orchestration", () => {
  test("succeeds when agent is healthy + dev server already stopped", async () => {
    const agent = await startMock((req, res) => {
      res.setHeader("Content-Type", "application/json");
      if (req.url === "/health") {
        res.end(JSON.stringify({ ok: true }));
        return;
      }
      if (req.url === "/info") {
        res.end(JSON.stringify({ ok: true, version: "1.99.30" }));
        return;
      }
      if (req.url === "/dev/status") {
        res.end(JSON.stringify({ running: false }));
        return;
      }
      res.statusCode = 404;
      res.end(JSON.stringify({ error: "no-op" }));
    });
    const c = new WebClient({ token: "abc", agentBaseUrl: agent.url });
    await c.connect("d1");

    const report = await c.reconnectAndFix({ deviceId: "d1" });
    expect(report.ok).toBe(true);
    expect(report.steps.some((s) => s.step === "agent health" && s.ok)).toBe(true);
    await stopMock(agent.server);
  });
});
