# Yaver Anywhere Runtime — Handoff Package

Last updated: 2026-06-17

This folder is the **complete handoff** for the "Yaver Anywhere" program: the strategy,
the verified current state, the full software architecture, the rollout/CapEx plan, the
edge-fleet (colocation / donation / phone-datacenter) model with its trust story, the
monetization/licensing posture, and the ready-to-execute coding tickets for Codex.

> Authored from a working session on 2026-06-17. Every code claim was ground-truthed
> against the repo (not copied from older docs). Per `CLAUDE.md`: **code is the source
> of truth — re-grep before acting; fix the doc in the same change if it drifts.**
> Nothing in this program has been committed; all referenced new code (e.g.
> `ops_remote_session.go`, `RemoteSessionView.tsx`) is in the working tree.

---

## What this is (one paragraph)

Yaver is **not** a remote-desktop tool, a cloud browser, or a device farm — each of
those is a single modality with a focused OSS competitor we won't out-feature. Yaver is
the **identity-bound control plane that turns any compute you own or rent — mac, Linux,
Windows, phone, Pi, Hetzner, redroid, Apple TV, robot — into a streamable,
agent-drivable, human-gated runtime, with a control plane that never holds your data.**
The runtime *fabric* is ~80% built; five finishing gaps stand between it and a sellable
product. The go-to-market is bootstrapped (no VC): sell the control plane, not the
compute, and grow free-tier supply via **colocation and donation of secondhand phones**,
where isolation is solved by physics (1 phone = 1 user).

---

## Reading order

| # | Doc | Read it for |
| --- | --- | --- |
| 1 | [strategy-and-reality.md](strategy-and-reality.md) | the thesis, the moat, the honest realism verdict, the five gaps |
| 2 | [ARCHITECTURE.md](ARCHITECTURE.md) | **the full software architecture** — components, abstractions, data model, flows, security |
| 3 | [build-handoff-ws-a-g.md](build-handoff-ws-a-g.md) | the core build spec — workstreams A–G with verified anchors |
| 4 | [edge-fleet-colo-donate.md](edge-fleet-colo-donate.md) | colo / donation / phone-datacenter, trust & data sovereignty, consumer onboarding (WS H/I/J) |
| 5 | [rollout-and-capex.md](rollout-and-capex.md) | phased rollout, your 3×4 GB boxes, CapEx, economics |
| 6 | [coding-tickets.md](coding-tickets.md) | **what to hand Codex now** — Ticket 1 (TURN) + Ticket 2 (onboarding) |
| 7 | [DECISIONS.md](DECISIONS.md) | every decision made + rationale + open questions + how we got here |
| 8 | [real-device-testing.md](real-device-testing.md) | physical Android, off-network TURN, reset wizard, and redroid evidence plan |
| 9 | [hermes-agent-gap-analysis.md](hermes-agent-gap-analysis.md) | official Hermes Agent comparison and Yaver-specific follow-up tickets |

> **Monetization & licensing** have no separate file: the no-VC monetization model lives
> in [strategy-and-reality.md](strategy-and-reality.md) §6, and ARCHITECTURE.md §16
> summarizes the FSL/Apache licensing boundaries per component.

---

## Status at a glance (verified 2026-06-17)

Maturity: **prod** = works end-to-end · **alpha** = wired, unhardened · **partial** =
real but OS/path-limited · **stub** = exists but gated/unpublished · **absent**.

| Capability | State | Note |
| --- | --- | --- |
| WebRTC stream sources (screen/capture/scene) | prod | pion v4, H.264, fan-out, TURN ok |
| WebRTC audio | partial | Opus, **Linux only** |
| Remote control `/rd/input` + policy | prod | mac/linux/windows; consent + audit |
| **TURN on interactive session path** | alpha | `remote_runtime_webrtc.go` now uses `iceServersForPeer()`; scoped tests pass; off-network hardware proof still pending |
| Remote Session MVP | alpha | `ops_remote_session.go` + `RemoteSessionView.tsx`, uncommitted, no tests |
| Managed cloud (Hetzner) | partial | real, gated behind owner-email env |
| Auto-down / idle shutdown | absent | **WS-B** (snapshot+delete, not power-off) |
| Doorman / wake | absent | **WS-F**, Convex httpAction |
| Published cloud image | stub | `cloud-images.json` all null → **WS-C** |
| Metering live | stub | `dryRun` until `YAVER_MANAGED_METER_LIVE` → **WS-D** |
| On-Android node | prod | `SandboxService.kt:99` runs `libyaver.so serve` |
| Android home-host toggle | alpha | starts single-owner `serve --relay-only`; battery/charging status; real relay proof pending |
| Android prepare-for-colo wizard | alpha | reset checklist + Android reset settings deep-link; physical walkthrough pending |
| Phone multi-tenant isolation | absent | impossible w/o root → **colo = 1 phone/user** |
| Licensing split (FSL core + Apache SDK) | prod | implemented; README badge fixed AGPL→FSL |

---

## Workstreams A–J (master list)

| WS | Goal | Doc |
| --- | --- | --- |
| **A** | TURN on interactive WebRTC path | build-handoff §3 / coding-tickets Ticket 1 |
| **B** | Auto-down: idle → snapshot+delete → wake | build-handoff §4 |
| **C** | Publish one Hetzner image | build-handoff §5 |
| **D** | Metering live behind the wallet | build-handoff §6 |
| **E** | Commit + harden Remote Session MVP | build-handoff §7 |
| **F** | Doorman (wake a downed box) | build-handoff §12 |
| **G** | Free own-hardware tier (isolation-gated) | build-handoff §13 |
| **H** | Donor/Colo onboarding + consumer wizard | edge-fleet §8, §12 |
| **I** | Phone-node hardening (battery/thermal/kiosk/FDE) | edge-fleet §8.3, §11.6, §12 |
| **J** | Multi-tenant resource isolation (PC pool) | edge-fleet §7.2 |

Sequence: **A → C → E → B → F**, then **G/H/I** (gated on the isolation gate), **D** last.
See ARCHITECTURE.md §17 for the dependency DAG.

---

## The five gaps (the whole game)

1. **TURN on the interactive path** — wired in code; off-network phone proof still pending.
2. **Auto-down** — snapshot+delete (Hetzner bills stopped boxes); the margin protector.
3. **Publish one image** — instant provision.
4. **Metering live** — flip flags + wallet.
5. **README badge** — done (AGPL → FSL).

Four of five are *finishing*, not greenfield.

## Fresh Competitive Scan

The Hermes Agent docs were reviewed on 2026-06-17. The useful gaps for Yaver are
not "copy Hermes"; they are product primitives Yaver should add around its
managed-cloud/runtime fabric: assistant profiles, scheduled jobs, memory-write
approval, toolset filtering, chat pairing, hosted MCP OAuth, and a clearer
redroid QA product surface. See
[hermes-agent-gap-analysis.md](hermes-agent-gap-analysis.md).

---

## Hard rules carried into every ticket

- **No commit/push without explicit user permission.**
- **Convex privacy contract** (`convex_privacy_test.go`): no secrets/tokens/paths/stream
  data in the control plane.
- **Do no harm / network jail**: relay-only + RFC1918 egress block on any shared or
  donated node; never a botnet/proxyware.
- **Trust by crypto, not faith**: colocated/donated phones are wiped clean appliances;
  secrets sealed in the TEE; home-hosting is the zero-trust default.
