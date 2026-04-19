import { mutation, query, internalMutation, internalAction, internalQuery } from "./_generated/server";
import { v } from "convex/values";
import { internal } from "./_generated/api";
import { listActiveInfraGrantsForGuest, listGrantedMachineIdsForGrant } from "./access";
import { sha256Hex } from "./auth";

// Machine specs by type. The Hetzner server_type strings are what you pass
// to POST https://api.hetzner.cloud/v1/servers.
const MACHINE_SPECS = {
  cpu: {
    hetznerType: "cx42",     // 8 vCPU, 16 GB RAM, 160 GB NVMe, amd64
    vcpu: 8,
    ramGb: 16,
    diskGb: 160,
    arch: "amd64" as const,
  },
  gpu: {
    hetznerType: "gex44",    // Dedicated NVIDIA RTX 4000, 20 GB VRAM
    vcpu: 16,
    ramGb: 64,
    diskGb: 320,
    arch: "amd64" as const,
    gpu: "rtx4000",
    vram: 20,
  },
};

// ─── Queries ────────────────────────────────────────────────────

/** Get all machines for a user (owned + team-shared). */
export const listForUser = query({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    // Direct machines
    const owned = await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();

    // Team machines (find user's teams, then machines for those teams)
    const memberships = await ctx.db
      .query("teamMembers")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();

    const teamMachines: typeof owned = [];
    for (const m of memberships) {
      const machines = await ctx.db
        .query("cloudMachines")
        .withIndex("by_team", (q) => q.eq("teamId", m.teamId))
        .collect();
      teamMachines.push(...machines);
    }

    const grantedMachines: typeof owned = [];
    const grants = await listActiveInfraGrantsForGuest(ctx, userId);
    for (const grant of grants) {
      if (grant.shareAllMachines) {
        const hostMachines = await ctx.db
          .query("cloudMachines")
          .withIndex("by_user", (q) => q.eq("userId", grant.hostUserId))
          .collect();
        grantedMachines.push(...hostMachines);
        continue;
      }
      const machineIds = await listGrantedMachineIdsForGrant(ctx, grant._id);
      for (const machineId of machineIds) {
        const machine = await ctx.db.get(machineId);
        if (!machine) continue;
        if (machine.userId !== grant.hostUserId) continue;
        grantedMachines.push(machine);
      }
    }

    // Deduplicate (user might own a team machine or receive the same machine twice)
    const seen = new Set<string>();
    const all = [...owned, ...teamMachines, ...grantedMachines].filter((m) => {
      const id = m._id.toString();
      if (seen.has(id)) return false;
      seen.add(id);
      return true;
    });

    return all;
  },
});

/** Get a specific machine by ID. */
export const get = query({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    return await ctx.db.get(machineId);
  },
});

/** Internal variant for actions that need to read a machine row. */
export const getInternal = internalQuery({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => ctx.db.get(machineId),
});

// ─── Mutations ──────────────────────────────────────────────────

/** Create a new cloud machine and start provisioning. */
export const create = mutation({
  args: {
    userId: v.id("users"),
    machineType: v.string(),        // "cpu" | "gpu"
    teamId: v.optional(v.string()), // if team-owned
    region: v.optional(v.string()), // "eu" | "us", default "eu"
    repoUrl: v.optional(v.string()),
    sshPublicKey: v.optional(v.string()),
    subscriptionId: v.optional(v.id("subscriptions")),
    customDomain: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const specDef = MACHINE_SPECS[args.machineType as keyof typeof MACHINE_SPECS];
    if (!specDef) throw new Error("Invalid machine type: " + args.machineType);

    const now = Date.now();
    const tools = ["nodejs", "python", "go", "rust", "docker", "expo-cli", "eas-cli"];
    if (args.machineType === "gpu") {
      tools.push("ollama", "personaplex", "whisper", "cuda");
    }

    const specs: {
      vcpu: number;
      ramGb: number;
      diskGb: number;
      arch: string;
      gpu?: string;
      vram?: number;
    } = {
      vcpu: specDef.vcpu,
      ramGb: specDef.ramGb,
      diskGb: specDef.diskGb,
      arch: specDef.arch,
    };
    if ("gpu" in specDef) {
      specs.gpu = specDef.gpu;
      specs.vram = specDef.vram;
    }

    const machineId = await ctx.db.insert("cloudMachines", {
      userId: args.userId,
      teamId: args.teamId,
      subscriptionId: args.subscriptionId,
      machineType: args.machineType,
      status: "provisioning",
      multiUser: !!args.teamId,
      region: args.region ?? "eu",
      tools,
      repoUrl: args.repoUrl,
      sshPublicKey: args.sshPublicKey,
      specs,
      createdAt: now,
      updatedAt: now,
    });

    // Schedule provisioning (runs async — provision is an action so it
    // can call the Hetzner + Cloudflare APIs). Passing customDomain via the
    // scheduler payload keeps it out of the DB until we know which server IP
    // to wire it to.
    await ctx.scheduler.runAfter(0, internal.cloudMachines.provision, {
      machineId,
      customDomain: args.customDomain,
    });

    return machineId;
  },
});

