import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

const webRoot = join(import.meta.dirname, "..");
const runtime = readFileSync(join(webRoot, "components/dashboard/RuntimeLabView.tsx"), "utf8");
const dashboard = readFileSync(join(webRoot, "app/dashboard/page.tsx"), "utf8");

function hasLeanStudioLaunchFlow(source: string): boolean {
  const flow = source.indexOf('data-testid="studio-launch-flow"');
  const configure = source.indexOf(">Configure</h2>", flow);
  const launch = source.indexOf(">Launch</h2>", configure);
  const launchAction = source.indexOf('"Launch Studio"', launch);
  return flow >= 0 && configure > flow && launch > configure && launchAction > launch;
}

test("Studio leads with configuration and then one primary launch action", () => {
  assert.equal(hasLeanStudioLaunchFlow(runtime), true);
  assert.ok(runtime.includes("disabled={!selectedProject || !connectedDevice?.id || !effectiveRenderDeviceId || webPreviewBusy}"));
  assert.ok(runtime.includes("onClick={() => void openWebUI()}"));
});

test("the setup-order guard fails when configuration is removed", () => {
  assert.equal(hasLeanStudioLaunchFlow(runtime.replace(">Configure</h2>", ">Setup</h2>")), false);
});

test("dashboard navigation exposes Studio, not the old Vibing label", () => {
  assert.match(dashboard, /id: "runtime", label: "Studio"/);
  assert.doesNotMatch(dashboard, /id: "runtime", label: "Vibing"/);
});
