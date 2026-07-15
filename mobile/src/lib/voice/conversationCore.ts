/**
 * voice/conversationCore.ts — the ONE hands-free voice loop, shared by every
 * surface (car, phone, watch, TV, web, glass, VR) on both platforms.
 *
 * The whole "Claude-app voice mode" experience lives here, and ONLY here:
 *
 *   listen ──▶ (timing endpointer proposes an end-of-utterance)
 *          ──▶ JUDGE: semantically complete? ── no ─▶ accumulate, keep listening
 *                       │ yes
 *          ──▶ interceptors (machine-switch / surface intents) ── handled ─▶ speak ─▶ listen
 *                       │ pass
 *          ──▶ risk gate ── risky ─▶ spoken confirm handshake ─▶ (yes) dispatch / (no) cancel
 *                       │ safe
 *          ──▶ DISPATCH one complete instruction to the runner (claude/codex)
 *          ──▶ SPEAK the one-sentence reply ──▶ auto-resume listening (hands-free)
 *
 * Barge-in: call `interrupt()` (from a native voice-activity trigger or a PTT
 * press) while speaking — TTS stops instantly and we drop back to listening.
 * True open-mic barge-in needs a native echo-cancelling capture adapter; the
 * core is already wired for it (interrupt()), the adapter just isn't built yet.
 *
 * No React, no expo, no whisper, no network, no `Date.now()` — every dependency
 * is an injected adapter/clock/scheduler, so the entire loop unit-tests under
 * `npx tsx` with fakes (see conversationCore.test.ts).
 */
import {
  UtteranceEndpointer,
  DEFAULT_ENDPOINT_CONFIG,
  type EndpointConfig,
} from "./endpointer";
import type {
  AgentChannelAdapter,
  AudioCaptureAdapter,
  CaptureSession,
  Clock,
  CompletenessJudge,
  InstructionInterceptor,
  RiskPolicy,
  Scheduler,
  TtsAdapter,
  TurnContext,
  VoiceCoreEvent,
  VoiceCoreListener,
  VoiceState,
  VoiceSurface,
} from "./types";

export interface VoiceCoreDeps {
  capture: AudioCaptureAdapter;
  tts: TtsAdapter;
  agent: AgentChannelAdapter;
  judge: CompletenessJudge;
  surface: VoiceSurface;
  clock: Clock;
  scheduler: Scheduler;
  /** Endpointer timing (trigger for WHEN to consult the judge). */
  endpoint?: Partial<EndpointConfig>;
  /** Local handlers that answer WITHOUT the runner (retarget machine, read
   *  mail, …). Run in order; first non-null wins. */
  interceptors?: InstructionInterceptor[];
  /** Hard gate for deploy/push/delete/force (CLAUDE.md). Omit to disable. */
  risk?: RiskPolicy;
  locale?: string;
  listener?: VoiceCoreListener;
  /** Consecutive empty listens (heard nothing) before parking to idle so we
   *  don't hold the mic/battery forever. Default 3. */
  maxIdleTimeouts?: number;
  /** How often the endpointer is polled while listening. Default 150ms. */
  tickMs?: number;
}

export class VoiceConversationCore {
  private readonly d: VoiceCoreDeps;
  private readonly endpointCfg: EndpointConfig;
  private readonly endpointer: UtteranceEndpointer;
  private readonly tickMs: number;
  private readonly maxIdleTimeouts: number;

  private running = false;
  /** Bumped on every stop()/interrupt() so stale async completions no-op. */
  private gen = 0;
  private state: VoiceState = "idle";

  private session: CaptureSession | null = null;
  private cancelTicker: (() => void) | null = null;

  /** Fragments accumulated across thinking-pauses within one instruction. */
  private accumulator = "";
  /** A prior agent turn left a menu open — the next utterance answers it. */
  private pendingChoice = false;
  /** A risky instruction is staged, awaiting a spoken yes/no. */
  private confirming: { instruction: string } | null = null;
  /** Consecutive empty listens. */
  private emptyTimeouts = 0;

  constructor(deps: VoiceCoreDeps) {
    this.d = deps;
    this.endpointCfg = { ...DEFAULT_ENDPOINT_CONFIG, ...(deps.endpoint ?? {}) };
    this.endpointer = new UtteranceEndpointer(this.endpointCfg, deps.clock.now());
    this.tickMs = deps.tickMs ?? 150;
    this.maxIdleTimeouts = deps.maxIdleTimeouts ?? 3;
  }

