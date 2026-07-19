import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  hasReusableManagedRelayForReconcile,
  genericMachineCreateDisabledMessage,
  legacyCreditPackWebhookDisabledMessage,
  legacyCreditPacksDisabledMessage,
  legacyPrepaidProvisionDisabledMessage,
  machineScopeDeniedMessage,
  managedServiceCapabilitiesRetiredMessage,
  normalizeLemonSqueezySubscriptionStatus,
  nonYaverManagedMachineDeniedMessage,
  normalizeBillingProduct,
  productForSubscriptionPlan,
  promptFreeMetadataBodyDeniedReason,
  unsupportedProviderResourceDeniedMessage,
  validateCustomerAutoParkRequest,
  yaverManagedMachineMutationDeniedReason,
} from "./http.js";

const httpSource = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "http.ts"), "utf8");

function httpRouteBlock(path: string, method?: string): string {
  const escaped = path.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const routeMatches = [...httpSource.matchAll(new RegExp(`http\\.route\\(\\{\\s*path: "${escaped}"`, "gm"))];
  assert.ok(routeMatches.length > 0, `route ${path} exists`);
  const match = routeMatches.find((candidate) => {
    const start = candidate.index ?? 0;
    const next = httpSource.indexOf("http.route({", start + 1);
    const block = httpSource.slice(start, next === -1 ? undefined : next);
    return !method || block.includes(`method: "${method}"`);
  });
  assert.ok(match, method ? `route ${method} ${path} exists` : `route ${path} exists`);
  const start = match.index ?? 0;
  const next = httpSource.indexOf("http.route({", start + 1);
  return httpSource.slice(start, next === -1 ? undefined : next);
}

test("customer auto-park validation requires a machine id", () => {
  assert.deepEqual(validateCustomerAutoParkRequest({ enabled: true }), {
    ok: false,
    error: "machineId is required",
  });
});

test("customer auto-park validation requires an explicit boolean", () => {
  assert.deepEqual(validateCustomerAutoParkRequest({ machineId: " machine-1 " }), {
    ok: false,
    error: "enabled (boolean) is required",
  });
});

test("customer auto-park validation rejects disabling cost protection", () => {
  assert.deepEqual(validateCustomerAutoParkRequest({ machineId: "machine-1", enabled: false }), {
    ok: false,
    error: "Cloud Workspace auto-close is required to protect your usage and Yaver's compute costs",
  });
});

test("customer auto-park validation accepts enable and optional idle minutes", () => {
  assert.deepEqual(validateCustomerAutoParkRequest({
    machineId: " machine-1 ",
    enabled: true,
    idleMinutes: 30,
  }), {
    ok: true,
    machineId: "machine-1",
    enabled: true,
    idleMinutes: 30,
  });
});

test("relay reconcile reuses healthy or in-flight managed relay rows", () => {
  assert.equal(hasReusableManagedRelayForReconcile([{ status: "active" }]), true);
  assert.equal(hasReusableManagedRelayForReconcile([{ status: "provisioning" }]), true);
});

test("relay reconcile repairs only when all existing relay rows are dead", () => {
  assert.equal(hasReusableManagedRelayForReconcile([{ status: "stopped" }]), false);
  assert.equal(hasReusableManagedRelayForReconcile([{ status: "error" }]), false);
  assert.equal(
    hasReusableManagedRelayForReconcile([{ status: "stopped" }, { status: "error" }]),
    false,
  );
});

test("relay reconcile status check fails closed on missing relay data", () => {
  assert.equal(hasReusableManagedRelayForReconcile(null), false);
  assert.equal(hasReusableManagedRelayForReconcile(undefined), false);
  assert.equal(hasReusableManagedRelayForReconcile([]), false);
});

test("legacy credit-pack checkout remains disabled for flat subscription model", () => {
  assert.equal(
    legacyCreditPacksDisabledMessage(),
    "Credit packs are not sold. Use Relay Pro or Cloud Workspace subscription billing.",
  );
  assert.equal(
    legacyCreditPackWebhookDisabledMessage(),
    "Legacy one-time credit-pack webhooks are ignored for the flat subscription model.",
  );
});

test("legacy prepaid workspace provision remains disabled for flat subscription model", () => {
  assert.equal(
    legacyPrepaidProvisionDisabledMessage(),
    "Legacy prepaid workspace provisioning is disabled. Subscribe to Cloud Workspace on web.",
  );
});

