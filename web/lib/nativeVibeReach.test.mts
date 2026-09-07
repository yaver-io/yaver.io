/**
 * nativeVibeReach.test.mts — can a NATIVE surface start a vibe at all?
 *
 *   npx tsx web/lib/nativeVibeReach.test.mts
 *
 * ── The finding this pins (2026-08-03) ─────────────────────────────────────
 *
 * Every native surface was unvibeable for the same single reason:
 * `tvos/YaverTV/AgentClient.swift` exposed `listTasks()` and **zero POST
 * verbs**. tvOS, visionOS (which shares that file), watch and Wear could WATCH
 * work happen and never START it.
 *
 * That is why the coverage audit read "untested" for those surfaces when the
 * honest word was "unable", and why the black → red → black colour loop could
 * not run there: step one of the loop is "send: change the login background to
 * red". Not a harness gap — a missing product capability.
 *
 * The things people assume are the blocker are NOT:
 *   • pixels — tvOS already streams frames (Views/DroidStreamView.swift polls
 *     /droid/frame; AgentClient also has the headless web-preview capture flow)
 *   • /tasks/{id}/continue — every arc stays on the SAME task/session/tmux seat
 *   • automation — simctl + XCUITest + `simctl io <udid> screenshot` is a
 *     complete driver; Playwright simply isn't the one
 *
 * Lives in web/lib so `scripts/run-client-guards.sh` sweeps it with every other
 * guard — a check nobody runs is a check that does not exist.
 */
import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const repo = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const agentClient = readFileSync(join(repo, "tvos/YaverTV/AgentClient.swift"), "utf8");
const visionProject = readFileSync(join(repo, "visionos/project.yml"), "utf8");

test("the shared native client can CREATE a task, not only list them", () => {
  assert.match(agentClient, /func createTask\(/,
    "AgentClient has no createTask — every native surface is back to watch-only, " +
    "and the colour loop cannot take its first step there");
  const start = agentClient.indexOf("func createTask(");
  const body = agentClient.slice(start, start + 3000);
  assert.match(body, /request\(\s*"POST"\s*,\s*path:\s*"\/tasks"/,
    "createTask does not POST /tasks — it must hit the same route the web funnel does");
});

test("it does NOT invent its own runner/model default", () => {
  const start = agentClient.indexOf("func createTask(");
  const body = agentClient.slice(start, start + 3000);
  // Empty means "let the agent apply the account's per-device primary", which
  // is exactly what the phone gets. A TV hardcoding a model is how a surface
  // drifts onto one the subscription cannot run — that shipped this week.
  assert.match(body, /runner:\s*String\s*=\s*""/,
    "createTask hardcodes a runner default instead of deferring to the account's primary");
  assert.match(body, /model:\s*String\s*=\s*""/,
    "createTask hardcodes a model default instead of deferring to the account's primary");
  assert.ok(!/gpt-5|claude-|glm-/i.test(body),
    "createTask names a concrete model — the agent owns that choice, per device");
});

test("visionOS still inherits the client rather than copying it", () => {
  assert.match(visionProject, /path:\s*\.\.\/tvos\/YaverTV\/AgentClient\.swift/,
    "visionOS stopped sharing AgentClient.swift — it would silently lose createTask, " +
    "and a copied client is the drift that broke the visionOS archive on 2026-08-03");
});

test("visionOS compiles the shared vibe panel with its visual dependencies", () => {
  assert.match(visionProject, /path:\s*\.\.\/tvos\/YaverTV\/Views\/VibeTurnPanel\.swift/,
    "visionOS no longer compiles the shared vibe panel");
  assert.match(visionProject, /path:\s*\.\.\/tvos\/YaverTV\/YouTubeAnimation\.swift/,
    "VibeTurnPanel renders MicListeningIndicator, but visionOS omitted its source — " +
    "the real archive then fails while the tvOS target stays green");
});

test("the pixel half already exists, so only creation was missing", () => {
  const streamView = readFileSync(join(repo, "tvos/YaverTV/Views/DroidStreamView.swift"), "utf8");
  assert.match(streamView, /droid\/frame/,
    "the tvOS frame source is gone — the colour verdict on TV depends on it");
  assert.match(agentClient, /vibing\/preview\/(start|snapshot|frames)/,
    "the headless web-preview capture flow is gone — that is how a TV reads a web project's pixels");
});
