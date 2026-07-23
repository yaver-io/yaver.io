export class YaverServerlessTodoClient {
  constructor(config) {
    if (!config || !config.baseUrl || !config.slug) {
      throw new Error("baseUrl and slug are required");
    }
    this.baseUrl = String(config.baseUrl).replace(/\/+$/, "");
    this.slug = encodeURIComponent(config.slug);
    this.token = config.token || "";
    this.fetchImpl = config.fetchImpl || globalThis.fetch;
    if (typeof this.fetchImpl !== "function") {
      throw new Error("fetch implementation is required");
    }
  }

  async listTodos(limit = 100) {
    const payload = await this.request(`/todos?limit=${limit}`, { method: "GET" });
    const rows = Array.isArray(payload.rows) ? payload.rows : [];
    return rows.map(normalizeTodo).sort((a, b) => b.createdAt.localeCompare(a.createdAt));
  }

  async createTodo(title, ownerId = "alice") {
    const trimmed = String(title || "").trim();
    if (!trimmed) throw new Error("title is required");
    const id = makeId();
    await this.request("/todos", {
      method: "POST",
      body: {
        id,
        title: trimmed,
        done: false,
        owner_id: ownerId,
      },
    });
    return id;
  }

  async setTodoDone(id, done) {
    await this.request(`/todos/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body: { done: Boolean(done) },
    });
  }

  async deleteTodo(id) {
    await this.request(`/todos/${encodeURIComponent(id)}`, { method: "DELETE" });
  }

  async request(path, init) {
    const headers = { Accept: "application/json" };
    if (init.body !== undefined) headers["Content-Type"] = "application/json";
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    const res = await this.fetchImpl(`${this.baseUrl}/data/${this.slug}${path}`, {
      method: init.method,
      headers,
      body: init.body === undefined ? undefined : JSON.stringify(init.body),
    });
    const text = await res.text();
    const json = text ? JSON.parse(text) : {};
    if (!res.ok) {
      throw new Error(json.error || json.message || `Yaver Serverless request failed: ${res.status}`);
    }
    return json;
  }
}

export function normalizeTodo(row) {
  return {
    id: String(row.id || ""),
    title: String(row.title || row.text || ""),
    done: row.done === true || row.done === 1 || row.done === "1" || row.done === "true",
    ownerId: String(row.owner_id || row.ownerId || ""),
    createdAt: String(row.created_at || row.createdAt || ""),
  };
}

function makeId() {
  if (globalThis.crypto && typeof globalThis.crypto.randomUUID === "function") {
    return globalThis.crypto.randomUUID();
  }
  return `todo-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}
