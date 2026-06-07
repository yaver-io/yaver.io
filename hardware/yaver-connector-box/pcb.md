# Yaver Connector Box — PCB fabrication spec (Rev B)

Implements `schematic.md`. Outline matches `enclosure.scad` (interior 104×84 mm →
board **100 × 80 mm**). Target fab: **JLCPCB 4-layer, ENIG**, economic assembly.

## Board outline & mounting

- Outline: 100 × 80 mm, 3 mm corner radius.
- 4× M3 mounting holes at (4,4), (96,4), (4,76), (96,76) — match `holes[]` in `enclosure.scad`.
- Connector edges:
  - **FRONT edge (y=0):** USB-C (J3) @ x≈22, USB-A (J4) @ x≈48, antenna SMA/u.FL (J5) @ x≈86.
  - **BACK edge (y=80):** RS485 term (J6) @ x≈18, RS232 term (J7) @ x≈40, DC jack (J1)/power (J2) @ x≈84. CAN (J8) optional between J7 and J1.
- Keep screw-terminal bodies flush to the edge so they protrude through the wall cutouts.

## Stackup (4-layer)

| Layer | Use |
|---|---|
| L1 (top) | components + signal; USB diff pairs; ESP32 RF keepout |
| L2 | solid **GND** plane (reference for USB + RF) |
| L3 | power: `+5V`, `+3V3`, `VBUS_PD` pours |
| L4 (bottom) | signal + screw-terminal fanout; isolated-island copper |

## Critical routing rules

1. **USB 2.0 differential pairs** (`UHUB_DP/DM`, `ESP_DP/DM`, `FT_DP/DM`, `UA_DP/DM`):
   90 Ω differential, length-matched ≤ 5 mil skew, reference L2 GND, no plane splits
   underneath, USBLC6/TPD6S300 ESD right at the connectors.
2. **Galvanic isolation barrier** under `U10` (ADM2587E) and `U12` (ADM3251E):
   - **≥ 4 mm creepage** between primary (`GND`/`+5V`) and isolated islands
     (`GND_ISO485/232`, `+5V_ISO485/232`).
   - **No copper on any layer** crosses the barrier (route a clearance slot/keepout
     spanning all 4 layers; optionally a board cut/slot under the package gap).
   - Isolated screw terminals (J6/J7) sit entirely on the isolated island side.
3. **ESP32-S3 RF**: module antenna pin → u.FL (J5); full **ground keepout** under
   the module's antenna zone; do not run traces beneath. Use the `-U` (external
   antenna) module variant — factory metal/DIN boxes kill PCB-trace antennas.
4. **Power**: TPS54360 (U1) hot loop tight (Vin cap → switch → diode), thermal vias
   under PowerPAD to L2/L3. Buck-boost U4 for `VBUS_PD` likewise.
5. **PD/VBUS**: J3 VBUS and `VBUS_PD` traces sized for **3 A** (≥ 60 mil on 1 oz, or
   pour). TPD6S300 at the USB-C connector.
6. **RS485 bus**: keep `A`/`B` as a tight pair from U10→U11(swap)→J6; place R_TERM,
   bias resistors, and SM712 ESD next to J6. CAN `H/L` likewise.
7. **Star ground** for primary; single-point connect of analog INA219 shunt.

## Copper / fab

- 1 oz outer / 0.5 oz inner (1 oz inner if you push 3 A through inner pours).
- Min trace/space 6/6 mil (JLC economic); 0.3 mm vias.
- ENIG finish (fine-pitch QFN U3/U7), lead-free.
- Silkscreen: label every terminal (`A B GND`, `TX RX GND`, `CANH CANL`, `9-24V`),
  the A/B-swap DIP, termination DIP, BOOT/RESET, and a "FACADE — phone is the brain"
  note. Polarity arrow on DC jack.

## Assembly

- JLCPCB economic SMT (top side); through-hole terminals + USB-A + DC jack hand-
  or wave-soldered. ESP32-S3-WROOM-1U module reflows on top.
- Populate variants per `bom.csv` `populate` column (`full` / `wired` / `wireless` / `plus-can` / `plus-sense`).

## Bring-up checklist

1. Power only: confirm `+5V`, `+3V3`, no shorts; INA219 reads input.
2. Flash ESP32-S3 (BOOT+RESET), confirm USB-CDC enumerates on a host + SoftAP appears.
3. RS485 loopback (A→A, B→B to a USB-RS485 dongle), verify isolation (megger primary↔iso ≥ 1 kV).
4. PD source: plug a phone, confirm charging + hub enumeration (charge-while-host).
5. End-to-end: Yaver app → `netcapture` serial/net on a modbus-emu / real PLC.
