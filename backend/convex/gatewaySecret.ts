// Shared-secret guard for the Yaver Gateway → Convex trust boundary.
//
// The gateway (Cloudflare Worker / relay) is the ONLY caller allowed to
// assert "user X consumed Y cents of inference" (POST /gateway/meter).
// That call carries an arbitrary userId + cost, so it must be
// gateway-authenticated, NOT user-authenticated. Mirrors cronSecret.ts.
//
// `GATEWAY_SHARED_SECRET` is a Convex env var (set on the Convex
// deployment) that ALSO lives as a Worker secret. If it's unset the
// meter route fail-closes (500) — same dormant posture as the rest of
// Yaver Premium. The user's own /gateway/authorize call does NOT use
// this; it uses the normal bearer-session path.

import { v } from "convex/values";
import { internalAction } from "./_generated/server";

export const verify = internalAction({
  args: { token: v.string() },
  handler: async (_ctx, { token }) => {
    const secret = process.env.GATEWAY_SHARED_SECRET ?? "";
    return {
      ok: secret.length > 0 && token === secret,
      secretConfigured: secret.length > 0,
    };
  },
});
