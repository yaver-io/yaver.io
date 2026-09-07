# Desktop GUI discoverability and remote access — audit handoff

Date: 2026-09-05

Status: implementation in the working tree; not committed, packaged, signed, deployed, or released.

## Owner intent

After a user installs the Yaver desktop GUI and signs in, the machine should be easy to find and use from another Yaver surface:

- on the same LAN;
- over Tailscale when the controller is remote;
- through the user's owner-scoped Convex device registration and Yaver relay paths;
- through Yaver Remote Desktop, and through the operating system's RDP client where the target is Windows;
- without weakening the Windows Firewall or the existing multi-tenant relay boundary;
- with visible diagnostics on Windows, macOS, and Linux;
- with deterministic `Try fix` actions when a safe repair exists;
- with `Fix with AI` through OpenCode and the account/machine's configured provider/model, including DeepSeek V4 Flash when that is the selected OpenCode model;
- with only one live desktop GUI instance and a recovery story for stale instances;
- branded as **Yaver** in Dock/taskbar hover and user-facing OS chrome, never as the generic runtime name.

The desired end state is a transport ladder, not one discovery mechanism:

1. Convex identifies owner-authorized machines and supplies current candidate metadata.
2. LAN beacon discovery accelerates same-network connection.
3. Tailscale candidates provide a private remote path when both devices are on the tailnet.
4. Public tunnel/relay remains the fallback.
5. The client operation-probes a candidate before claiming that it is usable.

## Code-truth findings

### Discovery producers

- `desktop/agent/beacon.go` broadcasts Yaver's proprietary UDP beacon on port `19837` every three seconds. This is LAN discovery, not DNS-SD/Bonjour service advertisement. It deliberately skips point-to-point `/32` VPN-style interfaces, so it must not be expected to discover a remote Tailscale peer by broadcast.
- The agent heartbeat publishes owner-scoped connection candidates to Convex, including `quicHost`, `localIps`, and `publicEndpoints`. The current machine rows contain LAN/tailnet candidates, but the inspected rows did not have populated connection preferences.
- `desktop/agent/mdns_local.go` resolves `.local` hostnames on supported platforms; it is not a cross-platform `_yaver._tcp` Bonjour implementation and does not provide Windows discovery.
- Mobile native already consumes UDP discovery and local IP candidates. A browser cannot receive UDP beacons or raw QUIC, so Convex plus standard HTTPS transports are its discovery source.

### Critical consumer gap

The desktop GUI loads the HTTPS dashboard in a hardened renderer with `webSecurity: true`. The web `AgentClient` primarily consumes `device.host` and cannot safely fetch a plain `http://` agent at a LAN or Tailscale address from an HTTPS page. Therefore:

- Convex can contain a correct Tailscale/LAN address while the desktop GUI still cannot use it for normal Yaver agent API traffic;
- the GUI currently relies on HTTPS tunnel/relay routes for those requests;
- disabling renderer security or relaxing authenticated CORS to `*` is not an acceptable fix.

This remains the most important unfinished implementation item. The safe direction is a narrow native main-process transport/proxy bridge, or authenticated agent HTTPS with identity pinning. It must validate every requested target against an owner-scoped Convex device row and preserve bearer-token and relay-password boundaries.

### Tailscale false-green

Tailscale detection used `exec.LookPath` in some paths even though the repo already had broader platform-aware binary resolution elsewhere. This missed the macOS App Store bundle and common Windows install locations. It also treated “binary exists” as equivalent to “tailnet works.”

The operation probe on this Mac found a concrete false-green: the binary and a tailnet address exist, but the daemon reports `NeedsLogin` and a DNS health problem. Diagnostics now name that state instead of reporting Tailscale as ready. No private address is included in this document.

### Remote desktop meanings

- Yaver Remote Desktop is the agent's authenticated screen/input feature (`/rd/*`); it is not Microsoft RDP.
- Microsoft RDP is useful only when the target supports and has explicitly enabled it. It must not be auto-enabled by Yaver.
- The first Yaver screen-view consent previously could only be granted by a loopback request, while the remote dashboard naturally arrived through a remote route. The desktop Health UI now supplies the local explicit-consent lane.
- A Windows device card can now offer `Open RDP` only when the native desktop shell sees a Convex-published Tailscale IPv4 candidate. The shell first operation-probes TCP `3389`, then opens the OS RDP client. It does not open or enable port `3389` itself.

### Distribution limitations

