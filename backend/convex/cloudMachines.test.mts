import test from "node:test";
import assert from "node:assert/strict";

import {
  buildManagedCloudInit,
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

  assert.match(cloudInit, /cat > \/root\/\.yaver\/config\.json/);
  assert.match(cloudInit, /"auth_token": "session-token-xyz"/);
  assert.match(cloudInit, /"convex_site_url": "https:\/\/example\.convex\.site"/);
  assert.match(cloudInit, /"device_id": "cloud-12345678"/);
  assert.match(cloudInit, /cat > \/etc\/systemd\/system\/yaver-agent\.service/);
  assert.match(cloudInit, /ExecStart=\/usr\/local\/bin\/yaver serve --debug --port 18080/);
  assert.match(cloudInit, /systemctl enable --now yaver-agent/);
  assert.match(cloudInit, /git clone 'https:\/\/github\.com\/example\/repo\.git' \/srv\/yaver\/workspace/);
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
