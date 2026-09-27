// Customer-facing product gates. Keep paid relay independent from hosted
// compute: Relay Pro is launching, Cloud Workspace is not.
export const ENABLE_RELAY_PRO_UI = true;
// Payment is a separate launch gate from the product UI. Default false means a
// deployment without a live Lemon Squeezy store shows an honest preview state
// instead of a Subscribe button that ends in HTTP 503. Enable only in the web
// build that has passed the real purchase/cancel/refund loop.
export const ENABLE_RELAY_PRO_CHECKOUT =
  process.env.NEXT_PUBLIC_YAVER_RELAY_PRO_CHECKOUT_ENABLED === "true";
export const ENABLE_CLOUD_WORKSPACE_UI = false;

// Compatibility for surfaces that only need to know whether checkout exists.
// New code should prefer the product-specific flags above.
export const HIDE_PAID_UI = !ENABLE_RELAY_PRO_UI;

export const ENABLE_TEAM_FEATURES = false;