- The direct signed/notarized desktop package is intended to include and supervise the local Yaver agent.
- The Mac App Store/TestFlight build is sandboxed and is intentionally client-only. It cannot honestly provide arbitrary repository access, spawn the full agent workload, or host all capture/automation capabilities. Its diagnostics now state this instead of promising a discoverable local node.
- The release workflow currently builds macOS and Linux. Windows packaging/signing steps exist, but Windows is not in the active build matrix while the Authenticode identity remains unresolved. Do not describe Windows desktop as released.

## Implemented in the working tree

### CLI discovery correctness

Files:

- `cli/src/discovery.js`
- `cli/test/discovery.test.js`

Changes:

- Corrected the probed agent port from `8347` to the canonical `18080`.
- Uses the UDP packet sender address instead of the nonexistent `socket.remoteAddress` value.
- Corrected the user-facing connection direction.
- Added pure parsing and focused regression coverage.

### Tailscale detection and doctor probes

Files:

- `desktop/agent/tailscale.go`
- `desktop/agent/tailscale_peers.go`
- `desktop/agent/diagnose_checks_v2.go`
- `desktop/agent/tailscale_binary_test.go`

Changes:

- Reused platform-aware Tailscale binary resolution.
- Added macOS app-bundle and Windows Program Files candidates.
- Changed desktop diagnosis from executable inventory to `tailscale status --json` backend/address truth.

### Owner registration and remote-desktop doctor

Files:

- `desktop/agent/httpserver.go`
- `desktop/agent/remotedesktop.go`

Changes:

- `/agent/doctor` now performs a bounded, owner-authenticated Convex devices-list readback for the local device row.
- It compares current interface categories with registered candidates without returning the actual addresses in diagnostic text.
- It checks Tailscale backend state and Yaver Remote Desktop policy/engine/display readiness.
- Doctor `ok` is no longer unconditionally true when a check failed.
- Removed a hard-coded “Windows machine” phrase from a cross-platform remote-desktop message.

### Native desktop connectivity doctor and fixes

Files:

- `electron/src/desktop-connectivity-doctor.js`
- `electron/src/main.js`
- `electron/src/preload.js`
- `electron/src/agent-manager.js`
- `electron/test/desktop-connectivity-doctor.test.js`
- `electron/test/agent-manager.test.js`
- `electron/test/main-wiring.test.js`

Changes:

- Added structured native checks for local agent health, local identity, private candidate counts, Tailscale daemon truth, host firewall posture, and OS/Yaver remote desktop readiness.
- The renderer receives categories and statuses, not secrets or private addresses.
- Added fixed-ID IPC actions. The renderer cannot provide an arbitrary command or executable path.
- Windows Firewall repair creates program-scoped inbound rules for the exact Yaver agent executable, TCP `18080`/`18443` and UDP `4433`, only for Domain/Private profiles, scoped to `LocalSubnet` and `100.64.0.0/10`. It never opens `3389`, never enables the Public profile, and uses the OS elevation prompt.
- Linux reports active `ufw`/`firewalld` filtering but does not invent a broad cross-distro rule. It routes the user to an operation probe and AI repair when deterministic safety cannot be guaranteed.
- macOS reports Application Firewall posture and routes to the correct settings surface.
- Added a local explicit-consent repair for Yaver screen view.
- Added a Tailscale-only OS RDP launcher with a TCP `3389` operation probe.
- Restart only controls a GUI-owned agent child. It refuses to kill an externally managed/adopted service.

### Desktop Health UI and AI repair

Files:

- `web/components/dashboard/HealthView.tsx`
- `web/lib/desktopConnectivityDoctor.test.ts`

Changes:

- Added **Connectivity & Remote Access** to the desktop Health surface.
- Renders named cause, status, rescan, and one deterministic repair action when available.
- Adds `Fix with AI` for eligible findings.
- AI repair creates an OpenCode task and intentionally omits a hard-coded model so the device/account's configured OpenCode provider/model is used. This permits DeepSeek V4 Flash when configured without forcing it for every account.
- The AI prompt requires before/after operation probes and forbids disabling the firewall, allowing the Public profile, enabling Microsoft RDP, changing Tailscale ownership/ACLs, or granting capture/input permission without explicit consent.
- The existing agent doctor filter now includes config, auth, agent, connectivity, network, relay, and remote-access findings.

### Windows RDP action

Files:

- `web/components/dashboard/DevicesView.tsx`
- `electron/src/main.js`
- `electron/src/preload.js`

Changes:

- Windows cards in the desktop shell can show `Open RDP` when a Tailscale candidate is available.
- The native shell validates that the host is in the Tailscale CGNAT range, probes `3389`, and opens `mstsc.exe` on Windows or the registered RDP URI handler on macOS/Linux.
- Added `data-device-id` to device cards so closed-loop tests identify a machine by owner-scoped identity rather than a hostname that may be replaced by a friendly alias.

