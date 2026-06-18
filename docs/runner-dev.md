# Yaver Runner — Self-Hosted Cloud-Runner Ecosystem

**Status:** design doc, not yet implemented. Last updated 2026-04-28.

## TL;DR

Cloud-runner SaaS is a $200B+ category sliced into a dozen sub-vendors
(GitHub Actions, EAS Build, Modal, e2b, Codespaces, Devin, …). Each
charges per-minute or per-build for what is, structurally, "run a job
on a machine that isn't yours." Yaver already has the four
ingredients to collapse most of these into a self-hosted free
substitute — fleet of user-owned machines, P2P transport, vault for
secrets, and coding-agent runners — but they are wired only into
end-to-end flows (deploy-ship), not exposed as a generic
runner primitive. This doc proposes a unified `runner` abstraction
(`Pool × Job × Schedule × Result × Notify`), shipped behind a new
`ops runner` verb, and a 6-phase build plan that brings CI, build
farm, sandbox, browser tests, GPU jobs, and agentic execution into
the platform without paying any SaaS bill.

---

## 1. The SaaS runner landscape

Compressed survey of categories developers currently pay for. Prices
are 2026 retail for a single small team unless noted.

### 1.1 CI/CD runners

| Vendor | Price | Notes |
|---|---|---|
| GitHub Actions (Linux x64) | $0.008/min after 2k free | macOS = $0.08/min (10×), Windows $0.016 |
| GitHub Actions Larger Runners | $0.04–0.16/min | "Faster CI" — same workload on bigger box |
| CircleCI | $15/mo + per-credit | "Performance plan" credits ≈ $1.50 per 1k min |
| Buildkite | $15/user/mo | BYO agents — they orchestrate |
| Jenkins | self-hosted | Free but operationally expensive |
| Buildjet / WarpBuild / Blacksmith / Depot / Ubicloud / Namespace.so | $0.004–0.01/min | "Faster GHA" — 2–4× speed at lower price |
| Drone / Woodpecker / Forgejo Actions | self-hosted | OSS alternatives |

**Pain:** macOS minutes are 10× Linux. Private repos burn the free
tier in a week. Self-hosted runners are easy to start and impossible
to maintain (auth rotation, queue saturation, security drift).

### 1.2 Mobile build farms

| Vendor | Price | Notes |
|---|---|---|
| EAS Build (Expo) | $99/mo unlimited iOS+Android | Most popular for RN/Expo |
| Bitrise | $36/mo starter, $200+/mo team | Generic mobile CI |
| Codemagic | $0.038/min macOS | Pay-per-build |
| Xcode Cloud | $14.99/mo for 25 hours, +$49.99 for 100 | Apple-native |
| App Center | deprecated 2025 | Microsoft sunset |

**Pain:** every team needs a Mac to ship iOS. Renting one in the
cloud is wasteful when most devs already own one.

### 1.3 Sandbox / code execution (LLM era)

| Vendor | Price | Notes |
|---|---|---|
| e2b.dev | $0.000016/sec compute, $0.000035/sec disk | Sandbox for AI agents |
| Modal | $0.000038/sec CPU, $0.0035/sec A10G | Serverless GPU |
| Replicate | per-second model billing | ML inference |
| Daytona | $39/user/mo cloud | Dev environments + sandboxes |
| RunPod | $0.39/hr A40 spot | Bare-metal GPU rent |
| Beam | $0.000025/sec | Modal competitor |

**Pain:** you pay for cold-start sprawl + idle reservation. Your own
M-series Mac sits at 5% utilization the whole time.

### 1.4 Browser / E2E runners

| Vendor | Price | Notes |
|---|---|---|
| BrowserStack | $29–249/mo | Real-device cloud |
| Sauce Labs | $39+/mo | Same |
| LambdaTest | $19+/mo | Same |
| Checkly | $40–200/mo | Playwright-as-a-service + monitoring |
| Datadog Synthetics | $5 / 10k runs | Bundled with their APM |

**Pain:** you write Playwright once and pay forever to run it
elsewhere. Most checks would happily run on your Hetzner box.

### 1.5 Cron / scheduled / background jobs

| Vendor | Price | Notes |
|---|---|---|
| Heroku Scheduler | free tier | Limited cadence |
| AWS EventBridge | $1 per 1M invocations | Plus Lambda runtime |
| GCP Cloud Scheduler | $0.10/job/mo | Plus Cloud Run runtime |
| cron-job.org | free / $4/mo | Public ping only |
| Healthchecks.io | $0–20/mo | Heartbeat (passive) |
| Cronitor | $30+/mo | Heartbeat + active probe |
| Inngest / Trigger.dev / Hatchet | $20–200/mo | Background-job orchestration with retries + DLQ |
| Temporal Cloud | per-action | Workflow engine |

**Pain:** every Yaver-style project needs cron eventually and ends
up either committing a sketchy `*/15 * * * *` to a runner or paying
for a 5-vendor stack.

### 1.6 GPU / ML runners

| Vendor | Price | Notes |
|---|---|---|
| Modal | $0.000597/sec L4, $0.0035/sec A10G | "Serverless" framing |
| Replicate | per model, per second | Inference-only |
| Together AI | per-token | LLM hosting |
| RunPod | $0.39/hr A40 spot, $1.49 H100 spot | Bare GPU rent |
| Vast.ai | market-priced GPUs | Spot market |
| Lambda Labs | $0.50–2.00/hr | On-demand |
| Beam Cloud | per-second | Cheaper Modal |

