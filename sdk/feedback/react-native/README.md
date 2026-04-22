# yaver-feedback-react-native

Visual feedback SDK for Yaver. Lets testers and developers shake their phone and then keep a small quick-access icon on screen for hot reload, vibing, screenshot or file upload, and screen recording uploads straight to a Yaver agent running on a dev machine.

## Installation

```bash
npm install -g yaver-cli
cd your-app
yaver feedback setup
```

Manual fallback:

```bash
npm install yaver-feedback-react-native
```

> **Mobile only.** This SDK targets React Native (iOS + Android). A `yaver-feedback-web` package exists for browser apps but currently expects a bring-your-own auth token — the equivalent in-app sign-in UX (Apple / Google / GitHub / GitLab / Microsoft / email) for the web SDK will land in a future release. Open an issue if you need it sooner.

### Peer dependencies

For full functionality, install these peer dependencies:

```bash
# Auth (in-app browser OAuth + native Apple Sign-In)
npm install expo-web-browser expo-apple-authentication

# Device discovery (stored connections)
npm install @react-native-async-storage/async-storage

# Screenshots
npm install react-native-view-shot

# Optional file upload
npx expo install expo-document-picker
```

`expo-apple-authentication` is optional — if it's missing, "Continue with Apple" falls through to in-app browser OAuth on Android. iOS hosts that want true native Apple Sign-In must also enable the **Sign in with Apple** capability in Xcode.

Android hosts must register the OAuth callback in `AndroidManifest.xml`:

```xml
<intent-filter>
  <action android:name="android.intent.action.VIEW" />
  <category android:name="android.intent.category.DEFAULT" />
  <category android:name="android.intent.category.BROWSABLE" />
  <data android:scheme="yaver" android:host="oauth-callback" />
</intent-filter>
```

iOS does not require any URL scheme registration — `ASWebAuthenticationSession` intercepts the redirect inside the auth session.

## Quick Start (0.6+: native auth)

Starting with **0.6.0**, the SDK login screen mirrors the Yaver mobile app: native Apple Sign-In on iOS, in-app browser OAuth for Google / GitHub / GitLab / Microsoft (no codes, no leaving the app), and inline email + password. The user never sees a verification code or has to switch to a web browser.

Drop in `<FeedbackModal />` and the first time a user triggers feedback without a cached session, they get:

1. A full-screen login modal — Apple (native) / Google / GitHub / GitLab / Microsoft (in-app browser) / email + password (inline).
2. A picker of the machines they can reach — **their own dev boxes** plus **guest-shared machines** (where another user invited them).

Both screens persist their selection to `AsyncStorage`, so subsequent launches reconnect silently.

```tsx
import { YaverFeedback, BlackBox, FeedbackModal } from 'yaver-feedback-react-native';

// Zero-config: no agentUrl, no authToken — the SDK handles auth + discovery.
YaverFeedback.init({
  trigger: 'shake',
});

// Start Black Box flight recorder once the user has signed in
BlackBox.start();
BlackBox.wrapConsole();  // opt-in: stream console.log/warn/error to the agent

// Add FeedbackModal to your root component — it embeds the login + picker UI.
function App() {
  return (
    <>
      <YourApp />
      <FeedbackModal />
    </>
  );
}
```

Shake your phone to open the feedback modal. On mobile the quick-access icon stays hidden until that first shake, then remains tappable for the rest of the session unless the user hides it. From there the sheet exposes four core actions: hot reload, vibing, screenshot or file upload, and screen recording upload.

### Quick Icon Styling

The quick icon is configurable at compile time through `YaverFeedback.init(...)`, with render-time overrides still available through `<QuickActionIcon />` props when needed. If you do nothing, the SDK uses a high-visibility orange bubble by default so it stays distinct from the blue/indigo FAB styling many mobile apps already have.

```tsx
YaverFeedback.init({
  trigger: 'shake',
  quickIcon: 'after-shake',
  quickIconBackgroundColor: '#0f766e',
  quickIconForegroundColor: '#f8fafc',
  quickIconBorderColor: 'rgba(255,255,255,0.85)',
  quickIconShadowColor: '#000000',
});
```

