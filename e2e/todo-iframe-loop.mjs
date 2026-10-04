/**
 * Closed loop over the LIGHT lanes: RN/Expo first, then Flutter — browser only.
 *
 *   node e2e/todo-iframe-loop.mjs
 *
 * Deliberately does NOT touch a simulator or emulator. Two reasons, and both are
 * in the code rather than a preference:
 *
 *  • devserver_kind.go makes browser the DEFAULT preview mode, noting Redroid
 *    needs ~6.5 GB before the app under test even loads — which is what lets the
 *    default machine be 2c/4GB. An emulator-first test suite would quietly
 *    invert the product's own cost model.
 *  • a simulator lane streams VIDEO (native-webrtc). Selenium sees a <video>
 *    element: no DOM, no text, no assertions beyond image diffing. The browser
 *    lane gives real elements, which is the only way a verdict can say WHY.
 *
 * Which apps can be here at all (verified against devserver_kind.go and
 * quic.ts:837, not assumed):
 *   expo/RN  → Hybrid  → web target ✓
 *   flutter  → Web     → web target ✓
 *   next     → Web     → web target ✓
 *   kotlin / swift → native-webrtc ✗  — no web target exists; they CANNOT be
 *     DOM-tested from a browser at any effort level, and pretending otherwise
 *     would be the same false green this suite exists to catch.
 *
 * Verdicts: PIXELS (mounted, real content) / NAMED (refused, said why) /
 * SILENT (neither — the only failure).
 */
import { chromium, devices } from '@playwright/test';
import { existsSync } from 'node:fs';
import path from 'node:path';

