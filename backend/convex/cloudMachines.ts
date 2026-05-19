import { mutation, query, internalMutation, internalAction, internalQuery } from "./_generated/server";
import { v } from "convex/values";
import { api, internal } from "./_generated/api";
import { listActiveInfraGrantsForGuest, listGrantedMachineIdsForGrant } from "./access";
import { randomHex, sha256Hex } from "./auth";

// Machine specs by type. The Hetzner server_type strings are what you pass
// to POST https://api.hetzner.cloud/v1/servers.
const MACHINE_SPECS = {
  cpu: {
    // History: cx42 DEPRECATED (422 "server type 106 is deprecated");
    // cpx41 non-deprecated but US-only stock; ccx33 (dedicated) works
    // EU+US but costs €73.99/mo — unviable vs the $/mo price.
    // cpx42 (shared AMD, 8 vCPU/16 GB/320 GB, x86=amd64, €29.99/mo)
    // is the chosen balance: enough RAM for RN/Hermes (Metro+hermesc,
    // no swap) at a price the SKU can sustain. Orderable in fsn1,
    // nbg1, hel1, sin — NOT ash/hil, so the eu→fsn1 map below works
    // but a us-region purchase needs a separate type/location (see
    // the region→location map; us currently falls back to ash which
    // has NO cpx42 → must be handled before selling a us SKU).
    // Re-verify with GET /v1/datacenters .server_types.available
    // (by type id) before changing — "priced" != "orderable".
    hetznerType: "cpx42",    // 8 vCPU, 16 GB RAM, 320 GB, amd64 (shared)
    vcpu: 8,
    ramGb: 16,
    diskGb: 320,
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

type ManagedCloudBootstrapSpec = {
  convexSite: string;
  machineId: string;
  machineToken: string;
  userSessionToken: string;
  deviceId: string;
  hostname: string;
  yaverArch: "amd64" | "arm64";
  yaverReleaseUrl: string;
  repoUrl?: string;
  gpu: boolean;
  // "hosted" ⇒ also run a self-hosted Convex (Docker) on the box so
  // deploys target the box itself (no Convex Cloud, no BYOK keys).
  // Absent/"byok" leaves the cloud-init byte-identical.
  tier?: "byok" | "hosted";
};

type CreateCloudMachineArgs = {
  userId: string;
  machineType: string;
  teamId?: string;
  region?: string;
  repoUrl?: string;
  sshPublicKey?: string;
  subscriptionId?: string;
  customDomain?: string;
  tier?: "byok" | "hosted";
};

function shellSingleQuote(value: string): string {
  return `'${value.replace(/'/g, `'\"'\"'`)}'`;
}

function jsonString(value: string): string {
  return JSON.stringify(value);
}

// buildManagedCloudInitContainer — thin cloud-init for the "yaver
// image" model: the VM only installs Docker, drops the per-user
// config into a host dir, and `docker run`s ghcr.io/.../yaver-cloud
// (the image already has the agent + every tool). The /srv/yaver/state
// dir is the container's /root volume, so remote-OAuth tokens
// (yaver/claude/codex/opencode/gh/glab) + the GLM api-key persist
// across container restarts and image upgrades. nginx+certbot still
// run on the HOST and proxy :443 → the container's published :18080.
// Single-user safe (one buyer, one dedicated VM). Multi-tenant Phase 2
// swaps `docker run` for a per-tenant Kata/Firecracker microVM — a
// plain shared-kernel container is NOT a no-code-leak boundary for
// untrusted tenants.
export function buildManagedCloudInitContainer(
  spec: ManagedCloudBootstrapSpec,
  image: string,
): string {
  const repoClone = spec.repoUrl
    ? `  - |
    if [ ! -d /srv/yaver/state/srv/yaver/workspace/.git ]; then
      mkdir -p /srv/yaver/state/srv/yaver/workspace
      git clone ${shellSingleQuote(spec.repoUrl)} /srv/yaver/state/srv/yaver/workspace || echo "[cloud-init] repo clone skipped"
    fi
`
    : "";

  // Hosted tier in the container model (SANDBOX_HOSTED_HANDOFF.md
  // §convergence). A self-hosted Convex runs as a HOST-side sibling
  // container (publishes 127.0.0.1:3210/3211). The admin-key file goes
  // on the PERSISTED state volume (/srv/yaver/state/.yaver/...) so the
  // agent — which runs INSIDE the yaver container with /root mounted
  // from that volume — reads it at /root/.yaver/convex-selfhosted.json
  // (passed via CONVEX_SELFHOSTED_FILE). It survives container
  // restarts / image upgrades. byok ⇒ empty (byte-identical path).
  // The admin key NEVER leaves the box (privacy contract).
  const hostedConvex =
    spec.tier === "hosted"
      ? `  # ── Hosted tier: self-hosted Convex (HOST-side sibling) ───────────
  - |
    INSTANCE_SECRET=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \\n')
    docker volume create yaver-convex-data >/dev/null 2>&1 || true
    docker rm -f yaver-convex >/dev/null 2>&1 || true
    docker run -d --name yaver-convex --restart always \\
      -p 127.0.0.1:3210:3210 -p 127.0.0.1:3211:3211 \\
      -v yaver-convex-data:/convex/data \\
      -e INSTANCE_NAME=yaver \\
      -e INSTANCE_SECRET="$INSTANCE_SECRET" \\
      -e CONVEX_CLOUD_ORIGIN=${shellSingleQuote(`https://${spec.hostname}/_convex-api`)} \\
      -e CONVEX_SITE_ORIGIN=${shellSingleQuote(`https://${spec.hostname}/_convex-http`)} \\
      ghcr.io/get-convex/convex-backend:latest \\
      || echo "[cloud-init] convex-backend start skipped"
  - |
    for i in $(seq 1 40); do
      (echo > /dev/tcp/127.0.0.1/3210) >/dev/null 2>&1 && break
      sleep 5
    done
    ADMIN_KEY=$(docker exec yaver-convex ./generate_admin_key.sh 2>/dev/null | tail -n1 || true)
    mkdir -p /srv/yaver/state/.yaver
    cat > /srv/yaver/state/.yaver/convex-selfhosted.json <<EOF
    {"url":"https://${spec.hostname}/_convex-api","adminKey":"$ADMIN_KEY"}
    EOF
    chmod 0600 /srv/yaver/state/.yaver/convex-selfhosted.json
    echo "[cloud-init] self-hosted Convex bootstrap done (key bytes: \${#ADMIN_KEY})"
  # nginx snippet (WS on /_convex-api) — included by every server block.
  - |
    cat > /etc/nginx/snippets/yaver-convex.conf <<'NGINX'
    location /_convex-api/ {
      proxy_pass http://127.0.0.1:3210/;
      proxy_http_version 1.1;
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection "upgrade";
      proxy_set_header Host $host;
      proxy_read_timeout 600s; proxy_buffering off;
    }
    location /_convex-http/ {
      proxy_pass http://127.0.0.1:3211/;
      proxy_set_header Host $host;
      proxy_read_timeout 600s; proxy_buffering off;
    }
    NGINX
`
      : `  - mkdir -p /etc/nginx/snippets
  - ': > /etc/nginx/snippets/yaver-convex.conf'
`;
  // The yaver container always gets CONVEX_SELFHOSTED_FILE; the agent
  // only acts on it when the file exists (hosted), so byok is unaffected.
  const selfhostedEnv =
    `-e CONVEX_SELFHOSTED_FILE=/root/.yaver/convex-selfhosted.json`;

  // Non-fatal onboarding-phase tick → /machine/phase (machine-token
  // authed). Values inlined at build time (no machine.json timing
  // dependency); `|| true` so a reporting hiccup never breaks the
  // proven provision. Drives the web/mobile progress bar.
  const phasePost = (phase: string) =>
    `  - 'curl -fsS -m 8 -X POST -H "X-Machine-Token: ${spec.machineToken}" -H "Content-Type: application/json" -d ${shellSingleQuote(
      JSON.stringify({ machineId: spec.machineId, phase }),
    )} ${spec.convexSite}/machine/phase >/dev/null 2>&1 || true'\n`;

  return `#cloud-config
package_update: true
packages:
  - ca-certificates
  - curl
  - jq
  - nginx
  - certbot
  - python3-certbot-nginx
  - ufw
runcmd:
${phasePost("installing-docker")}  - curl -fsSL https://get.docker.com | sh
  - systemctl enable --now docker
  - ufw allow 22/tcp
  - ufw allow 80/tcp
  - ufw allow 443/tcp
  - ufw --force enable || true
  # ── per-user config into the container's /root volume ──────────────
  - mkdir -p /srv/yaver/state/.yaver /etc/yaver
  - |
    cat > /srv/yaver/state/.yaver/config.json <<'EOF'
    {
      "auth_token": ${jsonString(spec.userSessionToken)},
      "convex_site_url": ${jsonString(spec.convexSite)},
      "device_id": ${jsonString(spec.deviceId)}
    }
    EOF
  - chmod 0600 /srv/yaver/state/.yaver/config.json
${repoClone}  # ── run the yaver image (the box IS this container) ────────────────
${phasePost("pulling-image")}  - docker pull ${shellSingleQuote(image)}
  - |
    docker rm -f yaver 2>/dev/null || true
    docker run -d --name yaver --restart always \
      -p 18080:18080 \
      -e YAVER_HOSTNAME=${shellSingleQuote(spec.hostname)} \
      ${selfhostedEnv} \
      -v /srv/yaver/state:/root \
      ${shellSingleQuote(image)}
${phasePost("registering")}  - mkdir -p /etc/nginx/snippets
${hostedConvex}  # ── host TLS reconciler (same contract as the VM path) ─────────────
  - |
    cat > /etc/yaver/machine.json <<'EOF'
    {"machineId":${jsonString(spec.machineId)},"machineToken":${jsonString(spec.machineToken)},"convexSite":${jsonString(spec.convexSite)}}
    EOF
  - chmod 0600 /etc/yaver/machine.json
  - |
    cat > /usr/local/bin/yaver-tls-reconciler <<'SCRIPT'
    #!/usr/bin/env bash
    set -euo pipefail
    conf=/etc/yaver/machine.json
    MID=$(jq -r .machineId "$conf"); MT=$(jq -r .machineToken "$conf"); CV=$(jq -r .convexSite "$conf")
    resp=$(curl -fsSL -H "X-Machine-Token: $MT" "$CV/machine/pending-tls?machineId=$MID" || echo '{"domains":[]}')
    echo "$resp" | jq -r '.domains[]?.domain' | while read -r d; do
      [ -z "$d" ] && continue
      cf="/etc/nginx/sites-available/$d"
      if [ ! -f "$cf" ]; then
        cat > "$cf" <<NGINX
    server { listen 80; server_name $d;
      include /etc/nginx/snippets/yaver-convex.conf;
      location / { proxy_pass http://127.0.0.1:18080; proxy_set_header Host \\$host;
        proxy_set_header X-Real-IP \\$remote_addr; proxy_set_header X-Forwarded-For \\$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \\$scheme; proxy_read_timeout 300s; proxy_buffering off; } }
    NGINX
        ln -sf "$cf" "/etc/nginx/sites-enabled/$d"; nginx -t && systemctl reload nginx
      fi
      certbot --nginx -d "$d" --non-interactive --agree-tos -m admin@yaver.io --redirect --no-eff-email \
        && curl -fsSL -X POST "$CV/machine/tls-issued" -H "Content-Type: application/json" \
             -H "X-Machine-Token: $MT" -d "{\\"machineId\\":\\"$MID\\",\\"domain\\":\\"$d\\"}" >/dev/null \
        || curl -fsSL -X POST "$CV/machine/tls-error" -H "Content-Type: application/json" \
             -H "X-Machine-Token: $MT" -d "{\\"machineId\\":\\"$MID\\",\\"domain\\":\\"$d\\",\\"error\\":\\"certbot failed\\"}" >/dev/null || true
    done
    SCRIPT
  - chmod +x /usr/local/bin/yaver-tls-reconciler
  - |
    cat > /etc/systemd/system/yaver-tls.service <<'EOF'
    [Unit]
    Description=Yaver TLS reconciler
    After=network-online.target nginx.service docker.service
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
`;
}

