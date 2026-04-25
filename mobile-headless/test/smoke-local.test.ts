// Smoke test against a real `yaver serve`. Opt-in via the env var
// YMH_SMOKE_AGENT_URL; if unset, the test is skipped so `bun test`
// on a fresh clone doesn't need a Go toolchain.
//
// The CI workflow (.github/workflows/mobile-headless.yml) boots an
// agent and points this test at http://127.0.0.1:18080 with a
// shared CI token.

import { describe, it, expect } from "bun:test";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { MobileClient } from "../src/mobile-client";

const AGENT_URL = process.env.YMH_SMOKE_AGENT_URL || "";
const AGENT_TOKEN = process.env.YMH_SMOKE_AGENT_TOKEN || "";
const GLM_API_KEY = process.env.YMH_GLM_API_KEY || "";

const maybe = AGENT_URL ? describe : describe.skip;

async function waitForExec(
  mobile: MobileClient,
  execId: string,
  timeoutMs = 120000,
) {
  const start = Date.now();
  for (;;) {
    const exec = await mobile.getExec(execId);
    if (exec.status !== "running") return exec;
    if (Date.now() - start > timeoutMs) {
      throw new Error(`exec ${execId} timed out`);
    }
    await Bun.sleep(1000);
  }
}

maybe("smoke against live yaver agent", () => {
  const mobile = new MobileClient({
    agentBaseUrl: AGENT_URL,
    authToken: AGENT_TOKEN,
  });

  it("GET /info responds", async () => {
    const res = await mobile.raw.get("/info");
    expect(res.status).toBe(200);
    expect(res.body).toBeDefined();
  });

  it("install catalogue is non-empty", async () => {
    const list = await mobile.listInstallables();
    // A real agent ships >= the built-in list (git, docker, etc.).
    expect(list.length).toBeGreaterThan(3);
  });

  it("infra summary has machine + package managers", async () => {
    const s = await mobile.infraSummary();
    expect(s.machine).toBeDefined();
    expect(Array.isArray(s.packageManagers)).toBe(true);
  });

  it("bus presence reaches subscribers within 90 s", async () => {
    // Hits /bus/status + /bus/events on a real agent. Locks in:
    //   - the bus is enabled by default
    //   - peer/{self}/online or peer/{self}/ping arrives within
    //     the 60-s heartbeat tick + jitter (90 s upper bound)
    //
    // This is the live-agent counterpart of bus-subscribe.test.ts and
    // catches the regression where a cost-cutting refactor on the
    // agent (or a dropped relay env var) silently kills the bus
    // without flipping `enabled=false` — the symptom on mobile would
    // be device cards flapping offline between Convex's 5-min
    // heartbeats. Background to the regression in
    // CLAUDE.md "Networking Stack" → P0 audit.
    const status = await mobile.getBusStatus();
    expect(status).toBeDefined();
    expect(status.enabled).toBe(true);

    const events: Array<{ topic: string; publisher: string }> = [];
    let resolve: (() => void) | null = null;
    const got = new Promise<void>((r) => {
      resolve = r;
    });
    const unsub = mobile.subscribeBusEvents({
      prefix: "peer",
      onEvent: (evt) => {
        events.push({ topic: evt.topic, publisher: evt.publisher });
        if (/^peer\/.+\/(online|ping)$/.test(evt.topic) && resolve) {
          resolve();
          resolve = null;
        }
      },
    });
    try {
      const timeout = new Promise<void>((_r, rej) =>
        setTimeout(() => rej(new Error(`no peer/+/online|ping in 90s; saw ${events.length} events: ${events.slice(0, 3).map((e) => e.topic).join(",")}`)), 90_000),
      );
      await Promise.race([got, timeout]);
      expect(events.length).toBeGreaterThan(0);
    } finally {
      unsub();
    }
  }, 95_000);

  it("wizard round-trip: start → answer → generate (dummy dir)", async () => {
    const start = await mobile.wizard.start();
    expect(start?.session?.id).toBeDefined();
    // Walk straight to done by accepting defaults for every question.
    // The real wizard has ~30 questions; just ensure start() + one
    // answer() round-trips cleanly. Skipping generate() — the real
    // agent would actually scaffold a monorepo on disk.
    const ans = await mobile.wizard.answer(start.session.id, "app_name", "ymh-smoke");
    expect(ans?.session?.id).toBe(start.session.id);
  });

  const maybeGLM = GLM_API_KEY ? it : it.skip;

  maybeGLM("mobile -> go agent exec -> opencode glm writes hello world", async () => {
    const workDir = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-glm-"));
    const opencodeDataDir = path.join(workDir, ".opencode-data");
    fs.writeFileSync(
      path.join(workDir, "package.json"),
      JSON.stringify({ name: "ymh-glm-fixture", version: "0.0.1" }, null, 2),
      "utf8",
    );

    const opencodeConfig = JSON.stringify({
      $schema: "https://opencode.ai/config.json",
      model: "{env:YAVER_OPENCODE_MODEL}",
      provider: {
        glm: {
          npm: "@ai-sdk/openai-compatible",
          name: "GLM Coding Plan",
          options: {
            baseURL: "https://api.z.ai/api/coding/paas/v4",
            apiKey: "{env:GLM_API_KEY}",
          },
          models: {
            "glm-4.5-air": {
              name: "GLM-4.5-Air",
              limit: { context: 131072, output: 98304 },
            },
          },
        },
      },
    });

    const prompt = "Create a file named hello_glm.py containing a single-line Python program that prints exactly hello from glm ci. Do not modify any other files.";
    const command = [
      "set -euo pipefail",
      "mkdir -p \"$OPENCODE_DATA_DIR\"",
      `opencode run --pure --dangerously-skip-permissions --model \"$YAVER_OPENCODE_MODEL\" '${prompt}' < /dev/null`,
      "test -s hello_glm.py",
      "grep -q 'print' hello_glm.py",
    ].join("\n");

    const started = await mobile.startExec(command, {
      workDir,
      timeout: 180,
      env: {
        GLM_API_KEY,
        ZAI_API_KEY: GLM_API_KEY,
        OPENCODE_CONFIG_CONTENT: opencodeConfig,
        OPENCODE_DATA_DIR: opencodeDataDir,
        YAVER_OPENCODE_MODEL: "glm/glm-4.5-air",
      },
    });
    const gen = await waitForExec(mobile, started.execId, 180000);
    expect(gen.status).toBe("completed");
    expect(gen.exitCode).toBe(0);
    expect(fs.existsSync(path.join(workDir, "hello_glm.py"))).toBe(true);

    const run = await mobile.startExec("python3 hello_glm.py", {
      workDir,
      timeout: 30,
    });
    const out = await waitForExec(mobile, run.execId, 30000);
    expect(out.status).toBe("completed");
    expect(out.exitCode).toBe(0);
    expect(out.stdout.trim()).toBe("hello from glm ci");
  }, 180000);
});
