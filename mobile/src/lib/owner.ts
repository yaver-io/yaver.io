// Owner-account check for owner-only experimental hardware cells (robot, arm,
// circuit, printer, screw cell). Mirrors the web lib/owner.ts and the
// daemon-side mcp_owner_gate.go: env-driven, no personal email baked into this
// public repo. The owner sets EXPO_PUBLIC_YAVER_OWNER_EMAILS at build time
// (Expo inlines EXPO_PUBLIC_* vars). Default (no env) = nobody is owner = the
// cells stay hidden — the simplified product.
export function isOwnerAccount(email: string | null | undefined): boolean {
  const normalized = String(email || "").trim().toLowerCase();
  if (!normalized) return false;
  const raw =
    process.env.EXPO_PUBLIC_YAVER_OWNER_EMAILS ||
    process.env.EXPO_PUBLIC_YAVER_OWNER_EMAIL ||
    "";
  const allowed = raw
    .split(",")
    .map((item: string) => item.trim().toLowerCase())
    .filter(Boolean);
  if (allowed.length === 0) return false;
  return allowed.includes(normalized);
}
