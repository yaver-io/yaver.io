# Web UI Tunneling Audit

Date: 2026-04-24

Scope: audit the Yaver web dashboard for making tunnel / remote-connect setup easy for a user who wants one of:

- Cloudflare Tunnel
- Yaver relay
- managed Yaver relay
- self-hosted relay on VPS
- Tailscale

Question: what exists now, what does not, what should exist, and can the web UI realistically own it?

## Short answer

Yes, Yaver can support this well, but the web UI is not the source of truth today.

Today the system is split across:

- strong desktop-agent capabilities
- decent mobile configuration UX
- weak web configuration UX

The biggest gap is not raw backend capability. The biggest gap is that the web dashboard does not yet present a unified transport setup model.

## What already exists

### 1. Account-level transport settings exist in Convex

There is already a persisted user settings shape for:

- `relayUrl`
- `relayPassword`
- `tunnelUrl`
- `managed.relay`

Relevant files:

- `backend/convex/schema.ts`
- `backend/convex/userSettings.ts`
- `backend/convex/http.ts`

This means the platform already has a place to store the user's chosen transport defaults.

### 2. Web dashboard is relay-aware for actual device connection

The web dashboard loads platform relays from `/config`, merges in user relay password from `/settings`, and uses relay-first connection logic when attaching to a device.

Relevant files:

- `web/app/dashboard/page.tsx`
- `web/lib/agent-client.ts`

So the web app already depends on relay configuration. It is not transport-blind.

### 3. Desktop agent already has the important transport features

The desktop agent already supports:

- Cloudflare Tunnel config persisted in `~/.yaver/config.json`
- Cloudflare Tunnel wizard via `yaver tunnel cloudflare wizard`
- add/list/remove/test Cloudflare tunnels
- Tailscale detection and status
- relay expose endpoints
- relay config resolution from account settings and platform config
- public endpoint advertisement on device heartbeat

Relevant files:

- `desktop/agent/config.go`
- `desktop/agent/tunnel_cf_wizard.go`
- `desktop/agent/tailscale.go`
- `desktop/agent/expose_http.go`
- `desktop/agent/main.go`
- `desktop/agent/auth.go`

This is important: the capability is mostly already in the agent, not missing from the product.

### 4. Mobile already has a much better transport setup UX than web

The mobile settings screen already has:

- add/remove/test relay servers
- add/remove/test Cloudflare tunnels
- cloud sync toggle for relay/tunnel settings
- setup guide for Cloudflare, relay, and Tailscale

Relevant file:

- `mobile/app/(tabs)/settings.tsx`

This means the right product shape is already partially designed. Web can reuse this model instead of inventing a second one.

### 5. Managed relay backend exists

There is already backend support for:

- managed relay records
- provisioning a managed relay on Hetzner
- attaching a domain
- surfacing managed relay info in dashboard domain flows

Relevant files:

- `backend/convex/managedRelays.ts`
- `backend/convex/provisionRelay.ts`
- `web/components/dashboard/DomainsView.tsx`

So "Yaver relay free or VPS based or managed" is not hypothetical. Part of that backend exists now.

### 6. Device model already carries public transport hints

Devices can already expose:

- `tunnelUrl`
- `publicEndpoints`

Relevant files:

- `backend/convex/devices.ts`
- `mobile/src/context/DeviceContext.tsx`
- `web/lib/use-devices.ts`

This is enough to build a real transport-status UI per device.

## What the web UI does not have

### 1. No unified transport setup screen

There is no single "Connectivity" or "Remote Access" screen in web where the user chooses:

- same LAN only
- Tailscale
- Cloudflare Tunnel
- Yaver relay
- managed Yaver relay

Instead, the functionality is scattered or absent.

### 2. The existing web relay component appears unused

`web/components/dashboard/RelayServerView.tsx` exists, but it is not mounted anywhere in the web app.

That means even the narrow relay URL/password editor is effectively dead UI right now.

### 3. No Cloudflare Tunnel management in web

The web dashboard has no UI for:

- listing configured Cloudflare tunnels
- adding one
- testing one
- showing CF Access credentials
- showing which device is advertising which public endpoint
- running the Cloudflare wizard indirectly through the agent

Desktop and mobile can do parts of this. Web cannot.

### 4. No Tailscale visibility in web

The agent exposes `/machine/tailscale`, but the web `agentClient` does not use it, and the dashboard does not render:

- whether Tailscale is installed
- whether it is running
- the Tailscale IPs
- whether Tailscale is the recommended path

So the product mentions Tailscale, but web does not operationalize it.

### 5. No relay provisioning / onboarding flow in web

Backend managed-relay support exists, but the web UI does not provide a clear flow like:

1. Choose "Managed Yaver Relay"
2. Pick region
3. Provision
4. Wait for ready state
5. Save as active transport

The data model exists, but the user journey is incomplete.

### 6. No distinction between transport types and their ownership model

The current settings model is transport fields, not transport products.

What the user needs is something like:

- `LAN`
- `Tailscale`
- `Cloudflare Tunnel`
- `Yaver Relay (platform/free)`
- `Self-hosted Relay`
- `Managed Relay`

And for each one:

- where it runs
- who owns credentials
- whether Yaver can auto-configure it
- whether it needs a domain or VPS
- whether it is best for one device or many devices

Today that abstraction does not exist in web.

### 7. No "what should I use?" recommendation engine

The user asked for easy configuration. Easy requires opinionated recommendation logic.

Today web does not ask simple questions like:

- Are your phone and machine on the same Wi-Fi?
- Do you already use Tailscale?
- Do you own a domain on Cloudflare?
- Do you want free managed transport or your own VPS?
- Do you want Yaver to host the relay or do you want to host it?

Without that, users must understand the transport architecture themselves.

### 8. No safe handling of multi-device semantics in web UX

There is an existing backend safety rule: account-level `tunnelUrl` is only attached automatically when a host effectively resolves to one device.

Relevant file:

- `backend/convex/devices.ts`

This is correct, but the web UI does not explain it. If a user has multiple machines, a single account-level tunnel can be ambiguous. That needs explicit UI.

## What the web UI can do now without major backend work

These are realistic near-term features using existing APIs and existing agent capability.

### 1. Add a real "Connectivity" dashboard section

This should unify:

- current active transport
- recommended transport
- fallback order
- status per device

Input sources already exist:

- `/settings`
- `/config`
- device list
- agent connection diagnostics
- `/infra/summary`
- `/machine/tailscale`

### 2. Surface agent-side transport status

For the currently connected machine, web can show:

- LAN reachable
- relay endpoints configured
- Cloudflare public endpoints advertised
- Tailscale running/not running
- last connection path used

Most of the data already exists or is one thin `agentClient` method away.

### 3. Reuse the mobile settings model in web

Web should mirror the mobile transport setup model:

- list custom relays
- add relay URL/password
- test relay `/health`
- list Cloudflare tunnels
- add tunnel URL plus optional CF Access credentials
- test tunnel `/health`
- sync to account settings when desired

This is mostly product/UI work, not new architecture.

### 4. Expose the existing agent features through web

The agent already knows how to:

- detect Tailscale
- manage Cloudflare tunnel config
- test tunnel health

Web needs thin APIs in `agentClient` plus views, not reinvention.

### 5. Show "recommended path" per user situation

This can be done immediately in UI logic:

- if same LAN and device seen locally: recommend LAN
- if Tailscale running: recommend Tailscale
- if Cloudflare public endpoint exists: recommend Cloudflare Tunnel
- if relay configured: recommend relay
- otherwise show setup options

## What needs backend or agent work before web can be truly easy

### 1. Web cannot directly run local installers or CLI flows

A browser cannot itself:

- install `cloudflared`
- run `tailscale up`
- run `cloudflared tunnel login`
- create a named tunnel locally
- edit `~/.yaver/config.json`

That work must happen in the desktop agent.

So "easy setup from web" really means:

- web talks to connected agent
- agent performs local machine actions
- web shows progress and resulting config

### 2. Cloudflare wizard is CLI-driven, not HTTP-driven

Today the Cloudflare wizard is implemented as an interactive CLI flow.

Relevant file:

- `desktop/agent/tunnel_cf_wizard.go`

That is good enough for terminal use, but not for web orchestration. To make it easy from web, the wizard needs a non-interactive HTTP or task API.

### 3. Tailscale support is detect-only

Today Yaver detects Tailscale. It does not manage Tailscale onboarding.

That means web can show:

- installed or not
- running or not
- IPs

But it cannot honestly claim "one-click Tailscale setup" unless agent-side install and auth flows are added.

### 4. Relay setup is split between multiple models

There are currently several relay concepts:

- platform relay list from Convex
- user-selected relay URL/password in settings
- self-hosted relay docs/scripts
- managed relay provisioning records

These need one product model in web or the UI will stay confusing.

## Recommended product model

The web UI should stop presenting raw fields first. It should present choices first.

### Recommended top-level choices

1. `Local only`
2. `Tailscale`
3. `Cloudflare Tunnel`
4. `Yaver Relay`
5. `Managed Yaver Relay`

### For each choice, the web UI should answer

- What is it?
- When should I use it?
- Does it require a domain?
- Does it require a VPS?
- Can Yaver set it up for me?
- What gets stored in my account versus only on my machine?
- What happens if I have multiple devices?

### Recommended UI flow

1. Detect the current machine state
2. Recommend one transport
3. Offer advanced alternatives
4. Run or guide setup
5. Test connectivity
6. Save as preferred transport
7. Show fallback order

## Concrete implementation plan

### Phase 1: low-risk, mostly UI

- add `Connectivity` tab in web dashboard
- mount and replace the unused `RelayServerView`
- port the mobile relay/tunnel editor UX into web
- add `agentClient.getTailscaleStatus()`
- show device public endpoints and selected transport
- show "recommended transport" banner

This phase is clearly feasible now.

### Phase 2: thin agent API additions

- add HTTP endpoints for Cloudflare tunnel list/add/remove/test if not already exposed outside MCP
- add HTTP endpoint returning Cloudflare tunnel config plus public endpoints
- expose relay-expose list/start/stop via `agentClient`
- expose package/install status for `cloudflared` and `tailscale`

This phase is also feasible and incremental.

### Phase 3: true guided setup

- non-interactive Cloudflare setup API in agent
- optional agent-side install flows for `cloudflared`
- optional managed relay onboarding from web
- explicit "make this my primary remote transport" workflow

This is where the UX becomes genuinely easy for non-technical users.

## Main risks

### 1. Ambiguous account-level transport config

A single `tunnelUrl` or `relayUrl` at account level does not map cleanly to many machines.

Recommendation:

- keep account-level defaults
- add per-device transport assignment in web

### 2. Browser-only expectations

If the web UI implies it can configure local networking directly, users will hit a wall.

Recommendation:

- clearly label actions as:
  - `saved to account`
  - `requires connected agent`
  - `runs on your machine`

### 3. Two product models drifting apart

Mobile already has a richer transport UX than web. If web builds a different mental model, support burden rises.

Recommendation:

- reuse mobile naming and flow
- make the backend shape consistent across mobile, web, and CLI

## Final assessment

### What we have

- strong agent capability
- usable mobile transport setup
- existing backend settings and managed-relay models
- relay-aware web connection logic

### What we do not have

- a real web transport setup experience
- Cloudflare Tunnel management in web
- Tailscale visibility in web
- managed relay onboarding in web
- a unified product model for transport choice

### What we should build

- one web `Connectivity` surface
- provider-based setup options
- per-device status plus account defaults
- guided recommendations
- thin agent APIs for status and config

### Can we?

Yes.

The foundation is already there. This is mainly a product-integration and UI-orchestration gap, not a fundamental architecture gap.

The only part web cannot truly own by itself is local machine setup. For that, web must act through the connected desktop agent.
