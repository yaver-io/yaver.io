// devLane.ts — the browser-vs-Hermes reload-lane rules, in ONE testable place.
//
// These encode two real dogfood bugs so they can't regress:
//
//  1. Flutter (and Swift/Kotlin/web) were told to "start Metro … load the JS
//     bundle via Hermes push" — a React-Native-only instruction. Metro and
//     Hermes do not exist for those stacks. devStartInstruction() returns a
//     framework-correct instruction.
//
//  2. "Browser Reload" silently became a Hermes native build for expo/RN,
//     because the phone dev-start hardcoded caller:"mobile" (Hermes-only) and
//     DevPreview forced the native path for any expo/RN status. Hermes couples
//     to the guest app's native modules (sfmg dies on `expo-gl`), is meaningless
//     for Flutter, and is blocked for Yaver-self-dev. browserLaneStart() carries
//     the web-lane intent so the agent serves the web target instead.

/** Framework-correct instruction for a `POST /dev/start` coding task. */
export function devStartInstruction(framework: string | undefined, targetPath: string): string {
  const fw = (framework || "").toLowerCase();
  if (fw === "flutter") {
    return `Call POST /dev/start with workDir=${targetPath} framework=flutter to start the Flutter web dev server (flutter run -d web-server). DO NOT run 'flutter build', native builds, xcodebuild, or gradlew — the preview renders the Flutter web build in the browser/WebRTC lane. Flutter has no Metro and no Hermes.`;
  }
  if (fw === "expo" || fw === "react-native") {
    return `Call POST /dev/start with workDir=${targetPath} to start Metro. DO NOT run 'expo run:ios', 'expo run:android', 'xcodebuild', 'gradlew', or any native build — the mobile app loads the JS bundle via Hermes push (/dev/build-native). Only Metro is needed.`;
  }
  if (fw === "swift" || fw === "kotlin") {
    return `Call POST /dev/start with workDir=${targetPath} framework=${fw}. This is a native ${fw} app — it previews over the WebRTC lane (simulator/emulator on the box). Do NOT expect Metro or a Hermes bundle; there is no JS runtime to load one into.`;
  }
  return `Call POST /dev/start with workDir=${targetPath}${fw ? ` framework=${fw}` : ""} to start the web dev server. No native builds. The browser/WebRTC lane renders the dev server output.`;
}

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
