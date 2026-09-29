"use strict";

/**
 * Yaver GUI — main process.
 *
 * A hardened desktop shell around the Yaver web dashboard. Design rules from
 * docs/audits/webui-chat-vibing-gui-2026-08-12.md:
 *
 *  1. The GUI is a shell, not a fork — it loads the real dashboard
 *     (https://yaver.io/dashboard, localhost:3000 in dev) so chat + vibing
 *     always match the deployed web app.
 *  2. Fixes the web-only "token in SSE URL" finding in the shell, where
 *     EventSource cannot be fixed: intercept requests, strip ?token=/?__rp=
 *     from stream URLs, re-inject as Authorization / X-Relay-Password
 *     headers (the agent's CORS allowlist already accepts both — see
 *     desktop/agent/httpserver.go:3231).
 *  3. Hardened window: contextIsolation, sandbox, nodeIntegration off,
 *     navigation allowlist, external links to the system browser.
 *  4. Native value the web cannot provide: tray with task-notification
 *     toggle, yaver:// deep links, task-completion notifications, and a
 *     window that survives relay flapping.
 */

const { app, BrowserWindow, Tray, Menu, Notification, ipcMain, shell, session, nativeImage, powerSaveBlocker, powerMonitor, dialog } = require("electron");
const path = require("node:path");
const fs = require("node:fs");
const http = require("node:http");
const net = require("node:net");
const { spawn } = require("node:child_process");
const { APP_ORIGINS, isAllowedAppUrl, inPageNavigationDecision } = require("./navigation-policy");
const { AgentManager, probeAgentHealth, resolveAgentBinary } = require("./agent-manager");
const { repairWindowsFirewall, runDesktopConnectivityDiagnostics } = require("./desktop-connectivity-doctor");
const {
  normalizeSettings,
  isLoginItemSupported,
  linuxAutostartEntry,
  needsMasJitlessWorkaround,
} = require("./desktop-runtime-policy");
const { stripAuthFromUrl, applyKnownAuthHeaders } = require("./auth-interceptor");
const { uninstallPlan, executeUninstallPlan } = require("./desktop-uninstall");
const { DesktopLog } = require("./desktop-log");
const {
  MAX_TRANSIENT_LOAD_RETRIES,
  rendererLoadRetryDelay,
  shouldRetryRendererLoad,
} = require("./renderer-recovery-policy");

// The development runtime's bundle is named Electron.app. Override its
// application-facing name before ready so Dock/taskbar hover, the application
// menu, dialogs and crash UI say Yaver. Packaged builds also pin productName in
// both builder configurations, but keeping this here prevents the generic
// runtime name from leaking back into local/dev launches.
app.setName("Yaver");

const DASHBOARD_PRODUCTION_URL = "https://yaver.io/dashboard";
const DEV_SERVER_URL = "http://localhost:3000";
// The sandboxed Mac App Store package is client-only. The Microsoft Store
// package is a full-trust packaged desktop app and includes the native Windows
// agent, so it supports client-only use, local-agent use, and both together.
// Both Store channels receive Store-managed updates.
const storeClientOnly = process.mas === true;
const storeManaged = process.mas === true || process.windowsStore === true;
const storeName = process.windowsStore === true ? "Microsoft Store" : "Mac App Store";
const distributionChannel = process.windowsStore === true
  ? "microsoft-store"
  : process.mas === true
    ? "mac-app-store"
    : "direct";

// This must be applied before the first renderer is created. A valid,
// TestFlight-delivered MAS renderer otherwise crashes before first paint on
// Apple-silicon macOS 26 (exit code 5). `--js-flags=--jitless` was
// operation-probed against the installed 0.1.10 build on 2026-09-05: the same
// renderer that crashed repeatedly loaded /dashboard and remained alive.
const masJitlessWorkaround = needsMasJitlessWorkaround({ isMas: storeClientOnly });
if (masJitlessWorkaround) {
  app.commandLine.appendSwitch("js-flags", "--jitless");
}

// Chromium's secure-storage backend uses the macOS login keychain. An
// unpackaged Electron binary has Electron's development identity, not the
// stable signed Yaver identity, so repeated automated launches can trigger an
// OS keychain prompt that no headless driver can answer. Keep production on
// the OS-protected store; only unpackaged automation gets Chromium's mock
// keychain and an isolated profile. Packaged builds cannot opt into this path.
const automationMode = !app.isPackaged && process.env.YAVER_ELECTRON_AUTOMATION === "1";
if (automationMode) {
  app.commandLine.appendSwitch("use-mock-keychain");
  const isolatedUserData = process.env.YAVER_ELECTRON_USER_DATA_DIR;
  if (isolatedUserData && path.isAbsolute(isolatedUserData)) {
    app.setPath("userData", isolatedUserData);
  }
}

const desktopLog = new DesktopLog({ directory: path.join(app.getPath("userData"), "logs") });
desktopLog.write("info", "process_start", `version=${app.getVersion()} platform=${process.platform} arch=${process.arch} packaged=${app.isPackaged} masJitless=${masJitlessWorkaround}`);
process.on("uncaughtExceptionMonitor", (error) => desktopLog.write("error", "uncaught_exception", error?.stack || error?.message || String(error)));
process.on("unhandledRejection", (reason) => desktopLog.write("error", "unhandled_rejection", reason?.stack || reason?.message || String(reason)));

let mainWindow = null;
let tray = null;
let isQuitting = false;
let launchHidden = process.argv.includes("--hidden");
let rendererRecoveryAttempts = 0;
let lastRendererFailure = null;
let rendererLoadRetryTimer = null;
// Deterministic renderer-failure fixture (audit pass-2 DP9): forces the
// black-screen recovery paths so a packaged/headless smoke can assert them
// without a live network or agent. "load" → main-frame load fails;
// "crash" → additionally crash the renderer after the recovery page renders.
// Harmless in production: it only makes the window show the recovery page.
const guiFailureFixture = (process.env.GUI_FAILURE_FIXTURE || "").trim();
/** Embedded yaver Go agent supervisor — makes this desktop a yaver node. */
let agentManager = null;
let agentStatus = storeClientOnly ? "client-only" : "starting";
let agentStatusDetail = storeClientOnly
  ? `${storeName} client: connect to a Yaver node on another machine`
  : null;
let keepAwakeBlockerId = null;
let updateTimer = null;
let directAutoUpdater = null;
let updateStatus = storeManaged
  ? { state: "store-managed", detail: `Updates are managed by the ${storeName}` }
  : { state: "idle", detail: null };

/** Captured auth material, per origin: { token, relayPassword }.
 *  Persisted for the process lifetime only (never written to disk). */
const authByOrigin = new Map();

// Read only the non-secret device identity needed to label this exact node in
// the desktop shell. Never return or log the rest of config.json: it contains
// bearer/relay material. The Go agent uses the same runtime-resolved path.
function localAgentDeviceId() {
  try {
    const override = String(process.env.YAVER_CONFIG_DIR || "").trim();
    const configDir = override && path.isAbsolute(override)
      ? override
      : path.join(app.getPath("home"), ".yaver");
    const parsed = JSON.parse(fs.readFileSync(path.join(configDir, "config.json"), "utf8"));
    return typeof parsed.device_id === "string" ? parsed.device_id.trim().slice(0, 200) : "";
  } catch {
    return "";
  }
}

