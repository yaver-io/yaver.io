import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import {
  MOBILE_APP_PALETTES,
  MOBILE_APP_GIT_PROVIDERS,
  buildMobileAppBuilderPrompt,
  chooseBuilderRemote,
  projectSlug,
} from "./mobileAppBuilderFlow.ts";

const devices = [
  { id: "secondary", name: "Beta box" },
  { id: "primary", name: "Alpha box" },
];

test("a connected primary remote wins over the active remote", () => {
  const chosen = chooseBuilderRemote(devices, new Set(["secondary", "primary"]), "primary", "secondary");
  assert.equal(chosen?.id, "primary");
});

test("project names become safe portable directory names", () => {
  assert.equal(projectSlug("  Café Companion!  "), "cafe-companion");
  assert.equal(projectSlug("***"), "mobile-app");
});

test("an unavailable primary is never presented as the build location", () => {
  const chosen = chooseBuilderRemote(devices, new Set(["secondary"]), "primary", "secondary");
  assert.equal(chosen?.id, "secondary");
  assert.equal(chooseBuilderRemote(devices, new Set(), "primary", "secondary"), null);
});

test("the chat handoff carries palette and inference rules instead of wizard questions", () => {
  const prompt = buildMobileAppBuilderPrompt(MOBILE_APP_PALETTES[0], "Alpha box");
  assert.match(prompt, /Alpha box/);
  assert.match(prompt, /#7557FF/);
  assert.match(prompt, /Do not turn this into a questionnaire/);
  assert.match(prompt, /Do not .*ask whether the app needs a backend/);
});

test("mobile initialization offers the three real Git destinations", () => {
  assert.deepEqual(MOBILE_APP_GIT_PROVIDERS.map((provider) => provider.id), ["yaver-git", "github", "gitlab"]);
});

test("initialization auto-submits a hidden kickoff into the selected project", async () => {
  const source = await readFile(new URL("../../app/(tabs)/newproject.tsx", import.meta.url), "utf8");
  assert.match(source, /autoSubmit:\s*"1"/);
  assert.match(source, /hideInitialPrompt:\s*"1"/);
  assert.match(source, /selectProject:\s*"1"/);
  assert.doesNotMatch(source, /openNew:\s*"1"/);
});

test("mobile app creation separates its four compact onboarding decisions", async () => {
  const source = await readFile(new URL("../../app/(tabs)/newproject.tsx", import.meta.url), "utf8");
  assert.match(source, /type Step = "name" \| "device" \| "git" \| "palette" \| "initializing"/);
  assert.match(source, /const WIZARD_STEPS = \["name", "device", "git", "palette"\] as const/);
  assert.match(source, /Step \{stepNumber\} of \{WIZARD_STEPS\.length\}/);
  assert.match(source, /accessibilityRole="progressbar"/);
  assert.match(source, /Name your project/);
  assert.match(source, /Choose a device/);
  assert.match(source, /Choose Git/);
  assert.match(source, /Choose colors/);
  assert.doesNotMatch(source, /Where should we build\?/);
  assert.doesNotMatch(source, /WE'LL USE THIS PRIMARY BOX/);
});

test("the project-name step remains usable above the native keyboard", async () => {
  const source = await readFile(new URL("../../app/(tabs)/newproject.tsx", import.meta.url), "utf8");
  assert.match(source, /<KeyboardAvoidingView/);
  assert.match(source, /behavior=\{Platform\.OS === "ios" \? "padding" : undefined\}/);
  assert.match(source, /keyboardDismissMode="interactive"/);
  assert.match(source, /keyboardShouldPersistTaps="handled"/);

  const nameStep = source.slice(source.indexOf('{step === "name" ? ('), source.indexOf(') : step === "device" ? ('));
  assert.match(nameStep, /<TextInput/);
  assert.match(nameStep, /onPress=\{continueFromName\}/);
  assert.doesNotMatch(nameStep, /<RemoteChoice/);
});

test("an explicit This phone choice is not replaced by the device recommendation", async () => {
  const source = await readFile(new URL("../../app/(tabs)/newproject.tsx", import.meta.url), "utf8");
  assert.match(source, /useState<string \| null \| undefined>\(undefined\)/);
  assert.match(source, /if \(selectedDeviceId === null\) return;/);
  assert.match(source, /accessibilityState=\{\{ selected: selectedDeviceId === null \}\}/);
});

test("This phone initialization stays in the local sandbox instead of opening an empty transport URL", async () => {
  const source = await readFile(new URL("../../app/(tabs)/newproject.tsx", import.meta.url), "utf8");
  const initializer = source.indexOf("const initializeProject");
  const localBranch = source.indexOf("if (!selectedDevice)", initializer);
  const remoteWizard = source.indexOf("quicClient.wizardStart()", localBranch);
  assert.ok(initializer >= 0, "expected the project initializer");
  assert.ok(localBranch >= 0, "expected an explicit local initialization branch");
  assert.ok(remoteWizard > localBranch, "local placement must be resolved before the remote wizard starts");
  assert.match(source.slice(localBranch, remoteWizard), /createLocalPhoneProject/);
  assert.match(source.slice(localBranch, remoteWizard), /router\.replace\(`\/phone-project\/\$\{project\.slug\}`/);
});
