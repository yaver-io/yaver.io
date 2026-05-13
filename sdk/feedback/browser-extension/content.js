/* eslint-disable no-undef */
// Yaver content script — runs in every page. Three jobs:
//   1. Element picker overlay (triggered by user from popup or keyboard).
//   2. DOM + computed-styles serializer.
//   3. Automation API on window.__yaver for Selenium / Playwright / Puppeteer.

(() => {
  if (window.__yaver_installed) return;
  window.__yaver_installed = true;

  // ---------- DOM / style serialization ----------

  const INTERESTING_STYLES = [
    'display', 'position', 'top', 'right', 'bottom', 'left',
    'width', 'height', 'min-width', 'min-height', 'max-width', 'max-height',
    'margin', 'margin-top', 'margin-right', 'margin-bottom', 'margin-left',
    'padding', 'padding-top', 'padding-right', 'padding-bottom', 'padding-left',
    'flex', 'flex-direction', 'flex-wrap', 'flex-grow', 'flex-shrink', 'flex-basis',
    'justify-content', 'align-items', 'align-content', 'align-self', 'gap',
    'grid-template-columns', 'grid-template-rows', 'grid-area', 'grid-column', 'grid-row',
    'color', 'background', 'background-color', 'background-image',
    'border', 'border-radius', 'border-color', 'border-width', 'border-style',
    'box-shadow', 'opacity', 'transform', 'transition',
    'font-family', 'font-size', 'font-weight', 'font-style', 'line-height',
    'letter-spacing', 'text-align', 'text-decoration', 'text-transform',
    'overflow', 'overflow-x', 'overflow-y', 'z-index', 'cursor',
  ];

  function isMeaningfulStyle(name, value) {
    if (!value || value === 'auto' || value === 'none' || value === 'normal') return false;
    if (value === '0px' || value === '0' || value === 'rgba(0, 0, 0, 0)') return false;
    if (name === 'display' && value === 'block') return false;
    if (name === 'position' && value === 'static') return false;
    return true;
  }

  function nthSelector(el) {
    if (!el || el === document.documentElement) return 'html';
    const tag = el.tagName.toLowerCase();
    if (el.id) return `${tag}#${CSS.escape(el.id)}`;
    const parent = el.parentElement;
    if (!parent) return tag;
    const siblings = Array.from(parent.children).filter((c) => c.tagName === el.tagName);
    if (siblings.length === 1) return tag;
    return `${tag}:nth-of-type(${siblings.indexOf(el) + 1})`;
  }

  function cssPath(el) {
    if (!el) return '';
    const parts = [];
    let cur = el;
    while (cur && cur.nodeType === 1 && cur !== document.documentElement && parts.length < 12) {
      parts.unshift(nthSelector(cur));
      cur = cur.parentElement;
    }
    return parts.join(' > ');
  }

  function serializeElement(el, opts = {}) {
    const { maxNodes = 800, includeStyles = true } = opts;
    const out = { rootSelector: cssPath(el), nodes: [], assets: [] };
    let count = 0;
    const seenAssets = new Set();

    function recordAsset(url) {
      if (!url || seenAssets.has(url)) return;
      seenAssets.add(url);
      out.assets.push(url);
    }

    function walk(node, depth) {
      if (count >= maxNodes) return;
      if (node.nodeType !== 1) return;
      if (node.tagName === 'SCRIPT' || node.tagName === 'NOSCRIPT') return;
      const tag = node.tagName.toLowerCase();
      const rect = node.getBoundingClientRect();
      const entry = {
        i: count++,
        depth,
        tag,
        selector: cssPath(node),
        id: node.id || undefined,
        cls: node.className && typeof node.className === 'string' ? node.className : undefined,
        text: tag === 'img' || tag === 'svg'
          ? undefined
          : (Array.from(node.childNodes)
              .filter((c) => c.nodeType === 3)
              .map((c) => c.textContent.trim())
              .filter(Boolean)
              .join(' ')
              .slice(0, 200) || undefined),
        rect: { x: rect.x, y: rect.y, w: rect.width, h: rect.height },
      };
      // Useful attributes for AI-readable references.
      const attrs = {};
      for (const a of ['src', 'href', 'alt', 'title', 'placeholder', 'aria-label', 'role', 'type', 'name']) {
        const v = node.getAttribute(a);
        if (v) attrs[a] = v;
      }
      if (Object.keys(attrs).length) entry.attrs = attrs;
      if (attrs.src) recordAsset(attrs.src);
      if (attrs.href && (tag === 'link' || tag === 'use')) recordAsset(attrs.href);

      if (includeStyles) {
        const cs = window.getComputedStyle(node);
        const styles = {};
        for (const name of INTERESTING_STYLES) {
          const v = cs.getPropertyValue(name);
          if (isMeaningfulStyle(name, v)) styles[name] = v;
        }
        // Pull background-image URLs as assets.
        const bg = cs.getPropertyValue('background-image');
        if (bg && bg !== 'none') {
          const m = bg.match(/url\(["']?([^"')]+)["']?\)/g);
          if (m) m.forEach((u) => recordAsset(u.replace(/^url\(["']?|["']?\)$/g, '')));
        }
        if (Object.keys(styles).length) entry.styles = styles;
      }
      out.nodes.push(entry);

      for (const child of node.children) walk(child, depth + 1);
    }
    walk(el, 0);
    return out;
  }

  function serializeFullPage(opts = {}) {
    const root = document.body;
    const result = serializeElement(root, opts);
    result.meta = {
      url: location.href,
      title: document.title,
      viewport: { w: window.innerWidth, h: window.innerHeight },
      docSize: {
        w: document.documentElement.scrollWidth,
        h: document.documentElement.scrollHeight,
      },
      userAgent: navigator.userAgent,
      capturedAt: new Date().toISOString(),
    };
    return result;
  }

  // ---------- Overlay / picker ----------

  function ensureOverlayRoot() {
    let root = document.getElementById('__yaver-overlay-root');
    if (!root) {
      root = document.createElement('div');
      root.id = '__yaver-overlay-root';
      document.documentElement.appendChild(root);
    }
    return root;
  }

  function toast(message, sub, opts = {}) {
    const root = ensureOverlayRoot();
    const el = document.createElement('div');
    el.className = `__yaver-toast${opts.error ? ' error' : ''}`;
    const title = document.createElement('div');
    title.className = '__yaver-toast-title';
    title.textContent = message;
    el.appendChild(title);
    if (sub) {
      const subEl = document.createElement('div');
      subEl.className = '__yaver-toast-sub';
      subEl.textContent = sub;
      el.appendChild(subEl);
    }
    root.appendChild(el);
    setTimeout(() => el.remove(), opts.duration || 3200);
  }

  let pickHighlight = null;
  let pickLabel = null;
  let pickHover = null;
  let pickActive = false;

  function moveHighlight(target) {
    if (!target) return;
    const r = target.getBoundingClientRect();
    if (!pickHighlight) {
      pickHighlight = document.createElement('div');
      pickHighlight.className = '__yaver-pick-highlight';
      ensureOverlayRoot().appendChild(pickHighlight);
    }
    if (!pickLabel) {
      pickLabel = document.createElement('div');
      pickLabel.className = '__yaver-pick-label';
      ensureOverlayRoot().appendChild(pickLabel);
    }
    pickHighlight.style.left = `${r.left}px`;
    pickHighlight.style.top = `${r.top}px`;
    pickHighlight.style.width = `${r.width}px`;
    pickHighlight.style.height = `${r.height}px`;
    pickLabel.textContent = `${target.tagName.toLowerCase()}${target.id ? '#' + target.id : ''} ${Math.round(r.width)}×${Math.round(r.height)}`;
    pickLabel.style.left = `${r.left}px`;
    pickLabel.style.top = `${Math.max(0, r.top - 22)}px`;
  }

  function clearHighlight() {
    pickHighlight?.remove();
    pickLabel?.remove();
    pickHighlight = null;
    pickLabel = null;
  }

  function onPickMove(e) {
    if (!pickActive) return;
    const el = document.elementFromPoint(e.clientX, e.clientY);
    if (el && el !== pickHover && !el.closest('#__yaver-overlay-root')) {
      pickHover = el;
      moveHighlight(el);
    }
  }

  function onPickClick(e) {
    if (!pickActive) return;
    e.preventDefault();
    e.stopPropagation();
    const target = pickHover;
    stopPick();
    if (target) captureElement(target).catch((err) => toast('Capture failed', String(err), { error: true }));
  }

  function onPickKey(e) {
    if (!pickActive) return;
    if (e.key === 'Escape') {
      stopPick();
      toast('Picker cancelled');
    }
  }

  function startPick() {
    if (pickActive) return;
    pickActive = true;
    document.documentElement.classList.add('__yaver-pick-cursor');
    document.addEventListener('mousemove', onPickMove, true);
    document.addEventListener('click', onPickClick, true);
    document.addEventListener('keydown', onPickKey, true);
    toast('Pick an element', 'Click to capture · Esc to cancel');
  }

  function stopPick() {
    pickActive = false;
    pickHover = null;
    document.documentElement.classList.remove('__yaver-pick-cursor');
    document.removeEventListener('mousemove', onPickMove, true);
    document.removeEventListener('click', onPickClick, true);
    document.removeEventListener('keydown', onPickKey, true);
    clearHighlight();
  }

  // ---------- Capture orchestration ----------

  async function captureElement(el, opts = {}) {
    const bundle = await buildBundle({ root: el, mode: 'element', opts });
    return finishCapture(bundle);
  }

  async function capturePage(opts = {}) {
    const bundle = await buildBundle({ root: document.body, mode: opts.fullPage ? 'fullpage' : 'viewport', opts });
    return finishCapture(bundle);
  }

  async function buildBundle({ root, mode, opts }) {
    const styles = serializeElement(root, opts);
    if (mode === 'fullpage') {
      styles.meta = serializeFullPage(opts).meta;
    } else {
      styles.meta = {
        url: location.href,
        title: document.title,
        viewport: { w: window.innerWidth, h: window.innerHeight },
        capturedAt: new Date().toISOString(),
      };
    }
    let screenshotDataUrl = null;
    let fullPageShots = null;
    if (mode === 'fullpage') {
      const fp = await sendBg({ type: 'yaver:capture-fullpage' });
      if (fp.ok) fullPageShots = fp.shots;
    } else {
      const shot = await sendBg({ type: 'yaver:capture-viewport' });
      if (shot.ok) screenshotDataUrl = shot.dataUrl;
    }
    return {
      metadata: { mode, selector: styles.rootSelector, meta: styles.meta },
      html: root.outerHTML,
      styles,
      screenshotDataUrl,
      fullPageShots,
    };
  }

  async function finishCapture(bundle) {
    const settings = await sendBg({ type: 'yaver:get-settings' });
    if (settings.autoSend) {
      const res = await sendBg({ type: 'yaver:post-bundle', bundle });
      if (res.ok) {
        toast('Captured → agent', `${bundle.metadata.mode} · ${bundle.styles.nodes.length} nodes`);
      } else {
        toast('Captured (agent unreachable)', `${settings.agentUrl} · ${res.status || res.error}`, { error: true });
      }
      return { ok: true, sent: res.ok, bundle };
    }
    toast('Captured (local only)', `${bundle.metadata.mode} · ${bundle.styles.nodes.length} nodes`);
    return { ok: true, sent: false, bundle };
  }

  function sendBg(msg) {
    return new Promise((resolve) => {
      try {
        chrome.runtime.sendMessage(msg, (resp) => resolve(resp || { ok: false, error: 'no response' }));
      } catch (e) {
        resolve({ ok: false, error: String(e?.message || e) });
      }
    });
  }

  // ---------- Public automation API ----------

  // Exposed on `window.__yaver` so Selenium / Playwright can drive captures
  // without simulating clicks on the popup.
  const api = {
    version: '0.1.0',
    async capturePage(opts = {}) {
      return capturePage(opts);
    },
    async captureSelector(selector, opts = {}) {
      const el = document.querySelector(selector);
      if (!el) throw new Error(`yaver: selector not found: ${selector}`);
      return captureElement(el, opts);
    },
    async captureFullPage(opts = {}) {
      return capturePage({ ...opts, fullPage: true });
    },
    startPicker() {
      startPick();
    },
    async ping() {
      return sendBg({ type: 'yaver:ping-agent' });
    },
    async settings(next) {
      if (next) return sendBg({ type: 'yaver:set-settings', settings: next });
      return sendBg({ type: 'yaver:get-settings' });
    },
  };
  Object.defineProperty(window, '__yaver', { value: api, writable: false, configurable: false });
  window.dispatchEvent(new CustomEvent('yaver:ready', { detail: { version: api.version } }));

  // ---------- Background → content routing ----------

  chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
    if (msg.type === 'yaver:start-element-pick') {
      startPick();
      sendResponse({ ok: true });
    } else if (msg.type === 'yaver:capture-page-now') {
      capturePage(msg.opts || {}).then((r) => sendResponse(r)).catch((e) => sendResponse({ ok: false, error: String(e) }));
      return true;
    } else if (msg.type === 'yaver:capture-fullpage-now') {
      capturePage({ ...(msg.opts || {}), fullPage: true }).then((r) => sendResponse(r));
      return true;
    } else if (msg.type === 'yaver:ping') {
      sendResponse({ ok: true });
    }
  });
})();
