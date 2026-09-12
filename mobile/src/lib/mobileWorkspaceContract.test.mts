import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const wizard = readFileSync(new URL("../../app/phone-projects.tsx", import.meta.url), "utf8");

test("Mobile Workspace target widget is remote-only", () => {
  const start = wizard.indexOf("Where should this workspace run?");
  const targetPane = wizard.slice(start, wizard.indexOf("Yaver Serverless", start));
  assert.ok(targetPane.includes("Primary device · Recommended"));
  assert.ok(targetPane.includes("vibing and rendering"));
  assert.equal(targetPane.includes("Remoteless"), false);
  assert.equal(targetPane.includes("This phone"), false);
});

test("Mobile Workspace does not ask stack-selection questions", () => {
  const survey = wizard.slice(wizard.indexOf("const SURVEY_QUESTIONS"), wizard.indexOf("const YAVER_CLOUD_BASE"));
  for (const forbidden of ["Where will it run?", "Which framework", "Which language", "Which backend", "Which database"]) {
    assert.equal(survey.includes(forbidden), false, `unexpected stack question: ${forbidden}`);
  }
});

test("Mobile Workspace consumes the agent readiness contract and exposes fixes", () => {
  assert.ok(wizard.includes("mobileWorkspaceStatus"));
  assert.ok(wizard.includes("getRunnersForTarget"));
  assert.ok(wizard.includes("setOpenCodeConfigVisible(true)"));
  assert.ok(wizard.includes("selectedWorkspaceClient.installRunner"));
  assert.ok(wizard.includes("configureGitProvider(gitProvider)"));
  assert.ok(wizard.includes("Test on remote box"));
});

test("Mobile Workspace retries readiness when the selected transport connects", () => {
  assert.match(
    wizard,
    /\}, \[connected, selectedRunnerConnected, selectedRunnerDevice, selectedWorkspaceClient, selectedWorkspaceTarget\]\);/,
    "readiness callback must be recreated when connection state or resolved transport changes",
  );
});

test("runner and model are persisted as separate remote-box choices", () => {
  assert.ok(wizard.includes("const [runner, setRunner]"));
  assert.ok(wizard.includes("const [model, setModel]"));
  assert.ok(wizard.includes("inventory.models.map"));
  assert.ok(wizard.includes("model || info?.model"));
  assert.ok(wizard.includes('"/agent/runners/test"'));
  assert.ok(wizard.includes("model: effectivePrompt && codingMode"));
  assert.ok(wizard.includes("model: model || undefined"));
});
