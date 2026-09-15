import { chromium, devices, expect, test } from "@playwright/test";
import { copyFileSync, mkdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join, resolve } from "node:path";
import { profileFor, viewportMatchesSurface } from "../../web/lib/surfaceViewports";

type Host = "sfmg" | "talos";

const host = String(process.env.E2E_SDK_HOST || "").toLowerCase() as Host;
const hostUrl = String(process.env.E2E_SDK_HOST_URL || "").replace(/\/$/, "");
const projectPath = String(process.env.E2E_SDK_PROJECT_PATH || "");
const persistentProfile = String(process.env.E2E_SDK_PROFILE || "");
const agent = String(process.env.E2E_AGENT_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const selectedDevice = String(process.env.E2E_SDK_DEVICE_ID || "");
const vibePrompt = String(process.env.E2E_SDK_VIBE_PROMPT || "");
const revertPrompt = String(process.env.E2E_SDK_REVERT_PROMPT || "");
const changedMarker = String(process.env.E2E_SDK_CHANGED_MARKER || "");
const restoredMarker = String(process.env.E2E_SDK_RESTORED_MARKER || "");
const hostIsAgentPreview = process.env.E2E_SDK_HOST_IS_AGENT_PREVIEW === "1";
const artifacts = resolve(process.env.E2E_VIDEO_DIR || "test-results/sdk-host-dogfood");
const chromiumExecutable = String(process.env.YAVER_CHROMIUM_PATH || "").trim();
const requestedRunner = String(process.env.E2E_SDK_RUNNER || "").trim();
const requestedModel = String(process.env.E2E_SDK_MODEL || "").trim();

function localConfig(): { auth_token: string; device_id: string; cached_relay_password?: string; relay_password?: string; code?: { runner?: string; model?: string } } {
  return JSON.parse(readFileSync(join(homedir(), ".yaver", "config.json"), "utf8"));
}

async function agentJSON(path: string, token: string, relayPassword: string, init?: RequestInit): Promise<any> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 20_000);
  try {
    const response = await fetch(`${agent}${path}`, {
      ...init,
      signal: init?.signal || controller.signal,
      headers: {
        Authorization: `Bearer ${token}`,
        "Content-Type": "application/json",
        ...(relayPassword ? { "X-Relay-Password": relayPassword } : {}),
        ...init?.headers,
      },
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(`${path} returned ${response.status}: ${JSON.stringify(body).slice(0, 500)}`);
    return body;
  } finally {
    clearTimeout(timer);
  }
}

test("SDK host auto-launches browser Dogfood and keeps real pixels through reload", async () => {
  test.skip(!(["sfmg", "talos"] as string[]).includes(host), "set E2E_SDK_HOST=sfmg|talos");
  test.skip(!hostUrl || !projectPath || !persistentProfile, "needs host URL, exact project path, and approved persistent profile");

  const cfg = localConfig();
  const deviceId = selectedDevice || cfg.device_id;
  const relayPassword = cfg.cached_relay_password || cfg.relay_password || "";
  test.skip(!cfg.auth_token || !deviceId, "Yaver OAuth and a selected device identity are required");
  mkdirSync(artifacts, { recursive: true });

  const surface = profileFor("mobile");
  const descriptor = devices[surface.playwrightDevice!];
  const context = await chromium.launchPersistentContext(persistentProfile, {
    ...descriptor,
    ...(chromiumExecutable ? { executablePath: chromiumExecutable } : {}),
    headless: true,
    recordVideo: { dir: artifacts },
  });
  // Scope relay credentials to the selected Yaver agent only. Chromium
  // otherwise sends context-wide extra headers to Convex/CDN origins too.
  if (relayPassword && agent.startsWith("https://public.yaver.io/")) {
    await context.route(`${agent}/**`, (route) => route.continue({
      headers: {
        ...route.request().headers(),
        Authorization: `Bearer ${cfg.auth_token}`,
        "X-Relay-Password": relayPassword,
      },
    }));
  }
  await context.addInitScript(({ authToken, deviceId, runner, model, activeHost, agentUrl }) => {
    localStorage.setItem("yaver_feedback_auth_token", authToken);
    localStorage.setItem("yaver_feedback_selected_device", deviceId);
    localStorage.setItem("yaver_feedback_preferred_runner", runner || "codex");
    localStorage.setItem("yaver_feedback_preferred_model", model || "gpt-5.6-sol");
    // Discovery must honor the selected device even if an earlier run cached
    // a healthy URL for another machine.
    if (activeHost === "sfmg") {
      // This arc proves cold auto-launch. A saved successful runtime is only
      // inventory and would intentionally route the host entry to compact
      // controls instead of exercising startup + browser handoff.
      localStorage.removeItem("@yaver/dogfood_runtime_selection:com.kivanccakmak.sfmg");
      // SFMG's current web MMKV shim uses \`<store-id>:<key>\` and a type
      // prefix. Mirror the implementation, not the old native delimiter.
      localStorage.setItem("sfmg.default:sfmg.yaverEnabled", "b:1");
      localStorage.setItem("sfmg.default:sfmg.yaverToken", "s:" + authToken);
      localStorage.setItem("sfmg.default:sfmg.yaverAgentUrl", "s:" + agentUrl);
    } else {
      sessionStorage.setItem("talos_token", "browser-contract-token");
      localStorage.setItem("talos_has_launched", "1");
      localStorage.setItem("talos_user", JSON.stringify({
        isAdmin: true,
        orgRole: "owner",
        email: "browser-contract@example.invalid",
        permissions: {},
        uiEntitlements: [],
        uiHiddenWidgetKeys: {},
      }));
    }
  }, {
    authToken: cfg.auth_token,
    deviceId,
    runner: requestedRunner || cfg.code?.runner,
    model: requestedModel || cfg.code?.model,
    activeHost: host,
    agentUrl: agent,
  });

  if (host === "talos") {
    await context.route("**/api/auth/me", (route) => route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        ok: true,
        user: {
          isAdmin: true,
          orgRole: "owner",
          email: "browser-contract@example.invalid",
          permissions: {},
          uiEntitlements: [],
          uiHiddenWidgetKeys: {},
        },
        usageHours: { allowed: true },
      }),
    }));
  }

  // Persistent approval profiles may restore tabs from a prior interrupted
  // run. Start with one deliberate host page so an old about:blank tab can
  // never masquerade as the preview that this tap must auto-launch.
  const restoredPages = context.pages();
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  let scopedPreviewReached = false;
  await Promise.all(restoredPages.map((item) => item.close().catch(() => undefined)));
  page.on("console", (message) => {
    if (!/Failed to load resource|Unexpected text node/.test(message.text())) {
      console.log(`[sdk-host:${message.type()}] ${message.text()}`);
    }
  });
  page.on("pageerror", (error) => console.log(`[sdk-host:pageerror] ${String(error)}`));
  page.on("response", (response) => {
    if (response.status() >= 400 && !response.url().includes("/blackbox/command-stream")) {
      console.log(`[sdk-host:http] ${response.status()} ${response.url()}`);
    }
  });
  context.on("page", (candidate) => {
    if (candidate === page) return;
    candidate.on("console", (message) => {
      if (!/Failed to load resource/.test(message.text())) {
        console.log(`[sdk-preview:${message.type()}] ${message.text()}`);
      }
    });
    candidate.on("pageerror", (error) => console.log(`[sdk-preview:pageerror] ${String(error)}`));
    candidate.on("response", (response) => {
      if (response.ok() && response.url().startsWith(`${agent}/dev`)) scopedPreviewReached = true;
      if (response.status() >= 400 && !response.url().includes("/blackbox/command-stream")) {
        console.log(`[sdk-preview:http] ${response.status()} ${response.url()}`);
      }
    });
  });
  const video = page.video();
  const checkpoint = async (name: string) => {
    console.log(`[sdk-dogfood] ${name}`);
    await Promise.race([
      page.screenshot({
        path: join(artifacts, `${host}-${name.replace(/[^a-z0-9]+/gi, "-").toLowerCase()}.png`),
        animations: "disabled",
        timeout: 5_000,
      }).catch(() => undefined),
      new Promise<void>((resolve) => setTimeout(resolve, 6_000)),
    ]);
  };
  try {
    const viewport = page.viewportSize()!;
    const verdict = viewportMatchesSurface("mobile", {
      ...viewport,
      isMobile: descriptor.isMobile,
      hasTouch: descriptor.hasTouch,
    }, 0);
    expect(verdict.ok, verdict.reason).toBe(true);

    if (!hostIsAgentPreview) {
      await agentJSON("/dev/stop", cfg.auth_token, relayPassword, { method: "POST", body: "{}" }).catch(() => undefined);
    }
    const entryUrl = host === "sfmg" ? `${hostUrl}/settings` : `${hostUrl}/app/more`;
    await checkpoint("before-host-navigation");
    await page.goto(entryUrl, { waitUntil: "domcontentloaded", timeout: 120_000 });
    await checkpoint("host-loaded");
    if (host === "talos") {
      await page.getByText("Settings", { exact: true }).first().click();
    }
    const entry = page.getByRole("button", {
      // RN-web folds the row's visible hint into its accessibility name even
      // when native uses the shorter accessibilityLabel. Exclude the adjacent
      // Settings row without coupling the arc to translated helper copy.
      name: host === "sfmg" ? /^Dogfood(?! Settings)/ : "Dogfood Talos",
    });
    await expect(entry).toBeVisible({ timeout: 120_000 });
    await checkpoint("dogfood-entry-visible");
    await entry.click();
    // The entry lives near the bottom of the long RN-web Settings document.
    // Return the document viewport to its origin so the portaled setup sheet
    // is judged where a native modal lives, not below the old page scroll.
    await page.evaluate(() => window.scrollTo(0, 0));
    await checkpoint("dogfood-entry-opened");

    // Remote project discovery can have several checkouts with similar app
    // metadata. The video must show and select the exact disposable checkout;
    // never let a fuzzy default launch an unrelated repo.
    const wizardTitle = page.getByText("Connect the app to its checkout", { exact: true });
    await wizardTitle.waitFor({ state: "visible", timeout: 30_000 }).catch(() => undefined);
    await checkpoint("onboarding-settled");
    if (await wizardTitle.isVisible().catch(() => false)) {
      // Setup auto-starts as soon as the saved/default choices become ready.
      // Clicking the checkout text races that transition and can target a
      // sheet that has already closed. The operation-level assertion below is
      // authoritative: the serving process must report this exact workDir.
      await checkpoint("checkout-auto-starting");
      const browserLogs = page.getByLabel(/^Browser Logs/);
      await expect(browserLogs).toBeVisible({ timeout: 30_000 });
      await expect.poll(async () => {
        const text = await browserLogs.innerText().catch(() => "");
        return /Metro waiting|Logs for your project|Bundl(?:ed|ing)|Starting project at/.test(text);
      }, { timeout: 120_000, intervals: [500, 1_000, 2_000] }).toBe(true);
      await checkpoint("browser-logs-live");
    }

    await expect.poll(async () => {
      try {
        const status = await agentJSON("/dev/status", cfg.auth_token, relayPassword);
        return status.running === true && status.serving === true && status.workDir === projectPath;
      } catch {
        return false;
      }
    }, { timeout: 180_000, intervals: [1_000, 2_000, 3_000] }).toBe(true);
    console.log(`[sdk-dogfood] pages after start: ${context.pages().map((item) => item.url()).join(" | ")}`);
    await expect.poll(() => scopedPreviewReached, { timeout: 60_000 }).toBe(true);

    const marker = host === "sfmg" ? /Dogfood|Settings/i : /Dashboard|Quick Actions/i;
    await expect.poll(async () => {
      for (const candidate of context.pages().filter((item) => item !== page)) {
        if (host === "sfmg" && candidate.url() !== "about:blank") {
          await candidate.goto(`${agent}/dev-web/settings`, { waitUntil: "domcontentloaded", timeout: 120_000 }).catch(() => undefined);
        }
        if (marker.test(await candidate.locator("body").innerText().catch(() => ""))) return true;
      }
      return false;
    }, { timeout: 120_000 }).toBe(true);
    await checkpoint("preview-launched");

    // The product auto-launches. A separate Launch button would regress the
    // one-action host entry into a two-step flow.
    await expect(page.getByRole("button", { name: /^Launch(?: Dogfood)?$/i })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Open Dogfood controls" })).toBeVisible();
    await checkpoint("quick-controls-visible");
    await page.getByRole("button", { name: "Open Dogfood controls" }).click();
    await checkpoint("quick-controls-open");
    const reloadResponse = page.waitForResponse((response) => response.url().includes("/dogfood/reload"), { timeout: 120_000 });
    await page.getByRole("button", { name: "Reload" }).click();
    await checkpoint("reload-clicked");
    expect((await reloadResponse).ok()).toBe(true);
    await checkpoint("reload-acknowledged");

    await expect.poll(async () => {
      for (const candidate of context.pages().filter((item) => item !== page)) {
        if (marker.test(await candidate.locator("body").innerText().catch(() => ""))) return true;
      }
      return false;
    }, { timeout: 60_000 }).toBe(true);

    if (vibePrompt && revertPrompt && changedMarker && restoredMarker) {
      const findPreview = async () => {
        for (const item of context.pages().filter((candidate) => candidate !== page)) {
          if (marker.test(await item.locator("body").innerText().catch(() => ""))) return item;
        }
        return undefined;
      };
      const waitForChatReady = async () => {
        await expect(page.getByPlaceholder("Follow up…").last()).toBeEditable({ timeout: 12 * 60_000 });
      };
      const renderAndAssert = async (expected: string, forbidden = "") => {
        await page.getByRole("button", { name: /render/i }).last().click();
        await expect.poll(async () => {
          const candidate = await findPreview();
          if (!candidate) return false;
          await candidate.reload({ waitUntil: "domcontentloaded", timeout: 120_000 }).catch(() => undefined);
          if (host === "sfmg") await candidate.goto(`${agent}/dev-web/settings`, { waitUntil: "domcontentloaded", timeout: 120_000 });
          if (host === "talos") {
            await candidate.goto(`${hostUrl}/app/more`, { waitUntil: "domcontentloaded", timeout: 120_000 });
            await candidate.getByText("Settings", { exact: true }).first().click().catch(() => undefined);
          }
          const body = await candidate.locator("body").innerText().catch(() => "");
          return body.includes(expected) && (!forbidden || !body.includes(forbidden));
        }, { timeout: 4 * 60_000, intervals: [3_000, 5_000, 10_000] }).toBe(true);
      };

      await expect(page.getByRole("button", { name: "Open Dogfood controls" })).toBeVisible({ timeout: 60_000 });
      await checkpoint("chat-controls-visible");
      await page.getByRole("button", { name: "Open Dogfood controls" }).click();
      await checkpoint("chat-controls-open");
      await expect(page.getByRole("button", { name: "Chat" })).toBeVisible();
      await page.getByRole("button", { name: "Chat" }).click();
      await checkpoint("chat-opened");
      const firstPrompt = page.getByPlaceholder("What would you like to change?").last();
      await expect(firstPrompt).toBeEditable({ timeout: 60_000 });
      console.log("[sdk-dogfood] chat-editor-ready");
      await firstPrompt.fill(vibePrompt, { timeout: 30_000 });
      console.log("[sdk-dogfood] change-filled");
      await page.getByRole("button", { name: "Send chat message" }).click({ timeout: 30_000 });
      await checkpoint("change-sent");
      await waitForChatReady();
      await checkpoint("change-completed");
      await renderAndAssert(changedMarker);
      await checkpoint("change-rendered");

      await page.getByPlaceholder("Follow up…").last().fill(revertPrompt);
      await page.getByRole("button", { name: "Send chat message" }).click();
      await checkpoint("revert-sent");
      await waitForChatReady();
      await checkpoint("revert-completed");
      await renderAndAssert(restoredMarker, changedMarker);
      await checkpoint("revert-rendered");
    }
    await page.screenshot({ path: join(artifacts, `${host}-sdk-dogfood-after-reload.png`), fullPage: true });
  } finally {
    await context.close();
    if (video) copyFileSync(await video.path(), join(artifacts, `${host}-sdk-dogfood-live.mp4`));
  }
});
