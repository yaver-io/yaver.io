import { randomUUID } from "crypto";
import { expect, test } from "@playwright/test";
import { rememberTestUser } from "../global-setup";

/**
 * Passkey (WebAuthn) signup + login via a CDP virtual authenticator.
 *
 * The backend (`backend/convex/passkeys.ts`) validates the request `Origin`
 * against a static allowlist (`yaver.io`, `localhost:3000`, `localhost:3001`,
 * plus any `WEBAUTHN_EXTRA_ORIGINS`). The default E2E server runs on
 * `127.0.0.1:3217`, which is NOT allowlisted, so these tests self-skip there
 * with guidance. To actually run them, serve the web app on an allowlisted
 * origin, e.g.:
 *
 *   npm --prefix web run dev -- --port 3001 --hostname 127.0.0.1
 *   E2E_BASE_URL=http://localhost:3001 \
 *     npx playwright test passkey.spec.ts --project=chromium
 */

const ALLOWED_ORIGINS = new Set([
  "https://yaver.io",
  "https://www.yaver.io",
  "http://localhost:3000",
  "http://localhost:3001",
]);

const CONVEX_URL =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";

test.describe("passkey (WebAuthn) auth", () => {
  test("signs up with a passkey then signs back in with it", async ({ page }) => {
    await page.goto("/auth");
    const origin = await page.evaluate(() => location.origin);
    test.skip(
      !ALLOWED_ORIGINS.has(origin),
      `passkey origin ${origin} is not in the backend allowlist — serve on http://localhost:3001 and set E2E_BASE_URL`,
    );

    // Attach a virtual platform authenticator with a resident (discoverable)
    // key and pre-verified user presence so navigator.credentials resolves
    // without any OS prompt.
    const client = await page.context().newCDPSession(page);
    await client.send("WebAuthn.enable");
    const { authenticatorId } = await client.send("WebAuthn.addVirtualAuthenticator", {
      options: {
        protocol: "ctap2",
        transport: "internal",
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    });

    const id = randomUUID();
    const email = `e2e-passkey-${id}@yaver.test`;
    const fullName = `E2E Passkey ${id.slice(0, 8)}`;

    // ── Signup with passkey ────────────────────────────────────────────
    await page.getByRole("button", { name: /^sign up$/i }).click();
    await page.getByPlaceholder("Full name").fill(fullName);
    await page.getByPlaceholder("Email address").fill(email);
    await page.getByRole("button", { name: /sign up with passkey/i }).click();

    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });
    const token = await page.evaluate(() => localStorage.getItem("yaver_auth_token"));
    expect(token, "passkey signup should persist a session token").toBeTruthy();

    // Validate + remember for teardown deletion.
    const validate = await page.request.get(`${CONVEX_URL}/auth/validate`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    expect(validate.ok()).toBe(true);
    const body = await validate.json();
    rememberTestUser({
      email,
      password: "",
      fullName,
      token: token!,
      userId: body.user?.userId ?? body.user?.id ?? "",
    });

    // The authenticator should now hold exactly one resident credential.
    const { credentials } = await client.send("WebAuthn.getCredentials", { authenticatorId });
    expect(credentials.length, "one passkey should be stored").toBe(1);

    // ── Log out locally, then sign back in with the passkey ─────────────
    await page.evaluate(() => {
      localStorage.removeItem("yaver_auth_token");
      document.cookie = "yaver_auth_token=; path=/; max-age=0";
    });

    await page.goto("/auth");
    await page.getByPlaceholder("Email address").fill(email);
    await page.getByRole("button", { name: /sign in with passkey/i }).click();

    await expect(page).toHaveURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 20_000 });
    await expect
      .poll(() => page.evaluate(() => localStorage.getItem("yaver_auth_token")), {
        message: "passkey sign-in should store a fresh token",
      })
      .toBeTruthy();
  });
});
