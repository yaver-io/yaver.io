# Yaver.io — Agents Guide

This file is for AI coding agents (OpenAI Codex, Aider, Amp, Goose, Claude Code, OpenCode, …) that look for an `AGENTS.md` convention. The detailed project guide lives in [`CLAUDE.md`](CLAUDE.md) — read that first. This file only calls out the rules that every agent needs to follow regardless of which tool is driving.

## Golden rule: .md files go stale, code is the source of truth

Every Markdown file in this repo — including this one, [`CLAUDE.md`](CLAUDE.md), [`docs/architecture/AI_ARCH.md`](docs/architecture/AI_ARCH.md), [`docs/architecture/REMOTE_WORKER.md`](docs/architecture/REMOTE_WORKER.md), `init.md`, and every `*.md` under `docs/` — was accurate on the day it was written. Drift is the norm, not the exception. Routes get renamed, handlers get refactored, fields get added, version numbers roll forward. The docs don't always keep up.

**Before you act on any claim a `.md` file makes:**

1. **Grep the code.** If a doc says the agent has `POST /foo/bar`, run `grep -n 'HandleFunc.*"/foo/bar"' desktop/agent/*.go`. We shipped CLI 1.99.33 with `yaver diagnose` handlers compiled in but the `mux.HandleFunc` line missing — `/diagnose` returned 404 in production despite the doc saying the endpoint existed, and the bug only got caught because a smoke test hit the real route.
2. **Re-read the file on disk, not from memory.** If a doc says a function signature is `foo(a, b int) error`, open the file — it may be `foo(a int, b string) (Result, error)` now.
3. **Check versions.** `yaver --version` (binary on PATH) vs `/info.version` (running process) vs `git log --oneline -- <file>` (HEAD). Disagreement means the doc describes a different slice of time than the one you're operating on.
4. **When the doc and the code disagree, the code wins, and fix the doc as part of your change.** Don't just code around a stale doc — update it.

Treat `.md` files the way you'd treat a commit message from six months ago: useful context, never the authoritative answer.

## What to read before making changes

- Full project guide → [`CLAUDE.md`](CLAUDE.md)
- Runtime architecture (auth / bootstrap / relay / recovery) → [`docs/architecture/AI_ARCH.md`](docs/architecture/AI_ARCH.md)
- Slave-machine / remote-build flows → [`docs/architecture/REMOTE_WORKER.md`](docs/architecture/REMOTE_WORKER.md)
- Per-project cached context → `init.md` at the project root (best-effort; may be out of date)
- For local iOS/TestFlight deploys on this Mac, also read the "iOS TestFlight deploy gotchas" and "iOS — TestFlight" sections in [`CLAUDE.md`](CLAUDE.md) before assuming the vault path is working.

After reading the docs, **grep the code for the symbols the docs name** before relying on them.

## Local Deploy Memory

- On this Mac, local TestFlight deploys can work even when `yaver vault env --project mobile` is unauthenticated, because the deploy guide in [`CLAUDE.md`](CLAUDE.md) already documents the fallback `APP_STORE_KEY_*` / `APPLE_TEAM_ID` exports used by the working local path.
- If `scripts/deploy-testflight.sh` appears stuck with almost no output, check for another active `xcodebuild archive` from another local mobile project or an earlier Yaver run before assuming credentials are broken.
- If you must clean local archive artifacts, inspect the exact path first (`ls -la /tmp/YaverBuild /tmp/Yaver.xcarchive /tmp/YaverExport`) and only then remove those specific directories.
- **Headless codesign (SSH / no GUI):** `CodeSign … errSecInternalComponent` means the signing **private key** is in a **locked** keychain, not that the cert is missing. The identity spans TWO keychains — `yaver-ci.keychain-db` (Apple Distribution) + `login.keychain-db` (Apple Development private keys) — so BOTH must be `unlock-keychain` + `set-key-partition-list`'d before archiving, or the archive dies at `CodeSign …/*.appex`. Full recipe in [`CLAUDE.md`](CLAUDE.md) → "Headless codesign". `launchctl asuser` does not help; only the passwords do.
- **Local privileged secrets** (signing-keychain / login / sudo passwords) live in `~/.yaver/local-secrets.env` — `chmod 600`, owner-only, **never committed, never synced to any cloud/GH secret** (a macOS login/sudo password in GH secrets widens the attack surface). The yaver agent reads these to unlock keychains / run sudo headlessly. Canonical home is the encrypted `yaver vault`; the env file is the fallback. See [`CLAUDE.md`](CLAUDE.md) → "Local privileged credential store". Never echo these values into logs, commits, or docs.

