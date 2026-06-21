// Records a zero-to-hero video: build a SQLite app in the browser sandbox →
// live preview → deploy to a (real, local) Yaver Serverless target → share →
// open the share link and USE the app (read-only, via the scoped token).
const { chromium } = require("@playwright/test");
const fs = require("node:fs");

const TOKEN = JSON.parse(fs.readFileSync("/tmp/sbx-acct.json", "utf8")).token;
const VID_DIR = "/tmp/yaver-video";
const log = (m) => console.log(`• ${m}`);

(async () => {
  fs.rmSync(VID_DIR, { recursive: true, force: true });
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({
    viewport: { width: 1280, height: 800 },
    recordVideo: { dir: VID_DIR, size: { width: 1280, height: 800 } },
  });
  // Owner session so deploy + share are authenticated.
  await ctx.addInitScript((t) => {
    try { localStorage.setItem("yaver_auth_token", t); } catch {}
    document.cookie = `yaver_auth_token=${t}; path=/`;
  }, TOKEN);
  const page = await ctx.newPage();
  page.on("console", (m) => { if (m.type() === "error") console.log("  [console.error]", m.text()); });

  let shareLink = null;
  try {
    log("open sandbox");
    await page.goto("http://localhost:3000/sandbox-test", { waitUntil: "networkidle", timeout: 30000 });
    await page.getByRole("button", { name: "+ New app" }).waitFor({ timeout: 15000 });
    await page.waitForTimeout(1200);

    log("create app from template");
    await page.getByRole("button", { name: "+ New app" }).click();
    await page.waitForTimeout(600);
    await page.getByPlaceholder("My app").fill("Trip Planner");
    await page.getByRole("button", { name: /Todos/ }).click();
    await page.waitForTimeout(500);
    await page.getByRole("button", { name: "Create" }).click();
    await page.getByText("Tables", { exact: true }).waitFor({ timeout: 20000 });
    await page.waitForTimeout(1000);

    log("browse seeded data");
    await page.getByRole("button", { name: /^todos/ }).first().click();
    await page.waitForTimeout(1200);

    log("live preview (esbuild + iframe)");
    await page.getByRole("button", { name: "Preview app" }).click();
    const frame = page.frameLocator('iframe[title="App preview"]');
    await frame.locator("table").first().waitFor({ timeout: 30000 });
    await page.waitForTimeout(800);
    await frame.locator('label:has-text("title") input').fill("Pack bags");
    await page.waitForTimeout(400);
    await frame.getByRole("button", { name: "Add" }).click();
    await page.waitForTimeout(1200);

    log("deploy to Yaver Serverless");
    await page.getByRole("button", { name: "Deploy to serverless" }).click();
    await page.getByRole("button", { name: "Share with a friend" }).waitFor({ timeout: 30000 });
    await page.waitForTimeout(1500);

    log("create share link for a friend");
    await page.getByRole("button", { name: "Share with a friend" }).click();
    const linkEl = page.locator('a[href*="/a?host="]');
    await linkEl.waitFor({ timeout: 20000 });
    shareLink = await linkEl.getAttribute("href");
    log("share link: " + shareLink);
    await page.waitForTimeout(1500);

    log("open the app as a friend (read-only)");
    await page.goto(shareLink, { waitUntil: "networkidle", timeout: 30000 });
    const friendFrame = page.frameLocator('iframe[title]');
    await friendFrame.locator("table").first().waitFor({ timeout: 30000 });
    await page.waitForTimeout(800);
    // Prove read-only: try to add — should error (server 403).
    const titleInput = friendFrame.locator('label:has-text("title") input');
    if (await titleInput.count()) {
      await titleInput.first().fill("friend edit attempt");
      await page.waitForTimeout(300);
      await friendFrame.getByRole("button", { name: "Add" }).click();
      await page.waitForTimeout(1500); // read-only alert / no-op
    }
    log("DONE — friend sees the deployed app with the owner's rows");
  } catch (e) {
    console.log("ERROR:", String(e).split("\n")[0]);
    await page.screenshot({ path: "/tmp/yaver-video-error.png" }).catch(() => {});
  }

  const vpathPromise = page.video() ? page.video().path() : null;
  await page.waitForTimeout(800);
  await ctx.close(); // finalizes the video
  await browser.close();
  const vpath = vpathPromise ? await vpathPromise : null;
  console.log("VIDEO:", vpath || "(none)");
  if (shareLink) console.log("SHARE_LINK:", shareLink);
})();
