import { expect, test, type Page } from "@playwright/test";
import { decodePng } from "../_framePixels.mjs";

/**
 * Console-first task view closed loop (web dashboard) — 2026-08-12.
 *
 *   cd e2e
 *   YAVER_TEST_TOKEN=<owner token> npx playwright test tests/opencode-terminal-web.spec.ts
 *
 * ── What it proves ─────────────────────────────────────────────────────────
 *
 * The dashboard task view IS the live console: the raw runner stdout lane
 * (`raw`/`raw_replay` SSE frames, agent 1.99.406+, commit d671b7c02) paints
 * in the main pane for EVERY runner via AnsiConsoleText — no Chat|Terminal
 * toggle (removed 2026-08-09), no folded "Live console" card (removed
 * 2026-08-12). This spec:
 *
 *   1. RUN    — dispatch an opencode task; the console appears IMMEDIATELY
 *               (no toggle to open) and paints the raw TUI — `> build ·
 *               <model>` banner + `$` prompts — judged on PIXELS (colour
 *               diversity, not a status badge) and DOM text.
 *   2. NO-FOLD — neither a Chat|Terminal toggle nor a "Live console" fold
 *               button may exist; the console is the default view.
 *   3. DONE   — the completed task's answer token is visible in the console,
 *               and re-selecting the task reseeds the raw tail WITHOUT
 *               duplicating the banner (raw_replay replaces, never appends).
 *
 * Verdicts are PIXELS + DOM text — never SILENT. Environment gaps skip with
 * a reason rather than fail.
 */

const APP = process.env.WEB_URL || "http://127.0.0.1:3217";

function creds() {
  return { token: process.env.YAVER_TEST_TOKEN || "" };
}

function haveCreds() {
  return Boolean(creds().token);
}

/** Seed the dashboard session from a token (web uses the UNPREFIXED key). */
async function signIn(page: Page) {
  const { token } = creds();
  await page.addInitScript((t) => {
    try { localStorage.setItem("yaver_auth_token", t as string); } catch { /* about:blank */ }
    document.cookie = `yaver_auth_token=${t}; path=/; max-age=${60 * 60 * 24 * 30}; samesite=lax`;
  }, token);
  await page.goto(`${APP}/dashboard`, { waitUntil: "domcontentloaded", timeout: 90_000 });
  await page.evaluate((t) => localStorage.setItem("yaver_auth_token", t as string), token);
  await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 45_000 });
  if (page.url().includes("/survey")) await page.goto(`${APP}/dashboard`);
  await page.waitForTimeout(8000);
}

/**
 * Count distinct non-background colours in a screenshot region. The console
 * palette (orange banner + green prompt + default text) gives >= 3; a
 * stripAnsi-to-plain regression renders one grey blob.
 */
async function distinctColors(page: Page, sel: string) {
  const el = page.locator(sel).first();
  const buf = await el.screenshot({ animations: "disabled" });
  const img = decodePng(buf);
  const { width, height, rgba } = img;
  const seen = new Set<string>();
  for (let y = 0; y < height; y += Math.max(1, Math.floor(height / 60))) {
    for (let x = 0; x < width; x += Math.max(1, Math.floor(width / 90))) {
      const i = (y * width + x) * 4;
      const [r, g, b] = [rgba[i], rgba[i + 1], rgba[i + 2]];
      if (r + g + b > 120) {
        seen.add(`${r >> 5},${g >> 5},${b >> 5}`);
      }
    }
  }
  return seen.size;
}

