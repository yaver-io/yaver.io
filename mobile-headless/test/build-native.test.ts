// Hermetic coverage for the /dev/build-native client contract used by
// DevPreview / Hot Reload in the real mobile app. Three guarantees the
// real UI relies on:
//
//   1. Happy path: a 200 response surfaces { status: "ok", bundleUrl }
//      so the loader knows where to fetch the Hermes bundle.
//   2. Hang path: when the agent never replies (busted Metro, dead
//      relay, crashed bundler), the client honours its own deadline
//      and rejects with AbortError. This is the regression we just
//      fixed in mobile/src/components/DevPreview.tsx — without the
//      AbortController the UI sat on "Building..." forever.
//   3. Fast-fail path: when the agent reports a structured failure
//      (timed-out bundler, missing node_modules, etc.) the client
//      returns the body so the UI can show the real reason.

import { afterAll, beforeAll, describe, expect, it } from "bun:test";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { MobileClient } from "../src/mobile-client";
import { startMockAgent, type MockAgentHandle } from "../src/mock-agent";

let agent: MockAgentHandle;
let mobile: MobileClient;
let mobileAndroid: MobileClient;

beforeAll(async () => {
  const dataDir = fs.mkdtempSync(path.join(os.tmpdir(), "ymh-build-native-"));
  process.env.YMH_DATA_DIR = dataDir;
  agent = await startMockAgent({ token: "mock-token" });
  mobile = new MobileClient({
    dataDir,
    authToken: "mock-token",
    platform: "ios",
    convexUrl: agent.baseUrl,
  });
  mobileAndroid = new MobileClient({
    dataDir,
    authToken: "mock-token",
    platform: "android",
    convexUrl: agent.baseUrl,
  });
  mobile.useAgentBaseUrl(agent.baseUrl);
  mobileAndroid.useAgentBaseUrl(agent.baseUrl);
});

afterAll(async () => {
  await agent.close();
});