test("managed service capability cockpit remains retired for flat subscription model", () => {
  assert.equal(
    managedServiceCapabilitiesRetiredMessage(),
    "Managed service capability toggles are retired. Use Relay Pro or Cloud Workspace subscription billing.",
  );
  for (const route of [
    { path: "/managed/services", method: "GET" },
    { path: "/managed/services", method: "POST" },
    { path: "/managed/cockpit", method: "GET" },
    { path: "/managed/burn", method: "GET" },
  ]) {
    const block = httpRouteBlock(route.path, route.method);
    assert.match(block, /managedServiceCapabilitiesRetiredMessage\(\)/, route.path);
    assert.doesNotMatch(block, /internal\.managedServices\./, route.path);
  }
});

test("direct machine creation remains disabled for flat subscription model", () => {
  assert.equal(
    genericMachineCreateDisabledMessage(),
    "Direct Cloud Workspace machine creation is disabled. Subscribe to Cloud Workspace on web.",
  );
  const block = httpRouteBlock("/machines", "POST");
  assert.match(block, /genericMachineCreateDisabledMessage\(\)/);
  assert.doesNotMatch(block, /internal\.cloudMachines\.create/);
});

test("dev preview activation does not schedule managed provisioning", () => {
  const start = httpSource.indexOf("async function ensurePreviewCloudMachine");
  assert.ok(start >= 0, "ensurePreviewCloudMachine exists");
  const end = httpSource.indexOf("/** Extract Bearer token", start);
  const block = httpSource.slice(start, end);
  assert.match(block, /internal\.cloudMachines\.createPreviewSharedMachine/);
  assert.doesNotMatch(block, /internal\.cloudMachines\.create,/);
});

test("billing product normalizer exposes only Relay Pro and Cloud Workspace", () => {
  for (const value of [undefined, "", "relay-pro", "relay-monthly", "relay-yearly", "managed-relay"]) {
    assert.equal(normalizeBillingProduct(value), "relay-pro", String(value));
  }
  for (const value of ["cloud-workspace", "yaver-cloud", "cloud-agent", "cpu", "gpu"]) {
    assert.equal(normalizeBillingProduct(value), "cloud-workspace", String(value));
  }
  for (const value of ["free", "credits", "compute-only", "openrouter"]) {
    assert.equal(normalizeBillingProduct(value), null, String(value));
  }
});

test("subscription plans normalize to the two paid products or free", () => {
  for (const value of ["relay-pro", "relay-monthly", "relay-yearly", "managed-relay"]) {
    assert.equal(productForSubscriptionPlan(value), "relay-pro", value);
  }
  for (const value of ["cloud-workspace", "cloud-agent", "yaver-cloud-standard"]) {
    assert.equal(productForSubscriptionPlan(value), "cloud-workspace", value);
  }
  for (const value of ["", undefined, "credits", "compute-only"]) {
    assert.equal(productForSubscriptionPlan(value), "free", String(value));
  }
});

test("lemonsqueezy subscription status normalization fails closed", () => {
  assert.equal(normalizeLemonSqueezySubscriptionStatus("active"), "active");
  assert.equal(normalizeLemonSqueezySubscriptionStatus("past_due"), "past_due");
  assert.equal(normalizeLemonSqueezySubscriptionStatus("cancelled"), "cancelled");
  assert.equal(normalizeLemonSqueezySubscriptionStatus(undefined), "unknown");
  assert.equal(normalizeLemonSqueezySubscriptionStatus(" payment failed "), "payment_failed");
});

test("prompt-free metadata body guard rejects task content keys", () => {
  for (const key of ["title", "description", "prompt", "userPrompt", "bodyJson", "workDir", "files", "images", "stdout", "secret", "token", "diff", "patch", "customCommand", "gitBranch"]) {
    const denied = promptFreeMetadataBodyDeniedReason({
      localTaskId: "pending-cloud:test",
      nested: { [key]: "secret task content" },
    });
    assert.match(denied ?? "", new RegExp(key.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "i"), key);
  }
  assert.equal(
    promptFreeMetadataBodyDeniedReason({
      localTaskId: "pending-cloud:test",
      placementId: "placement-1",
      sourceSurface: "web",
      lane: "cloud_build",
      targetDeviceId: "cloud-dev",
      requestedRunner: "codex",
      projectSlug: "demo",
      reason: "workspace waking",
      ttlMs: 60000,
    }),
    null,
  );
});

test("managed cloud mutation guard allows legacy and explicit Yaver-managed Hetzner rows", () => {
  assert.equal(yaverManagedMachineMutationDeniedReason({}), null);
  assert.equal(
    yaverManagedMachineMutationDeniedReason({ origin: "managed", provider: "hetzner" }),
    null,
  );
});

