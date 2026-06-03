# Yaver SDK Architecture — the generic control-plane spine

> Code is source of truth; this doc drifts. Grep `sdk/js/src/` and
> `backend/convex/companyAIOptions.ts` before relying on any name here.

Yaver embeds into other apps as a **policy-driven, multi-runner, multi-provider
control plane** — the "OpenRouter of coding agents." Talos is the first
consumer, but nothing app-specific lives in the Yaver core: an app contributes
an `AppProfile` and Yaver resolves runtimes generically.

## Two axes: runner vs provider

People conflate these. The code separates them and so must every consumer:

| Axis | Values | Wrapped where | Auth unit |
|---|---|---|---|
| **Runner** (agent CLI) | claude-code, codex, opencode, aider | agent runner registry + `/agent/runner/switch` | OAuth subscription / device-code |
| **Provider** (model backend) | anthropic, openai, openrouter, gemini, ollama, salad, on-prem vLLM | OpenCode BYOK (`/set byok …`) or a custom runner | API key: company-secret / user-secret / none |

"Wrap OpenRouter / Ollama / Salad" is **not** a new runner — it's OpenCode (or a
custom runner) + a provider entry with a `baseUrl` and a `keyPolicy`. Salad and
any on-prem box are just "OpenAI-compatible provider at baseUrl X."

## Three layers (and how they connect)

```
@yaver/server (YaverApp)            @yaver/client (connect)          the agent (Go)
  holds the account token             policy-blind transport            authoritative
  ├─ listDevices / status             ├─ connectHandle(handle)          enforcer of the
  ├─ getPolicy / setPolicy            ├─ races direct→tunnel→relay      token's scope
  ├─ resolve(workKind) ──────────┐    ├─ getToken refresh hook          ├─ /tasks
  ├─ resolvedHandle ─────────────┼──► ├─ allowedRunners guard           ├─ /agent/runner/switch
  └─ composeEntitlements(...)     │    └─ SSE stream + polling fallback   └─ MCP dispatch
                                  │
        ResolvedSession (no secrets) ─ opaque handle + scoped token ─► client renders only what's allowed
```

- **`sdk/js/src/policy.ts`** — generic types (`CompanyAIOptions`, `AppProfile`,
  `WorkKindDef`, `RolePolicy`, `ProviderDef`, `ResolvedSession`) +
  `YaverPolicyClient` over `/company-ai/{options,resolve}`. Pure helpers
  (`selectRunner`, `selectProvider`, `isWorkKindEnabled`) mirror the server for
  optimistic UI; the server stays authoritative.
- **`sdk/js/src/app.ts`** — `YaverApp` adds `getPolicy / setPolicy / resolve /
  resolvedHandle`. `resolvedHandle` resolves policy, composes ACL layers, mints a
  scoped token carrying the **effective** allowed-runner scope, and returns a
  ready handle.
- **`sdk/js/src/connect.ts`** — `AgentSession` gains a `getToken` refresh hook
  (mobile token rotation), a client-side `allowedRunners` guard, and SSE
  streaming with polling fallback. `.transport` is exposed for cross-launch
  persistence.
- **`sdk/js/src/acl.ts`** — composable entitlements (below).
- **`backend/convex/companyAIOptions.ts`** — the resolver. `workKind` is now a
  generic `v.string()` validated against the team's `appProfile.workKinds` (or
  the legacy Talos map). Adds provider resolution (`normalizeProvider`) and
  per-role runner/provider caps.

## De-Talos-ification

The core no longer hardcodes app vocabulary. An app registers its own work
kinds, role caps, and provider catalog under `options.appProfile`:

```ts
appProfile: {
  app: 'talos',
  workKinds: [{ key: 'harness-cad', approvals: ['robot-motion'], artifactKinds: ['openscad','stl'] }],
  roles: [{ role: 'operator', allowedRunners: ['opencode'], allowedProviders: ['ollama'] }],
  providers: [{ id: 'openrouter', label: 'OpenRouter', baseUrl: '…', models: ['…'], keyPolicy: 'company-secret' }],
}
```

The legacy fixed `workKinds` booleans + Talos defaults still work for the
existing dashboard; new consumers use `appProfile` and never inherit
`robotTrial`/`talos_*`.

## Composable ACL — jointly inclusive, never forcing

