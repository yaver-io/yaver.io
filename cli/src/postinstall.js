#!/usr/bin/env node
// npm postinstall hook — prefetch the Yaver agent and provision the
// Yaver-managed Hermes reload stack on global installs so a fresh
// Linux/WSL/macOS box can get to headless auth + Open in Yaver
// without an extra `yaver install mobile` step.
//
// Must NEVER fail npm install. This is best-effort bootstrap only.

const { ensureAgentBinary, runAgentCommand } = require("./agent-runtime");
const { execSync } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

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

// Make sure `yaver` resolves on PATH for the next shell session. npm's
// global prefix (e.g. ~/.npm-global/bin) is not on PATH by default on
// most Linux distros — so `npm install -g yaver-cli` succeeds but
// `yaver auth` then exits with command-not-found. That is the #1 fail
// mode on fresh machines, so we self-heal by appending a one-line,
// idempotent PATH export into the user's shell rc files. Never throws.
function ensurePathOnUnix() {
  try {
    if (process.platform === "win32") return; // Windows users have Scoop/Winget
    const prefix = (process.env.npm_config_prefix || "").trim();
    if (!prefix) return;
    const binDir = path.join(prefix, "bin");
    if (!fs.existsSync(path.join(binDir, "yaver"))) return;

    const currentPath = (process.env.PATH || "").split(path.delimiter);
    if (currentPath.includes(binDir)) return;

    // Also confirm `yaver` isn't already findable under a different name
    // (e.g. brew-installed /opt/homebrew/bin/yaver takes precedence).
    try {
      const found = execSync("command -v yaver", { stdio: ["ignore", "pipe", "ignore"] })
        .toString()
        .trim();
      if (found) return;
    } catch (_) {
      // `command -v` returns nonzero when not found — fall through and patch PATH.
    }

    const home = os.homedir();
    const rcFiles = [".bashrc", ".zshrc", ".profile"]
      .map((f) => path.join(home, f))
      .filter((p) => fs.existsSync(p));

    const marker = "# yaver-cli PATH";
    const line = `case \":$PATH:\" in *\":${binDir}:\"*) ;; *) export PATH=\"${binDir}:$PATH\" ;; esac`;
    const block = `\n${marker} (added by yaver-cli postinstall)\n${line}\n`;

    let patched = false;
    for (const rc of rcFiles) {
      const content = fs.readFileSync(rc, "utf8");
      if (content.includes(marker)) continue;
      fs.appendFileSync(rc, block);
      patched = true;
    }
    if (patched) {
      log(`Added ${binDir} to PATH in shell rc files. Run 'exec $SHELL -l' or open a new terminal.`);
    }
  } catch (err) {
    // Best-effort only.
  }
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

  ensurePathOnUnix();

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
