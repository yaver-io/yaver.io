import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright config for yaver.io browser tests.
 *
 * By default we boot the Next.js dev server in `web/` and drive it via
 * chromium headless. Set `E2E_BASE_URL` to point at a deployed environment
 * (e.g. a PR preview or `https://yaver.io`) and the `webServer` block will
 * be skipped.
 */
const localPort = process.env.E2E_PORT || "3217";
const baseURL = process.env.E2E_BASE_URL || `http://127.0.0.1:${localPort}`;
const useLocalServer = !process.env.E2E_BASE_URL;
const convexURL =
  process.env.E2E_CONVEX_URL ||
  process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
  "https://perceptive-minnow-557.eu-west-1.convex.site";
const recordAll = process.env.E2E_RECORD_ALL === "1";
// Useful when driving an already-installed system Chromium on a workstation
// where Playwright's downloaded FFmpeg helper is blocked by local execution
// policy. Screenshots and traces still prove failures without masking tests.
const disableVideo = process.env.E2E_DISABLE_VIDEO === "1";
const testTimeout = Number(process.env.E2E_CELL_TIMEOUT_MS || 30_000);
const chromiumExecutable = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;

export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [
    ["list"],
    ["html", { outputFolder: "playwright-report", open: "never" }],
  ],
  timeout: testTimeout,
  expect: { timeout: 7_000 },
  globalSetup: require.resolve("./global-setup"),
  globalTeardown: require.resolve("./global-teardown"),
  use: {
    baseURL,
    trace: recordAll ? "on" : "retain-on-failure",
    screenshot: recordAll ? "on" : "only-on-failure",
    video: disableVideo ? "off" : recordAll ? "on" : "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        ...(chromiumExecutable ? { launchOptions: { executablePath: chromiumExecutable } } : {}),
      },
    },
  ],
  webServer: useLocalServer
    ? {
        command: `NEXT_PUBLIC_CONVEX_SITE_URL=${convexURL} npm --prefix ../web run dev -- --port ${localPort} --hostname 127.0.0.1`,
        url: `http://127.0.0.1:${localPort}`,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        stdout: "pipe",
        stderr: "pipe",
      }
    : undefined,
});
