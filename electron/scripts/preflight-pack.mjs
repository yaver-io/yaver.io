#!/usr/bin/env node
/**
 * Packaging preflight for the Yaver GUI (audit pass-2 DP10).
 *
 * electron-builder fails obscurely — or silently bundles an absent agent —
 * when the embedded Go agent binary is missing from resources/bin. This
 * reports platform/arch, the agent version the build would bundle, the exact
 * missing path, and the one-command fix, and exits non-zero so `npm run
 * pack|dist*` fails fast instead of inside electron-builder.
 *
 * Direct (developer) builds embed the agent and therefore REQUIRE the binary.
 * The Mac App Store build (electron-builder.mas.cjs) is client-only and
 * deliberately excludes it — pass --client-only for those lanes, or run the
 * preflight only from the direct lane scripts.
 *
 * Usage: node scripts/preflight-pack.mjs [--client-only]
 */

import { existsSync, readFileSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const electronRoot = join(here, "..");
const repoRoot = join(electronRoot, "..");

const clientOnly = process.argv.includes("--client-only");
const platformArg = process.argv.find((arg) => arg.startsWith("--platform="));
const archArg = process.argv.find((arg) => arg.startsWith("--arch="));
const targetPlatform = platformArg ? platformArg.slice("--platform=".length) : process.platform;
const targetArch = archArg ? archArg.slice("--arch=".length) : process.arch;
const versions = JSON.parse(readFileSync(join(repoRoot, "versions.json"), "utf8"));
const pkg = JSON.parse(readFileSync(join(electronRoot, "package.json"), "utf8"));
const agentVersion = process.env.YAVER_AGENT_VERSION || versions.cli || versions.agent || "unknown";

const binaryName = targetPlatform === "win32" ? "yaver.exe" : "yaver";
const binPath = join(electronRoot, "resources", "bin", binaryName);
let hasBinary = false;
let binSize = null;
try {
  if (existsSync(binPath) && statSync(binPath).isFile()) {
    hasBinary = true;
    binSize = statSync(binPath).size;
  }
} catch {
  hasBinary = false;
}

const report = {
  platform: targetPlatform,
  arch: targetArch,
  guiVersion: pkg.version,
  agentVersion,
  bundledBinary: binPath,
  bundledBinaryPresent: hasBinary,
  bundledBinaryBytes: binSize,
  mode: clientOnly ? "client-only (Mac App Store)" : "direct (embedded agent)",
};

console.log(JSON.stringify(report, null, 2));

if (clientOnly) {
  console.log("[preflight] client-only build — embedded agent intentionally excluded.");
  process.exit(0);
}

if (hasBinary) {
  console.log(`[preflight] embedded agent present (${binSize} bytes).`);
  process.exit(0);
}

console.error(
  `\n[preflight] FAIL: the direct GUI build embeds the yaver agent, but the binary is missing.\n` +
  `  expected: ${binPath}\n` +
  `  agent version to bundle: ${agentVersion} (from versions.json "cli", override with YAVER_AGENT_VERSION)\n` +
  `\nFix (run from electron/):\n` +
  `  node scripts/fetch-agent-binary.mjs\n` +
  `\nIf this is a Mac App Store (client-only) build, pass --client-only to this preflight.`,
);
process.exit(1);
