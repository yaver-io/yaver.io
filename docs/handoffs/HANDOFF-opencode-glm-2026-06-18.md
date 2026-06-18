# Handoff for opencode / glm

Date: 2026-06-18
Branch: `main`

## Current State

The repo already has the main surface-plumbing slice for car, watch, TV, and MCP:

- `TaskViewport` now carries `surface`, `interaction`, `visualBudget`, and `riskPolicy`.
- The voice WS and `/tasks` prompt path can carry those fields.
- The prompt wrapper now shapes output for car, watch, TV, and MCP callers.
- A neutral `dpad_input` ops verb exists and routes to existing Apple TV, Android TV, or home key paths.
- Gateway ops wrappers exist so car/watch/TV/mobile can call `gateway_query`, `gateway_intent`, `gateway_act`, and `gateway_act_confirm` through ops instead of only MCP.
- Mobile has a reusable runtime-surface client and pure helper types for viewport headers and surface presets.

## Commits Already Pushed

- `758bf47d4` `docs: plan car watch tv mcp surfaces`
- `60bbecbd7` `feat: add runtime surface ops contracts`
- `52e5d2d14` `feat: add mobile runtime surface client`

## What Was Added

### Go / agent

- `desktop/agent/tasks.go`
- `desktop/agent/viewport_prompt.go`
- `desktop/agent/viewport_prompt_test.go`
- `desktop/agent/ops_dpad.go`
- `desktop/agent/ops_dpad_test.go`
- `desktop/agent/ops_gateway.go`
- `desktop/agent/ops_gateway_test.go`
- `desktop/agent/voice_http.go`

### Mobile

- `mobile/src/lib/runtimeSurfaceTypes.ts`
- `mobile/src/lib/runtimeSurfaceClient.ts`
- `mobile/src/lib/runtimeSurfaceClient.test.mts`
- `mobile/src/lib/agentVoice.ts`

## Verified

Go:

```bash
cd desktop/agent
go test . -run 'TestFormatViewportHint|TestMergeClientVoiceHints_SurfaceMetadataHeaders|TestNormalizeDpad|TestDpad|TestGatewayOps'
```

Mobile:

```bash
cd mobile
npx tsx src/lib/runtimeSurfaceClient.test.mts
npx tsx src/lib/carVoiceCoding.test.mts
npx tsx src/lib/watchBridge.test.mts
npx tsx src/lib/carVoiceConfirm.test.mts
npx tsc --noEmit --pretty false
```

All passed at the time of handoff.

## Important Files To Continue In

- [desktop/agent/tasks.go](../../desktop/agent/tasks.go)
- [desktop/agent/viewport_prompt.go](../../desktop/agent/viewport_prompt.go)
- [desktop/agent/ops_dpad.go](../../desktop/agent/ops_dpad.go)
- [desktop/agent/ops_gateway.go](../../desktop/agent/ops_gateway.go)
- [desktop/agent/voice_http.go](../../desktop/agent/voice_http.go)
- [mobile/src/lib/runtimeSurfaceTypes.ts](../../mobile/src/lib/runtimeSurfaceTypes.ts)
- [mobile/src/lib/runtimeSurfaceClient.ts](../../mobile/src/lib/runtimeSurfaceClient.ts)
- [mobile/src/lib/agentVoice.ts](../../mobile/src/lib/agentVoice.ts)

## Next Development Targets

1. Wire `runtimeSurfaceClient` into the actual car / watch / TV screens so they stop carrying raw ops payloads by hand.
2. Add UI entry points for gateway read / intent / act flows on the phone surfaces that will drive car/watch handoff.
3. Add MCP wrappers where the existing grand-tool path is not yet enough for first-class discoverability.
4. Extend the D-pad client usage to Apple TV / Android TV / home control screens so all surfaces share the same normalized key vocabulary.
5. Add tests around any new UI wiring before expanding the native surface set.

## Caveats

- The worktree is dirty with unrelated existing user changes. Do not revert them.
- There are already several unrelated untracked files in the repo root and under `desktop/agent/`, `docs/`, `mobile/`, and `web/`.
- GitHub accepted the pushed commits with a verified-signature rule bypass notice; that is a repo policy issue, not a code failure.

## Working Rule For The Next Agent

Treat the new runtime-surface contract as the source of truth for car/watch/TV/MCP shaping. Keep the same semantics between:

- the Go prompt wrapper
- the mobile voice helpers
- the ops surface
- MCP exposure

Do not create a second, parallel protocol.
