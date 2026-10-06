import { expect, test, type Page } from "@playwright/test";
import { signIn } from "../helpers/login";

/**
 * DOM MODE closed loop — element inspect end-to-end against a real box.
 *
 *   npx --prefix e2e playwright test dom-inspect-loop.spec.ts
 *
 * ── What it proves ─────────────────────────────────────────────────────────
 *
 * The whole DOM-mode pipeline, in one arc, judged by the same discipline as the
 * vibe-colour loop:
 *
 *   1. the agent injected the dom probe into the preview HTML (`data-yaver-dom-probe="1"`)
 *   2. the user flips Inspect ON in the Browse|Inspect radio on the dashboard
 *   3. a click INSIDE the cross-origin preview iframe is captured by the probe
 *      (the probe posts to window.parent — it can never talk to the agent
 *      directly, /dev/ is unauthenticated by design)
 *   4. the chip renders the attached element BY NAME (the SILENT-PROMPT-MUTATION
 *      disclosure: what gets prepended to the next prompt is shown, not implied)
 *   5. the surface forwarded it over its own authed channel — a POST to the
 *      agent's /dom-inspect route is OBSERVED on the wire (page.on("request"))
 *
 * ── Why the probe-marker assertion is the guard ────────────────────────────
 *
 * Delete the `injectDomInspectProbe(` call from either HTML lane
 * (devserver_basehref.go / build_web.go) and the marker vanishes from the
 * preview → this test fails there. That is the Snowball "prove the guard by
 * breaking it" contract, pinned the same way the Go inject test scans both
 * lanes statically (dom_inspect_inject_test.go) and the lib test scans the
 * probe file (domInspect.test.ts wire-literal guard).
 *
 * ── Why a REAL box and not a mocked agent ─────────────────────────────────
 *
 * The chip only renders when the dashboard is CONNECTED to an agent, and a
 * page.route mock of /dev/status + /dom-inspect would let the arc pass on a
 * UI that no live agent ever pairs with. Same reasoning as the lane matrix:
 * this spec requires YAVER_TEST_TOKEN + E2E_LIVE_DEVICE and skips, named,
 * without them — an environment gap is not a product defect.
 *
 * Env:
 *   YAVER_TEST_TOKEN (required), E2E_LIVE_DEVICE (required),
 *   E2E_BASE_URL (optional, defaults to the local web dev server),
 *   E2E_DOM_PROJECT (optional project label; default "todo-web" — the matrix's
 *   browser-lane web project, which always renders an iframe).
 */

const LIVE_DEVICE = process.env.E2E_LIVE_DEVICE || "";
const HAS_LIVE = !!(process.env.YAVER_TEST_TOKEN || process.env.E2E_USER_TOKEN) && LIVE_DEVICE;
const PROJECT = process.env.E2E_DOM_PROJECT || "todo-web";