export function buildManagedCloudInit(spec: ManagedCloudBootstrapSpec): string {
  const repoCloneSnippet = spec.repoUrl
    ? `  # Optional starter repo clone
  - |
    if [ ! -d /srv/yaver/workspace/.git ]; then
      git clone ${shellSingleQuote(spec.repoUrl)} /srv/yaver/workspace || echo "[cloud-init] repo clone skipped"
      chown -R root:root /srv/yaver/workspace
    fi
`
    : "";

  // Hosted tier: run a self-hosted Convex on the box (Docker, official
  // image). Deploys then target the box itself — no Convex Cloud
  // account, no BYOK key. Best-effort + loud logging (spike): the
  // health check / verification step surfaces a half-start. The
  // tenant's data lives in the yaver-convex-data volume on THEIR own
  // dedicated box — central Convex never sees it (privacy contract).
  const hostedSnippet =
    spec.tier === "hosted"
      ? `  # ── Hosted tier: self-hosted Convex (Docker) ──────────────────
  - |
    INSTANCE_SECRET=$(od -An -tx1 -N32 /dev/urandom | tr -d ' \\n')
    docker volume create yaver-convex-data >/dev/null 2>&1 || true
    docker rm -f yaver-convex >/dev/null 2>&1 || true
    docker run -d --name yaver-convex --restart always \\
      -p 127.0.0.1:3210:3210 -p 127.0.0.1:3211:3211 \\
      -v yaver-convex-data:/convex/data \\
      -e INSTANCE_NAME=yaver \\
      -e INSTANCE_SECRET="$INSTANCE_SECRET" \\
      -e CONVEX_CLOUD_ORIGIN=${shellSingleQuote(`https://${spec.hostname}/_convex-api`)} \\
      -e CONVEX_SITE_ORIGIN=${shellSingleQuote(`https://${spec.hostname}/_convex-http`)} \\
      ghcr.io/get-convex/convex-backend:latest \\
      || echo "[cloud-init] convex-backend start skipped"
  - |
    # Wait for the backend port, then mint an admin key the deploy
    # path (Phase 2) reads. 0600, root-only — never leaves the box.
    for i in $(seq 1 40); do
      (echo > /dev/tcp/127.0.0.1/3210) >/dev/null 2>&1 && break
      sleep 5
    done
    ADMIN_KEY=$(docker exec yaver-convex ./generate_admin_key.sh 2>/dev/null | tail -n1 || true)
    mkdir -p /etc/yaver
    cat > /etc/yaver/convex-selfhosted.json <<EOF
    {"url":"https://${spec.hostname}/_convex-api","adminKey":"$ADMIN_KEY"}
    EOF
    chmod 0600 /etc/yaver/convex-selfhosted.json
    echo "[cloud-init] self-hosted Convex bootstrap done (key bytes: \${#ADMIN_KEY})"
`
      : "";

  const gpuSnippet = spec.gpu
    ? `  # GPU tier: NVIDIA drivers + Ollama
  - apt-get install -y nvidia-driver-550
  - |
    curl -fsSL https://ollama.com/install.sh | sh
  - systemctl enable ollama
`
    : "";

  return `#cloud-config
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
  - bubblewrap
  - uidmap
runcmd:
  - systemctl enable docker && systemctl start docker
  - |
    cat > /etc/sysctl.d/99-yaver-runner-sandbox.conf <<'EOF'
    kernel.unprivileged_userns_clone=1
    user.max_user_namespaces=1048576
    EOF
    if [ -f /proc/sys/kernel/apparmor_restrict_unprivileged_userns ]; then
      echo 'kernel.apparmor_restrict_unprivileged_userns=0' >> /etc/sysctl.d/99-yaver-runner-sandbox.conf
    fi
    sysctl --system >/dev/null 2>&1 || true
  # Node.js 22 LTS
  - curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
  - apt-get install -y nodejs
  # Go 1.22
  - |
    curl -fsSL https://go.dev/dl/go1.22.6.linux-${spec.yaverArch}.tar.gz -o /tmp/go.tgz \
      && tar -C /usr/local -xzf /tmp/go.tgz \
      && ln -sf /usr/local/go/bin/go /usr/local/bin/go \
      && ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
  # Rust (rustup, default stable)
  - |
    curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal
  # Expo + EAS + hosted coding runners
  - npm install -g expo-cli eas-cli
  - |
    missing_pkgs=""
    command -v claude >/dev/null 2>&1 || missing_pkgs="$missing_pkgs @anthropic-ai/claude-code"
    command -v codex >/dev/null 2>&1 || missing_pkgs="$missing_pkgs @openai/codex"
    command -v opencode >/dev/null 2>&1 || missing_pkgs="$missing_pkgs opencode-ai"
    if [ -n "$missing_pkgs" ]; then
      npm install -g $missing_pkgs
    fi
  # Yaver Go agent — release-cli.yml ships the binary INSIDE
  # yaver-linux-<arch>.tar.gz as a single file named \`yaver\`, never
  # as a raw asset (raw-binary release paths were removed; npm-only
  # distribution). Fetch the tarball, extract, install. If this fails
  # the box has no agent and the health check fails loudly — never a
  # silent half-provisioned box.
  - |
    ( curl -fsSL "${spec.yaverReleaseUrl}" -o /tmp/yaver.tgz \
      && tar -xzf /tmp/yaver.tgz -C /usr/local/bin yaver \
      && chmod +x /usr/local/bin/yaver \
      && rm -f /tmp/yaver.tgz \
      && /usr/local/bin/yaver --version >/dev/null 2>&1 ) || echo "[cloud-init] yaver install skipped"
  # Basic UFW — SSH, HTTP, HTTPS, yaver HTTP, QUIC relay port.
  - ufw allow 22/tcp
  - ufw allow 80/tcp
  - ufw allow 443/tcp
  - ufw allow 18080/tcp
  - ufw allow 4433/udp
  - ufw --force enable || true

  # ── Managed Yaver agent bootstrap ─────────────────────────────
  - mkdir -p /root/.yaver /srv/yaver/workspace /etc/yaver
  - |
    cat > /root/.yaver/config.json <<'EOF'
    {
      "auth_token": ${jsonString(spec.userSessionToken)},
      "convex_site_url": ${jsonString(spec.convexSite)},
      "device_id": ${jsonString(spec.deviceId)}
    }
    EOF
  - chmod 0600 /root/.yaver/config.json
  - |
    cat > /etc/systemd/system/yaver-agent.service <<'EOF'
    [Unit]
    Description=Yaver managed cloud agent
    Wants=network-online.target
    After=network-online.target docker.service

    [Service]
    Type=simple
    WorkingDirectory=/srv/yaver/workspace
    Environment=HOME=/root
    Environment=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/root/.cargo/bin:/usr/local/go/bin
    Environment=YAVER_HOSTNAME=${jsonString(spec.hostname)}
    ExecStart=/usr/local/bin/yaver serve --debug --port 18080
    Restart=always
    RestartSec=5

    [Install]
    WantedBy=multi-user.target
    EOF
${repoCloneSnippet}  - systemctl daemon-reload
  - systemctl enable --now yaver-agent

  # ── TLS reconciler ─────────────────────────────────────────────
  - |
    cat > /etc/yaver/machine.json <<'EOF'
    {"machineId":${jsonString(spec.machineId)},"machineToken":${jsonString(spec.machineToken)},"convexSite":${jsonString(spec.convexSite)}}
    EOF
  - chmod 0600 /etc/yaver/machine.json
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
        # Hosted-tier self-hosted Convex (Docker) lives on loopback
        # 3210 (API, WebSocket) / 3211 (HTTP actions). On a byok box
        # nothing listens there — these paths just 502 and are never
        # used, so the block is safe to always emit (one nginx path).
        location /_convex-api/ {
            proxy_pass http://127.0.0.1:3210/;
            proxy_http_version 1.1;
            proxy_set_header Upgrade \$http_upgrade;
            proxy_set_header Connection "upgrade";
            proxy_set_header Host \$host;
            proxy_set_header X-Forwarded-Proto \$scheme;
            proxy_read_timeout 600s;
            proxy_buffering off;
        }
        location /_convex-http/ {
            proxy_pass http://127.0.0.1:3211/;
            proxy_set_header Host \$host;
            proxy_set_header X-Forwarded-Proto \$scheme;
            proxy_read_timeout 600s;
            proxy_buffering off;
        }
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
  - |
    cat > /etc/systemd/system/yaver-tls.service <<'EOF'
    [Unit]
    Description=Yaver TLS reconciler
    After=network-online.target nginx.service yaver-agent.service
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

${hostedSnippet}${gpuSnippet}`;
}

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

export const listBySubscription = internalQuery({
  args: { subscriptionId: v.id("subscriptions") },
  handler: async (ctx, { subscriptionId }) => {
    const rows = await ctx.db.query("cloudMachines").collect();
    return rows.filter((machine) => machine.subscriptionId === subscriptionId);
  },
});

// ─── Mutations ──────────────────────────────────────────────────

async function createCloudMachine(
  ctx: { db: any; scheduler: any },
  args: CreateCloudMachineArgs,
) {
  const specDef = MACHINE_SPECS[args.machineType as keyof typeof MACHINE_SPECS];
  if (!specDef) throw new Error("Invalid machine type: " + args.machineType);

  const now = Date.now();
  const tools = [
    "nodejs",
    "python",
    "go",
    "rust",
    "docker",
    "expo-cli",
    "eas-cli",
    "claude-code",
    "codex",
    "opencode",
  ];
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
    origin: "managed", // every cloudMachines row is a Yaver-side box
    tier: args.tier ?? "byok",
    status: "provisioning",
    provisionPhase: "creating",
    provisionProgress: 5,
    provisionPhaseAt: now,
    runnersAuthorized: false,
    multiUser: !!args.teamId,
    region: args.region ?? "eu",
    tools,
    repoUrl: args.repoUrl,
    sshPublicKey: args.sshPublicKey,
    specs,
    createdAt: now,
    updatedAt: now,
  });

  await ctx.scheduler.runAfter(0, internal.cloudMachines.provision, {
    machineId,
    customDomain: args.customDomain,
  });

  return machineId;
}

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
    tier: v.optional(v.union(v.literal("byok"), v.literal("hosted"))),
  },
  handler: async (ctx, args) => createCloudMachine(ctx, args),
});

// adoptExisting registers an ALREADY-RUNNING Hetzner box as a managed
// cloudMachines row WITHOUT provisioning a new server. Used by the
// owner-gated /billing/yaver-cloud/dev-adopt route to imitate "bought
// from Yaver managed cloud" for an existing box (e.g. the test
// ephemeral) so the managed teardown path can be exercised end-to-end
// without LemonSqueezy. It deliberately does NOT schedule
// internal.cloudMachines.provision (the box already exists).
export const adoptExisting = internalMutation({
  args: {
    userId: v.id("users"),
    hetznerServerId: v.string(),
    region: v.optional(v.string()),
    serverIp: v.optional(v.string()),
    hostname: v.optional(v.string()),
    deviceId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const spec = MACHINE_SPECS["cpu" as keyof typeof MACHINE_SPECS];
    const now = Date.now();
    return await ctx.db.insert("cloudMachines", {
      userId: args.userId,
      machineType: "cpu",
      origin: "managed",
      status: "active",
      multiUser: false,
      hetznerServerId: args.hetznerServerId,
      serverIp: args.serverIp,
      hostname: args.hostname,
      deviceId: args.deviceId,
      region: args.region ?? "eu",
      tools: [],
      specs: {
        vcpu: spec.vcpu,
        ramGb: spec.ramGb,
        diskGb: spec.diskGb,
        arch: spec.arch,
      },
      createdAt: now,
      updatedAt: now,
    });
  },
});

export const ensureForSubscription = mutation({
  args: {
    userId: v.id("users"),
    machineType: v.string(),
    teamId: v.optional(v.string()),
    region: v.optional(v.string()),
    repoUrl: v.optional(v.string()),
    sshPublicKey: v.optional(v.string()),
    subscriptionId: v.id("subscriptions"),
    customDomain: v.optional(v.string()),
    tier: v.optional(v.union(v.literal("byok"), v.literal("hosted"))),
  },
  handler: async (ctx, args) => {
    const existing = (await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q) => q.eq("userId", args.userId))
      .collect())
      .find(
        (machine) =>
          machine.subscriptionId === args.subscriptionId &&
          machine.machineType === args.machineType &&
          machine.status !== "stopped" &&
          machine.status !== "error",
      );
    if (existing) {
      // Resubscribe during the hosted grace window → cancel the
      // pending destroy and bring the box back. Their app + DB never
      // left the box, so this is a true resume, not a rebuild.
      if (existing.status === "grace") {
        if (existing.scheduledDestroyId) {
          await ctx.scheduler.cancel(existing.scheduledDestroyId);
        }
        await ctx.db.patch(existing._id, {
          status: "active",
          deprovisionAt: undefined,
          scheduledDestroyId: undefined,
          updatedAt: Date.now(),
        });
      }
      return existing._id;
    }

    return await createCloudMachine(ctx, args);
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
    deviceId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const patch: Record<string, unknown> = {
      hetznerServerId: args.hetznerServerId,
      serverIp: args.serverIp,
      hostname: args.hostname,
      // Clear any stale error from a prior failed attempt — this row
      // just provisioned successfully, so a leftover errorMessage
      // would render a scary (false) failure on the device card.
      errorMessage: undefined,
      updatedAt: Date.now(),
    };
    if (args.machineTokenHash) patch.machineTokenHash = args.machineTokenHash;
    if (args.deviceId) patch.deviceId = args.deviceId;
    await ctx.db.patch(args.machineId, patch);
  },
});

export const setStatus = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    status: v.string(),
    errorMessage: v.optional(v.string()),
    lastSnapshotId: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const patch: Record<string, unknown> = {
      status: args.status,
      updatedAt: Date.now(),
    };
    if (args.errorMessage) patch.errorMessage = args.errorMessage;
    // Healthy states clear a stale error from a prior failed attempt.
    else if (args.status === "active" || args.status === "provisioning")
      patch.errorMessage = undefined;
    if (args.status === "active") patch.lastHealthCheck = Date.now();
    if (args.lastSnapshotId) {
      patch.lastSnapshotId = args.lastSnapshotId;
      patch.lastSnapshotAt = Date.now();
    }
    await ctx.db.patch(args.machineId, patch);
  },
});

// First-class onboarding phase. Called server-side (provision /
// healthCheck bookends) AND by the box cloud-init via the
// machine-token /machine/phase route between steps. Idempotent /
// monotonic-ish: callers pass increasing progress. phase "ready"
// implies status active; phase fields are privacy-safe (label +
// percent only). project_managed_cloud_onboarding_gap.
export const PROVISION_PHASES = [
  "creating", "booting", "installing-docker", "pulling-image",
  "starting-agent", "registering", "authorizing-runners", "ready", "error",
] as const;

export const setPhase = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    phase: v.string(),
    progress: v.optional(v.number()),
    runnersAuthorized: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const patch: Record<string, unknown> = {
      provisionPhase: args.phase,
      provisionPhaseAt: Date.now(),
      updatedAt: Date.now(),
    };
    if (typeof args.progress === "number") {
      patch.provisionProgress = Math.max(0, Math.min(100, args.progress));
    }
    if (typeof args.runnersAuthorized === "boolean") {
      patch.runnersAuthorized = args.runnersAuthorized;
    }
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

    // Fail-closed billing gate — a managed machine is NEVER
    // provisioned on Yaver's Hetzner account unless the subscription
    // is active OR the owner is on the env allowlist (lets the repo
    // owner develop the full Hetzner flow without LemonSqueezy; env
    // unset ⇒ pure fail-closed). dev-activate machines never reach
    // this action (they attach to a shared box, no Hetzner create),
    // so gating unconditionally here is safe and closes the old
    // subscription-less hole.
    {
      const entitled = await ctx.runQuery(internal.subscriptions.canProvisionManaged, {
        subscriptionId: machine.subscriptionId ?? undefined,
        userId: machine.userId,
      });
      if (!entitled) {
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId: args.machineId,
          status: "error",
          errorMessage:
            "Not entitled — managed provisioning denied (active subscription or owner allowlist required)",
        });
        return;
      }
    }

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
    // Release asset is the gzipped tarball, not a raw binary (see the
    // cloud-init extract step in buildManagedCloudInit).
    const yaverAsset = `yaver-linux-${yaverArch}.tar.gz`;
    const yaverReleaseUrl = `https://github.com/kivanccakmak/yaver.io/releases/latest/download/${yaverAsset}`;

    // Machine-auth token — plaintext goes into /etc/yaver/machine.json on
    // the box, only the hash is persisted. Used by the TLS reconciler
    // systemd timer to fetch pending custom-domain TLS jobs from Convex
    // (GET /machine/pending-tls) and report the result back.
    const machineToken = randomHex(24);
    const machineTokenHash = await sha256Hex(machineToken);
    // Fall back to the production deployment, never dev — this runs
    // server-side on whichever Convex deployment is live, and the dev
    // deployment must never appear in URLs handed to real user devices.
    // Set CONVEX_SITE_URL via `npx convex env set` on each deployment
    // so the explicit value still wins.
    const convexSite = process.env.CONVEX_SITE_URL || "https://perceptive-minnow-557.eu-west-1.convex.site";
    const machineIdStr = machine._id.toString();
    const userSessionToken = randomHex(32);
    const userSessionTokenHash = await sha256Hex(userSessionToken);
    const deviceId = `cloud-${shortId}`;
    const sessionExpiresAt = Date.now() + 365 * 24 * 60 * 60 * 1000;

    const bootstrapSpec = {
      convexSite,
      machineId: machineIdStr,
      machineToken,
      userSessionToken,
      deviceId,
      hostname: autoDomain,
      yaverArch,
      yaverReleaseUrl,
      repoUrl: machine.repoUrl,
      gpu: isGpu,
      // Hosted-tier flag (Session B / SANDBOX_HOSTED_HANDOFF.md) —
      // drives the self-hosted-Convex + admin-key + nginx bootstrap
      // inside buildManagedCloudInit. byok today (ensureForSubscription
      // doesn't thread tier yet); hosted once the SKU is wired.
      tier: machine.tier === "hosted" ? "hosted" : "byok",
    } as const;
    // "yaver image" model: when YAVER_CLOUD_IMAGE is set, the thin
    // Docker cloud-init handles BOTH tiers — byok (agent container
    // only) and hosted (agent container + self-hosted-Convex sibling +
    // admin-key on the persisted volume + nginx /_convex-*). Unset ⇒
    // legacy in-VM cloud-init. Both paths assert byte-identical byok
    // in cloudMachines.test.mts.
    const cloudImage = process.env.YAVER_CLOUD_IMAGE;
    const cloudInit = cloudImage
      ? buildManagedCloudInitContainer(bootstrapSpec, cloudImage)
      : buildManagedCloudInit(bootstrapSpec);

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
          // Test-only cost override: YAVER_CLOUD_CPU_TYPE swaps the cpu
          // SKU's Hetzner type (e.g. cpx22 €9.49 vs cpx42 €29.99) so
          // headless/e2e provisions are cheap throwaways. Unset ⇒ the
          // real SKU type. Only applies to machineType "cpu". Must be a
          // non-deprecated type orderable in the resolved location.
          server_type:
            machine.machineType === "cpu" && process.env.YAVER_CLOUD_CPU_TYPE
              ? process.env.YAVER_CLOUD_CPU_TYPE
              : specDef.hetznerType,
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

      await ctx.runMutation(api.auth.createSession, {
        tokenHash: userSessionTokenHash,
        userId: machine.userId,
        deviceId,
        expiresAt: sessionExpiresAt,
      });

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
        deviceId, // deterministic cloud-<shortId> the box registers as
      });

      // Server-side bookend: box exists, now booting + cloud-init
      // (the box itself POSTs the granular installing-docker /
      // pulling-image / registering ticks to /machine/phase).
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId: args.machineId,
        phase: "booting",
        progress: 30,
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
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId: args.machineId,
        phase: "error",
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
      // Agent is up. NOT "ready" yet — runner OAuth still has to be
      // pushed; the device shows "Unauthorized — Authorize runners"
      // until then (runnersAuthorized stays false).
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "authorizing-runners",
        progress: 90,
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
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "error",
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