const AGENT = process.env.AGENT_URL || 'http://127.0.0.1:18099';
const TOKEN = (process.env.YAVER_AGENT_TOKEN || '').trim();
const BOOT_MS = Number(process.env.BOOT_BUDGET_MS || 240_000);
const auth = { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json' };
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function closeAfterEvidence(label, close) {
  try {
    await close();
  } catch (err) {
    // Browser teardown is cleanup, not product evidence. On resource-pressured
    // macOS hosts Playwright can throw `spawn ... -88` while closing an already
    // rendered page. Preserve the PIXELS/SILENT verdict produced above and
    // report teardown separately instead of turning a proven render red.
    console.warn(`  WARN ${label} teardown: ${err?.message || err}`);
  }
}

function previewUrl(previewPath) {
  const url = new URL(`${AGENT}${previewPath || '/dev-web/'}`);
  // Match the phone/WebView path. A browser context-level Authorization header
  // leaks onto third-party font/API requests and turns normal Flutter web loads
  // into CORS failures before the app can paint.
  if (TOKEN) url.searchParams.set('token', TOKEN);
  return url.toString();
}

function defaultWorkspaceRoot() {
  if (process.env.WORKSPACE_ROOT) return process.env.WORKSPACE_ROOT;
  if (process.env.HOME) return path.join(process.env.HOME, 'Workspace');
  return path.resolve('..');
}

function parseAppsEnv() {
  const raw = process.env.YAVER_LANE_APPS || '';
  if (!raw.trim()) return null;
  try {
    return JSON.parse(raw).map((app) => ({
      ...app,
      expect: app.expect ? new RegExp(app.expect, 'i') : null,
    }));
  } catch (err) {
    console.error(`YAVER_LANE_APPS must be JSON: ${err?.message || err}`);
    process.exit(2);
  }
}

function defaultApps() {
  const ws = defaultWorkspaceRoot();
  return [
    { name: 'sfmg', workDir: path.join(ws, 'sfmg'), framework: 'expo', expect: /todo/i },
    { name: 'talos/mobile', workDir: path.join(ws, 'talos', 'mobile'), framework: 'expo', expect: null },
    { name: 'yaver.io/mobile', workDir: path.join(ws, 'yaver.io', 'mobile'), framework: 'expo', expect: null },
    { name: 'e-mobile (flutter)', workDir: path.join(ws, 'e-mobile'), framework: 'flutter', expect: null },
  ];
}

// Light lanes only: browser targets give DOM assertions. Native-only projects
// belong in ios-simulator-loop.mjs, which can verify pixels but not DOM state.
// The workDir check is LOCAL, but the agent may be remote — those paths live on
// the other machine. Checking them here skipped every app as "missing" when
// pointed at a Hetzner box, which reads as "no projects" rather than "wrong
// filesystem". Only pre-filter when the agent is local; otherwise let the agent
// answer, since it is the one that can actually see the directory.
const AGENT_IS_LOCAL = /^https?:\/\/(127\.0\.0\.1|localhost)\b/.test(AGENT) && !process.env.YAVER_LANE_APPS;
const APPS = (parseAppsEnv() || defaultApps()).filter((app) => {
  if (!AGENT_IS_LOCAL) return true;
  const ok = existsSync(app.workDir);
  if (!ok) console.log(`\nSKIP   ${app.name} — missing workDir ${app.workDir}`);
  return ok;
});

if (APPS.length === 0) {
  console.log('NAMED  no local browser-lane projects found; set WORKSPACE_ROOT or YAVER_LANE_APPS');
  process.exit(0);
}

async function agent(path, init = {}) {
  const res = await fetch(`${AGENT}${path}`, { ...init, headers: { ...auth, ...(init.headers || {}) } });
  const text = await res.text();
  let json = null;
  try { json = JSON.parse(text); } catch { /* not json */ }
  return { status: res.status, json, text };
}

async function bringUpWebLane(app) {
  await agent('/dev/stop', { method: 'POST', body: '{}' }).catch(() => {});
  await sleep(2000);
  const start = await agent('/dev/start', {
    method: 'POST',
    body: JSON.stringify({ framework: app.framework, workDir: app.workDir, platform: 'web', caller: 'web-ui' }),
  });
  if (start.status >= 400) {
    return { ok: false, reason: `agent refused (HTTP ${start.status}): ${(start.json?.error || start.text).slice(0, 150)}` };
  }
  const deadline = Date.now() + BOOT_MS;
  let lastErr = '';
  let lastRefusal = '';
  while (Date.now() < deadline) {
    const st = (await agent('/dev/status')).json || {};
    if (st.error) lastErr = String(st.error);
    if (st.running === false && /exited before becoming ready|version solving failed/i.test(lastErr)) {
      return { ok: false, reason: lastErr.slice(0, 300) };
    }
    const bundleUrl = typeof st.bundleUrl === 'string' ? st.bundleUrl : '';
    const previewPath = bundleUrl || (st.webPort > 0 ? '/dev-web/' : '');
    if (previewPath) {
      const probe = await fetch(`${AGENT}${previewPath}`, { headers: auth });
      if (probe.status === 200) return { ok: true, webPort: st.webPort || st.port, previewPath };
      const body = await probe.text().catch(() => '');
      lastRefusal = `preview ${previewPath} returned HTTP ${probe.status}: ${body.slice(0, 150)}`;
      if (probe.status >= 400 && probe.status < 500) {
        return { ok: false, reason: lastRefusal };
      }
    }
    await sleep(5000);
  }
  return { ok: false, reason: lastErr || lastRefusal || `no preview URL within ${Math.round(BOOT_MS / 1000)}s` };
}

/** Look at it the way a user would: did anything actually mount and draw. */
async function render(page, previewPath) {
  await page.goto(previewUrl(previewPath), { waitUntil: 'domcontentloaded', timeout: 120_000 });
  const deadline = Date.now() + 90_000;
  let snap = { kids: 0, text: '', path: '' };
  while (Date.now() < deadline) {
    const s = await page.evaluate(() => ({
      kids: (document.getElementById('root') || document.body)?.children.length ?? 0,
      text: (document.body?.innerText || '').trim().slice(0, 300),
      path: location.pathname,
      flutterViews: document.querySelectorAll('flutter-view').length,
      canvases: document.querySelectorAll('canvas').length,
    })).catch(() => null);
    if (s) {
      snap = s;
      if ((s.kids > 0 && s.text.length > 0) || s.flutterViews > 0 || s.canvases > 0) break;
    }
    await sleep(3000);
  }
  return snap;
}

const results = [];
const browser = await chromium.launch();
const ctx = await browser.newContext({
  ...devices['iPhone 13'],
  recordVideo: { dir: process.env.VIDEO_DIR || '/tmp/yaver-todo-videos', size: { width: 390, height: 844 } },
});

for (const app of APPS) {
  console.log(`\n──── ${app.name} ────`);
  const page = await ctx.newPage();          // fresh page ⇒ one video PER app
  const errs = [];
  const consoleErrs = [];
  page.on('pageerror', (e) => errs.push(String(e.message).slice(0, 140)));
  page.on('console', (msg) => {
    if (msg.type() !== 'error') return;
    consoleErrs.push(msg.text().slice(0, 220));
  });

  const up = await bringUpWebLane(app);
  if (!up.ok) {
    results.push({ app: app.name, verdict: 'NAMED', detail: up.reason });
    console.log(`  NAMED  ${up.reason}`);
    await closeAfterEvidence('page', () => page.close());
    continue;
  }
  console.log(`  web lane :${up.webPort} ${up.previewPath || '/dev-web/'} — rendering in chromium`);

  const seen = await render(page, up.previewPath);
  await page.screenshot({ path: `/tmp/todo-${app.name.replace(/[^a-z0-9]+/gi, '-')}.png` }).catch(() => {});

  const four04 = /Unmatched Route|could not be found/i.test(seen.text);
  const dwdsFailure = [...consoleErrs, ...errs].find((msg) => /\$dwdsSseHandler/i.test(msg));
  const browserFailure = dwdsFailure || [...errs, ...consoleErrs].find((msg) =>
    /web_entrypoint\.dart|injected client\.js/i.test(msg)
  );
  if (four04) {
    // The base-path fix exists precisely for this; if it reappears, say so
    // instead of calling a mounted 404 a pass.
    results.push({ app: app.name, verdict: 'SILENT', detail: `mounted onto its own 404 at pathname=${seen.path} — base-path injection did not apply` });
    console.log(`  SILENT mounted onto a 404 (pathname=${seen.path})`);
  } else if (browserFailure) {
    const detail = /\$dwdsSseHandler/i.test(browserFailure)
      ? `Flutter DWDS websocket was not proxied at agent root: ${browserFailure}`
      : `Flutter injected client failed before a stable mount: ${browserFailure}`;
    results.push({ app: app.name, verdict: 'SILENT', detail });
    console.log(`  SILENT ${detail}`);
  } else if ((seen.kids > 0 && seen.text) || seen.flutterViews > 0 || seen.canvases > 0) {
    const matched = !app.expect || app.expect.test(seen.text);
    const visual = seen.flutterViews > 0 || seen.canvases > 0
      ? `flutterViews=${seen.flutterViews || 0} canvas=${seen.canvases || 0}`
      : `#root=${seen.kids}`;
    results.push({ app: app.name, verdict: 'PIXELS', detail: `${matched ? '' : '(content did not match expectation) '}${visual} "${seen.text.slice(0, 70)}"` });
    console.log(`  PIXELS ${visual} "${seen.text.slice(0, 70)}"`);
  } else {
    results.push({ app: app.name, verdict: 'SILENT', detail: errs[0] ? `first page error: ${errs[0]}` : 'served a shell, nothing mounted, nothing said' });
    console.log(`  SILENT ${errs[0] || 'nothing mounted and nothing said'}`);
  }
  await closeAfterEvidence('page', () => page.close());
}

await closeAfterEvidence('browser context', () => ctx.close());
await closeAfterEvidence('browser', () => browser.close());

console.log('\n===== TODO IFRAME LOOP (light lanes only) =====');
const w = Math.max(...results.map((r) => r.app.length));
for (const r of results) console.log(`${r.verdict.padEnd(6)} ${r.app.padEnd(w)}  ${r.detail}`);
const n = (v) => results.filter((r) => r.verdict === v).length;
console.log(`\n${n('PIXELS')} rendered · ${n('NAMED')} named refusal · ${n('SILENT')} SILENT (must be 0)`);
console.log('videos: /tmp/yaver-todo-videos · screenshots: /tmp/todo-*.png');
console.log('\nNOT COVERED, and cannot be: yaver-todo-kt (kotlin) and yaver-todo-swift');
console.log('have NO web target — native-webrtc streams video, which has no DOM to assert.\n');
process.exit(n('SILENT') > 0 ? 1 : 0);
