/**
 * conversationCore.test.ts — `npx tsx src/lib/voice/conversationCore.test.ts`.
 *
 * Drives the whole hands-free loop under virtual time with fake adapters. No
 * RN, no real timers.
 */
import { VoiceConversationCore, type VoiceCoreDeps } from "./conversationCore";
import { FakeTime } from "./scheduler";
import type {
  AgentChannelAdapter,
  AgentReply,
  AudioCaptureAdapter,
  CaptureSession,
  CompletenessJudge,
  InstructionInterceptor,
  RiskPolicy,
  TtsAdapter,
  TurnContext,
} from "./types";

let passed = 0;
let failed = 0;
function ok(cond: boolean, msg: string) {
  if (cond) passed++;
  else {
    failed++;
    console.error("  ✗ " + msg);
  }
}
function eq(a: unknown, b: unknown, msg: string) {
  ok(JSON.stringify(a) === JSON.stringify(b), `${msg} (got ${JSON.stringify(a)})`);
}

// ── Fakes ──────────────────────────────────────────────────────────────

class FakeCapture implements AudioCaptureAdapter {
  onPartial: ((t: string, atMs: number) => void) | null = null;
  finalText = "";
  starts = 0;
  private ft: FakeTime;
  constructor(ft: FakeTime) {
    this.ft = ft;
  }
  async start(onPartial: (t: string, atMs: number) => void): Promise<CaptureSession> {
    this.starts++;
    this.onPartial = onPartial;
    const self = this;
    return {
      stop: async () => {
        const f = self.finalText;
        self.finalText = "";
        self.onPartial = null;
        return f;
      },
      active: () => self.onPartial !== null,
    };
  }
  /** Simulate the STT engine emitting a (final) transcript now. */
  say(text: string) {
    this.finalText = text;
    this.onPartial?.(text, this.ft.now());
  }
}

class FakeTts implements TtsAdapter {
  spoken: string[] = [];
  stops = 0;
  async speak(text: string): Promise<void> {
    this.spoken.push(text);
  }
  async stop(): Promise<void> {
    this.stops++;
  }
}

class FakeAgent implements AgentChannelAdapter {
  sent: Array<{ text: string; ctx: TurnContext }> = [];
  reply: (text: string, ctx: TurnContext) => AgentReply;
  constructor(reply?: (text: string, ctx: TurnContext) => AgentReply) {
    this.reply =
      reply ?? (() => ({ spoken: "Done.", awaitingChoice: false, options: [] }));
  }
  async send(text: string, ctx: TurnContext): Promise<AgentReply> {
    this.sent.push({ text, ctx });
    return this.reply(text, ctx);
  }
}

/** A judge you script: returns `complete` from a predicate on the transcript. */
function scriptedJudge(isComplete: (t: string) => boolean): CompletenessJudge {
  return {
    async judge(input) {
      if (input.pendingChoice) return { complete: true, wantsAnswer: true, source: "heuristic" };
      return { complete: isComplete(input.transcript), wantsAnswer: true, source: "model" };
    },
  };
}

function build(over: Partial<VoiceCoreDeps> & { ft: FakeTime; capture: FakeCapture }): {
  core: VoiceConversationCore;
  states: string[];
} {
  const { ft, capture, ...rest } = over;
  const states: string[] = [];
  const core = new VoiceConversationCore({
    capture,
    tts: rest.tts ?? new FakeTts(),
    agent: rest.agent ?? new FakeAgent(),
    judge: rest.judge ?? scriptedJudge(() => true),
    surface: "car",
    clock: ft,
    scheduler: ft,
    listener: (ev) => states.push(ev.state),
    ...rest,
  });
  return { core, states };
}

async function step(ft: FakeTime, ms: number) {
  ft.advance(ms);
  await ft.flush();
}

async function main() {
// ── 1) Simple complete turn: say → dispatch → speak → resume ─────────────
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const tts = new FakeTts();
  const agent = new FakeAgent(() => ({ spoken: "Added the button.", awaitingChoice: false, options: [] }));
  const { core } = build({ ft, capture, tts, agent });
  core.start();
  await ft.flush(); // let beginListen arm
  capture.say("add a login button");
  await step(ft, 1400); // past silenceMs → submit → judge(complete) → dispatch → speak → resume
  eq(agent.sent.length, 1, "one instruction dispatched");
  eq(agent.sent[0]?.text, "add a login button", "dispatched the transcript");
  eq(tts.spoken, ["Added the button."], "spoke the agent reply");
  ok(capture.starts >= 2, "auto-resumed listening (mic reopened)");
  core.stop();
})();

// ── 2) Thinking-pause accumulation: incomplete fragment waits for the rest ─
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const agent = new FakeAgent();
  // "complete" only once the word "logs" appears.
  const judge = scriptedJudge((t) => t.includes("logs"));
  const { core } = build({ ft, capture, agent, judge });
  core.start();
  await ft.flush();

  capture.say("add a button"); // judge: incomplete → accumulate, keep listening
  await step(ft, 1400);
  eq(agent.sent.length, 0, "incomplete fragment not dispatched");

  capture.say("that logs in"); // judge: complete → dispatch combined
  await step(ft, 1400);
  eq(agent.sent.length, 1, "dispatched after completion");
  eq(agent.sent[0]?.text, "add a button that logs in", "accumulated both fragments in order");
  core.stop();
})();

