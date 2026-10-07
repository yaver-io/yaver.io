import { defineConfig } from "@playwright/test";

// This account-independent arc does not need dashboard provisioning/global
// setup. Use only the explicitly selected RN-web server.
export default defineConfig({
  testDir: "./tests",
  testMatch: "plain-ssh-*.spec.ts",
  timeout: 120_000,
  workers: 1,
  reporter: "list",
  use: { trace: "retain-on-failure" },
});
