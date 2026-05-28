// Glass terminal — a TUI-style screen optimised for AR-glasses viewing
// (Xreal One via USB-C from iPhone or Beam Pro). High contrast, big mono
// font, minimal chrome. Hosts EITHER:
//
//   • "agent" mode  — drives the existing on-phone runYaverAgent() loop
//                     (BYOK / subscription token, no remote box needed).
//                     This is the Path B in BEAM_PRO_DEV.md — full Claude-
//                     Code-like experience running natively on the phone.
//
//   • "shell" mode  — connects to the connected Yaver agent's /ws/terminal
//                     PTY endpoint (Path A in BEAM_PRO_DEV.md when the
//                     agent is on a remote dev box you SSH'd into; works
//                     the same whether the agent is local or remote).
//
// Built-in extras for the AR-glasses workflow:
//   - Device picker  — long-press the title in shell mode to switch which
//                      machine you're driving (`useDevice().selectDevice`
//                      changes the quicClient base URL → next reconnect
//                      lands on the chosen box).
//   - Auto-reconnect — shell websocket retries with 1/2/4/8/16/30 s backoff;
//                      survives the 3-second tunnel hiccups you get when an
//                      iPhone moves between cell towers.
//   - Saved prompts  — agent mode persists short prompt bookmarks in
//                      AsyncStorage; tap the ☆ chip to recall, long-press
//                      to delete, ⊕ to save the current input.
//   - Clear conv     — agent mode also has a ⌫ chip to reset history
//                      between conversations without leaving the screen.
//   - Vibe bar       — Path-C ("remote dev + on-device app under test"):
//                      one-tap chips for Hermes reload / wireless push /
//                      mobile-project status / Expo doctor. They run a
//                      detached on-phone agent round-trip so the shell
//                      websocket and tmux session stay live throughout.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Keyboard,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { useRouter } from "expo-router";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { useDevice, type Device } from "../src/context/DeviceContext";
import { quicClient } from "../src/lib/quic";
import {
  runYaverAgent,
  type YaverAgentProgressEvent,
  type YaverAgentHistoryTurn,
} from "../src/lib/yaverAgentRunner";
import type { YaverAgentToolContext } from "../src/lib/yaverAgentTools";
import {
  startRealtimeTranscribe,
  speakText,
  DEFAULT_TTS_MODEL,
  DEFAULT_TTS_VOICE,
} from "../src/lib/speech";
import { loadLocalSpeechConfig } from "../src/lib/auth";
import {
  callMobileHermesReload,
  callDeviceBroadcastCommand,
  callMobileProjectStatus,
  callMobileHermesDoctor,
} from "../src/lib/yaverMcpDirect";
import {
  fetchProjectKind,
  invalidateProjectKindCache,
  type ProjectKindResult,
} from "../src/lib/projectKind";

type Mode = "agent" | "shell";

type Line = {
  kind: "user" | "model" | "tool" | "sys" | "err";
  text: string;
};

const PAL = {
  bg: "#000000",
  fg: "#e5e7eb",
  muted: "#9ca3af",
  prompt: "#60a5fa",
  user: "#fbbf24",
  model: "#e5e7eb",
  tool: "#34d399",
  err: "#f87171",
  sys: "#94a3b8",
  border: "#1f2937",
  chip: "#111827",
  chipText: "#d1d5db",
  accent: "#a78bfa",
  star: "#fbbf24",
};

const FONT_SIZE = 14;
const LINE_HEIGHT = 20;
const SAVED_PROMPTS_KEY = "@yaver/glass_terminal/saved_prompts/v1";
const RELOAD_TARGET_KEY = "@yaver/glass_terminal/reload_target/v1";
const MAX_SAVED_PROMPTS = 30;
// Backoff steps (ms) for shell-mode auto-reconnect. Last value repeats.
const RECONNECT_BACKOFF_MS = [1_000, 2_000, 4_000, 8_000, 16_000, 30_000];

