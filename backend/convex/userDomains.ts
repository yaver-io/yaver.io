import { v } from "convex/values";
import { mutation, query, internalMutation, internalAction, internalQuery } from "./_generated/server";
import { internal } from "./_generated/api";

// Mapping of user-owned custom domains (myapp.com) to either a managed
// relay, a cloudMachines row, or a custom server IP. Separate from the
// auto-generated <shortId>.cloud.yaver.io / <shortId>.relay.yaver.io we
// create inside the yaver.io zone — that one is always provisioned, this
// one is a user-bring-your-own overlay on top.

// ─── Public queries ─────────────────────────────────────────────

export const listForUser = internalQuery({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) =>
    ctx.db.query("userDomains").withIndex("by_user", (q) => q.eq("userId", userId)).collect(),
});

export const getByDomain = internalQuery({
  args: { domain: v.string() },
  handler: async (ctx, { domain }) =>
    ctx.db
      .query("userDomains")
      .withIndex("by_domain", (q) => q.eq("domain", domain.toLowerCase()))
      .first(),
});

// ─── Add a domain ───────────────────────────────────────────────
// The user names the domain + target; we generate a verification
// token and return the DNS instructions they need to set.

export const add = internalMutation({
  args: {
    userId: v.id("users"),
    domain: v.string(),
    targetType: v.union(
      v.literal("cloud_machine"),
      v.literal("managed_relay"),
      v.literal("custom_server"),
    ),
    targetId: v.optional(v.string()),
    targetIp: v.optional(v.string()),
    dnsProvider: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const domain = args.domain.trim().toLowerCase();
    if (!/^[a-z0-9.-]+\.[a-z]{2,}$/.test(domain)) {
      throw new Error("Invalid domain format");
    }
    const existing = await ctx.db
      .query("userDomains")
      .withIndex("by_domain", (q) => q.eq("domain", domain))
      .first();
    if (existing && existing.userId !== args.userId) {
      throw new Error("Domain already claimed by a different user");
    }

    const verificationToken = "yaver-verify-" + Math.random().toString(36).substring(2, 14);
    const now = Date.now();

    // Pull the target's IP + auto-domain for the instructions — best effort,
    // we still record the row if the target isn't provisioned yet.
    let serverIp = args.targetIp;
    let autoDomain: string | undefined;
    if (args.targetType === "cloud_machine" && args.targetId) {
      const m = await ctx.db.get(args.targetId as any);
      if (m && typeof m === "object" && "serverIp" in m) serverIp = (m as any).serverIp ?? serverIp;
      if (m && typeof m === "object" && "hostname" in m) autoDomain = (m as any).hostname as string | undefined;
    } else if (args.targetType === "managed_relay" && args.targetId) {
      const r = await ctx.db.get(args.targetId as any);
      if (r && typeof r === "object" && "serverIp" in r) serverIp = (r as any).serverIp ?? serverIp;
      if (r && typeof r === "object" && "domain" in r) autoDomain = (r as any).domain as string | undefined;
    }

    if (existing) {
      await ctx.db.patch(existing._id, {
        targetType: args.targetType,
        targetId: args.targetId,
        targetIp: serverIp,
        autoDomain,
        dnsProvider: args.dnsProvider,
        status: "pending",
        verificationToken,
        updatedAt: now,
        errorMessage: undefined,
      });
      return { domainId: existing._id, verificationToken, serverIp, autoDomain };
    }

    const domainId = await ctx.db.insert("userDomains", {
      userId: args.userId,
      domain,
      targetType: args.targetType,
      targetId: args.targetId,
      targetIp: serverIp,
      autoDomain,
      dnsProvider: args.dnsProvider,
      verificationToken,
      status: "pending",
      createdAt: now,
      updatedAt: now,
    });
    return { domainId, verificationToken, serverIp, autoDomain };
  },
});

