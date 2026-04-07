"use client";

import { useState, useEffect } from "react";
import { agentClient } from "@/lib/agent-client";

interface TodoItem { id: string; description: string; status: string; }

export default function TodosView({ onTaskCreated }: { onTaskCreated?: (taskId: string) => void }) {
  const [todos, setTodos] = useState<TodoItem[]>([]);
  const [input, setInput] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => { loadTodos(); }, []);

  async function loadTodos() {
    setLoading(true);
    try { setTodos(await agentClient.listTodos()); } catch {}
    setLoading(false);
  }

  async function addTodo() {
    if (!input.trim()) return;
    await agentClient.addTodo(input.trim());
    setInput("");
    loadTodos();
  }

  async function deleteTodo(id: string) {
    await agentClient.deleteTodo(id);
    loadTodos();
  }

  async function runAutopilot() {
    const pending = todos.filter(t => t.status !== "done");
    if (!pending.length) return;
    try {
      const task = await agentClient.sendTask(
        "Autopilot: implement todos",
        `Implement these todos one by one:\n${pending.map((t, i) => `${i + 1}. ${t.description}`).join("\n")}\n\nWork through them sequentially. Show what you did for each.`
      );
      onTaskCreated?.(task.id);
    } catch {}
  }

  return (
    <div className="space-y-4">
      <div className="flex gap-2">
        <input value={input} onChange={(e) => setInput(e.target.value)} onKeyDown={(e) => e.key === "Enter" && addTodo()}
          placeholder="Add a todo..." className="flex-1 rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 placeholder-surface-500 outline-none focus:border-indigo-500" />
        <button onClick={addTodo} className="px-4 py-2 text-sm rounded-lg bg-indigo-500 text-white hover:bg-indigo-400">Add</button>
      </div>

      {todos.filter(t => t.status !== "done").length > 0 && (
        <button onClick={runAutopilot} className="text-xs px-3 py-1 rounded-md bg-amber-500/10 text-amber-400 hover:bg-amber-500/20">
          Autopilot: implement all pending
        </button>
      )}

      {loading ? (
        <div className="text-center py-8 text-surface-500 text-sm">Loading...</div>
      ) : todos.length === 0 ? (
        <div className="text-center py-8 text-surface-500 text-sm">No todos yet</div>
      ) : (
        <div className="space-y-1">
          {todos.map((t) => (
            <div key={t.id} className="rounded-lg border border-surface-800 bg-surface-900/50 p-3 flex items-center gap-3">
              <span className={`text-xs px-2 py-0.5 rounded-full ${t.status === "done" ? "bg-emerald-500/10 text-emerald-400" : "bg-amber-500/10 text-amber-400"}`}>{t.status}</span>
              <span className="flex-1 text-sm truncate">{t.description}</span>
              <button onClick={() => deleteTodo(t.id)} className="text-surface-600 hover:text-red-400 text-xs">&#x2715;</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
