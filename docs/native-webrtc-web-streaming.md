# Native WebRTC + Web Viewer Streaming — Deep Analysis

> **Status: design doc, partly implemented (see §16). The Go agent owns
> the entire pipeline — capture, encode, RTP, signaling, control. The
> mobile app stays untouched in this iteration.** Code is the source of
> truth. Re-grep before acting on any path cited below.
>
> **Hard rule (do not violate): the React Native / Expo Hermes path is
> not modified by anything in this document.** RN apps continue to load
> via Hermes guest bundles into the Yaver mobile super-host. WebRTC
> streaming never replaces, augments, or shadows the Hermes flow. If a
> change appears to touch RN code paths, it is the wrong change — back
> it out.

## 1. Goal

Make Swift / Kotlin / Flutter native projects first-class in Yaver,
streamed to the developer through a **browser-based web viewer** (in the
existing Next.js dashboard), not a custom desktop app. Three reasons:

1. The browser already has a hardware-accelerated H.264 decoder, native
   `<video>` rendering, native pointer events, and native IME. We get
   for free what GStreamer + GTK would cost weeks to glue together.
2. Yaver already ships a web dashboard at `web/`. Adding a viewer there
   is a new component, not a new binary.
3. Same mental model as the existing `WebView` for RN preview: the user
   thinks "open the project, see it in a tab", regardless of whether
   it's a Vite app or a SwiftUI app.

| Stack | Today | Target |
|---|---|---|
| **Expo / React Native** | Hermes guest bundle pushed into the Yaver mobile super-host. Hot reload via `/dev/build-native`. | **Unchanged. Do not touch.** |
| **Native iOS (Swift)** | `RemoteRuntimeManager` boots a sim, JPEG-polls at ~1.4 FPS over a WebRTC DataChannel. Only macOS hosts. | Real RTP H.264 video track at 24+ FPS. Linux developers dispatch the build to a paired remote-mac builder; sim UI streams back into the web viewer. |
| **Native Android (Kotlin)** | `RemoteRuntimeManager` boots an emulator, JPEG-polls, taps via `adb`. Linux + macOS hosts. | Real RTP H.264 captured on Linux directly from the emulator (`adb exec-out screenrecord --output-format=h264 -`); web viewer renders + injects taps. |
| **Flutter** | Build-only support. `executionModeForFramework("flutter")` returns `Unsupported`. | Promote to `ExecutionModeNativeWebRTC`. Reuses the Android emulator + iOS simulator targets. |
| **Mobile app** | Hermes everything. | Unchanged. Native-WebRTC projects show a `WebRTC` badge on the project card with a copy-able dashboard URL — the phone never opens a peer connection. |

The viewer surface is a **new tab in the web dashboard** that holds a
`<video>` element wired to an `RTCPeerConnection`, with pointer + key
handlers that translate to the device's input system. `web/` consumes the
stream; `desktop/agent/` produces it. That's the entire architecture.

## 2. What already exists (do not rebuild)

| Subsystem | Where | What it gives us |
|---|---|---|
| Session manager | `desktop/agent/remote_runtime.go` | Capabilities probe, target enable/disable, session CRUD, host-class detect (`macos-ios` / `linux-android`). |
| Pion WebRTC | `desktop/agent/remote_runtime_webrtc.go` | `webrtc.NewPeerConnection`, `frames` + `events` DataChannels, JPEG capture loop @ 700 ms. SDP offer/answer mounted at `/remote-runtime/sessions/<id>/webrtc/offer`. Tap/text/back/home control. |
| Sim/emu drivers | `github.com/yaver-io/agent/testkit` | `IOSSimDriver` + `AndroidEmuDriver`: boot, screenshot (PNG), tap, send-text, key-event. |
| Recording paths | `desktop/agent/recording_iossim.go` (xcrun simctl recordVideo --codec=h264) and `recording_androidemu.go` (adb shell screenrecord 2 Mbps) | Continuous H.264 MP4 capture pipelines. **The backbone the new RTP video track plugs into.** |
| Pion deps | `go.mod` includes `pion/webrtc/v4`, `pion/turn/v4`, `pion/srtp/v3`. | Everything for RTP video tracks already vendored. |
| npm postinstall | `cli/src/postinstall.js` | Already calls `yaver install remote-runtime` on global install (line 307). The dep-bootstrap hook exists; we just make sure it covers H.264 capture deps. |
| `yaver install` registry | `desktop/agent/install_cmd.go::runRemoteRuntimeInstall` (line 818) | Installs `android-sdk` everywhere; `xcodegen + cliclick` on macOS. We extend this list, not invent a new one. |
| Existing viewers | `web/components/dashboard/RemoteRuntimeViewer.tsx` (180 lines, JPEG-DC). `mobile/app/remote-runtime.tsx` (390 lines, JPEG-DC). | Web becomes the new RTP viewer; mobile becomes a badge-only display. |
| Privacy boundary | `desktop/agent/convex_privacy_test.go` | Already enforces "no frame bytes / no path leaks". Forbidden-keys list is the seam to extend. |

## 3. Four gaps

