/**
 * RuntimeTurnAnnouncerHost — delivers runtime-turn completions to the user.
 *
 * A watch or car acks in one sentence and then goes silent; the work lands
 * minutes later on a box nobody is looking at. This host closes that loop: it
 * polls the queue on the selected device and speaks / notifies when a turn
 * enters a state worth hearing about.
 *
 * The decision of WHAT to announce lives in runtimeTurnAnnouncer.ts (pure,
 * unit-tested). This file only owns delivery and lifecycle:
 *
 *   - Polls only while the app is foregrounded. A background timer here would
 *     be a battery burn with no way to speak anyway.
 *   - Backs off hard when there is nothing live, so an idle phone isn't
 *     hitting the box every few seconds forever.
 *   - Speaks at most one line per tick. Queueing five utterances at a red
 *     light is worse than saying the most urgent one.
 */
import { useEffect, useRef } from "react";
import { AppState, Platform } from "react-native";
import { useDevice } from "../context/DeviceContext";
import { connectionManager } from "../lib/connectionManager";
import { runtimeSurfaceClient } from "../lib/runtimeSurfaceClient";
import { RuntimeTurnAnnouncer, type RuntimeTurnAnnouncement } from "../lib/runtimeTurnAnnouncer";
import { presentCarConversation } from "../lib/carMessagingNotification";
import { speakText } from "../lib/speech";

/** Fast while work is in flight, slow when the queue is quiet. */
const POLL_ACTIVE_MS = 8000;
const POLL_IDLE_MS = 60000;

function pickDeviceId(devices: any[], activeDevice: any | null): string {
  const focused = connectionManager.focusedDeviceId();
  if (focused) return focused;
  const activeId = activeDevice?.id || activeDevice?.deviceId;
  if (activeId) return activeId;
  const connected = connectionManager.connectedDeviceIds()[0];
  if (connected) return connected;
  const online = devices.find((d) => d?.online);
  return online?.id || online?.deviceId || devices[0]?.id || devices[0]?.deviceId || "";
}

async function deliver(a: RuntimeTurnAnnouncement, deviceId: string): Promise<void> {
  // Android Auto / car surface: a notification the car can read and reply to.
  if (Platform.OS === "android") {
    try {
      await presentCarConversation({
        conversationId: `runtime-turn:${deviceId}`,
        contactName: "Yaver",
        messages: [{ from: "agent", text: a.spoken, timestamp: Date.now() }],
      });
    } catch {
      /* notification delivery is best-effort; speech below still runs */
    }
  }
  try {
    await speakText(a.spoken);
  } catch {
    /* TTS unavailable in this build — the notification already carried it */
  }
}

export function RuntimeTurnAnnouncerHost() {
  const deviceCtx = useDevice();
  const devices = ((deviceCtx as any).devices as any[]) || [];
  const activeDevice = (deviceCtx as any).activeDevice || null;
  const deviceId = pickDeviceId(devices, activeDevice);

  const announcerRef = useRef(new RuntimeTurnAnnouncer());
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const stoppedRef = useRef(false);

  // A different box has a different queue; a stale baseline there would
  // replay its history as if it were new.
  useEffect(() => {
    announcerRef.current.reset();
  }, [deviceId]);

  useEffect(() => {
    if (!deviceId) return;
    stoppedRef.current = false;

    const tick = async () => {
      if (stoppedRef.current) return;
      let delay = POLL_IDLE_MS;
      try {
        if (AppState.currentState === "active") {
          const res = await runtimeSurfaceClient.runtimeTurns(deviceId, 25);
          const items = res.items || [];
          const announcements = announcerRef.current.observe(items);
          if (announcements.length > 0) {
            // Most urgent first; say exactly one.
            const pick = announcements.find((a) => a.urgent) || announcements[0];
            await deliver(pick, deviceId);
          }
          const live = items.some(
            (i) => i.state === "running" || i.state === "queued" || i.state === "needs_input",
          );
          delay = live ? POLL_ACTIVE_MS : POLL_IDLE_MS;
        }
      } catch {
        // Box unreachable / signed out — stay quiet and retry slowly rather
        // than spamming a device that can't answer.
        delay = POLL_IDLE_MS;
      }
      if (!stoppedRef.current) timerRef.current = setTimeout(tick, delay);
    };

    timerRef.current = setTimeout(tick, POLL_ACTIVE_MS);
    return () => {
      stoppedRef.current = true;
      if (timerRef.current) clearTimeout(timerRef.current);
    };
  }, [deviceId]);

  return null;
}