function localAgentAuthToken() {
  try {
    const override = String(process.env.YAVER_CONFIG_DIR || "").trim();
    const configDir = override && path.isAbsolute(override)
      ? override
      : path.join(app.getPath("home"), ".yaver");
    const parsed = JSON.parse(fs.readFileSync(path.join(configDir, "config.json"), "utf8"));
    return typeof parsed.auth_token === "string" ? parsed.auth_token.trim() : "";
  } catch {
    return "";
  }
}

function localAgentJSON(route, { method = "GET", body } = {}) {
  return new Promise((resolve, reject) => {
    const token = localAgentAuthToken();
    if (!token) {
      reject(new Error("The local agent is not signed in."));
      return;
    }
    const payload = body === undefined ? null : Buffer.from(JSON.stringify(body));
    const req = http.request({
      hostname: "127.0.0.1",
      port: 18080,
      path: route,
      method,
      timeout: 5000,
      headers: {
        Authorization: `Bearer ${token}`,
        ...(payload ? { "Content-Type": "application/json", "Content-Length": String(payload.length) } : {}),
      },
    }, (res) => {
      let raw = "";
      res.setEncoding("utf8");
      res.on("data", (chunk) => { if (raw.length < 256_000) raw += chunk; });
      res.on("end", () => {
        let parsed = {};
        try { parsed = raw ? JSON.parse(raw) : {}; } catch { /* named below */ }
        if ((res.statusCode || 500) >= 400) {
          reject(new Error(parsed.error || `Local agent returned HTTP ${res.statusCode}`));
          return;
        }
        resolve(parsed);
      });
    });
    req.on("timeout", () => req.destroy(new Error("Local agent request timed out.")));
    req.on("error", reject);
    if (payload) req.write(payload);
    req.end();
  });
}

function isTailnetIPv4Host(value) {
  const parts = String(value || "").trim().split(".").map(Number);
  return parts.length === 4 && parts.every((part) => Number.isInteger(part) && part >= 0 && part <= 255) && parts[0] === 100 && parts[1] >= 64 && parts[1] <= 127;
}

function probeTCP(host, port, timeout = 3500) {
  return new Promise((resolve) => {
    const socket = net.createConnection({ host, port });
    let settled = false;
    const finish = (ok, error = "") => {
      if (settled) return;
      settled = true;
      socket.destroy();
      resolve({ ok, error });
    };
    socket.setTimeout(timeout, () => finish(false, "timed out"));
    socket.once("connect", () => finish(true));
    socket.once("error", (error) => finish(false, error?.code || error?.message || "connection failed"));
  });
}

function spawnDetached(file, args) {
  return new Promise((resolve) => {
    const child = spawn(file, args, { detached: true, stdio: "ignore", windowsHide: false });
    child.once("error", (error) => resolve({ ok: false, error: error.message }));
    child.once("spawn", () => {
      child.unref();
      resolve({ ok: true });
    });
  });
}

// ---------------------------------------------------------------------------
// Settings (persisted to userData; never secrets)
// ---------------------------------------------------------------------------

function settingsPath() {
  return path.join(app.getPath("userData"), "settings.json");
}

function loadSettings() {
  try {
    return normalizeSettings(JSON.parse(fs.readFileSync(settingsPath(), "utf8")));
  } catch {
    return normalizeSettings(null);
  }
}

function saveSettings(settings) {
  try {
    fs.writeFileSync(settingsPath(), JSON.stringify(settings, null, 2));
  } catch (err) {
    console.error("[yaver-gui] could not persist settings:", err.message);
    desktopLog.write("error", "settings_write_failed", err.message);
  }
}

let settings = loadSettings();

function publishUpdateStatus(state, detail = null) {
  updateStatus = { state, detail };
  if (tray) rebuildTray();
  if (mainWindow && !mainWindow.isDestroyed()) {
    mainWindow.webContents.send("yaver:update-status", updateStatus);
  }
}

function directUpdaterSupported() {
  if (storeManaged || !app.isPackaged) return false;
  // electron-updater performs in-place Linux updates for AppImage. deb/rpm
  // installations belong to the OS package manager and must not be silently
  // replaced with a second installation format.
  return process.platform !== "linux" || Boolean(process.env.APPIMAGE);
}

function ensureDirectUpdater() {
  if (!directUpdaterSupported()) return null;
  if (directAutoUpdater) return directAutoUpdater;
  // Lazy loading keeps MAS/TestFlight builds from initializing a direct
  // updater inside Apple's sandbox.
  const { autoUpdater } = require("electron-updater");
  directAutoUpdater = autoUpdater;
  autoUpdater.channel = `latest-${process.arch}`;
  autoUpdater.allowPrerelease = false;
  autoUpdater.autoDownload = settings.automaticUpdates;
  autoUpdater.autoInstallOnAppQuit = settings.automaticUpdates;
  autoUpdater.on("checking-for-update", () => publishUpdateStatus("checking", "Checking signed Yaver releases…"));
  autoUpdater.on("update-not-available", () => publishUpdateStatus("current", "Yaver is up to date"));
  autoUpdater.on("update-available", (info) => publishUpdateStatus("available", `Yaver ${info.version} is available`));
  autoUpdater.on("download-progress", (progress) => publishUpdateStatus("downloading", `Downloading ${Math.round(progress.percent || 0)}%`));
  autoUpdater.on("update-downloaded", (info) => {
    publishUpdateStatus("ready", `Yaver ${info.version} will install when the app quits`);
    if (Notification.isSupported()) {
      const notice = new Notification({
        title: "Yaver update ready",
        body: `Version ${info.version} will install when Yaver quits.`,
        icon: path.join(__dirname, "..", "assets", "icon.png"),
      });
      notice.on("click", () => {
        isQuitting = true;
        autoUpdater.quitAndInstall(false, true);
      });
      notice.show();
    }
  });
  autoUpdater.on("error", (err) => {
    desktopLog.write("error", "updater_failed", err?.message || "Update check failed");
    publishUpdateStatus("error", err?.message || "Update check failed");
  });
  return autoUpdater;
}

async function checkForDesktopUpdate({ manual = false } = {}) {
  if (storeManaged) {
    publishUpdateStatus("store-managed", `Updates are managed by the ${storeName}`);
    return updateStatus;
  }
  if (!app.isPackaged) {
    publishUpdateStatus("development", "Updates are disabled for unpackaged development builds");
    return updateStatus;
  }
  if (process.platform === "linux" && !process.env.APPIMAGE) {
    publishUpdateStatus("package-manager", "Update this deb/rpm install with your Linux package manager");
    return updateStatus;
  }
  const updater = ensureDirectUpdater();
  if (!updater) return updateStatus;
  // A manual click is explicit permission for this one download even when
  // background updates are disabled.
  updater.autoDownload = manual || settings.automaticUpdates;
  updater.autoInstallOnAppQuit = manual || settings.automaticUpdates;
  try {
    await updater.checkForUpdates();
  } catch (err) {
    publishUpdateStatus("error", err?.message || "Update check failed");
  }
  return updateStatus;
}

