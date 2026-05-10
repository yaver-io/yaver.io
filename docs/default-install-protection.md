# Default Install Protection

This is the default balancing rule for Yaver installs:

- keep the Yaver agent host-native
- keep owner workflows usable by default
- isolate untrusted guest work where it matters
- never claim a runner is ready when the host cannot actually run it

Yaver does not normally need root to run.

- normal use is unprivileged: auth, serve, hot reload, feedback, owner coding flows, and most build/deploy operations
- root is only for machine-level setup: package install, service install, Docker/service-user wiring, Linux sysctls, and fresh server provisioning

## Why The Agent Stays On The Host

Yaver's main value comes from using the real machine you already own. That means
these paths must keep working against the host:

- hot reload and dev servers
- iOS and Android builds
- Hermes bundle generation
- Expo / EAS
- TestFlight and Google Play internal deploy
- relay and direct device connectivity
- access to Xcode, Android SDK, signing state, tmux sessions, GPU models, and host auth

Putting the whole control plane in a container would make those flows more fragile,
not safer.

## Default Runtime Policy

The intended default policy is:

- owner tasks run on the host
- hosted coding runners for owner tasks stay on the host
- guest and feedback-only tasks keep the normal guest restrictions
- guest tasks can be additionally containerized when Docker isolation is enabled

This is why Yaver does not blindly force `claude`, `codex`, `opencode`, or `aider`
through the generic task container for normal owner work. Those tools are usually
installed and authenticated on the host machine itself.

## Linux Safety Rules

On Linux, Yaver now treats runner readiness conservatively.

If Codex exists on `PATH` but the host is still missing the prerequisites for its
own sandbox, Yaver marks Codex as blocked instead of letting the user start a task
that will fail later with `bwrap` / user-namespace errors.

The main Linux prerequisites are:

- `bubblewrap`
- `uidmap`
- `kernel.unprivileged_userns_clone=1`
- `user.max_user_namespaces` set to a non-zero value
- if present, `kernel.apparmor_restrict_unprivileged_userns=0`

## Install Path Expectations

### `npm install -g yaver-cli`

The lean npm install is best-effort.

- if it has root privileges on Linux and a supported native package tool is available, it now installs
  `bubblewrap` and `uidmap` when missing
- if it has root privileges, it also writes the user-namespace sysctl config
- if it does not have enough privilege, it does not mutate the host aggressively
- instead, Yaver surfaces blocked runners as blocked in the UI

That makes the lean path safe for normal laptops while still improving fresh Linux
machines when the install actually has permission to harden them.

### Managed cloud / Hetzner / service-user installs

Provisioned Linux hosts need a stricter baseline because they are expected to stay
available for remote owner work plus guest work.

The bootstrap paths now ensure:

- Docker is installed and enabled where required
- Linux runner sandbox prerequisites are written via sysctl
- AppArmor user-namespace restriction is disabled when that kernel knob exists
- the `yaver` service user is added to the `docker` group in service-user setups

That keeps guest/container isolation viable without accidentally breaking owner-side
coding, hot reload, build, or deploy flows.

## How Yaver Protects A Remote Box

There are several layers, and they protect different things.

### 1. Default command sandbox

Yaver's command sandbox is enabled by default. It blocks known-destructive command
patterns before execution, including:

- recursive deletion of critical system paths
- deletion of common code/workspace roots such as `/Users`, `/home`, `$HOME`,
  `~/Workspace`, `~/Projects`, `~/Code`, and similar directories
- privilege escalation commands like `sudo`, `su`, and `doas` by default
- raw disk writes and partitioning tools
- piping downloaded content directly into a shell
- several host-compromise and exfiltration patterns

This protects the machine from broad destructive commands and the most obvious
"delete the whole workspace" failures.

### 2. Guest/container isolation

For untrusted work, Yaver can run guest or feedback-only tasks inside a Docker
container instead of on the host.

In that mode:

- the task sees the project directory mounted at `/workspace`
- only selected env vars are passed through
- the root filesystem can be read-only
- CPU and memory can be capped
- container mounts are validated so they cannot re-expose `/`, `/home`, `/Users`,
  `/etc`, or the Docker socket back into the task

This is the main hard boundary for shared machines and guest usage.

### 3. Honest runner readiness

On Linux, Yaver now marks runners like Codex as blocked if the host still lacks the
kernel/package prerequisites they need. That prevents "runner looked installed but
the task failed later" behavior on remote boxes.

## Important Boundary

Yaver intentionally does **not** prevent an owner-authorized host task from editing
or even deleting files inside the current project directory. Owner coding tasks need
real repo write access to be useful.

So the rule is:

- protect the machine and broad home/workspace roots by default
- isolate untrusted guest work with containers
- trust owner tasks with the current project, because that is the point of the tool

If you need stronger protection against accidental repo loss even for owner tasks,
use standard repo safeguards too:

- git commits and remotes
- snapshots or ZFS/Btrfs/VM snapshots
- dedicated worktrees or throwaway clones for risky tasks

## Hetzner Guidance

For a persistent Hetzner box, the safe and usable default is:

- keep the control plane host-native
- keep owner coding runners host-native
- use container isolation primarily for guest execution
- ensure the service user has the permissions needed for the container path

That is the operational model behind the shared-owner Hetzner runbook.

See also: [hetzner-shared-owner-runbook.md](/Users/kivanccakmak/Workspace/yaver.io/docs/hetzner-shared-owner-runbook.md:1)
