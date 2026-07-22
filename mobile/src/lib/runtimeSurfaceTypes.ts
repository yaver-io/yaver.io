export type RuntimeSurface =
  | "mobile-phone"
  | "mobile-tablet"
  | "wearable-watch"
  | "wearable-wear"
  | "car-audio"
  | "car-android-auto"
  | "car-carplay"
  | "tv-living-room"
  | "tv-android"
  | "tv-apple"
  | "headset-visionos"
  | "headset-android-xr"
  | "web-spatial-hud"
  | "web-spatial-vr"
  | "mcp"
  | "cli";

export type SurfaceInteraction = "voice" | "dpad" | "touch" | "keyboard" | "approval" | "stream";
export type SurfaceVisualBudget = "none" | "glance" | "panel" | "full";
export type SurfaceRiskPolicy = "normal" | "driving" | "watch" | "shared-tv" | "spatial" | "mcp";

export interface TaskViewportInput {
  surface?: RuntimeSurface | string;
  interaction?: SurfaceInteraction | string;
  paneCount?: number;
  paneCols?: number;
  paneRows?: number;
  voice?: boolean;
  ttsBudget?: number;
  visualBudget?: SurfaceVisualBudget | string;
  riskPolicy?: SurfaceRiskPolicy | string;
  sttEnabled?: boolean;
  ttsEnabled?: boolean;
  sttProvider?: string;
  ttsProvider?: string;
  ttsMode?: boolean;
}

export type OpsTarget = { id?: string } | string | undefined;

export interface RuntimeTurnEvidence {
  kind?: string;
  ref?: string;
  sourceSurface?: string;
  screen?: string;
  durationMs?: number;
}

export interface RuntimeTurnTarget {
  deviceId?: string;
  deviceAlias?: string;
  session?: string;
  runner?: string;
  project?: string;
  workDir?: string;
}

export interface RuntimeTurnSurface {
  id?: string;
  class?: string;
  interaction?: SurfaceInteraction | string;
  visualBudget?: SurfaceVisualBudget | string;
  ttsBudget?: number;
  riskPolicy?: SurfaceRiskPolicy | string;
  replyTo?: string;
}

export interface RuntimeTurnRequest {
  utterance?: string;
  text?: string;
  prompt?: string;
  choice?: string;
  target?: RuntimeTurnTarget;
  surface?: RuntimeTurnSurface;
  development?: {
    goal?: string;
    intentClass?: "idea-capture" | "goal" | "start-coding" | "queue" | "autorun" | "session-turn" | "analysis" | string;
    evidence?: RuntimeTurnEvidence[];
    queue?: {
      mode?: "capture" | "enqueue" | "enqueue-or-run" | "run" | string;
      priority?: "low" | "normal" | "high" | string;
      afterFinish?: string[];
    };
    meta?: Record<string, unknown>;
  };
  mode?: "auto" | "run" | string;
  run?: boolean;
  queue?: boolean;
}

export interface RuntimeTurnQueueItem {
  itemId: string;
  state: RuntimeTurnState;
  utterance: string;
  intentClass?: string;
  target?: RuntimeTurnTarget;
  surface?: RuntimeTurnSurface;
  evidence?: RuntimeTurnEvidence[];
  taskId?: string;
  session?: string;
  runner?: string;
  reason?: string;
  spoken?: string;
  error?: string;
  testTarget?: RuntimeTurnTestTarget;
  createdAt?: string;
  updatedAt?: string;
  meta?: Record<string, unknown>;
}

/**
 * Whether the user can actually test this yet.
 *
 * `unverified` is the honest default after a task finishes: code changed, but
 * nothing has reloaded on a device. `delivered` means a reload reached
 * `listeners` live command streams. `unreachable` means the reload was
 * attempted and NOTHING was listening — never render that as success.
 */
export interface RuntimeTurnTestTarget {
  kind?: string;
  state?: "unverified" | "delivered" | "unreachable" | "failed" | string;
  deviceId?: string;
  detail?: string;
  listeners?: number;
  attemptedAt?: string;
}

export type RuntimeTurnState =
  | "captured"
  | "queued"
  | "waking"
  | "running"
  | "needs_input"
  | "ready_to_test"
  | "ready_to_deploy"
  | "deploying"
  | "done"
  | "failed"
  | "cancelled"
  | string;

