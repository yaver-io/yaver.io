/**
 * Guard: the model dropdown must SEED a selection, never FIGHT the user for it.
 *
 * ── The deadlock (2026-08-02) ──────────────────────────────────────────────
 *
 * RuntimeLabView's seeding effect called setSelectedModel(explicitModel)
 * unconditionally whenever the machine had a saved model — with `selectedModel`
 * in its own dependency array. Picking anything else re-ran the effect, saw the
 * saved default still present, and snapped the value back within a frame.
 *
 * The model was therefore UNCHANGEABLE on any machine with a saved default, and
 * the only route to changing the saved default was to select a different model
 * first. A closed loop with no exit — the owner could not move off a `gpt-5.4`
 * that his ChatGPT-account Codex login rejects outright.
 *
 * This is a source-level guard because the bug is in an effect's control flow,
 * not in a pure function: the shape that broke it (an unconditional set gated on
 * nothing, with selectedModel as a dependency) is exactly what must not return.
 *
 * Run: npx tsx web/lib/modelSelectSeeding.test.ts
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const src = readFileSync(join(here, "../components/dashboard/RuntimeLabView.tsx"), "utf8");

let failures = 0;
const ok = (cond: unknown, label: string) => {
  if (cond) console.log(`ok   ${label}`);
  else { console.error(`FAIL ${label}`); failures++; }
};

// Isolate the seeding effect: from the seed-key ref to the end of that effect.
const start = src.indexOf("const modelSeedKeyRef");
ok(start > 0, "the seeding effect is keyed by a (device, runner) ref");

const effect = start > 0 ? src.slice(start, start + 2200) : "";

// THE FIX: a still-valid selection must short-circuit before any set runs.
ok(/if \(alreadySeeded && currentIsValid\) return;/.test(effect),
  "a valid selection on the same device+runner returns EARLY — the user's pick is never overwritten");

// The unconditional set is what created the deadlock. It must be guarded.
const setsExplicit = /setSelectedModel\(explicitModel\);/.test(effect);
ok(setsExplicit, "it still seeds from the machine default when there is nothing valid selected");
ok(/alreadySeeded/.test(effect) && effect.indexOf("alreadySeeded") < effect.indexOf("setSelectedModel(explicitModel)"),
  "…but only AFTER the already-seeded check, never unconditionally");

// The fallback branch must not clobber a valid pick either.
ok(/if \(!currentIsValid\) \{\s*\n\s*setSelectedModel\(availableModels\.find/.test(effect),
  "the default-model fallback only fires when the current pick is invalid");

// An empty catalogue must not blank an existing selection.
ok(/if \(!availableModels\.length\) return;/.test(effect),
  "an empty model list leaves the current selection alone rather than clearing it");

// Re-seeding on a real context change must still work, or switching machines
// would silently keep the previous box's model — a false green of its own.
ok(/modelSeedKeyRef\.current = seedKey;/.test(effect),
  "the seed key is updated so a device/runner change RE-seeds");
ok(/\$\{connectedDevice\?\.id \|\| ""\}\|\$\{normalizeRunnerId\(selectedRunner\)\}/.test(effect),
  "the seed key spans BOTH device and runner — switching either re-seeds");

// NO FALSE RED: the dropdown itself must remain a plain controlled select.
// If someone 'fixes' this by disabling the control, the user still cannot pick.
const selectBlock = src.slice(src.indexOf("ref={modelSelectRef}"), src.indexOf("ref={modelSelectRef}") + 600);
ok(/onChange=\{\(event\) => \{[\s\S]{0,360}?setSelectedModel\(nextId\);/.test(selectBlock),
  "the select still writes the user's choice straight through");
ok(!/disabled/.test(selectBlock),
  "the model select is NOT disabled — a greyed-out control is the same dead end wearing a different hat");

if (failures) {
  console.error(`\nmodelSelectSeeding: ${failures} FAILED — the model dropdown can fight the user again`);
  process.exitCode = 1;
} else {
  console.log("\nmodelSelectSeeding: ALL PASS");
}
