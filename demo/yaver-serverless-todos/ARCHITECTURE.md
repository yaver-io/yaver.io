# Yaver Serverless Todo Architecture

## Goal

Provide Todo equivalents for every first-class Yaver demo platform without
using Convex as the app backend. The Todo UI should be boring and identical in
behavior; the important part is that every stack talks to the same Yaver-native
runtime API and can be deployed/moved as a Yaver Serverless bundle.

## Source Of Truth

The real data API already exists in the Go agent:

```text
/data/{slug}/{table}[/{id}]
```

For the Todo template, the table is `todos` and the durable fields are:

```text
id text primary key
title text not null
done bool default false
owner_id text
created_at timestamp default now
```

The app clients should not translate this into Convex-shaped functions. They
should call the HTTP data API directly through a tiny repository/client layer:

```text
Todo screen -> TodoRepository -> Yaver Serverless HTTP client -> /data/{slug}/todos
```

## Runtime Configuration

Each app needs only three runtime inputs:

| Name | Meaning |
|---|---|
| `baseUrl` | Agent or managed cloud origin, for example `http://127.0.0.1:18080` |
| `slug` | Yaver Serverless project slug, for example `todo-app` |
| `token` | Project-scoped `pp_...` API token, preferably write-enabled for demos |

Those values must be injected at runtime. They are not source constants.

## Why Separate Projects

Each platform needs a real standalone app because Yaver tests multiple runtime
paths:

- Web uses normal browser fetch and web preview.
- React Native / Expo can use Hermes bundle push.
- Flutter, Kotlin, and Swift use WebRTC remote-runtime surfaces.
- Native projects must be buildable without pulling in Yaver's own Convex
  control-plane code.

The separate app directories are intentionally thin. The shared behavior is the
contract, not a forced shared UI framework.

## Data Isolation

Yaver Serverless data is isolated by project slug and project token:

- A token minted for project A must not read project B.
- Read-only preview tokens can be used for friend/share flows.
- Write tokens are appropriate for owner editing demos.
- Owner bearer fallback is an operator path, not a shipped user app default.

Managed cloud should add provider-level isolation around the same runtime:

- one project workspace per app or tenant cell
- separate SQLite file per project
- no credentials inside the exported bundle
- export includes schema/seed/data only when requested
- deploy tokens scoped to a single project receive operation

## Cloud Placement

The apps are provider-neutral. Placement is selected by Yaver:

```text
client app -> Yaver Serverless public URL -> selected compute provider -> SQLite-backed runtime
```

Hetzner, AWS, GCP, Azure, and Alibaba can all host the runtime if they provide:

- Linux VM/container compute
- static or stable public URL behind TLS
- persistent disk or snapshot-backed data volume
- start/stop lifecycle controls
- firewall/security group rules for HTTP/WebRTC as needed
- provider API credentials stored outside source

The app should not know whether its backend is on Hetzner, AWS, GCP, Azure, or
Alibaba. At most it may display "Machine: AWS" or "Inference: BYO" as user
context.

## Cost Behavior

The Todo equivalents are designed for sleeping workspaces:

- The client treats `503`/network failure as "backend unavailable" and keeps the
  draft locally.
- Yaver can wake the workspace before opening the app.
- Idle workspaces can stop compute; the data remains on the project volume.
- The data API is stateless enough to move behind a wake proxy later.

No client should require a long-running websocket to keep data correct for the
MVP. Poll/refresh after mutations is enough for the demo and cheaper.

## Inference

These Todo apps do not require inference. A separate assistant layer may use
Bedrock, Gemini, Azure AI, OpenAI, DeepSeek-compatible APIs, Ollama, or BYO keys
to generate changes, but generated app runtime data still lands through Yaver
Serverless APIs. That keeps inference vendor choice separate from app hosting.

## Implementation Contract

Client behavior:

- `listTodos()` calls `GET /data/{slug}/todos?limit=100`
- `createTodo(title)` calls `POST /data/{slug}/todos`
- `setTodoDone(id, done)` calls `PATCH /data/{slug}/todos/{id}`
- `deleteTodo(id)` calls `DELETE /data/{slug}/todos/{id}`
- after each mutation, refresh from the backend

Backend response shape:

```json
{
  "rows": [
    {
      "id": "t1",
      "title": "Buy milk",
      "done": false,
      "owner_id": "alice",
      "created_at": "2026-07-21T00:00:00Z"
    }
  ],
  "nextCursor": ""
}
```

SQLite may encode booleans as `0`/`1`; clients normalize both numeric and
boolean values.
