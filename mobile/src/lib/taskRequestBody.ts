export type SendTaskRequestBodyArgs = {
  title: string;
  description: string;
  model?: string;
  runner?: string;
  customCommand?: string;
  speechContext?: Record<string, unknown> | undefined;
  images?: unknown[];
  workDir?: string;
  mode?: string;
  video?: { enabled?: boolean; source?: "browser" | "sim-ios" | "sim-android" | "phone" };
  codeMode?: boolean;
  allowLocalFallback?: boolean;
  placementKind?: "vibe" | "build" | "deploy" | "test" | "source" | "autorun" | "unknown";
};

export function buildSendTaskRequestBody(args: SendTaskRequestBodyArgs): Record<string, unknown> {
  return {
    title: args.title,
    description: args.description,
    source: args.codeMode ? "mobile-code" : "mobile",
    ...(args.model ? { model: args.model } : {}),
    ...(args.runner ? { runner: args.runner } : {}),
    ...(args.mode ? { mode: args.mode } : {}),
    ...(args.customCommand ? { customCommand: args.customCommand } : {}),
    ...(args.speechContext ? { speechContext: args.speechContext } : {}),
    ...(args.images?.length ? { images: args.images } : {}),
    ...(args.workDir ? { workDir: args.workDir } : {}),
    ...(args.video?.enabled ? { videoEnabled: true } : {}),
    ...(args.video?.source ? { videoSource: args.video.source } : {}),
    ...(args.allowLocalFallback ? { allowLocalFallback: true } : {}),
    ...(args.placementKind ? { placementKind: args.placementKind } : {}),
  };
}
