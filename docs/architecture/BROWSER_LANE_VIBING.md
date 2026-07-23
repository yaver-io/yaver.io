# Browser-lane vibing — how it works & how to test it

Reverse-engineer the code before trusting this doc (see `CLAUDE.md`). Everything
below was verified end-to-end on 2026-07-24 against the live agent on this Mac
using a real headless Chrome — the screenshots and HTTP codes are from that run,
not from memory.

## What the browser lane is

Three preview lanes exist. The user picks one from a project's action sheet:

| Lane | Who | Mechanism |
|---|---|---|
| **Hermes** | RN / Expo only | agent builds a Hermes bytecode bundle, phone loads it into the Yaver container |
| **Browser** | **all stacks** | agent serves the app's **web target**; phone shows it in a WebView through the `/dev/` proxy |
| **WebRTC** | all stacks | agent runs the app **natively** on a simulator/emulator on the box and streams pixels |

The **browser lane** is the universal, no-native-install path. It only works for
**web-compatible apps** — see "Native-only apps" below.

## The path a Browser Reload takes

```
mobile: tap "Browser Reload"
  → quicClient.startDevServer({ framework, workDir, platform:"web", caller:"web-ui" })
  → agent POST /dev/start  (devLane.ts::browserLaneStartBody = {platform:"web",caller:"web-ui"})

agent (desktop/agent/devserver.go):
  flutter → `flutter run -d web-server --web-port 9100`   (case "web","chrome","web-server")
            (auto-runs `flutter create --platforms web .` if there is no web/ dir)
  expo/RN → Metro web target
  next/vite → next dev / vite
  → serves on :9100, exposed to the phone via the agent's /dev/ reverse proxy

mobile: the preview WebView loads  <agentBaseUrl>/dev/  (relay adds ?token=&__rp=)
  → PREVIEW_READY_SCRIPT (src/lib/previewReadyScript.ts) confirms the app PAINTED
  → progress overlay lifts, the real app shows
```

Key files:
- Agent: `desktop/agent/devserver.go` (Flutter web spawn + readiness),
  `devserver_progress.go` (stdout → summarized phase events).
- Mobile: `mobile/app/(tabs)/apps.tsx` (Projects preview + lanes),
  `mobile/src/components/DevPreview.tsx` (Tasks preview),
  `mobile/src/lib/previewReadyScript.ts` (the "has it painted?" probe),
  `mobile/src/lib/devLane.ts` (lane bodies + `mustUseNativePreview`).

## Signalling: what the phone shows while it starts

The agent parses the dev server's stdout into **summarized phases** (not a raw
log firehose) and streams them on `/dev/events`:

- Flutter: `installing_deps` (pub get) → `launching` → `compiling` → `ready`
  (`rxFlutter*` in `devserver_progress.go`).
- Metro/Expo: `metro_bundling` → real `%` from `iOS … 44% (667/1025)` → `ready`.

The mobile preview renders a **starting panel** (command + phase + % + a live log
tail) until the probe confirms paint. On failure it renders a **failure panel**
with the real captured logs + **Retry / Restart / Fix in Yaver**.

`Fix in Yaver` dispatches the framework + workDir + captured output to a runner as
a coding task ("diagnose the root cause… fix so it builds and serves").

## How to test it on THIS machine (closed loop, no phone)

You need the agent running (`yaver serve` on :18080) and its auth token:

```bash
TOK=$(node -e 'console.log(require("fs").readFileSync(process.env.HOME+"/.yaver/config.json","utf8").match(/"auth_token":\s*"([^"]+)"/)[1])')
```

### 1. Start the dev server exactly as the phone does

```bash
curl -s -X POST http://127.0.0.1:18080/dev/start \
  -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -d '{"framework":"flutter","workDir":"/ABS/PATH/TO/app","platform":"web","caller":"web-ui"}'
```

### 2. Confirm it serves — AND that assets load through the PROXY

A black screen is almost always a proxy 404 on assets, not "not serving". Check
both the direct port and the `/dev/` proxy:

```bash
for a in "" main.dart.js flutter.js flutter_bootstrap.js assets/AssetManifest.json; do
  D=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:9100/$a")
  P=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:18080/dev/$a" -H "Authorization: Bearer $TOK")
  printf "/%-28s direct=%s proxy=%s\n" "$a" "$D" "$P"
done   # every one must be 200 on BOTH columns
```

### 3. Render it in a real browser + screenshot (proves paint, not just serve)

Use the Yaver MCP browser tools (headless Chrome on the box):

```
browser_open      { session_id: "vibe" }
browser_navigate  { session_id: "vibe", url: "http://127.0.0.1:9100/" }   # returns a screenshot
```

Flutter boots when `flutter-view` / `flt-glass-pane` exist; a page that only
paints its background color = the app is stuck in its own `main()` (see below).

### 4. Prove the VIBE loop (edit → hot reload → see it)

```bash
# edit a visible string in the app's source, then:
curl -s -X POST http://127.0.0.1:18080/dev/reload \
  -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" -d '{"mode":"dev"}'
# re-navigate the browser → the change is on screen
```

Verified on `yaver-todo-flutter`: title edit → hot reload → the new title
rendered in the browser. Full loop works.