1. **Polled JPEGs on a DataChannel are not video.** Use a real RTP track.
2. **Web viewer renders poorly and only over JPEG-DC.** Rewrite to consume an RTP video track via `RTCPeerConnection.ontrack` → `<video srcObject={stream} />`.
3. **Flutter is filed under `Unsupported`.** It builds fine via `yaver flutter`, but `RemoteRuntimeManager` won't open a session for it. 5-line oversight.
4. **No screen-size protocol.** RTP tracks are continuous — the viewer needs the device's logical width/height/rotation out-of-band so it can scale tap coordinates correctly.

## 4. Framework dispatch rules (the auto-WebRTC contract)

Encode the rule directly in `executionModeForFramework`:

```go
func executionModeForFramework(framework string) ProjectExecutionMode {
    switch strings.ToLower(strings.TrimSpace(framework)) {
    case "expo", "react-native":
        return ExecutionModeRNHermes        // hot-reload only, never WebRTC
    case "next", "nextjs", "vite", "react":
        return ExecutionModeWebWebview      // browser preview / vibe-preview
    case "swift", "kotlin", "flutter":      // ← flutter joins the WebRTC family
        return ExecutionModeNativeWebRTC
    default:
        return ExecutionModeUnsupported
    }
}
```

Downstream: any UI surface (mobile, web, agent CLI) reads `executionMode`
and branches. The mobile Hot Reload tab keeps showing RN/Expo only. The
web dashboard adds a Streaming tab for `native-webrtc` rows. RN never
sees a WebRTC affordance and never produces an SDP offer.

## 5. Topology

```
┌──────────────────────────────────────────────────────────────┐
│ Web dashboard  (Next.js, served by Cloudflare or `next dev`) │
│ ──────────────────────────────────────────                    │
│ RemoteRuntimeViewer.tsx                                      │
│   • RTCPeerConnection (browser-native)                       │
│   • <video srcObject={inboundStream} />                      │
│   • onpointerdown / move / up → tap, swipe events            │
│   • input → text events                                      │
│   • status overlay (FPS, bitrate, builder)                   │
└──────────┬───────────────────────────────────────────────────┘
           │ signaling: HTTP (offer/answer)
           │ media:    RTP H.264 over SRTP, ICE-routed
           ▼
┌──────────────────────────────────────────────────────────────┐
│ Linux dev box  (yaver agent, Go)                              │
│ ──────────────────────────────────────                        │
│ RemoteRuntimeManager                                         │
│   ├ session lifecycle (TeamViewer-style)                     │
│   ├ video track encoder:                                     │
│   │     adb exec-out screenrecord --output-format=h264 -     │
│   │  OR xcrun simctl io booted recordVideo --codec=h264 -    │
│   │     ↓                                                    │
│   │     pure-Go H.264 NAL extractor (h264_extract.go)        │
│   │     ↓                                                    │
│   │     Pion TrackLocalStaticSample.WriteSample              │
│   ├ events DataChannel (taps, text, dims, swipe, throttle)   │
│   ├ Android emulator: local                                  │
│   └ iOS simulator: local on macOS host, OR dispatched to a   │
│     paired remote-mac builder for Linux developers           │
└──────────────────────────────────────────────────────────────┘
                                ▲
                                │ relay (QUIC + TURN, when direct fails)
                                ▼
                        ┌──────────────┐
                        │ Yaver relay  │
                        └──────────────┘

┌────────────────────────────┐
│ Mobile app  (UNCHANGED)    │
│   • RN/Expo Hermes super   │
│     host, hot reload       │
│   • Native projects show a │
│     `WebRTC` badge + a     │
│     "open in web dash" URL │
│   • Never opens an SDP     │
└────────────────────────────┘
```

ICE candidates flow: STUN-discovered host first, then mDNS, then a TURN
candidate from the relay. When ICE finds a direct path, RTP runs P2P;
otherwise it relays. The JPEG-over-DataChannel path stays alive as a
last-ditch fallback through the existing relay tunnel.

## 5a. Surface split — who talks to whom

| Surface | What it does for native-WebRTC projects | What it does for RN/Hermes projects |
|---|---|---|
| **Web dashboard (THE viewer)** | The viewer. Browser RTCPeerConnection negotiates with the agent, decodes H.264 via the platform decoder, paints the device, captures clicks/keys, sends them back over the events DataChannel. | Existing project list + dev-server preview. Unchanged. |
| **Mobile app** | Shows a badge `WebRTC` on the project card with the device dims, current viewer (if any), and a deep-link to the web dashboard. **Never** opens a peer connection. | Hot Reload tab + super-host bundle loading. **Unchanged.** |
| **Go agent (Linux/Mac)** | Orchestrates: capture, encode, RTP, signaling, control. Never renders. | `/dev/*` endpoints exactly as today. |

## 5b. The TeamViewer-style session contract

1. **One device, one active viewer at a time.** A second offer takes over the session and gracefully boots the prior viewer (it sees a `taken-over` event and disconnects).
2. **Pointer maps 1:1 to a device tap.** Press is press, drag is swipe, key is text. No simulated hovering.
3. **Heartbeat with auto-reconnect.** Every 5 s the viewer sends `ping` over the events DataChannel. Three missed pings → tear the PC down and re-offer. The simulator stays booted across reconnects.
4. **Session ownership is the agent's, not the viewer's.** Closing the browser tab does not stop the simulator; the next viewer resumes exactly where they left off.
5. **No media data ever lands in Convex.** Frames, NALs, click coords, typed text — all P2P only. Convex sees a counter and the framework tag.
6. **Auth is owner-only for input. Read-only metadata is owner + vibing-scope-guest.**

