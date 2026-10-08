import assert from "node:assert/strict";
import test from "node:test";

import { bearerToken, constantTimeEqual, opaqueDeviceKey, sha256Hex, validDeviceId } from "./security";

test("bearer parsing rejects missing, short, and malformed credentials", () => {
  assert.equal(bearerToken(new Request("https://edge.test")), null);
  assert.equal(bearerToken(new Request("https://edge.test", { headers: { authorization: "Basic abc" } })), null);
  assert.equal(bearerToken(new Request("https://edge.test", { headers: { authorization: "Bearer short" } })), null);
  assert.equal(bearerToken(new Request("https://edge.test", { headers: { authorization: `Bearer ${"a".repeat(32)}` } })), "a".repeat(32));
});

test("device ids are bounded and path-safe", () => {
  for (const valid of ["device-1", "abc.def:3", "A_1"]) assert.equal(validDeviceId(valid), true, valid);
  for (const invalid of ["", "/other", "../other", "has space", "x".repeat(129)]) assert.equal(validDeviceId(invalid), false, invalid);
});

test("opaque object keys are stable, secret-scoped, and hide device ids", async () => {
  const secretA = "a".repeat(32);
  const secretB = "b".repeat(32);
  const first = await opaqueDeviceKey("device-1", secretA);
  assert.equal(first, await opaqueDeviceKey("device-1", secretA));
  assert.notEqual(first, await opaqueDeviceKey("device-1", secretB));
  assert.equal(first.includes("device-1"), false);
  assert.equal(first.length, 64);
});

test("session hashes are deterministic and constant-time comparison is exact", async () => {
  assert.equal(await sha256Hex("token"), "3c469e9d6c5875d37a43f353d4f88e61fcf812c66eee3457465a40b0da4153e0");
  assert.equal(constantTimeEqual("owner-a", "owner-a"), true);
  assert.equal(constantTimeEqual("owner-a", "owner-b"), false);
  assert.equal(constantTimeEqual("owner-a", "owner-aa"), false);
});
