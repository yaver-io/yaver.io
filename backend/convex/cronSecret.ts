import { v } from "convex/values";
import { internalAction } from "./_generated/server";

export const verify = internalAction({
  args: { token: v.string() },
  handler: async (_ctx, { token }) => {
    const secret = process.env.CRON_TRIGGER_SECRET ?? "";
    return { ok: secret.length > 0 && token === secret, secretConfigured: secret.length > 0 };
  },
});
