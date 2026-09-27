"use strict";

const { test } = require("node:test");
const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const { join } = require("node:path");

const main = readFileSync(join(__dirname, "..", "src", "main.js"), "utf8");
const preload = readFileSync(join(__dirname, "..", "src", "preload.js"), "utf8");
const pkg = JSON.parse(readFileSync(join(__dirname, "..", "package.json"), "utf8"));

test("desktop identity is Yaver even when launched from the development runtime", () => {
  assert.equal(pkg.productName, "Yaver");
  assert.match(main, /app\.setName\("Yaver"\)/);
  assert.ok(
    main.indexOf('app.setName("Yaver")') < main.indexOf("app.whenReady()"),
    "the visible application name must be set before Electron becomes ready",
  );
});

test("ready lifecycle starts the embedded agent and availability policy", () => {
  const ready = main.slice(main.indexOf("app.whenReady()"), main.indexOf("app.on(\"before-quit\""));
  assert.match(ready, /reconcileKeepAwake\(\)/);
  assert.match(ready, /reconcileLaunchAtLogin\(\)/);
  assert.match(ready, /if \(!storeClientOnly\) startEmbeddedAgent\(\)/);
  assert.match(ready, /setMacDockIcon\(\)/);
  assert.match(ready, /reconcileAutomaticUpdates\(\)/);
});

test("Mac Store stays client-only while Microsoft Store is Store-managed full-node", () => {
  assert.match(main, /const storeClientOnly = process\.mas === true;/);
  assert.match(main, /const storeManaged = process\.mas === true \|\| process\.windowsStore === true/);
  assert.match(main, /storeClientOnly \? "client-only" : "starting"/);
  assert.match(main, /distribution: distributionChannel/);
  assert.match(main, /port: storeClientOnly \? null : 18080/);
  assert.match(main, /!storeManaged && isLoginItemSupported\(\)/);
  assert.match(main, /if \(storeManaged \|\| !isLoginItemSupported\(\)\) return/);
  assert.match(main, /loginItemSupported: !storeManaged && isLoginItemSupported\(\)/);
});

test("affected Apple-silicon MAS renderers apply the macOS 26 JIT workaround before startup", () => {
  assert.match(main, /needsMasJitlessWorkaround\(\{ isMas: storeClientOnly \}\)/);
  assert.match(main, /appendSwitch\("js-flags", "--jitless"\)/);
  assert.ok(
    main.indexOf('appendSwitch("js-flags", "--jitless")') < main.indexOf("app.whenReady()"),
    "V8 flags must be set before Electron creates the first renderer",
  );
  assert.match(main, /masJitless=\$\{masJitlessWorkaround\}/);
});

test("automation cannot weaken packaged keychain storage", () => {
  assert.match(main, /!app\.isPackaged && process\.env\.YAVER_ELECTRON_AUTOMATION === "1"/);
  assert.match(main, /appendSwitch\("use-mock-keychain"\)/);
  assert.match(main, /path\.isAbsolute\(isolatedUserData\)/);
});

test("desktop keeps the native rounded operating-system frame", () => {
  const windowOptions = main.slice(main.indexOf("mainWindow = new BrowserWindow"), main.indexOf("webPreferences:"));
  assert.match(windowOptions, /frame:\s*true/);
  assert.match(windowOptions, /hasShadow:\s*true/);
  assert.match(windowOptions, /roundedCorners:\s*true/);
  assert.match(windowOptions, /titleBarStyle:\s*"default"/);
  assert.match(windowOptions, /titleBarSeparatorStyle:\s*"line"/);
  assert.match(windowOptions, /thickFrame:\s*true/);
});

test("quit lifecycle releases power and stops only the child we supervise", () => {
  const quit = main.slice(main.indexOf("app.on(\"before-quit\""));
  assert.match(quit, /stopKeepAwake\(\)/);
  assert.match(quit, /agentManager\.stop\(\)/);
});

test("renderer bridge exposes structured task and agent lifecycle seams", () => {
  assert.match(preload, /taskStatus\(payload\)/);
  assert.match(preload, /getDesktopStatus\(\)/);
  assert.match(preload, /runConnectivityDiagnostics\(\)/);
  assert.match(preload, /applyConnectivityFix\(id\)/);
  assert.match(preload, /openSystemRemoteDesktop\(host\)/);
  assert.match(preload, /onAgentStatus\(listener\)/);
  assert.match(preload, /setAutomaticUpdates\(enabled\)/);
  assert.match(preload, /checkForUpdates\(\)/);
  assert.match(preload, /onUpdateStatus\(listener\)/);
});

