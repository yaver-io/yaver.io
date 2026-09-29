#!/usr/bin/env node

/**
 * Real Electron desktop smoke driver.
 *
 * This deliberately launches Electron itself (not Chromium pointed at a
 * dashboard URL), waits for the embedded Go-agent supervisor, and inspects
 * the pixels/DOM through Playwright's Electron transport.  A token is read
 * from the local Yaver config only when YAVER_ELECTRON_USE_LOCAL_AUTH=1; it is
 * never printed or written to an artifact.
 *
 * Usage:
 *   YAVER_ELECTRON_USE_LOCAL_AUTH=1 node e2e/electron-desktop-smoke.mjs
 *
 * Optional:
 *   YAVER_DASHBOARD_URL=http://localhost:3000/dashboard
 *   YAVER_ELECTRON_ARTIFACT_DIR=/tmp/yaver-electron-smoke
 */

import { _electron as electron } from "playwright";
import { mkdir, readFile } from "node:fs/promises";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..");
const electronRoot = join(repoRoot, "electron");
const executablePath = process.platform === "darwin"
  ? join(electronRoot, "node_modules", "electron", "dist", "Electron.app", "Contents", "MacOS", "Electron")
  : process.platform === "win32"
    ? join(electronRoot, "node_modules", "electron", "dist", "electron.exe")
    : join(electronRoot, "node_modules", "electron", "dist", "electron");
const artifactDir = process.env.YAVER_ELECTRON_ARTIFACT_DIR || join(tmpdir(), "yaver-electron-smoke");

async function localIdentity() {
  if (process.env.YAVER_ELECTRON_USE_LOCAL_AUTH !== "1") return { token: "", deviceId: "" };
  try {
    const config = JSON.parse(await readFile(join(homedir(), ".yaver", "config.json"), "utf8"));
    return {
      token: typeof config.auth_token === "string" ? config.auth_token.trim() : "",
      deviceId: typeof config.device_id === "string" ? config.device_id.trim() : "",
    };
  } catch {
    return { token: "", deviceId: "" };
  }
}

function sanitizedSummary(text) {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .slice(0, 24)
    .join(" | ")
    .slice(0, 1400);
}

async function waitForDesktopBridge(page, timeoutMs = 45_000) {
  const deadline = Date.now() + timeoutMs;
  let lastError = "trusted desktop preload bridge is absent";
  while (Date.now() < deadline) {
    try {
      const bridge = await page.evaluate(async () => {
        const api = window.yaver;
        if (!api) return { present: false };
        const status = await api.getDesktopStatus();
        return {
          present: true,
          platform: api.platform,
          appVersion: api.versions?.app,
          agentState: status?.agent?.state,
          agentPort: status?.agent?.port,
          keepAwake: status?.keepAwake,
        };
      });
      if (bridge.present) return bridge;
    } catch (error) {
      lastError = error?.message || String(error);
      if (!/Execution context was destroyed|navigation/i.test(lastError)) throw error;
    }
    await page.waitForLoadState("domcontentloaded", { timeout: 5_000 }).catch(() => {});
    await page.waitForTimeout(200);
  }
  throw new Error(`desktop bridge did not stabilize: ${lastError}`);
}

await mkdir(artifactDir, { recursive: true });
const app = await electron.launch({
  executablePath,
  args: [electronRoot],
  cwd: electronRoot,
  env: {
    ...process.env,
    YAVER_DEV: process.env.YAVER_DEV || "0",
    YAVER_ELECTRON_AUTOMATION: "1",
    YAVER_ELECTRON_USER_DATA_DIR: process.env.YAVER_ELECTRON_USER_DATA_DIR || join(artifactDir, "profile"),
  },
  timeout: 45_000,
});

const processLog = [];
for (const stream of [app.process().stdout, app.process().stderr]) {
  stream?.on("data", (chunk) => {
    const safe = String(chunk)
      .replace(/(token|password|secret|api[_-]?key)([=: ]+)[^\s]+/gi, "$1$2[REDACTED]")
      .replace(/Bearer\s+[^\s]+/gi, "Bearer [REDACTED]");
    processLog.push(...safe.split("\n").filter(Boolean));
    if (processLog.length > 120) processLog.splice(0, processLog.length - 120);
  });
}

