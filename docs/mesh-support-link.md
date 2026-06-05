# Yaver Support Link — deep analysis & audit

**Goal.** Share one link to a friend. They open it → Yaver installs → they're
onboarded (no AI agent / no dev knowledge needed) → their machine joins **my**
mesh. Then **I** support them from my side using **my Claude Code + my yaver
CLI** — `ssh` / `exec` / `code --attach` / screen+input — over the mesh. A
RustDesk + Tailscale + SSH replacement, AI-powered from the supporter's side.
Cross-platform: Linux, macOS, Windows-via-WSL2.

This is an audit of what exists, what to reuse, the UX, the cross-platform
matrix, the "drive it from my Claude Code" path, and — most important — the
**security model**, because a link that installs software + joins a network +
grants remote shell is a serious attack surface.

---

## 1. It's 80% built — reuse, don't reinvent

| Capability needed | Existing primitive | File |
|---|---|---|
| "Let me into this machine" (TeamViewer moment) | **support session** (in-mem, 6-char + bearer, read-only default, `--shell` opt-in, redeem URL) | `desktop/agent/support.go`, `support_http.go` |
| Shareable join **link** + session TTL/idle | **host-share invite** (`yaver.io/host-share/join?code=`) | `backend/convex/hostShare.ts` |
| Persistent cross-user device access | **infraAccessGrants** (+ security flags: allowTerminal/allowDesktopControl/allowTunnel/requireIsolation) | `backend/convex/access.ts`, schema 1253-1306 |
| Friend's box shows up as **my mesh peer** | mesh peers derive from infraAccessGrants | `backend/convex/mesh.ts::meshPeersForUser` |
| Carry an invite code **through install** | postinstall env var + headless device-code | `cli/src/postinstall.js`, `backend/convex/deviceCode.ts` |
| Headless sign-in landing | `yaver.io/auth/device?code=` | `web/app/auth/device/page.tsx` |
| **I drive their box from my Claude Code** | MCP `device_id` proxy (Layer-4 secrets never cross) | `desktop/agent/mcp_remote_proxy.go` |
| ssh / shell / files / screen+input | `ssh_resolve_lan.go`, `code_cmd.go`, `exec_cmd.go`, `ops_ghost.go`, RustDesk via `remoteview.go` | — |

**The missing 20% is orchestration:** a single "support invite" that, when
redeemed on the friend's machine, (a) installs+auths, (b) creates the
**reverse** infra grant (friend = host, me = guest), and (c) auto-joins mesh —
so the friend's device lands in my mesh peer list with the scope I asked for,
behind an explicit consent screen.

### The one conceptual flip
Normal sharing = *host invites guest to host's devices*. Support is **reversed**:
*I* generate the link, but redemption shares the **friend's** new device **to
me**. So the link encodes "grant the inviter (me) access to whatever device
redeems this," and redemption creates `infraAccessGrant{hostUserId: friend,
guestUserId: me, scope: support}`. Everything downstream (mesh visibility,
`yaver code --attach`, MCP proxy) then works unchanged, because they all key off
that grant.

---

## 2. End-to-end flow (recommended)

```
[Me] yaver support invite --scope full --ttl 24h
       → https://yaver.io/j/AB12CD34   (short code, signed)

[Friend] opens link on their machine
   web/app/j/[code] landing page:
     - validates code → shows "Kıvanç wants to help you. Installing Yaver…"
     - detects OS (mac/linux/wsl) → ONE copy-paste install line carrying the code:
         curl -fsSL https://yaver.io/j/AB12CD34/install | sh      (wrapper → npm i -g yaver-cli + yaver join AB12CD34)
       or: npm i -g yaver-cli && yaver join AB12CD34
   postinstall.js: if YAVER_JOIN_CODE present → stash in ~/.yaver/pending-join

[Friend] `yaver join AB12CD34`  (or auto on first run)
   - lightweight identity (see Decision 1): OAuth OR accountless link-identity
   - `yaver serve` registers the device
   - CONSENT SCREEN (native, see §4): "Allow Kıvanç to: ☑ Terminal ☑ Files
     ☑ Screen — for 24h. [Allow] [Allow once] [Deny]"
   - on Allow → redeem code → backend creates reverse infraAccessGrant + the
     device auto-joins mesh (joinMesh) → a persistent "X is connected" indicator
     appears

