import assert from "node:assert/strict";
import test from "node:test";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const mobile = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const more = readFileSync(join(mobile, "app", "(tabs)", "more.tsx"), "utf8");
const dogfood = readFileSync(join(mobile, "app", "(tabs)", "dogfood.tsx"), "utf8");
const settings = readFileSync(join(mobile, "app", "(tabs)", "settings.tsx"), "utf8");
const attached = readFileSync(join(mobile, "app", "attach.tsx"), "utf8");
const rootLayout = readFileSync(join(mobile, "app", "_layout.tsx"), "utf8");
const gate = readFileSync(join(mobile, "src", "components", "AttachModeSection.tsx"), "utf8");
const launch = readFileSync(join(mobile, "app", "dogfood-launch.tsx"), "utf8");
const bubble = readFileSync(join(mobile, "src", "components", "BrowserVibeBubble.tsx"), "utf8");
const overlay = readFileSync(join(mobile, "src", "context", "DogfoodOverlayContext.tsx"), "utf8");
const quic = readFileSync(join(mobile, "src", "lib", "quic.ts"), "utf8");
const remoteRuntime = readFileSync(join(mobile, "app", "remote-runtime.tsx"), "utf8");
const tasks = readFileSync(join(mobile, "app", "(tabs)", "tasks.tsx"), "utf8");
const metro = readFileSync(join(mobile, "metro.config.js"), "utf8");
const mobilePackage = JSON.parse(readFileSync(join(mobile, "package.json"), "utf8"));
const webLauncher = readFileSync(join(mobile, "scripts", "start-web.mjs"), "utf8");
const projects = readFileSync(join(mobile, "app", "(tabs)", "apps.tsx"), "utf8");
const devPreview = readFileSync(join(mobile, "src", "components", "DevPreview.tsx"), "utf8");
const appVersion = readFileSync(join(mobile, "src", "lib", "appVersion.ts"), "utf8");
const taskRequestBody = readFileSync(join(mobile, "src", "lib", "taskRequestBody.ts"), "utf8");
const attachClient = readFileSync(join(mobile, "src", "lib", "attachClient.ts"), "utf8");
const pushAuth = readFileSync(join(mobile, "src", "lib", "pushAuth.ts"), "utf8");
const iosAppDelegate = readFileSync(join(mobile, "ios", "Yaver", "AppDelegate.swift"), "utf8");
const iosDogfoodSettings = readFileSync(join(mobile, "ios", "Yaver", "YaverSettingsPane.swift"), "utf8");
const sdk = join(mobile, "..", "sdk", "feedback", "react-native", "src");
const entryIcon = readFileSync(join(sdk, "DogfoodEntryIcon.tsx"), "utf8");
const nativeMenu = readFileSync(join(sdk, "DogfoodNativeMenu.tsx"), "utf8");
const require = createRequire(import.meta.url);

test("Settings leads with mobile build and runtime mode before TV sign-in", () => {
  const identity = settings.indexOf("runtimeIdentityBar");
  const tvSignIn = settings.indexOf("Sign in a TV");
  assert.ok(identity >= 0, "Settings must render the mobile runtime identity");
  assert.ok(tvSignIn > identity, "mobile runtime identity must appear before TV sign-in");
  assert.match(settings, /APP_BUILD, APP_VERSION, mobileRuntimeMode/);
  assert.match(settings, /Dogfood mode/);
  assert.match(settings, /Native mode/);
});

test("mobile requests send build and native-or-Dogfood runtime identity", () => {
  assert.match(appVersion, /nativeAppVersion/);
  assert.match(appVersion, /nativeBuildVersion/);
  assert.match(appVersion, /getBuildNumber/,
    "native builds must read CFBundleVersion when Expo constants omit it");
  assert.match(appVersion, /installer === "TestFlight"/);
  assert.match(appVersion, /installer === "AppStore"/);
  assert.match(settings, /mobileDistributionLabel\(\)/);
  assert.doesNotMatch(settings, /APP_BUILD \|\| "unknown"/,
    "mobile Settings must omit unavailable build metadata instead of showing unknown");
  assert.match(appVersion, /runtimeMode: mobileRuntimeMode/);
  assert.match(taskRequestBody, /sessionSettings/);
  assert.match(pushAuth, /mobileRuntimeIdentity\(\)/);
});

