#!/usr/bin/env python3
"""yaver_atv_bridge.py — pyatv sidecar for the Yaver agent.

The Go agent (desktop/agent/appletv.go) embeds this file, extracts it to
~/.yaver/appletv/, and launches it as `python3 yaver_atv_bridge.py --port N`.
It speaks plain JSON over HTTP on 127.0.0.1 only (never a LAN-reachable port) —
the same local-HTTP sidecar contract the arm sim harness uses
(arm/sim_harness.py), so it fits existing supervision/readiness patterns.

The bridge is STATELESS: the Go side owns pairing credentials (in the vault)
and passes them in each request body as `credentials` (a per-protocol dict).
This bridge never reads the vault and never persists anything.

pyatv is imported lazily so the process still starts (and /healthz still
answers `{"pyatv": false}`) when pyatv isn't installed — the agent's
`yaver doctor` surfaces that as an actionable install hint.

Endpoints (all POST JSON unless noted):
  GET  /healthz                         -> {ok, pyatv, version}
  POST /scan        {timeout?}          -> {devices:[{name,identifier,address,...}]}
  POST /pair_begin  {identifier, protocol?} -> {session, device_provides_pin}
  POST /pair_finish {session, pin}      -> {credentials:{...}}
  POST /remote_key  {address, credentials, key}
  POST /transport   {address, credentials, action}
  POST /power       {address, credentials, state}
  POST /seek        {address, credentials, seconds}
  POST /launch_app  {address, credentials, bundle_id}
  POST /now_playing {address, credentials} -> {title, artist, app, position, total,
                                               state, artwork_b64?, mimetype?}

No HDMI, no IR — pyatv drives the Apple TV over MRP/AirPlay/Companion on the LAN.
"""
import argparse
import asyncio
import base64
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

try:
    import pyatv
    from pyatv import scan as atv_scan, connect as atv_connect, pair as atv_pair
    from pyatv.const import Protocol
    _PYATV = True
    _PYATV_ERR = ""
except Exception as e:  # ImportError or transitive failure
    _PYATV = False
    _PYATV_ERR = str(e)


def _run(coro):
    """Run an async pyatv coroutine on a fresh event loop (per request)."""
    loop = asyncio.new_event_loop()
    try:
        return loop.run_until_complete(coro)
    finally:
        loop.close()


# Pairing sessions are short-lived and held in-process between begin/finish.
_PAIR_SESSIONS = {}
_PAIR_SEQ = [0]
_PAIR_LOCK = threading.Lock()


def _proto(name):
    name = (name or "MRP").lower()
    table = {
        "mrp": Protocol.MRP,
        "airplay": Protocol.AirPlay,
        "companion": Protocol.Companion,
        "raop": Protocol.RAOP,
    }
    return table.get(name, Protocol.MRP)


def _creds_to_dict(config):
    """Serialize a paired config's credentials, keyed by protocol name."""
    out = {}
    for service in config.services:
        if service.credentials:
            out[service.protocol.name] = service.credentials
    return out


async def _connect(loop, address, credentials):
    results = await atv_scan(loop, hosts=[address], timeout=5)
    if not results:
        raise RuntimeError("Apple TV not found at %s" % address)
    config = results[0]
    if credentials:
        for proto_name, cred in credentials.items():
            try:
                config.set_credentials(_proto(proto_name), cred)
            except Exception:
                pass
    return await atv_connect(config, loop)


async def do_scan(timeout):
    loop = asyncio.get_event_loop()
    results = await atv_scan(loop, timeout=timeout or 5)
    devices = []
    for r in results:
        devices.append({
            "name": r.name,
            "identifier": r.identifier,
            "address": str(r.address),
            "model": str(getattr(r.device_info, "model", "")),
            "services": [s.protocol.name for s in r.services],
        })
    return {"devices": devices}


async def do_pair_begin(identifier, protocol):
    loop = asyncio.get_event_loop()
    results = await atv_scan(loop, identifier=identifier, timeout=5)
    if not results:
        raise RuntimeError("device %s not found" % identifier)
    config = results[0]
    pairing = await atv_pair(config, _proto(protocol), loop)
    await pairing.begin()
    with _PAIR_LOCK:
        _PAIR_SEQ[0] += 1
        sid = "p%d" % _PAIR_SEQ[0]
        _PAIR_SESSIONS[sid] = (pairing, config)
    return {"session": sid, "device_provides_pin": pairing.device_provides_pin}


