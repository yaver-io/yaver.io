#!/usr/bin/env python3
"""Yaver BLE transport bridge (Raspberry Pi).

Runs a BLE GATT peripheral that tunnels the Yaver agent's HTTP/ops API over
Bluetooth LE, so a phone can reach the agent on a production floor with NO Wi-Fi.
Transport only: it forwards the phone's framed request (with the phone's own
Authorization bearer) to the local agent on 127.0.0.1:18080 and streams the reply
back. See GATT_PROTOCOL.md for the wire format.

Deps (Pi):  sudo apt install -y python3-dbus  &&  pip3 install bluezero
Run:        python3 peripheral.py   (or via the yaver-ble.service systemd unit)

NOTE: BlueZ/bluezero notify-push behavior should be validated on a real Pi; the
chunking + HTTP forwarding logic is hardware-independent and unit-checkable
(see selftest() at the bottom: `python3 peripheral.py --selftest`).
"""
import json
import sys
import http.client

AGENT_HOST = "127.0.0.1"
AGENT_PORT = 18080

SVC  = "59415645-0001-4d65-7368-0000000000a0"
INFO = "59415645-0002-4d65-7368-0000000000a0"
REQ  = "59415645-0003-4d65-7368-0000000000a0"
RESP = "59415645-0004-4d65-7368-0000000000a0"

MTU_PAYLOAD = 244          # 247 negotiated - 3 ATT header; client may force 17 (20-3)
HDR = 4                    # [msgId:1][seq:2][flags:1]

# ── chunk framing ────────────────────────────────────────────────────────────

def split_chunks(msg_id: int, data: bytes, payload_max: int = MTU_PAYLOAD):
    """Yield [msgId][seq BE][flags] + payload frames; flags bit0=LAST."""
    if not data:
        data = b""
    total = max(1, (len(data) + payload_max - 1) // payload_max)
    for seq in range(total):
        chunk = data[seq * payload_max:(seq + 1) * payload_max]
        last = 1 if seq == total - 1 else 0
        yield bytes([msg_id & 0xFF, (seq >> 8) & 0xFF, seq & 0xFF, last]) + chunk


class Reassembler:
    """Collects REQ chunks per msgId until the LAST flag."""
    def __init__(self):
        self.buf = {}     # msgId -> {seq: payload}
        self.last = {}    # msgId -> last seq index

    def feed(self, frame: bytes):
        if len(frame) < HDR:
            return None
        msg_id = frame[0]
        seq = (frame[1] << 8) | frame[2]
        is_last = frame[3] & 1
        payload = frame[HDR:]
        self.buf.setdefault(msg_id, {})[seq] = payload
        if is_last:
            self.last[msg_id] = seq
        if msg_id in self.last and len(self.buf[msg_id]) == self.last[msg_id] + 1:
            data = b"".join(self.buf[msg_id][i] for i in range(self.last[msg_id] + 1))
            del self.buf[msg_id]
            del self.last[msg_id]
            return msg_id, data
        return None


# ── HTTP forward to the local agent ─────────────────────────────────────────

def forward(req: dict) -> dict:
    """req: {id, method, path, headers, body} -> {id, status, body}."""
    mid = req.get("id", 0)
    method = req.get("method", "GET").upper()
    path = req.get("path", "/info")
    headers = req.get("headers", {}) or {}
    body = req.get("body", "")
    if isinstance(body, (dict, list)):
        body = json.dumps(body)
    try:
        conn = http.client.HTTPConnection(AGENT_HOST, AGENT_PORT, timeout=30)
        conn.request(method, path, body=body.encode() if body else None, headers=headers)
        r = conn.getresponse()
        out = r.read().decode("utf-8", "replace")
        conn.close()
        return {"id": mid, "status": r.status, "body": out}
    except Exception as e:  # noqa: BLE001 — surface as a 502 to the phone
        return {"id": mid, "status": 502, "body": json.dumps({"error": str(e)})}


def info_payload() -> bytes:
    """INFO characteristic: discovery JSON pulled from the local agent /info."""
    try:
        conn = http.client.HTTPConnection(AGENT_HOST, AGENT_PORT, timeout=3)
        conn.request("GET", "/info")
        data = json.loads(conn.getresponse().read().decode())
        conn.close()
        out = {"deviceId": data.get("deviceId", ""), "name": data.get("hostname", ""),
               "agentVersion": data.get("version", ""), "mesh": data.get("mesh", "")}
    except Exception:
        out = {"name": "yaver-edge", "agentVersion": "?"}
    return json.dumps(out).encode()


# ── BLE peripheral (bluezero / BlueZ) ────────────────────────────────────────

def run_peripheral():
    from bluezero import adapter, peripheral

    addr = list(adapter.Adapter.available())[0].address
    short = addr.replace(":", "")[-4:]
    p = peripheral.Peripheral(addr, local_name=f"Yaver-Edge-{short}", appearance=0)
    asm = Reassembler()
    state = {"resp_char": None}

    def on_req_write(value, options):
        frame = bytes(value)
        done = asm.feed(frame)
        if not done:
            return
        msg_id, data = done
        try:
            req = json.loads(data.decode())
        except Exception as e:  # noqa: BLE001
            req = {"id": msg_id, "method": "GET", "path": "/info"}
        resp = forward(req)
        send_response(state["resp_char"], resp)

    def on_resp_notify(notifying, char):
        state["resp_char"] = char if notifying else None

    p.add_service(srv_id=1, uuid=SVC, primary=True)
    p.add_characteristic(srv_id=1, chr_id=1, uuid=INFO, value=list(info_payload()),
                         notifying=False, flags=["read"],
                         read_callback=lambda: list(info_payload()))
    p.add_characteristic(srv_id=1, chr_id=2, uuid=REQ, value=[], notifying=False,
                         flags=["write", "write-without-response"],
                         write_callback=on_req_write)
    p.add_characteristic(srv_id=1, chr_id=3, uuid=RESP, value=[], notifying=False,
                         flags=["notify"], notify_callback=on_resp_notify)

    print(f"[yaver-ble] advertising Yaver-Edge-{short} on {addr}", flush=True)
    p.publish()


def send_response(resp_char, resp: dict):
    data = json.dumps(resp).encode()
    if resp_char is None:
        # client not subscribed; nothing to notify on. (Phone always subscribes
        # to RESP before writing REQ — see GATT_PROTOCOL.md.)
        return
    for frame in split_chunks(resp.get("id", 0), data):
        resp_char.set_value(list(frame))   # bluezero: triggers a notification


# ── offline self-test (no BLE hardware needed) ──────────────────────────────

def selftest():
    """Round-trip the chunker (the hardware-independent core)."""
    payload = json.dumps({"id": 7, "method": "POST", "path": "/ops",
                          "body": "x" * 1000}).encode()
    asm = Reassembler()
    out = None
    for fr in split_chunks(7, payload, payload_max=20):  # force tiny MTU
        out = asm.feed(fr)
    assert out is not None, "did not reassemble"
    mid, data = out
    assert mid == 7 and data == payload, "reassembly mismatch"
    # single-chunk path
    asm2 = Reassembler()
    r2 = None
    for fr in split_chunks(3, b"hi"):
        r2 = asm2.feed(fr)
    assert r2 == (3, b"hi"), "single-chunk failed"
    print("selftest OK: chunk/reassemble round-trips at MTU 20 and single-frame")


if __name__ == "__main__":
    if "--selftest" in sys.argv:
        selftest()
    else:
        run_peripheral()
