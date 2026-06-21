// Deep runtime test of the browser-local sandbox (no auth, no server).
// Drives a real Chromium against http://localhost:3000/sandbox-test and
// exercises: create-from-template -> sql.js tables/rows -> IndexedDB
// persistence across reload -> data CRUD -> esbuild+iframe live preview with
// the postMessage data bridge -> .yaver.tgz export, then verifies the export is
// a real gzip/tar containing a real SQLite file (serverless-compatible).
const { chromium } = require("@playwright/test");
const { execSync } = require("node:child_process");
const fs = require("node:fs");

const BASE = "http://localhost:3000/sandbox-test";
const results = [];
const consoleErrors = [];
const pageErrors = [];
const failedReqs = [];
let cdnHits = [];

function step(name, ok, detail) {
  results.push({ name, ok, detail: detail || "" });
  console.log(`${ok ? "PASS" : "FAIL"}  ${name}${detail ? "  — " + detail : ""}`);
}

(async () => {
  const browser = await chromium.launch({ headless: true });
  const ctx = await browser.newContext({ acceptDownloads: true });
  const page = await ctx.newPage();

  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
  page.on("pageerror", (e) => pageErrors.push(String(e)));
  page.on("requestfailed", (r) => failedReqs.push(`${r.url()} :: ${r.failure()?.errorText}`));
  page.on("requestfinished", (r) => {
    const u = r.url();
    if (u.includes("jsdelivr") || u.includes("sql-wasm") || u.includes("esbuild") || u.includes("fflate")) cdnHits.push(u);
  });

  try {
    // ── Load ──────────────────────────────────────────────────────────────
    await page.goto(BASE, { waitUntil: "networkidle", timeout: 30000 });
    await page.getByRole("button", { name: "+ New app" }).waitFor({ timeout: 15000 });
    step("page loads, sandbox UI renders", true);
    await page.screenshot({ path: "/tmp/sbx-1-load.png" });

    // ── Create from template (Todos) ───────────────────────────────────────
    await page.getByRole("button", { name: "+ New app" }).click();
    await page.getByPlaceholder("My app").fill("Deep Test Todos");
    await page.getByRole("button", { name: /Todos/ }).click();
    await page.getByRole("button", { name: "Create" }).click();
    // Wait for the project detail to show tables.
    await page.getByText("Tables", { exact: true }).waitFor({ timeout: 20000 });
    await page.screenshot({ path: "/tmp/sbx-2-created.png" });

    const tableChips = await page.locator("button.rounded-full").allInnerTexts();
    const hasUsers = tableChips.some((t) => t.includes("users"));
    const hasTodos = tableChips.some((t) => t.includes("todos"));
    step("create-from-template builds sql.js schema", hasUsers && hasTodos, `chips=${JSON.stringify(tableChips)}`);

    // Select todos table, count seeded rows.
    await page.getByRole("button", { name: /^todos/ }).first().click();
    await page.waitForTimeout(500);
    let rowCount = await page.locator("tbody tr").count();
    step("seeded rows present (sql.js query)", rowCount === 3, `rows=${rowCount} (expected 3)`);

    // ── Data CRUD: insert a row ─────────────────────────────────────────────
    await page.locator('input[placeholder="{\\"title\\":\\"hello\\"}"]').fill('{"id":"deep1","title":"Deep inserted","done":false,"owner_id":"alice"}');
    await page.getByRole("button", { name: "Insert", exact: true }).click();
    await page.waitForTimeout(600);
    rowCount = await page.locator("tbody tr").count();
    step("insert row via localProjects (sql.js write + IndexedDB)", rowCount === 4, `rows=${rowCount} (expected 4)`);
    await page.screenshot({ path: "/tmp/sbx-3-inserted.png" });

    // ── Persistence across reload (IndexedDB) ───────────────────────────────
    await page.reload({ waitUntil: "networkidle" });
    await page.getByText("Deep Test Todos").first().waitFor({ timeout: 15000 });
    await page.getByText("Deep Test Todos").first().click();
    await page.getByText("Tables", { exact: true }).waitFor({ timeout: 15000 });
    await page.getByRole("button", { name: /^todos/ }).first().click();
    await page.waitForTimeout(500);
    rowCount = await page.locator("tbody tr").count();
    step("persists across reload (IndexedDB)", rowCount === 4, `rows after reload=${rowCount}`);

    // ── Live preview: esbuild compile + iframe + data bridge ────────────────
    await page.getByRole("button", { name: "Preview app" }).click();
    const frame = page.frameLocator('iframe[title="App preview"]');
    await frame.locator("table").first().waitFor({ timeout: 30000 }); // esbuild compile can take a few s
    const previewRows = await frame.locator("tbody tr").count();
    step("preview renders via esbuild+iframe (reads sql.js over bridge)", previewRows >= 4, `preview rows=${previewRows}`);
    await page.screenshot({ path: "/tmp/sbx-4-preview.png" });

    // Add a row from inside the preview (bridge write path).
    await frame.locator('label:has-text("title") input').fill("Added from preview");
    await frame.getByRole("button", { name: "Add" }).click();
    await page.waitForTimeout(800);
    const previewRows2 = await frame.locator("tbody tr").count();
    step("preview write via bridge (insert)", previewRows2 === previewRows + 1, `before=${previewRows} after=${previewRows2}`);

    // Delete a row from inside the preview.
    await frame.locator("button.del").first().click();
    await page.waitForTimeout(800);
    const previewRows3 = await frame.locator("tbody tr").count();
    step("preview delete via bridge", previewRows3 === previewRows2 - 1, `after delete=${previewRows3}`);
    await page.screenshot({ path: "/tmp/sbx-5-preview-crud.png" });

    // ── Export .yaver.tgz and verify it is serverless-compatible ────────────
    const dl = await Promise.all([
      page.waitForEvent("download", { timeout: 15000 }),
      page.getByRole("button", { name: "Download .tgz" }).click(),
    ]).then((r) => r[0]);
    const path = "/tmp/sbx-export.tgz";
    await dl.saveAs(path);
    const listing = execSync(`tar tzf ${path}`).toString();
    const hasManifest = listing.includes("yaver.serverless.yaml");
    const hasSqlite = listing.includes("data/app.sqlite");
    step("export .yaver.tgz valid tar w/ canonical files", hasManifest && hasSqlite, listing.trim().split("\n").join(", "));

    execSync(`rm -rf /tmp/sbx-x && mkdir -p /tmp/sbx-x && tar xzf ${path} -C /tmp/sbx-x`);
    const dir = fs.readdirSync("/tmp/sbx-x")[0];
    const sqlitePath = `/tmp/sbx-x/${dir}/data/app.sqlite`;
    const head = fs.readFileSync(sqlitePath).subarray(0, 16).toString("latin1");
    const realSqlite = head.startsWith("SQLite format 3");
    step("exported data/app.sqlite is a real SQLite file", realSqlite, `magic="${head.replace(/\0/g, "")}"`);
    // Confirm the inserted row is actually in the exported DB.
    try {
      const total = execSync(`sqlite3 ${sqlitePath} "SELECT count(*) FROM todos"`).toString().trim();
      const deep1 = execSync(`sqlite3 ${sqlitePath} "SELECT count(*) FROM todos WHERE id='deep1'"`).toString().trim();
      step("exported SQLite reflects live mutations", deep1 === "1" && Number(total) >= 4, `todos total=${total}, deep1 present=${deep1}`);
    } catch (e) {
      step("exported SQLite reflects live mutations", false, `sqlite3 not available: ${e.message}`);
    }
  } catch (e) {
    step("EXCEPTION during run", false, String(e).split("\n")[0]);
    await page.screenshot({ path: "/tmp/sbx-error.png" }).catch(() => {});
  }

  await browser.close();

  console.log("\n=== CDN library loads (sql.js/esbuild/fflate) ===");
  console.log(cdnHits.length ? [...new Set(cdnHits)].join("\n") : "(none — libs may have failed to load!)");
  console.log("\n=== console.error ===");
  console.log(consoleErrors.length ? consoleErrors.join("\n") : "(none)");
  console.log("\n=== pageerror ===");
  console.log(pageErrors.length ? pageErrors.join("\n") : "(none)");
  console.log("\n=== failed requests ===");
  console.log(failedReqs.length ? failedReqs.join("\n") : "(none)");

  const passed = results.filter((r) => r.ok).length;
  console.log(`\n=== SUMMARY: ${passed}/${results.length} passed ===`);
  process.exit(results.every((r) => r.ok) ? 0 : 1);
})();
