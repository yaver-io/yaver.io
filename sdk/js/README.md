# yaver-sdk

Embed Yaver's local-first agent runtime into your JavaScript and TypeScript applications. Works in React Native, Node.js, and browsers.

## Install

```bash
npm install yaver-sdk
```

## Quick Start

```typescript
import { YaverClient } from 'yaver-sdk';

const client = new YaverClient('http://localhost:18080', 'your-auth-token');

// Create a task
const task = await client.createTask('Fix the login bug');
console.log(`Task ${task.id} created`);

// Stream output
for await (const chunk of client.streamOutput(task.id)) {
  process.stdout.write(chunk);
}

// List all tasks
const tasks = await client.listTasks();
```

## Remote agents — the developer-API boundary

When the agent isn't on `localhost` (on-prem box / VPS), use the high-level
boundary instead of wiring transports yourself. Your app holds one secret (an
org Yaver account token) and never touches Yaver internals (Convex, relay
passwords, the transport ladder). The agent stays **private** — it dials out to
the relay; the client reaches it over the best path: direct-LAN → tailscale →
HTTPS tunnel → relay (P2P fallback).

**Server (`@yaver/server`)** — runs where the account secret is safe:

```typescript
import { YaverApp } from 'yaver-sdk';

const app = new YaverApp({ accountToken: process.env.YAVER_ACCOUNT_TOKEN! });

const devices = await app.listDevices();                 // reachable agents + presence
const status  = await app.status(deviceId, clientToken); // online / linked / runners / ready
const handle  = await app.sessionHandle(deviceId, clientToken); // opaque bundle for the client
// send `handle` to your client over your own authenticated endpoint
```

**Client (`@yaver/client`)** — browser / React Native:

```typescript
import { connectHandle } from 'yaver-sdk';

const handle  = await fetch('/your/yaver-session').then((r) => r.json());
const session = await connectHandle(handle);             // picks the best transport
const task    = await session.createTask('Fix the login bug', { runner: 'claude' });
for await (const chunk of session.streamOutput(task.id)) process.stdout.write(chunk);
```

Use a scoped, least-privilege `clientToken` (per-device / short-lived) — the
account token stays on the server. The agent runs the runner with whatever MCP
servers you've configured on the box, so your AI uses your own tools on-prem.

## Policy + multi-provider runtime (the "OpenRouter of coding agents")

Yaver wraps many **runners** (claude-code / codex / opencode / aider) and, via
OpenCode BYOK, many **providers** (anthropic / openai / openrouter / gemini /
ollama / salad / on-prem vLLM). A team policy decides *which* a given role may
use; the resolver projects that onto a concrete runtime for a unit of work.

```typescript
const app = new YaverApp({ accountToken });

// Read / write the generic team policy (runners, provider catalog, work kinds,
// per-role caps, approvals, data policy). No secrets ever stored.
const { options } = await app.getPolicy(teamId);
await app.setPolicy(teamId, options);

// Resolve a runtime for a unit of work (any app-defined workKind string).
const resolved = await app.resolve({ teamId, workKind: 'app-code', requestedRunner: 'codex' });
//   → { runner, model, provider, runtime.deviceId, approvals, nextActions, … }

// One call: resolve + mint a scoped token + build the client handle.
const handle = await app.resolvedHandle({ teamId, workKind: 'app-code', source: 'api' });
```

Apps stay out of the Yaver core: a consumer registers its own work kinds, role
caps, and provider catalog via `options.appProfile` (`AppProfile`) instead of
baking app vocabulary into Yaver. Talos's `harness-cad` / `robot-trial` are just
one profile.

### Composable ACL — jointly inclusive, never forcing

The team policy is **one layer**. It composes with Yaver's existing layers
(guest grants, SDK-token scopes, host-share policy, peer ACL, and the user's own
prefs) by **intersecting only the constraints each layer sets** — an absent
allowlist never narrows, and no layer is forced onto another:

```typescript
import { composeEntitlements, entitlementFromGuest, entitlementFromUser } from 'yaver-sdk';

// company allows codex+opencode, but this guest is capped to opencode → codex blocked.
const handle = await app.resolvedHandle(
  { teamId, workKind: 'app-code' },
  { entitlements: [
      entitlementFromGuest({ scope: 'full', allowedRunners: ['opencode'] }),
      entitlementFromUser({ allowedProviders: ['ollama'] }),
  ]},
);
// handle.effective.allowedRunners === ['opencode']
```

The effective runner scope is baked into the minted token; the **agent enforces
it authoritatively** — the client SDK only renders what's allowed.

## Features

- **Task management**: create, list, get, stop, delete, continue tasks
- **Async streaming**: `for await` output streaming
- **Auth client**: validate tokens, list devices, manage settings
- **Speech-to-text**: transcribe audio via OpenAI, Deepgram, AssemblyAI
- **Verbosity control**: set response detail level 0-10
- **Full TypeScript types**: all types exported
- **Works everywhere**: React Native, Node.js 18+, modern browsers

## Auth Client

```typescript
import { YaverAuthClient } from 'yaver-sdk';

const auth = new YaverAuthClient('your-token');
const user = await auth.validateToken();
const devices = await auth.listDevices();
```

## Speech

```typescript
import { transcribe } from 'yaver-sdk';

const result = await transcribe(audioUri, 'openai', 'sk-...');
console.log(result.text);
```

## Links

- [Yaver](https://yaver.io) — main site
- [GitHub](https://github.com/kivanccakmak/yaver.io) — source code
- [SDK docs](https://github.com/kivanccakmak/yaver.io/tree/main/sdk) — all SDKs
