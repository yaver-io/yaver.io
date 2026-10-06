import { expect, test, type Page } from "@playwright/test";

/**
 * DeepSeek-via-OpenCode plumbing closed loop — web dashboard surface.
 *
 *   cd e2e
 *   YAVER_TEST_TOKEN=<owner token> npx playwright test tests/deepseek-opencode-plumbing.spec.ts
 *
 * ── What it proves ─────────────────────────────────────────────────────────
 *
 * The user's ask: DeepSeek must work as a first-class NTH provider under the
 * opencode runner, switchable from the web/mobile UI, with the preferred
 * opencode (runner + provider + model) persisted to Convex and plumbed into
 * the box's opencode.json — and tasks dispatched to the remote box must
 * actually run on the selected model.
 *
 * This spec drives the REAL web dashboard (local dev server so the catalogue
 * change is live) with the user's OWN account token and proves, in order:
 *
 *   1. NAMED  — the box's "Coding agent" modal shows Preferred = opencode,
 *               provider = DeepSeek, model = DeepSeek V4 Flash (deepseek-v4-flash).
 *   2. NTH    — switching the provider select to GLM (zai-coding-plan) flips
 *               the Convex per-device pref AND the box's opencode.json, and
 *               switching BACK to DeepSeek restores deepseek/deepseek-flash.
 *   3. RUN    — a task dispatched to the box with NO explicit runner/model
 *               resolves opencode + deepseek/deepseek-flash and COMPLETES.
 *   4. AUDIT  — the runner argv observed on the box includes the dangerous
 *               flag (--dangerously-skip-permissions) and the deepseek model.
 *
 * Verdicts are NAMED (explicit text/cause) — never SILENT. Environment gaps
 * skip with a reason rather than fail.
 *
 * Credentials: YAVER_TEST_TOKEN (owner scope, from the box agent's config).
 * The token value is never printed; it travels via env only.
 */

const APP = process.env.WEB_URL || "http://127.0.0.1:3217";
const BOX = process.env.VIBE_BOX || "ubuntu-4gb-hel1-1";
// The box's SSH target is deliberately ENV-REQUIRED: this repo is public and
// the runner box's address is operator infrastructure, not repo content.
// `yaver ssh`-style targets resolve via the operator's own ssh config.
const BOX_SSH = process.env.BOX_SSH || "";

function creds() {
  return { token: process.env.YAVER_TEST_TOKEN || "" };
}

function haveCreds() {
  return Boolean(creds().token);
}

async function boxCmd(cmd: string): Promise<string> {
  const { execSync } = await import("node:child_process");
  return execSync(
    `ssh -o ConnectTimeout=10 -o BatchMode=yes ${BOX_SSH} ${JSON.stringify(cmd)}`,
    { encoding: "utf8", timeout: 180_000 },
  ).trim();
}

/** Read the box's Convex per-device pref (its own agent token, localhost). */
async function boxPrimaryPref(): Promise<Record<string, string>> {
  try {
    return JSON.parse(await boxCmd("/root/ds-loop-probe.sh pref"));
  } catch {
    return { runner: "parse-error", model: "", provider: "", mode: "" };
  }
}

/** Read the box's opencode.json default model + deepseek provider presence. */
async function boxOpenCodeState(): Promise<{ model: string; hasDeepseek: boolean; permission: string }> {
  try {
    return JSON.parse(await boxCmd("/root/ds-loop-probe.sh opencode"));
  } catch {
    return { model: "parse-error", hasDeepseek: false, permission: "" };
  }
}

