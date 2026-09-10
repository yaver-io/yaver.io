import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const source = readFileSync(join(process.cwd(), "components/dashboard/VibeCodingView.tsx"), "utf8");

test("web Tasks support select all and prompt-free offline bulk deletion", () => {
  assert.match(source, />\s*Select all\s*</);
  assert.match(source, /Delete · \{selectedTaskIds\.size\}/);
  assert.match(source, /setTaskList\(\(previous\) => previous\.filter\(\(task\) => !deleted\.has\(task\.id\)\)\)/,
    "selected rows must disappear immediately");
  assert.match(source, /await tombstoneAgentTask\(CONVEX_URL, token, deviceId, task\.id\)/,
    "central deletion intent must not depend on agent reachability");
  assert.match(source, /void agentClient\.deleteTask\(task\.id\)\.catch/,
    "direct agent cleanup must remain best-effort");
  assert.doesNotMatch(source, /agent did not acknowledge deletion/,
    "an offline agent must never block or reverse removal");
});
