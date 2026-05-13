/* eslint-disable no-undef */
// Yaver browser extension — background service worker.
// Two responsibilities:
//   1. Capture screenshots (chrome.tabs.captureVisibleTab — content scripts can't).
//   2. POST the bundle to the local Yaver agent at the user-configured URL.

const DEFAULT_AGENT_URL = 'http://localhost:18080';
const STORAGE_KEY = 'yaver-extension-settings';

async function getSettings() {
  const stored = await chrome.storage.local.get(STORAGE_KEY);
  const s = stored[STORAGE_KEY] || {};
  return {
    agentUrl: (s.agentUrl || DEFAULT_AGENT_URL).replace(/\/$/, ''),
    authToken: s.authToken || '',
    autoSend: s.autoSend !== false,
    kind: s.kind || 'design-reference',
  };
}

async function captureViewport(tabId) {
  const tab = await chrome.tabs.get(tabId);
  return chrome.tabs.captureVisibleTab(tab.windowId, { format: 'png' });
}

async function captureFullPage(tabId) {
  // Strategy: ask the content script to scroll through the page, capture each
  // viewport via captureVisibleTab, then stitch in the content script (canvas).
  // Falls back to viewport-only if the scroll-stitch fails.
  const tab = await chrome.tabs.get(tabId);
  const [{ result: dims }] = await chrome.scripting.executeScript({
    target: { tabId },
    func: () => ({
      scrollHeight: document.documentElement.scrollHeight,
      clientHeight: window.innerHeight,
      scrollWidth: document.documentElement.scrollWidth,
      clientWidth: window.innerWidth,
      devicePixelRatio: window.devicePixelRatio || 1,
    }),
  });
  const steps = Math.ceil(dims.scrollHeight / dims.clientHeight);
  const shots = [];
  for (let i = 0; i < steps; i++) {
    await chrome.scripting.executeScript({
      target: { tabId },
      func: (y) => window.scrollTo(0, y),
      args: [i * dims.clientHeight],
    });
    await new Promise((r) => setTimeout(r, 150));
    const dataUrl = await chrome.tabs.captureVisibleTab(tab.windowId, { format: 'png' });
    shots.push({ y: i * dims.clientHeight, dataUrl });
    // captureVisibleTab is rate-limited to ~2/sec — pace ourselves.
    if (i < steps - 1) await new Promise((r) => setTimeout(r, 600));
  }
  // Reset scroll.
  await chrome.scripting.executeScript({
    target: { tabId },
    func: () => window.scrollTo(0, 0),
  });
  return { mode: 'fullpage', shots, dims };
}

function dataUrlToBlob(dataUrl) {
  const [meta, b64] = dataUrl.split(',');
  const mime = /data:(.*?);base64/.exec(meta)?.[1] || 'image/png';
  const bin = atob(b64);
  const arr = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
  return new Blob([arr], { type: mime });
}

