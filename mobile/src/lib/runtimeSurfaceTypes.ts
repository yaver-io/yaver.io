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
  | "mcp"
  | "cli";

export type SurfaceInteraction = "voice" | "dpad" | "touch" | "keyboard" | "approval" | "stream";
export type SurfaceVisualBudget = "none" | "glance" | "panel" | "full";
export type SurfaceRiskPolicy = "normal" | "driving" | "watch" | "shared-tv" | "mcp";

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
