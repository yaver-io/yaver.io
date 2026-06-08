#!/usr/bin/env python3
"""
parol6_bridge.py — thin JSON-over-HTTP shim that lets Yaver's arm `bridge`
driver control a PAROL6 arm through its OFFICIAL stack (the headless_commander
UDP client), instead of Yaver re-implementing PAROL6's undocumented steps-based
serial streaming. Run this next to the arm; point Yaver's arm config at it:

    arm_config_set { driver: "parol6", addr: "http://127.0.0.1:5056" }

It exposes exactly the endpoints the Go BridgeArmBackend calls:
    GET  /describe                 -> ArmInfo (6-DOF table)
    GET  /state                    -> {joints:[{name,position,unit}], pose:{...}}
    POST /enable   {on}
    POST /movej    {targets:{J1:..}, velPct, accPct}
    POST /movel    {pose:{x,y,z,roll,pitch,yaw}, velPct, accPct}
    POST /home     {}
    POST /freedrive{on}
    POST /stop     {} ; POST /estop {}

Wire the TODO calls to the PAROL6 Python API (PCrnjak/PAROL6-python-API). The
method names below mirror that client (moveJ / moveL / home / get_angles /
get_pose / halt). This file is the integration seam — adjust to your client
version; everything above it (Yaver UI, teach/repeat, camera, host MCP) is
robot-agnostic and unchanged.
"""
import json
import http.server

# from parol6 import Client          # PCrnjak/PAROL6-python-API
# robot = Client(host="127.0.0.1", port=5001)

JOINTS = ["J1", "J2", "J3", "J4", "J5", "J6"]
LIMITS = [(-123, 123), (-145, 0), (-148, 148), (-120, 120), (-120, 120), (-360, 360)]  # PAROL6 (deg) — verify

def describe():
    return {
        "model": "PAROL6", "vendor": "Source Robotics", "dof": 6, "hasCartesian": True,
        "poseFrame": "base", "source": "config",
        "joints": [{"name": n, "type": "revolute", "min": lo, "max": hi, "unit": "deg"}
                   for n, (lo, hi) in zip(JOINTS, LIMITS)],
    }

def get_state():
    # angles = robot.get_angles(); pose = robot.get_pose()
    angles = [0.0] * 6   # TODO: angles = robot.get_angles()
    pose = None          # TODO: x,y,z,rx,ry,rz = robot.get_pose(); pose = {...}
    js = [{"name": n, "position": a, "unit": "deg"} for n, a in zip(JOINTS, angles)]
    return {"joints": js, "pose": pose}

def move_j(targets, vel, acc):
    order = [targets.get(n, None) for n in JOINTS]
    _ = (order, vel, acc)
    # robot.moveJ([order...], speed=vel/100, accel=acc/100)   # TODO
    return {"ok": True}

def move_l(pose, vel, acc):
    _ = (pose, vel, acc)
    # robot.moveL([pose.x, pose.y, pose.z, pose.roll, pose.pitch, pose.yaw], speed=vel/100)  # TODO
    return {"ok": True}

class H(http.server.BaseHTTPRequestHandler):
    def _send(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        return json.loads(self.rfile.read(n) or b"{}")

    def do_GET(self):
        if self.path == "/describe":
            return self._send(describe())
        if self.path == "/state":
            return self._send(get_state())
        return self._send({"ok": False, "error": "unknown"}, 404)

    def do_POST(self):
        try:
            b = self._body()
            if self.path == "/enable":
                # robot.enable(b.get("on", True))
                return self._send({"ok": True})
            if self.path == "/movej":
                return self._send(move_j(b.get("targets", {}), b.get("velPct", 30), b.get("accPct", 30)))
            if self.path == "/movel":
                return self._send(move_l(b.get("pose", {}), b.get("velPct", 30), b.get("accPct", 30)))
            if self.path == "/home":
                # robot.home(wait=True)
                return self._send({"ok": True})
            if self.path == "/freedrive":
                # robot.freedrive(b.get("on", True))   # leadthrough / learning mode
                return self._send({"ok": True})
            if self.path in ("/stop", "/estop"):
                # robot.halt()
                return self._send({"ok": True})
            return self._send({"ok": False, "error": "unknown"}, 404)
        except Exception as e:  # noqa
            return self._send({"ok": False, "error": str(e)}, 500)

    def log_message(self, *_):  # quiet
        pass

if __name__ == "__main__":
    http.server.HTTPServer(("127.0.0.1", 5056), H).serve_forever()