test("system RDP launch is Tailscale-only and operation-probes port 3389", () => {
  assert.match(main, /yaver:open-system-rdp/);
  assert.match(main, /isTailnetIPv4Host\(host\)/);
  assert.match(main, /probeTCP\(host, 3389\)/);
  assert.match(main, /spawnDetached\("mstsc\.exe"/);
  assert.match(main, /rdp:\/\/full%20address=s:/);
});

test("desktop connectivity repairs are fixed-id IPC, never renderer-provided commands", () => {
  assert.match(main, /yaver:run-desktop-connectivity-diagnostics/);
  assert.match(main, /yaver:apply-desktop-connectivity-fix/);
  assert.match(main, /case "windows-firewall"/);
  assert.match(main, /repairWindowsFirewall\(agentPath\)/);
  assert.match(main, /case "windows-rdp-settings"/);
  assert.match(main, /case "enable-yaver-view"/);
  assert.match(main, /localAgentJSON\("\/rd\/policy"/);
  assert.match(main, /Unknown desktop connectivity repair; no change was made/);
});

test("direct updater is signed-release, architecture-aware, and excluded from managed Stores", () => {
  assert.match(main, /if \(storeManaged \|\| !app\.isPackaged\) return false/);
  assert.match(main, /require\("electron-updater"\)/);
  assert.match(main, /autoUpdater\.channel = `latest-\$\{process\.arch\}`/);
  assert.match(main, /process\.platform === "linux" && !process\.env\.APPIMAGE/);
  assert.match(main, /settings\.automaticUpdates = Boolean\(enabled\)/);
});

test("live interceptor strips URLs in onBeforeRequest and only injects headers later", () => {
  const interceptor = main.slice(main.indexOf("function installAuthInterceptor"), main.indexOf("// Notifications"));
  assert.match(interceptor, /onBeforeRequest/);
  assert.match(interceptor, /redirectURL: stripped\.url/);
  assert.match(interceptor, /onBeforeSendHeaders/);
  assert.ok(
    interceptor.indexOf("redirectURL: stripped.url") < interceptor.indexOf("ses.webRequest.onBeforeSendHeaders"),
    "URL redirect must happen before the header-only phase",
  );
  assert.equal((interceptor.match(/redirectURL/g) || []).length, 1);
  assert.match(interceptor, /setPermissionRequestHandler/);
  assert.match(interceptor, /clipboard-sanitized-write/);
  assert.match(interceptor, /fullscreen/);
  assert.match(interceptor, /APP_ORIGINS\.has/);
});

test("preload bridge is absent on third-party OAuth provider pages", () => {
  assert.match(preload, /yaver:is-trusted-renderer-origin/);
  assert.match(preload, /if \(trustedRenderer\) contextBridge\.exposeInMainWorld/);
  assert.match(main, /senderOrigin === claimedOrigin && APP_ORIGINS\.has\(senderOrigin\)/);
});

test("renderer failures become visible and recoverable instead of a black window", () => {
  assert.match(main, /renderer_load_failed/);
  assert.match(main, /renderer_process_gone/);
  assert.match(main, /showRendererFailure\(lastRendererFailure\)/);
  assert.match(main, /Yaver could not open the dashboard/);
  assert.match(main, /location\.reload\(\)/);
  assert.match(main, /Open in browser/);
  assert.match(main, /if \(code === -3 \|\| description === "ERR_ABORTED"\) return/);
  assert.match(main, /startsWith\("data:text\/html"\)/);
  assert.match(main, /if \(\/ERR_ABORTED\|\\\(-3\\\)\/\.test\(error\?\.message/);
  assert.match(main, /scheduleRendererLoadRetry\(lastRendererFailure, retryURL\)/);
  assert.match(main, /renderer_load_retry_scheduled/);
  assert.ok(main.includes('if (/^https?:\\/\\//.test(loadedURL) && isAllowedAppUrl(loadedURL))'));
});

test("tray navigation has a safe fallback when the current page URL is empty or non-HTTP", () => {
  assert.match(main, /function dashboardUrlForTab\(tab\)/);
  assert.match(main, /tray_navigation_fallback/);
  assert.match(main, /DASHBOARD_PRODUCTION_URL\}\?tab=/);
  assert.doesNotMatch(main, /const origin = new URL\(mainWindow\.webContents\.getURL\(\)\)\.origin/);
});

test("recovery page strips auth params from the browser-bound URL (M3)", () => {
  const recovery = main.slice(main.indexOf("function showRendererFailure"), main.indexOf("function trayIcon"));
  // The failing URL is stripped before it is embedded into the recovery HTML.
  assert.match(recovery, /stripAuthFromUrl\(failure\.url\)/);
  const safeUrlIdx = recovery.indexOf("const safeUrl = JSON.stringify(browserUrl)");
  assert.ok(
    recovery.indexOf("stripAuthFromUrl(failure.url)") < safeUrlIdx,
    "browserUrl must be stripped before safeUrl is built",
  );
});

test("external navigation never opens a token-bearing URL (M3)", () => {
  const lock = main.slice(main.indexOf("const enforceNavigationLock"), main.indexOf("// Top-level navigations"));
  assert.match(lock, /stripAuthFromUrl\(target\)\.url/);
  assert.match(lock, /shell\.openExternal\(externalUrl\)/);
  assert.doesNotMatch(lock, /shell\.openExternal\(target\)/);
});

test("GUI_FAILURE_FIXTURE makes load and crash failures deterministic (DP9)", () => {
  assert.match(main, /GUI_FAILURE_FIXTURE \|\| ""\)\.trim\(\)/);
  const url = main.slice(main.indexOf("async function resolveDashboardUrl"), main.indexOf("// Auth capture"));
  assert.match(url, /guifailure\.invalid/);
  const windowCreate = main.slice(main.indexOf("async function createWindow"), main.indexOf("function showRendererFailure"));
  assert.match(windowCreate, /forcefullyCrashRenderer\(\)/);
  assert.match(main, /rendererRecoveryAttempts < 1/);
  assert.match(main, /if \(guiFailureFixture \|\| isQuitting \|\| rendererLoadRetryTimer\)/);
});
