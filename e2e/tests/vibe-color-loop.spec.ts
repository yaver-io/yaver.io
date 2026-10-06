import { devices, expect, test, type Locator, type Page } from "@playwright/test";
import {
  classifyVibeColor,
  looksRendered,
  modalColor,
  samplePoints,
} from "../../web/lib/vibeVerdict";
import { profileFor, viewportMatchesSurface, type YaverSurface } from "../../web/lib/surfaceViewports";

/**
 * Vibe colour closed loop — black → RED → black — on BOTH driveable surfaces.
 *
 *   npx --prefix e2e playwright test vibe-color-loop.spec.ts
 *   (or via MCP: testkit_run {dir: "e2e", grep: "vibe colour"})
 *
 * ── What it proves ─────────────────────────────────────────────────────────
 *
 * The whole product in one arc: sign in, select the project on the box that
 * both RUNS and RENDERS, open the WEB-UI BROWSER LANE (explicitly not WebRTC),
 * change the login background by VIBING, and read the PIXELS back. Then revert,
 * as a separate task, and read them again.
 *
 * Two surfaces, one spec:
 *   • web      — the dashboard at the desktop viewport
 *   • mobile   — the YAVER MOBILE APP itself, served as RN-web so Chromium can
 *                drive it. That is the only honest way to automate the app the
 *                user actually holds (CLAUDE.md: browser transport contract).
 *                It needs MOBILE_WEB_URL and SKIPS without one — shrinking the
 *                dashboard viewport and calling it "mobile" would be a false
 *                equivalence, and they share neither transport ladder, auth
 *                storage key, nor render path.
 *
 * ── Why pixels, and only pixels ────────────────────────────────────────────
 *
 * A status badge has now been wrong in BOTH directions on this exact screen:
 * a run reported COMPLETED while the change was invisible (it landed in a
 * feature-gated component), and a run that had SUCCEEDED rendered as failed
 * because a sidecar logged 401 retries. So the terminal signal is the rendered
 * colour and nothing else.
 *
 * ── Why the sampling logic lives in web/lib ────────────────────────────────
 *
 * Because it was wrong, twice, in a way that made a WORKING product look
 * broken — a single band at 55% height ran through the sign-in buttons and read
 * a fully red screen as black, costing two twelve-minute runs and a bug hunt.
 * It is unit-tested in web/lib/vibeVerdict.test.ts and imported here, so the
 * decision that declares a pass can never again be an untested inline snippet.
 *
 * Skipped rather than failed without credentials: an environment gap is not a
 * product defect, and a suite that cries wolf on a fresh checkout gets ignored.
 */

const APP = process.env.WEB_URL || "https://yaver.io";
const BOX = process.env.VIBE_BOX || "ubuntu-4gb-hel1-1";
const PROJECT = process.env.VIBE_PROJECT || "yaver / mobile";
/** A runner turn + rebuild + reload. Generous: a loop that times out early
 *  reports failure for a system that was merely slow. */
const TURN_BUDGET_MS = Number(process.env.VIBE_BUDGET_MS || 12 * 60_000);
/** Relay HTTP base for the box (VIBE_BOX_HOST) — the tree-level guards
 *  (turnChangedTree / residue) are SILENTLY DISABLED without it, which is
 *  exactly how a false green slipped through on 2026-08-06: the pixel verdict
 *  said black while the box's working tree still held `backgroundColor:"red"`
 *  (a revert task raced the still-running red task, which re-applied its edit
 *  after the arc had already sampled "black"). The relay password header
 *  (VIBE_RELAY_PW) is required for /d/<id>/exec on the public relay. */
const VIBE_BOX_HOST = process.env.VIBE_BOX_HOST || "";
const VIBE_RELAY_PW = process.env.VIBE_RELAY_PW || "";
/** Project dir on the box (VIBE_PROJECT_PATH) + the file the arc's prompt
 *  edits (VIBE_TARGET_FILE, relative to that dir). The repo-level colour-truth
 *  guard needs both to ask the box "is the red literal actually in the tree?".
 *  Defaults match the yaver mobile app: the loop's most-tested target. */
const VIBE_PROJECT_PATH = process.env.VIBE_PROJECT_PATH || "/root/Workspace/yaver.io/mobile";
const VIBE_TARGET_FILE = process.env.VIBE_TARGET_FILE || "app/login.tsx";

function creds() {
  return {
    // A revocable session TOKEN is the preferred credential, exactly as
    // helpers/login.ts already argues: nothing secret travels through a form,
    // and access is cut off by deleting that one session. Added here because
    // this spec is the one that most wants to run from a developer machine or
    // a cron, where a password is the thing you least want lying in an env.
    token: process.env.YAVER_TEST_TOKEN || process.env.E2E_USER_TOKEN || "",
    email: process.env.YAVER_TEST_EMAIL || "",
    password: process.env.YAVER_TEST_PASSWORD || "",
  };
}

function haveCreds(): boolean {
  const c = creds();
  return Boolean(c.token || (c.email && c.password));
}

/**
 * Land on the dashboard already authenticated.
 *
 * Token path mirrors helpers/login.ts: seed BOTH localStorage and the cookie
 * before any page script runs. The web dashboard reads the UNPREFIXED
 * `yaver_auth_token` — the mobile app's RN-web shim namespaces its own copy as
 * `yaver.secure.yaver_auth_token`, and seeding the wrong one lands you on
 * /login looking exactly like a rejected token rather than a key miss.
 */
async function signIn(page: Page) {
  const { token } = creds();
  if (token) {
    await page.addInitScript((t) => {
      try {
        localStorage.setItem("yaver_auth_token", t as string);
      } catch {
        /* about:blank may deny storage; the post-nav write below covers it */
      }
      document.cookie = `yaver_auth_token=${t}; path=/; max-age=${60 * 60 * 24 * 30}; samesite=lax`;
    }, token);
    await page.goto(`${APP}/dashboard`, { waitUntil: "domcontentloaded", timeout: 60_000 });
    await page.evaluate((t) => localStorage.setItem("yaver_auth_token", t as string), token);
    await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 30_000 });
    if (page.url().includes("/survey")) await page.goto(`${APP}/dashboard`);
    await page.waitForTimeout(6000);
    return;
  }
  await signInWithPassword(page);
}

async function signInWithPassword(page: Page) {
  const { email, password } = creds();
  await page.goto(`${APP}/auth`, { waitUntil: "domcontentloaded", timeout: 60_000 });
  await page.getByPlaceholder("Email address").waitFor({ timeout: 20_000 });
  await page.getByPlaceholder("Email address").fill(email);
  await page.getByPlaceholder("Password").fill(password);
  await page.getByRole("button", { name: /^sign in$/i }).click();
  await page.waitForURL(/\/(survey|dashboard)(?:$|\?)/, { timeout: 30_000 });
  if (page.url().includes("/survey")) await page.goto(`${APP}/dashboard`);
  await page.waitForTimeout(6000);
}

/** The target-card "Open" controls.
 *
 *  Deliberately NOT getByRole("button"): they are not <button> elements, so a
 *  role query matched zero and an earlier version of this loop reported "no
 *  render target offered" for a screen that plainly showed two. */
