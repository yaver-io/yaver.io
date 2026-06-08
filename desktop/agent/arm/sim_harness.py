#!/usr/bin/env python3
"""yaver sim harness — a headless robot-arm simulator exposed over the same
JSON-over-HTTP contract Yaver's bridge driver speaks, plus a rendered-frame
endpoint so the existing camera/vision path shows the arm moving.

Yaver SPAWNS this (it is embedded in the agent and extracted to ~/.yaver/sim),
so users never run it by hand. Engine is PyBullet (zlib licence, pip-installable
on x86 + ARM, GPU-free TinyRenderer for headless frames). A "mujoco" engine is a
future drop-in behind the same HTTP contract.

Endpoints (all JSON unless noted; failures => {"ok": false, "error": ...}):
    GET  /healthz        -> {"ok": true, "engine": ...}
    GET  /describe       -> ArmInfo {model,vendor,dof,joints[],hasCartesian,...}
    GET  /state          -> {joints:[{name,position,unit}], pose:{x,y,z,r,p,y}}
    GET  /frame.jpg      -> image/jpeg (TinyRenderer, no GPU needed)
    POST /enable  {on}
    POST /movej   {targets:{joint:val}, velPct, accPct}
    POST /movel   {pose:{x,y,z,roll,pitch,yaw}, velPct}        (IK)
    POST /home    {velPct}
    POST /stop    {}
    POST /estop   {}
    POST /freedrive {on}                                       (no-op ok in sim)
    POST /reset   {}                                           (re-home)
    POST /load    {model}        -> {ok, info}                 (swap robot)
    POST /raw     {cmd}          -> {ok, reply}

Model load tokens (Yaver's SimSource):
    builtin:arm6        procedural 6-DOF arm — no asset, no download
    pybullet:<path>     a URDF bundled in pybullet_data (kuka_iiwa, franka_panda)
    desc:<name>         via robot_descriptions.py (ur5e, ur10e, iiwa14, gen3, ...)
    urdf:<path-or-url>  any URDF on disk or http(s)

Deps: pybullet (required), numpy + pillow (for /frame.jpg; a placeholder frame is
served if pillow is missing so the camera path never breaks).
"""
import argparse
import io
import json
import math
import os
import sys
import tempfile
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

RAD2DEG = 180.0 / math.pi
DEG2RAD = math.pi / 180.0

# A 1x1 grey JPEG so /frame.jpg always returns a valid image (SOI ff d8 ff)
# even when pillow is unavailable — the Go camera validates the JPEG marker.
_PLACEHOLDER_JPEG = bytes.fromhex(
    "ffd8ffe000104a46494600010100000100010000ffdb004300080606070605080707"
    "07090908"
    "0a0c140d0c0b0b0c1912130f141d1a1f1e1d1a1c1c20242e2720222c231c1c283729"
    "2c30313434341f27393d38323c2e333432ffc0000b080001000101011100ffc40014"
    "00010000000000000000000000000000000bffc4001410010000000000000000000000"
    "00000000ffda0008010100003f00d2cf20ffd9"
)


