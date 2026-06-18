# Yaver Box Platform, SoC, And BOM Analysis

Status: decision memo, written 2026-06-18.

Scope: Yaver Box as an industrial IoT edge box: Modbus/RS485, RS232, CAN/CAN-FD,
Ethernet, DI/DO, analog, cameras, robotics IO, OCPP/energy, Talos/machine APIs,
and Yaver mesh/ops on top. This is not a Wi-Fi repeater project.

Prices are approximate street prices checked on 2026-06-18. They will move,
especially Raspberry Pi and DRAM-heavy SKUs.

## 1. Short Answer

Use Raspberry Pi Compute Module 5 for the first serious Yaver Box software and
field prototype, but do not bet the company on Raspberry Pi only.

Recommended split:

1. Prototype 0 / internal Yaver Box Max:
   - Raspberry Pi CM5 or RevPi/Seeed-style industrial Pi platform.
   - Reason: fastest Linux bring-up, best community, easiest camera/USB/serial
     support, better chance of stable Yaver image and customer debugging.

2. China-cost/performance track:
   - Rockchip RK3588S or RK3576 module: Radxa CM5, Orange Pi CM5, Banana Pi
     BPI-CM5 Pro / ArmSoM-CM5.
   - Reason: much stronger CPU/NPU per dollar, Shenzhen supply chain, better
     local-AI/video headroom, cheaper sellable BOM.
   - Risk: kernel/BSP quality, long-term availability, industrial certification,
     carrier-board support, support burden.

3. Premium industrial/certified track:
   - RevPi Connect 5, OnLogic Factor, Seeed reComputer R-series.
   - Reason: enclosure, DIN rail, 24 V input, approvals, industrial IO already
     solved.
   - Risk: high COGS, less control over enclosure/brand, product becomes a
     repackaged gateway if Yaver software is not clearly superior.

The first Yaver Box should be intentionally over-capable. The production Yaver
Box can split into:

- Yaver Box Lite: Raspberry Pi CM5 or RK3576, 1-2 RS485, Ethernet, DI/DO.
- Yaver Box Pro: CM5/RK3588S, 2-4 RS485, RS232, CAN-FD, DI/DO, analog, LTE,
  NVMe, camera.
- Yaver Box Max: x86 N100/N305 or RK3588S with NPU, multi-camera, NVMe, LTE/5G,
  SDR/LoRa optional, local AI.

## 2. Product Positioning

Yaver Box should be closer to an upgraded Revolution Pi than to a Raspberry Pi
project:

- industrial edge Linux box;
- mobile claim/provisioning;
- Yaver mesh and remote ops;
- Modbus/RS485 gateway and sniffer;
- machine API wrapper;
- OCPP/Kalkan energy gateway;
- Talos screwdriver/robotics sidecar;
- observe-first retrofit box for JCW, YH, Schleuniger, Simkab machinery;
- normal-person troubleshooting box for car/boat/home sensors where safe.

Yaver's differentiator is not the SoC. The differentiator is the capability
surface:

```text
physical interfaces
  -> stable hardware inventory
  -> box_profiles / machine_* / robot_* / netcapture_* / ops
  -> mobile/web console
  -> remote engineer or AI agent
  -> safe observe/read first, gated writes later
```

## 3. Platform Candidates

### Raspberry Pi CM5

Use when software velocity and ecosystem matter more than raw performance per
dollar.

Strengths:

- mature Debian/Raspberry Pi OS base;
- huge community and driver ecosystem;
- camera, USB, GPIO, serial, HAT, and CM carrier ecosystem;
- CM5 options include Wi-Fi/Bluetooth, 2/4/8/16 GB RAM, and 0/16/32/64 GB eMMC;
- Raspberry Pi states CM5 is available from $45, though practical industrial
  SKUs are much higher;
- RevPi Connect 5 gives a certified industrial CM5 path with availability
  guarantee language until at least 2036.

Weaknesses:

- not the cheapest anymore;
- high-memory Pi pricing is volatile in 2026 because of memory shortages;
- NPU requires add-on accelerator;
- CM carrier design still needs careful power, thermal, IO isolation, and EMC.

