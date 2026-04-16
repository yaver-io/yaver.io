import { expect, test } from "@playwright/test";

test.describe("dashboard autodev workbench", () => {
  test("renders live autodev session and idea selection without raw JSON", async ({ page }) => {
    let loops = [
      {
        id: "loop-1",
        name: "onboarding-autodev",
        mode: "develop",
        status: "running",
        iterationCount: 4,
        lastSummary: "Fixing onboarding friction and keeping regression coverage green.",
        branch: "autodev/onboarding",
        commitsToday: 2,
        patchesToday: 3,
        testflightToday: 0,
        runner: "codex",
      },
    ];

    await page.addInitScript(() => {
      window.localStorage.setItem("yaver_auth_token", "mock-token");
    });

    await page.route("**/*", async (route) => {
      const url = route.request().url();
      const method = route.request().method();

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
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ relayServers: [] }),
        });
        return;
      }

      if (url.endsWith("/settings")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ settings: {} }),
        });
        return;
      }

      if (url === "http://127.0.0.1:18080/health") {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ok: true }) });
        return;
      }

      if (url === "http://127.0.0.1:18080/info") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ hostname: "test-mac", version: "0.0.0-test", workDir: "/tmp/project" }),
        });
        return;
      }

      if (url === "http://127.0.0.1:18080/agent/runners") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify([
            { id: "codex", name: "Codex", installed: true, isDefault: true },
            { id: "claude", name: "Claude", installed: true, isDefault: false },
          ]),
        });
        return;
      }

      if (url.startsWith("http://127.0.0.1:18080/tasks")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ tasks: [] }) });
        return;
      }

      if (url === "http://127.0.0.1:18080/todos/count") {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ count: 0 }) });
        return;
      }

      if (url === "http://127.0.0.1:18080/autodev/loops") {
        await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ loops }) });
        return;
      }

      if (url.startsWith("http://127.0.0.1:18080/autoideas/file")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            ok: true,
            path: "/tmp/project/ideas.md",
            items: [
              { line: 7, checked: false, title: "Add a calmer onboarding checklist for first-time users" },
              { line: 12, checked: false, title: "Ship a retry-safe magic link fallback" },
              { line: 18, checked: true, title: "Improve crash-safe draft persistence" },
            ],
          }),
        });
        return;
      }

      if (url === "http://127.0.0.1:18080/autoideas/start" && method === "POST") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true, loop_name: "project-autoideas", stream_name: "autodev:project-autoideas" }),
        });
        return;
      }

      if (url === "http://127.0.0.1:18080/autoideas/select" && method === "POST") {
        loops = [
          {
            ...loops[0],
            iterationCount: 5,
            lastSummary: "Implementing the selected ideas and validating the flow.",
          },
        ];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ ok: true, loop_name: "onboarding-autodev", stream_name: "autodev:onboarding-autodev" }),
        });
        return;
      }

      if (url === "http://127.0.0.1:18080/autodev/start" && method === "POST") {
        await route.fulfill({
          status: 202,
          contentType: "application/json",
          body: JSON.stringify({ loop_name: "manual-autodev", work_dir: "/tmp/project", hours: "8", deploy: "auto" }),
        });
        return;
      }

      if (url.startsWith("http://127.0.0.1:18080/streams/autodev%3Aonboarding-autodev")) {
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          body: [
            'data: {"type":"yaver_say","text":"Drive the onboarding flow like a paired coding session."}',
            "",
            'data: {"type":"runner_text","runner":"codex","text":"I am updating the checklist UI and wiring the new retry-safe fallback."}',
            "",
            'data: {"type":"runner_result","runner":"codex","status":"done"}',
            "",
          ].join("\n"),
        });
        return;
      }

      await route.continue();
    });

    await page.goto("/dashboard");
    await page.getByRole("complementary").getByRole("button", { name: /Test Mac/i }).click();
    await page.getByRole("button", { name: /console/i }).click();
    await page.getByRole("button", { name: /^autodev$/i }).click();
    await page.getByPlaceholder("/abs/path/to/project").fill("/tmp/project");

    await expect(page.getByTestId("autodev-live-title")).toContainText("onboarding-autodev is running");
    await expect(page.getByText("Drive the onboarding flow like a paired coding session.")).toBeVisible();
    await expect(page.getByText('"type":"yaver_say"')).toHaveCount(0);

    await page.getByTestId("autoidea-card-7").click();
    await page.getByTestId("autoidea-card-12").click();
    await page.getByTestId("implement-selected-btn").click();

    await expect(page.getByText("Started onboarding-autodev.")).toBeVisible();
    await expect(page.getByText("Implementing the selected ideas and validating the flow.").first()).toBeVisible();
  });
});
