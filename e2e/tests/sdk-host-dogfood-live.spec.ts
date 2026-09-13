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
const artifacts = resolve(process.env.E2E_VIDEO_DIR || "test-results/sdk-host-dogfood");

function localConfig(): { auth_token: string; device_id: string; code?: { runner?: string; model?: string } } {
  return JSON.parse(readFileSync(join(homedir(), ".yaver", "config.json"), "utf8"));
}

async function agentJSON(path: string, token: string, init?: RequestInit): Promise<any> {
  const response = await fetch(`${agent}${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", ...init?.headers },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(`${path} returned ${response.status}: ${JSON.stringify(body).slice(0, 500)}`);
  return body;
}

test("SDK host auto-launches browser Dogfood and keeps real pixels through reload", async () => {
  test.skip(!(["sfmg", "talos"] as string[]).includes(host), "set E2E_SDK_HOST=sfmg|talos");
  test.skip(!hostUrl || !projectPath || !persistentProfile, "needs host URL, exact project path, and approved persistent profile");

  const cfg = localConfig();
  test.skip(!cfg.auth_token || !cfg.device_id, "local Yaver OAuth and device identity are required");
  mkdirSync(artifacts, { recursive: true });

  const surface = profileFor("mobile");
  const descriptor = devices[surface.playwrightDevice!];
  const context = await chromium.launchPersistentContext(persistentProfile, {
    ...descriptor,
    headless: true,
    recordVideo: { dir: artifacts },
  });
  await context.addInitScript(({ authToken, deviceId, runner, model, activeHost }) => {
    localStorage.setItem("yaver_feedback_auth_token", authToken);
    localStorage.setItem("yaver_feedback_selected_device", deviceId);
    localStorage.setItem("yaver_feedback_preferred_runner", runner || "codex");
    localStorage.setItem("yaver_feedback_preferred_model", model || "gpt-5.6-sol");
    // Discovery must honor the selected device even if an earlier run cached
    // a healthy URL for another machine.
    if (activeHost === "sfmg") {
      localStorage.setItem("sfmg.default\\sfmg.yaverEnabled", "true");
      localStorage.setItem("sfmg.default\\sfmg.yaverToken", authToken);
      localStorage.setItem("sfmg.default\\sfmg.yaverAgentUrl", "http://127.0.0.1:18080");
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
    deviceId: cfg.device_id,
    runner: cfg.code?.runner,
    model: cfg.code?.model,
    activeHost: host,
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

  const page = context.pages()[0] || await context.newPage();
  const video = page.video();
  try {
    const viewport = page.viewportSize()!;
    const verdict = viewportMatchesSurface("mobile", {
      ...viewport,
      isMobile: descriptor.isMobile,
      hasTouch: descriptor.hasTouch,
    }, 0);
    expect(verdict.ok, verdict.reason).toBe(true);

    await agentJSON("/dev/stop", cfg.auth_token, { method: "POST", body: "{}" }).catch(() => undefined);
    const entryUrl = host === "sfmg" ? `${hostUrl}/settings` : `${hostUrl}/app/more`;
    await page.goto(entryUrl, { waitUntil: "domcontentloaded", timeout: 120_000 });
    if (host === "talos") {
      await page.getByText("Settings", { exact: true }).first().click();
    }
    const entry = page.getByRole("button", { name: host === "sfmg" ? "Dogfood SFMG" : "Dogfood Talos" });
    await expect(entry).toBeVisible({ timeout: 120_000 });
    await entry.click();

    await expect.poll(async () => {
      const status = await agentJSON("/dev/status", cfg.auth_token);
      return status.running === true && status.serving === true && status.workDir === projectPath;
    }, { timeout: 180_000, intervals: [1_000, 2_000, 3_000] }).toBe(true);

    const marker = host === "sfmg" ? /Choose Your Language|Türkçe|English/i : /Dashboard|Quick Actions/i;
    await expect.poll(async () => {
      for (const candidate of context.pages().filter((item) => item !== page)) {
        if (marker.test(await candidate.locator("body").innerText().catch(() => ""))) return true;
      }
      return false;
    }, { timeout: 120_000 }).toBe(true);

    // The product auto-launches. A separate Launch button would regress the
    // one-action host entry into a two-step flow.
    await expect(page.getByRole("button", { name: /^Launch(?: Dogfood)?$/i })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Open Dogfood controls" })).toBeVisible();
    await page.getByRole("button", { name: "Open Dogfood controls" }).click();
    const reloadResponse = page.waitForResponse((response) => response.url().includes("/dogfood/reload"), { timeout: 120_000 });
    await page.getByRole("button", { name: "Reload" }).click();
    expect((await reloadResponse).ok()).toBe(true);

    await expect.poll(async () => {
      for (const candidate of context.pages().filter((item) => item !== page)) {
        if (marker.test(await candidate.locator("body").innerText().catch(() => ""))) return true;
      }
      return false;
    }, { timeout: 60_000 }).toBe(true);
    await page.screenshot({ path: join(artifacts, `${host}-sdk-dogfood-after-reload.png`), fullPage: true });
  } finally {
    await context.close();
    if (video) copyFileSync(await video.path(), join(artifacts, `${host}-sdk-dogfood-live.mp4`));
  }
});
