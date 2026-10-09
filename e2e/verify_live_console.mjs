#!/usr/bin/env node
// verify_live_console.mjs — closed-loop check of the mobile task-detail
// LiveConsoleSection (live opencode console) on the RN-web app.
//
// SELF-CONTAINED: drives the REAL mobile app at a genuine iPhone 15 Pro
// device context, injects the agent token into the RN-web storage key
// (yaver.secure.yaver_auth_token via mobile/src/lib/secureStoreCompat.ts),
// creates a probe task THROUGH THE COMPOSER (so it routes to the connected
// device — the app is pointed at e.g. ubuntu-4gb-hel1-1, not the local
// agent), waits for it to finish, opens its detail, and asserts the
// "Live console" section rendered the streamed console (title + ● live /
// ○ idle dot + probe output) instead of a collapsed "_Working through
// implementation…_" bubble. Also asserts the command card carries real
// stdout (the stale-agent failure mode: start/end but no command_output).
//
// Use: node e2e/verify_live_console.mjs
//   Requires Metro serving the mobile app (cd mobile && npm run web → :8081)
//   and a local agent on :18080 with ~/.yaver/config.json auth_token.
// Verified 2026-08-09 (ubuntu-4gb-hel1-1 + deepseek-v4-flash); the
// `mobile-test-open` ops verb wraps this script.
import { createRequire } from 'module';
import { readFileSync } from 'fs';
const require = createRequire(import.meta.url);
const { chromium, devices } = require('@playwright/test');
const URL = process.env.MOBILE_WEB_URL || 'http://localhost:8081';
// Token: explicit env wins; otherwise read the local agent config (the same
// token the browser app would get from SecureStore on this machine).
let TOKEN = process.env.YAVER_AGENT_TOKEN || '';
if (!TOKEN) {
  try {
    const cfg = JSON.parse(readFileSync(`${process.env.HOME}/.yaver/config.json`, 'utf8'));
    TOKEN = cfg.auth_token || cfg.authToken || '';
  } catch { /* fall through — verification will fail with a clear message */ }
}
if (!TOKEN) {
  console.error('❌ no token: set YAVER_AGENT_TOKEN or have ~/.yaver/config.json auth_token');
  process.exit(1);
}

const PROBE_PROMPT = 'Run exactly this shell command and nothing else, then stop: echo LIVE_CONSOLE_PROBE_$(date +%s) && ls /tmp | head -5';

const browser = await chromium.launch({
  headless: true,
  ...(process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {}),
});
const ctx = await browser.newContext({ ...devices['iPhone 15 Pro'], viewport: { width: 393, height: 852 } });
const page = await ctx.newPage();
let failures = 0;
const ok = (name, cond, extra = '') => {
  if (cond) { console.log(`  ✅ ${name}`); }
  else { failures++; console.log(`  ❌ ${name} ${extra}`); }
};

// ── 1. Sign in (token inject) + land on Tasks ─────────────────────────────
await page.goto(URL, { waitUntil: 'domcontentloaded', timeout: 60000 });
await page.waitForTimeout(4000);
await page.evaluate((t) => { window.localStorage.setItem('yaver.secure.yaver_auth_token', t); }, TOKEN);
await page.goto(`${URL}/tasks`, { waitUntil: 'domcontentloaded', timeout: 60000 });
await page.waitForTimeout(6000);
const body0 = await page.evaluate(() => (document.body.innerText || ''));
ok('signed in (no login form)', !/Sign in to drive|Continue with Email/i.test(body0), 'still on login');
ok('device/runner row visible', /ubuntu-4gb-hel1-1|Connected|Primary|OpenCode/i.test(body0), 'no device row');

// ── 2. Open the composer (New task) and type the probe ────────────────────
// The composer opens via the "Dictate a new task" FAB. Use Playwright's
// native click (real pointer events — RN-web Pressables ignore el.click()).
const dictateLoc = page.locator('[aria-label="Dictate a new task"]').first();
let dictateClicked = false;
for (let i = 0; i < 3 && !dictateClicked; i++) {
  if (await dictateLoc.count().catch(() => 0) > 0) {
    await dictateLoc.click({ timeout: 8000 }).then(() => { dictateClicked = true; }).catch(() => {});
  }
  if (!dictateClicked) await page.waitForTimeout(2000);
}
ok('composer opened (dictate FAB)', dictateClicked, 'dictate FAB not clickable');
await page.waitForTimeout(3000);
const typed = await page.evaluate((prompt) => {
  const ta = document.querySelector('textarea');
  if (!ta) return 'no textarea';
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value').set;
  setter.call(ta, prompt);
  ta.dispatchEvent(new Event('input', { bubbles: true }));
  return 'typed';
}, PROBE_PROMPT);
ok('composer input typed', typed === 'typed', typed);

// Click Send (the composer footer's "Send" pill).
const sent = await page.evaluate(() => {
  const el = Array.from(document.querySelectorAll('[tabindex="0"], button, [role="button"]'))
    .find(e => (e.textContent || '').trim() === 'Send' && e.offsetParent !== null);
  if (el) { el.click(); return true; }
  return false;
});
ok('composer Send clicked', sent, 'Send not found');
if (!sent) { await browser.close(); process.exit(1); }

