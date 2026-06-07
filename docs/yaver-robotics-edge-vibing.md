# Yaver Robotics Edge — an old phone as a Pi-replacement for remote robotics vibe-coding

> **One line:** snap a connector-box kit onto a second-hand phone, plug it into
> a machine (Ender-3 today, any G-code/serial/GPIO rig tomorrow), and Yaver turns
> it into a remote robotics dev box you **vibe-code against from anywhere** — the
> phone is both the *command device* (send G-code / drive GPIO, SSH-style) **and**
> the *eye* (live camera + sensors), with an AI agent closing the loop.
>
> This unifies everything in `docs/robot-protocol.md`, `docs/robot-screwdriver-cell.md`,
> `docs/yaver-phone-harness-analysis.md`, `docs/yaver-companion-mcu.md`. Nothing here
> is new theory — it's the product those pieces add up to.

---

## 1. Why a phone, not a Pi

A used phone is a *better* robotics edge node than a Raspberry Pi, and it's cheaper
and not supply-constrained:

| | **Old phone + Yaver kit** | Raspberry Pi |
|---|---|---|
| Compute | ARM SoC, often beefier | Pi-class |
| **Camera** | built-in, good sensor + ISP | extra module + cable |
| **Battery / UPS** | built-in — survives power blips mid-job | none (needs a UPS HAT) |
| Screen | built-in touch dashboard | none (needs HDMI) |
| Network | WiFi **+ 4G/5G modem** (remote sites!) | WiFi/Eth only |
| GPIO | via the kit (USB→MCU/expander) | native header |
| Cost | ~£0–30 (e-waste) | £40–80 + bits |
| Supply | infinite (everyone has a drawer full) | chip shortages |

The two things a Pi has that a phone lacks — a **GPIO header** and a **Linux shell with
device access** — are exactly what the **kit** and **Yaver** provide.

---

## 2. The remote phone plays two roles — Yaver already serves both

### Role A — the *command* device ("SSH for the machine")
- **`yaver ssh` / `yaver shell`** — relay-PTY over the mesh gives a real remote shell to
  the phone-edge from anywhere (already shipped, see zero-touch provisioning).
- **The robot protocol** (`/ops robot_*` or `robotd /robot/*`) — send G-code / motion /
  GPIO / screw commands over the mesh, camera-validated. This is the structured "SSH for
  the machine": `robot_jog`, `robot_move`, `robot_tool`, `robot_screw`, `gpio_set`.
- **`yaver code --attach <phone>`** — attach a coding runner (Claude Code) *to the phone-
  edge* and drive it like a dev box.

### Role B — the *output* device (camera + sensors)
- **Live MJPEG stream** (`/robot/stream`, Bambu-P1S-style) — the phone's camera (or an
  attached webcam) streamed to the dev/app/Talos over the mesh.
- **Verify + telemetry** — `vision_verify` (camera→LLM verdict), encoder cross-check,
  and companion **torque/force** all flow back as structured feedback.

So "the phone acts like both an SSH device for sending G-code and a camera output" is
**two existing Yaver capabilities pointed at one device**.

---

## 3. The kit / connector box — "make the phone a Pi"

A small box the phone plugs into (USB-C) that breaks out everything a robotics rig needs.
This is the **ara-kablo bundle + companion**, productized:

```
        ┌──────────────── Yaver Robotics Kit (connector box) ────────────────┐
 phone ─┤ USB-C │→ PD passthrough (charges phone)                            │
 (USB-C)│  hub  │→ USB-serial #1  → machine board (Ender Marlin: G-code+XYZ) │→ machine
        │       │→ USB-serial #2  → companion MCU → GPIO/PWM/ADC, I²C/SPI    │→ relays, sensors,
        │       │→ (opt) 2nd camera / lights                                 │   load cell (torque),
        └───────┴───────────────────────────────────────────────────────────┘   estop button
            ▲ phone camera = the eye          ▲ phone battery = UPS
```

Contents (tiered):
- **Tier 0 (cable):** USB-C PD dock + short shielded USB-B to the machine. (Pure G-code rigs
  need nothing else — XYZ + tool ride the machine board, see §5.)
- **Tier 1 (GPIO):** + companion MCU (RP2040/ESP32) → digital/PWM/ADC, relays, the
  screwdriver MOSFET, an **e-stop button**, and **load-cell torque**.
- **Tier 2 (isolation/reach):** + USB isolator (noisy cells), ESP32-BLE I/O (cable-free),
  external lights/lens for the camera.

The box turns the phone's single USB into the Pi's GPIO header + the machine link, with PD
so the phone charges while hosting.

---

## 4. The remote-vibing loop (the actual experience)

