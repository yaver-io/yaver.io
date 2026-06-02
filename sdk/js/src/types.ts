export interface Task {
  id: string;
  title: string;
  description?: string;
  status: 'queued' | 'running' | 'completed' | 'failed' | 'stopped';
  runnerId?: string;
  sessionId?: string;
  output?: string;
  resultText?: string;
  costUsd?: number;
  turns?: Turn[];
  source?: string;
  tmuxSession?: string;
  isAdopted?: boolean;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
}

export interface Turn {
  role: 'user' | 'assistant';
  content: string;
  timestamp?: string;
}

export interface ImageAttachment {
  base64: string;
  mimeType: string;
  filename: string;
}

export interface CreateTaskOptions {
  model?: string;
  runner?: string;
  customCommand?: string;
  speechContext?: SpeechContext;
  images?: ImageAttachment[];
}

export interface SpeechContext {
  inputFromSpeech?: boolean;
  sttProvider?: string;
  ttsEnabled?: boolean;
  ttsProvider?: string;
  verbosity?: number;
}

export interface AgentInfo {
  hostname: string;
  platform: string;
  agentVersion: string;
  runningTasks: number;
  totalTasks: number;
}

export interface User {
  id: string;
  email: string;
  fullName: string;
  provider: string;
  surveyCompleted?: boolean;
}

export interface Device {
  deviceId: string;
  name: string;
  platform: string;
  quicHost: string;
  quicPort: number;
  isOnline: boolean;
  lastHeartbeat: string;
}

export interface UserSettings {
  forceRelay?: boolean;
  runnerId?: string;
  customRunnerCommand?: string;
  speechProvider?: SpeechProvider;
  speechApiKey?: string;
  ttsEnabled?: boolean;
  verbosity?: number;
}

export type SpeechProvider = 'on-device' | 'openai' | 'deepgram' | 'assemblyai';

export interface SpeechProviderInfo {
  id: SpeechProvider;
  name: string;
  description: string;
  requiresKey: boolean;
  keyPlaceholder?: string;
  keyHint?: string;
  pricePerMin?: string;
}

export interface TranscriptionResult {
  text: string;
  durationMs: number;
  provider: string;
}

export interface ExecSession {
  id: string;
  command: string;
  status: 'running' | 'completed' | 'failed' | 'killed';
  exitCode?: number;
  stdout: string;
  stderr: string;
  pid?: number;
  startedAt: string;
  finishedAt?: string;
}

export interface ExecOptions {
  workDir?: string;
  timeout?: number;
  env?: Record<string, string>;
}

/** A coding runner installed on the agent (claude-code / codex / opencode …). */
export interface RunnerInfo {
  id: string;
  name?: string;
  command?: string;
  installed?: boolean;
  ready?: boolean;
  authConfigured?: boolean;
  authSource?: string;
  supportsBrowserAuth?: boolean;
  supportsModelSelection?: boolean;
  isDefault?: boolean;
  warning?: string;
  error?: string;
  version?: string;
  models?: Array<{ id: string; name?: string; provider?: string; isDefault?: boolean }>;
}

/** In-flight runner OAuth (browser/device-code) session. */
export interface RunnerAuthSession {
  id: string;
  runner: string;
  method?: 'oauth' | 'device-auth' | string;
  status?: string;
  openUrl?: string;
  code?: string;
  detail?: string;
  authConfigured?: boolean;
  authSource?: string;
  error?: string;
}

/** Headless / API-key runner setup options. setupMCP wires in MCP servers. */
export interface RunnerSetupOptions {
  installIfMissing?: boolean;
  setupMCP?: boolean;
  notes?: string;
  anthropicApiKey?: string;
  anthropicAuthToken?: string;
  openaiApiKey?: string;
  glmApiKey?: string;
  zaiApiKey?: string;
}

/** Aggregate readiness snapshot — both auth levels + runtime, for gating UIs. */
export interface YaverCapability {
  agentReachable: boolean;
  account: { authed: boolean; raw: Record<string, unknown> | null };
  runners: RunnerInfo[];
  defaultRunner: string | null;
  /** agent reachable AND at least one runner authed. */
  ready: boolean;
  needs: { yaverAccountAuth: boolean; runnerAuth: string[] };
}