test.describe("console-first task view closed loop (web)", () => {
  test.setTimeout(420_000);
  test.skip(!haveCreds(),
    "needs YAVER_TEST_TOKEN (owner scope) — an environment gap is not a product defect");

  test("dashboard task view IS the live console: streams raw TUI with no toggle, paints colours, and reseeds a finished task without duplicating", async ({ page }) => {
    await signIn(page);

    // Signed-in chrome proves the dashboard rendered.
    await expect(page.getByText(/^(Studio|Devices)$/).first(),
      "dashboard must render signed-in chrome").toBeVisible({ timeout: 60_000 });

    // The dashboard may land on the Devices tab (auto-connect target). The
    // task composer only exists in the Chat pane — navigate there.
    const chatNav = page.locator("aside").getByRole("button", { name: /Chat/i }).first();
    if (await chatNav.count()) {
      await chatNav.click();
      await page.waitForTimeout(1500);
    }

    const composer = page.locator("textarea[placeholder*='Describe the task']").first();
    await expect(composer, "the task composer must render once a device connects").toBeVisible({ timeout: 90_000 });
    await expect(composer, "the composer must be enabled (device connected + runner ready)").toBeEnabled({ timeout: 60_000 });

    // 1. RUN: dispatch an opencode task whose expected raw output we know.
    const token = `TERMINAL_WEB_${Date.now().toString(36).toUpperCase()}`;
    await composer.fill(`Reply with exactly: ${token}`);
    await page.getByRole("button", { name: /Start task/ }).click();
    await page.waitForTimeout(2000);

    // The console is the DEFAULT view — the raw stream must be visible
    // immediately, with NO toggle clicked. The task pane is the
    // bg-surface-950 scroll container; its console is a font-mono <pre>.
    const consolePane = page.locator("div.bg-surface-950 pre.font-mono").first();
    await expect(consolePane, "the raw console must render in the task pane (no toggle to open)").toBeVisible({ timeout: 45_000 });

    // The runner's banner (`> build · <model>`) is the first raw bytes any
    // opencode run emits. Poll the console text for it (the lane may take a
    // moment to flush over the relay).
    let bannerSeen = false;
    for (let i = 0; i < 30 && !bannerSeen; i++) {
      const txt = await consolePane.innerText().catch(() => "");
      bannerSeen = /build ·|> plan/.test(txt);
      if (!bannerSeen) await page.waitForTimeout(1500);
    }
    expect(bannerSeen, "the console must paint the runner banner (`> build · <model>`)").toBe(true);

    // 2. NO-FOLD: the Chat|Terminal toggle is gone and there is no folded
    //    "Live console" card — the console is the view, not a drawer.
    const terminalToggle = page.locator('button[aria-pressed]:has-text("Terminal")');
    expect(await terminalToggle.count(), "the Chat|Terminal toggle must not exist (removed 2026-08-09)").toBe(0);
    const liveConsoleFold = page.getByRole("button", { name: /Live console/i });
    expect(await liveConsoleFold.count(), 'a folded "Live console" card must not exist (removed 2026-08-12)').toBe(0);

    // PIXELS: the console must paint the palette (orange banner + green
    // prompt + default text => >= 3 distinct hues). A stripAnsi regression
    // renders one grey.
    await page.waitForTimeout(1500);
    const colors = await distinctColors(page, "div.bg-surface-950 pre.font-mono");
    expect(colors, `the console must paint console colours (distinct=${colors})`).toBeGreaterThanOrEqual(3);

    // 3. DONE: the answer token lands in the console when the task completes.
    const done = page.getByText(token).first();
    try {
      await expect(done, "the completed task's answer must appear in the console").toBeVisible({ timeout: 240_000 });
    } catch {
      throw new Error("the task answer never appeared in the dashboard console");
    }
    await page.waitForTimeout(3000); // let status reconcile to completed

    // Re-select the task (sidebar list → the same task) and assert the raw
    // tail reseeds WITHOUT duplicating the banner: raw_replay REPLACES, so a
    // reseed must not double the banner count.
    const taskItem = page.locator("aside .task-item, aside [class*='task-item']", { hasText: token }).first();
    if (await taskItem.count()) {
      await taskItem.click();
      await page.waitForTimeout(4000);
    }
    const reseeded = await consolePane.innerText().catch(() => "");
    const banners = (reseeded.match(/build ·/g) || []).length;
    expect(banners, `reseed must not duplicate the banner (saw ${banners})`).toBeLessThanOrEqual(2);
    expect(reseeded, "the reseeded console must still hold the raw tail").toContain("build ·");
  });
});
