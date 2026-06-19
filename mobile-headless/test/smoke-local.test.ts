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
const BUILD_NATIVE_PROJECT_PATH = process.env.YMH_BUILD_NATIVE_PROJECT_PATH || "";
const SKIP_BUNDLEID_FIXTURE_SMOKE = process.env.YMH_SKIP_BUNDLEID_FIXTURE_SMOKE === "1";

const maybe = AGENT_URL ? describe : describe.skip;
const maybeBuildNative = BUILD_NATIVE_PROJECT_PATH ? it : it.skip;
const maybeBundleIdFixture = SKIP_BUNDLEID_FIXTURE_SMOKE ? it.skip : it;

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

async function waitForMobileProject(
  mobile: MobileClient,
  projectName: string,
  timeoutMs = 20000,
) {
  const start = Date.now();
  for (;;) {
    const res = await mobile.raw.get("/projects/mobile");
    const projects = Array.isArray(res.body?.projects) ? res.body.projects : [];
    const found = projects.find((p: any) => {
      const name = String(p?.name ?? "");
      const projectPath = String(p?.path ?? "");
      return (
        name === projectName ||
        name.startsWith(`${projectName} /`) ||
        path.basename(projectPath) === projectName
      );
    });
    if (found) return found;
    if (Date.now() - start > timeoutMs) {
      throw new Error(`mobile project ${projectName} not discovered within ${timeoutMs}ms`);
    }
    await Bun.sleep(500);
  }
}

function writeScannableBrokenExpoProject(
  root: string,
  projectName: string,
  bundleField: "bundleIdentifier" | "package",
  bundleId: string,
) {
  fs.mkdirSync(root, { recursive: true });
  fs.writeFileSync(
    path.join(root, "package.json"),
    '{\n  "name": "' + projectName + '",\n  "dependencies": { "expo": "~52.0.0" }\n',
    "utf8",
  );
  const platformConfig =
    bundleField === "bundleIdentifier"
      ? `"ios": { "bundleIdentifier": "${bundleId}" }`
      : `"android": { "package": "${bundleId}" }`;
  fs.writeFileSync(
    path.join(root, "app.json"),
    `{\n  "expo": {\n    "name": "${projectName}",\n    ${platformConfig}\n  }\n}\n`,
    "utf8",
  );
}

function assertResolvedPastActiveDevServerFailure(
  response: { status: number; body: any },
  _bundleId: string,
) {
  const text = JSON.stringify(response.body ?? {});
  expect(text).not.toContain("no active dev server");
  expect(text).not.toContain("start one first OR pass projectName / projectPath / bundleId");
  expect(response.status).not.toBe(400);
}

function assertBuildNativeContract(
  response: { status: number; body: any },
  platform: "ios" | "android",
) {
  const text = JSON.stringify(response.body ?? {});
  expect(text).not.toContain("no active dev server");
  expect(text).not.toContain("start one first OR pass projectName / projectPath / bundleId");
  expect(response.status).not.toBe(400);
  if (response.status === 200) {
    expect(response.body?.status).toBe("ok");
    expect(typeof response.body?.bundleUrl).toBe("string");
    expect(typeof response.body?.bcVersion).toBe("number");
    expect(response.body?.platform).toBe(platform);
    return;
  }
  expect([409, 500, 504]).toContain(response.status);
  expect(typeof response.body?.error).toBe("string");
}

maybe("smoke against live yaver agent", () => {
  const mobile = new MobileClient({
    agentBaseUrl: AGENT_URL,
    authToken: AGENT_TOKEN,
    platform: "ios",
  });
  const mobileAndroid = new MobileClient({
    agentBaseUrl: AGENT_URL,
    authToken: AGENT_TOKEN,
    platform: "android",
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

  maybeBundleIdFixture("bundleId-only reload resolves ios and android projects without an active dev server", async () => {
    const home = process.env.HOME;
    if (!home) throw new Error("HOME is required for project discovery smoke");

    const workspaceRoot = path.join(home, "Workspace");
    const iosName = "ymh-ios-bundleid";
    const androidName = "ymh-android-bundleid";
    const iosBundleId = "com.yaver.ymh.ios";
    const androidBundleId = "com.yaver.ymh.android";

    writeScannableBrokenExpoProject(
      path.join(workspaceRoot, iosName),
      iosName,
      "bundleIdentifier",
      iosBundleId,
    );
    writeScannableBrokenExpoProject(
      path.join(workspaceRoot, androidName),
      androidName,
      "package",
      androidBundleId,
    );

    const rescan = await mobile.raw.post("/projects/mobile");
    expect(rescan.status).toBe(200);
    await waitForMobileProject(mobile, iosName);
    await waitForMobileProject(mobile, androidName);

    const iosReload = await mobile.raw.post("/dev/reload-app", {
      mode: "bundle",
      bundleId: iosBundleId,
    });
    assertResolvedPastActiveDevServerFailure(iosReload, iosBundleId);

    const androidReload = await mobileAndroid.raw.post("/dev/reload-app", {
      mode: "bundle",
      bundleId: androidBundleId,
    });
    assertResolvedPastActiveDevServerFailure(androidReload, androidBundleId);
  }, 30000);

  maybeBuildNative("build-native returns a structured contract for ios and android against a real project", async () => {
    const iosBuild = await mobile.raw.post("/dev/build-native", {
      platform: "ios",
      projectPath: BUILD_NATIVE_PROJECT_PATH,
      consumerVersion: "mobile-headless",
      consumerBuild: "headless",
      consumerSdkVersion: "headless",
      consumerHermesBCVersion: 96,
    });
    assertBuildNativeContract(iosBuild, "ios");

    const androidBuild = await mobileAndroid.raw.post("/dev/build-native", {
      platform: "android",
      projectPath: BUILD_NATIVE_PROJECT_PATH,
      consumerVersion: "mobile-headless",
      consumerBuild: "headless",
      consumerSdkVersion: "headless",
      consumerHermesBCVersion: 96,
    });
    assertBuildNativeContract(androidBuild, "android");
  }, 15 * 60_000);

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
