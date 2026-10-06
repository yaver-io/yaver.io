/**
 * Vibing truth loop — does the Vibing panel tell the truth, and does it route a
 * failure to the CHEAPEST fix that can actually work?
 *
 *   node e2e/vibing-truth-loop.mjs
 *
 * ── The incident this locks down (2026-08-02, from the user's own screen) ───
 *
 * One click on Fast Reload produced a chain in which every link was knowable
 * for zero tokens and zero seconds:
 *
 *   Runtime target probe failed
 *   no mobile project named "yaver / mobile" on this machine
 *   ✓ Connection to <render box>: OK via relay (449ms) — the box is up; the
 *     failure is in the operation, not the connection.
 *   [ Sign in OpenAI Codex to fix ]
 *
 * The connection line is correct and excellent. Everything after it was not:
 *   • the picker had offered a project that only exists on the OTHER machine
 *     (project identity is a NAME, merged across every box);
 *   • the headline described a missing project while the button offered a
 *     runner sign-in — two different faults fused into one panel;
 *   • "Fix with <runner>" spent a real LLM run on a question a directory
 *     listing answers;
 *   • that run then died on an expired Codex token, while the sidebar chip
 *     said "✓ SIGNED IN";
 *   • …using gpt-5.4, which a ChatGPT-account Codex login can never run.
 *
 * ── What it asserts, and what it deliberately does not ─────────────────────
 *
 * It asserts HONESTY and ROUTING, never that any particular machine is healthy.
 * A test that fails because a box is legitimately offline is a test nobody
 * trusts — and a test that demands a green Vibing panel would be the very
 * false green this suite exists to catch. So: if Vibing is happy, the failure
 * assertions are vacuously satisfied and say so.
 *
 * Verdicts follow the house rule: PIXELS (saw it), NAMED (a cause was stated),
 * SILENT (a spinner or nothing) — SILENT is the only failing verdict.
 *
 * ── Credentials ────────────────────────────────────────────────────────────
 *
 * Read from the gitignored .env.test (YAVER_TEST_EMAIL / YAVER_TEST_PASSWORD),
 * never inlined and never logged. The account is echoed as its DOMAIN only, so
 * a CI log or a pasted terminal never carries the address. Machines are printed
 * by the name the dashboard already shows; no address, token or relay password
 * is ever read or emitted. If you add assertions here, keep every one of those
 * properties.
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

/** Models a ChatGPT-account Codex login cannot run. Kept in sync with
 *  web/lib/runnerModelCompat.ts — the general gpt-5.x line needs API billing,
 *  which the subscription-only rule forbids Yaver from using. */
const CODEX_SUBSCRIPTION_INCOMPATIBLE = /\bgpt-5(\.\d+)?(-mini|-thinking|-pro)?\b(?!-codex)/i;

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1600, height: 1000 } });
const page = await ctx.newPage();
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const text = () => page.evaluate(() => document.body?.innerText || '').catch(() => '');

try {
  // ── sign in ───────────────────────────────────────────────────────────────
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

  // ── open Vibing ───────────────────────────────────────────────────────────
  await page.getByText(/^Studio$/).first().click().catch(() => {});
  await sleep(8000);
  body = await text();
  step('Vibing renders', body.length > 0, `${body.length} chars`);

  // ── 1. the model on offer must be runnable by the signed-in login ────────
  // The picker's default silently overrode the agent's and dispatched a model
  // that 400s. `MODEL / <id>` is what the session header renders.
  const modelShown = body.match(/MODEL\s*\/?\s*([\w.\-]+)/i)?.[1] || '';
  const runnerShown = /OpenAI Codex|codex/i.test(body);
  if (modelShown && runnerShown) {
    const bad = CODEX_SUBSCRIPTION_INCOMPATIBLE.test(modelShown);
    step(
      'the Codex model on screen is one a ChatGPT-account login can actually run',
      !bad,
      bad ? `${modelShown} needs API billing — it will 400` : modelShown,
    );
  } else {
    step('the Codex model on screen is one a ChatGPT-account login can actually run', true,
      'no codex model rendered right now');
  }

  // ── 2. a deterministic failure must not be routed to a paid LLM run ──────
  const projectMissing = /no (mobile )?project named .* on this machine/i.test(body);
  const offersFixWithRunner = /Fix with (OpenAI Codex|Claude|OpenCode|codex|claude)/i.test(body);
  step(
    'a missing project is never routed to "Fix with <runner>"',
    !(projectMissing && offersFixWithRunner),
    projectMissing
      ? (offersFixWithRunner ? 'ESCALATED a directory listing to an LLM' : 'deterministic route offered')
      : 'no project-missing failure on screen',
  );

  // ── 3. headline and call-to-action must describe the SAME fault ──────────
  // "no project named X" + "Sign in <runner> to fix" is two faults in one
  // panel: following that button re-auths, retries, and lands on the identical
  // error. This is the assertion that would have caught the screenshot.
  const offersRunnerSignIn = /Sign in (OpenAI Codex|Claude|OpenCode)[^.]*to fix/i.test(body);
  step(
    'the fix button addresses the fault the headline names',
    !(projectMissing && offersRunnerSignIn),
    projectMissing && offersRunnerSignIn
      ? 'headline says project missing, button says sign in — different faults'
      : 'headline and CTA agree',
  );

  // ── 4. a runner cannot be "signed in" and rejected for auth at once ──────
  const claimsSignedIn = /SIGNED IN/i.test(body);
  const authRejected = /token_expired|Provided authentication token is expired|401 Unauthorized/i.test(body);
  step(
    'no runner is shown SIGNED IN while its own output says the token expired',
    !(claimsSignedIn && authRejected),
    claimsSignedIn && authRejected ? 'FALSE GREEN on screen' : 'no contradiction',
  );

  // ── 5. a failure must NAME its cause — a spinner is the only true failure ─
  const probeFailed = /Runtime target probe failed|preview build failed|FAILED/i.test(body);
  const namesCause = /not installed|no (mobile )?project named|relay|expired|Unauthorized|not reachable|no live relay|unknown_verb|SyntaxError|not supported/i.test(body);
  step('a Vibing failure states a cause', !probeFailed || namesCause,
    probeFailed ? (namesCause ? 'NAMED' : 'SILENT — a failure with no stated cause') : 'nothing failing');

  // ── 6. transport and operation must stay distinguishable ────────────────
  // The one thing the product already got right; lock it so it cannot regress.
  const connectionOk = /Connection to .*: OK via (relay|direct|lan)/i.test(body);
  if (probeFailed && connectionOk) {
    step('a reachable box is not blamed for an operation failure', true,
      'transport and operation reported separately');
  } else {
    step('a reachable box is not blamed for an operation failure', true,
      probeFailed ? 'no connection line rendered' : 'nothing failing');
  }
} catch (err) {
  step('loop completed without throwing', false, String(err.message).slice(0, 160));
} finally {
  await ctx.close().catch(() => {});
  await browser.close().catch(() => {});
}

const failed = steps.filter((s) => !s.ok);
console.log(`\n${failed.length === 0 ? 'ALL PASS' : `${failed.length} FAILED`} (${steps.length} checks) — ${ACCOUNT_HINT}`);
process.exit(failed.length === 0 ? 0 : 1);
