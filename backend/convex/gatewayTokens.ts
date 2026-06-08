// gatewayTokens.ts — scoped, rotatable inference-only tokens for the
// Yaver Gateway.
//
// A free-tier tenant's runner authenticates to the gateway with one of
// THESE (injected as OPENAI_API_KEY on the operator box), never a Yaver
// session token. Properties that make this the safe key path:
//   • Stored as sha256(raw); the raw value is shown ONCE at mint.
//   • Scope is "inference": resolving one yields a userId for the gateway
//     ONLY. It is never accepted by authenticateRequest, so a leaked token
//     cannot read projects/vault/billing or anything else — it can only
//     spend inference for that one user, within their gatewayPolicy caps.
//   • Rotatable + revocable by the OPERATOR (rotation = revoke old + mint
//     new). Key rotation is a first-class protection mechanism here.
//   • Never user-mutable: mint/rotate/revoke are gated by isOwner in the
//     HTTP route; the tenant has no endpoint that creates or changes one.

import { v } from "convex/values";
import { internalQuery, internalMutation } from "./_generated/server";

// Resolve a presented gateway token (by its hash) to a userId, or null if
// unknown / revoked / expired. Bumps lastUsedAt opportunistically is NOT
// done here (queries can't write) — the authorize route does that async.
export const resolveInternal = internalQuery({
  args: { tokenHash: v.string(), now: v.optional(v.number()) },
  handler: async (ctx, { tokenHash, now }) => {
    const row = await ctx.db
      .query("gatewayTokens")
      .withIndex("by_hash", (q) => q.eq("tokenHash", tokenHash))
      .unique();
    if (!row) return null;
    if (row.revokedAt) return null;
    const t = now ?? Date.now();
    if (row.expiresAt && row.expiresAt <= t) return null;
    return { tokenId: String(row._id), userId: String(row.userId), scope: row.scope };
  },
});

export const touchInternal = internalMutation({
  args: { tokenId: v.id("gatewayTokens"), now: v.optional(v.number()) },
  handler: async (ctx, { tokenId, now }) => {
    const row = await ctx.db.get(tokenId);
    if (row) await ctx.db.patch(tokenId, { lastUsedAt: now ?? Date.now() });
  },
});

// Operator-only (gated by isOwner in the HTTP route). Stores the HASH; the
// caller passes a pre-hashed value (http.ts hashes the freshly-generated
// raw token and returns the raw to the operator once).
export const mintInternal = internalMutation({
  args: {
    userId: v.id("users"),
    tokenHash: v.string(),
    scope: v.optional(v.string()),
    label: v.optional(v.string()),
    createdBy: v.optional(v.string()),
    expiresAt: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const id = await ctx.db.insert("gatewayTokens", {
      userId: args.userId,
      tokenHash: args.tokenHash,
      scope: args.scope ?? "inference",
      label: args.label,
      createdBy: args.createdBy,
      createdAt: Date.now(),
      expiresAt: args.expiresAt,
    });
    return { ok: true, tokenId: String(id) };
  },
});

export const revokeInternal = internalMutation({
  args: { tokenId: v.id("gatewayTokens") },
  handler: async (ctx, { tokenId }) => {
    const row = await ctx.db.get(tokenId);
    if (!row) return { ok: false, error: "not found" };
    if (!row.revokedAt) await ctx.db.patch(tokenId, { revokedAt: Date.now() });
    return { ok: true, tokenId: String(tokenId) };
  },
});

// Revoke EVERY active token for a user — used by rotation (revoke-all then
// mint a fresh one) and by an operator kill-switch.
export const revokeAllForUserInternal = internalMutation({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const rows = await ctx.db
      .query("gatewayTokens")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    const now = Date.now();
    let revoked = 0;
    for (const r of rows) {
      if (!r.revokedAt) {
        await ctx.db.patch(r._id, { revokedAt: now });
        revoked++;
      }
    }
    return { ok: true, revoked };
  },
});

export const listForUserInternal = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const rows = await ctx.db
      .query("gatewayTokens")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    // Never return the hash; only non-secret metadata.
    return rows.map((r) => ({
      tokenId: String(r._id),
      scope: r.scope,
      label: r.label ?? null,
      createdAt: r.createdAt,
      expiresAt: r.expiresAt ?? null,
      revokedAt: r.revokedAt ?? null,
      lastUsedAt: r.lastUsedAt ?? null,
    }));
  },
});
