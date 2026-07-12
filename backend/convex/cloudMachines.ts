import { mutation, query, internalMutation, internalAction, internalQuery } from "./_generated/server";
import { v } from "convex/values";
import { api, internal } from "./_generated/api";
import { listGrantedMachineIdsForGrant, listVisibleInfraGrantsForGuest } from "./access";
import { isOwnerUserId } from "./ownerAllowlist";
import { randomHex, sha256Hex } from "./auth";

// Machine specs by type. The Hetzner server_type strings are what you pass
// to POST https://api.hetzner.cloud/v1/servers.
const MACHINE_SPECS = {
  cpu: {
    // History: cx42 DEPRECATED (422 "server type 106 is deprecated");
    // cpx41 non-deprecated but US-only stock; ccx33 (dedicated) works
    // EU+US but costs €73.99/mo. cpx42 (8 vCPU/16 GB) was the prior
    // default — fine for a single RN/Hermes app, but TIGHT for a real
    // monorepo (Talos-class: workspace install + monorepo-wide tsc +
    // a Metro instance can collectively exceed 16 GB and OOM-kill mid
    // build, which surfaces to the user as "the agent crashed"). So
    // the default is now the 32 GB tier: ONE box that comfortably
    // satisfies a monorepo, not just a toy app. cpx51 (shared AMD,
    // 16 vCPU/32 GB/360 GB, x86=amd64) is the target SKU.
    //
    // ⚠️ VERIFY before HCLOUD_TOKEN goes live (prod is fail-closed
    // dry-run until then): "priced" != "orderable". Re-check the exact
    // type string + per-region stock with
    //   GET /v1/datacenters .server_types.available
    // and the price with GET /v1/server_types. The hetznerType is also
    // env-overridable at runtime via YAVER_CLOUD_CPU_TYPE (see
    // cloudLifecycle.hetznerServerType) so a region/stock swap needs no
    // redeploy. us-region still falls back to ash in the region→location
    // map — confirm the chosen type is orderable there too.
    hetznerType: "cpx51",    // 16 vCPU, 32 GB RAM, 360 GB, amd64 (shared)
    vcpu: 16,
    ramGb: 32,
    diskGb: 360,
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
  bootstrapDeviceCode: string;
  bootstrapExpiresAt: number;
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
  // Operator debug SSH public key (Convex env MANAGED_CLOUD_SSH_PUBKEY).
  // NOT a user key and never in git — it lets the operator read
  // `docker logs yaver` on a stuck box. Absent ⇒ no ssh_authorized_keys
  // emitted (cloud-init byte-identical to before). Public-key material
  // only; never a private key.
  sshAuthorizedKey?: string;
  // Platform relay password (Convex env MANAGED_CLOUD_RELAY_PASSWORD).
  // The agent already auto-discovers relay SERVERS from /config; the
  // only missing piece for a managed box to be web-dashboard-reachable
  // (browser path is relay-only) is this password. Baked into
  // config.json `relay_password`. Absent ⇒ omitted (no regression).
  relayPassword?: string;
  // Per-box self-relay password (Phase 2A). When set, cloud-init runs
  // a `yaver-relay` sidecar Docker container on the box itself —
  // ghcr.io/kivanccakmak/yaver-relay:latest on QUIC 4433/UDP +
  // HTTP 8443/TCP — and ufw opens those ports. The user's OTHER
  // self-hosted devices then prefer this user-owned relay (set in
  // userSettings.relayUrl/relayPassword) over the shared free
  // platform relay. Absent ⇒ no sidecar (byte-identical no-relay).
  boxRelayPassword?: string;
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

const OPENCODE_CODING_PLAN_PROVIDER = "zai-coding-plan";
const OPENCODE_CODING_PLAN_MODEL = "zai-coding-plan/glm-4.7";

function managedOpenCodeConfigBody(yaverBin: string): string {
  return JSON.stringify(
    {
      $schema: "https://opencode.ai/config.json",
      model: OPENCODE_CODING_PLAN_MODEL,
      enabled_providers: [OPENCODE_CODING_PLAN_PROVIDER],
      default_agent: "build",
      agent: {
        build: {
          steps: 50,
          temperature: 0.2,
        },
      },
      mcp: {
        yaver: {
          command: [yaverBin, "mcp"],
          enabled: true,
          type: "local",
        },
      },
    },
    null,
    2,
  );
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
  const optionalRepoClone = spec.repoUrl
    ? `      clone_one ${shellSingleQuote(spec.repoUrl)} starter\n`
    : "";
  const workspaceBootstrap = `  # ── persistent source workspace (visible inside the agent container) ─
  - mkdir -p /srv/yaver/state/Workspace
  - |
    cat > /usr/local/bin/yaver-bootstrap-workspace <<'SCRIPT'
    #!/usr/bin/env bash
    set -u
    root=/srv/yaver/state/Workspace
    mkdir -p "$root"
    clone_one() {
      repo="$1"; name="$2"; dest="$root/$name"
      [ -d "$dest/.git" ] && return 0
      git clone "$repo" "$dest" || echo "[workspace] clone skipped: $repo"
    }
    clone_one https://github.com/kivanccakmak/yaver.io.git yaver.io
    clone_one https://github.com/kivanccakmak/talos.git talos
${optionalRepoClone}    SCRIPT
  - chmod +x /usr/local/bin/yaver-bootstrap-workspace
  - /usr/local/bin/yaver-bootstrap-workspace || true
`;

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
  // authed). MUST be cloud-init YAML **list form** ([sh, -c, "..."]):
  // the earlier `- 'curl ... -d '{json}' ...'` had nested single
  // quotes → invalid YAML → cloud-init dropped the ENTIRE runcmd →
  // boxes stuck forever (no docker, no agent). List form has zero
  // quoting fragility. machineId/phase go as URL query params (simple
  // hex/kebab — no escaping); token in a header; `|| true` keeps it
  // non-fatal. machineToken/machineId/convexSite are build-time-known
  // simple strings (no quotes), safe in the double-quoted scalar.
  const phasePost = (phase: string) =>
    `  - [ sh, -c, "curl -fsS -m 8 -X POST -H 'X-Machine-Token: ${spec.machineToken}' '${spec.convexSite}/machine/phase?machineId=${spec.machineId}&phase=${phase}' >/dev/null 2>&1 || true" ]\n`;

  // Operator debug key — top-level cloud-config `ssh_authorized_keys`
  // (applies to the image default user, root on Hetzner Ubuntu). Only
  // emitted when the env-sourced key is present, so byok stays
  // byte-identical when unset. JSON.stringify ⇒ a YAML-safe flow scalar
  // (public keys contain spaces; never special YAML chars).
  const sshBlock = spec.sshAuthorizedKey
    ? `ssh_authorized_keys:\n  - ${JSON.stringify(spec.sshAuthorizedKey)}\n`
    : "";

  // config.json fields built as a list so an optional relay_password
  // can be appended without trailing-comma breakage. relay_password is
  // the ONLY missing piece for a managed box to be web-reachable: the
  // agent auto-discovers relay servers from /config, but the browser
  // dashboard path is relay-only and the relay is password-gated.
  const configFields = [
    `      "convex_site_url": ${jsonString(spec.convexSite)}`,
    `      "device_id": ${jsonString(spec.deviceId)}`,
    // Advertise the box's HTTPS endpoint so the browser dashboard
    // (which can't call LAN due to CORS) can reach it directly via the
    // Let's Encrypt-certed auto subdomain — no shared relay needed for
    // a managed box's OWN traffic. The on-box yaver-tls-reconciler
    // (below) issues the cert for ${spec.hostname}; the agent registers
    // this list in PublicEndpoints (auth.go RegisterDeviceRequest), so
    // every other surface (web, mobile, ops_connect) picks it up.
    `      "public_endpoints": [${jsonString("https://" + spec.hostname)}]`,
  ];
  if (spec.relayPassword) {
    // Defensive fallback: with auto-cert (preferred path) the box is
    // directly https-reachable and never needs to use the shared free
    // relay; relay_password stays as a last resort if cert issuance
    // races (Let's Encrypt rate-limit, DNS lag, etc.). Absent ⇒ no
    // baked password ⇒ agent uses userSettings/platform default only.
    configFields.push(`      "relay_password": ${jsonString(spec.relayPassword)}`);
  }
  const configBody = `    {\n${configFields.join(",\n")}\n    }`;
  const pendingAuthBody = `    {
      "deviceCode": ${jsonString(spec.bootstrapDeviceCode)},
      "userCode": "BROKERED",
      "url": "",
      "convexUrl": ${jsonString(spec.convexSite)},
      "expiresAt": ${spec.bootstrapExpiresAt},
      "createdAt": ${Date.now()}
    }`;

  // Phase 2 — yaver-relay BUNDLED into the same yaver-cloud container
  // (not a sidecar). The image's entrypoint wrapper backgrounds
  // `yaver-relay serve --quic-port=4433 --http-port=8443` when
  // RELAY_PASSWORD is in the container env; the agent stays PID 1.
  // Cloud-init's role here is just to (a) publish the relay ports and
  // (b) pass the password — both conditional on boxRelayPassword being
  // set (i.e. the user has a managed-cloud subscription). Absent ⇒
  // ports closed + env unset ⇒ the wrapper skips the relay entirely
  // (byte-identical no-relay behaviour).
  const relayUfwRules = spec.boxRelayPassword
    ? `  - ufw allow 4433/udp\n  - ufw allow 8443/tcp\n`
    : "";
  // docker run -p/-e flags are added conditionally so the byok / no-
  // subscription path stays byte-identical to pre-Phase-2.
  const yaverDockerRunLines = [
    `    docker rm -f yaver 2>/dev/null || true`,
    `    docker run -d --name yaver --restart always \\`,
    `      -p 18080:18080 \\`,
  ];
  if (spec.boxRelayPassword) {
    yaverDockerRunLines.push(`      -p 4433:4433/udp -p 8443:8443/tcp \\`);
  }
  yaverDockerRunLines.push(
    `      -e YAVER_HOSTNAME=${shellSingleQuote(spec.hostname)} \\`,
  );
  if (spec.boxRelayPassword) {
    yaverDockerRunLines.push(
      `      -e RELAY_PASSWORD=${shellSingleQuote(spec.boxRelayPassword)} \\`,
    );
    // CONVEX_URL lets the bundled yaver-relay per-user-validate
    // inbound tunnel registrations against managedRelays.password
    // (relay/main.go --convex-url). Without it, the relay falls back
    // to password-only mode — fine for a single-owner box but weaker
    // for future multi-device cross-user scenarios. Same Convex
    // deployment the agent uses for its session validation.
    yaverDockerRunLines.push(
      `      -e CONVEX_URL=${shellSingleQuote(spec.convexSite)} \\`,
    );
  }
  yaverDockerRunLines.push(`      ${selfhostedEnv} \\`);
  yaverDockerRunLines.push(`      -v /srv/yaver/state:/root \\`);
  yaverDockerRunLines.push(`      -v /srv/yaver/state/Workspace:/srv/yaver/workspace \\`);
  yaverDockerRunLines.push(`      ${shellSingleQuote(image)}`);
  const yaverDockerRun = yaverDockerRunLines.join("\n");

  // End-of-cloud-init observability beacon. Polls the in-container
  // agent's /health for up to 5 min, then POSTs phase=registering on
  // success or phase=error with a SHORT curated label on failure (no
  // logs/paths/secrets — the SSH key is how real logs are read). A
  // `- |` literal block, so the single-quote nesting that once broke
  // phasePost cannot recur here. machineToken/convexSite/machineId are
  // build-time-known simple strings.
  const healthBeacon =
    `  - |
    ok=0
    for i in $(seq 1 60); do
      if curl -fsS -m 4 http://127.0.0.1:18080/health >/dev/null 2>&1; then ok=1; break; fi
      sleep 5
    done
    if [ "$ok" = 1 ]; then
      curl -fsS -m 8 -X POST -H "X-Machine-Token: ${spec.machineToken}" "${spec.convexSite}/machine/phase?machineId=${spec.machineId}&phase=registering" >/dev/null 2>&1 || true
    else
      curl -fsS -m 8 -X POST -H "X-Machine-Token: ${spec.machineToken}" "${spec.convexSite}/machine/phase?machineId=${spec.machineId}&phase=error&error=agent-health-unreachable-300s" >/dev/null 2>&1 || true
    fi
`;

  return `#cloud-config
${sshBlock}package_update: true
packages:
  - ca-certificates
  - curl
  - git
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
${relayUfwRules}  - ufw --force enable || true
  # ── per-user config into the container's /root volume ──────────────
  - mkdir -p /srv/yaver/state/.yaver /etc/yaver
  - |
    cat > /srv/yaver/state/.yaver/config.json <<'EOF'
${configBody}
    EOF
  - chmod 0600 /srv/yaver/state/.yaver/config.json
  - |
    cat > /srv/yaver/state/.yaver/pending-auth.json <<'EOF'
${pendingAuthBody}
    EOF
  - chmod 0600 /srv/yaver/state/.yaver/pending-auth.json
  - mkdir -p /srv/yaver/state/.config/opencode
  - |
    cat > /srv/yaver/state/.config/opencode/opencode.json <<'EOF'
${managedOpenCodeConfigBody("/usr/local/bin/yaver")
  .split("\n")
  .map((line) => `    ${line}`)
  .join("\n")}
    EOF
  - chmod 0600 /srv/yaver/state/.config/opencode/opencode.json
${workspaceBootstrap}  # ── run the yaver image (the box IS this container) ────────────────
${phasePost("pulling-image")}  - docker pull ${shellSingleQuote(image)}
  - docker run --rm -v /srv/yaver/state:/root ${shellSingleQuote(image)} auth --headless --background-wait --convex-url ${shellSingleQuote(spec.convexSite)} || echo "[cloud-init] brokered yaver auth skipped"
${phasePost("starting-agent")}  - |
${yaverDockerRun}
  - mkdir -p /etc/nginx/snippets
${hostedConvex}  # ── host TLS reconciler (same contract as the VM path) ─────────────
  - |
    cat > /etc/yaver/machine.json <<'EOF'
    {"machineId":${jsonString(spec.machineId)},"machineToken":${jsonString(spec.machineToken)},"convexSite":${jsonString(spec.convexSite)},"hostname":${jsonString(spec.hostname)}}
    EOF
  - chmod 0600 /etc/yaver/machine.json
  - |
    cat > /usr/local/bin/yaver-tls-reconciler <<'SCRIPT'
    #!/usr/bin/env bash
    set -euo pipefail
    conf=/etc/yaver/machine.json
    MID=$(jq -r .machineId "$conf"); MT=$(jq -r .machineToken "$conf"); CV=$(jq -r .convexSite "$conf")
    HOST=$(jq -r .hostname "$conf" 2>/dev/null || echo "")
    # Auto-cert the box's own subdomain (<id>.cloud.yaver.io). Browser
    # dashboard hits https://<host> directly → managed traffic stays off
    # the shared free relay. Idempotent: nginx server block created
    # once, certbot refuses to re-issue if not due, || true keeps it
    # non-fatal during early-boot DNS lag. NO /machine/tls-issued POST
    # (that endpoint is for userDomains rows; the auto domain is not one).
    if [ -n "$HOST" ] && [ "$HOST" != "null" ]; then
      cf="/etc/nginx/sites-available/$HOST"
      if [ ! -f "$cf" ]; then
        cat > "$cf" <<NGINX
    server { listen 80; server_name $HOST;
      include /etc/nginx/snippets/yaver-convex.conf;
      location / { proxy_pass http://127.0.0.1:18080; proxy_set_header Host \\$host;
        proxy_set_header X-Real-IP \\$remote_addr; proxy_set_header X-Forwarded-For \\$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \\$scheme; proxy_read_timeout 300s; proxy_buffering off; } }
    NGINX
        ln -sf "$cf" "/etc/nginx/sites-enabled/$HOST"; nginx -t && systemctl reload nginx
      fi
      certbot --nginx -d "$HOST" --non-interactive --agree-tos -m admin@yaver.io --redirect --no-eff-email || true
    fi
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
${healthBeacon}`;
}

export function buildManagedCloudInit(spec: ManagedCloudBootstrapSpec): string {
  const optionalRepoCloneSnippet = spec.repoUrl
    ? `      clone_one ${shellSingleQuote(spec.repoUrl)} starter\n`
    : "";
  const repoCloneSnippet = `  # Managed source workspace — the mobile app's project scanner sees
  # these as normal sibling repos under ~/Workspace. Talos may be private;
  # the bootstrap is intentionally repeatable so it can succeed later after
  # GitHub credentials are configured from mobile.
  - |
    cat > /usr/local/bin/yaver-bootstrap-workspace <<'SCRIPT'
    #!/usr/bin/env bash
    set -u
    root=/home/yaver/Workspace
    mkdir -p "$root"
    chown yaver:yaver "$root"
    clone_one() {
      repo="$1"; name="$2"; dest="$root/$name"
      [ -d "$dest/.git" ] && return 0
      sudo -u yaver git clone "$repo" "$dest" || echo "[workspace] clone skipped: $repo"
    }
    clone_one https://github.com/kivanccakmak/yaver.io.git yaver.io
    clone_one https://github.com/kivanccakmak/talos.git talos
${optionalRepoCloneSnippet}    chown -R yaver:yaver "$root" || true
    SCRIPT
  - chmod +x /usr/local/bin/yaver-bootstrap-workspace
  - /usr/local/bin/yaver-bootstrap-workspace || true
`;

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

  # ── Managed Yaver agent bootstrap (operator tenant runtime) ────────
  # The agent runs as a dedicated unprivileged 'yaver' user. Claude/Codex
  # beta guests are launched as separate yv-* Unix users with isolated
  # HOME/CLAUDE_CONFIG_DIR/CODEX_HOME under /srv/yaver/tenants/<id>.
  # The TLS reconciler below stays a separate root unit (nginx/certbot
  # need root); it only proxies to the agent on loopback:18080.
  - id yaver >/dev/null 2>&1 || useradd --system --create-home --home-dir /home/yaver --shell /bin/bash yaver
  - usermod -aG docker yaver || true
  - install -d -o root -g root -m 0755 /srv /srv/yaver
  - install -d -o root -g root -m 0711 /srv/yaver/tenants
  - install -d -o yaver -g yaver -m 0700 /home/yaver/.yaver
  - install -d -o yaver -g yaver -m 0700 /home/yaver/.config/opencode
  - install -d -o yaver -g yaver -m 0755 /home/yaver/Workspace
  - mkdir -p /etc/yaver
  - |
    cat > /home/yaver/.yaver/config.json <<'EOF'
    {
      "convex_site_url": ${jsonString(spec.convexSite)},
      "device_id": ${jsonString(spec.deviceId)},
      "public_endpoints": [${jsonString("https://" + spec.hostname)}]
    }
    EOF
  - chown yaver:yaver /home/yaver/.yaver/config.json && chmod 0600 /home/yaver/.yaver/config.json
  - |
    cat > /home/yaver/.yaver/pending-auth.json <<'EOF'
    {
      "deviceCode": ${jsonString(spec.bootstrapDeviceCode)},
      "userCode": "BROKERED",
      "url": "",
      "convexUrl": ${jsonString(spec.convexSite)},
      "expiresAt": ${spec.bootstrapExpiresAt},
      "createdAt": ${Date.now()}
    }
    EOF
  - chown yaver:yaver /home/yaver/.yaver/pending-auth.json && chmod 0600 /home/yaver/.yaver/pending-auth.json
  - sudo -u yaver -H /usr/local/bin/yaver auth --headless --background-wait --convex-url ${shellSingleQuote(spec.convexSite)} || echo "[cloud-init] brokered yaver auth skipped"
  - |
    cat > /home/yaver/.config/opencode/opencode.json <<'EOF'
${managedOpenCodeConfigBody("/usr/local/bin/yaver")
  .split("\n")
  .map((line) => `    ${line}`)
  .join("\n")}
    EOF
  - chown yaver:yaver /home/yaver/.config/opencode/opencode.json && chmod 0600 /home/yaver/.config/opencode/opencode.json
  - /usr/local/bin/yaver serve --install-systemd-system --operator || echo "[cloud-init] yaver operator service install skipped"
${repoCloneSnippet}  - systemctl daemon-reload
  - systemctl enable --now yaver-helper yaver || true

  # ── TLS reconciler ─────────────────────────────────────────────
  - |
    cat > /etc/yaver/machine.json <<'EOF'
    {"machineId":${jsonString(spec.machineId)},"machineToken":${jsonString(spec.machineToken)},"convexSite":${jsonString(spec.convexSite)},"hostname":${jsonString(spec.hostname)}}
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
    After=network-online.target nginx.service yaver.service
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
    // Machine LIST (UI) drops hidden beta grants — beta users never see the
    // owner's box here. Routing/access paths keep the active variant.
    const grants = await listVisibleInfraGrantsForGuest(ctx, userId);
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

/** hostingForDevice — the three-tier provenance of ONE of the user's devices,
 *  for the agent's own auto-lifecycle gate (desktop/agent/hosting_tier.go).
 *  managed = a Yaver-side cloudMachines row (origin !== "self-hosted"); byo = a
 *  non-deleted byoMachines row linked by deviceId; else self-hosted. Mirrors
 *  the three-way logic in devices.listMyDevices so web/mobile/agent agree.
 *  Internal — the /machine/hosting HTTP route scopes userId from the session. */
export const hostingForDevice = internalQuery({
  args: { userId: v.id("users"), deviceId: v.string() },
  handler: async (ctx, { userId, deviceId }) => {
    const did = deviceId.trim();
    if (did === "") return { tier: "self-hosted" as const, managed: false, byo: false };

    const cloud = await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    if (cloud.some((m) => m.deviceId === did && m.origin !== "self-hosted")) {
      return { tier: "managed" as const, managed: true, byo: false };
    }

    const byo = await ctx.db
      .query("byoMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    if (byo.some((b) => b.deviceId === did && b.state !== "deleted")) {
      return { tier: "byo" as const, managed: false, byo: true };
    }

    return { tier: "self-hosted" as const, managed: false, byo: false };
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
// seedMachineInfo — write the box's REAL properties (server type, specs,
// OS/distro, available+authed runners) into Convex. Called by the agent once
// it's up (and usable to correct a row whose provisioning-time specs were a
// guess). Hardware/OS/runner capability is NOT P2P-sensitive, so it's allowed
// in Convex; it powers capacity planning, the resume server-type choice, and
// machine policies. specs are MERGED so a partial report never nulls a field.
export const seedMachineInfo = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    serverType: v.optional(v.string()),
    specs: v.optional(v.object({
      vcpu: v.optional(v.number()),
      ramGb: v.optional(v.number()),
      diskGb: v.optional(v.number()),
      arch: v.optional(v.string()),
      gpu: v.optional(v.string()),
      vram: v.optional(v.number()),
      os: v.optional(v.string()),
      distro: v.optional(v.string()),
      kernel: v.optional(v.string()),
    })),
    runnersAvailable: v.optional(v.array(v.object({
      id: v.string(),
      name: v.optional(v.string()),
      installed: v.optional(v.boolean()),
      authed: v.optional(v.boolean()),
      authSource: v.optional(v.string()),
    }))),
  },
  handler: async (ctx, args) => {
    const row = await ctx.db.get(args.machineId);
    if (!row) return;
    const patch: Record<string, unknown> = { updatedAt: Date.now() };
    if (args.serverType) patch.serverType = args.serverType;
    if (args.runnersAvailable) patch.runnersAvailable = args.runnersAvailable;
    if (args.specs) {
      const prev = (row as any).specs ?? {};
      const merged: Record<string, unknown> = { ...prev };
      for (const [k, v2] of Object.entries(args.specs)) {
        if (v2 !== undefined) merged[k] = v2;
      }
      // arch is required by the validator — keep a sane default if neither
      // the report nor the prior row carried one.
      if (merged.arch === undefined) merged.arch = "amd64";
      patch.specs = merged;
    }
    await ctx.db.patch(args.machineId, patch);
  },
});

export const setProvisioned = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    hetznerServerId: v.string(),
    serverIp: v.string(),
    hostname: v.string(),
    machineTokenHash: v.optional(v.string()),
    deviceId: v.optional(v.string()),
    bootImageSource: v.optional(v.string()),
    serverType: v.optional(v.string()),
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
    if (args.bootImageSource) patch.bootImageSource = args.bootImageSource;
    if (args.serverType) patch.serverType = args.serverType;
    await ctx.db.patch(args.machineId, patch);
  },
});