class Sim:
    def __init__(self, engine, gui):
        import pybullet as p  # noqa: imported lazily so /healthz can report the error
        import pybullet_data
        self.p = p
        self.engine = engine or "pybullet"
        mode = p.GUI if gui else p.DIRECT
        self.cid = p.connect(mode)
        p.setAdditionalSearchPath(pybullet_data.getDataPath())
        p.setGravity(0, 0, -9.81)
        self.lock = threading.Lock()
        self.body = None
        self.model_name = ""
        self.vendor = "Simulator"
        self.joints = []        # list of dicts: idx,name,type,min,max,home,unit,maxVel,maxEffort
        self.enabled = True
        self.estopped = False
        self._load_plane()

    def _load_plane(self):
        try:
            self.p.loadURDF("plane.urdf")
        except Exception:
            pass

    # ---- model loading -------------------------------------------------
    def load(self, token):
        p = self.p
        token = (token or "builtin:arm6").strip()
        with self.lock:
            if self.body is not None:
                try:
                    p.removeBody(self.body)
                except Exception:
                    pass
                self.body = None
            urdf_path, name = self._resolve(token)
            if urdf_path == "__builtin_arm6__":
                self.body = self._build_arm6()
                name = "Generic 6-DOF (built-in)"
            else:
                self.body = p.loadURDF(
                    urdf_path, useFixedBase=True,
                    flags=p.URDF_USE_INERTIA_FROM_FILE,
                )
            self.model_name = name
            self._index_joints()
            self.estopped = False
            self._home_now()
        return self.describe()

    def _resolve(self, token):
        """token -> (urdf_path_or_sentinel, display_name)"""
        if token.startswith("builtin:"):
            return "__builtin_arm6__", token
        if token.startswith("pybullet:"):
            rel = token[len("pybullet:"):]
            return rel, os.path.basename(rel)
        if token.startswith("urdf:"):
            ref = token[len("urdf:"):]
            if ref.startswith("http://") or ref.startswith("https://"):
                data = urllib.request.urlopen(ref, timeout=30).read()
                fd, tmp = tempfile.mkstemp(suffix=".urdf")
                os.write(fd, data)
                os.close(fd)
                return tmp, os.path.basename(ref)
            return ref, os.path.basename(ref)
        if token.startswith("desc:"):
            name = token[len("desc:"):]
            return self._resolve_description(name), name
        # bare path
        return token, os.path.basename(token)

    def _resolve_description(self, name):
        """Resolve a robot_descriptions.py URDF_PATH (downloads + caches on first
        import). We use the path (not the loader) so we keep control of
        fixed-base / inertia flags."""
        alias = {
            "ur5e": "ur5e_description",
            "ur10e": "ur10e_description",
            "ur3e": "ur3e_description",
            "iiwa14": "iiwa14_description",
            "gen3": "gen3_description",
            "panda": "panda_description",
        }
        mod = alias.get(name, name if name.endswith("_description") else name + "_description")
        try:
            m = __import__("robot_descriptions." + mod, fromlist=["URDF_PATH"])
        except Exception as e:
            raise RuntimeError(
                "model %r needs robot_descriptions.py: pip install robot_descriptions (%s)" % (name, e))
        return getattr(m, "URDF_PATH")

    def _build_arm6(self):
        """A procedural 6-DOF revolute arm via createMultiBody — zero assets."""
        p = self.p
        n = 6
        link_len = 0.18
        masses = [1.0] * n
        col = [p.createCollisionShape(p.GEOM_BOX, halfExtents=[0.03, 0.03, link_len / 2]) for _ in range(n)]
        vis = [p.createVisualShape(p.GEOM_BOX, halfExtents=[0.03, 0.03, link_len / 2],
                                   rgbaColor=[0.2, 0.5, 0.9, 1]) for _ in range(n)]
        positions = [[0, 0, link_len]] * n
        orients = [[0, 0, 0, 1]] * n
        inertial = [[0, 0, 0]] * n
        inertial_orn = [[0, 0, 0, 1]] * n
        parents = list(range(n))           # 0..n-1 -> chain
        # alternate revolute axes so it looks like a real arm (z,y,y,y,z,y)
        axes = [[0, 0, 1], [0, 1, 0], [0, 1, 0], [0, 1, 0], [0, 0, 1], [0, 1, 0]]
        jtypes = [p.JOINT_REVOLUTE] * n
        base = p.createMultiBody(
            baseMass=0, baseCollisionShapeIndex=-1, baseVisualShapeIndex=-1,
            basePosition=[0, 0, 0.05],
            linkMasses=masses, linkCollisionShapeIndices=col, linkVisualShapeIndices=vis,
            linkPositions=positions, linkOrientations=orients,
            linkInertialFramePositions=inertial, linkInertialFrameOrientations=inertial_orn,
            linkParentIndices=parents, linkJointTypes=jtypes, linkJointAxis=axes,
        )
        return base

    def _index_joints(self):
        p = self.p
        self.joints = []
        for i in range(p.getNumJoints(self.body)):
            ji = p.getJointInfo(self.body, i)
            jtype = ji[2]
            if jtype not in (p.JOINT_REVOLUTE, p.JOINT_PRISMATIC):
                continue
            name = ji[1].decode("utf-8", "replace") if isinstance(ji[1], bytes) else str(ji[1])
            lower, upper = ji[8], ji[9]
            max_force, max_vel = ji[10], ji[11]
            prismatic = jtype == p.JOINT_PRISMATIC
            if lower > upper:  # unlimited / continuous
                jmin, jmax, jt = (-0.8, 0.8, "prismatic") if prismatic else (-360.0, 360.0, "continuous")
                lower, upper = None, None
            else:
                scale = 1000.0 if prismatic else RAD2DEG
                jmin, jmax = lower * scale, upper * scale
                jt = "prismatic" if prismatic else "revolute"
            unit = "mm" if prismatic else "deg"
            home = 0.0 if (jmin <= 0 <= jmax) else (jmin + jmax) / 2.0
            self.joints.append({
                "idx": i, "name": name or ("J%d" % (len(self.joints) + 1)),
                "type": jt, "min": jmin, "max": jmax, "home": home, "unit": unit,
                "maxVel": (max_vel * (1000.0 if prismatic else RAD2DEG)) if max_vel else 0.0,
                "maxEffort": max_force or 0.0,
                "_lower": lower, "_upper": upper, "_prismatic": prismatic,
            })

    # ---- conversions ---------------------------------------------------
    def _to_native(self, j, val):
        return val / 1000.0 if j["_prismatic"] else val * DEG2RAD

    def _from_native(self, j, native):
        return native * 1000.0 if j["_prismatic"] else native * RAD2DEG

    # ---- control -------------------------------------------------------
    def _home_now(self):
        p = self.p
        for j in self.joints:
            native = self._to_native(j, j["home"])
            p.resetJointState(self.body, j["idx"], native)
        self._apply_targets({j["name"]: j["home"] for j in self.joints}, 50)

    def _apply_targets(self, targets, vel_pct):
        p = self.p
        vel_pct = max(1, min(100, vel_pct or 30))
        for j in self.joints:
            if j["name"] not in targets:
                continue
            native = self._to_native(j, float(targets[j["name"]]))
            max_v = j["maxVel"] and (j["maxVel"] * (1.0 / (1000.0 if j["_prismatic"] else RAD2DEG)))
            kwargs = {"controlMode": p.POSITION_CONTROL, "targetPosition": native,
                      "force": j["maxEffort"] or 200.0}
            if max_v:
                kwargs["maxVelocity"] = max_v * (vel_pct / 100.0)
            p.setJointMotorControl2(self.body, j["idx"], **kwargs)

    def _settled(self, targets, tol_native=0.01):
        p = self.p
        for j in self.joints:
            if j["name"] not in targets:
                continue
            cur = p.getJointState(self.body, j["idx"])[0]
            want = self._to_native(j, float(targets[j["name"]]))
            if abs(cur - want) > (tol_native if not j["_prismatic"] else 0.002):
                return False
        return True

    def movej(self, targets, vel_pct=30, acc_pct=30):
        if self.estopped:
            raise RuntimeError("e-stopped; reset first")
        if not self.enabled:
            raise RuntimeError("arm disabled; enable first")
        with self.lock:
            self._apply_targets(targets, vel_pct)
        # block until settled (the bridge contract: move endpoints block)
        deadline = time.time() + 20.0
        while time.time() < deadline:
            with self.lock:
                if self._settled(targets):
                    return
            time.sleep(0.02)

    def movel(self, pose, vel_pct=30):
        p = self.p
        if not self.joints:
            raise RuntimeError("no robot loaded")
        ee = self.joints[-1]["idx"]
        pos = [pose.get("x", 0) / 1000.0, pose.get("y", 0) / 1000.0, pose.get("z", 0) / 1000.0]
        orn = p.getQuaternionFromEuler([
            pose.get("roll", 0) * DEG2RAD, pose.get("pitch", 0) * DEG2RAD, pose.get("yaw", 0) * DEG2RAD])
        with self.lock:
            sol = p.calculateInverseKinematics(self.body, ee, pos, orn)
        targets = {}
        for k, j in enumerate(self.joints):
            if k < len(sol):
                targets[j["name"]] = self._from_native(j, sol[k])
        self.movej(targets, vel_pct)

    def home(self, vel_pct=40):
        self.movej({j["name"]: j["home"] for j in self.joints}, vel_pct)

    def stop(self):
        with self.lock:
            cur = {j["name"]: self._from_native(j, self.p.getJointState(self.body, j["idx"])[0])
                   for j in self.joints}
            self._apply_targets(cur, 100)

    def estop(self):
        self.estopped = True
        self.enabled = False
        self.stop()

    def reset(self):
        with self.lock:
            self.estopped = False
            self.enabled = True
            self._home_now()

    # ---- readback ------------------------------------------------------
    def describe(self):
        return {
            "model": self.model_name, "vendor": self.vendor,
            "dof": len(self.joints), "hasCartesian": True, "poseFrame": "base",
            "source": "robot",
            "joints": [{
                "name": j["name"], "type": j["type"], "min": j["min"], "max": j["max"],
                "home": j["home"], "unit": j["unit"], "maxVel": j["maxVel"],
                "maxEffort": j["maxEffort"],
            } for j in self.joints],
        }

    def state(self):
        p = self.p
        with self.lock:
            joints = [{
                "name": j["name"],
                "position": round(self._from_native(j, p.getJointState(self.body, j["idx"])[0]), 4),
                "unit": j["unit"],
            } for j in self.joints]
            pose = None
            if self.joints:
                ls = p.getLinkState(self.body, self.joints[-1]["idx"], computeForwardKinematics=True)
                px, py, pz = ls[0]
                roll, pitch, yaw = p.getEulerFromQuaternion(ls[1])
                pose = {"x": round(px * 1000, 2), "y": round(py * 1000, 2), "z": round(pz * 1000, 2),
                        "roll": round(roll * RAD2DEG, 2), "pitch": round(pitch * RAD2DEG, 2),
                        "yaw": round(yaw * RAD2DEG, 2)}
        return {"joints": joints, "pose": pose}

    def frame(self, w=480, h=360):
        p = self.p
        with self.lock:
            view = p.computeViewMatrixFromYawPitchRoll(
                cameraTargetPosition=[0, 0, 0.4], distance=1.6, yaw=50, pitch=-30, roll=0, upAxisIndex=2)
            proj = p.computeProjectionMatrixFOV(fov=60, aspect=w / h, nearVal=0.05, farVal=5.0)
            img = p.getCameraImage(w, h, view, proj, renderer=p.ER_TINY_RENDERER)
        rgb = img[2]
        try:
            import numpy as np
            from PIL import Image
            arr = np.reshape(np.array(rgb, dtype=np.uint8), (h, w, 4))[:, :, :3]
            buf = io.BytesIO()
            Image.fromarray(arr).save(buf, format="JPEG", quality=80)
            return buf.getvalue()
        except Exception:
            return _PLACEHOLDER_JPEG

    def step_forever(self):
        while True:
            with self.lock:
                if self.body is not None:
                    self.p.stepSimulation()
            time.sleep(1.0 / 240.0)


