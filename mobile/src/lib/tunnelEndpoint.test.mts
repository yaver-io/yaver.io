import assert from "node:assert/strict";
import test from "node:test";
import { normalizeTunnelEndpoint } from "./tunnelEndpoint.ts";

test("keeps absolute HTTPS tunnel origins", () => {
  assert.equal(normalizeTunnelEndpoint("https://example.test/"), "https://example.test");
});

test("keeps cleartext only for private or loopback origins", () => {
  assert.equal(normalizeTunnelEndpoint("http://127.0.0.1:18080"), "http://127.0.0.1:18080");
  assert.equal(normalizeTunnelEndpoint("http://[::1]:18080"), "http://[::1]:18080");
  assert.equal(normalizeTunnelEndpoint("http://203.0.113.10:18080"), null);
});

test("rejects raw IPv6 and relative endpoints that fetch would resolve below Metro", () => {
  assert.equal(normalizeTunnelEndpoint("2a01:4f9:c014:8ef::1"), null);
  assert.equal(normalizeTunnelEndpoint("/agent-proxy"), null);
});

test("rejects credentialed or path-bearing tunnel values", () => {
  assert.equal(normalizeTunnelEndpoint("https://user:secret@example.test"), null);
  assert.equal(normalizeTunnelEndpoint("https://example.test/not-an-origin"), null);
});
