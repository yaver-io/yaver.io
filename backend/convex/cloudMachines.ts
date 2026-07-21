import { mutation, query, internalMutation, internalAction, internalQuery } from "./_generated/server";
import { v } from "convex/values";
import { api, internal } from "./_generated/api";
import { listGrantedMachineIdsForGrant, listVisibleInfraGrantsForGuest } from "./access";
import { isOwnerUserId } from "./ownerAllowlist";
import { randomHex, sha256Hex } from "./auth";
import { selectComputeProvider } from "./cloudProviders/selection";

// Machine specs by type. The Hetzner server_type strings are what you pass
// to POST https://api.hetzner.cloud/v1/servers.
const MACHINE_SPECS = {
  standard: {
    // Normie Cloud Workspace default: enough for one app / Yaver serverless /
    // Hermes iteration without burning the $29 plan margin. CX32 is the current
    // 4 vCPU / 8 GB / 80 GB shared-vCPU shape in Hetzner's CX line. Keep the
    // exact provider type env-overridable because regional availability changes.
    hetznerType: "cx32",
    vcpu: 4,
    ramGb: 8,
    diskGb: 80,
    arch: "amd64" as const,
  },
  heavy: {
    // Two apps, Docker-heavy dev servers, or larger monorepos. Internal only:
    // users buy Cloud Workspace, placement picks this when needed.
    hetznerType: "cx42",
    vcpu: 8,
    ramGb: 16,
    diskGb: 160,
    arch: "amd64" as const,
  },
  build: {
    // Native mobile builds / large monorepo checks. Prefer this only when the
    // placement layer has evidence; otherwise it will erase the flat-plan margin.
    hetznerType: "cx52",
    vcpu: 16,
    ramGb: 32,
    diskGb: 320,
    arch: "amd64" as const,
  },
  cpu: {
    // History: cx42 DEPRECATED (422 "server type 106 is deprecated");
    // cpx41 non-deprecated but US-only stock; ccx33 (dedicated) works
    // EU+US but costs €73.99/mo. cpx42 (8 vCPU/16 GB) was the prior
    // default — fine for a single RN/Hermes app, but TIGHT for a real
    // large monorepo (workspace install + monorepo-wide tsc +
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
    hetznerType: "cpx51",    // legacy 16 vCPU, 32 GB RAM, 360 GB, amd64 (shared)
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

function normalizeMachineType(value: string | undefined | null): keyof typeof MACHINE_SPECS {
  const normalized = String(value || "").trim().toLowerCase();
  if (normalized === "standard" || normalized === "heavy" || normalized === "build" || normalized === "cpu" || normalized === "gpu") {
    return normalized as keyof typeof MACHINE_SPECS;
  }
  return "standard";
}

export function reusableSubscriptionMachineStatus(status: unknown): boolean {
  return [
    "active",
    "provisioning",
    "resuming",
    "paused",
    "suspended",
    "grace",
  ].includes(String(status || ""));
}

function envServerTypeFor(machineType: string): string | undefined {
  const key = `YAVER_CLOUD_${String(machineType || "standard").toUpperCase().replace(/[^A-Z0-9]/g, "_")}_TYPE`;
  return process.env[key] || undefined;
}

/**
 * The device id a managed box registers as. Derived, never invented: it is
 * `cloud-<first 8 of the machine _id>`, the same slug that builds the box's
 * hostname (`<shortId>.cloud.yaver.io`) and that provision bakes into the
 * box's own config.json.
 *
 * Derivable is the point. The row's `deviceId` column is only written when a
 * box successfully registers, and a box whose Yaver session expired never
 * does — so the column stayed null on exactly the machines that most needed
 * identifying, and the phone refused to sign them in for want of an id it
 * could have computed all along. Waking from a snapshot never wrote it either
 * (cloudLifecycle.ts recreates the server and doesn't touch deviceId), so a
 * park/wake cycle could strand a box that had been fine.
 */
export function managedDeviceIdFor(machineId: string): string {
  return `cloud-${machineId.toString().substring(0, 8)}`;
}

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
  // Per-box self-relay password (Phase 2A). When set, the yaver-cloud
  // image starts its bundled relay on QUIC 4433/UDP + HTTP 8443/TCP and
  // ufw opens those ports. The user's OTHER self-hosted devices then
  // prefer this user-owned relay (set in userSettings.relayUrl/relayPassword)
  // over the shared free platform relay. Absent ⇒ no relay listener.
  boxRelayPassword?: string;
  // Hetzner Volume id for durable agent/container state. When present,
  // cloud-init mounts it at /srv/yaver/state before writing config, auth,
  // repos, and caches.
  volumeId?: string;
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

  const persistentStateMount = spec.volumeId
    ? `  # ── durable state volume (/root inside yaver-cloud container) ─────
  - |
    set -euo pipefail
    dev=${shellSingleQuote(`/dev/disk/by-id/scsi-0HC_Volume_${spec.volumeId}`)}
    for i in $(seq 1 30); do
      [ -b "$dev" ] && break
      sleep 2
    done
    if [ ! -b "$dev" ]; then
      echo "[cloud-init] persistent volume ${spec.volumeId} did not appear; refusing ephemeral state"
      exit 1
    fi
    if ! blkid "$dev" >/dev/null 2>&1; then
      echo "[cloud-init] persistent volume ${spec.volumeId} has no filesystem; recreate it with format=ext4"
      exit 1
    fi
    mkdir -p /srv/yaver/state
    if ! grep -q " /srv/yaver/state " /etc/fstab; then
      echo "$dev /srv/yaver/state ext4 discard,nofail,defaults 0 0 # yaver-state-volume" >> /etc/fstab
    fi
    mountpoint -q /srv/yaver/state || mount /srv/yaver/state
`
    : "";

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
  // Mount the host's /etc/yaver so the CONTAINERIZED agent can read
  // machine.json (managed-box identity). Without it loadMachineIdentity() is
  // nil inside the container and git-autohydrate + idle-auto-off silently
  // no-op — the box never inherits the owner's gh/glab creds and never parks
  // itself. Bind mount reflects host writes live, so ordering vs the
  // machine.json write in cloud-init doesn't matter.
  yaverDockerRunLines.push(`      -v /etc/yaver:/etc/yaver \\`);
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
${persistentStateMount}
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
  # these as normal sibling repos under ~/Workspace. The bootstrap is
  # intentionally repeatable so optional user repo clones can succeed later
  # after GitHub credentials are configured from mobile.
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
// internalQuery: returns a user's machines (IPs, hostnames, provider IDs).
// Public exposure let any caller read ANY user's fleet by passing their userId.
// Callers are server-side HTTP routes that pass the authenticated session's own
// userDocId.
export const listForUser = internalQuery({
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
// internalQuery: single-machine read by id (see getInternal for the
// server-trusted variant). Public exposure leaked any machine's row by id.
export const get = internalQuery({
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
  const machineType = normalizeMachineType(args.machineType);
  const specDef = MACHINE_SPECS[machineType];
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
  if (machineType === "gpu") {
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
    machineType,
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
// internalMutation, NOT public: it inserts a machine row + schedules a
// billable Hetzner provision. Reachable only via server-side HTTP routes
// and LemonSqueezy webhook/reconcile flows that authenticate the bearer +
// scope userId to the caller's own session. The legacy wallet-funded
// /billing/yaver-cloud/provision route is disabled. A public mutation here
// would let anyone with the deployment URL create rows for any userId.
export const create = internalMutation({
  args: {
    userId: v.id("users"),
    machineType: v.string(),        // "standard" | "heavy" | "build" | legacy "cpu" | "gpu"
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

// createPreviewSharedMachine registers the owner/dev preview server as a
// cloudMachines row WITHOUT scheduling internal.cloudMachines.provision.
// The preview path is for testing an already-running shared machine; it must
// never allocate a fresh Hetzner server as a side effect.
export const createPreviewSharedMachine = internalMutation({
  args: {
    userId: v.id("users"),
    region: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const spec = MACHINE_SPECS.standard;
    const now = Date.now();
    return await ctx.db.insert("cloudMachines", {
      userId: args.userId,
      machineType: "standard",
      origin: "managed",
      status: "active",
      multiUser: false,
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
    const spec = MACHINE_SPECS.standard;
    const now = Date.now();
    return await ctx.db.insert("cloudMachines", {
      userId: args.userId,
      machineType: "standard",
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

// internalMutation: creates/revives a machine off a subscription. Only the
// LemonSqueezy webhook path and the subscription-reconcile action call it.
export const ensureForSubscription = internalMutation({
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
          reusableSubscriptionMachineStatus(machine.status),
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
    volumeId: v.optional(v.string()),
    volumeSizeGb: v.optional(v.number()),
    baseImageId: v.optional(v.string()),
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
    if (args.volumeId) patch.volumeId = args.volumeId;
    if (typeof args.volumeSizeGb === "number") patch.volumeSizeGb = args.volumeSizeGb;
    if (args.baseImageId) patch.baseImageId = args.baseImageId;
    await ctx.db.patch(args.machineId, patch);
  },
});

export const setResizedProvisioned = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    targetMachineType: v.string(),
    hetznerServerId: v.string(),
    serverIp: v.string(),
    hostname: v.string(),
    serverType: v.string(),
  },
  handler: async (ctx, args) => {
    const machineType = normalizeMachineType(args.targetMachineType);
    const specDef = MACHINE_SPECS[machineType];
    const optionalSpec = specDef as { gpu?: string; vram?: number };
    await ctx.db.patch(args.machineId, {
      machineType,
      specs: {
        vcpu: specDef.vcpu,
        ramGb: specDef.ramGb,
        diskGb: specDef.diskGb,
        arch: specDef.arch,
        ...(optionalSpec.gpu ? { gpu: optionalSpec.gpu } : {}),
        ...(typeof optionalSpec.vram === "number" ? { vram: optionalSpec.vram } : {}),
      },
      serverType: args.serverType,
      hetznerServerId: args.hetznerServerId,
      cloudResourceId: args.hetznerServerId,
      serverIp: args.serverIp,
      hostname: args.hostname,
      resizeTargetMachineType: undefined,
      resizeRequestedAt: undefined,
      resizeReason: undefined,
      resizePlacementId: undefined,
      errorMessage: undefined,
      updatedAt: Date.now(),
    });
    return { ok: true, machineType };
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
    const machineType = normalizeMachineType(args.machineType);
    const specDef = MACHINE_SPECS[machineType];
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
      machineType,
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
    const machineType = normalizeMachineType(args.machineType);
    const specDef = MACHINE_SPECS[machineType];
    if (!specDef) throw new Error("Invalid machine type: " + args.machineType);

    const machineId: any = await ctx.runMutation(internal.cloudMachines.createByoRow, {
      userId: args.userId,
      machineType,
      region: args.region,
    });

    const shortId = machineId.toString().substring(0, 8);
    const serverName = `yaver-${machineType}-${shortId}`;
    const deviceId = `byo-${shortId}`;
    const isGpu = machineType === "gpu";
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
    // Wake/park timestamps — stamped here so EVERY transition path records
    // them (idle sweep → paused, manual pause, wake() → resuming, resumeMachine
    // → resuming/active). The UI shows "slept 3h ago" / "woke 2m ago" from these.
    if (
      args.status === "stopped" ||
      args.status === "paused" ||
      args.status === "suspended" ||
      args.status === "stopping" ||
      args.status === "grace"
    ) {
      patch.lastParkedAt = Date.now();
    }
    if (args.status === "resuming") patch.lastWokeAt = Date.now();
    if (args.lastSnapshotId) {
      patch.lastSnapshotId = args.lastSnapshotId;
      patch.lastSnapshotAt = Date.now();
    }
    await ctx.db.patch(args.machineId, patch);
  },
});

export const requestResize = internalMutation({
  args: {
    userId: v.id("users"),
    machineId: v.id("cloudMachines"),
    targetMachineType: v.string(),
    placementId: v.optional(v.id("taskPlacements")),
    reason: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const machine = await ctx.db.get(args.machineId);
    if (!machine) return { ok: false, error: "machine not found" };
    if (String(machine.userId) !== String(args.userId)) {
      return { ok: false, error: "not your machine" };
    }
    const targetMachineType = normalizeMachineType(args.targetMachineType);
    if (!machine.volumeId || !machine.baseImageId) {
      return { ok: false, error: "machine has no persistent volume-backed recovery source" };
    }
    const now = Date.now();
    await ctx.db.patch(args.machineId, {
      resizeTargetMachineType: targetMachineType,
      resizeRequestedAt: now,
      resizeReason: args.reason?.slice(0, 200),
      resizePlacementId: args.placementId,
      provisionPhase: "resize-required",
      provisionProgress: 0,
      provisionPhaseAt: now,
      provisionError: undefined,
      updatedAt: now,
    });
    await ctx.scheduler.runAfter(0, internal.cloudLifecycle.resizeMachine, {
      machineId: args.machineId,
    });
    return { ok: true, targetMachineType };
  },
});

/**
 * isMachineWakeable — the ONE answer to "can this box be woken?"
 *
 * Two conditions, and both matter:
 *   1. a resumable status — a `removed` or `error` box is gone, not asleep, and
 *      offering to wake (or worse, PAUSE) it is a nonsense action.
 *   2. something to recreate it FROM. Scale-to-zero DELETES the server
 *      (Hetzner bills stopped ones), so a legacy row needs a full-disk
 *      snapshot and a fast-path row needs both its data volume and slim
 *      base image.
 *
 * This lives next to wakeMachine, its enforcement point, and is exported so the
 * device list can ship the same verdict to every client. The web previously
 * re-derived it from status alone and drifted from this gate: it offered Resume
 * on snapshot-less boxes that wakeMachine then refused, and offered PAUSE on
 * boxes that were already removed. A client must never re-implement this rule —
 * read `machineWakeable` off the device row instead.
 */
export function isMachineWakeable(machine: {
  status?: string;
  lastSnapshotId?: string;
  volumeId?: string;
  baseImageId?: string;
}): boolean {
  const status = String(machine.status ?? "");
  const resumable = status === "stopped" || status === "paused" || status === "suspended";
  if (!resumable) return false;
  return Boolean(machine.lastSnapshotId || (machine.volumeId && machine.baseImageId));
}

// seedParkedMachine records a legacy PARKED, wakeable managed machine — a box
// that was snapshotted + deleted (metered billing) so it accrues no server cost
// but can be recreated from its snapshot on demand. New Cloud Workspace rows
// prefer volume + base-image wake. Without a row here the mobile/web/CLI
// surfaces render nothing for it (the box is invisible). listForUser returns
// any-status row so every surface shows "Yaver-managed · Parked" with a Wake
// action. Idempotent on (userId, lastSnapshotId).
//
// STATUS = "paused": the wake path (POST /billing/yaver-cloud/start →
// cloudLifecycle.resumeMachine, and the wake() mutation below) only resumes a box
// whose status is "paused" or "suspended" — "stopped" is NOT resumable in the
// current pipeline. So a parked-but-wakeable box MUST be stored as "paused" for
// the recreate-from-snapshot to fire. The UI treats stopped|paused|suspended all
// as "Parked", so a legacy "stopped" row still renders correctly; re-running this
// seed migrates it to the wakeable "paused" state. lastParkedAt lets the card show
// "slept N ago".
export const seedParkedMachine = internalMutation({
  args: {
    userId: v.id("users"),
    lastSnapshotId: v.string(),
    serverType: v.string(),
    machineType: v.optional(v.string()),
    region: v.optional(v.string()),
    vcpu: v.optional(v.number()),
    ramGb: v.optional(v.number()),
    diskGb: v.optional(v.number()),
    arch: v.optional(v.string()),
  },
  handler: async (ctx, args) => {
    const existing = (
      await ctx.db
        .query("cloudMachines")
        .withIndex("by_user", (q) => q.eq("userId", args.userId))
        .collect()
    ).find((m) => m.lastSnapshotId === args.lastSnapshotId);
    if (existing) {
      // Keep it parked + refresh the snapshot pointer; don't duplicate.
      // "paused" (not "stopped") so the wake pipeline can recreate it.
      await ctx.db.patch(existing._id, {
        status: "paused",
        origin: "managed",
        serverType: args.serverType,
        lastSnapshotId: args.lastSnapshotId,
        lastSnapshotAt: Date.now(),
        lastParkedAt: Date.now(),
        updatedAt: Date.now(),
      });
      return existing._id;
    }
    const now = Date.now();
    const machineId = await ctx.db.insert("cloudMachines", {
      userId: args.userId,
      machineType: normalizeMachineType(args.machineType),
      serverType: args.serverType,
      origin: "managed",
      status: "paused", // parked = wakeable-from-snapshot (resumable status)
      lastSnapshotId: args.lastSnapshotId,
      lastSnapshotAt: now,
      lastParkedAt: now,
      region: args.region ?? "eu",
      tools: [],
      specs: {
        vcpu: args.vcpu ?? 8,
        ramGb: args.ramGb ?? 16,
        diskGb: args.diskGb ?? 160,
        arch: args.arch ?? "amd64",
      },
      createdAt: now,
      updatedAt: now,
    });
    // Give it the same canonical name a provisioned box gets. A nameless row
    // silently disabled the DNS upsert AND the resume health check (both are
    // guarded on hostname), which is how a seeded box could wake, run, bill,
    // and never be verified — so name it up front rather than at first wake.
    await ctx.db.patch(machineId, {
      hostname: `${machineId.toString().substring(0, 8)}.cloud.yaver.io`,
    });
    return machineId;
  },
});

// wake — owner-scoped WAKE entry point for a PARKED managed box. A parked box
// (stopped/paused/suspended) has deleted its server; this recreates it from the
// recorded recovery source (volume + base image for fast-path rows, snapshot
// for legacy rows) by delegating to the real, Hetzner-integrated recreate path
// (cloudLifecycle.resumeMachine) — we deliberately do NOT reinvent the Hetzner
// API calls here.
//
// Contract:
//   • Ownership is validated against the caller-supplied userId (the HTTP layer
//     authenticates the Yaver session and passes the resolved userDocId — the
//     same trust boundary every other internalMutation in this file relies on).
//   • lastWokeAt is stamped now (the user's "when did I wake it" signal).
//   • A "stopped" row is normalised to "paused" so resumeMachine's gate
//     (paused|suspended only) passes — otherwise a legacy stopped box would
//     dead-end at "not resumable from status stopped".
//   • We then schedule internal.cloudLifecycle.resumeMachine, which owns the
//     status ladder (paused → resuming → active) and the phase/progress ticks
//     the mobile/web card animates. We do NOT pre-set "resuming" here: doing so
//     would trip resumeMachine's own gate (it refuses anything but paused/
//     suspended). resumeMachine flips to "resuming" itself within ~1s.
//
// NOTE (mobile today): the mobile app has no Convex client — it wakes via the
// existing POST /billing/yaver-cloud/start HTTP route (→ resumeMachine). This
// mutation is the owner-scoped programmatic entry point for web/CLI/tests and
// the documented single place a future wake route can call. It is fully wired
// except for that HTTP surface, which lives in http.ts (out of scope here).
export const wake = internalMutation({
  args: {
    userId: v.id("users"),
    machineId: v.id("cloudMachines"),
  },
  handler: async (ctx, args) => {
    const machine = await ctx.db.get(args.machineId);
    if (!machine) return { ok: false, error: "machine not found" };
    // Ownership — one developer can never wake another's box even though all
    // managed boxes share Yaver's platform token.
    if (String(machine.userId) !== String(args.userId)) {
      return { ok: false, error: "not your machine" };
    }
    if (machine.status === "active") {
      return { ok: true, status: "active", alreadyAwake: true };
    }
    if (!isMachineWakeable(machine)) {
      if (!machine.lastSnapshotId && !(machine.volumeId && machine.baseImageId)) {
        return { ok: false, error: "no snapshot or volume-backed base image recorded — cannot recreate the box" };
      }
      return { ok: false, error: `not wakeable from status ${machine.status}` };
    }
    // Normalise stopped → paused (resumeMachine's gate) + stamp the wake time.
    // Seed the device id here too. It is derived from the machine id, so it is
    // the same value on every wake for the rest of this box's life — but the
    // column is only written when a box registers, and resumeMachine recreates
    // the server from a snapshot whose Yaver token may already have expired. A
    // box in that state never registers, so the id never lands, and the phone
    // then refuses to sign it in for want of an id we could always compute.
    // Writing it before the wake starts closes that loop.
    await ctx.db.patch(args.machineId, {
      status: "paused",
      lastWokeAt: Date.now(),
      deviceId: machine.deviceId ?? managedDeviceIdFor(args.machineId.toString()),
      updatedAt: Date.now(),
    });
    // Delegate to the real recovery-source action (owns the wake ladder).
    await ctx.scheduler.runAfter(0, internal.cloudLifecycle.resumeMachine, {
      machineId: args.machineId,
    });
    return { ok: true, status: "resuming" };
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
  "starting-agent", "registering", "awaiting-yaver-auth", "authorizing-runners", "ready", "error",
  // Wake-only steps. A resume does not "create" a box from nothing — it finds
  // the park snapshot, frees the volume, then restores onto a new server, and
  // each of those can be where a wake stalls. Clients map all three onto the
  // single "Restoring" rung of the wake ladder; the slug is what tells the
  // user WHICH part is slow.
  "checking-snapshot", "preparing-volume", "restoring-snapshot",
] as const;

/**
 * setLifecycleTiming — record how a wake/park run went.
 *
 * Deliberately a dumb patch of whatever the caller passes: the lifecycle
 * actions know the semantics (a wake started, a park finished, a snapshot
 * turned out to be 42 GB) and this just persists it. Every field is optional
 * so a caller can stamp one moment without clobbering the others.
 */
export const setLifecycleTiming = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    wakeStartedAt: v.optional(v.number()),
    wakeCompletedAt: v.optional(v.number()),
    lastWakeDurationMs: v.optional(v.number()),
    lastWakeOutcome: v.optional(v.string()),
    parkStartedAt: v.optional(v.number()),
    parkCompletedAt: v.optional(v.number()),
    lastParkDurationMs: v.optional(v.number()),
    snapshotSizeGb: v.optional(v.number()),
    snapshotCreatedAt: v.optional(v.number()),
    /** Clear the previous run's outcome when a fresh wake starts. */
    clearWakeOutcome: v.optional(v.boolean()),
  },
  handler: async (ctx, args) => {
    const { machineId, clearWakeOutcome, ...fields } = args;
    const patch: Record<string, unknown> = { updatedAt: Date.now() };
    for (const [k, val] of Object.entries(fields)) {
      if (val !== undefined) patch[k] = val;
    }
    if (clearWakeOutcome) {
      patch.lastWakeOutcome = undefined;
      patch.wakeCompletedAt = undefined;
    }
    await ctx.db.patch(machineId, patch);
    if (args.wakeStartedAt) {
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "wake",
        status: "running",
        phase: "starting",
        progress: 1,
      }).catch(() => {});
    }
    if (args.wakeCompletedAt || args.lastWakeOutcome) {
      const outcome = String(args.lastWakeOutcome || "");
      const ok = !args.lastWakeOutcome || outcome === "ready";
      const blocked = outcome === "needs-auth";
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "wake",
        status: ok ? "succeeded" : blocked ? "blocked" : "failed",
        phase: ok ? "ready" : String(args.lastWakeOutcome || "failed"),
        progress: ok ? 100 : undefined,
        error: ok ? undefined : String(args.lastWakeOutcome || "wake failed"),
      }).catch(() => {});
    }
    if (args.parkStartedAt) {
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "park",
        status: "running",
        phase: "snapshotting",
        progress: 10,
      }).catch(() => {});
    }
    if (args.parkCompletedAt) {
      await ctx.runMutation(internal.wakeRuns.markProgress, {
        machineId,
        kind: "park",
        status: "succeeded",
        phase: "parked",
        progress: 100,
      }).catch(() => {});
    }
  },
});

/**
 * setProviderStatus — record what the cloud provider says the server is doing.
 * Separate from setPhase because it is THEIR vocabulary on THEIR schedule;
 * folding it into our phase ladder would let a provider string move our bar.
 */
export const setProviderStatus = internalMutation({
  args: { machineId: v.id("cloudMachines"), status: v.string() },
  handler: async (ctx, { machineId, status }) => {
    const machine = await ctx.db.get(machineId);
    await ctx.db.patch(machineId, {
      providerStatus: status.slice(0, 40),
      providerStatusAt: Date.now(),
      updatedAt: Date.now(),
    });
    await ctx.runMutation(internal.wakeRuns.markProgress, {
      machineId,
      status: "running",
      provider: (machine as any)?.provider ?? "hetzner",
      providerResourceId: (machine as any)?.cloudResourceId ?? (machine as any)?.hetznerServerId,
      providerStatus: status,
    }).catch(() => {});
  },
});

/**
 * Record ONLY the runner-authorization fact, without touching the provision
 * phase. setPhase requires a phase and has side effects (it re-stamps
 * provisionPhaseAt and clears provisionError), so it is the wrong tool when a
 * caller knows about runners but must not claim anything about where the wake
 * is. The runners-authorized route uses this while a box is still climbing —
 * writing "ready" there disarms resumeHealthCheck's watchdog (it early-returns
 * on phase "ready") and leaves the box billing in "resuming" forever.
 */
export const setRunnersAuthorized = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    runnersAuthorized: v.boolean(),
  },
  handler: async (ctx, args) => {
    await ctx.db.patch(args.machineId, {
      runnersAuthorized: args.runnersAuthorized,
      updatedAt: Date.now(),
    });
  },
});

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
    const machine = await ctx.db.get(args.machineId);
    if (args.phase === "ready" && machine?.status !== "active") {
      return { ok: false, reason: "ready requires active machine status" };
    }
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
    await ctx.runMutation(internal.wakeRuns.markProgress, {
      machineId: args.machineId,
      phase: args.phase,
      progress: typeof args.progress === "number" ? args.progress : undefined,
      status: args.phase === "ready"
        ? "succeeded"
        : args.phase === "error"
          ? "failed"
          : args.phase === "awaiting-yaver-auth" || args.phase === "authorizing-runners"
            ? "blocked"
            : "running",
      error: args.phase === "error" || args.phase === "awaiting-yaver-auth" ? args.error : undefined,
    }).catch(() => {});
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
  "cloud-workspace": 1,
};
export function managedMachineLimit(plan?: string | null): number {
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
export const quota = internalQuery({
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
    // Provider-neutral placement. The user CANNOT influence this: the decision
    // is server-side only, defaults to Hetzner, and refuses any provider that
    // is not production-eligible or cannot satisfy the paid-placement
    // capability floor (delete-stops-spend, durable volume, tagged cleanup).
    // An operator may force an adapter for testing via a server-side env var,
    // which is loud and auditable — never a request field.
    let cloudProvider;
    let selectedProviderId: string = "hetzner";
    try {
      const selection = selectComputeProvider();
      cloudProvider = selection.provider;
      selectedProviderId = selection.providerId;
      if (selection.operatorForced) {
        console.warn(`[cloudMachines.provision] ${selection.reason}`);
      }
    } catch (e) {
      // Never silently fall back to a different provider — a placement we did
      // not intend is how spend lands somewhere nobody is watching.
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId: args.machineId,
        status: "error",
        errorMessage: `No eligible compute provider: ${e instanceof Error ? e.message : String(e)}`,
      });
      return;
    }

    await ctx.runMutation(internal.cloudMachines.setProviderId, {
      machineId: args.machineId,
      provider: selectedProviderId,
    });

    // ─── Serverless pool assignment (hosted tier only) ────────────────────
    // A hosted backend must STAY UP to serve requests, so it can never park —
    // which removes the one mechanism that makes a dedicated box affordable.
    // Dedicated is 14% gross at $29; pooled ~10 tenants it is 91%. So the same
    // rule that fixed Relay Pro applies: it cannot park, therefore it shares.
    //
    // Assigned BEFORE any provider spend so a reused host never reaches the
    // create path. byok workspaces are untouched — they park, so they stay
    // dedicated and keep VM-level isolation.
    let serverlessSlot: { hostKey: string; needsProvision: boolean; reason: string } | null = null;
    {
      const pre = await ctx.runQuery(internal.cloudMachines.getInternal, {
        machineId: args.machineId,
      });
      if (pre?.tier === "hosted") {
        serverlessSlot = await ctx.runMutation(internal.serverlessPool.assignToPool, {
          machineId: args.machineId,
          region: pre.region || "eu",
        });
        const host = await ctx.runQuery(internal.serverlessPool.hostEndpoint, {
          hostKey: serverlessSlot.hostKey,
        });
        if (host?.serverId && host.serverIp) {
          // REUSE: this is the entire saving. Creating a second box here would
          // silently restore the 14% margin with no visible symptom.
          //
          // ⚠️ Placement readiness is NOT runtime readiness. Co-tenanting
          // executing tenant functions requires the isolation floor in
          // desktop/agent/serverless_isolation.go, which itself reports NOT
          // ready for untrusted third-party code on a shared kernel.
          await ctx.runMutation(internal.cloudMachines.setProvisioned, {
            machineId: args.machineId,
            hetznerServerId: host.serverId,
            serverIp: host.serverIp,
            hostname: `${String(args.machineId).substring(0, 8)}.cloud.yaver.io`,
            serverType: "shared",
          });
          console.log(
            `[cloudMachines.provision] hosted backend joined shared host ${serverlessSlot.hostKey} (${serverlessSlot.reason})`,
          );
          return;
        }
      }
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

    const machineType = normalizeMachineType(machine.machineType);
    const specDef = MACHINE_SPECS[machineType];
    if (!specDef) {
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId: args.machineId,
        status: "error",
        errorMessage: "Unknown machine type: " + machine.machineType,
      });
      return;
    }

    const shortId = machine._id.toString().substring(0, 8);
    const serverName = `yaver-${machineType}-${shortId}`;
    const subdomain = `${shortId}.cloud`;
    const autoDomain = `${shortId}.cloud.yaver.io`;
    const isGpu = machineType === "gpu";

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
    const deviceId = managedDeviceIdFor(machineIdStr);
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
    // because the bundled relay is the whole point of the architecture:
    // the box runs yaver-relay from the yaver-cloud image so the user's OTHER
    // self-hosted devices can use it (managedRelays row + userSettings
    // pointers wired below, post-Hetzner). Dev-adopt boxes lacking a
    // subscriptionId skip the relay wiring (managedRelays.create requires
    // subId), so we only thread the password when both sides will land.
    const boxRelayPassword = machine.subscriptionId ? randomHex(24) : "";

    const bootstrapSpec: ManagedCloudBootstrapSpec = {
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
    };
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
    const baseImageId = typeof bootImage === "number" ? String(bootImage) : bootImage;
    let persistentVolumeId: string | undefined;
    // Provider-native id of a server that EXISTS but whose row has not been
    // written yet. This window is the R1 orphan: create succeeds, a later step
    // throws, and the VM bills forever with nothing referencing it and no sweep
    // able to find it (2026-07-21 audit).
    let createdCloudResourceId: string | undefined;
    let persistentVolumeSizeGb: number | undefined;

    try {
      // ── 1. Optional persistent data volume ──────────────────────
      // The container/golden-image path keeps mutable state in
      // /srv/yaver/state. Create that volume BEFORE the server so it can be
      // attached on first boot and cloud-init never writes OAuth/repo/cache
      // state onto a disposable boot disk. Legacy in-VM bootstrap keeps the
      // explicit migration path instead.
      if (cloudImage) {
        await ctx.runMutation(internal.cloudMachines.setPhase, {
          machineId: args.machineId,
          phase: "preparing-volume",
          progress: 12,
        });
        persistentVolumeSizeGb = Math.max(10, Math.round(specDef.diskGb));
        persistentVolumeId = (
          await cloudProvider.createVolume({
            name: `yaver-data-${machineIdStr}`.slice(0, 60),
            sizeGb: persistentVolumeSizeGb,
            region: machine.region ?? "eu",
            tags: {
              service: "yaver-cloud-machine",
              machine_type: machineType,
              user: machine.userId.toString().substring(0, 10),
              managed: "true",
              resource: "state-volume",
            },
          })
        ).volumeId;
        bootstrapSpec.volumeId = persistentVolumeId;
      }

      // ── 2. Hetzner server ───────────────────────────────────────
      // Cost/availability override per internal profile. Captured on the row so
      // resume recreates the exact same type; snapshots cannot restore onto a
      // smaller disk. Legacy "cpu" keeps YAVER_CLOUD_CPU_TYPE support.
      const createdServerType = envServerTypeFor(machineType) ?? specDef.hetznerType;
      const finalCloudInit = cloudImage
        ? buildManagedCloudInitContainer(bootstrapSpec, cloudImage)
        : cloudInit;
      const created = await cloudProvider.createMachine({
          name: serverName,
          sku: createdServerType,
          image: bootImage,
          region: machine.region ?? "eu",
          volumeIds: persistentVolumeId ? [persistentVolumeId] : undefined,
          sshKeyNames: bootSshKeyNames.length ? bootSshKeyNames : undefined,
          tags: {
            service: "yaver-cloud-machine",
            machine_type: machineType,
            user: machine.userId.toString().substring(0, 10),
            managed: "true",
          },
          userData: finalCloudInit,
      });
      const hetznerServerId = created.cloudResourceId;
      // Remember the server the MOMENT it exists. Everything below can throw,
      // and until setProvisioned runs there is no row pointing at this server —
      // so without this the catch has no idea a billing VM was created.
      createdCloudResourceId = hetznerServerId;
      const serverIp = created.serverIp;
      if (!serverIp) {
        throw new Error(`${selectedProviderId} provider returned no public IPv4 address`);
      }

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
        ...(persistentVolumeId
          ? {
              volumeId: persistentVolumeId,
              volumeSizeGb: persistentVolumeSizeGb,
              baseImageId,
            }
          : {}),
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
      let msg = e instanceof Error ? e.message : String(e);
      console.error("[cloudMachines.provision] failed:", msg);
      // ── Reclaim the SERVER first: it is the expensive thing, and it is the
      // one this path used to abandon. Ordered before the volume because a
      // Hetzner volume delete fails while still attached to a live server.
      if (createdCloudResourceId) {
        try {
          await cloudProvider.deleteMachine({ cloudResourceId: createdCloudResourceId });
        } catch (deleteErr) {
          // We could not stop the meter. Say so LOUDLY on the row with the
          // provider id — a running server nobody knows about is the one
          // outcome we never accept, and this string is the only breadcrumb.
          const de = deleteErr instanceof Error ? deleteErr.message : String(deleteErr);
          msg = `${msg} — AND the created server could not be deleted (${de}). ` +
            `${selectedProviderId} resource ${createdCloudResourceId} is STILL RUNNING and billing: delete it manually.`;
          console.error("[cloudMachines.provision] ORPHAN SERVER:", createdCloudResourceId, de);
        }
      }
      if (persistentVolumeId) {
        try {
          await cloudProvider.deleteVolume({ volumeId: persistentVolumeId });
        } catch (deleteErr) {
          console.error(
            "[cloudMachines.provision] cleanup volume failed:",
            deleteErr instanceof Error ? deleteErr.message : String(deleteErr),
          );
        }
      }
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
    const target = machine.hostname || machine.serverIp;
    if (!target) return;

    // Same contract as resumeHealthCheck: the agent answers {"ok":true} while
    // signed out or unpaired, and in that state it serves only the pairing
    // routes. Promoting such a box to "active" would put an unusable machine
    // in the billing set and paint it "online" in the app.
    let healthy = false;
    for (const proto of ["https", "http"]) {
      try {
        const resp = await fetch(`${proto}://${target}:18080/health`, {
          signal: AbortSignal.timeout(10_000),
        });
        if (resp.ok) {
          const data = (await resp.json()) as {
            ok?: boolean;
            needsAuth?: boolean;
            authExpired?: boolean;
            lifecycle?: { usable?: boolean };
          };
          if (
            data.ok &&
            data.lifecycle?.usable !== false &&
            data.needsAuth !== true &&
            data.authExpired !== true
          ) {
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
    if (!machine) return;
    // A wake sits in "resuming" until THIS check promotes it to "active" —
    // "active" now means USABLE, not merely "the provider created a server".
    // "active" is still accepted so a box resumed by an older deployment (or
    // one already promoted) still gets its phase finished.
    if (machine.status !== "resuming" && machine.status !== "active") return;
    if (machine.provisionPhase === "ready") return;

    // Probe the hostname when there is one, else the raw IP. A seeded/parked
    // row can legitimately have no hostname (seedParkedMachine never assigns
    // one), and bailing out on that was silent death: the row then sat at
    // "registering"/85 forever with a live server billing behind it.
    const target = machine.hostname || machine.serverIp;
    if (!target) return;

    // Reachable is NOT the same as usable. The agent answers /health with
    // {"ok":true} even when it is signed out ("yaver-auth-expired") or has
    // never been paired ("bootstrap") — in that state it serves nothing but
    // the pairing routes, so a box that "passes" this check would still be an
    // unusable box the user cannot send a single task to. Demand `usable`.
    let reachable = false;
    let usable = false;
    let lifecycleState = "";
    for (const proto of ["https", "http"]) {
      try {
        const resp = await fetch(`${proto}://${target}:18080/health`, {
          signal: AbortSignal.timeout(10_000),
        });
        if (!resp.ok) continue;
        const data = (await resp.json()) as {
          ok?: boolean;
          needsAuth?: boolean;
          authExpired?: boolean;
          lifecycle?: { state?: string; usable?: boolean };
        };
        if (!data.ok) continue;
        reachable = true;
        lifecycleState = String(data.lifecycle?.state ?? "");
        usable =
          data.lifecycle?.usable !== false &&
          data.needsAuth !== true &&
          data.authExpired !== true;
        break;
      } catch {
        /* try next protocol / retry */
      }
    }

    if (reachable && usable) {
      await ctx.runMutation(internal.cloudMachines.setStatus, { machineId, status: "active" });
      // Close the run clock. The measured duration becomes the ETA every
      // surface shows on the NEXT wake — this box's own disk and region,
      // rather than a constant that is wrong for every box but one.
      {
        const startedAt = machine.wakeStartedAt ?? machine.lastWokeAt ?? null;
        const now = Date.now();
        await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
          machineId,
          wakeCompletedAt: now,
          lastWakeOutcome: "ready",
          ...(startedAt ? { lastWakeDurationMs: Math.max(0, now - startedAt) } : {}),
        });
      }
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: machine.runnersAuthorized === false ? "authorizing-runners" : "ready",
        progress: machine.runnersAuthorized === false ? 90 : 100,
      });
      console.log(`[cloudMachines.resumeHealthCheck] usable: ${target}`);
      return;
    }

    // Answering but signed out needs USER action, not another blind wake retry.
    // Keep the server alive for a bounded recovery window so mobile can hit
    // /auth/recover on the live agent. If the user does nothing, abandonWake
    // parks it again to stop the meter.
    const signedOut =
      reachable && (lifecycleState === "yaver-auth-expired" || lifecycleState === "bootstrap");
    if (signedOut && attempt < 40) {
      const reason =
        lifecycleState === "bootstrap"
          ? "The box is awake but its Yaver agent is waiting to be claimed. Sign this machine in from your phone to finish wake."
          : "The box is awake but its Yaver agent session expired. Sign this machine in from your phone to finish wake.";
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId,
        phase: "awaiting-yaver-auth",
        progress: 88,
        error: reason,
      });
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId,
        status: "resuming",
        errorMessage: reason,
      });
      // Outcome, not duration: the run has not ended, it has stopped making
      // progress. Surfaces use this to stop pretending a bar is moving.
      await ctx.runMutation(internal.cloudMachines.setLifecycleTiming, {
        machineId, lastWakeOutcome: "needs-auth",
      });
      await ctx.scheduler.runAfter(15_000, internal.cloudMachines.resumeHealthCheck, {
        machineId,
        attempt: attempt + 1,
      });
      return;
    }
    // Not reachable yet: ask the PROVIDER what it sees. This is the only
    // signal that exists in the create→agent-answers window, and it is the
    // difference between "Hetzner is still initializing the server" and "the
    // server has been running for six minutes and the agent never came up" —
    // two situations that looked identical (a spinner) before.
    if (!reachable) {
      await ctx.runAction(internal.cloudLifecycle.probeProviderStatus, { machineId });
    }

    // The agent ANSWERED but isn't usable yet and isn't signed out — it is
    // mid-startup, dialing the relay. That is a real, observed signal, not a
    // timer, so it is the one honest moment to claim "registering". Before
    // this the box is still booting and the ladder must say so.
    if (reachable && machine.provisionPhase !== "registering") {
      await ctx.runMutation(internal.cloudMachines.setPhase, {
        machineId, phase: "registering", progress: 72,
      });
    }
    // How long a box that has NOT answered yet is given before we delete it.
    // The old cap was `attempt >= 10` — with the first check at +20s and 15s
    // between ticks that is ~2.6 minutes, which is SHORTER than this codebase's
    // own documented boot times (a vanilla first boot can take several minutes;
    // legacy snapshot restore is longer still). So a perfectly
    // healthy slow wake was deleted mid-boot and reported to the user as "never
    // became reachable" — and because abandonWake re-parks, the next attempt
    // hit the same wall. Scale the budget to what this box actually has to do.
    const bootBudgetAttempts =
      machine.bootImageSource === "vanilla" ? 40 : machine.volumeId ? 20 : 32; // ~10 / ~5 / ~8 min
    if (signedOut || attempt >= bootBudgetAttempts) {
      const reason = signedOut
        ? "The box stayed awake waiting for Yaver sign-in, but was not authorized in time. Parked again to stop the meter — sign it in, then wake."
        : "The box never became reachable after waking. Parked again to stop the meter — try waking it again.";
      console.log(`[cloudMachines.resumeHealthCheck] abandoning ${target}: ${reason}`);
      await ctx.scheduler.runAfter(0, internal.cloudLifecycle.abandonWake, { machineId, reason });
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
// has no machine in a healthy/in-flight/wakeable state. Idempotent:
// ensureForSubscription returns the existing row if one is reusable, so a
// healthy or parked box is never duplicated.
// project_managed_cloud_onboarding_gap (recovery).
const HEALTHY_OR_INFLIGHT = new Set([
  "provisioning", "active", "grace", "stopping", "suspended",
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
      // box → recovery: re-provision. Flat Cloud Workspace repairs to the
      // cost-friendly standard profile; explicit legacy GPU plans keep GPU.
      const machineType = s.plan.includes("gpu") ? "gpu" : "standard";
      await ctx.runMutation(internal.cloudMachines.ensureForSubscription, {
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
/**
 * setAutoPark — customer-facing auto-close configuration.
 *
 * Default is ON (the field is undefined === enabled), so a forgotten box still
 * stops its own meter. Disabling is rejected at this lower mutation boundary as
 * well as the HTTP validator; legacy OFF rows can only be turned back on.
 */
/** Record the persistent data volume attached to a machine. */
export const setVolume = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    volumeId: v.string(),
    volumeSizeGb: v.optional(v.number()),
  },
  handler: async (ctx, { machineId, volumeId, volumeSizeGb }) => {
    const patch: Record<string, unknown> = { volumeId, updatedAt: Date.now() };
    if (typeof volumeSizeGb === "number") patch.volumeSizeGb = volumeSizeGb;
    await ctx.db.patch(machineId, patch);
    return { ok: true, volumeId };
  },
});

/** Clear all cloud-resource pointers after a full purge (retire/reset). */
export const clearResources = internalMutation({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    await ctx.db.patch(machineId, {
      status: "removed",
      hetznerServerId: undefined,
      serverIp: undefined,
      volumeId: undefined,
      volumeSizeGb: undefined,
      baseImageId: undefined,
      egressIpId: undefined,
      egressIpAddress: undefined,
      egressIpScope: undefined,
      egressIpReservedAt: undefined,
      updatedAt: Date.now(),
    });
    return { ok: true };
  },
});

/**
 * Every provider-native resource id Convex believes it owns, across all rows.
 * This is the "expected" side of provider→Convex reconciliation; anything the
 * provider holds that is NOT in here is an orphan.
 *
 * Deliberately unscoped by user: an orphan has no owner by definition, so the
 * sweep must see the whole fleet. It returns opaque provider ids only — no
 * paths, no secrets, no customer data.
 */
export const listKnownProviderResourceIds = internalQuery({
  args: {},
  handler: async (ctx) => {
    const ids = new Set<string>();
    for (const m of await ctx.db.query("cloudMachines").collect()) {
      for (const id of [
        m.hetznerServerId,
        m.cloudResourceId,
        m.volumeId,
        m.baseImageId,
        m.lastSnapshotId,
        m.egressIpId,
      ]) {
        if (id) ids.add(String(id));
      }
    }
    // Relay Pro boxes live on the SAME provider account but in a different
    // table. Omitting them would make every managed relay server and its grace
    // snapshot look like an orphan — a sweep that cries wolf gets ignored, and
    // an ignored sweep is worse than no sweep at all.
    for (const r of await ctx.db.query("managedRelays").collect()) {
      for (const id of [r.hetznerServerId, r.cloudResourceId, r.lastSnapshotId]) {
        if (id) ids.add(String(id));
      }
    }
    return Array.from(ids);
  },
});

/**
 * Stamp which IaaS this row actually landed on.
 *
 * `createCloudMachine` never wrote this field, so every managed row carried
 * `provider === undefined` and every reader defaulted it to "hetzner". That is
 * fine while Hetzner is the only placement — and becomes a silent, expensive
 * lie the moment it is not: `cloudLifecycle.pauseMachine` reads
 * `machine.provider` for telemetry but calls `hetznerDelete` unconditionally,
 * so a mis-stamped row would be marked "paused" while its real VM kept running.
 * Recording the truth at provision time is what makes that fixable.
 */
export const setProviderId = internalMutation({
  args: { machineId: v.id("cloudMachines"), provider: v.string() },
  handler: async (ctx, { machineId, provider }) => {
    await ctx.db.patch(machineId, { provider, updatedAt: Date.now() });
    return { ok: true };
  },
});

/** Record a newly reserved stable egress address for this workspace. */
export const setEgressIp = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    egressIpId: v.string(),
    egressIpAddress: v.string(),
    egressIpScope: v.string(),
  },
  handler: async (ctx, { machineId, egressIpId, egressIpAddress, egressIpScope }) => {
    await ctx.db.patch(machineId, {
      egressIpId,
      egressIpAddress,
      egressIpScope,
      egressIpReservedAt: Date.now(),
      updatedAt: Date.now(),
    });
    return { ok: true };
  },
});

