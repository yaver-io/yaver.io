"use strict";

/**
 * Yaver GUI — preload bridge (sandboxed).
 *
 * Exposes a minimal, frozen API on window.yaver and runs a DOM observer that
 * detects task-completion states in the dashboard and asks the main process
 * for a native notification.
 *
 * Detection is deliberately conservative:
 *  - watches leaf text nodes for the exact status strings the chat header
 *    renders ({activeTask.status}: "completed" | "failed" | "stopped" |
 *    "review"), throttled to 1 Hz;
 *  - dedupes by status and suppresses repeats for 45s, so list rows and
 *    transient renders can't spam;
 *  - the task title is taken from the closest `span.truncate` sibling in the
 *    header (the dashboard's title span), with a fallback to the page title.
 *
 * Nothing here touches the renderer's state; it only reads the DOM.
 */

const { contextBridge, ipcRenderer } = require("electron");
const rendererOrigin = window.location.origin;
const trustedRenderer = ipcRenderer.sendSync("yaver:is-trusted-renderer-origin", rendererOrigin) === true;
const appVersion = trustedRenderer ? ipcRenderer.sendSync("yaver:get-app-version") : "unknown";

const TERMINAL_STATUSES = new Set(["completed", "failed", "stopped", "review"]);

let lastNotified = null; // { kind, at }
let notifiedTitle = null;

function statusText(node) {
  return node && node.nodeType === Node.TEXT_NODE ? (node.nodeValue || "").trim().toLowerCase() : "";
}

function nearestTitle(el) {
  // Header shape: <span class="truncate ...">{title}</span> next to the
  // status span. Walk up a few levels looking for a span.truncate sibling.
  let node = el;
  for (let depth = 0; depth < 4 && node; depth++) {
    const parent = node.parentElement;
    if (!parent) break;
    const titleSpan = parent.querySelector("span.truncate");
    if (titleSpan && titleSpan.textContent) {
      const t = titleSpan.textContent.trim();
      if (t && t.length < 160) return t;
    }
    node = parent;
  }
  return document.title || "";
}

function scanStatusTextNodes() {
  if (!document.body) return;
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const now = Date.now();
  const nodes = [];
  while (walker.nextNode()) {
    const value = statusText(walker.currentNode);
    if (TERMINAL_STATUSES.has(value)) {
      nodes.push({ value, node: walker.currentNode });
    }
  }
  if (nodes.length === 0) return;

  // Prefer the first match; the task header sits near the top of the DOM.
  const { value: kind, node } = nodes[0];
  if (lastNotified && lastNotified.kind === kind && now - lastNotified.at < 45_000) return;

  const title = nearestTitle(node);
  if (kind === "completed" && title === notifiedTitle) return; // same task re-render

  lastNotified = { kind, at: now };
  notifiedTitle = title;
  ipcRenderer.send("yaver:task-status", { kind, title });
}

let observerStarted = false;

function ensureObserver() {
  if (observerStarted) return;
  observerStarted = true;
  let pending = false;
  const observer = new MutationObserver(() => {
    if (pending) return;
    pending = true;
    setTimeout(() => {
      pending = false;
      scanStatusTextNodes();
    }, 1000);
  });
  observer.observe(document.documentElement, { childList: true, subtree: true, characterData: true });
  // First scan after the app settles.
  setTimeout(scanStatusTextNodes, 4000);
}

if (trustedRenderer) {
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", ensureObserver, { once: true });
  } else {
    ensureObserver();
  }
}

if (trustedRenderer) contextBridge.exposeInMainWorld("yaver", Object.freeze({
  surface: "desktop-gui",
  platform: process.platform,
  versions: {
    electron: process.versions.electron,
    chrome: process.versions.chrome,
    app: typeof appVersion === "string" ? appVersion : "unknown",
  },
  /**
   * Manual notification hook for the dashboard to adopt later
   * (e.g. on task terminal state, agent_question handoffs). No-op safe.
   */
  notify(title, body = "") {
    ipcRenderer.send("yaver:task-status", { kind: "notice", title: body ? `${title} — ${body}` : title });
  },
  /** Structured task lifecycle bridge used by the shared dashboard. */
  taskStatus(payload) {
    if (!payload || typeof payload !== "object") return false;
    ipcRenderer.send("yaver:task-status", {
      taskId: typeof payload.taskId === "string" ? payload.taskId : "",
      kind: typeof payload.kind === "string" ? payload.kind : "",
      title: typeof payload.title === "string" ? payload.title : "",
    });
    return true;
  },
  /** Toggle tray task notifications. Returns the new state. */
  setTaskNotifications(enabled) {
    ipcRenderer.send("yaver:set-task-notifications", Boolean(enabled));
    return Boolean(enabled);
  },
  /** Process-scoped availability; never mutates the OS power plan. */
  setKeepAwake(enabled) {
    ipcRenderer.send("yaver:set-keep-awake", Boolean(enabled));
    return Boolean(enabled);
  },
  setLaunchAtLogin(enabled) {
    ipcRenderer.send("yaver:set-launch-at-login", Boolean(enabled));
    return Boolean(enabled);
  },
  getDesktopStatus() {
    return ipcRenderer.invoke("yaver:get-desktop-status");
  },
  runConnectivityDiagnostics() {
    return ipcRenderer.invoke("yaver:run-desktop-connectivity-diagnostics");
  },
  applyConnectivityFix(id) {
    if (typeof id !== "string" || id.length > 80) return Promise.resolve({ ok: false, error: "Invalid repair id" });
    return ipcRenderer.invoke("yaver:apply-desktop-connectivity-fix", id);
  },
  openSystemRemoteDesktop(host) {
    if (typeof host !== "string" || host.length > 64) return Promise.resolve({ ok: false, error: "Invalid RDP host" });
    return ipcRenderer.invoke("yaver:open-system-rdp", host);
  },
  setAutomaticUpdates(enabled) {
    return ipcRenderer.invoke("yaver:set-automatic-updates", Boolean(enabled));
  },
  checkForUpdates() {
    return ipcRenderer.invoke("yaver:check-for-updates");
  },
  openDiagnosticLogs() {
    return ipcRenderer.invoke("yaver:open-diagnostic-logs");
  },
  /**
   * Ask the main process to uninstall Yaver from this computer. The renderer
   * never supplies a path; the plan is derived from the running process and
   * the user confirms in a native dialog. Store/package-manager builds return
   * { requiresUserAction: true, detail } instead of pretending to act.
   */
  uninstallApp() {
    return ipcRenderer.invoke("yaver:uninstall-app");
  },
  onUpdateStatus(listener) {
    if (typeof listener !== "function") return () => {};
    const handler = (_event, status) => listener(status);
    ipcRenderer.on("yaver:update-status", handler);
    return () => ipcRenderer.removeListener("yaver:update-status", handler);
  },
  onAgentStatus(listener) {
    if (typeof listener !== "function") return () => {};
    const handler = (_event, status) => listener(status);
    ipcRenderer.on("yaver:agent-status", handler);
    return () => ipcRenderer.removeListener("yaver:agent-status", handler);
  },
}));
