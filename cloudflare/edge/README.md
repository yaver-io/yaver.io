# Yaver Edge

The full product boundary, use cases, entitlement rules, security model, and
rollout checks live in
[`docs/architecture/CLOUDFLARE_EDGE.md`](../../docs/architecture/CLOUDFLARE_EDGE.md).

Yaver Edge is the Cloudflare-hosted identity and automatic relay fallback. It
preserves the existing public API contract:

- `GET /auth/validate`
- `POST /auth/refresh`
- `POST /devices/register`
- `GET /devices/list`
- `GET /presence?ids=...` (authenticated and access-filtered)
- `GET|POST|… /d/{deviceId}/{agent-path}`
- `GET /agent/tunnel/ws` (agent-only WebSocket registration)

Users do not need a Cloudflare account, `cloudflared`, DNS configuration, an
inbound firewall rule, or a tunnel credential. `yaver serve` opens the outbound
connection with the user's Yaver bearer and the registered device key.

The edge stores no projects, task content, terminal output, file paths, vault
data, model prompts/responses, or tunnel payloads. D1 is limited to identity,
hashed sessions, public device routing identity, access grants, and plan
entitlement. Each device is isolated in a Durable Object whose name is an HMAC
of its device ID. The target Go agent remains the final authorization boundary.

Free accounts may own two devices by default. Set
`FREE_OWNED_DEVICE_LIMIT` to a bounded non-negative integer to tune that cap;
existing same-owner devices may always reconnect, and shared devices do not
consume the recipient's allowance. Relay Pro is uncapped at this edge layer.

Local validation:

```bash
npm install
npm test
npm run typecheck
npx wrangler d1 migrations apply yaver-edge --local
npm run dev
```

`DEVICE_ID_HMAC_KEY` is a production secret and must be installed with the
canonical deploy flow; it must never be committed.

The Worker has explicit CPU and subrequest ceilings, uses hibernating
WebSockets, and stores no traffic counters in the request path. Configure
account billing notifications before opening the edge to a public cohort.