### Visible product branding

Files:

- `electron/src/main.js`
- `electron/package.json` (already had packaged `productName: "Yaver"`)
- `electron/electron-builder.mas.cjs` (already inherits the same product name)
- `electron/test/main-wiring.test.js`

Changes:

- Calls `app.setName("Yaver")` before application readiness so development runtime launches use Yaver in application-facing OS chrome.
- Packaged macOS, Windows, and Linux metadata already uses Yaver.
- The macOS Dock icon is set explicitly for unpackaged/dev launches.
- Internal source directories and dependency names still contain the runtime's technical name; this is not intended to be user-facing.

### Duplicate desktop task rows / React key overlay

Files:

- `web/app/dashboard/page.tsx`
- `web/lib/taskIdentity.ts`
- `web/lib/taskIdentity.test.ts`

Observed issue:

The desktop dashboard rendered the same `deviceId:taskId` twice, producing two copies of React's non-unique-key error overlay. The two overlay entries were repeat emissions of one underlying defect.

Root cause and change:

- Multiple connected device cards can route `/tasks` to the same runner-role machine.
- Task sources are now keyed by the effective task-route device rather than only the card used to open the connection.
- The merged history also deduplicates exact machine/task pairs after freshness sorting.
- Equal task IDs from different machines remain separate because device identity is part of the key.
- The sidebar now uses the same canonical scoped key helper.

### Installed macOS GUI startup incident

Files:

- `electron/src/desktop-runtime-policy.js`
- `electron/src/main.js`
- associated desktop runtime tests and `electron/README.md`

Evidence from the already-installed TestFlight app:

- On Apple-silicon macOS 26, the MAS renderer exited before first paint with code `5`.
- Launching the same installed build with `--js-flags=--jitless` allowed the dashboard renderer to load and stay alive.
- The MAS runtime policy now applies that bounded workaround only to affected Apple-silicon macOS 26+ MAS builds.

No new desktop package was built or installed in this session.

## Verification already completed

These checks were run before the owner instructed the session not to build or run anything further:

- Desktop unit suite: `75/75` passed.
- CLI discovery tests: `3/3` passed.
- Desktop connectivity web assertions: `3/3` passed.
- Scoped task identity tests: `2/2` passed.
- Web TypeScript check: passed.
- JavaScript syntax checks for desktop main/preload/doctor: passed.
- `git diff --check`: passed.
- Native diagnostic probe on the current Mac: local agent and identity passed; LAN and tailnet candidate categories were present; Tailscale truth was warning (`NeedsLogin` plus DNS health); macOS Application Firewall reported disabled.

Go package tests were attempted but did not complete in a reasonable time and were interrupted. Do not claim the Go test suite is green.

Closed-loop desktop smoke history:

1. The first run reached the real dashboard but failed because the harness searched for a raw hostname that the UI correctly replaced with a friendly alias. The harness now uses `data-device-id`.
2. The next run was interrupted by the desktop window closing during navigation (`Target page, context or browser has been closed`). It did not produce a green end-to-end result.
3. After the owner's instruction, no further smoke or build was run.

The duplicate-task fix has focused unit coverage but still needs a fresh closed-loop assertion when execution is authorized again.

## Process cleanup performed

- Closed the exact stale repo-launched desktop smoke process and its helper processes.
- Confirmed no installed Yaver or Yaver TestFlight GUI process remained running at that point.
- Stopped the Next.js dev server started for this audit.
- Did not stop or modify the unrelated pre-existing Xcode/iOS archive job.
- No data or application files were deleted. Temporary smoke artifacts may still exist under the test harness's temporary artifact directory.

The existing `requestSingleInstanceLock()` prevents two cooperative GUI instances. A genuinely hung lock-holder still needs a product recovery design; see remaining work.

## Remaining work, in priority order

### P0 — native direct-agent transport for LAN/Tailscale

Build a narrow native transport bridge so the desktop GUI can use owner-authorized Convex `localIps`/`quicHost` candidates without an HTTPS renderer making mixed-content HTTP calls.

Required invariants:

- Fetch candidates from the signed-in user's owner-scoped row.
- Validate device id, address, port, scheme, and route in the native process; no arbitrary renderer URL proxy.
- Preserve bearer auth in headers only; never put tokens in URLs or logs.
- Operation-probe `/health`/`/info`, then select direct LAN/Tailscale or fall back to HTTPS tunnel/relay.
- Do not disable `webSecurity` and do not add authenticated CORS `*`.
- Keep relay authorization owner/access-graph scoped and cryptographic; the relay must never become an authorization boundary.
- Add parity tests for browser/native transport selection and a real remote Tailscale arc.