[Me] friend's device is now a mesh peer:
   yaver devices                  # friend-laptop online, 100.96.x.x
   yaver ssh friend-laptop        # over mesh, no relay if direct
   yaver code --attach friend-laptop   # my Claude Code, their filesystem
   yaver exec --device friend-laptop "…"
   # or from my Claude Code via MCP device_id proxy (see §5)

[Either side] revoke instantly: friend hits "Disconnect" / I run
   yaver support revoke friend-laptop ; grant + mesh peer drop on next reconcile
```

---

## 3. Why this beats today's options

- **vs RustDesk**: no relay account, self-hostable relay, no separate app — and
  I drive it with **AI** (`ghost_locate "click Deploy"`, `code --attach` for
  real fixes), not just a remote mouse.
- **vs raw Tailscale**: Tailscale gives the network; it does **not** give you
  "install + onboard a non-technical friend from one link" or "run my coding
  agent against their box." Yaver mesh is the network *plus* the agent.
- **vs `yaver launch ssh`** (exists): that SSH-bootstraps a box *you* already
  have SSH to. The support link is for a friend you **can't** reach yet.

---

## 4. UI — per surface

### Friend's side (the non-technical user) — must be dead simple + safe
- **Landing page** `web/app/j/[code]`: who's inviting, what it installs, OS-aware
  one-liner, a short "what is Yaver" reassurance. No jargon.
- **Consent screen** (the security gate) — rendered by the agent, shown on every
  platform:
  - macOS/Linux desktop: a small native window (or the agent's local web console
    `127.0.0.1:18080/app/?join=CODE`) with granular toggles + duration.
  - Headless/SSH-only: a CLI prompt (`yaver join` prints the scope and asks y/N).
- **Always-visible "connected" indicator**: menu-bar / tray icon or a persistent
  agent notification while someone has an active grant — RustDesk-style "you are
  being supported," one-click **Disconnect**. This is non-negotiable.
- **Activity log**: "Kıvanç ran `npm test` at 14:03" — the friend can see what
  was done.

### My side (the supporter)
- **CLI** (primary): `yaver support invite`, `yaver devices`, `yaver ssh`,
  `yaver code --attach`, `yaver exec`, `yaver ops ghost_*`. Plus my Claude Code
  (§5).
- **Web console** `NetworkView` (Mesh tab, built): the friend's device appears as
  a mesh peer with scope badges; add a **"Invite to support"** button that mints
  the link, and a **"Supporting: friend-laptop (24h left) — Revoke"** row.
- **Mobile**: the `network.tsx` console shows the peer + revoke; generating an
  invite link from the phone (share-sheet) is a natural add.

### UI questions to resolve (see decisions at the end)
- Does the friend create an account or not? (changes the whole onboarding)
- What does the link grant by default, and is it one-time or persistent?
- Where does consent live on a headless Linux box (no GUI)?

---

## 5. Supporting via **my Claude Code** (the Tailscale-parity ask)

Two complementary paths, both already in the codebase:

1. **MCP `device_id` proxy** (`mcp_remote_proxy.go`): my Claude Code calls any
   Yaver MCP tool with `device_id: "friend-laptop"` and it executes on the
   friend's agent (`exec`, `dev_*`, `git_*`, file reads…). **Layer-4 tools
   (vault/tokens/deploy-creds) refuse to cross machines** — secrets stay on each
   box. Every proxied call carries an `X-Yaver-Proxied-By` audit header.
2. **`yaver code --attach friend-laptop`**: opens a full coding-agent session
   whose shell + files are the friend's machine, but the **runner runs on my
   plan** (my Claude/Codex subscription) — the friend needs no AI agent, no API
   key. This is the core of "even though there is no AI agent on their side, I
   support them with mine."

Mesh makes both **direct** (overlay IP, no relay hop) once the friend is a peer,
which is what makes it feel like Tailscale: `ssh friend-laptop`, `code --attach
friend-laptop` just work by name.

**Gap:** the AI runner must be told to target the remote device's workspace.
`code --attach` already tunnels the terminal; confirm the runner's cwd/file ops
resolve to the remote agent (they do via the HTTP tunnel) and that
`device_id` threads into `CreateTaskWithOptions` for the MCP path.

---

## 6. Cross-platform matrix (Linux / macOS / Windows-WSL2)

The Go agent cross-compiles to all three; the **mesh data plane** and **support
capabilities** differ per OS:

| Capability | macOS | Linux | Windows (WSL2) |
|---|---|---|---|
| Agent binary (npm) | ✅ Apple-signed | ✅ x64+arm64 | ✅ runs **inside WSL2** (Linux binary) |
| Mesh TUN | ✅ utun (root) | ✅ /dev/net/tun (CAP_NET_ADMIN) | ⚠️ TUN lives in the **WSL2 net namespace** — see note |
| STUN/relay-DERP NAT traversal | ✅ | ✅ | ✅ (WSL2 buffer auto-tune already in `yaver serve`) |
| `yaver ssh` / `exec` / `code --attach` | ✅ | ✅ | ✅ (targets the WSL2 environment) |
| Screen view + input (`ghost`) | ✅ CGEvent + ScreenCapture (needs Accessibility + Screen-Recording perms) | ⚠️ X11 ok; **Wayland** needs portal/uinput | ❌ WSL2 can't see the **Windows** desktop |
| RustDesk fallback (`ghost_remote_*`) | ✅ | ✅ | ✅ (runs on Windows host) |

**The WSL2 caveat (important).** "Windows via WSL2" means the agent is a Linux
process in the WSL2 VM. Its mesh TUN + overlay IP live in the **WSL2 network
namespace**, so:
- Supporting **WSL2-resident** work (a dev's repo/shell/services in WSL2): fully
  works — `ssh`/`exec`/`code --attach` into WSL2.
- A **Windows-host-wide** VPN or seeing the **Windows GUI**: NOT covered by the
  WSL2 agent. Two options:
  1. **WSL2 mirrored networking** (Win11 23H2+) can bridge the overlay to the
     host — partial, fiddly.
  2. Ship a **native Windows agent** (wintun — `netconfig_windows.go` is already
     scaffolded) so Windows gets a host-level overlay + GUI ghost. This means
     adding a windows target to the npm binary matrix (currently macOS/Linux).

→ **Decision 4 below.** For a non-technical Windows friend you want to *screen-
support*, native Windows (wintun + GUI ghost) is the real answer; WSL2-only
supports developer scenarios.

---

## 7. SECURITY & ABUSE AUDIT (read this twice)

A link that **installs software, joins a network, and grants remote shell** is
the single most dangerous feature in the product. The threats and required
mitigations:

### T1 — Link leakage / forwarding
A support link forwarded or leaked must not silently grant a stranger access.
- **Mitigations:** short TTL on the invite (hours, not days); **single-use** by
  default (one redemption binds the code to one device); the **consent screen on
  redemption** is the real gate, not the link; bind the grant to the **specific
  device** that redeemed (not shareAllDevices); rate-limit redeem
  (already done for support redeem).

### T2 — The install step itself (supply-chain / `curl | sh`)
`curl https://yaver.io/j/<code>/install | sh` is a remote-code-execution prompt
by design. This is the same trust model as any installer, but:
- **Mitigations:** the wrapper script must ONLY `npm i -g yaver-cli` (signed,
  notarized binary, checksum-verified by postinstall) + `yaver join <code>` —
  nothing code-from-the-link. Show the script source on the landing page
  ("inspect before you run"). Never let the invite payload inject commands.
  Prefer `npm i -g yaver-cli && yaver join <code>` (no piped shell) as the
  default; offer `curl|sh` only as convenience with the source visible.

