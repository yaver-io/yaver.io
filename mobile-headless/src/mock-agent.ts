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
  /**
   * Switch /dev/build-native behaviour at runtime.
   *
   *   "ok"     — instant 200 with a synthetic bundle descriptor
   *   "hang"   — never responds; lets tests verify client-side abort
   *   "fail"   — instant 500 with an error body (mirrors a real bundler crash)
   *   "slow"   — responds after `slowMs`; lets tests verify the client honours
   *              its own per-request deadline without waiting forever for the
   *              agent to die first
   */
  setBuildNativeMode: (mode: "ok" | "hang" | "fail" | "slow" | "blocked", slowMs?: number) => void;
  /**
   * Switch /dev/stop behaviour at runtime so tests can exercise the
   * Stop-UX state machine in mobile/web without spinning a real agent.
   *
   *   "verified"      — agent 1.99.93+ shape: ok=true, verified=true,
   *                     buildsCancelled=N (default 0). The mobile UI
   *                     should show the green "Dev server stopped" pill.
   *   "not-verified"  — ok=true but verified=false (subprocess didn't
   *                     confirm exit within 7s). Mobile UI should show
   *                     the red ⚠ "Stop incomplete" pill.
   *   "fail"          — non-2xx with an error body. Mobile UI should
   *                     fall back to the error pill with the message.
   *   "legacy"        — pre-1.99.93 shape (no verified / buildsCancelled
   *                     fields). Mobile UI should still treat the call
   *                     as success and show the green pill.
   */
  setStopMode: (
    mode: "verified" | "not-verified" | "fail" | "legacy",
    opts?: { buildsCancelled?: number; message?: string },
  ) => void;
  getLastBuildNativeRequest: () => any;
}

