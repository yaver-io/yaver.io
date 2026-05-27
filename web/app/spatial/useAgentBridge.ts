"use client";

/**
 * useAgentBridge — connects /spatial to a Yaver agent.
 *
 * URL params (passed when the user opens the spatial link from desktop):
 *   ?agent=https://primary.tail-xyz.ts.net:18080
 *   &token=<bearer SDK token, scope feedback,voice>
 *
 * The bridge handles:
 *   - Polling /tasks for the active session list
 *   - Per-task output polling (real SSE TBD — see project_voice_glasses_revival_2026_05_27)
 *   - Voice WebSocket to /voice/stream (mic in, transcript + TTS out)
 *
 * Mirrors mobile's quic.ts patterns but for a browser environment.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";

export type TaskStatus = "queued" | "running" | "review" | "completed" | "failed" | "stopped";

export interface Task {
  id: string;
  title: string;
  description: string;
  status: TaskStatus;
  output: string[];
  resultText?: string;
  source?: string;
  startedAt?: string;
  createdAt?: string;
  runnerId?: string;
  inputTokens?: number;
  outputTokens?: number;
}

export interface BridgeConfig {
  agentUrl: string;
  token: string;
  pollMs?: number;
}

export function readBridgeFromURL(): BridgeConfig | null {
  if (typeof window === "undefined") return null;
  const url = new URL(window.location.href);
  const agentUrl = url.searchParams.get("agent");
  const token = url.searchParams.get("token");
  if (!agentUrl || !token) return null;
  return { agentUrl: agentUrl.replace(/\/$/, ""), token };
}

export function useTasks(cfg: BridgeConfig | null): { tasks: Task[]; error: string } {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [error, setError] = useState("");
  const pollMs = cfg?.pollMs ?? 3500;

  useEffect(() => {
    if (!cfg) return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const res = await fetch(`${cfg.agentUrl}/tasks`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!res.ok) throw new Error(`tasks ${res.status}`);
        const list = (await res.json()) as Task[];
        if (cancelled) return;
        setTasks(list);
        setError("");
      } catch (e: any) {
        if (cancelled) return;
        setError(e?.message ?? "fetch failed");
      }
    };
    void refresh();
    const i = window.setInterval(refresh, pollMs);
    return () => {
      cancelled = true;
      window.clearInterval(i);
    };
  }, [cfg, pollMs]);

  return { tasks, error };
}

export interface TmuxSessionInfo {
  name: string;
  windows: number;
  created: string;
  attached: boolean;
  relationship?: string;
  agentType?: string;
  mainPid?: number;
  panePreview?: string;
  taskId?: string;
}

/** Poll the agent's /tmux/sessions list — used by /spatial to pick
 *  which sessions to attach to in the 3-pane layout. */
export function useTmuxSessions(cfg: BridgeConfig | null): { sessions: TmuxSessionInfo[]; error: string } {
  const [sessions, setSessions] = useState<TmuxSessionInfo[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!cfg) return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const res = await fetch(`${cfg.agentUrl}/tmux/sessions`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!res.ok) throw new Error(`tmux ${res.status}`);
        const body = (await res.json()) as { sessions: TmuxSessionInfo[] };
        if (cancelled) return;
        setSessions(body.sessions ?? []);
        setError("");
      } catch (e: any) {
        if (cancelled) return;
        setError(e?.message ?? "fetch failed");
      }
    };
    void refresh();
    const i = window.setInterval(refresh, 4000);
    return () => { cancelled = true; window.clearInterval(i); };
  }, [cfg]);

  return { sessions, error };
}

// One row from /remote-runtime/sessions filtered to browser-window
// runs. Each row maps to one floating RemoteWindow3D quad in the VR
// scene. The agent's session object carries more fields (transport,
// dims, …); we project only what the viewer needs to render.
export interface GlassPCSession {
  id: string;
  deviceId: string;
  framework: string;
  targetId: string;
  status: string;
  url?: string;
  title?: string;
}

/** Poll /remote-runtime/sessions and surface browser-window rows so
 *  the VR scene can render them as floating browser quads. */
export function useGlassPCSessions(cfg: BridgeConfig | null): {
  sessions: GlassPCSession[];
  error: string;
} {
  const [sessions, setSessions] = useState<GlassPCSession[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!cfg) return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const res = await fetch(`${cfg.agentUrl}/remote-runtime/sessions`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!res.ok) throw new Error(`remote-runtime ${res.status}`);
        const body = (await res.json()) as { sessions: any[] };
        if (cancelled) return;
        const list = (body.sessions ?? [])
          .filter((s) => s?.framework === "browser" && s?.targetId === "browser-window")
          .map((s) => ({
            id: String(s.id),
            deviceId: String(s.deviceId ?? ""),
            framework: String(s.framework),
            targetId: String(s.targetId),
            status: String(s.status ?? ""),
            url: typeof s.note === "string" && s.note.startsWith("navigated to ") ? s.note.slice(13) : undefined,
            title: undefined,
          }));
        setSessions(list);
        setError("");
      } catch (e: any) {
        if (cancelled) return;
        setError(e?.message ?? "fetch failed");
      }
    };
    void refresh();
    const i = window.setInterval(refresh, 5000);
    return () => {
      cancelled = true;
      window.clearInterval(i);
    };
  }, [cfg]);

  return { sessions, error };
}

