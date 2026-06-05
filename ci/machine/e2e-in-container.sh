#!/usr/bin/env bash
# Talos-IoT machine-hijack end-to-end test, in a plain Linux container (no
# privilege — Modbus is just TCP). A Modbus-TCP emulator (/modbus-emu) stands in
# for a wire-processing machine's PLC; the edge harness (/machinetest) is the
# Yaver agent on the Pi. It absorbs the register map, observes the live counter,
# writes a setpoint (verified by read-back), and syncs the schematic to a mock
# Talos commander. PASS = the whole READ/WRITE/SYNC flow succeeds.
set -euo pipefail

EMU_PORT=1502 # >1024 so no root needed

echo "== Talos-IoT machine e2e: $(uname -sr) =="
/modbus-emu --port "$EMU_PORT" --dynamic >/tmp/emu.log 2>&1 &
sleep 1
echo "modbus-emu (PLC stand-in) up on :$EMU_PORT"

if /machinetest flow "127.0.0.1:$EMU_PORT"; then
  exit 0
fi
echo "--- emulator log ---"; tail -10 /tmp/emu.log || true
exit 1
