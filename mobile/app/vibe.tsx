// Vibe — voice-first "keep talking, keep building" surface for the Tasks flow.
//
// The wedge: on a phone, texting a coding agent is a terrible vibing
// experience — there's no room and no flow. So here you just TALK. Speak a
// change → it's transcribed on-device → the semantic judge decides when your
// thought is finished (you can pause to think) → it's committed to the LIVE
// runner session (claude / codex) on your box → a ONE-sentence result is read
// back → the mic reopens. Hands-free, no submit button.
//
// And mid-flow you can say "load me the app with Hermes" / "load sfmg" / "just
// load it" — the Yaver container loads that guest app (with the feedback
// overlay), you poke at the running thing, tap "Back to Yaver", and keep
// vibing. That "load-app-by-voice" step is handled locally (loadAppInterceptor)
// and never reaches the runner as a prompt.
//
// The screen shows summarized, checklist-style cards — like Codex / Claude Code
// — never a raw transcript dump: each turn is one row with a status glyph, the
// instruction, and a one-line result. Risky verbs (deploy / push / delete /
// force) still ask for a spoken confirm via the shared risk gate.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  Text,
  View,
} from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { useAuth } from "../src/context/AuthContext";
import { quicClient } from "../src/lib/quic";
import { loadLocalSpeechConfig } from "../src/lib/auth";
import { openAppBus } from "../src/lib/openAppBus";
import { useHandsFreeVoice } from "../src/lib/voice/useHandsFreeVoice";
import type { CreateVoiceCoreOptions } from "../src/lib/voice/createVoiceCore";
import type { VoiceCoreEvent } from "../src/lib/voice/types";
import type { LoadAppIntent } from "../src/lib/voice/loadAppIntent";

type TurnStatus = "working" | "done" | "loaded" | "error";

interface VibeTurn {
  id: string;
  kind: "task" | "load";
  title: string;
  summary?: string;
  status: TurnStatus;
  at: number;
}

const GLYPH: Record<TurnStatus, string> = {
  working: "○",
  done: "✓",
  loaded: "▸",
  error: "⚠",
};

