"use client";

// /watch — VIEW-ONLY live stream from a shared link. No login: the link's hash
// carries a stream-scoped token (reaches only the read-only stream_* verbs on
// the box) + the device's connection info + relay config. We connect a
// relay-only AgentClient with that token and snapshot-poll stream_list /
// stream_snapshot. There are NO controls here — the token can't reach them.
//
// Yaver is a neutral streaming tool (like OBS): it shows whatever the source
// provides, as-is. What's shared and the right to share it is the owner's.
import { useCallback, useEffect, useRef, useState } from "react";
import { AgentClient } from "@/lib/agent-client";
import type { RelayServer } from "@/lib/agent-client";

type Source = { source: string; label?: string; kind?: string; live?: boolean };
type Snap = { image?: string; title?: string; artist?: string; app?: string; state?: string };

export default function WatchPage() {
  const [status, setStatus] = useState<"connecting" | "live" | "error">("connecting");
  const [error, setError] = useState<string | null>(null);
  const [sources, setSources] = useState<Source[]>([]);
  const [frames, setFrames] = useState<Record<string, Snap>>({});
  const clientRef = useRef<AgentClient | null>(null);
  const liveRef = useRef(true);

  const decode = useCallback((): { d: any; r: RelayServer[]; t: string } | null => {
    try {
      const hash = typeof window !== "undefined" ? window.location.hash.replace(/^#/, "") : "";
      if (!hash) return null;
      return JSON.parse(decodeURIComponent(atob(hash)));
    } catch {
      return null;
    }
  }, []);

  useEffect(() => {
    liveRef.current = true;
    const blob = decode();
    if (!blob?.d?.id || !blob?.t) {
      setStatus("error");
      setError("This watch link is invalid or incomplete.");
      return;
    }
    let timer: ReturnType<typeof setInterval> | null = null;
    (async () => {
      try {
        const client = new AgentClient();
        client.setRelayServers((blob.r || []).map((s) => ({ ...s })));
        const tunnelUrls = Array.from(
          new Set([...(Array.isArray(blob.d.publicEndpoints) ? blob.d.publicEndpoints : []), ...(blob.d.tunnelUrl ? [blob.d.tunnelUrl] : [])]),
        );
        await client.connect(blob.d.host, blob.d.port, blob.t, blob.d.id, { tunnelUrls });
        clientRef.current = client;

        const list = await client.callOps("stream_list", {});
        const srcs: Source[] = ((list as any)?.initial ?? list)?.sources || [];
        if (liveRef.current) setSources(srcs);
        setStatus("live");

        const poll = async () => {
          for (const s of srcs) {
            try {
              const r = await client.callOps("stream_snapshot", { source: s.source });
              const data = (r as any)?.initial ?? r;
              if (data && !data.error && liveRef.current) {
                setFrames((f) => ({ ...f, [s.source]: data }));
              }
            } catch {
              /* transient */
            }
          }
        };
        await poll();
        timer = setInterval(poll, 1200);
      } catch (e: any) {
        if (liveRef.current) {
          setStatus("error");
          setError(e?.message || "Couldn't connect to the stream.");
        }
      }
    })();

    return () => {
      liveRef.current = false;
      if (timer) clearInterval(timer);
      try {
        clientRef.current?.disconnect();
      } catch {}
    };
  }, [decode]);

  return (
    <div className="min-h-screen bg-neutral-950 p-4 text-neutral-100">
      <div className="mx-auto max-w-2xl space-y-4">
        <div className="flex items-center justify-between">
          <h1 className="text-lg font-semibold">Yaver — live view</h1>
          <span className={`rounded px-2 py-0.5 text-xs ${status === "live" ? "bg-emerald-700" : status === "error" ? "bg-rose-700" : "bg-neutral-700"}`}>
            {status === "live" ? "view only" : status}
          </span>
        </div>

        {status === "error" && <p className="text-sm text-rose-400">{error}</p>}
        {status === "connecting" && <p className="text-sm text-neutral-400">Connecting…</p>}

        {sources.length === 0 && status === "live" && (
          <p className="text-sm text-neutral-400">No live sources are being shared right now.</p>
        )}

        {sources.map((s) => {
          const f = frames[s.source];
          return (
            <div key={s.source} className="space-y-2 rounded-lg border border-neutral-800 bg-neutral-900 p-4">
              <div className="flex items-center justify-between">
                <span className="text-sm font-semibold text-neutral-200">{s.label || s.source}</span>
                <span className="text-xs text-neutral-500">{s.kind}</span>
              </div>
              {f?.image ? (
                <div className="flex aspect-video items-center justify-center overflow-hidden rounded-md border border-neutral-800 bg-black">
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img src={f.image} alt={s.source} className="max-h-full max-w-full object-contain" />
                </div>
              ) : (
                <p className="text-xs text-neutral-500">waiting for frames…</p>
              )}
              {(f?.title || f?.app) && (
                <div className="text-sm">
                  <div className="font-medium text-neutral-100">{f.title}</div>
                  <div className="text-xs text-neutral-400">{[f.artist, f.app, f.state].filter(Boolean).join(" · ")}</div>
                </div>
              )}
            </div>
          );
        })}

        <p className="pt-2 text-[11px] text-neutral-600">
          Shared with you via Yaver. View only — controls aren't available on this link.
        </p>
      </div>
    </div>
  );
}
