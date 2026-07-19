"use client";

import { useEffect, useState } from "react";
import { CONVEX_URL } from "@/lib/constants";
import { agentClient, type CapabilitySnapshot, type ConnectionState, type ConnectAttemptDiagnostic, type IncidentEvent, type InfraSummary, type TailscaleStatus } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";
import { classifyDiagnostic, summarizeFailures } from "@/lib/connection-error";

type SettingsState = {
  relayUrl: string;
  relayPassword: string;
  tunnelUrl: string;
};

function normalizeUrl(value: string) {
  return value.trim().replace(/\/+$/, "");
}

function recommendationLabel(args: {
  device?: Device | null;
  settings: SettingsState;
  tailscale?: TailscaleStatus | null;
  infra?: InfraSummary | null;
}) {
  if ((args.device?.publicEndpoints?.length || 0) > 0 || args.settings.tunnelUrl) return "Advanced HTTPS endpoint";
  if (args.tailscale?.running) return "Private network";
  if ((args.infra?.relays?.length || 0) > 0 || args.settings.relayUrl) return "Yaver Relay";
  return "LAN";
}

function transportOrder(args: {
  settings: SettingsState;
  device?: Device | null;
  tailscale?: TailscaleStatus | null;
  infra?: InfraSummary | null;
}) {
  const out: string[] = ["LAN"];
  if (args.tailscale?.running) out.push("Private network");
  if ((args.device?.publicEndpoints?.length || 0) > 0 || args.settings.tunnelUrl) out.push("Advanced HTTPS endpoint");
  if ((args.infra?.relays?.length || 0) > 0 || args.settings.relayUrl) out.push("Yaver Relay");
  return out;
}

