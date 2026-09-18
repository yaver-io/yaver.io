import { mutation, internalQuery, internalAction } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";
import { validateSessionInternal } from "./auth";
import { SignJWT, importPKCS8 } from "jose";

type FirebaseServiceAccount = {
  client_email?: string;
  private_key?: string;
  project_id?: string;
  token_uri?: string;
};

let cachedFcmAccessToken: { value: string; expiresAt: number } | null = null;

function firebaseServiceAccount(): FirebaseServiceAccount {
  const raw = String(process.env.FIREBASE_SERVICE_ACCOUNT_JSON || "").trim();
  if (!raw) throw new Error("FIREBASE_SERVICE_ACCOUNT_JSON is not configured");
  try {
    const decoded = raw.startsWith("{")
      ? raw
      : new TextDecoder().decode(Uint8Array.from(atob(raw), (c) => c.charCodeAt(0)));
    return JSON.parse(decoded) as FirebaseServiceAccount;
  } catch {
    throw new Error("FIREBASE_SERVICE_ACCOUNT_JSON is invalid");
  }
}

async function fcmAccessToken(): Promise<{ token: string; projectId: string }> {
  const serviceAccount = firebaseServiceAccount();
  const projectId = String(serviceAccount.project_id || "").trim();
  if (!serviceAccount.client_email || !serviceAccount.private_key || !projectId) {
    throw new Error("Firebase service account is missing client_email, private_key, or project_id");
  }
  if (cachedFcmAccessToken && cachedFcmAccessToken.expiresAt > Date.now() + 5 * 60_000) {
    return { token: cachedFcmAccessToken.value, projectId };
  }
  const tokenUri = serviceAccount.token_uri || "https://oauth2.googleapis.com/token";
  const now = Math.floor(Date.now() / 1000);
  const key = await importPKCS8(serviceAccount.private_key.replace(/\\n/g, "\n"), "RS256");
  const assertion = await new SignJWT({ scope: "https://www.googleapis.com/auth/firebase.messaging" })
    .setProtectedHeader({ alg: "RS256", typ: "JWT" })
    .setIssuer(serviceAccount.client_email)
    .setSubject(serviceAccount.client_email)
    .setAudience(tokenUri)
    .setIssuedAt(now)
    .setExpirationTime(now + 3600)
    .sign(key);
  const response = await fetch(tokenUri, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      grant_type: "urn:ietf:params:oauth-grant-type:jwt-bearer",
      assertion,
    }).toString(),
  });
  if (!response.ok) throw new Error(`Firebase OAuth failed with HTTP ${response.status}`);
  const body = await response.json() as { access_token?: string; expires_in?: number };
  if (!body.access_token) throw new Error("Firebase OAuth returned no access token");
  cachedFcmAccessToken = {
    value: body.access_token,
    expiresAt: Date.now() + (body.expires_in || 3600) * 1000,
  };
  return { token: body.access_token, projectId };
}

async function sendFcmMessage(pushToken: string, message: {
  title: string;
  body: string;
  data: Record<string, string>;
}): Promise<boolean> {
  const auth = await fcmAccessToken();
  const response = await fetch(
    `https://fcm.googleapis.com/v1/projects/${encodeURIComponent(auth.projectId)}/messages:send`,
    {
      method: "POST",
      headers: { Authorization: `Bearer ${auth.token}`, "Content-Type": "application/json" },
      body: JSON.stringify({
        message: {
          token: pushToken,
          notification: { title: message.title, body: message.body },
          data: message.data,
          android: {
            priority: "HIGH",
            notification: { channel_id: "yaver-task-review", sound: "default" },
          },
        },
      }),
    },
  );
  if (!response.ok) {
    console.error(`[push] FCM send failed with HTTP ${response.status}`);
    return false;
  }
  return true;
}

/**
 * Push channel for the device-auth approval flow (P2). A phone registers its
 * push token here; a remote box's re-auth can then ring it so the user
 * approves with Face ID (see mobile app/approve-device.tsx) instead of opening
 * a browser. Stores only a notification-routing id, never an auth token.
 *
 * Android registers its native FCM token. iOS may register an Expo token when
 * an EAS project id is configured. The backend dispatches by transport and
 * never stores an auth/session token here.
 */

