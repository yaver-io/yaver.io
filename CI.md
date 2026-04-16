# Yaver CI

This document explains Yaver's GitHub Actions setup, which workflows need secrets, and how to reproduce the important checks locally.

## At a Glance

Yaver has three CI layers:

- `CI` in [`.github/workflows/ci.yml`](.github/workflows/ci.yml)
  Runs the standard PR/build/test pipeline for Go, mobile, web, backend, SDKs, and agent E2E.
- `autodev-smoke` in [`.github/workflows/autodev-smoke.yml`](.github/workflows/autodev-smoke.yml)
  Verifies CLI planning surfaces without burning model credits.
- `runner-integrations` in [`.github/workflows/runner-integrations.yml`](.github/workflows/runner-integrations.yml)
  Exercises Yaver as the wrapper around real runner CLIs like Claude Code, Codex, OpenCode, and local Ollama.

There are also heavier or specialized workflows:

- [`.github/workflows/autodev-e2e.yml`](.github/workflows/autodev-e2e.yml)
- [`.github/workflows/hybrid-local.yml`](.github/workflows/hybrid-local.yml)
- [`.github/workflows/test-suite.yml`](.github/workflows/test-suite.yml)

## Required Secrets

These are the main secrets the repo expects today.

### Runner Integration Secrets

These power real wrapper tests in `runner-integrations` and `autodev-e2e`.

- `ANTHROPIC_API_KEY`
  Required for Claude Code wrapper tests.
- `OPENAI_API_KEY`
  Required for Codex and OpenCode wrapper tests when those are configured against OpenAI.

### Agent / Backend Secrets

These are used by the broader integration suite.

- `CONVEX_SITE_URL`
- `RELAY_QUIC_ADDR`
- `RELAY_HTTP_URL`
- `RELAY_PASSWORD`
- `CF_TUNNEL_URL`
- `CF_ACCESS_CLIENT_ID`
- `CF_ACCESS_CLIENT_SECRET`
- `TAILSCALE_AUTHKEY`

### Recommended GitHub Environments

For provider-backed workflows, keep secrets in protected environments instead of broad repo scope.

- `anthropic-prod`
  Used by `autodev-e2e`.
- `testing`
  Used by `test-suite`.

You can require manual approval for these environments before the secrets are exposed to a runner.

## How To Set Secrets

In GitHub:

1. Open the repo.
2. Go to `Settings -> Secrets and variables -> Actions`.
3. Add the repository secret or environment secret.
4. If you want approval gates, create the environment first under `Settings -> Environments`.

With GitHub CLI:

```bash
gh secret set ANTHROPIC_API_KEY --repo kivanccakmak/yaver.io
gh secret set OPENAI_API_KEY --repo kivanccakmak/yaver.io
gh secret set CONVEX_SITE_URL --repo kivanccakmak/yaver.io
```

For environment-scoped secrets:

```bash
gh secret set ANTHROPIC_API_KEY --env anthropic-prod --repo kivanccakmak/yaver.io
gh secret set OPENAI_API_KEY --env testing --repo kivanccakmak/yaver.io
```

## What Each Workflow Covers

### `ci.yml`

Baseline repo checks:

- path-filtered PR/build/test jobs
- Go unit tests and builds
- mobile TypeScript checks
- backend typecheck
- web build
- SDK tests
- agent E2E

### `autodev-smoke.yml`

Fast validation of CLI surfaces:

- `yaver autodev --plan`
- `yaver autotest --plan`
- `yaver autoideas --plan`
- `yaver autoinit --plan`
- runner override parsing for `codex` and `opencode`

This should stay cheap and safe to run on every PR.

### `runner-integrations.yml`

Real wrapper validation:

- installs the actual runner CLI
- builds `yaver`
- runs [scripts/test-runner-integration.sh](scripts/test-runner-integration.sh)
- checks `autoinit` and `autoideas` through the selected runner

Current runner coverage:

- `claude:sonnet`
- `codex`
- `opencode`
- `ollama:qwen2.5-coder:1.5b`

If the needed API key is not configured, the workflow skips that provider cleanly.

### `autodev-e2e.yml`

Real Anthropic-backed autodev/autoinit/autoideas run against a tiny fixture repo. This costs credits and should remain protected.

### `hybrid-local.yml`

Local-only hybrid path:

- real Ollama
- real Qwen local model
- real Aider
- no frontier API keys required

## How To Run The Important Checks Locally

### Agent build and unit tests

```bash
cd desktop/agent
go test ./...
go build ./...
```

### Mobile typecheck

```bash
cd mobile
npx tsc --noEmit
```

### Autodev smoke surface

```bash
cd desktop/agent
go build -o /tmp/yaver .

mkdir -p /tmp/fixture && cd /tmp/fixture
git init -q
git config user.email ci@example.com
git config user.name ci
echo '{"name":"fixture","version":"0.0.1"}' > package.json
echo "- [ ] item one" > remained.md
git add . && git commit -q -m init

/tmp/yaver autodev fixture --plan --hours 1 --no-autotest
/tmp/yaver autoideas fixture --plan --hours 1 --runner codex
/tmp/yaver autoinit fixture --plan --runner opencode
```

### Real runner wrapper tests

Build Yaver once:

```bash
cd desktop/agent
go build -o /tmp/yaver .
```

Run the shared wrapper script:

```bash
YAVER_BIN=/tmp/yaver bash scripts/test-runner-integration.sh codex
YAVER_BIN=/tmp/yaver bash scripts/test-runner-integration.sh claude:sonnet
YAVER_BIN=/tmp/yaver bash scripts/test-runner-integration.sh opencode
YAVER_BIN=/tmp/yaver bash scripts/test-runner-integration.sh ollama:qwen2.5-coder:1.5b
```

Environment requirements:

- `ANTHROPIC_API_KEY` for Claude Code
- `OPENAI_API_KEY` for Codex and usually OpenCode
- local `ollama` daemon plus the requested model for Ollama runs

### Full integration suite

```bash
./scripts/test-suite.sh
```

Or use the helper:

```bash
./scripts/run-ci-local.sh
```

## How To Add A New Runner To CI

1. Make sure the runner exists in [desktop/agent/tasks.go](desktop/agent/tasks.go).
2. Add runtime auth detection in [desktop/agent/runner_auth.go](desktop/agent/runner_auth.go) if needed.
3. Make sure `RunAIGenerator` supports the runner in [desktop/agent/ai_generator.go](desktop/agent/ai_generator.go).
4. Add a matrix entry in [`.github/workflows/runner-integrations.yml`](.github/workflows/runner-integrations.yml).
5. If it needs credentials, add a dedicated secret and document it here.
6. Verify it locally with [scripts/test-runner-integration.sh](scripts/test-runner-integration.sh).

## Notes

- Yaver does not create GitHub secrets by itself. Workflow code expects them to exist already.
- `runner-integrations` is intended to prove Yaver works as the orchestrator/wrapper, not just that a provider key is valid.
- Keep expensive provider-backed workflows gated. Cheap flag parsing and local checks should remain in default PR CI.
