import assert from "node:assert/strict";
import test from "node:test";
import { readAgentHealth } from "./agentHealth.ts";

test("accepts a real Yaver agent health response", async () => {
  const health = await readAgentHealth(new Response(JSON.stringify({ ok: true, lifecycleState: "ready-to-connect", version: "1.2.3" }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  }));
  assert.deepEqual(health, { ok: true, lifecycleState: "ready-to-connect", authExpired: false, hostname: undefined, version: "1.2.3" });
});

test("rejects a 200 HTML fallback instead of declaring a connection", async () => {
  const health = await readAgentHealth(new Response("<!DOCTYPE html><title>Metro</title>", {
    status: 200,
    headers: { "Content-Type": "text/html" },
  }));
  assert.equal(health, null);
});

test("rejects arbitrary JSON that is not the agent health contract", async () => {
  assert.equal(await readAgentHealth(new Response(JSON.stringify({ ok: true }), { status: 200 })), null);
});