// ─── Internal helpers used by the provisioning action ─────────────

/** Patch the machine row from inside an action (actions cannot touch db directly). */
export const setProvisioned = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    hetznerServerId: v.string(),
    serverIp: v.string(),
    hostname: v.string(),
    machineTokenHash: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const patch: Record<string, unknown> = {
      hetznerServerId: args.hetznerServerId,
      serverIp: args.serverIp,
      hostname: args.hostname,
      updatedAt: Date.now(),
    };
    if (args.machineTokenHash) patch.machineTokenHash = args.machineTokenHash;
    await ctx.db.patch(args.machineId, patch);
  },
});

export const setStatus = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    status: v.string(),
    errorMessage: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const patch: Record<string, unknown> = {
      status: args.status,
      updatedAt: Date.now(),
    };
    if (args.errorMessage) patch.errorMessage = args.errorMessage;
    if (args.status === "active") patch.lastHealthCheck = Date.now();
    await ctx.db.patch(args.machineId, patch);
  },
});

// ─── Provisioning action (calls Hetzner + Cloudflare) ──────────────

/**
 * Provision a Hetzner server for a cloudMachines row.
 *
 * Env vars required (set in Convex dashboard):
 *   HCLOUD_TOKEN    — Hetzner Cloud API token
 *   CF_API_TOKEN    — Cloudflare API token (Zone DNS Edit)
 *   CF_ZONE_ID      — Cloudflare zone ID for yaver.io (used for the auto-
 *                     generated `<shortId>.cloud.yaver.io` subdomain).
 *
 * Flow mirrors provisionRelay.provision:
 *   1. Create Hetzner server with cloud-init for yaver + dev tools
 *      (and Ollama/CUDA on the GPU tier).
 *   2. Add Cloudflare A record for `<shortId>.cloud.yaver.io`.
 *   3. If customDomain provided, create an additional CNAME in Cloudflare
 *      (only if it lives inside the yaver.io zone). For user-owned zones
 *      (myapp.com) the web UI records the binding in userDomains and the
 *      user points their own DNS at the server manually.
 *   4. Write Hetzner server id + IP back into the machine row.
 *   5. Schedule a health check 5 min later.
 */
