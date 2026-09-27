import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

function source(path: string): string {
  return readFileSync(join(root, path), "utf8");
}

test("web billing sells Relay Pro without hosted-compute purchase copy", () => {
  const billing = source("components/dashboard/BillingView.tsx");

  assert.match(billing, /Relay Pro is \$9\/month/);
  assert.doesNotMatch(billing, /Cloud Workspace/);
  assert.doesNotMatch(billing, />☁ Yaver Cloud</);
  assert.doesNotMatch(billing, /subscribe for a cloud workspace/);
});

test("Relay Pro checkout is fail-closed behind the explicit live-payment flag", () => {
  const flags = source("lib/launchFlags.ts");
  const billing = source("components/dashboard/BillingView.tsx");

  assert.match(flags, /NEXT_PUBLIC_YAVER_RELAY_PRO_CHECKOUT_ENABLED === "true"/);
  assert.match(billing, /ENABLE_RELAY_PRO_CHECKOUT \?/);
  assert.match(billing, /Relay Pro payments are not open yet/);
});

test("web billing resource rows do not display provider resource ids", () => {
  const billing = source("components/dashboard/BillingView.tsx");

  assert.doesNotMatch(billing, /resource\s*\{m\.hetznerServerId/);
  assert.doesNotMatch(billing, /resource \$\{m\.hetznerServerId/);
});