Available options:

- `quickIconBackgroundColor`: bubble fill color.
- `quickIconForegroundColor`: the `y` label color.
- `quickIconBorderColor`: outer ring color.
- `quickIconShadowColor`: shadow tint.
- `quickIconColor`: deprecated alias for `quickIconBackgroundColor`.
- `quickIconInitialPosition`: initial top-left position in pixels.

If you need a one-off local override instead of global SDK config:

```tsx
<QuickActionIcon
  backgroundColor="#111827"
  foregroundColor="#f9fafb"
  borderColor="#f59e0b"
/>
```

### Opt-out: bring-your-own auth (pre-0.5 behavior)

If your app already authenticates against yaver.io on its own and just wants to pass a ready token:

```tsx
YaverFeedback.init({
  agentUrl: 'http://192.168.1.10:18080',
  authToken: 'your-sdk-token',  // SDK token (recommended) or CLI token
  trigger: 'shake',
  autoLogin: false,             // don't show the in-app login flow
});
```

## Authentication

The SDK uses **bearer token auth** for all requests to the agent. Two token types are supported:

### SDK Tokens (recommended)

SDK tokens are **long-lived (1 year)** and **independent from CLI session tokens**. CLI reauth (`yaver auth`) does NOT invalidate SDK tokens.

```bash
# Create an SDK token
yaver sdk-token create --label "BentoApp dev"
# prints: 4a8f...b3c2
```

Use in your SDK config:
```tsx
YaverFeedback.init({
  authToken: '4a8f...b3c2',  // SDK token
  agentUrl: 'http://192.168.1.10:18080',
});
```

Or via env var for build-time injection:
```bash
# .env.yaver (gitignored)
YAVER_SDK_TOKEN=4a8f...b3c2
YAVER_AGENT_URL=http://192.168.1.10:18080
```

### CLI Token (fallback)

The SDK can share the CLI session token directly. This works because each `yaver auth` creates a **new session** without invalidating old ones. The old token stays valid until it expires (1 year) or is explicitly revoked via "logout everywhere".

```bash
# .env.yaver (fallback)
YAVER_AUTH_TOKEN=abc123...
```

### Token Validation Flow

```
SDK request → Agent HTTP server
  1. Exact match with agent's own token → Allow (fast path)
  2. Token in cache → userId match → Allow/Deny
  3. Try Convex /auth/validate (session token)
  4. Try Convex /sdk/token/validate (SDK token)
  → Cache result → userId match → Allow/Deny
```

Both token types resolve to the same `userId`. The agent checks `userId` equality, not token equality.

### Token Lifecycle

| Event | CLI Token | SDK Token |
|-------|-----------|-----------|
| `yaver auth` (reauth) | New token created, old stays valid | Unaffected |
| `yaver signout` | Current session deleted | Unaffected |
| `yaver signout --all` | ALL sessions deleted | Unaffected |
| SDK token revoke | Unaffected | Revoked |
| Token expiry | 1 year from last refresh | 1 year from creation |

### Security

The SDK implements defense-in-depth with 6 security layers:

**1. Scope Restriction** — SDK tokens can only access feedback/blackbox/voice/builds endpoints. They CANNOT execute tasks, run commands, access the vault, or shut down the agent.

```bash
# Default scopes (safe for embedding in app builds)
yaver sdk-token create --label "BentoApp"
# → scopes: feedback, blackbox, voice, builds

# Narrow scopes (feedback only)
yaver sdk-token create --scopes feedback,blackbox
```

**2. IP Binding** — Restrict tokens to specific networks:
```bash
yaver sdk-token create --allowed-ips 192.168.1.0/24
```

**3. Agent-side IP Allowlist** — Block all requests from outside your network:
```bash
yaver serve --allow-ips 192.168.1.0/24,10.0.0.0/8
```

**4. Token Rotation** — Rotate tokens without downtime (5-minute grace period):
```typescript
const { token } = await client.rotateToken();
// Old token valid for 5 more minutes
```

