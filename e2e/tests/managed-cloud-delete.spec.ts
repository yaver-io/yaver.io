/**
 * Headless web e2e: a managed cloud box (cheap cpx22, provisioned via
 * the signed LS webhook out-of-band) is removed entirely through the
 * REAL web UI — expand the Managed-cloud panel, click "♻ Delete box",
 * accept the confirm, and assert the row goes to stopping/stopped.
 *
 * Auth: an owner web-session token is minted out-of-band and injected
 * into localStorage (the buy flow is owner-gated; a fresh signup user
 * would 403). The LS hosted-checkout page itself is third-party and
 * intentionally NOT automated here — it is proven manually; this spec
 * covers the parts Yaver owns: the box shows, and the web Delete
 * button tears it down.
 *
 * Run:
 *   E2E_BASE_URL=https://yaver.io E2E_SKIP_LIVE_AUTH=1 \
 *   YAVER_E2E_TOKEN=$(cat /tmp/yaver-e2e-token.txt) \
 *   npx playwright test managed-cloud-delete --project=chromium
 */
import { test, expect } from "@playwright/test";

const TOKEN = process.env.YAVER_E2E_TOKEN || "";

test("managed cloud box can be deleted from the web UI", async ({ page }) => {
  test.skip(!TOKEN, "YAVER_E2E_TOKEN (owner session) required");

  // Owner auth — same key the production auth flow persists.
  await page.addInitScript((t) => {
    window.localStorage.setItem("yaver_auth_token", t as string);
  }, TOKEN);

  // Auto-accept the destructive confirm() the Delete button raises.
  page.on("dialog", (d) => d.accept());

  await page.goto("/", { waitUntil: "networkidle" });

  // Land on Devices (sidebar nav or already there).
  const devicesNav = page.getByRole("link", { name: /devices/i }).first();
  if (await devicesNav.isVisible().catch(() => false)) {
    await devicesNav.click();
  }

  // Expand the collapsed "☁ Managed cloud — buy / adopt" panel.
  const panelToggle = page.getByRole("button", {
    name: /Managed cloud — buy \/ adopt/i,
  });
  await expect(panelToggle).toBeVisible({ timeout: 20_000 });
  await panelToggle.click();

  // A managed machine row exposes a "♻ Delete box" button. Poll a bit:
  // the box was provisioned moments ago, the panel self-refreshes 8s.
  const deleteBtn = page.getByRole("button", { name: /Delete box/i }).first();
  await expect(deleteBtn).toBeVisible({ timeout: 30_000 });

  await deleteBtn.click();

  // Post-delete the panel reloads machines; the row should leave the
  // active set (status → stopping/stopped) or vanish. Assert the
  // delete button count drops or a stopped/stopping state appears.
  await expect(async () => {
    const txt = await page
      .locator("text=/Managed cloud/i")
      .locator("xpath=ancestor::div[1]")
      .innerText()
      .catch(() => "");
    expect(/stopping|stopped|No managed machines/i.test(txt)).toBeTruthy();
  }).toPass({ timeout: 30_000 });
});
