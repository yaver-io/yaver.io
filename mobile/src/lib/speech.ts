/**
 * Speech-to-text module — supports on-device (whisper.rn) and cloud providers.
 *
 * On-device: Uses whisper.rn (whisper.cpp) with the tiny model (~75MB).
 *            Downloads the model on first use. No API key needed.
 *
 * Cloud:     OpenAI (gpt-4o-mini-transcribe), Deepgram (Nova-2), AssemblyAI.
 *            User provides their own API key.
 */

import { Platform } from "react-native";
import type { SpeechProvider, TtsProvider } from "./auth";

// ── Types ────────────────────────────────────────────────────────────

export interface TranscriptionResult {
  text: string;
  durationMs: number;
}

export interface SpeechConfig {
  provider: SpeechProvider;
  apiKey?: string;
}

export interface TextToSpeechConfig {
  provider: TtsProvider;
  apiKey?: string;
}

// ── On-device (whisper.rn) ───────────────────────────────────────────

let whisperContext: any = null;
let isModelReady = false;
let isInitializing = false;

const MODEL_FILENAME = "ggml-whisper-tiny.bin";

/** Model is bundled in app — always available. */
export async function isWhisperModelDownloaded(): Promise<boolean> {
  return true;
}

/** No download needed — model is bundled. */
export function getWhisperDownloadState(): { isDownloading: boolean; progress: number } {
  return { isDownloading: false, progress: 1 };
}

/**
 * Initialize whisper.rn with the bundled quantized tiny model (~31MB).
 */
export async function initWhisper(
  _onProgress?: (progress: number) => void
): Promise<void> {
  if (isModelReady && whisperContext) return;
  if (isInitializing) return;
  isInitializing = true;

  try {
    const { initWhisper: rnInitWhisper } = require("whisper.rn");

    whisperContext = await rnInitWhisper({
      filePath: MODEL_FILENAME,
      isBundleAsset: true,
    });
    isModelReady = true;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    console.warn("[speech] Failed to init whisper.rn:", msg);
    throw new Error(`Failed to initialize on-device speech recognition: ${msg}`);
  } finally {
    isInitializing = false;
  }
}

export function isWhisperReady(): boolean {
  return isModelReady && whisperContext !== null;
}

/**
 * Start realtime streaming transcription using whisper.rn's built-in mic.
 * Returns a controller to stop recording and subscribe to partial results.
 * This handles mic recording internally — no expo-av needed.
 */
export async function startRealtimeTranscribe(
  onPartialResult: (text: string) => void,
): Promise<{ stop: () => Promise<string> }> {
  if (!whisperContext) {
    await initWhisper();
    if (!whisperContext) {
      throw new Error("Whisper model not available.");
    }
  }

  let finalText = "";

  const { stop, subscribe } = await whisperContext.transcribeRealtime({
    language: "en",
    realtimeAudioSec: 60,
    realtimeAudioSliceSec: 5,
    realtimeAudioMinSec: 1,
    // Audio session is pre-configured on Tasks mount — don't let whisper.rn touch it
    audioSessionOnStartIos: undefined,
    audioSessionOnStopIos: undefined,
  });

  subscribe((event: any) => {
    if (event.isCapturing) {
      const text = event.data?.result?.trim() ?? "";
      if (text) {
        finalText = text;
        onPartialResult(text);
      }
    }
  });

  return {
    stop: async () => {
      await stop();
      return finalText;
    },
  };
}

async function transcribeWithWhisper(audioUri: string): Promise<string> {
  if (!whisperContext) {
    await initWhisper();
    if (!whisperContext) {
      throw new Error("Whisper model not available. Check your internet connection and try again.");
    }
  }

  const { transcribe: whisperTranscribe } = whisperContext;
  const result = await whisperTranscribe(audioUri, {
    language: "en",
    maxLen: 0,
    translate: false,
  });

  return result?.result?.trim() ?? "";
}

// ── Cloud: OpenAI ────────────────────────────────────────────────────

