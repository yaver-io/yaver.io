import test from "node:test";
import assert from "node:assert/strict";

import {
  COMPUTE_PROVIDER_CATALOG,
  INFERENCE_PROVIDER_CATALOG,
  PLACEMENT_POLICY_CATALOG,
  providerCatalogDefaults,
} from "./providerCatalog.js";

test("compute catalog contains every planned provider but gates new clouds by default", () => {
  assert.deepEqual(
    COMPUTE_PROVIDER_CATALOG.map((entry) => entry.provider),
    ["hetzner", "gcp", "aws", "azure", "alibaba"],
  );
  assert.equal(COMPUTE_PROVIDER_CATALOG.find((entry) => entry.provider === "hetzner")?.productionEligible, true);
  for (const provider of ["gcp", "aws", "azure", "alibaba"]) {
    assert.equal(COMPUTE_PROVIDER_CATALOG.find((entry) => entry.provider === provider)?.productionEligible, false);
  }
});

test("inference catalog includes managed, external, and BYO choices", () => {
  const ids = INFERENCE_PROVIDER_CATALOG.map((entry) => entry.id);
  assert.equal(ids.includes("bedrock"), true);
  assert.equal(ids.includes("vertex-gemini"), true);
  assert.equal(ids.includes("azure-ai"), true);
  assert.equal(ids.includes("dashscope"), true);
  assert.equal(ids.includes("external-openai-compatible"), true);
  assert.equal(ids.includes("byo-openai-compatible"), true);
  assert.equal(INFERENCE_PROVIDER_CATALOG.find((entry) => entry.provider === "byo")?.productionEligible, true);
});

test("placement policy keeps provider details minimal for users", () => {
  assert.equal(PLACEMENT_POLICY_CATALOG.hideProviderDetailsByDefault, true);
  assert.deepEqual(PLACEMENT_POLICY_CATALOG.userVisibleComputeFields, [
    "providerLabel",
    "regionLabel",
    "machineState",
  ]);
  assert.deepEqual(PLACEMENT_POLICY_CATALOG.userVisibleInferenceFields, [
    "sourceLabel",
    "modelLabel",
    "byoRequired",
  ]);
});

test("seeded provider catalogs are serializable and contain no secret material", () => {
  const defaults = providerCatalogDefaults();
  for (const [key, value] of Object.entries(defaults)) {
    assert.equal(typeof key, "string");
    assert.doesNotThrow(() => JSON.parse(value));
  }

  const serialized = JSON.stringify(defaults).toLowerCase();
  for (const forbidden of [
    "access_key",
    "api_key",
    "bearer",
    "billing_account",
    "client_secret",
    "hcloud_token",
    "password",
    "secret_access_key",
    "subscription_id",
    "token:",
  ]) {
    assert.equal(serialized.includes(forbidden), false, forbidden);
  }
});