// ─── BYO phone-direct provisioning (no Yaver HCLOUD_TOKEN) ─────────
// The privacy-preserving counterpart to the managed provision action:
// Convex mints the box's device credential + self-bootstrapping
// cloud-init (reusing buildManagedCloudInit), but DOES NOT call Hetzner.
// The phone creates the server itself with the user's OWN Hetzner token
// (which never touches Convex). The box self-installs yaver, auths as
// the user via the brokered one-time pending-auth handle, registers as a device, and
// mirrors the runner — same vibe-ready bootstrap as a managed box, on
// the user's own account.

/** Insert a BYO ("self-hosted" origin) row WITHOUT scheduling the managed
 *  provision action (there's no Yaver token; the phone provisions). */
export const createByoRow = internalMutation({
  args: {
    userId: v.id("users"),
    machineType: v.string(),
    region: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const specDef = MACHINE_SPECS[args.machineType as keyof typeof MACHINE_SPECS];
    if (!specDef) throw new Error("Invalid machine type: " + args.machineType);
    const now = Date.now();
    const specs: { vcpu: number; ramGb: number; diskGb: number; arch: string; gpu?: string; vram?: number } = {
      vcpu: specDef.vcpu,
      ramGb: specDef.ramGb,
      diskGb: specDef.diskGb,
      arch: specDef.arch,
    };
    if ("gpu" in specDef) {
      specs.gpu = (specDef as any).gpu;
      specs.vram = (specDef as any).vram;
    }
    return await ctx.db.insert("cloudMachines", {
      userId: args.userId,
      machineType: args.machineType,
      origin: "self-hosted", // user's own Hetzner account
      provider: "hetzner",
      tier: "byok",
      status: "provisioning",
      provisionPhase: "creating",
      provisionProgress: 5,
      provisionPhaseAt: now,
      runnersAuthorized: false,
      multiUser: false,
      region: args.region ?? "eu",
      tools: [],
      specs,
      createdAt: now,
      updatedAt: now,
    });
  },
});