async function transcribeWithOpenAI(
  audioUri: string,
  apiKey: string
): Promise<string> {
  const formData = new FormData();
  formData.append("file", {
    uri: audioUri,
    type: "audio/m4a",
    name: "audio.m4a",
  } as any);
  formData.append("model", "gpt-4o-mini-transcribe");
  formData.append("language", "en");

  const response = await fetch(
    "https://api.openai.com/v1/audio/transcriptions",
    {
      method: "POST",
      headers: { Authorization: `Bearer ${apiKey}` },
      body: formData,
    }
  );

  if (!response.ok) {
    const err = await response.text().catch(() => "Unknown error");
    throw new Error(`OpenAI STT failed (${response.status}): ${err}`);
  }

  const data = await response.json();
  return data.text?.trim() ?? "";
}

// ── Cloud: Deepgram ──────────────────────────────────────────────────

async function transcribeWithDeepgram(
  audioUri: string,
  apiKey: string
): Promise<string> {
  // Read audio file as blob
  const audioResponse = await fetch(audioUri);
  const audioBlob = await audioResponse.blob();

  const response = await fetch(
    "https://api.deepgram.com/v1/listen?model=nova-2&language=en&smart_format=true",
    {
      method: "POST",
      headers: {
        Authorization: `Token ${apiKey}`,
        "Content-Type": "audio/m4a",
      },
      body: audioBlob,
    }
  );

  if (!response.ok) {
    const err = await response.text().catch(() => "Unknown error");
    throw new Error(`Deepgram STT failed (${response.status}): ${err}`);
  }

  const data = await response.json();
  return (
    data.results?.channels?.[0]?.alternatives?.[0]?.transcript?.trim() ?? ""
  );
}

// ── Cloud: AssemblyAI ────────────────────────────────────────────────

async function transcribeWithAssemblyAI(
  audioUri: string,
  apiKey: string
): Promise<string> {
  // Step 1: Upload audio
  const audioResponse = await fetch(audioUri);
  const audioBlob = await audioResponse.blob();

  const uploadRes = await fetch("https://api.assemblyai.com/v2/upload", {
    method: "POST",
    headers: { Authorization: apiKey },
    body: audioBlob,
  });

  if (!uploadRes.ok) {
    throw new Error(`AssemblyAI upload failed (${uploadRes.status})`);
  }

  const { upload_url } = await uploadRes.json();

  // Step 2: Create transcription
  const transcriptRes = await fetch(
    "https://api.assemblyai.com/v2/transcript",
    {
      method: "POST",
      headers: {
        Authorization: apiKey,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        audio_url: upload_url,
        language_code: "en",
      }),
    }
  );

  if (!transcriptRes.ok) {
    throw new Error(
      `AssemblyAI transcription failed (${transcriptRes.status})`
    );
  }

  const { id } = await transcriptRes.json();

  // Step 3: Poll for result
  const pollUrl = `https://api.assemblyai.com/v2/transcript/${id}`;
  for (let i = 0; i < 60; i++) {
    await new Promise((r) => setTimeout(r, 1000));
    const pollRes = await fetch(pollUrl, {
      headers: { Authorization: apiKey },
    });
    const pollData = await pollRes.json();

    if (pollData.status === "completed") {
      return pollData.text?.trim() ?? "";
    }
    if (pollData.status === "error") {
      throw new Error(
        `AssemblyAI error: ${pollData.error ?? "Unknown error"}`
      );
    }
  }

  throw new Error("AssemblyAI transcription timed out");
}

// ── Public API ───────────────────────────────────────────────────────

/**
 * Transcribe an audio file using the configured provider.
 */
export async function transcribe(
  audioUri: string,
  config: SpeechConfig
): Promise<TranscriptionResult> {
  const start = Date.now();
  let text: string;

  switch (config.provider) {
    case "on-device":
      text = await transcribeWithWhisper(audioUri);
      break;
    case "openai":
      if (!config.apiKey) throw new Error("OpenAI API key required");
      text = await transcribeWithOpenAI(audioUri, config.apiKey);
      break;
    case "deepgram":
      if (!config.apiKey) throw new Error("Deepgram API key required");
      text = await transcribeWithDeepgram(audioUri, config.apiKey);
      break;
    case "assemblyai":
      if (!config.apiKey) throw new Error("AssemblyAI API key required");
      text = await transcribeWithAssemblyAI(audioUri, config.apiKey);
      break;
    default:
      throw new Error(`Unknown speech provider: ${config.provider}`);
  }

  return { text, durationMs: Date.now() - start };
}

