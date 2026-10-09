#!/usr/bin/env node
// Headless, remote-safe overview of a React Native / Expo Router app.
//
// This is intentionally a browser-device lane, not a resized desktop page:
// Playwright's full iPhone descriptor supplies touch, mobile UA, scale factor,
// and mobile media behavior. The run never opens a headed window, waits for a
// local click, or falls back to a physical phone. Every discovered static route
// gets a screenshot plus visible text and browser failures in manifest.json.

import { createRequire } from 'node:module';
import { access, mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { constants as fsConstants } from 'node:fs';
import path from 'node:path';

const appURL = process.env.MOBILE_WEB_URL || 'http://127.0.0.1:8081';
const workDir = path.resolve(process.env.MOBILE_OVERVIEW_WORK_DIR || process.cwd());
const artifactDir = path.resolve(process.env.MOBILE_OVERVIEW_ARTIFACT_DIR || path.join(process.cwd(), 'test-results', `mobile-overview-${Date.now()}`));
let token = process.env.YAVER_AGENT_TOKEN || '';
if (!token) {
  try {
    const config = JSON.parse(await readFile(path.join(process.env.HOME || '', '.yaver', 'config.json'), 'utf8'));
    token = config.auth_token || config.authToken || '';
  } catch { /* Generic apps and signed-out boxes simply record their auth wall. */ }
}
const routeLimit = Math.max(1, Math.min(250, Number(process.env.MOBILE_OVERVIEW_ROUTE_LIMIT || 250)));

function loadPlaywright() {
  const roots = [process.env.YAVER_PLAYWRIGHT_ROOT, workDir, process.cwd()].filter(Boolean);
  const failures = [];
  for (const root of roots) {
    const fromRoot = createRequire(path.join(path.resolve(root), 'package.json'));
    for (const name of ['@playwright/test', 'playwright']) {
      try { return fromRoot(name); }
      catch (error) { failures.push(`${root}:${name}: ${error.code || error.message}`); }
    }
  }
  throw new Error(`Playwright module is unavailable; use Yaver's playwright_repair route. Tried ${failures.join('; ')}`);
}

const { chromium, devices } = loadPlaywright();

async function existsDir(dir) {
  try { return (await stat(dir)).isDirectory(); }
  catch { return false; }
}

async function walk(dir, base = dir) {
  const out = [];
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    if (entry.name.startsWith('.') || entry.name === 'node_modules') continue;
    const absolute = path.join(dir, entry.name);
    if (entry.isDirectory()) out.push(...await walk(absolute, base));
    else if (/\.(?:[cm]?[jt]sx?)$/.test(entry.name)) out.push(path.relative(base, absolute));
  }
  return out;
}

function routeFromFile(file) {
  const noExt = file.replace(/\.(?:[cm]?[jt]sx?)$/, '');
  const pieces = noExt.split(path.sep)
    .filter(piece => !/^\(.+\)$/.test(piece));
  const leaf = pieces.at(-1) || '';
  if (leaf.startsWith('_') || leaf.startsWith('+') || leaf.endsWith('.test') || leaf.endsWith('.spec')) return null;
  if (pieces.some(piece => /^\[.+\]$/.test(piece))) return { dynamic: true, file };
  if (leaf === 'index') pieces.pop();
  return { route: '/' + pieces.join('/'), file };
}

