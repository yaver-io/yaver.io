/**
 * useHandsFreeVoice.ts — the thin React seam between a surface screen and the
 * shared VoiceConversationCore. This is the ONLY React-coupled file in voice/;
 * the core, adapters, and judge stay pure so they unit-test under tsx.
 *
 * The core is built once and kept for the surface's lifetime. Mutable inputs
 * (the active device, the machine list, TTS prefs) must be read through refs by
 * the caller's `makeOptions` so a machine-switch does NOT require rebuilding the
 * core — see car-voice-coding.tsx for the pattern.
 */
import { useCallback, useEffect, useRef, useState } from "react";
import type { VoiceConversationCore } from "./conversationCore";
import { createVoiceCore, type CreateVoiceCoreOptions } from "./createVoiceCore";
import type { VoiceCoreEvent, VoiceState } from "./types";

export interface UseHandsFreeVoice {
  state: VoiceState;
  /** Best transcript while listening; the spoken line while speaking. */
  text: string;
  running: boolean;
  start: () => void;
  stop: () => void;
  /** Barge-in / stop toggle for the surface's single big control. */
  toggle: () => void;
  interrupt: () => void;
}

export function useHandsFreeVoice(
  makeOptions: () => Omit<CreateVoiceCoreOptions, "listener">,
  onEvent?: (ev: VoiceCoreEvent) => void,
): UseHandsFreeVoice {
  const [state, setState] = useState<VoiceState>("idle");
  const [text, setText] = useState("");
  const coreRef = useRef<VoiceConversationCore | null>(null);
  const makeRef = useRef(makeOptions);
  makeRef.current = makeOptions;
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  const ensureCore = useCallback((): VoiceConversationCore => {
    if (coreRef.current) return coreRef.current;
    coreRef.current = createVoiceCore({
      ...makeRef.current(),
      listener: (ev) => {
        setState(ev.state);
        if (ev.text !== undefined) setText(ev.text);
        onEventRef.current?.(ev);
      },
    });
    return coreRef.current;
  }, []);

  const start = useCallback(() => {
    ensureCore().start();
  }, [ensureCore]);

  const stop = useCallback(() => {
    coreRef.current?.stop();
  }, []);

  const interrupt = useCallback(() => {
    coreRef.current?.interrupt();
  }, []);

  const toggle = useCallback(() => {
    const core = coreRef.current;
    if (core && core.isRunning()) {
      // Speaking → barge-in; otherwise leave the loop.
      if (core.currentState === "speaking") core.interrupt();
      else core.stop();
    } else {
      ensureCore().start();
    }
  }, [ensureCore]);

  // Always release the mic/session when the screen unmounts.
  useEffect(() => {
    return () => {
      coreRef.current?.stop();
      coreRef.current = null;
    };
  }, []);

  return { state, text, running: state !== "idle", start, stop, toggle, interrupt };
}
