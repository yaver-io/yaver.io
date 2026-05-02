"use client";

import { useCallback, useEffect, useMemo, useState } from "react";

type Todo = {
  id: string;
  text: string;
  done: boolean;
  createdAt: number;
};

type Filter = "all" | "active" | "completed";

const STORAGE_KEY = "@todo-web/items/v1";

export default function Home() {
  const [items, setItems] = useState<Todo[]>([]);
  const [draft, setDraft] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [hydrated, setHydrated] = useState(false);

  // localStorage on mount. Wrapped in a try/catch because Safari
  // private mode throws on access — silently degrade to in-memory
  // todos rather than crashing the page.
  useEffect(() => {
    try {
      const raw = window.localStorage.getItem(STORAGE_KEY);
      if (raw) {
        const parsed = JSON.parse(raw);
        if (Array.isArray(parsed)) setItems(parsed as Todo[]);
      }
    } catch {}
    setHydrated(true);
  }, []);

  useEffect(() => {
    if (!hydrated) return;
    try {
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(items));
    } catch {}
  }, [items, hydrated]);

  const visible = useMemo(() => {
    switch (filter) {
      case "active":
        return items.filter((t) => !t.done);
      case "completed":
        return items.filter((t) => t.done);
      default:
        return items;
    }
  }, [items, filter]);

  const remaining = items.filter((t) => !t.done).length;

  const add = useCallback(() => {
    const text = draft.trim();
    if (!text) return;
    setItems((prev) => [
      {
        id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
        text,
        done: false,
        createdAt: Date.now(),
      },
      ...prev,
    ]);
    setDraft("");
  }, [draft]);

  const toggle = useCallback((id: string) => {
    setItems((prev) => prev.map((t) => (t.id === id ? { ...t, done: !t.done } : t)));
  }, []);

  const remove = useCallback((id: string) => {
    setItems((prev) => prev.filter((t) => t.id !== id));
  }, []);

  const clearCompleted = useCallback(() => {
    setItems((prev) => prev.filter((t) => !t.done));
  }, []);

  return (
    <main className="page">
      <header className="header">
        <h1 className="title">Todo</h1>
        <p className="subtitle">{remaining === 0 ? "All clear" : `${remaining} left`}</p>
      </header>

      <form
        className="composer"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <input
          className="input"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="What needs doing?"
          autoComplete="off"
        />
        <button type="submit" className="add-btn" disabled={!draft.trim()}>
          Add
        </button>
      </form>

      <div className="filter-row">
        {(["all", "active", "completed"] as Filter[]).map((f) => (
          <button
            key={f}
            onClick={() => setFilter(f)}
            className={`chip ${filter === f ? "chip-on" : ""}`}
          >
            {f[0].toUpperCase() + f.slice(1)}
          </button>
        ))}
        <span className="spacer" />
        {items.some((t) => t.done) ? (
          <button onClick={clearCompleted} className="clear-btn">
            Clear done
          </button>
        ) : null}
      </div>

      <ul className="list">
        {visible.length === 0 ? (
          <li className="empty">
            <strong>{filter === "completed" ? "Nothing done yet" : "Nothing here"}</strong>
            <span>
              {filter === "completed"
                ? "Tick a todo to see it here."
                : "Add your first todo above."}
            </span>
          </li>
        ) : (
          visible.map((item) => (
            <li
              key={item.id}
              className="item"
              onClick={() => toggle(item.id)}
            >
              <span className={`checkbox ${item.done ? "checkbox-on" : ""}`}>
                {item.done ? "✓" : ""}
              </span>
              <span className={`item-text ${item.done ? "item-text-done" : ""}`}>
                {item.text}
              </span>
              <button
                onClick={(e) => {
                  e.stopPropagation();
                  remove(item.id);
                }}
                className="remove-btn"
                aria-label="Delete todo"
              >
                ×
              </button>
            </li>
          ))
        )}
      </ul>

      <style jsx>{`
        .page {
          max-width: 560px;
          margin: 0 auto;
          padding: 32px 16px 64px;
        }
        .header {
          padding: 0 8px 12px;
        }
        .title {
          margin: 0;
          font-size: 36px;
          font-weight: 800;
          letter-spacing: -0.5px;
        }
        .subtitle {
          margin: 4px 0 0;
          color: var(--fg-muted);
          font-size: 14px;
        }
        .composer {
          display: flex;
          gap: 8px;
          padding: 8px;
        }
        .input {
          flex: 1;
          background: var(--bg-card);
          color: var(--fg);
          padding: 14px 16px;
          border-radius: 12px;
          border: 1px solid var(--border);
          outline: none;
        }
        .input:focus {
          border-color: var(--accent);
        }
        .add-btn {
          padding: 14px 18px;
          border-radius: 12px;
          background: var(--accent);
          color: var(--bg);
          font-weight: 700;
          min-width: 72px;
        }
        .add-btn:disabled {
          background: var(--bg-card);
          color: var(--fg-faint);
          cursor: not-allowed;
        }
        .filter-row {
          display: flex;
          align-items: center;
          gap: 8px;
          padding: 10px 8px;
        }
        .spacer {
          flex: 1;
        }
        .chip {
          padding: 6px 12px;
          border-radius: 999px;
          border: 1px solid var(--border);
          color: var(--fg-muted);
          font-size: 13px;
          font-weight: 600;
        }
        .chip-on {
          background: rgba(34, 197, 94, 0.13);
          border-color: var(--accent);
          color: var(--accent);
        }
        .clear-btn {
          padding: 6px 12px;
          border-radius: 999px;
          border: 1px solid rgba(239, 68, 68, 0.4);
          background: rgba(239, 68, 68, 0.08);
          color: #f87171;
          font-size: 13px;
          font-weight: 600;
        }
        .list {
          list-style: none;
          padding: 0 8px;
          margin: 0;
        }
        .item {
          display: flex;
          align-items: center;
          gap: 12px;
          background: var(--bg-card);
          padding: 14px;
          border-radius: 12px;
          margin-bottom: 8px;
          cursor: pointer;
        }
        .checkbox {
          width: 22px;
          height: 22px;
          border-radius: 6px;
          border: 1.5px solid var(--fg-faint);
          display: inline-flex;
          align-items: center;
          justify-content: center;
          color: var(--bg);
          font-weight: 900;
          font-size: 14px;
        }
        .checkbox-on {
          background: var(--accent);
          border-color: var(--accent);
        }
        .item-text {
          flex: 1;
          font-size: 16px;
        }
        .item-text-done {
          color: var(--fg-faint);
          text-decoration: line-through;
        }
        .remove-btn {
          color: var(--fg-faint);
          font-size: 22px;
          padding: 0 8px;
          line-height: 1;
        }
        .remove-btn:hover {
          color: var(--danger);
        }
        .empty {
          padding: 80px 16px;
          text-align: center;
          display: flex;
          flex-direction: column;
          gap: 6px;
          color: var(--fg-muted);
          list-style: none;
        }
        .empty strong {
          color: var(--fg);
          font-size: 18px;
        }
      `}</style>
    </main>
  );
}
