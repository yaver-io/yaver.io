import AsyncStorage from "@react-native-async-storage/async-storage";
import { Platform } from "react-native";
import {
  CONVEX_SITE_URL as DEFAULT_CONVEX_SITE_URL,
  WEB_BASE_URL as DEFAULT_WEB_BASE_URL,
} from "../_core/constants";
import { appLog } from "./logger";
import { normalizePrivateVpsUrl, PRIVATE_VPS_STORAGE_KEY } from "./privateVps";

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
let privateVpsUrl = "";
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

export function getPrivateVpsUrlSync(): string {
  return privateVpsUrl;
}

export async function setPrivateVpsUrl(value: string): Promise<string> {
  const normalized = normalizePrivateVpsUrl(value);
  if (!normalized) throw new Error("Enter a valid HTTPS server URL without credentials, query text, or a fragment.");
  privateVpsUrl = normalized;
  currentConvexSiteUrl = normalized;
  currentWebBaseUrl = normalized;
  lastRefreshAt = 0;
  await AsyncStorage.setItem(PRIVATE_VPS_STORAGE_KEY, normalized);
  return normalized;
}

export async function clearPrivateVpsUrl(): Promise<void> {
  privateVpsUrl = "";
  currentConvexSiteUrl = normalizeOrigin(DEFAULT_CONVEX_SITE_URL) || DEFAULT_CONVEX_SITE_URL;
  currentWebBaseUrl = normalizeOrigin(DEFAULT_WEB_BASE_URL) || DEFAULT_WEB_BASE_URL;
  lastRefreshAt = 0;
  await AsyncStorage.removeItem(PRIVATE_VPS_STORAGE_KEY);
}

export async function hydrateBackendConfigFromCache(): Promise<void> {
  try {
    const localOverride = normalizePrivateVpsUrl(await AsyncStorage.getItem(PRIVATE_VPS_STORAGE_KEY));
    if (localOverride) {
      privateVpsUrl = localOverride;
      currentConvexSiteUrl = localOverride;
      currentWebBaseUrl = localOverride;
      return;
    }
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
  // A device-local private server is authoritative until the user clears it.
  // Hosted discovery must never silently route that device back to Yaver.
  if (privateVpsUrl) return;
  const now = Date.now();
  if (!force && now - lastRefreshAt < REFRESH_TTL_MS) return;

  // Metro's localhost origin cannot consume the production discovery route
  // until that deployment advertises CORS. Defaults + explicit local
  // overrides already cover development, so avoid a guaranteed browser error
  // instead of emitting a false network incident on every RN-web launch.
  if (Platform.OS === "web" && typeof location !== "undefined" && /^(localhost|127\.0\.0\.1)$/.test(location.hostname)) {
    lastRefreshAt = now;
    return;
  }

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