/** Rows holding a reserved egress IP while parked — input to the release sweep. */
export const listParkedWithEgressIp = internalQuery({
  args: {},
  handler: async (ctx) => {
    const rows = await ctx.db.query("cloudMachines").collect();
    return rows
      .filter(
        (m) =>
          Boolean(m.egressIpId) &&
          (m.status === "paused" || m.status === "stopped" || m.status === "suspended"),
      )
      .map((m) => ({
        machineId: m._id,
        egressIpId: m.egressIpId as string,
        egressIpAddress: m.egressIpAddress,
        // lastParkedAt is the honest clock; fall back to the reservation time
        // so a row missing the park stamp still ages out instead of leaking.
        parkedAt: m.lastParkedAt ?? m.egressIpReservedAt ?? m.updatedAt ?? 0,
      }));
  },
});

/**
 * Drop the pointer to ONE detachable paid resource, and only after the
 * provider actually reclaimed it.
 *
 * Clearing a pointer we did NOT reclaim is how a resource becomes invisible
 * AND still billed: the row stops mentioning the volume/IP, so no retry, no
 * sweep, and no human ever looks for it again. So every caller must pass only
 * the resources whose delete returned success. reclaimAuxResources enforces
 * this by deriving the flags from its own per-resource outcomes.
 */