export default function GlassTerminalScreen() {
  const router = useRouter();
  const insets = useSafeAreaInsets();
  const { connectionStatus, devices, primaryDeviceId, selectDevice } = useDevice();

  const [mode, setMode] = useState<Mode>("agent");
  const [lines, setLines] = useState<Line[]>([
    { kind: "sys", text: "— glass terminal — switch mode top-right · long-press title for devices —" },
  ]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [shellReady, setShellReady] = useState(false);
  const [devicePickerOpen, setDevicePickerOpen] = useState(false);
  const [savedPrompts, setSavedPrompts] = useState<string[]>([]);
  const [vibeBusy, setVibeBusy] = useState<string | null>(null);
  const [recording, setRecording] = useState(false);
  const [autoTts, setAutoTts] = useState(false);
  // projectKind drives the vibe-bar chip set. Defaults to mobile so the
  // existing Hermes-reload / wire-push UX is preserved BYTE-IDENTICALLY
  // until the agent answers /project/kind otherwise. Per memory
  // [feedback_always_deploy_yaver] and the mobile-only-wire-push rule,
  // we never silently replace the mobile chips for a mobile project.
  const [projectKind, setProjectKind] = useState<ProjectKindResult | null>(null);
  // Cross-device reload target (Phase 7). When set, the ⟳ chip routes
  // the BlackBox reload command to this device id instead of the local
  // broadcast. Persisted to AsyncStorage so it survives app re-launches.
  const [reloadTargetId, setReloadTargetId] = useState<string | null>(null);
  const [reloadTargetPickerOpen, setReloadTargetPickerOpen] = useState(false);
  const recorderRef = useRef<{ stop: () => Promise<string> } | null>(null);
  const speechCfgRef = useRef<{ tts?: { provider: "device" | "openai" | "openrouter"; apiKey?: string; model?: string; voice?: string } }>({});
  // submitRef avoids a circular dep between submit (uses input) and
  // stopRecording (calls submit). Always set to the latest submit.
  const submitRef = useRef<(() => Promise<void>) | null>(null);
  // Same trick for triggerReloadDirect — stopRecording's voice keyword
  // shortcut needs to call it but it's defined later in the render.
  const triggerReloadDirectRef = useRef<(() => Promise<void>) | null>(null);
  const scrollRef = useRef<ScrollView | null>(null);
  const inputRef = useRef<TextInput | null>(null);

  const historyRef = useRef<YaverAgentHistoryTurn[]>([]);
  const wsRef = useRef<WebSocket | null>(null);
  const pendingShell = useRef<string>("");
  const flushTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnectAttempts = useRef(0);
  const shouldStayConnected = useRef(false);
  const abortRef = useRef<AbortController | null>(null);

  // ── Output helpers ─────────────────────────────────────────────────────
  const appendLine = useCallback((kind: Line["kind"], text: string) => {
    setLines((prev) => {
      const next = [...prev, { kind, text }];
      return next.length > 1000 ? next.slice(-800) : next;
    });
    requestAnimationFrame(() => scrollRef.current?.scrollToEnd({ animated: false }));
  }, []);

  // ── Load saved prompts from AsyncStorage on mount ──────────────────────
  useEffect(() => {
    let cancelled = false;
    AsyncStorage.getItem(SAVED_PROMPTS_KEY)
      .then((raw) => {
        if (cancelled || !raw) return;
        try {
          const parsed = JSON.parse(raw);
          if (Array.isArray(parsed)) {
            setSavedPrompts(parsed.filter((x): x is string => typeof x === "string").slice(0, MAX_SAVED_PROMPTS));
          }
        } catch {
          /* corrupt entry — ignore */
        }
      })
      .catch(() => { /* AsyncStorage failure is non-fatal */ });
    return () => { cancelled = true; };
  }, []);

  // ── Load persisted reload-target on mount (Phase 7). Survives app
  //    restarts so the user doesn't have to re-pick after each launch.
  useEffect(() => {
    let cancelled = false;
    AsyncStorage.getItem(RELOAD_TARGET_KEY).then((id) => {
      if (!cancelled && id) setReloadTargetId(id);
    }).catch(() => {});
    return () => { cancelled = true; };
  }, []);

  const setReloadTarget = useCallback((id: string | null) => {
    setReloadTargetId(id);
    if (id) AsyncStorage.setItem(RELOAD_TARGET_KEY, id).catch(() => {});
    else AsyncStorage.removeItem(RELOAD_TARGET_KEY).catch(() => {});
  }, []);

  // ── Re-classify the attached agent's working dir whenever the user
  //    picks a different device. The vibe bar swaps chips based on the
  //    result; an older agent without /project/kind returns "generic"
  //    via the lib's fallback path, which keeps a safe minimal chip set.
  useEffect(() => {
    let cancelled = false;
    const ac = new AbortController();
    // Invalidate first so the new device's kind is never masked by the
    // previous device's cached result.
    invalidateProjectKindCache();
    fetchProjectKind({ signal: ac.signal })
      .then((res) => { if (!cancelled) setProjectKind(res); })
      .catch(() => { /* AbortError — device switched again, fine */ });
    return () => { cancelled = true; ac.abort(); };
  }, [primaryDeviceId]);

  // ── Load TTS config so 🔊 toggle uses the user's chosen voice provider.
  //    STT uses on-device whisper.rn via startRealtimeTranscribe — no
  //    config needed for the mic button to work.
  useEffect(() => {
    loadLocalSpeechConfig().then((cfg) => {
      const provider = (cfg.ttsProvider ?? "device") as "device" | "openai" | "openrouter";
      speechCfgRef.current.tts = {
        provider,
        apiKey: cfg.apiKey,
        model: cfg.ttsModel || DEFAULT_TTS_MODEL,
        voice: cfg.ttsVoice || DEFAULT_TTS_VOICE,
      };
    }).catch(() => { /* fall back to device TTS */ });
  }, []);

  // ── Voice: mic toggle (whisper.rn on-device STT) ───────────────────────
  const startRecording = useCallback(async () => {
    if (recording) return;
    setRecording(true);
    appendLine("sys", "— 🎤 listening — tap mic again to send —");
    try {
      const controller = await startRealtimeTranscribe((partial) => {
        // Live update the input field as whisper streams partials.
        setInput(partial);
      });
      recorderRef.current = controller;
    } catch (e: unknown) {
      setRecording(false);
      appendLine("err", e instanceof Error ? e.message : "mic failed — check Speech permission");
    }
  }, [recording, appendLine]);

  const stopRecording = useCallback(async (autoSubmit = true) => {
    const rec = recorderRef.current;
    recorderRef.current = null;
    setRecording(false);
    if (!rec) return;
    try {
      const finalText = await rec.stop();
      if (finalText && finalText.trim()) {
        const trimmed = finalText.trim();
        // Voice keyword shortcut — single-word "reload" (any language)
        // jumps straight to the direct-MCP reload path without an LLM
        // round-trip. Saves ~1.5-3 s on the most common voice command
        // for the AR-glasses workflow.
        if (autoSubmit && isReloadKeyword(trimmed)) {
          setInput("");
          appendLine("user", `🎤 ${trimmed}`);
          setTimeout(() => { void triggerReloadDirectRef.current?.(); }, 0);
          return;
        }
        setInput(trimmed);
        if (autoSubmit) {
          // Defer one tick so the input update settles, then submit.
          setTimeout(() => { void submitRef.current?.(); }, 0);
        }
      } else {
        appendLine("sys", "— 🎤 nothing heard —");
      }
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : "transcription failed");
    }
  }, [appendLine]);

  // ── Voice: TTS speaker for model output ────────────────────────────────
  const speakIfEnabled = useCallback((text: string) => {
    if (!autoTts) return;
    const trimmed = text.trim();
    if (!trimmed) return;
    speakText(trimmed, speechCfgRef.current.tts ?? { provider: "device" }).catch(() => {
      // TTS failure is non-fatal — user still sees the text on screen.
    });
  }, [autoTts]);

  const persistSavedPrompts = useCallback((next: string[]) => {
    setSavedPrompts(next);
    AsyncStorage.setItem(SAVED_PROMPTS_KEY, JSON.stringify(next)).catch(() => {});
  }, []);

  const saveCurrentPrompt = useCallback(() => {
    const trimmed = input.trim();
    if (!trimmed) return;
    if (savedPrompts.includes(trimmed)) return;
    const next = [trimmed, ...savedPrompts].slice(0, MAX_SAVED_PROMPTS);
    persistSavedPrompts(next);
  }, [input, savedPrompts, persistSavedPrompts]);

  const deleteSavedPrompt = useCallback((p: string) => {
    persistSavedPrompts(savedPrompts.filter((x) => x !== p));
  }, [savedPrompts, persistSavedPrompts]);

  const clearHistory = useCallback(() => {
    historyRef.current = [];
    setLines([{ kind: "sys", text: "— conversation cleared —" }]);
  }, []);

  // ── Shell mode wiring with auto-reconnect ──────────────────────────────
  const connectShell = useCallback(() => {
    // Cancel any pending reconnect.
    if (reconnectTimer.current) {
      clearTimeout(reconnectTimer.current);
      reconnectTimer.current = null;
    }
    // Close any existing socket without triggering a reconnect cascade.
    if (wsRef.current) {
      const old = wsRef.current;
      wsRef.current = null;
      try { old.close(); } catch { /* harmless */ }
    }

    let url: string;
    try {
      url = buildTerminalWsUrl();
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : "shell url build failed");
      return;
    }

    let ws: WebSocket;
    try {
      ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : "shell connect failed");
      scheduleReconnect();
      return;
    }
    wsRef.current = ws;

    ws.onopen = () => {
      reconnectAttempts.current = 0;
      setShellReady(true);
      appendLine("sys", "— shell connected —");
    };
    ws.onmessage = (e) => {
      const raw = typeof e.data === "string"
        ? e.data
        : new TextDecoder().decode(new Uint8Array(e.data as ArrayBuffer));
      pendingShell.current += stripAnsi(raw);
      if (!flushTimer.current) {
        flushTimer.current = setTimeout(() => {
          flushTimer.current = null;
          const chunk = pendingShell.current;
          pendingShell.current = "";
          if (chunk.length) appendLine("model", chunk);
        }, 80);
      }
    };
    ws.onclose = () => {
      setShellReady(false);
      if (wsRef.current === ws) wsRef.current = null;
      if (shouldStayConnected.current) {
        appendLine("sys", "— shell disconnected · reconnecting —");
        scheduleReconnect();
      } else {
        appendLine("sys", "— shell disconnected —");
      }
    };
    ws.onerror = () => {
      // onclose will fire right after — log here so the user sees both signals.
      appendLine("err", "shell websocket error");
    };
  }, [appendLine]);

  const scheduleReconnect = useCallback(() => {
    if (!shouldStayConnected.current) return;
    if (reconnectTimer.current) return;
    const idx = Math.min(reconnectAttempts.current, RECONNECT_BACKOFF_MS.length - 1);
    const delay = RECONNECT_BACKOFF_MS[idx];
    reconnectAttempts.current += 1;
    reconnectTimer.current = setTimeout(() => {
      reconnectTimer.current = null;
      if (shouldStayConnected.current) connectShell();
    }, delay);
  }, [connectShell]);

  useEffect(() => {
    if (mode !== "shell") {
      shouldStayConnected.current = false;
      if (reconnectTimer.current) { clearTimeout(reconnectTimer.current); reconnectTimer.current = null; }
      if (wsRef.current) { try { wsRef.current.close(); } catch {} wsRef.current = null; }
      setShellReady(false);
      return;
    }
    shouldStayConnected.current = true;
    reconnectAttempts.current = 0;
    connectShell();
    return () => {
      shouldStayConnected.current = false;
      if (reconnectTimer.current) { clearTimeout(reconnectTimer.current); reconnectTimer.current = null; }
      if (wsRef.current) { try { wsRef.current.close(); } catch {} wsRef.current = null; }
    };
  }, [mode, connectShell]);

  // When the user picks a different device, force a reconnect so the
  // websocket now points at the new quicClient.baseUrl.
  const handleDevicePick = useCallback(async (device: Device) => {
    setDevicePickerOpen(false);
    try {
      await selectDevice(device);
      appendLine("sys", `— switched to ${device.alias || device.name} —`);
      if (mode === "shell") {
        reconnectAttempts.current = 0;
        connectShell();
      }
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : "device switch failed");
    }
  }, [appendLine, connectShell, mode, selectDevice]);

  // ── Vibe-coding actions (Path C) ───────────────────────────────────────
  // These spawn an independent runYaverAgent round-trip with a baked-in
  // prompt. They do NOT share the main `busy` / `abortRef` state and they
  // do NOT close the shell websocket — so a tmux/claude/codex session
  // stays alive while a Hermes reload or wireless push fires in the
  // background. The agent loop will pick the right MCP tool itself
  // (wire_push, hotreload, wireless_push, mobile_project_status, etc.).
  const triggerVibe = useCallback(async (label: string, prompt: string) => {
    if (vibeBusy) return;
    setVibeBusy(label);
    appendLine("sys", `— ${label} —`);
    const controller = new AbortController();
    try {
      const ctx: YaverAgentToolContext = {
        devices: () => devices,
        primaryDeviceId: () => primaryDeviceId,
        secondaryDeviceId: () => null,
        selectDevice: async (deviceId) => {
          const d = devices.find((x) => x.id === deviceId);
          if (d) await selectDevice(d);
        },
      };
      const result = await runYaverAgent({
        prompt,
        ctx,
        // Tight cap so a stuck reload doesn't trap the screen forever; the
        // user can always retry by tapping the chip again.
        maxSteps: 3,
        signal: controller.signal,
        onProgress: (ev: YaverAgentProgressEvent) => {
          if (ev.kind === "tool_call") {
            const name = ev.call.name;
            const result = ev.call.error
              ? `❌ ${ev.call.error}`
              : safeStringify(ev.call.result);
            appendLine("tool", `⏺ ${name}`);
            if (result) appendLine("tool", `  ${result}`);
          } else if (ev.kind === "model_text") {
            if (ev.text.trim()) appendLine("model", ev.text);
          }
        },
      });
      appendLine("sys", `— ${label} done · ${result.steps} step(s) —`);
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : `${label} failed`);
    } finally {
      setVibeBusy(null);
    }
  }, [vibeBusy, appendLine, devices, primaryDeviceId, selectDevice]);

  // Generic direct-MCP chip runner — invokes a single tool, renders the
  // raw payload as a JSON line, falls back to the LLM-narrated path on
  // failure (tool missing on older agents, transport error, etc.).
  const triggerDirectMcp = useCallback(async (
    label: string,
    fallbackPrompt: string,
    call: () => Promise<{ ok: boolean; result?: unknown; error?: string }>,
  ) => {
    if (vibeBusy) return;
    setVibeBusy(label);
    appendLine("sys", `— ${label} (direct MCP) —`);
    try {
      const res = await call();
      if (!res.ok) {
        appendLine("err", `direct ${label} failed: ${res.error ?? "unknown"} — falling back to LLM`);
        setVibeBusy(null);
        void triggerVibe(label, fallbackPrompt);
        return;
      }
      const summary = safeStringify(res.result);
      if (summary) appendLine("tool", `⏺ ${summary}`);
      appendLine("sys", `— ${label} done —`);
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : `${label} failed`);
    } finally {
      setVibeBusy(null);
    }
  }, [vibeBusy, appendLine, triggerVibe]);

  // Fast path for ⟳ reload — direct MCP call, no LLM. ~500 ms vs 1.5-3 s
  // for the LLM-narrated path. Falls back to triggerVibe(prompt) when
  // the direct call fails (e.g. agent older than 1.99.234 without the
  // mobile_hermes_reload tool).
  const triggerReloadDirect = useCallback(async () => {
    if (vibeBusy) return;
    setVibeBusy("⟳ reload");
    const targetLabel = reloadTargetId
      ? devices.find((d) => d.id === reloadTargetId)?.alias
        ?? devices.find((d) => d.id === reloadTargetId)?.name
        ?? reloadTargetId.slice(0, 8)
      : null;
    appendLine("sys", targetLabel
      ? `— ⟳ reload (direct MCP) → @${targetLabel} —`
      : "— ⟳ reload (direct MCP) —");
    try {
      const res = await callMobileHermesReload(reloadTargetId ? { targetDeviceId: reloadTargetId } : {});
      if (!res.ok) {
        // Phase-8 chain: when the agent has no dev-server (managed-cloud
        // relay scenario), fall back to device_broadcast_command which
        // just pushes a plain "reload" BlackBox cmd to the target. The
        // mobile-side BlackBox listener at (tabs)/_layout.tsx handles
        // the rest via loadApp().
        const noDevServer = (res.error ?? "").toLowerCase().includes("dev server");
        if (noDevServer) {
          appendLine("sys", "— no dev-server on agent · falling back to device_broadcast_command —");
          const fb = await callDeviceBroadcastCommand({
            command: "reload",
            targetDeviceId: reloadTargetId ?? undefined,
          });
          if (fb.ok && fb.result?.ok) {
            appendLine(
              "tool",
              `⏺ device_broadcast_command → ${fb.result.mode}${fb.result.reachedSession === false ? " (no session for target)" : ""}`,
            );
            appendLine("sys", "— ⟳ reload done (broadcast) —");
            setVibeBusy(null);
            return;
          }
          appendLine("err", `broadcast fallback failed: ${fb.error ?? fb.result?.error ?? "unknown"} — falling back to LLM`);
        } else {
          appendLine("err", `direct reload failed: ${res.error ?? "unknown"} — falling back to LLM`);
        }
        setVibeBusy(null);
        void triggerVibe(
          "⟳ reload",
          "Trigger a Hermes/Metro fast-refresh on the mobile app currently running on this phone. Pick whichever wire/wireless/hot-reload MCP tool fits; do not rebuild — only reload the bundle. Be concise.",
        );
        return;
      }
      const r = res.result;
      if (r?.changeClass === "native_rebuild_required") {
        appendLine("err", "⚠ native rebuild required — JS reload accepted but native files changed");
        if (r.nativeChanges?.length) {
          for (const c of r.nativeChanges.slice(0, 5)) {
            appendLine("tool", `  · ${c.Path} — ${c.Reason}`);
          }
        }
      } else {
        appendLine("tool", `⏺ mobile_hermes_reload → ${r?.changeClass ?? "ok"}`);
      }
      appendLine("sys", "— ⟳ reload done —");
    } catch (e: unknown) {
      appendLine("err", e instanceof Error ? e.message : "reload failed");
    } finally {
      setVibeBusy(null);
    }
  }, [vibeBusy, appendLine, triggerVibe, reloadTargetId, devices]);

  // ── Submit handler ─────────────────────────────────────────────────────
  const submit = useCallback(async () => {
    const text = input.trim();
    if (!text || busy) return;
    setInput("");
    Keyboard.dismiss();

    if (mode === "shell") {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        appendLine("err", "shell not connected — waiting for reconnect");
        return;
      }
      appendLine("user", `$ ${text}`);
      ws.send(new TextEncoder().encode(text + "\n"));
      return;
    }

    appendLine("user", `▶ ${text}`);
    setBusy(true);
    abortRef.current = new AbortController();

    try {
      const ctx: YaverAgentToolContext = {
        devices: () => devices,
        primaryDeviceId: () => primaryDeviceId,
        secondaryDeviceId: () => null,
        selectDevice: async (deviceId) => {
          const d = devices.find((x) => x.id === deviceId);
          if (d) await selectDevice(d);
        },
      };
      const result = await runYaverAgent({
        prompt: text,
        history: historyRef.current,
        ctx,
        signal: abortRef.current.signal,
        onProgress: (ev: YaverAgentProgressEvent) => {
          if (ev.kind === "model_text") {
            if (ev.text.trim()) {
              appendLine("model", ev.text);
              speakIfEnabled(ev.text);
            }
          } else if (ev.kind === "tool_call") {
            const name = ev.call.name;
            const args = safeStringify(ev.call.args);
            const result = ev.call.error
              ? `❌ ${ev.call.error}`
              : safeStringify(ev.call.result);
            appendLine("tool", `⏺ ${name}(${args})`);
            if (result) appendLine("tool", `  ${result}`);
          }
        },
      });

      historyRef.current = [
        ...historyRef.current,
        { role: "user", text },
        { role: "assistant", text: result.finalText },
      ];
      if (result.finalText && !lastLineMatches(lines, result.finalText)) {
        appendLine("model", result.finalText);
        speakIfEnabled(result.finalText);
      }
      appendLine("sys", `— done · ${result.steps} step(s)${result.outputTokens ? ` · ${result.outputTokens} out tokens` : ""} —`);
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : "agent run failed";
      appendLine("err", msg);
    } finally {
      abortRef.current = null;
      setBusy(false);
    }
  }, [input, busy, mode, appendLine, lines, devices, primaryDeviceId, selectDevice, speakIfEnabled]);

  // Keep submitRef pointing at the latest submit so stopRecording can
  // trigger it without taking a stale closure capture.
  useEffect(() => { submitRef.current = submit; }, [submit]);
  useEffect(() => { triggerReloadDirectRef.current = triggerReloadDirect; }, [triggerReloadDirect]);

  const cancel = useCallback(() => { abortRef.current?.abort(); }, []);

  // ── Render ─────────────────────────────────────────────────────────────
  const currentDevice = devices.find((d) => d.id === primaryDeviceId);
  const deviceLabel = currentDevice ? (currentDevice.alias || currentDevice.name) : "no device";

  return (
    <View style={[styles.root, { backgroundColor: PAL.bg, paddingTop: insets.top }]}>
      <View style={styles.header}>
        <Pressable hitSlop={12} onPress={() => router.back()}>
          <Text style={[styles.headerBtn, { color: PAL.muted }]}>‹</Text>
        </Pressable>
        <Pressable
          hitSlop={8}
          onLongPress={() => mode === "shell" && setDevicePickerOpen(true)}
          delayLongPress={350}
          style={{ flex: 1, alignItems: "center" }}
        >
          <Text style={[styles.headerTitle, { color: PAL.fg }]} numberOfLines={1}>
            {mode === "agent" ? "agent" : `shell · ${deviceLabel}`}
          </Text>
          {mode === "shell" ? (
            <Text style={{
              color: shellReady ? PAL.tool : (reconnectTimer.current ? PAL.accent : PAL.muted),
              fontFamily: "Menlo",
              fontSize: 10,
              marginTop: 2,
            }}>
              {shellReady ? "● live" : (reconnectTimer.current ? "○ reconnecting" : `○ ${connectionStatus}`)}
            </Text>
          ) : null}
        </Pressable>
        <Pressable
          hitSlop={8}
          onPress={() => router.replace("/glass-workspace")}
          style={[styles.modeChip, {
            backgroundColor: PAL.chip,
            borderColor: PAL.border,
            marginRight: 6,
          }]}
        >
          <Text style={[styles.modeChipText, { color: PAL.muted }]}>
            ▦
          </Text>
        </Pressable>
        <Pressable
          hitSlop={8}
          onPress={() => setAutoTts((v) => !v)}
          style={[styles.modeChip, {
            backgroundColor: autoTts ? "#1e1b4b" : PAL.chip,
            borderColor: autoTts ? PAL.accent : PAL.border,
            marginRight: 6,
          }]}
        >
          <Text style={[styles.modeChipText, { color: autoTts ? PAL.accent : PAL.muted }]}>
            🔊
          </Text>
        </Pressable>
        <Pressable
          hitSlop={12}
          onPress={() => setMode((m) => (m === "agent" ? "shell" : "agent"))}
          style={[styles.modeChip, { backgroundColor: PAL.chip, borderColor: PAL.border }]}
        >
          <Text style={[styles.modeChipText, { color: PAL.accent }]}>
            {mode === "agent" ? "→ shell" : "→ agent"}
          </Text>
        </Pressable>
      </View>

      <KeyboardAvoidingView
        style={{ flex: 1 }}
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <ScrollView
          ref={scrollRef}
          style={styles.body}
          contentContainerStyle={{ padding: 12, paddingBottom: 24 }}
        >
          {lines.map((line, i) => (
            <Text
              key={i}
              selectable
              style={{
                color: colorFor(line.kind),
                fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
                fontSize: FONT_SIZE,
                lineHeight: LINE_HEIGHT,
                marginBottom: 2,
              }}
            >
              {line.text}
            </Text>
          ))}
          {busy ? (
            <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: FONT_SIZE, marginTop: 4 }}>
              …
            </Text>
          ) : null}
        </ScrollView>

        {/* Vibe-coding action bar (visible in both modes — see Path C in
         *  BEAM_PRO_DEV.md). Each chip dispatches an independent on-phone
         *  agent run so the shell websocket / tmux stays untouched. */}
        <ScrollView
          horizontal
          showsHorizontalScrollIndicator={false}
          style={[styles.macroRow, { borderTopColor: PAL.border }]}
          contentContainerStyle={{ paddingHorizontal: 8 }}
        >
          {/* Reload-target picker chip (Phase 7 cross-device). Tap to
           *  pick which device the ⟳ chip reloads. Long-press to clear
           *  the target (revert to broadcast). */}
          <Pressable
            onPress={() => setReloadTargetPickerOpen(true)}
            onLongPress={() => setReloadTarget(null)}
            delayLongPress={400}
            style={[styles.macroKey, {
              borderColor: reloadTargetId ? PAL.accent : PAL.border,
              backgroundColor: reloadTargetId ? "#1e1b4b" : PAL.chip,
              maxWidth: 180,
            }]}
          >
            <Text
              numberOfLines={1}
              style={{
                color: reloadTargetId ? PAL.accent : PAL.muted,
                fontFamily: "Menlo",
                fontSize: 11,
              }}
            >
              {reloadTargetId
                ? `🎯 @${(devices.find((d) => d.id === reloadTargetId)?.alias
                  ?? devices.find((d) => d.id === reloadTargetId)?.name
                  ?? reloadTargetId.slice(0, 8))}`
                : "🎯 target"}
            </Text>
          </Pressable>
          {vibeChipsFor(projectKind).map(({ label, prompt }) => {
            const active = vibeBusy === label;
            // Direct-MCP chips (no LLM, fast). Anything not in the map
            // routes through the LLM-narrated loop as a generic prompt.
            let onPress: () => void;
            if (label === "⟳ reload") {
              onPress = () => void triggerReloadDirect();
            } else if (label === "📊 status") {
              onPress = () => void triggerDirectMcp(label, prompt, () => callMobileProjectStatus());
            } else if (label === "🩺 doctor") {
              onPress = () => void triggerDirectMcp(label, prompt, () => callMobileHermesDoctor());
            } else {
              onPress = () => void triggerVibe(label, prompt);
            }
            return (
              <Pressable
                key={label}
                disabled={!!vibeBusy}
                onPress={onPress}
                style={[
                  styles.macroKey,
                  {
                    borderColor: active ? PAL.accent : PAL.border,
                    backgroundColor: active ? "#1e1b4b" : PAL.chip,
                    opacity: vibeBusy && !active ? 0.45 : 1,
                  },
                ]}
              >
                <Text style={{
                  color: active ? PAL.accent : PAL.chipText,
                  fontFamily: "Menlo",
                  fontSize: 11,
                }}>{active ? `${label}…` : label}</Text>
              </Pressable>
            );
          })}
        </ScrollView>

        {/* Saved-prompt drawer (agent mode only) */}
        {mode === "agent" && (savedPrompts.length > 0 || input.trim().length > 0) ? (
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            style={[styles.macroRow, { borderTopColor: PAL.border }]}
            contentContainerStyle={{ paddingHorizontal: 8 }}
          >
            {input.trim().length > 0 ? (
              <Pressable
                onPress={saveCurrentPrompt}
                style={[styles.macroKey, { borderColor: PAL.border, backgroundColor: PAL.chip }]}
              >
                <Text style={{ color: PAL.star, fontFamily: "Menlo", fontSize: 11 }}>⊕ save</Text>
              </Pressable>
            ) : null}
            {historyRef.current.length > 0 ? (
              <Pressable
                onPress={clearHistory}
                style={[styles.macroKey, { borderColor: PAL.border, backgroundColor: PAL.chip }]}
              >
                <Text style={{ color: PAL.err, fontFamily: "Menlo", fontSize: 11 }}>⌫ reset</Text>
              </Pressable>
            ) : null}
            {savedPrompts.map((p) => (
              <Pressable
                key={p}
                onPress={() => { setInput(p); inputRef.current?.focus(); }}
                onLongPress={() => {
                  Alert.alert("Delete prompt?", p, [
                    { text: "Cancel", style: "cancel" },
                    { text: "Delete", style: "destructive", onPress: () => deleteSavedPrompt(p) },
                  ]);
                }}
                delayLongPress={400}
                style={[styles.macroKey, { borderColor: PAL.border, backgroundColor: PAL.chip, maxWidth: 200 }]}
              >
                <Text
                  numberOfLines={1}
                  style={{ color: PAL.chipText, fontFamily: "Menlo", fontSize: 11 }}
                >
                  ☆ {p.length > 24 ? p.slice(0, 24) + "…" : p}
                </Text>
              </Pressable>
            ))}
          </ScrollView>
        ) : null}

        {/* Shell macro keys */}
        {mode === "shell" ? (
          <ScrollView
            horizontal
            showsHorizontalScrollIndicator={false}
            style={[styles.macroRow, { borderTopColor: PAL.border }]}
            contentContainerStyle={{ paddingHorizontal: 8 }}
          >
            {[
              ["Esc", "\x1b"],
              ["Tab", "\t"],
              ["^C", "\x03"],
              ["^D", "\x04"],
              ["↑", "\x1b[A"],
              ["↓", "\x1b[B"],
              ["←", "\x1b[D"],
              ["→", "\x1b[C"],
              ["tmux ^B", "\x02"],
              ["clear", "\x0cclear\n"],
            ].map(([label, seq]) => (
              <Pressable
                key={label}
                onPress={() => wsRef.current?.send(new TextEncoder().encode(seq))}
                style={[styles.macroKey, { borderColor: PAL.border, backgroundColor: PAL.chip }]}
              >
                <Text style={{ color: PAL.chipText, fontFamily: "Menlo", fontSize: 11 }}>{label}</Text>
              </Pressable>
            ))}
          </ScrollView>
        ) : null}

        <View style={[styles.inputRow, { borderTopColor: PAL.border, paddingBottom: insets.bottom || 8 }]}>
          <Text style={{ color: PAL.prompt, fontFamily: "Menlo", fontSize: FONT_SIZE + 1, marginRight: 6 }}>
            {mode === "agent" ? "▶" : "$"}
          </Text>
          <TextInput
            ref={inputRef}
            value={input}
            onChangeText={setInput}
            onSubmitEditing={() => void submit()}
            placeholder={busy ? "running…" : mode === "agent" ? "ask the agent…" : "type a command…"}
            placeholderTextColor={PAL.muted}
            autoCorrect={false}
            autoCapitalize="none"
            autoComplete="off"
            spellCheck={false}
            multiline={mode === "agent"}
            style={{
              flex: 1,
              color: PAL.fg,
              fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }),
              fontSize: FONT_SIZE,
              padding: 6,
              maxHeight: 120,
            }}
            editable={!busy || mode === "shell"}
            returnKeyType="send"
            blurOnSubmit={false}
          />
          <Pressable
            onPress={() => recording ? void stopRecording(true) : void startRecording()}
            hitSlop={10}
            disabled={busy && !recording}
            style={{
              paddingHorizontal: 10,
              paddingVertical: 4,
              borderRadius: 999,
              backgroundColor: recording ? "#7f1d1d" : "transparent",
              opacity: busy && !recording ? 0.4 : 1,
            }}
          >
            <Text style={{
              fontSize: 18,
              color: recording ? "#fecaca" : PAL.muted,
            }}>🎤</Text>
          </Pressable>
          {busy ? (
            <Pressable onPress={cancel} hitSlop={12}>
              <Text style={[styles.cancelBtn, { color: PAL.err }]}>stop</Text>
            </Pressable>
          ) : null}
        </View>
      </KeyboardAvoidingView>

      {/* Device picker modal — only meaningful in shell mode but reachable
       *  any time via long-press on the title. */}
      <Modal
        visible={devicePickerOpen}
        transparent
        animationType="fade"
        onRequestClose={() => setDevicePickerOpen(false)}
      >
        <Pressable
          style={styles.modalBackdrop}
          onPress={() => setDevicePickerOpen(false)}
        >
          <Pressable
            style={[styles.modalCard, { backgroundColor: "#0a0a0a", borderColor: PAL.border }]}
            onPress={(e) => e.stopPropagation()}
          >
            <Text style={{ color: PAL.fg, fontFamily: "Menlo", fontSize: 13, fontWeight: "600", marginBottom: 12 }}>
              switch device
            </Text>
            {devices.length === 0 ? (
              <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 12 }}>
                no devices visible — pair one from the Devices tab
              </Text>
            ) : (
              devices.map((d) => {
                const isCurrent = d.id === primaryDeviceId;
                return (
                  <Pressable
                    key={d.id}
                    onPress={() => void handleDevicePick(d)}
                    style={{
                      paddingVertical: 10,
                      paddingHorizontal: 12,
                      borderRadius: 6,
                      backgroundColor: isCurrent ? PAL.chip : "transparent",
                      marginBottom: 4,
                      flexDirection: "row",
                      alignItems: "center",
                    }}
                  >
                    <Text style={{
                      color: d.online ? PAL.tool : PAL.muted,
                      fontFamily: "Menlo",
                      fontSize: 14,
                      marginRight: 8,
                    }}>{d.online ? "●" : "○"}</Text>
                    <Text style={{
                      color: isCurrent ? PAL.accent : PAL.fg,
                      fontFamily: "Menlo",
                      fontSize: 13,
                      flex: 1,
                    }} numberOfLines={1}>
                      {d.alias ? `@${d.alias}` : d.name}
                    </Text>
                    {isCurrent ? (
                      <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: 11 }}>current</Text>
                    ) : null}
                  </Pressable>
                );
              })
            )}
            {/* Finish setup for an already-active managed-cloud dev box. */}
            <Pressable
              onPress={() => {
                setDevicePickerOpen(false);
                router.push("/cloud-onboarding");
              }}
              style={{
                marginTop: 10,
                paddingVertical: 10,
                paddingHorizontal: 12,
                borderRadius: 6,
                borderWidth: 1,
                borderColor: PAL.accent,
                flexDirection: "row",
                alignItems: "center",
              }}
            >
              <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: 13, marginRight: 8 }}>＋</Text>
              <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: 13, flex: 1 }}>
                set up cloud box
              </Text>
            </Pressable>
          </Pressable>
        </Pressable>
      </Modal>

      {/* Reload-target picker (Phase 7). Pick which device the ⟳ chip
       *  reloads. Choosing the same device as the primary = same as no
       *  target (broadcast). Other devices = scoped BlackBox send. */}
      <Modal
        visible={reloadTargetPickerOpen}
        transparent
        animationType="fade"
        onRequestClose={() => setReloadTargetPickerOpen(false)}
      >
        <Pressable
          style={styles.modalBackdrop}
          onPress={() => setReloadTargetPickerOpen(false)}
        >
          <Pressable
            style={[styles.modalCard, { backgroundColor: "#0a0a0a", borderColor: PAL.border }]}
            onPress={(e) => e.stopPropagation()}
          >
            <Text style={{ color: PAL.fg, fontFamily: "Menlo", fontSize: 13, fontWeight: "600", marginBottom: 4 }}>
              reload target
            </Text>
            <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 10, marginBottom: 12 }}>
              ⟳ chip will scope the reload to this device only
            </Text>
            <Pressable
              onPress={() => { setReloadTarget(null); setReloadTargetPickerOpen(false); }}
              style={{
                paddingVertical: 10,
                paddingHorizontal: 12,
                borderRadius: 6,
                backgroundColor: !reloadTargetId ? PAL.chip : "transparent",
                marginBottom: 8,
                flexDirection: "row",
                alignItems: "center",
              }}
            >
              <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 14, marginRight: 8 }}>∗</Text>
              <Text style={{
                color: !reloadTargetId ? PAL.accent : PAL.fg,
                fontFamily: "Menlo",
                fontSize: 13,
                flex: 1,
              }}>
                broadcast (all SDK devices)
              </Text>
              {!reloadTargetId ? (
                <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: 11 }}>current</Text>
              ) : null}
            </Pressable>
            {devices.length === 0 ? (
              <Text style={{ color: PAL.muted, fontFamily: "Menlo", fontSize: 12 }}>
                no devices visible — pair one from the Devices tab
              </Text>
            ) : (
              devices.map((d) => {
                const isSelected = d.id === reloadTargetId;
                return (
                  <Pressable
                    key={d.id}
                    onPress={() => { setReloadTarget(d.id); setReloadTargetPickerOpen(false); }}
                    style={{
                      paddingVertical: 10,
                      paddingHorizontal: 12,
                      borderRadius: 6,
                      backgroundColor: isSelected ? PAL.chip : "transparent",
                      marginBottom: 4,
                      flexDirection: "row",
                      alignItems: "center",
                    }}
                  >
                    <Text style={{
                      color: d.online ? PAL.tool : PAL.muted,
                      fontFamily: "Menlo",
                      fontSize: 14,
                      marginRight: 8,
                    }}>{d.online ? "●" : "○"}</Text>
                    <Text style={{
                      color: isSelected ? PAL.accent : PAL.fg,
                      fontFamily: "Menlo",
                      fontSize: 13,
                      flex: 1,
                    }} numberOfLines={1}>
                      {d.alias ? `@${d.alias}` : d.name}
                    </Text>
                    {isSelected ? (
                      <Text style={{ color: PAL.accent, fontFamily: "Menlo", fontSize: 11 }}>current</Text>
                    ) : null}
                  </Pressable>
                );
              })
            )}
          </Pressable>
        </Pressable>
      </Modal>
    </View>
  );
}