```bash
# Short-lived tokens for CI/CD
yaver sdk-token create --expires 24h
```

**5. New Device Alerts** — When an SDK token is used from a new IP, a security event is logged to Convex. Query via `GET /security/events`.

**6. HTTPS on LAN** — Agent auto-generates a self-signed TLS cert and serves HTTPS on port 18443. The cert fingerprint is exposed via `/health` and the LAN beacon for cert pinning.

**General rules:**
- **Never commit tokens to source control** — use `.env.yaver` (gitignored) or env vars
- SDK tokens can be revoked independently: `POST /sdk/token/revoke`
- Even if an SDK token is stolen, it cannot run code on your machine (scope restriction)
- The Convex backend validates tokens against your account only

## Black Box (Flight Recorder)

Continuous streaming of app events to the agent. The agent keeps a ring buffer (last 1000 events per device) and injects context into fix prompts — so the AI agent already knows what the app was doing when you ask for a fix.

```tsx
import { BlackBox } from 'yaver-feedback-react-native';

// Start streaming (call after YaverFeedback.init)
BlackBox.start({
  flushInterval: 2000,   // send buffered events every 2s (default)
  maxBufferSize: 50,     // flush immediately at 50 events (default)
  appName: 'BentoApp',
});

// Logging
BlackBox.log('Cart updated', 'CartScreen');
BlackBox.warn('Low inventory', 'ProductCard');
BlackBox.error('Payment failed', 'Checkout', { orderId: '123' });

// Navigation tracking
BlackBox.navigation('ProductDetail', 'Home');

// Network monitoring
BlackBox.networkRequest('GET', '/api/products', 200, 142);
BlackBox.networkRequest('POST', '/api/order', 500, 3200);

// State changes (Redux actions, context updates, etc.)
BlackBox.stateChange('Cart cleared', { itemCount: 0 });

// Render performance
BlackBox.render('ProductList', 16.5);

// Error capture (also feeds into YaverFeedback error buffer)
BlackBox.captureError(new Error('Null ref'), false, { component: 'CartIcon' });

// Console wrapping (opt-in — SDK never auto-hooks)
BlackBox.wrapConsole();     // intercept console.log/warn/error
BlackBox.unwrapConsole();   // restore originals

// Error handler wrapper (pass-through, streams in real-time)
const existing = ErrorUtils.getGlobalHandler();
ErrorUtils.setGlobalHandler(BlackBox.wrapErrorHandler(existing));

// Control
BlackBox.stop();            // flush remaining events + stop timer
BlackBox.isStreaming;       // check if active
```

### Event Types

| Type | Description | Example |
|------|-------------|---------|
| `log` | Console output (info/warn/error) | `BlackBox.log('User signed in')` |
| `error` | Caught exceptions with stack traces | `BlackBox.captureError(err)` |
| `navigation` | Screen transitions | `BlackBox.navigation('Cart', 'Home')` |
| `lifecycle` | App state (mount, background, foreground) | `BlackBox.lifecycle('app_background')` |
| `network` | HTTP requests/responses | `BlackBox.networkRequest('POST', '/api/pay', 500)` |
| `state` | State mutations | `BlackBox.stateChange('theme toggled')` |
| `render` | Component render with duration | `BlackBox.render('FlatList', 32.1)` |

## Remote Reload (Agent Command Channel)

The SDK maintains a persistent SSE connection to the agent that receives commands. The primary use case: a vibe coder is away from their desk, coding on their phone via the Yaver mobile app, and wants to hot-reload the third-party app (with this SDK) running on the same or a different device.

```
Yaver Mobile App                    Agent                     Third-Party App (SDK)
  tap "Reload"                                                
  ───POST /dev/reload-app──►  process reload                  
                               ├─ dev server reload           
                               └─ BroadcastCommand("reload")  
                                  ───SSE push──────────────►  BlackBox receives command
                                                               ├─ onReload() callback
                                                               └─ DevSettings.reload()
```

