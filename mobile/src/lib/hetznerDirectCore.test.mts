import assert from "node:assert/strict";
import test from "node:test";
import {
  listHetznerServers,
  listHetznerServerActions,
  powerOnHetznerServer,
  renameHetznerServer,
  shutdownHetznerServer,
  validateHetznerToken,
} from "./hetznerDirectCore.ts";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

test("lists sanitized servers with the token only in the authorization header", async () => {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const servers = await listHetznerServers("super-secret", async (url, init) => {
    calls.push({ url, init });
    return jsonResponse({ servers: [{
      id: 42,
      name: "private-box",
      status: "running",
      public_net: { ipv4: { ip: "192.0.2.4" } },
      server_type: { name: "cpx22" },
      datacenter: { location: { name: "fsn1" } },
      created: "2026-01-01T00:00:00Z",
    }] });
  });
  assert.equal(calls.length, 1);
  assert.doesNotMatch(calls[0].url, /super-secret/);
  assert.equal((calls[0].init?.headers as Record<string, string>).Authorization, "Bearer super-secret");
  assert.deepEqual(servers[0], {
    id: 42, name: "private-box", status: "running", ip: "192.0.2.4",
    type: "cpx22", location: "fsn1", created: "2026-01-01T00:00:00Z",
  });
  assert.doesNotMatch(JSON.stringify(servers), /super-secret/);
});

test("lists sanitized native Hetzner action history for one server", async () => {
  const calls: Array<{ url: string; authorization: string }> = [];
  const actions = await listHetznerServerActions("history-secret", 42, async (url, init) => {
    calls.push({ url, authorization: (init?.headers as Record<string, string>).Authorization });
    return jsonResponse({ actions: [{
      id: 9,
      command: "shutdown_server",
      status: "success",
      started: "2026-10-04T10:00:00Z",
      finished: "2026-10-04T10:00:08Z",
      progress: 100,
      error: null,
    }] });
  });
  assert.equal(calls[0].url, "https://api.hetzner.cloud/v1/servers/42/actions?sort=started:desc&per_page=20");
  assert.equal(calls[0].authorization, "Bearer history-secret");
  assert.doesNotMatch(calls[0].url, /history-secret/);
  assert.deepEqual(actions, [{
    id: 9,
    command: "shutdown_server",
    status: "success",
    started: "2026-10-04T10:00:00Z",
    finished: "2026-10-04T10:00:08Z",
    progress: 100,
    errorCode: null,
  }]);
});

test("validates and performs only exact server actions", async () => {
  const calls: Array<{ url: string; method: string }> = [];
  const fake = async (url: string, init?: RequestInit) => {
    calls.push({ url, method: String(init?.method || "GET") });
    if (url.endsWith("/servers/7")) {
      const poweringOn = calls.some((call) => call.url.endsWith("/actions/poweron"));
      const shuttingDown = calls.some((call) => call.url.endsWith("/actions/shutdown"));
      return jsonResponse({ server: {
        id: 7, name: "box", status: shuttingDown ? "off" : poweringOn ? "running" : "off",
        public_net: { ipv4: { ip: "192.0.2.7" } },
      } });
    }
    return jsonResponse({ action: { id: 1, status: "success" } });
  };
  await validateHetznerToken("token", fake);
  await powerOnHetznerServer("token", 7, fake);
  await shutdownHetznerServer("token", 7, fake);
  assert.deepEqual(calls.map((c) => [c.method, c.url.replace("https://api.hetzner.cloud/v1", "")]), [
    ["GET", "/servers?per_page=1"],
    ["GET", "/servers/7"],
    ["POST", "/servers/7/actions/poweron"],
    ["GET", "/servers/7"],
    ["GET", "/servers/7"],
    ["POST", "/servers/7/actions/shutdown"],
    ["GET", "/servers/7"],
  ]);
  assert.ok(calls.every((call) => call.method !== "DELETE"));
});

test("provider errors never echo provider messages or the credential", async () => {
  await assert.rejects(
    validateHetznerToken("do-not-echo", async () => jsonResponse({
      error: { code: "unauthorized", message: "bad do-not-echo for customer-resource" },
    }, 401)),
    (error: Error) => {
      assert.match(error.message, /rejected this API token/);
      assert.doesNotMatch(error.message, /do-not-echo|customer-resource/);
      return true;
    },
  );
});

test("waits for Hetzner's asynchronous action before reporting success", async () => {
  const calls: string[] = [];
  const fake = async (url: string, init?: RequestInit) => {
    calls.push(`${init?.method || "GET"} ${url.replace("https://api.hetzner.cloud/v1", "")}`);
    if (url.endsWith("/servers/7")) return jsonResponse({ server: {
      id: 7, name: "box", status: calls.includes("GET /actions/99") ? "running" : "off",
      public_net: { ipv4: { ip: "192.0.2.7" } },
    } });
    if (url.endsWith("/actions/poweron")) return jsonResponse({ action: { id: 99, status: "running" } });
    return jsonResponse({ action: { id: 99, status: "success" } });
  };
  const waits: number[] = [];
  await powerOnHetznerServer("token", 7, fake, async (milliseconds) => { waits.push(milliseconds); });
  assert.deepEqual(calls, ["GET /servers/7", "POST /servers/7/actions/poweron", "GET /actions/99", "GET /servers/7"]);
  assert.deepEqual(waits, [2000]);
});

test("refuses to report success if a power transition changes the server IP", async () => {
  let reads = 0;
  await assert.rejects(
    shutdownHetznerServer("token", 7, async (url) => {
      if (url.endsWith("/servers/7")) {
        reads += 1;
        return jsonResponse({ server: {
          id: 7, name: "box", status: reads === 1 ? "running" : "off",
          public_net: { ipv4: { ip: reads === 1 ? "192.0.2.7" : "192.0.2.8" } },
        } });
      }
      return jsonResponse({ action: { id: 3, status: "success" } });
    }),
    (error: any) => error?.code === "server_identity_changed",
  );
});

test("renames the same server through PUT without changing its IP", async () => {
  const calls: Array<{ method: string; path: string; body?: string }> = [];
  const renamed = await renameHetznerServer("token", 7, "mn71pn9c.cloud.yaver.io", async (url, init) => {
    const path = url.replace("https://api.hetzner.cloud/v1", "");
    calls.push({ method: String(init?.method || "GET"), path, body: init?.body as string | undefined });
    return jsonResponse({ server: {
      id: 7,
      name: init?.method === "PUT" ? "mn71pn9c.cloud.yaver.io" : "old-name",
      status: "running",
      public_net: { ipv4: { ip: "192.0.2.7" } },
    } });
  });
  assert.deepEqual(renamed, {
    id: 7, name: "mn71pn9c.cloud.yaver.io", status: "running", ip: "192.0.2.7",
    type: null, location: null, created: null,
  });
  assert.deepEqual(calls, [
    { method: "GET", path: "/servers/7", body: undefined },
    { method: "PUT", path: "/servers/7", body: '{"name":"mn71pn9c.cloud.yaver.io"}' },
  ]);
  assert.ok(calls.every((call) => call.method !== "DELETE"));
});