Verdict:

Use CM5 for the first internal Yaver Box image and customer-visible prototype.
It is the safest engineering base.

### RevPi Connect 5

Use when industrial credibility matters immediately.

Strengths:

- CM5-based industrial PC;
- 24 V industrial environment target;
- EN 61131-2 positioning;
- eMMC;
- optional RS485 and CAN-FD;
- modular RevPi expansion family;
- price list shows Connect 5 variants roughly in the €536-€622 base range,
  with RAM surcharges listed separately; shop page shows as-low-as €571 before
  VAT / €679.49 incl. VAT style pricing.

Weaknesses:

- expensive for Yaver-owned BOM;
- limited differentiation if we only reskin it;
- expansion modules add cost quickly.

Verdict:

Buy one. Use it as the industrial reference target and comparison baseline, not
as the only Yaver Box product.

### Seeed reComputer R1000 / R1100

Use when we want cheap industrial Pi gateway hardware without designing a board.

Strengths:

- CM4-based industrial IoT gateway/controller;
- R1000 has dual Ethernet and 3 isolated RS485 channels;
- R1100 class has 2 Ethernet, 2 USB, 2 RS485, 2 RS232, 2 DI, 2 DO, optional
  LTE/LoRa/Wi-Fi/BLE/Zigbee, and NVMe slot depending on variant;
- public writeups put R1000 launch around $209 for a 4 GB / 32 GB CM4 unit;
- R11 pricing appears around $179-$279 depending on model/vendor.

Weaknesses:

- CM4, not CM5;
- less headroom for local AI/video than CM5/RK3588S;
- supply/configuration variants need validation.

Verdict:

Buy one R1100-style unit for Kalkan/OCPP and Modbus demos. It may be the fastest
low-cost DIN-rail demo box.

### Rockchip RK3588S: Radxa CM5 / Orange Pi CM5

Use when China supply chain, performance, local AI, and cost matter.

Strengths:

- 8-core CPU class: 4x Cortex-A76 + 4x Cortex-A55;
- integrated GPU and typically 6 TOPS NPU on RK3588S modules;
- Orange Pi CM5 public prices have been reported around:
  - 4 GB RAM + 32 GB eMMC: about $86.99;
  - 8 GB RAM + 32 GB eMMC: about $107.99;
  - 16 GB RAM + 32 GB eMMC: about $139.99;
- some retailers/listings show Orange Pi CM5 globally from about $70;
- Radxa CM5 has RK3588S/RK358x, LPDDR4X, eMMC, and small 55-56 mm class module
  footprint;
- better video/NPU headroom than CM5 for vision-heavy robotics.

Weaknesses:

- vendor kernels and NPU toolchains can be a maintenance tax;
- long-term supply and industrial lifecycle are less predictable than Pi/RevPi;
- carrier-board compatibility claims must be verified electrically, not assumed;
- fewer mainstream industrial certifications.

Verdict:

Start this as the China-performance track immediately, but keep it behind the
CM5 software target until the image, kernel, serial, CAN, camera, watchdog, and
OTA story are proven.

### Rockchip RK3576: Banana Pi BPI-CM5 Pro / ArmSoM-CM5

Use when RK3588S is too expensive/powerful but CM5 is not enough.

Strengths:

- quad Cortex-A72 + quad Cortex-A53 class;
- 6 TOPS NPU;
- up to 16 GB memory and up to 128 GB eMMC in published specs;
- CM4-like module direction;
- reported preorder/starting prices around $103+.

Weaknesses:

- newer and less field-proven;
- BSP/support risk remains;
- lower performance than RK3588S.

Verdict:

Good candidate for Yaver Box Lite/Pro China SKU after RK3588S validation. Do not
make it Prototype 0.

### x86 N100 / N305 Industrial Mini PC

Use for Yaver Box Max when local AI/video/dev workloads are more important than
GPIO elegance.

Strengths:

- normal Linux, Docker, browsers, local models, Playwright, video pipelines;
- NVMe and RAM are easy;
- good for remote debugging, robotics vision, Talos dev box, OCPP lab box;
- cheap mini PCs and industrial fanless boxes are widely available.

