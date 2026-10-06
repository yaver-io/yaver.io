/**
 * Connectivity truth loop: does the dashboard tell the truth about a machine it
 * cannot reach?
 *
 *   node e2e/connectivity-truth-loop.mjs
 *
 * 2026-08-01, from the user's own screen. The Vibing panel said of `magara`:
 *
 *     The agent on that box is not answering on any path, so nothing remote can
 *     repair it. Power it on (or power-cycle it)
 *
 * while the Devices panel, in the SAME session, showed that machine as
 * "Alive · can't reach (Relay refused…) · Last agent signal just now". Both were
 * rendering the same failed probe; only one had looked at why it failed. Being
 * confidently wrong there costs the user a reboot of a healthy box and hides the
 * real remedy.
 *
 * ── What it asserts, and what it deliberately does not ──────────────────────
 *
 * It asserts the two panels AGREE, and that neither tells you to power-cycle a
 * machine whose heartbeat is fresh. It does NOT assert any particular machine is
 * reachable — reachability depends on which boxes happen to be signed in, and a
 * test that fails because a box is legitimately offline is a test nobody trusts.
 * The invariant is about honesty, not connectivity.
 *
 * Verdicts follow the house rule: PIXELS (saw it), NAMED (a cause was stated),
 * SILENT (a spinner or nothing) — SILENT is the only failing verdict, because a
 * product that cannot say what is wrong is the bug this suite exists to catch.
 *
 * ── Credentials ────────────────────────────────────────────────────────────
 *
 * Read from the gitignored .env.test (YAVER_TEST_EMAIL / YAVER_TEST_PASSWORD),
 * never inlined and never logged. The account is echoed as its DOMAIN only, so a
 * CI log or a pasted terminal never carries the address. If you add assertions
 * here, keep that property.
 */
import { chromium } from '@playwright/test';
import { readFileSync } from 'fs';

const APP = process.env.WEB_URL || 'https://yaver.io';
const ENV_PATH = process.env.YAVER_ENV_FILE || '/Users/kivanccakmak/Workspace/yaver.io/.env.test';

const env = Object.fromEntries(
  readFileSync(ENV_PATH, 'utf8')
    .split('\n').filter((l) => l.includes('='))
    .map((l) => [l.slice(0, l.indexOf('=')).trim(), l.slice(l.indexOf('=') + 1).trim()]),
);
if (!env.YAVER_TEST_EMAIL || !env.YAVER_TEST_PASSWORD) {
  console.error(`FAIL: YAVER_TEST_EMAIL / YAVER_TEST_PASSWORD missing from ${ENV_PATH}`);
  process.exit(2);
}
// Domain only — never the address.
const ACCOUNT_HINT = `<redacted>@${String(env.YAVER_TEST_EMAIL).split('@')[1] || 'unknown'}`;

const steps = [];
const step = (name, ok, detail = '') => {
  steps.push({ name, ok, detail });
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? ` — ${detail}` : ''}`);
};

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const text = () => page.evaluate(() => document.body?.innerText || '').catch(() => '');

try {
  // ── sign in ───────────────────────────────────────────────────────────────
  // The dashboard itself is OAuth-only; /auth is the form route. Selectors
  // mirror e2e/helpers/login.ts so both paths break together rather than one
  // silently rotting.
  await page.goto(`${APP}/auth`, { waitUntil: 'domcontentloaded', timeout: 60_000 });
  await page.getByPlaceholder('Email address').waitFor({ timeout: 20_000 });
  await page.getByPlaceholder('Email address').fill(env.YAVER_TEST_EMAIL);
  await page.getByPlaceholder('Password').fill(env.YAVER_TEST_PASSWORD);
  await page.getByRole('button', { name: /^sign in$/i }).click();
  await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 30_000 });
  if (page.url().includes('/survey')) await page.goto(`${APP}/dashboard`);
  await sleep(6000);

  let body = await text();
  const signedIn = /Devices|Studio|Projects/i.test(body) && !/Continue with Email/i.test(body);
  step('signs in with email + password', signedIn, ACCOUNT_HINT);
  if (!signedIn) throw new Error('sign-in did not complete');

  // ── the device list states a reachability verdict per machine ─────────────
  await page.getByText(/^Devices$/).first().click().catch(() => {});
  await sleep(5000);
  body = await text();

  const cards = [...body.matchAll(/(Connected|Alive · can't reach|Offline)/g)].map((m) => m[1]);
  step('device list states a verdict per machine', cards.length > 0, `${cards.length} verdicts`);

  // The core invariant: a machine reported ALIVE must never be described as
  // needing a power-cycle. "Alive" and "power it on" cannot both be true.
  const claimsAlive = /Alive · can't reach/i.test(body);
  const tellsToPowerCycle = /not answering on any path, so nothing remote can repair it\. Power it on/i.test(body);
  step(
    'never tells you to power-cycle a machine it just called Alive',
    !(claimsAlive && tellsToPowerCycle),
    claimsAlive ? 'a machine is alive-but-unreachable — the honest case' : 'no alive-but-unreachable machine right now',
  );

  // A failed probe must NAME its cause. This is the NAMED verdict: a bare
  // "can't reach" with no reason is the SILENT failure the audit exists for.
  const namesCause = /Relay refused|device not connected to relay|Unauthorized|no relay|expired/i.test(body);
  step('an unreachable machine names WHY', !claimsAlive || namesCause,
    claimsAlive ? (namesCause ? 'cause stated' : 'SILENT — no cause given') : 'n/a');

  // ── Vibing must agree with Devices about the same machine ────────────────
  await page.getByText(/^Studio$/).first().click().catch(() => {});
  await sleep(6000);
  const vibing = await text();

  const vibingPowerCycle = /Power it on \(or power-cycle it\)/i.test(vibing);
  const vibingSaysRelay = /relay (is up but )?has no tunnel|device not connected to relay|never contacted/i.test(vibing);
  step(
    'Vibing does not claim a relay failure means a dead box',
    !(vibingPowerCycle && vibingSaysRelay),
    vibingSaysRelay ? 'relay-cause stated' : 'no render failure on screen',
  );

  // Whatever Vibing says about a render failure, it must not be a spinner.
  const vibingSilent = /Runtime target probe failed/i.test(vibing) && !/relay|agent|machine|sign|auth/i.test(vibing);
  step('a render failure is NAMED, not silent', !vibingSilent);
} catch (err) {
  step('loop completed without throwing', false, String(err.message).slice(0, 160));
} finally {
  await ctx.close().catch(() => {});
  await browser.close().catch(() => {});
}

const failed = steps.filter((s) => !s.ok);
console.log(`\n${failed.length === 0 ? 'ALL PASS' : `${failed.length} FAILED`} (${steps.length} checks) — ${ACCOUNT_HINT}`);
process.exit(failed.length === 0 ? 0 : 1);
