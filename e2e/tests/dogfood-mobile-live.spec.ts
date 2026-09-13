import { devices, expect, test } from "@playwright/test";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { profileFor, viewportMatchesSurface } from "../../web/lib/surfaceViewports";

const mobileURL = (process.env.MOBILE_WEB_URL || "").replace(/\/$/, "");
const deviceName = (process.env.YAVER_TEST_DEVICE_NAME || "").trim();
const recordAll = process.env.E2E_RECORD_ALL === "1";
const runColorLoop = process.env.YAVER_DOGFOOD_COLOR_LOOP === "1";
const requestedUsageMode = process.env.YAVER_DOGFOOD_USAGE_MODE === "reload-only"
  ? "reload-only"
  : "reload-and-chat";
const token = process.env.YAVER_TEST_TOKEN || tokenFromLocalConfig();
const agentURL = (process.env.YAVER_AGENT_URL || "http://127.0.0.1:18080").replace(/\/$/, "");
const convexSite = process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

function tokenFromLocalConfig(): string {
  try {
    const config = JSON.parse(readFileSync(join(homedir(), ".yaver", "config.json"), "utf8"));
    return typeof config.auth_token === "string" ? config.auth_token : "";
  } catch {
    return "";
  }
}

type AgentTask = { id: string; status?: string; title?: string; description?: string; output?: string };

async function agentTasks(): Promise<AgentTask[]> {
  const response = await fetch(`${agentURL}/tasks`, { headers: { Authorization: `Bearer ${token}` } });
  if (!response.ok) throw new Error(`agent task inventory returned HTTP ${response.status}`);
  const payload = await response.json() as { tasks?: AgentTask[] };
  return payload.tasks || [];
}

async function waitForNewTask(before: Set<string>, marker: string): Promise<AgentTask> {
  const deadline = Date.now() + 12 * 60_000;
  let candidate: AgentTask | undefined;
  while (Date.now() < deadline) {
    const tasks = await agentTasks();
    candidate = tasks.find((task) => !before.has(task.id) &&
      `${task.title || ""}\n${task.description || ""}\n${task.output || ""}`.includes(marker))
      || tasks.find((task) => !before.has(task.id));
    if (candidate && ["ready", "completed", "review", "failed", "cancelled", "stopped"].includes(candidate.status || "")) {
      if (["failed", "cancelled", "stopped"].includes(candidate.status || "")) {
        throw new Error(`Dogfood task ${candidate.id} ended ${candidate.status}: ${(candidate.output || "").slice(-1200)}`);
      }
      return candidate;
    }
    await new Promise((resolve) => setTimeout(resolve, 3_000));
  }
  throw new Error(`Dogfood task did not finish within 12 minutes${candidate ? ` (last status ${candidate.status})` : ""}`);
}

