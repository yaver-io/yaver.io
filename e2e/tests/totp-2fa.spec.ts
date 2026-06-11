import { randomUUID } from "crypto";
import { expect, test } from "@playwright/test";
import { rememberTestUser, type TestUser } from "../global-setup";
import { generateTotp } from "../lib/totp";

/**
 * TOTP (two-factor) end-to-end coverage.
 *
 * These tests exercise the real backend 2FA contract end to end:
 *   signup -> /auth/totp/setup -> derive code -> /auth/totp/enable
 *   -> password login now returns requires2fa -> /auth/totp page -> dashboard.
 *
 * The TOTP secret is minted per run by the backend and the account is deleted
 * by global-teardown (via rememberTestUser), so no secret is ever hardcoded.
 */

const CONVEX_URL =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

function newUser(): Omit<TestUser, "token" | "userId"> {
  const id = randomUUID();
  return {
    email: `e2e-totp-${id}@yaver.test`,
    password: `pw-${randomUUID()}A1`,
    fullName: `E2E TOTP ${id.slice(0, 8)}`,
  };
}

async function apiSignup(): Promise<TestUser> {
  const user = newUser();
  const res = await fetch(`${CONVEX_URL}/auth/signup`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(user),
  });
  if (!res.ok) throw new Error(`signup failed: ${res.status} ${await res.text()}`);
  const data = (await res.json()) as { token: string; userId: string };
  const created = { ...user, token: data.token, userId: data.userId };
  rememberTestUser(created);
  return created;
}

/** Enroll TOTP for an authed user and return the secret + recovery codes. */
async function enableTotp(token: string): Promise<{ secret: string; recoveryCodes: string[] }> {
  const setupRes = await fetch(`${CONVEX_URL}/auth/totp/setup`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
  });
  if (!setupRes.ok) throw new Error(`totp setup failed: ${setupRes.status} ${await setupRes.text()}`);
  const { secret } = (await setupRes.json()) as { secret: string; otpAuthUrl: string };

  const enableRes = await fetch(`${CONVEX_URL}/auth/totp/enable`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: JSON.stringify({ code: generateTotp(secret) }),
  });
  if (!enableRes.ok) throw new Error(`totp enable failed: ${enableRes.status} ${await enableRes.text()}`);
  const { recoveryCodes } = (await enableRes.json()) as { recoveryCodes: string[] };
  return { secret, recoveryCodes };
}

test.describe("two-factor (TOTP) login", () => {
  test("enrolling 2FA flips login into the verify step and a code completes it", async ({ page }) => {
    const user = await apiSignup();
    const { secret } = await enableTotp(user.token);

    // Status should now report enabled.
    const status = await page.request.get(`${CONVEX_URL}/auth/totp/status`, {
      headers: { Authorization: `Bearer ${user.token}` },
    });
    expect(status.ok()).toBe(true);
    expect((await status.json()).enabled).toBe(true);

    // Password login should now route to the TOTP page rather than dashboard.
    await page.goto("/auth");
    await page.getByPlaceholder("Email address").fill(user.email);
    await page.getByPlaceholder("Password", { exact: true }).fill(user.password);
    await page.getByRole("button", { name: /^sign in$/i }).click();

    await expect(page).toHaveURL(/\/auth\/totp\?/, { timeout: 25_000 });
    await expect(page.getByPlaceholder("000000")).toBeVisible();

    // A fresh code clears 2FA and mints a session.
    await page.getByPlaceholder("000000").fill(generateTotp(secret));
    await page.getByRole("button", { name: /^verify$/i }).click();

    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });
    const token = await page.evaluate(() => localStorage.getItem("yaver_auth_token"));
    expect(token, "2FA verify should persist a session token").toBeTruthy();

    const validate = await page.request.get(`${CONVEX_URL}/auth/validate`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(validate.ok()).toBe(true);
  });

  test("a wrong code is rejected on the TOTP step", async ({ page }) => {
    const user = await apiSignup();
    await enableTotp(user.token);

    await page.goto("/auth");
    await page.getByPlaceholder("Email address").fill(user.email);
    await page.getByPlaceholder("Password", { exact: true }).fill(user.password);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/auth\/totp\?/, { timeout: 25_000 });

    await page.getByPlaceholder("000000").fill("000000");
    await page.getByRole("button", { name: /^verify$/i }).click();

    // An error is surfaced (the friendly "Invalid code" or the raw INVALID_CODE,
    // depending on how the backend deploy unwraps the mutation error) and the
    // user stays on the verify step with no session minted.
    await expect(page.getByText(/invalid[\s_]?code/i)).toBeVisible({ timeout: 10_000 });
    await expect(page).toHaveURL(/\/auth\/totp\?/);
    expect(await page.evaluate(() => localStorage.getItem("yaver_auth_token"))).toBeFalsy();
  });

  test("a recovery code also completes the TOTP step", async ({ page }) => {
    const user = await apiSignup();
    const { recoveryCodes } = await enableTotp(user.token);
    expect(recoveryCodes.length).toBeGreaterThan(0);

    await page.goto("/auth");
    await page.getByPlaceholder("Email address").fill(user.email);
    await page.getByPlaceholder("Password", { exact: true }).fill(user.password);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/auth\/totp\?/, { timeout: 25_000 });

    await page.getByRole("button", { name: /use a recovery code/i }).click();
    await page.getByPlaceholder("Recovery code").fill(recoveryCodes[0]);
    await page.getByRole("button", { name: /^verify$/i }).click();

    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });
    await expect
      .poll(() => page.evaluate(() => localStorage.getItem("yaver_auth_token")))
      .toBeTruthy();
  });

  test("2FA can be disabled and login no longer requires a code", async ({ page }) => {
    const user = await apiSignup();
    const { secret } = await enableTotp(user.token);

    const disable = await page.request.post(`${CONVEX_URL}/auth/totp/disable`, {
      headers: { Authorization: `Bearer ${user.token}`, "Content-Type": "application/json" },
      data: { code: generateTotp(secret) },
    });
    expect(disable.ok(), "disable should accept a current code").toBe(true);

    await page.goto("/auth");
    await page.getByPlaceholder("Email address").fill(user.email);
    await page.getByPlaceholder("Password", { exact: true }).fill(user.password);
    await page.getByRole("button", { name: /^sign in$/i }).click();

    // No TOTP detour now — straight to the authenticated area.
    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });
  });
});