Works over both direct LAN and relay connections since it's standard HTTP/SSE.

### Setup

```tsx
import { YaverFeedback, BlackBox } from 'yaver-feedback-react-native';

YaverFeedback.init({
  authToken: 'your-sdk-token',
  agentUrl: 'http://192.168.1.10:18080',

  // Called when the vibe coder triggers reload from Yaver mobile app
  onReload: () => {
    console.log('Remote reload triggered!');
    // Default behavior if omitted: DevSettings.reload() in dev mode
  },

  // Called when agent pushes a new native bundle
  onReloadBundle: (bundleUrl, assetsUrl) => {
    console.log('New bundle available at:', bundleUrl);
    // Default: no-op. Implement custom bundle loading if needed.
  },
});

// Start BlackBox — this also connects the command channel
BlackBox.start({ appName: 'BentoApp' });
```

### Custom Command Handlers

For advanced use cases, register handlers directly on BlackBox:

```tsx
// Listen for any command from the agent
const unsubscribe = BlackBox.onCommand((cmd) => {
  switch (cmd.command) {
    case 'reload':
      // Hot reload from dev server
      DevSettings.reload();
      break;
    case 'reload_bundle':
      // Fetch new native bundle
      const { bundleUrl, assetsUrl } = cmd.data;
      loadNewBundle(bundleUrl, assetsUrl);
      break;
    default:
      console.log('Unknown command:', cmd.command);
  }
});

// Check connection status
BlackBox.isCommandChannelConnected; // boolean

// Clean up
unsubscribe();
```

### Triggering Reload from P2PClient

The SDK can also trigger reload programmatically (e.g., from a "Reload" button in the Feedback modal):

```tsx
import { P2PClient } from 'yaver-feedback-react-native';

const client = new P2PClient('http://192.168.1.10:18080', 'your-token');

// Hot reload (dev server restart)
await client.reloadApp('dev');

// Rebuild native bundle + push to all connected SDK devices
await client.reloadApp('bundle');
```

### How It Works

1. `BlackBox.start()` connects to `/blackbox/command-stream` via SSE
2. The connection auto-reconnects on disconnect (5s backoff)
3. When the agent receives `POST /dev/reload` or `POST /dev/reload-app`, it broadcasts a command to all connected SDK sessions
4. The SDK invokes `onReload` / `onReloadBundle` callbacks, or falls back to `DevSettings.reload()` in dev mode
5. Events are still sent via batch POST to `/blackbox/events` (unchanged)

### Resilience

- Failed flushes re-add events to the buffer (capped at 2x maxBufferSize)
- `YaverFeedback.setEnabled(false)` pauses BlackBox; `setEnabled(true)` resumes it
- BlackBox shares auth config with YaverFeedback (no separate init needed)

## Device Discovery

Three strategies (tried in order):

1. **Convex cloud** — fetch agent IP from Convex device registry (for cloud machines or cross-network)
2. **Stored connection** — try cached URL from last successful connection (AsyncStorage)
3. **LAN scan** — probe common LAN subnets (`192.168.1.*`, `192.168.0.*`, `10.0.0.*`, `10.0.1.*`)

### Convex discovery (recommended for teams)

No hardcoded IP needed — the SDK fetches the agent's IP from Convex:

```typescript
YaverFeedback.init({
  convexUrl: 'https://your-app.convex.site',
  authToken: 'your-token',
  preferredDeviceId: 'abc123',  // optional — first online device if omitted
});
```

### Auto-discovery (no agentUrl needed)

```typescript
YaverFeedback.init({
  authToken: 'your-token',
  trigger: 'shake',
  // No agentUrl — SDK discovers it automatically on first report
});
```

### Manual discovery

```typescript
import { YaverDiscovery } from 'yaver-feedback-react-native';

// Full discovery (Convex → stored → LAN scan)
const result = await YaverDiscovery.discover({
  convexUrl: 'https://your-app.convex.site',
  authToken: 'your-token',
});

// Probe a specific URL
const agent = await YaverDiscovery.probe('http://192.168.1.42:18080');

// Connect and store for future sessions
await YaverDiscovery.connect('http://192.168.1.42:18080');

// Clear stored connection
await YaverDiscovery.clear();
```

