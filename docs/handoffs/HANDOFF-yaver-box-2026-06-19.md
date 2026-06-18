# Handoff: Yaver Box / Talos Edge Work

Date: 2026-06-19
Repo cwd when written: `<repo>`

## User Intent

Build "Yaver Box" as a reusable Raspberry Pi based industrial IoT box for three
first real projects:

1. `../ocpp` / Kalkan / OCPP energy metering and load-balancing.
2. `../talos` / Talos edge (`tedge`) / JCWElec CST18D listening / Ender /
   Catpower screwdriver / robotics.
3. Simkab robotics / machine cells / Fairino / Ender / screwdriver.

Important product principles from user:

- V0 should not use a custom PCB.
- Keep Raspberry Pi GPIO clean where possible.
- GPIO is allowed only as internal fallback if unavoidable.
- Prefer USB peripherals, terminal blocks, DIN rail wiring, labeled pigtails.
- Talos and Yaver are interoperable peer projects, but both must work alone.
- Existing SD card is OK for bring-up, but not final logging/capture storage.
- Secondary RS485 is useful but not a V0 blocker.

## Privacy / Safety Note

The user pasted order confirmations containing personal address/phone/email.
Those details were intentionally not copied into repo files. Only technical parts
and prices were recorded.

## Current Owned / Purchased Hardware

Already owned or purchased:

- Raspberry Pi 4.
- Mean Well EDR-120-24 DIN 24 V PSU.
- Raspberry Pi 4 5 V adapters.
- Industrial USB-RS485 converter.
- PZEM-6L24 3-phase power monitor with split CT.
- MCP3008 ADCs.
- 10K resistors.
- 100 nF capacitors.
- Breadboards.
- BTS7960B / IBT-2 20 A motor driver.
- 2200 uF 50 V electrolytic capacitors.

Important safety boundaries:

- PZEM / mains / 3-phase wiring must be enclosed, fused, strain-relieved, and
  done as qualified electrical work.
- Breadboards are only for low-voltage experiments, not 24 V, motor current,
  RS485 field wiring, or mains.
- BTS7960 is for the Catpower screwdriver V0 path only, not arbitrary machinery.
- BTS7960 max supply is not a 36 V battery path; use 24 V from EDR-120-24.
- Add fused 24 V feed, physical kill switch, and 1000-2200 uF / 50 V bulk cap
  across BTS7960 B+ / B-.

## Main Yaver.io Files Changed / Added

Modified:

- `desktop/agent/ops_box.go`
  - Added `box_profiles`, `box_profile_plan`, `box_bom`.
  - Profiles include Kalkan/OCPP, Talos screwdriver, Ender/Marlin, Fairino,
    Simkab robotics/machine, JCWElec CST18D, Yuanhan YH8030H, robotics bench,
    Schleuniger observe.
  - Added peer-interoperability language: Yaver-alone, Talos/tedge-alone,
    interop through explicit port ownership.

- `desktop/agent/ops_box_test.go`
  - Added tests for profiles, aliases, BOM catalog/filtering, and
    interoperability string.

- `firmware/screwcell/README.md`
  - Marked USB companion path as preferred V0 boxed path.
  - Pi-GPIO `screwdriver_control.py` remains fallback.

Added:

- `docs/yaver-box-platform-soc-bom-analysis-2026-06-18.md`
  - Platform/BOM analysis: CM5 first, China RK/Radxa/Orange Pi track, RevPi /
    Seeed reference, x86 Max, RISC-V research.

- `docs/yaver-box-lite-rpi4-usb-prototype-analysis-2026-06-18.md`
  - Raspberry Pi 4 USB prototype analysis.
  - Explicit no-custom-PCB rule.
  - Yaver-alone / Talos-alone / interop rule.
  - `tedge` compatibility notes.
  - Existing purchased inventory and rough remaining cost estimates.

- `docs/yaver-box-v0-three-project-challenge.md`
  - First challenge: one physical RPi4 box for JCWElec, OCPP/Kalkan, and Simkab
    robotics.
  - Checked Talos sources including `../talos/edge`.
  - Adds `tedge` compatibility contract.
  - Explicit no-custom-PCB rule.
  - Explicit Yaver/Talos peer interoperability rule.
  - Buy list for cable-bundle simplification.

- `hardware/yaver-box-lite-v0/`
  - `README.md`
  - `base_plate.scad`
  - `terminal_panel.scad`
  - `usb_adapter_clip.scad`
  - `lab_tray.scad`
  - This is a printable internal mounting/label system, not a certified
    enclosure.

- `firmware/screwcell/usb_companion/`
  - `README.md`
  - `screwdriver_companion_arduino.ino`
  - Arduino Nano/Uno style USB serial companion for BTS7960/Catpower.
  - Commands: `PING`, `STATUS`, `ENABLE`, `DISABLE`, `BRAKE`,
    `DRIVE <dir> <duty_pct> <max_ms>`, `CALIBRATE`, `LIMIT <amps>`.

## Verification Done

Passed earlier:

```bash
cd desktop/agent
go test . -timeout 180s -run 'Test(Box|Modbus|ParseKV)' -count=1
```

Passed after latest ops changes:

```bash
cd desktop/agent
go test . -timeout 60s -run 'TestBox(Profile|BOM)' -count=1
```

One broader focused run hung and was stopped:

```bash
go test . -timeout 180s -run 'Test(Box|Modbus|ParseKV)' -count=1
```

It was stopped by killing the specific `go test` process. Do not count that run
as a pass.

