# Yaver Critical Failure Program

This document is the execution board for making critical Yaver flows fail visibly, consistently, and actionably across the Go agent, web UI, mobile app, SDKs, and MCP.

## Program Goal

For every critical flow, Yaver must either:

- succeed, or
- fail with a precise user-visible reason,
- expose the relevant logs or progress stream,
- hide or disable impossible actions before the user triggers them,
- and return the same truth through web, mobile, SDKs, and MCP.

This must be implemented vertically by flow, not horizontally by layer.

## Critical Flows

- connectivity: direct, relay, Cloudflare tunnel, Tailscale
- auth / re-auth
- runner auth: Codex, Claude Code, OpenCode
- dev reload / Hermes / preview worker / webview reload
- capability-gated actions: TestFlight, Play Store, preview, runner execution
- feedback upload / feedback tracing / BlackBox
- vibing / remote execution / guest and project restrictions
- MCP-triggered actions that touch any of the above

## Program Rules

- No critical route may exist without incident emission.
- No long-running critical operation may exist without normalized progress state.
- No UI may render a critical action from local heuristics if the agent can answer readiness.
- No SDK-facing route may remain exposed unless scope policy, failure UX, and tests are wired.
- No critical failure may remain console-only.
- No slice is complete until agent, web, mobile, SDK, MCP, and tests are all covered.

## Shared Contracts

The following contracts are the only allowed source of truth for failure and readiness.

### IncidentEvent

Used for durable, user-visible failure and blocker state.

Suggested shape:

```go
type IncidentEvent struct {
    ID              string                 `json:"id"`
    Timestamp       time.Time              `json:"timestamp"`
    Severity        string                 `json:"severity"`
    Category        string                 `json:"category"`
    Code            string                 `json:"code"`
    Source          string                 `json:"source"`
    Title           string                 `json:"title"`
    UserMessage     string                 `json:"userMessage"`
    TechnicalInfo   string                 `json:"technicalInfo,omitempty"`
    SuggestedAction string                 `json:"suggestedAction,omitempty"`
    OperationID     string                 `json:"operationId,omitempty"`
    DeviceID        string                 `json:"deviceId,omitempty"`
    ProjectPath     string                 `json:"projectPath,omitempty"`
    Target          string                 `json:"target,omitempty"`
    LogsAvailable   bool                   `json:"logsAvailable"`
    LogRefs         []string               `json:"logRefs,omitempty"`
    CorrelationID   string                 `json:"correlationId,omitempty"`
    Recoverable     bool                   `json:"recoverable"`
    Metadata        map[string]interface{} `json:"metadata,omitempty"`
    Resolved        bool                   `json:"resolved"`
}
```

### OperationState

Used for live operation progress.

```go
type OperationState struct {
    ID          string                 `json:"id"`
    Kind        string                 `json:"kind"`
    Status      string                 `json:"status"`
    Phase       string                 `json:"phase,omitempty"`
    Message     string                 `json:"message,omitempty"`
    Progress    float64                `json:"progress,omitempty"`
    DeviceID    string                 `json:"deviceId,omitempty"`
    ProjectPath string                 `json:"projectPath,omitempty"`
    StartedAt   time.Time              `json:"startedAt"`
    UpdatedAt   time.Time              `json:"updatedAt"`
    IncidentIDs []string               `json:"incidentIds,omitempty"`
    Metadata    map[string]interface{} `json:"metadata,omitempty"`
}
```

### CapabilitySnapshot

Used for every action gate in UI, SDKs, and MCP.

```json
{
  "generatedAt": "...",
  "machine": {},
  "targets": {
    "testflight": {
      "enabled": false,
      "reasonCode": "deploy.testflight.xcode_missing",
      "reason": "This machine does not have Xcode, so TestFlight deploy is not available.",
      "suggestedAction": "Switch to a macOS host with Xcode or use CI."
    }
  },
  "runners": {},
  "connectivity": {}
}
```

## Reason Code Catalog

The Go agent must own the stable reason code registry.

Initial families:

- `auth.*`
- `runner.*`
- `connectivity.*`
- `reload.*`
- `build.*`
- `deploy.*`
- `feedback.*`
- `sdk.*`
- `vibing.*`
- `mcp.*`
- `capability.*`

Examples:

- `connectivity.relay.auth_expired`
- `connectivity.tunnel.unreachable`
- `connectivity.no_viable_transport`
- `runner.codex.not_authenticated`
- `runner.codex.linux_sandbox_blocked`
- `runner.browser_auth.timeout`
- `reload.native_rebuild_required`
- `reload.preview_worker.offline`
- `build.hermes.failed`
- `deploy.testflight.xcode_missing`
- `deploy.play.android_sdk_missing`
- `auth.sdk.scope_denied`

## Backbone Work

This is the only horizontal prerequisite. It exists so all slices share one truth model.

### Agent Files

Add:

- `desktop/agent/incidents.go`
- `desktop/agent/incidents_store.go`
- `desktop/agent/incidents_http.go`
- `desktop/agent/operations.go`
- `desktop/agent/operations_store.go`
- `desktop/agent/operations_http.go`
- `desktop/agent/capabilities_snapshot.go`
- `desktop/agent/reason_codes.go`

Edit:

- `desktop/agent/httpserver.go`
- `desktop/agent/blackbox.go`
- `desktop/agent/errors_store.go`
- `desktop/agent/error_tracker.go`

### Agent Endpoints

Add:

- `GET /incidents`
- `GET /incidents/stream`
- `GET /incidents/summary`
- `GET /operations`
- `GET /operations/stream`
- `GET /capabilities/snapshot`

### Shared Client Type Files

Edit:

- `web/lib/agent-client.ts`
- `mobile/src/lib/quic.ts`
- `sdk/feedback/web/src/types.ts`
- `sdk/feedback/react-native/src/types.ts`
- `sdk/feedback/flutter/lib/src/types.dart`

### Backbone Test Work

Add tests for:

- incident persistence
- incident streaming
- operation streaming
- capability snapshot shape
- old error endpoints reading through incident adapters

## Slice 1: Connectivity

### Goal

The same connectivity truth must be visible in web, mobile, SDKs, and MCP.

### Agent

Add or edit:

- `desktop/agent/httpserver.go`
- `desktop/agent/tailscale.go`
- `relay/server.go`
- `relay/tunnel.go`
- `desktop/agent/incidents_connectivity.go` (new)
- `desktop/agent/capabilities_snapshot.go`

Implementation:

- emit incidents for failed direct probe
- emit incidents for relay auth expiry
- emit incidents for relay unreachable
- emit incidents for tunnel unreachable
- emit incidents for no viable transport
- include log refs and probe metadata
- include connectivity readiness in `CapabilitySnapshot`

### Web

Edit:

- `web/components/dashboard/ConnectivityView.tsx`
- `web/lib/agent-client.ts`

Implementation:

- render canonical incidents alongside probe diagnostics
- add blocker banner
- add “view details” and “view logs” affordances

### Mobile

Edit:

- `mobile/src/lib/quic.ts`
- `mobile/app/(tabs)/monitor.tsx`
- `mobile/app/(tabs)/infra.tsx`

Implementation:

- add incident query/subscription
- show top connectivity blocker from incident feed
- avoid mobile-only failure wording if agent already provides a message

### SDKs

Edit:

- `sdk/feedback/web/src/YaverFeedback.ts`
- `sdk/feedback/web/src/P2PClient.ts`
- `sdk/feedback/react-native/src/YaverFeedback.ts`
- `sdk/feedback/react-native/src/P2PClient.ts`
- `sdk/feedback/flutter/lib/src/connection_widget.dart`
- `sdk/feedback/flutter/lib/src/p2p_client.dart`

Implementation:

- surface transport-specific failure reason
- distinguish `auth expired`, `relay password missing`, and `unreachable`

### MCP

Edit:

- `mcp/server.go`
- `mcp/builtin_tools.go`
- `desktop/agent/mcp_infra.go`

Expose:

- `connectivity.summary`
- `incidents.list(category=connectivity)`

### Tests

Add:

- relay auth expired
- relay password missing
- tunnel unreachable
- no viable transport

## Slice 2: Runner Auth

### Goal

Runner readiness and browser auth must be streamable and consistent everywhere.

### Agent

Edit:

- `desktop/agent/runner_auth.go`
- `desktop/agent/runner_auth_http.go`
- `desktop/agent/runner_auth_browser_http.go`
- `desktop/agent/runner_auth_setup.go`
- `desktop/agent/httpserver.go`
- `desktop/agent/capabilities_snapshot.go`

Implementation:

- emit incidents for blocked runner readiness
- emit operations for browser auth session lifecycle
- attach runner IDs and host metadata

### Scope Cleanup In This Slice

Edit:

- `desktop/agent/httpserver.go`
- `backend/convex/auth.ts`

Implementation:

- verify `runner-auth` scope end-to-end
- test allow/deny behavior for SDK tokens

### Web

Edit:

- `web/lib/agent-client.ts`
- runner auth dashboard screens that call `/runner-auth/*`

Implementation:

- show exact readiness incidents
- show browser auth operation state

### Mobile

Edit:

- `mobile/src/lib/quic.ts`
- `mobile/app/(tabs)/settings.tsx`

### SDKs

Edit:

- `sdk/feedback/web/src/P2PClient.ts`
- `sdk/feedback/react-native/src/P2PClient.ts`
- any Flutter runner-auth surface if added

### MCP

Edit:

- `desktop/agent/mcp_auth_tools.go`
- `desktop/agent/mcp_auth_link_tools.go`
- `mcp/builtin_tools.go`

### Tests

Add:

- Codex unauthenticated
- Codex Linux sandbox blocked
- Claude readiness blocked
- browser auth timeout
- SDK token missing `runner-auth`

## Slice 3: Reload / Hermes / Preview

### Goal

Reload and bundle rebuild flows must never fail silently.

### Agent

Edit:

- `desktop/agent/devserver_http.go`
- `desktop/agent/logstream.go`
- `desktop/agent/blackbox.go`
- `desktop/agent/capabilities_snapshot.go`

Implementation:

- create operation states for reload and build-native flows
- emit incidents for native rebuild required
- emit incidents for preview worker unavailable
- emit incidents for build failure and no active dev server
- attach `/dev/events` and command-stream log refs

### Web

Edit:

- `web/lib/agent-client.ts`
- preview and hot reload dashboard views

### Mobile

Edit:

- `mobile/src/lib/quic.ts`
- `mobile/app/(tabs)/hotreload.tsx`
- `mobile/app/(tabs)/apps.tsx`

### SDKs

Edit:

- `sdk/feedback/react-native/src/BlackBox.ts`
- `sdk/feedback/react-native/src/P2PClient.ts`
- `sdk/feedback/web/src/P2PClient.ts`
- `sdk/feedback/flutter/lib/src/p2p_client.dart`

### MCP

Edit:

- `desktop/agent/mcp_appdev.go`
- `desktop/agent/mcp_devtools.go`
- `desktop/agent/mcp_devtools2.go`

### Tests

Add:

- native rebuild required
- preview worker offline
- no active dev server
- build-native failed

## Slice 4: Capability Gating

### Goal

Never show impossible actions as normal actions.

### Agent

Edit:

- `desktop/agent/capabilities_snapshot.go`
- `desktop/agent/doctor_build.go`
- `desktop/agent/console_machines.go`
- `desktop/agent/runner_auth.go`

Implementation:

- merge machine capability, doctor/build output, runner readiness, and connectivity prerequisites into `CapabilitySnapshot.targets`

### Web

Edit:

- `web/components/dashboard/BuildsView.tsx`
- `web/components/dashboard/VibeCodingView.tsx`
- any deploy/task launcher views

Implementation:

- remove unconditional TestFlight and Play actions
- render disabled states with reason where needed

### Mobile

Edit:

- `mobile/app/(tabs)/apps.tsx`
- `mobile/app/(tabs)/settings.tsx`
- `mobile/app/(tabs)/hotreload.tsx`
- `mobile/app/(tabs)/builds.tsx`

### SDKs

Edit any host action presenters to use capability snapshot before showing an option.

### MCP

Edit:

- `mcp/builtin_tools.go`
- relevant `desktop/agent/mcp_*.go` files by tool family

### Tests

Add:

- no Xcode -> TestFlight unavailable
- no Android SDK -> Play unavailable
- no relay password -> preview unavailable
- runner blocked -> action unavailable

## Slice 5: Feedback SDK

### Goal

Feedback failures and trace availability must be first-class incidents.

### Agent

Edit:

- `desktop/agent/feedback_http.go`
- `desktop/agent/feedback.go`
- `desktop/agent/blackbox.go`
- `desktop/agent/feedback_cmd.go`
- `desktop/agent/feedback_board.go`

Implementation:

- unify feedback failures into incident model
- decide real streaming session semantics versus official batch-first contract
- emit incidents for upload failure, stream failure, invalid payload, command stream disconnect

### Web and Mobile First-Party UI

Edit:

- `mobile/src/lib/feedback.ts`
- `mobile/app/(tabs)/monitor.tsx`
- feedback views in web if present

