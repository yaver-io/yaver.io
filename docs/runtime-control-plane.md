# Runtime Control Plane

This is the current code-backed runtime map for the parts of Yaver that are easiest to misunderstand in docs:

- auth and re-auth
- device discovery and reachability
- reload paths across web and mobile
- WebView versus native Hermes loading
- vibing eligibility and execution
- feedback SDK control flows
- phone sandbox export, push, and runtime deploy
- heartbeat, relay presence, and the P2P bus

Read this together with:

- [`AI_ARCH.md`](../AI_ARCH.md)
- [`README.md`](../README.md)

Code wins over docs. File paths below point at the current implementation.

## 1. Runtime surfaces

The control plane spans four cooperating surfaces:

1. `desktop/agent/`
   The real machine-side control plane. Owns auth state, HTTP routes, dev servers, Hermes builds, vibing, phone-project export/import, relay integration, and bus state.
2. `mobile/`
   The React Native app. Discovers devices, selects transports, recovers auth, drives Hermes reload and web preview flows, and surfaces vibing / feedback / phone sandbox UI.
3. `backend/convex/`
   Identity, device registry, heartbeat state, guest access, relay metadata, and account settings.
4. `sdk/feedback/*`
   Embedded in third-party apps. Uses the same account/device graph to report issues, receive reload commands, and optionally kick off vibing.

Key files:

- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go)
- [`desktop/agent/auth_bootstrap.go`](../desktop/agent/auth_bootstrap.go)
- [`desktop/agent/auth_recover.go`](../desktop/agent/auth_recover.go)
- [`desktop/agent/code_control.go`](../desktop/agent/code_control.go)
- [`desktop/agent/mcp_tools.go`](../desktop/agent/mcp_tools.go)
- [`desktop/agent/vibing.go`](../desktop/agent/vibing.go)
- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx)
- [`mobile/src/lib/quic.ts`](../mobile/src/lib/quic.ts)
- [`backend/convex/devices.ts`](../backend/convex/devices.ts)

## 1.1 `yaver code` and BYOK runner selection

`yaver code` is not just a thin wrapper around `/tasks`. It persists a machine-aware control-plane state in the CLI config and can target either the local machine or an attached remote machine.

Important current behavior:

1. `yaver code` without `--attach` means local terminal + local repo/files.
2. `yaver code --attach <device>` means local terminal + remote repo/files on that machine.
3. When the active runner is `opencode`, the preferred BYOK surface is:
   - `yaver code set byok <provider> ...`
   - `yaver code get byok`
   - `yaver code set/get provider`
   - `yaver code set/get base-url`
   - `yaver code set/get plan-model`
   - `yaver code set/get build-model`
4. That flow patches the local or remote OpenCode provider config instead of requiring manual edits to `opencode.json`.
5. The same coding state is now available through shared control-plane endpoints and MCP tools:
   - HTTP: `/code/status`, `/code/attach`, `/code/detach`, `/code/repos`, `/code/repo`, `/code/dev`, `/code/deploy`, `/code/config`
   - MCP: `code_status`, `code_attach`, `code_detach`, `code_repos`, `code_repo_set`, `code_dev`, `code_deploy`, `code_config_get`, `code_config_set`
6. `code_status` is the best current snapshot surface for wrapped agents: it includes the current code config, target context, online hosts, recent graph runs, and the default auto-orchestration policy.
7. `code_deploy` / `/code/deploy` now support:
   - direct host deploys to TestFlight, Play Store internal testing, Convex, and Cloudflare
   - repo override by project selector or explicit path
   - machine override, `machine=auto`, and multi-target `distribute=true`
   - optional GitHub/GitLab CI fallback for teams that still want workflow-triggered deploys

This is intentionally biased toward cost reduction: use owned machines for build/test/deploy whenever possible, and fall back to CI only when it provides real value.

This is the current intended path for OpenRouter and other OpenAI-compatible BYOK backends in terminal coding mode.

Code:

- [`desktop/agent/code_cmd.go`](../desktop/agent/code_cmd.go)
- [`desktop/agent/code_control.go`](../desktop/agent/code_control.go)
- [`desktop/agent/code_control_plane.go`](../desktop/agent/code_control_plane.go)
- [`desktop/agent/code_control_http.go`](../desktop/agent/code_control_http.go)
- [`desktop/agent/opencode_config.go`](../desktop/agent/opencode_config.go)

## 1.2 Graph resource-aware execution

The current graph runtime is no longer just "prompt in, generic nodes out". Graph nodes can now carry explicit self-hosted resource intent and continuity hints that feed directly into placement and execution.

Important current behavior:

1. `agent_graph_start` and `code_mesh_start` accept optional explicit `nodes` from MCP callers.
2. Each node can declare `resource_modes` such as:
   - `build`
   - `deploy`
   - `browser`
   - `sim-ios`
   - `sim-android`
   - `phone`
   - `proof-video`
   - `video-summary`
   - `test-video`
   - custom labels for future self-hosted resource selectors
3. Nodes can persist continuity and locality hints:
   - `prior_device`
   - `prior_runner`
   - `sticky_device`
   - `sticky_runner`
4. Nodes can bias placement/routing with:
   - `toughness`
   - `design_points`
   - `build_points`
   - `verify_points`
5. Proof/demo oriented nodes can set `preferred_video_mode` to choose the right capture target (`browser`, `sim-ios`, `sim-android`, `phone`).
6. Graph chat nodes that request `proof-video`, `video-summary`, or `test-video` now automatically enable task-side clip capture so the producing machine can serve a remote watch URL back to the client.

Placement currently uses those fields to prefer:

- machines that already ran the node in prior rounds
- runners that match the node's design/build/verify profile
- iOS/Android-capable hosts for simulator or native build work
- deploy-capable machines when the node requests deploy resources
- proof/video-capable hosts when the node requests demo artifacts

This is the current code-backed bridge between:

- graph planning
- multi-machine execution
- self-hosted build/deploy resources
- proof video generation
- later-round continuity

Code:

- [`desktop/agent/agent_mode.go`](../desktop/agent/agent_mode.go)
- [`desktop/agent/agent_mesh.go`](../desktop/agent/agent_mesh.go)
- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go)
- [`desktop/agent/mcp_tools.go`](../desktop/agent/mcp_tools.go)

## 2. Auth and re-auth

There are three important machine auth states:

1. Normal authenticated serve
   The agent has a valid token and mounts the full authenticated surface.
2. Bootstrap serve
   The agent has no token and mounts only the pairing/recovery surface.
3. Auth-expired serve
   The agent is still reachable, but normal authed routes mostly 401 until recovery succeeds.

Current route wiring:

- Normal public recovery/pair routes:
  - `/health`
  - `/auth/status`
  - `/auth/pair/info`
  - `/auth/pair/session`
  - `/auth/pair/submit`
  - `/auth/pair/encrypted`
  - `/auth/recover`
- Bootstrap routes:
  - `/health`
  - `/info`
  - `/auth/pair/info`
  - `/auth/pair/session`
  - `/auth/pair/submit`
  - `/auth/pair/encrypted`
  - `/auth/recover`

Code:

- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go:342)
- [`desktop/agent/auth_bootstrap.go`](../desktop/agent/auth_bootstrap.go:237)

### Pairing modes

Two pairing modes exist:

1. Passkey pairing
   A 6-character short-lived code gates `/auth/pair/submit`.
2. Encrypted pairing
   If the target advertises a device public key, mobile can encrypt the token and submit to `/auth/pair/encrypted`.

Bootstrap beacon payload may include:

- `na` = needs auth
- `pk` = bootstrap passkey
- `dpk` = device public key

Code:

- [`desktop/agent/beacon.go`](../desktop/agent/beacon.go:19)
- [`desktop/agent/auth_pair.go`](../desktop/agent/auth_pair.go:246)
- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx:1241)

### Recovery modes

`POST /auth/recover` currently supports three modes:

1. `direct`
   Verified host bearer token is applied immediately as the agent token.
