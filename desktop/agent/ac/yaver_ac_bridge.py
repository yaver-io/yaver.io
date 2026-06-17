#!/usr/bin/env python3
"""yaver_ac_bridge.py — local WiFi air-conditioner control sidecar.

Same shape as the IR/pyatv bridges: a stdlib HTTP server on 127.0.0.1 the Go
side (ac.go) drives over JSON. LOCAL control first (no vendor cloud): Tuya-local
via `tinytuya` (a huge share of cheap WiFi ACs are Tuya OEM). Gree-local is a
documented protocol; wired here as best-effort if a `greeclimate`-style lib is
present, else reported unavailable so the Go side can surface a hint.

Endpoints (127.0.0.1 only):
  GET  /healthz                 -> {ok, tinytuya: bool, gree: bool}
  POST /set    {kind, host, devid?, localkey?, version?, state{}, dps?{}} -> {ok, result}
  POST /status {kind, host, devid?, localkey?, version?}                  -> {state|dps}

Tuya DPS indices are device-specific; the default mapping below covers common
AC firmwares, and an explicit `dps` object overrides it. Credentials are passed
per-call by the Go side (from the vault) and never stored here.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def _tinytuya():
    try:
        import tinytuya  # type: ignore
        return tinytuya, None
    except Exception as e:  # pragma: no cover
        return None, str(e)


def _gree():
    try:
        import greeclimate  # type: ignore  # noqa: F401
        return True, None
    except Exception as e:  # pragma: no cover
        return False, str(e)


# Common Tuya AC DPS map (override per device with an explicit `dps`).
TUYA_DEFAULT_DPS = {"power": "1", "temp": "2", "mode": "4", "fan": "5", "swing": "104"}


def _tuya_device(host, devid, localkey, version):
    tt, err = _tinytuya()
    if tt is None:
        raise RuntimeError("tinytuya not installed: %s" % err)
    d = tt.Device(devid, host, localkey)
    try:
        d.set_version(float(version or 3.3))
    except Exception:
        pass
    return d


def _tuya_set(req):
    d = _tuya_device(req.get("host"), req.get("devid"), req.get("localkey"), req.get("version"))
    dps = dict(req.get("dps") or {})
    state = req.get("state") or {}
    m = dict(TUYA_DEFAULT_DPS)
    m.update(req.get("dpsMap") or {})
    if "power" in state:
        dps[m["power"]] = bool(state["power"])
    if "temp" in state:
        dps[m["temp"]] = int(state["temp"])
    if "mode" in state:
        dps[m["mode"]] = str(state["mode"])
    if "fan" in state:
        dps[m["fan"]] = str(state["fan"])
    if "swing" in state:
        dps[m["swing"]] = bool(state["swing"])
    results = {}
    for idx, val in dps.items():
        results[idx] = d.set_value(int(idx), val)
    return {"ok": True, "result": results}


def _tuya_status(req):
    d = _tuya_device(req.get("host"), req.get("devid"), req.get("localkey"), req.get("version"))
    return {"dps": d.status().get("dps", {})}


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
            tt, _ = _tinytuya()
            gree_ok, _ = _gree()
            self._reply(200, {"ok": True, "tinytuya": tt is not None, "gree": gree_ok})
        else:
            self._reply(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        try:
            req = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            self._reply(400, {"error": "bad json: %s" % e})
            return
        kind = (req.get("kind") or "tuya").lower()
        try:
            if kind != "tuya":
                self._reply(501, {"error": "kind %r not implemented in this bridge yet" % kind})
                return
            if self.path == "/set":
                self._reply(200, _tuya_set(req))
            elif self.path == "/status":
                self._reply(200, _tuya_status(req))
            else:
                self._reply(404, {"error": "not found"})
        except Exception as e:
            self._reply(500, {"error": str(e)})


def main():
    port = 17647
    args = sys.argv[1:]
    if "--port" in args:
        port = int(args[args.index("--port") + 1])
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