async function discoverRoutes() {
  const explicit = process.env.MOBILE_OVERVIEW_ROUTES_JSON;
  if (explicit) {
    const parsed = JSON.parse(explicit);
    if (!Array.isArray(parsed) || parsed.some(v => typeof v !== 'string')) throw new Error('MOBILE_OVERVIEW_ROUTES_JSON must be a JSON string array');
    return { routes: parsed.map(route => ({ route: route.startsWith('/') ? route : `/${route}`, file: 'explicit' })), skipped: [] };
  }
  const candidates = [path.join(workDir, 'app'), path.join(workDir, 'src', 'app')];
  let appDir = '';
  for (const candidate of candidates) {
    if (await existsDir(candidate)) { appDir = candidate; break; }
  }
  if (!appDir) return { routes: [{ route: '/', file: 'fallback' }], skipped: [] };
  const mapped = (await walk(appDir)).map(routeFromFile).filter(Boolean);
  const skipped = mapped.filter(v => v.dynamic).map(v => ({ file: v.file, reason: 'dynamic route needs concrete parameters' }));
  const seen = new Set();
  const routes = mapped.filter(v => !v.dynamic && !seen.has(v.route) && seen.add(v.route));
  routes.sort((a, b) => a.route === '/' ? -1 : b.route === '/' ? 1 : a.route.localeCompare(b.route));
  return { routes: routes.slice(0, routeLimit), skipped: [...skipped, ...routes.slice(routeLimit).map(v => ({ file: v.file, reason: `route limit ${routeLimit}` }))] };
}

function safeName(route, index) {
  const slug = route.replace(/^\/+|\/+$/g, '').replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '') || 'index';
  return `${String(index + 1).padStart(3, '0')}-${slug}.png`;
}

async function launchBrowser() {
  const candidates = [
    process.env.CHROMIUM,
    process.platform === 'darwin' ? '/opt/homebrew/bin/chromium' : '',
    process.platform === 'darwin' ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : '',
    process.platform === 'linux' ? '/usr/bin/google-chrome' : '',
    process.platform === 'linux' ? '/usr/bin/chromium' : '',
    process.platform === 'linux' ? '/usr/bin/chromium-browser' : '',
  ].filter(Boolean);
  const attempts = [];
  try {
    return { browser: await chromium.launch({ headless: true }), executable: 'playwright-managed' };
  } catch (error) {
    attempts.push(`playwright-managed: ${String(error.message || error).split('\n')[0]}`);
  }
  for (const executablePath of candidates) {
    try {
      await access(executablePath, fsConstants.X_OK);
      return { browser: await chromium.launch({ headless: true, executablePath }), executable: executablePath };
    } catch (error) {
      attempts.push(`${executablePath}: ${String(error.message || error).split('\n')[0]}`);
    }
  }
  throw new Error(`no launchable headless Chromium found; tried ${attempts.join('; ')}`);
}

await mkdir(artifactDir, { recursive: true, mode: 0o700 });
const discovery = await discoverRoutes();
const launched = await launchBrowser();
const browser = launched.browser;
const context = await browser.newContext({ ...devices['iPhone 15 Pro'] });
const probe = await context.newPage();
const viewport = probe.viewportSize();
const device = await probe.evaluate(({ width, height }) => ({
  width,
  height,
  layoutWidthBeforeNavigation: innerWidth,
  touch: navigator.maxTouchPoints > 0,
  mobileUA: /Mobile|iPhone|Android/i.test(navigator.userAgent),
  scale: devicePixelRatio,
}), viewport);
await probe.close();
if (!device.touch || !device.mobileUA || device.scale <= 1 || device.width > 500) {
  await browser.close();
  throw new Error(`Playwright did not create a real mobile context: ${JSON.stringify(device)}`);
}

