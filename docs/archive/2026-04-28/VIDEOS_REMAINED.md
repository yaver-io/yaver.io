# VIDEOS_REMAINED — 3 landing-page cuts we still need to shoot

The landing page (`web/app/page.tsx → DemoSection`) has three tabs. Tab 1
currently plays a real-but-rough reel of the Overnight Oats detail page
with its 3 intentional bugs visible. Tabs 2 and 3 are "Coming soon". This
doc is the shooting script for all three — **the v2 "Full Loop" cut
replaces what's there now, and tabs 2 + 3 become real for the first
time.**

Shoot together, record with Yaver's own tooling (`simctl io booted
recordVideo` for the iPhone simulator pane, macOS Screen Capture
`cmd+shift+5 → option` for the terminal pane).

All three are portrait (iPhone 16 Pro sim, 1206x2622 raw) with a side
terminal pane. Composite in post (Final Cut, DaVinci, or ffmpeg hstack).

---

## Video 1 — Full Loop (replaces current clip)

**What the current clip is:** 3:30 of raw simulator output — wizard
scaffold, pod install, xcodebuild, Bento booting, Overnight Oats
detail with the 3 bugs visible. Not bad, but not the story.

**What the new clip should be:** the literal "Yaver development of
Bento — end to end". ~90 seconds. Split-screen composite: phone-shaped
sim on the left, terminal with real logs on the right.

### Pre-shoot setup
- Docker Desktop running. `docker ps` responds.
- Fresh dev user: `curl -s $CONVEX/auth/signup` (see
  `demos/bento/.yaver-wizard-answers.json` for the answers we used the
  first time — keep them for consistency).
- `yaver set-runner claude` — runner configured.
- iPhone 16 Pro simulator booted and on the Home screen.
- Nuke any previous Bento state: `rm -rf ~/Library/Developer/CoreSimulator/Devices/*/data/Containers/Bundle/Application/*/Bento.app`
  then `xcrun simctl terminate booted io.yaver.bento`.
- `cd demos/bento` is where the wizard will land; alternatively use a
  fresh `/tmp/bento-fresh` parent dir so the on-camera target is
  pristine.

### Script (beat-by-beat, what each side shows)

```
0:00  OPEN — empty sim home screen / empty terminal
0:03  Terminal: `yaver new` (interactive) or `yaver new --quick ...`
      Phone: nothing yet
0:10  Terminal: wizard prompts stream through. Bento fields auto-answer.
0:20  Terminal: `✓ Generated /tmp/bento-fresh/bento` + 19 files list +
              `servicesStarted: true` (DOCKER MUST BE RUNNING for this)
0:25  Terminal: `cd bento/apps/mobile && npx expo run:ios`
0:28  Phone: Bento icon appears on the home screen
0:45  Phone: Bento launches — tabs visible, recipe grid rendering
1:00  Phone: tap Grocery tab → grouped items, then scroll to TOTAL
              → CRASH (null i.price → TypeError)
1:05  Terminal: BlackBox event stream in the corner shows the crash
1:10  Phone: shake gesture → Yaver Debug Console overlay
1:15  Phone: type "fix this crash" → agent streams output
1:30  Terminal: claude runs, edits GroceryTotal.tsx:47
              (price?.toFixed(2) ?? '0.00'), task completes
1:35  Phone: Gap-4 auto hot-reload fires, app re-mounts, Grocery total
             now renders "$7.50" (no crash)
1:45  Phone: navigate Home → Overnight Oats → scroll → Start Cooking
             → CookMode, timer starts
1:55  Phone: agent ALSO fixed CookTimer.tsx:12 in same session →
             Refrigerate step uses the 5:00 default
2:05  Terminal: `yaver deploy preview` output (tsc / lint / build / git
               all green)
