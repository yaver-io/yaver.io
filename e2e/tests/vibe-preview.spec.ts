import { expect, test } from "@playwright/test";

/**
 * Smoke test for the Vibe Preview dashboard tab.
 *
 * The whole agent surface is mocked at the network layer (same shape as
 * dashboard-autodev.spec.ts) — we're not testing chromedp here, just
 * that the React tree renders, the inputs gate the Start button, and
 * the SSE+frame fetch wiring is consistent enough to flip the modal
 * into "session active" state. Real chromedp end-to-end is covered by
 * the agent-side TestVibePreview_HeadlessE2E in vibe-preview.yml.
 */
test.describe("vibe preview dashboard tab", () => {
  test("opens the tab, starts a session, and renders the live frame area", async ({ page }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem("yaver_auth_token", "mock-token");
    });

    let activeProject: string | null = null;
    let frameSeq = 0;

    await page.route("**/*", async (route) => {
      const url = route.request().url();
      const method = route.request().method();
      const parsed = new URL(url);
      const path = parsed.pathname;

      if (url.endsWith("/auth/validate")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            user: { userId: "user-1", email: "mock@yaver.test", fullName: "Mock User" },
          }),
        });
        return;
      }

      if (url.endsWith("/devices/list")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            devices: [
              {
                deviceId: "dev-1",
                name: "Test Mac",
                platform: "darwin",
                host: "127.0.0.1",
                port: 18080,
                isOnline: true,
              },
            ],
          }),
        });
        return;
      }

      if (url.endsWith("/config")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ relayServers: [] }) });
        return;
      }

      if (url.endsWith("/settings")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ settings: {} }) });
        return;
      }

      if (parsed.host === "127.0.0.1:18080" && path === "/health") {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true }) });
        return;
      }

      if (parsed.host === "127.0.0.1:18080" && path === "/info") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ hostname: "test-mac", version: "0.0.0-test", workDir: "/tmp/project" }),
        });
        return;
      }

      // Vibe-preview agent endpoints
      if (parsed.host === "127.0.0.1:18080" && path === "/vibing/preview/start" && method === "POST") {
        const body = JSON.parse(route.request().postData() || "{}");
        activeProject = body.project;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            ok: true,
            session: {
              id: "vibe-preview-test-1",
              project: activeProject,
              targetUrl: body.targetUrl,
              browserId: "vibe-preview-test-1",
              mode: "live",
              profile: { name: "live-relay-wifi", fps: 4, width: 1280, height: 720, quality: 60, maxFrameKB: 200 },
              startedAt: new Date().toISOString(),
              lastFrame: new Date().toISOString(),
              frameCount: 0,
              stableHits: 0,
              errors: 0,
            },
          }),
        });
        return;
      }

      if (parsed.host === "127.0.0.1:18080" && path === "/vibing/preview/clips") {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true, clips: [] }) });
        return;
      }

      if (parsed.host === "127.0.0.1:18080" && path === "/vibing/preview/events") {
        // SSE stream — emit one started + one frame event so the tab
        // updates state, then keep the stream open with a keepalive
        // (Playwright closes the route on test teardown).
        const lines = [
          `data: {"type":"started","project":"${activeProject}","mode":"live","fps":4,"ts":"${new Date().toISOString()}"}`,
          "",
          `data: {"type":"frame","project":"${activeProject}","seq":${++frameSeq},"hash":"deadbeef0000","size":12345,"ts":"${new Date().toISOString()}"}`,
          "",
        ];
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          body: lines.join("\n"),
        });
        return;
      }

      if (parsed.host === "127.0.0.1:18080" && path.startsWith("/vibing/preview/frames/")) {
        // Tiny 1x1 PNG so the blob shim resolves to something playable.
        const png = Buffer.from(
          "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489" +
          "0000000a49444154789c63000100000005000100ad0a1d420000000049454e44ae426082",
          "hex",
        );
        await route.fulfill({
          status: 200,
          contentType: "image/png",
          body: png,
        });
        return;
      }

      await route.continue();
    });

    await page.goto("/dashboard");

    // Vibe Preview talks to the selected agent. Connect the mocked
    // device first, then open the standalone preview tab.
    await Promise.all([
      page.waitForResponse((res) => {
        const parsed = new URL(res.url());
        return parsed.host === "127.0.0.1:18080" && parsed.pathname === "/info" && res.status() === 200;
      }),
      page.getByRole("button", { name: /^test mac$/i }).click(),
    ]);

    // Open the new tab.
    await page.getByRole("button", { name: /vibe preview/i }).first().click();

    // Inputs default to localhost:3000 — fill a project + Start.
    const projectInput = page.getByPlaceholder("project name");
    await projectInput.fill("e2e-web");
    await page.getByRole("button", { name: /start preview/i }).click();

    // Stop button replaces Start when the session is live.
    await expect(page.getByRole("button", { name: /^stop$/i })).toBeVisible({ timeout: 5_000 });

    // The Record button is visible + clickable. Don't actually trigger
    // a recording — the agent-side simctl/adb path is exercised in the
    // Go integration test, not here.
    await expect(page.getByRole("button", { name: /● record/i })).toBeVisible();

    // Sanity: the empty-state text should have flipped to "Waiting for
    // first frame…" or the frame img should be present once the SSE
    // event arrives.
    const emptyOrFrame = page.getByText(/waiting for first frame|vibe preview frame/i);
    await expect(emptyOrFrame).toBeVisible({ timeout: 5_000 });
  });
});
