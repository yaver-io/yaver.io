import type {
  EVChargingIntent,
  EVParsedQR,
  EVProviderAdapter,
  EVProviderId,
  EVRouteEnv,
  EVRouteOption,
} from "./types";

const TOKEN_KEYS = [
  "station",
  "stationId",
  "station_id",
  "charger",
  "chargerId",
  "charger_id",
  "socket",
  "socketId",
  "socket_id",
  "connector",
  "connectorId",
  "connector_id",
  "evse",
  "evseId",
  "evse_id",
  "unit",
  "unitId",
];

function cleanToken(value?: string | null): string | undefined {
  const v = String(value ?? "").trim();
  if (!v) return undefined;
  if (v.length > 96) return undefined;
  return v;
}

function valueFromUrl(u: URL, keys: string[]): string | undefined {
  for (const key of keys) {
    const q = cleanToken(u.searchParams.get(key));
    if (q) return q;
  }
  const parts = u.pathname.split(/[/?#&=:_-]+/).map(cleanToken).filter(Boolean) as string[];
  return parts.find((part) => /[A-Za-z]*\d{2,}[A-Za-z0-9]*/.test(part));
}

function parseUrl(raw: string): URL | null {
  try {
    return new URL(raw.trim());
  } catch {
    return null;
  }
}

function hostMatches(u: URL, domains: string[]): boolean {
  const host = u.hostname.toLowerCase();
  return domains.some((domain) => host === domain || host.endsWith(`.${domain}`));
}

function routeOptions(provider: string, env: EVRouteEnv): EVRouteOption[] {
  return [
    {
      kind: "provider_deeplink",
      label: `Open ${provider} app`,
      description: "Hand off to the provider app on this phone. Payment, OTP, and final start stay inside the provider UI.",
      risk: "low",
      requiresUserPresent: true,
      requiresApproval: true,
      available: env.hasProviderUrl,
      unavailableReason: env.hasProviderUrl ? undefined : "The QR is not a provider URL.",
    },
    {
      kind: "remote_android",
      label: "Use remote Android",
      description: "Launch the real provider app on an Android device attached to your selected Yaver machine.",
      risk: "medium",
      requiresUserPresent: true,
      requiresApproval: true,
      available: env.hasActiveYaverDevice && env.hasRemoteAndroid,
      unavailableReason: env.hasActiveYaverDevice
        ? env.hasRemoteAndroid
          ? undefined
          : "The selected machine has no attached Android device yet."
        : "Connect to a Yaver device first.",
    },
    {
      kind: "manual_assist",
      label: "Manual assist",
      description: "Use Yaver to track station, connector, timer, and notes while you finish in the provider app.",
      risk: "low",
      requiresUserPresent: true,
      requiresApproval: false,
      available: true,
    },
  ];
}

function parseKnownUrl(raw: string, provider: EVProviderId, domains: string[]): EVParsedQR | null {
  const u = parseUrl(raw);
  if (!u || !hostMatches(u, domains)) return null;
  const stationId = valueFromUrl(u, ["stationId", "station_id", "station", "locationId", "location_id"]);
  const connectorId = valueFromUrl(u, ["connectorId", "connector_id", "connector", "socketId", "socket_id", "socket"]);
  const chargerId = valueFromUrl(u, ["chargerId", "charger_id", "charger", "evseId", "evse_id", "evse", "unitId", "unit"]);
  const hasExplicit = Boolean(stationId || connectorId || chargerId);
  return {
    provider,
    normalizedUrl: u.toString(),
    stationId,
    connectorId,
    chargerId,
    socketLabel: connectorId || chargerId,
    confidence: hasExplicit ? "high" : "medium",
    notes: hasExplicit
      ? ["Provider domain matched and charger fields were extracted."]
      : ["Provider domain matched, but charger fields are opaque. Continue in the provider app."],
  };
}

function parseUnknown(raw: string): EVParsedQR | null {
  const u = parseUrl(raw);
  if (!u) {
    const token = cleanToken(raw);
    if (!token) return null;
    return {
      provider: "unknown",
      confidence: "low",
      notes: ["This is not a URL. Treat it as a station or connector code and continue manually."],
      chargerId: token,
      socketLabel: token,
    };
  }
  const token = valueFromUrl(u, TOKEN_KEYS);
  return {
    provider: "unknown",
    normalizedUrl: u.toString(),
    confidence: token ? "low" : "low",
    chargerId: token,
    socketLabel: token,
    notes: token
      ? ["Unknown provider URL. A station-like value was extracted."]
      : ["Unknown provider URL. Continue with manual assist or open the link if you trust it."],
  };
}

export const EV_PROVIDERS: EVProviderAdapter[] = [
  {
    id: "esarj",
    label: "Esarj",
    domains: ["esarj.com", "esarj.com.tr"],
    androidPackageHints: ["com.esarj.mobile", "esarj"],
    parseQr: (raw) => parseKnownUrl(raw, "esarj", ["esarj.com", "esarj.com.tr"]),
    buildRoutes: (_intent, env) => routeOptions("Esarj", env),
  },
  {
    id: "zes",
    label: "ZES",
    domains: ["zes.net", "zes.com.tr", "zorluenergy.com.tr"],
    androidPackageHints: ["com.solidict.zorluenerji", "zes"],
    parseQr: (raw) => parseKnownUrl(raw, "zes", ["zes.net", "zes.com.tr", "zorluenergy.com.tr"]),
    buildRoutes: (_intent, env) => routeOptions("ZES", env),
  },
  {
    id: "trugo",
    label: "Trugo",
    domains: ["trugo.com.tr"],
    androidPackageHints: ["com.togg.trugoapp", "trugo"],
    parseQr: (raw) => parseKnownUrl(raw, "trugo", ["trugo.com.tr"]),
    buildRoutes: (_intent, env) => routeOptions("Trugo", env),
  },
  {
    id: "enyakit",
    label: "En Yakıt",
    domains: ["enyakit.com.tr"],
    androidPackageHints: ["com.ilerleyen.EnYakit", "enyakit"],
    parseQr: (raw) => parseKnownUrl(raw, "enyakit", ["enyakit.com.tr"]),
    buildRoutes: (_intent, env) => routeOptions("En Yakıt", env),
  },
  {
    id: "voltrun",
    label: "Voltrun",
    domains: ["voltrun.com"],
    androidPackageHints: ["com.voltrun", "voltrun"],
    parseQr: (raw) => parseKnownUrl(raw, "voltrun", ["voltrun.com"]),
    buildRoutes: (_intent, env) => routeOptions("Voltrun", env),
  },
  {
    id: "sharz",
    label: "Sharz",
    domains: ["sharz.net"],
    androidPackageHints: ["com.ipitex.sharz", "sharz"],
    parseQr: (raw) => parseKnownUrl(raw, "sharz", ["sharz.net"]),
    buildRoutes: (_intent, env) => routeOptions("Sharz", env),
  },
  {
    id: "sarjtr",
    label: "Şarj@TR",
    domains: ["epdk.gov.tr"],
    androidPackageHints: ["tr.gov.epdk.sarjetTR", "sarjet"],
    parseQr: (raw) => parseKnownUrl(raw, "sarjtr", ["epdk.gov.tr"]),
    buildRoutes: (_intent, env) => routeOptions("Şarj@TR", env),
  },
  {
    id: "unknown",
    label: "Unknown",
    domains: [],
    androidPackageHints: [],
    parseQr: parseUnknown,
    buildRoutes: (_intent, env) => routeOptions("provider app", env),
  },
];

export function providerLabel(id: EVProviderId): string {
  return EV_PROVIDERS.find((p) => p.id === id)?.label ?? "Unknown";
}

export function providerForIntent(intent: EVChargingIntent): EVProviderAdapter {
  return EV_PROVIDERS.find((p) => p.id === intent.provider) ?? EV_PROVIDERS[EV_PROVIDERS.length - 1];
}

export function parseEVQr(raw: string): EVParsedQR | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  for (const provider of EV_PROVIDERS) {
    if (provider.id === "unknown") continue;
    const parsed = provider.parseQr(trimmed);
    if (parsed) return parsed;
  }
  return parseUnknown(trimmed);
}

export function buildEVRouteOptions(intent: EVChargingIntent, env: EVRouteEnv): EVRouteOption[] {
  return providerForIntent(intent).buildRoutes(intent, env);
}
