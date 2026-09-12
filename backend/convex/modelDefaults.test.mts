import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";

import {
  YAVER_MODEL_DEFAULTS,
  applyRunnerModelDefaults,
  parseRunnerModelDefaults,
} from "./modelDefaults.ts";
import { PREDEFINED_MODELS } from "./aiModels.ts";

test("Convex defaults match the Yaver runner contract", () => {
  assert.deepEqual(YAVER_MODEL_DEFAULTS, {
    claude: { model: "claude-opus-4-8" },
    codex: { model: "gpt-5.6-sol", reasoningEffort: "medium" },
    opencode: { model: "deepseek/deepseek-v4-flash" },
  });
});

test("Convex owns the Codex model and reasoning matrix", () => {
  const codex = PREDEFINED_MODELS
    .filter((model) => model.runnerId === "codex")
    .map((model) => ({
      model: model.modelId,
      efforts: "supportedReasoningEfforts" in model ? model.supportedReasoningEfforts : undefined,
    }));
  assert.deepEqual(codex, [
    { model: "gpt-5.6-sol", efforts: ["low", "medium", "high", "xhigh", "max"] },
    { model: "gpt-5.6-terra", efforts: ["low", "medium", "high", "xhigh", "max"] },
    { model: "gpt-5.6-luna", efforts: ["low", "medium", "high", "xhigh", "max"] },
    { model: "gpt-5.5", efforts: ["low", "medium", "high", "xhigh"] },
    { model: "gpt-5.4-mini", efforts: ["low", "medium", "high", "xhigh"] },
    { model: "gpt-5.3-codex-spark", efforts: ["low", "medium", "high", "xhigh"] },
  ]);
});

test("Convex seeds the first-class DeepSeek OpenCode choices", () => {
  const deepseek = PREDEFINED_MODELS
    .filter((model) => model.runnerId === "opencode" && "providerId" in model && model.providerId === "deepseek")
    .map((model) => ({
      model: model.modelId,
      lifecycle: "lifecycle" in model ? model.lifecycle : undefined,
      isDefault: "isDefault" in model ? model.isDefault === true : false,
    }));
  assert.deepEqual(deepseek, [
    { model: "deepseek/deepseek-v4-flash", lifecycle: "active", isDefault: true },
    { model: "deepseek/deepseek-v4-pro", lifecycle: "active", isDefault: false },
    { model: "deepseek/deepseek-v4-flash-vision-exp", lifecycle: "active", isDefault: false },
    { model: "deepseek/deepseek-chat", lifecycle: "legacy", isDefault: false },
  ]);
});

test("stored Convex defaults override bootstrap values and invalid fields fail closed", () => {
  assert.deepEqual(parseRunnerModelDefaults(JSON.stringify({
    codex: { model: "future-codex", reasoningEffort: "xhigh" },
    unknown: { model: "bad" },
  })), {
    claude: { model: "claude-opus-4-8" },
    codex: { model: "future-codex", reasoningEffort: "xhigh" },
    opencode: { model: "deepseek/deepseek-v4-flash" },
  });
});

test("stale aiModels rows cannot remain the advertised default", () => {
  const rows = applyRunnerModelDefaults([
    { runnerId: "codex", modelId: "gpt-5.4", isDefault: true, sortOrder: 1 },
  ], YAVER_MODEL_DEFAULTS);
  assert.equal(rows.find((row) => row.modelId === "gpt-5.4")?.isDefault, false);
  assert.equal(rows.find((row) => row.modelId === "gpt-5.6-sol")?.isDefault, true);
});

test("global-default mutation is full-session and owner gated", () => {
  const source = fs.readFileSync(new URL("./http.ts", import.meta.url), "utf8");
  const start = source.indexOf('path: "/config/model-defaults"');
  const end = source.indexOf("// ── Public install catalogue", start);
  assert.ok(start >= 0, "route must exist");
  const route = source.slice(start, end);
  assert.match(route, /authenticateRequest\(ctx, request\)/);
  assert.match(route, /requireFullScope\(session\)/);
  assert.match(route, /isOwner\(session\.email, String\(session\.userDocId\)\)/);
  assert.match(route, /internal\.platformConfig\.get/);
  assert.match(route, /internal\.platformConfig\.set/);
});

test("canonical backend deploy synchronizes model rows without invoking the vault", () => {
  const deploy = fs.readFileSync(new URL("../../scripts/deploy-convex.sh", import.meta.url), "utf8");
  const installIndex = deploy.indexOf("npm ci --no-audit --no-fund");
  const deployIndex = deploy.indexOf("npx convex deploy --yes");
  const seedIndex = deploy.indexOf("npx convex run aiModels:seed --prod");
  assert.ok(installIndex >= 0, "a clean release worktree must restore pinned backend dependencies");
  assert.ok(installIndex < deployIndex, "backend dependencies must be restored before Convex bundles the deployment");
  assert.ok(deployIndex >= 0, "Convex code deploy must remain present");
  assert.ok(seedIndex > deployIndex, "production model rows must sync after the code deploy");
  assert.doesNotMatch(deploy, /echo\s+"[^"\n]*`yaver vault`/,
    "a missing-key diagnostic must not execute yaver vault through shell substitution");
  assert.match(deploy, /\.\/deploy\/deploy\.sh backend/,
    "credential guidance must point back to the canonical deploy wrapper");
});
