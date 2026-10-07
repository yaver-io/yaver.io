import { test, expect, devices } from "@playwright/test";
import { profileFor, viewportMatchesSurface } from "../../web/lib/surfaceViewports";

test("Plain SSH opens without cloud identity and names the browser limitation", async ({ browser }, info) => {
  test.skip(!process.env.MOBILE_WEB_URL, "MOBILE_WEB_URL must point to the real RN-web app");
  const profile = profileFor("mobile");
  const context = await browser.newContext({ ...devices["iPhone 15 Pro"] });
  const page = await context.newPage();
  const crashes: string[] = [];
  page.on("pageerror", (error) => crashes.push(error.message));
  try {
    // No injected auth, fake native module or dashboard substitution. Cloud
    // unavailability must not prevent the account-independent entry rendering.
    await context.route("**/*", (route) => {
      const url = new URL(route.request().url());
      if (url.origin !== new URL(process.env.MOBILE_WEB_URL!).origin && url.hostname !== "cdn.jsdelivr.net" && !["127.0.0.1", "localhost"].includes(url.hostname) && /^https?:$/.test(url.protocol)) return route.abort();
      return route.continue();
    });
    await page.goto(new URL("/plain",process.env.MOBILE_WEB_URL!).href);
    const observed = await page.evaluate(() => ({ width: innerWidth, height: innerHeight, hasTouch: navigator.maxTouchPoints > 0, isMobile: /Mobile/.test(navigator.userAgent) }));
    expect(observed.width).toBe(profile.width);
    expect(viewportMatchesSurface("mobile", observed).ok).toBe(true);
    await expect(page.getByText("Plain SSH", { exact: true })).toBeVisible({ timeout: 90_000 });
    if (!process.env.PLAIN_SSH_BROWSER_FIXTURE) {
      await expect(page.getByText("Direct SSH needs the native iOS or Android app. Browsers cannot open an SSH connection.")).toBeVisible();
      await expect(page.getByLabel("Private key (stored only on this device)")).toHaveCount(0);
    } else {
      await expect(page.getByLabel("Hostname or Tailscale address")).toBeVisible();
    }
    await expect(page.getByText("Starting Yaver…", {exact:true})).toHaveCount(0);
    await page.screenshot({ path: info.outputPath("plain-ssh-mobile.png"), fullPage: true });
    await page.getByText("‹ Back",{exact:true}).click();
    await expect(page.getByRole("button", { name: "Use Plain SSH without an account" })).toBeVisible();
    await page.getByRole("button", { name: "Use Plain SSH without an account" }).click();
    await expect(page.getByText("Plain SSH", { exact: true })).toBeVisible();
    await page.getByText("‹ Back",{exact:true}).click();
    await expect(page.getByRole("button", {name:"Use Plain SSH without an account"})).toBeVisible();
    expect(crashes).toEqual([]);
  } finally { await context.close(); }
});


test("real SSH pane stays attached while switching Raw and Pane chat", async ({browser},info)=>{
  test.skip(!process.env.PLAIN_SSH_BROWSER_FIXTURE || !process.env.MOBILE_WEB_URL,"real SSH fixture and RN-web URL required");
  const fs=await import("node:fs");
  const fixture=JSON.parse(fs.readFileSync(process.env.PLAIN_SSH_BROWSER_FIXTURE!,"utf8"));
  const context=await browser.newContext({...devices["iPhone 15 Pro"]});
  const page=await context.newPage();
  const opens:string[]=[];
  page.on("request",request=>{if(request.url().endsWith("/invoke") && request.postDataJSON()?.op==="open")opens.push(request.postData()!);});
  try {
    await page.goto(new URL("/plain",process.env.MOBILE_WEB_URL!).href);
    await expect(page.getByText("Plain SSH",{exact:true})).toBeVisible();
    await expect(page.getByText("Starting Yaver…",{exact:true})).toHaveCount(0);
    await page.getByLabel("Hostname or Tailscale address").fill(fixture.host);
    await page.getByLabel("SSH port",{exact:true}).fill(String(fixture.port));
    await page.getByLabel("SSH username",{exact:true}).fill(fixture.user);
    await page.getByRole("button",{name:"Password",exact:true}).click();
    await page.getByLabel("SSH password",{exact:true}).fill(fixture.password);
    await page.getByRole("button",{name:"Check host key",exact:true}).click();
    await expect(page.getByText(fixture.fingerprint,{exact:false})).toBeVisible();
    await page.getByRole("button",{name:"Trust host and connect",exact:true}).click();
    await page.getByRole("button",{name:/work · 0 · %/}).click();
    await expect(page.getByRole("tab",{name:"Raw",exact:true})).toHaveAttribute("aria-selected","true");
    await expect(page.locator(".xterm-screen")).toBeVisible();
    await page.getByLabel("Message to selected pane").fill("phone-pane-proof");
    await page.getByRole("button",{name:"Send",exact:true}).click();
    await page.getByRole("tab",{name:"Pane chat",exact:true}).click();
    await expect(page.getByText("Live pane",{exact:true})).toBeVisible();
    await expect(page.getByTestId("live-pane-output")).toContainText("phone-pane-proof");
    await expect(page.getByText("Starting Yaver…",{exact:true})).toHaveCount(0);
    await page.screenshot({path:info.outputPath("pane-chat-mobile.png"),fullPage:true});
    await page.getByRole("tab",{name:"Raw",exact:true}).click();
    await expect(page.locator(".xterm-screen")).toBeVisible();
    expect(opens).toHaveLength(1);
    await page.screenshot({path:info.outputPath("raw-pane-mobile.png"),fullPage:true});
    await page.getByText("‹ Back",{exact:true}).click();
    await expect(page.getByRole("button",{name:"Use Plain SSH without an account"})).toBeVisible();
  } finally {await context.close();}
});
