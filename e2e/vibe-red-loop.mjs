/**
 * Vibe closed loop — black → RED → black, on the web-UI browser lane.
 *
 *   node e2e/vibe-red-loop.mjs
 *
 * ── What this proves ───────────────────────────────────────────────────────
 *
 * The whole product in one arc: sign in, drive the real dashboard, pick the
 * project on the box that both RUNS and RENDERS (ubuntu-4gb), render its web-UI
 * preview, then change the login background to red BY VIBING and read the
 * PIXELS back. Then revert to black, as a second task, and read them again.
 *
 * The terminal signal is the rendered colour. Never a chat "completed" badge:
 * on 2026-08-02 a run reported COMPLETED with a correct diff while the change
 * was invisible (it landed in a feature-gated component), and separately a run
 * that had SUCCEEDED rendered as failed because a sidecar logged 401 retries.
 * A status string has now been wrong in both directions on this exact screen,
 * so only pixels count here.
 *
 * ── Why the browser lane, explicitly ───────────────────────────────────────
 *
 * This drives the dev-server/web-UI preview, NOT the WebRTC JPEG-DataChannel
 * lane. They fail differently and are worth separate loops; conflating them is
 * how a green run hides a broken transport.
 *
 * ── How the pixel is read ──────────────────────────────────────────────────
 *
 * The preview is a cross-origin iframe (a relay URL), so canvas sampling inside
 * it is blocked and reading CSS through the frame is not possible. Instead the
 * PREVIEW ELEMENT is screenshotted by the browser — origin-independent, and it
 * captures exactly what a human sees — then that PNG is re-injected as a
 * same-origin data: URL and sampled on a canvas. No decoder dependency, no
 * tainted canvas, and the thing measured is genuinely the rendered pixel.
 *
 * ── Credentials ────────────────────────────────────────────────────────────
 *
 * From the gitignored .env.test; the account is echoed as its DOMAIN only.
 */
import { chromium } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync } from 'fs';

const APP = process.env.WEB_URL || 'https://yaver.io';
const ENV_PATH = process.env.YAVER_ENV_FILE || new URL('../.env.test', import.meta.url).pathname;
const OUT = process.env.YAVER_OUT_DIR || '/private/tmp/claude-501/-Users-kivanccakmak-Workspace-yaver-io/1fd196a7-8136-4855-8016-90250d1a68f3/scratchpad/vibe-red';
const BOX = process.env.VIBE_BOX || 'ubuntu-4gb-hel1-1';
/** A Codex turn plus a rebuild plus a reload. Generous on purpose — a loop that
 *  times out early reports SILENT for a system that was merely slow. */
const TURN_BUDGET_MS = Number(process.env.VIBE_BUDGET_MS || 12 * 60_000);

mkdirSync(OUT, { recursive: true });
const env = Object.fromEntries(
  readFileSync(ENV_PATH, 'utf8').split('\n').filter((l) => l.includes('='))
    .map((l) => [l.slice(0, l.indexOf('=')).trim(), l.slice(l.indexOf('=') + 1).trim()]),
);
if (!env.YAVER_TEST_EMAIL || !env.YAVER_TEST_PASSWORD) {
  console.error(`FAIL: credentials missing from ${ENV_PATH}`);
  process.exit(2);
}
const ACCOUNT = `<redacted>@${String(env.YAVER_TEST_EMAIL).split('@')[1] || 'unknown'}`;

