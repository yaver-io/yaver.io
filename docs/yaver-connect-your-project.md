# Connect Your Project — surfacing any project's tools through Yaver

**Status:** design (2026-07-06). No code lands from this doc alone.

**One line:** generalize the pattern Talos already uses in production — bring
your own project, run it on a Yaver remote runtime, and drive its tools through
Yaver's multi-surface UI (mobile / web / glass / watch / voice / TUI) — into a
first-class, named onboarding flow.

This replaces the abandoned "Yaver publishes other people's apps to the stores"
ambition. It does **not** touch the core dev workflow (push / hot-reload /
remote coding / deploy) — that stays exactly as-is.

---

## Why this exists

The store-publishing idea was the wrong shape: publishing on behalf of a third
party means holding *their* Apple/Google identity (per-project vault, ASC JWT,
Play service account — see `desktop/agent/appstoreconnect.go`,
`playpublish_api.go`). That's an account-sharing ToS problem the moment it's
more than self-serve, and there's no compliant intermediary business in it.
Store tooling stays — reframed as *"manage YOUR OWN app's testers/builds"* — but
we stop pretending to be a publishing broker.

The replacement is stronger and already validated. **Talos already treats Yaver
exactly this way**, in production:

- `talos/web/src/lib/yaver-proxy.ts` — *"Talos is the user-facing surface …
  Yaver is hidden: it runs the coding runners on the remote Hetzner box."*
  Flow: `web/mobile chat → Talos /api/yaver/* → Yaver agent → runner → talos MCP`.
- `talos/.mcp.json` — Talos exposes itself as the `talos` MCP server
  (`talcli mcp serve`), which the Yaver-hosted runners call back into.
- `talos/.yaver/project.yaml` — Talos registers as a Yaver *project* so the
  Yaver runner drives Talos's own test suite from the Projects tab.
- Coupling is one-directional (Talos → Yaver). Yaver's Go module
  (`github.com/yaver-io/agent`) has **zero** compile-time dependency on Talos.
  The law holds: *Talos uses Yaver, never the reverse.*

"Connect Your Project" is that pattern, named and made repeatable for any repo —
not new plumbing.

---

## What already exists (the 80%)

Every mechanism this flow needs is already generic in the codebase. The work is
**packaging and naming**, not invention.

| Capability | Mechanism today | File |
|---|---|---|
| Register an external project's tools | External MCP client — merges `<server>__<tool>` into `tools/list`, forwards `tools/call`. "Nothing about the remote server is special-cased." | `desktop/agent/mcp_external.go:1` (`ExternalMCPServer{Name,URL,AuthToken,Enabled}`) |
| Same, for a peer agent | ACL peer tools — `acl_list_peer_tools` / `acl_call_peer_tool` | `desktop/agent/acl.go:171`, `talos_acl.go` |
| Clone any git repo onto a runtime | Framework-agnostic clone+prep on a mesh device; infers git remote from cwd | `desktop/agent/mcp_remote_dev_prepare.go:25`, `dev_env_clone.go:620` |
| Stand up / drive a remote box | SSH VPS fleet (`Setup`/`Provision`/`Exec`) + Hetzner/DO provisioning | `desktop/agent/remote.go:70,460,815` |
| Attach this machine to a remote device + repo | `code_attach` / `code_repo_set` — device-to-device over the mesh | `desktop/agent/code_control_plane.go:88,187` |
| Run one verb family in isolation | Capability-scoped guest tokens; per-verb `AllowGuest` | `desktop/agent/ops.go:202,326` |
| Drive tools conversationally | Chat / voice terminal / glass HUD / watch already call the agent's tool surface | mobile chat, `voice_dispatch.go`, watch |
| Frame a project's own web UI | Remote-runtime WebView / iframe host | `mobile/app/remote-runtime.tsx:240` |

The reason this works with no per-tool UI code: **the interface is the agent.**
When a project's tools are MCP-merged, every conversational surface Yaver already
has (chat, voice, glass, watch) can invoke them, because the model calls the
tools. That is exactly how a Talos shop-floor operator drives a hidden Yaver
runtime today.

---

## The flow

`yaver connect` (CLI) / "Connect a project" (web + mobile). Steps, each backed by
an existing primitive:

