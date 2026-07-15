/**
 * carVoiceEndpoint.ts — the "when to submit" business logic for hands-free
 * voice in the car.
 *
 * The problem (per the product owner): on the phone, STT text extraction is
 * good, but the user taps a SUBMIT button to end an utterance. In CarPlay the
 * driver can't tap. So something has to decide, from the transcript stream
 * alone, when a spoken thought is COMPLETE and should be sent to the agent.
 *
 * That decision is this module. It is deliberately transport-agnostic and
 * clock-injected: you feed it the partial transcripts coming off the STT
 * engine (whisper.rn realtime emits these ~1×/slice) plus the current time,
 * and it tells you `wait` / `submit` / `timeout`. It never touches audio, the
 * network, or React — so it unit-tests under `npx tsx` with no RN, exactly
 * like carSessionTurn.ts.
 *
 * Endpointing strategy — transcript stability:
 *   whisper realtime keeps re-emitting its best transcript for the rolling
 *   window. While the driver is talking, the text keeps CHANGING (new/rewritten
 *   words). When they stop, the text goes STABLE — the same string comes back
 *   slice after slice. So "the transcript hasn't changed for `silenceMs`" is a
 *   reliable end-of-utterance signal that needs no separate VAD/metering (which
 *   we can't cheaply run anyway while whisper.rn owns the mic).
 *
 * We also guard the two failure modes a button never had to:
 *   - the driver says nothing at all → `timeout` after `noSpeechTimeoutMs`, so
 *     the loop can re-prompt or go quiet instead of listening forever;
 *   - the driver monologues without a clean pause → a hard `maxUtteranceMs`
 *     cap force-submits what we have, so the turn always completes.
 */

export interface EndpointConfig {
  /** Trailing transcript-stability window that counts as "done speaking". */
  silenceMs: number;
  /** Don't submit a stray blip (a single misheard token). */
  minCharsToSubmit: number;
  /** No speech heard at all within this window → `timeout`. */
  noSpeechTimeoutMs: number;
  /** Hard ceiling: force-submit even mid-sentence so a turn can't hang. */
  maxUtteranceMs: number;
}

/**
 * Tuned for whisper.rn realtime with ~1s slices over a car Bluetooth mic.
 * 1.2s of stable transcript reads as a natural end-of-thought pause without
 * clipping the driver mid-sentence; 8s of pure silence ends a dead turn.
 */
export const DEFAULT_ENDPOINT_CONFIG: EndpointConfig = {
  silenceMs: 1200,
  minCharsToSubmit: 2,
  noSpeechTimeoutMs: 8000,
  maxUtteranceMs: 30000,
};

export type EndpointDecision =
  | { action: "wait" }
  | { action: "submit"; text: string; reason: "silence" | "max-length" }
  | { action: "timeout" };

/** Collapse whitespace + lowercase so cosmetic re-emits don't read as change. */
function normalize(text: string): string {
  return text.trim().replace(/\s+/g, " ").toLowerCase();
}

/**
 * Per-utterance endpointer. Lifecycle:
 *   const ep = new UtteranceEndpointer(cfg, startMs);
 *   // on each STT partial:   ep.onPartial(text, nowMs);
 *   // on a ~200ms timer:     const d = ep.tick(nowMs); act on d.action
 *   // then reset() for the next utterance.
 *
 * onPartial and tick are both idempotent w.r.t. time — safe to call in any
 * order. `tick` is what emits a terminal decision; `onPartial` only records.
 */
export class UtteranceEndpointer {
  private readonly cfg: EndpointConfig;
  private startMs: number;
  /** Best transcript seen so far (latest non-empty). */
  private bestText = "";
  private bestNorm = "";
  /** When the (normalized) transcript last CHANGED. */
  private lastChangeMs = 0;
  /** Whether any speech has been heard yet (a flag, not a sentinel — the
   *  clock can legitimately read 0 at the first partial). */
  private heard = false;
  /** When we first heard speech (valid only once `heard`). */
  private firstSpeechMs = 0;
  private decided = false;

  constructor(cfg: EndpointConfig = DEFAULT_ENDPOINT_CONFIG, startMs = 0) {
    this.cfg = cfg;
    this.startMs = startMs;
  }

  /** Feed a partial transcript from the STT engine. */
  onPartial(text: string, nowMs: number): void {
    if (this.decided) return;
    const norm = normalize(text);
    if (!norm) return; // whisper emits "" for silent slices — not speech
    if (!this.heard) {
      this.heard = true;
      this.firstSpeechMs = nowMs;
    }
    if (norm !== this.bestNorm) {
      this.bestNorm = norm;
      this.bestText = text.trim();
      this.lastChangeMs = nowMs;
    }
  }

  /**
   * Decide what to do at `nowMs`. Returns a terminal decision at most once;
   * subsequent calls after a terminal decision return `{action:"wait"}` until
   * reset() (so a caller that double-ticks can't double-submit).
   */
  tick(nowMs: number): EndpointDecision {
    if (this.decided) return { action: "wait" };

    // Nothing heard yet → only a timeout can fire.
    if (!this.heard) {
      if (nowMs - this.startMs >= this.cfg.noSpeechTimeoutMs) {
        this.decided = true;
        return { action: "timeout" };
      }
      return { action: "wait" };
    }

    // Heard speech. Hard cap wins even if still changing.
    if (nowMs - this.firstSpeechMs >= this.cfg.maxUtteranceMs) {
      if (this.bestText.length >= this.cfg.minCharsToSubmit) {
        this.decided = true;
        return { action: "submit", text: this.bestText, reason: "max-length" };
      }
      // Too little content even at the cap — treat as a dead turn.
      this.decided = true;
      return { action: "timeout" };
    }

    // Transcript has gone stable for the silence window → end of thought.
    if (
      nowMs - this.lastChangeMs >= this.cfg.silenceMs &&
      this.bestText.length >= this.cfg.minCharsToSubmit
    ) {
      this.decided = true;
      return { action: "submit", text: this.bestText, reason: "silence" };
    }

    return { action: "wait" };
  }

  /** The best transcript so far (for UI display before a decision). */
  currentText(): string {
    return this.bestText;
  }

  hasSpeech(): boolean {
    return this.heard;
  }

  /** Re-arm for the next utterance in the loop. */
  reset(nowMs = 0): void {
    this.startMs = nowMs;
    this.bestText = "";
    this.bestNorm = "";
    this.lastChangeMs = 0;
    this.heard = false;
    this.firstSpeechMs = 0;
    this.decided = false;
  }
}
