import test from "node:test";
import assert from "node:assert/strict";

import { buildManagedCloudInit } from "./cloudMachines.js";

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