export const registerPushToken = mutation({
  args: {
    tokenHash: v.string(),
    installId: v.string(),
    pushToken: v.string(),
    transport: v.string(), // "expo" | "apns" | "fcm"
    platform: v.string(), // "ios" | "android"
    appVersion: v.optional(v.string()),
    buildNumber: v.optional(v.string()),
    runtimeMode: v.optional(v.string()),
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
      appVersion: args.appVersion,
      buildNumber: args.buildNumber,
      runtimeMode: args.runtimeMode,
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
 * Ring a user's phones to approve a device code. No-ops when the user has no
 * registered push tokens. Expo and native Android/FCM transports may coexist.
 */
export const sendDeviceAuthPush = internalAction({
  args: {
    userId: v.id("users"),
    userCode: v.string(),
    machineName: v.optional(v.string()),
  },
  handler: async (ctx, args): Promise<{ sent: number; dormant?: boolean }> => {
    const rows = await ctx.runQuery(internal.pushNotifications.tokensForUser, {
      userId: args.userId,
    });
    const typedRows = rows as Array<{ transport: string; pushToken: string }>;
    const expo = typedRows
      .filter((r: { transport: string; pushToken: string }) => r.transport === "expo")
      .map((r: { transport: string; pushToken: string }) => r.pushToken);
    const fcm = typedRows
      .filter((r) => r.transport === "fcm")
      .map((r) => r.pushToken);
    if (expo.length === 0 && fcm.length === 0) {
      console.log("[push] device-auth approval has no registered push tokens");
      return { sent: 0, dormant: true };
    }
    const messages = expo.map((to: string) => ({
      to,
      title: "Approve sign-in",
      body: `${args.machineName || "A machine"} wants to sign in. Tap to approve with Face ID.`,
      data: { type: "device_auth_request", userCode: args.userCode },
      sound: "default",
    }));
    let sent = 0;
    if (messages.length > 0) {
      try {
        const res = await fetch("https://exp.host/--/api/v2/push/send", {
          method: "POST",
          headers: { "Content-Type": "application/json", Accept: "application/json" },
          body: JSON.stringify(messages),
        });
        if (res.ok) sent += expo.length;
      } catch (e) {
        console.error("[push] expo send failed", e);
      }
    }
    for (const pushToken of fcm) {
      try {
        if (await sendFcmMessage(pushToken, {
          title: "Approve sign-in",
          body: `${args.machineName || "A machine"} wants to sign in. Tap to approve.`,
          data: { type: "device_auth_request", userCode: args.userCode },
        })) sent += 1;
      } catch (e) {
        console.error("[push] FCM device-auth send failed", e);
      }
    }
    return { sent };
  },
});

/**
 * Notify native Android while React Native is suspended. Only opaque ids and
 * lifecycle state cross the push service; prompts, output, paths, and project
 * names stay on the user's machine.
 */
export const sendTaskLifecyclePush = internalAction({
  args: {
    userId: v.id("users"),
    deviceId: v.string(),
    taskId: v.string(),
    status: v.union(v.literal("review"), v.literal("completed"), v.literal("failed")),
  },
  handler: async (ctx, args): Promise<{ sent: number; dormant?: boolean }> => {
    const rows = await ctx.runQuery(internal.pushNotifications.tokensForUser, {
      userId: args.userId,
    });
    const fcm = (rows as Array<{ transport: string; pushToken: string }>)
      .filter((row) => row.transport === "fcm")
      .map((row) => row.pushToken);
    if (fcm.length === 0) return { sent: 0, dormant: true };

    const copy = args.status === "failed"
      ? { title: "Task needs attention", body: "Open Yaver to review what happened." }
      : args.status === "review"
        ? { title: "Task ready for review", body: "Open Yaver to review the result." }
        : { title: "Task completed", body: "Open Yaver to view the result." };
    let sent = 0;
    for (const pushToken of fcm) {
      try {
        if (await sendFcmMessage(pushToken, {
          ...copy,
          data: {
            kind: "task-review",
            taskId: args.taskId,
            deviceId: args.deviceId,
            status: args.status,
          },
        })) sent += 1;
      } catch (e) {
        console.error("[push] FCM task lifecycle send failed", e);
      }
    }
    return { sent };
  },
});