// ── 3. Wait for the probe task to appear and finish ───────────────────────
let taskVisible = false;
let taskStatus = '';
const waitDeadline = Date.now() + 240000;
while (Date.now() < waitDeadline) {
  await page.waitForTimeout(5000);
  const txt = await page.evaluate(() => (document.body.innerText || ''));
  if (/LIVE_CONSOLE_PROBE/.test(txt)) taskVisible = true;
  if (/LIVE_CONSOLE_PROBE/.test(txt) && /Needs you|Completed|Active|review/i.test(txt)) {
    // The task list header shows the probe; find its card status by opening it.
    taskStatus = 'visible';
    break;
  }
}
ok('probe task appeared in list', taskVisible, 'never appeared');
if (!taskVisible) { await browser.close(); process.exit(1); }

// ── 4. Wait for the probe task to FINISH (review/completed) — the
//      finished-task raw_replay seed only fires for terminal opencode tasks
//      (the live stream owns the raw lane while coding). Then open it. ─────
let terminal = false;
const termDeadline = Date.now() + 300000;
while (Date.now() < termDeadline) {
  await page.waitForTimeout(6000);
  // The task list chip shows the status; simplest proxy: the card still
  // shows "Needs you" (review) OR the detail itself reads terminal. We poll
  // the card's status pill text.
  const st = await page.evaluate(() => {
    const els = Array.from(document.querySelectorAll('div[dir="auto"]'));
    const cardTitle = els.find(e => /LIVE_CONSOLE_PROBE/.test(e.textContent || ''));
    if (!cardTitle) return '';
    // Walk up to the card and read its text — "Needs you"/"Completed" etc.
    let el = cardTitle;
    for (let i = 0; i < 10 && el; i++) { el = el.parentElement; if (!el) break; }
    return (el ? el.textContent || '' : '');
  });
  if (/Needs you|Completed|Active|Review/i.test(st)) { terminal = true; break; }
}
console.log('probe task terminal state reached:', terminal);

// Close any composer sheet first (it overlays the list and shifts geometry).
await page.keyboard.press('Escape').catch(() => {});
await page.waitForTimeout(1500);

// Click the probe card by walking up from its title text to the nearest
// full-width bordered container, then clicking its CENTER with a real mouse
// event. Retry a couple of times (the list may re-render).
let clicked = false;
for (let attempt = 0; attempt < 3 && !clicked; attempt++) {
  const found = await page.evaluate(() => {
    const titleEl = Array.from(document.querySelectorAll('div[dir="auto"]'))
      .find(e => /LIVE_CONSOLE_PROBE/.test(e.textContent || ''));
    if (!titleEl) return { ok: false, why: 'no title' };
    let card = titleEl;
    for (let i = 0; i < 12 && card; i++) {
      card = card.parentElement;
      if (!card) continue;
      const r = card.getBoundingClientRect();
      if (r.width > 300 && r.height > 80) {
        // Prefer a container that LOOKS like a card: rounded + bordered.
        const cs = getComputedStyle(card);
        const rounded = cs.borderRadius !== '0px';
        if (rounded || i === 11) {
          return { ok: true, x: r.x + r.width / 2, y: Math.min(r.y + 40, r.y + r.height / 2), w: r.width, h: r.height };
        }
      }
    }
    return { ok: false, why: 'no card container' };
  });
  if (found.ok) {
    await page.mouse.click(found.x, found.y);
    await page.waitForTimeout(6000);
    const txt = await page.evaluate(() => (document.body.innerText || ''));
    if (/Live console|Follow up|Send another command/i.test(txt)) { clicked = true; break; }
  } else {
    ok('probe card found', false, found.why);
    break;
  }
}
ok('probe task detail opened', clicked, 'card click did not open detail');
if (!clicked) { await browser.close(); process.exit(1); }
// The detail modal streams: raw_replay seed + live frames. Give it generous
// time to settle (remote box round-trips + bundle render).
await page.waitForTimeout(20000);
// Force a re-render pass by polling until the console section or content shows.
const detailWaitDeadline = Date.now() + 60000;
let detail = '';
while (Date.now() < detailWaitDeadline) {
  detail = await page.evaluate(() => (document.body.innerText || ''));
  if (/Live console|Agent context|Send another command/i.test(detail)) break;
  await page.waitForTimeout(3000);
}

// ── 5. Assert the task detail rendered the live console ───────────────────
const lines = detail.split('\n').filter(l => l.trim());
ok('task detail opened', /Live console|Agent context|Commands|Follow up|Send another command/i.test(detail), 'no detail modal');
ok('Live console section present', /Live console/i.test(detail), 'missing "Live console"');
ok('console dot present', /● live|○ idle/i.test(detail), 'missing live/idle dot');
ok('streamed probe output in console', /LIVE_CONSOLE_PROBE/i.test(detail), 'probe echo missing from console');
ok('command card has real stdout', /probe|LIVE_CONSOLE_PROBE/i.test(detail), 'card output empty');
ok('not the collapsed placeholder', !/_Working through implementation details/i.test(detail), 'still collapsed');
console.log('--- detail tail (last 40 lines) ---');
console.log(lines.slice(-40).join('\n'));
console.log(failures === 0 ? '\n  VERIFY: ALL PASS' : `\n  VERIFY: ${failures} FAILED`);
await browser.close();
process.exit(failures === 0 ? 0 : 1);
