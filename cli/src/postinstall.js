#!/usr/bin/env node
// npm postinstall hook — prefetch the Yaver agent and provision the
// Yaver-managed Hermes reload stack on global installs so a fresh
// Linux/WSL/macOS box can get to headless auth + Open in Yaver
// without an extra `yaver install mobile` step.
//
// Must NEVER fail npm install. This is best-effort bootstrap only.

const { ensureAgentBinary, runAgentCommand } = require("./agent-runtime");
const { ensureHermesc } = require("./hermesc-runtime");
const { execSync } = require("child_process");
const fs = require("fs");
const os = require("os");
const path = require("path");

const CODING_RUNNER_BOOTSTRAP = [
  { command: "claude", pkg: "@anthropic-ai/claude-code", label: "Claude Code" },
  { command: "codex", pkg: "@openai/codex", label: "OpenAI Codex" },
  { command: "opencode", pkg: "opencode-ai", label: "OpenCode" },
];

const MOBILE_TOOL_BOOTSTRAP = [
  { command: "expo", pkg: "expo", label: "Expo CLI" },
  { command: "eas", pkg: "eas-cli", label: "EAS CLI" },
];

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

function commandExists(name) {
  try {
    execSync(`command -v ${name}`, { stdio: ["ignore", "pipe", "ignore"] });
    return true;
  } catch (_) {
    return false;
  }
}

function npmGlobalBinDir() {
  const prefix = (process.env.npm_config_prefix || "").trim();
  if (!prefix) return "";
  return path.join(prefix, "bin");
}

function addNpmGlobalBinToProcessPath() {
  const binDir = npmGlobalBinDir();
  if (!binDir) return;
  const current = (process.env.PATH || "").split(path.delimiter);
  if (!current.includes(binDir)) {
    process.env.PATH = `${binDir}${path.delimiter}${process.env.PATH || ""}`;
  }
}

function installMissingCodingRunners() {
  const missing = CODING_RUNNER_BOOTSTRAP.filter((entry) => !commandExists(entry.command));
  if (missing.length === 0) {
    log("Claude Code, Codex, and OpenCode already exist on PATH.");
    return;
  }

  const npmCmd = (process.env.npm_execpath || "npm").trim() || "npm";
  const packages = missing.map((entry) => entry.pkg);
  const labels = missing.map((entry) => entry.label).join(", ");
  try {
    execSync(
      `"${npmCmd}" install -g --no-fund --no-audit ${packages.join(" ")}`,
      { stdio: "inherit" },
    );
    addNpmGlobalBinToProcessPath();
    log(`Installed missing coding runners: ${labels}.`);
  } catch (error) {
    log(`Skipping coding runner bootstrap: ${error.message}`);
  }
}

async function setupMCPForInstalledRunners() {
  const targets = [
    { command: "claude", client: "claude-code", label: "Claude Code" },
    { command: "codex", client: "codex", label: "Codex" },
    { command: "opencode", client: "opencode", label: "OpenCode" },
  ];
  const configured = [];
  for (const target of targets) {
    if (!commandExists(target.command)) continue;
    try {
      await runAgentCommand(["mcp", "setup", target.client], { quiet: true });
      configured.push(target.label);
    } catch (error) {
      log(`Skipping MCP setup for ${target.label}: ${error.message}`);
    }
  }
  if (configured.length > 0) {
    log(`Registered Yaver MCP in: ${configured.join(", ")}.`);
  }
}

