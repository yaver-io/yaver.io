/**
 * Customer-facing product availability.
 *
 * Cloud Workspace internals remain only so legacy subscriptions can be
 * cancelled and resources can be stopped/decommissioned safely. New purchase,
 * activation, placement, and wake paths are fail-closed for this release.
 */
export function cloudWorkspacePublicEnabled(_env: Record<string, string | undefined> = process.env): boolean {
  // Intentionally compile-time closed for the Relay Pro launch. A stale or
  // mistyped production environment variable must not resurrect checkout,
  // placement, provisioning, wake, or owner-preview UI. Reintroducing hosted
  // compute requires a reviewed code change and its full payment/capacity arc.
  return false;
}

export const CLOUD_WORKSPACE_UNAVAILABLE =
  "Cloud Workspace is not offered. Use Relay Pro with a machine or VPS you control.";

/** Payment launch is independent from Relay Pro's real infrastructure test
 * seam. Keep checkout closed until the live Lemon Squeezy purchase, webhook,
 * cancellation, and refund loop has been proven end to end. */
export function relayProCheckoutEnabled(env: Record<string, string | undefined> = process.env): boolean {
  return String(env.YAVER_RELAY_PRO_CHECKOUT_ENABLED || "").trim().toLowerCase() === "true";
}

export const RELAY_PRO_CHECKOUT_UNAVAILABLE =
  "Relay Pro payments are not open yet. Free and self-hosted relay remain available.";
