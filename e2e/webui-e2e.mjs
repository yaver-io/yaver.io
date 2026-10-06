/**
 * End-to-end through the REAL product: Chromium → yaver.io → Mac mini.
 *
 *   node e2e/webui-e2e.mjs
 *
 * A FRESH profile every run, on purpose. A saved session proves the dashboard
 * renders; it does not prove a user can get in. Sign-in is the step that broke
 * twice today (CORS preflight on Cache-Control, then the email/password path),
 * so it is the step this must actually perform.
 *
 * Credentials come from .env.test (gitignored) — never inline, never logged.
 *
 * Steps, each pass/fail on its own so a failure says WHICH part broke:
 *   1. dashboard loads
 *   2. sign in with email + password
 *   3. the Mac mini appears as a device
 *   4. connect to it
 *   5. dispatch a task and see the app report a live status
 *
 * Step 5 asserts the loop CLOSES — task created, status rendered by the app
 * itself. It does not judge the agent's answer; that is a different test and
 * claiming otherwise would be the false green this suite exists to catch.
 */
import { chromium } from '@playwright/test';
import { readFileSync, mkdtempSync } from 'fs';
import { tmpdir } from 'os';
import path from 'path';

const URL = process.env.WEBUI_URL || 'https://yaver.io/auth';  // the real login page
const VIDEO_DIR = process.env.VIDEO_DIR || '/tmp/yaver-webui-e2e';
const env = Object.fromEntries(
  readFileSync('/Users/kivanccakmak/Workspace/yaver.io/.env.test', 'utf8')
    .split('\n').filter((l) => l.includes('='))
    .map((l) => [l.slice(0, l.indexOf('=')).trim(), l.slice(l.indexOf('=') + 1).trim()]),
);
const EMAIL = env.YAVER_TEST_EMAIL;
const PASSWORD = env.YAVER_TEST_PASSWORD;
const DEVICE = process.env.E2E_DEVICE || 'Mobiles-Mac-mini';

