// localBox.test.mts — synthetic "This phone" device + loopback reachability.
// Run: npx tsx src/lib/localBox.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  LOCAL_BOX_DEVICE_ID,
  LOCAL_BOX_BASE_URL,
  isLocalBoxId,
  buildLocalBoxDevice,
  probeLocalBox,
} from "./localBox.ts";

test("isLocalBoxId only matches the synthetic id", () => {
  assert.ok(isLocalBoxId(LOCAL_BOX_DEVICE_ID));
  assert.ok(!isLocalBoxId("conv-abc123"));
  assert.ok(!isLocalBoxId(null));
  assert.ok(!isLocalBoxId(undefined));
});

test("buildLocalBoxDevice produces an online loopback device the UI can use", () => {
  const d: any = buildLocalBoxDevice({ platform: "android", runnerIds: ["claude", "opencode"] });
  assert.equal(d.id, LOCAL_BOX_DEVICE_ID);
  assert.equal(d.host, "127.0.0.1");
  assert.equal(d.port, 18080);
  assert.equal(d.online, true);
  assert.equal(d.isPhone, true);
  assert.equal(d.local, true);
  assert.equal(d.os, "android");
  assert.deepEqual(d.installedRunnerIds, ["claude", "opencode"]);
  assert.equal(d.deviceClass, "edge-mobile");
});

test("probeLocalBox: connection error → not reachable", async () => {
  const fail = (async () => {
    throw new Error("ECONNREFUSED");
  }) as unknown as typeof fetch;
  const r = await probeLocalBox(fail);
  assert.equal(r.reachable, false);
});

test("probeLocalBox: 200 with version JSON → reachable + version", async () => {
  const ok = (async () => ({
    status: 200,
    json: async () => ({ version: "1.99.300" }),
  })) as unknown as typeof fetch;
  const r = await probeLocalBox(ok);
  assert.equal(r.reachable, true);
  assert.equal(r.agentVersion, "1.99.300");
  assert.ok(!r.stale);
});

test("probeLocalBox: 404 → reachable but stale binary", async () => {
  const stale = (async () => ({ status: 404, json: async () => ({}) })) as unknown as typeof fetch;
  const r = await probeLocalBox(stale);
  assert.equal(r.reachable, true);
  assert.equal(r.stale, true);
});

test("probeLocalBox: 401 (auth-gated) still counts as reachable", async () => {
  const authed = (async () => ({
    status: 401,
    json: async () => {
      throw new Error("not json");
    },
  })) as unknown as typeof fetch;
  const r = await probeLocalBox(authed);
  assert.equal(r.reachable, true);
  assert.ok(!r.stale);
});

test("LOCAL_BOX_BASE_URL is loopback:18080", () => {
  assert.equal(LOCAL_BOX_BASE_URL, "http://127.0.0.1:18080");
});
