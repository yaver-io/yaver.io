import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const tasksSource = readFileSync(new URL("../../app/(tabs)/tasks.tsx", import.meta.url), "utf8");

test("runner operations resolve a client for the selected runner machine", () => {
  assert.match(
    tasksSource,
    /runnerSelectionDeviceId\s*\? connectionManager\.clientFor\(runnerSelectionDeviceId\)/,
  );
});

test("the removed orphan tmux banner cannot leak sessions from another machine", () => {
  assert.doesNotMatch(tasksSource, /const liveRunnerSessions = tmuxSessions/);
  assert.doesNotMatch(tasksSource, /sessionHasUntrackedRunnerPane/);
});
