import { devices, expect, test } from "@playwright/test";
import { profileFor, viewportMatchesSurface } from "../../web/lib/surfaceViewports";

/**
 * Tablet Vibe Studio — landscape split / portrait peek closed loop.
 *
 *   npx --prefix e2e playwright test tablet-vibe-studio.spec.ts
 *
 * ── What it proves ─────────────────────────────────────────────────────────
 *
 * The Vibe Studio (mobile/app/vibe-studio.tsx) renders the tvOS/web-shaped
 * split on a ~10" Android tablet:
 *   • landscape (Galaxy Tab S9 landscape profile) — preview pane LEFT (browser
 *     or live lane) + chat pane RIGHT, with the lane switcher visible;
 *   • portrait (iPad gen 7 profile, same tablet class) — single-pane chat with
 *     a "Preview" peek tab, NO lane switcher.
 *
 * ── Why a device context, and why assert it ────────────────────────────────
 *
 * A tablet is a DEVICE CONTEXT, not a viewport size. useResponsiveLayout
 * branches on short-edge >= 600 and orientation — a narrowed desktop renders
 * the phone tree and the split simply never exists. So this spec creates a NEW
 * context from the surface profile's Playwright descriptor and asserts the
 * viewport it actually got (viewportMatchesSurface) before touching the DOM,
 * exactly like vibe-color-loop.spec.ts does for mobile.
 *
 * The verdicts are STRUCTURAL (lane switcher / pane labels / peek tab), not
 * pixel reads: the split's existence is a layout fact, and there is no dev
 * server behind it in headless CI. The pixel loop already proves the preview
 * lanes themselves in vibe-color-loop.spec.ts.
 *
 * Skipped rather than failed without MOBILE_WEB_URL, matching the mobile arc:
 * an environment gap is not a product defect.
 */

const MOBILE_WEB_URL = process.env.MOBILE_WEB_URL || "";
const MOBILE_LANDSCAPE = profileFor("tabletLandscape");
const MOBILE_PORTRAIT = profileFor("tablet");

test("tablet vibe studio renders the landscape split", async ({ browser }) => {
  test.skip(!MOBILE_WEB_URL, "MOBILE_WEB_URL is unset — the RN-web app is not served");

  // NEW context from the surface profile's device descriptor. Never
  // page.setViewportSize() on a desktop context: isMobile/hasTouch/UA are
  // context properties and the RN-web tree branches on them.
  const context = await browser.newContext({ ...devices[MOBILE_LANDSCAPE.playwrightDevice!] });
  const page = await context.newPage();

  // VIEWPORT FIRST — the device-context guard. Measure the emulated DEVICE
  // geometry (screen.*), not window.innerWidth: on about:blank Chromium's
  // mobile emulation reports the 980px default layout viewport for iPad/Android
  // tablet descriptors (measured 2026-08-21: iPad gen 7 → 980x1307 inner vs
  // 810x1080 screen), so innerWidth is a false negative for every tablet
  // profile. screen.width/height report the true device CSS geometry and still
  // catch a narrowed desktop (screen.width stays the monitor width there).
  const vp = await page.evaluate(() => ({
    width: screen.width,
    height: screen.height,
    hasTouch: "ontouchstart" in window || navigator.maxTouchPoints > 0,
    isMobile: /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent),
  }));
  const vpCheck = viewportMatchesSurface("tabletLandscape", vp);
  expect(vpCheck.ok,
    `tabletLandscape viewport: ${vpCheck.reason} (saw ${vp.width}x${vp.height}, touch=${vp.hasTouch}, mobileUA=${vp.isMobile})`)
    .toBe(true);

  await page.goto(`${MOBILE_WEB_URL.replace(/\/$/, "")}/vibe-studio`, {
    waitUntil: "domcontentloaded",
    timeout: 60_000,
  });
  await page.waitForTimeout(12_000);

  // The studio screen rendered (its own header).
  await expect(page.getByText(/^Studio$/).first(), "tablet: /vibe-studio did not render")
    .toBeVisible({ timeout: 30_000 });

  // LANDSCAPE SPLIT — the lane switcher is the landscape-only control.
  await expect(page.getByText(/^Browser$/).first(), "tabletLandscape: lane switcher (Browser) missing")
    .toBeVisible({ timeout: 15_000 });
  await expect(page.getByText(/^Live$/).first(), "tabletLandscape: lane switcher (Live) missing")
    .toBeVisible({ timeout: 15_000 });

  // CHAT PANE — the right-side composer is the chat pane's marker.
  await expect(
    page.getByPlaceholder(/^Vibe — what should we change\?$|^Connect a box first$|^What should we change\?$/i).first(),
    "tabletLandscape: chat composer missing in the right pane",
  ).toBeVisible({ timeout: 15_000 });

  // Geometry contract: preview is physically left of chat and both retain a
  // usable first-class width. Text visibility alone cannot prove the split.
  const left = await page.getByTestId("studio-left-pane").boundingBox();
  const right = await page.getByTestId("studio-right-pane").boundingBox();
  expect(left, "tabletLandscape: preview pane has no geometry").not.toBeNull();
  expect(right, "tabletLandscape: chat pane has no geometry").not.toBeNull();
  expect(left!.x + left!.width).toBeLessThanOrEqual(right!.x + 20);
  expect(left!.width).toBeGreaterThan(300);
  expect(right!.width).toBeGreaterThan(300);

  await context.close();
});

