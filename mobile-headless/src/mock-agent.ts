// In-process mock Yaver agent — just enough surface for hermetic
// tests. Returns canned responses for the endpoints the lib hits
// during the core smoke paths (info, health, install list, infra
// summary, wizard start/answer/generate, streams).

import * as http from "node:http";
import { AddressInfo } from "node:net";

export interface MockAgentHandle {
  baseUrl: string;
  close: () => Promise<void>;
  /** Push a frame to any SSE subscriber connected to this stream. */
  pushFrame: (streamName: string, frame: any) => void;
}

export async function startMockAgent(opts?: { token?: string }): Promise<MockAgentHandle> {
  const token = opts?.token ?? "mock-token";
  const streamSubs = new Map<string, Set<http.ServerResponse>>();

  const pushFrame = (streamName: string, frame: any) => {
    const subs = streamSubs.get(streamName) ?? new Set();
    for (const res of subs) {
      res.write(`data: ${JSON.stringify(frame)}\n\n`);
    }
  };

  const server = http.createServer(async (req, res) => {
    const authOk = req.headers.authorization === `Bearer ${token}`;
    const url = new URL(req.url!, "http://localhost");
    const path = url.pathname;

    const json = (status: number, body: any) => {
      res.writeHead(status, { "Content-Type": "application/json" });
      res.end(JSON.stringify(body));
    };

    if (path === "/health") return json(200, { ok: true });
    if (!authOk && path !== "/info") return json(401, { error: "unauthorized" });

    if (path === "/info") return json(200, { hostname: "mock", deviceId: "mock-device", mode: "owner" });
    if (path === "/agent/runners") return json(200, { runners: [{ id: "claude-code", name: "Claude Code", installed: true, active: true, models: [] }] });
    if (path === "/infra/summary") return json(200, {
      machine: { name: "mock", platform: "linux", arch: "amd64", deviceId: "mock-device", isOnline: true },
      metrics: { cpuPct: 5, ramPct: 40, ramUsed: 4e9, ramTotal: 16e9, diskPct: 30, diskUsed: 120e9, diskTotal: 500e9, cores: 8, uptime: 1000, hostname: "mock" },
      capabilities: { terminal: true, mcp: true, devServices: true, systemServices: false, agentShutdown: true, hostReboot: false },
      sharing: { isShared: false, pendingGuests: 0, acceptedGuests: 0 },
      sandbox: { enabledMode: "off", imageReady: false, docker: false, imageName: "yaver-sandbox" },
      packageManagers: ["apt-get", "snap", "npm"],
      binaries: [{ name: "git", path: "/usr/bin/git", manager: "system" }],
    });
    if (path === "/install/list") return json(200, [
      { name: "ollama", installed: false, description: "Local LLM runtime", kind: "model-runtime", source: "registry" },
      { name: "aider", installed: false, description: "Terminal pair-programmer", kind: "ai-runner", source: "registry" },
      { name: "git", installed: true, description: "Version control", path: "/usr/bin/git", source: "builtin" },
    ]);
    if (path.startsWith("/install/") && req.method === "POST") {
      const tool = path.slice("/install/".length);
      if (tool === "sudo") { return json(202, { ok: true }); }
      const streamName = `install:${tool}`;
      // schedule a fake install run
      setTimeout(() => {
        pushFrame(streamName, { type: "line", text: "Starting install: " + tool });
        pushFrame(streamName, { type: "line", text: "→ fake step 1" });
        pushFrame(streamName, { type: "result", tool, status: "ok" });
      }, 50);
      return json(202, { ok: true, tool, stream: streamName, source: "mock" });
    }
    if (path.startsWith("/streams/")) {
      const streamName = decodeURIComponent(path.slice("/streams/".length));
      res.writeHead(200, {
        "Content-Type": "text/event-stream",
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      });
      res.write(": hello\n\n");
      const subs = streamSubs.get(streamName) ?? new Set();
      subs.add(res);
      streamSubs.set(streamName, subs);
      req.on("close", () => subs.delete(res));
      return;
    }
    if (path === "/project/wizard/start" && req.method === "POST") {
      return json(200, {
        session: { id: "mock-session", answers: {}, done: false, createdAt: Date.now(), updatedAt: Date.now() },
        question: { id: "app_template", kind: "choice", prompt: "What kind of app is this?", choices: ["saas-dashboard"], default: "saas-dashboard" },
      });
    }
    if (path === "/project/wizard/answer" && req.method === "POST") {
      // Accept anything, advance to "done" immediately so tests are fast.
      return json(200, {
        session: { id: "mock-session", answers: {}, done: true, createdAt: 0, updatedAt: 0 },
        question: { id: "", kind: "done", prompt: "All answered" },
      });
    }
    if (path === "/project/wizard/generate" && req.method === "POST") {
      return json(200, { ok: true, directory: "/tmp/mock-project", files: ["README.md"], nextSteps: ["cd mock-project"] });
    }
    json(404, { error: "mock agent: path not covered — " + path });
  });

  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address() as AddressInfo;
  const baseUrl = `http://127.0.0.1:${port}`;
  return {
    baseUrl,
    close: () => new Promise<void>((r) => {
      // Close any long-lived SSE responses first — otherwise
      // server.close() waits for them to finish naturally, which
      // hangs past the test's afterAll timeout.
      for (const [, subs] of streamSubs) {
        for (const res of subs) { try { res.end(); } catch { /* already ended */ } }
        subs.clear();
      }
      server.closeAllConnections?.();
      server.close(() => r());
    }),
    pushFrame,
  };
}
