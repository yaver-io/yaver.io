/**
 * carVoiceCoding.ts — Tier 0 "remote coding from the car by voice" loop.
 *
 * See docs/yaver-car-voice-coding.md.
 *
 * This is the driving-safe orchestrator that ties together primitives that
 * ALREADY EXIST in the app:
 *
 *   record (expo-av)  →  STT (speech.ts::transcribe)
 *                     →  DISPATCH to a remote box (quic.ts::sendTask, codeMode)
 *                     →  POLL to terminal (quic.ts::getTask)
 *                     →  SUMMARIZE to ONE sentence (this file)
 *                     →  TTS over car audio (speech.ts::speakText)
 *
 * The one load-bearing rule (docs §4): async voice command in, high-level
 * STATUS read-back out. We NEVER speak diffs, code, or long output while
 * driving — `summarizeForReadback` is a hard one-sentence gate and
 * `isReadCodeRequest` declines "read me the diff"-style asks.
 *
 * Everything here is dependency-injected via `CarVoiceDeps` so it can be
 * unit-tested with `npx tsx` (node:test) without the RN runtime, mirroring
 * discoveryDiagnostics.ts. The default deps wire to the real libs.
 *
 * Tier 0 needs NO car SDK and NO entitlement — audio plays over whatever the
 * active route is (Bluetooth car speakers when paired). Tier 1 (Android Auto
 * MessagingStyle) reuses `dispatchAndSummarize` and delivers the summary as an
 * incoming "message" instead of speaking it directly; see
 * carMessagingNotification.ts.
 */

import type { SpeechProvider, TtsProvider } from "./auth";

// ── Injected surface ─────────────────────────────────────────────────

export interface CarVoiceTaskRef {
  id: string;
  status: string;
  resultText?: string;
  /** Output lines, when resultText is absent. */
  output?: string[];
}

/** The pieces we depend on, injectable for tests. */
export interface CarVoiceDeps {
  /** Transcribe a recorded audio file URI to text (speech.ts::transcribe). */
  transcribe: (audioUri: string) => Promise<string>;
  /** Create a coding task on the chosen remote box; returns its id. */
  dispatch: (title: string, prompt: string) => Promise<string>;
  /** Fetch current state of a task (quic.ts::getTask). */
  getTask: (taskId: string) => Promise<CarVoiceTaskRef>;
  /** Speak a (already-summarized) sentence over the active audio route. */
  speak: (text: string) => Promise<void>;
  /** Sleep ms — injectable so tests don't actually wait. */
  sleep?: (ms: number) => Promise<void>;
  /** Clock — injectable for deterministic timeout tests. */
  now?: () => number;
}

export interface CarVoiceConfig {
  /** STT provider + key, forwarded to the injected transcribe(). */
  stt?: { provider: SpeechProvider; apiKey?: string; model?: string };
  /** TTS provider + key, forwarded to the injected speak(). */
  tts?: { provider: TtsProvider; apiKey?: string; voice?: string };
  /** Poll interval while waiting for the remote task (ms). */
  pollIntervalMs?: number;
  /** Hard ceiling before we stop waiting and tell the driver (ms). */
  maxWaitMs?: number;
  /** Speak an immediate "On it" the moment dispatch succeeds. Default true. */
  speakAcknowledgement?: boolean;
}

export type CarVoiceStage =
  | "listening"
  | "transcribed"
  | "dispatched"
  | "working"
  | "spoken"
  | "declined"
  | "error";

export interface CarVoiceStep {
  stage: CarVoiceStage;
  /** Human/spoken line for this step, when applicable. */
  text?: string;
  taskId?: string;
  status?: string;
}

export interface CarVoiceResult {
  transcript: string;
  taskId?: string;
  status?: string;
  /** The single sentence that was (or would be) spoken. */
  spoken: string;
  declined?: boolean;
  timedOut?: boolean;
  error?: string;
}

const TERMINAL = new Set(["completed", "finished", "failed", "stopped", "review"]);

const DEFAULT_POLL_MS = 4000;
const DEFAULT_MAX_WAIT_MS = 15 * 60 * 1000;
/** Hard cap on any spoken sentence. Driving readback must be glanceable-by-ear. */
export const READBACK_MAX_CHARS = 200;

// ── Driving-mode guard ───────────────────────────────────────────────

/**
 * True when the utterance is asking us to READ code / a diff / file contents
 * aloud — which we refuse while driving (docs §4). Deliberately conservative:
 * it only matches explicit read-the-code asks, not normal coding commands
 * ("add a test", "fix the build").
 */