2. `pair`
   Opens a recovery pairing session, then mobile/web submits a token through the pair route.
3. `device-code`
   Starts fresh device-code auth. Allowed only after verified host authentication.

Proof accepted by `/auth/recover`:

1. Host-token mode
   Caller sends its own bearer token; the agent verifies ownership through `POST /devices/owner-by-hardware`.
2. Bootstrap-secret mode
   Caller sends the locally stored bootstrap secret.

Important current behavior:

- Host-token recovery can be used even before `authExpired` flips.
- Bootstrap-secret recovery still requires the degraded state.
- Guests cannot recover the host machine.

Code:

- [`desktop/agent/auth_recover.go`](../desktop/agent/auth_recover.go:246)
- [`desktop/agent/auth_recover.go`](../desktop/agent/auth_recover.go:336)
- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx:1461)

### Mobile recovery order

Current mobile behavior for a known device is:

1. Prime transport targets for the device.
2. Try host-token recovery in `pair` mode.
3. If that fails, try bootstrap-secret recovery in `pair` mode.
4. If that still fails, try host-token recovery in `device-code` mode.
5. If a pair code is returned, submit the phone's bearer token back via encrypted pair or passkey pair.

Code:

- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx:1483)

## 3. Discovery and reachability

Yaver does not have one discovery channel. It combines several:

1. LAN UDP beacon
2. Convex device registry
3. Heartbeat-advertised `localIps`
4. `quicHost`
5. Tunnels / public endpoints
6. Relay `/d/<deviceId>` paths
7. Relay live presence

### Convex device registry

The registry stores:

- `deviceId`
- `hardwareId`
- `publicKey`
- `quicHost`
- `localIps`
- `publicEndpoints`
- `needsAuth`
- `lastHeartbeat`
- `lastTunnelEvent`
- `deviceClass`
- `edgeProfile`
- `recoveryPosture`

Queries derive online state from both explicit online flags and heartbeat freshness.

Code:

- [`backend/convex/devices.ts`](../backend/convex/devices.ts:22)
- [`backend/convex/devices.ts`](../backend/convex/devices.ts:443)
- [`backend/convex/devices.ts`](../backend/convex/devices.ts:949)

### Mobile selection logic

Mobile deduplicates device rows, merges relay/tunnel hints, and overlays relay live presence on top of heartbeat freshness. In practice:

- heartbeat answers "recently alive"
- relay presence answers "relay tunnel alive right now"
- beacon answers "same-LAN shortcut right now"

Code:

- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx:344)
- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx:651)
- [`mobile/src/context/DeviceContext.tsx`](../mobile/src/context/DeviceContext.tsx:740)

### Transport racing

The mobile app and feedback SDKs do not trust a single IP. They race candidates:

- beacon IP
- heartbeat-advertised `localIps`
- `quicHost`
- Tailscale-ish addresses
- tunnel/public endpoints
- relay paths

Code:

- [`mobile/src/lib/quic.ts`](../mobile/src/lib/quic.ts:3556)
- [`sdk/feedback/react-native/src/Discovery.ts`](../sdk/feedback/react-native/src/Discovery.ts:146)
- [`sdk/feedback/web/src/discovery.ts`](../sdk/feedback/web/src/discovery.ts:109)

## 4. Heartbeat, presence, and bus

Three related layers exist:

1. Convex heartbeat
   Durable registry freshness.
2. Relay presence / `lastTunnelEvent`
   Near-real-time tunnel status.
3. P2P bus
   Live state fanout and retained events across agents and foreground clients.

The bus is not the durable registry. Convex remains the source of truth for ownership and long-lived device listing.

Current bus surfaces:

- `/bus/status`
- `/bus/retained`
- `/bus/events`
- `/bus/publish`

Foreground mobile/web subscribe through `/bus/events` SSE. Backgrounded mobile falls back to registry polling.

Code:

- [`desktop/agent/bus.go`](../desktop/agent/bus.go:1)
- [`desktop/agent/bus_http.go`](../desktop/agent/bus_http.go:1)
- [`desktop/agent/heartbeat_watcher.go`](../desktop/agent/heartbeat_watcher.go:215)

