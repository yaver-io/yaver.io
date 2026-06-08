"use client";

import { useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

// DomainsView — "bring your own domain" flow. Lets a signed-in user:
//   1. Name a custom domain (myapp.com / api.myapp.com).
//   2. Point it at a server they control (raw IP).
//   3. See the exact DNS records they need to set at their registrar
//      (TXT for ownership verification, A or CNAME for routing).
//   4. Click "Verify" to poll DoH and flip the row to "verified" once both
//      records resolve.
//
// The backend (backend/convex/userDomains.ts) holds the truth; this view
// is a thin client.

type UserDomain = {
  _id: string;
  domain: string;
  targetType: "cloud_machine" | "managed_relay" | "custom_server";
  targetId?: string;
  targetIp?: string;
  autoDomain?: string;
  dnsProvider?: string;
  verificationToken: string;
  status: "pending" | "verified" | "active" | "error";
  errorMessage?: string;
  createdAt: number;
  updatedAt: number;
  verifiedAt?: number;
};

type DnsInstructions = {
  records: {
    type: "TXT" | "A" | "CNAME";
    name: string;
    value: string;
    ttl: number;
    note: string;
  }[];
};

export default function DomainsView({ token, userId }: { token: string; userId: string }) {
  const [domains, setDomains] = useState<UserDomain[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<UserDomain | null>(null);
  const [instructions, setInstructions] = useState<DnsInstructions | null>(null);
  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState<string | null>(null);

  // Form state for "Add a domain"
  const [newDomain, setNewDomain] = useState("");
  const targetType: UserDomain["targetType"] = "custom_server";
  const [targetIp, setTargetIp] = useState("");
  const [dnsProvider, setDnsProvider] = useState<"cloudflare" | "manual">("manual");
  const [submitting, setSubmitting] = useState(false);

  async function load() {
    setLoading(true);
    try {
      const dResp = await fetch(
        `${CONVEX_URL}/api/query/userDomains:listForUser?args=${encodeURIComponent(JSON.stringify({ userId }))}`,
        { headers: { Authorization: `Bearer ${token}` } },
      );
      const d = dResp.ok ? await dResp.json() : { value: [] };
      setDomains(d.value || d || []);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void load(); }, [token, userId]);

  async function addDomain() {
    if (!newDomain.trim()) return;
    setSubmitting(true);
    setError(null);
    try {
      const args: Record<string, unknown> = {
        userId,
        domain: newDomain.trim().toLowerCase(),
        targetType,
        dnsProvider,
        targetIp: targetIp.trim(),
      };
      const resp = await fetch(`${CONVEX_URL}/api/mutation/userDomains:add`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify({ args }),
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || "Add failed");
      setNewDomain("");
      setTargetIp("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  }

  async function openInstructions(row: UserDomain) {
    setSelected(row);
    setInstructions(null);
    setVerifyResult(null);
    try {
      const resp = await fetch(
        `${CONVEX_URL}/api/query/userDomains:instructions?args=${encodeURIComponent(JSON.stringify({ domainId: row._id }))}`,
        { headers: { Authorization: `Bearer ${token}` } },
      );
      const data = await resp.json();
      if (resp.ok) setInstructions(data.value || data);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function verifyDomain(row: UserDomain) {
    setVerifying(true);
    setVerifyResult(null);
    try {
      const resp = await fetch(`${CONVEX_URL}/api/action/userDomains:verify`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify({ args: { domainId: row._id } }),
      });
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || "Verify failed");
      const { ok, details } = data.value || data;
      setVerifyResult(`${ok ? "verified" : "not yet"}: ${details}`);
      if (ok) await load();
    } catch (e) {
      setVerifyResult(e instanceof Error ? e.message : String(e));
    } finally {
      setVerifying(false);
    }
  }

  async function removeDomain(row: UserDomain) {
    if (!confirm(`Delete ${row.domain}? You'll need to remove its DNS records at your registrar separately.`)) return;
    try {
      await fetch(`${CONVEX_URL}/api/mutation/userDomains:remove`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` },
        body: JSON.stringify({ args: { domainId: row._id, userId } }),
      });
      if (selected?._id === row._id) { setSelected(null); setInstructions(null); }
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div className="flex flex-1 flex-col overflow-auto p-4 gap-4">
      <div>
        <h2 className="text-sm font-semibold text-surface-100">Custom Domains</h2>
        <p className="mt-1 text-xs text-surface-400">
          Point a domain you own (from Namecheap, Porkbun, Cloudflare Registrar, GoDaddy, …)
          at a server you control. We'll generate the exact records you need to paste at
          your registrar.
        </p>
      </div>

      {/* Add form */}
      <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3 space-y-2">
        <div className="text-[11px] font-semibold uppercase tracking-widest text-surface-500">Add a domain</div>
        <div className="grid gap-2 md:grid-cols-2">
          <label className="text-xs text-surface-300 space-y-1">
            <span>Domain</span>
            <input value={newDomain} onChange={e => setNewDomain(e.target.value)} placeholder="myapp.com"
              className="w-full rounded border border-surface-800 bg-surface-950 px-2 py-1 text-xs text-surface-200" />
          </label>
          <label className="text-xs text-surface-300 space-y-1">
            <span>Server IPv4</span>
            <input value={targetIp} onChange={e => setTargetIp(e.target.value)} placeholder="203.0.113.42"
              className="w-full rounded border border-surface-800 bg-surface-950 px-2 py-1 text-xs text-surface-200" />
          </label>
          <label className="text-xs text-surface-300 space-y-1">
            <span>DNS at</span>
            <select value={dnsProvider} onChange={e => setDnsProvider(e.target.value as "cloudflare" | "manual")}
              className="w-full rounded border border-surface-800 bg-surface-950 px-2 py-1 text-xs text-surface-200">
              <option value="manual">Manual (any registrar)</option>
              <option value="cloudflare">Cloudflare (auto)</option>
            </select>
          </label>
        </div>
        <button disabled={submitting} onClick={addDomain}
          className="rounded-md bg-indigo-500 px-3 py-1.5 text-[11px] font-medium text-white hover:bg-indigo-400 disabled:opacity-40">
          {submitting ? "Saving…" : "Generate DNS instructions"}
        </button>
      </div>

      {/* List */}
      <div className="rounded-lg border border-surface-800 bg-surface-900/40">
        <div className="border-b border-surface-800 px-3 py-2 text-[11px] font-semibold uppercase tracking-widest text-surface-500">
          Your domains
        </div>
        {loading ? (
          <div className="p-4 text-xs text-surface-500">Loading…</div>
        ) : domains.length === 0 ? (
          <div className="p-4 text-xs text-surface-500">No domains yet.</div>
        ) : (
          <ul>
            {domains.map(d => (
              <li key={d._id} className="flex items-center justify-between border-b border-surface-800/50 px-3 py-2 last:border-0">
                <div>
                  <span className="text-sm text-surface-100">{d.domain}</span>
                  <span className="ml-2 text-[10px] uppercase tracking-wider text-surface-500">{d.targetType.replace("_", " ")}</span>
                  <span className={`ml-2 rounded px-1.5 py-[1px] text-[9px] uppercase tracking-wider ${
                    d.status === "active" ? "bg-emerald-500/20 text-emerald-700 dark:text-emerald-300" :
                    d.status === "verified" ? "bg-sky-500/20 text-sky-700 dark:text-sky-300" :
                    d.status === "error" ? "bg-red-500/20 text-red-700 dark:text-red-300" :
                    "bg-amber-500/20 text-amber-700 dark:text-amber-300"
                  }`}>{d.status}</span>
                  {d.errorMessage && <span className="ml-2 text-[10px] text-red-400">{d.errorMessage}</span>}
                </div>
                <div className="flex gap-2">
                  <button onClick={() => openInstructions(d)} className="text-[11px] text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-300">Records</button>
                  <button onClick={() => verifyDomain(d)} disabled={verifying} className="text-[11px] text-sky-400 hover:text-sky-700 dark:hover:text-sky-300 disabled:opacity-40">
                    {verifying && selected?._id === d._id ? "Checking…" : "Verify"}
                  </button>
                  <button onClick={() => removeDomain(d)} className="text-[11px] text-red-400 hover:text-red-700 dark:hover:text-red-300">Remove</button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* Instructions panel */}
      {selected && instructions && (
        <div className="rounded-lg border border-surface-800 bg-surface-900/40 p-3">
          <div className="flex items-center justify-between">
            <div className="text-[11px] font-semibold uppercase tracking-widest text-surface-500">
              DNS records for {selected.domain}
            </div>
            <button onClick={() => { setSelected(null); setInstructions(null); }} className="text-[11px] text-surface-500 hover:text-surface-300">close</button>
          </div>
          <p className="mt-1 text-xs text-surface-400">
            Add these records at your DNS host (Cloudflare, Namecheap, Porkbun, etc.) exactly as written.
            Once both are live, click <em>Verify</em>.
          </p>
          <table className="mt-2 w-full text-xs">
            <thead>
              <tr className="text-surface-500">
                <th className="p-1 text-left font-medium">Type</th>
                <th className="p-1 text-left font-medium">Name</th>
                <th className="p-1 text-left font-medium">Value</th>
                <th className="p-1 text-left font-medium">TTL</th>
              </tr>
            </thead>
            <tbody>
              {instructions.records.map((r, i) => (
                <tr key={i} className="border-t border-surface-800/50">
                  <td className="p-1 font-mono text-surface-200">{r.type}</td>
                  <td className="p-1 font-mono text-surface-200">{r.name}</td>
                  <td className="p-1 font-mono text-surface-200 break-all">{r.value}</td>
                  <td className="p-1 text-surface-400">{r.ttl}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {verifyResult && (
            <p className={`mt-2 text-xs ${verifyResult.startsWith("verified") ? "text-emerald-400" : "text-amber-400"}`}>
              {verifyResult}
            </p>
          )}
        </div>
      )}

      {error && <p className="text-xs text-red-400">{error}</p>}
    </div>
  );
}
