import { NextResponse } from "next/server";

function getConvexSiteUrl(): string {
  return process.env.CONVEX_SITE_URL || "";
}

export async function POST(request: Request) {
  const convexSiteUrl = getConvexSiteUrl();
  if (!convexSiteUrl) {
    return NextResponse.json({ error: "Auth backend is not configured." }, { status: 500 });
  }

  const authHeader = request.headers.get("authorization");
  if (!authHeader) {
    return NextResponse.json({ error: "Unauthorized" }, { status: 401 });
  }

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return NextResponse.json({ error: "Invalid request body" }, { status: 400 });
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
