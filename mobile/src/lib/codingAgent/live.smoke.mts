// live.smoke.mts — LIVE end-to-end smoke test of the agentic coding loop against
// the real GLM API. Skips when GLM_LIVE_KEY is unset, so it's safe to keep in
// the tree (no secret in the file; the key only ever comes from the env).
//
//   GLM_LIVE_KEY='<id.secret>' npx tsx src/lib/codingAgent/live.smoke.mts
//
// Seeds an in-memory sandbox with a tiny RN project, asks the agent to make a
// real multi-file change, and prints the tool trace + token usage so we can
// confirm GLM actually drives list/read/edit and stops cleanly.

import { runCodingAgent, defaultCodingAgentConfig } from "./runner.ts";
import type { CodingSandbox, CodingSandboxEntry } from "./sandboxTools.ts";

const KEY = process.env.GLM_LIVE_KEY?.trim();
if (!KEY) {
  console.log("⊘ GLM_LIVE_KEY unset — skipping live GLM smoke test.");
  process.exit(0);
}

function memSandbox(initial: Record<string, string>): CodingSandbox & { dump: () => Record<string, string> } {
  const files = new Map<string, string>(Object.entries(initial));
  return {
    async readFile(path) {
      if (!files.has(path)) throw new Error(`not found: ${path}`);
      return files.get(path)!;
    },
    async listFiles(): Promise<CodingSandboxEntry[]> {
      return [...files.entries()]
        .map(([path, content]) => ({ path, isDirectory: false, size: Buffer.byteLength(content) }))
        .sort((a, b) => a.path.localeCompare(b.path));
    },
    async writeFile(path, content) {
      files.set(path, content);
    },
    async deleteFile(path) {
      files.delete(path);
    },
    dump: () => Object.fromEntries(files),
  };
}

const box = memSandbox({
  "App.tsx":
    `import { Text, View } from "react-native";\n` +
    `import { greeting } from "./lib/greeting";\n\n` +
    `export default function App() {\n` +
    `  return (\n` +
    `    <View>\n` +
    `      <Text>{greeting("world")}</Text>\n` +
    `    </View>\n` +
    `  );\n` +
    `}\n`,
  "lib/greeting.ts": `export function greeting(name: string) {\n  return "Hello, " + name;\n}\n`,
});

const prompt =
  "Add an exclamation mark to the greeting so it returns 'Hello, <name>!', and " +
  "update App so it greets 'Yaver' instead of 'world'. Keep everything else.";

console.log("-> running agentic loop on GLM (glm-4.7)...\n");
const t0 = Date.now();
const res = await runCodingAgent({
  prompt,
  sandbox: box,
  config: defaultCodingAgentConfig(KEY),
  onProgress: (e) => {
    if (e.kind === "tool_call") {
      const c = e.call;
      const arg =
        c.name === "read_file" || c.name === "list_files"
          ? JSON.stringify(c.args)
          : c.name === "grep"
            ? JSON.stringify(c.args)
            : `{path:${(c.args as any)?.path}}`;
      const tag = c.error ? "✗" : c.denied ? "⊘" : "✓";
      console.log(`  ${tag} ${c.name} ${arg}`);
    }
  },
});

console.log("\n── result ───────────────────────────────");
console.log("final:", res.finalText);
console.log("steps:", res.steps, "| mutated:", res.mutatedPaths.join(", ") || "(none)");
console.log("tokens:", res.inputTokens, "in /", res.outputTokens, "out | hitMaxSteps:", res.hitMaxSteps);
console.log("ms:", Date.now() - t0);

console.log("\n── final tree ───────────────────────────");
for (const [p, content] of Object.entries(box.dump())) {
  console.log(`\n--- ${p} ---\n${content}`);
}

// Lightweight assertions so the smoke test fails loudly if GLM didn't do the job.
const greeting = box.dump()["lib/greeting.ts"] ?? "";
const app = box.dump()["App.tsx"] ?? "";
const ok = greeting.includes("!") && app.includes("Yaver");
console.log("\n", ok ? "✅ live smoke PASSED" : "❌ live smoke FAILED (expected '!' in greeting + 'Yaver' in App)");
process.exit(ok ? 0 : 1);
