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
