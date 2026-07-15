/**
 * voice/completenessJudge.ts — the SEMANTIC "is the user done?" decision.
 *
 * The product requirement: do NOT decide end-of-utterance with a hard-coded
 * timer. A driver pauses to THINK mid-instruction; a silence timer would cut
 * them off. So the real decision is semantic — read the words/verbs and judge
 * "is this a finished, actionable thought, and does the user expect an answer
 * now?" The timing endpointer (endpointer.ts) only decides WHEN to ask this.
 *
 * Placement (from the on-device-judge audit): this runs FREE on-device on the
 * existing llama.rn engine, constrained by a GBNF grammar so a 1B/1.5B model
 * can only emit the target JSON. To keep it cheap we do the house two-stage
 * pattern (mirrors interpreter.ts): a deterministic heuristic answers the
 * obvious cases; the model is consulted only when the heuristic is unsure.
 *
 * Pure + dependency-injected: the model call is an injected `complete` fn, so
 * this unit-tests under `npx tsx` with no RN and no model loaded.
 */
import type {
  CompletenessJudge,
  JudgeInput,
  JudgeVerdict,
} from "./types";

/** The on-device engine's completion surface (see localAgent/engine.ts). */
export type ModelComplete = (opts: {
  prompt: string;
  grammar?: string;
  maxTokens?: number;
  temperature?: number;
}) => Promise<{ text: string }>;

export interface CompletenessJudgeDeps {
  /** On-device model completion. null/undefined → heuristic + silence fallback
   *  only (still fully functional, just less nuanced). */
  complete?: ModelComplete | null;
  /** Trailing-silence (ms) at/above which an unresolved case is treated as
   *  complete when NO model is available. The endpointer's hard cap is the
   *  ultimate backstop, so this only shapes the no-model UX. */
  fallbackCompleteSilenceMs?: number;
}

const DEFAULT_FALLBACK_COMPLETE_SILENCE_MS = 1800;

// ── Grammar: force `{"complete": bool, "wantsAnswer": bool}` ──────────────

/**
 * GBNF that constrains decoding to exactly our two-boolean object — the tiny
 * model literally cannot emit anything else (same technique as
 * localAgent/grammar.ts::buildToolCallGrammar).
 */
export function buildCompletenessGrammar(): string {
  return [
    'root ::= "{" ws "\\"complete\\"" ws ":" ws bool ws "," ws "\\"wantsAnswer\\"" ws ":" ws bool ws "}"',
    'bool ::= "true" | "false"',
    'ws ::= [ \\t\\n]*',
  ].join("\n");
}

export function buildCompletenessPrompt(input: JudgeInput): string {
  const surface = input.surface;
  return [
    "You decide when a hands-free voice instruction to a coding agent is finished.",
    `The user is on a ${surface} surface, speaking in fragments and pausing to think.`,
    "Judge the transcript so far:",
    "- complete: TRUE only if it is a finished, actionable instruction or question.",
    "  FALSE if the user is mid-thought — trailed off, ended on a conjunction",
    '  ("and", "so", "because", "then"), or is clearly still listing/dictating.',
    "- wantsAnswer: TRUE if the user is addressing the agent and expects a reply",
    "  now (a command or a question), FALSE for thinking-aloud or an aside.",
    `Trailing pause so far: ${Math.round(input.trailingSilenceMs)} ms.`,
    `Transcript: "${input.transcript.replace(/"/g, "'")}"`,
    "Answer as JSON only.",
  ].join("\n");
}

// ── Heuristic fast-path ──────────────────────────────────────────────────

/** Words that, when the utterance ENDS on them, strongly imply "more coming". */
const TRAILING_CONTINUATION = new Set([
  "and", "or", "but", "so", "because", "cause", "then", "also", "plus",
  "to", "the", "a", "an", "of", "for", "with", "in", "on", "at", "by",
  "um", "uh", "er", "like", "well", "let", "lets", "let's", "maybe",
  "i", "we", "it", "that", "this", "my", "your", "if", "when", "while",
]);

