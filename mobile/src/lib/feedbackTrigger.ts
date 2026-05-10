import AsyncStorage from "@react-native-async-storage/async-storage";
import { Accelerometer } from "expo-sensors";
import { AppState, type AppStateStatus, NativeEventEmitter, NativeModules, Platform } from "react-native";
import { quicClient } from "./quic";

type FeedbackLaunchSource = "shake" | "native-guest-shake" | "remote-runtime";

type FeedbackLaunchListener = (payload: { source: FeedbackLaunchSource }) => void;

const listeners = new Set<FeedbackLaunchListener>();
const FEEDBACK_KEY_FALLBACK = "@yaver/feedback_config";
let activeRemoteRuntimeSessionID: string | null = null;
let cooldownUntil = 0;

function nowMs() {
  return Date.now();
}

export function subscribeFeedbackLaunch(listener: FeedbackLaunchListener): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function triggerFeedbackLaunch(source: FeedbackLaunchSource): void {
  for (const listener of listeners) listener({ source });
}

export function setActiveRemoteRuntimeSession(sessionId: string | null): void {
  activeRemoteRuntimeSessionID = sessionId;
}

async function currentFeedbackConfig(userId?: string | null): Promise<{ enabled?: boolean; trigger?: string } | null> {
  const keys = userId ? [`@yaver/u/${userId}/feedback_config`, FEEDBACK_KEY_FALLBACK] : [FEEDBACK_KEY_FALLBACK];
  for (const key of keys) {
    try {
      const raw = await AsyncStorage.getItem(key);
      if (raw) return JSON.parse(raw);
    } catch {
      // ignore
    }
  }
  return null;
}

async function maybeLaunchFeedbackFromShake(source: FeedbackLaunchSource, userId?: string | null): Promise<void> {
  if (nowMs() < cooldownUntil) return;
  // `native-guest-shake` and `remote-runtime` are unconditional: the user
  // explicitly entered guest-runtime mode (Hermes-pushed bundle inside the
  // Yaver host, or a remote-runtime session bridging an external app), so
  // a shake there IS the opt-in signal. Don't gate it on the user's
  // settings.feedback.enabled toggle — that toggle is for the standalone
  // Yaver app's own draggable mic/icon. If we honored it here, every
  // first-time guest-app shake would silently no-op and the user would
  // think the SDK is broken.
  //
  // For non-guest sources (a shake while standing on Yaver's own surfaces
  // with no guest active) we keep the toggle gate so the floating button
  // stays opt-in.
  const isImplicitOptIn = source === "native-guest-shake" || source === "remote-runtime";
  if (!isImplicitOptIn) {
    const cfg = await currentFeedbackConfig(userId);
    if (!cfg?.enabled || cfg.trigger !== "shake") return;
  }
  cooldownUntil = nowMs() + 2500;
  // quicClient.isConnected checks the FOCUSED pool client only. The
  // remote-runtime session might be bound to a different (still
  // pooled) device — in that case the focused check would fail and
  // the launch-feedback command would silently never go out. We can't
  // know from here which pool client owns the session, so be lenient:
  // try to send through the focused client AND let the call fail
  // softly (.catch), since the worst case is a no-op the user can
  // re-trigger by shaking again.
  if (activeRemoteRuntimeSessionID) {
    quicClient.sendRemoteRuntimeCommand(activeRemoteRuntimeSessionID, "launch-feedback", source).catch(() => {});
  }
  triggerFeedbackLaunch(source);
}

export function startFeedbackShakeBridge(userId?: string | null): () => void {
  let lastMagnitude = 0;
  let appState: AppStateStatus = AppState.currentState;
  const appStateSub = AppState.addEventListener("change", async (nextState) => {
    appState = nextState;
    if (nextState === "active") {
      try {
        const pending = await (NativeModules as any)?.YaverInfo?.consumePendingFeedbackLaunch?.();
        if (pending) {
          await maybeLaunchFeedbackFromShake("native-guest-shake", userId);
        }
      } catch {
        // ignore
      }
    }
  });

  Accelerometer.setUpdateInterval(220);
  const accelSub = Accelerometer.addListener(({ x, y, z }) => {
    if (appState !== "active") return;
    const magnitude = Math.sqrt(x * x + y * y + z * z);
    const delta = Math.abs(magnitude - lastMagnitude);
    lastMagnitude = magnitude;
    if (delta > 1.45) {
      void maybeLaunchFeedbackFromShake("shake", userId);
    }
  });

  void (async () => {
    try {
      const pending = await (NativeModules as any)?.YaverInfo?.consumePendingFeedbackLaunch?.();
      if (pending) {
        await maybeLaunchFeedbackFromShake("native-guest-shake", userId);
      }
    } catch {
      // ignore
    }
  })();

  // Android: subscribe to YaverShakeDetector's native event.
  // iOS's ShakeDetectingWindow (AppDelegate.swift:71) handles shake
  // at the UIWindow level and reaches JS through a different path;
  // on Android we wire the SensorManager-based detector here so the
  // gesture works even when the user's settings.feedback.trigger is
  // "floating-button" (the default). Routing through
  // "native-guest-shake" hits the unconditional branch at lines
  // 47-58 so shake always opens the overlay — matching iOS.
  let nativeShakeSub: { remove: () => void } | null = null;
  if (Platform.OS === "android") {
    const detector = (NativeModules as any)?.YaverShakeDetector;
    if (detector) {
      const emitter = new NativeEventEmitter(detector);
      nativeShakeSub = emitter.addListener("YaverShakeDetected", () => {
        if (appState !== "active") return;
        void maybeLaunchFeedbackFromShake("native-guest-shake", userId);
      });
    }
  }

  return () => {
    accelSub.remove();
    appStateSub.remove();
    nativeShakeSub?.remove();
  };
}