const steps = [];
const step = (name, ok, detail = '') => {
  steps.push({ name, ok, detail });
  console.log(`  ${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const profile = mkdtempSync(path.join(tmpdir(), 'yaver-e2e-fresh-'));
const browser = await chromium.launch();
const ctx = await browser.newContext({
  viewport: { width: 1280, height: 900 },
  recordVideo: { dir: VIDEO_DIR, size: { width: 1280, height: 900 } },
});
const page = await ctx.newPage();
const errs = [];
page.on('pageerror', (e) => errs.push(String(e.message).slice(0, 140)));
const body = () => page.evaluate(() => (document.body?.innerText || '').replace(/\n+/g, ' | ')).catch(() => '');

try {
  await page.goto(URL, { waitUntil: 'domcontentloaded', timeout: 120_000 });
  await sleep(10000);
  let txt = await body();
  // /dashboard with no session renders an AUTH GATE ("Sign in to continue"),
  // not the provider list. My first version looked straight for "Continue with
  // Email", never found it, so never logged in — and the sign-in step PASSED
  // because its check was a negation that is trivially true on a page
  // containing neither string. A false pass in the harness itself, which is the
  // exact failure this suite exists to catch.
  step('dashboard loads', /Sign in to continue|Continue with|Devices|Projects|Studio/i.test(txt), txt.slice(0, 70));

  const gate = page.getByRole('button', { name: /^sign in$/i }).first();
  if (await gate.isVisible().catch(() => false)) {
    await gate.click();
    await sleep(6000);
    txt = await body();
  }

  // The real /auth page has NO "Continue with Email" button: the providers sit
  // above an `or` divider, and email/password are plain fields with Sign In
  // beneath them. My earlier version clicked a control that does not exist, so
  // it never typed anything — and then reported on a login it had not attempted.
  const emailField = page.locator('input[type="email"], input[name*="email" i], input[placeholder*="mail" i]').first();
  if (await emailField.isVisible({ timeout: 20_000 }).catch(() => false)) {
    await emailField.fill(EMAIL);
    const pwField = page.locator('input[type="password"]').first();
    await pwField.fill(PASSWORD);
    await sleep(500);
    // Submit from the password field so the form's own handler runs, rather than
    // guessing which of several "Sign In" strings is the button.
    await pwField.press('Enter').catch(() => {});
    for (let i = 0; i < 16; i++) {
      await sleep(4000);
      txt = await body();
      if (/Devices|Projects|Studio|Chat/i.test(txt)) break;
    }
  } else {
    txt = await body();
  }
  // POSITIVE assertion. "the login strings are absent" was satisfied by a page
  // that had never shown them; reaching the app is the only honest proof.
  step('sign in', /Devices|Projects|Studio|Chat/i.test(txt), EMAIL);

  // The device must be VISIBLE before connecting — "not listed" and "listed but
  // unreachable" are different failures and the report should say which.
  let sawDevice = false;
  for (let i = 0; i < 10; i++) {
    txt = await body();
    if (new RegExp(DEVICE, 'i').test(txt)) { sawDevice = true; break; }
    await sleep(4000);
  }
  step(`${DEVICE} listed`, sawDevice, sawDevice ? '' : 'device never appeared in the dashboard');

  let connected = '';
  if (sawDevice) {
    const connectBtn = page.getByText(/^connect/i).first();
    if (await connectBtn.isVisible().catch(() => false)) await connectBtn.click().catch(() => {});
    for (let i = 0; i < 14; i++) {
      await sleep(5000);
      txt = await body();
      if (/Connected|disconnect/i.test(txt)) { connected = txt; break; }
    }
  }
  step('connect to the machine', !!connected,
    (connected.match(/Connected[^|]*(\|[^|]*){0,2}/) || [''])[0].slice(0, 70));

  // Chat lives on its own tab — the composer is not on the landing view, which
  // is why the first run reported "no composer found" as if the product lacked
  // one. Navigate first, then look.
  const chatTab = page.getByText(/^Chat$/).first();
  if (await chatTab.isVisible().catch(() => false)) {
    await chatTab.click();
    await sleep(5000);
  }
  step('open Chat', await page.getByText(/^Chat$/).first().isVisible().catch(() => false));

  // ── Remote runner sign-in, surfaced or not ──────────────────────────────
  //
  // The agent reports claude authConfigured=false / ready=false with
  // "No Claude Code credential detected on this machine". The product must
  // therefore OFFER a sign-in rather than silently accept a message that will
  // wait forever. This asserts the offer exists — the fix shipped today made
  // the CTA appear whenever the agent says a runner cannot run.
  txt = await body();
  const claimsSignedIn = /Claude Code\s*\|?\s*✓?\s*SIGNED IN/i.test(txt);
  const offersSignIn = /Sign in to|Sign in →|not signed in/i.test(txt);
  step('does NOT claim an unsigned runner is signed in', !claimsSignedIn,
    claimsSignedIn ? 'still shows "SIGNED IN" for claude, which the agent denies' : '');
  step('offers remote sign-in for the unsigned runner', offersSignIn,
    offersSignIn ? '' : 'no sign-in CTA anywhere — a stuck chat has no way out');

  // Dispatch real work.
  let dispatched = false;
  const composer = page.locator('textarea, input[type="text"]').filter({ hasNot: page.locator('[type="password"]') }).last();
  if (await composer.isVisible().catch(() => false)) {
    await composer.fill('List the files in this project. Do not modify anything.').catch(() => {});
    await sleep(800);
    await composer.press('Enter').catch(() => {});
    dispatched = true;
  }
  step('dispatch a task', dispatched, dispatched ? '' : 'no composer found on the page');

  let live = '';
  if (dispatched) {
    for (let i = 0; i < 16; i++) {
      await sleep(5000);
      txt = await body();
      if (/running|working|queued|thinking|completed|Waiting for response/i.test(txt)) { live = txt; break; }
    }
  }
  const stuck = /Waiting for response from AI agent/i.test(live);
  step('task reports a live status', !!live && !stuck,
    stuck ? 'STUCK on "Waiting for response from AI agent…" — the known signed-in-chip bug' : live.slice(0, 80));

  await page.screenshot({ path: '/tmp/yaver-webui-e2e.png', fullPage: false });
} finally {
  const passed = steps.filter((s) => s.ok).length;
  console.log('\n===== WEBUI E2E: chromium → yaver.io → mac mini =====');
  for (const s of steps) console.log(`${s.ok ? 'PASS' : 'FAIL'}  ${s.name}`);
  console.log(`\n${passed}/${steps.length} steps passed`);
  if (errs.length) console.log(`page errors: ${errs.slice(0, 3).join(' || ')}`);
  console.log(`video: ${VIDEO_DIR}\nscreenshot: /tmp/yaver-webui-e2e.png\nprofile: ${profile}`);
  await ctx.close();
  await browser.close();
  if (passed < steps.length) process.exitCode = 1;
}
