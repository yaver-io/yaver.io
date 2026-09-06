/**
 * Desktop task parity contract — `npx tsx lib/desktopTaskParity.test.ts`.
 *
 * Electron renders this dashboard, so a method existing only on AgentClient
 * is not a shipped GUI feature. Pin the consumer controls and the structured
 * notification bridge that make task lifecycle visible in the native shell.
 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const webRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const repoRoot = join(webRoot, "..");
const page = readFileSync(join(webRoot, "app/dashboard/page.tsx"), "utf8");
const machineRoles = readFileSync(join(webRoot, "components/dashboard/MachineRolesCard.tsx"), "utf8");
const preload = readFileSync(join(repoRoot, "electron/src/preload.js"), "utf8");
const runtimeLab = readFileSync(join(webRoot, "components/dashboard/RuntimeLabView.tsx"), "utf8");
const devicesView = readFileSync(join(webRoot, "components/dashboard/DevicesView.tsx"), "utf8");

test("desktop dashboard consumes task history and supports task selection", () => {
  assert.match(page, /\{tasks\.length\}/);
  assert.match(page, /tasks\.map\(\(task\) =>/);
  assert.match(page, /onClick=\{\(\) => selectTask\(task\)\}/);
  assert.match(page, /live \? "ongoing" : task\.status/);
});

test("ongoing and terminal tasks expose their real agent operations", () => {
  assert.match(page, /await taskClientFor\(task\)\.stopTask\(task\.id\)/);
  assert.match(page, /await taskClientFor\(task\)\.deleteTask\(task\.id\)/);
  assert.match(page, /window\.confirm\(/, "delete must stay an explicit user action");
});

test("terminal task events cross the explicit Electron bridge", () => {
  assert.match(page, /bridge\?\.taskStatus\?\.\(/);
  assert.match(preload, /ipcRenderer\.send\("yaver:task-status"/);
});

test("desktop GUI exposes independent local-or-remote runner and renderer placement", () => {
  assert.match(page, /<MachineRolesCard/);
  assert.match(machineRoles, /Default AI runner/);
  assert.match(machineRoles, /Default renderer \/ build machine/);
  assert.match(machineRoles, /Secondary AI runner/);
  assert.match(machineRoles, /Secondary renderer \/ build machine/);
  assert.match(machineRoles, /runnerDeviceId: runnerId/);
  assert.match(machineRoles, /renderDeviceId: renderId \|\| runnerId/);
});

test("desktop shell identifies its exact local device as This PC on every placement surface", () => {
  assert.match(preload, /surface: "desktop-gui"/);
  assert.match(page, /getDesktopStatus/);
  assert.match(page, /Desktop GUI/);
  assert.match(devicesView, /This PC · Desktop GUI/);
  assert.match(runtimeLab, /desktopDeviceLabel/);
  assert.match(machineRoles, /desktopDeviceLabel/);
});
