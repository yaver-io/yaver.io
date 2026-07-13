# Security Policy

## Reporting a vulnerability

Email `kivanc.cakmak@simkab.com` (formerly `security@yaver.io`, same inbox).

- **Acknowledgement:** within 48 hours.
- **Disclosure window:** 90 days from acknowledged report to public disclosure, unless you and we agree on a different timeline.
- **Safe harbour:** researchers acting in good faith under responsible disclosure will not have legal action pursued against them.

Do **not** open a public GitHub issue for a security vulnerability.

## Scope

In-scope:
- The Yaver agent (`desktop/agent/`), relay server (`relay/`), backend (`backend/`), mobile app (`mobile/`), desktop app (`desktop/app/`), desktop installer (`desktop/installer/`), and web app (`web/`).
- Any of the open-source SDKs under `cli/` and `sdk/*`.
- The Raspberry Pi dev-node image (`pi-image/`).
- `yaver.io` and `public.yaver.io` (the relay) as operated by us.

Out of scope:
- Self-hosted deployments operated by third parties.
- Denial-of-service attacks that do not demonstrate a novel vulnerability.
- Social engineering of contributors.
- Third-party services we integrate with (Convex, Cloudflare, Apple / Google / Microsoft OAuth, etc.) — please report those to their respective vendors.

## Supported versions

Security fixes are applied to:
- The latest release of each component (CLI, web, mobile, relay, backend, installer, pi-image).
- The `main` branch of this repository.

Older versions do not receive backported fixes. If you self-host, upgrade.

## How production is protected

This is a public open-source repo. The following controls make sure no external contributor (and no accidentally-merged PR) can trigger a production deploy or publish a package using our secrets:

1. **Deploy jobs are gated by the `Production` GitHub Environment** with a required reviewer (@kivanccakmak). Every production job waits for explicit human approval before running with secrets.
2. **The Production environment is restricted to `main` + tag patterns** (`cli/v*`, `mobile/v*`, `web/v*`, `installer/v*`, `relay/v*`, `pi-image/v*`, `piImage/v*`). Deploys cannot be triggered from branches or PRs.
3. **Release workflows only trigger on `push: tags:`**. Fork PRs cannot push tags to the base repo.
4. **Release tags are protected by a repository ruleset** — only admins can create, update, or delete them.
5. **`main` branch is protected** — no force-push, no deletion, linear history, required signatures (with admin bypass for the repo owner).
6. **CODEOWNERS** forces explicit owner review on PRs touching `.github/`, `/scripts/`, auth code, vault code, and licensing files.
7. **Fork PR workflows run without secrets** — GitHub blocks secret passthrough for `pull_request` events from forks by default, and we do not use `pull_request_target` anywhere.
8. **Workflow permissions** default to `contents: read` at the workflow level; jobs escalate only where needed (e.g., release jobs that need `contents: write` to create releases).

If you find a gap in these controls, please report it through the email above — defence-in-depth issues are in scope even without a demonstrable exfiltration path.

## Publishing / package integrity

Every published artefact comes from the CI pipeline on a pushed tag:

- **npm** (`yaver-cli`, `yaver-sdk`, `yaver-feedback-*`, `yaver-errors`) — published from `.github/workflows/release-cli.yml` / `release-sdk.yml` after environment approval.
- **PyPI** (`yaver`) — same, via `release-sdk.yml`.
- **pub.dev** (`yaver`, `yaver_feedback`) — currently manual from a clean workstation.
- **Homebrew / apt / AUR / Scoop / Winget / Chocolatey** — published from `release-cli.yml` to sibling repos; each manifest includes the SHA-256 of the release artefact. Verify the artefact hash against `checksums.txt` on the corresponding GitHub Release.
- **Docker** (`ghcr.io/kivanccakmak/yaver.io/cli`, `docker.io/kivanccakmak/yaver-cli`) — multi-arch images built + pushed from `release-cli.yml`.

If a published artefact's SHA-256 does not match what's on the GitHub Release, stop and email us immediately.

## GPG / SSH verification

Signed commits on `main` are preferred. All commits from the repo owner are signed; if you see an unsigned commit on `main` with the owner's name on it, that's a signal to investigate.
