# Yaver on-prem / air-gapped runtimes

How Yaver runs as a first-class wrapper for **local AI projects** (Talos, or any
dedicated AI use) where the tenant keeps code, data, and runtime on infra they
control — an on-prem box, or a cheap rented box (e.g. SaladCloud) — for
corporate privacy.

> Code is the source of truth. Every route/flag named here is grepped from the
> agent + backend; if a name drifts, the code wins.

## Two credential lanes (both first-class)

| Lane | What it is | When |
|---|---|---|
| **OAuth subscription (default)** | Yaver wraps Claude Code / Codex / OpenCode using each developer's own subscription OAuth (`--claudeai` / ChatGPT). No API keys. | Privacy = code + runtime stay on your box; the model is still Claude/Codex via the dev's plan. Yaver never sees the code. |
| **Local-model / external endpoint** | Point the runner at Ollama / vLLM / an internal gateway / a model hosted on Salad — base URL + optional key, both stored only in the runtime vault. | No-egress: code must never reach Anthropic/OpenAI at all. |

The company policy's `runners.credentialMode` selects the lane:
`user-auth-on-runtime` (default, OAuth) · `company-api-key-on-runtime` ·
`local-model-on-runtime` · `external-onprem-endpoint`.

## OAuth wrapping (the default)

The resolver (`/company-ai/resolve`, or its MCP verb `company_ai_resolve`)
returns `dispatch.runnerAuth` — the routes a client uses to sign a runner in on
the **selected runtime** via the user's subscription, never raw keys:

- `runner-auth/status` — is the runner signed in?
- `runner-auth/browser/{start,status,submit-code,cancel}` — `--claudeai` /
  ChatGPT browser/device flow on the runtime.
- `runner-auth/credentials/import` — mirror the owner's already-signed-in local
  credentials onto the runtime instead of re-OAuthing per box.

A no-inbound on-prem box is reachable without a public IP: LAN UDP beacon
(19837) for same-LAN discovery, self-hostable QUIC relay otherwise. Device
targeting goes through the agent peer proxy.

## Local-model / Salad lane (co-equal)

Configured per runtime in the **vault** (project `runner-provider`), so the key
never touches Convex or crosses machines:

```
BASE_URL          = http://10.0.0.9:8000/v1     # Salad / vLLM / Ollama / gateway
API_KEY           = <key>                        # optional; Ollama needs none
BASE_URL__codex   = http://localhost:11434/v1    # optional per-runner override
```

At runner spawn, `taskEnv` injects the protocol-correct env (Claude Code →
`ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN`; Codex/OpenCode →
`OPENAI_BASE_URL`/`OPENAI_API_BASE`/`OPENAI_API_KEY`). Empty `BASE_URL` → no-op,
so the runner falls back to its OAuth credentials.

Reachability preflight (Convex can't probe an on-prem endpoint):

```
GET /runner-provider/preflight?runner=codex
→ {configured, reachable, status, models[], keyPresent}   # never the key
```

## Air-gapped policy (no hosted Convex)

An egress-restricted box resolves + enforces policy **offline** from a local
file — the same `CompanyAIOptions` JSON the dashboard writes, so an admin
exports it from Convex and drops it on the box:

```
$YAVER_COMPANY_AI_POLICY  (env override) →
/etc/yaver/company-ai-policy.json →
<config-dir>/company-ai-policy.json
```

```
POST /company-ai/resolve-local   {workKind, requestedRunner?, ...}
→ same shape as /company-ai/resolve, source="local-airgap", no secrets
```

**Automatic fallback (preferred).** The agent's normal resolve path (the
`company_ai_resolve` MCP verb → `resolveCompanyAIWithFallback`) transparently
falls back to the local file when the Convex call fails **and** a local policy
is present — so air-gap is automatic, not a separate code path callers must
choose. A successful Convex resolution always wins; with no local file the
Convex error is surfaced unchanged. The fallback reply is tagged
`fallback:"convex-unreachable"`. The explicit `/company-ai/resolve-local` route
remains for direct/offline testing.

## dataPolicy enforcement (on the runtime)

| Control | Enforcement |
|---|---|
| `redactPII` | The SDK bakes a `policy:redactPII` token scope; the agent stamps `X-Yaver-RedactPII` (server-controlled, stripped on ingress) and scrubs emails / secrets / cards (Luhn) / IPs / bearer tokens from the fully-assembled prompt as the last step before the runner sees it. |
| `retentionDays` | A background loop prunes finished tasks past the window (running/pending never pruned), driven by the local policy's `dataPolicy.retentionDays`. |

Convex remains config-only: no keys, prompts, output, logs, or paths
(enforced by `convex_privacy_test.go`). Vault is NaCl + Argon2id, keychain-
derived, never synced to Convex.

## Status: done vs staged

**Done:** OAuth-first default + resolver `runnerAuth` hints · co-equal
local-model env injection + preflight · offline policy resolution + redaction +
retention · LAN-only discovery (beacon) · self-hostable relay.

**Staged (genuinely remaining for a zero-Convex plane):** fully offline **auth**.
The agent still validates sessions against Convex unless a pre-provisioned local
token is used; offline session minting/validation is the next piece. LAN
discovery and policy/enforcement already work without Convex.
