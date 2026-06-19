import test from "node:test";
import assert from "node:assert/strict";

import {
  buildManagedCloudInit,
  buildManagedCloudInitContainer,
  planDeprovision,
  snapshotIsMandatory,
  HOSTED_GRACE_MS,
} from "./cloudMachines.js";

test("buildManagedCloudInit writes managed agent config and service", () => {
  const cloudInit = buildManagedCloudInit({
    convexSite: "https://example.convex.site",
    machineId: "machine_12345678",
    machineToken: "machine-token-abc",
    userSessionToken: "session-token-xyz",
    deviceId: "cloud-12345678",
    hostname: "12345678.cloud.yaver.io",
    yaverArch: "amd64",
    yaverReleaseUrl: "https://example.invalid/yaver-linux-amd64",
    repoUrl: "https://github.com/example/repo.git",
    gpu: false,
  });

  assert.match(cloudInit, /cat > \/home\/yaver\/\.yaver\/config\.json/);
  assert.match(cloudInit, /"auth_token": "session-token-xyz"/);
  assert.match(cloudInit, /"convex_site_url": "https:\/\/example\.convex\.site"/);
  assert.match(cloudInit, /"device_id": "cloud-12345678"/);
  assert.match(cloudInit, /"public_endpoints": \["https:\/\/12345678\.cloud\.yaver\.io"\]/);
  assert.match(cloudInit, /cat > \/home\/yaver\/\.config\/opencode\/opencode\.json/);
  assert.match(cloudInit, /"model": "zai-coding-plan\/glm-4\.7"/);
  assert.match(cloudInit, /"enabled_providers": \[\n\s+"zai-coding-plan"\n\s+\]/);
  assert.match(cloudInit, /"default_agent": "build"/);
  assert.match(cloudInit, /"command": \[\n\s+"\/usr\/local\/bin\/yaver",\n\s+"mcp"\n\s+\]/);
  assert.match(cloudInit, /cat > \/etc\/systemd\/system\/yaver-agent\.service/);
  assert.match(cloudInit, /ExecStart=\/usr\/local\/bin\/yaver serve --debug --port 18080/);
  assert.match(cloudInit, /systemctl enable --now yaver-agent/);
  assert.match(cloudInit, /cat > \/usr\/local\/bin\/yaver-bootstrap-workspace/);
  assert.match(cloudInit, /clone_one https:\/\/github\.com\/kivanccakmak\/yaver\.io\.git yaver\.io/);
  assert.match(cloudInit, /clone_one https:\/\/github\.com\/kivanccakmak\/talos\.git talos/);
  assert.match(cloudInit, /clone_one 'https:\/\/github\.com\/example\/repo\.git' starter/);
  assert.match(cloudInit, /command -v claude >\/dev\/null 2>&1 \|\| missing_pkgs="\$missing_pkgs @anthropic-ai\/claude-code"/);
  assert.match(cloudInit, /command -v codex >\/dev\/null 2>&1 \|\| missing_pkgs="\$missing_pkgs @openai\/codex"/);
  assert.match(cloudInit, /command -v opencode >\/dev\/null 2>&1 \|\| missing_pkgs="\$missing_pkgs opencode-ai"/);
  assert.match(cloudInit, /npm install -g \$missing_pkgs/);
});

test("buildManagedCloudInit fetches the yaver agent as a .tar.gz and extracts it", () => {
  // Regression guard: release-cli.yml ships the agent INSIDE
  // yaver-linux-<arch>.tar.gz (one file named `yaver`), never as a raw
  // asset. A raw `curl -o /usr/local/bin/yaver` 404s and leaves the box
  // with no agent. Do not "simplify" this back to a raw curl.
  const cloudInit = buildManagedCloudInit({
    convexSite: "https://example.convex.site",
    machineId: "machine_tar",
    machineToken: "machine-token-tar",
    userSessionToken: "session-token-tar",
    deviceId: "cloud-tar",
    hostname: "tar.cloud.yaver.io",
    yaverArch: "amd64",
    yaverReleaseUrl:
      "https://github.com/kivanccakmak/yaver.io/releases/latest/download/yaver-linux-amd64.tar.gz",
    gpu: false,
  });

  assert.match(cloudInit, /yaver-linux-amd64\.tar\.gz/);
  assert.match(cloudInit, /tar -xzf \/tmp\/yaver\.tgz -C \/usr\/local\/bin yaver/);
  assert.doesNotMatch(
    cloudInit,
    /curl -fsSL "[^"]+" -o \/usr\/local\/bin\/yaver\b/,
  );
});