try {
  const applicationName = await app.evaluate(({ app: electronApp }) => electronApp.getName());
  if (applicationName !== "Yaver") throw new Error(`desktop application name is ${applicationName}, expected Yaver`);
  const page = await app.firstWindow({ timeout: 45_000 });
  await page.waitForLoadState("domcontentloaded", { timeout: 45_000 });

  const bridge = await waitForDesktopBridge(page);

  if (!bridge.present) throw new Error("trusted desktop preload bridge is absent");
  if (bridge.agentPort !== 18080) throw new Error(`desktop agent port is ${bridge.agentPort}, expected 18080`);

  const deadline = Date.now() + 55_000;
  let desktopStatus = bridge;
  while (Date.now() < deadline && !["running", "adopted", "pairing"].includes(desktopStatus.agentState)) {
    await page.waitForTimeout(500);
    try {
      desktopStatus = await page.evaluate(async () => {
        const status = await window.yaver.getDesktopStatus();
        return { agentState: status.agent.state, agentPort: status.agent.port, agentDetail: status.agent.detail };
      });
    } catch (err) {
      // The dashboard may redirect through its auth gate while the embedded
      // agent is pairing. A destroyed JS context names navigation, not agent
      // failure; wait for the next trusted Yaver document and probe again.
      if (!String(err?.message || err).includes("Execution context was destroyed")) throw err;
      await page.waitForLoadState("domcontentloaded", { timeout: 30_000 }).catch(() => {});
    }
  }
  if (!["running", "adopted", "pairing"].includes(desktopStatus.agentState)) {
    throw new Error(
      `embedded agent never became available (state=${desktopStatus.agentState}, ` +
      `detail=${desktopStatus.agentDetail || "none"})\n${processLog.slice(-30).join("\n")}`,
    );
  }

  // The agent may refresh/rotate its host session during startup. Read the
  // local config only AFTER it is available; reading before launch seeds a
  // token the process has just superseded and falsely reports "session
  // expired" for a healthy desktop.
  const { token, deviceId: localDeviceId } = await localIdentity();
  if (token) {
    const dashboardOrigin = new URL(page.url()).origin;
    await page.evaluate((value) => {
      localStorage.setItem("yaver_auth_token", value);
      document.cookie = `yaver_auth_token=${value}; path=/; max-age=${60 * 60}; samesite=lax`;
    }, token);
    await page.goto(`${dashboardOrigin}/dashboard`, { waitUntil: "domcontentloaded", timeout: 60_000 }).catch((error) => {
      // The auth gate can replace this navigation with its own dashboard
      // navigation after seeing the injected cookie. Chromium names that
      // successful supersession ERR_ABORTED; judge the settled document.
      if (!/ERR_ABORTED/.test(error?.message || String(error))) throw error;
    });
    await page.waitForLoadState("domcontentloaded", { timeout: 30_000 }).catch(() => {});
    await page.waitForTimeout(4_000);
  }

  let localNode = null;
  let chatControls = null;
  let helloResult = null;
  let projectInventory = null;
  let connectivity = null;
  if (token && desktopStatus.agentState !== "pairing") {
    const infoResponse = await fetch("http://127.0.0.1:18080/info", {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!infoResponse.ok) throw new Error(`local agent /info returned HTTP ${infoResponse.status}`);
    const info = await infoResponse.json();
    const hostname = typeof info.hostname === "string" ? info.hostname.trim() : "";
    if (!hostname) throw new Error("local agent /info omitted hostname");

    const refresh = page.getByRole("button", { name: /^refresh$/i }).first();
    // Device polling can replace this button between visibility and click.
    // Refresh is only an accelerator; the row wait below is the operation
    // proof, so a detached advisory control must not fail the smoke.
    if (await refresh.isVisible().catch(() => false)) {
      await refresh.click({ timeout: 5_000 }).catch(() => {});
    }
    // A friendly alias may intentionally replace the raw hostname. Anchor the
    // card to the stable owner-scoped device identity, then treat its rendered
    // label as UI—not as a protocol identifier.
    if (!localDeviceId) throw new Error("local Yaver config omitted device_id");
    if (!/^[A-Za-z0-9._:-]+$/.test(localDeviceId)) throw new Error("local Yaver config contains an invalid device_id");
    const card = page.locator(`[data-device-id="${localDeviceId}"]`);
    await card.waitFor({ state: "visible", timeout: 30_000 });
    await card.scrollIntoViewIfNeeded();
    localNode = { hostname, visible: true, connected: false };

    if (process.env.YAVER_ELECTRON_CONNECT_LOCAL === "1") {
      const connect = card.getByRole("button", { name: /connect/i }).first();
      await connect.click();
      await card.getByRole("button", { name: /close workspace/i }).waitFor({ state: "visible", timeout: 45_000 });
      localNode.connected = true;
    }
  }

  // Closed-loop project proof for the real Desktop shell. The headless agent
  // response is canonical; the assertion then verifies that the same names
  // survive the Desktop/Web merge and reach rendered project cards.
  if (process.env.YAVER_ELECTRON_INSPECT_PROJECTS === "1") {
    if (!token || !localNode?.connected) {
      throw new Error("project inspection requires YAVER_ELECTRON_USE_LOCAL_AUTH=1 and YAVER_ELECTRON_CONNECT_LOCAL=1");
    }
    const response = await fetch("http://127.0.0.1:18080/projects", {
      headers: { Authorization: `Bearer ${token}` },
    });
    if (!response.ok) throw new Error(`local agent /projects returned HTTP ${response.status}`);
    const payload = await response.json();
    const canonicalRows = Array.isArray(payload) ? payload : Array.isArray(payload?.projects) ? payload.projects : [];
    const canonicalNames = canonicalRows
      .map((row) => typeof row?.name === "string" ? row.name.trim() : "")
      .filter(Boolean);
    if (canonicalNames.length === 0) throw new Error("local agent /projects returned no named projects");

    await page.getByText(/^Projects$/, { exact: true }).first().click();
    const cards = page.locator('[data-testid="project-card"]');
    await cards.first().waitFor({ state: "visible", timeout: 45_000 });
    const renderedNames = (await cards.evaluateAll((nodes) => nodes
      .map((node) => node.getAttribute("data-project-name") || "")
      .filter(Boolean)));
    const missing = canonicalNames.filter((name) => !renderedNames.includes(name));
    if (missing.length > 0) {
      throw new Error(`Desktop Projects hid canonical agent rows: ${missing.slice(0, 8).join(", ")}`);
    }
    projectInventory = {
      canonicalCount: canonicalNames.length,
      renderedCount: renderedNames.length,
      parity: true,
    };
  }

  if (process.env.YAVER_ELECTRON_INSPECT_SOURCE === "1") {
    await page.getByText(/^Source$/, { exact: true }).first().click();
    await page.getByRole("heading", { name: /^Git$/ }).waitFor({ state: "visible", timeout: 30_000 });
  }

  if (process.env.YAVER_ELECTRON_INSPECT_CONNECTIVITY === "1") {
    await page.getByText(/^Health$/, { exact: true }).first().click();
    await page.getByRole("heading", { name: "Connectivity & Remote Access" }).waitFor({ state: "visible", timeout: 30_000 });
    const report = await page.evaluate(() => window.yaver.runConnectivityDiagnostics());
    if (!Array.isArray(report?.checks) || !report.checks.some((check) => check.id === "desktop-agent")) {
      throw new Error("desktop connectivity report omitted the local-agent operation probe");
    }
    connectivity = {
      platform: report.platform,
      checks: report.checks.map((check) => ({ id: check.id, status: check.status })),
    };
  }

  if (process.env.YAVER_ELECTRON_INSPECT_CHAT === "1") {
    await page.getByText(/^Chat$/, { exact: true }).first().click();
    await page.locator("textarea").first().waitFor({ state: "visible", timeout: 20_000 });
    chatControls = await page.locator("button").evaluateAll((buttons) => buttons
      .map((button) => ({
        text: (button.textContent || "").trim().replace(/\s+/g, " ").slice(0, 100),
        title: button.getAttribute("title") || "",
        aria: button.getAttribute("aria-label") || "",
      }))
      .filter((row) => row.text || row.title || row.aria)
      .slice(-40));
    if (process.env.YAVER_ELECTRON_INSPECT_PICKER === "1") {
      await page.getByTitle(/Edit agent, provider, model/i).click();
      chatControls = await page.locator("button").evaluateAll((buttons) => buttons
        .map((button) => ({
          text: (button.textContent || "").trim().replace(/\s+/g, " ").slice(0, 120),
          title: button.getAttribute("title") || "",
          aria: button.getAttribute("aria-label") || "",
        }))
        .filter((row) => row.text || row.title || row.aria)
        .slice(-70));
    }
  }

  if (process.env.YAVER_ELECTRON_HELLO === "1") {
    await page.getByText(/^Chat$/, { exact: true }).first().click();
    const composer = page.locator("textarea").first();
    await composer.waitFor({ state: "visible", timeout: 20_000 });

    await page.getByTitle(/Edit agent, provider, model/i).click();
    const openCode = page.getByRole("button", { name: /opencode/i }).filter({ hasText: /opencode/i }).first();
    await openCode.click();
    await page.getByRole("button", { name: /^DeepSeek$/ }).click();
    await page.getByRole("button", { name: /^DeepSeek V4 Flash$/ }).click();
    await page.getByRole("button", { name: /use this provider/i }).click();
    await page.getByText("DeepSeek V4 Flash", { exact: true }).first().waitFor({ state: "visible", timeout: 10_000 });

    const marker = "HELLO_DESKTOP_E2E";
    const prompt = `Reply with exactly ${marker} and nothing else. Do not edit files or run commands.`;
    await composer.fill(prompt);
    await page.getByRole("button", { name: /^Start task$/ }).click();
    const activeTitle = page.getByText(prompt, { exact: true }).first();
    await activeTitle.waitFor({ state: "visible", timeout: 30_000 });
    await page.getByText(marker, { exact: true }).last().waitFor({ state: "visible", timeout: 180_000 });
    // Output can arrive before the runner process exits. Tie completion to
    // THIS active task header; a generic "completed" locator can match an old
    // sidebar row and kill the desktop-owned agent mid-finalization.
    const activeHeader = activeTitle.locator("xpath=..");
    await activeHeader.getByRole("button", { name: /^Stop$/ }).waitFor({ state: "hidden", timeout: 90_000 });
    const terminalStatus = activeHeader.getByText(/^(review|completed)$/i).first();
    await terminalStatus.waitFor({ state: "visible", timeout: 30_000 });
    // A coding task may legitimately stop at review. Complete it through the
    // same desktop surface so the smoke proves the whole terminal lifecycle,
    // while still accepting review as the renderable state that ended coding.
    if ((await terminalStatus.innerText()).trim().toLowerCase() === "review") {
      await activeHeader.getByRole("button", { name: /^Complete$/ }).click();
      await activeHeader.getByText(/^completed$/i).waitFor({ state: "visible", timeout: 20_000 });
    }
    const helloShot = join(artifactDir, "desktop-hello.png");
    await page.screenshot({ path: helloShot, fullPage: true });
    helloResult = { ok: true, marker, model: "deepseek/deepseek-flash", screenshot: helloShot };
  }

  const screenshot = join(artifactDir, "desktop-dashboard.png");
  await page.screenshot({ path: screenshot, fullPage: true });
  const body = await page.locator("body").innerText().catch(() => "");
  const signedIn = !/sign in|continue with google|email address/i.test(body);

  console.log(JSON.stringify({
    ok: true,
    url: page.url().replace(/[?#].*$/, ""),
    signedIn,
    applicationName,
    bridge: { ...bridge, agentState: desktopStatus.agentState },
    localNode,
    projectInventory,
    connectivity,
    chatControls,
    helloResult,
    screenshot,
    visible: sanitizedSummary(body),
  }, null, 2));
} finally {
  await app.close();
}
