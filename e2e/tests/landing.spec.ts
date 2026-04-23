import { test, expect, type ConsoleMessage } from "@playwright/test";

/**
 * Landing-page smoke tests for yaver.io.
 *
 * Goal: catch bugs that stop the home page from rendering, breaking nav, or
 * spewing console errors in production. Everything here hits purely public
 * surfaces — no auth required.
 */

function captureConsoleErrors(page: import("@playwright/test").Page): string[] {
  const errors: string[] = [];
  page.on("console", (msg: ConsoleMessage) => {
    if (msg.type() === "error") {
      const text = msg.text();
      // Next.js dev server emits HMR / Fast Refresh noise we don't care about.
      if (/webpack-hmr|_next\/static|favicon/.test(text)) return;
      errors.push(text);
    }
  });
  page.on("pageerror", (err) => errors.push(`pageerror: ${err.message}`));
  return errors;
}

test.describe("landing page", () => {
  test("home renders hero + title", async ({ page }) => {
    const errors = captureConsoleErrors(page);
    await page.goto("/");

    await expect(page).toHaveTitle(/Yaver/i);
    // The landing h1 is split across two lines via <br>. Keep the
    // assertion aligned with the current mobile-first feedback wedge.
    const hero = page.getByRole("heading", { level: 1 });
    await expect(hero).toBeVisible();
    await expect(hero).toContainText(/Turn mobile feedback into/i);
    await expect(hero).toContainText(/AI-ready fixes/i);

    expect(errors, `console errors on /: ${errors.join(" | ")}`).toEqual([]);
  });

  test("sign in link navigates to /auth", async ({ page }) => {
    await page.goto("/");
    const signIn = page
      .getByRole("link", { name: /sign in|log in|get started/i })
      .first();
    await signIn.click();
    await page.waitForURL(/\/auth/);
    await expect(page.getByPlaceholder("Email address")).toBeVisible();
    await expect(page.getByPlaceholder("Password")).toBeVisible();
  });

  test("/docs loads without errors", async ({ page }) => {
    const errors = captureConsoleErrors(page);
    const res = await page.goto("/docs");
    expect(res?.status(), "/docs HTTP status").toBeLessThan(400);
    expect(errors, `console errors on /docs: ${errors.join(" | ")}`).toEqual([]);
  });

  test("/download loads without errors", async ({ page }) => {
    const errors = captureConsoleErrors(page);
    const res = await page.goto("/download");
    expect(res?.status(), "/download HTTP status").toBeLessThan(400);
    expect(errors, `console errors on /download: ${errors.join(" | ")}`).toEqual([]);
  });
});