// ── 3) Menu choice: agent asks, next utterance answers with pendingChoice ─
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const agent = new FakeAgent((text, ctx) => {
    if (!ctx.pendingChoice) return { spoken: "Choose one or two.", awaitingChoice: true, options: ["a", "b"] };
    return { spoken: "Picked it.", awaitingChoice: false, options: [] };
  });
  const { core } = build({ ft, capture, agent });
  core.start();
  await ft.flush();

  capture.say("open the menu");
  await step(ft, 1400);
  eq(agent.sent[0]?.ctx.pendingChoice, false, "first turn is a fresh instruction");

  capture.say("one"); // must go straight through as a menu answer
  await step(ft, 1400);
  eq(agent.sent.length, 2, "menu answer dispatched");
  eq(agent.sent[1]?.ctx.pendingChoice, true, "answer carries pendingChoice=true");
  core.stop();
})();

// ── 4) Risk gate: risky command needs a spoken yes ───────────────────────
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const agent = new FakeAgent();
  const tts = new FakeTts();
  const risk: RiskPolicy = {
    assess: (t) => ({ risky: /deploy|delete|push/.test(t), prompt: "Deploy to prod? Say yes or no." }),
    interpretReply: (t) => (/^(yes|yeah|confirm)/i.test(t.trim()) ? "confirm" : /^(no|cancel)/i.test(t.trim()) ? "cancel" : "unclear"),
  };
  const { core } = build({ ft, capture, agent, tts, risk });
  core.start();
  await ft.flush();

  capture.say("deploy to prod");
  await step(ft, 1400);
  eq(agent.sent.length, 0, "risky command NOT dispatched before confirm");
  ok(tts.spoken.includes("Deploy to prod? Say yes or no."), "asked for confirmation");

  capture.say("yes");
  await step(ft, 1400);
  eq(agent.sent.length, 1, "dispatched after 'yes'");
  eq(agent.sent[0]?.text, "deploy to prod", "dispatched the staged instruction");
  core.stop();
})();

// ── 4b) Risk gate: 'no' cancels, nothing dispatched ──────────────────────
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const agent = new FakeAgent();
  const tts = new FakeTts();
  const risk: RiskPolicy = {
    assess: (t) => ({ risky: /delete/.test(t), prompt: "Delete it? Yes or no." }),
    interpretReply: (t) => (/^no/i.test(t.trim()) ? "cancel" : "unclear"),
  };
  const { core } = build({ ft, capture, agent, tts, risk });
  core.start();
  await ft.flush();
  capture.say("delete the database");
  await step(ft, 1400);
  capture.say("no");
  await step(ft, 1400);
  eq(agent.sent.length, 0, "cancelled command never dispatched");
  ok(tts.spoken.some((s) => /cancelled/i.test(s)), "spoke a cancellation");
  core.stop();
})();

// ── 5) Interceptor handles locally, bypassing the agent ──────────────────
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const agent = new FakeAgent();
  let switched = "";
  const interceptor: InstructionInterceptor = {
    async intercept(text) {
      const m = text.match(/switch to (\w+)/);
      if (!m) return null;
      return { spoken: `Now on ${m[1]}.`, effect: () => { switched = m[1]; } };
    },
  };
  const tts = new FakeTts();
  const { core } = build({ ft, capture, agent, tts, interceptors: [interceptor] });
  core.start();
  await ft.flush();
  capture.say("switch to pokayoke");
  await step(ft, 1400);
  eq(agent.sent.length, 0, "interceptor kept it off the agent");
  eq(switched, "pokayoke", "interceptor side-effect ran");
  ok(tts.spoken.includes("Now on pokayoke."), "spoke the interceptor reply");
  core.stop();
})();

// ── 6) Barge-in: interrupt() during speaking stops TTS and re-listens ────
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const tts = new FakeTts();
  // Agent reply won't matter; we interrupt right after dispatch.
  const { core } = build({ ft, capture, tts });
  core.start();
  await ft.flush();
  capture.say("what is failing");
  await step(ft, 1400); // dispatched + spoke + resumed
  const startsBefore = capture.starts;
  core.interrupt();
  await ft.flush();
  ok(tts.stops >= 1, "interrupt stopped TTS");
  ok(capture.starts > startsBefore, "interrupt re-opened the mic");
  core.stop();
})();

// ── 7) No-speech parks to idle after maxIdleTimeouts ─────────────────────
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const { core, states } = build({ ft, capture, maxIdleTimeouts: 2 });
  core.start();
  await ft.flush();
  // Two no-speech timeouts (8s each) → park.
  await step(ft, 8200);
  await step(ft, 8200);
  ok(!core.isRunning(), "parked after repeated silence");
  ok(states[states.length - 1] === "idle", "ended in idle");
})();

// ── 8) stop() releases capture + tts ─────────────────────────────────────
await (async () => {
  const ft = new FakeTime();
  const capture = new FakeCapture(ft);
  const tts = new FakeTts();
  const { core } = build({ ft, capture, tts });
  core.start();
  await ft.flush();
  core.stop();
  await ft.flush();
  eq(core.currentState, "idle", "state idle after stop");
  ok(!core.isRunning(), "not running after stop");
})();

}

main().then(() => {
  console.log(`\nconversationCore: ${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
});
