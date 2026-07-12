#!/usr/bin/env node
/**
 * Capture Apple Watch App Store screenshots from generate-watch.html.
 * Renders each .screen (410x502, Apple Watch Ultra) at 1x = exact pixels.
 * App Store Connect's single Apple Watch slot accepts 410x502 and downscales
 * to the smaller watch sizes automatically.
 *
 * Usage: npx playwright install chromium && node scripts/screenshots/capture-watch.mjs
 */
import pw from '/Users/kivanccakmak/Workspace/yaver.io/e2e/node_modules/playwright/index.js';
const { chromium } = pw;
import { fileURLToPath } from 'url';
import path from 'path';
import fs from 'fs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const html = path.join(__dirname, 'generate-watch.html');
const outDir = path.join(__dirname, 'output-watch');

const shots = [
  { id: 'w1', name: '01_result' },
  { id: 'w2', name: '02_speak' },
  { id: 'w3', name: '03_working' },
  { id: 'w4', name: '04_wake' },
];

async function main() {
  if (!fs.existsSync(outDir)) fs.mkdirSync(outDir, { recursive: true });
  const browser = await chromium.launch({
    executablePath: '/Users/kivanccakmak/Library/Caches/ms-playwright/chromium_headless_shell-1228/chrome-headless-shell-mac-arm64/chrome-headless-shell',
  });
  const context = await browser.newContext({
    viewport: { width: 900, height: 1200 },
    deviceScaleFactor: 1,
  });
  const page = await context.newPage();
  await page.goto(`file://${html}`);
  await page.waitForTimeout(400);

  for (const { id, name } of shots) {
    const el = await page.$(`#${id}`);
    if (!el) { console.log(`  skip #${id}`); continue; }
    const outPath = path.join(outDir, `${name}.png`);
    await el.screenshot({ path: outPath });
    console.log(`  captured ${name}.png`);
  }

  await context.close();
  await browser.close();

  for (const f of fs.readdirSync(outDir).filter(f => f.endsWith('.png'))) {
    const s = fs.statSync(path.join(outDir, f));
    console.log(`  ${f} (${(s.size / 1024).toFixed(0)} KB)`);
  }
  console.log('Done → scripts/screenshots/output-watch/');
}
main().catch(e => { console.error(e); process.exit(1); });
