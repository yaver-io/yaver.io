import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const repo = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");
const tasks = readFileSync(join(repo, "mobile/app/(tabs)/tasks.tsx"), "utf8");
const tvos = readFileSync(join(repo, "tvos/YaverTV/Views/TaskDetailView.swift"), "utf8");
const androidTv = readFileSync(join(repo, "androidtv/app/src/main/kotlin/io/yaver/tv/ui/PlaceholderScreens.kt"), "utf8");

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

test("native TV task surfaces prioritize semantic assistant updates while coding", () => {
  assert.match(tvos, /runnerCoding[\s\S]{0,180}\$0\.kind == "message"[\s\S]{0,420}LATEST UPDATE FROM YAVER/);
  assert.match(androidTv, /status == "running" \|\| status == "queued"[\s\S]{0,180}it\.kind == "message"[\s\S]{0,420}LATEST UPDATE FROM YAVER/);
});