Weaknesses:

- no native Pi-style IO ecosystem;
- needs USB/PCIe modules for RS485/CAN/DI/DO;
- higher idle power;
- more expensive if industrial/certified.

Verdict:

Use for Yaver Box Max/lab. Not ideal as the universal mass-market DIN-rail box.

### RISC-V: Milk-V Jupiter / Jupiter NX

Use for research and brand differentiation, not first product.

Strengths:

- interesting open-ISA story;
- low-cost boards exist;
- Milk-V Jupiter advertises octa-core RISC-V and 2 TOPS AI class performance;
- Jupiter NX targets a module form factor with Wi-Fi/BT and eMMC.

Weaknesses:

- weaker industrial Linux ecosystem;
- more toolchain friction;
- more risk for camera/NPU/video/driver support;
- not where Yaver should spend first product energy.

Verdict:

Buy only if we want an experimental Yaver Box image target. Do not base the main
product on it yet.

## 4. Recommended Prototype BOM

### Prototype 0: Yaver Box Max Bench Rig

Goal: learn quickly, support every first use case, no custom PCB.

Compute:

- Raspberry Pi CM5, 8 GB or 16 GB, 32/64 GB eMMC, Wi-Fi optional;
- CM5 IO/carrier with PCIe/NVMe, RTC, watchdog if possible;
- NVMe SSD 256 GB or 512 GB;
- active/passive heatsink depending enclosure.

Industrial IO:

- 2x isolated USB-RS485 adapters;
- 1x 2-channel or 4-channel RS485 adapter if multiple buses matter;
- 1x isolated USB-RS232 adapter;
- 1x isolated USB-CAN/CAN-FD adapter;
- 1x Modbus RTU relay/DI module, 8 relay or 8 DI/DO class;
- optional 0-10 V / 4-20 mA Modbus analog module;
- terminal blocks for RS485 A/B/GND/shield;
- switchable 120 ohm termination and biasing per RS485 port;
- labeled pigtails for common machine/meter connectors.

Connectivity:

- dual Ethernet preferred;
- Wi-Fi/BLE;
- LTE modem via USB or mPCIe/M.2 for field deployments;
- optional LoRa/sub-GHz only after core Modbus/OCPP works.

Power/enclosure:

- 24 V DIN-rail power input;
- Mean Well HDR-60-24 or similar 24 V supply for bench;
- 24 V to 5 V buck sized for CM5 + USB devices;
- fuse per external power rail;
- DIN rail or bench enclosure;
- clear labels and strain relief.

Approximate cost:

| Area | Low | Practical | Notes |
| --- | ---: | ---: | --- |
| CM5 compute + carrier + cooling | $120 | $250-$350 | depends heavily on RAM/eMMC and current Pi price volatility |
| NVMe/storage | $20 | $35-$60 | 256-512 GB |
| RS485/RS232/CAN adapters | $50 | $120-$220 | isolated multi-channel costs more |
| DI/DO/relay/analog modules | $30 | $80-$180 | Waveshare/Chinese industrial modules are cheap and good for lab |
| Power/enclosure/terminal blocks | $60 | $150-$300 | industrializing costs more than compute |
| LTE/radio options | $0 | $60-$180 | optional |
| Total bench rig | $280 | $700-$1,300 | wide because this is intentionally over-capable |

### Prototype 1: DIN-Rail Demo Box

Goal: demo OCPP/Kalkan, Modbus meters, and machine readout quickly.

Option A:

- Seeed reComputer R1100/R1000 class.
- Add LTE if needed.
- Add second USB-RS485 only if built-in ports are not enough.

Approximate cost: $200-$450 depending model/options.

Option B:

- RevPi Connect 5 with RS485 or RS485+CAN-FD.
- Add RevPi IO modules as needed.

Approximate cost: €536-€622+ base before expansion and taxes, depending variant.

### Prototype 2: China Performance Box

Goal: validate cheaper/faster sellable hardware.

- Orange Pi CM5 or Radxa CM5, 8/16 GB;
- carrier with Ethernet, USB, PCIe/NVMe, MIPI CSI if needed;
- same external isolated USB industrial interfaces as Prototype 0;
- later migrate to custom carrier only after software is stable.

