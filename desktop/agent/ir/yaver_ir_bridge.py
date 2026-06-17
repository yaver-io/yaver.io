#!/usr/bin/env python3
"""yaver_ir_bridge.py — local IR/RF learn+blast sidecar for the Yaver agent.

Mirrors the pyatv bridge (appletv/yaver_atv_bridge.py): a tiny stdlib HTTP server
on 127.0.0.1 that the Go side (ir.go) drives over JSON. The backend is
python-broadlink (a Broadlink RM4/RM-class WiFi IR+RF learner/blaster on the
LAN). broadlink is imported lazily so /healthz can report availability without
crashing when it isn't installed (the Go side surfaces an actionable hint).

Endpoints (all 127.0.0.1 only):
  GET  /healthz          -> {ok, broadlink: bool, error?}
  POST /scan             -> {devices: [{host, mac, type, name}]}
  POST /learn  {host}    -> {code} (base64 IR) | {error}  (point the remote, press a button)
  POST /blast  {host, code} -> {ok} | {error}

This is content-agnostic plumbing for the user's OWN devices in their OWN room
(IR is line-of-sight): no third party, no mains. Codes are stored by the Go side
in the vault, never here.
"""
import base64
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer


def _broadlink():
    try:
        import broadlink  # type: ignore
        return broadlink, None
    except Exception as e:  # pragma: no cover - depends on host
        return None, str(e)


def _discover():
    bl, err = _broadlink()
    if bl is None:
        raise RuntimeError("broadlink not installed: %s" % err)
    out = []
    for d in bl.discover(timeout=5):
        try:
            d.auth()
        except Exception:
            pass
        out.append({
            "host": d.host[0] if isinstance(d.host, (list, tuple)) else str(d.host),
            "mac": d.mac.hex() if hasattr(d.mac, "hex") else str(d.mac),
            "type": getattr(d, "type", ""),
            "name": getattr(d, "name", ""),
        })
    return out


def _device(host):
    bl, err = _broadlink()
    if bl is None:
        raise RuntimeError("broadlink not installed: %s" % err)
    dev = bl.hello(host) if hasattr(bl, "hello") else None
    if dev is None:
        # Fall back to discovery and match by host.
        for d in bl.discover(timeout=5):
            dh = d.host[0] if isinstance(d.host, (list, tuple)) else str(d.host)
            if dh == host:
                dev = d
                break
    if dev is None:
        raise RuntimeError("no broadlink device at %s" % host)
    dev.auth()
    return dev


def _learn(host, timeout=30):
    dev = _device(host)
    dev.enter_learning()
    deadline = time.time() + timeout
    while time.time() < deadline:
        time.sleep(1)
        try:
            data = dev.check_data()
        except Exception:
            data = None
        if data:
            return base64.b64encode(data).decode("ascii")
    raise RuntimeError("learn timed out — no IR captured (point the remote and press a button)")


def _blast(host, code_b64):
    dev = _device(host)
    dev.send_data(base64.b64decode(code_b64))
    return True


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _reply(self, code, obj):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            bl, err = _broadlink()
            self._reply(200, {"ok": True, "broadlink": bl is not None, "error": err or ""})
        else:
            self._reply(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        try:
            req = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            self._reply(400, {"error": "bad json: %s" % e})
            return
        try:
            if self.path == "/scan":
                self._reply(200, {"devices": _discover()})
            elif self.path == "/learn":
                self._reply(200, {"code": _learn(req.get("host", ""), int(req.get("timeout", 30)))})
            elif self.path == "/blast":
                _blast(req.get("host", ""), req.get("code", ""))
                self._reply(200, {"ok": True})
            else:
                self._reply(404, {"error": "not found"})
        except Exception as e:
            self._reply(500, {"error": str(e)})


def main():
    port = 17646
    args = sys.argv[1:]
    if "--port" in args:
        port = int(args[args.index("--port") + 1])
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
