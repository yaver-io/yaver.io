#!/usr/bin/env node
// npm postinstall hook — prefetch the Yaver agent and provision the
// Yaver-managed Hermes reload stack on global installs so a fresh
// Linux/WSL/macOS box can get to headless auth + Open in Yaver
// without an extra `yaver install mobile` step.
//
// Must NEVER fail npm install. This is best-effort bootstrap only.

const { ensureAgentBinary, runAgentCommand } = require("./agent-runtime");

function envEnabled(name) {
  const raw = String(process.env[name] || "").trim().toLowerCase();
  return raw === "1" || raw === "true" || raw === "yes";
}

function isGlobalInstall() {
  if (envEnabled("YAVER_FORCE_POSTINSTALL_BOOTSTRAP")) {
    return true;
  }
  return String(process.env.npm_config_global || "").trim().toLowerCase() === "true";
}

function log(message) {
  console.error(`[yaver postinstall] ${message}`);
}

async function main() {
  if (envEnabled("YAVER_SKIP_POSTINSTALL") || envEnabled("YAVER_SKIP_POSTINSTALL_BOOTSTRAP")) {
    return;
  }
  if (!isGlobalInstall()) {
    return;
  }

  try {
    await ensureAgentBinary({ quiet: true });
  } catch (error) {
    log(`Skipping agent prefetch: ${error.message}`);
    return;
  }

  if (process.platform !== "linux" && process.platform !== "darwin") {
    return;
  }
  if (envEnabled("YAVER_SKIP_POSTINSTALL_MOBILE")) {
    return;
  }

  try {
    await runAgentCommand(["install", "mobile"], { quiet: true });
    log("Provisioned Hermes reload stack for Yaver mobile.");
  } catch (error) {
    log(`Skipping mobile bootstrap: ${error.message}`);
  }
}

main()
  .then(() => process.exit(0))
  .catch(() => process.exit(0));