1. **Point at a repo.** Local cwd (infer git remote) or a URL. Reuses
   `remote_dev_prepare`'s inference (`mcp_remote_dev_prepare.go:37`).
2. **Pick a runtime.** Local machine / an existing mesh device / provision a box.
   Reuses `code_attach` device selection + `remote.go` provisioning. Honor the
   Hetzner scale-to-zero rule (snapshot+delete when idle — CLAUDE.md).
3. **Declare the project's tools.** One of:
   - the project runs `<yourcli> mcp serve` and we register it as an
     `ExternalMCPServer` (`mcp_external.go`) — the Talos path; or
   - the project registers as an ACL peer (`acl.go`); or
   - the project has no tool server → it still gets the generic surfaces
     (shell, dev-server if it's one of the 5 known frameworks, remote-desktop,
     WebView of its own UI).
4. **Surface it.** Immediately available on every conversational surface. A
   catalog tile (`mobile/src/lib/yaverNativeCatalog.ts`) makes it a first-class
   entry. Native panels come from the companion doc (schema-driven panels) once
   built.

### The project descriptor

A `yaver.project.yaml` in the project repo (Talos already ships `.yaver/project.yaml`)
declares the connection so `yaver connect` is one command, not a wizard:

```yaml
# yaver.project.yaml — lives in the connected project's repo
name: talos
runtime:
  target: primary          # local | primary | <deviceId> | provision:hetzner-cx32
  scale_to_zero: true      # snapshot+delete when idle (mandatory for cloud)
tools:
  mcp:
    command: talcli mcp serve   # stdio MCP server the runner calls back into
    # or: url: https://…/mcp    # remote JSON-RPC-over-HTTP MCP endpoint
surfaces:
  - chat                   # conversational — free, works today
  - voice
  - panels                 # native schema-driven panels (companion doc)
  - webview:               # optional: frame the project's own web UI
      url: http://127.0.0.1:3000
secrets:
  # names only — resolved from `yaver vault`, never stored in the repo
  - TALOS_SESSION_TOKEN
```

This is a superset of Talos's existing `.yaver/project.yaml`, so Talos migrates
by adding the `tools`/`surfaces` blocks it already implements informally.

---

## Boundaries (carry the Talos law forward)

- **Coupling stays one-directional.** Connected projects depend on Yaver; Yaver
  never imports them. The external-MCP/ACL-peer seam already enforces this —
  Yaver is a client, not a dependent.
- **Secrets by reference only.** `yaver.project.yaml` names vault keys; values
  live in `yaver vault` (`TALOS_SESSION_TOKEN` is injected, never in-repo).
  Convex privacy contract unchanged (`convex_privacy_test.go`).
- **Proprietary drivers stay in private overlays.** The OCPP boundary
  (`charge_controller.go:1` — "Real charge control is proprietary … MUST NEVER
  live in this open-source repo") is the template: Yaver ships the generic
  interface; the domain driver is registered from a private overlay that imports
  Yaver.
- **Do-no-harm + scale-to-zero** apply to every provisioned runtime (CLAUDE.md).

---

## What this is NOT

- Not a generic "MCP-tools-to-UI gateway." If positioned that way it competes
  with every MCP wrapper and has no moat. **The moat is the breadth of native
  surfaces already built** (glass/watch/spatial/car/remote-desktop/capture) +
  the subscription-auth remote runtime. Sell the surfaces, not the adapter.
- Not a change to the dev workflow. Push / hot-reload / remote coding / deploy
  are untouched.
- Not a store-publishing broker. Store tooling remains own-app self-serve only.

---

## Sequencing

1. **Now (packaging):** `yaver connect` + `yaver.project.yaml` over the existing
   `mcp_external` / `acl` / `remote_dev_prepare` / `code_attach` primitives.
   Ship the conversational surfaces (they already work). Dogfood by migrating
   Talos's informal wiring onto the descriptor.
2. **Next (differentiator):** native schema-driven panels — see
   [`yaver-schema-driven-panels.md`](./yaver-schema-driven-panels.md). This is
   what makes "Yaver's tons of UI" literally true for a connected project
   without hand-writing a `View.tsx` per tool.
