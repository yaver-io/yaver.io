"use strict";

/**
 * Pure URL auth-stripping + header-injection helpers for the Electron GUI.
 * Split out of main.js so the token-in-URL fix is unit-testable.
 *
 * The web dashboard passes ?token=/?__rp= on SSE URLs because EventSource
 * cannot set headers (web/lib/agent-client.ts:6135-6164). The GUI strips them
 * and re-injects as Authorization / X-Relay-Password headers — both accepted
 * by the agent's CORS allowlist (desktop/agent/httpserver.go:3231).
 *
 * Path scoping (2026-08-19, audit macos-gui-desktop-code-audit-pass-2):
 * the agent's bearer and relay password are only ever re-injected into
 * AGENT ROUTES, never into assets, the dashboard's own /api, Next.js
 * internals, or marketing paths. Device-scoped requests (/d/<deviceId> or
 * /proxy/<deviceId>) additionally require that EXACT deviceId to have been
 * captured on that origin, so one device's auth can never be attached to
 * another device's (or another tenant's) path on a shared relay.
 */

/**
 * Origins served by the shared relay are MULTI-TENANT: paths on them do not
 * belong to the signing user. Captured auth must only be re-injected into
 * device-scoped paths on these origins.
 */
const TRUSTED_MULTI_TENANT_ORIGINS = new Set([
  "https://relay.yaver.io",
  "https://cloud.yaver.io",
]);

/**
 * Paths that are never agent API routes. Captured bearer/relay material is
 * re-injected only into agent routes; Next.js internals, static assets, the
 * dashboard's own /api and marketing paths never carry the user's agent auth.
 */
const NON_AGENT_PATH_PREFIXES = [
  "/_next/", "/__nextjs", "/_error",
  "/assets/", "/images/", "/fonts/", "/favicon", "/static/",
  "/screenshots/", "/videos/",
  "/api/",
  "/auth", "/dashboard", "/studio", "/workspace", "/account", "/settings",
  "/pricing", "/blog", "/docs", "/download", "/faq", "/manuals", "/games",
  "/spatial", "/sitemap", "/robots",
];

function isAgentRoute(urlString) {
  let pathname;
  try {
    pathname = new URL(urlString).pathname;
  } catch {
    return false;
  }
  if (pathname === "" || pathname === "/") return false;
  return !NON_AGENT_PATH_PREFIXES.some(
    (prefix) => pathname === prefix || pathname.startsWith(prefix),
  );
}

/** Device-scoped relay path: /d/<deviceId> or /proxy/<deviceId>. */
function isDeviceScoped(urlString) {
  return deviceIdFromUrl(urlString) !== "";
}

/**
 * Extract the deviceId from a device-scoped path (/d/<id> or /proxy/<id>),
 * or "" when the URL is not device-scoped. Path segments are never empty or
 * dot-encoded so a crafted "/d//" or "/d/../x" cannot match a captured id.
 */
function deviceIdFromUrl(urlString) {
  let pathname;
  try {
    pathname = new URL(urlString).pathname;
  } catch {
    return "";
  }
  for (const prefix of ["/d/", "/proxy/"]) {
    if (pathname.startsWith(prefix)) {
      const rest = pathname.slice(prefix.length);
      const id = rest.split("/", 1)[0];
      if (id && id !== "." && id !== "..") return id;
    }
  }
  return "";
}

/**
 * Remove `token` and `__rp` query params from a URL.
 * Returns { url, token, rp, capture, deviceId } — token/rp are the stripped
 * secrets (or null); `capture` is true only when the secret is worth
 * remembering for reuse (i.e. the URL was an agent route, not an asset or
 * dashboard page); `deviceId` is the captured device when the URL was
 * device-scoped ("" otherwise).
 */
