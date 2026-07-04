import { internalAction, internalMutation, internalQuery, mutation, query } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";
import { sha256Hex, validateSessionInternal } from "./auth";
import type { Id } from "./_generated/dataModel";

const DEFAULT_ACTIONS = ["task", "status", "reload"];
const MAX_ACTIONS = new Set(["task", "status", "reload", "build_reload"]);

function normalizeActions(actions?: string[]): string[] {
  const out = (actions && actions.length ? actions : DEFAULT_ACTIONS)
    .map((a) => a.trim().toLowerCase())
    .filter((a) => MAX_ACTIONS.has(a));
  return Array.from(new Set(out.length ? out : DEFAULT_ACTIONS));
}

function normalizePhone(phone: string): string {
  return phone.replace(/[^\d]/g, "");
}

function newJoinCode(): string {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const bytes = new Uint8Array(8);
  crypto.getRandomValues(bytes);
  let out = "";
  for (const b of bytes) out += alphabet[b % alphabet.length];
  return out;
}

async function sessionUser(ctx: any, tokenHash: string): Promise<Id<"users">> {
  const session = await validateSessionInternal(ctx, tokenHash);
  if (!session) throw new Error("Unauthorized");
  return session.user._id;
}

export const createInvite = mutation({
  args: {
    tokenHash: v.string(),
    targetDeviceId: v.string(),
    projectSlug: v.optional(v.string()),
    allowedActions: v.optional(v.array(v.string())),
    ttlHours: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const userId = await sessionUser(ctx, args.tokenHash);
    const device = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .collect()
      .then((rows) => rows.find((d) => d.deviceId === args.targetDeviceId));
    if (!device) throw new Error("target device not found for this account");

    const code = newJoinCode();
    const codeHash = await sha256Hex(code.toLowerCase());
    const now = Date.now();
    const ttlMs = Math.max(1, Math.min(args.ttlHours ?? 72, 24 * 14)) * 60 * 60 * 1000;
    await ctx.db.insert("whatsappInvites", {
      userId,
      codeHash,
      targetDeviceId: args.targetDeviceId,
      projectSlug: args.projectSlug?.trim() || undefined,
      allowedActions: normalizeActions(args.allowedActions),
      status: "active",
      createdAt: now,
      expiresAt: now + ttlMs,
    });
    const yaverNumber = (process.env.WHATSAPP_PUBLIC_NUMBER || "").replace(/[^\d]/g, "");
    const joinText = `join ${code}`;
    return {
      ok: true,
      code,
      joinText,
      waLink: yaverNumber ? `https://wa.me/${yaverNumber}?text=${encodeURIComponent(joinText)}` : null,
      expiresAt: now + ttlMs,
    };
  },
});

export const myContacts = query({
  args: { tokenHash: v.string() },
  handler: async (ctx, { tokenHash }) => {
    const userId = await sessionUser(ctx, tokenHash);
    const rows = await ctx.db
      .query("whatsappContacts")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    return rows.map((r) => ({
      _id: r._id,
      targetDeviceId: r.targetDeviceId,
      projectSlug: r.projectSlug,
      allowedActions: r.allowedActions,
      displayName: r.displayName ?? "",
      status: r.status,
      createdAt: r.createdAt,
      updatedAt: r.updatedAt,
    }));
  },
});

