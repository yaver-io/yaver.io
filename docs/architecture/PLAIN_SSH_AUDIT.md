# Plain SSH: office tmux to phone

Audit and implementation, 2026-10-08. Baseline: `a49b9a77f`.
This records the implementation and validation. Store upload status is recorded separately; a local build is not a release.

## Product contract

Open the phone, choose a saved SSH host, choose an existing session's pane,
and continue the runner already running there. The runner, its provider login,
working directory, history and tools remain on that machine. Disconnecting
the phone detaches the viewer; it does not stop or relaunch the runner.

The normal Yaver home and tab navigation remain. SSH has a direct connection
action, and login exposes account-free SSH. The separate screen always has
a Back action. Raw is the default terminal presentation. Cloud identity is useful for registry/relay/sharing but
is not a prerequisite for accessing a host which independently authorizes SSH.

## Baseline findings (code, not inferred from screen names)

| Area | Evidence | Finding |
|---|---|---|
| Startup | `mobile/app/index.tsx`, `(tabs)/_layout.tsx` | Index routed through cloud auth/survey; tabs redirected unauthenticated callers to login. |
| SSH home | `mobile/app/(tabs)/ssh.tsx` | Chose a registered Yaver device, called `selectDevice`, opened `/shell`. No direct SSH handshake. |
| Full terminal | `mobile/app/shell.tsx` | xterm rendered real VT output, but `/ws/terminal` and `/ws/runner` required a Yaver bearer. Session deep links contained no pane identity. |
| Legacy terminal | `mobile/app/(tabs)/terminal.tsx` | Removed ANSI cursor commands and rendered text. This is not adequate for interactive runner TUIs. |
| Agent attachment | `desktop/agent/runner_pty_attach.go` | Grouped tmux sessions preserve the source session and ignore phone dimensions. Targeting remains session-based. |
| Pane APIs | `desktop/agent/tmux_panes.go`, `mobile/src/lib/quic.ts` | Stable `%paneId` targeting exists for adopted task monitoring/input. It still goes through an authenticated Yaver agent. |
| Existing SSH control | `ssh_control_server.go`, `ssh_session_cmd.go` | Restricted recovery/ops channel. Its forced-command cage must not become a general shell. |
| Native SSH | `mobile/package.json`, iOS/Android native sources | No shipped general SSH client. `SSH_NATIVE_CLIENT_IMPL_PLAN.md` was a plan, not a native implementation. |
| Voice | `mobile/src/lib/speech.ts`, `terminalSpeech.ts` | On-device dictation and bounded speech-output helpers can be reused without moving runner credentials to the phone. |
| Other clients | `web/components/dashboard/TerminalView.tsx`, `tvos/YaverTV/Views/TVSSHView.swift` | Existing agent-backed terminals; they do not inherit a new phone-native SSH transport. |

The scoped MCP service was checked through its health tool and an independent
SSH/localhost HTTP probe. Both passed. The host has tmux and Tailscale. No
service restart, credential change, deployment or provider mutation occurred.
Private hostnames, addresses and credentials are deliberately absent here.

## Authentication decision

Tailscale networking and Tailscale SSH are different capabilities. Ordinary
OpenSSH reached through Tailscale still authenticates an OS user with an SSH
key/password. Tailscale SSH may authorize through its tailnet policy, including
additional check-mode requirements. A reachable tailnet address alone proves
neither authorization nor host identity.

The phone probes the SSH host key without sending credentials, asks the user
to verify the SHA-256 fingerprint, then pins it. A changed key fails closed.
Private keys/passwords/passphrases use native SecureStore, never the web
localStorage compatibility layer, Convex, the Yaver agent or URLs. Host metadata
is device-local. Existing runner OAuth remains entirely on the remote host.