function reconcileAutomaticUpdates() {
  if (updateTimer) clearInterval(updateTimer);
  updateTimer = null;
  if (storeManaged) {
    publishUpdateStatus("store-managed", `Updates are managed by the ${storeName}`);
    return;
  }
  if (!settings.automaticUpdates) {
    if (directAutoUpdater) {
      directAutoUpdater.autoDownload = false;
      directAutoUpdater.autoInstallOnAppQuit = false;
    }
    publishUpdateStatus("disabled", "Automatic updates are off");
    return;
  }
  void checkForDesktopUpdate();
  updateTimer = setInterval(() => void checkForDesktopUpdate(), 6 * 60 * 60 * 1000);
  updateTimer.unref?.();
}

/** Keep the GUI/agent available without changing a global Windows power plan.
 * Electron's blocker is process-scoped and automatically disappears on crash
 * or quit; the tray checkbox gives the user an immediate opt-out.
 *
 * Parity with the Talos desktop app (../talos/desktop-app/
 * src/electron/services/power-management.ts + agent-power-guard.ts): a
 * long-lived remote endpoint must block DISPLAY sleep too, not only
 * app-suspension — `prevent-app-suspension` lets the screen blank and, on some
 * machines, the radio/network power-saving kick in, so the box drops off the
 * relay. We use `prevent-display-sleep` and re-assert it whenever the OS
 * resumes, because a suspend cycle silently invalidates the assertion. */
function reconcileKeepAwake() {
  if (settings.keepAwake) {
    if (keepAwakeBlockerId === null || !powerSaveBlocker.isStarted(keepAwakeBlockerId)) {
      keepAwakeBlockerId = powerSaveBlocker.start("prevent-display-sleep");
    }
    return;
  }
  if (keepAwakeBlockerId !== null && powerSaveBlocker.isStarted(keepAwakeBlockerId)) {
    powerSaveBlocker.stop(keepAwakeBlockerId);
  }
  keepAwakeBlockerId = null;
}

function stopKeepAwake() {
  if (keepAwakeBlockerId !== null && powerSaveBlocker.isStarted(keepAwakeBlockerId)) {
    powerSaveBlocker.stop(keepAwakeBlockerId);
  }
  keepAwakeBlockerId = null;
}

function reconcileLaunchAtLogin() {
  if (storeManaged || !isLoginItemSupported()) return;
  try {
    if (process.platform === "linux") {
      const autostartDir = path.join(app.getPath("home"), ".config", "autostart");
      const autostartFile = path.join(autostartDir, "io.yaver.gui.desktop");
      if (!settings.launchAtLogin) {
        fs.rmSync(autostartFile, { force: true });
        return;
      }
      fs.mkdirSync(autostartDir, { recursive: true });
      // AppImage mounts under a transient /tmp path; APPIMAGE points back to
      // the stable user-installed file. deb/rpm installs use process.execPath.
      const executable = process.env.APPIMAGE || process.execPath;
      fs.writeFileSync(autostartFile, linuxAutostartEntry(executable), { mode: 0o600 });
      return;
    }
    app.setLoginItemSettings({
      openAtLogin: settings.launchAtLogin,
      // Windows should start as a tray node, not steal focus at sign-in.
      args: process.platform === "win32" ? ["--hidden"] : [],
      ...(process.platform === "darwin" ? { openAsHidden: true } : {}),
    });
  } catch (err) {
    desktopLog.write("error", "launch_at_login_failed", err.message);
    console.error("[yaver-gui] could not update start-at-login:", err.message);
  }
}

// ---------------------------------------------------------------------------
// URL resolution
// ---------------------------------------------------------------------------

function envDashboardUrl() {
  const u = process.env.YAVER_DASHBOARD_URL;
  if (!u) return null;
  try {
    const parsed = new URL(u);
    if (parsed.protocol === "https:" || parsed.protocol === "http:") return parsed.toString();
  } catch {
    /* fall through to defaults */
  }
  return null;
}

/** Dev mode: localhost:3000 wins when it answers, so `yarn dev` in web/
 *  is picked up automatically (chat + vibing iterate fastest there). */
function probeDevServer(timeoutMs = 1200) {
  return new Promise((resolve) => {
    const req = http.get(DEV_SERVER_URL + "/", { timeout: timeoutMs }, (res) => {
      res.resume();
      resolve(res.statusCode === 200);
    });
    req.on("error", () => resolve(false));
    req.on("timeout", () => {
      req.destroy();
      resolve(false);
    });
  });
}

async function resolveDashboardUrl() {
  if (guiFailureFixture) {
    // .invalid is a reserved TLD that never resolves: a deterministic,
    // network-independent main-frame load failure for the recovery smoke.
    return "https://guifailure.invalid/dashboard";
  }
  const explicit = envDashboardUrl();
  if (explicit) return explicit;
  if (process.env.YAVER_DEV === "1") {
    const up = await probeDevServer();
    return up ? DEV_SERVER_URL : DASHBOARD_PRODUCTION_URL;
  }
  return DASHBOARD_PRODUCTION_URL;
}

// ---------------------------------------------------------------------------
// Auth capture + header injection (the token-in-URL fix)
// ---------------------------------------------------------------------------

function installAuthInterceptor() {
  const ses = session.defaultSession;

  // URL mutation belongs to onBeforeRequest. onBeforeSendHeaders cannot
  // redirect; putting `url` in that callback's result is silently ignored by
  // Electron and would send the secret-bearing EventSource URL unchanged.
  ses.webRequest.onBeforeRequest((details, callback) => {
    try {
      const stripped = stripAuthFromUrl(details.url);
      if (!stripped.token && !stripped.rp) {
        callback({});
        return;
      }
      // Secrets are always stripped from the wire URL, but only remembered
      // for reuse when the URL was a real agent route (never an asset, the
      // dashboard's own /api, or a marketing page) — the captured bearer must
      // not be re-attachable to non-agent paths later.
      if (stripped.capture) {
        const origin = new URL(stripped.url).origin;
        const entry = authByOrigin.get(origin) || {};
        if (stripped.token) entry.token = stripped.token;
        if (stripped.rp) entry.rp = stripped.rp;
        if (stripped.deviceId) {
          if (!entry.deviceIds) entry.deviceIds = new Set();
          entry.deviceIds.add(stripped.deviceId);
        }
        authByOrigin.set(origin, entry);
      }
      callback({ redirectURL: stripped.url });
    } catch {
      callback({});
    }
  });

  ses.webRequest.onBeforeSendHeaders((details, callback) => {
    callback({
      requestHeaders: applyKnownAuthHeaders({
        url: details.url,
        headers: details.requestHeaders,
        authByOrigin,
      }),
    });
  });

  // The remote dashboard is not allowed to turn this developer shell into a
  // camera/microphone/location/device-permission broker. Receiving WebRTC
  // video and using WebAuthn do not require these grants. Keep only sanitized
  // clipboard writes for the dashboard's explicit Copy buttons.
  const permissionAllowed = (permission, rawOrigin) => {
    if (permission !== "clipboard-sanitized-write" && permission !== "fullscreen") return false;
    try { return APP_ORIGINS.has(new URL(rawOrigin).origin); } catch { return false; }
  };
  ses.setPermissionCheckHandler((_webContents, permission, requestingOrigin) => (
    permissionAllowed(permission, requestingOrigin)
  ));
  ses.setPermissionRequestHandler((webContents, permission, callback, details) => {
    callback(permissionAllowed(permission, details?.requestingUrl || webContents.getURL()));
  });
}

// ---------------------------------------------------------------------------
// Notifications (task completion, from preload observer)
// ---------------------------------------------------------------------------