## 6. Wire protocol — extension, not replacement

### 6.1 Capabilities response

`SupportedTransports` grows from `["direct-webrtc", "relay-jpeg-poll"]`:

```jsonc
{
  "executionMode": "native-webrtc",
  "remoteRuntimeEligible": true,
  "supportedTransports": [
    "direct-webrtc-rtp-h264",   // NEW: real RTP video track
    "direct-webrtc-jpeg-v1",    // alias for legacy jpeg-DC
    "relay-jpeg-poll"           // pre-existing fallback
  ],
  "currentHostClass": "linux-android",
  "viewerEnabled": true,        // NEW: this host can encode (deps present)
  "remoteBuilders": [
    { "id": "mac-rack-1", "host": "primary", "platforms": ["ios"], "status": "online" }
  ],
  "targets": [
    { "id": "android-emulator", "platform": "android", "enabled": true,
      "transportPreferences": ["direct-webrtc-rtp-h264", "direct-webrtc-jpeg-v1", "relay-jpeg-poll"] },
    { "id": "ios-simulator-remote", "platform": "ios", "enabled": true,
      "runtimeHostClass": "macos-ios", "via": "mac-rack-1",
      "transportPreferences": ["direct-webrtc-rtp-h264", "relay-jpeg-poll"] },
    { "id": "flutter-android-emulator", "platform": "android", "enabled": true,
      "transportPreferences": ["direct-webrtc-rtp-h264", "direct-webrtc-jpeg-v1"] }
  ]
}
```

The new `targets[].transportPreferences` is an ordered list. The viewer
SDP-offers them in order; the agent picks the first one it can satisfy
and answers with that one chosen. New transports are added by appending,
never by removing.

### 6.2 Session payload — new fields

```jsonc
{
  "id": "rr_173...",
  "framework": "swift",
  "targetId": "ios-simulator-remote",
  "transportMode": "direct-webrtc-rtp-h264",
  "frameTransport": "rtp-h264-v1",                                    // NEW
  "deviceDims": { "width": 393, "height": 852, "scale": 3, "rotation": "portrait" },  // NEW
  "remoteBuilderId": "mac-rack-1",                                    // NEW
  "status": "streaming",
  ...
}
```

### 6.3 SDP exchange — same endpoint, smarter handler

`POST /remote-runtime/sessions/<id>/webrtc/offer` already exists. The
change: the agent inspects the offer, picks a transport from the
preference list, and either adds a video track (RTP path) or a `frames`
DataChannel (legacy JPEG-DC path).

```go
pc, _ := webrtc.NewPeerConnection(cfg)

// Always add the events DataChannel (control). Every viewer expects it.
eventsDC, _ := pc.CreateDataChannel("events", nil)

if shouldUseRTPVideo(offer) && agentCanEncodeH264() {
    track, _ := webrtc.NewTrackLocalStaticSample(
        webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
        "yaver-runtime", "yaver-stream",
    )
    pc.AddTrack(track)
    go pumpH264FromDriver(track, sessionID)
} else {
    framesDC, _ := pc.CreateDataChannel("frames", nil)
    go pumpJPEGsToDataChannel(framesDC, sessionID)
}
```

### 6.4 Events DataChannel messages

Existing: `frame-meta`, `frame-error`, `session`, `ready`. New:

```jsonc
// Once on connect — viewer sizes its window, scales pointer events
{"type":"dims","width":393,"height":852,"scale":3,"rotation":"portrait","ts":"..."}

// Rotation
{"type":"rotation","rotation":"landscape","width":852,"height":393,"ts":"..."}

// Heartbeat
{"type":"ping","ts":"..."}        // viewer → agent every 5s
{"type":"pong","ts":"..."}        // agent → viewer reply

// Viewer takeover
{"type":"taken-over","by":"<remoteAddr>","ts":"..."}

// Bandwidth feedback
{"type":"throttle","reason":"rtp_loss_high","newBitrateKbps":500}

// Viewer → agent (input)
{"type":"tap","x":196,"y":420,"ts":"..."}
{"type":"swipe","points":[{"x":..,"y":..,"t":..}, ...],"durationMs":300}
{"type":"text","text":"hello world"}
{"type":"key","key":"back"}       // android only
{"type":"key","key":"home"}
{"type":"hardware","button":"sleep"}  // ios only
```

### 6.5 Backwards compatibility

| Viewer | Agent | What happens |
|---|---|---|
| Old web/mobile (JPEG-DC) | Old | JPEG-DC, exactly today. |
| Old | New | Old offer doesn't list rtp-h264 → JPEG-DC fallback. |
| New web viewer | Old | Old agent doesn't recognise rtp-h264 entries → JPEG-DC fallback. |
| New | New | Direct-WebRTC RTP H.264 happy path. |

No flag day, no version pinning. The transport list is the contract.

## 7. RTP video track — replacing the JPEG pump

### 7.1 Capture sources

| Target | Capture command | Format on stdout | Notes |
|---|---|---|---|
| Android emulator (Linux + macOS) | `adb exec-out screenrecord --output-format=h264 --bit-rate 4000000 --size 1080x2400 -` | Raw H.264 Annex-B (with start codes `0x00 0x00 0x00 0x01`) | `exec-out` (not `shell`) avoids CR/LF mangling. 3-min cap per call — restart on close. |
| iOS simulator (macOS) | `xcrun simctl io booted recordVideo --codec=h264 -` | Fragmented MP4 | Stop with SIGINT (existing `recording_iossim.go` pattern). Parse box stream → Annex-B. |

