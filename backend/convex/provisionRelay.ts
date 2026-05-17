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
    // the subscription is active. Defense-in-depth behind the signed
    // webhook so no replay/mis-schedule can spend Yaver's money.
    const entitled = await ctx.runQuery(internal.subscriptions.isActive, {
      subscriptionId: args.subscriptionId,
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

    const shortId = args.userId.substring(0, 8);
    const serverName = `relay-${shortId}`;
    const subdomain = `${shortId}.relay`;
    const domain = `${shortId}.relay.yaver.io`;

    // Map region to Hetzner location
    const location = args.region.startsWith("us") ? "ash" : "fsn1";

    try {
      // ── Step 1: Create Hetzner server ──────────────────────────

      // CAX11 is arm64 — pick the matching yaver release asset. If you
      // later switch to an amd64 server_type, flip the asset name here.
      const yaverAsset = "yaver-linux-arm64";
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
    ( curl -fsSL "${yaverReleaseUrl}" -o /usr/local/bin/yaver \
      && chmod +x /usr/local/bin/yaver \
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

      const hetznerResp = await fetch("https://api.hetzner.cloud/v1/servers", {
        method: "POST",
        headers: {
          "Authorization": `Bearer ${HCLOUD_TOKEN}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          name: serverName,
          server_type: "cax11",
          image: "ubuntu-24.04",
          location,
          labels: { service: "yaver-relay", user: shortId, managed: "true" },
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

    if (!HCLOUD_TOKEN || !CF_API_TOKEN || !CF_ZONE_ID) {
      console.error("[deprovision] Missing credentials");
      return;
    }

    try {
      // Grace snapshot before delete — a resubscribe can be restored
      // from it (CLAUDE.md: never delete un-snapshotted). Best-effort:
      // a failed snapshot must NOT leave a paid box running forever,
      // so we log and still delete. Cost-safety wins for managed
      // teardown (opposite tradeoff from the disposable dev box).
      try {
        await fetch(`https://api.hetzner.cloud/v1/servers/${args.hetznerServerId}/actions/create_image`, {
          method: "POST",
          headers: { "Authorization": `Bearer ${HCLOUD_TOKEN}`, "Content-Type": "application/json" },
          body: JSON.stringify({ type: "snapshot", description: `yaver-predelete-relay-${args.relayId}-${Date.now()}` }),
        });
      } catch (snapErr) {
        console.error("[deprovision] grace snapshot failed (continuing with delete):", snapErr);
      }

      // Delete Hetzner server
      await fetch(`https://api.hetzner.cloud/v1/servers/${args.hetznerServerId}`, {
        method: "DELETE",
        headers: { "Authorization": `Bearer ${HCLOUD_TOKEN}` },
      });

      // Find and delete Cloudflare DNS record
      const shortDomain = args.domain.replace(".yaver.io", "");
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
