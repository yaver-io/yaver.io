const FORWARDED_REQ_HEADERS = [
  "accept",
  "accept-language",
  "cache-control",
  "content-type",
  "if-modified-since",
  "if-none-match",
  "origin",
  "pragma",
  "range",
  "referer",
  "user-agent",
] as const;

export function previewDeviceProxyHeaders(
  requestHeaders: Headers,
  authToken: string,
  relayPassword: string,
): Headers {
  const headers = new Headers();
  for (const name of FORWARDED_REQ_HEADERS) {
    const value = requestHeaders.get(name);
    if (value) headers.set(name, value);
  }
  headers.set("Authorization", `Bearer ${authToken}`);
  if (relayPassword) headers.set("X-Relay-Password", relayPassword);
  return headers;
}