The agent feeds NAL units into Pion's `TrackLocalStaticSample.WriteSample`.
No ffmpeg subprocess. Pure-Go in-tree NAL extractor handles both formats.

### 7.2 Bitrate management

Pion's `interceptor` package gives TWCC + REMB. The manager subscribes to
bandwidth feedback and:

1. Restarts `screenrecord --bit-rate <new>` (Android — cheapest knob).
2. Drops resolution + frame-rate cap on iOS (`--mask` is the only knob).
3. Emits `{"type":"throttle"}` so the viewer surfaces "reduced quality".

When packet loss > 8% sustained for 10 s and two restarts haven't helped,
the manager renegotiates with `direct-webrtc-jpeg-v1`. Always shows
something.

### 7.3 Latency budget

| Stage | Target |
|---|---|
| Capture | ≤ 80 ms |
| Pipe → Go split | ≤ 5 ms |
| RTP packetise + DTLS-SRTP | ≤ 5 ms |
| ICE direct path | ≤ 30 ms LAN |
| Browser decode (`<video>`) | ≤ 40 ms |
| **Total wall-clock** | **≤ 160 ms LAN** / ≤ 350 ms over relay |

vs. today's ~700 ms tick + ~150 ms encode + transport: roughly 4×
snappier.

## 8. Screen-size alignment

### 8.1 Probing dimensions

| Target | Probe |
|---|---|
| iOS simulator | Parse `xcrun simctl list devices --json` for the booted UDID; map `deviceTypeIdentifier` → bundled `devicetypes.json` (`/Library/Developer/CoreSimulator/Profiles/DeviceTypes/<id>.simdevicetype/Contents/Resources/profile.plist`) → `Mainscreen.Width / Height`. Cache. |
| Android emulator | `adb shell wm size` → `Physical size: 1080x2400`. `adb shell wm density` → `Physical density: 440`. Trivial. |

Probe runs once at `Attach`, lands on the session payload's `deviceDims`
field, and re-runs on a `rotation` event.

### 8.2 Rotation

- Android: subscribe to `adb shell content observe --uri content://settings/system/user_rotation`.
- iOS: poll `xcrun simctl io booted screenshot --type=png /dev/stdout` PNG dimensions when something feels off.

### 8.3 Viewer alignment math

Browser `<video>` has the device's natural aspect ratio. We constrain
its container with CSS `aspect-ratio` and `object-fit: contain`.
Pointer coordinates are converted to device coords with:

```ts
const rect = videoEl.getBoundingClientRect();
const dx = Math.round((event.clientX - rect.left) * (deviceDims.width  / rect.width));
const dy = Math.round((event.clientY - rect.top)  * (deviceDims.height / rect.height));
```

A 4K monitor and a laptop both produce the same `tap` event for the same
UI control.

## 9. Swift on Linux — the truth

### 9.1 What works on Linux today

The official Swift toolchain (swift.org):

- ✅ `swift build`, `swift test`, `swift package`
- ✅ Foundation, Dispatch, FoundationXML, FoundationNetworking
- ✅ Server-side Swift (Vapor, Hummingbird), pure-Swift libraries
- ❌ UIKit, SwiftUI, Combine, CoreGraphics, AVFoundation, MapKit
- ❌ `xcodebuild`, `xcrun simctl`, code signing
- ❌ Producing a `.app` / `.ipa` for iOS

### 9.2 Two workflows

1. **Logic / library iteration** — `swift test` red/green works on Linux. Add `swift_toolchain.go`.
2. **UI / device iteration** — dispatch to a paired remote macOS builder. Linux agent is a signaling proxy; RTP video flows direct viewer↔Mac whenever ICE finds a path.

### 9.3 Remote macOS builder pairing

`remoteBuilders` table on Convex (counters only — no hostnames / no
tunnel tokens). Pairing:

```bash
# Mac
yaver primary serve --as-builder=ios     # NEW

# Linux
yaver builder list                       # NEW
yaver builder use mac-rack-1
```

When `framework === swift && hostClass !== macos-ios`, the manager
tunnels build + session calls through QUIC P2P to the builder. The Mac
runs the existing `RemoteRuntimeManager`; its session is re-exposed
through the Linux agent's signaling endpoint. The Linux box never
decodes or re-encodes — forward-only proxy.

### 9.4 Cost ceiling

- Mac mini in the rack (€600 once) → solo dev forever
- GitHub macOS runner (~$0.16/min, $0.08 self-hosted) → on-demand
- MacStadium / MacWeb (~$1/hr) → on-demand at scale
- Paired colleague's MacBook → free

`yaver builder use` treats them all the same.

## 10. Flutter on Linux

Flutter Linux toolchain compiles full apps locally; iOS still needs a
Mac builder. Implementation: `executionModeForFramework("flutter")`
returns `NativeWebRTC`; capability response maps Flutter to either an
Android-emulator or a (remote-)iOS-simulator target. Roughly one file's
work.

## 11. Web viewer — `web/components/dashboard/RemoteRuntimeViewer.tsx`

The new web viewer **replaces** the current 180-line JPEG-poll viewer.
The browser handles every hard part for free:

- H.264 decode — built into Chrome / Edge / Safari (Firefox depends on system codecs).
- Video paint — `<video srcObject={stream} autoPlay playsInline muted />`.
- Pointer events — DOM `pointerdown` / `pointermove` / `pointerup`.
- IME — `<input>` with `oninput` for text composition.
- DPI / scaling — CSS `aspect-ratio` and `object-fit: contain`.

### 11.1 Component shape

```tsx
function RemoteRuntimeViewer({ sessionId }: { sessionId: string }) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const eventsRef = useRef<RTCDataChannel | null>(null);
  const [dims, setDims] = useState<DeviceDims | null>(null);
  const [status, setStatus] = useState("connecting");

  useEffect(() => {
    const pc = new RTCPeerConnection({
      iceServers: [{ urls: "stun:stun.l.google.com:19302" }, /* TURN from /turn-credentials */],
    });
    pcRef.current = pc;

    pc.ontrack = (e) => {
      if (videoRef.current) videoRef.current.srcObject = e.streams[0];
    };

    const events = pc.createDataChannel("events");
    eventsRef.current = events;
    events.onmessage = (e) => {
      const msg = JSON.parse(e.data);
      if (msg.type === "dims") setDims(msg);
      if (msg.type === "rotation") setDims((d) => ({ ...d, ...msg }));
      if (msg.type === "throttle") setStatus(`throttled: ${msg.reason}`);
      if (msg.type === "taken-over") pc.close();
    };

    pc.addTransceiver("video", { direction: "recvonly" });

    (async () => {
      const offer = await pc.createOffer();
      await pc.setLocalDescription(offer);
      const res = await fetch(`/api/remote-runtime/sessions/${sessionId}/webrtc/offer`, {
        method: "POST",
        body: JSON.stringify({
          sdp: offer.sdp, type: offer.type,
          transportPreferences: ["direct-webrtc-rtp-h264", "direct-webrtc-jpeg-v1"],
        }),
      });
      const { answer } = await res.json();
      await pc.setRemoteDescription({ type: answer.type, sdp: answer.sdp });
    })();

    const heartbeat = setInterval(() => {
      events.readyState === "open" && events.send(JSON.stringify({ type: "ping", ts: Date.now() }));
    }, 5000);

    return () => { clearInterval(heartbeat); pc.close(); };
  }, [sessionId]);

  const sendTap = (e: React.PointerEvent) => {
    if (!dims || !videoRef.current || !eventsRef.current) return;
    const rect = videoRef.current.getBoundingClientRect();
    const x = Math.round((e.clientX - rect.left) * (dims.width / rect.width));
    const y = Math.round((e.clientY - rect.top) * (dims.height / rect.height));
    eventsRef.current.send(JSON.stringify({ type: "tap", x, y, ts: Date.now() }));
  };

  return (
    <div className="remote-runtime-viewer">
      <header>{status} · {dims ? `${dims.width}×${dims.height}` : "—"}</header>
      <div className="video-wrap" style={{ aspectRatio: dims ? `${dims.width}/${dims.height}` : "9/19.5" }}>
        <video ref={videoRef} autoPlay playsInline muted onPointerUp={sendTap} />
      </div>
      <input placeholder="type to send to device" onChange={...} />
      <Toolbar>{ /* Reload / Home / Back / Disconnect */ }</Toolbar>
    </div>
  );
}
```

### 11.2 Click → tap mapping (the core interaction)

A click on the `<video>` element IS a tap on the device. No hover, no
preview, no two-step. Drag → swipe.

For two-finger trackpad scrolls: turn into `swipe` events with points
sampled from `wheel` event deltas. Defer to Phase 6.

For the keyboard: a hidden `<input>` element receives focus when the
user clicks the viewer; its `oninput` becomes a `text` event, and
special keys (Tab, Enter, Esc, Backspace) become `key` events.

### 11.3 Visual layout

```
┌─ Viewer (web dashboard tab) ────────────────────────────┐
│ sfmg / mobile · iPhone 15 Pro (393×852)  · 24 FPS · LAN │
│ ┌─[ ⏵ Reload ][ ⌂ Home ][ ◁ Back ][ ⏏ Disconnect ]──┐ │
│ │                                                    │ │
│ │            < video element here, sized             │ │
│ │              by CSS aspect-ratio >                 │ │
│ │                                                    │ │
│ └────────────────────────────────────────────────────┘ │
│ ⌨ [ type to send to device ............... ] [ Send ] │
└─────────────────────────────────────────────────────────┘
```

The header text reads from the session payload + the `dims` event. The
toolbar buttons hit `/dev/reload` (existing), and `key:home` /
`key:back` events on the events channel.

### 11.4 Browser compatibility

| Browser | RTCPeerConnection | H.264 video track | Verdict |
|---|---|---|---|
| Chrome/Chromium ≥ 90 | ✅ | ✅ (HW where supported) | Full. |
| Edge ≥ 90 | ✅ | ✅ | Full. |
| Safari ≥ 14 | ✅ | ✅ | Full. |
| Firefox ≥ 100 | ✅ | ⚠️ Cisco OpenH264 plugin (some corp policies block) | Detect via `RTCRtpSender.getCapabilities("video")`; fall back to `direct-webrtc-jpeg-v1` if H.264 missing. |

### 11.5 Auth