test("RN-web shows the compact Dogfood setup and opens inventories only on demand", async ({ browser }, testInfo) => {
  test.skip(!mobileURL || !token, "needs MOBILE_WEB_URL + YAVER_TEST_TOKEN");

  // A viewport resize is not a mobile device. Own a genuine touch/mobile/UA
  // context so RN-web renders the same component tree a phone receives.
  const profile = profileFor("mobile");
  const videoDir = testInfo.outputPath("video");
  if (recordAll) mkdirSync(videoDir, { recursive: true });
  const context = await browser.newContext({
    ...devices[profile.playwrightDevice!],
    storageState: undefined,
    ...(recordAll ? { recordVideo: { dir: videoDir, size: devices[profile.playwrightDevice!].viewport } } : {}),
  });
  const page = await context.newPage();
  page.setDefaultTimeout(60_000);
  const video = page.video();
  let mutatedLoginPath = "";
  let baselineLogin = "";
  page.on("console", (message) => {
    if (
      message.type() === "error" ||
      message.type() === "warning" ||
      /\[(?:DeviceContext|QUIC)\]/.test(message.text())
    ) {
      console.log(`[rn-web:${message.type()}] ${message.text()}`);
    }
  });
  page.on("requestfailed", (request) => {
    console.log(`[rn-web:requestfailed] ${request.method()} ${request.url()} · ${request.failure()?.errorText || "unknown"}`);
  });
  page.on("response", (response) => {
    if (response.status() < 400) return;
    const url = new URL(response.url());
    console.log(`[rn-web:http] ${response.status()} ${response.request().method()} ${url.origin}${url.pathname}`);
  });
  try {
    await page.goto(mobileURL, { waitUntil: "domcontentloaded", timeout: 120_000 });
    // Let the app finish its fresh-install check first. Seeding during that
    // check races clearKeychainIfFreshInstall(), which correctly deletes stale
    // credentials and leaves the harness on Login despite a valid token.
    const continueWithEmail = page.getByText(/Continue with Email/i).first();
    await expect(continueWithEmail).toBeVisible({ timeout: 120_000 });
    await continueWithEmail.click();
    const auth = await page.request.get(`${convexSite}/auth/validate?_=${Date.now()}`, {
      headers: { Authorization: `Bearer ${token}`, "Cache-Control": "no-store" },
    });
    expect(auth.ok(), `auth validate returned HTTP ${auth.status()}`).toBe(true);
    const payload = await auth.json() as { user?: Record<string, unknown> };
    const user = payload.user || {};
    await page.evaluate(({ session, appUser }) => {
      localStorage.setItem("yaver.secure.yaver_auth_token", session);
      localStorage.setItem("yaver.secure.yaver_user", JSON.stringify(appUser));
    }, {
      session: token,
      appUser: {
        id: user.userId,
        email: user.email,
        name: user.fullName,
        provider: user.provider,
        emailVerified: user.emailVerified,
        surveyCompleted: user.surveyCompleted,
        isOwner: user.isOwner,
      },
    });

    await page.goto(`${mobileURL}/dogfood`, { waitUntil: "domcontentloaded", timeout: 120_000 });
    await expect(page.getByRole("button", { name: "Open Dogfood settings" })).toBeVisible({ timeout: 120_000 });
    await page.getByRole("button", { name: "Open Dogfood settings" }).click();
    await expect(page.getByText("Dogfood Settings").first()).toBeVisible({ timeout: 120_000 });
    const viewport = page.viewportSize()!;
    const contextSignals = await page.evaluate(() => ({
      isMobile: /Mobile|iPhone|Android/i.test(navigator.userAgent),
      hasTouch: navigator.maxTouchPoints > 0,
    }));
    const viewportVerdict = viewportMatchesSurface("mobile", { ...viewport, ...contextSignals });
    expect(viewportVerdict.ok, viewportVerdict.reason).toBe(true);

    await expect(page.getByText("Remote box", { exact: true })).toBeVisible();
    await expect(page.getByText("Runner", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("Checkout", { exact: true })).toBeVisible();
    await expect(page.getByText("Developer management", { exact: true })).toHaveCount(0);
    await expect(page.getByLabel("Box choices")).toHaveCount(0);
    await expect(page.getByLabel("Runner choices")).toHaveCount(0);
    await expect(page.getByLabel("Yaver checkout choices")).toHaveCount(0);
    await expect(page.getByText("Runtime", { exact: true })).toHaveCount(0);

    if (deviceName) {
      await page.getByRole("button", { name: /^(?:Change|Set up) Remote box$/ }).click();
      const boxChoices = page.getByLabel("Box choices");
      await expect(boxChoices).toBeVisible();
      // The offline rendering appends " · offline" to the same row. Exact
      // text therefore becomes a pixel-visible operation proof that the
      // selected test box really joined the connection pool.
      const connectedBox = boxChoices.getByText(deviceName, { exact: true });
      await expect(connectedBox).toBeVisible({ timeout: 120_000 });
      await connectedBox.click();
      await page.getByRole("button", { name: "Close Dogfood setting choices" }).last().click();
      await expect(boxChoices).toHaveCount(0);
    }

    const runnerControl = page.getByRole("button", { name: /^(?:Change|Set up) Runner$/ });
    await runnerControl.scrollIntoViewIfNeeded();
    await expect(runnerControl).toBeInViewport();
    await runnerControl.click();
    await expect(page.getByLabel("Runner choices")).toBeVisible();
    if (runColorLoop) {
      await page.getByLabel("Runner choices").getByText(/^codex$/).first().click();
    }
    await page.getByRole("button", { name: "Close Dogfood setting choices" }).last().click();
    await expect(page.getByLabel("Runner choices")).toHaveCount(0);

    // Runtime lanes are a compact inline radio group now, not a fourth
    // inventory modal. The old harness waited for a removed "Change runtime
    // lane" button even while the real controls were plainly visible in the
    // captured pixels. Assert the current named contract directly.
    const runtimeChoices = page.getByLabel("Runtime lane choices");
    await runtimeChoices.scrollIntoViewIfNeeded();
    await expect(runtimeChoices).toBeInViewport();
    await expect(page.getByRole("radiogroup", { name: "Dogfood runtime lane" })).toBeVisible();
    await expect(page.getByRole("radio", { name: /Browser lane/ })).toBeVisible();
    await expect(page.getByRole("radio", { name: /Hermes/ })).toBeVisible();
    await expect(page.getByRole("radio", { name: /WebRTC native/ })).toBeVisible();

    // The two requested browser-Dogfood modes must be independently visible,
    // selectable, and durable. This is pixel evidence for the policy that
    // Reload Only removes chat while Reload + Chat retains it; lower-level
    // contract tests cover the request-body booleans.
    const reloadOnly = page.getByRole("radio", { name: "Reload Only", exact: true });
    const reloadAndChat = page.getByRole("radio", { name: "Reload + Chat", exact: true });
    await reloadOnly.scrollIntoViewIfNeeded();
    await reloadOnly.click();
    await expect(reloadOnly).toBeChecked();
    await page.screenshot({ path: testInfo.outputPath("reload-only.png"), fullPage: true });
    await reloadAndChat.click();
    await expect(reloadAndChat).toBeChecked();
    await page.screenshot({ path: testInfo.outputPath("reload-and-chat.png"), fullPage: true });
    if (requestedUsageMode === "reload-only") {
      await reloadOnly.click();
      await expect(reloadOnly).toBeChecked();
    }

    // Cross the boundary into the real attached checkout. Merely proving the
    // settings toggles paints a configuration screen; it does not prove that
    // Yaver can actually dogfood Yaver through the browser lane.
    const enterDogfood = page.getByRole("button", { name: /Enter Dogfood mode/ });
    try {
      // A fresh mobile context still has to complete the genuine device
      // transport race and then ask that box to inspect Git. "checking…" is
      // truthful progress, not a terminal checkout verdict.
      await expect(enterDogfood).toBeVisible({ timeout: 120_000 });
    } catch (error) {
      throw new Error(
        `Dogfood entry did not become available. Visible settings:\n${(await page.locator("body").innerText()).slice(0, 4000)}`,
        { cause: error },
      );
    }
    await enterDogfood.scrollIntoViewIfNeeded();
    await enterDogfood.click();
    // A proved runtime opens itself. There must be no second launch CTA for a
    // user (or an automation harness) to discover and click.
    try {
      await expect(page.getByRole("button", { name: "Open Dogfood", exact: true })).toHaveCount(0);
      await expect(page.locator("iframe").first()).toBeVisible({ timeout: 300_000 });
    } catch (error) {
      throw new Error(
        `Dogfood did not auto-open after preparation. Visible launch state:\n${(await page.locator("body").innerText()).slice(0, 5000)}`,
        { cause: error },
      );
    }
    // A mounted iframe is only inventory. The attached surface is usable when
    // its document has loaded and the host's blocking loader is gone.
    await expect(page.getByText(/Loading Yaver from/)).toBeHidden({ timeout: 180_000 });
    const attached = page.frameLocator("iframe").first();
    try {
      await expect(attached.getByText("Starting Yaver…", { exact: true })).toBeHidden({ timeout: 120_000 });
    } catch (error) {
      throw new Error(
        `Attached Yaver never left its startup placeholder. Frame text:\n${(await attached.locator("body").innerText()).slice(0, 3000)}`,
        { cause: error },
      );
    }
    await page.screenshot({ path: testInfo.outputPath("attached-yaver.png"), fullPage: true });

    if (requestedUsageMode === "reload-only") {
      await page.locator('[data-testid="yaver-dogfood-entry"]:visible').first().click();
      await expect(page.getByLabel("Dogfood is active")).toBeVisible({ timeout: 60_000 });
      await expect(page.getByRole("button", { name: "Open Dogfood tasks" })).toHaveCount(0);
      await page.locator('[data-testid="dogfood-native-reload"]:visible').first().click();
      await expect(page.locator("iframe:visible").first()).toBeVisible({ timeout: 180_000 });
      await expect(page.getByText(/Loading Yaver from/)).toBeHidden({ timeout: 180_000 });
      await page.screenshot({ path: testInfo.outputPath("reload-only-live.png"), fullPage: true });
    }

    if (runColorLoop && requestedUsageMode === "reload-and-chat") {
      test.setTimeout(20 * 60_000);
      const loginPath = join(process.cwd(), "mobile", "app", "login.tsx");
      baselineLogin = readFileSync(loginPath, "utf8");
      mutatedLoginPath = loginPath;
      const marker = "DOGFOOD_COLOR_LOOP_20260913";
      const red = "rgb(255, 0, 170)";

      const openDogfoodMenu = async () => {
        // Replaced Expo routes remain mounted but hidden on RN-web. Address
        // the one control a user can see rather than failing on retained DOM.
        await page.locator('[data-testid="yaver-dogfood-entry"]:visible').first().click();
        await expect(page.getByLabel("Dogfood is active")).toBeVisible({ timeout: 60_000 });
      };

      const openTasksFromCurrentSurface = async () => {
        await openDogfoodMenu();
        await page.getByRole("button", { name: "Open Dogfood tasks" }).click();
        await expect(page.getByText("Tasks", { exact: true }).first()).toBeVisible({ timeout: 60_000 });
      };

      const reloadFromCurrentSurface = async () => {
        await openDogfoodMenu();
        await page.locator('[data-testid="dogfood-native-reload"]:visible').first().click();
        // Expo Router deliberately retains the previous Attach DOM below the
        // replacement route. Assert the newly visible guest, not the stale
        // hidden iframe that remains first in document order.
        await expect(page.locator("iframe:visible").first()).toBeVisible({ timeout: 180_000 });
        await expect(page.getByText(/Loading Yaver from/)).toBeHidden({ timeout: 180_000 });
        await expect(page.frameLocator("iframe:visible").first().getByText("Starting Yaver…", { exact: true }))
          .toBeHidden({ timeout: 120_000 });
      };

      const submitTask = async (prompt: string) => {
        const before = new Set((await agentTasks()).map((task) => task.id));
        const newTask = page.getByRole("button", { name: "New task", exact: true }).last();
        if (await newTask.isVisible().catch(() => false)) await newTask.click();
        const composer = page.getByPlaceholder(/What should the agent do\?|Send another command…/).last();
        await expect(composer).toBeVisible({ timeout: 60_000 });
        await composer.fill(prompt);
        await page.getByText(/^Send$/).last().click();
        return waitForNewTask(before, marker);
      };

      await openTasksFromCurrentSurface();
      await submitTask(
        `${marker}: In mobile/app/login.tsx only, change the SafeAreaView login-screen background from c.bg to the literal color #ff00aa. ` +
        "Do not edit any other file and do not commit. When the edit is saved, say UI updates are ready.",
      );
      expect(readFileSync(loginPath, "utf8"), "the Dogfood chat task must make the requested source edit")
        .toContain("#ff00aa");

      // Sending opens task detail as a modal route. Dismiss that route before
      // using the global Y; the modal correctly owns pointer events while open.
      await page.goBack();
      await reloadFromCurrentSurface();
      const changedFrame = page.frameLocator("iframe:visible").first();
      await expect.poll(async () => changedFrame.locator("body").evaluate((body) =>
        Array.from(body.querySelectorAll("*")).some((node) => getComputedStyle(node).backgroundColor === "rgb(255, 0, 170)"),
      ), { timeout: 180_000, message: "attached Yaver never painted the requested #ff00aa background" }).toBe(true);
      await page.screenshot({ path: testInfo.outputPath("dogfood-color-changed.png"), fullPage: true });

      await openTasksFromCurrentSurface();
      await submitTask(
        `${marker}: Revert only the temporary #ff00aa SafeAreaView background change in mobile/app/login.tsx back to c.bg. ` +
        "Do not alter any other code and do not commit. When reverted, say UI updates are ready.",
      );
      expect(readFileSync(loginPath, "utf8"), "the Dogfood revert must restore login.tsx byte-for-byte")
        .toBe(baselineLogin);

      await page.goBack();
      await reloadFromCurrentSurface();
      const revertedFrame = page.frameLocator("iframe:visible").first();
      await expect.poll(async () => revertedFrame.locator("body").evaluate(
        (body, expectedRed) => Array.from(body.querySelectorAll("*"))
          .every((node) => getComputedStyle(node).backgroundColor !== expectedRed),
        red,
      ), { timeout: 180_000, message: "attached Yaver retained the temporary color after revert" }).toBe(true);
      await page.screenshot({ path: testInfo.outputPath("dogfood-color-reverted.png"), fullPage: true });
    }
  } finally {
    // The normal path proves the revert through Dogfood chat. This fallback is
    // deliberately byte-exact so an interrupted destructive arc never leaves
    // the checkout colored or contaminates the next run.
    if (mutatedLoginPath && baselineLogin && readFileSync(mutatedLoginPath, "utf8") !== baselineLogin) {
      writeFileSync(mutatedLoginPath, baselineLogin);
    }
    await context.close();
    if (video) console.log(`recording: ${await video.path()}`);
  }
});
