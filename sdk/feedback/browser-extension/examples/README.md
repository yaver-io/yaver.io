# Yaver browser extension — automation examples

The extension exposes its capture API on `window.__yaver` in every page where
the content script runs. That makes it driveable from any browser-automation
framework that can `evaluate()` JavaScript in a page.

## API surface

```js
await window.__yaver.capturePage()                  // viewport screenshot + DOM + styles
await window.__yaver.captureFullPage()              // scroll + stitch (slow)
await window.__yaver.captureSelector('.pricing-card')
window.__yaver.startPicker()                        // overlay element picker
await window.__yaver.ping()                         // is the local agent reachable?
await window.__yaver.settings({ agentUrl, autoSend, authToken })
```

Each capture method resolves to:

```ts
{
  ok: true,
  sent: boolean,                  // whether the bundle was POSTed to the agent
  bundle: {
    metadata: { mode, selector, meta: {...} },
    html: string,                 // outerHTML of the captured root
    styles: { rootSelector, nodes: [...], assets: [...] },
    screenshotDataUrl?: string,
    fullPageShots?: [...]
  }
}
```

## Headless

All three drivers (Selenium / Playwright / Puppeteer) support extensions in
headless mode, but **only Chromium's `--headless=new` flag**. Classic
`--headless` drops extensions silently. The example scripts pass the right
flag for you.

## Rate limits

`chrome.tabs.captureVisibleTab` is capped at ~2 captures/sec by Chrome. Full-
page mode paces itself accordingly (~600ms between scroll steps). For high-
volume scraping, prefer `captureSelector` or skip the screenshot path.

## Selenium

```bash
pip install selenium
python selenium.py https://stripe.com/payments
YAVER_SELECTOR=".pricing" python selenium.py https://stripe.com/payments
YAVER_HEADLESS=1 python selenium.py https://linear.app
```

## Playwright

```bash
npm i -D playwright tsx
npx tsx playwright.ts https://linear.app
```

## Puppeteer

```bash
npm i -D puppeteer
node puppeteer.js https://vercel.com
```
