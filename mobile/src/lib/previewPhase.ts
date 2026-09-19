// previewPhase.ts — phase-accurate narration for the browser-preview overlay.
//
// The overlay title used to be a static "Starting <framework> dev server…"
// even when /dev/status already said running+serving and the injected render
// probe (previewReadyScript.ts) was reporting a far more specific truth —
// e.g. reason "flutter_booting": the server IS ready, Flutter's splash is on
// screen, and it's the ENGINE (CanvasKit/assets) that hasn't attached. Telling
// the user the server is still starting at that point is a small lie that
// sends them debugging the wrong layer. This maps {status, probe reason} to
// the honest sentence, in one place shared by BOTH browser-preview
// implementations (app/(tabs)/apps.tsx and src/components/DevPreview.tsx —
// a fix in one is not a fix).
//
// Probe reasons come from PREVIEW_READY_SCRIPT's classifier
// (src/lib/previewReadyScript.ts): document_not_ready,
// agent_starting_response, flutter_engine_attached, flutter_booting,
// empty_mount, mount_without_visible_content, mount_has_visible_content,
// plain_body_content, empty_body, http_error_document, probe_exception.

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
  // Server is up; the remaining phases live inside the WebView.
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