async def do_pair_finish(session, pin):
    with _PAIR_LOCK:
        entry = _PAIR_SESSIONS.pop(session, None)
    if not entry:
        raise RuntimeError("unknown pairing session %s" % session)
    pairing, config = entry
    if pin is not None:
        pairing.pin(int(pin))
    await pairing.finish()
    creds = _creds_to_dict(config) if pairing.has_paired else {}
    await pairing.close()
    if not creds:
        raise RuntimeError("pairing did not complete")
    return {"credentials": creds}


async def do_control(address, credentials, fn):
    loop = asyncio.get_event_loop()
    atv = await _connect(loop, address, credentials)
    try:
        return await fn(atv)
    finally:
        atv.close()


async def _key(atv, key):
    rc = atv.remote_control
    table = {
        "up": rc.up, "down": rc.down, "left": rc.left, "right": rc.right,
        "select": rc.select, "menu": rc.menu, "home": rc.home,
        "play": rc.play, "pause": rc.pause, "stop": rc.stop,
        "next": rc.next, "previous": rc.previous,
        "play_pause": rc.play_pause, "volume_up": rc.volume_up,
        "volume_down": rc.volume_down,
    }
    f = table.get(key)
    if not f:
        raise RuntimeError("unknown key %r" % key)
    await f()
    return {"ok": True, "key": key}


async def _power(atv, state):
    if state == "on":
        await atv.power.turn_on()
    else:
        await atv.power.turn_off()
    return {"ok": True, "power": state}


async def _seek(atv, seconds):
    await atv.remote_control.set_position(int(seconds))
    return {"ok": True, "position": int(seconds)}


async def _launch(atv, bundle_id):
    await atv.apps.launch_app(bundle_id)
    return {"ok": True, "launched": bundle_id}


async def _now_playing(atv):
    pl = await atv.metadata.playing()
    out = {
        "title": getattr(pl, "title", None),
        "artist": getattr(pl, "artist", None),
        "album": getattr(pl, "album", None),
        "app": getattr(getattr(atv, "metadata", None), "app", None) and atv.metadata.app.name,
        "state": str(getattr(pl, "device_state", "")),
        "position": getattr(pl, "position", None),
        "total": getattr(pl, "total_time", None),
    }
    try:
        artwork = await atv.metadata.artwork(width=400, height=400)
        if artwork and artwork.bytes:
            out["artwork_b64"] = base64.b64encode(artwork.bytes).decode("ascii")
            out["mimetype"] = artwork.mimetype or "image/jpeg"
    except Exception:
        pass
    return out


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass  # silence; the Go side logs

    def _send(self, code, obj):
        body = json.dumps(obj).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            self._send(200, {"ok": True, "pyatv": _PYATV,
                             "version": getattr(pyatv, "const", None) and pyatv.const.MAJOR_VERSION if _PYATV else None,
                             "error": _PYATV_ERR})
            return
        self._send(404, {"error": "not found"})

    def do_POST(self):
        if not _PYATV:
            self._send(503, {"error": "pyatv not installed: %s" % _PYATV_ERR})
            return
        try:
            n = int(self.headers.get("Content-Length", 0))
            body = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            self._send(400, {"error": "bad json: %s" % e})
            return
        try:
            result = self._dispatch(self.path, body)
            self._send(200, result)
        except Exception as e:
            self._send(500, {"error": str(e)})

    def _dispatch(self, path, b):
        if path == "/scan":
            return _run(do_scan(b.get("timeout")))
        if path == "/pair_begin":
            return _run(do_pair_begin(b["identifier"], b.get("protocol")))
        if path == "/pair_finish":
            return _run(do_pair_finish(b["session"], b.get("pin")))
        addr, creds = b.get("address"), b.get("credentials") or {}
        if path == "/remote_key":
            return _run(do_control(addr, creds, lambda a: _key(a, b["key"])))
        if path == "/transport":
            return _run(do_control(addr, creds, lambda a: _key(a, b["action"])))
        if path == "/power":
            return _run(do_control(addr, creds, lambda a: _power(a, b["state"])))
        if path == "/seek":
            return _run(do_control(addr, creds, lambda a: _seek(a, b["seconds"])))
        if path == "/launch_app":
            return _run(do_control(addr, creds, lambda a: _launch(a, b["bundle_id"])))
        if path == "/now_playing":
            return _run(do_control(addr, creds, _now_playing))
        raise RuntimeError("unknown endpoint %s" % path)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, required=True)
    args = ap.parse_args()
    srv = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    sys.stderr.write("yaver_atv_bridge listening on 127.0.0.1:%d (pyatv=%s)\n" % (args.port, _PYATV))
    sys.stderr.flush()
    srv.serve_forever()


if __name__ == "__main__":
    main()