## Connection Screen

A full-screen UI for discovering and connecting to a Yaver agent. Shows connection status, URL/token inputs, auto-discover button, and a Start/Stop testing toggle with recording timer.

```tsx
import { YaverConnectionScreen } from 'yaver-feedback-react-native';

function App() {
  return (
    <>
      <YourApp />
      {__DEV__ && <YaverConnectionScreen />}
    </>
  );
}
```

The connection screen auto-discovers agents on mount and provides:
- Green/red connection status indicator
- Text inputs for agent URL (pre-filled from discovery) and auth token
- "Auto-discover" button to scan the network
- "Connect" button for manual connection
- "Start Testing" / "Stop & Send" toggle with recording timer

## Three Feedback Modes

### Live

Events are streamed to the agent as they happen. The agent can respond with commentary in real-time.

```typescript
YaverFeedback.init({
  agentUrl: 'http://192.168.1.10:18080',
  authToken: 'your-token',
  feedbackMode: 'live',
  agentCommentaryLevel: 5, // Agent responds to what it sees
});
```

### Narrated

Record everything (screenshots, voice notes), then send the full bundle when you tap "Stop & Send". Good for walkthrough-style bug reports.

```typescript
YaverFeedback.init({
  agentUrl: 'http://192.168.1.10:18080',
  authToken: 'your-token',
  feedbackMode: 'narrated',
});
```

### Batch (default)

Collect everything and dump it all at the end when you tap "Send Report". The classic bug report flow.

```typescript
YaverFeedback.init({
  agentUrl: 'http://192.168.1.10:18080',
  authToken: 'your-token',
  feedbackMode: 'batch',
});
```

## Agent Commentary Levels

In live mode, the agent can comment on what it sees in real-time. Control verbosity with `agentCommentaryLevel` (0-10):

| Level | Behavior |
|-------|----------|
| 0 | Silent (default) |
| 1-3 | Only critical observations |
| 4-6 | Moderate commentary |
| 7-9 | Detailed observations and suggestions |
| 10 | Agent comments on everything it sees |

Commentary messages appear in a chat-like view inside the feedback modal.

## Voice-Driven Live Coding

In live mode, the feedback modal shows a "Speak to Fix" button. When you tap it:

1. Records your voice (uses `react-native-audio-recorder-player`)
2. Sends the recording to the agent as a `voice_command` event
3. The agent can transcribe and act on your instruction

This enables a hands-free workflow: see a bug, say what to fix, and the agent makes the change.

## Error Capture

Capture JS errors with full stack traces and attach them to feedback reports. The agent gets file names, line numbers, and optional context — goes straight to the right line.

**No conflicts with Sentry, Crashlytics, Bugsnag, or any other tool.** The SDK never auto-hooks global error handlers. You explicitly insert it into your error chain wherever you want.

### Option 1: Wrap the error handler (recommended)

```typescript
import { ErrorUtils } from 'react-native';

// Insert Yaver into the error chain — works alongside Sentry, Crashlytics, etc.
const existing = ErrorUtils.getGlobalHandler();
ErrorUtils.setGlobalHandler(YaverFeedback.wrapErrorHandler(existing));

// Other tools can still wrap after this. The chain stays intact:
// Sentry → Yaver wrapper → original RN handler
```

`wrapErrorHandler` returns a pass-through function that records the error in Yaver's ring buffer, then calls the next handler. It never swallows errors.

### Option 2: Manual attach (in catch blocks)

```typescript
try {
  await riskyOperation();
} catch (err) {
  YaverFeedback.attachError(err, {
    context: 'checkout-flow',
    userId: currentUser.id,
    cartItems: cart.length,
  });
  throw err; // still propagate
}
```

### What the agent receives

