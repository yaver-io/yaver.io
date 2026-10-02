import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const repo = join(dirname(fileURLToPath(import.meta.url)), "../..");
const read = (path: string) => readFileSync(join(repo, path), "utf8");

test("Convex owns one master/worker fleet policy", () => {
  const schema = read("backend/convex/schema.ts");
  for (const field of ["primaryDeviceId", "workerDeviceIds", "showWorkerDevices", "opportunisticFleet"]) {
    assert.match(schema, new RegExp(`${field}: v\\.`), `${field} must be persisted in userSettings`);
  }
  const mutation = read("backend/convex/userSettings.ts");
  assert.match(mutation, /normalizeOwnedDeviceIds/);
  assert.doesNotMatch(mutation, /filter\(\(id\) => id !== effectivePrimaryDeviceId\)/);
  assert.match(mutation, /patch\.workerDeviceIds = normalizedWorkerDeviceIds/);
});

test("task surfaces render structured fleet role metadata", () => {
  const contract = read("desktop/agent/tasks.go");
  assert.match(contract, /OrchestrationRole\s+string `json:"orchestrationRole,omitempty"`/);
  assert.match(contract, /OrchestrationStage\s+string `json:"orchestrationStage,omitempty"`/);
  assert.match(read("mobile/app/(tabs)/tasks.tsx"), /fleetMetadataForTask/);
  assert.match(read("web/components/dashboard/VibeCodingView.tsx"), /fleetTaskLabel/);
});

test("full device pickers default to master and disclose workers", () => {
  for (const path of [
    "mobile/app/(tabs)/devices.tsx",
    "mobile/src/components/RemoteBoxPickerModal.tsx",
    "web/components/dashboard/DevicesView.tsx",
    "tvos/YaverTV/Views/MachinePickerView.swift",
    "androidtv/app/src/main/kotlin/io/yaver/tv/ui/MachinePickerScreen.kt",
  ]) {
    const source = read(path);
    assert.match(source, /showWorkerDevices/iu, `${path} reads worker disclosure`);
    assert.match(source, /primaryDeviceId/iu, `${path} filters from the selected master`);
    assert.match(source, /Show worker/iu, `${path} offers progressive disclosure`);
    assert.match(source, /ready/iu, `${path} reports useful readiness, not only inventory`);
  }
});

test("mobile and web expose opportunistic fleet settings", () => {
  for (const path of ["mobile/app/(tabs)/devices.tsx", "web/components/dashboard/DevicesView.tsx"]) {
    const source = read(path);
    assert.match(source, /Automatic worker use/);
    assert.match(source, /opportunisticFleet/);
  }
});

test("thin surfaces inherit the authoritative full-surface routing", () => {
  // Electron hosts the web dashboard, visionOS compiles the tvOS views, and
  // watch/car companions delegate task execution to the phone. They must not
  // grow independent fleet-role copies that drift from the full pickers.
  assert.match(read("electron/src/auth-interceptor.js"), /dashboard/);
  assert.match(read("visionos/project.yml"), /tvos\/YaverTV\/Views/);
  assert.match(read("watch/YaverWatch/PhoneSession.swift"), /never talks to the runner directly/i);
  assert.match(read("wear/app/src/main/kotlin/io/yaver/wear/PhoneBridge.kt"), /phone app/i);
  assert.match(read("mobile/app/car-voice-coding.tsx"), /primaryDeviceId/);
});

test("MCP reports fleet roles and respects worker disclosure", () => {
  const server = read("desktop/agent/httpserver.go");
  assert.match(server, /Fleet: %d node\(s\) ready/);
  assert.match(server, /role=" \+ strings\.Join\(roles/);
  assert.match(server, /Show workers to list them/);
});
