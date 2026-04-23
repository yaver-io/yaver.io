# yaver-mobile-headless

A headless surrogate for the Yaver mobile app. Shares the exact code
from `mobile/src/lib/*` (auth, QUIC client, beacon, wizard, guests,
phone-projects, dev-server, install catalogue) via TS path aliases
into thin shims for the few native modules the lib actually touches.

Three surfaces, one facade:

- **Programmatic** — `import { MobileClient } from "yaver-mobile-headless"` in a jest/bun test.
- **CLI** — `yaver-mobile-headless <command>`. Every screen action has a verb, JSON on stdout.
- **MCP** — `yaver-mobile-headless mcp` exposes `mobile_tap_*` and `mobile_api_*` tools over stdio.

For the full design, see `MOBILE_HEADLESS.md` at the repo root.

## Quick start

```bash
cd mobile-headless
bun install
bun test test/hermetic.test.ts      # no external services needed

# drive a real agent
bun run src/bin/cli.ts sign-in --token=...
bun run src/bin/cli.ts devices
bun run src/bin/cli.ts install-list
bun run src/bin/cli.ts install ollama

# create a phone-backed todo app locally, then push its backend to a remote Yaver/Hetzner box
bun run src/bin/cli.ts phone-project-create --name="Todo App" --template=todos --prompt="Ship a mobile todo app backend"
bun run src/bin/cli.ts phone-project-push --slug=todo-app --base-url=https://your-box.example.com --target-token=$CLOUD_OWNER_TOKEN --include-data --containerize

# same flow as one command
bun run src/bin/cli.ts todo-cloud-bootstrap --name="Todo App" --base-url=https://your-box.example.com --target-token=$CLOUD_OWNER_TOKEN --prompt="Deploy the backend to my paid cloud box"

# emit a Hermes preview manifest with git + CI metadata for reopening inside Yaver
bun run src/bin/cli.ts preview-manifest-create \
  --name="Todo Preview" \
  --bundle-url=https://your-box.example.com/dev/index.bundle?platform=ios&dev=true \
  --repo-url=https://github.com/you/todo-app \
  --branch=main \
  --commit=abc1234 \
  --workflow=yaver-hermes \
  --run-id=123456789 \
  --compile-time-injected \
  --guest-visible \
  --out .yaver/preview-manifest.json
```

## Env knobs

| Var | Meaning |
|---|---|
| `YMH_DATA_DIR` | Where async-storage + secure-store are persisted. Set per-test for isolation. |
| `YMH_PLATFORM` | `"ios"` or `"android"` — `Platform.OS` in the lib reads this. |
| `YMH_DEVICE_NAME` | Device name surfaced to the agent. |
| `YMH_CONVEX_URL` | Override Convex site URL (defaults to prod). |
| `YMH_AUTH_TOKEN` | Pre-seed the auth token so MCP/CLI don't need `sign-in` first. |

## Shim coverage

Resolved via `tsconfig.json` `paths`:

- `react-native` → Platform / Alert / AppState / NativeModules stubs
- `react-native-udp` → wraps Node `dgram` (beacon actually works)
- `@react-native-async-storage/async-storage` → JSON file under `$YMH_DATA_DIR`
- `expo-secure-store` → chmod-600 JSON file under `$YMH_DATA_DIR`
- `expo-device`, `expo-constants`, `expo-application` → env-overridable constants
- `expo-apple-authentication`, `expo-auth-session`, `expo-web-browser` → stubs (headless uses direct-token sign-in)
- `expo-crypto` → `node:crypto`

If a new lib file imports a module not listed above, add a matching
shim to `src/shims/` and wire it in `tsconfig.json`. Never fork the
mobile source.

## CI

`.github/workflows/mobile-headless.yml` runs two jobs on every PR
that touches `mobile-headless/`, `mobile/src/lib/`, or `desktop/agent/`:

1. **hermetic** — typecheck + `bun test test/hermetic.test.ts` against the in-process mock agent.
2. **smoke-local** — builds the Go agent, runs it in background, drives a handful of CLI commands against it.

Both on free GitHub-hosted `ubuntu-latest` runners.