export const provision = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    customDomain: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const HCLOUD_TOKEN = process.env.HCLOUD_TOKEN;
    const CF_API_TOKEN = process.env.CF_API_TOKEN;
    const CF_ZONE_ID = process.env.CF_ZONE_ID;
    if (!HCLOUD_TOKEN || !CF_API_TOKEN || !CF_ZONE_ID) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId: args.machineId,
        status: "error",
        errorMessage:
          "Missing provisioning credentials (HCLOUD_TOKEN, CF_API_TOKEN, CF_ZONE_ID)",
      });
      return;
    }

    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, {
      machineId: args.machineId,
    });
    if (!machine) return;

    const specDef = MACHINE_SPECS[machine.machineType as keyof typeof MACHINE_SPECS];
    if (!specDef) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId: args.machineId,
        status: "error",
        errorMessage: "Unknown machine type: " + machine.machineType,
      });
      return;
    }

    const shortId = machine._id.toString().substring(0, 8);
    const serverName = `yaver-${machine.machineType}-${shortId}`;
    const subdomain = `${shortId}.cloud`;
    const autoDomain = `${shortId}.cloud.yaver.io`;
    const location = (machine.region ?? "eu").startsWith("us") ? "ash" : "fsn1";
    const isGpu = machine.machineType === "gpu";

    const yaverArch = specDef.arch === "amd64" ? "amd64" : "arm64";
    const yaverAsset = `yaver-linux-${yaverArch}`;
    const yaverReleaseUrl = `https://github.com/kivanccakmak/yaver.io/releases/latest/download/${yaverAsset}`;

    // Machine-auth token — plaintext goes into /etc/yaver/machine.json on
    // the box, only the hash is persisted. Used by the TLS reconciler
    // systemd timer to fetch pending custom-domain TLS jobs from Convex
    // (GET /machine/pending-tls) and report the result back.
    const machineToken = [
      Math.random().toString(36).substring(2),
      Math.random().toString(36).substring(2),
      Math.random().toString(36).substring(2),
    ].join("").substring(0, 48);
    const machineTokenHash = await sha256Hex(machineToken);
    const convexSite = process.env.CONVEX_SITE_URL || "https://shocking-echidna-394.eu-west-1.convex.site";
    const machineIdStr = machine._id.toString();

    // cloud-init: install dev tools, yaver CLI, docker; for GPU tier, also
    // Ollama + CUDA drivers. The same box runs as both a customer-owned
    // yaver agent AND a dev server for their apps — so we install the full
    // stack the user might reach for, not a stripped-down set.
    const cloudInit = `#cloud-config
package_update: true
packages:
  - ca-certificates
  - curl
  - git
  - gnupg
  - jq
  - tmux
  - ufw
  - unzip
  - build-essential
  - python3
  - python3-pip
  - nginx
  - certbot
  - python3-certbot-nginx
  - docker.io
  - docker-compose-v2
runcmd:
  - systemctl enable docker && systemctl start docker
  # Node.js 22 LTS
  - curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  - apt-get install -y nodejs
  # Go 1.22
  - |
    curl -fsSL https://go.dev/dl/go1.22.6.linux-${yaverArch}.tar.gz -o /tmp/go.tgz \
      && tar -C /usr/local -xzf /tmp/go.tgz \
      && ln -sf /usr/local/go/bin/go /usr/local/bin/go \
      && ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  # Rust (rustup, default stable)
  - |
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal
  # Expo + EAS
  - npm install -g expo-cli eas-cli
  # Yaver CLI itself (the user reaches this box via yaver serve / MCP /
  # HTTP, so the CLI is the primary admin surface).
  - |
    ( curl -fsSL "${yaverReleaseUrl}" -o /usr/local/bin/yaver \
      && chmod +x /usr/local/bin/yaver \
      && /usr/local/bin/yaver --version >/dev/null 2>&1 ) || echo "[cloud-init] yaver install skipped"
  # Basic UFW — SSH, HTTP, HTTPS, yaver HTTP, QUIC relay port.
  - ufw allow 22/tcp
  - ufw allow 80/tcp
  - ufw allow 443/tcp
  - ufw allow 18080/tcp
  - ufw allow 4433/udp
  - ufw --force enable || true

  # ── TLS reconciler ─────────────────────────────────────────────
  # Write the per-machine auth file. The token is long-lived; its
  # hash is stored in Convex and compared on every /machine/* call.
  - mkdir -p /etc/yaver
  - |
    cat > /etc/yaver/machine.json <<'EOF'
    {"machineId":"${machineIdStr}","machineToken":"${machineToken}","convexSite":"${convexSite}"}
    EOF
  - chmod 0600 /etc/yaver/machine.json

  # Install the reconciler — bash + jq + curl + certbot, all already
  # in the package list above. Runs every 5 minutes via a systemd timer
  # (below). On each tick:
  #   1. GET /machine/pending-tls for this machine → list of verified
  #      userDomains rows that need a cert issued.
  #   2. For each, write an nginx http-only server block, reload nginx,
  #      run certbot --nginx to upgrade it to https (also sets the
  #      auto-renew timer).
  #   3. POST /machine/tls-issued or /machine/tls-error back to Convex
  #      so the web UI flips the row from "verified" → "active".
  - |
    cat > /usr/local/bin/yaver-tls-reconciler <<'SCRIPT'
    #!/usr/bin/env bash
    set -euo pipefail
    conf=/etc/yaver/machine.json
    MACHINE_ID=$(jq -r .machineId "$conf")
    MACHINE_TOKEN=$(jq -r .machineToken "$conf")
    CONVEX=$(jq -r .convexSite "$conf")
    resp=$(curl -fsSL -H "X-Machine-Token: $MACHINE_TOKEN" \
      "$CONVEX/machine/pending-tls?machineId=$MACHINE_ID" || echo '{"domains":[]}')
    echo "$resp" | jq -r '.domains[]?.domain' | while read -r d; do
      [ -z "$d" ] && continue
      echo "[yaver-tls] issuing cert for $d"
      conf_file="/etc/nginx/sites-available/$d"
      if [ ! -f "$conf_file" ]; then
        cat > "$conf_file" <<NGINX
    server {
        listen 80;
        server_name $d;
        location / {
            proxy_pass http://127.0.0.1:18080;
            proxy_set_header Host \$host;
            proxy_set_header X-Real-IP \$remote_addr;
            proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto \$scheme;
            proxy_read_timeout 300s;
            proxy_buffering off;
        }
    }
    NGINX
        ln -sf "$conf_file" "/etc/nginx/sites-enabled/$d"
        nginx -t && systemctl reload nginx
      fi
      if certbot --nginx -d "$d" --non-interactive --agree-tos \
           -m admin@yaver.io --redirect --no-eff-email; then
        curl -fsSL -X POST "$CONVEX/machine/tls-issued" \
          -H "Content-Type: application/json" \
          -H "X-Machine-Token: $MACHINE_TOKEN" \
          -d "{\"machineId\":\"$MACHINE_ID\",\"domain\":\"$d\"}" >/dev/null || true
      else
        curl -fsSL -X POST "$CONVEX/machine/tls-error" \
          -H "Content-Type: application/json" \
          -H "X-Machine-Token: $MACHINE_TOKEN" \
          -d "{\"machineId\":\"$MACHINE_ID\",\"domain\":\"$d\",\"error\":\"certbot failed\"}" >/dev/null || true
      fi
    done
    SCRIPT
  - chmod +x /usr/local/bin/yaver-tls-reconciler

  # Systemd service + timer: runs every 5 min.
  - |
    cat > /etc/systemd/system/yaver-tls.service <<'EOF'
    [Unit]
    Description=Yaver TLS reconciler
    After=network-online.target nginx.service
    [Service]
    Type=oneshot
    ExecStart=/usr/local/bin/yaver-tls-reconciler
    EOF
  - |
    cat > /etc/systemd/system/yaver-tls.timer <<'EOF'
    [Unit]
    Description=Yaver TLS reconciler (5-min)
    [Timer]
    OnBootSec=3min
    OnUnitActiveSec=5min
    Unit=yaver-tls.service
    [Install]
    WantedBy=timers.target
    EOF
  - systemctl daemon-reload
  - systemctl enable --now yaver-tls.timer

  ${
    isGpu
      ? `# GPU tier: NVIDIA drivers + Ollama
  - apt-get install -y nvidia-driver-550
  - |
    curl -fsSL https://ollama.com/install.sh | sh
  - systemctl enable ollama
