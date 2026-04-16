"use client";

import { useState, useEffect, useRef } from "react";
import { agentClient } from "@/lib/agent-client";

interface PreviewTarget {
  id: string;
  name: string;
}

export default function PreviewPane({
  selectedPreviewTarget,
  onSelectPreviewTarget,
  mobileWorkers,
}: {
  selectedPreviewTarget: PreviewTarget | null;
  onSelectPreviewTarget: (deviceId: string | null) => void;
  mobileWorkers: PreviewTarget[];
}) {
  const [devStatus, setDevStatus] = useState<{
    running: boolean;
    framework?: string;
    workDir?: string;
    targetDeviceName?: string;
  } | null>(null);
  const [iframeKey, setIframeKey] = useState(0);
  const iframeRef = useRef<HTMLIFrameElement>(null);

  useEffect(() => {
    const poll = async () => {
      try { setDevStatus(await agentClient.getDevServerStatus()); } catch {}
    };
    poll();
    const interval = setInterval(poll, 3000);
    return () => clearInterval(interval);
  }, []);

  // SSE for live reload
  useEffect(() => {
    if (!devStatus?.running) return;
    const previewUrl = agentClient.devPreviewUrl;
    if (!previewUrl) return;

    const controller = new AbortController();
    (async () => {
      try {
        const eventsUrl = agentClient.devEventsUrl;
        if (!eventsUrl) return;
        const res = await fetch(eventsUrl, {
          headers: agentClient.getAuthHeaders(),
          signal: controller.signal,
        });
        const reader = res.body?.getReader();
        if (!reader) return;
        const decoder = new TextDecoder();
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          for (const line of decoder.decode(value).split("\n")) {
            if (line.startsWith("data: ")) {
              try {
                const ev = JSON.parse(line.slice(6));
                if (ev.type === "reload" || ev.type === "ready") {
                  setIframeKey((k) => k + 1);
                }
              } catch {}
            }
          }
        }
      } catch {}
    })();
    return () => controller.abort();
  }, [devStatus?.running]);

  const previewUrl = agentClient.devPreviewUrl;

  function handleReload() {
    setIframeKey((k) => k + 1);
    agentClient.reloadDevServer();
  }

  async function handleStop() {
    await agentClient.stopDevServer();
    setDevStatus(null);
  }

  if (!devStatus?.running || !previewUrl) {
    return (
      <div className="flex flex-col items-center justify-center h-full text-surface-500 gap-3">
        <div className="text-4xl opacity-20">&#x1F3A8;</div>
        <div className="text-sm">App preview appears here</div>
        <div className="text-xs text-surface-600 max-w-[250px] text-center leading-relaxed">
          Start a dev server from the Projects tab or ask the AI to build something
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <div className="h-9 flex items-center px-3 gap-2 border-b border-surface-800 bg-surface-900/50 shrink-0">
        <span className="flex-1 text-[10px] text-surface-500 font-mono truncate">{previewUrl}</span>
        <span className="text-[10px] text-emerald-400">{devStatus.framework}</span>
        <span className="text-[10px] text-sky-300">
          {devStatus.targetDeviceName || selectedPreviewTarget?.name || "current device"}
        </span>
        <button onClick={handleReload} className="text-surface-400 hover:text-surface-200 text-sm" title="Reload">&#x21BB;</button>
        <button onClick={handleStop} className="text-red-400 hover:text-red-300 text-sm" title="Stop">&#x25A0;</button>
      </div>
      {mobileWorkers.length > 0 && (
        <div className="flex items-center gap-2 px-3 py-2 border-b border-surface-800 bg-surface-950/60 overflow-x-auto">
          <span className="text-[10px] uppercase tracking-widest text-surface-500 shrink-0">Target</span>
          <button
            onClick={() => onSelectPreviewTarget(null)}
            className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
              !selectedPreviewTarget ? "border-sky-500/40 bg-sky-500/10 text-sky-300" : "border-surface-800 text-surface-500"
            }`}
          >
            Current device
          </button>
          {mobileWorkers.map((device) => (
            <button
              key={device.id}
              onClick={() => onSelectPreviewTarget(device.id)}
              className={`px-2 py-1 text-[10px] rounded border shrink-0 ${
                selectedPreviewTarget?.id === device.id ? "border-sky-500/40 bg-sky-500/10 text-sky-300" : "border-surface-800 text-surface-500"
              }`}
            >
              {device.name}
            </button>
          ))}
        </div>
      )}
      <iframe
        key={iframeKey}
        ref={iframeRef}
        src={previewUrl}
        className="flex-1 w-full border-none bg-white"
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
      />
    </div>
  );
}