test("tablet vibe studio shows portrait peek without the split", async ({ browser }) => {
  test.skip(!MOBILE_WEB_URL, "MOBILE_WEB_URL is unset — the RN-web app is not served");

  const context = await browser.newContext({ ...devices[MOBILE_PORTRAIT.playwrightDevice!] });
  const page = await context.newPage();

  const vp = await page.evaluate(() => ({
    width: screen.width,
    height: screen.height,
    hasTouch: "ontouchstart" in window || navigator.maxTouchPoints > 0,
    isMobile: /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent),
  }));
  const vpCheck = viewportMatchesSurface("tablet", vp);
  expect(vpCheck.ok,
    `tablet viewport: ${vpCheck.reason} (saw ${vp.width}x${vp.height}, touch=${vp.hasTouch}, mobileUA=${vp.isMobile})`)
    .toBe(true);

  await page.goto(`${MOBILE_WEB_URL.replace(/\/$/, "")}/vibe-studio`, {
    waitUntil: "domcontentloaded",
    timeout: 60_000,
  });
  await page.waitForTimeout(12_000);

  await expect(page.getByText(/^Studio$/).first(), "tablet(portrait): /vibe-studio did not render")
    .toBeVisible({ timeout: 30_000 });

  // PORTRAIT — no landscape split: the lane switcher must NOT exist.
  await expect(page.getByText(/^Browser$/).first(), "tablet(portrait): lane switcher must not exist").toHaveCount(0);

  // The peek tab is the portrait-only affordance.
  await expect(page.getByText(/^Preview$/).first(), "tablet(portrait): preview peek tab missing")
    .toBeVisible({ timeout: 15_000 });

  await page.getByText(/^Preview$/).first().click();
  await expect(page.getByText(/^Collapse$/).first(), "tablet(portrait): preview did not expand")
    .toBeVisible({ timeout: 15_000 });
  await expect(page.getByPlaceholder(/^Connect a box first$|^What should we change\?$|^Continue this task…$/i).first())
    .toBeVisible({ timeout: 15_000 });

  await context.close();
});

