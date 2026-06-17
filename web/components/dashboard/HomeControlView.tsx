"use client";
// HomeControlView — the "single kumanda" universal remote + activities on web
// (docs/yaver-single-kumanda.md). Mirrors AppleTVCellView's transport
// (ensureClient + callOps) exactly, but drives the home_* ops verbs
// (desktop/agent/ops_home.go). A separate "Home" surface — never the coding UI.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient, agentClient } from "@/lib/agent-client";
import type { Device } from "@/lib/use-devices";

type HomeDevice = { id: string; name?: string; kind: string; address?: string };
type HomeActivity = { name: string; steps: { device: string; key: string }[] };

const DPAD: { key: string; label: string }[][] = [
  [{ key: "power", label: "⏻" }, { key: "up", label: "▲" }, { key: "menu", label: "≡" }],
  [{ key: "left", label: "◀" }, { key: "ok", label: "OK" }, { key: "right", label: "▶" }],
  [{ key: "back", label: "↩" }, { key: "down", label: "▼" }, { key: "home", label: "⌂" }],
];
const TRANSPORT = [
  { key: "previous", label: "⏮" },
  { key: "play_pause", label: "⏯" },
  { key: "next", label: "⏭" },
  { key: "vol_down", label: "🔉" },
  { key: "vol_up", label: "🔊" },
];

