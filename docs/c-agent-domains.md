# C-Agent Domains

## Status

This document is part of the c-agent design baseline, which is an
**additional Yaver surface, not a pivot**. The dev-machine agent
remains Yaver's primary product. C-agent is an opt-in side project
that, if developed, would extend Yaver's reach into IoT / industrial
troubleshooting; this document is the application-side reference for
that work. It is not a focus area and should not be read as a roadmap
commitment.

## What this document covers

This is the application-side companion to
[`c-agent-architecture.md`](./c-agent-architecture.md). The architecture
doc describes the runtime — a tiny on-device loader that runs signed,
sandboxed code modules shipped from a cloud reasoning brain. This
document describes *where the runtime is worth deploying*: classes of
device with rich, complex, C-level internal state that today is
debugged by humans reading raw logs, by truck rolls, by vendor support
escalation, or not at all.

The unifying property of every device class below:

- significant Linux / RTOS / firmware complexity
- expensive, slow, or impossible local UI
- failure modes that depend on correlating multiple subsystems
- field-deployed in physically inconvenient locations
- an established log / control-plane surface that an AI agent has
  never been given structured access to

These devices already have the answer to their own problems written
inside them — kernel ringbuffer entries, driver tracepoints, control
buses, vendor management interfaces. What's missing is a way to
iteratively *ask the device the right questions*, which is what the
c-agent + brain loop provides.

## Contents

