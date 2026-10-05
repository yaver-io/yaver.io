import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const root = join(import.meta.dirname, "../..");
const dashboard = readFileSync(join(root, "web/app/dashboard/page.tsx"), "utf8");
const terminal = readFileSync(join(root, "web/components/dashboard/TerminalView.tsx"), "utf8");
const electron = readFileSync(join(root, "electron/src/main.js"), "utf8");

test("the macOS Electron shell inherits a first-class SSH dashboard", () => {
  assert.match(electron, /yaver\.io\/dashboard/);
  assert.match(dashboard, /useState<DashboardTab>\("ssh"\)/);
  assert.match(dashboard, /\{ id: "ssh",\s+label: "SSH" \}/);
  assert.match(dashboard, /onClick=\{\(\) => setShellDevice\(connectedDevice\)\}/);
  assert.match(dashboard, />\s*Open SSH\s*</);
});

test("desktop SSH keeps raw shells plain and exposes tmux controls", () => {
  assert.doesNotMatch(terminal, /AUTO_TMUX_COMMAND|autoTmuxTimer/);
  assert.match(terminal, />\s*Attach\s*</);
  assert.match(terminal, />\s*Detach\s*</);
  assert.match(terminal, /controlTmuxClient\(session, "detach"\)/);
  assert.match(terminal, /controlTmuxClient\(session, "kill-pane"\)/);
  assert.doesNotMatch(terminal, /AGENT_LAUNCHERS|dangerously-skip-permissions|dangerously-bypass-approvals/);
});
