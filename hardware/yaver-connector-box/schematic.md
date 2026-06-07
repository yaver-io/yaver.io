# Yaver Connector Box — Schematic (Rev B, component-level)

This is the implementable design by functional block, with a netlist table at the
end. It's written to be transcribed 1:1 into KiCad (the `.kicad_sch` is not
hand-authored here because the S-expression format is error-prone by hand; this
spec is unambiguous enough to schematic-capture directly). Net names are UPPER_SNAKE.

Power rails: `VIN` (9–24 V) · `+5V` · `+3V3` · `VBUS_PD` (5/9/12 V to phone) ·
`+5V_ISO485` / `GND_ISO485` (RS485 isolated island) · `+5V_ISO232` /
`GND_ISO232` (RS232 isolated island). Primary ground `GND`.

---

## Block A — Power input & protection

- **J1 DC jack** (5.5×2.1 mm) + **J2** 2-pin screw terminal (parallel) → `VIN`.
- **F1** PTC resettable fuse 3 A → series in `VIN`.
- **D1** SMBJ26A TVS (VIN→GND), **D2** SS54 reverse-polarity series Schottky (or P-MOS ideal-diode).
- **C1** 100 µF/35 V + **C2** 100 nF bulk on `VIN`.

## Block B — Buck converters

- **U1 TPS54360** (4.5–60 V, 3.5 A): `VIN`→`+5V`. L1 10 µH, Cout 2×22 µF, FB divider for 5.0 V, Cboot 100 nF, freq set ~500 kHz.
- **U2 TPS563201** (or AP63203): `+5V`→`+3V3` @ 2 A for logic/ESP32.
- Test points TP_5V, TP_3V3, TP_VIN.

## Block C — USB-C upstream + PD source (charge-while-host)  *(wired mode)*

- **J3 USB-C receptacle** (16-pin, with CC1/CC2, VBUS, D+/D−, SBU).
- **U3 TPS65987DDK** USB-PD controller (source-capable):
  - CC1/CC2 → J3 CC1/CC2.
  - VBUS path: PP_HV/PP_5V switches → `J3.VBUS`. VBUS rail = `VBUS_PD` from **U4** buck-boost (**TPS55288**, `+5V`→5/9/12 V, set by U3 over I²C).
  - I²C (`SDA`,`SCL`) to ESP32 for telemetry (VBUS/IBUS) + source-PDO config.
  - Advertise PDOs: 5 V/3 A, 9 V/2 A, 12 V/1.5 A.
- **J3.D+/D−** → hub upstream (Block D, `UHUB_DP`/`UHUB_DM`).
- ESD: **U5 TPD6S300** on CC/SBU/VBUS, **U6 USBLC6-2** on D+/D−.

> Wireless-only SKU: omit Block C (J3, U3, U4, U5). Phone charges on its own brick.

## Block D — USB 2.0 hub  *(wired mode)*

- **U7 FE1.1s** 4-port USB2 hub. Upstream = `UHUB_DP/DM` (from J3).
  - 12 MHz xtal Y1 + 2×22 pF.
  - Self-powered config (VBUS_DET tie per datasheet); ports powered from `+5V`.
- Downstream ports:
  - **P1 → ESP32-S3 native USB** (`ESP_DP/ESP_DM`) — smart bridge + companion (CDC).
  - **P2 → FT232RL** (`FT_DP/FT_DM`) — raw redundant USB-serial path.
  - **P3 → J4 USB-A** host passthrough (`UA_DP/UA_DM`, `+5V`/`GND`) — 3D-printer/USB device. **U8 power switch** (e.g. MIC2005) for J4 VBUS, current-limited 0.9 A, fault → ESP GPIO.

## Block E — Core MCU / facade brain: ESP32-S3  *(both modes)*

- **U9 ESP32-S3-WROOM-1U** module (–U = u.FL external antenna; choose modular-certified part to inherit FCC/CE).
  - Power `+3V3`, bulk 22 µF + 100 nF + 10 µF.
  - EN with 10 kΩ pull-up + 1 µF (RC reset); GPIO0 to BOOT button **SW1** + 10 kΩ pull-up.
  - **USB**: `GPIO19`=D−, `GPIO20`=D+ → hub P1 (`ESP_DP/ESP_DM`) for wired CDC.
  - **u.FL J5** → external 2.4 GHz antenna (factory-metal friendly).
  - **UART1 → RS485** (`U1_TX`=GPIO17, `U1_RX`=GPIO18, `U1_DE`=GPIO16 driver-enable).
  - **UART2 → RS232** (`U2_TX`=GPIO43, `U2_RX`=GPIO44).
  - **I²C** (`SDA`=GPIO8, `SCL`=GPIO9): TPS65987D telemetry, INA219, optional sensors.
  - **A/B swap control** `AB_SEL`=GPIO10 → analog mux (Block F).
  - **Term/bias sense/control** `TERM_EN`=GPIO11, `BIAS_EN`=GPIO12 (drive load switches or read DIP).
  - **CAN** (optional) `CAN_TX`=GPIO13, `CAN_RX`=GPIO14 → Block H.
  - **Status LEDs** `LED_PWR`=GPIO2, `LED_LINK`=GPIO3, `LED_BUS`=GPIO4 (or one WS2812 on GPIO48).
  - **Force/torque sense** (companion): HX711 `DT`=GPIO5/`SCK`=GPIO6, or INA219 over I²C.