## Flutter gotchas found the hard way (2026-07-24)

1. **Missing pubspec asset kills the compile silently.** `e-mobile`'s
   `pubspec.yaml` declares `.env` as an asset but the file was absent →
   `flutter run -d web-server` fails to compile → never serves → black screen.
   The agent's readiness loop now **early-exits on process death** and returns
   the **output tail**, so the failure panel shows *"No file or variants found
   for asset: .env"* instead of a blank 180s timeout.

2. **Native-only apps hang in `main()` on web.** `e-mobile` calls
   `Firebase.initializeApp()` + `FCMService().initialize()` — FCM needs a service
   worker + web push config it doesn't ship, so on web those **hang** and the app
   never leaves its splash (engine boots and paints the background, no widgets).
   The browser lane is fine; the *app* isn't web-ready.

   Fix pattern (kept on a **branch**, never production `main`):
   ```dart
   import 'package:flutter/foundation.dart' show kIsWeb;
   if (!kIsWeb) { await Firebase.initializeApp(); await FCMService().initialize(); }
   // connectivity_plus DOES support web — run the foreground service everywhere,
   // guard only the native background isolate, or the app boots into its
   // "no connection" overlay on web.
   ```
   With those guards `e-mobile` rendered its real **login screen** in the browser
   lane. `kIsWeb` is `false` on iOS/Android, so native (Firebase, FCM, camera,
   BLE, background tasks) is byte-for-byte unchanged.

## RN / Expo-web gotcha

Expo Web's `index.html` ships **three** body children (`<noscript>`, `<div
id="root">`, `<script defer>`) *before* React mounts. A naive "body has children"
probe declared the page rendered instantly and lifted the overlay onto an empty
`#root`. `previewReadyScript.ts` fixes this: for a known SPA mount point it
requires `#root`/`#app` to have a **child** (react-dom committed), Flutter keeps
its markers, plain-web keeps the old heuristic. It also ignores the agent's own
`{"status":"starting"}` 503 body so the placeholder never reads as "rendered".

## Flutter has the SAME premature-ready bug — it just wears a splash (2026-07-24)

The RN section above says "Flutter keeps its markers", which reads as *Flutter is
handled*. It is not. Walk the predicate against a **live** Flutter web page
(fetched from `flutter run -d web-server`, e-mobile on :9100):

```html
<body>
  <picture id="splash"> … </picture>                      ← element child 1
  <script> _flutter.loader.loadEntrypoint(…) </script>     ← element child 2
</body>
```

At document-end, in order:

1. `flutter-view` / `flt-glass-pane` / `flt-scene-host` — **absent**. The engine
   creates those only after `main.dart.js` downloads and CanvasKit boots.
2. `#root` / `#app` — **absent**. Flutter has no SPA mount point, so the branch
   that fixes RN never applies to it.
3. Legacy fallback → `body.children.length > 1` → **2 > 1 → TRUE**.

So Flutter fires "rendered" prematurely by exactly the same mechanism as
Expo-web did. The `#root` fix does not cover it, because there is no `#root`.

**Why nobody filed it:** Flutter ships a splash. Lifting the overlay early
reveals the app's own splash image rather than a blank void, so it reads as a
plausible loading state. That makes it less severe than the RN case, not absent —
two consequences survive:

- A Flutter app that fails to boot **shows its splash forever**: the predicate
  latched on the splash, the overlay is gone, the page returned 200 so the retry
  counter never increments, and no error is ever shown. Same dead end as RN, just
  prettier. This is very likely what "never leaves its splash" in the FCM note
  above actually looks like to a user.
- Yaver's own progress UI — startup steps, %, live logs, the failure panel with
  Retry — is dismissed before the app starts, so for Flutter it is never seen.

**The fix, when the Flutter owner wants it** (one line in branch 3 — deliberately
NOT applied here, because it changes Flutter behaviour and this lane is owned
elsewhere):

```js
// still booting: a splash is up and the engine has not attached yet
if (doc.getElementById('splash') || doc.querySelector('#flutter_service_worker')) return false;
```

**Measure before shipping it.** If a project removes or replaces its splash,
`children.length` can drop to 1 and the fallback flips to the *opposite* failure
(overlay never lifts) — which is the bug the Flutter side has presumably already
been fighting. `e2e/rn-browser-loop.mjs` takes any export directory and prints
the `t0`/`t1` `body=` / `#root=` / `OLD=` / `NEW=` numbers directly, so a Flutter
export can be measured the same way the three RN projects were:

```
sfmg   t0  body=3 #root=0  OLD=true  NEW=false
talos  t0  body=3 #root=0  OLD=true  NEW=false
yaver  t0  body=3 #root=0  OLD=true  NEW=false
```

## Decision guide

- App is a **web-ready** Flutter/RN/Next/Vite app → **Browser lane**. Fast, no
  native build, hot-reload vibing.
- App uses **native-only startup** (Firebase/FCM, platform channels, camera/BLE
  in init) and you don't want `kIsWeb` guards → **WebRTC lane** (runs it natively
  on a simulator on the box). This is the correct lane for `e-mobile` as-is.

A worthwhile product improvement: have the agent detect native-only init and
steer the user to WebRTC instead of a browser lane that will only show a splash.
