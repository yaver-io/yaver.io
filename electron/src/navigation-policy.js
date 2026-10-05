"use strict";

/**
 * Navigation policy for the Yaver GUI — sign-in → app only.
 *
 * The GUI is NOT the marketing web app: no docs, blog, pricing, download,
 * support, or any other web surface (user directive 2026-08-12). Only the
 * auth flow and the app itself (dashboard + the /d/ agent proxy + the Convex
 * / API routes the flow needs) may render in the window.
 *
 * Pure module so the policy is unit-testable; main.js wires it to
 * will-navigate / will-redirect / did-navigate-in-page.
 */

const APP_ORIGINS = new Set([
  "https://yaver.io",
  "https://www.yaver.io",
  "https://relay.yaver.io",
  "https://cloud.yaver.io",
  "http://localhost:3000",
]);

/** OAuth provider hosts the sign-in flow redirects through in-window.
 *  These are reachable only via the auth flow's server-side redirects. */
const AUTH_PROVIDER_ORIGINS = new Set([
  "https://accounts.google.com",
  "https://appleid.apple.com",
  "https://github.com",
  "https://gitlab.com",
  "https://login.microsoftonline.com",
  "https://login.live.com",
  "https://perceptive-minnow-557.eu-west-1.convex.site", // Convex auth site
]);

/**
 * /d/ is load-bearing: the dashboard reaches agents through the same-origin
 * /d/<deviceId>/ proxy when relay-backed (web/app/d/[deviceId]/route.ts).
 */
const ALLOWED_PATH_PREFIXES = ["/auth", "/api", "/dashboard", "/studio", "/workspace", "/d/", "/_next"];

function isAllowedAppPath(pathname) {
  return ALLOWED_PATH_PREFIXES.some((prefix) => {
    if (pathname === prefix) return true;
    // "/d/" already ends in a slash — appending another would require "/d//".
    if (prefix.endsWith("/")) return pathname.startsWith(prefix);
    return pathname.startsWith(prefix + "/");
  });
}

function isAllowedAppUrl(rawUrl) {
  let u;
  try {
    u = new URL(rawUrl);
  } catch {
    return false;
  }
  if (APP_ORIGINS.has(u.origin)) return isAllowedAppPath(u.pathname);
  if (AUTH_PROVIDER_ORIGINS.has(u.origin)) return true;
  return false;
}

/**
 * Decision for SPA soft-navigations (`did-navigate-in-page`).
 *
 * Next.js App Router navigates client-side via pushState, which bypasses
 * `will-navigate`/`will-redirect`. main.js must apply the same allowlist to
 * in-page navigations, or an in-app link to /pricing or /docs renders the
 * marketing page inside the GUI window. Extracted here so the bounce is
 * unit-testable (the wiring bug that made it a silent no-op was a missing
 * `isAllowedAppPath` import in main.js).
 *
 * Returns:
 *  - { allow: true }  → the URL may render in-window
 *  - { allow: false, bounce: "<origin>/auth?return=/dashboard" }
 *                    → app-origin marketing path; bounce to the auth gate
 *  - { allow: false, bounce: null }
 *                    → foreign origin; caller decides (system browser)
 */
function inPageNavigationDecision(rawUrl) {
  let u;
  try {
    u = new URL(rawUrl);
  } catch {
    return { allow: false, bounce: null };
  }
  if (isAllowedAppUrl(rawUrl)) return { allow: true, bounce: null };
  if (APP_ORIGINS.has(u.origin)) {
    return { allow: false, bounce: `${u.origin}/auth?return=/dashboard` };
  }
  return { allow: false, bounce: null };
}

module.exports = {
  APP_ORIGINS,
  AUTH_PROVIDER_ORIGINS,
  isAllowedAppPath,
  isAllowedAppUrl,
  inPageNavigationDecision,
};
