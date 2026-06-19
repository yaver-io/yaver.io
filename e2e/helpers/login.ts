import { Page, expect } from "@playwright/test";

/**
 * Resolve the test login. The PREFERRED credential is a revocable session
 * TOKEN (YAVER_TEST_TOKEN) minted for the automation account — injected
 * directly, so no password ever travels in CI and the credential can be cut
 * off by deleting that one session. Falls back to email/password (seeded
 * account, then the global-setup.ts throwaway user) for form-login coverage.
 */
export function testCreds(): { token?: string; email?: string; password?: string } {
  return {
    token: process.env.YAVER_TEST_TOKEN || process.env.E2E_USER_TOKEN,
    email: process.env.YAVER_TEST_EMAIL || process.env.E2E_USER_EMAIL,
    password: process.env.YAVER_TEST_PASSWORD || process.env.E2E_USER_PASSWORD,
  };
}

/**
 * Land on an authenticated route. Token path (default, secure): inject the
 * session token into localStorage + cookie before any page script runs and go
 * straight to the dashboard — no password, no form. Password path: drive the
 * real /auth form (kept for login-UI coverage).
 */
export async function signIn(page: Page): Promise<void> {
  const { token, email, password } = testCreds();

  if (token) {
    await page.addInitScript((t) => {
      try {
        localStorage.setItem("yaver_auth_token", t as string);
      } catch {
        /* localStorage may be unavailable on about:blank — set after nav too */
      }
      document.cookie = `yaver_auth_token=${t}; path=/; max-age=${60 * 60 * 24 * 30}; samesite=lax`;
    }, token);
    await page.goto("/dashboard");
    // Belt-and-suspenders for the rare case the init script raced the origin.
    await page.evaluate((t) => localStorage.setItem("yaver_auth_token", t as string), token);
    await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 25_000 });
    if (page.url().includes("/survey")) {
      await page.goto("/dashboard");
      await page.waitForURL(/\/dashboard/, { timeout: 15_000 });
    }
    return;
  }

  if (!email || !password) {
    throw new Error("no test creds (set YAVER_TEST_TOKEN, or YAVER_TEST_EMAIL/PASSWORD)");
  }
  await page.goto("/auth");
  await expect(page.getByPlaceholder("Email address")).toBeVisible({ timeout: 20_000 });
  await page.getByPlaceholder("Email address").fill(email);
  await page.getByPlaceholder("Password").fill(password);
  await page.getByRole("button", { name: /^sign in$/i }).click();
  await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 25_000 });
  if (page.url().includes("/survey")) {
    await page.goto("/dashboard");
    await page.waitForURL(/\/dashboard/, { timeout: 15_000 });
  }
}
