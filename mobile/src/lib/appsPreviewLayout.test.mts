// Source-level guard for the compact Projects/Preview UX. These assertions
// deliberately target the shipped apps.tsx surface: the regressions were
// native layout composition defects, not helper-function mistakes.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const mobileRoot = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const src = readFileSync(join(mobileRoot, "app/(tabs)/apps.tsx"), "utf8");
const sharedPreviewSrc = readFileSync(join(mobileRoot, "src/components/DevPreview.tsx"), "utf8");
const buildsSrc = readFileSync(join(mobileRoot, "app/(tabs)/builds.tsx"), "utf8");
const studioSrc = readFileSync(join(mobileRoot, "app/vibe-studio.tsx"), "utf8");
const studioConfigSrc = readFileSync(join(mobileRoot, "app/studio-config.tsx"), "utf8");
const studioTerminalSrc = readFileSync(join(mobileRoot, "src/components/studio/StudioTerminalPane.tsx"), "utf8");

assert.match(src, /<LaneStartupStatus[\s\S]{0,800}lines=\{webPreviewLogs\}/,
  "the first-glance browser wait must show the newest dev-server log lines");
assert.match(src, /<LaneStartupStatus[\s\S]{0,900}emptyText=/,
  "the first-glance browser wait must label an empty log stream honestly");
assert.doesNotMatch(src, /webPreviewLogs\.length > 0 && !webRuntimeLogOpen/,
  "preview output must have one log surface, not a second inline log box");
assert.doesNotMatch(src, />Preview logs</,
  "the single expanded surface should use the concise title Logs");
assert.doesNotMatch(src, />Show logs</,
  "the wait card must not duplicate the floating Logs entry point");
assert.doesNotMatch(src, /setWebRuntimeLogOpen\(true\);[\s\S]{0,180}browser lane after/,
  "the browser doctor must not automatically cover the preview with logs");
assert.doesNotMatch(src, /\(coming soon\)/,
  "supported Hermes reload must not be described as coming soon");
