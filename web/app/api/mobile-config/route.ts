import { NextResponse } from "next/server";

function normalizeOrigin(value: string | undefined, fallback: string): string {
  const raw = (value || fallback).trim();
  try {
    const parsed = new URL(raw);
    parsed.hash = "";
    parsed.search = "";
    parsed.pathname = "";
    return parsed.toString().replace(/\/+$/, "");
  } catch {
    return fallback;
  }
}

// Optional origin: returns "" when the env var is unset, so the client keeps
// the feature disabled rather than falling back to a guessed host.
function optionalOrigin(value: string | undefined): string {
  const raw = (value || "").trim();
  if (!raw) return "";
  try {
    const parsed = new URL(raw);
    parsed.hash = "";
    parsed.search = "";
    parsed.pathname = "";
    return parsed.toString().replace(/\/+$/, "");
  } catch {
    return "";
  }
}

export async function GET() {
  const convexSiteUrl = normalizeOrigin(
    process.env.CONVEX_SITE_URL,
    "https://perceptive-minnow-557.eu-west-1.convex.site",
  );
  const webBaseUrl = normalizeOrigin(
    process.env.NEXT_PUBLIC_BASE_URL,
    "https://yaver.io",
  );
  // Yaver Gateway origin (captive-OpenRouter inference proxy). Advertised only
  // once YAVER_GATEWAY_URL is configured; until then mobile managed mode stays
  // off (empty → no override). The mobile client also honours a device-local
  // LOCAL_KEYS.gatewayUrl override for pre-rollout testing.
  const gatewayUrl = optionalOrigin(process.env.YAVER_GATEWAY_URL);
  return NextResponse.json({
    convexSiteUrl,
    webBaseUrl,
    gatewayUrl,
    generatedAt: new Date().toISOString(),
  });
}