export async function startMockAgent(opts?: { token?: string }): Promise<MockAgentHandle> {
  const token = opts?.token ?? "mock-token";
  let buildNativeMode: "ok" | "hang" | "fail" | "slow" | "blocked" = "ok";
  let buildNativeSlowMs = 0;
  let stopMode: "verified" | "not-verified" | "fail" | "legacy" = "verified";
  let stopBuildsCancelled = 0;
  let stopMessage = "";
  let lastBuildNativeRequest: any = null;
  // Track in-flight "hang" responses so we can release them on close —
  // otherwise server.close() blocks forever waiting for them and the
  // test's afterAll hangs.
  const pendingBuildNative = new Set<http.ServerResponse>();
  const streamSubs = new Map<string, Set<http.ServerResponse>>();
  const phoneProjects = new Map<string, any>();
  const repos = new Map<string, any>();
  const execs = new Map<string, any>();
  let execCounter = 0;

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
    if (path === "/repos/list" && req.method === "GET") {
      return json(200, Array.from(repos.values()));
    }
    if (path === "/repos/clone" && req.method === "POST") {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
      const repoName = String(body.url || "repo").split("/").pop()?.replace(/\.git$/, "") || "repo";
      const path = `${body.dir || "/tmp/mock-repos"}/${repoName}`;
      const repo = {
        name: repoName,
        path,
        branch: body.branch || "main",
        remote: body.url,
        dirty: false,
      };
      repos.set(path, repo);
      return json(200, { ok: true, path, output: `cloned ${body.url}` });
    }
    if (path === "/exec" && req.method === "GET") {
      return json(200, { execs: Array.from(execs.values()) });
    }
    if (path === "/exec" && req.method === "POST") {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
      const execId = `exec-${++execCounter}`;
      const command = String(body.command || "");
      const now = new Date().toISOString();
      const exec = {
        id: execId,
        command,
        status: "completed",
        exitCode: 0,
        stdout: command.includes("yaver sdk add feedback")
          ? "installed yaver feedback sdk"
          : command.includes("yaver ci add")
            ? `wrote workflow for ${command.split(" ").pop()}`
            : `ran ${command}`,
        stderr: "",
        pid: 1000 + execCounter,
        startedAt: now,
        finishedAt: now,
      };
      execs.set(execId, exec);
      return json(200, { ok: true, execId, pid: exec.pid });
    }
    if (path.startsWith("/exec/") && req.method === "GET") {
      const execId = path.slice("/exec/".length);
      const exec = execs.get(execId);
      if (!exec) return json(404, { error: "exec not found" });
      return json(200, { exec });
    }
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
    if (path === "/vibing" && req.method === "GET") {
      const query = url.searchParams.get("query") || "mock-project";
      return json(200, {
        project: query,
        path: `/tmp/mock-phone-projects/${query}`,
        framework: "expo",
        suggestions: [
          {
            id: "mock-feature",
            icon: "✨",
            label: "Add Filters",
            desc: "Let users filter todos by status.",
            category: "feature",
            prompt: "Add an all/open/done filter to the todo list.",
            priority: 1,
          },
        ],
        quickActions: [
          {
            id: "mock-tests",
            icon: "🧪",
            label: "Run Tests",
            desc: "Run the todo tests.",
            category: "test",
            prompt: "Run the todo tests and summarize results.",
            priority: 1,
          },
        ],
        history: ["Create todo app"],
        generatedAt: new Date().toISOString(),
      });
    }
    if (path === "/vibing/execute" && req.method === "POST") {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
      return json(200, {
        ok: true,
        taskId: "mock-vibing-task",
        message: `queued: ${String(body.prompt || "").slice(0, 80)}`,
      });
    }
    if (path === "/dev/stop" && req.method === "POST") {
      // Drain body for connection hygiene, even though /dev/stop ignores it.
      for await (const chunk of req) { void chunk; }
      switch (stopMode) {
        case "fail":
          return json(500, {
            ok: false,
            stoppedServing: false,
            error: stopMessage || "mock: stop failed",
          });
        case "not-verified":
          return json(200, {
            ok: false,
            stoppedServing: true,
            previouslyServing: true,
            verified: false,
            buildsCancelled: stopBuildsCancelled,
            framework: "expo",
            workDir: "/mock/sfmg",
            message:
              stopMessage ||
              "Dev server failed to stop within 7s — subprocess may still be running.",
          });
        case "legacy":
          return json(200, {
            ok: true,
            stoppedServing: true,
            previouslyServing: true,
            framework: "expo",
            workDir: "/mock/sfmg",
            message: "Stopped serving the active preview.",
          });
        case "verified":
        default:
          return json(200, {
            ok: true,
            stoppedServing: true,
            previouslyServing: true,
            verified: true,
            buildsCancelled: stopBuildsCancelled,
            framework: "expo",
            workDir: "/mock/sfmg",
            message: "Stopped serving the active preview.",
          });
      }
    }
    if (path === "/dev/build-native" && req.method === "POST") {
      // Drain the request body so the connection is half-closed correctly.
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      lastBuildNativeRequest = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
      switch (buildNativeMode) {
        case "hang":
          // Hold the response open. The client must abort to make progress.
          pendingBuildNative.add(res);
          req.on("close", () => pendingBuildNative.delete(res));
          return;
        case "slow":
          await new Promise((r) => setTimeout(r, buildNativeSlowMs));
          return json(200, {
            status: "ok",
            bundleUrl: "/dev/native-bundle",
            assetsUrl: "/dev/native-assets",
            size: 4096,
            md5: "deadbeef",
            bcVersion: 96,
            platform: lastBuildNativeRequest?.platform ?? "ios",
            moduleName: "main",
            hasAssets: false,
          });
        case "fail":
          return json(500, {
            error: "bundle failed: mocked failure",
            phase: "bundle",
            timedOut: false,
            helpHint: "mock-agent: buildNativeMode=fail",
          });
        case "blocked":
          return json(409, {
            status: "blocked",
            code: "NATIVE_MODULE_INCOMPATIBLE",
            error: "Blocked native Hermes load: mock project declares modules Yaver does not register: react-native-fictional",
            helpHint: "mock-agent: buildNativeMode=blocked",
            incompatibleNativeModules: ["react-native-fictional"],
            matchedNativeModules: ["@react-native-async-storage/async-storage"],
            hostSdkVersion: "1.0.0",
            hostReactNative: "0.81.5",
            supportedRNRange: "0.81.x",
            bcVersion: 96,
            md5: "deadbeef",
            size: 4096,
            moduleName: "main",
            platform: lastBuildNativeRequest?.platform ?? "ios",
          });
        case "ok":
        default:
          return json(200, {
            status: "ok",
            bundleUrl: "/dev/native-bundle",
            assetsUrl: "/dev/native-assets",
            size: 4096,
            md5: "deadbeef",
            bcVersion: 96,
            platform: lastBuildNativeRequest?.platform ?? "ios",
            moduleName: "main",
            hasAssets: false,
          });
      }
    }
    if (path === "/phone/projects/list" && req.method === "GET") {
      return json(200, { projects: Array.from(phoneProjects.values()) });
    }
    if (path === "/phone/projects/get" && req.method === "GET") {
      const slug = url.searchParams.get("slug") || "";
      return json(200, phoneProjects.get(slug) ?? null);
    }
    if (path === "/phone/projects/create" && req.method === "POST") {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
      const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : {};
      const now = new Date().toISOString();
      const slug = String(body.slug || body.name || "project").trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
      const project = {
        slug,
        name: String(body.name || slug),
        template: body.template || "crud",
        dir: `/tmp/mock-phone-projects/${slug}`,
        createdAt: now,
        updatedAt: now,
        schema: null,
        auth: null,
        seed: null,
        app: body.prompt ? { summary: `Kickoff prompt: ${body.prompt}` } : null,
        stats: { tableCount: 0, rowCount: 0, perTable: {}, dbBytes: 0 },
      };
      phoneProjects.set(slug, project);
      return json(200, project);
    }
    if (path === "/phone/projects/export" && req.method === "GET") {
      const slug = url.searchParams.get("slug") || "";
      const project = phoneProjects.get(slug);
      if (!project) return json(404, { error: "project not found" });
      const payload = Buffer.from(JSON.stringify({ slug, project }), "utf8");
      res.writeHead(200, { "Content-Type": "application/gzip", "Content-Length": String(payload.byteLength) });
      res.end(payload);
      return;
    }
    if (path === "/phone/projects/receive" && req.method === "POST") {
      const slug = url.searchParams.get("slug") || `received-${phoneProjects.size + 1}`;
      const now = new Date().toISOString();
      const project = {
        slug,
        name: slug,
        template: "imported",
        dir: `/tmp/mock-phone-projects/${slug}`,
        createdAt: now,
        updatedAt: now,
        schema: null,
        auth: null,
        seed: null,
        app: { summary: "Imported from another agent" },
        stats: { tableCount: 0, rowCount: 0, perTable: {}, dbBytes: 0 },
      };
      phoneProjects.set(slug, project);
      return json(200, {
        slug,
        localUrl: `/phone-project/${slug}`,
        browseUrl: `/phone-projects/${slug}`,
        project,
      });
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
      // Same story for /dev/build-native hang-mode responses.
      for (const res of pendingBuildNative) { try { res.end(); } catch { /* already ended */ } }
      pendingBuildNative.clear();
      server.closeAllConnections?.();
      server.close(() => r());
    }),
    pushFrame,
    setBuildNativeMode: (mode, slowMs) => {
      buildNativeMode = mode;
      buildNativeSlowMs = slowMs ?? 0;
    },
    setStopMode: (mode, opts) => {
      stopMode = mode;
      stopBuildsCancelled = opts?.buildsCancelled ?? 0;
      stopMessage = opts?.message ?? "";
    },
    getLastBuildNativeRequest: () => lastBuildNativeRequest,
  };
}
