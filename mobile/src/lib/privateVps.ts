/**
 * Device-local private control-plane selection.
 *
 * The QR contains only a public URL locator, never a bearer token or password.
 * Accepted forms intentionally match every surface:
 *   https://vps.example.com
 *   yaver://private-vps?url=https%3A%2F%2Fvps.example.com
 *   {"privateVpsUrl":"https://vps.example.com"}
 */
export const PRIVATE_VPS_STORAGE_KEY = "@yaver/private_vps_url_v1";

export function normalizePrivateVpsUrl(value: string | null | undefined): string | null {
  const raw = (value || "").trim();
  if (!raw) return null;
  try {
    const parsed = new URL(raw);
    if (parsed.protocol !== "https:") return null;
    if (parsed.username || parsed.password || parsed.search || parsed.hash) return null;
    parsed.pathname = parsed.pathname.replace(/\/+$/, "");
    return parsed.toString().replace(/\/+$/, "");
  } catch {
    return null;
  }
}

export function privateVpsUrlFromQr(value: string | null | undefined): string | null {
  const raw = (value || "").trim();
  if (!raw) return null;

  const direct = normalizePrivateVpsUrl(raw);
  if (direct) return direct;

  try {
    const parsed = new URL(raw);
    if (parsed.protocol === "yaver:" && parsed.hostname === "private-vps") {
      return normalizePrivateVpsUrl(parsed.searchParams.get("url"));
    }
  } catch {
    // Fall through to the small JSON enrollment format.
  }

  try {
    const payload = JSON.parse(raw) as { privateVpsUrl?: unknown; url?: unknown };
    const candidate = typeof payload.privateVpsUrl === "string"
      ? payload.privateVpsUrl
      : typeof payload.url === "string" ? payload.url : null;
    return normalizePrivateVpsUrl(candidate);
  } catch {
    return null;
  }
}