// ── Helpers ──────────────────────────────────────────────────────────────

function colorFor(kind: Line["kind"]): string {
  switch (kind) {
    case "user": return PAL.user;
    case "model": return PAL.model;
    case "tool": return PAL.tool;
    case "sys": return PAL.sys;
    case "err": return PAL.err;
  }
}

function buildTerminalWsUrl(): string {
  const base = quicClient.baseUrl.replace(/^http/, "ws");
  const h = quicClient.getAuthHeaders();
  const token = encodeURIComponent((h.Authorization || "").replace("Bearer ", ""));
  return `${base}/ws/terminal?token=${token}`;
}

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[()][0AB]|\x1b[=>NOM78cDEHM]|\x07/g;
function stripAnsi(s: string): string {
  return s.replace(ANSI, "").replace(/\r(?!\n)/g, "");
}

function safeStringify(v: unknown): string {
  if (v == null) return "";
  if (typeof v === "string") return v.length > 200 ? v.slice(0, 200) + "…" : v;
  try {
    const s = JSON.stringify(v);
    return s.length > 200 ? s.slice(0, 200) + "…" : s;
  } catch {
    return String(v);
  }
}

// ── Vibe-bar chip sets per project kind ───────────────────────────────────
//
// The hardcoded mobile chips inline in the render preserve the
// existing Hermes-reload / wire-push / project-status / hermes-doctor
// flow byte-for-byte. The data below is kept as a reference set so a
// future refactor can route project-kind-aware swaps through here
// (the parallel `/glass-workspace` route already covers web + backend
// project kinds via its own panesForKind). Per memory
// [feedback_mobile_only_wire_push] the mobile flow is load-bearing
// for current users and is never demoted by project detection.
type VibeChip = { label: string; prompt: string };

