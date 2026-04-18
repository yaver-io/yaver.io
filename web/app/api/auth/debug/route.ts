import { NextResponse } from "next/server";
import { cookies } from "next/headers";

export async function GET() {
  const cookieStore = await cookies();
  const allCookies = cookieStore.getAll();

  const envCheck = {
    OAUTH_GOOGLE_CLIENT_ID: !!process.env.OAUTH_GOOGLE_CLIENT_ID,
    OAUTH_GOOGLE_CLIENT_SECRET: !!process.env.OAUTH_GOOGLE_CLIENT_SECRET,
    OAUTH_MICROSOFT_CLIENT_ID: !!process.env.OAUTH_MICROSOFT_CLIENT_ID,
    OAUTH_MICROSOFT_CLIENT_SECRET: !!process.env.OAUTH_MICROSOFT_CLIENT_SECRET,
    OAUTH_MICROSOFT_TENANT_ID: process.env.OAUTH_MICROSOFT_TENANT_ID || "not set",
    OAUTH_APPLE_CLIENT_ID: process.env.OAUTH_APPLE_CLIENT_ID || "not set",
    OAUTH_APPLE_CLIENT_SECRET: !!process.env.OAUTH_APPLE_CLIENT_SECRET,
    OAUTH_GITHUB_CLIENT_ID: !!process.env.OAUTH_GITHUB_CLIENT_ID,
    OAUTH_GITHUB_CLIENT_SECRET: !!process.env.OAUTH_GITHUB_CLIENT_SECRET,
    CONVEX_SITE_URL: process.env.CONVEX_SITE_URL || "not set",
    NEXT_PUBLIC_BASE_URL: process.env.NEXT_PUBLIC_BASE_URL || "not set",
    NODE_ENV: process.env.NODE_ENV,
  };

  return NextResponse.json({
    envCheck,
    cookies: allCookies.map((c) => ({ name: c.name, hasValue: !!c.value })),
  });
}
