// llm-client.test.ts — pins the BYOK LLM client contract from
// mobile/src/lib/{llmClient,llmAnthropic}.ts. Slice 3 of the
// phone-first dev stack.
//
// Coverage:
//   1. applyEditPlan walks the plan and writes through ApplyTarget,
//      partial-success on per-edit failures.
//   2. assertRequestSize rejects oversized requests up front.
//   3. createAnthropicProvider sends a properly-shaped Messages API
//      request (right URL, right headers, right body, right tool).
//   4. Provider parses tool-use responses into EditPlan.
//   5. Provider tolerates a no-tool-call response (text-only) by
//      surfacing the text as rationale rather than throwing.
//   6. Auth failure (401) bubbles up with the response body.
//   7. Timeout fires when the mock fetch hangs.
//   8. Format helper renders rationale + per-edit lines.

import { afterEach, beforeEach, describe, expect, it } from "bun:test";
import {
  applyEditPlan,
  assertRequestSize,
  formatEditPlan,
  type ApplyTarget,
  type EditPlan,
  type FileEdit,
} from "@yaver/mobile-lib/llmClient";
import { createAnthropicProvider } from "@yaver/mobile-lib/llmAnthropic";

// ---- applyEditPlan --------------------------------------------------

describe("applyEditPlan", () => {
  function newMockTarget() {
    const writes: Array<{ slug: string; relPath: string; content: string }> = [];
    const deletes: Array<{ slug: string; relPath: string }> = [];
    const target: ApplyTarget = {
      async writeSourceFile(slug, relPath, content) {
        writes.push({ slug, relPath, content });
      },
      async deleteSourceFile(slug, relPath) {
        deletes.push({ slug, relPath });
      },
    };
    return { target, writes, deletes };
  }

  it("applies create + update + delete in order", async () => {
    const { target, writes, deletes } = newMockTarget();
    const plan: EditPlan = {
      rationale: "ok",
      edits: [
        { action: "create", path: "App.tsx", content: "export default () => null;" },
        { action: "update", path: "screens/Home.tsx", content: "// updated" },
        { action: "delete", path: "old.tsx" },
      ],
    };
    const r = await applyEditPlan("todo", plan, target);
    expect(r.applied.length).toBe(3);
    expect(r.skipped.length).toBe(0);
    expect(writes.map((w) => w.relPath)).toEqual(["App.tsx", "screens/Home.tsx"]);
    expect(deletes.map((d) => d.relPath)).toEqual(["old.tsx"]);
  });

  it("skips create/update edits missing 'content' but applies the rest", async () => {
    const { target, writes } = newMockTarget();
    const plan: EditPlan = {
      rationale: "",
      edits: [
        { action: "create", path: "missing-content.tsx" /* no content */ },
        { action: "create", path: "fine.tsx", content: "ok" },
      ],
    };
    const r = await applyEditPlan("todo", plan, target);
    expect(r.applied.length).toBe(1);
    expect(r.skipped.length).toBe(1);
    expect(r.skipped[0].reason).toMatch(/missing 'content'/);
    expect(writes.map((w) => w.relPath)).toEqual(["fine.tsx"]);
  });

  it("captures store-side errors (e.g. unsafe path) as skipped, not thrown", async () => {
    const target: ApplyTarget = {
      async writeSourceFile() {
        throw new Error("UnsafeSourcePathError: ../etc");
      },
      async deleteSourceFile() {
        throw new Error("nope");
      },
    };
    const plan: EditPlan = {
      rationale: "x",
      edits: [
        { action: "create", path: "../etc/passwd", content: "boom" },
        { action: "delete", path: "../etc/shadow" },
      ],
    };
    const r = await applyEditPlan("todo", plan, target);
    expect(r.applied.length).toBe(0);
    expect(r.skipped.length).toBe(2);
    expect(r.skipped[0].reason).toMatch(/UnsafeSourcePathError/);
  });

  it("rejects unknown actions", async () => {
    const { target } = newMockTarget();
    const plan: EditPlan = {
      rationale: "",
      edits: [
        { action: "rename" as unknown as FileEdit["action"], path: "App.tsx", content: "x" },
      ],
    };
    const r = await applyEditPlan("todo", plan, target);
    expect(r.skipped.length).toBe(1);
    expect(r.skipped[0].reason).toMatch(/unknown action/);
  });
});

// ---- assertRequestSize ----------------------------------------------

describe("assertRequestSize", () => {
  it("returns the byte count for in-budget requests", () => {
    const got = assertRequestSize({
      prompt: "hello",
      files: [{ path: "a", content: "x" }, { path: "b", content: "yy" }],
    });
    expect(got).toBe(8); // 5 + 1 + 2
  });

  it("throws for over-budget requests", () => {
    const big = "a".repeat(250_000);
    expect(() =>
      assertRequestSize({
        prompt: "p",
        files: [{ path: "big", content: big }],
      }),
    ).toThrow(/exceeds 200000 bytes/);
  });

  it("supports custom maxBytes", () => {
    expect(() =>
      assertRequestSize({ prompt: "x".repeat(101), files: [] }, 100),
    ).toThrow(/exceeds 100 bytes/);
  });
});

