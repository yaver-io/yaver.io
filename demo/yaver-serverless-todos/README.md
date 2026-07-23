# Yaver Serverless Todo Equivalents

This directory contains the Todo app equivalents that use Yaver Serverless Lite
instead of Convex. Each app is a separate project, but all of them point at the
same portable backend contract:

```text
GET    /data/{slug}/todos
POST   /data/{slug}/todos
PATCH  /data/{slug}/todos/{id}
DELETE /data/{slug}/todos/{id}
```

Yaver itself can keep using Convex for its control plane. Apps built by users
should not need Convex. They should use the Yaver Serverless data API so the
same project can move between phone sandbox, self-hosted machines, and Yaver
managed cloud.

## Projects

| Path | Platform | Backend |
|---|---|---|
| `apps/web-next` | Next.js web | Yaver Serverless Lite HTTP data API |
| `apps/rn-expo` | React Native / Expo | Yaver Serverless Lite HTTP data API |
| `apps/flutter` | Flutter | Yaver Serverless Lite HTTP data API |
| `apps/android-kotlin` | Native Android / Kotlin | Yaver Serverless Lite HTTP data API |
| `apps/ios-swift` | Native iOS / SwiftUI | Yaver Serverless Lite HTTP data API |
| `packages/js-client` | Shared TypeScript client | Yaver Serverless Lite HTTP data API |

## Run a backend

Create a Yaver Serverless Lite project with the built-in todos template, then
mint a project API token:

```bash
yaver serve
# create/deploy a todos project through the app, CLI, or MCP
# mint a project token through /phone/projects/tokens or the Yaver UI
```

Point demos at that backend:

```bash
export YAVER_SERVERLESS_URL=http://127.0.0.1:18080
export YAVER_SERVERLESS_SLUG=todo-app
export YAVER_SERVERLESS_TOKEN=pp_todo-app_example
```

Mobile apps also expose these as in-app fields so a phone/simulator can point
at a local agent, a remote runner, or a Yaver managed cloud target.

## Security Rule

Do not commit real `pp_` project tokens, owner bearer tokens, provider
credentials, account IDs, hostnames, customer IPs, or cloud billing identifiers.
The checked-in files use placeholders only. Runtime config belongs in local env,
device settings, QR pairing, or Yaver's encrypted credential store.

## Backend Bundle

The backend shape for these demos is documented by:

- `yaver.serverless.yaml`
- `schema.yaml`
- `seed.json`

Those files are intentionally provider-neutral. The same backend can be hosted
on Hetzner, AWS, GCP, Azure, Alibaba later, or a user-owned machine as long as
the target exposes the Yaver Serverless Lite data API.