```
  YOU / Claude Code (anywhere)
        │  yaver code --attach <phone>     (mesh: LAN → relay → 4G)
        ▼
  ┌─────────────── phone-edge (Yaver agent/robotd) ───────────────┐
  │  write/iterate robotics code  ──►  run it                      │
  │     │                                  │                       │
  │     │                          robot protocol → machine        │ ──► XYZ moves, screw drives
  │     │                          (G-code / GPIO over USB)         │
  │     ▼                                  ▼                        │
  │  camera stream  ◄──────────  vision verdict + encoder + torque  │ ◄── the world changed?
  └───────────────────────────────────────────────────────────────┘
        ▲  live camera + structured verdict stream back
        │
  YOU watch it move on the live feed; Claude SEES the verdict and iterates.
```

"Perfect remote vibing" = the AI agent has a **real-world feedback loop**: it changes code
or sends a motion, the **camera + encoder + torque** tell it (and you) whether the physical
result matches intent, and it iterates — the same inner loop as web vibe-coding, but the
"render" is a real machine seen through the phone's camera. The verify gate (`moved`,
`obstruction`, `seated-at-torque`) is what makes it *safe* to let an agent drive metal.

---

## 5. What already exists vs. what's left

| Capability | Status |
|---|---|
| Move-and-verify protocol (`robot_*`, M400-gated, soft-limits, e-stop) | ✅ built, live-proven |
| Camera-LLM verify + encoder cross-check + **torque** loop | ✅ built (torque sim-tested) |
| Live MJPEG camera stream + P1S mobile view | ✅ built, live-proven (~9 fps) |
| `robotd` edge server (one binary, no full-agent rebuild) | ✅ built, deployed |
| Mesh "SSH"/PTY + `yaver code --attach` | ✅ exists (Yaver core) |
| Companion MCU firmware (GPIO + INA219/HX711 torque) | ✅ built |
| **Phone-as-host** (`OpenMarlinFD` + Termux, then native `YaverUsbSerial`) | ⬜ P0 next |
| **One-USB screwdriver** via repurposed E-stepper + TMC StallGuard torque | ⬜ next (less cabling) |
| **Kit BOM + connector box** (PD dock + companion + breakouts) | ⬜ productize |
| Talos surfaces (already rich Ender control; add camera stream) | ⬜ wire stream |

The software spine is done and proven. The remaining work is **(a)** running the proven
edge code *on the phone* (Termux fd → native module), **(b)** the one-USB screwdriver via
the freed E-stepper, and **(c)** packaging the kit.

---

## 5b. Same pattern, every machine — including PLCs

The loop is **machine-agnostic**: an edge node (laptop or phone) speaks the
machine's native protocol over USB/serial/IP, a camera watches, and a closed
loop gates each action. G-code is one protocol; **Modbus/PLC is another**, and
Yaver already has that half:

| | G-code robot (this work) | **Modbus/PLC** (already in `desktop/agent/machine/`) |
|---|---|---|
| Edge transport | Marlin serial (`SerialBackend`) | Modbus RTU (serial) / TCP (`machine/modbus.go`) |
| Command | `robot_move/jog/tool` | `machine_read/write/scan` (read+write registers) |
| **Deterministic gate** | encoder cross-check (`M114` vs commanded) | **write + read-back verify** (already in `machine_write`) |
| Camera gate | `vision_verify` (moved/obstruction) | same — watch the HMI / machine state change |
| Remote control | mesh `/ops` + `yaver ssh` | same `/ops` verbs + mesh |
| Edge host | laptop **or** second-hand phone | laptop **or** second-hand phone |

So "the same loop for PLC cases on a remote laptop or phone" is **already 80%
built**: `machine/` does the Modbus read/write with read-back verification (the
PLC analog of the encoder cross-check), and the camera-verify + closed-loop +
mesh-control + phone-host pieces are exactly what the robot work added. Unifying
them = one **Yaver Edge Control** feature with two backends (G-code, Modbus) under
the shared `Controller` (verify loop + camera + safety), each runnable from a
phone via the kit. That's the roadmap: a single edge abstraction, many machines.

## 6. Positioning

This is Yaver's wedge applied to hardware: **lower the opex of robotics dev**. Instead of a
Pi + UPS + camera module + screen + a SaaS cloud, it's *an old phone + a kit + the Yaver
mesh*, and you vibe-code robotics remotely with an AI in the loop and a camera that proves
it worked. The Ender-3 screwdriver cell is the first rig; the protocol is rig-agnostic
(any G-code/serial/GPIO machine), so the same phone-edge drives a CNC, a pick-and-place, a
camera rig, or a custom Cartesian build.
