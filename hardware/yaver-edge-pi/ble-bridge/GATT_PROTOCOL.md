# Yaver BLE Transport — GATT protocol

The no-Wi-Fi fallback. The Pi runs a BLE GATT peripheral that tunnels the Yaver
**agent HTTP/ops API** over Bluetooth LE so the phone can reach the agent on a
production floor with no Wi-Fi/internet. It is a **transport only** — it forwards
the phone's request (with the phone's own `Authorization` bearer) to the local
agent at `127.0.0.1:18080` and streams the reply back. Auth is unchanged from the
IP path; BLE bonding adds link-layer security.

Bandwidth note: BLE carries control/ops + Modbus + `netcapture` analysis JSON
comfortably. It does **not** carry live video — Widget B's live camera needs Wi-Fi
(stills-on-demand over BLE are fine). See `../../README.md` connectivity ladder.

## GATT structure

**Service** `59415645-0001-4d65-7368-0000000000a0`  ("YAVE" prefix)

| Characteristic | UUID | Props | Purpose |
|---|---|---|---|
| **INFO** | `59415645-0002-4d65-7368-0000000000a0` | read | discovery: JSON `{deviceId, name, agentVersion, mesh, ts}` |
| **REQ** | `59415645-0003-4d65-7368-0000000000a0` | write, write-without-response | phone → Pi: chunked request frames |
| **RESP** | `59415645-0004-4d65-7368-0000000000a0` | notify | Pi → phone: chunked response frames |

Advertised local name: `Yaver-Edge-<short-id>`. Scan filter on the **service UUID**.

## Framing (chunking over the MTU)

Negotiate MTU (request 247 → ~244-byte payload; assume 20 if refused). Each
logical message is split into chunks:

```
chunk = [ msgId : 1 ][ seq : 2 BE ][ flags : 1 ][ payload : N ]
        flags bit0 = LAST (1 = final chunk of this msgId)
```

- `msgId` rotates 1..255 per request (client-chosen); the response reuses it.
- Reassemble by `msgId` in `seq` order until the LAST chunk.

**Request payload** (reassembled JSON):
```json
{ "id": 7, "method": "POST", "path": "/ops",
  "headers": { "Authorization": "Bearer <phone-yaver-token>" },
  "body": "{\"verb\":\"box_autoconnect\",\"payload\":{\"unit\":1}}" }
```
**Response payload** (reassembled JSON):
```json
{ "id": 7, "status": 200, "body": "{\"ok\":true,\"initial\":{...}}" }
```

The bridge maps `method`+`path` to `http://127.0.0.1:18080<path>`, copies
`headers` + `body`, and returns `status`+`body`. So every existing agent endpoint
works over BLE verbatim: `/ops` (all the `box_*`/`netcapture_*`/`machine_*` verbs),
`/info`, `/netcapture/*`, `/streams/*` (poll, not SSE), etc.

## Mobile client (react-native-ble-plx) — IMPLEMENTED

Shipped in `mobile/src/lib/bleTransport.ts` (dep `react-native-ble-plx@^3.2`
already in `package.json`; the config plugin + iOS `NSBluetoothAlwaysUsageDescription`
+ Android `BLUETOOTH_SCAN/CONNECT/ACCESS_FINE_LOCATION` are wired in `app.json` /
the manifests). Exposed API:

- `bleConnect()` — scan the service UUID, connect, negotiate MTU (cached).
- `bleFetch(method, path, headers, body)` — chunked request/response per the frame
  format above; reassembles by `msgId`.
- `bleCallOps(verb, payload)` — `POST /ops` over BLE with the phone's bearer.
- `linkCallOps(verb, payload, ipAttempt)` — **the drop-in the UI uses**: tries the
  IP path first, auto-falls-back to BLE on a connectivity failure, and tags the
  result with `via: "ip" | "ble"`.

The NetCapture "Connect to machine" / "Self-test" buttons already route through
`linkCallOps`, so on a no-Wi-Fi floor they transparently switch to BLE and show
"· via BLE". A sketch of the wire handling for reference:

```ts
// mobile/src/lib/bleTransport.ts  (sketch — needs react-native-ble-plx)
const SVC = "59415645-0001-4d65-7368-0000000000a0";
const REQ = "59415645-0003-4d65-7368-0000000000a0";
const RESP = "59415645-0004-4d65-7368-0000000000a0";

async function bleFetch(dev, method, path, headers, body): Promise<{status,body}> {
  const id = nextMsgId();
  const msg = enc(JSON.stringify({ id, method, path, headers, body }));
  // chunk msg into [id][seq][flags][payload] frames, writeWithoutResponse to REQ
  // subscribe RESP notify, reassemble frames with this id until LAST, JSON.parse
}
// then: callOps(verb, payload) -> bleFetch(dev,"POST","/ops",{Authorization:bearer}, JSON.stringify({verb,payload}))
```

Selection logic in the app: if mesh/LAN reachable → use `quicClient` (IP);
else scan for the `Yaver-Edge` service and route `callOps`/reads through
`bleTransport`. The widget is the same; only the pipe changes.

> Not wired into the RN build yet (avoids pulling `react-native-ble-plx` into the
> bundle before it's needed). The Pi side (`peripheral.py`) is the deliverable;
> this is the client contract for when we light up the mobile BLE path.

## Pairing / security

- BLE bonding (LE Secure Connections) on first connect; the bond persists so
  reconnects are silent — part of "no config".
- The phone still presents its Yaver bearer in every framed request; the agent
  authorizes exactly as over IP. A bonded-but-unauthorized phone gets 401s.
- The bridge binds only to `127.0.0.1` for the HTTP side — it never widens the
  agent's network exposure.