### T3 — Over-broad / surprise access
The friend must understand and control exactly what they grant.
- **Mitigations:** **default to least privilege** (view + files; shell/desktop
  are explicit toggles, OFF by default — mirrors `support session` read-only
  default). Granular consent toggles. Duration picker. **"Allow once" vs
  "Allow until I revoke."** Persistent "you are connected" indicator + activity
  log + one-click disconnect on the friend's side.

### T4 — Supporter impersonation
The friend should know *who* they're letting in.
- **Mitigations:** the invite is signed by my user identity; the consent screen
  shows my real account (email/avatar from Convex), not just a code. Warn if the
  inviter identity can't be verified.

### T5 — Lateral movement onto the friend's LAN / the mesh
A compromised supporter (or a malicious "friend" who is actually the attacker)
must not pivot through the mesh.
- **Mitigations:** the reverse grant is **device-scoped** (just the redeeming
  device), **not** shareAllDevices; **mesh ACLs default-deny** between the
  friend and my *other* peers (the friend should reach **only** me, not my whole
  tailnet, and I reach only their one device); **no subnet routes / exit-node**
  auto-granted; `requireIsolation` available for running guest workloads
  contained.

### T6 — Secrets exposure
- **Already mitigated:** Layer-4 tools (vault/tokens/deploy-creds) refuse
  `device_id` proxying; the friend's WireGuard **private key never leaves their
  device**; Convex stores pubkeys/endpoints only (privacy test pins this).

