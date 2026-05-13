# Yaver browser extension

Capture any web UI as an AI-readable reference and feed it to your local
Yaver agent. The bundle is a screenshot **plus** the underlying DOM, computed
styles, layout boxes, and asset URLs — everything an AI runner needs to vibe-
code an interface that matches a polished reference instead of a vague prompt.

Pairs with the rest of Yaver: the captured bundle lands on your local agent as
a `design-reference` feedback report, and you can drive it from the active
vibing task via the existing `/feedback` workflow.

- **Manifest V3** — service worker + content script, no build step.
- **Local-first** — captures POST to `http://localhost:18080` (your Yaver
  agent), never to the cloud.
- **Automation-ready** — exposes `window.__yaver` so Selenium, Playwright,
  and Puppeteer can drive captures programmatically (examples included).

## Install (unpacked, dev mode)

1. Run a Yaver agent: `yaver serve`
2. Chrome / Brave / Edge / Arc → `chrome://extensions` → enable **Developer
   mode** (top right) → **Load unpacked** → select this folder
   (`sdk/feedback/browser-extension/`).
3. Pin the extension icon to the toolbar.

## Use

- Click the Yaver icon → **Pick an element** (or `⌘⇧Y` / `Ctrl+Shift+Y`).
- Or **Capture viewport** (`⌘⇧P` / `Ctrl+Shift+P`) for the whole visible page.
- Or **Capture full page** to scroll and stitch (slower, rate-limited by
  Chrome to ~2 screenshots/sec).

The green dot in the popup means your local agent is reachable.

The capture lands at `POST /feedback` on the configured agent with
`kind: design-reference`. From there you can:

- Browse it in the Yaver web dashboard's Feedback view.
- Pass the bundle ID to a vibing task as a reference.
- Wire it into your runner's prompt (`yaver code` reads the latest
  design-reference automatically when you ask "build me a UI like the
  reference I just captured").

## Automation API

```js
await window.__yaver.capturePage();
await window.__yaver.captureFullPage();
await window.__yaver.captureSelector('.pricing-card');
window.__yaver.startPicker();
await window.__yaver.ping();
await window.__yaver.settings({ agentUrl: 'http://localhost:18080', autoSend: true });
```

See `examples/` for Selenium, Playwright, and Puppeteer harnesses.

### Headless

All three frameworks load extensions only in Chromium and only with
`--headless=new` (Chrome 109+). Classic `--headless` silently drops
extensions. The example scripts pass the right flag.

## What lives in the bundle

```ts
{
  metadata: { mode, selector, meta: { url, title, viewport, docSize, userAgent, capturedAt } },
  html: string,                       // outerHTML of the captured root
  styles: {
    rootSelector,
    nodes: [{                         // depth-first walk, capped at 800 nodes
      i, depth, tag, selector,
      id?, cls?, text?, rect,
      attrs?,                         // src/href/alt/aria-label/role/...
      styles?,                        // filtered set of ~50 layout/visual props
    }],
    assets: string[],                 // image / font / background URLs
  },
  screenshotDataUrl?: string,         // viewport mode
  fullPageShots?: [{ y, dataUrl }],   // full-page mode (one PNG per scroll step)
}
```

The DOM walk strips `<script>` / `<noscript>`, filters computed styles to a
curated list of layout + visual properties (50ish, biased toward what an AI
needs to reproduce the look), and caps traversal at 800 nodes per capture to
keep bundles small.

## Configuration

Popup → settings:

- **Agent URL** — defaults to `http://localhost:18080`. Point at a remote
  device by tunneling first (`yaver code --attach <device>` exposes the agent
  locally) or by routing through the relay (`https://public.yaver.io/d/<id>`).
- **Auth token** — sent as `Authorization: Bearer …` if your agent requires it.
- **Auto-send to agent** — toggle off to keep captures local in extension
  storage (useful when iterating on the capture format itself).

Settings persist in `chrome.storage.local` and sync per-profile.

## Permissions — what the extension can do, and what it can't

- `activeTab`, `scripting`, `storage`, `tabs` — standard.
- Content script runs on `<all_urls>` so the overlay is available everywhere
  without a per-site permission prompt. It does **not** read network traffic
  or page secrets; it serializes the DOM only when you trigger a capture.
- `host_permissions` lists only `localhost` / `127.0.0.1` — captures can be
  POSTed to a local agent but never to a third-party endpoint without you
  changing the manifest.

## License

Apache-2.0 (same as the rest of Yaver).
