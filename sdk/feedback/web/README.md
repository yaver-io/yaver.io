# yaver-feedback-web

Visual feedback SDK for web apps. Record the bug, trigger reload, start vibing tasks, and stay connected to a Yaver agent running on your dev machine or over relay.

## Install

```bash
npm install yaver-feedback-web
```

Or via the umbrella CLI in any web project root:

```bash
npm install -g yaver-cli
yaver feedback setup
```

## Quick Start

```typescript
import { YaverFeedback } from 'yaver-feedback-web';

// Initialize in development only
if (process.env.NODE_ENV === 'development') {
  YaverFeedback.init({
    trigger: 'floating-button', // or 'keyboard' (Ctrl+Shift+F)
  });
}
```

That is enough for the common path. The first time the user clicks the floating button, the SDK:

1. opens the in-app sign-in modal
2. lets the user choose one of their reachable Yaver machines
3. discovers the agent directly on LAN or through relay
4. keeps a command stream open so reload/status messages can come back into the browser app

Sign-in uses popup OAuth for **Apple / Google / GitHub / GitLab / Microsoft** plus email/password. The issued session token and selected device are cached in `localStorage`.

If your app already authenticates against yaver.io and you want to pass a ready token (e.g. from your own server), opt out of the modal:

```typescript
YaverFeedback.init({
  authToken: 'your-token',
  autoLogin: false,
});
```

A small `Y` button appears in the corner. From there the web SDK can:

1. record screen + microphone
2. capture screenshots and notes
3. send reports to `/feedback`
4. trigger hot reloads
5. start vibing tasks on the connected agent

## Discovery And Device Picking

If the user is signed in, discovery is account-aware:

- fetch reachable devices from Yaver
- prefer the selected device
- probe direct LAN addresses first
- fall back to relay when direct probing fails

If you already know the target agent URL, you can still pass it directly:

```typescript
// Explicit URL (optional — auto-discovers if not set)
YaverFeedback.init({
  agentUrl: 'http://192.168.1.100:18080',
  authToken: 'your-token',
});
```

## Remote Control

The web SDK now exposes the same core control loop the mobile feedback SDK uses:

```typescript
await YaverFeedback.reloadApp('dev');

const eligibility = await YaverFeedback.getVibingEligibility();
if (eligibility.canVibe) {
  await YaverFeedback.vibing('Fix the checkout form validation bug.');
}
```

Agent-driven commands come back over `/blackbox/command-stream`. By default:

- `reload` -> `window.location.reload()`
- `reload_bundle` -> `window.location.reload()`
- `status` -> `onStatus` callback and a `window` event named `yaver-feedback:status`

Override those defaults if your host app wants custom behavior:

```typescript
YaverFeedback.init({
  trigger: 'floating-button',
  onReload: () => router.refresh(),
  onStatus: (status) => {
    console.log(status.phase, status.message, status.progress);
  },
});
```

## Connection Widget

For more control, use the `FeedbackWidget` component in your dev tools panel:

```typescript
import { FeedbackWidget } from 'yaver-feedback-web';

// Mount in your dev settings page
FeedbackWidget.mount(document.getElementById('yaver-panel'));
```

Shows: connection status, discovery/manual connect, bug report controls, reload, and vibing.

## Trigger Modes

| Mode | How it works |
|------|-------------|
| `floating-button` | Small draggable button in corner (default: bottom-right) |
| `keyboard` | Keyboard shortcut (default: Ctrl+Shift+F) |
| `manual` | Call `YaverFeedback.startReport()` from your own UI |

## API

```typescript
// Initialize
YaverFeedback.init(config?: FeedbackConfig): Promise<void>

// Manual trigger
YaverFeedback.startReport(): void

// Remote control
YaverFeedback.reloadApp(mode?: 'dev' | 'bundle'): Promise<ReloadAck>
YaverFeedback.getVibingEligibility(): Promise<{ canVibe: boolean; ... }>
YaverFeedback.vibing(prompt: string): Promise<{ taskId: string }>

// Programmatic recording
YaverFeedback.startRecording(): Promise<void>
YaverFeedback.captureScreenshot(annotation?: string): void
YaverFeedback.addAnnotation(text: string): void
YaverFeedback.stopAndSend(): Promise<string | null>  // returns report ID

// Discovery
YaverDiscovery.discover(options?): Promise<DiscoveryResult | null>
YaverDiscovery.connect(url: string): Promise<DiscoveryResult | null>
```

## Error Capture

Capture JS errors with full stack traces and attach them to feedback reports.

**No conflicts with Sentry, Bugsnag, or any other tool.** The SDK never auto-hooks `window.onerror` or `unhandledrejection`. You explicitly insert it into your error chain.

### Wrap the error handler

```typescript
// Insert Yaver into the error chain
window.addEventListener('error', (event) => {
  YaverFeedback.attachError(event.error);
});
window.addEventListener('unhandledrejection', (event) => {
  YaverFeedback.attachError(event.reason);
});
```

### Manual attach

```typescript
try {
  await riskyOperation();
} catch (err) {
  YaverFeedback.attachError(err, { context: 'checkout', cartItems: 3 });
  throw err;
}
```

## What Gets Captured

- Screen recording (WebM/VP9, via `getDisplayMedia`)
- Microphone audio (WebM/Opus, for voice annotations)
- Screenshots with annotations
- Console errors (automatically)
- JS errors with stack traces (when `captureErrors` is enabled)
- Page URL and browser info
- Timeline of all events

## How It Works

1. You test your web app and hit a bug
2. Open the SDK, sign in, and connect to the right machine
3. Record, screenshot, or type a vibing prompt
4. The SDK talks to the agent over HTTP plus command-stream SSE
5. The agent can fix, reload, and stream status back into the app

## Development Only

The SDK auto-disables in production (`process.env.NODE_ENV !== 'development'`). You can also control it manually:

```typescript
YaverFeedback.init({ enabled: false }); // explicitly disable
```

## Requirements

- Yaver CLI running on your dev machine (`yaver serve`)
- Modern browser with `getDisplayMedia` support (Chrome 72+, Firefox 66+, Edge 79+)
- HTTPS or localhost (required by `getDisplayMedia`)
- A signed-in Yaver account if you want account-backed discovery, shared machines, relay routing, or vibing

## License

MIT — part of the [Yaver](https://yaver.io) open-source project.
