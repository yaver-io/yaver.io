import assert from "node:assert/strict";

process.env.OAUTH_STATE_SECRET = "test-oauth-state-secret";
process.env.NEXT_PUBLIC_BASE_URL = "https://yaver.io";
process.env.YAVER_SDK_ALLOWED_ORIGINS = "https://app.example.com,https://*.customer.test";
process.env.NODE_ENV = "production";

const oauth = await import("../lib/oauth.ts");

function mustThrow(fn, message) {
  let threw = false;
  try {
    fn();
  } catch {
    threw = true;
  }
  assert.equal(threw, true, message);
}

{
  const encoded = oauth.encodeOAuthState({
    client: "sdk",
    intent: "signin",
    openerOrigin: "https://app.example.com",
  });
  const decoded = oauth.decodeOAuthState(encoded);
  assert.equal(decoded.client, "sdk");
  assert.equal(decoded.openerOrigin, "https://app.example.com");
  assert.equal(typeof decoded.iat, "number");
}

{
  const encoded = oauth.encodeOAuthState({ client: "web", returnTo: "/dashboard" });
  const [payload, signature] = encoded.split(".");
  const tampered = Buffer.from(
    JSON.stringify({ ...JSON.parse(Buffer.from(payload, "base64url").toString("utf8")), client: "sdk" })
  ).toString("base64url");
  mustThrow(
    () => oauth.decodeOAuthState(`${tampered}.${signature}`),
    "tampered OAuth state payload must be rejected"
  );
}

{
  const originalNow = Date.now;
  Date.now = () => 1_700_000_000_000;
  const encoded = oauth.encodeOAuthState({ client: "web" });
  Date.now = () => 1_700_000_000_000 + 31 * 60 * 1000;
  try {
    mustThrow(() => oauth.decodeOAuthState(encoded), "expired OAuth state must be rejected");
  } finally {
    Date.now = originalNow;
  }
}

assert.equal(oauth.sanitizeOpenerOrigin("https://app.example.com/path?x=1"), "https://app.example.com");
assert.equal(oauth.sanitizeOpenerOrigin("https://tenant.customer.test"), "https://tenant.customer.test");
assert.equal(oauth.sanitizeOpenerOrigin("https://customer.test"), undefined);
assert.equal(oauth.sanitizeOpenerOrigin("https://evil.example"), undefined);
assert.equal(oauth.sanitizeOpenerOrigin("javascript:alert(1)"), undefined);

process.env.NODE_ENV = "development";
assert.equal(oauth.sanitizeOpenerOrigin("http://localhost:5173"), "http://localhost:5173");

console.log("OAuth security regression tests passed");