test("Metro resolves shared Dogfood UI dependencies from the mobile workspace", () => {
	assert.match(mobilePackage.scripts.web, /--preserve-symlinks/,
		"RN-web must keep a browser-safe node_modules entry URL when dependencies live on a mounted artifact volume");
  assert.match(webLauncher, /EXPO_NO_METRO_LAZY\s*=\s*"1"/,
    "mounted dependencies must not leak filesystem-derived lazy chunk URLs into the browser");
  assert.match(metro, /resolver\.nodeModulesPaths/,
    "CI installs mobile/node_modules only, so sibling SDK source must resolve React from that workspace");
  assert.match(metro, /mobileNodeModules/);
  assert.match(metro, /realpathSync\(mobileNodeModules\)/,
    "Metro must include the physical target behind a mounted node_modules symlink in its file map");
  assert.match(metro, /disableHierarchicalLookup\s*=\s*false/,
    "nested npm dependencies must remain visible to Metro");
  assert.match(metro, /extraNodeModules/,
    "the shared SDK must pin React entrypoints to mobile/node_modules");
  assert.match(metro, /resolver\.resolveRequest/,
    "core runtime pins must override hierarchical lookup without disabling it");
  assert.match(metro, /react\/jsx-runtime/);
  assert.match(metro, /react-native/);
});

