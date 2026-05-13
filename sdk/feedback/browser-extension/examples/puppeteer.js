/**
 * Drive the Yaver browser extension from Puppeteer.
 *
 *   npm i -D puppeteer
 *   node puppeteer.js https://vercel.com
 *
 * Puppeteer's headless mode (true) drops extensions; use `headless: 'new'` or
 * leave the browser visible.
 */

import puppeteer from 'puppeteer';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';

const EXT_DIR = resolve(dirname(fileURLToPath(import.meta.url)), '..');

async function main() {
  const url = process.argv[2] ?? 'https://example.com';
  const selector = process.env.YAVER_SELECTOR;
  const headless = process.env.YAVER_HEADLESS === '1' ? 'new' : false;

  const browser = await puppeteer.launch({
    headless,
    args: [
      `--disable-extensions-except=${EXT_DIR}`,
      `--load-extension=${EXT_DIR}`,
    ],
  });

  try {
    const page = await browser.newPage();
    await page.goto(url, { waitUntil: 'networkidle2' });
    await page.waitForFunction(() => !!window.__yaver, { timeout: 5000 });

    const result = selector
      ? await page.evaluate((s) => window.__yaver.captureSelector(s), selector)
      : await page.evaluate(() => window.__yaver.capturePage());

    if (!result.ok) {
      console.error('capture failed:', result);
      process.exit(1);
    }
    const nodes = result.bundle?.styles?.nodes?.length ?? 0;
    console.log(`captured ${url} · ${nodes} nodes · sent_to_agent=${result.sent}`);
  } finally {
    await browser.close();
  }
}

main();
