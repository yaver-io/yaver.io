"use client";

import { useCallback, useEffect, useState } from "react";
import { agentClient } from "@/lib/agent-client";

type Server = {
  id?: string;
  ID?: string;
  name?: string;
  Name?: string;
  status?: string;
  Status?: string;
  ip?: string;
  IP?: string;
};

/**
 * Desktop-shell-only Hetzner power control. Ordinary browsers do not render or
 * originate infrastructure actions. The provider credential remains in the
 * connected user-owned endpoint's OS vault.
 */
export default function ByoCloudPanel() {
  const [desktopShell, setDesktopShell] = useState(false);
  const [connected, setConnected] = useState<boolean | null>(null);
  const [servers, setServers] = useState<Server[] | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const loadAccounts = useCallback(async () => {
    if (!desktopShell) return;
    if (!agentClient.isConnected) {
      setConnected(false);
      return;
    }
    try {
      const result = await agentClient.accountsList();
      const hetzner = (result.accounts || []).find((account: any) => account.provider === "hetzner");
      setConnected(hetzner?.connected === true);
    } catch {
      setConnected(false);
    }
  }, [desktopShell]);

  const loadServers = useCallback(async () => {
    setBusy("list");
    try {
      const result = await agentClient.callOps("hetzner_power", { action: "list" });
      if (!result.ok) throw new Error(result.error || "Hetzner list failed");
      setServers(Array.isArray(result.initial?.servers) ? result.initial.servers : []);
      setMessage(null);
    } catch {
      setMessage("Could not list this endpoint's Hetzner servers.");
    } finally {
      setBusy(null);
    }
  }, []);

  useEffect(() => {
    setDesktopShell((window as unknown as { yaver?: { surface?: string } }).yaver?.surface === "desktop-gui");
  }, []);
  useEffect(() => { if (desktopShell) void loadAccounts(); }, [desktopShell, loadAccounts]);

  if (!desktopShell || connected !== true) return null;

  const id = (server: Server) => String(server.id ?? server.ID ?? "");
  const name = (server: Server) => String(server.name ?? server.Name ?? id(server));
  const status = (server: Server) => String(server.status ?? server.Status ?? "unknown").toLowerCase();

  const changePower = async (server: Server, action: "power_on" | "shutdown") => {
    const serverId = id(server);
    if (!serverId) return;
    const label = action === "power_on" ? "Power on" : "Shut down";
    if (!window.confirm(`${label} ${name(server)}?`)) return;
    setBusy(`${action}:${serverId}`);
    setMessage(null);
    try {
      const result = await agentClient.callOps("hetzner_power", { action, serverId, confirm: true });
      if (!result.ok) throw new Error(result.error || "Hetzner power action failed");
      setMessage(`${label} completed and was verified by Hetzner.`);
      await loadServers();
    } catch {
      setMessage(`${label} failed. Check the trusted endpoint and Hetzner account.`);
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className="space-y-3 rounded-xl border border-sky-500/20 bg-sky-500/5 p-4">
      <div className="flex items-center justify-between gap-3">
        <div>
          <h3 className="text-sm font-semibold text-surface-200">Your Hetzner servers</h3>
          <p className="text-xs text-surface-500">
            Power control executes on your trusted endpoint. Yaver Cloud never receives the Hetzner token.
          </p>
        </div>
        <button
          onClick={() => void loadServers()}
          disabled={busy !== null}
          className="rounded-md border border-sky-500/40 px-3 py-1.5 text-xs font-semibold text-sky-700 disabled:opacity-50 dark:text-sky-300"
        >
          {busy === "list" ? "…" : servers === null ? "Load" : "Refresh"}
        </button>
      </div>

      {servers === null ? (
        <p className="text-xs text-surface-500">Load the servers owned by this Hetzner account.</p>
      ) : servers.length === 0 ? (
        <p className="text-xs text-surface-500">No servers on this Hetzner account.</p>
      ) : (
        <div className="space-y-1">
          {servers.map((server) => {
            const serverId = id(server);
            const isOff = status(server) === "off";
            const action = isOff ? "power_on" : "shutdown";
            const actionBusy = busy === `${action}:${serverId}`;
            return (
              <div key={serverId} className="flex items-center gap-2 text-xs">
                <span className="flex-1 truncate font-mono text-surface-400">
                  {name(server)} · {status(server)} · {String(server.ip ?? server.IP ?? "")}
                </span>
                <button
                  disabled={busy !== null}
                  onClick={() => void changePower(server, action)}
                  className={`${isOff ? "text-emerald-400" : "text-amber-400"} font-semibold disabled:opacity-50`}
                >
                  {actionBusy ? "…" : isOff ? "Power on" : "Shut down"}
                </button>
              </div>
            );
          })}
        </div>
      )}

      <p className="text-[11px] text-surface-500">A powered-off Hetzner server remains allocated and continues billing.</p>
      {message ? <p className="text-xs text-surface-400">{message}</p> : null}
    </div>
  );
}