const sentTaskNotifications = new Map();

function sendTaskNotification(kind, title, taskId = "") {
  if (!settings.taskNotifications) return;
  const now = Date.now();
  const key = `${kind}:${taskId || title}`;
  const previous = sentTaskNotifications.get(key) || 0;
  if (now - previous < 45_000) return;
  sentTaskNotifications.set(key, now);
  for (const [oldKey, at] of sentTaskNotifications) {
    if (now - at > 5 * 60_000) sentTaskNotifications.delete(oldKey);
  }
  if (Notification.isSupported()) {
    new Notification({
      title: kind === "completed" ? "Task completed" : `Task ${kind}`,
      body: title || "Yaver finished working.",
      icon: path.join(__dirname, "..", "assets", "icon.png"),
      silent: false,
    }).show();
  }
}

ipcMain.on("yaver:task-status", (_event, payload) => {
  const { kind, title, taskId } = payload || {};
  if (typeof kind !== "string") return;
  sendTaskNotification(
    kind,
    typeof title === "string" ? title.slice(0, 240) : "",
    typeof taskId === "string" ? taskId.slice(0, 160) : "",
  );
});

ipcMain.on("yaver:set-task-notifications", (_event, enabled) => {
  settings.taskNotifications = Boolean(enabled);
  saveSettings(settings);
  if (tray) rebuildTray();
});

ipcMain.on("yaver:set-keep-awake", (_event, enabled) => {
  settings.keepAwake = Boolean(enabled);
  saveSettings(settings);
  reconcileKeepAwake();
  if (tray) rebuildTray();
});

ipcMain.on("yaver:set-launch-at-login", (_event, enabled) => {
  if (storeManaged) return;
  settings.launchAtLogin = Boolean(enabled);
  saveSettings(settings);
  reconcileLaunchAtLogin();
  if (tray) rebuildTray();
});

ipcMain.handle("yaver:set-automatic-updates", (_event, enabled) => {
  if (storeManaged) return { enabled: true, managedByStore: true, ...updateStatus };
  settings.automaticUpdates = Boolean(enabled);
  saveSettings(settings);
  reconcileAutomaticUpdates();
  return { enabled: settings.automaticUpdates, managedByStore: false, ...updateStatus };
});

ipcMain.handle("yaver:check-for-updates", () => checkForDesktopUpdate({ manual: true }));

ipcMain.handle("yaver:open-diagnostic-logs", () => {
  desktopLog.flush();
  shell.showItemInFolder(desktopLog.filePath);
  return { ok: true };
});

function currentUninstallPlan() {
  return uninstallPlan({
    platform: process.platform,
    execPath: process.execPath,
    appImage: process.env.APPIMAGE,
    storeManaged,
    storeName,
  });
}

/**
 * Owner-initiated uninstall. The renderer can request it but never supplies a
 * path: the plan is derived from the running process. A Store-managed build
 * and a deb/rpm install are named, never faked. `~/.yaver` agent data and any
 * project checkout are deliberately left in place.
 */
async function performUninstallRequest() {
  const plan = currentUninstallPlan();
  desktopLog.write("info", "uninstall_requested", `supported=${plan.supported} action=${plan.action}`);
  if (mainWindow && !mainWindow.isDestroyed()) showWindow();
  if (!plan.supported) {
    await dialog.showMessageBox(mainWindow || undefined, {
      type: "info",
      buttons: ["OK"],
      title: "Uninstall Yaver",
      message: "Yaver cannot uninstall itself here",
      detail: plan.detail,
    });
    return { ok: false, requiresUserAction: true, detail: plan.detail };
  }
  const confirm = await dialog.showMessageBox(mainWindow || undefined, {
    type: "warning",
    buttons: ["Cancel", "Uninstall Yaver"],
    defaultId: 0,
    cancelId: 0,
    noLink: true,
    title: "Uninstall Yaver",
    message: "Uninstall Yaver from this computer?",
    detail: `${plan.detail}\n\nYour ~/.yaver agent data and project files are NOT removed.`,
  });
  if (confirm.response !== 1) return { ok: false, cancelled: true };

  // Stop only the child agent we supervise; never kill an adopted service.
  if (agentManager) {
    try { await agentManager.stop(); } catch { /* best-effort */ }
    agentManager = null;
  }
  const result = await executeUninstallPlan(plan, {
    trash: (target) => shell.trashItem(target),
    runDetached: (file, args) => spawnDetached(file, args),
  });
  desktopLog.write(result.ok ? "info" : "error", "uninstall_result", result.ok ? plan.action : result.error);
  if (result.ok) {
    // The NSIS uninstaller removes the running files; quit so it can. macOS
    // Trash and Linux AppImage removal are already complete.
    setTimeout(() => {
      isQuitting = true;
      app.quit();
    }, plan.action === "run-uninstaller" ? 2500 : 400);
  }
  return { ok: result.ok, action: plan.action, detail: plan.detail, error: result.error };
}

ipcMain.handle("yaver:uninstall-app", () => performUninstallRequest());

ipcMain.handle("yaver:run-desktop-connectivity-diagnostics", async () => {
  const report = await runDesktopConnectivityDiagnostics({
    platform: process.platform,
    agentStatus,
    agentStatusDetail,
    localDeviceId: localAgentDeviceId(),
    clientOnly: storeClientOnly,
    agentPath: storeClientOnly ? "" : (agentManager?.agentPath || resolveAgentBinary() || ""),
    probeAgent: () => storeClientOnly ? Promise.resolve({ ok: false }) : probeAgentHealth(18080),
  });
  if (!storeClientOnly) {
    try {
      const rd = await localAgentJSON("/rd/status");
      let check;
      if (!rd.supported) {
        check = { id: "yaver-remote-desktop", name: "Yaver Remote Desktop", status: "fail", detail: "This agent build does not support screen capture/input on this operating system.", aiEligible: true };
      } else if (rd.engineError || rd.displaysError) {
        check = {
          id: "yaver-remote-desktop",
          name: "Yaver Remote Desktop",
          status: "warn",
          detail: `Capture/input is blocked: ${rd.engineError || rd.displaysError}`,
          fix: process.platform === "darwin" ? { id: "macos-privacy", label: "Open permissions" } : undefined,
          aiEligible: true,
        };
      } else if (!rd.viewConsentSet) {
        check = { id: "yaver-remote-desktop", name: "Yaver Remote Desktop", status: "warn", detail: "Screen view is waiting for an explicit choice on this machine. Remote callers cannot grant first consent.", fix: { id: "enable-yaver-view", label: "Enable local view" }, aiEligible: true };
      } else if (!rd.viewEnabled) {
        check = { id: "yaver-remote-desktop", name: "Yaver Remote Desktop", status: "warn", detail: "The local owner has disabled screen view.", fix: { id: "enable-yaver-view", label: "Enable local view" }, aiEligible: true };
      } else {
        check = { id: "yaver-remote-desktop", name: "Yaver Remote Desktop", status: "pass", detail: rd.controlEnabled ? "Screen view and remote input are enabled." : "Screen view is enabled; remote input remains off." };
      }
      report.checks.push(check);
      report.ok = report.ok && check.status !== "fail";
    } catch (error) {
      report.checks.push({ id: "yaver-remote-desktop", name: "Yaver Remote Desktop", status: "warn", detail: `The local policy probe did not answer: ${error.message || error}`, aiEligible: true });
    }
  }
  return report;
});

