#!/usr/bin/env node
/**
 * Demo Video Generator for Yaver.io Landing Page
 *
 * Generates Video 2 (Bug Fix Loop) and Video 3 (Auto Test)
 * using Playwright to record animated HTML scenes as video.
 *
 * Output: demo-feedback.mp4, demo-autotest.mp4
 *
 * Usage:
 *   node generate.mjs              # Generate both videos
 *   node generate.mjs --video2     # Video 2 only
 *   node generate.mjs --video3     # Video 3 only
 *   node generate.mjs --output-dir /path  # Custom output directory
 */

import { chromium } from 'playwright';
import { execSync } from 'child_process';
import { mkdirSync, readdirSync, readFileSync } from 'fs';
import { join, resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const SCENES_DIR = join(__dirname, 'scenes');

const args = process.argv.slice(2);
const genVideo2 = args.includes('--video2') || (!args.includes('--video3'));
const genVideo3 = args.includes('--video3') || (!args.includes('--video2'));
const outputIdx = args.indexOf('--output-dir');
const OUTPUT_DIR = outputIdx >= 0 ? resolve(args[outputIdx + 1]) : join(__dirname, 'output');

const WIDTH = 1440;
const HEIGHT = 1080;
const FPS = 25;

async function recordScene(htmlFile, outputName, durationSec) {
  const tmpDir = join(OUTPUT_DIR, '.tmp-' + Date.now());
  mkdirSync(tmpDir, { recursive: true });

  console.log(`Recording ${outputName} (${durationSec}s)...`);

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    viewport: { width: WIDTH, height: HEIGHT },
    recordVideo: { dir: tmpDir, size: { width: WIDTH, height: HEIGHT } },
    deviceScaleFactor: 1,
    colorScheme: 'dark',
  });

  const page = await context.newPage();
  const htmlPath = join(SCENES_DIR, htmlFile);
  await page.goto(`file://${htmlPath}`);

  // Wait for animation to play out + 1s buffer
  await page.waitForTimeout((durationSec + 1) * 1000);

  // Close to finalize video
  const video = page.video();
  await page.close();
  const videoPath = await video.path();
  await context.close();
  await browser.close();

  // Convert webm → mp4 with h264
  const mp4Path = join(OUTPUT_DIR, outputName);
  console.log(`Converting to mp4: ${mp4Path}`);
  execSync([
    'ffmpeg', '-y',
    '-i', videoPath,
    '-c:v', 'libx264',
    '-crf', '26',
    '-preset', 'slow',
    '-r', String(FPS),
    '-pix_fmt', 'yuv420p',
    '-t', String(durationSec),
    '-an',
    mp4Path,
  ].join(' '), { stdio: 'inherit' });

  // Cleanup tmp
  try {
    for (const f of readdirSync(tmpDir)) {
      execSync(`rm -f "${join(tmpDir, f)}"`);
    }
    execSync(`rmdir "${tmpDir}"`);
  } catch {}

  const size = (execSync(`stat -f%z "${mp4Path}" 2>/dev/null || stat -c%s "${mp4Path}"`).toString().trim());
  console.log(`  ${outputName}: ${(parseInt(size) / 1024 / 1024).toFixed(1)} MB`);
}

async function main() {
  mkdirSync(OUTPUT_DIR, { recursive: true });

  // Install browser if needed
  try {
    execSync('npx playwright install chromium --with-deps 2>/dev/null', {
      cwd: __dirname,
      stdio: 'pipe',
      timeout: 120000,
    });
  } catch {
    console.log('(Playwright browser already installed)');
  }

  if (genVideo2) {
    await recordScene('video2-feedback.html', 'demo-feedback.mp4', 75);
  }

  if (genVideo3) {
    await recordScene('video3-autotest.html', 'demo-autotest.mp4', 75);
  }

  console.log(`\nDone! Videos in: ${OUTPUT_DIR}`);
}

main().catch(err => {
  console.error('Error:', err);
  process.exit(1);
});
