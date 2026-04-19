// Intentionally empty. Earlier iterations tried to rewrite imports
// in mobile/src/lib/*.ts so MobileClient could reuse them directly;
// Bun's runtime plugin API doesn't intercept bare specifiers
// reliably, so we now reimplement the HTTP/SSE contract in
// mobile-client.ts and use drift.test.ts to catch API divergence.
// Kept as a preload hook in case we add onLoad-based rewrites for
// future shared modules.
export {};
