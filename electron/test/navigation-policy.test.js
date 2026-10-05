"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const { isAllowedAppPath, isAllowedAppUrl, inPageNavigationDecision } = require("../src/navigation-policy");

test("allowed app paths", () => {
  assert.ok(isAllowedAppPath("/auth"));
  assert.ok(isAllowedAppPath("/auth/callback"));
  assert.ok(isAllowedAppPath("/auth/totp?return=/dashboard"));
  assert.ok(isAllowedAppPath("/auth"));
  assert.ok(isAllowedAppPath("/auth/reset-password"));
  assert.ok(isAllowedAppPath("/auth/verify-email"));
  assert.ok(isAllowedAppPath("/api/auth/oauth/google/callback"));
  assert.ok(isAllowedAppPath("/dashboard"));
  assert.ok(isAllowedAppPath("/dashboard/home"));
  assert.ok(isAllowedAppPath("/studio"));
  assert.ok(isAllowedAppPath("/workspace"));
  assert.ok(isAllowedAppPath("/d/abc123/dev/events"));
  assert.ok(isAllowedAppPath("/_next/static/chunks/x.js"));
});

test("blocked web-app surfaces (marketing/docs/blog/etc.)", () => {
  for (const p of ["/", "/docs", "/blog", "/pricing", "/download", "/faq",
    "/support", "/manuals", "/licensing", "/games", "/spatial", "/apps",
    "/integrations", "/launch", "/badge", "/survey", "/account", "/dev",
    "/admin", "/pair", "/watch", "/render", "/artifacts", "/a", "/j"]) {
    assert.equal(isAllowedAppPath(p), false, `${p} should be blocked`);
  }
});

test("prefix must not over-match (/dashboardx, /api2)", () => {
  assert.equal(isAllowedAppPath("/dashboardx"), false);
  assert.equal(isAllowedAppPath("/apix"), false);
  assert.equal(isAllowedAppPath("/authx"), false);
  assert.equal(isAllowedAppPath("/d"), false); // bare /d without device id
});

test("allowed URLs by origin", () => {
  assert.ok(isAllowedAppUrl("https://yaver.io/dashboard?tab=chat"));
  assert.ok(isAllowedAppUrl("https://yaver.io/auth?return=/dashboard"));
  assert.ok(isAllowedAppUrl("https://yaver.io/auth?mode=signup&return=/dashboard"));
  assert.ok(isAllowedAppUrl("https://yaver.io/d/xyz/dev/events"));
  assert.ok(isAllowedAppUrl("http://localhost:3000/dashboard"));
  assert.ok(isAllowedAppUrl("https://yaver.io/studio"));
  assert.ok(isAllowedAppUrl("http://localhost:3000/api/auth/oauth/github/callback"));
  // Auth-provider redirects (server-side OAuth) may render in-window.
  assert.ok(isAllowedAppUrl("https://accounts.google.com/o/oauth2/v2/auth?x=1"));
  assert.ok(isAllowedAppUrl("https://appleid.apple.com/auth/authorize"));
  assert.ok(isAllowedAppUrl("https://github.com/login/oauth/authorize"));
  assert.ok(isAllowedAppUrl("https://gitlab.com/oauth/authorize"));
  assert.ok(isAllowedAppUrl("https://login.microsoftonline.com/common/oauth2/v2.0/authorize"));
  assert.ok(isAllowedAppUrl("https://login.live.com/oauth20_authorize.srf"));
  assert.ok(isAllowedAppUrl("https://perceptive-minnow-557.eu-west-1.convex.site/oauth/authorize"));
});

test("blocked URLs by origin/path", () => {
  assert.equal(isAllowedAppUrl("https://yaver.io/"), false);
  assert.equal(isAllowedAppUrl("https://yaver.io/pricing"), false);
  assert.equal(isAllowedAppUrl("https://yaver.io/docs"), false);
  assert.equal(isAllowedAppUrl("https://yaver.io/blog"), false);
  assert.equal(isAllowedAppUrl("https://www.yaver.io/"), false);
  assert.equal(isAllowedAppUrl("https://evil.example.com/"), false);
  assert.equal(isAllowedAppUrl("https://relay.yaver.io/anything/else"), false); // non-app path on app origin
  assert.equal(isAllowedAppUrl("not a url"), false);
});

// In-page (SPA pushState) navigations bypass will-navigate, so main.js routes
// them through inPageNavigationDecision. This pins the behavior that used to
// silently no-op: main.js called isAllowedAppPath without importing it, the
// ReferenceError was swallowed, and a click on /docs or /pricing rendered the
// marketing page inside the GUI window. The decision must come from the pure
// module so it cannot regress to a swallowed throw.
test("in-page soft-navigation decision bounces marketing paths to the auth gate", () => {
  // App paths stay in-window.
  assert.deepEqual(inPageNavigationDecision("https://yaver.io/dashboard?tab=chat"), { allow: true, bounce: null });
  assert.deepEqual(inPageNavigationDecision("https://yaver.io/auth?return=/dashboard"), { allow: true, bounce: null });
  assert.deepEqual(inPageNavigationDecision("http://localhost:3000/api/auth/oauth/github/callback"), { allow: true, bounce: null });
  // Marketing/docs/blog paths on an app origin bounce to the auth gate.
  assert.deepEqual(inPageNavigationDecision("https://yaver.io/docs"), { allow: false, bounce: "https://yaver.io/auth?return=/dashboard" });
  assert.deepEqual(inPageNavigationDecision("https://yaver.io/pricing"), { allow: false, bounce: "https://yaver.io/auth?return=/dashboard" });
  assert.deepEqual(inPageNavigationDecision("https://yaver.io/blog"), { allow: false, bounce: "https://yaver.io/auth?return=/dashboard" });
  assert.deepEqual(inPageNavigationDecision("https://yaver.io/download"), { allow: false, bounce: "https://yaver.io/auth?return=/dashboard" });
  // Same-origin auth bounce (dev server is localhost, not production).
  assert.deepEqual(inPageNavigationDecision("http://localhost:3000/docs"), { allow: false, bounce: "http://localhost:3000/auth?return=/dashboard" });
  // Foreign origins: bounce is the caller's job (system browser).
  assert.deepEqual(inPageNavigationDecision("https://evil.example.com/x"), { allow: false, bounce: null });
  assert.deepEqual(inPageNavigationDecision("not a url"), { allow: false, bounce: null });
});
