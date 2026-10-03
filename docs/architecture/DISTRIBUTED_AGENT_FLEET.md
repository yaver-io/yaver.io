# Distributed Agent Fleet Architecture

Status: controller/worker policy, placement, and retry-safety plumbing are
implemented but not deployed; Git integration and durable graph recovery
remain release gates.

## Product statement

Yaver can become one control surface for a user-owned fleet: one controller
machine runs the strongest available planner/reviewer, while several worker
machines run cheaper coding agents in parallel. The five-node reference setup
is:

- controller: Mac mini, Yaver agent, a user-selected premium runner/model;
- workers: two Raspberry Pis, one Intel NUC, and one Linux machine;
- worker runner: OpenCode with a user-selected provider/model such as the
  operator's `deepseek-v4.1-flash` identifier;
- transport: LAN first, Yaver Mesh/relay fallback, same-owner authentication on
  every hop;
- coordination: dependency graph plus isolated Git worktrees and a serialized
  integration queue.

The provider/model string is configuration, not a product constant. Catalog
names, availability, pricing, and subscription rules change. Yaver must probe a
runner/model on the target machine before advertising it as ready.

A ChatGPT subscription is one useful controller seat; it is not five API seats
and it does not remove provider limits. Yaver must never evade account,
concurrency, or rate-limit terms. The performance goal is better wall-clock and
cost efficiency through decomposition, locality, and cheap worker capacity—not
a promise that $20 always equals a particular dollar value of another service.

