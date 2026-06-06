# Yaver Robot Fleet over the Mesh — deep audit + design

> **Use case:** one Yaver mobile app drives **many** robot cells. Each cell is a
> Yaver edge device (a Lenovo PC today, a **second-hand phone + Yaver IoT
> connector box** tomorrow) physically wired to a machine (Ender-3 now, any
> G-code/serial/GPIO/Modbus rig later). The phone reaches each cell **through the
> Yaver mesh** (possibly via a gateway device), sending **commands** and
> receiving **streaming video** — never by hardcoded LAN IP. Fleet-scale: N robot
> cells, pick any from the app. Yaver supports this as a first-class feature.
>
> Status: **AUDIT + DESIGN** (no build yet, per request). Verified against the
> codebase + the live bench (magara=10.0.0.45 edge, simkab-Vostro=10.0.0.30
> gateway, iPhone via mesh).

---

## 1. The bug this replaces (root cause)

The current `robotClient` (`mobile/src/lib/robotClient.ts`) talks to robotd at a
**hardcoded `host:8336`**, defaulting the host to `quicClient.baseUrl`'s hostname.
In a mesh/gateway/fleet topology that is simply the wrong layer:

- The phone is paired via the **gateway** (simkab, 10.0.0.30); the printer is on
  a **different** box (magara, 10.0.0.45). The default host became `10.0.0.30`
  → every request hit the wrong machine → "no camera" + "can't control".
- Even pointed at `10.0.0.45`, it only works if the phone is on magara's LAN.
  Over relay/4G/Tailscale there is no such route.
- It can't address **multiple** robots — one hardcoded host.

**The fix is architectural: route robot control + video over the Yaver mesh,
addressed by `deviceId`, exactly like every other Yaver capability.**

---

## 2. Existing codebase — what we reuse (audit, file:line)

The mesh already has everything needed; almost nothing is net-new plumbing.

| Capability | Where | Use for the fleet |
|---|---|---|
| **Machine-targeted ops** | `desktop/agent/ops.go:222` `proxyToDevice(machine,"POST","/ops",…)` | `callOps(verb, payload, machine=<robotDeviceId>)` executes the verb **on that device** over the mesh, auto-routing via relay/direct |
| **Remote agent resolution** | `agent_mesh_remote.go:232-356` (relay candidates `relay.HttpURL + "/d/" + deviceID`) | how a call finds the target device through the gateway/relay |
| **Device-addressed HTTP** | `relay/server.go:959-1101` `/d/<deviceId>/<path>` → forwarded to that device's agent (`:18080`), **no path allowlist** | **video over the mesh**: `<relay>/d/<id>/robot/snapshot` and `/robot/stream` |
| **Peer proxy** | `desktop/agent/peer_proxy_http.go:25-79` `/peer/<deviceId>/<path>` | gateway forwards to a peer on its LAN |
| **Ops verb registration** | `ops.go:100 registerOpsVerb` (init-time, self-registering) | add `robot_*` verbs with zero edits to shared files |
| **Robot engine (built this session)** | `desktop/agent/robot/` (`Controller`, `SerialBackend`, `GstCamera`, verify loop, torque) + `cmd/robotd` | the driver the verbs call; native Marlin serial, no Python |
| **Capability gating precedent** | `--machine`, `--ghost` serve flags + `machineEnabled` (main.go) | advertise a **`robot` capability** so the app lists robot-capable devices |
| **Heartbeat / device registry** | `auth.go::SendHeartbeat`, `backend/convex/devices.ts` | advertise `capabilities:["robot"]` per device → fleet discovery |
| **Headless mobile test harness** | `mobile-headless/` (bun CLI `ops --verb --machine --payload`, shares `mobile/src/lib/*`) | test the whole mesh-routed flow with **no native rebuild** |
| **Mobile mesh client** | `mobile/src/lib/quic.ts:1164` `baseUrl` (tunnel→relay `/d/<id>`→direct), `callOps:7439` | already resolves a device's mesh route; the robot screen should use it |

Live-verified: magara's **mesh agent is active** (`:18080`, device `08182df8…`,
same account) and **robotd active** (`:8336`). So magara is reachable over the
mesh today; only the robot *control path* is at the wrong layer.

---

## 3. Target architecture

```
   ┌────────────── Yaver mobile app (one app, many robots) ──────────────┐
   │  Device picker: list devices where capabilities ∋ "robot"            │
   │  Selected robot = a deviceId (magara-A, magara-B, … phone-cells)     │
   │   Commands:  callOps("robot_jog", payload, machine=<robotDeviceId>)  │
   │   Video:     GET <mesh>/d/<robotDeviceId>/robot/snapshot (poll)      │
   └───────────────┬───────────────────────────────────────┬─────────────┘
                   │ mesh (LAN → relay → 4G), via gateway   │ same mesh route
        ┌──────────▼───────────┐                 ┌──────────▼───────────┐
        │ gateway agent (simkab)│  proxyToDevice  │  relay /d/<id>/…      │
        └──────────┬───────────┘ ───────────────► └──────────┬───────────┘
                   ▼                                          ▼
   ┌──────────── robot edge device (magara: Lenovo now / phone+kit later) ─────┐
   │  yaver AGENT (:18080)                                                      │
   │    robot_* ops verbs ──► robot.Controller ──► SerialBackend ──► machine    │
   │    GET /robot/snapshot,/robot/stream ──► GstCamera / phone Camera2         │
   │  (robotd engine optional — verbs can drive the robot pkg in-process)       │
   └───────────────────────────────────────────────────────────────────────────┘
```

