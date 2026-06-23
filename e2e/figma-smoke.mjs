// figma-smoke — real-browser end-to-end test of the mini-figma design layer
// against the no-auth dev harness at /sandbox-test. Verifies the blind-written
// renderer/glass/board/persistence code actually works in Chromium.
//
// Run: cd e2e && node figma-smoke.mjs   (dev server must serve :3000)

import { chromium } from "playwright";

const BASE = process.env.BASE || "http://localhost:3000";
const SHOTS = "/tmp/figma-shots";
import { mkdirSync } from "node:fs";
mkdirSync(SHOTS, { recursive: true });

const log = (...a) => console.log("•", ...a);
let failures = 0;
function check(name, cond) {
  if (cond) { console.log("  ✅", name); }
  else { console.log("  ❌", name); failures++; }
}

// Find the preview iframe frame (srcdoc, sandbox=allow-scripts).
function previewFrame(page) {
  for (const f of page.frames()) {
    if (f.name() === "" && f !== page.mainFrame()) {
      // heuristic: the app preview is the only child frame
    }
  }
  const f = page.frames().find((fr) => fr !== page.mainFrame());
  return f;
}

// Read facts from inside the preview frame.
async function frameFacts(page) {
  const f = previewFrame(page);
  if (!f) return null;
  return await f.evaluate(() => {
    const q = (s) => document.querySelector(s);
    const ynodes = Array.from(document.querySelectorAll("[data-ynode]")).map((e) => e.getAttribute("data-ynode"));
    const title = q("h2");
    const quickadd = q('[data-ynode="quickadd"]');
    const rows = document.querySelectorAll("tbody tr").length;
    return {
      ynodes,
      titleText: title ? title.textContent : null,
      quickaddHidden: quickadd ? getComputedStyle(quickadd).display === "none" : null,
      rows,
      hasGlass: !!q("#design-glass"),
      hasBoard: !!q(".board"),
      boardCols: document.querySelectorAll(".bcol").length,
    };
  });
}

// Click the parent page at the center of a frame element (over the glass).
async function clickFrameNode(page, nodeId) {
  const iframe = await page.$('iframe[title="App preview"]');
  const ib = await iframe.boundingBox();
  const f = previewFrame(page);
  const box = await f.evaluate((id) => {
    const el = document.querySelector('[data-ynode="' + id + '"]');
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { x: r.left + Math.min(20, r.width / 2), y: r.top + Math.min(14, r.height / 2) };
  }, nodeId);
  if (!box) throw new Error("node not found in frame: " + nodeId);
  await page.mouse.click(ib.x + box.x, ib.y + box.y);
  await page.waitForTimeout(400);
}

// Drag a frame node via the Design Glass to a Y near the bottom of the iframe
// (→ nearestBeforeId returns null → move to end).
async function dragFrameNodeToEnd(page, nodeId) {
  const iframe = await page.$('iframe[title="App preview"]');
  const ib = await iframe.boundingBox();
  const f = previewFrame(page);
  const box = await f.evaluate((id) => {
    const el = document.querySelector('[data-ynode="' + id + '"]');
    if (!el) return null;
    const r = el.getBoundingClientRect();
    return { cx: r.left + r.width / 2, cy: r.top + r.height / 2 };
  }, nodeId);
  if (!box) throw new Error("drag node not found: " + nodeId);
  await page.mouse.move(ib.x + box.cx, ib.y + box.cy);
  await page.mouse.down();
  await page.mouse.move(ib.x + box.cx, ib.y + ib.height - 60, { steps: 8 });
  await page.mouse.move(ib.x + box.cx, ib.y + ib.height - 12, { steps: 4 });
  await page.mouse.up();
  await page.waitForTimeout(500);
}

async function frameOrder(page) {
  const f = previewFrame(page);
  return await f.evaluate(() =>
    Array.prototype.slice.call(document.getElementById("root").children)
      .map((el) => (el.getAttribute ? el.getAttribute("data-ynode") : null))
      .filter(Boolean),
  );
}

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 1100 } });
page.on("console", (m) => { if (m.type() === "error") console.log("   [page error]", m.text()); });

