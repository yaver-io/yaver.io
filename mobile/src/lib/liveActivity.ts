/**
 * liveActivity.ts — Yaver on the CarPlay Dashboard.
 *
 * A Live Activity is the ONE way a non-entitled app draws UI on a CarPlay
 * screen. Apple's CarPlay Developer Guide (June 2026) is explicit: "Your app
 * does not need to be a CarPlay app to support widgets and Live Activities in
 * CarPlay." The native widget declares supplementalActivityFamilies([.small]),
 * which is what makes it Dashboard-eligible; the same view is reused by the
 * Lock Screen, the Dynamic Island, and the Watch Smart Stack.
 *
 * DRIVING-SAFETY CONTRACT — the whole reason this file is thin:
 * every string that reaches the car is a PRE-SUMMARIZED one-liner. Never pass
 * raw task output, a diff, a log, or a stack trace to these functions. The
 * summarizers that already enforce this live in carVoiceCoding.ts
 * (summarizeForReadback) and carSurfaceIntent.ts (spokenForCarIntent) — feed
 * this from those, not from the wire.
 *
 * Failure is always non-fatal: Live Activities can be switched off by the user
 * and don't exist below iOS 16.2. Every call resolves to a boolean rather than
 * throwing, so no caller ever has to guard a dashboard nicety with try/catch.
 */

import { NativeModules, Platform } from "react-native";

/** Coarse state — drives the accent colour and glyph on the car screen. */
export type LiveActivityStatus =
  | "working"
  | "done"
  | "failed"
  | "listening"
  | "speaking";

export interface LiveActivityState {
  status: LiveActivityStatus;
  /** One short line: "Building sfmg". Keep under ~28 chars. */
  headline: string;
  /** Secondary line: "pokayoke · 2m". Keep under ~24 chars. */
  detail?: string;
  /** 0…1 when known; omit for indeterminate work. */
  progress?: number;
}

interface YaverLiveActivityNative {
  start(
    machine: string,
    taskId: string,
    status: string,
    headline: string,
    detail: string,
    progress: number | null,
  ): Promise<string>;
  update(
    status: string,
    headline: string,
    detail: string,
    progress: number | null,
  ): Promise<boolean>;
  end(
    status: string,
    headline: string,
    detail: string,
    dismissAfter: number | null,
  ): Promise<boolean>;
  isAvailable(): Promise<boolean>;
}

const native: YaverLiveActivityNative | undefined =
  Platform.OS === "ios"
    ? (NativeModules.YaverLiveActivity as YaverLiveActivityNative | undefined)
    : undefined;

/** True when the OS will actually render an activity (iOS 16.2+, user hasn't disabled it). */
export async function liveActivityAvailable(): Promise<boolean> {
  if (!native) return false;
  try {
    return await native.isAvailable();
  } catch {
    return false;
  }
}

/**
 * Put a task on the CarPlay Dashboard. Replaces any activity already showing —
 * one Yaver card at a time, because three competing cards on a dashboard is
 * worse than none.
 */
export async function startLiveActivity(
  machine: string,
  taskId: string,
  state: LiveActivityState,
): Promise<boolean> {
  if (!native) return false;
  try {
    await native.start(
      machine,
      taskId,
      state.status,
      clamp(state.headline, 40),
      clamp(state.detail ?? machine, 32),
      state.progress ?? null,
    );
    return true;
  } catch {
    // Disabled in Settings, or pre-16.2. A dashboard card is a nicety; the
    // voice loop that called us keeps working regardless.
    return false;
  }
}

/** Update the in-flight card. No-op when nothing is showing. */
export async function updateLiveActivity(
  state: LiveActivityState,
): Promise<boolean> {
  if (!native) return false;
  try {
    return await native.update(
      state.status,
      clamp(state.headline, 40),
      clamp(state.detail ?? "", 32),
      state.progress ?? null,
    );
  } catch {
    return false;
  }
}

/**
 * End the card with a terminal state. It lingers `dismissAfterSec` seconds so a
 * driver who glances late still sees "Deploy failed" rather than an empty slot.
 */
export async function endLiveActivity(
  state: LiveActivityState,
  dismissAfterSec = 8,
): Promise<boolean> {
  if (!native) return false;
  try {
    return await native.end(
      state.status,
      clamp(state.headline, 40),
      clamp(state.detail ?? "", 32),
      dismissAfterSec,
    );
  } catch {
    return false;
  }
}

/**
 * Hard cap on anything bound for the car. A Live Activity has no scroll, and a
 * driver has no time — an over-long string is a bug, not a layout problem. This
 * is a backstop, not a licence to pass raw output: summarize upstream.
 */
function clamp(s: string, max: number): string {
  const t = (s || "").replace(/\s+/g, " ").trim();
  if (t.length <= max) return t;
  return t.slice(0, Math.max(1, max - 1)).trimEnd() + "…";
}
