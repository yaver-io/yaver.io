// Car Voice Coding — Tier 0 "code from the car by voice".
//
// Speak a command → it's transcribed on-device (or via your STT key) →
// dispatched as a coding task to the box you pick → polled to done → a ONE
// sentence status is read back over the car's Bluetooth audio. We never read
// code/diffs aloud while driving (carVoiceCoding.ts::isReadCodeRequest) and we
// HARD-GATE risky commands (deploy / push / delete / force) behind an explicit
// on-screen + spoken confirm (carVoiceConfirm.ts) before anything dispatches.
//
// Tier 0 needs NO car SDK and NO entitlement: audio plays over whatever route
// is active — paired car speakers when you're in the car. The loop lib is
// makeRealCarVoiceDeps + runCarVoiceTurn; this screen owns recording state, the
// box picker, the turn history, and the confirmation modal.
//
// Big touch targets + high contrast for a glance-and-go context, but fully
// functional on the phone first. Hands-free entry (carVoiceEntry.ts) lets a
// native quick-action / Siri shortcut / deep-link autostart a turn with no
// navigation; until that native trigger ships, the big PTT button is the
// fallback.

import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  Alert,
  Linking,
  Modal,
  NativeModules,
  Platform,
  Pressable,
  ScrollView,
  Text,
  View,
} from "react-native";
import { useLocalSearchParams, useRouter } from "expo-router";
import { AppScreenHeader } from "../src/components/AppScreenHeader";
import { useColors } from "../src/context/ThemeContext";
import { useDevice } from "../src/context/DeviceContext";
import { useAuth } from "../src/context/AuthContext";
import { isDeviceAsleep, useMachineLifecycle } from "../src/lib/wakeMachine";
import WakeProgress from "../src/components/WakeProgress";
import { connectionManager } from "../src/lib/connectionManager";
import { quicClient } from "../src/lib/quic";
import { loadLocalSpeechConfig } from "../src/lib/auth";
import { speakText } from "../src/lib/speech";
import {
  makeRealCarVoiceDeps,
  runCarVoiceTurn,
  type CarVoiceConfig,
  type CarVoiceStage,
  type CarVoiceTaskRef,
} from "../src/lib/carVoiceCoding";
import { assessRisk, interpretConfirmReply } from "../src/lib/carVoiceConfirm";
import { carVoiceEntryBus, shouldAutostart } from "../src/lib/carVoiceEntry";
import { executeCarSurfaceIntent } from "../src/lib/carSurfaceIntent";
import {
  classifyMachineSwitch,
  matchMachine,
  spokenForMachineSwitch,
} from "../src/lib/carMachineSwitch";
import {
  CarReplyGate,
  SessionChoiceGate,
  handleCarReply,
} from "../src/lib/carReplyDispatch";
import {
  presentCarConversation,
  subscribeCarReplies,
} from "../src/lib/carMessagingNotification";
import {
  startLiveActivity,
  updateLiveActivity,
  endLiveActivity,
  type LiveActivityState,
} from "../src/lib/liveActivity";
import { runtimeSurfaceClient } from "../src/lib/runtimeSurfaceClient";
import { yaverNativeSurfaceSummary } from "../src/lib/yaverNativeCatalog";
import { useHandsFreeVoice } from "../src/lib/voice/useHandsFreeVoice";
import type { CreateVoiceCoreOptions } from "../src/lib/voice/createVoiceCore";
import type { VoiceCoreEvent } from "../src/lib/voice/types";

// ── turn history model (UI only) ────────────────────────────────────
interface Turn {
  id: string;
  transcript: string;
  spoken: string;
  stage: CarVoiceStage | "queued" | "confirming";
  status?: string;
  declined?: boolean;
  at: number;
}

const STAGE_LABEL: Record<string, string> = {
  queued: "Queued",
  listening: "Listening…",
  transcribed: "Heard you",
  confirming: "Confirm to run",
  dispatched: "Sent to box",
  working: "Working…",
  spoken: "Done",
  declined: "Declined",
  error: "Error",
};

