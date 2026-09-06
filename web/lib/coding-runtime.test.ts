import { runBrowserPrompt } from "./coding-runtime";

const workspace = { name: "test", branch: "main", files: { "src/App.tsx": "old" } };
const response = (message: unknown) => new Response(JSON.stringify({ choices: [{ message }] }), { status: 200, headers: { "content-type": "application/json" } });

async function check(name: string, fn: () => Promise<void>) {
  await fn();
  console.log(`ok ${name}`);
}

async function main() {
await check("audit mode rejects writes", async () => {
  const oldFetch = globalThis.fetch;
  let turn = 0;
  globalThis.fetch = (async () => turn++ === 0
    ? response({ role: "assistant", tool_calls: [{ id: "1", function: { name: "fs_write", arguments: JSON.stringify({ path: "src/App.tsx", content: "new" }) } }] })
    : response({ role: "assistant", content: "Audit complete." })) as typeof fetch;
  let changed = false;
  const answer = await runBrowserPrompt("deep-key", "deepseek", "deepseek-v4-flash", "audit", workspace, () => { changed = true; });
  globalThis.fetch = oldFetch;
  if (changed || !answer.includes("Audit")) throw new Error("audit mutated or did not finish");
});

await check("provider errors redact the key", async () => {
  const oldFetch = globalThis.fetch;
  globalThis.fetch = (async () => new Response(JSON.stringify({ error: { message: "invalid deep-key" } }), { status: 401 })) as typeof fetch;
  try {
    await runBrowserPrompt("deep-key", "deepseek", "deepseek-v4-flash", "audit", workspace, () => {});
    throw new Error("expected provider error");
  } catch (error) {
    if (String(error).includes("deep-key")) throw new Error("key leaked in provider error");
  } finally { globalThis.fetch = oldFetch; }
});
}

void main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
