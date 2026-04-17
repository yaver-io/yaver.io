# yaver-errors

Zero-config error tracking that ships to your **own Yaver agent** — no SaaS, no third-party servers, no per-event billing.

## Install

```sh
npm install yaver-errors
```

## Use

```ts
import { init, captureException, setUser } from "yaver-errors";

init({
  dsn: "https://api.myapp.com/errors/ingest",   // your agent's public URL
  project: "myapp",
  env: "production",
});

// Automatic: window.onerror + unhandledrejection + console.error.
// Manual:
try { doThing(); } catch (e) { captureException(e); }

setUser("user_123");
```

Events land in your Yaver agent's **Errors** tab (dashboard or mobile Ops screen) — grouped by fingerprint, deduped, resolved/unresolved workflow.

## Why

- **You own the data.** Events go to your Hetzner/Mac Mini/NUC, not to a vendor.
- **No billing cliff.** Ingest as many events as your SQLite can hold (plenty).
- **Privacy by default.** URLs + user IDs + stack traces stay on your machines.

## API

- `init(opts)` — setup auto-capture + batching
- `captureException(err, extra?)` — manual capture
- `captureMessage(msg, extra?)` — capture non-thrown string
- `setUser(id)` — tag subsequent events with a user
- `setContext(ctx)` — merge data attached to every event
- `flush()` — send queued events now (e.g. before a navigation)

## License

AGPL-3.0-only. Part of [Yaver](https://yaver.io).
