// download.yaver.io — Cloudflare Worker fronting the yaver-apk R2 bucket.
//
// Why a Worker (not a bare R2 custom domain): R2 custom domains have no
// index-document behavior, so the bare hostname returned 404. This Worker
// serves the QR install page at "/" (the `index.html` object uploaded by
// scripts/publish-android-r2.sh) and streams every other key straight from
// R2 — APK, version.json, assetlinks, the /apk /mcp /agent setup pages.
//
// One host, all HTTPS (Cloudflare-terminated), $0 egress. This is the
// serverless analog of talos's Python apk_server on a Hetzner box.
//
// Deploy: cd download && npx wrangler deploy   (see README.md for the
// one-time R2-custom-domain → Worker-custom-domain migration.)

// Root aliases that should render the install page rather than 404.
const PAGE_ALIASES = new Set(["", "index.html", "install", "android"]);

// Minimal fallback page if the index.html object is somehow missing from the
// bucket (e.g. first deploy before publish-android-r2.sh ran). The canonical,
// full QR page lives in R2 as `index.html` (source: scripts/download-site/).
const FALLBACK_HTML = `<!doctype html><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Install Yaver for Android</title>
<body style="font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:#0f1b34;color:#e6edf8;text-align:center;padding:12vh 20px">
<h1>Install Yaver</h1>
<p><a href="/latest.apk" style="display:inline-block;background:#3f6bd0;color:#fff;padding:14px 22px;border-radius:12px;text-decoration:none;font-weight:800">Download APK</a></p>
<p style="color:#9fb2cd;font-size:13px">Android 8+. Allow &ldquo;install unknown apps&rdquo; if prompted.</p>
</body>`;

function notFound() {
  return new Response("Not found", {
    status: 404,
    headers: { "content-type": "text/plain; charset=utf-8" },
  });
}

async function serveObject(env, key, req, { asPage = false } = {}) {
  const range = req.headers.get("range");
  const ifNoneMatch = req.headers.get("if-none-match") || undefined;

  // Parse a single "bytes=start-end" range (enough for download resume).
  let r2Range;
  if (range) {
    const m = /^bytes=(\d*)-(\d*)$/.exec(range.trim());
    if (m) {
      const start = m[1] === "" ? undefined : parseInt(m[1], 10);
      const end = m[2] === "" ? undefined : parseInt(m[2], 10);
      if (start !== undefined && m[2] === "") r2Range = { offset: start };
      else if (start !== undefined) r2Range = { offset: start, length: end - start + 1 };
      else if (end !== undefined) r2Range = { suffix: end };
    }
  }

  const obj = await env.BUCKET.get(key, {
    range: r2Range,
    onlyIf: ifNoneMatch ? { etagDoesNotMatch: ifNoneMatch } : undefined,
  });

  if (obj === null) return null; // caller decides fallback
  const headers = new Headers();
  obj.writeHttpMetadata(headers); // content-type + cache-control from the stored object
  headers.set("etag", obj.httpEtag);
  headers.set("accept-ranges", "bytes");
  if (!headers.has("cache-control")) {
    headers.set("cache-control", asPage ? "public, max-age=300" : "public, max-age=60");
  }
  if (asPage) headers.set("content-type", "text/html; charset=utf-8");

  // Not-modified (body absent when the etag matched onlyIf).
  if (!("body" in obj) || obj.body === undefined) {
    return new Response(null, { status: 304, headers });
  }

  // Partial content.
  if (obj.range && obj.size !== undefined && r2Range) {
    const start = obj.range.offset ?? 0;
    const len = obj.range.length ?? obj.size - start;
    headers.set("content-range", `bytes ${start}-${start + len - 1}/${obj.size}`);
    return new Response(obj.body, { status: 206, headers });
  }

  return new Response(obj.body, { status: 200, headers });
}

export default {
  async fetch(req, env) {
    if (req.method !== "GET" && req.method !== "HEAD") {
      return new Response("Method not allowed", { status: 405, headers: { allow: "GET, HEAD" } });
    }

    const url = new URL(req.url);
    let key = decodeURIComponent(url.pathname.replace(/^\/+/, ""));

    // Install page at the root (and a few friendly aliases).
    if (PAGE_ALIASES.has(key)) {
      const res = await serveObject(env, "index.html", req, { asPage: true });
      if (res) return res;
      return new Response(FALLBACK_HTML, {
        headers: { "content-type": "text/html; charset=utf-8", "cache-control": "public, max-age=60" },
      });
    }

    const res = await serveObject(env, key, req);
    if (res) return res;

    // Convenience: /apk → the install page too.
    if (key === "apk") {
      const page = await serveObject(env, "index.html", req, { asPage: true });
      if (page) return page;
    }
    return notFound();
  },
};
