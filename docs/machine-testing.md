# Machine engine (Modbus / Talos-IoT) — testing

The machine engine is the Yaver side of **Talos-IoT machine hijack**: a Yaver
agent on a Raspberry-Pi (or any Linux edge box) wired to an industrial machine's
Modbus bus. Talos (cloud) is the thin commander + system of record; Yaver is the
substrate — Modbus read/write + the ghost vision engine for HMIs + AI runners
for parameter understanding. (Design: `../talos/MACHINE_HIJACK_YAVER_DESIGN.md`.)

The hijack has two strictly-separated planes:
- **READ plane** — absorb the PLC's registers read-only (Modbus fc3/fc4),
  classify each register's role (setpoint / live measurement / counter / alarm)
  from how its value behaves.
- **WRITE plane** — write a setpoint (fc6) **verified by read-back** (the safe
  write gate; high-risk registers require `allowHighRisk`).
- **SYNC** — push the discovered schematic + telemetry to Talos `/machine-edge`
  with a Bearer org secret.

## Run the tests (no hardware, no cloud, $0)

```bash
# Unit + integration (in-process Modbus-TCP slave; runs anywhere incl. macOS)
cd desktop/agent && go test ./machine/

# Full edge flow e2e in a container (RPi-like): emulator PLC + edge harness +
# mock Talos. Cross-compiles for linux/arm64 on Apple Silicon.
./scripts/test-machine-e2e.sh
./scripts/test-suite.sh --machine-e2e
```

## What the suite covers

**`machine/modbus_test.go`** (in-process Modbus-TCP slave, real socket):
- `DialTCP` + `ReadRegisters` (fc3/fc4) decode.
- `WriteSingleRegister` (fc6) + read-back.
- `Engine.ScanTCP` / `ReadTCP` / `WriteTCP` (schematic shape, driver/source).
- `classify` register-role heuristics (setpoint/live/counter/constant/unknown).
- CRC-16 + RTU frame extraction round-trip (`scanFrames`).

**Docker e2e** (`scripts/test-machine-e2e.sh` → `ci/machine/e2e-in-container.sh`):
- `cmd/modbus-emu` — a standalone Modbus-TCP slave emulating a wire-processing
  machine's PLC. `--dynamic` animates it (constant setpoint, jittering live
  measurement, monotonic piece counter, periodic alarm bit).
- `cmd/machinetest` — the edge agent. `flow <addr>`: absorb registers → observe
  the counter advancing over time → write a setpoint, verify read-back → POST
  the schematic to a mock Talos `/machine-edge` (Bearer org secret). PASS only
  if every leg succeeds.

This mirrors the real topology — **Pi edge agent ↔ Modbus PLC ↔ Talos
commander** — so a regression in the Modbus driver, register classification, the
verified-write gate, or the Talos-sync shape fails locally in ~30s instead of on
a factory floor.

## Why an emulator (not a real PLC)

`cmd/modbus-emu` speaks the exact MBAP/PDU wire format the engine's `TCPClient`
expects, so the engine can't tell it from a real PLC. It's pure Go (no external
Modbus library — the engine is dependency-free), so the same code runs as an
in-process test fixture (`machine/modbus_test.go`) and as a container binary.
For RTU/serial (Linux-only) the unit tests exercise CRC + framing directly,
since serial needs a hardware tap.

## Not yet covered (follow-ups)
- AI `machine_understand` against a mock LLM (the OpenAI-compatible chat call).
- `machine_sync` from the real `ops_machine.go` path (the e2e simulates the
  POST shape; wiring the actual ops verb through a mock Talos is the next step).
- RTU serial sniff on a virtual serial pair (socat PTY) in a container.
- The ghost-vision HMI write-back path (covered by the ghost test surface).