### 3.1 Control = agent ops verbs (mesh-native, multi-robot)
Fold the robot engine into the **agent** as `robot_*` ops verbs (`robot_status`,
`home`, `jog`, `move`, `tool`, `verify`, `screw`, `estop`, `reset`). The phone
calls `callOps(verb, payload, machine=<robotDeviceId>)`:
- routes through the **gateway/relay** to the target device (ops.go `proxyToDevice`),
- executes against `robot.Controller` (native serial / future phone fd),
- returns the same `MoveResponse` (position + verify + encoder cross-check).

**Multi-robot is free:** the only difference between robots is the `machine`
deviceId. One app, N cells, each a row in the device list.

### 3.2 Video = device-addressed HTTP over the mesh
The agent serves `/robot/snapshot` (single JPEG, iOS-robust) and `/robot/stream`
(MJPEG). The phone fetches them via the relay's `/d/<deviceId>/robot/…` path —
**no LAN assumption**, works through the gateway, over 4G. iOS uses snapshot
polling (WKWebView can't do MJPEG `<img>`); Android/web can use the stream.

### 3.3 Capability advertisement = fleet discovery
The agent advertises a **`robot` capability** in its heartbeat (like `--machine`/
`--ghost`). The app lists devices where `capabilities ∋ "robot"` → the robot
picker. Each can carry a label (machineKey, e.g. `creality_ender`) for the UI.

### 3.4 Edge portability (laptop ⇄ phone + connector box)
The robot verbs sit in the **agent**, which already targets laptops; the same
verbs + `SerialBackend` run on a phone via Termux (`OpenMarlinFD` over a
`termux-usb` fd) or a native module, with the **Yaver IoT connector box** giving
USB-serial + GPIO + PD. The mesh-facing contract (`robot_*` verbs + `/robot/*`)
is identical regardless of edge hardware — the app never knows the difference.

---

## 4. Key design decisions (need your nod)

1. **Engine placement.** Two options:
   - **(A) Fold `robot` pkg into the agent** as `ops_robot.go` verbs + serve
     `/robot/snapshot|stream` from the agent. Cleanest mesh-native; one process;
     verbs + video both ride the existing `/ops` and `/d/<id>/` routing. **Recommended.**
   - **(B) Keep `robotd` sidecar**, agent **proxies** `/robot/*` → `localhost:8336`
     and adds thin `robot_*` verbs that forward. Less refactor, one more process.
   Recommend **A** for the perfect edge; B is a fast bridge.

2. **Video transport.** Snapshot-polling over `/d/<id>/robot/snapshot` for iOS
   (universal); MJPEG `/d/<id>/robot/stream` as an Android/web fast-path. (WebRTC
   later if low-latency is needed.)

3. **Auth.** Reuse the existing bearer + relay-password path (`__rp`) — no new
   auth. Robot verbs are owner-only (`AllowGuest=false`) like `machine_*`.

4. **Same pattern for PLC.** The `machine_*` (Modbus) verbs already exist and are
   already machine-targetable over the mesh — so a PLC cell is the *same* fleet
   entry with a different capability. Unify under "Edge Control".

---

## 5. Headless testing (no native rebuilds)

`mobile-headless/` runs the app's real `lib` code in Node/Bun. Once the agent has
`robot_*` verbs, the entire mesh-routed flow is testable headless:

```bash
cd mobile-headless && bun install
YMH_AUTH_TOKEN=<token> bun run src/bin/cli.ts ops \
  --verb=robot_status --machine=<magaraDeviceId> --payload='{}'
bun run src/bin/cli.ts ops --verb=robot_jog \
  --machine=<magaraDeviceId> --payload='{"axis":"Z","dist":10,"verify":"off"}'
```
This exercises the exact path the phone uses (callOps → proxyToDevice → relay →
magara agent → robot driver) with **no Xcode build** — the iteration loop you
asked for. Add a `mobile-headless/test/robot.test.ts` to lock it in CI.

---

## 6. Gaps → migration plan

| # | Gap | Work |
|---|---|---|
| G1 | Robot is a sidecar at a hardcoded IP, not mesh-routed | Add `robot_*` agent ops verbs (machine-targetable) — **option A** |
| G2 | Video not over the mesh | Serve `/robot/snapshot|stream` from the agent; phone fetches `/d/<id>/robot/*` |
| G3 | No robot capability advertised | Add `robot` to heartbeat capabilities + a `--robot` serve flag |
| G4 | Mobile uses hardcoded host | `robotClient` → `callOps(verb,payload,machine)` + mesh snapshot URL; **device picker** for multi-robot |
| G5 | No headless robot test | `mobile-headless/test/robot.test.ts` |
| G6 | Edge only laptop | Phone host (Termux `OpenMarlinFD`) + connector-box BOM (separate track) |
| G7 | Fleet UI | Robot list (capability-filtered) → per-robot screen; "more than one magara" |

**Phase 1 (unblocks the phone now):** G1+G2+G4 minimal — `robot_*` verbs in the
agent on magara + agent-served snapshot + mobile `robotClient` via `callOps`/mesh.
Test headless (G5). **Phase 2:** capability advertisement + fleet picker (G3,G7).
**Phase 3:** phone-host edge + connector box (G6) + PLC unification.

---

## 7. Why this is the right shape

It makes a robot cell **just another Yaver device**: discovered by capability,
addressed by `deviceId`, reached over the same mesh (LAN→relay→4G) through any
gateway, controlled by the same `/ops` verbs, observed over the same `/d/<id>/`
HTTP — so "one app, many robots", "laptop or phone edge", and "remote vibing over
4G" all fall out of the existing mesh with **no special-case networking**. The
direct-IP robotd was scaffolding; this is the load-bearing design.