// Fixed identifiers only: the remote dashboard can request a known repair but
// can never supply a command, executable path, URL, or PowerShell fragment.
ipcMain.handle("yaver:apply-desktop-connectivity-fix", async (_event, rawId) => {
  const id = typeof rawId === "string" ? rawId : "";
  desktopLog.write("info", "desktop_connectivity_fix", `requested=${id}`);
  switch (id) {
    case "restart-agent":
      if (storeClientOnly) return { ok: false, error: "This store build is client-only." };
      if (!agentManager) return { ok: false, error: "The desktop agent supervisor is not running." };
      return agentManager.restart();
    case "open-tailscale":
      await shell.openExternal("https://login.tailscale.com/admin/machines");
      return { ok: true, requiresUserAction: true };
    case "open-download":
      await shell.openExternal("https://yaver.io/download");
      return { ok: true, requiresUserAction: true };
    case "windows-rdp-settings":
      if (process.platform !== "win32") return { ok: false, error: "Windows Remote Desktop settings are only available on Windows." };
      await shell.openExternal("ms-settings:remotedesktop");
      return { ok: true, requiresUserAction: true };
    case "macos-privacy":
      if (process.platform !== "darwin") return { ok: false, error: "macOS Privacy settings are only available on macOS." };
      await shell.openExternal("x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture");
      return { ok: true, requiresUserAction: true };
    case "macos-firewall-settings":
      if (process.platform !== "darwin") return { ok: false, error: "macOS Firewall settings are only available on macOS." };
      await shell.openExternal("x-apple.systempreferences:com.apple.Network-Settings.extension?Firewall");
      return { ok: true, requiresUserAction: true };
    case "enable-yaver-view":
      if (storeClientOnly) return { ok: false, error: "This store build has no local agent host." };
      await localAgentJSON("/rd/policy", { method: "POST", body: { viewEnabled: true } });
      return { ok: true };
    case "windows-firewall": {
      if (process.platform !== "win32") return { ok: false, error: "Windows Firewall repair is only available on Windows." };
      const agentPath = agentManager?.agentPath || resolveAgentBinary();
      return repairWindowsFirewall(agentPath);
    }
    default:
      return { ok: false, error: "Unknown desktop connectivity repair; no change was made." };
  }
});

ipcMain.handle("yaver:open-system-rdp", async (_event, rawHost) => {
  const host = typeof rawHost === "string" ? rawHost.trim() : "";
  if (!isTailnetIPv4Host(host)) {
    return { ok: false, error: "RDP launch only accepts a validated Tailscale IPv4 address from the device row." };
  }
  const probe = await probeTCP(host, 3389);
  if (!probe.ok) {
    return { ok: false, error: `TCP 3389 did not answer over Tailscale (${probe.error}). Run Connectivity & Remote Access diagnostics on the Windows target.` };
  }
  if (process.platform === "win32") {
    return spawnDetached("mstsc.exe", [`/v:${host}`]);
  }
  const uri = process.platform === "darwin"
    ? `rdp://full%20address=s:${host}:3389`
    : `rdp://${host}:3389`;
  await shell.openExternal(uri);
  return { ok: true };
});

ipcMain.handle("yaver:get-desktop-status", () => {
  const plan = currentUninstallPlan();
  return {
    surface: "desktop-gui",
    localDeviceId: localAgentDeviceId() || null,
    appVersion: app.getVersion(),
    distribution: distributionChannel,
    agent: { state: agentStatus, detail: agentStatusDetail, port: storeClientOnly ? null : 18080 },
    keepAwake: settings.keepAwake,
    launchAtLogin: settings.launchAtLogin,
    loginItemSupported: !storeManaged && isLoginItemSupported(),
    updates: {
      enabled: storeManaged ? true : settings.automaticUpdates,
      managedByStore: storeManaged,
      ...updateStatus,
    },
    uninstall: {
      supported: plan.supported,
      action: plan.action,
      detail: plan.detail,
    },
    logs: { path: desktopLog.filePath, maxBytes: desktopLog.maxBytes, maxFiles: desktopLog.maxFiles },
  };
});

ipcMain.on("yaver:get-app-version", (event) => {
  event.returnValue = app.getVersion();
});

ipcMain.on("yaver:is-trusted-renderer-origin", (event, claimedOrigin) => {
  try {
    const senderOrigin = new URL(event.senderFrame.url).origin;
    event.returnValue = senderOrigin === claimedOrigin && APP_ORIGINS.has(senderOrigin);
  } catch {
    event.returnValue = false;
  }
});

// ---------------------------------------------------------------------------
// Window
// ---------------------------------------------------------------------------

