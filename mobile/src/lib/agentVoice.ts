/**
 * Agent-voice WebSocket client — drives the hands-free agent loop.
 *
 * Backend route: WS /voice/stream (desktop/agent/voice_http.go).
 *
 * Flow:
 *   1. start() opens the WS, sends {type:"start", project, model, runner}
 *   2. streamAudioFile() reads a recorded audio file in chunks and
 *      pushes each as a binary WS frame (Deepgram-side STT receives them)
 *   3. finalize() emits {type:"stop"} so the server flushes Deepgram
 *   4. callbacks fire as transcript-partial / transcript-final /
 *      task-created / task-result / tts-frame messages arrive
 *   5. tts-frame frames are accumulated; on "done" we assemble a WAV
 *      file and play it back via expo-av
 *
 * This is intentionally a small, focused class. The mic-button UI in
 * AgentVoiceButton.tsx owns recording state — agentVoice.ts only
 * cares about the wire protocol.
 */

import { Buffer } from "buffer";
import * as FileSystem from "expo-file-system/legacy";
import { quicClient } from "./quic";

export interface AgentVoiceStartOpts {
  project?: string;
  model?: string;
  runner?: string;
  /** Surface hint for the agent's prompt wrapper (see Go-side TaskViewport).
   *  Examples: "mobile-phone", "mobile-tablet", "web-spatial-hud". */
  surface?: string;
  /** Dominant input mode: voice, dpad, touch, keyboard, approval, stream. */
  interaction?: string;
  /** How many parallel Claude sessions the user has visible. */
  paneCount?: number;
  /** Max chars for TTS readback (Cartesia default ~280). */
  ttsBudget?: number;
  /** none, glance, panel, or full. */
  visualBudget?: string;
  /** normal, driving, watch, shared-tv, or mcp. */
  riskPolicy?: string;
}

export interface AgentVoiceCallbacks {
  onTranscriptPartial?: (text: string) => void;
  onTranscriptFinal?: (text: string) => void;
  onTaskCreated?: (taskId: string) => void;
  onTaskResult?: (taskId: string, text: string, status: string) => void;
  /** Cumulative PCM written so far; the player decides when to render. */
  onTTSProgress?: (totalBytes: number) => void;
  /** Final PCM blob assembled from all tts-frame messages. */
  onTTSReady?: (pcm: Uint8Array, sampleRate: number) => void;
  onError?: (msg: string) => void;
  onDone?: () => void;
}

interface WireMsg {
  type: string;
  text?: string;
  taskId?: string;
  status?: string;
  pcm?: string;
  sampleRate?: number;
  error?: string;
}

export class AgentVoiceSession {
  private ws: WebSocket | null = null;
  private callbacks: AgentVoiceCallbacks;
  private ttsChunks: Uint8Array[] = [];
  private ttsTotalBytes = 0;
  private ttsSampleRate = 22050;
  private closed = false;

  constructor(callbacks: AgentVoiceCallbacks) {
    this.callbacks = callbacks;
  }

  /** Open the WS and send the start frame. Resolves once the WS is open. */
  async start(opts: AgentVoiceStartOpts = {}): Promise<void> {
    const baseUrl = quicClient.baseUrl;
    const wsUrl =
      baseUrl.replace(/^https?:/, baseUrl.startsWith("https") ? "wss:" : "ws:") +
      "/voice/stream";
    const headers = quicClient.getAuthHeaders();

    return new Promise((resolve, reject) => {
      let opened = false;
      let ws: WebSocket;
      try {
        // RN-specific 3rd arg with headers — typing lags the runtime
        ws = new (WebSocket as any)(wsUrl, undefined, { headers });
      } catch (e) {
        reject(e);
        return;
      }
      this.ws = ws;

      ws.onopen = () => {
        opened = true;
        try {
          ws.send(
            JSON.stringify({
              type: "start",
              project: opts.project ?? "",
              model: opts.model ?? "",
              runner: opts.runner ?? "",
              surface: opts.surface ?? "",
              interaction: opts.interaction ?? "",
              paneCount: opts.paneCount ?? 0,
              ttsBudget: opts.ttsBudget ?? 0,
              visualBudget: opts.visualBudget ?? "",
              riskPolicy: opts.riskPolicy ?? "",
            }),
          );
          resolve();
        } catch (e) {
          reject(e);
        }
      };
      ws.onmessage = (e) => this.handleMessage(e);
      ws.onerror = () => {
        if (!opened) reject(new Error("voice WS connect failed"));
        else this.callbacks.onError?.("voice WS error");
      };
      ws.onclose = () => {
        this.closed = true;
        if (!opened) reject(new Error("voice WS closed before open"));
      };
    });
  }

