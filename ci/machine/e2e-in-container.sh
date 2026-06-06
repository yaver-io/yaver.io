#!/usr/bin/env bash
# Talos-IoT machine-hijack end-to-end test, in a plain Linux container (no
# privilege — Modbus is just TCP; the RTU phase uses socat PTYs). Two phases:
#
#   1. Modbus-TCP flow: /modbus-emu (PLC stand-in) + /machinetest flow —
#      absorb register map → observe live counter → write setpoint (verified by
#      read-back) → sync schematic to a mock Talos.
#   2. Modbus-RTU sniff: a virtual serial pair (socat); the engine passively
#      sniffs one end while the "master" writes setpoints on the other; the
#      schematic must absorb + classify the setpoint.
#   3. Modbus-RTU master: over a second virtual serial pair, the engine's active
#      RTU master (ReadRTU/WriteRTU over termios) drives a tiny RTU slave —
#      read + verified write round-trip. This is the actuate-over-serial plane.
#
# PASS = all phases succeed.
set -euo pipefail

EMU_PORT=1502

echo "== Talos-IoT machine e2e: $(uname -sr) =="

echo "--- Phase 1: Modbus-TCP edge flow ---"
/modbus-emu --port "$EMU_PORT" --dynamic >/tmp/emu.log 2>&1 &
sleep 1
echo "modbus-emu (PLC stand-in) up on :$EMU_PORT"
if ! /machinetest flow "127.0.0.1:$EMU_PORT"; then
  echo "--- emulator log ---"; tail -10 /tmp/emu.log || true
  exit 1
fi

echo
echo "--- Phase 2: Modbus-RTU serial sniff (virtual serial pair) ---"
socat -d -d pty,raw,echo=0,link=/tmp/ttyA pty,raw,echo=0,link=/tmp/ttyB >/tmp/socat.log 2>&1 &
SOCAT_PID=$!
trap 'kill $SOCAT_PID 2>/dev/null || true' EXIT
for i in $(seq 1 20); do [ -e /tmp/ttyA ] && [ -e /tmp/ttyB ] && break; sleep 0.25; done
if [ ! -e /tmp/ttyA ] || [ ! -e /tmp/ttyB ]; then
  echo "socat did not create the PTY pair"; cat /tmp/socat.log; exit 1
fi
echo "virtual serial pair ready: /tmp/ttyA <-> /tmp/ttyB"
if ! /machinetest sniff /tmp/ttyA /tmp/ttyB; then
  echo "--- socat log ---"; tail -10 /tmp/socat.log || true
  exit 1
fi

echo
echo "--- Phase 3: Modbus-RTU active master (virtual serial pair) ---"
socat -d -d pty,raw,echo=0,link=/tmp/ttyM pty,raw,echo=0,link=/tmp/ttyS >/tmp/socat2.log 2>&1 &
SOCAT2_PID=$!
trap 'kill $SOCAT_PID $SOCAT2_PID 2>/dev/null || true' EXIT
for i in $(seq 1 20); do [ -e /tmp/ttyM ] && [ -e /tmp/ttyS ] && break; sleep 0.25; done
if [ ! -e /tmp/ttyM ] || [ ! -e /tmp/ttyS ]; then
  echo "socat did not create the master/slave PTY pair"; cat /tmp/socat2.log; exit 1
fi
echo "virtual serial pair ready: /tmp/ttyM (master) <-> /tmp/ttyS (slave)"
if ! /machinetest rtu /tmp/ttyM /tmp/ttyS; then
  echo "--- socat2 log ---"; tail -10 /tmp/socat2.log || true
  exit 1
fi

echo
echo "RESULT: PASS — Modbus TCP edge flow + RTU sniff + RTU active master all verified ✓"
