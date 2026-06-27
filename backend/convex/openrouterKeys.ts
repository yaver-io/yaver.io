// openrouterKeys.ts — per-user OpenRouter API key lifecycle for the
// hosted "Cloud Agent" tier.
//
// WHY PER-USER KEYS (not one shared upstream key):
//   1. Rate limits. A single GLM/OpenRouter key throttles (429s) under
//      many concurrent paying users. OpenRouter rate-limits PER KEY, so a
//      key-per-user spreads load across OpenRouter's GLM provider pool —
//      no shared key to starve.
//   2. Margin lock. Each key carries its OWN hard credit `limit`, set to
//      our COGS BUDGET — NOT what the user paid. A $19 Cloud Agent seat
//      gets ~$5.33 of OpenRouter credit (the $8 retail AI wallet ÷ the
//      1.5x inference markup). The user can never cause more than ~$5.33
//      of OpenRouter spend, so "once their credit is gone we've already
//      made money" holds at the PROVIDER level, not just in our wallet.
//   3. Per-user accounting. OpenRouter tracks usage_monthly per key, so
//      we get clean per-user inference cost with zero extra plumbing.
//
// SECURITY (this is an open-source repo):
//   • The raw `sk-or-v1-...` secret is returned by OpenRouter ONCE at
//     mint and is pushed straight to the gateway Worker's KV. It is NEVER
//     written to Convex (privacy contract: no raw tokens/keys in Convex).
//     This module stores only OpenRouter's `hash` (a management id) + the
//     non-secret name/limit/status.
//   • Every export here is internal-only (internalAction / internalMutation
//     / internalQuery). There is NO client-callable surface — a tenant
//     cannot mint a key, raise their limit, or read another user's row.
//   • The provisioning credential (OPENROUTER_PROVISIONING_KEY) and the
//     gateway push secret (GATEWAY_SHARED_SECRET) are server-side env
//     only, never in code or the client bundle.
//   • Fail-soft: if the provisioning key or gateway URL is unset (local
//     dev / not-yet-configured), we log and no-op rather than throw, so
//     plan activation never breaks. The user simply has no managed key
//     until configured — they cannot be billed for inference they can't run.

import { internalAction, internalMutation, internalQuery } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";

const OPENROUTER_KEYS_URL = "https://openrouter.ai/api/v1/keys";

// Inference markup must match managedMeter.MARKUP_BY_KIND.inference so the
// OpenRouter limit (COGS) and the retail wallet ($8) hit zero together.
// Env-tunable (same key managedMeter reads) so a margin retune moves both.
function inferenceMarkup(): number {
  const env = Number(process.env.YAVER_MANAGED_MARKUP_INFERENCE);
  if (Number.isFinite(env) && env > 0) return env;
  return 1.5;
}

// COGS budget (cents) for a given retail AI wallet. This is the OpenRouter
// key limit: the most third-party inference spend a seat can ever incur.
// Margin is ALREADY removed here — we never hand the user a key worth what
// they paid us. A hard floor/ceiling guards against a misconfigured wallet.
export function openrouterLimitCents(monthlyWalletCents: number): number {
  const override = Number(process.env.YAVER_OPENROUTER_LIMIT_CENTS);
  if (Number.isFinite(override) && override >= 0) return Math.floor(override);
  // +1 cent of slack so OpenRouter's cap never trips a hair BEFORE our
  // wallet gate (which is the precise, markup-aware constraint).
  return Math.max(0, Math.ceil(monthlyWalletCents / inferenceMarkup()) + 1);
}

// ── Convex-side metadata (no secrets) ───────────────────────────────

export const getByUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    return await ctx.db
      .query("openrouterKeys")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
  },
});

export const upsertMeta = internalMutation({
  args: {
    userId: v.id("users"),
    orHash: v.string(),
    name: v.string(),
    limitCents: v.number(),
    status: v.string(),
  },
  handler: async (ctx, { userId, orHash, name, limitCents, status }) => {
    const now = Date.now();
    const existing = await ctx.db
      .query("openrouterKeys")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
    if (existing) {
      await ctx.db.patch(existing._id, {
        orHash,
        name,
        limitCents,
        status,
        updatedAt: now,
        ...(status === "active" ? { disabledAt: undefined } : {}),
      });
      return existing._id;
    }
    return await ctx.db.insert("openrouterKeys", {
      userId,
      orHash,
      name,
      limitCents,
      status,
      createdAt: now,
      updatedAt: now,
    });
  },
});

