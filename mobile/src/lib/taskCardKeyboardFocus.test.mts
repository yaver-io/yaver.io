import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const tasksSource = readFileSync(
  new URL("../../app/(tabs)/tasks.tsx", import.meta.url),
  "utf8",
);

function functionBody(name: string, nextName: string): string {
  const start = tasksSource.indexOf(`const ${name} =`);
  const end = tasksSource.indexOf(`const ${nextName} =`, start + 1);
  assert.notEqual(start, -1, `${name} must exist`);
  assert.notEqual(end, -1, `${nextName} must follow ${name}`);
  return tasksSource.slice(start, end);
}

test("opening a task card collapses and blurs the follow-up composer", () => {
  const openTaskDetails = functionBody("openTaskDetails", "startRecording");

  const closeIndex = openTaskDetails.indexOf("closeFollowUpComposer()");
  const selectIndex = openTaskDetails.indexOf("setSelectedTask(task)");
  assert.ok(closeIndex >= 0, "task-card navigation must close the follow-up composer");
  assert.ok(selectIndex > closeIndex, "the composer must close before task detail changes");

  const taskCardOpeners = tasksSource.match(/onPress=\{\(\) => selectingTasks \? toggleTaskSelection\(item\) : openTaskDetails\(item\)\}/g) ?? [];
  const renderedTaskCards = tasksSource.match(/<TaskCard\b/g) ?? [];
  assert.equal(taskCardOpeners.length, renderedTaskCards.length, "every rendered task card must use the guarded detail opener");
});

test("the follow-up field focuses only after an explicit composer tap", () => {
  const openFollowUpComposer = functionBody("openFollowUpComposer", "closeFollowUpComposer");
  assert.match(openFollowUpComposer, /setFollowUpExpanded\(true\)/);
  assert.match(openFollowUpComposer, /followUpInputRef\.current\?\.focus\(\)/);

  const inputStart = tasksSource.indexOf('testID="followup-input"');
  const inputEnd = tasksSource.indexOf("/>", inputStart);
  assert.notEqual(inputStart, -1, "follow-up input must exist");
  assert.notEqual(inputEnd, -1, "follow-up input must be closed");
  const input = tasksSource.slice(inputStart, inputEnd);
  assert.match(input, /ref=\{followUpInputRef\}/);
  assert.doesNotMatch(input, /\bautoFocus\b/);
});
