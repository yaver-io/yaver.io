# Screw-driving cell (firmware)

The Ender-3 + BTS7960 screwdriver cell that drives terminal-block screws and
pushes PASS/FAIL to the local Yaver agent.

- **`screwdriver_control.py`** — BTS7960 driver (CAT Power 36V motor @ 24V via a
  Mean Well EDR-120-24, GPIO12/13/5/6, VCC 3.3V). SPICE-tuned soft-start (first
  ramp step capped so standstill inrush < 5 A), dynamic brake, dead-time on
  reverse, optional MCP3008 clutch-slip. Needs a **1000–2200 µF / 50 V bulk cap**
  across B+/B−.
- **`endercli.py`** — Ender-3 as a Cartesian robot over serial (never heated).
- **`cell_runner.py`** — orchestrates: per screw → rapid to Y → spin-slow + creep
  Z to find the slot → ramp to drive → seat (clutch-slip auto-stop with MCP3008,
  else feed+dwell) → dynamic brake + lift → log PASS/FAIL.

## Run (on the Ender host)

```bash
sudo pigpiod
python3 cell_runner.py --calibrate-z                 # auto-find the contact Z (needs MCP3008)
python3 cell_runner.py --screws 5 --pitch 5.8 \
    --ficheno WO-7781 --product 94220142001EP \
    --yaver                                          # push to the local Yaver agent
```

`--yaver` (optional URL; default `http://127.0.0.1:18080`) records the block via
the **`screw_cell_record`** op using the agent bearer token from
`~/.yaver/config.json`. View results with the `screw_cell_analytics` /
`screw_cell_by_order` / `screw_cell_runs` ops. `--dry-run` traverses only.

deps: `pyserial`, `pigpio` (+ optional `spidev` for the MCP3008).