const results = [];
const page = await context.newPage();
try {
  for (let index = 0; index < discovery.routes.length; index++) {
    const item = discovery.routes[index];
    const consoleErrors = [];
    const pageErrors = [];
    const failedRequests = [];
    const failedResponses = [];
    page.removeAllListeners('console');
    page.removeAllListeners('pageerror');
    page.removeAllListeners('requestfailed');
    page.removeAllListeners('response');
    page.on('console', msg => {
      if (msg.type() === 'error') consoleErrors.push({ text: msg.text().slice(0, 1000), location: msg.location() });
    });
    page.on('pageerror', err => pageErrors.push(String(err.message || err).slice(0, 1000)));
    page.on('requestfailed', req => failedRequests.push({ url: req.url().replace(/[?#].*$/, ''), error: req.failure()?.errorText || 'request failed' }));
    page.on('response', response => {
      if (response.status() >= 400) failedResponses.push({ status: response.status(), url: response.url().replace(/[?#].*$/, '') });
    });

    const target = new URL(item.route.replace(/^\//, ''), appURL.endsWith('/') ? appURL : `${appURL}/`).toString();
    let status = 0;
    let navigationError = '';
    try {
      if (index === 0) {
        const response = await page.goto(target, { waitUntil: 'domcontentloaded', timeout: 60_000 });
        status = response?.status() || 0;
        if (token) {
          await page.evaluate(value => localStorage.setItem('yaver.secure.yaver_auth_token', value), token);
          await page.reload({ waitUntil: 'domcontentloaded', timeout: 60_000 });
        }
      } else {
        // Expo Router is already mounted. Client-side history navigation keeps
        // the authenticated RN tree alive and avoids recapturing the bootstrap
        // splash (or paying a full JS-bundle startup) for every screen.
        await page.evaluate(route => {
          history.pushState({}, '', route);
          dispatchEvent(new PopStateEvent('popstate'));
        }, item.route);
      }
      // A DOMContentLoaded from Expo is only the shell. On a cold remote Mac
      // the real RN tree can take seconds to replace its splash; capturing the
      // shell 120 times is a false green. Wait for the named bootstrap state to
      // leave, bounded so a genuinely wedged app becomes evidence instead of a
      // hung overview.
      await page.waitForTimeout(750);
      const booting = await page.locator('body').innerText({ timeout: 5000 }).catch(() => '');
      if (/Starting Yaver|Loading (?:app|workspace)|Preparing (?:app|workspace)/i.test(booting)) {
        await page.waitForFunction(
          () => !/Starting Yaver|Loading (?:app|workspace)|Preparing (?:app|workspace)/i.test(document.body?.innerText || ''),
          undefined,
          { timeout: 15_000 },
        ).catch(() => {});
      }
    } catch (error) {
      navigationError = String(error.message || error).slice(0, 1200);
    }
    const screenshot = safeName(item.route, index);
    await page.screenshot({ path: path.join(artifactDir, screenshot), fullPage: false, animations: 'disabled' }).catch(error => {
      pageErrors.push(`screenshot: ${String(error.message || error).slice(0, 500)}`);
    });
    const visibleText = await page.locator('body').innerText({ timeout: 5000 }).catch(() => '');
    const authBlocked = /sign in|log in|continue with (?:email|google|apple)/i.test(visibleText);
    const expectedAuthBoundary = authBlocked && !navigationError && pageErrors.length === 0 &&
      failedResponses.every(response => response.status === 401 || response.status === 403) &&
      consoleErrors.every(error => /status of (?:401|403)/i.test(error.text));
    results.push({
      route: item.route,
      source: item.file,
      finalURL: page.url().replace(/[?#].*$/, ''),
      status,
      screenshot,
      title: await page.title().catch(() => ''),
      visibleText: visibleText.replace(/\s+\n/g, '\n').trim().slice(0, 5000),
      authBlocked,
      expectedAuthBoundary,
      navigationError,
      consoleErrors,
      pageErrors,
      failedRequests: failedRequests.slice(0, 30),
      failedResponses: failedResponses.slice(0, 30),
    });
    console.log(`[${index + 1}/${discovery.routes.length}] ${item.route} -> ${screenshot}`);
  }
} finally {
  await page.close().catch(() => {});
  await browser.close();
}

const manifest = {
  version: 1,
  createdAt: new Date().toISOString(),
  lane: 'browser',
  browserExecutable: launched.executable,
  physicalAccessRequired: false,
  project: path.basename(workDir),
  device,
  routesDiscovered: discovery.routes.length + discovery.skipped.length,
  routesRendered: results.length,
  routesSkipped: discovery.skipped,
  failures: results.filter(result => !result.expectedAuthBoundary && (result.navigationError || result.status >= 400 || result.pageErrors.length || result.consoleErrors.length || result.failedResponses.length)).length,
  results,
};
await writeFile(path.join(artifactDir, 'manifest.json'), JSON.stringify(manifest, null, 2));
console.log(`mobile overview: ${results.length} route(s), ${manifest.failures} with browser failures`);
console.log(`manifest: ${path.join(artifactDir, 'manifest.json')}`);
