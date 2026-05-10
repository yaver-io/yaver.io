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

export async function GET() {
  const convexSiteUrl = normalizeOrigin(
    process.env.CONVEX_SITE_URL,
    "https://perceptive-minnow-557.eu-west-1.convex.site",
  );
  const webBaseUrl = normalizeOrigin(
    process.env.NEXT_PUBLIC_BASE_URL,
    "https://yaver.io",
  );
  return NextResponse.json({
    convexSiteUrl,
    webBaseUrl,
    generatedAt: new Date().toISOString(),
  });
}
