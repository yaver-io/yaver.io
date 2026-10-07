#!/usr/bin/env node

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const workflow = fs.readFileSync(
  path.join(root, ".github/workflows/release-gui.yml"),
  "utf8",
);
const versions = JSON.parse(
  fs.readFileSync(path.join(root, "versions.json"), "utf8"),
);
const electronPackage = JSON.parse(
  fs.readFileSync(path.join(root, "electron/package.json"), "utf8"),
);

assert.equal(
  versions.gui,
  electronPackage.version,
  "GUI versions must match before a release tag is created",
);
assert.match(
  workflow,
  /- os: macos-15\s+platform: mac\s+arch: arm64/,
  "signed arm64 desktop releases must use the proven macOS 15 runner",
);
assert.doesNotMatch(
  workflow,
  /- os: macos-latest\s+platform: mac\s+arch: arm64/,
  "macos-latest must not silently move the signed arm64 lane to an unproven image",
);

const helper = workflow.indexOf("run: node scripts/build-plain-ssh.mjs");
assert.ok(helper >= 0, "CI must compile the native SSH helper before packaging");
for (const platform of ["mac", "linux"]) {
  assert.ok(helper < workflow.indexOf(`npx electron-builder --${platform}`),
    `${platform} packaging must include the freshly built SSH helper`);
}
console.log("Desktop release contract passed.");