```json
{
  "errors": [
    {
      "message": "Cannot read property 'id' of undefined",
      "stack": [
        "at CheckoutButton.handlePress (CheckoutScreen.tsx:47)",
        "at processQueue (react-native/Libraries/Renderer/...)"
      ],
      "isFatal": false,
      "timestamp": 1742812200000,
      "metadata": {
        "context": "checkout-flow",
        "cartItems": 3
      }
    }
  ]
}
```

### API

| Method | Description |
|--------|-------------|
| `attachError(error, metadata?)` | Manually attach an error with optional context |
| `wrapErrorHandler(next?)` | Returns a pass-through handler for the error chain |
| `getCapturedErrors()` | Get the current error buffer |
| `clearCapturedErrors()` | Clear the error buffer |

## Configuration

```typescript
YaverFeedback.init({
  // Required
  authToken: 'your-token',                // Auth token for the agent

  // Optional
  agentUrl: 'http://192.168.1.10:18080',  // Agent URL (auto-discovered if omitted)
  trigger: 'shake',                        // 'shake' | 'floating-button' | 'manual'
  enabled: true,                           // Default: __DEV__ (auto-disabled in production)
  maxRecordingDuration: 120,               // Max recording duration in seconds (default: 120)
  feedbackMode: 'batch',                   // 'live' | 'narrated' | 'batch' (default: 'batch')
  agentCommentaryLevel: 0,                 // 0-10 (default: 0, only relevant in live mode)
  maxCapturedErrors: 5,                    // Error ring buffer size (default: 5)

  // Remote reload callbacks (from Yaver mobile app or another SDK device)
  onReload: () => { ... },                 // Called on hot reload command (default: DevSettings.reload())
  onReloadBundle: (url, assets) => { ... },// Called on native bundle rebuild command
});
```

## Running Inside the Yaver Mobile App (super-host)

When a third-party app is loaded through Yaver's Hermes-push flow (so the
Yaver mobile app is the runtime container for your app), the SDK detects
that situation via the `YaverInfo` native module and **automatically
no-ops `YaverFeedback.init()`** — no ShakeDetector, no FeedbackModal, no
BlackBox stream from inside the guest. The user only ever sees Yaver's
native shake overlay ("Reload" + "Back to Yaver") and uses Yaver's
built-in feedback flow instead.

Standalone installs (TestFlight / App Store / Play from your own dev
account) are unaffected — the SDK behaves exactly as it did before.

If you need to opt out of the auto-suppression in a specific build (for
example, to compare behaviour in a super-host smoke test), set
`enabled: true` explicitly — the Yaver-host gate runs before the
`enabled` flag is evaluated on purpose.

## Trigger Modes

### Shake (default)

Shake the device to open the feedback modal. Uses the built-in shake event on iOS and `ShakeEvent` on Android.

When the app is loaded inside the Yaver mobile app (super-host), the
SDK yields shake handling to Yaver's native overlay — see the
"Running Inside the Yaver Mobile App" section above.

```typescript
YaverFeedback.init({ authToken, trigger: 'shake' });
```

### Floating Button

A small draggable "Y" button overlays the app. Tap to open the feedback modal.

```tsx
import { FloatingButton, FeedbackModal, YaverFeedback } from 'yaver-feedback-react-native';

function App() {
  return (
    <>
      <YourApp />
      <FloatingButton onPress={() => YaverFeedback.startReport()} />
      <FeedbackModal />
    </>
  );
}
```

### Manual

Trigger feedback collection programmatically from anywhere in your app.

```typescript
import { YaverFeedback } from 'yaver-feedback-react-native';

// In a button handler, debug menu, etc.
YaverFeedback.startReport();
```

## P2P Client

For direct communication with the Yaver agent beyond feedback:

```typescript
import { P2PClient } from 'yaver-feedback-react-native';

const client = new P2PClient('http://192.168.1.10:18080', 'your-token');

// Health check
const isUp = await client.health();

// Get agent info
const info = await client.info();
// { hostname: 'MacBook-Air', version: '1.45.0', platform: 'darwin' }

// Upload feedback bundle
const reportId = await client.uploadFeedback(bundle);

// Builds
const builds = await client.listBuilds();
const build = await client.startBuild('ios');
const url = client.getArtifactUrl(build.id);

// Voice
const voiceCap = await client.voiceStatus();
const { text, provider } = await client.transcribeVoice('/path/to/audio.wav');

// Autonomous test sessions
const { sessionId } = await client.startTestSession();
const session = await client.getTestSession();
await client.stopTestSession();

// Update connection dynamically (e.g. after re-discovery)
client.setBaseUrl('http://10.0.0.2:18080');
client.setAuthToken('new-token');
```

## How It Works

1. User triggers feedback (shake, button tap, or manual call)
2. Feedback modal opens with mode selector (Live / Narrated / Batch)
3. User captures screenshots, records voice notes, or speaks commands
4. In live mode: events stream to the agent in real-time, agent can respond with commentary
5. In narrated/batch mode: everything is collected and uploaded on send
6. SDK uploads via multipart POST to `/feedback` (or streams to `/feedback/stream`)
7. The agent receives the report and can create a task from it

All data flows directly to your dev machine via the Yaver agent. Nothing goes through third-party servers.

## Development vs Production

