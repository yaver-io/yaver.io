// billingWebhook.ts — PURE Lemon Squeezy webhook convergence logic.
//
// No Convex imports, no I/O: every function here is unit-testable under
// `node --experimental-strip-types` (see scripts/test-suite.sh policy-test
// list — new tests must import `./billingWebhook.ts`, NOT `./http.js`).
//
// `http.ts` wires these into POST /webhooks/lemonsqueezy. The rule they
// encode: every lifecycle event must converge the SAME local state from the
// SAME inputs; a per-branch copy of plan/period math is how drift happens
// (2026-08-09 audit G1).

export type BillingProductId = "relay-pro" | "cloud-workspace";

export function normalizeBillingProduct(value: unknown): BillingProductId | null {
  const normalized = String(value || "").trim().toLowerCase();
  if (!normalized || normalized === "relay-pro" || normalized === "relay-monthly" || normalized === "relay-yearly" || normalized === "managed-relay") {
    return "relay-pro";
  }
  if (
    normalized === "cloud-workspace" ||
    normalized === "yaver-cloud" ||
    normalized === "cloud-agent" ||
    normalized === "cpu" ||
    normalized === "gpu"
  ) {
    return "cloud-workspace";
  }
  return null;
}

/** Canonical product id from webhook custom_data (set by the server at
 *  checkout — never forgeable by the buyer). */
export function billingProductIdFromPayload(payload: any): BillingProductId {
  const rawProductType = payload?.meta?.custom_data?.product_type || "relay-pro";
  return normalizeBillingProduct(rawProductType) ?? "relay-pro";
}

/**
 * Canonical internal plan label from a subscription webhook payload. Mirrors
 * the create/update branch so paused/plan_changed/payment_success converge on
 * the same label from the same inputs.
 *
 * Order of precedence:
 *  1. custom_data.product_type (authoritative — set by the server at
 *     checkout, cannot be forged by the buyer).
 *  2. The subscription's variant_id, resolved against the configured variant
 *     envs. This is the ONLY signal that changes on `subscription_plan_changed`
 *     (custom_data was frozen at checkout), so it is what makes a portal
 *     variant swap converge (audit G1).
 *  3. variant_name heuristics (yearly detection) as the historical fallback.
 */
export function subscriptionPlanFromPayload(
  payload: any,
  data: any,
  env: Record<string, string | undefined> = process.env,
): string {
  const billingProductId = billingProductIdFromPayload(payload);
  if (billingProductId === "cloud-workspace") return "cloud-workspace";
  const ls = (suffix: string): string | undefined =>
    env["LEMONSQUEEZY_" + suffix] ?? env["LEMON_SQUEEZY_" + suffix];
  const variantId = String(data?.variant_id ?? "").trim();
  if (variantId) {
    const cloudVariants = [
      ls("YAVER_CLOUD_WORKSPACE_VARIANT_ID"),
      ls("YAVER_CLOUD_BYOK_VARIANT_ID"),
      ls("YAVER_CLOUD_VARIANT_ID"),
      ls("YAVER_CLOUD_HOSTED_VARIANT_ID"),
    ]
      .filter((v): v is string => Boolean(v))
      .map(String);
    if (cloudVariants.includes(variantId)) return "cloud-workspace";
    const relayVariants = [
      ls("YAVER_RELAY_PRO_VARIANT_ID"),
      ls("MANAGED_RELAY_VARIANT_ID"),
      ls("YAVER_RELAY_VARIANT_ID"),
    ]
      .filter((v): v is string => Boolean(v))
      .map(String);
    if (relayVariants.includes(variantId)) return "relay-pro";
  }
  // Case-insensitive: LemonSqueezy variant names are human-facing
  // ("Relay Pro Yearly"). The old `.includes("yearly")` was case-sensitive,
  // so a capitalized "Yearly" never mapped to relay-yearly — caught by
  // billingWebhook.test.mts (2026-08-09).
  return String(data?.variant_name || "").toLowerCase().includes("yearly")
    ? "relay-yearly"
    : "relay-pro";
}

/**
 * Canonical period-end timestamp from a subscription webhook payload.
 * LemonSqueezy omits renews_at/ends_at on paused subs; a 0/NaN period end
 * would poison the billing page and the expiry sweep, so fall back to now
 * (the row's `status` is the real signal; period end only matters for rows
 * that are active or cancelling).
 */
export function subscriptionPeriodEnd(data: any, now: number = Date.now()): number {
  const raw = data?.renews_at || data?.ends_at;
  const t = raw ? new Date(raw).getTime() : NaN;
  return Number.isFinite(t) && t > 0 ? t : now;
}

/**
 * Truthful billing-state flags for /billing/status (audit G3): a
 * subscription is shown as subscribed while active or during a paid
 * cancel-at-period-end grace window. past_due/unpaid/payment_failed surface as
 * a named paymentProblem instead of a green subscribed state.
 */
export function billingStateFlags(status: unknown, currentPeriodEnd?: number | null, now: number = Date.now()): {
  subscribed: boolean;
  paymentProblem: boolean;
} {
  const s = String(status || "").trim().toLowerCase();
  const paymentProblem = ["past_due", "unpaid", "payment_failed"].includes(s);
  const cancellingWithPaidTime = s === "cancelled" && Number(currentPeriodEnd || 0) > now;
  return { subscribed: s === "active" || cancellingWithPaidTime, paymentProblem };
}

/** Product access follows Lemon Squeezy subscription validity, which is
 * broader than a healthy billing badge. Trial, paused, and dunning states keep
 * access until Lemon Squeezy marks the subscription expired; a cancellation
 * keeps access only through ends_at. Refund handling revokes separately. */
export function subscriptionHasServiceAccess(
  status: unknown,
  currentPeriodEnd?: number | null,
  now: number = Date.now(),
): boolean {
  const value = String(status || "").trim().toLowerCase();
  if (["on_trial", "active", "paused", "past_due", "unpaid", "payment_failed"].includes(value)) return true;
  return value === "cancelled" && Number(currentPeriodEnd || 0) > now;
}

/** Subscription statuses that are "paid, but not healthy" (payment problem). */
export const PAYMENT_PROBLEM_STATUSES = ["past_due", "unpaid", "payment_failed"] as const;

/**
 * Full vs partial refund classification for `order_refunded`. Explicit rule
 * (plan §5 REFUNDED): only a FULL refund ("refunded" order status or
 * refunded===true) revokes service. Partial refunds preserve service because
 * the user has paid for the period; they are logged for ops, never guessed.
 */
export function isFullyRefundedOrder(data: any): boolean {
  const orderStatus = String(data?.status || "").trim().toLowerCase();
  return orderStatus === "refunded" || data?.refunded === true;
}
