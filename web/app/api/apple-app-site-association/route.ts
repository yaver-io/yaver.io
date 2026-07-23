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
//
// INCIDENT (2026-07-23) — passkey / Face ID sign-in failed IN THE APP while
// web passkey + web Apple sign-in kept working, same account. Two coupled
// bugs, both from 3620ec238 ("phone-assisted cold-start install"):
//   1. A static /public/.well-known/apple-app-site-association was added with
//      `webcredentials` NESTED INSIDE `applinks`. Apple reads `webcredentials`
//      ONLY as a top-level key; nested, iOS finds no credential association for
//      yaver.io, so the app can't use passkeys registered for the site — the
//      Face ID sheet dismisses with "no credentials". Web is unaffected because
//      Safari uses the WebAuthn RP (yaver.io) directly, not the app↔site AASA.
//   2. That static file SHADOWED this route (CF serves the physical asset before
//      the Next rewrite runs), so even the correct structure here never reached
//      users — AND it was served without the application/json content-type, the
//      exact failure this route exists to prevent.
// Fix: this route is the SINGLE source of the AASA (webcredentials top-level),
// and the shadowing static file is deleted. Do NOT re-add a physical file at
// /public/.well-known/apple-app-site-association — it silently wins over this
// handler. `webcredentials` MUST stay a sibling of `applinks`, never nested.

const aasa = {
  applinks: {
    // Scope universal links to the pairing / device-code deep links only.
    // A blanket "*" would make every yaver.io link (blog, docs, dashboard)
    // try to open the app on iOS.
    details: [
      {
        appIDs: ["5SJZ4KA39A.io.yaver.mobile"],
        components: [
          { "/": "/pair*", comment: "Pairing deep links only" },
          {
            "/": "/auth/device*",
            comment:
              "Remote-box device-code approve — opens the in-app one-tap approver when the phone has the app; web page otherwise.",
          },
        ],
      },
    ],
  },
  // TOP-LEVEL, sibling of applinks — this is what makes in-app passkey / Face ID
  // sign-in work. Never move it inside applinks (see incident note above).
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