export function useTaskDetail(cfg: BridgeConfig | null, taskId: string | null): { task: Task | null; error: string } {
  const [task, setTask] = useState<Task | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!cfg || !taskId) { setTask(null); return; }
    let cancelled = false;
    const refresh = async () => {
      try {
        const res = await fetch(`${cfg.agentUrl}/tasks/${encodeURIComponent(taskId)}`, {
          headers: { Authorization: `Bearer ${cfg.token}` },
        });
        if (!res.ok) throw new Error(`task ${res.status}`);
        const t = (await res.json()) as Task;
        if (cancelled) return;
        setTask(t);
        setError("");
      } catch (e: any) {
        if (cancelled) return;
        setError(e?.message ?? "fetch failed");
      }
    };
    void refresh();
    const i = window.setInterval(refresh, 1500);
    return () => {
      cancelled = true;
      window.clearInterval(i);
    };
  }, [cfg, taskId]);

  return { task, error };
}

// ───────────────────────────── Voice WS ─────────────────────────────

// Surfaced here so VR / 2D / Mentra components all import the same
// enum + controller shape.
export type VoiceStatus = "idle" | "connecting" | "recording" | "uploading" | "thinking" | "speaking" | "error";

export interface VoiceState {
  status: VoiceStatus;
  transcript: string;
  resultText: string;
  taskId: string;
  errorMsg: string;
}

export interface VoiceController {
  state: VoiceState;
  start: () => Promise<void>;
  stop: () => Promise<void>;
  cancel: () => void;
}

export function useVoiceBridge(cfg: BridgeConfig | null): VoiceController {
  const [state, setState] = useState<VoiceState>({
    status: "idle",
    transcript: "",
    resultText: "",
    taskId: "",
    errorMsg: "",
  });
  const wsRef = useRef<WebSocket | null>(null);
  const mediaRef = useRef<MediaRecorder | null>(null);
  const streamRef = useRef<MediaStream | null>(null);
  const ttsChunksRef = useRef<Uint8Array[]>([]);
  const ttsSampleRateRef = useRef<number>(22050);

  const cancel = useCallback(() => {
    try { mediaRef.current?.stop(); } catch {}
    try { streamRef.current?.getTracks().forEach((t) => t.stop()); } catch {}
    try { wsRef.current?.close(); } catch {}
    mediaRef.current = null;
    streamRef.current = null;
    wsRef.current = null;
    ttsChunksRef.current = [];
    setState((s) => ({ ...s, status: "idle", transcript: "", resultText: "", taskId: "", errorMsg: "" }));
  }, []);

  const start = useCallback(async () => {
    if (!cfg) {
      setState((s) => ({ ...s, status: "error", errorMsg: "no agent configured" }));
      return;
    }
    setState((s) => ({ ...s, status: "connecting", transcript: "", resultText: "", taskId: "", errorMsg: "" }));
    ttsChunksRef.current = [];

    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, sampleRate: 16000 } });
      streamRef.current = stream;
    } catch (e: any) {
      setState((s) => ({ ...s, status: "error", errorMsg: `mic: ${e?.message ?? e}` }));
      return;
    }

    const wsUrl = cfg.agentUrl.replace(/^http/, "ws") + "/voice/stream";
    // Browsers don't support custom WS headers — fall back to query token.
    const wsWithToken = wsUrl + (wsUrl.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(cfg.token);
    const ws = new WebSocket(wsWithToken);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    await new Promise<void>((resolve, reject) => {
      const onErr = () => reject(new Error("WS connect failed"));
      ws.addEventListener("error", onErr, { once: true });
      ws.addEventListener("open", () => {
        ws.removeEventListener("error", onErr);
        resolve();
      }, { once: true });
    });

    // Detect surface for the prompt wrapper (mirrors useViewportClass
    // in page.tsx but inline so we don't depend on React state here).
    const w = typeof window !== "undefined" ? window.innerWidth : 0;
    const surface = w <= 800 ? "web-spatial-hud" : w <= 1600 ? "web-spatial-hud" : "web-spatial-vr";
    ws.send(JSON.stringify({
      type: "start",
      project: "",
      model: "",
      runner: "",
      surface,
      paneCount: surface === "web-spatial-vr" ? 3 : 1,
      ttsBudget: 280,
    }));

    ws.onmessage = (e) => handleVoiceMsg(e, setState, ttsChunksRef, ttsSampleRateRef);
    ws.onclose = () => {
      if (ttsChunksRef.current.length > 0) {
        playAccumulatedPCM(ttsChunksRef.current, ttsSampleRateRef.current).catch(() => {});
      }
    };

    // MediaRecorder pumps blobs we relay as binary WS frames.
    const mr = new MediaRecorder(streamRef.current!, { mimeType: pickMimeType() });
    mediaRef.current = mr;
    mr.ondataavailable = async (ev) => {
      if (ev.data && ev.data.size > 0 && ws.readyState === 1) {
        const buf = await ev.data.arrayBuffer();
        ws.send(buf);
      }
    };
    mr.start(250); // emit every 250ms
    setState((s) => ({ ...s, status: "recording" }));
  }, [cfg]);

  const stop = useCallback(async () => {
    try {
      mediaRef.current?.stop();
    } catch {}
    try {
      streamRef.current?.getTracks().forEach((t) => t.stop());
    } catch {}
    if (wsRef.current?.readyState === 1) {
      wsRef.current.send(JSON.stringify({ type: "stop" }));
    }
    setState((s) => ({ ...s, status: "uploading" }));
  }, []);

  useEffect(() => () => cancel(), [cancel]);

  return useMemo(() => ({ state, start, stop, cancel }), [state, start, stop, cancel]);
}

