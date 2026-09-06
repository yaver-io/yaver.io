/**
 * rawFailure.test.ts — `npx tsx lib/rawFailure.test.ts`
 *
 * Pins the classifier that stands between the browser's raw
 * `TypeError: Failed to fetch` and the user, plus the structural guard that
 * the dashboard actually mounts a listener for it. A classifier nobody renders
 * is the same silence it was written to remove.
 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  describeRawFailure,
  isAuthShapedFailure,
  isDeliberateAbort,
  isRawNetworkFailure,
  rawFailureMessage,
  SessionDeathError,
} from "./rawFailure";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

test("every engine's bare fetch failure is recognised", () => {
  // Chrome, Safari, Firefox, React Native. All the same underlying event.
  for (const message of [
    "Failed to fetch",
    "failed to fetch",
    "Load failed",
    "NetworkError when attempting to fetch resource.",
    "Network request failed",
    "fetch failed",
  ]) {
    assert.equal(isRawNetworkFailure(new TypeError(message)), true, message);
  }
});

test("a deliberate abort is never surfaced", () => {
  const abort = new Error("The operation was aborted.");
  abort.name = "AbortError";
  assert.equal(isDeliberateAbort(abort), true);
  assert.equal(isRawNetworkFailure(abort), false);
  assert.equal(describeRawFailure(abort), null);
});

test("a self-describing application error stays silent", () => {
  // These already render at their own call site; a second banner would be noise.
  assert.equal(describeRawFailure(new Error("status 400")), null);
  assert.equal(describeRawFailure(new Error("workDir is required")), null);
  assert.equal(describeRawFailure(new Error("HTTP 500 build failed")), null);
});

test("bare fetch failure is named, never echoed", () => {
  const named = describeRawFailure(new TypeError("Failed to fetch"), {
    operation: "Save for machine",
  });
  assert.ok(named, "must produce a sentence");
  assert.equal(named.kind, "unreachable");
  // The whole point: the user never reads the raw TypeError as the headline.
  assert.ok(!/failed to fetch/i.test(named.title));
  assert.ok(!/failed to fetch/i.test(named.action));
  // It must say the write did not land — the optimistic UI already reverted,
  // and a silent revert is what made this bug unfalsifiable.
  assert.match(named.detail, /nothing was saved/i);
  // It must name the operation the user clicked.
  assert.match(named.detail, /Save for machine/);
  // It must name the most common cause AND the route out.
  assert.match(named.detail, /expired sign-in session/i);
  assert.match(named.action, /sign out and sign in/i);
  assert.equal(named.retryable, true);
  // The raw text survives for a copy-details affordance.
  assert.equal(named.raw, "Failed to fetch");
});

test("offline is called offline, not 'unreachable'", () => {
  const named = describeRawFailure(new TypeError("Failed to fetch"), {
    online: false,
    operation: "Save for machine",
  });
  assert.ok(named);
  assert.equal(named.kind, "offline");
  assert.match(named.title, /offline/i);
  assert.match(named.action, /Wi-Fi|cellular/i);
  assert.equal(named.needsSignIn, false);
});

test("an auth-shaped rejection routes to sign-in, not retry", () => {
  for (const message of ["Unauthorized", "HTTP 401", "session expired", "invalid token"]) {
    assert.equal(isAuthShapedFailure(new Error(message)), true, message);
    const named = describeRawFailure(new Error(message), { operation: "Save for machine" });
    assert.ok(named, message);
    assert.equal(named.kind, "auth");
    assert.equal(named.needsSignIn, true);
    assert.equal(named.retryable, false);
    assert.match(named.action, /sign in again/i);
  }
});

test("rawFailureMessage survives non-Error rejections", () => {
  assert.equal(rawFailureMessage("Failed to fetch"), "Failed to fetch");
  assert.equal(rawFailureMessage({ message: "Load failed" }), "Load failed");
  assert.equal(rawFailureMessage(undefined), "");
  assert.equal(rawFailureMessage(null), "");
});

// ── Structural guards ────────────────────────────────────────────────────────
// A classifier that nothing mounts is the same silence it replaced.

test("the dashboard mounts the raw-failure banner", () => {
  const page = readFileSync(join(root, "app/dashboard/page.tsx"), "utf8");
  assert.match(page, /RawFailureBanner/, "dashboard must render <RawFailureBanner />");
});

test("the banner listens for unhandled rejections AND window errors", () => {
  const banner = readFileSync(join(root, "components/dashboard/RawFailureBanner.tsx"), "utf8");
  // `void someAsyncThing()` with no catch — the shape that produced this
  // incident — only ever reaches the page as an unhandledrejection event.
  assert.match(banner, /unhandledrejection/);
  assert.match(banner, /describeRawFailure/);
});

test("POST /settings answers auth failures with a CORS-carrying 401", () => {
  // The live root cause: Convex's uncaught-exception 500 ships no
  // Access-Control-Allow-Origin, so the browser cannot read the status and
  // reports the whole thing as `TypeError: Failed to fetch`. The handler must
  // catch and answer through jsonResponse (which sets the header) instead.
  const http = readFileSync(join(root, "../backend/convex/http.ts"), "utf8");
  const start = http.indexOf('path: "/settings",\n  method: "POST"');
  assert.ok(start > 0, "POST /settings route must exist");
  const nextRoute = http.indexOf("\nhttp.route({", start + 1);
  const route = http.slice(start, nextRoute > start ? nextRoute : undefined);
  const catchAt = route.indexOf("} catch (err) {");
  assert.ok(catchAt > 0, "the runMutation call must be wrapped in try/catch");
  // The guard is the CATCH arm specifically: a thrown Unauthorized must become
  // a readable 401, not Convex's CORS-less 500 envelope. Asserting on the
  // route as a whole would pass on the pre-fix code, because the missing-header
  // early return already contained a 401.
  const arm = route.slice(catchAt, route.indexOf("return jsonResponse({ ok: true })", catchAt));
  assert.match(arm, /unauthorized/i, "the catch arm must recognise an auth failure");
  assert.match(arm, /errorResponse\([^)]*401\)/, "the catch arm must answer 401");
  assert.match(arm, /errorResponse\([^)]*500\)/, "other throws must still answer WITH CORS headers");
});

// ── Session-death names itself (incident 2026-07-28) ────────────────────────
//
// GET /settings 401'd for a stale token, refreshRelayTopology swallowed it,
// relays got no per-user password, every relay probe 401'd, and the UI blamed
// the agent. The session's death must surface as its own named banner.

test("SessionDeathError maps to the dedicated sign-in-expired banner", () => {
  const named = describeRawFailure(new SessionDeathError("GET /settings returned HTTP 401 with a token present"));
  assert.ok(named, "a session death must never be silent");
  assert.equal(named.kind, "auth");
  assert.equal(named.title, "Your sign-in has expired");
  assert.match(named.detail, /sign in again to reconnect to your machines/i);
  assert.match(named.detail, /relay password/i, "the detail must explain the cascade it prevents");
  assert.equal(named.needsSignIn, true);
  assert.equal(named.retryable, false);
});

test("refreshRelayTopology announces the session death instead of swallowing the 401", () => {
  const page = readFileSync(join(root, "app/dashboard/page.tsx"), "utf8");
  const start = page.indexOf("const refreshRelayTopology");
  assert.ok(start > 0, "refreshRelayTopology must exist");
  const fn = page.slice(start, start + 3000);
  assert.match(fn, /sr\.status === 401/, "the /settings 401 must be checked, not swallowed");
  assert.match(fn, /SessionDeathError/, "the 401 must be announced as a SessionDeathError");
  assert.match(page, /sessionDeathAnnouncedRef/, "the banner must announce once, not per reconnect rung");
});