export default function ConnectivityView({
  token,
  devices,
  connectedDevice,
  connState,
  connectDiagnostics,
}: {
  token: string | null;
  devices: Device[];
  connectedDevice: Device | null;
  connState: ConnectionState;
  connectDiagnostics: ConnectAttemptDiagnostic[];
}) {
  const [settings, setSettings] = useState<SettingsState>({ relayUrl: "", relayPassword: "", tunnelUrl: "" });
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState<{ type: "ok" | "error"; text: string } | null>(null);
  const [infra, setInfra] = useState<InfraSummary | null>(null);
  const [tailscale, setTailscale] = useState<TailscaleStatus | null>(null);
  const [capabilitySnapshot, setCapabilitySnapshot] = useState<CapabilitySnapshot | null>(null);
  const [connectivityIncidents, setConnectivityIncidents] = useState<IncidentEvent[]>([]);
  const [testBusy, setTestBusy] = useState<"relay" | "tunnel" | null>(null);
  const [testResults, setTestResults] = useState<{ relay?: string; tunnel?: string }>({});

  useEffect(() => {
    let cancelled = false;
    async function loadSettings() {
      if (!token) {
        setLoading(false);
        return;
      }
      try {
        const res = await fetch(`${CONVEX_URL}/settings`, {
          headers: { Authorization: `Bearer ${token}` },
        });
        if (!res.ok) throw new Error(`settings ${res.status}`);
        const data = await res.json();
        const next = data?.settings || {};
        if (!cancelled) {
          setSettings({
            relayUrl: next.relayUrl || "",
            relayPassword: next.relayPassword || "",
            tunnelUrl: next.tunnelUrl || "",
          });
        }
      } catch {
        if (!cancelled) setMessage({ type: "error", text: "Could not load account connectivity settings." });
      } finally {
        if (!cancelled) setLoading(false);
      }
    }
    loadSettings();
    return () => {
      cancelled = true;
    };
  }, [token]);

  useEffect(() => {
    let cancelled = false;
    async function loadConnectedState() {
      if (connState !== "connected" || !connectedDevice) {
        setInfra(null);
        setTailscale(null);
        setCapabilitySnapshot(null);
        setConnectivityIncidents([]);
        return;
      }
      try {
        const [summary, ts, snapshot, incidents] = await Promise.all([
          agentClient.infraSummary(),
          agentClient.tailscaleStatus().catch(() => ({ running: false } as TailscaleStatus)),
          agentClient.capabilitySnapshot().catch(() => null),
          agentClient.incidents({ category: "connectivity", limit: 5 }).catch(() => []),
        ]);
        if (!cancelled) {
          setInfra(summary);
          setTailscale(ts);
          setCapabilitySnapshot(snapshot);
          setConnectivityIncidents(incidents);
        }
      } catch {
        if (!cancelled) {
          setInfra(null);
          setTailscale(null);
          setCapabilitySnapshot(null);
          setConnectivityIncidents([]);
        }
      }
    }
    loadConnectedState();
    return () => {
      cancelled = true;
    };
  }, [connState, connectedDevice?.id]);

  const ownerDevices = devices.filter((device) => !device.isGuest);
  const recommended = recommendationLabel({ device: connectedDevice, settings, tailscale, infra });
  const order = transportOrder({ device: connectedDevice, settings, tailscale, infra });
  const cloudflaredInstalled = !!infra?.binaries?.some((item) => item.name === "cloudflared");
  const tailscaleInstalled = !!infra?.binaries?.some((item) => item.name === "tailscale");
  const publicEndpoints = connectedDevice?.publicEndpoints || [];

  async function saveSettings(next: Partial<SettingsState>) {
    if (!token) return;
    const payload = {
      relayUrl: next.relayUrl !== undefined ? normalizeUrl(next.relayUrl) : normalizeUrl(settings.relayUrl),
      relayPassword: next.relayPassword !== undefined ? next.relayPassword : settings.relayPassword,
      tunnelUrl: next.tunnelUrl !== undefined ? normalizeUrl(next.tunnelUrl) : normalizeUrl(settings.tunnelUrl),
    };
    setSaving(true);
    setMessage(null);
    try {
      const res = await fetch(`${CONVEX_URL}/settings`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });
      if (!res.ok) throw new Error(`settings ${res.status}`);
      setSettings((prev) => ({ ...prev, ...payload }));
      setMessage({ type: "ok", text: "Connectivity settings saved to your account." });
    } catch {
      setMessage({ type: "error", text: "Could not save connectivity settings." });
    } finally {
      setSaving(false);
    }
  }

  async function testTarget(kind: "relay" | "tunnel") {
    const raw = kind === "relay" ? settings.relayUrl : settings.tunnelUrl;
    const url = normalizeUrl(raw);
    if (!url) {
      setTestResults((prev) => ({ ...prev, [kind]: "Enter a URL first." }));
      return;
    }
    setTestBusy(kind);
    const start = performance.now();
    try {
      const res = await fetch(`${url}/health`, { signal: AbortSignal.timeout(5000) });
      const elapsed = Math.round(performance.now() - start);
      setTestResults((prev) => ({
        ...prev,
        [kind]: res.ok ? `OK ${elapsed}ms` : `HTTP ${res.status}`,
      }));
    } catch {
      setTestResults((prev) => ({ ...prev, [kind]: "Unreachable" }));
    } finally {
      setTestBusy(null);
    }
  }

  return (
    <div className="space-y-6 p-6 max-w-6xl mx-auto w-full overflow-y-auto">
      <section className="rounded-3xl border border-surface-800 bg-surface-900/70 p-5">
        <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
          <div>
            <div className="mb-2 text-xs font-semibold uppercase tracking-[0.18em] text-surface-500">Connectivity</div>
            <h2 className="text-2xl font-semibold text-surface-100">Remote access setup</h2>
            <p className="mt-2 max-w-3xl text-sm text-surface-400">
              Choose one transport model per account, but keep in mind that local machine setup still runs through the connected agent. The web dashboard can save defaults and show status; it cannot install tunnel software by itself.
            </p>
          </div>
          <div className="rounded-2xl border border-emerald-500/30 bg-emerald-500/10 px-4 py-3 text-sm text-emerald-700 dark:text-emerald-200">
            Recommended now: <span className="font-semibold">{recommended}</span>
          </div>
        </div>

        <div className="mt-4 flex flex-wrap gap-2">
          {order.map((item) => (
            <span key={item} className="rounded-full border border-surface-700 bg-surface-950 px-3 py-1 text-xs text-surface-300">
              {item}
            </span>
          ))}
        </div>

        {!capabilitySnapshot?.targets?.["web-preview"]?.enabled && capabilitySnapshot?.targets?.["web-preview"]?.reason ? (
          <div className="mt-4 rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-700 dark:text-amber-200">
            <div className="font-medium">Preview blocked</div>
            <div className="mt-1">{capabilitySnapshot.targets["web-preview"].reason}</div>
            {capabilitySnapshot.targets["web-preview"].suggestedAction ? (
              <div className="mt-1 text-xs text-amber-800 dark:text-amber-100/80">{capabilitySnapshot.targets["web-preview"].suggestedAction}</div>
            ) : null}
          </div>
        ) : null}

        {connectivityIncidents.length > 0 ? (
          <div className="mt-4 rounded-2xl border border-red-500/30 bg-red-500/10 p-4">
            <div className="text-xs font-semibold uppercase tracking-[0.16em] text-red-700 dark:text-red-200">Current connectivity blockers</div>
            <div className="mt-3 space-y-3">
              {connectivityIncidents.map((incident) => (
                <div key={incident.id} className="rounded-xl border border-red-500/20 bg-surface-950/40 p-3">
                  <div className="flex items-center gap-2 text-sm text-surface-100">
                    <span className={`h-2 w-2 rounded-full ${incident.severity === "fatal" || incident.severity === "error" ? "bg-red-400" : "bg-amber-400"}`} />
                    <span>{incident.title || incident.code}</span>
                  </div>
                  <div className="mt-2 text-sm text-surface-300">{incident.userMessage}</div>
                  {incident.suggestedAction ? <div className="mt-1 text-xs text-surface-500">{incident.suggestedAction}</div> : null}
                  {incident.logRefs?.length ? <div className="mt-1 text-[11px] text-surface-600">Logs: {incident.logRefs.join(", ")}</div> : null}
                </div>
              ))}
            </div>
          </div>
        ) : null}

        {connectDiagnostics.length > 0 ? (() => {
          // If any attempt failed, surface the most actionable classified reason
          // at the top — the per-attempt rows underneath are useful for the
          // engineer but a single "this is why nothing worked" header tells
          // the user what to do.
          const summary = summarizeFailures(connectDiagnostics);
          return (
            <div className="mt-4 rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
              <div className="text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">Last connect attempts</div>
              {summary ? (
                <div className="mt-3 rounded border border-amber-500/30 bg-amber-500/5 p-3">
                  <div className="text-sm font-semibold text-amber-700 dark:text-amber-200">{summary.label}</div>
                  <div className="mt-1 text-xs text-surface-300">{summary.detail}</div>
                  {summary.suggestedAction ? (
                    <div className="mt-1 text-xs text-surface-500">{summary.suggestedAction}</div>
                  ) : null}
                </div>
              ) : null}
              <div className="mt-3 space-y-2">
                {connectDiagnostics.map((diag, index) => {
                  const classified = diag.ok ? null : classifyDiagnostic(diag);
                  return (
                    <div key={`${diag.path}:${diag.relayId || "direct"}:${index}`} className="flex items-start gap-3 text-xs text-surface-400">
                      <span className={`mt-1 h-2 w-2 shrink-0 rounded-full ${diag.ok ? "bg-emerald-400" : diag.authExpired ? "bg-amber-400" : "bg-red-400"}`} />
                      <span className="w-24 shrink-0 font-mono text-surface-300">
                        {diag.path === "relay" ? `relay:${diag.relayId || "?"}` : diag.path}
                      </span>
                      <span className="flex-1">
                        {diag.ok ? (
                          <span className="text-emerald-700 dark:text-emerald-300">ok</span>
                        ) : classified ? (
                          <>
                            <span className="text-amber-700 dark:text-amber-200">{classified.label}</span>
                            {classified.raw && classified.raw !== classified.label ? (
                              <span className="ml-1 text-surface-600">({classified.raw})</span>
                            ) : null}
                          </>
                        ) : (
                          <span>{diag.status ? `HTTP ${diag.status}` : diag.error || "failed"}</span>
                        )}
                      </span>
                      {diag.durationMs != null ? <span className="ml-auto text-surface-600">{diag.durationMs}ms</span> : null}
                    </div>
                  );
                })}
              </div>
            </div>
          );
        })() : null}
      </section>

      <div className="grid gap-6 xl:grid-cols-[1.1fr_0.9fr]">
        <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-5">
          <div className="mb-4">
            <h3 className="text-lg font-semibold text-surface-100">Account defaults</h3>
            <p className="mt-1 text-sm text-surface-500">
              These sync across devices. Use them for one primary remote path, not as a full per-machine routing table.
            </p>
          </div>

          {ownerDevices.length > 1 && (settings.relayUrl || settings.tunnelUrl) ? (
            <div className="mb-4 rounded-2xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-700 dark:text-amber-200">
              You have {ownerDevices.length} owner devices. Account-level relay and tunnel defaults are ambiguous across multiple machines; prefer per-device agent config for Cloudflare endpoints.
            </div>
          ) : null}

          {message ? (
            <div className={`mb-4 rounded-2xl border p-3 text-sm ${message.type === "ok" ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-200" : "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-200"}`}>
              {message.text}
            </div>
          ) : null}

          <div className="space-y-5">
            <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
              <div className="mb-3 flex items-center justify-between gap-3">
                <div>
                  <div className="font-medium text-surface-100">Yaver Relay</div>
                  <div className="text-xs text-surface-500">Best when you want a stable cross-network fallback without exposing your machine directly.</div>
                </div>
              </div>
              <div className="grid gap-3 md:grid-cols-2">
                <label className="text-xs text-surface-400">
                  Relay URL
                  <input
                    value={settings.relayUrl}
                    onChange={(e) => setSettings((prev) => ({ ...prev, relayUrl: e.target.value }))}
                    placeholder="https://relay.example.com"
                    className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 outline-none focus:border-surface-500"
                  />
                </label>
                <label className="text-xs text-surface-400">
                  Relay password
                  <input
                    type="password"
                    value={settings.relayPassword}
                    onChange={(e) => setSettings((prev) => ({ ...prev, relayPassword: e.target.value }))}
                    placeholder="Optional"
                    className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 outline-none focus:border-surface-500"
                  />
                </label>
              </div>
              <div className="mt-3 flex flex-wrap gap-2">
                <button
                  onClick={() => saveSettings({ relayUrl: settings.relayUrl, relayPassword: settings.relayPassword })}
                  disabled={saving || loading}
                  className="rounded-lg border border-surface-700 px-3 py-2 text-sm text-surface-200 hover:border-surface-500 disabled:opacity-40"
                >
                  {saving ? "Saving..." : "Save relay"}
                </button>
                <button
                  onClick={() => testTarget("relay")}
                  disabled={testBusy !== null || !normalizeUrl(settings.relayUrl)}
                  className="rounded-lg border border-surface-700 px-3 py-2 text-sm text-surface-200 hover:border-surface-500 disabled:opacity-40"
                >
                  {testBusy === "relay" ? "Testing..." : "Test"}
                </button>
                {testResults.relay ? <span className="self-center text-xs text-surface-500">{testResults.relay}</span> : null}
              </div>
            </div>

            <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
              <div className="mb-3">
                <div className="font-medium text-surface-100">Advanced HTTPS endpoint</div>
                <div className="text-xs text-surface-500">Optional compatibility field for an existing private endpoint. Yaver Relay is the normal remote path.</div>
              </div>
              <label className="text-xs text-surface-400">
                Tunnel URL
                <input
                  value={settings.tunnelUrl}
                  onChange={(e) => setSettings((prev) => ({ ...prev, tunnelUrl: e.target.value }))}
                  placeholder="https://machine.example.com"
                  className="mt-1 w-full rounded-lg border border-surface-700 bg-surface-900 px-3 py-2 text-sm text-surface-200 outline-none focus:border-surface-500"
                />
              </label>
              <div className="mt-3 flex flex-wrap gap-2">
                <button
                  onClick={() => saveSettings({ tunnelUrl: settings.tunnelUrl })}
                  disabled={saving || loading}
                  className="rounded-lg border border-surface-700 px-3 py-2 text-sm text-surface-200 hover:border-surface-500 disabled:opacity-40"
                >
                  {saving ? "Saving..." : "Save endpoint"}
                </button>
                <button
                  onClick={() => testTarget("tunnel")}
                  disabled={testBusy !== null || !normalizeUrl(settings.tunnelUrl)}
                  className="rounded-lg border border-surface-700 px-3 py-2 text-sm text-surface-200 hover:border-surface-500 disabled:opacity-40"
                >
                  {testBusy === "tunnel" ? "Testing..." : "Test"}
                </button>
                {testResults.tunnel ? <span className="self-center text-xs text-surface-500">{testResults.tunnel}</span> : null}
              </div>
            </div>
          </div>
        </section>

        <div className="space-y-6">
          <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-5">
            <div className="mb-4">
              <h3 className="text-lg font-semibold text-surface-100">Connected machine</h3>
              <p className="mt-1 text-sm text-surface-500">
                Live machine-side transport status. Connect to a device to see agent-discovered capabilities.
              </p>
            </div>

            {!connectedDevice ? (
              <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4 text-sm text-surface-500">
                No device selected. Connect to a machine to inspect relays, private-network status, and advertised public endpoints.
              </div>
            ) : (
              <div className="space-y-4">
                <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <div className="font-medium text-surface-100">{connectedDevice.name}</div>
                      <div className="text-xs text-surface-500">{connectedDevice.platform} · {connectedDevice.host}:{connectedDevice.port}</div>
                    </div>
                    <span className={`rounded-full px-2 py-1 text-[11px] ${connState === "connected" ? "bg-emerald-500/10 text-emerald-700 dark:text-emerald-200" : "bg-surface-800 text-surface-400"}`}>
                      {connState}
                    </span>
                  </div>
                </div>

                <StatusRow label="Custom tunnel helper installed" value={cloudflaredInstalled ? "yes" : "no"} tone={cloudflaredInstalled ? "ok" : "muted"} />
                <StatusRow label="Private network helper installed" value={tailscaleInstalled ? "yes" : "no"} tone={tailscaleInstalled ? "ok" : "muted"} />
                <StatusRow label="Private network running" value={tailscale?.running ? "yes" : "no"} tone={tailscale?.running ? "ok" : "muted"} />
                <StatusRow label="Relay endpoints" value={String(infra?.relays?.length || 0)} tone={(infra?.relays?.length || 0) > 0 ? "ok" : "muted"} />
                <StatusRow label="Advertised public endpoints" value={String(publicEndpoints.length)} tone={publicEndpoints.length > 0 ? "ok" : "muted"} />

                {tailscale?.running && tailscale.self?.addrs?.length ? (
                  <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
                    <div className="mb-2 text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">Private network IPs</div>
                    <div className="flex flex-wrap gap-2">
                      {tailscale.self.addrs.map((addr) => (
                        <span key={addr} className="rounded-full border border-surface-700 bg-surface-900 px-3 py-1 text-xs font-mono text-surface-300">
                          {addr}
                        </span>
                      ))}
                    </div>
                  </div>
                ) : null}

                {publicEndpoints.length > 0 ? (
                  <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
                    <div className="mb-2 text-xs font-semibold uppercase tracking-[0.16em] text-surface-500">Public endpoints from agent config</div>
                    <div className="space-y-2">
                      {publicEndpoints.map((endpoint) => (
                        <div key={endpoint} className="rounded-xl border border-surface-800 bg-surface-900 px-3 py-2 text-xs font-mono text-surface-300">
                          {endpoint}
                        </div>
                      ))}
                    </div>
                  </div>
                ) : (
                  <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4 text-sm text-surface-500">
                    No custom endpoint configured for this device yet. Set one up on the machine with the agent, then it will appear here.
                  </div>
                )}
              </div>
            )}
          </section>

          <section className="rounded-3xl border border-surface-800 bg-surface-900/50 p-5">
            <h3 className="text-lg font-semibold text-surface-100">What to use</h3>
            <div className="mt-4 space-y-3 text-sm text-surface-400">
              <ChoiceCard title="LAN" body="Best on the same Wi-Fi. Zero setup, lowest latency, but not useful once you leave the network." />
              <ChoiceCard title="Yaver Relay" body="Best as the normal remote path. Works well across networks and is already the transport the web dashboard prefers when available." />
              <ChoiceCard title="Private network" body="Best when you already operate your own private network. Yaver can detect compatible addresses, but setup stays outside the Yaver onboarding path." />
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}

function StatusRow({ label, value, tone }: { label: string; value: string; tone: "ok" | "muted" }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-2xl border border-surface-800 bg-surface-950/60 px-4 py-3">
      <span className="text-sm text-surface-400">{label}</span>
      <span className={tone === "ok" ? "text-sm font-medium text-emerald-700 dark:text-emerald-300" : "text-sm text-surface-500"}>{value}</span>
    </div>
  );
}

function ChoiceCard({ title, body }: { title: string; body: string }) {
  return (
    <div className="rounded-2xl border border-surface-800 bg-surface-950/60 p-4">
      <div className="font-medium text-surface-100">{title}</div>
      <p className="mt-1 text-sm text-surface-500">{body}</p>
    </div>
  );
}