By default, the SDK is only enabled when `__DEV__` is `true` (React Native's built-in dev mode flag). In production builds, the SDK is automatically disabled and all methods are no-ops.

Override this behavior:

```typescript
// Force enable in production (e.g., for internal beta testers)
YaverFeedback.init({ authToken, enabled: true });

// Disable at runtime
YaverFeedback.setEnabled(false);
```

## Requirements

- React Native >= 0.70
- React >= 18
- Yaver CLI running on your dev machine (`yaver serve`)
- Optional: `@react-native-async-storage/async-storage` for device discovery persistence
- Optional: `react-native-view-shot` for screenshots
- Optional: `expo-document-picker` for file uploads

## API Reference

### YaverFeedback

| Method | Description |
|--------|-------------|
| `init(config)` | Initialize the SDK with agent URL, auth token, and options |
| `startReport()` | Manually trigger the feedback modal (auto-discovers if needed) |
| `isInitialized()` | Check if the SDK has been initialized |
| `setEnabled(bool)` | Enable or disable at runtime |
| `isEnabled()` | Check if the SDK is currently enabled |
| `getP2PClient()` | Get the P2P client instance |
| `getFeedbackMode()` | Get the current feedback mode |
| `getCommentaryLevel()` | Get the agent commentary level (0-10) |
| `attachError(error, metadata?)` | Manually attach an error with optional context |
| `getCapturedErrors()` | Get the current captured errors buffer |
| `clearCapturedErrors()` | Clear the captured errors buffer |

### YaverDiscovery

| Method | Description |
|--------|-------------|
| `discover()` | Try stored connection, then scan LAN |
| `probe(url)` | Probe a specific URL for an agent |
| `connect(url)` | Connect and store for future sessions |
| `getStored()` | Get cached connection from storage |
| `store(result)` | Cache a discovery result |
| `clear()` | Clear stored connection |

### BlackBox

| Method | Description |
|--------|-------------|
| `start(config?)` | Start streaming events to the agent |
| `stop()` | Flush remaining events and stop |
| `isStreaming` | Whether streaming is active (getter) |
| `log(msg, source?, meta?)` | Log an info message |
| `warn(msg, source?, meta?)` | Log a warning |
| `error(msg, source?, meta?)` | Log an error |
| `captureError(err, isFatal?, meta?)` | Capture error with stack trace |
| `navigation(route, prevRoute?, meta?)` | Record screen navigation |
| `lifecycle(event, meta?)` | Record app lifecycle event |
| `networkRequest(method, url, status?, duration?, meta?)` | Record HTTP request |
| `stateChange(description, meta?)` | Record state mutation |
| `render(component, duration?, meta?)` | Record render event |
| `wrapConsole()` | Intercept console.log/warn/error |
| `unwrapConsole()` | Restore original console methods |
| `wrapErrorHandler(next?)` | Pass-through error handler with real-time streaming |
| `onCommand(handler)` | Register handler for agent commands (reload, etc.). Returns unsubscribe fn |
| `isCommandChannelConnected` | Whether the SSE command channel is connected (getter) |

### P2PClient

| Method | Description |
|--------|-------------|
| `health()` | Health check (returns boolean) |
| `info()` | Get agent hostname, version, platform |
| `uploadFeedback(bundle)` | Upload feedback bundle via multipart POST |
| `streamFeedback(events)` | Stream feedback events in live mode |
| `listBuilds()` | List available builds |
| `startBuild(platform)` | Start a build for the given platform |
| `getArtifactUrl(buildId)` | Get download URL for a build artifact |
| `voiceStatus()` | Get voice capability info |
| `transcribeVoice(audioUri)` | Send audio for transcription |
| `reloadApp(mode)` | Trigger remote reload: `'dev'` (hot reload) or `'bundle'` (rebuild + push) |
| `startTestSession()` | Start autonomous test session |
| `stopTestSession()` | Stop test session |
| `getTestSession()` | Get test session status + fixes |
| `setBaseUrl(url)` | Update connection URL |
| `setAuthToken(token)` | Update auth token |

### Components

| Component | Description |
|-----------|-------------|
| `FeedbackModal` | Modal with hot reload, vibing, screenshot or upload, and recording |
| `FloatingButton` | Draggable overlay button to trigger feedback |
| `YaverConnectionScreen` | Full-screen device discovery and connection UI |

### Helpers

| Function | Description |
|----------|-------------|
| `captureScreenshot()` | Capture the current screen (requires `react-native-view-shot`) |
| `startAudioRecording()` | Start recording a voice note |
| `stopAudioRecording()` | Stop recording, returns `{ path, duration }` |
| `uploadFeedback(url, token, bundle)` | Upload a feedback bundle to the agent |

## Agent Endpoints

The SDK communicates with these agent HTTP endpoints:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check (no auth) |
| `/feedback` | POST | Upload feedback bundle (multipart) |
| `/feedback/stream` | POST | Stream live feedback events |
| `/blackbox/events` | POST | Batch stream Black Box events |
| `/blackbox/command-stream` | GET | SSE command channel (agent pushes reload, etc.) |
| `/blackbox/subscribe` | GET | SSE live log stream |
| `/blackbox/context` | GET | Get generated prompt context |
| `/dev/reload-app` | POST | Trigger remote reload of SDK-connected apps |
| `/builds` | GET/POST | List or start builds |
| `/voice/status` | GET | Voice capability info |
| `/voice/transcribe` | POST | Send audio for transcription |
| `/test-app/start` | POST | Start autonomous test session |
| `/test-app/stop` | POST | Stop test session |
| `/test-app/status` | GET | Test session status + fixes |

## Architecture

```
┌──────────────────┐     HTTP (Bearer auth)     ┌──────────────────┐     HTTP/SSE      ┌──────────────────┐
│  Your App        │────────────────────────────►│  Yaver Agent     │◄────────────────── │  Yaver Mobile    │
│  + Feedback SDK  │  feedback, blackbox events  │  (Go CLI)        │  tasks, reload     │  App (phone)     │
│  + BlackBox      │  screenshots, voice, video  │  on your machine │  commands           │  vibe coding     │
│                  │◄────────────────────────────│                  │                    │                  │
│                  │  reload commands (SSE),     │  runs AI agent   │                    │                  │
│                  │  fixes, build status, voice │                  │                    │                  │
└──────────────────┘                             └──────────────────┘                    └──────────────────┘
       │                                                │                                       │
       │  Auth only                                     │  Auth only                             │
       ▼                                                ▼                                       ▼
┌──────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│                        Convex Backend                                                                    │
│  Token validation + device registry (no task data stored)                                                │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

All feedback data flows P2P between your app and the agent. The Yaver mobile app can trigger remote reloads that reach your app via the agent's command channel. Convex handles only auth and device discovery.

## License

MIT
