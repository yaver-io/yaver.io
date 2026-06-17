#!/usr/bin/env python3
"""yaver_atv2_bridge.py — Android TV Remote v2 sidecar (Mi Box / Google TV).

Same shape as the pyatv/IR/AC bridges: a stdlib HTTP server on 127.0.0.1 the Go
side (androidtv2.go) drives over JSON. Backend = the `androidtvremote2` library
(the SAME protocol the Google TV phone app uses, TLS 6466/6467) — NO adb, NO
developer mode, survives reboots, and gives real power/keys. The lib is imported
lazily so /healthz reports availability without crashing when it isn't installed.

Pairing persists a per-host cert/key under <bridge-dir>/certs so a paired TV
stays paired across restarts. Credentials never leave the box.

Endpoints (127.0.0.1 only):
  GET  /healthz                  -> {ok, atv2: bool}
  POST /pair_begin  {host}       -> {ok}        (TV shows a code)
  POST /pair_finish {host, code} -> {ok}
  POST /key   {host, key}        -> {ok}        (key = DPAD_UP/HOME/POWER/…)
  POST /launch{host, app}        -> {ok}        (app = a deep link / app id)
"""
import asyncio
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

CERTS_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "certs")


def _lib():
    try:
        import androidtvremote2  # type: ignore
        return androidtvremote2, None
    except Exception as e:  # pragma: no cover
        return None, str(e)


def _paths(host):
    os.makedirs(CERTS_DIR, exist_ok=True)
    safe = host.replace(":", "_").replace("/", "_")
    return os.path.join(CERTS_DIR, safe + ".crt"), os.path.join(CERTS_DIR, safe + ".key")


def _remote(host):
    lib, err = _lib()
    if lib is None:
        raise RuntimeError("androidtvremote2 not installed: %s" % err)
    cert, key = _paths(host)
    return lib.AndroidTVRemote("Yaver", cert, key, host)


async def _pair_begin(host):
    r = _remote(host)
    await r.async_generate_cert_if_missing()
    await r.async_start_pairing()
    return {"ok": True}


async def _pair_finish(host, code):
    r = _remote(host)
    await r.async_finish_pairing(str(code))
    return {"ok": True}


async def _key(host, key):
    r = _remote(host)
    await r.async_connect()
    r.send_key_command(key)
    return {"ok": True}


async def _launch(host, app):
    r = _remote(host)
    await r.async_connect()
    r.send_launch_app_command(app)
    return {"ok": True}


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
            lib, _ = _lib()
            self._reply(200, {"ok": True, "atv2": lib is not None})
        else:
            self._reply(404, {"error": "not found"})

    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        try:
            req = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            self._reply(400, {"error": "bad json: %s" % e})
            return
        host = req.get("host", "")
        try:
            if self.path == "/pair_begin":
                self._reply(200, asyncio.run(_pair_begin(host)))
            elif self.path == "/pair_finish":
                self._reply(200, asyncio.run(_pair_finish(host, req.get("code", ""))))
            elif self.path == "/key":
                self._reply(200, asyncio.run(_key(host, req.get("key", ""))))
            elif self.path == "/launch":
                self._reply(200, asyncio.run(_launch(host, req.get("app", ""))))
            else:
                self._reply(404, {"error": "not found"})
        except Exception as e:
            self._reply(500, {"error": str(e)})


def main():
    port = 17648
    args = sys.argv[1:]
    if "--port" in args:
        port = int(args[args.index("--port") + 1])
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()


if __name__ == "__main__":
    main()