// ensureLinuxHermescBuildDeps installs the apt packages required to
// compile hermesc from facebook/hermes sources on linux/arm64 (no
// upstream prebuilt exists for that arch). Best-effort; only runs as
// root. Mirrors ci/remote/install-hermesc.sh's dep list so the
// outcome matches the Hetzner test-box bootstrap path.
function ensureLinuxHermescBuildDeps() {
  try {
    if (process.platform !== "linux") return;
    if (typeof process.geteuid !== "function" || process.geteuid() !== 0) {
      log("linux/arm64 hermesc build needs cmake/ninja/clang/libicu-dev — re-run `npm install -g yaver-cli` as root to provision them, or install manually.");
      return;
    }
    const required = ["git", "cmake", "ninja-build", "clang", "python3", "build-essential", "ca-certificates", "libicu-dev", "zlib1g-dev"];
    if (!commandExists("apt-get")) {
      log(`hermesc build deps missing (${required.join(", ")}) and no apt-get available. Install manually then re-run \`yaver install mobile\`.`);
      return;
    }
    execSync("apt-get update -y", { stdio: ["ignore", "ignore", "ignore"] });
    execSync(`DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ${required.join(" ")}`, {
      stdio: "inherit",
    });
    log("Installed Linux hermesc build deps (cmake/ninja/clang/libicu-dev).");
  } catch (error) {
    log(`Skipping hermesc build deps install: ${error.message}`);
  }
}

function ensureLinuxRunnerSandboxPackages() {
  try {
    if (process.platform !== "linux") return;
    if (typeof process.geteuid !== "function" || process.geteuid() !== 0) return;
    const missing = [];
    if (!commandExists("bwrap")) missing.push("bubblewrap");
    if (!commandExists("newuidmap")) missing.push("uidmap");
    // tmux is a core Yaver dependency — /spatial 3-pane terminal layout
    // attaches to it via /ws/terminal, and the mobile Terminal tab uses
    // it the same way. Without tmux, the trio user (Cagri-style: Linux
    // remote dev + phone + glasses + keyboard) sees empty panes until
    // they ssh in and `apt install tmux`. Bundling tmux into the same
    // auto-install pass as the runner sandbox deps closes that gap.
    if (!commandExists("tmux")) missing.push("tmux");
    if (missing.length === 0) return;
    if (!commandExists("apt-get")) {
      log(`Linux packages missing (${missing.join(", ")}) and no apt-get is available for auto-install. Run: \`yaver install tmux\` (or your distro's equivalent for the rest).`);
      return;
    }
    execSync("apt-get update -y", { stdio: ["ignore", "ignore", "ignore"] });
    execSync(`DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ${missing.join(" ")}`, {
      stdio: "inherit",
    });
    log(`Installed Linux packages: ${missing.join(", ")}.`);
  } catch (error) {
    log(`Skipping Linux package bootstrap: ${error.message}`);
  }
}

function ensureLinuxRunnerSandboxSupport() {
  try {
    if (process.platform !== "linux") return;
    if (typeof process.geteuid !== "function" || process.geteuid() !== 0) return;
    const confPath = "/etc/sysctl.d/99-yaver-runner-sandbox.conf";
    let body = "kernel.unprivileged_userns_clone=1\nuser.max_user_namespaces=1048576\n";
    if (fs.existsSync("/proc/sys/kernel/apparmor_restrict_unprivileged_userns")) {
      body += "kernel.apparmor_restrict_unprivileged_userns=0\n";
    }
    fs.writeFileSync(confPath, body);
    execSync("sysctl --system", { stdio: ["ignore", "ignore", "ignore"] });
    log("Enabled Linux user-namespace prerequisites for Codex/runner sandboxes.");
  } catch (error) {
    log(`Skipping Linux runner sandbox bootstrap: ${error.message}`);
  }
}

function reportLinuxRunnerSandboxStatus() {
  try {
    if (process.platform !== "linux") return;
    const issues = [];
    if (!commandExists("bwrap")) issues.push("bubblewrap");
    if (!commandExists("newuidmap")) issues.push("uidmap");
    try {
      const userns = fs.readFileSync("/proc/sys/kernel/unprivileged_userns_clone", "utf8").trim();
      if (userns === "0") issues.push("kernel.unprivileged_userns_clone=0");
    } catch (_) {}
    try {
      const maxUserns = fs.readFileSync("/proc/sys/user/max_user_namespaces", "utf8").trim();
      if (!maxUserns || maxUserns === "0") issues.push("user.max_user_namespaces=0");
    } catch (_) {}
    try {
      const apparmor = fs.readFileSync("/proc/sys/kernel/apparmor_restrict_unprivileged_userns", "utf8").trim();
      if (apparmor === "1") issues.push("kernel.apparmor_restrict_unprivileged_userns=1");
    } catch (_) {}
    if (issues.length > 0) {
      log(`Linux runner sandbox still has blockers: ${issues.join(", ")}. Yaver will mark Codex blocked until the host allows it.`);
    }
  } catch (_) {
    // Best-effort only.
  }
}