1. [Common patterns](#1-common-patterns)
2. [EV chargers](#2-ev-chargers)
3. [Wi-Fi modems and routers](#3-wi-fi-modems-and-routers)
4. [Mesh Wi-Fi and EasyMesh](#4-mesh-wi-fi-and-easymesh)
5. [Set-top boxes, signage, smart TVs](#5-set-top-boxes-signage-smart-tvs)
6. [Industrial gateways and panel PCs](#6-industrial-gateways-and-panel-pcs)
7. [Elevator and lift controllers](#7-elevator-and-lift-controllers)
8. [HVAC and building automation](#8-hvac-and-building-automation)
9. [Solar inverters, batteries, energy controllers](#9-solar-inverters-batteries-energy-controllers)
10. [Kiosks, POS, vending, ATMs](#10-kiosks-pos-vending-atms)
11. [Access control and surveillance](#11-access-control-and-surveillance)
12. [Printers and multifunction devices](#12-printers-and-multifunction-devices)
13. [SD-WAN and branch-office gateways](#13-sd-wan-and-branch-office-gateways)
14. [Network-attached storage and backup appliances](#14-network-attached-storage-and-backup-appliances)
15. [Cross-cutting bus and protocol surfaces](#15-cross-cutting-bus-and-protocol-surfaces)
16. [What this enables that today's tools do not](#16-what-this-enables-that-todays-tools-do-not)

---

## 1. Common patterns

Every device class below has roughly the same internal shape:

```
┌──────────────────────────────────────────────────────────┐
│  Application layer                                       │
│  (vendor's product binary; usually closed source)        │
├──────────────────────────────────────────────────────────┤
│  Middleware                                              │
│  (HAL, vendor SDK, comms libraries, scheduler)           │
├──────────────────────────────────────────────────────────┤
│  OS surface                                              │
│  (Linux kernel + drivers; RTOS for deeply embedded)      │
├──────────────────────────────────────────────────────────┤
│  Hardware                                                │
│  (SoC, peripherals, buses, sensors, actuators, radios)   │
└──────────────────────────────────────────────────────────┘
```

The c-agent runs alongside the application layer and exposes a
capability-gated WASM/eBPF surface over the lower three. Brain-shipped
modules read the rich state that already exists and produce structured
evidence; remediation modules issue controlled commands through the
same surface.

The handful of capability families that show up across most device
classes:

- `fs.read.logs`, `fs.read.config`, `fs.read.proc`, `fs.read.sys` —
  read-only filesystem windows.
- `procfs.processes` — running process state.
- `netlink.route`, `nl80211.read`, `nl80211.write` — kernel network +
  wireless control.
- `service.read`, `service.restart`, `service.stop` — service manager.
- `ubus.read`, `ubus.write` — OpenWrt control bus (very common on CPE).
- `dbus.read`, `dbus.write` — desktop / embedded bus (common on
  systemd-based industrial gear).
- `serial.open` — RS-232 / RS-485 / UART access for ICS, lifts, BMS,
  inverters.
- `can.read`, `can.write` — SocketCAN access for automotive, EV, BMS,
  industrial.
- `modbus.rtu.read`, `modbus.rtu.write`, `modbus.tcp.*` — Modbus
  client.
- `bacnet.read`, `bacnet.write` — BACnet IP / MS-TP for HVAC.
- `opcua.browse`, `opcua.read`, `opcua.subscribe` — industrial.
- `mqtt.publish`, `mqtt.subscribe` — telemetry.
- `i2c.read`, `i2c.write`, `spi.transfer`, `gpio.*` — board-level.
- `ebpf.tracepoint`, `ebpf.kprobe`, `ebpf.uprobe` — Layer-2 kernel
  observability.

Per-domain capability sets are listed below. Each capability has a
strict redaction profile (see architecture doc §7.5) so vendor secrets,
PSKs, customer PII, and certificates do not leave the device.

## 2. EV chargers

### 2.1 Typical stack

| Layer | Common content |
|---|---|
| Application | OCPP 1.6 / 2.0.1 client, charge session state machine, billing/payment client |
| Middleware | RFID/NFC reader driver, payment terminal SDK (P2PE), GFCI driver, BMS comm stack |
| OS | Linux on ARM (i.MX6/8, RK33xx, BCM283x); occasionally Yocto + RT patch; sometimes a separate Cortex-M MCU runs the charge controller in hard real-time |
| Buses | CAN to BMS / inverter, RS-485 to meter, control-pilot PWM to EV, USB to RFID, Ethernet to backend |
| Hardware | Contactor relays, Type-2 / CCS / CHAdeMO connector, GFCI module, energy meter, isolation monitor, RCD |

### 2.2 Failure modes worth fixing

Recurring field issues that today require either a truck roll or hours
of vendor support:

- **OCPP backend disconnect cascades.** Charger reconnects, re-uploads
  buffered MeterValues, gets rate-limited, drops again. Logs are buried
  in vendor-specific JSONL with timestamps in mixed timezones.
- **Charge session aborts mid-charge.** Could be: EV-side BMS request,
  control-pilot resistance drift, contactor chatter, GFCI false trip,
  thermal derate. The application log usually says "session ended" with
  no cause.
- **Stuck in `Preparing` state.** Plug detected, RFID accepted, but
  contactor never closes. Usually a sequencing bug between CP signal,
  isolation check, and BMS handshake.
- **Phantom RFID failures.** Reader returns UID, backend rejects it,
  charger displays generic "auth failed." The actual reason is in an
  OCPP `Authorize.conf` payload that the user never sees.
- **GFCI false trips at specific times of day.** Often grid harmonic
  injection, sometimes a failing isolation monitor, sometimes a
  weatherproofing leak that only matters in rain.
- **Derating without explanation.** Thermal management, BMS
  request, grid-side current limit, dynamic load management — all can
  cause derating, all log differently.
- **Contactor weld detection late.** Detected only on session end;
  next session starts on a welded contact and trips the upstream
  breaker.
- **CP (control pilot) PWM duty cycle drift.** EV reads the duty cycle
  to determine offered current; a drifting duty cycle causes the EV to
  refuse charging or charge at wrong rate.
- **Payment terminal lockup.** EMV kernel hung; charger UI blames
  network.
- **Time-of-use scheduling collisions.** Smart-charge plan from
  backend conflicts with site-side dynamic load management; charger
  oscillates.

### 2.3 Why current troubleshooting is bad

- OCPP logs are vendor-specific; not parsed by anything off the shelf.
- CAN bus to BMS is invisible without an in-cabinet sniffer.
- Control-pilot timing requires either a scope or a driver that
  exposes raw PWM measurements; usually neither is accessible
  remotely.
- Power-quality issues need correlated meter data and grid-side
  evidence; the charger has both but vendor dashboards rarely surface
  them.
- Cabinet wiring varies per install; a fault that's a config problem
  on site A is a hardware problem on site B.
- Vendor field engineers reach a charger by laptop + Ethernet cable
  and read a serial console. AI does not enter the picture.

### 2.4 Iteration examples

A real session might look like:

```
operator → "EV charger #4137 keeps dropping sessions at ~80% SoC"

iter 1: probe ocpp_recent_transactions --hours=24
iter 2: probe ocpp_message_log --since=last_session_end --kind=*Stop*
iter 3: probe charge_controller_state_log --window=last_session
iter 4: probe can_bms_dump --bus=can0 --filter='id>=0x18FF50' --window=last_session
iter 5: probe contactor_event_log --window=24h
iter 6: probe isolation_monitor_history --window=7d
iter 7: probe thermal_log --window=last_session
iter 8: brain hypothesis: BMS-requested current step-down at 78% SoC
        is timing out the contactor hold-off, contactor opens, charger
        treats as fault.
iter 9: probe firmware_version_compatibility --component=bms --vehicle-make=...
remediation: apply_config_patch --key=contactor.holdoff_ms
             --from=200 --to=600
verify:    same probes after a real charge session.
```

The brain writes each probe as a small Rust → wasm32-wasi module
against the capability surface. Modules used here become library
candidates for future EV-charger incidents.

### 2.5 Capability surface

```
ocpp.read.transactions        list/read recent OCPP transactions + their state changes
ocpp.read.messagelog          OCPP message stream with structured filters
ocpp.send.diagnosticstrigger  trigger DiagnosticsStatusNotification
charge_controller.state       charge state machine snapshot + history
charge_controller.cp.pwm      control-pilot PWM duty + frequency history
contactor.events              relay open/close events with source attribution
contactor.weld_check          run weld-detection sequence
isolation.monitor             insulation resistance history
gfci.events                   ground-fault interrupter trip log
energy_meter.read             cumulative kWh + instantaneous P, Q, V, I
thermal.history               module + cabinet temp, fan PWM, derate events
can.bms.dump                  filtered CAN dump from BMS bus
can.bms.send                  controlled CAN message send (HIGH-RISK)
firmware.versions             component firmware version inventory
schedule.read                 active charging schedule + next override
config.read.cabinet           cabinet wiring profile + commissioning data
```

Remediation capabilities — gated behind operator approval signature:

```
service.restart                restart charge-controller daemon
contactor.cycle                deliberate open/close for weld test
config.patch.contactor         apply timing parameter delta
config.patch.derate            apply thermal derate threshold delta
ocpp.disconnect_reconnect      force backend reconnect
factory_reset                  full factory reset (DANGEROUS)
```

## 3. Wi-Fi modems and routers

### 3.1 Typical stack

| Layer | Common content |
|---|---|
| Application | Web UI + API, parental controls, QoS engine, captive portal, ISP management agent (TR-069 / TR-181 / USP) |
| Middleware | hostapd, wpa_supplicant, dnsmasq, conntrack, nftables, ndppd, miniupnpd |
| OS | OpenWrt 22+, RDK-B, prplOS, vendor-modified Linux on Broadcom / MediaTek / Qualcomm SoC |
| Buses | DSL/DOCSIS modem chip via vendor PHY driver, USB to LTE/5G modem, switch chip via swconfig/DSA |
| Hardware | 802.11ax/be radios (often 2.4 + 5 + 6 GHz), Ethernet switch, USB host, WAN modem chip, sometimes integrated VoIP |

### 3.2 Failure modes worth fixing

- **Random Wi-Fi disconnects.** Driver firmware crashes (`ath11k FW crashed`,
  `mt76 mac80211 RX hang`), DFS radar events on 5 GHz, beacon loss
  from a neighbor's interference, PMK cache mismatch.
- **Slow throughput on 5 GHz.** Channel utilization on the chosen
  channel is 80%+, neighbor APs occupying the same 80 MHz block,
  rate-control stuck on a conservative MCS.
- **DHCP starvation.** Lease pool exhausted; or dnsmasq stuck on a
  stale lease; or conntrack table full and dropping DHCP renewals.
- **IPv6 prefix delegation breakage.** ISP rotated PD; CPE didn't
  re-delegate to the LAN; clients show address but no default route.
- **Captive portal infinite redirect.** DNS interception conflict with
  Apple / Google connectivity probes; Hotspot 2.0 overlap; HSTS pinning.
- **Mesh backhaul instability.** Wi-Fi backhaul flaps between bands;
  agent ↔ controller heartbeat lost; STA gets steered in a loop.
- **TCP throughput collapse over Wi-Fi.** TCP SACK + bufferbloat +
  poor RTT estimation; retransmit storms on a single station.
- **Silent IPv4 NAT corruption.** conntrack races, vendor offload
  bug; some flows just hang.
- **WPA3 client incompat.** SAE handshake fails on legacy STAs that
  silently fall back nowhere.
- **DSL/DOCSIS line stats degrading.** SNR margin drops over weeks;
  CRC errors climb; ISP modem chip retrains and disconnects every
  few hours.
- **WAN modem (LTE/5G) USB reset loops.** Power-cycle → modem AT
  command timeout → modem manager force-resets → loop.
- **TR-181 datamodel out of sync.** ISP ACS pushes a config the device
  partly applied; device reports "applied" but feature isn't actually
  on.

### 3.3 Why current troubleshooting is bad

- ISP support sees only TR-069 datamodel — no kernel ringbuffer, no
  hostapd events, no driver firmware logs.
- End user sees only "Internet not working" lights.
- Wireless driver crashes are written to dmesg and erased on reboot.
- mac80211 has rich tracepoints (`wlan_*`, `tx_status`, `rx_completed`)
  that nobody attaches an eBPF probe to in production.
- Field tech tools (Wi-Fi analyzers) see the air, not the AP's
  internal scheduler decisions.
- Repeated "modem reboot" by the user erases the evidence they'd need
  to actually fix it.

### 3.4 Iteration examples

```
operator → "Customer says Wi-Fi 5 GHz is slow every evening 19:00–22:00"

iter 1: probe wifi_radio_state --band=5g
iter 2: probe channel_utilization_history --band=5g --window=24h
iter 3: probe neighbor_scan --band=5g
iter 4: probe sta_list --band=5g + sta_rate_history --window=4h
iter 5: probe acs_events_history
iter 6: probe driver_fw_log --since=24h
iter 7: probe ebpf_tx_drop_reasons --duration=60s        # Layer 2
iter 8: brain: 80 MHz overlap with two neighbor APs, ACS hasn't
        re-evaluated since boot 11 days ago; rate-control stuck on
        MCS 4 for one specific STA due to RTS/CTS exchange failures.
remediation: trigger_acs --band=5g
             + sta_rate_control_reset --mac=...
verify: rerun channel_utilization_history + sta_rate_history
```

### 3.5 Capability surface

```
nl80211.read.iface                radio + interface state
nl80211.read.station              per-STA info: rssi, rate, retries,
                                  airtime, capabilities, MCS history
nl80211.read.scan                 trigger + read scan results
nl80211.read.channel_util         channel utilization, busy time
nl80211.write.acs                 trigger automatic channel selection
nl80211.write.set_channel         force channel change (HIGH-RISK)
hostapd.ctrl.read                 hostapd ctrl-iface read commands
hostapd.ctrl.steering             BSS transition request (steer STA)
hostapd.ctrl.deauth               deauth a STA (LOW-RISK write)
wpa_supplicant.ctrl.read          STA-side control-iface read
dhcp.leases.read                  current + recent leases
dhcp.events                       DHCP server event log
nft.read                          nftables ruleset + counters
conntrack.read                    conntrack table snapshot + stats
fs.read.logs                      /var/log/**, /tmp/log/**
ebpf.tracepoint.mac80211          attach to wlan_* tracepoints
ebpf.tcp.retransmit               tap TCP retransmit kprobe
modem_manager.read                LTE/5G modem state, signal, last reset
modem_manager.reset               controlled modem reset (LOW-RISK)
dsl.line_stats                    DSL line stats: SNR, CRC, FEC, retrains
docsis.line_stats                 DOCSIS upstream/downstream stats
tr181.read                        read the TR-181 datamodel
tr181.write                       write parameters (HIGH-RISK)
```

## 4. Mesh Wi-Fi and EasyMesh

### 4.1 Typical stack

EasyMesh-compliant networks have a controller node and one or more
agent nodes, talking 1905.1 and CMDU messages over the backhaul. State
is partly in the controller, partly in each agent, partly in the air.

### 4.2 Failure modes

- **Backhaul flap loop.** Agent loses backhaul, joins via fronthaul,
  re-elects, repeats every few minutes.
- **Steering loops.** Controller steers STA to node B; node B says no
  capacity; STA bounces. End-user sees connection drops.
- **Topology view diverges.** Controller's topology is stale; an
  agent silently went offline; STAs assigned to dead AP.
- **Channel switch storms.** DFS radar event triggers switch on root;
  cascade triggers re-association on every agent.
- **Onboarding stuck.** New satellite paired but never reaches
  "operational" — usually a CMDU message lost on the fronthaul.

### 4.3 Capability surface

```
easymesh.topology.read         current network map (nodes, links, STAs)
easymesh.cmdu.log              recent CMDU messages with parsing
easymesh.controller.events     election + topology change events
easymesh.steering.history      every BSS transition request issued
easymesh.steering.stats        per-STA steering decisions + outcomes
easymesh.backhaul.metrics      RSSI / airtime / retries per backhaul link
easymesh.dfs.events            DFS-CAC + radar history per band
easymesh.controller.set_role   controller / agent role transition
easymesh.onboarding.replay     replay last onboarding sequence
```

## 5. Set-top boxes, signage, smart TVs

### 5.1 Typical stack

| Layer | Common content |
|---|---|
| Application | RDK-V, Android TV, webOS, Tizen, vendor stack; player apps |
| Middleware | gstreamer, omx-il, vendor codec libs, DRM (Widevine, PlayReady), HDCP enforcement, EAS |
| OS | Linux on Broadcom / Realtek / Amlogic / MediaTek STB SoCs |
| Buses | HDMI (TX or RX), USB, optical, ATSC/DVB tuner, Ethernet, Wi-Fi |

### 5.2 Failure modes

- **Decoder hangs after a stream change.** Hardware decoder gets stuck
  in a state from which only a power cycle recovers; vendor blob doesn't
  expose recovery API.
- **HDMI handshake regressions.** EDID negotiation succeeds but HDCP
  authentication fails on certain TVs after firmware update.
- **Audio drift over hours.** Codec clock vs system clock drift; A/V
  desyncs by 100+ ms after several hours of playback.
- **Tuner lock loss.** ATSC/DVB tuner drops lock at specific weather
  / temperature combinations; demod returns BER but app doesn't read it.
- **App launch timeouts.** GC pause in the app runtime + filesystem
  pressure; logs only say "ANR".
- **DRM provisioning failures.** OEMCrypto provisioning fails after a
  factory reset; user can't watch protected content.
- **Network stack starvation.** Multicast IGMP storm or video buffer
  pressure starves DNS lookups.

### 5.3 Capability surface

```
gstreamer.pipeline.dot         dump current pipeline as DOT graph
codec.hw.state                 vendor codec block state + last error
hdmi.edid.read                 EDID from sink + capability negotiation log
hdcp.session.state             HDCP version + auth state
dvb.frontend.stats             SNR, BER, signal strength, lock status
dvb.demod.events               demod lock/loss events
drm.provision.state            DRM provisioning state + last error
av_clock.skew_history          A vs V clock drift history
runtime.gc.history             app runtime GC pauses
ebpf.scheduler.runqueue        per-process runqueue latency
display.framebuffer.snapshot   snapshot active framebuffer (with consent)
codec.reset                    reset hardware codec (HIGH-RISK)
hdmi.renegotiate               force HDMI re-handshake (LOW-RISK)
drm.reprovision                redo DRM provisioning (HIGH-RISK)
```

## 6. Industrial gateways and panel PCs

### 6.1 Typical stack

| Layer | Common content |
|---|---|
| Application | SCADA client, HMI runtime, edge analytics, vendor PLC clients |
| Middleware | OPC-UA stack, MQTT broker, Modbus master/slave, BACnet stack, Profinet stack, EtherNet/IP, time-sync (PTP, NTP) |
| OS | Yocto-based Linux on x86 industrial SBC or ARM panel PC; sometimes Windows IoT |
| Buses | CAN, RS-485, RS-232, Profinet, EtherCAT, Ethernet/IP |

### 6.2 Failure modes

- **Modbus RTU silent corruption.** RS-485 termination wrong; bus
  noise causes intermittent CRC errors; master retries mask the
  problem until it doesn't.
- **OPC-UA subscription explosion.** Client subscribes to every node;
  publishing rate exceeds gateway CPU; samples dropped.
- **Time sync drift.** NTP unreachable; PTP master lost; data
  timestamps drift relative to plant-side controllers.
- **Profinet alarm storms.** A sensor flickers; Profinet alarm gets
  raised, acknowledged, raised again 100+ times/sec; controller chokes.
- **Container daemon OOMs the gateway.** Edge analytics container
  leaks memory; system OOM kills first the broker, then the
  scheduler, taking down the line.
- **Vendor protocol incompatibility.** PLC firmware update changes a
  field's units silently; gateway keeps reporting old units.
- **Failover doesn't.** Redundant gateway pair; primary's CPU is
  pinned; secondary thinks primary is alive because heartbeat IP-tx
  still works.

### 6.3 Capability surface

```
modbus.rtu.scan                discover slave addresses on a bus
modbus.rtu.read                read holding/input registers
modbus.rtu.write               write coil/holding (HIGH-RISK)
modbus.rtu.crc_stats            per-slave CRC error counts
modbus.tcp.connections         active Modbus/TCP connections + stats
opcua.browse                   browse address space
opcua.read                     read nodes
opcua.subscribe                subscribe with rate budget
opcua.session.stats            session + subscription counters
profinet.alarm_log             alarm history with deduplication
profinet.diag                  diagnostic record fetch
ethercat.slave_state           slave state machine snapshot
mqtt.broker.stats              broker connections, retained, queue depth
mqtt.subscribe.replay          replay recent retained messages
ptp.state                      PTP grandmaster/slave state, offset, delay
ntp.peers                      NTP peer table + reachability bits
container.runtime.snapshot     running containers + cgroup pressure
```

## 7. Elevator and lift controllers

### 7.1 Typical stack

Mostly safety-rated controllers (SIL, EN 81-20/50). The cabin
controller is real-time on a vendor MCU. The building controller is
often a Linux box that bridges between the cabin, the dispatcher,
emergency systems, and a remote-monitoring backend.

| Layer | Common content |
|---|---|
| Building gateway | Linux + vendor monitoring agent + remote-access tunnel |
| Buses | CANopen / proprietary CAN, RS-485 to dispatcher, Modbus to door operator, vendor proprietary serial |
| Sensors | Position encoder, door zone, weight sensor, governor, brake monitor |
| Actuators | Hoist motor (VFD-driven), door operator, brake, safety chain |

### 7.2 Failure modes

- **Door reversals.** Photoeye sees a phantom; door closes, reverses,
  closes, reverses; passenger gets annoyed.
- **Position-encoder drift.** Cabin slowly leaves "level" zone; floor
  arrival overshoots by inches.
- **Weight sensor calibration drift.** Empty-cabin tare creeps; cabin
  refuses to leave because it thinks it's overloaded.
- **Brake monitor faults.** Intermittent reading; cabin runs but
  monitor flags a fault and triggers safety.
- **Dispatcher loop convergence stalls.** Group control gets into a
  state where two cabins keep getting assigned to the same hall call.
- **Communication loss with monitoring backend.** Vendor's remote-
  service portal goes blind; building manager doesn't know there's a
  fault until someone calls about being stuck.

These devices are safety-rated; **the c-agent must NEVER write a
control bus that affects motion, brakes, or doors**. All capabilities
on this device class are read-only or address the monitoring layer
only. Remediation = "tell the technician what to look at."

### 7.3 Capability surface (read-only)

```
canopen.snapshot                CANopen NMT + PDO state
canopen.history                 PDO change history
elevator.position.history       cabin position over time
elevator.door.events            door open/close/reverse with source
elevator.brake.monitor          brake monitor signal history
elevator.weight.history         load cell readings
elevator.dispatcher.calls       hall + car call history
elevator.faults                 fault code log with vendor decode
modbus.rtu.read                 (read-only profile) door operator regs
serial.read.passive             passive RS-485 dump on a bus
```

No remediation capabilities are issued from c-agent on certified
elevator hardware. The brain produces a report; the technician acts.

## 8. HVAC and building automation

### 8.1 Typical stack

| Layer | Common content |
|---|---|
| Application | Building management head-end, vendor controller HMI |
| Middleware | BACnet/IP and BACnet MS-TP stack, Modbus client, KNX, vendor-proprietary |
| OS | Linux on industrial controller; sometimes Windows |
| Devices | Chillers, boilers, AHUs, VAVs, RTUs, VFDs, sensors (temp, pressure, CO₂, occupancy) |

### 8.2 Failure modes

- **Sensor drift.** Temperature sensor reads 1.5 °C high after years;
  zone temp regulation wrong; comfort complaints.
- **Valve actuator hunting.** PI loop tuned for one season hunts in
  another; actuator wears out.
- **BACnet IP traffic storms.** Misconfigured device sends Who-Is
  storms; whole subnet floods.
- **MS-TP token loss.** Repeater dies on a long MS-TP run; some
  devices vanish from the head-end.
- **Sequence of operation drift.** Setpoint schedule overridden
  manually; never reverted; building runs at hold setpoint indefinitely.
- **Energy consumption anomalies.** Equipment running outside
  schedule; tenant complaints lead to discovery weeks later.

### 8.3 Capability surface

```
bacnet.ip.scan                 device discovery on a subnet
bacnet.ip.read                 read object properties
bacnet.ip.write                write priority-array (HIGH-RISK)
bacnet.ip.alarms               alarm + event log
bacnet.mstp.tokens             token ring topology + token loss events
bacnet.mstp.diag               per-device retry counts
modbus.rtu.read                Modbus device read
hvac.schedule.read             active schedule + override stack
hvac.schedule.write            apply schedule change (HIGH-RISK)
hvac.sensor.history            sensor reading history with spike filter
hvac.vfd.state                 VFD speed, current, fault state
energy.meter.history           consumption history per circuit
```

## 9. Solar inverters, batteries, energy controllers

### 9.1 Typical stack

| Layer | Common content |
|---|---|
| Application | Vendor monitoring agent, grid-tie controller |
| Middleware | Modbus master, SunSpec, CAN BMS protocol, vendor cloud client |
| OS | Linux on inverter SBC or external monitoring gateway |
| Buses | CAN to BMS, Modbus RTU/TCP to inverters, RS-485 to meters |

### 9.2 Failure modes

- **Communication watchdog timeouts.** Inverter expects BMS heartbeat
  every N ms; on jitter, falls into a safe-derate; PV harvest drops.
- **MPPT issue.** One string underperforms; vendor portal shows
  string-level data 15 min late; nobody notices for weeks.
- **Battery cell imbalance.** BMS balances slowly; one string lags;
  total capacity drops; warranty implications.
- **Grid-side reactive power command not honored.** Utility commands
  Q via SunSpec; inverter reports compliance, behaves differently.
- **Time-sync wrong.** Inverter clock years off; energy data
  attributed to wrong intervals; settlement disputes.

### 9.3 Capability surface

```
sunspec.scan                   SunSpec device discovery
sunspec.read                   read SunSpec models
sunspec.write                  write writable points (HIGH-RISK)
modbus.rtu.read                inverter / meter Modbus
can.bms.read                   BMS state via CAN
mppt.string.history            per-string V/I/P history with anomalies
inverter.fault.log             fault + warning log with vendor decode
inverter.derate.history        derate events with cause attribution
grid.events                    over/under voltage, frequency events
clock.skew                     compare device clock to signed_now()
clock.set                      set device clock (HIGH-RISK)
```

## 10. Kiosks, POS, vending, ATMs

### 10.1 Typical stack

| Layer | Common content |
|---|---|
| Application | Locked-down kiosk shell, payment app, vendor management agent |
| Middleware | EMV kernel, P2PE library, printer driver, MSR/EMV/NFC reader SDK, barcode scanner driver, OS-locking shell |
| OS | Windows Embedded / 10 IoT, Linux kiosk distros, Android dedicated mode |
| Peripherals | Receipt printer, MSR/EMV/NFC reader, barcode scanner, cash drawer, cash recycler, dispense motors |

### 10.2 Failure modes

- **EMV kernel hung.** Card insert detected, kernel never returns;
  payment app blames network; reader needs power-cycle.
- **Printer paper-out misdetect.** Sensor returns false-positive after
  vibration; printer thinks it's out.
- **Barcode scanner lockup.** USB bus reset cascade; whole scanner
  enumeration fails.
- **Network proxy auth expired.** Captive-portal-style proxy on the
  store network; kiosk app shows a generic error.
- **Cash recycler jam.** Specific banknote orientation jams; recycler
  retries until it can't.
- **Kernel update broke a driver.** Mass deploy of OS update broke
  one specific peripheral driver; field engineers scramble.

### 10.3 Capability surface

```
peripheral.list                enumerated peripherals + drivers
peripheral.events              recent peripheral events + errors
emv.kernel.state               EMV kernel state machine + last command
emv.kernel.reset               kernel reset (HIGH-RISK)
printer.status                 printer status word + last error
printer.events                 print job log
scanner.events                 scanner errors / lockups
cash_handler.state             recycler / dispenser state + last jam
cash_handler.diag              run vendor diagnostic (HIGH-RISK)
network.proxy.state            proxy auth state, last response code
kiosk.shell.events              shell reload / app crash log
```

## 11. Access control and surveillance

### 11.1 Typical stack

| Layer | Common content |
|---|---|
| Application | VMS client, access controller firmware, ONVIF profile S/T client |
| Middleware | RTSP/RTP, ONVIF event services, Wiegand or OSDP reader interface, video codec stack |
| OS | Linux on ARM SBC, vendor RTOS for some readers |
| Buses | Wiegand, OSDP RS-485, IP camera over PoE, door strike via relay board |

### 11.2 Failure modes

- **ONVIF discovery fails.** WS-Discovery multicast blocked by switch
  IGMP snooping; cameras invisible to VMS.
- **RTSP stream drops.** Camera firmware bug under specific motion
  rates; stream re-establishes every few minutes.
- **PoE budget exceeded.** Switch silently truncates power; camera
  reboots randomly.
- **Reader-controller comm loss.** OSDP secure channel desyncs after
  controller reboot; reader shows red LED.
- **Door held alarm storms.** Magnet sensor drift + door bumper
  cracked; held alarms fire constantly.
- **Camera firmware partial corruption.** OTA failure leaves camera
  in bootloader; can't be reached normally.

### 11.3 Capability surface

```
onvif.discover                 ONVIF WS-Discovery + capabilities
onvif.events.subscribe         subscribe to motion / tamper events
rtsp.session.stats             active RTSP sessions, packet loss
codec.stream.health            per-stream packet loss + B-frame skip
poe.switch.budget              PoE budget + per-port consumption
osdp.bus.stats                 OSDP secure channel state per reader
wiegand.bus.events             raw Wiegand event log
access.events                  access grant/deny + door state events
access.replay                  replay last N access events from controller
camera.fw.status               camera firmware state + last OTA outcome
camera.reboot                  reboot camera (LOW-RISK)
```

## 12. Printers and multifunction devices

### 12.1 Typical stack

| Layer | Common content |
|---|---|
| Application | Job spooler, scan-to-cloud, vendor management agent |
| Middleware | IPP server, PostScript / PCL interpreter, fuser controller, paper path controller |
| OS | Vendor RTOS or Linux on ARM SoC |
| Hardware | Print heads, fuser, paper trays, sensors, formatter, scan bed |

### 12.2 Failure modes

- **Paper jam misreport.** Sensor stays asserted after jam clear;
  printer refuses to print until power cycle.
- **Fuser temperature instability.** Fuser cycles too fast or too
  slow; prints come out faded or scorched.
- **Cartridge auth chip failure.** Genuine cartridge with worn chip
  reads as third-party; refusal.
- **Scan-to-folder SMB auth.** AD password rotated; printer still has
  old credentials cached; jobs fail silently.
- **Network discovery loops.** mDNS announcement collisions on a
  busy office subnet; printer disappears from clients.

### 12.3 Capability surface

```
ipp.queue.read                 print queue state + recent jobs
ipp.queue.cancel               cancel jobs (LOW-RISK)
printer.sensor.state           paper, toner, fuser, cover sensors
printer.fuser.history          fuser temp + duty cycle history
printer.cartridge.auth         cartridge auth state per slot
printer.fw.versions            firmware inventory
scan.smb.test                  test SMB credentials (read-only)
scan.smb.update                update SMB credentials (HIGH-RISK)
printer.reboot                 reboot (LOW-RISK)
```

## 13. SD-WAN and branch-office gateways

### 13.1 Typical stack

| Layer | Common content |
|---|---|
| Application | Vendor SD-WAN agent, application-aware routing engine, telemetry uplink |
| Middleware | Multiple WAN tunnels (IPsec, GRE, vendor-proprietary), VRRP, BGP, OSPF, application classifier |
| OS | Vendor Linux on ARM/x86 |
| Buses | Multiple WAN ports (Ethernet, LTE/5G), LAN switch |

### 13.2 Failure modes

- **Tunnel flap.** IKE rekey fails on one peer; tunnel drops; SLA
  breached for minutes per rekey cycle.
- **Path-quality measurement gaslighting.** Gateway thinks path A is
  bad because BFD packets are deprioritized; real apps work fine.
- **Application-aware routing misclassifies.** A new SaaS uses an
  unknown port; default-class routing sends it to expensive path.
- **BGP flap on LAN side.** Customer router resets; ARP entry stale;
  gateway thinks BGP peer is up but ARP isn't refreshed.
- **VRRP split-brain.** Both gateways think they're master after a
  trunk port flap.
- **Telemetry uplink saturating WAN.** Gateway's own telemetry is
  using 30% of the customer's WAN.

### 13.3 Capability surface

```
ipsec.sa.list                  active SAs + counters
ipsec.events                   IKE_SA_INIT / AUTH / rekey events
sdwan.path_quality             per-path latency, loss, jitter, BFD state
sdwan.classifier.events        application classifier decisions
bgp.peer.state                 BGP peer state + recent events
bgp.routes.read                received + advertised routes
ospf.neighbors                 OSPF neighbor state
vrrp.state                     VRRP master/backup + recent transitions
qos.queue.stats                per-queue depth, drops
telemetry.budget               telemetry bytes / sec uplink
sdwan.path.disable             disable a path (HIGH-RISK)
```

## 14. Network-attached storage and backup appliances

### 14.1 Typical stack

| Layer | Common content |
|---|---|
| Application | NAS UI, backup scheduler, replication engine |
| Middleware | SMB, NFS, iSCSI, S3-compatible object server, Btrfs / ZFS / mdraid |
| OS | Custom Linux distro on x86 / ARM |
| Hardware | Multi-disk array, possibly NVMe cache, ECC RAM, redundant PSU |

### 14.2 Failure modes

- **RAID rebuild stalls.** A second drive throws read errors mid-
  rebuild; pool goes degraded.
- **ZFS scrub finds checksum errors.** Single-disk array; data
  corruption already shipped to clients.
- **SMB performance collapses.** SMB multichannel negotiation lands
  on a degraded link; clients see 1/10 throughput.
- **iSCSI session disconnects.** Initiator's CHAP secret rotated;
  sessions drop; VMs go read-only.
- **Replication backlog.** Snapshots accumulate; pool fills; writes
  fail on customer-visible volumes.
- **Disk firmware-level miss.** Drive starts logging predictive fail
  but at a verbosity the appliance doesn't parse; failure surprises
  the user.

### 14.3 Capability surface

```
zfs.pool.status                pool + vdev state, scrub progress
zfs.zpool.events               recent ZFS events
mdadm.array.state              mdadm array state + rebuild progress
smart.read                     SMART data per drive
smart.history                  SMART trend history
smb.sessions                   active SMB sessions + multichannel
nfs.exports                    active exports + client list
iscsi.sessions                 iSCSI sessions + auth state
replication.queue              replication backlog state
filesystem.usage               inode + space pressure per filesystem
zfs.scrub.start                start scrub (LOW-RISK)
mdadm.repair                   trigger repair (HIGH-RISK)
```

## 15. Cross-cutting bus and protocol surfaces

Some capability families show up across many of the device classes
above. Implementing them once gives the brain leverage in many
domains.

| Family | Domains |
|---|---|
| `serial.*` (RS-232/485, UART) | EV chargers, lifts, HVAC, energy, industrial |
| `can.*` (SocketCAN) | EV chargers, lifts, energy, industrial |
| `modbus.rtu.*`, `modbus.tcp.*` | EV chargers, HVAC, energy, industrial |
| `bacnet.*` | HVAC, building automation |
| `opcua.*` | Industrial |
| `mqtt.*` | All telemetry-pushing devices |
| `ebpf.*` | All Linux-based devices |
| `nl80211.*`, `hostapd.*` | Wi-Fi modems, mesh |
| `i2c.*`, `spi.*`, `gpio.*` | Any board with sensors / front-panel I/O |
| `service.*` (systemd / procd) | All Linux |
| `journal.*`, `dmesg.*` | All Linux |
| `tr181.*` (USP / TR-069) | CPE, mesh, ISP-managed |
| `ubus.*` | OpenWrt-derived devices |
| `dbus.*` | Systemd-based industrial / kiosks |

Each family is implemented once as a host-import set in the c-agent
runtime. Modules that need a family declare it in their signed
descriptor. The same `modbus.rtu.read` capability that diagnoses a
solar inverter also diagnoses an HVAC chiller.

## 16. What this enables that today's tools do not

The device classes above all have rich internal state and all suffer
from the same operational gap: **the cost of accessing that state in a
useful way exceeds the value of any single incident**. A vendor builds
a great log surface; nobody pulls real-time correlations across it. An
operator pays for a remote-access tool; technicians use it to read the
same five fields they always read.

The c-agent + brain architecture changes the economics:

1. **Per-incident probe authoring is cheap.** The brain writes a Rust
   probe in seconds. There is no "we need a feature request to the
   vendor" loop.

2. **Capability surfaces are reusable.** The fifth `modbus.rtu.read`
   probe is free; the first pays for the family.

3. **The pattern library compounds.** Every solved incident leaves a
   curated module behind. Within months, a tenant's library covers the
   common 80% of incidents in one library hit.

4. **The brain can correlate across subsystems.** No human reads the
   conntrack table, the hostapd ctrl-iface, the driver firmware log,
   and the BSS-transition history at the same time. The brain does.

5. **Field engineers get leverage.** A junior technician with a phone
   can run the same diagnostic loop a senior engineer would, supervised
   by the brain.

6. **Truck rolls drop.** Most incidents that today cost a site visit
   are diagnosable from a probe + remediation pair. Even when a visit
   is required, the tech arrives knowing what to bring.

The device classes that benefit most are exactly the ones with
expensive, slow, or impossible remote support today, complex internal
state worth correlating, and physically inconvenient deployment. Every
section above has at least one of those properties; most have all
three.
