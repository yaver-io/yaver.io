import { remotelessCapability, type RemotelessCapability } from "../_core/remoteless";

// renderCapability.ts — the client-side failure contract for preview surfaces.
//
// A missing runner is a capability constraint, not a transient loading state.
// Keep the code and route stable so mobile, tvOS, visionOS, and future native
// surfaces can render the same honest answer without matching prose.

export const REMOTE_RENDER_REQUIRED = "surface.remote_render_required";

export type RenderCapabilityFailure = {
  code: string;
  legacyCode: typeof REMOTE_RENDER_REQUIRED;
  capability: RemotelessCapability;
  title: string;
  message: string;
  action: { label: string; route: "/devices" };
  alternativeAction?: { label: string; route: "/devices" };
};

export function remoteRenderRequiredFailure(
  surface: string,
  capability: Extract<RemotelessCapability, "dev-server" | "web-build" | "flutter-render" | "native-build" | "simulator"> = "dev-server",
  platform: "ios" | "android" | "web" | "companion" = "companion",
): RenderCapabilityFailure {
  const gap = remotelessCapability(capability, platform);
  return {
    code: gap.code,
    legacyCode: REMOTE_RENDER_REQUIRED,
    capability,
    title: gap.summary,
    message: `${surface}: ${gap.detail}`,
    action: { label: gap.route.label, route: gap.route.path },
    alternativeAction: gap.alternateRoute
      ? { label: gap.alternateRoute.label, route: gap.alternateRoute.path }
      : undefined,
  };
}