Until this exists, the desktop GUI is discoverable in inventory but is not guaranteed to use a published Tailscale address for ordinary Yaver API traffic.

### P0 — Windows release and real Windows proof

- Restore/add Windows to the active CI build matrix only after the Authenticode identity is available.
- Build and sign the package through the canonical release workflow only with owner authorization.
- On a real Windows standard-user install, prove:
  - the embedded agent starts;
  - UAC appears only for firewall repair;
  - the exact program/profile/source/port rules are created;
  - Public profile remains closed;
  - LAN and Tailscale peer operation probes pass;
  - uninstall/update handles the rules deliberately;
  - RDP diagnostics distinguish unsupported Windows editions, disabled service, firewall block, and unreachable target.

The Windows firewall code is unit-tested but has not been operation-probed on Windows in this session.

### P0 — close the desktop smoke loop

When execution is authorized:

- reproduce why the last native window closed during authenticated navigation;
- ensure failure logs identify renderer exit, single-instance handoff, app quit, or dashboard navigation cause;
- assert runtime app name `Yaver`;
- assert Connectivity & Remote Access is visible;
- assert no duplicate-task console error after multi-device runner-role routing;
- assert no private address/token is printed in artifacts;
- test the direct signed/notarized package separately from the client-only MAS build.

### P1 — stale GUI recovery

`requestSingleInstanceLock()` covers normal duplicate launches, but not an unresponsive process that owns the lock.

A safe recovery design should:

- make the second launch request a focus/health acknowledgement from the first;
- wait for a short bounded acknowledgement;
- show a native recovery choice if the first instance is alive but unresponsive;
- terminate only the verified same-product/same-user GUI process after explicit user confirmation;
- never kill an externally managed Yaver agent or unrelated runtime process;
- preserve unsent work and diagnostic logs where possible.

Do not implement this as a broad process-name kill.

### P1 — discovery protocol consolidation

- Decide whether to add real DNS-SD (`_yaver._tcp`) for LAN convenience. It cannot replace Convex/Tailscale discovery and needs Windows firewall/mDNS handling.
- Define one shared candidate model and preference order for mobile, desktop native, web, and CLI.
- Populate and consume connection preferences only from measured outcomes, not address-shape guesses.
- Continue treating UDP broadcast as LAN-only and Convex as the browser-compatible remote inventory.

### P1 — repair lifecycle and AI result routing

- Stream deterministic repair output with bytes and elapsed time instead of only returning the final IPC result.
- Return to the failed operation and re-probe automatically after repair.
- Ensure the OpenCode repair task is visibly linked back to the exact diagnostic finding and streams in the desktop task console.
- Surface structured AI repair completion/failure, not only a task-created state.
- Prove configured DeepSeek V4 Flash selection end-to-end on an account that has it configured; do not hard-code it globally.

### P1 — cross-surface parity

- Consume the expanded structured doctor result on mobile and other native surfaces rather than copying text classifiers.
- Give CLI users the same named cause and invocable safe repair where possible.
- Add a parity guard for the discovery/transport ladder across desktop, mobile native, and browser constraints.

## Security decisions that must not regress

- Never expose auth tokens, relay passwords, customer IPs, or private hostnames in logs, test artifacts, docs, or AI prompts.
- Never trust a relay tier or request shape as authorization.
- Never open Windows Firewall to Public or `Any` when a program/profile/source-scoped rule suffices.
- Never auto-enable Microsoft RDP or remote input/capture permissions.
- Never build an arbitrary URL/command proxy from the renderer to the native process.
- Never kill processes by a broad `Electron`, `Yaver`, or executable-name match.
- Never make the sandboxed MAS client claim that it can host the full local agent.

## Working-tree and next-session instructions

- The repository was already heavily dirty with unrelated in-progress changes. Preserve all existing work and inspect overlapping diffs before editing.
- The files listed above contain this audit's changes, but several (especially `desktop/agent/httpserver.go`, `web/app/dashboard/page.tsx`, and `electron/src/main.js`) also contain other work.
- Nothing from this audit is committed or pushed.
- Do not deploy, publish, package, submit, tag, or commit without explicit owner permission.
- The owner explicitly said: **do not build anything yet**. A new session should begin read-only, re-read `AGENTS.md` and `CLAUDE.md`, inspect this handoff and the live diffs, and ask before resuming builds or closed-loop execution.
