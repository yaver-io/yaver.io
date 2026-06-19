import { test, expect, Page } from "@playwright/test";
import { signIn, testCreds } from "../helpers/login";

/**
 * Headless browser coverage for: sign in with the seeded email/password
 * account → reach the dashboard → see the real remote devices → open the
 * target box's workspace → drive a coding runner.
 *
 * This runs against a LIVE environment (set E2E_BASE_URL=https://yaver.io and
 * E2E_CONVEX_URL=https://perceptive-minnow-557.eu-west-1.convex.site) using a
 * real account, so it asserts only what is deterministic and degrades to
 * test.skip() when a prerequisite isn't met (box offline, runner not yet
 * authed on the box) rather than flaking.
 *
 * Prereqs to go fully green for a runner:
 *   - the target device is online + "Ready to Connect"/"Connected"
 *   - that runner is authenticated ON the box (claude setup-token / codex
 *     login / opencode/glm key) — the dashboard shows it as the PREFERRED or
 *     an available agent.
 */

const SITE =
  process.env.E2E_CONVEX_URL ||
  process.env.YAVER_TEST_CONVEX_SITE ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";
const DEVICE = process.env.YAVER_TEST_DEVICE || "ubuntu-4gb-hel1-1";
const RUNNERS = (process.env.YAVER_TEST_RUNNERS || "claude,codex,opencode,glm")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);

// glm is exercised through the opencode launcher pointed at the z.ai
// Anthropic-compatible endpoint; the rest map to their own launcher.
const LAUNCHER: Record<string, RegExp> = {
  claude: /claude/i,
  codex: /codex/i,
  opencode: /opencode/i,
  glm: /opencode|glm/i,
};

test.describe("seeded account → real device → coding runner (headless)", () => {
  test.skip(() => {
    const { email, password } = testCreds();
    return !email || !password;
  }, "no test account creds (set YAVER_TEST_EMAIL / YAVER_TEST_PASSWORD)");

  test("login resolves the seeded account and lists the real devices", async ({ page }) => {
    await signIn(page);
    // Resource access THROUGH the authenticated session (page-context fetch).
    const info = await page.evaluate(async (site) => {
      const t = localStorage.getItem("yaver_auth_token");
      const who = await fetch(site + "/auth/validate", {
        headers: { Authorization: "Bearer " + t },
      }).then((r) => (r.ok ? r.json() : { status: r.status }));
      const dl = await fetch(site + "/devices/list", {
        headers: { Authorization: "Bearer " + t },
      }).then((r) => r.json());
      return { who, names: (dl.devices || []).map((d: any) => d.name) };
    }, SITE);
    expect(info.who?.user?.email, "session resolves to a real user").toBeTruthy();
    expect(info.names.length, "account has at least one device").toBeGreaterThan(0);
    expect(info.names, `target device ${DEVICE} present`).toContain(DEVICE);
  });

  test(`open the ${DEVICE} workspace`, async ({ page }) => {
    test.setTimeout(120_000);
    await signIn(page);

    const card = deviceCard(page, DEVICE);
    await expect(card, `device card for ${DEVICE}`).toBeVisible({ timeout: 25_000 });

    test.skip(
      !(await isOpenable(card)),
      `${DEVICE} not openable (Offline / Bootstrap / Auth Expired / Unauthorized)`,
    );

    await card.getByRole("button", { name: /open workspace|try connect/i }).first().click();
    // The dashboard opens the workspace inline; the card flips to expose a
    // "Close Workspace" control once the relay session is up.
    await expect(
      card.getByRole("button", { name: /close workspace/i }).first(),
      "workspace opened (relay session live)",
    ).toBeVisible({ timeout: 45_000 });
  });

  for (const runner of RUNNERS) {
    test(`launch ${runner} on ${DEVICE}`, async ({ page }) => {
      test.setTimeout(180_000);
      await signIn(page);

      const card = deviceCard(page, DEVICE);
      await expect(card).toBeVisible({ timeout: 25_000 });

      test.skip(!(await isOpenable(card)), `${DEVICE} not openable`);

      // The runner must be available on the box (PREFERRED or an available
      // agent). If the dashboard doesn't list it, it isn't authed there yet.
      const runnerListed = await card
        .getByText(LAUNCHER[runner] ?? new RegExp(runner, "i"))
        .first()
        .isVisible()
        .catch(() => false);
      test.skip(
        !runnerListed,
        `${runner} not authed/available on ${DEVICE} (run its login on the box)`,
      );

      await card.getByRole("button", { name: /open workspace|try connect/i }).first().click();
      await expect(
        card.getByRole("button", { name: /close workspace/i }).first(),
      ).toBeVisible({ timeout: 45_000 });

      // Launch the runner. The launcher lives in the opened workspace panel;
      // selecting it starts the runner process on the remote box over relay.
      const launcher = page
        .getByRole("button", { name: LAUNCHER[runner] ?? new RegExp(runner, "i") })
        .first();
      await expect(launcher, `${runner} launcher`).toBeVisible({ timeout: 30_000 });
      await launcher.click();

      // Deterministic signal that the process actually started: the launcher
      // flips to its active/stop affordance (■ / "Stop"). Captured terminal
      // text is attached for diagnostics.
      const started = await page
        .getByRole("button", { name: new RegExp(`(■|stop).*${runner}`, "i") })
        .first()
        .isVisible({ timeout: 30_000 })
        .catch(() => false);

      const termText = await page
        .locator(".xterm, [class*='xterm'], [data-testid='terminal-xterm-container']")
        .first()
        .innerText()
        .catch(() => "");
      await test.info().attach(`${runner}-terminal.txt`, {
        body: termText || "(no terminal text captured)",
        contentType: "text/plain",
      });

      expect(
        started || termText.length > 0,
        `${runner} produced no sign of starting on ${DEVICE}`,
      ).toBeTruthy();
    });
  }
});

/**
 * A box is "openable" only when it's actually connectable: Connected, or
 * Ready to Connect WITHOUT an "Unauthorized" qualifier. Bootstrap / Auth
 * Expired / Offline / Unauthorized all mean a workspace can't come up yet
 * (the runner/agent needs auth on the box), so those skip rather than fail.
 */
async function isOpenable(card: ReturnType<Page["locator"]>): Promise<boolean> {
  const connected = await card.getByText(/\bConnected\b/i).first().isVisible().catch(() => false);
  if (connected) return true;
  const ready = await card.getByText(/Ready to Connect/i).first().isVisible().catch(() => false);
  const blocked = await card
    .getByText(/Unauthorized|Bootstrap|Auth Expired|Offline/i)
    .first()
    .isVisible()
    .catch(() => false);
  return ready && !blocked;
}

/** Locate a device card by its name heading (stable across the grid order). */
function deviceCard(page: Page, name: string) {
  return page
    .locator("div")
    .filter({ has: page.getByRole("heading", { name, exact: false }) })
    .filter({ has: page.getByRole("button", { name: /open workspace|try connect|shell/i }) })
    .first();
}
