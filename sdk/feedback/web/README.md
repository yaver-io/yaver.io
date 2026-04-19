# yaver-feedback-web

Visual feedback SDK for web apps — record screen + voice, take screenshots, send bug reports to your Yaver dev machine. The AI agent receives the report and fixes the bugs automatically.

## Install

```bash
npm install yaver-feedback-web
```

Or via the umbrella CLI in any web project root:

```bash
npm install -g yaver-cli
yaver feedback setup
```

## Quick Start (0.2+: native sign-in)

```typescript
import { YaverFeedback } from 'yaver-feedback-web';

// Initialize in development only
if (process.env.NODE_ENV === 'development') {
  YaverFeedback.init({
    trigger: 'floating-button', // or 'keyboard' (Ctrl+Shift+F)
  });
}
```

That's it — no auth token required. The first time the user clicks the floating button (or presses the keyboard shortcut), a compact sign-in modal opens with five OAuth providers (**Apple / Google / GitHub / GitLab / Microsoft**) and email + password. Each provider opens in a popup window that closes itself once authentication completes; the SDK persists the session token in `localStorage` so subsequent reports go straight through.

If your app already authenticates against yaver.io and you want to pass a ready token (e.g. from your own server), opt out of the modal:

```typescript
YaverFeedback.init({
  authToken: 'your-token',
  autoLogin: false,
});
```

A small "Y" button appears in the corner. Click it to:
1. Record your screen + microphone
2. Take annotated screenshots
3. Send the report to your Yaver agent

The AI agent gets screen recordings, voice transcripts, console errors, and a timeline — then fixes the bugs and hot-reloads.

## Auto-Discovery

The SDK automatically finds your Yaver agent on the local network. No manual URL configuration needed — just run `yaver serve` on your dev machine.

```typescript
// Explicit URL (optional — auto-discovers if not set)
YaverFeedback.init({
  agentUrl: 'http://192.168.1.100:18080',
  authToken: 'your-token',
});
```

## Connection Widget

For more control, use the `FeedbackWidget` component in your dev tools panel:

```typescript
import { FeedbackWidget } from 'yaver-feedback-web';

// Mount in your dev settings page
FeedbackWidget.mount(document.getElementById('yaver-panel'));
```

Shows: connection status, agent discovery, manual URL input, and feedback controls.

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

// Programmatic recording
YaverFeedback.startRecording(): Promise<void>
YaverFeedback.captureScreenshot(annotation?: string): void
YaverFeedback.addAnnotation(text: string): void
YaverFeedback.stopAndSend(): Promise<string | null>  // returns report ID

// Discovery
YaverDiscovery.discover(): Promise<DiscoveryResult | null>
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

1. You test your web app and find a bug
2. Click the feedback button (or press Ctrl+Shift+F)
3. Record your screen while narrating the issue
4. The SDK sends everything to your Yaver agent via HTTP multipart
5. Run `yaver feedback fix <id>` — the AI agent generates a fix task
6. Agent fixes the code, you see the changes via hot reload

## Development Only

The SDK auto-disables in production (`process.env.NODE_ENV !== 'development'`). You can also control it manually:

```typescript
YaverFeedback.init({ enabled: false }); // explicitly disable
```

## Requirements

- Yaver CLI running on your dev machine (`yaver serve`)
- Modern browser with `getDisplayMedia` support (Chrome 72+, Firefox 66+, Edge 79+)
- HTTPS or localhost (required by `getDisplayMedia`)

## License

MIT — part of the [Yaver](https://yaver.io) open-source project.