/** Returns the DNS records a user needs to create at their registrar. */
export const instructions = internalQuery({
  args: { domainId: v.id("userDomains") },
  handler: async (ctx, { domainId }) => {
    const row = await ctx.db.get(domainId);
    if (!row) throw new Error("Unknown domain");
    return {
      records: [
        // TXT challenge (proves ownership before we try to issue TLS).
        {
          type: "TXT",
          name: `_yaver-verify.${row.domain}`,
          value: row.verificationToken,
          ttl: 60,
          note: "Verifies that you own this domain.",
        },
        // Either an A record (IP-pinned, simpler) or a CNAME to the
        // auto-domain (survives IP changes, needs CNAME flattening at
        // the apex for root domains).
        row.targetIp
          ? {
              type: "A",
              name: row.domain,
              value: row.targetIp,
              ttl: 60,
              note: "Points your domain at the provisioned server.",
            }
          : row.autoDomain
            ? {
                type: "CNAME",
                name: row.domain,
                value: row.autoDomain,
                ttl: 60,
                note: "Aliases your domain to the Yaver-managed subdomain.",
              }
            : {
                type: "A",
                name: row.domain,
                value: "(target not yet provisioned — re-check after a minute)",
                ttl: 60,
                note: "Target IP is not known yet.",
              },
      ],
    };
  },
});

export const remove = internalMutation({
  args: { domainId: v.id("userDomains"), userId: v.id("users") },
  handler: async (ctx, { domainId, userId }) => {
    const row = await ctx.db.get(domainId);
    if (!row) return;
    if (row.userId !== userId) throw new Error("Not your domain");
    await ctx.db.delete(domainId);
  },
});

// ─── Internal (called from cloudMachines.provision) ─────────────
// Automatic binding when the user provisions a new machine with a custom
// domain in one shot. The provisioning action doesn't need to go through
// the verification flow because it already owns the target IP.

export const recordBinding = internalMutation({
  args: {
    userId: v.id("users"),
    domain: v.string(),
    targetType: v.union(
      v.literal("cloud_machine"),
      v.literal("managed_relay"),
      v.literal("custom_server"),
    ),
    targetId: v.string(),
    serverIp: v.string(),
    autoDomain: v.string(),
  },
  handler: async (ctx, args) => {
    const domain = args.domain.trim().toLowerCase();
    const existing = await ctx.db
      .query("userDomains")
      .withIndex("by_domain", (q) => q.eq("domain", domain))
      .first();
    const now = Date.now();
    if (existing) {
      await ctx.db.patch(existing._id, {
        targetType: args.targetType,
        targetId: args.targetId,
        targetIp: args.serverIp,
        autoDomain: args.autoDomain,
        updatedAt: now,
      });
      return existing._id;
    }
    return ctx.db.insert("userDomains", {
      userId: args.userId,
      domain,
      targetType: args.targetType,
      targetId: args.targetId,
      targetIp: args.serverIp,
      autoDomain: args.autoDomain,
      verificationToken: "yaver-verify-" + Math.random().toString(36).substring(2, 14),
      status: "pending",
      createdAt: now,
      updatedAt: now,
    });
  },
});

export const setStatus = internalMutation({
  args: {
    domainId: v.id("userDomains"),
    status: v.union(
      v.literal("pending"),
      v.literal("verified"),
      v.literal("active"),
      v.literal("error"),
    ),
    errorMessage: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const patch: Record<string, unknown> = {
      status: args.status,
      updatedAt: Date.now(),
    };
    if (args.errorMessage !== undefined) patch.errorMessage = args.errorMessage;
    if (args.status === "verified" || args.status === "active") {
      patch.verifiedAt = Date.now();
    }
    await ctx.db.patch(args.domainId, patch);
  },
});