**Pain:** if you already own a 4090/5090 or rent a Hetzner GPU box,
every LLM inference call you route through Modal is $0.30–2 lit on
fire.

### 1.7 Cloud dev environments

| Vendor | Price | Notes |
|---|---|---|
| GitHub Codespaces | $0.18/hr 2-core, $0.72/hr 8-core | Persistent VM |
| Gitpod | $9–39/user/mo | Same shape, multi-cloud |
| Coder | self-hosted enterprise | Open core |
| Daytona | $39/user/mo | "Dev env as a service" |
| Replit | $20/mo | Combined IDE + runtime |
| StackBlitz | $9–39/mo | WebContainers for Node |

**Pain:** the "dev environment" half is just SSH + a Docker image. Your
own desktop already does this.

### 1.8 Agentic runners (the new front)

| Vendor | Price | Notes |
|---|---|---|
| Devin (Cognition) | $500/mo | Autonomous engineer |
| Replit Agent | $25/mo | Lightweight version |
| Cursor (background agent) | $20/mo + token usage | Spawns headless Cursor jobs in cloud |
| Vercel v0 | $20/mo | UI generation runner |
| Bolt.new / Lovable.dev | $20–50/mo | Whole-app generation |
| Aider/Codex/Claude Code (BYOK) | API price | Local CLI; no managed runner |

**Pain:** Devin's pitch ("background coding agent") is a hosted
Aider+Codex+Claude loop with a file system and a browser. Yaver
already has all the parts — they just don't run on a queue yet.

### 1.9 Deploy targets that double as runners

| Vendor | Price | Notes |
|---|---|---|
| Vercel | $20/user + bandwidth | Build + run + edge |
| Netlify | $19/user + minutes | Same shape |
| Cloudflare Workers | $5/mo + per-request | Already where Yaver web ships |
| Fly.io Machines | per-VM-second | "Run a Docker image on demand" |
| Render / Railway | per-instance + bandwidth | PaaS |

**Pain:** every Yaver project ships to one of these. The build
itself happens on their infra at their per-minute rate, and you can
do nothing about it.

### 1.10 Test parallelization / utility

| Vendor | Price | Notes |
|---|---|---|
| Knapsack Pro | $19+/mo | Test sharding |
| Buildkite Test Engine | per-test | Flaky test detection |
| Mergify / GraphiteCI / Aviator | $20+/user/mo | Merge queue |

**Pain:** these are GitHub Actions parasites — they exist because
Actions can't parallelize a 30-minute test suite cheaply.

---

## 2. Yaver's structural advantages

Why we can collapse most of section 1 into self-hosted free.

| Advantage | Why it matters |
|---|---|
| **User-owned fleet** | A typical Yaver user has laptop + Mac mini + Hetzner test box + (maybe) GPU box. That's 4 free runners with 3 OS variants and a Mac for iOS builds. |
| **P2P transport** | Runners don't need public IPs; mobile/web can reach them via relay. No "configure ingress + TLS + auth" tax. |
| **Vault** | Secrets live on the host, never in a runner config file or env var pasted into a SaaS UI. |
| **Already has coding agents** | claude-code / codex / opencode are wired runners. The "agent runner" category is half-shipped. |
| **Workspace manifest** | `yaver.workspace.yaml` already declares per-app stack, env, depends. One more field (`runner:`) closes the loop. |
| **Container sandbox** | `container_runner.go` + `Dockerfile.sandbox` exist for guest-isolated tasks — directly reusable for sandbox/e2b-style work. |
| **Multi-surface parity** | MCP + HTTP + CLI + mobile + web + `yaver code` already share the same control plane via `ops`. |

The thing nobody else can do: **route a job to a specific named
machine in your fleet by capability** without that machine being on
the public internet. GitHub Actions self-hosted runners get close,
but the orchestration is hostile and per-tenant.

---

## 3. What lives in Yaver today

Inventory of existing runner-shaped primitives, so we don't
re-invent them.

| Primitive | File(s) | What it does |
|---|---|---|
| `TaskManager` | `desktop/agent/tasks.go` | Queues + executes coding-agent tasks. Foundational. |
| Runner registry | `desktop/agent/runner_*.go` | `spawnClaudeCode`, `spawnCodex`, `spawnOpenCode` spawners. |
| Container sandbox | `desktop/agent/container_runner.go`, `Dockerfile.sandbox` | Docker isolation per task; build caches via named volumes. |
| `/deploy/ship` | `desktop/agent/deploy_run.go` | Vault-aware deploy runner with composite targets, history, webhooks, error classification. |
| `/schedules` | hinted in CLAUDE.md guest scope | Owner-only scheduler; primitive exists, surface incomplete. |
| `/dev/start` | `desktop/agent/devserver.go` | Per-framework dev-server runners (Expo / Flutter / Vite / Next). |
| `RemoteTrigger` MCP | exposed via `RemoteTrigger` tool | Already routes work to a remote machine. |
| Workspace manifest | `yaver.workspace.yaml` | Declarative app catalogue with depends + env + provider. |
| `ops` verb registry | `desktop/agent/ops*.go` | 20 verbs; uniform MCP/HTTP/CLI surface. |
| Hetzner test ephemeral | `ci/hcloud/`, `ci/remote/` | Snapshot+recreate Linux box used for remote integration tests. Working blueprint for "burst" capacity. |
| Guest scopes | `desktop/agent/guest_scope.go` | Already supports tier-by-tier endpoint allow-listing — directly reusable for runner ACLs. |

