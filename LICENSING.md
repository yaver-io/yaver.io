# Yaver Licensing

Yaver uses a **split-license model**.

| Component | License |
|-----------|---------|
| **Core** (agent, relay, backend, web, mobile app, desktop app/installer, pi-image) | **FSL-1.1-Apache-2.0** — Functional Source License. Free for any non-competing use; each release auto-transitions to Apache-2.0 two years after it is published. |
| **Client SDKs & CLIs** (`cli/`, `sdk/js`, `sdk/feedback/*`, `sdk/flutter`, `sdk/python`, `sdk/go/*`, `sdk/errors-js`) | **Apache-2.0** — no time limit, permissive, embed in closed-source apps freely. |

The repo root `LICENSE` is the FSL text. Every Apache-2.0 package
ships its own `LICENSE` at its package root.

## The Competing Use clause (core only)

The FSL allows any use **except** hosting a commercial product or
service that substitutes for Yaver or offers substantially similar
functionality. Everything else — self-hosting, modifying, internal
business use, consulting, research, building your own app on top
— is explicitly permitted.

After the Change Date (2 years per release), that release
automatically becomes Apache-2.0 with no restrictions.

## What this means in practice

- **Using `yaver-cli` to build your own app** → Permitted. Your app
  is yours, any license, any distribution.
- **Embedding the Feedback SDK / BlackBox / push client** → Apache-2.0,
  no restrictions ever.
- **Self-hosting Yaver unmodified for your team or company** →
  Permitted, including commercially.
- **Modifying Yaver for internal use** → Permitted.
- **Hosting a paid SaaS that competes with Yaver Cloud** → Not
  permitted under FSL for 2 years per release. After that, the old
  version is Apache-2.0 and is fair game.

## Rule of thumb for new components

*Does a user's application import, bundle, or invoke this code?*

- **Yes** → Apache-2.0.
- **No, it's a service users talk to over the network** →
  FSL-1.1-Apache-2.0.

## Commercial license

If you need the core without the Competing Use restriction (e.g.,
to bundle a modified agent into a closed-source hosted product),
a commercial license is available. Contact
[kivanc.cakmak@simkab.com](mailto:kivanc.cakmak@simkab.com).

## Contributions

See [`CONTRIBUTING.md`](./CONTRIBUTING.md). Contributions are
licensed under the file's own license and SIMKAB ELEKTRIK retains
the right to relicense, including under commercial terms. DCO
sign-off required (`git commit -s`).

## Disclaimer

Not legal advice. If your organization has specific licensing
constraints, consult an attorney.