Same `agent-client.ts` path the rest of the dashboard uses. No tokens
in URLs.

## 11.6 What `npm install -g yaver-cli` actually delivers

The npm package ships:

- The Node shim (`bin/yaver` → resolves the right platform binary)
- Platform binaries downloaded into `~/.yaver/bin/<version>/<os-arch>/yaver` on first run
- `cli/sdk-manifest.json`
- `cli/hermesc/` (the bundled Hermes compiler for `yaver-push`)
- The Go binary statically links Pion (no libwebrtc dep)

It does **not** ship adb, Xcode CLT, ffmpeg, GTK, or GStreamer. Those
are runtime deps and the npm package neither bundles nor requires them
upfront — `cli/src/postinstall.js` calls `yaver install remote-runtime`
automatically on global install (line 307), and that is the existing
seam that lays down what's needed.

### 11.6.1 What WebRTC needs at runtime, by role

| Role | Hard runtime deps |
|---|---|
| **Agent (encoder) on Linux** | `adb` (covered today by `yaver install remote-runtime` → `android-sdk`). `ffmpeg` is *not* needed thanks to the in-tree H.264 NAL extractor. |
| **Agent (encoder) on macOS** | `xcrun` from Xcode CLT (assumed already present for `yaver wire push`). `xcodegen + cliclick` already covered by the existing remote-runtime install on macOS. |
| **Web viewer (browser)** | None added. Browser handles RTP + H.264 decode + video paint natively. |
| **Mobile viewer (badge-only)** | None added. |

So the install matrix collapses to "what we already install today, plus
optional ffmpeg as a fallback for unusual H.264 sources". `yaver doctor
webrtc` (new) reports any gap.

### 11.6.2 Per-platform reality

| Platform | Agent role install | Notes |
|---|---|---|
| Ubuntu 22.04 / 24.04, Debian 12 | `npm install -g yaver-cli` (postinstall runs `yaver install remote-runtime` → installs `android-sdk` which includes `adb`) | Tested in CI. |
| Fedora 40, RHEL 9 | same | Same plan via dnf. |
| Arch / Manjaro | same | Same plan via pacman. |
| Raspberry Pi OS (Bookworm, arm64) | same — Pi 4/5 only | iOS not supported (no Mac builder there). |
| macOS (Apple Silicon + Intel) | `npm install -g yaver-cli` — that's it | `xcrun` already present if Xcode CLT installed. |
| WSL2 | `npm install -g yaver-cli` | Direct ICE typically blocked by Hyper-V NAT — sessions go via TURN. |
| Bare-metal Windows | not supported | Use WSL2. |

### 11.6.3 The `yaver doctor webrtc` command (new)

Probe + recommend + (with `--install`) auto-fix:

```bash
$ yaver doctor webrtc
Linux x86_64 — yaver 1.99.133 — agent role
═════════════════════════════════════════════════════════════════
✓ pion/webrtc — built into agent
✓ adb (platform-tools 35.0.2)
✓ in-tree H.264 extractor — no ffmpeg dep needed
✓ android-emulator target enabled
✗ no remote macOS builder paired — iOS targets unavailable

Pair a macOS builder:
  on the Mac:  yaver primary serve --as-builder=ios
  on Linux:    yaver builder use <mac-alias>
