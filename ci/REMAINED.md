# ci/ — Remaining work

Checklist of what's left for the Hetzner CI pipeline started in commit `9750c924`. Orthogonal to `/CI_REMAINED.md` (Cloudflare tunnel) at repo root — this one tracks test coverage against the `yaver-test-ephemeral` box.

Conventions: `- [ ]` for pending, `- [x]` when done. Organised in the four phases from the conversation plan.

## Phase 1 — foundation (done)

- [x] Provision `yaver-test-ephemeral` (cax21 / 4 vCPU / 8 GB / hel1 / arm64)
- [x] Dedicated SSH keypair `yaver-ci` registered in Hetzner
- [x] `ci/remote/bootstrap.sh` installs Docker, Node 22, Go 1.22, Python 3.12 + pipx, Ollama + `qwen2.5-coder:1.5b`, aider, opencode, Yaver CLI
- [x] Snapshot `379185792` taken, stored as `HETZNER_TEST_SNAPSHOT_ID`
- [x] Five GitHub secrets set: `HCLOUD_TOKEN`, `HCLOUD_SSH_PRIVATE_KEY`, `HETZNER_TEST_SERVER_ID`, `HETZNER_TEST_SERVER_IP`, `HETZNER_TEST_SNAPSHOT_ID`
- [x] `ci/hcloud/*.sh` — create / wait / sync-repo / snapshot / delete / gather-logs
- [x] `.github/workflows/remote-verify.yml` with `persistent` / `ephemeral` targets
- [x] `ci/README.md` runbook
- [x] `CLAUDE.md` disclaimer — "not a production service"
- [x] Hetzner remote workflows now run a shared preflight (`ci/hcloud/preflight.sh`) so bad `HCLOUD_TOKEN` / stale persistent IP / bad snapshot secrets fail immediately instead of later in `wait-for-ssh` or create/delete cleanup.
- [ ] **Trigger `remote-verify.yml` once against `target=persistent` and confirm green.** Needed to prove the wiring before building anything on top. `gh workflow run remote-verify.yml -f target=persistent` then `gh run watch`.
  Attempted on 2026-04-22 via run `24798505098`; it failed in `Wait for SSH` after resolving `HETZNER_TEST_SERVER_IP`. Current state to check: the persistent box is either down, the stored IP is stale, or SSH is blocked.
- [ ] **Trigger `remote-verify.yml` against `target=ephemeral` once** to prove snapshot-restore + cleanup path works end-to-end. Budget ~10 min and ~€0.003.
  Attempted on 2026-04-22 via run `24798714571`; it failed immediately because `HCLOUD_TOKEN` was rejected as unauthorized. Cleanup also failed for the same reason.

## Phase 2 — wire existing `test-suite.sh` sections to the remote box

`scripts/test-suite.sh` already implements each of these locally. New workflow `.github/workflows/remote-integration.yml` should SSH into the Hetzner box, run `test-suite.sh --<flag>`, and surface the result. Each job gated on whether its secrets are set so we don't fail when a credential is intentionally absent.

- [x] `.github/workflows/remote-integration.yml` scaffold (workflow_dispatch, matrix-per-section, `always-cleanup`)
- [x] Connectivity cases are now explicit one-by-one jobs:
  - [x] `--lan`
  - [x] `--relay`
  - [x] `--relay-docker`
  - [x] `--relay-binary`
  - [x] `--tailscale`
  - [x] `--cloudflare`
- [x] Per-section jobs, each skippable via the `suite` input:
  - [x] `--auth` — email/password lifecycle (no extra secrets needed)
  - [x] `--oauth-mock` — mock provider callback path + auth-link/unlink/merge/TOTP/security-event smoke
  - [x] `--relay-docker` — deploy relay container to box, exercise proxy, teardown
  - [x] `--relay-binary` — same but with native binary
  - [x] `--tailscale` — `tailscale up` with `TAILSCALE_AUTHKEY`, verify cross-machine connect
  - [x] `--cloudflare` — quick tunnel + named-tunnel path (needs `CF_TUNNEL_URL` + `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET`)
  - [x] `--ollama` — wrapper smoke test (model already pulled on the box)
  - [x] `--ollama-ci` — end-to-end ops → runner → ollama path
  - [x] `--hybrid-local` — planner+implementer loop against local qwen
