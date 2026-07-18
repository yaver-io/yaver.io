import { NextRequest, NextResponse } from "next/server";

import { CONVEX_URL } from "@/lib/constants";

function readBearer(request: NextRequest): string | null {
  const auth = request.headers.get("authorization");
  if (!auth?.startsWith("Bearer ")) return null;
  return auth.slice(7).trim() || null;
}

export async function POST(request: NextRequest) {
  const token = readBearer(request);
  if (!token) {
    return NextResponse.json({ ok: false, error: "Unauthorized" }, { status: 401 });
  }

  const body = await request.json().catch(() => null);
  if (!body || typeof body.deviceId !== "string") {
    return NextResponse.json({ ok: false, error: "deviceId required" }, { status: 400 });
  }

  const upstream = await fetch(`${CONVEX_URL}/devices/request-update`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      deviceId: body.deviceId,
      ...(typeof body.version === "string" ? { version: body.version } : {}),
    }),
    cache: "no-store",
  });

  const text = await upstream.text();
  let data: any = {};
  try {
    data = text ? JSON.parse(text) : {};
  } catch {
    data = { error: text || `request-update HTTP ${upstream.status}` };
  }

  return NextResponse.json(data, { status: upstream.status });
}
