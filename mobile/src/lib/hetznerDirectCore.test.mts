import assert from "node:assert/strict";
import test from "node:test";
import {
  listHetznerServers,
  powerOnHetznerServer,
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

test("validates and performs only exact server actions", async () => {
  const calls: Array<{ url: string; method: string }> = [];
  const fake = async (url: string, init?: RequestInit) => {
    calls.push({ url, method: String(init?.method || "GET") });
    return jsonResponse({ action: { id: 1, status: "success" } });
  };
  await validateHetznerToken("token", fake);
  await powerOnHetznerServer("token", 7, fake);
  await shutdownHetznerServer("token", 7, fake);
  assert.deepEqual(calls.map((c) => [c.method, c.url.replace("https://api.hetzner.cloud/v1", "")]), [
    ["GET", "/servers?per_page=1"],
    ["POST", "/servers/7/actions/poweron"],
    ["POST", "/servers/7/actions/shutdown"],
  ]);
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
    if (calls.length === 1) return jsonResponse({ action: { id: 99, status: "running" } });
    return jsonResponse({ action: { id: 99, status: "success" } });
  };
  const waits: number[] = [];
  await powerOnHetznerServer("token", 7, fake, async (milliseconds) => { waits.push(milliseconds); });
  assert.deepEqual(calls, ["POST /servers/7/actions/poweron", "GET /actions/99"]);
  assert.deepEqual(waits, [2000]);
});
