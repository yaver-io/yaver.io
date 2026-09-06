import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const mobileRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const tasks = readFileSync(join(mobileRoot, "app/(tabs)/tasks.tsx"), "utf8");
const header = readFileSync(join(mobileRoot, "src/components/TaskHeader.tsx"), "utf8");
const studio = readFileSync(join(mobileRoot, "src/components/studio/StudioChatPane.tsx"), "utf8");
const transport = readFileSync(join(mobileRoot, "src/lib/quic.ts"), "utf8");

test("the mobile overview treats discovered coding seats only as Tasks", () => {
  assert.match(tasks, /label: "Active"[\s\S]{0,120}status === "running" \|\| t\.status === "queued"/);
  assert.match(tasks, /label: "Review"[\s\S]{0,120}status === "review" \|\| t\.status === "ready"/);
  assert.doesNotMatch(tasks, /Yaver Sessions|Adopt Session|Attach Session|adoptTmuxSession/);
  assert.doesNotMatch(tasks, /`Yaver session · \$\{\[item\.tmuxSession/,
    "task cards must not advertise the local hosting implementation");
  assert.doesNotMatch(studio, /\? "Yaver session" : null/,
    "chat topic cards must remain Task-first");
});

test("pull-to-refresh discovers local runners before repainting Tasks", () => {
  assert.match(transport, /async reconcileTasks\(\): Promise<number>[\s\S]{0,500}\/tasks\/reconcile/);
  assert.match(tasks, /const onRefresh = useCallback\([\s\S]{0,900}runnerClient\.reconcileTasks\(\)[\s\S]{0,300}await fetchTasks\(\)[\s\S]{0,200}await refreshAgentTaskSnapshots\(\)/);
});

test("Task cards render authoritative model and reasoning metadata", () => {
  assert.match(tasks, /const rawModel = String\(item\.model \|\| ""\)\.trim\(\)/);
  assert.match(tasks, /const effort = String\(item\.reasoningEffort \|\| ""\)\.trim\(\)/);
  assert.match(tasks, /`\$\{rawModel\}\$\{effort \? ` · \$\{effort\}` : ""\}`/);
});

test("hosting details remain progressive disclosure, not primary chrome", () => {
  assert.doesNotMatch(header, /tmuxSession|tmuxLabel|Yaver session/);
  assert.match(tasks, /label: "Yaver session"/,
    "Agent context may identify the exact local host for diagnostics");
  assert.match(tasks, /tmuxPaneId/,
    "the exact pane address remains available to continuation routing and details");
});

test("an active task exposes one Stop action in the fixed header", () => {
  assert.equal(
    [...header.matchAll(/accessibilityLabel="Stop task"/g)].length,
    1,
    "TaskHeader must own the single task-stop action",
  );
  assert.equal(
    [...tasks.matchAll(/accessibilityLabel="Stop task"/g)].length,
    0,
    "the follow-up composer must not duplicate the header Stop action",
  );
  assert.match(tasks, /primaryAction=\{[\s\S]{0,180}isRunning \? "stop"/,
    "running task detail must route Stop through TaskHeader");
});

test("the latest human-readable task update stays outside the scrolling transcript", () => {
  assert.match(
    tasks,
    /Keep the current human update outside the transcript[\s\S]{0,700}<TaskSessionSummary[\s\S]{0,500}<FlatList/,
    "task status must remain visible when queued follow-ups push the active assistant turn up",
  );
  assert.doesNotMatch(
    tasks,
    /ListHeaderComponent=\{[\s\S]{0,240}<TaskSessionSummary/,
    "the current task summary must not scroll away as a FlatList header",
  );
  assert.match(
    tasks,
    /\[selectedTask\?\.id, selectedTask\?\.presentation, selectedTask\?\.resultText, selectedTask\?\.status\]/,
    "semantic presentation updates must drive conversation follow behavior",
  );
});

test("bulk selection deletes only after each owning agent acknowledges", () => {
  assert.match(tasks, /accessibilityLabel="Select tasks"/);
  assert.match(tasks, /accessibilityLabel="Select all visible tasks"/);
  assert.match(tasks, /Delete · \{selectedBulkTaskKeys\.size\}/);
  assert.match(tasks, /for \(const task of selected\)[\s\S]{0,180}await handleDeleteTask\(task, true\)/);
  assert.match(tasks, /connectionManager\.clientFor\(owner\.id\)[\s\S]{0,500}await client\.deleteTask\(taskId\)/,
    "bulk delete must reuse the owning-agent ACK path");
  assert.match(tasks, /Those tasks remain selected/,
    "failed remote deletions must remain visible and selected");
});
