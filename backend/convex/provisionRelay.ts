import { v } from "convex/values";
import { action, internalAction } from "./_generated/server";
import { internal } from "./_generated/api";
import { randomHex } from "./auth";
import { hetznerPickAvailableServerType } from "./cloudLifecycle";
import {
  sharedHostDeletionDecision,
  sharedHostGraceSnapshotDecision,
} from "./relayPool";

// Location candidates per region group, most-preferred first. The pool host is
// created ONCE per ~20 tenants, so a sold-out preferred location must fall back
// to another datacenter in the SAME region group (EU stays EU — a relay in the
// US does not serve an EU buyer's latency expectations, and the inverse is
// true for US buyers). Same rule as Cloud Workspace wake (cloudLifecycle).
const RELAY_LOCATION_CANDIDATES: Record<string, string[]> = {
  eu: ["fsn1", "nbg1", "hel1"],
  us: ["ash", "hil"],
};

// A relay is pass-through: 1 vCPU / 2 GB is ample (the scarce resource is
// BANDWIDTH, not compute). Anything cheaper-sufficient is preferred; the
// selector ranks by gross €/h so an expensive box is never picked.
const RELAY_MIN_REQ = { minCores: 1, minRamGb: 2, minDiskGb: 20, architecture: "x86" as const };

/** Hetzner returns the routed IPv6 /64; the server owns the first address. */
export function hetznerPrimaryIPv6(prefix: unknown): string | undefined {
  const network = String(prefix || "").trim().split("/", 1)[0];
  if (!network || !network.includes(":")) return undefined;
  return network.endsWith("::") ? `${network}1` : network;
}