function installMissingMobileTools() {
  const missing = MOBILE_TOOL_BOOTSTRAP.filter((entry) => !commandExists(entry.command));
  if (missing.length === 0) {
    log("Expo and EAS CLIs already exist on PATH.");
    return;
  }

  const npmCmd = (process.env.npm_execpath || "npm").trim() || "npm";
  const packages = missing.map((entry) => entry.pkg);
  const labels = missing.map((entry) => entry.label).join(", ");
  try {
    execSync(
      `"${npmCmd}" install -g --no-fund --no-audit ${packages.join(" ")}`,
      { stdio: "inherit" },
    );
    log(`Installed missing mobile tools: ${labels}.`);
  } catch (error) {
    log(`Skipping mobile tool bootstrap: ${error.message}`);
  }
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

// Belt-and-braces companion to ensurePathOnUnix: write the same PATH
// rescue into ~/.zshenv. .zshrc patches break when the rc fails to
// fully load — e.g. one user had a hanging `gcloud completion.zsh.inc`
// `source` line whose interrupt aborted the rc before the appended
// yaver PATH block ever ran, so `yaver` was "command not found" in
// every new terminal even though the install succeeded. .zshenv loads
// on every zsh shell before .zshrc and is unaffected by .zshrc errors,
// so it survives that failure mode. Best-effort, never throws.
function ensureZshenvRescue() {
  try {
    if (process.platform === "win32") return;
    const prefix = (process.env.npm_config_prefix || "").trim();
    if (!prefix) return;
    const binDir = path.join(prefix, "bin");
    if (!fs.existsSync(path.join(binDir, "yaver"))) return;

    const home = os.homedir();
    if (!home) return;

    // Only touch .zshenv when the user actually uses zsh; otherwise we'd
    // be creating a dotfile they never asked for.
    const shell = String(process.env.SHELL || "");
    const usesZsh =
      shell.endsWith("/zsh") ||
      fs.existsSync(path.join(home, ".zshrc")) ||
      fs.existsSync(path.join(home, ".zshenv"));
    if (!usesZsh) return;

    const zshenv = path.join(home, ".zshenv");
    const marker = "# yaver-cli zshenv rescue";
    const yaverNodeBin = path.join(home, ".yaver", "runtimes", "node", "bin");
    const dirs = [yaverNodeBin, binDir];
    const dirsLiteral = dirs.map((d) => `"${d}"`).join(" ");
    const block =
      `\n${marker} (added by yaver-cli postinstall)\n` +
      "# Loads on every zsh shell before .zshrc, so yaver stays on PATH\n" +
      "# even if .zshrc fails to fully load.\n" +
      `for __yaver_d in ${dirsLiteral}; do\n` +
      "  case \":$PATH:\" in\n" +
      "    *\":$__yaver_d:\"*) ;;\n" +
      "    *) export PATH=\"$__yaver_d:$PATH\" ;;\n" +
      "  esac\n" +
      "done\n" +
      "unset __yaver_d\n";

    let existing = "";
    if (fs.existsSync(zshenv)) {
      existing = fs.readFileSync(zshenv, "utf8");
      if (existing.includes(marker)) return;
    }
    fs.appendFileSync(zshenv, block);
    log(`Patched ~/.zshenv with PATH rescue (yaver + bundled node).`);
  } catch (_) {
    // Best-effort only.
  }
}

// Update ~/.yaver/bin/current → versioned dir for the binary just
// installed. Without this, the symlink can lag behind npm — every time
// I bumped CLI on the user's Mac mini, `~/.yaver/bin/current` kept
// pointing at an older version, so `~/.yaver/bin/current/<arch>/yaver`
// silently ran the wrong binary even though `npm install -g yaver-cli@
// <new>` had succeeded. The auto-update watchdogs (heartbeat_watcher
// systemctl/launchctl kickstart paths) trust `current` too — so a
// stale symlink keeps the old binary running across a "restart"
// without anyone noticing. This belongs in postinstall, runs every
// global install, never throws.
function refreshCurrentSymlink(binaryPath) {
  try {
    if (process.platform === "win32") return;
    if (!binaryPath) return;
    // binaryPath is like ~/.yaver/bin/<version>/<cacheKey>/yaver — walk
    // up two levels to the version dir, point `current` at it.
    const versionDir = path.dirname(path.dirname(binaryPath));
    if (!versionDir.startsWith(path.join(os.homedir(), ".yaver", "bin") + path.sep)) {
      return;
    }
    if (!fs.existsSync(versionDir)) return;
    const symlink = path.join(os.homedir(), ".yaver", "bin", "current");
    let already = "";
    try { already = fs.readlinkSync(symlink); } catch (_) {}
    if (already === versionDir) return;
    try { fs.unlinkSync(symlink); } catch (_) {}
    fs.symlinkSync(versionDir, symlink);
    log(`Repointed ~/.yaver/bin/current → ${path.basename(versionDir)}`);
  } catch (err) {
    log(`Skipping current symlink refresh: ${err.message}`);
  }
}

// Restart whichever service supervisor the user has registered. Without
// this, a fresh `npm install -g yaver-cli@latest` updates the binary on
// disk but leaves the still-running agent process on the OLD binary
// until the user manually `yaver restart`s. Every supervisor variant
// is safe-to-call when not present — failures just no-op so we never
// block npm install.
function bounceRunningAgent() {
  if (process.platform === "win32") return;
  try {
    if (process.platform === "darwin") {
      // Per-user LaunchAgent — kickstart -k restarts in-place. We don't
      // touch LaunchDaemon (system-wide) — that requires sudo and is
      // out of scope for an unprivileged npm postinstall.
      execSync(
        `launchctl print "gui/$(id -u)/io.yaver.agent" >/dev/null 2>&1 && ` +
          `launchctl kickstart -k "gui/$(id -u)/io.yaver.agent"`,
        { stdio: "ignore", shell: "/bin/sh" },
      );
      log("Bounced launchd LaunchAgent so the new binary is live.");
      return;
    }
    // Linux: try systemd user unit first, then system unit.
    try {
      execSync(
        `systemctl --user is-active yaver >/dev/null 2>&1 && systemctl --user restart yaver`,
        { stdio: "ignore", shell: "/bin/sh" },
      );
      log("Restarted yaver.service (systemd user unit).");
      return;
    } catch (_) {}
    try {
      execSync(
        `systemctl is-active yaver-agent >/dev/null 2>&1 && systemctl restart yaver-agent`,
        { stdio: "ignore", shell: "/bin/sh" },
      );
      log("Restarted yaver-agent.service (systemd system unit).");
      return;
    } catch (_) {}
  } catch (err) {
    // Best-effort.
  }
}

async function main() {
  if (envEnabled("YAVER_SKIP_POSTINSTALL") || envEnabled("YAVER_SKIP_POSTINSTALL_BOOTSTRAP")) {
    return;
  }
  if (!isGlobalInstall()) {
    return;
  }

  let installedBinary = null;
  try {
    installedBinary = await ensureAgentBinary({ quiet: true });
  } catch (error) {
    log(`Skipping agent prefetch: ${error.message}`);
    return;
  }
  // Repoint the canonical `current` symlink + bounce any registered
  // service so a fresh `npm install -g yaver-cli@latest` actually
  // takes effect without a manual `yaver restart`.
  refreshCurrentSymlink(installedBinary);
  bounceRunningAgent();

  // Platform-aware hermesc provisioning. Downloads the binary matching
  // this host from the react-native npm tarball, caches it under the
  // CLI install dir + ~/.yaver so `yaver-push` works offline after.
  // Never blocks install — the bundler falls back to project-local RN
  // if this step skipped or failed.
  try {
    let hermescPath = await ensureHermesc({ quiet: true });
    // linux/arm64 (and any other platform with no upstream prebuilt)
    // falls through to build-from-source. Without this branch the
    // first `yaver-push` on the box pays a 1–2 min CMake bill — bad UX
    // on a fresh Hetzner ARM dev box. Install the toolchain (apt only,
    // when running as root) and try the source build now so push is
    // instant later. Never blocks npm install on failure.
    if (!hermescPath && process.platform === "linux" && process.arch === "arm64") {
      ensureLinuxHermescBuildDeps();
      try {
        hermescPath = await ensureHermesc({ quiet: true, allowBuildFromSource: true });
      } catch (buildErr) {
        log(`Source build of hermesc failed: ${buildErr.message}`);
      }
    }
    if (hermescPath) {
      log(`Hermes compiler ready at ${hermescPath}.`);
    } else {
      log(`Hermes compiler not pre-provisioned for ${process.platform}-${process.arch}; bundler will resolve project-local hermesc at push time.`);
    }
  } catch (error) {
    log(`Skipping hermesc install: ${error.message}`);
  }

  ensurePathOnUnix();
  ensureZshenvRescue();
  addNpmGlobalBinToProcessPath();
  ensureLinuxRunnerSandboxPackages();
  ensureLinuxRunnerSandboxSupport();
  reportLinuxRunnerSandboxStatus();

  if (process.platform !== "linux" && process.platform !== "darwin") {
    return;
  }
  if (envEnabled("YAVER_SKIP_POSTINSTALL_MOBILE")) {
    if (!envEnabled("YAVER_SKIP_POSTINSTALL_RUNNERS")) {
      installMissingCodingRunners();
      await setupMCPForInstalledRunners();
    }
    return;
  }

  try {
    await runAgentCommand(["install", "mobile"], { quiet: true });
    log("Provisioned Hermes reload stack for Yaver mobile.");
  } catch (error) {
    log(`Skipping mobile bootstrap: ${error.message}`);
  }

  if (!envEnabled("YAVER_SKIP_POSTINSTALL_REMOTE_RUNTIME")) {
    try {
      await runAgentCommand(["install", "remote-runtime"], { quiet: true });
      log("Provisioned native remote-runtime host tools (Android everywhere; macOS host helpers where supported).");
    } catch (error) {
      log(`Skipping remote-runtime bootstrap: ${error.message}`);
    }
  }

  if (!envEnabled("YAVER_SKIP_POSTINSTALL_MOBILE_TOOLS")) {
    installMissingMobileTools();
  }

  if (!envEnabled("YAVER_SKIP_POSTINSTALL_RUNNERS")) {
    installMissingCodingRunners();
    await setupMCPForInstalledRunners();
  }

  // Vibe Preview tool stack — best-effort provisioning so a fresh
  // global npm install gives the user a working chromium-based frame
  // capture + maestro-driven clip exercises out of the box. Opt out
  // with YAVER_SKIP_POSTINSTALL_VIBE_PREVIEW=1.
  if (!envEnabled("YAVER_SKIP_POSTINSTALL_VIBE_PREVIEW")) {
    try {
      await runAgentCommand(["install", "vibe-preview"], { quiet: true });
      log("Provisioned Vibe Preview tool stack (chromium + ffmpeg + maestro + appium + adb).");
    } catch (error) {
      log(`Skipping vibe-preview bootstrap: ${error.message}`);
    }
  }

  // Free/offline voice stack — provision ffmpeg + whisper.cpp + a ggml
  // model so `yaver voice test` / `voice listen` (provider=local) work out
  // of the box with no API key and no cost. Best-effort; cloud STT
  // (Deepgram/OpenAI) needs none of this. Opt out with
  // YAVER_SKIP_POSTINSTALL_VOICE=1.
  if (!envEnabled("YAVER_SKIP_POSTINSTALL_VOICE")) {
    try {
      await runAgentCommand(["voice", "deps", "--install", "--quiet"], { quiet: true });
      log("Provisioned free voice stack (ffmpeg + whisper.cpp + model).");
    } catch (error) {
      log(`Skipping voice deps bootstrap: ${error.message}`);
    }
  }
}

main()
  .then(() => process.exit(0))
  .catch(() => process.exit(0));