## Hard safety rules (summarised from CLAUDE.md)

- **Never push or commit without explicit user permission.**
- **Never run `rm -rf` on a computed path without `ls -la` first** — case-insensitive macOS filesystems already cost us a full repo once.
- **Only touch Yaver project resources from this repo.** Do not delete, revoke, stop, snapshot, migrate, or mutate personal machines, private sibling-project resources, generic `ubuntu-*` boxes, storage volumes, or non-Yaver provider state unless the user explicitly identifies that exact resource as part of the Yaver task. Before destructive provider/Convex cleanup, list candidates and verify Yaver-specific labels, names, IDs, subscription links, or `cloudMachines` rows; ask on ambiguity.
- **Never use WebView to load third-party React Native apps** — use the Hermes bundle push path (`/dev/build-native`).
- **Never commit credentials, customer IPs, relay hostnames, or any secret** — the repo is public on GitHub.
- **Never deploy mobile / publish npm / push a tag without confirming with the user first.**
- **Yaver is not single-user. Never hardcode a path, username, or home directory** — and never let the daemon's CWD stand in for a missing one. A remote box can be any OS, any user, any layout. Resolve at runtime (`os.UserHomeDir()`, `filepath.Abs`, explicit config); a literal `/Users/<name>` or `/home/<name>` outside of a deliberately-fixed system path (`/home/linuxbrew`, a container tenant root) is a bug. On 2026-07-20 `workDir` defaulted to `"."`, which was the agent's CWD — the user's HOME — so every `POST /tasks` recursively classified the entire home tree and never returned. The phone reported the machine unreachable while it was idle and healthy. See `desktop/agent/task_placement_scan_bounds_test.go`.
- **Never put advisory work in the critical path of the operation it annotates.** Placement labels, project classification, telemetry and metrics must be bounded by wall-clock and must degrade to empty, never block. Depth limits are not bounds — breadth defeats them; only a deadline bounds wall-clock.

## Every incident must leave the product harder than it found it

Fixing the symptom is half the job. Whenever you debug a real failure — yours, a user's, or a past session's — ask **"what would have told me this in ten seconds instead of an hour?"** and build that answer into Yaver before you call the task done. This is the deliverable, not cleanup.

1. **Encode the diagnosis where the agent already looks** — a `doctor` probe (`desktop/agent/doctor_*.go`), an ops verb, a deploy preflight. If a check existed and was GREEN during the incident, it's a *false green*: fix that check, don't add a second one next to it.
2. **Probe the real capability, never the proxy.** The recurring bug class here is "the inventory says yes, the operation says no" — a certificate that is present but cannot sign, a tool on PATH that is a stub, a device marked `online` that is unreachable, a deploy key that resolves to a deleted file. If the only way to know is to attempt the operation, attempt it.
3. **Put the *why* in the error text.** Name the specific fix; "check your configuration" is worthless. Vague errors cost whole sessions — `errSecInternalComponent` (2026-07-19) reads as "keychain locked, need the login password", which was wrong, and the wrong reading burned a session before anyone tested it.
4. **Ship it to every surface** — Go agent, MCP verb, CLI, web, mobile. A diagnosis only the CLI can see does not exist for a user on their phone. See "Cross-surface parity" in [`CLAUDE.md`](CLAUDE.md).
5. **Self-heal only when the repair is unambiguous and idempotent.** Unlocking a keychain the operator explicitly configured: yes. Guessing passwords or mutating Apple account state: no.
6. **Write the postmortem into the code.** `desktop/agent/doctor_build_deep.go` and `doctor_build_signing.go` are the model — every bullet in their file-top comments is a real incident, stated as the false green it produced.

Every other rule, convention, and subsystem detail is in [`CLAUDE.md`](CLAUDE.md). When it disagrees with the code you're looking at, the code wins.
