// Apple App Site Association.
//
// Apple requires this file at https://yaver.io/.well-known/apple-app-site-association
// served as application/json. Cloudflare's static-assets binding
// defaults to application/octet-stream for extension-less files, so
// the static copy in /public can't satisfy Apple's content-type check.
// next.config.ts headers() doesn't reach the assets binding either.
//
// Workaround: serve from this Route Handler under /api/, then add a
// rewrite from /.well-known/apple-app-site-association → /api/...
// in next.config.ts. Apple sees the canonical URL with the right
// content type; CF still treats this as a Worker route, not an asset.

const aasa = {
  applinks: {
    apps: [],
    details: [
      {
        appID: "5SJZ4KA39A.io.yaver.mobile",
        paths: ["*"],
      },
    ],
  },
  webcredentials: {
    apps: ["5SJZ4KA39A.io.yaver.mobile"],
  },
};

export function GET(): Response {
  return new Response(JSON.stringify(aasa, null, 2), {
    status: 200,
    headers: {
      "Content-Type": "application/json",
      "Cache-Control": "public, max-age=86400",
    },
  });
}
