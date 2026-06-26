// Owner-account check for owner-only experimental hardware cells (robot, arm,
// circuit, printer, Apple TV, capture). Mirrors backend/convex/ownerAllowlist.ts
// and the daemon-side mcp_owner_gate.go: env-driven, no personal email baked in.
// Default (no env) = nobody is owner = cells hidden (the simplified product).
//
// Kept as a tiny standalone util so owner-gated route pages don't have to pull
// in the heavy DevicesView module just to reuse isKivancAccount.
export function isOwnerAccount(email: string | null | undefined): boolean {
  const normalized = String(email || "").trim().toLowerCase();
  if (!normalized) return false;
  const raw =
    process.env.NEXT_PUBLIC_YAVER_OWNER_EMAIL ||
    process.env.NEXT_PUBLIC_YAVER_CLOUD_PREVIEW_EMAILS ||
    "";
  const allowed = raw
    .split(",")
    .map((item: string) => item.trim().toLowerCase())
    .filter(Boolean);
  if (allowed.length === 0) return false;
  return allowed.includes(normalized);
}