async function createWindow() {
  const url = await resolveDashboardUrl();

  mainWindow = new BrowserWindow({
    width: 1280,
    height: 820,
    minWidth: 980,
    minHeight: 640,
    show: false,
    backgroundColor: "#0a0a0c",
    title: "Yaver",
    // Keep the real operating-system frame. A frameless web rectangle loses
    // macOS' rounded outer corners, shadow, resize affordances and stable
    // traffic-light placement (and looks especially wrong beside Chrome or
    // Surfshark). The explicit options also stop a future title-bar styling
    // refactor from silently turning the desktop app into a square web shell.
    frame: true,
    hasShadow: true,
    roundedCorners: true,
    ...(process.platform === "darwin" ? {
      titleBarStyle: "default",
      titleBarSeparatorStyle: "line",
    } : {}),
    ...(process.platform === "win32" ? {
      thickFrame: true,
      backgroundMaterial: "mica",
    } : {}),
    // Window/taskbar icon on Windows + Linux (macOS uses the app bundle's
    // icns). Also the dock/taskbar image during the brief pre-paint window.
    icon: path.join(__dirname, "..", "assets", "icon.png"),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      spellcheck: false,
    },
  });

  // Keep the last good surface visible: hide until first paint, never flash
  // a blank window.
  mainWindow.once("ready-to-show", () => {
    if (!launchHidden) mainWindow.show();
  });

  // External links → system browser; allowed app/auth popups → a hardened
  // in-app child window. The old handler DENIED every window.open() that
  // targeted an allowed app URL (previews, runner OAuth, feedback) and only
  // opened the ones it did not recognise — so "Open preview" / "Sign in" did
  // nothing at all in the GUI while working in a normal browser. A browser has
  // real tabs; the desktop shell has to provide the equivalent.
  mainWindow.webContents.setWindowOpenHandler(({ url: target }) => {
    try {
      const parsed = new URL(target);
      if (parsed.protocol === "https:" || parsed.protocol === "http:") {
        if (isAllowedAppUrl(target)) {
          void createAppChildWindow(target);
          return { action: "deny" };
        }
        // Never hand a token-bearing URL to the OS browser.
        shell.openExternal(safeExternalUrl(target));
      }
    } catch {
      /* ignore malformed */
    }
    return { action: "deny" };
  });

  const enforceNavigationLock = (target) => {
    try {
      const parsed = new URL(target);
      if (parsed.protocol !== "https:" && parsed.protocol !== "http:") return;
      if (isAllowedAppUrl(target)) return;
      if (APP_ORIGINS.has(parsed.origin)) {
        // Marketing/docs/blog/pricing/… → the app's own auth gate, which
        // bounces an already-signed-in user straight to /dashboard. The GUI
        // is sign-in → app only; web-app surfaces never open in a browser.
        void mainWindow.loadURL(`${parsed.origin}/auth?return=/dashboard`);
        return;
      }
      // Never hand a token-bearing URL to the OS browser (audit pass-2 M3).
      shell.openExternal(safeExternalUrl(target));
    } catch {
      /* malformed URL — leave the navigation prevented */
    }
  };

  // Top-level navigations (incl. server redirects during OAuth).
  mainWindow.webContents.on("will-navigate", (event, target) => {
    if (isAllowedAppUrl(target)) return;
    event.preventDefault();
    enforceNavigationLock(target);
  });
  mainWindow.webContents.on("will-redirect", (event, target) => {
    if (isAllowedAppUrl(target)) return;
    event.preventDefault();
    enforceNavigationLock(target);
  });
  // SPA soft-navigations (Next.js App Router pushState) land here — enforce
  // the same allowlist so an in-app link to /pricing or /docs can't open.
  // (The decision lives in navigation-policy.js and is unit-tested; the
  // historical wiring bug — a missing `isAllowedAppPath` import — silently
  // no-oped this guard and let marketing pages render in-window.)
  mainWindow.webContents.on("did-navigate-in-page", (_event, target) => {
    const decision = inPageNavigationDecision(target);
    if (decision.allow) return;
    if (decision.bounce) {
      void mainWindow.loadURL(decision.bounce);
    }
    // bounce:null → foreign origin, nothing to do in-window.
  });

  mainWindow.webContents.on("did-finish-load", () => {
    const loadedURL = mainWindow?.webContents.getURL() || "";
    // Loading our data: recovery document is not dashboard recovery. Resetting
    // here made each terminal page look like a successful attempt and could
    // reopen the retry budget indefinitely.
    if (/^https?:\/\//.test(loadedURL) && isAllowedAppUrl(loadedURL)) {
      rendererRecoveryAttempts = 0;
      lastRendererFailure = null;
      if (rendererLoadRetryTimer) clearTimeout(rendererLoadRetryTimer);
      rendererLoadRetryTimer = null;
    }
    desktopLog.write("info", "renderer_loaded", loadedURL);
    if (mainWindow && !mainWindow.isDestroyed()) {
      mainWindow.setTitle("Yaver");
      mainWindow.webContents.send("yaver:agent-status", {
        state: agentStatus,
        detail: agentStatusDetail,
        port: storeClientOnly ? null : 18080,
      });
    }
  });
  mainWindow.webContents.on("did-fail-load", (_event, code, description, validatedURL, isMainFrame) => {
    if (!isMainFrame) return;
    // The only data: document Yaver loads is the terminal recovery surface.
    // A failed/superseded recovery navigation must not recursively diagnose
    // itself as another dashboard failure.
    if (String(validatedURL || "").startsWith("data:text/html")) return;
    // Chromium reports ERR_ABORTED when a deliberate navigation supersedes
    // the current one (for example auth -> dashboard or a headless page.goto).
    // It is not a load failure; rendering the recovery document here aborts
    // the successful replacement and can recurse on the data: page itself.
    if (code === -3 || description === "ERR_ABORTED") return;
    lastRendererFailure = { kind: "load", code, description, url: validatedURL };
    desktopLog.write("error", "renderer_load_failed", `code=${code} ${description} ${validatedURL}`);
    const retryURL = isAllowedAppUrl(validatedURL) ? validatedURL : url;
    if (scheduleRendererLoadRetry(lastRendererFailure, retryURL)) return;
    showRendererFailure(lastRendererFailure);
  });
  mainWindow.webContents.on("render-process-gone", (_event, details) => {
    desktopLog.write("error", "renderer_process_gone", JSON.stringify(details));
    lastRendererFailure = { kind: "renderer", reason: details?.reason || "unknown", exitCode: details?.exitCode };
    if (rendererRecoveryAttempts < 1 && !isQuitting) {
      rendererRecoveryAttempts += 1;
      desktopLog.write("warn", "renderer_recovery_retry", `attempt=${rendererRecoveryAttempts}`);
      setTimeout(() => {
        if (!mainWindow || mainWindow.isDestroyed() || isQuitting) return;
        void mainWindow.loadURL(url).catch((error) => {
          lastRendererFailure = { kind: "load", code: "exception", description: error?.message || String(error), url };
          showRendererFailure(lastRendererFailure);
        });
      }, 250);
    } else if (!isQuitting) {
      showRendererFailure(lastRendererFailure);
    }
  });
  mainWindow.on("unresponsive", () => desktopLog.write("warn", "window_unresponsive"));

  mainWindow.on("close", (event) => {
    // Keep alive in the tray (except on real quit / macOS Cmd+Q).
    if (!isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });

  mainWindow.on("closed", () => {
    mainWindow = null;
  });

  try {
    await mainWindow.loadURL(url);
  } catch (error) {
    if (/ERR_ABORTED|\(-3\)/.test(error?.message || String(error))) return;
    // A normal network rejection also emits did-fail-load, which owns retries
    // and terminal UI. Only surface an exceptional rejection that produced no
    // structured main-frame failure.
    if (!lastRendererFailure && !rendererLoadRetryTimer) {
      lastRendererFailure = { kind: "load", code: "exception", description: error?.message || String(error), url };
      showRendererFailure(lastRendererFailure);
    }
  }

  if (guiFailureFixture === "crash") {
    // Crash the renderer on the recovery page to exercise render-process-gone
    // + the single bounded retry path end to end.
    setTimeout(() => {
      if (mainWindow && !mainWindow.isDestroyed() && !isQuitting) {
        mainWindow.webContents.forcefullyCrashRenderer();
      }
    }, 600);
  }
}

/** Strip ?token=/?__rp= from a URL before it is handed to the OS browser. */
function safeExternalUrl(target) {
  try {
    return stripAuthFromUrl(target).url;
  } catch {
    return target;
  }
}

/**
 * Hardened in-app popup for allowed app/auth URLs (previews, runner OAuth,
 * feedback). A browser has real tabs; the desktop shell provides the
 * equivalent instead of silently dropping `window.open()`. Same security
 * posture as the main window: sandbox, context isolation, no node, and the
 * same navigation allowlist. Declared after createWindow so the unit test
 * that slices the MAIN window options still finds `mainWindow = new
 * BrowserWindow` first; function declarations hoist, so the call site works.
 */
async function createAppChildWindow(target) {
  if (!mainWindow || mainWindow.isDestroyed()) return;
  const child = new BrowserWindow({
    width: 1120,
    height: 780,
    show: false,
    parent: mainWindow,
    backgroundColor: "#0a0a0c",
    title: "Yaver",
    icon: path.join(__dirname, "..", "assets", "icon.png"),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      spellcheck: false,
    },
  });
  child.once("ready-to-show", () => {
    if (!child.isDestroyed()) child.show();
  });
  child.webContents.setWindowOpenHandler(({ url: next }) => {
    try {
      const parsed = new URL(next);
      if (parsed.protocol === "https:" || parsed.protocol === "http:") {
        if (isAllowedAppUrl(next)) {
          void createAppChildWindow(next);
          return { action: "deny" };
        }
        shell.openExternal(safeExternalUrl(next));
      }
    } catch {
      /* ignore malformed */
    }
    return { action: "deny" };
  });
  child.webContents.on("will-navigate", (event, next) => {
    if (isAllowedAppUrl(next)) return;
    event.preventDefault();
    try {
      const parsed = new URL(next);
      if (parsed.protocol !== "https:" && parsed.protocol !== "http:") return;
      if (APP_ORIGINS.has(parsed.origin)) {
        void child.loadURL(`${parsed.origin}/auth?return=/dashboard`);
        return;
      }
      shell.openExternal(safeExternalUrl(next));
    } catch {
      /* malformed URL — leave the navigation prevented */
    }
  });
  try {
    await child.loadURL(target);
  } catch {
    /* the child window surfaces its own load failure */
  }
}

