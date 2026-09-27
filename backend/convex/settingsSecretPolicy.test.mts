import assert from "node:assert/strict";
import test from "node:test";

import { rawSecretFieldsInSettings, settingsWithoutSecrets } from "./settingsSecretPolicy.ts";

test("settings rejects every raw credential family while allowing metadata", () => {
  assert.deepEqual(rawSecretFieldsInSettings({
    mobileCodingProvider: "deepseek",
    deepseekApiKey: "sk-must-not-reach-convex",
    githubToken: "github_pat_must-not-reach-convex",
  }), ["deepseekApiKey", "githubToken"]);
  assert.deepEqual(rawSecretFieldsInSettings({ mobileCodingProvider: "deepseek", hasApiKey: true }), []);
  assert.deepEqual(rawSecretFieldsInSettings({ deepseekApiKey: "" }), []);
});

test("settings reads strip legacy secret columns defensively", () => {
  assert.deepEqual(settingsWithoutSecrets({
    forceRelay: true,
    speechApiKey: "legacy-secret",
    relayPassword: "account-scoped-relay-routing-credential",
  }), { forceRelay: true, relayPassword: "account-scoped-relay-routing-credential" });
});

test("publisher profile refuses credentials, tax IDs, payment data, and signing material", () => {
  assert.deepEqual(rawSecretFieldsInSettings({
    publisherProfile: {
      legalName: "Example Studio Ltd",
      dunsNumber: "123456789",
      taxId: "must-stay-with-platform",
      privateKey: "must-stay-in-vault",
    },
  }), ["publisherProfile.privateKey", "publisherProfile.taxId"]);
});
