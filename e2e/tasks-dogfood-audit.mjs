// Read-only live task/UI audit. Run on the selected Yaver machine:
// MOBILE_WEB_URL=<real RN-web origin> node --experimental-strip-types tasks-dogfood-audit.mjs
// This opens existing tasks and edits an unsent draft; it never dispatches work.
import { chromium, devices, expect } from '@playwright/test';
import { readFile, mkdir, open, unlink } from 'node:fs/promises';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { profileFor, viewportMatchesSurface } from '../web/lib/surfaceViewports.ts';

const appURL = process.env.MOBILE_WEB_URL;
if (!appURL) throw new Error('MOBILE_WEB_URL must name the real RN-web app.');
const config = JSON.parse(await readFile(join(homedir(), '.yaver/config.json'), 'utf8'));
const token = process.env.YAVER_TEST_TOKEN || config.auth_token;
const agentURL = process.env.YAVER_TEST_AGENT_URL || 'http://127.0.0.1:18080';
async function json(url) {
  const response = await fetch(url, { headers: { Authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(20000) });
  if (!response.ok) throw new Error(`Headless probe returned HTTP ${response.status}`);
  return response.json();
}
const auth = await json(`${config.convex_site_url}/auth/validate`);
const taskPayload = await json(`${agentURL}/tasks`);
const task = (taskPayload.tasks || []).find(t => ['ready', 'review', 'completed'].includes(t.status));
if (!task) throw new Error('The selected box needs an existing finished task for this read-only audit.');
const health = await fetch(appURL, { signal: AbortSignal.timeout(15000) });
if (!health.ok) throw new Error(`RN-web returned HTTP ${health.status}`);

// Own the same singleton lease as the browser coordinator; do not overlap it.
const lockPath = join(homedir(), '.yaver-browser-automation-session.lock');
let lease;
try { lease = await open(lockPath, 'wx', 0o600); }
catch (error) {
  if (error.code !== 'EEXIST') throw error;
  const pid = Number((await readFile(lockPath, 'utf8')).trim());
  if (!Number.isInteger(pid) || pid <= 0) throw new Error('Browser coordinator lock needs inspection.');
  try { process.kill(pid, 0); throw new Error('Another browser coordinator is active.'); }
  catch (probe) { if (probe.code !== 'ESRCH') throw probe; }
  await unlink(lockPath);
  lease = await open(lockPath, 'wx', 0o600);
}
await lease.writeFile(`${process.pid}\n`);
const artifacts = join(process.cwd(), 'test-results', `tasks-dogfood-${Date.now()}`);
await mkdir(artifacts, { recursive: true });
let browser;
let page;
try {
  browser = await chromium.launch({ headless: true, executablePath: process.env.YAVER_CHROMIUM_PATH || '/usr/local/bin/chromium' });
  const profile = profileFor('mobile');
  const context = await browser.newContext({ ...devices['iPhone 15 Pro'] });
  page = await context.newPage();
  const errors = [];
  page.on('pageerror', error => {
    errors.push(error.message);
    console.log(`RN-web exception: ${error.message.replace(/https?:\/\/\S+/g, '[url]').slice(0, 300)}`);
  });
  await page.addInitScript(({ token, user, deviceId }) => {
    localStorage.setItem('yaver_installed', '1');
    localStorage.setItem('yaver.secure.yaver_auth_token', token);
    localStorage.setItem('yaver.secure.yaver_user', JSON.stringify(user));
    localStorage.setItem(`@yaver/u/${user.id}/last_selected_device`, deviceId);
  }, { token, deviceId: config.device_id, user: {
    id: auth.user.userId, email: auth.user.email, name: auth.user.fullName,
    provider: auth.user.provider, emailVerified: auth.user.emailVerified,
    surveyCompleted: auth.user.surveyCompleted, isOwner: auth.user.isOwner,
  } });
  const taskRoute = new URL('/tasks', appURL);
  taskRoute.searchParams.set('taskId', task.id);
  taskRoute.searchParams.set('taskDeviceId', config.device_id);
  await page.goto(taskRoute.href, { waitUntil: 'domcontentloaded', timeout: 120000 });
  const viewport = page.viewportSize();
  const signals = await page.evaluate(() => ({ isMobile: /Mobile|iPhone|Android/.test(navigator.userAgent), hasTouch: navigator.maxTouchPoints > 0 }));
  expect(viewport.width).toBe(profile.width);
  const verdict = viewportMatchesSurface('mobile', { ...viewport, ...signals });
  expect(verdict.ok, verdict.reason).toBe(true);
  const composer = page.getByTestId('open-followup');
  await expect(composer).toBeVisible({ timeout: 120000 });
  const focusedInput = () => page.evaluate(() => /^(INPUT|TEXTAREA)$/.test(document.activeElement?.tagName || ''));
  expect(await focusedInput()).toBe(false);
  console.log('PASS task opens in reading mode in a genuine iPhone context');
  await composer.click();
  const input = page.getByTestId('followup-input');
  await expect(input).toBeFocused();
  await input.fill('Unsent keyboard layout audit');
  await expect(page.getByTestId('followup-send')).toBeInViewport();
  const sheet = page.getByTestId('followup-modal-overlay');
  await sheet.screenshot({ path: join(artifacts, 'follow-up.png') });
  await page.getByTestId('close-followup').click();
  expect(await focusedInput()).toBe(false);
  await composer.click();
  await expect(input).toBeFocused();
  await expect(input).toHaveValue('Unsent keyboard layout audit');
  await input.fill('');
  await page.getByTestId('close-followup').click();
  console.log('PASS explicit tap focuses follow-up; close dismisses focus; draft survives reopening');

  // Fresh navigation proves ordinary task entry never inherits input focus.
  await page.goto(new URL('/tasks', appURL).href, { waitUntil: 'domcontentloaded', timeout: 120000 });
  const allTasks = page.getByRole('button', { name: 'Show all tasks', exact: true }).first();
  // Device restoration can remount the list during initial navigation. Wait
  // for the requested filter AND its data, not just a successful early tap.
  await expect(async () => {
    await allTasks.click();
    await expect(allTasks).toHaveAttribute('aria-selected', 'true', { timeout: 1500 });
    await expect(page.getByText(task.title, { exact: true }).first()).toBeVisible({ timeout: 1500 });
  }).toPass({ timeout: 60000 });
  await page.getByText(task.title, { exact: true }).first().click({ timeout: 60000 });
  await expect(composer).toBeVisible();
  expect(await focusedInput()).toBe(false);
  console.log('PASS task-card navigation keeps the keyboard closed');

  await page.goto(new URL('/dogfood', appURL).href, { waitUntil: 'domcontentloaded', timeout: 120000 });
  await page.getByRole('button', { name: 'Open Dogfood settings' }).click({ timeout: 120000 });
  await expect(page.getByText('Dogfood Settings', { exact: true }).first()).toBeVisible();
  const lanes = page.getByRole('radiogroup', { name: 'Dogfood runtime lane' });
  await lanes.scrollIntoViewIfNeeded();
  await expect(lanes).toBeInViewport();
  await expect(page.getByRole('radio', { name: /Browser lane/ })).toBeVisible();
  await expect(page.getByRole('radio', { name: /Hermes/ })).toBeVisible();
  await expect(page.getByRole('radio', { name: /WebRTC native/ })).toBeVisible();
  // Layout visibility alone can pass during a modal's entrance animation,
  // while its pixels still show the underlying startup surface. Prove the
  // enabled lane actually receives input before recording its pixels.
  await page.getByRole('radio', { name: /Browser lane/ }).click({ trial: true });
  await lanes.screenshot({ path: join(artifacts, 'dogfood-lanes.png'), animations: 'disabled' });
  const runners = page.getByRole('button', { name: /^(Change|Set up) Runner$/ });
  await runners.scrollIntoViewIfNeeded();
  await runners.click();
  await expect(page.getByLabel('Runner choices')).toBeVisible();
  await page.getByRole('button', { name: 'Close Dogfood setting choices' }).last().click();
  await expect(page.getByLabel('Runner choices')).toHaveCount(0);
  console.log('PASS shared Dogfood runtime picker and runner sheet are visible and dismissible');
  expect(errors.length, 'RN-web emitted an unhandled JavaScript exception').toBe(0);
  console.log(`PIXELS/PASS artifacts: test-results/${artifacts.split('/').pop()}`);
  console.log('LIMIT: browser focus/layout verified; physical iOS keyboard occlusion needs native-device validation.');
} catch (error) {
  if (page) {
    await page.screenshot({ path: join(artifacts, 'failure.png') }).catch(() => {});
    console.log('Browser failure surface:', await page.locator('body').innerText().then(text => text.replace(/https?:\/\/\S+/g, '[url]').slice(0, 1600)).catch(() => 'unavailable'));
    console.log(`Failure artifact: test-results/${artifacts.split('/').pop()}/failure.png`);
  }
  throw error;
} finally {
  await browser?.close();
  await lease.close();
  await unlink(lockPath);
}
