// remoteApplyTarget.test.mts — Hermes-only-remote apply layer writes/deletes on
// the box via /host-share/fs/*. Run: npx tsx src/lib/remoteApplyTarget.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import { makeRemoteApplyTarget } from "./remoteApplyTarget.ts";

interface Captured {
  url: string;
  method?: string;
  headers: Record<string, string>;
  body: any;
}

function recorder(responder: () => any) {
  const calls: Captured[] = [];
  const fetchImpl = (async (url: string, init: any) => {
    calls.push({
      url,
      method: init?.method,
      headers: init?.headers ?? {},
      body: init?.body ? JSON.parse(init.body) : undefined,
    });
    return responder();
  }) as unknown as typeof fetch;
  return { calls, fetchImpl };
}

const okRes = () => ({ ok: true, status: 200, json: async () => ({ ok: true }), text: async () => "" });

function cfg(fetchImpl: typeof fetch) {
  return {
    baseUrl: "https://relay.example/d/box-1/",
    headers: { Authorization: "Bearer tok" },
    root: "ws",
    rootPath: "/home/u/proj",
    fetchImpl,
  };
}

test("writeSourceFile POSTs host-share write with root/rootPath/path/content + headers", async () => {
  const { calls, fetchImpl } = recorder(okRes);
  const t = makeRemoteApplyTarget(cfg(fetchImpl));
  await t.writeSourceFile("slug", "src/App.tsx", "export const x = 1;");

  assert.equal(calls.length, 1);
  const c = calls[0];
  assert.match(c.url, /\/host-share\/fs\/write$/);
  assert.equal(c.method, "POST");
  assert.equal(c.headers["X-Yaver-HostShare"], "true");
  assert.equal(c.headers["Authorization"], "Bearer tok");
  assert.equal(c.headers["Content-Type"], "application/json");
  assert.deepEqual(c.body, {
    root: "ws",
    rootPath: "/home/u/proj",
    path: "src/App.tsx",
    content: "export const x = 1;",
  });
});

test("trailing slash in baseUrl is normalized (no //host-share)", async () => {
  const { calls, fetchImpl } = recorder(okRes);
  const t = makeRemoteApplyTarget(cfg(fetchImpl));
  await t.writeSourceFile("s", "a.ts", "x");
  assert.ok(!calls[0].url.includes("//host-share"));
  assert.ok(calls[0].url.startsWith("https://relay.example/d/box-1/host-share/fs/write"));
});

test("deleteSourceFile POSTs host-share delete and strips a leading slash", async () => {
  const { calls, fetchImpl } = recorder(okRes);
  const t = makeRemoteApplyTarget(cfg(fetchImpl));
  await t.deleteSourceFile("slug", "/src/old.ts");
  assert.match(calls[0].url, /\/host-share\/fs\/delete$/);
  assert.equal(calls[0].body.path, "src/old.ts"); // leading slash removed
});

test("HTTP error throws with status + snippet", async () => {
  const { fetchImpl } = recorder(() => ({
    ok: false,
    status: 403,
    json: async () => ({}),
    text: async () => "forbidden: not your project",
  }));
  const t = makeRemoteApplyTarget(cfg(fetchImpl));
  await assert.rejects(() => t.writeSourceFile("s", "a.ts", "x"), /403.*forbidden/);
});

test("200 with ok:false (agent-level rejection) throws", async () => {
  const { fetchImpl } = recorder(() => ({
    ok: true,
    status: 200,
    json: async () => ({ ok: false, error: "readonly root" }),
    text: async () => "",
  }));
  const t = makeRemoteApplyTarget(cfg(fetchImpl));
  await assert.rejects(() => t.deleteSourceFile("s", "a.ts"), /readonly root/);
});

test("non-JSON 200 (older agent) is tolerated", async () => {
  const { fetchImpl } = recorder(() => ({
    ok: true,
    status: 200,
    json: async () => {
      throw new Error("not json");
    },
    text: async () => "",
  }));
  const t = makeRemoteApplyTarget(cfg(fetchImpl));
  await t.writeSourceFile("s", "a.ts", "x"); // must not throw
});
