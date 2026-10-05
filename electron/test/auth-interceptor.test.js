"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const {
  stripAuthFromUrl,
  applyAuthHeaders,
  applyKnownAuthHeaders,
  isAgentRoute,
  isDeviceScoped,
  TRUSTED_MULTI_TENANT_ORIGINS,
} = require("../src/auth-interceptor");

test("stripAuthFromUrl removes token and __rp", () => {
  const out = stripAuthFromUrl("http://127.0.0.1:18080/dev/events?token=abc123&__rp=secret&caller=web-dashboard");
  assert.equal(out.url, "http://127.0.0.1:18080/dev/events?caller=web-dashboard");
  assert.equal(out.token, "abc123");
  assert.equal(out.rp, "secret");
});

test("stripAuthFromUrl leaves untouched URLs alone", () => {
  const out = stripAuthFromUrl("https://yaver.io/dashboard?tab=chat");
  assert.equal(out.url, "https://yaver.io/dashboard?tab=chat");
  assert.equal(out.token, null);
  assert.equal(out.rp, null);
});

test("stripAuthFromUrl handles missing params", () => {
  const out = stripAuthFromUrl("http://localhost:3000/dev/events?token=only&caller=web-dashboard");
  assert.equal(out.url, "http://localhost:3000/dev/events?caller=web-dashboard");
  assert.equal(out.token, "only");
  assert.equal(out.rp, null);
});

test("stripAuthFromUrl preserves application account tokens", () => {
  const url = "https://yaver.io/account/merge?token=merge-token";
  const out = stripAuthFromUrl(url);
  assert.equal(out.url, url);
  assert.equal(out.token, null);
  assert.equal(out.rp, null);
});

test("applyAuthHeaders strips + injects on first sight", () => {
  const authByOrigin = new Map();
  const { headers, url } = applyAuthHeaders({
    url: "https://agent.dev/stream?token=TOK&__rp=RP&caller=web-dashboard",
    headers: { "User-Agent": "x" },
    authByOrigin,
  });
  assert.equal(url, "https://agent.dev/stream?caller=web-dashboard");
  assert.equal(headers["Authorization"], "Bearer TOK");
  assert.equal(headers["X-Relay-Password"], "RP");
  assert.equal(headers["User-Agent"], "x");
});

test("applyAuthHeaders reuses captured material for later header-less URLs", () => {
  const authByOrigin = new Map();
  // First request carries the params; captures them per-origin.
  applyAuthHeaders({
    url: "http://10.0.0.5:18080/dev/events?token=TOK&__rp=RP",
    headers: {},
    authByOrigin,
  });
  // Follow-up stream to the same origin has no params (agent-client lazily
  // re-appends, but a second stream built before connect() would not).
  const second = applyAuthHeaders({
    url: "http://10.0.0.5:18080/vibing/preview/events",
    headers: {},
    authByOrigin,
  });
  assert.equal(second.headers["Authorization"], "Bearer TOK");
  assert.equal(second.headers["X-Relay-Password"], "RP");
  // ...and the URL still carries no token.
  assert.ok(!second.url.includes("token="));
});

test("applyAuthHeaders never overwrites an explicit Authorization header", () => {
  const authByOrigin = new Map();
  const { headers } = applyAuthHeaders({
    url: "http://x/stream?token=TOK&caller=web-dashboard",
    headers: { Authorization: "Bearer ALREADY" },
    authByOrigin,
  });
  assert.equal(headers["Authorization"], "Bearer ALREADY");
});

test("header injection treats lowercase names as already present", () => {
  const authByOrigin = new Map([["https://agent.dev", { token: "TOK", rp: "RP" }]]);
  const headers = applyKnownAuthHeaders({
    url: "https://agent.dev/events",
    headers: { authorization: "Bearer EXISTING", "x-relay-password": "EXISTING-RP" },
    authByOrigin,
  });
  assert.equal(headers.authorization, "Bearer EXISTING");
  assert.equal(headers["x-relay-password"], "EXISTING-RP");
  assert.equal(headers.Authorization, undefined);
  assert.equal(headers["X-Relay-Password"], undefined);
});

test("applyAuthHeaders does not leak secrets into origin-foreign requests", () => {
  const authByOrigin = new Map();
  applyAuthHeaders({
    url: "https://relay.yaver.io/p?token=TOK&__rp=RP",
    headers: {},
    authByOrigin,
  });
  const other = applyAuthHeaders({
    url: "https://example.com/plain",
    headers: {},
    authByOrigin,
  });
  assert.equal(other.headers["Authorization"], undefined);
  assert.equal(other.headers["X-Relay-Password"], undefined);
});

// --- Path scoping (M2, audit pass-2) ------------------------------------

test("isAgentRoute accepts agent API paths and rejects non-agent paths", () => {
  for (const agentPath of [
    "/d/abc123/dev/events",
    "/proxy/abc123/stream",
    "/voice/stream",
    "/dev/events",
    "/tasks",
    "/agent/status",
    "/health",
    "/info",
    "/vibing/preview/events",
  ]) {
    assert.equal(isAgentRoute(`https://relay.yaver.io${agentPath}`), true, agentPath);
  }
  for (const nonAgentPath of [
    "/",
    "/_next/static/chunks/x.js",
    "/__nextjs_original-stack-frame",
    "/assets/logo.png",
    "/favicon.ico",
    "/images/bg.jpg",
    "/static/foo.css",
    "/screenshots/a.png",
    "/api/auth/login",
    "/auth",
    "/dashboard",
    "/studio",
    "/workspace",
    "/account/settings",
    "/pricing",
    "/blog/stt-tts-voice-local-byok",
    "/sitemap.xml",
  ]) {
    assert.equal(isAgentRoute(`https://yaver.io${nonAgentPath}`), false, nonAgentPath);
  }
});