  // ── public API ─────────────────────────────────────────────────────────

  get currentState(): VoiceState {
    return this.state;
  }

  isRunning(): boolean {
    return this.running;
  }

  /** Enter the hands-free loop (idempotent). */
  start(): void {
    if (this.running) return;
    this.running = true;
    this.accumulator = "";
    this.pendingChoice = false;
    this.confirming = null;
    this.emptyTimeouts = 0;
    void this.beginListen();
  }

  /** Leave the loop entirely and release everything. */
  stop(): void {
    if (!this.running && this.state === "idle") return;
    this.running = false;
    this.gen++;
    this.teardownTicker();
    void this.session?.stop().catch(() => {});
    this.session = null;
    void this.d.tts.stop().catch(() => {});
    this.setState("idle");
  }

  /**
   * Barge-in: cut off the current spoken reply (or any active turn) and go
   * straight back to listening. Called by a native voice-activity trigger or a
   * PTT press. No-op if not running.
   */
  interrupt(): void {
    if (!this.running) return;
    this.gen++; // invalidate any in-flight speak/dispatch resume
    this.teardownTicker();
    void this.d.tts.stop().catch(() => {});
    void this.session?.stop().catch(() => {});
    this.session = null;
    void this.beginListen();
  }

  // ── internals ────────────────────────────────────────────────────────────

  private setState(s: VoiceState, extra?: Partial<VoiceCoreEvent>): void {
    this.state = s;
    this.d.listener?.({ state: s, ...extra });
  }

  private alive(myGen: number): boolean {
    return this.running && this.gen === myGen;
  }

  private teardownTicker(): void {
    if (this.cancelTicker) {
      this.cancelTicker();
      this.cancelTicker = null;
    }
  }

  private ctx(pendingChoice: boolean): TurnContext {
    return { pendingChoice, surface: this.d.surface };
  }

  private async beginListen(): Promise<void> {
    const myGen = this.gen;
    this.setState("listening", { text: this.accumulator || undefined });
    this.endpointer.reset(this.d.clock.now());

    let session: CaptureSession;
    try {
      session = await this.d.capture.start(
        (text) => {
          if (!this.alive(myGen)) return;
          this.endpointer.onPartial(text, this.d.clock.now());
          this.setState("listening", { text: this.endpointer.currentText() });
        },
        { locale: this.d.locale, surface: this.d.surface },
      );
    } catch (e) {
      if (!this.alive(myGen)) return;
      // Capture failed (mic/permission/route). Speak it and resume — never a
      // dead-end while driving.
      await this.speakThenResume(myGen, "I couldn't open the microphone.");
      return;
    }
    if (!this.alive(myGen)) {
      void session.stop().catch(() => {});
      return;
    }
    this.session = session;
    this.cancelTicker = this.d.scheduler.setInterval(() => this.onTick(myGen), this.tickMs);
  }

  private onTick(myGen: number): void {
    if (!this.alive(myGen)) return;
    const d = this.endpointer.tick(this.d.clock.now());
    if (d.action === "wait") return;
    this.teardownTicker();
    if (d.action === "timeout") {
      void this.onTimeout(myGen);
    } else {
      void this.onCandidate(myGen, d.text, d.reason);
    }
  }

  /** The endpointer proposed an utterance end. Resolve it. */
  private async onCandidate(
    myGen: number,
    triggerText: string,
    reason: "silence" | "max-length",
  ): Promise<void> {
    const final = await this.stopSession();
    if (!this.alive(myGen)) return;
    const utterance = (final && final.trim()) || triggerText.trim();

    // Confirm handshake takes precedence over everything.
    if (this.confirming) {
      await this.resolveConfirm(myGen, utterance);
      return;
    }

    // Menu answer: bypass judge/interceptors/risk — send straight through.
    if (this.pendingChoice) {
      this.accumulator = "";
      await this.dispatch(myGen, utterance);
      return;
    }

    const combined = (this.accumulator ? `${this.accumulator} ${utterance}` : utterance).trim();
    if (!combined) {
      // Heard nothing usable — keep listening.
      await this.beginListen();
      return;
    }

    // The hard cap force-submits (safety net) — skip the semantic judge.
    if (reason !== "max-length") {
      this.setState("judging", { text: combined });
      const verdict = await this.d.judge.judge({
        transcript: combined,
        trailingSilenceMs: this.endpointCfg.silenceMs,
        surface: this.d.surface,
        pendingChoice: false,
      });
      if (!this.alive(myGen)) return;
      if (!verdict.complete) {
        // Still thinking — accumulate this fragment and keep listening.
        this.accumulator = combined;
        await this.beginListen();
        return;
      }
    }

    this.accumulator = "";
    await this.handleComplete(myGen, combined);
  }