const steps = [];
const step = (name, ok, detail = '') => {
  steps.push({ name, ok, detail });
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? ` — ${detail}` : ''}`);
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * Classify a sampled pixel.
 *
 * Red must DOMINATE both other channels by a wide margin. The margin is not
 * decoration: the app paints error/danger chrome in red-ish tones, and a
 * loose threshold would let a failure banner masquerade as a successful vibe —
 * the same trap the WebRTC loop avoids by never probing for green (an H.264
 * no-signal frame is rgb(0,135,0)).
 */
function classify([r, g, b]) {
  if (r < 60 && g < 60 && b < 60) return 'black';
  if (r > 90 && r > g + 45 && r > b + 45) return 'red';
  if (g > 90 && g > r + 45 && g > b + 45) return 'green';
  return `other(${r},${g},${b})`;
}

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1600, height: 1100 } });
const page = await ctx.newPage();

/** Screenshot the preview element and sample a pixel from it. */
async function samplePreview(label) {
  // The preview iframe specifically. The earlier `iframe, [data-preview],
  // .preview-frame` union silently fell back to a full-page clip when no
  // preview existed, which is how an empty panel got sampled and reported as a
  // clean black baseline.
  const el = page.locator('iframe').first();
  let buf;
  try { buf = await el.screenshot({ timeout: 15_000 }); }
  catch { buf = await page.screenshot({ clip: { x: 300, y: 300, width: 800, height: 700 } }); }
  writeFileSync(`${OUT}/${label}.png`, buf);
  const dataUrl = `data:image/png;base64,${buf.toString('base64')}`;
  // Re-inject as a SAME-ORIGIN data: URL so the canvas is not tainted.
  return page.evaluate(async (url) => {
    const img = new Image();
    await new Promise((res, rej) => { img.onload = res; img.onerror = rej; img.src = url; });
    const c = document.createElement('canvas');
    c.width = img.width; c.height = img.height;
    const g = c.getContext('2d');
    g.drawImage(img, 0, 0);
    // Sample a GRID over the whole frame, not one row.
    //
    // A single band at 55% height ran straight through the sign-in BUTTONS,
    // whose fill is #1a1a1a — so a fully red login screen sampled as "black"
    // and the loop reported failure for a vibe that had plainly worked. The
    // background is most of the area, so a grid lets it win on its own merits
    // instead of depending on which row happens to miss the controls.
    const counts = new Map();
    for (let yy = Math.floor(img.height * 0.05); yy < img.height * 0.95; yy += 8) {
      for (let xx = Math.floor(img.width * 0.05); xx < img.width * 0.95; xx += 8) {
        const d = g.getImageData(xx, yy, 1, 1).data;
        const k = `${d[0]},${d[1]},${d[2]}`;
        counts.set(k, (counts.get(k) || 0) + 1);
      }
    }
    let best = '0,0,0', n = -1;
    for (const [k, v] of counts) if (v > n) { best = k; n = v; }
    return best.split(',').map(Number);
  }, dataUrl);
}

/** The target-card "Open" controls.
 *
 *  Deliberately NOT getByRole('button'): they are not <button> elements, so a
 *  role query returned zero and the loop reported "no render target offered"
 *  for a screen that plainly showed two. Match the visible text across anything
 *  clickable instead — the user does not care what tag it is.
 */
function openControls() {
  return page.locator('button, a, [role="button"], [class*="cursor-pointer"]')
    .filter({ hasText: /^Open$/ });
}

async function waitForColor(want, budgetMs, label) {
  const deadline = Date.now() + budgetMs;
  let last = 'unknown';
  let i = 0;
  while (Date.now() < deadline) {
    const px = await samplePreview(`${label}_${i++}`).catch(() => null);
    if (px) {
      last = classify(px);
      if (last === want) return { ok: true, color: last };
    }
    const leftMs = deadline - Date.now();
    console.log(`     … waiting for ${want}; currently ${last}; ${Math.round(leftMs / 1000)}s left`);
    await sleep(20_000);
  }
  return { ok: false, color: last };
}