// ─── Phase 4: hosted-tier teardown safety (pure, unit-tested) ──────

/** Grace window a hosted box is kept after subscription end. */
export const HOSTED_GRACE_MS = 7 * 24 * 60 * 60 * 1000;

/**
 * planDeprovision decides whether a teardown is immediate or deferred.
 * A hosted box holds the user's entire app + DB — deleting it the
 * instant a subscription lapses is data loss, so keep it for a grace
 * window (resubscribe / export). byok boxes are disposable and an
 * explicit force (user clicked "delete now") always deletes now.
 */
export function planDeprovision(
  tier: string | undefined,
  force: boolean,
  now: number,
): { grace: boolean; deprovisionAt?: number } {
  if (tier === "hosted" && !force) {
    return { grace: true, deprovisionAt: now + HOSTED_GRACE_MS };
  }
  return { grace: false };
}

/**
 * snapshotIsMandatory: for a hosted box the pre-delete snapshot is the
 * user's only data copy — a failed snapshot must ABORT the delete (not
 * "best-effort continue"). byok boxes are disposable.
 */
export function snapshotIsMandatory(tier: string | undefined): boolean {
  return tier === "hosted";
}

/** Stop and deprovision a machine. force=true skips the hosted grace. */
export const deprovision = mutation({
  args: { machineId: v.id("cloudMachines"), force: v.optional(v.boolean()) },
  handler: async (ctx, { machineId, force }) => {
    const machine = await ctx.db.get(machineId);
    if (!machine) throw new Error("Machine not found");

    const now = Date.now();
    const plan = planDeprovision(machine.tier, force === true, now);

    if (plan.grace && plan.deprovisionAt) {
      // Defer: keep the box serving through the grace window, schedule
      // the real destroy at the deadline, remember the job so a
      // resubscribe can cancel it.
      const scheduledDestroyId = await ctx.scheduler.runAt(
        plan.deprovisionAt,
        internal.cloudMachines.destroy,
        { machineId },
      );
      await ctx.db.patch(machineId, {
        status: "grace",
        deprovisionAt: plan.deprovisionAt,
        scheduledDestroyId,
        updatedAt: now,
      });
      return;
    }

    await ctx.db.patch(machineId, { status: "stopping", updatedAt: now });
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

    // No platform token → we CANNOT delete the real box. Never lie
    // with status:"stopped" (row says gone, box still billing). Set
    // an explicit error the UI surfaces verbatim.
    if (!HCLOUD_TOKEN) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId,
        status: "error",
        errorMessage:
          "Platform HCLOUD_TOKEN is not configured on this Convex deployment — the Hetzner box was NOT deleted. Set it with `npx convex env set HCLOUD_TOKEN <token> --prod`, then Decommission again.",
      });
      return;
    }

    let warn = "";
    const mustSnapshot = snapshotIsMandatory(machine.tier);
    if (machine.hetznerServerId) {
      // Pre-delete snapshot. For a hosted box this is the user's ONLY
      // data copy → a failure ABORTS the delete (status:error, box
      // kept). For byok it's disposable → best-effort, continue.
      let snapshotId = "";
      try {
        const snap = await fetch(`https://api.hetzner.cloud/v1/servers/${machine.hetznerServerId}/actions/create_image`, {
          method: "POST",
          headers: { Authorization: `Bearer ${HCLOUD_TOKEN}`, "Content-Type": "application/json" },
          body: JSON.stringify({ type: "snapshot", description: `yaver-predelete-machine-${machineId}-${Date.now()}` }),
        });
        if (snap.ok) {
          try {
            const sj = (await snap.json()) as { image?: { id?: number } };
            if (sj.image?.id) snapshotId = String(sj.image.id);
          } catch { /* id is best-effort metadata */ }
        } else if (mustSnapshot) {
          await ctx.runMutation(internal.cloudMachines.setStatus, {
            machineId,
            status: "error",
            errorMessage: `Hosted box NOT deleted: data snapshot failed (HTTP ${snap.status}). Your app + database are still on the box. Retry decommission, or contact support — we will not delete unrecoverable data.`,
          });
          return;
        } else {
          warn = `grace snapshot returned HTTP ${snap.status} (continued with delete); `;
        }
      } catch (e) {
        if (mustSnapshot) {
          await ctx.runMutation(internal.cloudMachines.setStatus, {
            machineId,
            status: "error",
            errorMessage: `Hosted box NOT deleted: data snapshot failed (${e instanceof Error ? e.message : String(e)}). Your app + database are still on the box. Retry decommission.`,
          });
          return;
        }
        warn = `grace snapshot failed (${e instanceof Error ? e.message : String(e)}); continued with delete; `;
      }
      if (snapshotId) {
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId,
          status: machine.status ?? "stopping",
          lastSnapshotId: snapshotId,
        });
      }
      // Delete. Surface a real failure as status:error — do NOT fall
      // through to "stopped" while the box is still alive.
      try {
        const del = await fetch(`https://api.hetzner.cloud/v1/servers/${machine.hetznerServerId}`, {
          method: "DELETE",
          headers: { Authorization: `Bearer ${HCLOUD_TOKEN}` },
        });
        if (!del.ok && del.status !== 404) {
          await ctx.runMutation(internal.cloudMachines.setStatus, {
            machineId,
            status: "error",
            errorMessage: `${warn}Hetzner delete returned HTTP ${del.status} — box may still be running. Check the Hetzner console / token project scope, then retry.`,
          });
          return;
        }
      } catch (e) {
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId,
          status: "error",
          errorMessage: `${warn}Hetzner delete failed: ${e instanceof Error ? e.message : String(e)} — box may still be running.`,
        });
        return;
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
