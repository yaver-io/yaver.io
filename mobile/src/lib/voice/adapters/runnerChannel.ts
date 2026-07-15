/**
 * runnerChannel.ts — AgentChannelAdapter that commits one complete instruction
 * to the LIVE remote runner session (claude code / codex / opencode) the user
 * already has running, via POST /runner/session/turn.
 *
 * Per the product owner: handle the conversation with the real coding runner on
 * the remote box, NOT a separate cloud voice pipeline (no Flux, no second bill).
 * The runner is the user's own Claude Max / ChatGPT Plus session on their own
 * machine — paid once, to them.
 *
 * All the hard parts — mapping a spoken "yes"/"one" to a menu digit, never
 * reading code aloud, clamping the pane to one spoken sentence, never throwing —
 * already live in carSessionTurn.ts (pure + tsx-tested). This adapter is a thin
 * shell over dispatchSessionTurn so that logic is shared, not re-implemented.
 */
import type { AgentChannelAdapter, AgentReply, TurnContext } from "../types";
import { dispatchSessionTurn, type SessionTurnDep } from "../../carSessionTurn";

export interface RunnerChannelDeps {
  /** Drives one session turn on the box. In production this wraps
   *  quicClient.runnerSessionTurn(deviceId, text, choice). */
  sessionTurn: SessionTurnDep;
}

export function createRunnerChannel(deps: RunnerChannelDeps): AgentChannelAdapter {
  return {
    async send(instruction: string, ctx: TurnContext): Promise<AgentReply> {
      const r = await dispatchSessionTurn(
        instruction,
        deps.sessionTurn,
        ctx.pendingChoice,
      );
      return {
        spoken: r.spoken,
        awaitingChoice: r.awaitingChoice,
        options: r.options,
        error: r.error,
      };
    },
  };
}
