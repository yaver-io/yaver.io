# Yaver install-time user & privilege model

**Status:** steps 1–3 SHIPPED (2026-06-20); step 4 (privileged helper) deferred.
**Scope:** how the agent picks its OS user and bounds its privileges at install
time, for **both** self-hosted (own machine / own dedicated box) and
managed-cloud / operator deployments. One model, parameterized per surface.

## What shipped (2026-06-20)

Source of truth: **`desktop/agent/install_privilege.go`** (+ `_test.go`).

- **`NOPASSWD: ALL` is gone.** `yaverSudoersContent(profile)` emits a scoped
  allowlist — `profileSelfHost` (package mgmt + full `systemctl` on the owner's
  own box) and `profileOperator` (package mgmt + `yv-*` tenant lifecycle +
  `yaver*`/docker services only; **no** arbitrary `systemctl`, **no** `userdel`
  of a human account). Wired into `multiregion_orchestrate.go` (selfhost) and
  `cloud_deploy.go` (operator). Installed via `writeSudoersSnippet`, which
  `visudo -cf`-validates before activating so a bad file can't lock out sudo.
- **Unified `yaver` user creation** — `ensureYaverUserSnippet()` (`--system`,
  `/home/yaver`, `/bin/bash`) replaces the three drifted `useradd` call sites.
- **Hardened SYSTEM unit** — `hardenedSystemUnit()` +
  `installSystemdSystemService()`, exposed as **`sudo yaver serve
  --install-systemd-system`** (add `--operator` for a fleet node). Runs as
  `User=yaver` with `ProtectSystem=strict` + `ProtectHome=read-only` + a single
  `ReadWritePaths` hole at the agent's own home — so the agent process itself
  cannot touch `/etc`, `/usr`, or any other user's home.

Not yet done: **step 4**, the privilege-separated helper (agent runs
`NoNewPrivileges=true`, zero sudo). `NoNewPrivileges` is deliberately left
unset because the agent still escalates via sudo until the helper exists.

### Install surfaces & how the model wires in

| Surface | Command | User | Privilege |
|---|---|---|---|
| Personal machine | `npm i -g yaver-cli` → `yaver serve --install-systemd` | invoking user | no sudoers (acts as you) |
| Dedicated/own VPS | `sudo yaver serve --install-systemd-system` | `yaver` system user | scoped selfhost sudoers + hardened unit |
| Managed / operator | `sudo yaver serve --install-systemd-system --operator` (or cloud bootstrap) | `yaver` system user | scoped operator sudoers + hardened unit |

---

## Original analysis (retained)

> Per `CLAUDE.md`: code is the source of truth. Every file:line below was
> grepped on 2026-06-20. If you act on this doc later, re-grep first — other
> threads move constants.

## The requirement, stated honestly

