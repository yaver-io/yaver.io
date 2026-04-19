# Yaver Licensing

Yaver uses a **split-license model**. Which license applies depends on the
role of the component, not its directory depth or programming language.

The practical summary:

- **If Yaver code runs as a server, agent, control plane, relay, or hosted
  dashboard → [AGPL-3.0-only](https://spdx.org/licenses/AGPL-3.0-only.html).**
  This prevents hosted clones — a competitor who modifies the core and runs
  it as a network service must publish their modifications.
- **If Yaver code is imported, bundled, or invoked by your application →
  [Apache-2.0](https://spdx.org/licenses/Apache-2.0.html).** You can embed
  the client SDKs and CLIs in closed-source commercial applications. Your
  application does not become AGPL because you use Yaver.

The repository root `LICENSE` is AGPL-3.0-only — it is the *default* license
for anything in the tree that does not have a more specific `LICENSE` file.
Every Apache-2.0 component ships its own `LICENSE` file at its package
root so tooling (npm, PyPI, GitHub, FOSSA, REUSE, etc.) resolves it
correctly.

## Server & infrastructure components — AGPL-3.0-only

These are the components a competitor would need to run a hosted Yaver
clone. They are licensed under AGPL-3.0-only.

| Path | What it is |
|------|------------|
| `desktop/agent/` | The Go agent (`yaver serve`, QUIC server, MCP server side, tmux manager, task runner) |
| `relay/` | QUIC relay server (self-hostable NAT traversal) |
| `backend/` | Convex backend (auth + peer discovery + platform config) |
| `web/` | yaver.io web app (landing + dashboards + MCP console) |
| `mobile/` | Yaver mobile app (Yaver's own end-user control surface) |
| `desktop/app/` | Electron desktop app (Yaver's own end-user control surface) |
| `desktop/installer/` | Electron installer (Yaver's own end-user install surface) |
| `pi-image/` | Raspberry Pi image infrastructure |

If you modify any of these and run the modified code as a network service,
you must release your modifications under AGPL-3.0-only. Running any of
them unmodified, even commercially, imposes no such obligation.

## Client libraries, SDKs, and CLIs — Apache-2.0

These are the components your own application imports, bundles, or
invokes. They are licensed under Apache-2.0 so you can embed them in
closed-source commercial apps with no copyleft contamination.

| Path | What it is | Package registry |
|------|------------|------------------|
| `cli/` | `yaver-cli` push-to-device CLI (npm) | `yaver-cli` on npm |
| `sdk/js/` | `yaver-sdk` programmatic JS/TS SDK | `yaver-sdk` on npm |
| `sdk/feedback/react-native/` | React Native Feedback SDK — `<FloatingButton />`, `YaverFeedback`, `BlackBox` client | `yaver-feedback-react-native` on npm |
| `sdk/feedback/web/` | Web Feedback SDK | `@yaver/feedback-web` on npm |
| `sdk/feedback/flutter/` | Flutter Feedback SDK | `yaver_feedback` on pub.dev |
| `sdk/flutter/` | Flutter programmatic SDK | `yaver` on pub.dev |
| `sdk/python/` | Python programmatic SDK | `yaver` on PyPI |
| `sdk/go/yaver/` | Go programmatic SDK | imported via `go get` |
| `sdk/go/clib/` | C shared library (`libyaver.so` / `.dylib`) | built per-platform |
| `sdk/errors-js/` | `yaver-errors` error-reporting client | `yaver-errors` on npm |

## Rule of thumb for future components

When you add a new component, ask:

> *"Does a user's application import, bundle, or link to this code?"*

- **Yes** — it lives inside the user's process or binary → **Apache-2.0**.
- **No** — it runs as a separate service the user's app talks to over the
  network → **AGPL-3.0-only**.

## What this means in practice

- **Using Yaver to build your app** (running `yaver-cli`, pushing bundles,
  scaffolding with a template): your app is yours. Any license. Closed-source
  commercial is fine. No AGPL obligations.
- **Embedding a Yaver SDK in your app** (FloatingButton, BlackBox, push
  client, programmatic SDKs): Apache-2.0. Your app stays under whatever
  license you choose. No AGPL obligations.
- **Forking Yaver and running a competing hosted service**: you must
  release your modifications to the AGPL-licensed components under
  AGPL-3.0-only.
- **Self-hosting Yaver unmodified for your own team**: no obligations to
  release anything.

## Commercial license

A commercial license is available for organizations that want to use the
AGPL-licensed components without AGPL obligations — for example, bundling
a modified agent into a closed-source product. Contact
[kivanc.cakmak@simkab.com](mailto:kivanc.cakmak@simkab.com).

## Contributions

By submitting a pull request, you agree to the terms in
[`CONTRIBUTING.md`](./CONTRIBUTING.md): your contribution is licensed
under the same license as the file or package you are modifying, and you
grant SIMKAB ELEKTRIK a perpetual, worldwide, royalty-free license to
relicense your contribution as part of the Yaver dual-licensing /
commercial-licensing program. We use the Developer Certificate of Origin
(DCO) — every commit must be `Signed-off-by:` (`git commit -s`).

## Disclaimer

This document is not legal advice. If your organization has specific
licensing constraints, consult an open-source attorney. The split-license
model used here is standard practice (MongoDB, Elastic, Grafana, Sentry,
Plausible all use variants) but the exact obligations depend on your use
case.