export const clearAuxPointers = internalMutation({
  args: {
    machineId: v.id("cloudMachines"),
    clearVolume: v.optional(v.boolean()),
    clearEgressIp: v.optional(v.boolean()),
    clearSnapshot: v.optional(v.boolean()),
  },
  handler: async (ctx, { machineId, clearVolume, clearEgressIp, clearSnapshot }) => {
    const patch: Record<string, unknown> = { updatedAt: Date.now() };
    if (clearVolume) {
      patch.volumeId = undefined;
      patch.volumeSizeGb = undefined;
    }
    if (clearEgressIp) {
      patch.egressIpId = undefined;
      patch.egressIpAddress = undefined;
      patch.egressIpScope = undefined;
      patch.egressIpReservedAt = undefined;
    }
    if (clearSnapshot) {
      patch.lastSnapshotId = undefined;
      patch.lastSnapshotAt = undefined;
      patch.snapshotSizeGb = undefined;
    }
    await ctx.db.patch(machineId, patch);
    return { ok: true };
  },
});

/** Drop the pointers to a provider server that no longer exists, WITHOUT
 *  touching the recovery snapshot (so the box stays wakeable). Deleting a
 *  server while leaving hetznerServerId/serverIp behind is how a row gets
 *  stuck: the next park tries to snapshot a server that is already gone, the
 *  snapshot 404s, and — because a failed snapshot aborts the delete — the box
 *  can never be parked again. */
