#!/usr/bin/env node

import fs from "fs";
import path from "path";
import { spawnSync } from "child_process";

const ROOT = process.cwd();
const DIST = process.argv[2]
  ? path.resolve(process.argv[2])
  : path.join(ROOT, "desktop", "installer", "dist");
const ARCH = process.argv[3] || process.env.YAVER_TARGET_ARCH || "";

function fail(message) {
  console.error(`verify-desktop-linux: ${message}`);
  process.exit(1);
}

function run(command, args, options = {}) {
  const result = spawnSync(command, args, {
    stdio: "pipe",
    encoding: "utf8",
    ...options,
  });
  if (result.status !== 0) {
    const stderr = (result.stderr || result.stdout || "").trim();
    fail(`${command} ${args.join(" ")} failed${stderr ? `\n${stderr}` : ""}`);
  }
  return result.stdout || "";
}

function findArtifact(ext) {
  const files = fs
    .readdirSync(DIST)
    .filter((file) => file.endsWith(ext))
    .filter((file) => !ARCH || file.includes(`-${ARCH}.`) || file.includes(`-${ARCH}${ext}`));

  if (files.length !== 1) {
    fail(`expected exactly one ${ext} artifact for arch=${ARCH || "any"} in ${DIST}, found ${files.length}`);
  }

  return path.join(DIST, files[0]);
}

function verifyDeb(debPath) {
  const info = run("dpkg-deb", ["-f", debPath, "Package", "Version", "Architecture"]);
  if (!info.includes("yaver-desktop")) {
    fail(`unexpected deb package metadata in ${path.basename(debPath)}:\n${info}`);
  }

  const contents = run("dpkg-deb", ["-c", debPath]);
  if (!contents.includes("/opt/Yaver/yaver-desktop")) {
    fail(`${path.basename(debPath)} does not contain /opt/Yaver/yaver-desktop`);
  }
  if (!contents.includes(".desktop")) {
    fail(`${path.basename(debPath)} is missing desktop entry`);
  }
}

function verifyAppImage(appImagePath) {
  fs.rmSync(path.join(DIST, "squashfs-root"), { recursive: true, force: true });
  fs.chmodSync(appImagePath, 0o755);

  const runtimeVersion = run(appImagePath, ["--appimage-version"]);
  if (!runtimeVersion.trim()) {
    fail(`${path.basename(appImagePath)} did not report an AppImage runtime version`);
  }

  run(appImagePath, ["--appimage-extract"], { cwd: DIST });

  const extractDir = path.join(DIST, "squashfs-root");
  const desktopFiles = fs.readdirSync(extractDir).filter((file) => file.endsWith(".desktop"));
  if (desktopFiles.length === 0) {
    fail(`${path.basename(appImagePath)} did not extract a desktop entry`);
  }

  const appRunPath = path.join(extractDir, "AppRun");
  if (!fs.existsSync(appRunPath)) {
    fail(`${path.basename(appImagePath)} did not extract AppRun`);
  }

  const hasExecutable = fs.existsSync(path.join(extractDir, "yaver-desktop"));
  if (!hasExecutable) {
    fail(`${path.basename(appImagePath)} did not extract yaver-desktop`);
  }
}

if (!fs.existsSync(DIST)) {
  fail(`dist directory not found: ${DIST}`);
}

const debPath = findArtifact(".deb");
const appImagePath = findArtifact(".AppImage");

console.log(`verify-desktop-linux: checking ${path.basename(debPath)}`);
verifyDeb(debPath);
console.log(`verify-desktop-linux: checking ${path.basename(appImagePath)}`);
verifyAppImage(appImagePath);
console.log("verify-desktop-linux: OK");