`
      : ""
  }
`;

    try {
      // ── 1. Hetzner server ───────────────────────────────────────
      const hetznerResp = await fetch("https://api.hetzner.cloud/v1/servers", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${HCLOUD_TOKEN}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          name: serverName,
          server_type: specDef.hetznerType,
          image: "ubuntu-24.04",
          location,
          labels: {
            service: "yaver-cloud-machine",
            machine_type: machine.machineType,
            user: machine.userId.toString().substring(0, 10),
            managed: "true",
          },
          user_data: cloudInit,
        }),
      });
      if (!hetznerResp.ok) {
        const errText = await hetznerResp.text();
        throw new Error(`Hetzner API ${hetznerResp.status}: ${errText}`);
      }
      const hetznerData = (await hetznerResp.json()) as {
        server: { id: number; public_net: { ipv4: { ip: string } } };
      };
      const hetznerServerId = String(hetznerData.server.id);
      const serverIp = hetznerData.server.public_net.ipv4.ip;

      // ── 2. Cloudflare DNS for the auto subdomain ───────────────
      const cfResp = await fetch(
        `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records`,
        {
          method: "POST",
          headers: {
            Authorization: `Bearer ${CF_API_TOKEN}`,
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            type: "A",
            name: subdomain,
            content: serverIp,
            proxied: false,
            ttl: 60,
          }),
        },
      );
      const cfData = (await cfResp.json()) as { success?: boolean; errors?: unknown };
      if (!cfData.success) {
        console.error("[cloudMachines.provision] Cloudflare A error:", cfData.errors);
        // Non-fatal — user can still reach server by IP.
      }

      // ── 3. Optional custom-domain binding ───────────────────────
      if (args.customDomain) {
        await ctx.runMutation(internal.userDomains.recordBinding, {
          userId: machine.userId,
          domain: args.customDomain,
          targetType: "cloud_machine",
          targetId: machine._id.toString(),
          serverIp,
          autoDomain,
        });
      }

      // ── 4. Save the Hetzner IDs + token hash back to the row ─────
      await ctx.runMutation(internal.cloudMachines.setProvisioned, {
        machineId: args.machineId,
        hetznerServerId,
        serverIp,
        hostname: autoDomain,
        machineTokenHash,
      });

      // ── 5. Health check in 5 minutes ────────────────────────────
      await ctx.scheduler.runAfter(5 * 60 * 1000, internal.cloudMachines.healthCheck, {
        machineId: args.machineId,
        attempt: 1,
      });

      console.log(
        `[cloudMachines.provision] provisioned ${serverName} (${serverIp}) → ${autoDomain}`,
      );
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e);
      console.error("[cloudMachines.provision] failed:", msg);
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId: args.machineId,
        status: "error",
        errorMessage: msg,
      });
    }
  },
});

