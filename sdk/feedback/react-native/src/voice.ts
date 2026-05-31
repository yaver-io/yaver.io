// Voice vibe-coding for the feedback SDK — speak a change, the agent
// acts (full MCP), and reads back a spoken headline. Ported from the
// Yaver mobile app's AgentVoiceSession (mobile/src/lib/agentVoice.ts) but
// decoupled from the app: connection details (WS URL + auth headers) come
// from the SDK's P2PClient, so it works both inside the Yaver container
// (inherited auth) and standalone in a third-party app.
//
// Local vs remote STT/TTS is decided entirely agent-side (free local
// whisper.cpp by default, Deepgram/OpenAI/etc when keys are configured),
// so nothing model-related is bundled here — the SDK just streams mic
// audio and renders the transcript + TTS the agent sends back.
//
// expo-file-system + buffer are loaded lazily so a bare-RN app that
// hasn't installed them can still use the rest of the SDK; the voice
// button hides itself via isVoiceStreamSupported() when they're absent.

export interface SDKVoiceStartOpts {
  /** WS URL for the agent voice stream — P2PClient.voiceStreamUrl(). */
  wsUrl: string;
  /** Auth headers — P2PClient.voiceAuthHeaders(). */
  headers: Record<string, string>;
  project?: string;
  model?: string;
  runner?: string;
  /** Surface hint for the agent's prompt wrapper. */
  surface?: string;
  /** Max chars for the spoken readback (Cartesia default ~280). */
  ttsBudget?: number;
  /** Per-session STT engine. "" = agent default. "local" = free
   *  whisper.cpp on the host; "deepgram" = Flux nova-3 streaming. */
  sttProvider?: string;
  /** Per-session TTS engine. "" = agent default. "local"/"device" =
   *  client synthesizes from the result text; cloud engines stream PCM. */
  ttsProvider?: string;
}

export interface SDKVoiceCallbacks {
  /** Active engines, echoed by the agent right after start — lets the UI
   *  show "Local" vs "Flux". */
  onProviders?: (stt: string, tts: string) => void;
  onTranscriptPartial?: (text: string) => void;
  onTranscriptFinal?: (text: string) => void;
  onTaskCreated?: (taskId: string) => void;
  onTaskResult?: (taskId: string, text: string, status: string) => void;
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
  stt?: string;
  tts?: string;
}

function loadBuffer(): any {
  // eslint-disable-next-line @typescript-eslint/no-var-requires
  return require('buffer').Buffer;
}

function loadFS(): any {
  // expo-file-system v16+ moved the classic API under /legacy; fall back
  // to the root export for older installs.
  try {
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    return require('expo-file-system/legacy');
  } catch {
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    return require('expo-file-system');
  }
}

/** True when the deps the voice stream needs (expo-file-system + buffer)
 *  are installed. The feedback UI hides the mic button otherwise. */
export function isVoiceStreamSupported(): boolean {
  try {
    loadBuffer();
    const fs = loadFS();
    return typeof fs?.readAsStringAsync === 'function';
  } catch {
    return false;
  }
}

export class SDKVoiceSession {
  private ws: WebSocket | null = null;
  private callbacks: SDKVoiceCallbacks;
  private ttsChunks: Uint8Array[] = [];
  private ttsTotalBytes = 0;
  private ttsSampleRate = 22050;
  private closed = false;

  constructor(callbacks: SDKVoiceCallbacks) {
    this.callbacks = callbacks;
  }

