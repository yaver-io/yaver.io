import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const source = readFileSync(join(process.cwd(), "components/dashboard/VibeCodingView.tsx"), "utf8");

test("web Tasks support select all and acknowledged bulk deletion", () => {
  assert.match(source, />\s*Select all\s*</);
  assert.match(source, /Delete · \{selectedTaskIds\.size\}/);
  assert.match(source, /for \(const task of taskList\.filter[\s\S]{0,240}await agentClient\.deleteTask\(task\.id\)/);
  assert.match(source, /setSelectedTaskIds\(new Set\(failed\)\)/,
    "unacknowledged rows must remain selected instead of disappearing");
});
