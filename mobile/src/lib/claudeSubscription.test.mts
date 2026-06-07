// claudeSubscription.test.mts — pin the subscription-OAuth transport so it can
// never silently regress to the metered API (the expensive path).
// Run: npx tsx src/lib/claudeSubscription.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  parseClaudeCredentials,
  serializeClaudeCredentials,
  isExpired,
  buildRefreshRequest,
  parseRefreshResponse,
  withClaudeCodeIdentity,
  buildMessagesRequest,
  assertSubscriptionRequest,
  CLAUDE_CODE_SYSTEM_IDENTITY,
  type ClaudeOAuthCreds,
} from "./claudeSubscription.ts";

const SUB = JSON.stringify({
  claudeAiOauth: {
    accessToken: "sk-ant-oat-abc",
    refreshToken: "sk-ant-ort-xyz",
    expiresAt: 2_000_000_000_000,
    scopes: ["user:inference"],
  },
});

test("parses the mirrored ~/.claude/.credentials.json subscription shape", () => {
  const c = parseClaudeCredentials(SUB);
  assert.ok(c);
  assert.equal(c!.accessToken, "sk-ant-oat-abc");
  assert.equal(c!.refreshToken, "sk-ant-ort-xyz");
  assert.equal(c!.expiresAt, 2_000_000_000_000);
  assert.deepEqual(c!.scopes, ["user:inference"]);
});

test("REJECTS a metered API key file so we never burn API credit", () => {
  const metered = JSON.stringify({ claudeAiOauth: { accessToken: "sk-ant-api-123" } });
  assert.equal(parseClaudeCredentials(metered), null);
});

test("returns null for non-credential json", () => {
  assert.equal(parseClaudeCredentials("{}"), null);
  assert.equal(parseClaudeCredentials("not json"), null);
});

test("credentials round-trip through serialize/parse", () => {
  const c = parseClaudeCredentials(SUB)!;
  const c2 = parseClaudeCredentials(serializeClaudeCredentials(c))!;
  assert.deepEqual(c2, c);
});

test("isExpired honours skew and unknown expiry", () => {
  const c: ClaudeOAuthCreds = { accessToken: "x", expiresAt: 1_000_000 };
  assert.equal(isExpired(c, 900_000), false);
  assert.equal(isExpired(c, 940_001, 60_000), true); // within 60s skew
  assert.equal(isExpired(c, 1_000_001), true);
  assert.equal(isExpired({ accessToken: "x" }, 999_999_999), false); // unknown → try it
});

test("buildRefreshRequest sends grant_type refresh_token + client_id, no secret leak", () => {
  const c = parseClaudeCredentials(SUB)!;
  const req = buildRefreshRequest(c);
  const body = JSON.parse(req.init.body);
  assert.equal(body.grant_type, "refresh_token");
  assert.equal(body.refresh_token, "sk-ant-ort-xyz");
  assert.ok(body.client_id, "client_id present");
  assert.equal(req.init.method, "POST");
});

test("buildRefreshRequest throws without a refresh token (must re-mirror)", () => {
  assert.throws(() => buildRefreshRequest({ accessToken: "x" }), /re-mirror/);
});

test("parseRefreshResponse updates token + expiry, preserves refresh token if not rotated", () => {
  const prev = parseClaudeCredentials(SUB)!;
  const updated = parseRefreshResponse(prev, { access_token: "sk-ant-oat-new", expires_in: 3600 }, 1_000_000);
  assert.equal(updated.accessToken, "sk-ant-oat-new");
  assert.equal(updated.refreshToken, "sk-ant-ort-xyz"); // preserved
  assert.equal(updated.expiresAt, 1_000_000 + 3600 * 1000);
});

test("identity block is injected FIRST (OAuth requirement)", () => {
  const blocks = withClaudeCodeIdentity("You help with RN apps.");
  assert.equal(blocks[0].text, CLAUDE_CODE_SYSTEM_IDENTITY);
  assert.equal(blocks[1].text, "You help with RN apps.");
  // No duplication when the caller already leads with the identity.
  const once = withClaudeCodeIdentity(CLAUDE_CODE_SYSTEM_IDENTITY);
  assert.equal(once.length, 1);
});

test("buildMessagesRequest uses Bearer + oauth beta + identity, NEVER x-api-key", () => {
  const c = parseClaudeCredentials(SUB)!;
  const req = buildMessagesRequest({
    creds: c,
    model: "claude-opus-4-7",
    system: "be terse",
    messages: [{ role: "user", content: "hi" }],
  });
  const h = req.init.headers;
  assert.equal(h.Authorization, "Bearer sk-ant-oat-abc");
  assert.equal(h["anthropic-beta"], "oauth-2025-04-20");
  assert.equal(h["x-api-key"], undefined);
  const body = JSON.parse(req.init.body);
  assert.equal(body.system[0].text, CLAUDE_CODE_SYSTEM_IDENTITY);
  assert.equal(body.model, "claude-opus-4-7");
  // The guard agrees this won't bill the API.
  assert.doesNotThrow(() => assertSubscriptionRequest(req));
});

test("assertSubscriptionRequest rejects a metered request", () => {
  assert.throws(
    () =>
      assertSubscriptionRequest({
        url: "x",
        init: { method: "POST", headers: { "x-api-key": "sk-ant-api-1" }, body: "{}" },
      }),
    /metered/,
  );
});

console.log("claudeSubscription: all assertions queued");
