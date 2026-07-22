// Real-agent integration test — drives an actual Go agent binary
// through the WebClient. Proves the HTTP contract the hermetic tests
// lock in matches what the Go agent actually serves.
//
// Gated on YAVER_AGENT_BIN. Set it to an absolute path to a pre-built
// `yaver` binary (`cd desktop/agent && go build -o /tmp/yaver .`), or
// skip locally. CI builds the binary itself — see
// .github/workflows/web-headless.yml.

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import net from "node:net";
import { WebClient } from "../src/web-client";

const AGENT_BIN = process.env.YAVER_AGENT_BIN || "";
const HAS_AGENT = AGENT_BIN !== "";

const TEST_TOKEN = "hermetic-integration-token";
const TEST_DEVICE_ID = "dev-integration-1";

const WORKSPACE_YAML = `version: 1
name: integration-repo
workspace:
  root: "."
apps:
  - name: web
    path: ./web
    stack: nextjs
  - name: marketing
    path: ./marketing
    stack: vite
  - name: mobile
    path: ./mobile
    stack: react-native-expo
  - name: mobile-native
    path: ./mobile-native
    stack: react-native
  - name: backend
    path: ./backend
    stack: go
`;

async function pickFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      if (typeof addr === "object" && addr) {
        const port = addr.port;
        srv.close(() => resolve(port));
      } else {
        reject(new Error("bad address"));
      }
    });
  });
}

async function waitForHealth(baseUrl: string, timeoutMs = 30_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr: unknown = null;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${baseUrl}/health`);
      if (res.ok) return;
    } catch (e) {
      lastErr = e;
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(
    `agent never healthy at ${baseUrl}: ${lastErr ? String((lastErr as Error).message) : "timeout"}`,
  );
}

interface Harness {
  proc: ChildProcess;
  baseUrl: string;
  homeDir: string;
  workDir: string;
}

async function startAgent(): Promise<Harness> {
  // Isolated HOME so the agent reads/writes only our config, never the
  // developer's real ~/.yaver/.
  const homeDir = mkdtempSync(join(tmpdir(), "yaver-web-headless-home-"));
  const yaverDir = join(homeDir, ".yaver");
  mkdirSync(yaverDir, { recursive: true });
  writeFileSync(
    join(yaverDir, "config.json"),
    JSON.stringify(
      {
        auth_token: TEST_TOKEN,
        device_id: TEST_DEVICE_ID,
        // The agent's mustLoadAuthConfig() hard-exits when ConvexSiteURL
        // is empty — even when we don't intend to touch Convex in-test.
        // Stub it out with a routable-but-unreachable URL so the initial
        // config-load gate passes; YAVER_NO_BOOTSTRAP stops any real
        // Convex calls from blocking startup.
        convex_site_url: "https://integration-test.invalid",
        // CI must exercise the binary it just built. Leaving this implicit lets
        // default-on auto-update replace /tmp/yaver-agent with the public
        // latest release and re-exec in the middle of the test harness.
        auto_update: false,
        headless_keep_awake: false,
        // Flag both subsystems' onboarding as already done — pipe stdin
        // should skip them, but this is defence in depth in case a
        // future release removes the stdin check.
        macos_permission_onboarding_done: true,
      },
      null,
      2,
    ),
    "utf8",
  );

  // Workspace root with the manifest + stub app directories. The
  // agent's buildAppViews checks dir existence, so all five must exist
  // on disk or `exists:false` will filter them out in the UI layer.
  const workDir = mkdtempSync(join(tmpdir(), "yaver-web-headless-work-"));
  for (const sub of ["web", "marketing", "mobile", "mobile-native", "backend"]) {
    mkdirSync(join(workDir, sub), { recursive: true });
  }
  writeFileSync(join(workDir, "yaver.workspace.yaml"), WORKSPACE_YAML, "utf8");

  const port = await pickFreePort();
  const quicPort = await pickFreePort();
  const args = [
    "serve",
    "--debug",
    `--port=${port}`,
    `--quic-port=${quicPort}`,
    "--no-relay",
    "--no-tls",
    `--work-dir=${workDir}`,
  ];

  // `stdio: "pipe"` then closing stdin immediately is deliberately
  // different from `stdio: "ignore"`. On macOS, /dev/null (which
  // stdio:"ignore" wires in) is a character device, so the agent's
  // `stdinLooksInteractive()` returns true and fires the one-time
  // permission + host-share onboarding prompts. An anonymous pipe
  // closed at EOF is not a char device — those prompts skip cleanly.
  const proc = spawn(AGENT_BIN, args, {
    env: {
      ...process.env,
      HOME: homeDir,
      // Skip bootstrap mode when AuthToken alone is set (no Convex URL).
      YAVER_NO_BOOTSTRAP: "1",
      // The integration harness owns this short-lived foreground process; it
      // must not register launchd/systemd auto-start units on the host.
      YAVER_SKIP_AUTO_START: "1",
      YAVER_VAULT_SKIP_KEYCHAIN: "1",
      YAVER_DISABLE_WIZARD_AUTOINIT: "1",
    },
    stdio: ["pipe", "pipe", "pipe"],
    detached: false,
  });
  proc.stdin?.end();

  proc.stderr?.on("data", (d) => {
    const line = String(d).trim();
    if (line) process.stderr.write(`[agent] ${line}\n`);
  });
  proc.stdout?.on("data", (d) => {
    const line = String(d).trim();
    if (line) process.stderr.write(`[agent] ${line}\n`);
  });

  const baseUrl = `http://127.0.0.1:${port}`;
  try {
    await waitForHealth(baseUrl);
  } catch (e) {
    proc.kill("SIGTERM");
    throw e;
  }

  return { proc, baseUrl, homeDir, workDir };
}

