#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";

const repoRoot = process.cwd();
const pkgDir = path.join(repoRoot, "sdk/feedback/unity");
const pkgPath = path.join(pkgDir, "package.json");

function fail(message) {
  console.error(`unity-package: ${message}`);
  process.exitCode = 1;
}

function exists(relPath) {
  return fs.existsSync(path.join(pkgDir, relPath));
}

function readJson(relPath) {
  return JSON.parse(fs.readFileSync(path.join(pkgDir, relPath), "utf8"));
}

if (!fs.existsSync(pkgPath)) {
  fail("sdk/feedback/unity/package.json not found");
  process.exit(1);
}

const pkg = readJson("package.json");
const requiredFields = ["name", "displayName", "version", "unity", "description", "license"];
for (const field of requiredFields) {
  if (!pkg[field] || String(pkg[field]).trim() === "") {
    fail(`package.json missing required field: ${field}`);
  }
}

if (!String(pkg.name).startsWith("io.yaver.")) {
  fail(`package name should start with io.yaver.* (found ${pkg.name})`);
}

if (!Array.isArray(pkg.keywords) || pkg.keywords.length < 3) {
  fail("package.json should include at least 3 keywords");
}

for (const rel of [
  "README.md",
  "CHANGELOG.md",
  "Third-Party Notices.txt",
  "Documentation~/index.md",
  "Runtime/Yaver.Feedback.asmdef",
]) {
  if (!exists(rel)) {
    fail(`missing required package file: ${rel}`);
  }
}

const forbiddenExts = new Set([".exe", ".dll", ".so", ".dylib", ".apk", ".aab", ".ipa", ".appimage"]);

function walk(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(full);
      continue;
    }
    const ext = path.extname(entry.name).toLowerCase();
    if (forbiddenExts.has(ext)) {
      fail(`forbidden binary artifact in Unity package: ${path.relative(pkgDir, full)}`);
    }
  }
}

walk(pkgDir);

if (process.exitCode) {
  process.exit(process.exitCode);
}

console.log(`unity-package: ok (${pkg.name}@${pkg.version})`);
