# Edge machine control on a Raspberry Pi (RS-485 / RS-232 → PLC / CNC), driven for Talos

This documents the edge-machine capability: a low-power Linux box (Raspberry Pi
is the canonical example) wired to an industrial bus over a USB-serial adapter,
running the Yaver agent, **actuated and observed remotely** from any Yaver
surface (mobile app, Claude Code via the Yaver MCP, `yaver` CLI, web), and
feeding a Talos backend as the historian / dashboard.

> Code is the source of truth. This file is accurate at commit time; if it and
> the code disagree, the code wins — grep `desktop/agent/machine/` and
> `desktop/agent/ops_machine.go` / `ops_gcode.go` / `machine_edge_loop.go`.

## The architecture in one rule

**Don't drive the machine register-by-register over the relay. Vibe the *code of
a durable edge worker* that runs the control loop locally on the Pi, and use
remote ops only for observe / setpoint / deploy / understand / e-stop.**

A relay round-trip plus a verified read-back is two WAN hops — fine for "set the
cut-length to 1300", wrong for a tight poll loop or a streamed G-code program
where a stalled frame crashes a spindle. So there are two planes:

| Plane | Where it runs | What it's for |
|---|---|---|
| Control plane | remote, occasional | `machine_ports`, sniff/understand, one-off `machine_read/write`, `gcode_*`, kicking off the loop |
| Data plane | local on the Pi, continuous, durable | `yaver machine edge-loop` as a companion systemd user unit — poll/sniff → understand → `machine_sync` to Talos |

## Protocols supported

- **Modbus-TCP** — active scan/read/write over Ethernet. Works on every platform.
- **Modbus-RTU over serial (RS-485/RS-232)** — both **passive sniff** *and*
  **active master** (read + verified write). Linux-only (termios). This is the
  Pi case.
- **G-code / CNC over serial** — line-oriented `ok`/`error` flow control
  (GRBL / Marlin / generic), with ok-gated streaming, realtime status, motion
  soft-limits, and an un-gated realtime e-stop.

## Ops verbs

All owner-only (`AllowGuest:false`), opt-in behind `yaver serve --machine`
(`config.MachineEnabled`). Each routes to a chosen device through the standard
`ops` dispatcher (`machine=<deviceId>` → `proxyToDevice`), so the same verb is
reachable identically from mobile / Claude Code / CLI / web.

### Modbus

| Verb | Notes |
|---|---|
| `machine_status` | engine enabled? serial supported? active sessions |
| `machine_ports` | enumerate `/dev/ttyUSB*` `/dev/ttyACM*` + stable `/dev/serial/by-id/*` + kernel driver. `autobaud=true device=...` probes baud rates and reports the one with the most CRC-valid frames |
| `machine_sniff_start/feed/sniff_status/sniff_stop` | passive RTU bus tap + classification (unchanged) |
| `machine_scan_registers` | `addr` (TCP) **or** `device`+`baud` (RTU), read-only |
| `machine_read` | `addr` (TCP) **or** `device`+`baud` (RTU); reports `transport` |
| `machine_write` | `addr` (TCP) **or** `device`+`baud` (RTU); range-clamped + read-back verified; `allowHighRisk` required without explicit min/max |
| `machine_understand` | AI labels the schematic; offloads inference (see below) |
| `machine_sync` | push heartbeat + schematic + telemetry to Talos `/machine-edge/*` |

### G-code / CNC

| Verb | Notes |
|---|---|
| `gcode_open` | open a controller on a serial device (`grbl`/`marlin`/`generic`); takes exclusive bus ownership |
| `gcode_send` | one line, waits for `ok`/`error`. A **motion** line needs a soft-limit envelope or `allowHighRisk` |
| `gcode_stream` | ok-gated program streaming. **Always** validated against soft limits first; `dryRun` validates without sending; an out-of-envelope move aborts before any byte is transmitted |
| `gcode_status` | live state (GRBL realtime `?` / Marlin `M114`) |
| `gcode_estop` | **un-gated** realtime feed-hold + soft-reset (GRBL) / `M112` (Marlin); bypasses an in-flight stream |
| `gcode_close` | close + release the bus |

## Half-duplex bus arbitration

An RS-485 pair is one shared half-duplex wire. The Engine tracks an **exclusive
holder per resolved device** (a `by-id` symlink and its `/dev/ttyUSB*` node
collapse to one bus):