  /**
   * Stream a recorded audio file as binary WS frames. The file must
   * already be in the format the backend → Deepgram expects:
   * raw PCM 16-bit little-endian, 16kHz mono — i.e. a WAV file with
   * the first 44 bytes (header) stripped, OR a raw PCM file.
   */
  async streamAudioFile(uri: string, opts?: { skipWavHeader?: boolean; chunkBytes?: number }): Promise<void> {
    if (!this.ws || this.closed) throw new Error("voice WS not open");
    const skipWavHeader = opts?.skipWavHeader ?? true;
    const chunkBytes = opts?.chunkBytes ?? 16384;

    const b64 = await FileSystem.readAsStringAsync(uri, { encoding: FileSystem.EncodingType.Base64 });
    let buf = Buffer.from(b64, "base64");
    if (skipWavHeader && buf.length > 44 && buf.slice(0, 4).toString() === "RIFF") {
      buf = buf.slice(44);
    }
    for (let i = 0; i < buf.length; i += chunkBytes) {
      if (this.closed) return;
      const slice = buf.slice(i, Math.min(i + chunkBytes, buf.length));
      // RN's WebSocket accepts ArrayBuffer for binary frames.
      this.ws.send(slice.buffer.slice(slice.byteOffset, slice.byteOffset + slice.byteLength));
      // Small yield so the bridge doesn't stall on large uploads
      await new Promise((r) => setTimeout(r, 0));
    }
  }

  /** Tell the server we're done speaking — it flushes STT and creates the task. */
  finalize(): void {
    if (this.ws && !this.closed) {
      this.ws.send(JSON.stringify({ type: "stop" }));
    }
  }

  close(): void {
    this.closed = true;
    try {
      this.ws?.close();
    } catch {
      // ignore
    }
    this.ws = null;
  }

  private handleMessage(e: WebSocketMessageEvent): void {
    let msg: WireMsg;
    try {
      msg = JSON.parse(typeof e.data === "string" ? e.data : "");
    } catch {
      return;
    }
    switch (msg.type) {
      case "transcript-partial":
        this.callbacks.onTranscriptPartial?.(msg.text ?? "");
        break;
      case "transcript-final":
        this.callbacks.onTranscriptFinal?.(msg.text ?? "");
        break;
      case "task-created":
        this.callbacks.onTaskCreated?.(msg.taskId ?? "");
        break;
      case "task-result":
        this.callbacks.onTaskResult?.(msg.taskId ?? "", msg.text ?? "", msg.status ?? "");
        break;
      case "tts-frame": {
        if (!msg.pcm) break;
        const pcm = Buffer.from(msg.pcm, "base64");
        const arr = new Uint8Array(pcm);
        this.ttsChunks.push(arr);
        this.ttsTotalBytes += arr.length;
        if (msg.sampleRate) this.ttsSampleRate = msg.sampleRate;
        this.callbacks.onTTSProgress?.(this.ttsTotalBytes);
        break;
      }
      case "done": {
        if (this.ttsTotalBytes > 0) {
          const pcm = new Uint8Array(this.ttsTotalBytes);
          let off = 0;
          for (const c of this.ttsChunks) {
            pcm.set(c, off);
            off += c.length;
          }
          this.callbacks.onTTSReady?.(pcm, this.ttsSampleRate);
        }
        this.callbacks.onDone?.();
        this.close();
        break;
      }
      case "error":
        this.callbacks.onError?.(msg.error ?? "voice error");
        break;
    }
  }
}

/**
 * Wrap raw PCM (signed 16-bit LE) in a minimal WAV container so it can
 * be handed to expo-av's Audio.Sound.createAsync without a custom
 * decoder. 44-byte header + interleaved samples.
 */
export function wrapPCMAsWAV(pcm: Uint8Array, sampleRate: number, channels = 1): Uint8Array {
  const bitsPerSample = 16;
  const byteRate = (sampleRate * channels * bitsPerSample) / 8;
  const blockAlign = (channels * bitsPerSample) / 8;
  const dataSize = pcm.length;
  const headerSize = 44;
  const buf = new Uint8Array(headerSize + dataSize);
  const dv = new DataView(buf.buffer);

  // "RIFF" chunk
  buf[0] = 0x52; buf[1] = 0x49; buf[2] = 0x46; buf[3] = 0x46; // "RIFF"
  dv.setUint32(4, 36 + dataSize, true);
  buf[8] = 0x57; buf[9] = 0x41; buf[10] = 0x56; buf[11] = 0x45; // "WAVE"
  // "fmt " sub-chunk
  buf[12] = 0x66; buf[13] = 0x6d; buf[14] = 0x74; buf[15] = 0x20; // "fmt "
  dv.setUint32(16, 16, true);
  dv.setUint16(20, 1, true); // PCM
  dv.setUint16(22, channels, true);
  dv.setUint32(24, sampleRate, true);
  dv.setUint32(28, byteRate, true);
  dv.setUint16(32, blockAlign, true);
  dv.setUint16(34, bitsPerSample, true);
  // "data" sub-chunk
  buf[36] = 0x64; buf[37] = 0x61; buf[38] = 0x74; buf[39] = 0x61; // "data"
  dv.setUint32(40, dataSize, true);
  buf.set(pcm, 44);
  return buf;
}

/** Write a PCM buffer to a temp WAV file and return its file:// URI. */
export async function pcmToTempWavURI(pcm: Uint8Array, sampleRate: number): Promise<string> {
  const wav = wrapPCMAsWAV(pcm, sampleRate);
  const b64 = Buffer.from(wav).toString("base64");
  const path = (FileSystem.cacheDirectory ?? FileSystem.documentDirectory ?? "") + `voice-tts-${Date.now()}.wav`;
  await FileSystem.writeAsStringAsync(path, b64, { encoding: FileSystem.EncodingType.Base64 });
  return path;
}