export interface RuntimeTurnResponse {
  ok: boolean;
  turnId?: string;
  state: RuntimeTurnState;
  spoken?: string;
  haptic?: "start" | "attention" | "success" | "failure" | string;
  glance?: { title?: string; line?: string; [key: string]: string | undefined };
  queue?: RuntimeTurnQueueItem;
  target?: RuntimeTurnTarget;
  testTarget?: RuntimeTurnTestTarget;
  awaitingChoice?: boolean;
  options?: string[];
  panel?: { kind?: string; text?: string; [key: string]: string | undefined };
  handoff?: { targetSurface?: string; reason?: string; url?: string; [key: string]: string | undefined };
  error?: string;
  code?: string;
  reason?: string;
}

export interface RuntimeTurnListResponse {
  ok: boolean;
  items: RuntimeTurnQueueItem[];
  count: number;
}

/**
 * Result of asking "is this shippable?" WITHOUT shipping it.
 *
 * `command` is what a human runs; Yaver never executes it for you. A voice
 * surface cannot meaningfully consent to a store submission, and TestFlight
 * has no rollback — a bad build can only be superseded.
 */
export interface RuntimeDeployPreflight {
  ok: boolean;
  target: string;
  ready: boolean;
  blockers?: string[];
  command?: string;
  note: string;
  spoken?: string;
  state: RuntimeTurnState;
  turnId?: string;
}

export type DpadTarget = "appletv" | "androidtv" | "home";
export type DpadKey =
  | "up" | "down" | "left" | "right" | "select" | "menu" | "home"
  | "play_pause" | "play" | "pause" | "stop" | "next" | "previous"
  | "power" | "volume_up" | "volume_down";

export interface GatewayIntentResult {
  decision?: unknown;
  result?: unknown;
  act_id?: string;
  preview?: unknown;
  note?: string;
  error?: string;
}

export function carVoiceViewport(ttsBudget = 200): TaskViewportInput {
  return {
    surface: "car-audio",
    interaction: "voice",
    visualBudget: "none",
    riskPolicy: "driving",
    voice: true,
    ttsBudget,
    sttEnabled: true,
    ttsEnabled: true,
  };
}

export function watchVoiceViewport(ttsBudget = 160): TaskViewportInput {
  return {
    surface: "wearable-watch",
    interaction: "voice",
    visualBudget: "glance",
    riskPolicy: "watch",
    voice: true,
    ttsBudget,
    sttEnabled: true,
    ttsEnabled: true,
  };
}

export function tvDpadViewport(surface: RuntimeSurface | string = "tv-living-room"): TaskViewportInput {
  return {
    surface,
    interaction: "dpad",
    visualBudget: "glance",
    riskPolicy: "shared-tv",
  };
}

export function headsetViewport(surface: RuntimeSurface | string = "headset-visionos"): TaskViewportInput {
  return {
    surface,
    interaction: "touch",
    visualBudget: "panel",
    riskPolicy: "spatial",
  };
}

export function viewportHeaders(vp: TaskViewportInput): Record<string, string> {
  const headers: Record<string, string> = {};
  if (vp.surface) headers["X-Yaver-Surface"] = String(vp.surface);
  if (vp.interaction) headers["X-Yaver-Interaction"] = String(vp.interaction);
  if (vp.visualBudget) headers["X-Yaver-Visual-Budget"] = String(vp.visualBudget);
  if (vp.riskPolicy) headers["X-Yaver-Risk-Policy"] = String(vp.riskPolicy);
  const voice: string[] = [];
  if (vp.sttEnabled || vp.voice) voice.push("stt");
  if (vp.ttsEnabled) voice.push("tts");
  if (voice.length) headers["X-Yaver-Voice"] = voice.join(",");
  if (vp.ttsMode) headers["X-Yaver-TTS-Mode"] = "1";
  return headers;
}

export function normalizeOpsInitial<T = unknown>(data: { ok?: boolean; error?: string; code?: string; initial?: T }): T {
  if (data?.ok === false) {
    throw new Error(data.error || data.code || "ops call failed");
  }
  return (data?.initial ?? data) as T;
}
