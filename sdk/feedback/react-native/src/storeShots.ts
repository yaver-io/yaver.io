/**
 * On-device App Store screenshot capture (Engine 2).
 *
 * The desktop `yaver shots` flow drives a simulator with Maestro. This is
 * the in-app counterpart: the SDK, living inside the real running app,
 * walks the app's own routes and screenshots each one with
 * `react-native-view-shot`, then uploads the frames to the Yaver agent
 * (POST /shots/upload). The agent normalizes them and runs the same App
 * Store Connect backend (upload → metadata → submit).
 *
 * Why this exists alongside the simulator path: it captures *real device*
 * pixels, knows the exact route map (no heuristics), and the user is
 * already authenticated — so it sidesteps the i18n / no-testID fragility
 * that makes a blind simulator walk hard.
 *
 * The host wires it once by handing us a navigation ref (react-navigation
 * or expo-router router) plus the ordered list of routes to visit.
 */

import { captureScreenshotBase64 } from './capture';

export interface StoreShotFrame {
  route: string;
  base64: string;
  mimeType: string;
}

export interface CaptureStoreScreenshotsOptions {
  /** Yaver agent base URL (e.g. http://192.168.1.5:18080 or a relay URL). */
  agentUrl: string;
  /** Bearer token for the agent (same-user envelope). */
  authToken: string;
  /** Relay password — required only when agentUrl is relay-routed. */
  relayPassword?: string;

  /** App name (vault scope / job label on the agent side). */
  app: string;
  /** iOS bundle id; the agent falls back to app.json if omitted. */
  bundleId?: string;
  /** App Store localization (default en-US). */
  locale?: string;
  /** When true, the agent also sets metadata + attempts submit-for-review. */
  submit?: boolean;

  /**
   * Navigation handle. Either a react-navigation ref (has `.navigate`) or
   * an expo-router `router` (has `.push`/`.navigate`). We call the first
   * available method with the route string.
   */
  navigationRef?: any;
  /** Ordered routes to visit + screenshot (e.g. ['/(tabs)/dashboard', ...]). */
  routes: string[];

  /** Optional per-route screenshot names (defaults to NN_<sanitized route>). */
  screens?: string[];
  /** Milliseconds to wait after navigating before capturing (default 900). */
  settleMs?: number;
  /** Hook the host can use to hide its own overlay before each capture. */
  onBeforeCapture?: (route: string, index: number) => void | Promise<void>;
}

export interface CaptureStoreScreenshotsResult {
  ok: boolean;
  captured: number;
  uploaded: number;
  submitted?: boolean;
  staged?: boolean;
  message?: string;
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

function navigateTo(navRef: any, route: string): void {
  if (!navRef) return;
  // react-navigation ref
  if (typeof navRef.navigate === 'function') {
    navRef.navigate(route as never);
    return;
  }
  // expo-router router
  if (typeof navRef.push === 'function') {
    navRef.push(route);
    return;
  }
  if (navRef.current && typeof navRef.current.navigate === 'function') {
    navRef.current.navigate(route as never);
    return;
  }
}

function shotName(route: string, index: number, override?: string): string {
  if (override) return override;
  const clean = route
    .replace(/^\/+/, '')
    .replace(/[()[\]/]+/g, '_')
    .replace(/[^a-zA-Z0-9_]+/g, '')
    .replace(/^_+|_+$/g, '');
  const n = String(index + 1).padStart(2, '0');
  return `${n}_${clean || 'screen'}`;
}

/**
 * Walk the app's routes, screenshot each, and upload the batch to the
 * agent. Returns a summary; never throws for a single missed route —
 * it captures what it can and reports the count.
 */
export async function captureStoreScreenshots(
  opts: CaptureStoreScreenshotsOptions,
): Promise<CaptureStoreScreenshotsResult> {
  if (!opts.agentUrl || !opts.authToken) {
    return { ok: false, captured: 0, uploaded: 0, message: 'agentUrl + authToken required' };
  }
  if (!opts.routes?.length) {
    return { ok: false, captured: 0, uploaded: 0, message: 'no routes to capture' };
  }

  const settleMs = opts.settleMs ?? 900;
  const frames: StoreShotFrame[] = [];

  for (let i = 0; i < opts.routes.length; i++) {
    const route = opts.routes[i];
    try {
      navigateTo(opts.navigationRef, route);
      await sleep(settleMs);
      if (opts.onBeforeCapture) await opts.onBeforeCapture(route, i);
      const shot = await captureScreenshotBase64();
      if (shot?.base64) {
        frames.push({
          route: shotName(route, i, opts.screens?.[i]),
          base64: shot.base64,
          mimeType: shot.mimeType,
        });
      }
    } catch {
      // Skip a route that failed to render — keep walking.
    }
  }

  if (frames.length === 0) {
    return { ok: false, captured: 0, uploaded: 0, message: 'captured no frames' };
  }

  const headers: Record<string, string> = {
    Authorization: `Bearer ${opts.authToken}`,
    'Content-Type': 'application/json',
  };
  if (opts.relayPassword) headers['X-Relay-Password'] = opts.relayPassword;

  const base = opts.agentUrl.replace(/\/$/, '');
  try {
    const resp = await fetch(`${base}/shots/upload`, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        app: opts.app,
        bundleId: opts.bundleId ?? '',
        locale: opts.locale ?? 'en-US',
        submit: !!opts.submit,
        frames,
      }),
    });
    const j = await resp.json().catch(() => ({}));
    if (!resp.ok || j?.ok === false) {
      return {
        ok: false,
        captured: frames.length,
        uploaded: 0,
        message: j?.error || `agent /shots/upload HTTP ${resp.status}`,
      };
    }
    return {
      ok: true,
      captured: frames.length,
      uploaded: typeof j?.uploaded === 'number' ? j.uploaded : frames.length,
      submitted: j?.submitted,
      staged: j?.staged,
      message: j?.message,
    };
  } catch (e: any) {
    return {
      ok: false,
      captured: frames.length,
      uploaded: 0,
      message: `upload failed: ${e?.message ?? 'network error'}`,
    };
  }
}
