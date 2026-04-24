# Headless Clients

Two Node/Bun-runnable npm packages expose the exact same HTTP / Convex / relay surfaces as Yaver's mobile app and web dashboard — without any native runtime. Together they let you automate anything the human-facing UIs can do.

| Package | Mirrors | Install |
|---|---|---|
| [`yaver-mobile-headless`](../mobile-headless/README.md) | The RN mobile app's lib (`mobile/src/lib/*`) | `npm install -g yaver-mobile-headless` |
| [`yaver-web-headless`](../web-headless/README.md) | The web dashboard's `web/lib/agent-client.ts` | `npm install -g yaver-web-headless` |

Both packages:
- Speak to the same Convex deployment (public: `https://perceptive-minnow-557.eu-west-1.convex.site`)
- Speak to the same Yaver agents over the same relay / tunnel / direct paths
- Use the same bearer token format and the same `YAVER_TOKEN` env var
- Print JSON on stdout, logs on stderr — safe to pipe into `jq` or a test harness

## When to use which

**Use `yaver-mobile-headless` when…**
- The automation would otherwise need a phone running the Yaver app
- You need LAN UDP beacon discovery
- You're working with phone-projects (the mobile-first mini backend sandbox)
- You're pushing a Hermes bundle into the Yaver container
- You're simulating the shake-to-reload gesture for Feedback SDK flows

**Use `yaver-web-headless` when…**
- The automation would otherwise need a browser open on the web dashboard
- You're starting / stopping / reloading a dev server (Vite, Next.js, Expo, Flutter) on a remote box
- You need the `__rp`-composed iframe URL that the web UI uses for its Hot Reload / Web Reload tabs
- You're exercising the "Reconnect & Fix" recovery path (`/settings/repair-relay` + stop + clear-cache + restart)
- You're dispatching vibing tasks (`createTask`) against a remote workDir

**Mix both** when the automation spans surfaces — e.g. sign in once, tail dev-server SSE logs via `yaver-web-headless`, then fire a phone-side gesture via `yaver-mobile-headless`.

## Authentication flow (shared by both)

1. Both packages support email/password against Convex:
   ```bash
   export YAVER_TOKEN=$(yaver-web-headless sign-in --email=you@example.com --password=... | jq -r .token)
   ```
   Today the headless CLIs do not run popup/native OAuth flows; use email/password or inject an existing bearer token.
2. Export `YAVER_TOKEN` once; every subsequent command picks it up automatically.
3. `yaver-web-headless whoami` or `yaver-mobile-headless config` both confirm the session.

## Connection flow (shared by both)

1. `connect <deviceId>` on either package does the same probe ladder the real UIs run:
   - **relay servers first** (works across networks, only path that survives CGNAT/VPN/LAN moves)
   - **user-supplied tunnel URLs** (Cloudflare Access / ngrok / Tailscale funnel)
   - **direct** (same LAN / same host) — only when `--agent=http://host:18080` is passed
2. The first successful probe wins; `baseUrl` and `connectionMode` are populated.
3. Every subsequent agent method routes through that base URL with `X-Relay-Password` headers set correctly (for relay paths) or nothing (for direct).

## Contract parity matrix

These tables show which methods are guaranteed-mirrored between the real UIs and the headless packages. If you need something not listed, file an issue — the headless packages are meant to be a 1:1 drop-in for automation, not a subset.

### `yaver-web-headless` ↔ `web/lib/agent-client.ts`

| Web dashboard action | Web method | Headless verb | Headless method |
|---|---|---|---|
| Sign in / out / me | `/auth/{login,logout,me}` | `sign-in`, `sign-out`, `whoami` | `signIn`, `signOut`, `whoami` |
| List devices | `GET /devices/list` | `devices` | `listDevices` |
| Connect to device | `connect()` (relay-first) | `connect <deviceId>` | `connect(deviceId)` |
| Re-auth the box | `reauthAgent` | `reauth <deviceId>` | `reauthAgent` |
| Hot Reload / Web Reload preview URL | `devPreviewUrl` | `webview-url` | `devPreviewUrl` |
| Start dev server | `startDevServer` | `dev-start --framework --work-dir` | `startDevServer` |
| Stop dev server | `stopDevServer` | `dev-stop` | `stopDevServer` |
| Reload dev server | `reloadDevServer` | `dev-reload` | `reloadDevServer` |
| Repair user relay password | `onRepairRelay` → `/settings/repair-relay` | `repair-relay` | `repairRelay` |
| "Reconnect & Fix" recovery | `handleReconnect` in PreviewPane | `reconnect --device` | `reconnectAndFix` |
| Dispatch vibing task | `createTask` | `vibe --device <prompt>` | `createTask` |
| Task list / stop / continue | `listTasks`, `stopTask`, `continueTask` | `task-list`, `task-stop`, `task-continue` | same names |

