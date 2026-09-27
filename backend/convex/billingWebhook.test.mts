import test from "node:test";
import assert from "node:assert/strict";

// Real-path import per scripts/test-suite.sh policy-test rule — this module
// must stay pure (no Convex imports) so node --experimental-strip-types can
// execute it. Do NOT import from "./http.js" — that never worked.
import {
  billingProductIdFromPayload,
  billingStateFlags,
  subscriptionHasServiceAccess,
  isFullyRefundedOrder,
  normalizeBillingProduct,
  PAYMENT_PROBLEM_STATUSES,
  subscriptionPeriodEnd,
  subscriptionPlanFromPayload,
} from "./billingWebhook.ts";

// ── normalizeBillingProduct ────────────────────────────────────────────────

test("normalizeBillingProduct maps legacy names to the two-product catalog", () => {
  assert.equal(normalizeBillingProduct("relay-pro"), "relay-pro");
  assert.equal(normalizeBillingProduct("relay-monthly"), "relay-pro");
  assert.equal(normalizeBillingProduct("relay-yearly"), "relay-pro");
  assert.equal(normalizeBillingProduct("managed-relay"), "relay-pro");
  assert.equal(normalizeBillingProduct("cloud-workspace"), "cloud-workspace");
  assert.equal(normalizeBillingProduct("yaver-cloud"), "cloud-workspace");
  assert.equal(normalizeBillingProduct("cloud-agent"), "cloud-workspace");
  assert.equal(normalizeBillingProduct("cpu"), "cloud-workspace");
  assert.equal(normalizeBillingProduct("gpu"), "cloud-workspace");
});

test("normalizeBillingProduct defaults empty to relay-pro and rejects unrecognized values", () => {
  // Empty/null/undefined default to relay-pro — NEVER to the expensive
  // cloud product (absent custom_data must not mint cloud entitlements).
  assert.equal(normalizeBillingProduct(""), "relay-pro");
  assert.equal(normalizeBillingProduct(null), "relay-pro");
  assert.equal(normalizeBillingProduct(undefined), "relay-pro");
  // Unrecognized non-empty values fail closed.
  assert.equal(normalizeBillingProduct("enterprise"), null);
  assert.equal(normalizeBillingProduct("bitbucket"), null);
});

// ── billingProductIdFromPayload ────────────────────────────────────────────

test("billingProductIdFromPayload reads checkout custom_data, defaults to relay-pro", () => {
  assert.equal(
    billingProductIdFromPayload({ meta: { custom_data: { product_type: "cloud-workspace" } } }),
    "cloud-workspace",
  );
  assert.equal(
    billingProductIdFromPayload({ meta: { custom_data: { product_type: "relay-pro" } } }),
    "relay-pro",
  );
  // Absent custom_data must NEVER default to the expensive product.
  assert.equal(billingProductIdFromPayload({}), "relay-pro");
  assert.equal(billingProductIdFromPayload(undefined), "relay-pro");
});

// ── subscriptionPlanFromPayload ────────────────────────────────────────────

const CLOUD_VARIANT = "123456";
const RELAY_VARIANT = "654321";

test("subscriptionPlanFromPayload honors checkout product_type first", () => {
  const payload = { meta: { custom_data: { product_type: "cloud-workspace" } } };
  assert.equal(subscriptionPlanFromPayload(payload, { variant_name: "Relay Pro" }), "cloud-workspace");
});

test("subscriptionPlanFromPayload detects yearly relay via variant_name", () => {
  const payload = { meta: { custom_data: { product_type: "relay-pro" } } };
  assert.equal(subscriptionPlanFromPayload(payload, { variant_name: "Relay Pro Yearly" }), "relay-yearly");
  assert.equal(subscriptionPlanFromPayload(payload, { variant_name: "Relay Pro" }), "relay-pro");
});

test("subscriptionPlanFromPayload converges portal variant swaps via variant_id (audit G1)", () => {
  const env = {
    LEMONSQUEEZY_YAVER_CLOUD_WORKSPACE_VARIANT_ID: CLOUD_VARIANT,
    LEMONSQUEEZY_YAVER_RELAY_PRO_VARIANT_ID: RELAY_VARIANT,
  };
  // custom_data was frozen at checkout as relay-pro; the user then changed
  // plan in the LS portal → the subscription's variant_id is the only signal.
  const relayPayload = { meta: { custom_data: { product_type: "relay-pro" } } };
  assert.equal(
    subscriptionPlanFromPayload(relayPayload, { variant_id: CLOUD_VARIANT, variant_name: "Relay Pro" }, env),
    "cloud-workspace",
  );
  assert.equal(
    subscriptionPlanFromPayload(relayPayload, { variant_id: RELAY_VARIANT, variant_name: "Relay Pro" }, env),
    "relay-pro",
  );
});

