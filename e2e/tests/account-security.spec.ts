import { randomUUID } from "crypto";
import { expect, test } from "@playwright/test";
import { rememberTestUser, type TestUser } from "../global-setup";

/**
 * Account-security flows that don't need a browser ceremony but are core to the
 * auth contract: changing a password, and session invalidation on logout.
 *
 * Each test mints its own throwaway account via the API; global-teardown
 * deletes them (rememberTestUser).
 */

const CONVEX_URL =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

async function apiSignup(): Promise<TestUser> {
  const id = randomUUID();
  const user = {
    email: `e2e-sec-${id}@yaver.test`,
    password: `pw-${randomUUID()}A1`,
    fullName: `E2E Sec ${id.slice(0, 8)}`,
  };
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

test.describe("account security", () => {
  test("change-password updates the password used to sign in", async ({ page }) => {
    const user = await apiSignup();
    const newPassword = `pw-${randomUUID()}Z9`;

    const change = await page.request.post(`${CONVEX_URL}/auth/change-password`, {
      headers: { Authorization: `Bearer ${user.token}`, "Content-Type": "application/json" },
      data: { currentPassword: user.password, newPassword },
    });
    expect(change.ok(), "change-password should succeed with the right current password").toBe(true);

    // Old password no longer works through the UI.
    await page.goto("/auth");
    await page.getByPlaceholder("Email address").fill(user.email);
    await page.getByPlaceholder("Password", { exact: true }).fill(user.password);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page.getByText(/invalid email or password/i)).toBeVisible({ timeout: 10_000 });

    // New password does.
    await page.getByPlaceholder("Password", { exact: true }).fill(newPassword);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });

    // Keep teardown able to delete the account with the new credentials.
    rememberTestUser({ ...user, password: newPassword });
  });

  test("change-password rejects a wrong current password", async ({ page }) => {
    const user = await apiSignup();
    const res = await page.request.post(`${CONVEX_URL}/auth/change-password`, {
      headers: { Authorization: `Bearer ${user.token}`, "Content-Type": "application/json" },
      data: { currentPassword: "not-the-password", newPassword: `pw-${randomUUID()}Z9` },
    });
    expect(res.status()).toBe(401);
  });

  test("logout invalidates the session token", async ({ page }) => {
    const user = await apiSignup();

    // Token is valid before logout.
    const before = await page.request.get(`${CONVEX_URL}/auth/validate`, {
      headers: { Authorization: `Bearer ${user.token}` },
    });
    expect(before.ok()).toBe(true);

    const logout = await page.request.post(`${CONVEX_URL}/auth/logout`, {
      headers: { Authorization: `Bearer ${user.token}` },
    });
    expect(logout.ok()).toBe(true);

    // The same token must no longer validate.
    const after = await page.request.get(`${CONVEX_URL}/auth/validate`, {
      headers: { Authorization: `Bearer ${user.token}` },
    });
    expect(after.status(), "validate should 401 after logout").toBe(401);

    // Re-establish a token so teardown can delete the account.
    const login = await page.request.post(`${CONVEX_URL}/auth/login`, {
      data: { email: user.email, password: user.password },
    });
    if (login.ok()) {
      const data = await login.json();
      if (data.token) rememberTestUser({ ...user, token: data.token });
    }
  });

  test("logout-all invalidates every session", async ({ page }) => {
    const user = await apiSignup();

    // Mint a second session for the same account.
    const login2 = await page.request.post(`${CONVEX_URL}/auth/login`, {
      data: { email: user.email, password: user.password },
    });
    expect(login2.ok()).toBe(true);
    const token2 = (await login2.json()).token as string;
    expect(token2).toBeTruthy();

    const logoutAll = await page.request.post(`${CONVEX_URL}/auth/logout-all`, {
      headers: { Authorization: `Bearer ${user.token}` },
    });
    expect(logoutAll.ok()).toBe(true);

    for (const t of [user.token, token2]) {
      const res = await page.request.get(`${CONVEX_URL}/auth/validate`, {
        headers: { Authorization: `Bearer ${t}` },
      });
      expect(res.status(), "all sessions should be invalid after logout-all").toBe(401);
    }

    const relogin = await page.request.post(`${CONVEX_URL}/auth/login`, {
      data: { email: user.email, password: user.password },
    });
    if (relogin.ok()) {
      const data = await relogin.json();
      if (data.token) rememberTestUser({ ...user, token: data.token });
    }
  });
});
