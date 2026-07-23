import assert from "node:assert/strict";
import test from "node:test";
import { YaverServerlessTodoClient, normalizeTodo } from "../src/index.js";

test("normalizes SQLite boolean variants", () => {
  assert.equal(normalizeTodo({ id: "a", title: "A", done: 1 }).done, true);
  assert.equal(normalizeTodo({ id: "b", title: "B", done: 0 }).done, false);
  assert.equal(normalizeTodo({ id: "c", title: "C", done: "true" }).done, true);
});

test("calls the Yaver Serverless data API", async () => {
  const calls = [];
  const client = new YaverServerlessTodoClient({
    baseUrl: "http://agent.local/",
    slug: "todo app",
    token: "pp_todo_app_placeholder",
    fetchImpl: async (url, init) => {
      calls.push({ url, init });
      return {
        ok: true,
        status: 200,
        text: async () => JSON.stringify({ rows: [{ id: "t1", title: "Ship", done: 0 }] }),
      };
    },
  });

  const rows = await client.listTodos();
  assert.equal(rows[0].title, "Ship");
  assert.equal(calls[0].url, "http://agent.local/data/todo%20app/todos?limit=100");
  assert.equal(calls[0].init.headers.Authorization, "Bearer pp_todo_app_placeholder");
});

test("rejects empty create title", async () => {
  const client = new YaverServerlessTodoClient({
    baseUrl: "http://agent.local",
    slug: "todo",
    fetchImpl: async () => {
      throw new Error("should not fetch");
    },
  });
  await assert.rejects(() => client.createTodo("   "), /title is required/);
});
