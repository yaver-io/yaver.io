/**
 * interceptors.ts — surface-specific handlers that answer a complete
 * instruction WITHOUT hitting the runner, plus the risk gate. Each wraps an
 * existing, tsx-tested car lib so the semantics live in one place and the
 * shared core stays surface-agnostic.
 */
import type {
  InstructionInterceptor,
  InterceptResult,
  RiskPolicy,
} from "../types";
import {
  classifyMachineSwitch,
  matchMachine,
  spokenForMachineSwitch,
} from "../../carMachineSwitch";
import { executeCarSurfaceIntent } from "../../carSurfaceIntent";
import { assessRisk, interpretConfirmReply } from "../../carVoiceConfirm";

export interface MachineOption {
  id: string;
  name: string;
  aliases: (string | undefined)[];
}

/**
 * "switch to pokayoke" — retarget the active machine by voice. On CarPlay there
 * is no on-screen picker (Apple forbids it while driving), so the spoken name is
 * the only handle; we always speak the machine back so a mishear is caught by
 * ear before anything runs on the wrong box.
 */
export function machineSwitchInterceptor(
  getMachines: () => MachineOption[],
  onSwitch: (deviceId: string) => void,
): InstructionInterceptor {
  return {
    async intercept(text): Promise<InterceptResult | null> {
      const req = classifyMachineSwitch(text);
      if (!req) return null;
      // MachineOption.aliases is permissive ((string|undefined)[]) so callers can
      // splat optional hints; matchMachine wants clean string aliases — filter at
      // the boundary rather than force every caller to pre-clean.
      const machines = getMachines().map((m) => ({
        ...m,
        aliases: (m.aliases ?? []).filter((a): a is string => !!a),
      }));
      const machine = matchMachine(req.spokenName, machines);
      const spoken = spokenForMachineSwitch(machine, req.spokenName);
      return {
        spoken,
        effect: machine ? () => onSwitch(machine.id) : undefined,
      };
    },
  };
}

/**
 * Car assistant intents (meetings / mail / git / maps / media / EV) that run
 * through /ops on the chosen runtime instead of becoming coding tasks.
 * callCarOps is injected so this adapter never imports the network layer.
 */
export function surfaceIntentInterceptor(
  callCarOps: (verb: string, payload: Record<string, unknown>) => Promise<unknown>,
): InstructionInterceptor {
  return {
    async intercept(text): Promise<InterceptResult | null> {
      try {
        const r = await executeCarSurfaceIntent(text, callCarOps);
        if (r.handled) return { spoken: r.spoken };
        return null;
      } catch {
        // A surface-op failure shouldn't sink the turn — let it fall through to
        // the runner (or surface a spoken error there).
        return { spoken: "I couldn't reach that service." };
      }
    },
  };
}

/**
 * The hard gate CLAUDE.md requires: deploy / push / delete / force never run
 * without an explicit spoken confirm. Wraps carVoiceConfirm so the risk regexes
 * and the yes/no parser are shared with the phone/watch paths.
 */
export function carRiskPolicy(): RiskPolicy {
  return {
    assess: (text) => assessRisk(text),
    interpretReply: (text) => interpretConfirmReply(text),
  };
}
