"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { codingRunnerBootstrapPlan } = require("../src/runner-bootstrap-policy");

const source = fs.readFileSync(path.join(__dirname, "..", "src", "postinstall.js"), "utf8");

test("global bootstrap names all three official first-class runner packages", () => {
  assert.match(source, /@anthropic-ai\/claude-code/);
  assert.match(source, /@openai\/codex/);
  assert.match(source, /opencode-ai/);
});

test("an existing OpenCode install is wired without installing competing runners", () => {
  const entries = [
    { command: "claude", pkg: "claude" },
    { command: "codex", pkg: "codex" },
    { command: "opencode", pkg: "opencode" },
  ];
  const plan = codingRunnerBootstrapPlan(entries, (command) => command === "opencode");
  assert.deepEqual(plan.installed.map((entry) => entry.command), ["opencode"]);
  assert.deepEqual(plan.toInstall, []);
  assert.match(source, /Other runners were not installed/);
  assert.match(source, /setupMCPForInstalledRunners/);
});

test("a fresh machine still receives the supported runner bootstrap", () => {
  const entries = [
    { command: "claude" },
    { command: "codex" },
    { command: "opencode" },
  ];
  const plan = codingRunnerBootstrapPlan(entries, () => false);
  assert.deepEqual(plan.installed, []);
  assert.deepEqual(plan.toInstall, entries);
});

test("PowerShell/global Windows installs bootstrap runners before returning", () => {
  const windowsBranch = source.slice(source.lastIndexOf('if (process.platform === "win32") {'));
  assert.match(windowsBranch, /installMissingCodingRunners\(\)/);
  assert.match(windowsBranch, /setupMCPForInstalledRunners\(\)/);
  assert.match(source, /where\.exe/);
  assert.match(source, /process\.platform === "win32" \? prefix/);
});

test("desktop companion bootstrap is verified, best-effort, and reversible", () => {
  assert.match(source, /desktop\(\["install", "--no-open"\]\)/);
  assert.match(source, /YAVER_SKIP_POSTINSTALL_DESKTOP/);
  assert.match(source, /installedDesktopCandidates/);
});

test("Linux global upgrades bounce both supported system service names", () => {
  assert.match(source, /for \(const unit of \["yaver", "yaver-agent"\]\)/);
  assert.match(source, /systemctl is-active \$\{unit\}/);
  assert.match(source, /systemctl restart \$\{unit\}/);
});

test("default postinstall is React Native first and heavy labs require positive opt-in", () => {
  assert.match(source, /runAgentCommand\(\["install", "mobile"\]/,
    "Hermes bundle push remains part of the default install");
  assert.match(source, /installMissingMobileTools\(\)/,
    "Expo and EAS remain part of complete React Native support");
  for (const name of ["REMOTE_RUNTIME", "VSR", "VIBE_PREVIEW", "TESTKIT", "VOICE"]) {
    assert.match(source, new RegExp(`envEnabled\\("YAVER_POSTINSTALL_${name}"\\)`),
      `${name} must require explicit opt-in`);
  }
  assert.match(source, /React Native \/ Expo core is ready/);
});
