#!/usr/bin/env node

/**
 * Upload desktop installers to Convex storage.
 * Usage: node scripts/upload-downloads.mjs
 */

import fs from "fs";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.join(__dirname, "..");

function normalizeConvexUrl(raw) {
  if (!raw) return "";
  const trimmed = raw.trim();
  if (trimmed.includes(".convex.site")) {
    return trimmed.replace(".convex.site", ".convex.cloud");
  }
  return trimmed;
}

let convexUrl = normalizeConvexUrl(process.env.CONVEX_URL || process.env.CONVEX_SITE_URL);
if (!convexUrl && fs.existsSync(path.join(ROOT, "backend", ".env.local"))) {
  const envFile = fs.readFileSync(path.join(ROOT, "backend", ".env.local"), "utf8");
  convexUrl = normalizeConvexUrl(envFile.match(/CONVEX_URL=(.+)/)?.[1]?.trim());
}
if (!convexUrl) {
  console.error("CONVEX_URL not found in env or backend/.env.local");
  process.exit(1);
}

const DIST = process.env.DOWNLOADS_DIR
  ? path.resolve(process.env.DOWNLOADS_DIR)
  : path.join(ROOT, "desktop", "installer", "dist");

const versions = JSON.parse(fs.readFileSync(path.join(ROOT, "versions.json"), "utf8"));
const installerVersion = JSON.parse(fs.readFileSync(path.join(ROOT, "desktop", "installer", "package.json"), "utf8")).version;

const CANONICAL_FILENAMES = {
  "linux:arm64:image": "yaver-pi5-devnode-arm64.img.xz",
  "macos:arm64:dmg": "Yaver-arm64.dmg",
  "macos:arm64:zip": "Yaver-arm64.zip",
  "macos:amd64:dmg": "Yaver-amd64.dmg",
  "macos:amd64:zip": "Yaver-amd64.zip",
  "linux:arm64:appimage": "Yaver-arm64.AppImage",
  "linux:arm64:deb": "yaver-arm64.deb",
  "linux:amd64:deb": "yaver-amd64.deb",
  "linux:amd64:appimage": "Yaver-amd64.AppImage",
  "windows:amd64:exe": "Yaver-Setup.exe",
};

function inferArtifact(file) {
  const lower = file.toLowerCase();
  const ext = path.extname(file).toLowerCase();
  const arch = lower.includes("arm64") || lower.includes("aarch64")
    ? "arm64"
    : lower.includes("amd64") || lower.includes("x64")
      ? "amd64"
      : null;

  if (lower.endsWith(".dmg")) {
    return { platform: "macos", arch, format: "dmg" };
  }
  if (lower.endsWith(".zip") && lower.includes("mac")) {
    return { platform: "macos", arch, format: "zip" };
  }
  if (lower.endsWith(".appimage")) {
    return { platform: "linux", arch, format: "appimage" };
  }
  if (lower.endsWith(".img.xz") || lower.endsWith(".img.gz") || lower.endsWith(".img.zip")) {
    return { platform: "linux", arch: arch || "arm64", format: "image" };
  }
  if (lower.endsWith(".deb")) {
    return { platform: "linux", arch, format: "deb" };
  }
  if (lower.endsWith(".exe")) {
    return { platform: "windows", arch: arch || "amd64", format: "exe" };
  }

  return null;
}

function extractVersion(file) {
  const match = file.match(/(\d+\.\d+\.\d+)/);
  return match ? match[1] : "0.0.0";
}

function compareSemver(a, b) {
  const pa = a.split(".").map((n) => parseInt(n, 10) || 0);
  const pb = b.split(".").map((n) => parseInt(n, 10) || 0);
  for (let i = 0; i < Math.max(pa.length, pb.length); i++) {
    const da = pa[i] || 0;
    const db = pb[i] || 0;
    if (da !== db) return da - db;
  }
  return 0;
}

function collectFiles() {
  if (!fs.existsSync(DIST)) {
    throw new Error(`Dist directory not found: ${DIST}`);
  }

  const files = fs.readdirSync(DIST);
  const seen = new Map();
  const entries = [];

  for (const file of files) {
    const inferred = inferArtifact(file);
    if (!inferred?.arch) continue;

    const key = `${inferred.platform}:${inferred.arch}:${inferred.format}`;
    if (seen.has(key)) {
      const prev = seen.get(key);
      if (compareSemver(extractVersion(file), extractVersion(prev.file)) <= 0) {
        continue;
      }
      const idx = entries.findIndex((entry) => `${entry.platform}:${entry.arch}:${entry.format}` === key);
      if (idx >= 0) entries.splice(idx, 1);
    }

    const entry = {
      file,
      platform: inferred.platform,
      arch: inferred.arch,
      format: inferred.format,
      filename: CANONICAL_FILENAMES[key] || file,
    };
    seen.set(key, entry);
    entries.push(entry);
  }

  return entries;
}

async function convexMutation(fnPath, args = {}) {
  const res = await fetch(`${convexUrl}/api/mutation`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path: fnPath, args, format: "json" }),
  });
  if (!res.ok) throw new Error(`Mutation ${fnPath} failed: ${await res.text()}`);
  const data = await res.json();
  return data.value;
}

async function convexQuery(fnPath, args = {}) {
  const res = await fetch(`${convexUrl}/api/query`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path: fnPath, args, format: "json" }),
  });
  if (!res.ok) throw new Error(`Query ${fnPath} failed: ${await res.text()}`);
  const data = await res.json();
  return data.value;
}

async function uploadFile(entry) {
  const filePath = path.join(DIST, entry.file);
  if (!fs.existsSync(filePath)) {
    console.log(`  skip: ${entry.file} (not found)`);
    return;
  }

  const stat = fs.statSync(filePath);
  const size = stat.size;
  console.log(`  uploading: ${entry.file} (${(size / 1024 / 1024).toFixed(1)} MB)...`);

  // Get upload URL
  const uploadUrl = await convexMutation("downloads:generateUploadUrl");

  // Upload the file as a Blob (avoids EPIPE with streams)
  const fileBuffer = fs.readFileSync(filePath);
  const uploadRes = await fetch(uploadUrl, {
    method: "POST",
    headers: {
      "Content-Type": "application/octet-stream",
    },
    body: new Blob([fileBuffer]),
  });

  if (!uploadRes.ok) {
    console.error(`  FAILED: ${entry.file} — ${await uploadRes.text()}`);
    return;
  }

  const { storageId } = await uploadRes.json();

  // Record the download entry
  await convexMutation("downloads:createDownload", {
    platform: entry.platform,
    arch: entry.arch,
    format: entry.format,
    version: entry.format === "image" ? (versions.piImage || installerVersion) : installerVersion,
    filename: entry.filename,
    storageId,
    size,
  });

  console.log(`  done: ${entry.file} → ${storageId}`);
}

async function main() {
  const files = collectFiles();
  console.log(`Uploading to: ${convexUrl}`);
  console.log(`Dist dir: ${DIST}\n`);

  for (const entry of files) {
    await uploadFile(entry);
  }

  console.log("\nAll uploads complete. Verifying...");
  const downloads = await convexQuery("downloads:listDownloads");
  console.log(`\n${downloads.length} downloads available:`);
  for (const d of downloads) {
    console.log(`  ${d.platform}/${d.arch}/${d.format}: ${d.filename} (${(d.size / 1024 / 1024).toFixed(1)} MB)`);
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
