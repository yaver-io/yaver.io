import { test, expect } from "@playwright/test";

test("remote approval reviews the device and never trusts a backend from the link", async ({ page }, testInfo) => {
  test.skip(!process.env.PLAIN_SSH_WEB_URL, "Set the real web URL");
  let approvals = 0;
  let hostileRequests = 0;
  await page.addInitScript(() => localStorage.setItem("yaver_auth_token", "test-approval-session"));
  await page.route("https://untrusted.invalid/**", async route => { hostileRequests++; await route.abort(); });
  await page.route("**/auth/validate", route => route.fulfill({ json: { valid: true } }));
  await page.route("**/auth/device-code/info?*", route => route.fulfill({ json: {
    userCode: "ABCD-1234", machineName: "SSH test device", platform: "darwin",
    status: "pending", expiresAt: Date.now() + 120000,
  } }));
  await page.route("**/api/auth/device/authorize", async route => {
    approvals++;
    expect(route.request().postDataJSON().convexUrl).not.toContain("untrusted.invalid");
    await route.fulfill({ json: { ok: true } });
  });
  await page.goto(`${process.env.PLAIN_SSH_WEB_URL}/auth/device?code=ABCD-1234&convex=https://untrusted.invalid`);
  await expect(page.getByRole("button", { name: "Review device", exact: true })).toBeVisible();
  expect(approvals).toBe(0);
  expect(hostileRequests).toBe(0);
  await page.getByRole("button", { name: "Review device", exact: true }).click();
  await expect(page.getByText(/Match ABCD-1234 on SSH test device/)).toBeVisible();
  expect(approvals).toBe(0);
  await page.screenshot({path:testInfo.outputPath("device-approval.png"),fullPage:true});
  await page.getByRole("button", { name: "Approve sign-in", exact: true }).click();
  await expect.poll(() => approvals).toBe(1);
  expect(hostileRequests).toBe(0);
});