OpenSCAD renders were previously successful to `/tmp/yaver-box-lite-v0-stl` for
the V0 mechanical files after Catpower updates.

## Talos Work Started

Modified in `../talos`:

- `edge/README.md`
  - Added link to `../docs/yaver-box-for-talos-edge-seed.md`.

Important issue:

- I attempted to add `../talos/docs/yaver-box-for-talos-edge-seed.md` with the
  patch tool, but it did not land in the Talos repo. The link now points to a
  missing file. Next agent should create that file or remove the link.

Intended Talos doc content:

- Yaver Box as Talos "Swiss army" edge box.
- `tedge` install steps:
  - apt repo install;
  - fallback `install.sh`;
  - Homebrew dev install.
- `tedge` modes:
  - `cst18d`;
  - `ocpp`;
  - `ender`;
  - `screwdriver`.
- No-custom-PCB V0 rule.
- Yaver/Talos peer modes:
  - Talos-alone;
  - Yaver-alone;
  - interop.
- Stable `/dev/serial/by-id/...` configs.
- Systemd template:
  - `tedge@cst18d`;
  - `tedge@ocpp`;
  - `tedge@ender`;
  - `tedge@screwdriver`.
- Warning that current `tedge@screwdriver` expects pigpiod/GPIO; Yaver Box V0
  hardware should prefer USB companion, so `tedge` needs a future
  `driver:"usb_companion"` backend.

## Recommended Next Buys To Avoid Raspberry Pi GPIO

Highest priority:

1. USB companion board for Catpower/BTS7960:
   - Arduino Nano/Uno compatible, or Raspberry Pi Pico/Pico H.
   - Prefer pre-soldered headers for speed.
   - This owns PWM, direction, braking, soft-start, watchdog.

2. Powered USB hub:
   - known-good, stable under camera + serial adapters.
   - avoid backfeeding the Pi.

3. USB panel-mount extensions:
   - Ender USB;
   - camera USB;
   - screwdriver companion USB.

4. Cable/harness simplification:
   - ferrule crimper kit;
   - DIN terminal blocks;
   - WAGO 221 assortment;
   - heat-shrink/wrap labels;
   - cable glands;
   - braided sleeve / spiral wrap.

5. 24 V -> 5 V USB-C buck for final one-supply box:
   - for bench, current Pi adapter is OK;
   - for box, use tested buck, no random GPIO 5 V feed.

6. USB camera:
   - do not defer camera; it is needed for Talos/Yaver robotics/machine observe.

Can wait:

- second isolated USB-RS485;
- USB-RS232;
- USB-CAN/CAN-FD;
- Modbus DI/DO module;
- LTE modem;
- industrial HAT;
- custom PCB.

Specific direction:

- Do not buy FT232H/MCP2221 as the primary screwdriver controller. They are fine
  for simple USB GPIO/I2C/SPI, but not ideal for local motor ramp/brake/watchdog.
- Do not use Pi GPIO field terminals.
- Use USB-RS485 for OCPP/JCWElec.
- Use USB serial for Ender/Marlin.
- Use Ethernet/API for Fairino.
- Use USB companion for Catpower/BTS7960.

## Suggested Talos Doc Skeleton To Add

Create in `../talos/docs/yaver-box-for-talos-edge-seed.md`:

```md
# Yaver Box For Talos Edge Seed

Status: seed runbook, written 2026-06-19.

Purpose: reusable Raspberry Pi industrial IoT box for Talos `tedge`, usable as
Talos-alone, Yaver-alone, or explicit interop.

## Rules

- no custom PCB in V0
- no raw Pi GPIO to field terminals
- stable `/dev/serial/by-id/...`
- one owner per serial path
- no secrets in git

## Hardware

Pi 4 + 24 V DIN PSU + USB-RS485 + USB camera + Ender USB + USB companion for
Catpower/BTS7960 + DIN terminals + labels.

## Install tedge

```bash
curl -fsSL https://talos.works/edge/apt/talos.gpg \
  | sudo tee /usr/share/keyrings/talos.gpg >/dev/null
echo "deb [signed-by=/usr/share/keyrings/talos.gpg] https://talos.works/edge/apt stable main" \
  | sudo tee /etc/apt/sources.list.d/talos.list
sudo apt update
sudo apt install tedge
tedge --doctor
```

## Systemd

```bash
sudo install -d -m 0755 /etc/talos-agent /var/lib/talos
ls -l /dev/serial/by-id/
sudo nano /etc/talos-agent/edge-cst18d.json
sudo systemctl enable --now tedge@cst18d
sudo journalctl -u tedge@cst18d -f
```

## Modes

- `cst18d`: USB-RS485 read-only JCWElec
- `ocpp`: USB-RS485 PZEM/meter/charger
- `ender`: USB serial Marlin + camera
- `screwdriver`: current GPIO fallback; future USB companion backend
```

## Git / Commit State

No commit or push was done.

Yaver repo has many pre-existing unrelated dirty/untracked files. Do not revert
anything unrelated.

Talos repo also has many pre-existing unrelated dirty files. Only touched
`edge/README.md`, and intended-but-missing doc should be added.

## Immediate Next Steps For opencode/glm

1. In `../talos`, create `docs/yaver-box-for-talos-edge-seed.md`.
2. Keep the `edge/README.md` link only if the file exists.
3. Optionally patch `tedge` later to support screwdriver
   `driver:"usb_companion"`.
4. In Yaver, consider adding future ops:
   - `box_tedge_status`;
   - `box_software_mode`;
   - port ownership conflict detection.
5. Buy the USB companion board + cable harness parts before more protocol
   adapters.
