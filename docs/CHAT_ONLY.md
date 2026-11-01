# Chat Only

This document is the recoverability ledger for the `chat-only` branch: a refactor that reduces Yaver's mobile client to **two destinations — Chat and Machines — with no public relay**, on the working checkout as of **2026-11-01**. Every phase below records what was removed and the exact route back, so nothing here is a one-way door and no decision depends on the author's memory.

## Backend: which one the chat talks to

**Keep Yaver's own Go agent as the chat backend.** Do not adopt `@getpaseo/client` as the transport.

1. **`@getpaseo/client@0.14.0` is Node-only.** Its `dist/**/*.js` references `react-native` exactly zero times, and only `dist/node/index.js` reaches `node:*` / `ws` — it cannot run inside the RN app without a shim layer that is pure new surface area.
2. **The agent already speaks opencode.** `desktop/agent/opencode_stream.go`, `opencode_config.go`, `opencode_catalog.go` carry the runner, config and catalog; `mobile/app/(tabs)/tasks.tsx` already renders that live console off the raw stdout lane (`?rawSince=` + `onRaw` SSE), so the client half is built.
3. **The provider is already wired.** `desktop/agent/accounts.go` has `ProviderDeepInfra`, which is the DeepSeek endpoint we target — no new credential path.
4. **Paseo's relay is off in the working setup anyway.** `~/.paseo/config.json` sets `daemon.relay.enabled=false`, i.e. that setup is already a direct address, which is exactly this branch's model.
5. **No `@getpaseo/client` dependency is added** to `mobile/package.json` (or anywhere else) by any phase of this work.

## What stays

- Yaver's Go agent and its opencode runner (`desktop/agent/opencode_*.go`) — the chat backend.
- The DeepInfra / DeepSeek provider (`ProviderDeepInfra` in `desktop/agent/accounts.go`).
- The live opencode console in `mobile/app/(tabs)/tasks.tsx`.
- Machines: `mobile/app/(tabs)/devices.tsx` + `mobile/src/context/DeviceContext.tsx`.
- **The whole `relay/` source tree in git** — public relay stays compiled and revertible, it is just no longer a default transport.
- Convex, minus everything that is not OAuth sign-in.
- Direct connectivity: Tailscale address or a Cloudflare tunnel.

## What goes

- The relay as a **default/automatic** transport in the mobile client (Phase 1).
- Every nav destination other than Chat and Machines (Phase 2).
- `claude` and `codex` runner plumbing on the agent (Phase 3).
- Convex functions/code unrelated to OAuth sign-in (Phase 4).
- Web dashboard and CLI destinations that are not the same two (Phase 5).

## Per-phase removal ledger

Restore column convention: the commit sha is not known when this row is written. Find it, then revert it —

```
git log --oneline chat-only -- <path>
git revert <sha>
```

`main` is untouched by every phase; all work is on `chat-only` only.

| Phase | What was removed | How to bring it back |
| --- | --- | --- |
| Phase 1 — relay off by default | Public/free relay is no longer a default transport in the mobile client (direct Tailscale / Cloudflare only). **`relay/` source is KEPT in git** — only the client's default selection changed. | `git log --oneline chat-only -- mobile/src mobile/app` then `git revert <sha>`. Restores the client-side default; `relay/` was never deleted. `main` untouched. |
| Phase 2 — Chat and Machines only | Every nav destination except Chat and Machines is hidden; the opening screen is connect-a-PC (address over Tailscale + password, or QR). Hidden entries are flipped in `mobile/app/(tabs)/_layout.tsx` (the `href: null` route set, plus the visible tab set), and screens under `mobile/app/` other than the two kept destinations. | `git log --oneline chat-only -- "mobile/app/(tabs)/_layout.tsx"` then `git revert <sha>`; for deleted screens `git log --oneline chat-only -- mobile/app` then `git revert <sha>`. `main` untouched. |
| Phase 3 — agent side: opencode + DeepInfra/DeepSeek only | `claude` and `codex` runner plumbing on the agent (`desktop/agent/*claude*`, `*codex*` runner/config paths, and their registration). | `git log --oneline chat-only -- desktop/agent` then `git revert <sha>` of the phase's commit; restores the deleted files/dirs. `main` untouched. |
| Phase 4 — Convex reduced to sign-in | Convex code unrelated to OAuth sign-in. **Code only — no live data touched, no Convex deploy.** | `git log --oneline chat-only -- convex` then `git revert <sha>`. Reverts code; no data was ever mutated. `main` untouched. |
| Phase 5 — web dashboard and CLI parity | Web dashboard and CLI destinations not equal to Chat and Machines, and non-opencode runner options on those surfaces. | `git log --oneline chat-only -- web cli` then `git revert <sha>` (per-surface commits revert independently). `main` untouched. |
| Phase 6 — proof | Nothing is deleted. This phase adds the verification run: type checks, Yaver's tests, one simulator launch with a screenshot. | Nothing to revert for removals; if the recorded proof needs discarding, `git log --oneline chat-only -- <proof path>` then `git revert <sha>`. `main` untouched. |

## Safety

- Work happens **only on `chat-only`**. `main` is never merged into, never rebased onto, never force-pushed.
- No force push, no deploy, no store upload.
- **No live Convex data is touched** — Phase 4 is code-only and does not deploy Convex.
- Each phase is one revertible commit so the ledger above stays true.
