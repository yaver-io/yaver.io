/**
 * Yaver Feedback SDK Bootstrap
 *
 * Reads SDK token and config from build-time .env.yaver file.
 * The SDK token is a long-lived token (1 year) that is independent
 * from the CLI session token — CLI reauth does not invalidate it.
 *
 * Token hierarchy:
 * - CLI session token: short-lived, used by `yaver serve`
 * - SDK token: long-lived (1 year), created via `yaver sdk-token create`
 *   or POST /sdk/token, used by the Feedback SDK
 *
 * Connection strategy (same as Yaver mobile app):
 * 1. Direct LAN — try agentUrl from .env.yaver (same WiFi, ~5ms)
 * 2. Convex discovery — fetch agent IP from device registry
 * 3. Relay fallback — connect via QUIC relay server
 */

import { YaverFeedback } from './YaverFeedback';
import { BlackBox } from './BlackBox';

// Build-time injected values from .env.yaver
// YAVER_SDK_TOKEN is preferred (long-lived, independent from CLI session)
// YAVER_AUTH_TOKEN is the fallback (CLI session token, shared temporarily)
const YAVER_CONFIG = {
  authToken: process.env.YAVER_SDK_TOKEN || process.env.YAVER_AUTH_TOKEN || '',
  agentUrl: process.env.YAVER_AGENT_URL || '',
  convexUrl: process.env.YAVER_CONVEX_URL || '',
};

/**
 * Initialize the Yaver Feedback SDK with auto-discovery.
 *
 * Call this once in your app's root layout/entry point.
 * The SDK will:
 * 1. Try direct connection to agentUrl (if on same WiFi)
 * 2. Fall back to Convex device discovery
 * 3. Fall back to relay server
 *
 * @param overrides - Override any auto-detected config values
 */
export async function initYaverSDK(overrides?: {
  authToken?: string;
  agentUrl?: string;
  convexUrl?: string;
  buildPlatforms?: 'ios' | 'android' | 'both' | 'web';
  autoDeploy?: boolean;
}) {
  if (YaverFeedback.isInitialized()) return;

  const authToken = overrides?.authToken || YAVER_CONFIG.authToken;
  const agentUrl = overrides?.agentUrl || YAVER_CONFIG.agentUrl;
  const convexUrl = overrides?.convexUrl || YAVER_CONFIG.convexUrl;

  if (!authToken) {
    console.warn('[YaverSDK] No auth token found. Set YAVER_AUTH_TOKEN in .env.yaver or pass authToken to initYaverSDK().');
    return;
  }

  // Try direct connection first
  let resolvedUrl = agentUrl;
  if (agentUrl) {
    try {
      const controller = new AbortController();
      const timeout = setTimeout(() => controller.abort(), 3000);
      const resp = await fetch(`${agentUrl}/health`, { signal: controller.signal });
      clearTimeout(timeout);
      if (resp.ok) {
        console.log('[YaverSDK] Direct connection OK:', agentUrl);
      } else {
        resolvedUrl = '';
      }
    } catch {
      console.log('[YaverSDK] Direct connection failed, trying discovery...');
      resolvedUrl = '';
    }
  }

  // If direct failed, try Convex discovery
  if (!resolvedUrl && convexUrl) {
    try {
      const resp = await fetch(`${convexUrl}/devices/list`, {
        headers: { Authorization: `Bearer ${authToken}` },
      });
      if (resp.ok) {
        const data = await resp.json();
        const devices = data.devices || data || [];
        const online = devices.find((d: any) => d.isOnline);
        if (online) {
          const candidateUrl = `http://${online.localIP || online.ip}:${online.httpPort || 18080}`;
          try {
            const controller = new AbortController();
            const timeout = setTimeout(() => controller.abort(), 3000);
            const healthResp = await fetch(`${candidateUrl}/health`, { signal: controller.signal });
            clearTimeout(timeout);
            if (healthResp.ok) {
              resolvedUrl = candidateUrl;
              console.log('[YaverSDK] Discovered via Convex:', resolvedUrl);
            }
          } catch {
            // Device not reachable directly
          }
        }
      }
    } catch {
      console.log('[YaverSDK] Convex discovery failed');
    }
  }

  // If still no connection, try relay
  if (!resolvedUrl && convexUrl) {
    try {
      const resp = await fetch(`${convexUrl}/platformConfig`);
      if (resp.ok) {
        const config = await resp.json();
        const relays = config.relayServers || [];
        for (const relay of relays) {
          try {
            const relayUrl = `${relay.url}/proxy/${authToken}`;
            const controller = new AbortController();
            const timeout = setTimeout(() => controller.abort(), 5000);
            const healthResp = await fetch(`${relayUrl}/health`, { signal: controller.signal });
            clearTimeout(timeout);
            if (healthResp.ok) {
              resolvedUrl = relayUrl;
              console.log('[YaverSDK] Connected via relay:', relay.url);
              break;
            }
          } catch {
            continue;
          }
        }
      }
    } catch {
      console.log('[YaverSDK] Relay fallback failed');
    }
  }

  if (!resolvedUrl) {
    console.warn('[YaverSDK] Could not connect to any agent. SDK will start in offline mode.');
  }

  YaverFeedback.init({
    agentUrl: resolvedUrl || undefined,
    authToken,
    convexUrl: convexUrl || undefined,
    trigger: 'floating-button',
    buildPlatforms: overrides?.buildPlatforms || 'both',
    autoDeploy: overrides?.autoDeploy ?? true,
  });

  BlackBox.start();
  BlackBox.wrapConsole();

  console.log('[YaverSDK] Initialized', resolvedUrl ? '(connected)' : '(offline)');
}