Approximate cost:

- module: about $70-$140 public reported range for Orange Pi CM5-class SKUs;
- carrier: $20-$80 typical dev-board class;
- industrial IO/enclosure: same as above.

## 5. Interface Requirements

Minimum Yaver Box Pro should expose:

- 2x isolated RS485:
  - Modbus RTU, meters, chargers, PLCs, wire machines;
  - switchable termination;
  - biasing;
  - labeled A/B/GND/shield;
  - read-only tap mode where feasible.
- 1x RS232:
  - legacy machines, scales, labelers, HMIs.
- 1x CAN/CAN-FD:
  - automotive, elevators where legally/ethically allowed, machinery, BMS.
- 2x Ethernet:
  - one plant/network side, one machine/charger/local side.
- 4-8x DI:
  - dry contact/status, limit switches, alarm relays.
- 2-4x DO:
  - non-safety relay signaling, reset/request lines, pilot outputs.
- 2-4x analog inputs:
  - 0-10 V and/or 4-20 mA sensors.
- USB:
  - camera, serial adapters, SDR, debug.
- Camera:
  - USB camera first, MIPI CSI later if carrier supports it.
- Storage:
  - eMMC for OS, NVMe for logs/captures/video.
- Watchdog/RTC:
  - mandatory for field use.
- Secure element/TPM:
  - nice-to-have for production claim identity.

Avoid in v0:

- custom mains control;
- safety PLC behavior;
- deterministic motion control;
- large custom PCB;
- fieldbus master claims beyond what we can test.

## 6. China/SZ Supply Chain Strategy

The Chinese ecosystem is a feature, not just a cheap-source option.

Use it for:

- fast module alternatives: Orange Pi, Radxa, Banana Pi, ArmSoM;
- isolated interface modules: Waveshare, Seeed, industrial Modbus IO boards;
- custom enclosure and harness work;
- low-cost camera, LTE, LoRa, USB hub, terminal-block modules;
- quick DFM iteration once the first wiring map is stable.

Do not let it control:

- the only supported OS image;
- the only kernel/BSP path;
- product safety claims;
- remote update reliability;
- secret provisioning/identity hardware without audit.

Procurement rule:

- every Chinese module must pass a Yaver image test matrix:
  - cold boot;
  - watchdog reboot;
  - Ethernet under load;
  - USB serial under load;
  - RS485 Modbus RTU sustained;
  - CAN/CAN-FD loop test;
  - camera capture;
  - LTE reconnect;
  - 72-hour soak;
  - OTA update and rollback;
  - power-cut recovery.

## 7. Software Implications

The hardware should report a stable capability manifest. Linux device paths are
not a product API.

Yaver Box should normalize:

```json
{
  "box": {
    "platform": "cm5|rk3588s|rk3576|x86",
    "sku": "box-pro",
    "serial": "..."
  },
  "interfaces": [
    {"id": "rs485-a", "path": "/dev/serial/by-id/...", "protocols": ["modbus_rtu"], "isolated": true, "termination": "switchable"},
    {"id": "can-a", "path": "can0", "protocols": ["can", "can_fd"], "isolated": true},
    {"id": "di-1", "protocols": ["digital_input"]},
    {"id": "camera-main", "path": "/dev/video0"}
  ],
  "profiles": ["kalkan-ocpp-load-balancer", "jcwelec-cst18d", "talos-screwdriver-cell"]
}
```

Existing ops already align with this:

- `box_profiles`, `box_profile_plan`;
- `box_status`, `box_selftest`, `box_autoconnect`;
- `machine_ports`, `machine_sniff_start`, `machine_scan_registers`,
  `machine_connect`, `machine_read_tags`, `machine_submit_job`;
- `robot_status`, `robot_config_set`, `robot_snapshot`, `robot_jog`,
  `robot_screw`, `robot_run`;
- `netcapture_*` for serial/network captures.

Missing software to prioritize:

1. `box_inventory`: detect platform, serial adapters, CAN, cameras, LTE,
   storage, power state.
