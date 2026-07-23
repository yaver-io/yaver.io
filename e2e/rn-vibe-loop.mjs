// rn-vibe-loop.mjs — closed-loop BROWSER VIBING test for an RN/Expo project.
//
// This is the loop a user actually performs: run the app in the browser lane,
// change a colour, and see it change. Static-export tests prove a project
// renders; only this proves the *vibing* loop — edit → rebuild → repaint —
// works end to end.
//
// It drives a live `expo start --web` (not an export, because an export cannot
// hot-reload), reads the painted background colour from a real Chromium,
// patches the theme on disk, waits for the change to reach the screen, and
// asserts the pixel actually changed.
//
// SAFETY: the edited file is restored in a `finally`, and the script re-reads
// the file at exit to prove the restore landed. It never commits, never stages,
// and refuses to run if the target file is already dirty in git — otherwise a
// crash mid-run could leave someone's working tree modified and look like their
// own edit.

import { chromium } from "playwright";
import { spawn, execSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";

const projectDir = process.argv[2];
const themeFile = process.argv[3];
const colorKey = process.argv[4] || "background";
const newColor = process.argv[5] || "#4B0082"; // indigo — nothing like sfmg's greens
const label = process.argv[6] || path.basename(projectDir);

if (!projectDir || !themeFile) {
  console.error("usage: node rn-vibe-loop.mjs <projectDir> <themeFileRelPath> [colorKey] [newColor] [label]");
  process.exit(2);
}

const abs = path.join(projectDir, themeFile);
const PORT = Number(process.env.VIBE_PORT || 19111);
const READY_TIMEOUT_MS = Number(process.env.VIBE_READY_MS || 240000);
const REPAINT_TIMEOUT_MS = Number(process.env.VIBE_REPAINT_MS || 120000);

// ── refuse to touch a dirty file ─────────────────────────────────────────────
try {
  const dirty = execSync(`git -C ${JSON.stringify(projectDir)} status --porcelain -- ${JSON.stringify(themeFile)}`, {
    encoding: "utf8",
  }).trim();
  if (dirty) {
    console.error(`REFUSING: ${themeFile} already has uncommitted changes:\n  ${dirty}\n` +
      `This test rewrites that file and restores it; running against a dirty file risks losing your edit.`);
    process.exit(2);
  }
} catch (e) {
  if (e.status === 2) throw e;
  console.error(`WARN: could not check git status for ${projectDir} (${e.message}) — continuing`);
}

const original = fs.readFileSync(abs, "utf8");
const beforeMatch = original.match(new RegExp(`${colorKey}:\\s*['"]([^'"]+)['"]`));
if (!beforeMatch) {
  console.error(`could not find \`${colorKey}: '<colour>'\` in ${themeFile}`);
  process.exit(2);
}
const originalColor = beforeMatch[1];

const hexToRgb = (h) => {
  const v = h.replace("#", "");
  const n = parseInt(v.length === 3 ? v.split("").map((c) => c + c).join("") : v, 16);
  return `rgb(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255})`;
};

let devProc = null;
let browser = null;
let restored = false;

const restore = () => {
  if (restored) return;
  fs.writeFileSync(abs, original, "utf8");
  restored = true;
};
// Restore on every abnormal exit too, not just the happy path.
for (const sig of ["SIGINT", "SIGTERM", "uncaughtException"]) {
  process.on(sig, () => { restore(); if (devProc) devProc.kill("SIGKILL"); process.exit(1); });
}

// Collect EVERY painted background colour on the page, not just the largest.
//
// Sampling only the dominant element was wrong and cost a run: sfmg renders the
// web preview inside a phone-frame wrapper (src/web/mobileFrame.web.ts) that
// hardcodes the same hex as the theme. The frame is bigger than the app, so
// "dominant" reported the frame and a real theme change inside the app looked
// like no change at all. A vibe has landed if the new colour appears ANYWHERE.
const sampleBg = (page) => page.evaluate(() => {
  const all = new Set();
  const areas = new Map();
  for (const el of document.querySelectorAll("*")) {
    const bg = getComputedStyle(el).backgroundColor;
    if (!bg || bg === "rgba(0, 0, 0, 0)" || bg === "transparent") continue;
    all.add(bg);
    const r = el.getBoundingClientRect();
    areas.set(bg, (areas.get(bg) || 0) + r.width * r.height);
  }
  let best = null, area = 0;
  for (const [bg, a] of areas) if (a > area) { best = bg; area = a; }
  return {
    dominant: best,
    bodyBg: getComputedStyle(document.body).backgroundColor,
    all: [...all],
  };
});

try {
  console.log(`\n=== ${label} — browser vibing loop ===`);
  console.log(`project : ${projectDir}`);
  console.log(`theme   : ${themeFile}  (${colorKey}: ${originalColor} -> ${newColor})`);

  devProc = spawn("npx", ["expo", "start", "--web", "--port", String(PORT), "--host", "localhost"], {
    cwd: projectDir,
    // NOT CI=1. Expo/Metro treat CI as "non-interactive", which disables the
    // file watcher — so the vibe edit never triggers a rebuild and the loop
    // reports "colour never reached the screen" for a lane that works fine.
    // Cost one full run to find; leave this alone.
    env: { ...process.env, BROWSER: "none", EXPO_NO_TELEMETRY: "1" },
    stdio: ["ignore", "pipe", "pipe"],
  });
  devProc.stdout.on("data", (d) => process.env.VIBE_VERBOSE && process.stdout.write(`[expo] ${d}`));
  devProc.stderr.on("data", (d) => process.env.VIBE_VERBOSE && process.stdout.write(`[expo!] ${d}`));

  browser = await chromium.launch();
  const page = await browser.newPage({ viewport: { width: 430, height: 932 } });
  const url = `http://localhost:${PORT}/`;

  // Wait for the dev server to compile and the app to actually mount. First
  // web compile is genuinely slow; this is why the phone's 60s budget was wrong.
  const deadline = Date.now() + READY_TIMEOUT_MS;
  let mounted = false;
  while (Date.now() < deadline) {
    try {
      await page.goto(url, { waitUntil: "domcontentloaded", timeout: 15000 });
      await page.waitForFunction(() => {
        const m = document.getElementById("root");
        return m && m.children.length > 0;
      }, { timeout: 10000 });
      mounted = true;
      break;
    } catch { await new Promise((r) => setTimeout(r, 3000)); }
  }
  if (!mounted) throw new Error(`app never mounted at ${url} within ${READY_TIMEOUT_MS}ms`);

  await page.waitForTimeout(1500); // let first paint settle
  const before = await sampleBg(page);
  console.log(`\nbefore  dominant=${before.dominant}  body=${before.bodyBg}`);

  // ── THE VIBE: change the colour on disk ────────────────────────────────────
  const patched = original.replace(
    new RegExp(`(${colorKey}:\\s*)['"][^'"]+['"]`),
    `$1'${newColor}'`,
  );
  if (patched === original) throw new Error("patch produced no change — regex did not match");
  fs.writeFileSync(abs, patched, "utf8");
  console.log(`vibe    wrote ${colorKey}: '${newColor}' to ${themeFile}`);

  const wantRgb = hexToRgb(newColor);
  const hit = (s) => !!s && Array.isArray(s.all) && s.all.includes(wantRgb);

  // Stage 1 — Fast Refresh. Does the edit reach the screen with no reload?
  // This is what a user expects "vibing" to mean.
  let after = null;
  const hmrDeadline = Date.now() + Math.min(45000, REPAINT_TIMEOUT_MS);
  while (Date.now() < hmrDeadline) {
    await page.waitForTimeout(2000);
    after = await sampleBg(page);
    if (hit(after)) break;
  }
  const viaHmr = hit(after);
  console.log(`after/hmr     present=${viaHmr}  dominant=${after?.dominant}  (want ${wantRgb} anywhere)  -> ${viaHmr ? "APPLIED" : "not applied"}`);

  // Stage 2 — explicit reload. Separates "the edit never compiled" from
  // "the edit compiled but Fast Refresh did not propagate it". Those have
  // completely different fixes, and the product HAS a reload path
  // (apps.tsx SSE auto-reload + the Reload button), so knowing which one
  // this is decides whether the lane is actually broken.
  let viaReload = false;
  if (!viaHmr) {
    await page.reload({ waitUntil: "domcontentloaded" });
    await page.waitForFunction(() => {
      const m = document.getElementById("root");
      return m && m.children.length > 0;
    }, { timeout: 60000 }).catch(() => {});
    const reloadDeadline = Date.now() + 30000;
    while (Date.now() < reloadDeadline) {
      await page.waitForTimeout(2000);
      after = await sampleBg(page);
      if (hit(after)) break;
    }
    viaReload = hit(after);
    console.log(`after/reload  present=${viaReload}  dominant=${after?.dominant}  -> ${viaReload ? "APPLIED" : "not applied"}`);
  }

  const shot = path.join(process.env.SCRATCH || "/tmp", `${label}-vibed.png`);
  await page.screenshot({ path: shot });

  const changed = viaHmr || viaReload;
  console.log("");
  console.log(`  ${mounted ? "PASS" : "FAIL"}  app renders in the browser lane`);
  console.log(`  ${changed ? "PASS" : "FAIL"}  colour vibe reached the screen (${originalColor} -> ${newColor})`);
  console.log(`         via: ${viaHmr ? "Fast Refresh (no reload needed)" : viaReload ? "RELOAD ONLY — Fast Refresh did not propagate the theme edit" : "neither"}`);
  console.log(`\nscreenshot: ${shot}`);
  process.exitCode = changed ? 0 : 1;
} catch (err) {
  console.error(`\nERROR: ${err.message}`);
  process.exitCode = 1;
} finally {
  restore();
  if (browser) await browser.close().catch(() => {});
  if (devProc) { devProc.kill("SIGTERM"); setTimeout(() => devProc.kill("SIGKILL"), 3000).unref?.(); }
  // Prove the restore landed — a test that silently leaves the tree modified is
  // worse than no test.
  const now = fs.readFileSync(abs, "utf8");
  console.log(now === original
    ? `restore: OK — ${themeFile} is byte-identical to its original`
    : `restore: FAILED — ${themeFile} DIFFERS FROM ORIGINAL, restore it by hand!`);
  if (now !== original) process.exitCode = 2;
}
