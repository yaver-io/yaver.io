import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const read = (path: string) => readFile(new URL(path, import.meta.url), "utf8");

test("local-only is a real transport boundary, not a presentation flag", async () => {
  const source = await read("../context/DeviceContext.tsx");
  assert.match(source, /allowsRemoteAutoConnect\(codingMode\)/);
  assert.match(source, /autoConnectCancelRef\.current = true/);
  assert.match(source, /connectionManager\.disconnectAll\(\)/);
  assert.match(source, /allowsRemoteAutoConnect\(codingModeRef\.current\)/);
});

test("No remote box is selectable from every requested entry point", async () => {
  const [picker, devices, settings] = await Promise.all([
    read("../components/RemoteBoxPickerModal.tsx"),
    read("../../app/(tabs)/devices.tsx"),
    read("../../app/(tabs)/settings.tsx"),
  ]);
  for (const source of [picker, devices, settings]) {
    assert.match(source, /No remote box/);
    assert.match(source, /setCodingMode\("local-only"\)|setCodingMode\(codingMode === "local-only"/);
  }
  assert.match(devices, /user\?\.isOwner === true/);
  assert.match(devices, /testID="devices-remoteless-card"/);
  assert.match(devices, /borderStyle: "dashed"/);
  assert.match(devices, /REMOTELESS · OWNER PREVIEW/);
});

test("phone-local Tasks require a phone checkout and use explicit placement", async () => {
  const source = await read("../../app/(tabs)/tasks.tsx");
  assert.match(source, /forceLocal: codingMode === "local-only"/);
  assert.match(source, /codingMode === "local-only" && !selectedPhoneCheckout/);
  assert.match(source, /askModeEnabled \? "audit" : "vibe"/);
  assert.match(source, /const consumeAskMode = useCallback\(\(\) => {\s*setAskModeEnabled\(false\);/);
  assert.match(source, /consumeAskMode\(\);\s*\n\s*pendingOpenTaskRef\.current = initialTask/);
  assert.match(source, /setFollowUpText\(""\);\s*\n\s*setFollowUpImages\(\[\]\);\s*\n\s*consumeAskMode\(\);/);
  assert.match(source, /const isLocalFollowUp = isPhoneLocalTask\(selectedTask\) \|\| selectedTask\.runnerId === "yaver-agent"/);
  assert.match(source, /isPhoneLocalTask\(task\)/);
  assert.match(source, /SandboxGitPanel/);
  assert.match(source, /Review &amp; deliver/);
  assert.match(source, /canComposeWithRemoteless/);
  assert.match(source, /ownerRemotelessEnabled && phoneProjects\.length/);
  assert.match(source, /selectedPhoneProject\?\.name \|\| selectedComposerProject\?\.name/);
  assert.match(source, /taskExecutionPlacement\.lane === "blocked"/);
  assert.match(source, /Images need a remote box/);
  assert.match(source, /endRemotelessTask\(taskId, "stopped"/);
  assert.match(
    source,
    /!isEffectivelyConnected && !\(selectedPhoneCheckout && taskExecutionPlacement\.lane !== "remote"\)/,
    "the Send button must remain enabled for an explicit phone-local checkout without a remote connection",
  );
  const projectsSource = await readFile(new URL("../../app/(tabs)/apps.tsx", import.meta.url), "utf8");
  assert.match(
    projectsSource,
    /ensureRepo\(gitForSlug\(project\.slug\)\)[\s\S]{0,500}phoneCheckout: project\.slug/,
    "a generated phone project must become a real repository before Vibe opens the task composer",
  );
  assert.match(
    projectsSource,
    /useFocusEffect\([\s\S]{0,300}loadRemotelessProjects\(\)/,
    "the mounted Projects tab must refresh phone checkouts whenever it regains focus",
  );
});

test("DeepSeek can be configured from the backend screen that advertises it", async () => {
  const source = await read("../../app/sandbox-ai.tsx");
  assert.match(source, /deepseek: LOCAL_KEYS\.deepseekApiKey/);
  assert.match(source, /\["deepseek", "anthropic", "openai", "glm"\]/);
  assert.match(source, /av\.deepseekKey/);
  assert.match(source, /setManagedCodingEnabled\(next\)/);
  assert.match(source, /Use account-managed coding/);
});

test("Projects reads phone checkouts and connected Git providers without a box", async () => {
  const source = await read("../../app/(tabs)/projects.tsx");
  assert.match(source, /listLocalPhoneProjectsMeta/);
  assert.match(source, /discoverConnectedProviderProjects/);
  assert.match(source, /cloneGitRepoToPhone/);
  assert.match(source, /codingMode === "local-only"/);
});

test("failed clones cannot overwrite or strand a phone project", async () => {
  const source = await read("./cloneToPhone.ts");
  assert.match(source, /listLocalPhoneProjectsMeta/);
  assert.match(source, /already exists on this phone/);
  assert.match(source, /deleteLocalPhoneProject\(slug\)/);
  assert.match(source, /globalThis as any\)\.Buffer\) \(globalThis as any\)\.Buffer = Buffer/);
});

test("phone-local repository downloads hold the Android data-sync foreground lease", async () => {
  const [screen, lifecycle, localStore] = await Promise.all([
    read("../../app/repo-coding.tsx"),
    read("./remotelessTaskLifecycleCore.ts"),
    read("./phoneSandboxLocal.ts"),
  ]);
  assert.match(screen, /withRemotelessTask\([\s\S]{0,400}kind: "git-clone"[\s\S]{0,300}cloneGitRepoToPhone\(input\)/);
  assert.match(lifecycle, /"git-clone"/);
  assert.match(
    localStore,
    /if \(!project\.schema\?\.tables\?\.length\) return \{ \.\.\.EMPTY_STATS, perTable: \{\} \}/,
    "blank Git repositories must not open the unrelated SQLite runtime while cloning",
  );
});