2:15  Phone: Deploy button tap → confirmation → success state
2:20  End card: "Created. Built. Deployed. From your phone." + logo
```

### What to actually record

- Left pane (sim): `xcrun simctl io booted recordVideo --codec h264
  /tmp/bento-video/full-loop-phone.mov &` before 0:00; kill the recorder
  with `SIGINT` at 2:25.
- Right pane (terminal): `cmd+shift+5` → Record Selected Portion,
  select a 900×720 rect over iTerm/Terminal showing the live `yaver` +
  `claude` streams.
- Composite in post: `ffmpeg -i phone.mov -i terminal.mov -filter_complex
  hstack=inputs=2 final.mp4`.
- Target final: ≤15 MB h264, 30 fps, 1920×1080 portrait-inside-landscape.

### Known snags learned from last attempt

- **iOS "Open in Bento?" dialog** blocks every `simctl openurl` /
  `simctl launch` after the dev client registers its URL scheme. Fix:
  run a `simctl shutdown booted && simctl boot <UDID>` once before
  recording, and never use `openurl` on camera — only `launch` via
  bundle id.
- **cliclick** needs Accessibility permissions granted to Terminal
  (Settings → Privacy → Accessibility). Grant it BEFORE the shoot.
  Test on a clean sim window with `cliclick c:640,400` and watch for
  the click.
- **`expo run:ios`** generates a new iOS project every time
  `prebuild --clean` runs. If you need a clean xcworkspace mid-shoot,
  `rm -rf apps/mobile/ios` first, then run prebuild.
- **The 3 bugs must still be in place in `demos/bento`** before
  shooting. `grep INTENTIONAL demos/bento/apps/mobile/app/**/*.tsx`
  should return 3 hits. If zero, re-apply from
  `BENTO_BUILD_QUEUE.md` T3.2/3.3/3.4.

---

## Video 2 — Push & Fix (~60s)

**Story:** Developer pushes Bento to their real phone via
`yaver-cli push`. Shake reports a crash. AI fixes. Hot reload.

### Pre-shoot setup
- Real iPhone plugged in via USB, paired and trusted on the Mac.
- Yaver mobile app on that phone, logged into the same account as the
  Mac agent.
- Feedback SDK wired in `demos/bento/apps/mobile/app/_layout.tsx`:
  ```tsx
  import { YaverFeedback, BlackBox, FloatingButton } from "yaver-feedback-react-native";
  if (__DEV__) {
    YaverFeedback.init({ trigger: "shake" });
    BlackBox.start();
    BlackBox.wrapConsole();
  }
  // at the root: <YourApp /> ... {__DEV__ && <FloatingButton />}
  ```
  **This isn't done yet in `demos/bento` — see T4.2 in BENTO_BUILD_QUEUE.md.**

### Script

```
0:00  Terminal: `cd demos/bento/apps/mobile && npx yaver-cli push`
0:02  Terminal: "📡 Found: iPhone 15 … 🚀 Done in 4.1s"
0:05  Phone: Bento launches on the device (not sim!)
0:10  Phone: navigate to Grocery tab → CRASH (same null-price bug)
0:14  Phone: shake gesture → Debug Console overlay
0:18  Phone: tap "fix this crash" in the overlay
0:20  Terminal: task f82c streams — reading file, writing patch
0:40  Terminal: ✅ GroceryTotal.tsx updated, 1 file
0:42  Phone: auto hot-reload (gap 4 fires), Grocery re-renders clean
0:50  Phone: tap overlay → see the staged diff
0:55  End card: "Crash found. Fixed. Hot reloaded. 45 seconds."
```

### Blockers before this can shoot
- T4.2 Feedback SDK integration (~30 min of vibe-coding; will land
  once someone runs `claude -p "install yaver-feedback-react-native
  and wrap the app"` inside `demos/bento/apps/mobile/`).
- Real phone + USB + provisioning profile working for `expo run:ios
  --device <UDID>` OR a fresh TestFlight build.

### Recording approach
- **Phone**: external camera on a tripod, or use QuickTime Player →
  New Movie Recording → select iPhone as source (wired).
- **Terminal**: same `cmd+shift+5` region capture as Video 1.

---

## Video 3 — Auto Test (~75s)

**Story:** Developer taps "Test App" in the debug console. Agent
autonomously walks every screen of Bento, catches the 2 crashes
(null price + null duration), patches both, hot-reloads after each,
produces a report.

