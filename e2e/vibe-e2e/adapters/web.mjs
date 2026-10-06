// adapters/web.mjs — Selenium web-dashboard adapter for the vibe scenario.
// Drives the REAL deployed yaver.io dashboard. Creds via env, never logged.
import { readFileSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { By, until, Key } from "selenium-webdriver";
import { classifyColor } from "../scenario.mjs";

const APP = process.env.YAVER_WEB_URL || "https://yaver.io";
const RELAY = process.env.YAVER_RELAY_HTTP || "https://public.yaver.io";
const TARGET_ID = process.env.YAVER_TARGET_DEVICE || "5e79cf10-90e8-4a4f-bf07-041061dca210";

export function makeWebAdapter(driver, framesDir) {
  let frame = 0;
  const cfg = JSON.parse(readFileSync(join(homedir(), ".yaver", "config.json"), "utf8"));
  const log = (m) => console.log(m);
  const snap = async (tag = "") => {
    try {
      const png = await driver.takeScreenshot();
      writeFileSync(join(framesDir, `f${String(frame++).padStart(4, "0")}${tag ? "_" + tag : ""}.png`), Buffer.from(png, "base64"));
    } catch {}
  };
  const bodyText = async () => (await driver.findElement(By.css("body")).getText().catch(() => "")) || "";
  const clickText = async (rx, timeout = 15000) => {
    const el = await driver.wait(until.elementLocated(By.xpath(`//*[self::button or self::a or @role='button' or self::div][contains(normalize-space(.),${xpathLit(rx)})]`)), timeout);
    await driver.executeScript("arguments[0].scrollIntoView({block:'center'})", el).catch(() => {});
    await el.click().catch(async () => { await driver.executeScript("arguments[0].click()", el); });
  };
  const xpathLit = (s) => (s.includes("'") ? `concat('${s.replace(/'/g, "',\"'\",'")}')` : `'${s}'`);

  return {
    log, snap,

    async login() {
      // FRESH email/password login with the user's own account (env-only, never
      // logged). A fresh sign-in mints a new session + pulls a fresh relay
      // password — the difference from a STALE cached session that showed the
      // false "Unauthorized" and skipped the primary.
      const email = process.env.YAVER_TEST_EMAIL;
      const password = process.env.YAVER_TEST_PASSWORD;
      await driver.get(APP + "/auth");
      await driver.sleep(2500); await snap("auth");
      // Reveal the email form if it's behind a "Continue with Email" toggle.
      const findEmail = async () => (await driver.findElements(By.css('input[type="email"], input[placeholder="Email address"]')))[0];
      if (!(await findEmail())) { try { await clickText("Continue with Email", 6000); await driver.sleep(1500); } catch {} }
      const emailIn = await driver.wait(async () => (await findEmail()) || null, 20000);
      await emailIn.clear(); await emailIn.sendKeys(email);
      const pwIn = await driver.findElement(By.css('input[type="password"]'));
      await pwIn.clear(); await pwIn.sendKeys(password);
      await snap("auth_filled");
      // Submit the login form.
      try {
        const submit = await driver.findElement(By.xpath("//button[@type='submit' or normalize-space(.)='Sign in' or normalize-space(.)='Log in' or normalize-space(.)='Continue']"));
        await driver.executeScript("arguments[0].click()", submit);
      } catch { await pwIn.sendKeys(Key.RETURN); }
      // Land on the dashboard.
      await driver.wait(until.elementLocated(By.xpath("//*[contains(text(),'Devices') or contains(text(),'Studio')]")), 45000);
      await driver.get(APP + "/dashboard");
      await driver.wait(until.elementLocated(By.xpath("//*[contains(text(),'Devices') or contains(text(),'Studio')]")), 30000);
      await snap("dashboard");
    },

    async ensureConnectedToPrimary() {
      // Oracle: the primary box is truly reachable+authorized.
      const H = { Authorization: `Bearer ${cfg.auth_token}` };
      if (cfg.cached_relay_password) H["X-Relay-Password"] = cfg.cached_relay_password;
      const health = await fetch(`${RELAY}/d/${TARGET_ID}/health`, { headers: H }).catch(() => ({ status: 0 }));
      if (health.status !== 200) return { ok: false, reason: `relay /health ${health.status} — box not reachable` };
      // POLL (up to 90s) for the dashboard to auto-connect to the primary; if it
      // hasn't after a warm-up, actively click the box's connect CTA.
      const deadline = Date.now() + 90_000;
      let triggered = false;
      while (Date.now() < deadline) {
        const txt = (await bodyText()).toLowerCase();
        const connected = /connected|relay ·|tunnel ·|direct ·/.test(txt) && txt.includes("ubuntu-4gb");
        if (connected) { await snap("connect"); return { ok: true, detail: `codex=${txt.includes("codex")}` }; }
        if (!triggered && Date.now() - (deadline - 90_000) > 12_000) {
          // Nudge: open/connect the primary box explicitly.
          for (const rx of ["Open Workspace", "Try Connect", "Connect"]) {
            try {
              const el = await driver.findElement(By.xpath(`//button[contains(normalize-space(.),${xpathLit(rx)})]`));
              await driver.executeScript("arguments[0].click()", el); triggered = true; log(`  ·· nudged connect via '${rx}'`); break;
            } catch {}
          }
        }
        await driver.sleep(5000);
      }
      await snap("connect");
      return { ok: false, reason: "dashboard did not show ubuntu-4gb Connected within 90s" };
    },

    async openVibing() {
      // The left-nav Studio item (exact case; the session list may share the label).
      const nav = await driver.wait(until.elementLocated(
        By.xpath("//nav//*[normalize-space(text())='Studio'] | //a[normalize-space(.)='Studio'] | //*[self::button or @role='button'][normalize-space(.)='Studio']"),
      ), 20000);
      await driver.executeScript("arguments[0].click()", nav);
      // Confirm we actually landed on Vibing: its composer / reload controls appear.
      await driver.wait(until.elementLocated(
        By.xpath("//*[contains(.,'Ask codex') or contains(.,'Fast Reload') or contains(.,'RUNNER') or contains(.,'Ready for')]"),
      ), 30000).catch(() => {});
      await driver.sleep(2500);
      await snap("vibing");
    },

    async selectProject() {
      // Vibing may auto-select yaver/mobile (default target). If a picker is
      // present, choose it; otherwise proceed.
      const txt = await bodyText();
      if (!/yaver \/ mobile|yaver\/mobile/i.test(txt)) {
        try { await clickText("yaver / mobile", 8000); await driver.sleep(1500); } catch {}
      }
      await snap("project");
    },

    async renderPreview() {
      // Boot the render surfaces on the primary box. "Load Targets" kicks a web
      // bundle build on the render machine (slow), then shows target cards.
      try {
        const btn = await driver.wait(until.elementLocated(By.xpath("//button[contains(normalize-space(.),'Load Targets')]")), 15000);
        await driver.executeScript("arguments[0].click()", btn);
        log("  ·· clicked Load Targets");
      } catch (e) { log(`  ·· Load Targets not clicked: ${e?.message || e}`); }
      // Wait (up to 3min) for targets/preview to appear: an iframe, or a
      // browser/phone target, or the web-bundle transport starting.
      await driver.wait(async () => {
        if ((await driver.findElements(By.css("iframe"))).length > 0) return true;
        const t = await bodyText();
        return /Web UI bundle|bundle rebuilt|Mobile Web UI|Fast Reload|Open in Yaver|webview\/transport|streaming/i.test(t);
      }, 180000).catch(() => {});
      await snap("targets_loading");
      // Open the "Web UI in browser" target — the DIRECT IFRAME lane (not
      // "WebRTC over browser", which is a video stream we can't DOM-read). Click
      // the Open button INSIDE that specific card.
      let opened = false;
      for (const card of ["Web UI in browser", "Web UI", "direct iframe"]) {
        try {
          const btn = await driver.findElement(By.xpath(`//*[contains(normalize-space(.),${xpathLit(card)})]//button[contains(normalize-space(.),'Open')]`));
          await driver.executeScript("arguments[0].scrollIntoView({block:'center'});arguments[0].click()", btn);
          log(`  ·· opened preview target via card: ${card}`);
          opened = true; break;
        } catch {}
      }
      if (!opened) { try { const b = await driver.findElement(By.xpath("//button[normalize-space(.)='Open']")); await driver.executeScript("arguments[0].click()", b); log("  ·· opened first Open button"); } catch {} }
      // Wait for an iframe (the actual rendered app) to mount.
      await driver.wait(async () => (await driver.findElements(By.css("iframe"))).length > 0, 120000).catch(() => {});
      await driver.sleep(6000);
      await snap("preview_open");
    },

    async _readBgViaFrame() {
      // The preview iframe is same-origin (relay proxy /d/<id>/dev/web-bundle/).
      const frames = await driver.findElements(By.css("iframe"));
      for (const f of frames) {
        try {
          await driver.switchTo().frame(f);
          const bg = await driver.executeScript(`
            const pick = (el)=>el?getComputedStyle(el).backgroundColor:null;
            const root = document.querySelector('[data-testid=login-root]') || document.body;
            // walk to the largest painted background
            let best = pick(root);
            for (const el of document.querySelectorAll('div')) {
              const r = el.getBoundingClientRect();
              if (r.width*r.height > (window.innerWidth*window.innerHeight*0.5)) { const c = pick(el); if (c && c!=='rgba(0, 0, 0, 0)') best = c; }
            }
            return best;
          `).catch(() => null);
          await driver.switchTo().defaultContent();
          if (bg) return bg;
        } catch { await driver.switchTo().defaultContent().catch(() => {}); }
      }
      return null;
    },

    async readPreviewBackground() {
      const viaFrame = await this._readBgViaFrame();
      if (viaFrame) return viaFrame;
      return null; // pixel-sample fallback handled by waitForBackground via screenshots
    },

    async sendChat(text) {
      const box = await driver.wait(until.elementLocated(By.css('textarea, input[placeholder*="Ask" i]')), 20000);
      await box.click();
      await box.sendKeys(Key.chord(Key.META, "a"), Key.DELETE);
      await box.sendKeys(text);
      await snap("chat_typed");
      // Click the ENABLED Send button with a REAL Selenium click (executeScript
      // click doesn't always fire React's onClick). Verify by the composer
      // CLEARING — that is the true "sent" signal (text-in-body is not: the
      // textarea IS in the body).
      let sent = false;
      for (let attempt = 0; attempt < 3 && !sent; attempt++) {
        try {
          const sendBtn = await driver.findElement(By.xpath("//button[normalize-space(.)='Send' and not(@disabled)]"));
          await driver.executeScript("arguments[0].scrollIntoView({block:'center'})", sendBtn);
          await sendBtn.click();
        } catch { try { await box.sendKeys(Key.RETURN); } catch {} }
        await driver.sleep(2500);
        const remaining = (await box.getAttribute("value").catch(() => "")) || "";
        sent = !remaining.includes(text.slice(0, 20));
        if (!sent) { await box.click(); await box.sendKeys(Key.chord(Key.META, "a")); await box.sendKeys(text.slice(0, 0)); }
      }
      await snap("chat_sent");
      return sent;
    },

    async newTask() {
      // Start a fresh vibing session so the next message is its OWN task.
      try {
        const btn = await driver.findElement(By.xpath("//button[normalize-space(.)='New session' and not(@disabled)]"));
        await driver.executeScript("arguments[0].scrollIntoView({block:'center'});arguments[0].click()", btn);
        await driver.sleep(2500);
        await snap("new_task");
      } catch (e) { log(`  ·· New session not clicked: ${e?.message || e}`); }
    },

    async waitForTurnComplete(timeoutMs) {
      const deadline = Date.now() + timeoutMs;
      let last = "";
      while (Date.now() < deadline) {
        const t = (await bodyText());
        const lower = t.toLowerCase();
        last = lower.match(/running|completed|ready|review|waiting for runner/g)?.slice(-1)[0] || last;
        if (/completed|review/.test(lower) && !/running|waiting for runner/.test(lower)) {
          await snap("turn_done");
          return { ok: true, detail: "status=completed" };
        }
        await driver.sleep(4000);
        await snap();
      }
      return { ok: false, reason: `timeout (last=${last})` };
    },

    async waitForRender() { await driver.sleep(6000); await snap("render"); },

    async waitForBackground(target, timeoutMs) {
      const deadline = Date.now() + timeoutMs;
      let color = "unknown";
      while (Date.now() < deadline) {
        const bg = await this.readPreviewBackground();
        color = classifyColor(bg);
        await snap(`bg_${color}`);
        if (color === target) return { ok: true, color };
        await driver.sleep(5000);
      }
      return { ok: false, color };
    },
  };
}
