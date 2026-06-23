// figma-ai — real-browser test of the select-then-prompt AI hybrid against the
// /sandbox-test harness. No real creds: Playwright intercepts /auth/validate
// (so useAuth keeps an injected token) and /v1/chat/completions (the gateway),
// so every line of OUR path runs — designChat builds the scoped request, the
// gateway client posts it, the patches apply, the preview re-renders — with only
// the z.ai upstream stubbed. Requires the dev server started with
// NEXT_PUBLIC_YAVER_GATEWAY_URL set (any non-empty value).
//
// Run: cd web && NEXT_PUBLIC_YAVER_GATEWAY_URL=https://stub.test npm run dev
//      cd e2e && node figma-ai.mjs

import { chromium } from "playwright";

const BASE = process.env.BASE || "http://localhost:3000";
let fails = 0;
const check = (name, cond) => { console.log((cond ? "  ✅ " : "  ❌ ") + name); if (!cond) fails++; };

function previewFrame(page) { return page.frames().find((fr) => fr !== page.mainFrame()); }
async function frameFacts(page) {
  const f = previewFrame(page);
  if (!f) return null;
  return await f.evaluate(() => {
    const qa = document.querySelector('[data-ynode="quickadd"]');
    return {
      ynodes: Array.from(document.querySelectorAll("[data-ynode]")).map((e) => e.getAttribute("data-ynode")),
      quickaddHidden: qa ? getComputedStyle(qa).display === "none" : null,
    };
  });
}
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
  await page.mouse.click(ib.x + box.x, ib.y + box.y);
  await page.waitForTimeout(400);
}

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 1100 } });

// Inject a session token BEFORE any page script runs.
await page.addInitScript(() => {
  try { localStorage.setItem("yaver_auth_token", "e2e-fake-token"); } catch {}
});
// useAuth validates the token against Convex — fulfil it so the token is kept.
await page.route("**/auth/validate", (route) =>
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ user: { userId: "u1", email: "beta@test", surveyCompleted: true } }) }),
);
// The gateway call: capture the request body, return scoped patches.
let gatewayBody = null;
await page.route("**/v1/chat/completions", (route) => {
  gatewayBody = route.request().postData();
  const content = JSON.stringify([{ op: "set", nodeId: "quickadd", props: { hidden: true } }]);
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ choices: [{ message: { content } }] }) });
});

try {
  console.log("• load harness (with injected token + gateway configured)");
  await page.goto(BASE + "/sandbox-test", { waitUntil: "networkidle" });
  await page.waitForTimeout(500);

  console.log("• create + preview + design mode");
  await page.getByRole("button", { name: "+ New app" }).click();
  await page.locator('input[placeholder="My app"]').fill("Todos");
  await page.getByRole("button", { name: /Todos/ }).click();
  await page.getByRole("button", { name: "Create" }).click();
  await page.waitForTimeout(700);
  await page.getByRole("button", { name: "Preview app" }).click();
  await page.waitForSelector('iframe[title="App preview"]', { timeout: 20000 });
  for (let i = 0; i < 30; i++) { const ff = await frameFacts(page); if (ff && ff.ynodes.length) break; await page.waitForTimeout(500); }
  await page.getByText(/Design mode/).click();
  await page.waitForTimeout(600);

  console.log("• AI input should appear (gateway configured + token kept)");
  const aiInput = page.locator('input[placeholder*="tweak the layout"], input[placeholder*="Change the selected"]');
  check("AI layout input is present", (await aiInput.count()) > 0);

  console.log("• select quickadd → scoped chip → prompt 'hide this' → Apply");
  await clickFrameNode(page, "quickadd");
  const chip = await page.getByText(/Editing: Quick add/).count();
  check("scoped 'Editing: Quick add' chip shown after selecting", chip > 0);

  const scopedInput = page.locator('input[placeholder*="Change the selected"]');
  check("placeholder switched to scoped form", (await scopedInput.count()) > 0);
  await scopedInput.fill("hide this");
  await page.getByRole("button", { name: "Apply" }).click();
  await page.waitForTimeout(900);

  check("gateway received the request", !!gatewayBody);
  check("request scoped to the selected node (mentions SELECTED quickadd)", !!gatewayBody && gatewayBody.includes("SELECTED") && gatewayBody.includes("quickadd"));
  // the model reply applied:
  const ff = await frameFacts(page);
  check("AI patch applied — quickadd hidden in preview", ff && ff.quickaddHidden === true);
  await page.screenshot({ path: "/tmp/figma-shots/ai-scoped.png" });

  console.log("\n" + (fails === 0 ? "ALL AI CHECKS PASSED ✅" : fails + " FAILED ❌"));
} catch (e) {
  console.error("SCRIPT ERROR:", e.message);
  fails++;
} finally {
  await browser.close();
  process.exit(fails === 0 ? 0 : 1);
}