function pickMimeType(): string {
  // Quest Browser + Vision Pro Safari + modern Chrome agree on webm/opus.
  // Backend's Deepgram stream config (linear16 16kHz) won't decode this
  // directly — Phase 2 work will be a small Opus→PCM bridge in the agent.
  // For v1 the WS gracefully drops audio frames the backend can't parse,
  // but we still get TTS + result loops working.
  const candidates = ["audio/webm;codecs=opus", "audio/ogg;codecs=opus", "audio/mp4"];
  for (const c of candidates) {
    try {
      if (typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported(c)) return c;
    } catch {}
  }
  return "";
}

interface VoiceMsg {
  type: string;
  text?: string;
  taskId?: string;
  status?: string;
  pcm?: string;
  sampleRate?: number;
  error?: string;
}

function handleVoiceMsg(
  e: MessageEvent,
  setState: (fn: (prev: VoiceState) => VoiceState) => void,
  ttsChunksRef: React.MutableRefObject<Uint8Array[]>,
  ttsSampleRateRef: React.MutableRefObject<number>,
): void {
  let msg: VoiceMsg;
  try {
    msg = JSON.parse(typeof e.data === "string" ? e.data : "");
  } catch {
    return;
  }
  switch (msg.type) {
    case "transcript-partial":
      setState((s) => ({ ...s, transcript: msg.text ?? s.transcript }));
      break;
    case "transcript-final":
      setState((s) => ({ ...s, transcript: msg.text ?? s.transcript, status: "thinking" }));
      break;
    case "task-created":
      setState((s) => ({ ...s, taskId: msg.taskId ?? s.taskId }));
      break;
    case "task-result":
      setState((s) => ({ ...s, resultText: msg.text ?? s.resultText, status: "speaking" }));
      break;
    case "tts-frame":
      if (msg.pcm) {
        const bin = atob(msg.pcm);
        const pcm = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) pcm[i] = bin.charCodeAt(i);
        ttsChunksRef.current.push(pcm);
        if (msg.sampleRate) ttsSampleRateRef.current = msg.sampleRate;
      }
      break;
    case "done":
      setState((s) => ({ ...s, status: "idle" }));
      break;
    case "error":
      setState((s) => ({ ...s, status: "error", errorMsg: msg.error ?? "voice error" }));
      break;
  }
}

async function playAccumulatedPCM(chunks: Uint8Array[], sampleRate: number): Promise<void> {
  if (chunks.length === 0) return;
  const totalLen = chunks.reduce((n, c) => n + c.length, 0);
  const merged = new Uint8Array(totalLen);
  let off = 0;
  for (const c of chunks) {
    merged.set(c, off);
    off += c.length;
  }
  // PCM s16le → Float32 [-1, 1]
  const samples = merged.length / 2;
  const dv = new DataView(merged.buffer, merged.byteOffset, merged.byteLength);
  const f32 = new Float32Array(samples);
  for (let i = 0; i < samples; i++) {
    f32[i] = dv.getInt16(i * 2, true) / 0x8000;
  }
  const AC = (window.AudioContext || (window as any).webkitAudioContext) as typeof AudioContext;
  const ctx = new AC({ sampleRate });
  const buf = ctx.createBuffer(1, samples, sampleRate);
  buf.copyToChannel(f32, 0, 0);
  const src = ctx.createBufferSource();
  src.buffer = buf;
  src.connect(ctx.destination);
  src.start(0);
  await new Promise<void>((res) => { src.onended = () => res(); });
  await ctx.close();
}
