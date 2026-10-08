import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { YAVER_EDGE_URL } from "@/lib/constants";
import { previewDeviceProxyHeaders } from "@/lib/preview-device-proxy";
import { injectPreviewPathRebase } from "@/lib/previewRebase";

const BLOCKED_RESP_HEADERS = new Set([
  "connection",
  "content-length",
  "content-security-policy",
  "content-security-policy-report-only",
  "content-encoding",
  "host",
  "set-cookie",
  "transfer-encoding",
]);

// Answers CORS preflights locally — an OPTIONS request carries no cookies or
// Authorization, so proxying it upstream 401s and kills every cross-origin
// browser-lane call. SECURITY: this echoes any Origin but deliberately never
// sets Access-Control-Allow-Credentials; adding it would turn the echo into a
// CSRF hole against the cookie fallback in readAuthToken. Never add it.
function preflightResponse(request: NextRequest) {
  const headers = new Headers();
  const origin = request.headers.get("origin");
  if (origin) headers.set("Access-Control-Allow-Origin", origin);
  headers.set("Vary", "Origin");
  headers.set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, DELETE, OPTIONS");
  headers.set(
    "Access-Control-Allow-Headers",
    request.headers.get("access-control-request-headers") ||
      "Authorization, Content-Type, X-Relay-Password, X-Client-Platform",
  );
  headers.set("Access-Control-Max-Age", "600");
  return new NextResponse(null, { status: 204, headers });
}

async function readAuthToken(request: NextRequest): Promise<string | null> {
  const auth = request.headers.get("authorization");
  if (auth?.startsWith("Bearer ")) return auth.slice(7).trim();
  const store = await cookies();
  return store.get("yaver_auth_token")?.value || store.get("yaver_session")?.value || null;
}

function loadRelayTarget() {
  return { relayUrl: YAVER_EDGE_URL, password: "" };
}

async function proxyRelay(
  request: NextRequest,
  relayUrl: string,
  relayPassword: string,
  authToken: string,
  deviceId: string,
  restPath: string,
) {
  const target = new URL(`${relayUrl}/d/${encodeURIComponent(deviceId)}/${restPath}`);
  target.search = request.nextUrl.search;

  const headers = previewDeviceProxyHeaders(request.headers, authToken, relayPassword);

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
    out = out.replace(
      /\b(src|href)=([\"'])(?![a-z][a-z0-9+.-]*:|#|\/)([^\"']*)\2/gi,
      (match, attr, quote, path) => {
        if (!/^(?:_next\/|favicon\.ico|manifest\.webmanifest|icon-\d+\.png|apple-touch-icon\.png)/i.test(path)) {
          return match;
        }
        return `${attr}=${quote}${rewritePath(path)}${quote}`;
      },
    );
    out = out
      .replace(/([\"'])\/_next\//g, (_match, quote) => `${quote}${dPrefix}/dev/_next/`)
      .replace(/\\\/_next\\\//g, `${dPrefix.replace(/\//g, "\\/")}\\/dev\\/_next\\/`);
    // Current agents already inject a transport shim that MUST capture the
    // scoped /d/<device>/dev path before their router makes "/" visible to the
    // guest. Injecting this outer history rewrite ahead of that shim made Metro
    // lazy imports (for example /src/lib/auth.bundle) escape to relay root and
    // 404. Keep this fallback only for legacy agents that do not own rebasing.
    out = injectPreviewPathRebase(out, PATH_REBASE_SCRIPT);
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

  const target = loadRelayTarget();
  const response = await proxyRelay(request, target.relayUrl, target.password, token, deviceId, restPath);

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
  return preflightResponse(request);
}
