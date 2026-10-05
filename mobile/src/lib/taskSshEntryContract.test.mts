import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";

const tasks = readFileSync(new URL("../../app/(tabs)/tasks.tsx", import.meta.url), "utf8");
const shell = readFileSync(new URL("../../app/shell.tsx", import.meta.url), "utf8");
const header = readFileSync(new URL("../components/TaskHeader.tsx", import.meta.url), "utf8");
const xterm = readFileSync(new URL("../components/XtermView.tsx", import.meta.url), "utf8");
const tabLayout = readFileSync(new URL("../../app/(tabs)/_layout.tsx", import.meta.url), "utf8");
const sshTab = readFileSync(new URL("../../app/(tabs)/ssh.tsx", import.meta.url), "utf8");
const sshPreferences = readFileSync(new URL("./sshPreferences.ts", import.meta.url), "utf8");

test("task SSH action carries the authoritative task owner into the shell", () => {
  assert.match(header, /accessibilityLabel="Open SSH terminal on this task's machine"/);
  assert.match(tasks, /pathname: "\/shell", params: \{ deviceId: ownerId, source: "task" \}/);
  assert.doesNotMatch(tasks, /pathname: "\/shell", params: \{ deviceId: activeDevice/);
});

test("shell connects the routed endpoint before opening its PTY", () => {
  assert.match(shell, /activeDevice\?\.id === requestedDeviceId/);
  assert.match(shell, /void selectDevice\(requested\)/);
  assert.match(shell, /const routeTargetMatches = !requestedDeviceId \|\| activeDevice\?\.id === requestedDeviceId/);
  assert.match(shell, /!routeTargetMatches/);
});

test("Tasks is first and SSH remains a primary option with machine selection", () => {
  assert.match(tabLayout, /name="tasks"/);
  assert.match(tabLayout, /TabIcon label="Tasks"/);
  assert.match(tabLayout, /name="ssh"/);
  assert.match(tabLayout, /TabIcon label="SSH"/);
  assert.ok(tabLayout.indexOf('name="tasks"') < tabLayout.indexOf('name="ssh"'));
  assert.match(sshTab, /<RemoteBoxBanner disableTap \/>/);
  assert.match(sshTab, /<NoMachineEmpty noun="SSH sessions" inlineOnly onDeviceChange={chooseLaunchForDevice} \/>/);
  assert.doesNotMatch(sshTab, /Open sessions|listTmuxSessions\(\)/);
  assert.doesNotMatch(sshTab, />\s*(?:Active|Review|Completed)\s*</);
  assert.match(sshTab, /activeDevice && connectedDeviceIds\.includes\(activeDevice\.id\)/);
  assert.match(sshTab, /devices\.find\(\(candidate\) => connectedDeviceIds\.includes\(candidate\.id\)\)/);
  assert.match(sshTab, /const initiallyOpened = alreadyConnected \|\| activeDevice \|\| devices\[0\]/);
  assert.match(sshTab, />MACHINE<\/Text>/);
  assert.match(sshTab, /accessibilityState=\{\{ selected \}\}/);
  assert.match(sshTab, /<Text[^>]*>Open SSH<\/Text>/);
  assert.doesNotMatch(sshTab, /<Modal/);
  assert.match(sshTab, /<ScrollView/);
  assert.match(sshTab, /contentContainerStyle={styles\.content}/);
  assert.match(sshTab, /pathname: "\/shell"/);
  assert.match(sshTab, /\["codex", "Codex", "Bypass approvals and sandbox"\]/);
  assert.match(sshTab, /\["claude", "Claude Code", "Skip permission prompts"\]/);
  assert.match(sshTab, /\["opencode", "OpenCode", "Start in auto mode"\]/);
  assert.match(sshTab, /\["raw", "Raw shell", "Plain login shell"\]/);
  assert.match(sshTab, /params: \{ deviceId, source: "ssh-home", launch: mode \}/);
  assert.match(shell, /if \(launch && launch !== "raw"\) q\.set\("launch", launch\)/);
  assert.match(shell, /Choose a machine for SSH/);
  assert.match(shell, /connectedDeviceIds\.includes\(device\.id\) \|\| device\.online/);
  assert.match(shell, /void pickDevice\(device\)/);
});

test("full VT bridge exposes and invokes hardware keyboard focus", () => {
  assert.match(xterm, /window\.__yvFocus = function \(\) \{ try \{ term\.focus\(\)/);
  assert.match(xterm, /window\.__yvFocus && window\.__yvFocus\(\)/);
  assert.match(shell, /xtermRef\.current\?\.focus\(\)/);
  assert.match(xterm, /Keyboard\.addListener\("keyboardDidShow"/);
  assert.match(xterm, /router\.isAttached\(\)/);
  assert.match(xterm, /if \(attached\) Keyboard\.dismiss\(\)/);
});

test("raw SSH stays raw while coding agents and manual sessions use tmux", () => {
  assert.doesNotMatch(shell, /AUTO_TMUX_COMMAND|autoTmuxTimer/);
  assert.match(shell, /runnerTmuxSession\(requestedLaunch\)/);
  assert.match(shell, /quicClient\.listTmuxSessions\(\)/);
  assert.match(shell, /New or resume yaver-shell/);
  assert.match(shell, />\{tmuxBusy \? "…" : "Attach"\}<\/Text>/);
  assert.match(shell, />Detach<\/Text>/);
  assert.match(shell, /controlTmuxClient\(sessionName, "detach"\)/);
  assert.match(shell, /controlTmuxClient\(sessionName, "kill-pane"\)/);
  assert.match(shell, /same as Ctrl-B, X, then Y/);
  assert.match(shell, /router\.replace\("\/\(tabs\)\/ssh"/);
  assert.match(shell, /label="Exit SSH"/);
});

test("SSH exposes named STT and TTS controls", () => {
  assert.match(shell, /name=\{dictating \? "mic" : "mic-outline"\}/);
  assert.match(shell, /startRealtimeTranscribe\(\(partial\) => setVoiceDraft\(partial\)\)/);
  assert.match(shell, /sendText\(`\$\{command\}\\r`\)/);
  assert.match(shell, /Send dictated command to the current terminal pane/);
  assert.match(shell, /Read recent terminal output aloud/);
  assert.match(shell, /speaking \? "stop-circle-outline" : "volume-medium-outline"/);
  assert.match(shell, /await speakText\(readable, \{ provider: "device" \}\)/);
  assert.match(shell, /Download the on-device speech model in Settings → Voice/);
});

test("SSH user-facing copy never uses task workflow language", () => {
  const quotedCopy = [...shell.matchAll(/(?:"([^"\n]*)"|`([^`\n]*)`)/g)]
    .map((match) => match[1] || match[2] || "")
    .filter((copy) => /\btask(?:s)?\b/i.test(copy));
  assert.deepEqual(quotedCopy, ["task-followup"], "only the wire-protocol inputMode may retain its stable task-followup value");
});

test("foreground TUIs retain an always-visible exit route without agent launchers", () => {
  assert.match(shell, /behavior=\{Platform\.OS === "ios" \? "padding" : "height"\}/);
  assert.match(shell, /accessibilityLabel="Exit the foreground terminal app"/);
  assert.match(shell, /<Text style=\{styles\.headerExitText\}>Exit TUI<\/Text>/);
  assert.doesNotMatch(shell, /AGENT_LAUNCHERS|OpenCodeConfigModal/);
});

test("SSH owns a persistent software-keyboard mode", () => {
  assert.match(sshPreferences, /"auto" \| "never" \| "always"/);
  assert.match(shell, /accessibilityLabel="SSH settings"/);
  assert.match(shell, /Voice or hardware keyboard only/);
  assert.match(shell, /softwareKeyboardMode=\{softwareKeyboardMode\}/);
  assert.match(xterm, /softwareKeyboardMode === "never"/);
  assert.match(xterm, /softwareKeyboardMode === "always"/);
  assert.match(xterm, /setAttribute\("inputmode", "none"\)/, "Never mode must prevent the WebView textarea from summoning Android's IME");
  assert.doesNotMatch(xterm, /Platform\.OS !== "android"/, "keyboard modes must also apply on iPhone/iPad");
});