test("tablet vibe studio drag divider resizes the split", async ({ browser }) => {
  test.skip(!MOBILE_WEB_URL, "MOBILE_WEB_URL is unset — the RN-web app is not served");

  // The split is a fixed 55/45 layout unless the drag divider works. Drive it
  // with TOUCH (CDP Input.dispatchTouchEvent), the input a real tablet uses:
  // RN-web's mouse-emulation path drops all but the first mousemove after the
  // responder grants (measured 2026-08-21), so a Playwright page.mouse drag
  // "sticks" at a few px and is NOT a valid harness for this gesture.
  const context = await browser.newContext({ ...devices[MOBILE_LANDSCAPE.playwrightDevice!] });
  const page = await context.newPage();

  const vp = await page.evaluate(() => ({
    width: screen.width,
    height: screen.height,
    hasTouch: "ontouchstart" in window || navigator.maxTouchPoints > 0,
    isMobile: /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent),
  }));
  const vpCheck = viewportMatchesSurface("tabletLandscape", vp);
  expect(vpCheck.ok,
    `tabletLandscape viewport: ${vpCheck.reason} (saw ${vp.width}x${vp.height}, touch=${vp.hasTouch}, mobileUA=${vp.isMobile})`)
    .toBe(true);

  await page.goto(`${MOBILE_WEB_URL.replace(/\/$/, "")}/vibe-studio`, {
    waitUntil: "domcontentloaded",
    timeout: 60_000,
  });
  await page.waitForTimeout(12_000);

  const divider = page.getByTestId("studio-divider");
  const leftPane = page.getByTestId("studio-left-pane");
  await expect(divider, "tabletLandscape: drag divider missing").toBeVisible({ timeout: 15_000 });

  const before = (await leftPane.boundingBox())!;
  const dbox = (await divider.boundingBox())!;
  const y = Math.round(dbox.y + Math.min(200, dbox.height / 2));
  const x0 = Math.round(dbox.x + dbox.width / 2);

  const cdp = await context.newCDPSession(page);
  await cdp.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: x0, y }] });
  for (let i = 1; i <= 8; i += 1) {
    await cdp.send("Input.dispatchTouchEvent", { type: "touchMove", touchPoints: [{ x: x0 + i * 18, y }] });
    await page.waitForTimeout(30);
  }
  await cdp.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
  await cdp.detach();

  await expect
    .poll(async () => (await leftPane.boundingBox())?.width ?? 0, {
      timeout: 10_000,
      message: "tabletLandscape: dragging the divider did not grow the left preview pane",
    })
    .toBeGreaterThan(before.width + 60);

  await context.close();
});

test("tablet vibe studio keeps the phone frame while the box has no dev server", async ({ browser }) => {
  test.skip(!MOBILE_WEB_URL, "MOBILE_WEB_URL is unset — the RN-web app is not served");

  // Regression guard (2026-08-21): with the browser lane as default, the left
  // phone frame used to render as an EMPTY box whenever the box had no dev
  // server — DevPreview returns null before a /dev/status exists, and the
  // "mobile frame is ready" pane only showed for the live lane with no project.
  // This spec runs against local Metro with no connected box (the documented
  // headless-CI scenario), so the frame must paint its ready pane + the split
  // must stay intact even with a project= URL pinned.
  const context = await browser.newContext({ ...devices[MOBILE_LANDSCAPE.playwrightDevice!] });
  const page = await context.newPage();

  const vp = await page.evaluate(() => ({
    width: screen.width,
    height: screen.height,
    hasTouch: "ontouchstart" in window || navigator.maxTouchPoints > 0,
    isMobile: /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent),
  }));
  const vpCheck = viewportMatchesSurface("tabletLandscape", vp);
  expect(vpCheck.ok,
    `tabletLandscape viewport: ${vpCheck.reason} (saw ${vp.width}x${vp.height}, touch=${vp.hasTouch}, mobileUA=${vp.isMobile})`)
    .toBe(true);

  await page.goto(`${MOBILE_WEB_URL.replace(/\/$/, "")}/vibe-studio?project=sfmg`, {
    waitUntil: "domcontentloaded",
    timeout: 60_000,
  });
  await page.waitForTimeout(12_000);

  await expect(page.getByText(/^Studio$/).first(), "tablet: /vibe-studio?project= did not render")
    .toBeVisible({ timeout: 30_000 });

  // THE GUARD: the persistent phone frame renders its ready pane even though no
  // dev server exists and no project can be selected yet.
  await expect(
    page.getByText(/Pick a project to open its preview|Preview this project beside the conversation|Connect the box to open the selected project/).first(),
    "tabletLandscape: left phone frame went blank with no dev server (DevPreview null path)",
  ).toBeVisible({ timeout: 15_000 });

  // The split is still intact around the frame.
  await expect(page.getByText(/^Browser$/).first(), "tabletLandscape: lane switcher (Browser) missing")
    .toBeVisible({ timeout: 15_000 });

  await context.close();
});
