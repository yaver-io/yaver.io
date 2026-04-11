import { defineConfig, devices } from "@playwright/test";

/**
 * Playwright config for yaver.io browser tests.
 *
 * By default we boot the Next.js dev server in `web/` and drive it via
 * chromium headless. Set `E2E_BASE_URL` to point at a deployed environment
 * (e.g. a PR preview or `https://yaver.io`) and the `webServer` block will
 * be skipped.
 */
const baseURL = process.env.E2E_BASE_URL || "http://127.0.0.1:3000";
const useLocalServer = !process.env.E2E_BASE_URL;

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
  timeout: 30_000,
  expect: { timeout: 7_000 },
  globalSetup: require.resolve("./global-setup"),
  globalTeardown: require.resolve("./global-teardown"),
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
  webServer: useLocalServer
    ? {
        command: "npm --prefix ../web run dev -- --port 3000 --hostname 127.0.0.1",
        url: "http://127.0.0.1:3000",
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        stdout: "pipe",
        stderr: "pipe",
      }
    : undefined,
});
