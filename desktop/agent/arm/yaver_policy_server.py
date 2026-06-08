#!/usr/bin/env python3
"""yaver policy server — the reference implementation of the served-model contract
that arm/policy.go (PolicyClient / RunPolicy) talks to. In production this wraps a
LeRobot / ACT / Diffusion-Policy / π0 checkpoint trained from your demos and runs
on a rented GPU (Salad/DeepInfra/Modal/RunPod) or a local Jetson. This reference
needs only the Python stdlib so the whole pipeline is runnable on any box for a
dry-run.

Contract (see docs/yaver-video-to-policy-harness-cell.md):
    GET  /healthz -> 200 {ok:true, policy:...}
    POST /act  { images:{name:dataURL}, state:{joints:{J1:..}, pose?}, prompt? }
           ->  { actions:[ {joints:{J1:..}} , … ], done?:bool }

The default policy here is a transparent proportional controller: drive each joint
toward a --goal vector by a bounded step, emit a short ACTION CHUNK, raise `done`
when within tolerance. It is a faithful STAND-IN for a learned policy (same I/O
shape, same action-chunking) so you can exercise observe→infer→safety-gate→execute
end to end. To serve a real model instead, replace `predict()` below with a
LeRobot policy:

    from lerobot.common.policies.act.modeling_act import ACTPolicy
    policy = ACTPolicy.from_pretrained("you/act-seat-wire")
    # in predict(): build the obs tensors from `state`+decoded `images`,
    #   chunk = policy.select_action(obs)  → return [{"joints": {...}}, ...]
"""
import argparse
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

GOAL = {}
STEP = 8.0          # max deg moved per action
CHUNK = 5           # actions per inference (the "action chunk")
TOL = 1.0           # done when every goal joint is within this many deg


def predict(state, prompt):
    """obs → action chunk. Replace with a LeRobot policy for real inference."""
    joints = dict(state.get("joints") or {})
    goal = GOAL or {k: 0.0 for k in joints}  # default: drive home
    actions = []
    cur = dict(joints)
    for _ in range(CHUNK):
        step = {}
        moved = False
        for name, target in goal.items():
            c = cur.get(name, 0.0)
            d = target - c
            if abs(d) <= 1e-6:
                continue
            move = max(-STEP, min(STEP, d))
            step[name] = round(c + move, 4)
            cur[name] = step[name]
            moved = True
        if not moved:
            break
        actions.append({"joints": step})
    done = all(abs(goal[n] - cur.get(n, 0.0)) <= TOL for n in goal)
    return {"actions": actions, "done": done}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            return self._json({"ok": True, "policy": "reference-proportional"})
        return self._json({"ok": False, "error": "not found"}, 404)

    def do_POST(self):
        if self.path != "/act":
            return self._json({"ok": False, "error": "not found"}, 404)
        n = int(self.headers.get("Content-Length", 0) or 0)
        try:
            obs = json.loads(self.rfile.read(n) or b"{}")
        except Exception as e:
            return self._json({"ok": False, "error": "bad obs: %s" % e}, 400)
        try:
            return self._json(predict(obs.get("state") or {}, obs.get("prompt")))
        except Exception as e:
            return self._json({"ok": False, "error": str(e)}, 500)


def main():
    global GOAL, STEP, CHUNK, TOL
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18093)
    ap.add_argument("--goal", default="", help='JSON joint goal, e.g. {"J1":30,"J2":-20}')
    ap.add_argument("--step", type=float, default=8.0)
    ap.add_argument("--chunk", type=int, default=5)
    ap.add_argument("--tol", type=float, default=1.0)
    args = ap.parse_args()
    if args.goal.strip():
        GOAL = json.loads(args.goal)
    STEP, CHUNK, TOL = args.step, args.chunk, args.tol
    srv = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print("policy: reference server on 127.0.0.1:%d goal=%s" % (args.port, GOAL or "home"), file=sys.stderr)
    srv.serve_forever()


if __name__ == "__main__":
    main()
