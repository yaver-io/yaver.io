import { chromium, devices } from "playwright";
import { mkdir } from "node:fs/promises";
import { resolve } from "node:path";

const appURL = (process.env.MOBILE_WEB_URL || "").replace(/\/$/, "");
const token = process.env.YAVER_TEST_TOKEN || "";
const requireWorkspaceReady = process.env.REQUIRE_WORKSPACE_READY === "1";
const createTodoWorkspace = process.env.CREATE_TODO_WORKSPACE === "1";
const boxHost = (process.env.VIBE_BOX_HOST || "").replace(/\/$/, "");
const boxDeviceId = process.env.VIBE_DEVICE_ID || "";
const workspaceAppName = process.env.WORKSPACE_APP_NAME || `Todo Validation ${new Date().toISOString().replace(/[-:TZ.]/g, "").slice(0, 12)}`;
const artifactRoot = resolve(process.env.E2E_ARTIFACT_DIR || "test-results/mobile-workspace-live");
if (!appURL || !token) {
  throw new Error("MOBILE_WEB_URL and YAVER_TEST_TOKEN are required");
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

function safeErrorClasses(messages) {
  const text = messages.join("\n").toLowerCase();
  return {
    count: messages.length,
    auth: /401|403|auth|token|sign.?in/.test(text),
    cors: /cors|cross-origin/.test(text),
    network: /fetch|network|connect|transport|timeout/.test(text),
  };
}

const iphone = devices["iPhone 15 Pro"];
await mkdir(`${artifactRoot}/video`, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.CHROMIUM_PATH || undefined });
const context = await browser.newContext({
  ...iphone,
  recordVideo: { dir: `${artifactRoot}/video`, size: iphone.viewport },
});
await context.addInitScript((authToken) => {
  // A fresh browser context otherwise executes the native reinstall guard and
  // intentionally clears the token before AuthContext can validate it.
  localStorage.setItem("yaver_installed", "1");
  localStorage.setItem("yaver.secure.yaver_auth_token", authToken);
}, token);
const page = await context.newPage();
if (boxHost && boxDeviceId) {
  // Browser automation cannot use the native UDP/QUIC discovery path. Route
  // the selected, real device through the local authenticated SSH forward;
  // every readiness/provider request still reaches the real agent endpoint.
  await page.route("**/devices/list?*", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    const rows = Array.isArray(body?.devices) ? body.devices : Array.isArray(body) ? body : [];
    const rewritten = rows.map((row) => {
      const id = row?.deviceId || row?.id;
      if (typeof id !== "string" || !id.startsWith(boxDeviceId)) return row;
      const url = new URL(boxHost);
      return {
        ...row,
        host: url.hostname,
        quicHost: url.hostname,
        port: Number(url.port || (url.protocol === "https:" ? 443 : 80)),
        quicPort: Number(url.port || (url.protocol === "https:" ? 443 : 80)),
        tunnelUrl: boxHost,
        publicEndpoints: [boxHost],
      };
    });
    await route.fulfill({
      response,
      json: Array.isArray(body) ? rewritten : { ...body, devices: rewritten },
    });
  });
  const forwardAgentProbe = async (route) => {
    const incoming = new URL(route.request().url());
    const markers = ["/mobile-workspace/status", "/agent/runners", "/runner-auth/status"];
    const marker = markers.find((candidate) => incoming.pathname.includes(candidate));
    if (!marker) return route.continue();
    const suffix = incoming.pathname.slice(incoming.pathname.lastIndexOf(marker));
    try {
      const response = await route.fetch({ url: `${boxHost}${suffix}` });
      await route.fulfill({ response });
    } catch (error) {
      if (page.isClosed() || context.pages().length === 0) return;
      throw new Error(`agent probe forwarding failed for ${suffix}`);
    }
  };
  for (const marker of ["mobile-workspace/status", "agent/runners", "runner-auth/status"]) {
    await page.route(`**/${marker}`, forwardAgentProbe);
    await page.route(`**/peer/*/${marker}`, forwardAgentProbe);
  }
}
page.on("dialog", (dialog) => void dialog.accept());
const errors = [];
const transportMessages = [];
const probes = { auth: null, devices: null, settings: null, workspace: null, storage: null, requestFailures: {}, failedPaths: [], failedResponses: [] };
page.on("pageerror", (error) => errors.push(`page: ${error.message}`));
page.on("console", (message) => {
  if (message.type() === "error") errors.push(`console: ${message.text()}`);
  if (message.text().includes("[QUIC]") && transportMessages.length < 20) {
    transportMessages.push(message.text().replaceAll(boxHost, "[box-forward]"));
  }
});
page.on("response", async (response) => {
  const pathname = new URL(response.url()).pathname;
  if (response.status() >= 400 && probes.failedResponses.length < 8) {
    probes.failedResponses.push({ path: pathname, status: response.status() });
  }
  if (pathname.endsWith("/auth/validate")) {
    probes.auth = { status: response.status() };
  } else if (pathname.endsWith("/devices/list")) {
    const body = await response.json().catch(() => ({}));
    probes.devices = {
      status: response.status(),
      count: Array.isArray(body.devices) ? body.devices.length : Array.isArray(body) ? body.length : null,
    };
  } else if (pathname.endsWith("/settings")) {
    const body = await response.json().catch(() => ({}));
    probes.settings = {
      status: response.status(),
      primaryConfigured: Boolean(body.primaryDeviceId ?? body.settings?.primaryDeviceId),
    };
  } else if (pathname.endsWith("/mobile-workspace/status")) {
    const body = await response.json().catch(() => ({}));
    probes.workspace = {
      status: response.status(),
      origin: new URL(response.url()).origin,
      layout: body?.layout ? {
        managed: body.layout.mode === "managed-default",
        explicitPathsSupported: body.layout.explicitPathsSupported === true,
        cleanupRequiresExplicit: body.layout.cleanupRequiresExplicit === true,
        separated: typeof body.layout.repositories === "string" &&
          typeof body.layout.worktrees === "string" &&
          body.layout.repositories !== body.layout.worktrees,
      } : null,
    };
  }
});
page.on("requestfailed", (request) => {
  const pathname = new URL(request.url()).pathname;
  const kind = pathname.endsWith("/api/mobile-config") ? "config"
    : pathname.endsWith("/auth/validate") ? "auth"
      : pathname.endsWith("/devices/list") ? "devices"
        : "other";
  probes.requestFailures[kind] = (probes.requestFailures[kind] || 0) + 1;
  if (probes.failedPaths.length < 5) probes.failedPaths.push(pathname);
});

