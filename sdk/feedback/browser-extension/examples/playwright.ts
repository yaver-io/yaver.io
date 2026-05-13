/**
 * Drive the Yaver browser extension from Playwright.
 *
 *   npm i -D playwright
 *   npx tsx playwright.ts https://linear.app
 *
 * Playwright loads extensions only via launchPersistentContext (Chromium only)
 * and only in headed mode by default. For headless, pass --headless=new in args.
 */

import { chromium, type BrowserContext, type Page } from 'playwright';
import { fileURLToPath } from 'node:url';
import { dirname, resolve } from 'node:path';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);
const EXT_DIR = resolve(__dirname, '..');

interface CaptureResult {
  ok: boolean;
  sent?: boolean;
  bundle?: {
    metadata: { mode: string; selector: string };
    styles: { nodes: unknown[] };
  };
  error?: string;
}

async function launch(headless = false): Promise<BrowserContext> {
  const userDataDir = mkdtempSync(`${tmpdir()}/yaver-pw-`);
  return chromium.launchPersistentContext(userDataDir, {
    headless,
    args: [
      `--disable-extensions-except=${EXT_DIR}`,
      `--load-extension=${EXT_DIR}`,
      ...(headless ? ['--headless=new'] : []),
    ],
  });
}

async function waitForYaver(page: Page, timeoutMs = 5000): Promise<void> {
  await page.waitForFunction(() => !!(window as Window & { __yaver?: unknown }).__yaver, undefined, {
    timeout: timeoutMs,
  });
}

async function capture(
  page: Page,
  opts: { selector?: string; fullPage?: boolean } = {},
): Promise<CaptureResult> {
  await waitForYaver(page);
  if (opts.selector) {
    return page.evaluate(
      (sel) => (window as Window & { __yaver: { captureSelector: (s: string) => Promise<CaptureResult> } }).__yaver.captureSelector(sel),
      opts.selector,
    );
  }
  if (opts.fullPage) {
    return page.evaluate(() => (window as Window & { __yaver: { captureFullPage: () => Promise<CaptureResult> } }).__yaver.captureFullPage());
  }
  return page.evaluate(() => (window as Window & { __yaver: { capturePage: () => Promise<CaptureResult> } }).__yaver.capturePage());
}

async function main() {
  const url = process.argv[2] ?? 'https://example.com';
  const selector = process.env.YAVER_SELECTOR;
  const headless = process.env.YAVER_HEADLESS === '1';

  const ctx = await launch(headless);
  const page = await ctx.newPage();
  try {
    await page.goto(url, { waitUntil: 'networkidle' });
    const result = await capture(page, { selector, fullPage: !selector });
    if (!result.ok) {
      console.error('capture failed:', result);
      process.exit(1);
    }
    const nodes = result.bundle?.styles?.nodes?.length ?? 0;
    console.log(`captured ${url} · ${nodes} nodes · sent_to_agent=${result.sent}`);
  } finally {
    await ctx.close();
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