test("buildManagedCloudInit includes GPU bootstrap only for GPU machines", () => {
  const cloudInit = buildManagedCloudInit({
    convexSite: "https://example.convex.site",
    machineId: "machine_gpu",
    machineToken: "machine-token-gpu",
    userSessionToken: "session-token-gpu",
    deviceId: "cloud-gpu",
    hostname: "gpu.cloud.yaver.io",
    yaverArch: "amd64",
    yaverReleaseUrl: "https://example.invalid/yaver-linux-amd64",
    gpu: true,
  });

  assert.match(cloudInit, /nvidia-driver-550/);
  assert.match(cloudInit, /ollama\.com\/install\.sh/);
});

test("buildManagedCloudInit runs self-hosted Convex only for the hosted tier", () => {
  const base = {
    convexSite: "https://example.convex.site",
    machineId: "machine_tier",
    machineToken: "machine-token-tier",
    userSessionToken: "session-token-tier",
    deviceId: "cloud-tier",
    hostname: "tier.cloud.yaver.io",
    yaverArch: "amd64" as const,
    yaverReleaseUrl: "https://example.invalid/yaver-linux-amd64.tar.gz",
    gpu: false,
  };

  // byok (default / absent) — byte-identical: no Convex container.
  const byok = buildManagedCloudInit(base);
  assert.doesNotMatch(byok, /ghcr\.io\/get-convex\/convex-backend/);
  assert.doesNotMatch(byok, /\/etc\/yaver\/convex-selfhosted\.json/);
  assert.equal(byok, buildManagedCloudInit({ ...base, tier: "byok" }));

  // hosted — Convex container + admin-key capture, origins point at
  // the box's own public hostname (no Convex Cloud).
  const hosted = buildManagedCloudInit({ ...base, tier: "hosted" });
  assert.match(hosted, /docker run -d --name yaver-convex/);
  assert.match(hosted, /ghcr\.io\/get-convex\/convex-backend:latest/);
  assert.match(hosted, /-v yaver-convex-data:\/convex\/data/);
  assert.match(hosted, /generate_admin_key\.sh/);
  assert.match(hosted, /chmod 0600 \/etc\/yaver\/convex-selfhosted\.json/);
  assert.match(hosted, /https:\/\/tier\.cloud\.yaver\.io\/_convex-api/);

  // The nginx Convex reverse-proxy is always emitted (internal-only
  // upstreams), so a byok box's config is unchanged but valid.
  for (const ci of [byok, hosted]) {
    assert.match(ci, /location \/_convex-api\/ \{/);
    assert.match(ci, /proxy_pass http:\/\/127\.0\.0\.1:3210\//);
    assert.match(ci, /location \/_convex-http\/ \{/);
  }
});

test("planDeprovision: hosted gets a grace window, byok deletes now, force overrides", () => {
  const now = 1_000_000;

  const hosted = planDeprovision("hosted", false, now);
  assert.equal(hosted.grace, true);
  assert.equal(hosted.deprovisionAt, now + HOSTED_GRACE_MS);
  assert.equal(HOSTED_GRACE_MS, 7 * 24 * 60 * 60 * 1000);

  // byok is disposable — immediate delete, no grace.
  for (const tier of ["byok", undefined]) {
    const p = planDeprovision(tier as string | undefined, false, now);
    assert.equal(p.grace, false);
    assert.equal(p.deprovisionAt, undefined);
  }

  // Explicit "delete now" (force) bypasses the hosted grace.
  const forced = planDeprovision("hosted", true, now);
  assert.equal(forced.grace, false);
});

test("snapshotIsMandatory: only hosted (the user's only data copy)", () => {
  assert.equal(snapshotIsMandatory("hosted"), true);
  assert.equal(snapshotIsMandatory("byok"), false);
  assert.equal(snapshotIsMandatory(undefined), false);
});

test("buildManagedCloudInitContainer: byok runs only the agent; hosted adds self-hosted Convex", () => {
  const base = {
    convexSite: "https://example.convex.site",
    machineId: "machine_ctr",
    machineToken: "machine-token-ctr",
    userSessionToken: "session-token-ctr",
    deviceId: "cloud-ctr",
    hostname: "ctr.cloud.yaver.io",
    yaverArch: "amd64" as const,
    yaverReleaseUrl: "https://example.invalid/yaver-linux-amd64.tar.gz",
    gpu: false,
  };
  const IMG = "ghcr.io/kivanccakmak/yaver-cloud:latest";

  // byok — agent container only, NO Convex sidecar; snippet truncated.
  const byok = buildManagedCloudInitContainer({ ...base, tier: "byok" }, IMG);
  assert.match(byok, /docker run -d --name yaver --restart always/);
  assert.match(byok, /docker pull '.*yaver-cloud:latest'/);
  assert.match(byok, /CONVEX_SELFHOSTED_FILE=\/root\/\.yaver\/convex-selfhosted\.json/);
  assert.match(byok, /cat > \/usr\/local\/bin\/yaver-bootstrap-workspace/);
  assert.match(byok, /cat > \/srv\/yaver\/state\/\.config\/opencode\/opencode\.json/);
  assert.match(byok, /"model": "zai-coding-plan\/glm-4\.7"/);
  assert.match(byok, /"enabled_providers": \[\n\s+"zai-coding-plan"\n\s+\]/);
  assert.match(byok, /"default_agent": "build"/);
  assert.match(byok, /"command": \[\n\s+"\/usr\/local\/bin\/yaver",\n\s+"mcp"\n\s+\]/);
  assert.match(byok, /clone_one https:\/\/github\.com\/kivanccakmak\/yaver\.io\.git yaver\.io/);
  assert.match(byok, /clone_one https:\/\/github\.com\/kivanccakmak\/talos\.git talos/);
  assert.match(byok, /-v \/srv\/yaver\/state\/Workspace:\/srv\/yaver\/workspace/);
  assert.doesNotMatch(byok, /ghcr\.io\/get-convex\/convex-backend/);
  assert.doesNotMatch(byok, /docker run -d --name yaver-convex/);
  assert.match(byok, /: > \/etc\/nginx\/snippets\/yaver-convex\.conf/);
  // tier absent ⇒ identical to explicit byok (byte-identical default).
  assert.equal(byok, buildManagedCloudInitContainer(base, IMG));

  // hosted — agent container + Convex sidecar + admin-key on the
  // PERSISTED volume (so the in-container agent reads it) + nginx WS.
  const hosted = buildManagedCloudInitContainer({ ...base, tier: "hosted" }, IMG);
  assert.match(hosted, /docker run -d --name yaver-convex --restart always/);
  assert.match(hosted, /ghcr\.io\/get-convex\/convex-backend:latest/);
  assert.match(hosted, /-v yaver-convex-data:\/convex\/data/);
  assert.match(hosted, /generate_admin_key\.sh/);
  assert.match(
    hosted,
    /cat > \/srv\/yaver\/state\/\.yaver\/convex-selfhosted\.json/,
  );
  assert.match(hosted, /chmod 0600 \/srv\/yaver\/state\/\.yaver\/convex-selfhosted\.json/);
  assert.match(hosted, /https:\/\/ctr\.cloud\.yaver\.io\/_convex-api/);
  assert.match(hosted, /location \/_convex-api\/ \{/);
  assert.match(hosted, /proxy_pass http:\/\/127\.0\.0\.1:3210\//);
  assert.match(hosted, /location \/_convex-http\/ \{/);
  assert.match(hosted, /include \/etc\/nginx\/snippets\/yaver-convex\.conf;/);
});

test("buildManagedCloudInitContainer: observability beacon + optional ssh/relay", () => {
  const base = {
    convexSite: "https://example.convex.site",
    machineId: "machine_obs",
    machineToken: "machine-token-obs",
    userSessionToken: "session-token-obs",
    deviceId: "cloud-obs",
    hostname: "obs.cloud.yaver.io",
    yaverArch: "amd64" as const,
    yaverReleaseUrl: "https://example.invalid/yaver-linux-amd64.tar.gz",
    gpu: false,
  };
  const IMG = "ghcr.io/kivanccakmak/yaver-cloud:latest";

  // Default (no ssh key, no relay password): the health beacon is
  // ALWAYS emitted, but ssh_authorized_keys / relay_password are NOT —
  // and the absent-tier default stays byte-identical to explicit byok.
  const plain = buildManagedCloudInitContainer(base, IMG);
  assert.match(plain, /phase=registering/);
  assert.match(plain, /phase=error&error=agent-health-unreachable-300s/);
  assert.match(plain, /curl -fsS -m 4 http:\/\/127\.0\.0\.1:18080\/health/);
  assert.match(plain, /phase=starting-agent/);
  assert.doesNotMatch(plain, /ssh_authorized_keys:/);
  assert.doesNotMatch(plain, /"relay_password":/);
  assert.equal(plain, buildManagedCloudInitContainer({ ...base, tier: "byok" }, IMG));

  // Operator debug key → top-level cloud-config ssh_authorized_keys
  // (JSON-quoted ⇒ YAML-safe), still byte-stable for the same inputs.
  const withSsh = buildManagedCloudInitContainer(
    { ...base, sshAuthorizedKey: "ssh-ed25519 AAAAC3NzaC1 operator@debug" },
    IMG,
  );
  assert.match(withSsh, /#cloud-config\nssh_authorized_keys:\n  - "ssh-ed25519 AAAAC3NzaC1 operator@debug"/);
  assert.equal(
    withSsh,
    buildManagedCloudInitContainer(
      { ...base, sshAuthorizedKey: "ssh-ed25519 AAAAC3NzaC1 operator@debug" },
      IMG,
    ),
  );

  // Relay password → config.json relay_password (defensive fallback now
  // that auto-cert + public_endpoints is the primary path).
  const withRelay = buildManagedCloudInitContainer(
    { ...base, relayPassword: "s3cr3t-relay-pw" },
    IMG,
  );
  assert.match(withRelay, /"relay_password": "s3cr3t-relay-pw"/);
  // config.json must stay valid JSON with the extra field (no dangling
  // comma): public_endpoints (always-present) then comma then relay_password.
  assert.match(
    withRelay,
    /"public_endpoints": \["https:\/\/obs\.cloud\.yaver\.io"\],\n {6}"relay_password": "s3cr3t-relay-pw"/,
  );
});

test("buildManagedCloudInitContainer: auto-cert the box's own subdomain (no shared relay needed)", () => {
  const base = {
    convexSite: "https://example.convex.site",
    machineId: "machine_cert",
    machineToken: "machine-token-cert",
    userSessionToken: "session-token-cert",
    deviceId: "cloud-cert",
    hostname: "cert42.cloud.yaver.io",
    yaverArch: "amd64" as const,
    yaverReleaseUrl: "https://example.invalid/yaver-linux-amd64.tar.gz",
    gpu: false,
  };
  const IMG = "ghcr.io/kivanccakmak/yaver-cloud:latest";
  const ci = buildManagedCloudInitContainer(base, IMG);

  // (1) config.json advertises HTTPS via public_endpoints so the agent
  // registers it in PublicEndpoints and the browser dashboard skips
  // the shared relay for this box's own traffic.
  assert.match(ci, /"public_endpoints": \["https:\/\/cert42\.cloud\.yaver\.io"\]/);

  // (2) machine.json carries hostname so the on-box reconciler can
  // process the auto domain (not just user custom domains).
  assert.match(
    ci,
    /\{"machineId":"machine_cert","machineToken":"machine-token-cert","convexSite":"https:\/\/example\.convex\.site","hostname":"cert42\.cloud\.yaver\.io"\}/,
  );

  // (3) Reconciler script reads HOST + ensures nginx server block +
  // certbot for the auto subdomain. Idempotent (cf existence guard,
  // certbot self-skips if not due, || true non-fatal). No
  // /machine/tls-issued POST for the auto domain (that's userDomains-only).
  assert.match(ci, /HOST=\$\(jq -r \.hostname "\$conf"/);
  assert.match(ci, /if \[ -n "\$HOST" \] && \[ "\$HOST" != "null" \]/);
  assert.match(ci, /certbot --nginx -d "\$HOST"/);
  // The auto-domain block is BEFORE the user-custom-domain loop.
  const reconcilerStart = ci.indexOf("yaver-tls-reconciler");
  const autoCertIdx = ci.indexOf('certbot --nginx -d "$HOST"', reconcilerStart);
  const customLoopIdx = ci.indexOf("pending-tls?machineId=", reconcilerStart);
  assert.ok(autoCertIdx > 0 && customLoopIdx > 0, "both blocks present");
  assert.ok(
    autoCertIdx < customLoopIdx,
    "auto-cert must run before the custom-domain loop",
  );

  // (4) byte-identical default still holds — every new field above is
  // tier-independent and unconditional, so byok == default still passes.
  assert.equal(ci, buildManagedCloudInitContainer({ ...base, tier: "byok" }, IMG));
});

test("buildManagedCloudInitContainer: Phase 2 — bundled yaver-relay (in-image, not sidecar) when boxRelayPassword set", () => {
  const base = {
    convexSite: "https://example.convex.site",
    machineId: "machine_relay",
    machineToken: "machine-token-relay",
    userSessionToken: "session-token-relay",
    deviceId: "cloud-relay",
    hostname: "relay42.cloud.yaver.io",
    yaverArch: "amd64" as const,
    yaverReleaseUrl: "https://example.invalid/yaver-linux-amd64.tar.gz",
    gpu: false,
  };
  const IMG = "ghcr.io/kivanccakmak/yaver-cloud:latest";

  // Absent boxRelayPassword ⇒ no relay ports on the main yaver run,
  // no RELAY_PASSWORD env, no ufw 4433/8443. The entrypoint wrapper
  // inside the image will see RELAY_PASSWORD unset and skip starting
  // yaver-relay (Dockerfile.yaver-cloud entrypoint).
  const plain = buildManagedCloudInitContainer(base, IMG);
  assert.doesNotMatch(plain, /-p 4433:4433\/udp/);
  assert.doesNotMatch(plain, /RELAY_PASSWORD/);
  assert.doesNotMatch(plain, /ufw allow 4433\/udp/);
  assert.doesNotMatch(plain, /ufw allow 8443\/tcp/);
  // tier absent ⇒ identical to explicit byok (byte-identical default).
  assert.equal(plain, buildManagedCloudInitContainer({ ...base, tier: "byok" }, IMG));
  // And critically NO separate yaver-relay container is launched —
  // it's bundled INTO the yaver image (Phase 2 design call).
  assert.doesNotMatch(plain, /--name yaver-relay\b/);

  // Set ⇒ ports + env are folded into the SAME yaver docker run (the
  // image's entrypoint wrapper backgrounds yaver-relay when it sees
  // RELAY_PASSWORD). ufw opens QUIC + admin ports BEFORE ufw enable.
  const withRelay = buildManagedCloudInitContainer(
    { ...base, boxRelayPassword: "relay-pw-abc123" },
    IMG,
  );
  assert.match(withRelay, /ufw allow 4433\/udp/);
  assert.match(withRelay, /ufw allow 8443\/tcp/);
  // Single docker run with both port pairs (the agent + the bundled relay).
  assert.match(withRelay, /docker run -d --name yaver --restart always/);
  assert.match(withRelay, /-p 18080:18080/);
  assert.match(withRelay, /-p 4433:4433\/udp -p 8443:8443\/tcp/);
  assert.match(withRelay, /-e RELAY_PASSWORD='relay-pw-abc123'/);
  // CONVEX_URL must also be passed when relay is bundled — the
  // entrypoint wrapper threads it as `--convex-url` so the relay
  // per-user-validates tunnel registrations instead of running in
  // password-only mode.
  assert.match(withRelay, /-e CONVEX_URL='https:\/\/example\.convex\.site'/);
  // No separate yaver-relay container — bundled.
  assert.doesNotMatch(withRelay, /--name yaver-relay\b/);

  // The ufw rules must land BEFORE `ufw --force enable` (otherwise the
  // ports never open).
  const ufwEnableIdx = withRelay.indexOf("ufw --force enable");
  const ufw4433Idx = withRelay.indexOf("ufw allow 4433/udp");
  assert.ok(ufw4433Idx > 0 && ufw4433Idx < ufwEnableIdx, "4433 rule before ufw enable");
});
