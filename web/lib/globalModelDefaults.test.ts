import assert from "node:assert/strict";
import fs from "node:fs";

const repoRoot = new URL("../../", import.meta.url);
const source = (path: string) => fs.readFileSync(new URL(path, repoRoot), "utf8");

const settingsSource = fs.readFileSync(
  new URL("../components/dashboard/SettingsView.tsx", import.meta.url),
  "utf8",
);

assert.match(
  settingsSource,
  /user\?\.isOwner \? <GlobalModelDefaultsCard/,
  "the global-default editor must be hidden unless Convex marks the session owner",
);
assert.match(
  settingsSource,
  /fetch\(`\$\{CONVEX_URL\}\/config\?ownerDefaults=/,
  "the editor must load its values from Convex",
);
assert.match(
  settingsSource,
  /fetch\(`\$\{CONVEX_URL\}\/config\/model-defaults`/,
  "the editor must save through the owner-gated Convex route",
);
assert.match(
  settingsSource,
  /Agents refresh from Convex within one minute/,
  "the management surface must state when the new default takes effect",
);

const backendDefaults = source("backend/convex/modelDefaults.ts");
assert.match(backendDefaults, /claude:\s*\{ model: "claude-opus-4-8" \}/);
assert.match(backendDefaults, /codex:\s*\{ model: "gpt-5\.6-sol", reasoningEffort: "medium" \}/);
assert.match(backendDefaults, /opencode:\s*\{ model: "deepseek\/deepseek-v4-flash" \}/);

const agentDefaults = source("desktop/agent/runner_model_defaults.go");
assert.match(agentDefaults, /"claude":\s*\{Model: "claude-opus-4-8"\}/);
assert.match(agentDefaults, /"codex":\s*\{Model: "gpt-5\.6-sol", ReasoningEffort: "medium"\}/);
assert.match(agentDefaults, /"opencode":\s*\{Model: "deepseek\/deepseek-v4-flash"\}/);

const mobileResolution = source("mobile/src/lib/remoteCodingSelection.ts");
assert.match(
  mobileResolution,
  /:\s*\[primary, fallback, rowDefault, picked, heuristic\]/,
  "an unpicked mobile seed must not override the live Yaver default",
);

const rnTV = source("mobile/app/tv-coding.tsx");
assert.match(
  rnTV,
  /runner\.models\?\.find\(\(row\) => row\.isDefault\)[\s\S]{0,180}preferredDefaultModelForRunner/,
  "the RN television surface must prefer the live Yaver default over its offline seed",
);

const phoneProjects = source("mobile/app/phone-projects.tsx");
assert.match(
  phoneProjects,
  /const advertisedDefault = inventory\?\.models\.find\(\(item\) => item\.isDefault\)\?\.id[\s\S]{0,220}DEFAULT_MODEL_BY_RUNNER/,
  "Mobile Workspace must probe the model advertised by the selected runner before an offline seed",
);

for (const path of [
  "tvos/YaverTV/Views/TaskComposerView.swift",
  "tvos/YaverTV/Views/VibeTurnPanel.swift",
]) {
  assert.match(
    source(path),
    /models\.first\(where:\s*\{\s*\$0\.isDefault == true\s*\}\)[\s\S]{0,100}models\.first/,
    `${path} must prefer the agent-advertised Yaver default`,
  );
}

const visionProject = source("visionos/project.yml");
assert.match(visionProject, /\.\.\/tvos\/YaverTV\/Views\/TaskComposerView\.swift/);
assert.match(visionProject, /\.\.\/tvos\/YaverTV\/Views\/VibeTurnPanel\.swift/);

const carSurface = source("mobile/app/car-voice-coding.tsx");
assert.match(carSurface, /runtimeSurfaceClient\.runtimeTurn/);
assert.match(
  carSurface,
  /runtimeSurfaceClient\.runtimeTurn\(deviceId, \{[\s\S]{0,260}target: \{\s*deviceId,/,
  "car/glass coding turns must let the shared runtime path resolve global runner/model defaults",
);

const runtimeTurn = source("desktop/agent/ops_runtime_turn.go");
assert.match(
  runtimeTurn,
  /CreateTaskWithOptions\([\s\S]{0,240}"runtime-turn",\s*req\.Target\.Runner/,
  "car/glass runtime turns must enter the shared agent task-resolution path",
);

const watchSession = source("watch/YaverWatch/SessionClient.swift");
assert.match(
  watchSession,
  /URL\(string: "http:\/\/\\\(box\.host\):\\\(box\.port\)\/runner\/session\/turn"\)/,
  "watchOS must continue the already-resolved live runner session",
);

console.log("globalModelDefaults: ALL PASS");
