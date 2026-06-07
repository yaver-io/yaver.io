"use client";

// NetworkView — the Yaver Mesh console (optional WireGuard overlay / Tailscale
// alternative). Lists the user's mesh nodes with their overlay IPs + liveness,
// and provides a port-level ACL editor (who → whom → which ports). Reads/writes
// the control plane through the Convex /mesh/* HTTP routes using the session
// token. Icons are hand-rolled feather-style SVGs per the repo's no-icon-library
// rule.

import { useCallback, useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";

type MeshPeer = {
  deviceId: string;
  alias?: string;
  meshIPv4?: string;
  online?: boolean;
  isExitNode?: boolean;
  accessScope?: "owner" | "shared" | "peer";
  endpoints?: string[];
  advertisedRoutes?: string[];
  wantEnabled?: boolean | null;
  wantExitNode?: boolean;
  wantUseExitNode?: string;
  wantRoutes?: string[];
};

type ACLRule = {
  srcType: "tag" | "device" | "user" | "any";
  src: string;
  dstType: "tag" | "device" | "user" | "any";
  dst: string;
  ports: string[];
  action: "accept" | "drop";
};

type SupportConn = {
  grantId: string;
  deviceId: string | null;
  counterpartName: string;
  counterpartEmail?: string;
  allowDesktopControl: boolean;
  expiresAt: number | null;
};

function Icon({ path, className }: { path: string; className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className ?? "h-4 w-4"}
      aria-hidden
    >
      <path d={path} />
    </svg>
  );
}
// Bridging a Tailnet means advertising Tailscale's CGNAT block as a mesh
// subnet route on a node that sits on BOTH networks. Mesh peer /32s and the
// 100.96/12 overlay are longer-prefix matches, so they still win — only real
// Tailnet hosts (the rest of 100.64/10) route through the bridge. Lets mesh
// peers reach a Tailnet without every node needing Tailscale installed.
const TAILSCALE_BRIDGE_CIDR = "100.64.0.0/10";
const ICON_GLOBE = "M12 3a9 9 0 100 18 9 9 0 000-18zM3 12h18M12 3c2.5 2.5 2.5 15.5 0 18M12 3c-2.5 2.5-2.5 15.5 0 18";
const ICON_SHIELD = "M12 3l7 3v6c0 4-3 6.5-7 9-4-2.5-7-5-7-9V6l7-3z";
const ICON_PLUS = "M12 5v14M5 12h14";
const ICON_TRASH = "M4 7h16M9 7V5h6v2M6 7l1 13h10l1-13";