class KinematicSim:
    """A no-physics, no-GPU engine: joints integrate instantly toward targets and
    a simple frame is rendered with PIL. It needs no pybullet, so the sim runs on
    ANY machine (a dev Mac, CI, a headless box without a GPU) — the dry-run twin
    for proving a policy/teach loop before touching pybullet or hardware. Speaks
    the same internal interface the Handler calls, so it's a drop-in for Sim."""

    def __init__(self):
        self.engine = "kinematic"
        self.lock = threading.Lock()
        self.model_name = ""
        self.vendor = "Simulator"
        self.joints = []  # {name,type,min,max,home,unit,pos}
        self.enabled = True
        self.estopped = False

    def load(self, token):
        token = (token or "builtin:arm6").strip()
        # Without pybullet we can't parse arbitrary URDFs here, so every token
        # resolves to a generic 6-DOF arm (the Go side already prefilled the exact
        # joint table for catalog models; this is the dry-run kinematic stand-in).
        lim = [(-170, 170), (-120, 120), (-160, 160), (-170, 170), (-120, 120), (-175, 175)]
        with self.lock:
            self.model_name = token if token.startswith(("urdf:", "desc:", "pybullet:")) else "Generic 6-DOF (kinematic)"
            self.joints = [{
                "name": "J%d" % (i + 1), "type": "revolute", "min": lo, "max": hi,
                "home": 0.0, "unit": "deg", "maxVel": 180.0, "maxEffort": 0.0, "pos": 0.0,
            } for i, (lo, hi) in enumerate(lim)]
            self.estopped = False
        return self.describe()

    def describe(self):
        return {
            "model": self.model_name, "vendor": self.vendor, "dof": len(self.joints),
            "hasCartesian": False, "poseFrame": "base", "source": "robot",
            "joints": [{k: j[k] for k in ("name", "type", "min", "max", "home", "unit", "maxVel", "maxEffort")}
                       for j in self.joints],
        }

    def state(self):
        with self.lock:
            return {"joints": [{"name": j["name"], "position": round(j["pos"], 4), "unit": j["unit"]}
                              for j in self.joints], "pose": None}

    def movej(self, targets, vel_pct=30, acc_pct=30):
        if self.estopped:
            raise RuntimeError("e-stopped; reset first")
        if not self.enabled:
            raise RuntimeError("arm disabled; enable first")
        with self.lock:
            for j in self.joints:
                if j["name"] in targets:
                    j["pos"] = float(targets[j["name"]])  # instantaneous (kinematic)

    def movel(self, pose, vel_pct=30):
        raise RuntimeError("kinematic engine has no IK; use joint moves")

    def home(self, vel_pct=40):
        self.movej({j["name"]: j["home"] for j in self.joints}, vel_pct)

    def stop(self):
        pass

    def estop(self):
        self.estopped = True
        self.enabled = False

    def reset(self):
        with self.lock:
            self.estopped = True
            self.enabled = True
            for j in self.joints:
                j["pos"] = j["home"]
            self.estopped = False

    def frame(self, w=480, h=360):
        try:
            from PIL import Image, ImageDraw
            img = Image.new("RGB", (w, h), (24, 28, 36))
            d = ImageDraw.Draw(img)
            d.text((12, 10), "Yaver SIM (kinematic) — %s" % (self.model_name or "arm"), fill=(180, 200, 255))
            with self.lock:
                joints = list(self.joints)
            y = 50
            for j in joints:
                frac = 0.0
                rng = (j["max"] - j["min"]) or 1.0
                frac = (j["pos"] - j["min"]) / rng
                bx = 120 + int(frac * 300)
                d.text((12, y - 4), "%s %.1f%s" % (j["name"], j["pos"], j["unit"]), fill=(220, 220, 220))
                d.rectangle([120, y, 420, y + 8], outline=(80, 90, 110))
                d.rectangle([120, y, bx, y + 8], fill=(90, 160, 240))
                y += 36
            buf = io.BytesIO()
            img.save(buf, format="JPEG", quality=80)
            return buf.getvalue()
        except Exception:
            return _PLACEHOLDER_JPEG

    def step_forever(self):
        while True:
            time.sleep(0.5)  # nothing to integrate; keeps the thread parked


