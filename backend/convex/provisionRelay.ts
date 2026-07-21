import { v } from "convex/values";
import { action, internalAction } from "./_generated/server";
import { internal } from "./_generated/api";

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
    subscriptionId: v.id("subscriptions"),
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

    // ─── Shared pool assignment (before ANY provider spend) ──────────────
    // Relay Pro rides a shared multi-tenant host. A dedicated box per
    // subscriber is 16% gross against $9/mo and cannot scale to zero (a relay
    // is useless when off), so the box is created ONCE per ~20 tenants and
    // reused thereafter. Safe because the relay is pass-through: it authorizes
    // nothing, executes no tenant code, and cross-tenant bridging is refused in
    // Convex before any forwarding. See relayPool.ts.
    const slot = await ctx.runMutation(internal.relayPool.assignToPool, {
      relayId: args.relayId,
      region: args.region,
    });
    const existingHost = await ctx.runQuery(internal.relayPool.hostEndpoint, {
      hostKey: slot.hostKey,
    });
    const shortId = args.userId.substring(0, 8);
    // Host boxes are named per POOL SLOT, not per user — the box serves many
    // tenants, so naming it after the first one would be a lie that outlives
    // that tenant's subscription.
    const serverName = slot.hostKey;
    const subdomain = `${shortId}.relay`;
    const domain = `${shortId}.relay.yaver.io`;

    // Map region to Hetzner location
    const location = args.region.startsWith("us") ? "ash" : "fsn1";

    try {
      // ── Step 1: Create Hetzner server ──────────────────────────

      // CAX11 is arm64 — pick the matching yaver release asset. If you
      // later switch to an amd64 server_type, flip the asset name here.
      // The release ships the binary inside a .tar.gz (single file
      // named `yaver`), never as a raw asset — extract on the box.
      const yaverAsset = "yaver-linux-arm64.tar.gz";
      const yaverReleaseUrl = `https://github.com/kivanccakmak/yaver.io/releases/latest/download/${yaverAsset}`;

      const cloudConfig = `#cloud-config
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
  # Install the yaver CLI on the managed relay so the box is usable as a
  # devops console (yaver sdk-token, yaver dns *, yaver guests *, etc.)
  # without SSHing in with extra tooling. Non-fatal on failure.
  - |
    ( curl -fsSL "${yaverReleaseUrl}" -o /tmp/yaver.tgz \
      && tar -xzf /tmp/yaver.tgz -C /usr/local/bin yaver \
      && chmod +x /usr/local/bin/yaver \
      && rm -f /tmp/yaver.tgz \
      && /usr/local/bin/yaver --version >/dev/null 2>&1 ) || echo "[cloud-init] yaver install skipped (release not yet published for arm64)"
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
          - RELAY_PASSWORD=${args.password}
          - RELAY_QUIC_PORT=4433
          - RELAY_HTTP_PORT=8080
          - RELAY_DATA_DIR=/data
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
        server_name ${domain};
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

      // REUSE: another tenant already provisioned this host. This is the whole
      // saving — every tenant after the first costs nothing but its share, and
      // creating a second box here would silently restore the 16% margin.
      if (existingHost?.serverId && existingHost.serverIp) {
        await ctx.runMutation(internal.managedRelays.updateProvisioned, {
          relayId: args.relayId,
          hetznerServerId: existingHost.serverId,
          serverIp: existingHost.serverIp,
          domain,
        });
        // The tenant still gets its OWN canonical hostname pointing at the
        // shared host, so its relay URL is stable and independent of which box
        // it happens to sit on today.
        await fetch(
          `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records`,
          {
            method: "POST",
            headers: { Authorization: `Bearer ${CF_API_TOKEN}`, "Content-Type": "application/json" },
            body: JSON.stringify({
              type: "A", name: subdomain, content: existingHost.serverIp,
              proxied: false, ttl: 60,
            }),
          },
        ).catch(() => { /* DNS is best-effort; IP-direct still works */ });
        console.log(`[provision] Relay ${domain} joined shared host ${slot.hostKey} (${slot.reason})`);
        await ctx.scheduler.runAfter(60_000, internal.provisionRelay.healthCheck, {
          relayId: args.relayId, domain,
        });
        return;
      }

      const hetznerResp = await fetch("https://api.hetzner.cloud/v1/servers", {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${HCLOUD_TOKEN}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          name: serverName,
          // ⚠️ VERIFIED AGAINST THE LIVE CATALOG 2026-07-21.
          // This was "cax11" (ARM, €6.99/mo) — which is SOLD OUT in every EU
          // datacenter, so provisioning a new pool host would have failed at
          // create. It also could never be resized: change-type cannot cross
          // architectures, and ZERO ARM types are currently orderable.
          //
          // cpx12 (1c/2GB, €13.49/mo) is the cheapest EU-available x86 type. It
          // is dearer per box, but the pool amortises it across ~20 tenants:
          // €0.67/user → 92% margin on Relay Pro, versus 16% dedicated. A relay
          // is pass-through, so 1 core and 2 GB is ample — the scarce resource
          // is BANDWIDTH, not CPU.
          //
          // Re-check before trusting this: `hcloud server-type list`.
          server_type: process.env.YAVER_RELAY_SERVER_TYPE || "cpx12",
          image: "ubuntu-24.04",
          location,
          // Labelled by POOL SLOT so the orphan sweep and cleanup can reason
          // about it; `user` is the tenant who happened to create it first.
          labels: { service: "yaver-relay", pool: slot.hostKey, user: shortId, managed: "true" },
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

      // ── Step 2: Add Cloudflare DNS record ─────────────────────

      const cfResp = await fetch(
        `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records`,
        {
          method: "POST",
          headers: {
            "Authorization": `Bearer ${CF_API_TOKEN}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            type: "A",
            name: subdomain,
            content: serverIp,
            proxied: false,
            ttl: 60,
          }),
        }
      );

      const cfData = await cfResp.json() as any;
      if (!cfData.success) {
        console.error("Cloudflare DNS error:", cfData.errors);
        // Don't fail — DNS can be added manually
      }

      // ── Step 3: Update Convex with server details ─────────────

      await ctx.runMutation(internal.managedRelays.updateProvisioned, {
        relayId: args.relayId,
        hetznerServerId: serverId,
        serverIp,
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

      console.log(`[provision] Relay provisioned: ${domain} (${serverIp}), server ${serverId}`);

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

// Deprovision — called when subscription expires
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
    if (!CF_API_TOKEN || !CF_ZONE_ID) {
      // DNS cleanup is secondary — proceed to delete the box anyway,
      // just skip the Cloudflare record removal below.
      console.error("[deprovision] CF creds missing — deleting box, skipping DNS cleanup");
    }

    try {
      // Grace snapshot before delete — a resubscribe can be restored
      // from it (CLAUDE.md: never delete un-snapshotted). Best-effort:
      // a failed snapshot must NOT leave a paid box running forever,
      // so we log and still delete. Cost-safety wins for managed
      // teardown (opposite tradeoff from the disposable dev box).
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

      // Delete Hetzner server
      await fetch(`https://api.hetzner.cloud/v1/servers/${args.hetznerServerId}`, {
        method: "DELETE",
        headers: { "Authorization": `Bearer ${HCLOUD_TOKEN}` },
      });

      // Find and delete Cloudflare DNS record (only if CF creds are
      // configured — DNS cleanup is secondary to deleting the box).
      if (CF_API_TOKEN && CF_ZONE_ID) {
        const listResp = await fetch(
          `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?name=${args.domain}`,
          { headers: { "Authorization": `Bearer ${CF_API_TOKEN}` } }
        );
        const listData = await listResp.json() as any;
        if (listData.result?.length > 0) {
          const recordId = listData.result[0].id;
          await fetch(
            `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${recordId}`,
            {
              method: "DELETE",
              headers: { "Authorization": `Bearer ${CF_API_TOKEN}` },
            }
          );
        }
      }

      // Update status
      await ctx.runMutation(internal.managedRelays.setStatus, {
        relayId: args.relayId,
        status: "stopped",
      });

      console.log(`[deprovision] Relay deprovisioned: ${args.domain}`);
    } catch (error: any) {
      console.error("[deprovision] Error:", error.message);
    }
  },
});
