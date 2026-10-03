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

console.log("Desktop release contract passed.");