export default function NetworkView({ token }: { token: string | null }) {
  const [peers, setPeers] = useState<MeshPeer[]>([]);
  const [rules, setRules] = useState<ACLRule[]>([]);
  const [tags, setTags] = useState<Record<string, string[]>>({});
  const [supportLink, setSupportLink] = useState<string | null>(null);
  const [supporting, setSupporting] = useState<SupportConn[]>([]);
  const [supportedBy, setSupportedBy] = useState<SupportConn[]>([]);
  const [linkCopied, setLinkCopied] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const authHeaders = useCallback(
    () => ({ Authorization: `Bearer ${token}`, "Content-Type": "application/json" }),
    [token]
  );

  const load = useCallback(async () => {
    if (!token) return;
    setLoading(true);
    setError(null);
    try {
      const [pRes, aRes, tRes] = await Promise.all([
        fetch(`${CONVEX_URL}/mesh/peers`, { headers: authHeaders() }),
        fetch(`${CONVEX_URL}/mesh/acls`, { headers: authHeaders() }),
        fetch(`${CONVEX_URL}/mesh/tags`, { headers: authHeaders() }),
      ]);
      if (!pRes.ok) throw new Error(`peers: HTTP ${pRes.status}`);
      const pJson = await pRes.json();
      setPeers(pJson.peers ?? []);
      if (aRes.ok) {
        const aJson = await aRes.json();
        setRules(aJson.rules ?? []);
      }
      if (tRes.ok) {
        const tJson = await tRes.json();
        const byDevice: Record<string, string[]> = {};
        for (const t of tJson.tags ?? []) {
          (byDevice[t.deviceId] ??= []).push(t.tag);
        }
        setTags(byDevice);
      }
      const cRes = await fetch(`${CONVEX_URL}/support/connections`, { headers: authHeaders() });
      if (cRes.ok) {
        const cJson = await cRes.json();
        setSupporting(cJson.supporting ?? []);
        setSupportedBy(cJson.supportedBy ?? []);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, [authHeaders]);

  useEffect(() => {
    void load();
  }, [load]);

  const saveRules = useCallback(
    async (next: ACLRule[]) => {
      setSaving(true);
      setError(null);
      try {
        const res = await fetch(`${CONVEX_URL}/mesh/acls/set`, {
          method: "POST",
          headers: authHeaders(),
          body: JSON.stringify({ rules: next }),
        });
        if (!res.ok) throw new Error(`save: HTTP ${res.status}`);
        setRules(next);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setSaving(false);
      }
    },
    [authHeaders]
  );

  const saveTags = useCallback(
    async (deviceId: string, deviceTags: string[]) => {
      setTags((prev) => ({ ...prev, [deviceId]: deviceTags }));
      try {
        await fetch(`${CONVEX_URL}/mesh/tags/set`, {
          method: "POST",
          headers: authHeaders(),
          body: JSON.stringify({ deviceId, tags: deviceTags }),
        });
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [authHeaders]
  );

  const saveNodeConfig = useCallback(
    async (deviceId: string, patch: Partial<Pick<MeshPeer, "wantEnabled" | "wantExitNode" | "wantUseExitNode" | "wantRoutes">>) => {
      // optimistic
      setPeers((prev) => prev.map((p) => (p.deviceId === deviceId ? { ...p, ...patch } : p)));
      try {
        const res = await fetch(`${CONVEX_URL}/mesh/node/config`, {
          method: "POST",
          headers: authHeaders(),
          body: JSON.stringify({ deviceId, ...patch }),
        });
        if (!res.ok) throw new Error(`node config: HTTP ${res.status}`);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        void load();
      }
    },
    [authHeaders, load]
  );

  const createSupportLink = useCallback(
    async (offerTerminal: boolean, offerDesktopControl: boolean) => {
      setError(null);
      try {
        const res = await fetch(`${CONVEX_URL}/support/invite`, {
          method: "POST",
          headers: authHeaders(),
          body: JSON.stringify({ offerTerminal, offerDesktopControl }),
        });
        if (!res.ok) throw new Error(`invite: HTTP ${res.status}`);
        const json = await res.json();
        const base = typeof window !== "undefined" ? window.location.origin : "https://yaver.io";
        setSupportLink(`${base}/j/${json.code}`);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [authHeaders]
  );

  const revokeSupportGrant = useCallback(
    async (grantId: string) => {
      try {
        await fetch(`${CONVEX_URL}/support/grant/revoke`, {
          method: "POST",
          headers: authHeaders(),
          body: JSON.stringify({ grantId }),
        });
        void load();
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      }
    },
    [authHeaders, load]
  );

  const addRule = () =>
    saveRules([
      ...rules,
      { srcType: "any", src: "*", dstType: "any", dst: "*", ports: ["*"], action: "accept" },
    ]);
  const removeRule = (i: number) => saveRules(rules.filter((_, idx) => idx !== i));
  const updateRule = (i: number, patch: Partial<ACLRule>) =>
    setRules(rules.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));

  const deviceOptions = peers.map((p) => ({ id: p.deviceId, label: p.alias || p.deviceId }));

  return (
    <div className="space-y-6 p-6 max-w-6xl mx-auto w-full overflow-y-auto">
      <header className="flex flex-col gap-2">
        <div className="flex items-center gap-2 text-surface-100">
          <Icon path={ICON_GLOBE} className="h-5 w-5 text-emerald-400" />
          <h1 className="text-2xl font-semibold">Mesh Network</h1>
        </div>
        <p className="max-w-3xl text-sm text-surface-400">
          Yaver Mesh is an optional WireGuard overlay — a Tailscale alternative built
          into your fleet. Bring a device on with <code className="text-surface-200">yaver mesh up</code>; it
          gets a stable overlay IP and becomes reachable from every other node. Sharing a
          device to another person automatically extends the mesh to them.
        </p>
      </header>

      {error && (
        <div className="rounded-2xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-200">
          {error}
        </div>
      )}

      <section className="rounded-3xl border border-surface-800 bg-surface-900/70 p-5">
        <div className="flex items-center justify-between">
          <h2 className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-surface-500">
            Mesh nodes
          </h2>
          <button
            onClick={() => void load()}
            className="rounded-full border border-surface-700 bg-surface-950 px-3 py-1 text-xs text-surface-300 hover:text-surface-100"
          >
            Refresh
          </button>
        </div>
        {loading ? (
          <p className="mt-2 text-sm text-surface-400">Loading…</p>
        ) : peers.length === 0 ? (
          <p className="mt-2 text-sm text-surface-400">
            No mesh nodes yet. Run <code className="text-surface-200">yaver mesh up</code> on a device.
          </p>
        ) : (
          <div className="mt-3 space-y-2">
            {peers.map((p) => {
              const isOwner = p.accessScope === "owner";
              const advertisingExit = p.isExitNode || p.wantExitNode;
              const currentRoutes = p.wantRoutes ?? (p.advertisedRoutes ?? []).filter((r) => r !== "0.0.0.0/0");
              const bridgingTailnet = currentRoutes.includes(TAILSCALE_BRIDGE_CIDR);
              const toggleTailnetBridge = () => {
                const next = bridgingTailnet
                  ? currentRoutes.filter((r) => r !== TAILSCALE_BRIDGE_CIDR)
                  : [...currentRoutes, TAILSCALE_BRIDGE_CIDR];
                void saveNodeConfig(p.deviceId, { wantRoutes: next });
              };
              return (
                <div
                  key={p.deviceId}
                  className="rounded-xl border border-surface-800 bg-surface-950/40 px-4 py-3"
                >
                  <div className="flex flex-wrap items-center gap-3">
                    <span
                      className={`h-2 w-2 rounded-full ${p.online ? "bg-emerald-400" : "bg-surface-600"}`}
                      title={p.online ? "online" : "offline"}
                    />
                    <span className="font-medium text-surface-100">{p.alias || p.deviceId}</span>
                    <code className="rounded bg-surface-900 px-2 py-0.5 text-xs text-emerald-300">
                      {p.meshIPv4 || "—"}
                    </code>
                    {p.alias && <code className="text-xs text-surface-500">{meshDnsName(p.alias)}</code>}
                    {p.accessScope === "shared" && (
                      <span className="rounded-full border border-violet-500/30 bg-violet-500/10 px-2 py-0.5 text-[11px] text-violet-200">
                        shared to you
                      </span>
                    )}
                    {advertisingExit && (
                      <span className="rounded-full border border-amber-500/30 bg-amber-500/10 px-2 py-0.5 text-[11px] text-amber-200">
                        exit node
                      </span>
                    )}
                    {(p.advertisedRoutes ?? []).filter((r) => r !== "0.0.0.0/0").length > 0 && (
                      <span
                        className="rounded-full border border-cyan-500/30 bg-cyan-500/10 px-2 py-0.5 text-[11px] text-cyan-200"
                        title="Gateway (subnet router) — advertises the subnet routes below"
                      >
                        gateway · {(p.advertisedRoutes ?? []).filter((r) => r !== "0.0.0.0/0").length}
                      </span>
                    )}
                    {(p.advertisedRoutes ?? []).filter((r) => r !== "0.0.0.0/0").map((r) => (
                      <span key={r} className="rounded-full border border-sky-500/30 bg-sky-500/10 px-2 py-0.5 text-[11px] text-sky-200">
                        {r}
                      </span>
                    ))}
                    {isOwner && (
                      <input
                        defaultValue={(tags[p.deviceId] ?? []).join(", ")}
                        onBlur={(e) => {
                          const next = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
                          void saveTags(p.deviceId, next);
                        }}
                        placeholder="tags…"
                        className="ml-auto w-36 rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-[11px] text-surface-300"
                        title="Comma-separated tags for ACL rules (e.g. prod, db)"
                      />
                    )}
                  </div>

                  {isOwner && (
                    <div className="mt-2 flex flex-wrap items-center gap-3 border-t border-surface-800/60 pt-2 text-xs text-surface-400">
                      <label className="flex items-center gap-1.5">
                        <input
                          type="checkbox"
                          checked={!!p.wantExitNode}
                          onChange={(e) => void saveNodeConfig(p.deviceId, { wantExitNode: e.target.checked })}
                        />
                        advertise as exit node
                      </label>
                      <span className="flex items-center gap-1.5">
                        use exit node:
                        <select
                          value={p.wantUseExitNode || ""}
                          onChange={(e) => void saveNodeConfig(p.deviceId, { wantUseExitNode: e.target.value })}
                          className="rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-surface-200"
                        >
                          <option value="">none</option>
                          {peers
                            .filter((x) => x.deviceId !== p.deviceId && (x.isExitNode || x.wantExitNode))
                            .map((x) => (
                              <option key={x.deviceId} value={x.deviceId}>
                                {x.alias || x.deviceId}
                              </option>
                            ))}
                        </select>
                      </span>
                      <input
                        defaultValue={currentRoutes.filter((r) => r !== TAILSCALE_BRIDGE_CIDR).join(", ")}
                        onBlur={(e) => {
                          const typed = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
                          // Preserve the Tailnet-bridge route (it has its own toggle).
                          const next = bridgingTailnet ? [...typed, TAILSCALE_BRIDGE_CIDR] : typed;
                          void saveNodeConfig(p.deviceId, { wantRoutes: next });
                        }}
                        placeholder="subnet routes e.g. 10.0.0.0/24"
                        className="w-52 rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-surface-300"
                        title="Subnet routes to advertise (subnet router)"
                      />
                      <button
                        onClick={toggleTailnetBridge}
                        className={`rounded-full border px-3 py-1 text-[11px] ${
                          bridgingTailnet
                            ? "border-cyan-500/40 bg-cyan-500/15 text-cyan-200"
                            : "border-surface-700 bg-surface-950 text-surface-400 hover:text-surface-200"
                        }`}
                        title="If this node is also on a Tailscale tailnet, bridge it so mesh peers can reach Tailnet hosts (advertises 100.64.0.0/10)"
                      >
                        {bridgingTailnet ? "✓ bridging Tailnet" : "bridge Tailnet"}
                      </button>
                      <button
                        onClick={() => void saveNodeConfig(p.deviceId, { wantEnabled: false })}
                        className="ml-auto rounded-full border border-red-500/30 bg-red-500/10 px-3 py-1 text-[11px] text-red-200 hover:bg-red-500/20"
                        title="Tell this node to leave the mesh"
                      >
                        Disable
                      </button>
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </section>

      <section className="rounded-3xl border border-surface-800 bg-surface-900/70 p-5">
        <h2 className="mb-1 text-xs font-semibold uppercase tracking-[0.18em] text-surface-500">
          Support a friend
        </h2>
        <p className="mb-3 text-xs text-surface-400">
          Send a link — your friend installs Yaver, approves access, and their machine joins your
          mesh so you can help them (SSH / your AI agent / screen). Default access is view + files;
          they opt into more on their own consent screen.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <button
            onClick={() => void createSupportLink(false, false)}
            className="rounded-full border border-emerald-500/40 bg-emerald-500/10 px-3 py-1.5 text-xs text-emerald-200 hover:bg-emerald-500/20"
          >
            Create view-only link
          </button>
          <button
            onClick={() => void createSupportLink(true, true)}
            className="rounded-full border border-amber-500/40 bg-amber-500/10 px-3 py-1.5 text-xs text-amber-200 hover:bg-amber-500/20"
          >
            Create full-support link
          </button>
        </div>
        {supportLink && (
          <div className="mt-3 flex items-center gap-2 rounded-xl border border-surface-800 bg-surface-950 p-3">
            <code className="flex-1 break-all text-xs text-emerald-300">{supportLink}</code>
            <button
              onClick={() => {
                navigator.clipboard?.writeText(supportLink);
                setLinkCopied(true);
                setTimeout(() => setLinkCopied(false), 1500);
              }}
              className="shrink-0 rounded-lg border border-surface-700 px-3 py-1 text-xs text-surface-200 hover:bg-surface-800"
            >
              {linkCopied ? "Copied" : "Copy"}
            </button>
          </div>
        )}
        {(supporting.length > 0 || supportedBy.length > 0) && (
          <div className="mt-4 space-y-3 border-t border-surface-800/60 pt-3">
            {supporting.length > 0 && (
              <div>
                <p className="mb-1 text-[11px] uppercase tracking-wide text-surface-500">You can support</p>
                {supporting.map((c) => (
                  <div key={c.grantId} className="flex items-center gap-2 text-xs text-surface-300">
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                    {c.counterpartName}
                    {c.allowDesktopControl && <span className="text-amber-300">· desktop</span>}
                    <span className="text-surface-500">{c.expiresAt ? "· time-boxed" : "· until revoked"}</span>
                    <button onClick={() => void revokeSupportGrant(c.grantId)} className="ml-auto text-surface-500 hover:text-red-300">
                      end
                    </button>
                  </div>
                ))}
              </div>
            )}
            {supportedBy.length > 0 && (
              <div>
                <p className="mb-1 text-[11px] uppercase tracking-wide text-rose-300/80">Who can access your machines</p>
                {supportedBy.map((c) => (
                  <div key={c.grantId} className="flex items-center gap-2 text-xs text-surface-300">
                    <span className="h-1.5 w-1.5 rounded-full bg-rose-400" />
                    {c.counterpartName}
                    <button onClick={() => void revokeSupportGrant(c.grantId)} className="ml-auto rounded border border-rose-500/30 px-2 text-rose-200 hover:bg-rose-500/10">
                      revoke
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </section>

      <section className="rounded-3xl border border-surface-800 bg-surface-900/70 p-5">
        <div className="flex items-center justify-between">
          <h2 className="mb-1 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.18em] text-surface-500">
            <Icon path={ICON_SHIELD} className="h-4 w-4" /> Access rules (ACLs)
          </h2>
          <button
            onClick={addRule}
            disabled={saving}
            className="flex items-center gap-1 rounded-full border border-emerald-500/40 bg-emerald-500/10 px-3 py-1 text-xs text-emerald-200 hover:bg-emerald-500/20 disabled:opacity-50"
          >
            <Icon path={ICON_PLUS} className="h-3.5 w-3.5" /> Add rule
          </button>
        </div>
        <p className="mt-1 mb-3 text-xs text-surface-400">
          With no rules, every node can reach every other (default allow). Add a rule and
          everything not explicitly allowed is denied — standard mesh-ACL semantics. Rules
          are enforced on each device, live.
        </p>
        {rules.length === 0 ? (
          <p className="text-sm text-surface-400">No rules — open mesh (default allow).</p>
        ) : (
          <div className="space-y-2">
            {rules.map((r, i) => (
              <div
                key={i}
                className="flex flex-wrap items-center gap-2 rounded-xl border border-surface-800 bg-surface-950/40 px-3 py-2 text-sm"
              >
                <EndpointPicker
                  type={r.srcType}
                  value={r.src}
                  devices={deviceOptions}
                  onChange={(srcType, src) => updateRule(i, { srcType, src })}
                />
                <span className="text-surface-500">→</span>
                <EndpointPicker
                  type={r.dstType}
                  value={r.dst}
                  devices={deviceOptions}
                  onChange={(dstType, dst) => updateRule(i, { dstType, dst })}
                />
                <input
                  value={r.ports.join(",")}
                  onChange={(e) => updateRule(i, { ports: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })}
                  onBlur={() => saveRules(rules)}
                  placeholder="ports e.g. 22,80-90,*"
                  className="w-40 rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
                />
                <select
                  value={r.action}
                  onChange={(e) => {
                    const next = rules.map((x, idx) => (idx === i ? { ...x, action: e.target.value as "accept" | "drop" } : x));
                    void saveRules(next);
                  }}
                  className="rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
                >
                  <option value="accept">accept</option>
                  <option value="drop">drop</option>
                </select>
                <button
                  onClick={() => removeRule(i)}
                  className="ml-auto text-surface-500 hover:text-red-300"
                  title="Remove rule"
                >
                  <Icon path={ICON_TRASH} className="h-4 w-4" />
                </button>
              </div>
            ))}
            <p className="text-[11px] text-surface-500">
              {saving ? "Saving…" : "Edits to ports save on blur; src/dst/action save instantly."}
            </p>
          </div>
        )}
      </section>
    </div>
  );
}

function meshDnsName(alias: string) {
  return `${alias.toLowerCase().replace(/[ .]/g, "-")}.mesh`;
}

function EndpointPicker({
  type,
  value,
  devices,
  onChange,
}: {
  type: ACLRule["srcType"];
  value: string;
  devices: { id: string; label: string }[];
  onChange: (type: ACLRule["srcType"], value: string) => void;
}) {
  return (
    <span className="flex items-center gap-1">
      <select
        value={type}
        onChange={(e) => {
          const t = e.target.value as ACLRule["srcType"];
          onChange(t, t === "any" ? "*" : value);
        }}
        className="rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
      >
        <option value="any">any</option>
        <option value="device">device</option>
        <option value="tag">tag</option>
      </select>
      {type === "device" ? (
        <select
          value={value}
          onChange={(e) => onChange(type, e.target.value)}
          className="rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
        >
          <option value="">choose…</option>
          {devices.map((d) => (
            <option key={d.id} value={d.id}>
              {d.label}
            </option>
          ))}
        </select>
      ) : type === "tag" ? (
        <input
          value={value}
          onChange={(e) => onChange(type, e.target.value)}
          placeholder="tag:prod"
          className="w-28 rounded-lg border border-surface-700 bg-surface-950 px-2 py-1 text-xs text-surface-200"
        />
      ) : null}
    </span>
  );
}