try {
  log("1. load harness");
  await page.goto(BASE + "/sandbox-test", { waitUntil: "networkidle" });
  await page.waitForTimeout(500);

  log("2. create a Todos project");
  await page.getByRole("button", { name: "+ New app" }).click();
  await page.locator('input[placeholder="My app"]').fill("Todos");
  await page.getByRole("button", { name: /Todos/ }).click();
  await page.getByRole("button", { name: "Create" }).click();
  await page.waitForTimeout(800);
  await page.screenshot({ path: SHOTS + "/01-created.png" });

  log("3. open preview (esbuild compile + iframe)");
  await page.getByRole("button", { name: "Preview app" }).click();
  await page.waitForTimeout(2500);
  const detailText = await page.locator("body").innerText();
  log("   detail panel mentions:", detailText.includes("Compiling") ? "Compiling…" : detailText.includes("Preview failed") ? "PREVIEW FAILED" : "(no compile/err marker)");
  await page.screenshot({ path: SHOTS + "/02a-after-preview-click.png" });
  await page.waitForSelector('iframe[title="App preview"]', { timeout: 40000 });
  // wait for the renderer to mount inside the frame
  for (let i = 0; i < 30; i++) {
    const ff = await frameFacts(page);
    if (ff && ff.ynodes.length) break;
    await page.waitForTimeout(500);
  }
  let ff = await frameFacts(page);
  await page.screenshot({ path: SHOTS + "/02-preview.png" });
  check("renderer mounted (data-ynode present)", ff && ff.ynodes.length >= 2);
  check("title is Todos", ff && /todo/i.test(ff.titleText || ""));
  check("seeded rows render (>=3)", ff && ff.rows >= 3);
  check("quickadd node present", ff && ff.ynodes.includes("quickadd"));

  log("4. toggle Design mode → Design Glass appears");
  await page.getByText(/Design mode/).click();
  await page.waitForTimeout(700);
  ff = await frameFacts(page);
  await page.screenshot({ path: SHOTS + "/03-designmode.png" });
  check("design glass present in design mode", ff && ff.hasGlass === true);

  log("5. select quickadd via glass → inspector → Hide it");
  await clickFrameNode(page, "quickadd");
  await page.screenshot({ path: SHOTS + "/04-selected.png" });
  const inspectorText = await page.locator("body").innerText();
  check("inspector shows Quick add", /Quick add/.test(inspectorText));
  await page.getByText("Hide this widget").click();
  await page.waitForTimeout(700);
  ff = await frameFacts(page);
  await page.screenshot({ path: SHOTS + "/05-hidden.png" });
  check("quickadd hidden override applied in preview", ff && ff.quickaddHidden === true);

  log("6. select title → rename it");
  await clickFrameNode(page, "title");
  const titleInput = page.locator('input[placeholder="(default)"]');
  await titleInput.fill("My Tasks");
  await page.waitForTimeout(700);
  ff = await frameFacts(page);
  await page.screenshot({ path: SHOTS + "/06-retitled.png" });
  check("title override applied (My Tasks)", ff && /my tasks/i.test(ff.titleText || ""));

  log("7. select list → show as kanban board grouped by done");
  await clickFrameNode(page, "list");
  // the board <select> — choose 'done'
  const sel = page.locator("select");
  await sel.selectOption({ label: "done" }).catch(async () => {
    // fall back to value
    await sel.selectOption("done");
  });
  await page.waitForTimeout(800);
  ff = await frameFacts(page);
  await page.screenshot({ path: SHOTS + "/07-board.png" });
  check("kanban board renders", ff && ff.hasBoard === true);
  check("board has columns", ff && ff.boardCols >= 1);

  log("7b. undo / redo the board toggle");
  await page.getByRole("button", { name: "Undo" }).click();
  await page.waitForTimeout(500);
  ff = await frameFacts(page);
  check("undo turned the board off", ff && ff.hasBoard === false);
  await page.getByRole("button", { name: "Redo" }).click();
  await page.waitForTimeout(500);
  ff = await frameFacts(page);
  check("redo turned the board back on", ff && ff.hasBoard === true);

  log("7c. drag-reorder a widget via the Design Glass");
  const before = await frameOrder(page);
  await dragFrameNodeToEnd(page, "title");
  await page.screenshot({ path: SHOTS + "/07c-dragged.png" });
  const after = await frameOrder(page);
  check("glass drag moved 'title' to the end", after[after.length - 1] === "title" && before.join() !== after.join());

  log("8. persistence across reload");
  await page.reload({ waitUntil: "networkidle" });
  await page.waitForTimeout(800);
  // re-open the same project + preview
  await page.getByRole("button", { name: /Todos|My Tasks|todos/ }).first().click().catch(() => {});
  // the project card is in the left list — click the first project card
  const card = page.locator("button:has-text('todos')").first();
  await card.click().catch(() => {});
  await page.getByRole("button", { name: "Preview app" }).click();
  await page.waitForSelector('iframe[title="App preview"]', { timeout: 20000 });
  for (let i = 0; i < 30; i++) {
    const f2 = await frameFacts(page);
    if (f2 && f2.ynodes.length) break;
    await page.waitForTimeout(500);
  }
  ff = await frameFacts(page);
  await page.screenshot({ path: SHOTS + "/08-after-reload.png" });
  check("design persisted across reload (board still on)", ff && ff.hasBoard === true);
  check("title override persisted (My Tasks)", ff && /my tasks/i.test(ff.titleText || ""));
  const orderAfterReload = await frameOrder(page);
  check("dragged layout order persisted across reload (title last)", orderAfterReload[orderAfterReload.length - 1] === "title");

  console.log("\n" + (failures === 0 ? "ALL CHECKS PASSED ✅" : failures + " CHECK(S) FAILED ❌"));
} catch (e) {
  console.error("SCRIPT ERROR:", e.message);
  await page.screenshot({ path: SHOTS + "/error.png" }).catch(() => {});
  failures++;
} finally {
  await browser.close();
  process.exit(failures === 0 ? 0 : 1);
}
