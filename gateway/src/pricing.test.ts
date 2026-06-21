// Pure-function tests for the gateway routing + cost math.
// Run: cd gateway && npx tsx --test src/pricing.test.ts

import test from "node:test";
import assert from "node:assert/strict";
import { ROUTES, resolveRoute, costCents } from "./pricing";

test("auto leads with a PAY-PER-TOKEN provider, not the flat coding plan", () => {
  const chain = resolveRoute("auto");
  assert.equal(chain, ROUTES.auto);
  // P2 staleness fix: the primary must NOT be the flat z.ai coding plan.
  assert.notEqual(chain[0].provider, "zai", "coding plan must not be primary");
  assert.ok(chain.length >= 2, "chain must have a fallback");
  // The coding plan is acceptable only as a LAST-RESORT overflow entry.
  const codingIdx = chain.findIndex((u) => u.provider === "zai");
  if (codingIdx !== -1) {
    assert.equal(codingIdx, chain.length - 1, "coding plan must be last resort");
  }
});

test("missing/empty model falls back to auto", () => {
  assert.equal(resolveRoute(undefined), ROUTES.auto);
  assert.equal(resolveRoute(""), ROUTES.auto);
  assert.equal(resolveRoute("   "), ROUTES.auto);
});

test("pin-by-model returns a single-entry chain for an exact model id", () => {
  const chain = resolveRoute("glm-4.6");
  assert.equal(chain.length, 1);
  assert.equal(chain[0].model, "glm-4.6");
});

test("pin-by-model is case-insensitive", () => {
  const chain = resolveRoute("GLM-4.6");
  assert.equal(chain.length, 1);
  assert.equal(chain[0].model, "glm-4.6");
});

test("unknown model falls back to the full auto chain (never empty)", () => {
  const chain = resolveRoute("totally-made-up-model");
  assert.equal(chain, ROUTES.auto);
  assert.ok(chain.length >= 1);
});

test("costCents computes input+output COGS per million tokens", () => {
  const u = ROUTES.auto[0]; // primary (rate-agnostic so a retune can't break this)
  // 1M in + 1M out = (in + out) cents
  assert.equal(costCents(u, 1_000_000, 1_000_000), u.inCentsPerM + u.outCentsPerM);
  // proportional + fractional (not rounded here — Convex ceils at debit)
  assert.equal(costCents(u, 500_000, 0), u.inCentsPerM / 2);
  assert.equal(costCents(u, 0, 0), 0);
});

test("costCents stays fractional for sub-million token counts", () => {
  const u = ROUTES.auto[0];
  // 1000 input tokens at 60c/M = 0.06c — must NOT be pre-rounded here
  const c = costCents(u, 1000, 0);
  assert.ok(c > 0 && c < 1, `expected sub-cent, got ${c}`);
});

test("every route entry is well-formed (keyEnv, baseUrl, positive rates)", () => {
  for (const [name, chain] of Object.entries(ROUTES)) {
    assert.ok(chain.length >= 1, `${name} chain empty`);
    for (const u of chain) {
      assert.ok(u.provider && u.model, `${name}: provider/model missing`);
      assert.ok(/^https:\/\//.test(u.baseUrl), `${name}: baseUrl not https`);
      assert.ok(u.keyEnv.length > 0, `${name}: keyEnv missing`);
      assert.ok(u.inCentsPerM >= 0 && u.outCentsPerM >= 0, `${name}: negative rate`);
    }
  }
});
