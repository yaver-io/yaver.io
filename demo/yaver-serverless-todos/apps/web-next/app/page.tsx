"use client";

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { YaverServerlessTodoClient } from "../../../packages/js-client/src/index.js";

type Todo = {
  id: string;
  title: string;
  done: boolean;
  createdAt: string;
};

type Config = {
  baseUrl: string;
  slug: string;
  token: string;
};

const defaultConfig: Config = {
  baseUrl: process.env.NEXT_PUBLIC_YAVER_SERVERLESS_URL || "http://127.0.0.1:18080",
  slug: process.env.NEXT_PUBLIC_YAVER_SERVERLESS_SLUG || "yaver-serverless-todo",
  token: process.env.NEXT_PUBLIC_YAVER_SERVERLESS_TOKEN || "",
};

export default function Home() {
  const [config, setConfig] = useState(defaultConfig);
  const [todos, setTodos] = useState<Todo[]>([]);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const client = useMemo(() => new YaverServerlessTodoClient(config), [config]);
  const remaining = todos.filter((todo) => !todo.done).length;

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      setTodos(await client.listTodos());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function add(e: FormEvent) {
    e.preventDefault();
    const title = draft.trim();
    if (!title) return;
    setDraft("");
    await client.createTodo(title).then(refresh).catch((err: Error) => setError(err.message));
  }

  async function toggle(todo: Todo) {
    await client.setTodoDone(todo.id, !todo.done).then(refresh).catch((err: Error) => setError(err.message));
  }

  async function remove(todo: Todo) {
    await client.deleteTodo(todo.id).then(refresh).catch((err: Error) => setError(err.message));
  }

  return (
    <main className="page">
      <section className="toolbar">
        <div>
          <h1>Yaver Serverless Todo</h1>
          <p>{loading ? "Syncing..." : remaining === 0 ? "All clear" : `${remaining} open tasks`}</p>
        </div>
        <button onClick={refresh}>Refresh</button>
      </section>

      <section className="settings" aria-label="Backend settings">
        <input
          aria-label="Yaver Serverless URL"
          value={config.baseUrl}
          onChange={(e) => setConfig((prev) => ({ ...prev, baseUrl: e.target.value }))}
        />
        <input
          aria-label="Project slug"
          value={config.slug}
          onChange={(e) => setConfig((prev) => ({ ...prev, slug: e.target.value }))}
        />
        <input
          aria-label="Project API token"
          type="password"
          placeholder="pp_ project token"
          value={config.token}
          onChange={(e) => setConfig((prev) => ({ ...prev, token: e.target.value }))}
        />
      </section>

      {error ? <p className="error">{error}</p> : null}

      <form className="composer" onSubmit={add}>
        <input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="What needs doing?" />
        <button disabled={!draft.trim()}>Add</button>
      </form>

      <ul className="list">
        {todos.map((todo) => (
          <li key={todo.id}>
            <button className={todo.done ? "check on" : "check"} onClick={() => toggle(todo)}>
              {todo.done ? "✓" : ""}
            </button>
            <span className={todo.done ? "done" : ""}>{todo.title}</span>
            <button className="delete" onClick={() => remove(todo)}>Delete</button>
          </li>
        ))}
        {!todos.length && !loading ? <li className="empty">No serverless todos yet.</li> : null}
      </ul>

      <style jsx>{`
        .page {
          width: min(760px, calc(100vw - 32px));
          margin: 0 auto;
          padding: 32px 0 72px;
        }
        .toolbar {
          display: flex;
          align-items: center;
          justify-content: space-between;
          gap: 16px;
          padding: 0 0 20px;
        }
        h1 {
          margin: 0;
          font-size: 32px;
          line-height: 1.1;
        }
        p {
          margin: 6px 0 0;
          color: var(--muted);
        }
        .toolbar button,
        .composer button {
          min-height: 42px;
          border-radius: 8px;
          padding: 0 16px;
          background: var(--accent);
          color: white;
          font-weight: 700;
        }
        .settings {
          display: grid;
          grid-template-columns: 1.4fr 1fr 1fr;
          gap: 8px;
          margin-bottom: 16px;
        }
        input {
          width: 100%;
          min-height: 44px;
          border: 1px solid var(--line);
          border-radius: 8px;
          padding: 0 12px;
          background: var(--panel);
          color: var(--fg);
        }
        .error {
          padding: 12px;
          border: 1px solid #fed7aa;
          background: #fff7ed;
          color: var(--danger);
          border-radius: 8px;
        }
        .composer {
          display: grid;
          grid-template-columns: 1fr auto;
          gap: 8px;
          margin-bottom: 16px;
        }
        .composer button:disabled {
          background: #b8c1cc;
          cursor: not-allowed;
        }
        .list {
          list-style: none;
          margin: 0;
          padding: 0;
          display: grid;
          gap: 8px;
        }
        li {
          min-height: 56px;
          display: grid;
          grid-template-columns: 36px 1fr auto;
          align-items: center;
          gap: 12px;
          border: 1px solid var(--line);
          border-radius: 8px;
          background: var(--panel);
          padding: 8px 12px;
        }
        .check {
          width: 28px;
          height: 28px;
          border: 1px solid var(--line);
          border-radius: 50%;
          background: white;
          color: white;
        }
        .check.on {
          background: var(--accent);
          border-color: var(--accent);
        }
        .done {
          color: var(--muted);
          text-decoration: line-through;
        }
        .delete {
          background: transparent;
          color: var(--danger);
          font-weight: 700;
        }
        .empty {
          display: block;
          color: var(--muted);
        }
        @media (max-width: 720px) {
          .toolbar,
          .settings,
          .composer {
            grid-template-columns: 1fr;
            display: grid;
          }
        }
      `}</style>
    </main>
  );
}
