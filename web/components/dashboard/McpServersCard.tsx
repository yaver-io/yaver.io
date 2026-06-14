"use client";

// Custom MCP Servers — register your own private MCPs (e.g. a yaver-bet on
// Hetzner) or anyone's public MCP, and use their tools from Yaver. CRUD against
// the agent's /mcp/servers registry via agentClient.
import { useCallback, useEffect, useState } from "react";
import { agentClient, type McpServer } from "@/lib/agent-client";

export default function McpServersCard({ connected }: { connected: boolean }) {
  const [servers, setServers] = useState<McpServer[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const [open, setOpen] = useState(false);
  const [orig, setOrig] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [testMsg, setTestMsg] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!connected) return;
    setLoading(true);
    setErr(null);
    try {
      setServers(await agentClient.listMcpServers());
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [connected]);

  useEffect(() => {
    load();
  }, [load]);

  const reset = () => {
    setOpen(false);
    setOrig(null);
    setName("");
    setUrl("");
    setToken("");
    setEnabled(true);
    setTestMsg(null);
  };

  const edit = (s: McpServer) => {
    setOrig(s.name);
    setName(s.name);
    setUrl(s.url);
    setToken("");
    setEnabled(s.enabled);
    setTestMsg(s.hasAuth ? "auth token kept (leave blank to keep)" : null);
    setOpen(true);
  };

  const test = async () => {
    if (!url.trim()) return;
    setBusy(true);
    setTestMsg("testing…");
    try {
      const r = await agentClient.testMcpServer({ name: name.trim(), url: url.trim(), auth_token: token || undefined });
      setTestMsg(r.ok ? `ok — ${r.toolCount ?? 0} tools` : `failed: ${r.error ?? "unreachable"}`);
    } catch (e) {
      setTestMsg(`failed: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!name.trim() || !url.trim()) {
      setTestMsg("name and URL are required");
      return;
    }
    setBusy(true);
    try {
      if (orig && orig !== name.trim()) await agentClient.deleteMcpServer(orig);
      await agentClient.saveMcpServer({ name: name.trim(), url: url.trim(), auth_token: token || undefined, enabled });
      reset();
      await load();
    } catch (e) {
      setTestMsg(`save failed: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      setBusy(false);
    }
  };

  const toggle = async (s: McpServer) => {
    try {
      await agentClient.saveMcpServer({ name: s.name, url: s.url, enabled: !s.enabled });
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  const remove = async (s: McpServer) => {
    if (!confirm(`Remove "${s.name}"?`)) return;
    try {
      await agentClient.deleteMcpServer(s.name);
      await load();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  };

  const input =
    "w-full rounded-md border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-100 placeholder:text-surface-500";

  return (
    <div className="card mb-6">
      <div className="mb-2 flex items-center justify-between">
        <h3 className="flex items-center gap-2 text-sm font-medium uppercase tracking-wider text-surface-400">
          <span aria-hidden>🔌</span> MCP Servers
        </h3>
        {!open && (
          <button className="text-xs font-medium text-sky-400 hover:text-sky-300" onClick={() => setOpen(true)} disabled={!connected}>
            + Add
          </button>
        )}
      </div>
      <p className="mb-3 text-xs text-surface-500">
        Connect any remote MCP — your own private servers or others&apos; public ones. Their tools become usable from
        Yaver, namespaced <span className="font-mono text-surface-300">name__tool</span>.
      </p>

      {!connected && <p className="text-xs text-surface-500">Connect a machine to manage MCP servers.</p>}
      {err && <p className="mb-2 text-xs text-red-400">{err}</p>}

      {open && (
        <div className="mb-3 space-y-2 rounded-lg border border-surface-700 p-3">
          <input className={input} placeholder="Name (e.g. yaverbet)" value={name} onChange={(e) => setName(e.target.value)} />
          <input className={input} placeholder="URL (https://host/mcp)" value={url} onChange={(e) => setUrl(e.target.value)} />
          <input className={input} type="password" placeholder="Bearer token (optional)" value={token} onChange={(e) => setToken(e.target.value)} />
          <label className="flex items-center gap-2 text-sm text-surface-300">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} /> Enabled
          </label>
          {testMsg && <p className="text-xs text-surface-400">{testMsg}</p>}
          <div className="flex gap-2">
            <button className="rounded-md border border-surface-700 px-3 py-1.5 text-xs text-sky-400" onClick={test} disabled={busy}>
              Test
            </button>
            <button className="flex-1 rounded-md bg-sky-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-sky-400" onClick={save} disabled={busy}>
              {busy ? "Saving…" : "Save"}
            </button>
            <button className="rounded-md border border-surface-700 px-3 py-1.5 text-xs text-surface-400" onClick={reset} disabled={busy}>
              Cancel
            </button>
          </div>
        </div>
      )}

      {loading && servers.length === 0 ? (
        <p className="text-xs text-surface-500">Loading…</p>
      ) : servers.length === 0 ? (
        connected && <p className="text-xs text-surface-500">No MCP servers yet.</p>
      ) : (
        <ul className="space-y-2">
          {servers.map((s) => (
            <li key={s.name} className="rounded-lg border border-surface-800 p-3">
              <div className="flex items-center justify-between gap-2">
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-surface-100">{s.name}</div>
                  <div className="truncate text-xs text-surface-500">{s.url}</div>
                  <div className="text-[11px] text-surface-500">
                    {s.toolCount ?? 0} tools{s.hasAuth ? " · auth" : ""}
                  </div>
                </div>
                <label className="flex shrink-0 items-center gap-1 text-xs text-surface-400">
                  <input type="checkbox" checked={s.enabled} onChange={() => toggle(s)} />
                  on
                </label>
              </div>
              <div className="mt-2 flex gap-2">
                <button className="rounded-md border border-surface-700 px-2.5 py-1 text-xs text-sky-400" onClick={() => edit(s)}>
                  Edit
                </button>
                <button className="rounded-md border border-red-500/30 px-2.5 py-1 text-xs text-red-400" onClick={() => remove(s)}>
                  Remove
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