async function stopAgent(h: Harness): Promise<void> {
  await new Promise<void>((resolve) => {
    if (h.proc.exitCode !== null || h.proc.killed) return resolve();
    h.proc.once("exit", () => resolve());
    h.proc.kill("SIGTERM");
    setTimeout(() => {
      if (h.proc.exitCode === null && !h.proc.killed) h.proc.kill("SIGKILL");
    }, 3000);
  });
  try {
    rmSync(h.homeDir, { recursive: true, force: true });
  } catch { /* best effort */ }
  try {
    rmSync(h.workDir, { recursive: true, force: true });
  } catch { /* best effort */ }
}

// ─────────────────────────────────────────────────────────────────────

const d = HAS_AGENT ? describe : describe.skip;

d("WebClient — real agent (workspace + Web Reload)", () => {
  let h: Harness;
  let client: WebClient;

  beforeAll(async () => {
    h = await startAgent();
    client = new WebClient({ token: TEST_TOKEN, agentBaseUrl: h.baseUrl });
    const r = await client.connect(TEST_DEVICE_ID);
    if (!r.ok) throw new Error(`connect failed: ${JSON.stringify(r.diagnostics)}`);
  }, 40_000);

  afterAll(async () => {
    if (h) await stopAgent(h);
  });

  test("getWorkspace returns the manifest + resolved root", async () => {
    const ws = await client.getWorkspace();
    expect(ws.ok).toBe(true);
    expect(ws.root).toBe(h.workDir);
    expect(ws.path).toBe(join(h.workDir, "yaver.workspace.yaml"));
    expect(ws.apps?.length).toBe(5);
  });

  test("getWorkspaceApps filters by kind=web,hybrid", async () => {
    const apps = await client.getWorkspaceApps("web,hybrid");
    expect(apps).toHaveLength(3);
    const names = new Set(apps.map((a) => a.name));
    expect(names).toEqual(new Set(["web", "marketing", "mobile"]));
    // backend (go) + mobile-native (react-native) are excluded.
    expect(names.has("backend")).toBe(false);
    expect(names.has("mobile-native")).toBe(false);
  });

  test("getWorkspaceApps returns kind field from agent", async () => {
    const apps = await client.getWorkspaceApps();
    const byName = Object.fromEntries(apps.map((a) => [a.name, a]));
    expect(byName["web"].kind).toBe("web");
    expect(byName["marketing"].kind).toBe("web");
    expect(byName["mobile"].kind).toBe("hybrid");
    expect(byName["mobile-native"].kind).toBe("mobile");
    // backend has stack=go → no kind.
    expect(byName["backend"].kind).toBeFalsy();
  });

  test("surface gate: mobile-native from web-reload → 400", async () => {
    await expect(
      client.startDevServer({ app: "mobile-native", surface: "web-reload" }),
    ).rejects.toThrow(/mobile-only|not available in Web Reload/);
  });

  test("surface gate: web-dashboard from hot-reload → 400", async () => {
    await expect(
      client.startDevServer({ app: "web", surface: "hot-reload" }),
    ).rejects.toThrow(/web-only|not available in Hot Reload/);
  });

  test("start web app → status surfaces kind=web and framework=nextjs", async () => {
    // The agent actually tries to boot a Metro/Next process, which
    // will fail (no node_modules in our stub dir). That's fine — we
    // only assert the state the agent records *before* the async
    // subprocess fails, which is visible immediately after Start()
    // returns. The 200 response path is what we care about.
    try {
      await client.startDevServer({ app: "web", surface: "web-reload" });
    } catch (e) {
      // Some frameworks 412 when node_modules is missing — that also
      // proves the gate passed and we reached the dev-server manager.
      expect(String(e)).toMatch(/HTTP 412|install|Cannot start/);
      return;
    }
    const status = await client.getDevServerStatus();
    expect(status?.framework).toBe("nextjs");
    // Kind is stamped the moment the server becomes active.
    if (status?.running) expect(status?.kind).toBe("web");
    await client.stopDevServer().catch(() => {});
  });

  test("unknown app → 400 with 'not in workspace manifest'", async () => {
    await expect(
      client.startDevServer({ app: "does-not-exist", surface: "web-reload" }),
    ).rejects.toThrow(/not in workspace manifest/);
  });
});
