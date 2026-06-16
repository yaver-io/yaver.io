// gatewayGateFormat.test.mts — pure helpers for the gateway human-gate screen.
// Run: npx tsx src/lib/gatewayGateFormat.test.mts

import test from "node:test";
import assert from "node:assert/strict";

import {
  normalizeStep,
  stepLabel,
  gateRisk,
  gateRiskColor,
  needsRemoteView,
  gateSummary,
  ageLabel,
} from "./gatewayGateFormat.ts";
import type { GatewayGate } from "./gatewayGateClient.ts";

test("normalizeStep maps known + aliases + unknown", () => {
  assert.equal(normalizeStep("two_factor"), "two_factor");
  assert.equal(normalizeStep("2FA"), "two_factor");
  assert.equal(normalizeStep("otp"), "two_factor");
  assert.equal(normalizeStep("OAuth"), "login");
  assert.equal(normalizeStep("consent"), "login");
  assert.equal(normalizeStep("robot"), "captcha");
  assert.equal(normalizeStep("kyc"), "kyc_upload");
  assert.equal(normalizeStep("payment"), "payment_confirm");
  assert.equal(normalizeStep("region-confirm"), "region_confirm");
  assert.equal(normalizeStep("push"), "push_approval");
  assert.equal(normalizeStep("something-weird"), "other");
  assert.equal(normalizeStep(undefined), "other");
});

test("stepLabel covers all steps", () => {
  assert.equal(stepLabel("two_factor"), "2FA code");
  assert.equal(stepLabel("payment_confirm"), "Confirm payment");
  assert.equal(stepLabel("other"), "Action needed");
});

test("gateRisk tiers", () => {
  assert.equal(gateRisk("payment_confirm"), "high");
  assert.equal(gateRisk("kyc_upload"), "high");
  assert.equal(gateRisk("login"), "medium");
  assert.equal(gateRisk("two_factor"), "medium");
  assert.equal(gateRisk("captcha"), "medium");
  assert.equal(gateRisk("region_confirm"), "low");
  assert.equal(gateRisk("push_approval"), "low");
});

test("gateRiskColor", () => {
  assert.equal(gateRiskColor("high"), "#ef4444");
  assert.equal(gateRiskColor("medium"), "#f59e0b");
  assert.equal(gateRiskColor("low"), "#22c55e");
});

test("needsRemoteView: explicit flag wins, else step heuristic", () => {
  // explicit true / false override the step
  assert.equal(needsRemoteView({ step: "payment_confirm", interactive: true }), true);
  assert.equal(needsRemoteView({ step: "captcha", interactive: false }), false);
  // heuristic: solving-type steps need the view
  assert.equal(needsRemoteView({ step: "captcha" }), true);
  assert.equal(needsRemoteView({ step: "login" }), true);
  assert.equal(needsRemoteView({ step: "kyc_upload" }), true);
  // approve-type steps don't
  assert.equal(needsRemoteView({ step: "payment_confirm" }), false);
  assert.equal(needsRemoteView({ step: "two_factor" }), false);
  assert.equal(needsRemoteView({ step: "region_confirm" }), false);
});

test("gateSummary prefers prompt, else synthesises", () => {
  const withPrompt: GatewayGate = { id: "g1", step: "captcha", prompt: "Solve the captcha to continue" };
  assert.equal(gateSummary(withPrompt), "Solve the captcha to continue");

  const noPrompt: GatewayGate = { id: "g2", step: "payment_confirm", connector: "Stripe" };
  assert.equal(gateSummary(noPrompt), "Stripe needs confirm payment");

  const bare: GatewayGate = { id: "g3" };
  assert.equal(gateSummary(bare), "A task needs action needed");
});

test("ageLabel relative formatting", () => {
  const now = 1_000_000_000_000;
  assert.equal(ageLabel(undefined, now), "");
  assert.equal(ageLabel(now - 5_000, now), "just now");
  assert.equal(ageLabel(now - 5 * 60_000, now), "5m ago");
  assert.equal(ageLabel(now - 3 * 3_600_000, now), "3h ago");
  assert.equal(ageLabel(now - 2 * 86_400_000, now), "2d ago");
  // future timestamp clamps to "just now"
  assert.equal(ageLabel(now + 10_000, now), "just now");
});
