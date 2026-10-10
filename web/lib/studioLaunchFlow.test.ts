import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

const webRoot = join(import.meta.dirname, "..");
const runtime = readFileSync(join(webRoot, "components/workspace/WorkspaceShell.tsx"), "utf8");
const dashboard = readFileSync(join(webRoot, "app/dashboard/page.tsx"), "utf8");
const terminal = readFileSync(join(webRoot, "components/dashboard/TerminalView.tsx"), "utf8");

test("Studio leads with configuration and then one primary launch action", () => {
  const configuration = runtime.indexOf("!launched ? (");
  const machine = runtime.indexOf('aria-label="Runner PC"', configuration);
  const project = runtime.indexOf(">Project</span>", machine);
  const runner = runtime.indexOf(">Runner</span>", project);
  const lane = runtime.indexOf(">Lane</span>", machine);
  const launch = runtime.indexOf("Save and open Studio", runner);
  assert.ok(configuration >= 0 && machine > configuration && project > machine && lane > machine && runner > project && launch > runner);
});

test("the setup-order guard fails when configuration is removed", () => {
  assert.doesNotMatch(runtime.replace('aria-label="Runner PC"', 'aria-label="Machine"'), /aria-label="Runner PC"/);
});

test("dashboard navigation exposes Studio, not the old Vibing label", () => {
  assert.match(dashboard, /id: "runtime", label: "Studio"/);
  assert.doesNotMatch(dashboard, /id: "runtime", label: "Vibing"/);
});

test("Studio is a strict 30/70 phone-preview and console-only SSH workspace", () => {
  assert.match(runtime, /"30% minmax\(0, 70%\)"/);
  assert.match(runtime, /data-studio-preview-frame=.*"phone"/);
  assert.match(runtime, /<TerminalView client=\{client\} consoleOnly/);
  assert.match(terminal, /consoleOnly\?: boolean/);
  assert.match(dashboard, /<WorkspaceShell/);
});