  /** Open the WS and send the start frame. Resolves once open. */
  async start(opts: SDKVoiceStartOpts): Promise<void> {
    return new Promise((resolve, reject) => {
      let opened = false;
      let ws: WebSocket;
      try {
        // RN-specific 3rd arg carries request headers.
        ws = new (WebSocket as any)(opts.wsUrl, undefined, { headers: opts.headers });
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
              type: 'start',
              project: opts.project ?? '',
              model: opts.model ?? '',
              runner: opts.runner ?? '',
              surface: opts.surface ?? '',
              ttsBudget: opts.ttsBudget ?? 0,
              sttProvider: opts.sttProvider ?? '',
              ttsProvider: opts.ttsProvider ?? '',
            }),
          );
          resolve();
        } catch (e) {
          reject(e);
        }
      };
      ws.onmessage = (e: any) => this.handleMessage(e);
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

  /** Stream a recorded WAV/PCM file as binary frames. Records produced by
   *  recordPcmWav() are LPCM 16-bit/16kHz mono with a 44-byte RIFF header
   *  we strip here — the exact shape the backend → STT expects. */
  async streamAudioFile(uri: string, opts?: { skipWavHeader?: boolean; chunkBytes?: number }): Promise<void> {
    if (!this.ws || this.closed) throw new Error('voice WS not open');
    const Buffer = loadBuffer();
    const FileSystem = loadFS();
    const skipWavHeader = opts?.skipWavHeader ?? true;
    const chunkBytes = opts?.chunkBytes ?? 16384;

    const b64 = await FileSystem.readAsStringAsync(uri, { encoding: 'base64' });
    let buf = Buffer.from(b64, 'base64');
    if (skipWavHeader && buf.length > 44 && buf.slice(0, 4).toString() === 'RIFF') {
      buf = buf.slice(44);
    }
    for (let i = 0; i < buf.length; i += chunkBytes) {
      if (this.closed) return;
      const slice = buf.slice(i, Math.min(i + chunkBytes, buf.length));
      this.ws.send(slice.buffer.slice(slice.byteOffset, slice.byteOffset + slice.byteLength));
      await new Promise((r) => setTimeout(r, 0));
    }
  }

  /** Done speaking — flush STT and create the agent task. */
  finalize(): void {
    if (this.ws && !this.closed) {
      this.ws.send(JSON.stringify({ type: 'stop' }));
    }
  }

  close(): void {
    this.closed = true;
    try { this.ws?.close(); } catch { /* ignore */ }
    this.ws = null;
  }

  private handleMessage(e: { data: any }): void {
    let msg: WireMsg;
    try {
      msg = JSON.parse(typeof e.data === 'string' ? e.data : '');
    } catch {
      return;
    }
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
        const Buffer = loadBuffer();
        const arr = new Uint8Array(Buffer.from(msg.pcm, 'base64'));
        this.ttsChunks.push(arr);
        this.ttsTotalBytes += arr.length;
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

/** Wrap raw PCM (signed 16-bit LE) in a minimal WAV container for
 *  expo-av playback. 44-byte header + samples. */
export function wrapPCMAsWAV(pcm: Uint8Array, sampleRate: number, channels = 1): Uint8Array {
  const bitsPerSample = 16;
  const byteRate = (sampleRate * channels * bitsPerSample) / 8;
  const blockAlign = (channels * bitsPerSample) / 8;
  const dataSize = pcm.length;
  const buf = new Uint8Array(44 + dataSize);
  const dv = new DataView(buf.buffer);
  buf[0] = 0x52; buf[1] = 0x49; buf[2] = 0x46; buf[3] = 0x46; // RIFF
  dv.setUint32(4, 36 + dataSize, true);
  buf[8] = 0x57; buf[9] = 0x41; buf[10] = 0x56; buf[11] = 0x45; // WAVE
  buf[12] = 0x66; buf[13] = 0x6d; buf[14] = 0x74; buf[15] = 0x20; // "fmt "
  dv.setUint32(16, 16, true);
  dv.setUint16(20, 1, true);
  dv.setUint16(22, channels, true);
  dv.setUint32(24, sampleRate, true);
  dv.setUint32(28, byteRate, true);
  dv.setUint16(32, blockAlign, true);
  dv.setUint16(34, bitsPerSample, true);
  buf[36] = 0x64; buf[37] = 0x61; buf[38] = 0x74; buf[39] = 0x61; // data
  dv.setUint32(40, dataSize, true);
  buf.set(pcm, 44);
  return buf;
}

/** Write a PCM buffer to a temp WAV file; returns its file:// URI. */
export async function pcmToTempWavURI(pcm: Uint8Array, sampleRate: number): Promise<string> {
  const Buffer = loadBuffer();
  const FileSystem = loadFS();
  const wav = wrapPCMAsWAV(pcm, sampleRate);
  const b64 = Buffer.from(wav).toString('base64');
  const dir = FileSystem.cacheDirectory ?? FileSystem.documentDirectory ?? '';
  const path = `${dir}yaver-voice-tts-${Date.now()}.wav`;
  await FileSystem.writeAsStringAsync(path, b64, { encoding: 'base64' });
  return path;
}