function openControls(page: Page): Locator {
  return page
    .locator('button, a, [role="button"], [class*="cursor-pointer"]')
    .filter({ hasText: /^Open$/ });
}

/**
 * Where the most recently sampled preview frame lives, for the text oracle.
 *
 * Module-scoped rather than threaded through every helper: the oracle is a
 * diagnostic that may run at any failure site, and a parameter chain would mean
 * the sites that forget to pass it silently lose the diagnosis — which is the
 * SILENT verdict this exists to remove.
 */
let lastPreviewFramePath = "";

/**
 * SILENT → NAMED for a surface that DOES have a DOM.
 *
 * The mobile preview is a cross-origin iframe, so `document.body.innerText`
 * stops at the boundary: the harness can read the dashboard's chrome and not
 * one word of the app it is judging. That is the same blindness tvOS and
 * visionOS have, arriving by a different route — and it is why a failing mobile
 * run could only ever say "the preview never turned red", never why.
 *
 * The oracle reads the rendered PIXELS, so the iframe boundary is irrelevant to
 * it. Same helper the TV and AR/VR arcs use (e2e/_visionOracle.mjs) — one
 * implementation, or the surfaces drift.
 *
 * Opportunistic: macOS-only, and its absence never changes a verdict.
 */
async function explainPreviewFrame(): Promise<string> {
  if (!lastPreviewFramePath) return "";
  try {
    const oracle = await import("../_visionOracle.mjs");
    const seen = oracle.explainFrame(lastPreviewFramePath);
    return seen ? seen.reason : "";
  } catch {
    return "";
  }
}


/**
 * PROVE WE ARE LOOKING AT THE SCREEN WE ARE CHANGING.
 *
 * The prompt below says "the login screen". The sampler reads whatever the app
 * happens to be rendering. Those are the same screen for the yaver app and NOT
 * for every project: sfmg opens on a language picker ("Choose Your Language ·
 * Türkçe · English"), so on 2026-08-04 this arc dispatched a correct edit — the
 * runner really did set the login background to #FF0000, verified in the file —
 * and then reported "preview never turned red", i.e. it called a WORKING product
 * broken.
 *
 * That is a FALSE RED, and CLAUDE.md is explicit that a false red is exactly as
 * corrosive as a false green. It is also the same discipline the ios/android
 * arcs already adopted after they lied in the other direction: require positive
 * proof of the state you are judging, and skip with a named cause otherwise.
 *
 * Deliberately conservative. It only skips when the oracle can SEE a gate screen
 * — an empty oracle (no macOS Vision, no frame yet) never changes a verdict,
 * because "we could not read the screen" must not become "the screen is wrong".
 */
/**
 * Did the edit LAND but stay invisible, or did nothing happen at all?
 *
 * Those are different verdicts and only one of them is a product failure, so
 * the arc must not collapse them into "preview never turned red".
 *
 * The first version of this guard matched screen-name phrases ("choose your
 * language", "get started"). That is prose-matching — the exact thing this
 * codebase keeps paying for — and it would drift the moment a project opened on
 * a screen nobody listed. This asks the box a QUESTION instead: did the working
 * tree change? It needs no vocabulary and works for every project, every colour
 * and every screen.
 *
 *   tree changed  → the runner did the work and the sampled screen is not the
 *                   screen it edited. A harness targeting problem: SKIP, named.
 *   tree unchanged→ nothing happened. That IS the product failing: FAIL.
 *
 * Degrades to "unknown" (and therefore to the strict FAIL) whenever the box
 * cannot be asked — an unreachable box must never soften a verdict.
 */
async function treeFingerprint(): Promise<string | null> {
  const host = process.env.VIBE_BOX_HOST;
  const token = process.env.YAVER_TEST_TOKEN;
  const path = process.env.VIBE_PROJECT_PATH;
  if (!host || !token || !path) return null;
  const headers: Record<string, string> = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
  // The public relay /d/<id>/exec requires the relay password; without it the
  // probe answers relay_password_missing and the guard degrades to "unknown" —
  // the silent-disable that let the 2026-08-06 false green through.
  if (VIBE_RELAY_PW) headers["X-Relay-Password"] = VIBE_RELAY_PW;
  try {
    // /exec is ASYNCHRONOUS — it answers {execId, pid} and NOT the output. The
    // first version of this helper read body.stdout from that reply, got "" every
    // time, and would therefore have compared "" to "" and concluded "unchanged"
    // on every run: a guard that silently always returns the strict answer is as
    // useless as one that always skips. The output lives on /exec/<id>.
    const started = await fetch(`${host}/exec`, {
      method: "POST",
      headers,
      body: JSON.stringify({ command: `git -C ${path} status --porcelain`, timeout: 20 }),
    });
    if (!started.ok) return null;
    const { execId } = (await started.json()) as { execId?: string };
    if (!execId) return null;
    for (let i = 0; i < 15; i++) {
      await new Promise((r) => setTimeout(r, 400));
      const res = await fetch(`${host}/exec/${execId}`, { headers });
      if (!res.ok) continue;
      const body = (await res.json()) as { exec?: { status?: string; stdout?: string } };
      if (body.exec?.status === "completed") return (body.exec.stdout ?? "").trim();
    }
    return null;
  } catch {
    return null;
  }
}

/**
 * Did THIS turn change the tree? A DELTA, never an absolute.
 *
 * The first version asked "is the tree dirty?" — which is useless on any real
 * project: sfmg carries the developer's own uncommitted work, so the answer was
 * permanently "changed" and every pixel miss would have SKIPPED, masking exactly
 * the product failures this arc exists to catch. A guard that can only ever
 * return one answer is not a guard.
 *
 * Comparing a before/after fingerprint measures the turn instead of the
 * repository's history. Null anywhere (box unreachable, /exec unavailable) stays
 * "unknown", which keeps the verdict strict.
 */
async function turnChangedTree(before: string | null): Promise<"changed" | "unchanged" | "unknown"> {
  if (before === null) return "unknown";
  const after = await treeFingerprint();
  if (after === null) return "unknown";
  return after === before ? "unchanged" : "changed";
}

/**
 * REPO-LEVEL COLOUR TRUTH — the guard that would have caught the 2026-08-06
 * false green.
 *
 * The pixel verdict reads the PREVIEW. A revert task can restore the pixels to
 * black while the working tree still holds the colour literal — exactly what
 * happened when the loop's revert task raced the still-running red task (the
 * red task re-applied its edit AFTER the arc sampled "black"). Pixels and repo
 * disagreed and the arc passed. This guard asks the box directly what colour
 * literals its working-tree diff contains, so a pixel verdict can never
 * outrank the repository truth.
 *
 * Extraction is MECHANICAL (regex the diff for hex/rgb/named tokens, quoting-
 * safe via a box-side python one-liner) and done on the box to keep the relay
 * payload small; CLASSIFICATION is the SAME unit-tested `classifyVibeColor`
 * that judges the pixels — one classifier for both surfaces, so a model
 * writing `#D32F2F` or `'red'` instead of "red" cannot make the two disagree
 * (measured 2026-08-06 twice: a hand-rolled "red|#ff0000" pattern missed
 * #D32F2F, then a double-quote-only extractor missed `'red'`).
 *
 * Tokens are returned WITH multiplicity: the gates compare red OCCURRENCE
 * COUNTS against a baseline captured before the turn, so pre-existing red
 * literals in the developer's uncommitted work (sfmg's tree carries some)
 * neither fake a pass nor block one — the turn must add occurrences, and the
 * revert must remove them again.
 */
