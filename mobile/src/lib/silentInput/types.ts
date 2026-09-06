export type VSRBackend = "mobile" | "user-machine" | "cloud";

export type SilentInputConfig = {
  enabled: boolean;
  backend: VSRBackend;
  language: "en";
  autoSend: boolean;
  sendFullVideo: false;
  mouthCropOnly: true;
  confidenceThreshold: number;
  commandBiasEnabled: boolean;
};

export const DEFAULT_SILENT_INPUT_CONFIG: SilentInputConfig = {
  enabled: true,
  backend: "user-machine",
  language: "en",
  autoSend: false,
  sendFullVideo: false,
  mouthCropOnly: true,
  confidenceThreshold: 0.55,
  commandBiasEnabled: true,
};

export type MouthFrame = {
  timestamp: number;
  width: number;
  height: number;
  format: "gray8";
  data: string;
};

export type MouthFrameBatch = {
  sessionId: string;
  sequence: number;
  frames: MouthFrame[];
};

export type VSRResult = {
  text: string;
  correctedFrom?: string;
  confidence?: number;
  alternatives?: Array<{ text: string; confidence?: number }>;
  durationMs: number;
  metrics?: { captureMs?: number; transferMs?: number; inferenceMs?: number; totalMs?: number };
};

export interface VisualSpeechSource {
  start(): Promise<void>;
  frames(): AsyncIterable<MouthFrame>;
  stop(): Promise<void>;
  dispose(): Promise<void>;
}

export interface VisualSpeechRecognizer {
  initialize(): Promise<void>;
  recognize(frames: MouthFrame[], options?: VSRRecognitionOptions): Promise<VSRResult>;
  dispose(): Promise<void>;
}

export type VSRRecognitionOptions = {
  language?: "en";
  contextualTerms?: string[];
  maxAlternatives?: number;
};