We want the agent to be able to **install/remove software, manage its own
services, run ops** — but **not** run as root and **not** be able to
`rm -rf $HOME` (or any other user's home). Those two halves are in tension:
anything that can `apt install` or `useradd` is, in practice, root-equivalent
(apt runs maintainer scripts as root; arbitrary user creation is escalation).

So the model is layered, and we are explicit about what each layer actually
buys:

| Layer | Protects against | Does NOT protect against |
|---|---|---|
| Dedicated non-root user | casual footgun, "agent is root" | a determined exploit with sudo |
| **Scoped sudoers allowlist** | `sudo rm`, `sudo cat /etc/shadow`, stopping sshd | maintainer-script / `useradd` escalation |
| **systemd unit hardening** (`ProtectHome`, `ProtectSystem`) | a *buggy* agent process touching home | anything the agent does *via* sudo |
| **Container + per-tenant OS user** | one tenant reaching another | host-kernel 0-days |
| **Privileged helper (zero-sudo agent)** | the agent's whole MCP surface being trusted with root | bugs in the ~200-line helper itself |

The cheap 80% is scoped sudoers + unit hardening. The actual multi-tenant wall
is containers + separate OS users. Sudoers cleverness is **not** an isolation
boundary on its own — do not oversell it.

## What already exists (verified)

A lot of the skeleton is built. This redesign is mostly *tightening*, not
greenfield.

| Capability | State | Location |
|---|---|---|
| Linux **user**-mode systemd unit (runs as invoker, no `User=`) | EXISTS | `main.go:2032` (`WantedBy=default.target`) |
| macOS LaunchAgent (runs as current user) | EXISTS | `process_unix.go:~443` |
| macOS LaunchDaemon (sudo install, `UserName` = invoker, not root) | EXISTS | `process_unix.go:~639` |
| Dedicated `yaver` user — cloud | EXISTS | `cloud_deploy.go:1432` (`useradd --system`) |
| Dedicated `yaver` user — multiregion / launch | EXISTS but **inconsistent** | `multiregion_orchestrate.go:120`, `launch_cmd.go:340` (plain `useradd`, no `--system`) |
| Per-tenant `yv-<id>` users, created + `userdel -r` on revoke | EXISTS | `tenant_osuser.go`, `host_share_reaper.go` |
| `--operator` **refuses root** unless `--allow-root` | EXISTS | `main.go:3161-3162` |
| `warnIfRunningAsRoot()` (warn on desktop, allow root-only envs) | EXISTS | `no_root_check.go:41-92` |
| Guest/tenant env filtered to safe whitelist + vault overlay | EXISTS | `runner.go:~860`, `deploy_run.go:68` |
| Reaper: hard-kill sessions + wipe workspace on revoke | EXISTS | `host_share_reaper.go:12-188` |

### The two real holes

1. **`NOPASSWD: ALL`** — `multiregion_orchestrate.go:123` writes
   `yaver ALL=(ALL) NOPASSWD: ALL` to `/etc/sudoers.d/90-yaver`. This is
   root-equivalent: the `yaver` user *can* `sudo rm -rf /home/<anyone>`. The
   "can't delete home" property is therefore **not enforced** — it relies
   entirely on the agent's own restraint (`access_policy.go`), which is a
   policy, not a boundary. This is the literal hole behind the question.

2. **No systemd unit hardening at all** — `grep -rn 'ProtectHome|ProtectSystem|NoNewPrivileges|ReadWritePaths' desktop/agent/`
   returns **zero hits**. The kernel-enforced confinement that would make
   "can't touch home" true regardless of agent behavior is simply not set.

3. **Inconsistent `yaver` user creation** — three call sites, three flag sets
   (`--system` only in cloud_deploy). A daemon user should be `--system`,
   `nologin`, home under `/var/lib/yaver` — not a `/bin/bash` login user in
   `/home/yaver`. Unify on one helper.

## The model — one design, parameterized by surface

### Install-time user decision

Branch on **what kind of box this is**, decided once at install:

| Surface | OS user | Home | Shell | Service |
|---|---|---|---|---|
| Personal machine (your laptop) | **invoking user** | the user's real home | their shell | user systemd / LaunchAgent |
| Dedicated box / single-tenant VPS the user owns | **`yaver` system user** | `/var/lib/yaver` | `nologin` | system systemd unit |
| Managed cloud / operator | **`yaver` system user** (mandatory) | `/var/lib/yaver` | `nologin` | system unit, `--operator` |
| Per tenant (operator only) | **`yv-<id>`** | `/home/yv-<id>` | `/bin/bash` | spawned `sudo -n -u yv-<id>` |

- **Personal machine:** keep running as the invoking user. A dedicated user
  here is pure friction — the whole point (OBS/utility framing) is to act on
  *your* files, and you already accept full home access for your own account.
  Do **not** change this.
- **Dedicated/cloud box:** `yaver` **system** user, `nologin`, `/var/lib/yaver`.
  The daemon never needs an interactive login shell. Unify the three creation
  sites onto one `ensureYaverSystemUser()` helper with these flags.

### Scoped sudoers (replaces `NOPASSWD: ALL`)

This is the single highest-value change and it directly delivers the stated
goal — install yes, `rm $HOME` no:

```
# /etc/sudoers.d/90-yaver  — enumerated, NOT ALL
yaver ALL=(root) NOPASSWD: /usr/bin/apt-get install *, /usr/bin/apt-get remove *, /usr/bin/apt-get update
yaver ALL=(root) NOPASSWD: /usr/bin/systemctl start yaver-*,   /usr/bin/systemctl stop yaver-*,   /usr/bin/systemctl restart yaver-*
yaver ALL=(root) NOPASSWD: /usr/sbin/useradd --create-home --shell /bin/bash yv-*, /usr/sbin/userdel -r yv-*
```

What this enforces: `sudo rm`, `sudo cat /etc/shadow`, `sudo systemctl stop
sshd`, `userdel` of any non-`yv-*` user → **all fail closed**. What it does
**not** stop: apt maintainer-script escalation, arbitrary-package install.
Accept that limit; the container layer is what contains a real compromise.

Mirror the same allowlist in the agent's own sudo call sites so behavior is
consistent: `mcp_registries.go:531` (apt), `mcp_sysadmin.go:529-553`
(systemctl), `tenant_osuser.go:81-90` (`sudo -n` user mgmt).

### systemd unit hardening (free, kernel-enforced)

Add to the **system** unit (dedicated/cloud). These make "can't nuke home"
true even if the agent process is buggy or partially compromised — no agent
cooperation required:

```ini
[Service]
User=yaver
ProtectSystem=strict
ReadWritePaths=/var/lib/yaver /home    # only where it legitimately writes
ProtectHome=tmpfs                      # daemon unit: other homes invisible
PrivateTmp=true
ProtectKernelModules=true
RestrictSUIDSGID=true
# NoNewPrivileges=true   <-- ONLY once the helper below exists; it breaks sudo
```

**Conflict to know:** `NoNewPrivileges=true` disables sudo. So you cannot have
both "agent calls sudo" and "agent can't gain privileges" in one process.
That conflict is exactly what the helper resolves.

### (Bigger, optional) privilege-separated helper

The clean end-state: a minimal `yaver-helper` (root or `CAP_*`-scoped) exposes
a **fixed RPC surface** over a local socket — `InstallPackage(x)`,
`CreateTenant(y)`, `RestartService(z)` — validating every call against the
same allowlist. The agent then runs with `NoNewPrivileges=true`,
`ProtectHome`, and **zero sudo**, and asks the helper.

This converts the security question from "is the agent's entire 800-verb MCP
surface safe to trust with root?" into "are these ~5 helper verbs safe?" — a
vastly smaller, fully auditable surface. It's how polkit / packagekit /
systemd solve the same problem. Defer until after the sudoers + hardening
changes land; those two already close the literal hole in the question.

## Priority order

1. **Kill `NOPASSWD: ALL`** → scoped allowlist (`multiregion_orchestrate.go:122`).
   Closes the "can rm home via sudo" hole. Small, high-value.
2. **Harden the system systemd unit** (`ProtectHome`/`ProtectSystem=strict`/
   `ReadWritePaths`). Kernel-enforced, no agent cooperation.
3. **Unify `yaver` user creation** on one `--system` + `nologin` +
   `/var/lib/yaver` helper; branch personal-vs-dedicated at install.
4. **(Later)** privilege-separated `yaver-helper`; flip agent to
   `NoNewPrivileges=true`, zero sudo.

## Cross-refs

- Operator-fleet gap C (no teardown / bare host PTY): partially closed by the
  reaper; the helper in step 4 is the durable fix.
- `access_policy.go` (Policy Guard) is the *behavioral* layer — it should stay,
  but it is not a substitute for the *enforced* layers above.
- Network jail (relay-only + RFC1918 block) is the egress half of the same
  isolation story; out of scope here.