async function postBundle(bundle) {
  const settings = await getSettings();
  const form = new FormData();
  form.append('metadata', JSON.stringify({
    kind: settings.kind,
    source: 'browser-extension',
    capturedAt: new Date().toISOString(),
    ...bundle.metadata,
  }));
  if (bundle.screenshotDataUrl) {
    form.append('screenshot_0', dataUrlToBlob(bundle.screenshotDataUrl), 'viewport.png');
  }
  if (Array.isArray(bundle.fullPageShots)) {
    bundle.fullPageShots.forEach((s, i) => {
      // Zero-padded so the agent's sort.Strings() preserves capture order
      // (fullpage_10.png would otherwise sort before fullpage_2.png).
      const idx = String(i).padStart(3, '0');
      form.append(`screenshot_${i + 1}`, dataUrlToBlob(s.dataUrl), `fullpage_${idx}.png`);
    });
  }
  if (bundle.html) {
    form.append('html', new Blob([bundle.html], { type: 'text/html' }), 'dom.html');
  }
  if (bundle.styles) {
    form.append('styles', new Blob([JSON.stringify(bundle.styles)], { type: 'application/json' }), 'styles.json');
  }
  const headers = {};
  if (settings.authToken) headers['Authorization'] = `Bearer ${settings.authToken}`;
  headers['X-Client-Platform'] = 'browser-extension';

  // Try the dedicated design-references endpoint first; fall back to the
  // legacy /feedback endpoint so older agents still ingest captures while
  // we wait for everyone to update.
  const endpoints = [`${settings.agentUrl}/design-references`, `${settings.agentUrl}/feedback`];
  let lastError = null;
  for (const url of endpoints) {
    try {
      const resp = await fetch(url, { method: 'POST', headers, body: form });
      if (resp.ok || (resp.status >= 400 && resp.status !== 404 && resp.status !== 405 && resp.status !== 501)) {
        return {
          ok: resp.ok,
          status: resp.status,
          endpoint: url,
          body: await resp.text().catch(() => ''),
        };
      }
      lastError = `HTTP ${resp.status} on ${url}`;
    } catch (e) {
      lastError = `${e?.message || e} on ${url}`;
    }
  }
  return { ok: false, status: 0, error: lastError || 'all endpoints failed' };
}

// Message routing from content scripts / popup.
chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  (async () => {
    try {
      if (msg.type === 'yaver:get-settings') {
        sendResponse(await getSettings());
        return;
      }
      if (msg.type === 'yaver:set-settings') {
        await chrome.storage.local.set({ [STORAGE_KEY]: msg.settings });
        sendResponse({ ok: true });
        return;
      }
      if (msg.type === 'yaver:capture-viewport') {
        const tabId = msg.tabId ?? sender.tab?.id;
        const dataUrl = await captureViewport(tabId);
        sendResponse({ ok: true, dataUrl });
        return;
      }
      if (msg.type === 'yaver:capture-fullpage') {
        const tabId = msg.tabId ?? sender.tab?.id;
        const result = await captureFullPage(tabId);
        sendResponse({ ok: true, ...result });
        return;
      }
      if (msg.type === 'yaver:post-bundle') {
        const result = await postBundle(msg.bundle);
        sendResponse(result);
        return;
      }
      if (msg.type === 'yaver:list-references') {
        const settings = await getSettings();
        const headers = {};
        if (settings.authToken) headers['Authorization'] = `Bearer ${settings.authToken}`;
        try {
          const resp = await fetch(`${settings.agentUrl}/design-references`, {
            headers,
            signal: AbortSignal.timeout(3000),
          });
          if (!resp.ok) {
            sendResponse({ ok: false, status: resp.status });
            return;
          }
          const items = await resp.json();
          sendResponse({ ok: true, items: Array.isArray(items) ? items : [] });
        } catch (e) {
          sendResponse({ ok: false, error: String(e) });
        }
        return;
      }
      if (msg.type === 'yaver:ping-agent') {
        const settings = await getSettings();
        try {
          const resp = await fetch(`${settings.agentUrl}/health`, {
            signal: AbortSignal.timeout(2000),
          });
          sendResponse({ ok: resp.ok, status: resp.status, agentUrl: settings.agentUrl });
        } catch (e) {
          sendResponse({ ok: false, error: String(e), agentUrl: settings.agentUrl });
        }
        return;
      }
      sendResponse({ ok: false, error: `unknown message ${msg.type}` });
    } catch (e) {
      sendResponse({ ok: false, error: String(e?.message || e) });
    }
  })();
  return true;
});

// Keyboard shortcuts: forward to active tab's content script.
chrome.commands.onCommand.addListener(async (command) => {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab?.id) return;
  if (command === 'capture-element') {
    chrome.tabs.sendMessage(tab.id, { type: 'yaver:start-element-pick' });
  } else if (command === 'capture-page') {
    chrome.tabs.sendMessage(tab.id, { type: 'yaver:capture-page-now' });
  }
});
