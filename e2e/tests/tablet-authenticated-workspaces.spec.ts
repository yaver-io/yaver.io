import { devices, expect, test, type Browser } from "@playwright/test";
import { profileFor, viewportMatchesSurface } from "../../web/lib/surfaceViewports";

const APP_URL = process.env.MOBILE_WEB_URL || "";
const TOKEN = process.env.YAVER_TEST_TOKEN || "";

async function openAuthenticatedTablet(
  browser: Browser,
  surface: "tablet" | "tabletLandscape",
  route: string,
) {
  const profile = profileFor(surface);
  const context = await browser.newContext({ ...devices[profile.playwrightDevice!] });
  const page = await context.newPage();
  await page.goto(APP_URL, { waitUntil: "domcontentloaded", timeout: 60_000 });
  await page.evaluate((token) => {
    localStorage.setItem("yaver_installed", "1");
    localStorage.setItem("yaver.secure.yaver_auth_token", token);
  }, TOKEN);
  await page.goto(`${APP_URL.replace(/\/$/, "")}${route}`, {
    waitUntil: "domcontentloaded",
    timeout: 60_000,
  });
  await page.waitForTimeout(12_000);

  const observed = await page.evaluate(() => ({
    width: screen.width,
    height: screen.height,
    hasTouch: "ontouchstart" in window || navigator.maxTouchPoints > 0,
    isMobile: /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent),
  }));
  const match = viewportMatchesSurface(surface, observed);
  expect(match.ok, match.reason).toBe(true);
  await expect(page.getByText(/Continue with Email|Sign In to drive/i)).toHaveCount(0);
  return { context, page };
}

test.describe("authenticated tablet workspaces", () => {
  test.skip(!APP_URL || !TOKEN, "needs MOBILE_WEB_URL + YAVER_TEST_TOKEN");

  test("Tasks landscape keeps list left, detail right, and completed console folded", async ({ browser }) => {
    const { context, page } = await openAuthenticatedTablet(browser, "tabletLandscape", "/tasks");
    try {
      await expect(page.getByText(/^Tasks$/).first()).toBeVisible();
      const completed = page.getByText(/^Completed · \d+$/).first();
      await expect(completed).toBeVisible();
      await completed.click();
      await page.waitForTimeout(3_000);

      const actions = page.getByRole("button", { name: "Task actions" });
      test.skip((await actions.count()) === 0, "connected account has no completed task fixture");
      const actionBox = await actions.first().boundingBox();
      expect(actionBox).not.toBeNull();
      await page.mouse.click(Math.max(20, actionBox!.x - 120), actionBox!.y + actionBox!.height / 2);
      await page.waitForTimeout(6_000);

      const viewport = page.viewportSize()!;
      const addButtons = page.getByTestId("new-task-button");
      const addBoxes = await Promise.all(
        Array.from({ length: await addButtons.count() }, (_, index) => addButtons.nth(index).boundingBox()),
      );
      const cockpitAdd = addBoxes.find((box) => box && box.x < viewport.width * 0.48);
      const detailAction = page.getByRole("button", { name: /Send command|Stop task/ }).first();
      const detailBox = await detailAction.boundingBox();

      expect(cockpitAdd, "landscape task list collapsed or disappeared").toBeTruthy();
      expect(detailBox, "task detail composer is missing").not.toBeNull();
      expect(cockpitAdd!.x).toBeLessThan(viewport.width * 0.48);
      expect(detailBox!.x).toBeGreaterThan(viewport.width * 0.48);
      await expect(page.getByRole("button", { name: "Show live console" }).first()).toBeVisible();
    } finally {
      await context.close();
    }
  });

  test("Tasks portrait renders completed tasks in two usable columns", async ({ browser }) => {
    const { context, page } = await openAuthenticatedTablet(browser, "tablet", "/tasks");
    try {
      const completed = page.getByText(/^Completed · \d+$/).first();
      await expect(completed).toBeVisible();
      await completed.click();
      await page.waitForTimeout(3_000);
      const actions = page.getByRole("button", { name: "Task actions" });
      test.skip((await actions.count()) < 2, "connected account needs two completed task fixtures");
      const first = await actions.nth(0).boundingBox();
      const second = await actions.nth(1).boundingBox();
      expect(first).not.toBeNull();
      expect(second).not.toBeNull();
      expect(Math.abs(first!.x - second!.x), "portrait task cards did not form two columns").toBeGreaterThan(250);
      expect(Math.abs(first!.y - second!.y), "first portrait task row is vertically misaligned").toBeLessThan(40);
    } finally {
      await context.close();
    }
  });

  test("connected Vibing landscape exposes project, preview, and enabled conversation", async ({ browser }) => {
    const { context, page } = await openAuthenticatedTablet(browser, "tabletLandscape", "/vibe-studio");
    try {
      await expect(page.getByText(/^Studio$/).first()).toBeVisible();
      await expect(page.getByTestId("studio-left-pane")).toBeVisible();
      await expect(page.getByTestId("studio-right-pane")).toBeVisible();
      await expect(page.getByText(/^disconnected$/i)).toHaveCount(0);
      const composer = page.getByPlaceholder(/What should we change\?|Continue this task…/i).first();
      await expect(composer).toBeVisible();
      await expect(composer).toBeEnabled();
      await expect(page.getByRole("button", { name: /^Change project, currently / }).first()).toBeVisible();
    } finally {
      await context.close();
    }
  });
});
