import { test, expect } from "@playwright/test";

/**
 * Sign-in flow smoke test using the dummy user created in `global-setup.ts`.
 *
 * The test navigates to /auth, fills the email/password form, and asserts
 * that a successful sign-in lands on /dashboard with the auth token
 * persisted in localStorage — exactly what the production auth flow does.
 */
test.describe("auth flow (dummy user)", () => {
  test("sign in redirects to /dashboard", async ({ page }) => {
    const email = process.env.E2E_USER_EMAIL;
    const password = process.env.E2E_USER_PASSWORD;
    test.skip(
      !email || !password,
      "dummy test user not available — global-setup didn't run",
    );

    await page.goto("/auth");
    await expect(page.getByPlaceholder("Email address")).toBeVisible();

    await page.getByPlaceholder("Email address").fill(email!);
    await page.getByPlaceholder("Password").fill(password!);
    await page.getByRole("button", { name: /^sign in$/i }).click();

    await page.waitForURL(/\/dashboard/, { timeout: 15_000 });

    const token = await page.evaluate(() =>
      localStorage.getItem("yaver_auth_token"),
    );
    expect(token, "auth token should be in localStorage").toBeTruthy();
  });

  test("wrong password shows an error", async ({ page }) => {
    const email = process.env.E2E_USER_EMAIL;
    test.skip(!email, "dummy test user not available");

    await page.goto("/auth");
    await page.getByPlaceholder("Email address").fill(email!);
    await page.getByPlaceholder("Password").fill("definitely-wrong-password");
    await page.getByRole("button", { name: /^sign in$/i }).click();

    await expect(page.getByText(/invalid email or password/i)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page).toHaveURL(/\/auth/);
  });
});
