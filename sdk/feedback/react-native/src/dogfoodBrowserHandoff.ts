type ReservedBrowserWindow = {
  closed?: boolean;
  close?: () => void;
  location?: { href: string };
  document?: {
    title?: string;
    body?: { innerHTML: string };
  };
};

let reservedWindow: ReservedBrowserWindow | null = null;

/**
 * Browser popup permission belongs to the original user gesture. Runtime
 * preparation is asynchronous, so reserve the preview surface before the
 * first await and paint an honest launching state into it.
 */
export function reserveDogfoodBrowserWindow(): boolean {
  const browser = globalThis as unknown as {
    window?: { open?: (url?: string, target?: string) => ReservedBrowserWindow | null };
  };
  if (!browser.window?.open) return false;
  if (reservedWindow && !reservedWindow.closed) return true;
  const next = browser.window.open('', '_blank');
  if (!next) return false;
  reservedWindow = next;
  try {
    if (next.document) {
      next.document.title = 'Launching Dogfood';
      if (next.document.body) {
        next.document.body.innerHTML = '<main style="min-height:100vh;display:grid;place-items:center;background:#071b3d;color:#eef7ff;font:600 18px system-ui"><div aria-label="Launching Dogfood">Launching Dogfood<span style="display:inline-block;animation:yaverPulse 1s ease-in-out infinite">…</span></div><style>@keyframes yaverPulse{50%{opacity:.25}}</style></main>';
      }
    }
  } catch {
    // The reserved surface still protects the gesture even if a browser does
    // not allow its about:blank document to be styled.
  }
  return true;
}

export function navigateReservedDogfoodBrowserWindow(rawUrl: string): boolean {
  const target = reservedWindow;
  reservedWindow = null;
  if (!target || target.closed || !target.location) return false;
  try {
    const parsed = new URL(rawUrl);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      target.close?.();
      return false;
    }
    target.location.href = parsed.toString();
    return true;
  } catch {
    target.close?.();
    return false;
  }
}

export function closeReservedDogfoodBrowserWindow(): void {
  const target = reservedWindow;
  reservedWindow = null;
  if (target && !target.closed) target.close?.();
}