export function isReadCodeRequest(transcript: string): boolean {
  const t = transcript.toLowerCase();
  const verbs = /\b(read|show|tell me|what'?s in|recite|dictate)\b/;
  const subjects = /\b(the )?(diff|code|file|function|patch|changes|contents?|source|stack ?trace|log|output)\b/;
  return verbs.test(t) && subjects.test(t);
}

// ── Summarization ────────────────────────────────────────────────────

/**
 * Collapse a (possibly long) task result into ONE spoken sentence keyed by
 * status. We NEVER read the body verbatim — at most we surface the task's own
 * first short clause if it already reads like a status line. Deterministic,
 * no extra LLM call.
 */
export function summarizeForReadback(task: CarVoiceTaskRef): string {
  const status = (task.status || "").toLowerCase();
  const body = (task.resultText && task.resultText.trim()) ||
    (task.output && task.output.filter(Boolean).join(" ").trim()) ||
    "";

  let lead: string;
  switch (status) {
    case "completed":
    case "finished":
      lead = "Done.";
      break;
    case "failed":
      lead = "That failed.";
      break;
    case "stopped":
      lead = "I stopped it.";
      break;
    case "review":
      lead = "It needs your review.";
      break;
    default:
      lead = "Finished.";
  }

  const clause = firstStatusClause(body);
  const sentence = clause ? `${lead} ${clause}` : lead;
  return clampSentence(sentence);
}

/**
 * Pull the FIRST short, status-shaped clause out of an agent result, never a
 * code block. Returns "" if the body looks like code / is too long to be a
 * status line — in that case the caller speaks only the status lead.
 */
function firstStatusClause(body: string): string {
  if (!body) return "";
  // Take only the first line; ignore anything fenced/markup-y.
  const firstLine = body.split(/\r?\n/).find((l) => l.trim().length > 0) || "";
  const line = firstLine.trim();
  if (!line) return "";
  // Refuse anything that smells like code or a path dump.
  if (/[{}<>;=]|```|\b(function|const|class|def|import|return)\b|\/\w+\//.test(line)) {
    return "";
  }
  // First sentence only.
  const m = line.match(/^(.{1,160}?[.!?])(\s|$)/);
  const clause = (m ? m[1] : line).trim();
  // Strip markdown emphasis/heading markers.
  return clause.replace(/[#*`_~]/g, "").trim();
}

function clampSentence(s: string): string {
  const t = s.replace(/\s+/g, " ").trim();
  if (t.length <= READBACK_MAX_CHARS) return t;
  return t.slice(0, READBACK_MAX_CHARS - 1).trimEnd() + "…";
}

/** Short Tasks-list title from a transcript (mirrors voice_dispatch.go). */
export function titleFromTranscript(t: string): string {
  const s = t.trim();
  if (s.length <= 60) return s;
  for (let i = 0; i < s.length; i++) {
    const r = s[i];
    if (i > 40 && (r === "." || r === "?" || r === "!" || r === ",")) {
      return s.slice(0, i).trim();
    }
    if (i >= 60) break;
  }
  return s.slice(0, 60).trim() + "…";
}

// ── Core: dispatch a transcript and summarize the result ─────────────

/**
 * Dispatch an already-transcribed command to the remote box and poll to a
 * terminal status, returning the ONE-sentence summary. Shared by the Tier 0
 * spoken loop and the Tier 1 MessagingStyle surface. Does NOT speak — the
 * caller decides how to deliver `spoken`.
 */
export async function dispatchAndSummarize(
  transcript: string,
  deps: CarVoiceDeps,
  config: CarVoiceConfig = {},
  onStep?: (s: CarVoiceStep) => void,
): Promise<CarVoiceResult> {
  const sleep = deps.sleep ?? ((ms: number) => new Promise((r) => setTimeout(r, ms)));
  const now = deps.now ?? (() => Date.now());
  const pollMs = config.pollIntervalMs ?? DEFAULT_POLL_MS;
  const maxWaitMs = config.maxWaitMs ?? DEFAULT_MAX_WAIT_MS;

  const clean = transcript.trim();
  if (!clean) {
    return { transcript: "", spoken: "I didn't catch that.", error: "empty transcript" };
  }

  if (isReadCodeRequest(clean)) {
    const spoken = "I'll have it ready on your phone when you're parked — I won't read code while you drive.";
    onStep?.({ stage: "declined", text: spoken });
    return { transcript: clean, spoken, declined: true };
  }

  let taskId: string;
  try {
    taskId = await deps.dispatch(titleFromTranscript(clean), clean);
  } catch (e) {
    const spoken = "I couldn't reach your box.";
    onStep?.({ stage: "error", text: spoken });
    return { transcript: clean, spoken, error: msgOf(e) };
  }
  onStep?.({ stage: "dispatched", taskId, text: "On it." });

  const deadline = now() + maxWaitMs;
  let last: CarVoiceTaskRef | undefined;
  // First poll immediately, then on the interval.
  for (;;) {
    try {
      last = await deps.getTask(taskId);
    } catch (e) {
      const spoken = "I lost track of that task.";
      onStep?.({ stage: "error", taskId, text: spoken });
      return { transcript: clean, taskId, spoken, error: msgOf(e) };
    }
    const status = (last.status || "").toLowerCase();
    if (TERMINAL.has(status)) {
      const spoken = summarizeForReadback(last);
      onStep?.({ stage: "spoken", taskId, status, text: spoken });
      return { transcript: clean, taskId, status, spoken };
    }
    onStep?.({ stage: "working", taskId, status });
    if (now() >= deadline) {
      const spoken = "Still working — I'll let you know on your phone.";
      onStep?.({ stage: "spoken", taskId, status, text: spoken });
      return { transcript: clean, taskId, status, spoken, timedOut: true };
    }
    await sleep(pollMs);
  }
}

