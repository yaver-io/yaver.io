import { devices, expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { profileFor, viewportMatchesSurface } from "../../web/lib/surfaceViewports";

const agent = (process.env.E2E_AGENT_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const projectPath = (process.env.YAVER_DOGFOOD_PROJECT_PATH || "").trim();
const token = process.env.YAVER_TEST_TOKEN || tokenFromLocalConfig();

function tokenFromLocalConfig(): string {
  try {
    const config = JSON.parse(readFileSync(join(homedir(), ".yaver", "config.json"), "utf8"));
    return typeof config.auth_token === "string" ? config.auth_token : "";
  } catch {
    return "";
  }
}

test("Yaver browser Dogfood reaches the live dev server for fast and full reload", async ({ browser, request }) => {
  test.skip(!token || !projectPath, "needs YAVER_TEST_TOKEN + YAVER_DOGFOOD_PROJECT_PATH");

  const profile = profileFor("mobile");
  const descriptor = devices[profile.playwrightDevice!];
  const context = await browser.newContext({
    ...descriptor,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  });
  // The Authorization header authenticates the browser-to-agent transport;
  // it does not authenticate the Yaver RN-web app running inside that
  // preview. A fresh browser context also executes the reinstall guard, which
  // clears an unaccompanied token before AuthContext can validate it. Seed the
  // same production storage keys as the mobile closed-loop harness before the
  // first document runs. Without this, React briefly mounts and then escapes
  // the scoped /dev/ lane to /login, leaving the test staring at the agent's
  // 404 page while reporting a reload failure.
  await context.addInitScript((authToken) => {
    localStorage.setItem("yaver_installed", "1");
    localStorage.setItem("yaver.secure.yaver_auth_token", authToken);
  }, token);
  const page = await context.newPage();

  try {
    const viewport = page.viewportSize()!;
    const verdict = viewportMatchesSurface("mobile", {
      ...viewport,
      isMobile: descriptor.isMobile,
      hasTouch: descriptor.hasTouch,
    }, 0);
    expect(verdict.ok, verdict.reason).toBe(true);

    // The local peer route races relay reconnection after an agent/dev-server
    // restart and can return a transient 502 before the next request succeeds.
    // Retry only gateway failures; a 4xx or a persistent 5xx remains a real
    // failure with the final response preserved for the assertion below.
    let response = await page.goto(`${agent}/dev/`, {
      waitUntil: "domcontentloaded",
      timeout: 120_000,
    });
    for (let attempt = 0; response?.status() === 502 && attempt < 3; attempt += 1) {
      await page.waitForTimeout(1_000);
      response = await page.goto(`${agent}/dev/`, {
        waitUntil: "domcontentloaded",
        timeout: 120_000,
      });
    }
    expect(response?.status(), "authenticated Yaver browser document status").toBe(200);
    await expect(page).toHaveTitle(/Yaver/i);
    await expect(page.locator("#root")).not.toBeEmpty({ timeout: 120_000 });
    // A mounted splash root is not a working RN app. The previous assertion
    // accepted the pixel-visible "Starting Yaver…" placeholder indefinitely.
    await expect(page.getByText("Starting Yaver…", { exact: true })).toBeHidden({ timeout: 120_000 });

    for (const mode of ["fast", "full"] as const) {
      const reload = await request.post(`${agent}/dogfood/reload`, {
        headers: { Authorization: `Bearer ${token}` },
        data: {
          lane: "browser",
          mode,
          projectPath,
          projectName: "yaver-mobile",
          source: "ubuntu-browser-e2e",
        },
      });
      const payload = await reload.json().catch(() => ({}));
      expect(reload.ok(), `${mode} reload failed: ${JSON.stringify(payload).slice(0, 500)}`).toBe(true);
      expect(payload).toMatchObject({
        ok: true,
        reloadTarget: "browser-dev-server",
        transport: "browser-dev-server",
      });
      await expect(page).toHaveTitle(/Yaver/i);
      await expect(page.locator("#root")).not.toBeEmpty();
    }
  } finally {
    await context.close();
  }
});