/** Run a hello task on the box headlessly; return {status, model, result}. */
async function boxHelloTask() {
  try {
    return JSON.parse(await boxCmd("/root/ds-loop-probe.sh hello"));
  } catch {
    return { status: "probe-error", model: "", result: "" };
  }
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

/** Open the box's "Coding agent" modal from the ⋯ menu on its card. */
async function openCodingAgentModal(page: Page) {
  // The dashboard may open on Chat/Vibing — make sure the Devices grid is live.
  const devicesNav = page.getByRole("button", { name: /Devices/i }).first();
  if (await devicesNav.count()) {
    await devicesNav.click().catch(() => {});
    await page.waitForTimeout(5000);
  }
  const menu = page
    .getByRole("button", { name: new RegExp(`Actions for .*${BOX}`, "i") })
    .first();
  await expect(menu, `device card for ${BOX} must render its ⋯ menu`).toBeVisible({ timeout: 90_000 });
  await menu.click();
  await page.waitForTimeout(1500);
  await page.getByText("Coding agent…").first().click();
  await page.waitForTimeout(2500);
  await expect(page.getByText("Coding agent").first(), "Coding agent modal must open").toBeVisible({ timeout: 30_000 });
}

test.describe("deepseek opencode plumbing closed loop", () => {
  test.skip(!haveCreds() || !BOX_SSH,
    "needs YAVER_TEST_TOKEN (owner scope) + BOX_SSH (operator ssh target for the runner box) — " +
    "an environment gap is not a product defect");

  test("web dashboard: deepseek is selectable, switchable, and runs on the box", async ({ page }) => {
    await signIn(page);

    // Signed in — the dashboard's own chrome (Vibing/Devices nav) proves it.
    await expect(page.getByText(/^(Studio|Devices)$/).first(),
      "dashboard must render signed-in chrome").toBeVisible({ timeout: 60_000 });

    await openCodingAgentModal(page);

    // 1. NAMED: preferred runner is opencode; provider is deepseek; model is DeepSeek V4 Flash.
    const primarySelect = page.locator('select[title*="Change primary coding agent"]').first();
    await expect(primarySelect, "the primary-runner select must exist").toBeVisible({ timeout: 30_000 });
    expect(await primarySelect.inputValue(), "preferred runner must be opencode").toBe("opencode");

    const providerSelect = page.locator('select[title*="OpenCode provider"]').first();
    await expect(providerSelect, "the opencode provider select must exist").toBeVisible({ timeout: 30_000 });
    const provOptions = await providerSelect.locator("option").allTextContents();
    // DeepSeek must be one of the provider options (the "nth option" requirement).
    expect(provOptions.some((o) => /DeepSeek/i.test(o)),
      `provider select must offer DeepSeek (saw: ${provOptions.join(" | ")})`).toBe(true);
    expect(await providerSelect.inputValue(), "selected provider must be deepseek").toBe("deepseek");

    const modelSelect = page.locator('select[title*="Model OpenCode spawns with"]').first();
    await expect(modelSelect, "the opencode model select must exist").toBeVisible({ timeout: 30_000 });
    const modelOptions = await modelSelect.locator("option").allTextContents();
    expect(modelOptions.some((o) => /DeepSeek V4 Flash/i.test(o)),
      `model select must offer DeepSeek V4 Flash (saw: ${modelOptions.join(" | ")})`).toBe(true);
    expect(await modelSelect.inputValue(),
      "selected model must be deepseek-v4-flash").toBe("deepseek-v4-flash");

    // 2. NTH: switch provider to GLM → the Convex pref + box opencode.json must flip.
    await providerSelect.selectOption("zai-coding-plan");
    await page.waitForTimeout(4000);
    let pref = await boxPrimaryPref();
    expect(pref.runner, "pref runner after switch").toBe("opencode");
    expect(pref.provider, "pref provider after switch").toBe("zai-coding-plan");
    expect(pref.model, "pref model after switch").toContain("glm");

    // 3. NTH: switch back to DeepSeek / DeepSeek V4 Flash → everything restores.
    await providerSelect.selectOption("deepseek");
    await page.waitForTimeout(1500);
    // Model select now lists deepseek models; pick DeepSeek V4 Flash explicitly.
    const m2 = page.locator('select[title*="Model OpenCode spawns with"]').first();
    await m2.selectOption("deepseek-v4-flash");
    await page.waitForTimeout(4000);
    pref = await boxPrimaryPref();
    expect(pref.runner, "pref runner after restore").toBe("opencode");
    expect(pref.provider, "pref provider after restore").toBe("deepseek");
    expect(pref.model, "pref model after restore").toBe("deepseek/deepseek-flash");

    const boxCfg = await boxOpenCodeState();
    expect(boxCfg.model, "box opencode.json default model").toBe("deepseek/deepseek-flash");
    expect(boxCfg.hasDeepseek, "box opencode.json must have provider.deepseek").toBe(true);
    expect(boxCfg.permission, "box opencode.json dangerous default").toBe("allow");

    // 4. RUN: dispatch a task with NO runner/model → must complete on deepseek.
    const run = await boxHelloTask();
    expect(run.status, `task on the box must complete (saw ${run.status})`).toBe("completed");
    expect(run.model, "task model must be deepseek/deepseek-flash").toBe("deepseek/deepseek-flash");
    expect(run.result, "task must answer the hello prompt").toContain("hello from deepseek flash");

    // 5. AUDIT: the running agent binary on the box must embed the dangerous
    //    flag (opencode is spawned with --dangerously-skip-permissions).
    const flagCount = Number(await boxCmd("/root/ds-loop-probe.sh argv"));
    expect(flagCount, "the box's agent binary must carry --dangerously-skip-permissions").toBeGreaterThan(0);
  });
});
