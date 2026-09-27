"use strict";

const os = require("node:os");

/**
 * Non-secret desktop runtime settings shared by main-process wiring and tests.
 *
 * A GUI node is useful as a remote box only while the process remains alive,
 * but availability settings are opt-in. Microsoft Store policy requires user
 * consent before an app changes Windows startup/preferences; silently adding
 * Yaver at login on first launch would fail that boundary even though the
 * control was later reversible from the tray. We never mutate the OS power
 * plan and never require administrator privileges.
 */
const DEFAULT_SETTINGS = Object.freeze({
  taskNotifications: true,
  keepAwake: false,
  launchAtLogin: false,
  automaticUpdates: true,
});

function normalizeSettings(value) {
  const raw = value && typeof value === "object" ? value : {};
  return {
    taskNotifications: typeof raw.taskNotifications === "boolean"
      ? raw.taskNotifications
      : DEFAULT_SETTINGS.taskNotifications,
    keepAwake: typeof raw.keepAwake === "boolean"
      ? raw.keepAwake
      : DEFAULT_SETTINGS.keepAwake,
    launchAtLogin: typeof raw.launchAtLogin === "boolean"
      ? raw.launchAtLogin
      : DEFAULT_SETTINGS.launchAtLogin,
    automaticUpdates: typeof raw.automaticUpdates === "boolean"
      ? raw.automaticUpdates
      : DEFAULT_SETTINGS.automaticUpdates,
  };
}

function isLoginItemSupported(platform = process.platform) {
  return platform === "win32" || platform === "darwin" || platform === "linux";
}

function linuxAutostartEntry(executable) {
  const escaped = String(executable || "").replace(/([\\"`$])/g, "\\$1");
  return [
    "[Desktop Entry]",
    "Type=Application",
    "Name=Yaver",
    "Comment=Keep this computer available as a Yaver development node",
    `Exec=\"${escaped}\" --hidden`,
    "Terminal=false",
    "X-GNOME-Autostart-enabled=true",
    "",
  ].join("\n");
}

/**
 * macOS 26 tightened MAP_JIT handling for App-Sandboxed Apple-silicon
 * processes. Electron's MAS renderer can consequently die with exit code 5
 * before drawing its first frame even though the bundle and helper signatures
 * are valid. The same TestFlight binary operation-probed on 2026-09-05 stays
 * alive when V8 receives --jitless. Keep the workaround narrower than the
 * distribution that needs it; direct/notarized builds and older macOS releases
 * retain normal JIT performance.
 */
function needsMasJitlessWorkaround({
  isMas = process.mas === true,
  platform = process.platform,
  arch = process.arch,
  release = os.release(),
} = {}) {
  const darwinMajor = Number.parseInt(String(release).split(".")[0], 10);
  return isMas === true
    && platform === "darwin"
    && arch === "arm64"
    && Number.isFinite(darwinMajor)
    && darwinMajor >= 25;
}

module.exports = {
  DEFAULT_SETTINGS,
  normalizeSettings,
  isLoginItemSupported,
  linuxAutostartEntry,
  needsMasJitlessWorkaround,
};
