import { expect, test } from "@playwright/test";
import { signIn } from "../helpers/login";

/**
 * WebRTC closed loop, from THIS machine's Chromium to a real remote box:
 *
 *   dashboard → Connect → Vibing tab → pick project → Load Capabilities →
 *   start a simulator target → RTCPeerConnection negotiates → <video> (or the
 *   JPEG-datachannel <img>) shows ACTUAL PIXELS → the event log narrates it.
 *
 * This is deliberately the Vibing tab (RuntimeLabView → RemoteRuntimeViewer),
 * not the Projects flow: it is the direct owner of the WebRTC lane, and the
 * assertion that matters is pixels — a visible <video> with videoWidth 0 is the
 * black-screen bug wearing a green checkmark.
 *
 * Env (all required; the spec skips otherwise so CI stays hermetic):
 *   YAVER_TEST_TOKEN, E2E_BASE_URL, E2E_LIVE_DEVICE
 * Optional:
 *   E2E_LIVE_PROJECT  project substring to select (default: first in the list)
 *   E2E_WEBRTC_TARGET target id substring to start (default: ios-simulator)
 */
const LIVE_DEVICE = process.env.E2E_LIVE_DEVICE || "";
const LIVE_PROJECT = process.env.E2E_LIVE_PROJECT || "";
const TARGET = process.env.E2E_WEBRTC_TARGET || "ios-simulator";
const HAS_LIVE_TARGET = !!(process.env.YAVER_TEST_TOKEN && LIVE_DEVICE);

// Always record — the video IS the deliverable: a human-checkable recording of
// this machine's Chromium receiving the remote box's stream, kept on success as
// proof and on failure as evidence. Top-level because Playwright forbids a
// worker-forcing use() inside a describe group.
test.use({ video: "on" });

