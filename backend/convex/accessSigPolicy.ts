// accessSigPolicy.ts — the relay signature-reach rule, as a pure function.
//
// Deliberately dependency-free (no convex imports, no db types) for two
// reasons: the rule is the part that must never drift, and a module with no
// imports can actually be executed by `node --experimental-strip-types --test`.
// Sibling policy that lives inside access.ts cannot be unit-tested at all —
// see accessSigPolicy.test.mts.
//
// Incident this encodes (2026-07-23): the relay's asymmetric-auth resolver
// allowed a signed request only when ONE account owned both the signing device
// and the target device. Every cross-account session — guest, host-share,
// project-share, support link — therefore failed the signature path and fell
// through to the LEGACY shared-password path. Nothing was broken, so nothing
// was noticed; but /authmix could not distinguish "never migrated" from
// "structurally cannot migrate", and the plan of record is to switch the
// password off once that metric looks clean. Doing so would have severed every
// cross-account session simultaneously, and the symptom would have read as
// "the relay is down" rather than "guests lost auth".

/**
 * Why the relay may carry a signed request from signer → target.
 *
 *   "same-owner"   — one user's own mesh (my phone → my Mac). Always allowed.
 *   "access-graph" — two DIFFERENT users linked by an ACTIVE infraAccessGrant
 *                    that covers this specific target device.
 *   "deny"         — no relationship; the caller falls back to the password path.
 */
export type SigReachReason = "same-owner" | "access-graph" | "deny";

export function resolveSigReach(opts: {
  signerUserId: string;
  targetUserId: string;
  /**
   * Whether an active grant links these two accounts AND covers the target
   * device. Computed by the caller (guestCanReachSpecificHostDevice), because
   * device-level narrowing must be honored: a grant scoped to one machine must
   * not open the owner's whole fleet.
   */
  grantCoversTarget: boolean;
}): SigReachReason {
  const signer = (opts.signerUserId ?? "").trim();
  const target = (opts.targetUserId ?? "").trim();
  // An empty id on either side is never a match. Without this, two unresolved
  // devices would compare equal and read as "same owner" — turning a Convex
  // hiccup into an open relay.
  if (!signer || !target) return "deny";
  if (signer === target) return "same-owner";
  return opts.grantCoversTarget ? "access-graph" : "deny";
}
