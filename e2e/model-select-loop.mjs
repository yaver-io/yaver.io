/**
 * Model-select closed loop — does the model dropdown KEEP what the user picks?
 *
 *   node e2e/model-select-loop.mjs
 *
 * ── What it proves ─────────────────────────────────────────────────────────
 *
 * Captured on video 2026-08-02 10:09: opening MODEL, choosing "GPT-5 Codex",
 * and watching the select snap straight back to "GPT-5.4". The seeding effect
 * in RuntimeLabView called setSelectedModel(explicitModel) unconditionally with
 * `selectedModel` in its own dependency array, so any pick re-ran the effect,
 * saw the machine default still saved, and overwrote the user within a frame.
 *
 * The model was therefore unchangeable on any machine with a saved default —
 * and the only way to change the saved default was to pick a different model
 * first. A closed loop with no exit, on an account whose Codex login rejects
 * the pinned model outright.
 *
 * This drives the REAL dashboard and asserts the value STICKS. A unit test
 * cannot catch it: the bug lives in an effect's control flow against a real
 * React commit, which is exactly the class that only shows up in a browser.
 *
 * Verdicts follow the house rule — PIXELS (saw it), NAMED (a cause was stated),
 * SILENT (a spinner or nothing). SILENT is the only failing verdict. Here the
 * pass condition is literally a rendered value, so this loop is PIXELS or bust.
 *
 * ── Credentials ────────────────────────────────────────────────────────────
 *
 * From the gitignored .env.test, never inlined and never logged; the account is
 * echoed as its DOMAIN only. Keep that property if you extend this.
 */
import { chromium } from '@playwright/test';
import { readFileSync } from 'fs';

const APP = process.env.WEB_URL || 'https://yaver.io';
const ENV_PATH = process.env.YAVER_ENV_FILE || new URL('../.env.test', import.meta.url).pathname;

const env = Object.fromEntries(
  readFileSync(ENV_PATH, 'utf8')
    .split('\n').filter((l) => l.includes('='))
    .map((l) => [l.slice(0, l.indexOf('=')).trim(), l.slice(l.indexOf('=') + 1).trim()]),
);
if (!env.YAVER_TEST_EMAIL || !env.YAVER_TEST_PASSWORD) {
  console.error(`FAIL: credentials missing from ${ENV_PATH}`);
  process.exit(2);
}
const ACCOUNT_HINT = `<redacted>@${String(env.YAVER_TEST_EMAIL).split('@')[1] || 'unknown'}`;

const steps = [];
const step = (name, ok, detail = '') => {
  steps.push({ name, ok, detail });
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? ` — ${detail}` : ''}`);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 } });
const page = await ctx.newPage();

try {
  await page.goto(`${APP}/auth`, { waitUntil: 'domcontentloaded', timeout: 60_000 });
  await page.getByPlaceholder('Email address').waitFor({ timeout: 20_000 });
  await page.getByPlaceholder('Email address').fill(env.YAVER_TEST_EMAIL);
  await page.getByPlaceholder('Password').fill(env.YAVER_TEST_PASSWORD);
  await page.getByRole('button', { name: /^sign in$/i }).click();
  await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 30_000 });
  if (page.url().includes('/survey')) await page.goto(`${APP}/dashboard`);
  await sleep(6000);
  step('signs in', /Devices|Studio/i.test(await page.evaluate(() => document.body.innerText)), ACCOUNT_HINT);

  await page.getByText(/^Studio$/).first().click().catch(() => {});
  await sleep(8000);

  // The model select lives behind "Edit" on the session header.
  await page.getByRole('button', { name: /^Edit$/ }).first().click().catch(() => {});
  await sleep(2500);

  // Identify the Model <select> by its OPTIONS, and require it to be VISIBLE.
  //
  // Both matter. Picking by DOM position drifts with layout; and the page
  // renders TWO model selects — one visible (the Edit panel) and one 0x0
  // hidden. `.last()` grabbed the hidden one and selectOption sat there until
  // it timed out, which reads as "the dropdown is broken" when it is the test
  // that is aiming at the wrong element. Assert the choice is unambiguous
  // rather than silently taking the first match.
  // Anchor on the label's own <span>Model</span> — the ONLY unambiguous handle.
  //
  // Two looser attempts each grabbed the wrong control and "passed": matching
  // the word Claude hit the RUNNER select (and switched the runner); matching
  // "a slash or a digit" hit the PROJECT select (whose options are absolute
  // paths). A test that mutates a different control than the one it names is
  // worse than no test — it reports green about something it never touched.
  const modelLabels = page.locator('label').filter({ has: page.locator('span', { hasText: /^Model$/ }) });
  // Scope to VISIBLE. The page renders the Model control twice — one on screen,
  // one 0x0 — and driving the hidden twin is how the first run sat until
  // selectOption timed out and read as "the dropdown is broken".
  const modelSelect = modelLabels.locator('select:visible').first();
  const visibleCount = await modelLabels.locator('select:visible').count();
  step('exactly one VISIBLE Model select', visibleCount === 1, `${visibleCount} visible`);
  const present = await modelSelect.count().then((n) => n > 0).catch(() => false);
  step('the model dropdown is present', present);
  if (!present) throw new Error('no model select on screen — Edit panel did not open');

  const options = await modelSelect.locator('option').evaluateAll((els) =>
    els.map((e) => ({ value: e.value, label: e.textContent?.trim() || '' })),
  );
  step('it offers more than one model', options.length > 1, options.map((o) => o.value).join(', '));

  const before = await modelSelect.inputValue();
  const target = options.find((o) => o.value && o.value !== before);
  step('there is a different model to switch to', !!target, target ? `${before} -> ${target.value}` : `only ${before}`);
  if (!target) throw new Error('nothing to switch to');

  // THE ASSERTION. Select, then wait long enough for the seeding effect to have
  // re-run several times (the old bug reverted within a frame; polls and
  // device refreshes re-trigger it for seconds afterwards).
  await modelSelect.selectOption(target.value);
  await sleep(600);
  const immediately = await modelSelect.inputValue();
  step('the pick applies at all', immediately === target.value, `${immediately}`);

  await sleep(6000);
  const afterSettling = await modelSelect.inputValue();
  step(
    'the pick SURVIVES — the seeding effect does not overwrite the user',
    afterSettling === target.value,
    afterSettling === target.value ? `stayed ${afterSettling}` : `SNAPPED BACK to ${afterSettling}`,
  );

  // The header must agree with the control; two truths on one screen is the
  // false-green shape this suite exists to catch.
  const header = await page.evaluate(() => document.body.innerText);
  const headerModel = header.match(/MODEL\s*\/?\s*([\w.\-]+)/i)?.[1] || '';
  step(
    'the session header agrees with the dropdown',
    !headerModel || headerModel === afterSettling,
    headerModel ? `header=${headerModel} select=${afterSettling}` : 'no header model rendered',
  );

  // The control must never be disabled — a greyed-out select is the same dead
  // end wearing a different hat.
  step('the model select is not disabled', !(await modelSelect.isDisabled()));
} catch (err) {
  step('loop completed without throwing', false, String(err.message).slice(0, 160));
} finally {
  await ctx.close().catch(() => {});
  await browser.close().catch(() => {});
}

const failed = steps.filter((s) => !s.ok);
console.log(`\n${failed.length === 0 ? 'ALL PASS' : `${failed.length} FAILED`} (${steps.length} checks) — ${ACCOUNT_HINT}`);
process.exit(failed.length === 0 ? 0 : 1);
