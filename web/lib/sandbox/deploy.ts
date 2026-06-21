"use client";

// deploy.ts — push a browser-built `.yaver.tgz` to a Yaver Serverless target.
//
// The bundle is byte-compatible with what the Go agent produces, so deploy is
// just `POST <base>/phone/projects/receive` with the gzip bytes as the raw body
// and slug/onConflict as query params (the non-multipart intake path in
// phone_backend_http.go handlePhoneReceive). The browser can reach Yaver Cloud
// directly over HTTPS; dev-machine/relay targets go through the agent client
// (added separately) because relay auth headers are private to it.

import { exportLocalBundle } from "./localProjects";

export interface DeployResult {
  ok: boolean;
  slug: string;
  browseUrl?: string;
  dataUrl?: string;
  status: number;
  error?: string;
}

export interface CloudDeployOptions {
  /** Yaver Cloud / self-hosted base URL, e.g. https://cloud.yaver.io */
  baseUrl: string;
  /** Yaver session token — Authorization: Bearer. Never a GLM/API key. */
  token?: string;
  slug: string;
  onConflict?: "reject" | "rename" | "overwrite";
  includeData?: boolean;
}

/** Deploy a local project to a serverless target reachable over plain HTTPS. */
export async function deployLocalProjectToCloud(opts: CloudDeployOptions): Promise<DeployResult> {
  const bytes = await exportLocalBundle(opts.slug, opts.includeData ?? true);
  const base = opts.baseUrl.replace(/\/$/, "");
  const qs = new URLSearchParams({
    slug: opts.slug,
    onConflict: opts.onConflict ?? "overwrite",
  });
  const url = `${base}/phone/projects/receive?${qs.toString()}`;

  const headers: Record<string, string> = { "Content-Type": "application/octet-stream" };
  if (opts.token) headers.Authorization = `Bearer ${opts.token}`;

  let res: Response;
  try {
    res = await fetch(url, {
      method: "POST",
      headers,
      // Copy into a fresh ArrayBuffer so the body is a clean BodyInit.
      body: bytes.slice().buffer,
    });
  } catch (e) {
    return { ok: false, slug: opts.slug, status: 0, error: e instanceof Error ? e.message : String(e) };
  }

  if (res.status === 402) {
    return { ok: false, slug: opts.slug, status: 402, error: "Payment required — activate a managed plan on the web dashboard, then retry." };
  }
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    return { ok: false, slug: opts.slug, status: res.status, error: text || `deploy failed (${res.status})` };
  }

  let body: { slug?: string; browseUrl?: string; dataUrl?: string } = {};
  try {
    body = (await res.json()) as typeof body;
  } catch {
    /* some targets stream SSE / return no JSON — treat 2xx as success */
  }
  const slug = body.slug || opts.slug;
  return {
    ok: true,
    slug,
    status: res.status,
    browseUrl: body.browseUrl || `${base}/phone/projects/browse?slug=${encodeURIComponent(slug)}`,
    dataUrl: body.dataUrl || `${base}/data/${encodeURIComponent(slug)}`,
  };
}