/**
 * Full Tier 0 loop from a recorded utterance: STT → dispatch → poll →
 * summarize → SPEAK. `audioUri` is a recorded clip (expo-av). Speaks an
 * immediate acknowledgement on dispatch (config.speakAcknowledgement), then
 * speaks the final one-sentence summary.
 */
export async function runCarVoiceTurn(
  audioUri: string,
  deps: CarVoiceDeps,
  config: CarVoiceConfig = {},
  onStep?: (s: CarVoiceStep) => void,
): Promise<CarVoiceResult> {
  onStep?.({ stage: "listening" });

  let transcript: string;
  try {
    transcript = (await deps.transcribe(audioUri)).trim();
  } catch (e) {
    const spoken = "I couldn't understand that.";
    onStep?.({ stage: "error", text: spoken });
    await safeSpeak(deps, spoken);
    return { transcript: "", spoken, error: msgOf(e) };
  }
  onStep?.({ stage: "transcribed", text: transcript });

  if (!transcript) {
    const spoken = "I didn't catch that.";
    await safeSpeak(deps, spoken);
    return { transcript: "", spoken };
  }

  const ackEnabled = config.speakAcknowledgement !== false;

  const result = await dispatchAndSummarize(transcript, deps, config, (s) => {
    onStep?.(s);
    if (s.stage === "dispatched" && ackEnabled && s.text) {
      // Fire-and-forget acknowledgement so the driver hears "On it" early.
      void safeSpeak(deps, s.text);
    }
  });

  await safeSpeak(deps, result.spoken);
  return result;
}

// ── helpers ──────────────────────────────────────────────────────────

async function safeSpeak(deps: CarVoiceDeps, text: string): Promise<void> {
  try {
    await deps.speak(text);
  } catch {
    // Never let a TTS failure crash the driving loop.
  }
}

function msgOf(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

// ── Default deps (real RN libs) ──────────────────────────────────────
// Lazily required so the module is importable in a plain node/tsx test
// context (where react-native / expo-* don't resolve). Tests inject their
// own deps and never touch these.

/**
 * Build the production deps wired to the real libs. `dispatchTask` lets the
 * caller pick the box (e.g. connectionManager.clientFor(id).sendTask(...,
 * codeMode=true)) and `getTask` the matching read. Kept as a factory so the
 * lib has no hard import of the RN runtime at module scope.
 */
export function makeRealCarVoiceDeps(opts: {
  config: CarVoiceConfig;
  dispatchTask: (title: string, prompt: string) => Promise<{ id: string }>;
  getTask: (taskId: string) => Promise<CarVoiceTaskRef>;
}): CarVoiceDeps {
  return {
    transcribe: async (audioUri: string) => {
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      const { transcribe } = require("./speech");
      const stt = opts.config.stt ?? { provider: "on-device" as SpeechProvider };
      const r = await transcribe(audioUri, { provider: stt.provider, apiKey: stt.apiKey, model: stt.model });
      return r?.text ?? "";
    },
    dispatch: async (title: string, prompt: string) => {
      const t = await opts.dispatchTask(title, prompt);
      return t.id;
    },
    getTask: opts.getTask,
    speak: async (text: string) => {
      // eslint-disable-next-line @typescript-eslint/no-require-imports
      const { speakText } = require("./speech");
      const tts = opts.config.tts ?? { provider: "device" as TtsProvider };
      await speakText(text, { provider: tts.provider, apiKey: tts.apiKey, voice: tts.voice });
    },
  };
}
