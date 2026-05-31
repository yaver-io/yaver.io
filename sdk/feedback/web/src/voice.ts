// Voice vibe coding for the WEB feedback SDK — speak a change, the agent
// acts (full MCP), and the reply is read back. Routes through the agent's
// WS /voice/stream, mirroring the react-native SDK so the wire protocol
// and the local-vs-flux semantics are identical across surfaces:
//
//   LOCAL = free whisper.cpp on the user's own dev machine (private —
//           audio never leaves their box) + browser SpeechSynthesis for
//           the spoken reply.
//   FLUX  = Deepgram nova-3 streaming STT + Deepgram Aura TTS (streamed
//           back as PCM and played via Web Audio).
//
// The browser can't set request headers on a WebSocket, so the bearer
// token is passed as ?access_token=… (the agent's wsQueryToken shim
// promotes it back into an Authorization header). Mic audio is captured
// with the Web Audio API and downsampled to the 16 kHz / 16-bit mono PCM
// the agent's STT expects.

export interface WebVoiceStartOpts {
  /** Agent base URL (http/https). Converted to ws/wss + /voice/stream. */
  agentUrl: string;
  accessToken: string;
  relayPassword?: string;
  project?: string;
  model?: string;
  runner?: string;
  surface?: string;
  ttsBudget?: number;
  /** "local" (whisper.cpp) | "deepgram" (Flux) | "" = agent default. */
  sttProvider?: string;
  ttsProvider?: string;
}

export interface WebVoiceCallbacks {
  onProviders?: (stt: string, tts: string) => void;
  onTranscriptPartial?: (text: string) => void;
  onTranscriptFinal?: (text: string) => void;
  onTaskCreated?: (taskId: string) => void;
  onTaskResult?: (taskId: string, text: string, status: string) => void;
  /** Cloud TTS only — assembled PCM ready to play. */
  onTTSReady?: (pcm: Uint8Array, sampleRate: number) => void;
  onError?: (msg: string) => void;
  onDone?: () => void;
}

/** True when the browser can do voice vibe coding (mic capture + WS). */
export function isWebVoiceSupported(): boolean {
  return (
    typeof navigator !== 'undefined' &&
    !!navigator.mediaDevices?.getUserMedia &&
    typeof WebSocket !== 'undefined' &&
    (typeof AudioContext !== 'undefined' || typeof (window as any).webkitAudioContext !== 'undefined')
  );
}

/** True when the browser can synthesize speech locally (for "local" TTS). */
export function isLocalTTSSupported(): boolean {
  return typeof window !== 'undefined' && 'speechSynthesis' in window;
}

// ── PCM mic capture ────────────────────────────────────────────────────

/** Captures the mic and emits 16 kHz / 16-bit mono PCM frames. Uses a
 *  ScriptProcessorNode — deprecated but universally supported and exactly
 *  what we need for raw-PCM streaming without an AudioWorklet build step. */
export class WebAudioPCMRecorder {
  private ctx: AudioContext | null = null;
  private stream: MediaStream | null = null;
  private source: MediaStreamAudioSourceNode | null = null;
  private node: ScriptProcessorNode | null = null;
  private onFrame: (pcm: ArrayBuffer) => void;

  constructor(onFrame: (pcm: ArrayBuffer) => void) {
    this.onFrame = onFrame;
  }

  async start(): Promise<void> {
    this.stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
    });
    const Ctor = (window.AudioContext || (window as any).webkitAudioContext) as typeof AudioContext;
    this.ctx = new Ctor();
    const inRate = this.ctx.sampleRate;
    this.source = this.ctx.createMediaStreamSource(this.stream);
    this.node = this.ctx.createScriptProcessor(4096, 1, 1);
    this.node.onaudioprocess = (e: AudioProcessingEvent) => {
      const input = e.inputBuffer.getChannelData(0);
      const pcm = floatTo16kInt16(input, inRate);
      if (pcm.byteLength > 0) this.onFrame(pcm);
    };
    this.source.connect(this.node);
    // Some browsers only fire onaudioprocess when the node is connected to
    // a destination; route through a muted gain so we don't echo the mic.
    const sink = this.ctx.createGain();
    sink.gain.value = 0;
    this.node.connect(sink);
    sink.connect(this.ctx.destination);
  }

  stop(): void {
    try { this.node?.disconnect(); } catch { /* ignore */ }
    try { this.source?.disconnect(); } catch { /* ignore */ }
    try { this.stream?.getTracks().forEach((t) => t.stop()); } catch { /* ignore */ }
    try { void this.ctx?.close(); } catch { /* ignore */ }
    this.node = null;
    this.source = null;
    this.stream = null;
    this.ctx = null;
  }
}

/** Linear-resample Float32 mic samples to 16 kHz and pack as Int16 LE. */
function floatTo16kInt16(input: Float32Array, inRate: number): ArrayBuffer {
  const outRate = 16000;
  const ratio = inRate / outRate;
  const outLen = ratio <= 1 ? input.length : Math.floor(input.length / ratio);
  const out = new Int16Array(outLen);
  for (let i = 0; i < outLen; i++) {
    const idx = i * ratio;
    const i0 = Math.floor(idx);
    const frac = idx - i0;
    const s = (input[i0] || 0) * (1 - frac) + (input[i0 + 1] || 0) * frac;
    out[i] = Math.max(-32768, Math.min(32767, Math.round(s * 32768)));
  }
  return out.buffer;
}

// ── WS session ─────────────────────────────────────────────────────────

