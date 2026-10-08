const JSON_HEADERS = {
  "cache-control": "no-store",
  "content-type": "application/json; charset=utf-8",
  "referrer-policy": "no-referrer",
  "x-content-type-options": "nosniff",
} as const;

export function json(body: unknown, status = 200, extra?: HeadersInit): Response {
  const headers = new Headers(JSON_HEADERS);
  if (extra) new Headers(extra).forEach((value, key) => headers.set(key, value));
  return new Response(JSON.stringify(body), { status, headers });
}

export function error(message: string, status: number, code?: string): Response {
  return json({ error: message, ...(code ? { code } : {}) }, status);
}

export function cors(request: Request, response: Response): Response {
  // A Cloudflare WebSocket upgrade carries a non-standard `webSocket` handle
  // on the Response object. Reconstructing a 101 response drops that handle
  // and turns a valid Durable Object upgrade into a 500. WebSocket origin
  // policy is enforced before the upgrade; return the object bit-for-bit.
  if (response.status === 101) return response;
  const origin = request.headers.get("origin");
  if (!origin) return response;
  const headers = new Headers(response.headers);
  headers.set("access-control-allow-origin", origin);
  headers.set("access-control-allow-credentials", "true");
  headers.set("vary", "Origin");
  return new Response(response.body, { status: response.status, headers });
}