## 4.1 Task artifact contract and remote demo video

Task APIs are now expected to return structured task objects rather than just ids plus raw output text.

Important current behavior:

1. MCP `create_task`, `list_tasks`, and `get_task` return structured task payloads.
2. Task video capture is optional and controlled at creation time with:
   - `video_enabled`
   - `video_source`
3. When a demo clip exists, the task may expose:
   - `videoClipId`
   - `videoStatus`
   - `videoClipUrl`
   - `videoPosterUrl`
4. The clip URL is served by the producing machine, including for remotely executed tasks, so clients can render a watch link or embed an inline player without a separate artifact fetch protocol.

Code:

- [`desktop/agent/tasks.go`](../desktop/agent/tasks.go)
- [`desktop/agent/tasks_video_summary.go`](../desktop/agent/tasks_video_summary.go)
- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go)
- [`desktop/agent/mcp_tools.go`](../desktop/agent/mcp_tools.go)

## 5. Reload model: web versus mobile

Yaver has two different preview/reload families and they should not be described as one thing.

### Web preview path

Used for:

- Vite
- Next.js
- other web stacks
- browser-oriented preview surfaces

Mechanics:

1. Agent starts a dev server.
2. Browser/web dashboard loads `/dev/` through an iframe or WebView-like web preview surface.
3. `/dev/events` SSE tells the preview when to refresh.
4. Relay preview URLs must carry `__rp=<relay-password>` or the relay returns `401 invalid relay password`.

Code:

- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go:607)
- [`web/lib/agent-client.ts`](../web/lib/agent-client.ts:2893)
- [`web/components/dashboard/PreviewPane.tsx`](../web/components/dashboard/PreviewPane.tsx:221)

### Mobile native Hermes path

Used for:

- Expo
- React Native
- remote iPhone/Android preview through the Yaver app

Mechanics:

1. Agent or CLI builds a JS bundle.
2. Embedded `hermesc` compiles to Hermes bytecode.
3. The bytecode is validated and served/pushed.
4. Yaver loads it into a native bridge, not a WebView.

This is the only first-class path that works over LAN, relay, and cellular.

Code:

- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go:604)
- [`mobile/app/(tabs)/apps.tsx`](../mobile/app/(tabs)/apps.tsx:67)
- [`README.md`](../README.md:1516)

### Second-class mobile paths

Flutter, Swift, and Kotlin are supported differently:

- Flutter can do LAN reload/build flows against the real app.
- Swift/Kotlin use build/install style flows.
- They do not load inside Yaver's native super-host like Hermes-powered RN apps do.

Code:

- [`mobile/app/(tabs)/apps.tsx`](../mobile/app/(tabs)/apps.tsx:67)

## 6. WebView versus native loading

Rule:

- Web content may use iframe/WebView preview surfaces.
- React Native app loading must not use WebView.

That split exists today in both docs and code:

- `/dev/` proxy is intentionally unauthenticated for preview content.
- Hermes/native bundle routes are separate.
- Mobile UI explicitly treats Hermes as the first-class mobile path.

Code:

- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go:604)
- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go:607)
- [`mobile/app/(tabs)/apps.tsx`](../mobile/app/(tabs)/apps.tsx:1222)

## 7. Vibing

Vibing is not merely "send prompt to runner". Eligibility has real gates.

Current eligibility checks:

1. Resolve the project path.
2. Allow host-granted guest vibing for permitted repos.
3. Require GitHub or GitLab visibility for owner vibing.
4. Require connected provider auth.
5. Require runner binary installed.
6. Require runner auth readiness.

Code:

- [`desktop/agent/vibing.go`](../desktop/agent/vibing.go:999)

Current SDK/web/mobile usage:

- `/vibing`
- `/vibing/eligibility`
- `/vibing/execute`

SDKs call eligibility first, then execute.

Code:

- [`desktop/agent/httpserver.go`](../desktop/agent/httpserver.go:642)
- [`sdk/feedback/web/src/P2PClient.ts`](../sdk/feedback/web/src/P2PClient.ts:379)
- [`sdk/feedback/react-native/src/P2PClient.ts`](../sdk/feedback/react-native/src/P2PClient.ts:537)

## 8. Feedback SDK control flow

The web and React Native feedback SDKs now share the same high-level pattern:

1. Sign into Yaver.
2. Pick a reachable machine.
3. Discover the best transport.
4. Open a command stream from the agent.
5. Use that connection for feedback upload, reload, and vibing.

### Web SDK

Current web SDK features:

- popup OAuth
- email auth
- cached token and selected device
- device picker
- transport-aware discovery
- command stream handling for `reload`, `reload_bundle`, and `status`

Code:

- [`sdk/feedback/web/src/auth.ts`](../sdk/feedback/web/src/auth.ts:144)
- [`sdk/feedback/web/src/YaverFeedback.ts`](../sdk/feedback/web/src/YaverFeedback.ts:75)

### React Native SDK

Current RN SDK features:

- native Apple sign-in on iOS
- in-app browser OAuth
- cached device/user session
- guest-token minting for scoped shared access
- command stream driven reload/status
- suppression when the SDK is already running inside Yaver's own host app

Code:

- [`sdk/feedback/react-native/src/auth.ts`](../sdk/feedback/react-native/src/auth.ts:232)
- [`sdk/feedback/react-native/src/YaverFeedback.ts`](../sdk/feedback/react-native/src/YaverFeedback.ts:26)

### Command stream semantics

Important commands currently used by SDKs:

- `reload`
- `reload_bundle`
- `status`

Those are delivered over the blackbox command stream and mapped to host-specific reload behavior.

Code:

- [`sdk/feedback/react-native/src/BlackBox.ts`](../sdk/feedback/react-native/src/BlackBox.ts:375)
- [`sdk/feedback/web/src/P2PClient.ts`](../sdk/feedback/web/src/P2PClient.ts:441)

## 9. Phone sandbox, export, push, and runtime deploy

Phone projects are local SQLite-backed mini backends under the agent. Promotion is explicit.

Current HTTP surface:

- `/phone/projects/export`
- `/phone/projects/receive`
- `/phone/projects/promote`

Code:

- [`desktop/agent/phone_backend_http.go`](../desktop/agent/phone_backend_http.go:13)

### Export bundle contract

Current export can include:

- `.yaver/config.yaml`
- `.yaver/project.yaml`
- `schema.yaml`
- `auth.yaml`
- `seed.json`
- `app.yaml`
- `local.db` when `includeData=true`
- `oauth-providers.yaml` when present
- `schema.sql`
- `schema.postgres.sql`
- `.gitignore`
- `README.md`

When `containerize=true`, it also includes:

- `Dockerfile`
- `docker-compose.yml`
- `.env.example`
- `.dockerignore`

Code:

- [`desktop/agent/phone_backend.go`](../desktop/agent/phone_backend.go:994)

### Import/push behavior

Imports honor:

- `reject`
- `rename`
- `overwrite`

and may skip seed application with `skip_seed=true`.

Code:

- [`desktop/agent/phone_backend.go`](../desktop/agent/phone_backend.go:1404)

### Runtime deploy surface

Phone sandbox continuation is bigger than raw export. The MCP/runtime deploy path can:

- connect provider accounts
- promote to Convex Cloud
- promote to Cloudflare Workers
- push to Yaver Cloud
- push to custom/self-hosted Yaver targets

Code:

- [`desktop/agent/mcp_phone.go`](../desktop/agent/mcp_phone.go:145)
- [`desktop/agent/vibing.go`](../desktop/agent/vibing.go:792)

## 10. What to keep updated

When one of these changes, update this doc and the surface README docs together:

- auth route set
- recovery mode behavior
- discovery candidate order
- relay-password preview behavior
- vibing eligibility gates
- feedback SDK auth model
- phone export bundle contents
- runtime deploy targets
