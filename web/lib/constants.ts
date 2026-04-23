/**
 * Convex site URL — the public HTTP endpoint for auth, device registry, etc.
 *
 * Defaults to the hosted Yaver Convex instance.
 * Override at build time by setting NEXT_PUBLIC_CONVEX_SITE_URL in .env or runtime env vars.
 */
export const CONVEX_URL =
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";