// ── Provider metadata ────────────────────────────────────────────────

export interface SpeechProviderInfo {
  id: SpeechProvider;
  name: string;
  description: string;
  requiresKey: boolean;
  keyPlaceholder?: string;
  keyHint?: string;
}

export const SPEECH_PROVIDERS: SpeechProviderInfo[] = [
  {
    id: "on-device",
    name: "On-Device (Free)",
    description: "Runs locally using bundled Whisper. No API key needed.",
    requiresKey: false,
  },
  {
    id: "openai",
    name: "OpenAI",
    description: "GPT-4o Mini Transcribe. Fast, accurate. $0.003/min.",
    requiresKey: true,
    keyPlaceholder: "sk-...",
    keyHint: "Get your key at platform.openai.com/api-keys",
  },
  {
    id: "deepgram",
    name: "Deepgram",
    description: "Nova-2. Real-time capable, top accuracy. $0.0043/min.",
    requiresKey: true,
    keyPlaceholder: "Your Deepgram API key",
    keyHint: "Get your key at console.deepgram.com",
  },
  {
    id: "assemblyai",
    name: "AssemblyAI",
    description: "Universal-2. Cheapest async option. $0.002/min.",
    requiresKey: true,
    keyPlaceholder: "Your AssemblyAI API key",
    keyHint: "Get your key at assemblyai.com/dashboard",
  },
];

export interface TtsProviderInfo {
  id: TtsProvider;
  name: string;
  description: string;
  requiresKey: boolean;
}

export const TTS_PROVIDERS: TtsProviderInfo[] = [
  {
    id: "device",
    name: "Local Device Voice",
    description: "Uses iOS or Android text-to-speech. Free and local.",
    requiresKey: false,
  },
  {
    id: "openai",
    name: "OpenAI Voice",
    description: "Uses OpenAI text-to-speech with your API key.",
    requiresKey: true,
  },
];

function stripSpeechMarkdown(text: string): string {
  return text.replace(/[#*`_~\[\]()>|\\-]/g, "").replace(/\n+/g, ". ").trim();
}

async function speakWithOpenAI(text: string, apiKey: string): Promise<void> {
  const response = await fetch("https://api.openai.com/v1/audio/speech", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${apiKey}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model: "gpt-4o-mini-tts",
      voice: "alloy",
      input: stripSpeechMarkdown(text).slice(0, 4000),
      response_format: "mp3",
    }),
  });
  if (!response.ok) {
    const err = await response.text().catch(() => "Unknown error");
    throw new Error(`OpenAI TTS failed (${response.status}): ${err}`);
  }

  const FileSystem = require("expo-file-system/legacy");
  const { Audio } = require("expo-av");
  const blob = await response.blob();
  const reader = new FileReader();
  const base64 = await new Promise<string>((resolve, reject) => {
    reader.onerror = () => reject(new Error("Failed to read OpenAI speech audio."));
    reader.onloadend = () => {
      const result = String(reader.result || "");
      resolve(result.includes(",") ? result.split(",")[1] : result);
    };
    reader.readAsDataURL(blob);
  });
  const uri = `${FileSystem.cacheDirectory}yaver-openai-tts-${Date.now()}.mp3`;
  await FileSystem.writeAsStringAsync(uri, base64, { encoding: FileSystem.EncodingType.Base64 });
  const { sound } = await Audio.Sound.createAsync({ uri }, { shouldPlay: true });
  sound.setOnPlaybackStatusUpdate((status: any) => {
    if (status?.didJustFinish) {
      sound.unloadAsync().catch(() => {});
      FileSystem.deleteAsync(uri, { idempotent: true }).catch(() => {});
    }
  });
}

export async function speakText(
  text: string,
  config: TextToSpeechConfig = { provider: "device" },
): Promise<void> {
  const plain = stripSpeechMarkdown(text);
  if (!plain) return;
  if (config.provider === "openai") {
    if (!config.apiKey) throw new Error("OpenAI API key required");
    await speakWithOpenAI(plain, config.apiKey);
    return;
  }
  const Speech = require("expo-speech");
  Speech.speak(plain, { language: "en" });
}
