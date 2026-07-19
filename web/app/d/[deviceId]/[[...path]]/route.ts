import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { CONVEX_URL } from "@/lib/constants";

const FORWARDED_REQ_HEADERS = [
  "accept",
  "accept-encoding",
  "accept-language",
  "cache-control",
  "content-type",
  "if-modified-since",
  "if-none-match",
  "origin",
  "pragma",
  "range",
  "referer",
  "user-agent",
] as const;

const BLOCKED_RESP_HEADERS = new Set([
  "connection",
  "content-length",
  "content-security-policy",
  "content-security-policy-report-only",
  "host",
  "set-cookie",
  "transfer-encoding",
]);

type RelayServer = {
  httpUrl?: string;
  password?: string;
};

async function readAuthToken(request: NextRequest): Promise<string | null> {
  const auth = request.headers.get("authorization");
  if (auth?.startsWith("Bearer ")) return auth.slice(7).trim();
  const store = await cookies();
  return store.get("yaver_auth_token")?.value || store.get("yaver_session")?.value || null;
}

async function convexJson(path: string, token: string, init?: RequestInit) {
  const res = await fetch(`${CONVEX_URL}${path}`, {
    ...init,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(init?.headers || {}),
    },
    cache: "no-store",
  });
  const text = await res.text();
  let json: any = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = null;
  }
  return { res, json, text };
}

async function loadRelayTarget(token: string) {
  const [{ res: configRes, json: configJson }, { res: settingsRes, json: settingsJson }] = await Promise.all([
    convexJson("/config", token),
    convexJson("/settings", token),
  ]);

  if (!configRes.ok) {
    throw new Error(configJson?.error || `Failed to load relay config: HTTP ${configRes.status}`);
  }
  if (!settingsRes.ok) {
    throw new Error(settingsJson?.error || `Failed to load settings: HTTP ${settingsRes.status}`);
  }

  const relays: RelayServer[] = Array.isArray(configJson?.relayServers) ? configJson.relayServers : [];
  const relay = relays[0];
  const password = settingsJson?.settings?.relayPassword || settingsJson?.relayPassword || relay?.password;
  if (!relay?.httpUrl || !password) {
    throw new Error("Relay preview is not configured for this account");
  }
  return { relayUrl: String(relay.httpUrl).replace(/\/+$/, ""), password: String(password) };
}