const MOBILE_VIBE_CHIPS: VibeChip[] = [
  {
    label: "⟳ reload",
    prompt:
      "Trigger a Hermes/Metro fast-refresh on the mobile app currently running on this phone. Pick whichever wire/wireless/hot-reload MCP tool fits; do not rebuild — only reload the bundle. Be concise.",
  },
  {
    label: "📦 push",
    prompt:
      "Push the latest code from the connected remote dev box to the mobile app under test on this phone using the appropriate wire/wireless push MCP tool. Use the currently-selected device as source. Be concise.",
  },
  {
    label: "📊 status",
    prompt:
      "Run mobile_project_status (and expo_status if relevant) for the currently-selected device. Summarise the bundler state, Hermes status, and whether the dev client is reachable. One short paragraph.",
  },
  {
    label: "🩺 doctor",
    prompt:
      "Run mobile_hermes_doctor for the currently-selected device. Summarise findings in one paragraph and list any urgent action items as bullets.",
  },
];

const WEB_VIBE_CHIPS: VibeChip[] = [
  {
    label: "⟳ reload",
    prompt:
      "Trigger a hot-reload of the running web dev server (Next.js / Vite / Nuxt / SvelteKit) for this project. Use the `web_preview` ops verb with action=reload, or call web_preview_reload, whichever is wired. Be concise.",
  },
  {
    label: "📺 preview",
    prompt:
      "Start (or surface the URL of) the web dev server for this project so the user can open it in a browser tile. Use the `web_preview` ops verb with action=start, defaulting to the workspace's primary web app. Return the iframe URL on success.",
  },
  {
    label: "🚀 deploy",
    prompt:
      "Run `ops deploy` for this project with the user's previously-configured target (Vercel / Netlify / Cloudflare / Fly / Railway). If no target is configured, ask which one to use. Stream output in one short paragraph.",
  },
  {
    label: "🧪 test",
    prompt:
      "Run the testkit suite under ./yaver-tests for this project (testkit_run). If any tests failed, follow up with testkit_last_failure and summarise the first failing step. One paragraph + bullets.",
  },
  {
    label: "🩺 doctor",
    prompt:
      "Run a fast health check: tsc_check (type errors) and eslint_check (lint errors) and testkit_last_failure (most recent failed test) for this project. Summarise the three signals in one paragraph + action bullets.",
  },
];

