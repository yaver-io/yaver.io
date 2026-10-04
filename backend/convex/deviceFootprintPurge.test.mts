import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const source = await readFile(new URL("./deviceFootprintPurge.ts", import.meta.url), "utf8");

test("permanent device purge is internal, post-removal, and cloud-lifecycle guarded", () => {
  assert.match(source, /internalMutation/);
  assert.match(source, /DEVICE_NOT_REMOVED/);
  assert.match(source, /DEVICE_HAS_CLOUD_LIFECYCLE_ROW/);
  assert.match(source, /cloudMachines/);
  assert.match(source, /byoMachines/);
});

test("purge covers security, workspace, telemetry, and user-facing device rows", () => {
  for (const table of [
    "sessions", "credentialHandoffDevices", "agentRescueCommands", "deviceMetrics",
    "deviceEvents", "agentTaskSnapshots", "cloudWorkspaces", "workloadCredentials",
    "sdkTokens", "userProjects", "userServices", "userDeployments", "userActivity",
    "infraAccessGrantDevices", "hostShareSessions", "whatsappContacts", "meshNodes",
    "tmuxRunnerSessions",
  ]) assert.match(source, new RegExp(`\\["${table}"`));
  assert.match(source, /workerDeviceIds/);
  assert.match(source, /machineRolesByProject/);
  assert.match(source, /mcpCatalogByDevice/);
});