export const patchMeta = internalMutation({
  args: {
    userId: v.id("users"),
    limitCents: v.optional(v.number()),
    status: v.optional(v.string()),
    usageCents: v.optional(v.number()),
    markSynced: v.optional(v.boolean()),
  },
  handler: async (ctx, { userId, limitCents, status, usageCents, markSynced }) => {
    const row = await ctx.db
      .query("openrouterKeys")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .unique();
    if (!row) return;
    const now = Date.now();
    await ctx.db.patch(row._id, {
      ...(typeof limitCents === "number" ? { limitCents } : {}),
      ...(status ? { status, ...(status === "disabled" ? { disabledAt: now } : {}) } : {}),
      ...(typeof usageCents === "number" ? { usageCents } : {}),
      ...(markSynced ? { lastSyncedAt: now } : {}),
      updatedAt: now,
    });
  },
});

// ── Gateway KV push (raw key lives ONLY in the Worker) ──────────────
// Posts the raw key to the Worker's authenticated /admin/orkey route,
// which writes it to the OR_USER_KEYS KV namespace keyed by userId. The
// route NEVER reads keys back. key=null deletes the KV entry. Fail-soft:
// an unset gateway URL just means the key isn't usable yet (logged).
async function pushKeyToGateway(userId: string, key: string | null): Promise<boolean> {
  const base = process.env.YAVER_GATEWAY_URL;
  const secret = process.env.GATEWAY_SHARED_SECRET;
  if (!base || !secret) {
    console.warn("openrouterKeys: YAVER_GATEWAY_URL/GATEWAY_SHARED_SECRET unset — KV push skipped");
    return false;
  }
  try {
    const r = await fetch(`${base.replace(/\/$/, "")}/admin/orkey`, {
      method: "POST",
      headers: { authorization: `Bearer ${secret}`, "content-type": "application/json" },
      body: JSON.stringify({ userId, key }),
    });
    if (!r.ok) {
      console.error("openrouterKeys: gateway KV push failed", r.status);
      return false;
    }
    return true;
  } catch (e) {
    console.error("openrouterKeys: gateway KV push threw", String(e));
    return false;
  }
}

// ── Provisioning actions (internal-only, operator/plan-activation path) ─

// Mint (or reconcile) the hosted seat's OpenRouter key. Idempotent:
//   • no row yet  → create a key (limit = COGS budget, monthly reset),
//                   store metadata, push raw key to gateway KV.
//   • row exists  → keep it; PATCH the limit if the wallet/markup changed
//                   (the raw key is already in KV — no re-push needed).
// Called from plans.applyPlanEntitlements for tier "hosted".
export const ensureForUser = internalAction({
  args: { userId: v.id("users"), monthlyWalletCents: v.number() },
  handler: async (ctx, { userId, monthlyWalletCents }): Promise<{ ok: boolean; reason?: string }> => {
    const prov = process.env.OPENROUTER_PROVISIONING_KEY;
    if (!prov) {
      console.warn("openrouterKeys.ensureForUser: OPENROUTER_PROVISIONING_KEY unset — no-op");
      return { ok: false, reason: "no-provisioning-key" };
    }
    const limitCents = openrouterLimitCents(monthlyWalletCents);
    const limitDollars = Number((limitCents / 100).toFixed(2));

    const existing = await ctx.runQuery(internal.openrouterKeys.getByUser, { userId });
    if (existing && existing.status === "active") {
      // Reconcile the limit (a margin/plan retune) without re-minting.
      if (existing.limitCents !== limitCents) {
        try {
          await fetch(`${OPENROUTER_KEYS_URL}/${existing.orHash}`, {
            method: "PATCH",
            headers: { authorization: `Bearer ${prov}`, "content-type": "application/json" },
            body: JSON.stringify({ limit: limitDollars, disabled: false }),
          });
          await ctx.runMutation(internal.openrouterKeys.patchMeta, { userId, limitCents, status: "active" });
        } catch (e) {
          console.error("openrouterKeys.ensureForUser: limit reconcile failed", String(e));
        }
      }
      return { ok: true };
    }

    // Create a fresh key. limit_reset:"monthly" → the cap refills each
    // month at 00:00 UTC, so the key never permanently dies mid-service;
    // our wallet remains the precise per-period gate.
    let key: string | null = null;
    let orHash = "";
    try {
      const r = await fetch(OPENROUTER_KEYS_URL, {
        method: "POST",
        headers: { authorization: `Bearer ${prov}`, "content-type": "application/json" },
        body: JSON.stringify({
          name: `yaver-user-${userId}`,
          limit: limitDollars,
          limit_reset: "monthly",
        }),
      });
      if (!r.ok) {
        console.error("openrouterKeys.ensureForUser: create failed", r.status);
        return { ok: false, reason: `create-${r.status}` };
      }
      const body = (await r.json()) as any;
      key = body?.key ?? null;
      orHash = body?.data?.hash ?? "";
    } catch (e) {
      console.error("openrouterKeys.ensureForUser: create threw", String(e));
      return { ok: false, reason: "create-threw" };
    }
    if (!key || !orHash) return { ok: false, reason: "no-key-in-response" };

    await ctx.runMutation(internal.openrouterKeys.upsertMeta, {
      userId,
      orHash,
      name: `yaver-user-${userId}`,
      limitCents,
      status: "active",
    });
    // Push the secret to the gateway and immediately drop our reference.
    await pushKeyToGateway(userId as unknown as string, key);
    key = null;
    return { ok: true };
  },
});

