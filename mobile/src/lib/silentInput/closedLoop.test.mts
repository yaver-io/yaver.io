import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const mobileRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const read = (path: string) => readFileSync(join(mobileRoot, path), "utf8");

test("Settings and Tasks More consume the same operational VSR control", () => {
  const panel = read("src/components/SilentInputControlPanel.tsx");
  assert.match(panel, /probeVSRCapabilities\(targetDeviceId\)/, "selected must be probed, not treated as ready");
  assert.match(panel, /runCapabilityGapFix\(/, "the typed agent recovery route must be invocable");
  assert.match(panel, /formatFixElapsed\(/, "an install must narrate elapsed time");
  assert.match(panel, /<SilentInputModal/, "Settings needs a real camera-to-inference test");
  assert.match(panel, /backend:\s*"user-machine"/, "the only selectable VSR backend must be the user machine");
  assert.doesNotMatch(panel, /<Choice[^>]+label="(?:On-device|Cloud)"/, "unimplemented VSR backends must not be selectable");

  const settings = read("app/(tabs)/settings.tsx");
  assert.match(settings, /settingsPane === "voice"[\s\S]*<SilentInputControlPanel/, "VSR test belongs in Voice settings");

  const tasks = read("app/(tabs)/tasks.tsx");
  assert.match(tasks, /showTaskOptions[\s\S]*<SilentInputControlPanel/, "Tasks ellipsis must expose VSR options");
  assert.match(tasks, /onTestRequested=\{\(\) => \{[\s\S]*setShowSilentInput\(true\)/, "Tasks must hand off its native Modal before the camera test");
  assert.match(tasks, /\[showNewTask\]/, "Tasks must refresh the preference after Settings changes it");
});

test("unsupported historical VSR selections migrate to off, not remote", () => {
  const config = read("src/lib/silentInput/config.ts");
  assert.match(config, /supportedBackend\s*=\s*value\.backend === "user-machine"/);
  assert.match(config, /enabled:\s*supportedBackend && value\.enabled === true/);
  assert.match(config, /backend:\s*"user-machine"/);
});

test("iOS Whisper model is a physical bundle resource", () => {
  const iosLocator = read("src/lib/whisperModelAsset.ios.ts");
  const project = read("ios/Yaver.xcodeproj/project.pbxproj");
  const speech = read("src/lib/speech.ts");
  assert.match(iosLocator, /filePath:\s*"ggml-whisper-tiny\.bin"/);
  assert.match(iosLocator, /isBundleAsset:\s*true/);
  assert.match(project, /ggml-whisper-tiny\.bin in Resources/);
  assert.match(project, /assets\/models\/ggml-whisper-tiny\.bin/);
  assert.match(speech, /rnInitWhisper\(whisperModelOptions\(\)\)/);
});
