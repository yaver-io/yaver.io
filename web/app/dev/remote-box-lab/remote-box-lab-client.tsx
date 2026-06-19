"use client";

import { useMemo, useState } from "react";

type LabDevice = {
  id: string;
  name: string;
  role: "primary" | "secondary" | "candidate";
  transport: "relay" | "direct";
  latencyMs: number;
  hermesReady: boolean;
  runners: Array<{ id: "codex" | "claude" | "opencode"; label: string; ready: boolean; authConfigured: boolean }>;
};

const DEFAULT_CONVEX_URL = "https://perceptive-minnow-557.eu-west-1.convex.site";

const LAB_DEVICES: LabDevice[] = [
  {
    id: "hetzner-codex",
    name: "Hetzner coding box",
    role: "primary",
    transport: "relay",
    latencyMs: 133,
    hermesReady: true,
    runners: [
      { id: "codex", label: "OpenAI Codex", ready: true, authConfigured: true },
      { id: "claude", label: "Claude Code", ready: true, authConfigured: true },
      { id: "opencode", label: "OpenCode", ready: true, authConfigured: true },
    ],
  },
  {
    id: "mac-local",
    name: "MacBook local box",
    role: "secondary",
    transport: "direct",
    latencyMs: 20,
    hermesReady: true,
    runners: [
      { id: "claude", label: "Claude Code", ready: true, authConfigured: true },
      { id: "codex", label: "OpenAI Codex", ready: false, authConfigured: false },
    ],
  },
];

function preferredRunner(device: LabDevice) {
  return device.runners.find((r) => r.id === "codex" && r.ready)?.id
    || device.runners.find((r) => r.ready)?.id
    || "";
}

export default function RemoteBoxLabClient() {
  const [selectedDeviceId, setSelectedDeviceId] = useState(LAB_DEVICES[0].id);
  const [convexUrl, setConvexUrl] = useState(DEFAULT_CONVEX_URL);
  const [ownerToken, setOwnerToken] = useState("");
  const [guestEmail, setGuestEmail] = useState("");
  const [projectSlice, setProjectSlice] = useState("workspace");
  const [inviteResult, setInviteResult] = useState<string>("");
  const [busy, setBusy] = useState(false);

  const selected = LAB_DEVICES.find((d) => d.id === selectedDeviceId) || LAB_DEVICES[0];
  const runner = preferredRunner(selected);
  const invitePayload = useMemo(() => ({
    email: guestEmail.trim(),
    deviceIds: [selected.id],
    scope: "full",
    allowedProjects: projectSlice
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean),
  }), [guestEmail, projectSlice, selected.id]);

  async function sendInvite() {
    setBusy(true);
    setInviteResult("");
    try {
      const res = await fetch(`${convexUrl.replace(/\/+$/, "")}/guests/invite`, {
        method: "POST",
        headers: {
          Authorization: `Bearer ${ownerToken.trim()}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(invitePayload),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body?.error || `HTTP ${res.status}`);
      setInviteResult(`Invite created: ${body.inviteCode || "(no code returned)"}`);
    } catch (err) {
      setInviteResult(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="min-h-screen bg-[#08080a] px-5 py-6 text-zinc-100">
      <section className="mx-auto max-w-5xl">
        <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
          <div>
            <p className="text-xs font-semibold uppercase tracking-wide text-violet-300">Local dev lab</p>
            <h1 className="text-2xl font-semibold">Remote box and guest invite flow</h1>
          </div>
          <div className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-200">
            Selected runner: {runner || "none"}
          </div>
        </div>

        <div className="grid gap-4 md:grid-cols-[1.1fr_0.9fr]">
          <div className="space-y-3">
            {LAB_DEVICES.map((device) => {
              const selectedRow = device.id === selectedDeviceId;
              const ready = device.runners.filter((r) => r.ready);
              return (
                <button
                  key={device.id}
                  onClick={() => setSelectedDeviceId(device.id)}
                  className={`w-full rounded-lg border p-4 text-left transition ${
                    selectedRow ? "border-violet-400 bg-violet-500/10" : "border-zinc-800 bg-zinc-950 hover:border-zinc-600"
                  }`}
                >
                  <div className="flex items-center justify-between gap-3">
                    <div>
                      <div className="text-lg font-semibold">{device.name}</div>
                      <div className="mt-1 text-sm text-zinc-400">
                        {device.role} · {device.transport} · {device.latencyMs}ms
                      </div>
                    </div>
                    <span className={selectedRow ? "text-violet-300" : "text-zinc-500"}>
                      {selectedRow ? "Selected" : "Pick"}
                    </span>
                  </div>
                  <div className="mt-3 flex flex-wrap gap-2 text-sm">
                    <span className="rounded-md bg-emerald-500/10 px-2 py-1 text-emerald-300">
                      Hermes {device.hermesReady ? "ready" : "missing"}
                    </span>
                    {ready.map((r) => (
                      <span key={r.id} className="rounded-md bg-blue-500/10 px-2 py-1 text-blue-300">
                        {r.label} ready
                      </span>
                    ))}
                  </div>
                </button>
              );
            })}
          </div>

          <div className="rounded-lg border border-zinc-800 bg-zinc-950 p-4">
            <h2 className="text-lg font-semibold">Scoped guest invite</h2>
            <div className="mt-4 space-y-3">
              <label className="block text-sm">
                <span className="text-zinc-400">Convex URL</span>
                <input className="mt-1 w-full rounded-md border border-zinc-700 bg-black px-3 py-2 text-zinc-100" value={convexUrl} onChange={(e) => setConvexUrl(e.target.value)} />
              </label>
              <label className="block text-sm">
                <span className="text-zinc-400">Owner bearer token</span>
                <input className="mt-1 w-full rounded-md border border-zinc-700 bg-black px-3 py-2 text-zinc-100" value={ownerToken} onChange={(e) => setOwnerToken(e.target.value)} type="password" />
              </label>
              <label className="block text-sm">
                <span className="text-zinc-400">Guest email</span>
                <input className="mt-1 w-full rounded-md border border-zinc-700 bg-black px-3 py-2 text-zinc-100" value={guestEmail} onChange={(e) => setGuestEmail(e.target.value)} />
              </label>
              <label className="block text-sm">
                <span className="text-zinc-400">Allowed projects</span>
                <input className="mt-1 w-full rounded-md border border-zinc-700 bg-black px-3 py-2 text-zinc-100" value={projectSlice} onChange={(e) => setProjectSlice(e.target.value)} />
              </label>
              <pre className="overflow-auto rounded-md border border-zinc-800 bg-black p-3 text-xs text-zinc-300">
                {JSON.stringify(invitePayload, null, 2)}
              </pre>
              <button
                onClick={sendInvite}
                disabled={busy || !guestEmail.trim() || !ownerToken.trim()}
                className="w-full rounded-md bg-violet-500 px-4 py-3 font-semibold text-black disabled:cursor-not-allowed disabled:opacity-40"
              >
                {busy ? "Sending..." : "Create scoped guest invite"}
              </button>
              {inviteResult ? <p className="text-sm text-zinc-300">{inviteResult}</p> : null}
            </div>
          </div>
        </div>
      </section>
    </main>
  );
}