### Blockers before this can shoot
- **The autotest runner doesn't exist yet.** `yaver autotest bento
  <scenario.yaml>` is the planned CLI entry. See `BENTO_VIDEO_ROADMAP.md`
  "Video 3 MVP task list":
  1. Accessibility labels on Yaver host chrome (4h)
  2. `bento_autotest_cmd.go` wrapper that chains push → launch → walk
     scenario → watch BlackBox → create-task-on-crash → reload →
     resume → emit report (1–2 days)
  3. Scenario YAML for Bento (~2h)
  4. Mobile report screen (~half day)
  5. `/autotest/*` HTTP endpoints (~2h)
- Estimate: **3–4 days of focused work** to make this shootable.
- The fully-autonomous planner (LLM reads accessibility tree, picks
  next tap) is an additional 6–8 weeks — don't build it for this
  shoot; the scripted scenario version is enough.

### Script once the MVP lands

```
0:00  Phone: Bento running on iPhone sim, Yaver debug overlay visible
0:05  Phone: tap [Test App] in the overlay
0:08  Phone: agent takes over — Home tab scrolls, taps Teriyaki Bowl
0:15  Phone: back to Home, taps Overnight Oats
0:20  Phone: taps Start Cooking → CookTimer crashes
0:22  Terminal: ⚠ CRASH at CookMode.tsx:112 captured
0:28  Terminal: agent patches (duration ?? 300), task completes
0:30  Phone: hot reload, resumes scenario
0:35  Phone: agent navigates to Grocery tab → CRASH #2
0:40  Terminal: ⚠ CRASH at GroceryTotal.tsx:47 captured
0:45  Terminal: agent patches (price?.toFixed), task completes
0:50  Phone: hot reload, Grocery renders clean
0:55  Phone: agent continues — Favorites, Profile, all pass
1:05  Phone: report screen: "6 screens tested, 2 crashes, 2 fixed"
1:10  Phone: [Accept All] → diff appears
1:15  End card: "Zero human intervention."
```

---

## Shoot-day checklist (all 3)

Run this on the recording Mac before rolling:

```bash
# 1. Docker
docker ps >/dev/null || open -a Docker && sleep 30

# 2. Yaver
yaver status | grep "Auth:" | grep -q valid || {
  echo "not authed — run: yaver auth"
  exit 1
}
jq -r '.runner' ~/.yaver/config.json | grep -qE "claude|aider|codex" || {
  echo "no runner set — run: yaver set-runner claude"
  exit 1
}

# 3. Bento bugs intact
grep -rq "INTENTIONAL" demos/bento/apps/mobile/app/ || {
  echo "bento bugs missing — re-run BENTO_BUILD_QUEUE.md T3.2/3.3/3.4"
  exit 1
}

# 4. Simulator clean boot
SIM_UDID=$(xcrun simctl list devices | grep "iPhone 16 Pro " | grep -v Max | head -1 | sed -E 's/.*\(([A-F0-9-]+)\).*/\1/')
xcrun simctl shutdown "$SIM_UDID" 2>/dev/null
xcrun simctl boot "$SIM_UDID"
open -a Simulator

# 5. cliclick perms check (Terminal needs Accessibility)
cliclick p  # prints mouse pos — if it errors, grant perms

# 6. ffmpeg + screencapture
which ffmpeg >/dev/null || brew install ffmpeg
```

## Post-shoot pipeline

```bash
# Compress: 720px wide, h264 CRF 30, veryslow preset, no audio.
# Target: ≤5 MB per tab.
ffmpeg -i raw.mov -vcodec libx264 -crf 30 -preset veryslow \
  -vf scale=720:-2,fps=30 -an out.mp4

# Upload to GitHub Releases (not Vercel — cost rule).
gh release create bento-demo-v2 out.mp4 \
  --repo kivanccakmak/yaver.io \
  --title "Bento demo footage v2"

# Wire in landing. web/app/page.tsx DEMO_TABS entry `video:` field
# points at releases.../bento-demo-v<N>/<file>.mp4. Bump v<N> each
# reshoot so the old URL keeps working.
```
