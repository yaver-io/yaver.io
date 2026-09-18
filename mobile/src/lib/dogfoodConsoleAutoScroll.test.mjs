import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(
  join(here, "../../../sdk/feedback/react-native/src/DogfoodSessionUi.tsx"),
  "utf8",
);

assert.match(source, /<ScrollView/,
  "Dogfood logs must render in a scroll container instead of clipping plain text");
assert.match(source, /nestedScrollEnabled/,
  "the log scroller must work inside Android's outer Dogfood ScrollView");
assert.match(source, /onContentSizeChange=\{followLogTail\}/,
  "new output must request a scroll to the live tail");
assert.match(source, /logScrollRef\.current\?\.scrollToEnd\(\{ animated: false \}\)/,
  "tail following must use the native scroll operation without animation backlog");
assert.match(source, /onScrollBeginDrag=\{pauseLogTail\}/,
  "manual scrolling must pause tail following while older logs are being read");
assert.match(source, /followLogTailRef\.current = distanceFromTail <= LOG_TAIL_THRESHOLD_PX/,
  "scrolling back to the bottom must resume live tail following");

console.log("dogfood console auto-scroll contract passed");