test("isDeviceScoped only accepts /d/ and /proxy/ paths", () => {
  assert.equal(isDeviceScoped("https://relay.yaver.io/d/abc/dev/events"), true);
  assert.equal(isDeviceScoped("https://relay.yaver.io/proxy/abc/health"), true);
  assert.equal(isDeviceScoped("https://relay.yaver.io/health"), false);
  assert.equal(isDeviceScoped("https://relay.yaver.io/voice/stream"), false);
});

test("stripAuthFromUrl never marks non-agent paths as capturable", () => {
  for (const url of [
    "https://yaver.io/_next/static/x.js?token=TOK&__rp=RP&caller=web-dashboard",
    "https://yaver.io/assets/logo.png?token=TOK&caller=web-dashboard",
    "https://relay.yaver.io/robots.txt?token=TOK&__rp=RP",
    "https://yaver.io/pricing?token=TOK&caller=web-dashboard",
  ]) {
    const out = stripAuthFromUrl(url);
    assert.equal(out.capture, false, url);
  }
});

test("stripAuthFromUrl captures only on agent routes", () => {
  const out = stripAuthFromUrl(
    "https://yaver.io/d/abc/dev/events?token=TOK&__rp=RP&caller=web-dashboard",
  );
  assert.equal(out.capture, true);
  assert.equal(out.token, "TOK");
  assert.equal(out.rp, "RP");
});

test("non-agent path on a captured origin never receives the bearer", () => {
  const authByOrigin = new Map([["https://agent.dev", { token: "TOK", rp: "RP" }]]);
  const headers = applyKnownAuthHeaders({
    url: "https://agent.dev/_next/static/x.js",
    headers: {},
    authByOrigin,
  });
  assert.equal(headers["Authorization"], undefined);
  assert.equal(headers["X-Relay-Password"], undefined);
});

test("agent route on a captured origin does receive the bearer", () => {
  const authByOrigin = new Map([["https://agent.dev", { token: "TOK", rp: "RP" }]]);
  const headers = applyKnownAuthHeaders({
    url: "https://agent.dev/dev/events",
    headers: {},
    authByOrigin,
  });
  assert.equal(headers["Authorization"], "Bearer TOK");
  assert.equal(headers["X-Relay-Password"], "RP");
});

test("multi-tenant relay never injects auth into non-device-scoped paths", () => {
  const authByOrigin = new Map([["https://relay.yaver.io", { token: "TOK", rp: "RP" }]]);
  for (const url of [
    "https://relay.yaver.io/health",
    "https://relay.yaver.io/voice/stream",
    "https://relay.yaver.io/p",
  ]) {
    const headers = applyKnownAuthHeaders({ url, headers: {}, authByOrigin });
    assert.equal(headers["Authorization"], undefined, url);
    assert.equal(headers["X-Relay-Password"], undefined, url);
  }
});

test("multi-tenant relay never injects auth into another tenant's device path", () => {
  const authByOrigin = new Map([[
    "https://relay.yaver.io",
    { token: "TOK", rp: "RP", deviceIds: new Set(["my-device"]) },
  ]]);
  const headers = applyKnownAuthHeaders({
    url: "https://relay.yaver.io/d/OTHER_TENANT/dev/events",
    headers: {},
    authByOrigin,
  });
  assert.equal(headers["Authorization"], undefined);
  assert.equal(headers["X-Relay-Password"], undefined);
});

test("multi-tenant relay injects auth only into the captured device's path", () => {
  const authByOrigin = new Map([[
    "https://relay.yaver.io",
    { token: "TOK", rp: "RP", deviceIds: new Set(["my-device"]) },
  ]]);
  for (const url of [
    "https://relay.yaver.io/d/my-device/dev/events",
    "https://relay.yaver.io/proxy/my-device/health",
  ]) {
    const headers = applyKnownAuthHeaders({ url, headers: {}, authByOrigin });
    assert.equal(headers["Authorization"], "Bearer TOK", url);
    assert.equal(headers["X-Relay-Password"], "RP", url);
  }
  assert.ok(TRUSTED_MULTI_TENANT_ORIGINS.has("https://relay.yaver.io"));
  assert.ok(TRUSTED_MULTI_TENANT_ORIGINS.has("https://cloud.yaver.io"));
});

test("capture on a non-agent path does not seed injection on later requests", () => {
  const authByOrigin = new Map();
  applyAuthHeaders({
    url: "https://relay.yaver.io/_next/static/x.js?token=TOK&caller=web-dashboard",
    headers: {},
    authByOrigin,
  });
  assert.equal(authByOrigin.has("https://relay.yaver.io"), false);
});

test("device-scoped capture binds the bearer to that exact device", () => {
  const authByOrigin = new Map();
  applyAuthHeaders({
    url: "https://yaver.io/d/my-device/dev/events?token=TOK&caller=web-dashboard",
    headers: {},
    authByOrigin,
  });
  const entry = authByOrigin.get("https://yaver.io");
  assert.ok(entry);
  assert.equal(entry.token, "TOK");
  assert.ok(entry.deviceIds.has("my-device"));

  // Same device stream gets the header…
  const ok = applyKnownAuthHeaders({
    url: "https://yaver.io/d/my-device/voice/stream",
    headers: {},
    authByOrigin,
  });
  assert.equal(ok["Authorization"], "Bearer TOK");
  // …another device on the same origin never does.
  const other = applyKnownAuthHeaders({
    url: "https://yaver.io/d/other-device/dev/events",
    headers: {},
    authByOrigin,
  });
  assert.equal(other["Authorization"], undefined);
});
