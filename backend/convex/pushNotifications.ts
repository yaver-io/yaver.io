import { mutation, internalQuery, action } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";
import { validateSessionInternal } from "./auth";

/**
 * Push channel for the device-auth approval flow (P2). A phone registers its
 * push token here; a remote box's re-auth can then ring it so the user
 * approves with Face ID (see mobile app/approve-device.tsx) instead of opening
 * a browser. Stores only a notification-routing id, never an auth token.
 *
 * Dormant until a transport exists: native builds have no EAS projectId yet,
 * so `getExpoPushTokenAsync` yields nothing and no rows are written → the
 * sender no-ops. Activating = give the app an EAS projectId (Expo push, no
 * APNs key needed) OR wire native APNs/FCM and store those tokens instead.
 */

export const registerPushToken = mutation({
  args: {
    tokenHash: v.string(),
    installId: v.string(),
    pushToken: v.string(),
    transport: v.string(), // "expo" | "apns" | "fcm"
    platform: v.string(), // "ios" | "android"
  },
  handler: async (ctx, args) => {
    const session = await validateSessionInternal(ctx, args.tokenHash);
    if (!session) throw new Error("Unauthorized");
    const fields = {
      userId: session.user._id,
      installId: args.installId,
      pushToken: args.pushToken,
      transport: args.transport,
      platform: args.platform,
      updatedAt: Date.now(),
    };
    // One row per phone install.
    const existing = await ctx.db
      .query("pushTokens")
      .withIndex("by_install", (q) => q.eq("installId", args.installId))
      .unique();
    if (existing) await ctx.db.patch(existing._id, fields);
    else await ctx.db.insert("pushTokens", fields);
    return { ok: true };
  },
});

export const tokensForUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, args) =>
    await ctx.db
      .query("pushTokens")
      .withIndex("by_user", (q) => q.eq("userId", args.userId))
      .collect(),
});

/**
 * Ring a user's phones to approve a device code. No-ops (dormant) when the
 * user has no registered push tokens — which is the case until the app ships
 * with a push transport. Uses the Expo Push API (Expo brokers APNs/FCM, so no
 * provider key is needed server-side); native tokens would dispatch the same
 * way through their own sender.
 */
export const sendDeviceAuthPush = action({
  args: {
    userId: v.id("users"),
    userCode: v.string(),
    machineName: v.optional(v.string()),
  },
  handler: async (ctx, args): Promise<{ sent: number; dormant?: boolean }> => {
    const rows = await ctx.runQuery(internal.pushNotifications.tokensForUser, {
      userId: args.userId,
    });
    const expo = (rows as Array<{ transport: string; pushToken: string }>)
      .filter((r: { transport: string; pushToken: string }) => r.transport === "expo")
      .map((r: { transport: string; pushToken: string }) => r.pushToken);
    if (expo.length === 0) {
      console.log(
        "[push] device-auth approval is dormant — no registered push tokens (configure an EAS projectId / native push to activate)",
      );
      return { sent: 0, dormant: true };
    }
    const messages = expo.map((to: string) => ({
      to,
      title: "Approve sign-in",
      body: `${args.machineName || "A machine"} wants to sign in. Tap to approve with Face ID.`,
      data: { type: "device_auth_request", userCode: args.userCode },
      sound: "default",
    }));
    try {
      const res = await fetch("https://exp.host/--/api/v2/push/send", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify(messages),
      });
      return { sent: res.ok ? expo.length : 0 };
    } catch (e) {
      console.error("[push] expo send failed", e);
      return { sent: 0 };
    }
  },
});
