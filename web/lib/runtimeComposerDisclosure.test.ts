/**
 * Runtime composer progressive-disclosure guard.
 *
 * The Studio conversation is the primary surface. Preview inspection,
 * project memory and MCP selection remain mounted so their observers keep
 * working, but their inventory must not permanently consume conversation
 * height. The desktop GUI renders this same dashboard, so this guards both.
 */
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

const source = readFileSync(join(import.meta.dirname, "../components/dashboard/RuntimeLabView.tsx"), "utf8");

test("Runtime composer hides secondary project, MCP and inspect controls behind an ellipsis", () => {
  assert.ok(source.includes('data-testid="runtime-composer-more"'), "composer has no options ellipsis");
  assert.ok(source.includes('data-testid="runtime-composer-options"'), "composer has no options panel");
  assert.match(
    source,
    /chatScopeControlsOpen\s*\?\s*"absolute"\s*:\s*"hidden"[\s\S]*?<ScreenContextChip[\s\S]*?<DomInspectChip[\s\S]*?>Project<[\s\S]*?latest project[\s\S]*?latest MCP[\s\S]*?includeYaverMcp/,
    "secondary composer controls escaped progressive disclosure",
  );
});

test("Runtime composer automatically chooses a real remote project when its old choice is unavailable", () => {
  assert.match(source, /!rows\.some\(\(row\) => row\.path === selectedPathRef\.current\)/);
  assert.match(source, /resolveRuntimeProjectPreference\(rows, saved\)[\s\S]*?\|\| rows\[0\]/);
  assert.ok(!source.includes('if (!next) setSelectedPath("")'), "disabling memory must not make a loaded composer project-less");
});
