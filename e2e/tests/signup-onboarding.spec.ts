import { randomUUID } from "crypto";
import { expect, test, type Page } from "@playwright/test";
import { rememberTestUser, type TestUser } from "../global-setup";

const CONVEX_URL =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

function newUser(): Omit<TestUser, "token" | "userId"> {
  const id = randomUUID();
  return {
    email: `e2e-signup-${id}@yaver.test`,
    password: `pw-${randomUUID()}A1`,
    fullName: `E2E Signup ${id.slice(0, 8)}`,
  };
}

async function apiSignup(user: Omit<TestUser, "token" | "userId">): Promise<TestUser> {
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

async function gotoAuth(page: Page): Promise<void> {
  const res = await page.goto("/auth");
  expect(res?.status(), "/auth should be served by the Yaver web app").toBeLessThan(400);
  await expect(page.getByPlaceholder("Email address")).toBeVisible();
}

async function switchToSignup(page: Page): Promise<void> {
  await page.getByRole("button", { name: /^sign up$/i }).click();
  await expect(page.getByPlaceholder("Full name")).toBeVisible();
}

async function fillSignupForm(
  page: Page,
  user: Omit<TestUser, "token" | "userId">,
  confirmPassword = user.password,
): Promise<void> {
  await page.getByPlaceholder("Full name").fill(user.fullName);
  await page.getByPlaceholder("Email address").fill(user.email);
  await page.getByPlaceholder("Password", { exact: true }).fill(user.password);
  await page.getByPlaceholder("Confirm password").fill(confirmPassword);
}

test.describe("signup and first-run onboarding", () => {
  test("creates a new email account from the UI and mints a valid session", async ({ page }) => {
    const user = newUser();

    await gotoAuth(page);
    await switchToSignup(page);
    await fillSignupForm(page, user);
    await page.getByRole("button", { name: /^sign up$/i }).last().click();

    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });
    const token = await page.evaluate(() => localStorage.getItem("yaver_auth_token"));
    expect(token, "signup should persist auth token").toBeTruthy();

    const validate = await page.request.get(`${CONVEX_URL}/auth/validate`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(validate.ok(), "new token should validate against Convex").toBe(true);
    const body = await validate.json();
    expect(body.user.email).toBe(user.email);

    rememberTestUser({
      ...user,
      token: token!,
      userId: body.user.userId ?? body.user.id ?? "",
    });
  });

  test("blocks mismatched passwords before calling signup", async ({ page }) => {
    let signupCalls = 0;
    await page.route(`${CONVEX_URL}/auth/signup`, async (route) => {
      signupCalls += 1;
      await route.fulfill({ status: 500, body: "signup should not be called" });
    });

    const user = newUser();
    await gotoAuth(page);
    await switchToSignup(page);
    await fillSignupForm(page, user, `${user.password}-different`);
    await page.getByRole("button", { name: /^sign up$/i }).last().click();

    await expect(page.getByText("Passwords do not match.")).toBeVisible();
    expect(signupCalls).toBe(0);
  });

  test("blocks short passwords before calling signup", async ({ page }) => {
    let signupCalls = 0;
    await page.route(`${CONVEX_URL}/auth/signup`, async (route) => {
      signupCalls += 1;
      await route.fulfill({ status: 500, body: "signup should not be called" });
    });

    const user = newUser();
    await gotoAuth(page);
    await switchToSignup(page);
    await fillSignupForm(page, { ...user, password: "short" });
    await page.getByRole("button", { name: /^sign up$/i }).last().click();

    await expect(page.getByText("Password must be at least 8 characters.")).toBeVisible();
    expect(signupCalls).toBe(0);
  });

  test("surfaces duplicate-email signup as an existing-account error", async ({ page }) => {
    const user = await apiSignup(newUser());

    await gotoAuth(page);
    await switchToSignup(page);
    await fillSignupForm(page, user);
    await page.getByRole("button", { name: /^sign up$/i }).last().click();

    await expect(page.getByText(/account with (this|that) email already exists/i)).toBeVisible({
      timeout: 10_000,
    });
    await expect(page).toHaveURL(/\/auth/);
  });

  test("supports signup, logout, and password sign-in with the same account", async ({ page }) => {
    const user = newUser();

    await gotoAuth(page);
    await switchToSignup(page);
    await fillSignupForm(page, user);
    await page.getByRole("button", { name: /^sign up$/i }).last().click();
    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });

    const token = await page.evaluate(() => localStorage.getItem("yaver_auth_token"));
    expect(token).toBeTruthy();
    rememberTestUser({ ...user, token: token!, userId: "" });

    await page.evaluate(() => {
      localStorage.removeItem("yaver_auth_token");
      document.cookie = "yaver_auth_token=; path=/; max-age=0";
    });

    await gotoAuth(page);
    await page.getByPlaceholder("Email address").fill(user.email);
    await page.getByPlaceholder("Password", { exact: true }).fill(user.password);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });
    await expect.poll(
      () => page.evaluate(() => localStorage.getItem("yaver_auth_token")),
      { message: "sign-in should store a fresh token" },
    ).toBeTruthy();
  });
});