export default function CarVoiceCodingScreen() {
  const c = useColors();
  const router = useRouter();
  const params = useLocalSearchParams<{
    surface?: string;
    autostart?: string;
  }>();
  const glass = params.surface === "glass";
  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices as any[]) || [];
  const { token } = useAuth();

  const [deviceId, setDeviceId] = useState("");
  const [status, setStatus] = useState<
    "idle" | "recording" | "thinking" | "speaking" | "error"
  >("idle");
  const [errorMsg, setErrorMsg] = useState("");
  const [turns, setTurns] = useState<Turn[]>([]);

  // Confirmation gate state for a risky command awaiting an explicit OK.
  const [confirm, setConfirm] = useState<{
    transcript: string;
    prompt: string;
  } | null>(null);

  const recordingRef = useRef<any>(null); // expo-av Audio.Recording
  const liveRef = useRef(true);
  // Whether a Live Activity card is currently on the dashboard/lock screen.
  const activityRef = useRef(false);
  const carReplyGateRef = useRef(new CarReplyGate());
  const sessionChoiceGateRef = useRef(new SessionChoiceGate());
  useEffect(
    () => () => {
      liveRef.current = false;
    },
    [],
  );

  const pickedDevice = devices.find((d) => (d.id || d.deviceId) === deviceId);
  // Wake a self-parked box straight from the car picker — a spoken task to a
  // sleeping box would otherwise just fail. Big, glanceable progress only.
  const carLifecycle = useMachineLifecycle({
    token,
    device: pickedDevice as any,
    deviceReachable: !!pickedDevice?.online,
    onTick: (deviceCtx as any).refreshDevices,
  });
  const carRunning = carLifecycle.direction !== null || carLifecycle.phase === "error";
  const pickedAsleep = isDeviceAsleep(pickedDevice as any);
  const carConversationId = deviceId ? `car:${deviceId}` : "car:yaver";
  const carContactName = `Yaver · ${pickedDevice?.name || pickedDevice?.alias || deviceId || "runtime"}`;

  // ── deps factory: dispatch + getTask go through THE picked box ────
  const buildDeps = useCallback(async () => {
    const cfg = await loadLocalSpeechConfig();
    const config: CarVoiceConfig = {
      stt: {
        provider: cfg.sttProvider || "on-device",
        apiKey: cfg.apiKey,
        model: cfg.sttModel,
      },
      tts: {
        provider: cfg.ttsProvider || "device",
        apiKey: cfg.apiKey,
        voice: cfg.ttsVoice,
      },
      speakAcknowledgement: true,
    };
    const client = connectionManager.clientFor(deviceId);
    const deps = makeRealCarVoiceDeps({
      config,
      // codeMode=true → terminal-style ("yaver code") prompt wrapping.
      dispatchTask: async (title, prompt) => {
        const t = await client.sendTask(
          title,
          prompt,
          undefined,
          undefined,
          undefined,
          undefined,
          undefined,
          undefined,
          undefined,
          undefined,
          true,
          undefined,
          "vibe",
        );
        return { id: t.id };
      },
      getTask: async (taskId): Promise<CarVoiceTaskRef> => {
        const t = await client.getTask(taskId);
        return {
          id: t.id,
          status: t.status,
          resultText: t.resultText,
          output: t.output,
        };
      },
    });
    return { deps, config };
  }, [deviceId]);

  const callCarOps = useCallback(
    async (verb: string, payload: Record<string, unknown>) => {
      if (verb === "meeting_next")
        return runtimeSurfaceClient.meetingNext(deviceId, payload as any);
      if (verb === "meeting_join_next")
        return runtimeSurfaceClient.meetingJoinNext(deviceId, payload as any);
      if (verb === "meeting_open_url")
        return runtimeSurfaceClient.meetingOpenUrl(deviceId, payload as any);
      if (verb === "mail_search")
        return runtimeSurfaceClient.mailSearch(deviceId, payload as any);
      if (verb === "mail_unread")
        return runtimeSurfaceClient.mailUnread(deviceId, payload as any);
      if (verb === "mail_send")
        return runtimeSurfaceClient.mailSend(deviceId, payload as any);
      if (verb === "git_prs")
        return runtimeSurfaceClient.gitPRs(deviceId, payload as any);
      if (verb === "git_issues")
        return runtimeSurfaceClient.gitIssues(deviceId, payload as any);
      if (verb === "git_ci_status")
        return runtimeSurfaceClient.gitCIStatus(deviceId, payload as any);
      if (verb === "git_connect")
        return runtimeSurfaceClient.gitConnect(deviceId, payload as any);
      if (verb === "media_open")
        return runtimeSurfaceClient.mediaOpen(deviceId, payload as any);
      if (verb === "maps_open")
        return runtimeSurfaceClient.mapsOpen(deviceId, payload as any);
      throw new Error(`unsupported car ops verb ${verb}`);
    },
    [deviceId],
  );

  // ── shared hands-free voice engine (the Claude-app-style loop) ────
  // The one VoiceConversationCore, reused by every surface. Everything mutable
  // it needs is read through refs so the core's own machine-switch interceptor
  // can retarget the box without rebuilding the loop.
  const devicesRef = useRef(devices);
  devicesRef.current = devices;
  const deviceIdRef = useRef(deviceId);
  deviceIdRef.current = deviceId;
  const speechCfgRef = useRef<Awaited<ReturnType<typeof loadLocalSpeechConfig>>>({});
  useEffect(() => {
    void loadLocalSpeechConfig().then((cfg) => {
      speechCfgRef.current = cfg;
    });
  }, []);

  const makeVoiceOptions = useCallback(
    (): Omit<CreateVoiceCoreOptions, "listener"> => ({
      surface: glass ? "glass" : "car",
      // Drive the LIVE runner session (claude/codex) the user already has up —
      // not a fresh task, not a cloud voice pipeline.
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
      onSwitchMachine: (id) => setDeviceId(id),
      callOps: callCarOps,
      tts: {
        provider: (speechCfgRef.current.ttsProvider as any) || "device",
        apiKey: speechCfgRef.current.apiKey,
        voice: speechCfgRef.current.ttsVoice,
      },
      locale: "en",
    }),
    [glass, callCarOps],
  );

  const onVoiceEvent = useCallback((ev: VoiceCoreEvent) => {
    if (!liveRef.current) return;
    // Map the core's fine-grained state onto the screen's 5-state model, which
    // already mirrors to the CarPlay template and the Live Activity.
    setStatus(
      ev.state === "listening"
        ? "recording"
        : ev.state === "speaking" || ev.state === "confirming"
          ? "speaking"
          : ev.state === "idle"
            ? "idle"
            : "thinking",
    );
    if (ev.turnComplete && ev.text) {
      const spoken = ev.text;
      setTurns((prev) =>
        [
          {
            id: `hf-${Date.now()}`,
            transcript: "",
            spoken,
            stage: "spoken" as const,
            at: Date.now(),
          },
          ...prev,
        ].slice(0, 50),
      );
    }
  }, []);

  const handsFree = useHandsFreeVoice(makeVoiceOptions, onVoiceEvent);

  const publishCarConversation = useCallback(
    async (transcript: string, reply: string) => {
      if (!deviceId || !transcript || !reply) return;
      await presentCarConversation({
        conversationId: carConversationId,
        contactName: carContactName,
        messages: [
          { from: "you", text: transcript, timestamp: Date.now() - 1 },
          { from: "agent", text: reply, timestamp: Date.now() },
        ],
      });
    },
    [carContactName, carConversationId, deviceId],
  );

  // ── recording (mirrors AgentVoiceButton's expo-av path) ───────────
  const startRecording = useCallback(async () => {
    setErrorMsg("");
    const { Audio } = require("expo-av");
    const perm = await Audio.getPermissionsAsync();
    if (perm.status !== "granted") {
      const req = perm.canAskAgain
        ? await Audio.requestPermissionsAsync()
        : perm;
      if (req.status !== "granted") {
        Alert.alert(
          "Microphone Access",
          "Mic permission is required to speak commands.",
          [
            { text: "Cancel", style: "cancel" },
            { text: "Open Settings", onPress: () => Linking.openSettings() },
          ],
        );
        return;
      }
    }
    // staysActiveInBackground is what keeps the loop alive once the phone
    // locks or backgrounds in a cradle — without it the session is torn down
    // mid-turn and the driver gets silence. Paired with UIBackgroundModes
    // "audio" in Info.plist / app.json; the plist key alone does nothing.
    await Audio.setAudioModeAsync({
      allowsRecordingIOS: true,
      playsInSilentModeIOS: true,
      staysActiveInBackground: true,
    });
    try {
      const { recording } = await Audio.Recording.createAsync(
        Audio.RecordingOptionsPresets.HIGH_QUALITY,
      );
      recordingRef.current = recording;
      setStatus("recording");
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
  }, []);

  /**
   * Release the audio session the moment a turn is over.
   *
   * This is a HARD requirement of Apple's CarPlay voice-based-conversation
   * category, not a nicety — criterion 2, verbatim: "Only hold an audio session
   * open when voice features are actively being used." We acquire the session
   * (staysActiveInBackground) when a turn starts so the loop survives the phone
   * locking in a cradle, and we hand it straight back when the turn ends.
   * Holding it while idle would both drain the battery and fail review.
   */
  const releaseAudioSession = useCallback(async () => {
    try {
      // expo-av is required lazily here, same as the recording path above.
      const { Audio } = require("expo-av");
      await Audio.setAudioModeAsync({
        allowsRecordingIOS: false,
        playsInSilentModeIOS: true,
        staysActiveInBackground: false,
      });
    } catch {
      // Never let session teardown surface as a driving-time error.
    }
  }, []);

  // One place, every exit path: the turn is done → give the session back.
  useEffect(() => {
    if (status === "idle" || status === "error") {
      void releaseAudioSession();
    }
  }, [status, releaseAudioSession]);

  /**
   * Mirror this screen's status onto the CarPlay voice template.
   *
   * The CarPlay scene (YaverCarPlaySceneDelegate) renders a CPVoiceControlTemplate
   * with exactly four states, and these strings ARE the contract with it. It's a
   * no-op when there's no CarPlay scene — un-entitled build, or simply no car
   * connected — so this screen never has to know whether it's driving a dashboard.
   */
  useEffect(() => {
    if (Platform.OS !== "ios") return;
    const carPlayState =
      status === "recording"
        ? "listening"
        : status === "thinking"
          ? "working"
          : status === "speaking"
            ? "speaking"
            : "ready"; // idle + error both return the driver to a resting template
    try {
      NativeModules.YaverInfo?.setCarPlayVoiceState?.(carPlayState);
    } catch {
      // A missing native module must never break the voice loop.
    }
  }, [status]);

  /**
   * Mirror the same status onto a Live Activity.
   *
   * This is the ONLY way a non-entitled app draws on the CarPlay Dashboard, and
   * unlike the template above it needs no entitlement at all — so it renders in
   * the car even on a build Apple hasn't blessed. The same card is reused by the
   * Lock Screen, the Dynamic Island and the Watch Smart Stack, which is why the
   * strings stay one-liners.
   *
   * Everything we pass is already a pre-summarized label, never task output —
   * see the driving-safety contract at the top of liveActivity.ts.
   */
  useEffect(() => {
    if (Platform.OS !== "ios") return;
    const machine =
      pickedDevice?.name || pickedDevice?.alias || deviceId || "runtime";

    if (status === "idle" || status === "error") {
      if (!activityRef.current) return; // nothing showing — don't summon a card to kill it
      activityRef.current = false;
      void endLiveActivity(
        status === "error"
          ? { status: "failed", headline: "Turn failed", detail: machine }
          : { status: "done", headline: "Done", detail: machine },
      );
      return;
    }

    const state: LiveActivityState =
      status === "recording"
        ? { status: "listening", headline: "Listening", detail: machine }
        : status === "thinking"
          ? { status: "working", headline: `Working on ${machine}`, detail: machine }
          : { status: "speaking", headline: "Replying", detail: machine };

    if (activityRef.current) {
      void updateLiveActivity(state);
    } else {
      activityRef.current = true;
      void startLiveActivity(machine, `car-${deviceId || "runtime"}`, state);
    }
  }, [status, pickedDevice, deviceId]);

  // Leaving the screen mid-turn must not strand a card on the driver's dashboard.
  useEffect(
    () => () => {
      if (activityRef.current) {
        activityRef.current = false;
        void endLiveActivity({ status: "done", headline: "Ended", detail: "" }, 0);
      }
    },
    [],
  );

  const stopRecordingToUri = useCallback(async (): Promise<string | null> => {
    const rec = recordingRef.current;
    if (!rec) return null;
    let uri: string | null = null;
    try {
      await rec.stopAndUnloadAsync();
      uri = rec.getURI() ?? null;
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : String(err));
      setStatus("error");
    }
    recordingRef.current = null;
    return uri;
  }, []);

  // Run a full turn from a recorded clip. Inserts a live turn row, transcribes,
  // GATES risky commands, then runs the lib loop (dispatch→poll→summarize→speak).
  const runTurnFromUri = useCallback(
    async (uri: string) => {
      const turnId = `${Date.now()}`;
      const seed: Turn = {
        id: turnId,
        transcript: "",
        spoken: "",
        stage: "listening",
        at: Date.now(),
      };
      setTurns((prev) => [seed, ...prev].slice(0, 50));
      setStatus("thinking");

      const { deps, config } = await buildDeps();

      // 1) Transcribe first so we can run the risk gate BEFORE any dispatch.
      let transcript = "";
      try {
        transcript = (await deps.transcribe(uri)).trim();
      } catch (e) {
        const spoken = "I couldn't understand that.";
        patchTurn(turnId, { stage: "error", spoken });
        await safeSpeak(deps, spoken);
        await publishCarConversation("voice command", spoken);
        setStatus("idle");
        return;
      }
      patchTurn(turnId, { transcript, stage: "transcribed" });
      if (!transcript) {
        const spoken = "I didn't catch that.";
        patchTurn(turnId, { stage: "error", spoken });
        await safeSpeak(deps, spoken);
        await publishCarConversation("voice command", spoken);
        setStatus("idle");
        return;
      }

      // 1.5) MACHINE SWITCH — "switch to pokayoke". Must run BEFORE the safety
      // gate and before dispatch: it retargets the turn rather than executing
      // anything, so it is never risky and must never reach a box. This is the
      // ONLY way to change machines on CarPlay — Apple's voice category forbids
      // showing a picker on the car screen, so the phone's device list is
      // unreachable while driving. We always speak the machine back, so a
      // misheard name is caught by ear instead of running a build on the wrong box.
      const switchReq = classifyMachineSwitch(transcript);
      if (switchReq) {
        const machine = matchMachine(
          switchReq.spokenName,
          devices.map((d: any) => ({
            id: d.id || d.deviceId,
            name: d.nickname || d.name || d.hostname || d.deviceId,
            // voiceHints are the names the user actually SAYS ("my mac mini",
            // "the box at maltepe") — set via /devices/voice-hints. They matter
            // most here: on CarPlay there's no picker, so the spoken name is
            // the only handle the driver has on a machine.
            aliases: [
              ...(Array.isArray(d.voiceHints) ? d.voiceHints : []),
              d.alias,
              d.hostname,
              d.name,
            ].filter(Boolean),
          })),
        );
        const spoken = spokenForMachineSwitch(machine, switchReq.spokenName);
        if (machine) setDeviceId(machine.id);
        patchTurn(turnId, { stage: "spoken", spoken });
        setStatus("speaking");
        await safeSpeak(deps, spoken);
        await publishCarConversation(transcript, spoken);
        setStatus("idle");
        return;
      }

      // 2) SAFETY GATE — risky commands stop here for explicit confirm.
      const risk = assessRisk(transcript);
      if (risk.risky) {
        patchTurn(turnId, { stage: "confirming", spoken: risk.prompt });
        setConfirm({ transcript, prompt: risk.prompt });
        setStatus("speaking");
        await safeSpeak(deps, risk.prompt);
        await publishCarConversation(transcript, risk.prompt);
        setStatus("idle");
        return; // dispatch only happens after confirmTurn()
      }

      // 3) Car assistant intents (meetings/mail) run through /ops on the chosen
      // runtime instead of becoming coding tasks.
      try {
        const surface = await executeCarSurfaceIntent(transcript, callCarOps);
        if (surface.handled) {
          patchTurn(turnId, { stage: "spoken", spoken: surface.spoken });
          setStatus("speaking");
          await safeSpeak(deps, surface.spoken);
          await publishCarConversation(transcript, surface.spoken);
          setStatus("idle");
          return;
        }
      } catch (e) {
        const spoken = "I couldn't reach that meeting or mail service.";
        patchTurn(turnId, { stage: "error", spoken });
        await safeSpeak(deps, spoken);
        await publishCarConversation(transcript, spoken);
        setStatus("idle");
        return;
      }

      await dispatchTurn(turnId, transcript, deps, config);
    },
    [buildDeps, callCarOps, publishCarConversation],
  );

  // Shared dispatch path (used after transcribe for safe commands AND after a
  // confirm for risky ones). We pass the already-known transcript by faking a
  // single-shot deps.transcribe so the lib's read-code guard + summarizer +
  // speak path all run unchanged.
  const dispatchTurn = useCallback(
    async (
      turnId: string,
      transcript: string,
      deps: ReturnType<typeof makeRealCarVoiceDeps> | any,
      config: CarVoiceConfig,
    ) => {
      setStatus("thinking");
      const fixedDeps = { ...deps, transcribe: async () => transcript };
      const r = await runCarVoiceTurn(
        "preset://" + turnId,
        fixedDeps,
        config,
        (step) => {
          if (!liveRef.current) return;
          if (step.stage === "dispatched") setStatus("thinking");
          if (step.stage === "spoken") setStatus("speaking");
          patchTurn(turnId, {
            stage: step.stage,
            status: step.status,
            ...(step.text ? { spoken: step.text } : {}),
          });
        },
      );
      patchTurn(turnId, {
        stage: r.declined ? "declined" : "spoken",
        spoken: r.spoken,
        status: r.status,
        declined: r.declined,
      });
      await publishCarConversation(transcript, r.spoken);
      setStatus("idle");
    },
    [publishCarConversation],
  );

  function patchTurn(id: string, patch: Partial<Turn>) {
    if (!liveRef.current) return;
    setTurns((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)));
  }

  // ── single control: start / stop / barge-in the hands-free loop ────
  // No push-to-talk. Tapping only enters or leaves the loop (and interrupts a
  // spoken reply); the driver never taps to submit a command — the semantic
  // endpointer + judge decide that. This is the whole point of the redesign.
  const onPressTalk = useCallback(async () => {
    if (!deviceId) {
      Alert.alert(
        "Pick a box first",
        "Choose the machine that should run your commands.",
      );
      return;
    }
    handsFree.toggle();
  }, [deviceId, handsFree]);

  // ── confirmation actions ──────────────────────────────────────────
  const confirmTurn = useCallback(async () => {
    if (!confirm) return;
    const { transcript } = confirm;
    setConfirm(null);
    const turnId = `${Date.now()}`;
    const seed: Turn = {
      id: turnId,
      transcript,
      spoken: "On it.",
      stage: "dispatched",
      at: Date.now(),
    };
    setTurns((prev) => [seed, ...prev].slice(0, 50));
    const { deps, config } = await buildDeps();
    await dispatchTurn(turnId, transcript, deps, config);
  }, [confirm, buildDeps, dispatchTurn]);

  const cancelTurn = useCallback(async () => {
    const transcript = confirm?.transcript;
    setConfirm(null);
    setStatus("idle");
    try {
      await speakText("Cancelled.", { provider: "device" });
    } catch {
      /* ignore */
    }
    if (transcript) {
      await publishCarConversation(transcript, "Cancelled. Nothing was run.");
    }
  }, [confirm?.transcript, publishCarConversation]);

  // Spoken confirm: record a short yes/no, transcribe, interpret. Lets the
  // driver confirm hands-free without reaching for the screen.
  const confirmBySpeech = useCallback(async () => {
    if (!confirm) return;
    await startRecording();
    // The user taps again (or we could VAD); for simplicity we reuse the PTT:
    // stop is wired through onPressTalk when status==="recording". Here we just
    // arm recording; the reply is handled by replyToConfirm below.
  }, [confirm, startRecording]);

  const replyToConfirm = useCallback(async () => {
    const uri = await stopRecordingToUri();
    setStatus("idle");
    if (!uri || !confirm) return;
    const { deps } = await buildDeps();
    let reply = "";
    try {
      reply = (await deps.transcribe(uri)).trim();
    } catch {
      /* ignore */
    }
    const verdict = interpretConfirmReply(reply);
    if (verdict === "confirm") {
      await confirmTurn();
    } else if (verdict === "cancel") {
      await cancelTurn();
    } else {
      try {
        await speakText("I didn't catch a yes or no — tap Confirm or Cancel.", {
          provider: "device",
        });
      } catch {
        /* ignore */
      }
    }
  }, [confirm, buildDeps, confirmTurn, cancelTurn, stopRecordingToUri]);

  // ── hands-free entry: autostart on deep link, and live trigger bus ─
  useEffect(() => {
    const unsub = carVoiceEntryBus.subscribe(() => {
      if (!deviceId) return; // need a box; the user picks once, then it's hands-free
      if (!handsFree.running) handsFree.start();
    });
    return unsub;
  }, [deviceId, handsFree]);

  useEffect(() => {
    // CarPlay connect deep-links here with ?autostart=1 → enter the hands-free
    // loop immediately. No tap, no PTT: listen → judge → run → speak → repeat.
    if (shouldAutostart(params.autostart) && deviceId && !handsFree.running) {
      handsFree.start();
    }
    // run once per device selection / autostart param
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [deviceId]);

  // Android Auto MessagingStyle RemoteInput path. Native emits yaverCarReply;
  // this bridges it into the same safe router used by the phone car screen.
  useEffect(() => {
    if (!deviceId) return () => {};
    return subscribeCarReplies((ev) => {
      const text = (ev.text || "").trim();
      if (!text) return;
      const conversationId = ev.conversationId || `car:${deviceId}`;
      const turnId = `car-reply-${Date.now()}`;
      const seed: Turn = {
        id: turnId,
        transcript: text,
        spoken: "",
        stage: "queued",
        at: Date.now(),
      };
      setTurns((prev) => [seed, ...prev].slice(0, 50));
      void (async () => {
        const { deps, config } = await buildDeps();
        const decision = await handleCarReply({
          conversationId,
          text,
          gate: carReplyGateRef.current,
          deps,
          config,
          ops: callCarOps,
          sessionTurn: async (prompt, choice) => {
            const r = await quicClient.runnerSessionTurn(deviceId, prompt, choice);
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
          sessionChoiceGate: sessionChoiceGateRef.current,
        });
        const stage: Turn["stage"] =
          decision.outcome === "needs-confirm"
            ? "confirming"
            : decision.outcome === "cancelled"
              ? "declined"
              : decision.outcome === "ignored"
                ? "error"
                : "spoken";
        patchTurn(turnId, { stage, spoken: decision.reply });
        await safeSpeak(deps, decision.reply);
        await presentCarConversation({
          conversationId,
          contactName: carContactName,
          messages: [
            { from: "you", text, timestamp: Date.now() - 1 },
            { from: "agent", text: decision.reply, timestamp: Date.now() },
          ],
        });
      })();
    });
  }, [
    buildDeps,
    callCarOps,
    carContactName,
    deviceId,
  ]);

  // ── styles ────────────────────────────────────────────────────────
  const card = {
    backgroundColor: c.bgCard,
    borderColor: c.border,
    borderWidth: 1,
    borderRadius: 12,
    padding: 14,
    marginBottom: 12,
  } as const;
  const TALK_COLOR =
    status === "recording"
      ? "#ef4444"
      : status === "thinking"
        ? "#8b5cf6"
        : status === "speaking"
          ? "#f59e0b"
          : c.accent;
  const talkLabel =
    status === "recording"
      ? "Listening…"
      : status === "thinking"
        ? "Working…"
        : status === "speaking"
          ? "Speaking… tap to interrupt"
          : confirm
            ? "Confirm needed"
            : "Tap to start — then just talk";

  // ── device picker ─────────────────────────────────────────────────
  if (!deviceId) {
    return (
      <View style={{ flex: 1, backgroundColor: c.bg }}>
        <AppScreenHeader
          title="Car Voice Coding"
          onBack={() => router.back()}
        />
        <ScrollView contentContainerStyle={{ padding: 16 }}>
          <Text
            style={{
              color: c.textPrimary,
              fontSize: 16,
              fontWeight: "700",
              marginBottom: 6,
            }}
          >
            Pick the box that runs your commands
          </Text>
          <Text style={{ color: c.textMuted, fontSize: 13, marginBottom: 14 }}>
            Speak a coding task; it runs on this machine and reads the result
            back over your car audio.
          </Text>
          {devices.map((d) => {
            const id = d.id || d.deviceId;
            const sleeping = isDeviceAsleep(d);
            return (
              <Pressable
                key={id}
                onPress={() => setDeviceId(id)}
                style={[
                  card,
                  {
                    flexDirection: "row",
                    justifyContent: "space-between",
                    alignItems: "center",
                  },
                ]}
              >
                <Text
                  style={{
                    color: c.textPrimary,
                    fontWeight: "600",
                    fontSize: 16,
                  }}
                >
                  {d.name || d.alias || id}
                </Text>
                <Text style={{ color: d.online ? "#22c55e" : sleeping ? c.accent : c.textMuted }}>
                  {d.online ? "online" : sleeping ? "asleep · tap to wake" : "offline"}
                </Text>
              </Pressable>
            );
          })}
          {devices.length === 0 && (
            <Text style={{ color: c.textMuted }}>
              No devices yet. Sign a box in first.
            </Text>
          )}

          {/* Live wake ladder for the picked box (big + glanceable for the car). */}
          {carRunning && (
            <View style={[card, { marginTop: 4 }]}>
              <WakeProgress state={carLifecycle} />
            </View>
          )}
          {!carRunning && pickedAsleep && (
            <Pressable
              onPress={carLifecycle.wake}
              disabled={carLifecycle.busy}
              style={[
                card,
                {
                  marginTop: 4,
                  alignItems: "center",
                  borderColor: c.accent,
                  backgroundColor: c.accentSoft,
                  opacity: carLifecycle.busy ? 0.6 : 1,
                },
              ]}
            >
              <Text style={{ color: c.accent, fontWeight: "800", fontSize: 16 }}>
                Wake {pickedDevice?.name || "box"}
              </Text>
            </Pressable>
          )}
        </ScrollView>
      </View>
    );
  }

  // ── glass HUD (compact: just the PTT + last status) ───────────────
  if (glass) {
    const last = turns[0];
    // A parked box can't run a spoken task — surface a big, glanceable wake
    // state on the HUD instead of a mic that would just fail.
    if (carRunning) {
      return (
        <View style={{ flex: 1, backgroundColor: c.bg, alignItems: "center", justifyContent: "center", padding: 20 }}>
          <View
            style={{
              width: 160, height: 160, borderRadius: 80, borderWidth: 6,
              borderColor: carLifecycle.phase === "error" ? c.error : c.accent,
              alignItems: "center", justifyContent: "center",
            }}
          >
            <Text style={{ color: c.textPrimary, fontSize: 34, fontWeight: "900", fontVariant: ["tabular-nums"] }}>
              {Math.round(carLifecycle.percent)}%
            </Text>
          </View>
          <Text style={{ color: c.textPrimary, marginTop: 18, fontSize: 20, fontWeight: "800", textAlign: "center" }}>
            {carLifecycle.meta.short}
          </Text>
          <View style={{ width: "90%", marginTop: 10 }}>
            <WakeProgress state={carLifecycle} compact />
          </View>
        </View>
      );
    }
    return (
      <View
        style={{
          flex: 1,
          backgroundColor: c.bg,
          alignItems: "center",
          justifyContent: "center",
          padding: 16,
        }}
      >
        <Pressable
          onPress={pickedAsleep ? carLifecycle.wake : onPressTalk}
          disabled={pickedAsleep && carLifecycle.busy}
          style={{
            width: 160,
            height: 160,
            borderRadius: 80,
            backgroundColor: pickedAsleep ? c.accent : TALK_COLOR,
            alignItems: "center",
            justifyContent: "center",
          }}
          accessibilityRole="button"
          accessibilityLabel={pickedAsleep ? `Wake ${pickedDevice?.name || "box"}` : talkLabel}
        >
          <Text style={{ color: "#fff", fontSize: pickedAsleep ? 22 : 20, fontWeight: "800" }}>
            {pickedAsleep ? "Wake" : status === "recording" ? "■" : "🎤"}
          </Text>
        </Pressable>
        <Text
          style={{
            color: c.textPrimary,
            marginTop: 16,
            fontSize: 16,
            textAlign: "center",
          }}
          numberOfLines={3}
        >
          {pickedAsleep ? `${pickedDevice?.name || "Box"} is asleep — tap to wake` : last?.spoken || talkLabel}
        </Text>
      </View>
    );
  }

  // ── full surface (phone / head unit) ──────────────────────────────
  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <AppScreenHeader title="Car Voice Coding" onBack={() => router.back()} />
      <ScrollView contentContainerStyle={{ padding: 16 }}>
        <View style={[card, { borderColor: c.accent }]}>
          <Text style={{ color: c.textMuted, fontSize: 11 }}>Yaver-native car surface</Text>
          <Text style={{ color: c.textPrimary, fontSize: 15, fontWeight: "700", marginTop: 4 }}>
            {yaverNativeSurfaceSummary("car")}
          </Text>
          <Text style={{ color: c.textSecondary, fontSize: 12, marginTop: 4 }}>
            Car is companion-only: voice, summaries, approvals, and handoff to phone/TV/runtime.
          </Text>
        </View>

        {/* Active box */}
        <View
          style={[
            card,
            {
              flexDirection: "row",
              justifyContent: "space-between",
              alignItems: "center",
            },
          ]}
        >
          <View style={{ flex: 1 }}>
            <Text style={{ color: c.textMuted, fontSize: 11 }}>Running on</Text>
            <Text
              style={{ color: c.textPrimary, fontWeight: "700", fontSize: 16 }}
              numberOfLines={1}
            >
              {pickedDevice?.name || pickedDevice?.alias || deviceId}
            </Text>
          </View>
          <Pressable
            onPress={() => setDeviceId("")}
            style={{
              paddingVertical: 8,
              paddingHorizontal: 12,
              backgroundColor: c.bgCard,
              borderRadius: 10,
              borderWidth: 1,
              borderColor: c.border,
            }}
          >
            <Text style={{ color: c.textMuted, fontSize: 13 }}>Switch</Text>
          </Pressable>
        </View>

        {/* Big push-to-talk */}
        <View style={{ alignItems: "center", marginVertical: 14 }}>
          <Pressable
            onPress={onPressTalk}
            style={{
              width: 200,
              height: 200,
              borderRadius: 100,
              backgroundColor: TALK_COLOR,
              alignItems: "center",
              justifyContent: "center",
              opacity: status === "thinking" ? 0.85 : 1,
            }}
            accessibilityRole="button"
            accessibilityLabel={talkLabel}
          >
            {status === "thinking" ? (
              <ActivityIndicator color="#fff" size="large" />
            ) : (
              <Text style={{ color: "#fff", fontSize: 56 }}>
                {status === "recording" ? "■" : "🎤"}
              </Text>
            )}
          </Pressable>
          <Text
            style={{
              color: c.textPrimary,
              fontSize: 18,
              fontWeight: "700",
              marginTop: 14,
              textAlign: "center",
            }}
          >
            {talkLabel}
          </Text>
          {!!errorMsg && (
            <Text
              style={{ color: c.error || "#f55", fontSize: 13, marginTop: 6 }}
            >
              {errorMsg}
            </Text>
          )}
        </View>

        {/* Turn history */}
        {turns.length > 0 && (
          <View style={card}>
            <Text
              style={{
                color: c.textPrimary,
                fontSize: 15,
                fontWeight: "700",
                marginBottom: 10,
              }}
            >
              History
            </Text>
            {turns.map((t) => {
              const stageColor =
                t.stage === "error"
                  ? c.error || "#ef4444"
                  : t.stage === "declined"
                    ? "#f59e0b"
                    : t.stage === "spoken"
                      ? "#22c55e"
                      : t.stage === "confirming"
                        ? "#f59e0b"
                        : c.textMuted;
              return (
                <View
                  key={t.id}
                  style={{
                    paddingVertical: 8,
                    borderBottomWidth: 1,
                    borderBottomColor: c.border,
                  }}
                >
                  <Text
                    style={{ color: c.textPrimary, fontSize: 15 }}
                    numberOfLines={2}
                  >
                    {t.transcript ? `“${t.transcript}”` : "…"}
                  </Text>
                  <View
                    style={{
                      flexDirection: "row",
                      alignItems: "center",
                      gap: 8,
                      marginTop: 4,
                    }}
                  >
                    <Text
                      style={{
                        color: stageColor,
                        fontSize: 12,
                        fontWeight: "600",
                      }}
                    >
                      {STAGE_LABEL[t.stage] || t.stage}
                    </Text>
                    {!!t.spoken && (
                      <Text
                        style={{ color: c.textMuted, fontSize: 13, flex: 1 }}
                        numberOfLines={2}
                      >
                        {t.spoken}
                      </Text>
                    )}
                  </View>
                </View>
              );
            })}
          </View>
        )}

        <Text
          style={{
            color: c.textMuted,
            fontSize: 11,
            textAlign: "center",
            marginTop: 4,
          }}
        >
          Risky commands (deploy / push / delete / force) always ask before
          running. Code is never read aloud while you drive.
        </Text>
      </ScrollView>

      {/* Confirmation gate modal */}
      <Modal
        visible={!!confirm}
        transparent
        animationType="fade"
        onRequestClose={cancelTurn}
      >
        <View
          style={{
            flex: 1,
            backgroundColor: "rgba(0,0,0,0.6)",
            justifyContent: "center",
            padding: 24,
          }}
        >
          <View
            style={{
              backgroundColor: c.bgCard,
              borderRadius: 16,
              padding: 20,
              borderWidth: 1,
              borderColor: c.border,
            }}
          >
            <Text
              style={{
                color: c.textPrimary,
                fontSize: 18,
                fontWeight: "800",
                marginBottom: 8,
              }}
            >
              Confirm before running
            </Text>
            <Text
              style={{ color: c.textPrimary, fontSize: 16, marginBottom: 6 }}
              numberOfLines={3}
            >
              “{confirm?.transcript}”
            </Text>
            <Text
              style={{ color: c.textMuted, fontSize: 13, marginBottom: 18 }}
            >
              {confirm?.prompt}
            </Text>
            <View style={{ flexDirection: "row", gap: 12 }}>
              <Pressable
                onPress={cancelTurn}
                style={{
                  flex: 1,
                  paddingVertical: 16,
                  borderRadius: 12,
                  backgroundColor: c.bgCard,
                  borderWidth: 1,
                  borderColor: c.border,
                  alignItems: "center",
                }}
                accessibilityRole="button"
                accessibilityLabel="Cancel command"
              >
                <Text
                  style={{
                    color: c.textPrimary,
                    fontSize: 16,
                    fontWeight: "700",
                  }}
                >
                  Cancel
                </Text>
              </Pressable>
              <Pressable
                onPress={confirmTurn}
                style={{
                  flex: 1,
                  paddingVertical: 16,
                  borderRadius: 12,
                  backgroundColor: "#ef4444",
                  alignItems: "center",
                }}
                accessibilityRole="button"
                accessibilityLabel="Confirm and run command"
              >
                <Text
                  style={{ color: "#fff", fontSize: 16, fontWeight: "800" }}
                >
                  Confirm
                </Text>
              </Pressable>
            </View>
            {/* Hands-free confirm: arm a short spoken yes/no */}
            <Pressable
              onPress={
                status === "recording" ? replyToConfirm : confirmBySpeech
              }
              style={{
                marginTop: 12,
                paddingVertical: 12,
                borderRadius: 12,
                backgroundColor: c.bg,
                borderWidth: 1,
                borderColor: c.border,
                alignItems: "center",
              }}
              accessibilityRole="button"
              accessibilityLabel="Answer by voice"
            >
              <Text
                style={{ color: c.accent, fontSize: 14, fontWeight: "600" }}
              >
                {status === "recording"
                  ? "Tap when done speaking"
                  : "Answer by voice (say “confirm” or “cancel”)"}
              </Text>
            </Pressable>
          </View>
        </View>
      </Modal>
    </View>
  );
}

// Never let a TTS failure crash the loop.
async function safeSpeak(
  deps: { speak: (s: string) => Promise<void> },
  text: string,
): Promise<void> {
  try {
    await deps.speak(text);
  } catch {
    /* ignore */
  }
}