SIM = None
SIM_ERR = None
ENGINE = "pybullet"


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass  # quiet

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length", 0) or 0)
        if n <= 0:
            return {}
        try:
            return json.loads(self.rfile.read(n) or b"{}")
        except Exception:
            return {}

    def do_GET(self):
        try:
            if self.path == "/healthz":
                return self._json({"ok": SIM is not None, "engine": ENGINE,
                                   "error": SIM_ERR or ""})
            if SIM is None:
                return self._json({"ok": False, "error": SIM_ERR or "sim not ready"}, 503)
            if self.path == "/describe":
                return self._json(SIM.describe())
            if self.path == "/state":
                return self._json(SIM.state())
            if self.path.startswith("/frame.jpg"):
                jpg = SIM.frame()
                self.send_response(200)
                self.send_header("Content-Type", "image/jpeg")
                self.send_header("Content-Length", str(len(jpg)))
                self.end_headers()
                self.wfile.write(jpg)
                return
            return self._json({"ok": False, "error": "not found"}, 404)
        except Exception as e:
            return self._json({"ok": False, "error": str(e)}, 500)

    def do_POST(self):
        try:
            if SIM is None:
                return self._json({"ok": False, "error": SIM_ERR or "sim not ready"}, 503)
            b = self._body()
            path = self.path
            if path == "/enable":
                SIM.enabled = bool(b.get("on", True))
                return self._json({"ok": True})
            if path == "/movej":
                SIM.movej(b.get("targets", {}), b.get("velPct", 30), b.get("accPct", 30))
                return self._json({"ok": True})
            if path == "/movel":
                SIM.movel(b.get("pose", {}), b.get("velPct", 30))
                return self._json({"ok": True})
            if path == "/home":
                SIM.home(b.get("velPct", 40))
                return self._json({"ok": True})
            if path == "/stop":
                SIM.stop()
                return self._json({"ok": True})
            if path == "/estop":
                SIM.estop()
                return self._json({"ok": True})
            if path == "/freedrive":
                # sim has no hand-guiding; jog/teach works via direct joint sets.
                return self._json({"ok": True, "note": "freedrive is a no-op in sim; jog then capture"})
            if path == "/reset":
                SIM.reset()
                return self._json({"ok": True})
            if path == "/load":
                info = SIM.load(b.get("model", "builtin:arm6"))
                return self._json({"ok": True, "info": info})
            if path == "/raw":
                return self._json({"ok": True, "reply": "sim ignores raw: %s" % b.get("cmd", "")})
            return self._json({"ok": False, "error": "not found"}, 404)
        except Exception as e:
            return self._json({"ok": False, "error": str(e)}, 500)


