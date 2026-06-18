# DOCKER_REMAINED.md

Loose ends from the May 2026 yaver-test-ephemeral cleanup. Docker is no
longer installed on the test box because (a) we hadn't actually exercised
the Docker / sandbox path much and (b) a stray `convex-dashboard` container
was creating the `docker0` / `br-*` interfaces that produced the stale
`172.18.0.1` heartbeat row that broke mobile pairing for two days.

This file is a parking lot for the Docker-related work we deferred so we
don't lose track of it.

## What was removed from yaver-test-ephemeral (2026-05-02)

- One running container: `ghcr.io/get-convex/convex-dashboard:latest`
  (port 6791, started 9 days ago, marked "Up 3 days"). Stopped + removed.
- `apt-get remove --purge -y docker.io docker-ce docker-ce-cli
  containerd.io docker-buildx-plugin docker-compose-plugin`
- `/var/lib/docker`, `/etc/docker`, `/var/lib/containerd`
- `/etc/systemd/system/multi-user.target.wants/docker.service`
- The `docker0` bridge (172.17.0.1) and any `br-<id>` bridges.

After cleanup, only `lo`, `eth0`, and `tailscale0` remain on the box.

## What still needs work in the codebase

### 1. Container-sandbox feature (`yaver serve --containerize-{guests,host}`)

Documented in `CLAUDE.md` under "Container Sandbox (Optional Task
Isolation)". The implementation is in:

- `desktop/agent/Dockerfile.sandbox` — sandbox image
- `desktop/agent/container_runner.go` — `ContainerRunner`
- `desktop/agent/sandbox_cmd.go` — `yaver sandbox build|status`
- `desktop/agent/sandbox_http.go` — `/sandbox/{status,config,build}`
- `desktop/agent/config.go` — `containerize_guests`, `containerize_host`,
  `container_*` fields
- `desktop/agent/tasks.go` — task execution branch
- `desktop/agent/mcp_tools.go` — `sandbox_status`, `sandbox_config`

It compiles and the unit tests pass, but **end-to-end coverage is
thin**. We have not exercised:

- Multi-tenant guest isolation under load (does Docker's namespacing
  actually keep one guest's task from snooping another's `/workspace`?)
- The `Dockerfile.yaver` per-project image override
- Cache volumes (`yaver-npm-cache`, `yaver-gradle-cache`, etc.) across
  agent restarts
- iOS builds — explicitly NOT supported (xcodebuild needs macOS host)
  but the error path when a guest tries `yaver build ios` inside a
  containerized agent is unverified

### 2. Docker is currently a hard dependency for `--containerize-*`

`canDoNativeInstall` checks for `xcodebuild`, but there's no equivalent
preflight for the sandbox path that distinguishes "Docker missing" from
"Docker present but daemon down". `yaver doctor build` should grow a
`docker` row.

### 3. Provisioning script left Docker on by default

`scripts/provision-machine.sh` installs Docker as part of the standard
Hetzner setup. With the npm-only distribution and the May 2026 decision
to keep Docker optional, that should change to install Docker only when
the operator opts in (e.g. `provision-machine.sh --with-docker`).

The convex-dashboard container that bit us was started by a one-off
Compose file outside the repo. We should not be running third-party
containers under the same hostname/IP as the yaver agent without an
explicit isolation story.

### 4. CLAUDE.md table claims "Docker image (multi-arch)"

Old install path. Removed from the landing page in 1.99.124, but
CLAUDE.md still mentions it under Distribution. Strip when the
container-sandbox feature is properly tested.

### 5. Multi-user mode (`yaver serve --multi-user`) overlap

`CLAUDE.md` describes a multi-user mode that gives each user an isolated
workspace at `/var/yaver/users/yaver-{userId[:8]}/`. That overlaps
conceptually with the container-sandbox path. Decide which one is the
primary isolation story, or document explicitly when each is used.

## Action items (next session, not now)

- [ ] Stand up a fresh Hetzner box with Docker, run a guest task that
      tries to `cat /etc/passwd`, verify it sees the container's
      `/etc/passwd` not the host's.
- [ ] Verify `Dockerfile.yaver` per-project override is picked up.
- [ ] Add `docker` to `yaver doctor build`'s checklist.
- [ ] Make `provision-machine.sh` Docker-optional.
- [ ] Strip "Docker image" mention from CLAUDE.md Distribution section.
- [ ] Decide on container-sandbox vs `--multi-user` as the primary
      isolation story; collapse one into the other or carve out clearly
      separate use cases.