2. `box_interface_test`: loopback/known-device tests per port.
3. `box_modbus_scan`: guided RTU/TCP scanner with safe rate limits.
4. `box_ocpp_bridge`: package wrapper around `../ocpp/kalkan`.
5. `box_recipe`: saved per-customer interface/profile wiring plans.
6. `box_update`: OTA/update channel with rollback.
7. `box_logs_bundle`: field support bundle without secrets/customer data.

## 8. Decision Matrix

| Platform | Prototype speed | Industrial credibility | Cost | Performance | Local AI/video | Software risk | Recommendation |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Raspberry Pi CM5 custom/carrier | High | Medium | Medium | Medium | Medium | Low | Main Prototype 0 |
| RevPi Connect 5 | High | High | High | Medium | Medium | Low | Buy reference unit |
| Seeed R1000/R1100 | High | Medium | Low/Medium | Low/Medium | Low | Low/Medium | Fast DIN demo |
| Radxa/Orange Pi RK3588S | Medium | Low/Medium | Low | High | High | Medium/High | China performance track |
| Banana Pi/ArmSoM RK3576 | Medium | Low/Medium | Low | Medium/High | Medium/High | Medium/High | Later Lite/Pro candidate |
| x86 N100/N305 | High | Medium | Medium | High | High | Low | Max/lab SKU |
| RISC-V Milk-V | Low/Medium | Low | Low | Medium | Low/Medium | High | Research only |

## 9. My Recommendation

Buy/build three boxes now:

1. Yaver Box Max Bench:
   - CM5 8/16 GB, 32/64 GB eMMC, NVMe, industrial USB IO modules, DIN power,
     over-capable enclosure.
   - This becomes the main Yaver image and ops development target.

2. Yaver Box DIN Demo:
   - Seeed R1100/R1000 or RevPi Connect 5.
   - This is for Kalkan/OCPP, meters, Modbus, and customer-facing demos.

3. Yaver Box China Track:
   - Orange Pi CM5 or Radxa CM5 RK3588S.
   - Same software, same ops, same interface tests.
   - Do not promise production until the soak matrix passes.

Do not design a custom PCB until:

- `box_inventory` works;
- Yaver image is reproducible;
- at least two platform families pass the same test matrix;
- Kalkan/OCPP and Talos/machine demos both run from the same capability model;
- we know which interfaces are actually used by customers.

## 10. Source Anchors Checked

- Raspberry Pi Compute Module 5 product page:
  https://www.raspberrypi.com/products/compute-module-5/
- Raspberry Pi CM5 launch/pricing note:
  https://www.raspberrypi.com/news/compute-module-5-on-sale-now/
- RevPi Connect 5 product/docs/prices:
  https://revolutionpi.com/en/docs/revpi-connect-5
  https://revolutionpi.com/en/ordering/overview-products-and-prices
  https://revolutionpi.com/shop/en/revpi-connect-5
- Seeed reComputer R1000/R1100:
  https://wiki.seeedstudio.com/recomputer_r/
  https://wiki.seeedstudio.com/recomputer_r1100_intro/
- Radxa CM5:
  https://docs.radxa.com/en/som/cm/cm5
  https://www.radxa.com/products/cm/cm5/
- Orange Pi CM5:
  https://www.orangepi.org/html/hardWare/computerAndMicrocontrollers/details/Orange-Pi-CM5.html
- Banana Pi BPI-CM5 Pro:
  https://docs.banana-pi.org/en/BPI-CM5_Pro/BananaPi_BPI-CM5_Pro
- Milk-V Jupiter / Jupiter NX:
  https://milkv.io/jupiter
  https://milkv.io/jupiter-nx
- OnLogic Factor 200:
  https://www.onlogic.com/store/computers/industrial/fanless/factor-200/
- Waveshare industrial adapters/modules:
  https://www.waveshare.com/usb-to-rs485.htm
  https://www.waveshare.com/usb-to-2ch-rs485.htm
  https://www.waveshare.com/usb-can-fd.htm
  https://www.waveshare.com/modbus-rtu-relay.htm
- Mean Well HDR-60-24:
  https://www.meanwell.com/Upload/PDF/HDR-60/HDR-60-SPEC.PDF