- [x] Each job uploads `/var/log/yaver-ci/*.log` + `test-suite-*.log` as artifacts
- [x] Matrix summary comment / aggregate job summary so one failing section doesn't hide the others.
- [x] Add `--remote-host` / `--remote-ssh-key` flags to `scripts/test-suite.sh` so callers don't need `.env.test` wiring for cross-machine runs.

## Phase 3 — mocked OAuth harness (all 6 providers, no real calls)

Goal: CI-driven "can a user sign in via provider X" tests that never touch `accounts.google.com`, `appleid.apple.com`, etc. Tests stability of our callback routes, Convex user-create path, and session issuance.

### Architecture

- [x] New dir `ci/oauth-mock/` — small Go HTTP server mimicking token + userinfo endpoints per provider. Read-only; no state; deterministic responses based on query params.
- [x] Web app (and where relevant, Convex) made configurable via env:
  - `OAUTH_APPLE_TOKEN_URL` (current callback path uses `id_token` directly; no JWKS fetch in this flow)
  - `OAUTH_GOOGLE_TOKEN_URL`, `OAUTH_GOOGLE_USERINFO_URL`
  - `OAUTH_MICROSOFT_TOKEN_URL`, `OAUTH_MICROSOFT_USERINFO_URL`
  - `OAUTH_GITHUB_TOKEN_URL`, `OAUTH_GITHUB_USERINFO_URL`
  - `OAUTH_GITLAB_TOKEN_URL`, `OAUTH_GITLAB_USERINFO_URL`
  - Each defaults to the real provider URL; test env overrides to `http://localhost:PORT/<provider>/{token,userinfo}`.
- [x] Convex test-mode endpoint `POST /auth/test/oauth-signin` — bypasses the real HTTPS round-trip by accepting pre-built identity claims `(provider, providerId, email, name?)` and returning a real session token. **Gated on `TEST_MODE_ENABLED=1`.** Never enable in prod.
- [x] `test-suite.sh --oauth-mock` section that spins up the mock server, runs the callback path with fabricated `code`/`state`, verifies a session comes back.

### Per-provider checklist

- [x] Apple — realistic ID token JSON (issuer, audience, sub, email, email_verified, is_private_email)
- [x] Google — userinfo JSON (sub, email, email_verified, name, picture)
- [x] Microsoft — v2.0 id_token claims (oid, preferred_username, email)
- [x] GitHub — userinfo endpoint returning `login`, `id`, `email`, `name` + `/user/emails` shape
- [x] GitLab — OIDC userinfo (sub, email, name, username)
- [ ] Email/password — lifecycle test via `test-suite.sh --auth` (no mock, hits Convex directly)

### Convex-side auth audits the mock should exercise

- [x] `findUserForOAuth` — `authIdentities` index hit, primary `(provider, providerId)` fallback, email fallback, all exercised
- [x] `createUserFromOAuth` — idempotency (second call with same `(provider, providerId)` reuses the user)
- [x] `linkOAuthIdentity` — second provider on an existing account (via `/auth/oauth-link/complete`)
- [x] `accountMergeIntent` happy path — totally synthetic, mocks don't matter here
- [x] TOTP-gated unlink refuses stale codes (already covered by Convex unit tests — verify via integration)
- [x] Security-event audit row written for every link / unlink / merge

## Phase 4 — qwen writes hello-world (new test, small)

- [ ] `ci/remote/verify_qwen_codegen.sh`
  - Pose: "write a single-line Python program that prints 'hello yaver'" to `ollama run qwen2.5-coder:1.5b`
  - Capture first code block, extract Python source
  - Pipe through `python3`, assert stdout contains `hello yaver`
  - Retry once on parse miss (qwen 1.5b is dumb; occasionally echoes prose instead of code)
- [ ] `ci/remote/verify_ops_ollama.sh`
  - Start `yaver serve` on the box
  - Call `/ops run --cmd="ollama run qwen2.5-coder:1.5b 'hello'"` via localhost HTTP
  - Assert 200 + non-empty stdout
- [ ] `ci/remote/verify_hybrid_loop.sh`
  - One kick of `yaver autodev --engine hybrid --runner aider-ollama --model ollama_chat/qwen2.5-coder:1.5b` against a trivial throwaway project on the box
  - Assert a commit or file-change happens. Quality doesn't matter; just that the planner→implementer handoff works.
