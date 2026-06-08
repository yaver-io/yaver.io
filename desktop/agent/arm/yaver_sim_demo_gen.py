#!/usr/bin/env python3
"""yaver sim demo generator — auto-produce imitation-learning DEMONSTRATIONS from
the simulator, so you can train a policy without hand-teleoping hardware.

It drives a running yaver sim harness (sim_harness.py) through a scripted task —
each episode starts at a RANDOM joint pose and drives to a FIXED target — and
records dense {camera frame, joint state} into the same on-disk episode layout the
Go DemoRecorder writes:

    <out>/<task>/episode_NNN/
        meta.json            {name, prompt, fps, dof, joints, frames, ...}
        frames/000001.jpg …
        states.jsonl         {t, joints{name:val}, pose?}

Those episodes are converted to a LeRobotDataset (yaver_lerobot_export.py) and
trained (ACT/Diffusion Policy/SmolVLA). The "reach a fixed target from a random
start" task is deliberately learnable from images+state — it's a PLUMBING PROOF of
the data→train→serve→run pipeline; a real cell records real teleop/free-drive
demos of the actual wire-harness step instead. The format is identical either way.

stdlib only (talks HTTP to the harness).
"""
import argparse
import json
import os
import random
import sys
import time
import urllib.request

random.seed(0)  # reproducible demos


def get(base, path):
    return json.load(urllib.request.urlopen("http://127.0.0.1:%d%s" % (base, path), timeout=10))


def post(base, path, body):
    r = urllib.request.Request("http://127.0.0.1:%d%s" % (base, path),
                               data=json.dumps(body).encode(), headers={"Content-Type": "application/json"})
    return json.load(urllib.request.urlopen(r, timeout=20))


def grab_jpg(base):
    return urllib.request.urlopen("http://127.0.0.1:%d/frame.jpg" % base, timeout=10).read()


def next_episode(base_dir):
    os.makedirs(base_dir, exist_ok=True)
    n = sum(1 for e in os.listdir(base_dir) if e.startswith("episode_"))
    ep = "episode_%03d" % n
    d = os.path.join(base_dir, ep)
    os.makedirs(os.path.join(d, "frames"), exist_ok=True)
    return d, ep


def record_episode(sim_port, ep_dir, joints, target, fps, prompt):
    """Drive from current pose to target in small steps, sampling each step."""
    f = open(os.path.join(ep_dir, "states.jsonl"), "w")
    t0 = time.time()
    frames = 0
    cur = {j["name"]: get(sim_port, "/state")["joints"][i]["position"] for i, j in enumerate(joints)}
    STEP = 6.0
    for _ in range(60):
        # one frame of obs BEFORE moving
        jpg = grab_jpg(sim_port)
        frames += 1
        open(os.path.join(ep_dir, "frames", "%06d.jpg" % frames), "wb").write(jpg)
        st = get(sim_port, "/state")
        f.write(json.dumps({"t": round(time.time() - t0, 3),
                            "joints": {j["name"]: j["position"] for j in st["joints"]},
                            "pose": st.get("pose")}) + "\n")
        # step every joint toward its target
        moved = False
        targets = {}
        for name, tgt in target.items():
            c = cur.get(name, 0.0)
            d = tgt - c
            if abs(d) <= 0.5:
                continue
            mv = max(-STEP, min(STEP, d))
            targets[name] = round(c + mv, 4)
            cur[name] = targets[name]
            moved = True
        if not moved:
            break
        post(sim_port, "/movej", {"targets": targets, "velPct": 70})
    f.close()
    meta = {"name": os.path.basename(os.path.dirname(ep_dir)), "prompt": prompt, "fps": fps,
            "dof": len(joints), "joints": joints, "frames": frames,
            "episode": os.path.basename(ep_dir), "createdAt": int(t0 * 1000)}
    json.dump(meta, open(os.path.join(ep_dir, "meta.json"), "w"), indent=2)
    return frames


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--sim-port", type=int, default=18092)
    ap.add_argument("--out", default=os.path.expanduser("~/.yaver/arm-demos"))
    ap.add_argument("--task", default="reach-target")
    ap.add_argument("--episodes", type=int, default=20)
    ap.add_argument("--fps", type=int, default=10)
    ap.add_argument("--target", default='{"J1":30,"J2":-20}', help="fixed goal the demos reach")
    ap.add_argument("--prompt", default="move to the target pose")
    args = ap.parse_args()

    info = get(args.sim_port, "/describe")
    joints = info["joints"]
    target = json.loads(args.target)
    base_dir = os.path.join(args.out, args.task)
    total = 0
    for e in range(args.episodes):
        # random start within limits
        start = {}
        for j in joints:
            lo, hi = j["min"], j["max"]
            start[j["name"]] = round(random.uniform(max(lo, -90), min(hi, 90)), 2)
        post(args.sim_port, "/reset", {})
        post(args.sim_port, "/movej", {"targets": start, "velPct": 100})
        ep_dir, ep = next_episode(base_dir)
        n = record_episode(args.sim_port, ep_dir, joints, target, args.fps, args.prompt)
        total += n
        print("episode %s: %d frames (start randomized → target %s)" % (ep, n, target), file=sys.stderr)
    print("DONE: %d episodes, %d frames in %s" % (args.episodes, total, base_dir))


if __name__ == "__main__":
    main()