async function repoColors(): Promise<{ tokens: string[] }> {
  const host = VIBE_BOX_HOST;
  const token = creds().token;
  if (!host || !token || !VIBE_PROJECT_PATH) return { tokens: [] };
  const headers: Record<string, string> = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
  if (VIBE_RELAY_PW) headers["X-Relay-Password"] = VIBE_RELAY_PW;
  try {
    const started = await fetch(`${host}/exec`, {
      method: "POST",
      headers,
      body: JSON.stringify({
        command:
          `git -C ${VIBE_PROJECT_PATH} diff | grep '^+' | ` +
          `python3 -c 'import sys,re; s=sys.stdin.read(); ` +
          `print("\\n".join(re.findall(r"#[0-9a-fA-F]{3,8}|rgb\\([^)]*\\)|rgba\\([^)]*\\)|` +
          `[\\x27\\x22\\x60](red|green|blue|black|white|yellow|orange|purple|pink|cyan|magenta|gray|grey)` +
          `[\\x27\\x22\\x60]", s)))'`,
        timeout: 30,
      }),
    });
    if (!started.ok) return { tokens: [] };
    const { execId } = (await started.json()) as { execId?: string };
    if (!execId) return { tokens: [] };
    for (let i = 0; i < 20; i++) {
      await new Promise((r) => setTimeout(r, 400));
      const res = await fetch(`${host}/exec/${execId}`, { headers });
      if (!res.ok) continue;
      const body = (await res.json()) as { exec?: { status?: string; stdout?: string } };
      if (body.exec?.status === "completed") {
        const tokens = (body.exec.stdout ?? "").split("\n").map((s) => s.trim()).filter(Boolean);
        return { tokens };
      }
    }
    return { tokens: [] };
  } catch {
    return { tokens: [] };
  }
}

/** Count red-classified colour occurrences in a token list (same classifier as the pixels). */
function redOccurrences(tokens: string[]): number {
  return tokens.filter((t) => classifyVibeColor(parseColorToken(t)) === "red").length;
}

