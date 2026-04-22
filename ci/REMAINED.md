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
- [ ] **Trigger `remote-verify.yml` once against `target=persistent` and confirm green.** Needed to prove the wiring before building anything on top. `gh workflow run remote-verify.yml -f target=persistent` then `gh run watch`.
- [ ] **Trigger `remote-verify.yml` against `target=ephemeral` once** to prove snapshot-restore + cleanup path works end-to-end. Budget ~10 min and ~€0.003.

## Phase 2 — wire existing `test-suite.sh` sections to the remote box

`scripts/test-suite.sh` already implements each of these locally. New workflow `.github/workflows/remote-integration.yml` should SSH into the Hetzner box, run `test-suite.sh --<flag>`, and surface the result. Each job gated on whether its secrets are set so we don't fail when a credential is intentionally absent.

- [ ] `.github/workflows/remote-integration.yml` scaffold (workflow_dispatch, job-per-section, `always-cleanup`)
- [ ] Per-section jobs, each skippable via an input:
  - [ ] `--auth` — email/password lifecycle (no extra secrets needed)
  - [ ] `--relay-docker` — deploy relay container to box, exercise proxy, teardown
  - [ ] `--relay-binary` — same but with native binary
  - [ ] `--tailscale` — `tailscale up` with `TAILSCALE_AUTHKEY`, verify cross-machine connect
  - [ ] `--cloudflare` — quick tunnel + named-tunnel path (needs `CF_TUNNEL_URL` + `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET`)
  - [ ] `--ollama` — wrapper smoke test (model already pulled on the box)
  - [ ] `--ollama-ci` — end-to-end ops → runner → ollama path
  - [ ] `--hybrid-local` — planner+implementer loop against local qwen
- [ ] Each job uploads `/var/log/yaver-ci/*.log` + `test-suite-*.log` as artifacts
- [ ] Matrix summary comment (so one failing section doesn't hide the others)
- [ ] Add `--remote-host` / `--remote-ssh-key` flags to `scripts/test-suite.sh` — today it reads `REMOTE_SERVER_IP` + `REMOTE_SERVER_SSH_KEY` from `.env.test`. The workflow writes those into env before invoking, so zero code changes are needed if we mirror the names.

## Phase 3 — mocked OAuth harness (all 6 providers, no real calls)

Goal: CI-driven "can a user sign in via provider X" tests that never touch `accounts.google.com`, `appleid.apple.com`, etc. Tests stability of our callback routes, Convex user-create path, and session issuance.

### Architecture

- [ ] New dir `ci/oauth-mock/` — small Go HTTP server mimicking token + userinfo endpoints per provider. Read-only; no state; deterministic responses based on query params.
- [ ] Web app (and where relevant, Convex) made configurable via env:
  - `OAUTH_APPLE_TOKEN_URL`, `OAUTH_APPLE_KEYS_URL`
  - `OAUTH_GOOGLE_TOKEN_URL`, `OAUTH_GOOGLE_USERINFO_URL`
  - `OAUTH_MICROSOFT_TOKEN_URL`, `OAUTH_MICROSOFT_USERINFO_URL`
  - `OAUTH_GITHUB_TOKEN_URL`, `OAUTH_GITHUB_USERINFO_URL`
  - `OAUTH_GITLAB_TOKEN_URL`, `OAUTH_GITLAB_USERINFO_URL`
  - Each defaults to the real provider URL; test env overrides to `http://localhost:PORT/<provider>/{token,userinfo}`.
- [ ] Convex test-mode endpoint `POST /auth/test/oauth-signin` — bypasses the real HTTPS round-trip by accepting pre-built identity claims `(provider, providerId, email, name?)` and returning a real session token. **Gated on `TEST_MODE_ENABLED=1`.** Never enable in prod.
- [ ] `test-suite.sh --oauth-mock` section that spins up the mock server, runs the callback path with fabricated `code`/`state`, verifies a session comes back.

### Per-provider checklist

- [ ] Apple — realistic ID token JSON (issuer, audience, sub, email, email_verified, is_private_email)
- [ ] Google — userinfo JSON (sub, email, email_verified, name, picture)
- [ ] Microsoft — v2.0 id_token claims (oid, preferred_username, email)
- [ ] GitHub — userinfo endpoint returning `login`, `id`, `email`, `name` + `/user/emails` shape
- [ ] GitLab — OIDC userinfo (sub, email, name, username)
- [ ] Email/password — lifecycle test via `test-suite.sh --auth` (no mock, hits Convex directly)

### Convex-side auth audits the mock should exercise

- [ ] `findUserForOAuth` — `authIdentities` index hit, primary `(provider, providerId)` fallback, email fallback, all exercised
- [ ] `createUserFromOAuth` — idempotency (second call with same `(provider, providerId)` reuses the user)
- [ ] `linkOAuthIdentity` — second provider on an existing account (via `/auth/oauth-link/complete`)
- [ ] `accountMergeIntent` happy path — totally synthetic, mocks don't matter here
- [ ] TOTP-gated unlink refuses stale codes (already covered by Convex unit tests — verify via integration)
- [ ] Security-event audit row written for every link / unlink / merge

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

## Stuff we noticed along the way (polish)

- [ ] Docker `hello-world` pull in `verify.sh` runs every time — cheap, but we could cache. Low priority.
- [ ] `verify.sh` runs `go test ./...` in `/opt/yaver/desktop/agent`. Needs build cache volume; first run is slow. Add Docker volume or GOCACHE location persisted between runs.
- [ ] opencode on the box has no API key → can't actually run a real completion. Either wire a scoped OpenAI key (new secret `OPENCODE_OPENAI_KEY`) or skip the opencode smoke and just assert `opencode --version`.
- [ ] aider same story — needs at least a model config. Skip real-call tests; verify `--version` is enough for now.
- [ ] Bootstrap doesn't install Rust (Cargo) or Flutter. Add when a test needs them, not speculatively.

## Janitor (prevents forgotten ephemeral boxes)

- [ ] `.github/workflows/hcloud-janitor.yml` on a schedule (e.g. hourly)
  - List servers with label `ephemeral=true` older than 6 hours
  - Delete them
  - Does not touch `yaver-test-ephemeral` (it's labelled `ephemeral=true` too → exclude by name, or use a separate `janitor-safe=true` label on the persistent one). **Decide the labelling convention before shipping this job or we'll nuke the persistent box.**

## Documentation follow-ups

- [ ] Add a "How I run remote-verify from my laptop" section to `ci/README.md` with the actual command sequence someone can copy-paste.
- [ ] Cross-link from `AI_ARCH.md` → `ci/README.md` so new contributors see the test rig when they touch auth/bootstrap.
- [ ] Note the snapshot-delete-restore cost pattern in the CLAUDE.md "Hetzner Test Server" section (already mentioned, but expand with numbers).

## Known unknowns

- [ ] Whether Convex's `platformConfig` is shared between dev and prod deployments — if so, test runs that set platform config might leak into real users. Confirm the deployment routing and add a guard if needed before Phase 3.
- [ ] Whether the mocked-OAuth approach will catch real provider drift (e.g. Google deprecating a claim). It won't. Document this limitation in `ci/README.md` so nobody assumes mocked tests mean "we work with Google."