Official OpenAI documentation says ChatGPT Plus and Pro developer mode support
read/write MCP tools. ChatGPT Work on the web connects to an MCP server through
a plugin URL, while Codex CLI can use Yaver's local stdio MCP registration. A
private LAN-only daemon is therefore not automatically callable from an
ordinary ChatGPT chat; Yaver must expose an authenticated plugin/tunnel or use
the local Codex lane. See [Build for ChatGPT](https://developers.openai.com/chatgpt)
and the [plugin quickstart](https://developers.openai.com/plugins/quickstart).

## Code-verified audit (2026-10-03)

| Capability | Current code | Verdict |
|---|---|---|
| Same-owner machine inventory and capability probes | `console_machines.go`, `/console/machines`, `/agent/capabilities` | Exists |
| Cross-machine authenticated dispatch | `agent_mesh_remote.go`, remote `/tasks` calls | Exists |
| SSH-only worker dispatch (no Yaver account session on worker) | `ssh_fleet_worker.go`, local `ssh_targets` config | Implemented for graph chat nodes; direct SSH only |
| Dependency-aware graph scheduler | `agent_mode.go`, `/agent/graphs`, MCP `agent_graph_*` | Exists |
| Machine balancing and task-slot caps | `agent_mesh.go` | Exists, heuristic |
| Per-device runner/model/provider preference | Convex `userSettings.primaryRunnerByDevice` | Exists |
| Controller identity | Convex `userSettings.primaryDeviceId` | Exists |
| Persistent worker membership and disclosure | Convex `workerDeviceIds`, `showWorkerDevices`, `opportunisticFleet` | Implemented in this slice; not deployed |
| Durable coordinator ownership | graph manager lives on whichever agent receives `/agent/graphs` | Partial; primary routing/recovery missing |
| Mobile multi-machine graph picker | `mobile/app/(tabs)/agent.tsx` | Exists |
| Upstream result passed to dependent node | `graphNodeWithDependencyContext` | Implemented in this slice |
| Saved per-machine runner/model used by graph placement | `loadAgentFleetPreferences` | Implemented in this slice |
| Strong controller preferred for plan/review | fleet preference scoring in `agent_mesh.go` | Implemented in this slice |
| Remote source preparation | graph slice contains repo metadata | Partial: metadata is not a checkout |
| Per-node remote Git worktree and branch | none in graph execution | Missing; release gate |
| Deterministic merge/integration queue | none in graph manager | Missing; release gate |
| Restart-safe graph leases/reconciliation | running graphs become failed on daemon restart | Missing; release gate |
| Dynamic decomposition into worker shards | templates are static | Missing |
| Master-first device picker on mobile/web/tvOS/Android TV | shared Convex settings | Implemented in this slice; other compact surfaces remain Phase 4 |
| Fleet-wide headless acceptance test | `agent mesh-smoke` tests one remote task | Partial |

Important code-audit findings explain why the missing items matter:

1. Before this slice, graph dependencies controlled start order but did not
   carry the parent result into the child prompt. “Plan → implement → verify”
   therefore looked orchestrated without sharing the plan.
2. Automatic runner selection happened before machine placement and probed the
   controller's local PATH. This could stamp the controller runner onto every
   node and ignore a worker's saved OpenCode/model configuration.
3. Remote task creation retried a non-idempotent `POST /tasks` across transport
   candidates. A slow runner startup could therefore buy several identical
   model turns after the caller had timed out. Task creation now carries one
   stable idempotency key and the receiving agent coalesces/replays it.

## Terms and hierarchy

```text
account
└── fleet
    ├── controller (one elected device, replaceable)
    │   ├── planner runner/model
    │   └── reviewer/integrator runner/model
    └── workers (zero or more owned devices)
        └── node
            ├── capacity and capability probe
            ├── allowed roles
            ├── runner (for example OpenCode)
            ├── provider/model preference
            └── local credentials (never Convex)
```

In the first slice, “controller” means the preferred planning/review machine;
the graph-manager process still lives on whichever agent received the create
request. Durable controller election/routing and restart takeover are Phase 2
work and must not be implied by the UI before then.

“Controller” is a role, not a trust shortcut. A worker independently validates
the caller's owner token/device authorization. A compromised relay or another
relay tenant must not be able to dispatch work.

An explicitly configured SSH-only worker is the narrow exception to the
*Yaver-token mechanism*, not to authentication. The master must already have
OS-level key/ssh-agent access recorded in its local SSH target book. The worker
does not register with Convex, open an agent HTTP port, or join relay/mesh; the
master invokes a bounded hidden CLI protocol over SSH and sends the task JSON on
stdin. SSH authenticates the OS principal. This lane deliberately rejects
password-backed fleet configuration and never turns the elected controller role
into ambient authority over arbitrary Yaver devices.

For Git repositories, implementation runs in an isolated worker worktree at the
controller's exact base commit. The worker returns a bounded binary Git patch,
including new non-ignored files; the controller verifies the base and
materializes it into the graph's shared integration worktree before master
validation. Commit drift, an unavailable base, an oversized patch, or an apply
conflict is a named failure—Yaver never presents report-only output as an
independently validated remote implementation.

## Control and data planes

### Convex control plane

Convex stores only non-secret, bounded metadata:

- controller device ID;
- enabled worker device IDs;
- runner/model/provider identifiers and role preferences;
- max task slots and user overrides;
- graph summaries, leases, and non-sensitive status in a later phase;
- repository slug, branch name, commit SHA, and artifact hashes—never absolute
  filesystem paths, prompts, source, stdout, tokens, or provider keys.

The first slice reuses and extends the existing settings row:

- `primaryDeviceId` = controller;
- `workerDeviceIds[]` = explicitly enabled worker pool;
- `showWorkerDevices` = progressive-disclosure preference;
- `opportunisticFleet` = whether automatic graph placement may use the pool;
- `primaryRunnerByDevice[]` = per-node runner/model/provider;
- graph request `allowedDevices[]` = run-specific worker pool.

Roles are capabilities, not mutually exclusive hardware labels. The controller
device may also remain in `workerDeviceIds[]`, allowing a master runner and the
default OpenCode worker to share one machine while still using separate graph
nodes and the isolated shared-chain worktree.

There is intentionally no second `agentFleetPolicy` source of truth. Future
role allowlists and Git policy should extend these rows or use a clearly scoped
project policy, not duplicate controller/worker identity under new names.

SSH-only workers are different: their host, OS user, identity file, remote work
directory, runner and model are private master-local configuration under
`~/.yaver/config.json`. Absolute paths and SSH topology must not be copied into
Convex. Their inventory IDs use `ssh:<local-name>` and are valid only through
the master that owns that local target book.

### Machine-local data plane

Each node owns:

- provider/API credentials in its vault or provider config;
- source checkout and isolated worktrees;
- full prompts, runner output, patches, and test artifacts;
- runner sessions and raw logs.

Yaver sends task envelopes over direct LAN, Mesh, or relay using the same bearer
authorization. The controller receives bounded semantic results plus artifact
references. Raw source/output does not go through Convex.

## Scheduling contract

Placement is a constrained choice, not round-robin:

1. reject offline/unreachable machines;
2. require an operational runner/model probe, not only an installed binary;
3. enforce task slots, memory pressure, disk floor, thermal/low-power limits,
   and provider concurrency limits;
4. honor explicit task pins and allowed device/runner sets;
5. prefer the controller for planning, architecture, final review, and merge;
6. spread independent implementation/test shards across workers;
7. prefer capability-specific hosts for Xcode, Android, attached hardware,
   browser, simulator, and deploy operations;
8. queue rather than silently changing runner/model when no eligible slot is
   available.

The scheduler must expose the reason for every placement. A model name in a
config row is inventory; a successful bounded probe on that node is readiness.

## Graph and artifact contract

### Default MCP master/worker loop

`agent_fleet_run` is the opinionated MCP entry point for coding work. It builds
one dependency chain:

```text
master-plan
  technical architecture + roadmap + risks + testing strategy
        ↓ bounded MASTER_PLAN
worker-implement (OpenCode by default)
  implement + iterate tests + summarize WORKER_REPORT
        ↓ working tree + bounded report
master-validate
  inspect actual diff + rerun evidence + final VALIDATION_REPORT
```

Master and worker are logical agent/process roles, not mutually exclusive
device tags. One computer may run the master through Codex/Claude and the
worker through OpenCode. When enabled worker devices exist, placement prefers
them for the worker role; otherwise a controller with both runners ready may
host both roles sequentially. On one computer the chain reuses one isolated
per-run Git worktree, so final validation sees the worker's actual dirty tree
without touching the user's main checkout. Separate `master_runner`/`master_model` and
`worker_runner`/`worker_model` settings prevent one device preference from
forcing the same model into both roles. `worker_runner` defaults to `opencode`.

The master does not accept the worker's success claim as proof. The final node
receives both the original plan and bounded worker report, inspects the actual
repository state, and reruns the highest-value checks. Neither role may push or
deploy without a separate explicit user action.

Cross-device mutation remains report-only until the Phase 1 branch/artifact
transport lands. The worker report therefore includes base/head or dirty-state
identity, diff stat, bounded key hunks, and test evidence. If that patch is not
materialized on the master's validation worktree, the master must say so and
must not return an unqualified `accepted` verdict. This keeps today's MCP tool
useful for remote iteration without pretending summarized prose is a synced
Git tree.

Every node receives an immutable input envelope:

```json
{
  "graphId": "…",
  "nodeId": "…",
  "role": "implement",
  "baseCommit": "<sha>",
  "repo": "owner/name",
  "dependencies": [{ "nodeId": "plan", "resultHash": "…" }],
  "runner": "opencode",
  "model": "operator-configured-model",
  "deadlineMs": 1800000,
  "attempt": 1
}
```

Every result is explicit:

```json
{
  "status": "completed",
  "summary": "…",
  "branch": "yaver/graph/<graph>/<node>",
  "baseCommit": "<sha>",
  "headCommit": "<sha>",
  "tests": [{ "name": "…", "status": "passed" }],
  "artifacts": [{ "kind": "patch", "sha256": "…" }]
}
```

Prose cannot stand in for commit identity, test outcome, or ownership.

## Git synchronization and conflict handling

Parallel agents must never edit one shared checkout.

1. Controller pins the run's base commit. Dirty or unpushed controller work is
   an explicit choice: commit/push, create a local bundle, or refuse dispatch.
2. Each worker clones/fetches the repository into its managed source cache.
3. Each node gets a dedicated worktree and branch:
   `yaver/graph/<graph-id>/<node-id>`.
4. A worker may push only its graph branch. It never pushes `main` and never
   force-pushes.
5. Controller integration is serialized. It verifies the recorded base/head,
   fetches the branch, and performs a no-commit merge in an integration
   worktree.
6. Textual conflicts produce structured paths/hunks. A deterministic resolver
   handles generated files/lockfiles only when project policy names the command.
7. Semantic conflicts go to one integrator task with both node summaries and
   diffs. The integrator commits a new resolution; it does not rewrite worker
   history.
8. Tests run on the integrated tree. Only an explicit user action may merge or
   push the destination branch.

Lease keys should be `(repo, baseCommit, pathScope)` with an expiry and renewal.
The existing autorun fleet lease code is useful precedent, but graph execution
must not claim it is protected until it actually acquires those leases.

## Failure plumbing

Minimum stable codes:

- `FLEET_CONTROLLER_UNREACHABLE`
- `FLEET_WORKER_UNREACHABLE`
- `FLEET_RUNNER_NOT_READY`
- `FLEET_MODEL_REJECTED`
- `FLEET_NO_ELIGIBLE_SLOT`
- `FLEET_REPO_DIRTY_UNSYNCED`
- `FLEET_REPO_CLONE_FAILED`
- `FLEET_BASE_SHA_MISMATCH`
- `FLEET_BRANCH_CONFLICT`
- `FLEET_LEASE_LOST`
- `FLEET_GRAPH_INTERRUPTED`

Each includes device ID/alias, graph/node ID, retryability, and an invocable
route. “Worker failed” or an endless spinner is not sufficient.

Retry rules:

- planning/review: retry once on another eligible strong node if the controller
  is unavailable;
- read-only analysis/tests: retry on another worker from the same base SHA;
- mutation nodes: retry only after proving the previous lease/process is dead;
- merge/deploy: never automatically duplicate; reconcile the receipt first.

## Live Raspberry Pi validation (2026-10-03)

The first real worker was an `aarch64` Raspberry Pi reached directly through
Tailscale at roughly 19 ms. The validation used the operation, not a device
badge:

- SSH and Yaver's remote `/health` probe succeeded;
- OpenCode 1.18.30 ran natively on the Pi;
- the configured OpenRouter spelling
  `openrouter/deepseek/deepseek-v4.1-flash` correctly failed with HTTP 401
  because that provider had no credential on the Pi;
- the credentialed native identifier `deepseek/deepseek-flash` returned the
  exact requested marker `HCS_DEEPSEEK_OK`;
- the successful tiny turn reported 92,317 input tokens, 8 output tokens,
  93,989 total tokens, and USD 0.013857342 cost.

That run found three product defects, each now represented by source and a
regression test:

1. SSH bootstrap polling consumed a one-shot enrollment token before the
   remote process could use it. SSH launch now probes the remote agent's
   `/health` operation and never consumes enrollment state.
2. OpenCode was installed under `~/.opencode/bin`, but SSH/daemon PATH did not
   include it. Runner resolution and task execution now include that standard
   location and use the resolved executable.
3. A slow `POST /tasks` could time out at several candidate addresses while
   every request continued into paid inference. Remote task creation now uses
   one stable idempotency key and the server coalesces concurrent retries for
   ten minutes.

The live Pi was unblocked for the session and its deployed settings row now
selects OpenCode with `deepseek/deepseek-flash`. The new fleet-role fields and
the idempotency receiver are source changes, not live claims: they require a
normal reviewed release/deploy before a production acceptance rerun. The Pi's
current tmux-owned agent is also not a durable boot service.

## Token economics and capacity model

### Measured conclusion

The hybrid can reduce the marginal cost of substantial, well-bounded coding
shards. It will not automatically turn a USD 20 subscription plus cheap nodes
into a guaranteed USD 500-equivalent service. A ChatGPT subscription is not an
API-credit balance, and five devices do not multiply one account's quota.
Parallel machines improve throughput only while provider credentials, quotas,
network, memory, and integration capacity also permit parallel work.

The Pi measurement is a warning against microtask fan-out. Its almost-empty
prompt still carried about 92k input tokens and cost USD 0.01386. At the same
fixed overhead, 100 tiny tasks cost about USD 1.39 and 1,000 cost about USD
13.86 before useful generation, review, retries, or merge work. The transport
retry bug observed during the test created four completed copies of one logical
request, turning that baseline into roughly USD 0.055 before counting the other
attempts. Idempotency is therefore a cost control, not merely HTTP hygiene.

Do not generalize that one observation into a provider price table. It is the
runner's reported cost for this machine, model, configuration, and date. Yaver
should record bounded per-turn measurements and use the user's real accepted
model probe instead of embedding volatile catalog pricing.

### Per-shard decision rule

For a proposed worker shard, estimate:

```text
worker cost = fixed context input
            + task-specific input
            + generated output
            + retry probability * redo cost
            + controller review cost
            + integration/conflict cost
```

Dispatch only when the expensive controller tokens avoided, or the wall-clock
value of genuine parallelism, exceeds that total. The scheduler also needs a
budget ceiling and should queue/ask instead of silently changing providers or
spending through repeated attempts.

Practical initial policy:

- keep architecture, security-sensitive changes, decomposition, integration,
  and final review on the strongest controller;
- send independent searches, tests, documentation audits, and bounded module
  implementations to cheap workers;
- batch related questions into one coherent shard rather than spawning a model
  for each file or assertion;
- target at least 15–30 minutes of useful human-equivalent work per paid shard
  until measurements demonstrate a lower break-even point;
- run one inference-heavy task at a time on each Pi; start with at most two on
  the NUC/Linux host, then raise the cap only from measured memory, latency,
  thermal, and provider-limit evidence;
- fan out only independent work. Parallel agents editing overlapping files
  create review and merge costs that can erase the model savings;
- disable nested agent delegation by default. The graph controller owns the
  concurrency budget; an OpenCode worker recursively spawning more OpenCode
  sessions makes spend and ownership non-auditable unless it receives an
  explicit child budget and idempotency scope.

For the proposed five machines, two or three active useful workers are likely
the initial sweet spot; all four should be available but not filled merely
because they exist. Raspberry Pis are good for analysis, small edits, linting,
and targeted tests. The NUC/Linux machine should take heavier builds, while
Apple-only builds and final integration stay on the Mac mini. This is a
starting hypothesis that fleet telemetry must confirm.

## Client surface contract

Every surface operates on the same graph API and policy; presentation differs:

- mobile/tablet: configure controller, worker pool, per-node runner/model, max
  parallelism; start/stop; inspect node placement and failures;
- web/Electron: same full control, plus branch/conflict and raw evidence detail;
- car/glass: start a saved fleet profile, hear status, stop, and approve one
  named action; no dense topology editor;
- tvOS/Android TV: graph status, node placement, one next action;
- watchOS/Wear OS: running/blocked/completed summary, stop, retry a safe node;
- CLI/MCP: full headless configure/doctor/start/show/stop/reconcile verbs.

The default view says “3 of 5 nodes running · controller reviewing” rather than
showing every capability chip. Detailed topology is one level deeper.

## Roadmap and release gates

### Phase 0 — truthful orchestration foundation (started)

- [x] carry bounded parent results into dependent prompts;
- [x] defer automatic runner choice until after machine placement;
- [x] use saved per-device runner/model preferences;
- [x] prefer the saved primary controller for plan/review;
- [x] keep mobile Agent Mode on Fleet/Auto unless the user explicitly forces a
  runner;
- [x] persist master/worker membership and master-first disclosure settings;
- [x] make cross-transport task creation idempotent;
- [x] add polite role/readiness summaries to MCP machine inventory;
- [x] allow one device to persist both master and worker roles, with an
  account-level automatic-worker-use switch on mobile and web;
- [x] carry structured master/worker stage metadata into task list/detail APIs
  and render quiet role chips on mobile and web task surfaces;
- [x] add `agent_fleet_run` with master plan → OpenCode worker report → master
  validation, including same-device shared-worktree validation;
- [ ] add the same structured readiness receipt to graph creation.

### Phase 1 — source isolation and integration

- remote repo prepare endpoint returns canonical checkout identity;
- per-graph/node worktree create/status/cleanup endpoints;
- base-SHA pinning, branch receipts, patch/artifact hashes;
- integration queue and structured conflicts;
- tests that break isolation and prove the guard fails.

No parallel mutation workload is production-ready before this phase.

### Phase 2 — durable fleet policy in Convex

- role allowlists beyond the implemented persistent enabled worker pool;
- default parallelism/cost ceilings;
- bounded graph leases and restart reconciliation;
- route graph creation to the elected controller and support explicit takeover;
- ownership validation for every referenced device;
- migration that reuses `primaryDeviceId` and `primaryRunnerByDevice` rather
  than creating competing truths.

### Phase 3 — adaptive decomposition

- controller planner emits typed shards with path scopes and dependencies;
- scheduler validates the DAG and detects overlapping mutation scopes;
- dynamic follow-up/retry nodes;
- strong controller synthesis over worker results and integrated tree.

### Phase 4 — all client surfaces

- full editor on mobile/tablet and web/Electron;
- compact status/action components in shared RN car/glass code;
- explicit native ports for tvOS, Android TV, watchOS, and Wear OS;
- parity tests that enumerate every surface and API operation.

### Phase 5 — performance and cost policy

- measured model acceptance/latency/cost per machine;
- queue-time and critical-path estimates;
- thermal/network-aware Pi scheduling;
- cache affinity for repositories, dependencies, and build artifacts;
- budget ceiling that stops or asks rather than silently spending.

## Five-node acceptance test

Headless first:

1. Register five owned devices and select the Mac mini as primary.
2. Save Codex (or another premium runner/model) on the controller and OpenCode
   plus the chosen cheap provider/model on all four workers.
3. Probe every runner/model and verify secrets never appear in `/settings`,
   device rows, graph JSON, logs, or task snapshots.
4. Start a graph with one plan node, four independent read-only audit/test
   shards, one integration node, and one review node.
5. Assert plan/review land on the controller and worker shards spread across
   eligible nodes without exceeding their slots.
6. Kill one Pi mid-task; assert a named failure, lease expiry, and safe retry on
   another worker.
7. Disconnect the controller; assert workers stop accepting new mutation work
   after lease expiry and the graph remains recoverable.
8. Reconnect/restart the controller; reconcile existing remote task/branch
   receipts instead of marking all running work failed.
9. Create an intentional same-file conflict; assert no direct `main` mutation,
   structured conflict output, integrator resolution, then integrated tests.
10. Verify no push/deploy occurs without the user's explicit action.

Closed loop second:

- mobile in a real iPhone device context: configure/start, see five named nodes,
  observe failover and conflict route;
- web: inspect graph/branches/evidence and approve integration;
- TV/watch/car/glass: see the same status and stop/retry the permitted action;
- PIXELS/NAMED/SILENT verdict, with SILENT as failure.

## Definition of done

The feature is not “five green device dots.” It is done when a five-node graph
can survive one worker loss and one controller restart, preserve source
isolation, deterministically integrate non-conflicting work, visibly route a
conflict, respect provider limits and user cost policy, and never push or deploy
without explicit approval.
