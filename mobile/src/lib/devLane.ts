// devLane.ts — the browser-vs-Hermes reload-lane rules, in ONE testable place.
//
// This encodes a real dogfood bug so it can't regress:
//
//  "Browser Reload" silently became a Hermes native build for expo/RN, because
//  the phone dev-start hardcoded caller:"mobile" (Hermes-only) and DevPreview
//  forced the native path for any expo/RN status. Hermes couples to the guest
//  app's native modules (sfmg dies on `expo-gl`), is meaningless for Flutter,
//  and is blocked for Yaver-self-dev. browserLaneStartBody() carries the
//  web-lane intent so the agent serves the web target instead.
//
// NOTE: Browser Reload is NOT a coding task — the mobile handler calls
// quicClient.startDevServer() directly and DevPreview renders the result. There
// is deliberately NO "instruction string" that describes /dev/start to an AI
// agent; an earlier devStartInstruction() helper (which told a Flutter project
// it "has no Metro and no Hermes") was removed because rendering must never be
// dispatched through the task/runner system.

/**
 * The body override that turns a dev-start into the BROWSER lane: the agent
 * serves the web target (caller "web-ui") instead of a Hermes native bundle
 * (caller "mobile"). `platform:"web"` is what the agent reflects back in status
 * so DevPreview knows to render the WebView.
 */
export function browserLaneStartBody(): { platform: string; caller: string } {
  return { platform: "web", caller: "web-ui" };
}

export function hermesLaneStartBody(): { caller: string } {
  return { caller: "mobile" };
}

/**
 * True when a dev-server status is serving the web target (browser lane).
 *
 * The agent signals this with `devMode: "web"` (verified live: a web-lane
 * expo/flutter dev server reports devMode="web", NOT platform="web" — platform
 * stays empty). We accept either so a future agent that sets platform also
 * works. Keying only on `platform` was a real bug: the browser lane still
 * forced Hermes because platform was empty.
 */
export function isWebServedStatus(status: { platform?: string; devMode?: string }): boolean {
  const dm = String(status.devMode || "").toLowerCase();
  const pf = String(status.platform || "").toLowerCase();
  return dm === "web" || pf === "web";
}

/**
 * Whether a project's reload MUST use the native (Hermes) path rather than the
 * WebView. A web-served status (browser lane) is never native, even for expo/RN
 * — that was the bug where Browser Reload became a Hermes build.
 */
export function mustUseNativePreview(input: {
  framework?: string;
  platform?: string;
  devMode?: string;
  building?: boolean;
}): boolean {
  if (isWebServedStatus(input)) return false;
  const fw = (input.framework || "").toLowerCase();
  const isHermes = fw.includes("expo") || fw.includes("react-native");
  // dev-client is a native-runtime mode; but "web" devMode was already handled.
  return isHermes || input.devMode === "dev-client" || !!input.building;
}