- a `sniff` or `gcode` session claims the bus exclusively for its lifetime;
- a transient RTU read/write refuses while a session holds the bus, and
  serializes against other transient txns.

So a remote `machine_write` can never collide with a running sniff, and two
masters can't talk over each other. (`desktop/agent/machine/ports.go`.)

## Hotplug / by-id durability

USB-serial adapters drop and renumber (`ttyUSB0` → `ttyUSB1`). Use a stable
`/dev/serial/by-id/...` path; the engine resolves symlinks for the bus key, and
the sniffer's reconnect mode reopens the device after a read error instead of
dying — the difference between a durable worker and one that fails on the first
cable wiggle. (`StartSniffOpts`, `desktop/agent/machine/sniff.go`.)

## Inference offload for `machine_understand`

A Pi can't run a useful model. Resolution order for the understand endpoint:

1. `YAVER_MACHINE_UNDERSTAND_URL` / `_MODEL` / `_API_KEY` (a paired beefy peer,
   or a rented GPU from the GPU-rental lane),
2. `GHOST_VISION_*` / `OPENAI_*`,
3. on-box Ollama (`localhost:11434`) — last resort.

Set `YAVER_MACHINE_UNDERSTAND_URL` on the Pi to your Mac or a GPU host.

## The durable edge-loop (Talos)

```
yaver machine edge-loop \
  --device /dev/serial/by-id/usb-FTDI_...-if00-port0 --baud 9600 \
  --start 0 --count 8 --interval 10s \
  --talos-url <TALOS_MACHINE_URL> --org-id <id> --org-secret <secret> \
  --device-id pi-edge-001 --machine-key wire-machine-1 --understand
```

(TCP PLC: `--addr host:port` instead of `--device`.) Each cycle reads the
register window, on the first cycle optionally AI-labels the schematic and pushes
it as the machine "manual", then streams telemetry. Talos creds come from env /
the box's vault — never Convex.

Make it reboot-durable by running it as a companion service. Generate the
manifest:

```
yaver machine companion --device /dev/ttyUSB0 --baud 9600 \
  --device-id pi-edge-001 --machine-key wire-machine-1 > yaver.companion.yaml
yaver companion up        # or the companion_up MCP verb
```

`durable: true` makes it a `yaver-companion-machine-edge-edge-loop` **systemd
user unit** with `Restart=always` + `loginctl enable-linger` — surviving reboot
*and* agent downtime — re-verified on every boot by the companion engine's
`Reconcile()`. Use the companion engine for the durable worker, **not** the Fleet
SDK `schedule()` (that's the backend-DB cron bridge, a different system).

## Reachability behind plant NAT

`yaver auth --headless` (device code) → registers → heartbeats
`localIps`/`quicHost`/`publicEndpoints` → the agent dials **out** to the relay
over QUIC 4433 and accepts proxied HTTP. No port-forward in the customer's
network. Direct-LAN / Tailscale are tried first when reachable.

## Safety posture

- **Modbus writes**: range-clamped (`min`/`max`) + read-back verified;
  high-risk (no range) refused unless `allowHighRisk`. Safety functions stay
  hardwired on the machine — Yaver never touches them.
- **CNC motion**: validated against a soft-limit envelope (modal G90/G91 aware)
  before transmission; `dryRun` to check a program without moving; **e-stop is
  never gated** and always lands, even mid-stream.

## Tests

- `desktop/agent/machine/rtu_test.go` — active RTU master (read / write /
  exception / timeout / resync) over an in-process slave on a `net.Pipe`; bus
  arbitration.
- `desktop/agent/machine/gcode_test.go` — ok-gated send/stream, soft-limit +
  dry-run aborts, relative-move validation, realtime e-stop bytes,
  `IsMotionLine`.
- `desktop/agent/machine/modbus_test.go` — existing TCP plane + classify +
  frame round-trip.
- E2E (`./scripts/test-machine-e2e.sh`, `--machine-e2e`): containerized, $0, no
  hardware — Phase 1 Modbus-TCP flow, Phase 2 RTU sniff (socat PTYs), **Phase 3
  RTU active master** (socat PTYs, `machinetest rtu`).

```bash
cd desktop/agent && go test ./machine/ -count=1
./scripts/test-machine-e2e.sh
```
