import assert from "node:assert/strict";
import { DEFAULT_STUDIO_DEFAULTS, resolveStudioDefaults, studioLanesFor } from "./studioWorkspace.ts";

assert.deepEqual(studioLanesFor("SwiftUI", ["ios"]), ["device", "live", "logs"]);
assert.ok(!studioLanesFor("SwiftUI", ["ios"]).includes("browser"), "Swift must not be offered a Hermes/browser lane");
assert.deepEqual(studioLanesFor("Expo", ["mobile"]), ["device", "browser", "live", "logs"]);
assert.deepEqual(studioLanesFor("Next.js", ["web"]), ["browser", "live", "logs"]);
assert.deepEqual(studioLanesFor("Go backend", ["server"]), ["logs"]);

const rejected = resolveStudioDefaults({ lane: "browser", runner: "codex", splitRatio: 0.9 }, "SwiftUI", ["ios"]);
assert.equal(rejected.lane, "device", "an incompatible saved lane must never be silently launched");
assert.equal(rejected.runner, "codex");
assert.equal(rejected.splitRatio, 0.5, "persisted ratios are bounded so the lane cannot bury SSH");
assert.equal(resolveStudioDefaults(null, "Expo", ["mobile"]).splitRatio, DEFAULT_STUDIO_DEFAULTS.splitRatio);

console.log("Studio workspace capability contract ok");