test("managed cloud mutation guard refuses self-hosted or unsupported provider rows", () => {
  assert.equal(
    yaverManagedMachineMutationDeniedReason({ origin: "self-hosted", provider: "hetzner" }),
    nonYaverManagedMachineDeniedMessage(),
  );
  assert.equal(
    yaverManagedMachineMutationDeniedReason({ origin: "managed", provider: "aws" }),
    unsupportedProviderResourceDeniedMessage(),
  );
});

test("machine-scoped tokens remain denied on account-level cloud operations", () => {
  assert.equal(
    machineScopeDeniedMessage(),
    "This token is machine-scoped and cannot perform account-level operations.",
  );
});

test("account-level billing and cloud-control routes require full user scope", () => {
  for (const route of [
    { path: "/billing/checkout" },
    { path: "/billing/yaver-cloud/checkout" },
    { path: "/billing/yaver-cloud/change-plan" },
    { path: "/billing/yaver-cloud/provision" },
    { path: "/billing/yaver-cloud/balance" },
    { path: "/billing/yaver-cloud/usage" },
    { path: "/billing/yaver-cloud/start" },
    { path: "/billing/yaver-cloud/stop" },
    { path: "/billing/yaver-cloud/auto-park" },
    { path: "/billing/yaver-cloud/dev-activate" },
    { path: "/billing/yaver-cloud/dev-adopt" },
    { path: "/billing/yaver-cloud/dev-deprovision" },
    { path: "/billing/yaver-cloud/reconcile" },
    { path: "/billing/yaver-cloud/runners-authorized" },
    { path: "/billing/yaver-cloud/topup-dev" },
    { path: "/billing/credits/packs" },
    { path: "/billing/credits/checkout" },
    { path: "/managed/services", method: "GET" },
    { path: "/managed/services", method: "POST" },
    { path: "/managed/cockpit" },
    { path: "/managed/burn" },
    { path: "/billing/status" },
    { path: "/billing/portal" },
    { path: "/billing/cancel" },
    { path: "/machines", method: "POST" },
  ]) {
    assert.match(httpRouteBlock(route.path, route.method), /requireFullScope\(session\)/, route.path);
  }
});

test("buyer billing status does not expose raw wallet balance", () => {
  const block = httpRouteBlock("/billing/status", "GET");
  assert.doesNotMatch(block, /walletCents/);
  assert.doesNotMatch(block, /getWallet/);
  assert.match(block, /includedStandardCredits/);
});

test("cloud placement activation filters machines through placement eligibility before wake", () => {
  const block = httpRouteBlock("/tasks/placement/activate", "POST");
  assert.match(block, /requireFullScope\(session\)/);
  assert.match(block, /internal\.cloudMachines\.listForUser/);
  assert.match(block, /String\(machine\.userId\) === String\(session\.userDocId\)/);
  assert.match(block, /cloudMachineEligibleForPlacement\(machine\)/);
  assert.match(block, /selectCloudMachineForPlacement\(\s*existingMachines,\s*placement\.resourceClass,\s*placement\.cloudMachineId,/);
  assert.doesNotMatch(block, /"stopped"[\s\S]*selectCloudMachineForPlacement/);
});

test("lemonsqueezy webhook provisions only for active subscription_created events", () => {
  const block = httpRouteBlock("/webhooks/lemonsqueezy", "POST");
  assert.match(block, /const status = normalizeLemonSqueezySubscriptionStatus\(data\.status\)/);
  assert.match(block, /if \(eventName === "subscription_created" && status === "active"\)/);
  assert.doesNotMatch(block, /data\.status === "active" \? "active" : data\.status === "past_due" \? "past_due" : "active"/);
});

test("prompt-free task intent HTTP routes reject sensitive request bodies before mutations", () => {
  for (const route of [
    "/tasks/placement/preview",
    "/tasks/placement/record",
    "/tasks/placement/status",
    "/tasks/placement/rebind",
    "/tasks/placement/activate",
    "/tasks/dispatch-intents",
    "/tasks/dispatch-intents/status",
    "/tasks/relay-source-intents",
    "/tasks/relay-source-intents/status",
    "/tasks/relay-source-intents/claim",
    "/tasks/relay-source-intents/github-app-token",
    "/tasks/relay-source-intents/gitlab-token",
  ]) {
    const block = httpRouteBlock(route, "POST");
    assert.match(block, /promptFreeMetadataBodyDeniedReason\(body\)/, route);
    assert.match(block, /if \(denied\) return errorResponse\(denied, 400\)/, route);
  }
});