function scheduleRendererLoadRetry(failure, retryURL) {
  if (guiFailureFixture || isQuitting || rendererLoadRetryTimer) return Boolean(rendererLoadRetryTimer);
  if (!shouldRetryRendererLoad({ code: failure?.code, attempts: rendererRecoveryAttempts })) return false;

  rendererRecoveryAttempts += 1;
  const delayMs = rendererLoadRetryDelay(rendererRecoveryAttempts);
  desktopLog.write(
    "warn",
    "renderer_load_retry_scheduled",
    `attempt=${rendererRecoveryAttempts}/${MAX_TRANSIENT_LOAD_RETRIES} delayMs=${delayMs} code=${failure?.code}`,
  );
  rendererLoadRetryTimer = setTimeout(() => {
    rendererLoadRetryTimer = null;
    if (!mainWindow || mainWindow.isDestroyed() || isQuitting) return;
    void mainWindow.loadURL(retryURL).catch(() => {
      // did-fail-load carries Chromium's stable error code and schedules the
      // next bounded attempt. Avoid racing it with a second exception path.
    });
  }, delayMs);
  return true;
}

function showRendererFailure(failure) {
  if (!mainWindow || mainWindow.isDestroyed() || isQuitting) return;
  const detail = [
    failure?.kind === "renderer" ? "The app renderer crashed." : "The dashboard could not be loaded.",
    failure?.description || failure?.reason || "Unknown renderer failure.",
    failure?.code !== undefined ? `Error code: ${failure.code}` : "",
    failure?.exitCode !== undefined ? `Exit code: ${failure.exitCode}` : "",
  ].filter(Boolean).join("\\n");
  const safeDetail = JSON.stringify(detail);
  // The failing URL may itself have carried ?token=/?__rp= (agent stream or
  // dashboard URL). Strip before embedding so "Open in browser" never hands
  // the bearer to the OS browser (audit pass-2 M3).
  let browserUrl = DASHBOARD_PRODUCTION_URL;
  if (failure?.url && /^(?:https?:)\/\//.test(failure.url)) {
    try {
      browserUrl = stripAuthFromUrl(failure.url).url;
    } catch {
      browserUrl = DASHBOARD_PRODUCTION_URL;
    }
  }
  const safeUrl = JSON.stringify(browserUrl);
  const html = `<!doctype html><meta charset="utf-8"><title>Yaver could not open</title>
    <style>body{margin:0;background:#0a0a0c;color:#f5f5f5;font:15px -apple-system,BlinkMacSystemFont,system-ui,sans-serif;display:flex;min-height:100vh;align-items:center;justify-content:center}main{width:min(620px,calc(100vw - 48px));padding:32px}h1{font-size:24px;margin:0 0 12px}p{color:#a1a1aa;line-height:1.5;white-space:pre-wrap}.actions{display:flex;gap:10px;flex-wrap:wrap;margin-top:24px}button,.button{border:0;border-radius:8px;padding:11px 16px;background:#34d399;color:#052e1b;font-weight:650;cursor:pointer}.button{display:inline-block;text-decoration:none}button.secondary,.button.secondary{background:#27272a;color:#f4f4f5}</style>
    <main><h1>Yaver could not open the dashboard</h1><p id="detail"></p><p>Yaver stopped retrying so it would not leave you with an endless black screen.</p><div class="actions"><button onclick="location.reload()">Retry</button><a class="button secondary" href=${safeUrl}>Open in browser</a></div></main>
    <script>document.getElementById('detail').textContent=${safeDetail}</script>`;
  mainWindow.show();
  void mainWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(html)}`);
}

// ---------------------------------------------------------------------------
// Tray
// ---------------------------------------------------------------------------

function trayIcon() {
  const pngPath = path.join(__dirname, "..", "assets", "icon.png");
  let image = nativeImage.createFromPath(pngPath);
  if (image.isEmpty()) {
    image = nativeImage.createEmpty();
  }
  return image.resize({ width: 18, height: 18 });
}

function rebuildTray() {
  if (!tray) return;
  const agentLabel = {
    running: "Agent · running ✓",
    adopted: "Agent · running (external) ✓",
    pairing: "Agent · pair this PC",
    starting: "Agent · starting…",
    missing: "Agent · binary not found",
    crashed: "Agent · restarting…",
    stopped: "Agent · stopped",
    "client-only": "App Store client · connect to a Yaver node",
  }[agentStatus] || "Agent · " + agentStatus;
  const menu = Menu.buildFromTemplate([
    { label: "Show Yaver", click: showWindow },
    { label: agentLabel, enabled: false },
    {
      label: "Task notifications",
      type: "checkbox",
      checked: settings.taskNotifications,
      click: (item) => {
        settings.taskNotifications = item.checked;
        saveSettings(settings);
      },
    },
    ...(!storeClientOnly ? [{
      label: "Keep this PC available",
      type: "checkbox",
      checked: settings.keepAwake,
      click: (item) => {
        settings.keepAwake = item.checked;
        saveSettings(settings);
        reconcileKeepAwake();
        rebuildTray();
      },
    }] : []),
    ...(!storeManaged && isLoginItemSupported() ? [{
      label: "Start Yaver at login",
      type: "checkbox",
      checked: settings.launchAtLogin,
      click: (item) => {
        settings.launchAtLogin = item.checked;
        saveSettings(settings);
        reconcileLaunchAtLogin();
        rebuildTray();
      },
    }] : []),
    ...(storeManaged ? [{
      label: `Updates · managed by ${storeName}`,
      enabled: false,
    }] : [{
      label: "Automatic updates",
      type: "checkbox",
      checked: settings.automaticUpdates,
      click: (item) => {
        settings.automaticUpdates = item.checked;
        saveSettings(settings);
        reconcileAutomaticUpdates();
        rebuildTray();
      },
    }, {
      label: updateStatus.state === "checking" ? "Checking for updates…" : "Check for updates…",
      enabled: updateStatus.state !== "checking",
      click: () => void checkForDesktopUpdate({ manual: true }),
    }]),
    { type: "separator" },
    {
      label: "Tasks",
      click: () => {
        showWindow();
        const target = dashboardUrlForTab("chat");
        if (target) void mainWindow.loadURL(target).catch((error) => showRendererFailure({ kind: "load", code: "exception", description: error?.message || String(error), url: target }));
      },
    },
    {
      // Doctor / diagnose — the embedded agent serves /diagnose (+ stream,
      // /agent/doctor, /net/doctor, /mobile/hermes/doctor); the dashboard's
      // HealthView tab renders them. Deep-link to that tab.
      label: "Diagnose (doctor)",
      click: () => {
        showWindow();
        if (mainWindow && !mainWindow.isDestroyed()) {
          const current = mainWindow.webContents.getURL();
          const sep = current.includes("?") ? "&" : "?";
          void mainWindow.loadURL(current + sep + "tab=health");
        }
      },
    },
    {
      // First-class opencode config — the dashboard's Settings tab renders
      // OpenCodeSettingsView (provider selection, API-key entry, model
      // pickers). Deep-link straight to it from the tray so the desktop
      // app's most-reached dev setting is one click, not a tab hunt.
      label: "OpenCode settings…",
      click: () => {
        showWindow();
        if (mainWindow && !mainWindow.isDestroyed()) {
          const current = mainWindow.webContents.getURL();
          const sep = current.includes("?") ? "&" : "?";
          void mainWindow.loadURL(current + sep + "tab=settings");
        }
      },
    },
    {
      label: "Reload",
      click: () => {
        if (mainWindow && !mainWindow.isDestroyed()) mainWindow.webContents.reload();
      },
    },
    {
      label: "Open diagnostic logs…",
      click: () => {
        desktopLog.flush();
        shell.showItemInFolder(desktopLog.filePath);
      },
    },
    { type: "separator" },
    {
      label: "Uninstall Yaver…",
      click: () => { void performUninstallRequest(); },
    },
    { label: "Quit Yaver", click: () => { isQuitting = true; app.quit(); } },
  ]);
  tray.setContextMenu(menu);
  tray.setToolTip(`Yaver — AI dev machine remote (${agentLabel})`);
}

function createTray() {
  tray = new Tray(trayIcon());
  tray.on("click", showWindow);
  rebuildTray();
}

/** BrowserWindow.icon is ignored by macOS. Set the Dock image explicitly so
 * an unpackaged/dev launch never presents itself as generic Electron; signed
 * packaged builds use the same canonical artwork from the app bundle. */
function setMacDockIcon() {
  if (process.platform !== "darwin" || !app.dock) return;
  const icon = nativeImage.createFromPath(path.join(__dirname, "..", "assets", "icon.png"));
  if (!icon.isEmpty()) app.dock.setIcon(icon);
}

// ---------------------------------------------------------------------------
// Embedded yaver agent (this desktop IS a yaver node)
// ---------------------------------------------------------------------------

/**
 * Start the embedded agent supervisor. The desktop can be a remote box
 * (vibed from tvOS/mobile/web/another desktop over the same device routing)
 * and a client surface (the window vibes the local agent or any other
 * device). Healthy agents already running on :18080 are adopted, not
 * duplicated — matching `yaver serve`'s own reuse semantics.
 */
function startEmbeddedAgent() {
  if (agentManager) return;
  agentManager = new AgentManager({
    onStatus: ({ state, detail }) => {
      agentStatus = state;
      agentStatusDetail = detail || null;
      desktopLog.write(state === "crashed" || state === "missing" ? "error" : "info", "agent_status", `${state}${detail ? `: ${detail}` : ""}`);
      if (tray) rebuildTray();
      if (mainWindow && !mainWindow.isDestroyed()) {
        mainWindow.webContents.send("yaver:agent-status", { state, detail: agentStatusDetail, port: 18080 });
      }
    },
    onLog: (line) => desktopLog.write("info", "agent", line),
  });
  void agentManager.start();
}

function showWindow() {
  if (!mainWindow || mainWindow.isDestroyed()) {
    void createWindow();
    return;
  }
  mainWindow.show();
  mainWindow.focus();
}

function dashboardUrlForTab(tab) {
  if (!mainWindow || mainWindow.isDestroyed()) return null;
  try {
    const current = new URL(mainWindow.webContents.getURL());
    if (current.protocol !== "http:" && current.protocol !== "https:") return `${DASHBOARD_PRODUCTION_URL}?tab=${encodeURIComponent(tab)}`;
    current.pathname = "/dashboard";
    current.searchParams.set("tab", tab);
    current.hash = "";
    return current.toString();
  } catch {
    desktopLog.write("warn", "tray_navigation_fallback", `tab=${tab}`);
    return `${DASHBOARD_PRODUCTION_URL}?tab=${encodeURIComponent(tab)}`;
  }
}

// ---------------------------------------------------------------------------
// Deep links: yaver://dashboard?tab=chat | runtime | devices | projects | health
// ---------------------------------------------------------------------------

function handleDeepLink(rawUrl) {
  try {
    const u = new URL(rawUrl);
    if (u.protocol !== "yaver:") return;
    const target = u.host || "dashboard"; // yaver://dashboard?tab=chat
    if (target !== "dashboard") return;
    const tab = u.searchParams.get("tab");
    showWindow();
    if (!mainWindow) return;
    // Let the SPA settle, then deep-link via ?tab= (the dashboard already
    // reads ?tab= on load and syncs it on navigation — page.tsx:1218-1221).
    setTimeout(() => {
      if (!mainWindow || mainWindow.isDestroyed()) return;
      if (tab) {
        const current = mainWindow.webContents.getURL();
        const sep = current.includes("?") ? "&" : "?";
        void mainWindow.webContents.executeJavaScript(
          `window.location.search = (window.location.search ? window.location.search + "&" : "?") + ${JSON.stringify(`tab=${encodeURIComponent(tab)}`)}; true`,
        ).catch(() => {
          void mainWindow.loadURL(current + sep + `tab=${encodeURIComponent(tab)}`);
        });
      }
    }, 900);
  } catch {
    /* malformed deep link — ignore */
  }
}

// ---------------------------------------------------------------------------
// App lifecycle
// ---------------------------------------------------------------------------

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on("second-instance", (_event, argv) => {
    showWindow();
    const link = argv.find((a) => a.startsWith("yaver://"));
    if (link) handleDeepLink(link);
  });

  app.setAsDefaultProtocolClient("yaver");

  app.on("open-url", (event, url) => {
    event.preventDefault();
    handleDeepLink(url);
  });

  app.whenReady().then(async () => {
    desktopLog.write("info", "app_ready");
    if (process.platform === "darwin") {
      try { launchHidden = launchHidden || app.getLoginItemSettings().wasOpenedAtLogin === true; } catch { /* visible fallback */ }
    }
    installAuthInterceptor();
    setMacDockIcon();
    if (!storeClientOnly) reconcileKeepAwake();
    // A suspend/resume cycle invalidates the power-save blocker; re-assert it so
    // the box stays awake and reachable (Talos parity).
    powerMonitor.on("resume", () => { if (!storeClientOnly) reconcileKeepAwake(); });
    reconcileLaunchAtLogin();
    createTray();
    reconcileAutomaticUpdates();
    if (!storeClientOnly) startEmbeddedAgent();
    await createWindow();

    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) {
        void createWindow();
      } else {
        showWindow();
      }
    });
  });

  app.on("before-quit", () => {
    desktopLog.write("info", "before_quit");
    isQuitting = true;
    if (updateTimer) clearInterval(updateTimer);
    updateTimer = null;
    stopKeepAwake();
    if (agentManager) {
      void agentManager.stop();
      agentManager = null;
    }
  });

  app.on("child-process-gone", (_event, details) => {
    desktopLog.write("error", "child_process_gone", JSON.stringify(details));
  });

  app.on("will-quit", () => desktopLog.close());

  app.on("window-all-closed", () => {
    // Keep the tray alive on every platform; the user quits from the tray.
    if (isQuitting) app.quit();
  });
}