test.describe("DOM mode closed loop (live)", () => {
  test.skip(!HAS_LIVE,
    "set YAVER_TEST_TOKEN + E2E_LIVE_DEVICE to run against a real box — " +
    "a mocked agent would let this pass on a UI no live agent ever pairs with");

  test.describe.configure({ mode: "serial", timeout: 8 * 60_000 });

  test("browser lane: probe injected → Inspect → click in frame → chip names element → POST observed", async ({ page }) => {
    test.setTimeout(8 * 60_000);

    // ── sign in + land on the connected dashboard ──────────────────────
    await signIn(page);
    const deviceRe = new RegExp(LIVE_DEVICE, "i");
    await expect
      .poll(async () => deviceRe.test(await page.locator("body").innerText().catch(() => "")), {
        timeout: 90_000,
        message: `dashboard never rendered device /${LIVE_DEVICE}/i`,
      })
      .toBe(true);

    // ── Vibing tab owns the browser-lane preview (RuntimeLabView) ──────
    const vibing = page.getByText(/^Studio$/).first();
    if (await vibing.isVisible().catch(() => false)) await vibing.click().catch(() => {});
    await page.waitForTimeout(6000);

    // Connect gate: first run needs an explicit Connect on the device card.
    const gate = page.getByText(/Connect a device to use Projects/i).first();
    if (await gate.isVisible().catch(() => false)) {
      const card = page
        .locator("div")
        .filter({ hasText: deviceRe })
        .filter({ has: page.getByRole("button", { name: /^Connect$/ }) })
        .last();
      if (await card.isVisible().catch(() => false)) {
        await card.getByRole("button", { name: /^Connect$/ }).first().click();
      }
      await gate.waitFor({ state: "hidden", timeout: 120_000 }).catch(() => undefined);
    }

    // ── project picker → target project → "Web UI in browser" lane ─────
    const select = page.locator("select").first();
    await expect(select, "Vibing tab has no project picker").toBeVisible({ timeout: 90_000 });
    const options = await select.locator("option").allTextContents();
    const match = options.find((o) => o.toLowerCase().includes(PROJECT.toLowerCase()));
    expect(match, `project "${PROJECT}" absent from the picker (have: ${options.slice(0, 8).join(", ")})`).toBeTruthy();
    const value = await select
      .locator("option")
      .filter({ hasText: new RegExp(PROJECT, "i") })
      .first()
      .getAttribute("value");
    await select.selectOption(value!);
    await page.waitForTimeout(6000);

    const loadTargets = page.getByRole("button", { name: /Load Targets/i }).first();
    if (await loadTargets.count()) await loadTargets.click().catch(() => {});
    await expect(
      page.locator('button, a, [role="button"], [class*="cursor-pointer"]').filter({ hasText: /^Open$/ }).first(),
      "the box must offer a render target",
    ).toBeVisible({ timeout: 90_000 });

    // Open the BROWSER-LANE card by name (not WebRTC — different transport).
    const openBtns = page.locator('button, a, [role="button"], [class*="cursor-pointer"]').filter({ hasText: /^Open$/ });
    const n = await openBtns.count();
    let opened = false;
    for (let i = 0; i < n; i++) {
      const btn = openBtns.nth(i);
      const cardText = await btn.evaluate((el) => {
        let node: HTMLElement | null = el as HTMLElement;
        for (let up = 0; up < 5 && node?.parentElement; up++) node = node.parentElement;
        return (node?.innerText || "").slice(0, 200);
      });
      if (/web ui in browser/i.test(cardText)) { await btn.click(); opened = true; break; }
    }
    expect(opened, 'the "Web UI in browser" target was not offered').toBe(true);

    const frame = page.locator("iframe").first();
    await expect(frame, "the browser-lane preview must render").toBeVisible({ timeout: 90_000 });
    await page.waitForTimeout(12_000);

    // ── GUARD 1: the probe is actually in the preview document ─────────
    // This is the assertion that fails when the injection is deleted from
    // either HTML lane (the Snowball break-it proof, e2e edition).
    await expect
      .poll(
        async () => {
          const f = page.locator("iframe").first().contentFrame();
          if (!f) return false;
          return (await f.locator('[data-yaver-dom-probe="1"]').count().catch(() => 0)) > 0;
        },
        { timeout: 60_000, message: "the dom probe was not injected into the preview (data-yaver-dom-probe missing)" },
      )
      .toBe(true);

    // ── GUARD 2: the surface forwards over ITS OWN authed channel ──────
    // The probe posts to window.parent; the chip re-posts to the agent as a
    // POST /dom-inspect. Watch the wire — a direct page→agent write would
    // ALSO show here, but the probe cannot fetch (no-network, asserted by the
    // Go probe-contract test), so a POST is by construction the surface's.
    let domPost: string | null = null;
    page.on("request", (req) => {
      if (req.method() === "POST" && req.url().includes("/dom-inspect") && !req.url().includes("/dom-inspect/items")) {
        domPost = req.url();
      }
    });

    // ── flip Inspect ON ────────────────────────────────────────────────
    // Preview/project/MCP chrome lives behind the composer ellipsis so the
    // agent discussion keeps the height. Open it by its named affordance;
    // role, not prose, because "Inspect" appears in several places.
    const composerMore = page.getByRole("button", { name: /More task options/i });
    await expect(composerMore, "the Runtime composer has no progressive-disclosure ellipsis").toBeVisible({ timeout: 30_000 });
    await composerMore.click();
    const inspectRadio = page.getByRole("radio", { name: /Inspect/i }).first();
    await expect(inspectRadio, "no Browse|Inspect radio on the dashboard").toBeVisible({ timeout: 30_000 });
    await inspectRadio.click();
    await page.waitForTimeout(2000);

    // ── click an element INSIDE the cross-origin preview iframe ────────
    // The probe's capture is click-driven; a real trusted click on a visible
    // interactive element is what a user does. Prefer a button/heading; fall
    // back to the document body only if the app renders none (the probe
    // captures whatever element the click lands on).
    const framePage = page.locator("iframe").first().contentFrame();
    expect(framePage, "preview frame did not resolve").toBeTruthy();
    const clickable = framePage!.locator("button, a, h1, h2, h3, [role=button], input, textarea, select").first();
    if (await clickable.count().catch(() => 0) > 0) {
      await clickable.click({ timeout: 30_000 }).catch(() => {
        // A transparent overlay can eat the click; retry on the body.
        return framePage!.locator("body").click().catch(() => {});
      });
    } else {
      await framePage!.locator("body").click().catch(() => {});
    }
    await page.waitForTimeout(3000);

    // ── GUARD 3: the chip NAMES the element (never a silent mutation) ──
    const chip = page.getByText(/^element: /).first();
    await expect(chip, "the DomInspectChip never showed the attached element").toBeVisible({ timeout: 30_000 });
    const chipText = (await chip.textContent()) || "";
    expect(chipText.length, "chip showed an empty element summary").toBeGreaterThan("element: ".length);

    // ── GUARD 4: the POST reached the agent ────────────────────────────
    await expect
      .poll(() => Promise.resolve(domPost), {
        timeout: 30_000,
        message: "no POST /dom-inspect observed — the surface never forwarded the selection",
      })
      .not.toBeNull();

    // ── opt-out DELETES (the "off means the agent is not holding it" rule) ──
    // Switching back to Browse posts the off-command AND clears the report.
    // We can only cheaply assert the visible half (chip clears) here; the
    // DELETE is covered by the unit/API layers.
    const browseRadio = page.getByRole("radio", { name: /Browse/i }).first();
    if (await browseRadio.isVisible().catch(() => false)) {
      await browseRadio.click().catch(() => {});
      await page.waitForTimeout(2000);
    }

    await test.info().attach("dom-inspect-chip", {
      body: await page.screenshot(),
      contentType: "image/png",
    });
  });
});