### `yaver-mobile-headless` ↔ `mobile/src/lib/*`

| Mobile app action | Mobile method | Headless verb |
|---|---|---|
| Sign in (email / Apple) | `signInWithApple`, `signInWithEmail` | `sign-in` |
| Beacon discovery | `beacon.ts` UDP listen | auto-runs during `devices` |
| Connect | `quic.ts` connect | `connect` |
| Install catalogue (Ollama, runners, etc.) | `install.ts` | `install-list`, `install <id>` |
| Phone project create / list / delete | `phone-projects.ts` | `phone-project-*` verbs |
| Preview manifest emission | `preview-manifest.ts` | `preview-manifest-create` |
| Todo cloud bootstrap (one-shot) | composite | `todo-cloud-bootstrap` |

See each package's README for the full verb catalogue — the tables above show the guaranteed-mirrored subset.

## Common patterns

### Full hot-reload cycle against a remote box

```ts
import { WebClient } from "yaver-web-headless";

const web = new WebClient({ token: process.env.YAVER_TOKEN });
const [device] = await web.listDevices();
if (!(await web.connect(device.id)).ok) throw new Error("unreachable");

await web.startDevServer({
  framework: "vite",
  workDir: "/workspace/carrotbet/apps/web",
});

while (!web.devPreviewUrl) await new Promise((r) => setTimeout(r, 200));
console.log("iframe URL:", web.devPreviewUrl);

// Later, after a code change:
await web.reloadDevServer();
```

### Full recovery loop when the iframe 401s

```bash
yaver-web-headless reconnect --device=$DEVICE_ID
# Stderr streams: health → reconnect → repair → stop → clear cache → restart
# Stdout: JSON report of every step
```

### Scaffold + push a phone project from Node

```bash
yaver-mobile-headless phone-project-create \
  --name="Todo App" --template=todos \
  --prompt="Ship a mobile todo app backend"

yaver-mobile-headless phone-project-push \
  --slug=todo-app \
  --base-url=https://my-dev-box.example.com \
  --target-token=$CLOUD_OWNER_TOKEN \
  --include-data --containerize
```

## Hetzner test-box usage

The persistent `yaver-test-ephemeral` Hetzner box runs smoke checks that exercise the full Convex → relay → agent path end-to-end. The relay-password smoke script (`ci/remote/smoke/relay-password.sh`) uses plain curl for speed, but for richer regression coverage the yaver-web-headless and yaver-mobile-headless CLIs can be scheduled as additional systemd oneshots on the same pattern:

```bash
# Install both headless CLIs (reuses the same PATH fix the agent uses)
npm install -g yaver-web-headless yaver-mobile-headless

# Smoke the web-reload path
yaver-web-headless sign-up --email="smoke-$(date +%s)@yaver.test" --password=SmokeTest!pw \
  | jq -r .token > /tmp/web-token
export YAVER_TOKEN=$(cat /tmp/web-token)
yaver-web-headless devices
# expect at least one device back
```

See `ci/remote/smoke/relay-password.sh` for the analogous curl-only smoke that ships today; extending it with a web-headless / mobile-headless variant is additive, not a replacement.

## Development

Both packages live in this monorepo:

- `mobile-headless/` — built with bun, ships as `yaver-mobile-headless` on npm
- `web-headless/` — built with bun, ships as `yaver-web-headless` on npm

```bash
cd web-headless
bun install
bun test                # hermetic tests, no external services
bun run typecheck
bun run build           # dist/{cli,web-client}.js
```

```bash
cd mobile-headless
bun install
bun test
bun run typecheck
bun run build           # dist/{cli,mcp,mobile-client}.js
```

When adding a new method to `web/lib/agent-client.ts` or `mobile/src/lib/*`, mirror it in the corresponding headless package so the contract stays 1:1. A smoke test that calls the new verb — even a hermetic one — is enough to keep drift under control.

## See also

- [`mobile-headless/README.md`](../mobile-headless/README.md) — full mobile verb catalogue
- [`web-headless/README.md`](../web-headless/README.md) — full web verb catalogue
- [`MOBILE_WORKER.md`](../MOBILE_WORKER.md) — phone-first backend spec the mobile client mirrors
- [`AI_ARCH.md`](../AI_ARCH.md) — how auth, bootstrap, and relay flows actually work at runtime