assert.match(src, /openBtn:\s*\{[^\n]*flex:\s*0/,
  "Open must remain a compact action rather than fill the card");
assert.match(src, /<Text style=\{s\.openBtnText\}>Open<\/Text>/,
  "the compact Projects preview action must use the readable Open label");
assert.match(src, /actionBtn:\s*\{[^\n]*minHeight:\s*36[^\n]*alignItems:\s*"center"[^\n]*justifyContent:\s*"center"/,
  "the Projects preview actions must stay short and center their labels");
assert.match(src, /openBtn:\s*\{[^\n]*minWidth:\s*72/,
  "the Open action must be wide enough to remain readable without filling the card");
assert.match(src, /cardActions:\s*\{[^\n]*alignItems:\s*"center"[^\n]*justifyContent:\s*"flex-start"/,
  "Open and Stop must stay left aligned as one compact action group");
assert.match(src, /stopBtn:\s*\{[^\n]*minWidth:\s*72/,
  "Open and Stop must have matching compact widths");
assert.match(src, /filterRow:\s*\{\s*height:\s*38/,
  "the chip ScrollView must be taller than its 34pt selected chips");
assert.match(src, /previewWaitLine/,
  "the Projects preview must use the shared wait narration contract");
assert.doesNotMatch(src, /quicClient\.getDevServerBundleUrl|doctorBrowserLane\(quicClient/,
  "the iOS preview URL and doctor must use the selected box's pooled client");
assert.match(src, /reconcilePreviewDevStatus\(previous, status, showWebViewRef\.current\)/,
  "a transient status fetch failure must not unmount an open iOS WebView");
assert.doesNotMatch(sharedPreviewSrc, /quicClient\.getDevServerBundleUrl|doctorBrowserLane\(quicClient/,
  "the second preview implementation must use the same selected-box client");
assert.match(sharedPreviewSrc, /reconcilePreviewDevStatus\(previous, s, true\)/,
  "the second preview implementation must preserve its WebView on a transient poll failure");
assert.match(sharedPreviewSrc, /openBtn:\s*\{[\s\S]{0,120}flex:\s*0/,
  "the shared Open in Yaver action must stay compact like Stop on every client width");
assert.match(sharedPreviewSrc, /const openLabel =[\s\S]{0,160}: "Open";/,
  "the shared preview must use the concise Open label");
assert.match(sharedPreviewSrc, /cardActions:\s*\{[^\n]*alignItems:\s*"center"[^\n]*justifyContent:\s*"flex-start"/,
  "the shared preview actions must stay left aligned");
assert.match(src, /backgroundColor:\s*isDark\s*\?\s*c\.successBg\s*:\s*c\.surfaceMuted/,
  "the Projects running-preview card must use a soft light surface instead of a dark hardcoded fill");
assert.match(sharedPreviewSrc, /backgroundColor:\s*isDark\s*\?\s*c\.successBg\s*:\s*c\.surfaceMuted/,
  "the Tasks running-preview card must use the same theme-aware light surface");
assert.doesNotMatch(src, /activeCard:\s*\{[\s\S]{0,120}backgroundColor:\s*"#0f1a0f"/,
  "the Projects running-preview card must not force its dark palette in light mode");
assert.doesNotMatch(sharedPreviewSrc, /activeCard:\s*\{[\s\S]{0,120}backgroundColor:\s*"#0f1a0f"/,
  "the Tasks running-preview card must not force its dark palette in light mode");
assert.match(buildsSrc, /backgroundColor:\s*isRunning\s*\?\s*c\.surfaceMuted\s*:\s*c\.bgCard/,
  "the legacy project card must keep the same theme-aware running surface");
assert.doesNotMatch(src, /\[preview\] log stream unavailable/,
  "an optional log-stream failure must not be presented as a preview failure");
assert.match(src, /subscribeProjectPreviewOutput\(previewClient/,
  "the Projects preview must use the shared Dogfood\/Projects output lane");
assert.match(readFileSync(join(mobileRoot, "src", "lib", "projectPreviewRuntime.ts"), "utf8"), /client\.subscribeDevEvents/,
  "the shared Dogfood\/Projects lane must retain reconnecting dev-events transport");
assert.match(sharedPreviewSrc, /previewClient\.subscribeDevEvents/,
  "the shared preview must use the reconnecting shared dev-events lane");
assert.match(src, /project:\s*usable\?\.workDir\s*\|\|\s*currentProject\?\.path\s*\|\|\s*runningProject/,
  "tablet Studio handoff must prefer the authoritative workDir over a display label");
assert.match(studioSrc, /requestedProject\.split\(\/\\s\+\\\/\\s\+\/\)\[0\]/,
  "Studio must tolerate legacy project / surface display-label links");
assert.match(studioSrc, /Promise\.all\(\[[\s\S]{0,180}listProjects\(true\)[\s\S]{0,180}getDevServerStatus\(\)/,
  "Studio project resolution must probe the running preview, not trust discovery inventory alone");
assert.match(studioSrc, /servingStatus\?\.workDir[\s\S]{0,300}mapped\.unshift/,
  "a serving workDir missing from discovery must remain selectable in Studio");
assert.match(studioSrc, /router\.push\("\/studio-config"/,
  "Studio must expose only the separate configuration route");
assert.match(studioConfigSrc, /Studio Configuration[\s\S]*Machine[\s\S]*Project[\s\S]*Lane[\s\S]*Runner inside SSH/,
  "the separate Studio configuration page must own machine, project, lane, and runner controls");
assert.match(sharedPreviewSrc, /onLogStateChange\(\{[\s\S]{0,180}lines:[\s\S]{0,180}live:/,
  "the shared preview must publish its existing bounded log state to its host");
assert.match(studioSrc, /resolveStudioDefaults/,
  "Studio must restore capability-checked per-project defaults before launch");
assert.match(studioSrc, /useState\(0\.3\)/,
  "Studio must default to a narrow 30 percent lane");
assert.doesNotMatch(studioSrc, /StudioChatPane/,
  "Studio's right side is SSH, never a chat pane");
assert.match(studioSrc, /<RealDevicePane projectPath=\{project\.path\} framework=\{project\.framework \|\| "react-native"\}/,
  "the Device source must stream the selected project's physical device beside SSH");
assert.match(studioSrc, /<View style=\{\[styles\.rightPane,[\s\S]{0,240}<StudioTerminalPane/,
  "the right side of landscape Studio must remain the SSH terminal when lanes change");
assert.match(studioTerminalSrc, /<XtermView/,
  "Studio SSH must use the full VT terminal renderer for tmux and coding TUIs");

console.log("Projects preview layout contract ok");