const BACKEND_VIBE_CHIPS: VibeChip[] = [
  {
    label: "🚀 deploy",
    prompt:
      "Run `ops deploy` for this backend project with the previously-configured target (cloud / vercel / fly / railway / convex / cloudflare). If no target is set, ask. One short paragraph of output.",
  },
  {
    label: "📋 logs",
    prompt:
      "Tail the most recent application logs for this backend's deployed target — pick the right cloud_logs / vercel_logs / fly_logs / convex_logs MCP tool. Show the last 50 lines, then summarise anomalies in one paragraph.",
  },
  {
    label: "🗄 db",
    prompt:
      "Show the schema of the project's primary database (Postgres / SQLite / Convex). Use db_schema (or convex_schema / supabase_db) and summarise tables + columns in one paragraph.",
  },
  {
    label: "🧪 test",
    prompt:
      "Run the project's tests via the best-fit MCP tool (go_test_suite / pytest_suite / cargo_test_suite / testkit_run). Summarise pass/fail counts and the first failing test if any. One paragraph + bullets.",
  },
  {
    label: "🩺 doctor",
    prompt:
      "Run language-appropriate static checks (go_vet_check + go_vulncheck for Go; ruff + mypy_check for Python; cargo_clippy for Rust; eslint_check + tsc_check for Node) plus testkit_last_failure. Summarise.",
  },
];

