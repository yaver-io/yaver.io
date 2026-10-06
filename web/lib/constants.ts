/**
 * Convex site URL — the public HTTP endpoint for auth, device registry, etc.
 *
 * Defaults to the hosted Yaver Convex instance.
 * Override at build time by setting NEXT_PUBLIC_CONVEX_SITE_URL in .env or runtime env vars.
 */
import { storedPrivateVpsUrl } from "./privateVps";

export const HOSTED_CONVEX_URL =
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

/** Evaluated when the client bundle starts. Saving/clearing the local override
 * reloads the page, so every existing CONVEX_URL consumer adopts one origin
 * atomically instead of mixing hosted and private requests in the same session. */
export const CONVEX_URL = storedPrivateVpsUrl() || HOSTED_CONVEX_URL;
