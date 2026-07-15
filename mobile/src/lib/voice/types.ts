/**
 * voice/types.ts — the seams of the shared, surface-agnostic voice conversation
 * engine.
 *
 * ONE core (conversationCore.ts) runs the business logic — semantic endpointing,
 * the accumulate/dispatch decision, the confirm handshake, barge-in, spoken
 * error recovery — for EVERY surface (car, phone, watch, TV, web, glass, VR) on
 * BOTH platforms (iOS + Android). Surfaces differ only in the four thin adapters
 * below. The core never imports React, expo, whisper, the network, or a clock —
 * everything it touches is one of these interfaces, so it unit-tests under
 * `npx tsx` exactly like the existing car-surface libs.
 *
 * Why interfaces and not concrete calls: the cross-surface audit found the same
 * STT→dispatch→TTS loop re-implemented for car, watch, TV (twice, once in
 * Swift), the local helper, and web spatial — plus the recording setup copied
 * five times and the one-sentence summarizer written three times in two
 * languages. This module is where that stops being copied.
 */

/** Injected time source so the core is deterministic under test. */
export interface Clock {
  now(): number;
}

/** Injected timer source (see scheduler.ts) so timeouts are virtual in tests. */
export interface Scheduler {
  setInterval(fn: () => void, ms: number): () => void;
  setTimeout(fn: () => void, ms: number): () => void;
}

export type VoiceSurface =
  | "car"
  | "phone"
  | "watch"
  | "tv"
  | "web"
  | "glass"
  | "vr";

// ── Audio capture (mic → streaming partial transcripts) ──────────────────

export interface CaptureOptions {
  /** BCP-47 locale, e.g. "en-US" / "tr-TR". Adapters default sensibly. */
  locale?: string;
  /** Surface hint so an adapter can pick an audio-session route (car BT-HFP,
   *  phone speaker, …). The core stays surface-agnostic; the adapter doesn't. */
  surface?: VoiceSurface;
}

/**
 * A live capture. `onPartial` fires as the STT engine revises its best guess;
 * `atMs` is capture-relative (0 at start). `stop()` returns the authoritative
 * final transcript. The adapter owns the mic and the audio session lifecycle.
 */
export interface AudioCaptureAdapter {
  start(
    onPartial: (text: string, atMs: number) => void,
    opts?: CaptureOptions,
  ): Promise<CaptureSession>;
}

export interface CaptureSession {
  stop(): Promise<string>;
  active(): boolean;
}

// ── Spoken output (with prompt interrupt for barge-in) ───────────────────

export interface TtsOptions {
  locale?: string;
}

/**
 * Spoken output. `stop()` MUST interrupt promptly (tens of ms) — it is what
 * makes barge-in feel instant. `speak()` resolves when the utterance finishes
 * OR when `stop()` cuts it off, whichever comes first (never rejects on a
 * normal interrupt).
 */
export interface TtsAdapter {
  speak(text: string, opts?: TtsOptions): Promise<void>;
  stop(): Promise<void>;
}

// ── Agent channel (commit ONE complete instruction, get a spoken reply) ──

export interface TurnContext {
  /** True when a prior agent turn left a menu open — this utterance is the
   *  answer to it (a digit / yes-no), not a fresh instruction. */
  pendingChoice: boolean;
  surface: VoiceSurface;
}

export interface AgentReply {
  /** The one short sentence to speak over audio. Never code/diffs/logs. */
  spoken: string;
  /** The agent left a menu open — the next utterance answers it. */
  awaitingChoice: boolean;
  options: string[];
  error?: string;
}

/**
 * Commits a COMPLETE instruction to the remote runner (claude/codex) and
 * returns a spoken reply. Per the runner audit, tmux send-keys submits with an
 * unconditional Enter — so by the time the core calls this, it must already be
 * sure the instruction is complete. Never throws (errors become a spoken line).
 */
export interface AgentChannelAdapter {
  send(instruction: string, ctx: TurnContext): Promise<AgentReply>;
}

// ── Semantic completeness judge (the "when to submit" decision) ──────────

export interface JudgeInput {
  /** The accumulated utterance so far. */
  transcript: string;
  /** Trailing silence (ms) observed when the timing trigger proposed a submit. */
  trailingSilenceMs: number;
  surface: VoiceSurface;
  /** True when answering a menu — always treat as complete. */
  pendingChoice: boolean;
}

export interface JudgeVerdict {
  /** The spoken thought is a complete instruction ready to act on. */
  complete: boolean;
  /** The user is addressing the agent and expects a response now (vs. an aside
   *  / thinking-aloud). Surfaces may use this to decide whether to reply. */
  wantsAnswer: boolean;
  /** Where the verdict came from — for logging/telemetry, not control flow. */
  source: "heuristic" | "model" | "fallback";
}

/**
 * Decides — semantically, not by a hard-coded timer — whether a spoken thought
 * is finished. The timing endpointer only decides WHEN to ask this; THIS
 * decides the answer. Runs on-device (free) per the on-device-judge audit.
 */
export interface CompletenessJudge {
  judge(input: JudgeInput): Promise<JudgeVerdict>;
}

// ── Pre-dispatch interceptors (machine-switch, surface intents, …) ───────

export interface InterceptResult {
  /** Handled locally — speak this and resume listening; do NOT hit the agent. */
  spoken: string;
  /** Optional surface side-effect the core should run before speaking
   *  (e.g. retarget the active machine). Kept opaque to the core. */
  effect?: () => void | Promise<void>;
}

/**
 * A chance to handle a complete instruction WITHOUT dispatching it to the
 * runner — "switch to pokayoke" (retarget), "any unread mail?" (surface op).
 * Return null to pass. Interceptors run in order; first non-null wins. This is
 * how car/watch inject their existing carMachineSwitch / carSurfaceIntent logic
 * without the core knowing anything surface-specific.
 */
export interface InstructionInterceptor {
  intercept(
    instruction: string,
    ctx: TurnContext,
  ): Promise<InterceptResult | null>;
}

// ── Risk gate (hands-free confirm handshake for dangerous verbs) ─────────

export interface RiskAssessment {
  risky: boolean;
  /** Spoken prompt asking for confirmation (e.g. "Deploy to prod? Say yes …"). */
  prompt: string;
}

/**
 * The hard gate CLAUDE.md requires: deploy / push / delete / force never run
 * without explicit confirmation. `interpretReply` maps a spoken yes/no to a
 * verdict. The core owns the confirm state machine; this just classifies.
 */
export interface RiskPolicy {
  assess(text: string): RiskAssessment;
  interpretReply(text: string): "confirm" | "cancel" | "unclear";
}

// ── Core lifecycle (observable state for the surface UI + native mirrors) ─

export type VoiceState =
  | "idle"
  | "listening"
  | "judging"
  | "confirming"
  | "dispatching"
  | "speaking";

export interface VoiceCoreEvent {
  state: VoiceState;
  /** Best transcript so far while listening; the spoken line while speaking. */
  text?: string;
  /** True on the transition into a terminal spoken line for a turn. */
  turnComplete?: boolean;
  error?: string;
}

export type VoiceCoreListener = (ev: VoiceCoreEvent) => void;
