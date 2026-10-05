"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const {
  openCodeBody,
  seedCIHostDefaults,
} = require("../src/ci-host-defaults");

function tempHome(t) {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), "yaver-ci-defaults-"));
  t.after(() => fs.rmSync(home, { recursive: true, force: true }));
  return home;
}

const allCommands = () => true;
const noNetworkClone = () => false;

test("raw Linux CI host receives credential-blind defaults", (t) => {
  const home = tempHome(t);
  const result = seedCIHostDefaults({
    home,
    platform: "linux",
    commandExists: allCommands,
    cloneIfMissing: noNetworkClone,
  });

  assert.deepEqual(result.created.sort(), [
    path.join(home, ".config", "opencode", "opencode.json"),
    path.join(home, ".tmux.conf"),
    path.join(home, ".zshrc"),
  ].sort());
  const opencode = JSON.parse(fs.readFileSync(path.join(home, ".config", "opencode", "opencode.json"), "utf8"));
  assert.equal(opencode.model, "deepseek/deepseek-flash");
  assert.deepEqual(opencode.mcp.yaver.command, ["yaver", "mcp"]);
  assert.equal(opencode.permission, "ask");
  assert.doesNotMatch(JSON.stringify(opencode), /token|apiKey|password|secret/i);
});

test("existing shell, tmux and OpenCode files are byte-for-byte preserved", (t) => {
  const home = tempHome(t);
  const fixtures = new Map([
    [path.join(home, ".zshrc"), "owner zsh config\n"],
    [path.join(home, ".tmux.conf"), "owner tmux config\n"],
    [path.join(home, ".config", "opencode", "opencode.json"), '{"owner":true}\n'],
  ]);
  for (const [filename, body] of fixtures) {
    fs.mkdirSync(path.dirname(filename), { recursive: true });
    fs.writeFileSync(filename, body);
  }

  const result = seedCIHostDefaults({
    home,
    platform: "linux",
    commandExists: allCommands,
    cloneIfMissing: () => assert.fail("must not clone over owner configuration"),
  });

  assert.equal(result.created.length, 0);
  for (const [filename, body] of fixtures) {
    assert.equal(fs.readFileSync(filename, "utf8"), body);
  }
});

test("OpenCode template uses the provider-native DeepSeek V4.1 Flash id without credential fields", () => {
  assert.match(openCodeBody(), /deepseek\/deepseek-flash/);
  assert.match(openCodeBody(), /DeepSeek V4\.1 Flash/);
  assert.doesNotMatch(openCodeBody(), /apiKey|token|password|secret/i);
});