## Block F — Isolated RS485 + termination/bias + A/B swap  *(both modes)*

- **U10 ADM2587E** — isolated RS485 transceiver with **integrated isolated DC-DC**
  (no separate iso supply needed). Logic side `+5V`/`GND`; bus side `+5V_ISO485`/`GND_ISO485`.
  - `TXD`←ESP `U1_TX`; `RXD`→ESP `U1_RX`; `DE`/`/RE`←ESP `U1_DE` (tie DE=/RE for half-duplex).
  - Bus pins `Y/Z` (or `A/B`) → **U11 A/B swap**: **74HC4066 / TS3A24159** dual SPDT,
    `AB_SEL` selects straight vs crossed → terminals `RS485_A`/`RS485_B`.
  - **R_TERM 120 Ω** across A/B via **SW2 DIP** (or load switch on `TERM_EN`).
  - **Bias**: `R_BIASP 560 Ω` A→`+5V_ISO485`, `R_BIASN 560 Ω` B→`GND_ISO485`, gated by `BIAS_EN`/DIP.
  - Bus ESD **D3 SM712** across A/B.
- **J6** 3-pin 3.5 mm screw terminal: `RS485_A`, `RS485_B`, `GND_ISO485`.

## Block G — Isolated RS232  *(both modes)*

- **U12 ADM3251E** — isolated RS232 transceiver (integrated iso DC-DC). Logic `+5V`;
  bus side isolated. `T1IN`←ESP `U2_TX`, `R1OUT`→ESP `U2_RX`.
- **J7** 3-pin 3.5 mm: `RS232_TX`, `RS232_RX`, `GND_ISO232`. (Optional DE-9 footprint J7b.)

## Block H — CAN  *(optional populate)*

- **U13 SN65HVD230** (3.3 V CAN), `D`←ESP `CAN_TX`, `R`→ESP `CAN_RX`, Rs→GND.
- `R_CANTERM 120 Ω` via **SW3**. **J8** 2-pin: `CANH`, `CANL`. (Not isolated by default; add ISO1042 variant for isolated CAN.)

## Block I — Telemetry / power deep-analysis

- **U14 INA219** on `VIN` shunt (0.01 Ω, R_SHUNT) → I²C → ESP reports input V/I.
- TPS65987D I²C exposes VBUS/IBUS to the phone (charging analytics).

## Block J — Status & controls

- **LED_PWR** (green) on `+3V3`. **LED_LINK** (blue, USB/Wi-Fi link). **LED_BUS** (amber, RS485/232 activity, ESP-driven).
- **SW1** BOOT, **SW4** RESET (EN), **SW2/SW3** termination DIPs, **SW5** mode hint (wired/wireless preference, ESP reads).

---

## Netlist (key nets → pins)

| Net | Connections |
|---|---|
| `VIN` | J1.+, J2.1, F1.in; F1.out→U1.VIN, D1, D2 |
| `+5V` | U1.SW(via L1/FB)→rail; U7.VBUS, U8.in, U10.Vcc, U12.Vcc, U2.VIN, J4 (via U8) |
| `+3V3` | U2.OUT; U9.3V3, U14.Vs, LED_PWR |
| `VBUS_PD` | U4.OUT → U3.PP → J3.VBUS (to phone) |
| `UHUB_DP/DM` | J3.D+/D− ↔ U7.UP_DP/DM (ESD U6) |
| `ESP_DP/DM` | U7.P1_DP/DM ↔ U9.GPIO20/GPIO19 |
| `FT_DP/DM` | U7.P2_DP/DM ↔ U11(FT232).USBDP/DM |
| `UA_DP/DM` | U7.P3_DP/DM ↔ J4.D+/D− |
| `U1_TX/RX/DE` | U9.GPIO17/18/16 ↔ U10.TXD/RXD/DE |
| `RS485_A/B` | U10.Y/Z → U11(swap) → J6.A/J6.B; R_TERM, bias, D3 |
| `U2_TX/RX` | U9.GPIO43/44 ↔ U12.T1IN/R1OUT → J7 |
| `CAN_TX/RX` | U9.GPIO13/14 ↔ U13.D/R → J8 |
| `SDA/SCL` | U9.GPIO8/9 ↔ U3, U14 (I²C, 4.7 kΩ pull-ups to +3V3) |
| `AB_SEL` | U9.GPIO10 → U11 select |
| `GND` | star ground; isolated islands `GND_ISO485`/`GND_ISO232` kept separate across the U10/U12 barrier |

## Isolation barrier

`U10` (RS485) and `U12` (RS232) define the galvanic barrier. Keep `GND_ISO*` and
`+5V_ISO*` on their own copper islands with **≥ 4 mm creepage** to `GND`/`+5V`. No
traces, planes, or stitching cross the barrier except the transceiver bodies. See
`pcb.md`.
