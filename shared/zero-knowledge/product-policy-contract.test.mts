import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const read = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

test("Convex publishes the fail-closed product policy", () => {
  const http = read("../../backend/convex/http.ts");
  assert.match(http, /defaultRunner: config\.default_runner \|\| "opencode"/);
  assert.match(http, /primaryCloudProvider: config\.primary_cloud_provider \|\| "hetzner"/);
  assert.match(http, /remoteRunnerOAuthEnabled: config\.remote_runner_oauth_enabled === "true"/);
  assert.match(http, /deviceRoles: \["master", "worker"\]/);
});

test("agent exposes no remote runner OAuth transport while task and tmux adapters remain generic", () => {
  const policy = read("../../desktop/agent/runner_oauth_policy.go");
  const server = read("../../desktop/agent/httpserver.go");
  const tools = read("../../desktop/agent/mcp_tools.go");
  const ops = read("../../desktop/agent/ops_runner_auth.go");
  const tasks = read("../../desktop/agent/tasks.go");
  const code = read("../../desktop/agent/code_cmd.go");
  assert.match(policy, /func remoteRunnerOAuthEnabled\(\) bool \{\s*return false/);
  assert.doesNotMatch(server, /HandleFunc\("\/runner-auth\/browser/);
  assert.doesNotMatch(server, /HandleFunc\("\/runner-auth\/credentials\/import/);
  assert.doesNotMatch(server, /HandleFunc\("\/runner\/auth\/mirror/);
  assert.doesNotMatch(tools, /\{"name": "runner_auth_browser_/);
  assert.doesNotMatch(tools, /\{"name": "runner_auth_credentials_import"/);
  assert.match(ops, /Intentionally not registered/);
  assert.match(ops, /Code: "removed"/);
  assert.match(tasks, /knownRunnerIDs := supportedRunnerIDs/);
  assert.match(tasks, /RunnerID:\s+"opencode"/);
  assert.match(tasks, /supportedRunnerIDs = \[\]string\{"opencode", "claude", "codex", "remoteless"\}/);
  assert.match(code, /firstNonEmpty\(strings\.TrimSpace\(\*runner\), "opencode"\)/);
});

test("all independent surfaces suppress the remote OAuth interface", () => {
  assert.match(read("../../mobile/src/components/RunnerAuthModal.tsx"), /if \(!isRemoteRunnerOAuthEnabled\(\)\) return null/);
  assert.match(read("../../web/app/dashboard/page.tsx"), /chatRunnerAuthModal && isRemoteRunnerOAuthEnabled\(\)/);
  assert.match(read("../../web/components/dashboard/ToolsView.tsx"), /isRemoteRunnerOAuthEnabled\(\) \? <section>/);
  assert.doesNotMatch(read("../../tvos/YaverTV/Views/RuntimeDashboardView.swift"), /Task \{ await startRunnerAuth\("(?:claude|codex)"\) \}/);
});

test("device roles and detailed operating-system inventory stay Convex-backed", () => {
  const schema = read("../../backend/convex/schema.ts");
  const devices = read("../../backend/convex/devices.ts");
  assert.match(schema, /primaryDeviceId: v\.optional\(v\.string\(\)\)/);
  assert.match(schema, /workerDeviceIds: v\.optional\(v\.array\(v\.string\(\)\)\)/);
  assert.match(schema, /osVersion: v\.optional\(v\.string\(\)\)/);
  assert.match(devices, /hardwareProfile: args\.hardwareProfile/);
});