interface WireMsg {
  type: string;
  text?: string;
  taskId?: string;
  status?: string;
  pcm?: string;
  sampleRate?: number;
  error?: string;
  stt?: string;
  tts?: string;
}

export class WebVoiceSession {
  private ws: WebSocket | null = null;
  private callbacks: WebVoiceCallbacks;
  private closed = false;
  private ttsChunks: Uint8Array[] = [];
  private ttsTotalBytes = 0;
  private ttsSampleRate = 22050;

  constructor(callbacks: WebVoiceCallbacks) {
    this.callbacks = callbacks;
  }

  async start(opts: WebVoiceStartOpts): Promise<void> {
    const wsBase = opts.agentUrl.replace(/^http/, 'ws').replace(/\/$/, '');
    const url = new URL(`${wsBase}/voice/stream`);
    url.searchParams.set('access_token', opts.accessToken);
    if (opts.relayPassword) url.searchParams.set('relay_password', opts.relayPassword);

    return new Promise((resolve, reject) => {
      let opened = false;
      let ws: WebSocket;
      try {
        ws = new WebSocket(url.toString());
      } catch (e) {
        reject(e);
        return;
      }
      ws.binaryType = 'arraybuffer';
      this.ws = ws;
      ws.onopen = () => {
        opened = true;
        try {
          ws.send(JSON.stringify({
            type: 'start',
            project: opts.project ?? '',
            model: opts.model ?? '',
            runner: opts.runner ?? '',
            surface: opts.surface ?? 'feedback-web',
            ttsBudget: opts.ttsBudget ?? 0,
            sttProvider: opts.sttProvider ?? '',
            ttsProvider: opts.ttsProvider ?? '',
          }));
          resolve();
        } catch (e) {
          reject(e);
        }
      };
      ws.onmessage = (e) => this.handleMessage(e);
      ws.onerror = () => {
        if (!opened) reject(new Error('voice WS connect failed'));
        else this.callbacks.onError?.('voice WS error');
      };
      ws.onclose = () => {
        this.closed = true;
        if (!opened) reject(new Error('voice WS closed before open'));
      };
    });
  }

  /** Send a raw PCM frame (binary). */
  sendAudio(pcm: ArrayBuffer): void {
    if (this.ws && !this.closed && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(pcm);
    }
  }

  /** Tell the server we're done speaking — flush STT and create the task. */
  finalize(): void {
    if (this.ws && !this.closed && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type: 'stop' }));
    }
  }

  close(): void {
    this.closed = true;
    try { this.ws?.close(); } catch { /* ignore */ }
    this.ws = null;
  }

  private handleMessage(e: MessageEvent): void {
    if (typeof e.data !== 'string') return; // all control frames are JSON text
    let msg: WireMsg;
    try { msg = JSON.parse(e.data); } catch { return; }
    switch (msg.type) {
      case 'providers':
        this.callbacks.onProviders?.(msg.stt ?? '', msg.tts ?? '');
        break;
      case 'transcript-partial':
        this.callbacks.onTranscriptPartial?.(msg.text ?? '');
        break;
      case 'transcript-final':
        this.callbacks.onTranscriptFinal?.(msg.text ?? '');
        break;
      case 'task-created':
        this.callbacks.onTaskCreated?.(msg.taskId ?? '');
        break;
      case 'task-result':
        this.callbacks.onTaskResult?.(msg.taskId ?? '', msg.text ?? '', msg.status ?? '');
        break;
      case 'tts-frame': {
        if (!msg.pcm) break;
        const bytes = base64ToBytes(msg.pcm);
        this.ttsChunks.push(bytes);
        this.ttsTotalBytes += bytes.length;
        if (msg.sampleRate) this.ttsSampleRate = msg.sampleRate;
        break;
      }
      case 'done': {
        if (this.ttsTotalBytes > 0) {
          const pcm = new Uint8Array(this.ttsTotalBytes);
          let off = 0;
          for (const c of this.ttsChunks) { pcm.set(c, off); off += c.length; }
          this.callbacks.onTTSReady?.(pcm, this.ttsSampleRate);
        }
        this.callbacks.onDone?.();
        this.close();
        break;
      }
      case 'error':
        this.callbacks.onError?.(msg.error ?? 'voice error');
        break;
    }
  }
}

// ── TTS playback ─────────────────────────────────────────────────────────

/** Play 16-bit LE mono PCM through Web Audio (cloud/Flux TTS path). */
export async function playPcm16(pcm: Uint8Array, sampleRate: number): Promise<void> {
  const Ctor = (window.AudioContext || (window as any).webkitAudioContext) as typeof AudioContext;
  const ctx = new Ctor();
  const samples = pcm.length >> 1;
  const view = new DataView(pcm.buffer, pcm.byteOffset, pcm.byteLength);
  const buffer = ctx.createBuffer(1, samples, sampleRate);
  const ch = buffer.getChannelData(0);
  for (let i = 0; i < samples; i++) {
    ch[i] = view.getInt16(i * 2, true) / 32768;
  }
  const src = ctx.createBufferSource();
  src.buffer = buffer;
  src.connect(ctx.destination);
  src.onended = () => { void ctx.close(); };
  src.start();
}

/** Speak text with the browser's built-in synthesizer (local TTS path). */
export function speakViaSynthesis(text: string): void {
  if (!text || !isLocalTTSSupported()) return;
  try {
    const headline = text.length > 280 ? `${text.slice(0, 280)} — see screen for the rest.` : text;
    window.speechSynthesis.cancel();
    window.speechSynthesis.speak(new SpeechSynthesisUtterance(headline));
  } catch { /* best-effort */ }
}

function base64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}