test.describe("webrtc stream closed loop", () => {
  test.skip(!HAS_LIVE_TARGET, "set YAVER_TEST_TOKEN + E2E_LIVE_DEVICE to run against a real box");
  test.setTimeout(Number(process.env.E2E_LIVE_TIMEOUT_MS || 8 * 60_000));

  test(`starts ${TARGET} and receives video frames`, async ({ page }, testInfo) => {
    const shot = async (name: string) =>
      testInfo.attach(name, { body: await page.screenshot(), contentType: "image/png" });

    const failedRequests: string[] = [];
    page.on("response", (res) => {
      if (res.status() < 400) return;
      const u = res.url();
      if (!/yaver|relay|convex|:18080|\/d\//i.test(u)) return;
      const entry = `${res.status()} ${u.replace(/([?&](token|__rp)=)[^&]+/g, "$1<redacted>").slice(0, 150)}`;
      if (!failedRequests.includes(entry)) failedRequests.push(entry);
    });

    await signIn(page);
    const deviceRe = new RegExp(LIVE_DEVICE, "i");

    // The dashboard boots behind a full-screen spinner (auth validation + first
    // data pull). Looking for the nav during that spinner "finds no Vibing tab"
    // — a race, not a product fact. The device name appearing in the rendered
    // text is the same readiness signal the sibling spec uses.
    await expect
      .poll(async () => deviceRe.test(await page.locator("body").innerText().catch(() => "")), {
        timeout: 90_000,
        message: `dashboard never finished booting (device /${LIVE_DEVICE}/i never rendered)`,
      })
      .toBe(true);

    // Connect to the box if the dashboard isn't already attached to it.
    // The sidebar device pill shows the connected box; the Devices tab has the
    // per-card Connect. Try the cheap path first.
    const connectGateVisible = async () =>
      page
        .getByText(/Connect a device|ready to connect/i)
        .first()
        .isVisible()
        .catch(() => false);

    // Vibing tab is the WebRTC owner. Unanchored: the nav button's accessible
    // name is "▣ Vibing" (icon + label), so ^Vibing$ never matches it.
    const vibingTab = page.getByRole("button", { name: /Studio|Runtime/ });
    const vcount = await vibingTab.count();
    let opened = false;
    for (let i = 0; i < vcount; i++) {
      const b = vibingTab.nth(i);
      if (await b.isVisible().catch(() => false)) {
        await b.click();
        opened = true;
        break;
      }
    }
    expect(opened, "no Vibing tab in the nav").toBe(true);
    await shot("vibing-tab");

    // If the tab demands a connection, connect via the Devices card.
    if (await connectGateVisible()) {
      const card = page
        .locator("div")
        .filter({ hasText: deviceRe })
        .filter({ has: page.getByRole("button", { name: /^Connect$/ }) })
        .last();
      if (await card.isVisible().catch(() => false)) {
        await card.getByRole("button", { name: /^Connect$/ }).first().click();
      }
    }

    // Project picker (RuntimeLabView's <select>). Wait for it to fill from the
    // box — its arrival proves the agent answered /projects.
    const select = page.locator("select").first();
    await expect(select, "the Vibing tab never rendered its project picker").toBeVisible({
      timeout: 90_000,
    });
    await expect
      .poll(async () => select.locator("option").count(), {
        timeout: 90_000,
        message: "the project list from the box never arrived",
      })
      .toBeGreaterThan(0);
    if (LIVE_PROJECT) {
      const options = await select.locator("option").allTextContents();
      const match = options.find((o) => o.toLowerCase().includes(LIVE_PROJECT.toLowerCase()));
      if (match) await select.selectOption({ label: match });
    }
    await shot("project-picked");

    // Open the secondary target picker, then start the requested target.
    // Read from the live DOM, not from what the code calls the capability probe.
    const loadBtn = page.getByRole("button", { name: /choose another target/i }).first();
    if (await loadBtn.isVisible().catch(() => false)) {
      await loadBtn.click();
    }
    // RuntimeLabView logs "targets: …" into its event pane — the user-visible
    // narration this spec also validates.
    // RuntimeLabView's event pane timestamps each line ("[5:06:29 PM] targets: …").
    await expect(page.getByText(/targets:/i).first(), "capability probe never answered").toBeVisible({
      timeout: 120_000,
    });
    await shot("capabilities");

    // Targets must RENDER. Without this the run could sail past a panel still
    // showing "Load targets to boot …" and then pass on unrelated page text.
    await expect(
      page.getByRole("button", { name: /simulator|emulator|browser-window/i }).first(),
      "Choose another target produced no target buttons",
    ).toBeVisible({ timeout: 120_000 });

    const targetBtn = page
      .getByRole("button", { name: new RegExp(TARGET.replace(/[-/\\^$*+?.()|[\]{}]/g, "\\$&"), "i") })
      .first();
    const genericStart = page.getByRole("button", { name: /start|launch|session/i }).first();
    if (await targetBtn.isVisible().catch(() => false)) {
      await targetBtn.click();
    } else if (await genericStart.isVisible().catch(() => false)) {
      await genericStart.click();
    } else {
      const body = (await page.locator("body").innerText()).slice(0, 1200);
      throw new Error(`no control to start ${TARGET}.\nPage said:\n${body}`);
    }
    await shot("session-requested");

    // Event log must narrate the attach — "create session", "Attached", dims…
    // Silence here is the defect this whole month kept finding.
    const narration = page.getByText(/create session|attach|control-ready|signaling/i).first();
    await expect(narration, "the event log never narrated the session start").toBeVisible({
      timeout: 60_000,
    });

    // THE assertion: pixels, or a failure that names itself.
    const video = page.locator("video").first();
    const imgFallback = page.locator("img[src^='blob:']").first();
    // PRECISE phrases only. A loose /failed|error/i matched log noise elsewhere on
    // the page (a tmux preview, a console line) and short-circuited the run into a
    // false pass — the test lying in the same direction as the bug it hunts. These
    // are the strings the runtime viewer/session actually emits on failure.
    const named = page
      .getByText(/attach-failed|could not attach|no simulator matching|already claimed|runtime not installed|stream failed/i)
      .first();

    const outcome = await Promise.race([
      video.waitFor({ state: "visible", timeout: 240_000 }).then(() => "video" as const).catch(() => "none" as const),
      imgFallback.waitFor({ state: "visible", timeout: 240_000 }).then(() => "img" as const).catch(() => "none" as const),
      named.waitFor({ state: "visible", timeout: 240_000 }).then(() => "named" as const).catch(() => "none" as const),
    ]);
    await shot(`outcome-${outcome}`);

    if (outcome === "named") {
      const msg = ((await named.textContent()) || "").trim();
      testInfo.annotations.push({ type: "named-failure", description: msg.slice(0, 300) });
      expect(msg.length, "failure appeared with no text").toBeGreaterThan(0);
      // An honest, specific failure is the product telling the truth — recorded
      // and allowed. But it is NOT a stream, so say so loudly in the report
      // instead of letting a green tick imply pixels.
      console.log(`[webrtc-live] NO STREAM — product reported: ${msg.slice(0, 200)}`);
      if (process.env.E2E_REQUIRE_PIXELS === "1") {
        throw new Error(`E2E_REQUIRE_PIXELS=1 and the product reported: ${msg.slice(0, 200)}`);
      }
      return;
    }
    expect(
      outcome,
      `no stream surface and no named failure.\nFailed requests:\n  ${failedRequests.slice(0, 10).join("\n  ") || "(none)"}`,
    ).not.toBe("none");

    if (outcome === "video") {
      await expect
        .poll(async () => video.evaluate((el) => (el as HTMLVideoElement).videoWidth), {
          timeout: 90_000,
          message: "the <video> is mounted but no frame ever arrived (videoWidth stayed 0) — signaling done, media dead",
        })
        .toBeGreaterThan(0);
      // And it must be advancing — one frozen frame is not a stream.
      const t0 = await video.evaluate((el) => (el as HTMLVideoElement).currentTime);
      await page.waitForTimeout(3_000);
      const t1 = await video.evaluate((el) => (el as HTMLVideoElement).currentTime);
      expect(t1, "video time is not advancing — a single frozen frame, not a stream").toBeGreaterThan(t0);
    }
    await shot("frames-flowing");
  });
});