### T7 — Revocation must be instant and total
- **Mitigations:** revoke (either side) flips the grant → mesh reconcile (≤20s)
  drops the peer + tears the WG peer; kill the support session token; the friend
  can always `yaver support deny-all` / hit Disconnect to nuke every grant.
  Auth-token rotation already cascades.

### T8 — Non-technical user can't assess risk
This is the hardest. The whole UX must assume the friend cannot evaluate
security. → conservative defaults, plain-language consent, prominent revoke,
time-boxing, and "Kıvanç can see your screen and type on this computer right
now" must be **unmissable** while active.

### Audit/telemetry
Every grant create/redeem/revoke and every proxied/exec/ssh action is logged
(actor + action + target + outcome + ts) to the existing Convex activity audit
(privacy-safe summary) and shown to the friend locally.

---

## 7b. Locked decisions (2026-06-05)

1. **Friend identity = full OAuth account.** Redemption requires a 1-tap sign-in;
   the reverse grant is user-to-user, reusing guestAccess/infraAccessGrants/audit.
   Friend owns their identity and can revoke from any device.
2. **Default scope = view + files; shell/desktop OFF by default** (explicit
   consent toggles), mirroring `support.go`'s read-only default. Least privilege.
3. **Persistence = friend chooses on consent**: "Allow for 24h" (default) vs
   "Allow until I revoke." Covers the one-off TeamViewer moment and ongoing
   family/fleet management.
4. **Windows = WSL2-scoped now** (agent in WSL2 → ssh/exec/code into WSL2),
   **native Windows agent (wintun + GUI ghost) as a fast-follow** (scaffolded in
   `netconfig_windows.go`).

## 8. Build outline (after decisions)

1. **Convex**: `supportInvites` table (or extend `hostShareInvites`) — inviter
   userId, scope, ttl, single-use, signed code; `redeemSupportInvite` mutation
   that creates the **reverse** infraAccessGrant (device-scoped) + marks consent;
   reuse mesh peer derivation. Web HTTP routes `/support/invite`, `/j/<code>`.
2. **Web**: `/j/[code]` landing (OS-detect + install line + inviter identity);
   "Invite to support" button + "Supporting…" row in `NetworkView`.
3. **CLI/agent**: `yaver support invite`, `yaver join <code>` (install→auth→
   consent→redeem→mesh up), consent prompt (GUI window + CLI fallback),
   persistent "connected" indicator, `yaver support revoke|deny-all`.
4. **postinstall**: capture `YAVER_JOIN_CODE` → `~/.yaver/pending-join`.
5. **Mesh ACL default**: friend ↔ supporter only; default-deny to other peers.
6. **Cross-platform**: verify TUN + ssh/exec/code on mac/linux/WSL2; decide
   native-Windows (wintun + GUI ghost) per Decision 4.
7. **Claude-Code path**: confirm `device_id` MCP proxy + `code --attach` target
   the friend's workspace; document "support a friend" recipe.
8. **Security**: consent UX, least-privilege defaults, instant revoke, audit,
   landing-page script transparency.