def main():
    global SIM, SIM_ERR, ENGINE
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18092)
    ap.add_argument("--model", default="builtin:arm6")
    ap.add_argument("--engine", default="pybullet")
    ap.add_argument("--gui", action="store_true")
    args = ap.parse_args()
    want = (args.engine or "pybullet").strip()
    # Engine selection: explicit "kinematic" uses the no-physics engine; otherwise
    # try pybullet and FALL BACK to kinematic if it isn't installed — so the sim
    # always comes up (degraded, no contact physics) rather than failing hard.
    try:
        if want == "kinematic":
            SIM = KinematicSim()
        else:
            SIM = Sim(want, args.gui)
        ENGINE = SIM.engine
        SIM.load(args.model)
        threading.Thread(target=SIM.step_forever, daemon=True).start()
    except ImportError as e:
        print("sim: pybullet unavailable (%s) — falling back to the kinematic engine "
              "(no contact physics). pip install pybullet numpy pillow for full fidelity." % e, file=sys.stderr)
        SIM = KinematicSim()
        ENGINE = SIM.engine
        SIM.load(args.model)
        threading.Thread(target=SIM.step_forever, daemon=True).start()
    except Exception as e:
        SIM_ERR = str(e)
        print("sim: init failed: %s" % e, file=sys.stderr)
        sys.exit(3)

    srv = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print("sim: %s harness on 127.0.0.1:%d model=%s" % (ENGINE, args.port, args.model), file=sys.stderr)
    srv.serve_forever()


if __name__ == "__main__":
    main()
