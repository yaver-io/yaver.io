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

const corsHeaders = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET, OPTIONS",
  "Access-Control-Allow-Headers": "Accept, Content-Type",
  "Access-Control-Max-Age": "86400",
};

export async function OPTIONS() {
  return new Response(null, { status: 204, headers: corsHeaders });
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
  return NextResponse.json(
    {
      convexSiteUrl,
      webBaseUrl,
      // Kept for older clients. Hosted inference is permanently unavailable
      // under the zero-knowledge control-plane contract.
      gatewayUrl: "",
      generatedAt: new Date().toISOString(),
    },
    { headers: corsHeaders },
  );
}
