// Wire-level coverage for /dev/stop against the same MobileClient code
// the real Yaver mobile app uses. Three guarantees the Hot Reload tab
// (mobile/app/(tabs)/hotreload.tsx) and the web Dev Server panes
// (web/components/dashboard/{PreviewPane,WebReloadView}.tsx) rely on:
//
//   1. The agent-1.99.93+ shape lands intact: { ok, verified,
//      buildsCancelled, framework, workDir, message }. A regression
//      that strips one of these fields will leave the UI showing
//      "Stopping…" forever or hide the post-stop banner.
//   2. verified=false surfaces as a soft failure to the client — the
//      mobile/web banner switches to the red ⚠ "Stop incomplete"
//      state, not the green ✓.
//   3. Pre-1.99.93 (legacy) agents that don't return verified still
//      look successful so older boxes don't show a permanent error
//      pill on every stop.

import { afterAll, beforeAll, describe, expect, it } from "bun:test";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { MobileClient } from "../src/mobile-client";
import { startMockAgent, type MockAgentHandle } from "../src/mock-agent";

let agent: MockAgentHandle;
let mobile: MobileClient;

beforeAll(async () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-dev-stop-"));
  process.env.YMH_DATA_DIR = dataDir;
  agent = await startMockAgent({ token: "mock-token" });
  mobile = new MobileClient({
    dataDir,
    authToken: "mock-token",
    convexUrl: agent.baseUrl,
  });
  mobile.useAgentBaseUrl(agent.baseUrl);
});

afterAll(async () => {
  await agent.close();
});

describe("devServer.stop — agent 1.99.93 contract", () => {
  it("returns { ok, verified=true, buildsCancelled } on the happy path", async () => {
    agent.setStopMode("verified", { buildsCancelled: 0 });
    const r: any = await mobile.devServer.stop();
    expect(r.ok).toBe(true);
    expect(r.verified).toBe(true);
    expect(r.buildsCancelled).toBe(0);
    expect(r.framework).toBe("expo");
    expect(r.workDir).toBe("/mock/sfmg");
  });

  it("surfaces buildsCancelled when an in-flight Hermes build was killed", async () => {
    agent.setStopMode("verified", { buildsCancelled: 2 });
    const r: any = await mobile.devServer.stop();
    expect(r.ok).toBe(true);
    expect(r.verified).toBe(true);
    expect(r.buildsCancelled).toBe(2);
  });

  it("returns verified=false when the agent could not confirm subprocess exit", async () => {
    agent.setStopMode("not-verified", {
      buildsCancelled: 1,
      message: "Subprocess didn't exit within 7s; SIGKILL issued.",
    });
    const r: any = await mobile.devServer.stop();
    expect(r.ok).toBe(false);
    expect(r.verified).toBe(false);
    expect(r.buildsCancelled).toBe(1);
    expect(r.message).toContain("SIGKILL");
  });

  it("falls back to ok=false on a 5xx response", async () => {
    agent.setStopMode("fail", { message: "mock: bundler held a lock" });
    const r: any = await mobile.devServer.stop();
    expect(r.ok).toBe(false);
    // The MobileClient maps {error|message} into `error` when the HTTP
    // call itself fails; we accept either shape so the assertion holds
    // across both legacy and current client wrapping.
    const surfaced = r.error || r.message || "";
    expect(surfaced).toContain("bundler");
  });

  it("treats a legacy (pre-1.99.93) success response as a happy stop", async () => {
    agent.setStopMode("legacy");
    const r: any = await mobile.devServer.stop();
    expect(r.ok).toBe(true);
    expect(r.stoppedServing).toBe(true);
    // Legacy agents don't include the new fields — UI must not crash.
    expect(r.verified).toBeUndefined();
    expect(r.buildsCancelled).toBeUndefined();
  });
});
