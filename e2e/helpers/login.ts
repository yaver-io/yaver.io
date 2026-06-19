import { Page, expect } from "@playwright/test";

/**
 * Resolve the test login. Prefers the seeded automation account
 * (YAVER_TEST_EMAIL / YAVER_TEST_PASSWORD — a real password credential on a
 * real account, so the dashboard shows real devices), and falls back to the
 * throwaway user that global-setup.ts mints (E2E_USER_*).
 */
export function testCreds(): { email?: string; password?: string } {
  return {
    email: process.env.YAVER_TEST_EMAIL || process.env.E2E_USER_EMAIL,
    password: process.env.YAVER_TEST_PASSWORD || process.env.E2E_USER_PASSWORD,
  };
}

/** Sign in through the real /auth form and land on an authenticated route. */
export async function signIn(page: Page): Promise<void> {
  const { email, password } = testCreds();
  if (!email || !password) {
    throw new Error("no test creds (set YAVER_TEST_EMAIL / YAVER_TEST_PASSWORD)");
  }
  await page.goto("/auth");
  await expect(page.getByPlaceholder("Email address")).toBeVisible({ timeout: 20_000 });
  await page.getByPlaceholder("Email address").fill(email);
  await page.getByPlaceholder("Password").fill(password);
  await page.getByRole("button", { name: /^sign in$/i }).click();
  await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 25_000 });
  // Fresh accounts may be parked on /survey; the seeded account is past it,
  // but jump straight to the dashboard either way so Devices is in view.
  if (page.url().includes("/survey")) {
    await page.goto("/dashboard");
    await page.waitForURL(/\/dashboard/, { timeout: 15_000 });
  }
}
