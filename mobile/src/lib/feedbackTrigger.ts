import AsyncStorage from "@react-native-async-storage/async-storage";
import { Accelerometer } from "expo-sensors";
import { AppState, type AppStateStatus, NativeModules } from "react-native";
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
  const cfg = await currentFeedbackConfig(userId);
  if (!cfg?.enabled || cfg.trigger !== "shake") return;
  cooldownUntil = nowMs() + 2500;
  if (activeRemoteRuntimeSessionID && quicClient.isConnected) {
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

  return () => {
    accelSub.remove();
    appStateSub.remove();
  };
}