═════════════════════════════════════════════════════════════════
```

Flags: `--json` (for the dashboard), `--install` (run `yaver install
remote-runtime` then re-probe).

### 11.6.4 Postinstall — what changes

`cli/src/postinstall.js` already calls `yaver install remote-runtime`.
We extend that path to also probe for and install ffmpeg *only if* the
host kernel is too old for adb's H.264 emit (pre-Android 10 emulator).
99% of installs do not need ffmpeg.

## 11.7 In-tree H.264 NAL extractor — eliminating ffmpeg

| Source | Container | Conversion |
|---|---|---|
| `adb exec-out screenrecord --output-format=h264 -` | Already raw Annex-B | Split on `0x00 0x00 0x00 0x01` boundaries — ~30 lines of Go. |
| `xcrun simctl io booted recordVideo --codec=h264 -` | Fragmented MP4 | Parse boxes, extract `avcC` from `moov`, emit SPS/PPS as the first NAL, decode AVCC length-prefixed → Annex-B. ~150 lines. |

`desktop/agent/h264_extract.go` (~200 lines). Replaces the hard ffmpeg
dep with pure Go.

## 11.8 Mobile + web viewer migration

- `web/components/dashboard/RemoteRuntimeViewer.tsx` is rewritten to the RTP-track-consuming component in §11.1. The old JPEG `<img>` + tap handler are removed.
- `mobile/app/remote-runtime.tsx` becomes a metadata-only screen — drop the `<Image>`, drop the SDP offer, show framework + dims + a deep-link to the dashboard. **Mobile is not touched in this Go-agent-first iteration.** It stays JPEG-renderable until a future minor bump removes the rendering.

## 12. Compliance with the existing protocol (and Hermes path)

1. The agent's `/remote-runtime/sessions/<id>/webrtc/offer` keeps the same shape. Only optional fields are added.
2. The events DataChannel keeps the existing message types and only adds new ones. Unknown types are ignored on both sides.
3. **Hermes endpoints are unchanged.** `/dev/build-native`, `/dev/reload`, `/dev/start`, `/dev/events`, `yaver insert`, super-host bundle loader, BC version validation, `incompatibleNativeModules` dialog — all in separate files from the new code. No imports added across that boundary.
4. New file: `desktop/agent/hermes_isolation_test.go` enumerates Hermes symbols + HTTP routes and asserts no `remote_runtime_*` file imports any of them.

## 13. Auto-WebRTC dispatch — user-facing rule

> RN/Expo apps stream via Hermes hot-reload. Everything else (Swift /
> Kotlin / Flutter) streams via WebRTC into the web dashboard's
> RemoteRuntimeViewer. There's no checkbox.

Encoded in:

1. `mobile/app/(tabs)/apps.tsx` — branches on `executionMode`. Hermes path unchanged.
2. `web/components/dashboard/ProjectsView.tsx` — same branch in the row's primary action.

The single gate to flip is `executionModeForFramework("flutter")` from
`Unsupported` to `NativeWebRTC`.

## 14. Failure modes

| Failure | Detection | Response |
|---|---|---|
| ICE never gathers | 5 s timeout on gathering state | Recreate with TURN-only candidates. Last resort: renegotiate as `relay-jpeg-poll`. |
| H.264 encode unavailable | `agent.canEncodeH264()` false at session create | Capabilities omits `direct-webrtc-rtp-h264`. Viewer falls through to JPEG-DC. |
| Mac builder offline | Heartbeat misses 30 s | Mark session `builder-disconnected`. Auto-retry 10 s, up to 5 min. |
| Simulator hung | 5 s without a NAL unit | Kill capture, re-Boot, restart. Emit `restart`. |
| Coordinate desync | User reports tap landing wrong | Viewer logs both window + device coords; UI shows a 1 s circle at device tap point. |
| Relay UDP blocked | TURN candidate fails | Already-handled — `relay-jpeg-poll` is HTTP/QUIC. |
| Multi-viewer collision | New offer arrives while PC open | One PC per session for v1; new offer boots old viewer with `taken-over`. |
| Swift build fails on Mac | `swift build` exit != 0 | Stream error through existing build SSE; viewer shows a build-error overlay with retry. |
| No Mac builder paired for Swift | `defaultBuilder("ios")` nil | Capabilities response includes `reason: "Pair a macOS builder ..."`. Dashboard surfaces a "Pair my Mac" button. |

## 15. Privacy boundary

Three mechanical extensions to `convex_privacy_test.go`:

1. **Forbidden keys** add: `videoFrame`, `rtpPayload`, `mediaSegment`, `screenStream`, `simctlOutput`, `screenrecordPayload`, `tapCoord`, `swipeCoord`, `keyText`, `clipboardText`, `remoteBuilderHostname`, `remoteBuilderTunnelToken`.
2. **Schema** adds only counters (`remoteRuntimeSessionMetrics`).
3. **No path leaks** — privacy scanner already greps for `/Users/`; new fixture exercises the iOS PNG path.

## 16. Phased plan & status

Each phase is mergeable on its own. Status reflects the live
implementation as the doc is updated alongside code.

| Phase | Scope | Status |
|---|---|---|
| **0.** Update doc + npm-install dep audit | This file. | ✅ shipped 2026-05-03 |
| **1.** Flutter into the WebRTC family + dims event | `executionModeForFramework("flutter")` → `NativeWebRTC`; capability response adds Flutter targets; events channel emits `dims` once on session start. | ✅ shipped |
| **2.** Pure-Go H.264 NAL extractor + `yaver doctor webrtc` + `viewerEnabled` capability | `h264_extract.go`, `doctor_webrtc.go`, capability surface. Unblocks Phase 3 without an ffmpeg dep. | ✅ shipped |
| **3.** Real RTP H.264 video track for Android | `remote_runtime_video_track.go`: adb screenrecord → in-tree NAL extractor → Pion `TrackLocalStaticSample`. | ✅ shipped |
| **4.** RTP H.264 for iOS simulator on macOS | xcrun simctl recordVideo → in-tree fragmented-MP4 parser (`MP4ToAnnexB`) → Pion track. `agentCanEncodeRTPH264("ios-simulator")` returns true on darwin. | ✅ shipped |
| **5.** Mac builder pairing + signaling proxy | `remote_builder.go` + `remote_builder_cmd.go`: local registry under `~/.yaver/builders.json` (mode 0600), `yaver builder {add,list,use,forget,ping}`, `yaver serve --builder-platforms=ios,…` opts an agent in as a builder + surfaces `isBuilder` on `/info`. `RemoteRuntimeCapabilities.RemoteBuilders` exposes paired builders to the dashboard. `remote_runtime_dispatch.go`: when Swift / Flutter+iOS hits a non-darwin host, the agent forwards Create / Get / Offer / Control / Delete to the paired Mac. Browser ICE-negotiates direct with the Mac; the Linux box only carries SDP signaling, never RTP. | ✅ shipped (full closer) |
| **6.** Web dashboard viewer | New `RemoteRuntimeViewer.tsx`: RTP H.264 happy path, JPEG-DC fallback, dims-aware pointer events, drag → swipe, heartbeat ping. | ✅ shipped |
| **7.** Relay TURN allocation | `relay/turn.go` Pion long-term-credential server. Agent endpoint `/remote-runtime/turn-credentials` mints short-lived creds. Web viewer fetches them on session start. | ✅ shipped |
| **8.** Swift logic on Linux | `swift_toolchain.go` + `swift_cmd.go`: `yaver swift doctor` probes the toolchain, `yaver swift logic [path]` runs `swift build` + `swift test` on a SwiftPM project and mirrors swift's exit code. | ✅ shipped |
| **9.** Compliance + privacy + Hermes-isolation gate | `hermes_isolation_test.go` parses the WebRTC region's source and fails the build if any file references a Hermes-only symbol. Multi-viewer fan-out: `remoteRuntimeLiveState.peers` lets N PeerConnections share one Pion video track; second offer no longer kicks the first viewer. | ✅ shipped |

Implementation order chosen for this iteration: **0 → 2 → 1 → 3 → 6 →
4 → 5 → 7 → 8 → 9**. Deps before features; web viewer rewritten only
after the Go API stabilizes.

## 17. File-by-file change list

New files:

- `desktop/agent/h264_extract.go` — pure-Go MP4 → Annex-B + raw NAL splitter
- `desktop/agent/h264_extract_test.go`
- `desktop/agent/doctor_webrtc.go` — runtime-dep probe + auto-install entry
- `desktop/agent/doctor_webrtc_test.go`
- `desktop/agent/remote_runtime_video_track.go` — pure-Go pipeline: adb / xcrun → NAL extractor → Pion track
- `desktop/agent/remote_runtime_dims.go` — device dim + rotation probe
- `desktop/agent/remote_runtime_dispatch.go` — Linux→Mac signaling proxy
- `desktop/agent/remote_runtime_protocol_test.go` — viewer SDP compliance
- `desktop/agent/remote_builder.go` — pairing + heartbeat
- `desktop/agent/remote_builder_cmd.go` — `yaver builder list/use/forget`
- `desktop/agent/swift_toolchain.go` — Linux Swift detect + invoke
- `desktop/agent/swift_toolchain_test.go`
- `desktop/agent/cmd_swift_logic.go` — `yaver swift logic`
- `desktop/agent/hermes_isolation_test.go` — symbol-boundary regression gate
- `relay/turn.go` — colocated Pion TURN
- `relay/turn_test.go`
- `web/components/dashboard/RemoteRuntimeViewer.tsx` — rewritten (replaces JPEG-DC version)
- `web/lib/remote-runtime-client.ts` — SDP plumbing helper
- `docs/native-webrtc-web-streaming.md` — this file

Modified files:

- `desktop/agent/remote_runtime.go` — `executionModeForFramework("flutter")`, `remoteBuilders` field, target list growth, `viewerEnabled` capability bool
- `desktop/agent/remote_runtime_webrtc.go` — branch on transport preference, attach video track when chosen, emit dims/rotation events, add swipe + hardware controls
- `desktop/agent/install_cmd.go` — extend `runRemoteRuntimeInstall` (optional ffmpeg), register `webrtc` doctor
- `desktop/agent/main.go` — register `yaver doctor webrtc` + `yaver builder` + `yaver swift` subcommands
- `desktop/agent/convex_privacy_test.go` — forbidden keys + new fixture
- `desktop/agent/convex_state_sync.go` — `recordRemoteRuntimeSessionMetrics` mutation (counters only)
- `desktop/agent/httpserver.go` — register `/remote-runtime/turn-credentials`, `/builders/*`, `/doctor/webrtc`
- `cli/src/postinstall.js` — already calls `yaver install remote-runtime`; add a `YAVER_SKIP_POSTINSTALL_WEBRTC` env-var escape hatch for advanced users
- `relay/tunnel.go` — TURN listener alongside QUIC
- `backend/convex/schema.ts` — `remoteBuilders`, `remoteRuntimeSessionMetrics`
- `backend/convex/agentSync.ts` — counter mutations
- `web/components/dashboard/ProjectsView.tsx` — Flutter row gets WebRTC affordance; native-webrtc rows show "Open Viewer" button
- `CLAUDE.md` — pointer to this doc

## 18. Open questions

1. **Multi-viewer fan-out.** Pion supports one track on multiple PCs; the manager doesn't yet. Defer to Phase 9.
2. **Hardware encode.** Some Linux emulators support qsv/vaapi; Apple Silicon has VideoToolbox. Auto-detect, prefer when present, but don't block v1.
3. **Real iPhones over USB.** libimobiledevice install + filesystem works; live screen mirror lags. Stretch goal.
4. **iOS simulator audio.** xcrun records silent video. Audio-over-WebRTC needs a separate capture (Soundflower/BlackHole). Out of scope.
5. **Browser H.264 decode on Firefox.** Cisco OpenH264 plugin auto-installs on most setups; corp policies sometimes block it. Detect and fall back to JPEG-DC.

## 19. Single-line summary

**Upgrade the existing JPEG-over-DataChannel WebRTC remote-runtime to a
real RTP H.264 pipeline served entirely from the Go agent; consume it in
a rewritten `RemoteRuntimeViewer.tsx` web component (browser RTC +
`<video>` + pointer events); pair Linux dev boxes to a remote macOS
builder for iOS targets; promote Flutter to a first-class WebRTC target
— all without touching the React Native / Hermes hot-reload path, and
with `npm install -g yaver-cli` already covering the only runtime dep
(`adb`) via the existing `yaver install remote-runtime` postinstall
hook.**

---

*When this doc and the code disagree, the code wins. Update this file as
part of the same change that modifies the behavior it describes.*
