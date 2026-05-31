// Codex-style update onboarder for the yaver CLI.
//
// On interactive startup (the bare `yaver` shell or `yaver wrap`/`code`),
// checks npm for a newer yaver-cli and offers a one-keystroke upgrade —
// mirroring Codex's "✨ Update available! X -> Y" prompt. Everything here
// is best-effort and fail-open: a network blip, a non-TTY, CI, or any
// error must NEVER block or break the CLI.
//
// Throttled to once per CHECK_INTERVAL so the multi-pane/tmux workflow
// (yaver runs constantly) isn't nagged or hammering the npm registry.

const https = require('https');
const fs = require('fs');
const os = require('os');
const path = require('path');
const readline = require('readline');
const { spawnSync } = require('child_process');

const STATE_DIR = path.join(os.homedir(), '.yaver');
const STATE_FILE = path.join(STATE_DIR, 'update-check.json');
const CHECK_INTERVAL_MS = 8 * 60 * 60 * 1000; // 8h
const FETCH_TIMEOUT_MS = 1500;
const RELEASES_URL = 'https://github.com/kivanccakmak/yaver.io/releases';

function readState() {
  try { return JSON.parse(fs.readFileSync(STATE_FILE, 'utf8')); } catch { return {}; }
}
function writeState(s) {
  try { fs.mkdirSync(STATE_DIR, { recursive: true }); fs.writeFileSync(STATE_FILE, JSON.stringify(s)); } catch { /* ignore */ }
}

function parseVer(v) { return String(v || '').split('.').map((n) => parseInt(n, 10) || 0); }
function isNewer(latest, current) {
  const a = parseVer(latest);
  const b = parseVer(current);
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const x = a[i] || 0;
    const y = b[i] || 0;
    if (x !== y) return x > y;
  }
  return false;
}

function fetchLatest(pkg) {
  return new Promise((resolve) => {
    let done = false;
    const finish = (v) => { if (!done) { done = true; resolve(v); } };
    try {
      const req = https.get(`https://registry.npmjs.org/${pkg}/latest`, { timeout: FETCH_TIMEOUT_MS }, (res) => {
        if (res.statusCode !== 200) { res.resume(); return finish(null); }
        let body = '';
        res.on('data', (c) => { body += c; });
        res.on('end', () => { try { finish(JSON.parse(body).version); } catch { finish(null); } });
      });
      req.on('error', () => finish(null));
      req.on('timeout', () => { req.destroy(); finish(null); });
    } catch { finish(null); }
  });
}

function ask(question) {
  return new Promise((resolve) => {
    const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
    rl.question(question, (ans) => { rl.close(); resolve((ans || '').trim()); });
  });
}

/**
 * Check for a newer yaver-cli and, if found, show the interactive
 * onboarder. Returns without doing anything when non-interactive,
 * throttled, in CI, or on any error.
 */
async function maybePromptUpdate(pkgName, currentVersion) {
  try {
    if (process.env.YAVER_NO_UPDATE_CHECK === '1') return;
    if (process.env.CI) return;
    if (!process.stdin.isTTY || !process.stdout.isTTY) return;

    const state = readState();
    const now = Date.now();
    if (state.lastCheck && now - state.lastCheck < CHECK_INTERVAL_MS) return;

    const latest = await fetchLatest(pkgName);
    writeState({ ...state, lastCheck: now });
    if (!latest || !isNewer(latest, currentVersion)) return;
    if (state.skipUntil === latest) return; // "skip until next version"

    const bold = '\x1b[1m';
    const green = '\x1b[32m';
    const dim = '\x1b[2m';
    const reset = '\x1b[0m';
    process.stdout.write(`\n  ${bold}✨ Update available!${reset} ${currentVersion} -> ${green}${latest}${reset}\n`);
    process.stdout.write(`  ${dim}Release notes: ${RELEASES_URL}${reset}\n\n`);
    process.stdout.write(`  1. Update now ${dim}(runs npm install -g ${pkgName}@latest)${reset}\n`);
    process.stdout.write(`  2. Skip\n`);
    process.stdout.write(`  3. Skip until next version\n\n`);
    const ans = await ask('  Choose [1]: ');
    const choice = ans === '' ? '1' : ans;

    if (choice === '2') return;
    if (choice === '3') { writeState({ ...readState(), skipUntil: latest }); return; }

    // Update now — same install path users bootstrap with.
    process.stdout.write(`\n  Updating ${pkgName} via npm...\n`);
    const r = spawnSync('npm', ['install', '-g', `${pkgName}@latest`], { stdio: 'inherit' });
    if (r.status === 0) {
      process.stdout.write(`\n  🎉 Updated to ${latest}. Please re-run yaver.\n`);
      process.exit(0);
    }
    process.stdout.write(`\n  Update failed — run \`npm install -g ${pkgName}@latest\` manually, or pick Skip.\n`);
  } catch {
    // Never let the update check break the CLI.
  }
}

module.exports = { maybePromptUpdate, isNewer };