export const bindJoinCode = internalMutation({
  args: { phone: v.string(), code: v.string(), displayName: v.optional(v.string()) },
  handler: async (ctx, { phone, code, displayName }) => {
    const phoneHash = await sha256Hex(normalizePhone(phone));
    const codeHash = await sha256Hex(code.trim().toLowerCase());
    const invite = await ctx.db
      .query("whatsappInvites")
      .withIndex("by_codeHash", (q) => q.eq("codeHash", codeHash))
      .first();
    const now = Date.now();
    if (!invite || invite.status !== "active" || invite.expiresAt <= now) {
      return { ok: false, reason: "invalid_or_expired" };
    }
    const existing = await ctx.db
      .query("whatsappContacts")
      .withIndex("by_user_phone", (q) => q.eq("userId", invite.userId).eq("phoneHash", phoneHash))
      .first();
    const patch = {
      userId: invite.userId,
      phoneHash,
      targetDeviceId: invite.targetDeviceId,
      projectSlug: invite.projectSlug,
      allowedActions: invite.allowedActions,
      displayName: displayName?.trim() || undefined,
      status: "active" as const,
      updatedAt: now,
    };
    if (existing) {
      await ctx.db.patch(existing._id, patch);
    } else {
      await ctx.db.insert("whatsappContacts", { ...patch, createdAt: now });
    }
    return { ok: true, userId: String(invite.userId), projectSlug: invite.projectSlug };
  },
});

export const resolveContact = internalQuery({
  args: { phone: v.string() },
  handler: async (ctx, { phone }) => {
    const phoneHash = await sha256Hex(normalizePhone(phone));
    const rows = await ctx.db
      .query("whatsappContacts")
      .withIndex("by_phoneHash", (q) => q.eq("phoneHash", phoneHash))
      .collect();
    const active = rows.find((r) => r.status === "active");
    if (!active) return null;
    return {
      userId: active.userId as Id<"users">,
      phoneHash,
      targetDeviceId: active.targetDeviceId,
      projectSlug: active.projectSlug,
      allowedActions: active.allowedActions,
    };
  },
});

export const receiptStart = internalMutation({
  args: {
    userId: v.id("users"),
    phoneHash: v.string(),
    waMessageIdHash: v.string(),
    targetDeviceId: v.string(),
    projectSlug: v.optional(v.string()),
    action: v.string(),
  },
  handler: async (ctx, args) => {
    const dup = await ctx.db
      .query("whatsappCommandReceipts")
      .withIndex("by_waMessageIdHash", (q) => q.eq("waMessageIdHash", args.waMessageIdHash))
      .first();
    if (dup) return { ok: false, duplicate: true, receiptId: dup._id };
    const receiptId = await ctx.db.insert("whatsappCommandReceipts", {
      ...args,
      status: "received",
      createdAt: Date.now(),
    });
    return { ok: true, duplicate: false, receiptId };
  },
});

export const receiptFinish = internalMutation({
  args: {
    receiptId: v.id("whatsappCommandReceipts"),
    status: v.string(),
    taskId: v.optional(v.string()),
    errorCode: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    await ctx.db.patch(args.receiptId, {
      status: args.status,
      taskId: args.taskId,
      errorCode: args.errorCode,
      deliveredAt: Date.now(),
    });
  },
});

export const deviceEndpoint = internalQuery({
  args: { userId: v.id("users"), deviceId: v.string() },
  handler: async (ctx, { userId, deviceId }): Promise<string | null> => {
    const rows = await ctx.db
      .query("devices")
      .withIndex("by_userId", (q) => q.eq("userId", userId))
      .collect();
    const device = rows.find((d) => d.deviceId === deviceId);
    if (!device) return null;
    const endpoints = (device.publicEndpoints ?? []).filter((e) => /^https?:\/\//i.test(e));
    return endpoints.length ? endpoints[0].replace(/\/+$/, "") : null;
  },
});

export const sendText = internalAction({
  args: { to: v.string(), body: v.string() },
  handler: async (_ctx, { to, body }) => {
    const token = (process.env.WHATSAPP_ACCESS_TOKEN || "").trim();
    const phoneNumberId = (process.env.WHATSAPP_PHONE_NUMBER_ID || "").trim();
    if (!token || !phoneNumberId) return { ok: false, error: "whatsapp_not_configured" };
    const r = await fetch(`https://graph.facebook.com/v21.0/${phoneNumberId}/messages`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
      body: JSON.stringify({
        messaging_product: "whatsapp",
        to,
        type: "text",
        text: { body: body.slice(0, 4000) },
      }),
    });
    return { ok: r.ok, status: r.status };
  },
});
