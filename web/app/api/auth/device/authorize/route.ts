import { NextResponse } from "next/server";

function getConvexSiteUrl(override?: unknown): string {
  if (typeof override === "string" && override.trim()) {
    return override.trim();
  }
  return process.env.CONVEX_SITE_URL || "";
}

export async function POST(request: Request) {
  const authHeader = request.headers.get("authorization");
  if (!authHeader) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }

  let body: { userCode?: string; convexUrl?: string } | null = null;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "Invalid request body" }, { status: 400 });
  }

  const convexSiteUrl = getConvexSiteUrl(body?.convexUrl);
  if (!convexSiteUrl) {
    return NextResponse.json({ error: "Auth backend is not configured." }, { status: 500 });
  }

  const upstream = await fetch(`${convexSiteUrl}/auth/device-code/authorize`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: authHeader,
    },
    body: JSON.stringify(body),
  });

  const text = await upstream.text();
  return new NextResponse(text, {
    status: upstream.status,
    headers: {
      "Content-Type": upstream.headers.get("content-type") || "application/json; charset=utf-8",
      "Cache-Control": "no-store",
    },
  });
}
