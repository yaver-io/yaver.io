// previewPhase.ts — phase-accurate narration for the web preview overlay.
//
// Web port of mobile/src/lib/previewPhase.ts (audit 2026-07 gap D6). The web
// PreviewPane used to show an undifferentiated spinner/blank stage while the
// box compiled — no statement of WHICH phase is running. Every wait the
// product imposes must narrate itself; a static title over a moving pipeline
// is a small lie that sends the user debugging the wrong layer.
//
// KEEP IN SYNC with mobile/src/lib/previewPhase.ts. The `probe` parameter is
// the in-page render probe's classification; the web dashboard's iframe is
// often cross-origin (relay/proxy), so callers that cannot inject the probe
// pass null and get the honest status-derived phrase.

export type PreviewPhaseStatus = {
  framework?: string;
  running?: boolean;
  building?: boolean;
  serving?: boolean;
} | null | undefined;

export type PreviewPhaseProbe = { reason?: string } | null | undefined;

/** Title for the preview's progress overlay, derived from what is ACTUALLY
 *  known: dev-server status first, then the in-page render probe. */
export function previewPhaseTitle(status: PreviewPhaseStatus, probe: PreviewPhaseProbe): string {
  const fw = (status?.framework || "web").trim() || "web";
  if (!status || !status.running) {
    // Not serving yet (includes building) — "starting" is the truth here.
    return `Starting ${fw} dev server…`;
  }
  // Server is up; the remaining phases live inside the iframe.
  switch (String(probe?.reason || "")) {
    case "flutter_booting":
      // Splash markup present, engine not attached — the server is DONE.
      return "Server ready — Flutter engine booting…";
    case "flutter_engine_attached":
    case "mount_has_visible_content":
    case "plain_body_content":
      // Content confirmed — the overlay is about to lift.
      return "Rendering first frame…";
    case "empty_mount":
    case "mount_without_visible_content":
    case "empty_body":
      return "Page loaded — app hasn't painted yet";
    case "agent_starting_response":
      // The agent's 503 "still starting" placeholder page.
      return `Server compiling — waiting for ${fw} to serve the page…`;
    case "http_error_document":
      return "Wrong preview route — agent returned 404";
    default:
      // No probe yet / document_not_ready / probe_exception.
      return `${fw} server ready — loading page…`;
  }
}

/** What to tell the user when the render probe TIMES OUT — names the likely
 *  cause per terminal reason instead of a generic "did not render". */
export function previewTimeoutExplanation(reason: string | undefined | null, framework?: string): string {
  const fw = (framework || "").trim();
  switch (String(reason || "")) {
    case "flutter_booting":
      return "Flutter's splash appeared but the engine never attached — usually a failed asset or CanvasKit fetch through the preview proxy. Look for 404s in the output above, or a pubspec asset that isn't on disk.";
    case "empty_mount":
    case "mount_without_visible_content":
      return `The page loaded but the ${fw || "app"} never painted into its mount — usually a runtime error right after startup. Check the output above for the first exception.`;
    case "empty_body":
      return "The server answered but the page body stayed empty — it may be serving a placeholder or the wrong path instead of the app bundle.";
    case "agent_starting_response":
      return "The agent kept serving its 'still starting' placeholder — the underlying dev server never finished compiling. Check the output above for the compile error.";
    case "http_error_document":
      return "The phone reached the machine, but the running Yaver agent does not serve the preview's logical refresh route. Update the agent, then retry.";
    default:
      return "The preview never confirmed a rendered frame. Check the output above for errors, then retry.";
  }
}