export const clearServerRef = internalMutation({
  args: { machineId: v.id("cloudMachines") },
  handler: async (ctx, { machineId }) => {
    await ctx.db.patch(machineId, {
      hetznerServerId: undefined,
      cloudResourceId: undefined,
      serverIp: undefined,
      updatedAt: Date.now(),
    });
    return { ok: true };
  },
});

/** Record the SLIM boot image used to recreate the server on wake. */
export const setBaseImage = internalMutation({
  args: { machineId: v.id("cloudMachines"), baseImageId: v.string() },
  handler: async (ctx, { machineId, baseImageId }) => {
    await ctx.db.patch(machineId, { baseImageId, updatedAt: Date.now() });
    return { ok: true, baseImageId };
  },
});

export const setAutoPark = internalMutation({
  args: {
    userDocId: v.id("users"),
    machineId: v.id("cloudMachines"),
    enabled: v.boolean(),
    idleMinutes: v.optional(v.number()),
  },
  handler: async (ctx, args) => {
    const machine = await ctx.db.get(args.machineId);
    if (!machine) throw new Error("Machine not found");
    if (String(machine.userId) !== String(args.userDocId)) throw new Error("Not your machine");
    if (args.enabled === false) {
      throw new Error("Cloud Workspace auto-close is required to protect usage and compute costs");
    }

    const patch: Record<string, unknown> = {
      autoParkEnabled: true,
      updatedAt: Date.now(),
    };
    if (typeof args.idleMinutes === "number" && args.idleMinutes > 0) {
      patch.autoParkMinutes = args.idleMinutes;
    }
    await ctx.db.patch(args.machineId, patch);
    return {
      ok: true,
      autoParkEnabled: true,
      autoParkMinutes: (patch.autoParkMinutes as number | undefined) ?? machine.autoParkMinutes ?? 45,
    };
  },
});