describe("devServer.buildNative", () => {
  it("returns the bundle descriptor on the happy path", async () => {
    agent.setBuildNativeMode("ok");
    const r = await mobile.devServer.buildNative("ios");
    expect(r.status).toBe(200);
    expect(r.body?.status).toBe("ok");
    expect(r.body?.bundleUrl).toBe("/dev/native-bundle");
    expect(r.body?.bcVersion).toBe(96);
    expect(r.body?.platform).toBe("ios");
    expect(agent.getLastBuildNativeRequest()).toEqual(
      expect.objectContaining({
        platform: "ios",
        consumerVersion: "mobile-headless",
        consumerBuild: "headless",
        consumerSdkVersion: "headless",
        consumerHermesBCVersion: 96,
      }),
    );
  });

  it("builds and parses Hermes metadata for android mode too", async () => {
    agent.setBuildNativeMode("ok");
    const r = await mobileAndroid.devServer.buildNative("android");
    expect(r.status).toBe(200);
    expect(r.body?.status).toBe("ok");
    expect(r.body?.bundleUrl).toBe("/dev/native-bundle");
    expect(r.body?.bcVersion).toBe(96);
    expect(r.body?.platform).toBe("android");
    expect(agent.getLastBuildNativeRequest()).toEqual(
      expect.objectContaining({
        platform: "android",
        consumerVersion: "mobile-headless",
        consumerBuild: "headless",
        consumerSdkVersion: "headless",
        consumerHermesBCVersion: 96,
      }),
    );
  });

  it("aborts with AbortError when the agent hangs past the deadline", async () => {
    agent.setBuildNativeMode("hang");
    const start = Date.now();
    let caught: any = null;
    try {
      // 250ms is way under the 12-min default — proves the per-call
      // override actually wires the abort signal through to fetch.
      await mobile.devServer.buildNative("ios", { timeoutMs: 250 });
    } catch (e: any) {
      caught = e;
    }
    const elapsed = Date.now() - start;
    expect(caught).not.toBeNull();
    // node/bun's fetch surfaces aborted requests as either AbortError
    // or DOMException with name "AbortError" — accept either signal.
    expect(caught?.name === "AbortError" || /aborted/i.test(String(caught))).toBe(true);
    // Sanity: the test must give up well before the 12-min default
    // would expire. 5s of slack is generous on a slow CI runner.
    expect(elapsed).toBeLessThan(5_000);
  });

  it("respects an external AbortSignal", async () => {
    agent.setBuildNativeMode("hang");
    const ctrl = new AbortController();
    setTimeout(() => ctrl.abort(), 100);
    let caught: any = null;
    try {
      await mobile.devServer.buildNative("android", { signal: ctrl.signal });
    } catch (e: any) {
      caught = e;
    }
    expect(caught).not.toBeNull();
    expect(caught?.name === "AbortError" || /aborted/i.test(String(caught))).toBe(true);
  });

  it("surfaces structured failure responses without throwing", async () => {
    agent.setBuildNativeMode("fail");
    const r = await mobile.devServer.buildNative("ios");
    expect(r.status).toBe(500);
    expect(r.body?.error).toMatch(/bundle failed/);
    expect(r.body?.helpHint).toBeDefined();
  });

  it("surfaces compatibility blocks with the same contract as the mobile app", async () => {
    agent.setBuildNativeMode("blocked");
    const r = await mobile.devServer.buildNative("ios");
    expect(r.status).toBe(409);
    expect(r.body?.status).toBe("blocked");
    expect(r.body?.code).toBe("NATIVE_MODULE_INCOMPATIBLE");
    expect(r.body?.incompatibleNativeModules).toEqual(["react-native-fictional"]);
    expect(r.body?.bcVersion).toBe(96);
    expect(r.body?.supportedRNRange).toBe("0.81.x");
    expect(r.body?.platform).toBe("ios");
  });

  it("surfaces compatibility blocks in android mode with the same Hermes metadata", async () => {
    agent.setBuildNativeMode("blocked");
    const r = await mobileAndroid.devServer.buildNative("android");
    expect(r.status).toBe(409);
    expect(r.body?.status).toBe("blocked");
    expect(r.body?.code).toBe("NATIVE_MODULE_INCOMPATIBLE");
    expect(r.body?.incompatibleNativeModules).toEqual(["react-native-fictional"]);
    expect(r.body?.bcVersion).toBe(96);
    expect(r.body?.supportedRNRange).toBe("0.81.x");
    expect(r.body?.platform).toBe("android");
  });

  it("surfaces native module version mismatches in ios mode", async () => {
    agent.setBuildNativeMode("blocked-version");
    const r = await mobile.devServer.buildNative("ios");
    expect(r.status).toBe(409);
    expect(r.body?.status).toBe("blocked");
    expect(r.body?.code).toBe("NATIVE_MODULE_VERSION_MISMATCH");
    expect(r.body?.nativeModuleVersionMismatches).toEqual([
      expect.objectContaining({
        name: "react-native-worklets",
        projectVersion: "0.7.4",
        hostVersion: "0.5.1",
        reason: "0.x minor version differs",
      }),
    ]);
    expect(r.body?.platform).toBe("ios");
  });

  it("surfaces react version mismatches in android mode", async () => {
    agent.setBuildNativeMode("blocked-react");
    const r = await mobileAndroid.devServer.buildNative("android");
    expect(r.status).toBe(409);
    expect(r.body?.status).toBe("blocked");
    expect(r.body?.code).toBe("REACT_VERSION_MISMATCH");
    expect(r.body?.reactVersionMismatch).toEqual(
      expect.objectContaining({
        projectVersion: "20.0.0",
        hostVersion: "19.1.0",
        reason: "major version differs",
      }),
    );
    expect(r.body?.platform).toBe("android");
  });

  it("surfaces Hermes bytecode mismatch metadata without throwing", async () => {
    agent.setBuildNativeMode("bc-mismatch");
    const r = await mobile.devServer.buildNative("ios");
    expect(r.status).toBe(409);
    expect(r.body?.status).toBe("blocked");
    expect(r.body?.code).toBe("BC_VERSION_MISMATCH");
    expect(r.body?.bcVersion).toBe(95);
    expect(r.body?.platform).toBe("ios");
  });

  it("recovers to ok after a transient failure", async () => {
    agent.setBuildNativeMode("fail");
    await mobile.devServer.buildNative("ios");
    agent.setBuildNativeMode("ok");
    const r = await mobile.devServer.buildNative("ios");
    expect(r.status).toBe(200);
    expect(r.body?.status).toBe("ok");
  });
});