test("subscriptionPlanFromPayload falls back to variant_name for unknown variant ids", () => {
  const env = { LEMONSQUEEZY_YAVER_RELAY_PRO_VARIANT_ID: RELAY_VARIANT };
  const payload = { meta: { custom_data: { product_type: "relay-pro" } } };
  assert.equal(
    subscriptionPlanFromPayload(payload, { variant_id: "999999", variant_name: "Relay Pro Yearly" }, env),
    "relay-yearly",
  );
  assert.equal(
    subscriptionPlanFromPayload(payload, { variant_id: "999999", variant_name: "Relay Pro" }, env),
    "relay-pro",
  );
});

// ── subscriptionPeriodEnd ──────────────────────────────────────────────────

test("subscriptionPeriodEnd parses renews_at and ends_at", () => {
  const t = Date.UTC(2026, 8, 1); // 2026-09-01
  assert.equal(subscriptionPeriodEnd({ renews_at: new Date(t).toISOString() }, 0), t);
  assert.equal(subscriptionPeriodEnd({ ends_at: new Date(t).toISOString() }, 0), t);
  // renews_at wins when both present
  const later = Date.UTC(2026, 9, 1);
  assert.equal(
    subscriptionPeriodEnd({ renews_at: new Date(t).toISOString(), ends_at: new Date(later).toISOString() }, 0),
    t,
  );
});

test("subscriptionPeriodEnd falls back to now for paused subs (no renews_at/ends_at)", () => {
  const now = 1234567890;
  assert.equal(subscriptionPeriodEnd({}, now), now);
  assert.equal(subscriptionPeriodEnd({ renews_at: null, ends_at: null }, now), now);
  assert.equal(subscriptionPeriodEnd({ renews_at: "not-a-date" }, now), now);
});

// ── billingStateFlags (audit G3 — past_due must not read as subscribed) ───

test("billingStateFlags: only active counts as subscribed", () => {
  assert.deepEqual(billingStateFlags("active"), { subscribed: true, paymentProblem: false });
});

test("billingStateFlags: cancellation preserves access only through the paid period", () => {
  const now = 1_700_000_000_000;
  assert.deepEqual(billingStateFlags("cancelled", now + 60_000, now), { subscribed: true, paymentProblem: false });
  assert.deepEqual(billingStateFlags("cancelled", now, now), { subscribed: false, paymentProblem: false });
});

test("service access follows provider validity through trial, pause, dunning, and paid cancellation grace", () => {
  const now = 1_700_000_000_000;
  for (const status of ["on_trial", "active", "paused", "past_due", "unpaid", "payment_failed"]) {
    assert.equal(subscriptionHasServiceAccess(status, null, now), true, status);
  }
  assert.equal(subscriptionHasServiceAccess("cancelled", now + 1, now), true);
  for (const status of ["cancelled", "expired", "refunded", "unknown", null]) {
    assert.equal(subscriptionHasServiceAccess(status, now, now), false, String(status));
  }
});

test("billingStateFlags: past_due / unpaid / payment_failed surface a payment problem", () => {
  for (const status of PAYMENT_PROBLEM_STATUSES) {
    assert.deepEqual(billingStateFlags(status), { subscribed: false, paymentProblem: true }, status);
  }
});

test("billingStateFlags: paused/cancelled/expired/refunded are neither", () => {
  for (const status of ["paused", "cancelled", "expired", "refunded", "on_trial", null, undefined, ""]) {
    assert.deepEqual(billingStateFlags(status), { subscribed: false, paymentProblem: false }, String(status));
  }
});

// ── isFullyRefundedOrder ───────────────────────────────────────────────────

test("isFullyRefundedOrder: full refunds revoke service", () => {
  assert.equal(isFullyRefundedOrder({ status: "refunded" }), true);
  assert.equal(isFullyRefundedOrder({ status: "REFUNDED" }), true);
  assert.equal(isFullyRefundedOrder({ status: "paid", refunded: true }), true);
});

test("isFullyRefundedOrder: partial/other refunds preserve service (explicit rule)", () => {
  assert.equal(isFullyRefundedOrder({ status: "partially_refunded" }), false);
  assert.equal(isFullyRefundedOrder({ status: "paid" }), false);
  assert.equal(isFullyRefundedOrder({ status: "failed" }), false);
  assert.equal(isFullyRefundedOrder({}), false);
  assert.equal(isFullyRefundedOrder(undefined), false);
});
