/**
 * Yaver's former hosted inference gateway is intentionally retired.
 *
 * Zero-knowledge boundary: Yaver-operated Cloudflare code must never receive
 * model prompts, model responses, or user/provider credentials. Inference now
 * runs directly from an authorized endpoint (mobile/desktop/agent) to the
 * selected provider, or locally. Keep this tombstone deployed while older
 * clients age out so they fail explicitly instead of sending plaintext to an
 * abandoned or accidentally re-enabled route.
 */

export interface Env {}

const NO_STORE = {
  "cache-control": "no-store",
  "content-type": "application/json",
  "referrer-policy": "no-referrer",
} as const;

function json(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), { status, headers: NO_STORE });
}

export default {
  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);
    if (request.method === "GET" && url.pathname === "/healthz") {
      return json({ ok: true, mode: "zero-knowledge", inferenceProxy: false }, 200);
    }
    if (url.pathname === "/admin/orkey" || url.pathname.endsWith("/chat/completions")) {
      // Do not read Authorization or the request body. Older clients get a
      // stable migration signal; no user content is parsed or logged.
      return json({
        error: "endpoint_direct_inference_required",
        message: "Configure the model provider on an authorized Yaver endpoint.",
      }, 410);
    }
    return json({ error: "not_found" }, 404);
  },
};
