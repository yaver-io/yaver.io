"use strict";

/**
 * Desktop app uninstall planning for the Yaver GUI.
 *
 * The renderer (the dashboard's Settings tab) can ASK to uninstall, but it can
 * never supply a path, an executable, or a flag: like the connectivity doctor,
 * every action here is derived from the running process (its own execPath /
 * APPIMAGE / distribution channel), so a compromised remote dashboard cannot
 * turn "uninstall" into an arbitrary-file delete.
 *
 * Why this exists: the GUI had no in-app uninstall, so a Windows/Mac user had
 * to find the installer in Programs & Features / drag the .app to the Trash,
 * and a Linux AppImage user had to guess where the file lives. Electron's own
 * `app.getPath("exe")` and `process.env.APPIMAGE` are the only trustworthy
 * sources for "what did this process actually run from".
 *
 * Each platform has an honest route:
 *   - macOS direct: move `Yaver.app` to the Trash (reversible, no shell-out).
 *   - Windows direct (NSIS): run the registered `Uninstall Yaver.exe`.
 *   - Linux AppImage: Trash the single AppImage file.
 *   - deb/rpm, Mac App Store, Microsoft Store: owned by the OS / package
 *     manager / Store; we NAME the route instead of pretending we can do it.
 *
 * Uninstalling removes the app; it never touches `~/.yaver` (the agent's data,
 * shared with the CLI) or any project checkout. Those are separate, and
 * deleting them silently would be the destructive surprise this module exists
 * to avoid.
 */

const path = require("node:path");
const fs = require("node:fs");

const WINDOWS_UNINSTALLER_NAMES = ["Uninstall Yaver.exe", "Uninstall.exe"];

function pathExists(candidate, exists) {
  try {
    return exists(candidate);
  } catch {
    return false;
  }
}

/**
 * Build the uninstall plan for the current process.
 *
 * Returns one of:
 *   { supported: true,  action: "trash",            target, detail }
 *   { supported: true,  action: "run-uninstaller",  target, installDir, detail }
 *   { supported: false, action: "store"|"package-manager"|"manual", detail }
 */
function uninstallPlan({
  platform = process.platform,
  execPath = process.execPath,
  appImage = process.env.APPIMAGE,
  storeManaged = false,
  storeName = "the Store",
  exists = fs.existsSync,
} = {}) {
  if (storeManaged) {
    return {
      supported: false,
      action: "store",
      detail: `This build was installed by ${storeName}. Remove it from ${storeName} (or your operating system's installed-apps list); Yaver cannot uninstall a Store-managed package from inside itself.`,
    };
  }

  if (platform === "darwin") {
    // execPath: /Applications/Yaver.app/Contents/MacOS/Yaver → three levels up.
    const bundle = path.resolve(execPath, "..", "..", "..");
    if (!bundle.endsWith(".app") || !pathExists(bundle, exists)) {
      return {
        supported: false,
        action: "manual",
        detail: "Yaver could not identify its own application bundle. Drag Yaver.app to the Trash from Finder.",
      };
    }
    return {
      supported: true,
      action: "trash",
      target: bundle,
      detail: "Yaver.app will be moved to the Trash (recoverable until you empty it). Your ~/.yaver agent data is left in place.",
    };
  }

  if (platform === "win32") {
    // path.win32 regardless of the host we're running on, so a macOS/Linux
    // build (or a unit test) still resolves Windows separators correctly.
    const installDir = path.win32.dirname(execPath);
    const uninstaller = WINDOWS_UNINSTALLER_NAMES
      .map((name) => path.win32.join(installDir, name))
      .find((candidate) => pathExists(candidate, exists));
    if (!uninstaller) {
      return {
        supported: false,
        action: "package-manager",
        detail: "The Windows uninstaller could not be located next to the app. Remove Yaver from Settings → Apps → Installed apps.",
      };
    }
    return {
      supported: true,
      action: "run-uninstaller",
      target: uninstaller,
      installDir,
      detail: "Windows will run Yaver's uninstaller. Your ~/.yaver agent data is left in place.",
    };
  }

  if (platform === "linux") {
    if (appImage) {
      return {
        supported: true,
        action: "trash",
        target: appImage,
        detail: "The AppImage file will be moved to the Trash. Your ~/.yaver agent data is left in place.",
      };
    }
    return {
      supported: false,
      action: "package-manager",
      detail: "This is a deb/rpm install. Remove it with your package manager (for example `sudo apt remove yaver` or `sudo rpm -e yaver`).",
    };
  }

  return {
    supported: false,
    action: "manual",
    detail: `Automatic uninstall is not implemented for ${platform}. Remove the app with your operating system's tools.`,
  };
}

/**
 * Run a plan that does not need Electron. `trash`/`run-uninstaller` are passed
 * in so main.js owns the Electron `shell`/`spawn` calls and this stays testable.
 * Returns { ok, error? }.
 */
async function executeUninstallPlan(plan, { trash, runDetached } = {}) {
  if (!plan || !plan.supported) {
    return { ok: false, error: plan?.detail || "This build cannot be uninstalled from inside the app." };
  }
  if (plan.action === "trash") {
    if (typeof trash !== "function") return { ok: false, error: "Trash is unavailable." };
    try {
      await trash(plan.target);
      return { ok: true };
    } catch (error) {
      return { ok: false, error: error?.message || "Could not move the app to the Trash." };
    }
  }
  if (plan.action === "run-uninstaller") {
    if (typeof runDetached !== "function") return { ok: false, error: "The uninstaller could not be launched." };
    const launched = await runDetached(plan.target, []);
    return launched?.ok ? { ok: true } : { ok: false, error: launched?.error || "The uninstaller could not be launched." };
  }
  return { ok: false, error: plan.detail || "Unsupported uninstall action." };
}

module.exports = {
  WINDOWS_UNINSTALLER_NAMES,
  executeUninstallPlan,
  uninstallPlan,
};
