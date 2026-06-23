// designchat-test — verifies the REAL designChat.ts logic (the GLM AI path's
// browser half): prompt → gateway call → parse → sanitize → DesignPatch[]. It
// bundles the actual module with esbuild and stubs ONLY the transport (fetch),
// so every line except the z.ai upstream is exercised. The upstream call itself
// is the gateway worker's responsibility (its own tests + the beta GLM key).
//
// Run: cd web && node scripts/designchat-test.mjs

import { build } from "esbuild";
import { writeFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

let fails = 0;
const check = (name, cond) => { console.log((cond ? "  ✅ " : "  ❌ ") + name); if (!cond) fails++; };

// Bundle the real designChat.ts (its only runtime dep is ./gateway; the
// @/lib/agent-client import is type-only and erased by esbuild).
const out = await build({
  entryPoints: ["lib/sandbox/designChat.ts"],
  bundle: true,
  format: "esm",
  platform: "node",
  write: false,
  logLevel: "silent",
});
const dir = mkdtempSync(join(tmpdir(), "dc-"));
const file = join(dir, "designChat.mjs");
writeFileSync(file, out.outputFiles[0].text);

// Configure the gateway + stub the transport.
process.env.NEXT_PUBLIC_YAVER_GATEWAY_URL = "https://stub.gateway";
let lastReq = null;
globalThis.fetch = async (url, init) => {
  lastReq = { url, init };
  // The model "replies" with patches wrapped in a markdown fence (designChat
  // must strip it) and includes one UNKNOWN node id (must be dropped) and an
  // out-of-range marginTop (must be clamped to 0-64).
  const content =
    "```json\n" +
    JSON.stringify([
      { op: "set", nodeId: "quickadd", props: { marginTop: 999, hidden: true } },
      { op: "move", nodeId: "quickadd", beforeId: "list" },
      { op: "enable", nodeId: "list", affordance: "reorder" },
      { op: "set", nodeId: "definitely_not_a_node", props: { marginTop: 8 } },
      { op: "bogus", nodeId: "title" },
    ]) +
    "\n```";
  return { ok: true, status: 200, json: async () => ({ choices: [{ message: { content } }] }) };
};

const { draftDesignPatch } = await import(file);

try {
  console.log("• designChat: prompt → gateway → parse → sanitize");
  const patches = await draftDesignPatch("move quick-add below the list and let users reorder", "fake-session-token");

  check("request hit the gateway /v1/chat/completions", String(lastReq?.url).endsWith("/v1/chat/completions"));
  check("request carried the session token as Bearer", String(lastReq?.init?.headers?.Authorization) === "Bearer fake-session-token");

  check("markdown fence stripped + JSON parsed", Array.isArray(patches));
  check("unknown node id dropped (definitely_not_a_node)", !patches.some((p) => p.nodeId === "definitely_not_a_node"));
  check("bogus op dropped", !patches.some((p) => p.op === "bogus"));
  check("3 valid patches kept", patches.length === 3);

  const setP = patches.find((p) => p.op === "set" && p.nodeId === "quickadd");
  check("set patch preserved (quickadd)", !!setP);
  check("marginTop clamped to 64", setP?.props?.marginTop === 64);
  check("hidden preserved", setP?.props?.hidden === true);

  const moveP = patches.find((p) => p.op === "move");
  check("move patch preserved (quickadd before list)", moveP?.nodeId === "quickadd" && moveP?.beforeId === "list");

  const enP = patches.find((p) => p.op === "enable");
  check("enable patch preserved (list reorder)", enP?.nodeId === "list" && enP?.affordance === "reorder");

  // Scoped (select-then-prompt): passing a selected node must inject a system
  // message naming it, so "this" resolves to that widget.
  await draftDesignPatch("hide this", "tok", { nodeId: "quickadd", kind: "Quick add" });
  const sentBody = JSON.parse(lastReq.init.body);
  const sysText = sentBody.messages.filter((m) => m.role === "system").map((m) => m.content).join("\n");
  check("scoped prompt injects the selected nodeId context", sysText.includes('nodeId="quickadd"'));
  check("scoped prompt names the selected kind", sysText.includes("Quick add"));

  // Error path: model returns prose → must throw, not crash.
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => ({ choices: [{ message: { content: "Sure! I moved it." } }] }) });
  let threw = false;
  try { await draftDesignPatch("x", "tok"); } catch { threw = true; }
  check("non-JSON model reply rejected (throws)", threw);

  console.log("\n" + (fails === 0 ? "ALL DESIGNCHAT CHECKS PASSED ✅" : fails + " FAILED ❌"));
} catch (e) {
  console.error("SCRIPT ERROR:", e);
  fails++;
}
process.exit(fails === 0 ? 0 : 1);
