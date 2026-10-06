export const PRIVATE_VPS_STORAGE_KEY = "yaver.privateVpsUrl";

export function normalizePrivateVpsUrl(value: string | null | undefined): string | null {
  const raw = (value || "").trim();
  if (!raw) return null;
  try {
    const url = new URL(raw);
    if (url.protocol !== "https:") return null;
    if (url.username || url.password || url.search || url.hash) return null;
    url.pathname = url.pathname.replace(/\/+$/, "");
    return url.toString().replace(/\/+$/, "");
  } catch { return null; }
}
export function privateVpsUrlFromQr(value: string): string | null {
  const direct = normalizePrivateVpsUrl(value);
  if (direct) return direct;
  try {
    const url = new URL(value);
    if (url.protocol === "yaver:" && url.hostname === "private-vps") {
      return normalizePrivateVpsUrl(url.searchParams.get("url"));
    }
  } catch { /* try JSON */ }
  try {
    const payload = JSON.parse(value) as { privateVpsUrl?: unknown; url?: unknown };
    return normalizePrivateVpsUrl(typeof payload.privateVpsUrl === "string" ? payload.privateVpsUrl : typeof payload.url === "string" ? payload.url : null);
  } catch { return null; }
}

export function storedPrivateVpsUrl(): string | null {
  if (typeof window === "undefined") return null;
  return normalizePrivateVpsUrl(window.localStorage.getItem(PRIVATE_VPS_STORAGE_KEY));
}
