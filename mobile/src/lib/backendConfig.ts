import AsyncStorage from "@react-native-async-storage/async-storage";
import {
  CONVEX_SITE_URL as DEFAULT_CONVEX_SITE_URL,
  WEB_BASE_URL as DEFAULT_WEB_BASE_URL,
} from "../_core/constants";
import { appLog } from "./logger";

const BACKEND_CONFIG_KEY = "@yaver/backend_config_v1";
const REFRESH_TTL_MS = 15 * 60 * 1000;

let currentConvexSiteUrl = normalizeOrigin(DEFAULT_CONVEX_SITE_URL) || DEFAULT_CONVEX_SITE_URL;
let currentWebBaseUrl = normalizeOrigin(DEFAULT_WEB_BASE_URL) || DEFAULT_WEB_BASE_URL;
// Yaver Gateway (captive-OpenRouter inference proxy) origin. Empty until the
// hosted config (/api/mobile-config) advertises it post-deploy, so an older
// binary discovers the Worker without a rebuild — same story as convexSiteUrl.
// Managed-mode coding stays disabled while empty (fail-safe). A device-local
// override (LOCAL_KEYS.gatewayUrl) takes priority for testing pre-rollout.
let currentGatewayUrl = "";
let lastRefreshAt = 0;

function normalizeOrigin(value: string | null | undefined): string | null {
  const raw = (value || "").trim();
  if (!raw) return null;
  try {
    const parsed = new URL(raw);
    parsed.hash = "";
    parsed.search = "";
    parsed.pathname = "";
    return parsed.toString().replace(/\/+$/, "");
  } catch {
    return null;
  }
}

function applyConfig(
  next: { convexSiteUrl?: string; webBaseUrl?: string; gatewayUrl?: string },
  source: string,
) {
  const nextConvex = normalizeOrigin(next.convexSiteUrl);
  const nextWeb = normalizeOrigin(next.webBaseUrl);
  const nextGateway = normalizeOrigin(next.gatewayUrl);
  let changed = false;

  if (nextConvex && nextConvex !== currentConvexSiteUrl) {
    currentConvexSiteUrl = nextConvex;
    changed = true;
  }
  if (nextWeb && nextWeb !== currentWebBaseUrl) {
    currentWebBaseUrl = nextWeb;
    changed = true;
  }
  if (nextGateway && nextGateway !== currentGatewayUrl) {
    currentGatewayUrl = nextGateway;
    changed = true;
  }

  if (changed) {
    appLog(
      "info",
      `[backend-config] ${source}: convex=${currentConvexSiteUrl} web=${currentWebBaseUrl} gateway=${currentGatewayUrl || "(unset)"}`,
    );
  }
}

export function getConvexSiteUrlSync(): string {
  return currentConvexSiteUrl;
}

export function getWebBaseUrlSync(): string {
  return currentWebBaseUrl;
}

/** Yaver Gateway origin, or "" when not advertised yet (managed mode off). */
export function getGatewayUrlSync(): string {
  return currentGatewayUrl;
}

export async function hydrateBackendConfigFromCache(): Promise<void> {
  try {
    const raw = await AsyncStorage.getItem(BACKEND_CONFIG_KEY);
    if (!raw) return;
    const parsed = JSON.parse(raw) as {
      convexSiteUrl?: string;
      webBaseUrl?: string;
      gatewayUrl?: string;
      refreshedAt?: number;
    };
    applyConfig(parsed, "cache");
    if (typeof parsed.refreshedAt === "number") {
      lastRefreshAt = parsed.refreshedAt;
    }
  } catch {
    // best-effort only
  }
}

export async function refreshHostedBackendConfig(force: boolean = false): Promise<void> {
  const now = Date.now();
  if (!force && now - lastRefreshAt < REFRESH_TTL_MS) return;

  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 5000);
  try {
    // Always resolve through the canonical web origin. This lets an older
    // mobile binary discover a newer Convex deployment after a migration.
    const res = await fetch(`${DEFAULT_WEB_BASE_URL}/api/mobile-config`, {
      signal: controller.signal,
      headers: { Accept: "application/json" },
    });
    if (!res.ok) {
      appLog("warn", `[backend-config] refresh failed: HTTP ${res.status}`);
      return;
    }
    const data = (await res.json()) as {
      convexSiteUrl?: string;
      webBaseUrl?: string;
      gatewayUrl?: string;
      generatedAt?: string;
    };
    applyConfig(data, "remote");
    lastRefreshAt = now;
    await AsyncStorage.setItem(
      BACKEND_CONFIG_KEY,
      JSON.stringify({
        convexSiteUrl: currentConvexSiteUrl,
        webBaseUrl: currentWebBaseUrl,
        gatewayUrl: currentGatewayUrl,
        refreshedAt: lastRefreshAt,
        generatedAt: data.generatedAt,
      }),
    );
  } catch (e) {
    appLog("warn", `[backend-config] refresh error: ${e}`);
  } finally {
    clearTimeout(timeout);
  }
}