// ─── Verification action ─────────────────────────────────────────
// Polls DoH (Cloudflare 1.1.1.1 JSON API) for the TXT record and the A/CNAME
// record. Flips to "verified" when both are present. Caller (web UI or
// CLI) should schedule retries; we don't self-schedule here to avoid
// hammering 1.1.1.1 on stuck domains.

export const verify = internalAction({
  args: { domainId: v.id("userDomains") },
  handler: async (ctx, { domainId }): Promise<{ ok: boolean; details: string }> => {
    const row: any = await ctx.runQuery(internal.userDomains.getInternal, { domainId });
    if (!row) return { ok: false, details: "not found" };

    const txtName = `_yaver-verify.${row.domain}`;
    const txtResp = await fetch(`https://cloudflare-dns.com/dns-query?name=${txtName}&type=TXT`, {
      headers: { Accept: "application/dns-json" },
    });
    const txtData = (await txtResp.json()) as { Answer?: { data: string }[] };
    const txtOk = (txtData.Answer ?? []).some((a) =>
      (a.data ?? "").replace(/"/g, "").includes(row.verificationToken),
    );

    // A/CNAME check — either the custom domain resolves to the expected IP,
    // or its CNAME target matches the auto-domain.
    let routeOk = false;
    if (row.targetIp) {
      const aResp = await fetch(
        `https://cloudflare-dns.com/dns-query?name=${row.domain}&type=A`,
        { headers: { Accept: "application/dns-json" } },
      );
      const aData = (await aResp.json()) as { Answer?: { data: string }[] };
      routeOk = (aData.Answer ?? []).some((a) => a.data === row.targetIp);
    } else if (row.autoDomain) {
      const cResp = await fetch(
        `https://cloudflare-dns.com/dns-query?name=${row.domain}&type=CNAME`,
        { headers: { Accept: "application/dns-json" } },
      );
      const cData = (await cResp.json()) as { Answer?: { data: string }[] };
      routeOk = (cData.Answer ?? []).some((a) =>
        (a.data ?? "").replace(/\.$/, "") === row.autoDomain,
      );
    }

    if (txtOk && routeOk) {
      await ctx.runMutation(internal.userDomains.setStatus, {
        domainId,
        status: "verified",
      });
      return { ok: true, details: "both records resolved" };
    }
    return {
      ok: false,
      details: `txt=${txtOk ? "ok" : "missing"}, route=${routeOk ? "ok" : "missing"}`,
    };
  },
});

export const getInternal = internalQuery({
  args: { domainId: v.id("userDomains") },
  handler: async (ctx, { domainId }) => ctx.db.get(domainId),
});

// ─── Machine-side TLS reconciler support ─────────────────────────
//
// The cloud-init installs /usr/local/bin/yaver-tls-reconciler + a
// 5-min systemd timer. On each tick the reconciler:
//
//   1. calls GET /machine/pending-tls (backend/convex/http.ts) →
//      listPendingTLSForMachine() below → all userDomains rows whose
//      target is this machine AND status = "verified".
//   2. runs certbot --nginx for each domain.
//   3. on success: POST /machine/tls-issued → markTLSIssued.
//   4. on failure: POST /machine/tls-error → setStatus("error", msg).
//
// The TXT ownership challenge + A/CNAME route check ran earlier
// (userDomains.verify) before the row reached this state, so this
// stage is strictly "the DNS exists, just go get the cert".

export const listPendingTLSForMachine = internalQuery({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    const rows = await ctx.db
      .query("userDomains")
      .withIndex("by_target", (q) => q.eq("targetType", "cloud_machine").eq("targetId", machineId.toString()))
      .collect();
    return rows.filter((r) => r.status === "verified");
  },
});

export const markTLSIssued = internalMutation({
  args: { domainId: v.id("userDomains") },
  handler: async (ctx, { domainId }) => {
    await ctx.db.patch(domainId, {
      status: "active",
      verifiedAt: Date.now(),
      updatedAt: Date.now(),
      errorMessage: undefined,
    });
  },
});