test("the iOS guest bridge keeps the shared Dogfood menu contract reachable", () => {
  assert.match(iosAppDelegate, /\?\? "floating-button"/,
    "the draggable Y must be the first-run guest default");
  assert.match(iosAppDelegate, /makeButton\(title: "Chat"/);
  assert.match(iosAppDelegate, /makeButton\(title: "Reload"/);
  assert.match(iosAppDelegate, /makeButton\(title: "Settings"/);
  assert.match(iosAppDelegate, /makeButton\(title: "Exit Dogfood"/);
  assert.match(iosAppDelegate, /showDogfoodMenu\(in: win\)/,
    "tapping Y must open the menu instead of skipping directly to chat");
  assert.match(iosDogfoodSettings, /\?\? "floating-button"/);
});

test("Metro pins core runtimes and preserves nested package resolution", () => {
  const config = require(join(mobile, "metro.config.js"));
  const resolved = [];
  const context = {
    resolveRequest(_context, moduleName, platform) {
      resolved.push({ moduleName, platform });
      return { type: "sourceFile", filePath: moduleName };
    },
  };

  config.resolver.resolveRequest(context, "react", "ios");
  assert.equal(resolved[0].moduleName, join(mobile, "node_modules", "react"));
  config.resolver.resolveRequest(context, "react-native", "ios");
  assert.equal(resolved[1].moduleName, join(mobile, "node_modules", "react-native"));
  config.resolver.resolveRequest(context, "react-native", "web");
  assert.equal(resolved[2].moduleName, join(mobile, "node_modules", "react-native-web"));
  config.resolver.resolveRequest(context, "semver/functions/satisfies", "ios");
  assert.equal(resolved[3].moduleName, "semver/functions/satisfies");
});

test("Metro removes Dogfood proxy mounts from Expo web HMR entry points", () => {
  const config = require(join(mobile, "metro.config.js"));
  const resolved = [];
  const context = {
    resolveRequest(_context, moduleName, platform) {
      resolved.push({ moduleName, platform });
      return { type: "sourceFile", filePath: moduleName };
    },
  };

  config.resolver.resolveRequest(context, "./dev/node_modules/expo-router/entry", "web");
  config.resolver.resolveRequest(context, "./d/device-1/dev-web/node_modules/expo-router/entry", "web");
  config.resolver.resolveRequest(context, "./peer/device-1/dev/node_modules/expo-router/entry", "web");
  config.resolver.resolveRequest(context, "./dev/node_modules/expo-router/entry", "ios");

  assert.deepEqual(resolved.map(({ moduleName }) => moduleName), [
    "./node_modules/expo-router/entry",
    "./node_modules/expo-router/entry",
    "./node_modules/expo-router/entry",
    "./dev/node_modules/expo-router/entry",
  ]);
});

test("More has one Dogfood destination for every contributor", () => {
  assert.doesNotMatch(more, /accessibilityLabel="Open Vibing"|>Vibing<|navigate\("\/vibing"/);
  assert.match(more, />Dogfood<\/Text>/);
  assert.doesNotMatch(more, />Dogfood Settings<\/Text>/);
  assert.match(more, /Launch, reload, tasks, and settings/);
  assert.match(more, /navigate\("\/\(tabs\)\/dogfood"/);
  assert.doesNotMatch(more, /isOwner\s*\?\s*\([\s\S]{0,500}Dogfood/);
});

test("Dogfood Settings and Dogfood Usage share a signed-in contributor gate", () => {
  assert.doesNotMatch(dogfood, /user\?\.isOwner|Owner access only|owner account/);
  assert.match(dogfood, /view === "settings" \? "Dogfood Settings" : "Dogfood"/);
  assert.match(dogfood, /<DogfoodNativeMenu/);
  assert.match(dogfood, /surface="settings"/);
  assert.match(dogfood, /surface="usage"/);
  assert.match(gate, /Launch opens the selected lane's live console before rendering the app/);
  const usageSurface = gate.match(/if \(surface === "usage"\) \{([\s\S]*?)\n  \}\n\n  return \(/)?.[1] || "";
  assert.doesNotMatch(usageSurface, /targetDevice\?\.name|checkoutLabel|startBehavior ===/,
    "the launch card must not repeat runtime inventory already available in Settings");
  assert.match(dogfood, /onOpenSettings=\{\(\) => router\.setParams\(\{ view: "settings" \}\)\}/);
  assert.match(dogfood, /management === "1"/,
    "developer administration must stay one level below Settings");
  assert.match(dogfood, /canonical main branch is protected/);
  assert.doesNotMatch(settings, /AttachModeSection/);
  assert.match(settings, /management: "1"/);
  assert.match(settings, /App testing &amp; approvals/);
  assert.doesNotMatch(rootLayout, /DogfoodCaptureHost|loadDogfoodMode/,
    "the retired screenshot catcher would silently keep the old meaning alive");
});

test("Dogfood can switch same-account devices and its Y stays outside the WebView", () => {
  assert.match(gate, /devices\.map/);
  assert.match(gate, /selectDevice\(device\)/);
  const match = /<WebView\s*\n/.exec(attached);
  assert.ok(match, "the Dogfood host must render its browser lane");
  const webViewEnd = attached.indexOf("/>", match.index);
  const webView = attached.slice(match.index, webViewEnd);
  assert.doesNotMatch(webView, /confirmDetach|Exit Dogfood mode/);
  assert.match(attached.replace(webView, ""), /<BrowserVibeBubble/);
  assert.match(attached.replace(webView, ""), /onExitPreview=\{confirmDetach\}/);
  assert.match(attached.replace(webView, ""), /onGoHome=\{goHome\}/,
    "the Y must return to native Dogfood without ending the session");
  assert.doesNotMatch(attached, /styles\.chrome|Dogfood mode<\/Text>/,
    "Dogfood should look like the real app, not an app inside a persistent host navigation bar");
  assert.match(attached, /onReload=\{\(kind\) => reloadDogfoodSurface\("manual", kind\)\}/,
    "the attached control must await its real reload instead of claiming success early");
  assert.match(attached, /A Dogfood reload is already in progress/,
    "a duplicate Fast Reload must be named rather than claimed as a second success");
  assert.match(attached, /DOGFOOD_WEBVIEW_LOAD_FAILED/,
    "an in-mode browser failure must carry a stable code, not only prose");
  assert.match(attached, /onHttpError=/);
  assert.match(attached, /DOGFOOD_WEBVIEW_HTTP_FAILED/,
    "HTTP failures must not paint a raw server error as if Dogfood succeeded");
  assert.match(attached, /parseDogfoodRenderMessage/);
  assert.match(attached, /parseDogfoodGuestException/);
  assert.match(attached, /DOGFOOD_EXCEPTION_CAPTURE_SCRIPT/);
  assert.match(attached, /reloadAttachedDogfoodBrowserLane/,
    "Fast Reload in attached Dogfood must ask the box to reload before remounting the WebView");
  assert.match(attached, /await reloadAttachedDogfoodBrowserLane\(deviceId, params\.workDir \|\| "", mode\)/,
    "attached Dogfood must post the exact checkout and requested reload mode before refreshing the WebView");
  assert.match(attached, /reportIssue\(guestException \? \{/,
    "captured guest exceptions must move to native Dogfood instead of covering the guest");
  assert.doesNotMatch(attached, /exceptionScrim|exceptionCard/,
    "a guest exception must not replace the app with an overlay card");
  assert.match(attached, /dogfoodExceptionFixPrompt/,
    "the exception fix task must receive the structured URL and stack evidence");
  assert.match(attached, /openTaskBus\.publish\(taskId\)/,
    "starting an exception fix must take the user to its live task chat");
  assert.match(attached, /onMessage=/);
  assert.doesNotMatch(attached, /<SafeAreaView/,
    "the browser app owns safe areas; a native SafeAreaView produces top and bottom bands");
  assert.match(attached, /contentInsetAdjustmentBehavior="never"/);
  assert.match(attached, /automaticallyAdjustContentInsets=\{false\}/);
  assert.match(attached, /scalesPageToFit=\{false\}/);
  assert.match(attached, /window\.localStorage\.setItem\("yaver\.secure\.yaver_theme"/,
    "the browser copy must use the installed app's theme so system bars and app pixels agree");
});

test("Dogfood launch shows the real runtime console before opening the app", () => {
  assert.match(launch, /DogfoodLiveConsole/,
    "launch must render the browser\/Hermes\/WebRTC output already retained by the root controller");
  assert.match(launch, /useDogfoodOverlay/);
  assert.match(launch, /: "browser";/,
    "a React Native launch with missing route state must use the browser lane");
  assert.match(launch, /Keep this open to follow live build logs/);
  assert.doesNotMatch(launch, /router\.replace\("\/\(tabs\)\/tasks" as any\)/,
    "launch must not erase its own logs by immediately redirecting to Tasks");
  assert.match(launch, /Open Dogfood/,
    "a ready runtime needs an explicit, named route into the rendered app");
  assert.doesNotMatch(launch, /Continue in Tasks/,
    "the launch screen already has Back; a second escape action crowds the preparation surface");
  assert.match(rootLayout, /<DogfoodOverlayProvider>/,
    "background preparation cannot survive navigation without a root owner");
  assert.match(overlay, /<BrowserVibeBubble/);
  assert.match(overlay, /snapshot\?\.phase === "ready"/,
    "the Y entry must not claim Dogfood is active while checkout, Expo, or browser proof is still running");
  assert.match(bubble, /<DogfoodEntryIcon/,
    "the root owner may expose only the shared Y over the running app");
  assert.match(overlay, /!nativeDogfoodOwnsControls/,
    "the native Dogfood menu must not receive a redundant Y over itself");
  assert.doesNotMatch(bubble, /Modal|StudioChatPane|KeyboardAvoidingView/,
    "the running app must not be covered by a second Dogfood interface");
  assert.match(overlay, /const workDir = typeof result\.metadata\?\.workDir === "string"/,
    "Fast Reload must prefer the checkout path resolved by the selected box");
  assert.match(overlay, /reloadAttachedDogfoodBrowserLane\(activeRequest\.deviceId, workDir, kind\)/,
    "Fast Reload from Tasks must reach the browser lane before it opens the attached preview");
  assert.match(overlay, /if \(kind && result\.lane === "browser"\)/,
    "opening Dogfood must not perform an unwanted second reload");
  assert.match(overlay, /controllerRef\.current !== controller \|\| requestRef\.current !== activeRequest/,
    "a replaced background preparation can still steal navigation");
  assert.match(overlay, /sessionStartedFrom: "vibing"/,
    "Tasks opened from Dogfood must stay pinned to the selected checkout and Vibing context");
  assert.match(overlay, /dir: workDir/,
    "Dogfood Tasks must edit the exact checkout prepared on this machine (main for the owner)");
  assert.doesNotMatch(overlay, /await controller\.handoff\(\)/,
    "opening or visiting Tasks must not transfer away the root cleanup that Exit Dogfood needs");
  assert.match(launch, /accessibilityLabel="Stop Dogfood launch"/,
    "an in-progress launch must have an explicit stop action");
  assert.match(overlay, /stopDogfoodDevLane/,
    "Stop must reach the agent operation rather than only clearing local state");
  assert.match(quic, /\/dev\/reload-app[\s\S]{0,600}155_000/,
    "Hermes builds must not inherit the generic 12-second request timeout");
});

test("Dogfood AI repair stays on the selected checkout when cloud placement is unavailable", () => {
  const start = attachClient.indexOf("export async function requestDogfoodFixWithAI");
  const end = attachClient.indexOf("export async function discoverYaverCheckout", start);
  assert.ok(start >= 0 && end > start, "Dogfood AI fix helper must remain inspectable");
  const helper = attachClient.slice(start, end);
  assert.match(helper, /true,\s*true,\s*\);/,
    "Dogfood AI fix must send codeMode=true and allowLocalFallback=true; otherwise placement can select an unready Cloud Workspace and discard the in-place repair route");
});

test("Dogfood Settings owns start, render, and durable session behavior", () => {
  assert.match(gate, /getDogfoodStartBehavior/);
  assert.match(gate, /getDogfoodRenderBehavior/);
  assert.match(gate, /getDogfoodSessionBehavior/);
  assert.match(gate, /"vibe-first", "render-on-open"/);
  assert.match(gate, /"manual", "auto-on-request"/);
  assert.match(gate, /"resume-last", "new-session"/);
  assert.match(gate, /startBehavior,/);
  assert.match(gate, /renderBehavior,/);
  assert.match(gate, /sessionBehavior,/);
  assert.match(gate, /const \[lane, setLane\] = useState<DogfoodLane>\("browser"\)/);
  assert.match(gate, /laneHydrated/,
    "Launch must wait for the persisted lane instead of racing a hardcoded fallback");
  assert.match(launch, /params\.lane === "webrtc" \|\| params\.lane === "hermes" \? params\.lane : "browser"/,
    "a missing or stale route parameter must resolve to Browser, never Hermes");
  assert.match(launch, /lane: requestedLane/,
    "the exact Settings choice must become the controller's launch lane");
  assert.match(gate, /accessibilityLabel="Runtime lane choices"/,
    "Browser, Hermes, and WebRTC choices must be visible directly in Settings");
  assert.match(overlay, /controller\.trigger\(\)/,
    "Dogfood no longer prepares in the background");
  assert.match(overlay, /next\.startBehavior === "render-on-open"[\s\S]{0,100}openPreparedPreview/,
    "vibe-first preparation still opens a renderer without explicit intent");
});

test("Yaver Chat Only, Reload Only, and combined modes survive every handoff", () => {
  assert.match(gate, /getDogfoodUsageMode/);
  assert.match(gate, /setDogfoodUsageMode/);
  assert.match(gate, /"chat-only", "reload-only", "reload-and-chat"/);
  assert.match(gate, /usageMode,/,
    "the selected UI mode must be part of the launch request");
  assert.match(overlay, /pathname: "\/remote-runtime"[\s\S]{0,260}usageMode/,
    "WebRTC navigation must preserve Reload Only");
  assert.match(overlay, /pathname: "\/attach"[\s\S]{0,300}usageMode/,
    "browser navigation must preserve Reload Only");
  assert.match(overlay, /<BrowserVibeBubble[\s\S]{0,500}onGoHome=\{goHome\}/,
    "the shared Y must return every mode to native Dogfood");
  assert.match(attached, /params\.usageMode === "chat-only" \|\| params\.usageMode === "reload-and-chat"/);
  assert.match(remoteRuntime, /usageMode=\{usageMode\}/);
  assert.match(nativeMenu, /dogfood-native-reload/);
  assert.match(nativeMenu, /dogfood-native-exit/);
  assert.match(nativeMenu, /Open Dogfood tasks/);
  assert.match(nativeMenu, /Open Dogfood settings/);
  assert.doesNotMatch(bubble, /Full Reload|StudioChatPane|reload-only-panel/,
    "mode-specific controls belong in native Dogfood, never over the guest");
});

test("attached Dogfood does not offer its own Yaver dev server as a guest card", () => {
  assert.match(tasks, /isAttachedDogfoodWebRuntime\(\)/);
  assert.match(tasks, /useDogfoodOverlay\(\)/,
    "native Tasks must read the root Dogfood lifecycle instead of relying on a WebView-local sentinel");
  assert.match(tasks, /dogfoodRuntime\.active\s*\|\|[\s\S]{0,100}isAttachedDogfoodWebRuntime\(\)/,
    "Tasks must suppress its duplicate preview controller on iPhone as well as RN-web");
  assert.match(tasks, /isEffectivelyConnected\s*&&\s*!attachedDogfoodRuntime/);
  assert.doesNotMatch(tasks, /isEffectivelyConnected\s*&&\s*<DevPreview/);
});

test("every browser guest owns one shared Y entry surface", () => {
  assert.doesNotMatch(devPreview, /showBrowserEscapeBar|browserEscapeLayer|Back from browser preview/,
    "DevPreview still paints a second Back, Reload, and Stop strip over a guest app");
  assert.match(devPreview, /<BrowserVibeBubble/,
    "browser guests must use the shared library control surface");
  assert.match(devPreview, /exitLabel=\{exitLabel\}/,
    "Tasks and Vibe previews cannot name the shared home destination");
  assert.match(projects, /exitLabel="Go to Projects"/,
    "Projects and SFMG previews do not identify their shared home destination");
  assert.match(bubble, /<DogfoodEntryIcon/);
  assert.doesNotMatch(entryIcon, /<Modal/);
  assert.match(entryIcon, /setDogfoodEntryIconHidden\(true, preferenceScope\)/,
    "the Y must be dismissible from inside itself");
});

test("attached Dogfood hides only its Yaver checkout and leaves other projects launchable", () => {
  assert.match(attached, /DOGFOOD_CHECKOUT_KEY/,
    "the outer host does not tell the inner app which verified checkout is Yaver");
  assert.match(projects, /attachedDogfoodCheckout\(\)/);
  assert.match(projects, /isPathInsideAttachedDogfoodCheckout/);
  assert.match(projects, /merged\.filter/,
    "the attached Yaver checkout is removed from the launchable Projects inventory");
  assert.match(projects, /!devServerBelongsToAttachedDogfoodCheckout/,
    "the running Yaver server must not crowd the Projects screen in Dogfood mode");
  assert.doesNotMatch(projects, /project\.name.*[Yy]aver/,
    "Dogfood filtering must use the verified path boundary, not a project-name guess");
});

test("Dogfood passes the active guest identity into Vibing", () => {
  assert.match(projects, /const activeProjectPath = dogfoodProjectRootPath\(devStatus\?\.workDir,/);
  assert.match(projects, /const guestProjectName = dogfoodGuestProjectName\(activeProjectPath \|\| devStatus\?\.workDir,/);
  assert.match(projects, /<BrowserVibeBubble[\s\S]{0,220}projectPath=\{activeProjectPath \|\| devStatus\?\.workDir\}[\s\S]{0,120}projectName=\{guestProjectName\}/);
  assert.doesNotMatch(bubble, /The SFMG preview stays available/);
});

test("an explicitly selected browser Dogfood lane is fail-closed until rendering is proved", () => {
  const attachClient = readFileSync(join(mobile, "src", "lib", "attachClient.ts"), "utf8");
  assert.match(attachClient, /prepareDogfoodMode/);
  assert.match(attachClient, /doctorBrowserLane\(client, 180, fetch, signal, browserViewport\)/,
    "Stop must interrupt the operation-level browser doctor too");
  assert.match(launch, /useWindowDimensions\(\)/,
    "Dogfood browser proof must use the requesting client's measured viewport");
  assert.match(launch, /deviceScaleFactor: PixelRatio\.get\(\)/,
    "a narrow desktop browser is not a phone; the device scale must cross the handoff too");
  assert.match(overlay, /next\.browserViewport/,
    "the measured client surface must reach the browser-lane doctor");
  assert.match(attachClient, /await stopAttachSession\(deviceId, session\.sessionId\)/,
    "a failed entry must revoke the partially minted capability");
  assert.match(attachClient, /DOGFOOD_PRIMARY_DISCONNECTED/);
  assert.match(attachClient, /resolveAgentPreviewUrl\(client\.baseUrl, bundlePath\)/,
    "the agent's relative browser path must retain the selected device's relay prefix");
  assert.match(attachClient, /waitForAgentPreviewRoute\(/,
    "Dogfood must probe the exact phone handoff URL instead of trusting only the box-local doctor");
  assert.match(attachClient, /DOGFOOD_RENDER_ROUTE_/,
    "a failed handoff route must stop entry with a stable code");
  assert.doesNotMatch(attachClient, /getDevServerBundleUrl\(bundlePath\)/,
    "Dogfood must not copy the owner bearer into a WebView URL");
  assert.match(attached, /const \[fixTaskId, setFixTaskId\] = useState/,
    "the visible route-to-fix task status must retain its state value");
});

test("Dogfood exposes framework-aware Browser, Hermes, and WebRTC lanes after checkout", () => {
  assert.match(gate, /dogfoodLanePlan\("expo"/);
  assert.match(gate, /useState<DogfoodLane>\("browser"\)/);
  assert.match(gate, /YAVER_DOGFOOD_APP_ID/);
  assert.match(gate, /getPreferredDogfoodLane/);
  assert.match(gate, /setPreferredDogfoodLane/);
  assert.ok(gate.indexOf('key={step.key}') < gate.indexOf('accessibilityLabel="Runtime lane choices"'),
    "runtime lane must follow machine, runner, and checkout readiness rows");
  assert.match(gate, /<DogfoodLanePicker/,
    "lane labels now belong to the shared SDK picker rather than the Yaver host");
  assert.match(gate, /fallbackLane=\{lanePolicy\.fallback\}/);
  assert.match(launch, /fallbackLane/);
  assert.match(overlay, /context\.project\.lane === "hermes"/);
  assert.match(overlay, /startDogfoodHermesLane/);
  assert.match(overlay, /context\.project\.lane === "webrtc"/);
  assert.doesNotMatch(overlay, /DOGFOOD_SELF_HERMES_UNSAFE/);
  assert.match(overlay, /prepareDogfoodMode/,
    "browser Dogfood must retain the proved attach/browser implementation");
  assert.match(overlay, /pathname: "\/remote-runtime"/,
    "WebRTC Dogfood must reuse the Projects native runtime surface");
  assert.match(attached, /<BrowserVibeBubble/,
    "browser Dogfood must expose Vibing and routing on the live surface");
  assert.match(remoteRuntime, /<BrowserVibeBubble/,
    "WebRTC Dogfood must expose Vibing and routing on the live surface");
  assert.match(remoteRuntime, /onGoHome=\{goHome\}/,
    "the Y must return WebRTC Dogfood to its native menu");
  assert.match(nativeMenu, /testID="dogfood-native-reload"/,
    "Fast Reload belongs on the stateful native menu");
  assert.match(nativeMenu, /testID="dogfood-native-exit"/,
    "Exit Dogfood belongs on the stateful native menu");
  assert.match(nativeMenu, /Fix in Tasks/,
    "a captured guest exception must retain a native recovery route");
});