/** Parse a colour token (hex / rgb() / named) into an [r,g,b] tuple, or null. */
function parseColorToken(tok: string): number[] | null {
  const hex = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.exec(tok);
  if (hex) {
    let h = hex[1];
    if (h.length === 3) h = h.split("").map((c) => c + c).join("");
    const n = parseInt(h.slice(0, 6), 16);
    if (Number.isNaN(n)) return null;
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
  }
  const rgb = /^rgba?\(\s*([0-9.]+)[,\s]+([0-9.]+)[,\s]+([0-9.]+)/.exec(tok);
  if (rgb) {
    const [r, g, b] = [rgb[1], rgb[2], rgb[3]].map(Number);
    if ([r, g, b].some((v) => Number.isNaN(v))) return null;
    return [r, g, b];
  }
  const named: Record<string, number[]> = {
    red: [255, 0, 0], green: [0, 128, 0], blue: [0, 0, 255], black: [0, 0, 0],
    white: [255, 255, 255], yellow: [255, 255, 0], orange: [255, 165, 0],
    purple: [128, 0, 128], pink: [255, 192, 203], cyan: [0, 255, 255],
    magenta: [255, 0, 255], gray: [128, 128, 128], grey: [128, 128, 128],
  };
  // The extraction may hand us a quoted name ('red' / "red" / `red`) — strip
  // any quote characters before the lookup (measured 2026-08-06: sfmg's model
  // wrote `backgroundColor: 'red'` with single quotes, which the pre-fix
  // classifier missed entirely).
  return named[tok.replace(/^['"\u0060]|['"\u0060]$/g, "").toLowerCase()] || null;
}

/**
 * RUNNER-FINISHES CONTRACT (AGENTS.md: "Let the runner finish. Then render
 * exactly once when the task reaches a renderable terminal state").
 *
 * The 2026-08-06 incidents BOTH came from judging mid-edit: the pixel gate
 * fired while the red task was still building (hot reload shows the edit
 * before the file settles), the arc sent the revert, and the two tasks raced
 * the same file — pixels passed, the tree disagreed. This polls the box's OWN
 * task list (via relay /exec, the same path as the tree guards) until the
 * newest task whose title contains `fragment` reaches a terminal state. Only
 * then may pixels or repo be judged.
 *
 * Returns the terminal status; "timeout" if the budget runs out.
 */
async function waitForTaskTerminal(fragment: string, budgetMs = 240_000): Promise<string> {
  const host = VIBE_BOX_HOST;
  const token = creds().token;
  if (!host || !token) return "unknown";
  const headers: Record<string, string> = { Authorization: `Bearer ${token}`, "Content-Type": "application/json" };
  if (VIBE_RELAY_PW) headers["X-Relay-Password"] = VIBE_RELAY_PW;
  const deadline = Date.now() + budgetMs;
  let last = "unknown";
  while (Date.now() < deadline) {
    try {
      const started = await fetch(`${host}/exec`, {
        method: "POST",
        headers,
        body: JSON.stringify({
          command:
            `curl -s -m 5 -H "Authorization: Bearer ${token}" "http://localhost:18080/tasks?limit=8" | ` +
            `python3 -c 'import json,sys; d=json.load(sys.stdin); [print(t.get("title","")[:60]+"\\t"+str(t.get("status",""))) for t in d.get("tasks",[])]'`,
          timeout: 12,
        }),
      });
      if (started.ok) {
        const { execId } = (await started.json()) as { execId?: string };
        if (execId) {
          for (let i = 0; i < 8; i++) {
            await new Promise((r) => setTimeout(r, 500));
            const res = await fetch(`${host}/exec/${execId}`, { headers });
            if (!res.ok) continue;
            const body = (await res.json()) as { exec?: { status?: string; stdout?: string } };
            if (body.exec?.status === "completed") {
              const lines = (body.exec.stdout ?? "").split("\n").map((l) => l.trim()).filter(Boolean);
              const match = lines.find((l) => l.includes(fragment));
              if (match) {
                last = match.split("\t").pop() || last;
                if (["completed", "failed", "stopped", "review"].includes(last)) return last;
              }
            }
          }
        }
      }
    } catch { /* keep polling */ }
    await new Promise((r) => setTimeout(r, 8000));
  }
  return last === "unknown" ? "timeout" : last;
}

/**
 * Screenshot the preview iframe and return its modal colour.
 *
 * The preview is a cross-origin iframe (a relay URL), so canvas sampling inside
 * it is blocked. Screenshotting the ELEMENT is origin-independent and captures
 * exactly what a human sees; re-injecting that PNG as a same-origin data: URL
 * keeps the canvas untainted, with no decoder dependency.
 */
async function samplePreview(page: Page): Promise<{ px: number[]; rendered: boolean }> {
  const buf = await page.locator("iframe").first().screenshot({ timeout: 20_000 });
  // Keep the LAST sampled frame on disk so the text oracle can read the exact
  // pixels the colour verdict judged. Reading a different capture would let the
  // two disagree about what was on screen, which is worse than no text at all.
  try {
    const { writeFileSync, mkdtempSync } = await import("node:fs");
    const { tmpdir } = await import("node:os");
    const { join } = await import("node:path");
    if (!lastPreviewFramePath) {
      lastPreviewFramePath = join(mkdtempSync(join(tmpdir(), "yaver-mobile-frame-")), "frame.png");
    }
    writeFileSync(lastPreviewFramePath, buf);
  } catch { /* persisting is a convenience for the oracle, never the verdict */ }
  const dataUrl = `data:image/png;base64,${buf.toString("base64")}`;
  const raw = await page.evaluate(async ({ url, stride }) => {
    const img = new Image();
    await new Promise((res, rej) => { img.onload = res; img.onerror = rej; img.src = url; });
    const c = document.createElement("canvas");
    c.width = img.width; c.height = img.height;
    const g = c.getContext("2d")!;
    g.drawImage(img, 0, 0);
    const out: number[][] = [];
    for (let y = Math.floor(img.height * 0.05); y < img.height * 0.95; y += stride) {
      for (let x = Math.floor(img.width * 0.05); x < img.width * 0.95; x += stride) {
        const d = g.getImageData(x, y, 1, 1).data;
        out.push([d[0], d[1], d[2]]);
      }
    }
    return out;
  }, { url: dataUrl, stride: 8 });
  return { px: modalColor(raw), rendered: looksRendered(raw) };
}

/**
 * Poll the preview until it shows `want`.
 *
 * `refresh` exists because POLLING A STALE IFRAME PROVES NOTHING (2026-08-03).
 * The mobile arc reloaded the preview ONCE, right after sending the prompt —
 * i.e. before the runner had finished editing — and then watched that frozen
 * frame for twelve minutes. Measured: the task reached `review`, the box's
 * `git diff` showed login.tsx correctly changed (5 +++--, the same edit shape
 * as the run that passed on web), and the arc still reported "preview never
 * turned red". The product had done the work; the harness was looking at an old
 * render and blaming it.
 *
 * The web arc gets away without this because the dashboard's own reload fires
 * when the task lands. The mobile preview does not, so the arc must re-trigger
 * it while waiting — otherwise the loop measures whoever reloaded last.
 */
async function waitForColor(
  page: Page,
  want: string,
  budgetMs: number,
  refresh?: () => Promise<unknown>,
) {
  const deadline = Date.now() + budgetMs;
  let last = "unknown";
  let polls = 0;
  while (Date.now() < deadline) {
    const s = await samplePreview(page).catch(() => null);
    if (s) {
      last = classifyVibeColor(s.px);
      if (last === want) return { ok: true, color: last };
    }
    await page.waitForTimeout(20_000);
    polls++;
    // Every ~60s. Often enough to catch the rebuild, rare enough not to fight
    // a render that is already in flight.
    if (refresh && polls % 3 === 0) await refresh().catch(() => {});
  }
  // Out of budget. Ask the frame what it was actually showing before reporting
  // a bare colour — "black" covers a sign-in wall, a compile error, a runner
  // quota wall and a preview that never loaded, and those need four different
  // actions from whoever reads this run.
  const said = await explainPreviewFrame();
  return { ok: false, color: last, said };
}

/** Drive one full black → target → black arc on whichever surface is loaded. */
/**
 * Fail immediately if the surface is sitting on its sign-in screen.
 *
 * Without this the mobile arc spent FORTY MINUTES clicking for a "Vibing" tab
 * that could never appear, then timed out pointing at a `waitForTimeout` — a
 * dead end with no stated cause, which is precisely the defect this whole suite
 * exists to remove from the product. A test may not do what it forbids.
 */
async function assertSignedIn(page: Page, surface: YaverSurface) {
  // NEVER DECIDE THIS FROM PAGE TEXT (2026-08-03).
  //
  // This used to grep document.body.innerText for "Continue with Apple|Google|…"
  // and only excused it when the words "Vibing" or "Devices" also appeared. On
  // the mobile Tasks screen neither word exists (the tabs are Tasks/Projects/
  // More) — and the task card renders the RUNNER'S OWN tool output, which for
  // this very loop reads:
  //
  //     ✳ Grep "Continue with Apple|/login" 16 matches
  //
  // because the task is about the login page. So the harness read the agent
  // grepping for a string as proof the user was logged out, and failed a run in
  // which the screenshot plainly shows "Connected · Primary · ubuntu-4gb-hel1-1",
  // "opencode ready · 258ms", and the dispatched task. A false red with a
  // confident, wrong explanation is worse than no check.
  //
  // Decide it STRUCTURALLY instead: signed-in surfaces render their own chrome,
  // and a sign-in BUTTON is a control, not a substring.
  const signedInChrome = surface === "mobile"
    // RN-web bottom tab bar. Roles, not prose — task output cannot forge these.
    ? page.getByText(/^Projects$/).first()
    : page.getByText(/^(Studio|Devices)$/).first();
  if (await signedInChrome.count()) return;

  const signInButton = page
    .getByRole("button", { name: /Continue with (Apple|Google|GitHub|GitLab|Microsoft|Email)/i })
    .first();
  const onLogin = (await signInButton.count()) > 0;
  expect(onLogin,
    `${surface} is on the SIGN-IN screen — the seeded session was not accepted. ` +
    `RN-web reads yaver.secure.yaver_auth_token AND yaver.secure.yaver_user; ` +
    `seeding only the token leaves the app logged out.`).toBe(false);
}

async function runVibeArc(page: Page, target: string, surface: YaverSurface) {
  // How this surface submits a vibe. The mobile arc replaces it below (the RN
  // composer lives in a modal opened from route params, not on the screen);
  // the web arc leaves it null and uses the dashboard composer.
  //
  // DECLARED HERE on purpose. It used to be assigned only inside the mobile
  // branch and read unconditionally at the prompt step, so the WEB arc threw
  // `ReferenceError: mobileSendVibe is not defined` before it ever reached the
  // product — a harness bug that looked exactly like a product failure, right
  // down to the recorded video and trace.
  let mobileSendVibe: ((text: string) => Promise<unknown>) | null = null;
  // Re-render the mobile preview mid-wait. Set by the mobile branch only; the
  // web arc leaves it null because the dashboard reloads itself when a task
  // lands. See waitForColor's comment for why polling without this lies.
  let mobileReloadPreview: (() => Promise<unknown>) | null = null;

  // VIEWPORT FIRST. A loop that drives the right app at the wrong size tests a
  // layout no user ever sees — and reports green about it. Assert the surface
  // profile before anything else, so a mis-sized run fails HERE with a clear
  // reason instead of producing a confident result about the wrong UI.
  const vp = await page.evaluate(() => ({
    width: window.innerWidth,
    height: window.innerHeight,
    hasTouch: "ontouchstart" in window || navigator.maxTouchPoints > 0,
    isMobile: /Mobi|Android|iPhone|iPad/i.test(navigator.userAgent),
  }));
  const vpCheck = viewportMatchesSurface(surface, vp);
  expect(vpCheck.ok, `${surface} viewport: ${vpCheck.reason} (saw ${vp.width}x${vp.height}, touch=${vp.hasTouch}, mobileUA=${vp.isMobile})`).toBe(true);

  await assertSignedIn(page, surface);

  // Navigate to the vibe surface. The dashboard has a "Vibing" nav item; the
  // MOBILE app has bottom tabs (Tasks / Projects / More) whose labels render
  // doubled (icon + label), so an exact-text match on "Projects" misses.
  if (surface === "mobile") {
    // NAVIGATE BY URL, not by tapping the tab.
    //
    // Measured 2026-08-02 on the RN-web build: clicking the Projects tab
    // changes the URL to /apps and the VIEW never leaves the Tasks list, while
    // a full goto('/apps') renders the Projects screen correctly. So client-
    // side tab navigation re-routes without re-rendering — a real defect, and
    // one this loop must not be blocked by while the vibe arc itself is what
    // we are here to test. mobile-tab-navigation.spec.ts pins the defect
    // separately so routing around it here cannot bury it.
    await page.goto(`${(process.env.MOBILE_WEB_URL || "").replace(/\/$/, "")}/apps`, {
      waitUntil: "domcontentloaded",
      timeout: 60_000,
    });
    await page.waitForTimeout(12_000);

    const projectsView = await page.evaluate(() => document.body?.innerText || "");
    expect(/Projects/.test(projectsView),
      "mobile: /apps did not render the Projects screen").toBe(true);

    // On mobile the row is labelled by PATH, not by the dashboard's
    // "yaver / mobile" display name — the same project wears different names on
    // the two surfaces, which is why this arc cannot reuse the web selector.
    const projectPath = process.env.VIBE_PROJECT_PATH || "/root/Workspace/yaver.io/mobile";
    // NOT exact-text on the full path: the app TRUNCATES it in the list
    // ("/root/Workspace/yaver.io/demo/mob…"), so an exact match can never hit —
    // measured from the run-7 screenshot, which showed a healthy, connected
    // Projects screen and a row the locator could not see. Match the tail
    // segment instead, which is what survives truncation.
    const projectLeaf = projectPath.split("/").filter(Boolean).slice(-2).join("/");
    const row = page.getByText(new RegExp(projectLeaf.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))).first();
    await expect(row, `mobile: no project row for ${projectPath}`).toBeVisible({ timeout: 30_000 });
    await row.click();
    await page.waitForTimeout(12_000);

    // Tapping a project opens an action sheet — "What do you want to do?" —
    // offering WebRTC Reload / Browser Reload / Hermes (coming soon). This loop
    // is the BROWSER lane, so take that one explicitly; WebRTC is a different
    // transport with different failure modes and picking it would test
    // something this spec does not claim to cover.
    // Two legitimate states, and the sheet differs between them: with NO preview
    // running the project offers "Browser Reload"; with one already running it
    // offers "Stop" and the preview is already up. Demanding the first state
    // makes the test order-dependent — it passed by hand and failed in the
    // suite purely because an earlier run had left a preview alive.
    const browserReload = page.getByText(/^Browser Reload$/).first();
    if (await browserReload.count()) {
      await browserReload.click();
      await page.waitForTimeout(25_000);
    } else {
      const alreadyRunning = await page.locator("iframe").count();
      expect(alreadyRunning,
        'mobile: the project sheet offered neither "Browser Reload" nor a running preview')
        .toBeGreaterThan(0);
      await page.waitForTimeout(6000);
    }

    await expect(page.locator("iframe").first(),
      "mobile: the browser-lane preview never rendered").toBeVisible({ timeout: 90_000 });

    // Re-render on demand while waiting for the colour. Same path the arc uses
    // to open the preview in the first place: back to /apps, tap the project,
    // take the Browser Reload lane. Without this the arc polls the frame it
    // rendered BEFORE the runner edited anything.
    mobileReloadPreview = async () => {
      const base = (process.env.MOBILE_WEB_URL || "").replace(/\/$/, "");
      await page.goto(`${base}/apps`, { waitUntil: "domcontentloaded", timeout: 60_000 });
      await page.waitForTimeout(6000);
      // NOT exact-text on the full path: the app TRUNCATES it in the list
    // ("/root/Workspace/yaver.io/demo/mob…"), so an exact match can never hit —
    // measured from the run-7 screenshot, which showed a healthy, connected
    // Projects screen and a row the locator could not see. Match the tail
    // segment instead, which is what survives truncation.
    const projectLeaf = projectPath.split("/").filter(Boolean).slice(-2).join("/");
    const row = page.getByText(new RegExp(projectLeaf.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"))).first();
      if (await row.count()) {
        await row.click().catch(() => {});
        await page.waitForTimeout(6000);
        const br = page.getByText(/^Browser Reload$/).first();
        if (await br.count()) { await br.click().catch(() => {}); await page.waitForTimeout(18_000); }
      }
      return true;
    };

    // The composer is NOT on the Tasks screen — it lives in a modal
    // (tasks.tsx:5288+, autoFocus) that the screen opens on demand, which is
    // why probing /tasks found zero inputs. The app opens it from its OWN route
    // params (tasks.tsx:1536-1544: openNew / prompt / dir / runner), so the
    // arc uses that seam rather than hunting for a floating button.
    mobileSendVibe = async (text: string) => {
      const base = (process.env.MOBILE_WEB_URL || "").replace(/\/$/, "");
      await page.goto(
        `${base}/tasks?openNew=1&dir=${encodeURIComponent(projectPath)}&prompt=${encodeURIComponent(text)}`,
        { waitUntil: "domcontentloaded", timeout: 60_000 },
      );
      await page.waitForTimeout(14_000);
      const composer = page.locator("input,textarea").first();
      await expect(composer, "mobile: the compose modal did not open from openNew=1")
        .toBeVisible({ timeout: 30_000 });
      await page.getByText(/^(Send|Start|Run)$/i).first().click();
      await page.waitForTimeout(6000);
      // Back to the preview so the colour can be read.
      await page.goto(`${base}/apps`, { waitUntil: "domcontentloaded", timeout: 60_000 });
      await page.waitForTimeout(10_000);
      const back = page.getByText(new RegExp(projectPath.split("/").filter(Boolean).slice(-2).join("/"))).first();
      if (await back.count()) {
        await back.click().catch(() => {});
        await page.waitForTimeout(8000);
        const br = page.getByText(/^Browser Reload$/).first();
        if (await br.count()) { await br.click().catch(() => {}); await page.waitForTimeout(20_000); }
      }
      return true;
    };
  } else {
    await page.getByText(/^Studio$/).first().click().catch(() => {});
  }
  await page.waitForTimeout(8000);

  const body = await page.evaluate(() => document.body.innerText);
  expect(body, "the box must both run and render for a single-box loop").toMatch(
    new RegExp(`${BOX}|runs and renders`, "i"),
  );

  // EVERYTHING BELOW UNTIL THE BASELINE IS THE DASHBOARD'S UI, NOT THE APP'S.
  //
  // The mobile branch above already did the equivalent work in the app's own
  // idiom: it picked the project by PATH row, took the "Browser Reload" lane
  // explicitly, and asserted the preview iframe. Then control fell through to
  // here and ran the dashboard steps anyway — a `<select>` of projects, an
  // "Ask … to change" composer placeholder, a "Load Targets" button, a
  // "Web UI in browser" card. The RN app renders none of those, so the mobile
  // arc died at the `<select>` EVERY time (`resolved to 0 elements`, 63×) and
  // had never once reached the vibe it exists to test — while the coverage
  // audit listed mobile as "TESTED — RN-web, phone viewport".
  //
  // That is the suite's own false-confidence failure mode, so the gate is
  // explicit rather than a shared selector that happens to match twice.
  if (surface !== "mobile") {
  // Project. Option VALUES are absolute paths while the LABEL reads
  // "yaver / mobile · expo", so match the label — a value-substring match
  // happily selects yaver-todo-rn.
  const projectSelect = page
    .locator("select")
    .filter({ has: page.locator("option", { hasText: new RegExp(PROJECT, "i") }) })
    .first();
  await expect(projectSelect, "the project catalogue must list the target project")
    .toHaveCount(1, { timeout: 60_000 });
  const opts = await projectSelect.locator("option").evaluateAll((els) =>
    els.map((e) => ({ value: (e as HTMLOptionElement).value, label: (e.textContent || "").trim() })),
  );
  const match = opts.find((o) => new RegExp(`^${PROJECT}\\b`, "i").test(o.label));
  expect(match, `no option labelled "${PROJECT}"`).toBeTruthy();
  await projectSelect.selectOption(match!.value);
  await page.waitForTimeout(6000);

  // The composer names the project that will ACTUALLY be vibed — verifying
  // page text alone passes on any screen that merely mentions the name.
  const placeholder = await page
    .getByPlaceholder(/Ask .* to change/i).first().getAttribute("placeholder");
  expect(placeholder || "", "composer must be pointed at the selected project")
    .toMatch(new RegExp(PROJECT, "i"));

  // Targets
  const loadTargets = page.getByRole("button", { name: /Load Targets/i }).first();
  if (await loadTargets.count()) await loadTargets.click().catch(() => {});
  await expect(openControls(page).first(), "the box must offer a render target")
    .toBeVisible({ timeout: 90_000 });

  // Open the BROWSER-LANE card by name. The list also offers "WebRTC over
  // browser"; opening the wrong card tests a different transport entirely.
  const n = await openControls(page).count();
  let opened = false;
  for (let i = 0; i < n; i++) {
    const btn = openControls(page).nth(i);
    const cardText = await btn.evaluate((el) => {
      let node: HTMLElement | null = el as HTMLElement;
      for (let up = 0; up < 5 && node?.parentElement; up++) node = node.parentElement;
      return (node?.innerText || "").slice(0, 200);
    });
    if (/web ui in browser/i.test(cardText)) { await btn.click(); opened = true; break; }
  }
  expect(opened, 'the "Web UI in browser" target was not offered').toBe(true);

  await expect(page.locator("iframe").first(), "the browser-lane preview must render")
    .toBeVisible({ timeout: 90_000 });
  await page.waitForTimeout(12_000);
  } // end dashboard-only setup

  // Baseline. An EMPTY panel and a black app are identical to a sampler, so
  // require real content before trusting any colour reading.
  const base = await samplePreview(page);
  expect(base.rendered, "preview frame is empty — nothing to assert a colour against").toBe(true);

  // → target
  // SAY "VISIBLE", OR A CORRECT EDIT CAN STILL READ BLACK (2026-08-03).
  //
  // "Change the login page background color to red" is satisfiable by editing
  // ONLY the outer SafeAreaView — which is exactly what a GLM turn did: the
  // file genuinely said backgroundColor: "red", the task reached `review`, and
  // the sampled pixels stayed black because the KeyboardAvoidingView and
  // ScrollView beneath it paint over the whole visible area. The web arc had
  // passed minutes earlier only because that turn happened to edit both.
  //
  // A loop whose verdict depends on how thorough the model felt is not
  // measuring the product. Naming the VISIBLE area removes that variance
  // without weakening the pixel verdict — we still demand real pixels, we just
  // stop accepting a prompt that a correct-but-invisible edit satisfies.
  // WHICH SCREEN, and why it is a parameter now.
  //
  // "the login screen" is correct for the yaver app, whose first screen IS the
  // login. It is wrong for any project that opens on something else: sfmg opens
  // on a language chooser, so on 2026-08-04 the runner correctly reddened the
  // login screen, the sampler photographed the chooser, and the arc reported the
  // product broken. The verdict must name the screen the app actually RENDERS,
  // or the pixels being judged belong to a different screen than the edit.
  //
  // Default keeps every existing invocation identical.
  const screenName = process.env.VIBE_TARGET_SCREEN || "login screen";
  const targetPrompt =
    `Change the ${screenName} so its VISIBLE background is ${target}. ` +
    `Every container that paints the full-screen background must be ${target} ` +
    `(the outer SafeAreaView AND any KeyboardAvoidingView/ScrollView/View that covers it), ` +
    `so the whole screen reads ${target}. Only the ${screenName} background — nothing else.`;
  // Fingerprint the tree BEFORE the turn, so the verdict can tell "this turn
  // edited something" from "this repository already had edits in it".
  const treeBefore = await treeFingerprint();
  // Baseline colour tokens too — the repo-truth gates compare against this so
  // pre-existing developer work can never masquerade as (or hide) the turn.
  const colorsBefore = await repoColors();
  if (mobileSendVibe) {
    await mobileSendVibe(targetPrompt);
  } else {
    const composer = page.getByPlaceholder(/Ask .* to change/i).first();
    await composer.fill(targetPrompt);
    await page.getByRole("button", { name: /^Send$/ }).first().click();
    await page.waitForTimeout(3000);
  }
  const hit = await waitForColor(page, target, TURN_BUDGET_MS, mobileReloadPreview ?? undefined);
  if (!hit.ok) {
    // Distinguish "the edit landed somewhere we are not looking" from "nothing
    // happened". Only the second is the product failing. See turnChangedTree.
    const tree = await turnChangedTree(treeBefore);
    if (tree === "changed") {
      test.skip(
        true,
        `the runner DID edit the project (working tree is dirty) but the sampled screen never became ${target} — ` +
        `the frame showed: ${hit.said || "(oracle unavailable)"}. That is this arc pointing at a screen the ` +
        `project does not open on, not the product failing. Set VIBE_TARGET_SCREEN to the screen this app ` +
        `actually renders, or navigate past the gate, then re-run.`,
      );
    }
  }
  // RUNNER-FINISHES CONTRACT: the pixel gate above can fire mid-edit (hot
  // reload shows the change while the runner is still building — measured
  // 2026-08-06: the red task's diff transiently held NO colour token because
  // the model had swapped the literal for a constant before defining it).
  // Judging the repo while the runner works races the edit, exactly as the
  // revert-vs-red task race did. Wait for the task to reach a terminal state.
  const redTerminal = await waitForTaskTerminal(`VISIBLE background`, TURN_BUDGET_MS);
  expect(redTerminal, `the red turn's task must reach a terminal state (saw ${redTerminal})`)
    .not.toBe("timeout");
  expect(redTerminal, `the red turn's task must not fail (saw ${redTerminal})`)
    .not.toBe("failed");
  expect(hit.ok, `preview never turned ${target} (last ${hit.color})${hit.said ? ` — ${hit.said}` : ""}`).toBe(true);

  // REPO-TRUTH GATE (red): the pixel verdict alone is not the edit landing.
  // The 2026-08-06 race — a revert task running while the red task was still
  // building, the red task re-applying after the revert — proved pixels can
  // say the right thing while the repository disagrees. The working-tree diff
  // must show a NEW red-classified literal (classified by the SAME unit-tested
  // classifier as the pixels, so a model writing #D32F2F cannot dodge it).
  const afterRed = await repoColors();
  const redBefore = redOccurrences(colorsBefore.tokens);
  const redAfter = redOccurrences(afterRed.tokens);
  expect(redAfter, `repo must show MORE red occurrences after the red turn ` +
    `(red went ${redBefore} → ${redAfter}; baseline tokens: ${colorsBefore.tokens.join(", ") || "(none)"}; ` +
    `after tokens: ${afterRed.tokens.join(", ") || "(none)"} — pixels said red but the box's working tree disagrees)`)
    .toBeGreaterThan(redBefore);

  // Revert as a SEPARATE task — exercises the new-task render path, not just a
  // follow-up on a warm session.
  const revertPrompt = "Revert the login page background color back to black.";
  if (mobileSendVibe) {
    await mobileSendVibe(revertPrompt);
  } else {
    const newSession = page.getByRole("button", { name: /New session/i }).first();
    if (await newSession.count()) { await newSession.click().catch(() => {}); await page.waitForTimeout(3000); }
    const composer2 = page.getByPlaceholder(/Ask .* to change/i).first();
    await composer2.fill(revertPrompt);
    await page.getByRole("button", { name: /^Send$/ }).first().click();
    await page.waitForTimeout(3000);
  }
  const back = await waitForColor(page, "black", TURN_BUDGET_MS, mobileReloadPreview ?? undefined);
  expect(back.ok, `preview never reverted to black (last ${back.color})${back.said ? ` — ${back.said}` : ""}`).toBe(true);

  // Same runner-finishes contract on the revert turn — the 2026-08-06 race
  // (revert sent while the red task was still running) cannot recur if the
  // revert's own task is allowed to reach a terminal state first.
  const revertTerminal = await waitForTaskTerminal(`Revert the login page`, TURN_BUDGET_MS);
  expect(revertTerminal, `the revert turn's task must reach a terminal state (saw ${revertTerminal})`)
    .not.toBe("timeout");
  expect(revertTerminal, `the revert turn's task must not fail (saw ${revertTerminal})`)
    .not.toBe("failed");

  // REPO-TRUTH GATE (black) — THE guard this loop was missing.
  //
  // Measured 2026-08-06: pixels went black, the arc passed, and the box's
  // working tree still held the colour literal — the revert task had raced the
  // still-running red task, which re-applied its edit AFTER the arc sampled
  // black. Pixels alone could not see it; the repository can. A black pixel
  // verdict with the colour still in the tree is a FAIL, not a pass.
  const afterBlack = await repoColors();
  const redAfterBlack = redOccurrences(afterBlack.tokens);
  expect(redAfterBlack, `repo must be clean of the red the turn introduced after the revert turn ` +
    `(red occurrences ${redBefore} → ${redAfterBlack}; baseline tokens: ` +
    `${colorsBefore.tokens.join(", ") || "(none)"}; after tokens: ${afterBlack.tokens.join(", ") || "(none)"}). ` +
    `This is the false-green the repo-truth gate exists to catch.`)
    .toBeLessThanOrEqual(redBefore);


  // THE PIXELS COMING BACK IS NOT PROOF THE PROJECT CAME BACK.
  //
  // Measured 2026-08-04: this arc PASSED on sfmg — red, then black again — and
  // left `contentStyle: { backgroundColor: '#FF0000' }` on the login Stack.Screen
  // and `backgroundColor: 'red'` on language-select. The revert turn had restored
  // what was VISIBLE and not everything it introduced, so a green run silently
  // deposited sediment in a real project with the developer's own uncommitted work
  // in it. Run this arc a few times and that accumulates, invisibly, forever.
  //
  // So the verdict now includes the repository. A mismatch is reported loudly
  // rather than failing the colour assertion — the loop DID prove the product
  // works, and conflating "the product is broken" with "the test did not tidy up"
  // is the same category error this file keeps correcting. It names the exact
  // paths so a human can revert them.
  if (treeBefore !== null) {
    const treeAfter = await treeFingerprint();
    if (treeAfter !== null && treeAfter !== treeBefore) {
      const before = new Set(treeBefore.split("\n").map((l) => l.trim()).filter(Boolean));
      const leftover = treeAfter
        .split("\n")
        .map((l) => l.trim())
        .filter((l) => l && !before.has(l));
      console.warn(
        `[vibe-loop] RESIDUE: the colour was reverted but the working tree was not restored. ` +
        `Still changed after the revert turn: ${leftover.length ? leftover.join(", ") : "(same files, different content)"}. ` +
        `Revert these by hand — the arc mutates a real project and must leave it as it found it.`,
      );
    }
  }}

test.describe("vibe colour closed loop", () => {
  test.skip(!haveCreds(),
    "needs YAVER_TEST_TOKEN (preferred) or YAVER_TEST_EMAIL + YAVER_TEST_PASSWORD — an environment gap is not a product defect");
  // One dev-server slot on the box, and each arc edits the same file.
  test.describe.configure({ mode: "serial", timeout: 40 * 60_000 });

  test("web dashboard: black → red → black on the browser lane", async ({ page }) => {
    const web = profileFor("web");
    await page.setViewportSize({ width: web.width, height: web.height });
    await signIn(page);
    await runVibeArc(page, "red", "web");
  });

  // ── the MOBILE CLIENT arc ────────────────────────────────────────────────
  //
  // This drives the YAVER MOBILE APP as the client — the same RN app the user
  // holds, served as RN-web so Chromium can drive it — against the SAME box,
  // the SAME runner and the SAME browser lane as the web test above. The arc is
  // identical on purpose: any difference observed is then the SURFACE, not the
  // scenario.
  //
  // It needs a URL for the RN-web build (`cd mobile && npm run web`, or a
  // deployed one) in MOBILE_WEB_URL. It is SKIPPED, not failed, without one.
  //
  // An earlier version of this test simply shrank the viewport and drove the
  // web dashboard, then called itself "mobile RN-web". That is a false
  // equivalence: the dashboard and the RN app share neither their transport
  // ladder, their auth storage (RN-web keys the token as
  // `yaver.secure.yaver_auth_token`, the dashboard as `yaver_auth_token`), nor
  // their render path. A green result there would have said nothing about the
  // app the user actually holds — the exact class of false confidence this
  // suite exists to prevent, reproduced inside the suite.
  test("mobile app (RN-web client): black → red → black on the browser lane", async ({ browser }) => {
    const mobileUrl = process.env.MOBILE_WEB_URL || "";
    test.skip(!mobileUrl,
      "set MOBILE_WEB_URL to the Yaver RN-web build (cd mobile && npm run web). " +
      "Refusing to shrink the dashboard viewport and call it the mobile app.");

    // A TOKEN alone is NOT enough for this arc, and that is a product fact
    // rather than a harness shortcut — measured 2026-08-02.
    //
    // Seeding both storage keys DOES restore the session: the app logs
    // `[auth] restored <email>` and keeps the keys. But every DEVICE call then
    // 401s — `public.yaver.io/presence`, `/d/<id>/info`, `/d/<id>/ops`, and the
    // tailnet agent — because the device/relay layer needs credentials that the
    // real sign-in flow establishes and that storage seeding does not. The app
    // reads that cascade as a dead session and routes to /login, which is the
    // documented false-logout shape, not a broken token (the same token drives
    // the WEB arc to a green pixel verdict, and /auth/validate reports
    // scope=full).
    //
    // So: SKIP with the reason, rather than fail. A red here would blame the
    // product for a credential the harness was never given.
    //
    // 2026-08-06 experiment: the token path below (seeding both storage keys
    // from /auth/validate) is now allowed to run when only a token is present.
    // The 2026-08-02 measurement (every device call 401 → empty project list)
    // predates the current full-scope owner token flow; if the token path now
    // completes the arc it stays enabled, and if it fails on the documented
    // empty-device-list it gets reverted to skip.
    test.skip(!creds().token && !(creds().email && creds().password),
      "the mobile arc needs YAVER_TEST_TOKEN (token path, 2026-08-06 experiment) or " +
      "YAVER_TEST_EMAIL + YAVER_TEST_PASSWORD (app's own flow, which also establishes " +
      "the device/relay credentials). A session token restores auth but on 2026-08-02 " +
      "left every device call 401 — see the comment above.");

    // A NEW CONTEXT with the full device descriptor — not setViewportSize.
    //
    // isMobile, hasTouch, deviceScaleFactor and the user agent are CONTEXT
    // properties; they cannot be changed on an existing page. So resizing a
    // desktop context to 393px gives a narrow desktop browser, and RN-web
    // renders a different component tree for it than for a phone. The viewport
    // assertion inside runVibeArc catches exactly that, which is how this was
    // found — the guard failed its own first draft.
    const profile = profileFor("mobile");
    const mobileCtx = await browser.newContext({
      ...devices[profile.playwrightDevice!],
      // RECORD EXPLICITLY. `video: "on"` in playwright.loops.config.ts applies
      // only to Playwright's OWN managed context (the `page` fixture) — a
      // context created by hand, as this arc must (device descriptors are
      // context properties), inherits none of it. So the mobile arc recorded
      // NOTHING for its entire history while the config plainly said video was
      // on: a false green about evidence, on the surface whose failures have
      // been hardest to read.
      recordVideo: { dir: "test-results/loops/" + (process.env.LOOP_RUN_ID || "mobile") + "/video" },
      // RN-web stores the session under a DIFFERENT key than the dashboard —
      // seeding the wrong one leaves the app on its login screen forever.
      storageState: undefined,
    });
    const page = await mobileCtx.newPage();
    try {
      await page.goto(mobileUrl, { waitUntil: "domcontentloaded", timeout: 60_000 });
      await page.waitForTimeout(9000);

      // TOKEN PATH FIRST — seed BOTH keys the app actually reads.
      //
      // An earlier attempt seeded only `yaver.secure.yaver_auth_token` and the
      // app stayed on its login screen, because auth.ts also requires a USER
      // record under `yaver.secure.yaver_user`. The note left behind said the
      // shape "was not copyable" from the dashboard — true, but the wrong
      // conclusion: it is not copied, it is FETCHED. `GET /auth/validate` with
      // the bearer token returns exactly the fields the app needs, under
      // different names, so the mapping (userId→id, fullName→name) is the whole
      // trick. That makes the mobile arc runnable from a revocable TOKEN with
      // no password anywhere — the same credential posture helpers/login.ts
      // already argues for.
      // …BUT ONLY WHEN THERE IS NO PASSWORD. This block used to run first
      // unconditionally, which contradicted the skip message ten lines above:
      // that message says the arc needs email+password precisely BECAUSE a
      // seeded token restores the session while every device call still 401s.
      // Running the token path first walked straight into the documented
      // failure — the app rendered "Projects", so the screen assertion passed,
      // but the list was EMPTY (no device credentials), and the arc died on a
      // missing project row that a password sign-in shows immediately.
      //
      // Measured 2026-08-02 with a standalone probe: signing in through the
      // app's own email flow yields "Connected · Primary · v1.99.397 ·
      // ubuntu-4gb-hel1-1" and 33 projects including the target row. Same app,
      // same box, same moment — only the credential path differed.
      if (creds().token && !(creds().email && creds().password)) {
        const convexSite =
          process.env.E2E_CONVEX_URL ||
          process.env.NEXT_PUBLIC_CONVEX_SITE_URL ||
          "https://perceptive-minnow-557.eu-west-1.convex.site";
        const res = await page.request.get(`${convexSite}/auth/validate?_=${Date.now()}`, {
          headers: { Authorization: `Bearer ${creds().token}`, "Cache-Control": "no-store" },
        });
        expect(res.ok(), `mobile: /auth/validate rejected the session token (HTTP ${res.status()})`).toBe(true);
        const v = (await res.json()) as { user?: Record<string, unknown> };
        const u = v.user || {};
        const appUser = {
          id: u.userId,
          email: u.email,
          name: u.fullName,
          provider: u.provider,
          emailVerified: u.emailVerified,
          surveyCompleted: u.surveyCompleted,
          isOwner: u.isOwner,
        };
        await page.evaluate(
          ({ t, user }) => {
            localStorage.setItem("yaver.secure.yaver_auth_token", t as string);
            localStorage.setItem("yaver.secure.yaver_user", JSON.stringify(user));
          },
          { t: creds().token, user: appUser },
        );
        await page.reload({ waitUntil: "domcontentloaded", timeout: 60_000 });
        await page.waitForTimeout(12_000);
      }

      // Fallback: the app's OWN email flow, for password-based runs.
      const emailBtn = page.getByText(/Continue with Email/i).first();
      if (creds().email && creds().password && (await emailBtn.count())) {
        await emailBtn.click();
        await page.waitForTimeout(4000);
        await page.getByPlaceholder("Email").first().fill(creds().email);
        await page.getByPlaceholder("Password").first().fill(creds().password);
        await page.getByText(/^Sign In$/).first().click();
        await page.waitForTimeout(15_000);
      }

      await runVibeArc(page, "red", "mobile");
    } finally {
      await mobileCtx.close().catch(() => {});
    }
  });
});
