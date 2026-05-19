// ownerAllowlist.ts — single source of truth for "is this email a
// Yaver owner / private-preview account?".
//
// The allowlist is a Convex ENV VAR (comma-separated emails), never a
// hardcoded literal in source: the repo is public and Yaver ships to
// every user, so owner identity must be runtime config, not code
// (see memory feedback_yaver_is_for_everyone). Unset env → returns
// false for everyone → the managed-cloud LemonSqueezy gate stays
// fully fail-closed by default. The owner opts themselves in by
// setting the env var in the Convex dashboard / `convex env set`.
//
// Used by: http.ts isCloudPreviewUser (dev-activate route) and the
// managed-provision billing gate (subscriptions.canProvisionManaged)
// so an owner can develop the full Hetzner create/remove flow without
// a LemonSqueezy subscription.

export function isOwnerEmail(email?: string | null): boolean {
  const normalized = (email ?? "").trim().toLowerCase();
  if (!normalized) return false;
  const raw =
    process.env.CLOUD_PREVIEW_OWNER_EMAIL ||
    process.env.YAVER_CLOUD_PREVIEW_EMAILS ||
    process.env.NEXT_PUBLIC_YAVER_CLOUD_PREVIEW_EMAILS ||
    "";
  const allowed = raw
    .split(",")
    .map((item) => item.trim().toLowerCase())
    .filter(Boolean);
  if (allowed.length === 0) return false;
  return allowed.includes(normalized);
}

// Owner by Convex userId — same env-config principle (never a
// hardcoded id). REQUIRED in practice because OAuth accounts
// (Apple/GitHub/GitLab) often have NO email, so an email-only
// allowlist can never match the owner's primary login. Set
// CLOUD_PREVIEW_OWNER_USER_IDS to comma-separated user _id values.
// Unset ⇒ false for everyone (stays fail-closed by default).
export function isOwnerUserId(userId?: string | null): boolean {
  const id = (userId ?? "").trim();
  if (!id) return false;
  const raw = process.env.CLOUD_PREVIEW_OWNER_USER_IDS || "";
  const allowed = raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);
  if (allowed.length === 0) return false;
  return allowed.includes(id);
}

// Combined owner check — email OR userId. Use everywhere the
// cloud-preview gate is applied so an emailless owner account works.
export function isOwner(
  email?: string | null,
  userId?: string | null,
): boolean {
  return isOwnerEmail(email) || isOwnerUserId(userId);
}