// ---- formatEditPlan -------------------------------------------------

describe("formatEditPlan", () => {
  it("renders rationale, per-edit lines, and token counts", () => {
    const out = formatEditPlan({
      rationale: "Adding a new screen.",
      edits: [
        { action: "create", path: "screens/Home.tsx", content: "abc", reason: "main entry" },
        { action: "delete", path: "old.tsx" },
      ],
      inputTokens: 1234,
      outputTokens: 567,
    });
    expect(out).toContain("Adding a new screen.");
    expect(out).toContain("CREATE screens/Home.tsx (3b)");
    expect(out).toContain("  main entry");
    expect(out).toContain("DELETE old.tsx (-)");
    expect(out).toContain("tokens: 1234 in, 567 out");
  });
});

// ---- createAnthropicProvider ----------------------------------------

interface MockFetchCall {
  url: string;
  init: RequestInit | undefined;
}

function newMockFetch(handler: (call: MockFetchCall) => Response | Promise<Response>) {
  const calls: MockFetchCall[] = [];
  // Cast through unknown: Bun's `typeof fetch` carries a `preconnect`
  // static the mock doesn't implement, and the call sites only ever
  // invoke it as a plain function.
  const f = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : (input as URL).toString();
    const call: MockFetchCall = { url, init };
    calls.push(call);
    return handler(call);
  }) as unknown as typeof fetch;
  return { fetch: f, calls };
}

function jsonRes(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("createAnthropicProvider", () => {
  it("rejects empty apiKey at construction", () => {
    expect(() =>
      createAnthropicProvider({ apiKey: "", fetchImpl: fetch }),
    ).toThrow(/apiKey is required/);
  });

  it("sends a Messages API request with auth + version headers and forces apply_edits tool use", async () => {
    const mock = newMockFetch(() =>
      jsonRes(200, {
        id: "msg_x",
        model: "claude-opus-4-7",
        content: [
          {
            type: "tool_use",
            id: "tu_1",
            name: "apply_edits",
            input: { rationale: "ok", edits: [] },
          },
        ],
        usage: { input_tokens: 10, output_tokens: 5 },
      }),
    );
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-test",
      fetchImpl: mock.fetch,
    });
    await provider.editFiles({
      prompt: "hi",
      files: [{ path: "App.tsx", content: "x" }],
      framework: "expo",
    });
    expect(mock.calls.length).toBe(1);
    const call = mock.calls[0];
    expect(call.url).toBe("https://api.anthropic.com/v1/messages");
    const headers = call.init?.headers as Record<string, string>;
    expect(headers["x-api-key"]).toBe("sk-ant-test");
    expect(headers["anthropic-version"]).toBe("2023-06-01");
    expect(headers["anthropic-dangerous-direct-browser-access"]).toBe("true");
    const body = JSON.parse(String(call.init?.body));
    expect(body.model).toBe("claude-opus-4-7");
    expect(body.tool_choice).toEqual({ type: "tool", name: "apply_edits" });
    expect(body.tools[0].name).toBe("apply_edits");
    expect(body.system).toContain("phone-authored project");
    expect(body.messages[0].content).toContain("Request:\nhi");
    expect(body.messages[0].content).toContain("App.tsx");
  });

  it("parses a tool-use response into a typed EditPlan", async () => {
    const mock = newMockFetch(() =>
      jsonRes(200, {
        id: "msg_x",
        model: "claude-opus-4-7",
        content: [
          {
            type: "tool_use",
            id: "tu_1",
            name: "apply_edits",
            input: {
              rationale: "Created App.tsx.",
              edits: [
                {
                  action: "create",
                  path: "App.tsx",
                  content: "export default () => null;",
                  reason: "entry",
                },
                { action: "delete", path: "old.tsx" },
              ],
            },
          },
        ],
        usage: { input_tokens: 100, output_tokens: 30 },
      }),
    );
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-test",
      fetchImpl: mock.fetch,
    });
    const plan = await provider.editFiles({
      prompt: "make me an app",
      files: [],
    });
    expect(plan.rationale).toBe("Created App.tsx.");
    expect(plan.edits.length).toBe(2);
    expect(plan.edits[0]).toMatchObject({ action: "create", path: "App.tsx" });
    expect(plan.edits[1]).toMatchObject({ action: "delete", path: "old.tsx" });
    expect(plan.inputTokens).toBe(100);
    expect(plan.outputTokens).toBe(30);
  });

  it("falls back to text-only rationale when the model skipped the tool", async () => {
    const mock = newMockFetch(() =>
      jsonRes(200, {
        id: "msg_x",
        model: "claude-opus-4-7",
        content: [{ type: "text", text: "I don't have enough information." }],
        usage: { input_tokens: 5, output_tokens: 10 },
      }),
    );
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-test",
      fetchImpl: mock.fetch,
    });
    const plan = await provider.editFiles({ prompt: "x", files: [] });
    expect(plan.edits.length).toBe(0);
    expect(plan.rationale).toBe("I don't have enough information.");
  });

  it("surfaces 401 / 5xx errors with the response body", async () => {
    const mock = newMockFetch(() => jsonRes(401, { error: { message: "invalid api key" } }));
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-bogus",
      fetchImpl: mock.fetch,
    });
    let caught: unknown = null;
    try {
      await provider.editFiles({ prompt: "x", files: [] });
    } catch (e) {
      caught = e;
    }
    expect(String(caught)).toMatch(/Anthropic 401/);
    expect(String(caught)).toMatch(/invalid api key/);
  });

  it("aborts when timeoutMs elapses", async () => {
    // Mock fetch that resolves only when the AbortSignal fires —
    // mirrors what real fetch does. The provider's AbortController
    // fires after timeoutMs and we want the rejection to propagate.
    const mock = {
      calls: [] as MockFetchCall[],
      fetch: ((input: RequestInfo | URL, init?: RequestInit) => {
        mock.calls.push({
          url: typeof input === "string" ? input : (input as URL).toString(),
          init,
        });
        return new Promise<Response>((_, reject) => {
          const signal = init?.signal as AbortSignal | undefined;
          if (signal?.aborted) {
            reject(new DOMException("aborted", "AbortError"));
            return;
          }
          signal?.addEventListener("abort", () => {
            reject(new DOMException("aborted", "AbortError"));
          });
        });
      }) as typeof fetch,
    };
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-test",
      fetchImpl: mock.fetch,
    });
    let caught: unknown = null;
    try {
      await provider.editFiles({ prompt: "x", files: [], timeoutMs: 30 });
    } catch (e) {
      caught = e;
    }
    expect(caught).not.toBeNull();
    expect(String((caught as { name?: string })?.name ?? caught)).toMatch(/AbortError|aborted/);
  });

  it("respects model + maxTokens overrides", async () => {
    const mock = newMockFetch(() =>
      jsonRes(200, {
        id: "msg_x",
        model: "claude-haiku-4-5",
        content: [{ type: "tool_use", id: "tu_1", name: "apply_edits", input: { rationale: "", edits: [] } }],
      }),
    );
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-test",
      fetchImpl: mock.fetch,
      model: "claude-haiku-4-5",
      maxTokens: 256,
    });
    await provider.editFiles({ prompt: "x", files: [] });
    const body = JSON.parse(String(mock.calls[0].init?.body));
    expect(body.model).toBe("claude-haiku-4-5");
    expect(body.max_tokens).toBe(256);
  });

  it("respects baseUrl override (used by tests + relay paths)", async () => {
    const mock = newMockFetch(() =>
      jsonRes(200, {
        id: "msg_x",
        model: "claude-opus-4-7",
        content: [{ type: "tool_use", id: "tu_1", name: "apply_edits", input: { rationale: "", edits: [] } }],
      }),
    );
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-test",
      fetchImpl: mock.fetch,
      baseUrl: "http://127.0.0.1:9000/proxy",
    });
    await provider.editFiles({ prompt: "x", files: [] });
    expect(mock.calls[0].url).toBe("http://127.0.0.1:9000/proxy/messages");
  });
});