export default function VibeScreen() {
  const c = useColors();
  const router = useRouter();
  const { activeDevice, devices, selectDevice } = useDevice() as any;
  const { token } = useAuth();
  // Route params — the composer's "back to Vibe" pill hands the currently-
  // typed text over as `?prompt=…` so the mic↔text switch is symmetric
  // (bc81f993e set up the mic-only FAB; audit follow-up 2026-07-19 closed
  // the loop). Seeding lastHeardRef with whatever the user just typed makes
  // "Prefer to type?" on this screen bring the SAME text back if they change
  // their mind again — exactly how lastHeardRef preserved speech going
  // Vibe → composer.
  const params = useLocalSearchParams<{ prompt?: string }>();
  const initialPrompt = typeof params.prompt === "string" ? params.prompt : "";

  const [turns, setTurns] = useState<VibeTurn[]>([]);
  const [live, setLive] = useState(""); // in-progress transcript / status line
  const openTurnRef = useRef<string | null>(null); // id of the working task turn
  // Last thing we actually HEARD the user say, as opposed to whatever is on the
  // live line (which also carries spoken results and confirm prompts). Only the
  // heard text is safe to hand to the composer when they switch to typing.
  const lastHeardRef = useRef(initialPrompt);
  const liveMounted = useRef(true);
  useEffect(() => () => { liveMounted.current = false; }, []);

  // Everything mutable the core reads goes through refs so a machine-switch or
  // a fresh active device never forces the loop to rebuild mid-conversation.
  const devicesRef = useRef<any[]>(devices || []);
  devicesRef.current = devices || [];
  const deviceIdRef = useRef<string>(activeDevice?.id || "");
  deviceIdRef.current = activeDevice?.id || "";
  const speechCfgRef = useRef<Awaited<ReturnType<typeof loadLocalSpeechConfig>>>({});
  useEffect(() => {
    void loadLocalSpeechConfig().then((cfg) => { speechCfgRef.current = cfg; });
  }, []);

  const pushTurn = useCallback((t: VibeTurn) => {
    if (!liveMounted.current) return;
    setTurns((prev) => [t, ...prev].slice(0, 40));
  }, []);
  const patchTurn = useCallback((id: string, patch: Partial<VibeTurn>) => {
    if (!liveMounted.current) return;
    setTurns((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)));
  }, []);

  // "load me the app with Hermes" — load a guest app into the container. Mirrors
  // the tab layout's open_app path (navigate to Hot Reload + publish on the
  // bus), which runs the exact same handleTapProject flow a manual tap would.
  const onLoadApp = useCallback(
    async (intent: LoadAppIntent) => {
      pushTurn({
        id: `load-${Date.now()}`,
        kind: "load",
        title: intent.app || "your apps",
        summary: intent.spoken,
        status: "loaded",
        at: Date.now(),
      });
      router.push("/(tabs)/apps");
      if (intent.app) openAppBus.publish(intent.app);
    },
    [pushTurn, router],
  );

  const makeVoiceOptions = useCallback(
    (): Omit<CreateVoiceCoreOptions, "listener"> => ({
      surface: "phone",
      // Commit to the LIVE runner session on the active box — not a new task.
      sessionTurn: async (text, choice) => {
        const r = await quicClient.runnerSessionTurn(
          deviceIdRef.current,
          text,
          choice,
        );
        return {
          ok: r.ok === true,
          session: r.session || "",
          runner: r.runner,
          sent: r.sent,
          awaitingChoice: r.awaitingChoice === true,
          options: r.options,
          pane: r.pane,
          error: r.error,
        };
      },
      // "switch to my mac mini" — retarget the active device by voice.
      machines: () =>
        devicesRef.current.map((d: any) => ({
          id: d.id || d.deviceId,
          name: d.nickname || d.name || d.hostname || d.deviceId,
          aliases: [
            ...(Array.isArray(d.voiceHints) ? d.voiceHints : []),
            d.alias,
            d.hostname,
            d.name,
          ],
        })),
      onSwitchMachine: (id) => selectDevice?.(id),
      onLoadApp,
      tts: {
        provider: (speechCfgRef.current.ttsProvider as any) || "device",
        apiKey: speechCfgRef.current.apiKey,
        voice: speechCfgRef.current.ttsVoice,
      },
      locale: "en",
    }),
    [onLoadApp, selectDevice],
  );

  const onVoiceEvent = useCallback((ev: VoiceCoreEvent) => {
    if (!liveMounted.current) return;

    // Live status line: the best transcript while listening/judging, the spoken
    // prompt while confirming. Cleared when we drop back to idle.
    if (ev.state === "listening" || ev.state === "judging" || ev.state === "confirming") {
      setLive(ev.text || "");
    } else if (ev.state === "idle") {
      setLive("");
    }
    if ((ev.state === "listening" || ev.state === "judging") && ev.text) {
      lastHeardRef.current = ev.text;
    }

    // A committed coding instruction opens a "working" checklist row.
    if (ev.state === "dispatching" && ev.text) {
      const id = `task-${Date.now()}`;
      openTurnRef.current = id;
      pushTurn({ id, kind: "task", title: ev.text, status: "working", at: Date.now() });
    }

    // Terminal spoken line: close the open task with its one-line result. A
    // turnComplete with no open task is an ack (load / switch / confirm) — that
    // already has its own row or lives on the live line, so we don't duplicate.
    if (ev.turnComplete && ev.text) {
      const openId = openTurnRef.current;
      if (openId) {
        patchTurn(openId, { status: "done", summary: ev.text });
        openTurnRef.current = null;
      }
      setLive(ev.text);
    }
  }, [pushTurn, patchTurn]);

  const handsFree = useHandsFreeVoice(makeVoiceOptions, onVoiceEvent);

  // "I'd rather type this." Voice is the wedge here, not a toll gate — a noisy
  // room, a word the STT keeps mangling, or a long path/flag string are all
  // perfectly good reasons to bail to the keyboard, and being stuck in a voice
  // loop with no visible way out is the opposite of polite.
  //
  // Hand off rather than dead-end: stop the loop, then open the Tasks composer
  // seeded with whatever we last HEARD, so a half-spoken thought survives the
  // switch instead of making them start over. `openNew=1` + `prompt=` is the
  // existing route seed the composer already honours (tasks.tsx route-seed
  // effect), so this needs no new plumbing on the Tasks side.
  const switchToTyping = useCallback(() => {
    handsFree.stop();
    const seed = lastHeardRef.current.trim();
    lastHeardRef.current = "";
    router.push({
      pathname: "/(tabs)/tasks",
      params: seed ? { openNew: "1", prompt: seed } : { openNew: "1" },
    } as any);
  }, [handsFree, router]);

  // ── derived UI state ──────────────────────────────────────────────────
  const state = handsFree.state;
  const listening = state === "listening" || state === "judging";
  const working = state === "dispatching";
  const speaking = state === "speaking" || state === "confirming";
  const micColor = listening
    ? "#ef4444"
    : working
      ? "#8b5cf6"
      : speaking
        ? "#f59e0b"
        : c.accent;
  const statusLabel = !handsFree.running
    ? "Tap to vibe — then just talk"
    : listening
      ? "Listening…"
      : working
        ? "Working…"
        : state === "confirming"
          ? "Confirm needed — say yes or no"
          : speaking
            ? "Speaking… tap to interrupt"
            : "…";

  const card = {
    backgroundColor: c.bgCard,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 12,
    padding: 14,
    marginBottom: 12,
  } as const;

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Vibe" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        {/* Active box */}
        {/* Just the box. The "Apps" button that sat here was removed
            (2026-07-20): Projects is a top-level tab, so this was a second
            door to the same room — and on a voice screen every control that
            is not the mic is a distraction from the one thing you came to do. */}
        <View style={[card, { flexDirection: "row", justifyContent: "space-between", alignItems: "center" }]}>
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>Vibing on</Text>
            <Text style={{ color: c.textPrimary, fontWeight: "700", fontSize: 16 }} numberOfLines={1}>
              {activeDevice?.name || activeDevice?.id || "No box connected"}
            </Text>
          </View>
        </View>

        {/* Big mic — start / stop / barge-in */}
        <View style={{ alignItems: "center", marginVertical: 18 }}>
          <Pressable
            onPress={handsFree.toggle}
            style={{
              width: 180,
              height: 180,
              borderRadius: 90,
              backgroundColor: micColor,
              alignItems: "center",
              justifyContent: "center",
              opacity: working ? 0.9 : 1,
            }}
            accessibilityRole="button"
            accessibilityLabel={statusLabel}
          >
            {working ? (
              <ActivityIndicator color="#fff" size="large" />
            ) : (
              <Text style={{ color: "#fff", fontSize: 52 }}>
                {listening ? "■" : "🎤"}
              </Text>
            )}
          </Pressable>
          <Text style={{ color: c.textPrimary, fontSize: 17, fontWeight: "700", marginTop: 14, textAlign: "center" }}>
            {statusLabel}
          </Text>
          {/* Live STT transcript — while listening the recognised text has to
              be UNMISTAKABLY visible so the user can see whether the phone
              actually heard them (audit §4.1, 2026-07-19). Bigger, higher-
              contrast text, quotes around the partial to make it read like
              speech. While judging/speaking/confirming we drop back to a
              quieter, muted line so the mic status still dominates. */}
          {!!live && listening && (
            <Text
              style={{
                color: c.textPrimary,
                fontSize: 18,
                fontWeight: "600",
                lineHeight: 24,
                marginTop: 10,
                textAlign: "center",
                paddingHorizontal: 8,
              }}
              numberOfLines={4}
            >
              {`“${live}”`}
            </Text>
          )}
          {!!live && !listening && (
            <Text style={{ color: c.textMuted, fontSize: 14, marginTop: 6, textAlign: "center" }} numberOfLines={3}>
              {live}
            </Text>
          )}
          {!activeDevice && (
            <Text style={{ color: c.textMuted, fontSize: 12, marginTop: 10, textAlign: "center" }}>
              Connect a box to run coding tasks. You can still say “load me the app”.
            </Text>
          )}

          {/* Escape hatch to the keyboard. Deliberately quiet — muted, no fill,
              below the mic — so it reads as "also available" rather than
              competing with the voice path. */}
          <Pressable
            onPress={switchToTyping}
            hitSlop={10}
            style={({ pressed }) => ({
              flexDirection: "row",
              alignItems: "center",
              gap: 7,
              marginTop: 18,
              paddingVertical: 9,
              paddingHorizontal: 14,
              borderRadius: 999,
              borderWidth: 1,
              borderColor: c.border,
              opacity: pressed ? 0.6 : 1,
            })}
            accessibilityRole="button"
            accessibilityLabel="Type instead — opens the task composer"
          >
            {/* No keyboard glyph exists in this Ionicons set (checked the
                glyphmap: only keypad-*), so the compose pencil carries it. */}
            <Ionicons name="create-outline" size={16} color={c.textMuted} />
            <Text style={{ color: c.textMuted, fontSize: 13, fontWeight: "600" }}>
              Prefer to type?
            </Text>
          </Pressable>
        </View>

        {/* Checklist of turns — summarized, Codex/Claude-Code style */}
        {turns.length > 0 && (
          <View style={card}>
            {turns.map((t, i) => {
              const glyphColor =
                t.status === "done" ? "#22c55e"
                  : t.status === "loaded" ? c.accent
                    : t.status === "error" ? (c.error || "#ef4444")
                      : c.textMuted;
              return (
                <View
                  key={t.id}
                  style={{
                    flexDirection: "row",
                    gap: 10,
                    paddingVertical: 10,
                    borderBottomWidth: i === turns.length - 1 ? 0 : 1,
                    borderBottomColor: c.border,
                  }}
                >
                  <Text style={{ color: glyphColor, fontSize: 16, fontWeight: "800", width: 18, textAlign: "center" }}>
                    {t.status === "working" ? "" : GLYPH[t.status]}
                  </Text>
                  {t.status === "working" && (
                    <ActivityIndicator size="small" color={c.textMuted} style={{ position: "absolute", left: 12, top: 12 }} />
                  )}
                  <View style={{ flex: 1 }}>
                    <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "600" }} numberOfLines={2}>
                      {t.kind === "load" ? `Load ${t.title}` : t.title}
                    </Text>
                    {!!t.summary && (
                      <Text style={{ color: c.textMuted, fontSize: 13, marginTop: 2 }} numberOfLines={2}>
                        {t.summary}
                      </Text>
                    )}
                  </View>
                </View>
              );
            })}
          </View>
        )}

        <Text style={{ color: c.textMuted, fontSize: 11, textAlign: "center", marginTop: 4 }}>
          Say “load me the app with Hermes”, “switch to my mac mini”, or just
          describe the change. Deploy / push / delete always ask first.
        </Text>
      </ScrollView>
    </View>
  );
}