/** Leading words that mark an imperative/interrogative — a real instruction. */
const IMPERATIVE_OR_QUESTION_START = new Set([
  // imperative verbs
  "add", "fix", "run", "show", "open", "close", "switch", "create", "make",
  "build", "deploy", "push", "pull", "commit", "delete", "remove", "update",
  "change", "write", "read", "list", "check", "start", "stop", "restart",
  "install", "test", "rename", "move", "copy", "set", "get", "find", "search",
  "tell", "give", "send", "call", "go", "undo", "redo", "revert", "merge",
  // question words
  "what", "what's", "whats", "how", "why", "when", "where", "who", "which",
  "is", "are", "can", "could", "should", "would", "do", "does", "did", "will",
]);

function words(text: string): string[] {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}'\s]/gu, " ")
    .split(/\s+/)
    .filter(Boolean);
}

/**
 * Cheap deterministic verdict, or null when genuinely ambiguous (→ ask the
 * model). Deliberately conservative: it only commits when a signal is strong.
 */
export function heuristicVerdict(input: JudgeInput): JudgeVerdict | null {
  // A menu answer is always a complete reply.
  if (input.pendingChoice) {
    return { complete: true, wantsAnswer: true, source: "heuristic" };
  }

  const raw = input.transcript.trim();
  const w = words(raw);
  if (w.length === 0) {
    return { complete: false, wantsAnswer: false, source: "heuristic" };
  }

  const last = w[w.length - 1];
  const endsOnPunctuation = /[.!?]$/.test(raw);

  // Strong "more coming": ended on a conjunction/preposition/filler and the
  // user did NOT put terminal punctuation. Keep listening — this is the
  // thinking-pause case the whole design exists for.
  if (!endsOnPunctuation && TRAILING_CONTINUATION.has(last)) {
    return { complete: false, wantsAnswer: false, source: "heuristic" };
  }

  // Strong "done": explicit terminal punctuation.
  if (endsOnPunctuation) {
    return { complete: true, wantsAnswer: true, source: "heuristic" };
  }

  // A clear imperative/question of reasonable length that did NOT trail off.
  if (IMPERATIVE_OR_QUESTION_START.has(w[0]) && w.length >= 3) {
    return { complete: true, wantsAnswer: true, source: "heuristic" };
  }

  // Otherwise: ambiguous → defer to the model (or the silence fallback).
  return null;
}

// ── Parse the model's constrained JSON ───────────────────────────────────

export function parseVerdict(text: string): JudgeVerdict | null {
  const m = text.match(/\{[\s\S]*\}/);
  if (!m) return null;
  try {
    const obj = JSON.parse(m[0]);
    if (typeof obj.complete !== "boolean") return null;
    return {
      complete: obj.complete,
      wantsAnswer:
        typeof obj.wantsAnswer === "boolean" ? obj.wantsAnswer : obj.complete,
      source: "model",
    };
  } catch {
    return null;
  }
}

// ── The judge ────────────────────────────────────────────────────────────

export function createCompletenessJudge(
  deps: CompletenessJudgeDeps = {},
): CompletenessJudge {
  const fallbackSilence =
    deps.fallbackCompleteSilenceMs ?? DEFAULT_FALLBACK_COMPLETE_SILENCE_MS;

  return {
    async judge(input: JudgeInput): Promise<JudgeVerdict> {
      // 1) Cheap deterministic pass.
      const h = heuristicVerdict(input);
      if (h) return h;

      // 2) On-device model, if loaded.
      if (deps.complete) {
        try {
          const r = await deps.complete({
            prompt: buildCompletenessPrompt(input),
            grammar: buildCompletenessGrammar(),
            maxTokens: 24,
            temperature: 0,
          });
          const v = parseVerdict(r.text);
          if (v) return v;
        } catch {
          // fall through to the silence fallback
        }
      }

      // 3) No model / model failed: lean on how long they've paused. A long
      //    pause on an ambiguous fragment reads as "done"; a short one as
      //    "still thinking". The endpointer's hard cap guarantees progress.
      return {
        complete: input.trailingSilenceMs >= fallbackSilence,
        wantsAnswer: input.trailingSilenceMs >= fallbackSilence,
        source: "fallback",
      };
    },
  };
}
