# yaver-web-headless

A headless surrogate for the Yaver web dashboard — drive every
dashboard feature (dev-server preview, webview reload, vibing,
reconnect-and-fix, repair-relay, OAuth / email auth) from a Node
script, a CLI, or an MCP tool call. Same HTTP + Convex contract
as `web/lib/agent-client.ts`.

Two surfaces, one facade:

- **Programmatic** — `import { WebClient } from "yaver-web-headless"` in a bun / node / jest test.
- **CLI** — `yaver-web-headless <command>`. Every dashboard action has a verb, JSON on stdout.

If your automation needs the *mobile* app (beacon discovery, Apple
Sign-In, phone-project mgmt, Hermes bundle push), use
[`yaver-mobile-headless`](../mobile-headless/README.md) instead. The
two packages share nothing at runtime but follow the same shape:
`connect(deviceId)` → agent methods → JSON output.

## Quick start

```bash
cd web-headless
bun install
bun test test/hermetic.test.ts             # no external services needed

# sign in to Convex, list devices
bun run src/bin/cli.ts sign-in --email=you@example.com --password='...'
export YAVER_TOKEN=$(bun run src/bin/cli.ts sign-in --email=... --password=... | jq -r .token)
bun run src/bin/cli.ts devices

# connect to a device (relay → tunnel → direct)
bun run src/bin/cli.ts connect $DEVICE_ID

# start a Vite dev server on the box
bun run src/bin/cli.ts dev-start --device=$DEVICE_ID --framework=vite --work-dir=/workspace/myapp

# print the URL the web UI would iframe
bun run src/bin/cli.ts webview-url --device=$DEVICE_ID

# vibe a code change — creates a task on the current workDir
bun run src/bin/cli.ts vibe --device=$DEVICE_ID 'add a signup page'

# full recovery loop (matches "Reconnect & Fix" in the Hot Reload tab)
bun run src/bin/cli.ts reconnect --device=$DEVICE_ID

# last-resort fix when the relay keeps saying "invalid relay password"
bun run src/bin/cli.ts repair-relay
```

## CLI verbs

| Verb | Purpose |
|---|---|
| `sign-in`, `sign-up`, `sign-out`, `whoami` | Convex auth (mirrors `/auth/{login,signup,logout,me}`) |
| `devices` | List visible devices for the current user |
| `connect <deviceId>` | Probe relay → tunnel → direct, report which path worked |
| `reauth <deviceId>` | Hand the box a fresh owner session via relay (no ssh needed) |
| `repair-relay` | Re-sync `userSettings.relayPassword` with the platform default |
| `dev-status`, `dev-start`, `dev-stop`, `dev-reload` | `/dev/*` lifecycle on the agent |
| `webview-url --device=<id>` | Print the iframe-ready preview URL (honours relay `__rp`) |
| `vibe --device=<id> <prompt>` | Dispatch a coding task against the connected workDir |
| `task-list`, `task-stop`, `task-continue` | Task mgmt |
| `reconnect --device=<id>` | Full Reconnect-and-Fix: health → reconnect → repair → stop → clear cache → restart → refresh |
| `config` | Print loaded config (token redacted) |

## Programmatic example — drive a full hot-reload cycle

```ts
import { WebClient } from "yaver-web-headless";

const web = new WebClient({ token: process.env.YAVER_TOKEN });
const [device] = await web.listDevices();
const connected = await web.connect(device.id);
if (!connected.ok) throw new Error("could not reach device");

await web.startDevServer({ framework: "vite", workDir: "/workspace/carrotbet/apps/web" });

// `devPreviewUrl` returns null until the relay password is populated.
// Poll once before driving a browser / WebView against it.
while (!web.devPreviewUrl) await new Promise((r) => setTimeout(r, 200));
console.log("open in your browser:", web.devPreviewUrl);

// Reload after a code change.
await web.reloadDevServer();
```

## Environment variables

| Var | Purpose |
|---|---|
| `YAVER_CONVEX_URL` | Convex site URL (default: `https://perceptive-minnow-557.eu-west-1.convex.site`) |
| `YAVER_TOKEN` | Bearer token. Every CLI command picks this up automatically. |

## How this differs from `yaver-mobile-headless`

| Capability | web-headless | mobile-headless |
|---|:-:|:-:|
| Devices / Convex auth | ✓ | ✓ |
| Connect (relay-first) | ✓ | ✓ |
| Dev-server preview (Vite / Next / Expo / Flutter) | ✓ | — |
| `webview-url` with `__rp` composition | ✓ | — |
| Repair user relay password | ✓ | — |
| Reconnect & Fix orchestration | ✓ | — |
| Vibing / task creation | ✓ | ✓ |
| Beacon (LAN UDP) discovery | — | ✓ |
| Phone-project mgmt | — | ✓ |
| Hermes bundle push | — | ✓ (see `yaver-cli`) |

If an automation mixes surfaces (a test that signs in as a user,
tails dev server logs from web-headless, and drives a shake-to-fix
gesture via mobile-headless), just import both packages in the
same script — they don't conflict.

## Package layout

```
web-headless/
├── src/
│   ├── web-client.ts        WebClient (programmatic API)
│   └── bin/cli.ts           yaver-web-headless CLI
├── test/hermetic.test.ts    In-process mock of Convex + agent
├── scripts/fix-shebang.mjs  Rewrites the built CLI shebang to node
├── package.json
├── tsconfig.json
├── bunfig.toml
└── README.md (this file)
```

## Development

```bash
bun install
bun test                    # hermetic tests (no external services)
bun run typecheck           # tsc --noEmit
bun run build               # dist/cli.js + dist/web-client.js (minified)
```

Publish: same flow as `yaver-mobile-headless` — `npm publish` from
the built `dist/` after `bun run build`.

## License

FSL-1.1-Apache-2.0 (matches the rest of Yaver).