### SDKs

Edit:

- `sdk/feedback/react-native/src/BlackBox.ts`
- `sdk/feedback/react-native/src/FeedbackModal.tsx`
- `sdk/feedback/react-native/src/P2PClient.ts`
- `sdk/feedback/web/src/P2PClient.ts`
- `sdk/feedback/web/src/YaverFeedback.ts`
- `sdk/feedback/flutter/lib/src/blackbox.dart`
- `sdk/feedback/flutter/lib/src/p2p_client.dart`
- `sdk/feedback/flutter/lib/src/feedback_overlay.dart`

### MCP

Edit:

- `desktop/agent/feedback_mcp.go`
- `mcp/builtin_tools.go`

### Tests

Add:

- upload failure
- malformed metadata
- partial asset failure
- stream unsupported
- command stream disconnected

## Slice 6: Vibing / Remote Execution / MCP

### Goal

Remote execution denials and failures must use the same incident and capability model.

### Agent

Edit:

- `desktop/agent/httpserver.go`
- vibing implementation files
- preview-session or mobile-worker files that gate target selection
- `desktop/agent/capabilities_snapshot.go`

Implementation:

- emit incidents for project denial, guest denial, missing target device, preview session absence, remote connectivity loss

### Web and Mobile

Edit the vibing and remote action surfaces to render those incidents and respect capability gating.

### SDKs

Ensure any vibing or remote-exec entrypoint uses the same capability and incident contracts.

### MCP

Edit:

- `desktop/agent/mcp_tools.go`
- `desktop/agent/mcp_workspace.go`
- `desktop/agent/mcp_workspace_handlers.go`
- tool-family-specific MCP files that trigger remote actions

### Tests

Add:

- guest denied by scope
- guest denied by project
- missing target device
- connectivity loss during remote action

## Global Auth and Scope Audit

This must run early and continue as slices land.

Primary files:

- `desktop/agent/httpserver.go`
- `backend/convex/auth.ts`
- `web/lib/agent-client.ts`
- `mobile/src/lib/quic.ts`
- `sdk/feedback/web/src/P2PClient.ts`
- `sdk/feedback/react-native/src/P2PClient.ts`

For every SDK or guest-facing route, record:

- path
- intended caller
- required scope
- whether clients currently call it
- whether scope prefixes allow it
- whether default scopes should include it

Current known gaps to resolve:

- `/analytics/ingest`
- `/flags/eval`
- `/releases/latest`
- `/errors/ingest` auth policy
- any mobile worker preview session SDK routes

## UI Components To Add

Shared web/mobile components should be introduced to avoid fragmenting failure presentation again.

- `CriticalBlockerBanner`
- `IncidentList`
- `IncidentDetailSheet`
- `OperationProgressCard`
- `CapabilityBadge`

## Logging Policy

Every critical incident should reference logs or streams where possible.

Examples:

- `reload.preview_worker.offline` -> `stream:dev-events`, `blackbox:device:<id>`
- `deploy.testflight.xcode_missing` -> `doctor:build:testflight`
- `connectivity.relay.auth_expired` -> `connectivity:probes`

UI should offer:

- view details
- view logs
- copy incident

## Completion Gate Per Slice

A slice is complete only when all are true:

- incident emitted
- incident persisted
- incident stream updated
- operation state wired where applicable
- capability snapshot updated where applicable
- web consumes it
- mobile consumes it
- SDKs consume it where relevant
- MCP exposes it where relevant
- logs are linked
- tests are green

## Immediate Execution Queue

1. Backbone contracts in the Go agent
2. Shared client types for incidents, operations, and capabilities
3. Global route/scope audit
4. Connectivity slice end-to-end
5. Runner auth slice end-to-end
6. Reload/Hermes/preview slice end-to-end
7. Capability gating slice end-to-end
8. Feedback SDK slice end-to-end
9. Vibing/remote execution/MCP slice end-to-end
10. Remove legacy duplicate error storage once all readers are migrated

## Notes

- `desktop/agent/errors_store.go` and `desktop/agent/error_tracker.go` should converge on the canonical incident store.
- `web/components/dashboard/BuildsView.tsx` must stop presenting unconditional TestFlight and Play actions.
- `desktop/agent/httpserver.go` scope and route mounting must be audited together with `backend/convex/auth.ts`.
- `sdk/feedback/flutter/lib/src/connection_widget.dart` needs parity uplift with web and RN failure detail.