// Disable the seat's managed inference at the PROVIDER: PATCH disabled:true
// (usage history preserved) + drop the raw key from gateway KV. Called on
// downgrade (hosted→byok), cancel, and expiry. Idempotent — a missing row
// or unset provisioning key is a successful no-op.
export const disableForUser = internalAction({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }): Promise<{ ok: boolean }> => {
    const row = await ctx.runQuery(internal.openrouterKeys.getByUser, { userId });
    // Always drop the gateway KV entry first — that alone stops all
    // managed inference for this user even if the OpenRouter PATCH fails.
    await pushKeyToGateway(userId as unknown as string, null);
    if (!row) return { ok: true };

    const prov = process.env.OPENROUTER_PROVISIONING_KEY;
    if (prov && row.status !== "disabled") {
      try {
        await fetch(`${OPENROUTER_KEYS_URL}/${row.orHash}`, {
          method: "PATCH",
          headers: { authorization: `Bearer ${prov}`, "content-type": "application/json" },
          body: JSON.stringify({ disabled: true }),
        });
      } catch (e) {
        console.error("openrouterKeys.disableForUser: PATCH threw", String(e));
      }
    }
    await ctx.runMutation(internal.openrouterKeys.patchMeta, { userId, status: "disabled" });
    return { ok: true };
  },
});

// Sync per-key OpenRouter usage into the metadata mirror (for the
// dashboard's per-user inference-credit display). Read-only on OpenRouter.
export const syncUsageForUser = internalAction({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }): Promise<{ ok: boolean; usageCents?: number; limitCents?: number }> => {
    const prov = process.env.OPENROUTER_PROVISIONING_KEY;
    const row = await ctx.runQuery(internal.openrouterKeys.getByUser, { userId });
    if (!prov || !row) return { ok: false };
    try {
      const r = await fetch(`${OPENROUTER_KEYS_URL}/${row.orHash}`, {
        headers: { authorization: `Bearer ${prov}` },
      });
      if (!r.ok) return { ok: false };
      const body = (await r.json()) as any;
      const d = body?.data ?? {};
      const usageCents = Math.round(Number(d.usage_monthly ?? d.usage ?? 0) * 100);
      const limitCents = d.limit != null ? Math.round(Number(d.limit) * 100) : row.limitCents;
      await ctx.runMutation(internal.openrouterKeys.patchMeta, {
        userId,
        usageCents,
        limitCents,
        markSynced: true,
      });
      return { ok: true, usageCents, limitCents };
    } catch (e) {
      console.error("openrouterKeys.syncUsageForUser: threw", String(e));
      return { ok: false };
    }
  },
});
