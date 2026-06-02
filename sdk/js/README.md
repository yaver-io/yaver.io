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
