/** Normalize only absolute browser-safe agent tunnel origins. */
export function normalizeTunnelEndpoint(raw: string): string | null {
  const value = raw.trim();
  if (!value) return null;
  try {
    const url = new URL(value);
    if (url.protocol !== "https:" && url.protocol !== "http:") return null;
    if (url.username || url.password || !url.hostname) return null;
    if (url.pathname !== "/" || url.search || url.hash) return null;
    if (url.protocol === "http:" && !isPrivateOrLoopbackHost(url.hostname)) return null;
    return url.origin;
  } catch {
    // Raw IPv4/IPv6 addresses and relative paths belong to the direct-candidate
    // lane. Passing one to fetch() in a browser resolves it below Metro's
    // current origin and can false-green on the dev server's HTML fallback.
    return null;
  }
}

function isPrivateOrLoopbackHost(hostname: string): boolean {
  const host = hostname.replace(/^\[|\]$/g, "").toLowerCase();
  return host === "localhost" || host === "::1" || host.startsWith("127.") || host.startsWith("10.") ||
    /^192\.168\./.test(host) || /^172\.(1[6-9]|2\d|3[0-1])\./.test(host) ||
    /^100\.(6[4-9]|[7-9]\d|1[0-1]\d|12[0-7])\./.test(host) ||
    /^(?:fc|fd|fe8|fe9|fea|feb)[0-9a-f]*:/.test(host);
}