// internalMutation: rewrites status/serverIp/hetznerServerId for a machine.
// Public exposure would let anyone mark a live billing box "stopped" or
// corrupt the teardown pointer (hetznerServerId) that destroy() relies on.
export const updateStatus = internalMutation({
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

/** Stop and deprovision a machine. force=true skips the hosted grace.
 *  internalMutation: this cancels the subscription AND schedules the real
 *  Hetzner destroy — the single most destructive resource op. Ownership is
 *  enforced by the calling HTTP routes (per-machine userId === session), so
 *  this must never be publicly callable with only a machineId. */
export const deprovision = internalMutation({
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
    // Hoisted out of the server block: the aux-resource reclaim below has to
    // know which snapshot is the user's freshly-taken data copy so it deletes
    // the STALE park snapshot and never the one we just made to protect them.
    let preservedSnapshotId = "";
    if (machine.hetznerServerId) {
      // Pre-delete snapshot. For a hosted box this is the user's ONLY
      // data copy → a failure ABORTS the delete (status:error, box
      // kept). For byok it's disposable → best-effort, continue.
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
            if (sj.image?.id) preservedSnapshotId = String(sj.image.id);
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
      if (preservedSnapshotId) {
        await ctx.runMutation(internal.cloudMachines.setStatus, {
          machineId,
          status: machine.status ?? "stopping",
          lastSnapshotId: preservedSnapshotId,
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

    // ─── Reclaim the satellites the server delete does NOT stop ───────────
    // A deleted server does not stop the volume, the reserved egress IP, or a
    // stale snapshot — they are designed to outlive it (that is how park keeps
    // state cheap). Until 2026-07-21 this path stopped at the server, so every
    // customer decommission left its volume billing forever, and no sweep
    // existed that could ever have found it. `preExistingSnapshotId` is the
    // pre-delete copy taken above for hosted/opt-in boxes: that one is the
    // user's data and is deliberately preserved.
    const auxReclaim = await ctx.runAction(internal.cloudLifecycle.reclaimAuxResources, {
      machineId,
      deleteSnapshot: true,
      keepSnapshotId: preservedSnapshotId || undefined,
    });
    if (!auxReclaim.ok) {
      // The server IS gone (the expensive part stopped), but something smaller
      // is still on the meter. Never report "stopped" — that word means "costs
      // you nothing now". Name the exact resource and its id so the remedy is
      // an action, not a scavenger hunt. destroy is idempotent (server delete
      // tolerates 404), so Decommission again is a safe, real retry.
      await ctx.runMutation(internal.cloudMachines.setStatus, {
        machineId,
        status: "error",
        errorMessage:
          `${warn}Server deleted, but these resources are STILL BILLING: ${auxReclaim.leaked.join("; ")}. Retry Decommission to reclaim them.`,
      });
      return;
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