const GENERIC_VIBE_CHIPS: VibeChip[] = [
  {
    label: "📁 status",
    prompt:
      "Tell me what kind of project is in the current working directory: list the marker files you see (package.json, go.mod, Cargo.toml, pubspec.yaml, etc.), the git branch, and the most recent commit. One paragraph.",
  },
  {
    label: "🚀 deploy",
    prompt:
      "Run `ops deploy` for this project. Ask the user for a target if none is configured. Stream output.",
  },
  {
    label: "🧪 test",
    prompt:
      "Look for a test command in this project (testkit_run if yaver-tests/ exists; otherwise infer from package.json scripts / go test / cargo test / pytest) and run it. Summarise.",
  },
];

function vibeChipsFor(pk: ProjectKindResult | null): VibeChip[] {
  // null / still-loading → assume mobile so we never strip the
  // load-bearing chips out from under a real mobile dev who pops the
  // screen open quickly. Per memory [feedback_mobile_only_wire_push]
  // the mobile flow is preserved byte-for-byte.
  if (!pk || pk.kind === "mobile") return MOBILE_VIBE_CHIPS;
  switch (pk.kind) {
    case "web":     return WEB_VIBE_CHIPS;
    case "backend": return BACKEND_VIBE_CHIPS;
    default:        return GENERIC_VIBE_CHIPS;
  }
}