/** Health check that pings the provisioned machine's /health endpoint. */
export const healthCheck = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    attempt: v.number(),
  },
  handler: async (ctx, { machineId, attempt }) => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine || machine.status !== "provisioning") return;
    if (!machine.hostname) return;

    let healthy = false;
    for (const proto of ["https", "http"]) {
      try {
        const resp = await fetch(`${proto}://${machine.hostname}:18080/health`, {
          signal: AbortSignal.timeout(10_000),
        });
        if (resp.ok) {
          const data = (await resp.json()) as { ok?: boolean };
          if (data.ok) {
            healthy = true;
            break;
          }
        }
      } catch {
        /* try next protocol */
      }
    }

    if (healthy) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId,
        status: "active",
      });
      console.log(`[cloudMachines.healthCheck] active: ${machine.hostname}`);
      return;
    }
    if (attempt >= 10) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId,
        status: "error",
        errorMessage: "Health check timed out after 10 attempts",
      });
      return;
    }
    await ctx.scheduler.runAfter(2 * 60 * 1000, internal.cloudMachines.healthCheck, {
      machineId,
      attempt: attempt + 1,
    });
  },
});

/** Update machine status (called by provisioning scripts via webhook). */
export const updateStatus = mutation({
  args: {
    machineId: v.id("cloudMachines"),
    status: v.string(),
    serverIp: v.optional(v.string()),
    hostname: v.optional(v.string()),
    hetznerServerId: v.optional(v.string()),
    errorMessage: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const machine = await ctx.db.get(args.machineId);
    if (!machine) throw new Error("Machine not found");

    const updates: Record<string, unknown> = {
      status: args.status,
      updatedAt: Date.now(),
    };
    if (args.serverIp) updates.serverIp = args.serverIp;
    if (args.hostname) updates.hostname = args.hostname;
    if (args.hetznerServerId) updates.hetznerServerId = args.hetznerServerId;
    if (args.errorMessage) updates.errorMessage = args.errorMessage;
    if (args.status === "active") updates.lastHealthCheck = Date.now();

    await ctx.db.patch(args.machineId, updates);
  },
});

/** Stop and deprovision a machine. */
export const deprovision = mutation({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    const machine = await ctx.db.get(machineId);
    if (!machine) throw new Error("Machine not found");

    await ctx.db.patch(machineId, {
      status: "stopping",
      updatedAt: Date.now(),
    });

    // Schedule the real destroy (Hetzner API call) in an action.
    await ctx.scheduler.runAfter(0, internal.cloudMachines.destroy, { machineId });
  },
});

/** Action that actually deletes the Hetzner server + Cloudflare DNS record. */
export const destroy = internalAction({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    const HCLOUD_TOKEN = process.env.HCLOUD_TOKEN;
    const CF_API_TOKEN = process.env.CF_API_TOKEN;
    const CF_ZONE_ID = process.env.CF_ZONE_ID;
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    if (!machine) return;

    if (HCLOUD_TOKEN && machine.hetznerServerId) {
      try {
        await fetch(`https://api.hetzner.cloud/v1/servers/${machine.hetznerServerId}`, {
          method: "DELETE",
          headers: { Authorization: `Bearer ${HCLOUD_TOKEN}` },
        });
      } catch (e) {
        console.error("[cloudMachines.destroy] hetzner delete error:", e);
      }
    }

    if (CF_API_TOKEN && CF_ZONE_ID && machine.hostname) {
      try {
        const listResp = await fetch(
          `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records?name=${machine.hostname}`,
          { headers: { Authorization: `Bearer ${CF_API_TOKEN}` } },
        );
        const listData = (await listResp.json()) as { result?: { id: string }[] };
        const recordId = listData.result?.[0]?.id;
        if (recordId) {
          await fetch(
            `https://api.cloudflare.com/client/v4/zones/${CF_ZONE_ID}/dns_records/${recordId}`,
            { method: "DELETE", headers: { Authorization: `Bearer ${CF_API_TOKEN}` } },
          );
        }
      } catch (e) {
        console.error("[cloudMachines.destroy] cloudflare delete error:", e);
      }
    }

    await ctx.runMutation(internal.cloudMachines.setStatus, {
      machineId,
      status: "stopped",
    });
  },
});