- [x] `ci/remote/verify_host_share_agentless.sh`
  - Agentless Hetzner shell authenticates directly to Convex as a guest, joins a host-share invite for a live host device, verifies the guest can see `codex` in `/agent/runners`, and writes/runs `hello_yaver.py` over the brokered terminal.
  - Helper added: `desktop/agent/cmd/hostshare-terminal-smoke`
- [ ] Run `verify_host_share_agentless.sh` against the real host MacBook
  - Host = `kivanc.cakmak@icloud.com` on the actively running MacBook agent
  - Guest = email/password-only Hetzner test user
  - Current scope: invite/join + runner visibility + brokered terminal hello-world
  - Not yet a `/tasks`-driven Codex-edit smoke; host-share still does not expose guest `/tasks`
- [x] Dedicated smoke auth users created and stored as GitHub secrets
  - `HOST_TEST_EMAIL`
  - `HOST_TEST_PASSWORD`
  - `GUEST_TEST_EMAIL`
  - `GUEST_TEST_PASSWORD`
  - `YAVER_SMOKE_CONVEX_SITE_URL`
- [x] `ci/remote/verify_npm_cli_reinstall.sh`
  - `npm pack` the current `cli/` tree on the remote box, global-install it, verify `yaver` / `yaver-push` / `yaver-mcp`, then reinstall the same tarball to catch lifecycle drift
- [x] Ran `verify_npm_cli_reinstall.sh` on the Hetzner box
  - Install and reinstall both passed for `yaver-cli@1.99.15`
  - Warning observed: `yaver-mobile-headless@0.1.1` requires Node `>=20`, but the box is still on Node `18.19.1`
- [ ] Only publish `yaver-cli` to npm after the host-share agentless smoke and npm reinstall smoke both pass on the Hetzner box

## Stuff we noticed along the way (polish)

- [ ] Docker `hello-world` pull in `verify.sh` runs every time — cheap, but we could cache. Low priority.
- [ ] `verify.sh` runs `go test ./...` in `/opt/yaver/desktop/agent`. Needs build cache volume; first run is slow. Add Docker volume or GOCACHE location persisted between runs.
- [ ] Align the Hetzner box with the advertised Node toolchain before npm release gating
  - `ci/remote/bootstrap.sh` says Node 22, but the current persistent box still reports Node `18.19.1`
  - Either upgrade the box or relax the dependent package engine requirement before calling npm publish "ready"
- [ ] opencode on the box has no API key → can't actually run a real completion. Either wire a scoped OpenAI key (new secret `OPENCODE_OPENAI_KEY`) or skip the opencode smoke and just assert `opencode --version`.
- [x] GitHub-hosted OpenCode coverage now has a separate GLM lane via `runner-integrations.yml` + `GLM_API_KEY`. Keep real OpenCode API smoke on GH-hosted runners; do not spend Hetzner capacity on this.
- [ ] aider same story — needs at least a model config. Skip real-call tests; verify `--version` is enough for now.
- [ ] Bootstrap doesn't install Rust (Cargo) or Flutter. Add when a test needs them, not speculatively.

## Janitor (prevents forgotten ephemeral boxes)

- [ ] `.github/workflows/hcloud-janitor.yml` on a schedule (e.g. hourly)
  - List servers with label `ephemeral=true` older than 6 hours
  - Delete them
  - Does not touch `yaver-test-ephemeral` (it's labelled `ephemeral=true` too → exclude by name, or use a separate `janitor-safe=true` label on the persistent one). **Decide the labelling convention before shipping this job or we'll nuke the persistent box.**

## Documentation follow-ups

- [x] Add a "How I run remote-verify from my laptop" section to `ci/README.md` with the actual command sequence someone can copy-paste.
- [ ] Cross-link from `AI_ARCH.md` → `ci/README.md` so new contributors see the test rig when they touch auth/bootstrap.
- [ ] Note the snapshot-delete-restore cost pattern in the CLAUDE.md "Hetzner Test Server" section (already mentioned, but expand with numbers).

## Known unknowns

- [ ] Whether Convex's `platformConfig` is shared between dev and prod deployments — if so, test runs that set platform config might leak into real users. Confirm the deployment routing and add a guard if needed before Phase 3.
- [ ] Whether the mocked-OAuth approach will catch real provider drift (e.g. Google deprecating a claim). It won't. Document this limitation in `ci/README.md` so nobody assumes mocked tests mean "we work with Google."