try {
  await page.goto(`${appURL}/phone-projects`, { waitUntil: "domcontentloaded", timeout: 120_000 });
  await page.getByText("Mobile Workspace", { exact: true }).waitFor({ timeout: 60_000 });
  probes.storage = await page.evaluate(() => ({
    installed: localStorage.getItem("yaver_installed") === "1",
    tokenPresent: Boolean(localStorage.getItem("yaver.secure.yaver_auth_token")),
  }));
  const viewport = page.viewportSize();
  const device = await page.evaluate(() => ({
    dpr: window.devicePixelRatio,
    touch: navigator.maxTouchPoints,
    ua: navigator.userAgent,
  }));
  assert(viewport?.width === iphone.viewport.width && viewport?.height === iphone.viewport.height, "not using the iPhone device viewport");
  assert(device.touch > 0 && /Mobile|iPhone/i.test(device.ua) && device.dpr >= 2, "not using a genuine mobile/touch context");

  await page.getByText("+ New mobile app", { exact: true }).click();
  await page.getByPlaceholder("My app").fill(workspaceAppName);
  await page.getByText("Next", { exact: true }).click();

  await page.getByText("2. Development target", { exact: true }).waitFor();
  let primaryHydrated = true;
  try {
    await page.getByText("Primary device · Recommended", { exact: true }).waitFor({ timeout: 60_000 });
  } catch {
    primaryHydrated = false;
  }
  const placement = await page.locator("body").innerText().then((text) => ({
    primary: text.includes("Primary device · Recommended"),
    connected: text.includes("Connected machine"),
    other: text.includes("Other online box"),
    missing: text.includes("Connect a Yaver machine first"),
  }));
  assert(
    primaryHydrated && placement.primary,
    `primary recommendation missing: ${JSON.stringify(placement)} probes=${JSON.stringify(probes)} errors=${JSON.stringify(safeErrorClasses(errors))}`,
  );
  const readyState = page.getByText("Ready", { exact: true }).first();
  const upgradeState = page.getByText("Agent update required", { exact: true });
  const unreachableState = page.getByText("Couldn't audit this remote box", { exact: true });
  try {
    if (requireWorkspaceReady) {
      // The first probe is allowed to race initial transport attachment. The
      // surface must expose an in-place retry, and one tap after attachment
      // must converge to the real readiness result.
      for (let attempt = 0; attempt < 6 && !(await readyState.isVisible().catch(() => false)); attempt += 1) {
        const retry = page.getByText("Retry readiness check →", { exact: true });
        if (await retry.isVisible().catch(() => false)) await retry.click();
        await page.waitForTimeout(4_000);
      }
      await readyState.waitFor({ timeout: 30_000 });
    } else {
      await page.waitForFunction(() => {
        const text = document.body.innerText;
        return text.includes("Agent update required") || text.includes("Couldn't audit this remote box") || /(^|\n)Ready($|\n)/.test(text);
      }, undefined, { timeout: 60_000 });
    }
  } catch {
    const state = await page.locator("body").innerText().then((text) => ({
      targetStep: text.includes("2. Development target"),
      runnerSection: text.includes("Runner"),
      notConfigured: text.includes("Not configured"),
      notInstalled: text.includes("Not installed"),
      checking: text.includes("Testing runner…"),
      connectMachine: text.includes("Connect a Yaver machine first"),
    }));
    throw new Error(`workspace readiness did not settle: ${JSON.stringify(state)} probes=${JSON.stringify(probes)} errors=${JSON.stringify(safeErrorClasses(errors))}`);
  }
  const targetText = await page.locator("body").innerText();
  await page.screenshot({ path: `${artifactRoot}/workspace-readiness.png`, fullPage: true });
  assert(!/remoteless/i.test(targetText), "Mobile Workspace must not offer Remoteless placement");
  assert(/Yaver Serverless/i.test(targetText), "fixed Yaver Serverless stack is not visible");
  if (requireWorkspaceReady) {
    assert(
      probes.workspace?.layout?.managed &&
        probes.workspace.layout.explicitPathsSupported &&
        probes.workspace.layout.cleanupRequiresExplicit &&
        probes.workspace.layout.separated,
      `managed repository/worktree contract missing from the real mobile lane: ${JSON.stringify(probes.workspace)}`,
    );
  }

  if (await unreachableState.isVisible()) {
    assert(await page.getByText("Retry readiness check →", { exact: true }).count() === 1, "unreachable readiness has no retry action");
    assert(!targetText.includes("Not configured"), "an unreachable agent was misreported as an unconfigured runner");
    assert(!requireWorkspaceReady, `Mobile Workspace readiness is required, but the selected box could not be audited: ${JSON.stringify(probes)} transports=${JSON.stringify(transportMessages)}`);
    console.log("live Mobile Workspace unreachable-agent recovery route passed");
  } else if (await upgradeState.isVisible()) {
    assert(await page.getByText("Update Yaver agent →", { exact: true }).count() === 1, "agent upgrade has no in-place action");
    assert(!targetText.includes("Not configured"), "an old agent was misreported as an unconfigured runner");
    assert(!requireWorkspaceReady, "Mobile Workspace readiness is required, but the selected box still runs an older agent");
    console.log("live Mobile Workspace old-agent recovery route passed");
  } else {
    await page.getByText(/OpenCode · (Preferred|Recommended)/).waitFor({ timeout: 30_000 });
    await page.getByText("deepseek/deepseek-v4-flash", { exact: true }).waitFor({ timeout: 30_000 });

    // Next runs the real remote provider probe before persisting the selected
    // device, runner, model, mode, and provider as the task defaults.
    await page.getByText("Next", { exact: true }).click();
    await page.getByText("3. Git provider", { exact: true }).waitFor({ timeout: 90_000 });
    await page.getByText("Yaver Git · Ready", { exact: true }).waitFor();
    await page.getByText("Mirror now", { exact: true }).click();
    await page.getByText(/GitHub · (Connected|Clone ready)/).waitFor({ timeout: 30_000 });
    await page.getByText(/GitLab · (Connected|Clone ready)/).waitFor({ timeout: 30_000 });
    assert(await page.getByText(/GitHub · (Connected|Clone ready)/).count() === 1, "GitHub status is ambiguous");
    assert(await page.getByText(/GitLab · (Connected|Clone ready)/).count() === 1, "GitLab status is ambiguous");

    if (createTodoWorkspace) {
      // Keep the validation workspace in Yaver Managed Git. Provider discovery
      // is proven above; creating a GitHub/GitLab repository would be a second,
      // unnecessary external mutation for this development-path test.
      await page.getByText("Yaver Git · Ready", { exact: true }).click();
      await page.getByText("Next", { exact: true }).click();
      await page.getByText("4. Quick survey (optional)", { exact: true }).waitFor();
      await page.getByText("Skip survey", { exact: true }).click();
      await page.getByText("Next", { exact: true }).click();
      await page.getByText("5. Setting up your project", { exact: true }).waitFor();
      await page.getByText("Next", { exact: true }).click();
      await page.getByText("6. Branding (optional)", { exact: true }).waitFor();
      await page.getByText("Next", { exact: true }).click();
      await page.getByText("7. Describe the app", { exact: true }).waitFor();
      await page.getByPlaceholder(/Tell Yaver what you're building/i).fill(
        "Build a small Expo React Native todo app with add, complete, filter, and delete actions. Keep the UI accessible, persist todos locally, and include a clear empty state.",
      );
      await page.screenshot({ path: `${artifactRoot}/todo-ready-to-create.png`, fullPage: true });
      await page.getByText("Create workspace", { exact: true }).click();
      await page.waitForURL(/\/phone-project\//, { timeout: 180_000 });
      await page.getByText(workspaceAppName, { exact: true }).first().waitFor({ timeout: 60_000 });
      await page.screenshot({ path: `${artifactRoot}/todo-created.png`, fullPage: true });
      console.log("live Mobile Workspace todo creation passed");
    }
    console.log("live Mobile Workspace onboarding passed");
  }

  assert(errors.length === 0, `Mobile Workspace browser errors: classes=${JSON.stringify(safeErrorClasses(errors))} probes=${JSON.stringify(probes)}`);
} finally {
  await page.unrouteAll({ behavior: "ignoreErrors" }).catch(() => {});
  await context.close();
  await browser.close();
}