// ---- end-to-end: provider → applyEditPlan → ApplyTarget --------------

describe("end-to-end edit flow", () => {
  it("LLM response → applyEditPlan persists the right files", async () => {
    const mock = newMockFetch(() =>
      jsonRes(200, {
        id: "msg_x",
        model: "claude-opus-4-7",
        content: [
          {
            type: "tool_use",
            id: "tu_1",
            name: "apply_edits",
            input: {
              rationale: "Bootstrap todo app",
              edits: [
                { action: "create", path: "App.tsx", content: "export const App = () => null;" },
                { action: "create", path: "screens/Todos.tsx", content: "// todo list" },
              ],
            },
          },
        ],
        usage: { input_tokens: 50, output_tokens: 20 },
      }),
    );
    const provider = createAnthropicProvider({
      apiKey: "sk-ant-test",
      fetchImpl: mock.fetch,
    });
    const plan = await provider.editFiles({
      prompt: "make me a todo app",
      files: [],
    });

    const writes: Record<string, string> = {};
    const target: ApplyTarget = {
      async writeSourceFile(_slug, relPath, content) {
        writes[relPath] = content;
      },
      async deleteSourceFile() {
        // not needed
      },
    };
    const r = await applyEditPlan("todo", plan, target);

    expect(r.applied.length).toBe(2);
    expect(r.skipped.length).toBe(0);
    expect(writes["App.tsx"]).toBe("export const App = () => null;");
    expect(writes["screens/Todos.tsx"]).toBe("// todo list");
  });
});

// silence unused-import warnings on the bun:test fixtures we don't
// use directly here (afterEach / beforeEach kept available for
// future state-bearing tests).
beforeEach(() => {});
afterEach(() => {});