What is missing: **a uniform job-submission surface that routes
across pools, persists results, retries, alerts, and exposes
history.** Each primitive above has its own ad-hoc shape; there's
no unified "runner" verb.

---

## 4. The gap map

Per category: build it, skip it, or extend something existing.
"Build" means new code; "extend" means it sits on top of an existing
primitive without reinventing.

| Category (§1) | Decision | Where it lives |
|---|---|---|
| 1.1 CI/CD | **Build** | `pkg/runner/ci/` — GHA-self-hosted-runner registration + a portable workflow shape |
| 1.2 Mobile build | **Extend** `/deploy/ship` | Expose pre-deploy build phase as `ops runner build mobile` |
| 1.3 Sandbox / e2b | **Extend** container_runner | New `ops runner sandbox` verb on top of existing Docker isolation |
| 1.4 Browser/E2E | **Build** | `pkg/runner/browser/` — Playwright runner; multi-machine "regions" out of fleet |
| 1.5 Cron / jobs | **Extend** `/schedules` | Promote to first-class — see §6.5 |
| 1.6 GPU / ML | **Build** | `pkg/runner/gpu/` — register GPU machines in fleet, route inference jobs |
| 1.7 Cloud dev env | **Build (small)** | Wrap existing remote-worker into a "dev session" lifecycle |
| 1.8 Agentic | **Extend** tasks | `ops runner agent` — Devin-shape API into existing runners |
| 1.9 Deploy | **Already done** | `/deploy/ship` covers the case |
| 1.10 Test parallel | **Skip v0** | Add only if v0 CI is too slow |

Skip rationale documented in §10.

---

## 5. Unified `runner` abstraction

The whole thing reduces to one model.

```
Runner = Pool × Job × Schedule × Result × Notify
```

- **Pool** — set of machines, capability-tagged. Examples:
  `pool=darwin-arm64`, `pool=linux-gpu`, `pool=any`. Built from the
  Convex device registry, but the actual identity is the same
  device IDs Yaver already uses.
- **Job** — what runs. One of:
  - `shell` — command + env + workdir. Always available.
  - `docker` — image + entrypoint + mounts. Reuses
    `container_runner.go`.
  - `workflow` — declarative steps (a list of shell or docker steps,
    sequential or parallel). GitHub-Actions-shaped but flatter.
  - `agent` — prompt + runner (`claude-code` / `codex` / `opencode`).
  - `playwright` — script + browser + assertions. v0 special case.
  - `gpu` — image + GPU constraints + entrypoint. Routes to
    GPU-tagged pool.
- **Schedule** — `manual` / `cron(expr)` / `interval(d)` /
  `webhook(secret)` / `event(name)` / `on_pr` / `on_push`.