async function createRelayDNSRecords(args: {
  token: string;
  zoneId: string;
  name: string;
  ipv4: string;
  ipv6?: string;
}): Promise<void> {
  const addresses = [
    { type: "A", content: args.ipv4 },
    ...(args.ipv6 ? [{ type: "AAAA", content: args.ipv6 }] : []),
  ];
  await Promise.all(addresses.map(async ({ type, content }) => {
    const response = await fetch(
      `https://api.cloudflare.com/client/v4/zones/${args.zoneId}/dns_records`,
      {
        method: "POST",
        headers: {
          Authorization: `Bearer ${args.token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ type, name: args.name, content, proxied: false, ttl: 60 }),
      },
    );
    const data = await response.json() as any;
    if (!data.success) console.error(`Cloudflare ${type} DNS error:`, data.errors);
  }));
}

/** First region-group location where ANY sufficient server type is orderable. */
async function pickRelayLocation(token: string, region: string): Promise<string | undefined> {
  const candidates = RELAY_LOCATION_CANDIDATES[String(region || "eu").startsWith("us") ? "us" : "eu"];
  for (const location of candidates) {
    const t = await hetznerPickAvailableServerType(token, location, RELAY_MIN_REQ);
    if (t) return location;
  }
  return undefined;
}

/** Cheapest orderable sufficient server type in `location`, or the env override. */
async function pickRelayServerType(token: string, location: string): Promise<string> {
  const envType = (process.env.YAVER_RELAY_SERVER_TYPE || "").trim();
  if (envType) return envType;
  const picked = await hetznerPickAvailableServerType(token, location, RELAY_MIN_REQ);
  return picked ?? "cpx12"; // last-resort default (cpx12 = €13.49/mo, verified orderable 2026-07-21)
}

/**
 * docker-compose for the relay container.
 *
 * SECURITY / AUTH (2026-08-09 audit): the shared relay MUST validate per-user
 * passwords against Convex (CONVEX_URL) — a box running only a shared
 * RELAY_PASSWORD accepts exactly ONE tenant's password, locking every other
 * tenant on the shared host out AND enforcing no ownership. Per-user mode
 * (validateRelayAccess → /relay/validate) checks password + device ownership +
 * paid entitlement per connection, fail-closed.
 *
 *  - SHARED hosts: per-user auth only (no RELAY_PASSWORD at all) + a random
 *    per-host RELAY_ADMIN_TOKEN so admin endpoints (/tunnels, /admin/*) are
 *    not reachable with any tenant's password.
 *  - DEDICATED (Private Relay) hosts: the tenant's own password is the shared
 *    secret (single tenant — no cross-tenant exposure) + Convex validation +
 *    admin token.
 */
function relayCloudInit(args: {
  domain: string;
  convexSite: string;
  adminToken: string;
  sharedPassword?: string;
}): string {
  const envLines = [
    `- CONVEX_URL=${args.convexSite}`,
    `- RELAY_ADMIN_TOKEN=${args.adminToken}`,
    ...(args.sharedPassword ? [`- RELAY_PASSWORD=${args.sharedPassword}`] : []),
    "- RELAY_QUIC_PORT=4433",
    "- RELAY_HTTP_PORT=8080",
    "- RELAY_DATA_DIR=/data",
  ].join("\n");

  return `#cloud-config
package_update: true
packages:
  - docker.io
  - docker-compose-v2
  - nginx
  - certbot
  - python3-certbot-nginx
  - jq
  - curl
  - ca-certificates
  - ufw
  - git
  - unzip
  - build-essential
  - tmux
runcmd:
  - systemctl enable docker
  - systemctl start docker
  - mkdir -p /opt/yaver-relay
  - |
    cat > /opt/yaver-relay/docker-compose.yml <<'YML'
    services:
      relay:
        image: ghcr.io/kivanccakmak/yaver-relay:latest
        container_name: yaver-relay
        restart: always
        ports:
          - "4433:4433/udp"
          - "8080:8080"
        environment:
${envLines}
        volumes:
          - relay-data:/data
      watchtower:
        image: containrrr/watchtower
        container_name: yaver-watchtower
        restart: always
        volumes:
          - /var/run/docker.sock:/var/run/docker.sock
        command: --interval 3600 --cleanup
    volumes:
      relay-data:
    YML
  - cd /opt/yaver-relay && docker compose pull && docker compose up -d
  - |
    cat > /etc/nginx/sites-available/relay <<'NGINX'
    server {
        listen 80;
        listen [::]:80;
        server_name ${args.domain};
        location / {
            proxy_pass http://127.0.0.1:8080;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
            proxy_read_timeout 300s;
            proxy_buffering off;
        }
    }
    NGINX
  - ln -sf /etc/nginx/sites-available/relay /etc/nginx/sites-enabled/
  - rm -f /etc/nginx/sites-enabled/default
  - nginx -t && systemctl reload nginx
  - ufw allow 80/tcp || true
  - ufw allow 443/tcp || true
  - ufw allow 4433/udp || true
`;
}

/**
 * Point the user's OTHER self-hosted devices at their managed relay once it
 * answers (this is the actual delivery of Relay Pro — the box nobody dials is
 * worth nothing). Only repoints when the user has no custom relay (empty, the
 * platform default, or already this domain); a self-hosted relay the user
 * configured themselves is never clobbered. Idempotent.
 */
async function wireUserRelayUrl(
  ctx: { runQuery: (ref: any, args: any) => Promise<any>; runMutation: (ref: any, args: any) => Promise<any> },
  relay: { userId: any },
  domain: string,
): Promise<void> {
  try {
    const target = `https://${domain}`;
    const settings = await ctx.runQuery(internal.userSettings.getByUserId, { userId: relay.userId });
    const platform = await ctx.runQuery(internal.platformConfig.getClientConfig, {});
    let defaultRelayUrl: string | undefined;
    try {
      const relays: Array<{ httpUrl?: string }> = JSON.parse(String(platform?.relay_servers ?? "[]"));
      defaultRelayUrl = relays[0]?.httpUrl;
    } catch { /* not configured */ }
    const current = settings?.relayUrl;
    if (current && current !== defaultRelayUrl && current !== target) {
      console.log(`[provision] user ${relay.userId} has a custom relayUrl (${current}) — not overwriting with ${target}`);
      return;
    }
    await ctx.runMutation(internal.userSettings.setRelayForUser, {
      userId: relay.userId,
      relayUrl: target,
    });
    console.log(`[provision] wired ${relay.userId} to their managed relay ${target}`);
  } catch (e) {
    console.error(`[provision] relayUrl wiring failed for ${relay.userId}:`, e);
  }
}

/**
 * Provision a managed relay server.
 * Called after LemonSqueezy payment confirmation.
 *
 * Flow:
 *   1. Create Hetzner CAX11 server via API
 *   2. Add Cloudflare DNS record (DNS only)
 *   3. Wait for server to boot
 *   4. Update Convex with server details
 *   5. The provisioning script on the server handles Docker + SSL
 *
 * Env vars required (set in Convex dashboard):
 *   HCLOUD_TOKEN    — Hetzner Cloud API token
 *   CF_API_TOKEN    — Cloudflare API token (Zone DNS Edit)
 *   CF_ZONE_ID      — Cloudflare zone ID for yaver.io
 */

// Provision a new managed relay server
export const provision = internalAction({
  args: {
    userId: v.id("users"),
    // Optional for the owner-dev path — see managedRelays.create.
    subscriptionId: v.optional(v.id("subscriptions")),
    relayId: v.id("managedRelays"),
    region: v.string(),
    password: v.string(),
    // Optional — user-supplied domain (e.g. relay.myapp.com). When set:
    //   • still create the <shortId>.relay.yaver.io subdomain in the
    //     yaver.io zone (so the relay always has a canonical URL);
    //   • also record a user_domains binding so the web UI surfaces the
    //     DNS records the user needs to set at their own registrar.
    // Nginx + certbot inside the cloud-init already accept any
    // Host:-based request via the default server_name, so no extra
    // config is needed on the box itself.
    customDomain: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const HCLOUD_TOKEN = process.env.HCLOUD_TOKEN;
    const CF_API_TOKEN = process.env.CF_API_TOKEN;
    const CF_ZONE_ID = process.env.CF_ZONE_ID;

    if (!HCLOUD_TOKEN || !CF_API_TOKEN || !CF_ZONE_ID) {
      await ctx.runMutation(internal.managedRelays.setStatus, {
        relayId: args.relayId,
        status: "error",
        errorMessage: "Missing provisioning credentials (HCLOUD_TOKEN, CF_API_TOKEN, CF_ZONE_ID)",
      });
      return;
    }

    // Fail-closed billing gate — NEVER create a Hetzner server unless
    // the subscription is active OR the owner is on the env allowlist
    // (lets the repo owner develop the managed Hetzner flow without
    // LemonSqueezy; env unset ⇒ pure fail-closed). Defense-in-depth
    // behind the signed webhook so no replay can spend Yaver's money.
    const entitled = await ctx.runQuery(internal.subscriptions.canProvisionManaged, {
      subscriptionId: args.subscriptionId,
      userId: args.userId,
    });
    if (!entitled) {
      await ctx.runMutation(internal.managedRelays.setStatus, {
        relayId: args.relayId,
        status: "error",
        errorMessage:
          "Subscription not active — provisioning denied (fail-closed billing gate)",
      });
      return;
    }

    // ─── Pool assignment (before ANY provider spend) ─────────────────────
    // Relay Pro rides a shared multi-tenant host by default. A dedicated box
    // per subscriber is 16% gross against $9/mo and cannot scale to zero (a
    // relay is useless when off), so the box is created ONCE per ~20 tenants
    // and reused thereafter. Safe because the relay is pass-through AND the
    // box validates each tenant's password per-user via Convex
    // (CONVEX_URL in the container env — see relayCloudInit). A dedicated
    // ("Private Relay") row skips the pool entirely.
    const relay = await ctx.runQuery(internal.managedRelays.getById, { relayId: args.relayId });
    const dedicated = Boolean(relay?.isDedicated);
    const shortId = args.userId.substring(0, 8);
    const subdomain = `${shortId}.relay`;
    const domain = `${shortId}.relay.yaver.io`;
    const convexSite =
      process.env.CONVEX_SITE_URL || "https://perceptive-minnow-557.eu-west-1.convex.site";

    let slot: { hostKey: string; reason: string } | null = null;
    if (!dedicated) {
      slot = await ctx.runMutation(internal.relayPool.assignToPool, {
        relayId: args.relayId,
        region: args.region,
      });
    }
    const hostKey = dedicated ? null : (slot?.hostKey ?? null);

    try {
      // ── REUSE (shared only): another tenant already provisioned this host.
      // This is the whole saving — every tenant after the first costs nothing
      // but its share. Only valid for pooled rows; a dedicated relay always
      // gets its own box.
      if (hostKey) {
        const existingHost = await ctx.runQuery(internal.relayPool.hostEndpoint, { hostKey });
        if (existingHost?.serverId && existingHost.serverIp) {
          await ctx.runMutation(internal.managedRelays.updateProvisioned, {
            relayId: args.relayId,
            hetznerServerId: existingHost.serverId,
            serverIp: existingHost.serverIp,
            serverIpv6: existingHost.serverIpv6,
            domain,
          });
          // The tenant still gets its OWN canonical hostname pointing at the
          // shared host, so its relay URL is stable and independent of which
          // box it happens to sit on today.
          await createRelayDNSRecords({
            token: CF_API_TOKEN,
            zoneId: CF_ZONE_ID,
            name: subdomain,
            ipv4: existingHost.serverIp,
            ipv6: existingHost.serverIpv6,
          }).catch(() => { /* DNS is best-effort; IP-direct still works */ });
          console.log(`[provision] Relay ${domain} joined shared host ${hostKey} (${slot?.reason ?? ""})`);
          await ctx.scheduler.runAfter(60_000, internal.provisionRelay.healthCheck, {
            relayId: args.relayId, domain,
          });
          return;
        }
      }

      // ── Capacity-aware placement ─────────────────────────────────────────
      // The pool host is created ONCE per ~20 tenants, so a sold-out
      // preferred location/type would fail the whole pool. Ask Hetzner what
      // is orderable: first location in the region group with ANY sufficient
      // type, then the cheapest sufficient type there (same machinery the
      // Cloud wake path uses — verified 2026-07-21: cax11 was sold out EU-wide
      // and cpx12 was the cheapest orderable x86 type).
      const location = await pickRelayLocation(HCLOUD_TOKEN, args.region);
      if (!location) {
        await ctx.runMutation(internal.managedRelays.setStatus, {
          relayId: args.relayId,
          status: "error",
          errorMessage: `No orderable relay server type in any ${String(args.region || "eu").startsWith("us") ? "US" : "EU"} location — capacity is temporarily exhausted.`,
        });
        return;
      }
      const serverType = await pickRelayServerType(HCLOUD_TOKEN, location);
      // Host boxes are named per POOL SLOT, not per user — the box serves many
      // tenants, so naming it after the first one would be a lie that outlives
      // that tenant's subscription. Dedicated boxes are named per relay row.
      const serverName = dedicated
        ? `relay-dedicated-${args.relayId.toString().substring(0, 8)}`
        : (hostKey ?? `relay-${shortId}`);
      const adminToken = randomHex(24);
      const cloudConfig = relayCloudInit({
        domain,
        convexSite,
        adminToken,
        sharedPassword: dedicated ? args.password : undefined,
      });

      const hetznerResp = await fetch("https://api.hetzner.cloud/v1/servers", {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${HCLOUD_TOKEN}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          name: serverName,
          server_type: serverType,
          image: "ubuntu-24.04",
          location,
          // Labelled by POOL SLOT so the orphan sweep and cleanup can reason
          // about it; `user` is the tenant who happened to create it first.
          labels: dedicated
            ? { service: "yaver-relay", dedicated: "true", user: shortId, managed: "true" }
            : { service: "yaver-relay", pool: hostKey ?? "", user: shortId, managed: "true" },
          user_data: cloudConfig,
        }),
      });

      if (!hetznerResp.ok) {
        const errText = await hetznerResp.text();
        throw new Error(`Hetzner API error ${hetznerResp.status}: ${errText}`);
      }

      const hetznerData = await hetznerResp.json() as any;
      const serverId = String(hetznerData.server.id);
      const serverIp = hetznerData.server.public_net.ipv4.ip;
      const serverIpv6 = hetznerPrimaryIPv6(hetznerData.server.public_net.ipv6?.ip);

      // ── Step 2: Add Cloudflare DNS record ─────────────────────

      await createRelayDNSRecords({
        token: CF_API_TOKEN,
        zoneId: CF_ZONE_ID,
        name: subdomain,
        ipv4: serverIp,
        ipv6: serverIpv6,
      }).catch((error) => {
        // Don't fail provisioning — IP-direct remains available.
        console.error("Cloudflare DNS error:", error);
      });

      // ── Step 3: Update Convex with server details ─────────────

      await ctx.runMutation(internal.managedRelays.updateProvisioned, {
        relayId: args.relayId,
        hetznerServerId: serverId,
        serverIp,
        serverIpv6,
        domain,
      });

      // Record custom-domain binding so the dashboard can show the user
      // which DNS records they still need to set at their registrar. This
      // is metadata only — nginx on the relay box is already Host-agnostic.
      if (args.customDomain) {
        await ctx.runMutation(internal.userDomains.recordBinding, {
          userId: args.userId,
          domain: args.customDomain,
          targetType: "managed_relay",
          targetId: args.relayId.toString(),
          serverIp,
          autoDomain: domain,
        });
      }

      console.log(`[provision] Relay provisioned: ${domain} (${serverIp}), server ${serverId}, type ${serverType} @ ${location}`);

      // ── Step 4: Schedule SSL setup ────────────────────────────
      // SSL is handled by cloud-init: certbot runs after nginx is up
      // and DNS has propagated. We schedule a health check for 3 min later.

      await ctx.scheduler.runAfter(180_000, internal.provisionRelay.healthCheck, {
        relayId: args.relayId,
        domain,
      });

    } catch (error: any) {
      console.error("[provision] Failed:", error.message);
      await ctx.runMutation(internal.managedRelays.setStatus, {
        relayId: args.relayId,
        status: "error",
        errorMessage: error.message,
      });
    }
  },
});

// Health check — called 3 minutes after provisioning
export const healthCheck = internalAction({
  args: {
    relayId: v.id("managedRelays"),
    domain: v.string(),
  },
  handler: async (ctx, args) => {
    try {
      // Try HTTPS first, then HTTP
      let healthy = false;
      for (const proto of ["https", "http"]) {
        try {
          const resp = await fetch(`${proto}://${args.domain}/health`, {
            signal: AbortSignal.timeout(10_000),
          });
          if (resp.ok) {
            const data = await resp.json() as any;
            if (data.ok) {
              healthy = true;
              break;
            }
          }
        } catch {
          // Try next protocol
        }
      }

      if (healthy) {
        await ctx.runMutation(internal.managedRelays.recordHealthCheck, {
          relayId: args.relayId,
        });
        console.log(`[provision] Health check passed: ${args.domain}`);
      } else {
        // Retry in 2 more minutes
        console.log(`[provision] Health check failed for ${args.domain}, retrying in 2min...`);
        await ctx.scheduler.runAfter(120_000, internal.provisionRelay.healthCheck, {
          relayId: args.relayId,
          domain: args.domain,
        });
      }
    } catch (error: any) {
      console.error(`[provision] Health check error for ${args.domain}:`, error.message);
    }
  },
});

// Deprovision — called when subscription expires / owner stops a dev relay.
export const deprovision = internalAction({
  args: {
    relayId: v.id("managedRelays"),
    hetznerServerId: v.string(),
    domain: v.string(),
  },
  handler: async (ctx, args) => {
    const HCLOUD_TOKEN = process.env.HCLOUD_TOKEN;
    const CF_API_TOKEN = process.env.CF_API_TOKEN;
    const CF_ZONE_ID = process.env.CF_ZONE_ID;

    // Fetch the row FIRST — the delete decision AND the snapshot decision
    // both depend on whether this is a shared-pool row or a dedicated relay.
    const relay = await ctx.runQuery(internal.managedRelays.getById, {
      relayId: args.relayId,
    });

    // Mark the tenant gone FIRST so hostIsEmpty no longer counts this row
    // when we ask whether the shared host can be drained.
    await ctx.runMutation(internal.managedRelays.setStatus, {
      relayId: args.relayId,
      status: "stopped",
    });

    // DNS cleanup ALWAYS — this tenant's own subdomain must stop resolving
    // even when the shared box stays up for the other tenants.
    if (CF_API_TOKEN && CF_ZONE_ID && args.domain) {
      try {
        const listResp = await fetch(
          `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?name=${args.domain}`,
          { headers: { "Authorization": `Bearer ${CF_API_TOKEN}` } }
        );
        const listData = await listResp.json() as any;
        await Promise.all((listData.result ?? []).map((record: { id: string }) =>
          fetch(
            `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${record.id}`,
            {
              method: "DELETE",
              headers: { "Authorization": `Bearer ${CF_API_TOKEN}` },
            },
          ),
        ));
      } catch (e) {
        console.error("[deprovision] DNS cleanup failed:", e);
      }
    }

    // ─── Shared host: never delete a box other tenants still use ──────────
    // A shared host serves up to RELAY_TENANTS_PER_HOST tenants from ONE
    // Hetzner box. The pre-fix behaviour deleted the box unconditionally, so
    // the FIRST tenant to cancel took the relay offline for everyone else on
    // the host — a fleet-wide outage triggered by one subscription ending.
    // Rule (relayPoolPolicy.sharedHostDeletionDecision): delete ONLY when the
    // host is drained. Dedicated relays are tenant-private and always
    // deletable.
    if (relay?.sharedHostKey) {
      const { tenants } = await ctx.runQuery(internal.relayPool.hostIsEmpty, {
        hostKey: relay.sharedHostKey,
      });
      const decision = sharedHostDeletionDecision({
        sharedHostKey: relay.sharedHostKey,
        liveTenantsOnHost: tenants,
      });
      if (!decision.deleteServer) {
        console.log(
          `[deprovision] ${decision.reason} — keeping box, releasing this tenant's slot`,
        );
        return;
      }
    }

    if (!HCLOUD_TOKEN) {
      // Never silently return leaving the row in a stale state while
      // the box still bills — surface it so the operator sets the
      // platform token (--prod) and retries.
      await ctx.runMutation(internal.managedRelays.setStatus, {
        relayId: args.relayId,
        status: "error",
        errorMessage:
          "Platform HCLOUD_TOKEN is not configured on this Convex deployment — the relay box was NOT deleted. Set it with `npx convex env set HCLOUD_TOKEN <token> --prod`, then retry.",
      });
      return;
    }

    try {
      // Grace snapshot BEFORE delete — ONLY for dedicated relays. A dedicated
      // box is tenant-private, so a resubscribe can be restored from it.
      // SHARED pool hosts are deliberately NOT snapshotted: they are
      // pass-through with no tenant data, and a drained host's snapshot is a
      // billed orphan with no restore path (measured 2026-08-09: a 0.39 GB
      // `yaver-predelete-relay-*` snapshot left billed by a shared teardown).
      if (sharedHostGraceSnapshotDecision(relay?.sharedHostKey)) {
        try {
          const snapResp = await fetch(`https://api.hetzner.cloud/v1/servers/${args.hetznerServerId}/actions/create_image`, {
            method: "POST",
            headers: { "Authorization": `Bearer ${HCLOUD_TOKEN}`, "Content-Type": "application/json" },
            body: JSON.stringify({ type: "snapshot", description: `yaver-predelete-relay-${args.relayId}-${Date.now()}` }),
          });
          // RECORD THE ID. Until 2026-07-21 this response was discarded, which
          // made the snapshot simultaneously (a) permanently billed and (b)
          // impossible to restore from — defeating the entire stated purpose of
          // taking it, and invisible to the orphan sweep because no row referenced
          // it. An unrecorded snapshot is pure cost with zero recovery value.
          if (snapResp.ok) {
            const sj = (await snapResp.json()) as { image?: { id?: number } };
            if (sj.image?.id) {
              await ctx.runMutation(internal.managedRelays.setSnapshot, {
                relayId: args.relayId,
                lastSnapshotId: String(sj.image.id),
              });
            }
          }
        } catch (snapErr) {
          console.error("[deprovision] grace snapshot failed (continuing with delete):", snapErr);
        }
      }

      // Delete Hetzner server
      await fetch(`https://api.hetzner.cloud/v1/servers/${args.hetznerServerId}`, {
        method: "DELETE",
        headers: { "Authorization": `Bearer ${HCLOUD_TOKEN}` },
      });

      console.log(`[deprovision] Relay deprovisioned: ${args.domain}`);
    } catch (error: any) {
      console.error("[deprovision] Error:", error.message);
    }
  },
});