function stripAuthFromUrl(urlString) {
  const u = new URL(urlString);
  const rp = u.searchParams.get("__rp");
  // `token` is also a legitimate application parameter (for example
  // /account/merge?token=...). AgentClient marks its native EventSource URLs
  // with caller=web-dashboard, and Relay Pro URLs carry __rp. Never strip a
  // generic app/OAuth/reset token merely because it shares the same name.
  const isAgentAuth = Boolean(rp) || u.searchParams.get("caller") === "web-dashboard";
  const token = isAgentAuth ? u.searchParams.get("token") : null;
  if (token || rp) {
    if (token) u.searchParams.delete("token");
    if (rp) u.searchParams.delete("__rp");
    return {
      url: u.toString(),
      token,
      rp,
      capture: isAgentRoute(urlString),
      deviceId: deviceIdFromUrl(urlString),
    };
  }
  return { url: urlString, token: null, rp: null, capture: false, deviceId: "" };
}

/**
 * Build the outgoing headers for a request, applying per-origin captured
 * auth material. `capture` mutates the per-origin map when the request URL
 * carries auth params (so follow-up streams to the same agent get headers
 * even when their URL has no params).
 *
 * Returns { headers, url } — url differs from the input when auth params
 * were stripped.
 */
function applyAuthHeaders({ url: urlString, headers, authByOrigin }) {
  let url = urlString;
  let token = null;
  let rp = null;
  let capture = false;
  let deviceId = "";
  try {
    const stripped = stripAuthFromUrl(url);
    url = stripped.url;
    token = stripped.token;
    rp = stripped.rp;
    capture = stripped.capture;
    deviceId = stripped.deviceId;
  } catch {
    return { headers: { ...headers }, url: urlString };
  }

  const next = { ...headers };
  let origin = null;
  try {
    origin = new URL(url).origin;
  } catch {
    /* keep going without origin-scoped capture */
  }

  if (origin && capture) {
    const entry = authByOrigin.get(origin) || {};
    if (token) entry.token = token;
    if (rp) entry.rp = rp;
    if (deviceId) {
      if (!entry.deviceIds) entry.deviceIds = new Set();
      entry.deviceIds.add(deviceId);
    }
    authByOrigin.set(origin, entry);
  }

  return { headers: applyKnownAuthHeaders({ url, headers: next, authByOrigin }), url };

}

function hasHeader(headers, name) {
  const needle = name.toLowerCase();
  return Object.keys(headers || {}).some((key) => key.toLowerCase() === needle);
}

/** Header-only half used from Electron's onBeforeSendHeaders callback. */
function applyKnownAuthHeaders({ url, headers, authByOrigin }) {
  const next = { ...headers };
  let origin = null;
  let pathname = "";
  try {
    const parsed = new URL(url);
    origin = parsed.origin;
    pathname = parsed.pathname;
  } catch {
    return next;
  }
  const known = authByOrigin.get(origin);
  if (!known) return next;
  if (!isAgentRoute(url)) return next;

  const deviceId = deviceIdFromUrl(url);
  if (deviceId) {
    // Device-scoped request: only inject when this EXACT device was captured
    // on this origin. Prevents cross-device / cross-tenant bearer reuse.
    if (!known.deviceIds || !known.deviceIds.has(deviceId)) return next;
  } else if (TRUSTED_MULTI_TENANT_ORIGINS.has(origin)) {
    // Non-device-scoped path on a shared relay: never attach captured auth.
    return next;
  }

  if (known.token && !hasHeader(next, "Authorization")) {
    next.Authorization = `Bearer ${known.token}`;
  }
  if (known.rp && !hasHeader(next, "X-Relay-Password")) {
    next["X-Relay-Password"] = known.rp;
  }

  return next;
}

module.exports = {
  stripAuthFromUrl,
  applyAuthHeaders,
  applyKnownAuthHeaders,
  hasHeader,
  isAgentRoute,
  isDeviceScoped,
  deviceIdFromUrl,
  TRUSTED_MULTI_TENANT_ORIGINS,
};