/** Store the bootstrap identity (token hash + deviceId) on a BYO row,
 *  before the phone creates the server. Separate from setProvisioned,
 *  which records the Hetzner id/ip the phone reports back afterwards. */
export const setByoBootstrap = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    machineTokenHash: v.string(),
    deviceId: v.string(),
  },
  handler: async (ctx, args) => {
    await ctx.db.patch(args.machineId, {
      machineTokenHash: args.machineTokenHash,
      deviceId: args.deviceId,
      bootImageSource: "vanilla", // BYO boots ubuntu + first-boot build
      updatedAt: Date.now(),
    });
  },
});

/** Mint a BYO box bootstrap: create the row, mint creds, create the user
 *  session the box will use, and build the cloud-init the phone bakes
 *  into the Hetzner server. Returns everything the phone needs to create
 *  the server itself — NO Hetzner token involved. */
export const mintByoBootstrap = internalAction({
  args: {
    userId: v.id("users"),
    machineType: v.string(),
    region: v.optional(v.string()),
  },
  handler: async (ctx, args): Promise<{
    machineId: string;
    deviceId: string;
    serverName: string;
    userData: string;
  }> => {
    const specDef = MACHINE_SPECS[args.machineType as keyof typeof MACHINE_SPECS];
    if (!specDef) throw new Error("Invalid machine type: " + args.machineType);

    const machineId: any = await ctx.runMutation(internal.cloudMachines.createByoRow, {
      userId: args.userId,
      machineType: args.machineType,
      region: args.region,
    });

    const shortId = machineId.toString().substring(0, 8);
    const serverName = `yaver-${args.machineType}-${shortId}`;
    const deviceId = `byo-${shortId}`;
    const isGpu = args.machineType === "gpu";
    const yaverArch = specDef.arch === "amd64" ? "amd64" : "arm64";
    const yaverReleaseUrl = `https://github.com/kivanccakmak/yaver.io/releases/latest/download/yaver-linux-${yaverArch}.tar.gz`;

    const machineToken = randomHex(24);
    const machineTokenHash = await sha256Hex(machineToken);
    const convexSite = process.env.CONVEX_SITE_URL || "https://perceptive-minnow-557.eu-west-1.convex.site";
    const brokeredAuth = await ctx.runMutation(internal.deviceCode.createAuthorizedDeviceCodeForUserInternal, {
      userId: args.userId,
      machineName: serverName,
      platform: "linux",
      arch: specDef.arch,
      deviceId,
    });

    await ctx.runMutation(internal.cloudMachines.setByoBootstrap, {
      machineId,
      machineTokenHash,
      deviceId,
    });

    // BYO has no Yaver-managed DNS; the box is reached by IP + registers
    // as a device. hostname is cosmetic here (no auto subdomain / TLS).
    const userData = buildManagedCloudInit({
      convexSite,
      machineId: machineId.toString(),
      machineToken,
      bootstrapDeviceCode: brokeredAuth.deviceCode,
      bootstrapExpiresAt: brokeredAuth.expiresAt,
      deviceId,
      hostname: deviceId,
      yaverArch,
      yaverReleaseUrl,
      gpu: isGpu,
      tier: "byok",
    });

    return { machineId: machineId.toString(), deviceId, serverName, userData };
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

// Bump last-meaningful-activity (idle auto-shutdown signal). Called by
// the box agent via the machine-token /machine/activity route when it
// runs a task / has an interactive session, and by the gateway on
// inference. THROTTLED: only writes when the prior stamp is older than
// ~60s, so a busy box doesn't generate a write per request. Idle sweep
// reads this; lastHealthCheck (liveness) is deliberately NOT the signal —
// an unused box still heartbeats and would never auto-pause.
export const touchActivity = internalMutation({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    const m = await ctx.db.get(machineId);
    if (!m) return { ok: false };
    const now = Date.now();
    const prev = (m as any).lastActivityAt ?? 0;
    if (now - prev < 60_000) return { ok: true, throttled: true };
    await ctx.db.patch(machineId, { lastActivityAt: now });
    return { ok: true };
  },
});

// Bump activity for ALL of a user's active managed boxes — the gateway
// path has a userId (inference request) but not a machineId. Keeps a
// hosted user's box warm while they use managed AI even before the agent
// reports task activity. Throttled per-row by touchActivity's guard via
// an inline check (cheap: a user has ~1 box).
export const touchActivityForUser = internalMutation({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const now = Date.now();
    const rows = await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    let bumped = 0;
    for (const m of rows) {
      if (m.status !== "active" || ((m as any).origin ?? "managed") !== "managed") continue;
      if (now - ((m as any).lastActivityAt ?? 0) < 60_000) continue;
      await ctx.db.patch(m._id, { lastActivityAt: now });
      bumped++;
    }
    return { ok: true, bumped };
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
    // Short curated failure label from the box's own beacon. Only
    // honoured when phase === "error"; any healthy phase clears it.
    error: v.optional(v.string()),
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
    if (args.phase === "error") {
      // Cap the label so a misbehaving box can't write an essay; it is
      // a status string, never logs/paths/secrets (privacy contract).
      patch.provisionError = (args.error ?? "provisioning failed").slice(0, 200);
    } else {
      // Any forward progress clears a stale error so the UI recovers.
      patch.provisionError = undefined;
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
// ─── Machine quota (control-plane authoritative) ─────────────────
// Cap how many managed machines a user may hold at once (any state except
// removed/deleted). Enforced in provision() BEFORE any Hetzner spend — never
// trusted from an MCP verb. Owner (env allowlist) is exempt so the repo owner
// can develop multi-box flows. Plan-tiered with an env-tunable fallback.
const MANAGED_MACHINE_QUOTA: Record<string, number> = {
  "cloud-agent": 1,
  "cloud-workspace": 2,
};
function managedMachineLimit(plan?: string | null): number {
  if (plan && MANAGED_MACHINE_QUOTA[plan] != null) return MANAGED_MACHINE_QUOTA[plan];
  const envN = parseInt(process.env.YAVER_MANAGED_MACHINE_LIMIT ?? "", 10);
  return Number.isFinite(envN) && envN > 0 ? envN : 1;
}

/** Count a user's managed machines that occupy a quota slot (not removed/deleted). */
export const activeCountForUser = internalQuery({
  args: { userId: v.id("users"), excludeMachineId: v.optional(v.id("cloudMachines")) },
  handler: async (ctx, { userId, excludeMachineId }) => {
    const rows = await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    return rows.filter(
      (m) => m._id !== excludeMachineId && m.status !== "removed" && m.status !== "deleted",
    ).length;
  },
});

/**
 * MCP/UI-facing quota status: how many managed machines the user holds vs the
 * cap. Lets the AI say "you're at 1 of 1 — upgrade for more" BEFORE the wall,
 * instead of a silent create failure. Enforcement still lives in provision().
 */
export const quota = query({
  args: { userId: v.id("users") },
  handler: async (ctx, { userId }) => {
    const rows = await ctx.db
      .query("cloudMachines")
      .withIndex("by_user", (q) => q.eq("userId", userId))
      .collect();
    const used = rows.filter((m) => m.status !== "removed" && m.status !== "deleted").length;
    const owner = isOwnerUserId(userId);
    const limit = owner ? Number.MAX_SAFE_INTEGER : managedMachineLimit();
    return { used, limit, remaining: Math.max(0, limit - used), owner };
  },
});

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
      let entitled = await ctx.runQuery(internal.subscriptions.canProvisionManaged, {
        subscriptionId: machine.subscriptionId ?? undefined,
        userId: machine.userId,
      });
      // Prepaid path: a funded wallet entitles provisioning even with no
      // subscription (OpenAI-style credits — the box is paid by burning
      // balance, not a recurring plan). Still fail-closed: an empty
      // wallet + no subscription + non-owner = denied. canStart checks
      // the balance covers this SKU's safe reserve floor.
      if (!entitled) {
        const gate = await ctx.runQuery(internal.cloudLifecycle.canStart, {
          userId: machine.userId,
          machineType: machine.machineType ?? "cpu",
        });
        entitled = gate.ok;
      }
      if (!entitled) {
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId: args.machineId,
          status: "error",
          errorMessage:
            "Not entitled — managed provisioning denied (active subscription, prepaid balance, or owner allowlist required)",
        });
        return;
      }
    }

    // Machine quota — control-plane cap so a user can't spin up many managed
    // boxes on our Hetzner account. Owner (env allowlist) exempt. Checked AFTER
    // entitlement and BEFORE any Hetzner spend; the row being provisioned is
    // excluded from the count.
    if (!isOwnerUserId(machine.userId)) {
      const limit = managedMachineLimit();
      const used = await ctx.runQuery(internal.cloudMachines.activeCountForUser, {
        userId: machine.userId,
        excludeMachineId: machine._id,
      });
      if (used >= limit) {
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId: args.machineId,
          status: "error",
          errorMessage: `Machine quota reached — you already have ${used} managed machine(s) (limit ${limit}). Remove one with 'machine rm' or upgrade your plan for more.`,
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
    const deviceId = `cloud-${shortId}`;
    const brokeredAuth = await ctx.runMutation(internal.deviceCode.createAuthorizedDeviceCodeForUserInternal, {
      userId: machine.userId,
      machineName: serverName,
      platform: "linux",
      arch: specDef.arch,
      deviceId,
    });

    // Operator-only injection points (same class as HCLOUD_TOKEN —
    // Convex env, never git). Both optional: unset ⇒ cloud-init is
    // byte-identical to before (no ssh key, no relay password). Set via
    // `npx convex env set --prod MANAGED_CLOUD_SSH_PUBKEY "ssh-ed25519 …"`
    // and `… MANAGED_CLOUD_RELAY_PASSWORD "<platform relay password>"`.
    // Tenant-aware SSH: our OPERATOR key goes ONLY onto our own (owner) boxes.
    // A box we SELL to a customer must NEVER carry our operator root key — it
    // gets the customer's OWN key (machine.sshPublicKey) and is managed via the
    // control-plane. This bounds operator footprint for the resale product.
    const ownerBox = isOwnerUserId(machine.userId);
    const sshAuthorizedKey = ownerBox
      ? (process.env.MANAGED_CLOUD_SSH_PUBKEY || "").trim()
      : ((machine as { sshPublicKey?: string }).sshPublicKey || "").trim();
    const relayPassword = (process.env.MANAGED_CLOUD_RELAY_PASSWORD || "").trim();
    // Attaching an SSH key on create makes Hetzner set NO root password (no
    // "server created" email, no forced-expiry that blocks the agent boot).
    // Owner boxes attach the operator boot key (Convex env, never a source
    // literal); customer boxes never attach it here.
    const bootSshKeyNames = ownerBox && (process.env.YAVER_CLOUD_SSH_KEY_NAME || "").trim()
      ? [(process.env.YAVER_CLOUD_SSH_KEY_NAME || "").trim()]
      : [];
    // Phase 2A — per-box self-relay password. Generated unconditionally
    // (no env gate) for every managed box that has a subscription,
    // because the relay sidecar is the whole point of the architecture:
    // the box hosts a yaver-relay container so the user's OTHER
    // self-hosted devices can use it (managedRelays row + userSettings
    // pointers wired below, post-Hetzner). Dev-adopt boxes lacking a
    // subscriptionId skip the sidecar (managedRelays.create requires
    // subId), so we only thread the password when both sides will land.
    const boxRelayPassword = machine.subscriptionId ? randomHex(24) : "";

    const bootstrapSpec = {
      convexSite,
      machineId: machineIdStr,
      machineToken,
      bootstrapDeviceCode: brokeredAuth.deviceCode,
      bootstrapExpiresAt: brokeredAuth.expiresAt,
      deviceId,
      hostname: autoDomain,
      yaverArch,
      yaverReleaseUrl,
      repoUrl: machine.repoUrl,
      gpu: isGpu,
      sshAuthorizedKey: sshAuthorizedKey || undefined,
      relayPassword: relayPassword || undefined,
      boxRelayPassword: boxRelayPassword || undefined,
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

    // P3 fast boot: when a prebuilt Yaver golden snapshot id is
    // configured (our managed Hetzner account), boot from it instead of
    // ubuntu-24.04 so the box comes up with everything pre-installed —
    // seconds instead of a 3–5 min first-boot build. cloud-init still
    // runs for per-box token injection + device registration. Per-arch
    // (the snapshot must match the server arch); numeric Hetzner image
    // id. Unset ⇒ byte-identical legacy behaviour (ubuntu-24.04).
    const bootArch = (specDef.arch as string) === "arm64" ? "ARM64" : "AMD64";
    const goldenImageId = (
      process.env[`YAVER_CLOUD_IMAGE_ID_${bootArch}`] ||
      process.env.YAVER_CLOUD_IMAGE_ID ||
      ""
    ).trim();
    const bootImage: string | number = goldenImageId
      ? (/^\d+$/.test(goldenImageId) ? Number(goldenImageId) : goldenImageId)
      : "ubuntu-24.04";

    try {
      // ── 1. Hetzner server ───────────────────────────────────────
      // Test-only cost override: YAVER_CLOUD_CPU_TYPE swaps the cpu SKU's
      // Hetzner type (e.g. cpx22 €9.49 vs cpx42 €29.99) so headless/e2e
      // provisions are cheap throwaways. Unset ⇒ the real SKU type. Only
      // applies to machineType "cpu". Must be a non-deprecated type orderable
      // in the resolved location. Captured so we can RECORD it on the row and
      // recreate on the exact same type at resume (a snapshot won't restore
      // onto a smaller disk).
      const createdServerType =
        machine.machineType === "cpu" && process.env.YAVER_CLOUD_CPU_TYPE
          ? process.env.YAVER_CLOUD_CPU_TYPE
          : specDef.hetznerType;
      const hetznerResp = await fetch("https://api.hetzner.cloud/v1/servers", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${HCLOUD_TOKEN}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          name: serverName,
          server_type: createdServerType,
          image: bootImage,
          location,
          ...(bootSshKeyNames.length ? { ssh_keys: bootSshKeyNames } : {}),
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
        deviceId, // deterministic cloud-<shortId> the box registers as
        // Record which boot path ran so a slow vanilla-fallback (no
        // golden snapshot configured for this arch) is visible on the
        // card instead of looking like a hang.
        bootImageSource: goldenImageId ? "golden" : "vanilla",
        // Persist the concrete type so resume-from-snapshot recreates on it.
        serverType: createdServerType,
      });

      // ── 4b. Phase 2B+2C — managed box doubles as this user's relay ──
      // Sidecar (yaver-relay) is launched by cloud-init when
      // boxRelayPassword is set; here we mirror the same password into
      // (i) a managedRelays row (so it's discoverable + auditable, same
      // class as the historical separate-Hetzner-relay SKU but with the
      // managed box AS the relay — no extra €/mo) and (ii) the user's
      // userSettings.relayUrl/relayPassword, which every device fetches
      // on serve start (FetchUserSettings, main.go:2478). Gated on
      // machine.subscriptionId because managedRelays.create requires
      // one — dev-adopt boxes skip cleanly.
      // Phase 2D gap: the agent currently drops a userSettings.RelayUrl
      // that doesn't match a platformConfig entry (main.go:2492-2503);
      // until that synth-RelayServerInfo change ships in a cli/v*
      // release, OTHER devices won't actually USE this relay even
      // though everything is wired here. Box itself is reachable via
      // its own auto-cert (Step 2b), so this gap is OTHER-device-only.
      if (machine.subscriptionId && boxRelayPassword) {
        try {
          const relayId = await ctx.runMutation(internal.managedRelays.create, {
            userId: machine.userId,
            subscriptionId: machine.subscriptionId,
            region: machine.region ?? "eu",
            password: boxRelayPassword,
          });
          await ctx.runMutation(internal.managedRelays.updateProvisioned, {
            relayId,
            hetznerServerId,
            serverIp,
            domain: autoDomain,
          });
          await ctx.runMutation(internal.userSettings.setRelayForUser, {
            userId: machine.userId,
            relayUrl: `https://${autoDomain}`,
            relayPassword: boxRelayPassword,
          });
          console.log(
            `[cloudMachines.provision] managedRelay wired: ${autoDomain} (relayId=${relayId})`,
          );
        } catch (e: unknown) {
          // Non-fatal — the box itself is still reachable via auto-cert.
          // Phase 2 (OTHER devices using this box as relay) just doesn't
          // light up for this provision. Logged for post-mortem.
          console.error(
            "[cloudMachines.provision] managedRelay wiring failed:",
            e instanceof Error ? e.message : String(e),
          );
        }
      }

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

/** Wake-side health poll. A resumed box's server RECORD exists (status
 *  "active") long before its OS boots and its agent re-registers. This
 *  polls /health and only flips the phase to "ready" (or
 *  "authorizing-runners" when runner OAuth is still pending) once the box
 *  actually answers — so the wake progress never lies about reachability.
 *  Self-limiting: gives up after ~10 attempts, leaving the phase at
 *  "registering" (clients still derive readiness from device presence). */
export const resumeHealthCheck = internalAction({
  args: {
    machineId: v.id("cloudMachines"),
    attempt: v.number(),
  },
  handler: async (ctx, { machineId, attempt }) => {
    const machine = await ctx.runQuery(internal.cloudMachines.getInternal, { machineId });
    // Only meaningful while a resumed box is still finishing. If it was
    // re-paused, errored, or already reached "ready", stop.
    if (!machine || machine.status !== "active") return;
    if (machine.provisionPhase === "ready") return;
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
        /* try next protocol / retry */
      }
    }

    if (healthy) {
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: machine.runnersAuthorized === false ? "authorizing-runners" : "ready",
        progress: machine.runnersAuthorized === false ? 90 : 100,
      });
      console.log(`[cloudMachines.resumeHealthCheck] ready: ${machine.hostname}`);
      return;
    }
    if (attempt >= 10) {
      // Give up quietly — the box may still be reachable P2P over the
      // relay even if the hostname /health probe never succeeded.
      console.log(`[cloudMachines.resumeHealthCheck] gave up after ${attempt}: ${machine.hostname}`);
      return;
    }
    await ctx.scheduler.runAfter(15_000, internal.cloudMachines.resumeHealthCheck, {
      machineId,
      attempt: attempt + 1,
    });
  },
});

// RECOVERY: "user paid but no cloud resource". A signed webhook can
// be missed (delivery failure, transient provision error, box later
// errored/destroyed) leaving an active subscription with no live box
// — the user paid for nothing. This reconciler (hourly cron + manual
// owner trigger) re-provisions any active managed subscription that
// has no machine in a healthy/in-flight state. Idempotent:
// ensureForSubscription returns the existing row if one is already
// {provisioning|active}, so a healthy box is never duplicated.
// project_managed_cloud_onboarding_gap (recovery).
const HEALTHY_OR_INFLIGHT = new Set([
  "provisioning", "active", "grace", "stopping",
]);

export const reconcileSubscriptions = internalAction({
  args: { onlyUserId: v.optional(v.id("users")) },
  handler: async (
    ctx,
    args,
  ): Promise<{ checked: number; repaired: number; cancelled: number }> => {
    const subs = await ctx.runQuery(internal.subscriptions.listActiveManaged, {});
    let repaired = 0;
    let cancelled = 0;
    for (const s of subs) {
      if (args.onlyUserId && s.userId !== args.onlyUserId) continue;
      const machinesRaw = await ctx.runQuery(
        internal.cloudMachines.listBySubscription,
        { subscriptionId: s.subscriptionId },
      );
      const machines: Array<{ status: string }> = Array.isArray(machinesRaw)
        ? machinesRaw
        : [];

      // A healthy/in-flight box — or one the user intentionally PAUSED
      // — means the subscription is doing its job. Leave it alone.
      if (
        machines.some(
          (m) => HEALTHY_OR_INFLIGHT.has(m.status) || m.status === "paused",
        )
      ) {
        continue;
      }

      // No live box. If every machine row is "stopped", the box was
      // deliberately torn down (decommission / destroy) and the still-
      // active subscription is ORPHANED — cancel it (Convex row +
      // LemonSqueezy, via cancelById) instead of resurrecting a box the
      // user already removed. This was the re-provision money-landmine;
      // reconcile now DISARMS it rather than arming it.
      if (
        machines.length > 0 &&
        machines.every((m) => m.status === "stopped")
      ) {
        await ctx.runMutation(internal.subscriptions.cancelById, {
          subscriptionId: s.subscriptionId,
        });
        cancelled++;
        console.log(
          `[reconcile] cancelled orphaned sub ${s.subscriptionId} — its box was decommissioned`,
        );
        continue;
      }

      // Genuinely no box (the create never ran) or a failed "error"
      // box → recovery: re-provision. machineType from the plan label
      // ("yaver-cloud-gpu" ⇒ gpu). Tier defaults byok (a hosted sub
      // still gets a working box; hosted Convex re-bootstraps).
      const machineType = s.plan.includes("gpu") ? "gpu" : "cpu";
      await ctx.runMutation(api.cloudMachines.ensureForSubscription, {
        userId: s.userId,
        machineType,
        subscriptionId: s.subscriptionId,
        region: "eu",
      });
      repaired++;
      console.log(
        `[reconcile] re-provisioned ${machineType} for sub ${s.subscriptionId} (paid, had no live box)`,
      );
    }
    return { checked: subs.length, repaired, cancelled };
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

    // User decommission ends BILLING too: cancel the linked
    // subscription so (a) the user stops paying and (b) the hourly
    // reconcile recovery (acts only on active subs) does NOT
    // resurrect the box the user just removed. Idempotent.
    if (machine.subscriptionId) {
      await ctx.runMutation(internal.subscriptions.cancelById, {
        subscriptionId: machine.subscriptionId,
      });
    }

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
      // Snapshot ONLY when it's the user's only data copy (hosted) or
      // they explicitly opted in. byok boxes are DISPOSABLE — taking a
      // pre-delete snapshot of one is recurring storage cost for
      // nothing (precisely the per-delete charge to avoid). Skip
      // entirely for byok ⇒ no snapshot, no lingering cost.
      if (mustSnapshot || machine.snapshotOnDelete === true) {
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
      } // end snapshot gate — byok skips, so no orphan/cost
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