export default function HomeControlView({ devices, token }: { devices: Device[]; token: string | null }) {
  const [deviceId, setDeviceId] = useState("");
  const [homeDevices, setHomeDevices] = useState<HomeDevice[]>([]);
  const [activities, setActivities] = useState<HomeActivity[]>([]);
  const [selected, setSelected] = useState("");
  const [cameras, setCameras] = useState<{ id: string; name?: string }[]>([]);
  const [snapshot, setSnapshot] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);

  const clientRef = useRef<AgentClient | null>(null);
  const connectedTo = useRef("");

  const ensureClient = useCallback(
    async (id: string): Promise<AgentClient | null> => {
      const device = devices.find((d) => d.id === id);
      if (!device || !token) return null;
      if (clientRef.current && connectedTo.current === id) return clientRef.current;
      try {
        clientRef.current?.disconnect();
      } catch {}
      clientRef.current = null;
      connectedTo.current = "";
      const client = new AgentClient();
      client.setRelayServers(agentClient.configuredRelayServers.map((r) => ({ ...r })));
      const tunnelUrls = Array.from(new Set([...(Array.isArray(device.publicEndpoints) ? device.publicEndpoints : []), ...(device.tunnelUrl ? [device.tunnelUrl] : [])]));
      await client.connect(device.host, device.port, token, device.id, { tunnelUrls });
      clientRef.current = client;
      connectedTo.current = id;
      return client;
    },
    [devices, token],
  );

  const callOps = useCallback(
    async (verb: string, payload: Record<string, unknown> = {}): Promise<any> => {
      try {
        const client = await ensureClient(deviceId);
        if (!client) return { ok: false, error: "not connected" };
        const res = await client.callOps(verb, payload);
        if (res?.ok === false) return { ok: false, code: res.code, error: res.error };
        return (res as any)?.initial ?? res;
      } catch (e: any) {
        setMsg(e?.message || "connection failed");
        return { ok: false, error: e?.message || "failed" };
      }
    },
    [deviceId, ensureClient],
  );

  const refresh = useCallback(async () => {
    if (!deviceId) return;
    setBusy(true);
    try {
      const [d, a, cams] = await Promise.all([callOps("home_device_list"), callOps("home_activity_list"), callOps("camera_list")]);
      const dl: HomeDevice[] = d?.devices || [];
      setHomeDevices(dl);
      setActivities(a?.activities || []);
      setCameras(cams?.cameras || []);
      if (!selected && dl.length) setSelected(dl[0].id);
    } finally {
      setBusy(false);
    }
  }, [deviceId, callOps, selected]);

  useEffect(() => {
    if (deviceId) refresh();
  }, [deviceId]); // eslint-disable-line react-hooks/exhaustive-deps

  const send = async (key: string) => {
    if (!selected) {
      setMsg("Pick a device first");
      return;
    }
    const r = await callOps("home_key", { device: selected, key });
    setMsg(r?.ok === false ? r.error || "Failed" : null);
  };

  const runActivity = async (name: string) => {
    setBusy(true);
    try {
      const r = await callOps("home_activity_run", { name });
      setMsg(r?.completed ? `✓ ${name}` : `${name}: stopped early`);
    } finally {
      setBusy(false);
    }
  };

  const grabSnapshot = async (id: string) => {
    setBusy(true);
    try {
      const r = await callOps("camera_snapshot", { id });
      if (r?.image_b64) setSnapshot(`data:${r.mime || "image/jpeg"};base64,${r.image_b64}`);
      else setMsg(r?.error || "No frame");
    } finally {
      setBusy(false);
    }
  };

  const chip = "rounded-lg border px-3 py-2 text-sm font-medium";
  const pad = "h-14 w-16 rounded-xl border text-lg font-bold";

  return (
    <div className="space-y-4">
      <div>
        <div className="mb-1 text-xs uppercase tracking-wide opacity-60">Hub device</div>
        <div className="flex flex-wrap gap-2">
          {devices.map((d) => (
            <button key={d.id} className={`${chip} ${deviceId === d.id ? "bg-foreground text-background" : ""}`} onClick={() => setDeviceId(d.id)}>
              {d.name || d.id}
            </button>
          ))}
        </div>
      </div>

      {deviceId ? (
        <>
          <div>
            <div className="mb-1 text-xs uppercase tracking-wide opacity-60">Device</div>
            <div className="flex flex-wrap gap-2">
              {homeDevices.length === 0 ? (
                <span className="text-sm opacity-60">No devices yet — add Apple TV / Mi Box / IR via home_device_add.</span>
              ) : (
                homeDevices.map((d) => (
                  <button key={d.id} className={`${chip} ${selected === d.id ? "bg-foreground text-background" : ""}`} onClick={() => setSelected(d.id)}>
                    {(d.name || d.id) + " · " + d.kind}
                  </button>
                ))
              )}
            </div>
          </div>

          <div className="flex flex-col items-center gap-2">
            {DPAD.map((row, i) => (
              <div key={i} className="flex gap-2">
                {row.map((b) => (
                  <button key={b.key} className={pad} onClick={() => send(b.key)}>
                    {b.label}
                  </button>
                ))}
              </div>
            ))}
            <div className="mt-2 flex gap-2">
              {TRANSPORT.map((b) => (
                <button key={b.key} className={pad} onClick={() => send(b.key)}>
                  {b.label}
                </button>
              ))}
            </div>
          </div>

          <div>
            <div className="mb-1 text-xs uppercase tracking-wide opacity-60">Activities</div>
            <div className="flex flex-wrap gap-2">
              {activities.length === 0 ? (
                <span className="text-sm opacity-60">No activities yet.</span>
              ) : (
                activities.map((a) => (
                  <button key={a.name} className={chip} onClick={() => runActivity(a.name)}>
                    ▶ {a.name}
                  </button>
                ))
              )}
            </div>
          </div>

          {cameras.length > 0 ? (
            <div>
              <div className="mb-1 text-xs uppercase tracking-wide opacity-60">Cameras</div>
              <div className="flex flex-wrap gap-2">
                {cameras.map((cam) => (
                  <button key={cam.id} className={chip} onClick={() => grabSnapshot(cam.id)}>
                    📷 {cam.name || cam.id}
                  </button>
                ))}
              </div>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              {snapshot ? <img src={snapshot} alt="camera snapshot" className="mt-2 max-h-64 rounded-lg border" /> : null}
            </div>
          ) : null}

          {busy ? <div className="text-sm opacity-60">…</div> : null}
          {msg ? <div className="text-sm opacity-70">{msg}</div> : null}
        </>
      ) : (
        <div className="text-sm opacity-60">Pick the hub device that runs the agent next to your AV gear.</div>
      )}
    </div>
  );
}
