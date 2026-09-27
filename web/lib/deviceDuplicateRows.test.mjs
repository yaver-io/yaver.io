import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const view = readFileSync(join(root, "components/dashboard/DevicesView.tsx"), "utf8");
const dashboard = readFileSync(join(root, "app/dashboard/page.tsx"), "utf8");

test("web trusts backend identity collapse and never hides by hostname", () => {
  for (const [name, source] of [["DevicesView", view], ["dashboard", dashboard]]) {
    assert.doesNotMatch(source, /function duplicateHostKey/, `${name} reintroduced hostname-only identity`);
    assert.doesNotMatch(source, /duplicateAuthSiblingIds|duplicateAuthSidebarIds/, `${name} reintroduced client duplicate hiding`);
  }
  assert.match(view, /const renderedDevices = \[\.\.\.devices\]\.sort/);
  assert.match(dashboard, /const visibleDevices = displayDevices/);
});

test("transient peer inventory failures preserve the last evidence", () => {
  assert.doesNotMatch(
    dashboard,
    /catch \{\s*if \(!cancelled\) setPeerStates\(\{\}\);\s*\}/,
    "one failed peer poll must not mark every machine stale",
  );
});
