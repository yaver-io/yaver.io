import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

function source(path: string): string {
  return readFileSync(join(root, path), "utf8");
}

test("web billing surfaces use Cloud Workspace product copy", () => {
  const billing = source("components/dashboard/BillingView.tsx");
  const managed = source("components/dashboard/ManagedCloudPanel.tsx");
  const combined = `${billing}\n${managed}`;

  assert.match(combined, /Cloud Workspace/);
  assert.doesNotMatch(combined, />☁ Yaver Cloud</);
  assert.doesNotMatch(combined, /Decommission this box/);
  assert.doesNotMatch(combined, /Delete box/);
  assert.doesNotMatch(combined, /subscribe for a cloud workspace/);
});

test("web billing resource rows do not display provider resource ids", () => {
  const billing = source("components/dashboard/BillingView.tsx");
  const managed = source("components/dashboard/ManagedCloudPanel.tsx");

  assert.doesNotMatch(billing, /resource\s*\{m\.hetznerServerId/);
  assert.doesNotMatch(managed, /resource\s*\{m\.hetznerServerId/);
  assert.doesNotMatch(billing, /resource \$\{m\.hetznerServerId/);
  assert.doesNotMatch(managed, /resource \$\{m\.hetznerServerId/);
});