// Voice keyword shortcut — single-word "reload" (en/tr/es/de/fr) jumps
// straight to the direct-MCP reload path. Tight allowlist so we don't
// hijack real prompts. Punctuation stripped before compare.
const RELOAD_KEYWORDS = new Set([
  "reload", "refresh", "rerun", "rebuild",
  "yenile", "tekrar yükle", "yeniden", "yenileme",
  "recargar", "actualizar",
  "neuladen", "aktualisieren",
  "recharger", "rafraîchir", "rafraichir",
]);

function isReloadKeyword(raw: string): boolean {
  const cleaned = raw.toLowerCase().replace(/[.!?,;:]/g, "").trim();
  if (!cleaned) return false;
  return RELOAD_KEYWORDS.has(cleaned);
}

function lastLineMatches(lines: Line[], text: string): boolean {
  for (let i = lines.length - 1; i >= 0; i--) {
    if (lines[i].kind === "model") return lines[i].text.includes(text.slice(0, 80));
  }
  return false;
}

// ── Styles ───────────────────────────────────────────────────────────────

const styles = StyleSheet.create({
  root: { flex: 1 },
  header: {
    flexDirection: "row",
    alignItems: "center",
    paddingHorizontal: 14,
    paddingVertical: 8,
    borderBottomWidth: StyleSheet.hairlineWidth,
    borderBottomColor: PAL.border,
  },
  headerBtn: { fontSize: 26, fontWeight: "300", paddingHorizontal: 4 },
  headerTitle: { fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }), fontSize: 13, fontWeight: "600" },
  modeChip: {
    paddingHorizontal: 10,
    paddingVertical: 4,
    borderRadius: 999,
    borderWidth: 1,
  },
  modeChipText: { fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }), fontSize: 11, fontWeight: "600" },
  body: { flex: 1 },
  macroRow: { borderTopWidth: 1, paddingVertical: 6, backgroundColor: "#0a0a0a" },
  macroKey: {
    paddingHorizontal: 10,
    paddingVertical: 6,
    marginHorizontal: 4,
    borderRadius: 6,
    borderWidth: 1,
    minWidth: 38,
    alignItems: "center",
    justifyContent: "center",
  },
  inputRow: {
    flexDirection: "row",
    alignItems: "center",
    padding: 10,
    borderTopWidth: 1,
    backgroundColor: "#0a0a0a",
  },
  cancelBtn: { fontFamily: Platform.select({ ios: "Menlo", android: "monospace" }), fontSize: 12, paddingHorizontal: 10 },
  modalBackdrop: {
    flex: 1,
    backgroundColor: "rgba(0,0,0,0.7)",
    justifyContent: "center",
    alignItems: "center",
    padding: 20,
  },
  modalCard: {
    width: "100%",
    maxWidth: 360,
    padding: 16,
    borderRadius: 10,
    borderWidth: 1,
  },
});