Yaver already enforces several ACL layers on the agent, all with the convention
that an **empty/absent allowlist = unconstrained**:

| Layer | Source | Constrains |
|---|---|---|
| guest grant | `backend/convex/guests.ts`, `desktop/agent/guest_scope.go` | scope paths, `allowedRunners`, `allowedProjects`, deviceIds, usage caps |
| SDK-token scope | `desktop/agent/auth.go` `SdkTokenInfo`, `backend/convex/auth.ts` | path scopes, `allowedCIDRs`, `allowedProjects`, delegated guest scope |
| host-share policy | `desktop/agent/auth.go` `HostSharePolicy` | `AllowedRunners`, `AllowedProjects`, tooling preset, TTL |
| peer / PC-sharing ACL | `desktop/agent/acl.go` | registered peers (per-peer tool ACL is **not** modeled; secrets blocked globally) |
| layer-4 secrets | `desktop/agent/mcp_remote_proxy.go` `layer4Tools` | vault/sdk-token/env/deploy-cred tools never cross devices (denylist) |
| **company AI policy** | `companyAIOptions.ts` (new) | `allowedRunners`, providers, work kinds, per-role tools |
| user's own prefs | the user | their own runner/provider/project/device choices |

`acl.ts` composes them with `composeEntitlements(layers)`:

- **List dimensions** (runners, providers, projects, work kinds, tools, devices,
  CIDRs): the effective allowlist is the **intersection of only the present
  (non-empty) allowlists**. A layer that omits a dimension does not narrow it.
  `'*'` in a tool allowlist means "all" (no constraint).
- **Denylists** (layer-4 secrets): **unioned** and always subtracted.
- **Numeric caps** (daily tokens, cpu, ram, TTL): the **min** present value wins.

This is what "jointly inclusive, not exclusive" means: the company policy is one
more layer; it intersects with the user's/guest's own rules and never overrides
or forces them. Builders (`entitlementFromResolved / Guest / SdkToken /
HostShare / User`) read the existing layers so the SDK composes rather than
reinvents.

## Enforcement boundary (critical)

Composition + the client `allowedRunners` guard are **defense-in-depth and UI
honesty only**. A mobile app or hostile MCP caller can send any JSON. The
authoritative enforcer is the **agent**, using the scope baked into the token:

- `resolvedHandle` mints a token with `runners:<effective list>` +
  `workKind:<kind>` scopes.
- **DONE (Go):** the agent now enforces the runner allowlist at the
  chokepoints. `stampSdkRunnerScope` (`httpserver.go`) translates the token's
  `runners:<csv>` scope into the server-controlled `X-Yaver-SdkAllowedRunners`
  header (inbound value always stripped first); `runnerDeniedByScopeHeaders`
  intersects it with `X-Yaver-HostShareAllowedRunners` (jointly inclusive) and
  is called in both `handleRunnerSwitch` and the `/tasks` create path. This also
  closed two prior gaps: host-share `AllowedRunners` was stamped but unenforced
  on `/tasks`, and runner-switch enforced no allowlist at all. Guest allowlists
  are enforced via `GuestConfigManager.CheckRequestedRunner` (now also on
  runner-switch). Covered by `runner_scope_test.go`.
- **DONE (Go):** the resolver is exposed as the MCP verb `company_ai_resolve`
  (`mcp_tools.go` def + `httpserver.go` dispatch → `resolveCompanyAIRuntime`
  calls Convex `/company-ai/resolve`), so headless/MCP callers get the same
  `ResolvedSession`.
- **TODO (Go):** enforce `toolPolicyByRole` at MCP dispatch (the per-role tool
  allowlist is resolved but not yet gated at the tool-call layer).

## What a consumer (e.g. Talos) does

1. `app.setPolicy(teamId, { …, appProfile })` once (admin).
2. Per chat/job: `const handle = await app.resolvedHandle({ teamId, workKind, source }, { entitlements: [...user/guest layers] })`.
3. Send `handle` to the client over your own authed endpoint.
4. Client: `connectHandle(handle, { getToken })` → `session.createTask(prompt, { runner: handle.runner })` → `session.streamOutput(task.id)`.
5. If `resolved.nextActions.reauthRunner` / `configureProviderKey`, drive the
   existing Yaver runner-auth / vault flows — never collect raw keys in chat.

Talos becomes one `AppProfile` against this spine; the next consumer is another.