let verdict = 'SILENT';
let reason = '';

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
  step('AUTH — signed in', true, ACCOUNT);

  // ── Vibing ────────────────────────────────────────────────────────────────
  await page.getByText(/^Studio$/).first().click().catch(() => {});
  await sleep(8000);
  let body = await page.evaluate(() => document.body.innerText);
  step('OPEN Studio', /Studio|RUNNER|Configure/i.test(body));

  // ── the box must both run AND render ─────────────────────────────────────
  const bothRoles = new RegExp(`${BOX}[^\\n]*runs and renders`, 'i').test(body)
    || (body.includes(BOX) && /runs and renders/i.test(body));
  step(`MACHINES — ${BOX} runs and renders`, bothRoles,
    bothRoles ? 'single-box: runner == renderer' : 'not reported as both roles');

  // ── select the project explicitly ────────────────────────────────────────
  // The composer is labelled "Ask codex to change <project>", so the WRONG
  // project is silently drivable. The first run of this loop vibed against
  // whatever happened to be selected.
  // Option VALUES are absolute paths (/root/Workspace/yaver.io/mobile) while the
  // visible LABEL is "yaver / mobile · expo". Match on the label and pick the
  // EXACT project — a value-substring match happily selects yaver-todo-rn.
  // WAIT for the catalogue to populate. The console logs "projects loaded: 36"
  // seconds after the panel paints; reading the select before that gets a short
  // list and silently selects whatever is first (a run picked "Bento / mobile"
  // this way). Acting before the data arrives is the same defect this whole
  // session has been removing from the product — it belongs out of the harness
  // too.
  const projectSelect = page.locator('select')
    .filter({ has: page.locator('option', { hasText: /yaver \/ mobile/i }) }).first();
  const listReady = await (async () => {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      if (await projectSelect.count()) return true;
      await sleep(2500);
    }
    return false;
  })();
  if (!listReady) {
    verdict = 'NAMED';
    reason = 'the project catalogue never listed "yaver / mobile" (still loading, or not on this box)';
    throw new Error(reason);
  }
  let picked = '';
  if (await projectSelect.count()) {
    const opts = await projectSelect.locator('option').evaluateAll((els) =>
      els.map((e) => ({ value: e.value, label: (e.textContent || '').trim() })));
    const match = opts.find((o) => /^yaver \/ mobile\b/i.test(o.label));
    if (match) {
      await projectSelect.selectOption(match.value).catch(() => {});
      picked = match.label;
      await sleep(6000);
    }
  }
  body = await page.evaluate(() => document.body.innerText);
  // Verify against the COMPOSER, which names the project it will actually vibe
  // ("Ask codex to change <project>"). Checking the page text alone passes on
  // any screen that merely mentions the name somewhere.
  const composerLabel = await page.getByPlaceholder(/Ask codex to change/i).first()
    .getAttribute('placeholder').catch(() => '');
  const projectOk = /yaver \/ mobile/i.test(composerLabel || '');
  step('SELECT PROJECT yaver / mobile', projectOk,
    projectOk ? `composer: "${composerLabel}"` : `composer is pointed elsewhere: "${composerLabel}" (picked "${picked}")`);
  if (!projectOk) { verdict = 'NAMED'; reason = 'could not select yaver / mobile'; throw new Error(reason); }

  // ── render the web-UI preview (browser lane, NOT WebRTC) ─────────────────
  const loadTargets = page.getByRole('button', { name: /Load Targets/i }).first();
  if (await loadTargets.count()) { await loadTargets.click().catch(() => {}); }
  // Target discovery probes the box; a fixed sleep either wastes time or fires
  // early. Poll for the first Open button instead, and NAME the timeout rather
  // than reporting "no targets offered" for something that was merely slow.
  const targetsReady = await (async () => {
    const deadline = Date.now() + 90_000;
    while (Date.now() < deadline) {
      if (await openControls().count()) return true;
      await sleep(3000);
    }
    return false;
  })();
  if (!targetsReady) {
    // Capture what WAS on screen. Diagnosing a discovery timeout from a boolean
    // is guesswork; a frame is evidence.
    await page.screenshot({ path: `${OUT}/no-targets.png`, fullPage: false }).catch(() => {});
  }
  step('LOAD TARGETS', targetsReady, targetsReady ? '' : 'no render target appeared in 90s (frame: no-targets.png)');
  if (!targetsReady) {
    verdict = 'NAMED';
    reason = 'target discovery produced nothing in 90s — the box never offered a render target';
    throw new Error(reason);
  }

  // Open the BROWSER-LANE card by name. The target list also offers "WebRTC
  // over browser"; picking the wrong card tests a different transport and would
  // report green about something this loop does not cover.
  // Some targets are collapsed behind "N show".
  const showMore = page.getByText(/^\d+ show$/).first();
  if (await showMore.count()) { await showMore.click().catch(() => {}); await sleep(2500); }

  // Walk the Open buttons and pick the one whose CARD says "Web UI in browser".
  // A CSS-ancestor filter proved brittle here; asking each button what card it
  // sits in is both simpler and states the intent directly.
  const openButtons = openControls();
  const openCount = await openButtons.count();
  let opened = '';
  for (let i = 0; i < openCount; i++) {
    const btn = openButtons.nth(i);
    const cardText = await btn.evaluate((el) => {
      let n = el;
      for (let up = 0; up < 5 && n?.parentElement; up++) n = n.parentElement;
      return (n?.innerText || '').slice(0, 200);
    }).catch(() => '');
    if (/web ui in browser/i.test(cardText)) {
      await btn.click().catch(() => {});
      opened = cardText.split('\n')[0];
      break;
    }
  }
  if (!opened) {
    const targets = await openControls().evaluateAll((els) =>
      els.map((e) => { let n = e; for (let u = 0; u < 5 && n?.parentElement; u++) n = n.parentElement;
        return (n?.innerText || '').split('\n')[0]; }));
    verdict = 'NAMED';
    reason = `the "Web UI in browser" target was not offered (saw: ${targets.join(' | ') || 'none'})`;
    throw new Error(reason);
  }
  step('OPEN "Web UI in browser" (browser lane, not WebRTC)', true, opened);

  // VERIFY the preview actually exists before claiming it rendered. The first
  // version of this loop asserted `true` unconditionally here and then sampled
  // an empty panel — a false green in the harness itself, which is the exact
  // defect class this suite exists to catch. A step must never report success
  // it did not check.
  const previewReady = await page.locator('iframe').first()
    .waitFor({ state: 'visible', timeout: 90_000 }).then(() => true).catch(() => false);
  step('RENDER web-UI preview (browser lane)', previewReady,
    previewReady ? 'iframe visible' : 'no preview iframe appeared in 90s');
  if (!previewReady) { verdict = 'NAMED'; reason = 'the browser-lane preview never rendered'; throw new Error(reason); }
  await sleep(12_000);

  // ── baseline ─────────────────────────────────────────────────────────────
  const basePx = await samplePreview('baseline');
  const baseColor = classify(basePx);
  // An all-black EMPTY panel and an all-black APP look identical to a sampler.
  // Require the frame to carry some non-uniform content, or "black" is just a
  // blank rectangle agreeing with us.
  const looksRendered = await page.locator('iframe').first().screenshot({ timeout: 15_000 })
    .then((b) => b.length > 6000).catch(() => false);
  step('BASELINE background read', baseColor !== 'unknown' && looksRendered,
    `${basePx.join(',')} → ${baseColor}${looksRendered ? '' : ' (frame looks EMPTY — not a rendered app)'}`);
  if (!looksRendered) { verdict = 'NAMED'; reason = 'preview frame is empty; nothing to assert a colour against'; throw new Error(reason); }

  // ── vibe → red ───────────────────────────────────────────────────────────
  const composer = page.getByPlaceholder(/Ask codex to change/i).first();
  await composer.fill('Change the login page background color to red. Only the login screen background.');
  await page.getByRole('button', { name: /^Send$/ }).first().click();
  await sleep(3000);
  step('CHAT → "background to red" sent', true);

  const red = await waitForColor('red', TURN_BUDGET_MS, 'red');
  step('ASSERT background == red', red.ok, red.color);
  if (!red.ok) { verdict = 'NAMED'; reason = `preview never turned red (last ${red.color})`; throw new Error(reason); }

  // ── revert as a SEPARATE task ────────────────────────────────────────────
  const newSession = page.getByRole('button', { name: /New session/i }).first();
  if (await newSession.count()) { await newSession.click().catch(() => {}); await sleep(3000); }
  step('NEW TASK (fresh session for the revert)', true);

  await composer.fill('Revert the login page background color back to black.');
  await page.getByRole('button', { name: /^Send$/ }).first().click();
  await sleep(3000);
  step('CHAT ← "revert to black" sent', true);

  const black = await waitForColor('black', TURN_BUDGET_MS, 'black');
  step('ASSERT background == black (reverted)', black.ok, black.color);
  if (!black.ok) { verdict = 'NAMED'; reason = `preview never reverted to black (last ${black.color})`; throw new Error(reason); }

  verdict = 'PIXELS';
  reason = 'black → red → black observed end to end on the browser lane';
} catch (err) {
  if (!reason) reason = String(err?.message || err).slice(0, 200);
  if (verdict === 'SILENT') step('loop completed without throwing', false, reason);
} finally {
  await ctx.close().catch(() => {});
  await browser.close().catch(() => {});
}

const failed = steps.filter((s) => !s.ok);
console.log(`\nVERDICT=${verdict} · ${reason}`);
console.log(`frames: ${OUT}`);
console.log(`${failed.length === 0 ? 'ALL PASS' : `${failed.length} FAILED`} (${steps.length} checks) — ${ACCOUNT}`);
process.exit(verdict === 'PIXELS' ? 0 : 1);
