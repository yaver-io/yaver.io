"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { ensureAgentBinary, refreshCurrentSymlink } = require("../src/agent-runtime");

function fixture() {
  const cacheRoot = fs.mkdtempSync(path.join(os.tmpdir(), "yaver-current-link-"));
  const oldVersion = path.join(cacheRoot, "1.99.452");
  const newVersion = path.join(cacheRoot, "1.99.459");
  const binary = path.join(newVersion, "darwin-arm64", "yaver");
  fs.mkdirSync(oldVersion, { recursive: true });
  fs.mkdirSync(path.dirname(binary), { recursive: true });
  fs.writeFileSync(binary, "fixture");
  fs.symlinkSync(oldVersion, path.join(cacheRoot, "current"));
  return { cacheRoot, oldVersion, newVersion, binary };
}

test("every successful agent resolution reconciles the supervisor link", async () => {
  const calls = [];
  const binary = "/cache/1.99.459/darwin-arm64/yaver";
  const resolved = await ensureAgentBinary({
    quiet: true,
    resolveBinary: async (options) => {
      calls.push(["resolve", options]);
      return binary;
    },
    refreshLink: (binaryPath, options) => calls.push(["refresh", binaryPath, options]),
  });

  assert.equal(resolved, binary);
  assert.deepEqual(calls, [
    ["resolve", { quiet: true }],
    ["refresh", binary, { quiet: true }],
  ]);
});

test("lazy agent resolution atomically replaces a stale current symlink", (t) => {
  const f = fixture();
  t.after(() => fs.rmSync(f.cacheRoot, { recursive: true, force: true }));

  const calls = [];
  const fsImpl = new Proxy(fs, {
    get(target, property) {
      if (property === "unlinkSync") {
        return (...args) => {
          calls.push(["unlink", ...args]);
          return target.unlinkSync(...args);
        };
      }
      if (property === "renameSync") {
        return (...args) => {
          calls.push(["rename", ...args]);
          return target.renameSync(...args);
        };
      }
      return target[property];
    },
  });

  assert.equal(refreshCurrentSymlink(f.binary, {
    platform: "darwin",
    cacheRoot: f.cacheRoot,
    fsImpl,
    quiet: true,
  }), "repointed");
  assert.equal(fs.realpathSync(path.join(f.cacheRoot, "current")), fs.realpathSync(f.newVersion));
  assert.equal(calls.filter(([operation]) => operation === "rename").length, 1);
  assert.equal(calls.filter(([operation, target]) =>
    operation === "unlink" && target === path.join(f.cacheRoot, "current")).length, 0);
});

test("current-link reconciliation is idempotent and cache-contained", (t) => {
  const f = fixture();
  t.after(() => fs.rmSync(f.cacheRoot, { recursive: true, force: true }));

  assert.equal(refreshCurrentSymlink(f.binary, {
    platform: "darwin",
    cacheRoot: f.cacheRoot,
    quiet: true,
  }), "repointed");
  assert.equal(refreshCurrentSymlink(f.binary, {
    platform: "darwin",
    cacheRoot: f.cacheRoot,
    quiet: true,
  }), "already-current");

  const outside = fs.mkdtempSync(path.join(os.tmpdir(), "yaver-outside-link-"));
  t.after(() => fs.rmSync(outside, { recursive: true, force: true }));
  const outsideBinary = path.join(outside, "1.99.999", "darwin-arm64", "yaver");
  fs.mkdirSync(path.dirname(outsideBinary), { recursive: true });
  fs.writeFileSync(outsideBinary, "fixture");
  assert.equal(refreshCurrentSymlink(outsideBinary, {
    platform: "darwin",
    cacheRoot: f.cacheRoot,
    quiet: true,
  }), "skipped");
  assert.equal(fs.realpathSync(path.join(f.cacheRoot, "current")), fs.realpathSync(f.newVersion));
});