async function repairRelayPassword(token: string): Promise<void> {
  await convexJson("/settings/repair-relay", token, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
}

async function proxyRelay(
  request: NextRequest,
  relayUrl: string,
  relayPassword: string,
  deviceId: string,
  restPath: string,
) {
  const target = new URL(`${relayUrl}/d/${encodeURIComponent(deviceId)}/${restPath}`);
  target.search = request.nextUrl.search;

  const headers = new Headers();
  for (const name of FORWARDED_REQ_HEADERS) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  headers.set("X-Relay-Password", relayPassword);

  return fetch(target, {
    method: request.method,
    headers,
    body: request.method === "GET" || request.method === "HEAD" ? undefined : request.body,
    redirect: "manual",
    cache: "no-store",
  });
}

// Tiny script we inject into proxied HTML so client-side routers
// (expo-router, react-router, next/link, etc.) read pathname "/" or
// the actual app path — not "/d/<deviceId>/dev/...". Without this
// strip the iframe shows expo-router's "Unmatched Route" 404 because
// it tries to render /d/<deviceId>/dev/ as an in-app route.
//
// Runs synchronously, before the body parses, so framework bootstrap
// sees the rewritten URL on first read. URL bar still shows the
// proxy URL — only document state is rewritten.
const PATH_REBASE_SCRIPT = `<script>(function(){try{var p=location.pathname;var m=p.match(/^\\/d\\/[^/]+\\/dev(\\/.*)?$/);if(m){var rest=m[1]||'/';history.replaceState(null,'',rest+location.search+location.hash);}}catch(e){}})();</script>`;

function rewritePreviewBody(body: string, contentType: string, deviceId: string): string {
  const dPrefix = `/d/${encodeURIComponent(deviceId)}`;
  // Path-aware prefix: if the URL's path already starts with `dev/`
  // (the agent's static-bundle case, e.g. `<base href="/dev/web-bundle/">`),
  // we only need to prepend `/d/<id>/`. Otherwise — the legacy
  // live-Metro case where index.html has root-absolute paths like
  // `src="/foo.js"` — we prepend `/d/<id>/dev/`. Without this
  // discrimination the static-bundle case ends up with a doubled
  // `/dev/dev/...` prefix that hits the agent's `/dev/` reverse-proxy
  // catchall and returns 503.
  const rewritePath = (path: string) =>
    path.startsWith("dev/") || path === "dev"
      ? `${dPrefix}/${path}`
      : `${dPrefix}/dev/${path}`;
  if (/text\/html/i.test(contentType)) {
    let out = body.replace(
      /\b(src|href|action)=([\"'])\/(?!\/)([^\"']*)\2/gi,
      (_match, attr, quote, path) => `${attr}=${quote}${rewritePath(path)}${quote}`,
    );
    // Inject the path-rebase script right after <head ...> so it
    // executes before any framework bootstrap that reads
    // window.location.pathname. Falls back to prepending if there's
    // no <head> tag in the response.
    if (/<head[^>]*>/i.test(out)) {
      out = out.replace(/<head([^>]*)>/i, (_m, attrs) => `<head${attrs}>${PATH_REBASE_SCRIPT}`);
    } else {
      out = PATH_REBASE_SCRIPT + out;
    }
    return out;
  }
  if (/text\/css/i.test(contentType)) {
    return body.replace(/url\((['"]?)\/(?!\/)([^)'"]*)\1\)/gi, (_match, quote, path) => {
      return `url(${quote}${rewritePath(path)}${quote})`;
    });
  }
  return body;
}

async function handle(request: NextRequest, context: { params: Promise<{ deviceId: string; path?: string[] }> }) {
  const token = await readAuthToken(request);
  if (!token) {
    return NextResponse.json({ ok: false, error: "missing auth token" }, { status: 401 });
  }

  const { deviceId, path = [] } = await context.params;
  if (!deviceId) {
    return NextResponse.json({ ok: false, error: "missing device id" }, { status: 400 });
  }
  const restPath = path.join("/");

  let target = await loadRelayTarget(token);
  let response = await proxyRelay(request, target.relayUrl, target.password, deviceId, restPath);

  if (response.status === 401) {
    const body = await response.clone().text();
    // Self-heal a missing/rotated password too, not just an invalid one. The
    // relay says "relay password missing — sign in again to fetch it" for a
    // fresh/rotated user with no password — the case that most needs re-pulling
    // creds — so match missing|invalid|rejected|denied. Mirrors the agent's
    // staleRelayPasswordHTTP.
    if (/relay password (missing|invalid|rejected|denied)/i.test(body)) {
      await repairRelayPassword(token);
      target = await loadRelayTarget(token);
      response = await proxyRelay(request, target.relayUrl, target.password, deviceId, restPath);
    }
  }

  const headers = new Headers();
  response.headers.forEach((value, key) => {
    if (!BLOCKED_RESP_HEADERS.has(key.toLowerCase())) {
      headers.set(key, value);
    }
  });
  headers.set("x-yaver-preview-proxy", "1");

  const contentType = response.headers.get("content-type") || "";
  if (/text\/html|text\/css/i.test(contentType)) {
    const body = rewritePreviewBody(await response.text(), contentType, deviceId);
    return new NextResponse(body, {
      status: response.status,
      headers,
    });
  }

  return new NextResponse(response.body, {
    status: response.status,
    headers,
  });
}

export async function GET(request: NextRequest, context: { params: Promise<{ deviceId: string; path?: string[] }> }) {
  return handle(request, context);
}

export async function HEAD(request: NextRequest, context: { params: Promise<{ deviceId: string; path?: string[] }> }) {
  return handle(request, context);
}

export async function POST(request: NextRequest, context: { params: Promise<{ deviceId: string; path?: string[] }> }) {
  return handle(request, context);
}

export async function PUT(request: NextRequest, context: { params: Promise<{ deviceId: string; path?: string[] }> }) {
  return handle(request, context);
}

export async function DELETE(request: NextRequest, context: { params: Promise<{ deviceId: string; path?: string[] }> }) {
  return handle(request, context);
}

export async function OPTIONS(request: NextRequest, context: { params: Promise<{ deviceId: string; path?: string[] }> }) {
  return handle(request, context);
}