References: [SSH over Tailscale](https://tailscale.com/docs/reference/ssh-over-tailscale),
[Tailscale SSH](https://tailscale.com/kb/1193/tailscale-ssh),
[tmux control mode](https://man.openbsd.org/tmux).

## Implementation

- `mobile/native/plain-ssh/core`: one Go `x/crypto/ssh` implementation bound
  into both platforms. No cloud dependency. Host verification precedes user
  authentication. Supports OpenSSH private keys, encrypted keys, passwords,
  and the server's SSH `none` authentication when tailnet policy permits it.
- `mobile/native/plain-ssh/{ios,android}`: asynchronous React Native bridges.
  The source-pinned `build.sh` produces XCFramework/AAR artifacts locally.
  Native autolinking and the Android config plugin preserve integration across
  Expo prebuild. Release scripts build the core before resolving native inputs.
- `mobile/src/lib/plainSSH.ts`: typed requests, local host metadata and native
  secure credential storage. An explicitly enabled, loopback-only browser
  companion supports the localhost RN-web preview. Browser credentials remain
  in session memory, never localStorage; the companion requires the exact
  configured Origin (loopback preview or explicitly selected production Yaver HTTPS). Without it, the browser names its TCP limitation.
- `mobile/app/plain.tsx`: saved hosts → fingerprint verification → session/pane
  picker → real terminal with raw keys and composed input. Dictation fills the
  draft; it does not execute automatically. Reading terminal output is explicit.
- `mobile/app/(tabs)/ssh.tsx`: direct SSH is the primary action; existing
  agent terminals remain behind disclosure. The normal app home is preserved.
- `PaneTerminalView.tsx`: a small Raw / Pane chat toggle keeps the same VT
  terminal mounted. Pane chat shows the parsed live screen and composed input,
  not guessed assistant turns. Mobile shell, Studio and glass share it; web
  dashboard and tvOS expose the same presentation choice.
- `YaverAccountCard.tsx`: optional phone account sign-in. `RemoteSSHEnrollment`
  and the native equivalents run `yaver auth --headless` on a separate SSH
  channel, with an approval link/code and an explicit install action if missing.
  They never send an auth command into the user's runner pane or copy a phone
  bearer to the remote.
- `apple/PlainSSH`: shared SwiftNIO SSH transport for TV, watch and vision;
  host-pinned channels, bounded exec output, exact pane identity and local
  Keychain credentials. The smaller surfaces expose screen snapshots honestly.
- `electron/src/plain-ssh.js`: bounded stdio RPC to the same Go core. Browser
  previews and subframes cannot invoke it. Windows packaging includes its own
  Windows companion, never a host macOS binary.
- `XtermView.tsx`: optional fixed remote dimensions preserve the desktop's
  terminal layout while allowing local scrolling. Other callers retain fitting.

The pane stream uses `tmux -C attach-session -f ignore-size` and filters raw
`%output` by the selected pane ID. `send-keys -H -t %id` encodes raw input bytes; composed messages use a
uniquely named tmux buffer and `paste-buffer -d -p -t %id` so the host respects
the running application's paste mode. Input cannot interpolate text as a tmux command. Selecting another pane on
the desktop cannot redirect phone input. No source pane/window is moved,
replaced, renamed or killed. Backgrounding closes the SSH client and stops
recording; reconnect is explicit. Uncertain input is never replayed.

## Surface boundary

| Surface | Result |
|---|---|
| Native iOS/Android phone and tablet | New direct SSH lane and shared core. Requires rebuilt app. |
| RN-web | Direct SSH through an explicitly enabled local companion; named limitation without one. Browser itself cannot dial TCP/SSH. |
| Existing mobile Yaver chat/terminal | Preserved authenticated agent lane; Raw / Pane chat presentation. |
| Web `/ssh` | Account-free direct SSH through the loopback companion, or desktop IPC inside Electron. Real xterm Raw / Pane chat. |
| macOS / Windows / Linux Electron | Bundled Go SSH companion over stdio; capability restricted to the trusted top-level `/ssh` page. No agent/cloud prerequisite. |
| tvOS / watchOS / visionOS | Shared SwiftNIO SSH package, host pinning and device Keychain. Raw / Pane chat use exact-pane screen snapshots, not a full VT emulator. Password, policy-authorized none, or unencrypted Ed25519 key. |
| Android TV / Wear OS | Shared Go SSH AAR and native account-free activity. Screen snapshots, composed input, raw control keys; credentials remain in activity memory. |
| Glass / AR via mobile | Shared interactive terminal and Plain SSH route; visionOS uses its native screen above. |
| CarPlay / Android Auto | Explicitly select a pane on the phone while parked; voice-only submission requires spoken confirmation and independently reconnects over SSH. No terminal/code on the car display. |
| Agent, CLI, relay, backend | Authorization and multi-tenant boundaries unchanged. |

## Verification and limits

`go test -race -count=1 ./...` in the core starts a real SSH server and an
isolated tmux server. It verifies fingerprint probing without authentication,
rejection of a changed/empty pin, correct-pane input after desktop selection
changes, raw output, and pane survival after disconnect. Fixtures do not use
an operator's tmux server or SSH credentials.

Native artifacts must be rebuilt after core changes. Full app/runtime checks,
physical-phone Tailscale transitions, Keychain/Keystore persistence, and actual
microphone/speaker behavior are separate from a successful core/library build.
Do not represent headless tests or RN-web's named limitation as physical-device
verification. Tailscale SSH check-mode interaction, SSH certificates/hardware
keys, non-default tmux sockets, and automatic network-roaming reconnection are
not implemented by this first direct lane. A host with no tmux sessions shows
the command to create one; this flow does not silently launch a new runner.


Local browser development: run `npm run ssh:bridge` in `mobile`, then start
RN-web with `EXPO_PUBLIC_PLAIN_SSH_BRIDGE=http://127.0.0.1:18494` and port
8094. The helper's `--origin` must match the UI origin exactly. It is not a
public relay, has no cloud bearer bypass, and cannot bind an external address.
The real SSH/tmux browser fixture is opt-in with `PLAIN_SSH_BROWSER_FIXTURE`;
`e2e/plain-ssh.config.ts` drives the real app with an iPhone device context.

Validation recorded on 2026-10-08: core race/integration tests, mobile and web
TypeScript checks, the eight terminal bridge tests, iOS XCFramework and Android
AAR builds, and Swift syntax parsing passed. Host-pin and stale-pane negative
controls failed when their guards were removed. RN-web tests exercise a real
loopback SSH server with an isolated tmux server, account-independent entry,
Back navigation, raw terminal output, composed input and toggling without
reattachment. Full native app installation and physical-phone checks remain
outstanding. Additional validation: full unsigned tvOS, watchOS and visionOS
builds; Swift integration against the real SSH/tmux fixture; Electron security
and packaging tests; Android TV and Wear OS Kotlin/Java compilation, and the current iOS XCFramework build. The browser and RN-web
loops prove the real pane with cloud identity absent. Native SSH key support
on Swift surfaces is narrower than the Go-based phone/desktop clients.


Real-host follow-up (2026-10-08): the Go client authenticated to the office
Mac over Tailscale with the existing SSH key, verified its host fingerprint
against public keys read through the established SSH connection, listed its
live panes and received raw output without sending input. The initial failure
was a locale-dependent tmux format: the SSH library does not inherit the
operator's UTF-8 locale. Every client now invokes `tmux -u`; the integration
server uses `LANG=C`/`LC_ALL=C` to cover this. No private host details are stored
in this audit. Composed input and snapshots also revalidate pane identity after
attachment, preventing a restarted tmux server's reused IDs from receiving input.

Car voice reads bounded current pane output, never claims a quiet screen means
completion, and supports read-pane, pause and repeated spoken turns. Sending
requires a spoken readback and explicit confirmation. Physical microphone,
CarPlay audio routing and vehicle testing remain device checks, not claims
made by the headless SSH proof.