  /** A complete instruction: interceptors → risk gate → dispatch. */
  private async handleComplete(myGen: number, text: string): Promise<void> {
    // 1) Local interceptors (machine-switch, surface intents).
    for (const it of this.d.interceptors ?? []) {
      const r = await it.intercept(text, this.ctx(false));
      if (!this.alive(myGen)) return;
      if (r) {
        if (r.effect) {
          try {
            await r.effect();
          } catch {
            /* a side-effect failure must not strand the loop */
          }
          if (!this.alive(myGen)) return;
        }
        await this.speakThenResume(myGen, r.spoken, true);
        return;
      }
    }

    // 2) Risk gate — stage the instruction and ask for a spoken confirm.
    if (this.d.risk) {
      const a = this.d.risk.assess(text);
      if (a.risky) {
        this.confirming = { instruction: text };
        this.setState("confirming", { text: a.prompt });
        await this.speakThenListen(myGen, a.prompt);
        return;
      }
    }

    // 3) Safe → dispatch to the runner.
    await this.dispatch(myGen, text);
  }

  private async resolveConfirm(myGen: number, utterance: string): Promise<void> {
    const verdict = this.d.risk
      ? this.d.risk.interpretReply(utterance)
      : "confirm";
    if (verdict === "confirm") {
      const instr = this.confirming!.instruction;
      this.confirming = null;
      this.accumulator = "";
      await this.dispatch(myGen, instr);
    } else if (verdict === "cancel") {
      this.confirming = null;
      this.accumulator = "";
      await this.speakThenResume(myGen, "Cancelled. Nothing ran.", true);
    } else {
      // Unclear — keep the instruction staged and re-ask.
      this.setState("confirming");
      await this.speakThenListen(myGen, "Say yes to run it, or no to cancel.");
    }
  }

  private async dispatch(myGen: number, instruction: string): Promise<void> {
    this.setState("dispatching", { text: instruction });
    const wasPendingChoice = this.pendingChoice;
    const reply = await this.d.agent.send(instruction, this.ctx(wasPendingChoice));
    if (!this.alive(myGen)) return;
    this.pendingChoice = reply.awaitingChoice;
    await this.speakThenResume(myGen, reply.spoken, true);
  }

  private async onTimeout(myGen: number): Promise<void> {
    await this.stopSession();
    if (!this.alive(myGen)) return;
    if (this.accumulator) {
      // The driver said something, then went quiet long enough — treat the
      // accumulated thought as complete rather than dropping it.
      const t = this.accumulator;
      this.accumulator = "";
      await this.handleComplete(myGen, t);
      return;
    }
    // Truly nothing. Keep listening a few rounds, then park to idle.
    this.emptyTimeouts++;
    if (this.emptyTimeouts >= this.maxIdleTimeouts) {
      this.running = false;
      this.setState("idle");
      return;
    }
    await this.beginListen();
  }

  // ── speech helpers (all reset emptyTimeouts — we clearly have a live user) ─

  /** Speak, then auto-resume the listen loop (the hands-free default). */
  private async speakThenResume(
    myGen: number,
    text: string,
    turnComplete = false,
  ): Promise<void> {
    this.emptyTimeouts = 0;
    this.setState("speaking", { text, turnComplete });
    await this.d.tts.speak(text, { locale: this.d.locale });
    if (!this.alive(myGen)) return;
    await this.beginListen();
  }

  /** Speak a prompt that expects an answer, then listen (confirm handshake). */
  private async speakThenListen(myGen: number, text: string): Promise<void> {
    this.emptyTimeouts = 0;
    await this.d.tts.speak(text, { locale: this.d.locale });
    if (!this.alive(myGen)) return;
    await this.beginListen();
  }

  private async stopSession(): Promise<string> {
    const s = this.session;
    this.session = null;
    if (!s) return "";
    try {
      return await s.stop();
    } catch {
      return "";
    }
  }
}