- **Result** — typed: exit code, structured outputs, log path,
  artifacts (paths to files in `~/.yaver/runner/runs/<id>/out/`),
  durations, classification (reuse `deploy_classify.go`'s shape).
- **Notify** — `mobile_push` / `email` / `slack` / `webhook` / `none`.

A run is the join of all five plus an ID. Stored in
`~/.yaver/runner/runs/<id>/` with the same on-disk shape as
`~/.yaver/deploys/<id>/`. Agent-shutdown survives via the same
ring-buffer + GC quota pattern (`deployDiskQuotaBytes` analogue).

### MCP / HTTP / CLI surface

Same shape as `ops`:

```bash
# CLI
yaver runner add <name> --pool darwin-arm64 --job shell:'./bin/test.sh' --cron '0 */1 * * *'
yaver runner list
yaver runner runs <name>            # history
yaver runner logs <run-id>          # full log
yaver runner trigger <name>         # one-shot
yaver runner pools                  # list pools + machines + load
yaver runner pause <name>
```

```http
GET  /runner/jobs
POST /runner/jobs                   # body: RunnerSpec
POST /runner/jobs/{name}/trigger
GET  /runner/jobs/{name}/runs
GET  /runner/runs/{id}
GET  /runner/runs/{id}/log
GET  /runner/pools
POST /runner/webhooks/{name}        # external trigger
```

MCP: `ops runner {op: list|add|trigger|runs|log|pools|pause, ...}`
plus aliases `runner_*`.

### Pool semantics

A pool is a label, not a separate object. Each Yaver agent declares
capabilities in its heartbeat:

```json
{
  "deviceId": "abc123",
  "capabilities": ["darwin-arm64", "macos", "xcode-15", "node-22", "docker", "ollama:qwen2.5-coder:14b"],
  "load": 0.3
}
```

A job's `pool` selector is a capability expression
(`darwin-arm64 AND xcode-15`, `linux-gpu`, `any`). The scheduler
picks the lightest-loaded machine matching the expression. If none
match, the job queues. Same pattern Buildkite uses for self-hosted
agents — proven shape.

### Privacy

Runner results follow the existing privacy contract: outputs stay on
the executing agent. Convex sees only `(jobId, runId, status, durationMs, exitCode, errorClass)` for cross-machine roll-up. The
forbidden-keys allowlist in `convex_privacy_test.go` gains
`runner_output`, `runner_log`, `runner_artifact`.

---

## 6. Per-category build plan

### 6.1 CI runner (replaces GitHub Actions self-hosted)

**Two modes:**

**Mode A — be a GHA runner.** Yaver registers as a self-hosted
GitHub Actions runner on configured repos. Push to a repo with a
matching label (`runs-on: [self-hosted, yaver-darwin]`) → GHA pings
runner → Yaver pulls the job, executes via the unified runner
shell/docker job, posts logs back to GHA. This is straight
[`actions/runner`](https://github.com/actions/runner) integration:
download the .NET runner binary, run as a child process, treat its
stdout as our log stream, persist into `~/.yaver/runner/runs/<id>/`.

**Mode B — native workflow shape.** A `.yaver/runner.yml` in the repo
declares jobs in a flatter shape:

```yaml
jobs:
  test:
    pool: any
    steps:
      - shell: go test ./...
      - shell: cd web && npm test
  build-mobile:
    pool: darwin-arm64
    needs: test
    steps:
      - shell: ./scripts/deploy-testflight.sh
        env_from_vault: true
```

`yaver runner sync` reads this file, registers the jobs, and wires
GitHub webhooks (or just polls) to trigger on push/PR. Webhook
secrets via vault.

**Why both:** Mode A migrates an existing repo with zero churn —
"point your runs-on at our label, save $0.08/min on macOS." Mode B
is the better long-term shape — vault-aware, no GHA YAML
limitations, runs identically on a laptop offline.

**Killer feature:** macOS minutes. A user with a Mac mini on
Hetzner has unlimited macOS CI minutes via Mode A while paying €0
versus $0.08/min on GitHub.

**Cost vs SaaS:** A team running 200 GHA minutes/day on macOS pays
$480/mo. Yaver Mode A on a Mac mini = $0/mo (the mini is theirs
already).

**Files (proposed):**
- `desktop/agent/runner_ci.go` — Mode A registration + lifecycle
- `desktop/agent/runner_workflow.go` — Mode B parser + executor
- `desktop/agent/runner_workflow_test.go`
- `desktop/agent/runner_github_webhook.go`

### 6.2 Mobile build farm (extends `/deploy/ship`)

Add a "build only, no upload" mode to the deploy pipeline. The
existing TestFlight / Play Store templates already separate build
(xcodebuild archive / gradle bundleRelease) from upload (altool /
upload-playstore.py). Expose the build phase as a runner job:

```bash
yaver runner build mobile --target=ios-archive    # produces /tmp/Yaver.xcarchive
yaver runner build mobile --target=android-aab    # produces app/build/outputs/bundle/release/app-release.aab
```

Output is uploaded to a per-run artifact dir; mobile/web UI shows
the artifact path + size + download via `/runner/runs/<id>/artifact`.

**Why this kills EAS Build:** EAS is $99/mo for unlimited mobile
builds because they run on their managed Macs. A Yaver user with
their own Mac builds for $0. Yaver also already has build cache
warmth (the deploy script's resumable archive logic) which EAS
explicitly does not — Apple build cache is per-VM and lost on
every EAS invocation.

**Files:**
- `desktop/agent/runner_build_mobile.go` — wraps existing template
  shell script in the runner shape; reuses
  `BuildDoctor`, vault env, `mobile-cache-cleanup`
- Extend `desktop/agent/deploy_script_gen.go` to emit "build only"
  variants of each existing target

### 6.3 Sandbox / e2b alternative (extends container_runner)

`/runner/sandbox/start` returns a sandbox handle. `/runner/sandbox/{id}/exec` runs a command in it. `/runner/sandbox/{id}/files/*` reads/writes files. `/runner/sandbox/{id}/stop` tears down.

The sandbox is just a `yaver-sandbox` Docker container with a
generous-but-not-infinite mount layout:

| Path | Mode | Why |
|---|---|---|
| `/sandbox` | rw | Working dir |
| `/sandbox/.cache` | named volume | Persists across sandbox lifetime per (user, label) |
| `/host/projects/<slug>` | ro mount of declared workspace app | Optional; off by default |
| network | bridge with optional egress allowlist | Same shape as Docker default |

This is a near-drop-in for [e2b's API](https://e2b.dev/docs/api).
The use case is "an LLM running on the user's laptop wants to
execute Python it just wrote." Currently every coding agent that
isn't claude-code-with-the-Bash-tool needs a remote sandbox. Yaver
exposes one without the per-second bill.

**Why we can do this for $0:** the user's own machine. Modal/e2b
charge per-second-CPU because they're amortizing colocation costs.
Yaver's amortization is "you already bought the laptop."

**Files:**
- `desktop/agent/runner_sandbox.go` — lifecycle wrapping `container_runner.go`
- `desktop/agent/runner_sandbox_http.go` — HTTP surface
- MCP tools `sandbox_start / sandbox_exec / sandbox_files / sandbox_stop`

**Caveat:** the e2b API contract is large. We ship a subset
(start, exec, files, stop, kill) and document gaps explicitly.

### 6.4 Browser / E2E runner

Built on Playwright. Job spec:

```yaml
job:
  type: playwright
  script: |
    test('checkout works', async ({ page }) => {
      await page.goto('https://yaver.io/checkout');
      await page.fill('[name=card]', '4242 4242 4242 4242');
      await page.click('button[type=submit]');
      await expect(page.locator('.receipt')).toBeVisible();
    });
  browsers: [chromium]
  pool: any
schedule:
  cron: "*/15 * * * *"
notify:
  on_fail:
    - mobile_push
```

Runs on whichever pooled machine has Playwright installed (capability
tag). Multi-machine "regions" come for free — declare
`pools: [laptop, mac-mini, hetzner-test]` and you get 3-region
synthetic monitoring at $0/mo.

**Cost vs SaaS:** Checkly is $40/mo for 12 checks at 1-min cadence
across 5 regions. Yaver: $0 with whatever cadence the user's
machines can sustain.

**Files:**
- `desktop/agent/runner_playwright.go` — installs Playwright via
  `npx playwright install` on first use, caches under
  `~/.yaver/runner/cache/playwright/`
- Reuses container sandbox for isolation by default

### 6.5 Cron / scheduled jobs (promote `/schedules`)

`/schedules` already exists in the agent (mentioned in
`guest_scope.go` allowlists). Promotion = first-class HTTP/MCP/CLI
surface, dead-letter queue, retry policy, history.

| Field | Meaning |
|---|---|
| `name` | unique slug |
| `cron` / `interval` / `at` | when |
| `job` | unified-runner Job (see §5) |
| `retry` | `{max, backoff, on_codes}` |
| `dead_letter` | `notify:` block fires after `retry.max` |
| `timeout` | hard kill |
| `concurrency` | "skip if previous still running" / "queue" / "kill previous" |

**Heartbeat receiver:** `POST /schedules/{name}/beat` from inside the
job (replaces Healthchecks.io ping). Missing beats fire `notify`.

**Files:**
- `desktop/agent/scheduler.go` — promote existing
- `desktop/agent/scheduler_http.go`
- `desktop/agent/scheduler_dlq.go`

### 6.6 GPU / ML runner

Capability tag the GPU machine: `linux-gpu`, `gpu:rtx-4090`,
`ollama`, `vllm`. The unified runner sees those tags and routes
matching jobs.

Standard shape — point a job spec at a Docker image with `--gpus all`,
mount a model dir, run inference. The thing this replaces is "rent
a Modal A10G to run my own fine-tuned model" → "use the 4090 already
under my desk."

For LLM serving specifically: ship a `yaver runner gpu serve <model>`
that fronts Ollama / vLLM / TGI behind the agent's auth + relay,
making the user's GPU box a private LLM endpoint reachable from any
Yaver client.

**Files:**
- `desktop/agent/runner_gpu.go`
- `desktop/agent/runner_llm_serve.go` — Ollama / vLLM frontends

### 6.7 Cloud dev environment (lightweight)

`yaver dev session start <project>` opens a remote-worker session
against a target machine, returning a connection bundle (SSH-style
URL + token, or a relay-tunneled VS Code Remote handle). Inside that
session the user has shell + the project workdir + vault env
preloaded. Closing the session GCs the resources.

This is the existing remote-worker path with a "session" wrapper +
TTL + auth scoping. Borrowed-session work in the host-share verifier
is the same shape.

**Why not Codespaces/Gitpod parity:** the persistent VM is the
expensive part. Yaver's session is cheap because the machine is
the user's own — there is no provisioning step.

**Files:**
- `desktop/agent/runner_dev_session.go`
- Extends existing `host-share` borrowed-workspace primitives

### 6.8 Agentic runner (Devin-shape)

The most distinctive. Devin's API is roughly:

```
POST /sessions     {repo_url, prompt}        → session_id
GET  /sessions/{id}                          → status, plan, log
POST /sessions/{id}/message {content}        → append guidance
```

We expose:

```
POST /runner/agent/sessions {workdir, prompt, runner, engine, hours}
GET  /runner/agent/sessions/{id}
POST /runner/agent/sessions/{id}/message {text}
```

Internally, this is a `TaskSpec` wrapping the existing task runner.
The runner registry already covers claude-code / codex / opencode.
The new piece is the session API + mid-flight steering.

**Cost vs Devin:** Devin = $500/mo unlimited agentic runs (with
their model bill on top). Routing through a user-owned Claude Code /
Codex subscription is the same workload at the user's existing
subscription rate — and runs on hardware they own.

**Files:**
- `desktop/agent/runner_agent_session.go` — API surface
- Reuses `tasks.go`

### 6.9 Deploy farm (already shipped)

`/deploy/ship` covers TestFlight, Play, Cloudflare, Convex, npm,
PyPI with composite targets, idempotent retries, history, webhooks,
HMAC. No further work in this category for v0; just expose
`deploy/*` as a `runner` family alias for naming consistency.

### 6.10 Test parallelization

Skip in v0. Revisit if v0 CI on a single Mac mini is the bottleneck
for any user. The bones are already there: a Job's `parallel: N`
field could split a test command across N machines in the same
pool. Cheap to add later.

---

## 7. Cross-cutting infrastructure

Shared across every runner type.

### 7.1 Job queue + scheduler

In-process priority queue, persisted at `~/.yaver/runner/queue.json`.
Jobs claim a runner slot from a per-pool semaphore (default = 1 per
agent, configurable via `~/.yaver/config.json:runner_concurrency`).

For multi-machine scheduling the scheduler is **best-fit local**:
each agent decides for itself whether to claim a job from the
shared Convex queue, based on its capability match + load. No
central scheduler. This is identical to how Buildkite scales —
proven for tens of thousands of agents per tenant.

The Convex shared queue stays metadata-only:
`(jobId, runId, status, claimedBy, claimedAt)` — no payloads. The
actual job spec lives on the agent that owns the job definition; if
the executing agent is different, the spec is sent over the relay
P2P channel (existing remote-proxy flow).

### 7.2 Result store + GC

`~/.yaver/runner/runs/<id>/`:
- `meta.json` — job ref, durations, exit code, classification
- `output.log` — full stdout+stderr
- `artifacts/` — declared outputs (size-capped)
- `events.ndjson` — typed events (start, step-start, step-end, fail, retry)

Same GC pattern as `deploys/`: ring buffer (default 500 runs per
job) + 5 GB total quota; oldest first.

### 7.3 Observability

- `/runner/runs?status=failed&since=24h` — query.
- Prometheus exposition (optional): `/metrics` already exists in the
  agent; add runner counters (`yaver_runner_runs_total{status}`,
  `yaver_runner_run_duration_seconds`).
- Mobile push integration via existing notification path.

### 7.4 Auth & guest scopes

Two new scopes:

| Scope | Allowed |
|---|---|
| `GuestScopeRunnerView` | `GET /runner/jobs`, `GET /runner/runs/{id}` (only own runs), `GET /runner/runs/{id}/log` |
| `GuestScopeRunnerSubmit` | adds `POST /runner/jobs/{name}/trigger` (only project-scoped, only manual jobs, only force-containerized) |

Same shape as the existing `feedback-only` / `deploy` tiers. Owner
gets all paths.

### 7.5 Webhook receivers

`POST /runner/webhooks/{slug}` with HMAC verification (reuse the
deploy webhook signing primitives). Triggers a job by name when the
HMAC validates and the slug matches a registered hook. Use case: a
GitHub PR webhook fires a Yaver CI job.

---

## 8. Surfaces

### 8.1 CLI

```bash
yaver runner pools
yaver runner add <name>
yaver runner trigger <name>
yaver runner runs <name> [--limit 20]
yaver runner logs <run-id>
yaver runner pause <name>
yaver runner sandbox start [--label foo]
yaver runner sandbox exec <id> -- <cmd>
yaver runner sandbox stop <id>
yaver runner build mobile --target=ios-archive
yaver runner agent session start <workdir> --prompt "..." --runner claude-code
yaver runner agent session msg <id> "tighten the test for empty input"
yaver runner github setup            # registers a self-hosted GHA runner
yaver runner gpu serve qwen2.5-coder:14b --pool linux-gpu
```

### 8.2 HTTP

`/runner/*` namespace per §5.

### 8.3 MCP

`ops runner {op, ...}` plus convenience aliases (`runner_trigger`,
`sandbox_*`, `runner_agent_*`).

### 8.4 Mobile

New "Runner" tab. Sections:
- **Jobs** — list with last-run status, next-run time, machine pool.
- **Runs** — flat history with filters; tap to see log + artifacts.
- **Sandboxes** — active sandboxes with quick-attach to terminal via existing `/exec` path.
- **Pools** — fleet view with load + capabilities; tap to inspect.
- **Push** — failed runs trigger a mobile notification with a "rerun" action.

### 8.5 Web

Same as mobile via relay. Public read-only **runner status page**
(`/runners/<slug>`) — opt-in per-job, shows last 30 days uptime &
latency. Optional, default off.

### 8.6 `yaver code`

Footer panel surfaces in-flight runs on the attached machine. `/runs`
in the REPL opens the runs view; `/run <name>` triggers a job; `/sandbox` opens a fresh sandbox shell. This aligns with the
existing `/incidents`-style minor commands.

---

## 9. Cost model & positioning

A representative single-developer / small-team baseline today:

| Need | Vendor | $/mo |
|---|---|---|
| GHA on macOS, 30 hr/mo | GitHub | $144 |
| Mobile builds | EAS Build | $99 |
| Sandbox for AI agents | e2b | $30 |
| Browser monitors | Checkly | $40 |
| Cron + heartbeats | Cronitor | $30 |
| GPU bursts | Modal | $50 |
| **Total** | | **$393** |

Yaver substitute (assuming user has laptop + Mac mini + Hetzner test
+ optional GPU box, all already paid for):

| Need | Yaver | $/mo |
|---|---|---|
| GHA on macOS | Mode A on Mac mini | $0 |
| Mobile builds | `runner build mobile` on Mac mini | $0 |
| Sandbox | container_runner via `runner sandbox` | $0 |
| Browser monitors | `runner playwright` on any pool | $0 |
| Cron + heartbeats | `/schedules` | $0 |
| GPU bursts | `runner gpu` on owned/spot GPU | metered electricity |
| **Total** | | **~$0–10** |

The platform's value proposition condenses to: **stop renting what
you already own**.

What we are NOT cheaper at:
- Apple-only macOS for users without a Mac. Xcode Cloud's $14.99/mo
  is unbeatable if you don't own one. We say so plainly.
- Always-on probing when your laptop sleeps. Use the Hetzner test
  box (or any user-controlled VPS) for that pool. We say so plainly.

---

## 10. Risks and non-goals

### 10.1 Non-goals

- **Multi-tenant SaaS.** The moment Yaver runs jobs for other
  people's repos on a shared fleet, we inherit Modal's cost
  structure. Stay single-tenant.
- **Heroku-style PaaS.** "Push and we deploy" lives in
  `/deploy/ship` and is bounded to declared targets. We do not host
  long-running web apps.
- **Replacing GitHub Actions for OSS projects.** Free tier is
  already free; the win is private repos + macOS minutes.
- **Enterprise-grade SLAs.** A Mac mini under a desk is not 99.99%.
  The status page must show "best-effort, owned-by-you"
  positioning. Honesty here is the moat.
- **Becoming a coding-agent vendor.** We compose agents (Claude /
  Codex / Aider / Ollama). We do not train one.

### 10.2 Risks

| Risk | Mitigation |
|---|---|
| Sleeping laptop = missed runs | UI surfaces "agent offline" state explicitly per pool; recommend Hetzner-test-ephemeral pattern; opt-in caffeinate hook for laptops the user marks "always on" |
| Apple developer signing locked to one machine | Vault holds the API key; runner_build_mobile uses the same `APP_STORE_KEY_*` as `/deploy/ship`. Solved. |
| Capability drift (machine X claims it has Xcode-15 but doesn't) | Periodic capability reverification job built into the heartbeat. If a job fails with a "tool not found" classifier, the agent self-demotes that capability tag and notifies. |
| Long-running runs eating the queue | Per-pool concurrency + per-job timeout + `concurrency: skip` policy default. |
| Privacy contract regression | Add `runner_*` keys to the forbidden list in `convex_privacy_test.go` upfront so any new sync path catches it in CI. |
| Self-hosted GHA runner security | Use [GitHub's ephemeral mode](https://docs.github.com/en/actions/hosting-your-own-runners) — runner registers with a token, processes one job, dies. No persistent inbound auth. Requires `--just-in-time` token from GitHub. |
| Sandbox escape | We already have the sandbox primitive in production for guest tasks. Extend the same hardening: read-only by default, named-volume cache only, opt-in egress, syscall allowlist via `--security-opt`. |
| Cost-of-electricity for GPU pools | Surface estimated kWh per run in the run summary so the user sees they're not actually free; just cheaper than Modal. |

---

## 11. Shipping plan

Six phases. Each is a self-contained release; ship behind a
`managed.runner` flag (per the existing per-subsystem managed
toggle pattern) so it's opt-in.

### Phase 1 — Foundation (1–2 weeks)

- [ ] `pkg/runner/` skeleton with the `Pool × Job × Schedule × Result × Notify` types.
- [ ] HTTP + MCP + CLI surface (read-only first: list/runs/log).
- [ ] In-process queue + persistent run store under `~/.yaver/runner/`.
- [ ] Reuse `deploy_run.go` ring-buffer/GC logic.
- [ ] Unit tests pinning the privacy contract.
- [ ] `ops runner` verb + `yaver runner` CLI.

**Done when:** can submit a `shell` job manually, see it run, see
its log, GC it.

### Phase 2 — Sandbox + agent sessions (1 week)

- [ ] `runner sandbox start/exec/files/stop` on top of `container_runner.go`.
- [ ] `runner agent session` API wrapping the task runner.
- [ ] MCP tools `sandbox_*`, `runner_agent_session_*`.
- [ ] Mobile UI: Sandboxes tab.

**Done when:** an LLM agent on the user's laptop can spin up a
sandbox, exec Python in it, and tear it down via MCP.

### Phase 3 — Scheduler + heartbeats (1 week)

- [ ] Promote `/schedules` to first-class with retry, DLQ, timeout,
  concurrency policy.
- [ ] `POST /schedules/{name}/beat` heartbeat receiver.
- [ ] Integrate with `notify:` block (push / webhook).
- [ ] Mobile UI: Schedules section in Runner tab.

**Done when:** a `*/15` cron with `notify.on_fail.mobile_push`
demonstrably fires a push notification.

### Phase 4 — CI runner (2 weeks)

- [ ] Mode A: register as GitHub Actions self-hosted runner;
  job-by-job ephemeral mode.
- [ ] Mode B: `.yaver/runner.yml` parser + executor.
- [ ] GitHub webhook handler + HMAC.
- [ ] `runner build mobile` extending `/deploy/ship` build phase.

**Done when:** a real Yaver-team repo's CI is one of "uses
Mode A" or "uses Mode B" and has been green for 7 days. Compare
$/mo before vs. after.

### Phase 5 — Browser + GPU (1–2 weeks)

- [ ] `runner playwright` job type with installable Playwright
  cache.
- [ ] Multi-pool fan-out (`pools: [a, b, c]`) for "regions for free."
- [ ] `runner gpu serve` for Ollama/vLLM frontend.
- [ ] Capability-tag heartbeat extension.

**Done when:** Yaver's own marketing site has a 3-region Playwright
synthetic check running on user-owned machines, and an Ollama-backed
client points at a `runner gpu serve` endpoint.

### Phase 6 — Polish + status pages (1 week)

- [ ] Public read-only status page (`/runners/<slug>`).
- [ ] Mobile push round-trip with "rerun" quick action.
- [ ] Prometheus metrics endpoint.
- [ ] Docs (`docs/runner.md`).

**Done when:** end-to-end docs ship; a new user installs Yaver and
follows a tutorial that goes "register CI runner → run mobile build
→ schedule a cron → watch it on mobile" in under 30 minutes.

### Phases not yet scheduled

- Test parallelization (`parallel: N`) — only if Phase 4 CI is the
  bottleneck.
- Cloud dev session lifecycle — wait for borrowed-session flow to
  stabilize first.
- Runner federation (route a job from team A's Yaver to team B's
  fleet under contract) — speculative; not until single-tenant is
  proven.

---

## 12. Open questions

These need a decision before Phase 1 lands.

1. **Job spec format — YAML or HCL or JSON?** `yaver.workspace.yaml`
   is YAML; `deploy_script_gen.go` is bash; tasks are JSON. YAML is
   probably right for `.yaver/runner.yml` to mirror GHA shape, but
   the API surface (HTTP/MCP) stays JSON.
2. **Pool definition — Convex-stored or local?** Currently leaning
   "capabilities are pushed via heartbeat (existing pattern) but
   pool *labels* are derived from capability expressions"; no new
   table. Confirm with the privacy contract review.
3. **Where does the `.yaver/runner.yml` live?** Repo root or under
   `.yaver/`? The latter matches `yaver.workspace.yaml`'s
   precedent.
4. **Should the scheduler be cluster-aware out of the gate?** v0
   single-machine; multi-machine claim via Convex shared queue in
   v1. Not blocking.
5. **GHA runner mode A — ephemeral or long-lived?** Recommend
   ephemeral (`--ephemeral`) for security; needs token-fetch via
   GitHub App. Spec the App registration before Phase 4.
6. **Naming — `runner` vs `worker` vs `pipeline`?** "Runner" is
   GHA-aligned and reads cleanly in CLI. Lock it in unless someone
   has a strong objection during Phase 1.
7. **Pricing position — does Yaver ever charge for runner usage on
   user-owned hardware?** Recommendation: never. Charge only if/when
   we host hardware ourselves (Phase 6+). Locks the
   "self-hosted = free forever" promise into the brand.
8. **Compatibility with the legacy `RemoteTrigger` MCP and the
   existing `ops run` verb?** Rename `ops run` → `ops runner shell`
   internally for clarity? Or keep `ops run` as the
   one-shot-shell-against-an-agent shortcut and `ops runner` as the
   queued/scheduled persistent surface?

---

## Appendix A — Source files touched / created (estimate)

| File | Action | Phase |
|---|---|---|
| `desktop/agent/runner.go` | new (core types) | 1 |
| `desktop/agent/runner_http.go` | new | 1 |
| `desktop/agent/runner_cmd.go` | new | 1 |
| `desktop/agent/runner_test.go` | new | 1 |
| `desktop/agent/ops_runner.go` | new (MCP verb) | 1 |
| `desktop/agent/runner_sandbox.go` | new | 2 |
| `desktop/agent/runner_agent_session.go` | new | 2 |
| `desktop/agent/scheduler.go` | promote | 3 |
| `desktop/agent/scheduler_dlq.go` | new | 3 |
| `desktop/agent/runner_ci.go` | new | 4 |
| `desktop/agent/runner_workflow.go` | new | 4 |
| `desktop/agent/runner_github_webhook.go` | new | 4 |
| `desktop/agent/runner_build_mobile.go` | new | 4 |
| `desktop/agent/runner_playwright.go` | new | 5 |
| `desktop/agent/runner_gpu.go` | new | 5 |
| `desktop/agent/runner_llm_serve.go` | new | 5 |
| `desktop/agent/guest_scope.go` | extend (RunnerView/Submit) | 1 |
| `desktop/agent/convex_privacy_test.go` | extend (runner_* keys) | 1 |
| `mobile/app/runner.tsx` | new | 2 |
| `mobile/src/lib/runner.ts` | new | 2 |
| `web/components/dashboard/RunnerView.tsx` | new | 2 |
| `web/lib/agent-client.ts` | extend | 2 |
| `docs/runner.md` | new | 6 |
| `CLAUDE.md` | new "Runner" section | rolling |

## Appendix B — Comparison table (one-liner positioning per category)

| Category | Yaver verb | Replaces | $/mo saved (est, single team) |
|---|---|---|---|
| CI macOS | `ops runner ci` | GitHub Actions macOS minutes | $50–500 |
| Mobile build | `ops runner build mobile` | EAS Build | $99 |
| Sandbox | `ops runner sandbox` | e2b / Modal sandbox | $30–200 |
| Browser E2E | `ops runner playwright` | Checkly | $40–200 |
| Cron + heartbeat | `ops scheduler` | Cronitor / Healthchecks | $20–50 |
| GPU LLM | `ops runner gpu` | Modal / Replicate | varies, $50–500+ |
| Agentic | `ops runner agent` | Devin | $500 |
| Deploy | `ops deploy` (shipped) | Vercel/Netlify build min | $20–100 |

Aggregate: a single solo dev with a Mac mini + an old GPU under their
desk recovers $300–$900/mo depending on workload, with no SaaS
account to maintain.

---

End of report.
