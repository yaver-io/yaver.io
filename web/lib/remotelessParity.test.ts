import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const repo = join(dirname(fileURLToPath(import.meta.url)), "../..");
const read = (path: string) => readFileSync(join(repo, path), "utf8");
const contract = JSON.parse(read("docs/architecture/REMOTELESS_CAPABILITIES.json"));

test("canonical placement keeps remoteless last", () => {
  assert.deepEqual(contract.placementOrder, ["explicit-target", "primary", "secondary", "remoteless"]);
  const go = read("desktop/agent/capability_gap.go");
  assert.match(go, /appendRemotelessFallback/);
  assert.doesNotMatch(go, /preferRemotelessFirst/);
});

test("shared TS clients consume the same remoteless core", () => {
  for (const path of [
    "mobile/src/_core/remoteless.ts",
    "web/lib/_core/remoteless.ts",
    "sdk/feedback/react-native/src/_core/remoteless.ts",
  ]) {
    const source = read(path);
    assert.match(source, /resolveRemotelessPlacement/);
    assert.match(source, /Primary and secondary devices are unavailable/);
    assert.match(source, /"flutter-render"/);
    assert.match(source, /`remoteless\.\$\{capability\}/);
  }
});

test("dormant native fallback stays bounded while shipped UI gates it off", () => {
  const apple = `${read("tvos/YaverTV/Views/BoxlessCodeView.swift")}\n${read("tvos/YaverTV/Views/TasksView.swift")}`;
  assert.match(apple, /remoteless\.analysis-chat\.supported/);
  assert.match(apple, /remoteless\.code-edit\.unavailable/);
  assert.match(read("tvos/YaverTV/YaverStore.swift"), /boxlessUIEnabled\s*=\s*false/);
  // visionOS compiles the same BoxlessCodeView; sharing is the parity mechanism.
  assert.match(read("visionos/project.yml"), /tvos\/YaverTV\/Views\/BoxlessCodeView\.swift/);
  assert.match(read("visionos/YaverVision/Views/VisionDashboardView.swift"), /if store\.remotelessAllowed/);

  const androidTV = read("androidtv/app/src/main/kotlin/io/yaver/tv/ui/PlaceholderScreens.kt");
  assert.match(androidTV, /remoteless\.code-edit\.unavailable/);
  assert.doesNotMatch(androidTV, /Use boxless|analysis fallback/);
});

test("watch and car surfaces delegate execution instead of inventing a local runtime", () => {
  assert.match(read("watch/YaverWatch/PhoneSession.swift"), /watch never talks to the runner directly/i);
  assert.match(read("wear/app/src/main/kotlin/io/yaver/wear/PhoneBridge.kt"), /phone app/i);
  assert.match(read("mobile/app/car-voice-coding.tsx"), /roleRunnerId|primaryDeviceId/);
});
